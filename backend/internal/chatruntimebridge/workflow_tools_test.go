package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/outboundidentity"

	"github.com/cloudwego/eino/components/tool"
)

// P3.4 + ZKL-56 UX-02: buildPipelineTools includes TOOL+WORKFLOW metadata without resolving.
func TestBuildPipelineToolsIncludesWorkflow(t *testing.T) {
	spy := &workflowResolveInvoker{
		resolved: map[string]execution.ResolvedInvocation{
			"cap-tool": {
				Snapshot: execution.ReleaseSnapshot{
					WorkspaceID: "ws-1", CapabilityID: "cap-tool", ReleaseID: "rel-tool",
					ExecutorType: execution.ExecutorTypeHTTP, ToolVersionID: "ver-1",
					InputSchema: json.RawMessage(`{"type":"object"}`),
				},
				Connection: execution.ConnectionSnapshot{ID: "conn-1", WorkspaceID: "ws-1"},
			},
			"cap-wf": {
				Snapshot: execution.ReleaseSnapshot{
					WorkspaceID: "ws-1", CapabilityID: "cap-wf", ReleaseID: "rel-wf",
					ExecutorType: execution.ExecutorTypeWORKFLOW, ToolVersionID: "rev-1",
					InputSchema: json.RawMessage(`{"type":"object"}`),
				},
				Connection: execution.ConnectionSnapshot{WorkspaceID: "ws-1"},
				Credential: execution.CredentialReference{WorkspaceID: "ws-1", AuthMode: "NONE"},
			},
		},
	}
	bridge := &Bridge{toolInvoker: spy}
	snapshot, _ := json.Marshal(map[string]any{
		"schemaVersion": "capability-snapshot.v1",
		"releases": []map[string]any{
			{
				"capabilityId": "cap-tool", "releaseId": "rel-tool", "kind": "TOOL",
				"callableName": "lookup_orders", "callableDescription": "lookup",
				"inputSchema": map[string]any{"type": "object"},
			},
			{
				"capabilityId": "cap-wf", "releaseId": "rel-wf", "kind": "WORKFLOW",
				"callableName": "process_order", "callableDescription": "process",
				"inputSchema": map[string]any{"type": "object"},
			},
			{
				"capabilityId": "cap-other", "releaseId": "rel-x", "kind": "OTHER",
				"callableName": "ignored", "callableDescription": "ignored",
				"inputSchema": map[string]any{"type": "object"},
			},
		},
	})
	tools, err := bridge.buildPipelineTools(
		context.Background(),
		agentrun.Job{WorkspaceID: "ws-1", RunID: "run-1", ActorID: "user-1"},
		execution.AgentRun{
			ID: "run-1", WorkspaceID: "ws-1", TraceID: "trace-1",
			TriggeredByType: "USER", TriggeredByID: "user-1",
			CapabilitySnapshot: snapshot,
		},
		"pending-key",
	)
	if err != nil {
		t.Fatalf("buildPipelineTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected TOOL+WORKFLOW invokable tools, got %d", len(tools))
	}
	names := toolNames(context.Background(), tools)
	if !containsName(names, "lookup_orders") || !containsName(names, "process_order") {
		t.Fatalf("tool names=%v want lookup_orders and process_order", names)
	}
	// ZKL-56 UX-02: build must not resolve any capability.
	if got := spy.resolveCount.Load(); got != 0 {
		t.Fatalf("buildPipelineTools resolve count = %d, want 0 (lazy)", got)
	}
	if len(spy.resolvedKeys) != 0 {
		t.Fatalf("resolve was called at build time: %v", spy.resolvedKeys)
	}
}

