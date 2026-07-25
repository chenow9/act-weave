package workflowruntime

// FROZEN (P1): core node handlers for PlanRunner only. New production node
// behavior belongs in einoruntime compose graph nodes, not here.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/domain"
)

type runtimeState struct {
	execution          domain.Execution
	context            ExecutionContext
	steps              []domain.ExecutionStepRecord
	scope              ExecutionScope
	executed           map[string]bool
	skipped            map[string]bool
	skipCauses         map[string]skipCause
	branchContexts     map[string]map[string]string
	foreachControllers map[string]string
	nodeTypes          map[string]string
	selectedBranches   map[string]string
	conditionBranches  map[string][]string
	reachedTerminal    bool
}

type skipCause struct {
	conditionID string
	branch      string
}

func (s runtimeState) shouldExecuteNode(node domain.ExecutionPlanNode) (bool, *skipCause) {
	for _, dep := range node.Dependencies {
		if s.executed[dep] {
			continue
		}
		if cause, ok := s.skipCauses[dep]; ok {
			if s.canIgnoreSkippedDependency(node, cause) {
				continue
			}
			causeCopy := cause
			return false, &causeCopy
		}
		return false, nil
	}
	for _, dep := range node.Dependencies {
		if s.nodeTypes[dep] == "Condition" && node.IncomingBranch != "" && s.selectedBranches[dep] != node.IncomingBranch {
			cause := skipCause{conditionID: dep, branch: node.IncomingBranch}
			return false, &cause
		}
	}
	return true, nil
}

func buildNodeTypes(plan domain.CompiledExecutionPlan) map[string]string {
	nodeTypes := make(map[string]string, len(plan.Nodes))
	for _, node := range plan.Nodes {
		nodeTypes[node.NodeID] = node.Type
	}
	return nodeTypes
}

func buildConditionBranches(plan domain.CompiledExecutionPlan) map[string][]string {
	branches := map[string][]string{}
	seen := map[string]map[string]bool{}
	nodeTypes := buildNodeTypes(plan)
	for _, node := range plan.Nodes {
		if node.IncomingBranch == "" {
			continue
		}
		for _, dep := range node.Dependencies {
			if nodeTypes[dep] != "Condition" {
				continue
			}
			if seen[dep] == nil {
				seen[dep] = map[string]bool{}
			}
			if seen[dep][node.IncomingBranch] {
				continue
			}
			seen[dep][node.IncomingBranch] = true
			branches[dep] = append(branches[dep], node.IncomingBranch)
		}
	}
	return branches
}

func (s runtimeState) canIgnoreSkippedDependency(node domain.ExecutionPlanNode, cause skipCause) bool {
	if cause.conditionID == "" || cause.branch == "" {
		return false
	}
	selectedContext := s.selectedBranchContext(node)
	selectedBranch, ok := selectedContext[cause.conditionID]
	if !ok {
		return false
	}
	return selectedBranch != cause.branch
}

func (s runtimeState) selectedBranchContext(node domain.ExecutionPlanNode) map[string]string {
	context := map[string]string{}
	for _, dep := range node.Dependencies {
		if branchContext, ok := s.branchContexts[dep]; ok {
			for conditionID, branch := range branchContext {
				context[conditionID] = branch
			}
		}
		if s.nodeTypes[dep] == "Condition" && node.IncomingBranch != "" && s.selectedBranches[dep] == node.IncomingBranch {
			context[dep] = node.IncomingBranch
		}
	}
	if len(context) == 0 {
		return nil
	}
	return context
}

func (r PlanRunner) executeNode(node domain.ExecutionPlanNode, state *runtimeState) error {
	switch node.Type {
	case "Start":
		return r.executeStartNode(node, state)
	case "Tool":
		return r.executeToolNode(node, state)
	case "Transform":
		return r.executeTransformNode(node, state)
	case "Condition":
		return r.executeConditionNode(node, state)
	case "Approval":
		return r.executeApprovalNode(node, state)
	case "Parallel":
		return r.executeParallelNode(node, state)
	case "HTTP":
		return r.executeHTTPNode(node, state)
	case "SubWorkflow":
		return r.executeSubWorkflowNode(node, state)
	case "ForEach":
		return r.executeForEachNode(node, state)
	case "End":
		return r.executeEndNode(node, state)
	default:
		return fmt.Errorf("unsupported plan node type %s", node.Type)
	}
}

