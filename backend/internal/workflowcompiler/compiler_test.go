package workflowcompiler

import (
	"testing"

	"actweave/backend/internal/domain"
)

func TestCompileRejectsCyclesWithEdgeContext(t *testing.T) {
	compiler := New()
	draft := domain.WorkflowGraphDraft{
		SchemaVersion: "workflow.graph.v1",
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start"},
			{ID: "tool", Type: "Tool", Data: map[string]any{"toolId": "order.status.query"}},
			{ID: "end", Type: "End"},
		},
		Edges: []domain.WorkflowGraphEdge{
			{ID: "edge-start-tool", SourceNodeID: "start", TargetNodeID: "tool"},
			{ID: "edge-tool-end", SourceNodeID: "tool", TargetNodeID: "end"},
			{ID: "edge-end-tool", SourceNodeID: "end", TargetNodeID: "tool"},
		},
	}

	compilation := compiler.Compile("wf-cycle", "draft-v1", draft)

	if compilation.Status != domain.WorkflowCompilationInvalid {
		t.Fatalf("expected invalid compilation, got %s", compilation.Status)
	}
	if len(compilation.Issues) == 0 {
		t.Fatalf("expected graph-stage issue, got none")
	}
	issue := compilation.Issues[0]
	if issue.SourceStage != domain.WorkflowIssueStageGraph {
		t.Fatalf("expected graph-stage issue, got %#v", compilation.Issues)
	}
	if issue.Code != "graph-cycle-detected" {
		t.Fatalf("expected cycle issue, got %#v", issue)
	}
	if issue.FieldPath != "edges" {
		t.Fatalf("expected cycle issue bound to graph edges, got %#v", issue)
	}
}

func TestCompileBuildsConditionAndForEachPlan(t *testing.T) {
	compiler := New()
	draft := domain.WorkflowGraphDraft{
		SchemaVersion: "workflow.graph.v1",
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start", Data: map[string]any{"inputSchema": map[string]any{"orderIds": "array"}}},
			{ID: "foreach", Type: "ForEach", Data: map[string]any{"collection": map[string]any{"kind": "ref", "path": "input.orderIds"}, "itemAlias": "orderId"}},
			{ID: "tool", Type: "Tool", Data: map[string]any{"toolId": "order.status.query"}},
			{ID: "branch", Type: "Condition", Data: map[string]any{"expression": "nodeOutputs.tool.status == 'paid'"}},
			{ID: "end", Type: "End", Data: map[string]any{"output": map[string]any{"kind": "ref", "path": "nodeOutputs.tool"}}},
		},
		Edges: []domain.WorkflowGraphEdge{
			{ID: "e1", SourceNodeID: "start", TargetNodeID: "foreach"},
			{ID: "e2", SourceNodeID: "foreach", TargetNodeID: "tool"},
			{ID: "e3", SourceNodeID: "tool", TargetNodeID: "branch"},
			{ID: "e4", SourceNodeID: "branch", TargetNodeID: "end", Data: map[string]any{"branch": "default"}},
		},
	}

	compilation := compiler.Compile("wf-foreach", "draft-v1", draft)

	if compilation.Status != domain.WorkflowCompilationValid {
		t.Fatalf("expected valid compilation, got %#v", compilation.Issues)
	}
	if compilation.Spec == nil || compilation.Plan == nil {
		t.Fatalf("expected spec and plan, got %#v", compilation)
	}
	if len(compilation.Spec.Nodes) != 5 {
		t.Fatalf("expected 5 spec nodes, got %#v", compilation.Spec.Nodes)
	}
	if len(compilation.Plan.Nodes) != 5 {
		t.Fatalf("expected 5 plan nodes, got %#v", compilation.Plan.Nodes)
	}
	if compilation.Plan.Nodes[1].NodeID != "foreach" || compilation.Plan.Nodes[1].Type != "ForEach" {
		t.Fatalf("expected foreach plan node, got %#v", compilation.Plan.Nodes)
	}
	if compilation.Plan.Nodes[1].Config["itemAlias"] != "orderId" {
		t.Fatalf("expected foreach item alias in plan config, got %#v", compilation.Plan.Nodes[1].Config)
	}
	if compilation.Plan.Nodes[4].IncomingBranch != "default" {
		t.Fatalf("expected default branch on end node, got %#v", compilation.Plan.Nodes[4])
	}
}

