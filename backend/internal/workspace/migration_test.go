// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package workspace_test

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
)

const (
	workspaceOwnerID = "018f1f2e-7b5a-7c3d-8e9f-7234567890ab"
	workspaceID      = "018f1f2e-7b5a-7c3d-8e9f-7234567890ac"
	missingUserID    = "018f1f2e-7b5a-7c3d-8e9f-7234567890ad"
	unknownUserID    = "018f1f2e-7b5a-7c3d-8e9f-7234567890ae"
)

func TestWorkspaceMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean workspace migration version 5, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertWorkspaceOwner(t, db)

	assertWorkspaceSchema(t, db)
	assertWorkspaceRBACConstraints(t, db)

}

func insertWorkspaceOwner(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO users (id, username, display_name)
		VALUES ($1, 'workspace.owner', 'Workspace Owner')
	`, workspaceOwnerID); err != nil {
		t.Fatalf("insert workspace owner user: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (
			id, slug, display_name, mode, owner_user_id, created_by, updated_by, settings
		) VALUES (
			$1, 'Operations', 'Operations Workspace', 'PRODUCTION', $2, $2, $2,
			'{"schemaVersion":"workspace.settings.v1"}'::JSONB
		)
	`, workspaceID, workspaceOwnerID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_members (workspace_id, user_id, role, invited_by)
		VALUES ($1, $2, 'OWNER', $2)
	`, workspaceID, workspaceOwnerID); err != nil {
		t.Fatalf("insert workspace owner member: %v", err)
	}
}

func assertWorkspaceSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	type expectedColumn struct {
		table    string
		column   string
		dataType string
		udtName  string
		nullable string
	}
	for _, expected := range []expectedColumn{
		{table: "workspaces", column: "id", dataType: "uuid", udtName: "uuid", nullable: "NO"},
		{table: "workspaces", column: "slug", dataType: "USER-DEFINED", udtName: "citext", nullable: "NO"},
		{table: "workspaces", column: "owner_user_id", dataType: "uuid", udtName: "uuid", nullable: "NO"},
		{table: "workspaces", column: "default_agent_id", dataType: "uuid", udtName: "uuid", nullable: "YES"},
		{table: "workspaces", column: "default_model_config_id", dataType: "uuid", udtName: "uuid", nullable: "YES"},
		{table: "workspaces", column: "settings", dataType: "jsonb", udtName: "jsonb", nullable: "NO"},
		{table: "workspaces", column: "lock_version", dataType: "bigint", udtName: "int8", nullable: "NO"},
		{table: "workspaces", column: "deleted_at", dataType: "timestamp with time zone", udtName: "timestamptz", nullable: "YES"},
		{table: "workspace_members", column: "workspace_id", dataType: "uuid", udtName: "uuid", nullable: "NO"},
		{table: "workspace_members", column: "user_id", dataType: "uuid", udtName: "uuid", nullable: "NO"},
		{table: "workspace_members", column: "disabled_at", dataType: "timestamp with time zone", udtName: "timestamptz", nullable: "YES"},
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
		  AND table_name IN ('workspaces', 'workspace_members')
		ORDER BY column_name
	`)
	if err != nil {
		t.Fatalf("query workspace columns: %v", err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatalf("scan workspace column: %v", err)
		}
		columns = append(columns, strings.ToLower(column))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate workspace columns: %v", err)
	}
	for _, forbidden := range []string{"visibility", "private", "resource_grant", "resourcegrant"} {
		if sort.SearchStrings(columns, forbidden) < len(columns) && columns[sort.SearchStrings(columns, forbidden)] == forbidden {
			t.Fatalf("workspace schema contains forbidden resource ACL column %q", forbidden)
		}
	}
}

func assertWorkspaceRBACConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	for index, role := range []string{"ADMIN", "EDITOR", "OPERATOR", "VIEWER"} {
		userID := fmt.Sprintf("018f1f2e-7b5a-7c3d-8e9f-7234567891%02d", index+1)
		username := "member." + strings.ToLower(role)
		if _, err := db.Exec(`
			INSERT INTO users (id, username, display_name)
			VALUES ($1, $2, $3)
		`, userID, username, role+" Member"); err != nil {
			t.Fatalf("insert %s user: %v", role, err)
		}
		if _, err := db.Exec(`
			INSERT INTO workspace_members (workspace_id, user_id, role, invited_by)
			VALUES ($1, $2, $3, $4)
		`, workspaceID, userID, role, workspaceOwnerID); err != nil {
			t.Fatalf("insert %s member: %v", role, err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO users (id, username, display_name)
		VALUES ($1, 'member.unassigned', 'Unassigned Member')
	`, missingUserID); err != nil {
		t.Fatalf("insert unassigned member user: %v", err)
	}
	assertWorkspaceStatementFails(t, db, `
		INSERT INTO workspace_members (workspace_id, user_id, role)
		VALUES ($1, $2, 'PRIVATE')
	`, workspaceID, missingUserID)
	assertWorkspaceStatementFails(t, db, `
		INSERT INTO workspace_members (workspace_id, user_id, role)
		VALUES ($1, $2, 'EDITOR')
	`, workspaceID, unknownUserID)
	assertWorkspaceStatementFails(t, db, `DELETE FROM users WHERE id = $1`, workspaceOwnerID)
	assertWorkspaceStatementFails(t, db, `DELETE FROM workspaces WHERE id = $1`, workspaceID)
	assertWorkspaceStatementFails(t, db, `
		UPDATE workspaces SET settings = '[]'::JSONB WHERE id = $1
	`, workspaceID)
	assertWorkspaceStatementFails(t, db, `
		INSERT INTO workspaces (
			id, slug, display_name, mode, owner_user_id, created_by, updated_by
		) VALUES ($1, 'operations', 'Duplicate Workspace', 'SANDBOX', $2, $2, $2)
	`, "018f1f2e-7b5a-7c3d-8e9f-7234567890ff", workspaceOwnerID)
}

func assertWorkspaceStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected workspace statement to fail: %s", strings.TrimSpace(query))
	}
}

func assertWorkspaceTablesMissing(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open rolled-back workspace database: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, tableName := range []string{"workspace_members", "workspaces"} {
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
