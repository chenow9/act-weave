package sse

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"actweave/backend/internal/redisx"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const redisLeaseTTL = 2 * time.Minute

var acquireScript = `
local now = tonumber(ARGV[1])
local expire = tonumber(ARGV[2])
local lease = ARGV[3]
local maxc = tonumber(ARGV[4])
local maxs = tonumber(ARGV[5])
local maxr = tonumber(ARGV[6])
for i = 1, 3 do
  redis.call('ZREMRANGEBYSCORE', KEYS[i], '-inf', now)
end
if redis.call('ZCARD', KEYS[1]) >= maxc then return 0 end
if redis.call('ZCARD', KEYS[2]) >= maxs then return 0 end
if redis.call('ZCARD', KEYS[3]) >= maxr then return 0 end
for i = 1, 3 do
  redis.call('ZADD', KEYS[i], expire, lease)
end
return 1
`

type RedisConnectionLimiter struct {
	client *redisx.Client
	policy BackpressurePolicy
	stats  ConnectionLimiterStats
	mu     sync.Mutex
}

func NewRedisConnectionLimiter(client *redisx.Client, policy BackpressurePolicy) (*RedisConnectionLimiter, error) {
	if client == nil || client.RDB == nil {
		return nil, ErrBackpressureInvalid
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &RedisConnectionLimiter{client: client, policy: policy}, nil
}

func (limiter *RedisConnectionLimiter) Acquire(
	ctx context.Context,
	identity ConnectionIdentity,
) (ConnectionLease, error) {
	if limiter == nil || ctx == nil {
		return nil, ErrBackpressureInvalid
	}
	identity = normalizeConnectionIdentity(identity)
	if !validConnectionIdentity(identity) {
		return nil, ErrBackpressureInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	leaseID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	expire := time.Now().Add(redisLeaseTTL).UnixMilli()
	keys := limiter.zsetKeys(identity)
	ok, err := limiter.client.RDB.Eval(ctx, acquireScript, keys,
		now, expire, leaseID.String(),
		limiter.policy.MaxConnectionsPerClient,
		limiter.policy.MaxConnectionsPerSubject,
		limiter.policy.MaxConnectionsPerRun,
	).Int()
	if err != nil {
		return nil, err
	}
	limiter.mu.Lock()
	if ok != 1 {
		limiter.stats.Rejected++
		limiter.mu.Unlock()
		return nil, ErrConnectionLimitExceeded
	}
	limiter.stats.Active++
	limiter.stats.Acquired++
	limiter.mu.Unlock()
	return &redisConnectionLease{
		limiter: limiter, identity: identity, id: leaseID.String(),
	}, nil
}

func (limiter *RedisConnectionLimiter) Stats() ConnectionLimiterStats {
	if limiter == nil {
		return ConnectionLimiterStats{}
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	return limiter.stats
}

func (limiter *RedisConnectionLimiter) zsetKeys(identity ConnectionIdentity) []string {
	return []string{
		limiter.client.Key("sse", "client", identity.ClientID),
		limiter.client.Key("sse", "subject", identity.SubjectID),
		limiter.client.Key("sse", "run", identity.RunID),
	}
}

type redisConnectionLease struct {
	limiter  *RedisConnectionLimiter
	identity ConnectionIdentity
	id       string
	once     sync.Once
	closed   atomic.Bool
}

func (lease *redisConnectionLease) Refresh(ctx context.Context) error {
	if lease == nil || lease.limiter == nil || lease.closed.Load() {
		return ErrBackpressureInvalid
	}
	if ctx == nil {
		ctx = context.Background()
	}
	expire := strconv.FormatInt(time.Now().Add(redisLeaseTTL).UnixMilli(), 10)
	pipe := lease.limiter.client.RDB.Pipeline()
	for _, key := range lease.limiter.zsetKeys(lease.identity) {
		score, _ := strconv.ParseFloat(expire, 64)
		pipe.ZAdd(ctx, key, redis.Z{Score: score, Member: lease.id})
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (lease *redisConnectionLease) Close() error {
	if lease == nil || lease.limiter == nil {
		return ErrBackpressureInvalid
	}
	lease.once.Do(func() {
		lease.closed.Store(true)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		pipe := lease.limiter.client.RDB.Pipeline()
		for _, key := range lease.limiter.zsetKeys(lease.identity) {
			pipe.ZRem(ctx, key, lease.id)
		}
		_, _ = pipe.Exec(ctx)
		lease.limiter.mu.Lock()
		lease.limiter.stats.Active--
		lease.limiter.stats.Released++
		lease.limiter.mu.Unlock()
	})
	return nil
}
