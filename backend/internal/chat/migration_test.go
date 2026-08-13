// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package chat_test

import (
	"database/sql"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

const (
	chatOwnerID          = "408f1f2e-7b5a-7c3d-8e9f-123456789001"
	chatWorkspaceID      = "408f1f2e-7b5a-7c3d-8e9f-123456789002"
	chatOtherWorkspaceID = "408f1f2e-7b5a-7c3d-8e9f-123456789003"
	chatModelID          = "408f1f2e-7b5a-7c3d-8e9f-123456789004"
	chatOtherModelID     = "408f1f2e-7b5a-7c3d-8e9f-123456789005"
	chatAgentID          = "408f1f2e-7b5a-7c3d-8e9f-123456789006"
	chatOtherAgentID     = "408f1f2e-7b5a-7c3d-8e9f-123456789007"
	chatSessionID        = "408f1f2e-7b5a-7c3d-8e9f-123456789008"
	chatMessageID        = "408f1f2e-7b5a-7c3d-8e9f-123456789009"
	chatObjectMessageID  = "408f1f2e-7b5a-7c3d-8e9f-12345678900a"
	chatObjectID         = "408f1f2e-7b5a-7c3d-8e9f-12345678900b"
	chatRunID            = "408f1f2e-7b5a-7c3d-8e9f-12345678900c"
	chatConfirmationID   = "408f1f2e-7b5a-7c3d-8e9f-12345678900d"
	chatProbeID          = "408f1f2e-7b5a-7c3d-8e9f-12345678900e"
	chatContentHash      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func TestChatMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 22 || version.Dirty {
		t.Fatalf("expected clean chat migration version 22, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertChatMigrationFixtures(t, db)
	assertChatMigrationSchema(t, db)
	assertChatMigrationConstraints(t, db)

}

func insertChatMigrationFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'chat.owner','Chat Owner')`, chatOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'chat-space','Chat Space','PRODUCTION',$3,$3,$3),
		($2,'chat-other','Chat Other','SANDBOX',$3,$3,$3)
	`, chatWorkspaceID, chatOtherWorkspaceID, chatOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(
		 id,workspace_id,name,provider,api_base,model_name,created_by,updated_by
		) VALUES
		($1,$3,'Chat Model','openai','https://models.example.test','chat-model',$5,$5),
		($2,$4,'Other Model','openai','https://models.example.test','other-model',$5,$5)
	`, chatModelID, chatOtherModelID, chatWorkspaceID, chatOtherWorkspaceID, chatOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES
		($1,$3,'Chat Agent',$5,$7,$7),
		($2,$4,'Other Agent',$6,$7,$7)
	`, chatAgentID, chatOtherAgentID, chatWorkspaceID, chatOtherWorkspaceID,
		chatModelID, chatOtherModelID, chatOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		VALUES($1,$2,$3,'Order support',$4)
	`, chatSessionID, chatWorkspaceID, chatAgentID, chatOwnerID); err != nil {
		t.Fatal(err)
	}
}

func assertChatMigrationSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"chat_sessions", "chat_messages"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected chat table %s", table)
		}
	}
	for _, forbidden := range []struct{ table, column string }{
		{"chat_sessions", "deleted_at"}, {"chat_messages", "deleted_at"},
		{"chat_messages", "updated_at"},
	} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM information_schema.columns
			 WHERE table_schema='public' AND table_name=$1 AND column_name=$2)
		`, forbidden.table, forbidden.column).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("permanent chat fact must not expose %s.%s", forbidden.table, forbidden.column)
		}
	}
	for _, index := range []string{
		"chat_sessions_workspace_creator_updated_idx",
		"chat_messages_workspace_session_created_idx",
		"chat_messages_workspace_run_created_idx",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+index).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected chat index %s", index)
		}
	}
}

func assertChatMigrationConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	assertChatStatementFails(t, db, `
		INSERT INTO chat_sessions(id,workspace_id,agent_id,created_by)
		VALUES($1,$2,$3,$4)
	`, chatProbeID, chatWorkspaceID, chatOtherAgentID, chatOwnerID)
	assertChatStatementFails(t, db, `
		INSERT INTO chat_sessions(id,workspace_id,agent_id,status,created_by)
		VALUES($1,$2,$3,'PRIVATE',$4)
	`, chatProbeID, chatWorkspaceID, chatAgentID, chatOwnerID)
	if _, err := db.Exec(`
		INSERT INTO chat_messages(
		 id,workspace_id,session_id,role,content,content_sha256,status,created_by
		) VALUES($1,$2,$3,'USER','Where is order A-1?',$4,'RECEIVED',$5)
	`, chatMessageID, chatWorkspaceID, chatSessionID, chatContentHash, chatOwnerID); err != nil {
		t.Fatalf("insert inline user chat message: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO chat_messages(
		 id,workspace_id,session_id,role,content_object_id,content_sha256,status
		) VALUES($1,$2,$3,'ASSISTANT',$4,$5,'PROCESSING')
	`, chatObjectMessageID, chatWorkspaceID, chatSessionID, chatObjectID, chatContentHash); err != nil {
		t.Fatalf("insert object-backed assistant message: %v", err)
	}
	assertChatStatementFails(t, db, `
		INSERT INTO chat_messages(
		 id,workspace_id,session_id,role,content,content_sha256,status
		) VALUES($1,$2,$3,'USER','missing actor',$4,'RECEIVED')
	`, chatProbeID, chatWorkspaceID, chatSessionID, chatContentHash)
	assertChatStatementFails(t, db, `
		INSERT INTO chat_messages(
		 id,workspace_id,session_id,role,content,content_sha256,status
		) VALUES($1,$2,$3,'ASSISTANT',NULL,$4,'EXECUTED')
	`, chatProbeID, chatWorkspaceID, chatSessionID, chatContentHash)
	assertChatStatementFails(t, db, `
		INSERT INTO chat_messages(
		 id,workspace_id,session_id,role,content,content_sha256,status
		) VALUES($1,$2,$3,'ASSISTANT','reply','bad','EXECUTED')
	`, chatProbeID, chatWorkspaceID, chatSessionID)
	assertChatStatementFails(t, db, `
		INSERT INTO chat_messages(
		 id,workspace_id,session_id,role,content,content_sha256,status
		) VALUES($1,$2,$3,'ASSISTANT','confirm',$4,'PENDING_CONFIRMATION')
	`, chatProbeID, chatWorkspaceID, chatSessionID, chatContentHash)
	assertChatStatementFails(t, db, `
		INSERT INTO chat_messages(
		 id,workspace_id,session_id,role,content,content_sha256,status,confirmation_id
		) VALUES($1,$2,$3,'ASSISTANT','cross workspace',$4,'PENDING_CONFIRMATION',$5)
	`, chatProbeID, chatOtherWorkspaceID, chatSessionID, chatContentHash, chatConfirmationID)
	if _, err := db.Exec(`
		UPDATE chat_messages SET status='EXECUTED',run_id=$2 WHERE id=$1
	`, chatObjectMessageID, chatRunID); err != nil {
		t.Fatalf("advance mutable chat message state: %v", err)
	}
	assertChatStatementFails(t, db, `UPDATE chat_messages SET content='changed' WHERE id=$1`, chatMessageID)
	assertChatStatementFails(t, db, `DELETE FROM chat_messages WHERE id=$1`, chatMessageID)
	assertChatStatementFails(t, db, `DELETE FROM chat_sessions WHERE id=$1`, chatSessionID)
}

func assertChatStatementFails(t *testing.T, db *sql.DB, statement string, arguments ...any) {
	t.Helper()
	if _, err := db.Exec(statement, arguments...); err == nil {
		t.Fatalf("expected chat statement to fail: %s", statement)
	}
}

func assertChatTablesMissing(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"chat_sessions", "chat_messages"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("chat table %s remained after rollback", table)
		}
	}
}
