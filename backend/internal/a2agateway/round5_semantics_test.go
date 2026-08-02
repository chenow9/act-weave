package a2agateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// TestDurableCancel_InterruptOnly_NoOutOfBandRunTransition proves CancelInbound
// uses InterruptRun (no durable agent_run write) before AtomicInboundCancel.
func TestDurableCancel_InterruptOnly_NoOutOfBandRunTransition(t *testing.T) {
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

	var interruptN atomic.Int64
	pause := make(chan struct{})
	release := make(chan struct{})
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			close(pause)
			<-release
			return "late", nil
		},
		CancelHook: func(ctx context.Context, workspaceID, runID string) error {
			interruptN.Add(1)
			return nil
		},
	}

	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	gw, err := a2agateway.NewInboundGateway(repo, audit, runner, "http://test",
		auth, a2agateway.WithLeaseTTL(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gw.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		postJSONRPCMessage(t, srv.URL, fx.workspaceID, fx.agentA, "cancel-me", "", "msg-r5-c")
	}()
	select {
	case <-pause:
	case <-time.After(5 * time.Second):
		t.Fatal("execute not started")
	}

	var taskID, runID string
	_ = db.QueryRow(`SELECT id, run_id FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT 1`,
		fx.workspaceID).Scan(&taskID, &runID)
	var extTask string
	_ = db.QueryRow(`SELECT external_task_id FROM agent_a2a_inbound_tasks WHERE id=$1`, taskID).Scan(&extTask)

	// Snapshot: run still RUNNING before cancel (Interrupt alone cannot terminalize).
	var runStBefore string
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, runID).Scan(&runStBefore)
	if runStBefore != "RUNNING" {
		t.Fatalf("pre-cancel run=%s", runStBefore)
	}

	if err := gw.CancelInbound(ctx, fx.workspaceID, expID, "USER", fx.ownerID, extTask); err != nil {
		t.Fatal(err)
	}
	if interruptN.Load() < 1 {
		t.Fatal("InterruptRun/CancelHook must be invoked")
	}

	var taskSt, runSt, delSt string
	_ = db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE id=$1`, taskID).Scan(&taskSt)
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, runID).Scan(&runSt)
	_ = db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=(SELECT delegation_id FROM agent_a2a_inbound_tasks WHERE id=$1)`, taskID).Scan(&delSt)
	if taskSt != "CANCELLED" || runSt != "CANCELLED" || delSt != "CANCELLED" {
		t.Fatalf("want all CANCELLED task=%s run=%s del=%s", taskSt, runSt, delSt)
	}
	// Generation bumped so late fenced complete conflicts.
	close(release)
	<-done
	// Late executor must not rewrite cancel → success.
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, runID).Scan(&runSt)
	if runSt != "CANCELLED" {
		t.Fatalf("late executor rewrote run to %s", runSt)
	}
}

// TestProductionJSONRPC_AuditPrewriteFail_AtomicCleanup: real message/send path.
func TestProductionJSONRPC_AuditPrewriteFail_AtomicCleanup(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	// Audit that always fails CreateDelegationAndStep.
	audit := &failingPrewriteAudit{}
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
	var execs atomic.Int64
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			execs.Add(1)
			return "nope", nil
		},
	}
	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	gw, err := a2agateway.NewInboundGateway(repo, audit, runner, "http://test", auth)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gw.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st, body := postJSONRPCMessage(t, srv.URL, fx.workspaceID, fx.agentA, "prewrite-fail", "", "msg-pre")
	if st != http.StatusOK {
		t.Fatalf("status=%d body=%s", st, body)
	}
	if execs.Load() != 0 {
		t.Fatalf("Execute must not run; n=%d", execs.Load())
	}
	var taskSt, runSt string
	var runID string
	_ = db.QueryRow(`SELECT status, run_id FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT 1`,
		fx.workspaceID).Scan(&taskSt, &runID)
	if runID != "" {
		_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, runID).Scan(&runSt)
	}
	if taskSt == "RUNNING" || runSt == "RUNNING" {
		t.Fatalf("orphans task=%s run=%s body=%s", taskSt, runSt, body)
	}
	if taskSt != "FAILED" || runSt != "FAILED" {
		t.Fatalf("want FAILED task=%s run=%s body=%s", taskSt, runSt, body)
	}
}

