package workflowruntime

// FROZEN (P1 / eino-no-reinvent): PlanRunner is the legacy workflow interpreter
// kept only for explicit engine=wrapper rollback and unit/integration tests.
// Do NOT add new node semantics here. Production default after Load is
// engine=eino → compose CoreGraphRunner (einoruntime/graph_*). New nodes go to
// einoruntime graph_nodes / graph_builder only.

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/principal"
)

type ToolInvoker interface {
	Invoke(toolID string, input map[string]any, ctx ToolInvocationContext) (map[string]any, error)
}

type WorkflowRevisionResolver interface {
	ResolvePublishedRevision(workflowID string) (domain.WorkflowRevision, error)
}

type ToolInvocationContext struct {
	TraceID               string
	WorkflowID            string
	WorkspaceID           string
	NodeID                string
	UserID                string
	ActorType             string
	PrincipalSnapshot     *principal.ExecutionSnapshot
	AuthorizationSnapshot json.RawMessage
	AgentRunID            string
	WorkflowExecutionID   string
	ExecutionStepID       string
}

type ExecutionContext struct {
	UserID                string
	Input                 map[string]any
	WorkflowVersion       string
	WorkspaceID           string
	Trigger               string
	Scope                 ExecutionScope
	ActorType             string
	PrincipalSnapshot     *principal.ExecutionSnapshot
	AuthorizationSnapshot json.RawMessage
	AgentRunID            string
	WorkflowExecutionID   string
	// TrialMode enables 模拟试运行 semantics (D11): Approval nodes auto-confirm so
	// compile→trial→publish can complete without human HITL. Production execute
	// must leave this false so Approval still pauses for real side-effect paths.
	TrialMode bool
}

type PlanRunner struct {
	invoker          ToolInvoker
	revisionResolver WorkflowRevisionResolver
	mode             executionMode
}

type executionMode string

const (
	executionModeCompat  executionMode = "compat"
	executionModeWrapper executionMode = "wrapper"
)

// NativeGraphRunner is a deprecated alias for EinoCoreRunner.
//
// Historically this type wrapped the entire PlanRunner in a single compose
// Lambda (fake "native" semantics). PR11 replaces that with a true node graph;
// prefer NewEinoCoreRunner / NewEinoCoreRunnerWithInvoker for new code.
type NativeGraphRunner = EinoCoreRunner

type WrappedPlanRunner struct {
	runner PlanRunner
}

func NewPlanRunner(invoker ToolInvoker) PlanRunner {
	return PlanRunner{invoker: invoker, mode: executionModeCompat}
}

func NewPlanRunnerWithRevisionResolver(invoker ToolInvoker, resolver WorkflowRevisionResolver) PlanRunner {
	return PlanRunner{invoker: invoker, revisionResolver: resolver, mode: executionModeCompat}
}

// NewNativeGraphRunner returns a true eino_core node-graph runner.
// Deprecated: use NewEinoCoreRunnerWithInvoker or NewEinoCoreRunner.
func NewNativeGraphRunner(invoker ToolInvoker) NativeGraphRunner {
	return NewEinoCoreRunnerWithInvoker(invoker)
}

func NewWrappedPlanRunner(invoker ToolInvoker) WrappedPlanRunner {
	return WrappedPlanRunner{runner: PlanRunner{invoker: invoker, mode: executionModeWrapper}}
}

func NewWrappedPlanRunnerWithRevisionResolver(invoker ToolInvoker, resolver WorkflowRevisionResolver) WrappedPlanRunner {
	return WrappedPlanRunner{runner: PlanRunner{invoker: invoker, revisionResolver: resolver, mode: executionModeWrapper}}
}

func (r WrappedPlanRunner) Run(plan domain.CompiledExecutionPlan, ctx ExecutionContext) (domain.Execution, error) {
	return r.runner.Run(plan, ctx)
}

func (r WrappedPlanRunner) RunWithCheckpoint(plan domain.CompiledExecutionPlan, ctx ExecutionContext) (domain.Execution, *WorkflowApprovalCheckpoint, error) {
	return r.runner.RunWithCheckpoint(plan, ctx)
}

