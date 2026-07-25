package identity_test

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
	userID       = "018f1f2e-7b5a-7c3d-8e9f-1234567890ab"
	sessionID    = "018f1f2e-7b5a-7c3d-8e9f-1234567890ac"
	secondUserID = "018f1f2e-7b5a-7c3d-8e9f-1234567890ad"
)

func TestIdentityMigration(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateTo(t, 3)
	if !version.Applied || version.Number != 3 || version.Dirty {
		t.Fatalf("expected clean identity migration version 3, got %+v", version)
	}
	db := testDatabase.Open(t)

	assertIdentityColumns(t, db)
	assertIdentityIndexes(t, db)
	assertIdentityConstraintsAndDefaults(t, db)

	version = testDatabase.MigrateTo(t, 2)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean rollback to version 2, got %+v", version)
	}
	assertTablesMissing(t, testDatabase.DSN(), "users", "user_credentials", "auth_sessions")

	version = testDatabase.MigrateTo(t, 3)
	if !version.Applied || version.Number != 3 || version.Dirty {
		t.Fatalf("expected clean identity migration reapply, got %+v", version)
	}
}

func assertIdentityColumns(t *testing.T, db *sql.DB) {
	t.Helper()
	type column struct {
		table    string
		name     string
		dataType string
		udtName  string
		nullable bool
	}
	want := []column{
		{table: "users", name: "id", dataType: "uuid", udtName: "uuid"},
		{table: "users", name: "username", dataType: "USER-DEFINED", udtName: "citext"},
		{table: "users", name: "email", dataType: "USER-DEFINED", udtName: "citext", nullable: true},
		{table: "users", name: "display_name", dataType: "character varying", udtName: "varchar"},
		{table: "users", name: "last_login_at", dataType: "timestamp with time zone", udtName: "timestamptz", nullable: true},
		{table: "user_credentials", name: "user_id", dataType: "uuid", udtName: "uuid"},
		{table: "user_credentials", name: "password_hash", dataType: "text", udtName: "text"},
		{table: "user_credentials", name: "locked_until", dataType: "timestamp with time zone", udtName: "timestamptz", nullable: true},
		{table: "auth_sessions", name: "id", dataType: "uuid", udtName: "uuid"},
		{table: "auth_sessions", name: "refresh_token_hash", dataType: "text", udtName: "text"},
		{table: "auth_sessions", name: "ip", dataType: "inet", udtName: "inet", nullable: true},
		{table: "auth_sessions", name: "expires_at", dataType: "timestamp with time zone", udtName: "timestamptz"},
	}
	for _, expected := range want {
		var dataType string
		var udtName string
		var nullable string
		err := db.QueryRow(`
			SELECT data_type, udt_name, is_nullable
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		`, expected.table, expected.name).Scan(&dataType, &udtName, &nullable)
		if err != nil {
			t.Fatalf("query %s.%s: %v", expected.table, expected.name, err)
		}
		if dataType != expected.dataType || udtName != expected.udtName || (nullable == "YES") != expected.nullable {
			t.Fatalf(
				"unexpected %s.%s definition: dataType=%q udtName=%q nullable=%q",
				expected.table,
				expected.name,
				dataType,
				udtName,
				nullable,
			)
		}
	}

	forbiddenPlaintextColumns := map[string]bool{
		"password":      true,
		"refresh_token": true,
		"access_token":  true,
		"token":         true,
	}
	rows, err := db.Query(`
		SELECT table_name, column_name
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name IN ('users', 'user_credentials', 'auth_sessions')
	`)
	if err != nil {
		t.Fatalf("query identity columns for plaintext check: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var tableName string
		var columnName string
		if err := rows.Scan(&tableName, &columnName); err != nil {
			t.Fatalf("scan identity column: %v", err)
		}
		if forbiddenPlaintextColumns[columnName] {
			t.Fatalf("identity table %s contains forbidden plaintext column %s", tableName, columnName)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate identity columns: %v", err)
	}
}

func assertIdentityIndexes(t *testing.T, db *sql.DB) {
	t.Helper()
	want := []string{
		"auth_sessions_expires_at_idx",
		"auth_sessions_refresh_token_hash_key",
		"auth_sessions_user_active_idx",
		"user_credentials_locked_until_idx",
		"users_email_key",
		"users_status_updated_idx",
		"users_username_key",
	}
	rows, err := db.Query(`
		SELECT indexname
		FROM pg_indexes
		WHERE schemaname = 'public'
		  AND tablename IN ('users', 'user_credentials', 'auth_sessions')
		  AND indexname <> ALL(ARRAY['users_pkey', 'user_credentials_pkey', 'auth_sessions_pkey'])
		ORDER BY indexname
	`)
	if err != nil {
		t.Fatalf("query identity indexes: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var indexName string
		if err := rows.Scan(&indexName); err != nil {
			t.Fatalf("scan identity index: %v", err)
		}
		got = append(got, indexName)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate identity indexes: %v", err)
	}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected identity indexes: got %v want %v", got, want)
	}
}

func assertIdentityConstraintsAndDefaults(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO users (id, username, email, display_name)
		VALUES ($1, 'Local.Admin', 'admin@example.com', 'Local Admin')
	`, userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var status string
	var platformRole string
	var locale string
	var timezone string
	if err := db.QueryRow(`
		SELECT status, platform_role, locale, timezone
		FROM users
		WHERE id = $1
	`, userID).Scan(&status, &platformRole, &locale, &timezone); err != nil {
		t.Fatalf("read user defaults: %v", err)
	}
	if status != "ACTIVE" || platformRole != "USER" || locale != "zh-CN" || timezone != "Asia/Singapore" {
		t.Fatalf(
			"unexpected user defaults: status=%q role=%q locale=%q timezone=%q",
			status,
			platformRole,
			locale,
			timezone,
		)
	}

	if _, err := db.Exec(`
		INSERT INTO user_credentials (
			user_id, password_hash, password_algo, password_changed_at
		) VALUES ($1, '$argon2id$v=19$test-hash', 'ARGON2ID', CURRENT_TIMESTAMP)
	`, userID); err != nil {
		t.Fatalf("insert credential hash: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO auth_sessions (
			id, user_id, refresh_token_hash, user_agent, ip, expires_at
		) VALUES ($1, $2, 'sha256:test-refresh-token', 'identity migration test', '127.0.0.1', CURRENT_TIMESTAMP + INTERVAL '7 days')
	`, sessionID, userID); err != nil {
		t.Fatalf("insert auth session hash: %v", err)
	}

	assertStatementFails(t, db, `
		INSERT INTO users (id, username, display_name)
		VALUES ($1, 'local.admin', 'Duplicate Admin')
	`, secondUserID)
	assertStatementFails(t, db, `
		INSERT INTO users (id, username, display_name, status)
		VALUES ($1, 'invalid.status', 'Invalid Status', 'UNKNOWN')
	`, secondUserID)
	assertStatementFails(t, db, `DELETE FROM users WHERE id = $1`, userID)
}

func assertStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected statement to fail: %s", strings.TrimSpace(query))
	}
}

func assertTablesMissing(t *testing.T, dsn string, tableNames ...string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open rolled-back identity database: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, tableName := range tableNames {
		var exists bool
		if err := db.QueryRowContext(
			ctx,
			`SELECT to_regclass($1) IS NOT NULL`,
			fmt.Sprintf("public.%s", tableName),
		).Scan(&exists); err != nil {
			t.Fatalf("query rolled-back table %s: %v", tableName, err)
		}
		if exists {
			t.Fatalf("expected rolled-back table %s to be absent", tableName)
		}
	}
}
