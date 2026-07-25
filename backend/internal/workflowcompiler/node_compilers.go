package workflowcompiler

import "actweave/backend/internal/domain"

type NodeCompiler interface {
	Compile(node domain.WorkflowGraphNode, graph normalizedGraph) (domain.ExecutableNodeSpec, []domain.WorkflowCompilationIssue)
}

type StartNodeCompiler struct{}

func (StartNodeCompiler) Compile(node domain.WorkflowGraphNode, graph normalizedGraph) (domain.ExecutableNodeSpec, []domain.WorkflowCompilationIssue) {
	config := mapFromNodeData(node)
	if inputSchema, ok := node.Data["inputSchema"]; ok {
		config["inputSchema"] = inputSchema
	}
	return executableNode(node, "Start", config), nil
}

type EndNodeCompiler struct{}

func (EndNodeCompiler) Compile(node domain.WorkflowGraphNode, graph normalizedGraph) (domain.ExecutableNodeSpec, []domain.WorkflowCompilationIssue) {
	config := mapFromNodeData(node)
	if output, ok := node.Data["output"]; ok {
		config["output"] = output
	}
	return executableNode(node, "End", config), nil
}

type ToolNodeCompiler struct{}

func (ToolNodeCompiler) Compile(node domain.WorkflowGraphNode, graph normalizedGraph) (domain.ExecutableNodeSpec, []domain.WorkflowCompilationIssue) {
	toolID, _ := node.Data["toolId"].(string)
	if toolID == "" {
		return domain.ExecutableNodeSpec{}, []domain.WorkflowCompilationIssue{
			specIssue("tool-missing-target", "Tool 节点必须绑定 toolId", node.ID, "data.toolId", "Bind the node to a tool before publishing."),
		}
	}
	config := mapFromNodeData(node)
	config["toolId"] = toolID
	return executableNode(node, "Tool", config), nil
}

type ConditionNodeCompiler struct{}

func (ConditionNodeCompiler) Compile(node domain.WorkflowGraphNode, graph normalizedGraph) (domain.ExecutableNodeSpec, []domain.WorkflowCompilationIssue) {
	expression, _ := node.Data["expression"].(string)
	if expression == "" {
		return domain.ExecutableNodeSpec{}, []domain.WorkflowCompilationIssue{
			specIssue("condition-missing-expression", "Condition 节点必须配置 expression", node.ID, "data.expression"),
		}
	}
	config := mapFromNodeData(node)
	config["expression"] = expression
	return executableNode(node, "Condition", config), nil
}

type ForEachNodeCompiler struct{}

func (ForEachNodeCompiler) Compile(node domain.WorkflowGraphNode, graph normalizedGraph) (domain.ExecutableNodeSpec, []domain.WorkflowCompilationIssue) {
	collection, ok := node.Data["collection"]
	if !ok {
		return domain.ExecutableNodeSpec{}, []domain.WorkflowCompilationIssue{
			specIssue("foreach-missing-collection", "ForEach 节点必须配置 collection", node.ID, "data.collection"),
		}
	}
	itemAlias, _ := node.Data["itemAlias"].(string)
	if itemAlias == "" {
		return domain.ExecutableNodeSpec{}, []domain.WorkflowCompilationIssue{
			specIssue("foreach-missing-item-alias", "ForEach 节点必须配置 itemAlias", node.ID, "data.itemAlias"),
		}
	}
	config := mapFromNodeData(node)
	config["collection"] = collection
	config["itemAlias"] = itemAlias
	if concurrency, ok := node.Data["concurrency"]; ok {
		config["concurrency"] = concurrency
	}
	return executableNode(node, "ForEach", config), nil
}

type HTTPNodeCompiler struct{}

func (HTTPNodeCompiler) Compile(node domain.WorkflowGraphNode, graph normalizedGraph) (domain.ExecutableNodeSpec, []domain.WorkflowCompilationIssue) {
	method, _ := node.Data["method"].(string)
	endpoint, _ := node.Data["endpoint"].(string)
	if method == "" || endpoint == "" {
		return domain.ExecutableNodeSpec{}, []domain.WorkflowCompilationIssue{
			specIssue("http-missing-config", "HTTP 节点缺少 method 或 endpoint", node.ID, "data"),
		}
	}
	config := mapFromNodeData(node)
	config["method"] = method
	config["endpoint"] = endpoint
	return executableNode(node, "HTTP", config), nil
}

type SubWorkflowNodeCompiler struct{}

func (SubWorkflowNodeCompiler) Compile(node domain.WorkflowGraphNode, graph normalizedGraph) (domain.ExecutableNodeSpec, []domain.WorkflowCompilationIssue) {
	workflowID, _ := node.Data["workflowId"].(string)
	if workflowID == "" {
		return domain.ExecutableNodeSpec{}, []domain.WorkflowCompilationIssue{
			specIssue("subworkflow-missing-target", "SubWorkflow 节点必须配置 workflowId", node.ID, "data.workflowId"),
		}
	}
	config := mapFromNodeData(node)
	config["workflowId"] = workflowID
	return executableNode(node, "SubWorkflow", config), nil
}

type TransformNodeCompiler struct{}

func (TransformNodeCompiler) Compile(node domain.WorkflowGraphNode, graph normalizedGraph) (domain.ExecutableNodeSpec, []domain.WorkflowCompilationIssue) {
	return executableNode(node, "Transform", mapFromNodeData(node)), nil
}

type ApprovalNodeCompiler struct{}

func (ApprovalNodeCompiler) Compile(node domain.WorkflowGraphNode, graph normalizedGraph) (domain.ExecutableNodeSpec, []domain.WorkflowCompilationIssue) {
	return executableNode(node, "Approval", mapFromNodeData(node)), nil
}

type ParallelNodeCompiler struct{}

func (ParallelNodeCompiler) Compile(node domain.WorkflowGraphNode, graph normalizedGraph) (domain.ExecutableNodeSpec, []domain.WorkflowCompilationIssue) {
	branches, ok := node.Data["branches"]
	if !ok {
		return domain.ExecutableNodeSpec{}, []domain.WorkflowCompilationIssue{
			specIssue("parallel-missing-branches", "Parallel 节点必须配置 branches", node.ID, "data.branches"),
		}
	}
	config := mapFromNodeData(node)
	config["branches"] = branches
	return executableNode(node, "Parallel", config), nil
}

func executableNode(node domain.WorkflowGraphNode, nodeType string, config map[string]any) domain.ExecutableNodeSpec {
	if config == nil {
		config = map[string]any{}
	}
	if node.Label != "" {
		config["label"] = node.Label
	}
	return domain.ExecutableNodeSpec{
		NodeID: node.ID,
		Type:   nodeType,
		Config: config,
	}
}

func mapFromNodeData(node domain.WorkflowGraphNode) map[string]any {
	config := map[string]any{}
	for key, value := range node.Data {
		config[key] = value
	}
	return config
}
