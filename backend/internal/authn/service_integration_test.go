package authn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/identity"
)

const (
	authServiceUserID   = "018f1f2e-7b5a-7c3d-8e9f-6234567890ab"
	authManagedUserID   = "018f1f2e-7b5a-7c3d-8e9f-6234567890ac"
	authServiceUsername = "auth.service.user"
	authServicePassword = "initial password with enough entropy"
)

type authServiceFixture struct {
	service    *Service
	repository *identity.Repository
	passwords  *PasswordManager
	access     *AccessTokenManager
	now        time.Time
}

func TestAuthServiceLoginRefreshAndLogout(t *testing.T) {
	fixture := newAuthServiceFixture(t)
	ctx := context.Background()
	userAgent := "auth service integration test"
	ip := "127.0.0.1"
	login, err := fixture.service.Login(ctx, LoginRequest{
		Username:  strings.ToUpper(authServiceUsername),
		Password:  authServicePassword,
		UserAgent: &userAgent,
		IP:        &ip,
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if login.User.ID != authServiceUserID || login.AccessToken == "" || login.RefreshToken == "" ||
		login.SessionID == "" || login.MustChangePassword {
		t.Fatalf("unexpected login result: %+v", login)
	}
	claims, err := fixture.access.Parse(login.AccessToken, fixture.now)
	if err != nil {
		t.Fatalf("parse login access token: %v", err)
	}
	if claims.Subject != authServiceUserID || claims.SessionID != login.SessionID {
		t.Fatalf("unexpected access claims: %+v", claims)
	}

	fixture.advance(time.Minute)
	refreshed, err := fixture.service.Refresh(ctx, login.RefreshToken)
	if err != nil {
		t.Fatalf("refresh login: %v", err)
	}
	if refreshed.SessionID != login.SessionID || refreshed.RefreshToken == login.RefreshToken ||
		refreshed.AccessToken == "" {
		t.Fatalf("unexpected refresh result: login=%+v refreshed=%+v", login, refreshed)
	}
	if _, err := fixture.service.Refresh(ctx, login.RefreshToken); !errors.Is(err, ErrRefreshRejected) {
		t.Fatalf("expected rotated refresh token rejection, got %v", err)
	}

	fixture.advance(time.Minute)
	if err := fixture.service.Logout(ctx, refreshed.RefreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if err := fixture.service.Logout(ctx, refreshed.RefreshToken); err != nil {
		t.Fatalf("idempotent logout: %v", err)
	}
	if _, err := fixture.service.Refresh(ctx, refreshed.RefreshToken); !errors.Is(err, ErrRefreshRejected) {
		t.Fatalf("expected logged-out refresh rejection, got %v", err)
	}
}

func TestLoginEmitsRedactedSuccessAndFailureAuditEvents(t *testing.T) {
	fixture := newAuthServiceFixture(t)
	capture := &authenticationAuditCapture{}
	fixture.service.audit = capture
	ip, userAgent := "203.0.113.44", "authn-audit-test"
	if _, err := fixture.service.Login(context.Background(), LoginRequest{
		Username: authServiceUsername, Password: "wrong-password", IP: &ip, UserAgent: &userAgent,
	}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("failed login error = %v", err)
	}
	if _, err := fixture.service.Login(context.Background(), LoginRequest{
		Username: authServiceUsername, Password: authServicePassword, IP: &ip, UserAgent: &userAgent,
	}); err != nil {
		t.Fatalf("successful login: %v", err)
	}
	if len(capture.events) != 2 || capture.events[0].Result != "FAILURE" ||
		capture.events[0].ErrorCode != "INVALID_CREDENTIALS" || capture.events[0].UserID != "" ||
		capture.events[1].Result != "SUCCESS" || capture.events[1].UserID != authServiceUserID {
		t.Fatalf("authentication audit events = %+v", capture.events)
	}
	for _, event := range capture.events {
		if event.SubjectHash == "" || strings.Contains(event.SubjectHash, authServiceUsername) ||
			event.SourceIP != ip || event.UserAgent != userAgent {
			t.Fatalf("unsafe authentication audit event = %+v", event)
		}
	}
}

type authenticationAuditCapture struct{ events []AuthenticationAuditEvent }

func (capture *authenticationAuditCapture) RecordAuthentication(
	_ context.Context,
	event AuthenticationAuditEvent,
) error {
	capture.events = append(capture.events, event)
	return nil
}

func TestAdminUserCommandsEmitAllowlistedManagementAuditEvents(t *testing.T) {
	fixture := newAuthServiceFixture(t)
	capture := &identityManagementAuditCapture{}
	fixture.service.managementAudit = capture
	ctx := context.Background()
	actor, err := fixture.repository.GetUserByID(ctx, authServiceUserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.repository.UpdatePlatformRoleAndRevokeSessions(
		ctx, actor.ID, identity.PlatformRoleAdmin, actor.LockVersion, fixture.now,
	); err != nil {
		t.Fatalf("promote management actor fixture: %v", err)
	}
	const temporaryPassword = "managed temporary password 1"
	created, err := fixture.service.AdminCreateUser(ctx, authServiceUserID, CreateUserRequest{
		ID: authManagedUserID, Username: "managed.user", DisplayName: "Managed User",
		Password: temporaryPassword, Status: identity.StatusActive, PlatformRole: identity.PlatformRoleUser,
		Locale: "zh-CN", Timezone: "Asia/Singapore", MustChangePassword: true,
	})
	if err != nil {
		t.Fatalf("admin create user: %v", err)
	}
	displayName := "Managed User Updated"
	updated, err := fixture.service.AdminUpdateUserProfile(ctx, authServiceUserID, created.ID, identity.UserProfileUpdate{
		DisplayName: &displayName, ExpectedLockVersion: created.LockVersion,
	})
	if err != nil {
		t.Fatalf("admin update profile: %v", err)
	}
	promoted, err := fixture.service.AdminSetPlatformRole(
		ctx, authServiceUserID, updated.ID, identity.PlatformRoleAdmin, updated.LockVersion,
	)
	if err != nil {
		t.Fatalf("admin promote user: %v", err)
	}
	locked, err := fixture.service.AdminSetUserStatus(
		ctx, authServiceUserID, promoted.ID, identity.StatusLocked, promoted.LockVersion,
	)
	if err != nil {
		t.Fatalf("admin lock user: %v", err)
	}
	unlocked, err := fixture.service.AdminUnlockUser(ctx, authServiceUserID, locked.ID, locked.LockVersion)
	if err != nil {
		t.Fatalf("admin unlock user: %v", err)
	}
	if err := fixture.service.AdminResetPassword(ctx, authServiceUserID, unlocked.ID, "replacement managed password 2"); err != nil {
		t.Fatalf("admin reset password: %v", err)
	}
	wantActions := []string{
		ActionUserCreated, ActionUserProfileChanged, ActionUserRoleChanged,
		ActionUserStatusChanged, ActionUserUnlocked, ActionUserPasswordReset,
	}
	if len(capture.events) != len(wantActions) {
		t.Fatalf("management audit count=%d events=%+v", len(capture.events), capture.events)
	}
	for index, event := range capture.events {
		if event.Action != wantActions[index] || event.ActorUserID != authServiceUserID || event.TargetUserID != authManagedUserID {
			t.Fatalf("management audit[%d]=%+v", index, event)
		}
		if strings.Contains(fmt.Sprint(event), temporaryPassword) || strings.Contains(strings.ToLower(fmt.Sprint(event)), "passwordhash") {
			t.Fatalf("management audit leaked credentials: %+v", event)
		}
	}
}

type identityManagementAuditCapture struct {
	events []IdentityManagementAuditEvent
}

func (capture *identityManagementAuditCapture) RecordIdentityManagement(
	_ context.Context,
	event IdentityManagementAuditEvent,
) error {
	capture.events = append(capture.events, event)
	return nil
}

func TestAuthServiceWrongPasswordLockoutAndUnlock(t *testing.T) {
	fixture := newAuthServiceFixture(t)
	ctx := context.Background()
	for attempt := 1; attempt <= 3; attempt++ {
		if _, err := fixture.service.Login(ctx, LoginRequest{
			Username: authServiceUsername,
			Password: "wrong password",
		}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("expected invalid credentials on attempt %d, got %v", attempt, err)
		}
	}
	if _, err := fixture.service.Login(ctx, LoginRequest{
		Username: authServiceUsername,
		Password: authServicePassword,
	}); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("expected locked account after threshold, got %v", err)
	}
	credential, err := fixture.repository.GetPasswordCredential(ctx, authServiceUserID)
	if err != nil || credential.FailedAttempts != 3 || credential.LockedUntil == nil {
		t.Fatalf("unexpected persisted lockout: credential=%+v err=%v", credential, err)
	}
	user, err := fixture.repository.GetUserByID(ctx, authServiceUserID)
	if err != nil {
		t.Fatalf("get lockout user: %v", err)
	}
	unlocked, err := fixture.service.UnlockUser(ctx, user.ID, user.LockVersion)
	if err != nil {
		t.Fatalf("administrator unlock: %v", err)
	}
	if unlocked.Status != identity.StatusActive || unlocked.LockVersion != user.LockVersion+1 {
		t.Fatalf("unexpected unlocked user: %+v", unlocked)
	}
	if _, err := fixture.service.Login(ctx, LoginRequest{
		Username: authServiceUsername,
		Password: authServicePassword,
	}); err != nil {
		t.Fatalf("login after unlock: %v", err)
	}
}

func TestAuthServicePasswordChangeRevokesAllSessions(t *testing.T) {
	fixture := newAuthServiceFixture(t)
	ctx := context.Background()
	first := fixture.login(t, authServicePassword)
	second := fixture.login(t, authServicePassword)
	fixture.advance(time.Minute)
	const replacementPassword = "replacement password with enough entropy"
	if err := fixture.service.ChangePassword(
		ctx,
		authServiceUserID,
		authServicePassword,
		replacementPassword,
	); err != nil {
		t.Fatalf("change password: %v", err)
	}
	for _, refreshToken := range []string{first.RefreshToken, second.RefreshToken} {
		if _, err := fixture.service.Refresh(ctx, refreshToken); !errors.Is(err, ErrRefreshRejected) {
			t.Fatalf("expected password change to revoke session, got %v", err)
		}
	}
	if _, err := fixture.service.Login(ctx, LoginRequest{
		Username: authServiceUsername,
		Password: authServicePassword,
	}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected old password rejection, got %v", err)
	}
	if _, err := fixture.service.Login(ctx, LoginRequest{
		Username: authServiceUsername,
		Password: replacementPassword,
	}); err != nil {
		t.Fatalf("login with replacement password: %v", err)
	}
}

func TestAuthServiceResetDisableAndPermanentUnlock(t *testing.T) {
	fixture := newAuthServiceFixture(t)
	ctx := context.Background()
	beforeReset := fixture.login(t, authServicePassword)
	fixture.advance(time.Minute)
	const temporaryPassword = "temporary password with enough entropy"
	if err := fixture.service.ResetPassword(ctx, authServiceUserID, temporaryPassword); err != nil {
		t.Fatalf("reset password: %v", err)
	}
	if _, err := fixture.service.Refresh(ctx, beforeReset.RefreshToken); !errors.Is(err, ErrRefreshRejected) {
		t.Fatalf("expected password reset to revoke session, got %v", err)
	}
	temporaryLogin := fixture.login(t, temporaryPassword)
	if !temporaryLogin.MustChangePassword {
		t.Fatal("temporary password login must require password change")
	}

	user, err := fixture.repository.GetUserByID(ctx, authServiceUserID)
	if err != nil {
		t.Fatalf("get user before disable: %v", err)
	}
	disabled, err := fixture.service.SetUserStatus(ctx, user.ID, identity.StatusDisabled, user.LockVersion)
	if err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if _, err := fixture.service.Refresh(ctx, temporaryLogin.RefreshToken); !errors.Is(err, ErrRefreshRejected) {
		t.Fatalf("expected disable to revoke session, got %v", err)
	}
	if _, err := fixture.service.Login(ctx, LoginRequest{
		Username: authServiceUsername,
		Password: temporaryPassword,
	}); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("expected disabled login rejection, got %v", err)
	}
	if _, err := fixture.service.UnlockUser(ctx, disabled.ID, disabled.LockVersion); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("expected disabled user unlock rejection, got %v", err)
	}

	fixture.advance(time.Minute)
	enabled, err := fixture.service.SetUserStatus(ctx, disabled.ID, identity.StatusActive, disabled.LockVersion)
	if err != nil {
		t.Fatalf("enable user: %v", err)
	}
	locked, err := fixture.service.SetUserStatus(ctx, enabled.ID, identity.StatusLocked, enabled.LockVersion)
	if err != nil {
		t.Fatalf("permanently lock user: %v", err)
	}
	if _, err := fixture.service.Login(ctx, LoginRequest{
		Username: authServiceUsername,
		Password: temporaryPassword,
	}); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("expected permanent lock rejection, got %v", err)
	}
	unlocked, err := fixture.service.UnlockUser(ctx, locked.ID, locked.LockVersion)
	if err != nil || unlocked.Status != identity.StatusActive {
		t.Fatalf("unlock permanent account status: user=%+v err=%v", unlocked, err)
	}
}

