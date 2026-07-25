package chat

import (
	"encoding/json"
	"time"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"
)

type ChatConfirmation struct {
	ID                               string
	WorkspaceID                      string
	SessionID                        string
	RunID                            string
	TargetItemID                     string
	ExecutionConfirmationID          string
	TargetType                       string
	TargetReleaseID                  string
	InputHash                        string
	ConnectionID                     string
	PlanHash                         string
	InteractionBindingHash           string
	RiskLevel                        string
	RiskReasons                      []string
	InputSummary                     json.RawMessage
	Status                           string
	ConfirmedBy                      string
	ConfirmedAt                      *time.Time
	CreatedAt                        time.Time
	RequestedBy                      string
	RequestPrincipalSnapshotVersion  string
	RequestPrincipalSnapshot         principal.ExecutionSnapshot
	DecisionPrincipalSnapshotVersion string
	DecisionPrincipalSnapshot        *principal.ExecutionSnapshot
	ExecutionLockVersion             int64
	ExpiresAt                        time.Time
}

type PrepareChatConfirmationInput struct {
	ID                         string
	WorkspaceID                string
	SessionID                  string
	MessageID                  string
	TargetType                 string
	RiskLevel                  string
	RiskReasons                []string
	InputSummary               json.RawMessage
	ExpectedSessionLockVersion int64
	Resume                     execution.PrepareConfirmationResumeInput
}

type PreparedChatConfirmation struct {
	Confirmation ChatConfirmation
	Prepared     execution.PreparedConfirmationResume
}

type ConfirmChatConfirmationInput struct {
	WorkspaceID                  string
	ConfirmationID               string
	ActorID                      string
	PrincipalSnapshot            *principal.ExecutionSnapshot
	ServiceDecisionPolicy        *execution.ServicePrincipalDecisionPolicy
	ResumeToken                  string `json:"-"`
	IdempotencyKey               string
	Binding                      *execution.InteractionDecisionBinding
	ExpectedExecutionLockVersion int64
}

type ConfirmedChatConfirmation struct {
	Confirmation ChatConfirmation
	Resume       execution.ConfirmationResumeResult
	Cached       bool
}

type CancelChatConfirmationInput struct {
	WorkspaceID                  string
	ConfirmationID               string
	ActorID                      string
	PrincipalSnapshot            *principal.ExecutionSnapshot
	ServiceDecisionPolicy        *execution.ServicePrincipalDecisionPolicy
	IdempotencyKey               string
	Binding                      *execution.InteractionDecisionBinding
	ExpectedExecutionLockVersion int64
}

type CancelledChatConfirmation struct {
	Confirmation ChatConfirmation
	Checkpoint   execution.ConfirmationResumeCheckpoint
	Cached       bool
}
