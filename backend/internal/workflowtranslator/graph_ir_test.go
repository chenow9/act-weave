package workflowtranslator_test

import (
	"testing"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflowtranslator"
)

func TestBuildGraphIRSmallCorePlan(t *testing.T) {
	t.Parallel()

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-order",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start", Config: map[string]any{
				"inputSchema": map[string]any{"orderId": "string"},
			}},
			{NodeID: "tool", Type: "Tool", Dependencies: []string{"start"}, Config: map[string]any{
				"toolId": "order.status.query",
			}},
			{NodeID: "branch", Type: "Condition", Dependencies: []string{"tool"}, Config: map[string]any{
				"expression": "nodeOutputs.tool.status == 'paid'",
			}},
			{NodeID: "approval", Type: "Approval", Dependencies: []string{"branch"}, IncomingBranch: "paid", Config: map[string]any{
				"reason": "manual confirmation",
			}},
			{NodeID: "transform", Type: "Transform", Dependencies: []string{"branch"}, IncomingBranch: "default", Config: map[string]any{
				"template": "skip",
			}},
			{NodeID: "end", Type: "End", Dependencies: []string{"approval", "transform"}},
		},
	}

	ir, err := workflowtranslator.BuildGraphIR(plan, workflowtranslator.EngineEinoCore)
	if err != nil {
		t.Fatalf("BuildGraphIR: %v", err)
	}
	if ir.WorkflowID != "wf-order" {
		t.Fatalf("WorkflowID=%q", ir.WorkflowID)
	}
	if ir.Engine != workflowtranslator.EngineEinoCore {
		t.Fatalf("Engine=%q", ir.Engine)
	}
	if len(ir.Nodes) != 6 {
		t.Fatalf("nodes=%d", len(ir.Nodes))
	}
	if ir.Nodes[1].ID != "tool" || ir.Nodes[1].Config["toolId"] != "order.status.query" {
		t.Fatalf("tool node not preserved: %#v", ir.Nodes[1])
	}

	// Config isolation: mutating plan must not change IR.
	plan.Nodes[1].Config["toolId"] = "mutated"
	if ir.Nodes[1].Config["toolId"] != "order.status.query" {
		t.Fatalf("IR config must be cloned, got %#v", ir.Nodes[1].Config)
	}

	// Edges from Dependencies.
	if len(ir.Edges) < 5 {
		t.Fatalf("expected edges from dependencies, got %#v", ir.Edges)
	}
	foundPaid := false
	foundDefault := false
	for _, e := range ir.Edges {
		if e.From == "branch" && e.To == "approval" && e.Branch == "paid" {
			foundPaid = true
		}
		if e.From == "branch" && e.To == "transform" && e.Branch == "default" {
			foundDefault = true
		}
	}
	if !foundPaid || !foundDefault {
		t.Fatalf("branch edges missing: paid=%v default=%v edges=%#v", foundPaid, foundDefault, ir.Edges)
	}

	// Branches map for GraphBranch selection.
	targets := ir.Branches["branch"]
	if len(targets) != 2 {
		t.Fatalf("Branches[branch]=%#v", targets)
	}
	branchSet := map[string]string{}
	for _, t := range targets {
		branchSet[t.Branch] = t.TargetNode
	}
	if branchSet["paid"] != "approval" || branchSet["default"] != "transform" {
		t.Fatalf("branch targets=%#v", branchSet)
	}

	// Approval coverage on IR.
	if ir.Coverage.ByType["Approval"].Status != workflowtranslator.CoverageNative {
		t.Fatalf("IR coverage Approval: %#v", ir.Coverage.ByType["Approval"])
	}
}

func TestBuildGraphIRRejectsUnknownOnEinoCore(t *testing.T) {
	t.Parallel()

	// ForEach is native after PR13d; unknown types still reject under eino_core.
	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-unknown",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "llm", Type: "LLM", Dependencies: []string{"start"}},
			{NodeID: "end", Type: "End", Dependencies: []string{"llm"}},
		},
	}

	_, err := workflowtranslator.BuildGraphIR(plan, workflowtranslator.EngineEinoCore)
	if err == nil {
		t.Fatal("expected error for unknown node under eino_core")
	}

	loose := workflowtranslator.BuildGraphIRLoose(plan, workflowtranslator.EngineEinoCore)
	if len(loose.Nodes) != 3 {
		t.Fatalf("loose IR nodes=%d", len(loose.Nodes))
	}
	if len(loose.Coverage.Unsupported) != 1 || loose.Coverage.Unsupported[0] != "LLM" {
		t.Fatalf("loose unsupported=%v", loose.Coverage.Unsupported)
	}
}

