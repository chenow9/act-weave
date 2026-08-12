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

	// Walk the full chain and assert latest version.
	current := version
	var last uint = version
	for {
		next, err := source.Next(current)
		if err != nil {
			break
		}
		if next != current+1 {
			t.Fatalf("expected sequential migration versions, got %d after %d", next, current)
		}
		current = next
		last = next
	}
	// Latest embedded migration (000020_model_config_provider_canonicalization).
	const wantLatest = 20
	if last != wantLatest {
		t.Fatalf("expected latest embedded migration version %d, got %d (update wantLatest when adding migrations)", wantLatest, last)
	}

	for _, item := range []struct {
		version    uint
		identifier string
	}{
		{version: 1, identifier: "init"},
		{version: 2, identifier: "session_context_contracts"},
		{version: 3, identifier: "chat_context_summaries"},
		{version: 4, identifier: "agent_context_llm_compaction"},
		{version: 5, identifier: "agent_access_cors_loopback"},
		{version: 6, identifier: "aap_files"},
		{version: 7, identifier: "aap_file_grant_scopes"},
		{version: 8, identifier: "aap_file_data_commands"},
		{version: 9, identifier: "agent_delegation_a2a"},
		{version: 10, identifier: "agent_delegation_hardening"},
		{version: 11, identifier: "delegation_execution_lease"},
		{version: 12, identifier: "inbound_lease_outbox_claim"},
		{version: 13, identifier: "delegation_audit_tokens"},
		{version: 14, identifier: "delegation_attempt_invariant"},
		{version: 15, identifier: "inbound_request_hash_sticky"},
		{version: 16, identifier: "inbound_task_aliases"},
		{version: 17, identifier: "a2a_task_principal_store"},
		{version: 18, identifier: "terminal_delegation_step_immutable"},
		{version: 19, identifier: "agentic_capabilities_context_assembly"},
		{version: 20, identifier: "model_config_provider_canonicalization"},
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
