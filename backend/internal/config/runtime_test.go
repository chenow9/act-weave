package config

import (
	"strings"
	"testing"
)

func TestRuntimeConfig(t *testing.T) {
	t.Run("ZeroValueStructFailClosed", testRuntimeZeroValueStructFailClosed)
	t.Run("LoadStagesAgentEinoDefault", testRuntimeLoadStagesAgentEinoDefault)
	t.Run("ExplicitDisableRollback", testRuntimeExplicitDisableRollback)
	t.Run("ExplicitWorkflowWrapperRollback", testRuntimeExplicitWorkflowWrapperRollback)
	t.Run("AgentAllowlistsAndAllowAll", testRuntimeAgentAllowlists)
	t.Run("WorkflowEngineAndAllowlists", testRuntimeWorkflowEngine)
	t.Run("ValidationRejectsContradictions", testRuntimeValidation)
	t.Run("EnvironmentOverrides", testRuntimeEnvironmentOverrides)
	t.Run("EnvDisableOverridesStagedDefault", testRuntimeEnvDisableOverridesStagedDefault)
	t.Run("EnvWorkflowWrapperOverridesStagedDefault", testRuntimeEnvWorkflowWrapperOverridesStagedDefault)
	t.Run("LoadFromYAML", testRuntimeLoadYAML)
	t.Run("EinoDefaults", testRuntimeEinoDefaults)
}

func testRuntimeZeroValueStructFailClosed(t *testing.T) {
	// Direct construction (no Load) stays fail-closed for unit tests / wiring.
	var cfg RuntimeConfig
	if cfg.Agent.AllowsWorkspace("a0000000-0000-4000-8000-000000000001") {
		t.Fatal("zero-value agent rollout must deny all workspaces")
	}
	if cfg.Agent.Enabled {
		t.Fatal("agent enabled must default false on zero-value struct")
	}
	// After Normalize: wrapper engine + eino budgets (agent not staged open).
	normalized := cfg.Normalized()
	if normalized.Workflow.Engine != WorkflowEngineWrapper {
		t.Fatalf("workflow engine default: got %q want %q", normalized.Workflow.Engine, WorkflowEngineWrapper)
	}
	if normalized.Eino.MaxIterations != DefaultEinoMaxIterations ||
		normalized.Eino.MaxToolInvocations != DefaultEinoMaxToolInvocations {
		t.Fatalf("eino defaults: %+v", normalized.Eino)
	}
	if normalized.Agent.Enabled {
		t.Fatal("Normalized must not stage agent open (Load/applyRuntimeDefaults does)")
	}
	// Wrapper allows all workspaces (legacy path).
	if !normalized.Workflow.AllowsWorkspace("a0000000-0000-4000-8000-000000000001") {
		t.Fatal("wrapper workflow must allow all workspaces")
	}
}

func testRuntimeLoadStagesAgentEinoDefault(t *testing.T) {
	// PR15: omitted runtime.agent after Load → enabled + allowAll (eino staged).
	path := writeConfig(t, validConfigYAML)
	loaded, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Runtime.Agent.Enabled {
		t.Fatal("loaded omitted agent must stage Enabled=true")
	}
	if !loaded.Runtime.Agent.AllowAllWorkspaces {
		t.Fatal("loaded omitted agent must stage AllowAllWorkspaces=true")
	}
	ws := "a0000000-0000-4000-8000-000000000001"
	if !loaded.Runtime.Agent.AllowsWorkspace(ws) {
		t.Fatal("staged default must allow all workspaces")
	}
	// P0: omitted runtime.workflow stages compose (eino) for all workspaces.
	if loaded.Runtime.Workflow.Engine != WorkflowEngineEino {
		t.Fatalf("workflow engine: got %q want eino", loaded.Runtime.Workflow.Engine)
	}
	if !loaded.Runtime.Workflow.AllowAllWorkspaces {
		t.Fatal("loaded omitted workflow must stage AllowAllWorkspaces=true")
	}
	if !loaded.Runtime.Workflow.AllowsWorkspace(ws) {
		t.Fatal("staged workflow default must allow all workspaces")
	}
}

