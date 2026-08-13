package agentdelegation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// RunContext carries root-run identity for audit and budget (context.Value).
type RunContext struct {
	WorkspaceID string
	ParentRunID string
	RootRunID   string
	// RunID is the agent_run currently executing (root or TASK child).
	RunID              string
	CallerAgentID      string
	ParentDelegationID *string
	ParentStepID       *string
	Depth              int
	Budget             *Budget
	Binding            GraphEdgeSnapshot
	// TraceID shared across the call tree.
	TraceID string
}

type runContextKey struct{}

func WithRunContext(ctx context.Context, rc *RunContext) context.Context {
	return context.WithValue(ctx, runContextKey{}, rc)
}

func RunContextFrom(ctx context.Context) (*RunContext, bool) {
	rc, ok := ctx.Value(runContextKey{}).(*RunContext)
	return rc, ok && rc != nil
}

// AuditWriter is the fail-closed prewrite / finalize / dispatch surface.
// Production paths require full implementation — DispatchAuditor methods are not optional.
type AuditWriter interface {
	CreateDelegationAndStep(ctx context.Context, input CreateDelegationInput) (Delegation, bool, error)
	FinalizeDelegation(ctx context.Context, input FinalizeDelegationInput) (Delegation, error)
	// SetChildRunID links a TASK child run once (NULL → value).
	SetChildRunID(ctx context.Context, workspaceID, delegationID, childRunID string) error
	// RecordDispatchAttempt counts one real agent execution (not idempotent replay,
	// not finalize-outbox retries). Required before any real agent/remote invoke.
	RecordDispatchAttempt(ctx context.Context, workspaceID, delegationID string) error
	// AccumulateModelTokens sums one nested MODEL turn's usage under a delegation.
	// Missing/failing must fail-closed (never silently skip).
	AccumulateModelTokens(ctx context.Context, workspaceID, delegationID string, usage TokenUsage) error
}

// DispatchAuditor is an alias of the attempt/token methods for callers that only need them.
// Prefer AuditWriter; both are required in production.
type DispatchAuditor interface {
	RecordDispatchAttempt(ctx context.Context, workspaceID, delegationID string) error
	AccumulateModelTokens(ctx context.Context, workspaceID, delegationID string, usage TokenUsage) error
}

// AsDispatchAuditor returns the AuditWriter as DispatchAuditor (always non-nil when a != nil).
func AsDispatchAuditor(a AuditWriter) DispatchAuditor {
	if a == nil {
		return nil
	}
	return a
}

// ChildRunStore creates and finishes independent TASK-mode agent runs.
type ChildRunStore interface {
	// StartChild creates a RUNNING agent_run with parent_run_id / parent_delegation_id.
	StartChild(ctx context.Context, in ChildRunStartInput) (childRunID string, err error)
	// FinishChild transitions child to a terminal status.
	FinishChild(ctx context.Context, workspaceID, childRunID, status, errorCode string, output json.RawMessage) error
	// CancelChild marks child CANCELLED if still non-terminal.
	CancelChild(ctx context.Context, workspaceID, childRunID string) error
}

// ChildRunStartInput seeds a TASK-mode child AgentRun.
type ChildRunStartInput struct {
	WorkspaceID        string
	ParentRunID        string
	ParentDelegationID string
	TargetAgentID      string
	TraceID            string
	TriggeredByType    string
	TriggeredByID      string
	// GraphSnapshot is the immutable subtree freeze for this child (may be full tree).
	GraphSnapshot      json.RawMessage
	CapabilitySnapshot json.RawMessage
	ModelSnapshot      json.RawMessage
	AgentSnapshot      json.RawMessage
	InputSummary       json.RawMessage
	// Timeout bounds the child execution (0 → default 5m).
	Timeout time.Duration
}

