package a2agateway_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

// Residual: short lease + blocking runner — heartbeat keeps ownership so second claim fails.
func TestInboundLease_HeartbeatPreventsDualOwnership(t *testing.T) {
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
	var execs atomic.Int64
	block := make(chan struct{})
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			execs.Add(1)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-block:
				return "done", nil
			case <-time.After(3 * time.Second):
				return "timeout-ok", nil
			}
		},
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
		ExternalKey: "hb-key", ExternalTaskID: "t-hb", RunID: runID, Status: "RUNNING",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Short lease (800ms); heartbeat every 200ms in production path uses lease/3.
	const lease = 800 * time.Millisecond
	l1, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "owner-a", lease)
	if err != nil || !l1.Owned {
		t.Fatalf("claim1: owned=%v err=%v", l1.Owned, err)
	}
	// Heartbeat goroutine (mirrors inbound executor).
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go func() {
		ticker := time.NewTicker(lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				_ = repo.RenewInboundExecutionLease(hbCtx, fx.workspaceID, task.ID, l1.Owner, l1.Token, lease)
			}
		}
	}()

	// Wait past original lease expiry while heartbeat keeps ownership.
	time.Sleep(lease + 400*time.Millisecond)
	l2, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "owner-b", lease)
	if err != nil {
		t.Fatal(err)
	}
	if l2.Owned {
		t.Fatal("second claim must not own while heartbeat renews lease")
	}

	// Stale token cannot finalize.
	if err := repo.MarkInboundExecutionFinished(ctx, fx.workspaceID, task.ID,
		agentdelegation.StatusSucceeded, "owner-b", "stale-token"); err == nil {
		t.Fatal("stale token mark must fail")
	}
	hbCancel()
	if err := repo.MarkInboundExecutionFinished(ctx, fx.workspaceID, task.ID,
		agentdelegation.StatusSucceeded, l1.Owner, l1.Token); err != nil {
		t.Fatalf("owner mark: %v", err)
	}
	close(block)
}

func TestCancelInbound_PropagatesErrorsAndIdempotentTerminal(t *testing.T) {
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
	runRepo, _ := execution.NewRunRepository(db)
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(context.Context, a2agateway.InboundRunRequest, string) (string, error) {
			return "ok", nil
		},
	}
	gw, err := a2agateway.NewInboundGateway(repo, audit, runner, "http://127.0.0.1", a2agateway.HeaderPresenceAuth{})
	if err != nil {
		t.Fatal(err)
	}
	// Missing task → error (not silent success).
	if err := gw.CancelInbound(ctx, fx.workspaceID, expID, "USER", fx.ownerID, "missing-task"); err == nil {
		t.Fatal("cancel missing task must return error")
	}

	req := a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "x",
		ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
		ExternalTaskID: "cancel-t", IdempotencyKey: "cancel-k",
	}
	runID, err := runner.PrepareRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := repo.ClaimInboundTask(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "cancel-k", ExternalTaskID: "cancel-t", RunID: runID, Status: "RUNNING",
	})
	if err != nil {
		t.Fatal(err)
	}
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	_, _, err = audit.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: fx.workspaceID, ParentRunID: runID,
		CallerAgentID: fx.agentA, Mode: agentdelegation.ModeTask,
		Protocol: agentdelegation.ProtocolA2A, Origin: agentdelegation.OriginExternal,
		Depth: 0, BindingVersion: 1, ToolCallID: "inbound",
		IdempotencyKey: agentdelegation.IdempotencyKey(expID+":cancel-k", "inbound", 1, expID),
		InputSummary:   []byte(`{}`), InputPayload: []byte(`{}`),
		StepID: stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = repo.BindInboundTaskDelegation(ctx, fx.workspaceID, task.ID, delID)

	if err := gw.CancelInbound(ctx, fx.workspaceID, expID, "USER", fx.ownerID, "cancel-t"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	var taskStatus string
	if err := db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE id=$1`, task.ID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if taskStatus != "CANCELLED" {
		t.Fatalf("task=%s", taskStatus)
	}
	// SUCCEEDED task must not flip to CANCELLED.
	if _, err := db.Exec(`UPDATE agent_a2a_inbound_tasks SET status='SUCCEEDED' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := gw.CancelInbound(ctx, fx.workspaceID, expID, "USER", fx.ownerID, "cancel-t"); err != nil {
		t.Fatalf("cancel terminal: %v", err)
	}
	if err := db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE id=$1`, task.ID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if taskStatus != "SUCCEEDED" {
		t.Fatalf("terminal rewrite to %s", taskStatus)
	}
}
