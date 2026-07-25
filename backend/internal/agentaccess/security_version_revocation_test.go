package agentaccess_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

func TestSecurityVersionRevocationRepositoryAndManagementIntegration(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertRepositoryFixtures(t, db)
	repository, err := agentaccess.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := agentaccessauth.NewSecurityVersionCache(30 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	changes := agentaccessauth.NewInProcessSecurityChanges()
	publisher := &revocationSecurityPublisher{cache: cache, changes: changes}
	management, err := agentaccess.NewManagementService(
		repository, bytes.Repeat([]byte{0x6a}, 32),
		agentaccess.WithSecurityChangePublisher(publisher),
	)
	if err != nil {
		t.Fatal(err)
	}
	store := revocationStreamStore{repository: repository}
	streamAuthorizer, err := agentaccessauth.NewCachedStreamAuthorizer(store, cache)
	if err != nil {
		t.Fatal(err)
	}
	revalidator, err := agentaccessauth.NewStreamRevalidator(
		streamAuthorizer, changes, agentaccessauth.DefaultRevalidationPolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	registration, grant := registerRevocationClient(t, management, "Credential revocation Client")
	binding := revocationBinding(registration, grant, 1)
	if err := streamAuthorizer.Reauthorize(ctx, binding, time.Now().UTC()); err != nil {
		t.Fatalf("initial Stream authorization: %v", err)
	}
	if _, err := repository.ResolveAAPStreamAuthorizationState(
		ctx, repositoryWorkspaceID, repositoryOtherAgentID, registration.Client.ID,
		grant.ID, registration.Principal.ID, time.Now().UTC(),
	); !errors.Is(err, agentaccess.ErrRepositoryNotFound) {
		t.Fatalf("cross-Agent Stream state err=%v", err)
	}

	credentialResult := monitorRevocation(t, revalidator, changes, binding, func() {
		issued, err := management.AddCredential(ctx, agentaccess.AddCredentialInput{
			WorkspaceID: repositoryWorkspaceID, ClientID: registration.Client.ID,
			ActorID: repositoryOwnerID, Type: agentaccess.CredentialTypeClientSecret,
			ReplacesCredentialID:        registration.Credential.ID,
			ReplacesExpectedLockVersion: registration.Credential.LockVersion,
			Overlap:                     time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
		old, err := repository.GetCredential(
			ctx, repositoryWorkspaceID, registration.Client.ID, registration.Credential.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		_, principal, err := management.RevokeCredential(
			ctx, repositoryWorkspaceID, registration.Client.ID, old.ID,
			repositoryOwnerID, old.LockVersion,
		)
		if err != nil || principal.SecurityVersion != 2 || issued.Credential.ID == "" {
			t.Fatalf("Credential revoke principal=%+v issued=%+v err=%v", principal, issued, err)
		}
	})
	if !errors.Is(credentialResult, agentaccessauth.ErrSecurityVersionChanged) {
		t.Fatalf("Credential revoke Stream result=%v", credentialResult)
	}
	if err := streamAuthorizer.Reauthorize(ctx, binding, time.Now().UTC()); !errors.Is(err, agentaccessauth.ErrSecurityVersionChanged) {
		t.Fatalf("old Token survived Credential revoke: %v", err)
	}
	fresh := binding
	fresh.SecurityVersion = 2
	if err := streamAuthorizer.Reauthorize(ctx, fresh, time.Now().UTC()); err != nil {
		t.Fatalf("fresh Token could not recover after Credential rotation: %v", err)
	}

	grantResult := monitorRevocation(t, revalidator, changes, fresh, func() {
		revoked, principal, err := management.RevokeGrant(
			ctx, repositoryWorkspaceID, registration.Client.ID, repositoryAgentID,
			grant.ID, repositoryOwnerID, grant.LockVersion,
		)
		if err != nil || revoked.Status != agentaccess.GrantStatusRevoked || principal.SecurityVersion != 3 {
			t.Fatalf("Grant revoke=%+v principal=%+v err=%v", revoked, principal, err)
		}
	})
	if !errors.Is(grantResult, agentaccessauth.ErrSecurityVersionChanged) {
		t.Fatalf("Grant revoke Stream result=%v", grantResult)
	}
	if err := streamAuthorizer.Reauthorize(ctx, fresh, time.Now().UTC()); !errors.Is(err, agentaccessauth.ErrAuthorizationRevoked) {
		t.Fatalf("revoked Grant remained authorized: %v", err)
	}

	clientRegistration, clientGrant := registerRevocationClient(t, management, "Client disable Client")
	clientBinding := revocationBinding(clientRegistration, clientGrant, 1)
	clientResult := monitorRevocation(t, revalidator, changes, clientBinding, func() {
		disabled, principal, err := management.SetClientStatus(
			ctx, repositoryWorkspaceID, clientRegistration.Client.ID, repositoryOwnerID,
			agentaccess.StatusDisabled, clientRegistration.Client.LockVersion,
		)
		if err != nil || disabled.Status != agentaccess.StatusDisabled || principal.SecurityVersion != 2 {
			t.Fatalf("Client disable=%+v principal=%+v err=%v", disabled, principal, err)
		}
	})
	if !errors.Is(clientResult, agentaccessauth.ErrSecurityVersionChanged) {
		t.Fatalf("Client disable Stream result=%v", clientResult)
	}
	if err := streamAuthorizer.Reauthorize(ctx, clientBinding, time.Now().UTC()); !errors.Is(err, agentaccessauth.ErrAuthorizationRevoked) {
		t.Fatalf("disabled Client remained authorized: %v", err)
	}

	if publisher.events != 3 || cache.Stats().Invalidations < 3 ||
		changes.Stats().ActiveSubscriptions != 0 {
		t.Fatalf("revocation propagation events=%d cache=%+v source=%+v",
			publisher.events, cache.Stats(), changes.Stats())
	}
}

type revocationStreamStore struct{ repository *agentaccess.Repository }

func (store revocationStreamStore) ResolveStreamAuthorizationState(
	ctx context.Context,
	binding agentaccessauth.StreamBinding,
	at time.Time,
) (agentaccessauth.StreamAuthorizationState, error) {
	record, err := store.repository.ResolveAAPStreamAuthorizationState(
		ctx, binding.WorkspaceID, binding.AgentID, binding.ClientID,
		binding.GrantID, binding.PrincipalID, at,
	)
	if err != nil {
		if errors.Is(err, agentaccess.ErrRepositoryNotFound) {
			return agentaccessauth.StreamAuthorizationState{}, agentaccessauth.ErrStreamAuthorizationStateNotFound
		}
		return agentaccessauth.StreamAuthorizationState{}, err
	}
	return agentaccessauth.StreamAuthorizationState{
		WorkspaceID: record.WorkspaceID, AgentID: record.AgentID,
		ClientID: record.ClientID, GrantID: record.GrantID,
		ServicePrincipalID: record.ServicePrincipalID,
		SecurityVersion:    record.SecurityVersion,
	}, nil
}

type revocationSecurityPublisher struct {
	cache   *agentaccessauth.SecurityVersionCache
	changes *agentaccessauth.InProcessSecurityChanges
	events  int
}

func (publisher *revocationSecurityPublisher) PublishAgentAccessSecurityChange(
	_ context.Context,
	event agentaccess.SecurityChangeEvent,
) error {
	change := agentaccessauth.SecurityChange{
		WorkspaceID: event.WorkspaceID, AgentID: event.AgentID,
		ClientID: event.ClientID, GrantID: event.GrantID,
		SecurityVersion: event.SecurityVersion,
	}
	if err := publisher.cache.Invalidate(change); err != nil {
		return err
	}
	publisher.events++
	return publisher.changes.Publish(change)
}

func registerRevocationClient(
	t *testing.T,
	management *agentaccess.ManagementService,
	name string,
) (agentaccess.ClientRegistration, agentaccess.AgentGrant) {
	t.Helper()
	registration, err := management.RegisterClient(context.Background(), agentaccess.RegisterClientInput{
		WorkspaceID: repositoryWorkspaceID, Name: name,
		ActorID: repositoryOwnerID, AuthMethod: agentaccess.ClientAuthMethodSecretBasic,
		TokenTTLSeconds: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := management.GrantAgent(context.Background(), agentaccess.CreateGrantInput{
		ID: uuid.NewString(), WorkspaceID: repositoryWorkspaceID,
		ClientID: registration.Client.ID, AgentID: repositoryAgentID,
		Scopes: []agentaccess.AgentScope{
			agentaccess.ScopeConversationCreate, agentaccess.ScopeRunCreate,
			agentaccess.ScopeRunRead, agentaccess.ScopeEventRead,
		},
		Policy: agentaccess.GrantPolicy{}, ValidFrom: time.Now().UTC().Add(-time.Minute),
		ActorID: repositoryOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return registration, grant
}

func revocationBinding(
	registration agentaccess.ClientRegistration,
	grant agentaccess.AgentGrant,
	version int64,
) agentaccessauth.StreamBinding {
	return agentaccessauth.StreamBinding{
		WorkspaceID: repositoryWorkspaceID, AgentID: repositoryAgentID,
		ClientID: registration.Client.ID, GrantID: grant.ID,
		PrincipalID: registration.Principal.ID, SubjectID: registration.Principal.ID,
		SecurityVersion: version, TokenExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	}
}

func monitorRevocation(
	t *testing.T,
	revalidator *agentaccessauth.StreamRevalidator,
	changes *agentaccessauth.InProcessSecurityChanges,
	binding agentaccessauth.StreamBinding,
	revoke func(),
) error {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- revalidator.Monitor(context.Background(), binding) }()
	deadline := time.Now().Add(time.Second)
	for changes.Stats().ActiveSubscriptions != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if changes.Stats().ActiveSubscriptions != 1 {
		t.Fatal("Stream revalidation subscription was not registered")
	}
	revoke()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("committed revocation did not disconnect the active Stream")
		return nil
	}
}
