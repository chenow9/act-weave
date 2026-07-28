package workspace

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func TestListAccessiblePagePaginationRolesAndSummary(t *testing.T) {
	db := newWorkspaceRepositoryDatabase(t)
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	ctx := context.Background()

	const total = 1001
	ids := make([]string, 0, total)
	for i := 0; i < total; i++ {
		id := uuid.Must(uuid.NewV7()).String()
		ids = append(ids, id)
		mode := ModeSandbox
		if i%3 == 0 {
			mode = ModeProduction
		}
		created, err := repository.Create(ctx, NewWorkspace{
			ID:          id,
			Slug:        fmt.Sprintf("ws-%04d", i),
			DisplayName: fmt.Sprintf("Workspace %04d", i),
			Mode:        mode,
			OwnerUserID: repositoryOwnerID,
			CreatedBy:   repositoryOwnerID,
		})
		if err != nil {
			t.Fatalf("create workspace %d: %v", i, err)
		}
		if i%10 == 0 {
			// leave some without default agent; Create may not set agent
			_ = created
		}
	}

	// Disable membership on one workspace via SQL — should disappear from list.
	disabledWS := ids[0]
	if _, err := db.Exec(`UPDATE workspace_members SET disabled_at = NOW() WHERE workspace_id = $1 AND user_id = $2`, disabledWS, repositoryOwnerID); err != nil {
		t.Fatalf("disable membership: %v", err)
	}

	page, err := repository.ListAccessiblePage(ctx, repositoryOwnerID, WorkspaceListQuery{
		Page: 1, PageSize: 20, SortBy: "slug", SortOrder: "asc",
	})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if page.Summary.Total != total-1 {
		t.Fatalf("summary total=%d want %d", page.Summary.Total, total-1)
	}
	if page.Total != total-1 {
		t.Fatalf("page total=%d want %d", page.Total, total-1)
	}
	if len(page.Items) != 20 {
		t.Fatalf("page 1 items=%d", len(page.Items))
	}
	for _, item := range page.Items {
		if item.CurrentUserRole != RoleOwner {
			t.Fatalf("expected OWNER role, got %s on %s", item.CurrentUserRole, item.ID)
		}
		if item.ID == disabledWS {
			t.Fatalf("disabled membership workspace leaked into page")
		}
	}

	lastPage := ((total - 1) + 19) / 20
	end, err := repository.ListAccessiblePage(ctx, repositoryOwnerID, WorkspaceListQuery{
		Page: lastPage, PageSize: 20, SortBy: "slug", SortOrder: "asc",
	})
	if err != nil {
		t.Fatalf("list last page: %v", err)
	}
	expectedLast := (total - 1) - (lastPage-1)*20
	if len(end.Items) != expectedLast {
		t.Fatalf("last page items=%d want %d", len(end.Items), expectedLast)
	}

	// status filter uses full-set summary still (D9-A)
	activeOnly, err := repository.ListAccessiblePage(ctx, repositoryOwnerID, WorkspaceListQuery{
		Page: 1, PageSize: 10, Status: statusPtr(StatusActive),
	})
	if err != nil {
		t.Fatalf("list active filter: %v", err)
	}
	if activeOnly.Summary.Total != total-1 {
		t.Fatalf("filtered page must keep full-set summary total, got %d", activeOnly.Summary.Total)
	}
	if activeOnly.Total > total-1 {
		t.Fatalf("filtered total larger than accessible set")
	}

	// invalid pageSize
	if _, err := repository.ListAccessiblePage(ctx, repositoryOwnerID, WorkspaceListQuery{Page: 1, PageSize: 15}); err != ErrInvalid {
		t.Fatalf("expected invalid pageSize, got %v", err)
	}
	// invalid sort
	if _, err := repository.ListAccessiblePage(ctx, repositoryOwnerID, WorkspaceListQuery{Page: 1, PageSize: 20, SortBy: "hax;drop"}); err != ErrInvalid {
		t.Fatalf("expected invalid sort, got %v", err)
	}

	// legacy limit path
	legacy, err := repository.ListAccessiblePage(ctx, repositoryOwnerID, WorkspaceListQuery{LegacyLimit: 50})
	if err != nil {
		t.Fatalf("legacy list: %v", err)
	}
	if !legacy.Legacy || len(legacy.Items) != 50 {
		t.Fatalf("legacy page unexpected: legacy=%v n=%d", legacy.Legacy, len(legacy.Items))
	}

	// roles: add a second user as EDITOR
	editorID := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`INSERT INTO users (id, username, display_name) VALUES ($1,'editor.user','Editor User')`, editorID); err != nil {
		t.Fatalf("insert editor: %v", err)
	}
	target := ids[1]
	if _, err := repository.AddMember(ctx, NewMember{WorkspaceID: target, UserID: editorID, Role: RoleEditor, InvitedBy: repositoryOwnerID}); err != nil {
		t.Fatalf("add editor: %v", err)
	}
	accessible, err := repository.GetAccessible(ctx, editorID, target)
	if err != nil {
		t.Fatalf("get accessible editor: %v", err)
	}
	if accessible.CurrentUserRole != RoleEditor {
		t.Fatalf("editor role=%s", accessible.CurrentUserRole)
	}
	// editor must not see owner-only disabled membership workspace, and only one workspace
	editorPage, err := repository.ListAccessiblePage(ctx, editorID, WorkspaceListQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("editor page: %v", err)
	}
	if editorPage.Summary.Total != 1 || len(editorPage.Items) != 1 || editorPage.Items[0].CurrentUserRole != RoleEditor {
		t.Fatalf("editor page unexpected: %+v", editorPage)
	}
}

func statusPtr(status Status) *Status { return &status }
