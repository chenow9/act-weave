package audit

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/lib/pq"
)

type OutboxEvent struct {
	ID             string
	WorkspaceID    string
	AggregateType  string
	AggregateID    string
	EventType      string
	Payload        json.RawMessage
	SchemaVersion  string
	IdempotencyKey string
	OccurredAt     time.Time
	AvailableAt    time.Time
	PublishedAt    *time.Time
	Attempts       int
	LastError      string
	CreatedAt      time.Time
}

type AppendOutboxInput struct {
	ID             string
	WorkspaceID    string
	AggregateType  string
	AggregateID    string
	EventType      string
	Payload        json.RawMessage
	SchemaVersion  string
	IdempotencyKey string
	OccurredAt     time.Time
	AvailableAt    time.Time
}

type AppendOutboxResult struct {
	Event   OutboxEvent
	Created bool
}

type ListOutboxInput struct {
	WorkspaceID   string
	AggregateType string
	AggregateID   string
	EventType     string
	Limit         int
}

type OutboxRepository struct{ db *sql.DB }

func NewOutboxRepository(db *sql.DB) (*OutboxRepository, error) {
	if db == nil {
		return nil, errors.New("outbox repository database is required")
	}
	return &OutboxRepository{db: db}, nil
}

func (repository *OutboxRepository) Append(
	ctx context.Context,
	input AppendOutboxInput,
) (AppendOutboxResult, error) {
	input, err := normalizeOutboxInput(input)
	if err != nil {
		return AppendOutboxResult{}, err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return AppendOutboxResult{}, fmt.Errorf("begin append outbox transaction: %w", err)
	}
	defer tx.Rollback()
	result, err := repository.appendInTransaction(ctx, tx, input)
	if err != nil {
		return AppendOutboxResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AppendOutboxResult{}, mapOutboxWrite("commit outbox event", err)
	}
	return result, nil
}

func (repository *OutboxRepository) AppendInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	input AppendOutboxInput,
) (AppendOutboxResult, error) {
	if tx == nil {
		return AppendOutboxResult{}, ErrInvalid
	}
	input, err := normalizeOutboxInput(input)
	if err != nil {
		return AppendOutboxResult{}, err
	}
	return repository.appendInTransaction(ctx, tx, input)
}

func (repository *OutboxRepository) appendInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	input AppendOutboxInput,
) (AppendOutboxResult, error) {
	value, err := scanOutboxEvent(tx.QueryRowContext(ctx, `
		INSERT INTO outbox_events(
		 id,workspace_id,aggregate_type,aggregate_id,event_type,payload,
		 schema_version,idempotency_key,occurred_at,available_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id,workspace_id,aggregate_type,aggregate_id,event_type,payload,
		 schema_version,idempotency_key,occurred_at,available_at,published_at,
		 attempts,last_error,created_at
	`, input.ID, nullableAuditString(input.WorkspaceID), input.AggregateType,
		input.AggregateID, input.EventType, []byte(input.Payload), input.SchemaVersion,
		input.IdempotencyKey, input.OccurredAt, input.AvailableAt))
	if err == nil {
		return AppendOutboxResult{Event: value, Created: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AppendOutboxResult{}, mapOutboxWrite("append outbox event", err)
	}
	existing, err := scanOutboxEvent(tx.QueryRowContext(ctx, `
		SELECT id,workspace_id,aggregate_type,aggregate_id,event_type,payload,
		 schema_version,idempotency_key,occurred_at,available_at,published_at,
		 attempts,last_error,created_at
		FROM outbox_events WHERE idempotency_key=$1
	`, input.IdempotencyKey))
	if err != nil {
		return AppendOutboxResult{}, mapOutboxWrite("read idempotent outbox event", err)
	}
	if !sameOutboxContract(existing, input) {
		return AppendOutboxResult{}, ErrConflict
	}
	return AppendOutboxResult{Event: existing, Created: false}, nil
}

func (repository *OutboxRepository) ListByAggregate(
	ctx context.Context,
	input ListOutboxInput,
) ([]OutboxEvent, error) {
	input = normalizeListOutbox(input)
	if !validAuditUUID(input.WorkspaceID) || !resourceTypePattern.MatchString(input.AggregateType) ||
		!validAuditUUID(input.AggregateID) || input.Limit < 1 || input.Limit > 1000 {
		return nil, ErrInvalid
	}
	return listOutboxRows(ctx, repository.db, `
		SELECT id,workspace_id,aggregate_type,aggregate_id,event_type,payload,
		 schema_version,idempotency_key,occurred_at,available_at,published_at,
		 attempts,last_error,created_at
		FROM outbox_events
		WHERE workspace_id=$1 AND aggregate_type=$2 AND aggregate_id=$3
		ORDER BY occurred_at,id LIMIT $4
	`, input.WorkspaceID, input.AggregateType, input.AggregateID, input.Limit)
}

func (repository *OutboxRepository) ListByEventType(
	ctx context.Context,
	input ListOutboxInput,
) ([]OutboxEvent, error) {
	input = normalizeListOutbox(input)
	if !validAuditUUID(input.WorkspaceID) || !actionPattern.MatchString(input.EventType) ||
		input.Limit < 1 || input.Limit > 1000 {
		return nil, ErrInvalid
	}
	return listOutboxRows(ctx, repository.db, `
		SELECT id,workspace_id,aggregate_type,aggregate_id,event_type,payload,
		 schema_version,idempotency_key,occurred_at,available_at,published_at,
		 attempts,last_error,created_at
		FROM outbox_events
		WHERE workspace_id=$1 AND event_type=$2
		ORDER BY occurred_at,id LIMIT $3
	`, input.WorkspaceID, input.EventType, input.Limit)
}

type outboxQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listOutboxRows(ctx context.Context, queryer outboxQueryer, query string, arguments ...any) ([]OutboxEvent, error) {
	rows, err := queryer.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list outbox events: %w", err)
	}
	defer rows.Close()
	values := make([]OutboxEvent, 0)
	for rows.Next() {
		value, err := scanOutboxEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func normalizeOutboxInput(input AppendOutboxInput) (AppendOutboxInput, error) {
	input.ID, input.WorkspaceID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID)
	input.AggregateType = strings.ToUpper(strings.TrimSpace(input.AggregateType))
	input.AggregateID = strings.TrimSpace(input.AggregateID)
	input.EventType = strings.ToLower(strings.TrimSpace(input.EventType))
	input.SchemaVersion = strings.ToLower(strings.TrimSpace(input.SchemaVersion))
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.OccurredAt.IsZero() {
		input.OccurredAt = time.Now().UTC()
	} else {
		input.OccurredAt = input.OccurredAt.UTC()
	}
	input.OccurredAt = input.OccurredAt.Truncate(time.Microsecond)
	if input.AvailableAt.IsZero() {
		input.AvailableAt = input.OccurredAt
	} else {
		input.AvailableAt = input.AvailableAt.UTC()
	}
	input.AvailableAt = input.AvailableAt.Truncate(time.Microsecond)
	canonical, err := canonicalOutboxPayload(input.Payload, input.SchemaVersion)
	if err != nil || !validOutboxInput(input) {
		return AppendOutboxInput{}, ErrInvalid
	}
	input.Payload = canonical
	return input, nil
}

func validOutboxInput(input AppendOutboxInput) bool {
	return validAuditUUID(input.ID) &&
		(input.WorkspaceID == "" || validAuditUUID(input.WorkspaceID)) &&
		resourceTypePattern.MatchString(input.AggregateType) && validAuditUUID(input.AggregateID) &&
		actionPattern.MatchString(input.EventType) && stableVersionPattern.MatchString(input.SchemaVersion) &&
		len(input.IdempotencyKey) >= 1 && len(input.IdempotencyKey) <= 255 &&
		!input.AvailableAt.Before(input.OccurredAt)
}

func canonicalOutboxPayload(raw json.RawMessage, schemaVersion string) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, ErrInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, ErrInvalid
	}
	value, exists := object["schemaVersion"].(string)
	if !exists || value != schemaVersion {
		return nil, ErrInvalid
	}
	return json.Marshal(object)
}

