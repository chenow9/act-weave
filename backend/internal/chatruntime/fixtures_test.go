package chatruntime_test

import (
	"database/sql"
	"testing"
)

// Shared IDs for package integration tests (auxiliary protocol, etc.).
const (
	runtimeOwnerID     = "a18f1f2e-7b5a-7c3d-8e9f-123456789001"
	runtimeWorkspaceID = "a18f1f2e-7b5a-7c3d-8e9f-123456789002"
	runtimeModelID     = "a18f1f2e-7b5a-7c3d-8e9f-123456789003"
	runtimeAgentID     = "a18f1f2e-7b5a-7c3d-8e9f-123456789004"
	runtimeSessionID   = "a18f1f2e-7b5a-7c3d-8e9f-123456789005"
	runtimeMessageID   = "a18f1f2e-7b5a-7c3d-8e9f-123456789006"
	runtimeRunID       = "a18f1f2e-7b5a-7c3d-8e9f-123456789007"
	runtimePromptID    = "a18f1f2e-7b5a-7c3d-8e9f-123456789008"
)

func insertRuntimeFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO users(id,username,display_name) VALUES($1,'chat.runtime.owner','Chat Runtime Owner')`,
		runtimeOwnerID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'chat-runtime-space','Chat Runtime Space','PRODUCTION',$2,$2,$2)
	`, runtimeWorkspaceID, runtimeOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(
			id,workspace_id,name,provider,api_base,model_name,created_by,updated_by
		) VALUES($1,$2,'Runtime Model','openai','https://models.example.test/v1','runtime-model',$3,$3)
	`, runtimeModelID, runtimeWorkspaceID, runtimeOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'Runtime Agent',$3,$4,$4)
	`, runtimeAgentID, runtimeWorkspaceID, runtimeModelID, runtimeOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_prompt_revisions(
			id,workspace_id,agent_id,revision_no,system_prompt,source,content_sha256,created_by
		) VALUES($1,$2,$3,1,'You are a concise runtime assistant.','MANUAL',repeat('a',64),$4)
	`, runtimePromptID, runtimeWorkspaceID, runtimeAgentID, runtimeOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		UPDATE agents SET current_prompt_revision_id=$1 WHERE id=$2
	`, runtimePromptID, runtimeAgentID); err != nil {
		t.Fatal(err)
	}
}
