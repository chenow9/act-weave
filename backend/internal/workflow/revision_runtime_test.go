package workflow

import (
	"context"
	"errors"
	"testing"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflowruntime"
)

func TestRevisionRuntimeResolvesPinnedReleaseWithoutReadingDraftOrActivePointer(t *testing.T) {
	repository, _, first, second, _ := prepareTwoPublishedWorkflowRevisions(t)
	snapshot, err := repository.ResolveRevisionSnapshot(
		context.Background(), draftWorkspaceID, draftCapabilityID, first.Release.ID,
	)
	if err != nil {
		t.Fatalf("resolve older pinned workflow release: %v", err)
	}
	if snapshot.ReleaseID != first.Release.ID || snapshot.RevisionID != first.Revision.ID ||
		snapshot.PlanHash != first.Revision.PlanHash || snapshot.ReleaseID == second.Release.ID {
		t.Fatalf("runtime did not resolve explicit pinned release: %+v", snapshot)
	}
	currentDraft, err := repository.GetDraft(context.Background(), draftWorkspaceID, draftCapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpdateDraft(context.Background(), draftWorkspaceID, draftCapabilityID, DraftUpdate{
		SchemaVersion: "workflow.graph.v1", Graph: validCompilationGraph(true),
		UpdatedBy: workflowPublishEditorID, ExpectedDraftVersion: currentDraft.DraftVersion,
		ExpectedLockVersion: currentDraft.LockVersion,
	}); err != nil {
		t.Fatal(err)
	}
	afterDraftChange, err := repository.ResolveRevisionSnapshot(
		context.Background(), draftWorkspaceID, draftCapabilityID, first.Release.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if afterDraftChange.RevisionID != snapshot.RevisionID ||
		afterDraftChange.PlanHash != snapshot.PlanHash ||
		len(afterDraftChange.Plan.Nodes) != len(snapshot.Plan.Nodes) {
		t.Fatalf("draft update changed immutable runtime snapshot: before=%+v after=%+v", snapshot, afterDraftChange)
	}
	runner, err := workflowruntime.NewPublishedRevisionRunner(
		repository, workflowruntime.NewWrappedPlanRunner(nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), workflowruntime.PublishedRunRequest{
		WorkspaceID: draftWorkspaceID, CapabilityID: draftCapabilityID,
		ReleaseID: first.Release.ID, ActorID: workflowPublishEditorID,
		Input: map[string]any{"orderId": "pinned-order"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot.RevisionID != first.Revision.ID ||
		result.Execution.WorkflowVersion != first.Revision.ID ||
		result.Execution.Status != domain.ExecutionSuccess {
		t.Fatalf("unexpected pinned revision execution: %+v", result)
	}
	if _, err := repository.ResolveRevisionSnapshot(
		context.Background(), draftOtherWorkspaceID, draftCapabilityID, first.Release.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workspace revision snapshot miss, got %v", err)
	}
}
