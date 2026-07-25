package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/workflowcompiler"
	"actweave/backend/internal/workflowruntime"
)

func TestTrialRunsImmutableCompilationPlanAndPersistsSuccess(t *testing.T) {
	repository, db, _, compilation := createValidCompiledWorkflow(t)
	runtimeRunner, err := NewRuntimeTrialRunner(workflowruntime.NewPlanRunner(nil))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewTrialService(repository, runtimeRunner)
	if err != nil {
		t.Fatal(err)
	}
	trial, err := service.Run(
		context.Background(), draftWorkspaceID, draftCapabilityID, compilation.ID,
		draftOwnerID, json.RawMessage(`{"orderId":"order-1"}`),
	)
	if err != nil {
		t.Fatalf("run workflow trial: %v", err)
	}
	if trial.Status != "SUCCEEDED" || trial.CompilationID != compilation.ID ||
		trial.FinishedAt == nil || len(trial.InputHash) != 64 ||
		!validUUID(trial.ID) || !validUUID(trial.ExecutionID) {
		t.Fatalf("unexpected successful trial: %+v", trial)
	}
	_, inputHash, err := canonicalJSON(json.RawMessage(`{"orderId":"order-1"}`), "object")
	if err != nil || inputHash != trial.InputHash {
		t.Fatalf("trial input hash is not reproducible: got=%s want=%s err=%v", inputHash, trial.InputHash, err)
	}
	stored, err := repository.GetTrialRun(context.Background(), draftWorkspaceID, draftCapabilityID, trial.ID)
	if err != nil || stored.Status != "SUCCEEDED" || stored.ExecutionID != trial.ExecutionID {
		t.Fatalf("get successful trial: %+v err=%v", stored, err)
	}
	if _, err := repository.GetTrialRun(context.Background(), draftOtherWorkspaceID, draftCapabilityID, trial.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workspace trial miss, got %v", err)
	}
	if _, err := repository.CompleteTrialRun(context.Background(), draftWorkspaceID, draftCapabilityID, trial.ID, "FAILED"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected completed trial conflict, got %v", err)
	}
	var executionReference string
	if err := db.QueryRow(`SELECT execution_id FROM workflow_trial_runs WHERE id=$1`, trial.ID).Scan(&executionReference); err != nil || executionReference != trial.ExecutionID {
		t.Fatalf("trial execution reference mismatch: id=%s err=%v", executionReference, err)
	}
}

func TestReadinessAggregatesCompilationAndSuccessfulTrialWithoutStoredFact(t *testing.T) {
	repository, db, _, compilation := createValidCompiledWorkflow(t)
	readinessService, err := NewReadinessService(repository)
	if err != nil {
		t.Fatal(err)
	}
	before, err := readinessService.Get(context.Background(), draftWorkspaceID, draftCapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Stage != ReadinessTrialRequired || !before.CompilationCurrent ||
		!before.CompilationValid || !before.CanTrial || before.CanPublish ||
		before.TrialSuccessful || len(before.Blockers) != 1 || before.Blockers[0].Code != "trial_required" {
		t.Fatalf("unexpected readiness before trial: %+v", before)
	}
	runtimeRunner, err := NewRuntimeTrialRunner(workflowruntime.NewPlanRunner(nil))
	if err != nil {
		t.Fatal(err)
	}
	trialService, err := NewTrialService(repository, runtimeRunner)
	if err != nil {
		t.Fatal(err)
	}
	trial, err := trialService.Run(
		context.Background(), draftWorkspaceID, draftCapabilityID, compilation.ID,
		draftOwnerID, json.RawMessage(`{"orderId":"order-2"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	after, err := readinessService.Get(context.Background(), draftWorkspaceID, draftCapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Stage != ReadinessPublishReady || !after.TrialCurrent ||
		!after.TrialSuccessful || !after.CanPublish || after.Published ||
		len(after.Blockers) != 0 || after.UpdatedAt.Before(*trial.FinishedAt) {
		t.Fatalf("unexpected readiness after successful trial: %+v", after)
	}
	for _, forbidden := range []string{"readiness", "trial_successful", "can_publish"} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM information_schema.columns
			 WHERE table_schema='public' AND table_name='workflows' AND column_name=$1)
		`, forbidden).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("readiness must not be stored as workflows.%s", forbidden)
		}
	}
}

