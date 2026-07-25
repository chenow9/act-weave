package einoruntime

import (
	"fmt"
	"time"

	"actweave/backend/internal/domain"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// Gob / schema register names for workflow graph checkpoint payloads.
// Stable names are part of the serialization contract (design Appendix B).
const (
	// ApprovalInterruptRegisterName is the schema.RegisterName key for
	// *ApprovalInterruptState. Do not rename — checkpoints depend on it.
	ApprovalInterruptRegisterName = "actweave_workflow_approval_v1"
	// SubWorkflowInterruptRegisterName is the schema.RegisterName key for
	// *SubWorkflowInterruptState. Do not rename — nested Approval resume depends on it.
	SubWorkflowInterruptRegisterName = "actweave_workflow_subworkflow_v1"
	// GraphStateRegisterName is the schema.RegisterName key for *GraphState.
	GraphStateRegisterName = "actweave_workflow_graph_state_v1"
)

// ApprovalInterruptSchemaVersion is stored on ApprovalInterruptState so resume
// handlers can reject unexpected payloads.
const ApprovalInterruptSchemaVersion = "actweave_workflow_approval.v1"

// SubWorkflowInterruptSchemaVersion is stored on SubWorkflowInterruptState so
// resume handlers can reject unexpected nested payloads.
const SubWorkflowInterruptSchemaVersion = "actweave_workflow_subworkflow.v1"

// Approval decision values carried via compose.ResumeWithData.
const (
	ApprovalDecisionConfirmed = "confirmed"
	ApprovalDecisionCancelled = "cancelled"
)

// approvalInterruptInfo is the user-facing interrupt info string (not persisted).
const approvalInterruptInfo = "workflow approval required"

// subWorkflowInterruptInfo is the user-facing composite interrupt info (not persisted).
const subWorkflowInterruptInfo = "nested subworkflow interrupt"

func init() {
	// Required for gob checkpoint round-trips of workflow Approval + graph local state.
	schema.RegisterName[*ApprovalInterruptState](ApprovalInterruptRegisterName)
	schema.RegisterName[*SubWorkflowInterruptState](SubWorkflowInterruptRegisterName)
	schema.RegisterName[*GraphState](GraphStateRegisterName)
	schema.RegisterName[ApprovalDecision]("actweave_workflow_approval_decision_v1")

	// Parallel fan-in (and any multi-predecessor join) merges GraphToken maps.
	// Default map merge rejects duplicate "from" keys; tokens are control-plane only.
	compose.RegisterValuesMergeFunc(func(tokens []GraphToken) (GraphToken, error) {
		return mergeGraphTokens(tokens)
	})
}

// ApprovalInterruptState is the gob-persisted interrupt payload for workflow
// Approval node pauses (design §4.4 strategy C).
//
// Contract:
//   - IDs only — no secrets, raw JWT, principal tokens, or large payloads
//   - Written on first-run StatefulInterrupt
//   - Restored via compose.GetInterruptState on resume
type ApprovalInterruptState struct {
	SchemaVersion string
	NodeID        string
	ExecutionID   string
	WorkflowID    string
	WorkspaceID   string
	// Reason is the Approval node reason (config); short human-readable, not a secret.
	Reason string
	// RequestedBy is the actor user id that hit the Approval node.
	RequestedBy string
}

// ApprovalDecision is the resume payload for an Approval interrupt.
// Decision is confirmed | cancelled.
type ApprovalDecision struct {
	Decision   string
	ResolvedBy string
}

// SubWorkflowInterruptState is the gob-persisted composite interrupt payload
// for a parent SubWorkflow node when a nested plan Approval (or other HITL)
// bubbles via compose.CompositeInterrupt (PR13c / strategy C).
//
// Contract:
//   - IDs only — no secrets, raw JWT, principal tokens, or large payloads
//   - Written when nested CoreGraphRunner returns Interrupted
//   - Restored via compose.GetInterruptState so resume can re-enter the child
//     with ChildCheckPointID + ChildInterruptIDs
type SubWorkflowInterruptState struct {
	SchemaVersion     string
	NodeID            string // parent SubWorkflow plan node id
	ChildWorkflowID   string
	ChildRevisionID   string
	ChildExecutionID  string
	ChildCheckPointID string
	// ChildInterruptIDs are root-cause-first IDs for compose.ResumeWithData on the child.
	ChildInterruptIDs []string
	// ChildApprovalNodeID / Reason mirror nested Approval root-cause (when present).
	ChildApprovalNodeID string
	ChildApprovalReason string
}

// GraphScope mirrors workflowruntime.ExecutionScope for eino_core graphs
// (design §4.2). ForeachItem / ForeachAlias are set only during PR13d
// loop-scoped body iterations (sequential; not persisted across non-loop nodes).
type GraphScope struct {
	Input        map[string]any
	NodeOutputs  map[string]map[string]any
	WorkflowVars map[string]any
	// ForeachItem is the current collection element during a ForEach body iteration.
	ForeachItem any
	// ForeachAlias is the itemAlias from the controlling ForEach node config.
	ForeachAlias string
}

// GraphState is the per-run local state shared across compose nodes.
// It is gob-persisted in the checkpoint so Approval resume continues without
// whole-plan re-run.
type GraphState struct {
	Scope GraphScope

	ExecutionID         string
	TraceID             string
	WorkspaceID         string
	WorkflowID          string
	WorkflowVersion     string
	UserID              string
	ActorType           string
	AgentRunID          string
	WorkflowExecutionID string
	Trigger             string
	// TrialMode auto-confirms Approval nodes (模拟试运行 / D11). Not for production.
	TrialMode bool

	Status           domain.ExecutionStatus
	ErrorMessage     string
	OutputSummary    string
	InputSummary     string
	Steps            []domain.ExecutionStepRecord
	SelectedBranches map[string]string

	StartedAt time.Time
	// ReachedTerminal is true after End or Approval interrupt path.
	ReachedTerminal bool
	// PendingApprovalNodeID is set when an Approval node interrupts.
	PendingApprovalNodeID string
	// PendingApprovalReason is the reason from the Approval node config.
	PendingApprovalReason string
}

// GraphInput is the compose graph invoke input (design §4.2 execution meta).
type GraphInput struct {
	ExecutionID         string
	TraceID             string
	WorkspaceID         string
	WorkflowID          string
	WorkflowVersion     string
	UserID              string
	ActorType           string
	AgentRunID          string
	WorkflowExecutionID string
	Trigger             string
	// TrialMode auto-confirms Approval nodes (模拟试运行 / D11). Not for production.
	TrialMode bool
	Input     map[string]any
	StartedAt time.Time
}

// GraphResult is the compose graph output after a successful complete run
// (or after resume that finishes the plan).
type GraphResult struct {
	Execution domain.Execution
}

// GraphToken is the data-plane token passed between nodes. Real workflow data
// lives in GraphState.Scope; the token only carries control-flow identity.
// Multi-predecessor joins (Parallel fan-out → shared End) register a custom
// merge via compose.RegisterValuesMergeFunc so duplicate "from" keys do not fail.
type GraphToken map[string]any

func newGraphToken(fromNode string) GraphToken {
	return GraphToken{"from": fromNode}
}

// mergeGraphTokens combines fan-in tokens. Payload is ignored by lambdas; we
// keep a stable merged view for diagnostics (from becomes a list when >1).
func mergeGraphTokens(tokens []GraphToken) (GraphToken, error) {
	if len(tokens) == 0 {
		return GraphToken{}, nil
	}
	if len(tokens) == 1 {
		return cloneGraphToken(tokens[0]), nil
	}
	out := GraphToken{}
	from := make([]string, 0, len(tokens))
	for _, t := range tokens {
		for k, v := range t {
			if k == "from" {
				switch typed := v.(type) {
				case string:
					from = append(from, typed)
				case []string:
					from = append(from, typed...)
				case []any:
					for _, item := range typed {
						from = append(from, fmt.Sprint(item))
					}
				default:
					from = append(from, fmt.Sprint(v))
				}
				continue
			}
			// Non-from keys: last writer wins (tokens rarely carry extras).
			out[k] = v
		}
	}
	switch len(from) {
	case 0:
		// leave unset
	case 1:
		out["from"] = from[0]
	default:
		// Store as []any so gob / json stay generic map-friendly.
		items := make([]any, len(from))
		for i, f := range from {
			items[i] = f
		}
		out["from"] = items
	}
	return out, nil
}

func cloneGraphToken(t GraphToken) GraphToken {
	if len(t) == 0 {
		return GraphToken{}
	}
	out := make(GraphToken, len(t))
	for k, v := range t {
		out[k] = v
	}
	return out
}

// newGraphState builds initial local state from invoke input.
func newGraphState(in GraphInput) *GraphState {
	input := cloneAnyMap(in.Input)
	if input == nil {
		input = map[string]any{}
	}
	started := in.StartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}
	return &GraphState{
		Scope: GraphScope{
			Input:        input,
			NodeOutputs:  map[string]map[string]any{},
			WorkflowVars: map[string]any{},
		},
		ExecutionID:         in.ExecutionID,
		TraceID:             in.TraceID,
		WorkspaceID:         in.WorkspaceID,
		WorkflowID:          in.WorkflowID,
		WorkflowVersion:     in.WorkflowVersion,
		UserID:              in.UserID,
		ActorType:           in.ActorType,
		AgentRunID:          in.AgentRunID,
		WorkflowExecutionID: in.WorkflowExecutionID,
		Trigger:             in.Trigger,
		TrialMode:           in.TrialMode,
		Status:              domain.ExecutionSuccess,
		InputSummary:        summarizeValue(input),
		SelectedBranches:    map[string]string{},
		StartedAt:           started,
	}
}

// toExecution projects GraphState into a domain.Execution snapshot.
func (s *GraphState) toExecution() domain.Execution {
	if s == nil {
		return domain.Execution{}
	}
	finished := time.Now().UTC()
	started := s.StartedAt
	if started.IsZero() {
		started = finished
	}
	status := s.Status
	if status == "" {
		status = domain.ExecutionSuccess
	}
	return domain.Execution{
		ID:                      s.ExecutionID,
		WorkflowID:              s.WorkflowID,
		WorkflowVersion:         s.WorkflowVersion,
		WorkspaceID:             s.WorkspaceID,
		Trigger:                 s.Trigger,
		UserID:                  s.UserID,
		TraceID:                 s.TraceID,
		Status:                  status,
		DurationMS:              int(finished.Sub(started).Milliseconds()),
		InputSummary:            s.InputSummary,
		OutputSummary:           s.OutputSummary,
		ErrorMessage:            s.ErrorMessage,
		RawPayloadObjectAddress: "s3://actweave-executions/" + s.ExecutionID + "/payload.json",
		Steps:                   append([]domain.ExecutionStepRecord(nil), s.Steps...),
		StartedAt:               started,
		FinishedAt:              finished,
		CreatedAt:               started,
	}
}
