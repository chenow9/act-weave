package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

const (
	repositoryOwnerID          = "098f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	repositoryWorkspaceID      = "098f1f2e-7b5a-7c3d-8e9f-1234567890a2"
	repositoryOtherWorkspaceID = "098f1f2e-7b5a-7c3d-8e9f-1234567890a3"
	repositoryProviderID       = "098f1f2e-7b5a-7c3d-8e9f-1234567890a4"
	repositoryProviderTwoID    = "098f1f2e-7b5a-7c3d-8e9f-1234567890a5"
	repositoryOtherProviderID  = "098f1f2e-7b5a-7c3d-8e9f-1234567890a6"
	repositoryAssetID          = "098f1f2e-7b5a-7c3d-8e9f-1234567890a7"
	repositoryOtherAssetID     = "098f1f2e-7b5a-7c3d-8e9f-1234567890a8"
	repositoryConnectionID     = "098f1f2e-7b5a-7c3d-8e9f-1234567890a9"
	repositoryConnectionTwoID  = "098f1f2e-7b5a-7c3d-8e9f-1234567890aa"
	repositoryWrongProviderID  = "098f1f2e-7b5a-7c3d-8e9f-1234567890ab"
	repositoryOtherConnID      = "098f1f2e-7b5a-7c3d-8e9f-1234567890ac"
	repositoryToolID           = "098f1f2e-7b5a-7c3d-8e9f-1234567890ad"
	repositoryVersionOneID     = "098f1f2e-7b5a-7c3d-8e9f-1234567890ae"
	repositoryVersionTwoID     = "098f1f2e-7b5a-7c3d-8e9f-1234567890af"
	repositoryVersionThreeAID  = "098f1f2e-7b5a-7c3d-8e9f-1234567890b0"
	repositoryVersionThreeBID  = "098f1f2e-7b5a-7c3d-8e9f-1234567890b1"
	repositoryFailedToolID     = "098f1f2e-7b5a-7c3d-8e9f-1234567890b2"
	repositoryFailedVersionID  = "098f1f2e-7b5a-7c3d-8e9f-1234567890b3"
)

