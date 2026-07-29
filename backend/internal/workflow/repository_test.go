package workflow

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
	draftOwnerID          = "108f1f2e-7b5a-7c3d-8e9f-123456789001"
	draftWorkspaceID      = "108f1f2e-7b5a-7c3d-8e9f-123456789002"
	draftOtherWorkspaceID = "108f1f2e-7b5a-7c3d-8e9f-123456789003"
	draftCapabilityID     = "108f1f2e-7b5a-7c3d-8e9f-123456789004"
	draftID               = "108f1f2e-7b5a-7c3d-8e9f-123456789005"
	draftFailedCapID      = "108f1f2e-7b5a-7c3d-8e9f-123456789006"
	draftFailedID         = "108f1f2e-7b5a-7c3d-8e9f-123456789007"
)

func TestDraftCRUDUsesCanonicalHashAndWorkspaceScope(t *testing.T) {
	repository, db := newDraftRepositoryTest(t)
	created, initial, err := repository.Create(context.Background(), validWorkflowCreateInput())
	if err != nil {
		t.Fatalf("create workflow and draft: %v", err)
	}
	if created.CapabilityID != draftCapabilityID || created.CurrentDraftID != draftID ||
		created.ActiveRevisionID != nil || created.LatestCompilationID != nil ||
		created.Status != "ACTIVE" || created.LockVersion != 1 ||
		created.NodeCount != 0 || created.EdgeCount != 0 {
		t.Fatalf("unexpected created workflow: %+v", created)
	}
	if initial.DraftVersion != 1 || initial.LockVersion != 1 || initial.SchemaVersion != "workflow.v1" ||
		string(initial.Graph) != `{"edges":[],"nodes":[]}` || len(initial.GraphHash) != 64 {
		t.Fatalf("unexpected initial draft: %+v", initial)
	}
	canonical, canonicalHash, err := canonicalGraph(json.RawMessage(`{"edges":[],"nodes":[]}`))
	if err != nil || string(canonical) != string(initial.Graph) || canonicalHash != initial.GraphHash {
		t.Fatalf("graph hash is not reproducible: graph=%s hash=%s err=%v", canonical, canonicalHash, err)
	}
	if _, err := repository.Get(context.Background(), draftOtherWorkspaceID, draftCapabilityID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workspace workflow miss, got %v", err)
	}
	if _, err := repository.GetDraft(context.Background(), draftOtherWorkspaceID, draftCapabilityID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workspace draft miss, got %v", err)
	}
	listed, err := repository.List(context.Background(), draftWorkspaceID)
	if err != nil || len(listed) != 1 || listed[0].CapabilityID != draftCapabilityID ||
		listed[0].NodeCount != 0 || listed[0].EdgeCount != 0 {
		t.Fatalf("list workflows: %+v err=%v", listed, err)
	}

	updatedWorkflow, err := repository.UpdateMetadata(context.Background(), draftWorkspaceID, draftCapabilityID, MetadataUpdate{
		Name: "Updated Workflow", Slug: "Updated-Workflow", Description: "updated",
		Status: "DISABLED", UpdatedBy: draftOwnerID, ExpectedLockVersion: created.LockVersion,
	})
	if err != nil || updatedWorkflow.Name != "Updated Workflow" || updatedWorkflow.Slug != "updated-workflow" ||
		updatedWorkflow.Status != "DISABLED" || updatedWorkflow.LockVersion != 2 {
		t.Fatalf("update workflow metadata: %+v err=%v", updatedWorkflow, err)
	}
	if _, err := repository.UpdateMetadata(context.Background(), draftWorkspaceID, draftCapabilityID, MetadataUpdate{
		Name: "Stale", Slug: "stale", Status: "ACTIVE", UpdatedBy: draftOwnerID, ExpectedLockVersion: 1,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale workflow metadata conflict, got %v", err)
	}

	updatedDraft, err := repository.UpdateDraft(context.Background(), draftWorkspaceID, draftCapabilityID, DraftUpdate{
		SchemaVersion: "workflow.v1", Graph: json.RawMessage(`{"nodes":[],"edges":[]}`),
		UpdatedBy: draftOwnerID, ExpectedDraftVersion: initial.DraftVersion,
		ExpectedLockVersion: initial.LockVersion,
	})
	if err != nil || updatedDraft.DraftVersion != 2 || updatedDraft.LockVersion != 2 ||
		updatedDraft.GraphHash != initial.GraphHash {
		t.Fatalf("update canonical workflow draft: %+v err=%v", updatedDraft, err)
	}
	if _, err := repository.UpdateDraft(context.Background(), draftWorkspaceID, draftCapabilityID, DraftUpdate{
		SchemaVersion: "workflow.v1", Graph: json.RawMessage(`{"nodes":[{"id":"stale"}],"edges":[]}`),
		UpdatedBy: draftOwnerID, ExpectedDraftVersion: 1, ExpectedLockVersion: 1,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale draft conflict, got %v", err)
	}
	storedDraft, err := repository.GetDraft(context.Background(), draftWorkspaceID, draftCapabilityID)
	if err != nil || storedDraft.DraftVersion != 2 || storedDraft.GraphHash != initial.GraphHash {
		t.Fatalf("stale save overwrote workflow draft: %+v err=%v", storedDraft, err)
	}
	derivedDraft, err := repository.UpdateDraft(context.Background(), draftWorkspaceID, draftCapabilityID, DraftUpdate{
		SchemaVersion: "workflow.v1",
		Graph:         json.RawMessage(`{"nodes":[{"id":"start"},{"id":"end"}],"edges":[{"id":"start-end"}]}`),
		UpdatedBy:     draftOwnerID, ExpectedDraftVersion: storedDraft.DraftVersion,
		ExpectedLockVersion: storedDraft.LockVersion,
	})
	if err != nil || derivedDraft.DraftVersion != 3 {
		t.Fatalf("update graph for derived counts: %+v err=%v", derivedDraft, err)
	}
	counted, err := repository.Get(context.Background(), draftWorkspaceID, draftCapabilityID)
	if err != nil || counted.NodeCount != 2 || counted.EdgeCount != 1 {
		t.Fatalf("derive current graph counts: %+v err=%v", counted, err)
	}

	invalid := validWorkflowCreateInput()
	invalid.CapabilityID, invalid.DraftID = draftFailedCapID, draftFailedID
	invalid.Name, invalid.Slug, invalid.Graph = "Invalid Workflow", "invalid-workflow", json.RawMessage(`[]`)
	if _, _, err := repository.Create(context.Background(), invalid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid graph rejection, got %v", err)
	}
	var failedCapabilityCount int
	if err := db.QueryRow(`SELECT count(*) FROM capabilities WHERE id=$1`, draftFailedCapID).Scan(&failedCapabilityCount); err != nil {
		t.Fatal(err)
	}
	if failedCapabilityCount != 0 {
		t.Fatalf("invalid create left capability behind: %d", failedCapabilityCount)
	}

	if err := repository.SoftDelete(context.Background(), draftWorkspaceID, draftCapabilityID,
		draftOwnerID, updatedWorkflow.LockVersion); err != nil {
		t.Fatalf("soft delete workflow: %v", err)
	}
	if _, err := repository.Get(context.Background(), draftWorkspaceID, draftCapabilityID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted workflow hidden, got %v", err)
	}
	if _, err := repository.GetDraft(context.Background(), draftWorkspaceID, draftCapabilityID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted workflow draft hidden, got %v", err)
	}
}

func TestDraftConflictConcurrentSavesDoNotOverwrite(t *testing.T) {
	repository, _ := newDraftRepositoryTest(t)
	_, initial, err := repository.Create(context.Background(), validWorkflowCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	graphs := []json.RawMessage{
		json.RawMessage(`{"nodes":[{"id":"first"}],"edges":[]}`),
		json.RawMessage(`{"nodes":[{"id":"second"}],"edges":[]}`),
	}
	results := make(chan error, len(graphs))
	drafts := make(chan Draft, len(graphs))
	var group sync.WaitGroup
	for _, graph := range graphs {
		graph := append(json.RawMessage(nil), graph...)
		group.Add(1)
		go func() {
			defer group.Done()
			value, err := repository.UpdateDraft(context.Background(), draftWorkspaceID, draftCapabilityID, DraftUpdate{
				SchemaVersion: "workflow.v1", Graph: graph, UpdatedBy: draftOwnerID,
				ExpectedDraftVersion: initial.DraftVersion, ExpectedLockVersion: initial.LockVersion,
			})
			if err == nil {
				drafts <- value
			}
			results <- err
		}()
	}
	group.Wait()
	close(results)
	close(drafts)
	success, conflicts := 0, 0
	for result := range results {
		switch {
		case result == nil:
			success++
		case errors.Is(result, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent save result: %v", result)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("expected one save and one conflict, got success=%d conflicts=%d", success, conflicts)
	}
	winner := <-drafts
	stored, err := repository.GetDraft(context.Background(), draftWorkspaceID, draftCapabilityID)
	if err != nil || stored.DraftVersion != 2 || stored.LockVersion != 2 ||
		stored.GraphHash != winner.GraphHash || string(stored.Graph) != string(winner.Graph) {
		t.Fatalf("concurrent save did not preserve winner: winner=%+v stored=%+v err=%v", winner, stored, err)
	}
}

func newDraftRepositoryTest(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("unexpected migration: %+v", version)
	}
	db := testDatabase.Open(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'draft.owner','Draft Owner')`, draftOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'draft-space','Draft Space','PRODUCTION',$3,$3,$3),
		($2,'draft-other','Draft Other','SANDBOX',$3,$3,$3)
	`, draftWorkspaceID, draftOtherWorkspaceID, draftOwnerID); err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db
}

func validWorkflowCreateInput() CreateInput {
	return CreateInput{
		CapabilityID: draftCapabilityID, DraftID: draftID, WorkspaceID: draftWorkspaceID,
		Name: "Order Workflow", Slug: "Order-Workflow", Description: "Order flow",
		SchemaVersion: "workflow.v1", Graph: json.RawMessage(`{"nodes":[],"edges":[]}`),
		CreatedBy: draftOwnerID,
	}
}
