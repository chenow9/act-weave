package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (r *Repository) GetLoginIdentity(ctx context.Context, username string) (LoginIdentity, error) {
	var login LoginIdentity
	err := r.db.QueryRowContext(ctx, `
		SELECT
			u.id,
			u.username::TEXT,
			u.email::TEXT,
			u.display_name,
			u.avatar_url,
			u.status,
			u.platform_role,
			u.locale,
			u.timezone,
			u.last_login_at,
			u.created_at,
			u.updated_at,
			u.lock_version,
			c.user_id,
			c.password_hash,
			c.password_algo,
			c.password_changed_at,
			c.failed_attempts,
			c.locked_until,
			c.must_change_password
		FROM users u
		JOIN user_credentials c ON c.user_id = u.id
		WHERE u.username = $1
	`, username).Scan(
		&login.User.ID,
		&login.User.Username,
		&login.User.Email,
		&login.User.DisplayName,
		&login.User.AvatarURL,
		&login.User.Status,
		&login.User.PlatformRole,
		&login.User.Locale,
		&login.User.Timezone,
		&login.User.LastLoginAt,
		&login.User.CreatedAt,
		&login.User.UpdatedAt,
		&login.User.LockVersion,
		&login.Credential.UserID,
		&login.Credential.PasswordHash,
		&login.Credential.PasswordAlgorithm,
		&login.Credential.PasswordChangedAt,
		&login.Credential.FailedAttempts,
		&login.Credential.LockedUntil,
		&login.Credential.MustChangePassword,
	)
	if err != nil {
		return LoginIdentity{}, mapReadError("get login identity", err)
	}
	return login, nil
}

