package workflowruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/principal"
)

func TestPlanRunnerRoutesOnlySelectedConditionBranch(t *testing.T) {
	invoker := &fakeToolInvoker{
		results: map[string]map[string]any{
			"order.status.query": {"status": "paid"},
		},
	}
	runner := NewPlanRunner(invoker)

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-order",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "tool", Type: "Tool", Dependencies: []string{"start"}, Config: map[string]any{"toolId": "order.status.query"}},
			{NodeID: "branch", Type: "Condition", Dependencies: []string{"tool"}, Config: map[string]any{"expression": "nodeOutputs.tool.status == 'paid'"}},
			{NodeID: "approval", Type: "Approval", Dependencies: []string{"branch"}, IncomingBranch: "paid"},
			{NodeID: "end", Type: "End", Dependencies: []string{"branch"}, IncomingBranch: "default"},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"orderId": "A10293"},
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if execution.Status != domain.ExecutionApproval {
		t.Fatalf("expected approval status, got %s", execution.Status)
	}
	if !hasStepStatus(execution.Steps, "approval", domain.ExecutionStepWaitingApproval) {
		t.Fatalf("expected approval branch to expose WaitingApproval status, got %#v", execution.Steps)
	}
	if !hasStepStatus(execution.Steps, "end", domain.ExecutionStepSkipped) {
		t.Fatalf("expected end branch to be recorded as skipped, got %#v", execution.Steps)
	}
	skippedEnd := lastNodeStep(execution.Steps, "end")
	if !strings.Contains(skippedEnd.OutputSummary, "condition") || !strings.Contains(skippedEnd.OutputSummary, "default") {
		t.Fatalf("expected skipped end step to explain branch routing, got %#v", skippedEnd)
	}
	if !containsNodeStep(execution.Steps, "approval") {
		t.Fatalf("expected approval branch to execute, got %#v", execution.Steps)
	}
}

func TestPlanRunnerCreatesApprovalCheckpointAndConfirmsOriginalExecution(t *testing.T) {
	runner := NewPlanRunner(&fakeToolInvoker{})
	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-approval-resume",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "approval", Type: "Approval", Dependencies: []string{"start"}, Config: map[string]any{"reason": "paid order requires review"}},
			{NodeID: "end", Type: "End", Dependencies: []string{"approval"}, Config: map[string]any{"output": map[string]any{"kind": "ref", "path": "input.orderId"}}},
		},
	}

	execution, checkpoint, err := runner.RunWithCheckpoint(plan, ExecutionContext{
		UserID: "requester-1",
		Input:  map[string]any{"orderId": "A10293"},
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if execution.Status != domain.ExecutionApproval {
		t.Fatalf("expected approval execution, got %s", execution.Status)
	}
	if checkpoint == nil {
		t.Fatal("expected pending approval checkpoint")
	}
	if checkpoint.ExecutionID != execution.ID || checkpoint.WorkflowID != "wf-approval-resume" || checkpoint.NodeID != "approval" {
		t.Fatalf("unexpected checkpoint identity: %#v", checkpoint)
	}
	if checkpoint.Status != "Pending" || checkpoint.RequestedBy != "requester-1" || checkpoint.NodeReason != "paid order requires review" {
		t.Fatalf("unexpected checkpoint metadata: %#v", checkpoint)
	}
	if got := checkpoint.Scope.Input["orderId"]; got != "A10293" {
		t.Fatalf("expected checkpoint scope to keep input, got %#v", checkpoint.Scope.Input)
	}
	if len(checkpoint.NextNodeIDs) != 1 || checkpoint.NextNodeIDs[0] != "end" {
		t.Fatalf("expected next node ids from approval, got %#v", checkpoint.NextNodeIDs)
	}

	resumed, err := runner.ConfirmApproval(*checkpoint, execution, "resolver-1")
	if err != nil {
		t.Fatalf("confirm approval: %v", err)
	}
	if resumed.ID != execution.ID {
		t.Fatalf("expected original execution id to be updated, got %s want %s", resumed.ID, execution.ID)
	}
	if resumed.Status != domain.ExecutionSuccess {
		t.Fatalf("expected resumed execution success, got %s: %#v", resumed.Status, resumed)
	}
	if !containsNodeStep(resumed.Steps, "end") {
		t.Fatalf("expected resumed execution to continue to End, got %#v", resumed.Steps)
	}
	if !strings.Contains(resumed.OutputSummary, "A10293") {
		t.Fatalf("expected resumed output summary from End node, got %q", resumed.OutputSummary)
	}
	if !hasStepStatus(resumed.Steps, "approval", domain.ExecutionStepPassed) {
		t.Fatalf("expected approval confirmation step, got %#v", resumed.Steps)
	}
	if !hasStepStatus(resumed.Steps, "end", domain.ExecutionStepPassed) {
		t.Fatalf("expected resumed end step to use stable passed status, got %#v", resumed.Steps)
	}
}

func TestPlanRunnerCancelsApprovalCheckpointOnOriginalExecution(t *testing.T) {
	runner := NewPlanRunner(&fakeToolInvoker{})
	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-approval-cancel",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "approval", Type: "Approval", Dependencies: []string{"start"}, Config: map[string]any{"reason": "paid order requires review"}},
			{NodeID: "end", Type: "End", Dependencies: []string{"approval"}, Config: map[string]any{"output": map[string]any{"kind": "literal", "value": "done"}}},
		},
	}
	execution, checkpoint, err := runner.RunWithCheckpoint(plan, ExecutionContext{UserID: "requester-1"})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if checkpoint == nil {
		t.Fatal("expected pending approval checkpoint")
	}

	cancelled, err := runner.CancelApproval(*checkpoint, execution, "resolver-1")
	if err != nil {
		t.Fatalf("cancel approval: %v", err)
	}
	if cancelled.ID != execution.ID {
		t.Fatalf("expected original execution id to be updated, got %s want %s", cancelled.ID, execution.ID)
	}
	if cancelled.Status != domain.ExecutionFailed {
		t.Fatalf("expected cancelled approval to fail execution, got %s", cancelled.Status)
	}
	if !strings.Contains(cancelled.ErrorMessage, "cancel") {
		t.Fatalf("expected cancellation error message, got %q", cancelled.ErrorMessage)
	}
	if containsNodeStep(cancelled.Steps, "end") {
		t.Fatalf("expected cancellation not to continue to End, got %#v", cancelled.Steps)
	}
	if !hasStepStatus(cancelled.Steps, "approval", domain.ExecutionStepCancelled) {
		t.Fatalf("expected approval cancellation step, got %#v", cancelled.Steps)
	}
}

