package protocolevent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/metrics"
)

var (
	ErrProtocolUnitOfWorkInvalid  = errors.New("protocol unit of work is invalid")
	ErrProtocolUnitOfWorkNoEvents = errors.New("protocol unit of work did not append an event")
)

// CommittedEventRef is the only event information passed to live fanout. The
// persisted payload remains in the event store and is read through EventReader.
type CommittedEventRef struct {
	EventID        string
	EventStreamID  string
	WorkspaceID    string
	AgentID        string
	ConversationID string
	RunID          string
	Sequence       int64
	GlobalPosition int64
}

type CommitNotification struct {
	Events []CommittedEventRef
}

// CommitNotifier is invoked only after the database transaction commits. Its
// implementation may use network I/O because it never runs inside the unit of
// work transaction.
type CommitNotifier interface {
	NotifyCommitted(context.Context, CommitNotification) error
}

type UnitOfWorkResult struct {
	Events      []ProtocolEvent
	NotifyError error
}

// ProtocolWork must only perform transactional persistence. Network fanout and
// other external side effects belong in CommitNotifier, which is called after
// commit.
type ProtocolWork func(context.Context, *ProtocolTransaction) error

// ProtocolTransaction binds domain writes, Run Item projection writes, and
// Protocol Event appends to one caller-owned sql.Tx.
type ProtocolTransaction struct {
	tx       *sql.Tx
	items    *RunItemRepository
	appender *EventAppender
	events   []ProtocolEvent
	active   bool
}

func (transaction *ProtocolTransaction) SQLTx() (*sql.Tx, error) {
	if transaction == nil || transaction.tx == nil || !transaction.active {
		return nil, ErrProtocolUnitOfWorkInvalid
	}
	return transaction.tx, nil
}

func (transaction *ProtocolTransaction) EnsureRunEventStream(
	ctx context.Context,
	streamID string,
	scope RunScope,
) (string, error) {
	if transaction == nil || transaction.tx == nil || !transaction.active {
		return "", ErrProtocolUnitOfWorkInvalid
	}
	return EnsureRunEventStreamInTx(ctx, transaction.tx, streamID, scope)
}

// EnsureRunEventStreamInTx idempotently creates the protocol_event_streams row
// for a run scope. streamID is conventionally the run ID (matches lifecycle /
// AAP RecordStarted / native protocol recorder). Safe to call more than once
// with the same streamID; a conflicting stream ID for the same run returns
// ErrEventConflict. Callers that create agent_runs in the same transaction
// (for example Console SendMessage) should invoke this after the run insert so
// the stream FK resolves and HighWatermark can succeed before async Execute.
func EnsureRunEventStreamInTx(
	ctx context.Context,
	tx *sql.Tx,
	streamID string,
	scope RunScope,
) (string, error) {
	if tx == nil {
		return "", ErrProtocolUnitOfWorkInvalid
	}
	streamID = strings.ToLower(strings.TrimSpace(streamID))
	normalized, err := normalizeRunScope(scope)
	if err != nil || !modelUUID(streamID) {
		return "", ErrProtocolUnitOfWorkInvalid
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO protocol_event_streams(
		 id,workspace_id,agent_id,conversation_id,run_id
		) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (workspace_id,run_id) DO NOTHING
	`, streamID, normalized.WorkspaceID, normalized.AgentID,
		normalized.ConversationID, normalized.RunID); err != nil {
		return "", mapAppendError("ensure protocol event stream", err)
	}
	var actual string
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM protocol_event_streams
		WHERE workspace_id=$1 AND agent_id=$2 AND conversation_id=$3 AND run_id=$4
	`, normalized.WorkspaceID, normalized.AgentID,
		normalized.ConversationID, normalized.RunID).Scan(&actual)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrEventConflict
	}
	if err != nil {
		return "", fmt.Errorf("read protocol event stream: %w", err)
	}
	if actual != streamID {
		return "", ErrEventConflict
	}
	return actual, nil
}

func (transaction *ProtocolTransaction) CreateRunItem(
	ctx context.Context,
	input CreateRunItemInput,
) (RunItemProjection, error) {
	if transaction == nil || transaction.items == nil || !transaction.active {
		return RunItemProjection{}, ErrProtocolUnitOfWorkInvalid
	}
	return transaction.items.CreateInTx(ctx, transaction.tx, input)
}

func (transaction *ProtocolTransaction) ApplyRunItemDelta(
	ctx context.Context,
	input ApplyItemDeltaInput,
) (RunItemProjection, error) {
	if transaction == nil || transaction.items == nil || !transaction.active {
		return RunItemProjection{}, ErrProtocolUnitOfWorkInvalid
	}
	return transaction.items.ApplyDeltaInTx(ctx, transaction.tx, input)
}

