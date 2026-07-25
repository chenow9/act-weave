package chat

import (
	"context"
	"strings"
	"time"

	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

type ProtocolMessageContentReader interface {
	ReadPermanentChat(context.Context, string, string, string) (string, error)
}

// ProtocolMessageMapper maps an already-persisted Chat Message to the public
// Text Content Part. StoredObject content is read only through the injected
// authorized reader and its length/hash are verified against chat_messages.
type ProtocolMessageMapper struct {
	content ProtocolMessageContentReader
}

func NewProtocolMessageMapper(content ProtocolMessageContentReader) *ProtocolMessageMapper {
	return &ProtocolMessageMapper{content: content}
}

func (mapper *ProtocolMessageMapper) MapCompleted(
	ctx context.Context,
	message Message,
	actorID string,
) (protocolevent.MessageItem, error) {
	if mapper == nil || !validProtocolMessageFact(message) {
		return protocolevent.MessageItem{}, ErrInvalid
	}
	role := protocolMessageRole(message.Role)
	status := completedProtocolMessageStatus(message.Role, message.Status)
	if role == protocolevent.MessageRoleUnknown || status == protocolevent.ItemStatusUnknown ||
		!terminalProtocolMessageStatus(status) {
		return protocolevent.MessageItem{}, ErrInvalid
	}
	content, err := mapper.resolveContent(ctx, message, actorID)
	if err != nil {
		return protocolevent.MessageItem{}, err
	}
	item := protocolevent.MessageItem{
		ID: message.ID, Type: protocolevent.ItemTypeMessage, Status: status, Role: role,
		Content: []protocolevent.ContentPart{
			protocolevent.TextContentPart{Type: protocolevent.ContentPartTypeText, Text: content},
		},
	}
	if err := protocolevent.ValidateItem(item); err != nil {
		return protocolevent.MessageItem{}, ErrInvalid
	}
	return item, nil
}

func (mapper *ProtocolMessageMapper) MapStarted(
	messageID, role string,
) (protocolevent.MessageItem, error) {
	mappedRole := protocolMessageRole(role)
	if mapper == nil || !validUUID(strings.TrimSpace(messageID)) ||
		(mappedRole != protocolevent.MessageRoleAssistant && mappedRole != protocolevent.MessageRoleTool) {
		return protocolevent.MessageItem{}, ErrInvalid
	}
	item := protocolevent.MessageItem{
		ID: strings.TrimSpace(messageID), Type: protocolevent.ItemTypeMessage,
		Status: protocolevent.ItemStatusInProgress, Role: mappedRole,
		Content: []protocolevent.ContentPart{
			protocolevent.TextContentPart{Type: protocolevent.ContentPartTypeText, Text: ""},
		},
	}
	return item, nil
}

func (mapper *ProtocolMessageMapper) MapTextDelta(
	itemID string,
	index int,
	text string,
) (protocolevent.TextDelta, error) {
	delta := protocolevent.TextDelta{
		Type: protocolevent.DeltaTypeText, Index: index, Text: text,
	}
	if mapper == nil || !validUUID(strings.TrimSpace(itemID)) || text == "" ||
		protocolevent.ValidateDelta(delta) != nil {
		return protocolevent.TextDelta{}, ErrInvalid
	}
	return delta, nil
}

func (mapper *ProtocolMessageMapper) resolveContent(
	ctx context.Context,
	message Message,
	actorID string,
) (string, error) {
	content := message.Content
	if message.ContentObjectID != "" {
		if content != "" || mapper.content == nil || !validUUID(strings.TrimSpace(actorID)) {
			return "", ErrInvalid
		}
		loaded, err := mapper.content.ReadPermanentChat(
			ctx, message.WorkspaceID, message.ContentObjectID, strings.TrimSpace(actorID),
		)
		if err != nil {
			return "", err
		}
		content = loaded
	}
	if content == "" || int64(len([]byte(content))) != message.ContentLength ||
		contentHash(content) != strings.ToLower(strings.TrimSpace(message.ContentSHA256)) {
		return "", ErrInvalid
	}
	return content, nil
}

type ProtocolMessageContext struct {
	Scope         protocolevent.RunScope
	EventStreamID string
	TraceID       string
}

type ProjectCompletedMessageInput struct {
	Context ProtocolMessageContext
	Message Message
	ActorID string
	Ordinal int
}

type ProjectStartedMessageInput struct {
	Context   ProtocolMessageContext
	MessageID string
	Role      string
	Ordinal   int
	StartedAt time.Time
}

type ProjectMessageDeltaInput struct {
	Context    ProtocolMessageContext
	MessageID  string
	Index      int
	Text       string
	OccurredAt time.Time
}

type CompleteProjectedMessageInput struct {
	Context     ProtocolMessageContext
	Message     Message
	ActorID     string
	CompletedAt time.Time
}

type ProtocolMessageProjectionResult struct {
	Projection  protocolevent.RunItemProjection
	Events      []protocolevent.ProtocolEvent
	NotifyError error
}

type ProtocolMessageProjector struct {
	unit   *protocolevent.ProtocolUnitOfWork
	mapper *ProtocolMessageMapper
}

func NewProtocolMessageProjector(
	unit *protocolevent.ProtocolUnitOfWork,
	mapper *ProtocolMessageMapper,
) (*ProtocolMessageProjector, error) {
	if unit == nil || mapper == nil {
		return nil, ErrInvalid
	}
	return &ProtocolMessageProjector{unit: unit, mapper: mapper}, nil
}

func (projector *ProtocolMessageProjector) ProjectCompleted(
	ctx context.Context,
	input ProjectCompletedMessageInput,
) (ProtocolMessageProjectionResult, error) {
	if !validProtocolMessageProjector(projector) {
		return ProtocolMessageProjectionResult{}, ErrInvalid
	}
	item, err := projector.mapper.MapCompleted(ctx, input.Message, input.ActorID)
	if err != nil || input.Ordinal < 1 || validateProtocolMessageContext(input.Context, input.Message) != nil {
		return ProtocolMessageProjectionResult{}, ErrInvalid
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return ProtocolMessageProjectionResult{}, err
	}
	event, err := buildMessageSnapshotEvent(
		input.Context, eventID.String(), protocolevent.EventItemCompleted,
		item, input.Message.CreatedAt,
	)
	if err != nil {
		return ProtocolMessageProjectionResult{}, err
	}
	started := item
	started.Status = protocolevent.ItemStatusInProgress
	var projection protocolevent.RunItemProjection
	unitResult, err := projector.unit.Execute(ctx, func(
		ctx context.Context,
		transaction *protocolevent.ProtocolTransaction,
	) error {
		if _, err := transaction.EnsureRunEventStream(
			ctx, input.Context.EventStreamID, input.Context.Scope,
		); err != nil {
			return err
		}
		if _, err := transaction.CreateRunItem(ctx, protocolevent.CreateRunItemInput{
			WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
			RunID: input.Context.Scope.RunID, Ordinal: input.Ordinal,
			SourceType: protocolevent.SourceChatMessage, SourceID: input.Message.ID,
			Item: started, StartedAt: input.Message.CreatedAt,
		}); err != nil {
			return err
		}
		projection, err = transaction.CompleteRunItem(ctx, protocolevent.CompleteRunItemInput{
			WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
			RunID: input.Context.Scope.RunID, Item: item, CompletedAt: input.Message.CreatedAt,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		return ProtocolMessageProjectionResult{}, err
	}
	return protocolMessageProjectionResult(projection, unitResult), nil
}

func (projector *ProtocolMessageProjector) ProjectStarted(
	ctx context.Context,
	input ProjectStartedMessageInput,
) (ProtocolMessageProjectionResult, error) {
	if !validProtocolMessageProjector(projector) {
		return ProtocolMessageProjectionResult{}, ErrInvalid
	}
	item, err := projector.mapper.MapStarted(input.MessageID, input.Role)
	if err != nil || input.Ordinal < 1 || input.StartedAt.IsZero() ||
		validateProtocolMessageContextValues(input.Context, input.MessageID) != nil {
		return ProtocolMessageProjectionResult{}, ErrInvalid
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return ProtocolMessageProjectionResult{}, err
	}
	event, err := buildMessageSnapshotEvent(
		input.Context, eventID.String(), protocolevent.EventItemStarted, item, input.StartedAt,
	)
	if err != nil {
		return ProtocolMessageProjectionResult{}, err
	}
	var projection protocolevent.RunItemProjection
	unitResult, err := projector.unit.Execute(ctx, func(
		ctx context.Context,
		transaction *protocolevent.ProtocolTransaction,
	) error {
		if _, err := transaction.EnsureRunEventStream(
			ctx, input.Context.EventStreamID, input.Context.Scope,
		); err != nil {
			return err
		}
		projection, err = transaction.CreateRunItem(ctx, protocolevent.CreateRunItemInput{
			WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
			RunID: input.Context.Scope.RunID, Ordinal: input.Ordinal,
			SourceType: protocolevent.SourceChatMessage, SourceID: input.MessageID,
			Item: item, StartedAt: input.StartedAt,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		return ProtocolMessageProjectionResult{}, err
	}
	return protocolMessageProjectionResult(projection, unitResult), nil
}

func (projector *ProtocolMessageProjector) ProjectDelta(
	ctx context.Context,
	input ProjectMessageDeltaInput,
) (ProtocolMessageProjectionResult, error) {
	if !validProtocolMessageProjector(projector) {
		return ProtocolMessageProjectionResult{}, ErrInvalid
	}
	delta, err := projector.mapper.MapTextDelta(input.MessageID, input.Index, input.Text)
	if err != nil || input.OccurredAt.IsZero() ||
		validateProtocolMessageContextValues(input.Context, input.MessageID) != nil {
		return ProtocolMessageProjectionResult{}, ErrInvalid
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return ProtocolMessageProjectionResult{}, err
	}
	event, err := buildMessageDeltaEvent(input.Context, eventID.String(), input.MessageID, delta, input.OccurredAt)
	if err != nil {
		return ProtocolMessageProjectionResult{}, err
	}
	var projection protocolevent.RunItemProjection
	unitResult, err := projector.unit.Execute(ctx, func(
		ctx context.Context,
		transaction *protocolevent.ProtocolTransaction,
	) error {
		projection, err = transaction.ApplyRunItemDelta(ctx, protocolevent.ApplyItemDeltaInput{
			WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
			RunID: input.Context.Scope.RunID, ItemID: input.MessageID, Delta: delta,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		return ProtocolMessageProjectionResult{}, err
	}
	return protocolMessageProjectionResult(projection, unitResult), nil
}

func (projector *ProtocolMessageProjector) CompleteProjected(
	ctx context.Context,
	input CompleteProjectedMessageInput,
) (ProtocolMessageProjectionResult, error) {
	if !validProtocolMessageProjector(projector) {
		return ProtocolMessageProjectionResult{}, ErrInvalid
	}
	item, err := projector.mapper.MapCompleted(ctx, input.Message, input.ActorID)
	if err != nil || input.CompletedAt.IsZero() ||
		validateProtocolMessageContext(input.Context, input.Message) != nil {
		return ProtocolMessageProjectionResult{}, ErrInvalid
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return ProtocolMessageProjectionResult{}, err
	}
	event, err := buildMessageSnapshotEvent(
		input.Context, eventID.String(), protocolevent.EventItemCompleted, item, input.CompletedAt,
	)
	if err != nil {
		return ProtocolMessageProjectionResult{}, err
	}
	var projection protocolevent.RunItemProjection
	unitResult, err := projector.unit.Execute(ctx, func(
		ctx context.Context,
		transaction *protocolevent.ProtocolTransaction,
	) error {
		projection, err = transaction.CompleteRunItem(ctx, protocolevent.CompleteRunItemInput{
			WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
			RunID: input.Context.Scope.RunID, Item: item, CompletedAt: input.CompletedAt,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		return ProtocolMessageProjectionResult{}, err
	}
	return protocolMessageProjectionResult(projection, unitResult), nil
}

func buildMessageSnapshotEvent(
	context ProtocolMessageContext,
	eventID, eventType string,
	item protocolevent.MessageItem,
	occurredAt time.Time,
) (protocolevent.NewProtocolEvent, error) {
	return protocolevent.BuildProtocolEvent(protocolevent.NewProtocolEvent{
		ID: eventID, EventStreamID: context.EventStreamID,
		WorkspaceID: context.Scope.WorkspaceID, AgentID: context.Scope.AgentID,
		ConversationID: context.Scope.ConversationID, RunID: context.Scope.RunID,
		Type: eventType, SpecVersion: "1.0", TraceID: context.TraceID,
		ItemID: item.ID, OccurredAt: occurredAt,
	}, protocolevent.ItemSnapshotData{Item: item})
}

func buildMessageDeltaEvent(
	context ProtocolMessageContext,
	eventID, itemID string,
	delta protocolevent.TextDelta,
	occurredAt time.Time,
) (protocolevent.NewProtocolEvent, error) {
	return protocolevent.BuildProtocolEvent(protocolevent.NewProtocolEvent{
		ID: eventID, EventStreamID: context.EventStreamID,
		WorkspaceID: context.Scope.WorkspaceID, AgentID: context.Scope.AgentID,
		ConversationID: context.Scope.ConversationID, RunID: context.Scope.RunID,
		Type: protocolevent.EventItemDelta, SpecVersion: "1.0", TraceID: context.TraceID,
		ItemID: itemID, OccurredAt: occurredAt,
	}, protocolevent.ItemDeltaData{ItemID: itemID, Delta: delta})
}

func validateProtocolMessageContext(context ProtocolMessageContext, message Message) error {
	if message.WorkspaceID != context.Scope.WorkspaceID || message.SessionID != context.Scope.ConversationID ||
		message.RunID != context.Scope.RunID {
		return ErrInvalid
	}
	return validateProtocolMessageContextValues(context, message.ID)
}

func validateProtocolMessageContextValues(context ProtocolMessageContext, messageID string) error {
	if !validUUID(context.Scope.WorkspaceID) || !validUUID(context.Scope.AgentID) ||
		!validUUID(context.Scope.ConversationID) || !validUUID(context.Scope.RunID) ||
		!validUUID(strings.TrimSpace(context.EventStreamID)) || !validUUID(strings.TrimSpace(messageID)) ||
		strings.TrimSpace(context.TraceID) == "" {
		return ErrInvalid
	}
	return nil
}

func validProtocolMessageFact(message Message) bool {
	return validUUID(strings.TrimSpace(message.ID)) && validUUID(strings.TrimSpace(message.WorkspaceID)) &&
		validUUID(strings.TrimSpace(message.SessionID)) && validUUID(strings.TrimSpace(message.RunID)) &&
		message.ContentLength > 0 && len(strings.TrimSpace(message.ContentSHA256)) == 64 &&
		!message.CreatedAt.IsZero() && validProtocolChatMessageStatus(message.Status)
}

func validProtocolMessageProjector(projector *ProtocolMessageProjector) bool {
	return projector != nil && projector.unit != nil && projector.mapper != nil
}

func validProtocolChatMessageStatus(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "RECEIVED", "PROCESSING", "NEEDS_INPUT", "MATCHED_NONE",
		"PENDING_CONFIRMATION", "EXECUTED", "FAILED":
		return true
	default:
		return false
	}
}

func protocolMessageRole(value string) protocolevent.MessageRole {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "USER":
		return protocolevent.MessageRoleUser
	case "ASSISTANT":
		return protocolevent.MessageRoleAssistant
	case "TOOL":
		return protocolevent.MessageRoleTool
	case "SYSTEM":
		return protocolevent.MessageRoleSystem
	default:
		return protocolevent.MessageRoleUnknown
	}
}

func completedProtocolMessageStatus(role, status string) protocolevent.ItemStatus {
	if strings.EqualFold(strings.TrimSpace(role), "USER") {
		return protocolevent.ItemStatusCompleted
	}
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "EXECUTED", "MATCHED_NONE":
		return protocolevent.ItemStatusCompleted
	case "FAILED":
		return protocolevent.ItemStatusFailed
	default:
		return protocolevent.ItemStatusUnknown
	}
}

func terminalProtocolMessageStatus(status protocolevent.ItemStatus) bool {
	return status == protocolevent.ItemStatusCompleted || status == protocolevent.ItemStatusFailed ||
		status == protocolevent.ItemStatusDeclined || status == protocolevent.ItemStatusCancelled
}

func protocolMessageProjectionResult(
	projection protocolevent.RunItemProjection,
	result protocolevent.UnitOfWorkResult,
) ProtocolMessageProjectionResult {
	return ProtocolMessageProjectionResult{
		Projection: projection, Events: result.Events, NotifyError: result.NotifyError,
	}
}
