package agentaccess_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/database/dbtest"
)

const (
	repositoryOwnerID      = "c08f1f2e-7b5a-7c3d-8e9f-123456789001"
	repositoryWorkspaceID  = "c08f1f2e-7b5a-7c3d-8e9f-123456789002"
	repositoryOtherSpaceID = "c08f1f2e-7b5a-7c3d-8e9f-123456789003"
	repositoryModelID      = "c08f1f2e-7b5a-7c3d-8e9f-123456789004"
	repositoryOtherModelID = "c08f1f2e-7b5a-7c3d-8e9f-123456789005"
	repositoryAgentID      = "c08f1f2e-7b5a-7c3d-8e9f-123456789006"
	repositoryOtherAgentID = "c08f1f2e-7b5a-7c3d-8e9f-123456789007"
	repositoryPrincipalID  = "c08f1f2e-7b5a-7c3d-8e9f-123456789008"
	repositoryClientID     = "c08f1f2e-7b5a-7c3d-8e9f-123456789009"
	repositoryCredentialID = "c08f1f2e-7b5a-7c3d-8e9f-12345678900a"
	repositoryGrantID      = "c08f1f2e-7b5a-7c3d-8e9f-12345678900b"
	repositorySubjectID    = "c08f1f2e-7b5a-7c3d-8e9f-12345678900c"
	repositoryPublicClient = "awcl_repository0123456789abcdef012345"
)

type repositoryFixture struct {
	repository *agentaccess.Repository
	at         time.Time
	hash       []byte
}