// AgentToolConfig wraps an Eino AgentTool with audit + budget + optional TASK child runs.
type AgentToolConfig struct {
	Inner                tool.InvokableTool
	Name                 string
	Description          string
	Edge                 GraphEdgeSnapshot
	Audit                AuditWriter
	DefaultCallerAgentID string
	Protocol             string
	Origin               string
	// ChildRuns required when Edge.Mode == TASK.
	ChildRuns ChildRunStore
	// TargetSnapshots freezes the target agent's prompt/model/capability/agent
	// identity for TASK child agent_runs (must not copy parent A's snapshots).
	TargetSnapshots ChildRunStartInput
	// DefaultTaskTimeout when Edge has no per-binding timeout.
	DefaultTaskTimeout time.Duration
	// FinalizeRetries for durable terminal write (default 5).
	FinalizeRetries int
	// EnqueueFinalizeOutbox durable recovery when finalize retries exhaust.
	// Signature matches a2agateway.Repository.EnqueueFinalizeOutbox without import cycle.
	EnqueueFinalizeOutbox func(ctx context.Context, workspaceID, delegationID, stepID string, payload json.RawMessage) error
}

// AuditedAgentTool never dispatches without audit rows.
type AuditedAgentTool struct {
	cfg AgentToolConfig
	mu  sync.Mutex
}

func NewAuditedAgentTool(cfg AgentToolConfig) (*AuditedAgentTool, error) {
	if cfg.Inner == nil {
		return nil, fmt.Errorf("agentdelegation: Inner tool is required")
	}
	if cfg.Audit == nil {
		return nil, fmt.Errorf("agentdelegation: Audit writer is required")
	}
	if strings.TrimSpace(cfg.Name) == "" {
		if info, err := cfg.Inner.Info(context.Background()); err == nil && info != nil {
			cfg.Name = info.Name
			if cfg.Description == "" {
				cfg.Description = info.Desc
			}
		}
	}
	if strings.TrimSpace(cfg.Name) == "" {
		cfg.Name = strings.TrimSpace(cfg.Edge.CallableName)
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("agentdelegation: tool name is required")
	}
	if cfg.Protocol == "" {
		cfg.Protocol = ProtocolInternal
	}
	if cfg.Origin == "" {
		cfg.Origin = OriginInternal
	}
	mode := firstNonEmpty(cfg.Edge.Mode, ModeInline)
	if mode == ModeTask && cfg.ChildRuns == nil {
		return nil, fmt.Errorf("agentdelegation: ChildRuns store required for TASK mode")
	}
	if cfg.DefaultTaskTimeout <= 0 {
		cfg.DefaultTaskTimeout = 5 * time.Minute
	}
	if cfg.FinalizeRetries <= 0 {
		cfg.FinalizeRetries = 5
	}
	// Only TASK_ONLY context is implemented.
	if pol := strings.TrimSpace(cfg.Edge.ContextPolicy); pol != "" && pol != ContextTaskOnly {
		return nil, fmt.Errorf("%w: context_policy %q not supported (only TASK_ONLY)", ErrInvalid, pol)
	}
	return &AuditedAgentTool{cfg: cfg}, nil
}

func (t *AuditedAgentTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	desc := strings.TrimSpace(t.cfg.Description)
	if desc == "" {
		desc = strings.TrimSpace(t.cfg.Edge.Description)
	}
	if desc == "" {
		if info, err := t.cfg.Inner.Info(ctx); err == nil && info != nil {
			desc = info.Desc
		}
	}
	if desc == "" {
		desc = "Delegate task to agent " + t.cfg.Edge.TargetAgentID
	}
	return &schema.ToolInfo{
		Name: t.cfg.Name,
		Desc: desc,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"request": {
				Type:     schema.String,
				Desc:     "Structured task for the sub-agent (TASK_ONLY: no caller system prompt or secrets)",
				Required: true,
			},
		}),
	}, nil
}

