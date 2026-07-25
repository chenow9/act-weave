package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"actweave/backend/internal/agentaccessauth"
)

func TestClientCredentialsTokenEndpointSupportsBasicAndPrivateKeyJWT(t *testing.T) {
	client := tokenEndpointTestClient()
	for _, authentication := range []string{"client_secret_basic", "private_key_jwt"} {
		t.Run(authentication, func(t *testing.T) {
			secrets := &tokenClientSecretStub{client: client}
			privateKeys := &tokenPrivateKeyStub{client: client}
			issuer := &tokenIssuerStub{token: agentaccessauth.ClientCredentialsToken{
				AccessToken: "signed-aap-access-token", TokenType: "Bearer",
				ExpiresIn: 600, Scope: "run:create event:read",
			}}
			router := tokenEndpointTestRouter(t, secrets, privateKeys, issuer)
			form := url.Values{
				"grant_type": {"client_credentials"},
				"agent_id":   {"d48f1f2e-7b5a-7c3d-8e9f-123456789006"},
				"scope":      {"event:read run:create"},
			}
			request := tokenEndpointFormRequest(form)
			request.Header.Set("User-Agent", "partner-platform/1.0")
			if authentication == "client_secret_basic" {
				request.Header.Set("Authorization", "Basic safe-test-value")
			} else {
				form.Set("client_assertion_type", clientAssertionTypeJWTBearer)
				form.Set("client_assertion", "signed-client-assertion")
				request = tokenEndpointFormRequest(form)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" ||
				response.Header().Get("Pragma") != "no-cache" {
				t.Fatalf("token response must disable storage: %v", response.Header())
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["access_token"] != "signed-aap-access-token" || body["token_type"] != "Bearer" ||
				body["expires_in"] != float64(600) || body["scope"] != "run:create event:read" {
				t.Fatalf("unexpected OAuth success body: %+v", body)
			}
			if _, exists := body["refresh_token"]; exists {
				t.Fatalf("Client Credentials must not return a Refresh Token: %+v", body)
			}
			if issuer.calls != 1 || issuer.request.Client != client ||
				issuer.request.AgentID != "d48f1f2e-7b5a-7c3d-8e9f-123456789006" ||
				strings.Join(issuer.request.RequestedScopes, " ") != "event:read run:create" {
				t.Fatalf("issuer request=%+v calls=%d", issuer.request, issuer.calls)
			}
			if authentication == "client_secret_basic" {
				if secrets.calls != 1 || privateKeys.calls != 0 || secrets.request.SourceIP != "192.0.2.1" {
					t.Fatalf("Basic auth calls secret=%d private=%d request=%+v", secrets.calls, privateKeys.calls, secrets.request)
				}
			} else if privateKeys.calls != 1 || secrets.calls != 0 ||
				privateKeys.request.ClientAssertion != "signed-client-assertion" {
				t.Fatalf("private_key_jwt calls secret=%d private=%d request=%+v", secrets.calls, privateKeys.calls, privateKeys.request)
			}
		})
	}
}

func TestClientCredentialsTokenEndpointReturnsOAuthErrorsWithoutCredentialLeakage(t *testing.T) {
	base := url.Values{
		"grant_type": {"client_credentials"},
		"agent_id":   {"d48f1f2e-7b5a-7c3d-8e9f-123456789006"},
		"scope":      {"run:create"},
	}
	tests := map[string]struct {
		mutate        func(*http.Request, *tokenClientSecretStub, *tokenPrivateKeyStub, *tokenIssuerStub)
		status        int
		code          string
		wantChallenge bool
	}{
		"missing authentication": {
			status: http.StatusUnauthorized, code: "invalid_client",
		},
		"invalid Basic": {
			mutate: func(request *http.Request, secrets *tokenClientSecretStub, _ *tokenPrivateKeyStub, _ *tokenIssuerStub) {
				request.Header.Set("Authorization", "Basic super-secret-presented-value")
				secrets.err = agentaccessauth.ErrInvalidClient
			}, status: http.StatusUnauthorized, code: "invalid_client", wantChallenge: true,
		},
		"authentication unavailable": {
			mutate: func(request *http.Request, secrets *tokenClientSecretStub, _ *tokenPrivateKeyStub, _ *tokenIssuerStub) {
				request.Header.Set("Authorization", "Basic unavailable")
				secrets.err = agentaccessauth.ErrClientAuthenticationUnavailable
			}, status: http.StatusServiceUnavailable, code: "temporarily_unavailable",
		},
		"unsupported grant": {
			mutate: func(request *http.Request, _ *tokenClientSecretStub, _ *tokenPrivateKeyStub, _ *tokenIssuerStub) {
				replacement := tokenEndpointFormRequest(url.Values{
					"grant_type": {"password"}, "agent_id": {base.Get("agent_id")}, "scope": {"run:create"},
				})
				*request = *replacement
				request.Header.Set("Authorization", "Basic valid")
			}, status: http.StatusBadRequest, code: "unsupported_grant_type",
		},
		"invalid scope": {
			mutate: func(request *http.Request, _ *tokenClientSecretStub, _ *tokenPrivateKeyStub, issuer *tokenIssuerStub) {
				request.Header.Set("Authorization", "Basic valid")
				issuer.err = agentaccessauth.ErrClientCredentialsScopeInvalid
			}, status: http.StatusBadRequest, code: "invalid_scope",
		},
		"invalid target": {
			mutate: func(request *http.Request, _ *tokenClientSecretStub, _ *tokenPrivateKeyStub, issuer *tokenIssuerStub) {
				request.Header.Set("Authorization", "Basic valid")
				issuer.err = agentaccessauth.ErrClientCredentialsTargetInvalid
			}, status: http.StatusBadRequest, code: "invalid_target",
		},
		"issuer unavailable": {
			mutate: func(request *http.Request, _ *tokenClientSecretStub, _ *tokenPrivateKeyStub, issuer *tokenIssuerStub) {
				request.Header.Set("Authorization", "Basic valid")
				issuer.err = agentaccessauth.ErrTokenServiceUnavailable
			}, status: http.StatusServiceUnavailable, code: "temporarily_unavailable",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			secrets := &tokenClientSecretStub{client: tokenEndpointTestClient()}
			privateKeys := &tokenPrivateKeyStub{client: tokenEndpointTestClient()}
			issuer := &tokenIssuerStub{token: agentaccessauth.ClientCredentialsToken{
				AccessToken: "must-not-leak", TokenType: "Bearer", ExpiresIn: 600, Scope: "run:create",
			}}
			request := tokenEndpointFormRequest(base)
			if test.mutate != nil {
				test.mutate(request, secrets, privateKeys, issuer)
			}
			response := httptest.NewRecorder()
			tokenEndpointTestRouter(t, secrets, privateKeys, issuer).ServeHTTP(response, request)
			assertOAuthTokenError(t, response, test.status, test.code)
			if strings.Contains(response.Body.String(), "super-secret-presented-value") ||
				strings.Contains(response.Body.String(), "must-not-leak") {
				t.Fatalf("OAuth error leaked credential/token material: %s", response.Body.String())
			}
			if (response.Header().Get("WWW-Authenticate") != "") != test.wantChallenge {
				t.Fatalf("challenge=%q want=%t", response.Header().Get("WWW-Authenticate"), test.wantChallenge)
			}
		})
	}
}

func TestClientCredentialsTokenEndpointRejectsNonFormDuplicateAndMixedAuthentication(t *testing.T) {
	tests := map[string]func() *http.Request{
		"JSON": func() *http.Request {
			request := httptest.NewRequest(http.MethodPost, "/api/agent-access/v1/oauth/token", strings.NewReader(`{"grant_type":"client_credentials"}`))
			request.Header.Set("Content-Type", "application/json")
			return request
		},
		"query parameters": func() *http.Request {
			request := tokenEndpointFormRequest(url.Values{"grant_type": {"client_credentials"}})
			request.URL.RawQuery = "scope=run%3Acreate"
			return request
		},
		"duplicate parameter": func() *http.Request {
			return tokenEndpointRawRequest("grant_type=client_credentials&grant_type=client_credentials")
		},
		"client secret post": func() *http.Request {
			return tokenEndpointRawRequest("grant_type=client_credentials&client_secret=forbidden")
		},
		"oversized body": func() *http.Request {
			return tokenEndpointRawRequest("grant_type=client_credentials&scope=" + strings.Repeat("x", maximumOAuthTokenFormBytes))
		},
	}
	for name, build := range tests {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			tokenEndpointTestRouter(t, &tokenClientSecretStub{}, &tokenPrivateKeyStub{}, &tokenIssuerStub{}).
				ServeHTTP(response, build())
			assertOAuthTokenError(t, response, http.StatusBadRequest, "invalid_request")
		})
	}

	mixed := url.Values{
		"grant_type": {"client_credentials"}, "agent_id": {"d48f1f2e-7b5a-7c3d-8e9f-123456789006"},
		"scope": {"run:create"}, "client_assertion_type": {clientAssertionTypeJWTBearer},
		"client_assertion": {"assertion-must-not-leak"},
	}
	request := tokenEndpointFormRequest(mixed)
	request.Header.Set("Authorization", "Basic also-present")
	response := httptest.NewRecorder()
	tokenEndpointTestRouter(t, &tokenClientSecretStub{}, &tokenPrivateKeyStub{}, &tokenIssuerStub{}).
		ServeHTTP(response, request)
	assertOAuthTokenError(t, response, http.StatusUnauthorized, "invalid_client")
	if strings.Contains(response.Body.String(), "assertion-must-not-leak") {
		t.Fatalf("mixed authentication error leaked assertion: %s", response.Body.String())
	}
}

