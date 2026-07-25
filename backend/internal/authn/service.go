package authn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/identity"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrAccountLocked      = errors.New("account is locked")
	ErrAccountDisabled    = errors.New("account is disabled")
	ErrRefreshRejected    = errors.New("refresh session rejected")
)

type ServiceConfig struct {
	RefreshTTL    time.Duration
	LockoutPolicy LockoutPolicy
}

type LoginRequest struct {
	Username  string
	Password  string
	UserAgent *string
	IP        *string
}

type AuthResult struct {
	User               identity.User
	AccessToken        string `json:"-"`
	AccessTokenExpires time.Time
	RefreshToken       string `json:"-"`
	RefreshExpiresAt   time.Time
	SessionID          string
	MustChangePassword bool
}

type AuthenticationAuditEvent struct {
	Action      string
	UserID      string
	SubjectHash string
	Result      string
	ErrorCode   string
	SourceIP    string
	UserAgent   string
}

type AuthenticationAuditSink interface {
	RecordAuthentication(context.Context, AuthenticationAuditEvent) error
}

type IdentityManagementAuditEvent struct {
	Action       string
	ActorUserID  string
	TargetUserID string
	Before       map[string]any
	After        map[string]any
	Metadata     map[string]any
}

type IdentityManagementAuditSink interface {
	RecordIdentityManagement(context.Context, IdentityManagementAuditEvent) error
}

type ServiceOption func(*Service) error

func WithAuthenticationAudit(sink AuthenticationAuditSink) ServiceOption {
	return func(service *Service) error {
		if sink == nil {
			return errors.New("authentication audit sink is required")
		}
		service.audit = sink
		return nil
	}
}

func WithIdentityManagementAudit(sink IdentityManagementAuditSink) ServiceOption {
	return func(service *Service) error {
		if sink == nil {
			return errors.New("identity management audit sink is required")
		}
		service.managementAudit = sink
		return nil
	}
}

type serviceRepository interface {
	CreateLocalUser(context.Context, identity.NewLocalUser) (identity.User, error)
	BootstrapFirstAdmin(context.Context, identity.NewLocalUser) (identity.User, bool, error)
	ListUsers(context.Context, int) ([]identity.User, error)
	SearchUsers(context.Context, identity.UserListQuery) (identity.UserPage, error)
	ListUserWorkspaceMemberships(context.Context, string, bool) ([]identity.UserWorkspaceMembership, error)
	UpdateUserProfile(context.Context, string, identity.UserProfileUpdate) (identity.User, error)
	GetLoginIdentity(context.Context, string) (identity.LoginIdentity, error)
	GetUserByID(context.Context, string) (identity.User, error)
	GetPasswordCredential(context.Context, string) (identity.PasswordCredential, error)
	RecordPasswordFailure(context.Context, string, time.Time, int, time.Duration) (identity.PasswordCredential, error)
	ClearPasswordFailures(context.Context, string) (identity.PasswordCredential, error)
	ReplacePasswordCredential(context.Context, string, identity.CredentialReplacement) (identity.PasswordCredential, error)
	CompleteSuccessfulLogin(context.Context, string, time.Time) (identity.User, error)
	CreateAuthSession(context.Context, identity.NewAuthSession) (identity.AuthSession, error)
	ValidateRefreshSession(context.Context, string, string, time.Time) (identity.AuthSession, error)
	RotateRefreshToken(context.Context, string, string, string, time.Time) (identity.AuthSession, error)
	GetAuthSession(context.Context, string) (identity.AuthSession, error)
	RevokeAuthSession(context.Context, string, time.Time) (identity.AuthSession, error)
	ReplacePasswordAndRevokeSessions(context.Context, string, identity.CredentialReplacement, time.Time) (identity.PasswordCredential, int64, error)
	UpdateStatusAndRevokeSessions(context.Context, string, identity.Status, int64, time.Time) (identity.User, int64, error)
	UpdatePlatformRoleAndRevokeSessions(context.Context, string, identity.PlatformRole, int64, time.Time) (identity.User, int64, error)
	UnlockUser(context.Context, string, int64, time.Time) (identity.User, identity.PasswordCredential, error)
}

