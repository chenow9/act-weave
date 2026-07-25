package workflowcompiler

import (
	"sort"

	"actweave/backend/internal/domain"
)

type normalizedGraph struct {
	nodesByID map[string]domain.WorkflowGraphNode
	incoming  map[string][]domain.WorkflowGraphEdge
	outgoing  map[string][]domain.WorkflowGraphEdge
	order     []string
}

func normalizeGraph(draft domain.WorkflowGraphDraft) (normalizedGraph, []domain.WorkflowCompilationIssue) {
	graph := normalizedGraph{
		nodesByID: map[string]domain.WorkflowGraphNode{},
		incoming:  map[string][]domain.WorkflowGraphEdge{},
		outgoing:  map[string][]domain.WorkflowGraphEdge{},
	}
	issues := []domain.WorkflowCompilationIssue{}
	startCount := 0
	endCount := 0

	for _, node := range draft.Nodes {
		if node.ID == "" {
			issues = append(issues, graphIssue("missing-node-id", "节点 ID 不能为空", "", "", "", "nodes.id"))
			continue
		}
		if _, exists := graph.nodesByID[node.ID]; exists {
			issues = append(issues, graphIssue("duplicate-node-id", "节点 ID 不能重复", node.ID, "", "", "id"))
			continue
		}
		graph.nodesByID[node.ID] = node
		graph.incoming[node.ID] = nil
		graph.outgoing[node.ID] = nil
		switch node.Type {
		case "Start":
			startCount++
		case "End":
			endCount++
		}
	}

	if startCount != 1 {
		issues = append(issues, graphIssue("start-count-invalid", "工作流必须包含且仅包含一个 Start 节点", "", "", "", "nodes"))
	}
	if endCount < 1 {
		issues = append(issues, graphIssue("missing-end-node", "工作流至少需要一个 End 节点", "", "", "", "nodes"))
	}

	inDegree := map[string]int{}
	for nodeID := range graph.nodesByID {
		inDegree[nodeID] = 0
	}
	for _, edge := range draft.Edges {
		if edge.SourceNodeID == edge.TargetNodeID {
			issues = append(issues, graphIssue("self-loop", "边不能指向自身节点", edge.SourceNodeID, edge.ID, edge.SourcePort, "targetNodeId"))
			continue
		}
		if _, ok := graph.nodesByID[edge.SourceNodeID]; !ok {
			issues = append(issues, graphIssue("missing-source-node", "边的 sourceNodeId 不存在", "", edge.ID, edge.SourcePort, "sourceNodeId"))
			continue
		}
		if _, ok := graph.nodesByID[edge.TargetNodeID]; !ok {
			issues = append(issues, graphIssue("missing-target-node", "边的 targetNodeId 不存在", "", edge.ID, edge.TargetPort, "targetNodeId"))
			continue
		}
		graph.outgoing[edge.SourceNodeID] = append(graph.outgoing[edge.SourceNodeID], edge)
		graph.incoming[edge.TargetNodeID] = append(graph.incoming[edge.TargetNodeID], edge)
		inDegree[edge.TargetNodeID]++
	}
	if len(issues) > 0 {
		return normalizedGraph{}, issues
	}

	queue := make([]string, 0, len(inDegree))
	for nodeID, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, nodeID)
		}
	}
	sort.Strings(queue)
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		graph.order = append(graph.order, nodeID)

		outgoing := append([]domain.WorkflowGraphEdge(nil), graph.outgoing[nodeID]...)
		sort.SliceStable(outgoing, func(i, j int) bool {
			if outgoing[i].TargetNodeID == outgoing[j].TargetNodeID {
				return outgoing[i].ID < outgoing[j].ID
			}
			return outgoing[i].TargetNodeID < outgoing[j].TargetNodeID
		})
		for _, edge := range outgoing {
			inDegree[edge.TargetNodeID]--
			if inDegree[edge.TargetNodeID] == 0 {
				queue = appendSorted(queue, edge.TargetNodeID)
			}
		}
	}

	if len(graph.order) != len(graph.nodesByID) {
		return normalizedGraph{}, []domain.WorkflowCompilationIssue{
			graphIssue("graph-cycle-detected", "工作流图存在环，无法编译为 DAG", "", "", "", "edges", "Remove at least one edge from the cycle."),
		}
	}

	branchIssues := validateConditionBranches(graph)
	if len(branchIssues) > 0 {
		return normalizedGraph{}, branchIssues
	}

	reachabilityIssues := validateReachableFromStart(graph)
	if len(reachabilityIssues) > 0 {
		return normalizedGraph{}, reachabilityIssues
	}
	return graph, nil
}

func appendSorted(values []string, next string) []string {
	values = append(values, next)
	sort.Strings(values)
	return values
}

func validateReachableFromStart(graph normalizedGraph) []domain.WorkflowCompilationIssue {
	startID := ""
	for nodeID, node := range graph.nodesByID {
		if node.Type == "Start" {
			startID = nodeID
			break
		}
	}
	if startID == "" {
		return nil
	}

	visited := map[string]bool{startID: true}
	queue := []string{startID}
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		for _, edge := range graph.outgoing[nodeID] {
			if visited[edge.TargetNodeID] {
				continue
			}
			visited[edge.TargetNodeID] = true
			queue = append(queue, edge.TargetNodeID)
		}
	}

	issues := []domain.WorkflowCompilationIssue{}
	for _, edge := range graph.incoming[startID] {
		issues = append(issues, graphIssue("start-has-incoming-edge", "Start 节点不能有入边", startID, edge.ID, edge.TargetPort, "edges"))
	}
	for _, nodeID := range graph.order {
		if visited[nodeID] {
			continue
		}
		issues = append(issues, graphIssue("node-not-reachable-from-start", "节点必须可从 Start 节点到达", nodeID, "", "", "nodes"))
	}
	return issues
}

func validateConditionBranches(graph normalizedGraph) []domain.WorkflowCompilationIssue {
	issues := []domain.WorkflowCompilationIssue{}
	for _, nodeID := range graph.order {
		node := graph.nodesByID[nodeID]
		if node.Type != "Condition" {
			continue
		}

		outgoing := append([]domain.WorkflowGraphEdge(nil), graph.outgoing[nodeID]...)
		sort.SliceStable(outgoing, func(i, j int) bool {
			return outgoing[i].ID < outgoing[j].ID
		})
		if len(outgoing) <= 1 {
			continue
		}

		hasDefault := false
		nonDefaultCount := 0
		for _, edge := range outgoing {
			branch, _ := edge.Data["branch"].(string)
			if branch == "default" {
				hasDefault = true
				continue
			}
			if branch == "" {
				continue
			}
			nonDefaultCount++
			if nonDefaultCount > 1 {
				issues = append(issues, graphIssue(
					"condition-branch-non-default-limit",
					"Condition 节点最多只能有一条非默认分支连线",
					nodeID,
					edge.ID,
					edge.SourcePort,
					"edges.data.branch",
					"Keep one non-default branch and route remaining outcomes through the default branch.",
				))
			}
		}

		if !hasDefault {
			issueEdge := outgoing[0]
			for _, edge := range outgoing {
				branch, _ := edge.Data["branch"].(string)
				if branch == "" {
					issueEdge = edge
					break
				}
			}
			issues = append(issues, graphIssue(
				"condition-branch-default-required",
				"Condition 节点存在多条出边时必须配置一条默认分支",
				nodeID,
				issueEdge.ID,
				issueEdge.SourcePort,
				"edges.data.branch",
				"Set one outgoing edge branch to default.",
			))
		}
	}
	return issues
}
