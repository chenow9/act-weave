// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package execution_test

import (
	"database/sql"
	"strings"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

const (
	executionOwnerID          = "608f1f2e-7b5a-7c3d-8e9f-123456789001"
	executionWorkspaceID      = "608f1f2e-7b5a-7c3d-8e9f-123456789002"
	executionOtherWorkspaceID = "608f1f2e-7b5a-7c3d-8e9f-123456789003"
	executionModelID          = "608f1f2e-7b5a-7c3d-8e9f-123456789004"
	executionOtherModelID     = "608f1f2e-7b5a-7c3d-8e9f-123456789005"
	executionAgentID          = "608f1f2e-7b5a-7c3d-8e9f-123456789006"
	executionOtherAgentID     = "608f1f2e-7b5a-7c3d-8e9f-123456789007"
	executionSessionID        = "608f1f2e-7b5a-7c3d-8e9f-123456789008"
	executionOtherSessionID   = "608f1f2e-7b5a-7c3d-8e9f-123456789009"
	executionAgentRunID       = "608f1f2e-7b5a-7c3d-8e9f-12345678900a"
	executionOtherAgentRunID  = "608f1f2e-7b5a-7c3d-8e9f-12345678900b"
	executionWorkflowID       = "608f1f2e-7b5a-7c3d-8e9f-12345678900c"
	executionOtherWorkflowID  = "608f1f2e-7b5a-7c3d-8e9f-12345678900d"
	executionDraftID          = "608f1f2e-7b5a-7c3d-8e9f-12345678900e"
	executionOtherDraftID     = "608f1f2e-7b5a-7c3d-8e9f-12345678900f"
	executionCompilationID    = "608f1f2e-7b5a-7c3d-8e9f-123456789010"
	executionOtherCompileID   = "608f1f2e-7b5a-7c3d-8e9f-123456789011"
	executionRevisionID       = "608f1f2e-7b5a-7c3d-8e9f-123456789012"
	executionOtherRevisionID  = "608f1f2e-7b5a-7c3d-8e9f-123456789013"
	executionID               = "608f1f2e-7b5a-7c3d-8e9f-123456789014"
	executionStepID           = "608f1f2e-7b5a-7c3d-8e9f-123456789015"
	executionTrialID          = "608f1f2e-7b5a-7c3d-8e9f-123456789016"
	executionProbeID          = "608f1f2e-7b5a-7c3d-8e9f-123456789017"
	executionGraphHash        = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	executionPlanHash         = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

func TestWorkflowExecutionMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 1 || version.Dirty {
		t.Fatalf("expected clean workflow execution migration version 19, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertWorkflowExecutionMigrationFixtures(t, db)
	assertWorkflowExecutionMigrationSchema(t, db)
	assertWorkflowExecutionMigrationConstraints(t, db)

}

func insertWorkflowExecutionMigrationFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'execution.owner','Execution Owner')`, executionOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'execution-space','Execution Space','PRODUCTION',$3,$3,$3),
		($2,'execution-other','Execution Other','SANDBOX',$3,$3,$3)
	`, executionWorkspaceID, executionOtherWorkspaceID, executionOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(
		 id,workspace_id,name,provider,api_base,model_name,created_by,updated_by
		) VALUES
		($1,$3,'Execution Model','openai','https://models.example.test','execution-model',$5,$5),
		($2,$4,'Other Execution Model','openai','https://models.example.test','other-execution-model',$5,$5)
	`, executionModelID, executionOtherModelID, executionWorkspaceID,
		executionOtherWorkspaceID, executionOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES
		($1,$3,'Execution Agent',$5,$7,$7),
		($2,$4,'Other Execution Agent',$6,$7,$7)
	`, executionAgentID, executionOtherAgentID, executionWorkspaceID,
		executionOtherWorkspaceID, executionModelID, executionOtherModelID,
		executionOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by) VALUES
		($1,$3,$5,'Execution session',$7),
		($2,$4,$6,'Other execution session',$7)
	`, executionSessionID, executionOtherSessionID, executionWorkspaceID,
		executionOtherWorkspaceID, executionAgentID, executionOtherAgentID,
		executionOwnerID); err != nil {
		t.Fatal(err)
	}
	insertFixtureAgentRun(t, db, executionAgentRunID, executionWorkspaceID,
		executionSessionID, executionAgentID, "trace-parent-run")
	insertFixtureAgentRun(t, db, executionOtherAgentRunID, executionOtherWorkspaceID,
		executionOtherSessionID, executionOtherAgentID, "trace-other-parent-run")

	if _, err := db.Exec(`
		INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by) VALUES
		($1,$3,'WORKFLOW','Execution Workflow','execution-workflow',$5,$5),
		($2,$4,'WORKFLOW','Other Execution Workflow','other-execution-workflow',$5,$5)
	`, executionWorkflowID, executionOtherWorkflowID, executionWorkspaceID,
		executionOtherWorkspaceID, executionOwnerID); err != nil {
		t.Fatal(err)
	}
	insertFixtureWorkflow(t, db, executionWorkspaceID, executionWorkflowID, executionDraftID)
	insertFixtureWorkflow(t, db, executionOtherWorkspaceID, executionOtherWorkflowID, executionOtherDraftID)
	insertFixtureCompilation(t, db, executionCompilationID, executionWorkspaceID,
		executionWorkflowID, executionDraftID)
	insertFixtureCompilation(t, db, executionOtherCompileID, executionOtherWorkspaceID,
		executionOtherWorkflowID, executionOtherDraftID)
	insertFixtureRevision(t, db, executionRevisionID, executionWorkspaceID,
		executionWorkflowID, executionCompilationID)
	insertFixtureRevision(t, db, executionOtherRevisionID, executionOtherWorkspaceID,
		executionOtherWorkflowID, executionOtherCompileID)
}

func insertFixtureAgentRun(
	t *testing.T,
	db *sql.DB,
	id, workspaceID, sessionID, agentID, traceID string,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO agent_runs(
		 id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
		 triggered_by_id,trace_id,model_snapshot,capability_snapshot
		) VALUES($1,$2,$3,$4,'RUNNING','CHAT','USER',$5,$6,'{}','{}')
	`, id, workspaceID, sessionID, agentID, executionOwnerID, traceID); err != nil {
		t.Fatalf("insert fixture agent run: %v", err)
	}
}

