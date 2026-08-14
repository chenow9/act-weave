package agentaccess

import (
	"context"
	"time"

	"actweave/backend/internal/redisx"
)

var quotaIncrScript = `
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

type RedisDataPlaneQuota struct {
	client *redisx.Client
	config DataPlaneQuotaConfig
}

func NewRedisDataPlaneQuota(client *redisx.Client, config DataPlaneQuotaConfig) (*RedisDataPlaneQuota, error) {
	if client == nil || client.RDB == nil || config.Window <= 0 || len(config.Limits) == 0 {
		return nil, ErrDataPlaneQuotaInvalid
	}
	for operation, limit := range config.Limits {
		if !validQuotaOperation(operation) || limit < 1 {
			return nil, ErrDataPlaneQuotaInvalid
		}
	}
	return &RedisDataPlaneQuota{client: client, config: cloneQuotaConfig(config)}, nil
}

func (quota *RedisDataPlaneQuota) Allow(ctx context.Context, request DataPlaneQuotaRequest) (DataPlaneQuotaDecision, error) {
	request = normalizeQuotaRequest(request)
	if quota == nil || ctx == nil || ctx.Err() != nil || !validQuotaRequest(request) {
		return DataPlaneQuotaDecision{}, ErrDataPlaneQuotaInvalid
	}
	limit, ok := quota.config.Limits[request.Operation]
	if !ok || limit < 1 {
		return DataPlaneQuotaDecision{}, ErrDataPlaneQuotaInvalid
	}
	parts := quotaKeys(request)
	keys := make([]string, len(parts))
	for i, part := range parts {
		keys[i] = quota.client.Key("ratelimit", "quota", part)
	}
	result, err := quota.client.RDB.Eval(ctx, quotaIncrScript, keys, limit, quota.config.Window.Milliseconds()).Slice()
	if err != nil {
		return DataPlaneQuotaDecision{}, err
	}
	allowed, ttl := int64(0), int64(quota.config.Window.Milliseconds())
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
		return DataPlaneQuotaDecision{
			Limit: limit, Remaining: 0, ResetAt: resetAt, RetryAfter: retry,
		}, ErrDataPlaneQuotaExceeded
	}
	return DataPlaneQuotaDecision{
		Limit: limit, Remaining: limit - 1, ResetAt: resetAt,
	}, nil
}
