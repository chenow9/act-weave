package protocolevent

import (
	"context"
	"errors"
	"strings"
	"sync"
)

var (
	ErrLiveNotifierInvalid = errors.New("live notifier input is invalid")
	ErrLiveNotifierClosed  = errors.New("live notifier is closed")
)

// LiveNotification is a wakeup hint, not a fact. Consumers must use
// EventReader to load every event through HighWatermark.
type LiveNotification struct {
	Scope         RunScope
	EventStreamID string
	HighWatermark int64
}

type LiveSubscription interface {
	Notifications() <-chan LiveNotification
	Close() error
}

// LiveNotifier is transport-neutral. An in-process, Redis, MQ, or WebSocket
// wakeup adapter can implement the same contract without becoming a fact store.
type LiveNotifier interface {
	CommitNotifier
	Subscribe(context.Context, RunScope) (LiveSubscription, error)
}

type LiveNotifierStats struct {
	Published           uint64
	Delivered           uint64
	Coalesced           uint64
	Rejected            uint64
	ActiveSubscriptions int
}

type InProcessLiveNotifier struct {
	mu            sync.Mutex
	subscriptions map[RunScope]map[uint64]*inProcessSubscription
	nextID        uint64
	closed        bool
	stats         LiveNotifierStats
}

var _ LiveNotifier = (*InProcessLiveNotifier)(nil)

func NewInProcessLiveNotifier() *InProcessLiveNotifier {
	return &InProcessLiveNotifier{
		subscriptions: make(map[RunScope]map[uint64]*inProcessSubscription),
	}
}

func (notifier *InProcessLiveNotifier) NotifyCommitted(
	_ context.Context,
	notification CommitNotification,
) error {
	if notifier == nil {
		return ErrLiveNotifierInvalid
	}
	wakeups, err := liveWakeups(notification)
	if err != nil {
		notifier.mu.Lock()
		notifier.stats.Rejected++
		notifier.mu.Unlock()
		return err
	}

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if notifier.closed {
		notifier.stats.Rejected++
		return ErrLiveNotifierClosed
	}
	for scope, wakeup := range wakeups {
		notifier.stats.Published++
		for _, subscription := range notifier.subscriptions[scope] {
			select {
			case subscription.notifications <- wakeup:
				notifier.stats.Delivered++
			default:
				queued := <-subscription.notifications
				if queued.HighWatermark > wakeup.HighWatermark {
					wakeup = queued
				}
				subscription.notifications <- wakeup
				notifier.stats.Coalesced++
			}
		}
	}
	return nil
}

func (notifier *InProcessLiveNotifier) Subscribe(
	ctx context.Context,
	scope RunScope,
) (LiveSubscription, error) {
	if notifier == nil || ctx == nil {
		return nil, ErrLiveNotifierInvalid
	}
	normalized, err := normalizeRunScope(scope)
	if err != nil {
		return nil, ErrLiveNotifierInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	notifier.mu.Lock()
	if notifier.closed {
		notifier.mu.Unlock()
		return nil, ErrLiveNotifierClosed
	}
	notifier.nextID++
	subscription := &inProcessSubscription{
		notifier: notifier, scope: normalized, id: notifier.nextID,
		notifications: make(chan LiveNotification, 1),
	}
	byScope := notifier.subscriptions[normalized]
	if byScope == nil {
		byScope = make(map[uint64]*inProcessSubscription)
		notifier.subscriptions[normalized] = byScope
	}
	byScope[subscription.id] = subscription
	notifier.stats.ActiveSubscriptions++
	notifier.mu.Unlock()

	stop := context.AfterFunc(ctx, func() { _ = subscription.Close() })
	subscription.stopMu.Lock()
	subscription.stopContext = stop
	closed := subscription.closed
	subscription.stopMu.Unlock()
	if closed {
		stop()
	}
	return subscription, nil
}

func (notifier *InProcessLiveNotifier) Stats() LiveNotifierStats {
	if notifier == nil {
		return LiveNotifierStats{}
	}
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	return notifier.stats
}

func (notifier *InProcessLiveNotifier) Close() error {
	if notifier == nil {
		return ErrLiveNotifierInvalid
	}
	notifier.mu.Lock()
	if notifier.closed {
		notifier.mu.Unlock()
		return nil
	}
	notifier.closed = true
	subscriptions := make([]*inProcessSubscription, 0, notifier.stats.ActiveSubscriptions)
	for _, byScope := range notifier.subscriptions {
		for _, subscription := range byScope {
			subscriptions = append(subscriptions, subscription)
		}
	}
	notifier.mu.Unlock()
	for _, subscription := range subscriptions {
		_ = subscription.Close()
	}
	return nil
}

type inProcessSubscription struct {
	notifier      *InProcessLiveNotifier
	scope         RunScope
	id            uint64
	notifications chan LiveNotification
	closeOnce     sync.Once
	stopMu        sync.Mutex
	stopContext   func() bool
	closed        bool
}

func (subscription *inProcessSubscription) Notifications() <-chan LiveNotification {
	if subscription == nil {
		return nil
	}
	return subscription.notifications
}

func (subscription *inProcessSubscription) Close() error {
	if subscription == nil || subscription.notifier == nil {
		return ErrLiveNotifierInvalid
	}
	subscription.closeOnce.Do(func() {
		notifier := subscription.notifier
		notifier.mu.Lock()
		if byScope := notifier.subscriptions[subscription.scope]; byScope != nil {
			if _, exists := byScope[subscription.id]; exists {
				delete(byScope, subscription.id)
				notifier.stats.ActiveSubscriptions--
			}
			if len(byScope) == 0 {
				delete(notifier.subscriptions, subscription.scope)
			}
		}
		close(subscription.notifications)
		notifier.mu.Unlock()

		subscription.stopMu.Lock()
		subscription.closed = true
		stop := subscription.stopContext
		subscription.stopMu.Unlock()
		if stop != nil {
			stop()
		}
	})
	return nil
}

func liveWakeups(notification CommitNotification) (map[RunScope]LiveNotification, error) {
	if len(notification.Events) == 0 {
		return nil, ErrLiveNotifierInvalid
	}
	wakeups := make(map[RunScope]LiveNotification)
	for _, reference := range notification.Events {
		scope, err := normalizeRunScope(RunScope{
			WorkspaceID: reference.WorkspaceID, AgentID: reference.AgentID,
			ConversationID: reference.ConversationID, RunID: reference.RunID,
		})
		reference.EventID = strings.ToLower(strings.TrimSpace(reference.EventID))
		reference.EventStreamID = strings.ToLower(strings.TrimSpace(reference.EventStreamID))
		if err != nil || !modelUUID(reference.EventID) || !modelUUID(reference.EventStreamID) ||
			reference.Sequence < 1 || reference.GlobalPosition < 1 {
			return nil, ErrLiveNotifierInvalid
		}
		wakeup := wakeups[scope]
		if wakeup.HighWatermark == 0 {
			wakeup = LiveNotification{Scope: scope, EventStreamID: reference.EventStreamID}
		}
		if wakeup.EventStreamID != reference.EventStreamID {
			return nil, ErrLiveNotifierInvalid
		}
		if reference.Sequence > wakeup.HighWatermark {
			wakeup.HighWatermark = reference.Sequence
		}
		wakeups[scope] = wakeup
	}
	return wakeups, nil
}
