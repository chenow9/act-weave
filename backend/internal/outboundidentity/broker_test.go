package outboundidentity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testAssertionKeys(t *testing.T) (*RotatingSigningKeyProvider, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := NewRotatingSigningKeyProvider("outbound-test-key-1", priv, DefaultMaxAssertionTTL)
	if err != nil {
		t.Fatal(err)
	}
	return keys, priv
}

func testMachineKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func baseExchangeRequest(subjectType SubjectType, subjectID string, machine ed25519.PrivateKey) BrokerExchangeRequest {
	return BrokerExchangeRequest{
		BootID: "boot-1", WorkspaceID: "ws-1",
		SubjectType: subjectType, SubjectID: subjectID,
		RootScopeType: RootScopeAgentRun, RootScopeID: "run-1",
		ConnectionID:            "conn-1",
		ProviderContractVersion: 1, ConnectionPolicyVersion: 2,
		Scopes: []string{"orders.read"},
		Provider: ProviderBrokerOBO{
			TokenEndpoint:      "http://127.0.0.1/token",
			Audience:           "urn:broker:tenant",
			GrantType:          defaultGrantType,
			SubjectTokenType:   defaultSubjectTokenType,
			RequestedTokenType: defaultRequestedTokenType,
			MachineAuthMethod:  MachineAuthPrivateKeyJWT,
			AllowedScopes:      []string{"orders.read"},
			Response: BrokerTokenResponse{
				AccessTokenPath: "access_token", TokenTypePath: "token_type",
				ExpiresInPath: "expires_in", ExpectedTokenType: "Bearer",
			},
			BusinessInjection: BusinessInjection{HeaderName: "Authorization", Prefix: "Bearer"},
		},
		Connection: ConnectionBrokerOBO{
			ClientID: "broker-client-1", Scopes: []string{"orders.read"},
			MaxTokenTTLSeconds: 300,
		},
		ActorType: "SERVICE_PRINCIPAL", ActorID: "actor-1",
		Machine: MachineCredential{PrivateKey: machine, Version: 3},
	}
}

func TestAssertionIssuerUSERAndExternalSubject(t *testing.T) {
	keys, _ := testAssertionKeys(t)
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	issuer, err := NewAssertionIssuer(keys, "https://actweave.example/outbound", clock)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		subjectType SubjectType
		subjectID   string
	}{
		{SubjectTypeUser, "user-uuid-1"},
		{SubjectTypeExternalSubject, "ext-uuid-1"},
	} {
		token, claims, err := issuer.Issue(AssertionIssueRequest{
			Audience: "urn:broker:tenant", WorkspaceID: "ws-1",
			ConnectionID: "conn-1", RootScopeID: "run-1",
			ActorType: "USER", ActorID: "user-uuid-1",
			SubjectType: tc.subjectType, SubjectID: tc.subjectID,
			Scopes: []string{"orders.read"},
		})
		if err != nil || token == "" {
			t.Fatalf("%s: %v", tc.subjectType, err)
		}
		if claims.Subject != tc.subjectID || claims.SubjectType != string(tc.subjectType) {
			t.Fatalf("claims subject mismatch: %+v", claims)
		}
		if claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time) > DefaultMaxAssertionTTL {
			t.Fatal("assertion TTL exceeds 60s")
		}
		if claims.ID == "" || claims.Issuer != "https://actweave.example/outbound" {
			t.Fatalf("unexpected iss/jti: %+v", claims)
		}
		// Parse header typ
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			t.Fatal("jwt parts")
		}
	}
}

