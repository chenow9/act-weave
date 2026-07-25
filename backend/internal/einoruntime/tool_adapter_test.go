package einoruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// spyInvoker counts InvokeResolved calls for HITL ownership assertions.
type spyInvoker struct {
	calls atomic.Int64
	fn    func(context.Context, execution.InvokeRequest, execution.ResolvedInvocation) (execution.PipelineResult, error)
}

func (s *spyInvoker) InvokeResolved(
	ctx context.Context,
	request execution.InvokeRequest,
	resolved execution.ResolvedInvocation,
) (execution.PipelineResult, error) {
	s.calls.Add(1)
	if s.fn != nil {
		return s.fn(ctx, request, resolved)
	}
	return execution.PipelineResult{
		InvocationResult: execution.InvocationResult{
			InvocationID: request.InvocationID,
			Output:       json.RawMessage(`{"answer":42}`),
			HTTPStatus:   200,
		},
	}, nil
}

// spyResolver counts ResolveInvocation calls for lazy-resolution assertions.
type spyResolver struct {
	calls    atomic.Int64
	resolved execution.ResolvedInvocation
	err      error
	fn       func(context.Context, execution.ResolveRequest) (execution.ResolvedInvocation, error)
}

func (s *spyResolver) ResolveInvocation(
	ctx context.Context,
	req execution.ResolveRequest,
) (execution.ResolvedInvocation, error) {
	s.calls.Add(1)
	if s.fn != nil {
		return s.fn(ctx, req)
	}
	if s.err != nil {
		return execution.ResolvedInvocation{}, s.err
	}
	if strings.TrimSpace(s.resolved.Snapshot.CapabilityID) != "" {
		return s.resolved, nil
	}
	return execution.ResolvedInvocation{
		Snapshot: execution.ReleaseSnapshot{
			WorkspaceID:  req.WorkspaceID,
			CapabilityID: req.CapabilityID,
			ReleaseID:    req.ReleaseID,
			InputSchema:  json.RawMessage(`{"type":"object"}`),
		},
		Connection: execution.ConnectionSnapshot{
			ID: req.BindingConnectionID, WorkspaceID: req.WorkspaceID,
		},
	}, nil
}

func baseToolConfig(pipeline ResolvedInvoker, requiresConfirmation bool) PipelineToolConfig {
	return PipelineToolConfig{
		Info: &schema.ToolInfo{
			Name: "demo_tool",
			Desc: "demo",
		},
		Pipeline:             pipeline,
		RequiresConfirmation: requiresConfirmation,
		WorkspaceID:          "ws-1",
		CapabilityID:         "cap-1",
		ReleaseID:            "rel-1",
		ActorType:            "USER",
		ActorID:              "user-1",
		TraceID:              "trace-1",
		AgentRunID:           "run-1",
		InvocationID:         "inv-fixed",
		StepID:               "step-1",
		Resolved: execution.ResolvedInvocation{
			Snapshot: execution.ReleaseSnapshot{
				WorkspaceID:  "ws-1",
				CapabilityID: "cap-1",
				ReleaseID:    "rel-1",
				InputSchema:  json.RawMessage(`{"type":"object"}`),
			},
			RequiresConfirmation: requiresConfirmation,
		},
	}
}

func TestNewPipelineToolValidation(t *testing.T) {
	t.Parallel()
	spy := &spyInvoker{}
	if _, err := NewPipelineTool(PipelineToolConfig{Pipeline: spy}); err == nil {
		t.Fatal("expected error when Info is nil")
	}
	if _, err := NewPipelineTool(PipelineToolConfig{
		Info: &schema.ToolInfo{Name: "x"},
	}); err == nil {
		t.Fatal("expected error when Pipeline is nil")
	}
}

func TestPipelineToolInfo(t *testing.T) {
	t.Parallel()
	spy := &spyInvoker{}
	tl, err := NewPipelineTool(baseToolConfig(spy, false))
	if err != nil {
		t.Fatal(err)
	}
	info, err := tl.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "demo_tool" {
		t.Fatalf("Info.Name = %q, want demo_tool", info.Name)
	}
}