type tokenClientSecretStub struct {
	client  agentaccessauth.AuthenticatedClient
	err     error
	request agentaccessauth.ClientSecretAuthenticationRequest
	calls   int
}

func (stub *tokenClientSecretStub) AuthenticateBasic(
	_ context.Context,
	request agentaccessauth.ClientSecretAuthenticationRequest,
) (agentaccessauth.AuthenticatedClient, error) {
	stub.calls++
	stub.request = request
	return stub.client, stub.err
}

type tokenPrivateKeyStub struct {
	client  agentaccessauth.AuthenticatedClient
	err     error
	request agentaccessauth.PrivateKeyJWTAuthenticationRequest
	calls   int
}

func (stub *tokenPrivateKeyStub) Authenticate(
	_ context.Context,
	request agentaccessauth.PrivateKeyJWTAuthenticationRequest,
) (agentaccessauth.AuthenticatedClient, error) {
	stub.calls++
	stub.request = request
	return stub.client, stub.err
}

type tokenIssuerStub struct {
	token   agentaccessauth.ClientCredentialsToken
	err     error
	request agentaccessauth.ClientCredentialsTokenRequest
	calls   int
}

func (stub *tokenIssuerStub) IssueClientCredentialsToken(
	_ context.Context,
	request agentaccessauth.ClientCredentialsTokenRequest,
) (agentaccessauth.ClientCredentialsToken, error) {
	stub.calls++
	stub.request = request
	return stub.token, stub.err
}

