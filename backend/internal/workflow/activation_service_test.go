package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/workflowcompiler"
	"actweave/backend/internal/workspace"
)

const (
	workflowSecondRevisionID   = "308f1f2e-7b5a-7c3d-8e9f-123456789001"
	workflowSecondReleaseID    = "308f1f2e-7b5a-7c3d-8e9f-123456789002"
	workflowSecondPublishEvent = "308f1f2e-7b5a-7c3d-8e9f-123456789003"
	workflowRollbackEventID    = "308f1f2e-7b5a-7c3d-8e9f-123456789004"
	workflowReactivateEventID  = "308f1f2e-7b5a-7c3d-8e9f-123456789005"
	workflowDeniedActivateID   = "308f1f2e-7b5a-7c3d-8e9f-123456789006"
	workflowOtherCapabilityID  = "308f1f2e-7b5a-7c3d-8e9f-123456789007"
	workflowOtherDraftID       = "308f1f2e-7b5a-7c3d-8e9f-123456789008"
	workflowOtherRevisionID    = "308f1f2e-7b5a-7c3d-8e9f-123456789009"
	workflowOtherReleaseID     = "308f1f2e-7b5a-7c3d-8e9f-12345678900a"
	workflowOtherPublishEvent  = "308f1f2e-7b5a-7c3d-8e9f-12345678900b"
	workflowCrossActivateEvent = "308f1f2e-7b5a-7c3d-8e9f-12345678900c"
)

