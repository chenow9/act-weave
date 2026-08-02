package a2agateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// TestTwoIndependentGateways_SameContextMessage_ReclaimsSameDurable proves item 9/12:
// two real InboundGateway servers share DB; each may mint a different a2asrv taskId,
// but identical contextId+messageId+body hit one durable task/run/delegation.
// Both HTTP/JSON-RPC requests must enter inboundExecutor (counted via PrepareRun).
func TestTwoIndependentGateways_SameContextMessage_ReclaimsSameDurable(t *testing.T) {
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
	const sharedCtx = "ctx-dual-gw"
	const sharedMsg = "msg-dual-gw"
	const sharedBody = "identical body for both gateways"

	pauseA := make(chan struct{})
	releaseA := make(chan struct{})
	var execN atomic.Int64
	newRunner := func() *a2agateway.DurableInboundRunner {
		return &a2agateway.DurableInboundRunner{
			Runs:    runRepo,
			Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
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
	runnerA := newRunner()
	runnerB := newRunner()

	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	gwA, err := a2agateway.NewInboundGateway(repo, audit, runnerA, "http://gw-a",
		auth, a2agateway.WithLeaseTTL(shortLease))
	if err != nil {
		t.Fatal(err)
	}
	gwB, err := a2agateway.NewInboundGateway(repo, audit, runnerB, "http://gw-b",
		auth, a2agateway.WithLeaseTTL(shortLease))
	if err != nil {
		t.Fatal(err)
	}
	muxA, muxB := http.NewServeMux(), http.NewServeMux()
	gwA.Register(muxA)
	gwB.Register(muxB)
	// Count every invoke that reaches the gateway handlers (both enter inboundExecutor path).
	var invokeA, invokeB atomic.Int64
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/invoke") {
			invokeA.Add(1)
		}
		muxA.ServeHTTP(w, r)
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/invoke") {
			invokeB.Add(1)
		}
		muxB.ServeHTTP(w, r)
	}))
	defer srvB.Close()

	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		// No taskId — server A mints its own a2asrv taskId.
		_, _ = postMessageSendCtx(t, srvA.URL, fx.workspaceID, fx.agentA, sharedBody, "", sharedMsg, sharedCtx)
	}()
	select {
	case <-pauseA:
	case <-time.After(8 * time.Second):
		t.Fatal("A never entered ExecuteRun")
	}

	var taskRowID, runID, delID, reqHash string
	var taskCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 AND exposure_id=$2`,
		fx.workspaceID, expID).Scan(&taskCount)
	if taskCount != 1 {
		t.Fatalf("want 1 durable inbound task after A, got %d", taskCount)
	}
	_ = db.QueryRow(`
		SELECT id, run_id, COALESCE(delegation_id::text,''), COALESCE(request_hash,'')
		FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 AND exposure_id=$2
	`, fx.workspaceID, expID).Scan(&taskRowID, &runID, &delID, &reqHash)
	if taskRowID == "" || runID == "" || delID == "" || reqHash == "" {
		t.Fatalf("incomplete durable row task=%s run=%s del=%s hash=%s", taskRowID, runID, delID, reqHash)
	}
	wantHash := a2agateway.RequestBodyHash(sharedBody)
	if reqHash != wantHash {
		t.Fatalf("request_hash=%s want %s", reqHash, wantHash)
	}

	// Expire A's lease so B can reclaim on the same durable task.
	if _, err := db.Exec(`UPDATE agent_a2a_inbound_tasks SET execute_lease_until=NOW()-interval '2 seconds' WHERE id=$1`, taskRowID); err != nil {
		t.Fatal(err)
	}

	// B: independent server, NO taskId (mints different a2asrv taskId), same context/message/body.
	stB, bodyB := postMessageSendCtx(t, srvB.URL, fx.workspaceID, fx.agentA, sharedBody, "", sharedMsg, sharedCtx)
	if stB != http.StatusOK {
		t.Fatalf("B status=%d body=%s", stB, bodyB)
	}
	if invokeA.Load() < 1 || invokeB.Load() < 1 {
		t.Fatalf("both gateways must receive invoke; A=%d B=%d", invokeA.Load(), invokeB.Load())
	}
	if execN.Load() < 2 {
		t.Fatalf("B must reclaim ExecuteRun; execN=%d bodyB=%s", execN.Load(), bodyB)
	}

	// Still exactly one durable task/run/delegation.
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 AND exposure_id=$2`,
		fx.workspaceID, expID).Scan(&taskCount)
	if taskCount != 1 {
		t.Fatalf("want 1 durable task after B, got %d (must not create second by new taskId)", taskCount)
	}
	var run2, del2 string
	_ = db.QueryRow(`SELECT run_id, COALESCE(delegation_id::text,'') FROM agent_a2a_inbound_tasks WHERE id=$1`, taskRowID).
		Scan(&run2, &del2)
	if run2 != runID || del2 != delID {
		t.Fatalf("durable identity changed: run %s→%s del %s→%s", runID, run2, delID, del2)
	}

	close(releaseA)
	<-doneA

	// Drain fenced outbox if any.
	worker, _ := a2agateway.NewFinalizeWorker(repo, audit, nil)
	for i := 0; i < 5; i++ {
		worker.DrainOnce(ctx)
		time.Sleep(15 * time.Millisecond)
	}

	var delOut string
	var attempt int
	_ = db.QueryRow(`SELECT COALESCE(output_payload->>'result',''), attempt_count FROM agent_run_delegations WHERE id=$1`, delID).
		Scan(&delOut, &attempt)
	if delOut != "from-B" {
		t.Fatalf("delOut=%q want from-B", delOut)
	}
	if attempt < 2 {
		t.Fatalf("attempt_count=%d want >=2 (A+B real dispatches)", attempt)
	}
}

