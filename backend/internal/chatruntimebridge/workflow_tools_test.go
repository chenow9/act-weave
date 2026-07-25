package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/execution"

	"github.com/cloudwego/eino/components/tool"
)

// P3.4: buildPipelineTools includes published WORKFLOW capabilities as InvokableTools.
func TestBuildPipelineToolsIncludesWorkflow(t *testing.T) {
	spy := &workflowResolveInvoker{
		resolved: map[string]execution.ResolvedInvocation{
			"cap-tool": {
				Snapshot: execution.ReleaseSnapshot{
					WorkspaceID: "ws-1", CapabilityID: "cap-tool", ReleaseID: "rel-tool",
					ExecutorType: execution.ExecutorTypeHTTP, ToolVersionID: "ver-1",
				},
				Connection: execution.ConnectionSnapshot{ID: "conn-1", WorkspaceID: "ws-1"},
			},
			"cap-wf": {
				Snapshot: execution.ReleaseSnapshot{
					WorkspaceID: "ws-1", CapabilityID: "cap-wf", ReleaseID: "rel-wf",
					ExecutorType: execution.ExecutorTypeWORKFLOW, ToolVersionID: "rev-1",
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
	if !containsName(spy.resolvedKeys, "cap-tool") || !containsName(spy.resolvedKeys, "cap-wf") {
		t.Fatalf("resolve was not called for both capabilities: %v", spy.resolvedKeys)
	}
	if containsName(spy.resolvedKeys, "cap-other") {
		t.Fatalf("OTHER kind must not be resolved: %v", spy.resolvedKeys)
	}
}

func TestBuildPipelineToolsWorkflowResolveFailure(t *testing.T) {
	spy := &workflowResolveInvoker{
		errFor: map[string]error{"cap-wf": errors.New("not published")},
		resolved: map[string]execution.ResolvedInvocation{
			"cap-tool": {
				Snapshot: execution.ReleaseSnapshot{
					WorkspaceID: "ws-1", CapabilityID: "cap-tool", ReleaseID: "rel-tool",
					ExecutorType: execution.ExecutorTypeHTTP,
				},
				Connection: execution.ConnectionSnapshot{WorkspaceID: "ws-1"},
			},
		},
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
	_, err := bridge.buildPipelineTools(
		context.Background(),
		agentrun.Job{WorkspaceID: "ws-1", RunID: "run-1", ActorID: "user-1"},
		execution.AgentRun{
			ID: "run-1", WorkspaceID: "ws-1", TraceID: "trace-1",
			TriggeredByType: "USER", TriggeredByID: "user-1",
			CapabilitySnapshot: snapshot,
		},
		"pending-key",
	)
	if err == nil {
		t.Fatal("expected resolve failure for unpublished WORKFLOW")
	}
}

type workflowResolveInvoker struct {
	resolved     map[string]execution.ResolvedInvocation
	errFor       map[string]error
	resolvedKeys []string
}

func (s *workflowResolveInvoker) ResolveInvocation(
	_ context.Context, req execution.ResolveRequest,
) (execution.ResolvedInvocation, error) {
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
	context.Context, execution.InvokeRequest, execution.ResolvedInvocation,
) (execution.PipelineResult, error) {
	return execution.PipelineResult{}, errors.New("invoke not used in this test")
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
