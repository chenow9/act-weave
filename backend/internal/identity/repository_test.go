package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
)

const (
	repositoryUserID       = "018f1f2e-7b5a-7c3d-8e9f-2234567890ab"
	repositorySecondUserID = "018f1f2e-7b5a-7c3d-8e9f-2234567890ac"
	initialPasswordHash    = "$argon2id$v=19$m=65536,t=3,p=2$initial"
	replacedPasswordHash   = "$argon2id$v=19$m=65536,t=3,p=2$replaced"
)

func TestUserRepositoryCreatesAndReadsWithoutPasswordHash(t *testing.T) {
	repository, db := newRepositoryTest(t)
	ctx := context.Background()
	email := "owner@example.com"
	avatar := "https://example.com/avatar.png"

	created, err := repository.CreateLocalUser(ctx, NewLocalUser{
		ID:                repositoryUserID,
		Username:          "Workspace.Owner",
		Email:             &email,
		DisplayName:       "Workspace Owner",
		AvatarURL:         &avatar,
		PlatformRole:      PlatformRoleAdmin,
		PasswordHash:      initialPasswordHash,
		PasswordAlgorithm: "ARGON2ID",
	})
	if err != nil {
		t.Fatalf("create local user: %v", err)
	}
	if created.LockVersion != 1 || created.Status != StatusActive || created.Locale != "zh-CN" {
		t.Fatalf("unexpected created user defaults: %+v", created)
	}

	byID, err := repository.GetUserByID(ctx, repositoryUserID)
	if err != nil {
		t.Fatalf("get user by id: %v", err)
	}
	byUsername, err := repository.GetUserByUsername(ctx, "workspace.owner")
	if err != nil {
		t.Fatalf("get user by case-insensitive username: %v", err)
	}
	if !reflect.DeepEqual(byID, byUsername) || !reflect.DeepEqual(byID, created) {
		t.Fatalf("user row mapping mismatch: created=%+v byID=%+v byUsername=%+v", created, byID, byUsername)
	}
	usernames, err := repository.UsernamesByIDs(ctx, []string{repositoryUserID, repositorySecondUserID, repositoryUserID, ""})
	if err != nil {
		t.Fatalf("resolve usernames by ids: %v", err)
	}
	if !reflect.DeepEqual(usernames, map[string]string{repositoryUserID: "Workspace.Owner"}) {
		t.Fatalf("unexpected username resolution: %+v", usernames)
	}

	encoded, err := json.Marshal(created)
	if err != nil {
		t.Fatalf("marshal user DTO: %v", err)
	}
	if strings.Contains(string(encoded), initialPasswordHash) || strings.Contains(string(encoded), "Password") {
		t.Fatalf("user DTO exposed credential data: %s", encoded)
	}

	var storedHash string
	if err := db.QueryRow(
		`SELECT password_hash FROM user_credentials WHERE user_id = $1`,
		repositoryUserID,
	).Scan(&storedHash); err != nil {
		t.Fatalf("read stored credential hash: %v", err)
	}
	if storedHash != initialPasswordHash {
		t.Fatalf("unexpected stored credential hash %q", storedHash)
	}
}

func TestUserRepositoryDetectsConcurrentStatusUpdate(t *testing.T) {
	repository, _ := newRepositoryTest(t)
	ctx := context.Background()
	created := createRepositoryUser(t, repository, repositoryUserID, "concurrent.user")

	updated, err := repository.UpdateStatus(ctx, created.ID, StatusLocked, created.LockVersion)
	if err != nil {
		t.Fatalf("update user status: %v", err)
	}
	if updated.Status != StatusLocked || updated.LockVersion != created.LockVersion+1 {
		t.Fatalf("unexpected updated user: %+v", updated)
	}
	if _, err := repository.UpdateStatus(ctx, created.ID, StatusDisabled, created.LockVersion); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale update conflict, got %v", err)
	}

	stored, err := repository.GetUserByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get user after conflict: %v", err)
	}
	if stored.Status != StatusLocked || stored.LockVersion != updated.LockVersion {
		t.Fatalf("stale writer changed stored user: %+v", stored)
	}
}

