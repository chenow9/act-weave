package outboundidentity

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

// Envelope size limits from technical design §5.1.
const (
	MaxBindingsPerEnvelope = 32
	MaxTokenBytes          = 16 * 1024
	MaxEnvelopeSecretBytes = 128 * 1024
)

// CredentialsEnvelope is the domain shape of outbound-credentials.v1.
//
// Transport plaintext decoding and Vault attach live in checklist item 7.
// This package only validates schema shape, uniqueness, sizes, control
// characters, required expiresAt (T3=A), and forbids caller-supplied subject /
// workspace / mode / header / origin fields via strict JSON decoding.
//
// Value is intentionally write-only domain material: it is never serialized by
// MarshalJSON and must be zeroed by callers after attach/cleanup. Response DTOs
// must never include Value.
type CredentialsEnvelope struct {
	SchemaVersion string              `json:"schemaVersion"`
	Bindings      []CredentialBinding `json:"bindings"`
}

// CredentialBinding is one Connection-scoped passthrough credential.
// Forbidden caller fields (subject, workspaceId, rootId, mode, header, origin,
// locator, vaultKey) are rejected by DisallowUnknownFields on decode.
type CredentialBinding struct {
	ConnectionID   string         `json:"connectionId"`
	CredentialType CredentialType `json:"credentialType"`
	// Value is write-only secret material for request decoding/validation only.
	// It must never appear in responses, requirements, plans, logs, or audit.
	Value     []byte    `json:"value"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// credentialsEnvelopeWire is used only for strict JSON decode so that value is
// accepted as a string and converted to a mutable byte slice for zeroing later.
type credentialsEnvelopeWire struct {
	SchemaVersion string                  `json:"schemaVersion"`
	Bindings      []credentialBindingWire `json:"bindings"`
}

type credentialBindingWire struct {
	ConnectionID   string `json:"connectionId"`
	CredentialType string `json:"credentialType"`
	Value          string `json:"value"`
	ExpiresAt      string `json:"expiresAt"`
}

// ParseCredentialsEnvelope decodes and validates outbound-credentials.v1.
// On failure, any partially decoded Value bytes are zeroed before return.
func ParseCredentialsEnvelope(raw json.RawMessage) (CredentialsEnvelope, error) {
	var wire credentialsEnvelopeWire
	if err := decodeStrictJSON(raw, &wire); err != nil {
		return CredentialsEnvelope{}, ErrCredentialInvalid.Wrap(err)
	}
	envelope, err := normalizeCredentialsWire(wire)
	if err != nil {
		zeroCredentialsEnvelope(&envelope)
		return CredentialsEnvelope{}, err
	}
	return envelope, nil
}

// NormalizeCredentialsEnvelope validates a domain envelope and returns a clone.
func NormalizeCredentialsEnvelope(envelope CredentialsEnvelope) (CredentialsEnvelope, error) {
	if strings.TrimSpace(envelope.SchemaVersion) != SchemaCredentials {
		return CredentialsEnvelope{}, ErrCredentialInvalid
	}
	if len(envelope.Bindings) == 0 {
		// Empty envelope is allowed only when omitted entirely by callers; an
		// explicit empty bindings array is treated as invalid to avoid ambiguity.
		return CredentialsEnvelope{}, ErrCredentialInvalid
	}
	if len(envelope.Bindings) > MaxBindingsPerEnvelope {
		return CredentialsEnvelope{}, ErrCredentialInvalid
	}
	normalized := make([]CredentialBinding, 0, len(envelope.Bindings))
	seen := map[string]struct{}{}
	totalBytes := 0
	for _, binding := range envelope.Bindings {
		connectionID := strings.TrimSpace(binding.ConnectionID)
		if connectionID == "" || len(connectionID) > 64 {
			return CredentialsEnvelope{}, ErrCredentialInvalid
		}
		if _, dup := seen[connectionID]; dup {
			return CredentialsEnvelope{}, ErrCredentialInvalid
		}
		seen[connectionID] = struct{}{}
		credentialType := CredentialType(strings.TrimSpace(string(binding.CredentialType)))
		if !credentialType.Valid() {
			return CredentialsEnvelope{}, ErrCredentialInvalid
		}
		if len(binding.Value) == 0 || len(binding.Value) > MaxTokenBytes {
			return CredentialsEnvelope{}, ErrCredentialInvalid
		}
		if !utf8.Valid(binding.Value) || containsControlBytes(binding.Value) {
			return CredentialsEnvelope{}, ErrCredentialInvalid
		}
		totalBytes += len(binding.Value)
		if totalBytes > MaxEnvelopeSecretBytes {
			return CredentialsEnvelope{}, ErrCredentialCapacityExceeded
		}
		// T3=A: expiresAt is required and must be in the future relative to validation
		// time supplied by callers via ValidateCredentialsEnvelopeExpiry.
		if binding.ExpiresAt.IsZero() {
			return CredentialsEnvelope{}, ErrCredentialInvalid
		}
		valueCopy := append([]byte(nil), binding.Value...)
		normalized = append(normalized, CredentialBinding{
			ConnectionID:   connectionID,
			CredentialType: credentialType,
			Value:          valueCopy,
			ExpiresAt:      binding.ExpiresAt.UTC(),
		})
	}
	return CredentialsEnvelope{
		SchemaVersion: SchemaCredentials,
		Bindings:      normalized,
	}, nil
}

// ValidateCredentialsEnvelopeExpiry rejects expired bindings at the given now.
func ValidateCredentialsEnvelopeExpiry(envelope CredentialsEnvelope, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	for _, binding := range envelope.Bindings {
		if !binding.ExpiresAt.After(now) {
			return ErrCredentialInvalid
		}
	}
	return nil
}

// ValidateCredentialsAgainstRequirements ensures every binding targets an
// allowlisted REQUEST_PASSTHROUGH connection and that required bindings exist.
// It does not read Token values beyond presence already validated.
func ValidateCredentialsAgainstRequirements(envelope CredentialsEnvelope, requirements Requirements) error {
	normalizedEnvelope, err := NormalizeCredentialsEnvelope(envelope)
	if err != nil {
		zeroCredentialsEnvelope(&normalizedEnvelope)
		return err
	}
	// Do not leave normalized copies of plaintext after validation when failing later.
	defer zeroCredentialsEnvelope(&normalizedEnvelope)

	normalizedRequirements, err := NormalizeRequirements(requirements)
	if err != nil {
		return err
	}
	bound := map[string]struct{}{}
	for _, binding := range normalizedEnvelope.Bindings {
		req, ok := normalizedRequirements.Lookup(binding.ConnectionID)
		if !ok {
			return ErrCredentialTargetMismatch
		}
		if req.Mode != ModeRequestPassthrough {
			return ErrCredentialTargetMismatch
		}
		bound[binding.ConnectionID] = struct{}{}
	}
	for _, req := range normalizedRequirements.Connections {
		if req.Mode == ModeRequestPassthrough && req.CredentialRequired {
			if _, ok := bound[req.ConnectionID]; !ok {
				return ErrCredentialRequired
			}
		}
	}
	return nil
}

// CloneCredentialsEnvelope deep-copies bindings including Value bytes.
// Callers must zero the clone when finished.
func CloneCredentialsEnvelope(envelope CredentialsEnvelope) CredentialsEnvelope {
	cloned := CredentialsEnvelope{SchemaVersion: envelope.SchemaVersion}
	if len(envelope.Bindings) == 0 {
		return cloned
	}
	cloned.Bindings = make([]CredentialBinding, len(envelope.Bindings))
	for i, binding := range envelope.Bindings {
		cloned.Bindings[i] = CredentialBinding{
			ConnectionID:   binding.ConnectionID,
			CredentialType: binding.CredentialType,
			Value:          append([]byte(nil), binding.Value...),
			ExpiresAt:      binding.ExpiresAt,
		}
	}
	return cloned
}

// ZeroCredentialsEnvelope overwrites and drops all Value bytes.
func ZeroCredentialsEnvelope(envelope *CredentialsEnvelope) {
	zeroCredentialsEnvelope(envelope)
}

// MarshalJSON for CredentialsEnvelope intentionally omits Value so accidental
// encoding cannot leak plaintext into logs, events, or DTOs.
func (e CredentialsEnvelope) MarshalJSON() ([]byte, error) {
	type bindingDTO struct {
		ConnectionID   string         `json:"connectionId"`
		CredentialType CredentialType `json:"credentialType"`
		ExpiresAt      time.Time      `json:"expiresAt"`
		Provided       bool           `json:"provided"`
	}
	type envelopeDTO struct {
		SchemaVersion string       `json:"schemaVersion"`
		Bindings      []bindingDTO `json:"bindings"`
	}
	dto := envelopeDTO{SchemaVersion: e.SchemaVersion}
	for _, binding := range e.Bindings {
		dto.Bindings = append(dto.Bindings, bindingDTO{
			ConnectionID:   binding.ConnectionID,
			CredentialType: binding.CredentialType,
			ExpiresAt:      binding.ExpiresAt,
			// Never encode the token; only acknowledge presence for diagnostics.
			// ExpiresAt is still sensitive in some contexts — transport responses
			// must not reuse this method. Domain canary tests assert Value absent.
			Provided: len(binding.Value) > 0,
		})
	}
	return json.Marshal(dto)
}

func normalizeCredentialsWire(wire credentialsEnvelopeWire) (CredentialsEnvelope, error) {
	if strings.TrimSpace(wire.SchemaVersion) != SchemaCredentials {
		return CredentialsEnvelope{}, ErrCredentialInvalid
	}
	if len(wire.Bindings) == 0 || len(wire.Bindings) > MaxBindingsPerEnvelope {
		return CredentialsEnvelope{}, ErrCredentialInvalid
	}
	bindings := make([]CredentialBinding, 0, len(wire.Bindings))
	seen := map[string]struct{}{}
	totalBytes := 0
	for _, item := range wire.Bindings {
		connectionID := strings.TrimSpace(item.ConnectionID)
		if connectionID == "" || len(connectionID) > 64 {
			return CredentialsEnvelope{}, ErrCredentialInvalid
		}
		if _, dup := seen[connectionID]; dup {
			return CredentialsEnvelope{}, ErrCredentialInvalid
		}
		seen[connectionID] = struct{}{}
		credentialType := CredentialType(strings.TrimSpace(item.CredentialType))
		if !credentialType.Valid() {
			return CredentialsEnvelope{}, ErrCredentialInvalid
		}
		if item.Value == "" || len(item.Value) > MaxTokenBytes {
			return CredentialsEnvelope{}, ErrCredentialInvalid
		}
		value := []byte(item.Value)
		if !utf8.Valid(value) || containsControlBytes(value) {
			// Clear residual string backing by zeroing our copy.
			for i := range value {
				value[i] = 0
			}
			return CredentialsEnvelope{}, ErrCredentialInvalid
		}
		totalBytes += len(value)
		if totalBytes > MaxEnvelopeSecretBytes {
			for i := range value {
				value[i] = 0
			}
			return CredentialsEnvelope{}, ErrCredentialCapacityExceeded
		}
		if strings.TrimSpace(item.ExpiresAt) == "" {
			for i := range value {
				value[i] = 0
			}
			return CredentialsEnvelope{}, ErrCredentialInvalid
		}
		expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(item.ExpiresAt))
		if err != nil {
			// Also accept RFC3339Nano.
			expiresAt, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(item.ExpiresAt))
			if err != nil {
				for i := range value {
					value[i] = 0
				}
				return CredentialsEnvelope{}, ErrCredentialInvalid
			}
		}
		bindings = append(bindings, CredentialBinding{
			ConnectionID:   connectionID,
			CredentialType: credentialType,
			Value:          value,
			ExpiresAt:      expiresAt.UTC(),
		})
	}
	return CredentialsEnvelope{
		SchemaVersion: SchemaCredentials,
		Bindings:      bindings,
	}, nil
}

func containsControlBytes(value []byte) bool {
	for _, b := range value {
		if b == 0 || b == '\r' || b == '\n' {
			return true
		}
	}
	return false
}

func zeroCredentialsEnvelope(envelope *CredentialsEnvelope) {
	if envelope == nil {
		return
	}
	for i := range envelope.Bindings {
		for j := range envelope.Bindings[i].Value {
			envelope.Bindings[i].Value[j] = 0
		}
		envelope.Bindings[i].Value = nil
	}
	envelope.Bindings = nil
}
