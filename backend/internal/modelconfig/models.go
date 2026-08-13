package modelconfig

import (
	"encoding/json"
	"time"
)

type Status string

const (
	StatusUnverified Status = "UNVERIFIED"
	StatusVerified   Status = "VERIFIED"
	StatusError      Status = "ERROR"
	StatusDisabled   Status = "DISABLED"
)

// RuntimeCapabilities holds non-provider model limits and tokenizer binding.
// Schema validation and CAS write paths are introduced in later checklist items;
// IC-01 only establishes the domain shape. Values are never merged into Options.
type RuntimeCapabilities struct {
	SchemaVersion              string `json:"schemaVersion,omitempty"`
	ContextWindowTokens        int64  `json:"contextWindowTokens,omitempty"`
	DefaultOutputReserveTokens int64  `json:"defaultOutputReserveTokens,omitempty"`
	OutputTokenLimitMode       string `json:"outputTokenLimitMode,omitempty"`
	TokenizerProfile           string `json:"tokenizerProfile,omitempty"`
	TokenizerVersion           string `json:"tokenizerVersion,omitempty"`
}

type Config struct {
	ID                   string
	WorkspaceID          string
	Name                 string
	Provider             string
	APIBase              string
	ModelName            string
	CredentialSecretID   *string
	CredentialConfigured bool
	Options              json.RawMessage
	// RuntimeCapabilities is the raw JSON object from model_configs.runtime_capabilities.
	// Empty object "{}" is the expand-only default and means "unset".
	RuntimeCapabilities json.RawMessage
	// AgenticCapabilities is verification-owned (D10). Empty "{}" means unverified.
	// Never client-writable; never merged into RuntimeCapabilities or Options.
	AgenticCapabilities json.RawMessage
	// ToolDisclosurePolicy is the raw JSON object from model_configs.tool_disclosure_policy.
	// Empty object "{}" means unset. Generic create/update always persist "{}".
	ToolDisclosurePolicy json.RawMessage
	Status               Status
	LastVerifiedAt       *time.Time
	LastLatencyMS        *int
	LastErrorCode        *string
	CreatedBy            string
	UpdatedBy            string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	LockVersion          int64
	DeletedAt            *time.Time
}

type NewConfig struct {
	ID                   string
	WorkspaceID          string
	Name                 string
	Provider             string
	APIBase              string
	ModelName            string
	CredentialSecretID   *string
	Options              json.RawMessage
	RuntimeCapabilities  json.RawMessage
	ToolDisclosurePolicy json.RawMessage
	CreatedBy            string
}

type UpdateConfig struct {
	Name                string
	Provider            string
	APIBase             string
	ModelName           string
	CredentialSecretID  *string
	Options             json.RawMessage
	RuntimeCapabilities json.RawMessage
	Status              Status
	UpdatedBy           string
	ExpectedLockVersion int64
}

// VerificationUpdate is the small, compare-and-swap write performed after an
// upstream verification call has completed outside a database transaction.
// Success writes canonical AgenticCapabilities; failure writes "{}".
// CAS refuses to apply when ExpectedLockVersion does not match the current row.
type VerificationUpdate struct {
	WorkspaceID string
	ConfigID    string
	Status      Status
	LatencyMS   int
	ErrorCode   *string
	// AgenticCapabilities is required: non-empty canonical document on VERIFIED,
	// or empty object on ERROR. Never raw provider bodies.
	AgenticCapabilities json.RawMessage
	// VerifiedAt is the UTC-second evidence timestamp written to both
	// last_verified_at and capability verifiedAt on VERIFIED. Required for
	// VERIFIED so read invariants can enforce equal UTC-second relationship.
	// On ERROR may be zero (repository uses clock_timestamp).
	VerifiedAt          time.Time
	VerifiedBy          string
	ExpectedLockVersion int64
}

// DisclosurePolicyUpdate is the compare-and-swap write for set-disclosure.
// It restamps verifiedLockVersion onto the existing capability document and
// does not clear verification evidence or change status.
type DisclosurePolicyUpdate struct {
	WorkspaceID         string
	ConfigID            string
	Policy              json.RawMessage
	UpdatedBy           string
	ExpectedLockVersion int64
}