func sameOutboxContract(value OutboxEvent, input AppendOutboxInput) bool {
	return value.WorkspaceID == input.WorkspaceID && value.AggregateType == input.AggregateType &&
		value.AggregateID == input.AggregateID && value.EventType == input.EventType &&
		value.SchemaVersion == input.SchemaVersion && value.IdempotencyKey == input.IdempotencyKey &&
		jsonPayloadEqual(value.Payload, input.Payload) && value.OccurredAt.Equal(input.OccurredAt) &&
		value.AvailableAt.Equal(input.AvailableAt)
}

func jsonPayloadEqual(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	leftDecoder := json.NewDecoder(bytes.NewReader(left))
	leftDecoder.UseNumber()
	rightDecoder := json.NewDecoder(bytes.NewReader(right))
	rightDecoder.UseNumber()
	if leftDecoder.Decode(&leftValue) != nil || rightDecoder.Decode(&rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func normalizeListOutbox(input ListOutboxInput) ListOutboxInput {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.AggregateType = strings.ToUpper(strings.TrimSpace(input.AggregateType))
	input.AggregateID = strings.TrimSpace(input.AggregateID)
	input.EventType = strings.ToLower(strings.TrimSpace(input.EventType))
	if input.Limit == 0 {
		input.Limit = 100
	}
	return input
}

type outboxScanner interface{ Scan(...any) error }

func scanOutboxEvent(scanner outboxScanner) (OutboxEvent, error) {
	var event OutboxEvent
	var workspaceID, lastError sql.NullString
	var publishedAt sql.NullTime
	var payload []byte
	err := scanner.Scan(
		&event.ID, &workspaceID, &event.AggregateType, &event.AggregateID,
		&event.EventType, &payload, &event.SchemaVersion, &event.IdempotencyKey,
		&event.OccurredAt, &event.AvailableAt, &publishedAt, &event.Attempts,
		&lastError, &event.CreatedAt,
	)
	if err != nil {
		return OutboxEvent{}, err
	}
	event.WorkspaceID, event.LastError = workspaceID.String, lastError.String
	event.Payload = append([]byte(nil), payload...)
	if publishedAt.Valid {
		published := publishedAt.Time
		event.PublishedAt = &published
	}
	return event, nil
}

func mapOutboxWrite(operation string, err error) error {
	if err == nil {
		return nil
	}
	var pqError *pq.Error
	if errors.As(err, &pqError) {
		switch pqError.Code {
		case "23505", "40001":
			return fmt.Errorf("%s (%s): %w", operation, pqError.Constraint, ErrConflict)
		case "23503", "23514", "22P02":
			return fmt.Errorf("%s (%s): %w", operation, pqError.Constraint, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
