package workflowruntime

import (
	"context"
	"errors"
	"strings"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/workflowtranslator"

	"github.com/cloudwego/eino/compose"
)

// EinoCoreRunner executes core workflow plans as a true eino compose graph
// (design §4.2). It replaces the former NativeGraphRunner single-Lambda façade.
//
// Production default remains WrappedPlanRunner (factory); this runner is used
// when runtime.workflow.engine=eino_core for allowed workspaces (PR12 wiring).
type EinoCoreRunner struct {
	inner *einoruntime.CoreGraphRunner
}

// EinoCoreRunnerConfig configures EinoCoreRunner.
type EinoCoreRunnerConfig struct {
	Invoker          ToolInvoker
	RevisionResolver WorkflowRevisionResolver
	CheckPointStore  compose.CheckPointStore
	Cache            *einoruntime.GraphCache
}

// NewEinoCoreRunner builds a true-node-graph runner for eino_core.
func NewEinoCoreRunner(cfg EinoCoreRunnerConfig) EinoCoreRunner {
	var invoker einoruntime.WorkflowToolInvoker
	if cfg.Invoker != nil {
		invoker = toolInvokerAdapter{inner: cfg.Invoker}
	}
	var resolver einoruntime.WorkflowRevisionResolver
	if cfg.RevisionResolver != nil {
		resolver = cfg.RevisionResolver
	}
	return EinoCoreRunner{
		inner: einoruntime.NewCoreGraphRunner(einoruntime.CoreGraphRunnerConfig{
			Invoker:          invoker,
			RevisionResolver: resolver,
			CheckPointStore:  cfg.CheckPointStore,
			Cache:            cfg.Cache,
			Engine:           workflowtranslator.EngineEinoCore,
		}),
	}
}

// NewEinoCoreRunnerWithInvoker is a convenience constructor for tests.
func NewEinoCoreRunnerWithInvoker(invoker ToolInvoker) EinoCoreRunner {
	return NewEinoCoreRunner(EinoCoreRunnerConfig{
		Invoker:         invoker,
		CheckPointStore: newMemCheckPointStore(),
	})
}

// NewEinoCoreRunnerWithResolver is a convenience constructor for SubWorkflow tests.
func NewEinoCoreRunnerWithResolver(invoker ToolInvoker, resolver WorkflowRevisionResolver) EinoCoreRunner {
	return NewEinoCoreRunner(EinoCoreRunnerConfig{
		Invoker:          invoker,
		RevisionResolver: resolver,
		CheckPointStore:  newMemCheckPointStore(),
	})
}

func (r EinoCoreRunner) Run(plan domain.CompiledExecutionPlan, ctx ExecutionContext) (domain.Execution, error) {
	execution, _, err := r.RunWithCheckpoint(plan, ctx)
	return execution, err
}

// RunWithCheckpoint executes the plan. On Approval interrupt it returns
// Execution with Status=Approval, a WorkflowApprovalCheckpoint (IDs + scope
// snapshot for platform wiring), and a nil error — matching PlanRunner semantics.
//
// The primary resume path is compose resume via ResumeApproval (not whole-plan re-run).
func (r EinoCoreRunner) RunWithCheckpoint(
	plan domain.CompiledExecutionPlan,
	ctx ExecutionContext,
) (domain.Execution, *WorkflowApprovalCheckpoint, error) {
	if r.inner == nil {
		return domain.Execution{}, nil, errors.New("eino_core runner is not initialized")
	}
	// Reject advanced nodes early with a stable message (also enforced by BuildGraphIR).
	for _, node := range plan.Nodes {
		if !workflowtranslator.IsCoreNodeType(node.Type) && strings.TrimSpace(node.Type) != "" {
			// Unknown or advanced → require wrapper / full eino.
			if workflowtranslator.IsAdvancedNodeType(node.Type) ||
				!workflowtranslator.IsCoreNodeType(node.Type) {
				cov := workflowtranslator.CoverageFor(node.Type, workflowtranslator.EngineEinoCore)
				if cov.Status != workflowtranslator.CoverageNative {
					return domain.Execution{}, nil, errors.New("node type " + node.Type + " requires workflowruntime wrapper")
				}
			}
		}
	}

	req := toWorkflowRunRequest(plan, ctx)
	out, err := r.inner.Invoke(context.Background(), req)
	if err != nil && !out.Interrupted {
		return out.Execution, nil, err
	}
	if out.Interrupted {
		cp := approvalCheckpointFromEino(plan, ctx, out)
		return out.Execution, cp, nil
	}
	return out.Execution, nil, nil
}

