package workflowruntime

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/config"
	"actweave/backend/internal/domain"
)

func TestPublishedRevisionRunnerExecutesExplicitImmutableReleaseSnapshot(t *testing.T) {
	resolver := &revisionSnapshotResolverStub{snapshot: publishedRevisionSnapshot()}
	runner, err := NewPublishedRevisionRunner(resolver, NewWrappedPlanRunner(nil))
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), PublishedRunRequest{
		WorkspaceID: "workspace-1", CapabilityID: "workflow-1", ReleaseID: "release-1",
		ActorID: "user-1", Input: map[string]any{"orderId": "order-1"},
	})
	if err != nil {
		t.Fatalf("run published revision snapshot: %v", err)
	}
	if resolver.calls != 1 || result.Snapshot.RevisionID != "revision-1" ||
		result.Snapshot.ReleaseID != "release-1" || result.Snapshot.PlanHash != "plan-hash-1" {
		t.Fatalf("unexpected resolved snapshot: %+v calls=%d", result.Snapshot, resolver.calls)
	}
	if result.Execution.Status != domain.ExecutionSuccess ||
		result.Execution.WorkflowVersion != "revision-1" ||
		result.Execution.WorkspaceID != "workspace-1" ||
		result.Execution.Trigger != "Workflow Published Revision" {
		t.Fatalf("unexpected published execution: %+v", result.Execution)
	}
	if !hasExecutionStepType(result.Execution.Steps, "HTTP") {
		t.Fatalf("advanced HTTP node behavior did not run from revision plan: %+v", result.Execution.Steps)
	}
}

func TestPublishedRevisionRunnerClonesSnapshotBeforeExecution(t *testing.T) {
	resolver := &revisionSnapshotResolverStub{snapshot: publishedRevisionSnapshot()}
	executor := &blockingCompiledPlanExecutor{
		entered: make(chan domain.CompiledExecutionPlan, 1),
		release: make(chan struct{}),
	}
	runner, err := NewPublishedRevisionRunner(resolver, executor)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), PublishedRunRequest{
			WorkspaceID: "workspace-1", CapabilityID: "workflow-1", ReleaseID: "release-1",
			ActorID: "user-1", Input: map[string]any{"orderId": "order-1"},
		})
		result <- err
	}()
	var received domain.CompiledExecutionPlan
	select {
	case received = <-executor.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("published revision runner did not receive plan")
	}
	released := false
	defer func() {
		if !released {
			close(executor.release)
		}
	}()
	resolver.snapshot.Plan.Nodes[1].Config["endpoint"] = "/changed-after-resolution"
	if received.Nodes[1].Config["endpoint"] != "/orders/check" {
		t.Fatalf("resolved snapshot was not deep-cloned: %+v", received.Nodes[1].Config)
	}
	close(executor.release)
	released = true
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("published revision run did not finish")
	}
	if resolver.calls != 1 {
		t.Fatalf("runner re-resolved mutable state during execution: calls=%d", resolver.calls)
	}
}

type revisionSnapshotResolverStub struct {
	mu       sync.Mutex
	snapshot RevisionSnapshot
	calls    int
}

func (r *revisionSnapshotResolverStub) ResolveRevisionSnapshot(
	_ context.Context,
	_, _, _ string,
) (RevisionSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.snapshot, nil
}

type blockingCompiledPlanExecutor struct {
	entered chan domain.CompiledExecutionPlan
	release chan struct{}
}

func (r *blockingCompiledPlanExecutor) Run(
	plan domain.CompiledExecutionPlan,
	ctx ExecutionContext,
) (domain.Execution, error) {
	execution, _, err := r.RunWithCheckpoint(plan, ctx)
	return execution, err
}

func (r *blockingCompiledPlanExecutor) RunWithCheckpoint(
	plan domain.CompiledExecutionPlan,
	_ ExecutionContext,
) (domain.Execution, *WorkflowApprovalCheckpoint, error) {
	r.entered <- plan
	<-r.release
	return domain.Execution{Status: domain.ExecutionSuccess}, nil, nil
}

