package httptransport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/authn"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/identity"
	"actweave/backend/internal/workspace"
)

const (
	v1AdminUserID     = "e38f1f2e-7b5a-7c3d-8e9f-123456789001"
	v1AuthWorkspaceID = "e38f1f2e-7b5a-7c3d-8e9f-123456789002"
	v1AdminName       = "v1.admin"
	v1AdminPass       = "Strong-admin-password-1"
)

func TestV1AuthLoginRefreshLogoutAndMe(t *testing.T) {
	fixture := newV1AuthFixture(t)
	login := fixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": v1AdminName, "password": v1AdminPass,
	}, "", nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	tokens := decodeTokenResponse(t, login)
	refresh := responseCookie(t, login, refreshCookieName)
	if tokens.AccessToken == "" || tokens.User.ID != v1AdminUserID || tokens.User.PlatformRole != identity.PlatformRoleAdmin ||
		refresh.Value == "" || !refresh.HttpOnly || !refresh.Secure || refresh.SameSite != http.SameSiteStrictMode ||
		strings.Contains(login.Body.String(), "refreshToken") || strings.Contains(login.Body.String(), "passwordHash") {
		t.Fatalf("unsafe login response body=%s cookie=%+v", login.Body.String(), refresh)
	}

	me := fixture.request(t, http.MethodGet, "/api/v1/users/me", nil, tokens.AccessToken, nil)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"username":"v1.admin"`) ||
		strings.Contains(me.Body.String(), "Password") {
		t.Fatalf("me status=%d body=%s", me.Code, me.Body.String())
	}

	refreshed := fixture.request(t, http.MethodPost, "/api/v1/auth/refresh", nil, "", refresh)
	if refreshed.Code != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", refreshed.Code, refreshed.Body.String())
	}
	rotated := responseCookie(t, refreshed, refreshCookieName)
	refreshedTokens := decodeTokenResponse(t, refreshed)
	if rotated.Value == refresh.Value || refreshedTokens.AccessToken == "" {
		t.Fatalf("refresh token was not rotated: old=%q new=%q", refresh.Value, rotated.Value)
	}
	replayed := fixture.request(t, http.MethodPost, "/api/v1/auth/refresh", nil, "", refresh)
	assertErrorResponse(t, replayed, http.StatusUnauthorized, "UNAUTHENTICATED")

	loggedOut := fixture.request(t, http.MethodPost, "/api/v1/auth/logout", nil,
		refreshedTokens.AccessToken, rotated)
	if loggedOut.Code != http.StatusNoContent || responseCookie(t, loggedOut, refreshCookieName).MaxAge != -1 {
		t.Fatalf("logout status=%d headers=%v body=%s", loggedOut.Code, loggedOut.Header(), loggedOut.Body.String())
	}
	afterLogout := fixture.request(t, http.MethodPost, "/api/v1/auth/refresh", nil, "", rotated)
	assertErrorResponse(t, afterLogout, http.StatusUnauthorized, "UNAUTHENTICATED")

	unknownField := fixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": v1AdminName, "password": v1AdminPass, "platformRole": "PLATFORM_ADMIN",
	}, "", nil)
	assertErrorResponse(t, unknownField, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
}

func TestV1UserProfilePasswordAndAdminCommands(t *testing.T) {
	fixture := newV1AuthFixture(t)
	adminLogin := fixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": v1AdminName, "password": v1AdminPass,
	}, "", nil)
	adminTokens := decodeTokenResponse(t, adminLogin)

	updated := fixture.request(t, http.MethodPatch, "/api/v1/users/me", map[string]any{
		"displayName": "V1 Administrator", "locale": "en-SG",
		"lockVersion": adminTokens.User.LockVersion,
	}, adminTokens.AccessToken, nil)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"displayName":"V1 Administrator"`) {
		t.Fatalf("update me status=%d body=%s", updated.Code, updated.Body.String())
	}
	var updatedUser userDTO
	if err := json.Unmarshal(updated.Body.Bytes(), &updatedUser); err != nil {
		t.Fatal(err)
	}
	stale := fixture.request(t, http.MethodPatch, "/api/v1/users/me", map[string]any{
		"displayName": "Stale", "lockVersion": adminTokens.User.LockVersion,
	}, adminTokens.AccessToken, nil)
	assertErrorResponse(t, stale, http.StatusConflict, "CONFLICT")

	createdResponse := fixture.request(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "v1.operator", "displayName": "V1 Operator",
		"password": "Temporary-password-1", "platformRole": "USER",
		"locale": "zh-CN", "timezone": "Asia/Singapore",
	}, adminTokens.AccessToken, nil)
	if createdResponse.Code != http.StatusCreated || strings.Contains(createdResponse.Body.String(), "Temporary-password-1") ||
		strings.Contains(createdResponse.Body.String(), "passwordHash") {
		t.Fatalf("create user status=%d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	var created userDTO
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Status != identity.StatusActive || created.PlatformRole != identity.PlatformRoleUser {
		t.Fatalf("created user=%+v", created)
	}

	operatorLogin := fixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "v1.operator", "password": "Temporary-password-1",
	}, "", nil)
	operatorTokens := decodeTokenResponse(t, operatorLogin)
	deniedList := fixture.request(t, http.MethodGet, "/api/v1/admin/users", nil,
		operatorTokens.AccessToken, nil)
	assertErrorResponse(t, deniedList, http.StatusForbidden, "FORBIDDEN")
	promotedResponse := fixture.request(t, http.MethodPost,
		"/api/v1/admin/users/"+created.ID+":change-platform-role", map[string]any{
			"platformRole": "PLATFORM_ADMIN", "lockVersion": created.LockVersion,
		}, adminTokens.AccessToken, nil)
	if promotedResponse.Code != http.StatusOK || !strings.Contains(promotedResponse.Body.String(), `"platformRole":"PLATFORM_ADMIN"`) {
		t.Fatalf("promote user status=%d body=%s", promotedResponse.Code, promotedResponse.Body.String())
	}
	var promoted userDTO
	if err := json.Unmarshal(promotedResponse.Body.Bytes(), &promoted); err != nil {
		t.Fatal(err)
	}
	oldRoleRefresh := responseCookie(t, operatorLogin, refreshCookieName)
	revokedByRole := fixture.request(t, http.MethodPost, "/api/v1/auth/refresh", nil, "", oldRoleRefresh)
	assertErrorResponse(t, revokedByRole, http.StatusUnauthorized, "UNAUTHENTICATED")
	demotedResponse := fixture.request(t, http.MethodPost,
		"/api/v1/admin/users/"+created.ID+":change-platform-role", map[string]any{
			"platformRole": "USER", "lockVersion": promoted.LockVersion,
		}, adminTokens.AccessToken, nil)
	if demotedResponse.Code != http.StatusOK || !strings.Contains(demotedResponse.Body.String(), `"platformRole":"USER"`) {
		t.Fatalf("demote user status=%d body=%s", demotedResponse.Code, demotedResponse.Body.String())
	}
	lastAdmin := fixture.request(t, http.MethodPost,
		"/api/v1/admin/users/"+v1AdminUserID+":change-platform-role", map[string]any{
			"platformRole": "USER", "lockVersion": updatedUser.LockVersion,
		}, adminTokens.AccessToken, nil)
	assertErrorResponse(t, lastAdmin, http.StatusConflict, "CONFLICT")

	listed := fixture.request(t, http.MethodGet, "/api/v1/admin/users?limit=10", nil,
		adminTokens.AccessToken, nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "v1.operator") ||
		!strings.Contains(listed.Body.String(), `"pagination":{"page":1,"pageSize":10,"total":2}`) {
		t.Fatalf("list users status=%d body=%s", listed.Code, listed.Body.String())
	}
	filtered := fixture.request(t, http.MethodGet,
		"/api/v1/admin/users?query=OPERATOR&status=ACTIVE&platformRole=USER&page=1&pageSize=1", nil,
		adminTokens.AccessToken, nil)
	if filtered.Code != http.StatusOK || !strings.Contains(filtered.Body.String(), `"username":"v1.operator"`) ||
		!strings.Contains(filtered.Body.String(), `"total":1`) {
		t.Fatalf("filter users status=%d body=%s", filtered.Code, filtered.Body.String())
	}
	invalidPage := fixture.request(t, http.MethodGet, "/api/v1/admin/users?pageSize=101", nil,
		adminTokens.AccessToken, nil)
	assertErrorResponse(t, invalidPage, http.StatusUnprocessableEntity, "VALIDATION_ERROR")

	workspaceRepository, err := workspace.NewRepository(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceRepository.Create(context.Background(), workspace.NewWorkspace{
		ID: v1AuthWorkspaceID, Slug: "v1-auth-workspace", DisplayName: "V1 Auth Workspace",
		Mode: workspace.ModeProduction, OwnerUserID: v1AdminUserID, CreatedBy: v1AdminUserID,
		Settings: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceRepository.AddMember(context.Background(), workspace.NewMember{
		WorkspaceID: v1AuthWorkspaceID, UserID: created.ID,
		Role: workspace.RoleViewer, InvitedBy: v1AdminUserID,
	}); err != nil {
		t.Fatal(err)
	}
	userWorkspaces := fixture.request(t, http.MethodGet,
		"/api/v1/admin/users/"+created.ID+"/workspaces", nil, adminTokens.AccessToken, nil)
	if userWorkspaces.Code != http.StatusOK || !strings.Contains(userWorkspaces.Body.String(), `"workspaceSlug":"v1-auth-workspace"`) ||
		!strings.Contains(userWorkspaces.Body.String(), `"role":"VIEWER"`) {
		t.Fatalf("list user workspaces status=%d body=%s", userWorkspaces.Code, userWorkspaces.Body.String())
	}
	deniedWorkspaces := fixture.request(t, http.MethodGet,
		"/api/v1/admin/users/"+created.ID+"/workspaces", nil, operatorTokens.AccessToken, nil)
	assertErrorResponse(t, deniedWorkspaces, http.StatusForbidden, "FORBIDDEN")
	reset := fixture.request(t, http.MethodPost, "/api/v1/admin/users/"+created.ID+":reset-password", map[string]any{
		"temporaryPassword": "Reset-password-2",
	}, adminTokens.AccessToken, nil)
	if reset.Code != http.StatusNoContent {
		t.Fatalf("reset password status=%d body=%s", reset.Code, reset.Body.String())
	}
	oldRefresh := responseCookie(t, operatorLogin, refreshCookieName)
	revoked := fixture.request(t, http.MethodPost, "/api/v1/auth/refresh", nil, "", oldRefresh)
	assertErrorResponse(t, revoked, http.StatusUnauthorized, "UNAUTHENTICATED")
	resetLogin := fixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "v1.operator", "password": "Reset-password-2",
	}, "", nil)
	resetTokens := decodeTokenResponse(t, resetLogin)
	if !resetTokens.MustChangePassword {
		t.Fatal("reset password did not require password change")
	}

	locked := fixture.request(t, http.MethodPatch, "/api/v1/admin/users/"+created.ID, map[string]any{
		"status": "LOCKED", "lockVersion": resetTokens.User.LockVersion,
	}, adminTokens.AccessToken, nil)
	if locked.Code != http.StatusOK {
		t.Fatalf("lock user status=%d body=%s", locked.Code, locked.Body.String())
	}
	var lockedUser userDTO
	if err := json.Unmarshal(locked.Body.Bytes(), &lockedUser); err != nil {
		t.Fatal(err)
	}
	unlocked := fixture.request(t, http.MethodPost, "/api/v1/admin/users/"+created.ID+":unlock", map[string]any{
		"lockVersion": lockedUser.LockVersion,
	}, adminTokens.AccessToken, nil)
	if unlocked.Code != http.StatusOK || !strings.Contains(unlocked.Body.String(), `"status":"ACTIVE"`) {
		t.Fatalf("unlock user status=%d body=%s", unlocked.Code, unlocked.Body.String())
	}

	change := fixture.request(t, http.MethodPost, "/api/v1/users/me:change-password", map[string]any{
		"currentPassword": v1AdminPass, "newPassword": "Changed-admin-password-2",
	}, adminTokens.AccessToken, nil)
	if change.Code != http.StatusNoContent || responseCookie(t, change, refreshCookieName).MaxAge != -1 {
		t.Fatalf("change password status=%d headers=%v body=%s", change.Code, change.Header(), change.Body.String())
	}
	newLogin := fixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": v1AdminName, "password": "Changed-admin-password-2",
	}, "", nil)
	if newLogin.Code != http.StatusOK {
		t.Fatalf("login with changed password status=%d body=%s", newLogin.Code, newLogin.Body.String())
	}
	_ = updatedUser
}

