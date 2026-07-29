package chatruntime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
)

// Production agent orchestration lives in chatruntimebridge (eino ADK).
// This package retains Messenger, protocol projection helpers, capability
// snapshot parsing, and shared contracts used by the bridge and HTTP layers.

var (
	// ErrWaitingConfirmation signals the run paused for human confirmation.
	ErrWaitingConfirmation = errors.New("chat run waiting confirmation")
	// ErrRunCancelled is the cancel cause for in-process chat run contexts.
	ErrRunCancelled = errors.New("chat runtime run was cancelled")
)

// ContinueTimeout is the hard deadline for one async continue drive. Durable
// multi-replica leases must cover this window (with renew) so recovery cannot
// reclaim a live continue.
const ContinueTimeout = 5 * time.Minute

// Job is the async AgentRun work unit. Aliased to agentrun.Job.
type Job = agentrun.Job

// ContinueLifecycle is an optional multi-replica lease hook for continue.
type ContinueLifecycle = agentrun.ContinueLifecycle

type SessionReader interface {
	GetSession(context.Context, string, string) (chat.Session, error)
	ListMessages(context.Context, string, string) ([]chat.Message, error)
	// ListMessagesReversePage returns newest-first pages without decrypting bodies.
	// Used by session-context.v1 assembly to avoid loading entire session history.
	ListMessagesReversePage(ctx context.Context, workspaceID, sessionID string, limit int, cursor *chat.MessagePageCursor) (chat.MessagePage, error)
	// GetMessage loads a single message by id (workspace scoped).
	GetMessage(ctx context.Context, workspaceID, messageID string) (chat.Message, error)
}

type AssistantRecorder interface {
	RecordAssistantResult(context.Context, chat.RecordAssistantResultInput) (chat.RecordAssistantResult, error)
}

type ContentReader interface {
	ReadPermanentChat(context.Context, string, string, string) (string, error)
}

type AgentReader interface {
	Get(context.Context, string, string) (agent.Agent, error)
	ListPromptRevisions(context.Context, string, string) ([]agent.PromptRevision, error)
}

type ModelReader interface {
	Get(context.Context, string, string) (modelconfig.Config, error)
}

type RunReader interface {
	GetAgentRun(context.Context, string, string) (execution.AgentRun, error)
}

const (
	ProtocolRecordRunStarted           = "run.started"
	ProtocolRecordRunCompleted         = "run.completed"
	ProtocolRecordRunFailed            = "run.failed"
	ProtocolRecordToolCompleted        = "tool.completed"
	ProtocolRecordWorkflowCompleted    = "workflow.completed"
	ProtocolRecordInteractionRequested = "interaction.requested"
)

// ProtocolRecord carries durable domain facts across the Runtime/Protocol
// boundary. Receivers map these facts through native AAP mappers/projectors.
type ProtocolRecord struct {
	Kind                string
	Job                 Job
	Run                 execution.AgentRun
	Message             *chat.Message
	ActorID             string
	ToolInvocationID    string
	ToolName            string
	WorkflowExecutionID string
	WorkflowStepID      string
	Confirmation        *chat.PreparedChatConfirmation
	TargetName          string
	TargetArguments     json.RawMessage
	OccurredAt          time.Time
}

type EventRecorder interface {
	Record(context.Context, ProtocolRecord) error
}

// ModelTurnRecordInput is the permanent MODEL step evidence payload.
type ModelTurnRecordInput struct {
	WorkspaceID    string
	StepID         string
	Content        []byte
	CreatedByType  string
	CreatedByID    string
	ExpectedStatus string
	NewStatus      string
	ErrorCode      string
	Reasoning      string
}

// ModelTurnRecorder stores permanent model-turn objects and transitions MODEL steps.
type ModelTurnRecorder interface {
	Record(context.Context, ModelTurnRecordInput) (execution.AgentRunStep, error)
}

// ToolInvoker resolves and executes Tool capabilities through the Invocation Pipeline.
type ToolInvoker interface {
	ResolveInvocation(context.Context, execution.ResolveRequest) (execution.ResolvedInvocation, error)
	InvokeResolved(context.Context, execution.InvokeRequest, execution.ResolvedInvocation) (execution.PipelineResult, error)
}

// StepStore persists AgentRun steps and optional workflow execution rows.
type StepStore interface {
	AppendAgentRunStep(context.Context, execution.AppendAgentRunStepInput) (execution.AgentRunStep, error)
	TransitionAgentRunStep(context.Context, string, string, execution.StepTransition) (execution.AgentRunStep, error)
	TransitionAgentRun(context.Context, string, string, execution.RunTransition) (execution.AgentRun, error)
	StartWorkflowExecution(context.Context, execution.StartWorkflowExecutionInput) (execution.WorkflowExecution, error)
	TransitionWorkflowExecution(context.Context, string, string, execution.RunTransition) (execution.WorkflowExecution, error)
	GetAgentRun(context.Context, string, string) (execution.AgentRun, error)
}

// ConfirmationPreparer creates chat confirmation + resume checkpoints.
type ConfirmationPreparer interface {
	Prepare(context.Context, chat.PrepareChatConfirmationInput) (chat.PreparedChatConfirmation, error)
}
