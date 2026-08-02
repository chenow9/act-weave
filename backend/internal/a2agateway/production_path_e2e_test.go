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

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2aclient"
	"github.com/google/uuid"
)

// postMessageSend hits production JSON-RPC path (Register → handleInvoke → inboundExecutor).
// Leave taskID empty for a new a2asrv task; set it to resume/reclaim the same task.
// contextID must be stable across reclaim to keep ExternalIdempotencyKey aligned.
func postMessageSend(t *testing.T, baseURL, workspaceID, agentID, text, taskID, msgID string) (status int, body []byte) {
	return postMessageSendCtx(t, baseURL, workspaceID, agentID, text, taskID, msgID, "")
}

func postMessageSendCtx(t *testing.T, baseURL, workspaceID, agentID, text, taskID, msgID, contextID string) (status int, body []byte) {
	t.Helper()
	if contextID == "" {
		if taskID != "" {
			contextID = "ctx-" + taskID
		} else if msgID != "" {
			contextID = "ctx-" + msgID
		}
	}
	msg := map[string]any{
		"kind":      "message",
		"messageId": msgID,
		"role":      "user",
		"parts":     []map[string]any{{"kind": "text", "text": text}},
	}
	if taskID != "" {
		msg["taskId"] = taskID
	}
	if contextID != "" {
		msg["contextId"] = contextID
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  "message/send",
		"params":  map[string]any{"message": msg},
	}
	raw, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/a2a/workspaces/%s/agents/%s/invoke", baseURL, workspaceID, agentID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// TestProductionJSONRPC_HeartbeatPreventsReclaim drives real Register/ServeHTTP + message/send.
func TestProductionJSONRPC_HeartbeatPreventsReclaim(t *testing.T) {
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

	const shortLease = 400 * time.Millisecond
	block := make(chan struct{})
	var execs atomic.Int64
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			execs.Add(1)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-block:
				return "heartbeat-ok", nil
			case <-time.After(1500 * time.Millisecond):
				return "timeout-path", nil
			}
		},
	}
	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	gw, err := a2agateway.NewInboundGateway(repo, audit, runner, "http://127.0.0.1",
		auth, a2agateway.WithLeaseTTL(shortLease))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gw.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	done := make(chan struct{})
	var st int
	var body []byte
	go func() {
		defer close(done)
		// Empty taskId → a2asrv creates new task; inboundExecutor still runs.
		st, body = postMessageSend(t, srv.URL, fx.workspaceID, fx.agentA, "hello heartbeat", "", "msg-hb-1")
	}()

	// Wait until execute is running past original TTL.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if execs.Load() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if execs.Load() == 0 {
		close(block)
		<-done
		t.Fatalf("execute never started body=%s", body)
	}
	// Past original lease; reclaim must fail while heartbeat holds ownership.
	time.Sleep(shortLease + 150*time.Millisecond)
	var taskRowID string
	_ = db.QueryRow(`SELECT id FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT 1`,
		fx.workspaceID).Scan(&taskRowID)
	if taskRowID == "" {
		close(block)
		<-done
		t.Fatal("task not created")
	}
	l2, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, taskRowID, "intruder", shortLease)
	if err != nil {
		t.Fatal(err)
	}
	if l2.Owned {
		close(block)
		<-done
		t.Fatal("heartbeat must prevent reclaim")
	}
	close(block)
	<-done
	if st != http.StatusOK {
		t.Fatalf("status=%d body=%s", st, body)
	}
	var runSt, taskSt, delSt string
	_ = db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE id=$1`, taskRowID).Scan(&taskSt)
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=(SELECT run_id FROM agent_a2a_inbound_tasks WHERE id=$1)`, taskRowID).Scan(&runSt)
	_ = db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=(SELECT delegation_id FROM agent_a2a_inbound_tasks WHERE id=$1)`, taskRowID).Scan(&delSt)
	if taskSt != "SUCCEEDED" || runSt != "SUCCEEDED" || delSt != "SUCCEEDED" {
		t.Fatalf("terminals task=%s run=%s del=%s body=%s", taskSt, runSt, delSt, body)
	}
}

// TestProductionJSONRPC_StaleOwnerAfterReclaim_NoUnfencedOutbox proves production
// path reclaim via TWO independent gateways (a2asrv forbids concurrent same TaskID
// on one server). Same contextId+messageId+body → one durable task; B reclaims.
func TestProductionJSONRPC_StaleOwnerAfterReclaim_NoUnfencedOutbox(t *testing.T) {
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

	const shortLease = 250 * time.Millisecond
	const sharedCtx = "ctx-reclaim-shared"
	const sharedMsg = "msg-reclaim-shared"
	const sharedBody = "shared reclaim body"
	pauseA := make(chan struct{})
	releaseA := make(chan struct{})
	var execN atomic.Int64
	newRunner := func() *a2agateway.DurableInboundRunner {
		return &a2agateway.DurableInboundRunner{
			Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
			Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
				n := execN.Add(1)
				if n == 1 {
					close(pauseA)
					<-releaseA
					return "from-A", nil
				}
				return "from-B", nil
			},
		}
	}
	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	gwA, err := a2agateway.NewInboundGateway(repo, audit, newRunner(), "http://gw-a",
		auth, a2agateway.WithLeaseTTL(shortLease))
	if err != nil {
		t.Fatal(err)
	}
	gwB, err := a2agateway.NewInboundGateway(repo, audit, newRunner(), "http://gw-b",
		auth, a2agateway.WithLeaseTTL(shortLease))
	if err != nil {
		t.Fatal(err)
	}
	muxA, muxB := http.NewServeMux(), http.NewServeMux()
	gwA.Register(muxA)
	gwB.Register(muxB)
	srvA := httptest.NewServer(muxA)
	defer srvA.Close()
	srvB := httptest.NewServer(muxB)
	defer srvB.Close()

	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		// A: no taskId; server mints taskId. Durable key = context|message.
		_, _ = postMessageSendCtx(t, srvA.URL, fx.workspaceID, fx.agentA, sharedBody, "", sharedMsg, sharedCtx)
	}()
	select {
	case <-pauseA:
	case <-time.After(5 * time.Second):
		t.Fatal("A never entered Execute")
	}

	var taskRowID, runID, delID string
	var attemptBefore int
	_ = db.QueryRow(`SELECT id, run_id, COALESCE(delegation_id::text,'') FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT 1`,
		fx.workspaceID).Scan(&taskRowID, &runID, &delID)
	if taskRowID == "" || delID == "" {
		t.Fatal("missing task/del after A start")
	}
	_ = db.QueryRow(`SELECT attempt_count FROM agent_run_delegations WHERE id=$1`, delID).Scan(&attemptBefore)
	if attemptBefore < 1 {
		t.Fatalf("A should have recorded attempt; got %d", attemptBefore)
	}
	// Force expire A lease while still in Execute.
	if _, err := db.Exec(`UPDATE agent_a2a_inbound_tasks SET execute_lease_until=NOW()-interval '2 seconds' WHERE id=$1`, taskRowID); err != nil {
		t.Fatal(err)
	}

	// B: independent gateway, no taskId (different a2asrv taskId), same context/message/body.
	stB, bodyB := postMessageSendCtx(t, srvB.URL, fx.workspaceID, fx.agentA, sharedBody, "", sharedMsg, sharedCtx)
	if stB != http.StatusOK {
		t.Fatalf("B status=%d body=%s", stB, bodyB)
	}
	if execN.Load() < 2 {
		t.Fatalf("B must enter ExecuteRun; execN=%d body=%s", execN.Load(), bodyB)
	}

	// Release A — production FencedInboundTerminal must conflict.
	close(releaseA)
	<-doneA

	worker, err := a2agateway.NewFinalizeWorker(repo, audit, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		worker.DrainOnce(ctx)
		time.Sleep(20 * time.Millisecond)
	}

	var runSt, taskSt, delSt, stepSt string
	var attemptAfter, retryAfter int
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, runID).Scan(&runSt)
	_ = db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE id=$1`, taskRowID).Scan(&taskSt)
	_ = db.QueryRow(`SELECT status, attempt_count, retry_count FROM agent_run_delegations WHERE id=$1`, delID).Scan(&delSt, &attemptAfter, &retryAfter)
	_ = db.QueryRow(`SELECT status FROM agent_run_steps WHERE delegation_id=$1 AND step_type='AGENT_DELEGATION'`, delID).Scan(&stepSt)
	if runSt != "SUCCEEDED" || taskSt != "SUCCEEDED" || delSt != "SUCCEEDED" || stepSt != "SUCCEEDED" {
		t.Fatalf("want all SUCCEEDED got run=%s task=%s del=%s step=%s bodyB=%s", runSt, taskSt, delSt, stepSt, bodyB)
	}
	// B's reclaim re-records attempt (idempotent del, second real dispatch).
	if attemptAfter < 2 {
		t.Fatalf("attempt_count after B=%d want >=2 (A+B dispatches)", attemptAfter)
	}
	if retryAfter != attemptAfter-1 {
		t.Fatalf("retry_count=%d want attempt-1=%d", retryAfter, attemptAfter-1)
	}
	var delOut string
	_ = db.QueryRow(`SELECT COALESCE(output_payload->>'result','') FROM agent_run_delegations WHERE id=$1`, delID).Scan(&delOut)
	if delOut != "from-B" {
		t.Fatalf("delOut=%q want from-B (A must not win)", delOut)
	}
	var nOut int
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_run_delegation_finalize_outbox WHERE workspace_id=$1`, fx.workspaceID).Scan(&nOut)
	if nOut > 0 {
		var payload []byte
		_ = db.QueryRow(`SELECT payload FROM agent_run_delegation_finalize_outbox WHERE workspace_id=$1 LIMIT 1`, fx.workspaceID).Scan(&payload)
		var env a2agateway.FencedTerminalOutboxPayload
		if json.Unmarshal(payload, &env) != nil || env.Kind != a2agateway.FencedTerminalOutboxKind {
			t.Fatalf("unfenced outbox must not exist; payload=%s", payload)
		}
	}
}

// TestProductionJSONRPC_RecordAttemptFail_NoExecute uses real invoke path.
func TestProductionJSONRPC_RecordAttemptFail_NoExecute(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	delRepo, _ := agentdelegation.NewRepository(db)
	base, _ := agentdelegation.NewService(delRepo)
	audit := &failingAttemptAudit{inner: base}
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
			return "should-not", nil
		},
	}
	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	gw, err := a2agateway.NewInboundGateway(repo, audit, runner, "http://test",
		auth, a2agateway.WithLeaseTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gw.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st, body := postMessageSend(t, srv.URL, fx.workspaceID, fx.agentA, "x", "", "msg-att")
	if st != http.StatusOK {
		t.Fatalf("status=%d body=%s", st, body)
	}
	if execs.Load() != 0 {
		t.Fatalf("Execute must not run; calls=%d", execs.Load())
	}
	var taskSt, runSt, delSt string
	_ = db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT 1`, fx.workspaceID).Scan(&taskSt)
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=(SELECT run_id FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT 1)`, fx.workspaceID).Scan(&runSt)
	_ = db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=(SELECT delegation_id FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT 1)`, fx.workspaceID).Scan(&delSt)
	if taskSt == "RUNNING" || runSt == "RUNNING" || delSt == "RUNNING" {
		t.Fatalf("orphans task=%s run=%s del=%s", taskSt, runSt, delSt)
	}
	if delSt != "FAILED" {
		t.Fatalf("del want FAILED got %s body=%s", delSt, body)
	}
}

