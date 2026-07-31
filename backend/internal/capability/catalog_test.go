package capability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

const (
	catalogOwnerID      = "068f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	catalogWorkspaceID  = "068f1f2e-7b5a-7c3d-8e9f-1234567890a2"
	catalogOtherSpaceID = "068f1f2e-7b5a-7c3d-8e9f-1234567890a3"
	catalogAgentID      = "068f1f2e-7b5a-7c3d-8e9f-1234567890a4"
	catalogCapabilityID = "068f1f2e-7b5a-7c3d-8e9f-1234567890a5"
	catalogDisabledID   = "068f1f2e-7b5a-7c3d-8e9f-1234567890a6"
	catalogRetiredID    = "068f1f2e-7b5a-7c3d-8e9f-1234567890a7"
	catalogConflictID   = "068f1f2e-7b5a-7c3d-8e9f-1234567890a8"
	catalogRelease1ID   = "068f1f2e-7b5a-7c3d-8e9f-1234567890a9"
	catalogRelease2ID   = "068f1f2e-7b5a-7c3d-8e9f-1234567890aa"
	catalogDisabledRel  = "068f1f2e-7b5a-7c3d-8e9f-1234567890ab"
	catalogRetiredRel1  = "068f1f2e-7b5a-7c3d-8e9f-1234567890ac"
	catalogRetiredRel2  = "068f1f2e-7b5a-7c3d-8e9f-1234567890ad"
	catalogConflictRel  = "068f1f2e-7b5a-7c3d-8e9f-1234567890ae"
	catalogSourceID     = "068f1f2e-7b5a-7c3d-8e9f-1234567890af"
	catalogChecksum     = "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"
)

func TestResolveActiveAndPinnedReleaseSnapshots(t *testing.T) {
	repository, _ := newCatalogRepositoryTest(t)
	createCatalogCapability(t, repository, catalogCapabilityID, "TOOL", "Orders", "orders")
	inputSchema := json.RawMessage(`{"type":"object","properties":{"orderId":{"type":"string"}}}`)
	_, first, err := repository.Publish(context.Background(), releaseInput(
		catalogRelease1ID, catalogCapabilityID, "get_order", "Get an order", inputSchema,
	))
	if err != nil {
		t.Fatalf("publish release 1: %v", err)
	}
	firstResolved, err := repository.Resolve(context.Background(), catalogWorkspaceID, catalogCapabilityID, "")
	if err != nil || firstResolved.ReleaseID != first.ID || firstResolved.CallableDescription != "Get an order" {
		t.Fatalf("resolve first active release: %+v err=%v", firstResolved, err)
	}
	inputSchema[0] = '['
	_, second, err := repository.Publish(context.Background(), releaseInput(
		catalogRelease2ID, catalogCapabilityID, "get_order_v2", "Get an order v2",
		json.RawMessage(`{"type":"object","required":["orderId"]}`),
	))
	if err != nil {
		t.Fatalf("publish release 2: %v", err)
	}
	active, err := repository.Resolve(context.Background(), catalogWorkspaceID, catalogCapabilityID, "")
	if err != nil || active.ReleaseID != second.ID || active.ReleaseNo != 2 {
		t.Fatalf("resolve second active release: %+v err=%v", active, err)
	}
	pinned, err := repository.Resolve(context.Background(), catalogWorkspaceID, catalogCapabilityID, first.ID)
	if err != nil || pinned.ReleaseID != first.ID || pinned.CallableName != "get_order" || pinned.CallableDescription != "Get an order" {
		t.Fatalf("resolve pinned release after active switch: %+v err=%v", pinned, err)
	}
	if string(pinned.InputSchema) != `{"type": "object", "properties": {"orderId": {"type": "string"}}}` {
		t.Fatalf("published input schema changed with caller buffer: %s", pinned.InputSchema)
	}
	if _, err := repository.Resolve(context.Background(), catalogOtherSpaceID, catalogCapabilityID, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workspace resolve miss, got %v", err)
	}
}

