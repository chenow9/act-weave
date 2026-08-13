package capability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/modelconfig"
)

const (
	bindingOwnerID       = "078f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	bindingWorkspaceID   = "078f1f2e-7b5a-7c3d-8e9f-1234567890a2"
	bindingOtherSpaceID  = "078f1f2e-7b5a-7c3d-8e9f-1234567890a3"
	bindingModelID       = "078f1f2e-7b5a-7c3d-8e9f-1234567890a4"
	bindingOtherModelID  = "078f1f2e-7b5a-7c3d-8e9f-1234567890a5"
	bindingAgentID       = "078f1f2e-7b5a-7c3d-8e9f-1234567890a6"
	bindingSecondAgentID = "078f1f2e-7b5a-7c3d-8e9f-1234567890a7"
	bindingOtherAgentID  = "078f1f2e-7b5a-7c3d-8e9f-1234567890a8"
	bindingCapabilityID  = "078f1f2e-7b5a-7c3d-8e9f-1234567890a9"
	bindingOtherCapID    = "078f1f2e-7b5a-7c3d-8e9f-1234567890aa"
	bindingReleaseID     = "078f1f2e-7b5a-7c3d-8e9f-1234567890ab"
	bindingRelease2ID    = "078f1f2e-7b5a-7c3d-8e9f-1234567890b3"
	bindingRelease3ID    = "078f1f2e-7b5a-7c3d-8e9f-1234567890b4"
	bindingOtherRelID    = "078f1f2e-7b5a-7c3d-8e9f-1234567890ac"
	bindingSourceID      = "078f1f2e-7b5a-7c3d-8e9f-1234567890ad"
	bindingProviderID    = "078f1f2e-7b5a-7c3d-8e9f-1234567890ae"
	bindingProvider2ID   = "078f1f2e-7b5a-7c3d-8e9f-1234567890af"
	bindingConnectionID  = "078f1f2e-7b5a-7c3d-8e9f-1234567890b0"
	bindingConnection2ID = "078f1f2e-7b5a-7c3d-8e9f-1234567890b1"
	bindingOtherConnID   = "078f1f2e-7b5a-7c3d-8e9f-1234567890b2"
	bindingChecksum      = "5994471abb01112afcc18159f6cc74b4f511b99806da59b3caf5a9c173cacfc5"
)

