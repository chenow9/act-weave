package workflowruntime

import (
	"context"
	"encoding/json"
	"testing"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/principal"
)

type recordingToolInvoker struct {
	last ToolInvocationContext
}

func (r *recordingToolInvoker) Invoke(_ string, _ map[string]any, ctx ToolInvocationContext) (map[string]any, error) {
	r.last = ctx
	return map[string]any{"ok": true}, nil
}

func TestToolInvokerAdapterForwardsPrincipalSnapshot(t *testing.T) {
	snap := principal.ExecutionSnapshot{}
	rec := &recordingToolInvoker{}
	adapter := toolInvokerAdapter{inner: rec}
	authz := json.RawMessage(`{"source":"test"}`)
	_, err := adapter.Invoke(context.Background(), einoruntime.WorkflowToolCall{
		ToolID:                "create_ops_task",
		ActorType:             "SERVICE_PRINCIPAL",
		PrincipalSnapshot:     &snap,
		AuthorizationSnapshot: authz,
		AgentRunID:            "run-1",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if rec.last.PrincipalSnapshot == nil {
		t.Fatal("PrincipalSnapshot dropped on eino_core tool adapter")
	}
	if rec.last.ActorType != "SERVICE_PRINCIPAL" || rec.last.AgentRunID != "run-1" {
		t.Fatalf("identity not forwarded: %+v", rec.last)
	}
	if string(rec.last.AuthorizationSnapshot) != string(authz) {
		t.Fatalf("AuthorizationSnapshot dropped: %s", rec.last.AuthorizationSnapshot)
	}
}

func TestToWorkflowRunRequestForwardsPrincipalSnapshot(t *testing.T) {
	snap := principal.ExecutionSnapshot{}
	req := toWorkflowRunRequest(domain.CompiledExecutionPlan{}, ExecutionContext{
		ActorType:             "SERVICE_PRINCIPAL",
		PrincipalSnapshot:     &snap,
		AgentRunID:            "run-1",
		AuthorizationSnapshot: json.RawMessage(`{"source":"agent_run"}`),
	})
	if req.PrincipalSnapshot == nil {
		t.Fatal("PrincipalSnapshot dropped on eino_core run request")
	}
	if req.ActorType != "SERVICE_PRINCIPAL" || req.AgentRunID != "run-1" {
		t.Fatalf("identity not forwarded: %+v", req)
	}
}