func (r PlanRunner) executeStartNode(node domain.ExecutionPlanNode, state *runtimeState) error {
	if workflowVars, ok := node.Config["workflowVars"].(map[string]any); ok {
		resolved, err := resolveMapValues(workflowVars, state.scope)
		if err != nil {
			return err
		}
		state.scope.WorkflowVars = mergeMaps(state.scope.WorkflowVars, resolved)
	}
	state.steps = append(state.steps, newStep(state.execution.ID, nodeLabel(node, "Start"), node.NodeID, node.Type, domain.ExecutionStepPassed, summarize(state.scope.Input), "variables available"))
	state.executed[node.NodeID] = true
	state.scope.NodeOutputs[node.NodeID] = cloneMap(state.scope.Input)
	return nil
}

func (r PlanRunner) executeToolNode(node domain.ExecutionPlanNode, state *runtimeState) error {
	toolID, _ := node.Config["toolId"].(string)
	resolvedInput, err := resolveToolInput(node.Config, state.scope)
	if err != nil {
		return err
	}
	stepInput := "standard ToolInvocation toolId=" + toolID
	if state.execution.WorkspaceID != "" {
		stepInput += " workspaceId=" + state.execution.WorkspaceID
	}
	stepInput += " traceId=" + state.execution.TraceID
	stepInput += " input=" + summarize(resolvedInput)
	if r.invoker == nil {
		step := newStep(state.execution.ID, "Runtime Call", node.NodeID, node.Type, domain.ExecutionStepFailed, stepInput, "")
		step.ErrorMessage = "workflow tool invoker is not configured"
		state.steps = append(state.steps, step)
		return fmt.Errorf("workflow tool invoker is not configured")
	}
	result, err := r.invoker.Invoke(toolID, resolvedInput, ToolInvocationContext{
		TraceID:               state.execution.TraceID,
		WorkflowID:            state.execution.WorkflowID,
		WorkspaceID:           state.execution.WorkspaceID,
		NodeID:                node.NodeID,
		UserID:                state.execution.UserID,
		ActorType:             state.context.ActorType,
		PrincipalSnapshot:     cloneExecutionPrincipalSnapshot(state.context.PrincipalSnapshot),
		AuthorizationSnapshot: append(json.RawMessage(nil), state.context.AuthorizationSnapshot...),
		AgentRunID:            state.context.AgentRunID,
		WorkflowExecutionID:   state.context.WorkflowExecutionID,
	})
	if err != nil {
		step := newStep(state.execution.ID, "Runtime Call", node.NodeID, node.Type, domain.ExecutionStepFailed, stepInput, "")
		step.ErrorMessage = err.Error()
		state.steps = append(state.steps, step)
		return err
	}
	step := newStep(state.execution.ID, "Runtime Call", node.NodeID, node.Type, domain.ExecutionStepPassed, stepInput, summarize(result))
	state.steps = append(state.steps, step)
	state.executed[node.NodeID] = true
	state.scope.NodeOutputs[node.NodeID] = normalizeToolResult(toolID, result)
	return nil
}

func (r PlanRunner) executeTransformNode(node domain.ExecutionPlanNode, state *runtimeState) error {
	template, _ := node.Config["template"].(string)
	output, err := renderTemplate(template, state.scope)
	if err != nil {
		return err
	}
	state.steps = append(state.steps, newStep(state.execution.ID, nodeLabel(node, "Transform"), node.NodeID, node.Type, domain.ExecutionStepPassed, template, output))
	state.executed[node.NodeID] = true
	state.scope.NodeOutputs[node.NodeID] = map[string]any{"result": output}
	return nil
}

func (r PlanRunner) executeConditionNode(node domain.ExecutionPlanNode, state *runtimeState) error {
	expression, _ := node.Config["expression"].(string)
	if expression == "" {
		expression, _ = node.Config["condition"].(string)
	}
	matched, err := evaluateCondition(expression, state.scope)
	if err != nil {
		return err
	}
	selected, err := selectConditionBranch(node.NodeID, matched, state.conditionBranches)
	if err != nil {
		return err
	}
	state.selectedBranches[node.NodeID] = selected
	state.steps = append(state.steps, newStep(state.execution.ID, nodeLabel(node, "Condition"), node.NodeID, node.Type, domain.ExecutionStepPassed, expression, "condition routed to "+selected))
	state.executed[node.NodeID] = true
	state.scope.NodeOutputs[node.NodeID] = map[string]any{"branch": selected, "result": matched}
	return nil
}

