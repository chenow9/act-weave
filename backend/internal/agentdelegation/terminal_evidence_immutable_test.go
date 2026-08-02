package agentdelegation_test

import (
	"context"
	"testing"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

// TestTerminalDelegationEvidenceImmutable rejects raw SQL mutation of terminal
// evidence fields while preserving original values.
func TestTerminalDelegationEvidenceImmutable(t *testing.T) {
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
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'imm','Imm')`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'imm-ws','Imm','PRODUCTION',$2,$2,$2)
	`, ws, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'m','openai','https://x','m',$3,$3)
	`, model, ws, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'a',$3,$4,$4)
	`, agent, ws, model, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		VALUES($1,$2,$3,'s',$4)
	`, session, ws, agent, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_runs(
			id,workspace_id,session_id,agent_id,status,trigger_type,
			triggered_by_type,triggered_by_id,trace_id,
			model_snapshot,capability_snapshot,context_policy_snapshot
		) VALUES ($1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'tr',
			'{}'::jsonb,'{}'::jsonb,'{}'::jsonb)
	`, runID, ws, session, agent, owner); err != nil {
		t.Fatal(err)
	}

	repo, _ := agentdelegation.NewRepository(db)
	svc, _ := agentdelegation.NewService(repo)
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	target := agent
	del, _, err := svc.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: ws, ParentRunID: runID,
		CallerAgentID: agent, TargetAgentID: &target, Mode: agentdelegation.ModeInline,
		Protocol: agentdelegation.ProtocolInternal, Origin: agentdelegation.OriginInternal,
		Depth: 1, BindingVersion: 1, ToolCallID: "t1",
		IdempotencyKey: "imm-" + delID,
		InputSummary:   []byte(`{"a":1}`), InputPayload: []byte(`{"b":2}`),
		StepID: stepID, AgentID: agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Record attempt + tokens before terminal.
	if err := svc.RecordDispatchAttempt(ctx, ws, del.ID); err != nil {
		t.Fatal(err)
	}
	outSum := []byte(`{"ok":true,"status":"SUCCEEDED"}`)
	outPay := []byte(`{"result":"done"}`)
	if _, err := svc.FinalizeDelegation(ctx, agentdelegation.FinalizeDelegationInput{
		WorkspaceID: ws, DelegationID: del.ID, StepID: del.StepID,
		Status: agentdelegation.StatusSucceeded, OutputSummary: outSum, OutputPayload: outPay,
	}); err != nil {
		t.Fatal(err)
	}

	// Capture originals.
	var status, outSumDB, outPayDB, errCode, errMsg, remoteTask, remoteCtx string
	var attempt, retry int
	var finishedAt any
	if err := db.QueryRow(`
		SELECT status, output_summary::text, output_payload::text,
		       COALESCE(error_code,''), COALESCE(error_message,''),
		       COALESCE(remote_task_id,''), COALESCE(remote_context_id,''),
		       attempt_count, retry_count, finished_at
		FROM agent_run_delegations WHERE workspace_id=$1 AND id=$2
	`, ws, del.ID).Scan(&status, &outSumDB, &outPayDB, &errCode, &errMsg, &remoteTask, &remoteCtx, &attempt, &retry, &finishedAt); err != nil {
		t.Fatal(err)
	}
	if status != "SUCCEEDED" {
		t.Fatalf("status=%s", status)
	}

	// Same-status finalize must still work (step reconcile only; no delegation mutation).
	if _, err := svc.FinalizeDelegation(ctx, agentdelegation.FinalizeDelegationInput{
		WorkspaceID: ws, DelegationID: del.ID, StepID: del.StepID,
		Status: agentdelegation.StatusSucceeded, OutputSummary: outSum, OutputPayload: outPay,
	}); err != nil {
		t.Fatalf("same-status finalize: %v", err)
	}

	// Tamper attempts — each must fail and leave originals intact.
	tampers := []struct {
		name string
		sql  string
		args []any
	}{
		{"status", `UPDATE agent_run_delegations SET status='FAILED' WHERE workspace_id=$1 AND id=$2`, []any{ws, del.ID}},
		{"output_summary", `UPDATE agent_run_delegations SET output_summary='{"hacked":true}'::jsonb WHERE workspace_id=$1 AND id=$2`, []any{ws, del.ID}},
		{"output_payload", `UPDATE agent_run_delegations SET output_payload='{"hacked":true}'::jsonb WHERE workspace_id=$1 AND id=$2`, []any{ws, del.ID}},
		{"error_code", `UPDATE agent_run_delegations SET error_code='HACK' WHERE workspace_id=$1 AND id=$2`, []any{ws, del.ID}},
		{"error_message", `UPDATE agent_run_delegations SET error_message='hack' WHERE workspace_id=$1 AND id=$2`, []any{ws, del.ID}},
		{"remote_task_id", `UPDATE agent_run_delegations SET remote_task_id='evil' WHERE workspace_id=$1 AND id=$2`, []any{ws, del.ID}},
		{"remote_context_id", `UPDATE agent_run_delegations SET remote_context_id='evil' WHERE workspace_id=$1 AND id=$2`, []any{ws, del.ID}},
		{"attempt_count", `UPDATE agent_run_delegations SET attempt_count=99 WHERE workspace_id=$1 AND id=$2`, []any{ws, del.ID}},
		{"retry_count", `UPDATE agent_run_delegations SET retry_count=99 WHERE workspace_id=$1 AND id=$2`, []any{ws, del.ID}},
		{"finished_at", `UPDATE agent_run_delegations SET finished_at=NOW() - INTERVAL '1 day' WHERE workspace_id=$1 AND id=$2`, []any{ws, del.ID}},
	}
	for _, tc := range tampers {
		_, err := db.Exec(tc.sql, tc.args...)
		if err == nil {
			t.Fatalf("tamper %s must fail", tc.name)
		}
	}
	// Verify originals preserved.
	var status2, outSum2, outPay2, errCode2, errMsg2, remoteTask2, remoteCtx2 string
	var attempt2, retry2 int
	if err := db.QueryRow(`
		SELECT status, output_summary::text, output_payload::text,
		       COALESCE(error_code,''), COALESCE(error_message,''),
		       COALESCE(remote_task_id,''), COALESCE(remote_context_id,''),
		       attempt_count, retry_count
		FROM agent_run_delegations WHERE workspace_id=$1 AND id=$2
	`, ws, del.ID).Scan(&status2, &outSum2, &outPay2, &errCode2, &errMsg2, &remoteTask2, &remoteCtx2, &attempt2, &retry2); err != nil {
		t.Fatal(err)
	}
	if status2 != status || outSum2 != outSumDB || outPay2 != outPayDB ||
		errCode2 != errCode || errMsg2 != errMsg || remoteTask2 != remoteTask ||
		remoteCtx2 != remoteCtx || attempt2 != attempt || retry2 != retry {
		t.Fatalf("evidence mutated: was status=%s sum=%s pay=%s attempt=%d; now status=%s sum=%s pay=%s attempt=%d",
			status, outSumDB, outPayDB, attempt, status2, outSum2, outPay2, attempt2)
	}
}
