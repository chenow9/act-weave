package a2agateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

// TestDualGateway_SameBody_NoShadowRuns proves claim-under-lock: concurrent/retry
// same exposure+context+message+body creates exactly one agent_run, one inbound
// task, one delegation; agentaudit stats do not count CANCELLED candidates.
func TestDualGateway_SameBody_NoShadowRuns(t *testing.T) {
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

	mkRunner := func() *a2agateway.DurableInboundRunner {
		return &a2agateway.DurableInboundRunner{
			Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
			Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
				return "ok-same-body", nil
			},
		}
	}

	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	rA := mkRunner()
	rB := mkRunner()
	gwA, err := a2agateway.NewInboundGateway(repo, audit, rA, "http://a", auth, a2agateway.WithLeaseTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	gwB, err := a2agateway.NewInboundGateway(repo, audit, rB, "http://b", auth, a2agateway.WithLeaseTTL(time.Minute))
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

	const sharedCtx = "ctx-shadow"
	const sharedMsg = "msg-shadow"
	const body = "identical durable body"

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = postMessageSendCtx(t, srvA.URL, fx.workspaceID, fx.agentA, body, "", sharedMsg, sharedCtx)
	}()
	go func() {
		defer wg.Done()
		// slight stagger still under lock
		time.Sleep(5 * time.Millisecond)
		_, _ = postMessageSendCtx(t, srvB.URL, fx.workspaceID, fx.agentA, body, "", sharedMsg+"", sharedCtx)
	}()
	wg.Wait()

	// Fixture seeds one parent run; count only A2A_INBOUND authoritative runs.
	var nTasks, nInboundRuns, nDels, nCancelledInbound int
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 AND exposure_id=$2`,
		fx.workspaceID, expID).Scan(&nTasks)
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE workspace_id=$1 AND trigger_type='A2A_INBOUND'`,
		fx.workspaceID).Scan(&nInboundRuns)
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_run_delegations WHERE workspace_id=$1 AND origin='EXTERNAL'`,
		fx.workspaceID).Scan(&nDels)
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE workspace_id=$1 AND trigger_type='A2A_INBOUND' AND status='CANCELLED'`,
		fx.workspaceID).Scan(&nCancelledInbound)
	if nTasks != 1 {
		t.Fatalf("inbound tasks=%d want 1", nTasks)
	}
	if nInboundRuns != 1 {
		t.Fatalf("A2A_INBOUND agent_runs=%d want 1 (no shadow candidates)", nInboundRuns)
	}
	if nDels != 1 {
		t.Fatalf("external delegations=%d want 1", nDels)
	}
	if nCancelledInbound != 0 {
		t.Fatalf("CANCELLED shadow inbound runs=%d want 0", nCancelledInbound)
	}

	var nTraces int
	_ = db.QueryRow(`SELECT COUNT(DISTINCT trace_id) FROM agent_runs WHERE workspace_id=$1 AND trigger_type='A2A_INBOUND'`,
		fx.workspaceID).Scan(&nTraces)
	if nTraces != 1 {
		t.Fatalf("inbound traces=%d want 1", nTraces)
	}

	// Authority run must be the sole inbound mapping.
	var authRun string
	_ = db.QueryRow(`SELECT run_id FROM agent_a2a_inbound_tasks WHERE exposure_id=$1`, expID).Scan(&authRun)
	var nAuth int
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE id=$1`, authRun).Scan(&nAuth)
	if nAuth != 1 {
		t.Fatal("missing authoritative run")
	}
	_ = ctx
}

// TestDualGateway_BodyConflict_NoShadowRun: different body same key does not create
// a second run/trace; original authority stays sole visible call.
func TestDualGateway_BodyConflict_NoShadowRun(t *testing.T) {
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
			return "first", nil
		},
	}
	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	gwA, _ := a2agateway.NewInboundGateway(repo, audit, runner, "http://a", auth)
	gwB, _ := a2agateway.NewInboundGateway(repo, audit, runner, "http://b", auth)
	muxA, muxB := http.NewServeMux(), http.NewServeMux()
	gwA.Register(muxA)
	gwB.Register(muxB)
	srvA := httptest.NewServer(muxA)
	defer srvA.Close()
	srvB := httptest.NewServer(muxB)
	defer srvB.Close()

	const sharedCtx, sharedMsg = "ctx-conflict", "msg-conflict"
	st, _ := postMessageSendCtx(t, srvA.URL, fx.workspaceID, fx.agentA, "body-one", "", sharedMsg, sharedCtx)
	if st != http.StatusOK {
		t.Fatalf("A status=%d", st)
	}
	stB, bodyB := postMessageSendCtx(t, srvB.URL, fx.workspaceID, fx.agentA, "body-TWO", "", sharedMsg, sharedCtx)
	if stB != http.StatusOK {
		t.Logf("B status=%d body=%s", stB, bodyB)
	}

	var nInboundRuns, nTasks, nCancelledInbound int
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE workspace_id=$1 AND trigger_type='A2A_INBOUND'`,
		fx.workspaceID).Scan(&nInboundRuns)
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_a2a_inbound_tasks WHERE workspace_id=$1`, fx.workspaceID).Scan(&nTasks)
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE workspace_id=$1 AND trigger_type='A2A_INBOUND' AND status='CANCELLED'`,
		fx.workspaceID).Scan(&nCancelledInbound)
	if nInboundRuns != 1 || nTasks != 1 {
		t.Fatalf("inbound runs=%d tasks=%d want 1 each", nInboundRuns, nTasks)
	}
	if nCancelledInbound != 0 {
		t.Fatalf("shadow cancelled inbound=%d", nCancelledInbound)
	}
}

