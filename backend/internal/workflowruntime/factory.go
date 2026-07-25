package workflowruntime

import (
	"strings"

	"actweave/backend/internal/config"
	"actweave/backend/internal/domain"
	"actweave/backend/internal/einoruntime"

	"github.com/cloudwego/eino/compose"
)

// Engine mode names (aligned with config.WorkflowEngine* / workflowtranslator).
const (
	EngineWrapper  = config.WorkflowEngineWrapper
	EngineEinoCore = config.WorkflowEngineEinoCore
	EngineEino     = config.WorkflowEngineEino
)

// Compile-time: factory outputs satisfy the published-run executor surface.
var (
	_ CompiledPlanExecutor = WrappedPlanRunner{}
	_ CompiledPlanExecutor = EinoCoreRunner{}
	_ CompiledPlanExecutor = (*workspaceRoutingExecutor)(nil)
)

// ExecutorFactoryConfig selects a CompiledPlanExecutor implementation.
//
// Empty Engine in this struct / zero WorkflowRuntimeConfig (no Load) still
// yields wrapper for unit tests. Production config.Load stages engine=eino
// (compose) via applyRuntimeDefaults; wrapper remains the explicit rollback.
// eino and eino_core share EinoCoreRunner (same runner).
type ExecutorFactoryConfig struct {
	// Engine is wrapper | eino_core | eino. Empty → wrapper.
	Engine string
	// Invoker is required for Tool nodes on all engines.
	Invoker ToolInvoker
	// RevisionResolver is optional for wrapper SubWorkflow and required for
	// eino_core/eino SubWorkflow nested runs (PR13c).
	RevisionResolver WorkflowRevisionResolver
	// CheckPointStore is used by eino_core Approval interrupt. When nil,
	// EinoCoreRunner runs without durable compose checkpoints (tests may inject
	// an in-memory store via NewEinoCoreRunnerWithInvoker).
	CheckPointStore compose.CheckPointStore
	// GraphCache is optional process-local compile cache for eino_core.
	GraphCache *einoruntime.GraphCache
}

// NewExecutorFromConfig builds a CompiledPlanExecutor from RuntimeConfig.Workflow
// plus invoker/store deps (design §7.3).
//
// Empty/zero config (Normalized) → wrapper. After process Load, Engine is
// typically eino with allowAll (P0). When Engine is eino_core/eino, workspace
// gray-release routes per ExecutionContext.WorkspaceID; non-allowed workspaces
// stay on wrapper. Empty allowlist + allowAll=false fail-closes to wrapper.
func NewExecutorFromConfig(
	workflowCfg config.WorkflowRuntimeConfig,
	factoryCfg ExecutorFactoryConfig,
) CompiledPlanExecutor {
	normalized := workflowCfg.Normalized()
	wrapperCfg := factoryCfg
	wrapperCfg.Engine = EngineWrapper
	wrapper := NewCompiledPlanExecutor(wrapperCfg)

	engine := normalized.Engine
	if engine == "" || engine == EngineWrapper {
		return wrapper
	}
	// Fail-closed: non-wrapper engine with empty allowlist → everyone on wrapper.
	if !normalized.AllowAllWorkspaces && len(normalized.WorkspaceIDs) == 0 {
		return wrapper
	}

	einoCfg := factoryCfg
	einoCfg.Engine = engine
	einoExec := NewCompiledPlanExecutor(einoCfg)

	if normalized.AllowAllWorkspaces {
		return einoExec
	}
	return &workspaceRoutingExecutor{
		cfg:     normalized,
		wrapper: wrapper,
		eino:    einoExec,
	}
}

// NewCompiledPlanExecutor returns the executor for the requested engine.
//
// Unknown engines fall back to wrapper (fail-safe). Full eino mode currently
// uses the same core graph runner as eino_core. Parallel (PR13a), HTTP
// simulation (PR13b), SubWorkflow nested + CompositeInterrupt (PR13c), and
// ForEach scoped iteration (PR13d) are native under eino_core.
func NewCompiledPlanExecutor(cfg ExecutorFactoryConfig) CompiledPlanExecutor {
	engine := strings.ToLower(strings.TrimSpace(cfg.Engine))
	if engine == "" {
		engine = EngineWrapper
	}
	switch engine {
	case EngineEinoCore, EngineEino:
		store := cfg.CheckPointStore
		if store == nil {
			// compose Invoke always passes WithCheckPointID; a store is required.
			// Production wires PostgresCheckPointStore via NewExecutorFromConfig.
			// Nil → process-local mem store (tests / accidental misconfig).
			store = newMemCheckPointStore()
		}
		return NewEinoCoreRunner(EinoCoreRunnerConfig{
			Invoker:          cfg.Invoker,
			RevisionResolver: cfg.RevisionResolver,
			CheckPointStore:  store,
			Cache:            cfg.GraphCache,
		})
	default:
		if cfg.RevisionResolver != nil {
			return NewWrappedPlanRunnerWithRevisionResolver(cfg.Invoker, cfg.RevisionResolver)
		}
		return NewWrappedPlanRunner(cfg.Invoker)
	}
}

// SelectWorkflowEngine applies workspace gray-release for non-wrapper engines.
// When the workspace is not allowed, returns wrapper.
func SelectWorkflowEngine(cfg config.WorkflowRuntimeConfig, workspaceID string) string {
	normalized := cfg.Normalized()
	engine := normalized.Engine
	if engine == "" || engine == EngineWrapper {
		return EngineWrapper
	}
	if !normalized.AllowsWorkspace(workspaceID) {
		return EngineWrapper
	}
	return engine
}

// workspaceRoutingExecutor selects wrapper vs eino_core/eino per workspace
// using WorkflowRuntimeConfig gray-release.
type workspaceRoutingExecutor struct {
	cfg     config.WorkflowRuntimeConfig
	wrapper CompiledPlanExecutor
	eino    CompiledPlanExecutor
}

func (e *workspaceRoutingExecutor) selectEngine(workspaceID string) CompiledPlanExecutor {
	if e == nil || e.wrapper == nil {
		return nil
	}
	if e.eino != nil && SelectWorkflowEngine(e.cfg, workspaceID) != EngineWrapper {
		return e.eino
	}
	return e.wrapper
}

func (e *workspaceRoutingExecutor) Run(
	plan domain.CompiledExecutionPlan,
	ctx ExecutionContext,
) (domain.Execution, error) {
	return e.selectEngine(ctx.WorkspaceID).Run(plan, ctx)
}

func (e *workspaceRoutingExecutor) RunWithCheckpoint(
	plan domain.CompiledExecutionPlan,
	ctx ExecutionContext,
) (domain.Execution, *WorkflowApprovalCheckpoint, error) {
	return e.selectEngine(ctx.WorkspaceID).RunWithCheckpoint(plan, ctx)
}

func (e *workspaceRoutingExecutor) ResumeApproval(
	plan domain.CompiledExecutionPlan,
	ctx ExecutionContext,
	checkpoint WorkflowApprovalCheckpoint,
	decision ApprovalResumeDecision,
) (domain.Execution, error) {
	// Prefer eino when the checkpoint carries a compose key; otherwise route
	// by workspace gray-release (wrapper ConfirmApproval vs compose resume).
	if strings.TrimSpace(checkpoint.EinoCheckPointID) != "" && e.eino != nil {
		return e.eino.ResumeApproval(plan, ctx, checkpoint, decision)
	}
	return e.selectEngine(ctx.WorkspaceID).ResumeApproval(plan, ctx, checkpoint, decision)
}
