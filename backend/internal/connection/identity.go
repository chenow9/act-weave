package connection

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/outboundidentity"
)

// Migration and dual-mode identity fields on Connection (checklist #3).
const (
	MigrationStateNone              = outboundidentity.MigrationStateNone
	MigrationStateMigrationRequired = outboundidentity.MigrationStateMigrationRequired
)

// IdentityWrite is the dual-mode Connection write surface. Legacy AuthMode /
// AuthConfig / CredentialSecretID must not be supplied for HTTP_OPENAPI targets.
type IdentityWrite struct {
	Mode                      outboundidentity.Mode
	BrokerOBO                 *outboundidentity.ConnectionBrokerOBO
	RequestPassthrough        *outboundidentity.ConnectionPassthrough
	MachineCredentialSecretID *string
	// Metadata-only fields may be written by EDITOR; identity fields require MANAGE.
	MetadataOnly bool
}

// ImpactChangeKind enumerates dangerous mutations that require proof.
type ImpactChangeKind string

const (
	ImpactChangeMode              ImpactChangeKind = "MODE"
	ImpactChangeInjectionOrOrigin ImpactChangeKind = "INJECTION_OR_ORIGIN"
	ImpactChangeMachineCredential ImpactChangeKind = "MACHINE_CREDENTIAL"
	ImpactChangeDisable           ImpactChangeKind = "DISABLE"
	ImpactChangeDelete            ImpactChangeKind = "DELETE"
	ImpactChangeLegacyMigrate     ImpactChangeKind = "LEGACY_MIGRATE"
)

// ImpactPreviewRequest is the non-secret preview body.
type ImpactPreviewRequest struct {
	ChangeKind                  ImpactChangeKind
	NonSecretChangeDescriptor   map[string]any
	MachineCredentialWillChange bool
	ExpectedLockVersion         int64
	ActorID                     string
	WorkspaceID                 string
	ConnectionID                string
	CurrentPolicyVersion        int64
}

// ImpactPreviewResult is returned by :impact. Proof is not a Secret but must
// not be logged or persisted outside the mutation request.
type ImpactPreviewResult struct {
	AffectedPublishedTools    int
	AffectedAgentBindings     int
	AffectedWorkflowRevisions int
	ImpactConfirmationProof   string
	ExpiresAt                 time.Time
	ImpactSetVersion          int64
}

// ImpactProofPayload is the signed proof content.
type ImpactProofPayload struct {
	WorkspaceID      string           `json:"workspaceId"`
	ConnectionID     string           `json:"connectionId"`
	ActorID          string           `json:"actorId"`
	ChangeKind       ImpactChangeKind `json:"changeKind"`
	DescriptorHash   string           `json:"descriptorHash"`
	LockVersion      int64            `json:"lockVersion"`
	PolicyVersion    int64            `json:"policyVersion"`
	ImpactSetVersion int64            `json:"impactSetVersion"`
	ExpiresAtUnix    int64            `json:"expiresAtUnix"`
}

// ImpactProofService signs and verifies impact proofs (HMAC, 5 minute TTL).
type ImpactProofService struct {
	secret []byte
	now    func() time.Time
	ttl    time.Duration
}

func NewImpactProofService(secret []byte) (*ImpactProofService, error) {
	if len(secret) < 32 {
		return nil, errors.New("impact proof secret must contain at least 32 bytes")
	}
	return &ImpactProofService{
		secret: append([]byte(nil), secret...),
		now:    time.Now,
		ttl:    5 * time.Minute,
	}, nil
}

func (s *ImpactProofService) WithClock(now func() time.Time) *ImpactProofService {
	if now != nil {
		s.now = now
	}
	return s
}

