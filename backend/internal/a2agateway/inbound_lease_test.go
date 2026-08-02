package a2agateway_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

// Concurrent + replay: only one ClaimInboundExecution owns model dispatch.
func TestInbound_ConcurrentExecutionLease_ModelOnce(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()

	runRepo, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	a2aRepo, err := a2agateway.NewRepository(db)
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

	// Minimal workspace/agent/exposure for ClaimInboundTask FK.
	fx := seedA2AAuditFixture(t, db)
	expID := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO agent_a2a_exposures(
			id, workspace_id, agent_id, public_name, public_description,
			enabled, auth_mode, version, created_by, updated_by
		) VALUES ($1,$2,$3,'pub','d',true,'AGENT_ACCESS',1,$4,$4)
	`, expID, fx.workspaceID, fx.agentA, fx.ownerID); err != nil {
		t.Fatal(err)
	}

	var executes atomic.Int64
	runner := &a2agateway.DurableInboundRunner{
		Runs:    runRepo,
		Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			executes.Add(1)
			time.Sleep(80 * time.Millisecond) // hold ownership window
			return "hello-once", nil
		},
	}
	gw, err := a2agateway.NewInboundGateway(a2aRepo, audit, runner, "http://127.0.0.1", a2agateway.HeaderPresenceAuth{})
	if err != nil {
		t.Fatal(err)
	}
	_ = gw

	// Simulate the critical lease path used by inboundExecutor (not full JSON-RPC).
	extKey := "task-lease-1"
	req := a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "hi",
		ExternalTaskID: "task-1", IdempotencyKey: extKey, ActorType: "USER", ActorID: fx.ownerID,
		TraceID: uuid.Must(uuid.NewV7()).String(),
	}

	// Prepare + claim task once.
	runID, err := runner.PrepareRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := a2aRepo.ClaimInboundTask(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: extKey, ExternalTaskID: "task-1", RunID: runID, Status: "RUNNING",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Prewrite audit once.
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	idem := agentdelegation.IdempotencyKey(expID+":"+extKey, "inbound", 1, expID)
	del, _, err := audit.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: fx.workspaceID, ParentRunID: runID,
		CallerAgentID: fx.agentA, Mode: agentdelegation.ModeTask,
		Protocol: agentdelegation.ProtocolA2A, Origin: agentdelegation.OriginExternal,
		Depth: 0, BindingVersion: 1, ToolCallID: "inbound", IdempotencyKey: idem,
		InputSummary: json.RawMessage(`{}`), InputPayload: json.RawMessage(`{}`),
		StepID: stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = a2aRepo.BindInboundTaskDelegation(ctx, fx.workspaceID, task.ID, del.ID)

	// Concurrent claim+execute: only first owns.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, cerr := a2aRepo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "w", 2*time.Minute)
			if cerr != nil || !lease.Owned {
				return
			}
			_, _ = runner.ExecuteRun(ctx, req, runID)
			_ = a2aRepo.MarkInboundExecutionFinished(ctx, fx.workspaceID, task.ID, agentdelegation.StatusSucceeded, lease.Owner, lease.Token)
			_, _ = audit.FinalizeDelegation(ctx, agentdelegation.FinalizeDelegationInput{
				WorkspaceID: fx.workspaceID, DelegationID: del.ID, StepID: stepID,
				Status:        agentdelegation.StatusSucceeded,
				OutputSummary: json.RawMessage(`{"ok":true}`),
				OutputPayload: json.RawMessage(`{"result":"hello-once"}`),
			})
		}()
	}
	wg.Wait()

	if executes.Load() != 1 {
		t.Fatalf("model executes=%d want 1", executes.Load())
	}

	// Replay claim after finished must not re-execute.
	lease2, err := a2aRepo.ClaimInboundExecution(ctx, fx.workspaceID, task.ID, "w2", 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease2.Owned {
		t.Fatal("finished task must not re-claim execution")
	}
	if executes.Load() != 1 {
		t.Fatalf("after replay executes=%d", executes.Load())
	}

	// Single run + single delegation.
	var runCount, delCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE workspace_id=$1 AND agent_id=$2 AND trigger_type='A2A_INBOUND'`,
		fx.workspaceID, fx.agentA).Scan(&runCount)
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_run_delegations WHERE workspace_id=$1 AND parent_run_id=$2`,
		fx.workspaceID, runID).Scan(&delCount)
	if runCount != 1 || delCount != 1 {
		t.Fatalf("runs=%d dels=%d want 1/1", runCount, delCount)
	}
}

// Candidate cancel after taskReplay must leave CANCELLED without audit pollution.
func TestInbound_CandidateCancel_NoAuditPollution(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)
	runRepo, _ := execution.NewRunRepository(db)
	a2aRepo, _ := a2agateway.NewRepository(db)
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
		Runs:    runRepo,
		Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(context.Context, a2agateway.InboundRunRequest, string) (string, error) {
			return "ok", nil
		},
	}
	req := a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "x",
		ExternalTaskID: "t1", IdempotencyKey: "k1", ActorType: "USER", ActorID: fx.ownerID,
		TraceID: uuid.Must(uuid.NewV7()).String(),
	}
	first, err := runner.PrepareRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = a2aRepo.ClaimInboundTask(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "k1", ExternalTaskID: "t1", RunID: first, Status: "RUNNING",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Second prepare (replay path): candidate cancelled, mapping keeps first run.
	candidate, err := runner.PrepareRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	task, replay, err := a2aRepo.ClaimInboundTask(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: expID, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "k1", ExternalTaskID: "t1", RunID: candidate, Status: "RUNNING",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !replay || task.RunID != first {
		t.Fatalf("replay=%v run=%s want first=%s", replay, task.RunID, first)
	}
	if candidate != first {
		if err := runner.CancelRun(ctx, fx.workspaceID, candidate); err != nil {
			t.Fatalf("cancel candidate: %v", err)
		}
		cand, err := runRepo.GetAgentRun(ctx, fx.workspaceID, candidate)
		if err != nil {
			t.Fatal(err)
		}
		if cand.Status != "CANCELLED" {
			t.Fatalf("candidate status=%s", cand.Status)
		}
	} else {
		t.Fatal("expected distinct candidate run on replay prepare")
	}
	// No AGENT_DELEGATION steps for candidate (never prewrote).
	var steps int
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_run_steps WHERE run_id=$1`, candidate).Scan(&steps)
	if steps != 0 {
		t.Fatalf("candidate audit pollution steps=%d", steps)
	}
}

