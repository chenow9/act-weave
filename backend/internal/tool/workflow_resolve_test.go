package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"actweave/backend/internal/capability"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
)

const (
	wfResolveOwnerID       = "b18f1f2e-7b5a-7c3d-8e9f-123456789001"
	wfResolveWorkspaceID   = "b18f1f2e-7b5a-7c3d-8e9f-123456789002"
	wfResolveCapabilityID  = "b18f1f2e-7b5a-7c3d-8e9f-123456789003"
	wfResolveReleaseID     = "b18f1f2e-7b5a-7c3d-8e9f-123456789004"
	wfResolveRevisionID    = "b18f1f2e-7b5a-7c3d-8e9f-123456789005"
	wfResolveDraftID       = "b18f1f2e-7b5a-7c3d-8e9f-123456789006"
	wfResolveCompilationID = "b18f1f2e-7b5a-7c3d-8e9f-123456789007"
	wfResolveChecksum      = "5994471abb01112afcc18159f6cc74b4f511b99806da59b3caf5a9c173cacfc5"
)

// P3.4: ResolveInvocation accepts published WORKFLOW_REVISION capabilities.
func TestResolveInvocationPublishedWorkflow(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertWorkflowResolveFixtures(t, db, true)

	resolver, err := NewInvocationResolver(db)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.ResolveInvocation(context.Background(), execution.ResolveRequest{
		WorkspaceID: wfResolveWorkspaceID, CapabilityID: wfResolveCapabilityID, ReleaseID: wfResolveReleaseID,
	})
	if err != nil {
		t.Fatalf("resolve published WORKFLOW: %v", err)
	}
	if resolved.Snapshot.ExecutorType != execution.ExecutorTypeWORKFLOW {
		t.Fatalf("executor type=%q want WORKFLOW", resolved.Snapshot.ExecutorType)
	}
	if resolved.Snapshot.ToolVersionID != wfResolveRevisionID {
		t.Fatalf("toolVersionId (revision)=%q want %s", resolved.Snapshot.ToolVersionID, wfResolveRevisionID)
	}
	if resolved.Snapshot.ReleaseID != wfResolveReleaseID ||
		resolved.Snapshot.CapabilityID != wfResolveCapabilityID {
		t.Fatalf("snapshot ids: %+v", resolved.Snapshot)
	}
	if !resolved.Credential.BypassOutboundIdentity || resolved.Credential.AuthMode != "" ||
		resolved.Connection.WorkspaceID != wfResolveWorkspaceID {
		t.Fatalf("WORKFLOW must bypass outbound identity without synthetic NONE: conn=%+v cred=%+v",
			resolved.Connection, resolved.Credential)
	}
	if !strings.Contains(string(resolved.Snapshot.ActionConfig), wfResolveRevisionID) {
		t.Fatalf("actionConfig missing revision: %s", resolved.Snapshot.ActionConfig)
	}
}

// Unpublished WORKFLOW (no active release) cannot resolve.
func TestResolveInvocationUnpublishedWorkflowRejected(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertWorkflowResolveFixtures(t, db, false)

	resolver, err := NewInvocationResolver(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.ResolveInvocation(context.Background(), execution.ResolveRequest{
		WorkspaceID: wfResolveWorkspaceID, CapabilityID: wfResolveCapabilityID,
	})
	if err == nil {
		t.Fatal("expected unpublished WORKFLOW resolve to fail")
	}
}

func insertWorkflowResolveFixtures(t *testing.T, db *sql.DB, published bool) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO users(id,username,display_name) VALUES($1,'wf.resolve','WF Resolve')`,
		wfResolveOwnerID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'wf-resolve','WF Resolve','SANDBOX',$2,$2,$2)
	`, wfResolveWorkspaceID, wfResolveOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by)
		VALUES($1,$2,'WORKFLOW','Resolve Workflow','resolve-workflow',$3,$3)
	`, wfResolveCapabilityID, wfResolveWorkspaceID, wfResolveOwnerID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO workflows(capability_id,workspace_id,current_draft_id) VALUES($1,$2,$3)
	`, wfResolveCapabilityID, wfResolveWorkspaceID, wfResolveDraftID); err != nil {
		t.Fatalf("workflows: %v", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO workflow_drafts(
		 id,workspace_id,capability_id,draft_version,schema_version,graph,graph_hash,updated_by)
		VALUES($1,$2,$3,1,'workflow.v1','{"nodes":[],"edges":[]}',$4,$5)
	`, wfResolveDraftID, wfResolveWorkspaceID, wfResolveCapabilityID, wfResolveChecksum, wfResolveOwnerID); err != nil {
		t.Fatalf("drafts: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if !published {
		return
	}
	planJSON := []byte(`{"workflowId":"` + wfResolveCapabilityID + `","nodes":[]}`)
	if _, err := db.Exec(`
		INSERT INTO workflow_compilations(
		 id,workspace_id,capability_id,draft_id,draft_version,graph_hash,compiler_version,
		 status,spec,plan,issues,plan_hash,compiled_by)
		VALUES($1,$2,$3,$4,1,$5,'test-compiler','VALID','{"inputs":{}}',$6,'[]',$5,$7)
	`, wfResolveCompilationID, wfResolveWorkspaceID, wfResolveCapabilityID, wfResolveDraftID,
		wfResolveChecksum, planJSON, wfResolveOwnerID); err != nil {
		t.Fatalf("compilations: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workflow_revisions(
		 id,workspace_id,capability_id,revision_no,source_compilation_id,draft_snapshot,
		 spec_snapshot,plan_snapshot,plan_hash,status,publish_note,created_by,activated_at)
		VALUES($1,$2,$3,1,$4,'{"nodes":[],"edges":[]}','{"inputs":{}}',
		 $5,$6,'PUBLISHED','test',$7,clock_timestamp())
	`, wfResolveRevisionID, wfResolveWorkspaceID, wfResolveCapabilityID, wfResolveCompilationID,
		planJSON, wfResolveChecksum, wfResolveOwnerID); err != nil {
		t.Fatalf("revisions: %v", err)
	}
	caps, err := capability.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := caps.Publish(context.Background(), capability.PublishRelease{
		ID: wfResolveReleaseID, WorkspaceID: wfResolveWorkspaceID, CapabilityID: wfResolveCapabilityID,
		SourceType: "WORKFLOW_REVISION", SourceID: wfResolveRevisionID,
		CallableName: "resolve_workflow", CallableDescription: "Resolve Workflow",
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		RiskLevel: "LOW", SideEffectLevel: "READ", Checksum: wfResolveChecksum, PublishedBy: wfResolveOwnerID,
	}); err != nil {
		t.Fatalf("publish workflow capability: %v", err)
	}
}