type CreateUserRequest struct {
	ID                 string
	Username           string
	Email              *string
	DisplayName        string
	AvatarURL          *string
	Password           string
	Status             identity.Status
	PlatformRole       identity.PlatformRole
	Locale             string
	Timezone           string
	MustChangePassword bool
}

// Service owns local login, refresh, logout, password changes, administrator
// reset/unlock, and security-sensitive session revocation workflows.
type Service struct {
	repository      serviceRepository
	passwords       *PasswordManager
	access          *AccessTokenManager
	refresh         *RefreshTokenManager
	lockout         *LoginLockout
	refreshTTL      time.Duration
	dummyHash       string
	now             func() time.Time
	audit           AuthenticationAuditSink
	managementAudit IdentityManagementAuditSink
}

func NewService(
	repository serviceRepository,
	passwords *PasswordManager,
	access *AccessTokenManager,
	refresh *RefreshTokenManager,
	config ServiceConfig,
	options ...ServiceOption,
) (*Service, error) {
	service, err := newService(repository, passwords, access, refresh, config, func() time.Time {
		return time.Now().UTC()
	})
	if err != nil {
		return nil, err
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("authentication service option is required")
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func newService(
	repository serviceRepository,
	passwords *PasswordManager,
	access *AccessTokenManager,
	refresh *RefreshTokenManager,
	config ServiceConfig,
	now func() time.Time,
) (*Service, error) {
	if repository == nil || passwords == nil || access == nil || refresh == nil || now == nil {
		return nil, errors.New("authentication service dependencies are required")
	}
	if config.RefreshTTL < 7*24*time.Hour || config.RefreshTTL > 30*24*time.Hour {
		return nil, errors.New("refresh TTL must be between 7 and 30 days")
	}
	lockout, err := newLoginLockout(repository, config.LockoutPolicy, now)
	if err != nil {
		return nil, err
	}
	dummyHash, err := passwords.Hash("actweave-dummy-password-verification")
	if err != nil {
		return nil, fmt.Errorf("create authentication timing hash: %w", err)
	}
	return &Service{
		repository: repository,
		passwords:  passwords,
		access:     access,
		refresh:    refresh,
		lockout:    lockout,
		refreshTTL: config.RefreshTTL,
		dummyHash:  dummyHash,
		now:        now,
	}, nil
}

func (s *Service) Login(ctx context.Context, request LoginRequest) (result AuthResult, resultErr error) {
	var authenticatedUserID string
	defer func() {
		if s.audit == nil {
			return
		}
		auditResult, errorCode := "SUCCESS", ""
		if resultErr != nil {
			auditResult, errorCode = "FAILURE", authenticationAuditErrorCode(resultErr)
		}
		event := AuthenticationAuditEvent{
			Action: "authentication.login", UserID: authenticatedUserID,
			SubjectHash: authenticationSubjectHash(request.Username), Result: auditResult,
			ErrorCode: errorCode,
		}
		if request.IP != nil {
			event.SourceIP = strings.TrimSpace(*request.IP)
		}
		if request.UserAgent != nil {
			event.UserAgent = strings.TrimSpace(*request.UserAgent)
		}
		if err := s.audit.RecordAuthentication(ctx, event); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("record authentication audit: %w", err))
		}
	}()
	if request.Username == "" || request.Password == "" {
		return AuthResult{}, ErrInvalidCredentials
	}
	login, err := s.repository.GetLoginIdentity(ctx, request.Username)
	if errors.Is(err, identity.ErrNotFound) {
		_, _, _ = s.passwords.Verify(request.Password, s.dummyHash)
		return AuthResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return AuthResult{}, err
	}
	if err := validateLoginStatus(login.User, login.Credential, s.now()); err != nil {
		return AuthResult{}, err
	}
	if login.Credential.PasswordAlgorithm != PasswordAlgorithmArgon2id {
		return AuthResult{}, ErrInvalidCredentials
	}
	valid, needsRehash, err := s.passwords.Verify(request.Password, login.Credential.PasswordHash)
	if err != nil {
		return AuthResult{}, ErrInvalidCredentials
	}
	if !valid {
		if _, failureErr := s.lockout.RecordFailure(ctx, login.User.ID); failureErr != nil {
			return AuthResult{}, failureErr
		}
		return AuthResult{}, ErrInvalidCredentials
	}
	if needsRehash {
		newHash, err := s.passwords.Hash(request.Password)
		if err != nil {
			return AuthResult{}, err
		}
		credential, err := s.repository.ReplacePasswordCredential(ctx, login.User.ID, identity.CredentialReplacement{
			PasswordHash:              newHash,
			PasswordAlgorithm:         PasswordAlgorithmArgon2id,
			PasswordChangedAt:         s.now(),
			ExpectedPasswordChangedAt: login.Credential.PasswordChangedAt,
			MustChangePassword:        login.Credential.MustChangePassword,
		})
		if err != nil {
			return AuthResult{}, err
		}
		login.Credential = credential
	}

	now := s.now()
	user, err := s.repository.CompleteSuccessfulLogin(ctx, login.User.ID, now)
	if err != nil {
		return AuthResult{}, err
	}
	issued, err := s.refresh.Issue()
	if err != nil {
		return AuthResult{}, err
	}
	access, err := s.access.Generate(user, issued.SessionID, now)
	if err != nil {
		return AuthResult{}, err
	}
	refreshExpiresAt := now.Add(s.refreshTTL)
	if _, err := s.repository.CreateAuthSession(ctx, identity.NewAuthSession{
		ID:               issued.SessionID,
		UserID:           user.ID,
		RefreshTokenHash: issued.Hash,
		UserAgent:        request.UserAgent,
		IP:               request.IP,
		ExpiresAt:        refreshExpiresAt,
	}); err != nil {
		return AuthResult{}, err
	}
	authenticatedUserID = user.ID
	return AuthResult{
		User:               user,
		AccessToken:        access.Value,
		AccessTokenExpires: access.ExpiresAt,
		RefreshToken:       issued.Plaintext,
		RefreshExpiresAt:   refreshExpiresAt,
		SessionID:          issued.SessionID,
		MustChangePassword: login.Credential.MustChangePassword,
	}, nil
}

