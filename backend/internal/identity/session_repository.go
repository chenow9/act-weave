package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrSessionInvalid = errors.New("auth session is invalid, revoked, or expired")

const sessionColumns = `
	id,
	user_id,
	user_agent,
	ip::TEXT,
	expires_at,
	revoked_at,
	last_seen_at,
	created_at
`

func (r *Repository) CreateAuthSession(
	ctx context.Context,
	input NewAuthSession,
) (AuthSession, error) {
	if input.ID == "" || input.UserID == "" || input.RefreshTokenHash == "" || input.ExpiresAt.IsZero() {
		return AuthSession{}, ErrInvalid
	}
	session, err := scanAuthSession(r.db.QueryRowContext(ctx, `
		INSERT INTO auth_sessions (
			id, user_id, refresh_token_hash, user_agent, ip, expires_at
		)
		SELECT $1, $2, $3, $4, $5, $6
		FROM users
		WHERE id = $2 AND status = 'ACTIVE'
		RETURNING `+sessionColumns,
		input.ID,
		input.UserID,
		input.RefreshTokenHash,
		input.UserAgent,
		input.IP,
		input.ExpiresAt,
	))
	if err != nil {
		return AuthSession{}, mapWriteError("create auth session", err)
	}
	return session, nil
}

func (r *Repository) GetAuthSession(ctx context.Context, id string) (AuthSession, error) {
	session, err := scanAuthSession(r.db.QueryRowContext(ctx, `
		SELECT `+sessionColumns+`
		FROM auth_sessions
		WHERE id = $1
	`, id))
	return session, mapReadError("get auth session", err)
}

// ValidateRefreshSession requires both the public session identifier and the
// hash of the presented opaque token. No token plaintext enters this package.
func (r *Repository) ValidateRefreshSession(
	ctx context.Context,
	sessionID string,
	refreshTokenHash string,
	at time.Time,
) (AuthSession, error) {
	if sessionID == "" || refreshTokenHash == "" || at.IsZero() {
		return AuthSession{}, ErrSessionInvalid
	}
	session, err := scanAuthSession(r.db.QueryRowContext(ctx, `
		SELECT `+sessionColumns+`
		FROM auth_sessions
		WHERE id = $1
		  AND refresh_token_hash = $2
		  AND revoked_at IS NULL
		  AND expires_at > $3
	`, sessionID, refreshTokenHash, at))
	if errors.Is(err, sql.ErrNoRows) {
		return AuthSession{}, ErrSessionInvalid
	}
	if err != nil {
		return AuthSession{}, fmt.Errorf("validate refresh session: %w", err)
	}
	return session, nil
}

// RotateRefreshToken is a compare-and-swap on the current token hash. Two
// requests presenting the same token cannot both install a successor token.
func (r *Repository) RotateRefreshToken(
	ctx context.Context,
	sessionID string,
	currentHash string,
	newHash string,
	at time.Time,
) (AuthSession, error) {
	if sessionID == "" || currentHash == "" || newHash == "" || currentHash == newHash || at.IsZero() {
		return AuthSession{}, ErrInvalid
	}
	session, err := scanAuthSession(r.db.QueryRowContext(ctx, `
		UPDATE auth_sessions
		SET refresh_token_hash = $3,
			last_seen_at = GREATEST($4, created_at)
		WHERE id = $1
		  AND refresh_token_hash = $2
		  AND revoked_at IS NULL
		  AND expires_at > $4
		RETURNING `+sessionColumns,
		sessionID,
		currentHash,
		newHash,
		at,
	))
	if errors.Is(err, sql.ErrNoRows) {
		stored, storedErr := r.GetAuthSession(ctx, sessionID)
		if errors.Is(storedErr, ErrNotFound) {
			return AuthSession{}, ErrSessionInvalid
		}
		if storedErr != nil {
			return AuthSession{}, storedErr
		}
		if stored.RevokedAt != nil || !stored.ExpiresAt.After(at) {
			return AuthSession{}, ErrSessionInvalid
		}
		return AuthSession{}, ErrConflict
	}
	if err != nil {
		return AuthSession{}, mapWriteError("rotate refresh token", err)
	}
	return session, nil
}

// RevokeAuthSession is idempotent; the first timestamp remains the revocation
// time on repeated logout requests.
func (r *Repository) RevokeAuthSession(
	ctx context.Context,
	sessionID string,
	at time.Time,
) (AuthSession, error) {
	if sessionID == "" || at.IsZero() {
		return AuthSession{}, ErrInvalid
	}
	session, err := scanAuthSession(r.db.QueryRowContext(ctx, `
		UPDATE auth_sessions
		SET revoked_at = COALESCE(revoked_at, GREATEST($2, created_at))
		WHERE id = $1
		RETURNING `+sessionColumns,
		sessionID,
		at,
	))
	if err != nil {
		return AuthSession{}, mapWriteError("revoke auth session", err)
	}
	return session, nil
}

// RevokeAllAuthSessions invalidates every refresh session for a user. It is
// used after security-sensitive identity changes.
func (r *Repository) RevokeAllAuthSessions(
	ctx context.Context,
	userID string,
	at time.Time,
) (int64, error) {
	if userID == "" || at.IsZero() {
		return 0, ErrInvalid
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE auth_sessions
		SET revoked_at = GREATEST($2, created_at)
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID, at)
	if err != nil {
		return 0, mapWriteError("revoke all auth sessions", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count revoked auth sessions: %w", err)
	}
	return count, nil
}

func scanAuthSession(row rowScanner) (AuthSession, error) {
	var session AuthSession
	err := row.Scan(
		&session.ID,
		&session.UserID,
		&session.UserAgent,
		&session.IP,
		&session.ExpiresAt,
		&session.RevokedAt,
		&session.LastSeenAt,
		&session.CreatedAt,
	)
	return session, err
}
