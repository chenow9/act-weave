package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

const protocolToolDeltaBytes = 4 * 1024

var protocolToolSensitiveText = regexp.MustCompile(
	`(?i)(?:"(?:authorization|cookie|password|secret|token|api[_-]?key|private[_-]?key|credential|headers?)"\s*:|\b(?:bearer|basic)\s+[a-z0-9._~+/=-]{8,}|awsk_[a-z0-9_-]{8,}|-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----)`,
)

// ToolCallProtocolMapper is the only public mapping from a persisted
// ToolInvocation fact to an AAP tool_call Item. It deliberately has no access
// to ConnectionSnapshot or InvocationRecord.Input/Output, so injected headers
// and retained raw payloads cannot accidentally become protocol data.
type ToolCallProtocolMapper struct {
	validator *protocolevent.PayloadValidator
}

func NewToolCallProtocolMapper() *ToolCallProtocolMapper {
	return &ToolCallProtocolMapper{validator: protocolevent.MustDefaultPayloadValidator()}
}

func (mapper *ToolCallProtocolMapper) MapStarted(
	invocation ToolInvocation,
	name string,
) (protocolevent.ToolCallItem, error) {
	item := protocolevent.ToolCallItem{
		ID: strings.TrimSpace(invocation.ID), Type: protocolevent.ItemTypeToolCall,
		Status: protocolevent.ItemStatusInProgress, Name: strings.TrimSpace(name),
	}
	if !validProtocolToolInvocation(invocation, "RUNNING") || mapper.validateItem(item, protocolevent.EventItemStarted) != nil {
		return protocolevent.ToolCallItem{}, ErrToolInvocationInvalid
	}
	return item, nil
}

// MapArguments validates the complete public argument object before emitting
// any fragment. This prevents a sensitive key or token split across provider
// chunks from leaking through otherwise individually harmless-looking Delta.
func (mapper *ToolCallProtocolMapper) MapArguments(
	invocation ToolInvocation,
) (json.RawMessage, []protocolevent.ArgumentsJSONDelta, error) {
	if !validProtocolToolInvocation(invocation, "RUNNING", "SUCCEEDED", "FAILED") {
		return nil, nil, ErrToolInvocationInvalid
	}
	arguments, decoded, err := canonicalPublicToolObject(invocation.InputSummary)
	if err != nil || containsInjectedHeader(decoded) {
		return nil, nil, ErrToolInvocationInvalid
	}
	probe := protocolevent.ToolCallItem{
		ID: invocation.ID, Type: protocolevent.ItemTypeToolCall,
		Status: protocolevent.ItemStatusCompleted, Name: "tool", Arguments: arguments,
	}
	if mapper.validateItem(probe, protocolevent.EventItemCompleted) != nil {
		return nil, nil, ErrToolInvocationInvalid
	}
	fragments := splitProtocolToolText(string(arguments), protocolToolDeltaBytes)
	deltas := make([]protocolevent.ArgumentsJSONDelta, 0, len(fragments))
	for _, fragment := range fragments {
		delta := protocolevent.ArgumentsJSONDelta{
			Type: protocolevent.DeltaTypeArgumentsJSON, PartialJSON: fragment,
		}
		if mapper.validateDelta(invocation.ID, delta) != nil {
			return nil, nil, ErrToolInvocationInvalid
		}
		deltas = append(deltas, delta)
	}
	return arguments, deltas, nil
}

func (mapper *ToolCallProtocolMapper) MapProgress(
	invocation ToolInvocation,
	current float64,
	total *float64,
	unit, message string,
) (protocolevent.ProgressDelta, error) {
	delta := protocolevent.ProgressDelta{
		Type: protocolevent.DeltaTypeProgress, Current: current, Total: total,
		Unit: strings.TrimSpace(unit), Message: strings.TrimSpace(message),
	}
	if !validProtocolToolInvocation(invocation, "RUNNING") || mapper.validateDelta(invocation.ID, delta) != nil {
		return protocolevent.ProgressDelta{}, ErrToolInvocationInvalid
	}
	return delta, nil
}

func (mapper *ToolCallProtocolMapper) MapWaiting(
	invocation ToolInvocation,
) (protocolevent.ProgressDelta, error) {
	return mapper.MapProgress(invocation, 0, nil, "state", "waiting_confirmation")
}

