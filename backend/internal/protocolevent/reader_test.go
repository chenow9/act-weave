package protocolevent_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

func TestReadRunAfter(t *testing.T) {
	db, reader := setupEventReader(t)
	appendReaderEvents(t, db, 5)
	scope := protocolRunScope()

	events, err := reader.ReadRunAfter(context.Background(), scope, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Sequence != 3 || events[1].Sequence != 4 {
		t.Fatalf("unexpected page after sequence 2: %+v", events)
	}
	for index, event := range events {
		if event.WorkspaceID != scope.WorkspaceID || event.AgentID != scope.AgentID ||
			event.ConversationID != scope.ConversationID || event.RunID != scope.RunID ||
			event.StreamID != "run:"+scope.RunID {
			t.Fatalf("event %d lost its complete scope: %+v", index, event)
		}
		if len(event.Payload) == 0 || len(event.Data) == 0 || event.TraceID != "trace-reader" {
			t.Fatalf("event %d lost envelope data: %+v", index, event)
		}
	}

	events, err = reader.ReadRunAfter(context.Background(), scope, 4, 500)
	if err != nil || len(events) != 1 || events[0].Sequence != 5 {
		t.Fatalf("unexpected tail page events=%+v err=%v", events, err)
	}
	events, err = reader.ReadRunAfter(context.Background(), scope, 5, 1)
	if err != nil || len(events) != 0 {
		t.Fatalf("expected an empty page at high watermark, events=%+v err=%v", events, err)
	}
	for _, input := range []struct {
		after int64
		limit int
	}{{after: -1, limit: 1}, {after: 0, limit: 0}, {after: 0, limit: 501}} {
		if _, err := reader.ReadRunAfter(context.Background(), scope, input.after, input.limit); !errors.Is(err, protocolevent.ErrReadInvalid) {
			t.Fatalf("after=%d limit=%d error=%v", input.after, input.limit, err)
		}
	}
}

func TestHighWatermark(t *testing.T) {
	db, reader := setupEventReader(t)
	scope := protocolRunScope()
	highWatermark, err := reader.HighWatermark(context.Background(), scope)
	if err != nil || highWatermark != 0 {
		t.Fatalf("empty stream high watermark=%d err=%v", highWatermark, err)
	}
	appendReaderEvents(t, db, 5)
	highWatermark, err = reader.HighWatermark(context.Background(), scope)
	if err != nil || highWatermark != 5 {
		t.Fatalf("populated stream high watermark=%d err=%v", highWatermark, err)
	}
}

func TestScopeIsolation(t *testing.T) {
	db, reader := setupEventReader(t)
	appendReaderEvents(t, db, 2)
	valid := protocolRunScope()
	scopes := []protocolevent.RunScope{
		{WorkspaceID: uuid.NewString(), AgentID: valid.AgentID, ConversationID: valid.ConversationID, RunID: valid.RunID},
		{WorkspaceID: valid.WorkspaceID, AgentID: protocolOtherAgentID, ConversationID: valid.ConversationID, RunID: valid.RunID},
		{WorkspaceID: valid.WorkspaceID, AgentID: valid.AgentID, ConversationID: protocolOtherSession, RunID: valid.RunID},
		{WorkspaceID: valid.WorkspaceID, AgentID: valid.AgentID, ConversationID: valid.ConversationID, RunID: protocolOtherRunID},
	}
	for index, scope := range scopes {
		if events, err := reader.ReadRunAfter(context.Background(), scope, 0, 100); !errors.Is(err, protocolevent.ErrRunScopeNotFound) || events != nil {
			t.Fatalf("scope %d leaked events=%+v err=%v", index, events, err)
		}
		if high, err := reader.HighWatermark(context.Background(), scope); !errors.Is(err, protocolevent.ErrRunScopeNotFound) || high != 0 {
			t.Fatalf("scope %d leaked high watermark=%d err=%v", index, high, err)
		}
	}
	invalid := valid
	invalid.AgentID = "not-a-uuid"
	if _, err := reader.ReadRunAfter(context.Background(), invalid, 0, 1); !errors.Is(err, protocolevent.ErrReadInvalid) {
		t.Fatalf("invalid scope error=%v", err)
	}
}

func setupEventReader(t *testing.T) (*sql.DB, *protocolevent.EventReader) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("expected event reader schema version 4, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertProtocolEventFixtures(t, db)
	insertProtocolStream(t, db)
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, reader
}

func protocolRunScope() protocolevent.RunScope {
	return protocolevent.RunScope{
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID,
		ConversationID: protocolSessionID, RunID: protocolRunID,
	}
}

func appendReaderEvents(t *testing.T, db *sql.DB, count int) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	events := make([]protocolevent.NewProtocolEvent, 0, count)
	for index := 0; index < count; index++ {
		data, err := json.Marshal(map[string]any{
			"index": index, "itemId": protocolItemID,
			"delta": map[string]any{
				"type": "progress", "current": index, "unit": "events",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, protocolevent.NewProtocolEvent{
			ID: uuid.NewString(), EventStreamID: protocolStreamID,
			WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID,
			ConversationID: protocolSessionID, RunID: protocolRunID,
			Type: "item.delta", SpecVersion: "1.0", TraceID: "trace-reader",
			OccurredAt: time.Date(2026, 7, 20, 7, 0, index, 0, time.UTC), Data: data,
		})
	}
	if _, err := protocolevent.NewEventAppender().AppendInTx(context.Background(), tx, events); err != nil {
		t.Fatalf("append reader events: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
