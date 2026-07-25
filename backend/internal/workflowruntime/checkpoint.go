package workflowruntime

import (
	"strings"
	"time"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/principal"
)

const (
	WorkflowApprovalPending   = "Pending"
	WorkflowApprovalConfirmed = "Confirmed"
	WorkflowApprovalCancelled = "Cancelled"
)

type WorkflowApprovalCheckpoint struct {
	ID          string                       `json:"id"`
	WorkflowID  string                       `json:"workflowId"`
	ExecutionID string                       `json:"executionId"`
	NodeID      string                       `json:"nodeId"`
	NodeReason  string                       `json:"nodeReason"`
	Status      string                       `json:"status"`
	Plan        domain.CompiledExecutionPlan `json:"plan"`
	Scope       ExecutionScope               `json:"scope"`
	Context     ExecutionContext             `json:"context"`
	NextNodeIDs []string                     `json:"nextNodeIds"`
	RequestedBy string                       `json:"requestedBy"`
	ResolvedBy  string                       `json:"resolvedBy,omitempty"`
	CreatedAt   time.Time                    `json:"createdAt"`
	ResolvedAt  *time.Time                   `json:"resolvedAt,omitempty"`
	// EinoCheckPointID is the compose checkpoint key used to resume an
	// eino_core/eino Approval interrupt. Empty on wrapper PlanRunner path
	// (ConfirmApproval re-runs the plan from the checkpoint snapshot).
	EinoCheckPointID string `json:"einoCheckPointId,omitempty"`
	// EinoInterruptIDs are root-cause-first compose interrupt context IDs for
	// ResumeWithData / BatchResumeWithData. Empty on wrapper PlanRunner path.
	EinoInterruptIDs []string `json:"einoInterruptIds,omitempty"`
}

func approvalCheckpointFromState(plan domain.CompiledExecutionPlan, state runtimeState, requestedBy string) *WorkflowApprovalCheckpoint {
	if state.execution.Status != domain.ExecutionApproval {
		return nil
	}
	nodeID := approvalNodeIDFromSteps(state.steps)
	if nodeID == "" {
		return nil
	}
	now := time.Now().UTC()
	return &WorkflowApprovalCheckpoint{
		ID:          "approval-" + state.execution.ID + "-" + nodeID,
		WorkflowID:  plan.WorkflowID,
		ExecutionID: state.execution.ID,
		NodeID:      nodeID,
		NodeReason:  approvalNodeReason(plan, nodeID),
		Status:      WorkflowApprovalPending,
		Plan:        plan,
		Scope:       cloneExecutionScope(state.scope),
		Context:     cloneExecutionContext(state.context),
		NextNodeIDs: nextNodeIDs(plan, nodeID),
		RequestedBy: requestedBy,
		CreatedAt:   now,
	}
}

func approvalNodeIDFromSteps(steps []domain.ExecutionStepRecord) string {
	for index := len(steps) - 1; index >= 0; index-- {
		step := steps[index]
		if step.NodeID != "" && step.NodeType == "Approval" && step.Status == domain.ExecutionStepWaitingApproval {
			return step.NodeID
		}
	}
	return ""
}

func approvalNodeReason(plan domain.CompiledExecutionPlan, nodeID string) string {
	for _, node := range plan.Nodes {
		if node.NodeID != nodeID {
			continue
		}
		reason, _ := node.Config["reason"].(string)
		return strings.TrimSpace(reason)
	}
	return ""
}

func nextNodeIDs(plan domain.CompiledExecutionPlan, nodeID string) []string {
	next := []string{}
	seen := map[string]bool{}
	for _, node := range plan.Nodes {
		for _, dependency := range node.Dependencies {
			if dependency != nodeID || seen[node.NodeID] {
				continue
			}
			next = append(next, node.NodeID)
			seen[node.NodeID] = true
		}
	}
	return next
}

func cloneExecutionScope(scope ExecutionScope) ExecutionScope {
	cloned := ExecutionScope{
		Input:        cloneMap(scope.Input),
		WorkflowVars: cloneMap(scope.WorkflowVars),
		ForeachItem:  scope.ForeachItem,
		ForeachAlias: scope.ForeachAlias,
	}
	if len(scope.NodeOutputs) > 0 {
		cloned.NodeOutputs = make(map[string]map[string]any, len(scope.NodeOutputs))
		for nodeID, output := range scope.NodeOutputs {
			cloned.NodeOutputs[nodeID] = cloneMap(output)
		}
	}
	return cloned
}

func runtimeStateFromCheckpoint(
	plan domain.CompiledExecutionPlan,
	execution domain.Execution,
	scope ExecutionScope,
	executionContext ExecutionContext,
) runtimeState {
	state := runtimeState{
		execution:          execution,
		context:            cloneExecutionContext(executionContext),
		steps:              append([]domain.ExecutionStepRecord{}, execution.Steps...),
		scope:              scope,
		executed:           executedNodeIDsFromScope(scope),
		skipped:            map[string]bool{},
		skipCauses:         map[string]skipCause{},
		branchContexts:     map[string]map[string]string{},
		foreachControllers: buildForEachControllers(plan, buildNodeTypes(plan)),
		nodeTypes:          buildNodeTypes(plan),
		selectedBranches:   selectedBranchesFromScope(plan, scope),
		conditionBranches:  buildConditionBranches(plan),
	}
	for _, node := range plan.Nodes {
		if !state.executed[node.NodeID] {
			continue
		}
		state.branchContexts[node.NodeID] = state.selectedBranchContext(node)
	}
	return state
}

func cloneExecutionContext(value ExecutionContext) ExecutionContext {
	value.Input = cloneMap(value.Input)
	value.Scope = cloneExecutionScope(value.Scope)
	value.AuthorizationSnapshot = append([]byte(nil), value.AuthorizationSnapshot...)
	value.PrincipalSnapshot = cloneExecutionPrincipalSnapshot(value.PrincipalSnapshot)
	return value
}

func cloneExecutionPrincipalSnapshot(value *principal.ExecutionSnapshot) *principal.ExecutionSnapshot {
	if value == nil {
		return nil
	}
	copyValue := *value
	if value.Identity.Subject != nil {
		subject := *value.Identity.Subject
		copyValue.Identity.Subject = &subject
	}
	return &copyValue
}

func executedNodeIDsFromScope(scope ExecutionScope) map[string]bool {
	executed := map[string]bool{}
	for nodeID := range scope.NodeOutputs {
		executed[nodeID] = true
	}
	return executed
}

func selectedBranchesFromScope(plan domain.CompiledExecutionPlan, scope ExecutionScope) map[string]string {
	selected := map[string]string{}
	nodeTypes := buildNodeTypes(plan)
	for nodeID, output := range scope.NodeOutputs {
		if nodeTypes[nodeID] != "Condition" {
			continue
		}
		branch, _ := output["branch"].(string)
		if branch != "" {
			selected[nodeID] = branch
		}
	}
	return selected
}

func defaultTime(value time.Time, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}