// ResumeApproval continues a wrapper Approval pause via ConfirmApproval / CancelApproval
// (not compose resume — EinoCheckPointID is empty on this path).
func (r WrappedPlanRunner) ResumeApproval(
	plan domain.CompiledExecutionPlan,
	ctx ExecutionContext,
	checkpoint WorkflowApprovalCheckpoint,
	decision ApprovalResumeDecision,
) (domain.Execution, error) {
	return r.runner.ResumeApproval(plan, ctx, checkpoint, decision)
}

// ResumeApproval implements CompiledPlanExecutor for the plan/wrapper path.
func (r PlanRunner) ResumeApproval(
	_ domain.CompiledExecutionPlan,
	_ ExecutionContext,
	checkpoint WorkflowApprovalCheckpoint,
	decision ApprovalResumeDecision,
) (domain.Execution, error) {
	resolvedBy := strings.TrimSpace(decision.ResolvedBy)
	if resolvedBy == "" {
		resolvedBy = "system"
	}
	// Reconstruct a minimal execution shell for Confirm/Cancel identity checks.
	execution := domain.Execution{
		ID:              checkpoint.ExecutionID,
		WorkflowID:      checkpoint.WorkflowID,
		WorkflowVersion: checkpoint.Context.WorkflowVersion,
		WorkspaceID:     checkpoint.Context.WorkspaceID,
		Trigger:         checkpoint.Context.Trigger,
		UserID:          checkpoint.RequestedBy,
		Status:          domain.ExecutionApproval,
		StartedAt:       checkpoint.CreatedAt,
		CreatedAt:       checkpoint.CreatedAt,
	}
	switch strings.ToLower(strings.TrimSpace(decision.Decision)) {
	case "cancelled", "cancel", "denied", "deny":
		return r.CancelApproval(checkpoint, execution, resolvedBy)
	default:
		return r.ConfirmApproval(checkpoint, execution, resolvedBy)
	}
}

func (r PlanRunner) Run(plan domain.CompiledExecutionPlan, ctx ExecutionContext) (domain.Execution, error) {
	execution, _, err := r.RunWithCheckpoint(plan, ctx)
	return execution, err
}

func (r PlanRunner) RunWithCheckpoint(plan domain.CompiledExecutionPlan, ctx ExecutionContext) (domain.Execution, *WorkflowApprovalCheckpoint, error) {
	started := time.Now().UTC()
	executionID := "exec-" + started.Format("20060102150405.000000000")
	traceID := "trace-" + executionID
	scope := newExecutionScope(ctx)
	execution := domain.Execution{
		ID:                      executionID,
		WorkflowID:              plan.WorkflowID,
		WorkflowVersion:         ctx.WorkflowVersion,
		WorkspaceID:             ctx.WorkspaceID,
		Trigger:                 defaultString(ctx.Trigger, r.defaultTrigger()),
		UserID:                  ctx.UserID,
		TraceID:                 traceID,
		Status:                  domain.ExecutionSuccess,
		InputSummary:            summarize(scope.Input),
		RawPayloadObjectAddress: "s3://actweave-executions/" + executionID + "/payload.json",
		StartedAt:               started,
		CreatedAt:               started,
	}

	state := runtimeState{
		execution: execution,
		context:   cloneExecutionContext(ctx),
		steps: []domain.ExecutionStepRecord{
			newStep(executionID, "Auth Check", "", "", domain.ExecutionStepPassed, "JWT claims", "user="+ctx.UserID),
			newStep(executionID, "Workspace Load", "", "", domain.ExecutionStepPassed, ctx.WorkspaceID, "workspace loaded"),
			newStep(executionID, "Workflow Decision", "", "", domain.ExecutionStepPassed, plan.WorkflowID, r.decisionSummary()),
		},
		scope:              scope,
		executed:           map[string]bool{},
		skipped:            map[string]bool{},
		skipCauses:         map[string]skipCause{},
		branchContexts:     map[string]map[string]string{},
		foreachControllers: buildForEachControllers(plan, buildNodeTypes(plan)),
		nodeTypes:          buildNodeTypes(plan),
		selectedBranches:   map[string]string{},
		conditionBranches:  buildConditionBranches(plan),
	}

	runErr := r.runPlanNodes(plan, &state)
	if state.execution.Status == domain.ExecutionSuccess && !state.reachedTerminal {
		state.execution.Status = domain.ExecutionFailed
		state.execution.ErrorMessage = "compiled plan did not reach a terminal node"
		runErr = errors.New(state.execution.ErrorMessage)
	}

	checkpoint := approvalCheckpointFromState(plan, state, ctx.UserID)
	state.finish(plan.WorkflowID, started)
	return state.execution, checkpoint, runErr
}

