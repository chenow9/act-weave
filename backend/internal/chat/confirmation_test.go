package chat_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/chat"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"
)

const (
	chatConfirmationOwnerID      = "c18f1f2e-7b5a-7c3d-8e9f-123456789001"
	chatConfirmationOtherID      = "c18f1f2e-7b5a-7c3d-8e9f-123456789002"
	chatConfirmationWorkspaceID  = "c18f1f2e-7b5a-7c3d-8e9f-123456789003"
	chatConfirmationModelID      = "c18f1f2e-7b5a-7c3d-8e9f-123456789004"
	chatConfirmationAgentID      = "c18f1f2e-7b5a-7c3d-8e9f-123456789005"
	chatConfirmationSessionID    = "c18f1f2e-7b5a-7c3d-8e9f-123456789006"
	chatConfirmationRunID        = "c18f1f2e-7b5a-7c3d-8e9f-123456789007"
	chatConfirmationMessageID    = "c18f1f2e-7b5a-7c3d-8e9f-123456789008"
	chatConfirmationProviderID   = "c18f1f2e-7b5a-7c3d-8e9f-123456789009"
	chatConfirmationConnectionID = "c18f1f2e-7b5a-7c3d-8e9f-12345678900a"
	chatConfirmationToolID       = "c18f1f2e-7b5a-7c3d-8e9f-12345678900b"
	chatConfirmationVersionID    = "c18f1f2e-7b5a-7c3d-8e9f-12345678900c"
	chatConfirmationReleaseID    = "c18f1f2e-7b5a-7c3d-8e9f-12345678900d"
	chatConfirmationStepID       = "c18f1f2e-7b5a-7c3d-8e9f-12345678900e"
	chatExecutionConfirmationID  = "c18f1f2e-7b5a-7c3d-8e9f-12345678900f"
	chatProjectionID             = "c18f1f2e-7b5a-7c3d-8e9f-123456789010"
	chatConcurrentExecutionID    = "c18f1f2e-7b5a-7c3d-8e9f-123456789011"
	chatConcurrentProjectionID   = "c18f1f2e-7b5a-7c3d-8e9f-123456789012"
	chatApproveIdempotencyKey    = "c18f1f2e-7b5a-7c3d-8e9f-123456789013"
	chatCancelIdempotencyKey     = "c18f1f2e-7b5a-7c3d-8e9f-123456789014"
	chatCompetingIdempotencyKey  = "c18f1f2e-7b5a-7c3d-8e9f-123456789015"
	chatConfirmationChecksum     = "abababababababababababababababababababababababababababababababab"
)

func TestChatConfirmationConfirmUsesExecutionStateAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	service, repository, confirmations, _, db, effects := newChatConfirmationFixture(t)
	input := prepareChatConfirmationInput(t)
	prepared, err := service.Prepare(ctx, input)
	if err != nil {
		t.Fatalf("prepare chat confirmation: %v", err)
	}
	if effects.Load() != 0 || prepared.Confirmation.Status != execution.ConfirmationStatusPending ||
		prepared.Confirmation.ExecutionConfirmationID != chatExecutionConfirmationID ||
		prepared.Prepared.Requested.ResumeToken == "" {
		t.Fatalf("unexpected prepared chat confirmation: %+v effects=%d", prepared, effects.Load())
	}
	assertChatConfirmationProjection(t, repository, db, execution.ConfirmationStatusPending,
		chatProjectionID, "PENDING_CONFIRMATION", "WAITING_CONFIRMATION", "WAITING_CONFIRMATION")

	if _, err := service.Confirm(ctx, chat.ConfirmChatConfirmationInput{
		WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: chatProjectionID,
		ActorID: chatConfirmationOtherID, ResumeToken: prepared.Prepared.Requested.ResumeToken,
		IdempotencyKey:               chatApproveIdempotencyKey,
		ExpectedExecutionLockVersion: 1,
	}); !errors.Is(err, execution.ErrConfirmationRequesterMismatch) {
		t.Fatalf("other user confirmation error = %v", err)
	}
	if effects.Load() != 0 {
		t.Fatal("other user confirmation executed the side effect")
	}

	confirmed, err := service.Confirm(ctx, chat.ConfirmChatConfirmationInput{
		WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: chatProjectionID,
		ActorID: chatConfirmationOwnerID, ResumeToken: prepared.Prepared.Requested.ResumeToken,
		IdempotencyKey:               chatApproveIdempotencyKey,
		ExpectedExecutionLockVersion: 1,
	})
	if err != nil {
		t.Fatalf("confirm chat confirmation: %v", err)
	}
	if confirmed.Cached || confirmed.Confirmation.Status != execution.ConfirmationStatusConfirmed ||
		confirmed.Confirmation.ConfirmedBy != chatConfirmationOwnerID || effects.Load() != 1 {
		t.Fatalf("unexpected confirmed result: %+v effects=%d", confirmed, effects.Load())
	}
	assertChatConfirmationProjection(t, repository, db, execution.ConfirmationStatusConfirmed,
		"", "PROCESSING", "RUNNING", "SUCCEEDED")

	repeated, err := service.Confirm(ctx, chat.ConfirmChatConfirmationInput{
		WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: chatProjectionID,
		ActorID: chatConfirmationOwnerID, ResumeToken: prepared.Prepared.Requested.ResumeToken,
		IdempotencyKey:               chatApproveIdempotencyKey,
		ExpectedExecutionLockVersion: 1,
	})
	if err != nil || !repeated.Cached || effects.Load() != 1 {
		t.Fatalf("repeat confirmation not idempotent: %+v effects=%d err=%v", repeated, effects.Load(), err)
	}
	if _, err := confirmations.Get(ctx, chatConfirmationWorkspaceID, chatExecutionConfirmationID); err != nil {
		t.Fatalf("execution confirmation unavailable: %v", err)
	}
	if _, err := db.Exec(`UPDATE chat_confirmations SET status='CANCELLED' WHERE id=$1`, chatProjectionID); err == nil {
		t.Fatal("chat projection advanced independently from execution state")
	}
}