func TestPlanRunnerResolvesStructuredValuesForToolTransformAndEnd(t *testing.T) {
	invoker := &fakeToolInvoker{
		results: map[string]map[string]any{
			"order.status.query": {"status": "paid", "reviewer": "ops"},
		},
	}
	runner := NewPlanRunner(invoker)

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-structured-values",
		Nodes: []domain.ExecutionPlanNode{
			{
				NodeID: "start",
				Type:   "Start",
				Config: map[string]any{
					"workflowVars": map[string]any{
						"reviewQueue": "risk-ops",
					},
				},
			},
			{
				NodeID:       "tool",
				Type:         "Tool",
				Dependencies: []string{"start"},
				Config: map[string]any{
					"toolId": "order.status.query",
					"input": map[string]any{
						"orderId": map[string]any{"kind": "ref", "path": "input.orderId"},
						"queue":   map[string]any{"kind": "ref", "path": "workflowVars.reviewQueue"},
						"item":    map[string]any{"kind": "ref", "path": "foreach.item"},
						"reason":  map[string]any{"kind": "literal", "value": "customer_requested"},
					},
				},
			},
			{
				NodeID:       "transform",
				Type:         "Transform",
				Dependencies: []string{"tool"},
				Config: map[string]any{
					"template": "订单 {{input.orderId}} -> {{nodeOutputs.tool.status}} @ {{workflowVars.reviewQueue}} / {{foreach.item}}",
				},
			},
			{
				NodeID:       "end",
				Type:         "End",
				Dependencies: []string{"transform"},
				Config: map[string]any{
					"output": map[string]any{"kind": "ref", "path": "nodeOutputs.transform.result"},
				},
			},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"orderId": "A10293"},
		Scope: ExecutionScope{
			ForeachItem: "line-1",
		},
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}

	if got := invoker.inputs["order.status.query"]["orderId"]; got != "A10293" {
		t.Fatalf("expected tool input orderId to be resolved, got %#v", invoker.inputs)
	}
	if got := invoker.inputs["order.status.query"]["queue"]; got != "risk-ops" {
		t.Fatalf("expected tool input queue to be resolved, got %#v", invoker.inputs)
	}
	if got := invoker.inputs["order.status.query"]["item"]; got != "line-1" {
		t.Fatalf("expected tool input foreach item to be resolved, got %#v", invoker.inputs)
	}
	if got := invoker.inputs["order.status.query"]["reason"]; got != "customer_requested" {
		t.Fatalf("expected tool literal input to be resolved, got %#v", invoker.inputs)
	}
	if !strings.Contains(execution.OutputSummary, "订单 A10293 -> paid @ risk-ops / line-1") {
		t.Fatalf("expected resolved transform output summary, got %s", execution.OutputSummary)
	}
}

func TestPlanRunnerDefaultsToWorkflowInputWhenToolMappingMissing(t *testing.T) {
	// smart-dag.v2 Tool nodes often only set toolId. Runtime defaults to the
	// workflow run input so trial/execute can invoke published tools.
	invoker := &fakeToolInvoker{
		results: map[string]map[string]any{
			"order.cancel": {"ok": true},
		},
	}
	runner := NewPlanRunner(invoker)

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-no-tool-mapping",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "tool", Type: "Tool", Dependencies: []string{"start"}, Config: map[string]any{"toolId": "order.cancel"}},
			{NodeID: "end", Type: "End", Dependencies: []string{"tool"}, Config: map[string]any{"output": map[string]any{"kind": "literal", "value": "ok"}}},
		},
	}

	_, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input: map[string]any{
			"orderId": "A10293",
			"reason":  "customer_requested",
		},
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	got := invoker.inputs["order.cancel"]
	if got["orderId"] != "A10293" || got["reason"] != "customer_requested" {
		t.Fatalf("expected missing tool mapping to pass workflow input, got %#v", got)
	}
}

func TestPlanRunnerRecordsResolvedToolInputWhenInvokeFails(t *testing.T) {
	invoker := &fakeToolInvoker{
		errors: map[string]error{
			"order.cancel": errors.New("tool timeout"),
		},
	}
	runner := NewPlanRunner(invoker)

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-tool-failure-trace",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "tool", Type: "Tool", Dependencies: []string{"start"}, Config: map[string]any{
				"toolId": "order.cancel",
				"inputMapping": map[string]any{
					"orderId": map[string]any{"kind": "ref", "path": "input.orderId"},
					"reason":  map[string]any{"kind": "literal", "value": "customer_requested"},
				},
			}},
			{NodeID: "end", Type: "End", Dependencies: []string{"tool"}, Config: map[string]any{"output": map[string]any{"kind": "literal", "value": "ok"}}},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"orderId": "A10293"},
	})
	if err == nil {
		t.Fatal("expected tool invoke error")
	}
	if execution.Status != domain.ExecutionFailed {
		t.Fatalf("expected failed execution, got %s", execution.Status)
	}

	var failedSteps []domain.ExecutionStepRecord
	for _, step := range execution.Steps {
		if step.NodeID == "tool" && step.Status == domain.ExecutionStepFailed {
			failedSteps = append(failedSteps, step)
		}
	}
	if len(failedSteps) != 1 {
		t.Fatalf("expected one failed tool step, got %#v", failedSteps)
	}
	inputSummary := failedSteps[0].InputSummary
	if !strings.Contains(inputSummary, "A10293") || !strings.Contains(inputSummary, "customer_requested") {
		t.Fatalf("expected failed tool step to include resolved input, got %q", inputSummary)
	}
	if strings.Contains(inputSummary, "inputMapping") || strings.Contains(inputSummary, "kind") {
		t.Fatalf("expected failed tool step to hide mapping config, got %q", inputSummary)
	}
	if failedSteps[0].ErrorMessage != "tool timeout" {
		t.Fatalf("expected failed tool step error message, got %#v", failedSteps[0])
	}
}

func TestPlanRunnerPrefersInputMappingOverLegacyInput(t *testing.T) {
	invoker := &fakeToolInvoker{
		results: map[string]map[string]any{"order.cancel": {"ok": true}},
	}
	runner := NewPlanRunner(invoker)

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-tool-input-precedence",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "tool", Type: "Tool", Dependencies: []string{"start"}, Config: map[string]any{
				"toolId": "order.cancel",
				"input": map[string]any{
					"orderId": map[string]any{"kind": "literal", "value": "STALE"},
					"legacy":  map[string]any{"kind": "literal", "value": "must not execute"},
				},
				"inputMapping": map[string]any{
					"orderId": map[string]any{"kind": "ref", "path": "input.orderId"},
					"reason":  map[string]any{"kind": "literal", "value": "customer_requested"},
				},
			}},
			{NodeID: "end", Type: "End", Dependencies: []string{"tool"}, Config: map[string]any{"output": map[string]any{"kind": "literal", "value": "ok"}}},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"orderId": "A10293"},
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected successful execution, got %s", execution.Status)
	}
	input := invoker.inputs["order.cancel"]
	if input["orderId"] != "A10293" || input["reason"] != "customer_requested" {
		t.Fatalf("expected inputMapping to drive tool input, got %#v", input)
	}
	if _, ok := input["legacy"]; ok {
		t.Fatalf("expected legacy input to be ignored when inputMapping is present, got %#v", input)
	}
}

