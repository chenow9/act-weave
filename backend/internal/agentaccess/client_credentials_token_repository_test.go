package agentaccess_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/database/dbtest"
)

func TestClientCredentialsTokenEndpointRepositoryResolvesOneActiveAgentGrant(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertRepositoryFixtures(t, db)
	repository, err := agentaccess.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	fixture := createRepositoryGraph(t, repository)
	record, err := repository.ResolveClientCredentialsTokenGrant(
		context.Background(), repositoryWorkspaceID, repositoryClientID,
		repositoryPublicClient, repositoryPrincipalID, repositoryCredentialID,
		repositoryAgentID, 1, fixture.at,
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.GrantID != repositoryGrantID || record.WorkspaceID != repositoryWorkspaceID ||
		record.ClientID != repositoryClientID || record.PublicClientID != repositoryPublicClient ||
		record.ServicePrincipalID != repositoryPrincipalID || record.ServicePrincipalVersion != 1 ||
		record.AgentID != repositoryAgentID || record.TokenTTLSeconds != 600 ||
		record.GrantExpiresAt == nil || !record.GrantExpiresAt.Equal(fixture.at.Add(24*time.Hour)) ||
		!slices.Equal(record.Scopes, []agentaccess.AgentScope{
			agentaccess.ScopeAgentRead, agentaccess.ScopeRunCreate, agentaccess.ScopeEventRead,
		}) {
		t.Fatalf("unexpected Client Credentials Grant snapshot: %+v", record)
	}

	for name, invoke := range map[string]func() error{
		"cross Workspace": func() error {
			_, err := repository.ResolveClientCredentialsTokenGrant(context.Background(), repositoryOtherSpaceID,
				repositoryClientID, repositoryPublicClient, repositoryPrincipalID, repositoryCredentialID,
				repositoryAgentID, 1, fixture.at)
			return err
		},
		"wrong Agent": func() error {
			_, err := repository.ResolveClientCredentialsTokenGrant(context.Background(), repositoryWorkspaceID,
				repositoryClientID, repositoryPublicClient, repositoryPrincipalID, repositoryCredentialID,
				repositoryOtherAgentID, 1, fixture.at)
			return err
		},
		"stale security version": func() error {
			_, err := repository.ResolveClientCredentialsTokenGrant(context.Background(), repositoryWorkspaceID,
				repositoryClientID, repositoryPublicClient, repositoryPrincipalID, repositoryCredentialID,
				repositoryAgentID, 2, fixture.at)
			return err
		},
		"unknown Credential": func() error {
			_, err := repository.ResolveClientCredentialsTokenGrant(context.Background(), repositoryWorkspaceID,
				repositoryClientID, repositoryPublicClient, repositoryPrincipalID,
				"c08f1f2e-7b5a-7c3d-8e9f-123456789099", repositoryAgentID, 1, fixture.at)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := invoke(); !errors.Is(err, agentaccess.ErrRepositoryNotFound) {
				t.Fatalf("error=%v want scoped not found", err)
			}
		})
	}

	if _, err := repository.RevokeCredentialCAS(context.Background(), repositoryWorkspaceID,
		repositoryClientID, repositoryCredentialID, repositoryOwnerID, 1, fixture.at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ResolveClientCredentialsTokenGrant(context.Background(), repositoryWorkspaceID,
		repositoryClientID, repositoryPublicClient, repositoryPrincipalID, repositoryCredentialID,
		repositoryAgentID, 1, fixture.at.Add(2*time.Minute)); !errors.Is(err, agentaccess.ErrRepositoryNotFound) {
		t.Fatalf("revoked Credential must close post-auth token race: %v", err)
	}
}

func TestClientCredentialsTokenEndpointMigrationEnforcesFiveToFifteenMinuteTTL(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertRepositoryFixtures(t, db)
	if _, err := db.Exec(`
		INSERT INTO service_principals(id,workspace_id,name,created_by,updated_by)
		VALUES($1,$2,'TTL principal',$3,$3)
	`, repositoryPrincipalID, repositoryWorkspaceID, repositoryOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_access_clients(
		 id,workspace_id,service_principal_id,client_id,name,auth_method,
		 token_ttl_seconds,created_by,updated_by
		) VALUES($1,$2,$3,$4,'TTL client','client_secret_basic',600,$5,$5)
	`, repositoryClientID, repositoryWorkspaceID, repositoryPrincipalID,
		repositoryPublicClient, repositoryOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agent_access_clients SET token_ttl_seconds=299 WHERE id=$1`, repositoryClientID); err == nil {
		t.Fatal("migration 47 must reject Access Token TTL below five minutes")
	}
	if _, err := db.Exec(`UPDATE agent_access_clients SET token_ttl_seconds=300 WHERE id=$1`, repositoryClientID); err != nil {
		t.Fatalf("five-minute Access Token TTL must be accepted: %v", err)
	}
	testDatabase.MigrateToLatest(t)
	if _, err := db.Exec(`UPDATE agent_access_clients SET token_ttl_seconds=60 WHERE id=$1`, repositoryClientID); err != nil {
		t.Fatalf("migration 47 down must restore previous constraint: %v", err)
	}
	if _, err := db.Exec(`UPDATE agent_access_clients SET token_ttl_seconds=300 WHERE id=$1`, repositoryClientID); err != nil {
		t.Fatal(err)
	}
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 23 || version.Dirty {
		t.Fatalf("expected clean token TTL migration version 47, got %+v", version)
	}
}
