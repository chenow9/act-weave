package httptransport_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/config"
	httptransport "actweave/backend/internal/transport/http"

	"github.com/gin-gonic/gin"
)

// TestAAPFeatureRollout is the M10-T8 HTTP gate: default-closed public surface,
// workspace/client allowlists, and unconstrained nil feature for unit tests.
func TestAAPFeatureRollout(t *testing.T) {
	t.Run("DisabledClosesPublicSurface", testAAPFeatureDisabledClosesSurface)
	t.Run("AllowlistEnforcesWorkspaceAndClient", testAAPFeatureAllowlist)
	t.Run("NilFeatureKeepsUnconstrainedTestWiring", testAAPFeatureNilUnconstrained)
	t.Run("InternalV1HealthUnaffected", testAAPFeatureInternalHealth)
}

const (
	featureWS     = "a1000000-0000-4000-8000-000000000101"
	featureOther  = "a2000000-0000-4000-8000-000000000102"
	featureClient = "b1000000-0000-4000-8000-000000000101"
	featureOtherC = "b2000000-0000-4000-8000-000000000102"
	featureAgent  = "c1000000-0000-4000-8000-000000000101"
	featureSP     = "d1000000-0000-4000-8000-000000000101"
)

type aapFeatureRegistrar struct{}

func (aapFeatureRegistrar) RegisterAgentAccessV1(v1 httptransport.AgentAccessV1Routes) {
	v1.Public.GET("/.well-known/jwks.json", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"keys": []any{}})
	})
	v1.Public.POST("/oauth/token", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"access_token": "short", "token_type": "Bearer", "expires_in": 600})
	})
	v1.Protected.GET("/workspaces/:wid/agents/:aid/profile", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"id": c.Param("aid")})
	})
}

type aapFeatureAuthenticator struct {
	principal agentaccessauth.AAPAccessTokenPrincipal
	err       error
}

func (auth aapFeatureAuthenticator) VerifyAccessToken(
	context.Context, string,
) (agentaccessauth.AAPAccessTokenPrincipal, error) {
	if auth.err != nil {
		return agentaccessauth.AAPAccessTokenPrincipal{}, auth.err
	}
	return auth.principal, nil
}

func featurePrincipal(workspaceID, clientID string) agentaccessauth.AAPAccessTokenPrincipal {
	return agentaccessauth.AAPAccessTokenPrincipal{
		PrincipalID: featureSP, ServicePrincipalID: featureSP, AuthorizedParty: clientID,
		WorkspaceID: workspaceID, AgentID: featureAgent,
		Scopes: []string{"agent:read"}, SecurityVersion: 1, TokenID: "token-feature-1",
		IssuedAt: time.Now().UTC().Add(-time.Minute), NotBefore: time.Now().UTC().Add(-time.Minute),
		ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	}
}

