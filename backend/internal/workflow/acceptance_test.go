package workflow

import (
	"context"
	"errors"
	"testing"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflowruntime"
)

func TestWorkflowAcceptanceFullLifecycleRollbackSnapshotAndIsolation(t *testing.T) {
	repository, db, first, second, _ := prepareTwoPublishedWorkflowRevisions(t)
	if first.Revision.RevisionNo != 1 || first.Release.ReleaseNo != 1 ||
		second.Revision.RevisionNo != 2 || second.Release.ReleaseNo != 2 {
		t.Fatalf("unexpected monotonic workflow versions: first=%+v second=%+v", first, second)
	}
	secondSnapshotBeforeRollback, err := repository.ResolveRevisionSnapshot(
		context.Background(), draftWorkspaceID, draftCapabilityID, second.Release.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	activationEvents := newWorkflowActivationEventWriter(t, db)
	activationService := newWorkflowActivationService(t, repository, db, activationEvents)
	rollback, err := activationService.Activate(context.Background(), ActivateRevisionInput{
		EventID: workflowRollbackEventID, WorkspaceID: draftWorkspaceID,
		CapabilityID: draftCapabilityID, RevisionID: first.Revision.ID,
		ActivatedBy: workflowPublishEditorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rollback.Event.Type != "workflow.release.rolled_back" {
		t.Fatalf("expected explicit rollback event, got %+v", rollback.Event)
	}
	assertWorkflowActivePointers(t, db, first.Revision.ID, first.Release.ID)

	publishedRunner, err := workflowruntime.NewPublishedRevisionRunner(
		repository, workflowruntime.NewWrappedPlanRunner(nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := publishedRunner.Run(context.Background(), workflowruntime.PublishedRunRequest{
		WorkspaceID: draftWorkspaceID, CapabilityID: draftCapabilityID,
		ReleaseID: second.Release.ID, ActorID: workflowPublishEditorID,
		Input: map[string]any{"orderId": "snapshot-order"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Execution.Status != domain.ExecutionSuccess ||
		run.Execution.WorkflowVersion != second.Revision.ID ||
		run.Snapshot.ReleaseID != second.Release.ID ||
		run.Snapshot.PlanHash != secondSnapshotBeforeRollback.PlanHash {
		t.Fatalf("rollback changed explicit historical release execution: %+v", run)
	}
	if len(run.Snapshot.Plan.Nodes) != len(secondSnapshotBeforeRollback.Plan.Nodes) {
		t.Fatalf("historical plan shape drifted after rollback: before=%+v after=%+v",
			secondSnapshotBeforeRollback.Plan, run.Snapshot.Plan)
	}
	readinessService, err := NewReadinessService(repository)
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := readinessService.Get(context.Background(), draftWorkspaceID, draftCapabilityID)
	if err != nil || readiness.Stage != ReadinessPublishReady || !readiness.Published || !readiness.CanPublish {
		t.Fatalf("unexpected post-rollback readiness: %+v err=%v", readiness, err)
	}
	if _, err := repository.Get(context.Background(), draftOtherWorkspaceID, draftCapabilityID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace workflow was visible: %v", err)
	}
	if _, err := repository.GetCurrentValidCompilation(
		context.Background(), draftOtherWorkspaceID, draftCapabilityID, second.Event.CompilationID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace compilation was visible: %v", err)
	}
	if _, err := repository.GetTrialRun(
		context.Background(), draftOtherWorkspaceID, draftCapabilityID, second.Event.TrialID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace trial was visible: %v", err)
	}
	if _, err := repository.ResolveRevisionSnapshot(
		context.Background(), draftOtherWorkspaceID, draftCapabilityID, second.Release.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace revision snapshot was visible: %v", err)
	}

	for table, expected := range map[string]int{
		"workflow_drafts":       1,
		"workflow_compilations": 2,
		"workflow_trial_runs":   2,
		"workflow_revisions":    2,
		"capability_releases":   2,
	} {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM `+table+` WHERE workspace_id=$1 AND capability_id=$2`,
			draftWorkspaceID, draftCapabilityID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != expected {
			t.Fatalf("unexpected %s fact count: got=%d want=%d", table, count, expected)
		}
	}
	for _, forbidden := range []string{"dsl", "canvas_graph", "readiness", "agent_id"} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM information_schema.columns
			 WHERE table_schema='public' AND table_name='workflows' AND column_name=$1)
		`, forbidden).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("normalized workflows table contains compatibility fact %s", forbidden)
		}
	}
}
