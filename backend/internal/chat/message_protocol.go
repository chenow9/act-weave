package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/a2ui"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

// MessageContentSchemaVersion is the durable AAP createRun message body schema (design §5.7.2).
const MessageContentSchemaVersion = "aap.message-content.v1"

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
	parts, err := ParseMessageContentParts(content)
	if err != nil {
		return protocolevent.MessageItem{}, ErrInvalid
	}
	item := protocolevent.MessageItem{
		ID: message.ID, Type: protocolevent.ItemTypeMessage, Status: status, Role: role,
		Content: parts,
	}
	if err := protocolevent.ValidateItem(item); err != nil {
		return protocolevent.MessageItem{}, ErrInvalid
	}
	return item, nil
}

// ParseMessageContentParts projects permanent message body to protocol content parts.
// aap.message-content.v1 → text + input_file + output_file + optional a2ui (never download URLs).
// a2ui is accepted on durable rehydrate for assistant multi-part (KD-6 / PR-5);
// inbound createRun / user multimodal paths continue to reject a2ui and output_file separately.
// Legacy non-JSON / missing schemaVersion → single text part (Console/history compat).
func ParseMessageContentParts(content string) ([]protocolevent.ContentPart, error) {
	if content == "" {
		return nil, ErrInvalid
	}
	var envelope struct {
		SchemaVersion string            `json:"schemaVersion"`
		Parts         []json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal([]byte(content), &envelope); err != nil ||
		envelope.SchemaVersion != MessageContentSchemaVersion || len(envelope.Parts) == 0 {
		return []protocolevent.ContentPart{
			protocolevent.TextContentPart{Type: protocolevent.ContentPartTypeText, Text: content},
		}, nil
	}
	parts := make([]protocolevent.ContentPart, 0, len(envelope.Parts))
	for _, raw := range envelope.Parts {
		part, err := protocolevent.DecodeContentPart(raw)
		if err != nil {
			return nil, ErrInvalid
		}
		switch typed := part.(type) {
		case protocolevent.TextContentPart:
			parts = append(parts, typed)
		case protocolevent.InputFileContentPart:
			// Strip any accidental URL-bearing fields by re-materializing allowlisted keys only.
			parts = append(parts, protocolevent.InputFileContentPart{
				Type:   protocolevent.ContentPartTypeInputFile,
				FileID: typed.FileID, MediaType: typed.MediaType,
			})
		case protocolevent.A2UIContentPart:
			// Allowlisted keys only; surface is opaque JSON object (size-checked by Decode).
			surface := append(json.RawMessage(nil), typed.Surface...)
			parts = append(parts, protocolevent.A2UIContentPart{
				Type:      protocolevent.ContentPartTypeA2UI,
				Version:   typed.Version,
				Surface:   surface,
				CatalogID: typed.CatalogID,
			})
		case protocolevent.OutputFileContentPart:
			cleaned, err := protocolevent.AllowlistedOutputFile(typed)
			if err != nil {
				return nil, ErrInvalid
			}
			parts = append(parts, cleaned)
		default:
			return nil, ErrInvalid
		}
	}
	if len(parts) == 0 {
		return nil, ErrInvalid
	}
	return parts, nil
}

// JoinTextPartsFromDurable projects durable chat content to natural-language text only.
//
//  1. Non-v1 / plain / legacy bodies → returned unchanged.
//  2. aap.message-content.v1 → concatenate type=="text" parts only; ignore a2ui,
//     input_file, output_file, and unknown parts (KD-10 / KD-13 / §3.4).
//  3. v1 with no text parts → "" (callers skip empty history rows).
//
// Used by Console messageDTO text-first history and (later) model history reload.
func JoinTextPartsFromDurable(content string) string {
	if content == "" {
		return ""
	}
	var envelope struct {
		SchemaVersion string            `json:"schemaVersion"`
		Parts         []json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal([]byte(content), &envelope); err != nil ||
		envelope.SchemaVersion != MessageContentSchemaVersion || len(envelope.Parts) == 0 {
		return content
	}
	var builder strings.Builder
	for _, raw := range envelope.Parts {
		var wire struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			continue
		}
		if wire.Type != "text" {
			continue
		}
		builder.WriteString(wire.Text)
	}
	return builder.String()
}

// A2UISurfacesFromDurable projects durable chat content to the A2UI surfaces a
// display client may render, in part order.
//
// This is the read counterpart of JoinTextPartsFromDurable: text goes to the
// message body, surfaces go to their own channel, and neither can reach the
// other. Only parts declaring wantVersion are returned, so a row written by an
// older surface version stays invisible instead of reaching a renderer built
// for a contract it does not satisfy.
//
// The result is the surface object alone. The envelope around it is a storage
// detail and never leaves the server.
func A2UISurfacesFromDurable(content, wantVersion string) []json.RawMessage {
	if content == "" || wantVersion == "" {
		return nil
	}
	var envelope struct {
		SchemaVersion string            `json:"schemaVersion"`
		Parts         []json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal([]byte(content), &envelope); err != nil ||
		envelope.SchemaVersion != MessageContentSchemaVersion || len(envelope.Parts) == 0 {
		return nil
	}
	var surfaces []json.RawMessage
	for _, raw := range envelope.Parts {
		var wire struct {
			Type    string          `json:"type"`
			Version string          `json:"version"`
			Surface json.RawMessage `json:"surface"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			continue
		}
		if wire.Type != string(protocolevent.ContentPartTypeA2UI) ||
			wire.Version != wantVersion || len(wire.Surface) == 0 {
			continue
		}
		surfaces = append(surfaces, append(json.RawMessage(nil), wire.Surface...))
	}
	return surfaces
}

// OutputFilesFromDurable returns allowlisted output_file parts in durable order.
func OutputFilesFromDurable(content string) []protocolevent.OutputFileContentPart {
	parts, err := ParseMessageContentParts(content)
	if err != nil {
		return nil
	}
	var files []protocolevent.OutputFileContentPart
	for _, part := range parts {
		if file, ok := part.(protocolevent.OutputFileContentPart); ok {
			files = append(files, file)
		}
	}
	return files
}

// MessageFileAttachment is a console-facing file reference projected from
// durable input_file / output_file parts. It never carries URLs or bytes.
type MessageFileAttachment struct {
	Type      string
	FileID    string
	MediaType string
	Filename  string
	SizeBytes int64
}

// FileAttachmentsFromDurable returns input_file and output_file parts in durable
// order. Used by Console history DTO and the session-scoped content proxy.
func FileAttachmentsFromDurable(content string) []MessageFileAttachment {
	parts, err := ParseMessageContentParts(content)
	if err != nil {
		return nil
	}
	var files []MessageFileAttachment
	for _, part := range parts {
		switch typed := part.(type) {
		case protocolevent.OutputFileContentPart:
			files = append(files, MessageFileAttachment{
				Type:   string(protocolevent.ContentPartTypeOutputFile),
				FileID: typed.FileID, MediaType: typed.MediaType,
				Filename: typed.Filename, SizeBytes: typed.SizeBytes,
			})
		case protocolevent.InputFileContentPart:
			files = append(files, MessageFileAttachment{
				Type:      string(protocolevent.ContentPartTypeInputFile),
				FileID:    typed.FileID,
				MediaType: typed.MediaType,
			})
		}
	}
	return files
}

// HasInboundFileParts reports whether durable content carries input_file or
// output_file parts. Console SendMessage must reject these so a user cannot
// self-bind an arbitrary workspace fileId.
func HasInboundFileParts(content string) bool {
	return len(FileAttachmentsFromDurable(content)) > 0
}

// SerializeAssistantDurableV2 builds the durable chat_messages body.
//
//   - files empty and a2ui nil → plain text (zero wire change)
//   - files non-empty and text == "" → non-empty v1 envelope with empty text part
//
// Each file part is allowlist-rebuilt (type/fileId/mediaType/filename/sizeBytes only).
func SerializeAssistantDurableV2(
	text string,
	files []protocolevent.OutputFileContentPart,
	payload *a2ui.Payload,
) (string, error) {
	if len(files) == 0 {
		return a2ui.SerializeAssistantDurable(text, payload)
	}
	parts := make([]any, 0, 2+len(files))
	parts = append(parts, map[string]any{"type": "text", "text": text})
	for _, file := range files {
		cleaned, err := protocolevent.AllowlistedOutputFile(file)
		if err != nil {
			return "", err
		}
		part := map[string]any{
			"type":   "output_file",
			"fileId": cleaned.FileID,
		}
		if cleaned.MediaType != "" {
			part["mediaType"] = cleaned.MediaType
		}
		if cleaned.Filename != "" {
			part["filename"] = cleaned.Filename
		}
		if cleaned.SizeBytes > 0 {
			part["sizeBytes"] = cleaned.SizeBytes
		}
		parts = append(parts, part)
	}
	if payload != nil {
		if len(payload.Surface) == 0 {
			return "", fmt.Errorf("a2ui: surface required")
		}
		version := payload.Version
		if version == "" {
			version = a2ui.EnvelopeVersionV1
		}
		a2uiPart := map[string]any{
			"type":    "a2ui",
			"version": version,
			"surface": json.RawMessage(payload.Surface),
		}
		if payload.CatalogID != "" {
			a2uiPart["catalogId"] = payload.CatalogID
		}
		parts = append(parts, a2uiPart)
	}
	raw, err := json.Marshal(map[string]any{
		"schemaVersion": MessageContentSchemaVersion,
		"parts":         parts,
	})
	if err != nil {
		return "", err
	}
	return string(raw), nil
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