type tokenExchangeStub struct {
	token agentaccessauth.TokenExchangeToken
	err   error
	calls int
}

func (stub *tokenExchangeStub) IssueTokenExchange(
	context.Context,
	agentaccessauth.TokenExchangeRequest,
) (agentaccessauth.TokenExchangeToken, error) {
	stub.calls++
	return stub.token, stub.err
}

func tokenEndpointTestRouter(
	t *testing.T,
	secrets *tokenClientSecretStub,
	privateKeys *tokenPrivateKeyStub,
	issuer *tokenIssuerStub,
) http.Handler {
	t.Helper()
	routes, err := NewAgentAccessTokenRoutes(secrets, privateKeys, issuer, &tokenExchangeStub{
		err: agentaccessauth.ErrTokenExchangeRequestInvalid,
	})
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{AgentAccessRegistrars: []AgentAccessV1RouteRegistrar{routes}})
	if err != nil {
		t.Fatal(err)
	}
	return router
}

func tokenEndpointTestClient() agentaccessauth.AuthenticatedClient {
	return agentaccessauth.AuthenticatedClient{
		WorkspaceID: "d48f1f2e-7b5a-7c3d-8e9f-123456789001",
		ClientID:    "d48f1f2e-7b5a-7c3d-8e9f-123456789002", PublicClientID: "awcl_test-client-id",
		ServicePrincipalID:      "d48f1f2e-7b5a-7c3d-8e9f-123456789003",
		ServicePrincipalVersion: 4,
		CredentialID:            "d48f1f2e-7b5a-7c3d-8e9f-123456789004", TokenTTLSeconds: 600,
	}
}

func tokenEndpointFormRequest(form url.Values) *http.Request {
	return tokenEndpointRawRequest(form.Encode())
}

func tokenEndpointRawRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/agent-access/v1/oauth/token", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func assertOAuthTokenError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status || response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("OAuth error status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var body oauthTokenFailure
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != code || body.ErrorDescription == "" {
		t.Fatalf("OAuth error=%+v want=%s", body, code)
	}
}
