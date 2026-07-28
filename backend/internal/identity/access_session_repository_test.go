package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
)

const (
	accessSessionUserID    = "018f1f2e-7b5a-7c3d-8e9f-6234567890ab"
	accessSessionOtherID   = "018f1f2e-7b5a-7c3d-8e9f-6234567890ac"
	accessSessionSessionID = "018f1f2e-7b5a-7c3d-8e9f-6234567890ad"
	accessSessionOtherSID  = "018f1f2e-7b5a-7c3d-8e9f-6234567890ae"
	accessSessionHash      = "$test$refresh-hash-not-a-secret"
)

func TestResolveAccessSessionStateValidProjection(t *testing.T) {
	repository, db := newAccessSessionRepositoryTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	createAccessSessionUser(t, repository, accessSessionUserID, "access.state", PlatformRoleAdmin, true)
	insertAuthSession(t, db, accessSessionSessionID, accessSessionUserID, now.Add(time.Hour), nil)

	state, err := repository.ResolveAccessSessionState(ctx, accessSessionUserID, accessSessionSessionID)
	if err != nil {
		t.Fatalf("resolve access session state: %v", err)
	}
	if state.SessionID != accessSessionSessionID ||
		state.UserID != accessSessionUserID ||
		state.Username != "access.state" ||
		state.UserStatus != StatusActive ||
		state.PlatformRole != PlatformRoleAdmin ||
		!state.MustChangePassword ||
		state.SessionRevokedAt != nil ||
		state.LockedUntil != nil {
		t.Fatalf("unexpected projection: %+v", state)
	}
	if !state.SessionExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("unexpected expires_at: got %v want %v", state.SessionExpiresAt, now.Add(time.Hour))
	}

	// Projection must never serialize credential material (hash / token text).
	// Field name MustChangePassword is intentional and not a secret.
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal projection: %v", err)
	}
	text := string(encoded)
	for _, forbidden := range []string{
		accessSessionHash, "password_hash", "PasswordHash", "refresh_token",
		"BEGIN PRIVATE", "Bearer ",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("projection leaked sensitive token %q: %s", forbidden, text)
		}
	}
	// Struct fields must stay limited to the narrow security projection.
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal projection: %v", err)
	}
	allowed := map[string]struct{}{
		"SessionID": {}, "UserID": {}, "SessionExpiresAt": {}, "SessionRevokedAt": {},
		"Username": {}, "UserStatus": {}, "PlatformRole": {}, "LockedUntil": {},
		"MustChangePassword": {},
	}
	for key := range decoded {
		if _, ok := allowed[key]; !ok {
			t.Fatalf("unexpected projection field %q in %s", key, text)
		}
	}
}

func TestResolveAccessSessionStateMissingAndSubjectMismatch(t *testing.T) {
	repository, db := newAccessSessionRepositoryTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	createAccessSessionUser(t, repository, accessSessionUserID, "access.owner", PlatformRoleUser, false)
	createAccessSessionUser(t, repository, accessSessionOtherID, "access.other", PlatformRoleUser, false)
	insertAuthSession(t, db, accessSessionSessionID, accessSessionUserID, now.Add(time.Hour), nil)

	cases := []struct {
		name      string
		subject   string
		sessionID string
	}{
		{"empty subject", "", accessSessionSessionID},
		{"empty session", accessSessionUserID, ""},
		{"missing session", accessSessionUserID, accessSessionOtherSID},
		{"subject mismatch", accessSessionOtherID, accessSessionSessionID},
		{"both missing", accessSessionOtherID, accessSessionOtherSID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := repository.ResolveAccessSessionState(ctx, tc.subject, tc.sessionID)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("expected ErrNotFound, got %v", err)
			}
		})
	}
}

