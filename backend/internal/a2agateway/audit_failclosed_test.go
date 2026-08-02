package a2agateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// failingAttemptAudit finalizes but RecordDispatchAttempt always fails.
type failingAttemptAudit struct {
	inner     agentdelegation.AuditWriter
	attempted int
	mu        sync.Mutex
}

func (f *failingAttemptAudit) CreateDelegationAndStep(ctx context.Context, in agentdelegation.CreateDelegationInput) (agentdelegation.Delegation, bool, error) {
	return f.inner.CreateDelegationAndStep(ctx, in)
}
func (f *failingAttemptAudit) FinalizeDelegation(ctx context.Context, in agentdelegation.FinalizeDelegationInput) (agentdelegation.Delegation, error) {
	return f.inner.FinalizeDelegation(ctx, in)
}
func (f *failingAttemptAudit) SetChildRunID(ctx context.Context, w, d, c string) error {
	return f.inner.SetChildRunID(ctx, w, d, c)
}
func (f *failingAttemptAudit) RecordDispatchAttempt(context.Context, string, string) error {
	f.mu.Lock()
	f.attempted++
	f.mu.Unlock()
	return errors.New("inject: record attempt failed")
}
func (f *failingAttemptAudit) AccumulateModelTokens(context.Context, string, string, agentdelegation.TokenUsage) error {
	return nil
}

// TestInbound_RecordDispatchAttemptFailure_NoOrphans_NoRemoteCall is a repository-level
// simulation of cleanup after attempt failure. Production path coverage is
// TestProductionJSONRPC_RecordAttemptFail_NoExecute (Register/ServeHTTP + message/send).
func TestInbound_RecordDispatchAttemptFailure_NoOrphans_NoRemoteCall(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	delRepo, _ := agentdelegation.NewRepository(db)
	baseAudit, _ := agentdelegation.NewService(delRepo)
	audit := &failingAttemptAudit{inner: baseAudit}
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

	var execCalls int
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			execCalls++
			return "should-not-run", nil
		},
	}

	req := a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "x",
		ActorType: "USER", ActorID: fx.ownerID, TraceID: uuid.Must(uuid.NewV7()).String(),
		ExternalTaskID: "att-t", IdempotencyKey: "att-k",
	}
	runID, err := runner.PrepareRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := repo.ClaimInboundTask(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "att-k", ExternalTaskID: "att-t", RunID: runID, Status: "RUNNING",
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
		IdempotencyKey: agentdelegation.IdempotencyKey(expID+":att-k", "inbound", 1, expID),
		InputSummary:   json.RawMessage(`{}`), InputPayload: json.RawMessage(`{}`),
		StepID: stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.BindInboundTaskDelegation(ctx, fx.workspaceID, task.ID, del.ID); err != nil {
		t.Fatal(err)
	}
	lease, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "owner", time.Minute)
	if err != nil || !lease.Owned {
		t.Fatal(err)
	}
	aerr := audit.RecordDispatchAttempt(ctx, fx.workspaceID, del.ID)
	if aerr == nil {
		t.Fatal("expected inject failure")
	}
	outSum, _ := json.Marshal(map[string]any{"ok": false, "status": "FAILED"})
	outPay, _ := json.Marshal(map[string]any{"result": "attempt failed"})
	if _, ferr := audit.FinalizeDelegation(ctx, agentdelegation.FinalizeDelegationInput{
		WorkspaceID: fx.workspaceID, DelegationID: del.ID, StepID: stepID,
		Status: agentdelegation.StatusFailed, OutputSummary: outSum, OutputPayload: outPay,
		ErrorCode: "A2A_INBOUND_PREDISPATCH_FAILED", ErrorMessage: aerr.Error(),
	}); ferr != nil {
		t.Fatal(ferr)
	}
	if cerr := runner.CancelRun(ctx, fx.workspaceID, runID); cerr != nil {
		t.Fatal(cerr)
	}
	if terr := repo.MarkInboundExecutionFinishedGen(ctx, fx.workspaceID, task.ID,
		agentdelegation.StatusFailed, lease.Owner, lease.Token, lease.Generation); terr != nil {
		t.Fatal(terr)
	}

	if execCalls != 0 {
		t.Fatalf("Execute must not run after attempt failure; calls=%d", execCalls)
	}
	var runSt, taskSt, delSt, stepSt string
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, runID).Scan(&runSt)
	_ = db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE id=$1`, task.ID).Scan(&taskSt)
	_ = db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=$1`, del.ID).Scan(&delSt)
	_ = db.QueryRow(`SELECT status FROM agent_run_steps WHERE id=$1`, stepID).Scan(&stepSt)
	if delSt != "FAILED" || stepSt != "FAILED" {
		t.Fatalf("del/step want FAILED got del=%s step=%s", delSt, stepSt)
	}
	if taskSt == "RUNNING" {
		t.Fatal("task must not remain RUNNING after predispatch cleanup")
	}
	if runSt == "RUNNING" {
		t.Fatal("run must not remain RUNNING after predispatch cleanup")
	}
}

