package execution_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

func TestWorkflowProtocolItems(t *testing.T) {
	ctx := context.Background()
	runs, runService, db, _ := newRunStateTest(t)
	db.SetMaxOpenConns(16)
	unit, err := protocolevent.NewProtocolUnitOfWork(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := execution.NewProtocolRunLifecycleService(runs, unit)
	if err != nil {
		t.Fatal(err)
	}
	run := startProtocolLifecycleRun(t, ctx, runService, lifecycle, "workflow-items").Run
	workflow, err := runService.StartWorkflowExecution(ctx, execution.StartWorkflowExecutionRequest{
		ID: uuid.NewString(), WorkspaceID: run.WorkspaceID,
		WorkflowID: executionWorkflowID, RevisionID: executionRevisionID, AgentRunID: run.ID,
		TriggerType: "AGENT", TriggeredByType: "USER", TriggeredByID: executionOwnerID,
		TraceID: run.TraceID, InputSummary: json.RawMessage(`{"request":"check order"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	protocolContext := execution.ProtocolToolCallContext{
		Scope: protocolevent.RunScope{
			WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
			ConversationID: run.SessionID, RunID: run.ID,
		},
		EventStreamID: run.ID, TraceID: run.TraceID,
	}
	tools, err := execution.NewToolInvocationRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	workflowProjector, err := execution.NewProtocolWorkflowStepProjector(
		unit, tools, execution.NewWorkflowStepProtocolMapper(),
	)
	if err != nil {
		t.Fatal(err)
	}
	toolProjector, err := execution.NewProtocolToolCallProjector(unit, execution.NewToolCallProtocolMapper())
	if err != nil {
		t.Fatal(err)
	}
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}

	steps := appendParallelWorkflowSteps(t, ctx, runs, workflow)
	projectParallelWorkflowStarts(t, ctx, workflowProjector, protocolContext, workflow, steps)

	waitingStep := steps[0]
	waitingStep, err = runs.TransitionExecutionStep(ctx, run.WorkspaceID, waitingStep.ID, execution.StepTransition{
		ExpectedStatus: "RUNNING", NewStatus: "WAITING_CONFIRMATION",
		OutputSummary: json.RawMessage(`{"reason":"manual review"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitingResult, err := workflowProjector.ProjectWaiting(ctx, execution.ProjectWorkflowStepWaitingInput{
		Context: protocolContext, Execution: workflow, Step: waitingStep, OccurredAt: time.Now().UTC(),
	})
	if err != nil || len(waitingResult.Events) != 1 {
		t.Fatalf("workflow waiting=%+v err=%v", waitingResult, err)
	}
	waitingStep, err = runs.TransitionExecutionStep(ctx, run.WorkspaceID, waitingStep.ID, execution.StepTransition{
		ExpectedStatus: "WAITING_CONFIRMATION", NewStatus: "RUNNING",
		OutputSummary: json.RawMessage(`{"decision":"approved"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	steps[0] = waitingStep

	toolStep := steps[1]
	toolInvocation := startWorkflowToolInvocation(t, ctx, tools, run, workflow, toolStep)
	if _, err := toolProjector.ProjectStarted(ctx, execution.ProjectToolCallStartedInput{
		Context: protocolContext, Invocation: toolInvocation, Name: "order.lookup", Ordinal: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := toolProjector.ProjectArguments(ctx, execution.ProjectToolCallDeltaInput{
		Context: protocolContext, Invocation: toolInvocation, OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	toolFinishedAt := time.Now().UTC()
	toolInvocation, err = tools.Complete(ctx, run.WorkspaceID, toolInvocation.ID, execution.CompleteToolInvocationInput{
		OutputSummary: json.RawMessage(`{"status":"available"}`),
		RawObjectID:   runStateRawObjectID, FinishedAt: toolFinishedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := toolProjector.Complete(ctx, execution.CompleteProtocolToolCallInput{
		Context: protocolContext, Invocation: toolInvocation,
		Name: "order.lookup", CompletedAt: toolFinishedAt,
	}); err != nil {
		t.Fatal(err)
	}

	terminalSteps := completeParallelWorkflowSteps(t, ctx, runs, run, steps)
	projectParallelWorkflowCompletions(
		t, ctx, workflowProjector, protocolContext, workflow, terminalSteps,
	)
	assertWorkflowStepTrace(
		t, reader, protocolContext.Scope, workflow, terminalSteps, toolStep.ID, toolInvocation.ID,
	)

	assertFailedAndSkippedWorkflowItems(
		t, ctx, runs, workflowProjector, reader, protocolContext, workflow,
	)
	assertInternalWorkflowPlanRejected(
		t, ctx, runs, workflowProjector, reader, protocolContext, workflow,
	)
}

func appendParallelWorkflowSteps(
	t *testing.T,
	ctx context.Context,
	runs *execution.RunRepository,
	workflow execution.WorkflowExecution,
) []execution.ExecutionStep {
	t.Helper()
	type request struct{ nodeID, nodeType string }
	requests := []request{{"risk-check", "HTTP"}, {"inventory-sync", "TOOL"}}
	results := make(chan execution.ExecutionStep, len(requests))
	errorsChannel := make(chan error, len(requests))
	start := make(chan struct{})
	var wait sync.WaitGroup
	for _, request := range requests {
		request := request
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			step, err := runs.AppendExecutionStep(ctx, execution.AppendExecutionStepInput{
				ID: uuid.NewString(), WorkspaceID: workflow.WorkspaceID,
				ExecutionID: workflow.ID, NodeID: request.nodeID, NodeType: request.nodeType,
				InputSummary: json.RawMessage(`{"internalPlan":{"must":"never be public"}}`),
			})
			results <- step
			errorsChannel <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	steps := make([]execution.ExecutionStep, 0, len(requests))
	for step := range results {
		steps = append(steps, step)
	}
	sort.Slice(steps, func(left, right int) bool { return steps[left].SequenceNo < steps[right].SequenceNo })
	if len(steps) != 2 || steps[0].SequenceNo != 1 || steps[1].SequenceNo != 2 {
		t.Fatalf("parallel workflow step sequences=%+v", steps)
	}
	return steps
}

func projectParallelWorkflowStarts(
	t *testing.T,
	ctx context.Context,
	projector *execution.ProtocolWorkflowStepProjector,
	protocolContext execution.ProtocolToolCallContext,
	workflow execution.WorkflowExecution,
	steps []execution.ExecutionStep,
) {
	t.Helper()
	errorsChannel := make(chan error, len(steps))
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index, step := range steps {
		index, step := index, step
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, err := projector.ProjectStarted(ctx, execution.ProjectWorkflowStepStartedInput{
				Context: protocolContext, Execution: workflow, Step: step, Ordinal: index + 1,
			})
			if err == nil && (result.Projection.SourceType != protocolevent.SourceWorkflowStep ||
				result.Projection.SourceID != step.ID) {
				err = errors.New("workflow step source ref mismatch")
			}
			errorsChannel <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func startWorkflowToolInvocation(
	t *testing.T,
	ctx context.Context,
	repository *execution.ToolInvocationRepository,
	run execution.AgentRun,
	workflow execution.WorkflowExecution,
	step execution.ExecutionStep,
) execution.ToolInvocation {
	t.Helper()
	input := validToolInvocationStart(uuid.NewString(), "workflow-tool-"+step.ID)
	input.AgentRunID = run.ID
	input.WorkflowExecutionID = workflow.ID
	input.ExecutionStepID = step.ID
	input.TraceID = run.TraceID
	input.InputSummary = json.RawMessage(`{"orderId":"A-1"}`)
	result, err := repository.Start(ctx, input)
	if err != nil || !result.Created {
		t.Fatalf("start workflow tool invocation=%+v err=%v", result, err)
	}
	return result.Invocation
}

func completeParallelWorkflowSteps(
	t *testing.T,
	ctx context.Context,
	runs *execution.RunRepository,
	run execution.AgentRun,
	steps []execution.ExecutionStep,
) []execution.ExecutionStep {
	t.Helper()
	results := make([]execution.ExecutionStep, len(steps))
	for index, step := range steps {
		completed, err := runs.TransitionExecutionStep(ctx, run.WorkspaceID, step.ID, execution.StepTransition{
			ExpectedStatus: "RUNNING", NewStatus: "SUCCEEDED",
			OutputSummary: json.RawMessage(`{"result":"ok","public":true}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		results[index] = completed
	}
	return results
}

func projectParallelWorkflowCompletions(
	t *testing.T,
	ctx context.Context,
	projector *execution.ProtocolWorkflowStepProjector,
	protocolContext execution.ProtocolToolCallContext,
	workflow execution.WorkflowExecution,
	steps []execution.ExecutionStep,
) {
	t.Helper()
	errorsChannel := make(chan error, len(steps))
	start := make(chan struct{})
	var wait sync.WaitGroup
	for _, step := range steps {
		step := step
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := projector.Complete(ctx, execution.CompleteProtocolWorkflowStepInput{
				Context: protocolContext, Execution: workflow,
				Step: step, CompletedAt: *step.FinishedAt,
			})
			errorsChannel <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func assertWorkflowStepTrace(
	t *testing.T,
	reader *protocolevent.EventReader,
	scope protocolevent.RunScope,
	workflow execution.WorkflowExecution,
	steps []execution.ExecutionStep,
	toolStepID, toolItemID string,
) {
	t.Helper()
	events, err := reader.ReadRunAfter(context.Background(), scope, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	seenToolItem := false
	finalSteps := make(map[string]protocolevent.WorkflowStepItem)
	waitingSeen := false
	for index, event := range events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("run event[%d] sequence=%d", index, event.Sequence)
		}
		payload := strings.ToLower(string(event.Payload))
		for _, forbidden := range []string{"internalplan", "must never be public", "authorizationsnapshot"} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("internal workflow plan leaked: %s", event.Payload)
			}
		}
		seenToolItem = seenToolItem || event.ItemID == toolItemID
		decoded, decodeErr := event.DecodeData()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if event.Type == protocolevent.EventItemDelta && event.ItemID == steps[0].ID {
			delta := decoded.(protocolevent.ItemDeltaData).Delta
			if progress, ok := delta.(protocolevent.ProgressDelta); ok && progress.Message == "waiting_confirmation" {
				waitingSeen = true
			}
		}
		if event.Type != protocolevent.EventItemCompleted {
			continue
		}
		item, ok := decoded.(protocolevent.ItemSnapshotData).Item.(protocolevent.WorkflowStepItem)
		if ok {
			finalSteps[item.ID] = item
		}
	}
	if !seenToolItem || !waitingSeen || len(finalSteps) != len(steps) {
		t.Fatalf("workflow trace tool=%v waiting=%v finalSteps=%+v", seenToolItem, waitingSeen, finalSteps)
	}
	for _, step := range steps {
		item := finalSteps[step.ID]
		if item.WorkflowExecutionID != workflow.ID || item.StepSequence != step.SequenceNo ||
			item.Status != protocolevent.ItemStatusCompleted {
			t.Fatalf("workflow final item=%+v step=%+v", item, step)
		}
		if step.ID == toolStepID {
			if len(item.ToolCallItemIDs) != 1 || item.ToolCallItemIDs[0] != toolItemID {
				t.Fatalf("workflow tool relation=%+v", item)
			}
		} else if len(item.ToolCallItemIDs) != 0 {
			t.Fatalf("non-tool step has tool relation=%+v", item)
		}
	}
}

func assertFailedAndSkippedWorkflowItems(
	t *testing.T,
	ctx context.Context,
	runs *execution.RunRepository,
	projector *execution.ProtocolWorkflowStepProjector,
	reader *protocolevent.EventReader,
	protocolContext execution.ProtocolToolCallContext,
	workflow execution.WorkflowExecution,
) {
	t.Helper()
	type terminalCase struct {
		nodeID, status, errorCode, expectedStatus, outcome string
		ordinal                                            int
	}
	cases := []terminalCase{
		{nodeID: "failed-node", status: "FAILED", errorCode: "WORKFLOW_NODE_FAILED", expectedStatus: "failed", ordinal: 4},
		{nodeID: "skipped-node", status: "SKIPPED", expectedStatus: "completed", outcome: "skipped", ordinal: 5},
	}
	for _, testCase := range cases {
		step, err := runs.AppendExecutionStep(ctx, execution.AppendExecutionStepInput{
			ID: uuid.NewString(), WorkspaceID: workflow.WorkspaceID, ExecutionID: workflow.ID,
			NodeID: testCase.nodeID, NodeType: "TRANSFORM", InputSummary: json.RawMessage(`{"plan":"private"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := projector.ProjectStarted(ctx, execution.ProjectWorkflowStepStartedInput{
			Context: protocolContext, Execution: workflow, Step: step, Ordinal: testCase.ordinal,
		}); err != nil {
			t.Fatal(err)
		}
		step, err = runs.TransitionExecutionStep(ctx, workflow.WorkspaceID, step.ID, execution.StepTransition{
			ExpectedStatus: "RUNNING", NewStatus: testCase.status,
			OutputSummary: json.RawMessage(`{"compiledPlan":{"nodes":["private"]},"rawCause":"internal"}`),
			ErrorCode:     testCase.errorCode,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := projector.Complete(ctx, execution.CompleteProtocolWorkflowStepInput{
			Context: protocolContext, Execution: workflow, Step: step, CompletedAt: *step.FinishedAt,
		}); err != nil {
			t.Fatal(err)
		}
		events := readToolProtocolEvents(t, reader, protocolContext.Scope, step.ID)
		if len(events) != 2 {
			t.Fatalf("terminal workflow events=%+v", events)
		}
		decoded, err := events[1].DecodeData()
		if err != nil {
			t.Fatal(err)
		}
		item := decoded.(protocolevent.ItemSnapshotData).Item.(protocolevent.WorkflowStepItem)
		if string(item.Status) != testCase.expectedStatus ||
			strings.Contains(strings.ToLower(string(events[1].Payload)), "compiledplan") ||
			strings.Contains(strings.ToLower(string(events[1].Payload)), "rawcause") {
			t.Fatalf("terminal workflow item=%+v payload=%s", item, events[1].Payload)
		}
		if testCase.outcome != "" {
			var result map[string]any
			if err := json.Unmarshal(item.Result, &result); err != nil || result["outcome"] != testCase.outcome {
				t.Fatalf("skipped result=%s err=%v", item.Result, err)
			}
		}
	}
}

func assertInternalWorkflowPlanRejected(
	t *testing.T,
	ctx context.Context,
	runs *execution.RunRepository,
	projector *execution.ProtocolWorkflowStepProjector,
	reader *protocolevent.EventReader,
	protocolContext execution.ProtocolToolCallContext,
	workflow execution.WorkflowExecution,
) {
	t.Helper()
	step, err := runs.AppendExecutionStep(ctx, execution.AppendExecutionStepInput{
		ID: uuid.NewString(), WorkspaceID: workflow.WorkspaceID, ExecutionID: workflow.ID,
		NodeID: "plan-leak-probe", NodeType: "TRANSFORM", InputSummary: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projector.ProjectStarted(ctx, execution.ProjectWorkflowStepStartedInput{
		Context: protocolContext, Execution: workflow, Step: step, Ordinal: 6,
	}); err != nil {
		t.Fatal(err)
	}
	step, err = runs.TransitionExecutionStep(ctx, workflow.WorkspaceID, step.ID, execution.StepTransition{
		ExpectedStatus: "RUNNING", NewStatus: "SUCCEEDED",
		OutputSummary: json.RawMessage(`{"executionPlan":{"nodes":[{"id":"private"}]}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := reader.HighWatermark(ctx, protocolContext.Scope)
	if err != nil {
		t.Fatal(err)
	}
	_, err = projector.Complete(ctx, execution.CompleteProtocolWorkflowStepInput{
		Context: protocolContext, Execution: workflow, Step: step, CompletedAt: *step.FinishedAt,
	})
	if !errors.Is(err, execution.ErrRunInvalid) {
		t.Fatalf("internal plan completion error=%v", err)
	}
	after, err := reader.HighWatermark(ctx, protocolContext.Scope)
	if err != nil || after != before {
		t.Fatalf("internal plan rejection changed stream before=%d after=%d err=%v", before, after, err)
	}
}
