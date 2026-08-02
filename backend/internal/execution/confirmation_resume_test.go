package execution_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
)

const (
	resumeConfirmationID       = "f08f1f2e-7b5a-7c3d-8e9f-123456789001"
	resumeInvocationID         = "f08f1f2e-7b5a-7c3d-8e9f-123456789002"
	resumeWorkflowConfirmation = "f08f1f2e-7b5a-7c3d-8e9f-123456789003"
	resumeCrashedClaimID       = "f08f1f2e-7b5a-7c3d-8e9f-123456789004"
)

func TestConfirmationResumeToolPausesBeforeSideEffectAndUsesPersistedSnapshot(t *testing.T) {
	ctx := context.Background()
	db, runRepository, confirmationService := newConfirmationResumeFixture(t)
	resolved, request := resumeToolResolution()
	requestSnapshot, resolvedSnapshot, err := execution.BuildToolConfirmationResumeSnapshots(request, resolved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(resolvedSnapshot), "must-never-persist") {
		t.Fatalf("resolved checkpoint leaked injected header: %s", resolvedSnapshot)
	}
	var resolverCalls atomic.Int32
	var sideEffects atomic.Int32
	pipeline := newResumeInvocationPipeline(t, confirmationService, &resolverCalls, &sideEffects)
	toolExecutor, err := execution.NewToolConfirmationResumeExecutor(pipeline)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := execution.NewConfirmationResumeRegistry(toolExecutor)
	if err != nil {
		t.Fatal(err)
	}
	checkpointRepository, _ := execution.NewConfirmationResumeRepository(db)
	resumeService, err := execution.NewConfirmationResumeService(
		checkpointRepository, confirmationService, runRepository, registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := runRepository.GetAgentRun(ctx, executionWorkspaceID, executionAgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	workflowExecution, err := runRepository.GetWorkflowExecution(ctx, executionWorkspaceID, invocationWorkflowExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	decision := resumeDecision(t, request.Input, request.ReleaseID, invocationConnectionID, "PRODUCTION", true)
	prepared, err := resumeService.Prepare(ctx, execution.PrepareConfirmationResumeInput{
		Confirmation: execution.RequestExecutionConfirmationInput{
			ID: resumeConfirmationID, WorkspaceID: executionWorkspaceID,
			ExecutionID: invocationWorkflowExecutionID, RunID: executionAgentRunID,
			TargetItemID: resumeInvocationID,
			NodeID:       "tool-1", ReleaseID: invocationReleaseID,
			ConnectionID: invocationConnectionID, PlanHash: executionPlanHash,
			RequestedBy: executionOwnerID, Decision: decision,
		},
		Kind: execution.ResumeKindTool, SnapshotSchemaVersion: execution.ConfirmationResumeSnapshotVersion,
		RequestSnapshot: requestSnapshot, ResolvedSnapshot: resolvedSnapshot, Input: request.Input,
		ExecutionStepID:              invocationExecutionStepID,
		ExpectedRunLockVersion:       run.LockVersion,
		ExpectedExecutionLockVersion: workflowExecution.LockVersion,
	})
	if err != nil {
		t.Fatalf("prepare tool confirmation pause: %v", err)
	}
	if sideEffects.Load() != 0 || prepared.Checkpoint.Status != execution.ResumeStatusPending {
		t.Fatalf("side effect ran before confirmation: calls=%d checkpoint=%+v", sideEffects.Load(), prepared.Checkpoint)
	}
	assertResumeDatabaseStatus(t, db, "WAITING_CONFIRMATION", "WAITING_CONFIRMATION", "WAITING_CONFIRMATION")

	if _, err := confirmationService.Confirm(ctx, execution.ConfirmExecutionConfirmationInput{
		WorkspaceID: executionWorkspaceID, ConfirmationID: resumeConfirmationID,
		ActorID: executionOwnerID, ResumeToken: prepared.Requested.ResumeToken,
		RunID: executionAgentRunID, TargetItemID: resumeInvocationID,
		ReleaseID: invocationReleaseID, ConnectionID: invocationConnectionID,
		PlanHash: executionPlanHash, Input: request.Input, ExpectedLockVersion: 1,
	}); err != nil {
		t.Fatalf("confirm prepared tool invocation: %v", err)
	}
	result, err := resumeService.Resume(ctx, executionWorkspaceID, resumeConfirmationID)
	if err != nil {
		t.Fatalf("resume confirmed tool invocation: %v", err)
	}
	if result.Cached || result.Checkpoint.Status != execution.ResumeStatusSucceeded ||
		sideEffects.Load() != 1 || resolverCalls.Load() != 0 ||
		!strings.Contains(string(result.Result), "persisted-release-result") {
		t.Fatalf("unexpected resumed tool result: result=%+v effects=%d resolver=%d",
			result, sideEffects.Load(), resolverCalls.Load())
	}
	assertResumeDatabaseStatus(t, db, "RUNNING", "RUNNING", "SUCCEEDED")

	cached, err := resumeService.Resume(ctx, executionWorkspaceID, resumeConfirmationID)
	if err != nil {
		t.Fatal(err)
	}
	if !cached.Cached || sideEffects.Load() != 1 || resolverCalls.Load() != 0 ||
		string(cached.Result) != string(result.Result) {
		t.Fatalf("repeat resume was not cached: cached=%+v effects=%d resolver=%d",
			cached, sideEffects.Load(), resolverCalls.Load())
	}
	if _, err := db.Exec(`UPDATE confirmation_resume_checkpoints SET resolved_snapshot='{}' WHERE confirmation_id=$1`, resumeConfirmationID); err == nil {
		t.Fatal("expected persisted resolution snapshot mutation to fail")
	}
}

func TestConfirmationResumeWorkflowCompletesParentsAndCancellationTerminates(t *testing.T) {
	for _, test := range []struct {
		name   string
		cancel bool
	}{
		{name: "confirmed workflow", cancel: false},
		{name: "cancelled workflow", cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db, runRepository, confirmationService := newConfirmationResumeFixture(t)
			var calls atomic.Int32
			executor := &resumeWorkflowExecutor{calls: &calls}
			registry, _ := execution.NewConfirmationResumeRegistry(executor)
			checkpointRepository, _ := execution.NewConfirmationResumeRepository(db)
			resumeService, _ := execution.NewConfirmationResumeService(
				checkpointRepository, confirmationService, runRepository, registry,
			)
			run, _ := runRepository.GetAgentRun(ctx, executionWorkspaceID, executionAgentRunID)
			workflowExecution, _ := runRepository.GetWorkflowExecution(ctx, executionWorkspaceID, invocationWorkflowExecutionID)
			workflowInput := json.RawMessage(`{"orderId":"A-1"}`)
			decision := resumeDecision(t, workflowInput, invocationReleaseID, "", "", true)
			requestSnapshot := json.RawMessage(`{
				"schemaVersion":"workflow-resume-request.v1",
				"workspaceId":"` + executionWorkspaceID + `",
				"capabilityId":"` + executionWorkflowID + `",
				"releaseId":"` + invocationReleaseID + `",
				"actorId":"` + executionOwnerID + `",
				"planHash":"` + executionPlanHash + `"
			}`)
			resolvedSnapshot := json.RawMessage(`{
				"schemaVersion":"workflow-resume-resolved.v1",
				"revisionId":"` + executionRevisionID + `",
				"planHash":"` + executionPlanHash + `"
			}`)
			prepared, err := resumeService.Prepare(ctx, execution.PrepareConfirmationResumeInput{
				Confirmation: execution.RequestExecutionConfirmationInput{
					ID: resumeWorkflowConfirmation, WorkspaceID: executionWorkspaceID,
					ExecutionID: invocationWorkflowExecutionID, RunID: executionAgentRunID,
					TargetItemID: invocationExecutionStepID,
					NodeID:       "workflow-root", ReleaseID: invocationReleaseID,
					PlanHash: executionPlanHash, RequestedBy: executionOwnerID, Decision: decision,
				},
				Kind:                  execution.ResumeKindWorkflow,
				SnapshotSchemaVersion: execution.ConfirmationResumeSnapshotVersion,
				RequestSnapshot:       requestSnapshot, ResolvedSnapshot: resolvedSnapshot,
				Input: workflowInput, ExpectedRunLockVersion: run.LockVersion,
				ExpectedExecutionLockVersion: workflowExecution.LockVersion,
				TerminalOnSuccess:            true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if test.cancel {
				cancelled, cancelErr := resumeService.Cancel(ctx, execution.CancelConfirmationResumeInput{
					WorkspaceID: executionWorkspaceID, ConfirmationID: resumeWorkflowConfirmation,
					ActorID: executionOwnerID, ExpectedConfirmationLockVersion: 1,
				})
				if cancelErr != nil || cancelled.Status != execution.ResumeStatusCancelled || calls.Load() != 0 {
					t.Fatalf("cancel workflow checkpoint: %+v calls=%d err=%v", cancelled, calls.Load(), cancelErr)
				}
				assertParentStatuses(t, db, "CANCELLED", "CANCELLED")
				if _, resumeErr := resumeService.Resume(ctx, executionWorkspaceID, resumeWorkflowConfirmation); !errors.Is(resumeErr, execution.ErrConfirmationResumeConflict) {
					t.Fatalf("resume cancelled workflow error = %v", resumeErr)
				}
				return
			}
			if _, err := confirmationService.Confirm(ctx, execution.ConfirmExecutionConfirmationInput{
				WorkspaceID: executionWorkspaceID, ConfirmationID: resumeWorkflowConfirmation,
				ActorID: executionOwnerID, ResumeToken: prepared.Requested.ResumeToken,
				RunID: executionAgentRunID, TargetItemID: invocationExecutionStepID,
				ReleaseID: invocationReleaseID, PlanHash: executionPlanHash,
				Input: workflowInput, ExpectedLockVersion: 1,
			}); err != nil {
				t.Fatal(err)
			}
			result, err := resumeService.Resume(ctx, executionWorkspaceID, resumeWorkflowConfirmation)
			if err != nil || result.Checkpoint.Status != execution.ResumeStatusSucceeded || calls.Load() != 1 {
				t.Fatalf("resume workflow: %+v calls=%d err=%v", result, calls.Load(), err)
			}
			assertParentStatuses(t, db, "SUCCEEDED", "SUCCEEDED")
		})
	}
}

func TestConfirmationResumeCrashRecoveryNeverDuplicatesUnknownSideEffect(t *testing.T) {
	for _, test := range []struct {
		name            string
		markExecuting   bool
		wantEffects     int32
		wantResumeError error
	}{
		{name: "expired pre-execution claim is recovered", wantEffects: 1},
		{name: "executing marker blocks unsafe replay", markExecuting: true,
			wantResumeError: execution.ErrConfirmationResumeExecuting},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db, runs, confirmations := newConfirmationResumeFixture(t)
			resolved, request := resumeToolResolution()
			requestSnapshot, resolvedSnapshot, err := execution.BuildToolConfirmationResumeSnapshots(request, resolved)
			if err != nil {
				t.Fatal(err)
			}
			var resolverCalls atomic.Int32
			var sideEffects atomic.Int32
			pipeline := newResumeInvocationPipeline(t, confirmations, &resolverCalls, &sideEffects)
			executor, err := execution.NewToolConfirmationResumeExecutor(pipeline)
			if err != nil {
				t.Fatal(err)
			}
			registry, err := execution.NewConfirmationResumeRegistry(executor)
			if err != nil {
				t.Fatal(err)
			}
			checkpoints, _ := execution.NewConfirmationResumeRepository(db)
			resumes, err := execution.NewConfirmationResumeService(checkpoints, confirmations, runs, registry)
			if err != nil {
				t.Fatal(err)
			}
			run, err := runs.GetAgentRun(ctx, executionWorkspaceID, executionAgentRunID)
			if err != nil {
				t.Fatal(err)
			}
			decision := resumeDecision(t, request.Input, request.ReleaseID,
				invocationConnectionID, "PRODUCTION", true)
			prepared, err := resumes.Prepare(ctx, execution.PrepareConfirmationResumeInput{
				Confirmation: execution.RequestExecutionConfirmationInput{
					ID: resumeConfirmationID, WorkspaceID: executionWorkspaceID,
					RunID: executionAgentRunID, NodeID: "crash-recovery-tool",
					TargetItemID: resumeInvocationID,
					ReleaseID:    invocationReleaseID, ConnectionID: invocationConnectionID,
					PlanHash: executionPlanHash, RequestedBy: executionOwnerID, Decision: decision,
				},
				Kind:                  execution.ResumeKindTool,
				SnapshotSchemaVersion: execution.ConfirmationResumeSnapshotVersion,
				RequestSnapshot:       requestSnapshot, ResolvedSnapshot: resolvedSnapshot,
				Input: request.Input, ExpectedRunLockVersion: run.LockVersion,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := confirmations.Confirm(ctx, execution.ConfirmExecutionConfirmationInput{
				WorkspaceID: executionWorkspaceID, ConfirmationID: resumeConfirmationID,
				ActorID: executionOwnerID, ResumeToken: prepared.Requested.ResumeToken,
				RunID: executionAgentRunID, TargetItemID: resumeInvocationID,
				ReleaseID: invocationReleaseID, ConnectionID: invocationConnectionID,
				PlanHash: executionPlanHash, Input: request.Input, ExpectedLockVersion: 1,
			}); err != nil {
				t.Fatal(err)
			}

			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(ctx, `
				UPDATE confirmation_resume_checkpoints
				SET status='CLAIMED',claim_id=$2,
				    claim_expires_at=clock_timestamp()-interval '1 second',
				    lock_version=lock_version+1
				WHERE confirmation_id=$1 AND status='PENDING'
			`, resumeConfirmationID, resumeCrashedClaimID); err != nil {
				t.Fatalf("simulate durable claim: %v", err)
			}
			if _, err := runs.TransitionAgentRunInTransaction(ctx, tx,
				executionWorkspaceID, executionAgentRunID, execution.RunTransition{
					ExpectedStatus:      "WAITING_CONFIRMATION",
					ExpectedLockVersion: *prepared.Checkpoint.RunWaitLockVersion,
					NewStatus:           "RUNNING", OutputSummary: json.RawMessage(`{"claimed":true}`),
				}); err != nil {
				t.Fatalf("simulate resumed parent before crash: %v", err)
			}
			if test.markExecuting {
				if _, err := tx.ExecContext(ctx, `
					UPDATE confirmation_resume_checkpoints
					SET status='EXECUTING',started_at=clock_timestamp(),
					    lock_version=lock_version+1
					WHERE confirmation_id=$1 AND status='CLAIMED'
				`, resumeConfirmationID); err != nil {
					t.Fatalf("simulate execution marker: %v", err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}

			result, err := resumes.Resume(ctx, executionWorkspaceID, resumeConfirmationID)
			if !errors.Is(err, test.wantResumeError) {
				t.Fatalf("resume after crash error=%v want=%v result=%+v", err, test.wantResumeError, result)
			}
			if sideEffects.Load() != test.wantEffects || resolverCalls.Load() != 0 {
				t.Fatalf("crash recovery effects/resolver=%d/%d want=%d/0",
					sideEffects.Load(), resolverCalls.Load(), test.wantEffects)
			}
			if test.wantResumeError == nil {
				cached, err := resumes.Resume(ctx, executionWorkspaceID, resumeConfirmationID)
				if err != nil || !cached.Cached || sideEffects.Load() != 1 {
					t.Fatalf("recovered result not cached: %+v effects=%d err=%v", cached, sideEffects.Load(), err)
				}
			}
		})
	}
}

func TestConfirmationResumeMigrationRollbackAndReapply(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 18 || version.Dirty {
		t.Fatalf("migration version = %+v", version)
	}
	db := testDatabase.Open(t)
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('public.confirmation_resume_checkpoints') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("checkpoint table missing: exists=%v err=%v", exists, err)
	}
}

func newConfirmationResumeFixture(
	t *testing.T,
) (*sql.DB, *execution.RunRepository, *execution.ConfirmationService) {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertToolInvocationFixtures(t, db)
	runs, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	confirmationRepository, err := execution.NewConfirmationRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	confirmations, err := execution.NewConfirmationService(confirmationRepository)
	if err != nil {
		t.Fatal(err)
	}
	return db, runs, confirmations
}

func resumeDecision(
	t *testing.T,
	input json.RawMessage,
	releaseID, connectionID, environment string,
	requiresConfirmation bool,
) execution.ConfirmationDecision {
	t.Helper()
	decision, err := execution.EvaluateConfirmationPolicy(execution.ConfirmationPolicyInput{
		WorkspaceSettings: json.RawMessage(`{}`),
		Release: execution.ConfirmationReleaseRisk{
			ReleaseID: releaseID, RiskLevel: "HIGH", SideEffectLevel: "WRITE",
			RequiresConfirmation: requiresConfirmation, InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Connection: execution.ConfirmationConnectionRisk{
			ConnectionID: connectionID, Environment: environment,
		},
		Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func resumeToolResolution() (execution.ResolvedInvocation, execution.InvokeRequest) {
	input := json.RawMessage(`{"orders":["A-1"]}`)
	request := execution.InvokeRequest{
		InvocationID: resumeInvocationID, WorkspaceID: executionWorkspaceID,
		CapabilityID: invocationToolID, ReleaseID: invocationReleaseID,
		ActorType: "USER", ActorID: executionOwnerID, TraceID: "trace-confirmation-resume",
		Input: input, ExplicitConnectionID: invocationConnectionID,
		PlanHash: executionPlanHash, IdempotencyKey: "confirmation-resume-tool",
	}
	resolved := execution.ResolvedInvocation{
		Snapshot: execution.ReleaseSnapshot{
			ReleaseID: invocationReleaseID, WorkspaceID: executionWorkspaceID,
			CapabilityID: invocationToolID, ToolVersionID: invocationVersionID,
			ExecutorType: execution.ExecutorTypeHTTP, ProviderID: invocationProviderID,
			InputSchema:   json.RawMessage(`{"type":"object","required":["orders"]}`),
			OutputSchema:  json.RawMessage(`{"type":"object","required":["result"]}`),
			ErrorMappings: json.RawMessage(`{}`), RuntimePolicy: json.RawMessage(`{}`),
			Checksum: invocationChecksum,
		},
		Connection: execution.ConnectionSnapshot{
			ID: invocationConnectionID, WorkspaceID: executionWorkspaceID,
			ProviderID: invocationProviderID, Environment: "PRODUCTION",
			Headers: map[string]string{"Authorization": "must-never-persist"},
		},
		Credential: execution.CredentialReference{WorkspaceID: executionWorkspaceID, AuthMode: "NONE"},
		RiskLevel:  "HIGH", SideEffectLevel: "WRITE", RequiresConfirmation: true,
		Idempotent: true, SupportsIdempotencyKey: true,
	}
	return resolved, request
}

func assertResumeDatabaseStatus(t *testing.T, db *sql.DB, runWant, executionWant, stepWant string) {
	t.Helper()
	var runStatus, executionStatus, stepStatus string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, executionAgentRunID).Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM workflow_executions WHERE id=$1`, invocationWorkflowExecutionID).Scan(&executionStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM execution_steps WHERE id=$1`, invocationExecutionStepID).Scan(&stepStatus); err != nil {
		t.Fatal(err)
	}
	if runStatus != runWant || executionStatus != executionWant || stepStatus != stepWant {
		t.Fatalf("statuses run/execution/step = %s/%s/%s, want %s/%s/%s",
			runStatus, executionStatus, stepStatus, runWant, executionWant, stepWant)
	}
}

func assertParentStatuses(t *testing.T, db *sql.DB, runWant, executionWant string) {
	t.Helper()
	var runStatus, executionStatus string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, executionAgentRunID).Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM workflow_executions WHERE id=$1`, invocationWorkflowExecutionID).Scan(&executionStatus); err != nil {
		t.Fatal(err)
	}
	if runStatus != runWant || executionStatus != executionWant {
		t.Fatalf("parent statuses = %s/%s, want %s/%s", runStatus, executionStatus, runWant, executionWant)
	}
}

type resumeAuthorizer struct{}

func (resumeAuthorizer) AuthorizeInvocation(context.Context, string, string) error { return nil }

type resumeResolver struct{ calls *atomic.Int32 }

func (resolver resumeResolver) ResolveInvocation(context.Context, execution.ResolveRequest) (execution.ResolvedInvocation, error) {
	resolver.calls.Add(1)
	return execution.ResolvedInvocation{}, errors.New("mutable resolver must not be called during resume")
}

type resumeIdempotency struct{}

func (resumeIdempotency) BeginInvocation(context.Context, execution.IdempotencyRequest) (execution.IdempotencyDecision, error) {
	return execution.IdempotencyDecision{State: execution.IdempotencyNew}, nil
}
func (resumeIdempotency) CompleteInvocation(context.Context, execution.IdempotencyRequest, execution.InvocationResult) error {
	return nil
}
func (resumeIdempotency) FailInvocation(context.Context, execution.IdempotencyRequest, string) error {
	return nil
}

type resumeLimiter struct{}

func (resumeLimiter) AllowInvocation(context.Context, execution.LimitRequest) error { return nil }

type resumeInjector struct{}

func (resumeInjector) WithInjectedConnection(
	_ context.Context,
	connection execution.ConnectionSnapshot,
	_ execution.CredentialReference,
	invoke func(execution.ConnectionSnapshot) error,
) error {
	return invoke(connection)
}

type resumeRecorder struct{}

func (resumeRecorder) InvocationStarted(context.Context, execution.InvocationRecord) error {
	return nil
}
func (resumeRecorder) InvocationFinished(context.Context, execution.InvocationRecord) error {
	return nil
}

type resumeSideEffectExecutor struct{ calls *atomic.Int32 }

func (*resumeSideEffectExecutor) Kind() string { return execution.ExecutorTypeHTTP }
func (*resumeSideEffectExecutor) Capabilities() execution.ExecutorFeatures {
	return execution.ExecutorFeatures{}
}
func (executor *resumeSideEffectExecutor) Invoke(
	_ context.Context,
	request execution.InvocationRequest,
	_ execution.InvocationEventSink,
) (execution.InvocationResult, error) {
	executor.calls.Add(1)
	return execution.InvocationResult{
		InvocationID: request.InvocationID, TraceID: request.TraceID,
		Output: json.RawMessage(`{"result":"persisted-release-result"}`), HTTPStatus: 200,
	}, nil
}
func (*resumeSideEffectExecutor) Cancel(context.Context, execution.InvocationRef) error { return nil }

func newResumeInvocationPipeline(
	t *testing.T,
	confirmations *execution.ConfirmationService,
	resolverCalls, sideEffects *atomic.Int32,
) *execution.InvocationPipeline {
	t.Helper()
	registry, err := execution.NewRegistry(&resumeSideEffectExecutor{calls: sideEffects})
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := execution.NewInvocationPipeline(
		resumeAuthorizer{}, resumeResolver{calls: resolverCalls}, confirmations,
		resumeIdempotency{}, resumeLimiter{}, resumeInjector{}, registry,
		resumeRecorder{}, execution.RetryWaiterFunc(func(context.Context, int) error { return nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	return pipeline
}

type resumeWorkflowExecutor struct{ calls *atomic.Int32 }

func (*resumeWorkflowExecutor) Kind() string { return execution.ResumeKindWorkflow }
func (executor *resumeWorkflowExecutor) Execute(
	context.Context,
	execution.ResumeExecutionInput,
) (execution.ResumeExecutionOutput, error) {
	executor.calls.Add(1)
	return execution.ResumeExecutionOutput{Result: json.RawMessage(`{"workflow":"completed"}`)}, nil
}