func (r *blockingCompiledPlanExecutor) ResumeApproval(
	_ domain.CompiledExecutionPlan,
	_ ExecutionContext,
	_ WorkflowApprovalCheckpoint,
	_ ApprovalResumeDecision,
) (domain.Execution, error) {
	return domain.Execution{Status: domain.ExecutionSuccess}, nil
}

func publishedRevisionSnapshot() RevisionSnapshot {
	return RevisionSnapshot{
		WorkspaceID: "workspace-1", CapabilityID: "workflow-1",
		ReleaseID: "release-1", RevisionID: "revision-1", PlanHash: "plan-hash-1",
		Plan: domain.CompiledExecutionPlan{
			WorkflowID: "workflow-1",
			Nodes: []domain.ExecutionPlanNode{
				{NodeID: "start", Type: "Start", Config: map[string]any{}},
				{NodeID: "http", Type: "HTTP", Dependencies: []string{"start"}, Config: map[string]any{
					"method": "POST", "endpoint": "/orders/check",
				}},
				{NodeID: "end", Type: "End", Dependencies: []string{"http"}, Config: map[string]any{}},
			},
		},
	}
}

func hasExecutionStepType(steps []domain.ExecutionStepRecord, nodeType string) bool {
	for _, step := range steps {
		if step.NodeType == nodeType && step.Status == domain.ExecutionStepPassed {
			return true
		}
	}
	return false
}

func approvalRevisionSnapshot() RevisionSnapshot {
	return RevisionSnapshot{
		WorkspaceID: "workspace-1", CapabilityID: "workflow-approval",
		ReleaseID: "release-1", RevisionID: "revision-1", PlanHash: "plan-hash-approval",
		Plan: domain.CompiledExecutionPlan{
			WorkflowID: "workflow-approval",
			Nodes: []domain.ExecutionPlanNode{
				{NodeID: "start", Type: "Start", Config: map[string]any{}},
				{NodeID: "approval", Type: "Approval", Dependencies: []string{"start"}, Config: map[string]any{
					"reason": "paid order requires review",
				}},
				{NodeID: "end", Type: "End", Dependencies: []string{"approval"}, Config: map[string]any{
					"output": map[string]any{"kind": "ref", "path": "input.orderId"},
				}},
			},
		},
	}
}

