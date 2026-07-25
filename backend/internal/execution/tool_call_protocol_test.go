package execution_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

func TestToolCallProtocolItem(t *testing.T) {
	ctx := context.Background()
	runRepository, runService, db, _ := newRunStateTest(t)
	unit, err := protocolevent.NewProtocolUnitOfWork(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := execution.NewProtocolRunLifecycleService(runRepository, unit)
	if err != nil {
		t.Fatal(err)
	}
	run := startProtocolLifecycleRun(t, ctx, runService, lifecycle, "tool-call").Run
	protocolContext := execution.ProtocolToolCallContext{
		Scope: protocolevent.RunScope{
			WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
			ConversationID: run.SessionID, RunID: run.ID,
		},
		EventStreamID: run.ID, TraceID: run.TraceID,
	}
	mapper := execution.NewToolCallProtocolMapper()
	projector, err := execution.NewProtocolToolCallProjector(unit, mapper)
	if err != nil {
		t.Fatal(err)
	}
	invocations, err := execution.NewToolInvocationRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}

	largeUTF8 := strings.Repeat("新", 1500)
	arguments, err := json.Marshal(map[string]any{
		"orderId": "A-1", "note": largeUTF8,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := startProtocolToolInvocation(
		t, ctx, invocations, run, uuid.NewString(), "tool-call-success", arguments,
	)
	startResult, err := projector.ProjectStarted(ctx, execution.ProjectToolCallStartedInput{
		Context: protocolContext, Invocation: started, Name: "order.lookup", Ordinal: 1,
	})
	if err != nil || startResult.Projection.SourceType != protocolevent.SourceToolInvocation ||
		startResult.Projection.SourceID != started.ID {
		t.Fatalf("start tool projection=%+v err=%v", startResult, err)
	}
	startedItem, ok := startResult.Projection.Item.(protocolevent.ToolCallItem)
	if !ok || startedItem.Status != protocolevent.ItemStatusInProgress || len(startedItem.Arguments) != 0 {
		t.Fatalf("started item=%+v", startResult.Projection.Item)
	}

	argumentsResult, err := projector.ProjectArguments(ctx, execution.ProjectToolCallDeltaInput{
		Context: protocolContext, Invocation: started, OccurredAt: time.Now().UTC(),
	})
	if err != nil || len(argumentsResult.Events) < 2 {
		t.Fatalf("argument deltas=%+v err=%v", argumentsResult, err)
	}
	for _, event := range argumentsResult.Events {
		decoded, decodeErr := event.DecodeData()
		data, dataOK := decoded.(protocolevent.ItemDeltaData)
		delta, deltaOK := data.Delta.(protocolevent.ArgumentsJSONDelta)
		if decodeErr != nil || !dataOK || !deltaOK || delta.PartialJSON == "" {
			t.Fatalf("argument event=%s decoded=%+v err=%v", event.Data, decoded, decodeErr)
		}
	}

	assertToolProtocolSensitiveInputRejected(t, ctx, reader, projector, protocolContext, started)

	waiting, err := projector.ProjectWaiting(ctx, execution.ProjectToolCallDeltaInput{
		Context: protocolContext, Invocation: started, OccurredAt: time.Now().UTC(),
	})
	if err != nil || len(waiting.Events) != 1 {
		t.Fatalf("waiting projection=%+v err=%v", waiting, err)
	}
	total := float64(3)
	progress, err := projector.ProjectProgress(ctx, execution.ProjectToolCallProgressInput{
		Context: protocolContext, Invocation: started, Current: 1, Total: &total,
		Unit: "steps", Message: "calling provider", OccurredAt: time.Now().UTC(),
	})
	if err != nil || len(progress.Events) != 1 {
		t.Fatalf("progress projection=%+v err=%v", progress, err)
	}
	output, err := projector.ProjectOutput(ctx, execution.ProjectToolCallOutputInput{
		Context: protocolContext, Invocation: started, PublicText: "lookup complete",
		OccurredAt: time.Now().UTC(),
	})
	if err != nil || len(output.Events) != 1 {
		t.Fatalf("output projection=%+v err=%v", output, err)
	}

	finishedAt := time.Now().UTC()
	completed, err := invocations.Complete(ctx, run.WorkspaceID, started.ID, execution.CompleteToolInvocationInput{
		OutputSummary: json.RawMessage(`{"status":"shipped","attempt":1}`),
		RawObjectID:   runStateRawObjectID, FinishedAt: finishedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	completedResult, err := projector.Complete(ctx, execution.CompleteProtocolToolCallInput{
		Context: protocolContext, Invocation: completed, Name: "order.lookup", CompletedAt: finishedAt,
	})
	if err != nil || completedResult.Projection.Status != protocolevent.ItemStatusCompleted {
		t.Fatalf("completed projection=%+v err=%v", completedResult, err)
	}

	toolEvents := readToolProtocolEvents(t, reader, protocolContext.Scope, started.ID)
	assertSuccessfulToolProtocolTrace(t, toolEvents, arguments)
	loaded, err := protocolevent.NewRunItemRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := loaded.Get(ctx, run.WorkspaceID, run.AgentID, run.ID, started.ID)
	if err != nil || projection.SourceType != protocolevent.SourceToolInvocation ||
		projection.SourceID != started.ID || projection.Status != protocolevent.ItemStatusCompleted {
		t.Fatalf("persisted tool projection=%+v err=%v", projection, err)
	}

	assertFailedToolProtocolTrace(
		t, ctx, invocations, projector, reader, protocolContext, run,
	)
	assertRawToolFactsAreJSONOmitted(t)
}

func startProtocolToolInvocation(
	t *testing.T,
	ctx context.Context,
	repository *execution.ToolInvocationRepository,
	run execution.AgentRun,
	id, idempotencyKey string,
	inputSummary json.RawMessage,
) execution.ToolInvocation {
	t.Helper()
	input := validToolInvocationStart(id, idempotencyKey)
	input.AgentRunID = run.ID
	input.WorkflowExecutionID = ""
	input.ExecutionStepID = ""
	input.TraceID = run.TraceID
	input.InputSummary = inputSummary
	result, err := repository.Start(ctx, input)
	if err != nil || !result.Created {
		t.Fatalf("start protocol tool invocation=%+v err=%v", result, err)
	}
	return result.Invocation
}

func assertToolProtocolSensitiveInputRejected(
	t *testing.T,
	ctx context.Context,
	reader *protocolevent.EventReader,
	projector *execution.ProtocolToolCallProjector,
	protocolContext execution.ProtocolToolCallContext,
	invocation execution.ToolInvocation,
) {
	t.Helper()
	before, err := reader.HighWatermark(ctx, protocolContext.Scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"password":"must-not-persist"}`),
		json.RawMessage(`{"headers":{"Authorization":"Bearer abcdefghijklmnop"}}`),
	} {
		probe := invocation
		probe.InputSummary = raw
		_, err := projector.ProjectArguments(ctx, execution.ProjectToolCallDeltaInput{
			Context: protocolContext, Invocation: probe, OccurredAt: time.Now().UTC(),
		})
		if !errors.Is(err, execution.ErrToolInvocationInvalid) {
			t.Fatalf("sensitive argument error=%v", err)
		}
	}
	_, err = projector.ProjectOutput(ctx, execution.ProjectToolCallOutputInput{
		Context: protocolContext, Invocation: invocation,
		PublicText: `{"Authorization":"Bearer abcdefghijklmnop"}`,
		OccurredAt: time.Now().UTC(),
	})
	if !errors.Is(err, execution.ErrToolInvocationInvalid) {
		t.Fatalf("sensitive output error=%v", err)
	}
	after, err := reader.HighWatermark(ctx, protocolContext.Scope)
	if err != nil || after != before {
		t.Fatalf("rejected sensitive data changed stream before=%d after=%d err=%v", before, after, err)
	}
}

func assertSuccessfulToolProtocolTrace(
	t *testing.T,
	events []protocolevent.ProtocolEvent,
	expectedArguments json.RawMessage,
) {
	t.Helper()
	if len(events) < 7 || events[0].Type != protocolevent.EventItemStarted ||
		events[len(events)-1].Type != protocolevent.EventItemCompleted {
		t.Fatalf("tool event lifecycle=%+v", events)
	}
	var arguments strings.Builder
	waiting, progress, output := false, false, false
	for _, event := range events {
		lower := strings.ToLower(string(event.Payload))
		for _, forbidden := range []string{"authorization", "password", "bearer ", `"headers"`} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("sensitive value in protocol payload: %s", event.Payload)
			}
		}
		if event.Type != protocolevent.EventItemDelta {
			continue
		}
		decoded, err := event.DecodeData()
		if err != nil {
			t.Fatal(err)
		}
		data := decoded.(protocolevent.ItemDeltaData)
		switch delta := data.Delta.(type) {
		case protocolevent.ArgumentsJSONDelta:
			arguments.WriteString(delta.PartialJSON)
		case protocolevent.ProgressDelta:
			waiting = waiting || delta.Message == "waiting_confirmation"
			progress = progress || delta.Message == "calling provider"
		case protocolevent.OutputDelta:
			output = output || delta.Text == "lookup complete"
		}
	}
	finalData, err := events[len(events)-1].DecodeData()
	if err != nil {
		t.Fatal(err)
	}
	finalItem := finalData.(protocolevent.ItemSnapshotData).Item.(protocolevent.ToolCallItem)
	var expectedObject, actualObject any
	if err := json.Unmarshal(expectedArguments, &expectedObject); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(finalItem.Arguments, &actualObject); err != nil {
		t.Fatal(err)
	}
	canonicalExpected, _ := json.Marshal(expectedObject)
	canonicalActual, _ := json.Marshal(actualObject)
	if arguments.String() != string(finalItem.Arguments) ||
		string(canonicalExpected) != string(canonicalActual) || !waiting || !progress || !output ||
		finalItem.Status != protocolevent.ItemStatusCompleted {
		t.Fatalf("arguments=%q final=%s waiting=%v progress=%v output=%v item=%+v",
			arguments.String(), finalItem.Arguments, waiting, progress, output, finalItem)
	}
}

func assertFailedToolProtocolTrace(
	t *testing.T,
	ctx context.Context,
	repository *execution.ToolInvocationRepository,
	projector *execution.ProtocolToolCallProjector,
	reader *protocolevent.EventReader,
	protocolContext execution.ProtocolToolCallContext,
	run execution.AgentRun,
) {
	t.Helper()
	started := startProtocolToolInvocation(
		t, ctx, repository, run, uuid.NewString(), "tool-call-failed",
		json.RawMessage(`{"orderId":"A-2"}`),
	)
	if _, err := projector.ProjectStarted(ctx, execution.ProjectToolCallStartedInput{
		Context: protocolContext, Invocation: started, Name: "order.lookup", Ordinal: 2,
	}); err != nil {
		t.Fatal(err)
	}
	finishedAt := time.Now().UTC()
	failed, err := repository.Fail(ctx, run.WorkspaceID, started.ID, execution.FailToolInvocationInput{
		OutputSummary: json.RawMessage(`{"authorization":"Bearer abcdefghijklmnop","upstream":"raw cause"}`),
		RawObjectID:   runStateRawObjectID, ErrorCode: "UPSTREAM_UNAVAILABLE", FinishedAt: finishedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	invalidCode := failed
	invalidCode.ErrorCode = "bad code"
	if _, err := execution.NewToolCallProtocolMapper().MapCompleted(invalidCode, "order.lookup"); !errors.Is(err, execution.ErrToolInvocationInvalid) {
		t.Fatalf("unstable tool error code=%v", err)
	}
	result, err := projector.Complete(ctx, execution.CompleteProtocolToolCallInput{
		Context: protocolContext, Invocation: failed, Name: "order.lookup", CompletedAt: finishedAt,
	})
	if err != nil || result.Projection.Status != protocolevent.ItemStatusFailed {
		t.Fatalf("failed tool projection=%+v err=%v", result, err)
	}
	events := readToolProtocolEvents(t, reader, protocolContext.Scope, started.ID)
	if len(events) != 2 {
		t.Fatalf("failed tool events=%+v", events)
	}
	decoded, err := events[1].DecodeData()
	if err != nil {
		t.Fatal(err)
	}
	item := decoded.(protocolevent.ItemSnapshotData).Item.(protocolevent.ToolCallItem)
	var output struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(item.Output, &output); err != nil {
		t.Fatal(err)
	}
	if item.Status != protocolevent.ItemStatusFailed || output.Error.Code != "UPSTREAM_UNAVAILABLE" ||
		output.Error.Message != "Tool execution failed" || output.Error.Retryable ||
		strings.Contains(strings.ToLower(string(events[1].Payload)), "authorization") ||
		strings.Contains(strings.ToLower(string(events[1].Payload)), "raw cause") {
		t.Fatalf("public failed tool item=%+v payload=%s", item, events[1].Payload)
	}
}

func readToolProtocolEvents(
	t *testing.T,
	reader *protocolevent.EventReader,
	scope protocolevent.RunScope,
	itemID string,
) []protocolevent.ProtocolEvent {
	t.Helper()
	events, err := reader.ReadRunAfter(context.Background(), scope, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]protocolevent.ProtocolEvent, 0)
	for _, event := range events {
		if event.ItemID == itemID {
			result = append(result, event)
		}
	}
	return result
}

func assertRawToolFactsAreJSONOmitted(t *testing.T) {
	t.Helper()
	record, err := json.Marshal(execution.InvocationRecord{
		InvocationID: uuid.NewString(),
		Input:        json.RawMessage(`{"password":"raw-input"}`),
		Output:       json.RawMessage(`{"authorization":"raw-output"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := json.Marshal(execution.ConnectionSnapshot{
		ID: uuid.NewString(), Headers: map[string]string{"Authorization": "Bearer raw-header"},
		SensitiveHeaderNames: []string{"Authorization"},
	})
	if err != nil {
		t.Fatal(err)
	}
	combined := strings.ToLower(string(record) + string(connection))
	for _, forbidden := range []string{"raw-input", "raw-output", "raw-header", "authorization"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("raw tool fact serialized forbidden value %q: %s", forbidden, combined)
		}
	}
}
