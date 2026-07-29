// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package agent_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
)

const (
	agentOwnerID      = "038f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	agentWorkspaceID  = "038f1f2e-7b5a-7c3d-8e9f-1234567890a2"
	agentOtherSpaceID = "038f1f2e-7b5a-7c3d-8e9f-1234567890a3"
	agentModelID      = "038f1f2e-7b5a-7c3d-8e9f-1234567890a4"
	agentOtherModelID = "038f1f2e-7b5a-7c3d-8e9f-1234567890a5"
	agentID           = "038f1f2e-7b5a-7c3d-8e9f-1234567890a6"
	agentSecondID     = "038f1f2e-7b5a-7c3d-8e9f-1234567890a7"
	agentOtherID      = "038f1f2e-7b5a-7c3d-8e9f-1234567890a8"
	promptRevisionID  = "038f1f2e-7b5a-7c3d-8e9f-1234567890a9"
	promptOtherID     = "038f1f2e-7b5a-7c3d-8e9f-1234567890aa"
	promptRunID       = "038f1f2e-7b5a-7c3d-8e9f-1234567890ab"
	promptInputID     = "038f1f2e-7b5a-7c3d-8e9f-1234567890ac"
	promptOutputID    = "038f1f2e-7b5a-7c3d-8e9f-1234567890ad"
	promptContentHash = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
)

func TestAgentMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 3 || version.Dirty {
		t.Fatalf("expected clean agent migration version 10, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertAgentMigrationFixtures(t, db)
	assertAgentMigrationConstraints(t, db)

}

func insertAgentMigrationFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'agent.owner','Agent Owner')`, agentOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'agent-workspace','Agent Workspace','PRODUCTION',$3,$3,$3),
		($2,'agent-other','Agent Other','SANDBOX',$3,$3,$3)
	`, agentWorkspaceID, agentOtherSpaceID, agentOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES
		($1,$3,'Agent Model','OPENAI_COMPATIBLE','https://models.example/v1','agent-model',$5,$5),
		($2,$4,'Other Model','OPENAI_COMPATIBLE','https://models.example/v1','other-model',$5,$5)
	`, agentModelID, agentOtherModelID, agentWorkspaceID, agentOtherSpaceID, agentOwnerID); err != nil {
		t.Fatal(err)
	}
}

func assertAgentMigrationConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,role_description,model_config_id,is_default,created_by,updated_by)
		VALUES($1,$2,'Default Agent','Helps users',$3,TRUE,$4,$4)
	`, agentID, agentWorkspaceID, agentModelID, agentOwnerID); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_prompt_revisions(id,workspace_id,agent_id,revision_no,system_prompt,source,content_sha256,created_by)
		VALUES($1,$2,$3,1,'You are a useful agent.','MANUAL',$4,$5)
	`, promptRevisionID, agentWorkspaceID, agentID, promptContentHash, agentOwnerID); err != nil {
		t.Fatalf("insert prompt revision: %v", err)
	}
	if _, err := db.Exec(`UPDATE agents SET current_prompt_revision_id=$2 WHERE id=$1`, agentID, promptRevisionID); err != nil {
		t.Fatalf("set current prompt revision: %v", err)
	}
	if _, err := db.Exec(`UPDATE workspaces SET default_agent_id=$2 WHERE id=$1`, agentWorkspaceID, agentID); err != nil {
		t.Fatalf("set workspace default agent: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO prompt_runs(
			id,workspace_id,agent_id,operation_type,model_config_id,model_snapshot,
			input_object_id,output_object_id,status,accepted_revision_id,trace_id,created_by,finished_at
		) VALUES($1,$2,$3,'ENHANCE',$4,'{"model":"agent-model"}',$5,$6,'SUCCEEDED',$7,'trace-agent-1',$8,clock_timestamp())
	`, promptRunID, agentWorkspaceID, agentID, agentModelID, promptInputID, promptOutputID, promptRevisionID, agentOwnerID); err != nil {
		t.Fatalf("insert prompt run: %v", err)
	}

	assertAgentStatementFails(t, db, `
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'Cross Model',$3,$4,$4)
	`, agentSecondID, agentWorkspaceID, agentOtherModelID, agentOwnerID)
	assertAgentStatementFails(t, db, `
		INSERT INTO agents(id,workspace_id,name,model_config_id,is_default,created_by,updated_by)
		VALUES($1,$2,'Second Default',$3,TRUE,$4,$4)
	`, agentSecondID, agentWorkspaceID, agentModelID, agentOwnerID)
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'Second Agent',$3,$4,$4)
	`, agentSecondID, agentWorkspaceID, agentModelID, agentOwnerID); err != nil {
		t.Fatalf("insert second agent: %v", err)
	}
	assertAgentStatementFails(t, db, `UPDATE agents SET current_prompt_revision_id=$2 WHERE id=$1`, agentSecondID, promptRevisionID)
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'Other Agent',$3,$4,$4)
	`, agentOtherID, agentOtherSpaceID, agentOtherModelID, agentOwnerID); err != nil {
		t.Fatalf("insert other agent: %v", err)
	}
	assertAgentStatementFails(t, db, `UPDATE workspaces SET default_agent_id=$2 WHERE id=$1`, agentOtherSpaceID, agentID)
	assertAgentStatementFails(t, db, `
		INSERT INTO agent_prompt_revisions(id,workspace_id,agent_id,revision_no,system_prompt,source,content_sha256,created_by)
		VALUES($1,$2,$3,1,'Cross prompt','MANUAL',$4,$5)
	`, promptOtherID, agentOtherSpaceID, agentID, promptContentHash, agentOwnerID)
	assertAgentStatementFails(t, db, `UPDATE agent_prompt_revisions SET system_prompt='changed' WHERE id=$1`, promptRevisionID)
	assertAgentStatementFails(t, db, `DELETE FROM agent_prompt_revisions WHERE id=$1`, promptRevisionID)
	assertAgentStatementFails(t, db, `
		INSERT INTO prompt_runs(
			id,workspace_id,agent_id,operation_type,model_config_id,model_snapshot,
			input_object_id,status,trace_id,created_by
		) VALUES($1,$2,$3,'PREVIEW',$4,'{}',$5,'PENDING','trace-cross-model',$6)
	`, promptOtherID, agentWorkspaceID, agentID, agentOtherModelID, promptInputID, agentOwnerID)

	var revisionCount int
	if err := db.QueryRow(`SELECT count(*) FROM agent_prompt_revisions WHERE id=$1`, promptRevisionID).Scan(&revisionCount); err != nil {
		t.Fatal(err)
	}
	if revisionCount != 1 {
		t.Fatalf("immutable prompt revision was lost: count=%d", revisionCount)
	}
}

func assertAgentStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected agent statement to fail: %s", strings.TrimSpace(query))
	}
}

func assertAgentTablesMissing(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, table := range []string{"prompt_runs", "agent_prompt_revisions", "agents"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("expected rolled-back %s to be absent", table)
		}
	}
	var defaultAgentConstraint bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='workspaces_default_agent_fk')
	`).Scan(&defaultAgentConstraint); err != nil {
		t.Fatal(err)
	}
	if defaultAgentConstraint {
		t.Fatal("workspace default agent FK survived rollback")
	}
}
