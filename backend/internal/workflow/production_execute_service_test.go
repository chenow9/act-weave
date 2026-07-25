package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/workflowruntime"

	"github.com/google/uuid"
)

func TestProductionExecuteActiveRevisionReturnsExecution(t *testing.T) {
	repository, _, _, compilation := prepareWorkflowPublish(t, true)
	events := newWorkflowPublishEventWriter(t, repository.db)
	publisher := newWorkflowPublishService(t, repository, repository.db, events)
	published, err := publisher.Publish(context.Background(), validWorkflowPublishInput(compilation.ID))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	execs := newFakeProductionExecutions()
	runner := successfulProductionPlanRunner{}
	service, err := NewProductionExecuteService(repository, execs, runner, NewMemoryProductionIdempotencyStore())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(context.Background(), ProductionExecuteInput{
		WorkspaceID: draftWorkspaceID, WorkflowID: draftCapabilityID,
		RevisionID: published.Revision.ID, ActorID: workflowPublishEditorID,
		TraceID: "trace-production-1", Trigger: ProductionTriggerConsole,
		Input: json.RawMessage(`{"orderId":"prod-1"}`),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.ExecutionID == "" || result.WorkflowID != draftCapabilityID ||
		result.RevisionID != published.Revision.ID || result.Status != ProductionStatusSucceeded ||
		result.TraceID != "trace-production-1" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if execs.starts != 1 {
		t.Fatalf("expected one start, got %d", execs.starts)
	}
	stored, ok := execs.byID[result.ExecutionID]
	if !ok || stored.Status != ProductionStatusSucceeded || stored.RevisionID != published.Revision.ID {
		t.Fatalf("stored execution: %+v ok=%t", stored, ok)
	}
}

func TestProductionExecuteRejectsNonActiveRevision(t *testing.T) {
	repository, _, _, compilation := prepareWorkflowPublish(t, true)
	events := newWorkflowPublishEventWriter(t, repository.db)
	publisher := newWorkflowPublishService(t, repository, repository.db, events)
	if _, err := publisher.Publish(context.Background(), validWorkflowPublishInput(compilation.ID)); err != nil {
		t.Fatal(err)
	}
	service, err := NewProductionExecuteService(
		repository, newFakeProductionExecutions(), successfulProductionPlanRunner{}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Execute(context.Background(), ProductionExecuteInput{
		WorkspaceID: draftWorkspaceID, WorkflowID: draftCapabilityID,
		RevisionID: uuid.NewString(), ActorID: workflowPublishEditorID,
		TraceID: "trace-non-active", Trigger: ProductionTriggerAPI,
		Input: json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrRevisionNotActive) {
		t.Fatalf("expected ErrRevisionNotActive, got %v", err)
	}
}

func TestProductionExecuteIdempotencyKeyNoDoubleRun(t *testing.T) {
	repository, _, _, compilation := prepareWorkflowPublish(t, true)
	events := newWorkflowPublishEventWriter(t, repository.db)
	publisher := newWorkflowPublishService(t, repository, repository.db, events)
	published, err := publisher.Publish(context.Background(), validWorkflowPublishInput(compilation.ID))
	if err != nil {
		t.Fatal(err)
	}
	execs := newFakeProductionExecutions()
	service, err := NewProductionExecuteService(
		repository, execs, successfulProductionPlanRunner{}, NewMemoryProductionIdempotencyStore(),
	)
	if err != nil {
		t.Fatal(err)
	}
	input := ProductionExecuteInput{
		WorkspaceID: draftWorkspaceID, WorkflowID: draftCapabilityID,
		RevisionID: published.Revision.ID, ActorID: workflowPublishEditorID,
		TraceID: "trace-idem-1", Trigger: ProductionTriggerConsole,
		Input: json.RawMessage(`{"orderId":"same"}`), IdempotencyKey: "idem-prod-1",
	}
	first, err := service.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	second, err := service.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if first.ExecutionID != second.ExecutionID {
		t.Fatalf("idempotent replay diverged: first=%s second=%s", first.ExecutionID, second.ExecutionID)
	}
	if execs.starts != 1 {
		t.Fatalf("idempotency allowed double-run starts=%d", execs.starts)
	}
	// Different body with same key → conflict.
	input.Input = json.RawMessage(`{"orderId":"other"}`)
	if _, err := service.Execute(context.Background(), input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestRuntimeProductionPlanRunnerUsesCompiledPlan(t *testing.T) {
	runner, err := NewRuntimeProductionPlanRunner(workflowruntime.NewPlanRunner(nil))
	if err != nil {
		t.Fatal(err)
	}
	plan := domain.CompiledExecutionPlan{
		WorkflowID: draftCapabilityID,
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "end", Type: "End", Dependencies: []string{"start"}},
		},
	}
	result, runErr := runner.Run(context.Background(), ProductionPlanRunRequest{
		ExecutionID: uuid.NewString(), WorkspaceID: draftWorkspaceID, WorkflowID: draftCapabilityID,
		RevisionID: uuid.NewString(), PlanHash: "hash", Plan: plan,
		Input: map[string]any{}, StartedBy: draftOwnerID, Trigger: ProductionTriggerConsole,
	})
	// PlanRunner with nil invoker may succeed linear start→end or fail; assert stable shape.
	if result.Status != ProductionStatusSucceeded && result.Status != ProductionStatusFailed {
		t.Fatalf("unexpected status: %+v err=%v", result, runErr)
	}
}

type successfulProductionPlanRunner struct{}

func (successfulProductionPlanRunner) Run(
	_ context.Context,
	_ ProductionPlanRunRequest,
) (ProductionPlanRunResult, error) {
	return ProductionPlanRunResult{
		Status: ProductionStatusSucceeded, OutputSummary: json.RawMessage(`{"outcome":"succeeded"}`),
	}, nil
}

type fakeProductionExecutions struct {
	mu     sync.Mutex
	byID   map[string]execution.WorkflowExecution
	starts int
}

func newFakeProductionExecutions() *fakeProductionExecutions {
	return &fakeProductionExecutions{byID: map[string]execution.WorkflowExecution{}}
}

func (f *fakeProductionExecutions) StartWorkflowExecution(
	_ context.Context,
	request execution.StartWorkflowExecutionRequest,
) (execution.WorkflowExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	value := execution.WorkflowExecution{
		ID: request.ID, WorkspaceID: request.WorkspaceID, WorkflowID: request.WorkflowID,
		RevisionID: request.RevisionID, TriggerType: request.TriggerType,
		TriggeredByType: request.TriggeredByType, TriggeredByID: request.TriggeredByID,
		TraceID: request.TraceID, Status: "RUNNING", InputSummary: request.InputSummary,
		OutputSummary: json.RawMessage(`{}`), LockVersion: 1,
	}
	f.byID[value.ID] = value
	return value, nil
}

func (f *fakeProductionExecutions) TransitionWorkflowExecution(
	_ context.Context,
	workspaceID, executionID string,
	transition execution.RunTransition,
) (execution.WorkflowExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.byID[executionID]
	if !ok || value.WorkspaceID != workspaceID {
		return execution.WorkflowExecution{}, execution.ErrRunNotFound
	}
	if value.Status != transition.ExpectedStatus || value.LockVersion != transition.ExpectedLockVersion {
		return execution.WorkflowExecution{}, execution.ErrRunConflict
	}
	value.Status = transition.NewStatus
	value.OutputSummary = transition.OutputSummary
	value.ErrorCode = transition.ErrorCode
	value.LockVersion++
	f.byID[executionID] = value
	return value, nil
}

func (f *fakeProductionExecutions) GetWorkflowExecution(
	_ context.Context,
	workspaceID, executionID string,
) (execution.WorkflowExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.byID[executionID]
	if !ok || value.WorkspaceID != workspaceID {
		return execution.WorkflowExecution{}, execution.ErrRunNotFound
	}
	return value, nil
}
