package authz

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/workspace"
)

var ErrNotVisible = errors.New("workspace scope not visible")

type DenialReason string

const (
	DenialScopeNotVisible   DenialReason = "SCOPE_NOT_VISIBLE"
	DenialUserInactive      DenialReason = "USER_INACTIVE"
	DenialWorkspaceInactive DenialReason = "WORKSPACE_INACTIVE"
	DenialMemberDisabled    DenialReason = "MEMBER_DISABLED"
	DenialRoleInsufficient  DenialReason = "ROLE_INSUFFICIENT"
)

// DenialError contains stable, non-secret fields suitable for a future
// authorization.denied AuditEvent. Cause controls transport mapping without
// exposing whether a cross-Workspace resource exists.
type DenialError struct {
	Reason      DenialReason
	UserID      string
	WorkspaceID string
	Action      Action
	Role        workspace.Role
	cause       error
}

func (e *DenialError) Error() string {
	return fmt.Sprintf("workspace authorization denied: %s", e.Reason)
}

func (e *DenialError) Unwrap() error {
	return e.cause
}

type WorkspaceContext struct {
	WorkspaceID string
	UserID      string
	Role        workspace.Role
	Action      Action
}

type AccessResolver interface {
	ResolveAccess(context.Context, string, string) (workspace.AccessRecord, error)
}

type AuthorizationDenialEvent struct {
	UserID      string
	WorkspaceID string
	Action      Action
	Role        workspace.Role
	Reason      DenialReason
}

type DenialAuditSink interface {
	RecordAuthorizationDenied(context.Context, AuthorizationDenialEvent) error
}

type Service struct {
	resolver AccessResolver
	audit    DenialAuditSink
}

func NewService(resolver AccessResolver, audit ...DenialAuditSink) (*Service, error) {
	if resolver == nil {
		return nil, errors.New("workspace access resolver is required")
	}
	if len(audit) > 1 || (len(audit) == 1 && audit[0] == nil) {
		return nil, errors.New("at most one authorization audit sink is allowed")
	}
	service := &Service{resolver: resolver}
	if len(audit) == 1 {
		service.audit = audit[0]
	}
	return service, nil
}

// AuthorizeWorkspace resolves current state on every call. Roles are not
// embedded in long-lived tokens or cached in this service.
func (s *Service) AuthorizeWorkspace(
	ctx context.Context,
	userID string,
	workspaceID string,
	action Action,
) (WorkspaceContext, error) {
	userID, workspaceID = strings.TrimSpace(userID), strings.TrimSpace(workspaceID)
	record, err := s.resolver.ResolveAccess(ctx, workspaceID, userID)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrInvalid) {
			return WorkspaceContext{}, s.denial(ctx,
				DenialScopeNotVisible,
				ErrNotVisible,
				userID,
				workspaceID,
				action,
				"",
			)
		}
		return WorkspaceContext{}, fmt.Errorf("resolve authorization context: %w", err)
	}
	if record.UserStatus != "ACTIVE" {
		return WorkspaceContext{}, s.denial(ctx,
			DenialUserInactive,
			ErrDenied,
			userID,
			workspaceID,
			action,
			record.Role,
		)
	}
	if record.WorkspaceStatus != workspace.StatusActive {
		return WorkspaceContext{}, s.denial(ctx,
			DenialWorkspaceInactive,
			ErrDenied,
			userID,
			workspaceID,
			action,
			record.Role,
		)
	}
	if record.MemberDisabled {
		return WorkspaceContext{}, s.denial(ctx,
			DenialMemberDisabled,
			ErrDenied,
			userID,
			workspaceID,
			action,
			record.Role,
		)
	}
	if !CanWorkspace(record.Role, action) {
		return WorkspaceContext{}, s.denial(ctx,
			DenialRoleInsufficient,
			ErrDenied,
			userID,
			workspaceID,
			action,
			record.Role,
		)
	}
	return WorkspaceContext{
		WorkspaceID: record.WorkspaceID,
		UserID:      record.UserID,
		Role:        record.Role,
		Action:      action,
	}, nil
}

func (s *Service) denial(
	ctx context.Context,
	reason DenialReason,
	cause error,
	userID string,
	workspaceID string,
	action Action,
	role workspace.Role,
) error {
	denial := newDenial(reason, cause, userID, workspaceID, action, role)
	if s.audit == nil {
		return denial
	}
	if err := s.audit.RecordAuthorizationDenied(ctx, AuthorizationDenialEvent{
		UserID: userID, WorkspaceID: workspaceID, Action: action, Role: role, Reason: reason,
	}); err != nil {
		return errors.Join(denial, fmt.Errorf("record authorization denial audit: %w", err))
	}
	return denial
}

func newDenial(
	reason DenialReason,
	cause error,
	userID string,
	workspaceID string,
	action Action,
	role workspace.Role,
) *DenialError {
	return &DenialError{
		Reason:      reason,
		UserID:      userID,
		WorkspaceID: workspaceID,
		Action:      action,
		Role:        role,
		cause:       cause,
	}
}
