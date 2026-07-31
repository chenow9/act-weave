package authz

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/workspace"
)

const (
	authzUserID        = "018f1f2e-7b5a-7c3d-8e9f-a234567890ab"
	authzOtherUserID   = "018f1f2e-7b5a-7c3d-8e9f-a234567890ac"
	authzWorkspaceID   = "018f1f2e-7b5a-7c3d-8e9f-a234567890ad"
	authzOtherSpaceID  = "018f1f2e-7b5a-7c3d-8e9f-a234567890ae"
	authzUnknownUserID = "018f1f2e-7b5a-7c3d-8e9f-a234567890af"
	authzOwnerID       = "018f1f2e-7b5a-7c3d-8e9f-a234567890b0"
)

func TestAuthorizationContextUsesCurrentDatabaseState(t *testing.T) {
	service, db := newAuthorizationTest(t)
	ctx := context.Background()

	allowed, err := service.AuthorizeWorkspace(ctx, authzUserID, authzWorkspaceID, ActionPublish)
	if err != nil {
		t.Fatalf("authorize active editor publish: %v", err)
	}
	if allowed.WorkspaceID != authzWorkspaceID || allowed.UserID != authzUserID ||
		allowed.Role != workspace.RoleEditor || allowed.Action != ActionPublish {
		t.Fatalf("unexpected authorization context: %+v", allowed)
	}

	if _, err := db.Exec(`
		UPDATE workspace_members
		SET disabled_at = clock_timestamp()
		WHERE workspace_id = $1 AND user_id = $2
	`, authzWorkspaceID, authzUserID); err != nil {
		t.Fatalf("disable authorization member: %v", err)
	}
	assertAuthorizationDenial(
		t,
		service,
		authzUserID,
		authzWorkspaceID,
		ActionView,
		DenialMemberDisabled,
		ErrDenied,
	)
	if _, err := db.Exec(`
		UPDATE workspace_members
		SET disabled_at = NULL
		WHERE workspace_id = $1 AND user_id = $2
	`, authzWorkspaceID, authzUserID); err != nil {
		t.Fatalf("enable authorization member: %v", err)
	}

	if _, err := db.Exec(`UPDATE workspaces SET status = 'DISABLED' WHERE id = $1`, authzWorkspaceID); err != nil {
		t.Fatalf("disable authorization workspace: %v", err)
	}
	assertAuthorizationDenial(
		t,
		service,
		authzUserID,
		authzWorkspaceID,
		ActionView,
		DenialWorkspaceInactive,
		ErrDenied,
	)
	if _, err := db.Exec(`UPDATE workspaces SET status = 'ACTIVE' WHERE id = $1`, authzWorkspaceID); err != nil {
		t.Fatalf("enable authorization workspace: %v", err)
	}

	if _, err := db.Exec(`UPDATE users SET status = 'DISABLED' WHERE id = $1`, authzUserID); err != nil {
		t.Fatalf("disable authorization user: %v", err)
	}
	assertAuthorizationDenial(
		t,
		service,
		authzUserID,
		authzWorkspaceID,
		ActionView,
		DenialUserInactive,
		ErrDenied,
	)
	if _, err := db.Exec(`UPDATE users SET status = 'ACTIVE' WHERE id = $1`, authzUserID); err != nil {
		t.Fatalf("enable authorization user: %v", err)
	}

	if _, err := service.AuthorizeWorkspace(ctx, authzUserID, authzWorkspaceID, ActionPublish); err != nil {
		t.Fatalf("authorization did not observe re-enabled database state: %v", err)
	}
}

func TestAuthorizationDenialContainsAuditableReason(t *testing.T) {
	service, _ := newAuthorizationTest(t)
	err := authorizationError(service, authzUserID, authzWorkspaceID, ActionDelete)
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected role denial, got %v", err)
	}
	var denial *DenialError
	if !errors.As(err, &denial) {
		t.Fatalf("expected structured denial error, got %T", err)
	}
	if denial.Reason != DenialRoleInsufficient || denial.UserID != authzUserID ||
		denial.WorkspaceID != authzWorkspaceID || denial.Action != ActionDelete ||
		denial.Role != workspace.RoleEditor {
		t.Fatalf("unexpected auditable denial fields: %+v", denial)
	}
}