func TestPlanRunnerFailsConditionNodeWhenExpressionCannotBeResolved(t *testing.T) {
	runner := NewPlanRunner(&fakeToolInvoker{
		results: map[string]map[string]any{
			"order.status.query": {"status": "paid"},
		},
	})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-bad-condition",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "tool", Type: "Tool", Dependencies: []string{"start"}, Config: map[string]any{"toolId": "order.status.query"}},
			{NodeID: "branch", Type: "Condition", Dependencies: []string{"tool"}, Config: map[string]any{"expression": "nodeOutputs.tool.missing == 'paid'"}},
			{NodeID: "approval", Type: "Approval", Dependencies: []string{"branch"}, IncomingBranch: "paid"},
			{NodeID: "end", Type: "End", Dependencies: []string{"branch"}, IncomingBranch: "default"},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"orderId": "A10293"},
	})
	if err == nil {
		t.Fatal("expected condition resolution error")
	}
	if execution.Status != domain.ExecutionFailed {
		t.Fatalf("expected failed execution status, got %s", execution.Status)
	}
	if containsNodeStep(execution.Steps, "approval") || containsNodeStep(execution.Steps, "end") {
		t.Fatalf("expected branch dependents to be skipped after failure, got %#v", execution.Steps)
	}
}

func TestPlanRunnerExecutesDefaultMergeWhenAlternateBranchSkipped(t *testing.T) {
	runner := NewPlanRunner(&fakeToolInvoker{})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-default-merge",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "branch", Type: "Condition", Dependencies: []string{"start"}, Config: map[string]any{"expression": "input.route == 'paid'"}},
			{
				NodeID:         "transform",
				Type:           "Transform",
				Dependencies:   []string{"branch"},
				IncomingBranch: "paid",
				Config:         map[string]any{"template": "paid path"},
			},
			{
				NodeID:         "end",
				Type:           "End",
				Dependencies:   []string{"branch", "transform"},
				IncomingBranch: "default",
				Config:         map[string]any{"output": map[string]any{"kind": "ref", "path": "input.orderId"}},
			},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"orderId": "A10293", "route": "default"},
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected default branch to reach end node, got %s", execution.Status)
	}
	if !hasStepStatus(execution.Steps, "transform", domain.ExecutionStepSkipped) {
		t.Fatalf("expected paid transform to be recorded as skipped on default route, got %#v", execution.Steps)
	}
	if !strings.Contains(stepOutputSummary(execution.Steps, "transform"), "branch") {
		t.Fatalf("expected skipped transform step to explain branch cause, got %#v", execution.Steps)
	}
	if !containsNodeStep(execution.Steps, "end") {
		t.Fatalf("expected merged end node to execute on default route, got %#v", execution.Steps)
	}
}

func TestPlanRunnerFailsWhenCompiledPlanDoesNotReachTerminalNode(t *testing.T) {
	runner := NewPlanRunner(&fakeToolInvoker{})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-missing-terminal",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "branch", Type: "Condition", Dependencies: []string{"start"}, Config: map[string]any{"expression": "input.route == 'paid'"}},
			{
				NodeID:         "transform",
				Type:           "Transform",
				Dependencies:   []string{"branch"},
				IncomingBranch: "paid",
				Config:         map[string]any{"template": "paid path"},
			},
			{
				NodeID:         "end",
				Type:           "End",
				Dependencies:   []string{"branch", "transform"},
				IncomingBranch: "default",
				Config:         map[string]any{"output": map[string]any{"kind": "ref", "path": "input.orderId"}},
			},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"orderId": "A10293", "route": "paid"},
	})
	if err == nil {
		t.Fatal("expected missing terminal error")
	}
	if execution.Status != domain.ExecutionFailed {
		t.Fatalf("expected failed execution when no terminal node is reached, got %s", execution.Status)
	}
	if !hasStepStatus(execution.Steps, "end", domain.ExecutionStepSkipped) {
		t.Fatalf("expected terminal end node to be recorded as skipped when branch routing leaves no terminal path, got %#v", execution.Steps)
	}
	if !strings.Contains(stepOutputSummary(execution.Steps, "end"), "branch") {
		t.Fatalf("expected skipped terminal node to explain branch routing, got %#v", execution.Steps)
	}
}

func TestPlanRunnerPassesToolInvocationContextToInvoker(t *testing.T) {
	invoker := &fakeToolInvoker{
		results: map[string]map[string]any{
			"order.status.query": {"status": "paid"},
		},
	}
	runner := NewPlanRunner(invoker)

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-tool-context",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "tool", Type: "Tool", Dependencies: []string{"start"}, Config: map[string]any{"toolId": "order.status.query"}},
			{NodeID: "end", Type: "End", Dependencies: []string{"tool"}, Config: map[string]any{"output": map[string]any{"kind": "ref", "path": "nodeOutputs.tool.status"}}},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID:          "user-chen-ops",
		Input:           map[string]any{"orderId": "A10293"},
		WorkflowVersion: "v0.1.0",
		WorkspaceID:     "order",
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	ctx, ok := invoker.contexts["order.status.query"]
	if !ok {
		t.Fatalf("expected tool invoker context, got %#v", invoker.contexts)
	}
	if ctx.TraceID == "" {
		t.Fatalf("expected trace id to be forwarded, got %#v", ctx)
	}
	if ctx.WorkflowID != "wf-tool-context" || ctx.WorkspaceID != "order" || ctx.NodeID != "tool" {
		t.Fatalf("expected workflow/node context to be forwarded, got %#v", ctx)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected successful execution, got %s", execution.Status)
	}
}

