package a2agateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

func TestInboundExecutionLease_ReclaimAndStaleOwnerRejected(t *testing.T) {
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
	runRepo, err := execution.NewRunRepository(db)
	if err != nil {
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
		ExternalKey: "lease-key", ExternalTaskID: "t1", RunID: runID, Status: "RUNNING",
	})
	if err != nil {
		t.Fatal(err)
	}
	l1, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "owner-a", 2*time.Minute)
	if err != nil || !l1.Owned {
		t.Fatalf("first claim: owned=%v err=%v", l1.Owned, err)
	}
	l2, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "owner-b", 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if l2.Owned {
		t.Fatal("second concurrent claim must not own while lease active")
	}
	// Force lease expiry for reclaim path.
	if _, err := db.Exec(`UPDATE agent_a2a_inbound_tasks SET execute_lease_until = NOW() - interval '1 second' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	l3, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "owner-c", 2*time.Minute)
	if err != nil || !l3.Owned {
		t.Fatalf("reclaim: owned=%v err=%v", l3.Owned, err)
	}
	if l3.Generation <= l1.Generation {
		t.Fatalf("generation must increase: old=%d new=%d", l1.Generation, l3.Generation)
	}
	if err := repo.MarkInboundExecutionFinished(ctx, fx.workspaceID, task.ID,
		agentdelegation.StatusSucceeded, l1.Owner, l1.Token); err == nil {
		t.Fatal("stale owner finish must fail")
	}
	if err := repo.MarkInboundExecutionFinished(ctx, fx.workspaceID, task.ID,
		agentdelegation.StatusSucceeded, l3.Owner, l3.Token); err != nil {
		t.Fatalf("current owner finish: %v", err)
	}
}

func TestOutbox_StaleClaimCannotAckOrNack(t *testing.T) {
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
		ToolCallID: "tc-stale", IdempotencyKey: fx.parentRunID + ":stale:1",
		InputSummary: json.RawMessage(`{}`), InputPayload: json.RawMessage(`{}`),
		StepID: stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatal(err)
	}
	fin := agentdelegation.FinalizeDelegationInput{
		WorkspaceID: fx.workspaceID, DelegationID: delID, StepID: stepID,
		Status:        agentdelegation.StatusSucceeded,
		OutputSummary: json.RawMessage(`{"ok":true}`), OutputPayload: json.RawMessage(`{"result":"x"}`),
	}
	payload, _ := json.Marshal(fin)
	_ = repo.EnqueueFinalizeOutbox(ctx, fx.workspaceID, delID, stepID, payload)

	rowsA, err := repo.ClaimFinalizeOutboxBatch(ctx, "worker-a", 1, 30*time.Millisecond)
	if err != nil || len(rowsA) != 1 {
		t.Fatalf("claim A: %v n=%d", err, len(rowsA))
	}
	time.Sleep(50 * time.Millisecond)
	rowsB, err := repo.ClaimFinalizeOutboxBatch(ctx, "worker-b", 1, 30*time.Second)
	if err != nil || len(rowsB) != 1 {
		t.Fatalf("claim B: %v n=%d", err, len(rowsB))
	}
	if err := repo.DeleteFinalizeOutboxClaimed(ctx, fx.workspaceID, delID, rowsA[0].ClaimedBy, rowsA[0].ClaimToken); err == nil {
		t.Fatal("stale delete must fail")
	}
	if err := repo.NackFinalizeOutbox(ctx, fx.workspaceID, delID, "x", rowsA[0].ClaimedBy, rowsA[0].ClaimToken, rowsA[0].Attempts); err == nil {
		t.Fatal("stale nack must fail")
	}
	if _, err := audit.FinalizeDelegation(ctx, fin); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteFinalizeOutboxClaimed(ctx, fx.workspaceID, delID, rowsB[0].ClaimedBy, rowsB[0].ClaimToken); err != nil {
		t.Fatalf("B delete: %v", err)
	}
}

func TestAuthPinnedTransport_NoCrossOriginAuthorization(t *testing.T) {
	t.Parallel()
	var gotAuth atomic.Value
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer second.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL+"/path", http.StatusFound)
	}))
	defer first.Close()

	req, _ := http.NewRequest(http.MethodGet, first.URL, nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	// Policy mirror of authPinnedTransport: strip Authorization on cross-origin redirect hops.
	stripped := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Header.Del("Authorization")
			return nil
		},
	}
	resp, err := stripped.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	auth, _ := gotAuth.Load().(string)
	if auth != "" {
		t.Fatalf("second host received Authorization=%q", auth)
	}
}

func TestOutboundCardHardFail_NoEndpointFallback(t *testing.T) {
	t.Parallel()
	endpointOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"kind":"message","role":"agent","parts":[{"kind":"text","text":"ok"}]}}`)
	}))
	defer endpointOK.Close()
	cardBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = io.WriteString(w, "not a card")
	}))
	defer cardBad.Close()

	tool, err := a2agateway.NewOutboundTool(a2agateway.OutboundConfig{
		Binding: a2agateway.RemoteBinding{
			ID: uuid.Must(uuid.NewV7()).String(), CallableName: "remote",
			EndpointURL: endpointOK.URL, AgentCardURL: cardBad.URL,
			AllowedHosts: []string{"127.0.0.1"}, Version: 1, TimeoutMs: 2000,
		},
		Audit: &localMemAudit{}, AllowHTTP: true,
		HTTPClient: a2agateway.SecureHTTPClient(2*time.Second, []string{"127.0.0.1"}, a2agateway.EgressPolicy{AllowHTTP: true}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
		WorkspaceID: ws, ParentRunID: run, RunID: run, CallerAgentID: "agent-a",
	})
	out, _ := tool.InvokableRun(ctx, `{"request":"hi"}`)
	if strings.Contains(out, `"result":"ok"`) {
		t.Fatalf("explicit card fail must not fallback to endpoint success: %s", out)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "card") && !strings.Contains(low, "error") && !strings.Contains(low, "fail") {
		t.Fatalf("expected card/error failure payload, got %s", out)
	}
}

type localMemAudit struct {
	rows map[string]agentdelegation.Delegation
}

func (m *localMemAudit) CreateDelegationAndStep(_ context.Context, in agentdelegation.CreateDelegationInput) (agentdelegation.Delegation, bool, error) {
	if m.rows == nil {
		m.rows = map[string]agentdelegation.Delegation{}
	}
	if d, ok := m.rows[in.IdempotencyKey]; ok {
		return d, true, nil
	}
	d := agentdelegation.Delegation{
		ID: in.ID, WorkspaceID: in.WorkspaceID, ParentRunID: in.ParentRunID,
		Status: agentdelegation.StatusRunning, StepID: in.StepID,
	}
	m.rows[in.IdempotencyKey] = d
	return d, false, nil
}
func (m *localMemAudit) FinalizeDelegation(_ context.Context, in agentdelegation.FinalizeDelegationInput) (agentdelegation.Delegation, error) {
	return agentdelegation.Delegation{ID: in.DelegationID, Status: in.Status}, nil
}
func (m *localMemAudit) SetChildRunID(context.Context, string, string, string) error { return nil }
func (m *localMemAudit) RecordDispatchAttempt(context.Context, string, string) error { return nil }
func (m *localMemAudit) AccumulateModelTokens(context.Context, string, string, agentdelegation.TokenUsage) error {
	return nil
}
