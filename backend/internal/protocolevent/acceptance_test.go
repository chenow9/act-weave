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
	"github.com/lib/pq"
)

func TestEventKernelDatabaseAcceptance(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 3 || version.Dirty {
		t.Fatalf("expected clean latest schema version 2, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertProtocolEventFixtures(t, db)
	insertProtocolStream(t, db)

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	eventID := uuid.NewString()
	appended, err := protocolevent.NewEventAppender().AppendInTx(
		context.Background(), tx, []protocolevent.NewProtocolEvent{{
			ID: eventID, EventStreamID: protocolStreamID,
			WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID,
			ConversationID: protocolSessionID, RunID: protocolRunID,
			Type: "item.completed", SpecVersion: "1.0", TraceID: "trace-acceptance",
			OccurredAt: time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC),
			Data:       json.RawMessage(`{"item":{"id":"608f1f2e-7b5a-7c3d-8e9f-12345678900f","type":"notice","status":"completed","code":"ACCEPTANCE","message":"accepted"}}`),
		}},
	)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if len(appended) != 1 || appended[0].Sequence != 1 {
		_ = tx.Rollback()
		t.Fatalf("unexpected accepted event: %+v", appended)
	}
	if _, err := tx.Exec(`
		INSERT INTO run_items(
		 id,workspace_id,agent_id,run_id,ordinal,item_type,status,
		 source_type,snapshot,completed_at
		) VALUES($1,$2,$3,$4,1,'notice','completed','RUNTIME',$5,clock_timestamp())
	`, protocolItemID, protocolWorkspaceID, protocolAgentID, protocolRunID,
		`{"id":"608f1f2e-7b5a-7c3d-8e9f-12345678900f","type":"notice","status":"completed"}`); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	assertNoProtocolOrphans(t, db)
	_, err = db.Exec(`
		INSERT INTO run_events(
		 id,workspace_id,run_id,sequence_no,event_type,payload
		) VALUES($1,$2,$3,1,'RUN_STARTED','{}')
	`, protocolProbeID, protocolWorkspaceID, protocolOtherRunID)
	var databaseError *pq.Error
	if !errors.As(err, &databaseError) || databaseError.Constraint != "run_events_cutover_complete" {
		t.Fatalf("legacy write entry point remains open: %v", err)
	}

}

func assertNoProtocolOrphans(t *testing.T, db *sql.DB) {
	t.Helper()
	queries := map[string]string{
		"stream": `
			SELECT count(*) FROM protocol_event_streams pes
			LEFT JOIN agent_runs ar
			  ON ar.workspace_id=pes.workspace_id AND ar.agent_id=pes.agent_id
			 AND ar.session_id=pes.conversation_id AND ar.id=pes.run_id
			WHERE ar.id IS NULL`,
		"event": `
			SELECT count(*) FROM protocol_events pe
			LEFT JOIN protocol_event_streams pes
			  ON pes.workspace_id=pe.workspace_id AND pes.agent_id=pe.agent_id
			 AND pes.conversation_id=pe.conversation_id AND pes.run_id=pe.run_id
			 AND pes.id=pe.stream_id
			WHERE pes.id IS NULL`,
		"item": `
			SELECT count(*) FROM run_items ri
			LEFT JOIN agent_runs ar
			  ON ar.workspace_id=ri.workspace_id AND ar.agent_id=ri.agent_id AND ar.id=ri.run_id
			WHERE ar.id IS NULL`,
	}
	for name, query := range queries {
		var count int
		if err := db.QueryRow(query).Scan(&count); err != nil {
			t.Fatalf("count orphan %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("found %d orphan %s facts", count, name)
		}
	}
}

func assertProtocolTablesExist(t *testing.T, db *sql.DB, expected bool) {
	t.Helper()
	for _, table := range []string{"protocol_event_streams", "protocol_events", "run_items"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != expected {
			t.Fatalf("table %s exists=%t, want %t", table, exists, expected)
		}
	}
}
