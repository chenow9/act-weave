package authn

import (
	"context"
	"errors"
	"time"

	"actweave/backend/internal/identity"
)

type LockoutPolicy struct {
	MaxFailedAttempts int
	Duration          time.Duration
}

type lockoutStore interface {
	RecordPasswordFailure(
		ctx context.Context,
		userID string,
		at time.Time,
		maxAttempts int,
		lockDuration time.Duration,
	) (identity.PasswordCredential, error)
	ClearPasswordFailures(ctx context.Context, userID string) (identity.PasswordCredential, error)
}

// LoginLockout coordinates atomic failure counts and temporary password
// lockouts. Permanent account status remains an identity/User concern.
type LoginLockout struct {
	store  lockoutStore
	policy LockoutPolicy
	now    func() time.Time
}

func NewLoginLockout(store lockoutStore, policy LockoutPolicy) (*LoginLockout, error) {
	return newLoginLockout(store, policy, func() time.Time { return time.Now().UTC() })
}

func newLoginLockout(
	store lockoutStore,
	policy LockoutPolicy,
	now func() time.Time,
) (*LoginLockout, error) {
	if store == nil {
		return nil, errors.New("login lockout store is required")
	}
	if policy.MaxFailedAttempts < 1 || policy.Duration <= 0 {
		return nil, errors.New("invalid login lockout policy")
	}
	if now == nil {
		return nil, errors.New("login lockout clock is required")
	}
	return &LoginLockout{store: store, policy: policy, now: now}, nil
}

func (l *LoginLockout) RecordFailure(
	ctx context.Context,
	userID string,
) (identity.PasswordCredential, error) {
	return l.store.RecordPasswordFailure(
		ctx,
		userID,
		l.now(),
		l.policy.MaxFailedAttempts,
		l.policy.Duration,
	)
}

func (l *LoginLockout) Unlock(
	ctx context.Context,
	userID string,
) (identity.PasswordCredential, error) {
	return l.store.ClearPasswordFailures(ctx, userID)
}

func (l *LoginLockout) IsLocked(credential identity.PasswordCredential) bool {
	return credential.LockedUntil != nil && credential.LockedUntil.After(l.now())
}