func TestPublishedRevisionRunnerPreservesWrapperApprovalCheckpoint(t *testing.T) {
	t.Parallel()
	resolver := &revisionSnapshotResolverStub{snapshot: approvalRevisionSnapshot()}
	// Config-driven default remains wrapper — zero behavior change vs NewWrappedPlanRunner.
	executor := NewExecutorFromConfig(
		config.WorkflowRuntimeConfig{},
		ExecutorFactoryConfig{Invoker: nil},
	)
	runner, err := NewPublishedRevisionRunner(resolver, executor)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), PublishedRunRequest{
		WorkspaceID: "workspace-1", CapabilityID: "workflow-approval", ReleaseID: "release-1",
		ActorID: "requester-1", Input: map[string]any{"orderId": "A10293"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Execution.Status != domain.ExecutionApproval {
		t.Fatalf("status=%s want Approval", result.Execution.Status)
	}
	if result.Approval == nil {
		t.Fatal("published result must surface Approval checkpoint (not discard)")
	}
	if result.Approval.NodeID != "approval" || result.Approval.Status != WorkflowApprovalPending {
		t.Fatalf("approval checkpoint: %#v", result.Approval)
	}
	if result.Approval.RequestedBy != "requester-1" {
		t.Fatalf("requestedBy=%q", result.Approval.RequestedBy)
	}
	// Wrapper path does not produce compose checkpoint IDs.
	if result.EinoCheckPointID != "" || result.Approval.EinoCheckPointID != "" {
		t.Fatalf("wrapper must not set EinoCheckPointID: result=%q approval=%q",
			result.EinoCheckPointID, result.Approval.EinoCheckPointID)
	}
}

func TestPublishedRevisionRunnerPreservesEinoCoreApprovalCheckpoint(t *testing.T) {
	t.Parallel()
	snapshot := approvalRevisionSnapshot()
	snapshot.WorkspaceID = "ws-eino"
	resolver := &revisionSnapshotResolverStub{snapshot: snapshot}
	executor := NewExecutorFromConfig(
		config.WorkflowRuntimeConfig{
			Engine:             config.WorkflowEngineEinoCore,
			AllowAllWorkspaces: true,
		},
		ExecutorFactoryConfig{
			Invoker:         nil,
			CheckPointStore: newMemCheckPointStore(),
		},
	)
	if _, ok := executor.(EinoCoreRunner); !ok {
		t.Fatalf("expected EinoCoreRunner, got %T", executor)
	}
	runner, err := NewPublishedRevisionRunner(resolver, executor)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), PublishedRunRequest{
		WorkspaceID: "ws-eino", CapabilityID: "workflow-approval", ReleaseID: "release-1",
		ActorID: "requester-1", Input: map[string]any{"orderId": "A10293"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Execution.Status != domain.ExecutionApproval {
		t.Fatalf("status=%s want Approval; errMsg=%s steps=%#v",
			result.Execution.Status, result.Execution.ErrorMessage, result.Execution.Steps)
	}
	if result.Approval == nil {
		t.Fatal("eino_core published result must surface Approval checkpoint (not discard)")
	}
	if result.Approval.NodeID != "approval" {
		t.Fatalf("approval node=%q", result.Approval.NodeID)
	}
	if result.Approval.EinoCheckPointID == "" {
		t.Fatal("eino_core Approval must carry EinoCheckPointID for compose resume")
	}
	if result.EinoCheckPointID == "" || result.EinoCheckPointID != result.Approval.EinoCheckPointID {
		t.Fatalf("PublishedRunResult.EinoCheckPointID=%q approval=%q",
			result.EinoCheckPointID, result.Approval.EinoCheckPointID)
	}
	if len(result.Approval.EinoInterruptIDs) == 0 {
		t.Fatal("eino_core Approval must carry EinoInterruptIDs for compose ResumeWithData")
	}
}

// TestPublishedRevisionRunnerEinoCoreApprovalResumeRoundTrip: pause → ResumeApproval → Success.
func TestPublishedRevisionRunnerEinoCoreApprovalResumeRoundTrip(t *testing.T) {
	t.Parallel()
	snapshot := approvalRevisionSnapshot()
	snapshot.WorkspaceID = "ws-eino-resume"
	snapshot.PlanHash = strings.Repeat("c", 64)
	resolver := &revisionSnapshotResolverStub{snapshot: snapshot}
	store := newMemCheckPointStore()
	executor := NewExecutorFromConfig(
		config.WorkflowRuntimeConfig{
			Engine:             config.WorkflowEngineEinoCore,
			AllowAllWorkspaces: true,
		},
		ExecutorFactoryConfig{
			Invoker:         nil,
			CheckPointStore: store,
		},
	)
	runner, err := NewPublishedRevisionRunner(resolver, executor)
	if err != nil {
		t.Fatal(err)
	}
	first, err := runner.Run(context.Background(), PublishedRunRequest{
		WorkspaceID: "ws-eino-resume", CapabilityID: "workflow-approval", ReleaseID: "release-1",
		ActorID: "requester-1", Input: map[string]any{"orderId": "A10293"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Execution.Status != domain.ExecutionApproval || first.Approval == nil {
		t.Fatalf("pause status=%s approval=%v", first.Execution.Status, first.Approval != nil)
	}
	second, err := runner.ResumeApproval(
		context.Background(),
		PublishedRunRequest{
			WorkspaceID: "ws-eino-resume", CapabilityID: "workflow-approval", ReleaseID: "release-1",
			ActorID: "approver-1", Input: map[string]any{"orderId": "A10293"},
		},
		snapshot,
		*first.Approval,
		ApprovalResumeDecision{Decision: "confirmed", ResolvedBy: "approver-1"},
	)
	if err != nil {
		t.Fatalf("ResumeApproval: %v", err)
	}
	if second.Execution.Status != domain.ExecutionSuccess {
		t.Fatalf("resume status=%s err=%s steps=%#v",
			second.Execution.Status, second.Execution.ErrorMessage, second.Execution.Steps)
	}
}