func TestTrialForOldCompilationDoesNotMakeUpdatedDraftReady(t *testing.T) {
	repository, _, draft, compilation := createValidCompiledWorkflow(t)
	runner := &blockingTrialRunner{entered: make(chan struct{}), release: make(chan struct{})}
	trialService, err := NewTrialService(repository, runner)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan struct {
		trial TrialRun
		err   error
	}, 1)
	go func() {
		trial, err := trialService.Run(
			context.Background(), draftWorkspaceID, draftCapabilityID, compilation.ID,
			draftOwnerID, json.RawMessage(`{"orderId":"order-3"}`),
		)
		result <- struct {
			trial TrialRun
			err   error
		}{trial: trial, err: err}
	}()
	select {
	case <-runner.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("trial runner did not start")
	}
	released := false
	defer func() {
		if !released {
			close(runner.release)
		}
	}()

	updateContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := repository.UpdateDraft(updateContext, draftWorkspaceID, draftCapabilityID, DraftUpdate{
		SchemaVersion: "workflow.graph.v1", Graph: validCompilationGraph(true), UpdatedBy: draftOwnerID,
		ExpectedDraftVersion: draft.DraftVersion, ExpectedLockVersion: draft.LockVersion,
	}); err != nil {
		t.Fatalf("draft update blocked by external trial runner: %v", err)
	}
	close(runner.release)
	released = true
	select {
	case outcome := <-result:
		if outcome.err != nil || outcome.trial.Status != "SUCCEEDED" {
			t.Fatalf("complete old compilation trial: %+v err=%v", outcome.trial, outcome.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("trial did not finish")
	}
	readinessService, err := NewReadinessService(repository)
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := readinessService.Get(context.Background(), draftWorkspaceID, draftCapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Stage != ReadinessCompileRequired || readiness.CompilationCurrent ||
		readiness.TrialCurrent || readiness.TrialSuccessful || readiness.CanPublish {
		t.Fatalf("old compilation trial made updated draft ready: %+v", readiness)
	}
	if _, err := trialService.Run(
		context.Background(), draftWorkspaceID, draftCapabilityID, compilation.ID,
		draftOwnerID, json.RawMessage(`{}`),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale compilation trial rejection, got %v", err)
	}
}

func TestReadinessReportsCompilationIssuesAndFailedTrial(t *testing.T) {
	repository, _ := newDraftRepositoryTest(t)
	_, _, err := repository.Create(context.Background(), validWorkflowCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	compilationService, err := NewCompilationService(repository, workflowcompiler.New())
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := compilationService.Compile(context.Background(), draftWorkspaceID, draftCapabilityID, draftOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	readinessService, err := NewReadinessService(repository)
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := readinessService.Get(context.Background(), draftWorkspaceID, draftCapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	if invalid.Status != "INVALID" || readiness.Stage != ReadinessCompileFailed ||
		readiness.CanTrial || readiness.CanPublish || len(readiness.Blockers) == 0 ||
		readiness.Blockers[0].Code == "" {
		t.Fatalf("unexpected invalid compilation readiness: compilation=%+v readiness=%+v", invalid, readiness)
	}

	// A fresh valid compilation with only a failed trial remains at TRIAL_REQUIRED.
	currentDraft, err := repository.GetDraft(context.Background(), draftWorkspaceID, draftCapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpdateDraft(context.Background(), draftWorkspaceID, draftCapabilityID, DraftUpdate{
		SchemaVersion: "workflow.graph.v1", Graph: validCompilationGraph(false), UpdatedBy: draftOwnerID,
		ExpectedDraftVersion: currentDraft.DraftVersion, ExpectedLockVersion: currentDraft.LockVersion,
	}); err != nil {
		t.Fatal(err)
	}
	valid, err := compilationService.Compile(context.Background(), draftWorkspaceID, draftCapabilityID, draftOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	trialService, err := NewTrialService(repository, failingTrialRunner{})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := trialService.Run(
		context.Background(), draftWorkspaceID, draftCapabilityID, valid.ID,
		draftOwnerID, json.RawMessage(`{}`),
	)
	if !errors.Is(err, ErrTrialFailed) || failed.Status != "FAILED" || failed.FinishedAt == nil {
		t.Fatalf("expected persisted failed trial, got %+v err=%v", failed, err)
	}
	readiness, err = readinessService.Get(context.Background(), draftWorkspaceID, draftCapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Stage != ReadinessTrialRequired || readiness.TrialSuccessful || readiness.CanPublish {
		t.Fatalf("failed trial made workflow ready: %+v", readiness)
	}
}

func createValidCompiledWorkflow(t *testing.T) (*Repository, *sql.DB, Draft, Compilation) {
	t.Helper()
	repository, db := newDraftRepositoryTest(t)
	input := validWorkflowCreateInput()
	input.Graph = validCompilationGraph(false)
	_, draft, err := repository.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewCompilationService(repository, workflowcompiler.New())
	if err != nil {
		t.Fatal(err)
	}
	compilation, err := service.Compile(context.Background(), draftWorkspaceID, draftCapabilityID, draftOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db, draft, compilation
}

type blockingTrialRunner struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingTrialRunner) Run(
	_ context.Context,
	request TrialExecutionRequest,
) (TrialExecutionResult, error) {
	r.once.Do(func() { close(r.entered) })
	<-r.release
	return TrialExecutionResult{ExecutionID: request.ExecutionID, Status: TrialExecutionSucceeded}, nil
}

type failingTrialRunner struct{}

func (failingTrialRunner) Run(
	_ context.Context,
	request TrialExecutionRequest,
) (TrialExecutionResult, error) {
	return TrialExecutionResult{ExecutionID: request.ExecutionID, Status: TrialExecutionFailed}, errors.New("runtime failed")
}