func (s *ImpactProofService) Issue(payload ImpactProofPayload) (proof string, expiresAt time.Time, err error) {
	if s == nil {
		return "", time.Time{}, errors.New("impact proof service is required")
	}
	now := s.now().UTC()
	expiresAt = now.Add(s.ttl)
	payload.ExpiresAtUnix = expiresAt.Unix()
	body, err := json.Marshal(payload)
	if err != nil {
		return "", time.Time{}, err
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(body)
	sig := mac.Sum(nil)
	proof = base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(sig)
	return proof, expiresAt, nil
}

func (s *ImpactProofService) Verify(
	proof string,
	expected ImpactProofPayload,
) error {
	if s == nil {
		return outboundidentity.ErrIdentityChangeConfirmationRequired
	}
	parts := strings.Split(proof, ".")
	if len(parts) != 2 {
		return outboundidentity.ErrIdentityChangeConfirmationRequired
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return outboundidentity.ErrIdentityChangeConfirmationRequired
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return outboundidentity.ErrIdentityChangeConfirmationRequired
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return outboundidentity.ErrIdentityChangeConfirmationRequired
	}
	var payload ImpactProofPayload
	if json.Unmarshal(body, &payload) != nil {
		return outboundidentity.ErrIdentityChangeConfirmationRequired
	}
	now := s.now().UTC()
	if payload.ExpiresAtUnix <= now.Unix() {
		return outboundidentity.ErrIdentityChangeConfirmationStale
	}
	if payload.WorkspaceID != expected.WorkspaceID ||
		payload.ConnectionID != expected.ConnectionID ||
		payload.ActorID != expected.ActorID ||
		payload.ChangeKind != expected.ChangeKind ||
		payload.DescriptorHash != expected.DescriptorHash ||
		payload.LockVersion != expected.LockVersion ||
		payload.PolicyVersion != expected.PolicyVersion ||
		payload.ImpactSetVersion != expected.ImpactSetVersion {
		return outboundidentity.ErrIdentityChangeConfirmationStale
	}
	return nil
}

// HashChangeDescriptor produces a stable hash of the non-secret change descriptor.
func HashChangeDescriptor(descriptor map[string]any) (string, error) {
	if descriptor == nil {
		descriptor = map[string]any{}
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// BuildConnectionIdentity constructs a normalized ConnectionIdentity for persistence.
// PolicyVersion is server-assigned; zero means "assign on write".
func BuildConnectionIdentity(write IdentityWrite, policyVersion int64) (outboundidentity.ConnectionIdentity, error) {
	identity := outboundidentity.ConnectionIdentity{
		SchemaVersion: outboundidentity.SchemaConnection,
		Mode:          write.Mode,
		PolicyVersion: policyVersion,
	}
	switch write.Mode {
	case outboundidentity.ModeBrokerOBO:
		if write.BrokerOBO == nil || write.RequestPassthrough != nil {
			return outboundidentity.ConnectionIdentity{}, outboundidentity.ErrIdentityPolicyInvalid
		}
		identity.BrokerOBO = write.BrokerOBO
	case outboundidentity.ModeRequestPassthrough:
		if write.RequestPassthrough == nil || write.BrokerOBO != nil {
			return outboundidentity.ConnectionIdentity{}, outboundidentity.ErrIdentityPolicyInvalid
		}
		identity.RequestPassthrough = write.RequestPassthrough
	default:
		return outboundidentity.ConnectionIdentity{}, outboundidentity.ErrIdentityModeUnsupported
	}
	return outboundidentity.NormalizeConnectionIdentity(identity)
}

// ValidateIdentityWrite enforces dual-mode rules against Provider identity.
func ValidateIdentityWrite(
	write IdentityWrite,
	providerIdentity outboundidentity.ProviderIdentity,
) error {
	identity, err := BuildConnectionIdentity(write, 1)
	if err != nil {
		return err
	}
	machineConfigured := write.MachineCredentialSecretID != nil &&
		strings.TrimSpace(*write.MachineCredentialSecretID) != ""
	if err := outboundidentity.ValidateConnectionAgainstProvider(identity, providerIdentity, machineConfigured); err != nil {
		return err
	}
	// REQUEST_PASSTHROUGH must not carry a business credential secret either;
	// callers must leave legacy credential_secret_id nil (enforced at transport).
	return nil
}

// Executable reports whether a Connection may run under dual-mode rules.
func (c Connection) Executable() bool {
	return c.Status == StatusVerified &&
		c.MigrationState == MigrationStateNone &&
		c.DeletedAt == nil &&
		c.OutboundIdentity != nil
}

// RequiresMigration reports the hard-cut dual-state.
func (c Connection) RequiresMigration() bool {
	return c.MigrationState == MigrationStateMigrationRequired
}

// ParseStoredOutboundIdentity decodes nullable JSONB.
func ParseStoredOutboundIdentity(raw json.RawMessage) (*outboundidentity.ConnectionIdentity, error) {
	if len(strings.TrimSpace(string(raw))) == 0 || string(raw) == "null" {
		return nil, nil
	}
	identity, err := outboundidentity.ParseConnectionIdentity(raw)
	if err != nil {
		return nil, err
	}
	return &identity, nil
}

// MarshalStoredOutboundIdentity encodes for JSONB; policyVersion is omitted from
// persistence JSON per technical design (column is source of truth) but may be
// present in API DTOs as read-only.
func MarshalStoredOutboundIdentity(identity outboundidentity.ConnectionIdentity) (json.RawMessage, error) {
	// Persist without policyVersion so clients cannot treat JSON as authority.
	type persistBroker struct {
		ClientID           string   `json:"clientId"`
		Scopes             []string `json:"scopes"`
		MaxTokenTTLSeconds int      `json:"maxTokenTtlSeconds"`
	}
	type persistPassthrough struct {
		MaxResidenceSeconds int `json:"maxResidenceSeconds"`
	}
	type persist struct {
		SchemaVersion      string                `json:"schemaVersion"`
		Mode               outboundidentity.Mode `json:"mode"`
		BrokerOBO          *persistBroker        `json:"brokerObo,omitempty"`
		RequestPassthrough *persistPassthrough   `json:"requestPassthrough,omitempty"`
	}
	normalized, err := outboundidentity.NormalizeConnectionIdentity(identity)
	if err != nil {
		return nil, err
	}
	body := persist{SchemaVersion: outboundidentity.SchemaConnection, Mode: normalized.Mode}
	if normalized.BrokerOBO != nil {
		body.BrokerOBO = &persistBroker{
			ClientID:           normalized.BrokerOBO.ClientID,
			Scopes:             append([]string(nil), normalized.BrokerOBO.Scopes...),
			MaxTokenTTLSeconds: normalized.BrokerOBO.MaxTokenTTLSeconds,
		}
	}
	if normalized.RequestPassthrough != nil {
		body.RequestPassthrough = &persistPassthrough{
			MaxResidenceSeconds: normalized.RequestPassthrough.MaxResidenceSeconds,
		}
	}
	return json.Marshal(body)
}

// IsLegacyAuthMode reports modes that are no longer writable.
func IsLegacyAuthMode(mode string) bool {
	switch strings.TrimSpace(strings.ToUpper(mode)) {
	case "NONE", "API_KEY", "BEARER", "OAUTH2_CLIENT", "BASIC", "CUSTOM_HEADER":
		return true
	case string(outboundidentity.ModeBrokerOBO), string(outboundidentity.ModeRequestPassthrough):
		return false
	default:
		// Unknown modes are also rejected as unsupported.
		return true
	}
}

// RejectLegacyWrite returns a stable outbound error when a caller attempts
// legacy auth fields on the dual-mode management surface.
func RejectLegacyWrite(authMode string, hasCredentialSecret bool, hasLegacyAuthConfig bool) error {
	if IsLegacyAuthMode(authMode) || hasCredentialSecret || hasLegacyAuthConfig {
		return outboundidentity.ErrIdentityModeUnsupported
	}
	return nil
}

// DescriptorHashForIdentity is a helper for impact proofs on identity mutations.
func DescriptorHashForIdentity(mode outboundidentity.Mode, identity json.RawMessage, machineWillChange bool) (string, error) {
	return HashChangeDescriptor(map[string]any{
		"mode":                        string(mode),
		"outboundIdentity":            json.RawMessage(append([]byte(nil), identity...)),
		"machineCredentialWillChange": machineWillChange,
	})
}

// FormatMachineCredentialConfigured is the only machine-credential signal DTO may expose.
func FormatMachineCredentialConfigured(secretID *string) bool {
	return secretID != nil && strings.TrimSpace(*secretID) != ""
}

// ImpactSetVersionFromCounts builds a simple impact-set version from counts.
func ImpactSetVersionFromCounts(tools, agents, workflows int) int64 {
	// Stable non-secret version: mix counts into a positive int64.
	return int64(tools+1)*1_000_000 + int64(agents+1)*1_000 + int64(workflows+1)
}

// String returns a debug-safe summary without secrets.
func (p ImpactProofPayload) String() string {
	return fmt.Sprintf("impactProof{ws=%s conn=%s actor=%s kind=%s lock=%d policy=%d exp=%d}",
		p.WorkspaceID, p.ConnectionID, p.ActorID, p.ChangeKind, p.LockVersion, p.PolicyVersion, p.ExpiresAtUnix)
}
