package a2agateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

// Repository-level lease fencing simulation (NOT production JSON-RPC entry).
// For real inboundExecutor via Register/ServeHTTP + message/send, see production_path_e2e_test.go.
func TestInboundExecutor_LeaseHeartbeatAndStaleReject(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	delRepo, _ := agentdelegation.NewRepository(db)
	audit, _ := agentdelegation.NewService(delRepo)
	expID := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO agent_a2a_exposures(
			id, workspace_id, agent_id, public_name, public_description,
			enabled, auth_mode, version, created_by, updated_by
		) VALUES ($1,$2,$3,'pub','d',true,'AGENT_ACCESS',1,$4,$4)
	`, expID, fx.workspaceID, fx.agentA, fx.ownerID); err != nil {
		t.Fatal(err)
	}

	var execs atomic.Int64
	block := make(chan struct{})
	runRepo, _ := execution.NewRunRepository(db)
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			execs.Add(1)
			// Hold longer than original lease; heartbeat must keep ownership.
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-block:
				return "done", nil
			case <-time.After(1500 * time.Millisecond):
				return "held", nil
			}
		},
	}
	const shortLease = 400 * time.Millisecond
	gw, err := a2agateway.NewInboundGateway(repo, audit, runner, "http://127.0.0.1",
		a2agateway.HeaderPresenceAuth{}, a2agateway.WithLeaseTTL(shortLease))
	if err != nil {
		t.Fatal(err)
	}
	_ = gw

	// Drive lease path like inboundExecutor: prepare, claim task, claim lease, fence, execute.
	req := a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "x",
		ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
		ExternalTaskID: "fence-t", IdempotencyKey: "fence-k",
	}
	runID, err := runner.PrepareRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := repo.ClaimInboundTask(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "fence-k", ExternalTaskID: "fence-t", RunID: runID, Status: "RUNNING",
	})
	if err != nil {
		t.Fatal(err)
	}
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	del, _, err := audit.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: fx.workspaceID, ParentRunID: runID,
		CallerAgentID: fx.agentA, Mode: agentdelegation.ModeTask,
		Protocol: agentdelegation.ProtocolA2A, Origin: agentdelegation.OriginExternal,
		Depth: 0, BindingVersion: 1, ToolCallID: "inbound",
		IdempotencyKey: agentdelegation.IdempotencyKey(expID+":fence-k", "inbound", 1, expID),
		InputSummary:   json.RawMessage(`{}`), InputPayload: json.RawMessage(`{}`),
		StepID: stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.BindInboundTaskDelegation(ctx, fx.workspaceID, task.ID, del.ID); err != nil {
		t.Fatal(err)
	}
	if err := audit.RecordDispatchAttempt(ctx, fx.workspaceID, del.ID); err != nil {
		t.Fatal(err)
	}

	lease, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "owner-a", shortLease)
	if err != nil || !lease.Owned {
		t.Fatalf("claim: owned=%v err=%v", lease.Owned, err)
	}

	// Heartbeat like production inboundExecutor.
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fence := a2agateway.ExecutionFence{
		WorkspaceID: fx.workspaceID, TaskID: task.ID,
		Owner: lease.Owner, Token: lease.Token, Generation: lease.Generation,
		AssertHeld: func(c context.Context) error {
			return repo.AssertInboundExecutionHeld(c, fx.workspaceID, task.ID, lease.Owner, lease.Token, lease.Generation)
		},
	}
	execCtx = a2agateway.WithExecutionFence(execCtx, fence)
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		ticker := time.NewTicker(shortLease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-execCtx.Done():
				return
			case <-ticker.C:
				if err := repo.RenewInboundExecutionLease(execCtx, fx.workspaceID, task.ID, lease.Owner, lease.Token, shortLease); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	done := make(chan struct{})
	var result a2agateway.InboundRunResult
	var execErr error
	go func() {
		defer close(done)
		result, execErr = runner.ExecuteRun(execCtx, req, runID)
	}()

	// While running past original lease, second claim must not own.
	time.Sleep(shortLease + 200*time.Millisecond)
	l2, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "owner-b", shortLease)
	if err != nil {
		t.Fatal(err)
	}
	if l2.Owned {
		t.Fatal("heartbeat must prevent reclaim")
	}
	// Expired/stale renew must fail.
	if err := repo.RenewInboundExecutionLease(ctx, fx.workspaceID, task.ID, "owner-b", "bad", shortLease); err == nil {
		t.Fatal("stale renew must fail")
	}
	// Stale finalize mark must fail.
	if err := repo.MarkInboundExecutionFinishedGen(ctx, fx.workspaceID, task.ID,
		agentdelegation.StatusSucceeded, "owner-b", "bad", 99); err == nil {
		t.Fatal("stale mark must fail")
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		close(block)
		<-done
	}
	cancel()
	<-hbDone
	_ = result
	_ = execErr

	// Owner can still mark with valid generation.
	if err := repo.MarkInboundExecutionFinishedGen(ctx, fx.workspaceID, task.ID,
		agentdelegation.StatusSucceeded, lease.Owner, lease.Token, lease.Generation); err != nil {
		// May already be finished by runner; conflict is ok only if terminal.
		var st string
		_ = db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE id=$1`, task.ID).Scan(&st)
		if st != "SUCCEEDED" && st != "FAILED" && st != "CANCELLED" && st != "TIMED_OUT" {
			t.Fatalf("owner mark failed and task not terminal: %v status=%s", err, st)
		}
	}

	// Force-expire then reclaim; old token cannot mark again.
	if _, err := db.Exec(`UPDATE agent_a2a_inbound_tasks SET status='RUNNING', execute_lease_until=NOW()-interval '1 second' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	l3, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "owner-c", shortLease)
	if err != nil || !l3.Owned {
		t.Fatalf("reclaim: owned=%v err=%v", l3.Owned, err)
	}
	if err := repo.MarkInboundExecutionFinishedGen(ctx, fx.workspaceID, task.ID,
		agentdelegation.StatusSucceeded, lease.Owner, lease.Token, lease.Generation); err == nil {
		t.Fatal("old generation must not finalize after reclaim")
	}
}

// TestFencedInboundTerminal_TOCTOU_StaleOwnerAfterReclaim proves atomic fence:
// owner A is paused between "would-have-AssertHeld" and terminal write; lease
// expires; owner B reclaims (generation+1); A is released and must fail all
// terminal writes (run/task/delegation/step); B succeeds.
func TestFencedInboundTerminal_TOCTOU_StaleOwnerAfterReclaim(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	delRepo, _ := agentdelegation.NewRepository(db)
	audit, _ := agentdelegation.NewService(delRepo)
	runRepo, _ := execution.NewRunRepository(db)
	expID := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO agent_a2a_exposures(
			id, workspace_id, agent_id, public_name, public_description,
			enabled, auth_mode, version, created_by, updated_by
		) VALUES ($1,$2,$3,'pub','d',true,'AGENT_ACCESS',1,$4,$4)
	`, expID, fx.workspaceID, fx.agentA, fx.ownerID); err != nil {
		t.Fatal(err)
	}

	// Gate between "pre-check" and terminal: simulates TOCTOU window.
	pause := make(chan struct{})
	releaseA := make(chan struct{})
	const shortLease = 250 * time.Millisecond

	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			// Signal we're inside execute (after claim fence attached by test harness).
			close(pause)
			<-releaseA
			// If lease was lost, heartbeat cancelled ctx; still return "success" to
			// force stale owner into fenced terminal.
			return "stale-owner-text", nil
		},
	}
	req := a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "toctou",
		ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
		ExternalTaskID: "toctou-t", IdempotencyKey: "toctou-k",
	}
	runID, err := runner.PrepareRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := repo.ClaimInboundTask(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "toctou-k", ExternalTaskID: "toctou-t", RunID: runID, Status: "RUNNING",
	})
	if err != nil {
		t.Fatal(err)
	}
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	del, _, err := audit.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: fx.workspaceID, ParentRunID: runID,
		CallerAgentID: fx.agentA, Mode: agentdelegation.ModeTask,
		Protocol: agentdelegation.ProtocolA2A, Origin: agentdelegation.OriginExternal,
		Depth: 0, BindingVersion: 1, ToolCallID: "inbound",
		IdempotencyKey: agentdelegation.IdempotencyKey(expID+":toctou-k", "inbound", 1, expID),
		InputSummary:   json.RawMessage(`{}`), InputPayload: json.RawMessage(`{}`),
		StepID: stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.BindInboundTaskDelegation(ctx, fx.workspaceID, task.ID, del.ID); err != nil {
		t.Fatal(err)
	}
	if err := audit.RecordDispatchAttempt(ctx, fx.workspaceID, del.ID); err != nil {
		t.Fatal(err)
	}

	leaseA, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "owner-a", shortLease)
	if err != nil || !leaseA.Owned {
		t.Fatalf("A claim: owned=%v err=%v", leaseA.Owned, err)
	}
	genA := leaseA.Generation

	// Owner A execute under fence (no heartbeat — allow expire).
	execCtx := a2agateway.WithExecutionFence(ctx, a2agateway.ExecutionFence{
		WorkspaceID: fx.workspaceID, TaskID: task.ID, RunID: runID,
		Owner: leaseA.Owner, Token: leaseA.Token, Generation: leaseA.Generation,
		Repo: repo,
	})
	doneA := make(chan struct{})
	var resultA a2agateway.InboundRunResult
	var execErrA error
	go func() {
		defer close(doneA)
		resultA, execErrA = runner.ExecuteRun(execCtx, req, runID)
	}()
	select {
	case <-pause:
	case <-time.After(3 * time.Second):
		t.Fatal("A execute did not enter pause")
	}

	// Expire lease and reclaim as B (generation must advance).
	if _, err := db.Exec(`UPDATE agent_a2a_inbound_tasks SET execute_lease_until=NOW()-interval '2 seconds' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	leaseB, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "owner-b", time.Minute)
	if err != nil || !leaseB.Owned {
		t.Fatalf("B reclaim: owned=%v err=%v", leaseB.Owned, err)
	}
	if leaseB.Generation <= genA {
		t.Fatalf("B generation %d must be > A generation %d", leaseB.Generation, genA)
	}

	// Release A: ExecuteRun returns; A then attempts FencedInboundTerminal (stale).
	close(releaseA)
	<-doneA
	_ = resultA
	_ = execErrA

	outSum, _ := json.Marshal(map[string]any{"ok": true, "status": "SUCCEEDED", "who": "A"})
	outPay, _ := json.Marshal(map[string]any{"result": "from-A"})
	runOut, _ := json.Marshal(map[string]any{"source": "a2a.inbound", "who": "A"})
	errA := repo.FencedInboundTerminal(ctx, a2agateway.FencedTerminalInput{
		WorkspaceID: fx.workspaceID, TaskID: task.ID, RunID: runID,
		Owner: leaseA.Owner, Token: leaseA.Token, Generation: leaseA.Generation,
		TaskStatus: "SUCCEEDED", RunStatus: "SUCCEEDED", ExpectedRunStatus: "RUNNING",
		RunOutputSummary: runOut,
		DelegationID:     del.ID, StepID: stepID,
		DelStatus: "SUCCEEDED", DelOutputSummary: outSum, DelOutputPayload: outPay,
	})
	if errA == nil {
		t.Fatal("stale owner A must not FencedInboundTerminal after B reclaim")
	}
	if !errors.Is(errA, a2agateway.ErrConflict) {
		// Accept any error that is not success; prefer conflict.
		t.Logf("A terminal err (want conflict): %v", errA)
	}

	// B succeeds atomically.
	outSumB, _ := json.Marshal(map[string]any{"ok": true, "status": "SUCCEEDED", "who": "B"})
	outPayB, _ := json.Marshal(map[string]any{"result": "from-B"})
	runOutB, _ := json.Marshal(map[string]any{"source": "a2a.inbound", "who": "B"})
	if err := repo.FencedInboundTerminal(ctx, a2agateway.FencedTerminalInput{
		WorkspaceID: fx.workspaceID, TaskID: task.ID, RunID: runID,
		Owner: leaseB.Owner, Token: leaseB.Token, Generation: leaseB.Generation,
		TaskStatus: "SUCCEEDED", RunStatus: "SUCCEEDED", ExpectedRunStatus: "RUNNING",
		RunOutputSummary: runOutB,
		DelegationID:     del.ID, StepID: stepID,
		DelStatus: "SUCCEEDED", DelOutputSummary: outSumB, DelOutputPayload: outPayB,
	}); err != nil {
		t.Fatalf("owner B FencedInboundTerminal: %v", err)
	}

	// Consistency: run/task/delegation/step terminal SUCCEEDED from B only.
	var runSt, taskSt, delSt, stepSt string
	var taskGen int64
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, runID).Scan(&runSt)
	_ = db.QueryRow(`SELECT status, execute_generation FROM agent_a2a_inbound_tasks WHERE id=$1`, task.ID).Scan(&taskSt, &taskGen)
	_ = db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=$1`, del.ID).Scan(&delSt)
	_ = db.QueryRow(`SELECT status FROM agent_run_steps WHERE id=$1`, stepID).Scan(&stepSt)
	if runSt != "SUCCEEDED" || taskSt != "SUCCEEDED" || delSt != "SUCCEEDED" || stepSt != "SUCCEEDED" {
		t.Fatalf("terminals: run=%s task=%s del=%s step=%s", runSt, taskSt, delSt, stepSt)
	}
	if taskGen != leaseB.Generation {
		t.Fatalf("task generation=%d want B=%d", taskGen, leaseB.Generation)
	}
	// A still cannot write after B terminalized.
	if err := repo.FencedInboundTerminal(ctx, a2agateway.FencedTerminalInput{
		WorkspaceID: fx.workspaceID, TaskID: task.ID, RunID: runID,
		Owner: leaseA.Owner, Token: leaseA.Token, Generation: leaseA.Generation,
		TaskStatus: "FAILED", RunStatus: "FAILED", ExpectedRunStatus: "SUCCEEDED",
		RunOutputSummary: json.RawMessage(`{"who":"A-late"}`),
		DelegationID:     del.ID, StepID: stepID,
		DelStatus: "FAILED", DelOutputSummary: json.RawMessage(`{}`), DelOutputPayload: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("A must not rewrite terminals after B")
	}
	var who string
	_ = db.QueryRow(`SELECT output_summary->>'who' FROM agent_runs WHERE id=$1`, runID).Scan(&who)
	if who != "B" {
		t.Fatalf("run output who=%q want B", who)
	}
}

