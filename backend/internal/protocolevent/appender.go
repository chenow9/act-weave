package protocolevent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const maxAppendBatchSize = 500

var (
	ErrAppendInvalid       = errors.New("protocol event append is invalid")
	ErrEventStreamNotFound = errors.New("protocol event stream was not found")
	ErrEventConflict       = errors.New("protocol event conflicts with persisted data")
)

type NewProtocolEvent struct {
	ID             string
	EventStreamID  string
	WorkspaceID    string
	AgentID        string
	ConversationID string
	RunID          string
	Type           string
	SpecVersion    string
	TraceID        string
	ItemID         string
	InteractionID  string
	OccurredAt     time.Time
	Data           json.RawMessage
}

type ProtocolEvent struct {
	GlobalPosition int64
	ID             string
	EventStreamID  string
	StreamID       string
	WorkspaceID    string
	AgentID        string
	ConversationID string
	RunID          string
	Type           string
	SpecVersion    string
	TraceID        string
	ItemID         string
	InteractionID  string
	Sequence       int64
	OccurredAt     time.Time
	Data           json.RawMessage
	Payload        json.RawMessage
}

// EventAppender only appends through a caller-owned transaction. It never
// begins, commits, or rolls back a transaction itself.
type EventAppender struct{ validator *PayloadValidator }

func NewEventAppender() *EventAppender {
	return &EventAppender{validator: MustDefaultPayloadValidator()}
}

func NewEventAppenderWithPolicy(policy PayloadPolicy) (*EventAppender, error) {
	validator, err := NewPayloadValidator(policy)
	if err != nil {
		return nil, err
	}
	return &EventAppender{validator: validator}, nil
}

func (appender *EventAppender) AppendInTx(
	ctx context.Context,
	tx *sql.Tx,
	events []NewProtocolEvent,
) ([]ProtocolEvent, error) {
	if appender == nil || tx == nil || len(events) == 0 || len(events) > maxAppendBatchSize {
		return nil, ErrAppendInvalid
	}
	normalized := make([]NewProtocolEvent, len(events))
	for index := range events {
		value, err := normalizeAppendEvent(events[index])
		if err != nil {
			return nil, err
		}
		normalized[index] = value
		validator := appender.validator
		if validator == nil {
			validator = MustDefaultPayloadValidator()
		}
		if err := validator.ValidateEventData(value.Type, value.Data); err != nil {
			return nil, err
		}
		largestEnvelope, err := buildEnvelope(value, int64(^uint64(0)>>1))
		if err != nil {
			return nil, err
		}
		if err := validator.ValidateEnvelopeSize(largestEnvelope); err != nil {
			return nil, err
		}
		if index > 0 && !sameAppendScope(normalized[0], value) {
			return nil, ErrAppendInvalid
		}
	}

	first := normalized[0]
	var firstSequence int64
	err := tx.QueryRowContext(ctx, `
		UPDATE protocol_event_streams
		SET next_sequence = next_sequence + $6
		WHERE id=$1 AND workspace_id=$2 AND agent_id=$3
		  AND conversation_id=$4 AND run_id=$5
		RETURNING next_sequence - $6
	`, first.EventStreamID, first.WorkspaceID, first.AgentID, first.ConversationID,
		first.RunID, len(normalized)).Scan(&firstSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEventStreamNotFound
	}
	if err != nil {
		return nil, mapAppendError("allocate protocol event sequence", err)
	}

	appended := make([]ProtocolEvent, 0, len(normalized))
	for index, event := range normalized {
		sequence := firstSequence + int64(index)
		payload, err := buildEnvelope(event, sequence)
		if err != nil {
			return nil, err
		}
		var globalPosition int64
		err = tx.QueryRowContext(ctx, `
			INSERT INTO protocol_events(
			 id,workspace_id,agent_id,conversation_id,run_id,stream_id,
			 sequence_no,event_type,spec_version,item_id,interaction_id,payload,occurred_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			RETURNING global_position
		`, event.ID, event.WorkspaceID, event.AgentID, event.ConversationID, event.RunID,
			event.EventStreamID, sequence, event.Type, event.SpecVersion,
			nullableAppendID(event.ItemID), nullableAppendID(event.InteractionID),
			[]byte(payload), event.OccurredAt).Scan(&globalPosition)
		if err != nil {
			return nil, mapAppendError("insert protocol event", err)
		}
		appended = append(appended, ProtocolEvent{
			GlobalPosition: globalPosition,
			ID:             event.ID, EventStreamID: event.EventStreamID,
			StreamID:    "run:" + event.RunID,
			WorkspaceID: event.WorkspaceID, AgentID: event.AgentID,
			ConversationID: event.ConversationID, RunID: event.RunID,
			Type: event.Type, SpecVersion: event.SpecVersion, TraceID: event.TraceID,
			ItemID: event.ItemID, InteractionID: event.InteractionID,
			Sequence: sequence, OccurredAt: event.OccurredAt,
			Data:    append(json.RawMessage(nil), event.Data...),
			Payload: append(json.RawMessage(nil), payload...),
		})
	}
	return appended, nil
}