func (t *AuditedAgentTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	rc, _ := RunContextFrom(ctx)
	caller := t.cfg.DefaultCallerAgentID
	parentRunID, workspaceID, rootRunID, traceID := "", "", "", ""
	depth := 1
	var budget *Budget
	var parentDel, parentStep *string
	if rc != nil {
		if rc.CallerAgentID != "" {
			caller = rc.CallerAgentID
		}
		parentRunID = firstNonEmpty(rc.RunID, rc.ParentRunID)
		workspaceID = rc.WorkspaceID
		rootRunID = firstNonEmpty(rc.RootRunID, parentRunID)
		traceID = rc.TraceID
		depth = rc.Depth + 1
		if depth <= 0 {
			depth = 1
		}
		budget = rc.Budget
		parentDel = rc.ParentDelegationID
		parentStep = rc.ParentStepID
	}
	bindingKey := t.cfg.Edge.BindingID
	if bindingKey == "" {
		bindingKey = t.cfg.Name
	}
	// Atomic reserve before any durable prewrite / child / remote work so parallel
	// ToolsNode invocations cannot oversubscribe total/per-binding slots.
	// On budget reject we never take a slot; we still write FAILED audit evidence when
	// workspace/run/caller are known (model already initiated the tool call).
	reserved := false
	if budget != nil {
		if err := budget.CheckAndReserve(depth, bindingKey); err != nil {
			return t.recordBudgetRejection(ctx, err, workspaceID, parentRunID, caller, depth, argumentsInJSON), nil
		}
		reserved = true
		defer func() {
			if reserved {
				budget.Release(bindingKey)
			}
		}()
	}
	if workspaceID == "" || parentRunID == "" || caller == "" {
		return formatDelegationError(ErrInvalid), nil
	}

	mode := firstNonEmpty(t.cfg.Edge.Mode, ModeInline)
	toolCallID := extractToolCallID(ctx, opts)
	idem := IdempotencyKey(parentRunID, toolCallID, t.cfg.Edge.Version, t.cfg.Edge.BindingID)

	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	taskText := extractRequest(argumentsInJSON)
	var targetPtr *string
	if tid := strings.TrimSpace(t.cfg.Edge.TargetAgentID); tid != "" {
		targetPtr = &tid
	}
	var externalPtr *string
	if ref := strings.TrimSpace(t.cfg.Edge.ExternalRef); ref != "" {
		externalPtr = &ref
	}
	// Authoritative identity fields in summary so UI works even before JOIN.
	inputSummary, _ := json.Marshal(map[string]any{
		"source": "agentdelegation", "callableName": t.cfg.Name,
		"mode": mode, "protocol": t.cfg.Protocol, "origin": t.cfg.Origin,
		"requestPreview": truncate(taskText, 500),
		"callerAgentId":  caller, "targetAgentId": firstNonEmpty(t.cfg.Edge.TargetAgentID),
		"externalAgentRef": firstNonEmpty(t.cfg.Edge.ExternalRef),
		"depth":            depth, "bindingVersion": t.cfg.Edge.Version,
	})
	inputPayload, _ := json.Marshal(map[string]any{"request": truncate(taskText, 8000)})

	del, replay, err := t.cfg.Audit.CreateDelegationAndStep(ctx, CreateDelegationInput{
		ID: delID, WorkspaceID: workspaceID, ParentRunID: parentRunID,
		ParentDelegationID: parentDel, CallerAgentID: caller, TargetAgentID: targetPtr,
		ExternalAgentRef: externalPtr, Mode: mode, Protocol: t.cfg.Protocol, Origin: t.cfg.Origin,
		Depth: depth, BindingVersion: t.cfg.Edge.Version,
		ToolCallID: toolCallID, IdempotencyKey: idem,
		InputSummary: inputSummary, InputPayload: inputPayload,
		StepID: stepID, AgentID: caller, ParentStepID: parentStep,
	})
	if err != nil {
		return formatDelegationError(fmt.Errorf("%w: %v", ErrAuditPrewriteFailed, err)), nil
	}
	if replay {
		// Idempotent replay is NOT a dispatch attempt/retry — release reservation.
		if isTerminal(del.Status) {
			if del.Status == StatusSucceeded {
				return extractResultString(del.OutputPayload), nil
			}
			return formatDelegationError(fmt.Errorf("delegation %s", del.Status)), nil
		}
		return formatDelegationError(ErrIdempotentReplay), nil
	}
	if t.cfg.Audit == nil {
		return formatDelegationError(fmt.Errorf("dispatch auditor required")), nil
	}

	// TASK: create independent child agent_run BEFORE counting a dispatch attempt.
	// attempt_count = actual agent dispatch only (not failed child create/link).
	var childRunID string
	execRunID := parentRunID // where nested MODEL/TOOL steps are attributed for INLINE
	if mode == ModeTask {
		if t.cfg.ChildRuns == nil {
			finErr := t.finalizeWithRetry(ctx, workspaceID, del.ID, del.StepID, StatusFailed,
				"DELEGATION_TASK_MISCONFIGURED", "TASK mode requires ChildRunStore", nil, nil)
			return formatDelegationError(errors.Join(ErrInvalid, finErr)), nil
		}
		timeout := t.cfg.DefaultTaskTimeout
		startIn := ChildRunStartInput{
			WorkspaceID: workspaceID, ParentRunID: parentRunID, ParentDelegationID: del.ID,
			TargetAgentID:   firstNonEmpty(t.cfg.Edge.TargetAgentID),
			TraceID:         firstNonEmpty(traceID, rootRunID, parentRunID),
			TriggeredByType: "SYSTEM", TriggeredByID: caller,
			InputSummary: inputSummary, Timeout: timeout,
			// Prefer frozen target-node snapshots; never silently inherit A's model/agent.
			GraphSnapshot:      t.cfg.TargetSnapshots.GraphSnapshot,
			CapabilitySnapshot: t.cfg.TargetSnapshots.CapabilitySnapshot,
			ModelSnapshot:      t.cfg.TargetSnapshots.ModelSnapshot,
			AgentSnapshot:      t.cfg.TargetSnapshots.AgentSnapshot,
		}
		cid, startErr := t.cfg.ChildRuns.StartChild(ctx, startIn)
		if startErr != nil {
			// No attempt yet — child never dispatched; release budget via defer.
			// Parent cancel/timeout can fail StartChild the same way as attempt
			// record; classify from ctx so the delegation is not FAILED.
			return t.abortPreDispatch(ctx, workspaceID, del.ID, firstNonEmpty(del.StepID, stepID),
				"", startErr, "DELEGATION_CHILD_START_FAILED"), nil
		}
		childRunID = cid
		if err := t.cfg.Audit.SetChildRunID(ctx, workspaceID, del.ID, childRunID); err != nil {
			// Child exists; link/audit failed or parent cancelled during the link.
			// Persist with WithoutCancel so a cancelled drive ctx cannot skip FinishChild.
			return t.abortPreDispatch(ctx, workspaceID, del.ID, firstNonEmpty(del.StepID, stepID),
				childRunID, err, "DELEGATION_LINK_FAILED"), nil
		}
		execRunID = childRunID
		// Bound TASK execution by timeout; parent cancel still cancels via ctx.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Real agent dispatch: count attempt only immediately before Inner.InvokableRun.
	if aerr := t.cfg.Audit.RecordDispatchAttempt(ctx, workspaceID, del.ID); aerr != nil {
		return t.abortPreDispatch(ctx, workspaceID, del.ID, firstNonEmpty(del.StepID, stepID),
			childRunID, aerr, "DELEGATION_ATTEMPT_RECORD_FAILED"), nil
	}
	// Dispatch is real: reservation is permanently consumed (AttemptCount already incremented).
	reserved = false

	// parent_step_id FK is (workspace_id, run_id, parent_step_id) — same run only.
	// INLINE: nested MODEL/TOOL share parent run → set ParentStepID for tree nesting.
	// TASK: nested steps land on child run_id; nest via delegation_id only (no cross-run parent_step_id).
	var parentStepForNested *string
	if mode != ModeTask {
		parentStepForNested = strPtr(firstNonEmpty(del.StepID, stepID))
	}
	childRC := &RunContext{
		WorkspaceID: workspaceID,
		ParentRunID: parentRunID,
		RootRunID:   firstNonEmpty(rootRunID, parentRunID),
		RunID:       execRunID,
		// Nested steps belong to the target agent under this delegation.
		CallerAgentID:      firstNonEmpty(t.cfg.Edge.TargetAgentID, caller),
		ParentDelegationID: strPtr(del.ID),
		ParentStepID:       parentStepForNested,
		Depth:              depth,
		Budget:             budget,
		Binding:            t.cfg.Edge,
		TraceID:            firstNonEmpty(traceID, rootRunID, parentRunID),
	}
	childCtx := WithRunContext(ctx, childRC)

	result, invokeErr := t.cfg.Inner.InvokableRun(childCtx, argumentsInJSON, opts...)

	// Prefer context cancellation/timeout even when inner returns nil error
	// (some tools swallow ctx errors).
	finalStatus := StatusSucceeded
	errCode, errMsg := "", ""
	if ctx.Err() == context.DeadlineExceeded {
		finalStatus = StatusTimedOut
		errCode = "DELEGATION_TIMED_OUT"
		errMsg = "delegation timed out"
		if invokeErr == nil {
			invokeErr = ctx.Err()
		}
	} else if ctx.Err() != nil {
		finalStatus = StatusCancelled
		errCode = "DELEGATION_CANCELLED"
		errMsg = "parent context cancelled"
		if invokeErr == nil {
			invokeErr = ctx.Err()
		}
	} else if invokeErr != nil {
		finalStatus = StatusFailed
		errCode = "DELEGATION_INVOKE_FAILED"
		errMsg = truncate(invokeErr.Error(), 500)
	}

	// Finish TASK child run first (independent lifecycle). Fail closed: if
	// FinishChild fails while we would otherwise SUCCEEDED, downgrade to FAILED.
	if mode == ModeTask && childRunID != "" && t.cfg.ChildRuns != nil {
		out := json.RawMessage(`{}`)
		if finalStatus == StatusSucceeded {
			out, _ = json.Marshal(map[string]any{"result": truncate(result, 8000)})
		}
		if finErr := t.cfg.ChildRuns.FinishChild(context.WithoutCancel(ctx), workspaceID, childRunID, finalStatus, errCode, out); finErr != nil {
			if finalStatus == StatusSucceeded {
				finalStatus = StatusFailed
				errCode = "DELEGATION_CHILD_FINISH_FAILED"
			}
			errMsg = truncate(finErr.Error(), 500)
			if invokeErr == nil {
				invokeErr = finErr
			}
		}
	}

	outSummary, _ := json.Marshal(map[string]any{
		"status": finalStatus, "ok": finalStatus == StatusSucceeded, "mode": mode,
		"errorCode": errCode, "message": errMsg,
	})
	outPayload, _ := json.Marshal(map[string]any{"result": truncate(result, 16000)})
	var childPtr *string
	if childRunID != "" {
		childPtr = &childRunID
	}
	if finErr := t.finalizeWithRetry(context.WithoutCancel(ctx), workspaceID, del.ID, firstNonEmpty(del.StepID, stepID),
		finalStatus, errCode, errMsg, childPtr, &finalizePayloads{summary: outSummary, payload: outPayload}); finErr != nil {
		// Preserve both invoke failure and finalize/outbox failure causality.
		return formatDelegationError(errors.Join(invokeErr, fmt.Errorf("finalize: %w", finErr))), nil
	}
	if invokeErr != nil {
		return formatDelegationError(invokeErr), nil
	}
	return result, nil
}

type finalizePayloads struct {
	summary, payload json.RawMessage
}

// recordBudgetRejection writes a terminal FAILED AGENT_DELEGATION frame without
// consuming a budget slot and without calling Inner. Requires workspace/run/caller;
// otherwise returns the budget error only (cannot safely audit across tenants).
//
// Idempotent safety: if CreateDelegationAndStep returns replay=true, this path
// NEVER finalizes or rewrites the existing row (RUNNING may still be mid-dispatch).
func (t *AuditedAgentTool) recordBudgetRejection(
	ctx context.Context, budgetErr error,
	workspaceID, parentRunID, caller string, depth int, argumentsInJSON string,
) string {
	if workspaceID == "" || parentRunID == "" || caller == "" || t.cfg.Audit == nil {
		return formatDelegationError(budgetErr)
	}
	mode := firstNonEmpty(t.cfg.Edge.Mode, ModeInline)
	toolCallID := extractToolCallID(ctx, nil)
	idem := IdempotencyKey(parentRunID, toolCallID, t.cfg.Edge.Version, t.cfg.Edge.BindingID)
	errCode := budgetErrorCode(budgetErr)
	errMsg := truncate(budgetErr.Error(), 500)

	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	taskText := extractRequest(argumentsInJSON)
	var targetPtr *string
	if tid := strings.TrimSpace(t.cfg.Edge.TargetAgentID); tid != "" {
		targetPtr = &tid
	}
	var externalPtr *string
	if ref := strings.TrimSpace(t.cfg.Edge.ExternalRef); ref != "" {
		externalPtr = &ref
	}
	var parentDel, parentStep *string
	if rc, ok := RunContextFrom(ctx); ok && rc != nil {
		parentDel = rc.ParentDelegationID
		parentStep = rc.ParentStepID
	}
	inputSummary, _ := json.Marshal(map[string]any{
		"source": "agentdelegation", "callableName": t.cfg.Name,
		"mode": mode, "protocol": t.cfg.Protocol, "origin": t.cfg.Origin,
		"requestPreview": truncate(taskText, 500),
		"callerAgentId":  caller, "targetAgentId": firstNonEmpty(t.cfg.Edge.TargetAgentID),
		"externalAgentRef": firstNonEmpty(t.cfg.Edge.ExternalRef),
		"depth":            depth, "bindingVersion": t.cfg.Edge.Version,
		"budgetRejected": true,
	})
	inputPayload, _ := json.Marshal(map[string]any{"request": truncate(taskText, 8000)})

	del, replay, err := t.cfg.Audit.CreateDelegationAndStep(ctx, CreateDelegationInput{
		ID: delID, WorkspaceID: workspaceID, ParentRunID: parentRunID,
		ParentDelegationID: parentDel, CallerAgentID: caller, TargetAgentID: targetPtr,
		ExternalAgentRef: externalPtr, Mode: mode, Protocol: t.cfg.Protocol, Origin: t.cfg.Origin,
		Depth: depth, BindingVersion: t.cfg.Edge.Version,
		ToolCallID: toolCallID, IdempotencyKey: idem,
		InputSummary: inputSummary, InputPayload: inputPayload,
		StepID: stepID, AgentID: caller, ParentStepID: parentStep,
	})
	if err != nil {
		// Fail closed like main path: surface audit prewrite failure (not a durable
		// budget-reject row). Keep budget cause in message only.
		return formatDelegationError(fmt.Errorf("%w: %v (budget: %v)", ErrAuditPrewriteFailed, err, budgetErr))
	}
	if replay {
		// Never finalize/rewrite existing evidence — a concurrent first call may still
		// be RUNNING mid-Inner; forcing FAILED would race terminal immutability.
		return replayBudgetRejectionResponse(del)
	}

	// New row for this tool-call only: finalize FAILED with stable budget code, 0/0 attempts.
	outSummary, _ := json.Marshal(map[string]any{
		"status": StatusFailed, "ok": false, "mode": mode,
		"errorCode": errCode, "message": errMsg,
	})
	outPayload, _ := json.Marshal(map[string]any{"result": ""})
	if finErr := t.finalizeWithRetry(context.WithoutCancel(ctx), workspaceID, del.ID, firstNonEmpty(del.StepID, stepID),
		StatusFailed, errCode, errMsg, nil, &finalizePayloads{summary: outSummary, payload: outPayload}); finErr != nil {
		// Preserve budget stable code via errors.Is; message includes finalize causality.
		return formatDelegationError(errors.Join(budgetErr, fmt.Errorf("finalize: %w", finErr)))
	}
	return formatDelegationError(budgetErr)
}

// replayBudgetRejectionResponse maps an existing idempotent row without mutation.
// Matches normal InvokableRun replay semantics for SUCCEEDED / terminal failure / RUNNING.
func replayBudgetRejectionResponse(del Delegation) string {
	if del.Status == StatusSucceeded {
		return extractResultString(del.OutputPayload)
	}
	if isTerminal(del.Status) {
		// Existing terminal evidence — do not rebrand as a fresh budget rejection.
		code := strings.TrimSpace(del.ErrorCode)
		if code == "" {
			switch del.Status {
			case StatusTimedOut:
				code = "DELEGATION_TIMED_OUT"
			case StatusCancelled:
				code = "DELEGATION_CANCELLED"
			default:
				code = "DELEGATION_FAILED"
			}
		}
		msg := strings.TrimSpace(del.ErrorMessage)
		if msg == "" {
			msg = "delegation " + del.Status
		}
		body, _ := json.Marshal(map[string]any{
			"ok": false, "errorCode": code, "message": truncate(msg, 500),
		})
		return string(body)
	}
	// RUNNING (or other non-terminal): concurrent first call still in flight.
	return formatDelegationError(ErrIdempotentReplay)
}

// budgetErrorCode maps budget sentinels to stable audit/step error_code values.
func budgetErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrDepthExceeded):
		return "DELEGATION_DEPTH_EXCEEDED"
	case errors.Is(err, ErrTotalBudgetExceeded):
		return "DELEGATION_TOTAL_BUDGET_EXCEEDED"
	case errors.Is(err, ErrBindingBudgetExceeded):
		return "DELEGATION_BINDING_BUDGET_EXCEEDED"
	default:
		return "DELEGATION_FAILED"
	}
}