func TestAuthorizationDenialInvokesAuditSink(t *testing.T) {
	service, _ := newAuthorizationTest(t)
	capture := &denialAuditCapture{}
	service, err := NewService(service.resolver, capture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthorizeWorkspace(
		context.Background(), authzUserID, authzWorkspaceID, ActionDelete,
	); !errors.Is(err, ErrDenied) {
		t.Fatalf("authorization denial = %v", err)
	}
	if len(capture.events) != 1 || capture.events[0].Reason != DenialRoleInsufficient ||
		capture.events[0].Action != ActionDelete || capture.events[0].UserID != authzUserID {
		t.Fatalf("denial audit events = %+v", capture.events)
	}
	if _, err := service.AuthorizeWorkspace(
		context.Background(), authzUserID, authzWorkspaceID, ActionView,
	); err != nil {
		t.Fatal(err)
	}
	if len(capture.events) != 1 {
		t.Fatalf("allowed authorization emitted denial audit: %+v", capture.events)
	}
}

type denialAuditCapture struct{ events []AuthorizationDenialEvent }

func (capture *denialAuditCapture) RecordAuthorizationDenied(
	_ context.Context,
	event AuthorizationDenialEvent,
) error {
	capture.events = append(capture.events, event)
	return nil
}

func TestScopeGuessingIsUniformlyNotVisible(t *testing.T) {
	service, db := newAuthorizationTest(t)
	for name, userID := range map[string]string{
		"member of another workspace": authzOtherUserID,
		"unknown user":                authzUnknownUserID,
	} {
		t.Run(name, func(t *testing.T) {
			assertAuthorizationDenial(
				t,
				service,
				userID,
				authzWorkspaceID,
				ActionView,
				DenialScopeNotVisible,
				ErrNotVisible,
			)
		})
	}
	if _, err := db.Exec(`
		UPDATE workspaces SET deleted_at = clock_timestamp() WHERE id = $1
	`, authzWorkspaceID); err != nil {
		t.Fatalf("soft delete authorization workspace: %v", err)
	}
	assertAuthorizationDenial(
		t,
		service,
		authzUserID,
		authzWorkspaceID,
		ActionView,
		DenialScopeNotVisible,
		ErrNotVisible,
	)
}

func TestScopeResolutionAlwaysIncludesWorkspaceID(t *testing.T) {
	service, _ := newAuthorizationTest(t)
	first, err := service.AuthorizeWorkspace(
		context.Background(),
		authzUserID,
		authzWorkspaceID,
		ActionPublish,
	)
	if err != nil {
		t.Fatalf("authorize first workspace: %v", err)
	}
	second, err := service.AuthorizeWorkspace(
		context.Background(),
		authzUserID,
		authzOtherSpaceID,
		ActionView,
	)
	if err != nil {
		t.Fatalf("authorize second workspace: %v", err)
	}
	if first.Role != workspace.RoleEditor || second.Role != workspace.RoleViewer {
		t.Fatalf("workspace-scoped roles leaked: first=%+v second=%+v", first, second)
	}
	assertAuthorizationDenial(
		t,
		service,
		authzUserID,
		authzOtherSpaceID,
		ActionExecute,
		DenialRoleInsufficient,
		ErrDenied,
	)
}

func newAuthorizationTest(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("expected clean authorization migration version 5, got %+v", version)
	}
	db := testDatabase.Open(t)
	if _, err := db.Exec(`
		INSERT INTO users (id, username, display_name)
		VALUES
			($1, 'authz.editor', 'Authorization Editor'),
			($2, 'authz.other', 'Authorization Other'),
			($3, 'authz.owner', 'Authorization Owner')
	`, authzUserID, authzOtherUserID, authzOwnerID); err != nil {
		t.Fatalf("insert authorization users: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (
			id, slug, display_name, mode, owner_user_id, created_by, updated_by
		) VALUES
			($1, 'authz-workspace', 'Authorization Workspace', 'PRODUCTION', $2, $2, $2),
			($3, 'authz-other', 'Authorization Other', 'SANDBOX', $4, $4, $4)
	`, authzWorkspaceID, authzOwnerID, authzOtherSpaceID, authzOtherUserID); err != nil {
		t.Fatalf("insert authorization workspaces: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_members (workspace_id, user_id, role, invited_by)
		VALUES
			($1, $5, 'OWNER', $5),
			($1, $2, 'EDITOR', $5),
			($3, $4, 'OWNER', $4),
			($3, $2, 'VIEWER', $4)
	`, authzWorkspaceID, authzUserID, authzOtherSpaceID, authzOtherUserID, authzOwnerID); err != nil {
		t.Fatalf("insert authorization members: %v", err)
	}
	repository, err := workspace.NewRepository(db)
	if err != nil {
		t.Fatalf("create authorization workspace repository: %v", err)
	}
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("create authorization service: %v", err)
	}
	return service, db
}

func assertAuthorizationDenial(
	t *testing.T,
	service *Service,
	userID string,
	workspaceID string,
	action Action,
	reason DenialReason,
	cause error,
) {
	t.Helper()
	err := authorizationError(service, userID, workspaceID, action)
	if !errors.Is(err, cause) {
		t.Fatalf("authorization error %v does not match %v", err, cause)
	}
	var denial *DenialError
	if !errors.As(err, &denial) {
		t.Fatalf("expected DenialError, got %T", err)
	}
	if denial.Reason != reason || denial.UserID != userID ||
		denial.WorkspaceID != workspaceID || denial.Action != action {
		t.Fatalf("unexpected denial details: %+v", denial)
	}
}

func authorizationError(
	service *Service,
	userID string,
	workspaceID string,
	action Action,
) error {
	_, err := service.AuthorizeWorkspace(context.Background(), userID, workspaceID, action)
	if err == nil {
		return errors.New("expected authorization denial")
	}
	return err
}