func TestExternalPrincipalSnapshotsWorkflowResume(t *testing.T) {
	const (
		workspaceID = "c18f1f2e-7b5a-7c3d-8e9f-123456789001"
		actorID     = "c18f1f2e-7b5a-7c3d-8e9f-123456789002"
		subjectID   = "c18f1f2e-7b5a-7c3d-8e9f-123456789003"
		clientID    = "c18f1f2e-7b5a-7c3d-8e9f-123456789004"
		grantID     = "c18f1f2e-7b5a-7c3d-8e9f-123456789005"
		agentRunID  = "c18f1f2e-7b5a-7c3d-8e9f-123456789006"
		workflowID  = "c18f1f2e-7b5a-7c3d-8e9f-123456789007"
	)
	actor := principal.Ref{WorkspaceID: workspaceID, Type: principal.TypeServicePrincipal, ID: actorID}
	subject := principal.Ref{WorkspaceID: workspaceID, Type: principal.TypeExternalSubject, ID: subjectID}
	identity, err := principal.NewInvocationIdentity(actor, &subject)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := principal.NewExecutionSnapshot(identity, clientID, grantID, 3, 7)
	if err != nil {
		t.Fatal(err)
	}
	expected := *cloneExecutionPrincipalSnapshot(&snapshot)
	authorization := json.RawMessage(`{"decision":"allow","policy":"agent.v7"}`)
	invoker := &fakeToolInvoker{results: map[string]map[string]any{"external.tool": {"ok": true}}}
	runner := NewPlanRunner(invoker)
	plan := domain.CompiledExecutionPlan{
		WorkflowID: workflowID,
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "approval", Type: "Approval", Dependencies: []string{"start"}, Config: map[string]any{"reason": "confirm"}},
			{NodeID: "tool", Type: "Tool", Dependencies: []string{"approval"}, Config: map[string]any{"toolId": "external.tool"}},
			{NodeID: "end", Type: "End", Dependencies: []string{"tool"}},
		},
	}
	executionResult, checkpoint, err := runner.RunWithCheckpoint(plan, ExecutionContext{
		UserID: actorID, WorkspaceID: workspaceID, ActorType: string(principal.TypeServicePrincipal),
		PrincipalSnapshot: &snapshot, AuthorizationSnapshot: authorization, AgentRunID: agentRunID,
	})
	if err != nil || checkpoint == nil || executionResult.Status != domain.ExecutionApproval {
		t.Fatalf("approval run=%+v checkpoint=%+v err=%v", executionResult, checkpoint, err)
	}

	// The caller-owned values may change after suspension; the checkpoint must
	// retain the exact authorization-time binding.
	snapshot.ClientID = "c18f1f2e-7b5a-7c3d-8e9f-123456789008"
	snapshot.Identity.Subject.ID = "c18f1f2e-7b5a-7c3d-8e9f-123456789009"
	authorization[2] = 'X'
	resumed, err := runner.ConfirmApproval(*checkpoint, executionResult, "reviewer")
	if err != nil || resumed.Status != domain.ExecutionSuccess {
		t.Fatalf("resume=%+v err=%v", resumed, err)
	}
	invocationContext, ok := invoker.contexts["external.tool"]
	if !ok || invocationContext.PrincipalSnapshot == nil {
		t.Fatalf("missing resumed invocation context: %+v", invoker.contexts)
	}
	if !expected.SameBinding(*invocationContext.PrincipalSnapshot) ||
		invocationContext.ActorType != string(principal.TypeServicePrincipal) ||
		invocationContext.AgentRunID != agentRunID ||
		!bytes.Equal(invocationContext.AuthorizationSnapshot, json.RawMessage(`{"decision":"allow","policy":"agent.v7"}`)) {
		t.Fatalf("principal snapshot changed across resume: %+v", invocationContext)
	}
}

func TestNativeGraphRunnerExecutesCoreNodes(t *testing.T) {
	invoker := &fakeToolInvoker{
		results: map[string]map[string]any{
			"order.status.query": {"status": "paid"},
		},
	}
	runner := NewNativeGraphRunner(invoker)

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-native-core",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "tool", Type: "Tool", Dependencies: []string{"start"}, Config: map[string]any{"toolId": "order.status.query"}},
			{NodeID: "branch", Type: "Condition", Dependencies: []string{"tool"}, Config: map[string]any{"expression": "nodeOutputs.tool.status == 'paid'"}},
			{NodeID: "transform", Type: "Transform", Dependencies: []string{"branch"}, IncomingBranch: "paid", Config: map[string]any{"template": "{{input.orderId}} is paid"}},
			{NodeID: "end", Type: "End", Dependencies: []string{"transform"}, Config: map[string]any{"output": map[string]any{"kind": "ref", "path": "nodeOutputs.transform.result"}}},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID:      "user-chen-ops",
		Input:       map[string]any{"orderId": "A10293"},
		WorkspaceID: "order",
	})
	if err != nil {
		t.Fatalf("run native graph: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected successful native execution, got %s", execution.Status)
	}
	if execution.Trigger != "Eino Core Workflow Graph" {
		t.Fatalf("expected eino_core graph trigger, got %#v", execution)
	}
	if !strings.Contains(execution.OutputSummary, "A10293 is paid") {
		t.Fatalf("expected native output summary, got %s", execution.OutputSummary)
	}
}

func TestNativeGraphRunnerRejectsWrappedNodeTypes(t *testing.T) {
	runner := NewNativeGraphRunner(&fakeToolInvoker{})

	// ForEach is native after PR13d; unknown types still require wrapper.
	_, err := runner.Run(domain.CompiledExecutionPlan{
		WorkflowID: "wf-needs-wrapper",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "llm", Type: "LLM", Dependencies: []string{"start"}},
			{NodeID: "end", Type: "End", Dependencies: []string{"llm"}},
		},
	}, ExecutionContext{UserID: "user-chen-ops"})
	if err == nil {
		t.Fatal("expected native graph runner to reject unknown node type")
	}
	if !strings.Contains(err.Error(), "requires workflowruntime wrapper") {
		t.Fatalf("expected wrapped-node error, got %v", err)
	}
}

// TestEinoCoreRunnerExecutesForEachScopedSuccess aligns with
// plan_runner TestPlanRunnerExecutesForeachLoopNodesPerItem under true compose graph.
func TestEinoCoreRunnerExecutesForEachScopedSuccess(t *testing.T) {
	runner := NewEinoCoreRunnerWithInvoker(&fakeToolInvoker{})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-foreach-native",
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
			{
				NodeID:       "transform",
				Type:         "Transform",
				Dependencies: []string{"foreach"},
				Config: map[string]any{
					"template": "{{foreach.item}}",
				},
			},
			{
				NodeID:       "end",
				Type:         "End",
				Dependencies: []string{"foreach", "transform"},
				Config: map[string]any{
					"output": map[string]any{"kind": "ref", "path": "nodeOutputs.transform.count"},
				},
			},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"items": []any{"line-1", "line-2"}},
	})
	if err != nil {
		t.Fatalf("run ForEach native plan: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected ForEach native success, got %s err=%s", execution.Status, execution.ErrorMessage)
	}
	if !strings.Contains(execution.OutputSummary, "2") {
		t.Fatalf("expected foreach loop output count, got %s", execution.OutputSummary)
	}
	if !containsNodeStep(execution.Steps, "foreach") {
		t.Fatalf("expected foreach step, got %#v", execution.Steps)
	}
	if !containsNodeStep(execution.Steps, "transform") {
		t.Fatalf("expected transform loop step, got %#v", execution.Steps)
	}
}