// MapOutput validates the complete already-redacted public text before
// splitting it. Raw executor output belongs in StoredObject, never here.
func (mapper *ToolCallProtocolMapper) MapOutput(
	invocation ToolInvocation,
	publicText string,
) ([]protocolevent.OutputDelta, error) {
	if !validProtocolToolInvocation(invocation, "RUNNING") || publicText == "" ||
		protocolToolSensitiveText.MatchString(publicText) {
		return nil, ErrToolInvocationInvalid
	}
	fragments := splitProtocolToolText(publicText, protocolToolDeltaBytes)
	deltas := make([]protocolevent.OutputDelta, 0, len(fragments))
	for _, fragment := range fragments {
		delta := protocolevent.OutputDelta{Type: protocolevent.DeltaTypeOutput, Text: fragment}
		if mapper.validateDelta(invocation.ID, delta) != nil {
			return nil, ErrToolInvocationInvalid
		}
		deltas = append(deltas, delta)
	}
	return deltas, nil
}

func (mapper *ToolCallProtocolMapper) MapCompleted(
	invocation ToolInvocation,
	name string,
) (protocolevent.ToolCallItem, error) {
	status := strings.ToUpper(strings.TrimSpace(invocation.Status))
	if !validProtocolToolInvocation(invocation, "SUCCEEDED", "FAILED") || invocation.FinishedAt == nil {
		return protocolevent.ToolCallItem{}, ErrToolInvocationInvalid
	}
	arguments, _, err := mapper.MapArguments(invocation)
	if err != nil {
		return protocolevent.ToolCallItem{}, err
	}
	item := protocolevent.ToolCallItem{
		ID: invocation.ID, Type: protocolevent.ItemTypeToolCall,
		Name: strings.TrimSpace(name), Arguments: arguments,
	}
	switch status {
	case "SUCCEEDED":
		output, decoded, outputErr := canonicalPublicToolOutput(invocation.OutputSummary)
		if outputErr != nil || containsInjectedHeader(decoded) {
			return protocolevent.ToolCallItem{}, ErrToolInvocationInvalid
		}
		item.Status, item.Output = protocolevent.ItemStatusCompleted, output
	case "FAILED":
		if !stableProtocolErrorCode.MatchString(strings.TrimSpace(invocation.ErrorCode)) {
			return protocolevent.ToolCallItem{}, ErrToolInvocationInvalid
		}
		// The database summary may contain provider diagnostics. Failed public
		// Items expose only a stable code and generic message.
		item.Status = protocolevent.ItemStatusFailed
		item.Output, err = json.Marshal(map[string]any{
			"error": map[string]any{
				"code": invocation.ErrorCode, "message": "Tool execution failed", "retryable": false,
			},
		})
		if err != nil {
			return protocolevent.ToolCallItem{}, ErrToolInvocationInvalid
		}
	}
	if mapper.validateItem(item, protocolevent.EventItemCompleted) != nil {
		return protocolevent.ToolCallItem{}, ErrToolInvocationInvalid
	}
	return item, nil
}

func (mapper *ToolCallProtocolMapper) validateItem(item protocolevent.ToolCallItem, eventType string) error {
	if mapper == nil || mapper.validator == nil {
		return ErrToolInvocationInvalid
	}
	data, err := json.Marshal(protocolevent.ItemSnapshotData{Item: item})
	if err != nil {
		return err
	}
	return mapper.validator.ValidateEventData(eventType, data)
}

func (mapper *ToolCallProtocolMapper) validateDelta(itemID string, delta protocolevent.Delta) error {
	if mapper == nil || mapper.validator == nil {
		return ErrToolInvocationInvalid
	}
	data, err := json.Marshal(protocolevent.ItemDeltaData{ItemID: itemID, Delta: delta})
	if err != nil {
		return err
	}
	return mapper.validator.ValidateEventData(protocolevent.EventItemDelta, data)
}

type ProtocolToolCallContext struct {
	Scope         protocolevent.RunScope
	EventStreamID string
	TraceID       string
}

type ProjectToolCallStartedInput struct {
	Context    ProtocolToolCallContext
	Invocation ToolInvocation
	Name       string
	Ordinal    int
	// SourceType overrides CreateRunItem.SourceType. Empty keeps TOOL_INVOCATION.
	SourceType string
}

type ProjectToolCallDeltaInput struct {
	Context    ProtocolToolCallContext
	Invocation ToolInvocation
	OccurredAt time.Time
}

type ProjectToolCallProgressInput struct {
	Context    ProtocolToolCallContext
	Invocation ToolInvocation
	Current    float64
	Total      *float64
	Unit       string
	Message    string
	OccurredAt time.Time
}

type ProjectToolCallOutputInput struct {
	Context    ProtocolToolCallContext
	Invocation ToolInvocation
	PublicText string
	OccurredAt time.Time
}

