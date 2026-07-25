package chatruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"actweave/backend/internal/chat"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

// approvedResumeEventNamespace seeds deterministic run.resumed event IDs so
// M10-T6 crash recovery can re-enter RecordApprovedInteraction safely.
var approvedResumeEventNamespace = uuid.MustParse("b45f49b3-85fd-50b7-9d80-45c01d7d1a18")

// terminalRunEventNamespace seeds deterministic run.completed / run.failed
// event IDs keyed by (runId, eventType) so re-entry treats conflict as done
// (ZKL-56 UX-03 / checklist #2). Does not collide with resumed namespace.
var terminalRunEventNamespace = uuid.MustParse("c56a50c4-96e0-61c8-ae91-56d12e8e2b29")

// NativeProtocolRecorder is the production Runtime adapter for the AAP Event
// Kernel. It consumes persisted domain facts and delegates public mapping to
// the native protocol mappers/projectors; it never creates a legacy RunEvent.
type NativeProtocolRecorder struct {
	runs                 *execution.RunRepository
	tools                *execution.ToolInvocationRepository
	items                *protocolevent.RunItemRepository
	unit                 *protocolevent.ProtocolUnitOfWork
	lifecycle            *execution.ProtocolRunLifecycleService
	runMapper            *execution.RunLifecycleMapper
	toolProjector        *execution.ProtocolToolCallProjector
	interactionProjector *execution.ProtocolInteractionProjector
	messageProjector     *chat.ProtocolMessageProjector
}

func NewNativeProtocolRecorder(
	runs *execution.RunRepository,
	tools *execution.ToolInvocationRepository,
	items *protocolevent.RunItemRepository,
	unit *protocolevent.ProtocolUnitOfWork,
	lifecycle *execution.ProtocolRunLifecycleService,
	content chat.ProtocolMessageContentReader,
) (*NativeProtocolRecorder, error) {
	if runs == nil || tools == nil || items == nil || unit == nil || lifecycle == nil {
		return nil, errors.New("native runtime protocol recorder dependencies are required")
	}
	toolProjector, err := execution.NewProtocolToolCallProjector(
		unit, execution.NewToolCallProtocolMapper(),
	)
	if err != nil {
		return nil, err
	}
	interactionProjector, err := execution.NewProtocolInteractionProjector(
		unit, execution.NewInteractionProtocolMapper(),
	)
	if err != nil {
		return nil, err
	}
	messageProjector, err := chat.NewProtocolMessageProjector(
		unit, chat.NewProtocolMessageMapper(content),
	)
	if err != nil {
		return nil, err
	}
	return &NativeProtocolRecorder{
		runs: runs, tools: tools, items: items, unit: unit, lifecycle: lifecycle,
		runMapper: execution.NewRunLifecycleMapper(), toolProjector: toolProjector,
		interactionProjector: interactionProjector, messageProjector: messageProjector,
	}, nil
}

func (recorder *NativeProtocolRecorder) Record(ctx context.Context, record ProtocolRecord) error {
	if recorder == nil || ctx == nil || strings.TrimSpace(record.Job.WorkspaceID) == "" ||
		strings.TrimSpace(record.Job.RunID) == "" {
		return errors.New("native runtime protocol record is invalid")
	}
	if record.OccurredAt.IsZero() {
		record.OccurredAt = time.Now().UTC()
	}
	switch record.Kind {
	case ProtocolRecordRunStarted:
		_, err := recorder.lifecycle.RecordStartedAgentRun(ctx, record.Run)
		return err
	case ProtocolRecordRunCompleted, ProtocolRecordRunFailed:
		return recorder.recordTerminal(ctx, record)
	case ProtocolRecordToolCompleted:
		return recorder.recordToolCompleted(ctx, record)
	case ProtocolRecordWorkflowCompleted:
		return recorder.recordWorkflowCompleted(ctx, record)
	case ProtocolRecordInteractionRequested:
		return recorder.recordInteractionRequested(ctx, record)
	default:
		return errors.New("native runtime protocol record kind is invalid")
	}
}

