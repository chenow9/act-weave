package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/chat"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

func TestInteractionProtocolEvents(t *testing.T) {
	t.Run("approved", func(t *testing.T) {
		fixture := newInteractionProtocolFixture(t, nil)
		prepared := fixture.prepareAndProject(t)
		confirmed, err := fixture.chatService.Confirm(context.Background(), chat.ConfirmChatConfirmationInput{
			WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: chatProjectionID,
			ActorID: chatConfirmationOwnerID, ResumeToken: prepared.Prepared.Requested.ResumeToken,
			IdempotencyKey:               chatApproveIdempotencyKey,
			ExpectedExecutionLockVersion: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		core, err := fixture.confirmations.Get(context.Background(), chatConfirmationWorkspaceID, chatExecutionConfirmationID)
		if err != nil || core.ConfirmedAt == nil {
			t.Fatalf("confirmed core=%+v err=%v", core, err)
		}
		fixture.projectTerminal(t, confirmed.Confirmation, core, *core.ConfirmedAt)
		fixture.assertTrace(t, prepared.Prepared.Requested.ResumeToken,
			protocolevent.EventInteractionResolved, protocolevent.InteractionStatusApproved,
			protocolevent.ItemStatusCompleted,
		)
	})

	t.Run("cancelled", func(t *testing.T) {
		fixture := newInteractionProtocolFixture(t, nil)
		prepared := fixture.prepareAndProject(t)
		cancelled, err := fixture.chatService.Cancel(context.Background(), chat.CancelChatConfirmationInput{
			WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: chatProjectionID,
			ActorID: chatConfirmationOwnerID, IdempotencyKey: chatCancelIdempotencyKey,
			ExpectedExecutionLockVersion: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		core, err := fixture.confirmations.Get(context.Background(), chatConfirmationWorkspaceID, chatExecutionConfirmationID)
		if err != nil || core.CancelledAt == nil {
			t.Fatalf("cancelled core=%+v err=%v", core, err)
		}
		fixture.projectTerminal(t, cancelled.Confirmation, core, *core.CancelledAt)
		fixture.assertTrace(t, prepared.Prepared.Requested.ResumeToken,
			protocolevent.EventInteractionResolved, protocolevent.InteractionStatusCancelled,
			protocolevent.ItemStatusCancelled,
		)
	})

	t.Run("expired", func(t *testing.T) {
		past := time.Now().UTC().Add(-2 * time.Hour)
		fixture := newInteractionProtocolFixture(t, func() time.Time { return past })
		prepared := fixture.prepareAndProject(t)
		expired, err := fixture.resumes.ExpireDue(context.Background(), 10)
		if err != nil || len(expired) != 1 {
			t.Fatalf("expire confirmations=%+v err=%v", expired, err)
		}
		chatConfirmation, err := fixture.chatRepository.GetConfirmation(
			context.Background(), chatConfirmationWorkspaceID, chatProjectionID,
		)
		if err != nil {
			t.Fatal(err)
		}
		core, err := fixture.confirmations.Get(context.Background(), chatConfirmationWorkspaceID, chatExecutionConfirmationID)
		if err != nil || core.Status != execution.ConfirmationStatusExpired {
			t.Fatalf("expired core=%+v err=%v", core, err)
		}
		fixture.projectTerminal(t, chatConfirmation, core, time.Now().UTC())
		fixture.assertTrace(t, prepared.Prepared.Requested.ResumeToken,
			protocolevent.EventInteractionExpired, protocolevent.InteractionStatusExpired,
			protocolevent.ItemStatusCancelled,
		)
	})
}

type interactionProtocolFixture struct {
	chatService    *chat.ConfirmationService
	chatRepository *chat.Repository
	confirmations  *execution.ConfirmationRepository
	resumes        *execution.ConfirmationResumeService
	projector      *execution.ProtocolInteractionProjector
	reader         *protocolevent.EventReader
	items          *protocolevent.RunItemRepository
	protocol       execution.ProtocolInteractionContext
	targetItemID   string
}

func newInteractionProtocolFixture(
	t *testing.T,
	clock func() time.Time,
) interactionProtocolFixture {
	t.Helper()
	chatService, chatRepository, confirmations, resumes, db, _ := newChatConfirmationFixtureWithClock(t, clock)
	unit, err := protocolevent.NewProtocolUnitOfWork(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	projector, err := execution.NewProtocolInteractionProjector(unit, execution.NewInteractionProtocolMapper())
	if err != nil {
		t.Fatal(err)
	}
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	items, err := protocolevent.NewRunItemRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	protocolContext := execution.ProtocolInteractionContext{
		Scope: protocolevent.RunScope{
			WorkspaceID: chatConfirmationWorkspaceID, AgentID: chatConfirmationAgentID,
			ConversationID: chatConfirmationSessionID, RunID: chatConfirmationRunID,
		},
		EventStreamID: chatConfirmationRunID, TraceID: "trace-chat-confirm",
	}
	startedAt := time.Now().UTC()
	if clock != nil {
		startedAt = clock().UTC().Add(-time.Minute)
	}
	createInteractionTargetItem(t, unit, protocolContext, chatConfirmationStepID, startedAt)
	return interactionProtocolFixture{
		chatService: chatService, chatRepository: chatRepository,
		confirmations: confirmations, resumes: resumes, projector: projector,
		reader: reader, items: items, protocol: protocolContext, targetItemID: chatConfirmationStepID,
	}
}

func createInteractionTargetItem(
	t *testing.T,
	unit *protocolevent.ProtocolUnitOfWork,
	protocolContext execution.ProtocolInteractionContext,
	targetItemID string,
	startedAt time.Time,
) {
	t.Helper()
	item := protocolevent.ToolCallItem{
		ID: targetItemID, Type: protocolevent.ItemTypeToolCall,
		Status: protocolevent.ItemStatusWaiting, Name: "cancel_order",
		Arguments: json.RawMessage(`{"orderId":"A-10293"}`),
	}
	event, err := protocolevent.BuildProtocolEvent(protocolevent.NewProtocolEvent{
		ID: uuid.NewString(), EventStreamID: protocolContext.EventStreamID,
		WorkspaceID: protocolContext.Scope.WorkspaceID, AgentID: protocolContext.Scope.AgentID,
		ConversationID: protocolContext.Scope.ConversationID, RunID: protocolContext.Scope.RunID,
		Type: protocolevent.EventItemStarted, SpecVersion: "1.0", TraceID: protocolContext.TraceID,
		ItemID: targetItemID, OccurredAt: startedAt,
	}, protocolevent.ItemSnapshotData{Item: item})
	if err != nil {
		t.Fatal(err)
	}
	_, err = unit.Execute(context.Background(), func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
		if _, err := transaction.EnsureRunEventStream(ctx, protocolContext.EventStreamID, protocolContext.Scope); err != nil {
			return err
		}
		if _, err := transaction.CreateRunItem(ctx, protocolevent.CreateRunItemInput{
			WorkspaceID: protocolContext.Scope.WorkspaceID, AgentID: protocolContext.Scope.AgentID,
			RunID: protocolContext.Scope.RunID, Ordinal: 1,
			SourceType: protocolevent.SourceRuntime, SourceID: targetItemID,
			Item: item, StartedAt: startedAt,
		}); err != nil {
			return err
		}
		_, err := transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (fixture interactionProtocolFixture) prepareAndProject(
	t *testing.T,
) chat.PreparedChatConfirmation {
	t.Helper()
	ctx := context.Background()
	input := prepareChatConfirmationInput(t)
	planHash := strings.Repeat("c", 64)
	input.Resume.Confirmation.PlanHash = planHash
	var requestSnapshot map[string]any
	if err := json.Unmarshal(input.Resume.RequestSnapshot, &requestSnapshot); err != nil {
		t.Fatal(err)
	}
	requestSnapshot["planHash"] = planHash
	input.Resume.RequestSnapshot, _ = json.Marshal(requestSnapshot)
	prepared, err := fixture.chatService.Prepare(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(prepared.Prepared.Requested)
	if err != nil || strings.Contains(string(serialized), prepared.Prepared.Requested.ResumeToken) {
		t.Fatalf("requested confirmation serialized token: %s err=%v", serialized, err)
	}
	core := prepared.Prepared.Requested.Confirmation
	presentation, err := chat.MapInteractionPresentation(prepared.Confirmation, core, fixture.targetItemID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := fixture.reader.HighWatermark(ctx, fixture.protocol.Scope)
	if err != nil {
		t.Fatal(err)
	}
	tampered := presentation
	tampered.InputSummary = json.RawMessage(`{"authorization":"Bearer abcdefghijklmnop"}`)
	if _, err := fixture.projector.ProjectRequested(ctx, execution.ProjectInteractionRequestedInput{
		Context: fixture.protocol, Confirmation: core, Presentation: tampered, Ordinal: 2,
	}); !errors.Is(err, execution.ErrConfirmationInvalid) {
		t.Fatalf("sensitive interaction request error=%v", err)
	}
	missingTarget := presentation
	missingTarget.TargetItemID = uuid.NewString()
	if _, err := fixture.projector.ProjectRequested(ctx, execution.ProjectInteractionRequestedInput{
		Context: fixture.protocol, Confirmation: core, Presentation: missingTarget, Ordinal: 2,
	}); !errors.Is(err, execution.ErrConfirmationInvalid) {
		t.Fatalf("missing interaction target error=%v", err)
	}
	after, err := fixture.reader.HighWatermark(ctx, fixture.protocol.Scope)
	if err != nil || after != before {
		t.Fatalf("rejected interaction changed sequence before=%d after=%d err=%v", before, after, err)
	}
	result, err := fixture.projector.ProjectRequested(ctx, execution.ProjectInteractionRequestedInput{
		Context: fixture.protocol, Confirmation: core, Presentation: presentation, Ordinal: 2,
	})
	if err != nil || result.Projection.SourceType != protocolevent.SourceExecutionConfirmation ||
		result.Projection.SourceID != core.ID || result.Projection.Status != protocolevent.ItemStatusWaiting {
		t.Fatalf("requested interaction projection=%+v err=%v", result, err)
	}
	return prepared
}

func (fixture interactionProtocolFixture) projectTerminal(
	t *testing.T,
	chatConfirmation chat.ChatConfirmation,
	core execution.ExecutionConfirmation,
	occurredAt time.Time,
) {
	t.Helper()
	presentation, err := chat.MapInteractionPresentation(chatConfirmation, core, fixture.targetItemID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.projector.ProjectTerminal(context.Background(), execution.ProjectInteractionTerminalInput{
		Context: fixture.protocol, Confirmation: core,
		Presentation: presentation, OccurredAt: occurredAt,
	})
	if err != nil || len(result.Events) != 1 {
		t.Fatalf("terminal interaction projection=%+v err=%v", result, err)
	}
}

func (fixture interactionProtocolFixture) assertTrace(
	t *testing.T,
	resumeToken string,
	terminalEventType string,
	terminalStatus protocolevent.InteractionStatus,
	terminalItemStatus protocolevent.ItemStatus,
) {
	t.Helper()
	events, err := fixture.reader.ReadRunAfter(context.Background(), fixture.protocol.Scope, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	interactionEvents := make([]protocolevent.ProtocolEvent, 0, 2)
	for index, event := range events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("interaction trace sequence[%d]=%d", index, event.Sequence)
		}
		payload := strings.ToLower(string(event.Payload))
		for _, forbidden := range []string{
			strings.ToLower(resumeToken), "resumetoken", "resume_token", "scope_snapshot", "input_payload",
		} {
			if forbidden != "" && strings.Contains(payload, forbidden) {
				t.Fatalf("interaction payload leaked %q: %s", forbidden, event.Payload)
			}
		}
		if event.InteractionID == chatExecutionConfirmationID {
			interactionEvents = append(interactionEvents, event)
		}
	}
	if len(interactionEvents) != 2 || interactionEvents[0].Type != protocolevent.EventInteractionRequested ||
		interactionEvents[1].Type != terminalEventType {
		t.Fatalf("interaction events=%+v", interactionEvents)
	}
	for index, event := range interactionEvents {
		decoded, err := event.DecodeData()
		if err != nil {
			t.Fatal(err)
		}
		interaction := decoded.(protocolevent.InteractionData).Interaction
		expectedStatus := protocolevent.InteractionStatusPending
		expectedVersion := int64(1)
		if index == 1 {
			expectedStatus, expectedVersion = terminalStatus, 2
		}
		if interaction.ID != chatExecutionConfirmationID || interaction.Status != expectedStatus ||
			interaction.TargetItemID != fixture.targetItemID || interaction.RunID != chatConfirmationRunID ||
			interaction.ReleaseID != chatConfirmationReleaseID ||
			interaction.ConnectionID != chatConfirmationConnectionID ||
			interaction.InputHash == "" || interaction.PlanHash != strings.Repeat("c", 64) ||
			interaction.Version != expectedVersion ||
			interaction.RequiredDecider != protocolevent.RequiredDeciderActWeaveUser ||
			len(interaction.AllowedDecisions) != 2 {
			t.Fatalf("interaction[%d]=%+v", index, interaction)
		}
	}
	projection, err := fixture.items.Get(
		context.Background(), chatConfirmationWorkspaceID, chatConfirmationAgentID,
		chatConfirmationRunID, chatExecutionConfirmationID,
	)
	if err != nil || projection.SourceType != protocolevent.SourceExecutionConfirmation ||
		projection.SourceID != chatExecutionConfirmationID || projection.Status != terminalItemStatus {
		t.Fatalf("terminal interaction item=%+v err=%v", projection, err)
	}
}
