package chatruntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"

	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

var (
	ErrAuxiliaryProtocolInvalid = errors.New("auxiliary protocol item is invalid")
	ErrUsageRegression          = errors.New("protocol usage snapshot regressed")
	ErrRawReasoning             = errors.New("raw reasoning is not public protocol content")
)

var (
	publicNoticeCode       = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)
	rawReasoningText       = regexp.MustCompile(`(?i)(chain[ _-]?of[ _-]?thought|<\/?thinking>|scratchpad|hidden reasoning|internal reasoning)`)
	publicReasoningMaxRune = 16 * 1024
)

type AuxiliaryProtocolMapper struct {
	validator *protocolevent.PayloadValidator
}

func NewAuxiliaryProtocolMapper() *AuxiliaryProtocolMapper {
	return &AuxiliaryProtocolMapper{validator: protocolevent.MustDefaultPayloadValidator()}
}

func (mapper *AuxiliaryProtocolMapper) MapUsage(
	inputTokens, outputTokens int64,
) (protocolevent.Usage, error) {
	if mapper == nil || inputTokens < 0 || outputTokens < 0 || inputTokens > math.MaxInt64-outputTokens {
		return protocolevent.Usage{}, ErrAuxiliaryProtocolInvalid
	}
	usage := protocolevent.Usage{
		InputTokens: inputTokens, OutputTokens: outputTokens,
		TotalTokens: inputTokens + outputTokens,
	}
	data, err := json.Marshal(protocolevent.UsageData{Usage: usage})
	if err != nil || mapper.validator.ValidateEventData(protocolevent.EventUsageUpdated, data) != nil {
		return protocolevent.Usage{}, ErrAuxiliaryProtocolInvalid
	}
	return usage, nil
}

func (mapper *AuxiliaryProtocolMapper) MapReasoningSummary(
	itemID, text string,
) (protocolevent.ReasoningSummaryItem, error) {
	text = strings.TrimSpace(text)
	item := protocolevent.ReasoningSummaryItem{
		ID: strings.TrimSpace(itemID), Type: protocolevent.ItemTypeReasoningSummary,
		Status: protocolevent.ItemStatusCompleted, Text: text,
	}
	if mapper == nil || text == "" || len([]rune(text)) > publicReasoningMaxRune || rawReasoningText.MatchString(text) {
		return protocolevent.ReasoningSummaryItem{}, ErrRawReasoning
	}
	if mapper.validateItem(item) != nil {
		return protocolevent.ReasoningSummaryItem{}, ErrAuxiliaryProtocolInvalid
	}
	return item, nil
}

func (mapper *AuxiliaryProtocolMapper) MapNotice(
	itemID, code, message string,
) (protocolevent.NoticeItem, error) {
	item := protocolevent.NoticeItem{
		ID: strings.TrimSpace(itemID), Type: protocolevent.ItemTypeNotice,
		Status: protocolevent.ItemStatusCompleted, Code: strings.TrimSpace(code),
		Message: strings.TrimSpace(message),
	}
	if mapper == nil || !publicNoticeCode.MatchString(item.Code) || item.Message == "" ||
		len([]rune(item.Message)) > 2048 || mapper.validateItem(item) != nil {
		return protocolevent.NoticeItem{}, ErrAuxiliaryProtocolInvalid
	}
	return item, nil
}

func (mapper *AuxiliaryProtocolMapper) validateItem(item protocolevent.Item) error {
	if mapper == nil || mapper.validator == nil {
		return ErrAuxiliaryProtocolInvalid
	}
	data, err := json.Marshal(protocolevent.ItemSnapshotData{Item: item})
	if err != nil {
		return err
	}
	return mapper.validator.ValidateEventData(protocolevent.EventItemCompleted, data)
}

type AuxiliaryProtocolContext struct {
	Scope         protocolevent.RunScope
	EventStreamID string
	TraceID       string
}

type ProjectUsageInput struct {
	Context      AuxiliaryProtocolContext
	InputTokens  int64
	OutputTokens int64
	OccurredAt   time.Time
}

type ProjectReasoningSummaryInput struct {
	Context    AuxiliaryProtocolContext
	ItemID     string
	Text       string
	Ordinal    int
	SourceID   string
	OccurredAt time.Time
}

type ProjectNoticeInput struct {
	Context    AuxiliaryProtocolContext
	ItemID     string
	Code       string
	Message    string
	Ordinal    int
	SourceID   string
	OccurredAt time.Time
}

