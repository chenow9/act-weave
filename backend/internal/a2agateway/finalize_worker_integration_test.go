package a2agateway_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

// Process-restart recovery: outbox row survives "crash"; new worker DrainOnce finalizes.
func TestFinalizeWorker_RestartRecovery_FinalizesDelegation(t *testing.T) {
	h := dbtest.New(t)
	v := h.MigrateToLatest(t)
	if !v.Applied || v.Number < 13 || v.Dirty {
		t.Fatalf("migration=%+v", v)
	}
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)

	repo, err := a2agateway.NewRepository(db)
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

	// Prewrite RUNNING delegation + step (parent run RUNNING).
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	target := fx.agentB
	del, _, err := audit.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: fx.workspaceID, ParentRunID: fx.parentRunID,
		CallerAgentID: fx.agentA, TargetAgentID: &target,
		Mode: agentdelegation.ModeTask, Protocol: agentdelegation.ProtocolInternal,
		Origin: agentdelegation.OriginInternal, Depth: 1, BindingVersion: 1,
		ToolCallID: "tc-outbox", IdempotencyKey: fx.parentRunID + ":outbox:1:b",
		InputSummary: json.RawMessage(`{"callableName":"call_b"}`),
		InputPayload: json.RawMessage(`{"request":"x"}`),
		StepID:       stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatalf("prewrite: %v", err)
	}
	if del.Status != agentdelegation.StatusRunning {
		t.Fatalf("status=%s", del.Status)
	}

	// Simulate retry exhaustion → enqueue outbox (worker not yet started).
	fin := agentdelegation.FinalizeDelegationInput{
		WorkspaceID: fx.workspaceID, DelegationID: delID, StepID: stepID,
		Status:        agentdelegation.StatusSucceeded,
		OutputSummary: json.RawMessage(`{"ok":true}`),
		OutputPayload: json.RawMessage(`{"result":"recovered"}`),
	}
	payload, _ := json.Marshal(fin)
	if err := repo.EnqueueFinalizeOutbox(ctx, fx.workspaceID, delID, stepID, payload); err != nil {
		t.Fatal(err)
	}

	// "Process restart": new worker claims + drains.
	w1, err := a2agateway.NewFinalizeWorker(repo, audit, nil)
	if err != nil {
		t.Fatal(err)
	}
	w1.Interval = time.Hour // no auto loop noise
	w1.DrainOnce(ctx)

	// Assert terminal + outbox gone.
	got, err := delRepo.GetByIdempotency(ctx, fx.workspaceID, fx.parentRunID+":outbox:1:b")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != agentdelegation.StatusSucceeded {
		t.Fatalf("after drain status=%s want SUCCEEDED", got.Status)
	}
	var n int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM agent_run_delegation_finalize_outbox
		WHERE workspace_id=$1 AND delegation_id=$2
	`, fx.workspaceID, delID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("outbox rows left=%d", n)
	}

	// Second worker Start/Stop lifecycle smoke.
	w2, err := a2agateway.NewFinalizeWorker(repo, audit, nil)
	if err != nil {
		t.Fatal(err)
	}
	w2.Interval = 50 * time.Millisecond
	w2.Start(ctx)
	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	w2.Stop(stopCtx)
}

// Flaky finalize then recover: Nack increments attempts; eventual success deletes.
func TestFinalizeWorker_NackThenSucceed(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	delRepo, _ := agentdelegation.NewRepository(db)
	audit, _ := agentdelegation.NewService(delRepo)

	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	target := fx.agentB
	_, _, err := audit.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: fx.workspaceID, ParentRunID: fx.parentRunID,
		CallerAgentID: fx.agentA, TargetAgentID: &target,
		Mode: agentdelegation.ModeInline, Protocol: agentdelegation.ProtocolInternal,
		Origin: agentdelegation.OriginInternal, Depth: 1, BindingVersion: 1,
		ToolCallID: "tc-nack", IdempotencyKey: fx.parentRunID + ":nack:1:b",
		InputSummary: json.RawMessage(`{}`), InputPayload: json.RawMessage(`{}`),
		StepID: stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatal(err)
	}

	var fails atomic.Int64
	fails.Store(2)
	flaky := &countingAudit{inner: audit, failsLeft: &fails}
	fin := agentdelegation.FinalizeDelegationInput{
		WorkspaceID: fx.workspaceID, DelegationID: delID, StepID: stepID,
		Status:        agentdelegation.StatusSucceeded,
		OutputSummary: json.RawMessage(`{"ok":true}`),
		OutputPayload: json.RawMessage(`{"result":"ok"}`),
	}
	payload, _ := json.Marshal(fin)
	_ = repo.EnqueueFinalizeOutbox(ctx, fx.workspaceID, delID, stepID, payload)

	w, _ := a2agateway.NewFinalizeWorker(repo, flaky, nil)
	// First two claims fail; need next_attempt_at ready — Nack uses ms backoff.
	for i := 0; i < 8; i++ {
		w.DrainOnce(ctx)
		time.Sleep(30 * time.Millisecond)
		got, gerr := delRepo.GetByIdempotency(ctx, fx.workspaceID, fx.parentRunID+":nack:1:b")
		if gerr == nil && got.Status == agentdelegation.StatusSucceeded {
			return
		}
	}
	t.Fatal("expected eventual finalize success after nacks")
}

type countingAudit struct {
	inner     agentdelegation.AuditWriter
	failsLeft *atomic.Int64
}

func (c *countingAudit) CreateDelegationAndStep(ctx context.Context, in agentdelegation.CreateDelegationInput) (agentdelegation.Delegation, bool, error) {
	return c.inner.CreateDelegationAndStep(ctx, in)
}
func (c *countingAudit) SetChildRunID(ctx context.Context, w, d, child string) error {
	return c.inner.SetChildRunID(ctx, w, d, child)
}
func (c *countingAudit) FinalizeDelegation(ctx context.Context, in agentdelegation.FinalizeDelegationInput) (agentdelegation.Delegation, error) {
	if c.failsLeft != nil && c.failsLeft.Add(-1) >= 0 {
		return agentdelegation.Delegation{}, agentdelegation.ErrNotFound
	}
	return c.inner.FinalizeDelegation(ctx, in)
}
func (c *countingAudit) RecordDispatchAttempt(ctx context.Context, w, d string) error {
	return c.inner.RecordDispatchAttempt(ctx, w, d)
}
func (c *countingAudit) AccumulateModelTokens(ctx context.Context, w, d string, u agentdelegation.TokenUsage) error {
	return c.inner.AccumulateModelTokens(ctx, w, d, u)
}

type a2aAuditFixture struct {
	ownerID, workspaceID string
	modelID              string
	agentA, agentB       string
	parentRunID          string
	sessionID            string
}

func seedA2AAuditFixture(t *testing.T, db *sql.DB) a2aAuditFixture {
	t.Helper()
	fx := a2aAuditFixture{
		ownerID: uuid.Must(uuid.NewV7()).String(), workspaceID: uuid.Must(uuid.NewV7()).String(),
		modelID: uuid.Must(uuid.NewV7()).String(),
		agentA:  uuid.Must(uuid.NewV7()).String(), agentB: uuid.Must(uuid.NewV7()).String(),
		parentRunID: uuid.Must(uuid.NewV7()).String(), sessionID: uuid.Must(uuid.NewV7()).String(),
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	exec(`INSERT INTO users(id,username,display_name) VALUES($1,'a2a.owner','A2A')`, fx.ownerID)
	exec(`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,$2,'A2A','SANDBOX',$3,$3,$3)`, fx.workspaceID, "a2a-"+fx.workspaceID[:8], fx.ownerID)
	exec(`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'m','openai','https://example.test','m',$3,$3)`, fx.modelID, fx.workspaceID, fx.ownerID)
	exec(`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES
		($1,$3,'A',$4,$5,$5),($2,$3,'B',$4,$5,$5)`,
		fx.agentA, fx.agentB, fx.workspaceID, fx.modelID, fx.ownerID)
	exec(`INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		VALUES($1,$2,$3,'s',$4)`, fx.sessionID, fx.workspaceID, fx.agentA, fx.ownerID)
	exec(`INSERT INTO agent_runs(
		id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
		triggered_by_id,trace_id,model_snapshot,capability_snapshot,context_policy_snapshot,
		authorization_snapshot,input_summary,agent_graph_snapshot
	) VALUES (
		$1,$2,$3,$4,'RUNNING','CHAT','USER',$5,$6,
		'{"modelName":"m"}'::jsonb,'{"releases":[]}'::jsonb,'{}'::jsonb,
		'{}'::jsonb,'{}'::jsonb,'{}'::jsonb
	)`, fx.parentRunID, fx.workspaceID, fx.sessionID, fx.agentA, fx.ownerID, "tr-"+fx.parentRunID[:8])
	return fx
}
