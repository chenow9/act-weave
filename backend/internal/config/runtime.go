package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Default Eino runtime budgets. Applied when config values are zero/unset.
const (
	DefaultEinoMaxIterations      = 8
	DefaultEinoMaxToolInvocations = 16

	// DefaultModelVerificationTimeoutSeconds is the outer budget for one model
	// config verification attempt (modelconfig.VerificationService.Verify wraps
	// the whole upstream call in it). It must stay at or above the sum of the
	// inner probe budgets — Responses streaming 30s, client tool_search 45s,
	// and function-calling 30s — so those budgets remain reachable rather than
	// dead code, with the remaining 15s covering the GET /models auth probe.
	// application.TestModelVerificationOuterBudgetCoversInnerProbeBudgets pins
	// that relation against the real probe constants.
	DefaultModelVerificationTimeoutSeconds = 120

	// MaxModelVerificationTimeoutSeconds bounds the operator-configurable outer
	// budget. A larger value would hold an HTTP verification request (and its
	// upstream connection) open long enough to be an availability problem, so it
	// fails closed instead of being clamped.
	MaxModelVerificationTimeoutSeconds = 600

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
	// MaxToolInvocations hard-caps total tool calls per run.
	// Contract (production-wide, no silent clamp):
	//   - 0 → DefaultEinoMaxToolInvocations (16) via Normalized / applyRuntimeDefaults
	//   - 1..16 accepted as-is
	//   - negative or >16 fail closed (Validate / validateRuntimeConfig / bridge)
	MaxToolInvocations int `yaml:"maxToolInvocations"`
}

// Normalized applies zero-value defaults for eino budgets.
//
// MaxToolInvocations: exactly 0 maps to DefaultEinoMaxToolInvocations (16).
// Negative and >16 values are left unchanged so Validate/validateRuntimeConfig
// and production boundaries can fail closed — never silently defaulted or clamped.
func (tuning EinoRuntimeTuning) Normalized() EinoRuntimeTuning {
	out := tuning
	if out.MaxIterations <= 0 {
		out.MaxIterations = DefaultEinoMaxIterations
	}
	if out.MaxToolInvocations == 0 {
		out.MaxToolInvocations = DefaultEinoMaxToolInvocations
	}
	return out
}

// Validate reports whether MaxToolInvocations is within the production contract.
// Accepts 0 (meaning default 16 before normalize) and 1..DefaultEinoMaxToolInvocations.
// Negative and >16 fail closed.
func (tuning EinoRuntimeTuning) Validate() error {
	if err := validateEinoMaxToolInvocations(tuning.MaxToolInvocations); err != nil {
		return err
	}
	return nil
}

// validateEinoMaxToolInvocations enforces 0 (default 16) or 1..DefaultEinoMaxToolInvocations.
func validateEinoMaxToolInvocations(max int) error {
	if max == 0 {
		return nil
	}
	if max < 0 || max > DefaultEinoMaxToolInvocations {
		return fmt.Errorf(
			"runtime.eino.maxToolInvocations must be 0 (default %d) or 1..%d, got %d",
			DefaultEinoMaxToolInvocations, DefaultEinoMaxToolInvocations, max,
		)
	}
	return nil
}

// ModelVerificationTuning holds the outer budget for one model config
// verification attempt.
//
// Contract (no silent clamp, mirroring EinoRuntimeTuning):
//   - 0 → DefaultModelVerificationTimeoutSeconds (120) via Normalized /
//     applyRuntimeDefaults
//   - 1..MaxModelVerificationTimeoutSeconds accepted as-is
//   - negative or above the maximum fail closed (Validate /
//     validateRuntimeConfig), and application.Open additionally refuses a
//     non-positive duration through modelconfig.NewVerificationService
type ModelVerificationTuning struct {
	TimeoutSeconds int `yaml:"timeoutSeconds"`
}