func TestUserRepositoryReplacesCredentialAndClearsLockout(t *testing.T) {
	repository, db := newRepositoryTest(t)
	ctx := context.Background()
	createRepositoryUser(t, repository, repositoryUserID, "password.user")
	if _, err := db.Exec(`
		UPDATE user_credentials
		SET failed_attempts = 4,
			locked_until = CURRENT_TIMESTAMP + INTERVAL '10 minutes'
		WHERE user_id = $1
	`, repositoryUserID); err != nil {
		t.Fatalf("set credential lockout fixture: %v", err)
	}
	before, err := repository.GetPasswordCredential(ctx, repositoryUserID)
	if err != nil {
		t.Fatalf("get credential before replacement: %v", err)
	}

	changedAt := time.Now().UTC().Truncate(time.Microsecond)
	replaced, err := repository.ReplacePasswordCredential(ctx, repositoryUserID, CredentialReplacement{
		PasswordHash:              replacedPasswordHash,
		PasswordAlgorithm:         "ARGON2ID",
		PasswordChangedAt:         changedAt,
		ExpectedPasswordChangedAt: before.PasswordChangedAt,
		MustChangePassword:        true,
	})
	if err != nil {
		t.Fatalf("replace password credential: %v", err)
	}
	if replaced.PasswordHash != replacedPasswordHash || replaced.FailedAttempts != 0 ||
		replaced.LockedUntil != nil || !replaced.MustChangePassword {
		t.Fatalf("unexpected replaced credential: %+v", replaced)
	}

	readBack, err := repository.GetPasswordCredential(ctx, repositoryUserID)
	if err != nil {
		t.Fatalf("get replaced credential: %v", err)
	}
	if readBack.PasswordHash != replacedPasswordHash || readBack.FailedAttempts != 0 || readBack.LockedUntil != nil {
		t.Fatalf("replacement did not persist: %+v", readBack)
	}
	if _, err := repository.ReplacePasswordCredential(ctx, repositoryUserID, CredentialReplacement{
		PasswordHash:              initialPasswordHash,
		PasswordAlgorithm:         "ARGON2ID",
		ExpectedPasswordChangedAt: before.PasswordChangedAt,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale credential replacement conflict, got %v", err)
	}
}

func TestUserRepositoryMapsConflictsAndRollsBackCredential(t *testing.T) {
	repository, db := newRepositoryTest(t)
	ctx := context.Background()
	createRepositoryUser(t, repository, repositoryUserID, "duplicate.user")

	_, err := repository.CreateLocalUser(ctx, NewLocalUser{
		ID:                repositorySecondUserID,
		Username:          "DUPLICATE.USER",
		DisplayName:       "Duplicate User",
		PasswordHash:      initialPasswordHash,
		PasswordAlgorithm: "ARGON2ID",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected duplicate username conflict, got %v", err)
	}
	var credentialCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM user_credentials WHERE user_id = $1`,
		repositorySecondUserID,
	).Scan(&credentialCount); err != nil {
		t.Fatalf("count rolled-back credential: %v", err)
	}
	if credentialCount != 0 {
		t.Fatalf("expected failed user transaction to leave no credential, got %d", credentialCount)
	}

	if _, err := repository.ReplacePasswordCredential(ctx, repositorySecondUserID, CredentialReplacement{
		PasswordHash:              replacedPasswordHash,
		PasswordAlgorithm:         "ARGON2ID",
		ExpectedPasswordChangedAt: time.Now().UTC(),
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected missing credential error, got %v", err)
	}
}

func newRepositoryTest(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 21 || version.Dirty {
		t.Fatalf("expected clean repository migration version 4, got %+v", version)
	}
	db := testDatabase.Open(t)
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatalf("create identity repository: %v", err)
	}
	return repository, db
}

func createRepositoryUser(t *testing.T, repository *Repository, id string, username string) User {
	t.Helper()
	user, err := repository.CreateLocalUser(context.Background(), NewLocalUser{
		ID:                id,
		Username:          username,
		DisplayName:       "Repository User",
		PasswordHash:      initialPasswordHash,
		PasswordAlgorithm: "ARGON2ID",
	})
	if err != nil {
		t.Fatalf("create repository user: %v", err)
	}
	return user
}
