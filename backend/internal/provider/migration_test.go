// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package provider_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
)

const (
	providerOwnerID      = "018f1f2e-7b5a-7c3d-8e9f-f234567890ab"
	providerWorkspaceID  = "018f1f2e-7b5a-7c3d-8e9f-f234567890ac"
	providerOtherSpaceID = "018f1f2e-7b5a-7c3d-8e9f-f234567890ad"
	providerID           = "018f1f2e-7b5a-7c3d-8e9f-f234567890ae"
	providerOtherID      = "018f1f2e-7b5a-7c3d-8e9f-f234567890af"
	providerAssetID      = "018f1f2e-7b5a-7c3d-8e9f-f234567890b0"
	providerOtherAssetID = "018f1f2e-7b5a-7c3d-8e9f-f234567890b1"
	providerSyncRunID    = "018f1f2e-7b5a-7c3d-8e9f-f234567890b2"
)

func TestProviderMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 3 || version.Dirty {
		t.Fatalf("expected clean provider migration version 8, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertProviderFixtures(t, db)
	assertProviderSchema(t, db)
	assertProviderConstraints(t, db)

}

func insertProviderFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO users (id, username, display_name)
		VALUES ($1, 'provider.owner', 'Provider Owner')
	`, providerOwnerID); err != nil {
		t.Fatalf("insert provider owner: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (
			id, slug, display_name, mode, owner_user_id, created_by, updated_by
		) VALUES
			($1, 'provider-workspace', 'Provider Workspace', 'PRODUCTION', $3, $3, $3),
			($2, 'provider-other', 'Provider Other', 'SANDBOX', $3, $3, $3)
	`, providerWorkspaceID, providerOtherSpaceID, providerOwnerID); err != nil {
		t.Fatalf("insert provider workspaces: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO capability_providers (
			id, workspace_id, name, provider_kind, driver_key, transport,
			endpoint_config, created_by, updated_by
		) VALUES
			($1, $3, 'Orders OpenAPI', 'HTTP_OPENAPI', 'http_openapi', 'HTTP', '{"baseUrl":"https://orders.example"}', $5, $5),
			($2, $4, 'Other OpenAPI', 'HTTP_OPENAPI', 'http_openapi', 'HTTP', '{"baseUrl":"https://other.example"}', $5, $5)
	`, providerID, providerOtherID, providerWorkspaceID, providerOtherSpaceID, providerOwnerID); err != nil {
		t.Fatalf("insert provider fixtures: %v", err)
	}
}

func assertProviderSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, tableName := range []string{"capability_providers", "provider_assets", "provider_sync_runs"} {
		var workspaceColumn bool
		if err := db.QueryRow(`
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = $1
				  AND column_name = 'workspace_id' AND is_nullable = 'NO'
			)
		`, tableName).Scan(&workspaceColumn); err != nil {
			t.Fatalf("query %s workspace column: %v", tableName, err)
		}
		if !workspaceColumn {
			t.Fatalf("%s lacks required workspace_id", tableName)
		}
	}
}

func assertProviderConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO provider_assets (
			id, workspace_id, provider_id, asset_kind, external_id, name,
			input_schema, output_schema, source_revision, source_checksum
		) VALUES ($1, $2, $3, 'TOOL', 'orders.get', 'Get Order',
			'{"type":"object"}', '{"type":"object"}', 'etag-1', 'sha256:first')
	`, providerAssetID, providerWorkspaceID, providerID); err != nil {
		t.Fatalf("insert provider asset: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_sync_runs (
			id, workspace_id, provider_id, status, discovered_count,
			changed_count, error_summary, started_by
		) VALUES ($1, $2, $3, 'SUCCEEDED', 4, 1, '{}', $4)
	`, providerSyncRunID, providerWorkspaceID, providerID, providerOwnerID); err != nil {
		t.Fatalf("insert provider sync run: %v", err)
	}
	assertProviderStatementFails(t, db, `
		INSERT INTO capability_providers (
			id, workspace_id, name, provider_kind, driver_key, transport,
			created_by, updated_by
		) VALUES ($1, $2, 'ORDERS OPENAPI', 'HTTP_OPENAPI', 'http_openapi', 'HTTP', $3, $3)
	`, providerOtherAssetID, providerWorkspaceID, providerOwnerID)
	assertProviderStatementFails(t, db, `
		INSERT INTO provider_assets (
			id, workspace_id, provider_id, asset_kind, external_id, name, source_checksum
		) VALUES ($1, $2, $3, 'TOOL', 'orders.get', 'Duplicate', 'sha256:duplicate')
	`, providerOtherAssetID, providerWorkspaceID, providerID)
	assertProviderStatementFails(t, db, `
		INSERT INTO provider_assets (
			id, workspace_id, provider_id, asset_kind, external_id, name, source_checksum
		) VALUES ($1, $2, $3, 'TOOL', 'cross.asset', 'Cross', 'sha256:cross')
	`, providerOtherAssetID, providerOtherSpaceID, providerID)
	assertProviderStatementFails(t, db, `
		UPDATE provider_assets SET input_schema = '[]'::JSONB WHERE id = $1
	`, providerAssetID)
	assertProviderStatementFails(t, db, `
		INSERT INTO provider_sync_runs (
			id, workspace_id, provider_id, status, discovered_count,
			changed_count, started_by
		) VALUES ($1, $2, $3, 'SUCCEEDED', 1, 2, $4)
	`, providerOtherAssetID, providerWorkspaceID, providerID, providerOwnerID)
	if _, err := db.Exec(`
		UPDATE provider_assets
		SET source_revision = 'etag-2', source_checksum = 'sha256:second', status = 'STALE'
		WHERE id = $1 AND workspace_id = $2
	`, providerAssetID, providerWorkspaceID); err != nil {
		t.Fatalf("update provider asset discovery snapshot: %v", err)
	}
	var providerStatus string
	if err := db.QueryRow(`SELECT status FROM capability_providers WHERE id = $1`, providerID).Scan(&providerStatus); err != nil {
		t.Fatalf("read provider after asset drift: %v", err)
	}
	if providerStatus != "ACTIVE" {
		t.Fatalf("asset drift mutated provider state to %q", providerStatus)
	}
}

func assertProviderStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected provider statement to fail: %s", strings.TrimSpace(query))
	}
}

func assertProviderTablesMissing(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open rolled-back provider database: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, tableName := range []string{"provider_sync_runs", "provider_assets", "capability_providers"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+tableName).Scan(&exists); err != nil {
			t.Fatalf("query rolled-back provider table %s: %v", tableName, err)
		}
		if exists {
			t.Fatalf("expected rolled-back provider table %s to be absent", tableName)
		}
	}
}
