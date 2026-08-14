// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package workflow_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
)

const (
	workflowOwnerID        = "0f8f1f2e-7b5a-7c3d-8e9f-123456789001"
	workflowWorkspaceID    = "0f8f1f2e-7b5a-7c3d-8e9f-123456789002"
	workflowOtherSpaceID   = "0f8f1f2e-7b5a-7c3d-8e9f-123456789003"
	workflowCapabilityID   = "0f8f1f2e-7b5a-7c3d-8e9f-123456789004"
	workflowToolCapID      = "0f8f1f2e-7b5a-7c3d-8e9f-123456789005"
	workflowOtherCapID     = "0f8f1f2e-7b5a-7c3d-8e9f-123456789006"
	workflowDraftID        = "0f8f1f2e-7b5a-7c3d-8e9f-123456789007"
	workflowOtherDraftID   = "0f8f1f2e-7b5a-7c3d-8e9f-123456789008"
	workflowCompilationID  = "0f8f1f2e-7b5a-7c3d-8e9f-123456789009"
	workflowOtherCompileID = "0f8f1f2e-7b5a-7c3d-8e9f-12345678900a"
	workflowRevisionID     = "0f8f1f2e-7b5a-7c3d-8e9f-12345678900b"
	workflowOtherRevision  = "0f8f1f2e-7b5a-7c3d-8e9f-12345678900c"
	workflowTrialID        = "0f8f1f2e-7b5a-7c3d-8e9f-12345678900d"
	workflowExecutionID    = "0f8f1f2e-7b5a-7c3d-8e9f-12345678900e"
	workflowProbeID        = "0f8f1f2e-7b5a-7c3d-8e9f-12345678900f"
	workflowGraphHash      = "6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b"
	workflowPlanHash       = "d4735e3a265e16eee03f59718b9b5d03019c07d8b6c51f90da3a666eec13ab35"
)

func TestWorkflowMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 23 || version.Dirty {
		t.Fatalf("expected clean workflow migration version 22, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertWorkflowFixtures(t, db)
	assertWorkflowSchema(t, db)
	assertWorkflowConstraints(t, db)

}

func insertWorkflowFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'workflow.owner','Workflow Owner')`, workflowOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'workflow-space','Workflow Space','PRODUCTION',$3,$3,$3),
		($2,'workflow-other','Workflow Other','SANDBOX',$3,$3,$3)
	`, workflowWorkspaceID, workflowOtherSpaceID, workflowOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by) VALUES
		($1,$4,'WORKFLOW','Order Workflow','order-workflow',$6,$6),
		($2,$4,'TOOL','Order Tool','order-tool',$6,$6),
		($3,$5,'WORKFLOW','Other Workflow','other-workflow',$6,$6)
	`, workflowCapabilityID, workflowToolCapID, workflowOtherCapID,
		workflowWorkspaceID, workflowOtherSpaceID, workflowOwnerID); err != nil {
		t.Fatal(err)
	}
	insertWorkflowAndDraft(t, db, workflowWorkspaceID, workflowCapabilityID, workflowDraftID)
	insertWorkflowAndDraft(t, db, workflowOtherSpaceID, workflowOtherCapID, workflowOtherDraftID)
}

func insertWorkflowAndDraft(t *testing.T, db *sql.DB, workspaceID, capabilityID, draftID string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO workflows(capability_id,workspace_id,current_draft_id) VALUES($1,$2,$3)
	`, capabilityID, workspaceID, draftID); err != nil {
		t.Fatalf("insert workflow specialization: %v", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO workflow_drafts(
		 id,workspace_id,capability_id,draft_version,schema_version,graph,graph_hash,updated_by)
		VALUES($1,$2,$3,1,'workflow.v1','{"nodes":[],"edges":[]}',$4,$5)
	`, draftID, workspaceID, capabilityID, workflowGraphHash, workflowOwnerID); err != nil {
		t.Fatalf("insert workflow draft: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit workflow and draft: %v", err)
	}
}

func assertWorkflowSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, forbidden := range []string{"dsl", "canvas_graph", "readiness", "agent_id"} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM information_schema.columns
			 WHERE table_schema='public' AND table_name='workflows' AND column_name=$1)
		`, forbidden).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("workflows must not contain compatibility fact column %s", forbidden)
		}
	}
	for _, indexName := range []string{
		"workflow_drafts_workspace_updated_idx",
		"workflow_compilations_workspace_capability_compiled_idx",
		"workflow_revisions_workspace_capability_revision_idx",
		"workflow_trial_runs_workspace_status_started_idx",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+indexName).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected workflow index %s", indexName)
		}
	}
}

func assertWorkflowConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	assertWorkflowStatementFails(t, db, `
		INSERT INTO workflows(capability_id,workspace_id,current_draft_id) VALUES($1,$2,$3)
	`, workflowToolCapID, workflowWorkspaceID, workflowProbeID)
	assertWorkflowStatementFails(t, db, `
		INSERT INTO workflow_drafts(
		 id,workspace_id,capability_id,draft_version,schema_version,graph,graph_hash,updated_by)
		VALUES($1,$2,$3,2,'workflow.v1','{}',$4,$5)
	`, workflowProbeID, workflowWorkspaceID, workflowCapabilityID, workflowGraphHash, workflowOwnerID)
	assertWorkflowStatementFails(t, db, `
		UPDATE workflow_drafts SET graph='[]' WHERE id=$1
	`, workflowDraftID)
	if _, err := db.Exec(`
		UPDATE workflow_drafts SET draft_version=2,graph='{"nodes":[{"id":"start"}],"edges":[]}',
		 graph_hash=$2,updated_at=clock_timestamp(),lock_version=lock_version+1 WHERE id=$1
	`, workflowDraftID, workflowPlanHash); err != nil {
		t.Fatalf("update mutable workflow draft: %v", err)
	}

	insertWorkflowCompilation(t, db, workflowCompilationID, workflowWorkspaceID,
		workflowCapabilityID, workflowDraftID, 2, "VALID")
	insertWorkflowCompilation(t, db, workflowOtherCompileID, workflowOtherSpaceID,
		workflowOtherCapID, workflowOtherDraftID, 1, "VALID")
	assertWorkflowStatementFails(t, db, `UPDATE workflow_compilations SET plan='{}' WHERE id=$1`, workflowCompilationID)
	assertWorkflowStatementFails(t, db, `DELETE FROM workflow_compilations WHERE id=$1`, workflowCompilationID)
	assertWorkflowStatementFails(t, db, `
		INSERT INTO workflow_compilations(
		 id,workspace_id,capability_id,draft_id,draft_version,graph_hash,compiler_version,
		 status,spec,plan,issues,plan_hash,compiled_by)
		VALUES($1,$2,$3,$4,1,$5,'compiler.v1','VALID','{}','{}','[]',$6,$7)
	`, workflowProbeID, workflowWorkspaceID, workflowCapabilityID, workflowOtherDraftID,
		workflowGraphHash, workflowPlanHash, workflowOwnerID)

	insertWorkflowRevision(t, db, workflowRevisionID, workflowWorkspaceID,
		workflowCapabilityID, workflowCompilationID)
	insertWorkflowRevision(t, db, workflowOtherRevision, workflowOtherSpaceID,
		workflowOtherCapID, workflowOtherCompileID)
	if _, err := db.Exec(`
		UPDATE workflows SET active_revision_id=$3,latest_compilation_id=$4
		WHERE workspace_id=$1 AND capability_id=$2
	`, workflowWorkspaceID, workflowCapabilityID, workflowRevisionID, workflowCompilationID); err != nil {
		t.Fatalf("activate workflow revision: %v", err)
	}
	assertWorkflowStatementFails(t, db, `
		UPDATE workflows SET active_revision_id=$3 WHERE workspace_id=$1 AND capability_id=$2
	`, workflowOtherSpaceID, workflowOtherCapID, workflowRevisionID)
	assertWorkflowStatementFails(t, db, `
		UPDATE workflows SET latest_compilation_id=$3 WHERE workspace_id=$1 AND capability_id=$2
	`, workflowOtherSpaceID, workflowOtherCapID, workflowCompilationID)
	assertWorkflowStatementFails(t, db, `UPDATE workflow_revisions SET plan_snapshot='{}' WHERE id=$1`, workflowRevisionID)
	assertWorkflowStatementFails(t, db, `DELETE FROM workflow_revisions WHERE id=$1`, workflowRevisionID)

	if _, err := db.Exec(`
		INSERT INTO workflow_trial_runs(
		 id,workspace_id,capability_id,compilation_id,execution_id,status,input_hash,
		 started_by,finished_at)
		VALUES($1,$2,$3,$4,$5,'SUCCEEDED',$6,$7,clock_timestamp())
	`, workflowTrialID, workflowWorkspaceID, workflowCapabilityID, workflowCompilationID,
		workflowExecutionID, workflowGraphHash, workflowOwnerID); err != nil {
		t.Fatalf("insert workflow trial: %v", err)
	}
	assertWorkflowStatementFails(t, db, `
		INSERT INTO workflow_trial_runs(
		 id,workspace_id,capability_id,compilation_id,execution_id,status,input_hash,started_by)
		VALUES($1,$2,$3,$4,$5,'SUCCEEDED',$6,$7)
	`, workflowProbeID, workflowWorkspaceID, workflowCapabilityID, workflowCompilationID,
		workflowExecutionID, workflowGraphHash, workflowOwnerID)
}

func insertWorkflowCompilation(
	t *testing.T,
	db *sql.DB,
	id, workspaceID, capabilityID, draftID string,
	draftVersion int,
	status string,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO workflow_compilations(
		 id,workspace_id,capability_id,draft_id,draft_version,graph_hash,compiler_version,
		 status,spec,plan,issues,plan_hash,compiled_by)
		VALUES($1,$2,$3,$4,$5,$6,'compiler.v1',$7,'{"inputs":{}}',
		 '{"steps":[]}','[]',$8,$9)
	`, id, workspaceID, capabilityID, draftID, draftVersion, workflowPlanHash,
		status, workflowGraphHash, workflowOwnerID); err != nil {
		t.Fatalf("insert workflow compilation: %v", err)
	}
}

func insertWorkflowRevision(
	t *testing.T,
	db *sql.DB,
	id, workspaceID, capabilityID, compilationID string,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO workflow_revisions(
		 id,workspace_id,capability_id,revision_no,source_compilation_id,draft_snapshot,
		 spec_snapshot,plan_snapshot,plan_hash,status,publish_note,created_by,activated_at)
		VALUES($1,$2,$3,1,$4,'{"nodes":[],"edges":[]}','{"inputs":{}}',
		 '{"steps":[]}',$5,'PUBLISHED','initial',$6,clock_timestamp())
	`, id, workspaceID, capabilityID, compilationID, workflowGraphHash, workflowOwnerID); err != nil {
		t.Fatalf("insert workflow revision: %v", err)
	}
}

func assertWorkflowStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected workflow statement to fail: %s", strings.TrimSpace(query))
	}
}

func assertWorkflowTablesMissing(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, table := range []string{
		"workflow_trial_runs", "workflow_revisions", "workflow_compilations",
		"workflow_drafts", "workflows",
	} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("expected rolled-back %s to be absent", table)
		}
	}
}
