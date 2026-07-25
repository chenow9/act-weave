package execution_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
)

const (
	stateConfirmationID           = "e08f1f2e-7b5a-7c3d-8e9f-123456789001"
	stateConcurrentConfirmationID = "e08f1f2e-7b5a-7c3d-8e9f-123456789002"
	stateCancelledConfirmationID  = "e08f1f2e-7b5a-7c3d-8e9f-123456789003"
	stateExpiredConfirmationID    = "e08f1f2e-7b5a-7c3d-8e9f-123456789004"
)

func TestConfirmationStateMachine(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertToolInvocationFixtures(t, db)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'state.other','Other State User')`, confirmationOtherUserID); err != nil {
		t.Fatal(err)
	}
	repository, err := execution.NewConfirmationRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	current := time.Now().UTC().Truncate(time.Millisecond)
	service, err := execution.NewConfirmationService(repository,
		execution.WithConfirmationClock(func() time.Time { return current }))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	t.Run("request stores only token hash and immutable decision", func(t *testing.T) {
		requested := requestConfirmation(t, ctx, service, stateConfirmationID)
		if requested.Confirmation.Status != execution.ConfirmationStatusPending ||
			requested.Confirmation.LockVersion != 1 || len(requested.ResumeToken) < 32 {
			t.Fatalf("unexpected requested confirmation: %+v", requested)
		}
		var tokenHash string
		if err := db.QueryRow(`SELECT resume_token_hash FROM execution_confirmations WHERE id=$1`,
			stateConfirmationID).Scan(&tokenHash); err != nil {
			t.Fatal(err)
		}
		if tokenHash == requested.ResumeToken || len(tokenHash) != 64 || strings.Contains(string(requested.Confirmation.ScopeSnapshot), requested.ResumeToken) {
			t.Fatalf("resume token leaked into persistence: hash=%q confirmation=%+v", tokenHash, requested.Confirmation)
		}
		if _, err := repository.Get(ctx, executionOtherWorkspaceID, stateConfirmationID); !errors.Is(err, execution.ErrConfirmationNotFound) {
			t.Fatalf("cross-workspace lookup error = %v", err)
		}

		base := confirmInput(requested, executionOwnerID, json.RawMessage(`{"orders":[1,2]}`))
		otherActor := base
		otherActor.ActorID = confirmationOtherUserID
		if _, err := service.Confirm(ctx, otherActor); !errors.Is(err, execution.ErrConfirmationRequesterMismatch) {
			t.Fatalf("other actor confirm error = %v", err)
		}
		wrongToken := base
		wrongToken.ResumeToken = strings.Repeat("x", 43)
		if _, err := service.Confirm(ctx, wrongToken); !errors.Is(err, execution.ErrConfirmationTokenInvalid) {
			t.Fatalf("wrong token confirm error = %v", err)
		}
		changedInput := base
		changedInput.Input = json.RawMessage(`{"orders":[1,2,3]}`)
		if _, err := service.Confirm(ctx, changedInput); !errors.Is(err, execution.ErrConfirmationBindingChanged) {
			t.Fatalf("changed input confirm error = %v", err)
		}
		changedPlan := base
		changedPlan.PlanHash = strings.Repeat("a", 64)
		if _, err := service.Confirm(ctx, changedPlan); !errors.Is(err, execution.ErrConfirmationBindingChanged) {
			t.Fatalf("changed plan confirm error = %v", err)
		}

		confirmed, err := service.Confirm(ctx, base)
		if err != nil {
			t.Fatalf("confirm by requester: %v", err)
		}
		if confirmed.Status != execution.ConfirmationStatusConfirmed ||
			confirmed.ConfirmedBy != executionOwnerID || confirmed.ConfirmedAt == nil ||
			confirmed.LockVersion != 2 {
			t.Fatalf("unexpected confirmed state: %+v", confirmed)
		}
		if err := service.VerifyInvocationConfirmation(ctx, execution.ConfirmationCheck{
			WorkspaceID: executionWorkspaceID, ConfirmationID: stateConfirmationID,
			ReleaseID: invocationReleaseID, ConnectionID: invocationConnectionID,
			PlanHash:  executionPlanHash,
			InputHash: requested.Confirmation.InputHash, ActorID: executionOwnerID,
		}); err != nil {
			t.Fatalf("verify confirmed invocation: %v", err)
		}
		if _, err := service.Confirm(ctx, base); !errors.Is(err, execution.ErrConfirmationConflict) {
			t.Fatalf("second confirmation error = %v", err)
		}
	})

	t.Run("concurrent confirmation has exactly one winner", func(t *testing.T) {
		requested := requestConfirmation(t, ctx, service, stateConcurrentConfirmationID)
		input := confirmInput(requested, executionOwnerID, json.RawMessage(`{"orders":[1,2]}`))
		const contenders = 12
		var successes atomic.Int32
		errorsSeen := make(chan error, contenders)
		var wait sync.WaitGroup
		start := make(chan struct{})
		for index := 0; index < contenders; index++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				_, confirmErr := service.Confirm(ctx, input)
				if confirmErr == nil {
					successes.Add(1)
					return
				}
				errorsSeen <- confirmErr
			}()
		}
		close(start)
		wait.Wait()
		close(errorsSeen)
		if successes.Load() != 1 {
			t.Fatalf("concurrent confirmation successes = %d, want 1", successes.Load())
		}
		for confirmErr := range errorsSeen {
			if !errors.Is(confirmErr, execution.ErrConfirmationConflict) {
				t.Fatalf("losing confirmation error = %v", confirmErr)
			}
		}
	})

	t.Run("cancel is requester-only and conditional", func(t *testing.T) {
		requested := requestConfirmation(t, ctx, service, stateCancelledConfirmationID)
		cancelInput := execution.CancelExecutionConfirmationInput{
			WorkspaceID: executionWorkspaceID, ConfirmationID: requested.Confirmation.ID,
			ActorID: confirmationOtherUserID, ExpectedLockVersion: 1,
		}
		if _, err := service.Cancel(ctx, cancelInput); !errors.Is(err, execution.ErrConfirmationRequesterMismatch) {
			t.Fatalf("other actor cancel error = %v", err)
		}
		cancelInput.ActorID = executionOwnerID
		cancelled, err := service.Cancel(ctx, cancelInput)
		if err != nil {
			t.Fatal(err)
		}
		if cancelled.Status != execution.ConfirmationStatusCancelled || cancelled.CancelledAt == nil || cancelled.LockVersion != 2 {
			t.Fatalf("unexpected cancelled confirmation: %+v", cancelled)
		}
		if _, err := service.Cancel(ctx, cancelInput); !errors.Is(err, execution.ErrConfirmationConflict) {
			t.Fatalf("second cancellation error = %v", err)
		}
		if _, err := service.Confirm(ctx, confirmInput(requested, executionOwnerID, json.RawMessage(`{"orders":[1,2]}`))); !errors.Is(err, execution.ErrConfirmationConflict) {
			t.Fatalf("confirm cancelled request error = %v", err)
		}
	})

	t.Run("expired request cannot confirm or verify", func(t *testing.T) {
		requested := requestConfirmation(t, ctx, service, stateExpiredConfirmationID)
		current = current.Add(11 * time.Minute)
		expired, err := service.ExpireDue(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, confirmation := range expired {
			if confirmation.ID == stateExpiredConfirmationID {
				found = confirmation.Status == execution.ConfirmationStatusExpired && confirmation.LockVersion == 2
			}
		}
		if !found {
			t.Fatalf("expired confirmation not returned: %+v", expired)
		}
		if _, err := service.Confirm(ctx, confirmInput(requested, executionOwnerID, json.RawMessage(`{"orders":[1,2]}`))); !errors.Is(err, execution.ErrConfirmationExpired) {
			t.Fatalf("expired confirm error = %v", err)
		}
		if err := service.VerifyInvocationConfirmation(ctx, execution.ConfirmationCheck{
			WorkspaceID: executionWorkspaceID, ConfirmationID: stateExpiredConfirmationID,
			ReleaseID: invocationReleaseID, ConnectionID: invocationConnectionID,
			PlanHash:  executionPlanHash,
			InputHash: requested.Confirmation.InputHash, ActorID: executionOwnerID,
		}); !errors.Is(err, execution.ErrConfirmationExpired) {
			t.Fatalf("expired verify error = %v", err)
		}
	})
}

func requestConfirmation(
	t *testing.T,
	ctx context.Context,
	service *execution.ConfirmationService,
	id string,
) execution.RequestedExecutionConfirmation {
	t.Helper()
	input := json.RawMessage(`{"orders":[1,2]}`)
	decision, err := execution.EvaluateConfirmationPolicy(execution.ConfirmationPolicyInput{
		WorkspaceSettings: json.RawMessage(`{}`),
		Release: execution.ConfirmationReleaseRisk{
			ReleaseID: invocationReleaseID, RiskLevel: "HIGH", SideEffectLevel: "IRREVERSIBLE",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Connection: execution.ConfirmationConnectionRisk{
			ConnectionID: invocationConnectionID, Environment: "PRODUCTION",
		},
		Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	requested, err := service.Request(ctx, execution.RequestExecutionConfirmationInput{
		ID: id, WorkspaceID: executionWorkspaceID,
		ExecutionID: invocationWorkflowExecutionID, RunID: executionAgentRunID,
		TargetItemID: invocationExecutionStepID,
		NodeID:       "tool-1", ReleaseID: invocationReleaseID,
		ConnectionID: invocationConnectionID, PlanHash: executionPlanHash,
		RequestedBy: executionOwnerID, Decision: decision,
	})
	if err != nil {
		t.Fatalf("request confirmation: %v", err)
	}
	return requested
}

func confirmInput(
	requested execution.RequestedExecutionConfirmation,
	actorID string,
	input json.RawMessage,
) execution.ConfirmExecutionConfirmationInput {
	return execution.ConfirmExecutionConfirmationInput{
		WorkspaceID: executionWorkspaceID, ConfirmationID: requested.Confirmation.ID,
		ActorID: actorID, ResumeToken: requested.ResumeToken,
		RunID: requested.Confirmation.RunID, TargetItemID: requested.Confirmation.TargetItemID,
		ReleaseID: invocationReleaseID, ConnectionID: invocationConnectionID,
		PlanHash: executionPlanHash, Input: input,
		ExpectedLockVersion: requested.Confirmation.LockVersion,
	}
}