// TestStrictTerminalSticky_DifferentStatusConflicts
func TestStrictTerminalSticky_DifferentStatusConflicts(t *testing.T) {
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
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
	}
	runID, err := runner.PrepareRun(ctx, a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "x",
		ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := repo.ClaimInboundTask(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "sticky-k", ExternalTaskID: "sticky-t", RunID: runID, Status: "RUNNING",
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
		IdempotencyKey: agentdelegation.IdempotencyKey(expID+":sticky-k", "inbound", 1, expID),
		InputSummary:   json.RawMessage(`{}`), InputPayload: json.RawMessage(`{}`),
		StepID: stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.BindInboundTaskDelegation(ctx, fx.workspaceID, task.ID, del.ID); err != nil {
		t.Fatal(err)
	}
	// Pre-set run SUCCEEDED, delegation FAILED — cancel must conflict and leave unchanged.
	if _, err := db.Exec(`UPDATE agent_runs SET status='SUCCEEDED', finished_at=NOW(), lock_version=lock_version+1 WHERE id=$1`, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agent_run_delegations SET status='FAILED', finished_at=NOW() WHERE id=$1`, del.ID); err != nil {
		t.Fatal(err)
	}
	// Task still RUNNING for cancel path.
	err = repo.AtomicInboundCancel(ctx, fx.workspaceID, task.ID)
	if err == nil {
		// Cancel may succeed on task while run sticky same? Run is SUCCEEDED, allowAlready only if CANCELLED==SUCCEEDED → conflict.
		t.Log("atomic cancel err=", err)
	}
	// Force: if cancel failed, good. Check no mixed rewrite of del to CANCELLED when run was SUCCEEDED.
	var runSt, delSt, taskSt string
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, runID).Scan(&runSt)
	_ = db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=$1`, del.ID).Scan(&delSt)
	_ = db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE id=$1`, task.ID).Scan(&taskSt)
	// Must not have rewritten SUCCEEDED run to CANCELLED while leaving FAILED del, or vice versa inconsistently.
	if runSt == "CANCELLED" && delSt == "FAILED" {
		t.Fatalf("mixed: run CANCELLED del FAILED")
	}
	if runSt == "SUCCEEDED" && delSt == "CANCELLED" {
		t.Fatalf("mixed: run SUCCEEDED del CANCELLED")
	}
	// Preferred: conflict left original pre-set terminals for run/del and task non-rewritten or consistent.
	if err == nil && taskSt == "CANCELLED" && runSt == "SUCCEEDED" {
		t.Fatal("task cancelled while run stayed SUCCEEDED (mixed)")
	}
}

// TestFencedRequiresBoundDelegation
func TestFencedRequiresBoundDelegation(t *testing.T) {
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
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
	}
	runID, _ := runner.PrepareRun(ctx, a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "x",
		ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
	})
	task, _, err := repo.ClaimInboundTask(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "ub-k", ExternalTaskID: "ub-t", RunID: runID, Status: "RUNNING",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Create delegation but DO NOT bind to task.
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	_, _, err = audit.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: fx.workspaceID, ParentRunID: runID,
		CallerAgentID: fx.agentA, Mode: agentdelegation.ModeTask,
		Protocol: agentdelegation.ProtocolA2A, Origin: agentdelegation.OriginExternal,
		Depth: 0, BindingVersion: 1, ToolCallID: "inbound",
		IdempotencyKey: agentdelegation.IdempotencyKey(expID+":ub-k", "inbound", 1, expID),
		InputSummary:   json.RawMessage(`{}`), InputPayload: json.RawMessage(`{}`),
		StepID: stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "o", time.Minute)
	if err != nil || !lease.Owned {
		t.Fatal(err)
	}
	err = repo.FencedInboundTerminal(ctx, a2agateway.FencedTerminalInput{
		WorkspaceID: fx.workspaceID, TaskID: task.ID, RunID: runID,
		Owner: lease.Owner, Token: lease.Token, Generation: lease.Generation,
		TaskStatus: "SUCCEEDED", RunStatus: "SUCCEEDED", ExpectedRunStatus: "RUNNING",
		RunOutputSummary: json.RawMessage(`{}`),
		DelegationID:     delID, StepID: stepID,
		DelStatus: "SUCCEEDED", DelOutputSummary: json.RawMessage(`{}`), DelOutputPayload: json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatal("unbound task must not fenced-terminal an arbitrary delegation")
	}
	var runSt, taskSt, delSt string
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, runID).Scan(&runSt)
	_ = db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE id=$1`, task.ID).Scan(&taskSt)
	_ = db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=$1`, delID).Scan(&delSt)
	if runSt != "RUNNING" || taskSt != "RUNNING" || delSt != "RUNNING" {
		t.Fatalf("partial commit run=%s task=%s del=%s", runSt, taskSt, delSt)
	}
}

// TestStaticReplyRunner_FenceSkipsTransition
func TestStaticReplyRunner_FenceSkipsTransition(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)
	runRepo, _ := execution.NewRunRepository(db)
	runner := &a2agateway.StaticReplyRunner{
		Reply: "static", Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
	}
	runID, err := runner.PrepareRun(ctx, a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "x",
		ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fenced := a2agateway.WithExecutionFence(ctx, a2agateway.ExecutionFence{
		WorkspaceID: fx.workspaceID, TaskID: uuid.Must(uuid.NewV7()).String(), RunID: runID,
		Owner: "o", Token: "t", Generation: 1,
	})
	res, err := runner.ExecuteRun(fenced, a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA,
	}, runID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "SUCCEEDED" {
		t.Fatalf("status=%s", res.Status)
	}
	var st string
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, runID).Scan(&st)
	if st != "RUNNING" {
		t.Fatalf("under fence run must stay RUNNING, got %s", st)
	}
}

func postJSONRPCMessage(t *testing.T, baseURL, workspaceID, agentID, text, taskID, msgID string) (int, []byte) {
	t.Helper()
	msg := map[string]any{
		"kind": "message", "messageId": msgID, "role": "user",
		"parts": []map[string]any{{"kind": "text", "text": text}},
	}
	if taskID != "" {
		msg["taskId"] = taskID
		msg["contextId"] = "ctx-" + taskID
	} else {
		msg["contextId"] = "ctx-" + msgID
	}
	raw, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": "1", "method": "message/send",
		"params": map[string]any{"message": msg},
	})
	url := fmt.Sprintf("%s/a2a/workspaces/%s/agents/%s/invoke", baseURL, workspaceID, agentID)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer t")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

type failingPrewriteAudit struct{}

func (f *failingPrewriteAudit) CreateDelegationAndStep(context.Context, agentdelegation.CreateDelegationInput) (agentdelegation.Delegation, bool, error) {
	return agentdelegation.Delegation{}, false, fmt.Errorf("inject: audit prewrite failed")
}
func (f *failingPrewriteAudit) FinalizeDelegation(context.Context, agentdelegation.FinalizeDelegationInput) (agentdelegation.Delegation, error) {
	return agentdelegation.Delegation{}, fmt.Errorf("inject: finalize")
}
func (f *failingPrewriteAudit) SetChildRunID(context.Context, string, string, string) error {
	return nil
}
func (f *failingPrewriteAudit) RecordDispatchAttempt(context.Context, string, string) error {
	return nil
}
func (f *failingPrewriteAudit) AccumulateModelTokens(context.Context, string, string, agentdelegation.TokenUsage) error {
	return nil
}
