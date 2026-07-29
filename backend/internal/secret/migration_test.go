// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package secret_test

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
)

const (
	secretOwnerID        = "018f1f2e-7b5a-7c3d-8e9f-b234567890ab"
	secretWorkspaceID    = "018f1f2e-7b5a-7c3d-8e9f-b234567890ac"
	secretOtherSpaceID   = "018f1f2e-7b5a-7c3d-8e9f-b234567890ad"
	secretID             = "018f1f2e-7b5a-7c3d-8e9f-b234567890ae"
	secretVersionID      = "018f1f2e-7b5a-7c3d-8e9f-b234567890af"
	secretOtherID        = "018f1f2e-7b5a-7c3d-8e9f-b234567890b0"
	secretOtherVersionID = "018f1f2e-7b5a-7c3d-8e9f-b234567890b1"
)

func TestSecretMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean secret migration version 6, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertSecretWorkspaceFixtures(t, db)

	assertSecretSchema(t, db)
	assertSecretVersionConstraints(t, db)

}

func insertSecretWorkspaceFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO users (id, username, display_name)
		VALUES ($1, 'secret.owner', 'Secret Owner')
	`, secretOwnerID); err != nil {
		t.Fatalf("insert secret owner: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (
			id, slug, display_name, mode, owner_user_id, created_by, updated_by
		) VALUES
			($1, 'secret-workspace', 'Secret Workspace', 'PRODUCTION', $3, $3, $3),
			($2, 'secret-other', 'Secret Other', 'SANDBOX', $3, $3, $3)
	`, secretWorkspaceID, secretOtherSpaceID, secretOwnerID); err != nil {
		t.Fatalf("insert secret workspaces: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_members (workspace_id, user_id, role, invited_by)
		VALUES
			($1, $3, 'OWNER', $3),
			($2, $3, 'OWNER', $3)
	`, secretWorkspaceID, secretOtherSpaceID, secretOwnerID); err != nil {
		t.Fatalf("insert secret workspace members: %v", err)
	}
}

func assertSecretSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	type expectedColumn struct {
		table    string
		column   string
		dataType string
		udtName  string
		nullable string
	}
	for _, expected := range []expectedColumn{
		{table: "secrets", column: "workspace_id", dataType: "uuid", udtName: "uuid", nullable: "NO"},
		{table: "secrets", column: "name", dataType: "USER-DEFINED", udtName: "citext", nullable: "NO"},
		{table: "secrets", column: "active_version_id", dataType: "uuid", udtName: "uuid", nullable: "YES"},
		{table: "secrets", column: "lock_version", dataType: "bigint", udtName: "int8", nullable: "NO"},
		{table: "secret_versions", column: "workspace_id", dataType: "uuid", udtName: "uuid", nullable: "NO"},
		{table: "secret_versions", column: "version_no", dataType: "bigint", udtName: "int8", nullable: "NO"},
		{table: "secret_versions", column: "ciphertext", dataType: "bytea", udtName: "bytea", nullable: "NO"},
		{table: "secret_versions", column: "nonce", dataType: "bytea", udtName: "bytea", nullable: "NO"},
		{table: "secret_versions", column: "revoked_at", dataType: "timestamp with time zone", udtName: "timestamptz", nullable: "YES"},
	} {
		var dataType string
		var udtName string
		var nullable string
		if err := db.QueryRow(`
			SELECT data_type, udt_name, is_nullable
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		`, expected.table, expected.column).Scan(&dataType, &udtName, &nullable); err != nil {
			t.Fatalf("query %s.%s: %v", expected.table, expected.column, err)
		}
		if dataType != expected.dataType || udtName != expected.udtName || nullable != expected.nullable {
			t.Fatalf(
				"unexpected %s.%s: dataType=%q udtName=%q nullable=%q",
				expected.table,
				expected.column,
				dataType,
				udtName,
				nullable,
			)
		}
	}

	rows, err := db.Query(`
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name IN ('secrets', 'secret_versions')
		ORDER BY column_name
	`)
	if err != nil {
		t.Fatalf("query secret columns: %v", err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatalf("scan secret column: %v", err)
		}
		columns = append(columns, strings.ToLower(column))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate secret columns: %v", err)
	}
	for _, forbidden := range []string{"plaintext", "secret_value", "token_value", "password"} {
		position := sort.SearchStrings(columns, forbidden)
		if position < len(columns) && columns[position] == forbidden {
			t.Fatalf("secret schema contains plaintext column %q", forbidden)
		}
	}
}

func assertSecretVersionConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO secrets (
			id, workspace_id, name, kind, created_by, updated_by
		) VALUES ($1, $2, 'Model API Key', 'API_KEY', $3, $3)
	`, secretID, secretWorkspaceID, secretOwnerID); err != nil {
		t.Fatalf("insert secret: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO secret_versions (
			id, workspace_id, secret_id, version_no, ciphertext, nonce,
			key_id, fingerprint, created_by
		) VALUES ($1, $2, $3, 1, $4, $5, 'local-master-v1', 'sha256:abcd', $6)
	`, secretVersionID, secretWorkspaceID, secretID, []byte("ciphertext"), []byte("nonce"), secretOwnerID); err != nil {
		t.Fatalf("insert secret version: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE secrets
		SET active_version_id = $2, lock_version = lock_version + 1
		WHERE id = $1 AND workspace_id = $3
	`, secretID, secretVersionID, secretWorkspaceID); err != nil {
		t.Fatalf("activate secret version: %v", err)
	}

	assertSecretStatementFails(t, db, `
		INSERT INTO secrets (
			id, workspace_id, name, kind, created_by, updated_by
		) VALUES ($1, $2, 'MODEL API KEY', 'API_KEY', $3, $3)
	`, secretOtherID, secretWorkspaceID, secretOwnerID)
	assertSecretStatementFails(t, db, `
		INSERT INTO secret_versions (
			id, workspace_id, secret_id, version_no, ciphertext, nonce,
			key_id, fingerprint, created_by
		) VALUES ($1, $2, $3, 1, $4, $5, 'local-master-v1', 'sha256:duplicate', $6)
	`, secretOtherVersionID, secretWorkspaceID, secretID, []byte("ciphertext"), []byte("nonce"), secretOwnerID)
	assertSecretStatementFails(t, db, `
		INSERT INTO secret_versions (
			id, workspace_id, secret_id, version_no, ciphertext, nonce,
			key_id, fingerprint, created_by
		) VALUES ($1, $2, $3, 2, $4, $5, 'local-master-v1', 'sha256:cross', $6)
	`, secretOtherVersionID, secretOtherSpaceID, secretID, []byte("ciphertext"), []byte("nonce"), secretOwnerID)
	assertSecretStatementFails(t, db, `
		INSERT INTO secret_versions (
			id, workspace_id, secret_id, version_no, ciphertext, nonce,
			key_id, fingerprint, created_by
		) VALUES ($1, $2, $3, 2, ''::BYTEA, $4, 'local-master-v1', 'sha256:empty', $5)
	`, secretOtherVersionID, secretWorkspaceID, secretID, []byte("nonce"), secretOwnerID)

	if _, err := db.Exec(`
		INSERT INTO secrets (
			id, workspace_id, name, kind, created_by, updated_by
		) VALUES ($1, $2, 'Other Secret', 'BEARER_TOKEN', $3, $3)
	`, secretOtherID, secretOtherSpaceID, secretOwnerID); err != nil {
		t.Fatalf("insert other secret: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO secret_versions (
			id, workspace_id, secret_id, version_no, ciphertext, nonce,
			key_id, fingerprint, created_by
		) VALUES ($1, $2, $3, 1, $4, $5, 'local-master-v1', 'sha256:other', $6)
	`, secretOtherVersionID, secretOtherSpaceID, secretOtherID, []byte("ciphertext"), []byte("nonce"), secretOwnerID); err != nil {
		t.Fatalf("insert other secret version: %v", err)
	}
	assertSecretStatementFails(t, db, `
		UPDATE secrets SET active_version_id = $2 WHERE id = $1
	`, secretID, secretOtherVersionID)
	assertSecretStatementFails(t, db, `DELETE FROM secret_versions WHERE id = $1`, secretVersionID)
	assertSecretStatementFails(t, db, `DELETE FROM secrets WHERE id = $1`, secretID)
}

func assertSecretStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected secret statement to fail: %s", strings.TrimSpace(query))
	}
}

func assertSecretTablesMissing(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open rolled-back secret database: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, tableName := range []string{"secret_versions", "secrets"} {
		var exists bool
		if err := db.QueryRowContext(
			ctx,
			`SELECT to_regclass($1) IS NOT NULL`,
			"public."+tableName,
		).Scan(&exists); err != nil {
			t.Fatalf("query rolled-back table %s: %v", tableName, err)
		}
		if exists {
			t.Fatalf("expected rolled-back table %s to be absent", tableName)
		}
	}
}
