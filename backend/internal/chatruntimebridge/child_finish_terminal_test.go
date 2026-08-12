package chatruntimebridge_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// Real TASK AgentTool cancel/timeout via official adk.NewChatModelAgent +
// adk.NewAgentTool (not a hand-rolled Inner). Asserts child agent_run,
// agent_run_delegations, and AGENT_DELEGATION step share one terminal status
// with no RUNNING orphans.
func TestAgentTool_TASK_CancelAndTimeout_ThreeObjectTerminal(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedChildFixture(t, db)
	runRepo, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	delRepo, err := agentdelegation.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := agentdelegation.NewService(delRepo)
	if err != nil {
		t.Fatal(err)
	}

	modelFrozen := producerNodeModel(fx.model, 1, "m", "https://example.test")
	agentASnap := producerNodeAgent(fx.agentA, fx.model, 1, "A", "")
	agentFrozen := producerNodeAgent(fx.agentB, fx.model, 1, "B", "")
	capFrozen := producerNodeCap()
	graph, _ := json.Marshal(map[string]any{
		"schemaVersion": "agent_graph_snapshot.v1",
		"rootAgentId":   fx.agentA,
		"maxDepth":      4, "maxTotalDelegations": 20, "maxPerBinding": 5,
		"builtAt": "2026-08-10T12:00:00Z",
		"nodes": []map[string]any{
			{
				"agentId": fx.agentA, "depth": 0, "modelConfigId": fx.model, "modelConfigLockVersion": 1,
				"modelSnapshot": modelFrozen, "agentSnapshot": agentASnap, "capabilitySnapshot": capFrozen,
			},
			{
				"agentId": fx.agentB, "depth": 1, "modelConfigId": fx.model, "modelConfigLockVersion": 1,
				"modelSnapshot": modelFrozen, "agentSnapshot": agentFrozen, "capabilitySnapshot": capFrozen,
			},
		},
		"edges": []map[string]any{{
			"bindingId": uuid.Must(uuid.NewV7()).String(), "callerAgentId": fx.agentA, "targetAgentId": fx.agentB,
			"callableName": "call_b", "mode": "TASK", "contextPolicy": "TASK_ONLY", "version": 1, "protocol": "INTERNAL",
		}},
		"remotesFrozen":         true,
		"frozenRemotesByCaller": map[string]any{fx.agentA: []any{}, fx.agentB: []any{}},
	})
	if _, err := agentdelegation.ParseSnapshot(fx.ws, graph); err != nil {
		t.Fatalf("fixture graph: %v", err)
	}

	parentID := uuid.Must(uuid.NewV7()).String()
	if _, err := runRepo.StartAgentRun(ctx, execution.StartAgentRunInput{
		ID: parentID, WorkspaceID: fx.ws, AgentID: fx.agentA,
		TriggerType: "MANUAL", TriggeredByType: "USER", TriggeredByID: fx.owner,
		TraceID: parentID,
		Snapshots: execution.AgentRunSnapshots{
			SchemaVersion: execution.RunSnapshotSchemaV2,
			Model:         modelFrozen, Capabilities: capFrozen, ContextPolicy: json.RawMessage(`{}`), Agent: agentFrozen,
		},
		AuthorizationSnapshot: json.RawMessage(`{}`),
		InputSummary:          json.RawMessage(`{}`),
		AgentGraphSnapshot:    graph,
	}); err != nil {
		t.Fatal(err)
	}

	children := &chatruntimebridge.ChildRunStoreAdapter{
		Runs: runRepo,
		GetParent: func(ctx context.Context, workspaceID, parentRunID string) (execution.AgentRun, error) {
			return runRepo.GetAgentRun(ctx, workspaceID, parentRunID)
		},
	}
	targetSnaps := agentdelegation.ChildRunStartInput{
		GraphSnapshot: graph, ModelSnapshot: modelFrozen,
		AgentSnapshot: agentFrozen, CapabilitySnapshot: capFrozen,
	}

	// Real Eino AgentTool whose ChatModelAgent blocks on ctx (cancel/timeout).
	mkRealAgentTool := func(t *testing.T, callable string) tool.InvokableTool {
		t.Helper()
		childAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
			Name: "agent-b-" + callable, Description: "TASK child blocks until ctx done",
			Instruction: "Wait; never finish until cancelled or timed out.",
			Model:       &ctxBlockingChatModel{}, MaxIterations: 2,
			ToolsConfig: adk.ToolsConfig{
				ToolsNodeConfig: compose.ToolsNodeConfig{
					Tools: nil, ExecuteSequentially: true,
				},
				EmitInternalEvents: false,
			},
		})
		if err != nil {
			t.Fatalf("NewChatModelAgent: %v", err)
		}
		raw := adk.NewAgentTool(ctx, childAgent)
		inv, ok := raw.(tool.InvokableTool)
		if !ok {
			t.Fatal("adk.NewAgentTool must be InvokableTool")
		}
		return inv
	}

	t.Run("cancel", func(t *testing.T) {
		edge := agentdelegation.GraphEdgeSnapshot{
			BindingID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: fx.agentA, TargetAgentID: fx.agentB,
			CallableName: "call_b_cancel", Mode: agentdelegation.ModeTask, Version: 1,
			ContextPolicy: agentdelegation.ContextTaskOnly, Protocol: agentdelegation.ProtocolInternal,
		}
		tool, err := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
			Inner: mkRealAgentTool(t, "cancel"), Name: "call_b_cancel", Edge: edge, Audit: audit,
			DefaultCallerAgentID: fx.agentA, ChildRuns: children,
			DefaultTaskTimeout: 8 * time.Second,
			TargetSnapshots:    targetSnaps,
		})
		if err != nil {
			t.Fatal(err)
		}
		runCtx, cancel := context.WithCancel(ctx)
		runCtx = agentdelegation.WithRunContext(runCtx, &agentdelegation.RunContext{
			WorkspaceID: fx.ws, ParentRunID: parentID, RootRunID: parentID, RunID: parentID,
			CallerAgentID: fx.agentA, Depth: 0, Budget: agentdelegation.NewBudget(), TraceID: parentID,
		})
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = tool.InvokableRun(runCtx, `{"request":"cancel-me"}`)
		}()
		// Wait until child agent_run is RUNNING (TASK StartChild + AgentTool entered).
		deadline := time.Now().Add(5 * time.Second)
		var childID, delID string
		for time.Now().Before(deadline) {
			_ = db.QueryRow(`
				SELECT COALESCE(child_run_id::text,''), id::text FROM agent_run_delegations
				WHERE workspace_id=$1 AND parent_run_id=$2 AND status='RUNNING'
				ORDER BY created_at DESC LIMIT 1
			`, fx.ws, parentID).Scan(&childID, &delID)
			if childID != "" && delID != "" {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
		if childID == "" {
			t.Fatal("TASK child never started via real AgentTool path")
		}
		cancel()
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			t.Fatal("cancel path hung after parent ctx cancel")
		}
		assertThreeObjectTerminal(t, db, fx.ws, childID, delID, "CANCELLED")
	})

	t.Run("timeout", func(t *testing.T) {
		// Distinct binding so idempotency key does not replay the cancel subtest row.
		edge := agentdelegation.GraphEdgeSnapshot{
			BindingID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: fx.agentA, TargetAgentID: fx.agentB,
			CallableName: "call_b_timeout", Mode: agentdelegation.ModeTask, Version: 1,
			ContextPolicy: agentdelegation.ContextTaskOnly, Protocol: agentdelegation.ProtocolInternal,
		}
		tool, err := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
			Inner: mkRealAgentTool(t, "timeout"), Name: "call_b_timeout", Edge: edge, Audit: audit,
			DefaultCallerAgentID: fx.agentA, ChildRuns: children,
			DefaultTaskTimeout: 80 * time.Millisecond,
			TargetSnapshots:    targetSnaps,
		})
		if err != nil {
			t.Fatal(err)
		}
		runCtx := agentdelegation.WithRunContext(ctx, &agentdelegation.RunContext{
			WorkspaceID: fx.ws, ParentRunID: parentID, RootRunID: parentID, RunID: parentID,
			CallerAgentID: fx.agentA, Depth: 0, Budget: agentdelegation.NewBudget(),
			TraceID: parentID + "-to",
		})
		_, _ = tool.InvokableRun(runCtx, `{"request":"timeout-me"}`)

		var childID, delID, delStatus string
		_ = db.QueryRow(`
			SELECT COALESCE(child_run_id::text,''), id::text, status FROM agent_run_delegations
			WHERE workspace_id=$1 AND parent_run_id=$2
			ORDER BY created_at DESC LIMIT 1
		`, fx.ws, parentID).Scan(&childID, &delID, &delStatus)
		if childID == "" || delID == "" {
			t.Fatal("timeout path missing child/delegation")
		}
		if delStatus != "TIMED_OUT" {
			t.Fatalf("latest del status=%s want TIMED_OUT", delStatus)
		}
		assertThreeObjectTerminal(t, db, fx.ws, childID, delID, "TIMED_OUT")
	})
}