func (recorder *NativeProtocolRecorder) recordTerminal(ctx context.Context, record ProtocolRecord) error {
	if record.Message == nil || record.ActorID == "" {
		return errors.New("terminal runtime protocol record is invalid")
	}
	msgCtx := chat.ProtocolMessageContext{
		Scope: runtimeProtocolScope(record.Run), EventStreamID: record.Run.ID,
		TraceID: record.Run.TraceID,
	}
	// Streaming path (D14): item.started + item.delta already opened a run_item
	// with Message.ID. Complete that same item so item.completed.id matches
	// item.delta.itemId (AAP text golden / SDK RunReducer). Non-stream path
	// still create+complete via ProjectCompleted.
	_, itemErr := recorder.items.Get(
		ctx, record.Run.WorkspaceID, record.Run.AgentID, record.Run.ID, record.Message.ID,
	)
	switch {
	case itemErr == nil:
		if _, err := recorder.messageProjector.CompleteProjected(ctx, chat.CompleteProjectedMessageInput{
			Context: msgCtx, Message: *record.Message, ActorID: record.ActorID,
			CompletedAt: record.Message.CreatedAt,
		}); err != nil {
			return err
		}
	case errors.Is(itemErr, protocolevent.ErrRunItemNotFound):
		ordinal, err := recorder.nextOrdinal(ctx, record.Run)
		if err != nil {
			return err
		}
		if _, err := recorder.messageProjector.ProjectCompleted(ctx, chat.ProjectCompletedMessageInput{
			Context: msgCtx, Message: *record.Message, ActorID: record.ActorID, Ordinal: ordinal,
		}); err != nil {
			return err
		}
	default:
		return itemErr
	}
	eventType := protocolevent.EventRunCompleted
	if record.Kind == ProtocolRecordRunFailed {
		eventType = protocolevent.EventRunFailed
	}
	// Deterministic terminal event ID: (runId, eventType). Re-entry / crash
	// recovery that races a prior successful append treats conflict as done.
	eventID := uuid.NewSHA1(
		terminalRunEventNamespace,
		[]byte(strings.TrimSpace(record.Run.ID)+"\x00"+eventType),
	).String()
	return recorder.appendRunSnapshotWithID(
		ctx, record.Run, eventType, record.OccurredAt, "", eventID,
	)
}

func (recorder *NativeProtocolRecorder) recordToolCompleted(ctx context.Context, record ProtocolRecord) error {
	invocation, err := recorder.tools.Get(ctx, record.Job.WorkspaceID, record.ToolInvocationID)
	if err != nil {
		return err
	}
	protocolContext := execution.ProtocolToolCallContext{
		Scope: runtimeProtocolScope(record.Run), EventStreamID: record.Run.ID,
		TraceID: record.Run.TraceID,
	}
	_, itemErr := recorder.items.Get(
		ctx, record.Run.WorkspaceID, record.Run.AgentID, record.Run.ID, invocation.ID,
	)
	if errors.Is(itemErr, protocolevent.ErrRunItemNotFound) {
		ordinal, ordinalErr := recorder.nextOrdinal(ctx, record.Run)
		if ordinalErr != nil {
			return ordinalErr
		}
		started := invocation
		started.Status, started.FinishedAt = "RUNNING", nil
		started.OutputSummary, started.ErrorCode = nil, ""
		if _, err := recorder.toolProjector.ProjectStarted(ctx, execution.ProjectToolCallStartedInput{
			Context: protocolContext, Invocation: started, Name: record.ToolName, Ordinal: ordinal,
		}); err != nil {
			return err
		}
		if _, err := recorder.toolProjector.ProjectArguments(ctx, execution.ProjectToolCallDeltaInput{
			Context: protocolContext, Invocation: started, OccurredAt: invocation.StartedAt,
		}); err != nil {
			return err
		}
	} else if itemErr != nil {
		return itemErr
	}
	completedAt := record.OccurredAt
	if invocation.FinishedAt != nil {
		completedAt = invocation.FinishedAt.UTC()
	}
	_, err = recorder.toolProjector.Complete(ctx, execution.CompleteProtocolToolCallInput{
		Context: protocolContext, Invocation: invocation, Name: record.ToolName,
		CompletedAt: completedAt,
	})
	return err
}