// TestEinoCoreRunnerExecutesSubWorkflowNative aligns with
// plan_runner executeSubWorkflowNode success path under true compose graph.
func TestEinoCoreRunnerExecutesSubWorkflowNative(t *testing.T) {
	resolver := fakeRevisionResolver{
		revisions: map[string]domain.WorkflowRevision{
			"wf-fulfillment": {
				WorkflowID: "wf-fulfillment",
				RevisionID: "rev-child-1",
				Plan: domain.CompiledExecutionPlan{
					WorkflowID: "wf-fulfillment",
					Nodes: []domain.ExecutionPlanNode{
						{NodeID: "start", Type: "Start"},
						{
							NodeID:       "child-end",
							Type:         "End",
							Dependencies: []string{"start"},
							Config:       map[string]any{"output": map[string]any{"kind": "ref", "path": "input.orderId"}},
						},
					},
				},
			},
		},
	}
	runner := NewEinoCoreRunnerWithResolver(&fakeToolInvoker{}, resolver)

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-sub-native",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "sub",
				Type:         "SubWorkflow",
				Dependencies: []string{"start"},
				Config: map[string]any{
					"workflowId": "wf-fulfillment",
					"input": map[string]any{
						"orderId": map[string]any{"kind": "ref", "path": "input.orderId"},
					},
				},
			},
			{
				NodeID:       "end",
				Type:         "End",
				Dependencies: []string{"sub"},
				Config: map[string]any{
					"output": map[string]any{"kind": "ref", "path": "nodeOutputs.sub.status"},
				},
			},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID:      "user-chen-ops",
		Input:       map[string]any{"orderId": "A10293"},
		WorkspaceID: "order",
	})
	if err != nil {
		t.Fatalf("run SubWorkflow native graph: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected SubWorkflow native success, got %s err=%s", execution.Status, execution.ErrorMessage)
	}
	if !containsNodeStep(execution.Steps, "sub") {
		t.Fatalf("expected SubWorkflow node step, got %#v", execution.Steps)
	}
	if !containsNodeStep(execution.Steps, "child-end") {
		t.Fatalf("expected merged child-end step, got %#v", execution.Steps)
	}
	if !strings.Contains(execution.OutputSummary, "Success") {
		t.Fatalf("expected end output to expose sub.status=Success, got %q", execution.OutputSummary)
	}
}

// TestEinoCoreRunnerSubWorkflowNestedApprovalBubbles: child Approval pauses
// parent RunWithCheckpoint with Approval status + checkpoint (PR13c bubble).
func TestEinoCoreRunnerSubWorkflowNestedApprovalBubbles(t *testing.T) {
	resolver := fakeRevisionResolver{
		revisions: map[string]domain.WorkflowRevision{
			"wf-child-approval": {
				WorkflowID: "wf-child-approval",
				RevisionID: "rev-child-appr",
				Plan: domain.CompiledExecutionPlan{
					WorkflowID: "wf-child-approval",
					Nodes: []domain.ExecutionPlanNode{
						{NodeID: "start", Type: "Start"},
						{
							NodeID:       "approval",
							Type:         "Approval",
							Dependencies: []string{"start"},
							Config:       map[string]any{"reason": "nested sign-off"},
						},
						{NodeID: "end", Type: "End", Dependencies: []string{"approval"}},
					},
				},
			},
		},
	}
	runner := NewEinoCoreRunnerWithResolver(&fakeToolInvoker{}, resolver)

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-parent-nested-appr",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "sub",
				Type:         "SubWorkflow",
				Dependencies: []string{"start"},
				Config:       map[string]any{"workflowId": "wf-child-approval"},
			},
			{NodeID: "end", Type: "End", Dependencies: []string{"sub"}},
		},
	}

	execution, cp, err := runner.RunWithCheckpoint(plan, ExecutionContext{
		UserID:      "user-chen-ops",
		Input:       map[string]any{"orderId": "A1"},
		WorkspaceID: "ws-nested",
	})
	if err != nil {
		t.Fatalf("RunWithCheckpoint nested Approval: %v", err)
	}
	if execution.Status != domain.ExecutionApproval {
		t.Fatalf("status=%s want Approval", execution.Status)
	}
	if cp == nil {
		t.Fatal("expected WorkflowApprovalCheckpoint from nested bubble")
	}
	if strings.TrimSpace(cp.EinoCheckPointID) == "" {
		t.Fatal("expected parent EinoCheckPointID for compose resume surface")
	}
	if cp.NodeID == "" {
		t.Fatal("expected approval node id on checkpoint")
	}
}

// TestEinoCoreRunnerExecutesHTTPNative aligns with plan_runner HTTP simulation:
// method/endpoint Passed step and nodeOutputs.http.status for End refs.
func TestEinoCoreRunnerExecutesHTTPNative(t *testing.T) {
	runner := NewEinoCoreRunnerWithInvoker(&fakeToolInvoker{})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-http-native",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "http",
				Type:         "HTTP",
				Dependencies: []string{"start"},
				Config: map[string]any{
					"method":   "POST",
					"endpoint": "/orders/check",
					"input": map[string]any{
						"orderId": map[string]any{"kind": "ref", "path": "input.orderId"},
					},
				},
			},
			{
				NodeID:       "end",
				Type:         "End",
				Dependencies: []string{"http"},
				Config: map[string]any{
					"output": map[string]any{"kind": "ref", "path": "nodeOutputs.http.status"},
				},
			},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID:      "user-chen-ops",
		Input:       map[string]any{"orderId": "A10293"},
		WorkspaceID: "order",
	})
	if err != nil {
		t.Fatalf("run HTTP native graph: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected HTTP native success, got %s err=%s", execution.Status, execution.ErrorMessage)
	}
	if execution.Trigger != "Eino Core Workflow Graph" {
		t.Fatalf("expected eino_core graph trigger, got %#v", execution)
	}
	if !containsNodeStep(execution.Steps, "http") {
		t.Fatalf("expected HTTP node step, got %#v", execution.Steps)
	}
	httpInput := ""
	for _, step := range execution.Steps {
		if step.NodeID == "http" {
			httpInput = step.InputSummary
			break
		}
	}
	if !strings.Contains(httpInput, "POST") || !strings.Contains(httpInput, "/orders/check") {
		t.Fatalf("expected HTTP step METHOD endpoint input, got %q", httpInput)
	}
	if !strings.Contains(execution.OutputSummary, "ok") {
		t.Fatalf("expected end output to expose http.status=ok, got %q", execution.OutputSummary)
	}
}