// ResumeApproval implements CompiledPlanExecutor: continue after platform
// confirmation via compose resume (not whole-plan re-run).
func (r EinoCoreRunner) ResumeApproval(
	plan domain.CompiledExecutionPlan,
	ctx ExecutionContext,
	checkpoint WorkflowApprovalCheckpoint,
	decision ApprovalResumeDecision,
) (domain.Execution, error) {
	if r.inner == nil {
		return domain.Execution{}, errors.New("eino_core runner is not initialized")
	}
	checkPointID := strings.TrimSpace(checkpoint.EinoCheckPointID)
	if checkPointID == "" {
		return domain.Execution{}, errors.New("eino_core resume requires EinoCheckPointID")
	}
	decisionValue := strings.TrimSpace(decision.Decision)
	if decisionValue == "" {
		decisionValue = einoruntime.ApprovalDecisionConfirmed
	}
	// Normalize platform vocabulary (confirmed/cancelled) to einoruntime values.
	switch strings.ToLower(decisionValue) {
	case "cancelled", "cancel", "denied", "deny":
		decisionValue = einoruntime.ApprovalDecisionCancelled
	case "confirmed", "confirm", "approved", "approve":
		decisionValue = einoruntime.ApprovalDecisionConfirmed
	}
	interruptIDs := append([]string(nil), checkpoint.EinoInterruptIDs...)
	req := toWorkflowRunRequest(plan, ctx)
	out, err := r.inner.ResumeApproval(
		context.Background(),
		req,
		checkPointID,
		einoruntime.ApprovalDecision{
			Decision:   decisionValue,
			ResolvedBy: strings.TrimSpace(decision.ResolvedBy),
		},
		interruptIDs...,
	)
	if err != nil && !out.Interrupted {
		return out.Execution, err
	}
	return out.Execution, nil
}

// ResumeApprovalWithIDs is a convenience for tests that already hold raw IDs.
func (r EinoCoreRunner) ResumeApprovalWithIDs(
	plan domain.CompiledExecutionPlan,
	ctx ExecutionContext,
	checkPointID string,
	decision einoruntime.ApprovalDecision,
	interruptIDs ...string,
) (domain.Execution, error) {
	return r.ResumeApproval(plan, ctx, WorkflowApprovalCheckpoint{
		EinoCheckPointID: checkPointID,
		EinoInterruptIDs: append([]string(nil), interruptIDs...),
	}, ApprovalResumeDecision{
		Decision:   decision.Decision,
		ResolvedBy: decision.ResolvedBy,
	})
}

func toWorkflowRunRequest(plan domain.CompiledExecutionPlan, ctx ExecutionContext) einoruntime.WorkflowRunRequest {
	return einoruntime.WorkflowRunRequest{
		Plan:                plan,
		Input:               cloneMap(ctx.Input),
		UserID:              ctx.UserID,
		WorkspaceID:         ctx.WorkspaceID,
		WorkflowVersion:     ctx.WorkflowVersion,
		Trigger:             defaultString(ctx.Trigger, "Eino Core Workflow Graph"),
		ActorType:           ctx.ActorType,
		AgentRunID:          ctx.AgentRunID,
		WorkflowExecutionID: ctx.WorkflowExecutionID,
		TrialMode:           ctx.TrialMode,
		RevisionID:          ctx.WorkflowVersion,
	}
}

