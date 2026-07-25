package workspace

import (
	"encoding/json"
	"time"
)

type Mode string

const (
	ModeProduction Mode = "PRODUCTION"
	ModeSandbox    Mode = "SANDBOX"
)

type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusDisabled Status = "DISABLED"
)

type Role string

const (
	RoleOwner    Role = "OWNER"
	RoleAdmin    Role = "ADMIN"
	RoleEditor   Role = "EDITOR"
	RoleOperator Role = "OPERATOR"
	RoleViewer   Role = "VIEWER"
)

// Workspace contains persisted tenant facts only. Counts and health are read
// model concerns and are deliberately absent.
type Workspace struct {
	ID                   string
	Slug                 string
	DisplayName          string
	Mode                 Mode
	Status               Status
	OwnerUserID          string
	DefaultAgentID       *string
	DefaultModelConfigID *string
	Settings             json.RawMessage
	CreatedBy            string
	UpdatedBy            string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	LockVersion          int64
	DeletedAt            *time.Time
}

type NewWorkspace struct {
	ID          string
	Slug        string
	DisplayName string
	Mode        Mode
	OwnerUserID string
	CreatedBy   string
	Settings    json.RawMessage
}

type UpdateWorkspaceInput struct {
	DisplayName         *string
	Mode                *Mode
	Settings            json.RawMessage
	UpdatedBy           string
	ExpectedLockVersion int64
}

type Member struct {
	WorkspaceID string
	UserID      string
	Role        Role
	InvitedBy   *string
	JoinedAt    time.Time
	DisabledAt  *time.Time
}

type NewMember struct {
	WorkspaceID string
	UserID      string
	Role        Role
	InvitedBy   string
}

// MemberCandidate is the allowlisted user projection exposed to Workspace
// OWNER/ADMIN users while selecting a new member. It deliberately omits
// credentials, contact details, account timestamps, and other platform-only
// user fields.
type MemberCandidate struct {
	UserID       string
	Username     string
	DisplayName  string
	PlatformRole string
}

// AccessRecord is the current database state needed by the authorization
// service. It is resolved by workspace_id and user_id together.
type AccessRecord struct {
	WorkspaceID     string
	WorkspaceStatus Status
	UserID          string
	UserStatus      string
	Role            Role
	MemberDisabled  bool
}

// CreationDefaults are references created by a domain hook within the same
// database transaction as the Workspace and its OWNER membership.
type CreationDefaults struct {
	DefaultAgentID       *string
	DefaultModelConfigID *string
}
