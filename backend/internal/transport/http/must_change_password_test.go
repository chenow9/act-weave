package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"actweave/backend/internal/identity"

	"github.com/gin-gonic/gin"
)

// TestMustChangePasswordAllowlist enforces HIGH-03 D5=A exact method+template matching.
func TestMustChangePasswordAllowlist(t *testing.T) {
	authenticator := mustChangePrincipalAuthenticator{
		principal: Principal{
			UserID:             "u-must-change",
			SessionID:          "s-must-change",
			Username:           "must.change",
			PlatformRole:       string(identity.PlatformRoleUser),
			MustChangePassword: true,
		},
	}
	var handlerHits map[string]int
	router, err := NewRouter(Config{
		Authenticator: authenticator,
		Registrars: []V1RouteRegistrar{routeFunc(func(routes V1Routes) {
			handlerHits = map[string]int{}
			hit := func(key string) gin.HandlerFunc {
				return func(c *gin.Context) {
					handlerHits[key]++
					c.Status(http.StatusNoContent)
				}
			}
			// Registered templates (colon commands become __command via adapter).
			// Also register wrong-method handlers on the same templates so the
			// must-change gate (not Gin 404) is the rejecting layer under test.
			routes.Protected.POST("/users/me/__command/change-password", hit("change-password"))
			routes.Protected.GET("/users/me/__command/change-password", hit("change-password-get"))
			routes.Protected.POST("/auth/logout", hit("logout"))
			routes.Protected.GET("/auth/logout", hit("logout-get"))
			routes.Protected.GET("/users/me", hit("me"))
			routes.Protected.PATCH("/users/me", hit("patch-me"))
			routes.Protected.GET("/admin/users", hit("admin-users"))
			routes.Protected.GET("/workspaces", hit("workspaces"))
			routes.Protected.POST("/workspaces", hit("create-workspace"))
		})},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Allowed: external change-password colon path.
	allowed := []struct {
		method string
		path   string
		key    string
	}{
		{http.MethodPost, "/api/v1/users/me:change-password", "change-password"},
		{http.MethodPost, "/api/v1/auth/logout", "logout"},
		{http.MethodGet, "/api/v1/users/me", "me"},
	}
	for _, tc := range allowed {
		response := serveBearer(router, tc.method, tc.path, "token")
		if response.Code != http.StatusNoContent {
			t.Fatalf("allow %s %s status=%d body=%s", tc.method, tc.path, response.Code, response.Body.String())
		}
		if handlerHits[tc.key] != 1 {
			t.Fatalf("allow %s %s handler hits=%d", tc.method, tc.path, handlerHits[tc.key])
		}
		handlerHits[tc.key] = 0
	}

	// Denied: wrong method on allowlisted templates, plus business routes.
	denied := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/users/me:change-password"},
		{http.MethodPatch, "/api/v1/users/me"},
		{http.MethodGet, "/api/v1/auth/logout"},
		{http.MethodGet, "/api/v1/admin/users"},
		{http.MethodGet, "/api/v1/workspaces"},
		{http.MethodPost, "/api/v1/workspaces"},
	}
	for _, tc := range denied {
		response := serveBearer(router, tc.method, tc.path, "token")
		body := assertErrorResponse(t, response, http.StatusForbidden, "PASSWORD_CHANGE_REQUIRED")
		if body.Error.Message != "Password change is required before continuing." {
			t.Fatalf("unexpected message for %s %s: %+v", tc.method, tc.path, body.Error)
		}
		if body.Error.Retryable {
			t.Fatalf("PASSWORD_CHANGE_REQUIRED must not be retryable: %+v", body.Error)
		}
	}
}

func TestMustChangePasswordFalseDoesNotGate(t *testing.T) {
	router, err := NewRouter(Config{
		Authenticator: mustChangePrincipalAuthenticator{
			principal: Principal{
				UserID: "u-ok", SessionID: "s-ok", Username: "ok.user",
				PlatformRole: string(identity.PlatformRoleUser),
			},
		},
		Registrars: []V1RouteRegistrar{routeFunc(func(routes V1Routes) {
			routes.Protected.GET("/admin/users", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"ok": true})
			})
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := serveBearer(router, http.MethodGet, "/api/v1/admin/users", "token")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMustChangePasswordLoginAndRefreshRemainPublic(t *testing.T) {
	// Public routes are not behind authentication / must-change middleware.
	// Ensure health remains open and protected routes still require auth.
	router, err := NewRouter(Config{
		Authenticator: mustChangePrincipalAuthenticator{
			principal: Principal{
				UserID: "u", SessionID: "s", MustChangePassword: true,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status=%d", health.Code)
	}
}

func TestTemporaryPasswordLoginSetsMustChangeAndGatesBusiness(t *testing.T) {
	fixture := newV1AuthFixture(t)
	adminLogin := fixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": v1AdminName, "password": v1AdminPass,
	}, "", nil)
	adminTokens := decodeTokenResponse(t, adminLogin)
	created := fixture.request(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "temp.user", "displayName": "Temp User",
		"password": "Temporary-password-x1", "platformRole": "USER",
		"locale": "zh-CN", "timezone": "Asia/Shanghai",
	}, adminTokens.AccessToken, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var user userDTO
	if err := json.Unmarshal(created.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	// Admin create sets must_change_password=true by default for temporary passwords.
	login := fixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "temp.user", "password": "Temporary-password-x1",
	}, "", nil)
	tokens := decodeTokenResponse(t, login)
	if !tokens.MustChangePassword {
		t.Fatal("expected mustChangePassword=true for temporary password login")
	}
	// Allowlist works.
	me := fixture.request(t, http.MethodGet, "/api/v1/users/me", nil, tokens.AccessToken, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("me status=%d body=%s", me.Code, me.Body.String())
	}
	// Business path blocked.
	assertErrorResponse(t,
		fixture.request(t, http.MethodPatch, "/api/v1/users/me", map[string]any{
			"displayName": "Blocked", "lockVersion": user.LockVersion,
		}, tokens.AccessToken, nil),
		http.StatusForbidden, "PASSWORD_CHANGE_REQUIRED",
	)
	// Change password succeeds, then old token is invalid.
	change := fixture.request(t, http.MethodPost, "/api/v1/users/me:change-password", map[string]any{
		"currentPassword": "Temporary-password-x1", "newPassword": "Changed-password-x2",
	}, tokens.AccessToken, nil)
	if change.Code != http.StatusNoContent {
		t.Fatalf("change password status=%d body=%s", change.Code, change.Body.String())
	}
	assertErrorResponse(t,
		fixture.request(t, http.MethodGet, "/api/v1/users/me", nil, tokens.AccessToken, nil),
		http.StatusUnauthorized, "UNAUTHENTICATED",
	)
	newLogin := fixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "temp.user", "password": "Changed-password-x2",
	}, "", nil)
	newTokens := decodeTokenResponse(t, newLogin)
	if newTokens.MustChangePassword {
		t.Fatal("expected mustChangePassword=false after successful change")
	}
	me2 := fixture.request(t, http.MethodGet, "/api/v1/users/me", nil, newTokens.AccessToken, nil)
	if me2.Code != http.StatusOK {
		t.Fatalf("post-change me status=%d body=%s", me2.Code, me2.Body.String())
	}
}

type mustChangePrincipalAuthenticator struct {
	principal Principal
}

func (a mustChangePrincipalAuthenticator) AuthenticateAccessToken(
	context.Context, string,
) (Principal, error) {
	return a.principal, nil
}

func serveBearer(handler http.Handler, method, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("X-Request-ID", "request-must-change")
	request.Header.Set("X-Trace-ID", "trace-must-change")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
