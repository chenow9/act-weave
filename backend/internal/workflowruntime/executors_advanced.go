package workflowruntime

// FROZEN (P1): advanced node handlers for PlanRunner only. Parallel/HTTP/
// SubWorkflow/ForEach production path is einoruntime compose (PR13). Do not
// extend this interpreter for product features.

import (
	"fmt"
	"reflect"
	"strings"

	"actweave/backend/internal/domain"
)

func (r PlanRunner) executeParallelNode(node domain.ExecutionPlanNode, state *runtimeState) error {
	branches := toStringSlice(node.Config["branches"])
	branchTrace := "sequential simulation branches=" + strings.Join(branches, " -> ")
	state.steps = append(state.steps, newStep(
		state.execution.ID,
		nodeLabel(node, "Parallel"),
		node.NodeID,
		node.Type,
		domain.ExecutionStepPassed,
		summarize(branches),
		branchTrace,
	))
	state.executed[node.NodeID] = true
	state.scope.NodeOutputs[node.NodeID] = map[string]any{
		"branches":    branches,
		"branchCount": len(branches),
		"trace":       branchTrace,
		"mode":        "sequential-simulation",
	}
	return nil
}

func (r PlanRunner) executeHTTPNode(node domain.ExecutionPlanNode, state *runtimeState) error {
	method, _ := node.Config["method"].(string)
	endpoint, _ := node.Config["endpoint"].(string)
	input, err := resolveOptionalInput(node.Config, state.scope)
	if err != nil {
		return err
	}
	state.steps = append(state.steps, newStep(state.execution.ID, nodeLabel(node, "HTTP"), node.NodeID, node.Type, domain.ExecutionStepPassed, fmt.Sprintf("%s %s", method, endpoint), summarize(input)))
	state.executed[node.NodeID] = true
	state.scope.NodeOutputs[node.NodeID] = map[string]any{
		"method":   method,
		"endpoint": endpoint,
		"request":  input,
		"status":   "ok",
	}
	return nil
}

func (r PlanRunner) executeSubWorkflowNode(node domain.ExecutionPlanNode, state *runtimeState) error {
	workflowID, _ := node.Config["workflowId"].(string)
	input, err := resolveOptionalInput(node.Config, state.scope)
	if err != nil {
		return err
	}
	if r.revisionResolver == nil {
		return fmt.Errorf("subworkflow runtime resolver is not configured")
	}
	revision, err := r.revisionResolver.ResolvePublishedRevision(workflowID)
	if err != nil {
		return err
	}
	childRunner := PlanRunner{
		invoker:          r.invoker,
		revisionResolver: r.revisionResolver,
		mode:             r.mode,
	}
	childExecution, err := childRunner.Run(revision.Plan, ExecutionContext{
		UserID:                state.execution.UserID,
		Input:                 cloneMap(input),
		WorkflowVersion:       revision.RevisionID,
		WorkspaceID:           state.execution.WorkspaceID,
		Trigger:               "Workflow SubWorkflow Run",
		ActorType:             state.context.ActorType,
		PrincipalSnapshot:     cloneExecutionPrincipalSnapshot(state.context.PrincipalSnapshot),
		AuthorizationSnapshot: append([]byte(nil), state.context.AuthorizationSnapshot...),
		AgentRunID:            state.context.AgentRunID,
		WorkflowExecutionID:   state.context.WorkflowExecutionID,
	})
	if err != nil {
		return err
	}
	for _, step := range childExecution.Steps {
		step.ExecutionID = state.execution.ID
		step.ID = fmt.Sprintf("step-%s-subworkflow-%s-%s", state.execution.ID, node.NodeID, step.NodeID)
		state.steps = append(state.steps, step)
	}
	state.steps = append(state.steps, newStep(
		state.execution.ID,
		nodeLabel(node, "SubWorkflow"),
		node.NodeID,
		node.Type,
		domain.ExecutionStepPassed,
		workflowID,
		fmt.Sprintf("revision=%s status=%s", revision.RevisionID, childExecution.Status),
	))
	state.executed[node.NodeID] = true
	state.scope.NodeOutputs[node.NodeID] = map[string]any{
		"workflowId": workflowID,
		"revisionId": revision.RevisionID,
		"input":      input,
		"status":     childExecution.Status,
		"output":     cloneMap(executionOutputMap(childExecution)),
	}
	return nil
}

