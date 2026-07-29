package protocolevent_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

func TestConcurrentAppendInTx(t *testing.T) {
	const (
		workers   = 16
		batchSize = 3
	)
	ctx := context.Background()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected protocol appender schema version 39, got %+v", version)
	}
	db := testDatabase.Open(t)
	db.SetMaxOpenConns(workers + 4)
	insertProtocolEventFixtures(t, db)
	insertProtocolStream(t, db)
	appender := protocolevent.NewEventAppender()

	type outcome struct {
		worker int
		events []protocolevent.ProtocolEvent
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wait.Add(1)
		go func() {
			defer wait.Done()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				outcomes <- outcome{worker: worker, err: err}
				return
			}
			defer tx.Rollback()
			batch := make([]protocolevent.NewProtocolEvent, 0, batchSize)
			for index := 0; index < batchSize; index++ {
				batch = append(batch, concurrentProtocolEvent(worker, index))
			}
			<-start
			appended, err := appender.AppendInTx(ctx, tx, batch)
			if err == nil {
				err = tx.Commit()
			}
			outcomes <- outcome{worker: worker, events: appended, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(outcomes)

	seenSequence := make(map[int64]struct{}, workers*batchSize)
	for result := range outcomes {
		if result.err != nil {
			t.Fatalf("worker %d append: %v", result.worker, result.err)
		}
		if len(result.events) != batchSize {
			t.Fatalf("worker %d appended %d events", result.worker, len(result.events))
		}
		for index, event := range result.events {
			if index > 0 && event.Sequence != result.events[index-1].Sequence+1 {
				t.Fatalf("worker %d batch sequence is unstable: %+v", result.worker, result.events)
			}
			if _, duplicate := seenSequence[event.Sequence]; duplicate {
				t.Fatalf("duplicate allocated sequence %d", event.Sequence)
			}
			seenSequence[event.Sequence] = struct{}{}
			var data struct {
				Worker int `json:"worker"`
				Index  int `json:"index"`
			}
			if err := json.Unmarshal(event.Data, &data); err != nil ||
				data.Worker != result.worker || data.Index != index {
				t.Fatalf("worker %d batch order changed at %d: %s", result.worker, index, event.Data)
			}
		}
	}
	for sequence := int64(1); sequence <= workers*batchSize; sequence++ {
		if _, exists := seenSequence[sequence]; !exists {
			t.Fatalf("missing committed sequence %d", sequence)
		}
	}
	assertPersistedSequences(t, db, workers*batchSize)

	rolledBackTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := appender.AppendInTx(ctx, rolledBackTx, []protocolevent.NewProtocolEvent{
		concurrentProtocolEvent(workers, 0),
	})
	if err != nil {
		t.Fatalf("append rollback probe: %v", err)
	}
	if rolledBack[0].Sequence != workers*batchSize+1 {
		t.Fatalf("unexpected rollback probe sequence %d", rolledBack[0].Sequence)
	}
	if err := rolledBackTx.Rollback(); err != nil {
		t.Fatal(err)
	}

	committedTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := appender.AppendInTx(ctx, committedTx, []protocolevent.NewProtocolEvent{
		concurrentProtocolEvent(workers+1, 0),
	})
	if err != nil {
		_ = committedTx.Rollback()
		t.Fatalf("append after rollback: %v", err)
	}
	if committed[0].Sequence != workers*batchSize+1 {
		_ = committedTx.Rollback()
		t.Fatalf("rollback consumed visible sequence: got %d", committed[0].Sequence)
	}
	if err := committedTx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertPersistedSequences(t, db, workers*batchSize+1)

	if _, err := appender.AppendInTx(ctx, nil, []protocolevent.NewProtocolEvent{
		concurrentProtocolEvent(99, 0),
	}); !errors.Is(err, protocolevent.ErrAppendInvalid) {
		t.Fatalf("nil caller transaction error=%v", err)
	}
	missingTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	missing := concurrentProtocolEvent(100, 0)
	missing.EventStreamID = uuid.NewString()
	if _, err := appender.AppendInTx(ctx, missingTx, []protocolevent.NewProtocolEvent{missing}); !errors.Is(err, protocolevent.ErrEventStreamNotFound) {
		_ = missingTx.Rollback()
		t.Fatalf("missing stream error=%v", err)
	}
	_ = missingTx.Rollback()
}

func concurrentProtocolEvent(worker, index int) protocolevent.NewProtocolEvent {
	data, err := json.Marshal(map[string]any{
		"worker": worker, "index": index, "itemId": protocolItemID,
		"delta": map[string]any{
			"type": "progress", "current": index, "unit": "events",
		},
	})
	if err != nil {
		panic(err)
	}
	return protocolevent.NewProtocolEvent{
		ID: uuid.NewString(), EventStreamID: protocolStreamID,
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID,
		ConversationID: protocolSessionID, RunID: protocolRunID,
		Type: "item.delta", SpecVersion: "1.0",
		TraceID:    "trace-concurrent-append",
		OccurredAt: time.Date(2026, 7, 20, 6, 0, 0, worker*batchNanoseconds+index, time.UTC),
		Data:       data,
	}
}

const batchNanoseconds = 10

func assertPersistedSequences(t *testing.T, db *sql.DB, expected int) {
	t.Helper()
	rows, err := db.Query(`
		SELECT sequence_no FROM protocol_events
		WHERE workspace_id=$1 AND agent_id=$2 AND run_id=$3
		ORDER BY sequence_no
	`, protocolWorkspaceID, protocolAgentID, protocolRunID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	actual := 0
	for rows.Next() {
		actual++
		var sequence int64
		if err := rows.Scan(&sequence); err != nil {
			t.Fatal(err)
		}
		if sequence != int64(actual) {
			t.Fatalf("persisted sequence gap at row %d: got %d", actual, sequence)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("persisted event count=%d, want %d", actual, expected)
	}
	var nextSequence int64
	if err := db.QueryRow(`SELECT next_sequence FROM protocol_event_streams WHERE id=$1`, protocolStreamID).Scan(&nextSequence); err != nil {
		t.Fatal(err)
	}
	if nextSequence != int64(expected+1) {
		t.Fatalf("stream head=%d, want %d", nextSequence, expected+1)
	}
}

func TestEventAppenderRejectsMixedScopeAndDuplicateID(t *testing.T) {
	ctx := context.Background()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertProtocolEventFixtures(t, db)
	insertProtocolStream(t, db)
	appender := protocolevent.NewEventAppender()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, second := concurrentProtocolEvent(1, 0), concurrentProtocolEvent(1, 1)
	second.AgentID = protocolOtherAgentID
	if _, err := appender.AppendInTx(ctx, tx, []protocolevent.NewProtocolEvent{first, second}); !errors.Is(err, protocolevent.ErrAppendInvalid) {
		_ = tx.Rollback()
		t.Fatalf("mixed scope error=%v", err)
	}
	_ = tx.Rollback()

	firstTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := concurrentProtocolEvent(2, 0)
	if _, err := appender.AppendInTx(ctx, firstTx, []protocolevent.NewProtocolEvent{duplicate}); err != nil {
		_ = firstTx.Rollback()
		t.Fatal(err)
	}
	if err := firstTx.Commit(); err != nil {
		t.Fatal(err)
	}

	duplicateTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appender.AppendInTx(ctx, duplicateTx, []protocolevent.NewProtocolEvent{duplicate}); !errors.Is(err, protocolevent.ErrEventConflict) {
		_ = duplicateTx.Rollback()
		t.Fatalf("duplicate event error=%v", err)
	}
	_ = duplicateTx.Rollback()
}

func ExampleEventAppender_AppendInTx() {
	var appender *protocolevent.EventAppender
	var tx *sql.Tx
	_, _ = appender.AppendInTx(context.Background(), tx, nil)
	fmt.Println("the caller owns commit and rollback")
	// Output: the caller owns commit and rollback
}
