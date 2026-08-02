package chatruntimebridge_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

func TestStartChild_MalformedParentGraph_FailClosed(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedChildFixture(t, db)
	runRepo, _ := execution.NewRunRepository(db)

	parentID := uuid.Must(uuid.NewV7()).String()
	if _, err := runRepo.StartAgentRun(ctx, execution.StartAgentRunInput{
		ID: parentID, WorkspaceID: fx.ws, AgentID: fx.agentA,
		TriggerType: "MANUAL", TriggeredByType: "USER", TriggeredByID: fx.owner,
		TraceID: parentID,
		Snapshots: execution.AgentRunSnapshots{
			SchemaVersion: execution.RunSnapshotSchemaV2,
			Model:         json.RawMessage(`{"id":"` + fx.model + `"}`),
			Capabilities:  json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[]}`),
			ContextPolicy: json.RawMessage(`{}`),
			Agent:         json.RawMessage(`{"schemaVersion":"agent-binding.v1","agentId":"` + fx.agentA + `"}`),
		},
		AuthorizationSnapshot: json.RawMessage(`{}`),
		InputSummary:          json.RawMessage(`{}`),
		// Non-empty malformed v1 (missing remotesFrozen / incomplete nodes).
		AgentGraphSnapshot: json.RawMessage(`{"schemaVersion":"agent_graph_snapshot.v1","rootAgentId":"` + fx.agentA + `","nodes":[]}`),
	}); err != nil {
		t.Fatal(err)
	}

	adapter := &chatruntimebridge.ChildRunStoreAdapter{
		Runs: runRepo,
		GetParent: func(ctx context.Context, workspaceID, parentRunID string) (execution.AgentRun, error) {
			return runRepo.GetAgentRun(ctx, workspaceID, parentRunID)
		},
	}
	_, err := adapter.StartChild(ctx, agentdelegation.ChildRunStartInput{
		WorkspaceID: fx.ws, ParentRunID: parentID, ParentDelegationID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: fx.agentB, TriggeredByType: "USER", TriggeredByID: fx.owner,
	})
	if err == nil {
		t.Fatal("malformed parent graph must fail closed")
	}
}

func TestStartChild_GetParentFailure_FailClosed(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	runRepo, _ := execution.NewRunRepository(db)
	adapter := &chatruntimebridge.ChildRunStoreAdapter{
		Runs: runRepo,
		GetParent: func(ctx context.Context, workspaceID, parentRunID string) (execution.AgentRun, error) {
			return execution.AgentRun{}, errors.New("inject: parent load failed")
		},
	}
	_, err := adapter.StartChild(context.Background(), agentdelegation.ChildRunStartInput{
		WorkspaceID: uuid.Must(uuid.NewV7()).String(), ParentRunID: uuid.Must(uuid.NewV7()).String(),
		ParentDelegationID: uuid.Must(uuid.NewV7()).String(), TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		ModelSnapshot:      json.RawMessage(`{"id":"m"}`),
		AgentSnapshot:      json.RawMessage(`{"schemaVersion":"agent-binding.v1"}`),
		CapabilitySnapshot: json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[]}`),
	})
	if err == nil {
		t.Fatal("GetParent failure must fail closed")
	}
}

