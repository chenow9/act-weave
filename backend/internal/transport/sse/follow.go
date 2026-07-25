package sse

import (
	"context"
	"errors"
	"time"

	"actweave/backend/internal/protocolevent"
)

var (
	ErrFollowInvalid = errors.New("AAP SSE follow input is invalid")
	ErrFollowStream  = errors.New("AAP SSE follow stream is invalid")
)

type FollowEventReader interface {
	ReadRunAfter(context.Context, protocolevent.RunScope, int64, int) ([]protocolevent.ProtocolEvent, error)
	HighWatermark(context.Context, protocolevent.RunScope) (int64, error)
}

type FollowPolicy struct {
	PageSize          int
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
}

func DefaultFollowPolicy() FollowPolicy {
	return FollowPolicy{
		PageSize: 100, PollInterval: time.Second, HeartbeatInterval: 15 * time.Second,
	}
}

// CatchUpFollow treats notifications only as wakeup hints. PostgreSQL remains
// the source of truth, so every wakeup and periodic tick drains committed
// events through EventReader rather than trusting notification contents.
type CatchUpFollow struct {
	reader   FollowEventReader
	notifier protocolevent.LiveNotifier
	policy   FollowPolicy
}

func NewCatchUpFollow(
	reader FollowEventReader,
	notifier protocolevent.LiveNotifier,
	policy FollowPolicy,
) (*CatchUpFollow, error) {
	if reader == nil || notifier == nil || policy.PageSize < 1 || policy.PageSize > 500 ||
		policy.PollInterval <= 0 || policy.HeartbeatInterval <= 0 {
		return nil, ErrFollowInvalid
	}
	return &CatchUpFollow{reader: reader, notifier: notifier, policy: policy}, nil
}

// Follow starts strictly after cursor and returns after a terminal Run event or
// context cancellation. deliver must finish writing a page before returning;
// this bounds in-memory data to policy.PageSize and preserves page order.
func (follow *CatchUpFollow) Follow(
	ctx context.Context,
	scope protocolevent.RunScope,
	cursor int64,
	deliver func([]protocolevent.ProtocolEvent) error,
) error {
	return follow.follow(ctx, scope, cursor, deliver, nil)
}

// FollowWithHeartbeat serializes event pages and transport-only heartbeat
// callbacks on one goroutine. Heartbeats never cause a database read.
func (follow *CatchUpFollow) FollowWithHeartbeat(
	ctx context.Context,
	scope protocolevent.RunScope,
	cursor int64,
	deliver func([]protocolevent.ProtocolEvent) error,
	heartbeat func(time.Time) error,
) error {
	if heartbeat == nil {
		return ErrFollowInvalid
	}
	return follow.follow(ctx, scope, cursor, deliver, heartbeat)
}

