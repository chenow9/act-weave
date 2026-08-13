package modelconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	// AgenticCapabilitiesSchemaV1 is the native client-search capability schema.
	AgenticCapabilitiesSchemaV1 = "agentic-model.v1"
	// AgenticCapabilitiesSchemaV2 is the function_calling / none / explicit-native schema.
	AgenticCapabilitiesSchemaV2 = "agentic-model.v2"

	// AgenticProtocolOpenAIResponsesV1 is the only accepted OpenAI Responses protocol id.
	AgenticProtocolOpenAIResponsesV1 = "openai-responses-v1"

	// AgenticToolSearchModeClient is the Provider protocol capability for client-executed tool search.
	// Runtime strategy client_bounded is recorded on assembly manifests, not here (D10).
	AgenticToolSearchModeClient = "client"

	ToolCallingNativeClientSearch = "native_client_search"
	ToolCallingFunctionCalling    = "function_calling"
	ToolCallingNone               = "none"

	// AgenticReasoningReplayEncryptedOrNone is the only accepted reasoning-replay policy.
	AgenticReasoningReplayEncryptedOrNone = "encrypted-or-none"

	// VerifiedAdapterAgenticOpenAIV022 is the pinned verified adapter identity/version.
	VerifiedAdapterAgenticOpenAIV022 = "agenticopenai/v0.2.2"

	// canonicalVerifiedAtLayout is exact UTC RFC3339 seconds with Z (no offset, no fraction).
	canonicalVerifiedAtLayout = "2006-01-02T15:04:05Z"
)

// AgenticCapabilities is the strict, verification-owned capability document.
// Empty object "{}" means unverified only. Values are never merged into
// runtime_capabilities or options.
type AgenticCapabilities struct {
	SchemaVersion        string    `json:"schemaVersion,omitempty"`
	Protocol             string    `json:"protocol,omitempty"`
	Streaming            bool      `json:"streaming"`
	Usage                bool      `json:"usage"`
	ToolCalling          string    `json:"toolCalling,omitempty"`
	ToolSearchModes      []string  `json:"toolSearchModes,omitempty"`
	ReasoningReplay      string    `json:"reasoningReplay,omitempty"`
	VerifiedAdapter      string    `json:"verifiedAdapter,omitempty"`
	VerifiedAt           time.Time `json:"verifiedAt,omitempty"`
	VerifiedLockVersion  int64     `json:"verifiedLockVersion,omitempty"`
	VerifiedConfigDigest string    `json:"verifiedConfigDigest,omitempty"`
}

// MarshalJSON emits verifiedAt as exact canonical UTC seconds (`…Z`) without
// fractional seconds or numeric offsets. Other fields use standard encoding.
func (a AgenticCapabilities) MarshalJSON() ([]byte, error) {
	type wire struct {
		SchemaVersion        string   `json:"schemaVersion,omitempty"`
		Protocol             string   `json:"protocol,omitempty"`
		Streaming            bool     `json:"streaming"`
		Usage                bool     `json:"usage"`
		ToolCalling          string   `json:"toolCalling,omitempty"`
		ToolSearchModes      []string `json:"toolSearchModes,omitempty"`
		ReasoningReplay      string   `json:"reasoningReplay,omitempty"`
		VerifiedAdapter      string   `json:"verifiedAdapter,omitempty"`
		VerifiedAt           string   `json:"verifiedAt,omitempty"`
		VerifiedLockVersion  int64    `json:"verifiedLockVersion,omitempty"`
		VerifiedConfigDigest string   `json:"verifiedConfigDigest,omitempty"`
	}
	w := wire{
		SchemaVersion:        a.SchemaVersion,
		Protocol:             a.Protocol,
		Streaming:            a.Streaming,
		Usage:                a.Usage,
		ToolSearchModes:      a.ToolSearchModes,
		ReasoningReplay:      a.ReasoningReplay,
		VerifiedAdapter:      a.VerifiedAdapter,
		VerifiedLockVersion:  a.VerifiedLockVersion,
		VerifiedConfigDigest: a.VerifiedConfigDigest,
	}
	// v1 normalize must stay v1 bytes: never emit the in-memory toolCalling fill.
	if a.SchemaVersion == AgenticCapabilitiesSchemaV2 {
		w.ToolCalling = a.ToolCalling
	}
	if !a.VerifiedAt.IsZero() {
		w.VerifiedAt = a.VerifiedAt.UTC().Truncate(time.Second).Format(canonicalVerifiedAtLayout)
	}
	return json.Marshal(w)
}