func TestRollbackSwitchesActiveRevisionAndReleaseWithoutMutatingHistory(t *testing.T) {
	repository, db, first, second, publishEvents := prepareTwoPublishedWorkflowRevisions(t)
	activationEvents := newWorkflowActivationEventWriter(t, db)
	service := newWorkflowActivationService(t, repository, db, activationEvents)
	firstBefore := getWorkflowRevision(t, db, first.Revision.ID)
	secondBefore := getWorkflowRevision(t, db, second.Revision.ID)

	denied := ActivateRevisionInput{
		EventID: workflowDeniedActivateID, WorkspaceID: draftWorkspaceID,
		CapabilityID: draftCapabilityID, RevisionID: first.Revision.ID,
		ActivatedBy: workflowPublishOperatorID,
	}
	if _, err := service.Activate(context.Background(), denied); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("expected operator rollback denial, got %v", err)
	}
	assertWorkflowActivePointers(t, db, second.Revision.ID, second.Release.ID)

	activationEvents.failAfterInsert = true
	rollbackInput := ActivateRevisionInput{
		EventID: workflowRollbackEventID, WorkspaceID: draftWorkspaceID,
		CapabilityID: draftCapabilityID, RevisionID: first.Revision.ID,
		ActivatedBy: workflowPublishEditorID,
	}
	if _, err := service.Activate(context.Background(), rollbackInput); err == nil {
		t.Fatal("expected rollback event failure")
	}
	assertWorkflowActivePointers(t, db, second.Revision.ID, second.Release.ID)
	var activationEventCount int
	if err := db.QueryRow(`SELECT count(*) FROM test_workflow_activation_events`).Scan(&activationEventCount); err != nil {
		t.Fatal(err)
	}
	if activationEventCount != 0 {
		t.Fatalf("failed rollback left event row: %d", activationEventCount)
	}

	activationEvents.failAfterInsert = false
	rolledBack, err := service.Activate(context.Background(), rollbackInput)
	if err != nil {
		t.Fatalf("roll back to first workflow revision: %v", err)
	}
	if rolledBack.Revision.ID != first.Revision.ID || rolledBack.Release.ID != first.Release.ID ||
		rolledBack.Event.Type != "workflow.release.rolled_back" ||
		rolledBack.Event.PreviousRevisionID == nil || *rolledBack.Event.PreviousRevisionID != second.Revision.ID ||
		rolledBack.Event.PreviousReleaseID == nil || *rolledBack.Event.PreviousReleaseID != second.Release.ID {
		t.Fatalf("unexpected rollback result: %+v", rolledBack)
	}
	assertWorkflowActivePointers(t, db, first.Revision.ID, first.Release.ID)
	if firstAfter := getWorkflowRevision(t, db, first.Revision.ID); !reflect.DeepEqual(firstAfter, firstBefore) {
		t.Fatalf("rollback mutated target revision: before=%+v after=%+v", firstBefore, firstAfter)
	}
	if secondAfter := getWorkflowRevision(t, db, second.Revision.ID); !reflect.DeepEqual(secondAfter, secondBefore) {
		t.Fatalf("rollback mutated previous revision: before=%+v after=%+v", secondBefore, secondAfter)
	}
	readinessService, err := NewReadinessService(repository)
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := readinessService.Get(context.Background(), draftWorkspaceID, draftCapabilityID)
	if err != nil || readiness.Stage != ReadinessPublishReady || !readiness.Published || !readiness.CanPublish {
		t.Fatalf("rollback readiness should expose published old revision plus newer ready draft: %+v err=%v", readiness, err)
	}

	reactivated, err := service.Activate(context.Background(), ActivateRevisionInput{
		EventID: workflowReactivateEventID, WorkspaceID: draftWorkspaceID,
		CapabilityID: draftCapabilityID, RevisionID: second.Revision.ID,
		ActivatedBy: workflowPublishEditorID,
	})
	if err != nil {
		t.Fatalf("reactivate second workflow revision: %v", err)
	}
	if reactivated.Event.Type != "workflow.release.activated" ||
		reactivated.Release.ID != second.Release.ID || reactivated.Revision.ID != second.Revision.ID {
		t.Fatalf("unexpected forward activation: %+v", reactivated)
	}
	assertWorkflowActivePointers(t, db, second.Revision.ID, second.Release.ID)
	if _, err := service.Activate(context.Background(), ActivateRevisionInput{
		EventID: workflowDeniedActivateID, WorkspaceID: draftWorkspaceID,
		CapabilityID: draftCapabilityID, RevisionID: second.Revision.ID,
		ActivatedBy: workflowPublishEditorID,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected already-active revision conflict, got %v", err)
	}

	otherRevisionID := publishOtherWorkflowRevision(t, repository, db, publishEvents)
	if _, err := service.Activate(context.Background(), ActivateRevisionInput{
		EventID: workflowCrossActivateEvent, WorkspaceID: draftWorkspaceID,
		CapabilityID: draftCapabilityID, RevisionID: otherRevisionID,
		ActivatedBy: workflowPublishEditorID,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workflow revision rejection, got %v", err)
	}
	assertWorkflowActivePointers(t, db, second.Revision.ID, second.Release.ID)
}

func TestActivateRevisionRejectsUnknownWorkflowScope(t *testing.T) {
	repository, db, first, _, _ := prepareTwoPublishedWorkflowRevisions(t)
	events := newWorkflowActivationEventWriter(t, db)
	service := newWorkflowActivationService(t, repository, db, events)
	if _, err := service.Activate(context.Background(), ActivateRevisionInput{
		EventID: workflowCrossActivateEvent, WorkspaceID: draftOtherWorkspaceID,
		CapabilityID: draftCapabilityID, RevisionID: first.Revision.ID,
		ActivatedBy: workflowPublishEditorID,
	}); !errors.Is(err, authz.ErrNotVisible) {
		t.Fatalf("expected cross-workspace activation to be not visible, got %v", err)
	}
}

func prepareTwoPublishedWorkflowRevisions(
	t *testing.T,
) (*Repository, *sql.DB, PublishWorkflowResult, PublishWorkflowResult, *testWorkflowPublishEventWriter) {
	t.Helper()
	repository, db, draft, firstCompilation := prepareWorkflowPublish(t, true)
	publishEvents := newWorkflowPublishEventWriter(t, db)
	publishService := newWorkflowPublishService(t, repository, db, publishEvents)
	first, err := publishService.Publish(context.Background(), validWorkflowPublishInput(firstCompilation.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpdateDraft(context.Background(), draftWorkspaceID, draftCapabilityID, DraftUpdate{
		SchemaVersion: "workflow.graph.v1", Graph: workflowPublishGraphVersionTwo(),
		UpdatedBy: workflowPublishEditorID, ExpectedDraftVersion: draft.DraftVersion,
		ExpectedLockVersion: draft.LockVersion,
	}); err != nil {
		t.Fatal(err)
	}
	compilationService, err := NewCompilationService(repository, workflowcompiler.New())
	if err != nil {
		t.Fatal(err)
	}
	secondCompilation, err := compilationService.Compile(
		context.Background(), draftWorkspaceID, draftCapabilityID, workflowPublishEditorID,
	)
	if err != nil {
		t.Fatal(err)
	}
	runSuccessfulWorkflowTrial(t, repository, secondCompilation.ID)
	secondInput := validWorkflowPublishInput(secondCompilation.ID)
	secondInput.RevisionID, secondInput.ReleaseID, secondInput.EventID =
		workflowSecondRevisionID, workflowSecondReleaseID, workflowSecondPublishEvent
	secondInput.PublishNote = "Second release"
	second, err := publishService.Publish(context.Background(), secondInput)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db, first, second, publishEvents
}

func publishOtherWorkflowRevision(
	t *testing.T,
	repository *Repository,
	db *sql.DB,
	events *testWorkflowPublishEventWriter,
) string {
	t.Helper()
	input := CreateInput{
		CapabilityID: workflowOtherCapabilityID, DraftID: workflowOtherDraftID,
		WorkspaceID: draftWorkspaceID, Name: "Other Workflow", Slug: "other-workflow",
		Description: "Other flow", SchemaVersion: "workflow.graph.v1",
		Graph: workflowPublishGraph(), CreatedBy: workflowPublishEditorID,
	}
	if _, _, err := repository.Create(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	compilationService, err := NewCompilationService(repository, workflowcompiler.New())
	if err != nil {
		t.Fatal(err)
	}
	compilation, err := compilationService.Compile(
		context.Background(), draftWorkspaceID, workflowOtherCapabilityID, workflowPublishEditorID,
	)
	if err != nil {
		t.Fatal(err)
	}
	runner := successfulTrialRunner{}
	trialService, err := NewTrialService(repository, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trialService.Run(
		context.Background(), draftWorkspaceID, workflowOtherCapabilityID, compilation.ID,
		workflowPublishEditorID, json.RawMessage(`{}`),
	); err != nil {
		t.Fatal(err)
	}
	publishService := newWorkflowPublishService(t, repository, db, events)
	publishInput := validWorkflowPublishInput(compilation.ID)
	publishInput.RevisionID, publishInput.ReleaseID, publishInput.EventID =
		workflowOtherRevisionID, workflowOtherReleaseID, workflowOtherPublishEvent
	publishInput.CapabilityID = workflowOtherCapabilityID
	publishInput.CallableName = "other_workflow"
	result, err := publishService.Publish(context.Background(), publishInput)
	if err != nil {
		t.Fatal(err)
	}
	return result.Revision.ID
}

type successfulTrialRunner struct{}

func (successfulTrialRunner) Run(
	_ context.Context,
	request TrialExecutionRequest,
) (TrialExecutionResult, error) {
	return TrialExecutionResult{ExecutionID: request.ExecutionID, Status: TrialExecutionSucceeded}, nil
}

type testWorkflowActivationEventWriter struct{ failAfterInsert bool }

func newWorkflowActivationEventWriter(t *testing.T, db *sql.DB) *testWorkflowActivationEventWriter {
	t.Helper()
	if _, err := db.Exec(`
		CREATE TABLE test_workflow_activation_events(
		 id UUID PRIMARY KEY,event_type TEXT NOT NULL,workspace_id UUID NOT NULL,
		 capability_id UUID NOT NULL,previous_revision_id UUID,previous_release_id UUID,
		 target_revision_id UUID NOT NULL,target_revision_no INTEGER NOT NULL,
		 target_release_id UUID NOT NULL,target_release_no INTEGER NOT NULL,
		 activated_by UUID NOT NULL,occurred_at TIMESTAMPTZ NOT NULL,schema_version INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	return &testWorkflowActivationEventWriter{}
}

func (w *testWorkflowActivationEventWriter) AppendWorkflowRevisionActivated(
	ctx context.Context,
	tx *sql.Tx,
	event WorkflowRevisionActivatedEvent,
) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO test_workflow_activation_events(
		 id,event_type,workspace_id,capability_id,previous_revision_id,previous_release_id,
		 target_revision_id,target_revision_no,target_release_id,target_release_no,
		 activated_by,occurred_at,schema_version
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`, event.ID, event.Type, event.WorkspaceID, event.CapabilityID,
		event.PreviousRevisionID, event.PreviousReleaseID, event.TargetRevisionID,
		event.TargetRevisionNo, event.TargetReleaseID, event.TargetReleaseNo,
		event.ActivatedBy, event.OccurredAt, event.SchemaVersion); err != nil {
		return err
	}
	if w.failAfterInsert {
		return errors.New("forced workflow activation event failure")
	}
	return nil
}

func newWorkflowActivationService(
	t *testing.T,
	repository *Repository,
	db *sql.DB,
	events ActivationEventWriter,
) *ActivationService {
	t.Helper()
	workspaceRepository, err := workspace.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := authz.NewService(workspaceRepository)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewActivationService(repository, authorizer, events)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func getWorkflowRevision(t *testing.T, db *sql.DB, revisionID string) Revision {
	t.Helper()
	value, err := scanRevision(db.QueryRow(`
		SELECT `+revisionColumns+` FROM workflow_revisions wr WHERE wr.id=$1
	`, revisionID))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func assertWorkflowActivePointers(t *testing.T, db *sql.DB, revisionID, releaseID string) {
	t.Helper()
	var activeRevisionID, activeReleaseID string
	if err := db.QueryRow(`SELECT active_revision_id FROM workflows WHERE capability_id=$1`, draftCapabilityID).Scan(&activeRevisionID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT active_release_id FROM capabilities WHERE id=$1`, draftCapabilityID).Scan(&activeReleaseID); err != nil {
		t.Fatal(err)
	}
	if activeRevisionID != revisionID || activeReleaseID != releaseID {
		t.Fatalf("unexpected active pointers: revision=%s release=%s", activeRevisionID, activeReleaseID)
	}
}

func workflowPublishGraphVersionTwo() json.RawMessage {
	var graph map[string]any
	if err := json.Unmarshal(workflowPublishGraph(), &graph); err != nil {
		panic(err)
	}
	graph["ui"] = map[string]any{"release": 2}
	payload, err := json.Marshal(graph)
	if err != nil {
		panic(err)
	}
	return payload
}
