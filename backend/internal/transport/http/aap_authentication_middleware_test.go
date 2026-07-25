package httptransport

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/authn"
	"actweave/backend/internal/identity"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestAAPAuthenticationMiddlewareUsesIndependentPrincipalContextAndJWTProfile(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x81}, ed25519.SeedSize))
	keys, err := agentaccessauth.NewRotatingSigningKeyProvider(
		"aap-http-auth-key", privateKey, 15*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	aapVerifier, err := agentaccessauth.NewAAPAccessTokenVerifier(keys,
		"https://api.example.test/api/agent-access/v1/oauth/token", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	userTokens, err := authn.NewAccessTokenManager(strings.Repeat("u", 32), "actweave", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	userAuthenticator, err := NewAccessTokenAuthenticator(userTokens)
	if err != nil {
		t.Fatal(err)
	}
	userAuthenticator.now = func() time.Time { return now }
	userToken, err := userTokens.Generate(identity.User{
		ID: "f58f1f2e-7b5a-7c3d-8e9f-123456789001", Username: "aap-isolation-user",
		PlatformRole: identity.PlatformRoleUser,
	}, "f58f1f2e-7b5a-7c3d-8e9f-123456789002", now)
	if err != nil {
		t.Fatal(err)
	}
	aapClaims := aapHTTPAuthenticationClaims(now)
	key, err := keys.ActiveSigningKey(now)
	if err != nil {
		t.Fatal(err)
	}
	aapToken, err := key.SignAccessToken(aapClaims)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		Authenticator: userAuthenticator, AgentAccessAuthenticator: aapVerifier,
		Registrars:            []V1RouteRegistrar{userPrincipalProbeRoutes{}},
		AgentAccessRegistrars: []AgentAccessV1RouteRegistrar{aapPrincipalProbeRoutes{}},
	})
	if err != nil {
		t.Fatal(err)
	}

	aapResponse := authenticatedProbeRequest(router, "/api/agent-access/v1/auth-probe", aapToken)
	if aapResponse.Code != http.StatusOK ||
		!strings.Contains(aapResponse.Body.String(), `"servicePrincipalId":"`+aapClaims.Subject+`"`) ||
		!strings.Contains(aapResponse.Body.String(), `"workspaceId":"`+aapClaims.WorkspaceID+`"`) ||
		!strings.Contains(aapResponse.Body.String(), `"agentId":"`+aapClaims.AgentID+`"`) ||
		!strings.Contains(aapResponse.Body.String(), `"userContext":false`) {
		t.Fatalf("AAP protected response status=%d body=%s", aapResponse.Code, aapResponse.Body.String())
	}
	userResponse := authenticatedProbeRequest(router, "/api/v1/auth-probe", userToken.Value)
	if userResponse.Code != http.StatusOK || !strings.Contains(userResponse.Body.String(), `"aapContext":false`) {
		t.Fatalf("user protected response status=%d body=%s", userResponse.Code, userResponse.Body.String())
	}

	userAgainstAAP := authenticatedProbeRequest(router, "/api/agent-access/v1/auth-probe", userToken.Value)
	assertErrorResponse(t, userAgainstAAP, http.StatusUnauthorized, "UNAUTHENTICATED")
	aapAgainstUser := authenticatedProbeRequest(router, "/api/v1/auth-probe", aapToken)
	assertErrorResponse(t, aapAgainstUser, http.StatusUnauthorized, "UNAUTHENTICATED")
	missing := authenticatedProbeRequest(router, "/api/agent-access/v1/auth-probe", "")
	assertErrorResponse(t, missing, http.StatusUnauthorized, "UNAUTHENTICATED")
	malformed := authenticatedProbeRequest(router, "/api/agent-access/v1/auth-probe", aapToken+"tampered")
	assertErrorResponse(t, malformed, http.StatusUnauthorized, "UNAUTHENTICATED")
	duplicateRequest := httptest.NewRequest(http.MethodGet, "/api/agent-access/v1/auth-probe", nil)
	duplicateRequest.Header.Add("Authorization", "Bearer "+aapToken)
	duplicateRequest.Header.Add("Authorization", "Bearer "+aapToken)
	duplicateResponse := httptest.NewRecorder()
	router.ServeHTTP(duplicateResponse, duplicateRequest)
	assertErrorResponse(t, duplicateResponse, http.StatusUnauthorized, "UNAUTHENTICATED")
}