func TestAssertionIssuerRejectsSYSTEMAndMissingSubject(t *testing.T) {
	keys, _ := testAssertionKeys(t)
	issuer, err := NewAssertionIssuer(keys, "https://actweave.example/outbound", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = issuer.Issue(AssertionIssueRequest{
		Audience: "urn:broker:tenant", WorkspaceID: "ws-1",
		ConnectionID: "conn-1", RootScopeID: "run-1",
		ActorType: "SYSTEM", ActorID: "system",
		// empty subject
	})
	if err == nil || !IsErrorCode(err, CodeSubjectRequired) {
		t.Fatalf("expected SUBJECT_REQUIRED, got %v", err)
	}
}

func TestBrokerExchangePrivateKeyJWTAndClaims(t *testing.T) {
	keys, _ := testAssertionKeys(t)
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	issuer, _ := NewAssertionIssuer(keys, "https://actweave.example/outbound", clock)
	machine := testMachineKey(t)

	var gotForm url.Values
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method %s", r.Method)
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, MaxBrokerResponseBytes))
		form, _ := url.ParseQuery(string(body))
		mu.Lock()
		gotForm = form
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "biz-token-canary-AAA",
			"token_type":   "Bearer",
			"expires_in":   120,
		})
	}))
	defer server.Close()

	client, err := NewBrokerClient(issuer,
		WithBrokerHTTPClient(server.Client()),
		WithBrokerClock(clock),
		WithBrokerAllowLoopbackHTTP(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	req := baseExchangeRequest(SubjectTypeExternalSubject, "ext-1", machine)
	req.Provider.TokenEndpoint = server.URL + "/token"
	token, err := client.Exchange(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer token.Zero()
	if string(token.AccessToken) != "biz-token-canary-AAA" {
		t.Fatalf("token %q", token.AccessToken)
	}

	mu.Lock()
	form := gotForm
	mu.Unlock()
	if form.Get("grant_type") != defaultGrantType {
		t.Fatalf("grant %q", form.Get("grant_type"))
	}
	if form.Get("client_assertion_type") != clientAssertionType {
		t.Fatal("missing client_assertion_type")
	}
	subjectJWT := form.Get("subject_token")
	clientJWT := form.Get("client_assertion")
	if subjectJWT == "" || clientJWT == "" {
		t.Fatal("missing assertions")
	}
	// Verify subject assertion claims without trusting alg from wire beyond EdDSA.
	// Use the same FakeClock so exp/nbf match issuance time (not wall clock).
	parsed, err := jwt.Parse(subjectJWT, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != OutboundSigningAlgorithm {
			t.Fatalf("unexpected alg %s", token.Method.Alg())
		}
		pub, err := keys.VerificationKey("outbound-test-key-1", clock.Now())
		return pub, err
	}, jwt.WithTimeFunc(clock.Now))
	if err != nil || !parsed.Valid {
		t.Fatalf("parse subject assertion: %v", err)
	}
	// Client assertion signed with machine key, aud = token endpoint.
	clientParsed, err := jwt.Parse(clientJWT, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != OutboundSigningAlgorithm {
			t.Fatalf("client alg %s", token.Method.Alg())
		}
		return machine.Public(), nil
	}, jwt.WithTimeFunc(clock.Now))
	if err != nil || !clientParsed.Valid {
		t.Fatalf("parse client assertion: %v", err)
	}
	claims, _ := clientParsed.Claims.(jwt.MapClaims)
	if claims["iss"] != "broker-client-1" || claims["sub"] != "broker-client-1" {
		t.Fatalf("client claims %+v", claims)
	}
}

func TestBrokerRejectsSYSTEMSubject(t *testing.T) {
	keys, _ := testAssertionKeys(t)
	issuer, _ := NewAssertionIssuer(keys, "https://actweave.example/outbound", nil)
	client, _ := NewBrokerClient(issuer, WithBrokerAllowLoopbackHTTP(true))
	req := baseExchangeRequest(SubjectTypeUser, "u1", testMachineKey(t))
	req.SubjectType = ""
	req.SubjectID = ""
	req.ActorType = "SYSTEM"
	_, err := client.Exchange(context.Background(), req)
	if err == nil || !IsErrorCode(err, CodeSubjectRequired) {
		t.Fatalf("expected subject required, got %v", err)
	}
}