func (r PlanRunner) executeForEachNode(node domain.ExecutionPlanNode, state *runtimeState) error {
	collectionValue, err := resolveValue(node.Config["collection"], state.scope)
	if err != nil {
		return err
	}
	items, err := normalizeCollection(collectionValue)
	if err != nil {
		return err
	}
	itemAlias, _ := node.Config["itemAlias"].(string)
	concurrency := normalizeConcurrency(node.Config["concurrency"])
	state.steps = append(state.steps, newStep(
		state.execution.ID,
		nodeLabel(node, "ForEach"),
		node.NodeID,
		node.Type,
		domain.ExecutionStepPassed,
		summarize(collectionValue),
		fmt.Sprintf("items=%d alias=%s concurrency=%d", len(items), itemAlias, concurrency),
	))
	state.executed[node.NodeID] = true
	state.scope.NodeOutputs[node.NodeID] = map[string]any{
		"items":              items,
		"count":              len(items),
		"itemAlias":          itemAlias,
		"concurrency":        concurrency,
		"__configuredOutput": node.Config["output"],
	}
	return nil
}

func (r PlanRunner) executeLoopControlledNode(node domain.ExecutionPlanNode, controllerID string, state *runtimeState) error {
	if node.Type == "Condition" {
		return fmt.Errorf("foreach-controlled Condition nodes are not supported in trial run")
	}
	controllerOutput := state.scope.NodeOutputs[controllerID]
	items, err := normalizeCollection(controllerOutput["items"])
	if err != nil {
		return err
	}

	results := make([]any, 0, len(items))
	for index, item := range items {
		iterationScope := ExecutionScope{
			Input:        cloneMap(state.scope.Input),
			WorkflowVars: cloneMap(state.scope.WorkflowVars),
			NodeOutputs:  state.loopScopeNodeOutputs(controllerID, index),
			ForeachItem:  item,
			ForeachAlias: foreachAliasForController(controllerID, state.scope.NodeOutputs),
		}
		iterationState := runtimeState{
			execution:          state.execution,
			context:            cloneExecutionContext(state.context),
			scope:              iterationScope,
			executed:           map[string]bool{},
			skipped:            state.skipped,
			skipCauses:         state.skipCauses,
			branchContexts:     state.branchContexts,
			foreachControllers: state.foreachControllers,
			nodeTypes:          state.nodeTypes,
			selectedBranches:   state.selectedBranches,
			conditionBranches:  state.conditionBranches,
		}
		if err := r.executeNode(node, &iterationState); err != nil {
			return err
		}
		results = append(results, iterationState.scope.NodeOutputs[node.NodeID])
	}

	loopOutput := map[string]any{
		"items": results,
		"count": len(results),
	}
	state.steps = append(state.steps, newStep(
		state.execution.ID,
		nodeLabel(node, node.Type),
		node.NodeID,
		node.Type,
		domain.ExecutionStepPassed,
		fmt.Sprintf("foreach=%s items=%d", controllerID, len(items)),
		fmt.Sprintf("loop completed items=%d", len(items)),
	))
	state.executed[node.NodeID] = true
	state.scope.NodeOutputs[node.NodeID] = loopOutput
	if configuredOutput, ok := foreachConfiguredOutput(controllerID, state.scope.NodeOutputs); ok && configuredOutput != nil {
		resolvedOutput, err := resolveValue(configuredOutput, ExecutionScope{
			Input:        cloneMap(state.scope.Input),
			WorkflowVars: cloneMap(state.scope.WorkflowVars),
			NodeOutputs:  cloneNodeOutputs(state.scope.NodeOutputs),
		})
		if err != nil {
			return err
		}
		controllerOutput := cloneMap(state.scope.NodeOutputs[controllerID])
		delete(controllerOutput, "__configuredOutput")
		if outputMap, ok := resolvedOutput.(map[string]any); ok {
			for key, value := range outputMap {
				controllerOutput[key] = value
			}
		} else {
			controllerOutput["result"] = resolvedOutput
		}
		state.scope.NodeOutputs[controllerID] = controllerOutput
	}
	return nil
}

func resolveOptionalInput(config map[string]any, scope ExecutionScope) (map[string]any, error) {
	rawInput, ok := config["input"]
	if !ok {
		rawInput, ok = config["inputMapping"]
	}
	if !ok {
		return nil, nil
	}
	resolved, err := resolveValue(rawInput, scope)
	if err != nil {
		return nil, err
	}
	resolvedMap, ok := resolved.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("advanced node input must resolve to an object")
	}
	return resolvedMap, nil
}