func (recorder *NativeProtocolRecorder) recordWorkflowCompleted(ctx context.Context, record ProtocolRecord) error {
	if record.WorkflowExecutionID == "" || record.WorkflowStepID == "" {
		return errors.New("workflow runtime protocol record is invalid")
	}
	workflow, err := recorder.runs.GetWorkflowExecution(
		ctx, record.Job.WorkspaceID, record.WorkflowExecutionID,
	)
	if err != nil {
		return err
	}
	ordinal, err := recorder.nextOrdinal(ctx, record.Run)
	if err != nil {
		return err
	}
	started := protocolevent.WorkflowStepItem{
		ID: record.WorkflowStepID, Type: protocolevent.ItemTypeWorkflowStep,
		Status: protocolevent.ItemStatusInProgress, NodeID: workflow.WorkflowID,
		NodeType: "WORKFLOW", WorkflowExecutionID: workflow.ID, StepSequence: 1,
	}
	completed := started
	completed.Status = protocolevent.ItemStatusCompleted
	completed.Result = append(json.RawMessage(nil), workflow.OutputSummary...)
	if protocolevent.ValidateItem(started) != nil || protocolevent.ValidateItem(completed) != nil {
		return errors.New("workflow runtime protocol item is invalid")
	}
	startedEvent, err := runtimeItemSnapshotEvent(record.Run, started, protocolevent.EventItemStarted, workflow.StartedAt)
	if err != nil {
		return err
	}
	completedAt := record.OccurredAt
	if workflow.FinishedAt != nil {
		completedAt = workflow.FinishedAt.UTC()
	}
	completedEvent, err := runtimeItemSnapshotEvent(record.Run, completed, protocolevent.EventItemCompleted, completedAt)
	if err != nil {
		return err
	}
	_, err = recorder.unit.Execute(ctx, func(ctx context.Context, tx *protocolevent.ProtocolTransaction) error {
		if _, err := tx.EnsureRunEventStream(ctx, record.Run.ID, runtimeProtocolScope(record.Run)); err != nil {
			return err
		}
		if _, err := tx.CreateRunItem(ctx, protocolevent.CreateRunItemInput{
			WorkspaceID: record.Run.WorkspaceID, AgentID: record.Run.AgentID, RunID: record.Run.ID,
			Ordinal: ordinal, SourceType: protocolevent.SourceWorkflowExecution,
			SourceID: workflow.ID, Item: started, StartedAt: workflow.StartedAt,
		}); err != nil {
			return err
		}
		if _, err := tx.CompleteRunItem(ctx, protocolevent.CompleteRunItemInput{
			WorkspaceID: record.Run.WorkspaceID, AgentID: record.Run.AgentID, RunID: record.Run.ID,
			Item: completed, CompletedAt: completedAt,
		}); err != nil {
			return err
		}
		_, err := tx.Append(ctx, []protocolevent.NewProtocolEvent{startedEvent, completedEvent})
		return err
	})
	return err
}

func (recorder *NativeProtocolRecorder) recordInteractionRequested(ctx context.Context, record ProtocolRecord) error {
	if record.Confirmation == nil || record.Run.Status != "WAITING_CONFIRMATION" ||
		strings.TrimSpace(record.TargetName) == "" {
		return errors.New("interaction runtime protocol record is invalid")
	}
	core := record.Confirmation.Prepared.Requested.Confirmation
	presentation, err := chat.MapInteractionPresentation(
		record.Confirmation.Confirmation, core, core.TargetItemID,
	)
	if err != nil {
		return err
	}
	arguments := append(json.RawMessage(nil), record.TargetArguments...)
	var object map[string]any
	if json.Unmarshal(arguments, &object) != nil || object == nil {
		arguments = json.RawMessage(`{"redacted":true}`)
	}
	target := protocolevent.ToolCallItem{
		ID: core.TargetItemID, Type: protocolevent.ItemTypeToolCall,
		Status: protocolevent.ItemStatusWaiting, Name: record.TargetName, Arguments: arguments,
	}
	if data, marshalErr := json.Marshal(protocolevent.ItemSnapshotData{Item: target}); marshalErr != nil || protocolevent.MustDefaultPayloadValidator().ValidateEventData(
		protocolevent.EventItemStarted, data,
	) != nil {
		target.Arguments = json.RawMessage(`{"redacted":true}`)
	}
	ordinal, err := recorder.nextOrdinal(ctx, record.Run)
	if err != nil {
		return err
	}
	_, err = recorder.interactionProjector.ProjectRequestedWithTarget(
		ctx, execution.ProjectInteractionRequestedWithTargetInput{
			Context: execution.ProtocolInteractionContext{
				Scope: runtimeProtocolScope(record.Run), EventStreamID: record.Run.ID,
				TraceID: record.Run.TraceID,
			},
			Confirmation: core, Presentation: presentation, Run: record.Run, Target: target,
			TargetOrdinal: ordinal, InteractionOrdinal: ordinal + 1,
		},
	)
	return err
}