// CompleteSuccessfulLogin resets transient password failures and records the
// last successful login in one transaction.
func (r *Repository) CompleteSuccessfulLogin(
	ctx context.Context,
	userID string,
	at time.Time,
) (User, error) {
	if userID == "" || at.IsZero() {
		return User{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin successful login transaction: %w", err)
	}
	defer tx.Rollback()
	credentialResult, err := tx.ExecContext(ctx, `
		UPDATE user_credentials
		SET failed_attempts = 0,
			locked_until = NULL
		WHERE user_id = $1
	`, userID)
	if err != nil {
		return User{}, mapWriteError("clear login failures", err)
	}
	if affected, err := credentialResult.RowsAffected(); err != nil {
		return User{}, fmt.Errorf("count cleared login credentials: %w", err)
	} else if affected != 1 {
		return User{}, ErrNotFound
	}

	user, err := scanUser(tx.QueryRowContext(ctx, `
		UPDATE users
		SET last_login_at = GREATEST(COALESCE(last_login_at, $2), $2)
		WHERE id = $1 AND status = 'ACTIVE'
		RETURNING `+userColumns,
		userID,
		at,
	))
	if err != nil {
		return User{}, mapWriteError("record successful login", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit successful login transaction: %w", err)
	}
	return user, nil
}

// ReplacePasswordAndRevokeSessions atomically changes a password credential
// and invalidates every refresh session for the user.
func (r *Repository) ReplacePasswordAndRevokeSessions(
	ctx context.Context,
	userID string,
	replacement CredentialReplacement,
	revokedAt time.Time,
) (PasswordCredential, int64, error) {
	if replacement.PasswordChangedAt.IsZero() {
		replacement.PasswordChangedAt = time.Now().UTC()
	}
	if userID == "" || replacement.PasswordHash == "" || replacement.PasswordAlgorithm == "" ||
		replacement.ExpectedPasswordChangedAt.IsZero() || revokedAt.IsZero() {
		return PasswordCredential{}, 0, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return PasswordCredential{}, 0, fmt.Errorf("begin password replacement transaction: %w", err)
	}
	defer tx.Rollback()

	credential, err := scanPasswordCredential(tx.QueryRowContext(ctx, `
		UPDATE user_credentials
		SET password_hash = $2,
			password_algo = $3,
			password_changed_at = $4,
			failed_attempts = 0,
			locked_until = NULL,
			must_change_password = $5
		WHERE user_id = $1 AND password_changed_at = $6
		RETURNING
			user_id,
			password_hash,
			password_algo,
			password_changed_at,
			failed_attempts,
			locked_until,
			must_change_password
	`,
		userID,
		replacement.PasswordHash,
		replacement.PasswordAlgorithm,
		replacement.PasswordChangedAt,
		replacement.MustChangePassword,
		replacement.ExpectedPasswordChangedAt,
	))
	if errors.Is(err, sql.ErrNoRows) {
		exists, existsErr := credentialExistsWith(ctx, tx, userID)
		if existsErr != nil {
			return PasswordCredential{}, 0, existsErr
		}
		if exists {
			return PasswordCredential{}, 0, ErrConflict
		}
		return PasswordCredential{}, 0, ErrNotFound
	}
	if err != nil {
		return PasswordCredential{}, 0, mapWriteError("replace password in transaction", err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE auth_sessions
		SET revoked_at = GREATEST($2, created_at)
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID, revokedAt)
	if err != nil {
		return PasswordCredential{}, 0, mapWriteError("revoke sessions after password replacement", err)
	}
	revokedCount, err := result.RowsAffected()
	if err != nil {
		return PasswordCredential{}, 0, fmt.Errorf("count password-change revocations: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PasswordCredential{}, 0, fmt.Errorf("commit password replacement transaction: %w", err)
	}
	return credential, revokedCount, nil
}

// UpdateStatusAndRevokeSessions applies the optimistic User status update and,
// for non-active states, revokes refresh sessions in the same transaction.
func (r *Repository) UpdateStatusAndRevokeSessions(
	ctx context.Context,
	userID string,
	status Status,
	expectedLockVersion int64,
	at time.Time,
) (User, int64, error) {
	if userID == "" || expectedLockVersion < 1 || !validStatus(status) || at.IsZero() {
		return User{}, 0, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, 0, fmt.Errorf("begin user status transaction: %w", err)
	}
	defer tx.Rollback()
	if err := lockPlatformAdminInvariant(ctx, tx); err != nil {
		return User{}, 0, err
	}
	current, err := lockUserForSecurityMutation(ctx, tx, userID, expectedLockVersion)
	if err != nil {
		return User{}, 0, err
	}
	if current.Status == StatusActive && current.PlatformRole == PlatformRoleAdmin && status != StatusActive {
		if err := ensureAnotherActivePlatformAdmin(ctx, tx, userID); err != nil {
			return User{}, 0, err
		}
	}
	user, err := scanUser(tx.QueryRowContext(ctx, `
		UPDATE users
		SET status = $2,
			updated_at = clock_timestamp(),
			lock_version = lock_version + 1
		WHERE id = $1 AND lock_version = $3
		RETURNING `+userColumns,
		userID,
		status,
		expectedLockVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		exists, existsErr := userExistsWith(ctx, tx, userID)
		if existsErr != nil {
			return User{}, 0, existsErr
		}
		if exists {
			return User{}, 0, ErrConflict
		}
		return User{}, 0, ErrNotFound
	}
	if err != nil {
		return User{}, 0, mapWriteError("update user status in transaction", err)
	}

	var revokedCount int64
	if status != StatusActive {
		result, err := tx.ExecContext(ctx, `
			UPDATE auth_sessions
			SET revoked_at = GREATEST($2, created_at)
			WHERE user_id = $1 AND revoked_at IS NULL
		`, userID, at)
		if err != nil {
			return User{}, 0, mapWriteError("revoke sessions after user status change", err)
		}
		revokedCount, err = result.RowsAffected()
		if err != nil {
			return User{}, 0, fmt.Errorf("count status-change revocations: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, 0, fmt.Errorf("commit user status transaction: %w", err)
	}
	return user, revokedCount, nil
}

// UpdatePlatformRoleAndRevokeSessions applies an optimistic platform-role
// change and invalidates every refresh session so existing JWT claims cannot
// be refreshed with obsolete privileges.
func (r *Repository) UpdatePlatformRoleAndRevokeSessions(
	ctx context.Context,
	userID string,
	role PlatformRole,
	expectedLockVersion int64,
	at time.Time,
) (User, int64, error) {
	if userID == "" || expectedLockVersion < 1 || !validPlatformRole(role) || at.IsZero() {
		return User{}, 0, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, 0, fmt.Errorf("begin platform role transaction: %w", err)
	}
	defer tx.Rollback()
	if err := lockPlatformAdminInvariant(ctx, tx); err != nil {
		return User{}, 0, err
	}
	current, err := lockUserForSecurityMutation(ctx, tx, userID, expectedLockVersion)
	if err != nil {
		return User{}, 0, err
	}
	if current.PlatformRole == role {
		return current, 0, nil
	}
	if current.Status == StatusActive && current.PlatformRole == PlatformRoleAdmin && role != PlatformRoleAdmin {
		if err := ensureAnotherActivePlatformAdmin(ctx, tx, userID); err != nil {
			return User{}, 0, err
		}
	}
	user, err := scanUser(tx.QueryRowContext(ctx, `
		UPDATE users
		SET platform_role = $2,
			updated_at = clock_timestamp(),
			lock_version = lock_version + 1
		WHERE id = $1 AND lock_version = $3
		RETURNING `+userColumns,
		userID, role, expectedLockVersion,
	))
	if err != nil {
		return User{}, 0, mapWriteError("update platform role in transaction", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE auth_sessions
		SET revoked_at = GREATEST($2, created_at)
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID, at)
	if err != nil {
		return User{}, 0, mapWriteError("revoke sessions after platform role change", err)
	}
	revokedCount, err := result.RowsAffected()
	if err != nil {
		return User{}, 0, fmt.Errorf("count platform-role revocations: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, 0, fmt.Errorf("commit platform role transaction: %w", err)
	}
	return user, revokedCount, nil
}

func lockPlatformAdminInvariant(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended('actweave.identity.active-platform-admin', 0))
	`); err != nil {
		return fmt.Errorf("lock active platform administrator invariant: %w", err)
	}
	return nil
}

func lockUserForSecurityMutation(
	ctx context.Context,
	tx *sql.Tx,
	userID string,
	expectedLockVersion int64,
) (User, error) {
	user, err := scanUser(tx.QueryRowContext(ctx, `
		SELECT `+userColumns+`
		FROM users
		WHERE id = $1
		FOR UPDATE
	`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("lock user for security mutation: %w", err)
	}
	if user.LockVersion != expectedLockVersion {
		return User{}, ErrConflict
	}
	return user, nil
}

func ensureAnotherActivePlatformAdmin(ctx context.Context, tx *sql.Tx, excludedUserID string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM users
			WHERE id <> $1 AND status = 'ACTIVE' AND platform_role = 'PLATFORM_ADMIN'
		)
	`, excludedUserID).Scan(&exists); err != nil {
		return fmt.Errorf("check active platform administrator invariant: %w", err)
	}
	if !exists {
		return ErrLastPlatformAdmin
	}
	return nil
}

// UnlockUser clears both a permanent LOCKED status and temporary password
// failure state atomically. DISABLED users are not eligible for this command.
func (r *Repository) UnlockUser(
	ctx context.Context,
	userID string,
	expectedLockVersion int64,
	at time.Time,
) (User, PasswordCredential, error) {
	if userID == "" || expectedLockVersion < 1 || at.IsZero() {
		return User{}, PasswordCredential{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, PasswordCredential{}, fmt.Errorf("begin user unlock transaction: %w", err)
	}
	defer tx.Rollback()
	user, err := scanUser(tx.QueryRowContext(ctx, `
		UPDATE users
		SET status = 'ACTIVE',
			updated_at = clock_timestamp(),
			lock_version = lock_version + 1
		WHERE id = $1
		  AND lock_version = $2
		  AND status IN ('ACTIVE', 'LOCKED')
		RETURNING `+userColumns,
		userID,
		expectedLockVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		existing, readErr := scanUser(tx.QueryRowContext(ctx, `
			SELECT `+userColumns+`
			FROM users
			WHERE id = $1
		`, userID))
		if errors.Is(readErr, sql.ErrNoRows) {
			return User{}, PasswordCredential{}, ErrNotFound
		}
		if readErr != nil {
			return User{}, PasswordCredential{}, fmt.Errorf("read user during unlock: %w", readErr)
		}
		if existing.Status == StatusDisabled {
			return User{}, PasswordCredential{}, ErrInvalid
		}
		return User{}, PasswordCredential{}, ErrConflict
	}
	if err != nil {
		return User{}, PasswordCredential{}, mapWriteError("unlock user status", err)
	}
	credential, err := scanPasswordCredential(tx.QueryRowContext(ctx, `
		UPDATE user_credentials
		SET failed_attempts = 0,
			locked_until = NULL
		WHERE user_id = $1
		RETURNING
			user_id,
			password_hash,
			password_algo,
			password_changed_at,
			failed_attempts,
			locked_until,
			must_change_password
	`, userID))
	if err != nil {
		return User{}, PasswordCredential{}, mapWriteError("clear credential during unlock", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, PasswordCredential{}, fmt.Errorf("commit user unlock transaction: %w", err)
	}
	return user, credential, nil
}

type existsQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func credentialExistsWith(ctx context.Context, queryer existsQuerier, userID string) (bool, error) {
	var exists bool
	if err := queryer.QueryRowContext(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM user_credentials WHERE user_id = $1)`,
		userID,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check credential existence in transaction: %w", err)
	}
	return exists, nil
}

func userExistsWith(ctx context.Context, queryer existsQuerier, userID string) (bool, error) {
	var exists bool
	if err := queryer.QueryRowContext(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`,
		userID,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check user existence in transaction: %w", err)
	}
	return exists, nil
}
