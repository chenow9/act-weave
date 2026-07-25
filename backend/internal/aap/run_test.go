package aap

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
)

const (
	testRunWorkspaceID    = "e51f1f2e-7b5a-7c3d-8e9f-123456789001"
	testRunAgentID        = "e51f1f2e-7b5a-7c3d-8e9f-123456789002"
	testRunServiceID      = "e51f1f2e-7b5a-7c3d-8e9f-123456789003"
	testRunSubjectID      = "e51f1f2e-7b5a-7c3d-8e9f-123456789004"
	testRunClientID       = "e51f1f2e-7b5a-7c3d-8e9f-123456789005"
	testRunGrantID        = "e51f1f2e-7b5a-7c3d-8e9f-123456789006"
	testRunTokenID        = "e51f1f2e-7b5a-7c3d-8e9f-123456789007"
	testRunConversationID = "e51f1f2e-7b5a-7c3d-8e9f-123456789008"
	testRunKey            = "e51f1f2e-7b5a-7c3d-8e9f-123456789009"
)

func TestRunServiceCreatesAndRecoversOneDurableRun(t *testing.T) {
	store := &runServiceStore{}
	events := &runServiceEvents{}
	lifecycle := &runServiceLifecycle{events: events}
	dispatcher := &runServiceDispatcher{lifecycle: lifecycle}
	service, err := NewRunService(store, store, lifecycle, events, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	input := validRunServiceInput()

	created, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if created.Idempotent || created.Run.ID == "" || created.AcceptedEvent.Sequence != 1 ||
		store.sendCalls != 1 || lifecycle.calls != 1 || dispatcher.calls != 1 ||
		!dispatcher.sawCommittedEvents {
		t.Fatalf("created=%+v send=%d lifecycle=%d dispatch=%d committed=%v",
			created, store.sendCalls, lifecycle.calls, dispatcher.calls, dispatcher.sawCommittedEvents)
	}
	if store.sent.RunID != created.Run.ID || store.sent.MessageID == "" ||
		store.sent.PrincipalSnapshot == nil || len(store.sent.AuthorizationSnapshot) == 0 ||
		store.run.PrincipalSnapshot.Identity.Actor.Type != "SERVICE_PRINCIPAL" ||
		store.run.PrincipalSnapshot.Identity.Actor.ID != testRunServiceID ||
		store.run.PrincipalSnapshot.Identity.Subject == nil ||
		store.run.PrincipalSnapshot.Identity.Subject.Type != "EXTERNAL_SUBJECT" ||
		store.run.PrincipalSnapshot.Identity.Subject.ID != testRunSubjectID {
		t.Fatalf("persisted input=%+v run=%+v", store.sent, store.run)
	}

	replayed, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Idempotent || replayed.Run.ID != created.Run.ID ||
		store.sendCalls != 1 || lifecycle.calls != 1 || dispatcher.calls != 1 {
		t.Fatalf("replay=%+v send=%d lifecycle=%d dispatch=%d",
			replayed, store.sendCalls, lifecycle.calls, dispatcher.calls)
	}

	changed := input
	changed.Text = "different request"
	if _, err := service.Create(context.Background(), changed); !errors.Is(err, ErrRunIdempotencyConflict) {
		t.Fatalf("changed request error=%v", err)
	}
	if store.sendCalls != 1 || dispatcher.calls != 1 {
		t.Fatalf("conflict performed side effects: send=%d dispatch=%d",
			store.sendCalls, dispatcher.calls)
	}

	// Simulate a process interruption after Chat atomically committed the Run,
	// but before the initial Protocol Event unit of work returned.
	events.values = nil
	recovered, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.Idempotent || recovered.Run.ID != created.Run.ID ||
		store.sendCalls != 1 || lifecycle.calls != 2 || dispatcher.calls != 2 {
		t.Fatalf("recovered=%+v send=%d lifecycle=%d dispatch=%d",
			recovered, store.sendCalls, lifecycle.calls, dispatcher.calls)
	}

	if _, err := NewRunService(nil, store, lifecycle, events, dispatcher); err == nil {
		t.Fatal("expected nil Message starter rejection")
	}
}

func validRunServiceInput() CreateRunInput {
	now := time.Now().UTC()
	principal := agentaccessauth.AAPAccessTokenPrincipal{
		PrincipalID: testRunSubjectID, ServicePrincipalID: testRunServiceID,
		AuthorizedParty: "awcl_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		WorkspaceID:     testRunWorkspaceID, AgentID: testRunAgentID,
		Scopes: []string{"run:create"}, SecurityVersion: 1, TokenID: testRunTokenID,
		IssuedAt: now.Add(-time.Minute), NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute),
	}
	snapshot := agentaccessauth.AAPAuthorizationSnapshot{
		SpecVersion: "aap.authorization.v1", WorkspaceID: testRunWorkspaceID,
		AgentID: testRunAgentID, ClientID: testRunClientID,
		AuthorizedParty: principal.AuthorizedParty, ServicePrincipalID: testRunServiceID,
		SubjectID: testRunSubjectID, GrantID: testRunGrantID,
		Action: agentaccessauth.ActionRunCreate, RequiredScope: "run:create",
		TokenScopes: []string{"run:create"}, GrantScopes: []string{"run:create"},
		AgentPolicyScopes: []string{"run:create"}, EffectiveScopes: []string{"run:create"},
		TokenSecurityVersion: 1, ResolvedSecurityVersion: 1,
		WorkspaceVersion: 1, ClientVersion: 1, GrantVersion: 2, AgentPolicyVersion: 3,
		TokenID: testRunTokenID, ResourceType: agentaccessauth.ResourceConversation,
		ResourceID: testRunConversationID, OwnershipMode: "SUBJECT_OWNED",
		OwnershipPolicyVersion: 3, AuthorizedAt: now,
	}
	return CreateRunInput{
		Scope:          ConversationScope{WorkspaceID: testRunWorkspaceID, AgentID: testRunAgentID},
		ConversationID: testRunConversationID, Text: "hello",
		Metadata: map[string]string{"request": "one"}, IdempotencyKey: testRunKey,
		TraceID: "trace-run-service", Principal: principal,
		Authorization: agentaccessauth.AAPAuthorizationDecision{
			EffectiveScopes: []string{"run:create"}, Snapshot: snapshot,
		},
	}
}

