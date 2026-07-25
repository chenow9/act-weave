package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"actweave/backend/internal/config"
	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflowcompiler"
	"actweave/backend/internal/workflowruntime"
)

// publishedToolFixtureID stands in for a published HTTP tool capability id on a
// workspace catalog (smart-dag.v2 toolId). The invoker below models a successful
// invoke against that published release + mock connection.
const publishedToolFixtureID = "118f1f2e-7b5a-7c3d-8e9f-1234567890aa"

// publishedToolFixtureInvoker simulates DirectInvocation of a published HTTP
// tool (resolve release + mock connection + 200). Real wiring uses
// application.workflowToolInvoker → tool.DirectInvocationService.
type publishedToolFixtureInvoker struct {
	calls []string
	err   error
}

func (i *publishedToolFixtureInvoker) Invoke(
	toolID string,
	input map[string]any,
	ctx workflowruntime.ToolInvocationContext,
) (map[string]any, error) {
	i.calls = append(i.calls, toolID+"@"+ctx.WorkspaceID)
	if i.err != nil {
		return nil, i.err
	}
	return map[string]any{
		"ok":     true,
		"toolId": toolID,
		"trial":  true,
		"input":  input,
	}, nil
}

func TestTrialSucceedsWithPublishedToolNode(t *testing.T) {
	repository, _, _, compilation := createCompiledToolWorkflow(t, publishedToolFixtureID)
	inv := &publishedToolFixtureInvoker{}
	runner, err := NewRuntimeTrialRunner(workflowruntime.NewPlanRunner(inv))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewTrialService(repository, runner)
	if err != nil {
		t.Fatal(err)
	}
	trial, err := service.Run(
		context.Background(), draftWorkspaceID, draftCapabilityID, compilation.ID,
		draftOwnerID, json.RawMessage(`{"orderId":"A-1"}`),
	)
	if err != nil {
		t.Fatalf("trial with Tool node: %v trial=%+v", err, trial)
	}
	if trial.Status != "SUCCEEDED" {
		t.Fatalf("status=%s want SUCCEEDED", trial.Status)
	}
	if len(inv.calls) != 1 || inv.calls[0] != publishedToolFixtureID+"@"+draftWorkspaceID {
		t.Fatalf("published tool fixture invokes=%v", inv.calls)
	}
}

func TestTrialFailsWhenPublishedToolInvokeErrors(t *testing.T) {
	repository, _, _, compilation := createCompiledToolWorkflow(t, publishedToolFixtureID)
	inv := &publishedToolFixtureInvoker{err: errors.New("mock connection refused")}
	runner, err := NewRuntimeTrialRunner(workflowruntime.NewPlanRunner(inv))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewTrialService(repository, runner)
	if err != nil {
		t.Fatal(err)
	}
	trial, err := service.Run(
		context.Background(), draftWorkspaceID, draftCapabilityID, compilation.ID,
		draftOwnerID, json.RawMessage(`{}`),
	)
	if !errors.Is(err, ErrTrialFailed) {
		t.Fatalf("expected ErrTrialFailed got %v trial=%+v", err, trial)
	}
	if trial.Status != "FAILED" {
		t.Fatalf("status=%s", trial.Status)
	}
}

