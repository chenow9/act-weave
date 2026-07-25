package agentaccessauth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// TokenEndpointLimiter bounds OAuth token issuance (client_credentials and
// token exchange) by client public id and remote IP. Multi-replica deployments
// should implement the same interface with a Redis/Gateway adapter; the
// in-memory adapter is the single-process baseline.
type TokenEndpointLimiter interface {
	AllowTokenIssue(context.Context, TokenIssueAttempt) (TokenIssueDecision, error)
}

type TokenIssueAttempt struct {
	PublicClientID string
	RemoteIP       string
	GrantType      string
}

type TokenIssueDecision struct {
	Limit      int
	Remaining  int
	ResetAt    time.Time
	RetryAfter time.Duration
}

var (
	ErrTokenIssueLimited = errors.New("AAP token endpoint rate limited")
	ErrTokenIssueInvalid = errors.New("AAP token endpoint limit request is invalid")
)

type TokenEndpointLimiterConfig struct {
	// MaxIssues is the max successful/attempted token responses per window per key.
	MaxIssues  int
	Window     time.Duration
	MaxEntries int
}

func DefaultTokenEndpointLimiterConfig() TokenEndpointLimiterConfig {
	return TokenEndpointLimiterConfig{
		MaxIssues: 30, Window: time.Minute, MaxEntries: 50_000,
	}
}

type tokenIssueBucket struct {
	count   int
	resetAt time.Time
}

// InMemoryTokenEndpointLimiter is the single-process Token limiter.
// A Redis implementation can share the same TokenEndpointLimiter interface for
// global hard limits across replicas.
type InMemoryTokenEndpointLimiter struct {
	mu      sync.Mutex
	buckets map[string]tokenIssueBucket
	config  TokenEndpointLimiterConfig
	now     func() time.Time
}

func NewInMemoryTokenEndpointLimiter(config TokenEndpointLimiterConfig) (*InMemoryTokenEndpointLimiter, error) {
	if config.MaxIssues < 1 || config.Window <= 0 || config.MaxEntries < 1 {
		return nil, ErrTokenIssueInvalid
	}
	return &InMemoryTokenEndpointLimiter{
		buckets: make(map[string]tokenIssueBucket),
		config:  config,
		now:     func() time.Time { return time.Now().UTC() },
	}, nil
}

func (limiter *InMemoryTokenEndpointLimiter) AllowTokenIssue(
	ctx context.Context,
	attempt TokenIssueAttempt,
) (TokenIssueDecision, error) {
	if limiter == nil || ctx == nil || ctx.Err() != nil {
		return TokenIssueDecision{}, ErrTokenIssueInvalid
	}
	keys := tokenIssueKeys(attempt)
	if len(keys) == 0 {
		return TokenIssueDecision{}, ErrTokenIssueInvalid
	}
	now := limiter.now()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.prune(now)

	limit := limiter.config.MaxIssues
	resetAt := now.Add(limiter.config.Window)
	remaining := limit
	for _, key := range keys {
		bucket, exists := limiter.buckets[key]
		if exists && now.Before(bucket.resetAt) {
			if bucket.resetAt.Before(resetAt) {
				resetAt = bucket.resetAt
			}
			if left := limit - bucket.count; left < remaining {
				remaining = left
			}
			if bucket.count >= limit {
				retry := bucket.resetAt.Sub(now)
				if retry < time.Second {
					retry = time.Second
				}
				return TokenIssueDecision{
					Limit: limit, Remaining: 0, ResetAt: bucket.resetAt, RetryAfter: retry,
				}, ErrTokenIssueLimited
			}
		}
	}
	newBuckets := 0
	for _, key := range keys {
		if _, exists := limiter.buckets[key]; !exists {
			newBuckets++
		}
	}
	if len(limiter.buckets)+newBuckets > limiter.config.MaxEntries {
		return TokenIssueDecision{
			Limit: limit, Remaining: 0, ResetAt: resetAt, RetryAfter: limiter.config.Window,
		}, ErrTokenIssueLimited
	}
	remaining = limit - 1
	resetAt = now.Add(limiter.config.Window)
	for _, key := range keys {
		bucket, exists := limiter.buckets[key]
		if !exists || !now.Before(bucket.resetAt) {
			bucket = tokenIssueBucket{resetAt: now.Add(limiter.config.Window)}
		}
		bucket.count++
		limiter.buckets[key] = bucket
		if left := limit - bucket.count; left < remaining {
			remaining = left
		}
		if bucket.resetAt.Before(resetAt) {
			resetAt = bucket.resetAt
		}
	}
	return TokenIssueDecision{Limit: limit, Remaining: remaining, ResetAt: resetAt}, nil
}

func (limiter *InMemoryTokenEndpointLimiter) prune(now time.Time) {
	for key, bucket := range limiter.buckets {
		if !now.Before(bucket.resetAt) {
			delete(limiter.buckets, key)
		}
	}
}

func tokenIssueKeys(attempt TokenIssueAttempt) []string {
	client := strings.ToLower(strings.TrimSpace(attempt.PublicClientID))
	ip := strings.TrimSpace(attempt.RemoteIP)
	if ip == "" {
		ip = "0.0.0.0"
	}
	grant := strings.ToLower(strings.TrimSpace(attempt.GrantType))
	if client == "" || grant == "" {
		return nil
	}
	// Multi-dimensional: client, IP, and grant type — most restrictive wins.
	return []string{
		"client\x00" + client,
		"ip\x00" + ip,
		"grant\x00" + grant + "\x00" + client,
	}
}
