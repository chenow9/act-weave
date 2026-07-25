package database_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"actweave/backend/internal/database"
	"actweave/backend/internal/database/dbtest"
	_ "github.com/lib/pq"
)

func TestPostgresBaselineMigration(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDSN := testDatabase.DSN()

	applyMigrations(t, testDSN, func(migrator *database.Migrator) {
		if err := migrator.To(2); err != nil {
			t.Fatalf("apply baseline migrations: %v", err)
		}
		assertMigrationVersion(t, migrator, 2)
	})
	assertPostgresBaseline(t, testDSN, true)

	applyMigrations(t, testDSN, func(migrator *database.Migrator) {
		if err := migrator.Down(1); err != nil {
			t.Fatalf("roll back baseline migration: %v", err)
		}
		assertMigrationVersion(t, migrator, 1)
	})
	assertPostgresBaseline(t, testDSN, false)

	applyMigrations(t, testDSN, func(migrator *database.Migrator) {
		if err := migrator.To(2); err != nil {
			t.Fatalf("reapply baseline migration: %v", err)
		}
		assertMigrationVersion(t, migrator, 2)
		if err := migrator.To(2); err != nil {
			t.Fatalf("reapply baseline target as no-op: %v", err)
		}
	})
	assertPostgresBaseline(t, testDSN, true)
}

func applyMigrations(t *testing.T, dsn string, operation func(*database.Migrator)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	migrator, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open isolated migrator: %v", err)
	}
	operation(migrator)
	if err := migrator.Close(); err != nil {
		t.Fatalf("close isolated migrator: %v", err)
	}
}

func assertMigrationVersion(t *testing.T, migrator *database.Migrator, want uint) {
	t.Helper()
	version, err := migrator.Version()
	if err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if !version.Applied || version.Number != want || version.Dirty {
		t.Fatalf("expected clean migration version %d, got %+v", want, version)
	}
}

func assertPostgresBaseline(t *testing.T, dsn string, wantInstalled bool) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var extensionInstalled bool
	if err := db.QueryRowContext(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citext')`,
	).Scan(&extensionInstalled); err != nil {
		t.Fatalf("query citext extension: %v", err)
	}
	if extensionInstalled != wantInstalled {
		t.Fatalf("expected citext installed=%t, got %t", wantInstalled, extensionInstalled)
	}

	if wantInstalled {
		var timezone string
		if err := db.QueryRowContext(ctx, `SHOW TimeZone`).Scan(&timezone); err != nil {
			t.Fatalf("query database timezone: %v", err)
		}
		if timezone != "UTC" {
			t.Fatalf("expected database timezone UTC, got %q", timezone)
		}
	}

	rows, err := db.QueryContext(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = 'public'
		  AND table_type = 'BASE TABLE'
		  AND table_name <> 'schema_migrations'
	`)
	if err != nil {
		t.Fatalf("query baseline tables: %v", err)
	}
	defer rows.Close()
	var unexpectedTables []string
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			t.Fatalf("scan baseline table: %v", err)
		}
		unexpectedTables = append(unexpectedTables, tableName)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate baseline tables: %v", err)
	}
	if len(unexpectedTables) != 0 {
		t.Fatalf("baseline migration must not create business tables, got %v", unexpectedTables)
	}
}