// TestEinoCoreRunnerExecutesParallelNative aligns with
// TestPlanRunnerRecordsSequentialParallelBranchTrace: same plan shape under
// true compose graph (PR13a Parallel native).
func TestEinoCoreRunnerExecutesParallelNative(t *testing.T) {
	invoker := &fakeToolInvoker{
		results: map[string]map[string]any{
			"risk.check":     {"status": "ok"},
			"inventory.sync": {"status": "queued"},
		},
	}
	runner := NewEinoCoreRunnerWithInvoker(invoker)

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-parallel-native",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "parallel",
				Type:         "Parallel",
				Dependencies: []string{"start"},
				Config:       map[string]any{"branches": []any{"risk-check", "inventory-sync"}},
			},
			{
				NodeID:         "risk-check",
				Type:           "Tool",
				Dependencies:   []string{"parallel"},
				IncomingBranch: "risk-check",
				Config:         map[string]any{"toolId": "risk.check"},
			},
			{
				NodeID:         "inventory-sync",
				Type:           "Tool",
				Dependencies:   []string{"parallel"},
				IncomingBranch: "inventory-sync",
				Config:         map[string]any{"toolId": "inventory.sync"},
			},
			{
				NodeID:       "end",
				Type:         "End",
				Dependencies: []string{"risk-check", "inventory-sync"},
				Config: map[string]any{
					"output": map[string]any{"kind": "ref", "path": "nodeOutputs.parallel.branchCount"},
				},
			},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID:      "user-chen-ops",
		Input:       map[string]any{"orderId": "A10293"},
		WorkspaceID: "order",
	})
	if err != nil {
		t.Fatalf("run parallel native graph: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected Parallel native success, got %s err=%s", execution.Status, execution.ErrorMessage)
	}
	if execution.Trigger != "Eino Core Workflow Graph" {
		t.Fatalf("expected eino_core graph trigger, got %#v", execution)
	}

	parallelOutput := stepOutputSummary(execution.Steps, "parallel")
	if !strings.Contains(parallelOutput, "risk-check") || !strings.Contains(parallelOutput, "inventory-sync") {
		t.Fatalf("expected Parallel step branch trace, got %q", parallelOutput)
	}
	if !strings.Contains(execution.OutputSummary, "2") {
		t.Fatalf("expected end output to expose branch count, got %q", execution.OutputSummary)
	}
	if !containsNodeStep(execution.Steps, "risk-check") || !containsNodeStep(execution.Steps, "inventory-sync") {
		t.Fatalf("expected both Parallel branch tool steps, got %#v", execution.Steps)
	}
}

func TestWrappedPlanRunnerExecutesAdvancedNodes(t *testing.T) {
	runner := NewWrappedPlanRunner(&fakeToolInvoker{})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-wrapped-advanced",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "parallel", Type: "Parallel", Dependencies: []string{"start"}, Config: map[string]any{"branches": []any{"http"}}},
			{NodeID: "http", Type: "HTTP", Dependencies: []string{"parallel"}, Config: map[string]any{"method": "POST", "endpoint": "/orders/check"}},
			{NodeID: "end", Type: "End", Dependencies: []string{"http"}, Config: map[string]any{"output": map[string]any{"kind": "ref", "path": "nodeOutputs.http.status"}}},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"orderId": "A10293"},
	})
	if err != nil {
		t.Fatalf("run wrapped plan: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected wrapped execution success, got %s", execution.Status)
	}
	if execution.Trigger != "Workflow Runtime Wrapper" {
		t.Fatalf("expected wrapper trigger, got %#v", execution)
	}
}

func TestPlanRunnerDoesNotIgnoreSkippedDependencyFromDifferentCondition(t *testing.T) {
	runner := NewPlanRunner(&fakeToolInvoker{})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-cross-condition-skip",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "gate-a", Type: "Condition", Dependencies: []string{"start"}, Config: map[string]any{"expression": "input.approved == true"}},
			{NodeID: "gate-b", Type: "Condition", Dependencies: []string{"start"}, Config: map[string]any{"expression": "input.paid == true"}},
			{
				NodeID:         "transform-a",
				Type:           "Transform",
				Dependencies:   []string{"gate-a"},
				IncomingBranch: "approved",
				Config:         map[string]any{"template": "approved path"},
			},
			{
				NodeID:         "transform-b",
				Type:           "Transform",
				Dependencies:   []string{"gate-b"},
				IncomingBranch: "paid",
				Config:         map[string]any{"template": "paid path"},
			},
			{
				NodeID:         "end",
				Type:           "End",
				Dependencies:   []string{"gate-a", "transform-a", "transform-b"},
				IncomingBranch: "approved",
				Config:         map[string]any{"output": map[string]any{"kind": "ref", "path": "input.orderId"}},
			},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"orderId": "A10293", "approved": true, "paid": false},
	})
	if err == nil {
		t.Fatal("expected missing required dependency from different condition to fail")
	}
	if execution.Status != domain.ExecutionFailed {
		t.Fatalf("expected failed execution status, got %s", execution.Status)
	}
	if !hasStepStatus(execution.Steps, "end", domain.ExecutionStepSkipped) {
		t.Fatalf("expected terminal node to be recorded as skipped when blocked by another condition branch, got %#v", execution.Steps)
	}
	if !strings.Contains(stepOutputSummary(execution.Steps, "end"), "gate-b") {
		t.Fatalf("expected skipped terminal node to preserve blocking condition cause, got %#v", execution.Steps)
	}
}

func TestPlanRunnerAllowsMultiHopSkippedBranchMerge(t *testing.T) {
	runner := NewPlanRunner(&fakeToolInvoker{})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-multi-hop-merge",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "branch", Type: "Condition", Dependencies: []string{"start"}, Config: map[string]any{"expression": "input.route == 'paid'"}},
			{
				NodeID:         "tool",
				Type:           "Tool",
				Dependencies:   []string{"branch"},
				IncomingBranch: "paid",
				Config:         map[string]any{"toolId": "order.status.query"},
			},
			{
				NodeID:       "transform",
				Type:         "Transform",
				Dependencies: []string{"tool"},
				Config:       map[string]any{"template": "paid path"},
			},
			{
				NodeID:         "end",
				Type:           "End",
				Dependencies:   []string{"branch", "transform"},
				IncomingBranch: "default",
				Config:         map[string]any{"output": map[string]any{"kind": "ref", "path": "input.orderId"}},
			},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"orderId": "A10293", "route": "default"},
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected default branch to merge past skipped multi-hop paid path, got %s", execution.Status)
	}
	if !containsNodeStep(execution.Steps, "end") {
		t.Fatalf("expected end node to execute after multi-hop skipped branch, got %#v", execution.Steps)
	}
}

