package authn

import (
	"context"
	"errors"
	"strings"

	"actweave/backend/internal/identity"
)

const (
	ActionUserCreated        = "identity.user.created"
	ActionUserProfileChanged = "identity.user.profile.changed"
	ActionUserStatusChanged  = "identity.user.status.changed"
	ActionUserRoleChanged    = "identity.user.role.changed"
	ActionUserPasswordReset  = "identity.user.password.reset"
	ActionUserUnlocked       = "identity.user.unlocked"
)

func (s *Service) AdminCreateUser(
	ctx context.Context,
	actorUserID string,
	request CreateUserRequest,
) (identity.User, error) {
	created, err := s.CreateUser(ctx, request)
	if err != nil {
		return identity.User{}, err
	}
	if err := s.recordIdentityManagement(ctx, IdentityManagementAuditEvent{
		Action: ActionUserCreated, ActorUserID: actorUserID, TargetUserID: created.ID,
		After: userSecurityAuditState(created), Metadata: map[string]any{"username": created.Username},
	}); err != nil {
		return created, err
	}
	return created, nil
}

func (s *Service) AdminUpdateUserProfile(
	ctx context.Context,
	actorUserID string,
	targetUserID string,
	input identity.UserProfileUpdate,
) (identity.User, error) {
	before, err := s.GetUser(ctx, targetUserID)
	if err != nil {
		return identity.User{}, err
	}
	updated, err := s.UpdateUserProfile(ctx, targetUserID, input)
	if err != nil {
		return identity.User{}, err
	}
	if err := s.recordIdentityManagement(ctx, IdentityManagementAuditEvent{
		Action: ActionUserProfileChanged, ActorUserID: actorUserID, TargetUserID: targetUserID,
		Before: userProfileAuditState(before), After: userProfileAuditState(updated),
	}); err != nil {
		return updated, err
	}
	return updated, nil
}

func (s *Service) AdminSetUserStatus(
	ctx context.Context,
	actorUserID string,
	targetUserID string,
	status identity.Status,
	expectedLockVersion int64,
) (identity.User, error) {
	before, err := s.GetUser(ctx, targetUserID)
	if err != nil {
		return identity.User{}, err
	}
	updated, err := s.SetUserStatus(ctx, targetUserID, status, expectedLockVersion)
	if err != nil {
		return identity.User{}, err
	}
	if err := s.recordIdentityManagement(ctx, IdentityManagementAuditEvent{
		Action: ActionUserStatusChanged, ActorUserID: actorUserID, TargetUserID: targetUserID,
		Before: userSecurityAuditState(before), After: userSecurityAuditState(updated),
	}); err != nil {
		return updated, err
	}
	return updated, nil
}

func (s *Service) AdminSetPlatformRole(
	ctx context.Context,
	actorUserID string,
	targetUserID string,
	role identity.PlatformRole,
	expectedLockVersion int64,
) (identity.User, error) {
	before, err := s.GetUser(ctx, targetUserID)
	if err != nil {
		return identity.User{}, err
	}
	updated, err := s.SetPlatformRole(ctx, targetUserID, role, expectedLockVersion)
	if err != nil {
		return identity.User{}, err
	}
	if err := s.recordIdentityManagement(ctx, IdentityManagementAuditEvent{
		Action: ActionUserRoleChanged, ActorUserID: actorUserID, TargetUserID: targetUserID,
		Before: userSecurityAuditState(before), After: userSecurityAuditState(updated),
	}); err != nil {
		return updated, err
	}
	return updated, nil
}

func (s *Service) AdminResetPassword(
	ctx context.Context,
	actorUserID string,
	targetUserID string,
	temporaryPassword string,
) error {
	if err := s.ResetPassword(ctx, targetUserID, temporaryPassword); err != nil {
		return err
	}
	return s.recordIdentityManagement(ctx, IdentityManagementAuditEvent{
		Action: ActionUserPasswordReset, ActorUserID: actorUserID, TargetUserID: targetUserID,
		Metadata: map[string]any{"credentialRotated": true, "sessionsRevoked": true},
	})
}

func (s *Service) AdminUnlockUser(
	ctx context.Context,
	actorUserID string,
	targetUserID string,
	expectedLockVersion int64,
) (identity.User, error) {
	before, err := s.GetUser(ctx, targetUserID)
	if err != nil {
		return identity.User{}, err
	}
	updated, err := s.UnlockUser(ctx, targetUserID, expectedLockVersion)
	if err != nil {
		return identity.User{}, err
	}
	if err := s.recordIdentityManagement(ctx, IdentityManagementAuditEvent{
		Action: ActionUserUnlocked, ActorUserID: actorUserID, TargetUserID: targetUserID,
		Before: userSecurityAuditState(before), After: userSecurityAuditState(updated),
		Metadata: map[string]any{"credentialLockoutCleared": true},
	}); err != nil {
		return updated, err
	}
	return updated, nil
}

func (s *Service) recordIdentityManagement(ctx context.Context, event IdentityManagementAuditEvent) error {
	if s.managementAudit == nil {
		return nil
	}
	event.ActorUserID = strings.TrimSpace(event.ActorUserID)
	event.TargetUserID = strings.TrimSpace(event.TargetUserID)
	if event.ActorUserID == "" || event.TargetUserID == "" {
		return identity.ErrInvalid
	}
	if err := s.managementAudit.RecordIdentityManagement(ctx, event); err != nil {
		return errors.Join(errors.New("record identity management audit"), err)
	}
	return nil
}

func userSecurityAuditState(user identity.User) map[string]any {
	return map[string]any{
		"status": string(user.Status), "platformRole": string(user.PlatformRole),
		"lockVersion": user.LockVersion,
	}
}

func userProfileAuditState(user identity.User) map[string]any {
	return map[string]any{
		"displayName": user.DisplayName, "email": user.Email, "avatarUrl": user.AvatarURL,
		"locale": user.Locale, "timezone": user.Timezone, "lockVersion": user.LockVersion,
	}
}
