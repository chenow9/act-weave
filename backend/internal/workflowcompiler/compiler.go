package workflowcompiler

import (
	"time"

	"actweave/backend/internal/domain"
)

type Compiler struct {
	nodeCompilers map[string]NodeCompiler
}

const Version = "workflowcompiler.v1"

func New() Compiler {
	return Compiler{
		nodeCompilers: map[string]NodeCompiler{
			"Start":       StartNodeCompiler{},
			"End":         EndNodeCompiler{},
			"Tool":        ToolNodeCompiler{},
			"HTTP":        HTTPNodeCompiler{},
			"SubWorkflow": SubWorkflowNodeCompiler{},
			"Transform":   TransformNodeCompiler{},
			"Approval":    ApprovalNodeCompiler{},
			"Condition":   ConditionNodeCompiler{},
			"Parallel":    ParallelNodeCompiler{},
			"ForEach":     ForEachNodeCompiler{},
		},
	}
}

func (Compiler) Version() string { return Version }

func (c Compiler) Compile(workflowID string, draftVersion string, draft domain.WorkflowGraphDraft) domain.WorkflowCompilation {
	now := time.Now().UTC()
	normalized, issues := normalizeGraph(draft)
	if len(issues) > 0 {
		return domain.WorkflowCompilation{
			WorkflowID:   workflowID,
			DraftVersion: draftVersion,
			Status:       domain.WorkflowCompilationInvalid,
			Issues:       issues,
			CompiledAt:   now,
		}
	}

	spec, specIssues := buildExecutableSpec(workflowID, normalized, c.nodeCompilers)
	if len(specIssues) > 0 {
		return domain.WorkflowCompilation{
			WorkflowID:   workflowID,
			DraftVersion: draftVersion,
			Status:       domain.WorkflowCompilationInvalid,
			Issues:       specIssues,
			CompiledAt:   now,
		}
	}

	plan, planIssues := buildExecutionPlan(spec, normalized)
	if len(planIssues) > 0 {
		return domain.WorkflowCompilation{
			WorkflowID:   workflowID,
			DraftVersion: draftVersion,
			Status:       domain.WorkflowCompilationInvalid,
			Spec:         &spec,
			Issues:       planIssues,
			CompiledAt:   now,
		}
	}

	return domain.WorkflowCompilation{
		WorkflowID:   workflowID,
		DraftVersion: draftVersion,
		Status:       domain.WorkflowCompilationValid,
		Spec:         &spec,
		Plan:         &plan,
		Issues:       nil,
		CompiledAt:   now,
	}
}

func buildExecutableSpec(workflowID string, graph normalizedGraph, nodeCompilers map[string]NodeCompiler) (domain.ExecutableWorkflowSpec, []domain.WorkflowCompilationIssue) {
	spec := domain.ExecutableWorkflowSpec{
		WorkflowID: workflowID,
		Nodes:      make([]domain.ExecutableNodeSpec, 0, len(graph.order)),
	}
	issues := []domain.WorkflowCompilationIssue{}

	for _, nodeID := range graph.order {
		node := graph.nodesByID[nodeID]
		nodeCompiler, ok := nodeCompilers[node.Type]
		if !ok {
			issues = append(issues, specIssue("unsupported-node-type", "不支持的节点类型", node.ID, "type", "Use a registered workflow node type."))
			continue
		}
		executableNode, nodeIssues := nodeCompiler.Compile(node, graph)
		if len(nodeIssues) > 0 {
			issues = append(issues, nodeIssues...)
			continue
		}
		spec.Nodes = append(spec.Nodes, executableNode)
	}
	if len(issues) > 0 {
		return domain.ExecutableWorkflowSpec{}, issues
	}
	return spec, nil
}

func buildExecutionPlan(spec domain.ExecutableWorkflowSpec, graph normalizedGraph) (domain.CompiledExecutionPlan, []domain.WorkflowCompilationIssue) {
	specByNodeID := map[string]domain.ExecutableNodeSpec{}
	for _, node := range spec.Nodes {
		specByNodeID[node.NodeID] = node
	}

	plan := domain.CompiledExecutionPlan{
		WorkflowID: spec.WorkflowID,
		Nodes:      make([]domain.ExecutionPlanNode, 0, len(spec.Nodes)),
	}
	issues := []domain.WorkflowCompilationIssue{}
	for _, nodeID := range graph.order {
		specNode, ok := specByNodeID[nodeID]
		if !ok {
			issues = append(issues, newIssue("plan-missing-spec-node", "执行计划缺少节点规格", "error", domain.WorkflowIssueStagePlan, nodeID, "", "", "nodes"))
			continue
		}
		incoming := graph.incoming[nodeID]
		planNode := domain.ExecutionPlanNode{
			NodeID:       specNode.NodeID,
			Type:         specNode.Type,
			Dependencies: dependenciesFromEdges(incoming),
			Config:       cloneMap(specNode.Config),
		}
		incomingBranch, branchIssue := incomingBranchFromEdges(nodeID, incoming)
		if branchIssue != nil {
			issues = append(issues, *branchIssue)
			continue
		}
		planNode.IncomingBranch = incomingBranch
		plan.Nodes = append(plan.Nodes, planNode)
	}
	if len(issues) > 0 {
		return domain.CompiledExecutionPlan{}, issues
	}
	return plan, nil
}

func dependenciesFromEdges(edges []domain.WorkflowGraphEdge) []string {
	if len(edges) == 0 {
		return nil
	}
	dependencies := make([]string, 0, len(edges))
	seen := map[string]bool{}
	for _, edge := range edges {
		if seen[edge.SourceNodeID] {
			continue
		}
		seen[edge.SourceNodeID] = true
		dependencies = append(dependencies, edge.SourceNodeID)
	}
	return dependencies
}

func incomingBranchFromEdges(nodeID string, edges []domain.WorkflowGraphEdge) (string, *domain.WorkflowCompilationIssue) {
	branches := map[string]bool{}
	for _, edge := range edges {
		branch, _ := edge.Data["branch"].(string)
		if branch == "" {
			continue
		}
		branches[branch] = true
	}
	if len(branches) == 0 {
		return "", nil
	}
	if len(branches) > 1 {
		issue := newIssue("conflicting-incoming-branches", "节点入边包含冲突的分支标签", "error", domain.WorkflowIssueStagePlan, nodeID, "", "", "incomingBranch", "Route a merge node from a single branch label or split the merge.")
		return "", &issue
	}
	for branch := range branches {
		return branch, nil
	}
	return "", nil
}

func cloneMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
