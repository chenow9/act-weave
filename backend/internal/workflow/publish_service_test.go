package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/workflowcompiler"
	"actweave/backend/internal/workflowruntime"
	"actweave/backend/internal/workspace"
)

const (
	workflowPublishEditorID   = "208f1f2e-7b5a-7c3d-8e9f-123456789001"
	workflowPublishOperatorID = "208f1f2e-7b5a-7c3d-8e9f-123456789002"
	workflowPublishRevisionID = "208f1f2e-7b5a-7c3d-8e9f-123456789003"
	workflowPublishReleaseID  = "208f1f2e-7b5a-7c3d-8e9f-123456789004"
	workflowPublishEventID    = "208f1f2e-7b5a-7c3d-8e9f-123456789005"
	workflowDeniedRevisionID  = "208f1f2e-7b5a-7c3d-8e9f-123456789006"
	workflowDeniedReleaseID   = "208f1f2e-7b5a-7c3d-8e9f-123456789007"
	workflowDeniedEventID     = "208f1f2e-7b5a-7c3d-8e9f-123456789008"
	workflowFailedRevisionID  = "208f1f2e-7b5a-7c3d-8e9f-123456789009"
	workflowFailedReleaseID   = "208f1f2e-7b5a-7c3d-8e9f-12345678900a"
	workflowFailedEventID     = "208f1f2e-7b5a-7c3d-8e9f-12345678900b"
	workflowRaceRevisionOne   = "208f1f2e-7b5a-7c3d-8e9f-12345678900c"
	workflowRaceReleaseOne    = "208f1f2e-7b5a-7c3d-8e9f-12345678900d"
	workflowRaceEventOne      = "208f1f2e-7b5a-7c3d-8e9f-12345678900e"
	workflowRaceRevisionTwo   = "208f1f2e-7b5a-7c3d-8e9f-12345678900f"
	workflowRaceReleaseTwo    = "208f1f2e-7b5a-7c3d-8e9f-123456789010"
	workflowRaceEventTwo      = "208f1f2e-7b5a-7c3d-8e9f-123456789011"
)

