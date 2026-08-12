package storedobject

import (
	"testing"

	"actweave/backend/internal/database/dbtest"
)

func TestPermanentContentReferenceMigration(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 20 || version.Dirty {
		t.Fatalf("permanent content migration version = %+v", version)
	}
	db := testDatabase.Open(t)
	for table, columns := range map[string][]string{
		"prompt_runs":     {"input_sha256", "input_length", "output_sha256", "output_length"},
		"chat_messages":   {"content_length"},
		"agent_run_steps": {"raw_sha256", "raw_length"},
	} {
		for _, column := range columns {
			var exists bool
			if err := db.QueryRow(`
				SELECT EXISTS(SELECT 1 FROM information_schema.columns
				 WHERE table_schema='public' AND table_name=$1 AND column_name=$2)
			`, table, column).Scan(&exists); err != nil || !exists {
				t.Fatalf("expected %s.%s: exists=%v err=%v", table, column, exists, err)
			}
		}
	}
	for _, constraint := range []string{
		"prompt_runs_input_object_fk", "prompt_runs_output_object_fk",
		"chat_messages_content_carrier_check", "chat_messages_content_object_fk",
		"agent_run_steps_raw_evidence_check", "agent_run_steps_raw_object_fk",
	} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname=$1)
		`, constraint).Scan(&exists); err != nil || !exists {
			t.Fatalf("expected constraint %s: exists=%v err=%v", constraint, exists, err)
		}
	}
}
