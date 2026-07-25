package httptransport

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
)

func TestAgentAccessJWKSIsPublicCacheableAndContainsNoPrivateMaterial(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
	provider, err := agentaccessauth.NewRotatingSigningKeyProvider(
		"aap-http-key", privateKey, 10*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewAgentAccessJWKSRoutes(provider)
	if err != nil {
		t.Fatal(err)
	}
	routes.now = func() time.Time { return now }
	router, err := NewRouter(Config{AgentAccessRegistrars: []AgentAccessV1RouteRegistrar{routes}})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/agent-access/v1/.well-known/jwks.json", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("JWKS status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/jwk-set+json" ||
		response.Header().Get("Cache-Control") != "public, max-age=60, must-revalidate" ||
		response.Header().Get("ETag") == "" {
		t.Fatalf("unexpected JWKS response headers: %v", response.Header())
	}
	var set agentaccessauth.JSONWebKeySet
	if err := json.Unmarshal(response.Body.Bytes(), &set); err != nil {
		t.Fatal(err)
	}
	if len(set.Keys) != 1 || set.Keys[0].KeyID != "aap-http-key" || set.Keys[0].Algorithm != "EdDSA" {
		t.Fatalf("unexpected JWKS body: %+v", set)
	}
	if strings.Contains(response.Body.String(), `"d"`) || bytes.Contains(response.Body.Bytes(), privateKey.Seed()) {
		t.Fatalf("JWKS leaked private material: %s", response.Body.String())
	}

	conditional := httptest.NewRequest(http.MethodGet, "/api/agent-access/v1/.well-known/jwks.json", nil)
	conditional.Header.Set("If-None-Match", response.Header().Get("ETag"))
	notModified := httptest.NewRecorder()
	router.ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional JWKS status=%d body=%s", notModified.Code, notModified.Body.String())
	}
}

func TestAgentAccessJWKSRequiresConfiguredProviderAndOwnRouteRoot(t *testing.T) {
	if _, err := NewAgentAccessJWKSRoutes(nil); err == nil {
		t.Fatal("JWKS routes must require a signing key provider")
	}
	router, err := NewRouter(Config{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(
		http.MethodGet, "/api/v1/agent-access/.well-known/jwks.json", nil,
	))
	if response.Code != http.StatusNotFound {
		t.Fatalf("JWKS must not be exposed under the management API root: %d", response.Code)
	}
}