func TestAAPAuthenticationMiddlewareReturnsStableExpiredTokenError(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x82}, ed25519.SeedSize))
	keys, err := agentaccessauth.NewRotatingSigningKeyProvider("aap-http-expired-key", privateKey, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := agentaccessauth.NewAAPAccessTokenVerifier(keys,
		"https://api.example.test/api/agent-access/v1/oauth/token", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims := aapHTTPAuthenticationClaims(now)
	claims.IssuedAt = jwt.NewNumericDate(now.Add(-10 * time.Minute))
	claims.NotBefore = jwt.NewNumericDate(now.Add(-10*time.Minute - agentaccessauth.DefaultTokenClockSkew))
	claims.ExpiresAt = jwt.NewNumericDate(now)
	key, err := keys.ActiveSigningKey(now)
	if err != nil {
		t.Fatal(err)
	}
	value, err := key.SignAccessToken(claims)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: verifier,
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{aapPrincipalProbeRoutes{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := authenticatedProbeRequest(router, "/api/agent-access/v1/auth-probe", value)
	assertErrorResponse(t, response, http.StatusUnauthorized, "TOKEN_EXPIRED")
}

type aapPrincipalProbeRoutes struct{}

func (aapPrincipalProbeRoutes) RegisterAgentAccessV1(routes AgentAccessV1Routes) {
	routes.Protected.GET("/auth-probe", func(c *gin.Context) {
		principal, ok := AAPPrincipalFrom(c.Request.Context())
		_, userContext := PrincipalFrom(c.Request.Context())
		if !ok {
			RespondError(c, ErrUnauthenticated)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"servicePrincipalId": principal.ServicePrincipalID,
			"authorizedParty":    principal.AuthorizedParty,
			"workspaceId":        principal.WorkspaceID, "agentId": principal.AgentID,
			"scope": principal.Scopes, "userContext": userContext,
		})
	})
}

type userPrincipalProbeRoutes struct{}

func (userPrincipalProbeRoutes) RegisterV1(routes V1Routes) {
	routes.Protected.GET("/auth-probe", func(c *gin.Context) {
		principal, ok := PrincipalFrom(c.Request.Context())
		_, aapContext := AAPPrincipalFrom(c.Request.Context())
		if !ok {
			RespondError(c, ErrUnauthenticated)
			return
		}
		c.JSON(http.StatusOK, gin.H{"userId": principal.UserID, "aapContext": aapContext})
	})
}

func aapHTTPAuthenticationClaims(now time.Time) agentaccessauth.AAPAccessTokenClaims {
	return agentaccessauth.AAPAccessTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://api.example.test/api/agent-access/v1/oauth",
			Subject:   "f58f1f2e-7b5a-7c3d-8e9f-123456789003",
			Audience:  jwt.ClaimStrings{agentaccessauth.AAPAccessTokenAudience},
			ID:        "f58f1f2e-7b5a-7c3d-8e9f-123456789004",
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-agentaccessauth.DefaultTokenClockSkew)),
			ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		},
		AuthorizedParty: "awcl_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x83}, 32)),
		WorkspaceID:     "f58f1f2e-7b5a-7c3d-8e9f-123456789005",
		AgentID:         "f58f1f2e-7b5a-7c3d-8e9f-123456789006",
		Scope:           "run:create event:read", SecurityVersion: 5,
	}
}

func authenticatedProbeRequest(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
