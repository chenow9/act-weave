package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Default Eino runtime budgets. Applied when config values are zero/unset.
const (
	DefaultEinoMaxIterations      = 8
	DefaultEinoMaxToolInvocations = 16

	// Workflow engine modes. After Load/applyRuntimeDefaults (P0 / no-reinvent):
	// omitted engine stages "eino" (compose CoreGraphRunner). Explicit
	// "wrapper" remains the emergency rollback valve. "eino_core" is an
	// alias of "eino" (same runner in workflowruntime.NewCompiledPlanExecutor).
	WorkflowEngineWrapper  = "wrapper"
	WorkflowEngineEinoCore = "eino_core"
	WorkflowEngineEino     = "eino"
)

// RuntimeFeatureRollout is the workspace gray-release / diagnostics gate for
// agent eino (PR15/PR16).
//
// Production Enqueue always uses the eino bridge when the process boots
// successfully. Enabled/allowlist do not switch engines:
//   - AllowsWorkspace remains useful for ops diagnostics and future gates.
//   - Explicit enabled:false does NOT change Enqueue routing.
//   - Emergency agent rollback = previous binary / drain traffic.
//
// Load-time defaults (PR15, applyRuntimeDefaults after YAML+env):
//   - Omitted / zero runtime.agent → Enabled=true + AllowAllWorkspaces=true.
//
// Direct struct zero value (no Load) remains Enabled=false so unit tests and
// in-process construction stay fail-closed unless rollout is set explicitly.
//
// There is intentionally no dual-run / shadow field (v1 product decision).
type RuntimeFeatureRollout struct {
	Enabled            bool     `yaml:"enabled"`
	AllowAllWorkspaces bool     `yaml:"allowAllWorkspaces"`
	WorkspaceIDs       []string `yaml:"workspaceIds"`

	// Presence flags distinguish omitted keys from explicit false during Load.
	// Not part of the public runtime contract; ignored by Normalized().
	enabledPresent            bool `yaml:"-"`
	allowAllWorkspacesPresent bool `yaml:"-"`
}