func assertThreeObjectTerminal(t *testing.T, db *sql.DB, workspaceID, childRunID, delID, wantStatus string) {
	t.Helper()
	var childSt, delSt, stepSt string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, childRunID).Scan(&childSt); err != nil {
		t.Fatalf("child_run: %v", err)
	}
	if err := db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=$1`, delID).Scan(&delSt); err != nil {
		t.Fatalf("delegation: %v", err)
	}
	if err := db.QueryRow(`
		SELECT status FROM agent_run_steps
		WHERE workspace_id=$1 AND delegation_id=$2 AND step_type='AGENT_DELEGATION'
		LIMIT 1
	`, workspaceID, delID).Scan(&stepSt); err != nil {
		t.Fatalf("AGENT_DELEGATION step: %v", err)
	}
	if childSt != wantStatus {
		t.Fatalf("child_run status=%s want %s", childSt, wantStatus)
	}
	if delSt != wantStatus {
		t.Fatalf("agent_run_delegations status=%s want %s", delSt, wantStatus)
	}
	if stepSt != wantStatus {
		t.Fatalf("AGENT_DELEGATION step status=%s want %s", stepSt, wantStatus)
	}
	if childSt == "RUNNING" || delSt == "RUNNING" || stepSt == "RUNNING" {
		t.Fatal("RUNNING orphan among child_run / delegation / step")
	}
	// Non-FAILED child must not carry error_code (validRunTransition contract).
	if wantStatus != "FAILED" {
		var errCol sql.NullString
		_ = db.QueryRow(`SELECT error_code FROM agent_runs WHERE id=$1`, childRunID).Scan(&errCol)
		if errCol.Valid && errCol.String != "" {
			t.Fatalf("non-FAILED child has error_code=%q", errCol.String)
		}
	}
	// No residual RUNNING/PENDING TASK children under this child's parent run.
	var parentOfChild string
	_ = db.QueryRow(`SELECT COALESCE(parent_run_id::text,'') FROM agent_runs WHERE id=$1`, childRunID).Scan(&parentOfChild)
	var nRunning int
	_ = db.QueryRow(`
		SELECT COUNT(*) FROM agent_runs
		WHERE workspace_id=$1 AND parent_run_id=$2 AND status IN ('RUNNING','PENDING')
	`, workspaceID, parentOfChild).Scan(&nRunning)
	if nRunning != 0 {
		t.Fatalf("RUNNING/PENDING TASK child residual count=%d", nRunning)
	}
}

// ctxBlockingChatModel is a real model.BaseChatModel that only returns when ctx is done.
// Used so adk.NewChatModelAgent + adk.NewAgentTool exercise cancel/timeout through Eino.
type ctxBlockingChatModel struct{}

func (m *ctxBlockingChatModel) Generate(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *ctxBlockingChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	// Honor the same cancel/timeout contract as Generate.
	<-ctx.Done()
	return nil, ctx.Err()
}

var _ model.BaseChatModel = (*ctxBlockingChatModel)(nil)
