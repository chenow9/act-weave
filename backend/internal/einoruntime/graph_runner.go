package einoruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflowtranslator"

	"github.com/cloudwego/eino/compose"
	"github.com/google/uuid"
)

// CoreGraphRunner executes CompiledExecutionPlan as a true eino compose graph
// for engine mode eino_core (design §4.2 / §4.4).
//
// Approval uses compose.StatefulInterrupt + compose resume — not whole-plan re-run.
// SubWorkflow nests via recursive CoreGraphRunner and bubbles interrupts with
// compose.CompositeInterrupt (PR13c).
type CoreGraphRunner struct {
	invoker          WorkflowToolInvoker
	revisionResolver WorkflowRevisionResolver
	store            compose.CheckPointStore
	cache            *GraphCache
	engine           string
}

// CoreGraphRunnerConfig constructs a CoreGraphRunner.
type CoreGraphRunnerConfig struct {
	Invoker          WorkflowToolInvoker
	RevisionResolver WorkflowRevisionResolver
	CheckPointStore  compose.CheckPointStore
	// Cache is optional; when nil a private cache is created.
	Cache *GraphCache
	// Engine defaults to eino_core.
	Engine string
}

// NewCoreGraphRunner builds a runner for true node-graph execution.
func NewCoreGraphRunner(cfg CoreGraphRunnerConfig) *CoreGraphRunner {
	engine := strings.TrimSpace(cfg.Engine)
	if engine == "" {
		engine = workflowtranslator.EngineEinoCore
	}
	buildCfg := GraphBuildConfig{
		Invoker:          cfg.Invoker,
		RevisionResolver: cfg.RevisionResolver,
		CheckPointStore:  cfg.CheckPointStore,
		Engine:           engine,
	}
	cache := cfg.Cache
	if cache == nil {
		cache = NewGraphCache(buildCfg)
	} else {
		// Ensure cache builds use runner deps when the cache was created empty.
		if cache.BuildConfig.Invoker == nil && cfg.Invoker != nil {
			cache.BuildConfig.Invoker = cfg.Invoker
		}
		if cache.BuildConfig.RevisionResolver == nil && cfg.RevisionResolver != nil {
			cache.BuildConfig.RevisionResolver = cfg.RevisionResolver
		}
		if cache.BuildConfig.CheckPointStore == nil && cfg.CheckPointStore != nil {
			cache.BuildConfig.CheckPointStore = cfg.CheckPointStore
		}
		if cache.BuildConfig.Engine == "" {
			cache.BuildConfig.Engine = engine
		}
	}
	return &CoreGraphRunner{
		invoker:          cfg.Invoker,
		revisionResolver: cfg.RevisionResolver,
		store:            cfg.CheckPointStore,
		cache:            cache,
		engine:           engine,
	}
}

// WorkflowRunRequest is the invoke input for CoreGraphRunner.
type WorkflowRunRequest struct {
	Plan                domain.CompiledExecutionPlan
	Input               map[string]any
	UserID              string
	WorkspaceID         string
	WorkflowVersion     string
	Trigger             string
	ActorType           string
	AgentRunID          string
	WorkflowExecutionID string
	// TrialMode enables 模拟试运行 Approval auto-confirm (D11). Production false.
	TrialMode bool
	// CheckPointID, when set, is used for compose checkpoint persistence.
	// Empty → generated once per run as ws/{ws}/workflow_exec/{execID}/{nonce}.
	CheckPointID string
	// CacheKey overrides automatic key derivation (workspace, revision, planHash).
	CacheKey string
	// PlanHash contributes to the compile cache key when CacheKey is empty.
	PlanHash string
	// RevisionID contributes to the compile cache key when CacheKey is empty.
	RevisionID string
}