// ZKL-56 UX-02: broken Tool must not prevent build; pure-text path has resolver=0.
func TestBuildPipelineToolsLazy_BrokenToolDoesNotFailBuild(t *testing.T) {
	spy := &workflowResolveInvoker{
		errFor: map[string]error{
			"cap-broken": outboundidentity.ErrIdentityConnectionNotReady,
		},
	}
	bridge := &Bridge{toolInvoker: spy}
	snapshot, _ := json.Marshal(map[string]any{
		"schemaVersion": "capability-snapshot.v1",
		"releases": []map[string]any{
			{
				"capabilityId": "cap-broken", "releaseId": "rel-broken", "kind": "TOOL",
				"callableName": "broken_tool", "callableDescription": "broken",
				"inputSchema": map[string]any{"type": "object"}, "connectionId": "conn-bad",
			},
			{
				"capabilityId": "cap-ok", "releaseId": "rel-ok", "kind": "TOOL",
				"callableName": "ok_tool", "callableDescription": "ok",
				"inputSchema": map[string]any{"type": "object"}, "connectionId": "conn-ok",
			},
		},
	})
	tools, err := bridge.buildPipelineTools(
		context.Background(),
		agentrun.Job{WorkspaceID: "ws-1", RunID: "run-1", ActorID: "user-1"},
		execution.AgentRun{
			ID: "run-1", WorkspaceID: "ws-1", TraceID: "trace-1",
			TriggeredByType: "USER", TriggeredByID: "user-1",
			CapabilitySnapshot: snapshot,
		},
		"pending-key",
	)
	if err != nil {
		t.Fatalf("build must succeed with broken bound tool: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if got := spy.resolveCount.Load(); got != 0 {
		t.Fatalf("resolve count at build = %d, want 0", got)
	}
	if got := spy.invokeCount.Load(); got != 0 {
		t.Fatalf("invoke count at build = %d, want 0", got)
	}
}

// ZKL-56 UX-02: actual call of broken tool resolves once, never invokes, returns stable code.
func TestBuildPipelineToolsLazy_ActualBrokenCallMapsStableError(t *testing.T) {
	spy := &workflowResolveInvoker{
		errFor: map[string]error{
			"cap-broken": outboundidentity.ErrIdentityConnectionNotReady,
		},
	}
	bridge := &Bridge{toolInvoker: spy}
	snapshot, _ := json.Marshal(map[string]any{
		"schemaVersion": "capability-snapshot.v1",
		"releases": []map[string]any{
			{
				"capabilityId": "cap-broken", "releaseId": "rel-broken", "kind": "TOOL",
				"callableName": "broken_tool", "callableDescription": "broken",
				"inputSchema": map[string]any{"type": "object"}, "connectionId": "conn-bad",
			},
		},
	})
	tools, err := bridge.buildPipelineTools(
		context.Background(),
		agentrun.Job{WorkspaceID: "ws-1", RunID: "run-1", ActorID: "user-1"},
		execution.AgentRun{
			ID: "run-1", WorkspaceID: "ws-1", TraceID: "trace-1",
			TriggeredByType: "USER", TriggeredByID: "user-1",
			CapabilitySnapshot: snapshot,
		},
		"pending-key",
	)
	if err != nil {
		t.Fatalf("buildPipelineTools: %v", err)
	}
	inv, ok := tools[0].(tool.InvokableTool)
	if !ok {
		t.Fatal("tool is not InvokableTool")
	}
	out, runErr := inv.InvokableRun(context.Background(), `{}`)
	if runErr != nil {
		t.Fatalf("resolution failure must map to tool result string, got Go err: %v", runErr)
	}
	if got := spy.resolveCount.Load(); got != 1 {
		t.Fatalf("resolve count = %d, want 1", got)
	}
	if got := spy.invokeCount.Load(); got != 0 {
		t.Fatalf("invoke count = %d, want 0 (no external/pipeline success path)", got)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("result not JSON: %v (%s)", err, out)
	}
	if body["ok"] != false {
		t.Fatalf("ok = %v, want false", body["ok"])
	}
	if body["errorCode"] != outboundidentity.CodeIdentityConnectionNotReady {
		t.Fatalf("errorCode = %v, want %s", body["errorCode"], outboundidentity.CodeIdentityConnectionNotReady)
	}
}

// WORKFLOW resolve failure is deferred to actual call (no longer fails build).
func TestBuildPipelineToolsWorkflowResolveFailureOnCall(t *testing.T) {
	spy := &workflowResolveInvoker{
		errFor: map[string]error{"cap-wf": errors.New("not published")},
	}
	bridge := &Bridge{toolInvoker: spy}
	snapshot, _ := json.Marshal(map[string]any{
		"schemaVersion": "capability-snapshot.v1",
		"releases": []map[string]any{
			{
				"capabilityId": "cap-wf", "releaseId": "rel-wf", "kind": "WORKFLOW",
				"callableName": "process_order", "callableDescription": "process",
				"inputSchema": map[string]any{"type": "object"},
			},
		},
	})
	tools, err := bridge.buildPipelineTools(
		context.Background(),
		agentrun.Job{WorkspaceID: "ws-1", RunID: "run-1", ActorID: "user-1"},
		execution.AgentRun{
			ID: "run-1", WorkspaceID: "ws-1", TraceID: "trace-1",
			TriggeredByType: "USER", TriggeredByID: "user-1",
			CapabilitySnapshot: snapshot,
		},
		"pending-key",
	)
	if err != nil {
		t.Fatalf("build must succeed; resolve is lazy: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	inv := tools[0].(tool.InvokableTool)
	out, runErr := inv.InvokableRun(context.Background(), `{}`)
	if runErr != nil {
		t.Fatalf("want tool error result, got Go err: %v", runErr)
	}
	if spy.resolveCount.Load() != 1 {
		t.Fatalf("resolve count = %d, want 1", spy.resolveCount.Load())
	}
	if spy.invokeCount.Load() != 0 {
		t.Fatalf("invoke count = %d, want 0", spy.invokeCount.Load())
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != false {
		t.Fatalf("ok = %v", body["ok"])
	}
	// Generic non-typed error maps to stable resolve code.
	if body["errorCode"] != execution.ErrorCodeResolve {
		t.Fatalf("errorCode = %v, want %s", body["errorCode"], execution.ErrorCodeResolve)
	}
}

// Successful actual call resolves once then invokes once.
func TestBuildPipelineToolsLazy_SuccessfulCallResolvesThenInvokes(t *testing.T) {
	spy := &workflowResolveInvoker{
		resolved: map[string]execution.ResolvedInvocation{
			"cap-tool": {
				Snapshot: execution.ReleaseSnapshot{
					WorkspaceID: "ws-1", CapabilityID: "cap-tool", ReleaseID: "rel-tool",
					ExecutorType: execution.ExecutorTypeHTTP, ToolVersionID: "ver-1",
					InputSchema: json.RawMessage(`{"type":"object"}`),
				},
				Connection: execution.ConnectionSnapshot{ID: "conn-1", WorkspaceID: "ws-1"},
			},
		},
	}
	bridge := &Bridge{toolInvoker: spy}
	snapshot, _ := json.Marshal(map[string]any{
		"schemaVersion": "capability-snapshot.v1",
		"releases": []map[string]any{
			{
				"capabilityId": "cap-tool", "releaseId": "rel-tool", "kind": "TOOL",
				"callableName": "lookup_orders", "callableDescription": "lookup",
				"inputSchema": map[string]any{"type": "object"}, "connectionId": "conn-1",
			},
		},
	})
	tools, err := bridge.buildPipelineTools(
		context.Background(),
		agentrun.Job{WorkspaceID: "ws-1", RunID: "run-1", ActorID: "user-1"},
		execution.AgentRun{
			ID: "run-1", WorkspaceID: "ws-1", TraceID: "trace-1",
			TriggeredByType: "USER", TriggeredByID: "user-1",
			CapabilitySnapshot: snapshot,
		},
		"pending-key",
	)
	if err != nil {
		t.Fatal(err)
	}
	out, runErr := tools[0].(tool.InvokableTool).InvokableRun(context.Background(), `{"q":1}`)
	if runErr != nil {
		t.Fatalf("InvokableRun: %v", runErr)
	}
	if spy.resolveCount.Load() != 1 {
		t.Fatalf("resolve = %d, want 1", spy.resolveCount.Load())
	}
	if spy.invokeCount.Load() != 1 {
		t.Fatalf("invoke = %d, want 1", spy.invokeCount.Load())
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Fatalf("body=%v", body)
	}
}

type workflowResolveInvoker struct {
	resolved     map[string]execution.ResolvedInvocation
	errFor       map[string]error
	resolvedKeys []string
	resolveCount atomic.Int64
	invokeCount  atomic.Int64
}

func (s *workflowResolveInvoker) ResolveInvocation(
	_ context.Context, req execution.ResolveRequest,
) (execution.ResolvedInvocation, error) {
	s.resolveCount.Add(1)
	s.resolvedKeys = append(s.resolvedKeys, req.CapabilityID)
	if s.errFor != nil {
		if err, ok := s.errFor[req.CapabilityID]; ok {
			return execution.ResolvedInvocation{}, err
		}
	}
	value, ok := s.resolved[req.CapabilityID]
	if !ok {
		return execution.ResolvedInvocation{}, errors.New("unknown capability")
	}
	return value, nil
}

func (s *workflowResolveInvoker) InvokeResolved(
	_ context.Context, req execution.InvokeRequest, _ execution.ResolvedInvocation,
) (execution.PipelineResult, error) {
	s.invokeCount.Add(1)
	return execution.PipelineResult{
		InvocationResult: execution.InvocationResult{
			InvocationID: req.InvocationID,
			Output:       json.RawMessage(`{"ok":true}`),
		},
	}, nil
}

func toolNames(ctx context.Context, tools []tool.BaseTool) []string {
	names := make([]string, 0, len(tools))
	for _, item := range tools {
		info, err := item.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		names = append(names, info.Name)
	}
	return names
}

func containsName(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// Ensure SnapshotCapability WORKFLOW parses for bridge tests.
var _ = chatruntime.SnapshotCapability{}
