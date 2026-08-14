// Package redisx is the shared Redis client and key helper for multi-replica
// wakeup, rate limits, and cancel/security broadcasts. It is not a fact store.
package redisx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrInvalid = errors.New("redis configuration is invalid")
	ErrClosed  = errors.New("redis client is closed")
)

const DefaultKeyPrefix = "actweave"

type Config struct {
	Addr      string
	Password  string
	DB        int
	KeyPrefix string
}

type Client struct {
	RDB    *redis.Client
	Prefix string
}

func Open(ctx context.Context, cfg Config) (*Client, error) {
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		return nil, fmt.Errorf("%w: addr is required", ErrInvalid)
	}
	if cfg.DB < 0 {
		return nil, fmt.Errorf("%w: db must be >= 0", ErrInvalid)
	}
	prefix := strings.TrimSpace(cfg.KeyPrefix)
	if prefix == "" {
		prefix = DefaultKeyPrefix
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})
	if ctx == nil {
		ctx = context.Background()
	}
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("ping redis %s: %w", addr, err)
	}
	return &Client{RDB: rdb, Prefix: prefix}, nil
}

func (c *Client) Close() error {
	if c == nil || c.RDB == nil {
		return nil
	}
	return c.RDB.Close()
}

func (c *Client) Key(parts ...string) string {
	prefix := DefaultKeyPrefix
	if c != nil && strings.TrimSpace(c.Prefix) != "" {
		prefix = c.Prefix
	}
	cleaned := make([]string, 0, len(parts)+1)
	cleaned = append(cleaned, prefix)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		cleaned = append(cleaned, part)
	}
	return strings.Join(cleaned, ":")
}

func (c *Client) Channel(name string) string {
	return c.Key("ch", name)
}