// TestDualGateway_TaskIDAliases_BothCancelSameAuthority: A and B mint different
// a2asrv TaskIDs from independent official message/send responses; either cancel
// maps to the same durable run/delegation. No synthetic RegisterInboundTaskAlias.
func TestDualGateway_TaskIDAliases_BothCancelSameAuthority(t *testing.T) {
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

	// A holds Execute so B can only claim-as-replay + register alias (in-progress).
	// Short lease so AtomicInboundCancel → heartbeat miss cancels execCtx promptly.
	const leaseTTL = 300 * time.Millisecond
	block := make(chan struct{})
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			select {
			case <-block:
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(15 * time.Second):
			}
			return "slow", nil
		},
	}
	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	gwA, _ := a2agateway.NewInboundGateway(repo, audit, runner, "http://a", auth, a2agateway.WithLeaseTTL(leaseTTL))
	gwB, _ := a2agateway.NewInboundGateway(repo, audit, runner, "http://b", auth, a2agateway.WithLeaseTTL(leaseTTL))
	muxA, muxB := http.NewServeMux(), http.NewServeMux()
	gwA.Register(muxA)
	gwB.Register(muxB)
	srvA := httptest.NewServer(muxA)
	defer srvA.Close()
	srvB := httptest.NewServer(muxB)
	defer srvB.Close()

	const sharedCtx, sharedMsg, body = "ctx-alias", "msg-alias", "alias-body"
	type sendResult struct {
		status int
		body   []byte
	}
	doneA := make(chan sendResult, 1)
	go func() {
		st, b := postMessageSendCtx(t, srvA.URL, fx.workspaceID, fx.agentA, body, "", sharedMsg, sharedCtx)
		doneA <- sendResult{status: st, body: b}
	}()

	// Wait until durable authority exists (A claimed under lock).
	var inboundID, runID, delID string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_ = db.QueryRow(`
			SELECT id, run_id, COALESCE(delegation_id::text,'')
			FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 AND exposure_id=$2
		`, fx.workspaceID, expID).Scan(&inboundID, &runID, &delID)
		if inboundID != "" && delID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if inboundID == "" || delID == "" {
		t.Fatal("A never claimed durable task")
	}

	// B: independent server, no client taskId — a2asrv B mints its own TaskID.
	// Lease still held by A → B returns in-progress after auto-registering alias.
	stB, bodyB := postMessageSendCtx(t, srvB.URL, fx.workspaceID, fx.agentA, body, "", sharedMsg, sharedCtx)
	if stB != http.StatusOK {
		t.Fatalf("B status=%d body=%s", stB, bodyB)
	}
	taskIDB := parseJSONRPCTaskID(t, bodyB)
	if taskIDB == "" {
		t.Fatalf("B message/send must return TaskID; body=%s", bodyB)
	}

	// Cancel via B's real HTTP TaskID — must map to the same authority while A runs.
	if err := gwB.CancelInbound(ctx, fx.workspaceID, expID, "USER", fx.ownerID, taskIDB); err != nil {
		t.Fatalf("cancel via B TaskID %s: %v", taskIDB, err)
	}
	var runSt, delSt, taskSt string
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, runID).Scan(&runSt)
	_ = db.QueryRow(`SELECT status FROM agent_run_delegations WHERE id=$1`, delID).Scan(&delSt)
	_ = db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE id=$1`, inboundID).Scan(&taskSt)
	if runSt != "CANCELLED" || delSt != "CANCELLED" || taskSt != "CANCELLED" {
		t.Fatalf("after cancel via B TaskID run=%s del=%s task=%s", runSt, delSt, taskSt)
	}

	// A HTTP returns after cancel (lease heartbeat / execCtx cancel); parse real TaskID.
	var resA sendResult
	select {
	case resA = <-doneA:
	case <-time.After(5 * time.Second):
		close(block)
		t.Fatal("A message/send never returned after cancel")
	}
	select {
	case <-block:
	default:
		close(block)
	}
	taskIDA := parseJSONRPCTaskID(t, resA.body)
	if taskIDA == "" {
		t.Fatalf("A message/send must return TaskID; status=%d body=%s", resA.status, resA.body)
	}
	if taskIDA == taskIDB {
		t.Fatalf("independent servers must mint distinct TaskIDs; both=%s", taskIDA)
	}

	// Both TaskIDs must have been auto-registered as aliases (no manual Register).
	for _, tid := range []string{taskIDA, taskIDB} {
		var mapped string
		err := db.QueryRow(`
			SELECT inbound_task_id::text FROM agent_a2a_inbound_task_aliases
			WHERE workspace_id=$1 AND exposure_id=$2 AND external_task_id=$3
		`, fx.workspaceID, expID, tid).Scan(&mapped)
		if err != nil || mapped != inboundID {
			t.Fatalf("TaskID %s not auto-aliased to authority %s (mapped=%q err=%v)", tid, inboundID, mapped, err)
		}
	}

	// Cancel via A's real TaskID is idempotent and still maps to the same authority.
	if err := gwA.CancelInbound(ctx, fx.workspaceID, expID, "USER", fx.ownerID, taskIDA); err != nil {
		t.Fatalf("cancel via A TaskID %s: %v", taskIDA, err)
	}
	var nInboundRuns, nTasks, nDels int
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE workspace_id=$1 AND trigger_type='A2A_INBOUND'`,
		fx.workspaceID).Scan(&nInboundRuns)
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_a2a_inbound_tasks WHERE workspace_id=$1`, fx.workspaceID).Scan(&nTasks)
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_run_delegations WHERE workspace_id=$1 AND origin='EXTERNAL'`,
		fx.workspaceID).Scan(&nDels)
	if nInboundRuns != 1 || nTasks != 1 || nDels != 1 {
		t.Fatalf("after dual cancel inboundRuns=%d tasks=%d dels=%d want 1", nInboundRuns, nTasks, nDels)
	}
	_ = db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, runID).Scan(&runSt)
	if runSt != "CANCELLED" {
		t.Fatalf("authority run status=%s", runSt)
	}
}

// parseJSONRPCTaskID extracts TaskID from an official message/send JSON-RPC body:
// result.id (kind=task) or result.taskId (kind=message).
func parseJSONRPCTaskID(t *testing.T, body []byte) string {
	t.Helper()
	var envelope struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Result == nil {
		return ""
	}
	if id, ok := envelope.Result["id"].(string); ok && strings.TrimSpace(id) != "" {
		if kind, _ := envelope.Result["kind"].(string); kind == "task" || kind == "" {
			// Prefer task.id when present.
			if kind == "task" {
				return strings.TrimSpace(id)
			}
		}
	}
	if tid, ok := envelope.Result["taskId"].(string); ok && strings.TrimSpace(tid) != "" {
		return strings.TrimSpace(tid)
	}
	// Some task results still carry id without kind in intermediate shapes.
	if id, ok := envelope.Result["id"].(string); ok {
		return strings.TrimSpace(id)
	}
	return ""
}