func normalizeCollection(value any) ([]any, error) {
	switch typed := value.(type) {
	case []any:
		return typed, nil
	case nil:
		return []any{}, nil
	}
	collection := reflect.ValueOf(value)
	if collection.Kind() != reflect.Slice && collection.Kind() != reflect.Array {
		return nil, fmt.Errorf("foreach collection must resolve to an array")
	}
	items := make([]any, 0, collection.Len())
	for index := 0; index < collection.Len(); index++ {
		items = append(items, collection.Index(index).Interface())
	}
	return items, nil
}

func toStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			items = append(items, fmt.Sprint(item))
		}
		return items
	default:
		return nil
	}
}

func executionOutputMap(execution domain.Execution) map[string]any {
	if strings.TrimSpace(execution.OutputSummary) == "" {
		return nil
	}
	return map[string]any{
		"summary": execution.OutputSummary,
	}
}

func normalizeConcurrency(value any) int {
	switch typed := value.(type) {
	case int:
		if typed > 0 {
			return typed
		}
	case float64:
		if typed > 0 {
			return int(typed)
		}
	}
	return 1
}

func foreachAliasForController(controllerID string, outputs map[string]map[string]any) string {
	if controllerOutput, ok := outputs[controllerID]; ok {
		if alias, ok := controllerOutput["itemAlias"].(string); ok {
			return alias
		}
	}
	return ""
}

func foreachConfiguredOutput(controllerID string, outputs map[string]map[string]any) (any, bool) {
	controllerOutput, ok := outputs[controllerID]
	if !ok {
		return nil, false
	}
	output, ok := controllerOutput["__configuredOutput"]
	return output, ok
}

func cloneNodeOutputs(outputs map[string]map[string]any) map[string]map[string]any {
	cloned := make(map[string]map[string]any, len(outputs))
	for nodeID, output := range outputs {
		cloned[nodeID] = cloneMap(output)
	}
	return cloned
}

func buildForEachControllers(plan domain.CompiledExecutionPlan, nodeTypes map[string]string) map[string]string {
	controllers := map[string]string{}
	for _, node := range plan.Nodes {
		controllers[node.NodeID] = foreachControllerForNode(node, controllers, nodeTypes)
	}
	return controllers
}

func foreachControllerForNode(node domain.ExecutionPlanNode, controllers map[string]string, nodeTypes map[string]string) string {
	if node.Type == "End" || node.Type == "Approval" {
		return ""
	}
	controllerID := ""
	hasDirectControllerDependency := false
	hasIndirectControllerDependency := false
	for _, dep := range node.Dependencies {
		dependencyControllerID := controllers[dep]
		if nodeTypes[dep] == "ForEach" {
			dependencyControllerID = dep
			hasDirectControllerDependency = true
		} else if dependencyControllerID != "" {
			hasIndirectControllerDependency = true
		}
		if dependencyControllerID == "" {
			return ""
		}
		if controllerID == "" {
			controllerID = dependencyControllerID
			continue
		}
		if controllerID != dependencyControllerID {
			return ""
		}
	}
	if hasDirectControllerDependency && hasIndirectControllerDependency {
		return ""
	}
	return controllerID
}

func (s runtimeState) loopScopeNodeOutputs(controllerID string, index int) map[string]map[string]any {
	outputs := make(map[string]map[string]any, len(s.scope.NodeOutputs))
	for nodeID, output := range s.scope.NodeOutputs {
		nodeControllerID := s.foreachControllers[nodeID]
		if nodeID == controllerID || nodeControllerID == "" || nodeControllerID != controllerID {
			outputs[nodeID] = output
			continue
		}
		itemOutput, ok := loopItemOutput(output, index)
		if !ok {
			outputs[nodeID] = output
			continue
		}
		outputs[nodeID] = itemOutput
	}
	return outputs
}

func loopItemOutput(output map[string]any, index int) (map[string]any, bool) {
	rawItems, ok := output["items"]
	if !ok {
		return nil, false
	}
	items, err := normalizeCollection(rawItems)
	if err != nil || index >= len(items) {
		return nil, false
	}
	itemOutput, ok := items[index].(map[string]any)
	if !ok {
		return nil, false
	}
	return itemOutput, true
}
