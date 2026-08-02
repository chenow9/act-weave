package a2agateway_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

// TestClaim_MaxOpenConns1_NoDeadlock: prepare writes agent_run on the claim TX,
// so a single-connection pool cannot deadlock under the claim advisory lock.
func TestClaim_MaxOpenConns1_NoDeadlock(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	fx := seedA2AAuditFixture(t, db)
	repo, err := a2agateway.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	runRepo, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
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
	const extKey = "ctx-max1|msg-max1"
	bodyHash := a2agateway.RequestBodyHash("same-body")

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			taskID := "task-max1-" + uuid.Must(uuid.NewV7()).String()
			freeze, ferr := runner.MaterializeFreeze(ctx, a2agateway.InboundRunRequest{
				WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "same-body",
				ExternalTaskID: taskID, ExternalContext: "ctx-max1", ExternalMessage: "msg-max1",
				ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
			})
			if ferr != nil {
				errs <- ferr
				return
			}
			req := a2agateway.InboundRunRequest{
				WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "same-body",
				ExternalTaskID: taskID, ExternalContext: "ctx-max1", ExternalMessage: "msg-max1",
				ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
			}
			_, _, err := repo.ClaimInboundTaskWithPrepare(ctx, a2agateway.InboundTask{
				WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
				ActorType: "USER", ActorID: fx.ownerID,
				ExternalKey: extKey, ExternalTaskID: taskID,
				ExternalContextID: "ctx-max1", ExternalMessageID: "msg-max1",
				RequestHash: bodyHash, Status: "RUNNING",
			}, func(c context.Context, tx *sql.Tx) (string, error) {
				return runner.PrepareRunInTx(c, tx, req, freeze)
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("deadlock: claim under MaxOpenConns=1 timed out")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("claim err: %v", err)
		}
	}

	var nTasks, nInboundRuns int
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 AND exposure_id=$2`,
		fx.workspaceID, expID).Scan(&nTasks)
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE workspace_id=$1 AND trigger_type='A2A_INBOUND'`,
		fx.workspaceID).Scan(&nInboundRuns)
	if nTasks != 1 || nInboundRuns != 1 {
		t.Fatalf("tasks=%d inbound_runs=%d want 1 each", nTasks, nInboundRuns)
	}
}

// TestClaim_AliasConflictAfterPrepare_NoOrphanRun: prepare inserts agent_run on the
// claim TX; alias conflict rolls back so no visible orphan run remains.
func TestClaim_AliasConflictAfterPrepare_NoOrphanRun(t *testing.T) {
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

	// First authority owns external task id "shared-task-alias".
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
	}
	req1 := a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "first",
		ExternalTaskID: "shared-task-alias", ExternalContext: "c1", ExternalMessage: "m1",
		ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
	}
	freeze1, err := runner.MaterializeFreeze(ctx, req1)
	if err != nil {
		t.Fatal(err)
	}
	task1, _, err := repo.ClaimInboundTaskWithPrepare(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "c1|m1", ExternalTaskID: "shared-task-alias",
		ExternalContextID: "c1", ExternalMessageID: "m1",
		RequestHash: a2agateway.RequestBodyHash("first"), Status: "RUNNING",
	}, func(c context.Context, tx *sql.Tx) (string, error) {
		return runner.PrepareRunInTx(c, tx, req1, freeze1)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Second claim with different external key but same ExternalTaskID → alias conflict
	// after prepare inserts a candidate run on the TX; rollback must drop that run.
	beforeRuns := countA2AInboundRuns(t, db, fx.workspaceID)
	req2 := a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "second",
		ExternalTaskID: "shared-task-alias", ExternalContext: "c2", ExternalMessage: "m2",
		ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
	}
	freeze2, err := runner.MaterializeFreeze(ctx, req2)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = repo.ClaimInboundTaskWithPrepare(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "c2|m2", ExternalTaskID: "shared-task-alias",
		ExternalContextID: "c2", ExternalMessageID: "m2",
		RequestHash: a2agateway.RequestBodyHash("second"), Status: "RUNNING",
	}, func(c context.Context, tx *sql.Tx) (string, error) {
		return runner.PrepareRunInTx(c, tx, req2, freeze2)
	})
	if err == nil {
		t.Fatal("expected alias conflict after prepare")
	}
	afterRuns := countA2AInboundRuns(t, db, fx.workspaceID)
	if afterRuns != beforeRuns {
		t.Fatalf("orphan agent_run after alias conflict: before=%d after=%d (authority=%s)",
			beforeRuns, afterRuns, task1.RunID)
	}
	// Only the first authority mapping remains.
	var nTasks int
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_a2a_inbound_tasks WHERE workspace_id=$1`, fx.workspaceID).Scan(&nTasks)
	if nTasks != 1 {
		t.Fatalf("tasks=%d want 1", nTasks)
	}
}

// TestClaim_TaskInsertFailureAfterPrepare_NoOrphanRun injects a post-prepare failure
// via an invalid exposure FK on the inbound task insert (run was already written on TX).
func TestClaim_TaskInsertFailureAfterPrepare_NoOrphanRun(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	runRepo, _ := execution.NewRunRepository(db)

	// No exposure row — inbound task INSERT fails FK after prepare on same TX.
	missingExp := uuid.Must(uuid.NewV7()).String()
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
	}
	beforeRuns := countA2AInboundRuns(t, db, fx.workspaceID)
	req := a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "x",
		ExternalTaskID: "t-fk", ExternalContext: "c", ExternalMessage: "m",
		ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
	}
	freeze, err := runner.MaterializeFreeze(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = repo.ClaimInboundTaskWithPrepare(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: missingExp, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "c|m", ExternalTaskID: "t-fk",
		ExternalContextID: "c", ExternalMessageID: "m",
		RequestHash: a2agateway.RequestBodyHash("x"), Status: "RUNNING",
	}, func(c context.Context, tx *sql.Tx) (string, error) {
		return runner.PrepareRunInTx(c, tx, req, freeze)
	})
	if err == nil {
		t.Fatal("expected task insert failure")
	}
	afterRuns := countA2AInboundRuns(t, db, fx.workspaceID)
	if afterRuns != beforeRuns {
		t.Fatalf("orphan agent_run after task insert fail: before=%d after=%d", beforeRuns, afterRuns)
	}
}

func countA2AInboundRuns(t *testing.T, db *sql.DB, workspaceID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE workspace_id=$1 AND trigger_type='A2A_INBOUND'`,
		workspaceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
