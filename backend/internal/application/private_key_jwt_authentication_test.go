package application

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/database/dbtest"

	"github.com/golang-jwt/jwt/v5"
)

func TestPrivateKeyJWTAuthenticationRepositoryAdapters(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	const (
		ownerID       = "a38f1f2e-7b5a-7c3d-8e9f-123456789001"
		workspaceID   = "a38f1f2e-7b5a-7c3d-8e9f-123456789002"
		tokenEndpoint = "https://api.example.test/api/agent-access/v1/oauth/token"
	)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'private.jwt.owner','Private JWT Owner')`, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'private-jwt-space','Private JWT Space','PRODUCTION',$2,$2,$2)
	`, workspaceID, ownerID); err != nil {
		t.Fatal(err)
	}
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x43}, ed25519.SeedSize))
	publicKey := privateKey.Public().(ed25519.PublicKey)
	x := base64.RawURLEncoding.EncodeToString(publicKey)
	canonical, _ := json.Marshal(struct {
		Curve   string `json:"crv"`
		KeyType string `json:"kty"`
		X       string `json:"x"`
	}{Curve: "Ed25519", KeyType: "OKP", X: x})
	thumbprint := sha256.Sum256(canonical)
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]any{{
		"kty": "OKP", "crv": "Ed25519", "use": "sig", "alg": "EdDSA",
		"kid": "adapter-ed-1", "x": x,
	}}})
	repository, err := agentaccess.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	management, err := agentaccess.NewManagementService(repository, bytes.Repeat([]byte{0x44}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registration, err := management.RegisterClient(context.Background(), agentaccess.RegisterClientInput{
		WorkspaceID: workspaceID, Name: "Private JWT adapter Client", ActorID: ownerID,
		AuthMethod: agentaccess.ClientAuthMethodPrivateKey,
		JWKSURI:    "https://keys.example.test/client.jwks", JWKThumbprint: thumbprint[:],
		CredentialPublicHint: "adapter-ed-1", TokenTTLSeconds: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := agentaccessauth.NewRemoteJWKSCache(
		privateKeyJWTStaticFetcher{body: jwks}, agentaccessauth.DefaultRemoteJWKSMaxBytes,
		agentaccessauth.DefaultRemoteJWKSMaxKeys, 100,
	)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := agentaccessauth.NewPrivateKeyJWTAuthenticator(
		agentAccessPrivateKeyJWTStore{repository: repository}, cache,
		agentAccessClientAssertionJTIStore{repository: repository}, tokenEndpoint,
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claims := jwt.RegisteredClaims{
		Issuer: registration.Client.ClientID, Subject: registration.Client.ClientID,
		Audience: jwt.ClaimStrings{tokenEndpoint}, ID: "adapter-assertion-jti-0001",
		IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(2 * time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"], token.Header["typ"] = "adapter-ed-1", "JWT"
	assertion, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	request := agentaccessauth.PrivateKeyJWTAuthenticationRequest{ClientAssertion: assertion, SourceIP: "203.0.113.41"}
	result, err := authenticator.Authenticate(context.Background(), request)
	if err != nil || result.ClientID != registration.Client.ID ||
		result.CredentialID != registration.Credential.ID || result.WorkspaceID != workspaceID {
		t.Fatalf("private_key_jwt adapter result=%+v err=%v", result, err)
	}
	if _, err := authenticator.Authenticate(context.Background(), request); !errors.Is(err, agentaccessauth.ErrInvalidClient) {
		t.Fatalf("PostgreSQL JTI replay err=%v", err)
	}
	var jtiCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_access_client_assertion_jtis WHERE client_id=$1`,
		registration.Client.ID).Scan(&jtiCount); err != nil || jtiCount != 1 {
		t.Fatalf("persisted JTI count=%d err=%v", jtiCount, err)
	}
	credential, err := repository.GetCredential(context.Background(), workspaceID,
		registration.Client.ID, registration.Credential.ID)
	if err != nil || credential.LastUsedAt == nil {
		t.Fatalf("private_key_jwt last use=%v err=%v", credential.LastUsedAt, err)
	}
}

type privateKeyJWTStaticFetcher struct{ body []byte }

func (fetcher privateKeyJWTStaticFetcher) FetchRemoteJWKS(
	context.Context,
	string,
	int64,
) (agentaccessauth.RemoteJWKSFetchResult, error) {
	return agentaccessauth.RemoteJWKSFetchResult{Body: append([]byte(nil), fetcher.body...)}, nil
}
