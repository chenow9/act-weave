// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
// Historical step-migration tests were retired when migrations were squashed
// into 000001_init. See migrations_archive/ for the pre-squash chain.
package protocolevent_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"

	"github.com/lib/pq"
)

const (
	legacyStartedID  = "b08f1f2e-7b5a-7c3d-8e9f-123456789001"
	legacyStepID     = "b08f1f2e-7b5a-7c3d-8e9f-123456789002"
	legacyTerminalID = "b08f1f2e-7b5a-7c3d-8e9f-123456789003"
	legacyNewID      = "b08f1f2e-7b5a-7c3d-8e9f-123456789004"
)

func TestRunEventCutover(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertProtocolEventFixtures(t, db)
	insertLegacyRunEvents(t, db)

	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 4 || version.Dirty {
		t.Fatalf("expected clean run event cutover version 40, got %+v", version)
	}
	assertRunEventCounts(t, db, protocolRunID, 3, 3)
	var nextSequence int64
	if err := db.QueryRow(`
		SELECT next_sequence FROM protocol_event_streams
		WHERE workspace_id=$1 AND run_id=$2
	`, protocolWorkspaceID, protocolRunID).Scan(&nextSequence); err != nil {
		t.Fatal(err)
	}
	if nextSequence != 4 {
		t.Fatalf("backfilled stream head=%d, want 4", nextSequence)
	}

	_, err := db.Exec(`
		INSERT INTO run_events(
		 id,workspace_id,run_id,sequence_no,event_type,payload
		) VALUES($1,$2,$3,4,'RUN_STARTED','{}')
	`, protocolProbeID, protocolWorkspaceID, protocolOtherRunID)
	var databaseError *pq.Error
	if !errors.As(err, &databaseError) || databaseError.Constraint != "run_events_cutover_complete" {
		t.Fatalf("direct legacy write was not blocked by cutover: %v", err)
	}

	repository, err := execution.NewRunEventRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	appended, err := repository.Append(context.Background(), execution.AppendRunEventInput{
		ID: legacyNewID, WorkspaceID: protocolWorkspaceID, RunID: protocolOtherRunID,
		EventType: "RUN_STARTED", Payload: json.RawMessage(`{"source":"cutover"}`),
	})
	if err != nil || appended.SequenceNo != 1 {
		t.Fatalf("compatibility append event=%+v err=%v", appended, err)
	}
	assertRunEventCounts(t, db, protocolOtherRunID, 0, 1)

	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 4 || version.Dirty {
		t.Fatalf("expected clean cutover rollback version 39, got %+v", version)
	}
	assertRunEventCounts(t, db, protocolOtherRunID, 1, 0)
	var restoredPayload string
	if err := db.QueryRow(`SELECT payload::TEXT FROM run_events WHERE id=$1`, legacyNewID).Scan(&restoredPayload); err != nil {
		t.Fatal(err)
	}
	if restoredPayload != `{"source": "cutover"}` {
		t.Fatalf("rollback did not restore compatibility payload: %s", restoredPayload)
	}
	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 4 || version.Dirty {
		t.Fatalf("expected clean cutover migration reapply, got %+v", version)
	}
}

func TestLegacyReplay(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertProtocolEventFixtures(t, db)
	insertLegacyRunEvents(t, db)
	testDatabase.MigrateToLatest(t)

	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	events, err := reader.ReadRunAfter(context.Background(), protocolRunScope(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	actualTypes := make([]string, 0, len(events))
	for index, event := range events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("legacy replay sequence gap: %+v", events)
		}
		actualTypes = append(actualTypes, event.Type)
		var data map[string]json.RawMessage
		if err := json.Unmarshal(event.Data, &data); err != nil ||
			len(data["legacyEventType"]) == 0 || len(data["legacyPayload"]) == 0 {
			t.Fatalf("legacy evidence missing at sequence %d: %s", event.Sequence, event.Data)
		}
	}
	expectedTypes := []string{"run.started", "item.completed", "run.completed"}
	if !reflect.DeepEqual(actualTypes, expectedTypes) {
		t.Fatalf("legacy type mapping=%v, want %v", actualTypes, expectedTypes)
	}

	repository, err := execution.NewRunEventRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := repository.ListAfter(context.Background(), protocolWorkspaceID, protocolRunID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	legacyTypes := make([]string, 0, len(legacy))
	for _, event := range legacy {
		legacyTypes = append(legacyTypes, event.EventType)
	}
	if !reflect.DeepEqual(legacyTypes, []string{"RUN_STARTED", "STEP_COMPLETED", "RUN_COMPLETED"}) {
		t.Fatalf("compatibility replay types=%v", legacyTypes)
	}
	var stepPayload map[string]string
	if err := json.Unmarshal(legacy[1].Payload, &stepPayload); err != nil || stepPayload["stepId"] != legacyStepID {
		t.Fatalf("compatibility replay changed legacy payload: %s", legacy[1].Payload)
	}
}

func insertLegacyRunEvents(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO run_events(
		 id,workspace_id,run_id,sequence_no,event_type,payload,created_at
		) VALUES
		($1::UUID,$3,$4,1,'RUN_STARTED','{"source":"chat"}','2026-07-20T08:00:00Z'),
		($2::UUID,$3,$4,2,'STEP_COMPLETED',jsonb_build_object('stepId',$2::UUID::TEXT),'2026-07-20T08:00:01Z')
	`, legacyStartedID, legacyStepID, protocolWorkspaceID, protocolRunID); err != nil {
		t.Fatalf("insert legacy nonterminal events: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE agent_runs
		SET status='SUCCEEDED',finished_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2
	`, protocolWorkspaceID, protocolRunID); err != nil {
		t.Fatalf("finish legacy run: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO run_events(
		 id,workspace_id,run_id,sequence_no,event_type,payload,terminal,created_at
		) VALUES($1,$2,$3,3,'RUN_COMPLETED','{"status":"SUCCEEDED"}',TRUE,'2026-07-20T08:00:02Z')
	`, legacyTerminalID, protocolWorkspaceID, protocolRunID); err != nil {
		t.Fatalf("insert legacy terminal event: %v", err)
	}
}

func assertRunEventCounts(t *testing.T, db *sql.DB, runID string, legacy, protocol int) {
	t.Helper()
	var legacyCount, protocolCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM run_events WHERE run_id=$1`, runID).Scan(&legacyCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM protocol_events WHERE run_id=$1`, runID).Scan(&protocolCount); err != nil {
		t.Fatal(err)
	}
	if legacyCount != legacy || protocolCount != protocol {
		t.Fatalf("run %s counts legacy=%d/%d protocol=%d/%d", runID, legacyCount, legacy, protocolCount, protocol)
	}
}