func (r PlanRunner) executeApprovalNode(node domain.ExecutionPlanNode, state *runtimeState) error {
	reason, _ := node.Config["reason"].(string)
	requestedAt := time.Now().UTC()
	// 模拟试运行 (TrialMode): auto-confirm Approval so realistic generated graphs
	// (Tool + Approval) can reach SUCCEEDED without interactive HITL. Production
	// execute leaves TrialMode=false and still pauses here (D11).
	if state.context.TrialMode {
		resolvedAt := time.Now().UTC()
		resolvedBy := "trial-auto"
		state.steps = append(state.steps, newStep(
			state.execution.ID,
			nodeLabel(node, "Approval"),
			node.NodeID,
			node.Type,
			domain.ExecutionStepPassed,
			reason,
			approvalAuditSummary("confirmed", state.execution.UserID, requestedAt, resolvedBy, resolvedAt),
		))
		state.executed[node.NodeID] = true
		state.scope.NodeOutputs[node.NodeID] = map[string]any{
			"approval":    "confirmed",
			"decision":    "confirmed",
			"requestedBy": state.execution.UserID,
			"requestedAt": requestedAt.Format(time.RFC3339Nano),
			"resolvedBy":  resolvedBy,
			"resolvedAt":  resolvedAt.Format(time.RFC3339Nano),
			"trialAuto":   true,
		}
		return nil
	}
	state.steps = append(state.steps, newStep(
		state.execution.ID,
		nodeLabel(node, "Approval"),
		node.NodeID,
		node.Type,
		domain.ExecutionStepWaitingApproval,
		reason,
		approvalAuditSummary("pending", state.execution.UserID, requestedAt, "", time.Time{}),
	))
	state.execution.Status = domain.ExecutionApproval
	state.reachedTerminal = true
	state.executed[node.NodeID] = true
	state.scope.NodeOutputs[node.NodeID] = map[string]any{
		"approval":    "pending",
		"decision":    "pending",
		"requestedBy": state.execution.UserID,
		"requestedAt": requestedAt.Format(time.RFC3339Nano),
	}
	return nil
}

func (r PlanRunner) executeEndNode(node domain.ExecutionPlanNode, state *runtimeState) error {
	output, err := resolveValue(node.Config["output"], state.scope)
	if err != nil {
		return err
	}
	state.steps = append(state.steps, newStep(state.execution.ID, nodeLabel(node, "End"), node.NodeID, node.Type, domain.ExecutionStepPassed, summarize(node.Config["output"]), summarize(output)))
	state.executed[node.NodeID] = true
	state.reachedTerminal = true
	state.scope.NodeOutputs[node.NodeID] = map[string]any{"result": output}
	state.execution.OutputSummary = summarize(output)
	return nil
}

func resolveToolInput(config map[string]any, scope ExecutionScope) (map[string]any, error) {
	rawInput, ok := config["inputMapping"]
	if !ok {
		rawInput, ok = config["input"]
	}
	if !ok {
		// smart-dag.v2 Tool nodes often bind toolId only. Default to workflow
		// run input so trial/execute can invoke published tools without an
		// explicit mapping (tests and mock tools still receive real payloads).
		return cloneMap(scope.Input), nil
	}
	resolved, err := resolveValue(rawInput, scope)
	if err != nil {
		return nil, err
	}
	resolvedMap, ok := resolved.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tool input must resolve to an object")
	}
	return resolvedMap, nil
}

func approvalAuditSummary(decision string, requestedBy string, requestedAt time.Time, resolvedBy string, resolvedAt time.Time) string {
	parts := []string{"decision=" + decision}
	if strings.TrimSpace(requestedBy) != "" {
		parts = append(parts, "requestedBy="+requestedBy)
	}
	if !requestedAt.IsZero() {
		parts = append(parts, "requestedAt="+requestedAt.UTC().Format(time.RFC3339Nano))
	}
	if strings.TrimSpace(resolvedBy) != "" {
		parts = append(parts, "resolvedBy="+resolvedBy)
	}
	if !resolvedAt.IsZero() {
		parts = append(parts, "resolvedAt="+resolvedAt.UTC().Format(time.RFC3339Nano))
	}
	return strings.Join(parts, " ")
}

