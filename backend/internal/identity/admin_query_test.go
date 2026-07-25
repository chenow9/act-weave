package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/workspace"
)

const (
	adminQueryOwnerID     = "018f1f2e-7b5a-7c3d-8e9f-7234567890ab"
	adminQueryMemberID    = "018f1f2e-7b5a-7c3d-8e9f-7234567890ac"
	adminQueryDisabledID  = "018f1f2e-7b5a-7c3d-8e9f-7234567890ad"
	adminQueryWorkspaceID = "018f1f2e-7b5a-7c3d-8e9f-7234567890ae"
)

func TestAdminUserSearchFiltersAndPaginates(t *testing.T) {
	repository, _ := newAdminQueryRepository(t)
	createAdminQueryUser(t, repository, adminQueryOwnerID, "owner.alpha", "Alpha Owner", StatusActive, PlatformRoleAdmin)
	createAdminQueryUser(t, repository, adminQueryMemberID, "member.alpha", "Alpha Member", StatusActive, PlatformRoleUser)
	createAdminQueryUser(t, repository, adminQueryDisabledID, "disabled.beta", "Beta Disabled", StatusDisabled, PlatformRoleUser)

	active := StatusActive
	page, err := repository.SearchUsers(context.Background(), UserListQuery{
		Query: "ALPHA", Status: &active, Limit: 1, Offset: 0,
	})
	if err != nil {
		t.Fatalf("search first page: %v", err)
	}
	if page.Total != 2 || len(page.Items) != 1 {
		t.Fatalf("unexpected first page: %+v", page)
	}
	second, err := repository.SearchUsers(context.Background(), UserListQuery{
		Query: "alpha", Status: &active, Limit: 1, Offset: 1,
	})
	if err != nil || second.Total != 2 || len(second.Items) != 1 || second.Items[0].ID == page.Items[0].ID {
		t.Fatalf("unexpected second page: page=%+v err=%v", second, err)
	}
	role := PlatformRoleAdmin
	admins, err := repository.SearchUsers(context.Background(), UserListQuery{
		PlatformRole: &role, Limit: 20,
	})
	if err != nil || admins.Total != 1 || len(admins.Items) != 1 || admins.Items[0].ID != adminQueryOwnerID {
		t.Fatalf("unexpected administrator filter: page=%+v err=%v", admins, err)
	}
	if _, err := repository.SearchUsers(context.Background(), UserListQuery{Limit: 101}); err != ErrInvalid {
		t.Fatalf("expected invalid page size, got %v", err)
	}
}

func TestAdminUserWorkspaceProjection(t *testing.T) {
	repository, db := newAdminQueryRepository(t)
	createAdminQueryUser(t, repository, adminQueryOwnerID, "workspace.owner", "Workspace Owner", StatusActive, PlatformRoleAdmin)
	createAdminQueryUser(t, repository, adminQueryMemberID, "workspace.member", "Workspace Member", StatusActive, PlatformRoleUser)
	workspaceRepository, err := workspace.NewRepository(db)
	if err != nil {
		t.Fatalf("create workspace repository: %v", err)
	}
	if _, err := workspaceRepository.Create(context.Background(), workspace.NewWorkspace{
		ID: adminQueryWorkspaceID, Slug: "identity-membership", DisplayName: "Identity Membership",
		Mode: workspace.ModeProduction, OwnerUserID: adminQueryOwnerID, CreatedBy: adminQueryOwnerID,
		Settings: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := workspaceRepository.AddMember(context.Background(), workspace.NewMember{
		WorkspaceID: adminQueryWorkspaceID, UserID: adminQueryMemberID,
		Role: workspace.RoleEditor, InvitedBy: adminQueryOwnerID,
	}); err != nil {
		t.Fatalf("add workspace member: %v", err)
	}

	items, err := repository.ListUserWorkspaceMemberships(context.Background(), adminQueryMemberID, false)
	if err != nil {
		t.Fatalf("list user memberships: %v", err)
	}
	if len(items) != 1 || items[0].WorkspaceID != adminQueryWorkspaceID ||
		items[0].WorkspaceSlug != "identity-membership" || items[0].Role != string(workspace.RoleEditor) ||
		items[0].WorkspaceStatus != string(workspace.StatusActive) || items[0].DisabledAt != nil {
		t.Fatalf("unexpected membership projection: %+v", items)
	}
}

func newAdminQueryRepository(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatalf("create identity repository: %v", err)
	}
	return repository, db
}

func createAdminQueryUser(
	t *testing.T,
	repository *Repository,
	id string,
	username string,
	displayName string,
	status Status,
	role PlatformRole,
) User {
	t.Helper()
	user, err := repository.CreateLocalUser(context.Background(), NewLocalUser{
		ID: id, Username: username, DisplayName: displayName, Status: status, PlatformRole: role,
		PasswordHash: initialPasswordHash, PasswordAlgorithm: "ARGON2ID",
	})
	if err != nil {
		t.Fatalf("create admin query user %s: %v", username, err)
	}
	return user
}
