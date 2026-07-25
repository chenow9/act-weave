package workflowruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/execution"
)

func TestConfirmationResumeWorkflowExecutorUsesPersistedRevisionWithoutResolver(t *testing.T) {
	snapshot := publishedRevisionSnapshot()
	snapshot.PlanHash = strings.Repeat("d", 64)
	resolver := &revisionSnapshotResolverStub{snapshot: snapshot}
	capturing := &resumeCompiledPlanExecutor{}
	runner, err := NewPublishedRevisionRunner(resolver, capturing)
	if err != nil {
		t.Fatal(err)
	}
	request := PublishedRunRequest{
		WorkspaceID: "workspace-1", CapabilityID: "workflow-1", ReleaseID: "release-1",
		ActorID: "user-1", Input: map[string]any{"orderId": "order-1"},
	}
	requestSnapshot, resolvedSnapshot, err := BuildWorkflowConfirmationResumeSnapshots(request, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	resumeExecutor, err := NewConfirmationResumeExecutor(runner)
	if err != nil {
		t.Fatal(err)
	}
	resolver.snapshot.Plan.Nodes[1].Config["endpoint"] = "/latest-release-must-not-run"
	output, err := resumeExecutor.Execute(context.Background(), execution.ResumeExecutionInput{
		ConfirmationID: "confirmation-1", RequestSnapshot: requestSnapshot,
		ResolvedSnapshot: resolvedSnapshot, Input: json.RawMessage(`{"orderId":"order-1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 0 {
		t.Fatalf("workflow resume consulted mutable resolver %d times", resolver.calls)
	}
	if capturing.calls != 1 || capturing.plan.Nodes[1].Config["endpoint"] != "/orders/check" {
		t.Fatalf("workflow resume did not execute persisted plan: calls=%d plan=%+v", capturing.calls, capturing.plan)
	}
	if !strings.Contains(string(output.Result), snapshot.RevisionID) ||
		!strings.Contains(string(output.Result), snapshot.PlanHash) {
		t.Fatalf("workflow resume result lacks immutable identity: %s", output.Result)
	}
}

// TestConfirmationResumeApprovalUsesResumeApprovalNotWholePlanReRun proves PR14
// strategy C: when Approval checkpoint is present, Execute calls ResumeApproval
// (compose / ConfirmApproval path) rather than re-running the plan from start.
func TestConfirmationResumeApprovalUsesResumeApprovalNotWholePlanReRun(t *testing.T) {
	t.Parallel()
	snapshot := approvalRevisionSnapshot()
	snapshot.PlanHash = strings.Repeat("a", 64)
	resolver := &revisionSnapshotResolverStub{snapshot: snapshot}
	capturing := &resumeCompiledPlanExecutor{}
	runner, err := NewPublishedRevisionRunner(resolver, capturing)
	if err != nil {
		t.Fatal(err)
	}
	request := PublishedRunRequest{
		WorkspaceID: "workspace-1", CapabilityID: "workflow-approval", ReleaseID: "release-1",
		ActorID: "requester-1", Input: map[string]any{"orderId": "A10293"},
	}
	approval := &WorkflowApprovalCheckpoint{
		ID: "approval-1", WorkflowID: "workflow-approval", ExecutionID: "exec-1",
		NodeID: "approval", NodeReason: "paid order requires review",
		Status: WorkflowApprovalPending, Plan: snapshot.Plan,
		RequestedBy:      "requester-1",
		EinoCheckPointID: "ws/ws-1/workflow_exec/exec-1/nonce",
		EinoInterruptIDs: []string{"interrupt-root"},
	}
	requestSnapshot, resolvedSnapshot, err := BuildWorkflowConfirmationResumeSnapshotsWithApproval(
		request, snapshot, "", approval,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Snapshots must carry compose surface.
	if !strings.Contains(string(requestSnapshot), "einoCheckPointId") ||
		!strings.Contains(string(resolvedSnapshot), `"approval"`) {
		t.Fatalf("approval surface missing: req=%s resolved=%s", requestSnapshot, resolvedSnapshot)
	}
	resumeExecutor, err := NewConfirmationResumeExecutor(runner)
	if err != nil {
		t.Fatal(err)
	}
	output, err := resumeExecutor.Execute(context.Background(), execution.ResumeExecutionInput{
		ConfirmationID: "confirmation-approval", RequestSnapshot: requestSnapshot,
		ResolvedSnapshot: resolvedSnapshot, Input: json.RawMessage(`{"orderId":"A10293"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if capturing.resumeCalls != 1 {
		t.Fatalf("expected ResumeApproval calls=1, got %d (runCalls=%d)", capturing.resumeCalls, capturing.calls)
	}
	if capturing.calls != 0 {
		t.Fatalf("Approval resume must not whole-plan re-run; RunWithCheckpoint calls=%d", capturing.calls)
	}
	if !strings.Contains(string(output.Result), snapshot.RevisionID) {
		t.Fatalf("resume result lacks revision: %s", output.Result)
	}
}

// TestConfirmationResumeApprovalWrapperConfirmEndToEnd: wrapper path pause →
// ConfirmApproval via ConfirmationResumeExecutor → Success (not re-hit Approval).
func TestConfirmationResumeApprovalWrapperConfirmEndToEnd(t *testing.T) {
	t.Parallel()
	snapshot := approvalRevisionSnapshot()
	snapshot.PlanHash = strings.Repeat("b", 64)
	resolver := &revisionSnapshotResolverStub{snapshot: snapshot}
	executor := NewWrappedPlanRunner(nil)
	runner, err := NewPublishedRevisionRunner(resolver, executor)
	if err != nil {
		t.Fatal(err)
	}
	// Pause
	result, err := runner.Run(context.Background(), PublishedRunRequest{
		WorkspaceID: "workspace-1", CapabilityID: "workflow-approval", ReleaseID: "release-1",
		ActorID: "requester-1", Input: map[string]any{"orderId": "A10293"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Execution.Status != domain.ExecutionApproval || result.Approval == nil {
		t.Fatalf("expected Approval pause, got status=%s approval=%v", result.Execution.Status, result.Approval != nil)
	}
	// Resume via ConfirmationResumeExecutor (product path).
	requestSnapshot, resolvedSnapshot, err := BuildWorkflowConfirmationResumeSnapshotsWithApproval(
		PublishedRunRequest{
			WorkspaceID: "workspace-1", CapabilityID: "workflow-approval", ReleaseID: "release-1",
			ActorID: "requester-1", Input: map[string]any{"orderId": "A10293"},
		},
		snapshot, "", result.Approval,
	)
	if err != nil {
		t.Fatal(err)
	}
	resumeExecutor, err := NewConfirmationResumeExecutor(runner)
	if err != nil {
		t.Fatal(err)
	}
	output, err := resumeExecutor.Execute(context.Background(), execution.ResumeExecutionInput{
		ConfirmationID: "c-approval", RequestSnapshot: requestSnapshot,
		ResolvedSnapshot: resolvedSnapshot, Input: json.RawMessage(`{"orderId":"A10293"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output.Result), `"Success"`) &&
		!strings.Contains(string(output.Result), string(domain.ExecutionSuccess)) {
		// domain.ExecutionSuccess is "Success"
		if !strings.Contains(string(output.Result), "A10293") {
			t.Fatalf("expected resumed success payload, got %s", output.Result)
		}
	}
	var decoded struct {
		ExecutionStatus string `json:"executionStatus"`
	}
	if err := json.Unmarshal(output.Result, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ExecutionStatus != string(domain.ExecutionSuccess) {
		t.Fatalf("executionStatus=%q want Success; payload=%s", decoded.ExecutionStatus, output.Result)
	}
}

func TestAAPDataPlaneAcceptanceWorkflowResume(t *testing.T) {
	t.Run("immutable revision", TestConfirmationResumeWorkflowExecutorUsesPersistedRevisionWithoutResolver)
	t.Run("approval checkpoint", TestPlanRunnerCreatesApprovalCheckpointAndConfirmsOriginalExecution)
}

type resumeCompiledPlanExecutor struct {
	calls       int
	resumeCalls int
	plan        domain.CompiledExecutionPlan
}

func (executor *resumeCompiledPlanExecutor) Run(
	plan domain.CompiledExecutionPlan,
	ctx ExecutionContext,
) (domain.Execution, error) {
	execution, _, err := executor.RunWithCheckpoint(plan, ctx)
	return execution, err
}

func (executor *resumeCompiledPlanExecutor) RunWithCheckpoint(
	plan domain.CompiledExecutionPlan,
	ctx ExecutionContext,
) (domain.Execution, *WorkflowApprovalCheckpoint, error) {
	executor.calls++
	executor.plan = plan
	return domain.Execution{
		ID: "persisted-execution", WorkflowID: plan.WorkflowID,
		WorkflowVersion: ctx.WorkflowVersion, WorkspaceID: ctx.WorkspaceID,
		Status: domain.ExecutionSuccess,
	}, nil, nil
}

func (executor *resumeCompiledPlanExecutor) ResumeApproval(
	plan domain.CompiledExecutionPlan,
	ctx ExecutionContext,
	_ WorkflowApprovalCheckpoint,
	_ ApprovalResumeDecision,
) (domain.Execution, error) {
	executor.resumeCalls++
	executor.plan = plan
	return domain.Execution{
		ID: "persisted-execution", WorkflowID: plan.WorkflowID,
		WorkflowVersion: ctx.WorkflowVersion, WorkspaceID: ctx.WorkspaceID,
		Status: domain.ExecutionSuccess,
	}, nil
}