func TestTrialSucceedsWithToolAndApprovalAutoConfirm(t *testing.T) {
	// Realistic smart-dag.v2 topology: Start → Tool → Approval → End.
	// Before TrialMode, Approval mapped to trial FAILED (409).
	repository, db := newDraftRepositoryTest(t)
	_ = db
	graph := json.RawMessage(`{
		"schemaVersion":"workflow.graph.v1",
		"nodes":[
			{"id":"start","type":"Start"},
			{"id":"tool","type":"Tool","data":{"toolId":"` + publishedToolFixtureID + `"}},
			{"id":"approval","type":"Approval","data":{"reason":"manual review"}},
			{"id":"end","type":"End","data":{"output":{"kind":"literal","value":"ok"}}}
		],
		"edges":[
			{"id":"e1","sourceNodeId":"start","targetNodeId":"tool"},
			{"id":"e2","sourceNodeId":"tool","targetNodeId":"approval"},
			{"id":"e3","sourceNodeId":"approval","targetNodeId":"end"}
		]
	}`)
	input := validWorkflowCreateInput()
	input.Graph = graph
	if _, _, err := repository.Create(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	compiler, err := NewCompilationService(repository, workflowcompiler.New())
	if err != nil {
		t.Fatal(err)
	}
	compilation, err := compiler.Compile(context.Background(), draftWorkspaceID, draftCapabilityID, draftOwnerID)
	if err != nil || compilation.Status != "VALID" {
		t.Fatalf("compile: %+v err=%v", compilation, err)
	}

	inv := &publishedToolFixtureInvoker{}
	// PlanRunner path (wrapper) — TrialMode auto-confirms Approval.
	runner, err := NewRuntimeTrialRunner(workflowruntime.NewPlanRunner(inv))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewTrialService(repository, runner)
	if err != nil {
		t.Fatal(err)
	}
	trial, err := service.Run(
		context.Background(), draftWorkspaceID, draftCapabilityID, compilation.ID,
		draftOwnerID, json.RawMessage(`{}`),
	)
	if err != nil {
		t.Fatalf("trial Tool+Approval: %v trial=%+v", err, trial)
	}
	if trial.Status != "SUCCEEDED" {
		t.Fatalf("status=%s want SUCCEEDED", trial.Status)
	}
	if len(inv.calls) != 1 {
		t.Fatalf("tool invokes=%d want 1", len(inv.calls))
	}
}

func TestTrialSucceedsWithToolAndApprovalUnderEinoEngine(t *testing.T) {
	repository, _, _, compilation := createCompiledToolApprovalWorkflow(t, publishedToolFixtureID)
	inv := &publishedToolFixtureInvoker{}
	exec := workflowruntime.NewExecutorFromConfig(
		config.WorkflowRuntimeConfig{Engine: config.WorkflowEngineEino, AllowAllWorkspaces: true},
		workflowruntime.ExecutorFactoryConfig{Invoker: inv},
	)
	runner, err := NewRuntimeTrialRunner(exec)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewTrialService(repository, runner)
	if err != nil {
		t.Fatal(err)
	}
	trial, err := service.Run(
		context.Background(), draftWorkspaceID, draftCapabilityID, compilation.ID,
		draftOwnerID, json.RawMessage(`{}`),
	)
	if err != nil {
		t.Fatalf("eino trial Tool+Approval: %v trial=%+v", err, trial)
	}
	if trial.Status != "SUCCEEDED" {
		t.Fatalf("status=%s want SUCCEEDED", trial.Status)
	}
	if len(inv.calls) != 1 {
		t.Fatalf("tool invokes=%d want 1", len(inv.calls))
	}
}

func TestProductionPathStillPausesOnApprovalWithoutTrialMode(t *testing.T) {
	// Guard D11: non-trial ExecutionContext must still pause on Approval.
	inv := &publishedToolFixtureInvoker{}
	runner := workflowruntime.NewPlanRunner(inv)
	plan := domain.CompiledExecutionPlan{
		WorkflowID: draftCapabilityID,
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "tool", Type: "Tool", Dependencies: []string{"start"},
				Config: map[string]any{"toolId": publishedToolFixtureID}},
			{NodeID: "approval", Type: "Approval", Dependencies: []string{"tool"},
				Config: map[string]any{"reason": "prod"}},
			{NodeID: "end", Type: "End", Dependencies: []string{"approval"},
				Config: map[string]any{"output": map[string]any{"kind": "literal", "value": "ok"}}},
		},
	}
	execution, err := runner.Run(plan, workflowruntime.ExecutionContext{
		UserID: draftOwnerID, WorkspaceID: draftWorkspaceID, Input: map[string]any{},
		Trigger: "console", TrialMode: false,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if execution.Status != domain.ExecutionApproval {
		t.Fatalf("production must pause on Approval, got %s", execution.Status)
	}
	if len(inv.calls) != 1 {
		t.Fatalf("tool should run before Approval pause, calls=%d", len(inv.calls))
	}
}

func createCompiledToolWorkflow(t *testing.T, toolID string) (*Repository, interface{ Close() error }, Draft, Compilation) {
	t.Helper()
	repository, db := newDraftRepositoryTest(t)
	graph := json.RawMessage(`{
		"schemaVersion":"workflow.graph.v1",
		"nodes":[
			{"id":"start","type":"Start"},
			{"id":"tool","type":"Tool","data":{"toolId":"` + toolID + `"}},
			{"id":"end","type":"End","data":{"output":{"kind":"literal","value":"ok"}}}
		],
		"edges":[
			{"id":"e1","sourceNodeId":"start","targetNodeId":"tool"},
			{"id":"e2","sourceNodeId":"tool","targetNodeId":"end"}
		]
	}`)
	input := validWorkflowCreateInput()
	input.Graph = graph
	_, draft, err := repository.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewCompilationService(repository, workflowcompiler.New())
	if err != nil {
		t.Fatal(err)
	}
	compilation, err := service.Compile(context.Background(), draftWorkspaceID, draftCapabilityID, draftOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if compilation.Status != "VALID" {
		t.Fatalf("expected VALID compilation: %+v issues=%s", compilation, compilation.Issues)
	}
	return repository, db, draft, compilation
}

func createCompiledToolApprovalWorkflow(t *testing.T, toolID string) (*Repository, interface{ Close() error }, Draft, Compilation) {
	t.Helper()
	repository, db := newDraftRepositoryTest(t)
	graph := json.RawMessage(`{
		"schemaVersion":"workflow.graph.v1",
		"nodes":[
			{"id":"start","type":"Start"},
			{"id":"tool","type":"Tool","data":{"toolId":"` + toolID + `"}},
			{"id":"approval","type":"Approval","data":{"reason":"manual"}},
			{"id":"end","type":"End","data":{"output":{"kind":"literal","value":"ok"}}}
		],
		"edges":[
			{"id":"e1","sourceNodeId":"start","targetNodeId":"tool"},
			{"id":"e2","sourceNodeId":"tool","targetNodeId":"approval"},
			{"id":"e3","sourceNodeId":"approval","targetNodeId":"end"}
		]
	}`)
	input := validWorkflowCreateInput()
	input.Graph = graph
	_, draft, err := repository.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewCompilationService(repository, workflowcompiler.New())
	if err != nil {
		t.Fatal(err)
	}
	compilation, err := service.Compile(context.Background(), draftWorkspaceID, draftCapabilityID, draftOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if compilation.Status != "VALID" {
		t.Fatalf("expected VALID compilation: %+v", compilation)
	}
	return repository, db, draft, compilation
}
