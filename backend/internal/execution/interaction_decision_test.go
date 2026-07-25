package execution_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"
)

const (
	interactionDecisionKeyOne = "f18f1f2e-7b5a-7c3d-8e9f-123456789001"
	interactionDecisionKeyTwo = "f18f1f2e-7b5a-7c3d-8e9f-123456789002"
)

func TestAAPInteractionDecisionBinding(t *testing.T) {
	ctx := context.Background()
	db, runs, confirmations := newConfirmationResumeFixture(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name)
		VALUES($1,'interaction.other','Interaction Other')`, confirmationOtherUserID); err != nil {
		t.Fatal(err)
	}
	resolved, request := resumeToolResolution()
	requestSnapshot, resolvedSnapshot, err := execution.BuildToolConfirmationResumeSnapshots(request, resolved)
	if err != nil {
		t.Fatal(err)
	}
	var effects atomic.Int32
	registry, err := execution.NewConfirmationResumeRegistry(&failingDecisionExecutor{effects: &effects})
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
	prepared, err := resumes.Prepare(ctx, execution.PrepareConfirmationResumeInput{
		Confirmation: execution.RequestExecutionConfirmationInput{
			ID: resumeConfirmationID, WorkspaceID: executionWorkspaceID,
			RunID: executionAgentRunID, TargetItemID: resumeInvocationID,
			NodeID: "bound-interaction", ReleaseID: invocationReleaseID,
			ConnectionID: invocationConnectionID, PlanHash: executionPlanHash,
			RequestedBy: executionOwnerID,
			Decision: resumeDecision(t, request.Input, invocationReleaseID,
				invocationConnectionID, "PRODUCTION", true),
		},
		Kind: execution.ResumeKindTool, SnapshotSchemaVersion: execution.ConfirmationResumeSnapshotVersion,
		RequestSnapshot: requestSnapshot, ResolvedSnapshot: resolvedSnapshot, Input: request.Input,
		ExpectedRunLockVersion: run.LockVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	decisionService, err := execution.NewInteractionDecisionService(confirmations, resumes)
	if err != nil {
		t.Fatal(err)
	}
	confirmation := prepared.Requested.Confirmation
	binding := execution.InteractionDecisionBinding{
		RunID: confirmation.RunID, TargetItemID: confirmation.TargetItemID,
		ReleaseID: confirmation.ReleaseID, InputHash: confirmation.InputHash,
		ConnectionID: confirmation.ConnectionID, PlanHash: confirmation.PlanHash,
		Version: confirmation.LockVersion, ExpiresAt: confirmation.ExpiresAt,
		BindingHash: confirmation.InteractionBindingHash,
	}
	base := execution.DecideInteractionInput{
		WorkspaceID: executionWorkspaceID, ConfirmationID: confirmation.ID,
		ActorID: executionOwnerID, Decision: execution.InteractionDecisionApprove,
		IdempotencyKey: interactionDecisionKeyOne, Binding: binding,
	}

	mutations := []struct {
		name   string
		mutate func(*execution.InteractionDecisionBinding)
	}{
		{"run", func(value *execution.InteractionDecisionBinding) { value.RunID = invocationWorkflowExecutionID }},
		{"item", func(value *execution.InteractionDecisionBinding) { value.TargetItemID = invocationExecutionStepID }},
		{"release", func(value *execution.InteractionDecisionBinding) { value.ReleaseID = invocationToolID }},
		{"input hash", func(value *execution.InteractionDecisionBinding) { value.InputHash = strings.Repeat("a", 64) }},
		{"connection", func(value *execution.InteractionDecisionBinding) { value.ConnectionID = invocationProviderID }},
		{"plan hash", func(value *execution.InteractionDecisionBinding) { value.PlanHash = strings.Repeat("b", 64) }},
		{"version", func(value *execution.InteractionDecisionBinding) { value.Version++ }},
		{"expiry", func(value *execution.InteractionDecisionBinding) { value.ExpiresAt = value.ExpiresAt.Add(-1) }},
		{"binding hash", func(value *execution.InteractionDecisionBinding) { value.BindingHash = strings.Repeat("c", 64) }},
	}
	for _, mutation := range mutations {
		t.Run("rejects changed "+mutation.name, func(t *testing.T) {
			input := base
			changed := binding
			mutation.mutate(&changed)
			input.Binding = changed
			if _, err := decisionService.Decide(ctx, input); !errors.Is(err, execution.ErrInteractionDecisionBindingChanged) {
				t.Fatalf("changed %s error=%v", mutation.name, err)
			}
		})
	}
	otherPrincipal, err := principal.NewInternalExecutionSnapshot(
		executionWorkspaceID, principal.TypeUser, confirmationOtherUserID,
	)
	if err != nil {
		t.Fatal(err)
	}
	wrongSubject := base
	wrongSubject.ActorID = confirmationOtherUserID
	wrongSubject.PrincipalSnapshot = &otherPrincipal
	if _, err := decisionService.Decide(ctx, wrongSubject); !errors.Is(err, execution.ErrConfirmationRequesterMismatch) {
		t.Fatalf("changed decision Principal error=%v", err)
	}

	type outcome struct {
		input  execution.DecideInteractionInput
		result execution.InteractionDecisionResult
		err    error
	}
	inputs := []execution.DecideInteractionInput{base, base}
	inputs[1].IdempotencyKey = interactionDecisionKeyTwo
	outcomes := make(chan outcome, 2)
	var wait sync.WaitGroup
	for _, input := range inputs {
		input := input
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, decideErr := decisionService.Decide(ctx, input)
			outcomes <- outcome{input: input, result: result, err: decideErr}
		}()
	}
	wait.Wait()
	close(outcomes)
	var winner outcome
	successes, conflicts := 0, 0
	for value := range outcomes {
		if value.err == nil {
			successes++
			winner = value
			continue
		}
		if errors.Is(value.err, execution.ErrInteractionAlreadyResolved) {
			conflicts++
			continue
		}
		t.Fatalf("unexpected competing decision error=%v", value.err)
	}
	if successes != 1 || conflicts != 1 || effects.Load() != 1 ||
		winner.result.ResumeStatus != execution.ResumeStatusFailed || winner.result.DispatchError == nil {
		t.Fatalf("competition successes/conflicts/effects=%d/%d/%d result=%+v",
			successes, conflicts, effects.Load(), winner.result)
	}
	// Decision acceptance succeeded although the Tool failed. A caller must
	// observe ResumeStatus/item.completed instead of treating command 2xx as
	// evidence that the side effect succeeded.
	replayed, err := decisionService.Decide(ctx, winner.input)
	if err != nil || !replayed.Cached || replayed.ResumeStatus != execution.ResumeStatusFailed ||
		effects.Load() != 1 {
		t.Fatalf("idempotent replay=%+v effects=%d err=%v", replayed, effects.Load(), err)
	}
	changedCommand := winner.input
	changedCommand.Decision = execution.InteractionDecisionCancel
	if _, err := decisionService.Decide(ctx, changedCommand); !errors.Is(err, execution.ErrInteractionIdempotencyConflict) {
		t.Fatalf("changed command with reused key error=%v", err)
	}
	serialized, err := json.Marshal(struct {
		Confirmation execution.ExecutionConfirmation
		Checkpoint   execution.ConfirmationResumeCheckpoint
	}{replayed.Confirmation, replayed.Checkpoint})
	if err != nil || strings.Contains(string(serialized), prepared.Requested.ResumeToken) ||
		strings.Contains(strings.ToLower(string(serialized)), "resumetoken") {
		t.Fatalf("public decision/read model leaked resume token: %s err=%v", serialized, err)
	}
}

type failingDecisionExecutor struct {
	effects *atomic.Int32
}

func TestInteractionDecisionBindingMigration(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateTo(t, 52)
	if !version.Applied || version.Number != 52 || version.Dirty {
		t.Fatalf("migration 52=%+v", version)
	}
	version = testDatabase.MigrateTo(t, 53)
	if !version.Applied || version.Number != 53 || version.Dirty {
		t.Fatalf("migration 53=%+v", version)
	}
	db := testDatabase.Open(t)
	for _, relation := range []string{
		"interaction_decision_commands",
		"execution_confirmations",
		"confirmation_resume_checkpoints",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass('public.' || $1) IS NOT NULL`, relation).Scan(&exists); err != nil || !exists {
			t.Fatalf("relation %s exists=%v err=%v", relation, exists, err)
		}
	}
	version = testDatabase.MigrateTo(t, 52)
	if !version.Applied || version.Number != 52 || version.Dirty {
		t.Fatalf("rollback migration 53=%+v", version)
	}
	version = testDatabase.MigrateTo(t, 53)
	if !version.Applied || version.Number != 53 || version.Dirty {
		t.Fatalf("reapply migration 53=%+v", version)
	}
}

func (*failingDecisionExecutor) Kind() string { return execution.ResumeKindTool }

func (executor *failingDecisionExecutor) Execute(
	context.Context,
	execution.ResumeExecutionInput,
) (execution.ResumeExecutionOutput, error) {
	executor.effects.Add(1)
	return execution.ResumeExecutionOutput{Result: json.RawMessage(`{"accepted":true}`)},
		errors.New("simulated downstream failure")
}
