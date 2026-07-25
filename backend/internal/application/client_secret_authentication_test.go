package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/url"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/database/dbtest"
)

func TestClientSecretAuthenticationRepositoryAdapter(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	const (
		ownerID     = "a28f1f2e-7b5a-7c3d-8e9f-123456789001"
		workspaceID = "a28f1f2e-7b5a-7c3d-8e9f-123456789002"
	)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'aap.auth.owner','AAP Auth Owner')`, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'aap-auth-space','AAP Auth Space','PRODUCTION',$2,$2,$2)
	`, workspaceID, ownerID); err != nil {
		t.Fatal(err)
	}
	repository, err := agentaccess.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	pepper := bytes.Repeat([]byte{0x3c}, 32)
	management, err := agentaccess.NewManagementService(repository, pepper)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := management.RegisterClient(context.Background(), agentaccess.RegisterClientInput{
		WorkspaceID: workspaceID, Name: "Authentication adapter Client", ActorID: ownerID,
		AuthMethod: agentaccess.ClientAuthMethodSecretBasic, TokenTTLSeconds: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := agentaccessauth.NewInMemoryClientAuthenticationLimiter(5, time.Minute, 100)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := agentaccessauth.NewClientSecretAuthenticator(
		agentAccessClientSecretStore{repository: repository}, pepper,
		agentaccessauth.WithClientSecretAuthenticationLimiter(limiter),
	)
	if err != nil {
		t.Fatal(err)
	}
	basicValue := url.QueryEscape(registration.Client.ClientID) + ":" + url.QueryEscape(registration.OneTimeSecret)
	result, err := authenticator.AuthenticateBasic(context.Background(), agentaccessauth.ClientSecretAuthenticationRequest{
		Authorization: "Basic " + base64.StdEncoding.EncodeToString([]byte(basicValue)),
		SourceIP:      "203.0.113.21", UserAgent: "adapter-test/1.0",
	})
	if err != nil || result.WorkspaceID != workspaceID || result.ClientID != registration.Client.ID ||
		result.CredentialID != registration.Credential.ID || result.ServicePrincipalID != registration.Principal.ID {
		t.Fatalf("authenticated Client=%+v err=%v", result, err)
	}
	credential, err := repository.GetCredential(context.Background(), workspaceID,
		registration.Client.ID, registration.Credential.ID)
	if err != nil || credential.LastUsedAt == nil {
		t.Fatalf("Credential last use was not recorded: %+v err=%v", credential, err)
	}
}
