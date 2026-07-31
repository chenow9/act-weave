// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package protocolevent_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/database/dbtest"

	"github.com/lib/pq"
)

const (
	protocolOwnerID       = "608f1f2e-7b5a-7c3d-8e9f-123456789001"
	protocolWorkspaceID   = "608f1f2e-7b5a-7c3d-8e9f-123456789002"
	protocolModelID       = "608f1f2e-7b5a-7c3d-8e9f-123456789003"
	protocolAgentID       = "608f1f2e-7b5a-7c3d-8e9f-123456789004"
	protocolOtherAgentID  = "608f1f2e-7b5a-7c3d-8e9f-123456789005"
	protocolSessionID     = "608f1f2e-7b5a-7c3d-8e9f-123456789006"
	protocolOtherSession  = "608f1f2e-7b5a-7c3d-8e9f-123456789007"
	protocolRunID         = "608f1f2e-7b5a-7c3d-8e9f-123456789008"
	protocolOtherRunID    = "608f1f2e-7b5a-7c3d-8e9f-123456789009"
	protocolStreamID      = "608f1f2e-7b5a-7c3d-8e9f-12345678900a"
	protocolOtherStreamID = "608f1f2e-7b5a-7c3d-8e9f-12345678900b"
	protocolEventID       = "608f1f2e-7b5a-7c3d-8e9f-12345678900c"
	protocolEventID2      = "608f1f2e-7b5a-7c3d-8e9f-12345678900d"
	protocolProbeID       = "608f1f2e-7b5a-7c3d-8e9f-12345678900e"
	protocolItemID        = "608f1f2e-7b5a-7c3d-8e9f-12345678900f"
	protocolItemID2       = "608f1f2e-7b5a-7c3d-8e9f-123456789010"
)

func TestProtocolEventMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("expected clean protocol event migration version 4, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertProtocolEventFixtures(t, db)
	assertProtocolEventSchema(t, db)
	assertProtocolEventConstraints(t, db)

}

func TestRunItemMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("expected clean run item migration version 4, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertProtocolEventFixtures(t, db)

	if _, err := db.Exec(`
		INSERT INTO run_items(
		 id,workspace_id,agent_id,run_id,ordinal,item_type,status,
		 source_type,source_id,snapshot
		) VALUES($1,$2,$3,$4,1,'message','in_progress','CHAT_MESSAGE',$5,$6)
	`, protocolItemID, protocolWorkspaceID, protocolAgentID, protocolRunID,
		protocolEventID, `{"id":"608f1f2e-7b5a-7c3d-8e9f-12345678900f","type":"message"}`); err != nil {
		t.Fatalf("insert run item: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO run_items(
		 id,workspace_id,agent_id,run_id,ordinal,item_type,status,
		 source_type,snapshot,completed_at
		) VALUES($1,$2,$3,$4,2,'tool_call','completed','TOOL_INVOCATION',$5,clock_timestamp())
	`, protocolItemID2, protocolWorkspaceID, protocolAgentID, protocolRunID,
		`{"id":"608f1f2e-7b5a-7c3d-8e9f-123456789010","type":"tool_call"}`); err != nil {
		t.Fatalf("insert completed run item: %v", err)
	}

	assertProtocolStatementFails(t, db, `
		INSERT INTO run_items(
		 id,workspace_id,agent_id,run_id,ordinal,item_type,status,source_type,snapshot
		) VALUES($1,$2,$3,$4,1,'notice','in_progress','RUNTIME','{}')
	`, protocolProbeID, protocolWorkspaceID, protocolAgentID, protocolRunID)
	assertProtocolStatementFails(t, db, `
		INSERT INTO run_items(
		 id,workspace_id,agent_id,run_id,ordinal,item_type,status,source_type,snapshot
		) VALUES($1,$2,$3,$4,1,'notice','in_progress','RUNTIME','{}')
	`, protocolProbeID, protocolWorkspaceID, protocolOtherAgentID, protocolRunID)
	assertProtocolStatementFails(t, db, `
		INSERT INTO run_items(
		 id,workspace_id,agent_id,run_id,ordinal,item_type,status,source_type,snapshot
		) VALUES($1,$2,$3,$4,3,'notice','in_progress','INVALID','{}')
	`, protocolProbeID, protocolWorkspaceID, protocolAgentID, protocolRunID)
	assertProtocolStatementFails(t, db, `
		INSERT INTO run_items(
		 id,workspace_id,agent_id,run_id,ordinal,item_type,status,source_type,snapshot
		) VALUES($1,$2,$3,$4,3,'notice','in_progress','RUNTIME','[]')
	`, protocolProbeID, protocolWorkspaceID, protocolAgentID, protocolRunID)
	assertProtocolStatementFails(t, db, `
		INSERT INTO run_items(
		 id,workspace_id,agent_id,run_id,ordinal,item_type,status,source_type,snapshot
		) VALUES($1,$2,$3,$4,3,'notice','completed','RUNTIME','{}')
	`, protocolProbeID, protocolWorkspaceID, protocolAgentID, protocolRunID)
	assertProtocolStatementFails(t, db, `
		INSERT INTO run_items(
		 id,workspace_id,agent_id,run_id,ordinal,item_type,status,source_type,snapshot,completed_at
		) VALUES($1,$2,$3,$4,3,'notice','waiting','RUNTIME','{}',clock_timestamp())
	`, protocolProbeID, protocolWorkspaceID, protocolAgentID, protocolRunID)

	for _, index := range []string{
		"run_items_run_ordinal_key", "run_items_scope_ordinal_idx",
		"run_items_scope_status_started_idx", "run_items_source_ref_idx",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+index).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected run item index %s", index)
		}
	}

}

func TestProtocolEventImmutable(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("expected clean envelope guard migration version 4, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertProtocolEventFixtures(t, db)
	insertProtocolStream(t, db)
	payload := protocolEnvelope(protocolEventID, "run.accepted", "1.0", 1)
	insertGuardedProtocolEvent(t, db, protocolEventID, 1, "run.accepted", "1.0", payload)

	_, err := db.Exec(`UPDATE protocol_events SET occurred_at=clock_timestamp() WHERE id=$1`, protocolEventID)
	assertProtocolDatabaseError(t, err, "55000", "protocol_events_immutable")
	_, err = db.Exec(`DELETE FROM protocol_events WHERE id=$1`, protocolEventID)
	assertProtocolDatabaseError(t, err, "55000", "protocol_events_immutable")

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM protocol_events WHERE id=$1`, protocolEventID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("immutable event was changed or removed, count=%d", count)
	}
}