func (follow *CatchUpFollow) follow(
	ctx context.Context,
	scope protocolevent.RunScope,
	cursor int64,
	deliver func([]protocolevent.ProtocolEvent) error,
	heartbeat func(time.Time) error,
) error {
	if follow == nil || follow.reader == nil || follow.notifier == nil || ctx == nil ||
		cursor < 0 || deliver == nil {
		return ErrFollowInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// The first watermark defines the pre-subscribe race boundary. The second
	// watermark, read only after Subscribe succeeds, closes that window.
	firstHigh, err := follow.reader.HighWatermark(ctx, scope)
	if err != nil {
		return err
	}
	if firstHigh < cursor {
		return ErrFollowStream
	}
	terminal, err := follow.terminalAtCursor(ctx, scope, cursor)
	if err != nil {
		return err
	}
	if terminal && cursor == firstHigh {
		return nil
	}

	subscription, err := follow.notifier.Subscribe(ctx, scope)
	if err != nil {
		return err
	}
	defer subscription.Close()

	secondHigh, err := follow.reader.HighWatermark(ctx, scope)
	if err != nil {
		return err
	}
	cursor, terminal, err = follow.drain(ctx, scope, cursor, secondHigh, deliver)
	if err != nil || terminal {
		return err
	}

	ticker := time.NewTicker(follow.policy.PollInterval)
	defer ticker.Stop()
	var heartbeatTicker *time.Ticker
	var heartbeats <-chan time.Time
	if heartbeat != nil {
		heartbeatTicker = time.NewTicker(follow.policy.HeartbeatInterval)
		heartbeats = heartbeatTicker.C
		defer heartbeatTicker.Stop()
	}
	notifications := subscription.Notifications()
	for {
		shouldRead := false
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, open := <-notifications:
			if !open {
				// A lost/closed wakeup channel degrades to periodic PostgreSQL
				// polling; it cannot make committed facts disappear.
				notifications = nil
				continue
			}
			shouldRead = true
		case <-ticker.C:
			shouldRead = true
		case occurredAt := <-heartbeats:
			if err := heartbeat(occurredAt.UTC()); err != nil {
				return err
			}
			continue
		}
		if !shouldRead {
			continue
		}

		highWatermark, readErr := follow.reader.HighWatermark(ctx, scope)
		if readErr != nil {
			return readErr
		}
		cursor, terminal, readErr = follow.drain(
			ctx, scope, cursor, highWatermark, deliver,
		)
		if readErr != nil || terminal {
			return readErr
		}
	}
}

func (follow *CatchUpFollow) drain(
	ctx context.Context,
	scope protocolevent.RunScope,
	cursor, target int64,
	deliver func([]protocolevent.ProtocolEvent) error,
) (int64, bool, error) {
	if target < cursor {
		return cursor, false, ErrFollowStream
	}
	for cursor < target {
		limit := follow.policy.PageSize
		if remaining := target - cursor; remaining < int64(limit) {
			limit = int(remaining)
		}
		events, err := follow.reader.ReadRunAfter(ctx, scope, cursor, limit)
		if err != nil {
			return cursor, false, err
		}
		if len(events) == 0 || len(events) > limit {
			return cursor, false, ErrFollowStream
		}
		expected := cursor + 1
		terminal := false
		for index, event := range events {
			if event.Sequence != expected || event.Sequence > target || !sameFollowScope(event, scope) {
				return cursor, false, ErrFollowStream
			}
			if terminalEvent(event.Type) {
				if index != len(events)-1 || event.Sequence != target {
					return cursor, false, ErrFollowStream
				}
				terminal = true
			}
			expected++
		}
		page := append([]protocolevent.ProtocolEvent(nil), events...)
		if err := deliver(page); err != nil {
			return cursor, false, err
		}
		cursor = expected - 1
		if terminal {
			return cursor, true, nil
		}
	}
	return cursor, false, nil
}

func (follow *CatchUpFollow) terminalAtCursor(
	ctx context.Context,
	scope protocolevent.RunScope,
	cursor int64,
) (bool, error) {
	if cursor == 0 {
		return false, nil
	}
	events, err := follow.reader.ReadRunAfter(ctx, scope, cursor-1, 1)
	if err != nil {
		return false, err
	}
	if len(events) != 1 || events[0].Sequence != cursor || !sameFollowScope(events[0], scope) {
		return false, ErrFollowStream
	}
	return terminalEvent(events[0].Type), nil
}

func sameFollowScope(event protocolevent.ProtocolEvent, scope protocolevent.RunScope) bool {
	return event.WorkspaceID == scope.WorkspaceID && event.AgentID == scope.AgentID &&
		event.ConversationID == scope.ConversationID && event.RunID == scope.RunID &&
		event.StreamID == "run:"+scope.RunID
}

func terminalEvent(eventType string) bool {
	return eventType == protocolevent.EventRunCompleted ||
		eventType == protocolevent.EventRunFailed ||
		eventType == protocolevent.EventRunCancelled
}