func (s *Service) GetUser(ctx context.Context, userID string) (identity.User, error) {
	return s.repository.GetUserByID(ctx, strings.TrimSpace(userID))
}

func (s *Service) ListUsers(ctx context.Context, limit int) ([]identity.User, error) {
	return s.repository.ListUsers(ctx, limit)
}

func (s *Service) SearchUsers(ctx context.Context, query identity.UserListQuery) (identity.UserPage, error) {
	return s.repository.SearchUsers(ctx, query)
}

func (s *Service) ListUserWorkspaceMemberships(
	ctx context.Context,
	userID string,
	includeDisabled bool,
) ([]identity.UserWorkspaceMembership, error) {
	userID = strings.TrimSpace(userID)
	if _, err := s.repository.GetUserByID(ctx, userID); err != nil {
		return nil, err
	}
	return s.repository.ListUserWorkspaceMemberships(ctx, userID, includeDisabled)
}

func (s *Service) CreateUser(
	ctx context.Context,
	request CreateUserRequest,
) (identity.User, error) {
	request.ID, request.Username = strings.TrimSpace(request.ID), strings.TrimSpace(request.Username)
	request.DisplayName, request.Locale = strings.TrimSpace(request.DisplayName), strings.TrimSpace(request.Locale)
	request.Timezone = strings.TrimSpace(request.Timezone)
	if request.ID == "" || request.Username == "" || request.DisplayName == "" || len(request.Password) < 12 {
		return identity.User{}, identity.ErrInvalid
	}
	hash, err := s.passwords.Hash(request.Password)
	if err != nil {
		return identity.User{}, err
	}
	return s.repository.CreateLocalUser(ctx, identity.NewLocalUser{
		ID: request.ID, Username: request.Username, Email: request.Email,
		DisplayName: request.DisplayName, AvatarURL: request.AvatarURL,
		Status: request.Status, PlatformRole: request.PlatformRole,
		Locale: request.Locale, Timezone: request.Timezone,
		PasswordHash: hash, PasswordAlgorithm: PasswordAlgorithmArgon2id,
		PasswordChangedAt: s.now(), MustChangePassword: request.MustChangePassword,
	})
}