func TestEnvelopeConstraint(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("expected clean envelope guard migration version 4, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertProtocolEventFixtures(t, db)
	insertProtocolStream(t, db)
	insertGuardedProtocolEvent(
		t, db, protocolEventID, 1, "run.accepted", "1.0",
		protocolEnvelope(protocolEventID, "run.accepted", "1.0", 1),
	)

	tests := []struct {
		name        string
		eventType   string
		specVersion string
		mutate      func(map[string]any)
	}{
		{name: "event id", eventType: "run.started", specVersion: "1.0", mutate: func(value map[string]any) { value["eventId"] = protocolEventID2 }},
		{name: "event type", eventType: "run.started", specVersion: "1.0", mutate: func(value map[string]any) { value["type"] = "run.completed" }},
		{name: "spec version", eventType: "run.started", specVersion: "1.0", mutate: func(value map[string]any) { value["specVersion"] = "2.0" }},
		{name: "sequence", eventType: "run.started", specVersion: "1.0", mutate: func(value map[string]any) { value["sequence"] = 3 }},
		{name: "workspace", eventType: "run.started", specVersion: "1.0", mutate: func(value map[string]any) { value["workspaceId"] = protocolOtherRunID }},
		{name: "agent", eventType: "run.started", specVersion: "1.0", mutate: func(value map[string]any) { value["agentId"] = protocolOtherAgentID }},
		{name: "conversation", eventType: "run.started", specVersion: "1.0", mutate: func(value map[string]any) { value["conversationId"] = protocolOtherSession }},
		{name: "run", eventType: "run.started", specVersion: "1.0", mutate: func(value map[string]any) { value["runId"] = protocolOtherRunID }},
		{name: "stream", eventType: "run.started", specVersion: "1.0", mutate: func(value map[string]any) { value["streamId"] = "run:" + protocolOtherRunID }},
		{name: "data object", eventType: "run.started", specVersion: "1.0", mutate: func(value map[string]any) { value["data"] = []any{} }},
		{name: "transport signal", eventType: "stream.error", specVersion: "1.0", mutate: func(value map[string]any) { value["type"] = "stream.error" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := protocolEnvelopeMap(protocolProbeID, test.eventType, test.specVersion, 2)
			test.mutate(payload)
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(`
				INSERT INTO protocol_events(
				 id,workspace_id,agent_id,conversation_id,run_id,stream_id,
				 sequence_no,event_type,spec_version,payload
				) VALUES($1,$2,$3,$4,$5,$6,2,$7,$8,$9)
			`, protocolProbeID, protocolWorkspaceID, protocolAgentID, protocolSessionID,
				protocolRunID, protocolStreamID, test.eventType, test.specVersion, raw)
			assertProtocolDatabaseError(t, err, "23514", "protocol_events_envelope_consistency")
		})
	}
}

func insertProtocolStream(t testing.TB, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO protocol_event_streams(
		 id,workspace_id,agent_id,conversation_id,run_id
		) VALUES($1,$2,$3,$4,$5)
	`, protocolStreamID, protocolWorkspaceID, protocolAgentID, protocolSessionID, protocolRunID); err != nil {
		t.Fatalf("insert guarded protocol event stream: %v", err)
	}
}

func insertGuardedProtocolEvent(
	t *testing.T,
	db *sql.DB,
	id string,
	sequence int64,
	eventType, specVersion string,
	payload []byte,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO protocol_events(
		 id,workspace_id,agent_id,conversation_id,run_id,stream_id,
		 sequence_no,event_type,spec_version,payload
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, id, protocolWorkspaceID, protocolAgentID, protocolSessionID, protocolRunID,
		protocolStreamID, sequence, eventType, specVersion, payload); err != nil {
		t.Fatalf("insert guarded protocol event: %v", err)
	}
}

func protocolEnvelope(eventID, eventType, specVersion string, sequence int64) []byte {
	value := protocolEnvelopeMap(eventID, eventType, specVersion, sequence)
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func protocolEnvelopeMap(eventID, eventType, specVersion string, sequence int64) map[string]any {
	return map[string]any{
		"specVersion":    specVersion,
		"type":           eventType,
		"eventId":        eventID,
		"streamId":       "run:" + protocolRunID,
		"sequence":       sequence,
		"occurredAt":     "2026-07-20T05:00:00Z",
		"workspaceId":    protocolWorkspaceID,
		"agentId":        protocolAgentID,
		"conversationId": protocolSessionID,
		"runId":          protocolRunID,
		"traceId":        "trace-protocol-guard",
		"data":           map[string]any{},
	}
}

func assertProtocolDatabaseError(t *testing.T, err error, code, constraint string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected PostgreSQL error %s/%s", code, constraint)
	}
	var databaseError *pq.Error
	if !errors.As(err, &databaseError) {
		t.Fatalf("expected pq.Error, got %T: %v", err, err)
	}
	if string(databaseError.Code) != code || databaseError.Constraint != constraint {
		t.Fatalf("unexpected PostgreSQL error code=%s constraint=%s message=%s",
			databaseError.Code, databaseError.Constraint, databaseError.Message)
	}
}

func insertProtocolEventFixtures(t testing.TB, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'protocol.owner','Protocol Owner')`, protocolOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'protocol-space','Protocol Space','PRODUCTION',$2,$2,$2)
	`, protocolWorkspaceID, protocolOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(
		 id,workspace_id,name,provider,api_base,model_name,created_by,updated_by
		) VALUES($1,$2,'Protocol Model','openai','https://models.example.test','protocol-model',$3,$3)
	`, protocolModelID, protocolWorkspaceID, protocolOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES
		($1,$3,'Protocol Agent',$4,$5,$5),
		($2,$3,'Protocol Other Agent',$4,$5,$5)
	`, protocolAgentID, protocolOtherAgentID, protocolWorkspaceID, protocolModelID, protocolOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by) VALUES
		($1,$3,$4,'Protocol session',$6),
		($2,$3,$5,'Protocol other session',$6)
	`, protocolSessionID, protocolOtherSession, protocolWorkspaceID, protocolAgentID, protocolOtherAgentID, protocolOwnerID); err != nil {
		t.Fatal(err)
	}
	insertProtocolRun(t, db, protocolRunID, protocolSessionID, protocolAgentID, "trace-protocol")
	insertProtocolRun(t, db, protocolOtherRunID, protocolOtherSession, protocolOtherAgentID, "trace-protocol-other")
}

