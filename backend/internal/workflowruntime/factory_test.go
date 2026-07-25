package workflowruntime_test

import (
	"testing"

	"actweave/backend/internal/config"
	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflowruntime"
)

type factoryStubInvoker struct{}

func (factoryStubInvoker) Invoke(string, map[string]any, workflowruntime.ToolInvocationContext) (map[string]any, error) {
	return map[string]any{}, nil
}

func TestNewCompiledPlanExecutorDefaultWrapper(t *testing.T) {
	t.Parallel()
	exec := workflowruntime.NewCompiledPlanExecutor(workflowruntime.ExecutorFactoryConfig{
		Invoker: factoryStubInvoker{},
	})
	if _, ok := exec.(workflowruntime.WrappedPlanRunner); !ok {
		t.Fatalf("default engine should be WrappedPlanRunner, got %T", exec)
	}
}

func TestNewCompiledPlanExecutorEinoCore(t *testing.T) {
	t.Parallel()
	exec := workflowruntime.NewCompiledPlanExecutor(workflowruntime.ExecutorFactoryConfig{
		Engine:  workflowruntime.EngineEinoCore,
		Invoker: factoryStubInvoker{},
	})
	if _, ok := exec.(workflowruntime.EinoCoreRunner); !ok {
		t.Fatalf("eino_core should be EinoCoreRunner, got %T", exec)
	}
}

func TestSelectWorkflowEngineGray(t *testing.T) {
	t.Parallel()
	cfg := config.WorkflowRuntimeConfig{
		Engine:       config.WorkflowEngineEinoCore,
		WorkspaceIDs: []string{"ws-a"},
	}
	if got := workflowruntime.SelectWorkflowEngine(cfg, "ws-a"); got != workflowruntime.EngineEinoCore {
		t.Fatalf("allowed workspace: got %s", got)
	}
	if got := workflowruntime.SelectWorkflowEngine(cfg, "ws-other"); got != workflowruntime.EngineWrapper {
		t.Fatalf("denied workspace should fall back to wrapper, got %s", got)
	}
	if got := workflowruntime.SelectWorkflowEngine(config.WorkflowRuntimeConfig{}, "ws-a"); got != workflowruntime.EngineWrapper {
		t.Fatalf("empty config: got %s", got)
	}
}

func TestNewExecutorFromConfigZeroConfigWrapper(t *testing.T) {
	t.Parallel()
	// Zero config without Load stays on wrapper (Normalized empty → wrapper).
	exec := workflowruntime.NewExecutorFromConfig(
		config.WorkflowRuntimeConfig{},
		workflowruntime.ExecutorFactoryConfig{Invoker: factoryStubInvoker{}},
	)
	if _, ok := exec.(workflowruntime.WrappedPlanRunner); !ok {
		t.Fatalf("zero config should yield WrappedPlanRunner, got %T", exec)
	}
}

func TestNewExecutorFromConfigLoadStagedEino(t *testing.T) {
	t.Parallel()
	// Production Load stages engine=eino + allowAll → compose runner.
	exec := workflowruntime.NewExecutorFromConfig(
		config.WorkflowRuntimeConfig{
			Engine:             config.WorkflowEngineEino,
			AllowAllWorkspaces: true,
		},
		workflowruntime.ExecutorFactoryConfig{Invoker: factoryStubInvoker{}},
	)
	if _, ok := exec.(workflowruntime.EinoCoreRunner); !ok {
		t.Fatalf("staged Load eino should yield EinoCoreRunner, got %T", exec)
	}
}

func TestNewExecutorFromConfigEinoAliasSameRunner(t *testing.T) {
	t.Parallel()
	for _, engine := range []string{config.WorkflowEngineEino, config.WorkflowEngineEinoCore} {
		exec := workflowruntime.NewExecutorFromConfig(
			config.WorkflowRuntimeConfig{Engine: engine, AllowAllWorkspaces: true},
			workflowruntime.ExecutorFactoryConfig{Invoker: factoryStubInvoker{}},
		)
		if _, ok := exec.(workflowruntime.EinoCoreRunner); !ok {
			t.Fatalf("engine %q should yield EinoCoreRunner, got %T", engine, exec)
		}
	}
}

func TestNewExecutorFromConfigEinoCoreAllowAll(t *testing.T) {
	t.Parallel()
	exec := workflowruntime.NewExecutorFromConfig(
		config.WorkflowRuntimeConfig{
			Engine:             config.WorkflowEngineEinoCore,
			AllowAllWorkspaces: true,
		},
		workflowruntime.ExecutorFactoryConfig{Invoker: factoryStubInvoker{}},
	)
	if _, ok := exec.(workflowruntime.EinoCoreRunner); !ok {
		t.Fatalf("allow-all eino_core should yield EinoCoreRunner, got %T", exec)
	}
}

