package agentaccess_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/database/dbtest"
)

func TestClientSecretAuthenticationRepository(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertRepositoryFixtures(t, db)
	repository, err := agentaccess.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	fixture := createRepositoryGraph(t, repository)
	ctx := context.Background()
	record, err := repository.FindClientSecretAuthentication(ctx, repositoryCredentialID)
	if err != nil || record.WorkspaceID != repositoryWorkspaceID || record.ClientID != repositoryClientID ||
		record.PublicClientID != repositoryPublicClient || record.ServicePrincipalID != repositoryPrincipalID ||
		record.ServicePrincipalVersion != 1 || record.ClientStatus != agentaccess.StatusActive ||
		record.ServicePrincipalStatus != agentaccess.StatusActive ||
		record.AuthMethod != agentaccess.ClientAuthMethodSecretBasic ||
		record.CredentialType != agentaccess.CredentialTypeClientSecret || len(record.SecretHash) != 32 {
		t.Fatalf("authentication record=%+v hash=%x err=%v", record, record.SecretHash, err)
	}

	firstUse := fixture.at.Add(time.Minute)
	if err := repository.RecordClientSecretAuthenticated(ctx, repositoryCredentialID, repositoryPublicClient, firstUse); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordClientSecretAuthenticated(ctx, repositoryCredentialID, repositoryPublicClient, firstUse.Add(30*time.Second)); err != nil {
		t.Fatalf("throttled last-used update must still authenticate: %v", err)
	}
	credential, err := repository.GetCredential(ctx, repositoryWorkspaceID, repositoryClientID, repositoryCredentialID)
	if err != nil || credential.LastUsedAt == nil || !credential.LastUsedAt.Equal(firstUse) {
		t.Fatalf("controlled last_used_at=%v err=%v", credential.LastUsedAt, err)
	}
	if _, err := repository.RevokeCredentialCAS(ctx, repositoryWorkspaceID, repositoryClientID,
		repositoryCredentialID, repositoryOwnerID, 1, firstUse.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordClientSecretAuthenticated(ctx, repositoryCredentialID, repositoryPublicClient,
		firstUse.Add(2*time.Minute)); !errors.Is(err, agentaccess.ErrRepositoryNotFound) {
		t.Fatalf("revoked credential usage update err=%v", err)
	}
}