// RecordApprovedInteraction publishes the post-decision resume facts in the
// same order clients reduce them, then returns control to the chat continuation.
// run.resumed uses a deterministic event ID so crash recovery is idempotent.
func (recorder *NativeProtocolRecorder) RecordApprovedInteraction(
	ctx context.Context,
	run execution.AgentRun,
	interactionID, invocationID, toolName string,
) error {
	if run.Status != "RUNNING" || interactionID == "" || invocationID == "" || toolName == "" {
		return errors.New("approved interaction protocol record is invalid")
	}
	if err := recorder.ensureRunResumed(ctx, run, interactionID); err != nil {
		return err
	}
	return recorder.recordToolCompleted(ctx, ProtocolRecord{
		Kind: ProtocolRecordToolCompleted,
		Job:  Job{WorkspaceID: run.WorkspaceID, SessionID: run.SessionID, RunID: run.ID},
		Run:  run, ToolInvocationID: invocationID, ToolName: toolName,
		OccurredAt: time.Now().UTC(),
	})
}

// ensureRunResumed appends run.resumed with a deterministic event ID. Recovery
// re-entry that races with a prior successful write treats event-id conflict as
// already recovered (committed facts are never re-written).
func (recorder *NativeProtocolRecorder) ensureRunResumed(
	ctx context.Context,
	run execution.AgentRun,
	interactionID string,
) error {
	eventID := uuid.NewSHA1(
		approvedResumeEventNamespace,
		[]byte("resumed\x00"+run.ID+"\x00"+interactionID),
	).String()
	return recorder.appendRunSnapshotWithID(
		ctx, run, protocolevent.EventRunResumed, time.Now().UTC(), interactionID, eventID,
	)
}

func (recorder *NativeProtocolRecorder) appendRunSnapshot(
	ctx context.Context,
	run execution.AgentRun,
	eventType string,
	occurredAt time.Time,
	interactionID string,
) error {
	eventID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	return recorder.appendRunSnapshotWithID(ctx, run, eventType, occurredAt, interactionID, eventID.String())
}

func (recorder *NativeProtocolRecorder) appendRunSnapshotWithID(
	ctx context.Context,
	run execution.AgentRun,
	eventType string,
	occurredAt time.Time,
	interactionID, eventID string,
) error {
	event, err := recorder.runMapper.Map(execution.RunLifecycleEventInput{
		EventID: eventID, EventStreamID: run.ID, EventType: eventType,
		Run: run, OccurredAt: occurredAt.UTC(), InteractionID: interactionID,
	})
	if err != nil {
		return err
	}
	_, err = recorder.unit.Execute(ctx, func(ctx context.Context, tx *protocolevent.ProtocolTransaction) error {
		if _, err := tx.EnsureRunEventStream(ctx, run.ID, runtimeProtocolScope(run)); err != nil {
			return err
		}
		_, err := tx.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	// Idempotent recovery: a concurrent recovery may have inserted the same
	// deterministic run.resumed / run.completed / run.failed id first —
	// treat conflict as already recovered (do not create a second terminal).
	if err != nil && errors.Is(err, protocolevent.ErrEventConflict) {
		switch eventType {
		case protocolevent.EventRunResumed,
			protocolevent.EventRunCompleted,
			protocolevent.EventRunFailed:
			return nil
		}
	}
	return err
}

func (recorder *NativeProtocolRecorder) nextOrdinal(ctx context.Context, run execution.AgentRun) (int, error) {
	items, err := recorder.items.ListForRun(ctx, run.WorkspaceID, run.AgentID, run.ID)
	if err != nil {
		return 0, err
	}
	ordinal := 1
	for _, item := range items {
		if item.Ordinal >= ordinal {
			ordinal = item.Ordinal + 1
		}
	}
	return ordinal, nil
}

func runtimeProtocolScope(run execution.AgentRun) protocolevent.RunScope {
	return protocolevent.RunScope{
		WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
		ConversationID: run.SessionID, RunID: run.ID,
	}
}

func runtimeItemSnapshotEvent(
	run execution.AgentRun,
	item protocolevent.Item,
	eventType string,
	occurredAt time.Time,
) (protocolevent.NewProtocolEvent, error) {
	eventID, err := uuid.NewV7()
	if err != nil {
		return protocolevent.NewProtocolEvent{}, err
	}
	return protocolevent.BuildProtocolEvent(protocolevent.NewProtocolEvent{
		ID: eventID.String(), EventStreamID: run.ID, WorkspaceID: run.WorkspaceID,
		AgentID: run.AgentID, ConversationID: run.SessionID, RunID: run.ID,
		Type: eventType, SpecVersion: "1.0", TraceID: run.TraceID,
		OccurredAt: occurredAt.UTC(), ItemID: item.ItemID(),
	}, protocolevent.ItemSnapshotData{Item: item})
}
