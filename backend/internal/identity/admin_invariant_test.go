package identity

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	platformAdminFirstID   = "018f1f2e-7b5a-7c3d-8e9f-8234567890ab"
	platformAdminSecondID  = "018f1f2e-7b5a-7c3d-8e9f-8234567890ac"
	platformAdminSessionID = "018f1f2e-7b5a-7c3d-8e9f-8234567890ad"
)

func TestLastActivePlatformAdminCannotBeDemotedOrDisabled(t *testing.T) {
	repository, db := newRepositoryTest(t)
	admin := createPlatformAdminInvariantUser(t, repository, platformAdminFirstID, "only.platform.admin")
	now := time.Now().UTC()
	if _, err := repository.CreateAuthSession(context.Background(), NewAuthSession{
		ID: platformAdminSessionID, UserID: admin.ID, RefreshTokenHash: "last-admin-session-hash",
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create last administrator session: %v", err)
	}
	if _, _, err := repository.UpdatePlatformRoleAndRevokeSessions(
		context.Background(), admin.ID, PlatformRoleUser, admin.LockVersion, now,
	); !errors.Is(err, ErrLastPlatformAdmin) {
		t.Fatalf("expected last administrator demotion rejection, got %v", err)
	}
	if _, _, err := repository.UpdateStatusAndRevokeSessions(
		context.Background(), admin.ID, StatusDisabled, admin.LockVersion, now,
	); !errors.Is(err, ErrLastPlatformAdmin) {
		t.Fatalf("expected last administrator disable rejection, got %v", err)
	}
	stored, err := repository.GetUserByID(context.Background(), admin.ID)
	if err != nil || stored.PlatformRole != PlatformRoleAdmin || stored.Status != StatusActive || stored.LockVersion != admin.LockVersion {
		t.Fatalf("last administrator mutation left partial state: user=%+v err=%v", stored, err)
	}
	var revoked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM auth_sessions WHERE user_id=$1 AND revoked_at IS NOT NULL`, admin.ID).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if revoked != 0 {
		t.Fatalf("rejected administrator mutation revoked %d sessions", revoked)
	}
}

func TestConcurrentPlatformAdminDemotionKeepsOneActiveAdmin(t *testing.T) {
	repository, db := newRepositoryTest(t)
	first := createPlatformAdminInvariantUser(t, repository, platformAdminFirstID, "first.platform.admin")
	second := createPlatformAdminInvariantUser(t, repository, platformAdminSecondID, "second.platform.admin")
	now := time.Now().UTC()

	type outcome struct{ err error }
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var wait sync.WaitGroup
	for _, user := range []User{first, second} {
		wait.Add(1)
		go func(user User) {
			defer wait.Done()
			<-start
			_, _, err := repository.UpdatePlatformRoleAndRevokeSessions(
				context.Background(), user.ID, PlatformRoleUser, user.LockVersion, now,
			)
			outcomes <- outcome{err: err}
		}(user)
	}
	close(start)
	wait.Wait()
	close(outcomes)
	successes, rejected := 0, 0
	for result := range outcomes {
		switch {
		case result.err == nil:
			successes++
		case errors.Is(result.err, ErrLastPlatformAdmin):
			rejected++
		default:
			t.Fatalf("unexpected concurrent demotion error: %v", result.err)
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatalf("unexpected concurrent outcomes: successes=%d rejected=%d", successes, rejected)
	}
	var activeAdmins int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM users WHERE status='ACTIVE' AND platform_role='PLATFORM_ADMIN'
	`).Scan(&activeAdmins); err != nil {
		t.Fatal(err)
	}
	if activeAdmins != 1 {
		t.Fatalf("expected one active administrator, got %d", activeAdmins)
	}
}

func createPlatformAdminInvariantUser(t *testing.T, repository *Repository, id, username string) User {
	t.Helper()
	user, err := repository.CreateLocalUser(context.Background(), NewLocalUser{
		ID: id, Username: username, DisplayName: username, Status: StatusActive,
		PlatformRole: PlatformRoleAdmin, PasswordHash: initialPasswordHash, PasswordAlgorithm: "ARGON2ID",
	})
	if err != nil {
		t.Fatalf("create platform administrator: %v", err)
	}
	return user
}