type AuxiliaryProjectionResult struct {
	Projection  protocolevent.RunItemProjection
	Events      []protocolevent.ProtocolEvent
	NotifyError error
}

type AuxiliaryProtocolProjector struct {
	unit   *protocolevent.ProtocolUnitOfWork
	mapper *AuxiliaryProtocolMapper
}

func NewAuxiliaryProtocolProjector(
	unit *protocolevent.ProtocolUnitOfWork,
	mapper *AuxiliaryProtocolMapper,
) (*AuxiliaryProtocolProjector, error) {
	if unit == nil || mapper == nil {
		return nil, ErrAuxiliaryProtocolInvalid
	}
	return &AuxiliaryProtocolProjector{unit: unit, mapper: mapper}, nil
}

func (projector *AuxiliaryProtocolProjector) ProjectUsage(
	ctx context.Context,
	input ProjectUsageInput,
) (AuxiliaryProjectionResult, error) {
	if !validAuxiliaryProjector(projector) || input.OccurredAt.IsZero() ||
		validateAuxiliaryContext(input.Context) != nil {
		return AuxiliaryProjectionResult{}, ErrAuxiliaryProtocolInvalid
	}
	usage, err := projector.mapper.MapUsage(input.InputTokens, input.OutputTokens)
	if err != nil {
		return AuxiliaryProjectionResult{}, err
	}
	event, err := buildAuxiliaryEvent(input.Context, "", protocolevent.EventUsageUpdated,
		protocolevent.UsageData{Usage: usage}, input.OccurredAt)
	if err != nil {
		return AuxiliaryProjectionResult{}, err
	}
	result, err := projector.unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
		if _, err := transaction.EnsureRunEventStream(ctx, input.Context.EventStreamID, input.Context.Scope); err != nil {
			return err
		}
		tx, err := transaction.SQLTx()
		if err != nil {
			return err
		}
		var nextSequence int64
		if err := tx.QueryRowContext(ctx, `
			SELECT next_sequence FROM protocol_event_streams
			WHERE id=$1 AND workspace_id=$2 AND run_id=$3 FOR UPDATE
		`, input.Context.EventStreamID, input.Context.Scope.WorkspaceID, input.Context.Scope.RunID).Scan(&nextSequence); err != nil {
			return err
		}
		var previous protocolevent.Usage
		err = tx.QueryRowContext(ctx, `
			SELECT (payload->'data'->'usage'->>'inputTokens')::bigint,
			       (payload->'data'->'usage'->>'outputTokens')::bigint,
			       (payload->'data'->'usage'->>'totalTokens')::bigint
			FROM protocol_events
			WHERE stream_id=$1 AND event_type='usage.updated'
			ORDER BY sequence_no DESC LIMIT 1
		`, input.Context.EventStreamID).Scan(
			&previous.InputTokens, &previous.OutputTokens, &previous.TotalTokens,
		)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && (usage.InputTokens < previous.InputTokens ||
			usage.OutputTokens < previous.OutputTokens || usage.TotalTokens < previous.TotalTokens ||
			usage == previous) {
			return ErrUsageRegression
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		return AuxiliaryProjectionResult{}, err
	}
	return AuxiliaryProjectionResult{Events: result.Events, NotifyError: result.NotifyError}, nil
}

func (projector *AuxiliaryProtocolProjector) ProjectReasoningSummary(
	ctx context.Context,
	input ProjectReasoningSummaryInput,
) (AuxiliaryProjectionResult, error) {
	if !validAuxiliaryProjector(projector) || input.Ordinal < 1 || input.OccurredAt.IsZero() ||
		validateAuxiliaryContext(input.Context) != nil {
		return AuxiliaryProjectionResult{}, ErrAuxiliaryProtocolInvalid
	}
	item, err := projector.mapper.MapReasoningSummary(input.ItemID, input.Text)
	if err != nil {
		return AuxiliaryProjectionResult{}, err
	}
	return projector.projectCompletedItem(ctx, input.Context, item, input.Ordinal, input.SourceID, input.OccurredAt)
}

func (projector *AuxiliaryProtocolProjector) ProjectNotice(
	ctx context.Context,
	input ProjectNoticeInput,
) (AuxiliaryProjectionResult, error) {
	if !validAuxiliaryProjector(projector) || input.Ordinal < 1 || input.OccurredAt.IsZero() ||
		validateAuxiliaryContext(input.Context) != nil {
		return AuxiliaryProjectionResult{}, ErrAuxiliaryProtocolInvalid
	}
	item, err := projector.mapper.MapNotice(input.ItemID, input.Code, input.Message)
	if err != nil {
		return AuxiliaryProjectionResult{}, err
	}
	return projector.projectCompletedItem(ctx, input.Context, item, input.Ordinal, input.SourceID, input.OccurredAt)
}

