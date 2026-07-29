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
	// ContextPolicy is the raw JSON object from workspaces.context_policy.
	// Empty object "{}" is the expand-only default and means platform defaults apply.
	ContextPolicy json.RawMessage
	CreatedBy     string
	UpdatedBy     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LockVersion   int64
	DeletedAt     *time.Time
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

// AccessibleWorkspace is a read model for the current principal: persisted
// workspace facts plus the caller's effective membership role. It must never
// be written back as a persistence entity.
type AccessibleWorkspace struct {
	Workspace
	CurrentUserRole Role
}

// WorkspaceListQuery drives GET /workspaces server-side listing (ZKL-64 D1-A).
// When Page and PageSize are zero and LegacyLimit > 0, the repository uses the
// pre-ZKL-64 limit semantics (no filters / client-side catalog style).
type WorkspaceListQuery struct {
	Query       string
	Status      *Status
	Mode        *Mode
	Page        int
	PageSize    int
	SortBy      string
	SortOrder   string
	LegacyLimit int
}

// WorkspaceAccessibleSummary counts the caller's full accessible set, not the
// current page or the filtered result (ZKL-64 D9-A).
type WorkspaceAccessibleSummary struct {
	Total       int
	Active      int
	Production  int
	BoundAgents int
}

// WorkspacePage is a paged accessible-workspace result plus full-set summary.
type WorkspacePage struct {
	Items    []AccessibleWorkspace
	Page     int
	PageSize int
	Total    int
	Summary  WorkspaceAccessibleSummary
	// Legacy is true when the request used the old limit-only contract.
	Legacy bool
}
