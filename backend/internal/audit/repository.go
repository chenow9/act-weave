package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("audit repository database is required")
	}
	return &Repository{db: db}, nil
}

// Insert is deliberately the only mutation exposed by Repository.
func (repository *Repository) Insert(ctx context.Context, event Event) (Event, error) {
	return repository.insertWith(ctx, repository.db, event)
}

func (repository *Repository) InsertInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	event Event,
) (Event, error) {
	if tx == nil {
		return Event{}, ErrInvalid
	}
	return repository.insertWith(ctx, tx, event)
}

type auditQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (repository *Repository) insertWith(
	ctx context.Context,
	queryer auditQueryRower,
	event Event,
) (Event, error) {
	if err := normalizeAndValidateEvent(&event); err != nil {
		return Event{}, err
	}
	return scanEvent(queryer.QueryRowContext(ctx, `
		INSERT INTO audit_events(
		 id,occurred_at,workspace_id,actor_type,actor_id,actor_display,action,
		 resource_type,resource_id,result,request_id,trace_id,source_ip,user_agent,
		 changes,metadata,payload_object_id,schema_version
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING id,occurred_at,workspace_id,actor_type,actor_id,actor_display,action,
		 resource_type,resource_id,result,request_id,trace_id,source_ip,user_agent,
		 changes,metadata,payload_object_id,schema_version
	`, event.ID, event.OccurredAt, nullableAuditString(event.WorkspaceID), event.ActorType,
		nullableAuditString(event.ActorID), event.ActorDisplay, event.Action, event.ResourceType,
		nullableAuditString(event.ResourceID), event.Result, nullableAuditString(event.RequestID),
		nullableAuditString(event.TraceID), nullableAuditIP(event), nullableAuditString(event.UserAgent),
		[]byte(event.Changes), []byte(event.Metadata), nullableAuditString(event.PayloadObjectID),
		event.SchemaVersion))
}

func normalizeAndValidateEvent(event *Event) error {
	event.ID, event.WorkspaceID = strings.TrimSpace(event.ID), strings.TrimSpace(event.WorkspaceID)
	event.ActorType, event.ActorID = strings.ToUpper(strings.TrimSpace(event.ActorType)), strings.TrimSpace(event.ActorID)
	event.ActorDisplay = strings.TrimSpace(event.ActorDisplay)
	event.Action, event.ResourceType = strings.ToLower(strings.TrimSpace(event.Action)), strings.ToUpper(strings.TrimSpace(event.ResourceType))
	event.ResourceID, event.Result = strings.TrimSpace(event.ResourceID), strings.ToUpper(strings.TrimSpace(event.Result))
	event.RequestID, event.TraceID = strings.TrimSpace(event.RequestID), strings.TrimSpace(event.TraceID)
	event.UserAgent, event.PayloadObjectID = strings.TrimSpace(event.UserAgent), strings.TrimSpace(event.PayloadObjectID)
	event.SchemaVersion = strings.ToLower(strings.TrimSpace(event.SchemaVersion))
	input := BuildInput{
		ID: event.ID, OccurredAt: event.OccurredAt, WorkspaceID: event.WorkspaceID,
		ActorType: event.ActorType, ActorID: event.ActorID, ActorDisplay: event.ActorDisplay,
		Action: event.Action, ResourceType: event.ResourceType, ResourceID: event.ResourceID,
		Result: event.Result, RequestID: event.RequestID, TraceID: event.TraceID,
		UserAgent: event.UserAgent, PayloadObjectID: event.PayloadObjectID,
	}
	if !validBuildInput(input) || validateEvent(*event) != nil {
		return ErrInvalid
	}
	return nil
}

type eventScanner interface{ Scan(...any) error }

func scanEvent(scanner eventScanner) (Event, error) {
	var event Event
	var workspaceID, actorID, resourceID, requestID, traceID sql.NullString
	var sourceIP, userAgent, payloadObjectID sql.NullString
	var changes, metadata []byte
	err := scanner.Scan(
		&event.ID, &event.OccurredAt, &workspaceID, &event.ActorType, &actorID,
		&event.ActorDisplay, &event.Action, &event.ResourceType, &resourceID, &event.Result,
		&requestID, &traceID, &sourceIP, &userAgent, &changes, &metadata,
		&payloadObjectID, &event.SchemaVersion,
	)
	if err != nil {
		return Event{}, mapAuditWrite("insert audit event", err)
	}
	event.WorkspaceID, event.ActorID, event.ResourceID = workspaceID.String, actorID.String, resourceID.String
	event.RequestID, event.TraceID, event.UserAgent = requestID.String, traceID.String, userAgent.String
	event.PayloadObjectID = payloadObjectID.String
	event.Changes, event.Metadata = append([]byte(nil), changes...), append([]byte(nil), metadata...)
	if sourceIP.Valid {
		event.SourceIP = event.SourceIP.Unmap()
		_ = event.SourceIP.UnmarshalText([]byte(sourceIP.String))
	}
	return event, nil
}

func nullableAuditString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableAuditIP(event Event) any {
	if !event.SourceIP.IsValid() {
		return nil
	}
	return event.SourceIP.String()
}

func mapAuditWrite(operation string, err error) error {
	if err == nil {
		return nil
	}
	var pqError *pq.Error
	if errors.As(err, &pqError) {
		switch pqError.Code {
		case "23505":
			return fmt.Errorf("%s (%s): %w", operation, pqError.Constraint, ErrConflict)
		case "23503", "23514", "22P02":
			return fmt.Errorf("%s (%s): %w", operation, pqError.Constraint, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
