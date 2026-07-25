package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/transport/sse"
)

// TestAAPAbuseControls is the M10-T3 HTTP gate: exact CORS, BFF-disabled CORS,
// multi-dimensional command/SSE quotas, connection leases, input/metadata limits,
// and Retry/RateLimit headers without resource leakage.
func TestAAPAbuseControls(t *testing.T) {
	t.Run("ExactCORSAllowsRegisteredOriginOnly", testAbuseExactCORS)
	t.Run("BFFDisablesCORSAndDoesNotEchoOrigin", testAbuseBFFCORS)
	t.Run("PreflightRejectsWildcardMethodsAndHeaders", testAbusePreflightTight)
	t.Run("AuthFailureStillEmitsCORSForRegisteredOrigin", testAbuseAuthFailureCORS)
	t.Run("ClientMatcherAuthFailureEmitsWorkspaceScopedCORS", testAbuseClientMatcherAuthFailureCORS)
	t.Run("ClientMatcherSuccessIsolatesACAOByAzp", testAbuseClientMatcherSuccessIsolatesCORS)
	t.Run("RunQuotaSetsRetryAndRateLimitHeaders", testAbuseRunQuotaHeaders)
	t.Run("SSEStreamQuotaIsSeparateFromCommandQuota", testAbuseSSEStreamQuota)
	t.Run("SSEConnectionLimiterBoundsConcurrentStreams", testAbuseSSEConnectionLimit)
	t.Run("TokenIssueLimiterReturns429WithoutClientLeak", testAbuseTokenIssueLimiter)
	t.Run("MetadataAndBodySizeLimits", testAbuseInputLimits)
}

func testAbuseExactCORS(t *testing.T) {
	policy, err := agentaccessauth.NewExactCORSPolicy([]string{"https://app.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: aapRunRouteAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{&AAPAgentProfileRoutes{}},
		AAPCORS:                  policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agent-access/v1/workspaces/" + aapRunWorkspaceID + "/agents/" + aapRunAgentID + "/profile"

	ok := httptest.NewRequest(http.MethodGet, path, nil)
	ok.Header.Set("Origin", "https://app.example.test")
	ok.Header.Set("Authorization", "Bearer subject-a")
	okRec := httptest.NewRecorder()
	router.ServeHTTP(okRec, ok)
	if okRec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.test" {
		t.Fatalf("missing exact ACAO headers=%v", okRec.Header())
	}
	if strings.Contains(okRec.Header().Get("Access-Control-Allow-Origin"), "*") {
		t.Fatal("star origin emitted")
	}

	evil := httptest.NewRequest(http.MethodGet, path, nil)
	evil.Header.Set("Origin", "https://evil.example.test")
	evil.Header.Set("Authorization", "Bearer subject-a")
	evilRec := httptest.NewRecorder()
	router.ServeHTTP(evilRec, evil)
	if acao := evilRec.Header().Get("Access-Control-Allow-Origin"); acao != "" {
		t.Fatalf("echoed unauthorized origin %q", acao)
	}
}

func testAbuseBFFCORS(t *testing.T) {
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: aapRunRouteAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{&AAPAgentProfileRoutes{}},
		AAPCORS:                  agentaccessauth.NewDisabledCORSPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agent-access/v1/workspaces/" + aapRunWorkspaceID + "/agents/" + aapRunAgentID + "/profile"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("Authorization", "Bearer subject-a")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	for _, key := range []string{
		"Access-Control-Allow-Origin", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers",
	} {
		if rec.Header().Get(key) != "" {
			t.Fatalf("BFF mode emitted %s=%q", key, rec.Header().Get(key))
		}
	}
}

// authFailureAuthenticator always rejects tokens so we can assert CORS on 401.
type authFailureAuthenticator struct {
	expired bool
}

func (authFailureAuthenticator) VerifyAccessToken(
	context.Context, string,
) (agentaccessauth.AAPAccessTokenPrincipal, error) {
	return agentaccessauth.AAPAccessTokenPrincipal{}, agentaccessauth.ErrTokenExpired
}