func TestAAPInteractionDecisionBinding(t *testing.T) {
	ctx := context.Background()
	service, _, _, _, _, effects := newChatConfirmationFixture(t)
	prepared, err := service.Prepare(ctx, prepareChatConfirmationInput(t))
	if err != nil {
		t.Fatal(err)
	}
	value := prepared.Confirmation
	binding := execution.InteractionDecisionBinding{
		RunID: value.RunID, TargetItemID: value.TargetItemID,
		ReleaseID: value.TargetReleaseID, InputHash: value.InputHash,
		ConnectionID: value.ConnectionID, PlanHash: value.PlanHash,
		Version: value.ExecutionLockVersion, ExpiresAt: value.ExpiresAt,
		BindingHash: value.InteractionBindingHash,
	}
	tampered := binding
	tampered.TargetItemID = chatConfirmationMessageID
	if _, err := service.Confirm(ctx, chat.ConfirmChatConfirmationInput{
		WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: chatProjectionID,
		ActorID: chatConfirmationOwnerID, IdempotencyKey: chatApproveIdempotencyKey,
		Binding: &tampered, ExpectedExecutionLockVersion: 1,
	}); !errors.Is(err, execution.ErrInteractionDecisionBindingChanged) {
		t.Fatalf("tampered Chat decision error=%v", err)
	}

	inputs := []chat.ConfirmChatConfirmationInput{
		{WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: chatProjectionID,
			ActorID: chatConfirmationOwnerID, IdempotencyKey: chatApproveIdempotencyKey,
			Binding: &binding, ExpectedExecutionLockVersion: 1},
		{WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: chatProjectionID,
			ActorID: chatConfirmationOwnerID, IdempotencyKey: chatCompetingIdempotencyKey,
			Binding: &binding, ExpectedExecutionLockVersion: 1},
	}
	type outcome struct {
		input  chat.ConfirmChatConfirmationInput
		result chat.ConfirmedChatConfirmation
		err    error
	}
	results := make(chan outcome, len(inputs))
	var wait sync.WaitGroup
	for _, input := range inputs {
		input := input
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, confirmErr := service.Confirm(ctx, input)
			results <- outcome{input: input, result: result, err: confirmErr}
		}()
	}
	wait.Wait()
	close(results)
	var winner outcome
	successes, conflicts := 0, 0
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			winner = result
		case errors.Is(result.err, chat.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected competing Chat decision error=%v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 || effects.Load() != 1 ||
		winner.result.Resume.Checkpoint.Status != execution.ResumeStatusSucceeded {
		t.Fatalf("Chat competition successes/conflicts/effects=%d/%d/%d winner=%+v",
			successes, conflicts, effects.Load(), winner.result)
	}
	replayed, err := service.Confirm(ctx, winner.input)
	if err != nil || !replayed.Cached || effects.Load() != 1 {
		t.Fatalf("Chat replay=%+v effects=%d err=%v", replayed, effects.Load(), err)
	}
	serialized, err := json.Marshal(replayed)
	if err != nil || strings.Contains(string(serialized), prepared.Prepared.Requested.ResumeToken) ||
		strings.Contains(strings.ToLower(string(serialized)), "resumetoken") {
		t.Fatalf("Chat decision response leaked resume token: %s err=%v", serialized, err)
	}
}