// WorkflowRunResult is returned from Invoke / Resume.
type WorkflowRunResult struct {
	Execution domain.Execution
	// Interrupted is true when an Approval (or other) StatefulInterrupt paused the run.
	Interrupted bool
	// CheckPointID is the compose checkpoint key (stable for resume).
	CheckPointID string
	// InterruptIDs are root-cause interrupt context IDs for compose.ResumeWithData.
	InterruptIDs []string
	// Approval is non-nil when Interrupted due to an Approval node.
	Approval *ApprovalInterruptState
	// State is the in-memory GraphState snapshot (also checkpointed).
	State *GraphState
	// InterruptErr is the raw compose interrupt error when Interrupted=true.
	// SubWorkflow uses this for compose.CompositeInterrupt funneling; nil otherwise.
	// Top-level callers still treat Interrupted with err=nil as a soft pause.
	InterruptErr error
}

// Invoke runs the plan from the start (or continues if CheckPointID already has state).
func (r *CoreGraphRunner) Invoke(ctx context.Context, req WorkflowRunRequest) (WorkflowRunResult, error) {
	if r == nil {
		return WorkflowRunResult{}, errors.New("einoruntime: CoreGraphRunner is nil")
	}
	started := time.Now().UTC()
	// Durable workflow_executions.id when provided (trial / production :execute).
	// Chat/AAP WORKFLOW-as-tool often omit this — do NOT invent a fake id here:
	// tool_invocations.workflow_execution_id FK requires a real row (or NULL).
	durableWorkflowExecutionID := strings.TrimSpace(req.WorkflowExecutionID)
	executionID := durableWorkflowExecutionID
	if executionID == "" {
		executionID = "exec-" + started.Format("20060102150405.000000000")
	}
	traceID := "trace-" + executionID
	workspaceID := strings.TrimSpace(req.WorkspaceID)

	checkPointID := strings.TrimSpace(req.CheckPointID)
	if checkPointID == "" {
		nonce, err := newWorkflowCheckpointNonce()
		if err != nil {
			return WorkflowRunResult{}, err
		}
		// Prefer stable multi-tenant shape when workspace is known; tests may use a short id.
		if workspaceID != "" {
			checkPointID = FormatWorkflowCheckpointID(workspaceID, executionID, nonce)
		} else {
			checkPointID = "cp-" + executionID + "-" + nonce
		}
	}

	graph, err := r.loadGraph(ctx, req)
	if err != nil {
		return WorkflowRunResult{}, err
	}

	input := GraphInput{
		ExecutionID:         executionID,
		TraceID:             traceID,
		WorkspaceID:         workspaceID,
		WorkflowID:          req.Plan.WorkflowID,
		WorkflowVersion:     req.WorkflowVersion,
		UserID:              req.UserID,
		ActorType:           req.ActorType,
		AgentRunID:          req.AgentRunID,
		WorkflowExecutionID: durableWorkflowExecutionID,
		Trigger:             defaultString(req.Trigger, "Eino Core Workflow Graph"),
		TrialMode:           req.TrialMode,
		Input:               cloneAnyMap(req.Input),
		StartedAt:           started,
	}
	state := newGraphState(input)
	runCtx := WithGraphStateHolder(ctx, state)
	if workspaceID != "" {
		runCtx = WithTrustedWorkspaceID(runCtx, workspaceID)
	}

	result, invokeErr := graph.Runnable.Invoke(runCtx, input, compose.WithCheckPointID(checkPointID))
	return r.mapInvokeOutcome(state, checkPointID, result, invokeErr)
}