// CanonicalAgenticCapabilities builds the only accepted successful verification document.
// verifiedAt is normalized to UTC second precision on construction; lockVersion must be >= 1;
// configDigest must be lowercase 64-hex (no whitespace).
func CanonicalAgenticCapabilities(verifiedAt time.Time, lockVersion int64, configDigest string) (AgenticCapabilities, error) {
	if verifiedAt.IsZero() {
		return AgenticCapabilities{}, fmt.Errorf("%w: verifiedAt is required", ErrInvalid)
	}
	if lockVersion < 1 {
		return AgenticCapabilities{}, fmt.Errorf("%w: verifiedLockVersion must be >= 1", ErrInvalid)
	}
	if !isHex64(configDigest) {
		return AgenticCapabilities{}, fmt.Errorf("%w: verifiedConfigDigest must be 64 lowercase hex chars", ErrInvalid)
	}
	return AgenticCapabilities{
		SchemaVersion:        AgenticCapabilitiesSchemaV1,
		Protocol:             AgenticProtocolOpenAIResponsesV1,
		Streaming:            true,
		Usage:                true,
		ToolSearchModes:      []string{AgenticToolSearchModeClient},
		ReasoningReplay:      AgenticReasoningReplayEncryptedOrNone,
		VerifiedAdapter:      VerifiedAdapterAgenticOpenAIV022,
		VerifiedAt:           verifiedAt.UTC().Truncate(time.Second),
		VerifiedLockVersion:  lockVersion,
		VerifiedConfigDigest: configDigest,
	}, nil
}

// AgenticCapabilityLockMatches reports whether a verified capability document
// belongs to the config row (or frozen snapshot) carrying configLockVersion.
//
// Verification CAS stamps verifiedLockVersion = pre-CAS lock and then writes
// lock_version = lock_version + 1 (see Repository.RecordVerification), so a
// readable VERIFIED row always satisfies verifiedLockVersion == lockVersion-1.
// Frozen model snapshots copy lock_version verbatim, so freeze consumers must
// use the same relation — comparing for plain equality makes every real
// production config look stale and is what made the Agentic initial path
// unreachable outside test fixtures.
func AgenticCapabilityLockMatches(doc AgenticCapabilities, configLockVersion int64) bool {
	return doc.VerifiedLockVersion >= 1 && configLockVersion >= 2 &&
		doc.VerifiedLockVersion == configLockVersion-1
}