func insertProtocolRun(t testing.TB, db *sql.DB, id, sessionID, agentID, traceID string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO agent_runs(
		 id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
		 triggered_by_id,trace_id,model_snapshot,capability_snapshot
		) VALUES($1,$2,$3,$4,'RUNNING','API','USER',$5,$6,'{}','{}')
	`, id, protocolWorkspaceID, sessionID, agentID, protocolOwnerID, traceID); err != nil {
		t.Fatalf("insert protocol run: %v", err)
	}
}

func assertProtocolEventSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"protocol_event_streams", "protocol_events"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected protocol event table %s", table)
		}
		var hasWorkspace bool
		if err := db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM information_schema.columns
			 WHERE table_schema='public' AND table_name=$1 AND column_name='workspace_id' AND is_nullable='NO')
		`, table).Scan(&hasWorkspace); err != nil {
			t.Fatal(err)
		}
		if !hasWorkspace {
			t.Fatalf("expected non-null workspace_id on %s", table)
		}
	}
	for _, index := range []string{
		"protocol_event_streams_workspace_run_key",
		"protocol_event_streams_scope_created_idx",
		"protocol_events_global_position_key",
		"protocol_events_stream_sequence_key",
		"protocol_events_scope_sequence_idx",
		"protocol_events_global_delivery_idx",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+index).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected protocol event index %s", index)
		}
	}
}

func assertProtocolEventConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO protocol_event_streams(
		 id,workspace_id,agent_id,conversation_id,run_id
		) VALUES($1,$2,$3,$4,$5)
	`, protocolStreamID, protocolWorkspaceID, protocolAgentID, protocolSessionID, protocolRunID); err != nil {
		t.Fatalf("insert protocol event stream: %v", err)
	}

	assertProtocolStatementFails(t, db, `
		INSERT INTO protocol_event_streams(
		 id,workspace_id,agent_id,conversation_id,run_id
		) VALUES($1,$2,$3,$4,$5)
	`, protocolOtherStreamID, protocolWorkspaceID, protocolAgentID, protocolSessionID, protocolRunID)
	assertProtocolStatementFails(t, db, `
		INSERT INTO protocol_event_streams(
		 id,workspace_id,agent_id,conversation_id,run_id
		) VALUES($1,$2,$3,$4,$5)
	`, protocolOtherStreamID, protocolWorkspaceID, protocolOtherAgentID, protocolSessionID, protocolRunID)
	assertProtocolStatementFails(t, db, `
		INSERT INTO protocol_event_streams(
		 id,workspace_id,agent_id,conversation_id,run_id,next_sequence
		) VALUES($1,$2,$3,$4,$5,0)
	`, protocolOtherStreamID, protocolWorkspaceID, protocolOtherAgentID, protocolOtherSession, protocolOtherRunID)

	firstPosition := insertProtocolEvent(t, db, protocolEventID, 1, `{"specVersion":"1.0","type":"run.accepted"}`)
	secondPosition := insertProtocolEvent(t, db, protocolEventID2, 2, `{"specVersion":"1.0","type":"run.started"}`)
	if secondPosition <= firstPosition {
		t.Fatalf("global_position did not increase: %d then %d", firstPosition, secondPosition)
	}

	assertProtocolStatementFails(t, db, `
		INSERT INTO protocol_events(
		 id,workspace_id,agent_id,conversation_id,run_id,stream_id,
		 sequence_no,event_type,spec_version,payload
		) VALUES($1,$2,$3,$4,$5,$6,2,'run.started','1.0','{}')
	`, protocolProbeID, protocolWorkspaceID, protocolAgentID, protocolSessionID, protocolRunID, protocolStreamID)
	assertProtocolStatementFails(t, db, `
		INSERT INTO protocol_events(
		 id,workspace_id,agent_id,conversation_id,run_id,stream_id,
		 sequence_no,event_type,spec_version,payload
		) VALUES($1,$2,$3,$4,$5,$6,3,'run.started','1.0','[]')
	`, protocolProbeID, protocolWorkspaceID, protocolAgentID, protocolSessionID, protocolRunID, protocolStreamID)
	assertProtocolStatementFails(t, db, `
		INSERT INTO protocol_events(
		 id,workspace_id,agent_id,conversation_id,run_id,stream_id,
		 sequence_no,event_type,spec_version,payload
		) VALUES($1,$2,$3,$4,$5,$6,3,'run.started','1.0','{}')
	`, protocolProbeID, protocolWorkspaceID, protocolOtherAgentID, protocolOtherSession, protocolOtherRunID, protocolStreamID)
	assertProtocolStatementFails(t, db, `
		INSERT INTO protocol_events(
		 id,workspace_id,agent_id,conversation_id,run_id,stream_id,
		 sequence_no,event_type,spec_version,payload
		) VALUES($1,$2,$3,$4,$5,$6,0,'run.started','1.0','{}')
	`, protocolProbeID, protocolWorkspaceID, protocolAgentID, protocolSessionID, protocolRunID, protocolStreamID)
}

func insertProtocolEvent(t *testing.T, db *sql.DB, id string, sequence int64, payload string) int64 {
	t.Helper()
	var position int64
	err := db.QueryRow(`
		INSERT INTO protocol_events(
		 id,workspace_id,agent_id,conversation_id,run_id,stream_id,
		 sequence_no,event_type,spec_version,payload
		) VALUES($1,$2,$3,$4,$5,$6,$7,'run.started','1.0',$8)
		RETURNING global_position
	`, id, protocolWorkspaceID, protocolAgentID, protocolSessionID, protocolRunID,
		protocolStreamID, sequence, payload).Scan(&position)
	if err != nil {
		t.Fatalf("insert protocol event: %v", err)
	}
	return position
}

func assertProtocolStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected protocol event statement to fail: %s", strings.TrimSpace(query))
	}
}
