package agentdelegation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

// TestFinalizeDelegation_FirstTerminal_StepEvidenceMismatch_Conflicts: delegation
// still RUNNING while paired step is already SUCCEEDED with different evidence.
// FinalizeDelegation must return ErrConflict and leave delegation RUNNING (rollback).
func TestFinalizeDelegation_FirstTerminal_StepEvidenceMismatch_Conflicts(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()

	owner := uuid.Must(uuid.NewV7()).String()
	ws := uuid.Must(uuid.NewV7()).String()
	model := uuid.Must(uuid.NewV7()).String()
	agent := uuid.Must(uuid.NewV7()).String()
	session := uuid.Must(uuid.NewV7()).String()
	runID := uuid.Must(uuid.NewV7()).String()
	seed := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(sql, args...); err != nil {
			t.Fatalf("%v: %v", sql[:min(40, len(sql))], err)
		}
	}
	seed(`INSERT INTO users(id,username,display_name) VALUES($1,'fev','F')`, owner)
	seed(`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES($1,'fev-ws','F','PRODUCTION',$2,$2,$2)`, ws, owner)
	seed(`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES($1,$2,'m','openai','https://x','m',$3,$3)`, model, ws, owner)
	seed(`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES($1,$2,'a',$3,$4,$4)`, agent, ws, model, owner)
	seed(`INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by) VALUES($1,$2,$3,'s',$4)`, session, ws, agent, owner)
	seed(`INSERT INTO agent_runs(id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,trace_id,model_snapshot,capability_snapshot,context_policy_snapshot)
		VALUES($1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'tr','{}'::jsonb,'{}'::jsonb,'{}'::jsonb)`, runID, ws, session, agent, owner)

	repo, _ := agentdelegation.NewRepository(db)
	svc, _ := agentdelegation.NewService(repo)
	target := agent
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	del, _, err := svc.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: ws, ParentRunID: runID,
		CallerAgentID: agent, TargetAgentID: &target,
		Mode: agentdelegation.ModeInline, Protocol: agentdelegation.ProtocolInternal,
		Origin: agentdelegation.OriginInternal, Depth: 1, BindingVersion: 1,
		ToolCallID: "t1", IdempotencyKey: "fev-" + delID,
		InputSummary: []byte(`{"callableName":"c"}`), InputPayload: []byte(`{"request":"x"}`),
		StepID: stepID, AgentID: agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordDispatchAttempt(ctx, ws, del.ID); err != nil {
		t.Fatal(err)
	}

	// Terminalize step once while still RUNNING (trigger allows RUNNING→terminal).
	// Evidence A: original sticky write.
	origSum := []byte(`{"ok":true,"status":"SUCCEEDED","marker":"original-A"}`)
	if _, err := db.Exec(`
		UPDATE agent_run_steps SET
			status='SUCCEEDED',
			output_summary=$2::jsonb,
			error_code=NULL,
			finished_at=NOW()
		WHERE workspace_id=$1 AND id=$3 AND status='RUNNING'
	`, ws, string(origSum), del.StepID); err != nil {
		t.Fatal(err)
	}
	var finishedAt time.Time
	if err := db.QueryRow(`SELECT finished_at FROM agent_run_steps WHERE id=$1`, del.StepID).Scan(&finishedAt); err != nil {
		t.Fatal(err)
	}

	// Delegation still RUNNING; finalize with DIFFERENT output evidence.
	badSum := []byte(`{"ok":true,"status":"SUCCEEDED","marker":"different-B"}`)
	_, finErr := svc.FinalizeDelegation(ctx, agentdelegation.FinalizeDelegationInput{
		WorkspaceID: ws, DelegationID: del.ID, StepID: del.StepID,
		Status: agentdelegation.StatusSucceeded, OutputSummary: badSum, OutputPayload: []byte(`{"result":"x"}`),
	})
	if finErr == nil || !errors.Is(finErr, agentdelegation.ErrConflict) {
		t.Fatalf("want ErrConflict on evidence mismatch, got %v", finErr)
	}

	// Delegation must remain RUNNING (transaction rolled back).
	var delSt string
	if err := db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=$1`, del.ID).Scan(&delSt); err != nil {
		t.Fatal(err)
	}
	if delSt != "RUNNING" {
		t.Fatalf("delegation status=%s want RUNNING after conflict", delSt)
	}
	// Step evidence unchanged (no rewrite, finished_at stable).
	var stOut string
	var finished2 time.Time
	if err := db.QueryRow(`SELECT output_summary::text, finished_at FROM agent_run_steps WHERE id=$1`, del.StepID).
		Scan(&stOut, &finished2); err != nil {
		t.Fatal(err)
	}
	if stOut != string(origSum) && stOut != `{"marker": "original-A", "ok": true, "status": "SUCCEEDED"}` {
		// PostgreSQL may normalize JSON key order — require marker original-A present, not different-B.
		if !containsStr(stOut, "original-A") || containsStr(stOut, "different-B") {
			t.Fatalf("step output changed: %s", stOut)
		}
	}
	if !finished2.Equal(finishedAt) {
		t.Fatalf("finished_at rewritten: %v -> %v", finishedAt, finished2)
	}
}

// TestFinalizeDelegation_FirstTerminal_StepEvidenceMatch_Idempotent: same status
// and exact evidence → finalize succeeds without rewriting step bytes/finished_at.
func TestFinalizeDelegation_FirstTerminal_StepEvidenceMatch_Idempotent(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()

	owner := uuid.Must(uuid.NewV7()).String()
	ws := uuid.Must(uuid.NewV7()).String()
	model := uuid.Must(uuid.NewV7()).String()
	agent := uuid.Must(uuid.NewV7()).String()
	session := uuid.Must(uuid.NewV7()).String()
	runID := uuid.Must(uuid.NewV7()).String()
	seed := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	seed(`INSERT INTO users(id,username,display_name) VALUES($1,'fev2','F')`, owner)
	seed(`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES($1,'fev2-ws','F','PRODUCTION',$2,$2,$2)`, ws, owner)
	seed(`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES($1,$2,'m','openai','https://x','m',$3,$3)`, model, ws, owner)
	seed(`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES($1,$2,'a',$3,$4,$4)`, agent, ws, model, owner)
	seed(`INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by) VALUES($1,$2,$3,'s',$4)`, session, ws, agent, owner)
	seed(`INSERT INTO agent_runs(id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,trace_id,model_snapshot,capability_snapshot,context_policy_snapshot)
		VALUES($1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'tr','{}'::jsonb,'{}'::jsonb,'{}'::jsonb)`, runID, ws, session, agent, owner)

	repo, _ := agentdelegation.NewRepository(db)
	svc, _ := agentdelegation.NewService(repo)
	target := agent
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	del, _, err := svc.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: ws, ParentRunID: runID,
		CallerAgentID: agent, TargetAgentID: &target,
		Mode: agentdelegation.ModeInline, Protocol: agentdelegation.ProtocolInternal,
		Origin: agentdelegation.OriginInternal, Depth: 1, BindingVersion: 1,
		ToolCallID: "t1", IdempotencyKey: "fev2-" + delID,
		InputSummary: []byte(`{"callableName":"c"}`), InputPayload: []byte(`{"request":"x"}`),
		StepID: stepID, AgentID: agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = svc.RecordDispatchAttempt(ctx, ws, del.ID)

	// Canonical object JSON (mustObject normalizes key order the same way).
	outSum := []byte(`{"ok":true,"status":"SUCCEEDED"}`)
	outPay := []byte(`{"result":"done"}`)
	if _, err := db.Exec(`
		UPDATE agent_run_steps SET
			status='SUCCEEDED',
			output_summary=$2::jsonb,
			error_code=NULL,
			finished_at=NOW()
		WHERE id=$1 AND status='RUNNING'
	`, del.StepID, string(outSum)); err != nil {
		t.Fatal(err)
	}
	var finBefore time.Time
	var outBefore string
	_ = db.QueryRow(`SELECT finished_at, output_summary::text FROM agent_run_steps WHERE id=$1`, del.StepID).
		Scan(&finBefore, &outBefore)

	// First finalize: del RUNNING, step already terminal with matching evidence.
	if _, err := svc.FinalizeDelegation(ctx, agentdelegation.FinalizeDelegationInput{
		WorkspaceID: ws, DelegationID: del.ID, StepID: del.StepID,
		Status: agentdelegation.StatusSucceeded, OutputSummary: outSum, OutputPayload: outPay,
	}); err != nil {
		t.Fatalf("matching evidence finalize: %v", err)
	}
	var delSt string
	_ = db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=$1`, del.ID).Scan(&delSt)
	if delSt != "SUCCEEDED" {
		t.Fatalf("del=%s", delSt)
	}
	var finAfter time.Time
	var outAfter string
	_ = db.QueryRow(`SELECT finished_at, output_summary::text FROM agent_run_steps WHERE id=$1`, del.StepID).
		Scan(&finAfter, &outAfter)
	if !finAfter.Equal(finBefore) {
		t.Fatalf("finished_at rewritten on match path")
	}
	if outAfter != outBefore {
		t.Fatalf("output rewritten: %s -> %s", outBefore, outAfter)
	}

	// Second same-status finalize remains idempotent.
	if _, err := svc.FinalizeDelegation(ctx, agentdelegation.FinalizeDelegationInput{
		WorkspaceID: ws, DelegationID: del.ID, StepID: del.StepID,
		Status: agentdelegation.StatusSucceeded, OutputSummary: outSum, OutputPayload: outPay,
	}); err != nil {
		t.Fatalf("second finalize: %v", err)
	}
}

func containsStr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
