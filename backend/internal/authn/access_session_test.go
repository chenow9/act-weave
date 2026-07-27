package authn

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"actweave/backend/internal/identity"
)

const (
	accessAuthUserID    = "018f1f2e-7b5a-7c3d-8e9f-7234567890ab"
	accessAuthSessionID = "018f1f2e-7b5a-7c3d-8e9f-7234567890ac"
	accessAuthSecret    = "access-session-authn-test-secret-32b!"
)

func TestAuthenticateAccessTokenValidUsesDatabaseIdentity(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	token := mustAccessToken(t, now, "jwt.stale.user", identity.PlatformRoleAdmin)

	stub := &accessSessionRepoStub{
		state: identity.AccessSessionState{
			SessionID:          accessAuthSessionID,
			UserID:             accessAuthUserID,
			SessionExpiresAt:   now.Add(24 * time.Hour),
			Username:           "db.current.user",
			UserStatus:         identity.StatusActive,
			PlatformRole:       identity.PlatformRoleUser,
			MustChangePassword: true,
		},
	}
	service := newAccessSessionService(t, stub, now)

	identityResult, err := service.AuthenticateAccessToken(context.Background(), token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("expected exactly one ResolveAccessSessionState call, got %d", stub.calls)
	}
	if stub.lastSubject != accessAuthUserID || stub.lastSessionID != accessAuthSessionID {
		t.Fatalf("resolver args subject=%q session=%q", stub.lastSubject, stub.lastSessionID)
	}
	if identityResult.UserID != accessAuthUserID ||
		identityResult.SessionID != accessAuthSessionID ||
		identityResult.Username != "db.current.user" ||
		identityResult.PlatformRole != identity.PlatformRoleUser ||
		!identityResult.MustChangePassword {
		t.Fatalf("identity must come from DB, got %+v", identityResult)
	}
	// JWT claimed ADMIN/stale username must not authorize.
	if identityResult.Username == "jwt.stale.user" ||
		identityResult.PlatformRole == identity.PlatformRoleAdmin {
		t.Fatalf("JWT claims leaked into identity: %+v", identityResult)
	}
	if !identityResult.TokenExpiresAt.Equal(now.Add(DefaultAccessTokenTTL)) {
		t.Fatalf("token expiry=%v", identityResult.TokenExpiresAt)
	}
}

func TestAuthenticateAccessTokenInvalidStatesAreUnified(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	token := mustAccessToken(t, now, "access.user", identity.PlatformRoleUser)
	revoked := now.Add(-time.Minute)
	futureLock := now.Add(10 * time.Minute)
	pastLock := now.Add(-10 * time.Minute)

	base := identity.AccessSessionState{
		SessionID:        accessAuthSessionID,
		UserID:           accessAuthUserID,
		SessionExpiresAt: now.Add(time.Hour),
		Username:         "access.user",
		UserStatus:       identity.StatusActive,
		PlatformRole:     identity.PlatformRoleUser,
	}

	cases := []struct {
		name  string
		state identity.AccessSessionState
		err   error // if set, returned by resolver instead of state
	}{
		{"missing", identity.AccessSessionState{}, identity.ErrNotFound},
		{"subject mismatch", withState(base, func(s *identity.AccessSessionState) { s.UserID = "other-user" }), nil},
		{"session mismatch", withState(base, func(s *identity.AccessSessionState) { s.SessionID = "other-session" }), nil},
		{"revoked", withState(base, func(s *identity.AccessSessionState) { s.SessionRevokedAt = &revoked }), nil},
		{"expired", withState(base, func(s *identity.AccessSessionState) { s.SessionExpiresAt = now }), nil},
		{"locked status", withState(base, func(s *identity.AccessSessionState) { s.UserStatus = identity.StatusLocked }), nil},
		{"disabled status", withState(base, func(s *identity.AccessSessionState) { s.UserStatus = identity.StatusDisabled }), nil},
		{"future locked_until", withState(base, func(s *identity.AccessSessionState) { s.LockedUntil = &futureLock }), nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &accessSessionRepoStub{state: tc.state, err: tc.err}
			service := newAccessSessionService(t, stub, now)
			_, err := service.AuthenticateAccessToken(context.Background(), token)
			if !errors.Is(err, ErrAccessUnauthenticated) {
				t.Fatalf("expected ErrAccessUnauthenticated, got %v", err)
			}
			// Past lock must not reject (control case covered separately).
			_ = pastLock
		})
	}

	// past locked_until with ACTIVE status remains valid.
	t.Run("past locked_until allowed", func(t *testing.T) {
		state := base
		state.LockedUntil = &pastLock
		stub := &accessSessionRepoStub{state: state}
		service := newAccessSessionService(t, stub, now)
		if _, err := service.AuthenticateAccessToken(context.Background(), token); err != nil {
			t.Fatalf("past locked_until should allow: %v", err)
		}
	})
}

