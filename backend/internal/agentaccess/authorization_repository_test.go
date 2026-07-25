package agentaccess_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/database/dbtest"
)

func TestAuthorizationRepositoryScopesEveryLookupByWorkspaceAndAgent(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertRepositoryFixtures(t, db)
	repository, err := agentaccess.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	fixture := createRepositoryGraph(t, repository)
	record, err := repository.ResolveAAPAuthorizationState(
		context.Background(), repositoryWorkspaceID, repositoryAgentID,
		repositoryPublicClient, repositoryPrincipalID, fixture.at,
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.WorkspaceID != repositoryWorkspaceID || record.AgentID != repositoryAgentID ||
		record.ClientID != repositoryClientID || record.PublicClientID != repositoryPublicClient ||
		record.ServicePrincipalID != repositoryPrincipalID || record.CurrentSecurityVersion != 1 ||
		record.GrantID != repositoryGrantID || record.WorkspaceVersion != 1 ||
		record.ClientVersion != 1 || record.GrantVersion != 1 || record.AgentPolicyVersion != 1 ||
		!slices.Equal(record.GrantScopes, []agentaccess.AgentScope{
			agentaccess.ScopeAgentRead, agentaccess.ScopeRunCreate, agentaccess.ScopeEventRead,
		}) || record.GrantPolicy.ServiceDecision != nil {
		t.Fatalf("unexpected authorization state: %+v", record)
	}

	// Even a coincident internal User with OWNER membership cannot contribute a
	// role to an AAP Client. The data-plane query has no User/member join.
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'coincident.principal','Coincident Principal')`, repositoryPrincipalID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_members(workspace_id,user_id,role,invited_by)
		VALUES($1,$2,'OWNER',$3)
	`, repositoryWorkspaceID, repositoryPrincipalID, repositoryOwnerID); err != nil {
		t.Fatal(err)
	}
	afterMembership, err := repository.ResolveAAPAuthorizationState(
		context.Background(), repositoryWorkspaceID, repositoryAgentID,
		repositoryPublicClient, repositoryPrincipalID, fixture.at,
	)
	if err != nil || !slices.Equal(afterMembership.GrantScopes, record.GrantScopes) ||
		afterMembership.GrantID != record.GrantID {
		t.Fatalf("Workspace membership altered AAP authorization: %+v err=%v", afterMembership, err)
	}

	for name, invoke := range map[string]func() error{
		"cross Workspace": func() error {
			_, err := repository.ResolveAAPAuthorizationState(context.Background(), repositoryOtherSpaceID,
				repositoryAgentID, repositoryPublicClient, repositoryPrincipalID, fixture.at)
			return err
		},
		"cross Agent": func() error {
			_, err := repository.ResolveAAPAuthorizationState(context.Background(), repositoryWorkspaceID,
				repositoryOtherAgentID, repositoryPublicClient, repositoryPrincipalID, fixture.at)
			return err
		},
		"wrong Client": func() error {
			_, err := repository.ResolveAAPAuthorizationState(context.Background(), repositoryWorkspaceID,
				repositoryAgentID, "awcl_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
				repositoryPrincipalID, fixture.at)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := invoke(); !errors.Is(err, agentaccess.ErrRepositoryNotFound) {
				t.Fatalf("error=%v want scoped not found", err)
			}
		})
	}

	if _, err := db.Exec(`UPDATE agents SET status='DISABLED' WHERE workspace_id=$1 AND id=$2`,
		repositoryWorkspaceID, repositoryAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ResolveAAPAuthorizationState(context.Background(), repositoryWorkspaceID,
		repositoryAgentID, repositoryPublicClient, repositoryPrincipalID,
		fixture.at); !errors.Is(err, agentaccess.ErrRepositoryNotFound) {
		t.Fatalf("disabled Agent authorization err=%v", err)
	}
}