func testRuntimeExplicitWorkflowWrapperRollback(t *testing.T) {
	// Explicit yaml engine: wrapper must remain the rollback valve.
	yaml := validConfigYAML + `
runtime:
  workflow:
    engine: wrapper
`
	path := writeConfig(t, yaml)
	loaded, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Runtime.Workflow.Engine != WorkflowEngineWrapper {
		t.Fatalf("explicit wrapper must not be overwritten: got %q", loaded.Runtime.Workflow.Engine)
	}
}

func testRuntimeExplicitDisableRollback(t *testing.T) {
	// Explicit yaml enabled:false must force legacy (rollback valve).
	yaml := validConfigYAML + `
runtime:
  agent:
    enabled: false
`
	path := writeConfig(t, yaml)
	loaded, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Runtime.Agent.Enabled {
		t.Fatal("explicit enabled:false must not be overwritten by staged default")
	}
	if loaded.Runtime.Agent.AllowsWorkspace("a0000000-0000-4000-8000-000000000001") {
		t.Fatal("explicit disable must deny all workspaces")
	}
}

func testRuntimeAgentAllowlists(t *testing.T) {
	ws := "a0000000-0000-4000-8000-0000000000aa"
	feature := RuntimeFeatureRollout{Enabled: true, WorkspaceIDs: []string{ws}}
	if !feature.AllowsWorkspace(ws) {
		t.Fatal("allowlisted workspace must pass")
	}
	if feature.AllowsWorkspace("c0000000-0000-4000-8000-0000000000cc") {
		t.Fatal("non-allowlisted workspace must fail")
	}
	empty := RuntimeFeatureRollout{Enabled: true}
	if empty.AllowsWorkspace(ws) {
		t.Fatal("empty allowlist must deny")
	}
	open := RuntimeFeatureRollout{Enabled: true, AllowAllWorkspaces: true}
	if !open.AllowsWorkspace(ws) {
		t.Fatal("allow-all must pass")
	}
	// Case-insensitive match.
	if !feature.AllowsWorkspace(strings.ToUpper(ws)) {
		t.Fatal("workspace allowlist must be case-insensitive")
	}
}

func testRuntimeWorkflowEngine(t *testing.T) {
	ws := "a0000000-0000-4000-8000-0000000000aa"
	wrapper := WorkflowRuntimeConfig{Engine: WorkflowEngineWrapper}
	if !wrapper.AllowsWorkspace(ws) {
		t.Fatal("wrapper must allow all workspaces")
	}
	gated := WorkflowRuntimeConfig{
		Engine: WorkflowEngineEino, WorkspaceIDs: []string{ws},
	}
	if !gated.AllowsWorkspace(ws) {
		t.Fatal("allowlisted workspace must use eino workflow")
	}
	if gated.AllowsWorkspace("c0000000-0000-4000-8000-0000000000cc") {
		t.Fatal("non-allowlisted workspace must not use eino workflow")
	}
	open := WorkflowRuntimeConfig{Engine: WorkflowEngineEinoCore, AllowAllWorkspaces: true}
	if !open.AllowsWorkspace(ws) {
		t.Fatal("allow-all workflow eino must pass")
	}
}