// TestProductionJSONRPC_CancelRace_SingleWinner races AtomicInboundCancel vs success.
func TestProductionJSONRPC_CancelRace_SingleWinner(t *testing.T) {
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

	pause := make(chan struct{})
	release := make(chan struct{})
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			close(pause)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-release:
				return "success-late", nil
			}
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
		_, _ = postMessageSend(t, srv.URL, fx.workspaceID, fx.agentA, "cancel me", "", "msg-c")
	}()
	select {
	case <-pause:
	case <-time.After(5 * time.Second):
		t.Fatal("execute not started")
	}
	var extTaskID string
	_ = db.QueryRow(`SELECT external_task_id FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT 1`,
		fx.workspaceID).Scan(&extTaskID)
	// Cancel via production CancelInbound (atomic).
	if err := gw.CancelInbound(ctx, fx.workspaceID, expID, "USER", fx.ownerID, extTaskID); err != nil {
		// May race with completion — still check consistency below.
		t.Logf("cancel err (may race): %v", err)
	}
	close(release)
	<-done

	var taskSt, runSt, delSt, stepSt string
	_ = db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE external_task_id=$1`, extTaskID).Scan(&taskSt)
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=(SELECT run_id FROM agent_a2a_inbound_tasks WHERE external_task_id=$1)`, extTaskID).Scan(&runSt)
	_ = db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=(SELECT delegation_id FROM agent_a2a_inbound_tasks WHERE external_task_id=$1)`, extTaskID).Scan(&delSt)
	_ = db.QueryRow(`SELECT status FROM agent_run_steps WHERE delegation_id=(SELECT delegation_id FROM agent_a2a_inbound_tasks WHERE external_task_id=$1) AND step_type='AGENT_DELEGATION' LIMIT 1`, extTaskID).Scan(&stepSt)
	// Single winner: all four share the same terminal family (all CANCELLED or all SUCCEEDED).
	if taskSt == "RUNNING" || runSt == "RUNNING" || delSt == "RUNNING" || stepSt == "RUNNING" {
		t.Fatalf("RUNNING orphan task=%s run=%s del=%s step=%s", taskSt, runSt, delSt, stepSt)
	}
	// Consistency: task/run/del/step should agree on cancel vs success (not mixed cancel+success).
	terms := map[string]bool{taskSt: true, runSt: true, delSt: true, stepSt: true}
	if len(terms) > 1 {
		// Allow CANCELLED/TIMED_OUT family mixture only if not mixing with SUCCEEDED.
		hasSucc := taskSt == "SUCCEEDED" || runSt == "SUCCEEDED" || delSt == "SUCCEEDED" || stepSt == "SUCCEEDED"
		hasCanc := taskSt == "CANCELLED" || runSt == "CANCELLED" || delSt == "CANCELLED" || stepSt == "CANCELLED"
		if hasSucc && hasCanc {
			t.Fatalf("mixed success+cancel task=%s run=%s del=%s step=%s", taskSt, runSt, delSt, stepSt)
		}
	}
}

// TestFencedInboundTerminal_WrongDelegation_NoPartialCommit
func TestFencedInboundTerminal_WrongDelegation_NoPartialCommit(t *testing.T) {
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
		ExternalKey: "wd-k", ExternalTaskID: "wd-t", RunID: runID, Status: "RUNNING",
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
		IdempotencyKey: agentdelegation.IdempotencyKey(expID+":wd-k", "inbound", 1, expID),
		InputSummary:   json.RawMessage(`{}`), InputPayload: json.RawMessage(`{}`),
		StepID: stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.BindInboundTaskDelegation(ctx, fx.workspaceID, task.ID, del.ID); err != nil {
		t.Fatal(err)
	}
	// Second decoy delegation on same parent run (should not be terminalized by wrong id).
	decoyID := uuid.Must(uuid.NewV7()).String()
	decoyStep := uuid.Must(uuid.NewV7()).String()
	_, _, err = audit.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: decoyID, WorkspaceID: fx.workspaceID, ParentRunID: runID,
		CallerAgentID: fx.agentA, Mode: agentdelegation.ModeTask,
		Protocol: agentdelegation.ProtocolA2A, Origin: agentdelegation.OriginExternal,
		Depth: 0, BindingVersion: 1, ToolCallID: "decoy",
		IdempotencyKey: agentdelegation.IdempotencyKey(expID+":decoy", "decoy", 1, expID),
		InputSummary:   json.RawMessage(`{}`), InputPayload: json.RawMessage(`{}`),
		StepID: decoyStep, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := repo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "owner", time.Minute)
	if err != nil || !lease.Owned {
		t.Fatal(err)
	}
	// Wrong delegation id relative to task binding.
	err = repo.FencedInboundTerminal(ctx, a2agateway.FencedTerminalInput{
		WorkspaceID: fx.workspaceID, TaskID: task.ID, RunID: runID,
		Owner: lease.Owner, Token: lease.Token, Generation: lease.Generation,
		TaskStatus: "SUCCEEDED", RunStatus: "SUCCEEDED", ExpectedRunStatus: "RUNNING",
		RunOutputSummary: json.RawMessage(`{"who":"wrong"}`),
		DelegationID:     decoyID, StepID: decoyStep,
		DelStatus: "SUCCEEDED", DelOutputSummary: json.RawMessage(`{}`), DelOutputPayload: json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatal("wrong delegation must fail")
	}
	// No partial commit.
	var runSt, taskSt, delSt, decoySt string
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, runID).Scan(&runSt)
	_ = db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE id=$1`, task.ID).Scan(&taskSt)
	_ = db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=$1`, del.ID).Scan(&delSt)
	_ = db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=$1`, decoyID).Scan(&decoySt)
	if runSt != "RUNNING" || taskSt != "RUNNING" || delSt != "RUNNING" || decoySt != "RUNNING" {
		t.Fatalf("partial commit run=%s task=%s del=%s decoy=%s", runSt, taskSt, delSt, decoySt)
	}
	// Wrong step id for correct delegation also rolls back.
	err = repo.FencedInboundTerminal(ctx, a2agateway.FencedTerminalInput{
		WorkspaceID: fx.workspaceID, TaskID: task.ID, RunID: runID,
		Owner: lease.Owner, Token: lease.Token, Generation: lease.Generation,
		TaskStatus: "SUCCEEDED", RunStatus: "SUCCEEDED", ExpectedRunStatus: "RUNNING",
		RunOutputSummary: json.RawMessage(`{}`),
		DelegationID:     del.ID, StepID: decoyStep, // belongs to other del
		DelStatus: "SUCCEEDED", DelOutputSummary: json.RawMessage(`{}`), DelOutputPayload: json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatal("wrong step must fail")
	}
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, runID).Scan(&runSt)
	_ = db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE id=$1`, task.ID).Scan(&taskSt)
	if runSt != "RUNNING" || taskSt != "RUNNING" {
		t.Fatalf("partial after wrong step run=%s task=%s", runSt, taskSt)
	}
}

// TestClientSendMessage_ViaOfficialClient ensures a2aclient hits registered handler.
func TestClientSendMessage_ViaOfficialClient(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
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
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			return "client-ok", nil
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

	endpoint := fmt.Sprintf("%s/a2a/workspaces/%s/agents/%s/invoke", srv.URL, fx.workspaceID, fx.agentA)
	httpClient := &http.Client{Transport: roundTripAuth{base: http.DefaultTransport}}
	client, err := a2aclient.NewFromEndpoints(context.Background(), []a2a.AgentInterface{{
		URL: endpoint, Transport: a2a.TransportProtocolJSONRPC,
	}}, a2aclient.WithJSONRPCTransport(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.TextPart{Text: "hi from client"})
	// Leave TaskID empty so a2asrv creates a new task (production first-message path).
	_, err = client.SendMessage(context.Background(), &a2a.MessageSendParams{Message: msg})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	var delSt string
	_ = db.QueryRow(`SELECT status FROM agent_run_delegations WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT 1`, fx.workspaceID).Scan(&delSt)
	if delSt != "SUCCEEDED" {
		t.Fatalf("delegation status=%s", delSt)
	}
}

type roundTripAuth struct{ base http.RoundTripper }

func (r roundTripAuth) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer test")
	base := r.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// fixedUserAuth returns a valid USER principal for StartAgentRun (production-shaped).
type fixedUserAuth struct{ actorType, actorID string }

func (f fixedUserAuth) Authorize(context.Context, *http.Request, a2agateway.Exposure) (string, string, error) {
	return f.actorType, f.actorID, nil
}
