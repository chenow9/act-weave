package agentaccessauth

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAuthorizationActionMatrixCoversEveryAAPV1Scope(t *testing.T) {
	matrix := AAPActionMatrix()
	if len(matrix) != len(canonicalAAPScopes) {
		t.Fatalf("action matrix size=%d scopes=%d", len(matrix), len(canonicalAAPScopes))
	}
	want := map[AAPAction]AAPActionRule{
		ActionAgentProfileRead:   {RequiredScope: "agent:read", ConcealDenial: true},
		ActionConversationCreate: {RequiredScope: "conversation:create"},
		ActionConversationRead: {
			RequiredScope: "conversation:read", ResourceType: ResourceConversation,
			OwnershipRequired: true, ConcealDenial: true,
		},
		ActionRunCreate: {
			RequiredScope: "run:create", ResourceType: ResourceConversation,
			OwnershipRequired: true, ConcealDenial: true,
		},
		ActionRunRead: {
			RequiredScope: "run:read", ResourceType: ResourceRun,
			OwnershipRequired: true, ConcealDenial: true,
		},
		ActionRunCancel: {
			RequiredScope: "run:cancel", ResourceType: ResourceRun,
			OwnershipRequired: true, ConcealDenial: true,
		},
		ActionEventRead: {
			RequiredScope: "event:read", ResourceType: ResourceRun,
			OwnershipRequired: true, ConcealDenial: true,
		},
		ActionInteractionDecide: {
			RequiredScope: "interaction:decide", ResourceType: ResourceInteraction,
			OwnershipRequired: true, ConcealDenial: true,
		},
		ActionArtifactRead: {
			RequiredScope: "artifact:read", ResourceType: ResourceArtifact,
			OwnershipRequired: true, ConcealDenial: true,
		},
	}
	if !reflect.DeepEqual(matrix, want) {
		t.Fatalf("unexpected AAP action matrix:\ngot=%+v\nwant=%+v", matrix, want)
	}
	delete(matrix, ActionRunRead)
	if len(AAPActionMatrix()) != len(want) {
		t.Fatal("callers must not mutate the shared AAP action matrix")
	}
}

func TestAuthorizationScopeIntersectionProducesImmutableRunSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	principal := authorizationTestPrincipal(now)
	state := authorizationTestState(principal)
	state.GrantScopes = []string{"event:read", "run:create", "run:read", "interaction:decide"}
	state.AgentPolicyScopes = []string{"interaction:decide", "event:read", "run:read"}
	states := &authorizationStateStoreStub{state: state}
	ownership := &subjectOwnershipStub{decision: SubjectOwnershipDecision{
		Mode: "SUBJECT_OWNED", OwnerID: principal.PrincipalID, PolicyVersion: 3,
	}}
	audit := &authorizationAuditStub{}
	service, err := NewAAPAuthorizationService(states, ownership, WithAAPAuthorizationAudit(audit))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	resource := AAPAuthorizationResource{
		Type: ResourceRun, ID: "a68f1f2e-7b5a-7c3d-8e9f-123456789010",
	}
	decision, err := service.Authorize(context.Background(), AAPAuthorizationRequest{
		Principal: principal, Action: ActionRunRead, Resource: resource,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantEffective := []string{"run:read", "event:read", "interaction:decide"}
	if !reflect.DeepEqual(decision.EffectiveScopes, wantEffective) || ownership.calls != 1 ||
		len(audit.denials) != 0 {
		t.Fatalf("decision=%+v ownership calls=%d audit=%+v", decision, ownership.calls, audit.denials)
	}
	snapshot := decision.Snapshot
	if snapshot.SpecVersion != "aap.authorization.v1" || snapshot.ClientID != state.ClientID ||
		snapshot.GrantID != state.GrantID || snapshot.Action != ActionRunRead ||
		snapshot.RequiredScope != "run:read" || snapshot.OwnershipMode != "SUBJECT_OWNED" ||
		snapshot.OwnershipPolicyVersion != 3 || snapshot.TokenSecurityVersion != principal.SecurityVersion ||
		snapshot.ResolvedSecurityVersion != state.CurrentSecurityVersion ||
		!snapshot.AuthorizedAt.Equal(now) || !reflect.DeepEqual(snapshot.EffectiveScopes, wantEffective) {
		t.Fatalf("unexpected authorization snapshot: %+v", snapshot)
	}
	raw, err := snapshot.JSON()
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	for _, required := range []string{state.WorkspaceID, state.AgentID, state.ClientID, state.GrantID, "aap.authorization.v1"} {
		if !strings.Contains(serialized, required) {
			t.Fatalf("snapshot missing %q: %s", required, serialized)
		}
	}
	for _, forbidden := range []string{"accessToken", "Authorization", "Bearer ", "workspaceRole", "memberRole"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("snapshot contains forbidden %q: %s", forbidden, serialized)
		}
	}
	decision.EffectiveScopes[0] = "mutated"
	if snapshot.EffectiveScopes[0] != "run:read" {
		t.Fatalf("decision and Run snapshot share mutable Scope backing storage: %+v", snapshot)
	}
}