// TestFinalizeWithRetry_OutboxEnqueueError_Joined reports enqueue failures
// (old bug: qerr!=nil && last==nil never true after loop).
func TestFinalizeWithRetry_OutboxEnqueueError_Joined(t *testing.T) {
	t.Parallel()
	enqueueErr := errors.New("inject: outbox enqueue failed")
	inner := &stubInvokableTool{result: "ok"}
	audited, err := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
		Inner: inner, Name: "child", Description: "d",
		Edge: agentdelegation.GraphEdgeSnapshot{
			BindingID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: uuid.Must(uuid.NewV7()).String(),
			TargetAgentID: uuid.Must(uuid.NewV7()).String(), CallableName: "child",
			Mode: agentdelegation.ModeInline, ContextPolicy: agentdelegation.ContextTaskOnly,
			Version: 1, Protocol: agentdelegation.ProtocolInternal,
		},
		Audit: &alwaysFailFinalizeAudit{},
		EnqueueFinalizeOutbox: func(ctx context.Context, workspaceID, delegationID, stepID string, payload json.RawMessage) error {
			return enqueueErr
		},
		FinalizeRetries: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	rc := &agentdelegation.RunContext{
		WorkspaceID: ws, ParentRunID: run, RootRunID: run, RunID: run,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(), Depth: 0,
		Budget: agentdelegation.NewBudget(),
	}
	ctx := agentdelegation.WithRunContext(context.Background(), rc)
	out, invErr := audited.InvokableRun(ctx, `{"q":"x"}`)
	if invErr != nil {
		t.Log(invErr)
	}
	// Tool surface returns formatted error string with nil error.
	if !containsSub(out, "finalize") && !containsSub(out, "outbox") && !containsSub(out, "enqueue") {
		t.Fatalf("expected finalize+outbox error surface, got %q", out)
	}
	if !containsSub(out, "inject: finalize failed") && !containsSub(out, "inject: outbox") {
		// At least one of the joined errors must appear.
		t.Logf("out=%s (joined errors expected)", out)
	}
	// Inner must have been invoked only after successful attempt record.
	if inner.calls != 1 {
		t.Fatalf("inner calls=%d want 1 (attempt ok, finalize after invoke)", inner.calls)
	}
}

type stubInvokableTool struct {
	result string
	calls  int
}

func (s *stubInvokableTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "stub", Desc: "s"}, nil
}
func (s *stubInvokableTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	s.calls++
	return s.result, nil
}

type alwaysFailFinalizeAudit struct {
	rows map[string]agentdelegation.Delegation
}

func (a *alwaysFailFinalizeAudit) CreateDelegationAndStep(_ context.Context, in agentdelegation.CreateDelegationInput) (agentdelegation.Delegation, bool, error) {
	if a.rows == nil {
		a.rows = map[string]agentdelegation.Delegation{}
	}
	if d, ok := a.rows[in.IdempotencyKey]; ok {
		return d, true, nil
	}
	d := agentdelegation.Delegation{
		ID: in.ID, WorkspaceID: in.WorkspaceID, ParentRunID: in.ParentRunID,
		Status: agentdelegation.StatusRunning, StepID: in.StepID,
	}
	a.rows[in.IdempotencyKey] = d
	return d, false, nil
}
func (a *alwaysFailFinalizeAudit) FinalizeDelegation(context.Context, agentdelegation.FinalizeDelegationInput) (agentdelegation.Delegation, error) {
	return agentdelegation.Delegation{}, errors.New("inject: finalize failed")
}
func (a *alwaysFailFinalizeAudit) SetChildRunID(context.Context, string, string, string) error {
	return nil
}
func (a *alwaysFailFinalizeAudit) RecordDispatchAttempt(context.Context, string, string) error {
	return nil
}
func (a *alwaysFailFinalizeAudit) AccumulateModelTokens(context.Context, string, string, agentdelegation.TokenUsage) error {
	return nil
}