func TestPublishWorkflowAtomicallyFreezesRevisionReleaseAndEvent(t *testing.T) {
	repository, db, draft, compilation := prepareWorkflowPublish(t, true)
	events := newWorkflowPublishEventWriter(t, db)
	service := newWorkflowPublishService(t, repository, db, events)
	result, err := service.Publish(context.Background(), validWorkflowPublishInput(compilation.ID))
	if err != nil {
		t.Fatalf("publish workflow as editor: %v", err)
	}
	if result.Revision.ID != workflowPublishRevisionID || result.Revision.RevisionNo != 1 ||
		result.Revision.SourceCompilationID != compilation.ID || result.Revision.Status != "PUBLISHED" ||
		result.Revision.PlanHash != compilation.PlanHash || result.Revision.ActivatedAt == nil ||
		string(result.Revision.DraftSnapshot) != string(draft.Graph) ||
		string(result.Revision.SpecSnapshot) != string(compilation.Spec) ||
		string(result.Revision.PlanSnapshot) != string(compilation.Plan) {
		t.Fatalf("revision did not freeze compilation snapshots: %+v", result.Revision)
	}
	if result.Release.ID != workflowPublishReleaseID || result.Release.ReleaseNo != 1 ||
		result.Release.SourceType != "WORKFLOW_REVISION" || result.Release.SourceID != result.Revision.ID ||
		result.Release.Checksum != compilation.PlanHash || result.Release.RiskLevel != "HIGH" ||
		result.Release.SideEffectLevel != "WRITE" || !result.Release.RequiresConfirmation ||
		result.Release.PublishedBy != workflowPublishEditorID {
		t.Fatalf("unexpected workflow capability release: %+v", result.Release)
	}
	if string(result.Release.InputSchema) != `{"properties":{"orderId":{"type":"string"}},"required":["orderId"],"type":"object"}` ||
		string(result.Release.OutputSchema) != `{"properties":{"status":{"type":"string"}},"type":"object"}` {
		t.Fatalf("release schemas were not frozen from compiled spec: input=%s output=%s",
			result.Release.InputSchema, result.Release.OutputSchema)
	}
	var activeRevisionID, activeReleaseID string
	if err := db.QueryRow(`SELECT active_revision_id FROM workflows WHERE capability_id=$1`, draftCapabilityID).Scan(&activeRevisionID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT active_release_id FROM capabilities WHERE id=$1`, draftCapabilityID).Scan(&activeReleaseID); err != nil {
		t.Fatal(err)
	}
	if activeRevisionID != result.Revision.ID || activeReleaseID != result.Release.ID {
		t.Fatalf("workflow/release active pointers mismatch: revision=%s release=%s", activeRevisionID, activeReleaseID)
	}
	var eventType, eventPlanHash string
	var eventRevisionNo, eventReleaseNo, eventSchemaVersion int
	if err := db.QueryRow(`
		SELECT event_type,plan_hash,revision_no,release_no,schema_version
		FROM test_workflow_publish_events WHERE id=$1
	`, workflowPublishEventID).Scan(
		&eventType, &eventPlanHash, &eventRevisionNo, &eventReleaseNo, &eventSchemaVersion,
	); err != nil {
		t.Fatal(err)
	}
	if eventType != "workflow.release.published" || eventPlanHash != compilation.PlanHash ||
		eventRevisionNo != 1 || eventReleaseNo != 1 || eventSchemaVersion != 1 {
		t.Fatalf("unexpected workflow publish event: type=%s hash=%s revision=%d release=%d schema=%d",
			eventType, eventPlanHash, eventRevisionNo, eventReleaseNo, eventSchemaVersion)
	}
	if _, err := db.Exec(`UPDATE workflow_revisions SET plan_snapshot='{}' WHERE id=$1`, result.Revision.ID); err == nil {
		t.Fatal("published workflow revision remained mutable")
	}
	if _, err := db.Exec(`UPDATE capability_releases SET callable_description='changed' WHERE id=$1`, result.Release.ID); err == nil {
		t.Fatal("published workflow release remained mutable")
	}
	readinessService, err := NewReadinessService(repository)
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := readinessService.Get(context.Background(), draftWorkspaceID, draftCapabilityID)
	if err != nil || readiness.Stage != ReadinessPublished || !readiness.Published || readiness.CanPublish {
		t.Fatalf("published workflow readiness mismatch: %+v err=%v", readiness, err)
	}
}

func TestPublishWorkflowRequiresEditorTrialAndAtomicEvent(t *testing.T) {
	repository, db, draft, compilation := prepareWorkflowPublish(t, false)
	events := newWorkflowPublishEventWriter(t, db)
	service := newWorkflowPublishService(t, repository, db, events)

	denied := validWorkflowPublishInput(compilation.ID)
	denied.RevisionID, denied.ReleaseID, denied.EventID = workflowDeniedRevisionID, workflowDeniedReleaseID, workflowDeniedEventID
	denied.PublishedBy = workflowPublishOperatorID
	if _, err := service.Publish(context.Background(), denied); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("expected operator publish denial, got %v", err)
	}
	assertNoWorkflowPublishState(t, db)

	if _, err := service.Publish(context.Background(), validWorkflowPublishInput(compilation.ID)); !errors.Is(err, ErrNoSuccessfulTrial) {
		t.Fatalf("expected successful trial prerequisite, got %v", err)
	}
	assertNoWorkflowPublishState(t, db)

	runSuccessfulWorkflowTrial(t, repository, compilation.ID)
	events.failAfterInsert = true
	failed := validWorkflowPublishInput(compilation.ID)
	failed.RevisionID, failed.ReleaseID, failed.EventID = workflowFailedRevisionID, workflowFailedReleaseID, workflowFailedEventID
	if _, err := service.Publish(context.Background(), failed); err == nil {
		t.Fatal("expected transactional workflow event failure")
	}
	assertNoWorkflowPublishState(t, db)

	currentDraft, err := repository.GetDraft(context.Background(), draftWorkspaceID, draftCapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	if currentDraft.ID != draft.ID {
		t.Fatalf("unexpected workflow draft: %+v", currentDraft)
	}
	if _, err := repository.UpdateDraft(context.Background(), draftWorkspaceID, draftCapabilityID, DraftUpdate{
		SchemaVersion: "workflow.graph.v1", Graph: validCompilationGraph(true), UpdatedBy: draftOwnerID,
		ExpectedDraftVersion: currentDraft.DraftVersion, ExpectedLockVersion: currentDraft.LockVersion,
	}); err != nil {
		t.Fatal(err)
	}
	events.failAfterInsert = false
	if _, err := service.Publish(context.Background(), validWorkflowPublishInput(compilation.ID)); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale compilation publish conflict, got %v", err)
	}
	assertNoWorkflowPublishState(t, db)
}

func TestPublishWorkflowConcurrentPublicationHasSingleWinner(t *testing.T) {
	repository, db, _, compilation := prepareWorkflowPublish(t, true)
	events := newWorkflowPublishEventWriter(t, db)
	service := newWorkflowPublishService(t, repository, db, events)
	inputs := []PublishWorkflowInput{
		validWorkflowPublishInput(compilation.ID),
		validWorkflowPublishInput(compilation.ID),
	}
	inputs[0].RevisionID, inputs[0].ReleaseID, inputs[0].EventID = workflowRaceRevisionOne, workflowRaceReleaseOne, workflowRaceEventOne
	inputs[1].RevisionID, inputs[1].ReleaseID, inputs[1].EventID = workflowRaceRevisionTwo, workflowRaceReleaseTwo, workflowRaceEventTwo
	results := make(chan error, len(inputs))
	start := make(chan struct{})
	var group sync.WaitGroup
	for _, input := range inputs {
		input := input
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := service.Publish(context.Background(), input)
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent workflow publish result: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected one workflow publish winner, successes=%d conflicts=%d", successes, conflicts)
	}
	var revisions, releases, eventCount int
	if err := db.QueryRow(`SELECT count(*) FROM workflow_revisions`).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM capability_releases`).Scan(&releases); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM test_workflow_publish_events`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if revisions != 1 || releases != 1 || eventCount != 1 {
		t.Fatalf("concurrent publish left duplicate state: revisions=%d releases=%d events=%d",
			revisions, releases, eventCount)
	}
}

func prepareWorkflowPublish(t *testing.T, withTrial bool) (*Repository, *sql.DB, Draft, Compilation) {
	t.Helper()
	repository, db := newDraftRepositoryTest(t)
	insertWorkflowPublishMembers(t, db)
	input := validWorkflowCreateInput()
	input.Graph = workflowPublishGraph()
	_, draft, err := repository.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	compilationService, err := NewCompilationService(repository, workflowcompiler.New())
	if err != nil {
		t.Fatal(err)
	}
	compilation, err := compilationService.Compile(context.Background(), draftWorkspaceID, draftCapabilityID, workflowPublishEditorID)
	if err != nil {
		t.Fatal(err)
	}
	if withTrial {
		runSuccessfulWorkflowTrial(t, repository, compilation.ID)
	}
	return repository, db, draft, compilation
}

func runSuccessfulWorkflowTrial(t *testing.T, repository *Repository, compilationID string) TrialRun {
	t.Helper()
	runner, err := NewRuntimeTrialRunner(workflowruntime.NewPlanRunner(nil))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewTrialService(repository, runner)
	if err != nil {
		t.Fatal(err)
	}
	trial, err := service.Run(
		context.Background(), draftWorkspaceID, draftCapabilityID, compilationID,
		workflowPublishEditorID, json.RawMessage(`{"orderId":"publish-order"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	return trial
}

func insertWorkflowPublishMembers(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO users(id,username,display_name) VALUES
		($1,'workflow.publish.editor','Workflow Publish Editor'),
		($2,'workflow.publish.operator','Workflow Publish Operator')
	`, workflowPublishEditorID, workflowPublishOperatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_members(workspace_id,user_id,role,invited_by) VALUES
		($1,$2,'EDITOR',$4),($1,$3,'OPERATOR',$4)
	`, draftWorkspaceID, workflowPublishEditorID, workflowPublishOperatorID, draftOwnerID); err != nil {
		t.Fatal(err)
	}
}

