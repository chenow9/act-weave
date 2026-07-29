// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
// Historical step-migration tests were retired when migrations were squashed
// into 000001_init. See migrations_archive/ for the pre-squash chain.
package database_test

import (
	"database/sql"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

func TestWorkflowGenerateSessionsMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean workflow generate sessions migration version 59, got %+v", version)
	}
	db := testDatabase.Open(t)
	assertWorkflowGenerateSessionsSchema(t, db, true)

	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean rollback to version 58, got %+v", version)
	}
	assertWorkflowGenerateSessionsSchema(t, db, false)

	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean reapply version 59, got %+v", version)
	}
	assertWorkflowGenerateSessionsSchema(t, db, true)
}

func assertWorkflowGenerateSessionsSchema(t *testing.T, db *sql.DB, wantPresent bool) {
	t.Helper()
	var sessions, turns bool
	if err := db.QueryRow(`
		SELECT
		 EXISTS (
		  SELECT 1 FROM information_schema.tables
		  WHERE table_schema = 'public' AND table_name = 'workflow_generate_sessions'
		 ),
		 EXISTS (
		  SELECT 1 FROM information_schema.tables
		  WHERE table_schema = 'public' AND table_name = 'workflow_generate_turns'
		 )
	`).Scan(&sessions, &turns); err != nil {
		t.Fatalf("query generate session tables: %v", err)
	}
	if sessions != wantPresent || turns != wantPresent {
		t.Fatalf("expected tables present=%t, got sessions=%t turns=%t", wantPresent, sessions, turns)
	}
	if !wantPresent {
		return
	}

	var statusCheck, closedCheck, turnStatusCheck, turnIndexUnique bool
	if err := db.QueryRow(`
		SELECT
		 EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'workflow_generate_sessions_status_check'),
		 EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'workflow_generate_sessions_closed_state_check'),
		 EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'workflow_generate_turns_status_check'),
		 EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'workflow_generate_turns_session_turn_index_key')
	`).Scan(&statusCheck, &closedCheck, &turnStatusCheck, &turnIndexUnique); err != nil {
		t.Fatalf("query generate session constraints: %v", err)
	}
	if !statusCheck || !closedCheck || !turnStatusCheck || !turnIndexUnique {
		t.Fatalf("missing expected constraints: status=%t closed=%t turnStatus=%t turnIndex=%t",
			statusCheck, closedCheck, turnStatusCheck, turnIndexUnique)
	}
}
