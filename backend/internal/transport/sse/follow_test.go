package sse_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/transport/sse"
)

const (
	followWorkspaceID    = "11000000-0000-4000-8000-000000000201"
	followAgentID        = "22000000-0000-4000-8000-000000000201"
	followConversationID = "33000000-0000-4000-8000-000000000201"
	followRunID          = "44000000-0000-4000-8000-000000000201"
)

func TestCatchUpFollowNoLostWakeup(t *testing.T) {
	t.Run("closes both race windows and recovers a dropped notification", func(t *testing.T) {
		reader := newFollowReader(followEvent(1, protocolevent.EventRunStarted))
		notifier := newFakeFollowNotifier()
		reader.afterHigh = func(call int) {
			if call == 1 {
				reader.append(followEvent(2, protocolevent.EventItemStarted))
			}
		}
		notifier.onSubscribe = func() {
			reader.append(followEvent(3, protocolevent.EventItemDelta))
		}
		follow := newTestFollow(t, reader, notifier)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		received := make(chan protocolevent.ProtocolEvent, 8)
		result := make(chan error, 1)
		go func() {
			result <- follow.Follow(ctx, followScope(), 0, func(events []protocolevent.ProtocolEvent) error {
				for _, event := range events {
					received <- event
				}
				return nil
			})
		}()

		assertFollowSequences(t, received, 1, 2, 3)
		// No notification: the periodic authoritative read must find sequence 4.
		reader.append(followEvent(4, protocolevent.EventItemCompleted))
		assertFollowSequences(t, received, 4)

		reader.append(followEvent(5, protocolevent.EventUsageUpdated))
		notifier.notify()
		assertFollowSequences(t, received, 5)

		reader.append(followEvent(6, protocolevent.EventRunCompleted))
		notifier.notify()
		assertFollowSequences(t, received, 6)
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("follow result=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("terminal event did not close follow loop")
		}
		if notifier.activeCount() != 0 {
			t.Fatalf("terminal close leaked subscription: %d", notifier.activeCount())
		}
	})

	t.Run("context cancellation immediately releases subscription", func(t *testing.T) {
		reader := newFollowReader()
		notifier := newFakeFollowNotifier()
		follow := newTestFollow(t, reader, notifier)
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			result <- follow.Follow(ctx, followScope(), 0, func([]protocolevent.ProtocolEvent) error {
				return nil
			})
		}()
		select {
		case <-notifier.subscribed:
		case <-time.After(time.Second):
			t.Fatal("follow loop did not subscribe")
		}
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation result=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("cancellation did not stop follow loop")
		}
		if notifier.activeCount() != 0 {
			t.Fatalf("cancellation leaked subscription: %d", notifier.activeCount())
		}
	})

	t.Run("terminal cursor closes without subscribing", func(t *testing.T) {
		reader := newFollowReader(followEvent(1, protocolevent.EventRunCompleted))
		notifier := newFakeFollowNotifier()
		follow := newTestFollow(t, reader, notifier)
		if err := follow.Follow(context.Background(), followScope(), 1,
			func([]protocolevent.ProtocolEvent) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if notifier.activeCount() != 0 {
			t.Fatalf("terminal cursor subscribed: %d", notifier.activeCount())
		}
	})

	t.Run("gap is rejected", func(t *testing.T) {
		reader := newFollowReader(followEvent(2, protocolevent.EventItemStarted))
		notifier := newFakeFollowNotifier()
		follow := newTestFollow(t, reader, notifier)
		err := follow.Follow(context.Background(), followScope(), 0,
			func([]protocolevent.ProtocolEvent) error { return nil })
		if !errors.Is(err, sse.ErrFollowStream) {
			t.Fatalf("gap error=%v", err)
		}
		if notifier.activeCount() != 0 {
			t.Fatalf("gap leaked subscription: %d", notifier.activeCount())
		}
	})
}