// ParseAgenticCapabilities validates and normalizes a raw JSON object.
// Empty object or empty/nil raw means "unverified" and returns a zero value with raw `{}`.
// JSON null is rejected (only absent/default `{}` means unverified).
// Unknown fields, duplicate JSON keys, wrong types/nulls, duplicate/unknown modes,
// noncanonical values, missing required fields, and non-canonical timestamps fail closed.
// Timestamps must be exact UTC RFC3339 seconds (`YYYY-MM-DDTHH:MM:SSZ`); no silent normalize.
func ParseAgenticCapabilities(raw json.RawMessage) (AgenticCapabilities, json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return AgenticCapabilities{}, json.RawMessage(`{}`), nil
	}
	// JSON null is not "unverified" — only absent/default raw `{}` is.
	if bytes.Equal(raw, []byte("null")) {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: agenticCapabilities must not be JSON null", ErrInvalid)
	}
	if !json.Valid(raw) {
		return AgenticCapabilities{}, nil, ErrInvalid
	}
	// Fail closed on non-object roots (arrays, strings, numbers, bool).
	if raw[0] != '{' {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: agenticCapabilities must be a JSON object", ErrInvalid)
	}

	// Reject duplicate keys at any nesting level before decode.
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		return AgenticCapabilities{}, nil, ErrInvalid
	}
	if len(top) == 0 {
		return AgenticCapabilities{}, json.RawMessage(`{}`), nil
	}

	schemaVersion, err := parseRequiredJSONString(top["schemaVersion"])
	if err != nil {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: missing or invalid agenticCapabilities field %q", ErrInvalid, "schemaVersion")
	}

	allowed := map[string]struct{}{
		"schemaVersion":        {},
		"protocol":             {},
		"streaming":            {},
		"usage":                {},
		"toolSearchModes":      {},
		"reasoningReplay":      {},
		"verifiedAdapter":      {},
		"verifiedAt":           {},
		"verifiedLockVersion":  {},
		"verifiedConfigDigest": {},
	}
	if schemaVersion == AgenticCapabilitiesSchemaV2 {
		allowed["toolCalling"] = struct{}{}
	}
	for key := range top {
		if _, ok := allowed[key]; !ok {
			return AgenticCapabilities{}, nil, fmt.Errorf("%w: unknown agenticCapabilities field %q", ErrInvalid, key)
		}
	}

	required := []string{
		"schemaVersion", "protocol", "streaming", "usage",
		"reasoningReplay", "verifiedAdapter",
		"verifiedAt", "verifiedLockVersion", "verifiedConfigDigest",
	}
	switch schemaVersion {
	case AgenticCapabilitiesSchemaV1:
		required = append(required, "toolSearchModes")
	case AgenticCapabilitiesSchemaV2:
		required = append(required, "toolCalling")
	default:
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: unsupported agenticCapabilities schemaVersion", ErrInvalid)
	}
	for _, key := range required {
		if _, ok := top[key]; !ok {
			return AgenticCapabilities{}, nil, fmt.Errorf("%w: missing agenticCapabilities field %q", ErrInvalid, key)
		}
	}

	for _, key := range required {
		if bytes.Equal(bytes.TrimSpace(top[key]), []byte("null")) {
			return AgenticCapabilities{}, nil, fmt.Errorf("%w: agenticCapabilities field %q must not be null", ErrInvalid, key)
		}
	}
	if rawModes, ok := top["toolSearchModes"]; ok && bytes.Equal(bytes.TrimSpace(rawModes), []byte("null")) {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: agenticCapabilities field %q must not be null", ErrInvalid, "toolSearchModes")
	}

	// Strict verifiedAt: exact UTC seconds with Z before any time.Time decode.
	verifiedAt, err := parseCanonicalVerifiedAt(top["verifiedAt"])
	if err != nil {
		return AgenticCapabilities{}, nil, err
	}

	// Decode remaining fields with unknown-field rejection (verifiedAt re-checked below).
	var doc AgenticCapabilities
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if dec.More() {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: trailing data after agenticCapabilities", ErrInvalid)
	}
	// Overwrite with the strictly-validated timestamp (no silent offset/fraction normalize).
	doc.VerifiedAt = verifiedAt

	// No whitespace/case normalization on string fields — reject non-canonical forms.
	if doc.SchemaVersion != schemaVersion {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: unsupported agenticCapabilities schemaVersion", ErrInvalid)
	}
	if doc.Protocol != AgenticProtocolOpenAIResponsesV1 {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: unsupported agenticCapabilities protocol", ErrInvalid)
	}
	if !doc.Streaming {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: streaming must be true", ErrInvalid)
	}
	if !doc.Usage {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: usage must be true", ErrInvalid)
	}
	if doc.ReasoningReplay != AgenticReasoningReplayEncryptedOrNone {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: unsupported reasoningReplay", ErrInvalid)
	}
	if doc.VerifiedAdapter != VerifiedAdapterAgenticOpenAIV022 {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: unsupported verifiedAdapter", ErrInvalid)
	}
	if doc.VerifiedAt.IsZero() {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: verifiedAt is required", ErrInvalid)
	}
	if doc.VerifiedLockVersion < 1 {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: verifiedLockVersion must be >= 1", ErrInvalid)
	}
	// Digest: exact lowercase 64-hex; no ToLower/trim normalization on read.
	if !isHex64(doc.VerifiedConfigDigest) {
		return AgenticCapabilities{}, nil, fmt.Errorf("%w: verifiedConfigDigest must be 64 lowercase hex chars", ErrInvalid)
	}

	if err := applyAgenticToolCallingRules(&doc); err != nil {
		return AgenticCapabilities{}, nil, err
	}

	normalized, err := json.Marshal(doc)
	if err != nil {
		return AgenticCapabilities{}, nil, ErrInvalid
	}
	return doc, json.RawMessage(normalized), nil
}

// parseCanonicalVerifiedAt requires a JSON string equal to YYYY-MM-DDTHH:MM:SSZ.
// Offsets (+00:00), fractional seconds, and non-Z spellings are rejected (no normalize-on-read).
func parseCanonicalVerifiedAt(raw json.RawMessage) (time.Time, error) {
	raw = bytes.TrimSpace(raw)
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return time.Time{}, fmt.Errorf("%w: verifiedAt must be a JSON string", ErrInvalid)
	}
	if !isCanonicalVerifiedAtString(s) {
		return time.Time{}, fmt.Errorf("%w: verifiedAt must be exact UTC RFC3339 seconds (…Z)", ErrInvalid)
	}
	t, err := time.Parse(canonicalVerifiedAtLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: verifiedAt must be exact UTC RFC3339 seconds (…Z)", ErrInvalid)
	}
	return t, nil
}

func isCanonicalVerifiedAtString(s string) bool {
	if len(s) != len(canonicalVerifiedAtLayout) {
		return false
	}
	t, err := time.Parse(canonicalVerifiedAtLayout, s)
	if err != nil {
		return false
	}
	// Round-trip must be identical (rejects noncanonical zero-pad / case variants if any).
	return t.UTC().Format(canonicalVerifiedAtLayout) == s
}

