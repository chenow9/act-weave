package httptransport

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/identity"
	"actweave/backend/internal/workspace"
)

func TestV1WorkspaceCRUDStatusAndIsolation(t *testing.T) {
	fixture := newV1WorkspaceFixture(t)
	admin := fixture.login(t, v1AdminName, v1AdminPass)
	createdResponse := fixture.request(t, http.MethodPost, "/api/v1/workspaces", map[string]any{
		"slug": "v1-workspace", "displayName": "V1 Workspace",
		"mode": "PRODUCTION", "settings": map[string]any{"region": "sg"},
	}, admin.AccessToken, nil)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create workspace status=%d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	var created workspaceDTO
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.OwnerUserID != v1AdminUserID ||
		created.CreatedBy != v1AdminUserID || created.UpdatedBy != v1AdminUserID || created.LockVersion != 1 {
		t.Fatalf("created workspace=%+v", created)
	}
	if created.CreatedByUsername != v1AdminName || created.UpdatedByUsername != v1AdminName {
		t.Fatalf("workspace audit actors were not resolved to usernames: %+v", created)
	}
	listed := fixture.request(t, http.MethodGet, "/api/v1/workspaces", nil, admin.AccessToken, nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), created.ID) {
		t.Fatalf("list workspaces status=%d body=%s", listed.Code, listed.Body.String())
	}
	detail := fixture.request(t, http.MethodGet, "/api/v1/workspaces/"+created.ID, nil, admin.AccessToken, nil)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"createdBy":"`+v1AdminUserID+`"`) ||
		!strings.Contains(detail.Body.String(), `"createdByUsername":"`+v1AdminName+`"`) {
		t.Fatalf("workspace detail status=%d body=%s", detail.Code, detail.Body.String())
	}

	disabled := fixture.request(t, http.MethodPost, "/api/v1/workspaces/"+created.ID+":disable", map[string]any{
		"lockVersion": created.LockVersion,
	}, admin.AccessToken, nil)
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable workspace status=%d body=%s", disabled.Code, disabled.Body.String())
	}
	var disabledWorkspace workspaceDTO
	if err := json.Unmarshal(disabled.Body.Bytes(), &disabledWorkspace); err != nil {
		t.Fatal(err)
	}
	enabled := fixture.request(t, http.MethodPost, "/api/v1/workspaces/"+created.ID+":enable", map[string]any{
		"lockVersion": disabledWorkspace.LockVersion,
	}, admin.AccessToken, nil)
	if enabled.Code != http.StatusOK || !strings.Contains(enabled.Body.String(), `"status":"ACTIVE"`) {
		t.Fatalf("enable workspace status=%d body=%s", enabled.Code, enabled.Body.String())
	}
	var enabledWorkspace workspaceDTO
	if err := json.Unmarshal(enabled.Body.Bytes(), &enabledWorkspace); err != nil {
		t.Fatal(err)
	}
	deleted := fixture.request(t, http.MethodDelete,
		"/api/v1/workspaces/"+created.ID+"?lockVersion="+strconv.FormatInt(enabledWorkspace.LockVersion, 10),
		nil, admin.AccessToken, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete workspace status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	afterDelete := fixture.request(t, http.MethodGet, "/api/v1/workspaces/"+created.ID, nil, admin.AccessToken, nil)
	assertErrorResponse(t, afterDelete, http.StatusNotFound, "NOT_FOUND")
}

func TestV1MemberRBACPathOwnershipAndCrossWorkspaceNotVisible(t *testing.T) {
	fixture := newV1WorkspaceFixture(t)
	admin := fixture.login(t, v1AdminName, v1AdminPass)
	workspaceResponse := fixture.request(t, http.MethodPost, "/api/v1/workspaces", map[string]any{
		"slug": "v1-members", "displayName": "V1 Members", "mode": "SANDBOX",
	}, admin.AccessToken, nil)
	var value workspaceDTO
	if err := json.Unmarshal(workspaceResponse.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	userResponse := fixture.request(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "v1.member", "displayName": "V1 Member",
		"password": "Member-password-1", "platformRole": "USER",
	}, admin.AccessToken, nil)
	if userResponse.Code != http.StatusCreated {
		t.Fatalf("create member user status=%d body=%s", userResponse.Code, userResponse.Body.String())
	}
	var memberUser userDTO
	if err := json.Unmarshal(userResponse.Body.Bytes(), &memberUser); err != nil {
		t.Fatal(err)
	}
	memberLogin := fixture.loginAndClearMustChange(t, "v1.member", "Member-password-1", "Member-password-1x")
	candidates := fixture.request(t, http.MethodGet,
		"/api/v1/workspaces/"+value.ID+"/member-candidates?query=v1.member",
		nil, admin.AccessToken, nil)
	if candidates.Code != http.StatusOK || !strings.Contains(candidates.Body.String(), memberUser.ID) ||
		!strings.Contains(candidates.Body.String(), `"username":"v1.member"`) ||
		strings.Contains(candidates.Body.String(), `"email"`) {
		t.Fatalf("owner candidate search status=%d body=%s", candidates.Code, candidates.Body.String())
	}

	pathOverride := fixture.request(t, http.MethodPost, "/api/v1/workspaces/"+value.ID+"/members", map[string]any{
		"workspaceId": "f38f1f2e-7b5a-7c3d-8e9f-123456789099",
		"userId":      memberUser.ID, "role": "VIEWER",
	}, admin.AccessToken, nil)
	assertErrorResponse(t, pathOverride, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	added := fixture.request(t, http.MethodPost, "/api/v1/workspaces/"+value.ID+"/members", map[string]any{
		"userId": memberUser.ID, "role": "VIEWER",
	}, admin.AccessToken, nil)
	if added.Code != http.StatusCreated || !strings.Contains(added.Body.String(), `"role":"VIEWER"`) {
		t.Fatalf("add member status=%d body=%s", added.Code, added.Body.String())
	}
	assignedCandidates := fixture.request(t, http.MethodGet,
		"/api/v1/workspaces/"+value.ID+"/member-candidates?query=v1.member",
		nil, admin.AccessToken, nil)
	if assignedCandidates.Code != http.StatusOK || strings.Contains(assignedCandidates.Body.String(), memberUser.ID) {
		t.Fatalf("assigned user remained a candidate status=%d body=%s", assignedCandidates.Code, assignedCandidates.Body.String())
	}
	viewerDetail := fixture.request(t, http.MethodGet, "/api/v1/workspaces/"+value.ID,
		nil, memberLogin.AccessToken, nil)
	if viewerDetail.Code != http.StatusOK {
		t.Fatalf("viewer workspace detail status=%d body=%s", viewerDetail.Code, viewerDetail.Body.String())
	}
	viewerEdit := fixture.request(t, http.MethodPatch, "/api/v1/workspaces/"+value.ID, map[string]any{
		"displayName": "Forbidden", "lockVersion": value.LockVersion,
	}, memberLogin.AccessToken, nil)
	assertErrorResponse(t, viewerEdit, http.StatusForbidden, "FORBIDDEN")
	viewerMembers := fixture.request(t, http.MethodGet, "/api/v1/workspaces/"+value.ID+"/members",
		nil, memberLogin.AccessToken, nil)
	if viewerMembers.Code != http.StatusOK || !strings.Contains(viewerMembers.Body.String(), memberUser.ID) {
		t.Fatalf("viewer member list status=%d body=%s", viewerMembers.Code, viewerMembers.Body.String())
	}
	viewerCandidates := fixture.request(t, http.MethodGet,
		"/api/v1/workspaces/"+value.ID+"/member-candidates",
		nil, memberLogin.AccessToken, nil)
	assertErrorResponse(t, viewerCandidates, http.StatusForbidden, "FORBIDDEN")

	promoted := fixture.request(t, http.MethodPatch,
		"/api/v1/workspaces/"+value.ID+"/members/"+memberUser.ID,
		map[string]any{"role": "ADMIN"}, admin.AccessToken, nil)
	if promoted.Code != http.StatusOK || !strings.Contains(promoted.Body.String(), `"role":"ADMIN"`) {
		t.Fatalf("promote member status=%d body=%s", promoted.Code, promoted.Body.String())
	}
	adminCandidates := fixture.request(t, http.MethodGet,
		"/api/v1/workspaces/"+value.ID+"/member-candidates",
		nil, memberLogin.AccessToken, nil)
	if adminCandidates.Code != http.StatusOK {
		t.Fatalf("workspace admin candidate search status=%d body=%s", adminCandidates.Code, adminCandidates.Body.String())
	}
	edited := fixture.request(t, http.MethodPatch, "/api/v1/workspaces/"+value.ID, map[string]any{
		"displayName": "Edited by member", "lockVersion": value.LockVersion,
	}, memberLogin.AccessToken, nil)
	if edited.Code != http.StatusOK || !strings.Contains(edited.Body.String(), `"updatedBy":"`+memberUser.ID+`"`) ||
		!strings.Contains(edited.Body.String(), `"updatedByUsername":"v1.member"`) {
		t.Fatalf("editor update status=%d body=%s", edited.Code, edited.Body.String())
	}
	removed := fixture.request(t, http.MethodDelete,
		"/api/v1/workspaces/"+value.ID+"/members/"+memberUser.ID,
		nil, admin.AccessToken, nil)
	if removed.Code != http.StatusNoContent {
		t.Fatalf("remove member status=%d body=%s", removed.Code, removed.Body.String())
	}
	afterRemoval := fixture.request(t, http.MethodGet, "/api/v1/workspaces/"+value.ID,
		nil, memberLogin.AccessToken, nil)
	assertErrorResponse(t, afterRemoval, http.StatusNotFound, "NOT_FOUND")
	unknown := fixture.request(t, http.MethodGet,
		"/api/v1/workspaces/f38f1f2e-7b5a-7c3d-8e9f-123456789099",
		nil, admin.AccessToken, nil)
	assertErrorResponse(t, unknown, http.StatusNotFound, "NOT_FOUND")
}

type v1WorkspaceFixture struct{ *v1AuthFixture }

func newV1WorkspaceFixture(t *testing.T) *v1WorkspaceFixture {
	t.Helper()
	authFixture := newV1AuthFixture(t)
	repository, err := workspace.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := authz.NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	identityRepository, err := identity.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewWorkspaceRoutes(repository, authorizer, identityRepository)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		Authenticator: authFixture.auth,
		Registrars:    []V1RouteRegistrar{authFixture.authRoutes, routes},
	})
	if err != nil {
		t.Fatal(err)
	}
	authFixture.router = router
	return &v1WorkspaceFixture{v1AuthFixture: authFixture}
}

func (fixture *v1WorkspaceFixture) login(t *testing.T, username, password string) tokenResponse {
	t.Helper()
	response := fixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": username, "password": password,
	}, "", nil)
	return decodeTokenResponse(t, response)
}