// Normalized applies the zero-value default. Negative and above-maximum values
// are left unchanged so Validate/validateRuntimeConfig can fail closed instead
// of a hostile or mistyped value being silently defaulted or clamped.
func (tuning ModelVerificationTuning) Normalized() ModelVerificationTuning {
	out := tuning
	if out.TimeoutSeconds == 0 {
		out.TimeoutSeconds = DefaultModelVerificationTimeoutSeconds
	}
	return out
}

// Timeout returns the configured outer budget as a duration. It does not apply
// the zero-value default: call Normalized() first. Out-of-contract values are
// returned as-is (including non-positive) so the consuming boundary rejects them.
func (tuning ModelVerificationTuning) Timeout() time.Duration {
	return time.Duration(tuning.TimeoutSeconds) * time.Second
}

// Validate accepts 0 (meaning the default is applied later) or
// 1..MaxModelVerificationTimeoutSeconds. Negative and larger values fail closed.
func (tuning ModelVerificationTuning) Validate() error {
	return validateModelVerificationTimeoutSeconds(tuning.TimeoutSeconds)
}

// validateModelVerificationTimeoutSeconds enforces 0 (default 120) or
// 1..MaxModelVerificationTimeoutSeconds.
func validateModelVerificationTimeoutSeconds(seconds int) error {
	if seconds == 0 {
		return nil
	}
	if seconds < 0 || seconds > MaxModelVerificationTimeoutSeconds {
		return fmt.Errorf(
			"runtime.modelVerification.timeoutSeconds must be 0 (default %d) or 1..%d, got %d",
			DefaultModelVerificationTimeoutSeconds, MaxModelVerificationTimeoutSeconds, seconds,
		)
	}
	return nil
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
	// ModelVerification is the outer budget for model config verification.
	// Omitted / zero maps to DefaultModelVerificationTimeoutSeconds (120s).
	ModelVerification ModelVerificationTuning `yaml:"modelVerification"`
	// SessionContext is the fail-closed gate for session context window management
	// (ZKL-74). Default remains disabled unless explicitly enabled + allowlisted.
	// Nested Compaction is the independent LLM compact gate (ZKL-81 / T6-A),
	// default-off and evaluated only at run creation.
	SessionContext SessionContextRollout `yaml:"sessionContext"`
	// ToolDisclosure gates platform_bounded / carry_all (not native client_bounded).
	// Follows SessionContext: omitted / Enabled=false ⇒ AllowsWorkspace is false.
	// Must not be promoted in applyRuntimeDefaults (unlike runtime.agent).
	ToolDisclosure RuntimeFeatureRollout `yaml:"toolDisclosure"`
}

// SessionContextRollout controls whether new agent runs write session-context.v1
// / run.v2 snapshots. Gate is evaluated only at run creation time.
// Mode: disabled (default) | shadow | enforced — IC-10 wires full matrix;
// IC-03 only needs enabled+allowlist for writing v1/v2 snapshots when enforced.
type SessionContextRollout struct {
	Enabled            bool     `yaml:"enabled"`
	AllowAllWorkspaces bool     `yaml:"allowAllWorkspaces"`
	WorkspaceIDs       []string `yaml:"workspaceIds"`
	// Mode is disabled|shadow|enforced. Empty means disabled when Enabled=false.
	Mode           string `yaml:"mode"`
	RolloutVersion string `yaml:"rolloutVersion"`
	// Compaction is an independent sub-gate for session-context.v2 LLM compact (T6-A).
	// Default closed: does not inherit parent sessionContext allowlist.
	Compaction CompactionRollout `yaml:"compaction"`
}

// CompactionRollout is the independent, default-off LLM compact gate (ZKL-81 T6-A).
// Supports shadow (plan only, no LLM/step/item body) and enforced (full path later).
// Evaluated only at run creation and frozen into the run snapshot.
type CompactionRollout struct {
	Enabled            bool     `yaml:"enabled"`
	AllowAllWorkspaces bool     `yaml:"allowAllWorkspaces"`
	WorkspaceIDs       []string `yaml:"workspaceIds"`
	// Mode is disabled|shadow|enforced. Empty + Enabled=false → disabled.
	Mode           string `yaml:"mode"`
	RolloutVersion string `yaml:"rolloutVersion"`
}

