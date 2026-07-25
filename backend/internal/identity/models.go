package identity

import "time"

// Status is the persisted local-account state.
type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusLocked   Status = "LOCKED"
	StatusDisabled Status = "DISABLED"
)

// PlatformRole contains only platform-wide privileges. Workspace membership
// roles are intentionally modeled in the workspace domain.
type PlatformRole string

const (
	PlatformRoleUser  PlatformRole = "USER"
	PlatformRoleAdmin PlatformRole = "PLATFORM_ADMIN"
)

// User is safe for application services and read DTO mapping. It deliberately
// contains no password or session-token fields.
type User struct {
	ID           string
	Username     string
	Email        *string
	DisplayName  string
	AvatarURL    *string
	Status       Status
	PlatformRole PlatformRole
	Locale       string
	Timezone     string
	LastLoginAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LockVersion  int64
}

type UserListQuery struct {
	Query        string
	Status       *Status
	PlatformRole *PlatformRole
	Limit        int
	Offset       int
}

type UserPage struct {
	Items []User
	Total int64
}

// UserWorkspaceMembership is an administrator read model. Workspace remains
// the owner of membership writes and authorization decisions.
type UserWorkspaceMembership struct {
	WorkspaceID          string
	WorkspaceSlug        string
	WorkspaceDisplayName string
	WorkspaceStatus      string
	Role                 string
	JoinedAt             time.Time
	DisabledAt           *time.Time
}

// PasswordCredential is restricted to identity/authentication services and is
// never embedded in User or a transport read DTO.
type PasswordCredential struct {
	UserID             string
	PasswordHash       string `json:"-"`
	PasswordAlgorithm  string
	PasswordChangedAt  time.Time
	FailedAttempts     int
	LockedUntil        *time.Time
	MustChangePassword bool
}

// NewLocalUser contains the values needed for the atomic creation of a local
// user and its first password credential.
type NewLocalUser struct {
	ID                 string
	Username           string
	Email              *string
	DisplayName        string
	AvatarURL          *string
	Status             Status
	PlatformRole       PlatformRole
	Locale             string
	Timezone           string
	PasswordHash       string `json:"-"`
	PasswordAlgorithm  string
	PasswordChangedAt  time.Time
	MustChangePassword bool
}

// UserProfileUpdate is intentionally limited to mutable presentation fields.
// Account status and platform role use security-sensitive authn commands.
type UserProfileUpdate struct {
	DisplayName         *string
	Email               *string
	AvatarURL           *string
	Locale              *string
	Timezone            *string
	ExpectedLockVersion int64
}

// CredentialReplacement replaces the password hash and clears any lockout
// counters in one statement.
type CredentialReplacement struct {
	PasswordHash              string `json:"-"`
	PasswordAlgorithm         string
	PasswordChangedAt         time.Time
	ExpectedPasswordChangedAt time.Time
	MustChangePassword        bool
}

// AuthSession is safe for application services and read DTO mapping. The
// refresh-token hash remains an input to narrowly scoped repository methods and
// is never returned on this model.
type AuthSession struct {
	ID         string
	UserID     string
	UserAgent  *string
	IP         *string
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	LastSeenAt time.Time
	CreatedAt  time.Time
}

type NewAuthSession struct {
	ID               string
	UserID           string
	RefreshTokenHash string `json:"-"`
	UserAgent        *string
	IP               *string
	ExpiresAt        time.Time
}

type LoginIdentity struct {
	User       User
	Credential PasswordCredential
}