// TestFencedTransitionAgentRun_AtomicExists rejects stale generation without TOCTOU.
func TestFencedTransitionAgentRun_AtomicExists(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	runRepo, _ := execution.NewRunRepository(db)
	expID := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO agent_a2a_exposures(
			id, workspace_id, agent_id, public_name, public_description,
			enabled, auth_mode, version, created_by, updated_by
		) VALUES ($1,$2,$3,'pub','d',true,'AGENT_ACCESS',1,$4,$4)
	`, expID, fx.workspaceID, fx.agentA, fx.ownerID); err != nil {
		t.Fatal(err)
	}
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
	}
	req := a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "x",
		ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
	}
	runID, err := runner.PrepareRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := repo.ClaimInboundTask(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "ft-k", ExternalTaskID: "ft-t", RunID: runID, Status: "RUNNING",
	})
	if err != nil {
		t.Fatal(err)
	}
	l1, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "o1", 200*time.Millisecond)
	if err != nil || !l1.Owned {
		t.Fatal(err)
	}
	// Force expire + reclaim.
	if _, err := db.Exec(`UPDATE agent_a2a_inbound_tasks SET execute_lease_until=NOW()-interval '1 second' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	l2, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "o2", time.Minute)
	if err != nil || !l2.Owned {
		t.Fatal(err)
	}
	// Old owner atomic transition fails (EXISTS fence).
	if err := repo.FencedTransitionAgentRun(ctx, fx.workspaceID, runID, task.ID,
		l1.Owner, l1.Token, l1.Generation, "RUNNING", 0, "SUCCEEDED", json.RawMessage(`{}`), ""); err == nil {
		t.Fatal("stale FencedTransitionAgentRun must fail")
	}
	// New owner succeeds.
	if err := repo.FencedTransitionAgentRun(ctx, fx.workspaceID, runID, task.ID,
		l2.Owner, l2.Token, l2.Generation, "RUNNING", 0, "SUCCEEDED", json.RawMessage(`{"ok":true}`), ""); err != nil {
		t.Fatal(err)
	}
}

func TestRenewRejectsExpiredLease(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
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
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
	}
	req := a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "x",
		ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
	}
	runID, _ := runner.PrepareRun(ctx, req)
	task, _, err := repo.ClaimInboundTask(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "exp-k", ExternalTaskID: "exp-t", RunID: runID, Status: "RUNNING",
	})
	if err != nil {
		t.Fatal(err)
	}
	l1, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "o", 200*time.Millisecond)
	if err != nil || !l1.Owned {
		t.Fatal(err)
	}
	// Force expiry.
	if _, err := db.Exec(`UPDATE agent_a2a_inbound_tasks SET execute_lease_until=NOW()-interval '1 second' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.RenewInboundExecutionLease(ctx, fx.workspaceID, task.ID, l1.Owner, l1.Token, time.Minute); err == nil {
		t.Fatal("expired owner must not renew")
	}
	if err := repo.MarkInboundExecutionFinishedGen(ctx, fx.workspaceID, task.ID,
		agentdelegation.StatusSucceeded, l1.Owner, l1.Token, l1.Generation); err == nil {
		t.Fatal("expired owner must not mark finished")
	}
}