func TestCapabilityBindingMigration(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 22 || version.Dirty {
		t.Fatalf("expected binding migration version 12, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertBindingFixtures(t, db)

	if _, err := db.Exec(`
		INSERT INTO agent_capability_bindings(
			workspace_id,agent_id,capability_id,version_policy,connection_id,bound_by
		) VALUES($1,$2,$3,'FOLLOW_ACTIVE',$4,$5)
	`, bindingWorkspaceID, bindingAgentID, bindingCapabilityID, bindingConnectionID, bindingOwnerID); err != nil {
		t.Fatalf("insert follow-active binding: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_capability_bindings(
			workspace_id,agent_id,capability_id,version_policy,pinned_release_id,connection_id,bound_by
		) VALUES($1,$2,$3,'PINNED',$4,$5,$6)
	`, bindingWorkspaceID, bindingSecondAgentID, bindingCapabilityID, bindingReleaseID, bindingConnection2ID, bindingOwnerID); err != nil {
		t.Fatalf("bind same capability to second agent: %v", err)
	}
	assertBindingStatementFails(t, db, `
		INSERT INTO agent_capability_bindings(workspace_id,agent_id,capability_id,version_policy,bound_by)
		VALUES($1,$2,$3,'FOLLOW_ACTIVE',$4)
	`, bindingWorkspaceID, bindingOtherAgentID, bindingCapabilityID, bindingOwnerID)
	assertBindingStatementFails(t, db, `
		UPDATE agent_capability_bindings SET version_policy='PINNED',pinned_release_id=$3
		WHERE agent_id=$1 AND capability_id=$2
	`, bindingAgentID, bindingCapabilityID, bindingOtherRelID)
	assertBindingStatementFails(t, db, `
		UPDATE agent_capability_bindings SET pinned_release_id=$3
		WHERE agent_id=$1 AND capability_id=$2
	`, bindingAgentID, bindingCapabilityID, bindingReleaseID)
	assertBindingStatementFails(t, db, `
		UPDATE agent_capability_bindings SET connection_id=$3
		WHERE agent_id=$1 AND capability_id=$2
	`, bindingAgentID, bindingCapabilityID, bindingOtherConnID)
	assertBindingStatementFails(t, db, `
		UPDATE agent_capability_bindings SET config_overrides='[]'::JSONB
		WHERE agent_id=$1 AND capability_id=$2
	`, bindingAgentID, bindingCapabilityID)

}

func TestCapabilityBindingServiceValidatesCompatibilityAndCatalog(t *testing.T) {
	repository, db := newBindingRepositoryTest(t)
	compatibility := ConnectionCompatibilityFunc(func(_ context.Context, workspaceID, capabilityID, connectionID string) error {
		if workspaceID != bindingWorkspaceID || capabilityID != bindingCapabilityID ||
			(connectionID != bindingConnectionID && connectionID != bindingConnection2ID) {
			return ErrInvalid
		}
		return nil
	})
	service, err := NewBindingService(repository, compatibility)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Bind(context.Background(), BindInput{
		WorkspaceID: bindingWorkspaceID, AgentID: bindingAgentID, CapabilityID: bindingCapabilityID,
		VersionPolicy: "FOLLOW_ACTIVE", ConnectionID: stringReference(bindingConnectionID), Enabled: true,
		ConfigOverrides: json.RawMessage(`{"timeoutMs":1000}`), BoundBy: bindingOwnerID,
	})
	if err != nil || created.LockVersion != 1 {
		t.Fatalf("create binding: %+v err=%v", created, err)
	}
	if _, err := service.Bind(context.Background(), BindInput{
		WorkspaceID: bindingWorkspaceID, AgentID: bindingSecondAgentID, CapabilityID: bindingCapabilityID,
		VersionPolicy: "FOLLOW_ACTIVE", ConnectionID: stringReference(bindingConnection2ID), Enabled: true,
		BoundBy: bindingOwnerID,
	}); err != nil {
		t.Fatalf("bind same capability to another agent/connection: %v", err)
	}
	if _, err := service.Bind(context.Background(), BindInput{
		WorkspaceID: bindingWorkspaceID, AgentID: bindingAgentID, CapabilityID: bindingOtherCapID,
		VersionPolicy: "FOLLOW_ACTIVE", ConnectionID: stringReference(bindingConnectionID), Enabled: true,
		BoundBy: bindingOwnerID,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected provider compatibility rejection, got %v", err)
	}
	if _, err := repository.Bind(context.Background(), BindInput{
		WorkspaceID: bindingWorkspaceID, AgentID: bindingAgentID, CapabilityID: bindingOtherCapID,
		VersionPolicy: "FOLLOW_ACTIVE", ConnectionID: stringReference(bindingOtherConnID), Enabled: true,
		BoundBy: bindingOwnerID,
	}); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected cross-workspace connection rejection, got %v", err)
	}
	updated, err := service.Bind(context.Background(), BindInput{
		WorkspaceID: bindingWorkspaceID, AgentID: bindingAgentID, CapabilityID: bindingCapabilityID,
		VersionPolicy: "PINNED", PinnedReleaseID: stringReference(bindingReleaseID),
		ConnectionID: stringReference(bindingConnectionID), Enabled: true,
		ConfigOverrides: json.RawMessage(`{"timeoutMs":2000}`), BoundBy: bindingOwnerID,
		ExpectedLockVersion: created.LockVersion,
	})
	if err != nil || updated.LockVersion != 2 || updated.PinnedReleaseID == nil {
		t.Fatalf("update pinned binding: %+v err=%v", updated, err)
	}
	if _, err := service.Bind(context.Background(), BindInput{
		WorkspaceID: bindingWorkspaceID, AgentID: bindingAgentID, CapabilityID: bindingCapabilityID,
		VersionPolicy: "FOLLOW_ACTIVE", Enabled: true, BoundBy: bindingOwnerID,
		ExpectedLockVersion: created.LockVersion,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale binding conflict, got %v", err)
	}
	catalog, err := NewCatalog(repository, repository)
	if err != nil {
		t.Fatal(err)
	}
	descriptors, err := catalog.ListForAgent(context.Background(), bindingWorkspaceID, bindingAgentID)
	if err != nil || len(descriptors) != 1 || descriptors[0].ReleaseID != bindingReleaseID {
		t.Fatalf("catalog did not consume concrete bindings: %+v err=%v", descriptors, err)
	}
	if err := service.Unbind(context.Background(), bindingWorkspaceID, bindingAgentID, bindingCapabilityID, updated.LockVersion); err != nil {
		t.Fatalf("unbind capability: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM agent_capability_bindings WHERE agent_id=$1`, bindingAgentID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("binding remained after unbind: count=%d", count)
	}
}

// UT-BIND-PUBLISHED / P3.3: unpublished WORKFLOW (no active release) cannot bind.
func TestBindUnpublishedWorkflowRejected(t *testing.T) {
	repository, db := newBindingRepositoryTest(t)
	const unpublishedWorkflowID = "078f1f2e-7b5a-7c3d-8e9f-1234567890c1"
	if _, err := db.Exec(`
		INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by)
		VALUES($1,$2,'WORKFLOW','Unpublished Workflow','unpublished-workflow',$3,$3)
	`, unpublishedWorkflowID, bindingWorkspaceID, bindingOwnerID); err != nil {
		t.Fatalf("insert unpublished workflow capability: %v", err)
	}
	compatibility := ConnectionCompatibilityFunc(func(context.Context, string, string, string) error {
		return nil
	})
	service, err := NewBindingService(repository, compatibility)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Bind(context.Background(), BindInput{
		WorkspaceID: bindingWorkspaceID, AgentID: bindingAgentID, CapabilityID: unpublishedWorkflowID,
		VersionPolicy: "FOLLOW_ACTIVE", Enabled: true, BoundBy: bindingOwnerID,
	}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected unpublished WORKFLOW bind to fail with ErrUnavailable, got %v", err)
	}
	var count int
	if err := db.QueryRow(`
		SELECT count(*) FROM agent_capability_bindings
		WHERE workspace_id=$1 AND agent_id=$2 AND capability_id=$3
	`, bindingWorkspaceID, bindingAgentID, unpublishedWorkflowID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unpublished WORKFLOW must not leave a binding row, count=%d", count)
	}
}

// P3.1 / P3.2 / UT-BIND-PUBLISHED: published WORKFLOW binds successfully; catalog resolves it.
func TestBindPublishedWorkflowSucceedsAndCatalogResolves(t *testing.T) {
	repository, db := newBindingRepositoryTest(t)
	const (
		workflowCapID     = "078f1f2e-7b5a-7c3d-8e9f-1234567890c2"
		workflowReleaseID = "078f1f2e-7b5a-7c3d-8e9f-1234567890c3"
		workflowSourceID  = "078f1f2e-7b5a-7c3d-8e9f-1234567890c4"
	)
	if _, err := db.Exec(`
		INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by)
		VALUES($1,$2,'WORKFLOW','Published Workflow','published-workflow',$3,$3)
	`, workflowCapID, bindingWorkspaceID, bindingOwnerID); err != nil {
		t.Fatalf("insert workflow capability: %v", err)
	}
	// Pre-publish: still no active release → bind rejected.
	compatibility := ConnectionCompatibilityFunc(func(context.Context, string, string, string) error {
		return nil
	})
	service, err := NewBindingService(repository, compatibility)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Bind(context.Background(), BindInput{
		WorkspaceID: bindingWorkspaceID, AgentID: bindingAgentID, CapabilityID: workflowCapID,
		VersionPolicy: "FOLLOW_ACTIVE", Enabled: true, BoundBy: bindingOwnerID,
	}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("pre-publish WORKFLOW bind should fail, got %v", err)
	}

	_, release, err := repository.Publish(context.Background(), PublishRelease{
		ID: workflowReleaseID, WorkspaceID: bindingWorkspaceID, CapabilityID: workflowCapID,
		SourceType: "WORKFLOW_REVISION", SourceID: workflowSourceID,
		CallableName: "published_workflow", CallableDescription: "Published Workflow",
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		RiskLevel: "LOW", SideEffectLevel: "READ", Checksum: bindingChecksum, PublishedBy: bindingOwnerID,
	})
	if err != nil {
		t.Fatalf("publish workflow capability: %v", err)
	}

	// Default agentId path (D12): bind to the generate-session agent after publish.
	created, err := service.Bind(context.Background(), BindInput{
		WorkspaceID: bindingWorkspaceID, AgentID: bindingAgentID, CapabilityID: workflowCapID,
		VersionPolicy: "FOLLOW_ACTIVE", Enabled: true, BoundBy: bindingOwnerID,
	})
	if err != nil || created.LockVersion != 1 || !created.Enabled {
		t.Fatalf("bind published WORKFLOW: %+v err=%v", created, err)
	}

	catalog, err := NewCatalog(repository, repository)
	if err != nil {
		t.Fatal(err)
	}
	// P3.4: agentrun capability resolution surfaces bound WORKFLOW.
	descriptors, err := catalog.ListForAgent(context.Background(), bindingWorkspaceID, bindingAgentID)
	if err != nil {
		t.Fatalf("ListForAgent: %v", err)
	}
	var found *Descriptor
	for i := range descriptors {
		if descriptors[i].CapabilityID == workflowCapID {
			found = &descriptors[i]
			break
		}
	}
	if found == nil || found.Kind != "WORKFLOW" || found.ReleaseID != release.ID ||
		found.CallableName != "published_workflow" {
		t.Fatalf("catalog did not resolve bound WORKFLOW: %+v descriptors=%+v", found, descriptors)
	}
}

func TestCapabilityMultiAgentVersionAcceptance(t *testing.T) {
	repository, db := newBindingRepositoryTest(t)
	_, secondRelease, err := repository.Publish(context.Background(), bindingPublishInput(
		bindingRelease2ID, "binding_tool_v2", "Binding tool release two",
	))
	if err != nil {
		t.Fatalf("publish release two: %v", err)
	}
	compatibility := ConnectionCompatibilityFunc(func(_ context.Context, workspaceID, capabilityID, connectionID string) error {
		if workspaceID == bindingWorkspaceID && capabilityID == bindingCapabilityID &&
			(connectionID == bindingConnectionID || connectionID == bindingConnection2ID) {
			return nil
		}
		return ErrInvalid
	})
	service, err := NewBindingService(repository, compatibility)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Bind(context.Background(), BindInput{
		WorkspaceID: bindingWorkspaceID, AgentID: bindingAgentID, CapabilityID: bindingCapabilityID,
		VersionPolicy: "PINNED", PinnedReleaseID: stringReference(bindingReleaseID),
		ConnectionID: stringReference(bindingConnectionID), Enabled: true, BoundBy: bindingOwnerID,
	}); err != nil {
		t.Fatalf("bind pinned agent: %v", err)
	}
	if _, err := service.Bind(context.Background(), BindInput{
		WorkspaceID: bindingWorkspaceID, AgentID: bindingSecondAgentID, CapabilityID: bindingCapabilityID,
		VersionPolicy: "FOLLOW_ACTIVE", ConnectionID: stringReference(bindingConnection2ID),
		Enabled: true, BoundBy: bindingOwnerID,
	}); err != nil {
		t.Fatalf("bind follow-active agent: %v", err)
	}
	catalog, err := NewCatalog(repository, repository)
	if err != nil {
		t.Fatal(err)
	}
	pinnedAssets, err := catalog.ListForAgent(context.Background(), bindingWorkspaceID, bindingAgentID)
	if err != nil || len(pinnedAssets) != 1 || pinnedAssets[0].ReleaseID != bindingReleaseID {
		t.Fatalf("unexpected pinned assets before switch: %+v err=%v", pinnedAssets, err)
	}
	followAssets, err := catalog.ListForAgent(context.Background(), bindingWorkspaceID, bindingSecondAgentID)
	if err != nil || len(followAssets) != 1 || followAssets[0].ReleaseID != secondRelease.ID {
		t.Fatalf("unexpected active assets before switch: %+v err=%v", followAssets, err)
	}
	_, thirdRelease, err := repository.Publish(context.Background(), bindingPublishInput(
		bindingRelease3ID, "binding_tool_v3", "Binding tool release three",
	))
	if err != nil {
		t.Fatalf("publish release three: %v", err)
	}
	pinnedAssets, err = catalog.ListForAgent(context.Background(), bindingWorkspaceID, bindingAgentID)
	if err != nil || pinnedAssets[0].ReleaseID != bindingReleaseID {
		t.Fatalf("pinned agent drifted after active switch: %+v err=%v", pinnedAssets, err)
	}
	followAssets, err = catalog.ListForAgent(context.Background(), bindingWorkspaceID, bindingSecondAgentID)
	if err != nil || followAssets[0].ReleaseID != thirdRelease.ID {
		t.Fatalf("follow-active agent did not switch: %+v err=%v", followAssets, err)
	}
	crossAssets, err := catalog.ListForAgent(context.Background(), bindingOtherSpaceID, bindingAgentID)
	if err != nil || len(crossAssets) != 0 {
		t.Fatalf("cross-workspace catalog exposure: %+v err=%v", crossAssets, err)
	}
	var distinctConnections int
	if err := db.QueryRow(`
		SELECT count(DISTINCT connection_id) FROM agent_capability_bindings
		WHERE workspace_id=$1 AND capability_id=$2
	`, bindingWorkspaceID, bindingCapabilityID).Scan(&distinctConnections); err != nil {
		t.Fatal(err)
	}
	if distinctConnections != 2 {
		t.Fatalf("same capability did not preserve per-agent connections: %d", distinctConnections)
	}
	for _, table := range []string{"capabilities", "capability_releases"} {
		var agentColumn bool
		if err := db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM information_schema.columns
			WHERE table_schema='public' AND table_name=$1 AND column_name='agent_id')
		`, table).Scan(&agentColumn); err != nil {
			t.Fatal(err)
		}
		if agentColumn {
			t.Fatalf("%s retains single-agent factual ownership", table)
		}
	}
}

func bindingPublishInput(id, callableName, description string) PublishRelease {
	return PublishRelease{
		ID: id, WorkspaceID: bindingWorkspaceID, CapabilityID: bindingCapabilityID,
		SourceType: "TOOL_VERSION", SourceID: bindingSourceID, CallableName: callableName,
		CallableDescription: description, InputSchema: json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`), RiskLevel: "LOW",
		SideEffectLevel: "READ", Checksum: bindingChecksum, PublishedBy: bindingOwnerID,
	}
}