func TestCompileRejectsNodesNotReachableFromStart(t *testing.T) {
	compiler := New()
	draft := domain.WorkflowGraphDraft{
		SchemaVersion: "workflow.graph.v1",
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start"},
			{ID: "end", Type: "End"},
			{ID: "orphan", Type: "Tool", Data: map[string]any{"toolId": "order.status.query"}},
		},
		Edges: []domain.WorkflowGraphEdge{
			{ID: "edge-start-end", SourceNodeID: "start", TargetNodeID: "end"},
		},
	}

	compilation := compiler.Compile("wf-unreachable", "draft-v1", draft)

	if compilation.Status != domain.WorkflowCompilationInvalid {
		t.Fatalf("expected invalid compilation, got %#v", compilation)
	}
	if !hasIssue(compilation.Issues, "node-not-reachable-from-start", domain.WorkflowIssueStageGraph) {
		t.Fatalf("expected unreachable graph issue, got %#v", compilation.Issues)
	}
}

func TestCompilePreservesIncomingBranchAcrossMultipleDependencies(t *testing.T) {
	compiler := New()
	draft := domain.WorkflowGraphDraft{
		SchemaVersion: "workflow.graph.v1",
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start"},
			{ID: "branch", Type: "Condition", Data: map[string]any{"expression": "input.approved == true"}},
			{ID: "tool", Type: "Tool", Data: map[string]any{"toolId": "order.status.query"}},
			{ID: "end", Type: "End"},
		},
		Edges: []domain.WorkflowGraphEdge{
			{ID: "edge-start-branch", SourceNodeID: "start", TargetNodeID: "branch"},
			{ID: "edge-start-tool", SourceNodeID: "start", TargetNodeID: "tool"},
			{ID: "edge-branch-end", SourceNodeID: "branch", TargetNodeID: "end", Data: map[string]any{"branch": "approved"}},
			{ID: "edge-tool-end", SourceNodeID: "tool", TargetNodeID: "end"},
		},
	}

	compilation := compiler.Compile("wf-branch-join", "draft-v1", draft)

	if compilation.Status != domain.WorkflowCompilationValid {
		t.Fatalf("expected valid compilation, got %#v", compilation.Issues)
	}
	endNode := findPlanNode(t, compilation.Plan.Nodes, "end")
	if endNode.IncomingBranch != "approved" {
		t.Fatalf("expected approved branch on joined end node, got %#v", endNode)
	}
}

func TestCompileRejectsConflictingIncomingBranchLabels(t *testing.T) {
	compiler := New()
	draft := domain.WorkflowGraphDraft{
		SchemaVersion: "workflow.graph.v1",
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start"},
			{ID: "approval-branch", Type: "Condition", Data: map[string]any{"expression": "input.approved == true"}},
			{ID: "payment-branch", Type: "Condition", Data: map[string]any{"expression": "input.paid == true"}},
			{ID: "end", Type: "End"},
		},
		Edges: []domain.WorkflowGraphEdge{
			{ID: "edge-start-approval", SourceNodeID: "start", TargetNodeID: "approval-branch"},
			{ID: "edge-start-payment", SourceNodeID: "start", TargetNodeID: "payment-branch"},
			{ID: "edge-approval-end", SourceNodeID: "approval-branch", TargetNodeID: "end", Data: map[string]any{"branch": "approved"}},
			{ID: "edge-payment-end", SourceNodeID: "payment-branch", TargetNodeID: "end", Data: map[string]any{"branch": "paid"}},
		},
	}

	compilation := compiler.Compile("wf-conflicting-branches", "draft-v1", draft)

	if compilation.Status != domain.WorkflowCompilationInvalid {
		t.Fatalf("expected invalid compilation, got %#v", compilation)
	}
	if !hasIssue(compilation.Issues, "conflicting-incoming-branches", domain.WorkflowIssueStagePlan) {
		t.Fatalf("expected conflicting branch issue, got %#v", compilation.Issues)
	}
}

func TestCompileRejectsConditionWithMultipleNonDefaultOutgoingBranches(t *testing.T) {
	compiler := New()
	draft := domain.WorkflowGraphDraft{
		SchemaVersion: "workflow.graph.v1",
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start"},
			{ID: "branch", Type: "Condition", Data: map[string]any{"expression": "input.approved == true"}},
			{ID: "approved", Type: "Transform"},
			{ID: "rejected", Type: "End"},
			{ID: "fallback", Type: "End"},
		},
		Edges: []domain.WorkflowGraphEdge{
			{ID: "edge-start-branch", SourceNodeID: "start", TargetNodeID: "branch"},
			{ID: "edge-branch-approved", SourceNodeID: "branch", TargetNodeID: "approved", Data: map[string]any{"branch": "true"}},
			{ID: "edge-branch-rejected", SourceNodeID: "branch", TargetNodeID: "rejected", Data: map[string]any{"branch": "false"}},
			{ID: "edge-branch-fallback", SourceNodeID: "branch", TargetNodeID: "fallback", Data: map[string]any{"branch": "default"}},
		},
	}

	compilation := compiler.Compile("wf-condition-non-default", "draft-v1", draft)

	if compilation.Status != domain.WorkflowCompilationInvalid {
		t.Fatalf("expected invalid compilation, got %#v", compilation)
	}
	issue := findIssue(t, compilation.Issues, "condition-branch-non-default-limit", domain.WorkflowIssueStageGraph)
	if issue.EdgeID != "edge-branch-rejected" {
		t.Fatalf("expected branch issue bound to second non-default edge, got %#v", issue)
	}
}