func normalizeToolResult(_ string, result map[string]any) map[string]any {
	normalized := cloneMap(result)
	if nested, ok := result["data"].(map[string]any); ok {
		for key, value := range nested {
			if _, exists := normalized[key]; !exists {
				normalized[key] = value
			}
		}
	}
	return normalized
}

func selectConditionBranch(nodeID string, conditionResult bool, conditionBranches map[string][]string) (string, error) {
	if !conditionResult {
		return "default", nil
	}
	nonDefault := map[string]bool{}
	for _, label := range conditionBranches[nodeID] {
		if label != "" && label != "default" {
			nonDefault[label] = true
		}
	}
	switch len(nonDefault) {
	case 0:
		return "default", nil
	case 1:
		for label := range nonDefault {
			return label, nil
		}
	}
	return "", fmt.Errorf("condition node %s has ambiguous non-default branches", nodeID)
}

func (s *runtimeState) finish(workflowID string, started time.Time) {
	switch s.execution.Status {
	case domain.ExecutionSuccess:
		s.steps = append(s.steps, newStep(s.execution.ID, "Result Return", "", "", domain.ExecutionStepPassed, workflowID, defaultString(s.execution.OutputSummary, "workflow trial completed")))
		if s.execution.OutputSummary == "" {
			s.execution.OutputSummary = "Workflow trial run completed"
		}
	case domain.ExecutionApproval:
		s.steps = append(s.steps, newStep(s.execution.ID, "Result Return", "", "", domain.ExecutionStepWaitingApproval, workflowID, "waiting for approval.result"))
		s.execution.OutputSummary = "Workflow trial run is blocked by Approval node"
	default:
		s.steps = append(s.steps, newStep(s.execution.ID, "Result Return", "", "", domain.ExecutionStepFailed, workflowID, s.execution.ErrorMessage))
		s.execution.OutputSummary = "Workflow trial run failed"
	}

	finished := time.Now().UTC()
	s.execution.Steps = s.steps
	s.execution.FinishedAt = finished
	s.execution.DurationMS = int(finished.Sub(started).Milliseconds())
}

func nodeLabel(node domain.ExecutionPlanNode, fallback string) string {
	if label, ok := node.Config["label"].(string); ok && strings.TrimSpace(label) != "" {
		return label
	}
	return fallback
}

func newStep(
	executionID string,
	name string,
	nodeID string,
	nodeType string,
	status domain.ExecutionStepStatus,
	inputSummary string,
	outputSummary string,
) domain.ExecutionStepRecord {
	started := time.Now().UTC()
	stepSlug := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	if nodeID != "" {
		stepSlug = nodeID + "-" + stepSlug
	}
	return domain.ExecutionStepRecord{
		ID:                      "step-" + executionID + "-" + stepSlug,
		ExecutionID:             executionID,
		Name:                    name,
		NodeID:                  nodeID,
		NodeType:                nodeType,
		Status:                  status,
		InputSummary:            inputSummary,
		OutputSummary:           outputSummary,
		DurationMS:              1,
		RawPayloadObjectAddress: stepPayloadAddress(executionID, nodeID, name, status),
		StartedAt:               started,
		FinishedAt:              started.Add(time.Millisecond),
	}
}

func stepPayloadAddress(executionID string, nodeID string, name string, status domain.ExecutionStepStatus) string {
	parts := []string{"s3://actweave-executions", executionID, "steps"}
	label := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	if nodeID != "" {
		label = nodeID + "-" + label
	}
	label = strings.Trim(label, "-")
	if label == "" {
		label = "system-step"
	}
	statusSlug := strings.ToLower(strings.ReplaceAll(string(status), " ", "-"))
	return strings.Join(parts, "/") + "/" + label + "-" + statusSlug + ".json"
}

func summarize(value any) string {
	if value == nil {
		return "{}"
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	summary := strings.TrimSpace(buffer.String())
	if len(summary) > 420 {
		return summary[:420] + "..."
	}
	return summary
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

func mergeMaps(current map[string]any, updates map[string]any) map[string]any {
	merged := cloneMap(current)
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range updates {
		merged[key] = value
	}
	return merged
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
