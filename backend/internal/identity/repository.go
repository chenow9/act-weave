package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

var (
	ErrNotFound          = errors.New("identity record not found")
	ErrConflict          = errors.New("identity record conflict")
	ErrInvalid           = errors.New("invalid identity record")
	ErrLastPlatformAdmin = errors.New("last active platform administrator cannot be removed")
)

const userColumns = `
	id,
	username::TEXT,
	email::TEXT,
	display_name,
	avatar_url,
	status,
	platform_role,
	locale,
	timezone,
	last_login_at,
	created_at,
	updated_at,
	lock_version
`

type rowScanner interface {
	Scan(dest ...any) error
}

type localUserTransaction interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// Repository performs row-oriented identity persistence. It does not own the
// supplied connection pool.
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("identity repository database is required")
	}
	return &Repository{db: db}, nil
}

// CreateLocalUser atomically inserts the user and password credential so a
// login-capable local user cannot be observed without a credential.
func (r *Repository) CreateLocalUser(ctx context.Context, input NewLocalUser) (User, error) {
	input = defaultNewLocalUser(input)
	if err := validateNewLocalUser(input); err != nil {
		return User{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin local user transaction: %w", err)
	}
	defer tx.Rollback()
	user, err := insertLocalUser(ctx, tx, input)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit local user transaction: %w", err)
	}
	return user, nil
}

