package agentaccessauth

import (
	"context"
	"errors"
	"strconv"
	"time"

	"actweave/backend/internal/redisx"

	"github.com/redis/go-redis/v9"
)

type RedisClientAuthenticationLimiter struct {
	client      *redisx.Client
	maxFailures int
	window      time.Duration
}

func NewRedisClientAuthenticationLimiter(
	client *redisx.Client,
	maxFailures int,
	window time.Duration,
) (*RedisClientAuthenticationLimiter, error) {
	if client == nil || client.RDB == nil || maxFailures < 1 || window <= 0 {
		return nil, errors.New("Client authentication limiter bounds must be positive")
	}
	return &RedisClientAuthenticationLimiter{
		client: client, maxFailures: maxFailures, window: window,
	}, nil
}

func (limiter *RedisClientAuthenticationLimiter) key(ctx context.Context, attempt ClientAuthenticationAttempt) (string, error) {
	part, err := clientAuthenticationAttemptKey(ctx, attempt)
	if err != nil {
		return "", err
	}
	return limiter.client.Key("ratelimit", "clientauth", part), nil
}

func (limiter *RedisClientAuthenticationLimiter) AllowClientAuthentication(
	ctx context.Context,
	attempt ClientAuthenticationAttempt,
) error {
	if limiter == nil {
		return ErrClientAuthenticationLimited
	}
	key, err := limiter.key(ctx, attempt)
	if err != nil {
		return err
	}
	raw, err := limiter.client.RDB.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	n, convErr := strconv.Atoi(raw)
	if convErr != nil {
		return ErrClientAuthenticationLimited
	}
	if n >= limiter.maxFailures {
		return ErrClientAuthenticationLimited
	}
	return nil
}

func (limiter *RedisClientAuthenticationLimiter) RecordClientAuthenticationFailure(
	ctx context.Context,
	attempt ClientAuthenticationAttempt,
) error {
	if limiter == nil {
		return ErrClientAuthenticationLimited
	}
	key, err := limiter.key(ctx, attempt)
	if err != nil {
		return err
	}
	n, err := limiter.client.RDB.Incr(ctx, key).Result()
	if err != nil {
		return err
	}
	if n == 1 {
		return limiter.client.RDB.PExpire(ctx, key, limiter.window).Err()
	}
	return nil
}

func (limiter *RedisClientAuthenticationLimiter) RecordClientAuthenticationSuccess(
	ctx context.Context,
	attempt ClientAuthenticationAttempt,
) error {
	if limiter == nil {
		return ErrClientAuthenticationLimited
	}
	key, err := limiter.key(ctx, attempt)
	if err != nil {
		return err
	}
	return limiter.client.RDB.Del(ctx, key).Err()
}
