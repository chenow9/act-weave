package smartdag

import (
	"context"
	"errors"
	"testing"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/modelconfig"
)

const (
	testAgentID       = "118f1f2e-7b5a-7c3d-8e9f-123456789011"
	testModelConfigID = "118f1f2e-7b5a-7c3d-8e9f-123456789012"
	testOtherWS       = "118f1f2e-7b5a-7c3d-8e9f-123456789099"
)

func TestAgentModelGateRequiresUsableModel(t *testing.T) {
	t.Parallel()
	agents := &fakeAgentLookup{byKey: map[string]agent.Agent{
		testWorkspaceID + "/" + testAgentID: {
			ID: testAgentID, WorkspaceID: testWorkspaceID, Name: "A",
			ModelConfigID: "", Status: agent.StatusActive,
		},
	}}
	models := &fakeModelLookup{}
	gate, err := NewAgentModelGate(agents, models)
	if err != nil {
		t.Fatal(err)
	}
	_, err = gate.Resolve(context.Background(), testWorkspaceID, testAgentID, "")
	if !errors.Is(err, ErrAgentModelRequired) {
		t.Fatalf("want ErrAgentModelRequired, got %v", err)
	}
}

func TestAgentModelGateRejectsMissingModelConfig(t *testing.T) {
	t.Parallel()
	agents := &fakeAgentLookup{byKey: map[string]agent.Agent{
		testWorkspaceID + "/" + testAgentID: {
			ID: testAgentID, WorkspaceID: testWorkspaceID, Name: "A",
			ModelConfigID: testModelConfigID, Status: agent.StatusActive,
		},
	}}
	models := &fakeModelLookup{err: modelconfig.ErrNotFound}
	gate, err := NewAgentModelGate(agents, models)
	if err != nil {
		t.Fatal(err)
	}
	_, err = gate.Resolve(context.Background(), testWorkspaceID, testAgentID, "")
	if !errors.Is(err, ErrAgentModelRequired) {
		t.Fatalf("want ErrAgentModelRequired, got %v", err)
	}
}

func TestAgentModelGateRejectsDisabledModel(t *testing.T) {
	t.Parallel()
	agents := &fakeAgentLookup{byKey: map[string]agent.Agent{
		testWorkspaceID + "/" + testAgentID: {
			ID: testAgentID, WorkspaceID: testWorkspaceID,
			ModelConfigID: testModelConfigID, Status: agent.StatusActive,
		},
	}}
	models := &fakeModelLookup{cfg: modelconfig.Config{
		ID: testModelConfigID, WorkspaceID: testWorkspaceID,
		APIBase: "https://example.com/v1", ModelName: "gpt-test",
		Status: modelconfig.StatusDisabled,
	}}
	gate, err := NewAgentModelGate(agents, models)
	if err != nil {
		t.Fatal(err)
	}
	_, err = gate.Resolve(context.Background(), testWorkspaceID, testAgentID, "")
	if !errors.Is(err, ErrAgentModelRequired) {
		t.Fatalf("want ErrAgentModelRequired, got %v", err)
	}
}

func TestAgentModelGateRejectsRequestBodyModelBypass(t *testing.T) {
	t.Parallel()
	gate, err := NewAgentModelGate(&fakeAgentLookup{}, &fakeModelLookup{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = gate.Resolve(context.Background(), testWorkspaceID, testAgentID, testModelConfigID)
	if !errors.Is(err, ErrModelConfigBypassRejected) {
		t.Fatalf("want ErrModelConfigBypassRejected, got %v", err)
	}
}

func TestAgentModelGateRejectsCrossWorkspaceAgent(t *testing.T) {
	t.Parallel()
	agents := &fakeAgentLookup{err: agent.ErrNotFound}
	gate, err := NewAgentModelGate(agents, &fakeModelLookup{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = gate.Resolve(context.Background(), testWorkspaceID, testAgentID, "")
	if !errors.Is(err, ErrAgentNotInWorkspace) {
		t.Fatalf("want ErrAgentNotInWorkspace, got %v", err)
	}
}

func TestAgentModelGateResolvesUsableAgentModel(t *testing.T) {
	t.Parallel()
	agents := &fakeAgentLookup{byKey: map[string]agent.Agent{
		testWorkspaceID + "/" + testAgentID: {
			ID: testAgentID, WorkspaceID: testWorkspaceID,
			ModelConfigID: testModelConfigID, Status: agent.StatusActive,
		},
	}}
	models := &fakeModelLookup{cfg: modelconfig.Config{
		ID: testModelConfigID, WorkspaceID: testWorkspaceID,
		APIBase: "https://example.com/v1", ModelName: "gpt-test",
		Status: modelconfig.StatusVerified,
	}}
	gate, err := NewAgentModelGate(agents, models)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := gate.Resolve(context.Background(), testWorkspaceID, testAgentID, "")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Agent.ID != testAgentID || resolved.ModelConfig.ID != testModelConfigID {
		t.Fatalf("unexpected resolve: %+v", resolved)
	}
	// Model must come from Agent binding, not a free-form request field.
	if resolved.ModelConfig.ID != agents.byKey[testWorkspaceID+"/"+testAgentID].ModelConfigID {
		t.Fatal("model config must equal agent.ModelConfigID")
	}
}

func TestAgentModelGateRejectsAgentInOtherWorkspaceKey(t *testing.T) {
	t.Parallel()
	// Agent only registered under other workspace; lookup by target workspace fails.
	agents := &fakeAgentLookup{byKey: map[string]agent.Agent{
		testOtherWS + "/" + testAgentID: {
			ID: testAgentID, WorkspaceID: testOtherWS,
			ModelConfigID: testModelConfigID, Status: agent.StatusActive,
		},
	}}
	gate, err := NewAgentModelGate(agents, &fakeModelLookup{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = gate.Resolve(context.Background(), testWorkspaceID, testAgentID, "")
	if !errors.Is(err, ErrAgentNotInWorkspace) {
		t.Fatalf("want cross-workspace ErrAgentNotInWorkspace, got %v", err)
	}
}

type fakeAgentLookup struct {
	byKey map[string]agent.Agent
	err   error
}

func (f *fakeAgentLookup) Get(_ context.Context, workspaceID, agentID string) (agent.Agent, error) {
	if f.err != nil {
		return agent.Agent{}, f.err
	}
	if f.byKey == nil {
		return agent.Agent{}, agent.ErrNotFound
	}
	value, ok := f.byKey[workspaceID+"/"+agentID]
	if !ok {
		return agent.Agent{}, agent.ErrNotFound
	}
	return value, nil
}

type fakeModelLookup struct {
	cfg modelconfig.Config
	err error
}

func (f *fakeModelLookup) Get(_ context.Context, workspaceID, configID string) (modelconfig.Config, error) {
	if f.err != nil {
		return modelconfig.Config{}, f.err
	}
	if f.cfg.ID == "" {
		return modelconfig.Config{}, modelconfig.ErrNotFound
	}
	if f.cfg.WorkspaceID != "" && f.cfg.WorkspaceID != workspaceID {
		return modelconfig.Config{}, modelconfig.ErrNotFound
	}
	if f.cfg.ID != configID {
		return modelconfig.Config{}, modelconfig.ErrNotFound
	}
	return f.cfg, nil
}