func testAbuseAuthFailureCORS(t *testing.T) {
	policy, err := agentaccessauth.NewExactCORSPolicy([]string{"https://app.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: authFailureAuthenticator{expired: true},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{&AAPAgentProfileRoutes{}},
		AAPCORS:                  policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agent-access/v1/workspaces/" + aapRunWorkspaceID + "/agents/" + aapRunAgentID + "/profile"

	// Expired token + registered Origin: browser must still see ACAO + body code.
	expired := httptest.NewRequest(http.MethodGet, path, nil)
	expired.Header.Set("Origin", "https://app.example.test")
	expired.Header.Set("Authorization", "Bearer expired-token")
	expiredRec := httptest.NewRecorder()
	router.ServeHTTP(expiredRec, expired)
	if expiredRec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", expiredRec.Code, expiredRec.Body.String())
	}
	if expiredRec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.test" {
		t.Fatalf("auth failure missing ACAO: headers=%v", expiredRec.Header())
	}
	if !strings.Contains(expiredRec.Body.String(), `"code":"TOKEN_EXPIRED"`) {
		t.Fatalf("expected TOKEN_EXPIRED body: %s", expiredRec.Body.String())
	}

	// Unauthorized origin must not be echoed even on 401.
	evil := httptest.NewRequest(http.MethodGet, path, nil)
	evil.Header.Set("Origin", "https://evil.example.test")
	evil.Header.Set("Authorization", "Bearer expired-token")
	evilRec := httptest.NewRecorder()
	router.ServeHTTP(evilRec, evil)
	if acao := evilRec.Header().Get("Access-Control-Allow-Origin"); acao != "" {
		t.Fatalf("echoed unauthorized origin on 401: %q", acao)
	}
}

// clientMatcherForCORSTest isolates origins per public client + workspace.
type clientMatcherForCORSTest struct {
	clientOrigins    map[string]map[string]struct{}
	workspaceOrigins map[string]map[string]struct{}
	anyOrigins       map[string]struct{}
}

func (m *clientMatcherForCORSTest) AllowsExactOrigin(origin string) bool {
	_, ok := m.anyOrigins[origin]
	return ok
}

func (m *clientMatcherForCORSTest) AllowsOriginForClient(origin, publicClientID string) bool {
	set, ok := m.clientOrigins[publicClientID]
	if !ok {
		return false
	}
	_, ok = set[origin]
	return ok
}

func (m *clientMatcherForCORSTest) AllowsOriginForWorkspace(origin, workspaceID string) bool {
	set, ok := m.workspaceOrigins[workspaceID]
	if !ok {
		return false
	}
	_, ok = set[origin]
	return ok
}