type runServiceStore struct {
	run       execution.AgentRun
	sent      chat.SendMessageInput
	sendCalls int
}

func (store *runServiceStore) GetAgentRun(
	_ context.Context,
	workspaceID, runID string,
) (execution.AgentRun, error) {
	if store.run.ID == "" || store.run.WorkspaceID != workspaceID || store.run.ID != runID {
		return execution.AgentRun{}, execution.ErrRunNotFound
	}
	return store.run, nil
}

func (store *runServiceStore) SendMessage(
	_ context.Context,
	input chat.SendMessageInput,
) (chat.SendMessageResult, error) {
	store.sendCalls++
	store.sent = input
	if input.PrincipalSnapshot == nil {
		return chat.SendMessageResult{}, chat.ErrInvalid
	}
	started := time.Now().UTC().Truncate(time.Millisecond)
	store.run = execution.AgentRun{
		ID: input.RunID, WorkspaceID: input.WorkspaceID, AgentID: testRunAgentID,
		SessionID: input.SessionID, TriggerType: "API", Status: "RUNNING",
		TriggeredByType: "SERVICE_PRINCIPAL", TriggeredByID: testRunServiceID,
		TraceID: input.TraceID, PrincipalSnapshot: *input.PrincipalSnapshot,
		InputSummary:          append(json.RawMessage(nil), input.RunInputSummary...),
		AuthorizationSnapshot: append(json.RawMessage(nil), input.AuthorizationSnapshot...),
		StartedAt:             started, LockVersion: 1,
	}
	return chat.SendMessageResult{
		Session: chat.Session{ID: input.SessionID},
		Message: chat.Message{ID: input.MessageID, RunID: input.RunID},
	}, nil
}

type runServiceEvents struct {
	values []protocolevent.ProtocolEvent
}

func (events *runServiceEvents) ReadRunAfter(
	_ context.Context,
	_ protocolevent.RunScope,
	_ int64,
	_ int,
) ([]protocolevent.ProtocolEvent, error) {
	if len(events.values) == 0 {
		return nil, protocolevent.ErrRunScopeNotFound
	}
	return append([]protocolevent.ProtocolEvent(nil), events.values...), nil
}

type runServiceLifecycle struct {
	events *runServiceEvents
	calls  int
}

func (lifecycle *runServiceLifecycle) RecordStartedAgentRun(
	_ context.Context,
	run execution.AgentRun,
) (execution.ProtocolRunLifecycleResult, error) {
	lifecycle.calls++
	accepted := protocolevent.ProtocolEvent{
		ID: "e51f1f2e-7b5a-7c3d-8e9f-12345678900a", EventStreamID: run.ID,
		WorkspaceID: run.WorkspaceID, AgentID: run.AgentID, ConversationID: run.SessionID,
		RunID: run.ID, Type: protocolevent.EventRunAccepted, Sequence: 1,
	}
	started := accepted
	started.ID = "e51f1f2e-7b5a-7c3d-8e9f-12345678900b"
	started.Type = protocolevent.EventRunStarted
	started.Sequence = 2
	lifecycle.events.values = []protocolevent.ProtocolEvent{accepted, started}
	return execution.ProtocolRunLifecycleResult{
		Run: run, Events: append([]protocolevent.ProtocolEvent(nil), lifecycle.events.values...),
	}, nil
}

type runServiceDispatcher struct {
	lifecycle          *runServiceLifecycle
	calls              int
	sawCommittedEvents bool
}

func (dispatcher *runServiceDispatcher) DispatchRun(RunDispatch) error {
	dispatcher.calls++
	dispatcher.sawCommittedEvents = len(dispatcher.lifecycle.events.values) >= 2
	return nil
}
