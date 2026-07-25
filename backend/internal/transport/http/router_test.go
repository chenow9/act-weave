package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/audit"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/workspace"

	"github.com/gin-gonic/gin"
)

const (
	transportTestUserID    = "d38f1f2e-7b5a-7c3d-8e9f-123456789001"
	transportTestSessionID = "d38f1f2e-7b5a-7c3d-8e9f-123456789002"
)

func TestRequestContextAndAuthentication(t *testing.T) {
	router, err := NewRouter(Config{
		Authenticator: transportAuthenticator{}, Registrars: []V1RouteRegistrar{transportTestRoutes{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/context", nil)
	request.RemoteAddr = "203.0.113.61:4321"
	request.Header.Set("X-Request-ID", "request-v1-context")
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	request.Header.Set("User-Agent", "actweave-v1-test")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("request context status=%d body=%s", response.Code, response.Body.String())
	}
	var contextBody RequestContext
	if err := json.Unmarshal(response.Body.Bytes(), &contextBody); err != nil {
		t.Fatal(err)
	}
	if contextBody.RequestID != "request-v1-context" ||
		contextBody.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" ||
		contextBody.SourceIP != "203.0.113.61" || contextBody.UserAgent != "actweave-v1-test" ||
		response.Header().Get("X-Request-ID") != contextBody.RequestID ||
		response.Header().Get("X-Trace-ID") != contextBody.TraceID {
		t.Fatalf("request context=%+v headers=%v", contextBody, response.Header())
	}

	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/protected-context", nil))
	assertErrorResponse(t, unauthenticated, http.StatusUnauthorized, "UNAUTHENTICATED")

	protectedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/protected-context", nil)
	protectedRequest.Header.Set("Authorization", "Bearer valid-access-token")
	protectedRequest.Header.Set("X-Request-ID", "request-v1-protected")
	protectedRequest.Header.Set("X-Trace-ID", "trace-v1-protected")
	protected := httptest.NewRecorder()
	router.ServeHTTP(protected, protectedRequest)
	if protected.Code != http.StatusOK {
		t.Fatalf("protected status=%d body=%s", protected.Code, protected.Body.String())
	}
	var principal Principal
	if err := json.Unmarshal(protected.Body.Bytes(), &principal); err != nil {
		t.Fatal(err)
	}
	if principal.UserID != transportTestUserID || principal.SessionID != transportTestSessionID ||
		principal.PlatformRole != "ADMIN" {
		t.Fatalf("protected principal=%+v", principal)
	}

	generatedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/context", nil)
	generatedRequest.Header.Set("X-Request-ID", "bad request id with spaces")
	generated := httptest.NewRecorder()
	router.ServeHTTP(generated, generatedRequest)
	if generated.Code != http.StatusOK || generated.Header().Get("X-Request-ID") == "" ||
		generated.Header().Get("X-Request-ID") == "bad request id with spaces" ||
		generated.Header().Get("X-Trace-ID") != generated.Header().Get("X-Request-ID") {
		t.Fatalf("generated request identifiers headers=%v body=%s", generated.Header(), generated.Body.String())
	}
}

func TestErrorContractAndCrossWorkspaceNotVisible(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	router, err := NewRouter(Config{Logger: logger, Registrars: []V1RouteRegistrar{transportTestRoutes{}}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		kind   string
		status int
		code   string
	}{
		{"unauthenticated", http.StatusUnauthorized, "UNAUTHENTICATED"},
		{"forbidden", http.StatusForbidden, "FORBIDDEN"},
		{"not-visible", http.StatusNotFound, "NOT_FOUND"},
		{"not-found", http.StatusNotFound, "NOT_FOUND"},
		{"conflict", http.StatusConflict, "CONFLICT"},
		{"invalid", http.StatusUnprocessableEntity, "VALIDATION_ERROR"},
		{"internal", http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/errors/"+test.kind, nil)
			request.Header.Set("X-Request-ID", "request-error-"+test.kind)
			request.Header.Set("X-Trace-ID", "trace-error-"+test.kind)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			body := assertErrorResponse(t, response, test.status, test.code)
			if body.Error.RequestID != "request-error-"+test.kind ||
				body.Error.TraceID != "trace-error-"+test.kind {
				t.Fatalf("error correlation=%+v", body.Error)
			}
			if test.kind == "internal" &&
				json.Valid(response.Body.Bytes()) && contains(response.Body.String(), "database password") {
				t.Fatalf("internal error leaked implementation detail: %s", response.Body.String())
			}
		})
	}
	panicResponse := httptest.NewRecorder()
	router.ServeHTTP(panicResponse, httptest.NewRequest(http.MethodGet, "/api/v1/panic", nil))
	assertErrorResponse(t, panicResponse, http.StatusInternalServerError, "INTERNAL_ERROR")

	logOutput := logs.String()
	for _, expected := range []string{
		"event=http.request.completed",
		"request_id=request-error-internal",
		"error_code=INTERNAL_ERROR",
		"error_source=",
		"database password must never appear",
		"event=http.request.panic",
		"secret panic detail",
		"transportTestRoutes.RegisterV1",
	} {
		if !contains(logOutput, expected) {
			t.Fatalf("request log missing %q:\n%s", expected, logOutput)
		}
	}
}

type transportAuthenticator struct{}

func (transportAuthenticator) AuthenticateAccessToken(
	_ context.Context,
	token string,
) (Principal, error) {
	if token != "valid-access-token" {
		return Principal{}, errors.New("invalid token")
	}
	return Principal{
		UserID: transportTestUserID, SessionID: transportTestSessionID,
		Username: "transport.admin", PlatformRole: "ADMIN",
	}, nil
}

type transportTestRoutes struct{}

func (transportTestRoutes) RegisterV1(routes V1Routes) {
	routes.Public.GET("/panic", func(*gin.Context) { panic("secret panic detail") })
	routes.Public.GET("/context", func(c *gin.Context) {
		value, exists := RequestContextFrom(c.Request.Context())
		if !exists {
			RespondError(c, errors.New("request context missing"))
			return
		}
		c.JSON(http.StatusOK, value)
	})
	routes.Protected.GET("/protected-context", func(c *gin.Context) {
		value, exists := PrincipalFrom(c.Request.Context())
		if !exists {
			RespondError(c, ErrUnauthenticated)
			return
		}
		c.JSON(http.StatusOK, value)
	})
	routes.Public.GET("/errors/:kind", func(c *gin.Context) {
		var err error
		switch c.Param("kind") {
		case "unauthenticated":
			err = ErrUnauthenticated
		case "forbidden":
			err = authz.ErrDenied
		case "not-visible":
			err = authz.ErrNotVisible
		case "not-found":
			err = agent.ErrNotFound
		case "conflict":
			err = audit.ErrConflict
		case "invalid":
			err = workspace.ErrInvalid
		default:
			err = errors.New("database password must never appear")
		}
		RespondError(c, err)
	})
}

func assertErrorResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	code string,
) ErrorResponse {
	t.Helper()
	if response.Code != status || response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("error response status=%d content-type=%q body=%s",
			response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var body ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != code || body.Error.Message == "" ||
		body.Error.RequestID == "" || body.Error.TraceID == "" {
		t.Fatalf("error response=%+v", body)
	}
	return body
}

func contains(value, substring string) bool {
	for index := 0; index+len(substring) <= len(value); index++ {
		if value[index:index+len(substring)] == substring {
			return true
		}
	}
	return false
}