// UnmarshalYAML records whether enabled / allowAllWorkspaces were present so
// applyRuntimeDefaults can stage PR15 open defaults without clobbering
// explicit enabled:false rollback.
func (feature *RuntimeFeatureRollout) UnmarshalYAML(value *yaml.Node) error {
	if feature == nil {
		return errors.New("RuntimeFeatureRollout: nil receiver")
	}
	var raw struct {
		Enabled            *bool    `yaml:"enabled"`
		AllowAllWorkspaces *bool    `yaml:"allowAllWorkspaces"`
		WorkspaceIDs       []string `yaml:"workspaceIds"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	if raw.Enabled != nil {
		feature.Enabled = *raw.Enabled
		feature.enabledPresent = true
	}
	if raw.AllowAllWorkspaces != nil {
		feature.AllowAllWorkspaces = *raw.AllowAllWorkspaces
		feature.allowAllWorkspacesPresent = true
	}
	feature.WorkspaceIDs = raw.WorkspaceIDs
	return nil
}

// AllowsWorkspace reports whether the workspace may use the agent eino engine.
func (feature RuntimeFeatureRollout) AllowsWorkspace(workspaceID string) bool {
	if !feature.Enabled {
		return false
	}
	workspaceID = strings.ToLower(strings.TrimSpace(workspaceID))
	if workspaceID == "" {
		return false
	}
	if feature.AllowAllWorkspaces {
		return true
	}
	for _, id := range feature.WorkspaceIDs {
		if strings.ToLower(strings.TrimSpace(id)) == workspaceID {
			return true
		}
	}
	return false
}

// Normalized returns a copy with trimmed lower-case workspace IDs (empty dropped).
func (feature RuntimeFeatureRollout) Normalized() RuntimeFeatureRollout {
	out := RuntimeFeatureRollout{
		Enabled:            feature.Enabled,
		AllowAllWorkspaces: feature.AllowAllWorkspaces,
	}
	for _, id := range feature.WorkspaceIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" {
			out.WorkspaceIDs = append(out.WorkspaceIDs, id)
		}
	}
	return out
}

// WorkflowRuntimeConfig selects the workflow execution engine and optional
// workspace gray rollout for non-wrapper modes.
//
// Load-time defaults (P0, applyRuntimeDefaults after YAML+env):
//   - Omitted engine → "eino" (compose) for all workspaces when allowAll was
//     also omitted and no workspace allowlist was given.
//   - Explicit engine: wrapper is preserved (rollback valve).
//   - Explicit allowAllWorkspaces:false is preserved (gray start / fail-closed).
//
// Normalized() (no Load) still maps empty engine → wrapper so unit tests and
// direct construction stay fail-closed / legacy unless they set engine explicitly.
//
// When Engine is wrapper, AllowsWorkspace is always true (no gray gate).
// When Engine is eino_core/eino, allowAll vs workspaceIds gate which workspaces
// use compose; others stay on wrapper.
type WorkflowRuntimeConfig struct {
	Engine             string   `yaml:"engine"`
	AllowAllWorkspaces bool     `yaml:"allowAllWorkspaces"`
	WorkspaceIDs       []string `yaml:"workspaceIds"`

	// Presence flags for Load-time staging (not part of public runtime contract).
	enginePresent             bool `yaml:"-"`
	allowAllWorkspacesPresent bool `yaml:"-"`
}

// UnmarshalYAML records whether engine / allowAllWorkspaces were present so
// applyRuntimeDefaults can stage compose defaults without clobbering explicit
// wrapper rollback or allowAllWorkspaces:false gray-start.
func (cfg *WorkflowRuntimeConfig) UnmarshalYAML(value *yaml.Node) error {
	if cfg == nil {
		return errors.New("WorkflowRuntimeConfig: nil receiver")
	}
	var raw struct {
		Engine             *string  `yaml:"engine"`
		AllowAllWorkspaces *bool    `yaml:"allowAllWorkspaces"`
		WorkspaceIDs       []string `yaml:"workspaceIds"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	if raw.Engine != nil {
		cfg.Engine = strings.TrimSpace(*raw.Engine)
		cfg.enginePresent = true
	}
	if raw.AllowAllWorkspaces != nil {
		cfg.AllowAllWorkspaces = *raw.AllowAllWorkspaces
		cfg.allowAllWorkspacesPresent = true
	}
	cfg.WorkspaceIDs = raw.WorkspaceIDs
	return nil
}

// AllowsWorkspace reports whether the workspace may use the configured non-wrapper
// workflow engine. Wrapper mode always allows (legacy path for everyone).
func (cfg WorkflowRuntimeConfig) AllowsWorkspace(workspaceID string) bool {
	engine := strings.ToLower(strings.TrimSpace(cfg.Engine))
	if engine == "" || engine == WorkflowEngineWrapper {
		return true
	}
	workspaceID = strings.ToLower(strings.TrimSpace(workspaceID))
	if workspaceID == "" {
		return false
	}
	if cfg.AllowAllWorkspaces {
		return true
	}
	for _, id := range cfg.WorkspaceIDs {
		if strings.ToLower(strings.TrimSpace(id)) == workspaceID {
			return true
		}
	}
	return false
}

// Normalized returns a copy with normalized engine + trimmed workspace IDs.
// Empty engine maps to wrapper (not Load staged "eino") so direct construction
// without applyRuntimeDefaults stays on the legacy interpreter for tests.
func (cfg WorkflowRuntimeConfig) Normalized() WorkflowRuntimeConfig {
	engine := strings.ToLower(strings.TrimSpace(cfg.Engine))
	if engine == "" {
		engine = WorkflowEngineWrapper
	}
	out := WorkflowRuntimeConfig{
		Engine:             engine,
		AllowAllWorkspaces: cfg.AllowAllWorkspaces,
	}
	for _, id := range cfg.WorkspaceIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" {
			out.WorkspaceIDs = append(out.WorkspaceIDs, id)
		}
	}
	return out
}

// EinoRuntimeTuning holds orchestration budgets shared by agent/workflow eino paths.
type EinoRuntimeTuning struct {
	// MaxIterations caps model rounds (adk MaxIterations). Default 8.
	MaxIterations int `yaml:"maxIterations"`
	// MaxToolInvocations hard-caps total tool calls per run. Default 16.
	MaxToolInvocations int `yaml:"maxToolInvocations"`
}

// Normalized applies zero-value defaults for eino budgets.
func (tuning EinoRuntimeTuning) Normalized() EinoRuntimeTuning {
	out := tuning
	if out.MaxIterations <= 0 {
		out.MaxIterations = DefaultEinoMaxIterations
	}
	if out.MaxToolInvocations <= 0 {
		out.MaxToolInvocations = DefaultEinoMaxToolInvocations
	}
	return out
}

