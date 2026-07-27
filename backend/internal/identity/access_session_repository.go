package identity

import (
	"context"
)

// ResolveAccessSessionState loads the current session + user + credential
// security facts for one Console Access Token subject/session pair.
//
// Contract:
//   - Single parameterized primary-key JOIN; no cache, no writes, no DB clock policy.
//   - Missing session, subject mismatch, missing user, or missing credential all
//     surface as ErrNotFound (no table-level distinction for callers).
//   - Infrastructure/scan failures are wrapped and must not be collapsed into
//     ErrNotFound so authn can map them to AUTHENTICATION_UNAVAILABLE.
func (r *Repository) ResolveAccessSessionState(
	ctx context.Context,
	subject string,
	sessionID string,
) (AccessSessionState, error) {
	if subject == "" || sessionID == "" {
		return AccessSessionState{}, ErrNotFound
	}
	var state AccessSessionState
	err := r.db.QueryRowContext(ctx, `
		SELECT
			s.id,
			s.user_id,
			s.expires_at,
			s.revoked_at,
			u.username::TEXT,
			u.status,
			u.platform_role,
			c.locked_until,
			c.must_change_password
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		JOIN user_credentials c ON c.user_id = u.id
		WHERE s.id = $1
		  AND s.user_id = $2
	`, sessionID, subject).Scan(
		&state.SessionID,
		&state.UserID,
		&state.SessionExpiresAt,
		&state.SessionRevokedAt,
		&state.Username,
		&state.UserStatus,
		&state.PlatformRole,
		&state.LockedUntil,
		&state.MustChangePassword,
	)
	if err != nil {
		return AccessSessionState{}, mapReadError("resolve access session state", err)
	}
	return state, nil
}