func TestAuthServiceUnknownUserUsesGenericCredentialError(t *testing.T) {
	fixture := newAuthServiceFixture(t)
	if _, err := fixture.service.Login(context.Background(), LoginRequest{
		Username: "missing.user",
		Password: "irrelevant password",
	}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected generic missing-user credential error, got %v", err)
	}
}

func newAuthServiceFixture(t *testing.T) *authServiceFixture {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateTo(t, 4)
	db := testDatabase.Open(t)
	repository, err := identity.NewRepository(db)
	if err != nil {
		t.Fatalf("create identity repository: %v", err)
	}
	passwords, err := NewPasswordManager(DefaultArgon2idParams())
	if err != nil {
		t.Fatalf("create password manager: %v", err)
	}
	hash, err := passwords.Hash(authServicePassword)
	if err != nil {
		t.Fatalf("hash fixture password: %v", err)
	}
	if _, err := repository.CreateLocalUser(context.Background(), identity.NewLocalUser{
		ID:                authServiceUserID,
		Username:          authServiceUsername,
		DisplayName:       "Auth Service User",
		PlatformRole:      identity.PlatformRoleUser,
		PasswordHash:      hash,
		PasswordAlgorithm: PasswordAlgorithmArgon2id,
	}); err != nil {
		t.Fatalf("create authentication fixture user: %v", err)
	}
	access, err := NewAccessTokenManager(strings.Repeat("a", 32), "actweave-auth-test", 0)
	if err != nil {
		t.Fatalf("create access token manager: %v", err)
	}
	refresh := NewRefreshTokenManager()
	fixture := &authServiceFixture{
		repository: repository,
		passwords:  passwords,
		access:     access,
		now:        time.Now().UTC().Add(2 * time.Second).Truncate(time.Microsecond),
	}
	service, err := newService(
		repository,
		passwords,
		access,
		refresh,
		ServiceConfig{
			RefreshTTL: 7 * 24 * time.Hour,
			LockoutPolicy: LockoutPolicy{
				MaxFailedAttempts: 3,
				Duration:          15 * time.Minute,
			},
		},
		func() time.Time { return fixture.now },
	)
	if err != nil {
		t.Fatalf("create authentication service: %v", err)
	}
	fixture.service = service
	return fixture
}

func (f *authServiceFixture) advance(duration time.Duration) {
	f.now = f.now.Add(duration)
}

func (f *authServiceFixture) login(t *testing.T, password string) AuthResult {
	t.Helper()
	result, err := f.service.Login(context.Background(), LoginRequest{
		Username: authServiceUsername,
		Password: password,
	})
	if err != nil {
		t.Fatalf("fixture login: %v", err)
	}
	return result
}
