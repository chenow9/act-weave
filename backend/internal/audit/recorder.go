package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ActionAuthenticationLogin       = "authentication.login"
	ActionAuthorizationDenied       = "authorization.denied"
	ActionWorkspaceMemberChanged    = "workspace.member.changed"
	ActionAgentChanged              = "agent.changed"
	ActionConfigurationChanged      = "configuration.changed"
	ActionToolReleasePublished      = "tool.release.published"
	ActionWorkflowReleasePublished  = "workflow.release.published"
	ActionWorkflowRevisionActivated = "workflow.revision.activated"
	ActionConfirmationRequested     = "execution.confirmation.requested"
	ActionConfirmationConfirmed     = "execution.confirmation.confirmed"
	ActionConfirmationCancelled     = "execution.confirmation.cancelled"
	ActionChatMessageSent           = "chat.message.sent"
	ActionRunCompleted              = "execution.run.completed"
	ActionRunFailed                 = "execution.run.failed"
	ActionAuditExportRequested      = "audit.export.requested"
	ActionAuditExportCompleted      = "audit.export.completed"
	ActionAuditExportFailed         = "audit.export.failed"
)

type ManagementEventInput struct {
	EventID           string
	OccurredAt        time.Time
	WorkspaceID       string
	ActorType         string
	ActorID           string
	ActorDisplay      string
	Action            string
	ResourceType      string
	ResourceID        string
	Result            string
	Before            map[string]any
	After             map[string]any
	Metadata          map[string]any
	PayloadObjectID   string
	OutboxEventType   string
	OutboxSchema      string
	OutboxPayload     map[string]any
	OutboxIdempotency string
}

type Recorder struct {
	events  *Repository
	outbox  *OutboxRepository
	builder *Builder
}

func NewRecorder(events *Repository, outbox *OutboxRepository, builder *Builder) (*Recorder, error) {
	if events == nil || outbox == nil || builder == nil {
		return nil, errors.New("audit recorder repositories and builder are required")
	}
	return &Recorder{events: events, outbox: outbox, builder: builder}, nil
}

func (recorder *Recorder) Record(ctx context.Context, input ManagementEventInput) (Event, error) {
	tx, err := recorder.events.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback()
	event, err := recorder.RecordInTransaction(ctx, tx, input)
	if err != nil {
		return Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (recorder *Recorder) RecordInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	input ManagementEventInput,
) (Event, error) {
	if tx == nil {
		return Event{}, ErrInvalid
	}
	request := requestContextFrom(ctx)
	event, err := recorder.builder.Build(BuildInput{
		ID: input.EventID, OccurredAt: input.OccurredAt, WorkspaceID: input.WorkspaceID,
		ActorType: input.ActorType, ActorID: input.ActorID, ActorDisplay: input.ActorDisplay,
		Action: input.Action, ResourceType: input.ResourceType, ResourceID: input.ResourceID,
		Result: input.Result, RequestID: request.RequestID, TraceID: request.TraceID,
		SourceIP: request.SourceIP, UserAgent: request.UserAgent,
		Before: input.Before, After: input.After, Metadata: input.Metadata,
		PayloadObjectID: input.PayloadObjectID,
	})
	if err != nil {
		return Event{}, err
	}
	created, err := recorder.events.InsertInTransaction(ctx, tx, event)
	if err != nil {
		return Event{}, err
	}
	outboxType := strings.TrimSpace(input.OutboxEventType)
	outboxSchema := strings.TrimSpace(input.OutboxSchema)
	payload := cloneAuditMap(input.OutboxPayload)
	if outboxType == "" {
		outboxType, outboxSchema = "audit.event.recorded", "audit.event.v1"
		payload = map[string]any{
			"schemaVersion": outboxSchema, "auditEventId": created.ID,
			"action": created.Action, "resourceType": created.ResourceType,
			"resourceId": created.ResourceID, "result": created.Result,
			"requestId": created.RequestID, "traceId": created.TraceID,
		}
	}
	if payload == nil {
		return Event{}, ErrInvalid
	}
	payload["schemaVersion"] = outboxSchema
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return Event{}, ErrInvalid
	}
	idempotencyKey := strings.TrimSpace(input.OutboxIdempotency)
	if idempotencyKey == "" {
		idempotencyKey = "audit:" + created.ID
	}
	_, err = recorder.outbox.AppendInTransaction(ctx, tx, AppendOutboxInput{
		ID: created.ID, WorkspaceID: created.WorkspaceID,
		AggregateType: created.ResourceType, AggregateID: aggregateID(created),
		EventType: outboxType, Payload: payloadJSON, SchemaVersion: outboxSchema,
		IdempotencyKey: idempotencyKey, OccurredAt: created.OccurredAt,
	})
	if err != nil {
		return Event{}, err
	}
	return created, nil
}

func aggregateID(event Event) string {
	if validAuditUUID(event.ResourceID) {
		return event.ResourceID
	}
	return event.ID
}

func cloneAuditMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]any, len(input)+1)
	for key, value := range input {
		output[key] = value
	}
	return output
}

func newAuditEventID() (string, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return value.String(), nil
}