func TestResolveAccessSessionStateFieldVariants(t *testing.T) {
	repository, db := newAccessSessionRepositoryTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	createAccessSessionUser(t, repository, accessSessionUserID, "access.fields", PlatformRoleUser, false)
	insertAuthSession(t, db, accessSessionSessionID, accessSessionUserID, now.Add(2*time.Hour), nil)

	// revoked session still returns facts (policy is authn's job).
	// revoked_at must satisfy auth_sessions_revocation_check (>= created_at).
	revokedAt := now.Add(time.Minute)
	if _, err := db.Exec(
		`UPDATE auth_sessions SET revoked_at = GREATEST($2, created_at) WHERE id = $1`,
		accessSessionSessionID, revokedAt,
	); err != nil {
		t.Fatalf("mark revoked: %v", err)
	}
	state, err := repository.ResolveAccessSessionState(ctx, accessSessionUserID, accessSessionSessionID)
	if err != nil {
		t.Fatalf("resolve revoked: %v", err)
	}
	if state.SessionRevokedAt == nil {
		t.Fatalf("expected revoked_at fact, got %+v", state)
	}

	// expired expires_at still returned as fact (policy is authn's job).
	// Satisfy auth_sessions_expiry_check: expires_at > created_at, while both are past wall clock.
	expiredSessionID := accessSessionOtherSID
	createdPast := now.Add(-2 * time.Hour)
	expiredAt := now.Add(-time.Hour)
	if _, err := db.Exec(`
		INSERT INTO auth_sessions (id, user_id, refresh_token_hash, expires_at, created_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $5)
	`, expiredSessionID, accessSessionUserID, accessSessionHash+"-expired", expiredAt, createdPast); err != nil {
		t.Fatalf("insert expired session: %v", err)
	}
	state, err = repository.ResolveAccessSessionState(ctx, accessSessionUserID, expiredSessionID)
	if err != nil {
		t.Fatalf("resolve expired: %v", err)
	}
	if !state.SessionExpiresAt.Equal(expiredAt) || state.SessionRevokedAt != nil {
		t.Fatalf("expected expired fact without revoke: %+v", state)
	}

	// user status LOCKED / DISABLED.
	for _, status := range []Status{StatusLocked, StatusDisabled, StatusActive} {
		if _, err := db.Exec(`UPDATE users SET status = $2 WHERE id = $1`, accessSessionUserID, status); err != nil {
			t.Fatalf("set status %s: %v", status, err)
		}
		state, err = repository.ResolveAccessSessionState(ctx, accessSessionUserID, accessSessionSessionID)
		if err != nil {
			t.Fatalf("resolve status %s: %v", status, err)
		}
		if state.UserStatus != status {
			t.Fatalf("status=%s got %+v", status, state)
		}
	}

	// future / past / null locked_until.
	future := now.Add(30 * time.Minute)
	if _, err := db.Exec(
		`UPDATE user_credentials SET locked_until = $2 WHERE user_id = $1`,
		accessSessionUserID, future,
	); err != nil {
		t.Fatalf("set future locked_until: %v", err)
	}
	state, err = repository.ResolveAccessSessionState(ctx, accessSessionUserID, accessSessionSessionID)
	if err != nil || state.LockedUntil == nil || !state.LockedUntil.Equal(future) {
		t.Fatalf("future locked_until: state=%+v err=%v", state, err)
	}
	past := now.Add(-30 * time.Minute)
	if _, err := db.Exec(
		`UPDATE user_credentials SET locked_until = $2 WHERE user_id = $1`,
		accessSessionUserID, past,
	); err != nil {
		t.Fatalf("set past locked_until: %v", err)
	}
	state, err = repository.ResolveAccessSessionState(ctx, accessSessionUserID, accessSessionSessionID)
	if err != nil || state.LockedUntil == nil || !state.LockedUntil.Equal(past) {
		t.Fatalf("past locked_until: state=%+v err=%v", state, err)
	}
	if _, err := db.Exec(
		`UPDATE user_credentials SET locked_until = NULL, must_change_password = TRUE WHERE user_id = $1`,
		accessSessionUserID,
	); err != nil {
		t.Fatalf("clear locked_until: %v", err)
	}
	state, err = repository.ResolveAccessSessionState(ctx, accessSessionUserID, accessSessionSessionID)
	if err != nil || state.LockedUntil != nil || !state.MustChangePassword {
		t.Fatalf("null locked_until / must-change: state=%+v err=%v", state, err)
	}

	// role + username current values.
	if _, err := db.Exec(
		`UPDATE users SET platform_role = $2, username = $3 WHERE id = $1`,
		accessSessionUserID, PlatformRoleAdmin, "access.renamed",
	); err != nil {
		t.Fatalf("update role/username: %v", err)
	}
	state, err = repository.ResolveAccessSessionState(ctx, accessSessionUserID, accessSessionSessionID)
	if err != nil || state.PlatformRole != PlatformRoleAdmin || state.Username != "access.renamed" {
		t.Fatalf("role/username: state=%+v err=%v", state, err)
	}
}

