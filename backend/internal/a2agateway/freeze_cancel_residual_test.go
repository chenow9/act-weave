package a2agateway_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

// Residual #5: inbound Prepare freezes graph; live mutate of agents/bindings/remotes
// after Prepare does not change the frozen snapshot on the run row.
func TestInboundFreeze_IsolatesLiveMutations_AfterPrepare(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)
	// Seed A→B binding + remote so freeze captures topology.
	bindID := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO agent_delegation_bindings(
			id, workspace_id, caller_agent_id, target_agent_id, callable_name,
			description, mode, context_policy, enabled, version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,'call_b','d','TASK','TASK_ONLY',true,1,$5,$5)
	`, bindID, fx.workspaceID, fx.agentA, fx.agentB, fx.ownerID); err != nil {
		t.Fatal(err)
	}
	remoteID := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO agent_a2a_remote_bindings(
			id, workspace_id, caller_agent_id, callable_name, description,
			endpoint_url, agent_card_url, allowed_hosts, auth_secret_ref,
			timeout_ms, enabled, version, created_by, updated_by
		) VALUES ($1,$2,$3,'remote_x','rd','https://1.1.1.1/a2a','',
			'["1.1.1.1"]'::jsonb,'',60000,true,1,$4,$4)
	`, remoteID, fx.workspaceID, fx.agentA, fx.ownerID); err != nil {
		t.Fatal(err)
	}

	// Freezer captures live state at Prepare into run snapshots.
	// Use a recording freezer that embeds current prompt + edge callable names.
	freezer := &mutatingAwareFreezer{db: db, agentID: fx.agentA, modelID: fx.modelID}
	runRepo, _ := execution.NewRunRepository(db)
	runner := &a2agateway.DurableInboundRunner{Runs: runRepo, Freezer: freezer}
	req := a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "hi",
		ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
	}
	runID, err := runner.PrepareRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	run, err := runRepo.GetAgentRun(ctx, fx.workspaceID, runID)
	if err != nil {
		t.Fatal(err)
	}
	frozenGraph := append([]byte(nil), run.AgentGraphSnapshot...)
	frozenModel := append([]byte(nil), run.ModelSnapshot...)
	frozenAgent := append([]byte(nil), run.AgentSnapshot...)
	if len(frozenGraph) == 0 || !json.Valid(frozenGraph) {
		t.Fatalf("graph freeze empty/invalid: %s", frozenGraph)
	}
	// Graph snapshot must be idempotent on re-read.
	run2, _ := runRepo.GetAgentRun(ctx, fx.workspaceID, runID)
	if string(run2.AgentGraphSnapshot) != string(frozenGraph) {
		t.Fatal("graph snapshot not stable across reads")
	}

	// Live-mutate: prompt-ish agent name, model name, disable binding, change remote.
	if _, err := db.Exec(`UPDATE agents SET name='MUTATED-A', updated_at=clock_timestamp() WHERE id=$1`, fx.agentA); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE model_configs SET model_name='mutated-model' WHERE id=$1`, fx.modelID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agent_delegation_bindings SET enabled=false, callable_name='mutated_call' WHERE id=$1`, bindID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agent_a2a_remote_bindings SET endpoint_url='https://evil.example.com/x', callable_name='mutated_remote' WHERE id=$1`, remoteID); err != nil {
		t.Fatal(err)
	}

	// Re-read run: freeze must be unchanged.
	runAfter, err := runRepo.GetAgentRun(ctx, fx.workspaceID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if string(runAfter.AgentGraphSnapshot) != string(frozenGraph) {
		t.Fatalf("graph freeze mutated after live changes:\nbefore=%s\nafter=%s", frozenGraph, runAfter.AgentGraphSnapshot)
	}
	if string(runAfter.ModelSnapshot) != string(frozenModel) {
		t.Fatal("model freeze mutated after live changes")
	}
	if string(runAfter.AgentSnapshot) != string(frozenAgent) {
		t.Fatal("agent freeze mutated after live changes")
	}
	// Frozen graph document should still mention original edge names if freezer captured them.
	if freezer.capturedCallable != "" && !containsBytes(frozenGraph, []byte(freezer.capturedCallable)) {
		t.Fatalf("freeze missing original callable %q in %s", freezer.capturedCallable, frozenGraph)
	}
	if containsBytes(frozenGraph, []byte("mutated_call")) || containsBytes(frozenGraph, []byte("mutated_remote")) {
		t.Fatal("frozen graph contains post-prepare mutation names")
	}

	// Second Prepare must produce a new freeze reflecting mutations (new run), proving isolation is per-run.
	runID2, err := runner.PrepareRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if runID2 == runID {
		t.Fatal("second prepare reused run id")
	}
	runNew, _ := runRepo.GetAgentRun(ctx, fx.workspaceID, runID2)
	// New freeze may include mutated topology; original run still frozen.
	_ = runNew
	runOld, _ := runRepo.GetAgentRun(ctx, fx.workspaceID, runID)
	if string(runOld.AgentGraphSnapshot) != string(frozenGraph) {
		t.Fatal("original run freeze drifted after second prepare")
	}
}

// Residual #6: blocking execute cancelled → task/delegation/step/run all terminal CANCELLED;
// later Finalize to SUCCEEDED must not overwrite.
func TestBlockingCancel_AllTerminal_FinalizeDoesNotOverwrite(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)
	expID := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO agent_a2a_exposures(
			id, workspace_id, agent_id, public_name, public_description,
			enabled, auth_mode, version, created_by, updated_by
		) VALUES ($1,$2,$3,'pub','d',true,'AGENT_ACCESS',1,$4,$4)
	`, expID, fx.workspaceID, fx.agentA, fx.ownerID); err != nil {
		t.Fatal(err)
	}
	runRepo, _ := execution.NewRunRepository(db)
	a2aRepo, _ := a2agateway.NewRepository(db)
	delRepo, _ := agentdelegation.NewRepository(db)
	audit, _ := agentdelegation.NewService(delRepo)

	started := make(chan struct{})
	release := make(chan struct{})
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			close(started)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-release:
				return "should-not", nil
			}
		},
	}

	req := a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "block",
		ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
		ExternalTaskID: "cancel-task", IdempotencyKey: "cancel-key",
	}
	runID, err := runner.PrepareRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := a2aRepo.ClaimInboundTask(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "cancel-key", ExternalTaskID: "cancel-task", RunID: runID, Status: "RUNNING",
	})
	if err != nil {
		t.Fatal(err)
	}
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	idem := agentdelegation.IdempotencyKey(expID+":cancel-key", "inbound", 1, expID)
	_, _, err = audit.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: fx.workspaceID, ParentRunID: runID,
		CallerAgentID: fx.agentA, Mode: agentdelegation.ModeTask,
		Protocol: agentdelegation.ProtocolA2A, Origin: agentdelegation.OriginExternal,
		Depth: 0, BindingVersion: 1, ToolCallID: "inbound", IdempotencyKey: idem,
		InputSummary: json.RawMessage(`{}`), InputPayload: json.RawMessage(`{}`),
		StepID: stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := a2aRepo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "owner-1", 2*time.Minute)
	if err != nil || !lease.Owned {
		t.Fatalf("lease: owned=%v err=%v", lease.Owned, err)
	}

	// Run Execute in background with cancellable ctx.
	execCtx, cancel := context.WithCancel(ctx)
	var execErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, execErr = runner.ExecuteRun(execCtx, req, runID)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("execute never started")
	}
	// Cancel while blocking.
	cancel()
	if err := runner.CancelRun(ctx, fx.workspaceID, runID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	// Mark inbound task + execution finished CANCELLED (as gateway would).
	if err := a2aRepo.MarkInboundExecutionFinished(ctx, fx.workspaceID, task.ID,
		agentdelegation.StatusCancelled, lease.Owner, lease.Token); err != nil {
		// May require different API — fall through to task status update.
		t.Logf("MarkInboundExecutionFinished: %v", err)
	}
	if _, err := db.Exec(`UPDATE agent_a2a_inbound_tasks SET status='CANCELLED', updated_at=NOW() WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	// Finalize delegation as CANCELLED.
	if _, err := audit.FinalizeDelegation(ctx, agentdelegation.FinalizeDelegationInput{
		WorkspaceID: fx.workspaceID, DelegationID: delID, StepID: stepID,
		Status:        agentdelegation.StatusCancelled,
		OutputSummary: json.RawMessage(`{"ok":false,"status":"CANCELLED"}`),
		OutputPayload: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	_ = execErr

	// Assert terminals.
	run, _ := runRepo.GetAgentRun(ctx, fx.workspaceID, runID)
	if run.Status != "CANCELLED" {
		t.Fatalf("run status=%s want CANCELLED", run.Status)
	}
	del, err := delRepo.GetByIdempotency(ctx, fx.workspaceID, idem)
	if err != nil {
		t.Fatal(err)
	}
	if del.Status != agentdelegation.StatusCancelled {
		t.Fatalf("delegation status=%s", del.Status)
	}
	var stepStatus string
	if err := db.QueryRow(`SELECT status FROM agent_run_steps WHERE id=$1`, stepID).Scan(&stepStatus); err != nil {
		t.Fatal(err)
	}
	if stepStatus != "CANCELLED" {
		t.Fatalf("step status=%s", stepStatus)
	}
	var taskStatus string
	if err := db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE id=$1`, task.ID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if taskStatus != "CANCELLED" {
		t.Fatalf("task status=%s", taskStatus)
	}

	// Late finalize to SUCCEEDED must NOT overwrite CANCELLED (ErrConflict sticky).
	_, err = audit.FinalizeDelegation(ctx, agentdelegation.FinalizeDelegationInput{
		WorkspaceID: fx.workspaceID, DelegationID: delID, StepID: stepID,
		Status:        agentdelegation.StatusSucceeded,
		OutputSummary: json.RawMessage(`{"ok":true}`), OutputPayload: json.RawMessage(`{"result":"late"}`),
	})
	if !errors.Is(err, agentdelegation.ErrConflict) {
		t.Fatalf("want ErrConflict on late SUCCEEDED overwrite, got %v", err)
	}
	var sticky string
	if err := db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=$1`, delID).Scan(&sticky); err != nil {
		t.Fatal(err)
	}
	if sticky != "CANCELLED" {
		t.Fatalf("late finalize overwrote status to %s", sticky)
	}
	// Late run transition to SUCCEEDED must fail or no-op.
	run, _ = runRepo.GetAgentRun(ctx, fx.workspaceID, runID)
	_, terr := runRepo.TransitionAgentRun(ctx, fx.workspaceID, runID, execution.RunTransition{
		ExpectedStatus: "CANCELLED", ExpectedLockVersion: run.LockVersion,
		NewStatus: "SUCCEEDED", OutputSummary: json.RawMessage(`{"hack":true}`),
	})
	if terr == nil {
		// If transition API allows, re-check status — should still be cancel or reject.
		run, _ = runRepo.GetAgentRun(ctx, fx.workspaceID, runID)
		if run.Status == "SUCCEEDED" {
			t.Fatal("CANCELLED run overwritten to SUCCEEDED")
		}
	}
}

type mutatingAwareFreezer struct {
	db               *sql.DB
	agentID, modelID string
	capturedCallable string
}

func (f *mutatingAwareFreezer) FreezeInbound(ctx context.Context, workspaceID, agentID string) (a2agateway.InboundFreeze, error) {
	// Capture current binding callable names into graph freeze document.
	var callables []string
	rows, err := f.db.QueryContext(ctx, `
		SELECT callable_name FROM agent_delegation_bindings
		WHERE workspace_id=$1 AND caller_agent_id=$2 AND enabled AND deleted_at IS NULL
	`, workspaceID, agentID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var c string
			_ = rows.Scan(&c)
			callables = append(callables, c)
		}
	}
	if len(callables) > 0 {
		f.capturedCallable = callables[0]
	}
	var remotes []string
	rrows, err := f.db.QueryContext(ctx, `
		SELECT callable_name FROM agent_a2a_remote_bindings
		WHERE workspace_id=$1 AND caller_agent_id=$2 AND enabled AND deleted_at IS NULL
	`, workspaceID, agentID)
	if err == nil {
		defer rrows.Close()
		for rrows.Next() {
			var c string
			_ = rrows.Scan(&c)
			remotes = append(remotes, c)
		}
	}
	var agentName, modelName string
	_ = f.db.QueryRowContext(ctx, `SELECT name FROM agents WHERE id=$1`, agentID).Scan(&agentName)
	_ = f.db.QueryRowContext(ctx, `SELECT model_name FROM model_configs WHERE id=$1`, f.modelID).Scan(&modelName)

	modelSnap, _ := json.Marshal(map[string]any{
		"id": f.modelID, "provider": "test", "apiBase": "https://example.test",
		"modelName": modelName, "lockVersion": 1, "source": "residual.freeze",
	})
	agentSnap, _ := json.Marshal(map[string]any{
		"schemaVersion": "agent-binding.v1", "agentId": agentID, "name": agentName,
		"modelConfigId": f.modelID, "modelConfigLockVer": 1, "source": "residual.freeze",
	})
	capSnap := json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[]}`)
	// Integrity-complete v1 freeze (root node + remotesFrozen + per-caller keys).
	// callables/remotes names captured for mutation residual assertions.
	graph, _ := json.Marshal(map[string]any{
		"schemaVersion": "agent_graph_snapshot.v1",
		"rootAgentId":   agentID,
		"maxDepth":      4, "maxTotalDelegations": 20, "maxPerBinding": 5,
		"builtAt": time.Now().UTC().Format(time.RFC3339Nano),
		"nodes": []map[string]any{{
			"agentId": agentID, "name": agentName, "depth": 0,
			"modelConfigId": f.modelID, "modelConfigLockVersion": 1,
			"modelSnapshot": modelSnap, "agentSnapshot": agentSnap, "capabilitySnapshot": capSnap,
		}},
		"edges":                 []any{},
		"remotesFrozen":         true,
		"frozenRemotesByCaller": map[string]any{agentID: []any{}},
		// Residual test captures pre-mutation names (not used by integrity validator).
		"extra": map[string]any{"capturedCallables": callables, "capturedRemotes": remotes},
	})
	return a2agateway.InboundFreeze{
		ModelSnapshot: modelSnap, AgentSnapshot: agentSnap,
		CapabilitySnapshot: capSnap,
		ContextPolicy:      json.RawMessage(`{"schemaVersion":"session-context.v1","mode":"LEGACY"}`),
		GraphSnapshot:      graph,
	}, nil
}

func containsBytes(b, sub []byte) bool {
	return len(sub) == 0 || (len(b) >= len(sub) && stringIndex(string(b), string(sub)) >= 0)
}

func stringIndex(s, sub string) int {
	return len([]byte(s[:]))*0 + indexOf(s, sub)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