func (t *AuditedAgentTool) finalizeWithRetry(
	ctx context.Context, workspaceID, delID, stepID, status, errCode, errMsg string,
	childRunID *string, payloads *finalizePayloads,
) error {
	if payloads == nil {
		payloads = &finalizePayloads{
			summary: json.RawMessage(`{}`), payload: json.RawMessage(`{}`),
		}
	}
	var last error
	retries := t.cfg.FinalizeRetries
	if retries <= 0 {
		retries = 5
	}
	in := FinalizeDelegationInput{
		WorkspaceID: workspaceID, DelegationID: delID, StepID: stepID,
		Status: status, OutputSummary: payloads.summary, OutputPayload: payloads.payload,
		ErrorCode: errCode, ErrorMessage: errMsg, ChildRunID: childRunID,
	}
	for i := 0; i < retries; i++ {
		_, err := t.cfg.Audit.FinalizeDelegation(ctx, in)
		if err == nil {
			return nil
		}
		last = err
		time.Sleep(time.Duration(20*(i+1)) * time.Millisecond)
	}
	// Durable path: enqueue outbox so RUNNING is not left without recovery.
	// Only report "deferred" when enqueue succeeds; otherwise Join both errors.
	if t.cfg.EnqueueFinalizeOutbox != nil {
		payload, _ := json.Marshal(in)
		if qerr := t.cfg.EnqueueFinalizeOutbox(ctx, workspaceID, delID, stepID, payload); qerr != nil {
			return errors.Join(last, fmt.Errorf("enqueue finalize outbox: %w", qerr))
		}
		// Enqueued for deferred recovery — still surface finalize failure.
		return fmt.Errorf("finalize deferred via outbox: %w", last)
	}
	return last
}

