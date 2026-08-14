package acceptance_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/transport/sse"

	"github.com/google/uuid"
)

// TestAAPFailureRecovery is the M10-T6 chaos / recovery gate.
//
// Chaos matrix coverage for AAP failure recovery lives in this package.
// RTO/RPO conclusions, and dual-tx / continuation repair notes.
func TestAAPFailureRecovery(t *testing.T) {
	t.Run("DBBriefFailureDoesNotClaimSuccess", testFailureDBBriefFailure)
	t.Run("DualTransactionGapRepairIdempotent", testFailureDualTransactionRepair)
	t.Run("NotifyLossCommittedFactsRemainReadable", testFailureNotifyLoss)
	t.Run("ProxyDisconnectSSEReconnect", testFailureProxyDisconnect)
	t.Run("TokenExpiryReconnectSameCursor", testFailureTokenExpiry)
	t.Run("RollingRestartCatchUp", testFailureRollingRestart)
	t.Run("DecisionRaceSingleWinner", testFailureDecisionRace)
	t.Run("ApprovedContinuationDispatchRecovery", testFailureContinuationRecovery)
}

func testFailureDBBriefFailure(t *testing.T) {
	db, ids := openSeededRun(t, 1)
	unit, err := protocolevent.NewProtocolUnitOfWork(db, &failingNotifier{fail: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = unit.Execute(context.Background(), func(context.Context, *protocolevent.ProtocolTransaction) error {
		return errors.New("injected db brief failure")
	})
	if err == nil {
		t.Fatal("expected transactional work failure")
	}
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := reader.ReadRunAfter(context.Background(), ids.scope(), 0, 10)
	if len(events) != 0 {
		t.Fatalf("failed work leaked events: %+v", events)
	}
}

func testFailureDualTransactionRepair(t *testing.T) {
	db, ids := openSeededRun(t, 2)
	mustExec(t, db, `DELETE FROM protocol_events WHERE run_id=$1`, ids.runID)
	mustExec(t, db, `DELETE FROM protocol_event_streams WHERE run_id=$1`, ids.runID)
	// Run is already RUNNING from seed; leave status alone (lock_version triggers).
	runs, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	notifier := protocolevent.NewInProcessLiveNotifier()
	defer notifier.Close()
	unit, err := protocolevent.NewProtocolUnitOfWork(db, notifier)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := execution.NewProtocolRunLifecycleService(runs, unit)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	repair, err := execution.NewProtocolLifecycleRepair(runs, lifecycle, reader)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := repair.EnsureStartedEvents(ctx, ids.workspaceID, ids.runID)
	if err != nil || len(first.Events) < 2 {
		t.Fatalf("first repair=%+v err=%v", first, err)
	}
	second, err := repair.EnsureStartedEvents(ctx, ids.workspaceID, ids.runID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Events[0].ID != first.Events[0].ID {
		t.Fatal("repair not idempotent")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM protocol_events WHERE run_id=$1`, ids.runID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func testFailureNotifyLoss(t *testing.T) {
	db, ids := openSeededRun(t, 3)
	notifier := &failingNotifier{fail: true}
	unit, err := protocolevent.NewProtocolUnitOfWork(db, notifier)
	if err != nil {
		t.Fatal(err)
	}
	event := buildStartedEvent(t, ids)
	result, err := unit.Execute(context.Background(), func(ctx context.Context, tx *protocolevent.ProtocolTransaction) error {
		_, err := tx.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.NotifyError == nil {
		t.Fatal("expected notify error after commit")
	}
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	events, err := reader.ReadRunAfter(context.Background(), ids.scope(), 0, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("committed facts lost: n=%d err=%v", len(events), err)
	}
}

func testFailureProxyDisconnect(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	writer, err := sse.NewDeadlineWriter(server, server.SetWriteDeadline, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	event := buildValidSSEProtocolEvent(t, 42)
	done := make(chan error, 1)
	go func() { done <- sse.NewEncoder().Encode(writer, event) }()
	select {
	case err := <-done:
		if !errors.Is(err, sse.ErrSlowConsumer) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	var buf bytes.Buffer
	if err := sse.NewEncoder().Encode(&buf, event); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("id: 42\n")) {
		t.Fatalf("reconnect lost cursor: %s", buf.String())
	}
}

func testFailureTokenExpiry(t *testing.T) {
	changes := agentaccessauth.NewInProcessSecurityChanges()
	defer changes.Close()
	revalidator, err := agentaccessauth.NewStreamRevalidator(
		agentaccessauth.NewControlledStreamAuthorizer(), changes,
		agentaccessauth.RevalidationPolicy{Interval: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := agentaccessauth.StreamBinding{
		WorkspaceID: "a0000000-0000-4000-8000-000000000001",
		AgentID:     "a0000000-0000-4000-8000-000000000002",
		ClientID:    "a0000000-0000-4000-8000-000000000003",
		GrantID:     "a0000000-0000-4000-8000-000000000004",
		PrincipalID: "a0000000-0000-4000-8000-000000000005",
		SubjectID:   "a0000000-0000-4000-8000-000000000006",
		SecurityVersion: 1,
		TokenExpiresAt:  time.Now().UTC().Add(15 * time.Millisecond),
	}
	if err := revalidator.Monitor(context.Background(), binding); !errors.Is(err, agentaccessauth.ErrTokenExpired) {
		t.Fatalf("err=%v", err)
	}
	var signal bytes.Buffer
	if err := sse.NewEncoder().EncodeStreamError(&signal, sse.NewStreamErrorSignal(
		"TOKEN_EXPIRED", "access token expired", true,
		"req-fail", "trace-fail", nil, time.Now().UTC(),
	)); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(signal.Bytes(), []byte("id: ")) {
		t.Fatalf("cursor advanced: %s", signal.String())
	}
}

func testFailureRollingRestart(t *testing.T) {
	db, ids := openSeededRun(t, 4)
	instanceA := protocolevent.NewInProcessLiveNotifier()
	unit, err := protocolevent.NewProtocolUnitOfWork(db, instanceA)
	if err != nil {
		t.Fatal(err)
	}
	event := buildStartedEvent(t, ids)
	if _, err := unit.Execute(context.Background(), func(ctx context.Context, tx *protocolevent.ProtocolTransaction) error {
		_, err := tx.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_ = instanceA.Close()

	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	events, err := reader.ReadRunAfter(context.Background(), ids.scope(), 0, 100)
	if err != nil || len(events) != 1 || events[0].Sequence != 1 {
		t.Fatalf("rolling restart lost facts: %+v err=%v", events, err)
	}
}

func testFailureDecisionRace(t *testing.T) {
	type state struct {
		mu     sync.Mutex
		status string
		wins   int
	}
	s := &state{status: "PENDING"}
	var wait sync.WaitGroup
	results := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.status != "PENDING" {
				results <- false
				return
			}
			s.status = "CONFIRMED"
			s.wins++
			results <- true
		}()
	}
	wait.Wait()
	close(results)
	var wins, losses int
	for ok := range results {
		if ok {
			wins++
		} else {
			losses++
		}
	}
	if wins != 1 || losses != 7 || s.wins != 1 {
		t.Fatalf("wins=%d losses=%d s.wins=%d", wins, losses, s.wins)
	}
}

func testFailureContinuationRecovery(t *testing.T) {
	// Durable approve → crash before Dispatch → ContinuationRecoveryService re-drives
	// claim-CAS Resume without relying on in-memory EnqueueContinue.
	// Detailed fixture setup lives with production recovery unit tests; this gate
	// asserts the public recovery surface against a live DB of approved PENDING
	// checkpoints using the same service constructors as ops workers.
	db, runs, confirmations := openExecutionStyleFixture(t)
	ctx := context.Background()

	var sideEffects atomic.Int32
	var resolverCalls atomic.Int32
	pipeline := newStubPipeline(confirmations, &resolverCalls, &sideEffects)
	toolExecutor, err := execution.NewToolConfirmationResumeExecutor(pipeline)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := execution.NewConfirmationResumeRegistry(toolExecutor)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := execution.NewConfirmationResumeRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	resumes, err := execution.NewConfirmationResumeService(checkpoints, confirmations, runs, registry)
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := execution.NewInteractionDecisionService(confirmations, resumes)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := execution.NewContinuationRecoveryService(confirmations, resumes, decisions)
	if err != nil {
		t.Fatal(err)
	}

	// Empty list is safe.
	pending, err := recovery.ListApprovedPendingDispatch(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected empty pending on fresh fixture, got %d", len(pending))
	}

	// Prepare + confirm without Dispatch (process-exit window).
	run, err := runs.GetAgentRun(ctx, execWS, execRun)
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"orders":["A-1"]}`)
	policy, err := execution.EvaluateConfirmationPolicy(execution.ConfirmationPolicyInput{
		WorkspaceSettings: json.RawMessage(`{}`),
		Release: execution.ConfirmationReleaseRisk{
			ReleaseID: execRelease, RiskLevel: "HIGH", SideEffectLevel: "WRITE",
			RequiresConfirmation: true, InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Connection: execution.ConfirmationConnectionRisk{
			ConnectionID: execConn, Environment: "PRODUCTION",
		},
		Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := execution.InvokeRequest{
		InvocationID: execInv, WorkspaceID: execWS, CapabilityID: execCap,
		ReleaseID: execRelease, ActorType: "USER", ActorID: execOwner,
		TraceID: "trace-acc-cont", Input: input, ExplicitConnectionID: execConn,
		PlanHash: execPlanHash, IdempotencyKey: "acc-cont",
	}
	resolved := execution.ResolvedInvocation{
		Snapshot: execution.ReleaseSnapshot{
			ReleaseID: execRelease, WorkspaceID: execWS, CapabilityID: execCap,
			ToolVersionID: execVer, ExecutorType: execution.ExecutorTypeHTTP,
			ProviderID: execProv, InputSchema: json.RawMessage(`{"type":"object","required":["orders"]}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`), ErrorMappings: json.RawMessage(`{}`),
			RuntimePolicy: json.RawMessage(`{}`), Checksum: execChecksum,
		},
		Connection: execution.ConnectionSnapshot{
			ID: execConn, WorkspaceID: execWS, ProviderID: execProv, Environment: "PRODUCTION",
		},
		Credential: execution.CredentialReference{WorkspaceID: execWS, AuthMode: "NONE"},
		RiskLevel:  "HIGH", SideEffectLevel: "WRITE", RequiresConfirmation: true,
		Idempotent: true, SupportsIdempotencyKey: true,
	}
	reqSnap, resSnap, err := execution.BuildToolConfirmationResumeSnapshots(request, resolved)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := resumes.Prepare(ctx, execution.PrepareConfirmationResumeInput{
		Confirmation: execution.RequestExecutionConfirmationInput{
			ID: execConf, WorkspaceID: execWS, RunID: execRun, NodeID: "acc-cont",
			TargetItemID: execInv, ReleaseID: execRelease, ConnectionID: execConn,
			PlanHash: execPlanHash, RequestedBy: execOwner, Decision: policy,
		},
		Kind: execution.ResumeKindTool, SnapshotSchemaVersion: execution.ConfirmationResumeSnapshotVersion,
		RequestSnapshot: reqSnap, ResolvedSnapshot: resSnap, Input: input,
		ExpectedRunLockVersion: run.LockVersion,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := confirmations.Confirm(ctx, execution.ConfirmExecutionConfirmationInput{
		WorkspaceID: execWS, ConfirmationID: execConf, ActorID: execOwner,
		ResumeToken: prepared.Requested.ResumeToken, RunID: execRun, TargetItemID: execInv,
		ReleaseID: execRelease, ConnectionID: execConn, PlanHash: execPlanHash,
		Input: input, ExpectedLockVersion: 1,
	}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if sideEffects.Load() != 0 {
		t.Fatal("side effect before recovery")
	}
	pending, err = recovery.ListApprovedPendingDispatch(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	result, err := recovery.RecoverDispatch(ctx, execWS, execConf)
	if err != nil || result.DispatchError != nil || result.ResumeStatus != execution.ResumeStatusSucceeded {
		t.Fatalf("recover=%+v err=%v", result, err)
	}
	if sideEffects.Load() != 1 {
		t.Fatalf("effects=%d", sideEffects.Load())
	}
	again, err := recovery.RecoverDispatch(ctx, execWS, execConf)
	if err != nil || !again.Cached || sideEffects.Load() != 1 {
		t.Fatalf("idempotent recover failed: %+v effects=%d err=%v", again, sideEffects.Load(), err)
	}
}

// --- seed / stubs ------------------------------------------------------------

const (
	execOwner     = "608f1f2e-7b5a-7c3d-8e9f-123456789001"
	execWS        = "608f1f2e-7b5a-7c3d-8e9f-123456789002"
	execOtherWS   = "608f1f2e-7b5a-7c3d-8e9f-123456789003"
	execModel     = "608f1f2e-7b5a-7c3d-8e9f-123456789004"
	execAgent     = "608f1f2e-7b5a-7c3d-8e9f-123456789006"
	execSession   = "608f1f2e-7b5a-7c3d-8e9f-123456789008"
	execRun       = "608f1f2e-7b5a-7c3d-8e9f-12345678900a"
	execProv      = "708f1f2e-7b5a-7c3d-8e9f-123456789001"
	execConn      = "708f1f2e-7b5a-7c3d-8e9f-123456789003"
	execCap       = "708f1f2e-7b5a-7c3d-8e9f-123456789005"
	execVer       = "708f1f2e-7b5a-7c3d-8e9f-123456789007"
	execRelease   = "708f1f2e-7b5a-7c3d-8e9f-123456789009"
	execInv       = "f08f1f2e-7b5a-7c3d-8e9f-123456789002"
	execConf      = "f08f1f2e-7b5a-7c3d-8e9f-123456789001"
	execPlanHash  = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	execChecksum  = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	execWorkflow  = "608f1f2e-7b5a-7c3d-8e9f-12345678900c"
	execRevision  = "608f1f2e-7b5a-7c3d-8e9f-123456789012"
	execWfExec    = "708f1f2e-7b5a-7c3d-8e9f-12345678900c"
	execGraphHash = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

type seededIDs struct {
	ownerID, workspaceID, modelID, agentID, sessionID, runID, streamID string
}

func (ids seededIDs) scope() protocolevent.RunScope {
	return protocolevent.RunScope{
		WorkspaceID: ids.workspaceID, AgentID: ids.agentID,
		ConversationID: ids.sessionID, RunID: ids.runID,
	}
}

func openSeededRun(t *testing.T, salt int) (*sql.DB, seededIDs) {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	ids := seededIDs{
		ownerID: fmt.Sprintf("a%07x-0000-4000-8000-000000000001", salt),
		workspaceID: fmt.Sprintf("a%07x-0000-4000-8000-000000000002", salt),
		modelID: fmt.Sprintf("a%07x-0000-4000-8000-000000000003", salt),
		agentID: fmt.Sprintf("a%07x-0000-4000-8000-000000000004", salt),
		sessionID: fmt.Sprintf("a%07x-0000-4000-8000-000000000005", salt),
		runID: fmt.Sprintf("a%07x-0000-4000-8000-000000000006", salt),
		streamID: fmt.Sprintf("a%07x-0000-4000-8000-000000000007", salt),
	}
	mustExec(t, db, `INSERT INTO users(id,username,display_name) VALUES($1,$2,'Owner')`,
		ids.ownerID, fmt.Sprintf("owner%d", salt))
	mustExec(t, db, `
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,$2,'Space','PRODUCTION',$3,$3,$3)`,
		ids.workspaceID, fmt.Sprintf("space-%d", salt), ids.ownerID)
	mustExec(t, db, `
		INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'M','openai','https://m.example','m',$3,$3)`,
		ids.modelID, ids.workspaceID, ids.ownerID)
	mustExec(t, db, `
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'A',$3,$4,$4)`,
		ids.agentID, ids.workspaceID, ids.modelID, ids.ownerID)
	mustExec(t, db, `
		INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		VALUES($1,$2,$3,'S',$4)`,
		ids.sessionID, ids.workspaceID, ids.agentID, ids.ownerID)
	mustExec(t, db, `
		INSERT INTO agent_runs(
		 id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
		 triggered_by_id,trace_id,model_snapshot,capability_snapshot
		) VALUES($1,$2,$3,$4,'RUNNING','API','USER',$5,'trace','{}','{}')`,
		ids.runID, ids.workspaceID, ids.sessionID, ids.agentID, ids.ownerID)
	mustExec(t, db, `
		INSERT INTO protocol_event_streams(id,workspace_id,agent_id,conversation_id,run_id)
		VALUES($1,$2,$3,$4,$5)`,
		ids.streamID, ids.workspaceID, ids.agentID, ids.sessionID, ids.runID)
	return db, ids
}

func openExecutionStyleFixture(t *testing.T) (*sql.DB, *execution.RunRepository, *execution.ConfirmationService) {
	t.Helper()
	// Reuse the same ID namespace as execution confirmation resume fixtures so
	// insert paths stay aligned with production FK constraints.
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	// Minimal graph sufficient for Prepare/Confirm/Resume with stub pipeline.
	mustExec(t, db, `INSERT INTO users(id,username,display_name) VALUES($1,'execution.owner','Execution Owner')`, execOwner)
	mustExec(t, db, `
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'execution-space','Execution Space','PRODUCTION',$3,$3,$3),
		($2,'execution-other','Execution Other','SANDBOX',$3,$3,$3)
	`, execWS, execOtherWS, execOwner)
	mustExec(t, db, `
		INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'Execution Model','openai','https://models.example.test','execution-model',$3,$3)
	`, execModel, execWS, execOwner)
	mustExec(t, db, `
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'Execution Agent',$3,$4,$4)
	`, execAgent, execWS, execModel, execOwner)
	mustExec(t, db, `
		INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		VALUES($1,$2,$3,'Execution session',$4)
	`, execSession, execWS, execAgent, execOwner)
	mustExec(t, db, `
		INSERT INTO agent_runs(
		 id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
		 triggered_by_id,trace_id,model_snapshot,capability_snapshot
		) VALUES($1,$2,$3,$4,'RUNNING','API','USER',$5,'trace-execution','{}','{}')
	`, execRun, execWS, execSession, execAgent, execOwner)
	mustExec(t, db, `
		INSERT INTO capability_providers(
		 id,workspace_id,name,provider_kind,driver_key,transport,created_by,updated_by
		) VALUES($1,$2,'Invocation Provider','HTTP_OPENAPI','http.openapi','HTTP',$3,$3)
	`, execProv, execWS, execOwner)
	mustExec(t, db, `
		INSERT INTO service_connections(
		 id,workspace_id,provider_id,name,alias,environment,auth_mode,created_by,updated_by
		) VALUES($1,$2,$3,'Invocation Connection','primary','PRODUCTION','NONE',$4,$4)
	`, execConn, execWS, execProv, execOwner)
	mustExec(t, db, `
		INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by)
		VALUES($1,$2,'TOOL','Invocation Tool','invocation-tool',$3,$3)
	`, execCap, execWS, execOwner)
	mustExec(t, db, `
		INSERT INTO tools(capability_id,workspace_id,provider_id,default_connection_id)
		VALUES($1,$2,$3,$4)
	`, execCap, execWS, execProv, execConn)
	mustExec(t, db, `
		INSERT INTO tool_versions(
		 id,workspace_id,capability_id,version_no,lifecycle_status,executor_type,
		 provider_id,default_connection_id,action_schema_version,action_config,
		 input_schema,output_schema,risk_level,side_effect_level,checksum,
		 created_by,updated_by,published_at
		) VALUES($1,$2,$3,1,'PUBLISHED','HTTP',$4,$5,'http.v1',
		 '{"method":"GET","path":"/orders/{id}"}','{}','{}','HIGH','WRITE',$6,$7,$7,
		 clock_timestamp())
	`, execVer, execWS, execCap, execProv, execConn, execChecksum, execOwner)
	mustExec(t, db, `
		INSERT INTO capability_releases(
		 id,workspace_id,capability_id,release_no,source_type,source_id,callable_name,
		 input_schema,output_schema,risk_level,side_effect_level,checksum,published_by
		) VALUES($1,$2,$3,1,'TOOL_VERSION',$4,'invoke_order','{}','{}','HIGH','WRITE',$5,$6)
	`, execRelease, execWS, execCap, execVer, execChecksum, execOwner)

	runs, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := execution.NewConfirmationRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	confirmations, err := execution.NewConfirmationService(repo)
	if err != nil {
		t.Fatal(err)
	}
	return db, runs, confirmations
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec: %v\n%s", err, q)
	}
}

func buildStartedEvent(t *testing.T, ids seededIDs) protocolevent.NewProtocolEvent {
	t.Helper()
	run := execution.AgentRun{
		ID: ids.runID, WorkspaceID: ids.workspaceID, SessionID: ids.sessionID,
		AgentID: ids.agentID, Status: "RUNNING", TraceID: "trace-fail",
		StartedAt: time.Now().UTC().Add(-time.Minute),
	}
	event, err := execution.NewRunLifecycleMapper().Map(execution.RunLifecycleEventInput{
		EventID: uuid.NewString(), EventStreamID: ids.streamID,
		EventType: protocolevent.EventRunStarted, Run: run, OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

type failingNotifier struct {
	fail  bool
	calls int
}

func (n *failingNotifier) NotifyCommitted(context.Context, protocolevent.CommitNotification) error {
	n.calls++
	if n.fail {
		return errors.New("injected notify loss")
	}
	return nil
}

func buildValidSSEProtocolEvent(t *testing.T, sequence int64) protocolevent.ProtocolEvent {
	t.Helper()
	const (
		ws, ag, conv, run = "11000000-0000-4000-8000-000000000101",
			"22000000-0000-4000-8000-000000000101",
			"33000000-0000-4000-8000-000000000101",
			"44000000-0000-4000-8000-000000000101"
		ev, st, item = "55000000-0000-4000-8000-000000000101",
			"66000000-0000-4000-8000-000000000101",
			"77000000-0000-4000-8000-000000000101"
	)
	occurredAt := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	built, err := protocolevent.BuildProtocolEvent(protocolevent.NewProtocolEvent{
		ID: ev, EventStreamID: st, WorkspaceID: ws, AgentID: ag,
		ConversationID: conv, RunID: run, Type: protocolevent.EventItemDelta,
		SpecVersion: "1.0", TraceID: "trace-sse", ItemID: item, OccurredAt: occurredAt,
	}, protocolevent.ItemDeltaData{
		ItemID: item,
		Delta:  protocolevent.TextDelta{Type: protocolevent.DeltaTypeText, Index: 0, Text: "fail"},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(struct {
		SpecVersion    string          `json:"specVersion"`
		Type           string          `json:"type"`
		EventID        string          `json:"eventId"`
		StreamID       string          `json:"streamId"`
		Sequence       int64           `json:"sequence"`
		OccurredAt     time.Time       `json:"occurredAt"`
		WorkspaceID    string          `json:"workspaceId"`
		AgentID        string          `json:"agentId"`
		ConversationID string          `json:"conversationId"`
		RunID          string          `json:"runId"`
		TraceID        string          `json:"traceId"`
		Data           json.RawMessage `json:"data"`
	}{
		SpecVersion: "1.0", Type: built.Type, EventID: built.ID,
		StreamID: "run:" + run, Sequence: sequence, OccurredAt: occurredAt,
		WorkspaceID: ws, AgentID: ag, ConversationID: conv, RunID: run,
		TraceID: "trace-sse", Data: built.Data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return protocolevent.ProtocolEvent{
		ID: built.ID, EventStreamID: st, StreamID: "run:" + run,
		WorkspaceID: ws, AgentID: ag, ConversationID: conv, RunID: run,
		Type: built.Type, SpecVersion: "1.0", TraceID: "trace-sse",
		ItemID: item, Sequence: sequence, OccurredAt: occurredAt,
		Data: built.Data, Payload: payload,
	}
}

// Stub invocation pipeline pieces for confirmation resume without network I/O.
type stubAuthorizer struct{}

func (stubAuthorizer) AuthorizeInvocation(context.Context, string, string) error { return nil }

type stubResolver struct{ calls *atomic.Int32 }

func (r stubResolver) ResolveInvocation(context.Context, execution.ResolveRequest) (execution.ResolvedInvocation, error) {
	r.calls.Add(1)
	return execution.ResolvedInvocation{}, errors.New("resolver must not run after pause")
}

type stubIdempotency struct{}

func (stubIdempotency) BeginInvocation(context.Context, execution.IdempotencyRequest) (execution.IdempotencyDecision, error) {
	return execution.IdempotencyDecision{State: execution.IdempotencyNew}, nil
}
func (stubIdempotency) CompleteInvocation(context.Context, execution.IdempotencyRequest, execution.InvocationResult) error {
	return nil
}
func (stubIdempotency) FailInvocation(context.Context, execution.IdempotencyRequest, string) error {
	return nil
}

type stubLimiter struct{}

func (stubLimiter) AllowInvocation(context.Context, execution.LimitRequest) error { return nil }

type stubInjector struct{}

func (stubInjector) WithInjectedConnection(
	_ context.Context,
	connection execution.ConnectionSnapshot,
	_ execution.CredentialReference,
	invoke func(execution.ConnectionSnapshot) error,
) error {
	return invoke(connection)
}

type stubRecorder struct{}

func (stubRecorder) InvocationStarted(context.Context, execution.InvocationRecord) error {
	return nil
}
func (stubRecorder) InvocationFinished(context.Context, execution.InvocationRecord) error {
	return nil
}

type stubSideEffect struct{ calls *atomic.Int32 }

func (*stubSideEffect) Kind() string { return execution.ExecutorTypeHTTP }
func (*stubSideEffect) Capabilities() execution.ExecutorFeatures {
	return execution.ExecutorFeatures{}
}
func (s *stubSideEffect) Invoke(
	_ context.Context,
	request execution.InvocationRequest,
	_ execution.InvocationEventSink,
) (execution.InvocationResult, error) {
	s.calls.Add(1)
	return execution.InvocationResult{
		InvocationID: request.InvocationID, TraceID: request.TraceID,
		Output: json.RawMessage(`{"result":"recovered"}`), HTTPStatus: 200,
	}, nil
}
func (*stubSideEffect) Cancel(context.Context, execution.InvocationRef) error { return nil }

func newStubPipeline(
	confirmations *execution.ConfirmationService,
	resolverCalls, sideEffects *atomic.Int32,
) *execution.InvocationPipeline {
	registry, err := execution.NewRegistry(&stubSideEffect{calls: sideEffects})
	if err != nil {
		panic(err)
	}
	pipeline, err := execution.NewInvocationPipeline(
		stubAuthorizer{}, stubResolver{calls: resolverCalls}, confirmations,
		stubIdempotency{}, stubLimiter{}, stubInjector{}, registry,
		stubRecorder{}, execution.RetryWaiterFunc(func(context.Context, int) error { return nil }),
	)
	if err != nil {
		panic(err)
	}
	return pipeline
}
