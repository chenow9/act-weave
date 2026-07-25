package execution

import (
	"encoding/json"
	"time"

	"actweave/backend/internal/principal"
)

const (
	ConfirmationStatusPending   = "PENDING"
	ConfirmationStatusConfirmed = "CONFIRMED"
	ConfirmationStatusCancelled = "CANCELLED"
	ConfirmationStatusExpired   = "EXPIRED"
)

type ExecutionConfirmation struct {
	ID                               string
	WorkspaceID                      string
	ExecutionID                      string
	RunID                            string
	TargetItemID                     string
	NodeID                           string
	Status                           string
	Reason                           string
	RiskReasons                      []string
	ScopeSnapshot                    json.RawMessage
	ReleaseID                        string
	InputHash                        string
	ConnectionID                     string
	PlanHash                         string
	InteractionBindingHash           string
	RequestedBy                      string
	ConfirmedBy                      string
	RequestPrincipalSnapshotVersion  string
	RequestPrincipalSnapshot         principal.ExecutionSnapshot
	DecisionPrincipalSnapshotVersion string
	DecisionPrincipalSnapshot        *principal.ExecutionSnapshot
	DecisionPolicySnapshot           json.RawMessage
	CreatedAt                        time.Time
	ExpiresAt                        time.Time
	ConfirmedAt                      *time.Time
	CancelledAt                      *time.Time
	LockVersion                      int64
}

type RequestExecutionConfirmationInput struct {
	ID                string
	WorkspaceID       string
	ExecutionID       string
	RunID             string
	TargetItemID      string
	NodeID            string
	ReleaseID         string
	ConnectionID      string
	PlanHash          string
	RequestedBy       string
	PrincipalSnapshot *principal.ExecutionSnapshot
	Decision          ConfirmationDecision
}

// ServicePrincipalDecisionPolicy is the current Agent Grant policy evidence
// presented when a pure Service Principal decides an interaction. It is not
// needed for an ActWeave User or a delegated External Subject.
type ServicePrincipalDecisionPolicy struct {
	Enabled bool
	MaxRisk string
}

type RequestedExecutionConfirmation struct {
	Confirmation ExecutionConfirmation
	// ResumeToken is returned once for the runtime checkpoint. It must never be
	// persisted, logged, or copied into an API DTO.
	ResumeToken string `json:"-"`
}

type ConfirmExecutionConfirmationInput struct {
	WorkspaceID           string
	ConfirmationID        string
	ActorID               string
	PrincipalSnapshot     *principal.ExecutionSnapshot
	ServiceDecisionPolicy *ServicePrincipalDecisionPolicy
	ResumeToken           string `json:"-"`
	RunID                 string
	ReleaseID             string
	ConnectionID          string
	PlanHash              string
	TargetItemID          string
	Input                 json.RawMessage `json:"-"`
	ExpectedLockVersion   int64
}

// ConfirmPreparedExecutionInput confirms the immutable request/checkpoint that
// was persisted when execution paused. The raw token is a transient capability
// and must not be persisted, logged, or returned by an API read model.
type ConfirmPreparedExecutionInput struct {
	WorkspaceID            string
	ConfirmationID         string
	ActorID                string
	PrincipalSnapshot      *principal.ExecutionSnapshot
	ServiceDecisionPolicy  *ServicePrincipalDecisionPolicy
	ResumeToken            string `json:"-"`
	RunID                  string
	TargetItemID           string
	ReleaseID              string
	InputHash              string
	ConnectionID           string
	PlanHash               string
	InteractionBindingHash string
	ExpiresAt              time.Time
	ExpectedLockVersion    int64
}

type CancelExecutionConfirmationInput struct {
	WorkspaceID           string
	ConfirmationID        string
	ActorID               string
	PrincipalSnapshot     *principal.ExecutionSnapshot
	ServiceDecisionPolicy *ServicePrincipalDecisionPolicy
	ExpectedLockVersion   int64
}

type newExecutionConfirmation struct {
	ExecutionConfirmation
	ResumeTokenHash string
}

type confirmationMutationBinding struct {
	WorkspaceID            string
	ConfirmationID         string
	ActorID                string
	PrincipalSnapshot      principal.ExecutionSnapshot
	DecisionPolicySnapshot json.RawMessage
	ResumeTokenHash        string
	ReleaseID              string
	ConnectionID           string
	PlanHash               string
	InputHash              string
	RunID                  string
	TargetItemID           string
	InteractionBindingHash string
	ExpiresAt              time.Time
	ExpectedLockVersion    int64
	Now                    time.Time
}
