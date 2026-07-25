package application

import (
	"context"
	"slices"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
)

func TestAuthorizationPolicyScopesDoNotInheritWorkspaceMembership(t *testing.T) {
	principal := agentaccessauth.AAPAccessTokenPrincipal{
		PrincipalID:        "a2000000-0000-4000-8000-000000000005",
		ServicePrincipalID: "a2000000-0000-4000-8000-000000000005",
	}
	withoutServiceDecision := agentAccessPolicyScopes(principal, agentaccess.GrantPolicy{})
	if slices.Contains(withoutServiceDecision, string(agentaccess.ScopeInteractionDecide)) {
		t.Fatalf("pure Service Principal inherited decision permission: %v", withoutServiceDecision)
	}
	if !slices.Contains(withoutServiceDecision, string(agentaccess.ScopeRunCreate)) {
		t.Fatalf("ordinary Agent policy scopes were lost: %v", withoutServiceDecision)
	}

	enabled := agentAccessPolicyScopes(principal, agentaccess.GrantPolicy{
		ServiceDecision: &agentaccess.ServiceDecisionPolicy{Enabled: true, MaxRisk: "low"},
	})
	if !slices.Contains(enabled, string(agentaccess.ScopeInteractionDecide)) {
		t.Fatalf("explicit service-decision policy was ignored: %v", enabled)
	}

	// A future delegated external Subject is evaluated by Subject Ownership;
	// it is not made equivalent to the Client's pure Service Principal policy.
	principal.PrincipalID = "a2000000-0000-4000-8000-000000000006"
	delegated := agentAccessPolicyScopes(principal, agentaccess.GrantPolicy{})
	if !slices.Contains(delegated, string(agentaccess.ScopeInteractionDecide)) {
		t.Fatalf("delegated Subject was incorrectly treated as pure service principal: %v", delegated)
	}
}

func TestAuthorizationSubjectSharingComesOnlyFromExplicitGrantPolicy(t *testing.T) {
	if resources := agentAccessSubjectSharingResources(agentaccess.GrantPolicy{}); len(resources) != 0 {
		t.Fatalf("missing Subject Sharing policy enabled resources: %v", resources)
	}
	if resources := agentAccessSubjectSharingResources(agentaccess.GrantPolicy{
		SubjectSharing: &agentaccess.SubjectSharingPolicy{Enabled: false},
	}); len(resources) != 0 {
		t.Fatalf("disabled Subject Sharing policy enabled resources: %v", resources)
	}
	policy := agentaccess.GrantPolicy{SubjectSharing: &agentaccess.SubjectSharingPolicy{
		Enabled: true,
		Resources: []agentaccess.SubjectSharingResource{
			agentaccess.SubjectSharingConversation, agentaccess.SubjectSharingArtifact,
		},
	}}
	resources := agentAccessSubjectSharingResources(policy)
	if !slices.Equal(resources, []string{"conversation", "artifact"}) {
		t.Fatalf("explicit Subject Sharing resources=%v", resources)
	}
	resources[0] = "run"
	if policy.SubjectSharing.Resources[0] != agentaccess.SubjectSharingConversation {
		t.Fatal("authorization state mutated the persisted Grant policy")
	}
}

func TestAgentAccessSecurityPublisherDrivesStreamNotification(t *testing.T) {
	source := agentaccessauth.NewInProcessSecurityChanges()
	t.Cleanup(func() { _ = source.Close() })
	cache, err := agentaccessauth.NewSecurityVersionCache(30 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	binding := agentaccessauth.StreamBinding{
		WorkspaceID:     "a2000000-0000-4000-8000-000000000001",
		AgentID:         "a2000000-0000-4000-8000-000000000002",
		ClientID:        "a2000000-0000-4000-8000-000000000003",
		GrantID:         "a2000000-0000-4000-8000-000000000004",
		PrincipalID:     "a2000000-0000-4000-8000-000000000005",
		SubjectID:       "a2000000-0000-4000-8000-000000000006",
		SecurityVersion: 1, TokenExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	store := applicationStreamAuthorizationStore{state: agentaccessauth.StreamAuthorizationState{
		WorkspaceID: binding.WorkspaceID, AgentID: binding.AgentID,
		ClientID: binding.ClientID, GrantID: binding.GrantID,
		ServicePrincipalID: binding.PrincipalID, SecurityVersion: binding.SecurityVersion,
	}}
	streamAuthorizer, err := agentaccessauth.NewCachedStreamAuthorizer(store, cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := streamAuthorizer.Reauthorize(context.Background(), binding, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	subscription, err := source.Subscribe(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = subscription.Close() })
	publisher := agentAccessSecurityPublisher{source: source, cache: cache}
	if err := publisher.PublishAgentAccessSecurityChange(context.Background(), agentaccess.SecurityChangeEvent{
		WorkspaceID: binding.WorkspaceID, AgentID: binding.AgentID,
		ClientID: binding.ClientID, GrantID: binding.GrantID, SecurityVersion: 2,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case change := <-subscription.Changes():
		if change.SecurityVersion != 2 || change.GrantID != binding.GrantID {
			t.Fatalf("security change=%+v", change)
		}
	case <-time.After(time.Second):
		t.Fatal("security change did not reach the SSE revalidation source")
	}
	store.state.SecurityVersion = 2
	// The production publisher invalidates before sending the wakeup. A second
	// authority check therefore cannot reuse the old cached version.
	streamAuthorizer, _ = agentaccessauth.NewCachedStreamAuthorizer(store, cache)
	if err := streamAuthorizer.Reauthorize(context.Background(), binding, time.Now().UTC()); err != agentaccessauth.ErrSecurityVersionChanged {
		t.Fatalf("publisher did not invalidate Security Version cache: %v", err)
	}
	if cache.Stats().Invalidations != 1 {
		t.Fatalf("cache stats=%+v", cache.Stats())
	}
}

type applicationStreamAuthorizationStore struct {
	state agentaccessauth.StreamAuthorizationState
}

func (store applicationStreamAuthorizationStore) ResolveStreamAuthorizationState(
	context.Context,
	agentaccessauth.StreamBinding,
	time.Time,
) (agentaccessauth.StreamAuthorizationState, error) {
	return store.state, nil
}