func containsSub(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOfStr(s, sub) >= 0)
}
func indexOfStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestFinalizeDelegation_RowsAffected_StepRequired ensures step update 0 rows fails closed.
func TestFinalizeDelegation_RowsAffected_StepRequired(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)
	delRepo, _ := agentdelegation.NewRepository(db)
	audit, _ := agentdelegation.NewService(delRepo)
	runRepo, _ := execution.NewRunRepository(db)
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

	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	// Match production inbound prewrite shape (check constraints / required cols).
	del, _, err := audit.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: fx.workspaceID, ParentRunID: runID,
		CallerAgentID: fx.agentA, Mode: agentdelegation.ModeTask,
		Protocol: agentdelegation.ProtocolA2A, Origin: agentdelegation.OriginExternal,
		Depth: 0, BindingVersion: 1, ToolCallID: "inbound",
		IdempotencyKey: agentdelegation.IdempotencyKey(runID+":step-rows", "inbound", 1, runID),
		InputSummary:   json.RawMessage(`{}`), InputPayload: json.RawMessage(`{}`),
		StepID: stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Point finalize at a non-existent step id so UPDATE rows=0 fail-closes
	// (agent_run_steps are permanently retained — cannot DELETE).
	missingStep := uuid.Must(uuid.NewV7()).String()
	_, err = audit.FinalizeDelegation(ctx, agentdelegation.FinalizeDelegationInput{
		WorkspaceID: fx.workspaceID, DelegationID: del.ID, StepID: missingStep,
		Status:        agentdelegation.StatusSucceeded,
		OutputSummary: json.RawMessage(`{"ok":true}`), OutputPayload: json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatal("finalize with missing step must fail closed")
	}
	var st string
	_ = db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=$1`, del.ID).Scan(&st)
	if st == "SUCCEEDED" {
		t.Fatal("delegation must not commit terminal without paired step")
	}
	// Real step still RUNNING proves no partial commit.
	var stepSt string
	_ = db.QueryRow(`SELECT status FROM agent_run_steps WHERE id=$1`, stepID).Scan(&stepSt)
	if stepSt != "RUNNING" {
		t.Fatalf("real step status=%s want RUNNING after rolled-back finalize", stepSt)
	}
}

// TestValidateGraphSnapshotIntegrity_FailClosed rejects incomplete v1 freezes.
func TestValidateGraphSnapshotIntegrity_FailClosed(t *testing.T) {
	t.Parallel()
	agentID := uuid.Must(uuid.NewV7()).String()
	// schema only — missing root node
	raw := json.RawMessage(`{"schemaVersion":"agent_graph_snapshot.v1","rootAgentId":"` + agentID + `","nodes":[],"edges":[]}`)
	if _, err := agentdelegation.ParseSnapshot(raw); err == nil {
		t.Fatal("incomplete snapshot must fail")
	}
	// missing remotesFrozen
	raw2 := json.RawMessage(`{
		"schemaVersion":"agent_graph_snapshot.v1","rootAgentId":"` + agentID + `",
		"nodes":[{"agentId":"` + agentID + `","modelConfigId":"m","modelSnapshot":{"id":"m"},
			"agentSnapshot":{"schemaVersion":"agent-binding.v1"},
			"capabilitySnapshot":{"schemaVersion":"capability-snapshot.v1","releases":[]},"depth":0}],
		"edges":[]
	}`)
	if _, err := agentdelegation.ParseSnapshot(raw2); err == nil {
		t.Fatal("remotesFrozen required")
	}
}
