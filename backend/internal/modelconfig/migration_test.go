// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package modelconfig_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
)

const (
	migrationOwnerID       = "018f1f2e-7b5a-7c3d-8e9f-d234567890ab"
	migrationWorkspaceID   = "018f1f2e-7b5a-7c3d-8e9f-d234567890ac"
	migrationOtherSpaceID  = "018f1f2e-7b5a-7c3d-8e9f-d234567890ad"
	migrationSecretID      = "018f1f2e-7b5a-7c3d-8e9f-d234567890ae"
	migrationOtherSecretID = "018f1f2e-7b5a-7c3d-8e9f-d234567890af"
	migrationConfigID      = "018f1f2e-7b5a-7c3d-8e9f-d234567890b0"
	migrationOtherConfigID = "018f1f2e-7b5a-7c3d-8e9f-d234567890b1"
)

func TestModelConfigMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean model config migration version 7, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertModelConfigMigrationFixtures(t, db)
	assertModelConfigSchema(t, db)
	assertModelConfigConstraints(t, db)

}

func insertModelConfigMigrationFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO users (id, username, display_name)
		VALUES ($1, 'model.migration.owner', 'Model Migration Owner')
	`, migrationOwnerID); err != nil {
		t.Fatalf("insert model migration owner: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (
			id, slug, display_name, mode, owner_user_id, created_by, updated_by
		) VALUES
			($1, 'model-migration', 'Model Migration', 'PRODUCTION', $3, $3, $3),
			($2, 'model-migration-other', 'Model Migration Other', 'SANDBOX', $3, $3, $3)
	`, migrationWorkspaceID, migrationOtherSpaceID, migrationOwnerID); err != nil {
		t.Fatalf("insert model migration workspaces: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO secrets (id, workspace_id, name, kind, created_by, updated_by)
		VALUES
			($1, $3, 'Model Credential', 'API_KEY', $5, $5),
			($2, $4, 'Other Credential', 'API_KEY', $5, $5)
	`, migrationSecretID, migrationOtherSecretID, migrationWorkspaceID, migrationOtherSpaceID, migrationOwnerID); err != nil {
		t.Fatalf("insert model migration secrets: %v", err)
	}
}

func assertModelConfigSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, expected := range []struct {
		column   string
		dataType string
		udtName  string
		nullable string
	}{
		{column: "workspace_id", dataType: "uuid", udtName: "uuid", nullable: "NO"},
		{column: "name", dataType: "USER-DEFINED", udtName: "citext", nullable: "NO"},
		{column: "credential_secret_id", dataType: "uuid", udtName: "uuid", nullable: "YES"},
		{column: "options", dataType: "jsonb", udtName: "jsonb", nullable: "NO"},
		{column: "last_verified_at", dataType: "timestamp with time zone", udtName: "timestamptz", nullable: "YES"},
		{column: "last_latency_ms", dataType: "integer", udtName: "int4", nullable: "YES"},
		{column: "deleted_at", dataType: "timestamp with time zone", udtName: "timestamptz", nullable: "YES"},
	} {
		var dataType string
		var udtName string
		var nullable string
		if err := db.QueryRow(`
			SELECT data_type, udt_name, is_nullable
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'model_configs' AND column_name = $1
		`, expected.column).Scan(&dataType, &udtName, &nullable); err != nil {
			t.Fatalf("query model_configs.%s: %v", expected.column, err)
		}
		if dataType != expected.dataType || udtName != expected.udtName || nullable != expected.nullable {
			t.Fatalf("unexpected model_configs.%s type=%q udt=%q nullable=%q", expected.column, dataType, udtName, nullable)
		}
	}
	for _, forbidden := range []string{"api_key", "api_key_masked", "secret_value", "credential_value"} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = 'model_configs' AND column_name = $1
			)
		`, forbidden).Scan(&exists); err != nil {
			t.Fatalf("query forbidden model config column: %v", err)
		}
		if exists {
			t.Fatalf("model_configs contains secret field %q", forbidden)
		}
	}
}

func assertModelConfigConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO model_configs (
			id, workspace_id, name, provider, api_base, model_name,
			credential_secret_id, created_by, updated_by
		) VALUES ($1, $2, 'Primary Model', 'OPENAI_COMPATIBLE', 'https://models.example/v1', 'gpt-test', $3, $4, $4)
	`, migrationConfigID, migrationWorkspaceID, migrationSecretID, migrationOwnerID); err != nil {
		t.Fatalf("insert model config: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE workspaces SET default_model_config_id = $2 WHERE id = $1
	`, migrationWorkspaceID, migrationConfigID); err != nil {
		t.Fatalf("set same-workspace default model config: %v", err)
	}
	assertModelConfigStatementFails(t, db, `
		INSERT INTO model_configs (
			id, workspace_id, name, provider, api_base, model_name,
			credential_secret_id, created_by, updated_by
		) VALUES ($1, $2, 'PRIMARY MODEL', 'OPENAI_COMPATIBLE', 'https://models.example/v1', 'gpt-test', $3, $4, $4)
	`, migrationOtherConfigID, migrationWorkspaceID, migrationSecretID, migrationOwnerID)
	assertModelConfigStatementFails(t, db, `
		INSERT INTO model_configs (
			id, workspace_id, name, provider, api_base, model_name,
			credential_secret_id, created_by, updated_by
		) VALUES ($1, $2, 'Cross Secret', 'OPENAI_COMPATIBLE', 'https://models.example/v1', 'gpt-test', $3, $4, $4)
	`, migrationOtherConfigID, migrationWorkspaceID, migrationOtherSecretID, migrationOwnerID)
	assertModelConfigStatementFails(t, db, `
		UPDATE workspaces SET default_model_config_id = $2 WHERE id = $1
	`, migrationOtherSpaceID, migrationConfigID)
	assertModelConfigStatementFails(t, db, `
		UPDATE model_configs SET options = '[]'::JSONB WHERE id = $1
	`, migrationConfigID)
	assertModelConfigStatementFails(t, db, `
		UPDATE model_configs SET status = 'CONNECTED' WHERE id = $1
	`, migrationConfigID)
}

func assertModelConfigStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected model config statement to fail: %s", strings.TrimSpace(query))
	}
}

func assertModelConfigTableMissing(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open rolled-back model config database: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.model_configs') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("query rolled-back model config table: %v", err)
	}
	if exists {
		t.Fatal("expected rolled-back model_configs table to be absent")
	}
}