// BootstrapFirstAdmin creates exactly one initial platform administrator only
// when the identity store is empty. The transaction-level advisory lock makes
// concurrent process startup deterministic without introducing seed tables.
func (r *Repository) BootstrapFirstAdmin(
	ctx context.Context,
	input NewLocalUser,
) (User, bool, error) {
	input = defaultNewLocalUser(input)
	if input.PlatformRole != PlatformRoleAdmin || input.Status != StatusActive {
		return User{}, false, ErrInvalid
	}
	if err := validateNewLocalUser(input); err != nil {
		return User{}, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, false, fmt.Errorf("begin administrator bootstrap transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('actweave.identity.bootstrap-admin', 0))`); err != nil {
		return User{}, false, fmt.Errorf("lock administrator bootstrap: %w", err)
	}
	var userCount int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		return User{}, false, fmt.Errorf("inspect administrator bootstrap state: %w", err)
	}
	if userCount != 0 {
		return User{}, false, nil
	}
	user, err := insertLocalUser(ctx, tx, input)
	if err != nil {
		return User{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, false, fmt.Errorf("commit administrator bootstrap: %w", err)
	}
	return user, true, nil
}

func insertLocalUser(ctx context.Context, tx localUserTransaction, input NewLocalUser) (User, error) {
	user, err := scanUser(tx.QueryRowContext(ctx, `
		INSERT INTO users (
			id, username, email, display_name, avatar_url,
			status, platform_role, locale, timezone
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+userColumns,
		input.ID,
		input.Username,
		input.Email,
		input.DisplayName,
		input.AvatarURL,
		input.Status,
		input.PlatformRole,
		input.Locale,
		input.Timezone,
	))
	if err != nil {
		return User{}, mapWriteError("create local user", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_credentials (
			user_id, password_hash, password_algo, password_changed_at,
			must_change_password
		) VALUES ($1, $2, $3, $4, $5)
	`,
		input.ID,
		input.PasswordHash,
		input.PasswordAlgorithm,
		input.PasswordChangedAt,
		input.MustChangePassword,
	); err != nil {
		return User{}, mapWriteError("create local credential", err)
	}
	return user, nil
}

func (r *Repository) GetUserByID(ctx context.Context, id string) (User, error) {
	user, err := scanUser(r.db.QueryRowContext(ctx, `
		SELECT `+userColumns+`
		FROM users
		WHERE id = $1
	`, id))
	return user, mapReadError("get user by id", err)
}

func (r *Repository) GetUserByUsername(ctx context.Context, username string) (User, error) {
	user, err := scanUser(r.db.QueryRowContext(ctx, `
		SELECT `+userColumns+`
		FROM users
		WHERE username = $1
	`, username))
	return user, mapReadError("get user by username", err)
}

// UsernamesByIDs resolves user-facing audit actor labels without exposing the
// rest of the identity record. Missing users are intentionally omitted so
// callers can fall back to the persisted actor ID for historical records.
func (r *Repository) UsernamesByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return map[string]string{}, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id::TEXT, username::TEXT
		FROM users
		WHERE id = ANY($1::uuid[])
	`, pq.Array(unique))
	if err != nil {
		return nil, fmt.Errorf("resolve usernames by ids: %w", err)
	}
	defer rows.Close()

	usernames := make(map[string]string, len(unique))
	for rows.Next() {
		var id, username string
		if err := rows.Scan(&id, &username); err != nil {
			return nil, fmt.Errorf("scan username resolution: %w", err)
		}
		usernames[id] = username
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resolve usernames by ids: %w", err)
	}
	return usernames, nil
}

func (r *Repository) ListUsers(ctx context.Context, limit int) ([]User, error) {
	if limit < 1 || limit > 500 {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+userColumns+`
		FROM users ORDER BY created_at DESC,id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	values := make([]User, 0)
	for rows.Next() {
		value, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user list: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *Repository) SearchUsers(ctx context.Context, query UserListQuery) (UserPage, error) {
	query.Query = strings.TrimSpace(query.Query)
	if query.Limit < 1 || query.Limit > 100 || query.Offset < 0 || len(query.Query) > 200 ||
		(query.Status != nil && !validStatus(*query.Status)) ||
		(query.PlatformRole != nil && !validPlatformRole(*query.PlatformRole)) {
		return UserPage{}, ErrInvalid
	}
	status, platformRole := optionalStatus(query.Status), optionalPlatformRole(query.PlatformRole)
	var total int64
	// Search username / display_name / email, and also full user id (audit deep-links).
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM users
		WHERE ($1 = '' OR strpos(lower(
			username::TEXT || ' ' || display_name || ' ' || COALESCE(email::TEXT, '') || ' ' || id::TEXT
		), lower($1)) > 0)
		  AND ($2 = '' OR status = $2)
		  AND ($3 = '' OR platform_role = $3)
	`, query.Query, status, platformRole).Scan(&total); err != nil {
		return UserPage{}, fmt.Errorf("count filtered users: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+userColumns+`
		FROM users
		WHERE ($1 = '' OR strpos(lower(
			username::TEXT || ' ' || display_name || ' ' || COALESCE(email::TEXT, '') || ' ' || id::TEXT
		), lower($1)) > 0)
		  AND ($2 = '' OR status = $2)
		  AND ($3 = '' OR platform_role = $3)
		ORDER BY created_at DESC, id
		LIMIT $4 OFFSET $5
	`, query.Query, status, platformRole, query.Limit, query.Offset)
	if err != nil {
		return UserPage{}, fmt.Errorf("search users: %w", err)
	}
	defer rows.Close()
	items := make([]User, 0, query.Limit)
	for rows.Next() {
		value, err := scanUser(rows)
		if err != nil {
			return UserPage{}, fmt.Errorf("scan filtered user: %w", err)
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		return UserPage{}, fmt.Errorf("iterate filtered users: %w", err)
	}
	return UserPage{Items: items, Total: total}, nil
}

func (r *Repository) ListUserWorkspaceMemberships(
	ctx context.Context,
	userID string,
	includeDisabled bool,
) ([]UserWorkspaceMembership, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			w.id, w.slug, w.display_name, w.status,
			m.role, m.joined_at, m.disabled_at
		FROM workspace_members AS m
		JOIN workspaces AS w ON w.id = m.workspace_id
		WHERE m.user_id = $1
		  AND w.deleted_at IS NULL
		  AND ($2 OR m.disabled_at IS NULL)
		ORDER BY w.display_name, w.id
	`, userID, includeDisabled)
	if err != nil {
		return nil, fmt.Errorf("list user workspace memberships: %w", err)
	}
	defer rows.Close()
	items := make([]UserWorkspaceMembership, 0)
	for rows.Next() {
		var item UserWorkspaceMembership
		if err := rows.Scan(
			&item.WorkspaceID, &item.WorkspaceSlug, &item.WorkspaceDisplayName,
			&item.WorkspaceStatus, &item.Role, &item.JoinedAt, &item.DisabledAt,
		); err != nil {
			return nil, fmt.Errorf("scan user workspace membership: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user workspace memberships: %w", err)
	}
	return items, nil
}

func optionalStatus(value *Status) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func optionalPlatformRole(value *PlatformRole) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func (r *Repository) UpdateUserProfile(
	ctx context.Context,
	id string,
	input UserProfileUpdate,
) (User, error) {
	if id == "" || input.ExpectedLockVersion < 1 ||
		(input.DisplayName == nil && input.Email == nil && input.AvatarURL == nil &&
			input.Locale == nil && input.Timezone == nil) ||
		!validOptionalProfileString(input.DisplayName, false) ||
		!validOptionalProfileString(input.Email, false) ||
		!validOptionalProfileString(input.AvatarURL, true) ||
		!validOptionalProfileString(input.Locale, false) ||
		!validOptionalProfileString(input.Timezone, false) {
		return User{}, ErrInvalid
	}
	value, err := scanUser(r.db.QueryRowContext(ctx, `
		UPDATE users SET
			display_name=COALESCE($2,display_name),
			email=COALESCE($3,email),avatar_url=COALESCE($4,avatar_url),
			locale=COALESCE($5,locale),timezone=COALESCE($6,timezone),
			updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE id=$1 AND lock_version=$7
		RETURNING `+userColumns,
		id, input.DisplayName, input.Email, input.AvatarURL, input.Locale, input.Timezone,
		input.ExpectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		exists, existsErr := r.userExists(ctx, id)
		if existsErr != nil {
			return User{}, existsErr
		}
		if exists {
			return User{}, ErrConflict
		}
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, mapWriteError("update user profile", err)
	}
	return value, nil
}

func validOptionalProfileString(value *string, allowEmpty bool) bool {
	if value == nil {
		return true
	}
	length := len(*value)
	return (allowEmpty || length > 0) && length <= 1024
}

// UpdateStatus uses lock_version as an optimistic compare-and-swap. A stale
// version returns ErrConflict rather than silently overwriting another writer.
func (r *Repository) UpdateStatus(
	ctx context.Context,
	id string,
	status Status,
	expectedLockVersion int64,
) (User, error) {
	if id == "" || expectedLockVersion < 1 || !validStatus(status) {
		return User{}, ErrInvalid
	}
	user, err := scanUser(r.db.QueryRowContext(ctx, `
		UPDATE users
		SET status = $2,
			updated_at = clock_timestamp(),
			lock_version = lock_version + 1
		WHERE id = $1 AND lock_version = $3
		RETURNING `+userColumns,
		id,
		status,
		expectedLockVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		exists, existsErr := r.userExists(ctx, id)
		if existsErr != nil {
			return User{}, existsErr
		}
		if exists {
			return User{}, ErrConflict
		}
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, mapWriteError("update user status", err)
	}
	return user, nil
}

func (r *Repository) GetPasswordCredential(
	ctx context.Context,
	userID string,
) (PasswordCredential, error) {
	credential, err := scanPasswordCredential(r.db.QueryRowContext(ctx, `
		SELECT
			user_id,
			password_hash,
			password_algo,
			password_changed_at,
			failed_attempts,
			locked_until,
			must_change_password
		FROM user_credentials
		WHERE user_id = $1
	`, userID))
	if err != nil {
		return PasswordCredential{}, mapReadError("get password credential", err)
	}
	return credential, nil
}

// RecordPasswordFailure atomically increments the failure count and applies a
// lock timestamp when the configured threshold is reached.
func (r *Repository) RecordPasswordFailure(
	ctx context.Context,
	userID string,
	at time.Time,
	maxAttempts int,
	lockDuration time.Duration,
) (PasswordCredential, error) {
	if userID == "" || at.IsZero() || maxAttempts < 1 || lockDuration <= 0 {
		return PasswordCredential{}, ErrInvalid
	}
	lockUntil := at.Add(lockDuration)
	credential, err := scanPasswordCredential(r.db.QueryRowContext(ctx, `
		UPDATE user_credentials
		SET failed_attempts = failed_attempts + 1,
			locked_until = CASE
				WHEN failed_attempts + 1 >= $2
					THEN GREATEST(COALESCE(locked_until, $3), $3)
				ELSE locked_until
			END
		WHERE user_id = $1
		RETURNING
			user_id,
			password_hash,
			password_algo,
			password_changed_at,
			failed_attempts,
			locked_until,
			must_change_password
	`, userID, maxAttempts, lockUntil))
	if err != nil {
		return PasswordCredential{}, mapWriteError("record password failure", err)
	}
	return credential, nil
}

// ClearPasswordFailures removes transient lockout state after a successful
// password verification or an explicit administrator unlock.
func (r *Repository) ClearPasswordFailures(
	ctx context.Context,
	userID string,
) (PasswordCredential, error) {
	credential, err := scanPasswordCredential(r.db.QueryRowContext(ctx, `
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
		return PasswordCredential{}, mapWriteError("clear password failures", err)
	}
	return credential, nil
}

// ReplacePasswordCredential resets failed-attempt and lockout state while
// replacing only the credential record; User remains free of password data.
func (r *Repository) ReplacePasswordCredential(
	ctx context.Context,
	userID string,
	replacement CredentialReplacement,
) (PasswordCredential, error) {
	if replacement.PasswordChangedAt.IsZero() {
		replacement.PasswordChangedAt = time.Now().UTC()
	}
	if userID == "" || replacement.PasswordHash == "" || replacement.PasswordAlgorithm == "" ||
		replacement.ExpectedPasswordChangedAt.IsZero() {
		return PasswordCredential{}, ErrInvalid
	}
	var credential PasswordCredential
	err := r.db.QueryRowContext(ctx, `
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
	).Scan(
		&credential.UserID,
		&credential.PasswordHash,
		&credential.PasswordAlgorithm,
		&credential.PasswordChangedAt,
		&credential.FailedAttempts,
		&credential.LockedUntil,
		&credential.MustChangePassword,
	)
	if errors.Is(err, sql.ErrNoRows) {
		exists, existsErr := r.credentialExists(ctx, userID)
		if existsErr != nil {
			return PasswordCredential{}, existsErr
		}
		if exists {
			return PasswordCredential{}, ErrConflict
		}
		return PasswordCredential{}, ErrNotFound
	}
	if err != nil {
		return PasswordCredential{}, mapWriteError("replace password credential", err)
	}
	return credential, nil
}

func (r *Repository) credentialExists(ctx context.Context, userID string) (bool, error) {
	var exists bool
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM user_credentials WHERE user_id = $1)`,
		userID,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check credential existence: %w", err)
	}
	return exists, nil
}

func (r *Repository) userExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`,
		id,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check user existence: %w", err)
	}
	return exists, nil
}

func scanUser(row rowScanner) (User, error) {
	var user User
	err := row.Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.DisplayName,
		&user.AvatarURL,
		&user.Status,
		&user.PlatformRole,
		&user.Locale,
		&user.Timezone,
		&user.LastLoginAt,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LockVersion,
	)
	return user, err
}

func scanPasswordCredential(row rowScanner) (PasswordCredential, error) {
	var credential PasswordCredential
	err := row.Scan(
		&credential.UserID,
		&credential.PasswordHash,
		&credential.PasswordAlgorithm,
		&credential.PasswordChangedAt,
		&credential.FailedAttempts,
		&credential.LockedUntil,
		&credential.MustChangePassword,
	)
	return credential, err
}

func defaultNewLocalUser(input NewLocalUser) NewLocalUser {
	if input.Status == "" {
		input.Status = StatusActive
	}
	if input.PlatformRole == "" {
		input.PlatformRole = PlatformRoleUser
	}
	if input.Locale == "" {
		input.Locale = "zh-CN"
	}
	if input.Timezone == "" {
		input.Timezone = "Asia/Singapore"
	}
	if input.PasswordChangedAt.IsZero() {
		input.PasswordChangedAt = time.Now().UTC()
	}
	return input
}

func validateNewLocalUser(input NewLocalUser) error {
	if input.ID == "" || input.Username == "" || input.DisplayName == "" ||
		input.PasswordHash == "" || input.PasswordAlgorithm == "" ||
		!validStatus(input.Status) || !validPlatformRole(input.PlatformRole) {
		return ErrInvalid
	}
	return nil
}

func validStatus(status Status) bool {
	switch status {
	case StatusActive, StatusLocked, StatusDisabled:
		return true
	default:
		return false
	}
}

func validPlatformRole(role PlatformRole) bool {
	switch role {
	case PlatformRoleUser, PlatformRoleAdmin:
		return true
	default:
		return false
	}
}

func mapReadError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func mapWriteError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var postgresError *pq.Error
	if errors.As(err, &postgresError) {
		switch postgresError.Code.Class() {
		case "22", "23":
			if postgresError.Code == "23505" {
				return fmt.Errorf("%s: %w", operation, ErrConflict)
			}
			return fmt.Errorf("%s: %w", operation, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
