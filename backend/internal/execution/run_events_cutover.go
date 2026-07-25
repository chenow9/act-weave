package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

type legacyProtocolRun struct {
	AgentID     string
	SessionID   string
	TraceID     string
	Status      string
	TriggerType string
	StartedAt   time.Time
	FinishedAt  sql.NullTime
}

func (r *RunEventRepository) protocolEventCutoverActive(ctx context.Context) (bool, error) {
	var active bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pg_trigger
			WHERE tgrelid='public.run_events'::regclass
			  AND tgname='run_events_cutover_complete' AND NOT tgisinternal
		)
	`).Scan(&active)
	if err != nil {
		return false, fmt.Errorf("detect protocol event cutover: %w", err)
	}
	return active, nil
}

func (r *RunEventRepository) appendProtocolEvent(
	ctx context.Context,
	input AppendRunEventInput,
	legacyPayload json.RawMessage,
) (RunEvent, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return RunEvent{}, fmt.Errorf("begin legacy protocol event append: %w", err)
	}
	defer tx.Rollback()

	var run legacyProtocolRun
	var sessionID sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT agent_id,session_id,trace_id,status,trigger_type,started_at,finished_at
		FROM agent_runs
		WHERE workspace_id=$1 AND id=$2
		FOR UPDATE
	`, input.WorkspaceID, input.RunID).Scan(
		&run.AgentID, &sessionID, &run.TraceID, &run.Status, &run.TriggerType,
		&run.StartedAt, &run.FinishedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RunEvent{}, ErrRunNotFound
	}
	if err != nil {
		return RunEvent{}, fmt.Errorf("lock run for protocol cutover append: %w", err)
	}
	if !sessionID.Valid {
		return RunEvent{}, ErrRunInvalid
	}
	run.SessionID = sessionID.String
	if !legacyEventMatchesRunState(input.EventType, run.Status) {
		return RunEvent{}, ErrRunConflict
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO protocol_event_streams(
		 id,workspace_id,agent_id,conversation_id,run_id
		) VALUES($1,$2,$3,$4,$1)
		ON CONFLICT (workspace_id,run_id) DO NOTHING
	`, input.RunID, input.WorkspaceID, run.AgentID, run.SessionID); err != nil {
		return RunEvent{}, mapRunEventWrite("ensure protocol event stream", err)
	}
	var streamID string
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM protocol_event_streams
		WHERE workspace_id=$1 AND agent_id=$2 AND conversation_id=$3 AND run_id=$4
	`, input.WorkspaceID, run.AgentID, run.SessionID, input.RunID).Scan(&streamID); err != nil {
		return RunEvent{}, mapRunEventWrite("read protocol event stream", err)
	}
	if terminalRunEvent(input.EventType) {
		var terminalExists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM protocol_events
				WHERE stream_id=$1 AND event_type IN (
					'run.completed','run.failed','run.cancelled'
				)
			)
		`, streamID).Scan(&terminalExists); err != nil {
			return RunEvent{}, fmt.Errorf("check protocol terminal event: %w", err)
		}
		if terminalExists {
			return RunEvent{}, ErrRunConflict
		}
	}

	data, itemID, err := buildLegacyProtocolData(input, legacyPayload, run)
	if err != nil {
		return RunEvent{}, ErrRunInvalid
	}
	occurredAt := time.Now().UTC()
	appended, err := protocolevent.NewEventAppender().AppendInTx(ctx, tx, []protocolevent.NewProtocolEvent{{
		ID: input.ID, EventStreamID: streamID,
		WorkspaceID: input.WorkspaceID, AgentID: run.AgentID,
		ConversationID: run.SessionID, RunID: input.RunID,
		Type: legacyProtocolEventType(input.EventType), SpecVersion: "1.0",
		TraceID: run.TraceID, ItemID: itemID, OccurredAt: occurredAt, Data: data,
	}})
	if err != nil {
		return RunEvent{}, mapProtocolAppendError("append legacy protocol event", err)
	}
	if err := tx.Commit(); err != nil {
		return RunEvent{}, mapRunEventWrite("commit legacy protocol event", err)
	}
	return RunEvent{
		ID: input.ID, WorkspaceID: input.WorkspaceID, RunID: input.RunID,
		SequenceNo: appended[0].Sequence, EventType: input.EventType,
		Payload:  append(json.RawMessage(nil), legacyPayload...),
		Terminal: terminalRunEvent(input.EventType), CreatedAt: occurredAt,
	}, nil
}

func (r *RunEventRepository) listProtocolEvents(
	ctx context.Context,
	workspaceID, sessionID, runID string,
	afterSequence int64,
	limit int,
) ([]RunEvent, error) {
	var scope protocolevent.RunScope
	var actualSession string
	err := r.db.QueryRowContext(ctx, `
		SELECT workspace_id,agent_id,session_id,id
		FROM agent_runs
		WHERE workspace_id=$1 AND id=$2
		  AND ($3='' OR session_id=$3::UUID)
	`, workspaceID, runID, sessionID).Scan(
		&scope.WorkspaceID, &scope.AgentID, &actualSession, &scope.RunID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("resolve legacy replay scope: %w", err)
	}
	scope.ConversationID = actualSession
	reader, err := protocolevent.NewEventReader(r.db)
	if err != nil {
		return nil, fmt.Errorf("create protocol event reader: %w", err)
	}
	events, err := reader.ReadRunAfter(ctx, scope, afterSequence, limit)
	if errors.Is(err, protocolevent.ErrRunScopeNotFound) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read legacy protocol events: %w", err)
	}
	result := make([]RunEvent, 0, len(events))
	for _, event := range events {
		legacy, ok := protocolEventAsLegacy(event)
		if ok {
			result = append(result, legacy)
		}
	}
	return result, nil
}

func buildLegacyProtocolData(
	input AppendRunEventInput,
	legacyPayload json.RawMessage,
	run legacyProtocolRun,
) (json.RawMessage, string, error) {
	var payload map[string]any
	if err := json.Unmarshal(legacyPayload, &payload); err != nil || payload == nil {
		return nil, "", ErrRunInvalid
	}
	data := map[string]any{
		"legacyEventType": input.EventType,
		"legacyPayload":   payload,
	}
	itemID := ""
	if strings.HasPrefix(input.EventType, "STEP_") {
		itemID = stringValue(payload["stepId"])
		if _, err := uuid.Parse(itemID); err != nil {
			itemID = input.ID
		}
		status := "in_progress"
		if input.EventType == "STEP_COMPLETED" {
			status = "completed"
		}
		data["item"] = map[string]any{
			"id": itemID, "type": "notice", "status": status,
			"code":    "LEGACY_" + input.EventType,
			"message": "Imported legacy execution step event",
		}
	} else {
		status := map[string]string{
			"RUN_STARTED": "running", "RUN_WAITING_CONFIRMATION": "waiting_interaction",
			"RUN_RESUMED": "running", "RUN_COMPLETED": "completed",
			"RUN_FAILED": "failed", "RUN_CANCELLED": "cancelled",
		}[input.EventType]
		runSnapshot := map[string]any{
			"id": input.RunID, "conversationId": run.SessionID, "agentId": run.AgentID,
			"status": status, "trigger": legacyProtocolTrigger(run.TriggerType),
			"startedAt": run.StartedAt,
		}
		if terminalRunEvent(input.EventType) {
			finishedAt := time.Now().UTC()
			if run.FinishedAt.Valid {
				finishedAt = run.FinishedAt.Time.UTC()
			}
			runSnapshot["completedAt"] = finishedAt
		}
		data["run"] = runSnapshot
		interactionID := stringValue(payload["chatConfirmationId"])
		if _, err := uuid.Parse(interactionID); err != nil {
			interactionID = input.ID
		}
		if input.EventType == "RUN_WAITING_CONFIRMATION" {
			data["interactionIds"] = []string{interactionID}
		}
		if input.EventType == "RUN_RESUMED" {
			data["interactionId"] = interactionID
		}
	}
	raw, err := json.Marshal(data)
	return raw, itemID, err
}

func legacyEventMatchesRunState(eventType, status string) bool {
	status = strings.ToUpper(strings.TrimSpace(status))
	if status == "SUCCEEDED" {
		return eventType == "RUN_COMPLETED"
	}
	if status == "FAILED" {
		return eventType == "RUN_FAILED"
	}
	if status == "CANCELLED" {
		return eventType == "RUN_CANCELLED"
	}
	return !terminalRunEvent(eventType)
}

func legacyProtocolEventType(value string) string {
	return map[string]string{
		"RUN_STARTED": "run.started", "STEP_STARTED": "item.started",
		"STEP_COMPLETED": "item.completed", "RUN_WAITING_CONFIRMATION": "run.waiting",
		"RUN_RESUMED": "run.resumed", "RUN_COMPLETED": "run.completed",
		"RUN_FAILED": "run.failed", "RUN_CANCELLED": "run.cancelled",
	}[value]
}

func legacyProtocolTrigger(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "CHAT":
		return "message"
	case "WORKFLOW":
		return "workflow"
	case "API":
		return "api"
	default:
		return "system"
	}
}

func protocolEventAsLegacy(event protocolevent.ProtocolEvent) (RunEvent, bool) {
	var data map[string]json.RawMessage
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return RunEvent{}, false
	}
	var eventType string
	_ = json.Unmarshal(data["legacyEventType"], &eventType)
	if !validRunEventType(eventType) {
		eventType = map[string]string{
			"run.accepted": "RUN_STARTED", "run.started": "RUN_STARTED",
			"item.started": "STEP_STARTED", "item.completed": "STEP_COMPLETED",
			"run.waiting": "RUN_WAITING_CONFIRMATION", "run.resumed": "RUN_RESUMED",
			"run.completed": "RUN_COMPLETED", "run.failed": "RUN_FAILED",
			"run.cancelled": "RUN_CANCELLED",
		}[event.Type]
	}
	if !validRunEventType(eventType) {
		return RunEvent{}, false
	}
	payload := data["legacyPayload"]
	if len(payload) == 0 {
		payload = event.Data
	}
	return RunEvent{
		ID: event.ID, WorkspaceID: event.WorkspaceID, RunID: event.RunID,
		SequenceNo: event.Sequence, EventType: eventType,
		Payload:  append(json.RawMessage(nil), payload...),
		Terminal: terminalRunEvent(eventType), CreatedAt: event.OccurredAt,
	}, true
}

func mapProtocolAppendError(operation string, err error) error {
	switch {
	case errors.Is(err, protocolevent.ErrAppendInvalid):
		return fmt.Errorf("%s: %w", operation, ErrRunInvalid)
	case errors.Is(err, protocolevent.ErrEventConflict),
		errors.Is(err, protocolevent.ErrEventStreamNotFound):
		return fmt.Errorf("%s: %w", operation, ErrRunConflict)
	default:
		return fmt.Errorf("%s: %w", operation, err)
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}