func TestCompileRejectsConditionWithMultipleOutgoingEdgesWithoutDefaultBranch(t *testing.T) {
	compiler := New()
	draft := domain.WorkflowGraphDraft{
		SchemaVersion: "workflow.graph.v1",
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start"},
			{ID: "branch", Type: "Condition", Data: map[string]any{"expression": "input.approved == true"}},
			{ID: "approved", Type: "Transform"},
			{ID: "fallback", Type: "End"},
		},
		Edges: []domain.WorkflowGraphEdge{
			{ID: "edge-start-branch", SourceNodeID: "start", TargetNodeID: "branch"},
			{ID: "edge-branch-approved", SourceNodeID: "branch", TargetNodeID: "approved", Data: map[string]any{"branch": "true"}},
			{ID: "edge-branch-fallback", SourceNodeID: "branch", TargetNodeID: "fallback"},
		},
	}

	compilation := compiler.Compile("wf-condition-missing-default", "draft-v1", draft)

	if compilation.Status != domain.WorkflowCompilationInvalid {
		t.Fatalf("expected invalid compilation, got %#v", compilation)
	}
	issue := findIssue(t, compilation.Issues, "condition-branch-default-required", domain.WorkflowIssueStageGraph)
	if issue.EdgeID != "edge-branch-fallback" {
		t.Fatalf("expected missing default issue bound to an outgoing edge, got %#v", issue)
	}
}

func TestCompileAcceptsConditionWithDefaultAndOneNonDefaultBranch(t *testing.T) {
	compiler := New()
	draft := domain.WorkflowGraphDraft{
		SchemaVersion: "workflow.graph.v1",
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start"},
			{ID: "branch", Type: "Condition", Data: map[string]any{"expression": "input.approved == true"}},
			{ID: "approved", Type: "Transform"},
			{ID: "fallback", Type: "End"},
		},
		Edges: []domain.WorkflowGraphEdge{
			{ID: "edge-start-branch", SourceNodeID: "start", TargetNodeID: "branch"},
			{ID: "edge-branch-approved", SourceNodeID: "branch", TargetNodeID: "approved", Data: map[string]any{"branch": "true"}},
			{ID: "edge-branch-fallback", SourceNodeID: "branch", TargetNodeID: "fallback", Data: map[string]any{"branch": "default"}},
		},
	}

	compilation := compiler.Compile("wf-condition-valid", "draft-v1", draft)

	if compilation.Status != domain.WorkflowCompilationValid {
		t.Fatalf("expected valid compilation, got %#v", compilation.Issues)
	}
	approvedNode := findPlanNode(t, compilation.Plan.Nodes, "approved")
	if approvedNode.IncomingBranch != "true" {
		t.Fatalf("expected true branch in plan, got %#v", approvedNode)
	}
	fallbackNode := findPlanNode(t, compilation.Plan.Nodes, "fallback")
	if fallbackNode.IncomingBranch != "default" {
		t.Fatalf("expected default branch in plan, got %#v", fallbackNode)
	}
}

func TestCompileAcceptsNonConditionMultipleOutgoingEdgesWithoutBranchLabels(t *testing.T) {
	compiler := New()
	draft := domain.WorkflowGraphDraft{
		SchemaVersion: "workflow.graph.v1",
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start"},
			{ID: "tool", Type: "Tool", Data: map[string]any{"toolId": "order.status.query"}},
			{ID: "transform", Type: "Transform"},
			{ID: "end", Type: "End"},
		},
		Edges: []domain.WorkflowGraphEdge{
			{ID: "edge-start-tool", SourceNodeID: "start", TargetNodeID: "tool"},
			{ID: "edge-tool-transform", SourceNodeID: "tool", TargetNodeID: "transform"},
			{ID: "edge-tool-end", SourceNodeID: "tool", TargetNodeID: "end"},
		},
	}

	compilation := compiler.Compile("wf-non-condition-branches", "draft-v1", draft)

	if compilation.Status != domain.WorkflowCompilationValid {
		t.Fatalf("expected valid compilation, got %#v", compilation.Issues)
	}
}

