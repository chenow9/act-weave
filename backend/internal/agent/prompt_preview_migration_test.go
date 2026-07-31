// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
// Historical step-migration tests were retired when migrations were squashed
// into 000001_init. See migrations_archive/ for the pre-squash chain.
package agent_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database"
	"actweave/backend/internal/database/dbtest"
	"github.com/google/uuid"
)

const (
	previewOwnerID     = "a18f1f2e-7b5a-7c3d-8e9f-1234567890b1"
	previewWorkspaceID = "a18f1f2e-7b5a-7c3d-8e9f-1234567890b2"
	previewModelID     = "a18f1f2e-7b5a-7c3d-8e9f-1234567890b3"
	previewAgentID     = "a18f1f2e-7b5a-7c3d-8e9f-1234567890b4"
	previewRevisionID  = "a18f1f2e-7b5a-7c3d-8e9f-1234567890b5"
	previewInputObjID  = "a18f1f2e-7b5a-7c3d-8e9f-1234567890b6"
	previewOutputObjID = "a18f1f2e-7b5a-7c3d-8e9f-1234567890b7"
	previewRunID       = "a18f1f2e-7b5a-7c3d-8e9f-1234567890b8"
	previewHash        = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
)

func TestPromptPreviewRetentionMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("expected clean migration 2, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertPreviewMigrationFixtures(t, db)
	assertPreviewMigrationSchema(t, db)
	assertPreviewMigrationConstraints(t, db)
	assertExistingPermanentRegression(t, db)

	// Once preview data exists, down must fail closed.
	// golang-migrate marks dirty before executing down SQL; schema remains at 61.
	if err := attemptPreviewMigrateTo(t, testDatabase, 60); err == nil {
		t.Fatal("expected down of 000061 to fail while preview data exists")
	}
	assertPreviewMigrationSchema(t, db)
	forceCleanMigrationVersion(t, db, 1)

	// Clean environment: delete preview data, then down/re-up succeeds.
	clearPreviewMigrationData(t, db)
	assertPreviewMigrationSchema(t, db)
}

func insertPreviewMigrationFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO users(id,username,display_name)
		VALUES($1,'preview.owner','Preview Owner')
	`, previewOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'preview-workspace','Preview Workspace','PRODUCTION',$2,$2,$2)
	`, previewWorkspaceID, previewOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'Preview Model','OPENAI_COMPATIBLE','https://models.example/v1','preview-model',$3,$3)
	`, previewModelID, previewWorkspaceID, previewOwnerID); err != nil {
		t.Fatal(err)
	}
}

func assertPreviewMigrationSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, column := range []struct {
		table  string
		column string
	}{
		{"prompt_runs", "expires_at"},
		{"prompt_runs", "promoted_at"},
		{"prompt_runs", "content_purged_at"},
		{"stored_objects", "body_purged_at"},
		{"stored_objects", "purge_claim_token"},
		{"stored_objects", "purge_claim_expires_at"},
		{"stored_objects", "purge_attempts"},
		{"stored_objects", "purge_next_attempt_at"},
		{"stored_objects", "purge_last_error_code"},
	} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM information_schema.columns
				WHERE table_schema='public' AND table_name=$1 AND column_name=$2
			)
		`, column.table, column.column).Scan(&exists); err != nil || !exists {
			t.Fatalf("column %s.%s missing: exists=%v err=%v", column.table, column.column, exists, err)
		}
	}
	var indexExists bool
	if err := db.QueryRow(`
		SELECT to_regclass('public.stored_objects_preview_purge_claim_idx') IS NOT NULL
	`).Scan(&indexExists); err != nil || !indexExists {
		t.Fatalf("preview purge index missing: exists=%v err=%v", indexExists, err)
	}
}

func assertPreviewMigrationConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,role_description,model_config_id,created_by,updated_by)
		VALUES($1,$2,'Preview Agent','role',$3,$4,$4)
	`, previewAgentID, previewWorkspaceID, previewModelID, previewOwnerID); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_prompt_revisions(
			id,workspace_id,agent_id,revision_no,system_prompt,source,content_sha256,created_by
		) VALUES($1,$2,$3,1,'You are helpful.','AI_ASSISTED',$4,$5)
	`, previewRevisionID, previewWorkspaceID, previewAgentID, previewHash, previewOwnerID); err != nil {
		t.Fatalf("insert AI_ASSISTED revision: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE agents SET current_prompt_revision_id=$2 WHERE id=$1
	`, previewAgentID, previewRevisionID); err != nil {
		t.Fatalf("set current revision: %v", err)
	}

	expires := time.Now().UTC().Add(30 * 24 * time.Hour)
	if _, err := db.Exec(`
		INSERT INTO stored_objects(
			id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
			encryption_key_id,classification,retention_mode,retention_until,
			created_by_type,created_by_id
		) VALUES($1,$2,'actweave-objects','preview/input-1.bin','PROMPT_PREVIEW_INPUT',
			'application/vnd.actweave.encrypted+octet-stream',12,$3,'key-v1','SENSITIVE',
			'EXPIRING',$4,'USER',$5)
	`, previewInputObjID, previewWorkspaceID, previewHash, expires, previewOwnerID); err != nil {
		t.Fatalf("insert preview input object: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO stored_objects(
			id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
			encryption_key_id,classification,retention_mode,retention_until,
			created_by_type,created_by_id
		) VALUES($1,$2,'actweave-objects','preview/output-1.bin','PROMPT_PREVIEW_OUTPUT',
			'application/vnd.actweave.encrypted+octet-stream',12,$3,'key-v1','SENSITIVE',
			'EXPIRING',$4,'USER',$5)
	`, previewOutputObjID, previewWorkspaceID, previewHash, expires, previewOwnerID); err != nil {
		t.Fatalf("insert preview output object: %v", err)
	}

	// Invalid preview combinations fail (missing encryption).
	assertPreviewStatementFails(t, db, `
		INSERT INTO stored_objects(
			id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
			classification,retention_mode,retention_until,created_by_type,created_by_id
		) VALUES($1,$2,'actweave-objects','preview/bad-plain.bin','PROMPT_PREVIEW_INPUT',
			'text/plain',1,$3,'SENSITIVE','EXPIRING',clock_timestamp()+interval '1 day','USER',$4)
	`, uuid.Must(uuid.NewV7()).String(), previewWorkspaceID, previewHash, previewOwnerID)

	assertPreviewStatementFails(t, db, `
		INSERT INTO stored_objects(
			id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
			encryption_key_id,classification,retention_mode,retention_until,
			created_by_type,created_by_id
		) VALUES($1,$2,'actweave-objects','preview/bad-internal.bin','PROMPT_PREVIEW_INPUT',
			'application/octet-stream',1,$3,'key-v1','INTERNAL','EXPIRING',
			clock_timestamp()+interval '1 day','USER',$4)
	`, uuid.Must(uuid.NewV7()).String(), previewWorkspaceID, previewHash, previewOwnerID)

	var createdAt, expiresAt time.Time
	if err := db.QueryRow(`
		WITH stamp AS (SELECT clock_timestamp() AS ts)
		INSERT INTO prompt_runs(
			id,workspace_id,agent_id,operation_type,model_config_id,model_snapshot,
			input_object_id,input_sha256,input_length,status,trace_id,created_by,
			created_at,expires_at
		)
		SELECT $1,$2,NULL,'CREATE_PREVIEW',$3,'{"model":"preview-model"}',$4,$5,12,
			'PENDING','trace-preview-1',$6,stamp.ts,stamp.ts + INTERVAL '30 days'
		FROM stamp
		RETURNING created_at, expires_at
	`, previewRunID, previewWorkspaceID, previewModelID, previewInputObjID, previewHash, previewOwnerID).
		Scan(&createdAt, &expiresAt); err != nil {
		t.Fatalf("insert CREATE_PREVIEW run: %v", err)
	}
	wantExpires := createdAt.Add(30 * 24 * time.Hour)
	if !expiresAt.Equal(wantExpires) {
		t.Fatalf("expires_at=%v want created_at+30d=%v", expiresAt, wantExpires)
	}

	// CREATE_PREVIEW with agent_id set at insert fails.
	assertPreviewStatementFails(t, db, `
		WITH stamp AS (SELECT clock_timestamp() AS ts)
		INSERT INTO prompt_runs(
			id,workspace_id,agent_id,operation_type,model_config_id,model_snapshot,
			input_object_id,input_sha256,input_length,status,trace_id,created_by,
			created_at,expires_at
		)
		SELECT $1,$2,$3,'CREATE_PREVIEW',$4,'{}',$5,$6,12,'PENDING','trace-bad',$7,
			stamp.ts,stamp.ts + INTERVAL '30 days'
		FROM stamp
	`, uuid.Must(uuid.NewV7()).String(), previewWorkspaceID, previewAgentID, previewModelID,
		previewInputObjID, previewHash, previewOwnerID)

	// Non-CREATE_PREVIEW cannot set expires_at.
	assertPreviewStatementFails(t, db, `
		INSERT INTO prompt_runs(
			id,workspace_id,agent_id,operation_type,model_config_id,model_snapshot,
			input_object_id,input_sha256,input_length,status,trace_id,created_by,expires_at
		) VALUES($1,$2,$3,'ENHANCE',$4,'{}',$5,$6,12,'PENDING','trace-enh',$7,clock_timestamp()+interval '30 days')
	`, uuid.Must(uuid.NewV7()).String(), previewWorkspaceID, previewAgentID, previewModelID,
		previewInputObjID, previewHash, previewOwnerID)

	// Promote preview object EXPIRING -> PERMANENT once.
	if _, err := db.Exec(`
		UPDATE stored_objects
		SET retention_mode='PERMANENT', retention_until=NULL
		WHERE id=$1
	`, previewInputObjID); err != nil {
		t.Fatalf("promote preview object: %v", err)
	}
	assertPreviewStatementFails(t, db, `
		UPDATE stored_objects SET size_bytes=size_bytes+1 WHERE id=$1
	`, previewInputObjID)
	assertPreviewStatementFails(t, db, `
		UPDATE stored_objects SET retention_mode='EXPIRING', retention_until=clock_timestamp()+interval '1 day'
		WHERE id=$1
	`, previewInputObjID)

	// Cannot purge body before expiry on remaining EXPIRING object.
	assertPreviewStatementFails(t, db, `
		UPDATE stored_objects SET body_purged_at=clock_timestamp() WHERE id=$1
	`, previewOutputObjID)
}