// RuntimeConfig is the process-level execution-engine configuration.
//
// After Load/applyRuntimeDefaults:
//   - Agent (PR15): defaults to eino for all workspaces (enabled + allowAll).
//   - Workflow (P0): defaults to engine=eino (compose) + allowAll when omitted.
//
// Direct zero value (no Load) keeps agent Enabled=false and workflow engine
// empty (Normalized → wrapper) for fail-closed unit tests.
// No dual-run fields by design.
type RuntimeConfig struct {
	Agent    RuntimeFeatureRollout `yaml:"agent"`
	Workflow WorkflowRuntimeConfig `yaml:"workflow"`
	Eino     EinoRuntimeTuning     `yaml:"eino"`
}

// Normalized returns a copy with defaults applied (engine, budgets, ID lists).
// Does not apply PR15 agent staged defaults — those run only in applyRuntimeDefaults
// during Load so explicit construction stays under caller control.
func (cfg RuntimeConfig) Normalized() RuntimeConfig {
	return RuntimeConfig{
		Agent:    cfg.Agent.Normalized(),
		Workflow: cfg.Workflow.Normalized(),
		Eino:     cfg.Eino.Normalized(),
	}
}

// applyRuntimeDefaults mutates config so zero/omitted runtime fields become
// process defaults. Called after YAML + env load so consumers see production
// values without requiring every caller to call Normalized().
//
// PR15: omitted agent rollout stages eino for all workspaces. Explicit
// enabled:false (yaml or ACTWEAVE_RUNTIME_AGENT_ENABLED) is preserved.
//
// P0 (no-reinvent): omitted workflow.engine stages "eino" (compose). Explicit
// engine: wrapper is preserved as rollback. When engine is compose and
// allowAll was omitted with an empty allowlist, allowAllWorkspaces=true so
// the factory does not fail-closed back to wrapper for everyone.
func (config *Config) applyRuntimeDefaults() {
	if config == nil {
		return
	}
	wf := &config.Runtime.Workflow
	if !wf.enginePresent && strings.TrimSpace(wf.Engine) == "" {
		wf.Engine = WorkflowEngineEino
	} else {
		wf.Engine = strings.ToLower(strings.TrimSpace(wf.Engine))
	}
	// Stage open compose for all workspaces when operator did not set allowAll
	// and did not supply a gray allowlist. Explicit allowAllWorkspaces:false
	// is preserved (factory then fail-closes non-listed workspaces to wrapper).
	if isComposeWorkflowEngine(wf.Engine) &&
		!wf.allowAllWorkspacesPresent && len(wf.WorkspaceIDs) == 0 {
		wf.AllowAllWorkspaces = true
	}

	if config.Runtime.Eino.MaxIterations <= 0 {
		config.Runtime.Eino.MaxIterations = DefaultEinoMaxIterations
	}
	if config.Runtime.Eino.MaxToolInvocations <= 0 {
		config.Runtime.Eino.MaxToolInvocations = DefaultEinoMaxToolInvocations
	}

	// Stage agent engine=eino when the operator did not set the field.
	if !config.Runtime.Agent.enabledPresent {
		config.Runtime.Agent.Enabled = true
	}
	// Open all workspaces when allowAll was omitted and no gray allowlist was given.
	// Explicit allowAllWorkspaces:false is preserved; non-empty workspaceIds implies gray.
	if !config.Runtime.Agent.allowAllWorkspacesPresent && len(config.Runtime.Agent.WorkspaceIDs) == 0 {
		config.Runtime.Agent.AllowAllWorkspaces = true
	}
}

// isComposeWorkflowEngine reports engines that use CoreGraphRunner (not PlanRunner).
func isComposeWorkflowEngine(engine string) bool {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case WorkflowEngineEino, WorkflowEngineEinoCore:
		return true
	default:
		return false
	}
}

