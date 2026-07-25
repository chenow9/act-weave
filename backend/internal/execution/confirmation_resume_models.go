package execution

import (
	"encoding/json"
	"time"

	"actweave/backend/internal/principal"
)

const (
	ConfirmationResumeSnapshotVersion = "confirmation-resume-checkpoint.v1"
	ResumeKindTool                    = "TOOL"
	ResumeKindWorkflow                = "WORKFLOW"
	ResumeStatusPending               = "PENDING"
	ResumeStatusClaimed               = "CLAIMED"
	ResumeStatusExecuting             = "EXECUTING"
	ResumeStatusSucceeded             = "SUCCEEDED"
	ResumeStatusFailed                = "FAILED"
	ResumeStatusCancelled             = "CANCELLED"
)

type ConfirmationResumeCheckpoint struct {
	ConfirmationID           string
	WorkspaceID              string
	Kind                     string
	RunID                    string
	TargetItemID             string
	ExecutionID              string
	AgentRunStepID           string
	ExecutionStepID          string
	NodeID                   string
	RunWaitLockVersion       *int64
	ExecutionWaitLockVersion *int64
	Status                   string
	SnapshotSchemaVersion    string
	RequestSnapshot          json.RawMessage
	ResolvedSnapshot         json.RawMessage
	Input                    json.RawMessage `json:"-"`
	InputHash                string
	PlanHash                 string
	InteractionBindingHash   string
	TerminalOnSuccess        bool
	ResultSnapshot           json.RawMessage
	ErrorCode                string
	ClaimID                  string
	ClaimExpiresAt           *time.Time
	CreatedAt                time.Time
	StartedAt                *time.Time
	CompletedAt              *time.Time
	LockVersion              int64
}

type PrepareConfirmationResumeInput struct {
	Confirmation                 RequestExecutionConfirmationInput
	Kind                         string
	SnapshotSchemaVersion        string
	RequestSnapshot              json.RawMessage
	ResolvedSnapshot             json.RawMessage
	Input                        json.RawMessage `json:"-"`
	AgentRunStepID               string
	ExecutionStepID              string
	ExpectedRunLockVersion       int64
	ExpectedExecutionLockVersion int64
	TerminalOnSuccess            bool
}

type PreparedConfirmationResume struct {
	Requested  RequestedExecutionConfirmation
	Checkpoint ConfirmationResumeCheckpoint
}

type ResumeExecutionInput struct {
	Checkpoint       ConfirmationResumeCheckpoint
	ConfirmationID   string
	RequestSnapshot  json.RawMessage
	ResolvedSnapshot json.RawMessage
	Input            json.RawMessage `json:"-"`
}

type ResumeExecutionOutput struct {
	Result json.RawMessage
}

type ConfirmationResumeResult struct {
	Checkpoint ConfirmationResumeCheckpoint
	Result     json.RawMessage
	Cached     bool
}

type CancelConfirmationResumeInput struct {
	WorkspaceID                     string
	ConfirmationID                  string
	ActorID                         string
	PrincipalSnapshot               *principal.ExecutionSnapshot
	ServiceDecisionPolicy           *ServicePrincipalDecisionPolicy
	ExpectedConfirmationLockVersion int64
}

type confirmationResumeClaim struct {
	Checkpoint ConfirmationResumeCheckpoint
	Recovered  bool
}
