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
	ID                 string
	WorkspaceID        string
	Name               string
	Provider           string
	APIBase            string
	ModelName          string
	CredentialSecretID *string
	Options            json.RawMessage
	CreatedBy          string
}

type UpdateConfig struct {
	Name                string
	Provider            string
	APIBase             string
	ModelName           string
	CredentialSecretID  *string
	Options             json.RawMessage
	Status              Status
	UpdatedBy           string
	ExpectedLockVersion int64
}

// VerificationUpdate is the small, compare-and-swap write performed after an
// upstream verification call has completed outside a database transaction.
type VerificationUpdate struct {
	WorkspaceID         string
	ConfigID            string
	Status              Status
	LatencyMS           int
	ErrorCode           *string
	VerifiedBy          string
	ExpectedLockVersion int64
}
