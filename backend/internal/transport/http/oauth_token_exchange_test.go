package httptransport

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"actweave/backend/internal/agentaccessauth"
)

func TestOAuthTokenExchangeHTTPIssuesDelegatedTokenAndRejectsForgery(t *testing.T) {
	client := tokenEndpointTestClient()
	secrets := &tokenClientSecretStub{client: client}
	exchange := &tokenExchangeStub{
		token: agentaccessauth.TokenExchangeToken{
			AccessToken: "exchanged-access-token", IssuedTokenType: agentaccessauth.IssuedTokenTypeAccessToken,
			TokenType: "Bearer", ExpiresIn: 600, Scope: "run:create event:read",
		},
	}
	routes, err := NewAgentAccessTokenRoutes(
		secrets, &tokenPrivateKeyStub{err: agentaccessauth.ErrInvalidClient},
		&tokenIssuerStub{}, exchange,
	)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{AgentAccessRegistrars: []AgentAccessV1RouteRegistrar{routes}})
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{}
	form.Set("grant_type", agentaccessauth.TokenExchangeGrantType)
	form.Set("agent_id", "d48f1f2e-7b5a-7c3d-8e9f-123456789010")
	form.Set("scope", "run:create event:read")
	form.Set("subject_token", "subject-token-value")
	form.Set("subject_token_type", agentaccessauth.SubjectTokenTypeJWT)
	request := httptest.NewRequest(http.MethodPost, "/api/agent-access/v1/oauth/token",
		strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("client:secret")))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body oauthTokenSuccess
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.AccessToken != "exchanged-access-token" ||
		body.IssuedTokenType != agentaccessauth.IssuedTokenTypeAccessToken ||
		body.TokenType != "Bearer" || body.ExpiresIn != 600 ||
		body.Scope != "run:create event:read" ||
		recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("body=%+v headers=%v", body, recorder.Header())
	}
	if exchange.calls != 1 || secrets.calls != 1 {
		t.Fatalf("exchange calls=%d secret calls=%d", exchange.calls, secrets.calls)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("subject-token-value")) {
		t.Fatal("response leaked subject token")
	}

	// Invalid subject token path.
	exchange.err = agentaccessauth.ErrTokenExchangeSubjectInvalid
	exchange.token = agentaccessauth.TokenExchangeToken{}
	badRequest := httptest.NewRequest(http.MethodPost, "/api/agent-access/v1/oauth/token",
		strings.NewReader(form.Encode()))
	badRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badRequest.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("client:secret")))
	bad := httptest.NewRecorder()
	router.ServeHTTP(bad, badRequest)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), `"error":"invalid_grant"`) {
		t.Fatalf("invalid subject status=%d body=%s", bad.Code, bad.Body.String())
	}
	if strings.Contains(bad.Body.String(), "subject-token-value") {
		t.Fatal("error response leaked subject token")
	}

	// Unsupported grant remains rejected.
	unsupported := url.Values{}
	unsupported.Set("grant_type", "password")
	unsupported.Set("agent_id", "d48f1f2e-7b5a-7c3d-8e9f-123456789010")
	unsupported.Set("scope", "run:create")
	req := httptest.NewRequest(http.MethodPost, "/api/agent-access/v1/oauth/token",
		strings.NewReader(unsupported.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("client:secret")))
	out := httptest.NewRecorder()
	router.ServeHTTP(out, req)
	if out.Code != http.StatusBadRequest || !strings.Contains(out.Body.String(), "unsupported_grant_type") {
		t.Fatalf("unsupported grant status=%d body=%s", out.Code, out.Body.String())
	}
}
