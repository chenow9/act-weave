package connection

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

type Connection struct {
	ID                 string
	WorkspaceID        string
	ProviderID         string
	Name               string
	Alias              string
	Environment        string
	ExternalAccountRef *string
	// AuthMode / AuthConfig / CredentialSecretID are legacy evidence columns
	// retained after 000060. New management paths must not write them.
	AuthMode              string
	AuthConfig            json.RawMessage
	CredentialSecretID    *string `json:"-"`
	CredentialConfigured  bool
	CredentialFingerprint string
	GrantedScopes         json.RawMessage
	Policy                json.RawMessage
	Status                Status
	LastVerifiedAt        *time.Time
	LastErrorCode         *string
	// Dual-mode outbound identity (000060+).
	OutboundIdentity              json.RawMessage
	OutboundIdentityPolicyVersion int64
	MigrationState                string
	MachineCredentialSecretID     *string `json:"-"`
	MachineCredentialConfigured   bool
	CreatedBy                     string
	UpdatedBy                     string
	CreatedAt                     time.Time
	UpdatedAt                     time.Time
	LockVersion                   int64
	DeletedAt                     *time.Time
}

type NewConnection struct {
	ID                 string
	WorkspaceID        string
	ProviderID         string
	Name               string
	Alias              string
	Environment        string
	ExternalAccountRef *string
	// Dual-mode only. Legacy AuthMode/AuthConfig/CredentialSecretID are rejected.
	OutboundIdentity          json.RawMessage
	MachineCredentialSecretID *string `json:"-"`
	// AuthMode is forced to a non-blank legacy placeholder for NOT NULL column
	// compatibility until the later cleanup migration drops the column.
	// Application code must not interpret it as an active scheme.
	AuthMode       string
	AuthConfig     json.RawMessage
	GrantedScopes  json.RawMessage
	Policy         json.RawMessage
	MigrationState string
	CreatedBy      string
}

type UpdateConnection struct {
	Name                      string
	Alias                     string
	Environment               string
	ExternalAccountRef        *string
	OutboundIdentity          json.RawMessage
	MachineCredentialSecretID *string `json:"-"`
	// ClearMachineCredential forces machine_credential_secret_id to NULL.
	ClearMachineCredential bool
	// IncrementPolicyVersion bumps outbound_identity_policy_version and sets UNVERIFIED.
	IncrementPolicyVersion bool
	// KeepMigrationState preserves MIGRATION_REQUIRED across identity edits until verify.
	KeepMigrationState  bool
	GrantedScopes       json.RawMessage
	Policy              json.RawMessage
	UpdatedBy           string
	ExpectedLockVersion int64
	// MetadataOnly skips identity/policy mutations (EDITOR path).
	MetadataOnly bool
}

type Verification struct {
	ID           string
	WorkspaceID  string
	ConnectionID string
	Status       string
	Diagnostics  json.RawMessage
	LatencyMS    *int
	TestedBy     string
	TestedAt     time.Time
	RawObjectID  *string
}

type NewVerification struct {
	ID           string
	WorkspaceID  string
	ConnectionID string
	Status       string
	Diagnostics  json.RawMessage
	LatencyMS    *int
	TestedBy     string
	RawObjectID  *string
	ErrorCode    *string
	// ExpectedLockVersion prevents a result obtained from an old Connection
	// snapshot from being applied after a concurrent edit. Zero is accepted for
	// repository callers that already serialize the operation.
	ExpectedLockVersion int64
}