func testAAPFeatureDisabledClosesSurface(t *testing.T) {
	feature := config.AAPFeatureRollout{Enabled: false}
	router, err := httptransport.NewRouter(httptransport.Config{
		AgentAccessAuthenticator: aapFeatureAuthenticator{principal: featurePrincipal(featureWS, featureClient)},
		AgentAccessRegistrars:    []httptransport.AgentAccessV1RouteRegistrar{aapFeatureRegistrar{}},
		AAPFeature:               &feature,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/agent-access/v1/.well-known/jwks.json",
		"/api/agent-access/v1/oauth/token",
		"/api/agent-access/v1/workspaces/" + featureWS + "/agents/" + featureAgent + "/profile",
	} {
		method := http.MethodGet
		if path == "/api/agent-access/v1/oauth/token" {
			method = http.MethodPost
		}
		req := httptest.NewRequest(method, path, nil)
		if method == http.MethodGet && path != "/api/agent-access/v1/.well-known/jwks.json" {
			req.Header.Set("Authorization", "Bearer unused")
		}
		if method == http.MethodGet && path != "/api/agent-access/v1/.well-known/jwks.json" &&
			path != "/api/agent-access/v1/oauth/token" {
			req.Header.Set("Authorization", "Bearer unused")
		}
		if path != "/api/agent-access/v1/.well-known/jwks.json" && path != "/api/agent-access/v1/oauth/token" {
			req.Header.Set("Authorization", "Bearer unused")
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("disabled surface path %s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func testAAPFeatureAllowlist(t *testing.T) {
	feature := config.AAPFeatureRollout{
		Enabled: true,
		WorkspaceIDs: []string{featureWS},
		ClientIDs:    []string{featureClient},
	}
	router, err := httptransport.NewRouter(httptransport.Config{
		AgentAccessAuthenticator: aapFeatureAuthenticator{
			principal: featurePrincipal(featureWS, featureClient),
		},
		AgentAccessRegistrars: []httptransport.AgentAccessV1RouteRegistrar{aapFeatureRegistrar{}},
		AAPFeature:            &feature,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Public JWKS open when feature enabled.
	jwks := httptest.NewRequest(http.MethodGet, "/api/agent-access/v1/.well-known/jwks.json", nil)
	jwksRec := httptest.NewRecorder()
	router.ServeHTTP(jwksRec, jwks)
	if jwksRec.Code != http.StatusOK {
		t.Fatalf("jwks status=%d body=%s", jwksRec.Code, jwksRec.Body.String())
	}

	// Allowlisted client + workspace succeeds.
	okReq := httptest.NewRequest(http.MethodGet,
		"/api/agent-access/v1/workspaces/"+featureWS+"/agents/"+featureAgent+"/profile", nil)
	okReq.Header.Set("Authorization", "Bearer ok")
	okRec := httptest.NewRecorder()
	router.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("allowlisted status=%d body=%s", okRec.Code, okRec.Body.String())
	}

	// Wrong client denied (404 not-visible).
	denyClientRouter, err := httptransport.NewRouter(httptransport.Config{
		AgentAccessAuthenticator: aapFeatureAuthenticator{
			principal: featurePrincipal(featureWS, featureOtherC),
		},
		AgentAccessRegistrars: []httptransport.AgentAccessV1RouteRegistrar{aapFeatureRegistrar{}},
		AAPFeature:            &feature,
	})
	if err != nil {
		t.Fatal(err)
	}
	denyClient := httptest.NewRequest(http.MethodGet,
		"/api/agent-access/v1/workspaces/"+featureWS+"/agents/"+featureAgent+"/profile", nil)
	denyClient.Header.Set("Authorization", "Bearer x")
	denyClientRec := httptest.NewRecorder()
	denyClientRouter.ServeHTTP(denyClientRec, denyClient)
	if denyClientRec.Code != http.StatusNotFound {
		t.Fatalf("deny client status=%d body=%s", denyClientRec.Code, denyClientRec.Body.String())
	}

	// Wrong workspace denied.
	denyWSRouter, err := httptransport.NewRouter(httptransport.Config{
		AgentAccessAuthenticator: aapFeatureAuthenticator{
			principal: featurePrincipal(featureOther, featureClient),
		},
		AgentAccessRegistrars: []httptransport.AgentAccessV1RouteRegistrar{aapFeatureRegistrar{}},
		AAPFeature:            &feature,
	})
	if err != nil {
		t.Fatal(err)
	}
	denyWS := httptest.NewRequest(http.MethodGet,
		"/api/agent-access/v1/workspaces/"+featureOther+"/agents/"+featureAgent+"/profile", nil)
	denyWS.Header.Set("Authorization", "Bearer x")
	denyWSRec := httptest.NewRecorder()
	denyWSRouter.ServeHTTP(denyWSRec, denyWS)
	if denyWSRec.Code != http.StatusNotFound {
		t.Fatalf("deny workspace status=%d body=%s", denyWSRec.Code, denyWSRec.Body.String())
	}
}

func testAAPFeatureNilUnconstrained(t *testing.T) {
	// Existing unit tests pass AAPFeature=nil and must remain open.
	router, err := httptransport.NewRouter(httptransport.Config{
		AgentAccessAuthenticator: aapFeatureAuthenticator{
			principal: featurePrincipal(featureWS, featureClient),
		},
		AgentAccessRegistrars: []httptransport.AgentAccessV1RouteRegistrar{aapFeatureRegistrar{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/agent-access/v1/.well-known/jwks.json", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("nil feature must leave surface open: status=%d", rec.Code)
	}
}

func testAAPFeatureInternalHealth(t *testing.T) {
	// Management / internal surface stays up while AAP is closed.
	feature := config.AAPFeatureRollout{Enabled: false}
	router, err := httptransport.NewRouter(httptransport.Config{
		AAPFeature: &feature,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("internal health status=%d body=%s", rec.Code, rec.Body.String())
	}
}
