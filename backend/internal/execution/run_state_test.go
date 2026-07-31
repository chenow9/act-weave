package execution_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
	"github.com/google/uuid"
)

const (
	runStateAgentRunID      = "808f1f2e-7b5a-7c3d-8e9f-123456789001"
	runStateCompetingRunID  = "808f1f2e-7b5a-7c3d-8e9f-123456789002"
	runStateWorkflowExecID  = "808f1f2e-7b5a-7c3d-8e9f-123456789003"
	runStateAgentStepID     = "808f1f2e-7b5a-7c3d-8e9f-123456789004"
	runStateExecutionStepID = "808f1f2e-7b5a-7c3d-8e9f-123456789005"
	runStateRawObjectID     = "808f1f2e-7b5a-7c3d-8e9f-123456789006"
	runStateSnapshotRunID   = "808f1f2e-7b5a-7c3d-8e9f-123456789007"
	runStateSnapshotExecID  = "808f1f2e-7b5a-7c3d-8e9f-123456789008"
)

func TestRunStateMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("expected clean run state migration version 21, got %+v", version)
	}
	db := testDatabase.Open(t)
	for _, table := range []string{"agent_runs", "workflow_executions"} {
		for _, column := range []string{"snapshot_schema_version", "authorization_snapshot"} {
			var exists bool
			if err := db.QueryRow(`
				SELECT EXISTS(SELECT 1 FROM information_schema.columns
				 WHERE table_schema='public' AND table_name=$1 AND column_name=$2)
			`, table, column).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if !exists {
				t.Fatalf("expected %s.%s", table, column)
			}
		}
	}
}