func (config *Config) applyRuntimeEnvironment(lookup LookupEnv) error {
	if config == nil || lookup == nil {
		return nil
	}
	if raw, ok := lookup("ACTWEAVE_RUNTIME_AGENT_ENABLED"); ok {
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return errors.New("ACTWEAVE_RUNTIME_AGENT_ENABLED must be a boolean")
		}
		config.Runtime.Agent.Enabled = value
		config.Runtime.Agent.enabledPresent = true
	}
	if raw, ok := lookup("ACTWEAVE_RUNTIME_AGENT_ALLOW_ALL_WORKSPACES"); ok {
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return errors.New("ACTWEAVE_RUNTIME_AGENT_ALLOW_ALL_WORKSPACES must be a boolean")
		}
		config.Runtime.Agent.AllowAllWorkspaces = value
		config.Runtime.Agent.allowAllWorkspacesPresent = true
	}
	if raw, ok := lookup("ACTWEAVE_RUNTIME_AGENT_WORKSPACE_IDS"); ok {
		config.Runtime.Agent.WorkspaceIDs = splitCSV(raw)
	}
	if raw, ok := lookup("ACTWEAVE_RUNTIME_WORKFLOW_ENGINE"); ok {
		config.Runtime.Workflow.Engine = strings.TrimSpace(raw)
		config.Runtime.Workflow.enginePresent = true
	}
	if raw, ok := lookup("ACTWEAVE_RUNTIME_WORKFLOW_ALLOW_ALL_WORKSPACES"); ok {
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return errors.New("ACTWEAVE_RUNTIME_WORKFLOW_ALLOW_ALL_WORKSPACES must be a boolean")
		}
		config.Runtime.Workflow.AllowAllWorkspaces = value
		config.Runtime.Workflow.allowAllWorkspacesPresent = true
	}
	if raw, ok := lookup("ACTWEAVE_RUNTIME_WORKFLOW_WORKSPACE_IDS"); ok {
		config.Runtime.Workflow.WorkspaceIDs = splitCSV(raw)
	}
	if raw, ok := lookup("ACTWEAVE_RUNTIME_EINO_MAX_ITERATIONS"); ok {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return errors.New("ACTWEAVE_RUNTIME_EINO_MAX_ITERATIONS must be an integer")
		}
		config.Runtime.Eino.MaxIterations = value
	}
	if raw, ok := lookup("ACTWEAVE_RUNTIME_EINO_MAX_TOOL_INVOCATIONS"); ok {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return errors.New("ACTWEAVE_RUNTIME_EINO_MAX_TOOL_INVOCATIONS must be an integer")
		}
		config.Runtime.Eino.MaxToolInvocations = value
	}
	return nil
}

func validateRuntimeConfig(cfg RuntimeConfig) error {
	if err := validateRuntimeFeatureRollout("runtime.agent", cfg.Agent); err != nil {
		return err
	}
	if err := validateWorkflowRuntimeConfig(cfg.Workflow); err != nil {
		return err
	}
	if cfg.Eino.MaxIterations <= 0 {
		return errors.New("runtime.eino.maxIterations must be a positive integer")
	}
	if cfg.Eino.MaxToolInvocations <= 0 {
		return errors.New("runtime.eino.maxToolInvocations must be a positive integer")
	}
	return nil
}

func validateRuntimeFeatureRollout(path string, feature RuntimeFeatureRollout) error {
	if feature.AllowAllWorkspaces && len(feature.WorkspaceIDs) > 0 {
		return fmt.Errorf("%s.workspaceIds must be empty when allowAllWorkspaces is true", path)
	}
	const maxAllowlist = 10_000
	if len(feature.WorkspaceIDs) > maxAllowlist {
		return fmt.Errorf("%s allowlist exceeds the configured maximum", path)
	}
	seen := make(map[string]struct{}, len(feature.WorkspaceIDs))
	for _, id := range feature.WorkspaceIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			return fmt.Errorf("%s.workspaceIds must not contain empty values", path)
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("%s.workspaceIds must be unique", path)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validateWorkflowRuntimeConfig(cfg WorkflowRuntimeConfig) error {
	engine := strings.ToLower(strings.TrimSpace(cfg.Engine))
	switch engine {
	case "", WorkflowEngineWrapper, WorkflowEngineEinoCore, WorkflowEngineEino:
	default:
		return fmt.Errorf(
			"runtime.workflow.engine must be %s, %s, or %s",
			WorkflowEngineWrapper, WorkflowEngineEinoCore, WorkflowEngineEino,
		)
	}
	// allowAll + list mutual exclusion applies for any engine (including wrapper)
	// so operators cannot misconfigure a future engine switch.
	if cfg.AllowAllWorkspaces && len(cfg.WorkspaceIDs) > 0 {
		return errors.New("runtime.workflow.workspaceIds must be empty when allowAllWorkspaces is true")
	}
	const maxAllowlist = 10_000
	if len(cfg.WorkspaceIDs) > maxAllowlist {
		return errors.New("runtime.workflow allowlist exceeds the configured maximum")
	}
	seen := make(map[string]struct{}, len(cfg.WorkspaceIDs))
	for _, id := range cfg.WorkspaceIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			return errors.New("runtime.workflow.workspaceIds must not contain empty values")
		}
		if _, dup := seen[id]; dup {
			return errors.New("runtime.workflow.workspaceIds must be unique")
		}
		seen[id] = struct{}{}
	}
	return nil
}