func TestAuthorizationRejectsEachIntersectionAndOwnershipBoundary(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	basePrincipal := authorizationTestPrincipal(now)
	baseState := authorizationTestState(basePrincipal)
	resource := AAPAuthorizationResource{
		Type: ResourceRun, ID: "a68f1f2e-7b5a-7c3d-8e9f-123456789011",
	}
	tests := map[string]struct {
		action AAPAction
		mutate func(*AAPAccessTokenPrincipal, *authorizationStateStoreStub, *subjectOwnershipStub)
		want   error
		reason string
	}{
		"Token Scope": {action: ActionRunRead, mutate: func(principal *AAPAccessTokenPrincipal, _ *authorizationStateStoreStub, _ *subjectOwnershipStub) {
			principal.Scopes = []string{"run:create", "event:read", "interaction:decide"}
		}, want: ErrAAPAuthorizationNotVisible, reason: "TOKEN_SCOPE_MISSING"},
		"current Grant Scope": {action: ActionRunRead, mutate: func(_ *AAPAccessTokenPrincipal, store *authorizationStateStoreStub, _ *subjectOwnershipStub) {
			store.state.GrantScopes = []string{"run:create", "event:read", "interaction:decide"}
		}, want: ErrAAPAuthorizationNotVisible, reason: "GRANT_SCOPE_MISSING"},
		"Agent Policy": {action: ActionRunRead, mutate: func(_ *AAPAccessTokenPrincipal, store *authorizationStateStoreStub, _ *subjectOwnershipStub) {
			store.state.AgentPolicyScopes = []string{"run:create", "event:read", "interaction:decide"}
		}, want: ErrAAPAuthorizationNotVisible, reason: "AGENT_POLICY_DENIED"},
		"Subject Ownership": {action: ActionRunRead, mutate: func(_ *AAPAccessTokenPrincipal, _ *authorizationStateStoreStub, ownership *subjectOwnershipStub) {
			ownership.err = ErrSubjectOwnershipNotFound
		}, want: ErrAAPAuthorizationNotVisible, reason: "SUBJECT_OWNERSHIP_DENIED"},
		"inactive binding": {action: ActionRunRead, mutate: func(_ *AAPAccessTokenPrincipal, store *authorizationStateStoreStub, _ *subjectOwnershipStub) {
			store.err = ErrAAPAuthorizationStateNotFound
		}, want: ErrAAPAuthorizationNotVisible, reason: "BINDING_NOT_VISIBLE"},
		"mismatched Workspace": {action: ActionRunRead, mutate: func(_ *AAPAccessTokenPrincipal, store *authorizationStateStoreStub, _ *subjectOwnershipStub) {
			store.state.WorkspaceID = "a68f1f2e-7b5a-7c3d-8e9f-123456789099"
		}, want: ErrAAPAuthorizationNotVisible, reason: "BINDING_NOT_VISIBLE"},
		"creation forbidden": {action: ActionConversationCreate, mutate: func(principal *AAPAccessTokenPrincipal, _ *authorizationStateStoreStub, _ *subjectOwnershipStub) {
			principal.Scopes = []string{"run:create", "run:read", "event:read", "interaction:decide"}
		}, want: ErrAAPAuthorizationDenied, reason: "TOKEN_SCOPE_MISSING"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			principal := basePrincipal
			principal.Scopes = append([]string(nil), basePrincipal.Scopes...)
			store := &authorizationStateStoreStub{state: baseState}
			store.state.GrantScopes = append([]string(nil), baseState.GrantScopes...)
			store.state.AgentPolicyScopes = append([]string(nil), baseState.AgentPolicyScopes...)
			ownership := &subjectOwnershipStub{decision: SubjectOwnershipDecision{
				Mode: "SUBJECT_OWNED", OwnerID: principal.PrincipalID, PolicyVersion: 1,
			}}
			audit := &authorizationAuditStub{}
			service, err := NewAAPAuthorizationService(store, ownership, WithAAPAuthorizationAudit(audit))
			if err != nil {
				t.Fatal(err)
			}
			service.now = func() time.Time { return now }
			test.mutate(&principal, store, ownership)
			requestResource := resource
			if test.action == ActionConversationCreate {
				requestResource = AAPAuthorizationResource{}
			}
			_, err = service.Authorize(context.Background(), AAPAuthorizationRequest{
				Principal: principal, Action: test.action, Resource: requestResource,
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
			var denial *AAPAuthorizationError
			if !errors.As(err, &denial) || denial.Reason != test.reason || len(audit.denials) != 1 ||
				audit.denials[0].Reason != test.reason {
				t.Fatalf("denial=%+v audit=%+v err=%v", denial, audit.denials, err)
			}
		})
	}
}

func TestAuthorizationFailsClosedOnResolverAndAuditFailures(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	principal := authorizationTestPrincipal(now)
	resource := AAPAuthorizationResource{
		Type: ResourceRun, ID: "a68f1f2e-7b5a-7c3d-8e9f-123456789012",
	}
	for name, test := range map[string]struct {
		store     *authorizationStateStoreStub
		ownership *subjectOwnershipStub
	}{
		"state resolver": {
			store:     &authorizationStateStoreStub{err: errors.New("database unavailable")},
			ownership: &subjectOwnershipStub{},
		},
		"ownership resolver": {
			store:     &authorizationStateStoreStub{state: authorizationTestState(principal)},
			ownership: &subjectOwnershipStub{err: errors.New("ownership unavailable")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			service, err := NewAAPAuthorizationService(test.store, test.ownership)
			if err != nil {
				t.Fatal(err)
			}
			service.now = func() time.Time { return now }
			if _, err := service.Authorize(context.Background(), AAPAuthorizationRequest{
				Principal: principal, Action: ActionRunRead, Resource: resource,
			}); !errors.Is(err, ErrAAPAuthorizationUnavailable) {
				t.Fatalf("error=%v want unavailable", err)
			}
		})
	}

	audit := &authorizationAuditStub{err: errors.New("audit unavailable")}
	service, err := NewAAPAuthorizationService(
		&authorizationStateStoreStub{state: authorizationTestState(principal)},
		&subjectOwnershipStub{}, WithAAPAuthorizationAudit(audit),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	principal.Scopes = []string{"run:create"}
	_, err = service.Authorize(context.Background(), AAPAuthorizationRequest{
		Principal: principal, Action: ActionRunRead, Resource: resource,
	})
	if !errors.Is(err, ErrAAPAuthorizationNotVisible) || !errors.Is(err, audit.err) {
		t.Fatalf("denial must survive joined Audit failure: %v", err)
	}
}

func TestAuthorizationCreationDoesNotInvokeSubjectOwnershipResolver(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	principal := authorizationTestPrincipal(now)
	ownership := &subjectOwnershipStub{err: errors.New("must not be called")}
	service, err := NewAAPAuthorizationService(
		&authorizationStateStoreStub{state: authorizationTestState(principal)}, ownership,
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	if _, err := service.Authorize(context.Background(), AAPAuthorizationRequest{
		Principal: principal, Action: ActionConversationCreate,
	}); err != nil || ownership.calls != 0 {
		t.Fatalf("new Conversation authorization err=%v ownership calls=%d", err, ownership.calls)
	}
}

type authorizationStateStoreStub struct {
	state AAPAuthorizationState
	err   error
}

func (store *authorizationStateStoreStub) ResolveAAPAuthorizationState(
	context.Context,
	AAPAccessTokenPrincipal,
	time.Time,
) (AAPAuthorizationState, error) {
	return store.state, store.err
}

type subjectOwnershipStub struct {
	decision SubjectOwnershipDecision
	err      error
	calls    int
}

func (stub *subjectOwnershipStub) ResolveSubjectOwnership(
	context.Context,
	AAPAccessTokenPrincipal,
	AAPAuthorizationState,
	AAPAction,
	AAPAuthorizationResource,
) (SubjectOwnershipDecision, error) {
	stub.calls++
	return stub.decision, stub.err
}

type authorizationAuditStub struct {
	denials []AAPAuthorizationDenial
	err     error
}

func (stub *authorizationAuditStub) RecordAAPAuthorizationDenied(
	_ context.Context,
	denial AAPAuthorizationDenial,
) error {
	stub.denials = append(stub.denials, denial)
	return stub.err
}

func authorizationTestPrincipal(now time.Time) AAPAccessTokenPrincipal {
	return AAPAccessTokenPrincipal{
		PrincipalID:        "a68f1f2e-7b5a-7c3d-8e9f-123456789001",
		ServicePrincipalID: "a68f1f2e-7b5a-7c3d-8e9f-123456789001",
		AuthorizedParty:    clientSecretTestPublicID(101),
		WorkspaceID:        "a68f1f2e-7b5a-7c3d-8e9f-123456789002",
		AgentID:            "a68f1f2e-7b5a-7c3d-8e9f-123456789003",
		Scopes: []string{
			"conversation:create", "run:create", "run:read", "event:read", "interaction:decide",
		},
		SecurityVersion: 7, TokenID: "a68f1f2e-7b5a-7c3d-8e9f-123456789004",
		IssuedAt: now, NotBefore: now.Add(-DefaultTokenClockSkew), ExpiresAt: now.Add(10 * time.Minute),
	}
}

func authorizationTestState(principal AAPAccessTokenPrincipal) AAPAuthorizationState {
	return AAPAuthorizationState{
		WorkspaceID: principal.WorkspaceID, AgentID: principal.AgentID,
		ClientID:       "a68f1f2e-7b5a-7c3d-8e9f-123456789005",
		PublicClientID: principal.AuthorizedParty, ServicePrincipalID: principal.ServicePrincipalID,
		CurrentSecurityVersion: principal.SecurityVersion,
		GrantID:                "a68f1f2e-7b5a-7c3d-8e9f-123456789006",
		GrantScopes: []string{
			"conversation:create", "run:create", "run:read", "event:read", "interaction:decide",
		},
		AgentPolicyScopes: []string{
			"agent:read", "conversation:create", "conversation:read", "run:create", "run:read",
			"run:cancel", "event:read", "interaction:decide", "artifact:read",
		},
		WorkspaceVersion: 2, ClientVersion: 3, GrantVersion: 4, AgentPolicyVersion: 5,
	}
}

func TestAuthorizationDenialDTOContainsNoTokenMaterial(t *testing.T) {
	value, err := json.Marshal(AAPAuthorizationDenial{
		WorkspaceID:        "a68f1f2e-7b5a-7c3d-8e9f-123456789002",
		AgentID:            "a68f1f2e-7b5a-7c3d-8e9f-123456789003",
		ServicePrincipalID: "a68f1f2e-7b5a-7c3d-8e9f-123456789001",
		AuthorizedParty:    clientSecretTestPublicID(101), Action: ActionRunRead,
		RequiredScope: "run:read", Reason: "TOKEN_SCOPE_MISSING",
		ResourceType: ResourceRun, ResourceID: "a68f1f2e-7b5a-7c3d-8e9f-123456789010",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"accessToken", "Bearer ", "clientSecret", "authorization"} {
		if strings.Contains(string(value), forbidden) {
			t.Fatalf("authorization denial leaked %q: %s", forbidden, value)
		}
	}
}
