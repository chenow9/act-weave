// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package workflow

import (
	"database/sql"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

func TestWorkflowTrialExecutionSourceMigrationReplays(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("unexpected latest migration: %+v", version)
	}
	db := testDatabase.Open(t)
	assertTrialExecutionSourceSchema(t, db, true)
	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("unexpected down migration: %+v", version)
	}
	assertTrialExecutionSourceSchema(t, db, false)
	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("unexpected replayed migration: %+v", version)
	}
	assertTrialExecutionSourceSchema(t, db, true)
}

func assertTrialExecutionSourceSchema(t *testing.T, db *sql.DB, expected bool) {
	t.Helper()
	var columnExists, sourceConstraintExists, foreignKeyExists bool
	if err := db.QueryRow(`
		SELECT
		 EXISTS(SELECT 1 FROM information_schema.columns
		  WHERE table_schema='public' AND table_name='workflow_executions'
		   AND column_name='compilation_id'),
		 EXISTS(SELECT 1 FROM pg_constraint
		  WHERE conname='workflow_executions_exact_source_check'),
		 EXISTS(SELECT 1 FROM pg_constraint
		  WHERE conname='workflow_executions_workspace_compilation_fk')
	`).Scan(&columnExists, &sourceConstraintExists, &foreignKeyExists); err != nil {
		t.Fatal(err)
	}
	if columnExists != expected || sourceConstraintExists != expected || foreignKeyExists != expected {
		t.Fatalf("trial execution source schema column=%t check=%t fk=%t expected=%t",
			columnExists, sourceConstraintExists, foreignKeyExists, expected)
	}
}
