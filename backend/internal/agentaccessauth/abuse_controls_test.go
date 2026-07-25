package agentaccessauth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
)

// TestAAPAbuseControls covers Token endpoint multi-dimensional rate limits and
// exact-origin CORS policy validation (auth package surface).
func TestAAPAbuseControls(t *testing.T) {
	t.Run("CORSExactOriginsRejectStarWildcardAndEcho", testCORSExactPolicy)
	t.Run("CORSDisabledBFFMode", testCORSBFFDisabled)
	t.Run("TokenEndpointLimiterMultiDimensional", testTokenEndpointLimiter)
	t.Run("PreflightHeaderAndMethodAllowlist", testCORSPreflightAllowlists)
}

func testCORSExactPolicy(t *testing.T) {
	t.Parallel()
	_, err := agentaccessauth.NewExactCORSPolicy([]string{"*"})
	if !errors.Is(err, agentaccessauth.ErrCORSPolicyInvalid) {
		t.Fatalf("star origin error=%v", err)
	}
	_, err = agentaccessauth.NewExactCORSPolicy([]string{"https://*.example.test"})
	if !errors.Is(err, agentaccessauth.ErrCORSPolicyInvalid) {
		t.Fatalf("wildcard origin error=%v", err)
	}
	_, err = agentaccessauth.NewExactCORSPolicy([]string{"http://evil.example.test"})
	if !errors.Is(err, agentaccessauth.ErrCORSPolicyInvalid) {
		t.Fatalf("non-https non-loopback error=%v", err)
	}
	_, err = agentaccessauth.NewExactCORSPolicy([]string{"https://app.example.test/path"})
	if !errors.Is(err, agentaccessauth.ErrCORSPolicyInvalid) {
		t.Fatalf("origin with path error=%v", err)
	}
	policy, err := agentaccessauth.NewExactCORSPolicy([]string{
		"https://app.example.test",
		"https://app.example.test/", // normalized
		"http://127.0.0.1:8787",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Allows("https://app.example.test") {
		t.Fatal("expected exact allow")
	}
	if policy.Allows("https://evil.example.test") {
		t.Fatal("evil origin allowed")
	}
	// Must never echo unauthorized origins.
	if got := policy.ReflectOrigin("https://evil.example.test"); got != "" {
		t.Fatalf("reflected unauthorized origin %q", got)
	}
	if got := policy.ReflectOrigin("https://app.example.test"); got != "https://app.example.test" {
		t.Fatalf("reflect=%q", got)
	}
}

func testCORSBFFDisabled(t *testing.T) {
	t.Parallel()
	policy := agentaccessauth.NewDisabledCORSPolicy()
	if policy.Allows("https://app.example.test") || policy.ReflectOrigin("https://app.example.test") != "" {
		t.Fatal("disabled CORS must not allow or reflect origins")
	}
}

func testTokenEndpointLimiter(t *testing.T) {
	t.Parallel()
	limiter, err := agentaccessauth.NewInMemoryTokenEndpointLimiter(agentaccessauth.TokenEndpointLimiterConfig{
		MaxIssues: 2, Window: time.Minute, MaxEntries: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	attempt := agentaccessauth.TokenIssueAttempt{
		PublicClientID: "awcl_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		RemoteIP:       "203.0.113.10",
		GrantType:      "client_credentials",
	}
	for i := 0; i < 2; i++ {
		decision, err := limiter.AllowTokenIssue(ctx, attempt)
		if err != nil || decision.Limit != 2 {
			t.Fatalf("issue %d decision=%+v err=%v", i, decision, err)
		}
	}
	decision, err := limiter.AllowTokenIssue(ctx, attempt)
	if !errors.Is(err, agentaccessauth.ErrTokenIssueLimited) || decision.Remaining != 0 || decision.RetryAfter <= 0 {
		t.Fatalf("expected limit decision=%+v err=%v", decision, err)
	}
	// Different IP still shares client dimension → still limited.
	otherIP := attempt
	otherIP.RemoteIP = "203.0.113.11"
	if _, err := limiter.AllowTokenIssue(ctx, otherIP); !errors.Is(err, agentaccessauth.ErrTokenIssueLimited) {
		t.Fatalf("client dimension not enforced: %v", err)
	}
	// Independent client + IP pair on a fresh limiter is not blocked.
	fresh, err := agentaccessauth.NewInMemoryTokenEndpointLimiter(agentaccessauth.TokenEndpointLimiterConfig{
		MaxIssues: 2, Window: time.Minute, MaxEntries: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherClient := agentaccessauth.TokenIssueAttempt{
		PublicClientID: "awcl_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
		RemoteIP:       "198.51.100.9",
		GrantType:      "client_credentials",
	}
	// Public client ids must be well-formed; use a known-good fixture style id.
	otherClient.PublicClientID = attempt.PublicClientID[:len(attempt.PublicClientID)-1] + "B"
	if _, err := fresh.AllowTokenIssue(ctx, otherClient); err != nil {
		t.Fatalf("independent client/IP pair blocked: %v", err)
	}
}

func testCORSPreflightAllowlists(t *testing.T) {
	t.Parallel()
	if !agentaccessauth.AllowPreflightMethod("GET") || !agentaccessauth.AllowPreflightMethod("post") {
		t.Fatal("expected GET/POST preflight methods")
	}
	if agentaccessauth.AllowPreflightMethod("DELETE") || agentaccessauth.AllowPreflightMethod("TRACE") {
		t.Fatal("dangerous methods must be rejected")
	}
	if !agentaccessauth.AllowPreflightHeaders("Authorization, Content-Type, Last-Event-ID, ActWeave-Protocol-Version") {
		t.Fatal("expected standard AAP headers")
	}
	foundProtocol := false
	for _, name := range agentaccessauth.AAPCORSExposedHeaders {
		if name == "ActWeave-Protocol-Version" {
			foundProtocol = true
		}
		if name == "X-AAP-Protocol-Version" {
			t.Fatal("legacy X-AAP-Protocol-Version must not be exposed")
		}
	}
	if !foundProtocol {
		t.Fatal("ActWeave-Protocol-Version must be CORS-exposed")
	}
	if agentaccessauth.AllowPreflightHeaders("Authorization, X-Custom-Secret") {
		t.Fatal("custom headers must be rejected")
	}
	if !agentaccessauth.IsCORSPreflight("OPTIONS", "GET") || agentaccessauth.IsCORSPreflight("GET", "") {
		t.Fatal("preflight detection incorrect")
	}
}