type v1AuthFixture struct {
	router     http.Handler
	db         *sql.DB
	service    *authn.Service
	identity   *identity.Repository
	auth       *AccessTokenAuthenticator
	authRoutes *AuthUserRoutes
}

func newV1AuthFixture(t *testing.T) *v1AuthFixture {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	repository, err := identity.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	passwords, err := authn.NewPasswordManager(authn.Argon2idParams{
		MemoryKiB: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := passwords.Hash(v1AdminPass)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateLocalUser(context.Background(), identity.NewLocalUser{
		ID: v1AdminUserID, Username: v1AdminName, DisplayName: "V1 Admin",
		Status: identity.StatusActive, PlatformRole: identity.PlatformRoleAdmin,
		PasswordHash: hash, PasswordAlgorithm: authn.PasswordAlgorithmArgon2id,
	}); err != nil {
		t.Fatal(err)
	}
	access, err := authn.NewAccessTokenManager(strings.Repeat("v", 32), "actweave-v1-test", 0)
	if err != nil {
		t.Fatal(err)
	}
	service, err := authn.NewService(
		repository, passwords, access, authn.NewRefreshTokenManager(),
		authn.ServiceConfig{RefreshTTL: 7 * 24 * time.Hour,
			LockoutPolicy: authn.LockoutPolicy{MaxFailedAttempts: 3, Duration: 15 * time.Minute}},
	)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, _ := NewAccessTokenAuthenticator(access)
	routes, _ := NewAuthUserRoutes(service)
	router, err := NewRouter(Config{Authenticator: authenticator, Registrars: []V1RouteRegistrar{routes}})
	if err != nil {
		t.Fatal(err)
	}
	return &v1AuthFixture{
		router: router, db: db, service: service, identity: repository,
		auth: authenticator, authRoutes: routes,
	}
}

func (fixture *v1AuthFixture) request(
	t *testing.T,
	method, path string,
	body any,
	accessToken string,
	cookie *http.Cookie,
) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	request.Header.Set("X-Request-ID", "request-v1-auth-test")
	request.Header.Set("X-Trace-ID", "trace-v1-auth-test")
	response := httptest.NewRecorder()
	fixture.router.ServeHTTP(response, request)
	return response
}

func decodeTokenResponse(t *testing.T, response *httptest.ResponseRecorder) tokenResponse {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("token response status=%d body=%s", response.Code, response.Body.String())
	}
	var value tokenResponse
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func responseCookie(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %s missing from headers %v", name, response.Header())
	return nil
}
