// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package execution_test

import (
	"database/sql"
	"strings"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

const (
	runOwnerID          = "508f1f2e-7b5a-7c3d-8e9f-123456789001"
	runWorkspaceID      = "508f1f2e-7b5a-7c3d-8e9f-123456789002"
	runOtherWorkspaceID = "508f1f2e-7b5a-7c3d-8e9f-123456789003"
	runModelID          = "508f1f2e-7b5a-7c3d-8e9f-123456789004"
	runOtherModelID     = "508f1f2e-7b5a-7c3d-8e9f-123456789005"
	runAgentID          = "508f1f2e-7b5a-7c3d-8e9f-123456789006"
	runOtherAgentID     = "508f1f2e-7b5a-7c3d-8e9f-123456789007"
	runSessionID        = "508f1f2e-7b5a-7c3d-8e9f-123456789008"
	runOtherSessionID   = "508f1f2e-7b5a-7c3d-8e9f-123456789009"
	runCapabilityID     = "508f1f2e-7b5a-7c3d-8e9f-12345678900a"
	runOtherCapability  = "508f1f2e-7b5a-7c3d-8e9f-12345678900b"
	runReleaseID        = "508f1f2e-7b5a-7c3d-8e9f-12345678900c"
	runOtherReleaseID   = "508f1f2e-7b5a-7c3d-8e9f-12345678900d"
	runID               = "508f1f2e-7b5a-7c3d-8e9f-12345678900e"
	runStepID           = "508f1f2e-7b5a-7c3d-8e9f-12345678900f"
	runMessageID        = "508f1f2e-7b5a-7c3d-8e9f-123456789010"
	runProbeID          = "508f1f2e-7b5a-7c3d-8e9f-123456789011"
	runContentHash      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	runReleaseHash      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestAgentRunMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 3 || version.Dirty {
		t.Fatalf("expected clean agent run migration version 18, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertAgentRunMigrationFixtures(t, db)
	assertAgentRunMigrationSchema(t, db)
	assertAgentRunMigrationConstraints(t, db)

}

func insertAgentRunMigrationFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'run.owner','Run Owner')`, runOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'run-space','Run Space','PRODUCTION',$3,$3,$3),
		($2,'run-other','Run Other','SANDBOX',$3,$3,$3)
	`, runWorkspaceID, runOtherWorkspaceID, runOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(
		 id,workspace_id,name,provider,api_base,model_name,created_by,updated_by
		) VALUES
		($1,$3,'Run Model','openai','https://models.example.test','run-model',$5,$5),
		($2,$4,'Other Run Model','openai','https://models.example.test','other-run-model',$5,$5)
	`, runModelID, runOtherModelID, runWorkspaceID, runOtherWorkspaceID, runOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES
		($1,$3,'Run Agent',$5,$7,$7),
		($2,$4,'Other Run Agent',$6,$7,$7)
	`, runAgentID, runOtherAgentID, runWorkspaceID, runOtherWorkspaceID,
		runModelID, runOtherModelID, runOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by) VALUES
		($1,$3,$5,'Run session',$7),
		($2,$4,$6,'Other run session',$7)
	`, runSessionID, runOtherSessionID, runWorkspaceID, runOtherWorkspaceID,
		runAgentID, runOtherAgentID, runOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by) VALUES
		($1,$3,'TOOL','Run Tool','run-tool',$5,$5),
		($2,$4,'TOOL','Other Run Tool','other-run-tool',$5,$5)
	`, runCapabilityID, runOtherCapability, runWorkspaceID, runOtherWorkspaceID, runOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO capability_releases(
		 id,workspace_id,capability_id,release_no,source_type,source_id,callable_name,
		 input_schema,output_schema,risk_level,side_effect_level,checksum,published_by
		) VALUES
		($1,$3,$5,1,'TOOL_VERSION',$1,'run_tool','{}','{}','LOW','NONE',$7,$8),
		($2,$4,$6,1,'TOOL_VERSION',$2,'other_run_tool','{}','{}','LOW','NONE',$7,$8)
	`, runReleaseID, runOtherReleaseID, runWorkspaceID, runOtherWorkspaceID,
		runCapabilityID, runOtherCapability, runReleaseHash, runOwnerID); err != nil {
		t.Fatal(err)
	}
}

func assertAgentRunMigrationSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"agent_runs", "agent_run_steps"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected execution table %s", table)
		}
	}
	for _, column := range []string{
		"model_snapshot", "capability_snapshot", "context_policy_snapshot", "lock_version",
	} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM information_schema.columns
			 WHERE table_schema='public' AND table_name='agent_runs' AND column_name=$1)
		`, column).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected agent_runs snapshot/state column %s", column)
		}
	}
	for _, index := range []string{
		"agent_runs_workspace_started_idx",
		"agent_runs_workspace_status_started_idx",
		"agent_runs_trace_idx",
		"agent_run_steps_workspace_run_sequence_idx",
		"agent_run_steps_workspace_status_started_idx",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+index).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected agent run index %s", index)
		}
	}
}

func assertAgentRunMigrationConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	insertAgentRun(t, db, runID, runWorkspaceID, runSessionID, runAgentID)
	if _, err := db.Exec(`UPDATE chat_sessions SET latest_run_id=$2 WHERE id=$1`, runSessionID, runID); err != nil {
		t.Fatalf("associate chat session latest run: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO chat_messages(
		 id,workspace_id,session_id,role,content,content_sha256,status,run_id,created_by
		) VALUES($1,$2,$3,'USER','start run',$4,'EXECUTED',$5,$6)
	`, runMessageID, runWorkspaceID, runSessionID, runContentHash, runID, runOwnerID); err != nil {
		t.Fatalf("associate chat message run: %v", err)
	}

	assertRunStatementFails(t, db, `
		INSERT INTO agent_runs(
		 id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
		 triggered_by_id,trace_id,model_snapshot,capability_snapshot
		) VALUES($1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'trace-cross-session','{}','{}')
	`, runProbeID, runWorkspaceID, runOtherSessionID, runAgentID, runOwnerID)
	assertRunStatementFails(t, db, `
		INSERT INTO agent_runs(
		 id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
		 triggered_by_id,trace_id,model_snapshot,capability_snapshot
		) VALUES($1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'trace-cross-agent','{}','{}')
	`, runProbeID, runWorkspaceID, runSessionID, runOtherAgentID, runOwnerID)
	assertRunStatementFails(t, db, `
		INSERT INTO agent_runs(
		 id,workspace_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,
		 trace_id,model_snapshot,capability_snapshot
		) VALUES($1,$2,$3,'DONE','CHAT','USER',$4,'trace-bad-status','{}','{}')
	`, runProbeID, runWorkspaceID, runAgentID, runOwnerID)
	assertRunStatementFails(t, db, `
		INSERT INTO agent_runs(
		 id,workspace_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,
		 trace_id,model_snapshot,capability_snapshot
		) VALUES($1,$2,$3,'RUNNING','CHAT','USER',$4,'trace-bad-snapshot','[]','{}')
	`, runProbeID, runWorkspaceID, runAgentID, runOwnerID)
	assertRunStatementFails(t, db, `
		INSERT INTO agent_runs(
		 id,workspace_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,
		 trace_id,model_snapshot,capability_snapshot,error_code
		) VALUES($1,$2,$3,'FAILED','CHAT','USER',$4,'trace-no-finish','{}','{}','FAILED')
	`, runProbeID, runWorkspaceID, runAgentID, runOwnerID)
	assertRunStatementFails(t, db, `UPDATE agent_runs SET model_snapshot='{"model":"changed"}' WHERE id=$1`, runID)
	assertRunStatementFails(t, db, `UPDATE agent_runs SET capability_snapshot='{}' WHERE id=$1`, runID)
	assertRunStatementFails(t, db, `UPDATE agent_runs SET context_policy_snapshot='{"policy":"changed"}' WHERE id=$1`, runID)

	if _, err := db.Exec(`
		UPDATE agent_runs SET status='SUCCEEDED',output_summary='{"answer":"ok"}',
		 finished_at=clock_timestamp(),lock_version=lock_version+1 WHERE id=$1
	`, runID); err != nil {
		t.Fatalf("advance mutable agent run result: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO agent_run_steps(
		 id,workspace_id,run_id,sequence_no,step_type,status,capability_release_id,
		 input_summary,output_summary,finished_at
		) VALUES($1,$2,$3,1,'TOOL','SUCCEEDED',$4,'{"query":"A-1"}',
		 '{"found":true}',clock_timestamp())
	`, runStepID, runWorkspaceID, runID, runReleaseID); err != nil {
		t.Fatalf("insert completed agent run step: %v", err)
	}
	assertRunStatementFails(t, db, `
		INSERT INTO agent_run_steps(
		 id,workspace_id,run_id,sequence_no,step_type,status,finished_at
		) VALUES($1,$2,$3,1,'MODEL','SUCCEEDED',clock_timestamp())
	`, runProbeID, runWorkspaceID, runID)
	assertRunStatementFails(t, db, `
		INSERT INTO agent_run_steps(
		 id,workspace_id,run_id,sequence_no,step_type,status,capability_release_id
		) VALUES($1,$2,$3,2,'TOOL','RUNNING',$4)
	`, runProbeID, runWorkspaceID, runID, runOtherReleaseID)
	assertRunStatementFails(t, db, `
		INSERT INTO agent_run_steps(
		 id,workspace_id,run_id,sequence_no,step_type,status
		) VALUES($1,$2,$3,2,'MODEL','RUNNING')
	`, runProbeID, runOtherWorkspaceID, runID)
	assertRunStatementFails(t, db, `
		INSERT INTO agent_run_steps(
		 id,workspace_id,run_id,sequence_no,step_type,status,error_code
		) VALUES($1,$2,$3,2,'MODEL','FAILED','MODEL_ERROR')
	`, runProbeID, runWorkspaceID, runID)
	assertRunStatementFails(t, db, `UPDATE agent_run_steps SET sequence_no=2 WHERE id=$1`, runStepID)
	assertRunStatementFails(t, db, `UPDATE agent_run_steps SET input_summary='{}' WHERE id=$1`, runStepID)
	assertRunStatementFails(t, db, `DELETE FROM agent_run_steps WHERE id=$1`, runStepID)
	assertRunStatementFails(t, db, `DELETE FROM agent_runs WHERE id=$1`, runID)

	assertRunStatementFails(t, db, `UPDATE chat_sessions SET latest_run_id=$2 WHERE id=$1`, runOtherSessionID, runID)
	assertRunStatementFails(t, db, `
		INSERT INTO chat_messages(
		 id,workspace_id,session_id,role,content,content_sha256,status,run_id
		) VALUES($1,$2,$3,'ASSISTANT','cross run',$4,'EXECUTED',$5)
	`, runProbeID, runOtherWorkspaceID, runOtherSessionID, runContentHash, runID)
}

func insertAgentRun(t *testing.T, db *sql.DB, id, workspaceID, sessionID, agentID string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO agent_runs(
		 id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
		 triggered_by_id,trace_id,model_snapshot,capability_snapshot,context_policy_snapshot,
		 input_summary
		) VALUES($1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'trace-agent-run',
		 '{"provider":"openai","model":"run-model"}',
		 '{"releases":["508f1f2e-7b5a-7c3d-8e9f-12345678900c"]}',
		 '{"memory":false}','{"message":"start run"}')
	`, id, workspaceID, sessionID, agentID, runOwnerID); err != nil {
		t.Fatalf("insert agent run: %v", err)
	}
}

func assertRunStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected agent run statement to fail: %s", strings.TrimSpace(query))
	}
}

func assertAgentRunTablesMissing(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"agent_runs", "agent_run_steps"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("agent run table %s remained after rollback", table)
		}
	}
	var releaseWorkspaceConstraint bool
	if err := db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM pg_constraint
		 WHERE conname='capability_releases_workspace_id_key')
	`).Scan(&releaseWorkspaceConstraint); err != nil {
		t.Fatal(err)
	}
	if releaseWorkspaceConstraint {
		t.Fatal("capability release workspace key remained after rollback")
	}
}
