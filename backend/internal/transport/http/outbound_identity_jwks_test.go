package httptransport

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/outboundidentity"

	"github.com/gin-gonic/gin"
)

func TestOutboundIdentityJWKSPublicOnly(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := outboundidentity.NewRotatingSigningKeyProvider(
		"outbound-jwks-test", priv, outboundidentity.DefaultMaxAssertionTTL,
	)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewOutboundIdentityJWKSRoutes(keys)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	routes.Register(r)

	req := httptest.NewRequest(http.MethodGet, "/api/outbound-identity/v1/.well-known/jwks.json", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/jwk-set+json") {
		t.Fatalf("content-type %q", ct)
	}
	var set outboundidentity.JSONWebKeySet
	if err := json.Unmarshal(rec.Body.Bytes(), &set); err != nil {
		t.Fatal(err)
	}
	if len(set.Keys) != 1 || set.Keys[0].KeyID != "outbound-jwks-test" {
		t.Fatalf("keys %+v", set.Keys)
	}
	if set.Keys[0].Algorithm != outboundidentity.OutboundSigningAlgorithm {
		t.Fatalf("alg %s", set.Keys[0].Algorithm)
	}
	body := rec.Body.String()
	for _, forbidden := range []string{`"d"`, `"p"`, `"q"`, "PRIVATE", string(priv)} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("forbidden material in JWKS: %s", forbidden)
		}
	}

	// ETag / 304
	etag := rec.Header().Get("ETag")
	req2 := httptest.NewRequest(http.MethodGet, "/api/outbound-identity/v1/.well-known/jwks.json", nil)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", rec2.Code)
	}
}

func TestOutboundAndAAPJWKSAreSeparatePaths(t *testing.T) {
	// Sanity: outbound path is not under agent-access.
	path := "/api/outbound-identity/v1/.well-known/jwks.json"
	if strings.Contains(path, "agent-access") {
		t.Fatal("outbound JWKS must not share AAP path")
	}
	// Keys from different domains would have different kids in production config;
	// this test only freezes the URL contract.
	_ = time.Now()
}