func newWorkflowPublishService(
	t *testing.T,
	repository *Repository,
	db *sql.DB,
	events PublishEventWriter,
) *PublishService {
	t.Helper()
	workspaceRepository, err := workspace.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := authz.NewService(workspaceRepository)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewPublishService(repository, authorizer, events)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type testWorkflowPublishEventWriter struct{ failAfterInsert bool }

func newWorkflowPublishEventWriter(t *testing.T, db *sql.DB) *testWorkflowPublishEventWriter {
	t.Helper()
	if _, err := db.Exec(`
		CREATE TABLE test_workflow_publish_events(
		 id UUID PRIMARY KEY,event_type TEXT NOT NULL,workspace_id UUID NOT NULL,
		 capability_id UUID NOT NULL,compilation_id UUID NOT NULL,trial_id UUID NOT NULL,
		 revision_id UUID NOT NULL,revision_no INTEGER NOT NULL,release_id UUID NOT NULL,
		 release_no INTEGER NOT NULL,plan_hash TEXT NOT NULL,published_by UUID NOT NULL,
		 occurred_at TIMESTAMPTZ NOT NULL,schema_version INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	return &testWorkflowPublishEventWriter{}
}

func (w *testWorkflowPublishEventWriter) AppendWorkflowReleasePublished(
	ctx context.Context,
	tx *sql.Tx,
	event WorkflowReleasePublishedEvent,
) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO test_workflow_publish_events(
		 id,event_type,workspace_id,capability_id,compilation_id,trial_id,
		 revision_id,revision_no,release_id,release_no,plan_hash,published_by,
		 occurred_at,schema_version
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	`, event.ID, event.Type, event.WorkspaceID, event.CapabilityID,
		event.CompilationID, event.TrialID, event.RevisionID, event.RevisionNo,
		event.ReleaseID, event.ReleaseNo, event.PlanHash, event.PublishedBy,
		event.OccurredAt, event.SchemaVersion); err != nil {
		return err
	}
	if w.failAfterInsert {
		return errors.New("forced workflow publish event failure")
	}
	return nil
}

func validWorkflowPublishInput(compilationID string) PublishWorkflowInput {
	return PublishWorkflowInput{
		RevisionID: workflowPublishRevisionID, ReleaseID: workflowPublishReleaseID,
		EventID: workflowPublishEventID, WorkspaceID: draftWorkspaceID,
		CapabilityID: draftCapabilityID, CompilationID: compilationID,
		CallableName: "orders_workflow", CallableDescription: "Process an order",
		RiskLevel: "HIGH", SideEffectLevel: "WRITE", RequiresConfirmation: true,
		PublishNote: "Initial release", PublishedBy: workflowPublishEditorID,
	}
}

func workflowPublishGraph() json.RawMessage {
	return json.RawMessage(`{
		"schemaVersion":"workflow.graph.v1",
		"nodes":[
			{"id":"start","type":"Start","data":{"inputSchema":{"type":"object","properties":{"orderId":{"type":"string"}},"required":["orderId"]}}},
			{"id":"end","type":"End","data":{"outputSchema":{"type":"object","properties":{"status":{"type":"string"}}}}}
		],
		"edges":[{"id":"edge-1","sourceNodeId":"start","targetNodeId":"end"}]
	}`)
}

func assertNoWorkflowPublishState(t *testing.T, db *sql.DB) {
	t.Helper()
	var revisions, releases, events int
	var activeRevision, activeRelease sql.NullString
	if err := db.QueryRow(`SELECT count(*) FROM workflow_revisions`).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM capability_releases`).Scan(&releases); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM test_workflow_publish_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT active_revision_id FROM workflows WHERE capability_id=$1`, draftCapabilityID).Scan(&activeRevision); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT active_release_id FROM capabilities WHERE id=$1`, draftCapabilityID).Scan(&activeRelease); err != nil {
		t.Fatal(err)
	}
	if revisions != 0 || releases != 0 || events != 0 || activeRevision.Valid || activeRelease.Valid {
		t.Fatalf("workflow publish state was not atomic: revisions=%d releases=%d events=%d revision=%v release=%v",
			revisions, releases, events, activeRevision, activeRelease)
	}
}