func TestStartChild_NilGetParent_FailClosed(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	runRepo, _ := execution.NewRunRepository(db)
	adapter := &chatruntimebridge.ChildRunStoreAdapter{Runs: runRepo, GetParent: nil}
	_, err := adapter.StartChild(context.Background(), agentdelegation.ChildRunStartInput{
		WorkspaceID: uuid.Must(uuid.NewV7()).String(), ParentRunID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		ModelSnapshot: json.RawMessage(`{"id":"m"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "GetParent") {
		t.Fatalf("nil GetParent must fail closed; err=%v", err)
	}
}

func TestStartChild_EmptyParentGraph_FailClosed(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedChildFixture(t, db)
	runRepo, _ := execution.NewRunRepository(db)
	parentID := uuid.Must(uuid.NewV7()).String()
	if _, err := runRepo.StartAgentRun(ctx, execution.StartAgentRunInput{
		ID: parentID, WorkspaceID: fx.ws, AgentID: fx.agentA,
		TriggerType: "MANUAL", TriggeredByType: "USER", TriggeredByID: fx.owner, TraceID: parentID,
		Snapshots: execution.AgentRunSnapshots{
			SchemaVersion: execution.RunSnapshotSchemaV2,
			Model:         json.RawMessage(`{"id":"` + fx.model + `"}`),
			Capabilities:  json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[]}`),
			ContextPolicy: json.RawMessage(`{}`),
			Agent:         json.RawMessage(`{"schemaVersion":"agent-binding.v1","agentId":"` + fx.agentA + `"}`),
		},
		AuthorizationSnapshot: json.RawMessage(`{}`), InputSummary: json.RawMessage(`{}`),
		AgentGraphSnapshot: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	adapter := &chatruntimebridge.ChildRunStoreAdapter{
		Runs: runRepo,
		GetParent: func(ctx context.Context, w, p string) (execution.AgentRun, error) {
			return runRepo.GetAgentRun(ctx, w, p)
		},
	}
	_, err := adapter.StartChild(ctx, agentdelegation.ChildRunStartInput{
		WorkspaceID: fx.ws, ParentRunID: parentID, TargetAgentID: fx.agentB,
		ModelSnapshot: json.RawMessage(`{"id":"x"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "parent agent_graph_snapshot") {
		t.Fatalf("empty parent graph must fail; err=%v", err)
	}
}

func TestStartChild_CallerGraphMismatch_FailClosed(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedChildFixture(t, db)
	runRepo, _ := execution.NewRunRepository(db)
	modelFrozen := json.RawMessage(`{"id":"` + fx.model + `","marker":"P"}`)
	agentFrozen := json.RawMessage(`{"schemaVersion":"agent-binding.v1","agentId":"` + fx.agentB + `"}`)
	capFrozen := json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[]}`)
	graph, _ := json.Marshal(map[string]any{
		"schemaVersion": "agent_graph_snapshot.v1", "rootAgentId": fx.agentA,
		"maxDepth": 4, "maxTotalDelegations": 20, "maxPerBinding": 5,
		"nodes": []map[string]any{
			{"agentId": fx.agentA, "depth": 0, "modelConfigId": fx.model, "modelSnapshot": modelFrozen, "agentSnapshot": agentFrozen, "capabilitySnapshot": capFrozen},
			{"agentId": fx.agentB, "depth": 1, "modelConfigId": fx.model, "modelSnapshot": modelFrozen, "agentSnapshot": agentFrozen, "capabilitySnapshot": capFrozen},
		},
		"edges": []map[string]any{{
			"bindingId": uuid.Must(uuid.NewV7()).String(), "callerAgentId": fx.agentA, "targetAgentId": fx.agentB,
			"callableName": "call_b", "mode": "TASK", "contextPolicy": "TASK_ONLY", "version": 1, "protocol": "INTERNAL",
		}},
		"remotesFrozen": true, "frozenRemotesByCaller": map[string]any{fx.agentA: []any{}, fx.agentB: []any{}},
	})
	parentID := uuid.Must(uuid.NewV7()).String()
	if _, err := runRepo.StartAgentRun(ctx, execution.StartAgentRunInput{
		ID: parentID, WorkspaceID: fx.ws, AgentID: fx.agentA,
		TriggerType: "MANUAL", TriggeredByType: "USER", TriggeredByID: fx.owner, TraceID: parentID,
		Snapshots: execution.AgentRunSnapshots{
			SchemaVersion: execution.RunSnapshotSchemaV2,
			Model:         modelFrozen, Capabilities: capFrozen, ContextPolicy: json.RawMessage(`{}`), Agent: agentFrozen,
		},
		AuthorizationSnapshot: json.RawMessage(`{}`), InputSummary: json.RawMessage(`{}`),
		AgentGraphSnapshot: graph,
	}); err != nil {
		t.Fatal(err)
	}
	adapter := &chatruntimebridge.ChildRunStoreAdapter{
		Runs: runRepo,
		GetParent: func(ctx context.Context, w, p string) (execution.AgentRun, error) {
			return runRepo.GetAgentRun(ctx, w, p)
		},
	}
	_, err := adapter.StartChild(ctx, agentdelegation.ChildRunStartInput{
		WorkspaceID: fx.ws, ParentRunID: parentID, TargetAgentID: fx.agentB,
		GraphSnapshot: json.RawMessage(`{"schemaVersion":"agent_graph_snapshot.v1","rootAgentId":"other","nodes":[],"remotesFrozen":true,"frozenRemotesByCaller":{}}`),
	})
	if err == nil || !strings.Contains(err.Error(), "mismatches parent") {
		t.Fatalf("caller graph mismatch must fail; err=%v", err)
	}
}

func TestStartChild_TargetMissingFromParentGraph_FailClosed(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedChildFixture(t, db)
	runRepo, _ := execution.NewRunRepository(db)
	modelFrozen := json.RawMessage(`{"id":"` + fx.model + `","marker":"P"}`)
	agentFrozen := json.RawMessage(`{"schemaVersion":"agent-binding.v1","agentId":"` + fx.agentA + `"}`)
	capFrozen := json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[]}`)
	// Graph only has root A — not agentB.
	graph, _ := json.Marshal(map[string]any{
		"schemaVersion": "agent_graph_snapshot.v1", "rootAgentId": fx.agentA,
		"maxDepth": 4, "maxTotalDelegations": 20, "maxPerBinding": 5,
		"nodes": []map[string]any{{
			"agentId": fx.agentA, "depth": 0, "modelConfigId": fx.model,
			"modelSnapshot": modelFrozen, "agentSnapshot": agentFrozen, "capabilitySnapshot": capFrozen,
		}},
		"edges": []any{}, "remotesFrozen": true,
		"frozenRemotesByCaller": map[string]any{fx.agentA: []any{}},
	})
	parentID := uuid.Must(uuid.NewV7()).String()
	if _, err := runRepo.StartAgentRun(ctx, execution.StartAgentRunInput{
		ID: parentID, WorkspaceID: fx.ws, AgentID: fx.agentA,
		TriggerType: "MANUAL", TriggeredByType: "USER", TriggeredByID: fx.owner, TraceID: parentID,
		Snapshots: execution.AgentRunSnapshots{
			SchemaVersion: execution.RunSnapshotSchemaV2,
			Model:         modelFrozen, Capabilities: capFrozen, ContextPolicy: json.RawMessage(`{}`), Agent: agentFrozen,
		},
		AuthorizationSnapshot: json.RawMessage(`{}`), InputSummary: json.RawMessage(`{}`),
		AgentGraphSnapshot: graph,
	}); err != nil {
		t.Fatal(err)
	}
	adapter := &chatruntimebridge.ChildRunStoreAdapter{
		Runs: runRepo,
		GetParent: func(ctx context.Context, w, p string) (execution.AgentRun, error) {
			return runRepo.GetAgentRun(ctx, w, p)
		},
	}
	// Caller supplies full snapshots for B — must still fail (no fallback).
	_, err := adapter.StartChild(ctx, agentdelegation.ChildRunStartInput{
		WorkspaceID: fx.ws, ParentRunID: parentID, TargetAgentID: fx.agentB,
		ModelSnapshot: modelFrozen, AgentSnapshot: agentFrozen, CapabilitySnapshot: capFrozen,
	})
	if err == nil || !strings.Contains(err.Error(), "not in parent graph") {
		t.Fatalf("missing target node must fail; err=%v", err)
	}
}

func TestStartChild_UsesExactFrozenNodeBytes(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedChildFixture(t, db)
	runRepo, _ := execution.NewRunRepository(db)

	modelFrozen := json.RawMessage(`{"id":"` + fx.model + `","provider":"openai","apiBase":"https://frozen.example","modelName":"frozen-model","lockVersion":7,"marker":"FROZEN_MODEL"}`)
	agentFrozen := json.RawMessage(`{"schemaVersion":"agent-binding.v1","agentId":"` + fx.agentB + `","name":"FrozenB","modelConfigId":"` + fx.model + `","modelConfigLockVer":7,"marker":"FROZEN_AGENT"}`)
	capFrozen := json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[],"marker":"FROZEN_CAP"}`)
	graph, _ := json.Marshal(map[string]any{
		"schemaVersion": "agent_graph_snapshot.v1",
		"rootAgentId":   fx.agentA,
		"maxDepth":      4, "maxTotalDelegations": 20, "maxPerBinding": 5,
		"nodes": []map[string]any{
			{
				"agentId": fx.agentA, "depth": 0, "modelConfigId": fx.model,
				"modelSnapshot": modelFrozen, "agentSnapshot": agentFrozen, "capabilitySnapshot": capFrozen,
			},
			{
				"agentId": fx.agentB, "depth": 1, "modelConfigId": fx.model,
				"modelSnapshot": modelFrozen, "agentSnapshot": agentFrozen, "capabilitySnapshot": capFrozen,
			},
		},
		"edges": []map[string]any{{
			"bindingId": uuid.Must(uuid.NewV7()).String(), "callerAgentId": fx.agentA, "targetAgentId": fx.agentB,
			"callableName": "call_b", "mode": "TASK", "contextPolicy": "TASK_ONLY", "version": 1, "protocol": "INTERNAL",
		}},
		"remotesFrozen":         true,
		"frozenRemotesByCaller": map[string]any{fx.agentA: []any{}, fx.agentB: []any{}},
	})

	parentID := uuid.Must(uuid.NewV7()).String()
	if _, err := runRepo.StartAgentRun(ctx, execution.StartAgentRunInput{
		ID: parentID, WorkspaceID: fx.ws, AgentID: fx.agentA,
		TriggerType: "MANUAL", TriggeredByType: "USER", TriggeredByID: fx.owner,
		TraceID: parentID,
		Snapshots: execution.AgentRunSnapshots{
			SchemaVersion: execution.RunSnapshotSchemaV2,
			Model:         modelFrozen, Capabilities: capFrozen, ContextPolicy: json.RawMessage(`{}`), Agent: agentFrozen,
		},
		AuthorizationSnapshot: json.RawMessage(`{}`),
		InputSummary:          json.RawMessage(`{}`),
		AgentGraphSnapshot:    graph,
	}); err != nil {
		t.Fatal(err)
	}

	// Live drift must not affect frozen child bytes.
	if _, err := db.Exec(`UPDATE model_configs SET model_name='live-drift', lock_version=99 WHERE id=$1`, fx.model); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agents SET name='LiveDriftB' WHERE id=$1`, fx.agentB); err != nil {
		t.Fatal(err)
	}

	// Real parent delegation row for FK (TASK child parent_delegation_id).
	delRepo, _ := agentdelegation.NewRepository(db)
	audit, _ := agentdelegation.NewService(delRepo)
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	target := fx.agentB
	_, _, err := audit.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: fx.ws, ParentRunID: parentID,
		CallerAgentID: fx.agentA, TargetAgentID: &target,
		Mode: agentdelegation.ModeInline, Protocol: agentdelegation.ProtocolInternal,
		Origin: agentdelegation.OriginInternal,
		Depth:  1, BindingVersion: 1, ToolCallID: "call_b",
		IdempotencyKey: agentdelegation.IdempotencyKey(parentID, "call_b", 1, ""),
		InputSummary:   json.RawMessage(`{}`), InputPayload: json.RawMessage(`{}`),
		StepID: stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatal(err)
	}

	adapter := &chatruntimebridge.ChildRunStoreAdapter{
		Runs: runRepo,
		GetParent: func(ctx context.Context, workspaceID, parentRunID string) (execution.AgentRun, error) {
			return runRepo.GetAgentRun(ctx, workspaceID, parentRunID)
		},
	}
	_, err = adapter.StartChild(ctx, agentdelegation.ChildRunStartInput{
		WorkspaceID: fx.ws, ParentRunID: parentID, ParentDelegationID: delID,
		TargetAgentID: fx.agentB, TriggeredByType: "USER", TriggeredByID: fx.owner,
		ModelSnapshot: json.RawMessage(`{"id":"` + fx.model + `","marker":"WRONG"}`),
	})
	if err == nil {
		t.Fatal("input/node mismatch must fail")
	}

	childID, err := adapter.StartChild(ctx, agentdelegation.ChildRunStartInput{
		WorkspaceID: fx.ws, ParentRunID: parentID, ParentDelegationID: delID,
		TargetAgentID: fx.agentB, TriggeredByType: "USER", TriggeredByID: fx.owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := runRepo.GetAgentRun(ctx, fx.ws, childID)
	if err != nil {
		t.Fatal(err)
	}
	// Compare canonical JSON (StartAgentRun may re-order keys).
	if !jsonSemanticEqual(child.ModelSnapshot, modelFrozen) {
		t.Fatalf("model freeze not exact:\n got %s\nwant %s", child.ModelSnapshot, modelFrozen)
	}
	if !jsonSemanticEqual(child.AgentSnapshot, agentFrozen) {
		t.Fatalf("agent freeze not exact:\n got %s\nwant %s", child.AgentSnapshot, agentFrozen)
	}
	if !jsonSemanticEqual(child.CapabilitySnapshot, capFrozen) {
		t.Fatalf("cap freeze not exact")
	}
	if !bytesContain(child.ModelSnapshot, []byte("FROZEN_MODEL")) {
		t.Fatal("missing frozen marker")
	}
	if bytesContain(child.ModelSnapshot, []byte("live-drift")) {
		t.Fatal("live drift leaked into child")
	}
}

func bytesContain(b, sub []byte) bool {
	if len(sub) == 0 {
		return true
	}
	s, p := string(b), string(sub)
	for i := 0; i+len(p) <= len(s); i++ {
		if s[i:i+len(p)] == p {
			return true
		}
	}
	return false
}

func jsonSemanticEqual(a, b json.RawMessage) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return string(a) == string(b)
	}
	ba, _ := json.Marshal(va)
	bb, _ := json.Marshal(vb)
	return string(ba) == string(bb)
}

type childFx struct{ owner, ws, model, agentA, agentB string }

func seedChildFixture(t *testing.T, db *sql.DB) childFx {
	t.Helper()
	fx := childFx{
		owner: uuid.Must(uuid.NewV7()).String(), ws: uuid.Must(uuid.NewV7()).String(),
		model:  uuid.Must(uuid.NewV7()).String(),
		agentA: uuid.Must(uuid.NewV7()).String(), agentB: uuid.Must(uuid.NewV7()).String(),
	}
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	exec(`INSERT INTO users(id,username,display_name) VALUES($1,'child.o','C')`, fx.owner)
	exec(`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,$2,'C','SANDBOX',$3,$3,$3)`, fx.ws, "ch-"+fx.ws[:8], fx.owner)
	exec(`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'m','openai','https://x','m',$3,$3)`, fx.model, fx.ws, fx.owner)
	exec(`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'A',$3,$4,$4),($5,$2,'B',$3,$4,$4)`, fx.agentA, fx.ws, fx.model, fx.owner, fx.agentB)
	return fx
}
