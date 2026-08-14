package agentaccessauth

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"

	"actweave/backend/internal/redisx"

	"github.com/redis/go-redis/v9"
)

type RedisSecurityChanges struct {
	client *redisx.Client
	mu     sync.Mutex
	subs   map[uint64]*redisSecuritySubscription
	nextID uint64
	closed atomic.Bool
	cancel context.CancelFunc
	pubsub *redis.PubSub
	stats  SecurityChangeStats
}

func NewRedisSecurityChanges(parent context.Context, client *redisx.Client) (*RedisSecurityChanges, error) {
	if client == nil || client.RDB == nil {
		return nil, ErrStreamRevalidationInvalid
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	source := &RedisSecurityChanges{
		client: client, subs: make(map[uint64]*redisSecuritySubscription), cancel: cancel,
	}
	source.pubsub = client.RDB.Subscribe(ctx, client.Channel("security"))
	go source.listen(ctx)
	return source, nil
}

func (source *RedisSecurityChanges) Subscribe(
	ctx context.Context,
	binding StreamBinding,
) (SecurityChangeSubscription, error) {
	binding = normalizeStreamBinding(binding)
	if source == nil || ctx == nil || !validStreamBinding(binding) || source.closed.Load() {
		return nil, ErrStreamRevalidationInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source.mu.Lock()
	source.nextID++
	sub := &redisSecuritySubscription{
		source: source, id: source.nextID, binding: binding,
		changes: make(chan SecurityChange, 1),
	}
	source.subs[sub.id] = sub
	source.stats.ActiveSubscriptions++
	source.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { _ = sub.Close() })
	sub.stopMu.Lock()
	sub.stopContext = stop
	closed := sub.closed
	sub.stopMu.Unlock()
	if closed {
		stop()
	}
	return sub, nil
}

func (source *RedisSecurityChanges) Publish(change SecurityChange) error {
	change.WorkspaceID = normalizeIdentity(change.WorkspaceID)
	change.AgentID = normalizeIdentity(change.AgentID)
	change.ClientID = normalizeIdentity(change.ClientID)
	change.GrantID = normalizeIdentity(change.GrantID)
	if source == nil || source.closed.Load() || !validSecurityChange(change) {
		return ErrStreamRevalidationInvalid
	}
	source.deliver(change)
	payload, err := json.Marshal(change)
	if err != nil {
		return err
	}
	return source.client.RDB.Publish(context.Background(), source.client.Channel("security"), payload).Err()
}

func (source *RedisSecurityChanges) Stats() SecurityChangeStats {
	if source == nil {
		return SecurityChangeStats{}
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.stats
}

func (source *RedisSecurityChanges) Close() error {
	if source == nil {
		return ErrStreamRevalidationInvalid
	}
	if !source.closed.CompareAndSwap(false, true) {
		return nil
	}
	if source.cancel != nil {
		source.cancel()
	}
	if source.pubsub != nil {
		_ = source.pubsub.Close()
	}
	source.mu.Lock()
	subs := make([]*redisSecuritySubscription, 0, len(source.subs))
	for _, sub := range source.subs {
		subs = append(subs, sub)
	}
	source.mu.Unlock()
	for _, sub := range subs {
		_ = sub.Close()
	}
	return nil
}

func (source *RedisSecurityChanges) listen(ctx context.Context) {
	ch := source.pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var change SecurityChange
			if json.Unmarshal([]byte(msg.Payload), &change) != nil {
				continue
			}
			source.deliver(change)
		}
	}
}

func (source *RedisSecurityChanges) deliver(change SecurityChange) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.stats.Published++
	for _, sub := range source.subs {
		if !securityChangeMatches(change, sub.binding) {
			continue
		}
		delivery := change
		select {
		case sub.changes <- delivery:
			source.stats.Delivered++
		default:
			queued := <-sub.changes
			if queued.SecurityVersion > delivery.SecurityVersion {
				delivery = queued
			}
			sub.changes <- delivery
			source.stats.Coalesced++
		}
	}
}

type redisSecuritySubscription struct {
	source      *RedisSecurityChanges
	id          uint64
	binding     StreamBinding
	changes     chan SecurityChange
	closeOnce   sync.Once
	stopMu      sync.Mutex
	stopContext func() bool
	closed      bool
}

func (s *redisSecuritySubscription) Changes() <-chan SecurityChange {
	if s == nil {
		return nil
	}
	return s.changes
}

func (s *redisSecuritySubscription) Close() error {
	if s == nil || s.source == nil {
		return ErrStreamRevalidationInvalid
	}
	s.closeOnce.Do(func() {
		source := s.source
		source.mu.Lock()
		if _, exists := source.subs[s.id]; exists {
			delete(source.subs, s.id)
			source.stats.ActiveSubscriptions--
		}
		close(s.changes)
		source.mu.Unlock()
		s.stopMu.Lock()
		s.closed = true
		stop := s.stopContext
		s.stopMu.Unlock()
		if stop != nil {
			stop()
		}
	})
	return nil
}
