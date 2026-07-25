package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	postgresmigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/lib/pq"
)

const (
	migrationDirectory = "migrations"
	migrationTable     = "schema_migrations"
)

// migrationFiles is the single source used by the API process, migration CLI,
// and tests. Shipping the SQL in the binaries prevents a container from
// starting with a migration set that differs from the compiled application.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// Version describes the database migration state. Applied is false for a
// database that has not applied any migration yet.
type Version struct {
	Number  uint
	Dirty   bool
	Applied bool
}

// Migrator owns the database connection created by Open. Call Close when the
// operation finishes.
type Migrator struct {
	migrate *migrate.Migrate
}

// Open prepares a PostgreSQL-backed migrator using the SQL files embedded in
// this package.
func Open(ctx context.Context, dsn string) (*Migrator, error) {
	if dsn == "" {
		return nil, errors.New("migration database DSN is required")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open migration database: %w", err)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping migration database: %w", err)
	}

	source, err := iofs.New(migrationFiles, migrationDirectory)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}

	driver, err := postgresmigrate.WithInstance(db, &postgresmigrate.Config{
		MigrationsTable: migrationTable,
	})
	if err != nil {
		_ = source.Close()
		_ = db.Close()
		return nil, fmt.Errorf("initialize postgres migration driver: %w", err)
	}

	instance, err := migrate.NewWithInstance("embedded", source, "postgres", driver)
	if err != nil {
		_ = source.Close()
		_ = driver.Close()
		return nil, fmt.Errorf("initialize migrator: %w", err)
	}
	// A fresh schema now applies the complete domain migration set. Concurrent
	// application replicas must wait for that first migration instead of
	// failing startup while the advisory lock is legitimately held.
	instance.LockTimeout = 2 * time.Minute
	return &Migrator{migrate: instance}, nil
}

// Up applies every pending migration. Re-running it at the latest version is
// successful so deployment jobs remain idempotent.
func (m *Migrator) Up() error {
	if m == nil || m.migrate == nil {
		return errors.New("migrator is not initialized")
	}
	if err := m.migrate.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// Down rolls back exactly steps migrations. Destructive all-the-way-down
// behavior is intentionally not exposed.
func (m *Migrator) Down(steps int) error {
	if m == nil || m.migrate == nil {
		return errors.New("migrator is not initialized")
	}
	if steps < 1 {
		return errors.New("migration down steps must be positive")
	}
	if err := m.migrate.Steps(-steps); err != nil {
		return fmt.Errorf("roll back %d migration(s): %w", steps, err)
	}
	return nil
}

// To migrates to an exact version. It exists primarily for migration tests and
// controlled roll-forward/roll-back operations; normal deployments should use
// Up so they always converge on the compiled migration set.
func (m *Migrator) To(version uint) error {
	if m == nil || m.migrate == nil {
		return errors.New("migrator is not initialized")
	}
	if version < 1 {
		return errors.New("target migration version must be positive")
	}
	if err := m.migrate.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate to version %d: %w", version, err)
	}
	return nil
}

// Version returns the applied migration version and dirty flag.
func (m *Migrator) Version() (Version, error) {
	if m == nil || m.migrate == nil {
		return Version{}, errors.New("migrator is not initialized")
	}
	version, dirty, err := m.migrate.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return Version{}, nil
	}
	if err != nil {
		return Version{}, fmt.Errorf("read migration version: %w", err)
	}
	return Version{Number: version, Dirty: dirty, Applied: true}, nil
}

// Close releases both the embedded source and PostgreSQL connection.
func (m *Migrator) Close() error {
	if m == nil || m.migrate == nil {
		return nil
	}
	sourceErr, databaseErr := m.migrate.Close()
	return errors.Join(sourceErr, databaseErr)
}