func TestCatalogFiltersUnavailableAndUsesBindingVersionPolicy(t *testing.T) {
	repository, db := newCatalogRepositoryTest(t)
	createCatalogCapability(t, repository, catalogCapabilityID, "TOOL", "Orders", "orders")
	_, activeRelease, err := repository.Publish(context.Background(), releaseInput(
		catalogRelease1ID, catalogCapabilityID, "get_order", "Get order", json.RawMessage(`{"type":"object"}`),
	))
	if err != nil {
		t.Fatal(err)
	}
	createCatalogCapability(t, repository, catalogDisabledID, "WORKFLOW", "Disabled", "disabled")
	_, disabledRelease, err := repository.Publish(context.Background(), releaseInput(
		catalogDisabledRel, catalogDisabledID, "disabled_flow", "Disabled flow", json.RawMessage(`{"type":"object"}`),
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE capabilities SET status='DISABLED' WHERE id=$1`, catalogDisabledID); err != nil {
		t.Fatal(err)
	}
	createCatalogCapability(t, repository, catalogRetiredID, "TOOL", "Retired", "retired")
	_, retiredRelease, err := repository.Publish(context.Background(), releaseInput(
		catalogRetiredRel1, catalogRetiredID, "retired_tool", "Retired", json.RawMessage(`{"type":"object"}`),
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.Publish(context.Background(), releaseInput(
		catalogRetiredRel2, catalogRetiredID, "retired_tool_v2", "Replacement", json.RawMessage(`{"type":"object"}`),
	)); err != nil {
		t.Fatal(err)
	}
	if err := repository.Retire(context.Background(), catalogWorkspaceID, catalogRetiredID, retiredRelease.ID); err != nil {
		t.Fatal(err)
	}
	bindings := &staticBindings{selections: []BindingSelection{
		{CapabilityID: catalogCapabilityID, VersionPolicy: "FOLLOW_ACTIVE"},
		{CapabilityID: catalogDisabledID, VersionPolicy: "PINNED", PinnedReleaseID: &disabledRelease.ID},
		{CapabilityID: catalogRetiredID, VersionPolicy: "PINNED", PinnedReleaseID: &retiredRelease.ID},
	}}
	catalog, err := NewCatalog(repository, bindings)
	if err != nil {
		t.Fatal(err)
	}
	descriptors, err := catalog.ListForAgent(context.Background(), catalogWorkspaceID, catalogAgentID)
	if err != nil {
		t.Fatalf("list agent catalog: %v", err)
	}
	if len(descriptors) != 1 || descriptors[0].ReleaseID != activeRelease.ID || descriptors[0].Kind != "TOOL" {
		t.Fatalf("unexpected filtered descriptors: %+v", descriptors)
	}
	bindings.selections = []BindingSelection{{
		CapabilityID: catalogCapabilityID, VersionPolicy: "PINNED", PinnedReleaseID: &activeRelease.ID,
	}}
	descriptors, err = catalog.ListForAgent(context.Background(), catalogWorkspaceID, catalogAgentID)
	if err != nil || len(descriptors) != 1 || descriptors[0].ReleaseID != activeRelease.ID {
		t.Fatalf("pinned catalog selection failed: %+v err=%v", descriptors, err)
	}
}

func TestCatalogPublishRejectsCallableNameConflict(t *testing.T) {
	repository, db := newCatalogRepositoryTest(t)
	createCatalogCapability(t, repository, catalogCapabilityID, "TOOL", "Orders", "orders")
	if _, _, err := repository.Publish(context.Background(), releaseInput(
		catalogRelease1ID, catalogCapabilityID, "get_order", "Get order", json.RawMessage(`{"type":"object"}`),
	)); err != nil {
		t.Fatal(err)
	}
	createCatalogCapability(t, repository, catalogConflictID, "WORKFLOW", "Conflict", "conflict")
	if _, _, err := repository.Publish(context.Background(), releaseInput(
		catalogConflictRel, catalogConflictID, "get_order", "Conflict", json.RawMessage(`{"type":"object"}`),
	)); !errors.Is(err, ErrCallableConflict) {
		t.Fatalf("expected callable conflict, got %v", err)
	}
	var releaseCount int
	if err := db.QueryRow(`SELECT count(*) FROM capability_releases WHERE id=$1`, catalogConflictRel).Scan(&releaseCount); err != nil {
		t.Fatal(err)
	}
	if releaseCount != 0 {
		t.Fatalf("conflicting release insert did not roll back: count=%d", releaseCount)
	}
}

func newCatalogRepositoryTest(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("unexpected migration: %+v", version)
	}
	db := testDatabase.Open(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'catalog.owner','Catalog Owner')`, catalogOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'catalog-workspace','Catalog Workspace','PRODUCTION',$3,$3,$3),
		($2,'catalog-other','Catalog Other','SANDBOX',$3,$3,$3)
	`, catalogWorkspaceID, catalogOtherSpaceID, catalogOwnerID); err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db
}

func createCatalogCapability(t *testing.T, repository *Repository, id, kind, name, slug string) Capability {
	t.Helper()
	value, err := repository.Create(context.Background(), NewCapability{
		ID: id, WorkspaceID: catalogWorkspaceID, Kind: kind, Name: name, Slug: slug, CreatedBy: catalogOwnerID,
	})
	if err != nil {
		t.Fatalf("create catalog capability: %v", err)
	}
	return value
}

func releaseInput(id, capabilityID, callable, description string, inputSchema json.RawMessage) PublishRelease {
	return PublishRelease{
		ID: id, WorkspaceID: catalogWorkspaceID, CapabilityID: capabilityID,
		SourceType: "TOOL_VERSION", SourceID: catalogSourceID,
		CallableName: callable, CallableDescription: description,
		InputSchema: inputSchema, OutputSchema: json.RawMessage(`{"type":"object"}`),
		RiskLevel: "LOW", SideEffectLevel: "READ", Checksum: catalogChecksum,
		PublishedBy: catalogOwnerID,
	}
}

type staticBindings struct{ selections []BindingSelection }

func (b *staticBindings) ListEnabledSelections(context.Context, string, string) ([]BindingSelection, error) {
	return append([]BindingSelection(nil), b.selections...), nil
}