func approvalCheckpointFromEino(
	plan domain.CompiledExecutionPlan,
	ctx ExecutionContext,
	out einoruntime.WorkflowRunResult,
) *WorkflowApprovalCheckpoint {
	nodeID := ""
	reason := ""
	if out.Approval != nil {
		nodeID = out.Approval.NodeID
		reason = out.Approval.Reason
	}
	if nodeID == "" && out.State != nil {
		nodeID = out.State.PendingApprovalNodeID
		reason = out.State.PendingApprovalReason
	}
	if nodeID == "" {
		nodeID = approvalNodeIDFromSteps(out.Execution.Steps)
	}
	if nodeID == "" {
		return nil
	}
	if reason == "" {
		reason = approvalNodeReason(plan, nodeID)
	}

	scope := ExecutionScope{
		Input:        cloneMap(ctx.Input),
		NodeOutputs:  map[string]map[string]any{},
		WorkflowVars: map[string]any{},
	}
	if out.State != nil {
		scope.Input = cloneMap(out.State.Scope.Input)
		scope.WorkflowVars = cloneMap(out.State.Scope.WorkflowVars)
		if len(out.State.Scope.NodeOutputs) > 0 {
			scope.NodeOutputs = make(map[string]map[string]any, len(out.State.Scope.NodeOutputs))
			for k, v := range out.State.Scope.NodeOutputs {
				scope.NodeOutputs[k] = cloneMap(v)
			}
		}
	}

	interruptIDs := append([]string(nil), out.InterruptIDs...)
	return &WorkflowApprovalCheckpoint{
		ID:               "approval-" + out.Execution.ID + "-" + nodeID,
		WorkflowID:       plan.WorkflowID,
		ExecutionID:      out.Execution.ID,
		NodeID:           nodeID,
		NodeReason:       reason,
		Status:           WorkflowApprovalPending,
		Plan:             plan,
		Scope:            scope,
		Context:          cloneExecutionContext(ctx),
		NextNodeIDs:      nextNodeIDs(plan, nodeID),
		RequestedBy:      ctx.UserID,
		CreatedAt:        out.Execution.StartedAt,
		EinoCheckPointID: strings.TrimSpace(out.CheckPointID),
		EinoInterruptIDs: interruptIDs,
	}
}

// toolInvokerAdapter bridges workflowruntime.ToolInvoker → einoruntime.WorkflowToolInvoker.
type toolInvokerAdapter struct {
	inner ToolInvoker
}

func (a toolInvokerAdapter) Invoke(ctx context.Context, call einoruntime.WorkflowToolCall) (map[string]any, error) {
	if a.inner == nil {
		return nil, errors.New("tool invoker is nil")
	}
	return a.inner.Invoke(call.ToolID, call.Input, ToolInvocationContext{
		TraceID:             call.TraceID,
		WorkflowID:          call.WorkflowID,
		WorkspaceID:         call.WorkspaceID,
		NodeID:              call.NodeID,
		UserID:              call.UserID,
		ActorType:           call.ActorType,
		AgentRunID:          call.AgentRunID,
		WorkflowExecutionID: call.WorkflowExecutionID,
		ExecutionStepID:     call.ExecutionStepID,
	})
}

// memCheckPointStore is an in-memory compose.CheckPointStore for unit tests and
// default EinoCoreRunner when no store is injected.
type memCheckPointStore struct {
	data map[string][]byte
}

func newMemCheckPointStore() *memCheckPointStore {
	return &memCheckPointStore{data: make(map[string][]byte)}
}

func (m *memCheckPointStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	b, ok := m.data[id]
	if !ok {
		return nil, false, nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out, true, nil
}

func (m *memCheckPointStore) Set(_ context.Context, id string, checkPoint []byte) error {
	out := make([]byte, len(checkPoint))
	copy(out, checkPoint)
	m.data[id] = out
	return nil
}
