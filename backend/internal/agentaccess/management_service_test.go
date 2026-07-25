package agentaccess_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/database/dbtest"
)

func TestManagementService(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertRepositoryFixtures(t, db)
	repository, err := agentaccess.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	pepper := bytes.Repeat([]byte{0x5a}, 32)
	service, err := agentaccess.NewManagementService(repository, pepper)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := service.RegisterClient(ctx, agentaccess.RegisterClientInput{
		WorkspaceID: repositoryWorkspaceID, Name: "Unsafe short token Client",
		ActorID: repositoryOwnerID, AuthMethod: agentaccess.ClientAuthMethodSecretBasic,
		TokenTTLSeconds: 299,
	}); !errors.Is(err, agentaccess.ErrManagementInvalid) {
		t.Fatalf("Client Access Token TTL below five minutes err=%v", err)
	}

	secretRegistration, err := service.RegisterClient(ctx, agentaccess.RegisterClientInput{
		WorkspaceID: repositoryWorkspaceID, Name: "Managed secret Client",
		ActorID: repositoryOwnerID, AuthMethod: agentaccess.ClientAuthMethodSecretBasic,
		AllowedCORSOrigins: []string{"https://app.example.test"}, TokenTTLSeconds: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertOneTimeSecret(t, repository, secretRegistration, pepper)

	privateRegistration, err := service.RegisterClient(ctx, agentaccess.RegisterClientInput{
		WorkspaceID: repositoryWorkspaceID, Name: "Managed key Client",
		ActorID: repositoryOwnerID, AuthMethod: agentaccess.ClientAuthMethodPrivateKey,
		JWKSURI:       "https://keys.example.test/client.jwks",
		JWKThumbprint: bytes.Repeat([]byte{0x41}, 32), CredentialPublicHint: "kid-managed-1",
		TokenTTLSeconds: 900,
	})
	if err != nil || privateRegistration.OneTimeSecret != "" ||
		privateRegistration.Credential.Type != agentaccess.CredentialTypeJWK {
		t.Fatalf("private key registration=%+v err=%v", privateRegistration, err)
	}

	t.Run("Grant validates data-plane scopes and revocation bumps security atomically", func(t *testing.T) {
		now := time.Now().UTC()
		grant, err := service.GrantAgent(ctx, agentaccess.CreateGrantInput{
			ID:          "d08f1f2e-7b5a-7c3d-8e9f-123456789001",
			WorkspaceID: repositoryWorkspaceID, ClientID: secretRegistration.Client.ID,
			AgentID: repositoryAgentID,
			Scopes: []agentaccess.AgentScope{
				agentaccess.ScopeAgentRead, agentaccess.ScopeRunCreate, agentaccess.ScopeEventRead,
			},
			Policy: agentaccess.GrantPolicy{}, ValidFrom: now.Add(-time.Minute),
			ActorID: repositoryOwnerID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.GrantAgent(ctx, agentaccess.CreateGrantInput{
			ID:          "d08f1f2e-7b5a-7c3d-8e9f-123456789002",
			WorkspaceID: repositoryWorkspaceID, ClientID: secretRegistration.Client.ID,
			AgentID: repositoryAgentID, Scopes: []agentaccess.AgentScope{"workspace:manage"},
			Policy: agentaccess.GrantPolicy{}, ValidFrom: now.Add(24 * time.Hour),
			ActorID: repositoryOwnerID,
		}); !errors.Is(err, agentaccess.ErrGrantConfigurationInvalid) {
			t.Fatalf("management Scope grant err=%v", err)
		}
		principalBefore, err := repository.GetServicePrincipal(
			ctx, repositoryWorkspaceID, secretRegistration.Principal.ID,
		)
		if err != nil || principalBefore.SecurityVersion != 1 {
			t.Fatalf("Principal before Grant revoke=%+v err=%v", principalBefore, err)
		}
		revoked, principal, err := service.RevokeGrant(
			ctx, repositoryWorkspaceID, secretRegistration.Client.ID,
			repositoryAgentID, grant.ID, repositoryOwnerID, grant.LockVersion,
		)
		if err != nil || revoked.Status != agentaccess.GrantStatusRevoked ||
			principal.SecurityVersion != 2 {
			t.Fatalf("Grant revoke=%+v Principal=%+v err=%v", revoked, principal, err)
		}
		if _, _, err := service.RevokeGrant(
			ctx, repositoryWorkspaceID, secretRegistration.Client.ID,
			repositoryAgentID, grant.ID, repositoryOwnerID, grant.LockVersion,
		); !errors.Is(err, agentaccess.ErrRepositoryConflict) {
			t.Fatalf("stale Grant revoke err=%v", err)
		}
		afterFailure, _ := repository.GetServicePrincipal(ctx, repositoryWorkspaceID, principal.ID)
		if afterFailure.SecurityVersion != 2 {
			t.Fatalf("failed Grant revoke bumped security version: %+v", afterFailure)
		}
	})

	t.Run("Credential rotation and last-active rule", func(t *testing.T) {
		initial := secretRegistration.Credential
		issued, err := service.AddCredential(ctx, agentaccess.AddCredentialInput{
			WorkspaceID: repositoryWorkspaceID, ClientID: secretRegistration.Client.ID,
			ActorID: repositoryOwnerID, Type: agentaccess.CredentialTypeClientSecret,
			ReplacesCredentialID: initial.ID, ReplacesExpectedLockVersion: initial.LockVersion,
			Overlap: time.Hour,
		})
		if err != nil || issued.OneTimeSecret == "" ||
			issued.ReplacedCredentialExpiresAt == nil ||
			time.Until(*issued.ReplacedCredentialExpiresAt) <= 0 ||
			time.Until(*issued.ReplacedCredentialExpiresAt) > agentaccess.MaxCredentialRotationOverlap {
			t.Fatalf("Credential rotation=%+v err=%v", issued, err)
		}
		old, err := repository.GetCredential(
			ctx, repositoryWorkspaceID, secretRegistration.Client.ID, initial.ID,
		)
		if err != nil || old.ExpiresAt == nil || old.LockVersion != 2 ||
			!old.ExpiresAt.Equal(*issued.ReplacedCredentialExpiresAt) {
			t.Fatalf("replaced Credential=%+v err=%v", old, err)
		}
		if _, err := service.AddCredential(ctx, agentaccess.AddCredentialInput{
			WorkspaceID: repositoryWorkspaceID, ClientID: secretRegistration.Client.ID,
			ActorID: repositoryOwnerID, Type: agentaccess.CredentialTypeClientSecret,
		}); !errors.Is(err, agentaccess.ErrRotationLimit) {
			t.Fatalf("third active Credential err=%v", err)
		}
		revokedOld, principal, err := service.RevokeCredential(
			ctx, repositoryWorkspaceID, secretRegistration.Client.ID, old.ID,
			repositoryOwnerID, old.LockVersion,
		)
		if err != nil || revokedOld.RevokedAt == nil || principal.SecurityVersion != 3 {
			t.Fatalf("revoke replaced Credential=%+v Principal=%+v err=%v", revokedOld, principal, err)
		}
		if _, _, err := service.RevokeCredential(
			ctx, repositoryWorkspaceID, secretRegistration.Client.ID, issued.Credential.ID,
			repositoryOwnerID, issued.Credential.LockVersion,
		); !errors.Is(err, agentaccess.ErrLastActiveCredential) {
			t.Fatalf("last active Credential revoke err=%v", err)
		}
		afterRefusal, _ := repository.GetServicePrincipal(ctx, repositoryWorkspaceID, principal.ID)
		if afterRefusal.SecurityVersion != 3 {
			t.Fatalf("last-Credential refusal bumped security version: %+v", afterRefusal)
		}

		disabled, principal, err := service.SetClientStatus(
			ctx, repositoryWorkspaceID, secretRegistration.Client.ID, repositoryOwnerID,
			agentaccess.StatusDisabled, secretRegistration.Client.LockVersion,
		)
		if err != nil || disabled.Status != agentaccess.StatusDisabled ||
			principal.SecurityVersion != 4 {
			t.Fatalf("disable Client=%+v Principal=%+v err=%v", disabled, principal, err)
		}
		revokedLast, principal, err := service.RevokeCredential(
			ctx, repositoryWorkspaceID, secretRegistration.Client.ID, issued.Credential.ID,
			repositoryOwnerID, issued.Credential.LockVersion,
		)
		if err != nil || revokedLast.RevokedAt == nil || principal.SecurityVersion != 5 {
			t.Fatalf("revoke disabled Client last Credential=%+v Principal=%+v err=%v",
				revokedLast, principal, err)
		}
		if _, _, err := service.SetClientStatus(
			ctx, repositoryWorkspaceID, secretRegistration.Client.ID, repositoryOwnerID,
			agentaccess.StatusActive, disabled.LockVersion,
		); !errors.Is(err, agentaccess.ErrLastActiveCredential) {
			t.Fatalf("enabled Client without Credential err=%v", err)
		}
		finalPrincipal, _ := repository.GetServicePrincipal(ctx, repositoryWorkspaceID, principal.ID)
		finalClient, _ := repository.GetClient(ctx, repositoryWorkspaceID, disabled.ID)
		if finalPrincipal.SecurityVersion != 5 || finalClient.Status != agentaccess.StatusDisabled {
			t.Fatalf("failed enable was not atomic: Principal=%+v Client=%+v",
				finalPrincipal, finalClient)
		}
	})
}

func assertOneTimeSecret(
	t *testing.T,
	repository *agentaccess.Repository,
	registration agentaccess.ClientRegistration,
	pepper []byte,
) {
	t.Helper()
	parts := strings.Split(registration.OneTimeSecret, "_")
	if len(parts) != 4 || parts[0] != "awsk" || parts[1] != "live" ||
		parts[2] != registration.Credential.ID {
		t.Fatalf("invalid one-time Secret format: %q", registration.OneTimeSecret)
	}
	randomPart, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(randomPart) != 32 {
		t.Fatalf("one-time Secret entropy bytes=%d err=%v", len(randomPart), err)
	}
	publicRandom, err := base64.RawURLEncoding.DecodeString(
		strings.TrimPrefix(registration.Client.ClientID, "awcl_"),
	)
	if err != nil || len(publicRandom) != 32 {
		t.Fatalf("public Client ID entropy bytes=%d err=%v", len(publicRandom), err)
	}
	evidence, err := repository.GetCredentialEvidence(
		context.Background(), registration.Client.WorkspaceID,
		registration.Client.ID, registration.Credential.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, pepper)
	_, _ = mac.Write([]byte(registration.OneTimeSecret))
	if !hmac.Equal(evidence.SecretHash, mac.Sum(nil)) ||
		bytes.Contains(evidence.SecretHash, []byte(registration.OneTimeSecret)) {
		t.Fatal("persisted Credential is not the expected Keyed Hash")
	}
	credentialJSON, _ := json.Marshal(registration.Credential)
	if strings.Contains(string(credentialJSON), registration.OneTimeSecret) ||
		strings.Contains(string(credentialJSON), parts[3]) {
		t.Fatalf("ordinary Credential DTO retained one-time Secret: %s", credentialJSON)
	}
}