func (transaction *ProtocolTransaction) CompleteRunItem(
	ctx context.Context,
	input CompleteRunItemInput,
) (RunItemProjection, error) {
	if transaction == nil || transaction.items == nil || !transaction.active {
		return RunItemProjection{}, ErrProtocolUnitOfWorkInvalid
	}
	return transaction.items.CompleteInTx(ctx, transaction.tx, input)
}

func (transaction *ProtocolTransaction) Append(
	ctx context.Context,
	events []NewProtocolEvent,
) ([]ProtocolEvent, error) {
	if transaction == nil || transaction.appender == nil || !transaction.active {
		return nil, ErrProtocolUnitOfWorkInvalid
	}
	appended, err := transaction.appender.AppendInTx(ctx, transaction.tx, events)
	if err != nil {
		return nil, err
	}
	transaction.events = append(transaction.events, appended...)
	return append([]ProtocolEvent(nil), appended...), nil
}

type ProtocolUnitOfWork struct {
	db       *sql.DB
	items    *RunItemRepository
	appender *EventAppender
	notifier CommitNotifier
}

func NewProtocolUnitOfWork(
	db *sql.DB,
	notifier CommitNotifier,
) (*ProtocolUnitOfWork, error) {
	items, err := NewRunItemRepository(db)
	if err != nil {
		return nil, ErrProtocolUnitOfWorkInvalid
	}
	return &ProtocolUnitOfWork{
		db: db, items: items, appender: NewEventAppender(), notifier: notifier,
	}, nil
}

func (unit *ProtocolUnitOfWork) Execute(
	ctx context.Context,
	work ProtocolWork,
) (UnitOfWorkResult, error) {
	if unit == nil || unit.db == nil || unit.items == nil || unit.appender == nil || work == nil {
		return UnitOfWorkResult{}, ErrProtocolUnitOfWorkInvalid
	}
	started := time.Now()
	tx, err := unit.db.BeginTx(ctx, nil)
	if err != nil {
		return UnitOfWorkResult{}, fmt.Errorf("begin protocol unit of work: %w", err)
	}
	transaction := &ProtocolTransaction{
		tx: tx, items: unit.items, appender: unit.appender, active: true,
	}
	defer func() {
		transaction.active = false
		_ = tx.Rollback()
	}()

	if err := work(ctx, transaction); err != nil {
		transaction.active = false
		labels := appendMetricLabels(transaction.events)
		metrics.Default().ObserveProtocolEventAppendError(labels)
		if errors.Is(err, ErrEventConflict) {
			metrics.Default().ObserveSequenceConflict(labels)
		}
		return UnitOfWorkResult{}, fmt.Errorf("execute protocol unit of work: %w", err)
	}
	if len(transaction.events) == 0 {
		transaction.active = false
		return UnitOfWorkResult{}, ErrProtocolUnitOfWorkNoEvents
	}
	transaction.active = false
	if err := tx.Commit(); err != nil {
		metrics.Default().ObserveProtocolEventAppendError(appendMetricLabels(transaction.events))
		return UnitOfWorkResult{}, fmt.Errorf("commit protocol unit of work: %w", err)
	}

	labels := appendMetricLabels(transaction.events)
	metrics.Default().ObserveProtocolEventAppend(time.Since(started), labels)

	result := UnitOfWorkResult{Events: append([]ProtocolEvent(nil), transaction.events...)}
	if unit.notifier != nil {
		notification := committedNotification(transaction.events)
		if err := unit.notifier.NotifyCommitted(ctx, notification); err != nil {
			result.NotifyError = fmt.Errorf("notify committed protocol events: %w", err)
			metrics.Default().ObserveFanoutNotifyFailure(labels)
		}
	}
	return result, nil
}

func appendMetricLabels(events []ProtocolEvent) map[string]string {
	if len(events) == 0 {
		return nil
	}
	event := events[0]
	return map[string]string{
		"workspace_id": event.WorkspaceID,
		"agent_id":     event.AgentID,
		"run_id":       event.RunID,
	}
}

func committedNotification(events []ProtocolEvent) CommitNotification {
	result := CommitNotification{Events: make([]CommittedEventRef, 0, len(events))}
	for _, event := range events {
		result.Events = append(result.Events, CommittedEventRef{
			EventID: event.ID, EventStreamID: event.EventStreamID,
			WorkspaceID: event.WorkspaceID, AgentID: event.AgentID,
			ConversationID: event.ConversationID, RunID: event.RunID,
			Sequence: event.Sequence, GlobalPosition: event.GlobalPosition,
		})
	}
	return result
}