// TestPipelineToolFirstRunNeedsConfirmation_NoInvoke proves design §3.6.3:
// first run needing confirmation → StatefulInterrupt; Invoke count = 0.
func TestPipelineToolFirstRunNeedsConfirmation_NoInvoke(t *testing.T) {
	t.Parallel()
	spy := &spyInvoker{}
	tl, err := NewPipelineTool(baseToolConfig(spy, true))
	if err != nil {
		t.Fatal(err)
	}

	_, runErr := tl.InvokableRun(context.Background(), `{"x":1}`)
	if runErr == nil {
		t.Fatal("expected StatefulInterrupt error")
	}
	info, ok := compose.IsInterruptRerunError(runErr)
	if !ok {
		t.Fatalf("expected interrupt error, got %v", runErr)
	}
	if info != toolConfirmInterruptInfo {
		t.Fatalf("interrupt info = %v, want %q", info, toolConfirmInterruptInfo)
	}
	if got := spy.calls.Load(); got != 0 {
		t.Fatalf("InvokeResolved calls = %d, want 0 (confirm-before-invoke)", got)
	}
}

// TestPipelineToolResumeWithData_NoInvoke proves: after Dispatch already
// invoked, Eino resume returns GetResumeContext data and never re-invokes.
func TestPipelineToolResumeWithData_NoInvoke(t *testing.T) {
	// Not parallel: mutates package-level readers.
	origState := readInterruptState
	origResume := readResumeContext
	t.Cleanup(func() {
		readInterruptState = origState
		readResumeContext = origResume
	})

	saved := ToolConfirmInterruptState{
		SchemaVersion: ToolConfirmInterruptSchemaVersion,
		InvocationID:  "inv-fixed",
		ReleaseID:     "rel-1",
		CapabilityID:  "cap-1",
		StepID:        "step-1",
	}
	resumePayload := formatToolSuccessResult(json.RawMessage(`{"done":true}`), map[string]any{
		"invocationId": "inv-fixed",
		"confirmed":    true,
	})

	readInterruptState = func(context.Context) (bool, bool, ToolConfirmInterruptState) {
		return true, true, saved
	}
	readResumeContext = func(context.Context) (bool, bool, string) {
		return true, true, resumePayload
	}

	spy := &spyInvoker{}
	tl, err := NewPipelineTool(baseToolConfig(spy, true))
	if err != nil {
		t.Fatal(err)
	}

	out, runErr := tl.InvokableRun(context.Background(), `{"x":1}`)
	if runErr != nil {
		t.Fatalf("resume should return data, got err: %v", runErr)
	}
	if out != resumePayload {
		t.Fatalf("resume output = %q, want injected payload", out)
	}
	if got := spy.calls.Load(); got != 0 {
		t.Fatalf("InvokeResolved calls = %d, want 0 on resume path", got)
	}
}

// TestPipelineToolResumeNotTarget_ReinterruptsWithoutInvoke covers the
// sibling-resume case: wasInterrupted && !isTarget → re-interrupt, no Invoke.
func TestPipelineToolResumeNotTarget_ReinterruptsWithoutInvoke(t *testing.T) {
	origState := readInterruptState
	origResume := readResumeContext
	t.Cleanup(func() {
		readInterruptState = origState
		readResumeContext = origResume
	})

	saved := ToolConfirmInterruptState{
		SchemaVersion: ToolConfirmInterruptSchemaVersion,
		InvocationID:  "inv-fixed",
		ReleaseID:     "rel-1",
	}
	readInterruptState = func(context.Context) (bool, bool, ToolConfirmInterruptState) {
		return true, true, saved
	}
	readResumeContext = func(context.Context) (bool, bool, string) {
		return false, false, ""
	}

	spy := &spyInvoker{}
	tl, err := NewPipelineTool(baseToolConfig(spy, true))
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := tl.InvokableRun(context.Background(), `{}`)
	if runErr == nil {
		t.Fatal("expected re-interrupt error")
	}
	if _, ok := compose.IsInterruptRerunError(runErr); !ok {
		t.Fatalf("expected interrupt error, got %v", runErr)
	}
	if got := spy.calls.Load(); got != 0 {
		t.Fatalf("InvokeResolved calls = %d, want 0", got)
	}
}

