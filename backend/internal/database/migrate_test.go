package database

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func TestEmbeddedMigrationSetStartsWithBaselineThenSessionContext(t *testing.T) {
	source, err := iofs.New(migrationFiles, migrationDirectory)
	if err != nil {
		t.Fatalf("open embedded migrations: %v", err)
	}
	t.Cleanup(func() { _ = source.Close() })

	version, err := source.First()
	if err != nil {
		t.Fatalf("read first migration: %v", err)
	}
	if version != 1 {
		t.Fatalf("expected first migration version 1, got %d", version)
	}
	next, err := source.Next(version)
	if err != nil {
		t.Fatalf("expected session-context migration after baseline: %v", err)
	}
	if next != 2 {
		t.Fatalf("expected second migration version 2, got %d", next)
	}
	if _, err := source.Next(next); err == nil {
		t.Fatal("expected only two embedded migrations (baseline + session context contracts)")
	}

	for _, item := range []struct {
		version    uint
		identifier string
	}{
		{version: 1, identifier: "init"},
		{version: 2, identifier: "session_context_contracts"},
	} {
		for _, direction := range []struct {
			name string
			read func(uint) (io.ReadCloser, string, error)
		}{
			{name: "up", read: source.ReadUp},
			{name: "down", read: source.ReadDown},
		} {
			t.Run(item.identifier+"/"+direction.name, func(t *testing.T) {
				reader, identifier, err := direction.read(item.version)
				if err != nil {
					t.Fatalf("read %s migration: %v", direction.name, err)
				}
				defer reader.Close()

				body, err := io.ReadAll(reader)
				if err != nil {
					t.Fatalf("read %s migration body: %v", direction.name, err)
				}
				if identifier != item.identifier {
					t.Fatalf("unexpected migration identifier %q", identifier)
				}
				if len(strings.TrimSpace(string(body))) == 0 {
					t.Fatalf("%s migration body must not be empty", direction.name)
				}
			})
		}
	}
}

func TestOpenRejectsEmptyDSN(t *testing.T) {
	_, err := Open(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "DSN is required") {
		t.Fatalf("expected a clear empty DSN error, got %v", err)
	}
}

func TestUninitializedMigratorOperationsFailClearly(t *testing.T) {
	var migrator *Migrator
	if err := migrator.Up(); err == nil {
		t.Fatal("expected uninitialized Up to fail")
	}
	if err := migrator.Down(1); err == nil {
		t.Fatal("expected uninitialized Down to fail")
	}
	if err := migrator.To(1); err == nil {
		t.Fatal("expected uninitialized To to fail")
	}
	if _, err := migrator.Version(); err == nil {
		t.Fatal("expected uninitialized Version to fail")
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("nil Close must be safe: %v", err)
	}
}