type CompleteProtocolToolCallInput struct {
	Context     ProtocolToolCallContext
	Invocation  ToolInvocation
	Name        string
	CompletedAt time.Time
}

type ProtocolToolCallProjectionResult struct {
	Projection  protocolevent.RunItemProjection
	Events      []protocolevent.ProtocolEvent
	NotifyError error
}

type ProtocolToolCallProjector struct {
	unit   *protocolevent.ProtocolUnitOfWork
	mapper *ToolCallProtocolMapper
}

func NewProtocolToolCallProjector(
	unit *protocolevent.ProtocolUnitOfWork,
	mapper *ToolCallProtocolMapper,
) (*ProtocolToolCallProjector, error) {
	if unit == nil || mapper == nil {
		return nil, ErrToolInvocationInvalid
	}
	return &ProtocolToolCallProjector{unit: unit, mapper: mapper}, nil
}

func (projector *ProtocolToolCallProjector) ProjectStarted(
	ctx context.Context,
	input ProjectToolCallStartedInput,
) (ProtocolToolCallProjectionResult, error) {
	if !validProtocolToolProjector(projector) || input.Ordinal < 1 ||
		validateProtocolToolContext(input.Context, input.Invocation) != nil {
		return ProtocolToolCallProjectionResult{}, ErrToolInvocationInvalid
	}
	item, err := projector.mapper.MapStarted(input.Invocation, input.Name)
	if err != nil {
		return ProtocolToolCallProjectionResult{}, err
	}
	event, err := buildToolSnapshotEvent(input.Context, item, protocolevent.EventItemStarted, input.Invocation.StartedAt)
	if err != nil {
		return ProtocolToolCallProjectionResult{}, err
	}
	var projection protocolevent.RunItemProjection
	result, err := projector.unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
		if _, err := transaction.EnsureRunEventStream(ctx, input.Context.EventStreamID, input.Context.Scope); err != nil {
			return err
		}
		sourceType := strings.TrimSpace(input.SourceType)
		if sourceType == "" {
			sourceType = protocolevent.SourceToolInvocation
		}
		projection, err = transaction.CreateRunItem(ctx, protocolevent.CreateRunItemInput{
			WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
			RunID: input.Context.Scope.RunID, Ordinal: input.Ordinal,
			SourceType: sourceType, SourceID: input.Invocation.ID,
			Item: item, StartedAt: input.Invocation.StartedAt,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		return ProtocolToolCallProjectionResult{}, err
	}
	return toolProjectionResult(projection, result), nil
}

func (projector *ProtocolToolCallProjector) ProjectArguments(
	ctx context.Context,
	input ProjectToolCallDeltaInput,
) (ProtocolToolCallProjectionResult, error) {
	if !validProtocolToolProjector(projector) || input.OccurredAt.IsZero() ||
		validateProtocolToolContext(input.Context, input.Invocation) != nil {
		return ProtocolToolCallProjectionResult{}, ErrToolInvocationInvalid
	}
	_, mapped, err := projector.mapper.MapArguments(input.Invocation)
	if err != nil {
		return ProtocolToolCallProjectionResult{}, err
	}
	deltas := make([]protocolevent.Delta, 0, len(mapped))
	for _, delta := range mapped {
		deltas = append(deltas, delta)
	}
	return projector.projectDeltas(ctx, input.Context, input.Invocation.ID, deltas, input.OccurredAt)
}

func (projector *ProtocolToolCallProjector) ProjectWaiting(
	ctx context.Context,
	input ProjectToolCallDeltaInput,
) (ProtocolToolCallProjectionResult, error) {
	if !validProtocolToolProjector(projector) || input.OccurredAt.IsZero() ||
		validateProtocolToolContext(input.Context, input.Invocation) != nil {
		return ProtocolToolCallProjectionResult{}, ErrToolInvocationInvalid
	}
	delta, err := projector.mapper.MapWaiting(input.Invocation)
	if err != nil {
		return ProtocolToolCallProjectionResult{}, err
	}
	return projector.projectDeltas(ctx, input.Context, input.Invocation.ID, []protocolevent.Delta{delta}, input.OccurredAt)
}

func (projector *ProtocolToolCallProjector) ProjectProgress(
	ctx context.Context,
	input ProjectToolCallProgressInput,
) (ProtocolToolCallProjectionResult, error) {
	if !validProtocolToolProjector(projector) || input.OccurredAt.IsZero() ||
		validateProtocolToolContext(input.Context, input.Invocation) != nil {
		return ProtocolToolCallProjectionResult{}, ErrToolInvocationInvalid
	}
	delta, err := projector.mapper.MapProgress(
		input.Invocation, input.Current, input.Total, input.Unit, input.Message,
	)
	if err != nil {
		return ProtocolToolCallProjectionResult{}, err
	}
	return projector.projectDeltas(ctx, input.Context, input.Invocation.ID, []protocolevent.Delta{delta}, input.OccurredAt)
}

func (projector *ProtocolToolCallProjector) ProjectOutput(
	ctx context.Context,
	input ProjectToolCallOutputInput,
) (ProtocolToolCallProjectionResult, error) {
	if !validProtocolToolProjector(projector) || input.OccurredAt.IsZero() ||
		validateProtocolToolContext(input.Context, input.Invocation) != nil {
		return ProtocolToolCallProjectionResult{}, ErrToolInvocationInvalid
	}
	mapped, err := projector.mapper.MapOutput(input.Invocation, input.PublicText)
	if err != nil {
		return ProtocolToolCallProjectionResult{}, err
	}
	deltas := make([]protocolevent.Delta, 0, len(mapped))
	for _, delta := range mapped {
		deltas = append(deltas, delta)
	}
	return projector.projectDeltas(ctx, input.Context, input.Invocation.ID, deltas, input.OccurredAt)
}

func (projector *ProtocolToolCallProjector) Complete(
	ctx context.Context,
	input CompleteProtocolToolCallInput,
) (ProtocolToolCallProjectionResult, error) {
	if !validProtocolToolProjector(projector) || input.CompletedAt.IsZero() ||
		validateProtocolToolContext(input.Context, input.Invocation) != nil {
		return ProtocolToolCallProjectionResult{}, ErrToolInvocationInvalid
	}
	item, err := projector.mapper.MapCompleted(input.Invocation, input.Name)
	if err != nil {
		return ProtocolToolCallProjectionResult{}, err
	}
	event, err := buildToolSnapshotEvent(input.Context, item, protocolevent.EventItemCompleted, input.CompletedAt)
	if err != nil {
		return ProtocolToolCallProjectionResult{}, err
	}
	var projection protocolevent.RunItemProjection
	result, err := projector.unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
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
		return ProtocolToolCallProjectionResult{}, err
	}
	return toolProjectionResult(projection, result), nil
}

