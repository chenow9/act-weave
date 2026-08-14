package agentaccessauth

import (
	"context"
	"strconv"
	"time"

	"actweave/backend/internal/redisx"
)

// windowIncrScript increments every key only if all are still under limit.
var windowIncrScript = `
local limit = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
for i = 1, #KEYS do
  local n = tonumber(redis.call('GET', KEYS[i]) or '0')
  if n >= limit then
    local ttl = redis.call('PTTL', KEYS[i])
    return {0, ttl}
  end
end
local min_ttl = window_ms
for i = 1, #KEYS do
  local n = redis.call('INCR', KEYS[i])
  if n == 1 then
    redis.call('PEXPIRE', KEYS[i], window_ms)
  end
  local ttl = redis.call('PTTL', KEYS[i])
  if ttl > 0 and ttl < min_ttl then
    min_ttl = ttl
  end
end
return {1, min_ttl}
`

type RedisTokenEndpointLimiter struct {
	client *redisx.Client
	config TokenEndpointLimiterConfig
}

func NewRedisTokenEndpointLimiter(client *redisx.Client, config TokenEndpointLimiterConfig) (*RedisTokenEndpointLimiter, error) {
	if client == nil || client.RDB == nil || config.MaxIssues < 1 || config.Window <= 0 {
		return nil, ErrTokenIssueInvalid
	}
	return &RedisTokenEndpointLimiter{client: client, config: config}, nil
}

func (limiter *RedisTokenEndpointLimiter) AllowTokenIssue(
	ctx context.Context,
	attempt TokenIssueAttempt,
) (TokenIssueDecision, error) {
	if limiter == nil || ctx == nil || ctx.Err() != nil {
		return TokenIssueDecision{}, ErrTokenIssueInvalid
	}
	parts := tokenIssueKeys(attempt)
	if len(parts) == 0 {
		return TokenIssueDecision{}, ErrTokenIssueInvalid
	}
	keys := make([]string, len(parts))
	for i, part := range parts {
		keys[i] = limiter.client.Key("ratelimit", "token", strconv.Itoa(i)+"-"+part)
	}
	result, err := limiter.client.RDB.Eval(ctx, windowIncrScript, keys,
		limiter.config.MaxIssues, limiter.config.Window.Milliseconds(),
	).Slice()
	if err != nil {
		return TokenIssueDecision{}, err
	}
	allowed, ttl := int64(0), int64(limiter.config.Window.Milliseconds())
	if len(result) >= 1 {
		allowed, _ = result[0].(int64)
	}
	if len(result) >= 2 {
		if parsed, ok := result[1].(int64); ok && parsed > 0 {
			ttl = parsed
		}
	}
	resetAt := time.Now().UTC().Add(time.Duration(ttl) * time.Millisecond)
	retry := time.Duration(ttl) * time.Millisecond
	if retry < time.Second {
		retry = time.Second
	}
	if allowed == 0 {
		return TokenIssueDecision{
			Limit: limiter.config.MaxIssues, Remaining: 0, ResetAt: resetAt, RetryAfter: retry,
		}, ErrTokenIssueLimited
	}
	return TokenIssueDecision{
		Limit: limiter.config.MaxIssues, Remaining: limiter.config.MaxIssues - 1, ResetAt: resetAt,
	}, nil
}