func TestPrincipalAwareConfirmationProjection(t *testing.T) {
	const (
		servicePrincipalID = "c38f1f2e-7b5a-7c3d-8e9f-123456789001"
		clientID           = "c38f1f2e-7b5a-7c3d-8e9f-123456789002"
		subjectID          = "c38f1f2e-7b5a-7c3d-8e9f-123456789003"
		grantID            = "c38f1f2e-7b5a-7c3d-8e9f-123456789004"
		runID              = "c38f1f2e-7b5a-7c3d-8e9f-123456789005"
		confirmationID     = "c38f1f2e-7b5a-7c3d-8e9f-123456789006"
		projectionID       = "c38f1f2e-7b5a-7c3d-8e9f-123456789007"
	)
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 60 || version.Dirty {
		t.Fatalf("expected Interaction decision binding migration 59, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertChatConfirmationFixtures(t, db)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO service_principals(id,workspace_id,name,created_by,updated_by)
		 VALUES($1,$2,'Chat External Principal',$3,$3)`, []any{servicePrincipalID, chatConfirmationWorkspaceID, chatConfirmationOwnerID}},
		{`INSERT INTO agent_access_clients(
		 id,workspace_id,service_principal_id,client_id,name,auth_method,
		 token_ttl_seconds,created_by,updated_by
		 ) VALUES($1,$2,$3,'awcl_12345678901234567890123456789012',
		 'Chat External Client','client_secret_basic',600,$4,$4)`, []any{clientID, chatConfirmationWorkspaceID, servicePrincipalID, chatConfirmationOwnerID}},
		{`INSERT INTO external_subjects(
		 id,workspace_id,client_id,issuer,subject_hash,display_ref
		 ) VALUES($1,$2,$3,'https://chat-identity.example.test',
		 decode(repeat('55',32),'hex'),'ref_chat_external_subject')`, []any{subjectID, chatConfirmationWorkspaceID, clientID}},
		{`INSERT INTO agent_access_grants(
		 id,workspace_id,client_id,agent_id,scopes,policy,created_by,updated_by
		 ) VALUES($1,$2,$3,$4,'["run:create","interaction:decide"]','{}',$5,$5)`, []any{grantID, chatConfirmationWorkspaceID, clientID, chatConfirmationAgentID, chatConfirmationOwnerID}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("insert external chat confirmation fixture: %v\n%s", err, statement.query)
		}
	}
	actor := principal.Ref{WorkspaceID: chatConfirmationWorkspaceID, Type: principal.TypeServicePrincipal, ID: servicePrincipalID}
	subject := principal.Ref{WorkspaceID: chatConfirmationWorkspaceID, Type: principal.TypeExternalSubject, ID: subjectID}
	identity, err := principal.NewInvocationIdentity(actor, &subject)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := principal.NewExecutionSnapshot(identity, clientID, grantID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.StartAgentRun(context.Background(), execution.StartAgentRunInput{
		ID: runID, WorkspaceID: chatConfirmationWorkspaceID,
		AgentID: chatConfirmationAgentID, TriggerType: "AAP",
		TriggeredByType: "SERVICE_PRINCIPAL", TriggeredByID: servicePrincipalID,
		TraceID: "trace-chat-external", PrincipalSnapshot: &snapshot,
		Snapshots: execution.AgentRunSnapshots{
			SchemaVersion: "run.v1", Model: json.RawMessage(`{"model":"chat-external"}`),
			Capabilities:  json.RawMessage(`{"releases":[]}`),
			ContextPolicy: json.RawMessage(`{"memory":false}`),
		},
		AuthorizationSnapshot: json.RawMessage(`{"action":"run:create"}`),
		InputSummary:          json.RawMessage(`{"contentType":"text"}`),
	}); err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"orderId":"A-200"}`)
	decision, err := execution.EvaluateConfirmationPolicy(execution.ConfirmationPolicyInput{
		WorkspaceSettings: json.RawMessage(`{}`),
		Release: execution.ConfirmationReleaseRisk{
			ReleaseID: chatConfirmationReleaseID, RiskLevel: "HIGH",
			SideEffectLevel: "IRREVERSIBLE", RequiresConfirmation: true,
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Connection: execution.ConfirmationConnectionRisk{
			ConnectionID: chatConfirmationConnectionID, Environment: "PRODUCTION",
		},
		Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	confirmationRepository, err := execution.NewConfirmationRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	confirmationService, err := execution.NewConfirmationService(confirmationRepository)
	if err != nil {
		t.Fatal(err)
	}
	requested, err := confirmationService.Request(context.Background(), execution.RequestExecutionConfirmationInput{
		ID: confirmationID, WorkspaceID: chatConfirmationWorkspaceID, RunID: runID,
		TargetItemID: chatConfirmationStepID,
		NodeID:       "external-chat-confirmation", ReleaseID: chatConfirmationReleaseID,
		ConnectionID: chatConfirmationConnectionID, PrincipalSnapshot: &snapshot,
		Decision: decision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO chat_confirmations(
	 id,workspace_id,session_id,run_id,execution_confirmation_id,target_type,
	 target_release_id,risk_level,risk_reasons,input_summary,status,created_at
	 ) VALUES($1,$2,$3,$4,$5,'TOOL',$6,'CRITICAL','["external"]','{}','PENDING',$7)`,
		projectionID, chatConfirmationWorkspaceID, chatConfirmationSessionID, runID,
		confirmationID, chatConfirmationReleaseID, requested.Confirmation.CreatedAt); err != nil {
		t.Fatal(err)
	}
	repository, err := chat.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := repository.GetConfirmation(context.Background(), chatConfirmationWorkspaceID, projectionID)
	if err != nil || projected.RequestedBy != "" ||
		!projected.RequestPrincipalSnapshot.SameBinding(snapshot) {
		t.Fatalf("external request projection=%+v err=%v", projected, err)
	}
	confirmed, err := confirmationService.Confirm(context.Background(), execution.ConfirmExecutionConfirmationInput{
		WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: confirmationID,
		ActorID: servicePrincipalID, PrincipalSnapshot: &snapshot,
		ResumeToken: requested.ResumeToken, ReleaseID: chatConfirmationReleaseID,
		RunID: runID, TargetItemID: chatConfirmationStepID,
		ConnectionID: chatConfirmationConnectionID,
		PlanHash:     requested.Confirmation.PlanHash, Input: input,
		ExpectedLockVersion: requested.Confirmation.LockVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	projected, err = repository.GetConfirmation(context.Background(), chatConfirmationWorkspaceID, projectionID)
	if err != nil || projected.Status != execution.ConfirmationStatusConfirmed ||
		projected.ConfirmedBy != "" || projected.DecisionPrincipalSnapshot == nil ||
		!projected.DecisionPrincipalSnapshot.SameBinding(*confirmed.DecisionPrincipalSnapshot) {
		t.Fatalf("external decision projection=%+v err=%v", projected, err)
	}
}

func TestChatConfirmationCancelIsIdempotentAndTerminatesRun(t *testing.T) {
	ctx := context.Background()
	service, repository, _, _, db, effects := newChatConfirmationFixture(t)
	prepared, err := service.Prepare(ctx, prepareChatConfirmationInput(t))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := service.Cancel(ctx, chat.CancelChatConfirmationInput{
		WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: chatProjectionID,
		ActorID: chatConfirmationOwnerID, IdempotencyKey: chatCancelIdempotencyKey,
		ExpectedExecutionLockVersion: 1,
	})
	if err != nil || cancelled.Cached || cancelled.Checkpoint.Status != execution.ResumeStatusCancelled ||
		cancelled.Confirmation.Status != execution.ConfirmationStatusCancelled || effects.Load() != 0 {
		t.Fatalf("cancel chat confirmation: %+v effects=%d err=%v", cancelled, effects.Load(), err)
	}
	assertChatConfirmationProjection(t, repository, db, execution.ConfirmationStatusCancelled,
		"", "FAILED", "CANCELLED", "CANCELLED")
	repeated, err := service.Cancel(ctx, chat.CancelChatConfirmationInput{
		WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: chatProjectionID,
		ActorID: chatConfirmationOwnerID, IdempotencyKey: chatCancelIdempotencyKey,
		ExpectedExecutionLockVersion: 1,
	})
	if err != nil || !repeated.Cached || repeated.Checkpoint.Status != execution.ResumeStatusCancelled {
		t.Fatalf("repeat cancellation not idempotent: %+v err=%v", repeated, err)
	}
	if _, err := service.Confirm(ctx, chat.ConfirmChatConfirmationInput{
		WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: chatProjectionID,
		ActorID: chatConfirmationOwnerID, ResumeToken: prepared.Prepared.Requested.ResumeToken,
		IdempotencyKey:               chatApproveIdempotencyKey,
		ExpectedExecutionLockVersion: 1,
	}); !errors.Is(err, chat.ErrConflict) {
		t.Fatalf("confirm after cancellation error = %v", err)
	}
}

func TestChatConfirmationEmitsRequestedAndConfirmedAuditEvents(t *testing.T) {
	capture := &chatAuditCapture{}
	service, _, _, _, _, _ := newChatConfirmationFixtureWithClock(
		t, nil, chat.WithConfirmationAuditSink(capture),
	)
	prepared, err := service.Prepare(context.Background(), prepareChatConfirmationInput(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Confirm(context.Background(), chat.ConfirmChatConfirmationInput{
		WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: chatProjectionID,
		ActorID: chatConfirmationOwnerID, ResumeToken: prepared.Prepared.Requested.ResumeToken,
		IdempotencyKey:               chatApproveIdempotencyKey,
		ExpectedExecutionLockVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if len(capture.events) != 2 ||
		capture.events[0].Action != "execution.confirmation.requested" ||
		capture.events[1].Action != "execution.confirmation.confirmed" {
		t.Fatalf("confirmation audit events = %+v", capture.events)
	}
	serialized, _ := json.Marshal(capture.events)
	if strings.Contains(string(serialized), "Cancel order") ||
		strings.Contains(string(serialized), prepared.Prepared.Requested.ResumeToken) {
		t.Fatalf("confirmation detail entered audit event: %s", serialized)
	}
}

func TestChatConfirmationPrepareRollsBackSensitiveDisplaySummary(t *testing.T) {
	ctx := context.Background()
	service, repository, confirmations, _, db, effects := newChatConfirmationFixture(t)
	input := prepareChatConfirmationInput(t)
	input.InputSummary = json.RawMessage(`{"action":"cancel order","authorization":"Bearer secret"}`)
	if _, err := service.Prepare(ctx, input); !errors.Is(err, chat.ErrInvalid) {
		t.Fatalf("sensitive display summary error = %v", err)
	}
	if _, err := repository.GetConfirmation(ctx, chatConfirmationWorkspaceID, chatProjectionID); !errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("chat mapping survived failed preparation: %v", err)
	}
	if _, err := confirmations.Get(ctx, chatConfirmationWorkspaceID, chatExecutionConfirmationID); !errors.Is(err, execution.ErrConfirmationNotFound) {
		t.Fatalf("execution confirmation survived failed preparation: %v", err)
	}
	assertChatConfirmationProjection(t, repository, db, "", "", "PROCESSING", "RUNNING", "RUNNING")
	if effects.Load() != 0 {
		t.Fatal("invalid display preparation executed a side effect")
	}
}

func TestChatConfirmationConcurrentPrepareCreatesOnePendingRequest(t *testing.T) {
	ctx := context.Background()
	service, _, _, _, db, effects := newChatConfirmationFixture(t)
	inputs := []chat.PrepareChatConfirmationInput{
		prepareChatConfirmationInput(t), prepareChatConfirmationInput(t),
	}
	inputs[1].ID = chatConcurrentProjectionID
	inputs[1].Resume.Confirmation.ID = chatConcurrentExecutionID
	type outcome struct {
		prepared chat.PreparedChatConfirmation
		err      error
	}
	results := make(chan outcome, len(inputs))
	var wait sync.WaitGroup
	for _, input := range inputs {
		input := input
		wait.Add(1)
		go func() {
			defer wait.Done()
			prepared, err := service.Prepare(ctx, input)
			results <- outcome{prepared: prepared, err: err}
		}()
	}
	wait.Wait()
	close(results)
	successes := 0
	winningID := ""
	for result := range results {
		if result.err == nil {
			successes++
			winningID = result.prepared.Confirmation.ID
		}
	}
	if successes != 1 || effects.Load() != 0 {
		t.Fatalf("concurrent preparations successes=%d effects=%d", successes, effects.Load())
	}
	var chatCount, executionCount int
	if err := db.QueryRow(`SELECT count(*) FROM chat_confirmations`).Scan(&chatCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM execution_confirmations`).Scan(&executionCount); err != nil {
		t.Fatal(err)
	}
	if chatCount != 1 || executionCount != 1 {
		t.Fatalf("concurrent preparation facts chat/execution=%d/%d", chatCount, executionCount)
	}
	assertChatConfirmationProjection(t, nil, db, "", winningID,
		"PENDING_CONFIRMATION", "WAITING_CONFIRMATION", "WAITING_CONFIRMATION")
}

func TestChatConfirmationConcurrentConfirmExecutesSideEffectOnce(t *testing.T) {
	ctx := context.Background()
	service, _, _, _, _, effects := newChatConfirmationFixture(t)
	prepared, err := service.Prepare(ctx, prepareChatConfirmationInput(t))
	if err != nil {
		t.Fatal(err)
	}
	const callers = 12
	errorsByCall := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := service.Confirm(ctx, chat.ConfirmChatConfirmationInput{
				WorkspaceID:                  chatConfirmationWorkspaceID,
				ConfirmationID:               chatProjectionID,
				ActorID:                      chatConfirmationOwnerID,
				ResumeToken:                  prepared.Prepared.Requested.ResumeToken,
				IdempotencyKey:               chatApproveIdempotencyKey,
				ExpectedExecutionLockVersion: 1,
			})
			errorsByCall <- err
		}()
	}
	wait.Wait()
	close(errorsByCall)
	successes := 0
	for err := range errorsByCall {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, execution.ErrConfirmationResumeConflict) &&
			!errors.Is(err, execution.ErrConfirmationResumeExecuting) {
			t.Fatalf("unexpected concurrent confirmation error: %v", err)
		}
	}
	if successes == 0 || effects.Load() != 1 {
		t.Fatalf("concurrent confirmations successes=%d effects=%d", successes, effects.Load())
	}
	final, err := service.Confirm(ctx, chat.ConfirmChatConfirmationInput{
		WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: chatProjectionID,
		ActorID: chatConfirmationOwnerID, ResumeToken: prepared.Prepared.Requested.ResumeToken,
		IdempotencyKey:               chatApproveIdempotencyKey,
		ExpectedExecutionLockVersion: 1,
	})
	if err != nil || !final.Cached || effects.Load() != 1 {
		t.Fatalf("final idempotent confirmation: %+v effects=%d err=%v", final, effects.Load(), err)
	}
}

func TestChatConfirmationExpiryTerminatesWaitingTargets(t *testing.T) {
	ctx := context.Background()
	past := time.Now().UTC().Add(-2 * time.Hour)
	service, repository, _, resumes, db, effects := newChatConfirmationFixtureWithClock(
		t, func() time.Time { return past },
	)
	prepared, err := service.Prepare(ctx, prepareChatConfirmationInput(t))
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.Confirmation.ExpiresAt.Before(time.Now().UTC()) {
		t.Fatalf("fixture confirmation is not expired: %s", prepared.Confirmation.ExpiresAt)
	}
	expired, err := resumes.ExpireDue(ctx, 10)
	if err != nil || len(expired) != 1 || expired[0].Status != execution.ResumeStatusCancelled {
		t.Fatalf("expire checkpoint: %+v err=%v", expired, err)
	}
	assertChatConfirmationProjection(t, repository, db, execution.ConfirmationStatusExpired,
		"", "FAILED", "CANCELLED", "CANCELLED")
	repeated, err := resumes.ExpireDue(ctx, 10)
	if err != nil || len(repeated) != 0 || effects.Load() != 0 {
		t.Fatalf("repeat expiry: %+v effects=%d err=%v", repeated, effects.Load(), err)
	}
	if _, err := service.Confirm(ctx, chat.ConfirmChatConfirmationInput{
		WorkspaceID: chatConfirmationWorkspaceID, ConfirmationID: chatProjectionID,
		ActorID: chatConfirmationOwnerID, ResumeToken: prepared.Prepared.Requested.ResumeToken,
		IdempotencyKey:               chatApproveIdempotencyKey,
		ExpectedExecutionLockVersion: 1,
	}); !errors.Is(err, chat.ErrConflict) {
		t.Fatalf("confirm expired request error = %v", err)
	}
}

func TestChatConfirmationProjectionMigration(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateTo(t, 25)
	if !version.Applied || version.Number != 25 || version.Dirty {
		t.Fatalf("migration version = %+v", version)
	}
	db := testDatabase.Open(t)
	for _, name := range []string{
		"chat_confirmations_one_pending_per_session_idx",
		"chat_confirmations_projection_guard",
		"execution_confirmations_chat_projection_sync",
	} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS(
			 SELECT 1 FROM pg_class WHERE relname=$1
			 UNION ALL SELECT 1 FROM pg_trigger WHERE tgname=$1 AND NOT tgisinternal
			 LIMIT 1
			)
		`, name).Scan(&exists); err != nil || !exists {
			t.Fatalf("projection database object %s: exists=%v err=%v", name, exists, err)
		}
	}
	version = testDatabase.MigrateTo(t, 24)
	if !version.Applied || version.Number != 24 || version.Dirty {
		t.Fatalf("rollback version = %+v", version)
	}
	version = testDatabase.MigrateTo(t, 25)
	if !version.Applied || version.Number != 25 || version.Dirty {
		t.Fatalf("reapply version = %+v", version)
	}
}

func prepareChatConfirmationInput(t *testing.T) chat.PrepareChatConfirmationInput {
	t.Helper()
	payload := json.RawMessage(`{"orderId":"A-10293"}`)
	decision, err := execution.EvaluateConfirmationPolicy(execution.ConfirmationPolicyInput{
		WorkspaceSettings: json.RawMessage(`{}`),
		Release: execution.ConfirmationReleaseRisk{
			ReleaseID: chatConfirmationReleaseID, RiskLevel: "HIGH",
			SideEffectLevel: "IRREVERSIBLE", RequiresConfirmation: true,
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Connection: execution.ConfirmationConnectionRisk{
			ConnectionID: chatConfirmationConnectionID, Environment: "PRODUCTION",
		},
		Input: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	return chat.PrepareChatConfirmationInput{
		ID: chatProjectionID, WorkspaceID: chatConfirmationWorkspaceID,
		SessionID: chatConfirmationSessionID, MessageID: chatConfirmationMessageID,
		TargetType: execution.ResumeKindTool, RiskLevel: "CRITICAL",
		RiskReasons: append([]string(nil), decision.RiskReasons...),
		InputSummary: json.RawMessage(`{
			"action":"Cancel order","orderId":"A-10293","warning":"This cannot be undone"
		}`),
		ExpectedSessionLockVersion: 2,
		Resume: execution.PrepareConfirmationResumeInput{
			Confirmation: execution.RequestExecutionConfirmationInput{
				ID: chatExecutionConfirmationID, WorkspaceID: chatConfirmationWorkspaceID,
				RunID: chatConfirmationRunID, TargetItemID: chatConfirmationStepID,
				NodeID:       "cancel-order",
				ReleaseID:    chatConfirmationReleaseID,
				ConnectionID: chatConfirmationConnectionID,
				RequestedBy:  chatConfirmationOwnerID, Decision: decision,
			},
			Kind:                  execution.ResumeKindTool,
			SnapshotSchemaVersion: execution.ConfirmationResumeSnapshotVersion,
			RequestSnapshot: json.RawMessage(`{
				"releaseId":"` + chatConfirmationReleaseID + `",
				"connectionId":"` + chatConfirmationConnectionID + `",
				"actorId":"` + chatConfirmationOwnerID + `","planHash":""
			}`),
			ResolvedSnapshot: json.RawMessage(`{
				"releaseId":"` + chatConfirmationReleaseID + `","executorType":"HTTP"
			}`),
			Input: payload, AgentRunStepID: chatConfirmationStepID,
			ExpectedRunLockVersion: 1,
		},
	}
}

func newChatConfirmationFixture(
	t *testing.T,
) (*chat.ConfirmationService, *chat.Repository, *execution.ConfirmationRepository, *execution.ConfirmationResumeService, *sql.DB, *atomic.Int32) {
	return newChatConfirmationFixtureWithClock(t, nil)
}

func newChatConfirmationFixtureWithClock(
	t *testing.T,
	clock func() time.Time,
	auditOptions ...chat.ConfirmationServiceOption,
) (*chat.ConfirmationService, *chat.Repository, *execution.ConfirmationRepository, *execution.ConfirmationResumeService, *sql.DB, *atomic.Int32) {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertChatConfirmationFixtures(t, db)
	repository, err := chat.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	confirmationRepository, err := execution.NewConfirmationRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	options := make([]execution.ConfirmationServiceOption, 0, 1)
	if clock != nil {
		options = append(options, execution.WithConfirmationClock(clock))
	}
	confirmationService, err := execution.NewConfirmationService(confirmationRepository, options...)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := execution.NewConfirmationResumeRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	effects := &atomic.Int32{}
	registry, err := execution.NewConfirmationResumeRegistry(&chatConfirmationExecutor{calls: effects})
	if err != nil {
		t.Fatal(err)
	}
	resumes, err := execution.NewConfirmationResumeService(checkpoints, confirmationService, runs, registry)
	if err != nil {
		t.Fatal(err)
	}
	service, err := chat.NewConfirmationService(repository, confirmationService, resumes, auditOptions...)
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, confirmationRepository, resumes, db, effects
}

type chatConfirmationExecutor struct{ calls *atomic.Int32 }

func (*chatConfirmationExecutor) Kind() string { return execution.ResumeKindTool }
func (executor *chatConfirmationExecutor) Execute(
	context.Context,
	execution.ResumeExecutionInput,
) (execution.ResumeExecutionOutput, error) {
	executor.calls.Add(1)
	return execution.ResumeExecutionOutput{Result: json.RawMessage(`{"cancelled":true}`)}, nil
}

func insertChatConfirmationFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,username,display_name) VALUES
		 ($1,'chat.confirm.owner','Chat Confirm Owner'),
		 ($2,'chat.confirm.other','Chat Confirm Other')`, []any{chatConfirmationOwnerID, chatConfirmationOtherID}},
		{`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		 VALUES($1,'chat-confirm','Chat Confirm','PRODUCTION',$2,$2,$2)`, []any{chatConfirmationWorkspaceID, chatConfirmationOwnerID}},
		{`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		 VALUES($1,$2,'Chat Confirm Model','openai','https://models.example.test','confirm',$3,$3)`, []any{chatConfirmationModelID, chatConfirmationWorkspaceID, chatConfirmationOwnerID}},
		{`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		 VALUES($1,$2,'Chat Confirm Agent',$3,$4,$4)`, []any{chatConfirmationAgentID, chatConfirmationWorkspaceID, chatConfirmationModelID, chatConfirmationOwnerID}},
		{`INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		 VALUES($1,$2,$3,'Chat confirmation',$4)`, []any{chatConfirmationSessionID, chatConfirmationWorkspaceID, chatConfirmationAgentID, chatConfirmationOwnerID}},
		{`INSERT INTO capability_providers(id,workspace_id,name,provider_kind,driver_key,transport,created_by,updated_by)
		 VALUES($1,$2,'Chat Confirm Provider','HTTP_OPENAPI','http.openapi','HTTP',$3,$3)`, []any{chatConfirmationProviderID, chatConfirmationWorkspaceID, chatConfirmationOwnerID}},
		{`INSERT INTO service_connections(id,workspace_id,provider_id,name,alias,environment,auth_mode,created_by,updated_by)
		 VALUES($1,$2,$3,'Chat Confirm Connection','primary','PRODUCTION','NONE',$4,$4)`, []any{chatConfirmationConnectionID, chatConfirmationWorkspaceID, chatConfirmationProviderID, chatConfirmationOwnerID}},
		{`INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by)
		 VALUES($1,$2,'TOOL','Cancel Order','cancel-order',$3,$3)`, []any{chatConfirmationToolID, chatConfirmationWorkspaceID, chatConfirmationOwnerID}},
		{`INSERT INTO tools(capability_id,workspace_id,provider_id,default_connection_id)
		 VALUES($1,$2,$3,$4)`, []any{chatConfirmationToolID, chatConfirmationWorkspaceID, chatConfirmationProviderID, chatConfirmationConnectionID}},
		{`INSERT INTO tool_versions(
		 id,workspace_id,capability_id,version_no,lifecycle_status,executor_type,
		 provider_id,default_connection_id,action_schema_version,action_config,
		 input_schema,output_schema,risk_level,side_effect_level,checksum,
		 created_by,updated_by,published_at
		) VALUES($1,$2,$3,1,'PUBLISHED','HTTP',$4,$5,'http.v1',
		 '{"method":"POST","path":"/orders/{id}/cancel"}','{}','{}','HIGH',
		 'IRREVERSIBLE',$6,$7,$7,clock_timestamp())`, []any{chatConfirmationVersionID, chatConfirmationWorkspaceID, chatConfirmationToolID, chatConfirmationProviderID, chatConfirmationConnectionID, chatConfirmationChecksum, chatConfirmationOwnerID}},
		{`INSERT INTO capability_releases(
		 id,workspace_id,capability_id,release_no,source_type,source_id,callable_name,
		 input_schema,output_schema,risk_level,side_effect_level,checksum,published_by
		) VALUES($1,$2,$3,1,'TOOL_VERSION',$4,'cancel_order','{}','{}','HIGH',
		 'IRREVERSIBLE',$5,$6)`, []any{chatConfirmationReleaseID, chatConfirmationWorkspaceID, chatConfirmationToolID, chatConfirmationVersionID, chatConfirmationChecksum, chatConfirmationOwnerID}},
		{`INSERT INTO agent_runs(
		 id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
		 triggered_by_id,trace_id,model_snapshot,capability_snapshot
		) VALUES($1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'trace-chat-confirm','{}','{}')`, []any{chatConfirmationRunID, chatConfirmationWorkspaceID, chatConfirmationSessionID, chatConfirmationAgentID, chatConfirmationOwnerID}},
		{`UPDATE chat_sessions SET latest_run_id=$2,lock_version=2 WHERE id=$1`, []any{chatConfirmationSessionID, chatConfirmationRunID}},
		{`INSERT INTO chat_messages(
		 id,workspace_id,session_id,role,content,content_sha256,content_length,status,run_id,created_by
		) VALUES($1,$2,$3,'USER','Cancel order A-10293',$4,20,'PROCESSING',$5,$6)`, []any{chatConfirmationMessageID, chatConfirmationWorkspaceID, chatConfirmationSessionID, strings.Repeat("a", 64), chatConfirmationRunID, chatConfirmationOwnerID}},
		{`INSERT INTO agent_run_steps(
		 id,workspace_id,run_id,step_type,sequence_no,status,capability_release_id,input_summary
		) VALUES($1,$2,$3,'TOOL',1,'RUNNING',$4,'{}')`, []any{chatConfirmationStepID, chatConfirmationWorkspaceID, chatConfirmationRunID, chatConfirmationReleaseID}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("insert chat confirmation fixture: %v\n%s", err, statement.query)
		}
	}
}