func testAbuseClientMatcherAuthFailureCORS(t *testing.T) {
	// Production path: ClientMatcher present, so path middleware used to skip
	// CORS entirely until post-auth — which never runs on 401.
	matcher := &clientMatcherForCORSTest{
		anyOrigins: map[string]struct{}{"https://app.example.test": {}},
		clientOrigins: map[string]map[string]struct{}{
			"awcl_client_a": {"https://app.example.test": {}},
		},
		workspaceOrigins: map[string]map[string]struct{}{
			aapRunWorkspaceID: {"https://app.example.test": {}},
		},
	}
	policy := agentaccessauth.CORSPolicy{
		Mode: agentaccessauth.CORSModeExact, Matcher: matcher, ClientMatcher: matcher,
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: authFailureAuthenticator{expired: true},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{&AAPAgentProfileRoutes{}},
		AAPCORS:                  policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agent-access/v1/workspaces/" + aapRunWorkspaceID + "/agents/" + aapRunAgentID + "/profile"

	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("Authorization", "Bearer expired-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.test" {
		t.Fatalf("ClientMatcher auth failure missing workspace-scoped ACAO: %v", rec.Header())
	}
	if !strings.Contains(rec.Body.String(), `"code":"TOKEN_EXPIRED"`) {
		t.Fatalf("SDK must read TOKEN_EXPIRED: %s", rec.Body.String())
	}

	// Origin registered only for another workspace must not be reflected.
	other := httptest.NewRequest(http.MethodGet, path, nil)
	other.Header.Set("Origin", "https://other.example.test")
	other.Header.Set("Authorization", "Bearer expired-token")
	otherRec := httptest.NewRecorder()
	router.ServeHTTP(otherRec, other)
	if acao := otherRec.Header().Get("Access-Control-Allow-Origin"); acao != "" {
		t.Fatalf("reflected foreign-workspace origin on 401: %q", acao)
	}
}

// azpScopedAuthenticator issues Tokens for Client A or B based on the Bearer value.
// Used to prove post-auth CORS rewrites by azp rather than workspace union.
type azpScopedAuthenticator struct {
	azpByToken map[string]string
	expired    map[string]bool
}

func (a azpScopedAuthenticator) VerifyAccessToken(
	_ context.Context,
	value string,
) (agentaccessauth.AAPAccessTokenPrincipal, error) {
	if a.expired != nil && a.expired[value] {
		return agentaccessauth.AAPAccessTokenPrincipal{}, agentaccessauth.ErrTokenExpired
	}
	azp, ok := a.azpByToken[value]
	if !ok {
		return agentaccessauth.AAPAccessTokenPrincipal{}, agentaccessauth.ErrInvalidAAPAccessToken
	}
	now := time.Now().UTC()
	return agentaccessauth.AAPAccessTokenPrincipal{
		PrincipalID: aapRunSubjectAID, ServicePrincipalID: aapRunServiceID,
		AuthorizedParty: azp, WorkspaceID: aapRunWorkspaceID, AgentID: aapRunAgentID,
		Scopes:          []string{"run:read"},
		SecurityVersion: 1, TokenID: aapRunTokenID,
		IssuedAt: now.Add(-time.Minute), NotBefore: now.Add(-time.Minute),
		ExpiresAt: now.Add(10 * time.Minute),
	}, nil
}

// testAbuseClientMatcherSuccessIsolatesCORS is the P1 gate:
//   - A Origin + A Token → success keeps ACAO for A
//   - A Origin + B Token → success must NOT keep pre-auth workspace ACAO
//   - A Origin + expired Token → 401 still has ACAO so SDK can read TOKEN_EXPIRED
func testAbuseClientMatcherSuccessIsolatesCORS(t *testing.T) {
	const (
		clientA = "awcl_client_a_isolation"
		clientB = "awcl_client_b_isolation"
		originA = "https://client-a.example.test"
		originB = "https://client-b.example.test"
	)
	matcher := &clientMatcherForCORSTest{
		anyOrigins: map[string]struct{}{originA: {}, originB: {}},
		clientOrigins: map[string]map[string]struct{}{
			clientA: {originA: {}},
			clientB: {originB: {}},
		},
		workspaceOrigins: map[string]map[string]struct{}{
			// Workspace union includes both origins — pre-auth would reflect either.
			aapRunWorkspaceID: {originA: {}, originB: {}},
		},
	}
	policy := agentaccessauth.CORSPolicy{
		Mode: agentaccessauth.CORSModeExact, Matcher: matcher, ClientMatcher: matcher,
	}
	auth := azpScopedAuthenticator{
		azpByToken: map[string]string{
			"token-a": clientA,
			"token-b": clientB,
		},
		expired: map[string]bool{"token-expired": true},
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: auth,
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{&AAPAgentProfileRoutes{}},
		AAPCORS:                  policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agent-access/v1/workspaces/" + aapRunWorkspaceID + "/agents/" + aapRunAgentID + "/profile"

	// A Origin + A Token: auth succeeds; post-auth rewrite keeps Client A's ACAO.
	// (Handler may still 401 for missing profile deps — CORS headers are set earlier.)
	match := httptest.NewRequest(http.MethodGet, path, nil)
	match.Header.Set("Origin", originA)
	match.Header.Set("Authorization", "Bearer token-a")
	matchRec := httptest.NewRecorder()
	router.ServeHTTP(matchRec, match)
	if matchRec.Header().Get("Access-Control-Allow-Origin") != originA {
		t.Fatalf("A Origin + A Token must keep ACAO=%q, status=%d headers=%v body=%s",
			originA, matchRec.Code, matchRec.Header(), matchRec.Body.String())
	}

	// A Origin + B Token: pre-auth would set ACAO (workspace owns A), but post-auth
	// must clear it because Client B did not register origin A.
	cross := httptest.NewRequest(http.MethodGet, path, nil)
	cross.Header.Set("Origin", originA)
	cross.Header.Set("Authorization", "Bearer token-b")
	crossRec := httptest.NewRecorder()
	router.ServeHTTP(crossRec, cross)
	if acao := crossRec.Header().Get("Access-Control-Allow-Origin"); acao != "" {
		t.Fatalf("A Origin + B Token must not expose ACAO (got %q); browser isolation broken; body=%s",
			acao, crossRec.Body.String())
	}

	// B Origin + B Token: auth success reflects Client B only.
	bMatch := httptest.NewRequest(http.MethodGet, path, nil)
	bMatch.Header.Set("Origin", originB)
	bMatch.Header.Set("Authorization", "Bearer token-b")
	bMatchRec := httptest.NewRecorder()
	router.ServeHTTP(bMatchRec, bMatch)
	if bMatchRec.Header().Get("Access-Control-Allow-Origin") != originB {
		t.Fatalf("B Origin + B Token must keep ACAO=%q, headers=%v",
			originB, bMatchRec.Header())
	}

	// Expired Token: auth fails before client rewrite; pre-auth workspace CORS
	// stays so TOKEN_EXPIRED remains browser-readable.
	expired := httptest.NewRequest(http.MethodGet, path, nil)
	expired.Header.Set("Origin", originA)
	expired.Header.Set("Authorization", "Bearer token-expired")
	expiredRec := httptest.NewRecorder()
	router.ServeHTTP(expiredRec, expired)
	if expiredRec.Code != http.StatusUnauthorized {
		t.Fatalf("expired status=%d body=%s", expiredRec.Code, expiredRec.Body.String())
	}
	if expiredRec.Header().Get("Access-Control-Allow-Origin") != originA {
		t.Fatalf("expired Token must still reflect workspace-scoped ACAO: %v", expiredRec.Header())
	}
	if !strings.Contains(expiredRec.Body.String(), `"code":"TOKEN_EXPIRED"`) {
		t.Fatalf("SDK must read TOKEN_EXPIRED on 401: %s", expiredRec.Body.String())
	}
}

func testAbusePreflightTight(t *testing.T) {
	policy, err := agentaccessauth.NewExactCORSPolicy([]string{"https://app.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: aapRunRouteAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{&AAPAgentProfileRoutes{}},
		AAPCORS:                  policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agent-access/v1/workspaces/" + aapRunWorkspaceID + "/agents/" + aapRunAgentID + "/profile"

	// Allowed preflight.
	pre := httptest.NewRequest(http.MethodOptions, path, nil)
	pre.Header.Set("Origin", "https://app.example.test")
	pre.Header.Set("Access-Control-Request-Method", "GET")
	pre.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	preRec := httptest.NewRecorder()
	router.ServeHTTP(preRec, pre)
	if preRec.Code != http.StatusNoContent ||
		preRec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.test" ||
		!strings.Contains(preRec.Header().Get("Access-Control-Allow-Methods"), "GET") ||
		strings.Contains(preRec.Header().Get("Access-Control-Allow-Methods"), "*") {
		t.Fatalf("allowed preflight status=%d headers=%v", preRec.Code, preRec.Header())
	}

	// Disallowed method.
	badMethod := httptest.NewRequest(http.MethodOptions, path, nil)
	badMethod.Header.Set("Origin", "https://app.example.test")
	badMethod.Header.Set("Access-Control-Request-Method", "DELETE")
	badRec := httptest.NewRecorder()
	router.ServeHTTP(badRec, badMethod)
	if badRec.Code != http.StatusForbidden || badRec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("DELETE preflight status=%d headers=%v", badRec.Code, badRec.Header())
	}

	// Disallowed custom header.
	badHeader := httptest.NewRequest(http.MethodOptions, path, nil)
	badHeader.Header.Set("Origin", "https://app.example.test")
	badHeader.Header.Set("Access-Control-Request-Method", "POST")
	badHeader.Header.Set("Access-Control-Request-Headers", "X-Evil-Token")
	badHeaderRec := httptest.NewRecorder()
	router.ServeHTTP(badHeaderRec, badHeader)
	if badHeaderRec.Code != http.StatusForbidden {
		t.Fatalf("custom header preflight status=%d", badHeaderRec.Code)
	}

	// Unauthorized origin preflight must not echo.
	evil := httptest.NewRequest(http.MethodOptions, path, nil)
	evil.Header.Set("Origin", "https://evil.example.test")
	evil.Header.Set("Access-Control-Request-Method", "GET")
	evilRec := httptest.NewRecorder()
	router.ServeHTTP(evilRec, evil)
	if evilRec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("evil preflight echoed origin: %v", evilRec.Header())
	}
}

func testAbuseRunQuotaHeaders(t *testing.T) {
	reader := &aapRunRouteReader{}
	application := &aapRunRouteApplication{reader: reader}
	attacher, err := NewAAPEventCatchUp(reader)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewAAPRunRoutes(
		&aapRunRouteAuthorizer{}, &aapRunRouteConversations{}, application,
		reader, &aapRunRouteItems{}, attacher,
	)
	if err != nil {
		t.Fatal(err)
	}
	quota, err := agentaccess.NewInMemoryDataPlaneQuota(agentaccess.DataPlaneQuotaConfig{
		Window: time.Minute, MaxEntries: 100,
		Limits: map[agentaccess.DataPlaneQuotaOperation]int{
			agentaccess.QuotaRunCreate: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.ConfigureCommandQuota(quota); err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: aapRunRouteAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agent-access/v1/workspaces/" + aapRunWorkspaceID + "/agents/" + aapRunAgentID + "/runs"
	body := map[string]any{
		"conversationId": aapRunConversationID,
		"input": []any{map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "text", "text": "hello"}},
		}},
		"stream": false, "metadata": map[string]string{"businessRequestId": "ORDER-1"},
	}
	first := requestAAPRun(t, router, http.MethodPost, path, body,
		"subject-a", "d41f1f2e-7b5a-7c3d-8e9f-123456789051", "application/json", "")
	if first.Code != http.StatusAccepted || first.Header().Get("RateLimit-Limit") == "" {
		t.Fatalf("first status=%d headers=%v", first.Code, first.Header())
	}
	second := requestAAPRun(t, router, http.MethodPost, path, body,
		"subject-a", "d41f1f2e-7b5a-7c3d-8e9f-123456789052", "application/json", "")
	assertAAPRouterError(t, second, http.StatusTooManyRequests, "RATE_LIMITED")
	if second.Header().Get("Retry-After") == "" || second.Header().Get("RateLimit-Remaining") != "0" {
		t.Fatalf("missing rate headers: %v", second.Header())
	}
	// Body must not leak subject/workspace identifiers beyond standard error envelope.
	lower := strings.ToLower(second.Body.String())
	if strings.Contains(lower, strings.ToLower(aapRunWorkspaceID)) ||
		strings.Contains(lower, "subject-a") ||
		strings.Contains(lower, "rate_bucket") {
		t.Fatalf("rate limit body leaked resource detail: %s", second.Body.String())
	}
}

func testAbuseSSEStreamQuota(t *testing.T) {
	events := &createRunEventReader{}
	events.append(createRunProtocolEvent(t, 1, protocolevent.EventRunAccepted))
	events.append(createRunProtocolEvent(t, 2, protocolevent.EventRunCompleted))
	attacher, err := NewAAPEventCatchUp(events)
	if err != nil {
		t.Fatal(err)
	}
	runReader := &aapRunEventsResourceReader{run: execution.AgentRun{
		ID: createRunID, WorkspaceID: catchUpWorkspaceID, AgentID: catchUpAgentID,
		SessionID: createConversationID, Status: "SUCCEEDED",
		StartedAt: time.Now().UTC(), LockVersion: 2,
	}}
	routes, err := NewAAPRunRoutes(
		&aapRunEventsAuthorizer{}, &aapRunRouteConversations{}, &aapRunRouteApplication{},
		runReader, &aapRunRouteItems{}, attacher,
	)
	if err != nil {
		t.Fatal(err)
	}
	quota, err := agentaccess.NewInMemoryDataPlaneQuota(agentaccess.DataPlaneQuotaConfig{
		Window: time.Minute, MaxEntries: 100,
		Limits: map[agentaccess.DataPlaneQuotaOperation]int{
			agentaccess.QuotaEventStream: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.ConfigureCommandQuota(quota); err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: aapRunEventsAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agent-access/v1/workspaces/" + catchUpWorkspaceID +
		"/agents/" + catchUpAgentID + "/runs/" + createRunID + "/events"
	first := requestAAPRunEvents(router, path, "fresh-a", "", "text/event-stream")
	if first.Code != http.StatusOK {
		t.Fatalf("first stream status=%d body=%s", first.Code, first.Body.String())
	}
	second := requestAAPRunEvents(router, path, "fresh-a", "", "text/event-stream")
	assertAAPRouterError(t, second, http.StatusTooManyRequests, "RATE_LIMITED")
	if second.Header().Get("Retry-After") == "" {
		t.Fatalf("SSE quota missing Retry-After: %v", second.Header())
	}
}

func testAbuseSSEConnectionLimit(t *testing.T) {
	policy := sse.DefaultBackpressurePolicy()
	policy.MaxConnectionsPerClient = 1
	policy.MaxConnectionsPerSubject = 1
	policy.MaxConnectionsPerRun = 1
	limiter, err := sse.NewInMemoryConnectionLimiter(policy)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	id := sse.ConnectionIdentity{ClientID: "c1", SubjectID: "s1", RunID: "r1"}
	lease, err := limiter.Acquire(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Acquire(ctx, id); !errorsIsConnectionLimit(err) {
		t.Fatalf("second lease err=%v", err)
	}
	stats := limiter.Stats()
	if stats.Rejected < 1 || stats.Active != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func testAbuseTokenIssueLimiter(t *testing.T) {
	client := tokenEndpointTestClient()
	secrets := &tokenClientSecretStub{client: client}
	routes, err := NewAgentAccessTokenRoutes(
		secrets, &tokenPrivateKeyStub{err: agentaccessauth.ErrInvalidClient},
		&tokenIssuerStub{token: agentaccessauth.ClientCredentialsToken{
			AccessToken: "tok", TokenType: "Bearer", ExpiresIn: 600, Scope: "run:read",
		}},
		&tokenExchangeStub{},
	)
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := agentaccessauth.NewInMemoryTokenEndpointLimiter(agentaccessauth.TokenEndpointLimiterConfig{
		MaxIssues: 1, Window: time.Minute, MaxEntries: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.ConfigureTokenIssueLimiter(limiter); err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{AgentAccessRegistrars: []AgentAccessV1RouteRegistrar{routes}})
	if err != nil {
		t.Fatal(err)
	}
	form := "grant_type=client_credentials&agent_id=d48f1f2e-7b5a-7c3d-8e9f-123456789010&scope=run%3Aread"
	first := httptest.NewRequest(http.MethodPost, "/api/agent-access/v1/oauth/token", strings.NewReader(form))
	first.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	first.Header.Set("Authorization", "Basic "+basicAuthClientSecret())
	first.RemoteAddr = "203.0.113.20:1234"
	firstRec := httptest.NewRecorder()
	router.ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first token status=%d body=%s", firstRec.Code, firstRec.Body.String())
	}
	second := httptest.NewRequest(http.MethodPost, "/api/agent-access/v1/oauth/token", strings.NewReader(form))
	second.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	second.Header.Set("Authorization", "Basic "+basicAuthClientSecret())
	second.RemoteAddr = "203.0.113.20:1234"
	secondRec := httptest.NewRecorder()
	router.ServeHTTP(secondRec, second)
	if secondRec.Code != http.StatusTooManyRequests {
		t.Fatalf("second token status=%d body=%s", secondRec.Code, secondRec.Body.String())
	}
	if secondRec.Header().Get("Retry-After") == "" || secondRec.Header().Get("RateLimit-Limit") == "" {
		t.Fatalf("token limit headers=%v", secondRec.Header())
	}
	if strings.Contains(secondRec.Body.String(), client.PublicClientID) ||
		strings.Contains(secondRec.Body.String(), "203.0.113.20") {
		t.Fatalf("token limit body leaked identity: %s", secondRec.Body.String())
	}
}

func testAbuseInputLimits(t *testing.T) {
	// OAuth form max is enforced; oversized body is invalid_request without echo of body.
	client := tokenEndpointTestClient()
	routes, err := NewAgentAccessTokenRoutes(
		&tokenClientSecretStub{client: client},
		&tokenPrivateKeyStub{err: agentaccessauth.ErrInvalidClient},
		&tokenIssuerStub{}, &tokenExchangeStub{},
	)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{AgentAccessRegistrars: []AgentAccessV1RouteRegistrar{routes}})
	if err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("a", 40*1024)
	req := httptest.NewRequest(http.MethodPost, "/api/agent-access/v1/oauth/token", strings.NewReader("grant_type=client_credentials&pad="+huge))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+basicAuthClientSecret())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("oversized oauth form accepted")
	}
	if strings.Contains(rec.Body.String(), huge[:32]) {
		t.Fatalf("error echoed oversized body: %s", rec.Body.String())
	}

	// Run metadata size/secret limits are shared (already enforced on create run path).
	reader := &aapRunRouteReader{}
	application := &aapRunRouteApplication{reader: reader}
	attacher, err := NewAAPEventCatchUp(reader)
	if err != nil {
		t.Fatal(err)
	}
	runRoutes, err := NewAAPRunRoutes(
		&aapRunRouteAuthorizer{}, &aapRunRouteConversations{}, application,
		reader, &aapRunRouteItems{}, attacher,
	)
	if err != nil {
		t.Fatal(err)
	}
	runRouter, err := NewRouter(Config{
		AgentAccessAuthenticator: aapRunRouteAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{runRoutes},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agent-access/v1/workspaces/" + aapRunWorkspaceID + "/agents/" + aapRunAgentID + "/runs"
	secretMeta := requestAAPRun(t, runRouter, http.MethodPost, path, map[string]any{
		"conversationId": aapRunConversationID,
		"input": []any{map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "text", "text": "hi"}},
		}},
		"stream": false,
		"metadata": map[string]string{
			"authorization": "Bearer not-allowed",
		},
	}, "subject-a", "d41f1f2e-7b5a-7c3d-8e9f-123456789060", "application/json", "")
	assertAAPRouterError(t, secretMeta, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	if application.sideEffects != 0 {
		t.Fatal("secret metadata consumed run side effect")
	}
}

func errorsIsConnectionLimit(err error) bool {
	return err != nil && strings.Contains(err.Error(), "connection limit")
}

func basicAuthClientSecret() string {
	return "Y2xpZW50OnNlY3JldA==" // client:secret
}
