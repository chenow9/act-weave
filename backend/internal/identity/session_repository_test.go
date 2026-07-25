package identity_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/authn"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/identity"
)

const sessionRepositoryUserID = "018f1f2e-7b5a-7c3d-8e9f-4234567890ab"

func TestSessionCreateValidateRotateAndRevoke(t *testing.T) {
	repository, db := newSessionRepositoryTest(t)
	manager := authn.NewRefreshTokenManager()
	issued, err := manager.Issue()
	if err != nil {
		t.Fatalf("issue initial refresh token: %v", err)
	}
	now := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	userAgent := "session repository test"
	ip := "127.0.0.1"
	created, err := repository.CreateAuthSession(context.Background(), identity.NewAuthSession{
		ID:               issued.SessionID,
		UserID:           sessionRepositoryUserID,
		RefreshTokenHash: issued.Hash,
		UserAgent:        &userAgent,
		IP:               &ip,
		ExpiresAt:        now.Add(7 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create auth session: %v", err)
	}
	if created.UserID != sessionRepositoryUserID || created.RevokedAt != nil {
		t.Fatalf("unexpected created auth session: %+v", created)
	}

	var storedHash string
	if err := db.QueryRow(
		`SELECT refresh_token_hash FROM auth_sessions WHERE id = $1`,
		issued.SessionID,
	).Scan(&storedHash); err != nil {
		t.Fatalf("read stored refresh token hash: %v", err)
	}
	if storedHash != issued.Hash || storedHash == issued.Plaintext {
		t.Fatalf("refresh token plaintext was persisted: stored=%q issuedHash=%q", storedHash, issued.Hash)
	}

	if _, err := repository.ValidateRefreshSession(
		context.Background(),
		issued.SessionID,
		issued.Hash,
		now,
	); err != nil {
		t.Fatalf("validate initial refresh session: %v", err)
	}

	replacement, err := manager.Rotate(issued.SessionID)
	if err != nil {
		t.Fatalf("issue replacement refresh token: %v", err)
	}
	if _, err := repository.RotateRefreshToken(
		context.Background(),
		issued.SessionID,
		issued.Hash,
		replacement.Hash,
		now.Add(time.Minute),
	); err != nil {
		t.Fatalf("rotate refresh token: %v", err)
	}
	if _, err := repository.ValidateRefreshSession(
		context.Background(),
		issued.SessionID,
		issued.Hash,
		now.Add(time.Minute),
	); !errors.Is(err, identity.ErrSessionInvalid) {
		t.Fatalf("expected rotated token to be invalid, got %v", err)
	}
	if _, err := repository.ValidateRefreshSession(
		context.Background(),
		issued.SessionID,
		replacement.Hash,
		now.Add(time.Minute),
	); err != nil {
		t.Fatalf("validate replacement refresh token: %v", err)
	}

	revoked, err := repository.RevokeAuthSession(
		context.Background(),
		issued.SessionID,
		now.Add(2*time.Minute),
	)
	if err != nil || revoked.RevokedAt == nil {
		t.Fatalf("revoke auth session: session=%+v err=%v", revoked, err)
	}
	again, err := repository.RevokeAuthSession(
		context.Background(),
		issued.SessionID,
		now.Add(3*time.Minute),
	)
	if err != nil || again.RevokedAt == nil || !again.RevokedAt.Equal(*revoked.RevokedAt) {
		t.Fatalf("idempotent session revoke changed timestamp: first=%+v again=%+v err=%v", revoked, again, err)
	}
	if _, err := repository.ValidateRefreshSession(
		context.Background(),
		issued.SessionID,
		replacement.Hash,
		now.Add(3*time.Minute),
	); !errors.Is(err, identity.ErrSessionInvalid) {
		t.Fatalf("expected revoked session to be invalid, got %v", err)
	}
}

func TestSessionTimestampsTolerateApplicationClockBehindDatabase(t *testing.T) {
	repository, _ := newSessionRepositoryTest(t)
	manager := authn.NewRefreshTokenManager()
	initial, err := manager.Issue()
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.CreateAuthSession(context.Background(), identity.NewAuthSession{
		ID: initial.SessionID, UserID: sessionRepositoryUserID,
		RefreshTokenHash: initial.Hash, ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	behind := created.CreatedAt.Add(-time.Second)
	replacement, err := manager.Rotate(initial.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := repository.RotateRefreshToken(
		context.Background(), initial.SessionID, initial.Hash, replacement.Hash, behind,
	)
	if err != nil {
		t.Fatalf("rotate with lagging application clock: %v", err)
	}
	if rotated.LastSeenAt.Before(rotated.CreatedAt) {
		t.Fatalf("last_seen_at=%s before created_at=%s", rotated.LastSeenAt, rotated.CreatedAt)
	}
	revoked, err := repository.RevokeAuthSession(context.Background(), initial.SessionID, behind)
	if err != nil {
		t.Fatalf("revoke with lagging application clock: %v", err)
	}
	if revoked.RevokedAt == nil || revoked.RevokedAt.Before(revoked.CreatedAt) {
		t.Fatalf("revoked_at=%v before created_at=%s", revoked.RevokedAt, revoked.CreatedAt)
	}
}

func TestSessionRejectsExpiredRefreshToken(t *testing.T) {
	repository, _ := newSessionRepositoryTest(t)
	manager := authn.NewRefreshTokenManager()
	issued, err := manager.Issue()
	if err != nil {
		t.Fatalf("issue refresh token: %v", err)
	}
	createdAt := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	if _, err := repository.CreateAuthSession(context.Background(), identity.NewAuthSession{
		ID:               issued.SessionID,
		UserID:           sessionRepositoryUserID,
		RefreshTokenHash: issued.Hash,
		ExpiresAt:        createdAt.Add(time.Second),
	}); err != nil {
		t.Fatalf("create short session: %v", err)
	}
	if _, err := repository.ValidateRefreshSession(
		context.Background(),
		issued.SessionID,
		issued.Hash,
		createdAt.Add(2*time.Second),
	); !errors.Is(err, identity.ErrSessionInvalid) {
		t.Fatalf("expected expired session error, got %v", err)
	}
}

func TestSessionConcurrentRefreshRotationOnlySucceedsOnce(t *testing.T) {
	repository, _ := newSessionRepositoryTest(t)
	manager := authn.NewRefreshTokenManager()
	initial, err := manager.Issue()
	if err != nil {
		t.Fatalf("issue initial token: %v", err)
	}
	now := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	if _, err := repository.CreateAuthSession(context.Background(), identity.NewAuthSession{
		ID:               initial.SessionID,
		UserID:           sessionRepositoryUserID,
		RefreshTokenHash: initial.Hash,
		ExpiresAt:        now.Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("create concurrent rotation session: %v", err)
	}

	replacements := make([]authn.IssuedRefreshToken, 2)
	for index := range replacements {
		replacements[index], err = manager.Rotate(initial.SessionID)
		if err != nil {
			t.Fatalf("issue concurrent replacement %d: %v", index, err)
		}
	}
	type result struct {
		index int
		err   error
	}
	results := make(chan result, len(replacements))
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	for index := range replacements {
		index := index
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			_, rotateErr := repository.RotateRefreshToken(
				context.Background(),
				initial.SessionID,
				initial.Hash,
				replacements[index].Hash,
				now.Add(time.Minute),
			)
			results <- result{index: index, err: rotateErr}
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)

	successes := 0
	conflicts := 0
	winner := -1
	for rotation := range results {
		switch {
		case rotation.err == nil:
			successes++
			winner = rotation.index
		case errors.Is(rotation.err, identity.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent rotation error: %v", rotation.err)
		}
	}
	if successes != 1 || conflicts != 1 || winner < 0 {
		t.Fatalf("expected one rotation success and conflict, got successes=%d conflicts=%d", successes, conflicts)
	}
	if _, err := repository.ValidateRefreshSession(
		context.Background(),
		initial.SessionID,
		replacements[winner].Hash,
		now.Add(time.Minute),
	); err != nil {
		t.Fatalf("winning replacement token is not valid: %v", err)
	}
}

func newSessionRepositoryTest(t *testing.T) (*identity.Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateTo(t, 4)
	db := testDatabase.Open(t)
	repository, err := identity.NewRepository(db)
	if err != nil {
		t.Fatalf("create identity repository: %v", err)
	}
	if _, err := repository.CreateLocalUser(context.Background(), identity.NewLocalUser{
		ID:                sessionRepositoryUserID,
		Username:          "session.user",
		DisplayName:       "Session User",
		PasswordHash:      "$argon2id$v=19$m=65536,t=3,p=2$stored",
		PasswordAlgorithm: authn.PasswordAlgorithmArgon2id,
	}); err != nil {
		t.Fatalf("create session repository user: %v", err)
	}
	return repository, db
}