func (r PlanRunner) ConfirmApproval(checkpoint WorkflowApprovalCheckpoint, execution domain.Execution, resolvedBy string) (domain.Execution, error) {
	if checkpoint.Status != WorkflowApprovalPending {
		return domain.Execution{}, errors.New("approval checkpoint is not pending")
	}
	if execution.ID == "" || execution.ID != checkpoint.ExecutionID {
		return domain.Execution{}, errors.New("approval checkpoint execution does not match")
	}
	started := defaultTime(execution.StartedAt, time.Now().UTC())
	scope := cloneExecutionScope(checkpoint.Scope)
	if scope.NodeOutputs == nil {
		scope.NodeOutputs = map[string]map[string]any{}
	}
	resolvedAt := time.Now().UTC()
	scope.NodeOutputs[checkpoint.NodeID] = map[string]any{
		"approval":    "confirmed",
		"decision":    "confirmed",
		"requestedBy": checkpoint.RequestedBy,
		"requestedAt": checkpoint.CreatedAt.Format(time.RFC3339Nano),
		"resolvedBy":  resolvedBy,
		"resolvedAt":  resolvedAt.Format(time.RFC3339Nano),
	}

	execution.Status = domain.ExecutionSuccess
	execution.ErrorMessage = ""
	execution.OutputSummary = ""
	state := runtimeStateFromCheckpoint(checkpoint.Plan, execution, scope, checkpoint.Context)
	state.steps = append(state.steps, newStep(
		execution.ID,
		"Approval Confirmed",
		checkpoint.NodeID,
		"Approval",
		domain.ExecutionStepPassed,
		checkpoint.NodeReason,
		approvalAuditSummary("confirmed", checkpoint.RequestedBy, checkpoint.CreatedAt, resolvedBy, resolvedAt),
	))

	runErr := r.runPlanNodes(checkpoint.Plan, &state)
	if state.execution.Status == domain.ExecutionSuccess && !state.reachedTerminal {
		state.execution.Status = domain.ExecutionFailed
		state.execution.ErrorMessage = "compiled plan did not reach a terminal node"
		runErr = errors.New(state.execution.ErrorMessage)
	}
	state.finish(checkpoint.WorkflowID, started)
	return state.execution, runErr
}

func (r PlanRunner) CancelApproval(checkpoint WorkflowApprovalCheckpoint, execution domain.Execution, resolvedBy string) (domain.Execution, error) {
	if checkpoint.Status != WorkflowApprovalPending {
		return domain.Execution{}, errors.New("approval checkpoint is not pending")
	}
	if execution.ID == "" || execution.ID != checkpoint.ExecutionID {
		return domain.Execution{}, errors.New("approval checkpoint execution does not match")
	}
	started := defaultTime(execution.StartedAt, time.Now().UTC())
	resolvedAt := time.Now().UTC()
	execution.Status = domain.ExecutionFailed
	execution.ErrorMessage = "workflow approval cancelled"
	execution.OutputSummary = "Workflow approval was cancelled"
	state := runtimeState{
		execution: execution,
		steps:     append([]domain.ExecutionStepRecord{}, execution.Steps...),
	}
	state.steps = append(state.steps, newStep(
		execution.ID,
		"Approval Cancelled",
		checkpoint.NodeID,
		"Approval",
		domain.ExecutionStepCancelled,
		checkpoint.NodeReason,
		approvalAuditSummary("cancelled", checkpoint.RequestedBy, checkpoint.CreatedAt, resolvedBy, resolvedAt),
	))
	state.finish(checkpoint.WorkflowID, started)
	return state.execution, nil
}

