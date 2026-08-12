package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMigrationTestHarnessRebuildsFromAnyVersion(t *testing.T) {
	database := New(t)

	version := database.MigrateToLatest(t)
	if !version.Applied || version.Number != 20 || version.Dirty {
		t.Fatalf("expected clean version 6, got %+v", version)
	}
	db := database.Open(t)
	if _, err := db.Exec(`CREATE TABLE reset_probe (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create reset probe: %v", err)
	}

	version = database.ResetToLatest(t)
	if !version.Applied || version.Number != 20 || version.Dirty {
		t.Fatalf("expected a clean latest version 6, got %+v", version)
	}
	assertTableMissing(t, database.DSN(), "reset_probe")
}

func TestMigrationTestHarnessUsesParallelIsolation(t *testing.T) {
	const parallelDatabases = 3
	names := make(chan string, parallelDatabases)
	for index := 0; index < parallelDatabases; index++ {
		index := index
		t.Run(fmt.Sprintf("database-%d", index), func(t *testing.T) {
			t.Parallel()
			database := New(t)
			database.MigrateToLatest(t)
			db := database.Open(t)
			if _, err := db.Exec(`CREATE TABLE parallel_probe (id INTEGER PRIMARY KEY)`); err != nil {
				t.Fatalf("create parallel probe: %v", err)
			}
			names <- database.Name()
		})
	}
	t.Cleanup(func() {
		close(names)
		seen := map[string]bool{}
		for name := range names {
			if seen[name] {
				t.Errorf("parallel harness reused database name %q", name)
			}
			seen[name] = true
		}
		if len(seen) != parallelDatabases {
			t.Errorf("expected %d isolated databases, got %d", parallelDatabases, len(seen))
		}
	})
}

func TestMigrationTestHarnessRejectsUnsafeDatabaseNames(t *testing.T) {
	for _, name := range []string{
		"actweave",
		"postgres",
		databaseNamePrefix,
		databaseNamePrefix + "unsafe-name",
		databaseNamePrefix + strings.Repeat("x", 64),
	} {
		if err := validateOwnedDatabaseName(name); err == nil {
			t.Fatalf("expected unsafe database name %q to be rejected", name)
		}
	}
	if err := validateOwnedDatabaseName(databaseNamePrefix + "123_1"); err != nil {
		t.Fatalf("expected generated-style test database name to pass: %v", err)
	}
}

func assertTableMissing(t *testing.T, dsn string, tableName string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open reset database: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var exists bool
	if err := db.QueryRowContext(
		ctx,
		`SELECT to_regclass('public.' || $1) IS NOT NULL`,
		tableName,
	).Scan(&exists); err != nil {
		t.Fatalf("query reset probe: %v", err)
	}
	if exists {
		t.Fatalf("expected table %q to be removed by reset", tableName)
	}
}
