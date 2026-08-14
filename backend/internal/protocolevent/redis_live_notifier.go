package protocolevent

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"

	"actweave/backend/internal/redisx"

	"github.com/redis/go-redis/v9"
)

type redisWakeupPayload struct {
	WorkspaceID    string `json:"workspaceId"`
	AgentID        string `json:"agentId"`
	ConversationID string `json:"conversationId"`
	RunID          string `json:"runId"`
	EventStreamID  string `json:"eventStreamId"`
	HighWatermark  int64  `json:"highWatermark"`
}

// RedisLiveNotifier implements LiveNotifier over Redis Pub/Sub.
// Payloads are wakeup hints only; consumers still read events from PostgreSQL.
type RedisLiveNotifier struct {
	client *redisx.Client
	mu     sync.Mutex
	subs   map[RunScope]map[uint64]*redisLiveSubscription
	nextID uint64
	closed atomic.Bool
	cancel context.CancelFunc
	pubsub *redis.PubSub
	stats  LiveNotifierStats
}

func NewRedisLiveNotifier(parent context.Context, client *redisx.Client) (*RedisLiveNotifier, error) {
	if client == nil || client.RDB == nil {
		return nil, ErrLiveNotifierInvalid
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	notifier := &RedisLiveNotifier{
		client: client,
		subs:   make(map[RunScope]map[uint64]*redisLiveSubscription),
		cancel: cancel,
	}
	notifier.pubsub = client.RDB.Subscribe(ctx, client.Channel("live"))
	go notifier.listen(ctx)
	return notifier, nil
}

func (n *RedisLiveNotifier) NotifyCommitted(ctx context.Context, notification CommitNotification) error {
	if n == nil || n.closed.Load() {
		return ErrLiveNotifierClosed
	}
	wakeups, err := liveWakeups(notification)
	if err != nil {
		n.mu.Lock()
		n.stats.Rejected++
		n.mu.Unlock()
		return err
	}
	n.deliver(wakeups)
	for _, wakeup := range wakeups {
		payload, err := json.Marshal(redisWakeupPayload{
			WorkspaceID: wakeup.Scope.WorkspaceID, AgentID: wakeup.Scope.AgentID,
			ConversationID: wakeup.Scope.ConversationID, RunID: wakeup.Scope.RunID,
			EventStreamID: wakeup.EventStreamID, HighWatermark: wakeup.HighWatermark,
		})
		if err != nil {
			return err
		}
		if err := n.client.RDB.Publish(ctx, n.client.Channel("live"), payload).Err(); err != nil {
			return err
		}
	}
	return nil
}

func (n *RedisLiveNotifier) Subscribe(ctx context.Context, scope RunScope) (LiveSubscription, error) {
	if n == nil || ctx == nil || n.closed.Load() {
		return nil, ErrLiveNotifierInvalid
	}
	normalized, err := normalizeRunScope(scope)
	if err != nil {
		return nil, ErrLiveNotifierInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n.mu.Lock()
	if n.closed.Load() {
		n.mu.Unlock()
		return nil, ErrLiveNotifierClosed
	}
	n.nextID++
	sub := &redisLiveSubscription{
		notifier: n, scope: normalized, id: n.nextID,
		notifications: make(chan LiveNotification, 1),
	}
	byScope := n.subs[normalized]
	if byScope == nil {
		byScope = make(map[uint64]*redisLiveSubscription)
		n.subs[normalized] = byScope
	}
	byScope[sub.id] = sub
	n.stats.ActiveSubscriptions++
	n.mu.Unlock()
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

func (n *RedisLiveNotifier) Stats() LiveNotifierStats {
	if n == nil {
		return LiveNotifierStats{}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.stats
}

func (n *RedisLiveNotifier) Close() error {
	if n == nil {
		return ErrLiveNotifierInvalid
	}
	if !n.closed.CompareAndSwap(false, true) {
		return nil
	}
	if n.cancel != nil {
		n.cancel()
	}
	if n.pubsub != nil {
		_ = n.pubsub.Close()
	}
	n.mu.Lock()
	subs := make([]*redisLiveSubscription, 0, n.stats.ActiveSubscriptions)
	for _, byScope := range n.subs {
		for _, sub := range byScope {
			subs = append(subs, sub)
		}
	}
	n.mu.Unlock()
	for _, sub := range subs {
		_ = sub.Close()
	}
	return nil
}

func (n *RedisLiveNotifier) listen(ctx context.Context) {
	ch := n.pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var payload redisWakeupPayload
			if json.Unmarshal([]byte(msg.Payload), &payload) != nil {
				continue
			}
			scope, err := normalizeRunScope(RunScope{
				WorkspaceID: payload.WorkspaceID, AgentID: payload.AgentID,
				ConversationID: payload.ConversationID, RunID: payload.RunID,
			})
			if err != nil {
				continue
			}
			n.deliver(map[RunScope]LiveNotification{
				scope: {
					Scope: scope, EventStreamID: payload.EventStreamID,
					HighWatermark: payload.HighWatermark,
				},
			})
		}
	}
}

func (n *RedisLiveNotifier) deliver(wakeups map[RunScope]LiveNotification) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for scope, wakeup := range wakeups {
		n.stats.Published++
		for _, sub := range n.subs[scope] {
			select {
			case sub.notifications <- wakeup:
				n.stats.Delivered++
			default:
				queued := <-sub.notifications
				if queued.HighWatermark > wakeup.HighWatermark {
					wakeup = queued
				}
				sub.notifications <- wakeup
				n.stats.Coalesced++
			}
		}
	}
}

type redisLiveSubscription struct {
	notifier      *RedisLiveNotifier
	scope         RunScope
	id            uint64
	notifications chan LiveNotification
	closeOnce     sync.Once
	stopMu        sync.Mutex
	stopContext   func() bool
	closed        bool
}

func (s *redisLiveSubscription) Notifications() <-chan LiveNotification {
	if s == nil {
		return nil
	}
	return s.notifications
}

func (s *redisLiveSubscription) Close() error {
	if s == nil || s.notifier == nil {
		return ErrLiveNotifierInvalid
	}
	s.closeOnce.Do(func() {
		n := s.notifier
		n.mu.Lock()
		if byScope := n.subs[s.scope]; byScope != nil {
			if _, exists := byScope[s.id]; exists {
				delete(byScope, s.id)
				n.stats.ActiveSubscriptions--
			}
			if len(byScope) == 0 {
				delete(n.subs, s.scope)
			}
		}
		close(s.notifications)
		n.mu.Unlock()
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
