package workspace

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
)

const (
	memberAdminID      = "018f1f2e-7b5a-7c3d-8e9f-9234567890ab"
	memberEditorID     = "018f1f2e-7b5a-7c3d-8e9f-9234567890ac"
	memberSecondOwner  = "018f1f2e-7b5a-7c3d-8e9f-9234567890ad"
	memberWorkspaceTwo = "018f1f2e-7b5a-7c3d-8e9f-9234567890ae"
)

func TestMemberRepositoryManagesLifecycleAndScopedLists(t *testing.T) {
	repository, db := newMemberRepositoryTest(t)
	ctx := context.Background()

	admin, err := repository.AddMember(ctx, NewMember{
		WorkspaceID: repositoryWorkspaceID,
		UserID:      memberAdminID,
		Role:        RoleAdmin,
		InvitedBy:   repositoryOwnerID,
	})
	if err != nil {
		t.Fatalf("add admin member: %v", err)
	}
	if admin.Role != RoleAdmin || admin.DisabledAt != nil {
		t.Fatalf("unexpected added admin: %+v", admin)
	}
	if _, err := repository.AddMember(ctx, NewMember{
		WorkspaceID: repositoryWorkspaceID,
		UserID:      memberEditorID,
		Role:        RoleEditor,
		InvitedBy:   repositoryOwnerID,
	}); err != nil {
		t.Fatalf("add editor member: %v", err)
	}
	if _, err := repository.AddMember(ctx, NewMember{
		WorkspaceID: repositoryWorkspaceID,
		UserID:      memberAdminID,
		Role:        RoleViewer,
		InvitedBy:   repositoryOwnerID,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected duplicate member conflict, got %v", err)
	}
	if _, err := repository.AddMember(ctx, NewMember{
		WorkspaceID: repositoryWorkspaceID,
		UserID:      memberSecondOwner,
		Role:        Role("PRIVATE"),
		InvitedBy:   repositoryOwnerID,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid resource role, got %v", err)
	}

	changed, err := repository.ChangeMemberRole(
		ctx,
		repositoryWorkspaceID,
		memberEditorID,
		RoleOperator,
		repositoryOwnerID,
	)
	if err != nil {
		t.Fatalf("change editor member role: %v", err)
	}
	if changed.Role != RoleOperator {
		t.Fatalf("unexpected changed member: %+v", changed)
	}
	disabled, err := repository.DisableMember(
		ctx,
		repositoryWorkspaceID,
		memberAdminID,
		repositoryOwnerID,
	)
	if err != nil {
		t.Fatalf("disable admin member: %v", err)
	}
	if disabled.DisabledAt == nil {
		t.Fatalf("expected disabled timestamp: %+v", disabled)
	}

	active, err := repository.ListMembers(ctx, repositoryWorkspaceID, false)
	if err != nil {
		t.Fatalf("list active members: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected owner and operator in active list, got %+v", active)
	}
	all, err := repository.ListMembers(ctx, repositoryWorkspaceID, true)
	if err != nil {
		t.Fatalf("list all members: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected disabled admin in full list, got %+v", all)
	}
	if err := repository.RemoveMember(
		ctx,
		repositoryWorkspaceID,
		memberAdminID,
		repositoryOwnerID,
	); err != nil {
		t.Fatalf("remove disabled admin: %v", err)
	}
	assertRowCount(t, db, "workspace_members", "user_id", memberAdminID, 0)
}

func TestMemberRepositorySearchesOnlyActiveUnassignedCandidates(t *testing.T) {
	repository, db := newMemberRepositoryTest(t)
	ctx := context.Background()
	if _, err := repository.AddMember(ctx, NewMember{
		WorkspaceID: repositoryWorkspaceID,
		UserID:      memberAdminID,
		Role:        RoleAdmin,
		InvitedBy:   repositoryOwnerID,
	}); err != nil {
		t.Fatalf("add existing member: %v", err)
	}
	if _, err := db.Exec(`UPDATE users SET status = 'DISABLED' WHERE id = $1`, memberSecondOwner); err != nil {
		t.Fatalf("disable candidate: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE users SET display_name = 'Candidate Editor', platform_role = 'PLATFORM_ADMIN'
		WHERE id = $1
	`, memberEditorID); err != nil {
		t.Fatalf("prepare candidate projection: %v", err)
	}

	candidates, err := repository.SearchMemberCandidates(ctx, repositoryWorkspaceID, "editor", 20)
	if err != nil {
		t.Fatalf("search member candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].UserID != memberEditorID ||
		candidates[0].Username != "workspace.member.editor" ||
		candidates[0].DisplayName != "Candidate Editor" ||
		candidates[0].PlatformRole != "PLATFORM_ADMIN" {
		t.Fatalf("unexpected candidate projection: %+v", candidates)
	}
	all, err := repository.SearchMemberCandidates(ctx, repositoryWorkspaceID, "", 20)
	if err != nil {
		t.Fatalf("list member candidates: %v", err)
	}
	if len(all) != 1 || all[0].UserID != memberEditorID {
		t.Fatalf("expected assigned, disabled, and owner users to be excluded, got %+v", all)
	}
	if _, err := repository.SearchMemberCandidates(ctx, repositoryWorkspaceID, "", 101); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid candidate limit, got %v", err)
	}
}

func TestMemberRepositoryRejectsCrossWorkspaceMutation(t *testing.T) {
	repository, db := newMemberRepositoryTest(t)
	createSecondWorkspace(t, repository)
	if _, err := repository.AddMember(context.Background(), NewMember{
		WorkspaceID: memberWorkspaceTwo,
		UserID:      memberAdminID,
		Role:        RoleEditor,
		InvitedBy:   memberSecondOwner,
	}); err != nil {
		t.Fatalf("add member to second workspace: %v", err)
	}

	if _, err := repository.ChangeMemberRole(
		context.Background(),
		repositoryWorkspaceID,
		memberAdminID,
		RoleViewer,
		repositoryOwnerID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workspace member to be invisible, got %v", err)
	}
	var storedRole Role
	if err := db.QueryRow(`
		SELECT role FROM workspace_members
		WHERE workspace_id = $1 AND user_id = $2
	`, memberWorkspaceTwo, memberAdminID).Scan(&storedRole); err != nil {
		t.Fatalf("read second workspace member: %v", err)
	}
	if storedRole != RoleEditor {
		t.Fatalf("cross-workspace mutation changed role to %q", storedRole)
	}
}

func TestLastOwnerCannotBeDowngradedDisabledOrRemoved(t *testing.T) {
	repository, db := newMemberRepositoryTest(t)
	ctx := context.Background()
	if _, err := repository.ChangeMemberRole(
		ctx,
		repositoryWorkspaceID,
		repositoryOwnerID,
		RoleAdmin,
		repositoryOwnerID,
	); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("expected last owner downgrade rejection, got %v", err)
	}
	if _, err := repository.DisableMember(
		ctx,
		repositoryWorkspaceID,
		repositoryOwnerID,
		repositoryOwnerID,
	); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("expected last owner disable rejection, got %v", err)
	}
	if err := repository.RemoveMember(
		ctx,
		repositoryWorkspaceID,
		repositoryOwnerID,
		repositoryOwnerID,
	); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("expected last owner removal rejection, got %v", err)
	}
	assertActiveOwnerCount(t, db, repositoryWorkspaceID, 1)
}

func TestMemberRepositoryTransfersPrimaryOwner(t *testing.T) {
	repository, db := newMemberRepositoryTest(t)
	if _, err := repository.AddMember(context.Background(), NewMember{
		WorkspaceID: repositoryWorkspaceID,
		UserID:      memberSecondOwner,
		Role:        RoleOwner,
		InvitedBy:   repositoryOwnerID,
	}); err != nil {
		t.Fatalf("add second owner: %v", err)
	}
	if _, err := repository.ChangeMemberRole(
		context.Background(),
		repositoryWorkspaceID,
		repositoryOwnerID,
		RoleEditor,
		memberSecondOwner,
	); err != nil {
		t.Fatalf("downgrade primary owner: %v", err)
	}
	var primaryOwnerID string
	if err := db.QueryRow(`
		SELECT owner_user_id FROM workspaces WHERE id = $1
	`, repositoryWorkspaceID).Scan(&primaryOwnerID); err != nil {
		t.Fatalf("read transferred workspace owner: %v", err)
	}
	if primaryOwnerID != memberSecondOwner {
		t.Fatalf("expected primary owner %s, got %s", memberSecondOwner, primaryOwnerID)
	}
	assertActiveOwnerCount(t, db, repositoryWorkspaceID, 1)
}

func TestLastOwnerConcurrentDowngradesLeaveOneOwner(t *testing.T) {
	repository, db := newMemberRepositoryTest(t)
	if _, err := repository.AddMember(context.Background(), NewMember{
		WorkspaceID: repositoryWorkspaceID,
		UserID:      memberSecondOwner,
		Role:        RoleOwner,
		InvitedBy:   repositoryOwnerID,
	}); err != nil {
		t.Fatalf("add concurrent second owner: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, userID := range []string{repositoryOwnerID, memberSecondOwner} {
		workers.Add(1)
		go func(userID string) {
			defer workers.Done()
			<-start
			_, err := repository.ChangeMemberRole(
				context.Background(),
				repositoryWorkspaceID,
				userID,
				RoleEditor,
				memberSecondOwner,
			)
			results <- err
		}(userID)
	}
	close(start)
	workers.Wait()
	close(results)

	var successCount int
	var lastOwnerCount int
	for err := range results {
		switch {
		case err == nil:
			successCount++
		case errors.Is(err, ErrLastOwner):
			lastOwnerCount++
		default:
			t.Fatalf("unexpected concurrent owner result: %v", err)
		}
	}
	if successCount != 1 || lastOwnerCount != 1 {
		t.Fatalf("expected one success and one last-owner rejection, got success=%d rejection=%d", successCount, lastOwnerCount)
	}
	assertActiveOwnerCount(t, db, repositoryWorkspaceID, 1)
}

func newMemberRepositoryTest(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	db := newWorkspaceRepositoryDatabase(t)
	insertMemberUsers(t, db)
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatalf("create member repository: %v", err)
	}
	if _, err := repository.Create(
		context.Background(),
		validNewWorkspace(repositoryWorkspaceID, "member-workspace"),
	); err != nil {
		t.Fatalf("create member test workspace: %v", err)
	}
	return repository, db
}

func insertMemberUsers(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, user := range []struct {
		id       string
		username string
	}{
		{id: memberAdminID, username: "workspace.member.admin"},
		{id: memberEditorID, username: "workspace.member.editor"},
		{id: memberSecondOwner, username: "workspace.member.second-owner"},
	} {
		if _, err := db.Exec(`
			INSERT INTO users (id, username, display_name)
			VALUES ($1, $2, 'Workspace Member')
		`, user.id, user.username); err != nil {
			t.Fatalf("insert member user %s: %v", user.username, err)
		}
	}
}

func createSecondWorkspace(t *testing.T, repository *Repository) {
	t.Helper()
	if _, err := repository.Create(context.Background(), NewWorkspace{
		ID:          memberWorkspaceTwo,
		Slug:        "member-workspace-two",
		DisplayName: "Member Workspace Two",
		OwnerUserID: memberSecondOwner,
		CreatedBy:   memberSecondOwner,
	}); err != nil {
		t.Fatalf("create second member workspace: %v", err)
	}
}

func assertActiveOwnerCount(t *testing.T, db *sql.DB, workspaceID string, expected int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM workspace_members
		WHERE workspace_id = $1 AND role = 'OWNER' AND disabled_at IS NULL
	`, workspaceID).Scan(&count); err != nil {
		t.Fatalf("count active workspace owners: %v", err)
	}
	if count != expected {
		t.Fatalf("expected %d active owners, got %d", expected, count)
	}
}
