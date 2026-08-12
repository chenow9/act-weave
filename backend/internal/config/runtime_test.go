package config

import (
	"strings"
	"testing"
	"time"
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
	t.Run("MaxToolInvocationsContract", testRuntimeMaxToolInvocationsContract)
	t.Run("ModelVerificationTimeoutContract", testRuntimeModelVerificationTimeoutContract)
	t.Run("ModelVerificationTimeoutLoadPaths", testRuntimeModelVerificationTimeoutLoadPaths)
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
		"ACTWEAVE_RUNTIME_EINO_MAX_TOOL_INVOCATIONS":     "8",
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
	if loaded.Runtime.Eino.MaxIterations != 12 || loaded.Runtime.Eino.MaxToolInvocations != 8 {
		t.Fatalf("eino env override failed: %+v", loaded.Runtime.Eino)
	}
	if err := loaded.ValidateServer(); err != nil {
		t.Fatalf("valid maxToolInvocations=8 must pass: %v", err)
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
    maxToolInvocations: 16
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
	if loaded.Runtime.Eino.MaxIterations != 10 || loaded.Runtime.Eino.MaxToolInvocations != 16 {
		t.Fatalf("yaml eino load failed: %+v", loaded.Runtime.Eino)
	}
	if err := loaded.ValidateServer(); err != nil {
		t.Fatalf("valid yaml runtime rejected: %v", err)
	}
}

// testRuntimeMaxToolInvocationsContract locks the production-wide invariant:
// 0 → default 16; 1..16 valid; -1 and 17 (and any negative/>16) fail closed.
// Normalized must not silently default negatives or clamp >16.
func testRuntimeMaxToolInvocationsContract(t *testing.T) {
	// Normalized: 0 → 16; boundaries 1 and 16 preserved; negatives and 17 untouched.
	if n := (EinoRuntimeTuning{}).Normalized(); n.MaxToolInvocations != DefaultEinoMaxToolInvocations {
		t.Fatalf("0 normalize: got %d want %d", n.MaxToolInvocations, DefaultEinoMaxToolInvocations)
	}
	if n := (EinoRuntimeTuning{MaxToolInvocations: 1}).Normalized(); n.MaxToolInvocations != 1 {
		t.Fatalf("1 normalize: got %d", n.MaxToolInvocations)
	}
	if n := (EinoRuntimeTuning{MaxToolInvocations: 16}).Normalized(); n.MaxToolInvocations != 16 {
		t.Fatalf("16 normalize: got %d", n.MaxToolInvocations)
	}
	if n := (EinoRuntimeTuning{MaxToolInvocations: -1}).Normalized(); n.MaxToolInvocations != -1 {
		t.Fatalf("negative must not be silently defaulted: got %d", n.MaxToolInvocations)
	}
	if n := (EinoRuntimeTuning{MaxToolInvocations: 17}).Normalized(); n.MaxToolInvocations != 17 {
		t.Fatalf(">16 must not be silently clamped: got %d", n.MaxToolInvocations)
	}

	// Validate() / validateEinoMaxToolInvocations: 0,1,16 ok; -1,17 fail.
	for _, ok := range []int{0, 1, 16} {
		if err := validateEinoMaxToolInvocations(ok); err != nil {
			t.Fatalf("validate(%d) unexpected: %v", ok, err)
		}
		if err := (EinoRuntimeTuning{MaxToolInvocations: ok}).Validate(); err != nil {
			t.Fatalf("Validate(%d) unexpected: %v", ok, err)
		}
	}
	for _, bad := range []int{-1, 17, -3, 100} {
		if err := validateEinoMaxToolInvocations(bad); err == nil {
			t.Fatalf("validate(%d) must fail", bad)
		}
		if err := (EinoRuntimeTuning{MaxToolInvocations: bad}).Validate(); err == nil {
			t.Fatalf("Validate(%d) must fail", bad)
		}
	}

	// applyRuntimeDefaults + ValidateServer: negative and >16 fail closed.
	path := writeConfig(t, validConfigYAML)
	loaded, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Explicit 1 is valid.
	loaded.Runtime.Eino.MaxToolInvocations = 1
	if err := loaded.ValidateServer(); err != nil {
		t.Fatalf("maxToolInvocations=1 must pass: %v", err)
	}
	loaded.Runtime.Eino.MaxToolInvocations = 16
	if err := loaded.ValidateServer(); err != nil {
		t.Fatalf("maxToolInvocations=16 must pass: %v", err)
	}
	loaded.Runtime.Eino.MaxToolInvocations = -1
	if err := loaded.ValidateServer(); err == nil {
		t.Fatal("maxToolInvocations=-1 must fail ValidateServer")
	}
	loaded.Runtime.Eino.MaxToolInvocations = 17
	if err := loaded.ValidateServer(); err == nil {
		t.Fatal("maxToolInvocations=17 must fail ValidateServer")
	}

	// Env override >16 loads but fails validation (no silent clamp at load).
	over, err := Load(path, lookup(map[string]string{
		"ACTWEAVE_RUNTIME_EINO_MAX_TOOL_INVOCATIONS": "17",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if over.Runtime.Eino.MaxToolInvocations != 17 {
		t.Fatalf("env 17 must not be clamped at load: got %d", over.Runtime.Eino.MaxToolInvocations)
	}
	if err := over.ValidateServer(); err == nil {
		t.Fatal("env maxToolInvocations=17 must fail ValidateServer")
	}
	neg, err := Load(path, lookup(map[string]string{
		"ACTWEAVE_RUNTIME_EINO_MAX_TOOL_INVOCATIONS": "-1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if neg.Runtime.Eino.MaxToolInvocations != -1 {
		t.Fatalf("env -1 must not be defaulted at load: got %d", neg.Runtime.Eino.MaxToolInvocations)
	}
	if err := neg.ValidateServer(); err == nil {
		t.Fatal("env maxToolInvocations=-1 must fail ValidateServer")
	}
}

// testRuntimeModelVerificationTimeoutContract locks R11-1's outer verification
// budget contract on the value itself: 0 → default 90s; 1..600 preserved;
// negative and >600 are neither defaulted nor clamped, they fail closed.
func testRuntimeModelVerificationTimeoutContract(t *testing.T) {
	if DefaultModelVerificationTimeoutSeconds != 90 {
		t.Fatalf("default outer verification budget must stay 90s, got %d",
			DefaultModelVerificationTimeoutSeconds)
	}
	// The default must be able to contain the inner Task 3 probe budgets
	// (Responses stream 30s + client tool_search 45s). The application package
	// pins this against the real probe constants; this is the config-side floor.
	if DefaultModelVerificationTimeoutSeconds < 75 {
		t.Fatalf("default %ds cannot contain the 30s+45s inner probe budgets",
			DefaultModelVerificationTimeoutSeconds)
	}

	// Normalized: only exactly 0 is defaulted.
	if n := (ModelVerificationTuning{}).Normalized(); n.TimeoutSeconds != DefaultModelVerificationTimeoutSeconds {
		t.Fatalf("0 normalize: got %d want %d", n.TimeoutSeconds, DefaultModelVerificationTimeoutSeconds)
	}
	for _, preserved := range []int{1, 75, 90, MaxModelVerificationTimeoutSeconds} {
		if n := (ModelVerificationTuning{TimeoutSeconds: preserved}).Normalized(); n.TimeoutSeconds != preserved {
			t.Fatalf("%d normalize: got %d", preserved, n.TimeoutSeconds)
		}
	}
	for _, hostile := range []int{-1, -90, MaxModelVerificationTimeoutSeconds + 1, 100000} {
		if n := (ModelVerificationTuning{TimeoutSeconds: hostile}).Normalized(); n.TimeoutSeconds != hostile {
			t.Fatalf("%d must not be silently defaulted/clamped: got %d", hostile, n.TimeoutSeconds)
		}
	}

	// Timeout(): seconds → duration, no hidden default, no hidden clamp.
	if got := (ModelVerificationTuning{}).Normalized().Timeout(); got != 90*time.Second {
		t.Fatalf("normalized zero Timeout(): got %v want 90s", got)
	}
	if got := (ModelVerificationTuning{TimeoutSeconds: 120}).Timeout(); got != 120*time.Second {
		t.Fatalf("Timeout(120): got %v", got)
	}
	// Un-normalized zero must stay non-positive so the consuming boundary
	// (modelconfig.NewVerificationService) rejects it instead of silently
	// running with an already-expired context.
	if got := (ModelVerificationTuning{}).Timeout(); got > 0 {
		t.Fatalf("un-normalized zero Timeout() must not be positive: %v", got)
	}
	if got := (ModelVerificationTuning{TimeoutSeconds: -5}).Timeout(); got != -5*time.Second {
		t.Fatalf("Timeout(-5) must stay negative for the consumer to reject: %v", got)
	}

	// Validate(): 0 (default applied later) and 1..600 pass; the rest fail.
	for _, ok := range []int{0, 1, 75, 90, MaxModelVerificationTimeoutSeconds} {
		if err := validateModelVerificationTimeoutSeconds(ok); err != nil {
			t.Fatalf("validate(%d) unexpected: %v", ok, err)
		}
		if err := (ModelVerificationTuning{TimeoutSeconds: ok}).Validate(); err != nil {
			t.Fatalf("Validate(%d) unexpected: %v", ok, err)
		}
	}
	for _, bad := range []int{-1, -90, MaxModelVerificationTimeoutSeconds + 1, 100000} {
		if err := validateModelVerificationTimeoutSeconds(bad); err == nil {
			t.Fatalf("validate(%d) must fail", bad)
		}
		err := (ModelVerificationTuning{TimeoutSeconds: bad}).Validate()
		if err == nil {
			t.Fatalf("Validate(%d) must fail", bad)
		}
		if !strings.Contains(err.Error(), "runtime.modelVerification.timeoutSeconds") {
			t.Fatalf("Validate(%d) error must name the config key, got %v", bad, err)
		}
	}
}

// testRuntimeModelVerificationTimeoutLoadPaths locks the same contract through
// the real Load pipeline: yaml, staged default, env override, ValidateServer.
func testRuntimeModelVerificationTimeoutLoadPaths(t *testing.T) {
	path := writeConfig(t, validConfigYAML)

	// Omitted yaml → 90s default applied by applyRuntimeDefaults, and valid.
	loaded, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Runtime.ModelVerification.TimeoutSeconds != DefaultModelVerificationTimeoutSeconds {
		t.Fatalf("omitted modelVerification must default to %d, got %d",
			DefaultModelVerificationTimeoutSeconds, loaded.Runtime.ModelVerification.TimeoutSeconds)
	}
	if got := loaded.Runtime.ModelVerification.Timeout(); got != 90*time.Second {
		t.Fatalf("loaded default timeout: got %v want 90s", got)
	}
	if err := loaded.ValidateServer(); err != nil {
		t.Fatalf("default modelVerification must validate: %v", err)
	}

	// Explicit yaml value is honoured, not overwritten by the default.
	explicit, err := Load(writeConfig(t, validConfigYAML+`
runtime:
  modelVerification:
    timeoutSeconds: 120
`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Runtime.ModelVerification.TimeoutSeconds != 120 {
		t.Fatalf("yaml timeoutSeconds=120: got %d", explicit.Runtime.ModelVerification.TimeoutSeconds)
	}
	if err := explicit.ValidateServer(); err != nil {
		t.Fatalf("yaml timeoutSeconds=120 must validate: %v", err)
	}

	// Env override wins over yaml.
	overridden, err := Load(writeConfig(t, validConfigYAML+`
runtime:
  modelVerification:
    timeoutSeconds: 120
`), lookup(map[string]string{
		"ACTWEAVE_RUNTIME_MODEL_VERIFICATION_TIMEOUT_SECONDS": "240",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if overridden.Runtime.ModelVerification.TimeoutSeconds != 240 {
		t.Fatalf("env override: got %d want 240", overridden.Runtime.ModelVerification.TimeoutSeconds)
	}

	// Non-integer env value fails at Load without leaking the value.
	if _, err := Load(path, lookup(map[string]string{
		"ACTWEAVE_RUNTIME_MODEL_VERIFICATION_TIMEOUT_SECONDS": "not-a-number",
	})); err == nil {
		t.Fatal("non-integer timeout env must fail Load")
	} else if strings.Contains(err.Error(), "not-a-number") {
		t.Fatalf("Load error must not leak the raw value: %v", err)
	}

	// Hostile env values load unchanged (no silent clamp/default) and then fail
	// ValidateServer, so startup refuses to run with them.
	for _, bad := range []struct {
		raw  string
		want int
	}{
		{raw: "-1", want: -1},
		{raw: "0", want: DefaultModelVerificationTimeoutSeconds}, // explicit 0 is "use default"
		{raw: "601", want: 601},
		{raw: "100000", want: 100000},
	} {
		cfg, err := Load(path, lookup(map[string]string{
			"ACTWEAVE_RUNTIME_MODEL_VERIFICATION_TIMEOUT_SECONDS": bad.raw,
		}))
		if err != nil {
			t.Fatalf("Load(%s): %v", bad.raw, err)
		}
		if cfg.Runtime.ModelVerification.TimeoutSeconds != bad.want {
			t.Fatalf("env %s: got %d want %d", bad.raw,
				cfg.Runtime.ModelVerification.TimeoutSeconds, bad.want)
		}
		validateErr := cfg.ValidateServer()
		if bad.want == DefaultModelVerificationTimeoutSeconds {
			if validateErr != nil {
				t.Fatalf("env %s (default) must validate: %v", bad.raw, validateErr)
			}
			continue
		}
		if validateErr == nil {
			t.Fatalf("env %s must fail ValidateServer", bad.raw)
		}
	}

	// Directly constructed RuntimeConfig (no Load) also fails closed on a
	// hostile value, and a zero value stays acceptable because
	// application.Open normalizes before use.
	direct, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	direct.Runtime.ModelVerification.TimeoutSeconds = 0
	if err := direct.ValidateServer(); err != nil {
		t.Fatalf("zero modelVerification (normalized at use site) must validate: %v", err)
	}
	direct.Runtime.ModelVerification.TimeoutSeconds = MaxModelVerificationTimeoutSeconds + 1
	if err := direct.ValidateServer(); err == nil {
		t.Fatal("above-maximum modelVerification must fail ValidateServer")
	}
	direct.Runtime.ModelVerification.TimeoutSeconds = -1
	if err := direct.ValidateServer(); err == nil {
		t.Fatal("negative modelVerification must fail ValidateServer")
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