func TestResolveAccessSessionStateMissingCredentialIsNotFound(t *testing.T) {
	repository, db := newAccessSessionRepositoryTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	createAccessSessionUser(t, repository, accessSessionUserID, "access.nocred", PlatformRoleUser, false)
	insertAuthSession(t, db, accessSessionSessionID, accessSessionUserID, now.Add(time.Hour), nil)
	if _, err := db.Exec(`DELETE FROM user_credentials WHERE user_id = $1`, accessSessionUserID); err != nil {
		t.Fatalf("delete credential: %v", err)
	}
	_, err := repository.ResolveAccessSessionState(ctx, accessSessionUserID, accessSessionSessionID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected credential missing → ErrNotFound, got %v", err)
	}
}

func TestResolveAccessSessionStateInfrastructureErrorNotNotFound(t *testing.T) {
	// Closed DB must surface a wrapped infrastructure error, not ErrNotFound.
	db := openClosedAccessSessionDB(t)
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	_, resolveErr := repository.ResolveAccessSessionState(
		context.Background(),
		accessSessionUserID,
		accessSessionSessionID,
	)
	if resolveErr == nil {
		t.Fatal("expected infrastructure error")
	}
	if errors.Is(resolveErr, ErrNotFound) {
		t.Fatalf("infra error collapsed to ErrNotFound: %v", resolveErr)
	}
	if !strings.Contains(resolveErr.Error(), "resolve access session state") {
		t.Fatalf("expected wrapped operation error, got %v", resolveErr)
	}
}

func newAccessSessionRepositoryTest(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatalf("create identity repository: %v", err)
	}
	return repository, db
}

func createAccessSessionUser(
	t *testing.T,
	repository *Repository,
	id string,
	username string,
	role PlatformRole,
	mustChange bool,
) {
	t.Helper()
	if _, err := repository.CreateLocalUser(context.Background(), NewLocalUser{
		ID:                 id,
		Username:           username,
		DisplayName:        "Access Session User",
		PlatformRole:       role,
		PasswordHash:       "$argon2id$v=19$m=65536,t=3,p=2$access-session-fixture",
		PasswordAlgorithm:  "ARGON2ID",
		MustChangePassword: mustChange,
	}); err != nil {
		t.Fatalf("create access session user: %v", err)
	}
}

func insertAuthSession(
	t *testing.T,
	db *sql.DB,
	sessionID string,
	userID string,
	expiresAt time.Time,
	revokedAt *time.Time,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO auth_sessions (id, user_id, refresh_token_hash, expires_at, revoked_at)
		VALUES ($1, $2, $3, $4, $5)
	`, sessionID, userID, accessSessionHash, expiresAt, revokedAt); err != nil {
		t.Fatalf("insert auth session: %v", err)
	}
}

func openClosedAccessSessionDB(t *testing.T) *sql.DB {
	t.Helper()
	// Open a real postgres handle then close it so QueryRowContext fails with a
	// driver/infrastructure error rather than sql.ErrNoRows.
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	return db
}
