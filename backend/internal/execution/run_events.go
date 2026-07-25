package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type RunEvent struct {
	ID          string
	WorkspaceID string
	RunID       string
	SequenceNo  int64
	EventType   string
	Payload     json.RawMessage
	Terminal    bool
	CreatedAt   time.Time
}

type AppendRunEventInput struct {
	ID          string
	WorkspaceID string
	RunID       string
	EventType   string
	Payload     json.RawMessage
}

type RunEventRepository struct{ db *sql.DB }

func NewRunEventRepository(db *sql.DB) (*RunEventRepository, error) {
	if db == nil {
		return nil, errors.New("run event repository database is required")
	}
	return &RunEventRepository{db: db}, nil
}

func (r *RunEventRepository) Append(
	ctx context.Context,
	input AppendRunEventInput,
) (RunEvent, error) {
	input.ID, input.WorkspaceID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID)
	input.RunID, input.EventType = strings.TrimSpace(input.RunID), strings.TrimSpace(input.EventType)
	payload, err := canonicalRunObject(input.Payload)
	if err != nil || !invocationValidUUID(input.ID) || !invocationValidUUID(input.WorkspaceID) ||
		!invocationValidUUID(input.RunID) || !validRunEventType(input.EventType) {
		return RunEvent{}, ErrRunInvalid
	}
	if cutover, cutoverErr := r.protocolEventCutoverActive(ctx); cutoverErr != nil {
		return RunEvent{}, cutoverErr
	} else if cutover {
		return r.appendProtocolEvent(ctx, input, json.RawMessage(payload))
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return RunEvent{}, fmt.Errorf("begin append run event: %w", err)
	}
	defer tx.Rollback()
	var ignored string
	if err := tx.QueryRowContext(ctx, `
		SELECT status FROM agent_runs WHERE workspace_id=$1 AND id=$2 FOR UPDATE
	`, input.WorkspaceID, input.RunID).Scan(&ignored); errors.Is(err, sql.ErrNoRows) {
		return RunEvent{}, ErrRunNotFound
	} else if err != nil {
		return RunEvent{}, fmt.Errorf("lock run for event append: %w", err)
	}
	var sequence int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence_no),0)+1 FROM run_events
		WHERE workspace_id=$1 AND run_id=$2
	`, input.WorkspaceID, input.RunID).Scan(&sequence); err != nil {
		return RunEvent{}, fmt.Errorf("allocate run event sequence: %w", err)
	}
	value, err := scanRunEvent(tx.QueryRowContext(ctx, `
		INSERT INTO run_events AS re(
		 id,workspace_id,run_id,sequence_no,event_type,payload,terminal
		) VALUES($1,$2,$3,$4,$5,$6,$7)
		RETURNING re.id,re.workspace_id,re.run_id,re.sequence_no,re.event_type,
		 re.payload,re.terminal,re.created_at
	`, input.ID, input.WorkspaceID, input.RunID, sequence, input.EventType,
		[]byte(payload), terminalRunEvent(input.EventType)))
	if err != nil {
		return RunEvent{}, mapRunEventWrite("append run event", err)
	}
	if err := tx.Commit(); err != nil {
		return RunEvent{}, mapRunEventWrite("commit run event", err)
	}
	return value, nil
}

func (r *RunEventRepository) ListAfter(
	ctx context.Context,
	workspaceID, runID string,
	afterSequence int64,
	limit int,
) ([]RunEvent, error) {
	return r.listAfter(ctx, workspaceID, "", runID, afterSequence, limit)
}

func (r *RunEventRepository) ListForSessionAfter(
	ctx context.Context,
	workspaceID, sessionID, runID string,
	afterSequence int64,
	limit int,
) ([]RunEvent, error) {
	return r.listAfter(ctx, workspaceID, sessionID, runID, afterSequence, limit)
}

func (r *RunEventRepository) listAfter(
	ctx context.Context,
	workspaceID, sessionID, runID string,
	afterSequence int64,
	limit int,
) ([]RunEvent, error) {
	workspaceID, sessionID = strings.TrimSpace(workspaceID), strings.TrimSpace(sessionID)
	runID = strings.TrimSpace(runID)
	if !invocationValidUUID(workspaceID) || !invocationValidUUID(runID) ||
		(sessionID != "" && !invocationValidUUID(sessionID)) || afterSequence < 0 {
		return nil, ErrRunInvalid
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if cutover, cutoverErr := r.protocolEventCutoverActive(ctx); cutoverErr != nil {
		return nil, cutoverErr
	} else if cutover {
		return r.listProtocolEvents(ctx, workspaceID, sessionID, runID, afterSequence, limit)
	}
	var exists bool
	if sessionID == "" {
		if err := r.db.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM agent_runs WHERE workspace_id=$1 AND id=$2)
		`, workspaceID, runID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("verify run event scope: %w", err)
		}
	} else if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM agent_runs
		 WHERE workspace_id=$1 AND session_id=$2 AND id=$3)
	`, workspaceID, sessionID, runID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("verify run event session scope: %w", err)
	}
	if !exists {
		return nil, ErrRunNotFound
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT re.id,re.workspace_id,re.run_id,re.sequence_no,re.event_type,
		 re.payload,re.terminal,re.created_at
		FROM run_events re
		WHERE re.workspace_id=$1 AND re.run_id=$2 AND re.sequence_no>$3
		ORDER BY re.sequence_no,re.id LIMIT $4
	`, workspaceID, runID, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}
	defer rows.Close()
	values := make([]RunEvent, 0)
	for rows.Next() {
		value, err := scanRunEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan run event: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func WriteRunEventSSE(writer io.Writer, event RunEvent) error {
	if writer == nil || event.SequenceNo <= 0 || !validRunEventType(event.EventType) {
		return ErrRunInvalid
	}
	payload, err := canonicalRunObject(event.Payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "id: %d\nevent: %s\ndata: %s\n\n",
		event.SequenceNo, event.EventType, payload)
	return err
}

func validRunEventType(value string) bool {
	switch value {
	case "RUN_STARTED", "STEP_STARTED", "STEP_COMPLETED",
		"RUN_WAITING_CONFIRMATION", "RUN_RESUMED",
		"RUN_COMPLETED", "RUN_FAILED", "RUN_CANCELLED":
		return true
	default:
		return false
	}
}

func terminalRunEvent(value string) bool {
	return value == "RUN_COMPLETED" || value == "RUN_FAILED" || value == "RUN_CANCELLED"
}

func scanRunEvent(row runScanner) (RunEvent, error) {
	var value RunEvent
	if err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.RunID, &value.SequenceNo,
		&value.EventType, &value.Payload, &value.Terminal, &value.CreatedAt,
	); err != nil {
		return RunEvent{}, err
	}
	return value, nil
}

func mapRunEventWrite(operation string, err error) error {
	if err == nil {
		return nil
	}
	var pqError interface{ SQLState() string }
	if errors.As(err, &pqError) {
		switch pqError.SQLState() {
		case "23505", "23514", "40001", "55000":
			return fmt.Errorf("%s: %w", operation, ErrRunConflict)
		case "23502", "23503", "22P02":
			return fmt.Errorf("%s: %w", operation, ErrRunInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
