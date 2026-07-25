package aap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
)

func TestRunCancellationService(t *testing.T) {
	input := validRunCancellationInput(t)
	owner, err := cancelDecisionPrincipal(input)
	if err != nil {
		t.Fatal(err)
	}
	store := &runCancellationStore{run: execution.AgentRun{
		ID: input.RunID, WorkspaceID: input.Scope.WorkspaceID, AgentID: input.Scope.AgentID,
		SessionID: testRunConversationID, Status: "RUNNING", TriggerType: "API",
		StartedAt: time.Now().UTC().Add(-time.Second), LockVersion: 1,
		PrincipalSnapshot: owner,
	}}
	lifecycle := &runCancellationLifecycle{store: store}
	runtime := &runCancellationRuntime{lifecycle: lifecycle}
	service, err := NewRunCancellationService(store, lifecycle, runtime)
	if err != nil {
		t.Fatal(err)
	}

	cancelled, err := service.Cancel(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Idempotent || cancelled.Run.Status != "CANCELLED" ||
		cancelled.Run.FinishedAt == nil || cancelled.CancelledEvent.Type != protocolevent.EventRunCancelled ||
		lifecycle.calls != 1 || runtime.calls != 1 || !runtime.sawCommittedCancellation {
		t.Fatalf("cancelled=%+v lifecycle=%d runtime=%d committed=%v",
			cancelled, lifecycle.calls, runtime.calls, runtime.sawCommittedCancellation)
	}
	if strings.Contains(string(store.run.OutputSummary), input.IdempotencyKey) ||
		!strings.Contains(string(store.run.OutputSummary), "idempotencyKeySha256") {
		t.Fatalf("unsafe cancellation summary=%s", store.run.OutputSummary)
	}

	repeated, err := service.Cancel(context.Background(), input)
	if err != nil || !repeated.Idempotent || repeated.Run.ID != cancelled.Run.ID ||
		lifecycle.calls != 1 || runtime.calls != 1 {
		t.Fatalf("repeated=%+v err=%v lifecycle=%d runtime=%d",
			repeated, err, lifecycle.calls, runtime.calls)
	}

	for _, status := range []string{"SUCCEEDED", "FAILED", "WAITING_CONFIRMATION"} {
		terminal := store.run
		terminal.Status = status
		store.run = terminal
		if _, err := service.Cancel(context.Background(), input); !errors.Is(err, ErrRunNotCancellable) {
			t.Fatalf("terminal %s error=%v", status, err)
		}
	}

	store.run.Status = "RUNNING"
	store.run.FinishedAt = nil
	other := input
	other.Principal.PrincipalID = "e51f1f2e-7b5a-7c3d-8e9f-123456789099"
	other.Authorization.Snapshot.SubjectID = other.Principal.PrincipalID
	if _, err := service.Cancel(context.Background(), other); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("other Subject error=%v", err)
	}

	if _, err := NewRunCancellationService(nil, lifecycle, runtime); err == nil {
		t.Fatal("expected nil Run store rejection")
	}
}

func validRunCancellationInput(t *testing.T) CancelRunInput {
	t.Helper()
	created := validRunServiceInput()
	created.Authorization.Snapshot.Action = agentaccessauth.ActionRunCancel
	created.Authorization.Snapshot.RequiredScope = "run:cancel"
	created.Authorization.Snapshot.TokenScopes = []string{"run:cancel"}
	created.Authorization.Snapshot.GrantScopes = []string{"run:cancel"}
	created.Authorization.Snapshot.AgentPolicyScopes = []string{"run:cancel"}
	created.Authorization.Snapshot.EffectiveScopes = []string{"run:cancel"}
	created.Authorization.Snapshot.ResourceType = agentaccessauth.ResourceRun
	created.Authorization.Snapshot.ResourceID = "e51f1f2e-7b5a-7c3d-8e9f-123456789010"
	created.Authorization.EffectiveScopes = []string{"run:cancel"}
	return CancelRunInput{
		Scope: created.Scope, RunID: created.Authorization.Snapshot.ResourceID,
		IdempotencyKey: testRunKey, Principal: created.Principal,
		Authorization: created.Authorization,
	}
}

type runCancellationStore struct {
	run execution.AgentRun
}

func (store *runCancellationStore) GetAgentRun(
	_ context.Context,
	workspaceID, runID string,
) (execution.AgentRun, error) {
	if store.run.WorkspaceID != workspaceID || store.run.ID != runID {
		return execution.AgentRun{}, execution.ErrRunNotFound
	}
	return store.run, nil
}

type runCancellationLifecycle struct {
	store *runCancellationStore
	calls int
}

func (lifecycle *runCancellationLifecycle) TransitionAgentRun(
	_ context.Context,
	input execution.ProtocolRunTransitionInput,
) (execution.ProtocolRunLifecycleResult, error) {
	lifecycle.calls++
	if lifecycle.store.run.Status != input.Transition.ExpectedStatus ||
		lifecycle.store.run.LockVersion != input.Transition.ExpectedLockVersion {
		return execution.ProtocolRunLifecycleResult{}, execution.ErrRunConflict
	}
	lifecycle.store.run.Status = input.Transition.NewStatus
	lifecycle.store.run.LockVersion++
	lifecycle.store.run.OutputSummary = append(json.RawMessage(nil), input.Transition.OutputSummary...)
	finished := time.Now().UTC()
	lifecycle.store.run.FinishedAt = &finished
	event := protocolevent.ProtocolEvent{Type: protocolevent.EventRunCancelled, Sequence: 3}
	return execution.ProtocolRunLifecycleResult{
		Run: lifecycle.store.run, Events: []protocolevent.ProtocolEvent{event},
	}, nil
}

type runCancellationRuntime struct {
	lifecycle                *runCancellationLifecycle
	calls                    int
	sawCommittedCancellation bool
}

func (runtime *runCancellationRuntime) CancelRun(_, _ string) error {
	runtime.calls++
	runtime.sawCommittedCancellation = runtime.lifecycle.store.run.Status == "CANCELLED"
	return nil
}