// NormalizeAgenticCapabilitiesRaw returns canonical JSON for storage.
// Empty / unset becomes `{}`. Invalid documents (including JSON null) return ErrInvalid.
func NormalizeAgenticCapabilitiesRaw(raw json.RawMessage) (json.RawMessage, error) {
	_, normalized, err := ParseAgenticCapabilities(raw)
	return normalized, err
}

// IsUnverifiedAgenticCapabilities reports whether raw is the unset/unverified form.
// Only empty/absent or exact `{}` count; JSON null is not unverified.
func IsUnverifiedAgenticCapabilities(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || bytes.Equal(raw, []byte("{}"))
}

// WireConfigDigest returns a secret-free SHA-256 hex identity over fields that
// can change wire behavior. Used to detect capability staleness vs current config.
// Never includes credential plaintext; only the optional secret id binding.
//
// The JSON members are compacted before hashing. The same document reaches this
// function in two different byte shapes: straight off a jsonb column
// (`{"a": 1, "b": 2}`, Postgres spacing) and out of a frozen run snapshot, where
// encoding/json compacted the embedded RawMessage (`{"a":1,"b":2}`). Hashing raw
// bytes made the freeze change the digest, so verification evidence recorded on
// the live row never matched the same config read back from the freeze.
// Compaction is byte-shape only: member order and values are untouched, so two
// genuinely different configs still produce different digests.
func WireConfigDigest(config Config) string {
	cred := ""
	if config.CredentialSecretID != nil {
		cred = strings.TrimSpace(*config.CredentialSecretID)
	}
	payload := strings.Join([]string{
		strings.TrimSpace(config.Provider),
		strings.TrimSpace(config.APIBase),
		strings.TrimSpace(config.ModelName),
		compactJSONForDigest(config.Options),
		compactJSONForDigest(config.RuntimeCapabilities),
		cred,
		VerifiedAdapterAgenticOpenAIV022,
	}, "|")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// compactJSONForDigest removes insignificant whitespace. Invalid or empty input
// degrades to the trimmed original so a malformed column can never collide with
// a well-formed one.
func compactJSONForDigest(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "{}"
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, trimmed); err != nil {
		return string(trimmed)
	}
	return buf.String()
}

func validateToolSearchModes(modes []string) error {
	if len(modes) != 1 {
		return fmt.Errorf("%w: toolSearchModes must be exactly [\"client\"]", ErrInvalid)
	}
	if modes[0] != AgenticToolSearchModeClient {
		return fmt.Errorf("%w: unknown toolSearchMode %q", ErrInvalid, modes[0])
	}
	return nil
}

func applyAgenticToolCallingRules(doc *AgenticCapabilities) error {
	if doc == nil {
		return ErrInvalid
	}
	switch doc.SchemaVersion {
	case AgenticCapabilitiesSchemaV1:
		if err := validateToolSearchModes(doc.ToolSearchModes); err != nil {
			return err
		}
		doc.ToolSearchModes = []string{AgenticToolSearchModeClient}
		// Memory fill only; MarshalJSON omits toolCalling for v1.
		doc.ToolCalling = ToolCallingNativeClientSearch
		return nil
	case AgenticCapabilitiesSchemaV2:
		switch doc.ToolCalling {
		case ToolCallingNativeClientSearch:
			if err := validateToolSearchModes(doc.ToolSearchModes); err != nil {
				return err
			}
			doc.ToolSearchModes = []string{AgenticToolSearchModeClient}
			return nil
		case ToolCallingFunctionCalling, ToolCallingNone:
			if len(doc.ToolSearchModes) != 0 {
				return fmt.Errorf("%w: toolSearchModes must be omitted for toolCalling %q", ErrInvalid, doc.ToolCalling)
			}
			doc.ToolSearchModes = nil
			return nil
		default:
			return fmt.Errorf("%w: unsupported toolCalling", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unsupported agenticCapabilities schemaVersion", ErrInvalid)
	}
}

func parseRequiredJSONString(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", ErrInvalid
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", ErrInvalid
	}
	if s == "" {
		return "", ErrInvalid
	}
	return s, nil
}

func isHex64(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, c := range v {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// rejectDuplicateJSONKeys scans JSON and rejects duplicate object keys at any level.
func rejectDuplicateJSONKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	return rejectDupValue(dec)
}

func rejectDupValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("object key must be a string")
			}
			if _, dup := seen[key]; dup {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := rejectDupValue(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := end.(json.Delim); !ok || d != '}' {
			return fmt.Errorf("expected end of object")
		}
		return nil
	case '[':
		for dec.More() {
			if err := rejectDupValue(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := end.(json.Delim); !ok || d != ']' {
			return fmt.Errorf("expected end of array")
		}
		return nil
	default:
		return fmt.Errorf("unexpected delimiter %v", delim)
	}
}