func testRuntimeValidation(t *testing.T) {
	path := writeConfig(t, validConfigYAML)
	loaded, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}

	loaded.Runtime.Agent = RuntimeFeatureRollout{
		Enabled: true, AllowAllWorkspaces: true,
		WorkspaceIDs: []string{"a0000000-0000-4000-8000-000000000001"},
	}
	if err := loaded.ValidateServer(); err == nil {
		t.Fatal("expected validation error for agent allowAll + workspace list")
	}

	loaded.Runtime.Agent = RuntimeFeatureRollout{}
	loaded.Runtime.Workflow = WorkflowRuntimeConfig{
		Engine: WorkflowEngineWrapper, AllowAllWorkspaces: true,
		WorkspaceIDs: []string{"a0000000-0000-4000-8000-000000000001"},
	}
	if err := loaded.ValidateServer(); err == nil {
		t.Fatal("expected validation error for workflow allowAll + workspace list")
	}

	loaded.Runtime.Workflow = WorkflowRuntimeConfig{Engine: "native"}
	if err := loaded.ValidateServer(); err == nil {
		t.Fatal("expected validation error for invalid workflow engine")
	}

	loaded.Runtime.Workflow = WorkflowRuntimeConfig{Engine: WorkflowEngineWrapper}
	loaded.Runtime.Agent = RuntimeFeatureRollout{
		Enabled: true, WorkspaceIDs: []string{"ws-1", "ws-1"},
	}
	if err := loaded.ValidateServer(); err == nil {
		t.Fatal("expected duplicate workspace validation error")
	}

	// Valid gray config.
	loaded.Runtime = RuntimeConfig{
		Agent: RuntimeFeatureRollout{
			Enabled:      true,
			WorkspaceIDs: []string{"a0000000-0000-4000-8000-000000000001"},
		},
		Workflow: WorkflowRuntimeConfig{
			Engine:       WorkflowEngineEinoCore,
			WorkspaceIDs: []string{"a0000000-0000-4000-8000-000000000002"},
		},
		Eino: EinoRuntimeTuning{
			MaxIterations: DefaultEinoMaxIterations, MaxToolInvocations: DefaultEinoMaxToolInvocations,
		},
	}
	if err := loaded.ValidateServer(); err != nil {
		t.Fatalf("valid runtime gray config rejected: %v", err)
	}
}

func testRuntimeEnvironmentOverrides(t *testing.T) {
	path := writeConfig(t, validConfigYAML)
	values := map[string]string{
		"ACTWEAVE_RUNTIME_AGENT_ENABLED":                 "true",
		"ACTWEAVE_RUNTIME_AGENT_ALLOW_ALL_WORKSPACES":    "false",
		"ACTWEAVE_RUNTIME_AGENT_WORKSPACE_IDS":           "a0000000-0000-4000-8000-0000000000a1, a0000000-0000-4000-8000-0000000000a2",
		"ACTWEAVE_RUNTIME_WORKFLOW_ENGINE":               "eino_core",
		"ACTWEAVE_RUNTIME_WORKFLOW_ALLOW_ALL_WORKSPACES": "false",
		"ACTWEAVE_RUNTIME_WORKFLOW_WORKSPACE_IDS":        "b0000000-0000-4000-8000-0000000000b1",
		"ACTWEAVE_RUNTIME_EINO_MAX_ITERATIONS":           "12",
		"ACTWEAVE_RUNTIME_EINO_MAX_TOOL_INVOCATIONS":     "24",
	}
	loaded, err := Load(path, lookup(values))
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Runtime.Agent.Enabled || loaded.Runtime.Agent.AllowAllWorkspaces {
		t.Fatalf("agent env override failed: %+v", loaded.Runtime.Agent)
	}
	if len(loaded.Runtime.Agent.WorkspaceIDs) != 2 {
		t.Fatalf("agent workspace env override failed: %+v", loaded.Runtime.Agent.WorkspaceIDs)
	}
	if !loaded.Runtime.Agent.AllowsWorkspace("a0000000-0000-4000-8000-0000000000a1") {
		t.Fatal("env gray allowlist must allow listed workspace")
	}
	if loaded.Runtime.Agent.AllowsWorkspace("c0000000-0000-4000-8000-0000000000cc") {
		t.Fatal("env gray allowlist must deny non-listed workspace")
	}
	if loaded.Runtime.Workflow.Engine != WorkflowEngineEinoCore {
		t.Fatalf("workflow engine env override failed: %+v", loaded.Runtime.Workflow)
	}
	if loaded.Runtime.Workflow.WorkspaceIDs[0] != "b0000000-0000-4000-8000-0000000000b1" {
		t.Fatalf("workflow workspace env override failed: %+v", loaded.Runtime.Workflow)
	}
	if loaded.Runtime.Eino.MaxIterations != 12 || loaded.Runtime.Eino.MaxToolInvocations != 24 {
		t.Fatalf("eino env override failed: %+v", loaded.Runtime.Eino)
	}
}