func newTestFollow(
	t *testing.T,
	reader sse.FollowEventReader,
	notifier protocolevent.LiveNotifier,
) *sse.CatchUpFollow {
	t.Helper()
	follow, err := sse.NewCatchUpFollow(reader, notifier, sse.FollowPolicy{
		PageSize: 2, PollInterval: 2 * time.Millisecond, HeartbeatInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return follow
}

func assertFollowSequences(
	t *testing.T,
	events <-chan protocolevent.ProtocolEvent,
	sequences ...int64,
) {
	t.Helper()
	for _, sequence := range sequences {
		select {
		case event := <-events:
			if event.Sequence != sequence {
				t.Fatalf("event sequence=%d want=%d", event.Sequence, sequence)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for sequence %d", sequence)
		}
	}
}

func followScope() protocolevent.RunScope {
	return protocolevent.RunScope{
		WorkspaceID: followWorkspaceID, AgentID: followAgentID,
		ConversationID: followConversationID, RunID: followRunID,
	}
}

func followEvent(sequence int64, eventType string) protocolevent.ProtocolEvent {
	scope := followScope()
	return protocolevent.ProtocolEvent{
		WorkspaceID: scope.WorkspaceID, AgentID: scope.AgentID,
		ConversationID: scope.ConversationID, RunID: scope.RunID,
		StreamID: "run:" + scope.RunID, Sequence: sequence, Type: eventType,
	}
}

type fakeFollowReader struct {
	mu        sync.Mutex
	events    []protocolevent.ProtocolEvent
	highCalls int
	afterHigh func(int)
}

func newFollowReader(events ...protocolevent.ProtocolEvent) *fakeFollowReader {
	return &fakeFollowReader{events: append([]protocolevent.ProtocolEvent(nil), events...)}
}

func (reader *fakeFollowReader) append(event protocolevent.ProtocolEvent) {
	reader.mu.Lock()
	reader.events = append(reader.events, event)
	reader.mu.Unlock()
}

func (reader *fakeFollowReader) HighWatermark(
	_ context.Context,
	_ protocolevent.RunScope,
) (int64, error) {
	reader.mu.Lock()
	reader.highCalls++
	call := reader.highCalls
	high := int64(len(reader.events))
	hook := reader.afterHigh
	reader.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	return high, nil
}

func (reader *fakeFollowReader) ReadRunAfter(
	_ context.Context,
	_ protocolevent.RunScope,
	after int64,
	limit int,
) ([]protocolevent.ProtocolEvent, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	start := int(after)
	if start >= len(reader.events) {
		return nil, nil
	}
	end := min(start+limit, len(reader.events))
	return append([]protocolevent.ProtocolEvent(nil), reader.events[start:end]...), nil
}

type fakeFollowNotifier struct {
	mu           sync.Mutex
	subscription *fakeFollowSubscription
	onSubscribe  func()
	subscribed   chan struct{}
}

func newFakeFollowNotifier() *fakeFollowNotifier {
	return &fakeFollowNotifier{subscribed: make(chan struct{}, 1)}
}

func (*fakeFollowNotifier) NotifyCommitted(context.Context, protocolevent.CommitNotification) error {
	return nil
}

func (notifier *fakeFollowNotifier) Subscribe(
	_ context.Context,
	_ protocolevent.RunScope,
) (protocolevent.LiveSubscription, error) {
	subscription := &fakeFollowSubscription{
		notifier: notifier, notifications: make(chan protocolevent.LiveNotification, 1),
	}
	notifier.mu.Lock()
	notifier.subscription = subscription
	hook := notifier.onSubscribe
	notifier.mu.Unlock()
	if hook != nil {
		hook()
	}
	notifier.subscribed <- struct{}{}
	return subscription, nil
}

func (notifier *fakeFollowNotifier) notify() {
	notifier.mu.Lock()
	subscription := notifier.subscription
	notifier.mu.Unlock()
	if subscription == nil {
		return
	}
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	if subscription.closed {
		return
	}
	select {
	case subscription.notifications <- protocolevent.LiveNotification{Scope: followScope()}:
	default:
	}
}

func (notifier *fakeFollowNotifier) activeCount() int {
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if notifier.subscription == nil {
		return 0
	}
	notifier.subscription.mu.Lock()
	defer notifier.subscription.mu.Unlock()
	if notifier.subscription.closed {
		return 0
	}
	return 1
}

type fakeFollowSubscription struct {
	mu            sync.Mutex
	notifier      *fakeFollowNotifier
	notifications chan protocolevent.LiveNotification
	closed        bool
}

func (subscription *fakeFollowSubscription) Notifications() <-chan protocolevent.LiveNotification {
	return subscription.notifications
}

func (subscription *fakeFollowSubscription) Close() error {
	subscription.mu.Lock()
	if !subscription.closed {
		subscription.closed = true
		close(subscription.notifications)
	}
	subscription.mu.Unlock()
	return nil
}