var (
	_ tool.BaseTool      = (*AuditedAgentTool)(nil)
	_ tool.InvokableTool = (*AuditedAgentTool)(nil)
)

func IdempotencyKey(parentRunID, toolCallID string, bindingVersion int64, bindingID string) string {
	return fmt.Sprintf("%s:%s:%d:%s",
		strings.TrimSpace(parentRunID), strings.TrimSpace(toolCallID),
		bindingVersion, strings.TrimSpace(bindingID),
	)
}

func extractRequest(argumentsInJSON string) string {
	argumentsInJSON = strings.TrimSpace(argumentsInJSON)
	if argumentsInJSON == "" {
		return ""
	}
	var m map[string]any
	if json.Unmarshal([]byte(argumentsInJSON), &m) != nil {
		return truncate(argumentsInJSON, 8000)
	}
	if v, ok := m["request"].(string); ok {
		return strings.TrimSpace(v)
	}
	if v, ok := m["task"].(string); ok {
		return strings.TrimSpace(v)
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}

// extractToolCallID uses Eino compose.GetToolCallID (set by ToolsNode) for stable IDs.
func extractToolCallID(ctx context.Context, _ []tool.Option) string {
	if id := strings.TrimSpace(compose.GetToolCallID(ctx)); id != "" {
		return id
	}
	// Fail closed for idempotency: without a stable call id, use deterministic
	// empty marker so callers that retry without ToolsNode still collide on idempotency
	// only when they also share the same explicit injection — prefer never inventing
	// a random UUID that breaks retry dedupe.
	return "missing-tool-call-id"
}

func extractResultString(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(payload, &m) == nil {
		if v, ok := m["result"].(string); ok {
			return v
		}
	}
	return string(payload)
}

// preDispatchAbort classifies a failed StartChild / SetChildRunID /
// RecordDispatchAttempt. Cancel/timeout of the parent must not be recorded as a
// generic start/link/audit failure, or TASK child / delegation / step disagree.
func preDispatchAbort(ctx context.Context, err error, failedCode string) (status, errCode, errMsg string) {
	if ctx != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return StatusTimedOut, "DELEGATION_TIMED_OUT", "delegation timed out"
		case errors.Is(ctx.Err(), context.Canceled):
			return StatusCancelled, "DELEGATION_CANCELLED", "parent context cancelled"
		}
	}
	msg := failedCode
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		msg = truncate(err.Error(), 500)
	}
	if failedCode == "" {
		failedCode = "DELEGATION_FAILED"
	}
	return StatusFailed, failedCode, msg
}

