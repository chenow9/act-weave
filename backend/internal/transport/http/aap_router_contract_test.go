package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/protocolschema"

	"github.com/gin-gonic/gin"
)

const (
	aapRouterContractWorkspaceID = "a18f1f2e-7b5a-7c3d-8e9f-123456789001"
	aapRouterContractAgentID     = "a18f1f2e-7b5a-7c3d-8e9f-123456789002"
	aapRouterOtherWorkspaceID    = "a18f1f2e-7b5a-7c3d-8e9f-123456789003"
	aapRouterOtherAgentID        = "a18f1f2e-7b5a-7c3d-8e9f-123456789004"
)

func TestAAPRouterContract(t *testing.T) {
	router, err := NewRouter(Config{
		Authenticator:            aapRouterUserAuthenticator{},
		AgentAccessAuthenticator: aapRouterTokenAuthenticator{},
		Registrars:               []V1RouteRegistrar{aapRouterUserRoutes{}},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{aapRouterContractRoutes{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agent-access/v1/workspaces/" + aapRouterContractWorkspaceID +
		"/agents/" + aapRouterContractAgentID + "/contract"

	t.Run("defaults omitted date version and returns context", func(t *testing.T) {
		request := aapRouterRequest(path, "aap-contract-token")
		request.Header.Set("X-Request-ID", "request-aap-router-contract")
		request.Header.Set("X-Trace-ID", "trace-aap-router-contract")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if response.Header().Get(AAPProtocolVersionHeader) != protocolschema.ProtocolVersion ||
			response.Header().Get("X-Request-ID") != "request-aap-router-contract" ||
			response.Header().Get("X-Trace-ID") != "trace-aap-router-contract" ||
			!strings.Contains(response.Header().Get("Vary"), AAPProtocolVersionHeader) {
			t.Fatalf("response headers=%v", response.Header())
		}
		var body struct {
			RequestID       string `json:"requestId"`
			TraceID         string `json:"traceId"`
			MajorVersion    string `json:"majorVersion"`
			ProtocolVersion string `json:"protocolVersion"`
			PrincipalID     string `json:"principalId"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.RequestID != "request-aap-router-contract" ||
			body.TraceID != "trace-aap-router-contract" || body.MajorVersion != "v1" ||
			body.ProtocolVersion != protocolschema.ProtocolVersion || body.PrincipalID == "" {
			t.Fatalf("response body=%+v", body)
		}
	})

	t.Run("accepts current date version", func(t *testing.T) {
		request := aapRouterRequest(path, "aap-contract-token")
		request.Header.Set(AAPProtocolVersionHeader, protocolschema.ProtocolVersion)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK ||
			response.Header().Get(AAPProtocolVersionHeader) != protocolschema.ProtocolVersion {
			t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
		}
	})

	t.Run("rejects unsupported blank and ambiguous date versions before auth", func(t *testing.T) {
		tests := []struct {
			name   string
			values []string
		}{
			{name: "unsupported", values: []string{"2025-01-01"}},
			{name: "blank", values: []string{""}},
			{name: "duplicate", values: []string{protocolschema.ProtocolVersion, protocolschema.ProtocolVersion}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				request := aapRouterRequest(path, "")
				for _, value := range test.values {
					request.Header.Add(AAPProtocolVersionHeader, value)
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				body := assertAAPRouterError(t, response, http.StatusBadRequest, "PROTOCOL_VERSION_UNSUPPORTED")
				if body.Error.Retryable || body.Error.Details == nil || len(body.Error.Details) != 0 ||
					response.Header().Get(AAPProtocolVersionHeader) != protocolschema.ProtocolVersion {
					t.Fatalf("error=%+v headers=%v", body.Error, response.Header())
				}
			})
		}
	})

	t.Run("does not interchange management and Agent Access tokens", func(t *testing.T) {
		userAgainstAAP := httptest.NewRecorder()
		router.ServeHTTP(userAgainstAAP, aapRouterRequest(path, "user-contract-token"))
		assertAAPRouterError(t, userAgainstAAP, http.StatusUnauthorized, "UNAUTHENTICATED")

		aapRequest := httptest.NewRequest(http.MethodGet, "/api/v1/aap-router-user-probe", nil)
		aapRequest.Header.Set("Authorization", "Bearer aap-contract-token")
		aapAgainstUser := httptest.NewRecorder()
		router.ServeHTTP(aapAgainstUser, aapRequest)
		assertErrorResponse(t, aapAgainstUser, http.StatusUnauthorized, "UNAUTHENTICATED")
	})

	t.Run("hides cross-workspace cross-agent and unknown resources", func(t *testing.T) {
		paths := []string{
			"/api/agent-access/v1/workspaces/" + aapRouterOtherWorkspaceID +
				"/agents/" + aapRouterContractAgentID + "/contract",
			"/api/agent-access/v1/workspaces/" + aapRouterContractWorkspaceID +
				"/agents/" + aapRouterOtherAgentID + "/contract",
			"/api/agent-access/v1/unknown-resource",
		}
		var canonical string
		for index, candidate := range paths {
			request := aapRouterRequest(candidate, "aap-contract-token")
			request.Header.Set("X-Request-ID", "request-aap-hidden")
			request.Header.Set("X-Trace-ID", "trace-aap-hidden")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assertAAPRouterError(t, response, http.StatusNotFound, "RESOURCE_NOT_FOUND")
			if index == 0 {
				canonical = response.Body.String()
			} else if response.Body.String() != canonical {
				t.Fatalf("hidden resource responses differ: first=%s current=%s", canonical, response.Body.String())
			}
		}
	})

	t.Run("distinguishes visible authorization denial", func(t *testing.T) {
		deniedPath := "/api/agent-access/v1/workspaces/" + aapRouterContractWorkspaceID +
			"/agents/" + aapRouterContractAgentID + "/denied"
		response := httptest.NewRecorder()
		router.ServeHTTP(response, aapRouterRequest(deniedPath, "aap-contract-token"))
		assertAAPRouterError(t, response, http.StatusForbidden, "AGENT_ACCESS_DENIED")
	})

	t.Run("rejects unsupported URL major with JSON before streaming headers", func(t *testing.T) {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, aapRouterRequest(
			"/api/agent-access/v2/workspaces/"+aapRouterContractWorkspaceID, "aap-contract-token"))
		assertAAPRouterError(t, response, http.StatusBadRequest, "PROTOCOL_VERSION_UNSUPPORTED")
		if response.Header().Get(AAPProtocolVersionHeader) != protocolschema.ProtocolVersion {
			t.Fatalf("headers=%v", response.Header())
		}
	})
}

type aapRouterTokenAuthenticator struct{}

func (aapRouterTokenAuthenticator) VerifyAccessToken(
	_ context.Context,
	value string,
) (agentaccessauth.AAPAccessTokenPrincipal, error) {
	if value != "aap-contract-token" {
		return agentaccessauth.AAPAccessTokenPrincipal{}, errors.New("invalid AAP token")
	}
	now := time.Now().UTC()
	return agentaccessauth.AAPAccessTokenPrincipal{
		PrincipalID:        "a18f1f2e-7b5a-7c3d-8e9f-123456789005",
		ServicePrincipalID: "a18f1f2e-7b5a-7c3d-8e9f-123456789005",
		AuthorizedParty:    "awcl_aap_router_contract_client",
		WorkspaceID:        aapRouterContractWorkspaceID,
		AgentID:            aapRouterContractAgentID,
		Scopes:             []string{"run:create", "event:read"},
		SecurityVersion:    1,
		TokenID:            "a18f1f2e-7b5a-7c3d-8e9f-123456789006",
		IssuedAt:           now.Add(-time.Minute), NotBefore: now.Add(-time.Minute),
		ExpiresAt: now.Add(10 * time.Minute),
	}, nil
}

type aapRouterUserAuthenticator struct{}

func (aapRouterUserAuthenticator) AuthenticateAccessToken(
	_ context.Context,
	value string,
) (Principal, error) {
	if value != "user-contract-token" {
		return Principal{}, errors.New("invalid user token")
	}
	return Principal{UserID: "a18f1f2e-7b5a-7c3d-8e9f-123456789007", SessionID: "a18f1f2e-7b5a-7c3d-8e9f-123456789008"}, nil
}

type aapRouterContractRoutes struct{}

func (aapRouterContractRoutes) RegisterAgentAccessV1(routes AgentAccessV1Routes) {
	base := "/workspaces/:workspaceId/agents/:agentId"
	routes.Protected.GET(base+"/contract", func(c *gin.Context) {
		principal, ok := AAPPrincipalFrom(c.Request.Context())
		if !ok || principal.WorkspaceID != c.Param("workspaceId") ||
			principal.AgentID != c.Param("agentId") {
			RespondError(c, agentaccessauth.ErrAAPAuthorizationNotVisible)
			return
		}
		requestContext, requestOK := RequestContextFrom(c.Request.Context())
		protocolContext, protocolOK := AAPProtocolContextFrom(c.Request.Context())
		if !requestOK || !protocolOK {
			RespondError(c, errors.New("AAP router context missing"))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"requestId": requestContext.RequestID, "traceId": requestContext.TraceID,
			"majorVersion":    protocolContext.MajorVersion,
			"protocolVersion": protocolContext.ProtocolVersion,
			"principalId":     principal.PrincipalID,
		})
	})
	routes.Protected.GET(base+"/denied", func(c *gin.Context) {
		RespondError(c, agentaccessauth.ErrAAPAuthorizationDenied)
	})
}

type aapRouterUserRoutes struct{}

func (aapRouterUserRoutes) RegisterV1(routes V1Routes) {
	routes.Protected.GET("/aap-router-user-probe", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
}

func aapRouterRequest(path, token string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request
}

func assertAAPRouterError(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	code string,
) ErrorResponse {
	t.Helper()
	body := assertErrorResponse(t, response, status, code)
	var raw struct {
		Error map[string]json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	expectedFields := []string{"code", "message", "requestId", "traceId", "retryable", "details"}
	if len(raw.Error) != len(expectedFields) {
		t.Fatalf("AAP error fields=%v body=%s", raw.Error, response.Body.String())
	}
	for _, field := range expectedFields {
		if _, ok := raw.Error[field]; !ok {
			t.Fatalf("AAP error missing %q: %s", field, response.Body.String())
		}
	}
	return body
}