var appendEventTypePattern = regexp.MustCompile(`^[a-z][a-z_]*\.[a-z][a-z_]*$`)

func normalizeAppendEvent(event NewProtocolEvent) (NewProtocolEvent, error) {
	event.ID = strings.ToLower(strings.TrimSpace(event.ID))
	event.EventStreamID = strings.ToLower(strings.TrimSpace(event.EventStreamID))
	event.WorkspaceID = strings.ToLower(strings.TrimSpace(event.WorkspaceID))
	event.AgentID = strings.ToLower(strings.TrimSpace(event.AgentID))
	event.ConversationID = strings.ToLower(strings.TrimSpace(event.ConversationID))
	event.RunID = strings.ToLower(strings.TrimSpace(event.RunID))
	event.Type = strings.TrimSpace(event.Type)
	event.SpecVersion = strings.TrimSpace(event.SpecVersion)
	event.TraceID = strings.TrimSpace(event.TraceID)
	event.ItemID = strings.ToLower(strings.TrimSpace(event.ItemID))
	event.InteractionID = strings.ToLower(strings.TrimSpace(event.InteractionID))
	event.OccurredAt = event.OccurredAt.UTC()

	for _, value := range []string{
		event.ID, event.EventStreamID, event.WorkspaceID,
		event.AgentID, event.ConversationID, event.RunID,
	} {
		if _, err := uuid.Parse(value); err != nil {
			return NewProtocolEvent{}, ErrAppendInvalid
		}
	}
	for _, value := range []string{event.ItemID, event.InteractionID} {
		if value != "" {
			if _, err := uuid.Parse(value); err != nil {
				return NewProtocolEvent{}, ErrAppendInvalid
			}
		}
	}
	if !appendEventTypePattern.MatchString(event.Type) || event.Type == "stream.error" ||
		event.SpecVersion == "" || len(event.SpecVersion) > 32 || event.TraceID == "" ||
		event.OccurredAt.IsZero() {
		return NewProtocolEvent{}, ErrAppendInvalid
	}
	data, err := canonicalAppendObject(event.Data)
	if err != nil {
		return NewProtocolEvent{}, ErrAppendInvalid
	}
	event.Data = data
	return event, nil
}

func sameAppendScope(left, right NewProtocolEvent) bool {
	return left.EventStreamID == right.EventStreamID &&
		left.WorkspaceID == right.WorkspaceID && left.AgentID == right.AgentID &&
		left.ConversationID == right.ConversationID && left.RunID == right.RunID
}

func canonicalAppendObject(raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, ErrAppendInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, ErrAppendInvalid
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, ErrAppendInvalid
	}
	return canonical, nil
}

func buildEnvelope(event NewProtocolEvent, sequence int64) (json.RawMessage, error) {
	value := struct {
		SpecVersion    string          `json:"specVersion"`
		Type           string          `json:"type"`
		EventID        string          `json:"eventId"`
		StreamID       string          `json:"streamId"`
		Sequence       int64           `json:"sequence"`
		OccurredAt     time.Time       `json:"occurredAt"`
		WorkspaceID    string          `json:"workspaceId"`
		AgentID        string          `json:"agentId"`
		ConversationID string          `json:"conversationId"`
		RunID          string          `json:"runId"`
		TraceID        string          `json:"traceId"`
		Data           json.RawMessage `json:"data"`
	}{
		SpecVersion: event.SpecVersion, Type: event.Type, EventID: event.ID,
		StreamID: "run:" + event.RunID, Sequence: sequence, OccurredAt: event.OccurredAt,
		WorkspaceID: event.WorkspaceID, AgentID: event.AgentID,
		ConversationID: event.ConversationID, RunID: event.RunID,
		TraceID: event.TraceID, Data: event.Data,
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal protocol event envelope: %w", err)
	}
	return raw, nil
}

func nullableAppendID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func mapAppendError(operation string, err error) error {
	var databaseError *pq.Error
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23505":
			return fmt.Errorf("%s: %w", operation, ErrEventConflict)
		case "23503":
			return fmt.Errorf("%s: %w", operation, ErrEventStreamNotFound)
		case "23514", "22P02":
			return fmt.Errorf("%s: %w", operation, ErrAppendInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