// AllowsWorkspace reports whether the workspace may create session-context.v1 runs.
// Fail-closed: requires Enabled, mode not disabled, and allowlist/allowAll.
func (feature SessionContextRollout) AllowsWorkspace(workspaceID string) bool {
	if !feature.Enabled {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(feature.Mode))
	if mode == "disabled" {
		return false
	}
	// Empty mode with Enabled=true is treated as eligible for snapshot writing
	// (used by IC-03); shadow/enforced semantics expand in IC-10.
	return allowlistedWorkspace(feature.AllowAllWorkspaces, feature.WorkspaceIDs, workspaceID)
}

// AllowsCompaction reports whether the workspace may freeze session-context.v2
// compact knobs on new runs. Independent of parent allowlist; fail-closed.
func (feature SessionContextRollout) AllowsCompaction(workspaceID string) bool {
	return feature.Compaction.AllowsWorkspace(workspaceID)
}

// AllowsWorkspace reports whether compaction gate admits the workspace.
func (feature CompactionRollout) AllowsWorkspace(workspaceID string) bool {
	if !feature.Enabled {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(feature.Mode))
	if mode == "" || mode == "disabled" {
		return false
	}
	// shadow and enforced both freeze v2 snapshot at create time; execution of
	// compact LLM is still gate-mode dependent in later ICs.
	if mode != "shadow" && mode != "enforced" {
		return false
	}
	return allowlistedWorkspace(feature.AllowAllWorkspaces, feature.WorkspaceIDs, workspaceID)
}

// IsShadow reports shadow mode (plan only; no LLM/step/item body writes later).
func (feature CompactionRollout) IsShadow() bool {
	return strings.ToLower(strings.TrimSpace(feature.Mode)) == "shadow" && feature.Enabled
}

// IsEnforced reports enforced mode.
func (feature CompactionRollout) IsEnforced() bool {
	return strings.ToLower(strings.TrimSpace(feature.Mode)) == "enforced" && feature.Enabled
}

func allowlistedWorkspace(allowAll bool, ids []string, workspaceID string) bool {
	workspaceID = strings.ToLower(strings.TrimSpace(workspaceID))
	if workspaceID == "" {
		return false
	}
	if allowAll {
		return true
	}
	for _, id := range ids {
		if strings.ToLower(strings.TrimSpace(id)) == workspaceID {
			return true
		}
	}
	return false
}

// Normalized trims IDs and mode.
func (feature SessionContextRollout) Normalized() SessionContextRollout {
	out := SessionContextRollout{
		Enabled:            feature.Enabled,
		AllowAllWorkspaces: feature.AllowAllWorkspaces,
		Mode:               strings.ToLower(strings.TrimSpace(feature.Mode)),
		RolloutVersion:     strings.TrimSpace(feature.RolloutVersion),
		Compaction:         feature.Compaction.Normalized(),
	}
	if out.Mode == "" && !out.Enabled {
		out.Mode = "disabled"
	}
	if out.RolloutVersion == "" {
		out.RolloutVersion = "session-context-default"
	}
	for _, id := range feature.WorkspaceIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" {
			out.WorkspaceIDs = append(out.WorkspaceIDs, id)
		}
	}
	return out
}

// Normalized trims compaction gate fields; default remains closed.
func (feature CompactionRollout) Normalized() CompactionRollout {
	out := CompactionRollout{
		Enabled:            feature.Enabled,
		AllowAllWorkspaces: feature.AllowAllWorkspaces,
		Mode:               strings.ToLower(strings.TrimSpace(feature.Mode)),
		RolloutVersion:     strings.TrimSpace(feature.RolloutVersion),
	}
	if out.Mode == "" {
		out.Mode = "disabled"
	}
	if out.RolloutVersion == "" {
		out.RolloutVersion = "context-compaction-default"
	}
	for _, id := range feature.WorkspaceIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" {
			out.WorkspaceIDs = append(out.WorkspaceIDs, id)
		}
	}
	return out
}