func TestAuthenticateAccessTokenInfrastructureUnavailable(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	token := mustAccessToken(t, now, "access.user", identity.PlatformRoleUser)
	infra := errors.New("database connection refused")
	stub := &accessSessionRepoStub{err: infra}
	service := newAccessSessionService(t, stub, now)

	_, err := service.AuthenticateAccessToken(context.Background(), token)
	if !errors.Is(err, ErrAuthenticationUnavailable) {
		t.Fatalf("expected ErrAuthenticationUnavailable, got %v", err)
	}
	if errors.Is(err, ErrAccessUnauthenticated) {
		t.Fatalf("infra error must not collapse to unauthenticated: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("resolver calls=%d", stub.calls)
	}
}

func TestAuthenticateAccessTokenRejectsInvalidJWT(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	stub := &accessSessionRepoStub{
		state: identity.AccessSessionState{
			SessionID: accessAuthSessionID, UserID: accessAuthUserID,
			SessionExpiresAt: now.Add(time.Hour), Username: "access.user",
			UserStatus: identity.StatusActive, PlatformRole: identity.PlatformRoleUser,
		},
	}
	service := newAccessSessionService(t, stub, now)

	_, err := service.AuthenticateAccessToken(context.Background(), "not-a-jwt")
	if !errors.Is(err, ErrAccessUnauthenticated) {
		t.Fatalf("expected unauthenticated for bad jwt, got %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("resolver must not be called for invalid JWT, calls=%d", stub.calls)
	}

	// Expired JWT (use fixed now after expiry).
	token := mustAccessToken(t, now, "access.user", identity.PlatformRoleUser)
	serviceExpired := newAccessSessionService(t, stub, now.Add(DefaultAccessTokenTTL+time.Minute))
	_, err = serviceExpired.AuthenticateAccessToken(context.Background(), token)
	if !errors.Is(err, ErrAccessUnauthenticated) {
		t.Fatalf("expected unauthenticated for expired jwt, got %v", err)
	}
}

func TestAuthenticateAccessTokenSingleNowAndSingleResolve(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	token := mustAccessToken(t, now, "access.user", identity.PlatformRoleUser)
	var nowCalls int
	stub := &accessSessionRepoStub{
		state: identity.AccessSessionState{
			SessionID: accessAuthSessionID, UserID: accessAuthUserID,
			SessionExpiresAt: now.Add(time.Hour), Username: "access.user",
			UserStatus: identity.StatusActive, PlatformRole: identity.PlatformRoleUser,
		},
	}
	access, err := NewAccessTokenManager(accessAuthSecret, "actweave-test", 0)
	if err != nil {
		t.Fatal(err)
	}
	passwords, err := NewPasswordManager(DefaultArgon2idParams())
	if err != nil {
		t.Fatal(err)
	}
	refresh := NewRefreshTokenManager()
	service, err := newService(stub, passwords, access, refresh, ServiceConfig{
		RefreshTTL: 7 * 24 * time.Hour,
		LockoutPolicy: LockoutPolicy{
			MaxFailedAttempts: 5,
			Duration:          15 * time.Minute,
		},
	}, func() time.Time {
		nowCalls++
		return now
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateAccessToken(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if nowCalls != 1 {
		t.Fatalf("expected exactly one now() capture, got %d", nowCalls)
	}
	if stub.calls != 1 {
		t.Fatalf("expected exactly one resolve, got %d", stub.calls)
	}
}

func mustAccessToken(t *testing.T, now time.Time, username string, role identity.PlatformRole) string {
	t.Helper()
	access, err := NewAccessTokenManager(accessAuthSecret, "actweave-test", 0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := access.Generate(identity.User{
		ID: accessAuthUserID, Username: username, PlatformRole: role,
	}, accessAuthSessionID, now)
	if err != nil {
		t.Fatal(err)
	}
	return token.Value
}

func newAccessSessionService(t *testing.T, stub *accessSessionRepoStub, now time.Time) *Service {
	t.Helper()
	access, err := NewAccessTokenManager(accessAuthSecret, "actweave-test", 0)
	if err != nil {
		t.Fatal(err)
	}
	passwords, err := NewPasswordManager(DefaultArgon2idParams())
	if err != nil {
		t.Fatal(err)
	}
	refresh := NewRefreshTokenManager()
	service, err := newService(stub, passwords, access, refresh, ServiceConfig{
		RefreshTTL: 7 * 24 * time.Hour,
		LockoutPolicy: LockoutPolicy{
			MaxFailedAttempts: 5,
			Duration:          15 * time.Minute,
		},
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func withState(base identity.AccessSessionState, mutate func(*identity.AccessSessionState)) identity.AccessSessionState {
	state := base
	mutate(&state)
	return state
}

// accessSessionRepoStub implements serviceRepository for access-session unit tests.
// Unused methods panic so accidental calls fail loudly.
type accessSessionRepoStub struct {
	state         identity.AccessSessionState
	err           error
	calls         int
	lastSubject   string
	lastSessionID string
	nowReads      int
}

func (s *accessSessionRepoStub) ResolveAccessSessionState(
	_ context.Context,
	subject string,
	sessionID string,
) (identity.AccessSessionState, error) {
	s.calls++
	s.lastSubject = subject
	s.lastSessionID = sessionID
	if s.err != nil {
		return identity.AccessSessionState{}, s.err
	}
	return s.state, nil
}

func (s *accessSessionRepoStub) unused(name string) {
	panic(fmt.Sprintf("accessSessionRepoStub: unexpected call to %s", name))
}

func (s *accessSessionRepoStub) CreateLocalUser(context.Context, identity.NewLocalUser) (identity.User, error) {
	s.unused("CreateLocalUser")
	return identity.User{}, nil
}
func (s *accessSessionRepoStub) BootstrapFirstAdmin(context.Context, identity.NewLocalUser) (identity.User, bool, error) {
	s.unused("BootstrapFirstAdmin")
	return identity.User{}, false, nil
}
func (s *accessSessionRepoStub) ListUsers(context.Context, int) ([]identity.User, error) {
	s.unused("ListUsers")
	return nil, nil
}
func (s *accessSessionRepoStub) SearchUsers(context.Context, identity.UserListQuery) (identity.UserPage, error) {
	s.unused("SearchUsers")
	return identity.UserPage{}, nil
}
func (s *accessSessionRepoStub) ListUserWorkspaceMemberships(context.Context, string, bool) ([]identity.UserWorkspaceMembership, error) {
	s.unused("ListUserWorkspaceMemberships")
	return nil, nil
}
func (s *accessSessionRepoStub) UpdateUserProfile(context.Context, string, identity.UserProfileUpdate) (identity.User, error) {
	s.unused("UpdateUserProfile")
	return identity.User{}, nil
}
func (s *accessSessionRepoStub) GetLoginIdentity(context.Context, string) (identity.LoginIdentity, error) {
	s.unused("GetLoginIdentity")
	return identity.LoginIdentity{}, nil
}
func (s *accessSessionRepoStub) GetUserByID(context.Context, string) (identity.User, error) {
	s.unused("GetUserByID")
	return identity.User{}, nil
}
func (s *accessSessionRepoStub) GetPasswordCredential(context.Context, string) (identity.PasswordCredential, error) {
	s.unused("GetPasswordCredential")
	return identity.PasswordCredential{}, nil
}
func (s *accessSessionRepoStub) RecordPasswordFailure(context.Context, string, time.Time, int, time.Duration) (identity.PasswordCredential, error) {
	s.unused("RecordPasswordFailure")
	return identity.PasswordCredential{}, nil
}
func (s *accessSessionRepoStub) ClearPasswordFailures(context.Context, string) (identity.PasswordCredential, error) {
	s.unused("ClearPasswordFailures")
	return identity.PasswordCredential{}, nil
}
func (s *accessSessionRepoStub) ReplacePasswordCredential(context.Context, string, identity.CredentialReplacement) (identity.PasswordCredential, error) {
	s.unused("ReplacePasswordCredential")
	return identity.PasswordCredential{}, nil
}
func (s *accessSessionRepoStub) CompleteSuccessfulLogin(context.Context, string, time.Time) (identity.User, error) {
	s.unused("CompleteSuccessfulLogin")
	return identity.User{}, nil
}
func (s *accessSessionRepoStub) CreateAuthSession(context.Context, identity.NewAuthSession) (identity.AuthSession, error) {
	s.unused("CreateAuthSession")
	return identity.AuthSession{}, nil
}
func (s *accessSessionRepoStub) ValidateRefreshSession(context.Context, string, string, time.Time) (identity.AuthSession, error) {
	s.unused("ValidateRefreshSession")
	return identity.AuthSession{}, nil
}
func (s *accessSessionRepoStub) RotateRefreshToken(context.Context, string, string, string, time.Time) (identity.AuthSession, error) {
	s.unused("RotateRefreshToken")
	return identity.AuthSession{}, nil
}
func (s *accessSessionRepoStub) GetAuthSession(context.Context, string) (identity.AuthSession, error) {
	s.unused("GetAuthSession")
	return identity.AuthSession{}, nil
}
func (s *accessSessionRepoStub) RevokeAuthSession(context.Context, string, time.Time) (identity.AuthSession, error) {
	s.unused("RevokeAuthSession")
	return identity.AuthSession{}, nil
}
func (s *accessSessionRepoStub) ReplacePasswordAndRevokeSessions(context.Context, string, identity.CredentialReplacement, time.Time) (identity.PasswordCredential, int64, error) {
	s.unused("ReplacePasswordAndRevokeSessions")
	return identity.PasswordCredential{}, 0, nil
}
func (s *accessSessionRepoStub) UpdateStatusAndRevokeSessions(context.Context, string, identity.Status, int64, time.Time) (identity.User, int64, error) {
	s.unused("UpdateStatusAndRevokeSessions")
	return identity.User{}, 0, nil
}
func (s *accessSessionRepoStub) UpdatePlatformRoleAndRevokeSessions(context.Context, string, identity.PlatformRole, int64, time.Time) (identity.User, int64, error) {
	s.unused("UpdatePlatformRoleAndRevokeSessions")
	return identity.User{}, 0, nil
}
func (s *accessSessionRepoStub) UnlockUser(context.Context, string, int64, time.Time) (identity.User, identity.PasswordCredential, error) {
	s.unused("UnlockUser")
	return identity.User{}, identity.PasswordCredential{}, nil
}