func TestBrokerRejectsRedirectAndNonHTTPS(t *testing.T) {
	keys, _ := testAssertionKeys(t)
	issuer, _ := NewAssertionIssuer(keys, "https://actweave.example/outbound", nil)
	machine := testMachineKey(t)

	// Redirecting broker
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 60, "token_type": "Bearer"})
	}))
	defer final.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer redirect.Close()

	client, _ := NewBrokerClient(issuer,
		WithBrokerHTTPClient(&http.Client{
			// Explicit: would follow if not blocked
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Timeout:       5 * time.Second,
		}),
		WithBrokerAllowLoopbackHTTP(true),
	)
	req := baseExchangeRequest(SubjectTypeUser, "u1", machine)
	req.Provider.TokenEndpoint = redirect.URL
	_, err := client.Exchange(context.Background(), req)
	if err == nil || !IsErrorCode(err, CodeTargetRejected) {
		t.Fatalf("expected target rejected on redirect, got %v", err)
	}

	// Non-HTTPS non-loopback
	clientStrict, _ := NewBrokerClient(issuer, WithBrokerAllowLoopbackHTTP(false))
	req.Provider.TokenEndpoint = "http://evil.example/token"
	_, err = clientStrict.Exchange(context.Background(), req)
	if err == nil || !IsErrorCode(err, CodeTargetRejected) {
		t.Fatalf("expected target rejected for http, got %v", err)
	}
}

