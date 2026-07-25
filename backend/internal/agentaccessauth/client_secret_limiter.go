package agentaccessauth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	DefaultClientAuthenticationMaxFailures    = 10
	DefaultClientAuthenticationWindow         = time.Minute
	DefaultClientAuthenticationLimiterEntries = 10_000
)

type clientAuthenticationLimitEntry struct {
	failures  int
	expiresAt time.Time
}

type InMemoryClientAuthenticationLimiter struct {
	mu          sync.Mutex
	entries     map[string]clientAuthenticationLimitEntry
	maxFailures int
	window      time.Duration
	maxEntries  int
	now         func() time.Time
}

func NewInMemoryClientAuthenticationLimiter(
	maxFailures int,
	window time.Duration,
	maxEntries int,
) (*InMemoryClientAuthenticationLimiter, error) {
	if maxFailures < 1 || window <= 0 || maxEntries < 1 {
		return nil, errors.New("Client authentication limiter bounds must be positive")
	}
	return &InMemoryClientAuthenticationLimiter{
		entries: make(map[string]clientAuthenticationLimitEntry), maxFailures: maxFailures,
		window: window, maxEntries: maxEntries, now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (limiter *InMemoryClientAuthenticationLimiter) AllowClientAuthentication(
	ctx context.Context,
	attempt ClientAuthenticationAttempt,
) error {
	key, err := clientAuthenticationAttemptKey(ctx, attempt)
	if err != nil || limiter == nil {
		return ErrClientAuthenticationLimited
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.now()
	entry, exists := limiter.entries[key]
	if exists && !now.Before(entry.expiresAt) {
		delete(limiter.entries, key)
		exists = false
	}
	if exists && entry.failures >= limiter.maxFailures {
		return ErrClientAuthenticationLimited
	}
	if !exists {
		limiter.pruneExpired(now)
		if len(limiter.entries) >= limiter.maxEntries {
			return ErrClientAuthenticationLimited
		}
	}
	return nil
}

func (limiter *InMemoryClientAuthenticationLimiter) RecordClientAuthenticationFailure(
	ctx context.Context,
	attempt ClientAuthenticationAttempt,
) error {
	key, err := clientAuthenticationAttemptKey(ctx, attempt)
	if err != nil || limiter == nil {
		return ErrClientAuthenticationLimited
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.now()
	entry, exists := limiter.entries[key]
	if exists && !now.Before(entry.expiresAt) {
		delete(limiter.entries, key)
		exists = false
	}
	if !exists {
		limiter.pruneExpired(now)
		if len(limiter.entries) >= limiter.maxEntries {
			return ErrClientAuthenticationLimited
		}
		entry = clientAuthenticationLimitEntry{expiresAt: now.Add(limiter.window)}
	}
	entry.failures++
	limiter.entries[key] = entry
	return nil
}

func (limiter *InMemoryClientAuthenticationLimiter) RecordClientAuthenticationSuccess(
	ctx context.Context,
	attempt ClientAuthenticationAttempt,
) error {
	key, err := clientAuthenticationAttemptKey(ctx, attempt)
	if err != nil || limiter == nil {
		return ErrClientAuthenticationLimited
	}
	limiter.mu.Lock()
	delete(limiter.entries, key)
	limiter.mu.Unlock()
	return nil
}

func (limiter *InMemoryClientAuthenticationLimiter) pruneExpired(now time.Time) {
	for key, entry := range limiter.entries {
		if !now.Before(entry.expiresAt) {
			delete(limiter.entries, key)
		}
	}
}

func clientAuthenticationAttemptKey(ctx context.Context, attempt ClientAuthenticationAttempt) (string, error) {
	if ctx == nil || ctx.Err() != nil {
		return "", ErrClientAuthenticationLimited
	}
	publicClientID := strings.TrimSpace(attempt.PublicClientID)
	if publicClientID != "" && !validPublicClientID(publicClientID) {
		return "", ErrClientAuthenticationLimited
	}
	if publicClientID == "" {
		publicClientID = "<malformed>"
	}
	return publicClientID + "\x00" + strings.TrimSpace(attempt.SourceIP), nil
}