func TestRepository(t *testing.T) {
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

	t.Run("reads complete scoped graph without exposing hashes", func(t *testing.T) {
		principal, err := repository.GetServicePrincipal(ctx, repositoryWorkspaceID, repositoryPrincipalID)
		if err != nil || principal.SecurityVersion != 1 || principal.LockVersion != 1 {
			t.Fatalf("Principal=%+v err=%v", principal, err)
		}
		client, err := repository.GetClientByPublicID(ctx, repositoryWorkspaceID, repositoryPublicClient)
		if err != nil || client.ServicePrincipalID != principal.ID ||
			len(client.AllowedCORSOrigins) != 1 || client.TokenTTLSeconds != 600 {
			t.Fatalf("Client=%+v err=%v", client, err)
		}
		credential, err := repository.GetCredential(ctx, repositoryWorkspaceID, client.ID, repositoryCredentialID)
		if err != nil || credential.PublicHint != "…cafe" || credential.Type != agentaccess.CredentialTypeClientSecret {
			t.Fatalf("Credential=%+v err=%v", credential, err)
		}
		evidence, err := repository.GetCredentialEvidence(ctx, repositoryWorkspaceID, client.ID, repositoryCredentialID)
		if err != nil || !bytes.Equal(evidence.SecretHash, fixture.hash) {
			t.Fatalf("Credential evidence hash=%x err=%v", evidence.SecretHash, err)
		}
		raw, err := json.Marshal(evidence)
		if err != nil || strings.Contains(string(raw), "SecretHash") ||
			strings.Contains(string(raw), "JWKThumbprint") || bytes.Contains(raw, fixture.hash) {
			t.Fatalf("Credential evidence leaked through JSON: err=%v json=%s", err, raw)
		}
		grant, err := repository.GetGrant(ctx, repositoryWorkspaceID, client.ID, repositoryAgentID, repositoryGrantID)
		if err != nil || grant.Status != agentaccess.GrantStatusActive || len(grant.Scopes) != 3 {
			t.Fatalf("Grant=%+v err=%v", grant, err)
		}
		subject, err := repository.FindExternalSubject(
			ctx, repositoryWorkspaceID, client.ID, "https://issuer.example.test", fixture.hash,
		)
		if err != nil || subject.ID != repositorySubjectID || subject.DisplayRef != "ref_customer_cafe" {
			t.Fatalf("External Subject=%+v err=%v", subject, err)
		}
		subjectJSON, _ := json.Marshal(subject)
		if bytes.Contains(subjectJSON, fixture.hash) || strings.Contains(string(subjectJSON), "SubjectHash") {
			t.Fatalf("External Subject hash leaked through JSON: %s", subjectJSON)
		}
		binding, err := repository.ResolveAccess(
			ctx, repositoryWorkspaceID, repositoryPublicClient, repositoryAgentID, fixture.at,
		)
		if err != nil || binding.Principal.ID != principal.ID ||
			binding.Client.ID != client.ID || binding.Grant.ID != grant.ID {
			t.Fatalf("Access binding=%+v err=%v", binding, err)
		}
	})

	t.Run("CAS permits one concurrent Principal update", func(t *testing.T) {
		start := make(chan struct{})
		results := make(chan error, 2)
		var wait sync.WaitGroup
		for _, name := range []string{"Principal A", "Principal B"} {
			wait.Add(1)
			go func(name string) {
				defer wait.Done()
				<-start
				_, err := repository.UpdateServicePrincipalCAS(ctx,
					repositoryWorkspaceID, repositoryPrincipalID,
					agentaccess.UpdateServicePrincipalInput{
						Name: name, Status: agentaccess.StatusActive, ActorID: repositoryOwnerID,
						ExpectedLockVersion: 1, BumpSecurityVersion: true,
					})
				results <- err
			}(name)
		}
		close(start)
		wait.Wait()
		close(results)
		var success, conflict int
		for err := range results {
			switch {
			case err == nil:
				success++
			case errors.Is(err, agentaccess.ErrRepositoryConflict):
				conflict++
			default:
				t.Fatalf("concurrent Principal CAS error=%v", err)
			}
		}
		principal, err := repository.GetServicePrincipal(ctx, repositoryWorkspaceID, repositoryPrincipalID)
		if success != 1 || conflict != 1 || err != nil ||
			principal.LockVersion != 2 || principal.SecurityVersion != 2 {
			t.Fatalf("CAS success=%d conflict=%d Principal=%+v err=%v",
				success, conflict, principal, err)
		}
	})

	t.Run("operational metadata and revocations use scoped CAS", func(t *testing.T) {
		if err := repository.RecordCredentialUsed(ctx, repositoryWorkspaceID, repositoryClientID,
			repositoryCredentialID, fixture.at.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		credential, err := repository.RevokeCredentialCAS(
			ctx, repositoryWorkspaceID, repositoryClientID, repositoryCredentialID,
			repositoryOwnerID, 1, fixture.at.Add(2*time.Minute),
		)
		if err != nil || credential.RevokedAt == nil || credential.LockVersion != 2 {
			t.Fatalf("revoked Credential=%+v err=%v", credential, err)
		}
		if _, err := repository.RevokeCredentialCAS(
			ctx, repositoryWorkspaceID, repositoryClientID, repositoryCredentialID,
			repositoryOwnerID, 1, fixture.at.Add(3*time.Minute),
		); !errors.Is(err, agentaccess.ErrRepositoryConflict) {
			t.Fatalf("stale Credential CAS err=%v", err)
		}
		subject, err := repository.UpdateExternalSubjectCAS(
			ctx, repositoryWorkspaceID, repositoryClientID, repositorySubjectID,
			agentaccess.UpdateExternalSubjectInput{
				DisplayRef: "ref_customer_updated", Status: agentaccess.StatusActive,
				LastSeenAt: fixture.at.Add(4 * time.Minute), ExpectedLockVersion: 1,
			},
		)
		if err != nil || subject.LockVersion != 2 || subject.DisplayRef != "ref_customer_updated" {
			t.Fatalf("updated External Subject=%+v err=%v", subject, err)
		}
		grant, err := repository.RevokeGrantCAS(
			ctx, repositoryWorkspaceID, repositoryClientID, repositoryAgentID,
			repositoryGrantID, repositoryOwnerID,
			1, fixture.at.Add(5*time.Minute),
		)
		if err != nil || grant.Status != agentaccess.GrantStatusRevoked || grant.LockVersion != 2 {
			t.Fatalf("revoked Grant=%+v err=%v", grant, err)
		}
		if _, err := repository.ResolveAccess(
			ctx, repositoryWorkspaceID, repositoryPublicClient, repositoryAgentID, fixture.at,
		); !errors.Is(err, agentaccess.ErrRepositoryNotFound) {
			t.Fatalf("revoked Grant remained resolvable: %v", err)
		}
	})
}

func TestScopeIsolation(t *testing.T) {
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

	reads := []struct {
		name string
		read func() error
	}{
		{"Principal", func() error {
			_, err := repository.GetServicePrincipal(ctx, repositoryOtherSpaceID, repositoryPrincipalID)
			return err
		}},
		{"Client", func() error {
			_, err := repository.GetClient(ctx, repositoryOtherSpaceID, repositoryClientID)
			return err
		}},
		{"Credential", func() error {
			_, err := repository.GetCredential(ctx, repositoryOtherSpaceID, repositoryClientID, repositoryCredentialID)
			return err
		}},
		{"Grant", func() error {
			_, err := repository.GetGrant(ctx, repositoryOtherSpaceID, repositoryClientID, repositoryAgentID, repositoryGrantID)
			return err
		}},
		{"External Subject", func() error {
			_, err := repository.FindExternalSubject(ctx, repositoryOtherSpaceID, repositoryClientID,
				"https://issuer.example.test", fixture.hash)
			return err
		}},
		{"Access binding", func() error {
			_, err := repository.ResolveAccess(ctx, repositoryOtherSpaceID,
				repositoryPublicClient, repositoryAgentID, fixture.at)
			return err
		}},
	}
	for _, read := range reads {
		t.Run(read.name, func(t *testing.T) {
			if err := read.read(); !errors.Is(err, agentaccess.ErrRepositoryNotFound) {
				t.Fatalf("cross-Workspace read err=%v", err)
			}
		})
	}
	withinWorkspaceNestedReads := []struct {
		name string
		read func() error
	}{
		{"Credential Client scope", func() error {
			_, err := repository.GetCredential(ctx, repositoryWorkspaceID,
				repositoryPrincipalID, repositoryCredentialID)
			return err
		}},
		{"Grant Client scope", func() error {
			_, err := repository.GetGrant(ctx, repositoryWorkspaceID,
				repositoryPrincipalID, repositoryAgentID, repositoryGrantID)
			return err
		}},
		{"External Subject Client scope", func() error {
			_, err := repository.GetExternalSubject(ctx, repositoryWorkspaceID,
				repositoryPrincipalID, repositorySubjectID)
			return err
		}},
	}
	for _, read := range withinWorkspaceNestedReads {
		t.Run(read.name, func(t *testing.T) {
			if err := read.read(); !errors.Is(err, agentaccess.ErrRepositoryNotFound) {
				t.Fatalf("wrong same-Workspace parent scope err=%v", err)
			}
		})
	}

	if _, err := repository.CreateCredential(ctx, agentaccess.CreateCredentialInput{
		ID: "c08f1f2e-7b5a-7c3d-8e9f-123456789020", WorkspaceID: repositoryOtherSpaceID,
		ClientID: repositoryClientID, Type: agentaccess.CredentialTypeClientSecret,
		SecretHash: bytes.Repeat([]byte{0x77}, 32), PublicHint: "probe",
		ValidFrom: fixture.at, ActorID: repositoryOwnerID,
	}); !errors.Is(err, agentaccess.ErrRepositoryNotFound) {
		t.Fatalf("cross-Workspace Credential create err=%v", err)
	}
	if _, err := repository.CreateGrant(ctx, agentaccess.CreateGrantInput{
		ID: "c08f1f2e-7b5a-7c3d-8e9f-123456789021", WorkspaceID: repositoryWorkspaceID,
		ClientID: repositoryClientID, AgentID: repositoryOtherAgentID,
		Scopes: []agentaccess.AgentScope{agentaccess.ScopeRunRead}, Policy: agentaccess.GrantPolicy{},
		ValidFrom: fixture.at.Add(24 * time.Hour), ActorID: repositoryOwnerID,
	}); !errors.Is(err, agentaccess.ErrRepositoryNotFound) {
		t.Fatalf("cross-Workspace Grant create err=%v", err)
	}
	if _, err := repository.UpdateServicePrincipalCAS(ctx, repositoryOtherSpaceID,
		repositoryPrincipalID, agentaccess.UpdateServicePrincipalInput{
			Name: "cross", Status: agentaccess.StatusActive, ActorID: repositoryOwnerID,
			ExpectedLockVersion: 1,
		}); !errors.Is(err, agentaccess.ErrRepositoryNotFound) {
		t.Fatalf("cross-Workspace Principal CAS err=%v", err)
	}
}

func createRepositoryGraph(t *testing.T, repository *agentaccess.Repository) repositoryFixture {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	principal, err := repository.CreateServicePrincipal(ctx, agentaccess.CreateServicePrincipalInput{
		ID: repositoryPrincipalID, WorkspaceID: repositoryWorkspaceID,
		Name: "Repository principal", ActorID: repositoryOwnerID,
	})
	if err != nil || principal.Status != agentaccess.StatusActive {
		t.Fatalf("create Principal=%+v err=%v", principal, err)
	}
	client, err := repository.CreateClient(ctx, agentaccess.CreateClientInput{
		ID: repositoryClientID, WorkspaceID: repositoryWorkspaceID,
		ServicePrincipalID: repositoryPrincipalID, ClientID: repositoryPublicClient,
		Name: "Repository client", AuthMethod: agentaccess.ClientAuthMethodSecretBasic,
		AllowedCORSOrigins: []string{"https://app.example.test"}, TokenTTLSeconds: 600,
		ActorID: repositoryOwnerID,
	})
	if err != nil || client.Status != agentaccess.StatusActive {
		t.Fatalf("create Client=%+v err=%v", client, err)
	}
	hash := bytes.Repeat([]byte{0xca}, 32)
	expires := at.Add(24 * time.Hour)
	credential, err := repository.CreateCredential(ctx, agentaccess.CreateCredentialInput{
		ID: repositoryCredentialID, WorkspaceID: repositoryWorkspaceID,
		ClientID: repositoryClientID, Type: agentaccess.CredentialTypeClientSecret,
		SecretHash: hash, PublicHint: "…cafe", ValidFrom: at,
		ExpiresAt: &expires, ActorID: repositoryOwnerID,
	})
	if err != nil || credential.LockVersion != 1 {
		t.Fatalf("create Credential=%+v err=%v", credential, err)
	}
	grant, err := repository.CreateGrant(ctx, agentaccess.CreateGrantInput{
		ID: repositoryGrantID, WorkspaceID: repositoryWorkspaceID,
		ClientID: repositoryClientID, AgentID: repositoryAgentID,
		Scopes: []agentaccess.AgentScope{
			agentaccess.ScopeAgentRead, agentaccess.ScopeRunCreate, agentaccess.ScopeEventRead,
		},
		Policy: agentaccess.GrantPolicy{}, ValidFrom: at, ExpiresAt: &expires,
		ActorID: repositoryOwnerID,
	})
	if err != nil || grant.LockVersion != 1 {
		t.Fatalf("create Grant=%+v err=%v", grant, err)
	}
	subject, err := repository.CreateExternalSubject(ctx, agentaccess.CreateExternalSubjectInput{
		ID: repositorySubjectID, WorkspaceID: repositoryWorkspaceID,
		ClientID: repositoryClientID, Issuer: "https://issuer.example.test",
		SubjectHash: hash, DisplayRef: "ref_customer_cafe", SeenAt: at,
	})
	if err != nil || subject.LockVersion != 1 {
		t.Fatalf("create External Subject=%+v err=%v", subject, err)
	}
	return repositoryFixture{repository: repository, at: at, hash: hash}
}

func insertRepositoryFixtures(t *testing.T, db interface {
	Exec(string, ...any) (sql.Result, error)
}) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'repository.owner','Repository Owner')`, repositoryOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'repository-space','Repository Space','PRODUCTION',$3,$3,$3),
		($2,'repository-other','Repository Other','PRODUCTION',$3,$3,$3)
	`, repositoryWorkspaceID, repositoryOtherSpaceID, repositoryOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES
		($1,$3,'Repository Model','openai','https://models.example.test','repo-model',$5,$5),
		($2,$4,'Repository Other Model','openai','https://models.example.test','repo-model',$5,$5)
	`, repositoryModelID, repositoryOtherModelID, repositoryWorkspaceID,
		repositoryOtherSpaceID, repositoryOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES
		($1,$3,'Repository Agent',$5,$6,$6),
		($2,$4,'Repository Other Agent',$7,$6,$6)
	`, repositoryAgentID, repositoryOtherAgentID, repositoryWorkspaceID,
		repositoryOtherSpaceID, repositoryModelID, repositoryOwnerID, repositoryOtherModelID); err != nil {
		t.Fatal(err)
	}
}