// TestPipelineToolResumeMissingData errors when targeted without payload.
func TestPipelineToolResumeMissingData(t *testing.T) {
	origState := readInterruptState
	origResume := readResumeContext
	t.Cleanup(func() {
		readInterruptState = origState
		readResumeContext = origResume
	})

	readInterruptState = func(context.Context) (bool, bool, ToolConfirmInterruptState) {
		return true, true, ToolConfirmInterruptState{InvocationID: "inv-fixed", ReleaseID: "rel-1"}
	}
	readResumeContext = func(context.Context) (bool, bool, string) {
		return true, false, ""
	}

	spy := &spyInvoker{}
	tl, err := NewPipelineTool(baseToolConfig(spy, true))
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := tl.InvokableRun(context.Background(), `{}`)
	if runErr == nil || !strings.Contains(runErr.Error(), "missing result data") {
		t.Fatalf("expected missing result data error, got %v", runErr)
	}
	if got := spy.calls.Load(); got != 0 {
		t.Fatalf("InvokeResolved calls = %d, want 0", got)
	}
}

// TestPipelineToolFirstRunNoConfirmation_InvokeOnce maps pipeline result to
// toolSuccessResult shape.
func TestPipelineToolFirstRunNoConfirmation_InvokeOnce(t *testing.T) {
	t.Parallel()
	spy := &spyInvoker{}
	tl, err := NewPipelineTool(baseToolConfig(spy, false))
	if err != nil {
		t.Fatal(err)
	}

	out, runErr := tl.InvokableRun(context.Background(), `{"x":1}`)
	if runErr != nil {
		t.Fatalf("unexpected error: %v", runErr)
	}
	if got := spy.calls.Load(); got != 1 {
		t.Fatalf("InvokeResolved calls = %d, want 1", got)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, out)
	}
	if body["ok"] != true {
		t.Fatalf("ok = %v, want true; body=%v", body["ok"], body)
	}
	if body["invocationId"] != "inv-fixed" {
		t.Fatalf("invocationId = %v, want inv-fixed", body["invocationId"])
	}
	// output should be decoded object, not raw string
	output, ok := body["output"].(map[string]any)
	if !ok {
		t.Fatalf("output type %T, want object; body=%v", body["output"], body)
	}
	// JSON numbers decode as float64
	if output["answer"].(float64) != 42 {
		t.Fatalf("output.answer = %v, want 42", output["answer"])
	}
}

func TestPipelineToolFirstRunInvokeError_MapsToolErrorResult(t *testing.T) {
	t.Parallel()
	spy := &spyInvoker{
		fn: func(context.Context, execution.InvokeRequest, execution.ResolvedInvocation) (execution.PipelineResult, error) {
			return execution.PipelineResult{}, execution.NewError("UPSTREAM_FAIL", "UPSTREAM", true, 502, errors.New("boom"))
		},
	}
	tl, err := NewPipelineTool(baseToolConfig(spy, false))
	if err != nil {
		t.Fatal(err)
	}
	out, runErr := tl.InvokableRun(context.Background(), `{}`)
	if runErr != nil {
		t.Fatalf("business errors should be mapped to result string, got Go err: %v", runErr)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != false {
		t.Fatalf("ok = %v, want false", body["ok"])
	}
	if body["errorCode"] != "UPSTREAM_FAIL" {
		t.Fatalf("errorCode = %v, want UPSTREAM_FAIL", body["errorCode"])
	}
	if body["message"] == nil || body["message"] == "" {
		t.Fatal("message should be set")
	}
	if got := spy.calls.Load(); got != 1 {
		t.Fatalf("InvokeResolved calls = %d, want 1", got)
	}
}

func TestFormatToolResultShapesMatchChatruntime(t *testing.T) {
	t.Parallel()
	// Align with chatruntime.toolSuccessResult / toolErrorResult shapes.
	success := formatToolSuccessResult(json.RawMessage(`{"k":"v"}`), map[string]any{
		"invocationId": "i1", "cached": false, "confirmed": true,
	})
	var s map[string]any
	if err := json.Unmarshal([]byte(success), &s); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ok", "invocationId", "cached", "confirmed", "output"} {
		if _, ok := s[key]; !ok {
			t.Fatalf("success missing key %q: %s", key, success)
		}
	}
	if s["ok"] != true {
		t.Fatalf("ok = %v", s["ok"])
	}

	errStr := formatToolErrorResult("TOOL_BUDGET_EXCEEDED", "tool call budget exhausted")
	var e map[string]any
	if err := json.Unmarshal([]byte(errStr), &e); err != nil {
		t.Fatal(err)
	}
	if e["ok"] != false || e["errorCode"] != "TOOL_BUDGET_EXCEEDED" || e["message"] != "tool call budget exhausted" {
		t.Fatalf("error shape mismatch: %s", errStr)
	}
}