// ResumeApproval continues a paused Approval interrupt via compose resume
// (not whole-plan re-run). interruptID should be a root-cause InterruptCtx.ID
// from the prior Invoke result; when empty, all prior InterruptIDs are tried
// via BatchResumeWithData if provided.
func (r *CoreGraphRunner) ResumeApproval(
	ctx context.Context,
	req WorkflowRunRequest,
	checkPointID string,
	decision ApprovalDecision,
	interruptIDs ...string,
) (WorkflowRunResult, error) {
	if r == nil {
		return WorkflowRunResult{}, errors.New("einoruntime: CoreGraphRunner is nil")
	}
	checkPointID = strings.TrimSpace(checkPointID)
	if checkPointID == "" {
		return WorkflowRunResult{}, errors.New("einoruntime: checkpoint id is required for approval resume")
	}
	if strings.TrimSpace(decision.Decision) == "" {
		return WorkflowRunResult{}, errors.New("einoruntime: approval decision is required")
	}

	graph, err := r.loadGraph(ctx, req)
	if err != nil {
		return WorkflowRunResult{}, err
	}

	// On resume, state is restored from checkpoint; holder still helps final projection
	// if another interrupt occurs.
	state := &GraphState{
		Scope: GraphScope{
			Input:        cloneAnyMap(req.Input),
			NodeOutputs:  map[string]map[string]any{},
			WorkflowVars: map[string]any{},
		},
		SelectedBranches: map[string]string{},
	}
	runCtx := WithGraphStateHolder(ctx, state)
	if ws := strings.TrimSpace(req.WorkspaceID); ws != "" {
		runCtx = WithTrustedWorkspaceID(runCtx, ws)
	}
	// Nested SubWorkflow conduits need the decision even when compose marks only
	// the descendant interrupt ID as the address target (hasData=false on parent).
	runCtx = WithPendingApprovalDecision(runCtx, decision)

	if len(interruptIDs) == 0 {
		return WorkflowRunResult{}, errors.New("einoruntime: interrupt id is required for approval resume")
	}
	if len(interruptIDs) == 1 {
		runCtx = compose.ResumeWithData(runCtx, interruptIDs[0], decision)
	} else {
		targets := make(map[string]any, len(interruptIDs))
		// Root-cause first entry receives the decision payload.
		for i, id := range interruptIDs {
			if i == 0 {
				targets[id] = decision
			} else {
				targets[id] = nil
			}
		}
		runCtx = compose.BatchResumeWithData(runCtx, targets)
	}

	// GraphInput is required by the Runnable type; Start node is not re-run on resume
	// (checkpoint restores node progress). Values here are mostly unused on pure resume.
	input := GraphInput{
		ExecutionID:     req.WorkflowExecutionID,
		WorkspaceID:     req.WorkspaceID,
		WorkflowID:      req.Plan.WorkflowID,
		WorkflowVersion: req.WorkflowVersion,
		UserID:          req.UserID,
		Input:           cloneAnyMap(req.Input),
	}

	result, invokeErr := graph.Runnable.Invoke(runCtx, input, compose.WithCheckPointID(checkPointID))
	out, err := r.mapInvokeOutcome(state, checkPointID, result, invokeErr)
	return out, err
}

func (r *CoreGraphRunner) loadGraph(ctx context.Context, req WorkflowRunRequest) (*CompiledWorkflowGraph, error) {
	key := strings.TrimSpace(req.CacheKey)
	if key == "" {
		key = CacheKeyFor(
			req.WorkspaceID,
			defaultString(req.RevisionID, req.WorkflowVersion),
			req.PlanHash,
			r.engine,
		)
	}
	if r.cache == nil {
		return BuildWorkflowGraph(ctx, req.Plan, GraphBuildConfig{
			Invoker:          r.invoker,
			RevisionResolver: r.revisionResolver,
			CheckPointStore:  r.store,
			Engine:           r.engine,
		})
	}
	// Ensure build config tracks runner deps.
	r.cache.BuildConfig.Invoker = r.invoker
	r.cache.BuildConfig.RevisionResolver = r.revisionResolver
	r.cache.BuildConfig.CheckPointStore = r.store
	r.cache.BuildConfig.Engine = r.engine
	return r.cache.GetOrBuild(ctx, key, req.Plan)
}