func assertExistingPermanentRegression(t *testing.T, db *sql.DB) {
	t.Helper()
	permanentID := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO stored_objects(
			id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
			encryption_key_id,classification,retention_mode,retention_until,
			created_by_type,created_by_id
		) VALUES($1,$2,'actweave-objects','permanent/prompt-input.bin','PROMPT_RUN_INPUT',
			'application/octet-stream',4,$3,'key-v1','SENSITIVE','PERMANENT',NULL,'USER',$4)
	`, permanentID, previewWorkspaceID, previewHash, previewOwnerID); err != nil {
		t.Fatalf("insert permanent prompt input: %v", err)
	}
	assertPreviewStatementFails(t, db, `
		UPDATE stored_objects SET size_bytes=1 WHERE id=$1
	`, permanentID)
	assertPreviewStatementFails(t, db, `
		DELETE FROM stored_objects WHERE id=$1
	`, permanentID)
	assertPreviewStatementFails(t, db, `
		INSERT INTO stored_objects(
			id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
			encryption_key_id,classification,retention_mode,retention_until,
			created_by_type,created_by_id
		) VALUES($1,$2,'actweave-objects','permanent/bad-expiring.bin','PROMPT_RUN_INPUT',
			'application/octet-stream',4,$3,'key-v1','SENSITIVE','EXPIRING',
			clock_timestamp()+interval '1 day','USER',$4)
	`, uuid.Must(uuid.NewV7()).String(), previewWorkspaceID, previewHash, previewOwnerID)
}

func clearPreviewMigrationData(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`ALTER TABLE prompt_runs DISABLE TRIGGER prompt_runs_permanent_content`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM prompt_runs WHERE workspace_id=$1`, previewWorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE prompt_runs ENABLE TRIGGER prompt_runs_permanent_content`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE agent_prompt_revisions DISABLE TRIGGER agent_prompt_revisions_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agents SET current_prompt_revision_id=NULL WHERE id=$1`, previewAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM agent_prompt_revisions WHERE workspace_id=$1`, previewWorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE agent_prompt_revisions ENABLE TRIGGER agent_prompt_revisions_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM agents WHERE workspace_id=$1`, previewWorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE stored_objects DISABLE TRIGGER stored_objects_metadata_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM stored_objects WHERE workspace_id=$1`, previewWorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE stored_objects ENABLE TRIGGER stored_objects_metadata_guard`); err != nil {
		t.Fatal(err)
	}
}

func assertPreviewColumnsAbsent(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, column := range []struct {
		table  string
		column string
	}{
		{"prompt_runs", "expires_at"},
		{"stored_objects", "body_purged_at"},
	} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM information_schema.columns
				WHERE table_schema='public' AND table_name=$1 AND column_name=$2
			)
		`, column.table, column.column).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("expected %s.%s absent after down", column.table, column.column)
		}
	}
}

func assertPreviewStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected statement to fail: %s", strings.TrimSpace(query))
	}
}

func attemptPreviewMigrateTo(t *testing.T, testDatabase *dbtest.Database, version uint) error {
	t.Helper()
	ctx := context.Background()
	migrator, err := database.Open(ctx, testDatabase.DSN())
	if err != nil {
		t.Fatalf("open migrator: %v", err)
	}
	defer migrator.Close()
	return migrator.To(version)
}

// forceCleanMigrationVersion repairs schema_migrations after a deliberate fail-closed
// down that left the database dirty without applying schema changes.
func forceCleanMigrationVersion(t *testing.T, db *sql.DB, version uint) {
	t.Helper()
	if _, err := db.Exec(`UPDATE schema_migrations SET version=$1, dirty=false`, version); err != nil {
		t.Fatalf("force clean migration version %d: %v", version, err)
	}
}