func assertChatConfirmationProjection(
	t *testing.T,
	repository *chat.Repository,
	db *sql.DB,
	status, pendingID, messageStatus, runStatus, stepStatus string,
) {
	t.Helper()
	if status != "" {
		value, err := repository.GetConfirmation(context.Background(), chatConfirmationWorkspaceID, chatProjectionID)
		if err != nil || value.Status != status {
			t.Fatalf("chat confirmation projection: %+v err=%v", value, err)
		}
	}
	var storedPending sql.NullString
	var storedMessage, storedRun, storedStep string
	if err := db.QueryRow(`SELECT pending_confirmation_id FROM chat_sessions WHERE id=$1`, chatConfirmationSessionID).Scan(&storedPending); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM chat_messages WHERE id=$1`, chatConfirmationMessageID).Scan(&storedMessage); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id=$1`, chatConfirmationRunID).Scan(&storedRun); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM agent_run_steps WHERE id=$1`, chatConfirmationStepID).Scan(&storedStep); err != nil {
		t.Fatal(err)
	}
	if storedPending.String != pendingID || storedMessage != messageStatus ||
		storedRun != runStatus || storedStep != stepStatus {
		t.Fatalf("projection pending/message/run/step=%q/%s/%s/%s want %q/%s/%s/%s",
			storedPending.String, storedMessage, storedRun, storedStep,
			pendingID, messageStatus, runStatus, stepStatus)
	}
}