func (r *CoreGraphRunner) mapInvokeOutcome(
	state *GraphState,
	checkPointID string,
	result GraphResult,
	invokeErr error,
) (WorkflowRunResult, error) {
	if invokeErr == nil {
		exec := result.Execution
		if exec.ID == "" && state != nil {
			exec = state.toExecution()
		}
		return WorkflowRunResult{
			Execution:    exec,
			CheckPointID: checkPointID,
			State:        state,
		}, nil
	}

	info, isInterrupt := compose.ExtractInterruptInfo(invokeErr)
	if !isInterrupt {
		// Non-interrupt failure: project state if available.
		if state != nil && state.ExecutionID != "" {
			if state.Status == domain.ExecutionSuccess {
				state.Status = domain.ExecutionFailed
			}
			if state.ErrorMessage == "" {
				state.ErrorMessage = invokeErr.Error()
			}
			return WorkflowRunResult{
				Execution:    state.toExecution(),
				CheckPointID: checkPointID,
				State:        state,
			}, invokeErr
		}
		return WorkflowRunResult{CheckPointID: checkPointID}, invokeErr
	}

	// Approval / HITL pause: return Approval execution without treating as hard error.
	// Nested SubWorkflow CompositeInterrupt still exposes child Approval as root-cause Info.
	var approval *ApprovalInterruptState
	var interruptIDs []string
	if info != nil {
		for _, ic := range info.InterruptContexts {
			if ic == nil {
				continue
			}
			interruptIDs = append(interruptIDs, ic.ID)
			if ic.IsRootCause {
				if st, ok := ic.Info.(ApprovalInterruptState); ok {
					approval = &st
				} else if st, ok := ic.Info.(*ApprovalInterruptState); ok {
					approval = st
				}
			}
		}
		// Prefer root-cause first in InterruptIDs for resume helpers.
		if info.InterruptContexts != nil {
			rootFirst := make([]string, 0, len(interruptIDs))
			rest := make([]string, 0, len(interruptIDs))
			for _, ic := range info.InterruptContexts {
				if ic == nil {
					continue
				}
				if ic.IsRootCause {
					rootFirst = append(rootFirst, ic.ID)
				} else {
					rest = append(rest, ic.ID)
				}
			}
			interruptIDs = append(rootFirst, rest...)
		}
	}

	if approval == nil && state != nil && state.PendingApprovalNodeID != "" {
		approval = &ApprovalInterruptState{
			SchemaVersion: ApprovalInterruptSchemaVersion,
			NodeID:        state.PendingApprovalNodeID,
			ExecutionID:   state.ExecutionID,
			WorkflowID:    state.WorkflowID,
			WorkspaceID:   state.WorkspaceID,
			Reason:        state.PendingApprovalReason,
			RequestedBy:   state.UserID,
		}
	}

	if state != nil {
		if state.Status != domain.ExecutionApproval {
			state.Status = domain.ExecutionApproval
		}
		if state.OutputSummary == "" {
			state.OutputSummary = "Workflow trial run is blocked by Approval node"
		}
		return WorkflowRunResult{
			Execution:    state.toExecution(),
			Interrupted:  true,
			CheckPointID: checkPointID,
			InterruptIDs: interruptIDs,
			Approval:     approval,
			State:        state,
			InterruptErr: invokeErr,
		}, nil
	}

	return WorkflowRunResult{
		Interrupted:  true,
		CheckPointID: checkPointID,
		InterruptIDs: interruptIDs,
		Approval:     approval,
		InterruptErr: invokeErr,
	}, nil
}

// FormatWorkflowCheckpointID builds ws/{workspaceID}/workflow_exec/{executionID}/{nonce}.
func FormatWorkflowCheckpointID(workspaceID, executionID, nonce string) string {
	return fmt.Sprintf("ws/%s/%s/%s/%s",
		strings.TrimSpace(workspaceID),
		checkpointPathKindWorkflowExec,
		strings.TrimSpace(executionID),
		strings.TrimSpace(nonce),
	)
}

func newWorkflowCheckpointNonce() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		// Fallback for rare uuid failures.
		return fmt.Sprintf("%d", time.Now().UnixNano()), nil
	}
	return id.String(), nil
}