func TestPipelineToolInvalidArgs_NoInvoke(t *testing.T) {
	t.Parallel()
	spy := &spyInvoker{}
	tl, err := NewPipelineTool(baseToolConfig(spy, false))
	if err != nil {
		t.Fatal(err)
	}
	out, runErr := tl.InvokableRun(context.Background(), `not-json`)
	if runErr != nil {
		t.Fatalf("invalid args should map to error result string, got: %v", runErr)
	}
	if got := spy.calls.Load(); got != 0 {
		t.Fatalf("InvokeResolved calls = %d, want 0 for invalid args", got)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != false || body["errorCode"] != "TOOL_ARGS_INVALID" {
		t.Fatalf("unexpected body: %s", out)
	}
}

// TestPipelineToolImplementsInvokableTool is a compile-time + runtime check
// that the constructed tool is usable as tool.BaseTool / InvokableTool.
func TestPipelineToolImplementsInvokableTool(t *testing.T) {
	t.Parallel()
	spy := &spyInvoker{}
	tl, err := NewPipelineTool(baseToolConfig(spy, false))
	if err != nil {
		t.Fatal(err)
	}
	var _ tool.BaseTool = tl
	var _ tool.InvokableTool = tl
}

// TestPipelineToolForwardsPrincipalSnapshot covers AAP SERVICE_PRINCIPAL:
// first-run InvokeResolved must carry PrincipalSnapshot or the invocation
// pipeline rejects with EXECUTION_INVALID_REQUEST before durable rows.
func TestPipelineToolForwardsPrincipalSnapshot(t *testing.T) {
	t.Parallel()
	const (
		workspaceID = "019f8f43-5b4d-7ac5-acb2-c74434338e97"
		actorID     = "019f8f43-aaaa-7ac5-acb2-c74434338e01"
		clientID    = "019f8f43-bbbb-7ac5-acb2-c74434338e02"
		grantID     = "019f8f43-cccc-7ac5-acb2-c74434338e03"
	)
	actor := principal.Ref{WorkspaceID: workspaceID, Type: principal.TypeServicePrincipal, ID: actorID}
	identity, err := principal.NewInvocationIdentity(actor, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := principal.NewExecutionSnapshot(identity, clientID, grantID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	authz := json.RawMessage(`{"source":"test","parent":"agent_run"}`)

	var gotRequest execution.InvokeRequest
	spy := &spyInvoker{
		fn: func(_ context.Context, request execution.InvokeRequest, _ execution.ResolvedInvocation) (execution.PipelineResult, error) {
			gotRequest = request
			return execution.PipelineResult{
				InvocationResult: execution.InvocationResult{
					InvocationID: request.InvocationID,
					Output:       json.RawMessage(`{"ok":true}`),
				},
			}, nil
		},
	}
	cfg := baseToolConfig(spy, false)
	cfg.WorkspaceID = workspaceID
	cfg.ActorType = "SERVICE_PRINCIPAL"
	cfg.ActorID = actorID
	cfg.PrincipalSnapshot = &snap
	cfg.AuthorizationSnapshot = authz
	cfg.Resolved.Snapshot.WorkspaceID = workspaceID
	tl, err := NewPipelineTool(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tl.InvokableRun(context.Background(), `{"x":1}`); err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if gotRequest.PrincipalSnapshot == nil {
		t.Fatal("PrincipalSnapshot not forwarded on InvokeRequest")
	}
	if !gotRequest.PrincipalSnapshot.SameBinding(snap) {
		t.Fatalf("PrincipalSnapshot binding mismatch: %+v", gotRequest.PrincipalSnapshot)
	}
	if gotRequest.ActorType != "SERVICE_PRINCIPAL" || gotRequest.ActorID != actorID {
		t.Fatalf("actor = %s/%s", gotRequest.ActorType, gotRequest.ActorID)
	}
	if string(gotRequest.AuthorizationSnapshot) != string(authz) {
		t.Fatalf("AuthorizationSnapshot = %s, want %s", gotRequest.AuthorizationSnapshot, authz)
	}
}

// ZKL-56 UX-02: Resolver is called once on first actual call, then Invoke once.
func TestPipelineToolLazyResolve_ThenInvokeOnce(t *testing.T) {
	t.Parallel()
	invoker := &spyInvoker{}
	resolver := &spyResolver{}
	cfg := baseToolConfig(invoker, false)
	cfg.Resolved = execution.ResolvedInvocation{} // force lazy path
	cfg.Resolver = resolver
	tl, err := NewPipelineTool(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, runErr := tl.InvokableRun(context.Background(), `{"x":1}`)
	if runErr != nil {
		t.Fatalf("InvokableRun: %v", runErr)
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("ResolveInvocation calls = %d, want 1", got)
	}
	if got := invoker.calls.Load(); got != 1 {
		t.Fatalf("InvokeResolved calls = %d, want 1", got)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Fatalf("body=%v", body)
	}
}

// ZKL-56 UX-02: resolution failure maps to tool error; zero InvokeResolved.
func TestPipelineToolLazyResolve_FailureNoInvoke(t *testing.T) {
	t.Parallel()
	invoker := &spyInvoker{}
	resolver := &spyResolver{
		err: execution.NewError(execution.ErrorCodeResolve, "RESOLUTION", false, 0, errors.New("conn not ready")),
	}
	cfg := baseToolConfig(invoker, false)
	cfg.Resolved = execution.ResolvedInvocation{}
	cfg.Resolver = resolver
	tl, err := NewPipelineTool(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, runErr := tl.InvokableRun(context.Background(), `{}`)
	if runErr != nil {
		t.Fatalf("want tool result string, got Go err: %v", runErr)
	}
	if resolver.calls.Load() != 1 {
		t.Fatalf("resolve = %d, want 1", resolver.calls.Load())
	}
	if invoker.calls.Load() != 0 {
		t.Fatalf("invoke = %d, want 0", invoker.calls.Load())
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != false || body["errorCode"] != execution.ErrorCodeResolve {
		t.Fatalf("body=%v", body)
	}
}

// ZKL-56 UX-02: resume with platform data must not resolve or invoke.
func TestPipelineToolResumeWithData_NoResolve(t *testing.T) {
	origState := readInterruptState
	origResume := readResumeContext
	t.Cleanup(func() {
		readInterruptState = origState
		readResumeContext = origResume
	})

	saved := ToolConfirmInterruptState{
		SchemaVersion: ToolConfirmInterruptSchemaVersion,
		InvocationID:  "inv-fixed",
		ReleaseID:     "rel-1",
		CapabilityID:  "cap-1",
		StepID:        "step-1",
	}
	resumePayload := formatToolSuccessResult(json.RawMessage(`{"done":true}`), map[string]any{
		"invocationId": "inv-fixed",
		"confirmed":    true,
	})
	readInterruptState = func(context.Context) (bool, bool, ToolConfirmInterruptState) {
		return true, true, saved
	}
	readResumeContext = func(context.Context) (bool, bool, string) {
		return true, true, resumePayload
	}

	invoker := &spyInvoker{}
	resolver := &spyResolver{}
	cfg := baseToolConfig(invoker, true)
	cfg.Resolved = execution.ResolvedInvocation{}
	cfg.Resolver = resolver
	tl, err := NewPipelineTool(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, runErr := tl.InvokableRun(context.Background(), `{"x":1}`)
	if runErr != nil {
		t.Fatalf("resume: %v", runErr)
	}
	if out != resumePayload {
		t.Fatalf("out mismatch")
	}
	if resolver.calls.Load() != 0 {
		t.Fatalf("resolve on resume = %d, want 0", resolver.calls.Load())
	}
	if invoker.calls.Load() != 0 {
		t.Fatalf("invoke on resume = %d, want 0", invoker.calls.Load())
	}
}

// ZKL-56 UX-02: confirm path resolves once, never invokes, attaches Resolved to pending.
func TestPipelineToolLazyResolve_ConfirmAttachesResolved(t *testing.T) {
	t.Parallel()
	invoker := &spyInvoker{}
	resolver := &spyResolver{
		resolved: execution.ResolvedInvocation{
			Snapshot: execution.ReleaseSnapshot{
				WorkspaceID: "ws-1", CapabilityID: "cap-1", ReleaseID: "rel-1",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
			Connection:           execution.ConnectionSnapshot{ID: "conn-1", WorkspaceID: "ws-1"},
			RequiresConfirmation: true,
			RiskLevel:            "HIGH",
		},
	}
	var pending PendingConfirmInterrupt
	cfg := baseToolConfig(invoker, false) // snapshot flag false; resolved requires confirm
	cfg.Resolved = execution.ResolvedInvocation{}
	cfg.Resolver = resolver
	cfg.OnConfirmInterrupt = func(_ context.Context, p PendingConfirmInterrupt) {
		pending = p
	}
	tl, err := NewPipelineTool(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := tl.InvokableRun(context.Background(), `{"x":1}`)
	if runErr == nil {
		t.Fatal("expected interrupt")
	}
	if _, ok := compose.IsInterruptRerunError(runErr); !ok {
		t.Fatalf("expected interrupt, got %v", runErr)
	}
	if resolver.calls.Load() != 1 {
		t.Fatalf("resolve = %d, want 1", resolver.calls.Load())
	}
	if invoker.calls.Load() != 0 {
		t.Fatalf("invoke = %d, want 0", invoker.calls.Load())
	}
	if pending.Resolved.Connection.ID != "conn-1" {
		t.Fatalf("pending.Resolved not attached: %+v", pending.Resolved)
	}
	if pending.InvocationID != "inv-fixed" {
		t.Fatalf("invocationId = %q", pending.InvocationID)
	}
}

// Schema validation before confirmation: illegal args → TOOL_ARGS_INVALID, no interrupt, no invoke.
func TestPipelineToolSchemaInvalid_BeforeConfirm(t *testing.T) {
	t.Parallel()
	invoker := &spyInvoker{}
	resolver := &spyResolver{
		resolved: execution.ResolvedInvocation{
			Snapshot: execution.ReleaseSnapshot{
				WorkspaceID: "ws-1", CapabilityID: "cap-1", ReleaseID: "rel-1",
				InputSchema: json.RawMessage(`{"type":"object","required":["orderId"],"properties":{"orderId":{"type":"string"}},"additionalProperties":false}`),
			},
			RequiresConfirmation: true,
		},
	}
	cfg := baseToolConfig(invoker, true)
	cfg.Resolved = execution.ResolvedInvocation{}
	cfg.Resolver = resolver
	cfg.OnConfirmInterrupt = func(context.Context, PendingConfirmInterrupt) {
		t.Fatal("must not confirm with invalid args")
	}
	tl, err := NewPipelineTool(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, runErr := tl.InvokableRun(context.Background(), `{"wrong":1}`)
	if runErr != nil {
		t.Fatalf("want tool result, got %v", runErr)
	}
	if invoker.calls.Load() != 0 {
		t.Fatalf("invoke = %d", invoker.calls.Load())
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatal(err)
	}
	if body["errorCode"] != "TOOL_ARGS_INVALID" {
		t.Fatalf("body=%v", body)
	}
}