func TestNewExecutorFromConfigEinoCoreEmptyAllowlistFallsBackToWrapper(t *testing.T) {
	t.Parallel()
	exec := workflowruntime.NewExecutorFromConfig(
		config.WorkflowRuntimeConfig{Engine: config.WorkflowEngineEinoCore},
		workflowruntime.ExecutorFactoryConfig{Invoker: factoryStubInvoker{}},
	)
	if _, ok := exec.(workflowruntime.WrappedPlanRunner); !ok {
		t.Fatalf("eino_core with empty allowlist must stay wrapper, got %T", exec)
	}
}

func TestNewExecutorFromConfigRoutesByWorkspace(t *testing.T) {
	t.Parallel()
	exec := workflowruntime.NewExecutorFromConfig(
		config.WorkflowRuntimeConfig{
			Engine:       config.WorkflowEngineEinoCore,
			WorkspaceIDs: []string{"ws-allowed"},
		},
		workflowruntime.ExecutorFactoryConfig{Invoker: factoryStubInvoker{}},
	)
	// Routing executor is unexported; prove behavior via RunWithCheckpoint on a
	// core plan (eino_core) vs advanced HTTP plan on denied workspace (wrapper).
	corePlan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-core",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "end", Type: "End", Dependencies: []string{"start"}, Config: map[string]any{
				"output": map[string]any{"kind": "literal", "value": "ok"},
			}},
		},
	}
	// Allowed workspace uses eino_core.
	execResult, _, err := exec.RunWithCheckpoint(corePlan, workflowruntime.ExecutionContext{
		UserID: "u1", WorkspaceID: "ws-allowed",
	})
	if err != nil {
		t.Fatalf("allowed workspace core plan: %v", err)
	}
	if execResult.Status != domain.ExecutionSuccess {
		t.Fatalf("allowed workspace status=%s", execResult.Status)
	}

	// Denied → wrapper path; verify denied can run HTTP on wrapper and allowed
	// runs HTTP / ForEach on eino_core successfully (PR13b/d).
	foreachPlan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-foreach",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "foreach", Type: "ForEach", Dependencies: []string{"start"}, Config: map[string]any{
				"collection": map[string]any{"kind": "literal", "value": []any{"a"}},
				"itemAlias":  "item",
			}},
			{NodeID: "end", Type: "End", Dependencies: []string{"foreach"}, Config: map[string]any{
				"output": map[string]any{"kind": "ref", "path": "nodeOutputs.foreach.count"},
			}},
		},
	}
	httpPlan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-http",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "http", Type: "HTTP", Dependencies: []string{"start"}, Config: map[string]any{
				"method": "GET", "endpoint": "/health",
			}},
			{NodeID: "end", Type: "End", Dependencies: []string{"http"}, Config: map[string]any{}},
		},
	}
	httpResult, _, err := exec.RunWithCheckpoint(httpPlan, workflowruntime.ExecutionContext{
		UserID: "u1", WorkspaceID: "ws-denied",
	})
	if err != nil {
		t.Fatalf("denied workspace should use wrapper for HTTP: %v", err)
	}
	if httpResult.Status != domain.ExecutionSuccess {
		t.Fatalf("denied workspace HTTP status=%s err=%s", httpResult.Status, httpResult.ErrorMessage)
	}

	// Allowed workspace + HTTP succeeds on eino_core (PR13b native simulation).
	allowedHTTP, _, err := exec.RunWithCheckpoint(httpPlan, workflowruntime.ExecutionContext{
		UserID: "u1", WorkspaceID: "ws-allowed",
	})
	if err != nil {
		t.Fatalf("allowed workspace + HTTP should succeed under eino_core: %v", err)
	}
	if allowedHTTP.Status != domain.ExecutionSuccess {
		t.Fatalf("allowed workspace HTTP status=%s err=%s", allowedHTTP.Status, allowedHTTP.ErrorMessage)
	}

	// Allowed workspace + ForEach succeeds on eino_core (PR13d native scoped iteration).
	allowedForEach, _, err := exec.RunWithCheckpoint(foreachPlan, workflowruntime.ExecutionContext{
		UserID: "u1", WorkspaceID: "ws-allowed",
	})
	if err != nil {
		t.Fatalf("allowed workspace + ForEach should succeed under eino_core: %v", err)
	}
	if allowedForEach.Status != domain.ExecutionSuccess {
		t.Fatalf("allowed workspace ForEach status=%s err=%s", allowedForEach.Status, allowedForEach.ErrorMessage)
	}
}