func (r PlanRunner) runPlanNodes(plan domain.CompiledExecutionPlan, state *runtimeState) error {
	var runErr error
	for _, node := range plan.Nodes {
		if state.executed[node.NodeID] {
			continue
		}
		shouldExecute, cause := state.shouldExecuteNode(node)
		if !shouldExecute {
			if cause != nil {
				state.skipped[node.NodeID] = true
				state.skipCauses[node.NodeID] = *cause
				state.steps = append(state.steps, newStep(
					state.execution.ID,
					nodeLabel(node, node.Type),
					node.NodeID,
					node.Type,
					domain.ExecutionStepSkipped,
					summarize(node.Config),
					skipCauseSummary(*cause),
				))
			}
			continue
		}
		executeNode := r.executeNode
		if controllerID := state.foreachControllers[node.NodeID]; controllerID != "" && node.Type != "ForEach" {
			executeNode = func(node domain.ExecutionPlanNode, state *runtimeState) error {
				return r.executeLoopControlledNode(node, controllerID, state)
			}
		}
		if err := executeNode(node, state); err != nil {
			state.execution.Status = domain.ExecutionFailed
			state.execution.ErrorMessage = err.Error()
			if !hasFailedStepForNode(state.steps, node.NodeID) {
				failedStep := newStep(state.execution.ID, nodeLabel(node, node.Type), node.NodeID, node.Type, domain.ExecutionStepFailed, summarize(node.Config), "")
				failedStep.ErrorMessage = err.Error()
				state.steps = append(state.steps, failedStep)
			}
			runErr = err
			break
		}
		state.branchContexts[node.NodeID] = state.selectedBranchContext(node)
		if state.execution.Status == domain.ExecutionApproval {
			r.recordKnownSkippedNodes(plan, state)
			break
		}
	}
	return runErr
}

func (r PlanRunner) recordKnownSkippedNodes(plan domain.CompiledExecutionPlan, state *runtimeState) {
	for {
		recorded := false
		for _, node := range plan.Nodes {
			if state.executed[node.NodeID] || state.skipped[node.NodeID] {
				continue
			}
			shouldExecute, cause := state.shouldExecuteNode(node)
			if shouldExecute || cause == nil {
				continue
			}
			state.skipped[node.NodeID] = true
			state.skipCauses[node.NodeID] = *cause
			state.steps = append(state.steps, newStep(
				state.execution.ID,
				nodeLabel(node, node.Type),
				node.NodeID,
				node.Type,
				domain.ExecutionStepSkipped,
				summarize(node.Config),
				skipCauseSummary(*cause),
			))
			recorded = true
		}
		if !recorded {
			return
		}
	}
}

func hasFailedStepForNode(steps []domain.ExecutionStepRecord, nodeID string) bool {
	if len(steps) == 0 {
		return false
	}
	step := steps[len(steps)-1]
	return step.NodeID == nodeID && step.Status == domain.ExecutionStepFailed
}

func (r PlanRunner) defaultTrigger() string {
	switch r.mode {
	case executionModeWrapper:
		return "Workflow Runtime Wrapper"
	default:
		return "Workflow Trial Run"
	}
}

func (r PlanRunner) decisionSummary() string {
	switch r.mode {
	case executionModeWrapper:
		return "workflow.graph.v1 -> workflowruntime wrapper"
	default:
		return "workflow.graph.v1 -> compiled execution plan"
	}
}

func skipCauseSummary(cause skipCause) string {
	if cause.conditionID == "" {
		return "skipped because a required dependency was not executed"
	}
	if cause.branch == "" {
		return "skipped because condition " + cause.conditionID + " did not unblock this node"
	}
	return "skipped because condition " + cause.conditionID + " did not select branch " + cause.branch
}

// isCoreGraphNodeType reports whether nodeType is in the eino_core native set.
// Kept for test helpers; coverage matrix lives in workflowtranslator.
func isCoreGraphNodeType(nodeType string) bool {
	switch nodeType {
	case "Start", "Tool", "Condition", "Approval", "Transform", "Parallel", "HTTP", "SubWorkflow", "End":
		return true
	default:
		return false
	}
}