// TestTwoIndependentGateways_BodyHashConflict_DoesNotMutateAuthority proves different
// body with same context+message → request-hash conflict; original rows unchanged.
func TestTwoIndependentGateways_BodyHashConflict_DoesNotMutateAuthority(t *testing.T) {
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

	const sharedCtx = "ctx-hash-conflict"
	const sharedMsg = "msg-hash-conflict"
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			return "ok-body-1", nil
		},
	}
	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	gwA, _ := a2agateway.NewInboundGateway(repo, audit, runner, "http://a", auth, a2agateway.WithLeaseTTL(time.Minute))
	gwB, _ := a2agateway.NewInboundGateway(repo, audit, runner, "http://b", auth, a2agateway.WithLeaseTTL(time.Minute))
	muxA, muxB := http.NewServeMux(), http.NewServeMux()
	gwA.Register(muxA)
	gwB.Register(muxB)
	srvA := httptest.NewServer(muxA)
	defer srvA.Close()
	srvB := httptest.NewServer(muxB)
	defer srvB.Close()

	stA, bodyA := postMessageSendCtx(t, srvA.URL, fx.workspaceID, fx.agentA, "body-one", "", sharedMsg, sharedCtx)
	if stA != http.StatusOK {
		t.Fatalf("A status=%d body=%s", stA, bodyA)
	}

	var taskID, runID, delID, hashBefore, statusBefore string
	_ = db.QueryRow(`
		SELECT id, run_id, COALESCE(delegation_id::text,''), request_hash, status
		FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 AND exposure_id=$2
	`, fx.workspaceID, expID).Scan(&taskID, &runID, &delID, &hashBefore, &statusBefore)
	if hashBefore != a2agateway.RequestBodyHash("body-one") {
		t.Fatalf("hash=%s", hashBefore)
	}

	// B: same context+message, different body → conflict; both enter invoke path.
	stB, bodyB := postMessageSendCtx(t, srvB.URL, fx.workspaceID, fx.agentA, "body-TWO-different", "", sharedMsg, sharedCtx)
	if stB != http.StatusOK {
		// JSON-RPC may still 200 with error message in result.
		t.Logf("B status=%d body=%s", stB, bodyB)
	}
	if !strings.Contains(string(bodyB), "conflict") && !strings.Contains(string(bodyB), "claim") {
		// Accept agent message text inside JSON-RPC result.
		if !strings.Contains(string(bodyB), "body does not match") && !strings.Contains(string(bodyB), "inbound claim") {
			t.Fatalf("B should report claim conflict, body=%s", bodyB)
		}
	}

	var hashAfter, statusAfter, runAfter, delAfter string
	var nTasks int
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 AND exposure_id=$2`,
		fx.workspaceID, expID).Scan(&nTasks)
	_ = db.QueryRow(`
		SELECT request_hash, status, run_id, COALESCE(delegation_id::text,'')
		FROM agent_a2a_inbound_tasks WHERE id=$1
	`, taskID).Scan(&hashAfter, &statusAfter, &runAfter, &delAfter)
	if nTasks != 1 {
		t.Fatalf("conflict must not insert second task; n=%d", nTasks)
	}
	if hashAfter != hashBefore || runAfter != runID || delAfter != delID {
		t.Fatalf("authority mutated: hash %s→%s run %s→%s del %s→%s",
			hashBefore, hashAfter, runID, runAfter, delID, delAfter)
	}
}

// TestInboundInvoke_OversizeBody_413 proves MaxBytesReader hard limit.
func TestInboundInvoke_OversizeBody_413(t *testing.T) {
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

	// Content-Length over limit → 413 before read.
	url := fmt.Sprintf("%s/a2a/workspaces/%s/agents/%s/invoke", srv.URL, fx.workspaceID, fx.agentA)
	huge := bytes.Repeat([]byte("x"), a2agateway.MaxInboundRequestBytes+64)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(huge))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	req.ContentLength = int64(len(huge))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413 body=%s", resp.StatusCode, b)
	}
	if strings.Contains(strings.ToLower(string(b)), "sql") ||
		strings.Contains(strings.ToLower(string(b)), "outbox") ||
		strings.Contains(strings.ToLower(string(b)), "pq:") {
		t.Fatalf("public error must not leak internal details: %s", b)
	}
	var env map[string]any
	if json.Unmarshal(b, &env) != nil {
		t.Fatalf("body not json: %s", b)
	}
	if env["error"] != "A2A_BODY_TOO_LARGE" {
		t.Fatalf("error code=%v", env["error"])
	}
	if _, ok := env["traceRef"].(string); !ok || env["traceRef"] == "" {
		t.Fatalf("missing traceRef: %v", env)
	}
}

// TestNewInboundGateway_NilAuth_FailClosed requires non-nil AuthChecker.
func TestNewInboundGateway_NilAuth_FailClosed(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	repo, err := a2agateway.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	delRepo, _ := agentdelegation.NewRepository(db)
	audit, _ := agentdelegation.NewService(delRepo)
	runRepo, _ := execution.NewRunRepository(db)
	runner := &a2agateway.DurableInboundRunner{Runs: runRepo}
	_, err = a2agateway.NewInboundGateway(repo, audit, runner, "http://test", nil)
	if err == nil {
		t.Fatal("nil AuthChecker must fail closed")
	}
	if !errors.Is(err, a2agateway.ErrInvalid) && !strings.Contains(err.Error(), "AuthChecker") {
		t.Fatalf("want AuthChecker required, got %v", err)
	}
}

func TestExternalIdempotencyKey_MessagePreferredOverTask(t *testing.T) {
	k1 := a2agateway.ExternalIdempotencyKey("task-A", "ctx", "msg-1")
	k2 := a2agateway.ExternalIdempotencyKey("task-B", "ctx", "msg-1")
	if k1 != k2 || k1 != "ctx|msg-1" {
		t.Fatalf("keys must ignore taskId when message present: %q %q", k1, k2)
	}
	k3 := a2agateway.ExternalIdempotencyKey("task-A", "ctx", "")
	if k3 == k1 {
		t.Fatal("legacy task|context key must differ")
	}
}

// TestAgentCard_VersionAndSecuritySchemes
func TestAgentCard_VersionAndSecuritySchemes(t *testing.T) {
	card := a2agateway.BuildAgentCardForExposure("https://api.example", a2agateway.Exposure{
		WorkspaceID: "ws", AgentID: "ag", PublicName: "N", PublicDescription: "D",
		AuthMode: a2agateway.AuthModeAgentAccess, Version: 7,
	})
	if card.Version != "7" {
		t.Fatalf("version=%q", card.Version)
	}
	if len(card.SecuritySchemes) == 0 || len(card.Security) == 0 {
		t.Fatalf("security schemes required for AGENT_ACCESS: %+v", card)
	}
}
