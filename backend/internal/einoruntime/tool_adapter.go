package einoruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/principal"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// ResolvedInvoker is the thin pipeline surface used by PipelineTool.
// execution.InvocationPipeline implements this interface.
//
// Design HITL ownership (D5 / §3.6.3):
//   - First run, no confirm → InvokableTool calls InvokeResolved
//   - First run, needs confirm → nobody invokes; StatefulInterrupt only
//   - Approve/Dispatch → ToolConfirmationResumeExecutor only
//   - Eino resume → forbidden to invoke; return GetResumeContext data
type ResolvedInvoker interface {
	InvokeResolved(
		ctx context.Context,
		request execution.InvokeRequest,
		resolved execution.ResolvedInvocation,
	) (execution.PipelineResult, error)
}

// InvocationResolver resolves capability/connection readiness for a tool call.
// Production wiring uses the shared ToolInvoker; buildPipelineTools must not
// call this (ZKL-56 UX-02 lazy resolution).
type InvocationResolver interface {
	ResolveInvocation(context.Context, execution.ResolveRequest) (execution.ResolvedInvocation, error)
}

// PendingConfirmInterrupt is emitted immediately before StatefulInterrupt so
// chatruntimebridge can prepare platform confirmation rows without stuffing
// tool args into gob interrupt state (IDs-only gob contract, design Appendix B).
type PendingConfirmInterrupt struct {
	InvocationID string
	CapabilityID string
	ReleaseID    string
	StepID       string
	// ArgsJSON is the model-supplied tool arguments (not a secret).
	ArgsJSON string
	// ToolName is the callable name exposed to the model.
	ToolName string
	// AgentRunID correlates the pending confirm with the AgentRun.
	AgentRunID string
	// WorkspaceID is the tenant scope for the pending confirm.
	WorkspaceID string
	// Resolved is the non-sensitive resolution snapshot from the single
	// first-call ResolveInvocation. pauseForInterrupt must reuse it and must
	// not resolve again.
	Resolved execution.ResolvedInvocation
}

// ConfirmInterruptHook is invoked on first-run confirmation gate, before
// StatefulInterrupt returns. Nil is a no-op.
type ConfirmInterruptHook func(ctx context.Context, pending PendingConfirmInterrupt)

// ToolCompleteEvent is emitted after a real InvokeResolved (success or mapped
// tool-error result). Used by chatruntimebridge to persist agent_run_steps TOOL
// evidence for the platform-admin audit timeline.
type ToolCompleteEvent struct {
	WorkspaceID  string
	AgentRunID   string
	CapabilityID string
	ReleaseID    string
	InvocationID string
	ToolName     string
	// ArgsJSON is the model-supplied tool arguments (JSON object text).
	ArgsJSON string
	// ResultJSON is the tool result string returned to the model (ok/error shape).
	ResultJSON string
	// OK is false when the pipeline returned an error mapped into toolErrorResult,
	// or when args were invalid (no pipeline call).
	OK bool
	// ErrorCode is set when OK is false (pipeline ErrorCode or TOOL_ARGS_INVALID).
	ErrorCode string
}

// ToolCompleteHook is invoked after first-run InvokeResolved (or invalid args).
// Nil is a no-op. Errors from the hook are ignored so the model still receives
// the tool result string.
type ToolCompleteHook func(ctx context.Context, event ToolCompleteEvent)