func testRuntimeEnvDisableOverridesStagedDefault(t *testing.T) {
	// Emergency rollback: ACTWEAVE_RUNTIME_AGENT_ENABLED=false forces legacy
	// even when yaml omits runtime.agent (which would otherwise stage open).
	path := writeConfig(t, validConfigYAML)
	loaded, err := Load(path, lookup(map[string]string{
		"ACTWEAVE_RUNTIME_AGENT_ENABLED": "false",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Runtime.Agent.Enabled {
		t.Fatal("env enabled=false must override staged open default")
	}
	if loaded.Runtime.Agent.AllowsWorkspace("a0000000-0000-4000-8000-000000000001") {
		t.Fatal("env disable must deny workspaces")
	}
}

func testRuntimeEnvWorkflowWrapperOverridesStagedDefault(t *testing.T) {
	// Emergency rollback: ACTWEAVE_RUNTIME_WORKFLOW_ENGINE=wrapper keeps PlanRunner
	// even when yaml omits runtime.workflow (which would otherwise stage eino).
	path := writeConfig(t, validConfigYAML)
	loaded, err := Load(path, lookup(map[string]string{
		"ACTWEAVE_RUNTIME_WORKFLOW_ENGINE": "wrapper",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Runtime.Workflow.Engine != WorkflowEngineWrapper {
		t.Fatalf("env wrapper must override staged eino: got %q", loaded.Runtime.Workflow.Engine)
	}
}

func testRuntimeLoadYAML(t *testing.T) {
	yaml := validConfigYAML + `
runtime:
  agent:
    enabled: true
    allowAllWorkspaces: false
    workspaceIds: ["a0000000-0000-4000-8000-0000000000f1"]
  workflow:
    engine: eino
    allowAllWorkspaces: false
    workspaceIds: ["b0000000-0000-4000-8000-0000000000f2"]
  eino:
    maxIterations: 10
    maxToolInvocations: 20
`
	path := writeConfig(t, yaml)
	loaded, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Runtime.Agent.Enabled ||
		!loaded.Runtime.Agent.AllowsWorkspace("a0000000-0000-4000-8000-0000000000f1") {
		t.Fatalf("yaml agent runtime load failed: %+v", loaded.Runtime.Agent)
	}
	if loaded.Runtime.Agent.AllowAllWorkspaces {
		t.Fatal("explicit gray yaml must keep allowAllWorkspaces=false")
	}
	if loaded.Runtime.Agent.AllowsWorkspace("c0000000-0000-4000-8000-0000000000cc") {
		t.Fatal("gray yaml must deny non-allowlisted workspace")
	}
	if loaded.Runtime.Workflow.Engine != WorkflowEngineEino ||
		!loaded.Runtime.Workflow.AllowsWorkspace("b0000000-0000-4000-8000-0000000000f2") {
		t.Fatalf("yaml workflow runtime load failed: %+v", loaded.Runtime.Workflow)
	}
	if loaded.Runtime.Eino.MaxIterations != 10 || loaded.Runtime.Eino.MaxToolInvocations != 20 {
		t.Fatalf("yaml eino load failed: %+v", loaded.Runtime.Eino)
	}
	if err := loaded.ValidateServer(); err != nil {
		t.Fatalf("valid yaml runtime rejected: %v", err)
	}
}

func testRuntimeEinoDefaults(t *testing.T) {
	path := writeConfig(t, validConfigYAML)
	loaded, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Runtime.Workflow.Engine != WorkflowEngineEino {
		t.Fatalf("loaded default engine: got %q want eino", loaded.Runtime.Workflow.Engine)
	}
	if !loaded.Runtime.Workflow.AllowAllWorkspaces {
		t.Fatalf("loaded workflow must stage allowAll: %+v", loaded.Runtime.Workflow)
	}
	if loaded.Runtime.Eino.MaxIterations != DefaultEinoMaxIterations ||
		loaded.Runtime.Eino.MaxToolInvocations != DefaultEinoMaxToolInvocations {
		t.Fatalf("loaded eino defaults: %+v", loaded.Runtime.Eino)
	}
	// PR15 staged agent default after Load.
	if !loaded.Runtime.Agent.Enabled || !loaded.Runtime.Agent.AllowAllWorkspaces {
		t.Fatalf("loaded agent must stage open: %+v", loaded.Runtime.Agent)
	}
	if err := loaded.ValidateServer(); err != nil {
		t.Fatalf("default runtime config must validate: %v", err)
	}
}
