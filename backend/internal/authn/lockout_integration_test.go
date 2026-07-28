package authn

import (
	"context"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/identity"
)

const lockoutUserID = "018f1f2e-7b5a-7c3d-8e9f-3234567890ab"

func TestLoginLockoutThresholdAndUnlock(t *testing.T) {
	repository := newLockoutRepository(t)
	ctx := context.Background()
	fixedNow := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	lockout, err := newLoginLockout(
		repository,
		LockoutPolicy{MaxFailedAttempts: 3, Duration: 15 * time.Minute},
		func() time.Time { return fixedNow },
	)
	if err != nil {
		t.Fatalf("create login lockout: %v", err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		credential, err := lockout.RecordFailure(ctx, lockoutUserID)
		if err != nil {
			t.Fatalf("record failed attempt %d: %v", attempt, err)
		}
		if credential.FailedAttempts != attempt || lockout.IsLocked(credential) {
			t.Fatalf("unexpected pre-threshold credential: %+v", credential)
		}
	}
	locked, err := lockout.RecordFailure(ctx, lockoutUserID)
	if err != nil {
		t.Fatalf("record threshold failure: %v", err)
	}
	if locked.FailedAttempts != 3 || !lockout.IsLocked(locked) ||
		locked.LockedUntil == nil || !locked.LockedUntil.Equal(fixedNow.Add(15*time.Minute)) {
		t.Fatalf("unexpected locked credential: %+v", locked)
	}

	unlocked, err := lockout.Unlock(ctx, lockoutUserID)
	if err != nil {
		t.Fatalf("unlock credential: %v", err)
	}
	if unlocked.FailedAttempts != 0 || unlocked.LockedUntil != nil || lockout.IsLocked(unlocked) {
		t.Fatalf("unexpected unlocked credential: %+v", unlocked)
	}
}

func TestLoginLockoutConcurrentFailuresAreAtomic(t *testing.T) {
	repository := newLockoutRepository(t)
	fixedNow := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	lockout, err := newLoginLockout(
		repository,
		LockoutPolicy{MaxFailedAttempts: 3, Duration: 30 * time.Minute},
		func() time.Time { return fixedNow },
	)
	if err != nil {
		t.Fatalf("create login lockout: %v", err)
	}

	const failures = 5
	errorsByAttempt := make(chan error, failures)
	var waitGroup sync.WaitGroup
	for attempt := 0; attempt < failures; attempt++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, err := lockout.RecordFailure(context.Background(), lockoutUserID)
			errorsByAttempt <- err
		}()
	}
	waitGroup.Wait()
	close(errorsByAttempt)
	for err := range errorsByAttempt {
		if err != nil {
			t.Fatalf("record concurrent failure: %v", err)
		}
	}

	credential, err := repository.GetPasswordCredential(context.Background(), lockoutUserID)
	if err != nil {
		t.Fatalf("get concurrently updated credential: %v", err)
	}
	if credential.FailedAttempts != failures || !lockout.IsLocked(credential) {
		t.Fatalf("concurrent failures were lost: %+v", credential)
	}
}

func newLockoutRepository(t *testing.T) *identity.Repository {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	repository, err := identity.NewRepository(db)
	if err != nil {
		t.Fatalf("create identity repository: %v", err)
	}
	if _, err := repository.CreateLocalUser(context.Background(), identity.NewLocalUser{
		ID:                lockoutUserID,
		Username:          "lockout.user",
		DisplayName:       "Lockout User",
		PasswordHash:      "$argon2id$v=19$m=65536,t=3,p=2$stored",
		PasswordAlgorithm: PasswordAlgorithmArgon2id,
	}); err != nil {
		t.Fatalf("create lockout user: %v", err)
	}
	return repository
}
