package agentrun_test

import (
	"encoding/json"
	"sync"
	"testing"

	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/config"
)

type countingRuntime struct {
	mu        sync.Mutex
	enqueued  []agentrun.Job
	continued int
	cancelled int
}

func (c *countingRuntime) Enqueue(job agentrun.Job) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enqueued = append(c.enqueued, job)
}

func (c *countingRuntime) CancelRun(_, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelled++
	return nil
}

func (c *countingRuntime) EnqueueContinueWithLifecycle(
	_ agentrun.Job, _, _ json.RawMessage, _ agentrun.ContinueLifecycle,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.continued++
}

func TestFactory_AlwaysEinoEvenWhenEnabledFalse(t *testing.T) {
	t.Parallel()
	eino := &countingRuntime{}
	factory, err := agentrun.NewFactory(config.RuntimeFeatureRollout{
		Enabled:            false,
		AllowAllWorkspaces: true,
	}, eino)
	if err != nil {
		t.Fatal(err)
	}
	factory.Enqueue(agentrun.Job{
		WorkspaceID: "ws-1", SessionID: "s", RunID: "r",
		UserMessageID: "m", ActorID: "a",
	})
	if len(eino.enqueued) != 1 {
		t.Fatalf("eino enqueued = %d, want 1", len(eino.enqueued))
	}
}

func TestFactory_AlwaysEinoIgnoresAllowlistForRouting(t *testing.T) {
	t.Parallel()
	eino := &countingRuntime{}
	factory, err := agentrun.NewFactory(config.RuntimeFeatureRollout{
		Enabled:      true,
		WorkspaceIDs: []string{"ws-allowed"},
	}, eino)
	if err != nil {
		t.Fatal(err)
	}
	factory.Enqueue(agentrun.Job{
		WorkspaceID: "ws-allowed", SessionID: "s", RunID: "r1",
		UserMessageID: "m", ActorID: "a",
	})
	factory.Enqueue(agentrun.Job{
		WorkspaceID: "ws-other", SessionID: "s", RunID: "r2",
		UserMessageID: "m", ActorID: "a",
	})
	if len(eino.enqueued) != 2 {
		t.Fatalf("eino enqueued = %+v, want both workspaces on eino", eino.enqueued)
	}
	if !factory.AllowsWorkspace("ws-allowed") {
		t.Fatal("AllowsWorkspace should still report config allowlist")
	}
	if factory.AllowsWorkspace("ws-other") {
		t.Fatal("ws-other must not be allowlisted in config")
	}
}

func TestFactory_RequiresEino(t *testing.T) {
	t.Parallel()
	if _, err := agentrun.NewFactory(config.RuntimeFeatureRollout{
		Enabled: true, AllowAllWorkspaces: true,
	}, nil); err == nil {
		t.Fatal("expected error when eino is nil")
	}
}

func TestFactory_ContinueAlwaysEino(t *testing.T) {
	t.Parallel()
	eino := &countingRuntime{}
	factory, err := agentrun.NewFactory(config.RuntimeFeatureRollout{
		Enabled: false,
	}, eino)
	if err != nil {
		t.Fatal(err)
	}
	factory.EnqueueContinueWithLifecycle(
		agentrun.Job{WorkspaceID: "ws", SessionID: "s", RunID: "r", UserMessageID: "m", ActorID: "a"},
		json.RawMessage(`{}`), json.RawMessage(`{}`), nil,
	)
	if eino.continued != 1 {
		t.Fatalf("continued eino=%d", eino.continued)
	}
}

func TestFactory_CancelEino(t *testing.T) {
	t.Parallel()
	eino := &countingRuntime{}
	factory, err := agentrun.NewFactory(config.RuntimeFeatureRollout{Enabled: true, AllowAllWorkspaces: true}, eino)
	if err != nil {
		t.Fatal(err)
	}
	if err := factory.CancelRun("ws", "run"); err != nil {
		t.Fatal(err)
	}
	if eino.cancelled != 1 {
		t.Fatalf("eino cancelled = %d", eino.cancelled)
	}
}