func insertFixtureWorkflow(t *testing.T, db *sql.DB, workspaceID, workflowID, draftID string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO workflows(capability_id,workspace_id,current_draft_id) VALUES($1,$2,$3)
	`, workflowID, workspaceID, draftID); err != nil {
		t.Fatalf("insert fixture workflow: %v", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO workflow_drafts(
		 id,workspace_id,capability_id,draft_version,schema_version,graph,graph_hash,updated_by
		) VALUES($1,$2,$3,1,'workflow.v1','{"nodes":[],"edges":[]}',$4,$5)
	`, draftID, workspaceID, workflowID, executionGraphHash, executionOwnerID); err != nil {
		t.Fatalf("insert fixture workflow draft: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit fixture workflow: %v", err)
	}
}

func insertFixtureCompilation(
	t *testing.T,
	db *sql.DB,
	id, workspaceID, workflowID, draftID string,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO workflow_compilations(
		 id,workspace_id,capability_id,draft_id,draft_version,graph_hash,compiler_version,
		 status,spec,plan,issues,plan_hash,compiled_by
		) VALUES($1,$2,$3,$4,1,$5,'compiler.v1','VALID','{}','{"steps":[]}',
		 '[]',$6,$7)
	`, id, workspaceID, workflowID, draftID, executionGraphHash,
		executionPlanHash, executionOwnerID); err != nil {
		t.Fatalf("insert fixture workflow compilation: %v", err)
	}
}

func insertFixtureRevision(
	t *testing.T,
	db *sql.DB,
	id, workspaceID, workflowID, compilationID string,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO workflow_revisions(
		 id,workspace_id,capability_id,revision_no,source_compilation_id,draft_snapshot,
		 spec_snapshot,plan_snapshot,plan_hash,status,created_by,activated_at
		) VALUES($1,$2,$3,1,$4,'{}','{}','{"steps":[]}',$5,'PUBLISHED',$6,
		 clock_timestamp())
	`, id, workspaceID, workflowID, compilationID, executionPlanHash,
		executionOwnerID); err != nil {
		t.Fatalf("insert fixture workflow revision: %v", err)
	}
}

func assertWorkflowExecutionMigrationSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"workflow_executions", "execution_steps"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected workflow execution table %s", table)
		}
	}
	for _, index := range []string{
		"workflow_executions_workspace_started_idx",
		"workflow_executions_workspace_status_started_idx",
		"workflow_executions_workspace_workflow_started_idx",
		"workflow_executions_trace_idx",
		"execution_steps_execution_sequence_idx",
		"execution_steps_workspace_status_started_idx",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+index).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected workflow execution index %s", index)
		}
	}
	for _, constraint := range []string{
		"workflow_executions_workspace_revision_fk",
		"workflow_executions_workspace_agent_run_fk",
		"execution_steps_workspace_execution_fk",
		"workflow_trial_runs_execution_fk",
	} {
		var deleteAction string
		if err := db.QueryRow(`SELECT confdeltype::TEXT FROM pg_constraint WHERE conname=$1`, constraint).Scan(&deleteAction); err != nil {
			t.Fatalf("read constraint %s: %v", constraint, err)
		}
		if deleteAction != "r" {
			t.Fatalf("expected RESTRICT delete action for %s, got %q", constraint, deleteAction)
		}
	}
}

func assertWorkflowExecutionMigrationConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	insertFixtureWorkflowExecution(t, db, executionID, executionWorkspaceID,
		executionWorkflowID, executionRevisionID, executionAgentRunID)

	assertExecutionStatementFails(t, db, `
		INSERT INTO workflow_executions(
		 id,workspace_id,workflow_id,revision_id,agent_run_id,trigger_type,
		 triggered_by_type,triggered_by_id,trace_id,input_summary
		) VALUES($1,$2,$3,$4,$5,'AGENT','USER',$6,'trace-cross-revision','{}')
	`, executionProbeID, executionWorkspaceID, executionWorkflowID,
		executionOtherRevisionID, executionAgentRunID, executionOwnerID)
	assertExecutionStatementFails(t, db, `
		INSERT INTO workflow_executions(
		 id,workspace_id,workflow_id,revision_id,agent_run_id,trigger_type,
		 triggered_by_type,triggered_by_id,trace_id,input_summary
		) VALUES($1,$2,$3,$4,$5,'AGENT','USER',$6,'trace-cross-run','{}')
	`, executionProbeID, executionWorkspaceID, executionWorkflowID,
		executionRevisionID, executionOtherAgentRunID, executionOwnerID)
	assertExecutionStatementFails(t, db, `
		INSERT INTO workflow_executions(
		 id,workspace_id,workflow_id,revision_id,trigger_type,triggered_by_type,
		 triggered_by_id,trace_id,status,input_summary
		) VALUES($1,$2,$3,$4,'API','USER',$5,'trace-invalid','DONE','[]')
	`, executionProbeID, executionWorkspaceID, executionWorkflowID,
		executionRevisionID, executionOwnerID)
	assertExecutionStatementFails(t, db, `UPDATE workflow_executions SET revision_id=$2,lock_version=2 WHERE id=$1`, executionID, executionOtherRevisionID)
	assertExecutionStatementFails(t, db, `UPDATE workflow_executions SET status='RUNNING' WHERE id=$1`, executionID)

	if _, err := db.Exec(`
		UPDATE workflow_executions SET status='RUNNING',lock_version=lock_version+1 WHERE id=$1
	`, executionID); err != nil {
		t.Fatalf("start workflow execution with optimistic lock: %v", err)
	}
	assertExecutionStatementFails(t, db, `
		UPDATE workflow_executions SET status='PENDING',lock_version=lock_version+1 WHERE id=$1
	`, executionID)
	if _, err := db.Exec(`
		UPDATE workflow_executions SET status='WAITING_CONFIRMATION',
		 output_summary='{"checkpoint":"approval"}',lock_version=lock_version+1 WHERE id=$1
	`, executionID); err != nil {
		t.Fatalf("pause workflow execution: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE workflow_executions SET status='RUNNING',lock_version=lock_version+1 WHERE id=$1
	`, executionID); err != nil {
		t.Fatalf("resume workflow execution: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE workflow_executions SET status='SUCCEEDED',output_summary='{"result":"ok"}',
		 finished_at=clock_timestamp(),lock_version=lock_version+1 WHERE id=$1
	`, executionID); err != nil {
		t.Fatalf("complete workflow execution: %v", err)
	}
	assertExecutionStatementFails(t, db, `
		UPDATE workflow_executions SET output_summary='{}',lock_version=lock_version+1 WHERE id=$1
	`, executionID)

	if _, err := db.Exec(`
		INSERT INTO execution_steps(
		 id,workspace_id,execution_id,node_id,node_type,sequence_no,status,input_summary
		) VALUES($1,$2,$3,'http-1','HTTP',1,'QUEUED','{"url":"https://example.test"}')
	`, executionStepID, executionWorkspaceID, executionID); err != nil {
		t.Fatalf("insert workflow execution step: %v", err)
	}
	assertExecutionStatementFails(t, db, `
		INSERT INTO execution_steps(
		 id,workspace_id,execution_id,node_id,node_type,sequence_no,status
		) VALUES($1,$2,$3,'end','END',1,'QUEUED')
	`, executionProbeID, executionWorkspaceID, executionID)
	assertExecutionStatementFails(t, db, `
		INSERT INTO execution_steps(
		 id,workspace_id,execution_id,node_id,node_type,sequence_no,status
		) VALUES($1,$2,$3,'other','END',2,'QUEUED')
	`, executionProbeID, executionOtherWorkspaceID, executionID)
	assertExecutionStatementFails(t, db, `
		INSERT INTO execution_steps(
		 id,workspace_id,execution_id,node_id,node_type,sequence_no,status,error_code
		) VALUES($1,$2,$3,'failed','HTTP',2,'FAILED','HTTP_ERROR')
	`, executionProbeID, executionWorkspaceID, executionID)
	if _, err := db.Exec(`UPDATE execution_steps SET status='RUNNING' WHERE id=$1`, executionStepID); err != nil {
		t.Fatalf("start workflow execution step: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE execution_steps SET status='WAITING_CONFIRMATION',
		 output_summary='{"checkpoint":"risk"}' WHERE id=$1
	`, executionStepID); err != nil {
		t.Fatalf("pause workflow execution step: %v", err)
	}
	if _, err := db.Exec(`UPDATE execution_steps SET status='RUNNING' WHERE id=$1`, executionStepID); err != nil {
		t.Fatalf("resume workflow execution step: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE execution_steps SET status='SUCCEEDED',output_summary='{"status":200}',
		 finished_at=clock_timestamp() WHERE id=$1
	`, executionStepID); err != nil {
		t.Fatalf("complete workflow execution step: %v", err)
	}
	assertExecutionStatementFails(t, db, `UPDATE execution_steps SET node_id='changed' WHERE id=$1`, executionStepID)
	assertExecutionStatementFails(t, db, `UPDATE execution_steps SET output_summary='{}' WHERE id=$1`, executionStepID)
	assertExecutionStatementFails(t, db, `DELETE FROM execution_steps WHERE id=$1`, executionStepID)
	assertExecutionStatementFails(t, db, `DELETE FROM workflow_executions WHERE id=$1`, executionID)

	if _, err := db.Exec(`
		INSERT INTO workflow_trial_runs(
		 id,workspace_id,capability_id,compilation_id,execution_id,status,input_hash,
		 started_by,finished_at
		) VALUES($1,$2,$3,$4,$5,'SUCCEEDED',$6,$7,clock_timestamp())
	`, executionTrialID, executionWorkspaceID, executionWorkflowID,
		executionCompilationID, executionID, executionGraphHash, executionOwnerID); err != nil {
		t.Fatalf("associate workflow trial with execution: %v", err)
	}
	assertExecutionStatementFails(t, db, `
		INSERT INTO workflow_trial_runs(
		 id,workspace_id,capability_id,compilation_id,execution_id,status,input_hash,
		 started_by
		) VALUES($1,$2,$3,$4,$5,'RUNNING',$6,$7)
	`, executionProbeID, executionOtherWorkspaceID, executionOtherWorkflowID,
		executionOtherCompileID, executionID, executionGraphHash, executionOwnerID)
}

func insertFixtureWorkflowExecution(
	t *testing.T,
	db *sql.DB,
	id, workspaceID, workflowID, revisionID, agentRunID string,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO workflow_executions(
		 id,workspace_id,workflow_id,revision_id,agent_run_id,trigger_type,
		 triggered_by_type,triggered_by_id,trace_id,status,input_summary
		) VALUES($1,$2,$3,$4,$5,'AGENT','USER',$6,'trace-workflow-execution',
		 'PENDING','{"order_id":"A-1"}')
	`, id, workspaceID, workflowID, revisionID, agentRunID, executionOwnerID); err != nil {
		t.Fatalf("insert workflow execution: %v", err)
	}
}

func assertExecutionStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected workflow execution statement to fail: %s", strings.TrimSpace(query))
	}
}

func assertWorkflowExecutionTablesMissing(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"workflow_executions", "execution_steps"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("workflow execution table %s remained after rollback", table)
		}
	}
	var trialCount int
	if err := db.QueryRow(`SELECT count(*) FROM workflow_trial_runs WHERE id=$1`, executionTrialID).Scan(&trialCount); err != nil {
		t.Fatal(err)
	}
	if trialCount != 0 {
		t.Fatal("workflow trial retained a dangling execution reference after rollback")
	}
}