func TestCreateToolPersistsMetadataDraftAndScope(t *testing.T) {
	repository, db := newRepositoryTest(t)
	created, draft, err := repository.Create(context.Background(), validCreateInput())
	if err != nil {
		t.Fatalf("create tool: %v", err)
	}
	if created.CapabilityID != repositoryToolID || created.ProviderID != repositoryProviderID ||
		created.Name != "Orders Tool" || created.Status != "ACTIVE" || created.LockVersion != 1 {
		t.Fatalf("unexpected created tool: %+v", created)
	}
	if draft.ID != repositoryVersionOneID || draft.VersionNo != 1 ||
		draft.LifecycleStatus != "DRAFT" || draft.ExecutorType != "HTTP" ||
		draft.LockVersion != 1 || len(draft.Checksum) != 64 {
		t.Fatalf("unexpected initial draft: %+v", draft)
	}
	if _, err := repository.Get(context.Background(), repositoryOtherWorkspaceID, repositoryToolID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workspace tool miss, got %v", err)
	}
	if _, err := repository.GetVersion(context.Background(), repositoryOtherWorkspaceID, repositoryToolID, draft.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workspace version miss, got %v", err)
	}

	updated, err := repository.UpdateMetadata(context.Background(), repositoryWorkspaceID, repositoryToolID, MetadataUpdate{
		Name: "Orders API", Slug: "Orders-API", Description: "Updated metadata", Status: "DISABLED",
		SourceAssetID: pointer(repositoryAssetID), DefaultConnectionID: pointer(repositoryConnectionTwoID),
		UpdatedBy: repositoryOwnerID, ExpectedLockVersion: created.LockVersion,
	})
	if err != nil {
		t.Fatalf("update metadata: %v", err)
	}
	if updated.Name != "Orders API" || updated.Slug != "orders-api" || updated.Status != "DISABLED" ||
		updated.DefaultConnectionID == nil || *updated.DefaultConnectionID != repositoryConnectionTwoID || updated.LockVersion != 2 {
		t.Fatalf("unexpected updated metadata: %+v", updated)
	}
	if _, err := repository.UpdateMetadata(context.Background(), repositoryWorkspaceID, repositoryToolID, MetadataUpdate{
		Name: "Stale", Slug: "stale", Status: "ACTIVE", UpdatedBy: repositoryOwnerID, ExpectedLockVersion: 1,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale metadata conflict, got %v", err)
	}

	if err := repository.ValidateBindingConnection(context.Background(), repositoryWorkspaceID, repositoryToolID, repositoryConnectionID); err != nil {
		t.Fatalf("validate matching binding connection: %v", err)
	}
	for _, connectionID := range []string{repositoryWrongProviderID, repositoryOtherConnID} {
		if err := repository.ValidateBindingConnection(context.Background(), repositoryWorkspaceID, repositoryToolID, connectionID); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected incompatible connection %s, got %v", connectionID, err)
		}
	}

	invalid := validCreateInput()
	invalid.CapabilityID = repositoryFailedToolID
	invalid.InitialVersionID = repositoryFailedVersionID
	invalid.Name, invalid.Slug = "Invalid Tool", "invalid-tool"
	invalid.SourceAssetID = pointer(repositoryOtherAssetID)
	if _, _, err := repository.Create(context.Background(), invalid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected scoped source asset rejection, got %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM capabilities WHERE id=$1`, repositoryFailedToolID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed create left capability behind: count=%d", count)
	}

	invalid = validCreateInput()
	invalid.CapabilityID = repositoryFailedToolID
	invalid.InitialVersionID = repositoryFailedVersionID
	invalid.Name, invalid.Slug = "Secret Tool", "secret-tool"
	invalid.Draft.ActionConfig = json.RawMessage(`{"method":"GET","apiKeyValue":"plaintext"}`)
	if _, _, err := repository.Create(context.Background(), invalid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected sensitive action config rejection, got %v", err)
	}
}

func TestDraftVersionUpdateCopyAndPublishedImmutability(t *testing.T) {
	repository, db := newRepositoryTest(t)
	_, first, err := repository.Create(context.Background(), validCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	originalChecksum := first.Checksum
	updatedSpec := validDraftSpec()
	updatedSpec.ActionConfig = json.RawMessage(`{"method":"POST","path":"/orders"}`)
	updatedSpec.RiskLevel = "HIGH"
	updatedSpec.SideEffectLevel = "WRITE"
	updatedSpec.RequiresConfirmation = true
	first, err = repository.UpdateDraft(context.Background(), repositoryWorkspaceID, repositoryToolID, first.ID, DraftUpdate{
		Spec: updatedSpec, LifecycleStatus: "REVIEW", UpdatedBy: repositoryOwnerID, ExpectedLockVersion: first.LockVersion,
	})
	if err != nil {
		t.Fatalf("update first draft: %v", err)
	}
	if first.LockVersion != 2 || first.LifecycleStatus != "REVIEW" || first.Checksum == originalChecksum {
		t.Fatalf("draft update did not persist versioned snapshot: %+v", first)
	}
	if _, err := repository.UpdateDraft(context.Background(), repositoryWorkspaceID, repositoryToolID, first.ID, DraftUpdate{
		Spec: updatedSpec, LifecycleStatus: "REVIEW", UpdatedBy: repositoryOwnerID, ExpectedLockVersion: 1,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale draft conflict, got %v", err)
	}
	publishVersion(t, db, first.ID)
	first, err = repository.GetVersion(context.Background(), repositoryWorkspaceID, repositoryToolID, first.ID)
	if err != nil || first.LifecycleStatus != "PUBLISHED" || first.PublishedAt == nil || first.LockVersion != 3 {
		t.Fatalf("read published first version: %+v err=%v", first, err)
	}
	if _, err := repository.UpdateDraft(context.Background(), repositoryWorkspaceID, repositoryToolID, first.ID, DraftUpdate{
		Spec: updatedSpec, LifecycleStatus: "DRAFT", UpdatedBy: repositoryOwnerID, ExpectedLockVersion: first.LockVersion,
	}); !errors.Is(err, ErrImmutable) {
		t.Fatalf("expected published version immutability, got %v", err)
	}

	second, err := repository.CreateDraftFromPublished(context.Background(), repositoryWorkspaceID, repositoryToolID,
		first.ID, repositoryVersionTwoID, repositoryOwnerID)
	if err != nil {
		t.Fatalf("copy published version: %v", err)
	}
	if second.VersionNo != 2 || second.LifecycleStatus != "DRAFT" || second.LockVersion != 1 ||
		second.Checksum != first.Checksum || string(second.ActionConfig) != string(first.ActionConfig) {
		t.Fatalf("unexpected copied draft: %+v", second)
	}
	versions, err := repository.ListVersions(context.Background(), repositoryWorkspaceID, repositoryToolID)
	if err != nil || len(versions) != 2 || versions[0].VersionNo != 1 || versions[1].VersionNo != 2 {
		t.Fatalf("unexpected version list: %+v err=%v", versions, err)
	}
	if _, err := repository.CreateDraftFromPublished(context.Background(), repositoryWorkspaceID, repositoryToolID,
		first.ID, repositoryVersionThreeAID, repositoryOwnerID); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected single mutable draft conflict, got %v", err)
	}

	second, err = repository.UpdateDraft(context.Background(), repositoryWorkspaceID, repositoryToolID, second.ID, DraftUpdate{
		Spec: updatedSpec, LifecycleStatus: "REVIEW", UpdatedBy: repositoryOwnerID, ExpectedLockVersion: second.LockVersion,
	})
	if err != nil {
		t.Fatalf("update second draft: %v", err)
	}
	publishVersion(t, db, second.ID)

	ids := []string{repositoryVersionThreeAID, repositoryVersionThreeBID}
	results := make(chan error, len(ids))
	versionsCreated := make(chan Version, len(ids))
	var group sync.WaitGroup
	for _, id := range ids {
		id := id
		group.Add(1)
		go func() {
			defer group.Done()
			value, err := repository.CreateDraftFromPublished(context.Background(), repositoryWorkspaceID,
				repositoryToolID, second.ID, id, repositoryOwnerID)
			if err == nil {
				versionsCreated <- value
			}
			results <- err
		}()
	}
	group.Wait()
	close(results)
	close(versionsCreated)
	var success, conflicts int
	for result := range results {
		switch {
		case result == nil:
			success++
		case errors.Is(result, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent copy result: %v", result)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("expected one monotonic draft allocation and one conflict, got success=%d conflict=%d", success, conflicts)
	}
	createdThird := <-versionsCreated
	if createdThird.VersionNo != 3 || createdThird.LifecycleStatus != "DRAFT" {
		t.Fatalf("unexpected concurrently allocated draft: %+v", createdThird)
	}
}

func dualModeDriverConfig() string {
	return `{
		"outboundIdentity":{
			"schemaVersion":"outbound-identity.v1",
			"supportedModes":["REQUEST_PASSTHROUGH"],
			"supportedSubjectTypes":["USER"],
			"requestPassthrough":{
				"credentialTypes":["ACCESS_TOKEN"],
				"businessInjection":{"headerName":"Authorization","prefix":"Bearer"}
			}
		}
	}`
}

func dualModeConnectionIdentity() string {
	return `{"schemaVersion":"outbound-connection.v1","mode":"REQUEST_PASSTHROUGH","requestPassthrough":{"maxResidenceSeconds":600}}`
}

func newRepositoryTest(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateTo(t, 13)
	if !version.Applied || version.Number != 13 || version.Dirty {
		t.Fatalf("unexpected migration: %+v", version)
	}
	db := testDatabase.Open(t)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,username,display_name) VALUES($1,'tool.repository.owner','Tool Repository Owner')`, []any{repositoryOwnerID}},
		{`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		 ($1,'tool-repository','Tool Repository','PRODUCTION',$3,$3,$3),
		 ($2,'tool-repository-other','Tool Repository Other','SANDBOX',$3,$3,$3)`, []any{repositoryWorkspaceID, repositoryOtherWorkspaceID, repositoryOwnerID}},
		{`INSERT INTO capability_providers(id,workspace_id,name,provider_kind,driver_key,transport,created_by,updated_by) VALUES
		 ($1,$4,'Repository Provider','HTTP_OPENAPI','http_openapi','HTTP',$6,$6),
		 ($2,$4,'Repository Provider Two','HTTP_OPENAPI','http_openapi','HTTP',$6,$6),
		 ($3,$5,'Repository Other Provider','HTTP_OPENAPI','http_openapi','HTTP',$6,$6)`, []any{repositoryProviderID, repositoryProviderTwoID, repositoryOtherProviderID, repositoryWorkspaceID, repositoryOtherWorkspaceID, repositoryOwnerID}},
		{`INSERT INTO provider_assets(id,workspace_id,provider_id,asset_kind,external_id,name,source_checksum) VALUES
		 ($1,$3,$4,'TOOL','orders.get','Get Order','sha256:orders'),
		 ($2,$5,$6,'TOOL','other.get','Other Get','sha256:other')`, []any{repositoryAssetID, repositoryOtherAssetID, repositoryWorkspaceID, repositoryProviderID, repositoryOtherWorkspaceID, repositoryOtherProviderID}},
		{`INSERT INTO service_connections(id,workspace_id,provider_id,name,alias,environment,auth_mode,created_by,updated_by) VALUES
		 ($1,$5,$6,'Repository Connection','primary','TEST','NONE',$9,$9),
		 ($2,$5,$6,'Repository Connection Two','secondary','TEST','NONE',$9,$9),
		 ($3,$5,$7,'Wrong Provider Connection','wrong-provider','TEST','NONE',$9,$9),
		 ($4,$8,$10,'Other Connection','other','TEST','NONE',$9,$9)`, []any{repositoryConnectionID, repositoryConnectionTwoID, repositoryWrongProviderID, repositoryOtherConnID, repositoryWorkspaceID, repositoryProviderID, repositoryProviderTwoID, repositoryOtherWorkspaceID, repositoryOwnerID, repositoryOtherProviderID}},
	}
	for index, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("insert repository fixture %d: %v", index, err)
		}
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db
}

