package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"actweave/backend/internal/identity"
)

// Access authentication sentinels for transport mapping (ZKL-63 HIGH-01).
// Invalid session/user/credential states collapse to ErrAccessUnauthenticated
// so callers cannot probe why a token was rejected. Infrastructure failures
// use ErrAuthenticationUnavailable and must fail closed (no JWT-only fallback).
var (
	ErrAccessUnauthenticated      = errors.New("access token is not authenticated")
	ErrAuthenticationUnavailable  = errors.New("authentication service is unavailable")
)

// AccessIdentity is the authoritative Console principal derived from a verified
// JWT plus the current database session/user/credential projection.
// Username, PlatformRole, and MustChangePassword always come from the database,
// never from JWT claim hints.
type AccessIdentity struct {
	UserID             string
	SessionID          string
	Username           string
	PlatformRole       identity.PlatformRole
	MustChangePassword bool
	TokenExpiresAt     time.Time
}

// AuthenticateAccessToken cryptographically validates a Console Access Token
// and revalidates the current session/user/credential state from identity.
//
// A single process UTC timestamp is captured once and used for both JWT
// validation and session/user/credential policy checks.
func (s *Service) AuthenticateAccessToken(
	ctx context.Context,
	accessToken string,
) (AccessIdentity, error) {
	if s == nil || s.access == nil || s.repository == nil {
		return AccessIdentity{}, ErrAuthenticationUnavailable
	}
	if accessToken == "" {
		return AccessIdentity{}, ErrAccessUnauthenticated
	}

	now := s.now().UTC()
	claims, err := s.access.Parse(accessToken, now)
	if err != nil {
		return AccessIdentity{}, ErrAccessUnauthenticated
	}
	if claims.Subject == "" || claims.SessionID == "" {
		return AccessIdentity{}, ErrAccessUnauthenticated
	}

	state, err := s.repository.ResolveAccessSessionState(ctx, claims.Subject, claims.SessionID)
	if err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			return AccessIdentity{}, ErrAccessUnauthenticated
		}
		// Fail closed: never fall back to JWT-only trust.
		return AccessIdentity{}, fmt.Errorf("%w: %v", ErrAuthenticationUnavailable, err)
	}

	if err := validateAccessSessionState(state, claims.Subject, claims.SessionID, now); err != nil {
		return AccessIdentity{}, err
	}

	tokenExpiresAt := now
	if claims.ExpiresAt != nil {
		tokenExpiresAt = claims.ExpiresAt.Time
	}

	return AccessIdentity{
		UserID:             state.UserID,
		SessionID:          state.SessionID,
		Username:           state.Username,
		PlatformRole:       state.PlatformRole,
		MustChangePassword: state.MustChangePassword,
		TokenExpiresAt:     tokenExpiresAt,
	}, nil
}

func validateAccessSessionState(
	state identity.AccessSessionState,
	subject string,
	sessionID string,
	now time.Time,
) error {
	// Unified invalid class — no reason differentiation for callers.
	if state.SessionID == "" || state.UserID == "" {
		return ErrAccessUnauthenticated
	}
	if state.SessionID != sessionID || state.UserID != subject {
		return ErrAccessUnauthenticated
	}
	if state.SessionRevokedAt != nil {
		return ErrAccessUnauthenticated
	}
	if !state.SessionExpiresAt.After(now) {
		return ErrAccessUnauthenticated
	}
	if state.UserStatus != identity.StatusActive {
		return ErrAccessUnauthenticated
	}
	// D3=A: locked_until in the future rejects access even if status is ACTIVE.
	if state.LockedUntil != nil && state.LockedUntil.After(now) {
		return ErrAccessUnauthenticated
	}
	return nil
}
