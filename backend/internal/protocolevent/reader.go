package protocolevent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrReadInvalid      = errors.New("protocol event read is invalid")
	ErrRunScopeNotFound = errors.New("protocol event run scope was not found")
)

type RunScope struct {
	WorkspaceID    string
	AgentID        string
	ConversationID string
	RunID          string
}

type EventReader struct{ db *sql.DB }

func NewEventReader(db *sql.DB) (*EventReader, error) {
	if db == nil {
		return nil, ErrReadInvalid
	}
	return &EventReader{db: db}, nil
}

func (reader *EventReader) ReadRunAfter(
	ctx context.Context,
	scope RunScope,
	after int64,
	limit int,
) ([]ProtocolEvent, error) {
	if reader == nil || reader.db == nil || after < 0 || limit < 1 || limit > 500 {
		return nil, ErrReadInvalid
	}
	scope, err := normalizeRunScope(scope)
	if err != nil {
		return nil, err
	}
	streamID, err := reader.resolveStream(ctx, scope)
	if err != nil {
		return nil, err
	}
	rows, err := reader.db.QueryContext(ctx, `
		SELECT global_position,id,stream_id,sequence_no,event_type,spec_version,
		       item_id,interaction_id,occurred_at,payload,
		       payload->>'traceId',payload->'data'
		FROM protocol_events
		WHERE workspace_id=$1 AND agent_id=$2 AND conversation_id=$3 AND run_id=$4
		  AND stream_id=$5 AND sequence_no>$6
		ORDER BY sequence_no
		LIMIT $7
	`, scope.WorkspaceID, scope.AgentID, scope.ConversationID, scope.RunID,
		streamID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("read protocol events: %w", err)
	}
	defer rows.Close()
	events := make([]ProtocolEvent, 0, limit)
	for rows.Next() {
		event, err := scanProtocolEvent(rows, scope)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate protocol events: %w", err)
	}
	return events, nil
}

func (reader *EventReader) HighWatermark(ctx context.Context, scope RunScope) (int64, error) {
	if reader == nil || reader.db == nil {
		return 0, ErrReadInvalid
	}
	scope, err := normalizeRunScope(scope)
	if err != nil {
		return 0, err
	}
	var nextSequence int64
	err = reader.db.QueryRowContext(ctx, `
		SELECT next_sequence
		FROM protocol_event_streams
		WHERE workspace_id=$1 AND agent_id=$2 AND conversation_id=$3 AND run_id=$4
	`, scope.WorkspaceID, scope.AgentID, scope.ConversationID, scope.RunID).Scan(&nextSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrRunScopeNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read protocol event high watermark: %w", err)
	}
	return nextSequence - 1, nil
}

func (reader *EventReader) resolveStream(ctx context.Context, scope RunScope) (string, error) {
	var streamID string
	err := reader.db.QueryRowContext(ctx, `
		SELECT id FROM protocol_event_streams
		WHERE workspace_id=$1 AND agent_id=$2 AND conversation_id=$3 AND run_id=$4
	`, scope.WorkspaceID, scope.AgentID, scope.ConversationID, scope.RunID).Scan(&streamID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrRunScopeNotFound
	}
	if err != nil {
		return "", fmt.Errorf("resolve protocol event stream: %w", err)
	}
	return streamID, nil
}

func normalizeRunScope(scope RunScope) (RunScope, error) {
	scope.WorkspaceID = strings.ToLower(strings.TrimSpace(scope.WorkspaceID))
	scope.AgentID = strings.ToLower(strings.TrimSpace(scope.AgentID))
	scope.ConversationID = strings.ToLower(strings.TrimSpace(scope.ConversationID))
	scope.RunID = strings.ToLower(strings.TrimSpace(scope.RunID))
	for _, value := range []string{
		scope.WorkspaceID, scope.AgentID, scope.ConversationID, scope.RunID,
	} {
		if _, err := uuid.Parse(value); err != nil {
			return RunScope{}, ErrReadInvalid
		}
	}
	return scope, nil
}

type protocolEventScanner interface{ Scan(...any) error }

func scanProtocolEvent(scanner protocolEventScanner, scope RunScope) (ProtocolEvent, error) {
	var event ProtocolEvent
	var itemID, interactionID sql.NullString
	var payload, data []byte
	err := scanner.Scan(
		&event.GlobalPosition, &event.ID, &event.EventStreamID, &event.Sequence,
		&event.Type, &event.SpecVersion, &itemID, &interactionID, &event.OccurredAt,
		&payload, &event.TraceID, &data,
	)
	if err != nil {
		return ProtocolEvent{}, fmt.Errorf("scan protocol event: %w", err)
	}
	event.StreamID = "run:" + scope.RunID
	event.WorkspaceID, event.AgentID = scope.WorkspaceID, scope.AgentID
	event.ConversationID, event.RunID = scope.ConversationID, scope.RunID
	event.ItemID, event.InteractionID = itemID.String, interactionID.String
	event.Payload = append(json.RawMessage(nil), payload...)
	event.Data = append(json.RawMessage(nil), data...)
	return event, nil
}
