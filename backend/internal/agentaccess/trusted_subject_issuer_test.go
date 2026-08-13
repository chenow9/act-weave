package agentaccess_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/database/dbtest"

	"github.com/lib/pq"
)

func TestTrustedSubjectIssuerMigrationAndConfigUpdateBumpsSecurityVersion(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 21 || version.Dirty {
		t.Fatalf("expected clean latest schema for Trusted Subject Issuer, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertRepositoryFixtures(t, db)
	repository, err := agentaccess.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	service, err := agentaccess.NewManagementService(repository, bytes.Repeat([]byte{0x6b}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	registration, err := service.RegisterClient(ctx, agentaccess.RegisterClientInput{
		WorkspaceID: repositoryWorkspaceID, Name: "Trusted Subject Client",
		ActorID: repositoryOwnerID, AuthMethod: agentaccess.ClientAuthMethodSecretBasic,
		TokenTTLSeconds: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	if registration.Principal.SecurityVersion != 1 {
		t.Fatalf("initial security version=%d", registration.Principal.SecurityVersion)
	}

	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	inlineJWKS := trustedSubjectTestInlineJWKS(t, "subject-inline-1", publicKey)
	config := agentaccess.TrustedSubjectIssuerConfig{
		Issuer: "https://idp.partner.example.test", Audience: "actweave-partner-subject",
		InlineJWKS: inlineJWKS, Algorithms: []string{"EdDSA"},
		ClaimPolicy: agentaccessauth.DefaultSubjectClaimPolicy(),
	}
	updated, principal, err := service.UpdateTrustedSubjectIssuer(ctx, agentaccess.UpdateTrustedSubjectIssuerInput{
		WorkspaceID: repositoryWorkspaceID, ClientID: registration.Client.ID,
		ActorID: repositoryOwnerID, ExpectedLockVersion: registration.Client.LockVersion,
		Config: config,
	})
	if err != nil {
		t.Fatal(err)
	}
	if principal.SecurityVersion != 2 || updated.LockVersion != registration.Client.LockVersion+1 {
		t.Fatalf("security/lock versions principal=%+v client=%+v", principal, updated)
	}
	if updated.TrustedSubjectIssuer != config.Issuer ||
		updated.TrustedSubjectAudience != config.Audience ||
		updated.TrustedSubjectJWKSURI != "" ||
		len(updated.TrustedSubjectInlineJWKS) == 0 ||
		len(updated.TrustedSubjectAlgorithms) != 1 ||
		updated.TrustedSubjectAlgorithms[0] != "EdDSA" ||
		updated.TrustedSubjectClaimPolicy.SubjectClaim != "sub" {
		t.Fatalf("updated client trust fields=%+v", updated)
	}

	reloaded, err := repository.GetClient(ctx, repositoryWorkspaceID, registration.Client.ID)
	if err != nil {
		t.Fatal(err)
	}
	reloadedPrincipal, err := repository.GetServicePrincipal(ctx, repositoryWorkspaceID, registration.Principal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.TrustedSubjectIssuer != config.Issuer || reloadedPrincipal.SecurityVersion != 2 {
		t.Fatalf("persisted trust/security mismatch client=%+v principal=%+v", reloaded, reloadedPrincipal)
	}

	clientJSON, err := json.Marshal(toPublicTrustedSubjectClient(reloaded))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"subject"`, "subjectToken", "rawToken", `"d":`} {
		if strings.Contains(string(clientJSON), forbidden) {
			t.Fatalf("public client JSON leaked sensitive field %q: %s", forbidden, clientJSON)
		}
	}
	auditState, err := json.Marshal(agentaccess.TrustedSubjectIssuerConfig{
		Issuer: reloaded.TrustedSubjectIssuer, Audience: reloaded.TrustedSubjectAudience,
		JWKSURI: reloaded.TrustedSubjectJWKSURI, InlineJWKS: reloaded.TrustedSubjectInlineJWKS,
		Algorithms: reloaded.TrustedSubjectAlgorithms, ClaimPolicy: reloaded.TrustedSubjectClaimPolicy,
	}.PublicAuditState())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(auditState, reloaded.TrustedSubjectInlineJWKS) {
		t.Fatalf("audit state leaked full inline JWKS: %s", auditState)
	}

	if _, _, err := service.UpdateTrustedSubjectIssuer(ctx, agentaccess.UpdateTrustedSubjectIssuerInput{
		WorkspaceID: repositoryWorkspaceID, ClientID: registration.Client.ID,
		ActorID: repositoryOwnerID, ExpectedLockVersion: updated.LockVersion,
		Config: agentaccess.TrustedSubjectIssuerConfig{
			Issuer: config.Issuer, Audience: config.Audience,
			JWKSURI:    "https://idp.partner.example.test/jwks.json",
			InlineJWKS: inlineJWKS, Algorithms: []string{"EdDSA"},
			ClaimPolicy: agentaccessauth.DefaultSubjectClaimPolicy(),
		},
	}); !errors.Is(err, agentaccess.ErrManagementInvalid) {
		t.Fatalf("inline+URI update err=%v", err)
	}
	stalePrincipal, err := repository.GetServicePrincipal(ctx, repositoryWorkspaceID, registration.Principal.ID)
	if err != nil || stalePrincipal.SecurityVersion != 2 {
		t.Fatalf("failed update must not bump security version principal=%+v err=%v", stalePrincipal, err)
	}

	withURI, principal, err := service.UpdateTrustedSubjectIssuer(ctx, agentaccess.UpdateTrustedSubjectIssuerInput{
		WorkspaceID: repositoryWorkspaceID, ClientID: registration.Client.ID,
		ActorID: repositoryOwnerID, ExpectedLockVersion: updated.LockVersion,
		Config: agentaccess.TrustedSubjectIssuerConfig{
			Issuer: config.Issuer, Audience: config.Audience,
			JWKSURI:     "https://idp.partner.example.test/jwks.json",
			Algorithms:  []string{"EdDSA", "PS256"},
			ClaimPolicy: agentaccessauth.DefaultSubjectClaimPolicy(),
		},
	})
	if err != nil || principal.SecurityVersion != 3 || withURI.TrustedSubjectJWKSURI == "" ||
		len(withURI.TrustedSubjectInlineJWKS) != 0 || len(withURI.TrustedSubjectAlgorithms) != 2 {
		t.Fatalf("URI update client=%+v principal=%+v err=%v", withURI, principal, err)
	}

	cleared, principal, err := service.UpdateTrustedSubjectIssuer(ctx, agentaccess.UpdateTrustedSubjectIssuerInput{
		WorkspaceID: repositoryWorkspaceID, ClientID: registration.Client.ID,
		ActorID: repositoryOwnerID, ExpectedLockVersion: withURI.LockVersion, Clear: true,
	})
	if err != nil || principal.SecurityVersion != 4 ||
		cleared.TrustedSubjectIssuer != "" || cleared.TrustedSubjectAudience != "" ||
		cleared.TrustedSubjectJWKSURI != "" || len(cleared.TrustedSubjectInlineJWKS) != 0 ||
		len(cleared.TrustedSubjectAlgorithms) != 0 {
		t.Fatalf("clear update client=%+v principal=%+v err=%v", cleared, principal, err)
	}

	assertTrustedSubjectConstraintFailures(t, db, registration.Client.ID)

}

func TestTrustedSubjectIssuerRejectsHTTPAndBothJWKSSourcesAtDatabase(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertRepositoryFixtures(t, db)
	if _, err := db.Exec(`
		INSERT INTO service_principals(id,workspace_id,name,created_by,updated_by)
		VALUES($1,$2,'Trust principal',$3,$3)
	`, repositoryPrincipalID, repositoryWorkspaceID, repositoryOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_access_clients(
		 id,workspace_id,service_principal_id,client_id,name,auth_method,created_by,updated_by
		) VALUES($1,$2,$3,$4,'Base client','client_secret_basic',$5,$5)
	`, repositoryClientID, repositoryWorkspaceID, repositoryPrincipalID,
		repositoryPublicClient, repositoryOwnerID); err != nil {
		t.Fatal(err)
	}
	assertTrustedSubjectConstraintFailures(t, db, repositoryClientID)
}

func assertTrustedSubjectConstraintFailures(t *testing.T, db *sql.DB, clientID string) {
	t.Helper()
	validJWKS := `{"keys":[{"kty":"OKP","crv":"Ed25519","use":"sig","alg":"EdDSA","kid":"k1","x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}]}`
	validPolicy := `{"subjectClaim":"sub","requireJti":true,"maxSubjectBytes":256,"maxTokenTTLSeconds":3600}`
	validAlgorithms := `["EdDSA"]`

	failures := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name: "HTTP issuer",
			query: `
				UPDATE agent_access_clients SET
				 trusted_subject_issuer=$2,trusted_subject_audience=$3,trusted_subject_jwks_uri=$4,
				 trusted_subject_inline_jwks=NULL,trusted_subject_algorithms=$5::jsonb,
				 trusted_subject_claim_policy=$6::jsonb
				WHERE id=$1`,
			args: []any{clientID, "http://idp.example.test", "aud",
				"https://idp.example.test/jwks", validAlgorithms, validPolicy},
		},
		{
			name: "HTTP JWKS URI",
			query: `
				UPDATE agent_access_clients SET
				 trusted_subject_issuer=$2,trusted_subject_audience=$3,trusted_subject_jwks_uri=$4,
				 trusted_subject_inline_jwks=NULL,trusted_subject_algorithms=$5::jsonb,
				 trusted_subject_claim_policy=$6::jsonb
				WHERE id=$1`,
			args: []any{clientID, "https://idp.example.test", "aud",
				"http://idp.example.test/jwks", validAlgorithms, validPolicy},
		},
		{
			name: "inline and URI together",
			query: `
				UPDATE agent_access_clients SET
				 trusted_subject_issuer=$2,trusted_subject_audience=$3,trusted_subject_jwks_uri=$4,
				 trusted_subject_inline_jwks=$5::jsonb,trusted_subject_algorithms=$6::jsonb,
				 trusted_subject_claim_policy=$7::jsonb
				WHERE id=$1`,
			args: []any{clientID, "https://idp.example.test", "aud",
				"https://idp.example.test/jwks", validJWKS, validAlgorithms, validPolicy},
		},
		{
			name: "issuer without audience",
			query: `
				UPDATE agent_access_clients SET
				 trusted_subject_issuer=$2,trusted_subject_audience=NULL,trusted_subject_jwks_uri=$3,
				 trusted_subject_inline_jwks=NULL,trusted_subject_algorithms=$4::jsonb,
				 trusted_subject_claim_policy=$5::jsonb
				WHERE id=$1`,
			args: []any{clientID, "https://idp.example.test",
				"https://idp.example.test/jwks", validAlgorithms, validPolicy},
		},
		{
			name: "unsupported algorithm",
			query: `
				UPDATE agent_access_clients SET
				 trusted_subject_issuer=$2,trusted_subject_audience=$3,trusted_subject_jwks_uri=$4,
				 trusted_subject_inline_jwks=NULL,trusted_subject_algorithms=$5::jsonb,
				 trusted_subject_claim_policy=$6::jsonb
				WHERE id=$1`,
			args: []any{clientID, "https://idp.example.test", "aud",
				"https://idp.example.test/jwks", `["RS256"]`, validPolicy},
		},
		{
			name: "invalid claim policy subject claim",
			query: `
				UPDATE agent_access_clients SET
				 trusted_subject_issuer=$2,trusted_subject_audience=$3,trusted_subject_jwks_uri=$4,
				 trusted_subject_inline_jwks=NULL,trusted_subject_algorithms=$5::jsonb,
				 trusted_subject_claim_policy=$6::jsonb
				WHERE id=$1`,
			args: []any{clientID, "https://idp.example.test", "aud",
				"https://idp.example.test/jwks", validAlgorithms,
				`{"subjectClaim":"email","requireJti":true,"maxSubjectBytes":256,"maxTokenTTLSeconds":3600}`},
		},
	}
	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			_, err := db.Exec(failure.query, failure.args...)
			if err == nil {
				t.Fatal("expected constraint failure")
			}
			var pqErr *pq.Error
			if !errors.As(err, &pqErr) || (pqErr.Code != "23514" && pqErr.Code != "22023") {
				t.Fatalf("expected check constraint failure, got %v", err)
			}
		})
	}

	// Valid complete config must succeed.
	if _, err := db.Exec(`
		UPDATE agent_access_clients SET
		 trusted_subject_issuer=$2,trusted_subject_audience=$3,trusted_subject_jwks_uri=NULL,
		 trusted_subject_inline_jwks=$4::jsonb,trusted_subject_algorithms=$5::jsonb,
		 trusted_subject_claim_policy=$6::jsonb
		WHERE id=$1
	`, clientID, "https://idp.example.test", "aud", validJWKS, validAlgorithms, validPolicy); err != nil {
		t.Fatalf("valid trusted subject config rejected: %v", err)
	}
}

func trustedSubjectTestInlineJWKS(t *testing.T, keyID string, publicKey ed25519.PublicKey) json.RawMessage {
	t.Helper()
	jwk, err := json.Marshal(map[string]any{
		"kty": "OKP", "crv": "Ed25519", "use": "sig", "alg": "EdDSA", "kid": keyID,
		"x": base64.RawURLEncoding.EncodeToString(publicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	set, err := json.Marshal(map[string]any{"keys": []json.RawMessage{jwk}})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func toPublicTrustedSubjectClient(value agentaccess.AgentAccessClient) map[string]any {
	return map[string]any{
		"id": value.ID, "trustedSubjectIssuer": value.TrustedSubjectIssuer,
		"trustedSubjectAudience":    value.TrustedSubjectAudience,
		"trustedSubjectJwksUri":     value.TrustedSubjectJWKSURI,
		"trustedSubjectInlineJwks":  value.TrustedSubjectInlineJWKS,
		"trustedSubjectAlgorithms":  value.TrustedSubjectAlgorithms,
		"trustedSubjectClaimPolicy": value.TrustedSubjectClaimPolicy,
	}
}