// BootstrapAdmin hashes the configured initial credential and delegates the
// empty-store check plus concurrent-startup serialization to the repository.
// It is not exposed through HTTP and never creates demo or workspace data.
func (s *Service) BootstrapAdmin(
	ctx context.Context,
	request CreateUserRequest,
) (identity.User, bool, error) {
	request.ID, request.Username = strings.TrimSpace(request.ID), strings.TrimSpace(request.Username)
	request.DisplayName, request.Locale = strings.TrimSpace(request.DisplayName), strings.TrimSpace(request.Locale)
	request.Timezone = strings.TrimSpace(request.Timezone)
	if request.ID == "" || request.Username == "" || request.DisplayName == "" || len(request.Password) < 12 ||
		request.Status != identity.StatusActive || request.PlatformRole != identity.PlatformRoleAdmin {
		return identity.User{}, false, identity.ErrInvalid
	}
	hash, err := s.passwords.Hash(request.Password)
	if err != nil {
		return identity.User{}, false, err
	}
	return s.repository.BootstrapFirstAdmin(ctx, identity.NewLocalUser{
		ID: request.ID, Username: request.Username, Email: request.Email,
		DisplayName: request.DisplayName, AvatarURL: request.AvatarURL,
		Status: request.Status, PlatformRole: request.PlatformRole,
		Locale: request.Locale, Timezone: request.Timezone,
		PasswordHash: hash, PasswordAlgorithm: PasswordAlgorithmArgon2id,
		PasswordChangedAt: s.now(), MustChangePassword: request.MustChangePassword,
	})
}

func (s *Service) UpdateUserProfile(
	ctx context.Context,
	userID string,
	input identity.UserProfileUpdate,
) (identity.User, error) {
	return s.repository.UpdateUserProfile(ctx, strings.TrimSpace(userID), input)
}

func authenticationSubjectHash(username string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username))))
	return hex.EncodeToString(digest[:])
}

func authenticationAuditErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		return "INVALID_CREDENTIALS"
	case errors.Is(err, ErrAccountLocked):
		return "ACCOUNT_LOCKED"
	case errors.Is(err, ErrAccountDisabled):
		return "ACCOUNT_DISABLED"
	default:
		return "AUTHENTICATION_FAILED"
	}
}

func (s *Service) Refresh(ctx context.Context, presentedToken string) (AuthResult, error) {
	now := s.now()
	sessionID, currentHash, err := s.refresh.Parse(presentedToken)
	if err != nil {
		return AuthResult{}, ErrRefreshRejected
	}
	session, err := s.repository.ValidateRefreshSession(ctx, sessionID, currentHash, now)
	if err != nil {
		if errors.Is(err, identity.ErrSessionInvalid) {
			return AuthResult{}, ErrRefreshRejected
		}
		return AuthResult{}, err
	}
	user, err := s.repository.GetUserByID(ctx, session.UserID)
	if err != nil {
		return AuthResult{}, err
	}
	if user.Status == identity.StatusDisabled {
		return AuthResult{}, ErrAccountDisabled
	}
	if user.Status != identity.StatusActive {
		return AuthResult{}, ErrAccountLocked
	}
	replacement, err := s.refresh.Rotate(sessionID)
	if err != nil {
		return AuthResult{}, err
	}
	access, err := s.access.Generate(user, sessionID, now)
	if err != nil {
		return AuthResult{}, err
	}
	if _, err := s.repository.RotateRefreshToken(ctx, sessionID, currentHash, replacement.Hash, now); err != nil {
		if errors.Is(err, identity.ErrConflict) || errors.Is(err, identity.ErrSessionInvalid) {
			return AuthResult{}, ErrRefreshRejected
		}
		return AuthResult{}, err
	}
	credential, err := s.repository.GetPasswordCredential(ctx, user.ID)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{
		User:               user,
		AccessToken:        access.Value,
		AccessTokenExpires: access.ExpiresAt,
		RefreshToken:       replacement.Plaintext,
		RefreshExpiresAt:   session.ExpiresAt,
		SessionID:          sessionID,
		MustChangePassword: credential.MustChangePassword,
	}, nil
}

func (s *Service) Logout(ctx context.Context, presentedToken string) error {
	now := s.now()
	sessionID, hash, err := s.refresh.Parse(presentedToken)
	if err != nil {
		return ErrRefreshRejected
	}
	if _, err := s.repository.ValidateRefreshSession(ctx, sessionID, hash, now); err != nil {
		if !errors.Is(err, identity.ErrSessionInvalid) {
			return err
		}
		stored, readErr := s.repository.GetAuthSession(ctx, sessionID)
		if readErr == nil && stored.RevokedAt != nil {
			return nil
		}
		if readErr != nil && !errors.Is(readErr, identity.ErrNotFound) {
			return readErr
		}
		return ErrRefreshRejected
	}
	_, err = s.repository.RevokeAuthSession(ctx, sessionID, now)
	return err
}

