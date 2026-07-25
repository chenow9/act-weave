package httptransport

import (
	"bytes"
	"crypto/ed25519"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
)

func TestAgentAccessSecurityAcceptanceLogsNeverContainCredentialsOrTokens(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x91}, ed25519.SeedSize))
	keys, err := agentaccessauth.NewRotatingSigningKeyProvider(
		"aap-log-safety-key", privateKey, 15*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := agentaccessauth.NewAAPAccessTokenVerifier(
		keys, "https://api.example.test/api/agent-access/v1/oauth/token", 15*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	key, err := keys.ActiveSigningKey(now)
	if err != nil {
		t.Fatal(err)
	}
	validToken, err := key.SignAccessToken(aapHTTPAuthenticationClaims(now))
	if err != nil {
		t.Fatal(err)
	}
	secretMarker := "basic-secret-MUST-NEVER-ENTER-LOGS-9f3d"
	assertionMarker := "assertion-MUST-NEVER-ENTER-LOGS-7a2c"
	invalidBearerMarker := "bearer-MUST-NEVER-ENTER-LOGS-4b1e"
	tokenRoutes, err := NewAgentAccessTokenRoutes(
		&tokenClientSecretStub{err: agentaccessauth.ErrInvalidClient},
		&tokenPrivateKeyStub{err: agentaccessauth.ErrInvalidClient},
		&tokenIssuerStub{},
		&tokenExchangeStub{err: agentaccessauth.ErrTokenExchangeRequestInvalid},
	)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	router, err := NewRouter(Config{
		Logger:                   slog.New(slog.NewJSONHandler(&logs, nil)),
		AgentAccessAuthenticator: verifier,
		AgentAccessRegistrars: []AgentAccessV1RouteRegistrar{
			aapPrincipalProbeRoutes{}, tokenRoutes,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, value := range []string{validToken, invalidBearerMarker} {
		request := httptest.NewRequest(http.MethodGet, "/api/agent-access/v1/auth-probe", nil)
		request.Header.Set("Authorization", "Bearer "+value)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
	}
	basic := tokenEndpointFormRequest(url.Values{
		"grant_type": {"client_credentials"}, "agent_id": {"a68f1f2e-7b5a-7c3d-8e9f-123456789001"},
		"scope": {"run:create"},
	})
	basic.Header.Set("Authorization", "Basic "+secretMarker)
	basicResponse := httptest.NewRecorder()
	router.ServeHTTP(basicResponse, basic)
	privateAssertion := tokenEndpointFormRequest(url.Values{
		"grant_type": {"client_credentials"}, "agent_id": {"a68f1f2e-7b5a-7c3d-8e9f-123456789001"},
		"scope": {"run:create"}, "client_assertion_type": {clientAssertionTypeJWTBearer},
		"client_assertion": {assertionMarker},
	})
	assertionResponse := httptest.NewRecorder()
	router.ServeHTTP(assertionResponse, privateAssertion)

	serialized := logs.String() + basicResponse.Body.String() + assertionResponse.Body.String()
	for _, forbidden := range []string{
		validToken, secretMarker, assertionMarker, invalidBearerMarker,
		"Authorization", "client_assertion",
	} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("HTTP log/denial response leaked %q: %s", forbidden, serialized)
		}
	}
}
