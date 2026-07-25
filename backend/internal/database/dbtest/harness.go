// Package dbtest provides isolated PostgreSQL databases for integration tests.
// Every harness owns a uniquely named database and refuses to drop databases
// outside its reserved prefix.
package dbtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/database"
	_ "github.com/lib/pq"
)

const (
	DefaultPostgresDSN = "postgres://actweave:actweave-dev@127.0.0.1:15432/actweave?sslmode=disable"
	databaseNamePrefix = "actweave_test_"
	adminTimeout       = 30 * time.Second
)

var databaseSequence atomic.Uint64

// Database owns one isolated PostgreSQL database for the lifetime of a test.
type Database struct {
	t            testing.TB
	admin        *sql.DB
	dsn          string
	databaseName string

	mu     sync.Mutex
	closed bool
}

// New creates an isolated database using ACTWEAVE_TEST_POSTGRES_DSN, falling
// back to the repository's local Docker Compose PostgreSQL instance.
func New(t testing.TB) *Database {
	t.Helper()
	baseDSN := os.Getenv("ACTWEAVE_TEST_POSTGRES_DSN")
	if baseDSN == "" {
		baseDSN = DefaultPostgresDSN
	}
	return NewFromDSN(t, baseDSN)
}

// NewFromDSN creates an isolated database on the PostgreSQL server described
// by baseDSN. The database component of baseDSN is never dropped or modified.
func NewFromDSN(t testing.TB, baseDSN string) *Database {
	t.Helper()
	parsed, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse test postgres DSN: %v", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		t.Fatalf("test postgres DSN must use postgres scheme, got %q", parsed.Scheme)
	}

	databaseName := fmt.Sprintf(
		"%s%d_%d",
		databaseNamePrefix,
		time.Now().UTC().UnixNano(),
		databaseSequence.Add(1),
	)
	if err := validateOwnedDatabaseName(databaseName); err != nil {
		t.Fatalf("generate safe test database name: %v", err)
	}

	adminURL := *parsed
	adminURL.Path = "/postgres"
	adminURL.RawPath = ""
	admin, err := sql.Open("postgres", adminURL.String())
	if err != nil {
		t.Fatalf("open postgres admin connection: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), adminTimeout)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("ping postgres admin connection: %v", err)
	}

	testURL := *parsed
	testURL.Path = "/" + databaseName
	testURL.RawPath = ""
	harness := &Database{
		t:            t,
		admin:        admin,
		dsn:          testURL.String(),
		databaseName: databaseName,
	}
	if err := harness.create(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("create isolated test database: %v", err)
	}
	t.Cleanup(func() {
		if err := harness.Close(); err != nil {
			t.Errorf("clean isolated test database: %v", err)
		}
	})
	return harness
}

// DSN returns the connection string for the owned database.
func (d *Database) DSN() string {
	return d.dsn
}

// Name returns the generated database name. It is useful for isolation
// assertions and diagnostics.
func (d *Database) Name() string {
	return d.databaseName
}

// Open opens and verifies an application connection pool. The pool closes
// before the harness cleanup because testing cleanups execute in LIFO order.
func (d *Database) Open(t testing.TB) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", d.dsn)
	if err != nil {
		t.Fatalf("open isolated test database: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Fatalf("ping isolated test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// MigrateToLatest applies the embedded migration set and returns its version.
func (d *Database) MigrateToLatest(t testing.TB) database.Version {
	t.Helper()
	return d.runMigration(t, func(migrator *database.Migrator) error {
		return migrator.Up()
	})
}

// MigrateTo moves the owned database to an exact positive migration version.
func (d *Database) MigrateTo(t testing.TB, version uint) database.Version {
	t.Helper()
	return d.runMigration(t, func(migrator *database.Migrator) error {
		return migrator.To(version)
	})
}

// ResetToLatest drops and recreates only the owned database, then applies all
// migrations. It is safe to call from any existing migration version.
func (d *Database) ResetToLatest(t testing.TB) database.Version {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		t.Fatal("reset closed test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), adminTimeout)
	defer cancel()
	if err := d.drop(ctx); err != nil {
		t.Fatalf("drop isolated database during reset: %v", err)
	}
	if err := d.create(ctx); err != nil {
		t.Fatalf("recreate isolated database during reset: %v", err)
	}
	return d.runMigration(t, func(migrator *database.Migrator) error {
		return migrator.Up()
	})
}

func (d *Database) runMigration(
	t testing.TB,
	operation func(*database.Migrator) error,
) database.Version {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), adminTimeout)
	defer cancel()
	migrator, err := database.Open(ctx, d.dsn)
	if err != nil {
		t.Fatalf("open isolated database migrator: %v", err)
	}
	if err := operation(migrator); err != nil {
		_ = migrator.Close()
		t.Fatalf("migrate isolated database: %v", err)
	}
	version, err := migrator.Version()
	if err != nil {
		_ = migrator.Close()
		t.Fatalf("read isolated database migration version: %v", err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close isolated database migrator: %v", err)
	}
	return version
}

// Close drops only the database created by this harness and closes the admin
// connection. Repeated calls are safe.
func (d *Database) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dropErr := d.drop(ctx)
	closeErr := d.admin.Close()
	return errors.Join(dropErr, closeErr)
}

func (d *Database) create(ctx context.Context) error {
	if err := validateOwnedDatabaseName(d.databaseName); err != nil {
		return err
	}
	_, err := d.admin.ExecContext(
		ctx,
		`CREATE DATABASE `+quoteIdentifier(d.databaseName)+` WITH TEMPLATE template0 ENCODING 'UTF8'`,
	)
	return err
}

func (d *Database) drop(ctx context.Context) error {
	if err := validateOwnedDatabaseName(d.databaseName); err != nil {
		return err
	}
	if _, err := d.admin.ExecContext(
		ctx,
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`,
		d.databaseName,
	); err != nil {
		return fmt.Errorf("terminate test database connections: %w", err)
	}
	if _, err := d.admin.ExecContext(
		ctx,
		`DROP DATABASE IF EXISTS `+quoteIdentifier(d.databaseName),
	); err != nil {
		return fmt.Errorf("drop test database: %w", err)
	}
	return nil
}

func validateOwnedDatabaseName(name string) error {
	if !strings.HasPrefix(name, databaseNamePrefix) || len(name) <= len(databaseNamePrefix) {
		return fmt.Errorf("refusing to manage non-test database %q", name)
	}
	if len(name) > 63 {
		return fmt.Errorf("test database name exceeds PostgreSQL limit: %q", name)
	}
	for _, character := range name {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '_' {
			return fmt.Errorf("test database name contains unsafe character: %q", name)
		}
	}
	return nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