func TestRunStateMachineAndStepAppend(t *testing.T) {
	repository, service, db, _ := newRunStateTest(t)
	db.SetMaxOpenConns(16)
	ctx := context.Background()
	run, err := service.StartAgentRun(ctx, runStateAgentRequest(runStateAgentRunID, "trace-run-state"))
	if err != nil {
		t.Fatalf("start agent run: %v", err)
	}
	if run.Status != "RUNNING" || run.LockVersion != 1 || run.TriggeredByType != "USER" {
		t.Fatalf("unexpected started agent run: %+v", run)
	}
	if _, err := repository.GetAgentRun(ctx, executionOtherWorkspaceID, run.ID); !errors.Is(err, execution.ErrRunNotFound) {
		t.Fatalf("expected cross-workspace run lookup miss, got %v", err)
	}
	if _, err := repository.AppendAgentRunStep(ctx, execution.AppendAgentRunStepInput{
		ID: uuid.NewString(), WorkspaceID: executionOtherWorkspaceID, RunID: run.ID,
		StepType: "MODEL", InputSummary: json.RawMessage(`{}`),
	}); !errors.Is(err, execution.ErrRunNotFound) {
		t.Fatalf("expected cross-workspace step append miss, got %v", err)
	}

	const agentStepCount = 8
	agentStepResults := make(chan execution.AgentRunStep, agentStepCount)
	agentStepErrors := make(chan error, agentStepCount)
	var wait sync.WaitGroup
	for index := 0; index < agentStepCount; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, err := repository.AppendAgentRunStep(ctx, execution.AppendAgentRunStepInput{
				ID: uuid.NewString(), WorkspaceID: executionWorkspaceID, RunID: run.ID,
				StepType: "MODEL", InputSummary: json.RawMessage(`{"turn":1}`),
			})
			agentStepResults <- value
			agentStepErrors <- err
		}()
	}
	wait.Wait()
	close(agentStepResults)
	close(agentStepErrors)
	for err := range agentStepErrors {
		if err != nil {
			t.Fatalf("append concurrent agent run step: %v", err)
		}
	}
	sequences := map[int]struct{}{}
	for value := range agentStepResults {
		sequences[value.SequenceNo] = struct{}{}
	}
	if len(sequences) != agentStepCount {
		t.Fatalf("expected %d unique agent step sequences, got %v", agentStepCount, sequences)
	}

	agentStep, err := repository.AppendAgentRunStep(ctx, execution.AppendAgentRunStepInput{
		ID: runStateAgentStepID, WorkspaceID: executionWorkspaceID, RunID: run.ID,
		StepType: "TOOL", CapabilityReleaseID: invocationReleaseID,
		InputSummary: json.RawMessage(`{"order_id":"A-1"}`),
	})
	if err != nil {
		t.Fatalf("append release-backed agent run step: %v", err)
	}
	assertSingleStepTransitionWinner(t, func(status string) error {
		transition := execution.StepTransition{
			ExpectedStatus: "RUNNING", NewStatus: status,
			OutputSummary: json.RawMessage(`{"done":true}`), RawObjectID: runStateRawObjectID,
			RawSHA256: strings.Repeat("a", 64), RawLength: 17,
		}
		if status == "FAILED" {
			transition.ErrorCode = "STEP_FAILED"
		}
		_, err := repository.TransitionAgentRunStep(ctx, executionWorkspaceID, agentStep.ID, transition)
		return err
	})

	workflowExecution, err := service.StartWorkflowExecution(ctx, execution.StartWorkflowExecutionRequest{
		ID: runStateWorkflowExecID, WorkspaceID: executionWorkspaceID,
		WorkflowID: executionWorkflowID, RevisionID: executionRevisionID,
		AgentRunID: run.ID, TriggerType: "AGENT", TriggeredByType: "USER",
		TriggeredByID: executionOwnerID, TraceID: "trace-workflow-state",
		InputSummary: json.RawMessage(`{"order_id":"A-1"}`),
	})
	if err != nil {
		t.Fatalf("start workflow execution: %v", err)
	}
	if workflowExecution.Status != "RUNNING" || workflowExecution.RevisionID != executionRevisionID {
		t.Fatalf("unexpected workflow execution: %+v", workflowExecution)
	}
	const executionStepCount = 6
	executionSteps := make(chan execution.ExecutionStep, executionStepCount)
	executionErrors := make(chan error, executionStepCount)
	for index := 0; index < executionStepCount; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			value, err := repository.AppendExecutionStep(ctx, execution.AppendExecutionStepInput{
				ID: uuid.NewString(), WorkspaceID: executionWorkspaceID,
				ExecutionID: workflowExecution.ID, NodeID: uuid.NewString(), NodeType: "HTTP",
				InputSummary: json.RawMessage(`{"input":true}`),
			})
			executionSteps <- value
			executionErrors <- err
		}(index)
	}
	wait.Wait()
	close(executionSteps)
	close(executionErrors)
	for err := range executionErrors {
		if err != nil {
			t.Fatalf("append concurrent workflow execution step: %v", err)
		}
	}
	executionSequences := map[int]struct{}{}
	for value := range executionSteps {
		executionSequences[value.SequenceNo] = struct{}{}
	}
	if len(executionSequences) != executionStepCount {
		t.Fatalf("expected %d unique execution sequences, got %v", executionStepCount, executionSequences)
	}
	workflowStep, err := repository.AppendExecutionStep(ctx, execution.AppendExecutionStepInput{
		ID: runStateExecutionStepID, WorkspaceID: executionWorkspaceID,
		ExecutionID: workflowExecution.ID, NodeID: "end", NodeType: "END",
		InputSummary: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.TransitionExecutionStep(ctx, executionWorkspaceID, workflowStep.ID,
		execution.StepTransition{
			ExpectedStatus: "RUNNING", NewStatus: "SUCCEEDED",
			OutputSummary: json.RawMessage(`{"result":"ok"}`),
		}); err != nil {
		t.Fatalf("complete workflow execution step: %v", err)
	}
	if _, err := repository.TransitionWorkflowExecution(ctx, executionWorkspaceID,
		workflowExecution.ID, execution.RunTransition{
			ExpectedStatus: "RUNNING", ExpectedLockVersion: 99,
			NewStatus: "SUCCEEDED", OutputSummary: json.RawMessage(`{}`),
		}); !errors.Is(err, execution.ErrRunConflict) {
		t.Fatalf("expected stale workflow execution lock conflict, got %v", err)
	}
	finishedExecution, err := repository.TransitionWorkflowExecution(ctx, executionWorkspaceID,
		workflowExecution.ID, execution.RunTransition{
			ExpectedStatus: "RUNNING", ExpectedLockVersion: 1,
			NewStatus: "SUCCEEDED", OutputSummary: json.RawMessage(`{"result":"ok"}`),
		})
	if err != nil || finishedExecution.FinishedAt == nil {
		t.Fatalf("complete workflow execution: %+v err=%v", finishedExecution, err)
	}

	if _, err := repository.TransitionAgentRun(ctx, executionWorkspaceID, run.ID,
		execution.RunTransition{
			ExpectedStatus: "RUNNING", ExpectedLockVersion: 1,
			NewStatus: "PENDING", OutputSummary: json.RawMessage(`{}`),
		}); !errors.Is(err, execution.ErrRunConflict) {
		t.Fatalf("expected illegal run transition conflict, got %v", err)
	}
	finishedRun, err := repository.TransitionAgentRun(ctx, executionWorkspaceID, run.ID,
		execution.RunTransition{
			ExpectedStatus: "RUNNING", ExpectedLockVersion: 1,
			NewStatus: "SUCCEEDED", OutputSummary: json.RawMessage(`{"answer":"ok"}`),
		})
	if err != nil || finishedRun.FinishedAt == nil || finishedRun.LockVersion != 2 {
		t.Fatalf("complete agent run: %+v err=%v", finishedRun, err)
	}
	if _, err := repository.AppendAgentRunStep(ctx, execution.AppendAgentRunStepInput{
		ID: uuid.NewString(), WorkspaceID: executionWorkspaceID, RunID: run.ID,
		StepType: "MODEL", InputSummary: json.RawMessage(`{}`),
	}); !errors.Is(err, execution.ErrRunConflict) {
		t.Fatalf("expected terminal run step append conflict, got %v", err)
	}

	competingRun, err := service.StartAgentRun(ctx,
		runStateAgentRequest(runStateCompetingRunID, "trace-competing-run"))
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := repository.TransitionAgentRun(ctx, executionWorkspaceID, competingRun.ID,
		execution.RunTransition{
			ExpectedStatus: "RUNNING", ExpectedLockVersion: 1,
			NewStatus: "WAITING_CONFIRMATION", OutputSummary: json.RawMessage(`{"waiting":true}`),
		})
	if err != nil {
		t.Fatal(err)
	}
	assertSingleRunTransitionWinner(t, repository, waiting)
}

func TestSnapshotIsolationAndAuthorization(t *testing.T) {
	repository, service, db, sources := newRunStateTest(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE capabilities SET active_release_id=$2 WHERE id=$1`,
		invocationToolID, invocationReleaseID); err != nil {
		t.Fatal(err)
	}
	run, err := service.StartAgentRun(ctx,
		runStateAgentRequest(runStateSnapshotRunID, "trace-snapshot-run"))
	if err != nil {
		t.Fatal(err)
	}
	if !jsonContainsString(run.ModelSnapshot, "model-before") ||
		!jsonContainsString(run.CapabilitySnapshot, invocationReleaseID) ||
		!jsonContainsString(run.AuthorizationSnapshot, "EDITOR") {
		t.Fatalf("missing persisted run snapshots: %+v", run)
	}

	sources.snapshots.value.Model = json.RawMessage(`{"model":"model-after"}`)
	sources.snapshots.value.Capabilities = json.RawMessage(`{"releases":["` + invocationMismatchReleaseID + `"]}`)
	sources.authorization.value = json.RawMessage(`{"decision":"ALLOW","role":"OWNER"}`)
	if _, err := db.Exec(`UPDATE model_configs SET model_name='model-after' WHERE id=$1`, executionModelID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE capabilities SET active_release_id=$2 WHERE id=$1`,
		invocationToolID, invocationMismatchReleaseID); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetAgentRun(ctx, executionWorkspaceID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonContainsString(stored.ModelSnapshot, "model-before") ||
		!jsonContainsString(stored.CapabilitySnapshot, invocationReleaseID) ||
		!jsonContainsString(stored.AuthorizationSnapshot, "EDITOR") {
		t.Fatalf("started run snapshots changed with current configuration: %+v", stored)
	}
	if _, err := db.Exec(`
		UPDATE agent_runs SET authorization_snapshot='{}',lock_version=lock_version+1 WHERE id=$1
	`, run.ID); err == nil {
		t.Fatal("expected direct authorization snapshot mutation to fail")
	}

	workflowExecution, err := service.StartWorkflowExecution(ctx,
		execution.StartWorkflowExecutionRequest{
			ID: runStateSnapshotExecID, WorkspaceID: executionWorkspaceID,
			WorkflowID: executionWorkflowID, RevisionID: executionRevisionID,
			AgentRunID: run.ID, TriggerType: "AGENT", TriggeredByType: "USER",
			TriggeredByID: executionOwnerID, TraceID: "trace-snapshot-workflow",
			InputSummary: json.RawMessage(`{}`),
		})
	if err != nil {
		t.Fatal(err)
	}
	if !jsonContainsString(workflowExecution.AuthorizationSnapshot, "OWNER") ||
		workflowExecution.RevisionID != executionRevisionID {
		t.Fatalf("workflow authorization/revision snapshot missing: %+v", workflowExecution)
	}
	if _, err := db.Exec(`
		UPDATE workflow_executions SET revision_id=$2,lock_version=lock_version+1 WHERE id=$1
	`, workflowExecution.ID, executionOtherRevisionID); err == nil {
		t.Fatal("expected workflow revision snapshot mutation to fail")
	}
}

type mutableRunSnapshotSource struct{ value execution.AgentRunSnapshots }

func (source *mutableRunSnapshotSource) SnapshotAgentRun(
	context.Context,
	string,
	string,
) (execution.AgentRunSnapshots, error) {
	return source.value, nil
}

type mutableRunAuthorization struct{ value json.RawMessage }

func (source *mutableRunAuthorization) AuthorizeRun(
	context.Context,
	string,
	string,
	string,
	string,
	string,
) (json.RawMessage, error) {
	return source.value, nil
}

type runStateSources struct {
	snapshots     *mutableRunSnapshotSource
	authorization *mutableRunAuthorization
}

func newRunStateTest(
	t *testing.T,
) (*execution.RunRepository, *execution.RunService, *sql.DB, runStateSources) {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertToolInvocationFixtures(t, db)
	if _, err := db.Exec(`
		INSERT INTO stored_objects(
		 id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
		 encryption_key_id,classification,retention_mode,created_by_type,created_by_id
		) VALUES($1,$2,'actweave-executions',$3,'TOOL_INVOCATION_PAYLOAD',
		 'application/json',17,$4,'run-state-key-v1','SENSITIVE','PERMANENT','USER',$5)
	`, runStateRawObjectID, executionWorkspaceID,
		executionWorkspaceID+"/tool-invocation/"+runStateRawObjectID,
		strings.Repeat("b", 64), executionOwnerID); err != nil {
		t.Fatal(err)
	}
	repository, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := &mutableRunSnapshotSource{value: execution.AgentRunSnapshots{
		SchemaVersion: "run.v1",
		Model:         json.RawMessage(`{"modelConfigId":"` + executionModelID + `","model":"model-before"}`),
		Capabilities:  json.RawMessage(`{"releases":["` + invocationReleaseID + `"]}`),
		ContextPolicy: json.RawMessage(`{"memory":false,"maxTurns":20}`),
	}}
	authorization := &mutableRunAuthorization{value: json.RawMessage(`{"decision":"ALLOW","role":"EDITOR"}`)}
	service, err := execution.NewRunService(repository, snapshots, authorization)
	if err != nil {
		t.Fatal(err)
	}
	return repository, service, db, runStateSources{snapshots: snapshots, authorization: authorization}
}

func runStateAgentRequest(id, traceID string) execution.StartAgentRunRequest {
	return execution.StartAgentRunRequest{
		ID: id, WorkspaceID: executionWorkspaceID, SessionID: executionSessionID,
		AgentID: executionAgentID, TriggerType: "CHAT", TriggeredByType: "USER",
		TriggeredByID: executionOwnerID, TraceID: traceID,
		InputSummary: json.RawMessage(`{"message":"run the order workflow"}`),
	}
}

func assertSingleStepTransitionWinner(t *testing.T, transition func(string) error) {
	t.Helper()
	errorsChannel := make(chan error, 2)
	go func() { errorsChannel <- transition("SUCCEEDED") }()
	go func() { errorsChannel <- transition("FAILED") }()
	first, second := <-errorsChannel, <-errorsChannel
	successes, conflicts := 0, 0
	for _, err := range []error{first, second} {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, execution.ErrRunConflict):
			conflicts++
		default:
			t.Fatalf("unexpected competing step transition error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected one step transition winner, successes=%d conflicts=%d", successes, conflicts)
	}
}

func assertSingleRunTransitionWinner(
	t *testing.T,
	repository *execution.RunRepository,
	run execution.AgentRun,
) {
	t.Helper()
	errorsChannel := make(chan error, 2)
	for _, status := range []string{"RUNNING", "CANCELLED"} {
		go func(status string) {
			_, err := repository.TransitionAgentRun(context.Background(), executionWorkspaceID,
				run.ID, execution.RunTransition{
					ExpectedStatus: "WAITING_CONFIRMATION", ExpectedLockVersion: run.LockVersion,
					NewStatus: status, OutputSummary: json.RawMessage(`{"decision":"recorded"}`),
				})
			errorsChannel <- err
		}(status)
	}
	first, second := <-errorsChannel, <-errorsChannel
	successes, conflicts := 0, 0
	for _, err := range []error{first, second} {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, execution.ErrRunConflict):
			conflicts++
		default:
			t.Fatalf("unexpected competing run transition error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected one run transition winner, successes=%d conflicts=%d", successes, conflicts)
	}
}

func jsonContainsString(value json.RawMessage, expected string) bool {
	return strings.Contains(string(value), expected)
}