func TestBuildGraphIRAcceptsForEachOnEinoCore(t *testing.T) {
	t.Parallel()

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-foreach",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "foreach",
				Type:         "ForEach",
				Dependencies: []string{"start"},
				Config: map[string]any{
					"collection": map[string]any{"kind": "ref", "path": "input.items"},
					"itemAlias":  "item",
				},
			},
			{NodeID: "end", Type: "End", Dependencies: []string{"foreach"}},
		},
	}

	ir, err := workflowtranslator.BuildGraphIR(plan, workflowtranslator.EngineEinoCore)
	if err != nil {
		t.Fatalf("BuildGraphIR eino_core ForEach: %v", err)
	}
	if ir.Nodes[1].Type != "ForEach" {
		t.Fatalf("ForEach node missing: %#v", ir.Nodes)
	}
	if ir.Coverage.ByType["ForEach"].Status != workflowtranslator.CoverageNative {
		t.Fatalf("ForEach coverage: %#v", ir.Coverage.ByType["ForEach"])
	}
}

func TestBuildGraphIRAcceptsSubWorkflowOnEinoCore(t *testing.T) {
	t.Parallel()

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-sub",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "sub",
				Type:         "SubWorkflow",
				Dependencies: []string{"start"},
				Config:       map[string]any{"workflowId": "wf-child"},
			},
			{NodeID: "end", Type: "End", Dependencies: []string{"sub"}},
		},
	}

	ir, err := workflowtranslator.BuildGraphIR(plan, workflowtranslator.EngineEinoCore)
	if err != nil {
		t.Fatalf("SubWorkflow must BuildGraphIR under eino_core: %v", err)
	}
	if ir.Coverage.ByType["SubWorkflow"].Status != workflowtranslator.CoverageNative {
		t.Fatalf("IR coverage SubWorkflow: %#v", ir.Coverage.ByType["SubWorkflow"])
	}
	if ir.Nodes[1].Type != "SubWorkflow" || ir.Nodes[1].Config["workflowId"] != "wf-child" {
		t.Fatalf("SubWorkflow node not preserved: %#v", ir.Nodes[1])
	}
}

func TestBuildGraphIRAcceptsHTTPOnEinoCore(t *testing.T) {
	t.Parallel()

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-http",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "http",
				Type:         "HTTP",
				Dependencies: []string{"start"},
				Config: map[string]any{
					"method":   "POST",
					"endpoint": "/orders/check",
				},
			},
			{NodeID: "end", Type: "End", Dependencies: []string{"http"}},
		},
	}

	ir, err := workflowtranslator.BuildGraphIR(plan, workflowtranslator.EngineEinoCore)
	if err != nil {
		t.Fatalf("HTTP must BuildGraphIR under eino_core: %v", err)
	}
	if ir.Coverage.ByType["HTTP"].Status != workflowtranslator.CoverageNative {
		t.Fatalf("IR coverage HTTP: %#v", ir.Coverage.ByType["HTTP"])
	}
	if ir.Nodes[1].Type != "HTTP" || ir.Nodes[1].Config["method"] != "POST" {
		t.Fatalf("HTTP node not preserved: %#v", ir.Nodes[1])
	}
	foundEdge := false
	for _, e := range ir.Edges {
		if e.From == "start" && e.To == "http" {
			foundEdge = true
		}
	}
	if !foundEdge {
		t.Fatalf("expected start → http edge, got %#v", ir.Edges)
	}
}

func TestBuildGraphIRAcceptsParallelOnEinoCore(t *testing.T) {
	t.Parallel()

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-parallel",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "parallel",
				Type:         "Parallel",
				Dependencies: []string{"start"},
				Config:       map[string]any{"branches": []any{"a", "b"}},
			},
			{NodeID: "a", Type: "Transform", Dependencies: []string{"parallel"}, Config: map[string]any{"template": "A"}},
			{NodeID: "b", Type: "Transform", Dependencies: []string{"parallel"}, Config: map[string]any{"template": "B"}},
			{NodeID: "end", Type: "End", Dependencies: []string{"a", "b"}},
		},
	}

	ir, err := workflowtranslator.BuildGraphIR(plan, workflowtranslator.EngineEinoCore)
	if err != nil {
		t.Fatalf("Parallel must BuildGraphIR under eino_core: %v", err)
	}
	if ir.Coverage.ByType["Parallel"].Status != workflowtranslator.CoverageNative {
		t.Fatalf("IR coverage Parallel: %#v", ir.Coverage.ByType["Parallel"])
	}
	// Fan-out edges: parallel → a, parallel → b (not GraphBranch — that is Condition-only).
	edgeFromParallel := 0
	for _, e := range ir.Edges {
		if e.From == "parallel" && (e.To == "a" || e.To == "b") {
			if e.Branch != "" {
				t.Fatalf("Parallel successors must not be Condition branch edges: %#v", e)
			}
			edgeFromParallel++
		}
	}
	if edgeFromParallel != 2 {
		t.Fatalf("expected 2 fan-out edges from Parallel, got %d edges=%#v", edgeFromParallel, ir.Edges)
	}
	if ir.Branches != nil {
		if _, ok := ir.Branches["parallel"]; ok {
			t.Fatal("Parallel must not populate GraphIR.Branches (Condition only)")
		}
	}
}

func TestBuildGraphIRRejectsUnknownType(t *testing.T) {
	t.Parallel()

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-unknown",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "llm", Type: "LLM"},
		},
	}
	_, err := workflowtranslator.BuildGraphIR(plan, workflowtranslator.EngineEino)
	if err == nil {
		t.Fatal("expected error for unknown LLM node")
	}
}
