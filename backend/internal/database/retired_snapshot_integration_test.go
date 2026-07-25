package database_test

import (
	"testing"

	"actweave/backend/internal/database/dbtest"
)

func TestLatestSchemaHasNoAggregateSnapshotTable(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)

	var matchingTables int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM information_schema.tables t
		WHERE t.table_schema='public' AND t.table_type='BASE TABLE'
		  AND EXISTS (
			SELECT 1 FROM information_schema.columns c
			WHERE c.table_schema=t.table_schema AND c.table_name=t.table_name
			  AND c.column_name='state_key'
		  )
		  AND EXISTS (
			SELECT 1 FROM information_schema.columns c
			WHERE c.table_schema=t.table_schema AND c.table_name=t.table_name
			  AND c.column_name='payload' AND c.data_type='jsonb'
		  )
	`).Scan(&matchingTables); err != nil {
		t.Fatalf("inspect latest schema for aggregate snapshot shape: %v", err)
	}
	if matchingTables != 0 {
		t.Fatalf("latest schema contains %d retired aggregate snapshot table(s)", matchingTables)
	}
}