func TestNormalizeToolResultDoesNotSynthesizeOrderStatus(t *testing.T) {
	normalized := normalizeToolResult("order.status.query", map[string]any{
		"data": map[string]any{
			"toolId": "order.status.query",
		},
	})

	if _, ok := normalized["status"]; ok {
		t.Fatalf("expected normalized tool result to keep missing status missing, got %#v", normalized)
	}
}

func TestPlanRunnerAllowsMergeWithoutDirectConditionDependency(t *testing.T) {
	runner := NewPlanRunner(&fakeToolInvoker{})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-indirect-merge",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "branch", Type: "Condition", Dependencies: []string{"start"}, Config: map[string]any{"expression": "input.route == 'paid'"}},
			{
				NodeID:         "paid-1",
				Type:           "Transform",
				Dependencies:   []string{"branch"},
				IncomingBranch: "paid",
				Config:         map[string]any{"template": "paid 1"},
			},
			{
				NodeID:       "paid-2",
				Type:         "Transform",
				Dependencies: []string{"paid-1"},
				Config:       map[string]any{"template": "paid 2"},
			},
			{
				NodeID:         "default-1",
				Type:           "Transform",
				Dependencies:   []string{"branch"},
				IncomingBranch: "default",
				Config:         map[string]any{"template": "default 1"},
			},
			{
				NodeID:       "end",
				Type:         "End",
				Dependencies: []string{"paid-2", "default-1"},
				Config:       map[string]any{"output": map[string]any{"kind": "ref", "path": "input.orderId"}},
			},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"orderId": "A10293", "route": "default"},
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected indirect default merge to reach end node, got %s", execution.Status)
	}
	if !containsNodeStep(execution.Steps, "end") {
		t.Fatalf("expected end node to execute for indirect merge, got %#v", execution.Steps)
	}
}

func TestPlanRunnerExecutesAdvancedNodes(t *testing.T) {
	runner := NewPlanRunnerWithRevisionResolver(&fakeToolInvoker{}, fakeRevisionResolver{
		revisions: map[string]domain.WorkflowRevision{
			"wf-fulfillment": {
				WorkflowID: "wf-fulfillment",
				RevisionID: "rev-child-1",
				Plan: domain.CompiledExecutionPlan{
					WorkflowID: "wf-fulfillment",
					Nodes: []domain.ExecutionPlanNode{
						{NodeID: "start", Type: "Start"},
						{
							NodeID:       "child-end",
							Type:         "End",
							Dependencies: []string{"start"},
							Config:       map[string]any{"output": map[string]any{"kind": "ref", "path": "input.orderId"}},
						},
					},
				},
			},
		},
	})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-advanced-runtime",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "parallel", Type: "Parallel", Dependencies: []string{"start"}, Config: map[string]any{"branches": []any{"http", "subworkflow"}}},
			{
				NodeID:       "http",
				Type:         "HTTP",
				Dependencies: []string{"parallel"},
				Config: map[string]any{
					"method":   "POST",
					"endpoint": "/orders/check",
					"input": map[string]any{
						"orderId": map[string]any{"kind": "ref", "path": "input.orderId"},
					},
				},
			},
			{
				NodeID:       "sub",
				Type:         "SubWorkflow",
				Dependencies: []string{"parallel"},
				Config: map[string]any{
					"workflowId": "wf-fulfillment",
					"input": map[string]any{
						"orderId": map[string]any{"kind": "ref", "path": "input.orderId"},
					},
				},
			},
			{
				NodeID:       "foreach",
				Type:         "ForEach",
				Dependencies: []string{"http"},
				Config: map[string]any{
					"collection":  map[string]any{"kind": "ref", "path": "input.items"},
					"itemAlias":   "item",
					"concurrency": 2,
				},
			},
			{NodeID: "end", Type: "End", Dependencies: []string{"sub", "foreach"}, Config: map[string]any{"output": map[string]any{"kind": "ref", "path": "nodeOutputs.foreach.count"}}},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"orderId": "A10293", "items": []any{"line-1", "line-2"}},
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected advanced runtime execution success, got %s", execution.Status)
	}
	for _, nodeID := range []string{"parallel", "http", "sub", "foreach", "end"} {
		if !containsNodeStep(execution.Steps, nodeID) {
			t.Fatalf("expected %s node step, got %#v", nodeID, execution.Steps)
		}
	}
	if !containsNodeStep(execution.Steps, "child-end") {
		t.Fatalf("expected subworkflow child plan step, got %#v", execution.Steps)
	}
	if !strings.Contains(execution.OutputSummary, "2") {
		t.Fatalf("expected foreach count in output summary, got %s", execution.OutputSummary)
	}
}

func TestPlanRunnerRecordsSequentialParallelBranchTrace(t *testing.T) {
	invoker := &fakeToolInvoker{
		results: map[string]map[string]any{
			"risk.check":     {"status": "ok"},
			"inventory.sync": {"status": "queued"},
		},
	}
	runner := NewPlanRunner(invoker)

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-parallel-trace",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "parallel",
				Type:         "Parallel",
				Dependencies: []string{"start"},
				Config:       map[string]any{"branches": []any{"risk-check", "inventory-sync"}},
			},
			{
				NodeID:         "risk-check",
				Type:           "Tool",
				Dependencies:   []string{"parallel"},
				IncomingBranch: "risk-check",
				Config:         map[string]any{"toolId": "risk.check"},
			},
			{
				NodeID:         "inventory-sync",
				Type:           "Tool",
				Dependencies:   []string{"parallel"},
				IncomingBranch: "inventory-sync",
				Config:         map[string]any{"toolId": "inventory.sync"},
			},
			{
				NodeID:       "end",
				Type:         "End",
				Dependencies: []string{"risk-check", "inventory-sync"},
				Config:       map[string]any{"output": map[string]any{"kind": "ref", "path": "nodeOutputs.parallel.branchCount"}},
			},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"orderId": "A10293"},
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected sequential parallel simulation to succeed, got %s", execution.Status)
	}

	parallelOutput := stepOutputSummary(execution.Steps, "parallel")
	if !strings.Contains(parallelOutput, "risk-check") || !strings.Contains(parallelOutput, "inventory-sync") {
		t.Fatalf("expected parallel step to record branch trace summary, got %q", parallelOutput)
	}
	if !strings.Contains(execution.OutputSummary, "2") {
		t.Fatalf("expected end output to expose branch count, got %q", execution.OutputSummary)
	}
}