// PipelineToolConfig constructs one InvokableTool bound to a capability release
// for a single agent run.
//
// Lazy resolution (ZKL-56 / UX-02):
//   - Prefer Resolver; ResolveInvocation runs only on first actual tool call.
//   - Resolved may be pre-set for unit tests without a Resolver.
//   - RequiresConfirmation is the capability-snapshot flag; OR with resolved
//     RequiresConfirmation after the single resolve.
type PipelineToolConfig struct {
	// Info is the Eino ToolInfo (from tooltranslator). Required; Name must be set.
	Info *schema.ToolInfo
	// Pipeline executes InvokeResolved on the no-confirm first-run path.
	Pipeline ResolvedInvoker
	// Resolver performs Connection/identity resolution on first actual call.
	// When nil, Resolved must already be populated (test path).
	Resolver InvocationResolver
	// RequiresConfirmation is the capability-snapshot flag only.
	RequiresConfirmation bool

	// Immutable invoke identity (IDs only — no secrets).
	WorkspaceID         string
	CapabilityID        string
	ReleaseID           string
	ActorType           string
	ActorID             string
	TraceID             string
	AgentRunID          string
	BindingConnectionID string
	// PrincipalSnapshot is required for SERVICE_PRINCIPAL actors (AAP).
	// USER/SYSTEM may omit it; the invocation pipeline synthesizes an internal snapshot.
	PrincipalSnapshot *principal.ExecutionSnapshot
	// AuthorizationSnapshot is optional evidence attached to the durable invocation.
	AuthorizationSnapshot json.RawMessage
	// Resolved is an optional pre-baked resolution for tests. Production
	// chatruntimebridge leaves this empty and supplies Resolver instead.
	Resolved execution.ResolvedInvocation

	// InvocationID, when set, is used for interrupt state / invoke request.
	// Empty → generated per InvokableRun (uuid v7).
	InvocationID string
	// StepID is optional agent-run step correlation stored on interrupt state.
	StepID string

	// OnConfirmInterrupt is optional; chatruntimebridge uses it to capture
	// tool args + non-sensitive resolved snapshot for platform confirmation prepare.
	OnConfirmInterrupt ConfirmInterruptHook
	// OnToolComplete is optional; chatruntimebridge persists TOOL agent_run_steps
	// for Agent 审计中心 (arguments + result).
	OnToolComplete ToolCompleteHook
}

// pipelineTool implements tool.InvokableTool with interrupt-before-invoke and
// resume-without-reinvoke semantics (design §3.6.3), plus lazy resolution (UX-02).
type pipelineTool struct {
	info                 *schema.ToolInfo
	pipeline             ResolvedInvoker
	resolver             InvocationResolver
	requiresConfirmation bool

	workspaceID           string
	capabilityID          string
	releaseID             string
	actorType             string
	actorID               string
	traceID               string
	agentRunID            string
	bindingConnectionID   string
	principalSnapshot     *principal.ExecutionSnapshot
	authorizationSnapshot json.RawMessage
	// resolved holds pre-baked test resolution or empty for lazy path.
	resolved          execution.ResolvedInvocation
	fixedInvocationID string
	stepID            string
	onConfirmInterrupt ConfirmInterruptHook
	onToolComplete     ToolCompleteHook
}

// Ensure pipelineTool satisfies InvokableTool at compile time.
var _ tool.InvokableTool = (*pipelineTool)(nil)

// NewPipelineTool builds an InvokableTool that routes execution through
// ResolvedInvoker with the HITL ownership rules from design §3.6.3 and
// lazy ResolveInvocation on first actual tool call (ZKL-56 UX-02).
func NewPipelineTool(cfg PipelineToolConfig) (tool.InvokableTool, error) {
	if cfg.Info == nil || strings.TrimSpace(cfg.Info.Name) == "" {
		return nil, errors.New("pipeline tool: Info with Name is required")
	}
	if cfg.Pipeline == nil {
		return nil, errors.New("pipeline tool: Pipeline invoker is required")
	}
	if cfg.Resolver == nil && strings.TrimSpace(cfg.Resolved.Snapshot.CapabilityID) == "" &&
		strings.TrimSpace(cfg.CapabilityID) == "" {
		// Allow empty Resolved for pure-metadata tests only when CapabilityID is set
		// for identity; resolve path will fail closed if both missing at call time.
	}
	return &pipelineTool{
		info:                  cfg.Info,
		pipeline:              cfg.Pipeline,
		resolver:              cfg.Resolver,
		requiresConfirmation:  cfg.RequiresConfirmation,
		workspaceID:           strings.TrimSpace(cfg.WorkspaceID),
		capabilityID:          strings.TrimSpace(cfg.CapabilityID),
		releaseID:             strings.TrimSpace(cfg.ReleaseID),
		actorType:             strings.TrimSpace(cfg.ActorType),
		actorID:               strings.TrimSpace(cfg.ActorID),
		traceID:               strings.TrimSpace(cfg.TraceID),
		agentRunID:            strings.TrimSpace(cfg.AgentRunID),
		bindingConnectionID:   strings.TrimSpace(cfg.BindingConnectionID),
		principalSnapshot:     cfg.PrincipalSnapshot,
		authorizationSnapshot: append(json.RawMessage(nil), cfg.AuthorizationSnapshot...),
		resolved:              cfg.Resolved,
		fixedInvocationID:     strings.TrimSpace(cfg.InvocationID),
		stepID:                strings.TrimSpace(cfg.StepID),
		onConfirmInterrupt:    cfg.OnConfirmInterrupt,
		onToolComplete:        cfg.OnToolComplete,
	}, nil
}