// attemptRecordAbort keeps the RecordDispatchAttempt classification name used
// by tests; it is preDispatchAbort with the attempt-record failure code.
func attemptRecordAbort(ctx context.Context, aerr error) (status, errCode, errMsg string) {
	return preDispatchAbort(ctx, aerr, "DELEGATION_ATTEMPT_RECORD_FAILED")
}

// abortPreDispatch finishes a TASK child (if any) and the delegation after a
// pre-Inner abort. Persist uses WithoutCancel so a cancelled drive ctx cannot
// skip FinishChild / finalize.
func (t *AuditedAgentTool) abortPreDispatch(
	ctx context.Context,
	workspaceID, delegationID, stepID, childRunID string,
	cause error,
	failedCode string,
) string {
	finalStatus, errCode, errMsg := preDispatchAbort(ctx, cause, failedCode)
	var finChildErr error
	if childRunID != "" && t.cfg.ChildRuns != nil {
		finChildErr = t.cfg.ChildRuns.FinishChild(context.WithoutCancel(ctx), workspaceID, childRunID,
			finalStatus, errCode, json.RawMessage(`{}`))
	}
	var childPtr *string
	if childRunID != "" {
		childPtr = &childRunID
	}
	finErr := t.finalizeWithRetry(context.WithoutCancel(ctx), workspaceID, delegationID, stepID,
		finalStatus, errCode, errMsg, childPtr, nil)
	wrapped := cause
	switch finalStatus {
	case StatusCancelled:
		wrapped = errors.Join(ErrCancelled, cause)
	case StatusTimedOut:
		wrapped = errors.Join(ErrTimedOut, cause)
	}
	return formatDelegationError(errors.Join(wrapped, finChildErr, finErr))
}

