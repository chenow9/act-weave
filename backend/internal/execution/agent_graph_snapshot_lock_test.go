package execution_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

// TestSetAgentGraphSnapshotIfEmpty_BumpsLockVersion reproduces the Chrome
// acceptance failure: freeze-on-first-dispatch must UPDATE agent_runs without
// tripping enforce_agent_run_permanent_snapshot (lock_version must +1).
func TestSetAgentGraphSnapshotIfEmpty_BumpsLockVersion(t *testing.T) {
	repo, svc, _, _ := newRunStateTest(t)
	ctx := context.Background()
	runID := uuid.Must(uuid.NewV7()).String()
	run, err := svc.StartAgentRun(ctx, runStateAgentRequest(runID, "trace-graph-freeze"))
	if err != nil {
		t.Fatal(err)
	}
	if run.LockVersion != 1 {
		t.Fatalf("start lock=%d want 1", run.LockVersion)
	}
	// Fresh chat runs start with empty graph; freeze happens later in bridge drive.
	if len(run.AgentGraphSnapshot) > 0 && string(run.AgentGraphSnapshot) != "{}" {
		t.Fatalf("expected empty graph at start, got %s", run.AgentGraphSnapshot)
	}

	snap := json.RawMessage(`{
		"schemaVersion":"agent_graph_snapshot.v1",
		"rootAgentId":"` + run.AgentID + `",
		"maxDepth":4,"maxTotal":20,"maxPerBinding":5,
		"nodes":[{"agentId":"` + run.AgentID + `","depth":0,"name":"root"}],
		"edges":[{"bindingId":"b1","callableName":"call_child","callerAgentId":"` + run.AgentID + `",
			"targetAgentId":"` + run.AgentID + `","mode":"TASK","version":1}],
		"builtAt":"2026-08-02T00:00:00Z",
		"remotesFrozen":true,
		"frozenRemotesByCaller":{}
	}`)
	if err := repo.SetAgentGraphSnapshotIfEmpty(ctx, run.WorkspaceID, run.ID, snap); err != nil {
		t.Fatalf("SetAgentGraphSnapshotIfEmpty (root cause of persist agent graph snapshot / run state conflict): %v", err)
	}

	after, err := repo.GetAgentRun(ctx, run.WorkspaceID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LockVersion != 2 {
		t.Fatalf("lock_version=%d want 2 after graph freeze write", after.LockVersion)
	}
	if len(after.AgentGraphSnapshot) == 0 || string(after.AgentGraphSnapshot) == "{}" {
		t.Fatal("graph snapshot still empty after freeze")
	}
	if !strings.Contains(string(after.AgentGraphSnapshot), "call_child") {
		t.Fatalf("snapshot missing edge: %s", after.AgentGraphSnapshot)
	}

	// Idempotent same topology (builtAt may differ) must not fail or double-bump.
	snap2 := json.RawMessage(`{
		"schemaVersion":"agent_graph_snapshot.v1",
		"rootAgentId":"` + run.AgentID + `",
		"maxDepth":4,"maxTotal":20,"maxPerBinding":5,
		"nodes":[{"agentId":"` + run.AgentID + `","depth":0,"name":"root"}],
		"edges":[{"bindingId":"b1","callableName":"call_child","callerAgentId":"` + run.AgentID + `",
			"targetAgentId":"` + run.AgentID + `","mode":"TASK","version":1}],
		"builtAt":"2026-08-02T12:00:00Z",
		"remotesFrozen":true,
		"frozenRemotesByCaller":{}
	}`)
	if err := repo.SetAgentGraphSnapshotIfEmpty(ctx, run.WorkspaceID, run.ID, snap2); err != nil {
		t.Fatalf("idempotent freeze: %v", err)
	}
	again, _ := repo.GetAgentRun(ctx, run.WorkspaceID, run.ID)
	if again.LockVersion != 2 {
		t.Fatalf("idempotent equal topology must not bump lock again: lock=%d", again.LockVersion)
	}

	// Different frozen content → conflict (immutability).
	diff := json.RawMessage(`{
		"schemaVersion":"agent_graph_snapshot.v1",
		"rootAgentId":"` + run.AgentID + `",
		"nodes":[],"edges":[],
		"builtAt":"2026-08-02T00:00:00Z",
		"remotesFrozen":true,"frozenRemotesByCaller":{}
	}`)
	if err := repo.SetAgentGraphSnapshotIfEmpty(ctx, run.WorkspaceID, run.ID, diff); !errors.Is(err, execution.ErrRunConflict) {
		t.Fatalf("want ErrRunConflict on different freeze, got %v", err)
	}

	// Status transition still works with post-freeze lock.
	if _, err := repo.TransitionAgentRun(ctx, run.WorkspaceID, run.ID, execution.RunTransition{
		ExpectedStatus: "RUNNING", ExpectedLockVersion: again.LockVersion,
		NewStatus: "SUCCEEDED", OutputSummary: json.RawMessage(`{"ok":true}`),
	}); err != nil {
		t.Fatalf("transition after freeze: %v", err)
	}
}

// TestSetAgentGraphSnapshotIfEmpty_EmptyToValue_RealBridgeStartShape mirrors a
// brand-new /chat session: StartAgentRun leaves graph {}, freeze writes once.
func TestSetAgentGraphSnapshotIfEmpty_EmptyToValue_RealBridgeStartShape(t *testing.T) {
	repo, svc, db, _ := newRunStateTest(t)
	ctx := context.Background()
	runID := uuid.Must(uuid.NewV7()).String()
	run, err := svc.StartAgentRun(ctx, runStateAgentRequest(runID, "trace-new-session"))
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRow(`SELECT agent_graph_snapshot::text FROM agent_runs WHERE id=$1`, run.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "{}" {
		t.Fatalf("DB graph at start=%s want {}", raw)
	}
	// Without lock_version+1 this exact UPDATE historically raised 40001 → run state conflict.
	snap := json.RawMessage(`{"schemaVersion":"agent_graph_snapshot.v1","rootAgentId":"` + run.AgentID + `","nodes":[],"edges":[{"bindingId":"x","callableName":"call_audit_child_b","callerAgentId":"` + run.AgentID + `","targetAgentId":"` + run.AgentID + `","mode":"TASK","version":1}],"builtAt":"2026-08-02T00:00:00.000000001Z","remotesFrozen":true,"frozenRemotesByCaller":{}}`)
	if err := repo.SetAgentGraphSnapshotIfEmpty(ctx, run.WorkspaceID, run.ID, snap); err != nil {
		t.Fatalf("new-session freeze must succeed: %v", err)
	}
	var lock int64
	var graph string
	if err := db.QueryRow(`SELECT lock_version, agent_graph_snapshot::text FROM agent_runs WHERE id=$1`, run.ID).
		Scan(&lock, &graph); err != nil {
		t.Fatal(err)
	}
	if lock != 2 {
		t.Fatalf("lock=%d want 2", lock)
	}
	if !strings.Contains(graph, "call_audit_child_b") {
		t.Fatalf("graph=%s", graph)
	}
}