type staticFreezer struct {
	agentID, modelID string
}

func (f staticFreezer) FreezeInbound(context.Context, string, string) (a2agateway.InboundFreeze, error) {
	model, _ := json.Marshal(map[string]any{
		"id": f.modelID, "provider": "openai", "apiBase": "https://example.test",
		"modelName": "m", "lockVersion": 1,
	})
	agent, _ := json.Marshal(map[string]any{
		"schemaVersion": "agent-binding.v1", "agentId": f.agentID,
		"modelConfigId": f.modelID, "modelConfigLockVer": 1,
	})
	cap := json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[]}`)
	// Full v1 integrity: root node + remotesFrozen + explicit empty remotes key.
	graph, _ := json.Marshal(map[string]any{
		"schemaVersion": "agent_graph_snapshot.v1",
		"rootAgentId":   f.agentID,
		"maxDepth":      4, "maxTotalDelegations": 20, "maxPerBinding": 5,
		"nodes": []map[string]any{{
			"agentId": f.agentID, "modelConfigId": f.modelID, "modelConfigLockVersion": 1,
			"modelSnapshot": model, "agentSnapshot": agent, "capabilitySnapshot": cap, "depth": 0,
		}},
		"edges":                 []any{},
		"remotesFrozen":         true,
		"frozenRemotesByCaller": map[string]any{f.agentID: []any{}},
	})
	return a2agateway.InboundFreeze{
		ModelSnapshot:      model,
		AgentSnapshot:      agent,
		CapabilitySnapshot: cap,
		ContextPolicy:      json.RawMessage(`{"schemaVersion":"session-context.v1","mode":"LEGACY"}`),
		GraphSnapshot:      graph,
	}, nil
}
