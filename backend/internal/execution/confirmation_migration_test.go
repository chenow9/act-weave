// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package execution_test

import (
	"database/sql"
	"strings"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

const (
	confirmationOtherUserID = "d08f1f2e-7b5a-7c3d-8e9f-123456789001"
	confirmationID          = "d08f1f2e-7b5a-7c3d-8e9f-123456789002"
	chatConfirmationID      = "d08f1f2e-7b5a-7c3d-8e9f-123456789003"
	confirmationMessageID   = "d08f1f2e-7b5a-7c3d-8e9f-123456789004"
	confirmationProbeID     = "d08f1f2e-7b5a-7c3d-8e9f-123456789005"
	confirmationHash        = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
)

func TestConfirmationMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean confirmation migration version 23, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertToolInvocationFixtures(t, db)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'confirmation.other','Other Confirmer')`, confirmationOtherUserID); err != nil {
		t.Fatal(err)
	}
	assertConfirmationSchema(t, db)
	assertConfirmationConstraints(t, db)
}

func assertConfirmationSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"execution_confirmations", "chat_confirmations"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected confirmation table %s", table)
		}
	}
	for _, index := range []string{
		"execution_confirmations_workspace_status_created_idx",
		"execution_confirmations_pending_expiry_idx",
		"chat_confirmations_workspace_session_created_idx",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+index).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected confirmation index %s", index)
		}
	}
}

func assertConfirmationConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO execution_confirmations(
		 id,workspace_id,execution_id,run_id,node_id,reason,risk_reasons,
		 scope_snapshot,release_id,input_hash,connection_id,plan_hash,
		 resume_token_hash,requested_by,expires_at
		) VALUES($1,$2,$3,$4,'tool-1','Production write','["bulk operation"]',
		 '{"environment":"PRODUCTION","amount":1000}',$5,$6,$7,$6,$6,$8,
		 clock_timestamp()+interval '10 minutes')
	`, confirmationID, executionWorkspaceID, invocationWorkflowExecutionID,
		executionAgentRunID, invocationReleaseID, confirmationHash,
		invocationConnectionID, executionOwnerID); err != nil {
		t.Fatalf("insert execution confirmation: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO chat_confirmations(
		 id,workspace_id,session_id,run_id,execution_confirmation_id,target_type,
		 target_release_id,risk_level,risk_reasons,input_summary
		) VALUES($1,$2,$3,$4,$5,'TOOL',$6,'HIGH','["bulk operation"]','{"count":1000}')
	`, chatConfirmationID, executionWorkspaceID, executionSessionID,
		executionAgentRunID, confirmationID, invocationReleaseID); err != nil {
		t.Fatalf("insert chat confirmation mapping: %v", err)
	}
	if _, err := db.Exec(`UPDATE chat_sessions SET pending_confirmation_id=$2 WHERE id=$1`,
		executionSessionID, chatConfirmationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO chat_messages(
		 id,workspace_id,session_id,role,content,content_sha256,status,run_id,confirmation_id
		) VALUES($1,$2,$3,'ASSISTANT','Please confirm production write',$4,
		 'PENDING_CONFIRMATION',$5,$6)
	`, confirmationMessageID, executionWorkspaceID, executionSessionID,
		confirmationHash, executionAgentRunID, chatConfirmationID); err != nil {
		t.Fatal(err)
	}
	assertConfirmationStatementFails(t, db, `
		INSERT INTO execution_confirmations(
		 id,workspace_id,run_id,node_id,reason,scope_snapshot,release_id,input_hash,
		 resume_token_hash,requested_by,expires_at
		) VALUES($1,$2,$3,'tool','bad chain','{}',$4,$5,$5,$6,
		 clock_timestamp()+interval '10 minutes')
	`, confirmationProbeID, executionWorkspaceID, executionOtherAgentRunID,
		invocationReleaseID, confirmationHash, executionOwnerID)
	assertConfirmationStatementFails(t, db, `
		UPDATE execution_confirmations SET status='CONFIRMED',confirmed_by=$2,
		 confirmed_at=clock_timestamp(),lock_version=lock_version+1 WHERE id=$1
	`, confirmationID, confirmationOtherUserID)
	if _, err := db.Exec(`
		UPDATE execution_confirmations SET status='CONFIRMED',confirmed_by=requested_by,
		 confirmed_at=clock_timestamp(),lock_version=lock_version+1 WHERE id=$1
	`, confirmationID); err != nil {
		t.Fatalf("confirm by original requester: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE chat_confirmations SET status='CONFIRMED',confirmed_by=$2,
		 confirmed_at=clock_timestamp() WHERE id=$1
	`, chatConfirmationID, executionOwnerID); err != nil {
		t.Fatalf("mirror confirmed chat presentation: %v", err)
	}
	assertConfirmationStatementFails(t, db, `UPDATE execution_confirmations SET input_hash=$2 WHERE id=$1`, confirmationID, strings.Repeat("a", 64))
	assertConfirmationStatementFails(t, db, `DELETE FROM chat_confirmations WHERE id=$1`, chatConfirmationID)
	assertConfirmationStatementFails(t, db, `DELETE FROM execution_confirmations WHERE id=$1`, confirmationID)
}

func assertConfirmationStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected confirmation statement to fail: %s", strings.TrimSpace(query))
	}
}

func assertConfirmationTablesMissing(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"execution_confirmations", "chat_confirmations"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("confirmation table %s remained after rollback", table)
		}
	}
	var pendingID, confirmationID sql.NullString
	var messageStatus string
	if err := db.QueryRow(`SELECT pending_confirmation_id FROM chat_sessions WHERE id=$1`,
		executionSessionID).Scan(&pendingID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status,confirmation_id FROM chat_messages WHERE id=$1`,
		confirmationMessageID).Scan(&messageStatus, &confirmationID); err != nil {
		t.Fatal(err)
	}
	if pendingID.Valid || confirmationID.Valid || messageStatus != "FAILED" {
		t.Fatalf("rollback left dangling confirmation refs: pending=%v message=%v status=%s",
			pendingID, confirmationID, messageStatus)
	}
}
