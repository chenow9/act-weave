package protocolevent_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

func TestProtocolUnitOfWork(t *testing.T) {
	ctx := context.Background()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertProtocolEventFixtures(t, db)
	insertProtocolStream(t, db)

	t.Run("append failure rolls back terminal fact and item", func(t *testing.T) {
		notifier := &recordingCommitNotifier{db: db}
		unit, err := protocolevent.NewProtocolUnitOfWork(db, notifier)
		if err != nil {
			t.Fatal(err)
		}
		item := unitOfWorkNotice("APPEND_FAILURE")
		event := unitOfWorkStartedEvent(t, item)
		event.Data = json.RawMessage(`{"authorization":"Bearer must-not-persist"}`)

		_, err = unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
			tx, err := transaction.SQLTx()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE agent_runs
				SET status='SUCCEEDED',output_summary='{"marker":"terminal"}',
				    finished_at=clock_timestamp(),lock_version=lock_version+1
				WHERE workspace_id=$1 AND id=$2
			`, protocolWorkspaceID, protocolRunID); err != nil {
				return err
			}
			if _, err := transaction.CreateRunItem(ctx, unitOfWorkCreateInput(item, 1)); err != nil {
				return err
			}
			_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
			return err
		})
		if err == nil {
			t.Fatal("expected append policy failure")
		}
		assertUnitOfWorkRunState(t, db, "RUNNING", "")
		assertUnitOfWorkRows(t, db, item.ID, 0, 0)
		assertUnitOfWorkStreamHead(t, db, 1)
		if notifier.calls != 0 {
			t.Fatalf("notifier called for rolled back transaction: %d", notifier.calls)
		}
	})

	t.Run("injected work failure rolls back every write", func(t *testing.T) {
		unit, err := protocolevent.NewProtocolUnitOfWork(db, nil)
		if err != nil {
			t.Fatal(err)
		}
		item := unitOfWorkNotice("WORK_FAILURE")
		event := unitOfWorkStartedEvent(t, item)
		injected := errors.New("injected failure after event append")
		_, err = unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
			tx, err := transaction.SQLTx()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE agent_runs SET output_summary='{"marker":"work-failure"}',
				                      lock_version=lock_version+1
				WHERE workspace_id=$1 AND id=$2
			`, protocolWorkspaceID, protocolRunID); err != nil {
				return err
			}
			if _, err := transaction.CreateRunItem(ctx, unitOfWorkCreateInput(item, 2)); err != nil {
				return err
			}
			if _, err := transaction.Append(ctx, []protocolevent.NewProtocolEvent{event}); err != nil {
				return err
			}
			return injected
		})
		if !errors.Is(err, injected) {
			t.Fatalf("injected failure error=%v", err)
		}
		assertUnitOfWorkRunState(t, db, "RUNNING", "")
		assertUnitOfWorkRows(t, db, item.ID, 0, 0)
		assertUnitOfWorkStreamHead(t, db, 1)
	})

	t.Run("missing event rejects a domain-only commit", func(t *testing.T) {
		unit, err := protocolevent.NewProtocolUnitOfWork(db, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
			tx, err := transaction.SQLTx()
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `
				UPDATE agent_runs SET output_summary='{"marker":"no-event"}',
				                      lock_version=lock_version+1
				WHERE workspace_id=$1 AND id=$2
			`, protocolWorkspaceID, protocolRunID)
			return err
		})
		if !errors.Is(err, protocolevent.ErrProtocolUnitOfWorkNoEvents) {
			t.Fatalf("missing event error=%v", err)
		}
		assertUnitOfWorkRunState(t, db, "RUNNING", "")
	})

	t.Run("notify failure happens after durable commit", func(t *testing.T) {
		item := unitOfWorkNotice("COMMITTED")
		notifyFailure := errors.New("injected notifier failure")
		notifier := &recordingCommitNotifier{
			db: db, expectedItemID: item.ID, expectedMarker: "committed", failure: notifyFailure,
		}
		unit, err := protocolevent.NewProtocolUnitOfWork(db, notifier)
		if err != nil {
			t.Fatal(err)
		}
		event := unitOfWorkStartedEvent(t, item)
		result, err := unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
			tx, err := transaction.SQLTx()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE agent_runs SET output_summary='{"marker":"committed"}',
				                      lock_version=lock_version+1
				WHERE workspace_id=$1 AND id=$2
			`, protocolWorkspaceID, protocolRunID); err != nil {
				return err
			}
			if _, err := transaction.CreateRunItem(ctx, unitOfWorkCreateInput(item, 4)); err != nil {
				return err
			}
			_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
			return err
		})
		if err != nil {
			t.Fatalf("committed unit of work: %v", err)
		}
		if !errors.Is(result.NotifyError, notifyFailure) {
			t.Fatalf("notify error=%v", result.NotifyError)
		}
		if len(result.Events) != 1 || result.Events[0].ID != event.ID || result.Events[0].Sequence != 1 {
			t.Fatalf("committed events=%+v", result.Events)
		}
		if notifier.calls != 1 || notifier.observationError != nil {
			t.Fatalf("notifier calls=%d observation error=%v", notifier.calls, notifier.observationError)
		}
		if len(notifier.notification.Events) != 1 ||
			notifier.notification.Events[0].EventID != event.ID ||
			notifier.notification.Events[0].Sequence != 1 {
			t.Fatalf("notification=%+v", notifier.notification)
		}
		assertUnitOfWorkRunState(t, db, "RUNNING", "committed")
		assertUnitOfWorkRows(t, db, item.ID, 1, 1)
		assertUnitOfWorkStreamHead(t, db, 2)
	})
}

type recordingCommitNotifier struct {
	db               *sql.DB
	expectedItemID   string
	expectedMarker   string
	failure          error
	calls            int
	notification     protocolevent.CommitNotification
	observationError error
}

func (notifier *recordingCommitNotifier) NotifyCommitted(
	ctx context.Context,
	notification protocolevent.CommitNotification,
) error {
	notifier.calls++
	notifier.notification = notification
	if notifier.expectedItemID != "" {
		var itemCount, eventCount int
		var marker string
		err := notifier.db.QueryRowContext(ctx, `
			SELECT
			 (SELECT count(*) FROM run_items WHERE id=$1),
			 (SELECT count(*) FROM protocol_events WHERE item_id=$1),
			 COALESCE((SELECT output_summary->>'marker' FROM agent_runs
			           WHERE workspace_id=$2 AND id=$3),'')
		`, notifier.expectedItemID, protocolWorkspaceID, protocolRunID).
			Scan(&itemCount, &eventCount, &marker)
		if err != nil {
			notifier.observationError = err
		} else if itemCount != 1 || eventCount != 1 || marker != notifier.expectedMarker {
			notifier.observationError = fmt.Errorf(
				"notification observed item=%d event=%d marker=%q",
				itemCount, eventCount, marker,
			)
		}
	}
	return notifier.failure
}

func unitOfWorkNotice(code string) protocolevent.NoticeItem {
	return protocolevent.NoticeItem{
		ID: uuid.NewString(), Type: protocolevent.ItemTypeNotice,
		Status: protocolevent.ItemStatusInProgress, Code: code, Message: "unit of work probe",
	}
}

func unitOfWorkCreateInput(item protocolevent.NoticeItem, ordinal int) protocolevent.CreateRunItemInput {
	return protocolevent.CreateRunItemInput{
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID, RunID: protocolRunID,
		Ordinal: ordinal, SourceType: protocolevent.SourceRuntime,
		Item: item, StartedAt: time.Now().UTC(),
	}
}

func unitOfWorkStartedEvent(t *testing.T, item protocolevent.NoticeItem) protocolevent.NewProtocolEvent {
	t.Helper()
	event, err := protocolevent.BuildProtocolEvent(protocolevent.NewProtocolEvent{
		ID: uuid.NewString(), EventStreamID: protocolStreamID,
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID,
		ConversationID: protocolSessionID, RunID: protocolRunID,
		Type: protocolevent.EventItemStarted, SpecVersion: "1.0",
		TraceID: "trace-protocol-unit-of-work", ItemID: item.ID,
		OccurredAt: time.Now().UTC(),
	}, protocolevent.ItemSnapshotData{Item: item})
	if err != nil {
		t.Fatalf("build unit of work event: %v", err)
	}
	return event
}

func assertUnitOfWorkRunState(t *testing.T, db *sql.DB, expectedStatus, expectedMarker string) {
	t.Helper()
	var status, marker string
	if err := db.QueryRow(`
		SELECT status,COALESCE(output_summary->>'marker','')
		FROM agent_runs WHERE workspace_id=$1 AND id=$2
	`, protocolWorkspaceID, protocolRunID).Scan(&status, &marker); err != nil {
		t.Fatal(err)
	}
	if status != expectedStatus || marker != expectedMarker {
		t.Fatalf("run status=%s marker=%q, want status=%s marker=%q",
			status, marker, expectedStatus, expectedMarker)
	}
}

func assertUnitOfWorkRows(t *testing.T, db *sql.DB, itemID string, expectedItems, expectedEvents int) {
	t.Helper()
	var itemCount, eventCount int
	if err := db.QueryRow(`
		SELECT
		 (SELECT count(*) FROM run_items WHERE id=$1),
		 (SELECT count(*) FROM protocol_events WHERE item_id=$1)
	`, itemID).Scan(&itemCount, &eventCount); err != nil {
		t.Fatal(err)
	}
	if itemCount != expectedItems || eventCount != expectedEvents {
		t.Fatalf("item %s rows item=%d event=%d, want item=%d event=%d",
			itemID, itemCount, eventCount, expectedItems, expectedEvents)
	}
}

func assertUnitOfWorkStreamHead(t *testing.T, db *sql.DB, expected int64) {
	t.Helper()
	var head int64
	if err := db.QueryRow(`SELECT next_sequence FROM protocol_event_streams WHERE id=$1`, protocolStreamID).Scan(&head); err != nil {
		t.Fatal(err)
	}
	if head != expected {
		t.Fatalf("stream head=%d, want %d", head, expected)
	}
}
