package agentdelegation_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

// TestTASKChildFinish_RecoveredInFinalizeTX: when FinishChild fails but finalize
// succeeds (or outbox worker re-runs FinalizeDelegation), the same-TX linked child
// terminal recovery leaves no RUNNING child.
func TestTASKChildFinish_RecoveredInFinalizeTX(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()

	owner := uuid.Must(uuid.NewV7()).String()
	ws := uuid.Must(uuid.NewV7()).String()
	model := uuid.Must(uuid.NewV7()).String()
	agentA := uuid.Must(uuid.NewV7()).String()
	agentB := uuid.Must(uuid.NewV7()).String()
	session := uuid.Must(uuid.NewV7()).String()
	parentRun := uuid.Must(uuid.NewV7()).String()
	childRun := uuid.Must(uuid.NewV7()).String()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users(id,username,display_name) VALUES($1,'cf','CF')`, []any{owner}},
		{`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES($1,'cf2-ws','CF','PRODUCTION',$2,$2,$2)`, []any{ws, owner}},
		{`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES($1,$2,'m','openai','https://x','m',$3,$3)`, []any{model, ws, owner}},
		{`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES($1,$2,'A',$3,$4,$4)`, []any{agentA, ws, model, owner}},
		{`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES($1,$2,'B',$3,$4,$4)`, []any{agentB, ws, model, owner}},
		{`INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by) VALUES($1,$2,$3,'s',$4)`, []any{session, ws, agentA, owner}},
		{`INSERT INTO agent_runs(id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,trace_id,model_snapshot,capability_snapshot,context_policy_snapshot)
		  VALUES($1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'tr','{}'::jsonb,'{}'::jsonb,'{}'::jsonb)`, []any{parentRun, ws, session, agentA, owner}},
		// Linked TASK child left RUNNING (simulates FinishChild never succeeding).
		{`INSERT INTO agent_runs(id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,trace_id,model_snapshot,capability_snapshot,context_policy_snapshot,parent_run_id)
		  VALUES($1,$2,$3,$4,'RUNNING','DELEGATION_TASK','USER',$5,'tr-c','{}'::jsonb,'{}'::jsonb,'{}'::jsonb,$6)`,
			[]any{childRun, ws, session, agentB, owner, parentRun}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// parent_delegation_id may be required after migrations — set if column exists via optional update.
	_, _ = db.Exec(`UPDATE agent_runs SET parent_delegation_id=NULL WHERE id=$1`, childRun)

	delRepo, _ := agentdelegation.NewRepository(db)
	audit, _ := agentdelegation.NewService(delRepo)
	a2aRepo, _ := a2agateway.NewRepository(db)

	target := agentB
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	del, _, err := audit.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: ws, ParentRunID: parentRun,
		CallerAgentID: agentA, TargetAgentID: &target,
		Mode: agentdelegation.ModeTask, Protocol: agentdelegation.ProtocolInternal,
		Origin: agentdelegation.OriginInternal, Depth: 1, BindingVersion: 1,
		ToolCallID: "task-1", IdempotencyKey: "child-finish-" + delID,
		InputSummary: []byte(`{"mode":"TASK"}`), InputPayload: []byte(`{"request":"x"}`),
		StepID: stepID, AgentID: agentA, ChildRunID: &childRun,
	})
	if err != nil {
		// ChildRunID on create may be ignored until SetChildRunID — create without child then set.
		del, _, err = audit.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
			ID: delID, WorkspaceID: ws, ParentRunID: parentRun,
			CallerAgentID: agentA, TargetAgentID: &target,
			Mode: agentdelegation.ModeTask, Protocol: agentdelegation.ProtocolInternal,
			Origin: agentdelegation.OriginInternal, Depth: 1, BindingVersion: 1,
			ToolCallID: "task-1", IdempotencyKey: "child-finish-" + delID,
			InputSummary: []byte(`{"mode":"TASK"}`), InputPayload: []byte(`{"request":"x"}`),
			StepID: stepID, AgentID: agentA,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := audit.SetChildRunID(ctx, ws, del.ID, childRun); err != nil {
			t.Fatal(err)
		}
	}
	if err := audit.RecordDispatchAttempt(ctx, ws, del.ID); err != nil {
		t.Fatal(err)
	}

	// Simulate FinishChild failure: leave child RUNNING, finalize via outbox path.
	// First finalize fails once by using a repo that... simpler: enqueue outbox and drain.
	outSum := []byte(`{"ok":true,"status":"SUCCEEDED"}`)
	outPay := []byte(`{"result":"done"}`)
	in := agentdelegation.FinalizeDelegationInput{
		WorkspaceID: ws, DelegationID: del.ID, StepID: del.StepID,
		Status: agentdelegation.StatusSucceeded, OutputSummary: outSum, OutputPayload: outPay,
		ChildRunID: &childRun,
	}
	// Direct finalize with same-TX child recovery (production recovery path).
	if _, err := audit.FinalizeDelegation(ctx, in); err != nil {
		// If finalize fails, outbox recovery.
		payload, _ := json.Marshal(in)
		if qerr := a2aRepo.EnqueueFinalizeOutbox(ctx, ws, del.ID, del.StepID, payload); qerr != nil {
			t.Fatalf("finalize=%v enqueue=%v", err, qerr)
		}
		worker, _ := a2agateway.NewFinalizeWorker(a2aRepo, audit, nil)
		for i := 0; i < 5; i++ {
			worker.DrainOnce(ctx)
			time.Sleep(20 * time.Millisecond)
		}
	}

	var childSt, delSt, stepSt string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, childRun).Scan(&childSt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=$1`, del.ID).Scan(&delSt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM agent_run_steps WHERE id=$1`, del.StepID).Scan(&stepSt); err != nil {
		t.Fatal(err)
	}
	if childSt == "RUNNING" || childSt == "PENDING" {
		t.Fatalf("child still %s", childSt)
	}
	if delSt == "RUNNING" || stepSt == "RUNNING" {
		t.Fatalf("del=%s step=%s", delSt, stepSt)
	}
	if delSt != childSt && !(delSt == "SUCCEEDED" && childSt == "SUCCEEDED") {
		// Status intent must agree (both terminal, same family preferred).
		t.Logf("del=%s child=%s step=%s (both terminal ok)", delSt, childSt, stepSt)
	}
	if delSt != "SUCCEEDED" || childSt != "SUCCEEDED" || stepSt != "SUCCEEDED" {
		t.Fatalf("want all SUCCEEDED, got del=%s child=%s step=%s", delSt, childSt, stepSt)
	}

	// Sticky: different terminal on already-terminal child must fail closed.
	if _, err := audit.FinalizeDelegation(ctx, agentdelegation.FinalizeDelegationInput{
		WorkspaceID: ws, DelegationID: del.ID, StepID: del.StepID,
		Status: agentdelegation.StatusFailed, OutputSummary: outSum, OutputPayload: outPay,
		ChildRunID: &childRun,
	}); err == nil {
		t.Fatal("different terminal finalize must conflict")
	}
	var childSt2 string
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, childRun).Scan(&childSt2)
	if childSt2 != "SUCCEEDED" {
		t.Fatalf("child rewritten to %s", childSt2)
	}
}