func formatDelegationError(err error) string {
	code := "DELEGATION_FAILED"
	msg := "delegation failed"
	if err != nil {
		msg = err.Error()
		// Stable codes only via errors.Is (unwrap %w / errors.Join). Never match
		// untrusted error text — remote/tool messages may contain sentinel substrings.
		switch {
		case errors.Is(err, ErrDepthExceeded):
			code = "DELEGATION_DEPTH_EXCEEDED"
		case errors.Is(err, ErrTotalBudgetExceeded):
			code = "DELEGATION_TOTAL_BUDGET_EXCEEDED"
		case errors.Is(err, ErrBindingBudgetExceeded):
			code = "DELEGATION_BINDING_BUDGET_EXCEEDED"
		case errors.Is(err, ErrAuditPrewriteFailed):
			code = "DELEGATION_AUDIT_PREWRITE_FAILED"
		case errors.Is(err, ErrCancelled):
			code = "DELEGATION_CANCELLED"
		case errors.Is(err, ErrIdempotentReplay):
			code = "DELEGATION_IDEMPOTENT_REPLAY"
		case errors.Is(err, ErrCycle):
			code = "DELEGATION_CYCLE"
		case errors.Is(err, ErrTimedOut):
			code = "DELEGATION_TIMED_OUT"
		}
	}
	body, _ := json.Marshal(map[string]any{
		"ok": false, "errorCode": code, "message": truncate(msg, 500),
	})
	return string(body)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func strPtr(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}