// Normalized returns a copy with defaults applied (engine, budgets, ID lists).
// Does not apply PR15 agent staged defaults — those run only in applyRuntimeDefaults
// during Load so explicit construction stays under caller control.
func (cfg RuntimeConfig) Normalized() RuntimeConfig {
	return RuntimeConfig{
		Agent:             cfg.Agent.Normalized(),
		Workflow:          cfg.Workflow.Normalized(),
		Eino:              cfg.Eino.Normalized(),
		ModelVerification: cfg.ModelVerification.Normalized(),
		SessionContext:    cfg.SessionContext.Normalized(),
		ToolDisclosure:    cfg.ToolDisclosure.Normalized(),
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
	// Exactly 0 → default 16. Negative and >16 are left for validateRuntimeConfig
	// to reject (no silent clamp/default of invalid values).
	if config.Runtime.Eino.MaxToolInvocations == 0 {
		config.Runtime.Eino.MaxToolInvocations = DefaultEinoMaxToolInvocations
	}
	// Exactly 0 → default 120s. Negative and above-maximum values are left for
	// validateRuntimeConfig to reject.
	if config.Runtime.ModelVerification.TimeoutSeconds == 0 {
		config.Runtime.ModelVerification.TimeoutSeconds = DefaultModelVerificationTimeoutSeconds
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

	// ZKL-69 create-preview purge pacing defaults (business TTL remains fixed).
	if config.AgentPrompt.PreviewPurge.IntervalSeconds <= 0 {
		config.AgentPrompt.PreviewPurge.IntervalSeconds = 300
	}
	if config.AgentPrompt.PreviewPurge.BatchLimit <= 0 {
		config.AgentPrompt.PreviewPurge.BatchLimit = 100
	}
	if config.AgentPrompt.PreviewPurge.ClaimLeaseSeconds <= 0 {
		config.AgentPrompt.PreviewPurge.ClaimLeaseSeconds = 120
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
	if raw, ok := lookup("ACTWEAVE_RUNTIME_MODEL_VERIFICATION_TIMEOUT_SECONDS"); ok {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return errors.New("ACTWEAVE_RUNTIME_MODEL_VERIFICATION_TIMEOUT_SECONDS must be an integer")
		}
		config.Runtime.ModelVerification.TimeoutSeconds = value
	}
	return nil
}

func validateRuntimeConfig(cfg RuntimeConfig) error {
	if err := validateRuntimeFeatureRollout("runtime.agent", cfg.Agent); err != nil {
		return err
	}
	if err := validateRuntimeFeatureRollout("runtime.toolDisclosure", cfg.ToolDisclosure); err != nil {
		return err
	}
	if err := validateWorkflowRuntimeConfig(cfg.Workflow); err != nil {
		return err
	}
	if cfg.Eino.MaxIterations <= 0 {
		return errors.New("runtime.eino.maxIterations must be a positive integer")
	}
	// After applyRuntimeDefaults, 0 has already become 16. Reject negative and >16
	// (and any residual 0 if defaults were bypassed) — 1..16 only at this boundary.
	if cfg.Eino.MaxToolInvocations < 1 || cfg.Eino.MaxToolInvocations > DefaultEinoMaxToolInvocations {
		return fmt.Errorf(
			"runtime.eino.maxToolInvocations must be 1..%d (0 defaults to %d at load), got %d",
			DefaultEinoMaxToolInvocations, DefaultEinoMaxToolInvocations, cfg.Eino.MaxToolInvocations,
		)
	}
	// After applyRuntimeDefaults 0 has already become 90. A residual 0 can only
	// come from a struct built without Load, where application.Open applies
	// Normalized() before handing the duration to NewVerificationService, so 0
	// stays accepted here. Negative and above-maximum always fail closed.
	if err := validateModelVerificationTimeoutSeconds(cfg.ModelVerification.TimeoutSeconds); err != nil {
		return err
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
