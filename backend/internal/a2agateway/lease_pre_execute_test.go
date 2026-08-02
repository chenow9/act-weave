package a2agateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

// TestProductionJSONRPC_LeaseLostBetweenAttemptAndExecute_NoExecute proves that
// after RecordDispatchAttempt, inboundExecutor re-asserts lease; if TTL elapsed
// and another owner reclaimed, ExecuteRun must not run for the stale owner.
func TestProductionJSONRPC_LeaseLostBetweenAttemptAndExecute_NoExecute(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	delRepo, _ := agentdelegation.NewRepository(db)
	base, _ := agentdelegation.NewService(delRepo)

	// Slow attempt record so we can expire+reclaim before execute assert.
	audit := &slowAttemptAudit{inner: base, delay: 400 * time.Millisecond}
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

	var execN atomic.Int64
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			execN.Add(1)
			return "should-not-run", nil
		},
	}
	const shortLease = 150 * time.Millisecond
	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	gw, err := a2agateway.NewInboundGateway(repo, audit, runner, "http://test",
		auth, a2agateway.WithLeaseTTL(shortLease))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gw.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Background: after attempt starts delaying, expire lease and reclaim as B.
	go func() {
		time.Sleep(200 * time.Millisecond)
		var taskID string
		// Wait for task row.
		for i := 0; i < 50; i++ {
			_ = db.QueryRow(`SELECT id FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT 1`,
				fx.workspaceID).Scan(&taskID)
			if taskID != "" {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if taskID == "" {
			return
		}
		_, _ = db.Exec(`UPDATE agent_a2a_inbound_tasks SET execute_lease_until=NOW()-interval '2 seconds' WHERE id=$1`, taskID)
		_, _ = repo.ClaimInboundExecution(context.Background(), fx.workspaceID, taskID, "reclaimer-b", time.Minute)
	}()

	st, body := postMessageSend(t, srv.URL, fx.workspaceID, fx.agentA, "race", "", "msg-lease-race")
	if st != 200 {
		t.Fatalf("status=%d body=%s", st, body)
	}
	if execN.Load() != 0 {
		t.Fatalf("stale owner must not ExecuteRun; n=%d body=%s", execN.Load(), body)
	}
}

type slowAttemptAudit struct {
	inner agentdelegation.AuditWriter
	delay time.Duration
}

func (s *slowAttemptAudit) CreateDelegationAndStep(ctx context.Context, in agentdelegation.CreateDelegationInput) (agentdelegation.Delegation, bool, error) {
	return s.inner.CreateDelegationAndStep(ctx, in)
}
func (s *slowAttemptAudit) FinalizeDelegation(ctx context.Context, in agentdelegation.FinalizeDelegationInput) (agentdelegation.Delegation, error) {
	return s.inner.FinalizeDelegation(ctx, in)
}
func (s *slowAttemptAudit) SetChildRunID(ctx context.Context, w, d, c string) error {
	return s.inner.SetChildRunID(ctx, w, d, c)
}
func (s *slowAttemptAudit) RecordDispatchAttempt(ctx context.Context, w, d string) error {
	time.Sleep(s.delay)
	return s.inner.RecordDispatchAttempt(ctx, w, d)
}
func (s *slowAttemptAudit) AccumulateModelTokens(ctx context.Context, w, d string, u agentdelegation.TokenUsage) error {
	return s.inner.AccumulateModelTokens(ctx, w, d, u)
}
