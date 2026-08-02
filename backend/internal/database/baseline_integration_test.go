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
		if err := migrator.Up(); err != nil {
			t.Fatalf("apply migrations: %v", err)
		}
		// Latest: 1 init + 2 session context + 3 summaries + 4 LLM compact +
		// 5 cors loopback + 6 aap files.
		assertMigrationVersion(t, migrator, 18)
	})
	assertPostgresBaseline(t, testDSN, true)

	applyMigrations(t, testDSN, func(migrator *database.Migrator) {
		if err := migrator.Down(18); err != nil {
			t.Fatalf("roll back all migrations: %v", err)
		}
		version, err := migrator.Version()
		if err != nil {
			t.Fatalf("read migration version after down: %v", err)
		}
		if version.Applied {
			t.Fatalf("expected no applied migration after full down, got %+v", version)
		}
	})
	assertPostgresBaseline(t, testDSN, false)

	applyMigrations(t, testDSN, func(migrator *database.Migrator) {
		if err := migrator.Up(); err != nil {
			t.Fatalf("reapply migrations: %v", err)
		}
		assertMigrationVersion(t, migrator, 18)
		if err := migrator.Up(); err != nil {
			t.Fatalf("reapply latest as no-op: %v", err)
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
}