func (projector *AuxiliaryProtocolProjector) projectCompletedItem(
	ctx context.Context,
	protocolContext AuxiliaryProtocolContext,
	item protocolevent.Item,
	ordinal int,
	sourceID string,
	occurredAt time.Time,
) (AuxiliaryProjectionResult, error) {
	event, err := buildAuxiliaryEvent(protocolContext, item.ItemID(), protocolevent.EventItemCompleted,
		protocolevent.ItemSnapshotData{Item: item}, occurredAt)
	if err != nil {
		return AuxiliaryProjectionResult{}, err
	}
	started := auxiliaryStartedItem(item)
	if started == nil {
		return AuxiliaryProjectionResult{}, ErrAuxiliaryProtocolInvalid
	}
	var projection protocolevent.RunItemProjection
	result, err := projector.unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
		if _, err := transaction.EnsureRunEventStream(ctx, protocolContext.EventStreamID, protocolContext.Scope); err != nil {
			return err
		}
		if _, err := transaction.CreateRunItem(ctx, protocolevent.CreateRunItemInput{
			WorkspaceID: protocolContext.Scope.WorkspaceID, AgentID: protocolContext.Scope.AgentID,
			RunID: protocolContext.Scope.RunID, Ordinal: ordinal,
			SourceType: protocolevent.SourceRuntime, SourceID: strings.TrimSpace(sourceID),
			Item: started, StartedAt: occurredAt,
		}); err != nil {
			return err
		}
		projection, err = transaction.CompleteRunItem(ctx, protocolevent.CompleteRunItemInput{
			WorkspaceID: protocolContext.Scope.WorkspaceID, AgentID: protocolContext.Scope.AgentID,
			RunID: protocolContext.Scope.RunID, Item: item, CompletedAt: occurredAt,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		return AuxiliaryProjectionResult{}, err
	}
	return AuxiliaryProjectionResult{
		Projection: projection, Events: result.Events, NotifyError: result.NotifyError,
	}, nil
}

func buildAuxiliaryEvent(
	protocolContext AuxiliaryProtocolContext,
	itemID, eventType string,
	data protocolevent.EventData,
	occurredAt time.Time,
) (protocolevent.NewProtocolEvent, error) {
	eventID, err := uuid.NewV7()
	if err != nil {
		return protocolevent.NewProtocolEvent{}, err
	}
	return protocolevent.BuildProtocolEvent(protocolevent.NewProtocolEvent{
		ID: eventID.String(), EventStreamID: protocolContext.EventStreamID,
		WorkspaceID: protocolContext.Scope.WorkspaceID, AgentID: protocolContext.Scope.AgentID,
		ConversationID: protocolContext.Scope.ConversationID, RunID: protocolContext.Scope.RunID,
		Type: eventType, SpecVersion: "1.0", TraceID: protocolContext.TraceID,
		ItemID: itemID, OccurredAt: occurredAt.UTC(),
	}, data)
}

func auxiliaryStartedItem(item protocolevent.Item) protocolevent.Item {
	switch value := item.(type) {
	case protocolevent.ReasoningSummaryItem:
		value.Status = protocolevent.ItemStatusInProgress
		return value
	case protocolevent.NoticeItem:
		value.Status = protocolevent.ItemStatusInProgress
		return value
	default:
		return nil
	}
}

func validateAuxiliaryContext(protocolContext AuxiliaryProtocolContext) error {
	for _, value := range []string{
		protocolContext.Scope.WorkspaceID, protocolContext.Scope.AgentID,
		protocolContext.Scope.ConversationID, protocolContext.Scope.RunID,
		protocolContext.EventStreamID,
	} {
		if _, err := uuid.Parse(strings.TrimSpace(value)); err != nil {
			return ErrAuxiliaryProtocolInvalid
		}
	}
	if strings.TrimSpace(protocolContext.TraceID) == "" {
		return ErrAuxiliaryProtocolInvalid
	}
	return nil
}

func validAuxiliaryProjector(projector *AuxiliaryProtocolProjector) bool {
	return projector != nil && projector.unit != nil && projector.mapper != nil
}
