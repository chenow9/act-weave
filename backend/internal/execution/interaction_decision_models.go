package execution

import (
	"errors"
	"time"

	"actweave/backend/internal/principal"
)

const (
	InteractionDecisionApprove = "approve"
	InteractionDecisionDecline = "decline"
	InteractionDecisionCancel  = "cancel"
)

var (
	ErrInteractionDecisionInvalid        = errors.New("invalid interaction decision")
	ErrInteractionAlreadyResolved        = errors.New("interaction already resolved")
	ErrInteractionIdempotencyConflict    = errors.New("interaction decision idempotency conflict")
	ErrInteractionDecisionBindingChanged = errors.New("interaction decision binding changed")
)

// InteractionDecisionBinding is the exact public Interaction snapshot the
// caller decided. It is compared with both the core fact and durable recovery
// checkpoint before the CAS transition is allowed.
type InteractionDecisionBinding struct {
	RunID        string
	TargetItemID string
	ReleaseID    string
	InputHash    string
	ConnectionID string
	PlanHash     string
	Version      int64
	ExpiresAt    time.Time
	BindingHash  string
}

type DecideInteractionInput struct {
	WorkspaceID           string
	ConfirmationID        string
	ActorID               string
	PrincipalSnapshot     *principal.ExecutionSnapshot
	ServiceDecisionPolicy *ServicePrincipalDecisionPolicy
	Decision              string
	IdempotencyKey        string
	Binding               InteractionDecisionBinding
}

type InteractionDecisionResult struct {
	Confirmation ExecutionConfirmation
	Checkpoint   ConfirmationResumeCheckpoint
	Decision     string
	Cached       bool
	// ResumeStatus is independent of the accepted decision. SUCCEEDED means
	// the Tool/Workflow completed; an HTTP command success alone never does.
	ResumeStatus  string
	DispatchError error `json:"-"`
}

type interactionDecisionCommand struct {
	WorkspaceID          string
	ConfirmationID       string
	PrincipalBindingHash string
	IdempotencyKey       string
	RequestHash          string
	Decision             string
	ExpectedVersion      int64
	ConfirmationStatus   string
	ConfirmationVersion  int64
	CreatedAt            time.Time
}