func TestBrokerDeniedNoRetryAnd5xxRetry(t *testing.T) {
	keys, _ := testAssertionKeys(t)
	issuer, _ := NewAssertionIssuer(keys, "https://actweave.example/outbound", nil)
	machine := testMachineKey(t)

	var calls403 atomic.Int32
	s403 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls403.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"denied"}`))
	}))
	defer s403.Close()

	client, _ := NewBrokerClient(issuer, WithBrokerHTTPClient(s403.Client()), WithBrokerAllowLoopbackHTTP(true))
	req := baseExchangeRequest(SubjectTypeUser, "u1", machine)
	req.Provider.TokenEndpoint = s403.URL
	_, err := client.Exchange(context.Background(), req)
	if err == nil || !IsErrorCode(err, CodeBrokerDenied) {
		t.Fatalf("expected broker denied: %v", err)
	}
	if calls403.Load() != 1 {
		t.Fatalf("403 must not retry: calls=%d", calls403.Load())
	}

	var calls5xx atomic.Int32
	s5 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls5xx.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ok", "token_type": "Bearer", "expires_in": 90,
		})
	}))
	defer s5.Close()
	client2, _ := NewBrokerClient(issuer, WithBrokerHTTPClient(s5.Client()), WithBrokerAllowLoopbackHTTP(true))
	req.Provider.TokenEndpoint = s5.URL
	token, err := client2.Exchange(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	token.Zero()
	if calls5xx.Load() != 2 {
		t.Fatalf("expected one retry on 5xx, calls=%d", calls5xx.Load())
	}
}

func TestBrokerResponseAnomalies(t *testing.T) {
	keys, _ := testAssertionKeys(t)
	issuer, _ := NewAssertionIssuer(keys, "https://actweave.example/outbound", nil)
	machine := testMachineKey(t)

	cases := []struct {
		name string
		body string
		code int
	}{
		{"missing token", `{"token_type":"Bearer","expires_in":60}`, 200},
		{"wrong type", `{"access_token":"x","token_type":"Mac","expires_in":60}`, 200},
		{"control char", `{"access_token":"a\nb","token_type":"Bearer","expires_in":60}`, 200},
		{"missing expiry", `{"access_token":"x","token_type":"Bearer"}`, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			client, _ := NewBrokerClient(issuer, WithBrokerHTTPClient(s.Client()), WithBrokerAllowLoopbackHTTP(true))
			req := baseExchangeRequest(SubjectTypeUser, "u1", machine)
			req.Provider.TokenEndpoint = s.URL
			_, err := client.Exchange(context.Background(), req)
			if err == nil {
				t.Fatal("expected failure")
			}
			// Response body must not appear in error string.
			if strings.Contains(err.Error(), "access_token") || strings.Contains(err.Error(), "a\nb") {
				t.Fatalf("upstream body leaked: %v", err)
			}
		})
	}
}

func TestBrokerCacheIsolationAndSingleflight(t *testing.T) {
	keys, _ := testAssertionKeys(t)
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	issuer, _ := NewAssertionIssuer(keys, "https://actweave.example/outbound", clock)
	machine := testMachineKey(t)

	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-" + strings.TrimPrefix(r.Header.Get("X-Ignore"), ""),
			"token_type":   "Bearer",
			"expires_in":   300,
		})
		// unique token per call
		_ = r.ParseForm()
		// rewrite with call number
	}))
	// Better unique tokens:
	s.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-" + string(rune('A'+n-1)),
			"token_type":   "Bearer",
			"expires_in":   300,
		})
	})
	defer s.Close()

	client, _ := NewBrokerClient(issuer,
		WithBrokerHTTPClient(s.Client()), WithBrokerClock(clock), WithBrokerAllowLoopbackHTTP(true),
	)
	cache := NewBrokerTokenCache(clock)

	reqA := baseExchangeRequest(SubjectTypeUser, "user-A", machine)
	reqA.Provider.TokenEndpoint = s.URL
	reqB := baseExchangeRequest(SubjectTypeUser, "user-B", machine)
	reqB.Provider.TokenEndpoint = s.URL
	reqB.RootScopeID = "run-2"
	reqA2 := baseExchangeRequest(SubjectTypeUser, "user-A", machine)
	reqA2.Provider.TokenEndpoint = s.URL
	reqA2.WorkspaceID = "ws-2"

	// Concurrent same key → singleflight one exchange
	var wg sync.WaitGroup
	results := make([]string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := cache.GetOrExchange(context.Background(), client, reqA)
			if err != nil {
				t.Errorf("exchange: %v", err)
				return
			}
			results[i] = string(tok.AccessToken)
			tok.Zero()
		}(i)
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("singleflight expected 1 call, got %d", calls.Load())
	}
	for _, r := range results {
		if r != results[0] {
			t.Fatalf("divergent tokens under singleflight: %v", results)
		}
	}

	// Different subject → new exchange
	tokB, err := cache.GetOrExchange(context.Background(), client, reqB)
	if err != nil {
		t.Fatal(err)
	}
	tokB.Zero()
	// Different workspace
	tokW, err := cache.GetOrExchange(context.Background(), client, reqA2)
	if err != nil {
		t.Fatal(err)
	}
	tokW.Zero()
	if calls.Load() != 3 {
		t.Fatalf("expected 3 exchanges (A singleflight + B + ws2), got %d", calls.Load())
	}

	// Policy version change does not hit old cache
	reqPolicy := reqA
	reqPolicy.ConnectionPolicyVersion = 99
	_, err = cache.GetOrExchange(context.Background(), client, reqPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Fatalf("policy invalidation expected new exchange, calls=%d", calls.Load())
	}

	// Invalidate root clears A
	cache.InvalidateRoot("boot-1", "ws-1", "run-1")
	_, err = cache.GetOrExchange(context.Background(), client, reqA)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 5 {
		t.Fatalf("after root invalidate expected re-exchange, calls=%d", calls.Load())
	}

	// Secret version change
	reqSecret := reqA
	reqSecret.Machine.Version = 99
	_, err = cache.GetOrExchange(context.Background(), client, reqSecret)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 6 {
		t.Fatalf("secret version change expected new exchange, calls=%d", calls.Load())
	}
}

func TestBrokerCacheNoCrossRun(t *testing.T) {
	keys, _ := testAssertionKeys(t)
	clock := NewFakeClock(time.Now().UTC())
	issuer, _ := NewAssertionIssuer(keys, "https://actweave.example/outbound", clock)
	machine := testMachineKey(t)
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "run-tok-" + string(rune('0'+n)),
			"token_type":   "Bearer", "expires_in": 200,
		})
	}))
	defer s.Close()
	client, _ := NewBrokerClient(issuer, WithBrokerHTTPClient(s.Client()), WithBrokerClock(clock), WithBrokerAllowLoopbackHTTP(true))
	cache := NewBrokerTokenCache(clock)
	req1 := baseExchangeRequest(SubjectTypeUser, "u1", machine)
	req1.Provider.TokenEndpoint = s.URL
	req1.RootScopeID = "run-1"
	req2 := req1
	req2.RootScopeID = "run-2"
	t1, _ := cache.GetOrExchange(context.Background(), client, req1)
	t2, _ := cache.GetOrExchange(context.Background(), client, req2)
	if string(t1.AccessToken) == string(t2.AccessToken) {
		t.Fatal("cross-run cache hit is forbidden")
	}
	t1.Zero()
	t2.Zero()
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestJWKSPublicOnlyAndRotationWindow(t *testing.T) {
	_, priv1, _ := ed25519.GenerateKey(rand.Reader)
	_, priv2, _ := ed25519.GenerateKey(rand.Reader)
	keys, err := NewRotatingSigningKeyProvider("kid-a", priv1, DefaultMaxAssertionTTL)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := keys.Rotate("kid-b", priv2, now); err != nil {
		t.Fatal(err)
	}
	set, err := keys.PublicJWKS(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Keys) != 2 {
		t.Fatalf("expected active+retired, got %d", len(set.Keys))
	}
	raw, _ := json.Marshal(set)
	if strings.Contains(string(raw), `"d"`) || strings.Contains(strings.ToLower(string(raw)), "private") {
		t.Fatalf("private material in JWKS: %s", raw)
	}
	// After full retention window, old key drops.
	later := now.Add(DefaultMaxAssertionTTL + DefaultAssertionClockSkew + DefaultBrokerJWKSCacheWindow + time.Second)
	set2, err := keys.PublicJWKS(later)
	if err != nil {
		t.Fatal(err)
	}
	if len(set2.Keys) != 1 || set2.Keys[0].KeyID != "kid-b" {
		t.Fatalf("expected only active after retention: %+v", set2.Keys)
	}
}

func TestBrokerNetworkGuardRejectsPrivateAndMetadata(t *testing.T) {
	// Direct IP literal private / metadata targets fail closed at ValidateURL.
	for _, raw := range []string{
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.1/token",
		"https://192.168.1.1/token",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		g, err := NewBrokerNetworkGuard(u, false, nil)
		if err != nil {
			// construction may also reject — either is fine
			continue
		}
		if err := g.ValidateURL(context.Background(), u); err == nil {
			t.Fatalf("expected private/metadata rejection for %s", raw)
		}
	}
	// Non-HTTPS non-loopback rejected at construction.
	if _, err := NewBrokerNetworkGuard(mustURL(t, "http://evil.example/token"), false, nil); err == nil {
		t.Fatal("expected http non-loopback rejection")
	}
	// Hostname resolving only to private IP fails.
	resolver := staticResolver{
		"broker.internal": {{IP: net.ParseIP("10.1.2.3")}},
		"rebind.example":  {{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}},
	}
	u, _ := url.Parse("https://broker.internal/token")
	g, err := NewBrokerNetworkGuard(u, false, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.ValidateURL(context.Background(), u); err == nil {
		t.Fatal("expected private IP rejection")
	}
	// DNS rebinding: any disallowed IP in set fails closed.
	u2, _ := url.Parse("https://rebind.example/token")
	g2, err := NewBrokerNetworkGuard(u2, false, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if err := g2.ValidateURL(context.Background(), u2); err == nil {
		t.Fatal("expected DNS rebinding rejection")
	}
	// Public unicast allowed.
	pub := staticResolver{"broker.example": {{IP: net.ParseIP("203.0.114.10")}}}
	// 203.0.113.0/24 is documentation — use a non-restricted public.
	pub = staticResolver{"broker.example": {{IP: net.ParseIP("1.1.1.1")}}}
	u3, _ := url.Parse("https://broker.example/token")
	g3, err := NewBrokerNetworkGuard(u3, false, pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := g3.ValidateURL(context.Background(), u3); err != nil {
		t.Fatalf("public endpoint should pass: %v", err)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestCanaryNotInErrors(t *testing.T) {
	keys, _ := testAssertionKeys(t)
	issuer, _ := NewAssertionIssuer(keys, "https://actweave.example/outbound", nil)
	machine := testMachineKey(t)
	canary := "CANARY_SECRET_TOKEN_XYZ"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"` + canary + `","access_token":"` + canary + `"}`))
	}))
	defer s.Close()
	client, _ := NewBrokerClient(issuer, WithBrokerHTTPClient(s.Client()), WithBrokerAllowLoopbackHTTP(true))
	req := baseExchangeRequest(SubjectTypeUser, "u1", machine)
	req.Provider.TokenEndpoint = s.URL
	_, err := client.Exchange(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("canary leaked in error: %v", err)
	}
}

// IsErrorCode reports whether err is an outbound *Error with the given code.
func IsErrorCode(err error, code string) bool {
	var oe *Error
	if err == nil {
		return false
	}
	if !asOutboundError(err, &oe) {
		return false
	}
	return oe.Code == code
}

func asOutboundError(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