// Info returns the Eino ToolInfo exposed to the model.
func (t *pipelineTool) Info(context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

// InvokableRun implements tool.InvokableTool.
//
// Order (ZKL-56 §4.2.2 + design §3.6.3):
//
//	resume with platform data → return (no resolve, no invoke)
//	first call → invocationID → normalize args → resolve once →
//	  schema validate → confirm interrupt OR InvokeResolved
//	resolution failure → structured tool error (zero successful Invocation)
func (t *pipelineTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	wasInterrupted, _, st := readInterruptState(ctx)
	isTarget, hasData, data := readResumeContext(ctx)

	if wasInterrupted {
		if !isTarget {
			// Leaf must re-interrupt when a sibling is the resume target.
			return "", tool.StatefulInterrupt(ctx, toolConfirmInterruptInfo, st)
		}
		if hasData {
			// Platform already InvokeResolved during Dispatch — DO NOT resolve or invoke.
			return data, nil
		}
		return "", fmt.Errorf("eino tool resume missing result data")
	}

	// First run.
	invocationID, err := t.invocationID()
	if err != nil {
		return "", err
	}

	input, err := normalizeToolArgs(args)
	if err != nil {
		out := formatToolErrorResult("TOOL_ARGS_INVALID", err.Error())
		t.emitToolComplete(ctx, args, out, invocationID, false, "TOOL_ARGS_INVALID")
		return out, nil
	}

	resolved, resolveErr := t.resolveOnce(ctx)
	if resolveErr != nil {
		code, message := mapResolutionFailure(resolveErr)
		out := formatToolErrorResult(code, message)
		t.emitToolComplete(ctx, args, out, invocationID, false, code)
		return out, nil
	}

	// Same projection + schema rules as InvocationPipeline, before confirmation.
	// Empty schema skips pre-check (test fixtures / missing declaration); the
	// real InvocationPipeline still enforces schema on InvokeResolved.
	if len(resolved.Snapshot.InputSchema) > 0 {
		if projected, ok := execution.ProjectToolInputOntoSchema(resolved.Snapshot.InputSchema, input); ok {
			input = projected
		}
		if !execution.MatchToolInputSchema(ctx, resolved.Snapshot.InputSchema, input) {
			out := formatToolErrorResult("TOOL_ARGS_INVALID", "tool arguments failed input schema validation")
			t.emitToolComplete(ctx, args, out, invocationID, false, "TOOL_ARGS_INVALID")
			return out, nil
		}
	}

	needsConfirm := t.requiresConfirmation || resolved.RequiresConfirmation
	if needsConfirm {
		if t.onConfirmInterrupt != nil {
			toolName := ""
			if t.info != nil {
				toolName = t.info.Name
			}
			t.onConfirmInterrupt(ctx, PendingConfirmInterrupt{
				InvocationID: invocationID,
				CapabilityID: t.capabilityID,
				ReleaseID:    t.releaseID,
				StepID:       t.stepID,
				ArgsJSON:     args,
				ToolName:     toolName,
				AgentRunID:   t.agentRunID,
				WorkspaceID:  t.workspaceID,
				Resolved:     resolved,
			})
		}
		return "", tool.StatefulInterrupt(ctx, toolConfirmInterruptInfo, ToolConfirmInterruptState{
			SchemaVersion: ToolConfirmInterruptSchemaVersion,
			InvocationID:  invocationID,
			ReleaseID:     t.releaseID,
			CapabilityID:  t.capabilityID,
			StepID:        t.stepID,
		})
	}

	return t.invokeResolved(ctx, input, args, invocationID, resolved)
}

func (t *pipelineTool) resolveOnce(ctx context.Context) (execution.ResolvedInvocation, error) {
	if t.resolver != nil {
		return t.resolver.ResolveInvocation(ctx, execution.ResolveRequest{
			WorkspaceID:         t.workspaceID,
			CapabilityID:        t.capabilityID,
			ReleaseID:           t.releaseID,
			BindingConnectionID: t.bindingConnectionID,
		})
	}
	// Test / legacy path: pre-baked Resolved without a live resolver.
	if strings.TrimSpace(t.resolved.Snapshot.WorkspaceID) == "" &&
		strings.TrimSpace(t.resolved.Snapshot.CapabilityID) == "" &&
		strings.TrimSpace(t.resolved.Snapshot.ReleaseID) == "" {
		// Fill identity from tool config so InvokeResolved pin checks can pass
		// when tests only set CapabilityID on the tool config.
		resolved := t.resolved
		if strings.TrimSpace(resolved.Snapshot.WorkspaceID) == "" {
			resolved.Snapshot.WorkspaceID = t.workspaceID
		}
		if strings.TrimSpace(resolved.Snapshot.CapabilityID) == "" {
			resolved.Snapshot.CapabilityID = t.capabilityID
		}
		if strings.TrimSpace(resolved.Snapshot.ReleaseID) == "" {
			resolved.Snapshot.ReleaseID = t.releaseID
		}
		return resolved, nil
	}
	return t.resolved, nil
}

func (t *pipelineTool) invocationID() (string, error) {
	if t.fixedInvocationID != "" {
		return t.fixedInvocationID, nil
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("pipeline tool: generate invocation id: %w", err)
	}
	return id.String(), nil
}

func (t *pipelineTool) invokeResolved(
	ctx context.Context,
	input json.RawMessage,
	argsJSON string,
	invocationID string,
	resolved execution.ResolvedInvocation,
) (string, error) {
	request := execution.InvokeRequest{
		InvocationID:          invocationID,
		WorkspaceID:           t.workspaceID,
		CapabilityID:          t.capabilityID,
		ReleaseID:             t.releaseID,
		ActorType:             t.actorType,
		ActorID:               t.actorID,
		TraceID:               t.traceID,
		Input:                 input,
		BindingConnectionID:   t.bindingConnectionID,
		AgentRunID:            t.agentRunID,
		PrincipalSnapshot:     t.principalSnapshot,
		AuthorizationSnapshot: append(json.RawMessage(nil), t.authorizationSnapshot...),
	}

	result, invokeErr := t.pipeline.InvokeResolved(ctx, request, resolved)
	if invokeErr != nil {
		code, message := mapInvocationFailure(invokeErr)
		out := formatToolErrorResult(code, message)
		t.emitToolComplete(ctx, argsJSON, out, firstNonEmpty(result.InvocationID, invocationID), false, code)
		return out, nil
	}
	out := formatToolSuccessResult(result.Output, map[string]any{
		"invocationId": result.InvocationID,
		"cached":       result.Cached,
	})
	t.emitToolComplete(ctx, argsJSON, out, firstNonEmpty(result.InvocationID, invocationID), true, "")
	return out, nil
}

func (t *pipelineTool) emitToolComplete(
	ctx context.Context,
	argsJSON, resultJSON, invocationID string,
	ok bool,
	errorCode string,
) {
	if t.onToolComplete == nil {
		return
	}
	toolName := ""
	if t.info != nil {
		toolName = t.info.Name
	}
	// Hook errors must not change the tool result returned to the model.
	// (ToolCompleteHook is void; callers log internally.)
	t.onToolComplete(ctx, ToolCompleteEvent{
		WorkspaceID:  t.workspaceID,
		AgentRunID:   t.agentRunID,
		CapabilityID: t.capabilityID,
		ReleaseID:    t.releaseID,
		InvocationID: invocationID,
		ToolName:     toolName,
		ArgsJSON:     argsJSON,
		ResultJSON:   resultJSON,
		OK:           ok,
		ErrorCode:    errorCode,
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// mapResolutionFailure returns a stable public code + safe message for tool
// resolution failures. Never includes Secret/Token/Broker body material.
func mapResolutionFailure(err error) (code, message string) {
	if err == nil {
		return execution.ErrorCodeResolve, "tool resolution failed"
	}
	var outbound *outboundidentity.Error
	if errors.As(err, &outbound) && outbound != nil && strings.TrimSpace(outbound.Code) != "" {
		msg := strings.TrimSpace(outbound.Message)
		if msg == "" {
			msg = outbound.Code
		}
		return outbound.Code, msg
	}
	if code := execution.ErrorCode(err); code != "" {
		return code, code
	}
	// Last resort: prefer a stable resolve code over raw error text.
	return execution.ErrorCodeResolve, "tool resolution failed"
}

func mapInvocationFailure(err error) (code, message string) {
	if err == nil {
		return "TOOL_INVOKE_FAILED", "tool invocation failed"
	}
	var outbound *outboundidentity.Error
	if errors.As(err, &outbound) && outbound != nil && strings.TrimSpace(outbound.Code) != "" {
		msg := strings.TrimSpace(outbound.Message)
		if msg == "" {
			msg = outbound.Code
		}
		return outbound.Code, msg
	}
	if code := execution.ErrorCode(err); code != "" {
		return code, code
	}
	return "TOOL_INVOKE_FAILED", "tool invocation failed"
}

// normalizeToolArgs parses model tool-call arguments into a JSON object.
// Empty / whitespace becomes {}.
func normalizeToolArgs(args string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, fmt.Errorf("tool arguments are not valid JSON")
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return nil, fmt.Errorf("tool arguments are not valid JSON: %w", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// formatToolErrorResult mirrors chatruntime.toolErrorResult JSON shape:
//
//	{"ok":false,"errorCode":...,"message":...}
func formatToolErrorResult(code, message string) string {
	return toolResultContent(map[string]any{
		"ok": false, "errorCode": code, "message": message,
	})
}

// formatToolSuccessResult mirrors chatruntime.toolSuccessResult JSON shape:
//
//	{"ok":true, ...meta, "output": ...}
func formatToolSuccessResult(output json.RawMessage, meta map[string]any) string {
	body := map[string]any{"ok": true}
	for key, value := range meta {
		body[key] = value
	}
	if len(output) > 0 {
		var decoded any
		if json.Unmarshal(output, &decoded) == nil {
			body["output"] = decoded
		} else {
			body["output"] = json.RawMessage(output)
		}
	}
	return toolResultContent(body)
}

func toolResultContent(payload any) string {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return `{"ok":false,"error":"ENCODE_FAILED"}`
	}
	return string(encoded)
}

// readInterruptState / readResumeContext default to eino tool APIs.
// Tests may override to inject resume/interrupt context without a full graph.
var (
	readInterruptState = func(ctx context.Context) (wasInterrupted bool, hasState bool, state ToolConfirmInterruptState) {
		return tool.GetInterruptState[ToolConfirmInterruptState](ctx)
	}
	readResumeContext = func(ctx context.Context) (isTarget bool, hasData bool, data string) {
		return tool.GetResumeContext[string](ctx)
	}
)