func TestPlanRunnerExecutesForeachLoopNodesPerItem(t *testing.T) {
	runner := NewPlanRunner(&fakeToolInvoker{})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-foreach-loop",
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
			{
				NodeID:       "transform",
				Type:         "Transform",
				Dependencies: []string{"foreach"},
				Config: map[string]any{
					"template": "{{foreach.item}}",
				},
			},
			{NodeID: "end", Type: "End", Dependencies: []string{"foreach", "transform"}, Config: map[string]any{"output": map[string]any{"kind": "ref", "path": "nodeOutputs.transform.count"}}},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"items": []any{"line-1", "line-2"}},
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected foreach loop execution success, got %s", execution.Status)
	}
	if !strings.Contains(execution.OutputSummary, "2") {
		t.Fatalf("expected foreach loop output count, got %s", execution.OutputSummary)
	}
}

func TestPlanRunnerSupportsForeachAliasAndLoopOutputMapping(t *testing.T) {
	runner := NewPlanRunner(&fakeToolInvoker{})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-foreach-alias-output",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "foreach",
				Type:         "ForEach",
				Dependencies: []string{"start"},
				Config: map[string]any{
					"collection":  map[string]any{"kind": "ref", "path": "input.items"},
					"itemAlias":   "lineItem",
					"concurrency": 3,
					"output": map[string]any{
						"items": map[string]any{"kind": "ref", "path": "nodeOutputs.transform.items"},
						"count": map[string]any{"kind": "ref", "path": "nodeOutputs.transform.count"},
					},
				},
			},
			{
				NodeID:       "transform",
				Type:         "Transform",
				Dependencies: []string{"foreach"},
				Config: map[string]any{
					"template": "{{foreach.lineItem.id}}",
				},
			},
			{
				NodeID:       "end",
				Type:         "End",
				Dependencies: []string{"foreach", "transform"},
				Config: map[string]any{
					"output": map[string]any{"kind": "ref", "path": "nodeOutputs.foreach.items"},
				},
			},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input: map[string]any{
			"items": []any{
				map[string]any{"id": "line-1"},
				map[string]any{"id": "line-2"},
			},
		},
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected foreach alias execution success, got %s", execution.Status)
	}
	if !strings.Contains(execution.OutputSummary, "line-1") || !strings.Contains(execution.OutputSummary, "line-2") {
		t.Fatalf("expected foreach loop output mapping to surface per-item results, got %q", execution.OutputSummary)
	}

	foreachStep := lastNodeStep(execution.Steps, "foreach")
	if !strings.Contains(foreachStep.OutputSummary, "alias=lineItem") || !strings.Contains(foreachStep.OutputSummary, "concurrency=3") {
		t.Fatalf("expected foreach trace to explain alias and concurrency, got %#v", foreachStep)
	}
}

func TestPlanRunnerFinishesForeachChainWithoutExplicitJoinDependency(t *testing.T) {
	runner := NewPlanRunner(&fakeToolInvoker{})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-foreach-chain",
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
			{
				NodeID:       "transform",
				Type:         "Transform",
				Dependencies: []string{"foreach"},
				Config: map[string]any{
					"template": "{{foreach.item}}",
				},
			},
			{NodeID: "end", Type: "End", Dependencies: []string{"transform"}, Config: map[string]any{"output": map[string]any{"kind": "ref", "path": "nodeOutputs.transform.count"}}},
		},
	}

	execution, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"items": []any{"line-1", "line-2"}},
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if execution.Status != domain.ExecutionSuccess {
		t.Fatalf("expected foreach chain to reach terminal end, got %s", execution.Status)
	}
	if !containsNodeStep(execution.Steps, "end") {
		t.Fatalf("expected end node step in foreach chain, got %#v", execution.Steps)
	}
}

func TestPlanRunnerRejectsConditionInsideForeachLoop(t *testing.T) {
	runner := NewPlanRunner(&fakeToolInvoker{})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-foreach-condition",
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
			{
				NodeID:       "branch",
				Type:         "Condition",
				Dependencies: []string{"foreach"},
				Config: map[string]any{
					"expression": "foreach.item == 'line-1'",
				},
			},
			{NodeID: "end", Type: "End", Dependencies: []string{"branch"}, IncomingBranch: "default", Config: map[string]any{"output": map[string]any{"kind": "ref", "path": "input.items"}}},
		},
	}

	_, err := runner.Run(plan, ExecutionContext{
		UserID: "user-chen-ops",
		Input:  map[string]any{"items": []any{"line-1", "line-2"}},
	})
	if err == nil {
		t.Fatal("expected foreach-controlled condition to be rejected")
	}
}

type fakeToolInvoker struct {
	results  map[string]map[string]any
	errors   map[string]error
	inputs   map[string]map[string]any
	contexts map[string]ToolInvocationContext
}

func (f *fakeToolInvoker) Invoke(toolID string, input map[string]any, ctx ToolInvocationContext) (map[string]any, error) {
	if f.inputs == nil {
		f.inputs = map[string]map[string]any{}
	}
	if f.contexts == nil {
		f.contexts = map[string]ToolInvocationContext{}
	}
	f.inputs[toolID] = cloneMap(input)
	f.contexts[toolID] = ctx
	if err, ok := f.errors[toolID]; ok {
		return nil, err
	}
	if result, ok := f.results[toolID]; ok {
		return result, nil
	}
	return map[string]any{}, nil
}

type fakeRevisionResolver struct {
	revisions map[string]domain.WorkflowRevision
}

func (f fakeRevisionResolver) ResolvePublishedRevision(workflowID string) (domain.WorkflowRevision, error) {
	revision, ok := f.revisions[workflowID]
	if !ok {
		return domain.WorkflowRevision{}, errors.New("workflow revision not found")
	}
	return revision, nil
}

func containsNodeStep(steps []domain.ExecutionStepRecord, nodeID string) bool {
	for _, step := range steps {
		if step.NodeID == nodeID {
			return true
		}
	}
	return false
}

func stepOutputSummary(steps []domain.ExecutionStepRecord, nodeID string) string {
	for _, step := range steps {
		if step.NodeID == nodeID {
			return step.OutputSummary
		}
	}
	return ""
}

func lastNodeStep(steps []domain.ExecutionStepRecord, nodeID string) domain.ExecutionStepRecord {
	for index := len(steps) - 1; index >= 0; index-- {
		if steps[index].NodeID == nodeID {
			return steps[index]
		}
	}
	return domain.ExecutionStepRecord{}
}

func hasStepStatus(steps []domain.ExecutionStepRecord, nodeID string, status domain.ExecutionStepStatus) bool {
	for _, step := range steps {
		if step.NodeID == nodeID && step.Status == status {
			return true
		}
	}
	return false
}