func (projector *ProtocolToolCallProjector) projectDeltas(
	ctx context.Context,
	protocolContext ProtocolToolCallContext,
	itemID string,
	deltas []protocolevent.Delta,
	occurredAt time.Time,
) (ProtocolToolCallProjectionResult, error) {
	if len(deltas) == 0 {
		return ProtocolToolCallProjectionResult{}, ErrToolInvocationInvalid
	}
	events := make([]protocolevent.NewProtocolEvent, 0, len(deltas))
	for _, delta := range deltas {
		event, err := buildToolDeltaEvent(protocolContext, itemID, delta, occurredAt)
		if err != nil {
			return ProtocolToolCallProjectionResult{}, err
		}
		events = append(events, event)
	}
	var projection protocolevent.RunItemProjection
	result, err := projector.unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
		for _, delta := range deltas {
			var err error
			projection, err = transaction.ApplyRunItemDelta(ctx, protocolevent.ApplyItemDeltaInput{
				WorkspaceID: protocolContext.Scope.WorkspaceID, AgentID: protocolContext.Scope.AgentID,
				RunID: protocolContext.Scope.RunID, ItemID: itemID, Delta: delta,
			})
			if err != nil {
				return err
			}
		}
		_, err := transaction.Append(ctx, events)
		return err
	})
	if err != nil {
		return ProtocolToolCallProjectionResult{}, err
	}
	return toolProjectionResult(projection, result), nil
}

func buildToolSnapshotEvent(
	protocolContext ProtocolToolCallContext,
	item protocolevent.ToolCallItem,
	eventType string,
	occurredAt time.Time,
) (protocolevent.NewProtocolEvent, error) {
	eventID, err := uuid.NewV7()
	if err != nil {
		return protocolevent.NewProtocolEvent{}, err
	}
	return protocolevent.BuildProtocolEvent(toolEventBase(
		protocolContext, eventID.String(), item.ID, eventType, occurredAt,
	), protocolevent.ItemSnapshotData{Item: item})
}

func buildToolDeltaEvent(
	protocolContext ProtocolToolCallContext,
	itemID string,
	delta protocolevent.Delta,
	occurredAt time.Time,
) (protocolevent.NewProtocolEvent, error) {
	eventID, err := uuid.NewV7()
	if err != nil {
		return protocolevent.NewProtocolEvent{}, err
	}
	return protocolevent.BuildProtocolEvent(toolEventBase(
		protocolContext, eventID.String(), itemID, protocolevent.EventItemDelta, occurredAt,
	), protocolevent.ItemDeltaData{ItemID: itemID, Delta: delta})
}

