package httptransport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"actweave/backend/internal/workspace"
)

func TestV1WorkspaceListPaginationRoleAndSummary(t *testing.T) {
	fixture := newV1WorkspaceFixture(t)
	admin := fixture.login(t, v1AdminName, v1AdminPass)

	for i := 0; i < 3; i++ {
		resp := fixture.request(t, http.MethodPost, "/api/v1/workspaces", map[string]any{
			"slug":        fmt.Sprintf("page-ws-%d", i),
			"displayName": fmt.Sprintf("Page WS %d", i),
			"mode":        "SANDBOX",
		}, admin.AccessToken, nil)
		if resp.Code != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", resp.Code, resp.Body.String())
		}
		var created workspaceDTO
		if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		if created.CurrentUserRole != workspace.RoleOwner {
			t.Fatalf("create role=%q", created.CurrentUserRole)
		}
	}

	listed := fixture.request(t, http.MethodGet, "/api/v1/workspaces?page=1&pageSize=10&sortBy=updatedAt&sortOrder=desc", nil, admin.AccessToken, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	var body struct {
		Items      []workspaceDTO `json:"items"`
		Pagination struct {
			Page     int `json:"page"`
			PageSize int `json:"pageSize"`
			Total    int `json:"total"`
		} `json:"pagination"`
		Summary struct {
			Total       int `json:"total"`
			Active      int `json:"active"`
			Production  int `json:"production"`
			BoundAgents int `json:"boundAgents"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Pagination.Page != 1 || body.Pagination.PageSize != 10 || body.Pagination.Total < 3 {
		t.Fatalf("pagination=%+v", body.Pagination)
	}
	if body.Summary.Total < 3 || body.Summary.Active < 1 {
		t.Fatalf("summary=%+v", body.Summary)
	}
	for _, item := range body.Items {
		if item.CurrentUserRole == "" {
			t.Fatalf("missing currentUserRole on item %+v", item)
		}
	}

	detail := fixture.request(t, http.MethodGet, "/api/v1/workspaces/"+body.Items[0].ID, nil, admin.AccessToken, nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var detailDTO workspaceDTO
	if err := json.Unmarshal(detail.Body.Bytes(), &detailDTO); err != nil {
		t.Fatal(err)
	}
	if detailDTO.CurrentUserRole != workspace.RoleOwner {
		t.Fatalf("detail role=%q", detailDTO.CurrentUserRole)
	}

	bad := fixture.request(t, http.MethodGet, "/api/v1/workspaces?page=1&pageSize=15", nil, admin.AccessToken, nil)
	// Existing envelope maps workspace.ErrInvalid → 422 VALIDATION_ERROR.
	assertErrorResponse(t, bad, http.StatusUnprocessableEntity, "VALIDATION_ERROR")

	legacy := fixture.request(t, http.MethodGet, "/api/v1/workspaces?limit=50", nil, admin.AccessToken, nil)
	if legacy.Code != http.StatusOK || !json.Valid(legacy.Body.Bytes()) {
		t.Fatalf("legacy list status=%d body=%s", legacy.Code, legacy.Body.String())
	}
	var legacyBody struct {
		Items []workspaceDTO `json:"items"`
	}
	if err := json.Unmarshal(legacy.Body.Bytes(), &legacyBody); err != nil {
		t.Fatal(err)
	}
	if len(legacyBody.Items) == 0 {
		t.Fatal("legacy items empty")
	}
}