// newDualModeRepositoryTest migrates to latest so dual-mode outbound identity
// columns exist. Used by invocation resolution tests only.
func newDualModeRepositoryTest(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 60 || version.Dirty {
		t.Fatalf("unexpected migration: %+v", version)
	}
	db := testDatabase.Open(t)
	driver := dualModeDriverConfig()
	identity := dualModeConnectionIdentity()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,username,display_name) VALUES($1,'tool.repository.owner','Tool Repository Owner')`, []any{repositoryOwnerID}},
		{`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		 ($1,'tool-repository','Tool Repository','PRODUCTION',$3,$3,$3),
		 ($2,'tool-repository-other','Tool Repository Other','SANDBOX',$3,$3,$3)`, []any{repositoryWorkspaceID, repositoryOtherWorkspaceID, repositoryOwnerID}},
		{`INSERT INTO capability_providers(id,workspace_id,name,provider_kind,driver_key,transport,endpoint_config,driver_config,created_by,updated_by) VALUES
		 ($1,$4,'Repository Provider','HTTP_OPENAPI','http_openapi','HTTP','{"schemaVersion":2,"serviceBaseUrl":"https://api.example","sourceUri":"https://api.example/openapi.json"}',$6,$7,$7),
		 ($2,$4,'Repository Provider Two','HTTP_OPENAPI','http_openapi','HTTP','{"schemaVersion":2,"serviceBaseUrl":"https://api2.example","sourceUri":"https://api2.example/openapi.json"}',$6,$7,$7),
		 ($3,$5,'Repository Other Provider','HTTP_OPENAPI','http_openapi','HTTP','{"schemaVersion":2,"serviceBaseUrl":"https://other.example","sourceUri":"https://other.example/openapi.json"}',$6,$7,$7)`,
			[]any{repositoryProviderID, repositoryProviderTwoID, repositoryOtherProviderID, repositoryWorkspaceID, repositoryOtherWorkspaceID, driver, repositoryOwnerID}},
		{`INSERT INTO provider_assets(id,workspace_id,provider_id,asset_kind,external_id,name,source_checksum) VALUES
		 ($1,$3,$4,'TOOL','orders.get','Get Order','sha256:orders'),
		 ($2,$5,$6,'TOOL','other.get','Other Get','sha256:other')`, []any{repositoryAssetID, repositoryOtherAssetID, repositoryWorkspaceID, repositoryProviderID, repositoryOtherWorkspaceID, repositoryOtherProviderID}},
		{`INSERT INTO service_connections(
			id,workspace_id,provider_id,name,alias,environment,auth_mode,auth_config,
			status,outbound_identity,outbound_identity_policy_version,migration_state,created_by,updated_by
		) VALUES
		 ($1,$5,$6,'Repository Connection','primary','TEST','OUTBOUND_IDENTITY','{}','VERIFIED',$11::jsonb,1,'NONE',$9,$9),
		 ($2,$5,$6,'Repository Connection Two','secondary','TEST','OUTBOUND_IDENTITY','{}','VERIFIED',$11::jsonb,1,'NONE',$9,$9),
		 ($3,$5,$7,'Wrong Provider Connection','wrong-provider','TEST','OUTBOUND_IDENTITY','{}','VERIFIED',$11::jsonb,1,'NONE',$9,$9),
		 ($4,$8,$10,'Other Connection','other','TEST','OUTBOUND_IDENTITY','{}','VERIFIED',$11::jsonb,1,'NONE',$9,$9)`,
			[]any{repositoryConnectionID, repositoryConnectionTwoID, repositoryWrongProviderID, repositoryOtherConnID, repositoryWorkspaceID, repositoryProviderID, repositoryProviderTwoID, repositoryOtherWorkspaceID, repositoryOwnerID, repositoryOtherProviderID, identity}},
	}
	for index, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("insert dual-mode repository fixture %d: %v", index, err)
		}
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db
}

func validCreateInput() CreateInput {
	return CreateInput{
		CapabilityID: repositoryToolID, InitialVersionID: repositoryVersionOneID,
		WorkspaceID: repositoryWorkspaceID, ProviderID: repositoryProviderID,
		SourceAssetID: pointer(repositoryAssetID), DefaultConnectionID: pointer(repositoryConnectionID),
		Name: "Orders Tool", Slug: "orders-tool", Description: "Order operations",
		Draft: validDraftSpec(), CreatedBy: repositoryOwnerID,
	}
}

func validDraftSpec() DraftSpec {
	return DraftSpec{
		ProviderAssetID: pointer(repositoryAssetID), DefaultConnectionID: pointer(repositoryConnectionID),
		ActionSchemaVersion: "http.v1",
		ActionConfig:        json.RawMessage(`{"method":"GET","path":"/orders/{orderId}"}`),
		InputSchema:         json.RawMessage(`{"type":"object","required":["orderId"]}`),
		OutputSchema:        json.RawMessage(`{"type":"object"}`), ErrorMappings: json.RawMessage(`{}`),
		RuntimePolicy: json.RawMessage(`{"timeoutMs":5000}`), RiskLevel: "LOW", SideEffectLevel: "READ",
	}
}

func publishVersion(t *testing.T, db *sql.DB, versionID string) {
	t.Helper()
	if _, err := db.Exec(`
		UPDATE tool_versions SET lifecycle_status='PUBLISHED',published_at=clock_timestamp(),
			updated_by=$2,updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE id=$1
	`, versionID, repositoryOwnerID); err != nil {
		t.Fatalf("publish tool version fixture: %v", err)
	}
}

func pointer(value string) *string { return &value }