func TestCompileBuildsAdvancedNodePlan(t *testing.T) {
	compiler := New()
	draft := domain.WorkflowGraphDraft{
		SchemaVersion: "workflow.graph.v1",
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start"},
			{ID: "parallel", Type: "Parallel", Data: map[string]any{"branches": []any{"http", "subworkflow"}}},
			{ID: "http", Type: "HTTP", Data: map[string]any{"method": "POST", "endpoint": "/orders/check"}},
			{ID: "sub", Type: "SubWorkflow", Data: map[string]any{"workflowId": "wf-fulfillment"}},
			{ID: "foreach", Type: "ForEach", Data: map[string]any{"collection": map[string]any{"kind": "ref", "path": "input.items"}, "itemAlias": "item", "concurrency": 2}},
			{ID: "end", Type: "End", Data: map[string]any{"output": map[string]any{"kind": "ref", "path": "nodeOutputs.foreach.count"}}},
		},
		Edges: []domain.WorkflowGraphEdge{
			{ID: "e1", SourceNodeID: "start", TargetNodeID: "parallel"},
			{ID: "e2", SourceNodeID: "parallel", TargetNodeID: "http"},
			{ID: "e3", SourceNodeID: "parallel", TargetNodeID: "sub"},
			{ID: "e4", SourceNodeID: "http", TargetNodeID: "foreach"},
			{ID: "e5", SourceNodeID: "sub", TargetNodeID: "end"},
			{ID: "e6", SourceNodeID: "foreach", TargetNodeID: "end"},
		},
	}

	compilation := compiler.Compile("wf-advanced", "draft-v1", draft)

	if compilation.Status != domain.WorkflowCompilationValid {
		t.Fatalf("expected valid advanced compilation, got %#v", compilation.Issues)
	}
	if compilation.Plan == nil || len(compilation.Plan.Nodes) != 6 {
		t.Fatalf("expected 6 plan nodes, got %#v", compilation.Plan)
	}
	httpNode := findPlanNode(t, compilation.Plan.Nodes, "http")
	if httpNode.Type != "HTTP" || httpNode.Config["method"] != "POST" || httpNode.Config["endpoint"] != "/orders/check" {
		t.Fatalf("expected HTTP plan node config, got %#v", httpNode)
	}
	foreachNode := findPlanNode(t, compilation.Plan.Nodes, "foreach")
	if foreachNode.Config["itemAlias"] != "item" || foreachNode.Config["concurrency"] != 2 {
		t.Fatalf("expected foreach plan node config, got %#v", foreachNode)
	}
}

func TestCompileRejectsHTTPNodeWithoutMethodOrEndpoint(t *testing.T) {
	compiler := New()
	draft := domain.WorkflowGraphDraft{
		SchemaVersion: "workflow.graph.v1",
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start"},
			{ID: "http", Type: "HTTP", Data: map[string]any{"method": "POST"}},
			{ID: "end", Type: "End"},
		},
		Edges: []domain.WorkflowGraphEdge{
			{ID: "e1", SourceNodeID: "start", TargetNodeID: "http"},
			{ID: "e2", SourceNodeID: "http", TargetNodeID: "end"},
		},
	}

	compilation := compiler.Compile("wf-http-invalid", "draft-v1", draft)

	if compilation.Status != domain.WorkflowCompilationInvalid {
		t.Fatalf("expected invalid compilation, got %#v", compilation)
	}
	if !hasIssue(compilation.Issues, "http-missing-config", domain.WorkflowIssueStageSpec) {
		t.Fatalf("expected http config issue, got %#v", compilation.Issues)
	}
}

func findPlanNode(t *testing.T, nodes []domain.ExecutionPlanNode, nodeID string) domain.ExecutionPlanNode {
	t.Helper()
	for _, node := range nodes {
		if node.NodeID == nodeID {
			return node
		}
	}
	t.Fatalf("expected plan node %q in %#v", nodeID, nodes)
	return domain.ExecutionPlanNode{}
}

func hasIssue(issues []domain.WorkflowCompilationIssue, code string, stage domain.WorkflowIssueStage) bool {
	for _, issue := range issues {
		if issue.Code == code && issue.SourceStage == stage {
			return true
		}
	}
	return false
}

func findIssue(t *testing.T, issues []domain.WorkflowCompilationIssue, code string, stage domain.WorkflowIssueStage) domain.WorkflowCompilationIssue {
	t.Helper()
	for _, issue := range issues {
		if issue.Code == code && issue.SourceStage == stage {
			return issue
		}
	}
	t.Fatalf("expected issue %q/%s in %#v", code, stage, issues)
	return domain.WorkflowCompilationIssue{}
}