func (s *Service) ChangePassword(
	ctx context.Context,
	userID string,
	currentPassword string,
	newPassword string,
) error {
	user, credential, err := s.userAndCredential(ctx, userID)
	if err != nil {
		return err
	}
	if err := validateLoginStatus(user, credential, s.now()); err != nil {
		return err
	}
	valid, _, err := s.passwords.Verify(currentPassword, credential.PasswordHash)
	if err != nil || !valid {
		if _, failureErr := s.lockout.RecordFailure(ctx, userID); failureErr != nil {
			return failureErr
		}
		return ErrInvalidCredentials
	}
	newHash, err := s.passwords.Hash(newPassword)
	if err != nil {
		return err
	}
	now := s.now()
	_, _, err = s.repository.ReplacePasswordAndRevokeSessions(ctx, userID, identity.CredentialReplacement{
		PasswordHash:              newHash,
		PasswordAlgorithm:         PasswordAlgorithmArgon2id,
		PasswordChangedAt:         now,
		ExpectedPasswordChangedAt: credential.PasswordChangedAt,
		MustChangePassword:        false,
	}, now)
	return err
}

func (s *Service) ResetPassword(
	ctx context.Context,
	userID string,
	temporaryPassword string,
) error {
	credential, err := s.repository.GetPasswordCredential(ctx, userID)
	if err != nil {
		return err
	}
	newHash, err := s.passwords.Hash(temporaryPassword)
	if err != nil {
		return err
	}
	now := s.now()
	_, _, err = s.repository.ReplacePasswordAndRevokeSessions(ctx, userID, identity.CredentialReplacement{
		PasswordHash:              newHash,
		PasswordAlgorithm:         PasswordAlgorithmArgon2id,
		PasswordChangedAt:         now,
		ExpectedPasswordChangedAt: credential.PasswordChangedAt,
		MustChangePassword:        true,
	}, now)
	return err
}

func (s *Service) SetUserStatus(
	ctx context.Context,
	userID string,
	status identity.Status,
	expectedLockVersion int64,
) (identity.User, error) {
	user, _, err := s.repository.UpdateStatusAndRevokeSessions(
		ctx,
		userID,
		status,
		expectedLockVersion,
		s.now(),
	)
	return user, err
}

func (s *Service) SetPlatformRole(
	ctx context.Context,
	userID string,
	role identity.PlatformRole,
	expectedLockVersion int64,
) (identity.User, error) {
	user, _, err := s.repository.UpdatePlatformRoleAndRevokeSessions(
		ctx, userID, role, expectedLockVersion, s.now(),
	)
	return user, err
}

func (s *Service) UnlockUser(
	ctx context.Context,
	userID string,
	expectedLockVersion int64,
) (identity.User, error) {
	user, _, err := s.repository.UnlockUser(ctx, userID, expectedLockVersion, s.now())
	if errors.Is(err, identity.ErrInvalid) {
		return identity.User{}, ErrAccountDisabled
	}
	return user, err
}

func (s *Service) userAndCredential(
	ctx context.Context,
	userID string,
) (identity.User, identity.PasswordCredential, error) {
	user, err := s.repository.GetUserByID(ctx, userID)
	if err != nil {
		return identity.User{}, identity.PasswordCredential{}, err
	}
	credential, err := s.repository.GetPasswordCredential(ctx, userID)
	if err != nil {
		return identity.User{}, identity.PasswordCredential{}, err
	}
	return user, credential, nil
}

func validateLoginStatus(
	user identity.User,
	credential identity.PasswordCredential,
	now time.Time,
) error {
	switch user.Status {
	case identity.StatusDisabled:
		return ErrAccountDisabled
	case identity.StatusLocked:
		return ErrAccountLocked
	case identity.StatusActive:
	default:
		return ErrInvalidCredentials
	}
	if credential.LockedUntil != nil && credential.LockedUntil.After(now) {
		return ErrAccountLocked
	}
	return nil
}
