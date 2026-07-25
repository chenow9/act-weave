package agentaccess_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/database/dbtest"
)

func TestPrivateKeyJWTAuthenticationRepository(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 60 || version.Dirty {
		t.Fatalf("expected clean latest schema for private_key_jwt, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertRepositoryFixtures(t, db)
	repository, err := agentaccess.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	management, err := agentaccess.NewManagementService(repository, bytes.Repeat([]byte{0x71}, 32))
	if err != nil {
		t.Fatal(err)
	}
	thumbprint := sha256.Sum256([]byte("registered-client-jwk-thumbprint"))
	registration, err := management.RegisterClient(context.Background(), agentaccess.RegisterClientInput{
		WorkspaceID: repositoryWorkspaceID, Name: "Repository private_key_jwt Client",
		ActorID: repositoryOwnerID, AuthMethod: agentaccess.ClientAuthMethodPrivateKey,
		JWKSURI: "https://keys.example.test/client.jwks", JWKThumbprint: thumbprint[:],
		CredentialPublicHint: "client-key-2026-07", TokenTTLSeconds: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := repository.FindPrivateKeyJWTAuthentication(
		context.Background(), registration.Client.ClientID,
	)
	if err != nil || record.WorkspaceID != repositoryWorkspaceID ||
		record.ClientID != registration.Client.ID || record.ServicePrincipalID != registration.Principal.ID ||
		record.ServicePrincipalVersion != 1 || record.AuthMethod != agentaccess.ClientAuthMethodPrivateKey ||
		record.JWKSURI != "https://keys.example.test/client.jwks" || len(record.Credentials) != 1 ||
		!bytes.Equal(record.Credentials[0].JWKThumbprint, thumbprint[:]) {
		t.Fatalf("private_key_jwt record=%+v err=%v", record, err)
	}
	usedAt := time.Now().UTC().Add(time.Second)
	if err := repository.RecordPrivateKeyJWTAuthenticated(context.Background(), registration.Credential.ID,
		registration.Client.ClientID, usedAt); err != nil {
		t.Fatal(err)
	}
	credential, err := repository.GetCredential(context.Background(), repositoryWorkspaceID,
		registration.Client.ID, registration.Credential.ID)
	if err != nil || credential.LastUsedAt == nil || !credential.LastUsedAt.Equal(usedAt) {
		t.Fatalf("private_key_jwt last use=%v err=%v", credential.LastUsedAt, err)
	}
	if _, _, err := management.SetClientStatus(context.Background(), repositoryWorkspaceID,
		registration.Client.ID, repositoryOwnerID, agentaccess.StatusDisabled,
		registration.Client.LockVersion); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordPrivateKeyJWTAuthenticated(context.Background(), registration.Credential.ID,
		registration.Client.ClientID, usedAt.Add(2*time.Minute)); !errors.Is(err, agentaccess.ErrRepositoryNotFound) {
		t.Fatalf("disabled Client authentication update err=%v", err)
	}

	t.Run("JTI is atomic across concurrent claims and reusable only after expiry", func(t *testing.T) {
		now := time.Now().UTC()
		hash := sha256.Sum256([]byte("client-assertion-jti-1"))
		start := make(chan struct{})
		results := make(chan bool, 12)
		errorsFound := make(chan error, 12)
		var wait sync.WaitGroup
		for index := 0; index < cap(results); index++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				claimed, err := repository.ClaimClientAssertionJTI(context.Background(),
					registration.Client.ID, hash[:], now.Add(time.Minute), now)
				results <- claimed
				errorsFound <- err
			}()
		}
		close(start)
		wait.Wait()
		close(results)
		close(errorsFound)
		winners := 0
		for claimed := range results {
			if claimed {
				winners++
			}
		}
		for err := range errorsFound {
			if err != nil {
				t.Fatalf("concurrent JTI claim: %v", err)
			}
		}
		if winners != 1 {
			t.Fatalf("concurrent PostgreSQL JTI winners=%d", winners)
		}
		claimed, err := repository.ClaimClientAssertionJTI(context.Background(),
			registration.Client.ID, hash[:], now.Add(3*time.Minute), now.Add(2*time.Minute))
		if err != nil || !claimed {
			t.Fatalf("expired JTI was not reclaimable: claimed=%t err=%v", claimed, err)
		}
	})

	version = testDatabase.MigrateTo(t, 45)
	if !version.Applied || version.Number != 45 || version.Dirty {
		t.Fatalf("expected migration rollback to 45, got %+v", version)
	}
	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 60 || version.Dirty {
		t.Fatalf("expected latest migration reapply 60, got %+v", version)
	}
}