func newBindingRepositoryTest(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 22 || version.Dirty {
		t.Fatalf("unexpected migration: %+v", version)
	}
	db := testDatabase.Open(t)
	insertBindingFixtures(t, db)
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db
}

func insertBindingFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,username,display_name) VALUES($1,'binding.owner','Binding Owner')`, []any{bindingOwnerID}},
		{`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		 ($1,'binding-workspace','Binding Workspace','PRODUCTION',$3,$3,$3),
		 ($2,'binding-other','Binding Other','SANDBOX',$3,$3,$3)`, []any{bindingWorkspaceID, bindingOtherSpaceID, bindingOwnerID}},
		{`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES
		 ($1,$3,'Binding Model','OPENAI_COMPATIBLE','https://models.example/v1','binding-model',$5,$5),
		 ($2,$4,'Binding Other Model','OPENAI_COMPATIBLE','https://models.example/v1','binding-other',$5,$5)`, []any{bindingModelID, bindingOtherModelID, bindingWorkspaceID, bindingOtherSpaceID, bindingOwnerID}},
		{`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES
		 ($1,$4,'Binding Agent',$6,$7,$7),($2,$4,'Binding Second Agent',$6,$7,$7),
		 ($3,$5,'Binding Other Agent',$8,$7,$7)`, []any{bindingAgentID, bindingSecondAgentID, bindingOtherAgentID, bindingWorkspaceID, bindingOtherSpaceID, bindingModelID, bindingOwnerID, bindingOtherModelID}},
		{`INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by) VALUES
		 ($1,$3,'TOOL','Binding Tool','binding-tool',$5,$5),
		 ($2,$4,'TOOL','Binding Other Tool','binding-other-tool',$5,$5)`, []any{bindingCapabilityID, bindingOtherCapID, bindingWorkspaceID, bindingOtherSpaceID, bindingOwnerID}},
		{`INSERT INTO capability_releases(
		 id,workspace_id,capability_id,release_no,source_type,source_id,callable_name,
		 input_schema,output_schema,risk_level,side_effect_level,checksum,published_by)
		 VALUES($1,$3,$5,1,'TOOL_VERSION',$7,'binding_tool','{}','{}','LOW','READ',$8,$9),
		 ($2,$4,$6,1,'TOOL_VERSION',$7,'binding_other_tool','{}','{}','LOW','READ',$8,$9)`, []any{bindingReleaseID, bindingOtherRelID, bindingWorkspaceID, bindingOtherSpaceID, bindingCapabilityID, bindingOtherCapID, bindingSourceID, bindingChecksum, bindingOwnerID}},
		{`UPDATE capabilities SET active_release_id=CASE id WHEN $1::UUID THEN $3::UUID ELSE $4::UUID END WHERE id IN ($1,$2)`, []any{bindingCapabilityID, bindingOtherCapID, bindingReleaseID, bindingOtherRelID}},
	}
	for index, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("insert binding fixture %d: %v", index, err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO capability_providers(id,workspace_id,name,provider_kind,driver_key,transport,created_by,updated_by) VALUES
		($1,$3,'Binding Provider','HTTP_OPENAPI','http_openapi','HTTP',$5,$5),
		($2,$3,'Binding Provider Two','HTTP_OPENAPI','http_openapi','HTTP',$5,$5),
		($4,$6,'Binding Other Provider','HTTP_OPENAPI','http_openapi','HTTP',$5,$5)
	`, bindingProviderID, bindingProvider2ID, bindingWorkspaceID, bindingOtherSpaceID, bindingOwnerID, bindingOtherSpaceID); err != nil {
		t.Fatalf("insert binding providers: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO service_connections(id,workspace_id,provider_id,name,alias,environment,auth_mode,created_by,updated_by) VALUES
		($1,$4,$6,'Binding Connection','one','TEST','NONE',$8,$8),
		($2,$4,$7,'Binding Connection Two','two','TEST','NONE',$8,$8),
		($3,$5,$9,'Binding Other Connection','other','TEST','NONE',$8,$8)
	`, bindingConnectionID, bindingConnection2ID, bindingOtherConnID, bindingWorkspaceID, bindingOtherSpaceID,
		bindingProviderID, bindingProvider2ID, bindingOwnerID, bindingOtherSpaceID); err != nil {
		t.Fatalf("insert binding connections: %v", err)
	}
}

func assertBindingStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected binding statement to fail: %s", query)
	}
}

func assertBindingTableMissing(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('public.agent_capability_bindings') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("binding table survived rollback")
	}
}

func stringReference(value string) *string { return &value }

func TestBindRejectsNoneModelAndAllowsUnverified(t *testing.T) {
	repository, db := newBindingRepositoryTest(t)
	models, err := modelconfig.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := NewCatalog(repository, repository)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewBindingService(repository, ConnectionCompatibilityFunc(func(context.Context, string, string, string) error {
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	service = service.WithToolCompatibility(models, catalog, nil)

	if _, err := service.Bind(context.Background(), BindInput{
		WorkspaceID: bindingWorkspaceID, AgentID: bindingAgentID, CapabilityID: bindingCapabilityID,
		VersionPolicy: "FOLLOW_ACTIVE", Enabled: true, BoundBy: bindingOwnerID,
	}); err != nil {
		t.Fatalf("unverified bind should succeed: %v", err)
	}
	if err := service.Unbind(context.Background(), bindingWorkspaceID, bindingAgentID, bindingCapabilityID, 1); err != nil {
		t.Fatal(err)
	}

	plantBindingModelCalling(t, models, bindingWorkspaceID, bindingModelID, modelconfig.ToolCallingNone)
	if _, err := service.Bind(context.Background(), BindInput{
		WorkspaceID: bindingWorkspaceID, AgentID: bindingAgentID, CapabilityID: bindingCapabilityID,
		VersionPolicy: "FOLLOW_ACTIVE", Enabled: true, BoundBy: bindingOwnerID,
	}); !errors.Is(err, modelconfig.ErrAgentModelToolsUnsupported) {
		t.Fatalf("none bind: %v", err)
	}
}

func TestBindCarryAllTooLargeUsesProposedCatalogCount(t *testing.T) {
	repository, db := newBindingRepositoryTest(t)
	models, err := modelconfig.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	plantBindingModelCalling(t, models, bindingWorkspaceID, bindingModelID, modelconfig.ToolCallingFunctionCalling)
	plantBindingCarryAll(t, models, bindingWorkspaceID, bindingModelID)
	existing := make([]Descriptor, modelconfig.CarryAllHardLimit)
	for i := range existing {
		existing[i] = Descriptor{CapabilityID: "existing-" + strings.Repeat("0", 2)}
	}
	catalog := stubAgentCatalog{descriptors: existing}
	service, err := NewBindingService(repository, ConnectionCompatibilityFunc(func(context.Context, string, string, string) error {
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	service = service.WithToolCompatibility(models, catalog, nil)
	_, err = service.Bind(context.Background(), BindInput{
		WorkspaceID: bindingWorkspaceID, AgentID: bindingAgentID, CapabilityID: bindingCapabilityID,
		VersionPolicy: "FOLLOW_ACTIVE", Enabled: true, BoundBy: bindingOwnerID,
	})
	tooLarge, ok := modelconfig.AsCarryAllTooLarge(err)
	if !ok || tooLarge.Count != modelconfig.CarryAllHardLimit+1 {
		t.Fatalf("proposed carry-all: %+v err=%v", tooLarge, err)
	}
}

type stubAgentCatalog struct {
	descriptors []Descriptor
}

func (s stubAgentCatalog) ListForAgent(context.Context, string, string) ([]Descriptor, error) {
	return s.descriptors, nil
}

func plantBindingModelCalling(t *testing.T, models *modelconfig.Repository, workspaceID, configID, calling string) {
	t.Helper()
	ctx := context.Background()
	cfg, err := models.Get(ctx, workspaceID, configID)
	if err != nil {
		t.Fatal(err)
	}
	digest := modelconfig.WireConfigDigest(cfg)
	at := time.Now().UTC().Truncate(time.Second)
	var doc modelconfig.AgenticCapabilities
	if calling == modelconfig.ToolCallingNativeClientSearch {
		doc, err = modelconfig.CanonicalAgenticCapabilities(at, cfg.LockVersion, digest)
	} else {
		doc, err = modelconfig.CanonicalAgenticCapabilitiesV2(calling, at, cfg.LockVersion, digest)
	}
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	policy := json.RawMessage(`{}`)
	if calling == modelconfig.ToolCallingFunctionCalling {
		policy, err = modelconfig.CanonicalToolDisclosurePolicy(modelconfig.DisclosureModePlatformOnDemand)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := models.RecordVerification(ctx, modelconfig.VerificationUpdate{
		WorkspaceID: workspaceID, ConfigID: configID, Status: modelconfig.StatusVerified, LatencyMS: 1,
		AgenticCapabilities: raw, ToolDisclosurePolicy: policy, VerifiedAt: at,
		VerifiedBy: bindingOwnerID, ExpectedLockVersion: cfg.LockVersion,
	}); err != nil {
		t.Fatalf("plant %s: %v", calling, err)
	}
}

func plantBindingCarryAll(t *testing.T, models *modelconfig.Repository, workspaceID, configID string) {
	t.Helper()
	ctx := context.Background()
	cfg, err := models.Get(ctx, workspaceID, configID)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := modelconfig.CanonicalToolDisclosurePolicy(modelconfig.DisclosureModeCarryAll)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UpdateDisclosurePolicy(ctx, modelconfig.DisclosurePolicyUpdate{
		WorkspaceID: workspaceID, ConfigID: configID, Policy: policy,
		UpdatedBy: bindingOwnerID, ExpectedLockVersion: cfg.LockVersion,
	}); err != nil {
		t.Fatalf("plant carry_all: %v", err)
	}
}