func toolEventBase(
	protocolContext ProtocolToolCallContext,
	eventID, itemID, eventType string,
	occurredAt time.Time,
) protocolevent.NewProtocolEvent {
	return protocolevent.NewProtocolEvent{
		ID: eventID, EventStreamID: protocolContext.EventStreamID,
		WorkspaceID: protocolContext.Scope.WorkspaceID, AgentID: protocolContext.Scope.AgentID,
		ConversationID: protocolContext.Scope.ConversationID, RunID: protocolContext.Scope.RunID,
		Type: eventType, SpecVersion: "1.0", TraceID: protocolContext.TraceID,
		ItemID: itemID, OccurredAt: occurredAt,
	}
}

func validateProtocolToolContext(protocolContext ProtocolToolCallContext, invocation ToolInvocation) error {
	if !invocationValidUUID(protocolContext.Scope.WorkspaceID) ||
		!invocationValidUUID(protocolContext.Scope.AgentID) ||
		!invocationValidUUID(protocolContext.Scope.ConversationID) ||
		!invocationValidUUID(protocolContext.Scope.RunID) ||
		!invocationValidUUID(strings.TrimSpace(protocolContext.EventStreamID)) ||
		strings.TrimSpace(protocolContext.TraceID) == "" ||
		invocation.WorkspaceID != protocolContext.Scope.WorkspaceID ||
		invocation.AgentRunID != protocolContext.Scope.RunID ||
		invocation.TraceID != protocolContext.TraceID {
		return ErrToolInvocationInvalid
	}
	return nil
}

func validProtocolToolInvocation(invocation ToolInvocation, statuses ...string) bool {
	if !invocationValidUUID(strings.TrimSpace(invocation.ID)) ||
		!invocationValidUUID(strings.TrimSpace(invocation.WorkspaceID)) ||
		!invocationValidUUID(strings.TrimSpace(invocation.CapabilityReleaseID)) ||
		!invocationValidUUID(strings.TrimSpace(invocation.AgentRunID)) ||
		strings.TrimSpace(invocation.TraceID) == "" || invocation.StartedAt.IsZero() {
		return false
	}
	actual := strings.ToUpper(strings.TrimSpace(invocation.Status))
	for _, status := range statuses {
		if actual == status {
			return true
		}
	}
	return false
}

func canonicalPublicToolObject(raw json.RawMessage) (json.RawMessage, any, error) {
	canonical, value, err := canonicalPublicToolJSON(raw)
	if err != nil {
		return nil, nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, nil, ErrToolInvocationInvalid
	}
	return canonical, value, nil
}

func canonicalPublicToolOutput(raw json.RawMessage) (json.RawMessage, any, error) {
	canonical, value, err := canonicalPublicToolJSON(raw)
	if err != nil {
		return nil, nil, err
	}
	switch value.(type) {
	case nil, string, []any, map[string]any:
		return canonical, value, nil
	default:
		return nil, nil, ErrToolInvocationInvalid
	}
}

func canonicalPublicToolJSON(raw json.RawMessage) (json.RawMessage, any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil, ErrToolInvocationInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, ErrToolInvocationInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, nil, ErrToolInvocationInvalid
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, nil, ErrToolInvocationInvalid
	}
	return canonical, value, nil
}

func containsInjectedHeader(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(key))
			if strings.Contains(normalized, "header") || containsInjectedHeader(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsInjectedHeader(nested) {
				return true
			}
		}
	}
	return false
}

func splitProtocolToolText(value string, maxBytes int) []string {
	if value == "" || maxBytes <= 0 {
		return nil
	}
	result := make([]string, 0, (len(value)+maxBytes-1)/maxBytes)
	for len(value) > maxBytes {
		cut := maxBytes
		for cut > 0 && !utf8.ValidString(value[:cut]) {
			cut--
		}
		if cut == 0 {
			cut = len(value)
		}
		result = append(result, value[:cut])
		value = value[cut:]
	}
	if value != "" {
		result = append(result, value)
	}
	return result
}

func validProtocolToolProjector(projector *ProtocolToolCallProjector) bool {
	return projector != nil && projector.unit != nil && projector.mapper != nil
}

func toolProjectionResult(
	projection protocolevent.RunItemProjection,
	result protocolevent.UnitOfWorkResult,
) ProtocolToolCallProjectionResult {
	return ProtocolToolCallProjectionResult{
		Projection: projection, Events: result.Events, NotifyError: result.NotifyError,
	}
}
