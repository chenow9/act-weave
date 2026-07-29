package chat

import (
	"encoding/json"
	"strings"
	"time"

	"actweave/backend/internal/principal"

	"github.com/google/uuid"
)

type OwnershipMode string

const (
	OwnershipSubjectOwned    OwnershipMode = "SUBJECT_OWNED"
	OwnershipPolicyShared    OwnershipMode = "POLICY_SHARED"
	RuntimeSystemPrincipalID               = "00000000-0000-0000-0000-000000000001"
)

// Ownership is the immutable resource owner established when a Chat Session
// is created. Actor is the transport caller; Subject is the represented owner.
// With no Subject, the Service Principal owns the resource directly.
type Ownership struct {
	Identity      principal.InvocationIdentity
	ClientID      string
	Mode          OwnershipMode
	PolicyVersion int64
}

// Access identifies a caller attempting to observe or mutate a Chat resource.
// Policy sharing is an explicit authorization result, never inferred from a
// matching Service Principal alone.
type Access struct {
	Identity          principal.InvocationIdentity
	ClientID          string
	AllowPolicyShared bool
}

func NewUserOwnership(workspaceID, userID string) (Ownership, error) {
	ref := principal.Ref{WorkspaceID: strings.TrimSpace(workspaceID), Type: principal.TypeUser, ID: strings.TrimSpace(userID)}
	identity, err := principal.NewInvocationIdentity(ref, &ref)
	if err != nil {
		return Ownership{}, ErrInvalid
	}
	return Ownership{Identity: identity, Mode: OwnershipSubjectOwned, PolicyVersion: 1}, nil
}

func NewUserAccess(workspaceID, userID string) (Access, error) {
	ownership, err := NewUserOwnership(workspaceID, userID)
	if err != nil {
		return Access{}, err
	}
	return Access{Identity: ownership.Identity}, nil
}

func (value Ownership) Validate(workspaceID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if value.Identity.Validate() != nil || value.Identity.Actor.WorkspaceID != workspaceID || value.PolicyVersion < 1 {
		return ErrInvalid
	}
	switch value.Identity.Actor.Type {
	case principal.TypeUser:
		if value.Identity.Subject == nil || value.Identity.Subject.Type != principal.TypeUser ||
			value.Identity.Subject.ID != value.Identity.Actor.ID || value.ClientID != "" ||
			value.Mode != OwnershipSubjectOwned {
			return ErrInvalid
		}
	case principal.TypeServicePrincipal:
		if !canonicalUUID(value.ClientID) {
			return ErrInvalid
		}
		if value.Identity.Subject != nil && value.Identity.Subject.Type != principal.TypeExternalSubject {
			return ErrInvalid
		}
		if value.Identity.Subject != nil && value.Mode != OwnershipSubjectOwned {
			return ErrInvalid
		}
		if value.Mode != OwnershipSubjectOwned && value.Mode != OwnershipPolicyShared {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func (value Access) Validate(workspaceID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if value.Identity.Validate() != nil || value.Identity.Actor.WorkspaceID != workspaceID {
		return ErrInvalid
	}
	switch value.Identity.Actor.Type {
	case principal.TypeUser:
		if value.Identity.Subject == nil || value.Identity.Subject.Type != principal.TypeUser ||
			value.Identity.Subject.ID != value.Identity.Actor.ID || value.ClientID != "" || value.AllowPolicyShared {
			return ErrInvalid
		}
	case principal.TypeServicePrincipal:
		if !canonicalUUID(value.ClientID) ||
			(value.Identity.Subject != nil && value.Identity.Subject.Type != principal.TypeExternalSubject) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func (value Ownership) Access(allowPolicyShared bool) Access {
	return Access{Identity: value.Identity, ClientID: value.ClientID, AllowPolicyShared: allowPolicyShared}
}

func canonicalUUID(value string) bool {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil && parsed.String() == strings.TrimSpace(value)
}

type Session struct {
	ID                    string
	WorkspaceID           string
	AgentID               string
	Title                 string
	Status                string
	CreatedBy             string
	LatestRunID           string
	PendingConfirmationID string
	CreatedAt             time.Time
	UpdatedAt             time.Time
	LockVersion           int64
	Ownership             Ownership
}

type Message struct {
	ID              string
	WorkspaceID     string
	SessionID       string
	Role            string
	Content         string
	ContentObjectID string
	ContentSHA256   string
	ContentLength   int64
	Status          string
	RunID           string
	ConfirmationID  string
	CreatedBy       string
	CreatedAt       time.Time
	Identity        principal.InvocationIdentity
	ClientID        string
	OwnershipMode   OwnershipMode
	PolicyVersion   int64
}

// MessagePageCursor is a reverse (created_at, id) cursor for history pagination.
// Empty means "start from newest".
type MessagePageCursor struct {
	CreatedAt time.Time
	ID        string
}

// MessagePage is one page of messages newest-first (caller may reverse for chronology).
type MessagePage struct {
	Messages   []Message
	NextCursor *MessagePageCursor
	HasMore    bool
}

type CreateSessionInput struct {
	ID          string
	WorkspaceID string
	AgentID     string
	Title       string
	CreatedBy   string
	Ownership   *Ownership
}

type SendMessageInput struct {
	MessageID             string
	RunID                 string
	WorkspaceID           string
	SessionID             string
	Content               string
	CreatedBy             string
	TraceID               string
	RunInputSummary       json.RawMessage
	AuthorizationSnapshot json.RawMessage
	Access                *Access
	PrincipalSnapshot     *principal.ExecutionSnapshot
}

type SendMessageResult struct {
	Session Session
	Message Message
}

type RecordAssistantResultInput struct {
	AssistantMessageID string
	WorkspaceID        string
	SessionID          string
	UserMessageID      string
	RunID              string
	Content            string
	ExpectedRunStatus  string
	ExpectedRunLock    int64
	RunStatus          string
	RunOutputSummary   []byte
	RunErrorCode       string
}

type RecordAssistantResult struct {
	Message Message
}
