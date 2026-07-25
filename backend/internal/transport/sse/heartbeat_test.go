package sse_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/transport/sse"
)

func TestHeartbeat(t *testing.T) {
	t.Run("default policy is fifteen seconds", func(t *testing.T) {
		policy := sse.DefaultFollowPolicy()
		if policy.HeartbeatInterval != 15*time.Second {
			t.Fatalf("heartbeat interval=%s", policy.HeartbeatInterval)
		}
	})

	t.Run("idle follow writes only a comment without reading database", func(t *testing.T) {
		reader := newFollowReader()
		notifier := newFakeFollowNotifier()
		follow, err := sse.NewCatchUpFollow(reader, notifier, sse.FollowPolicy{
			PageSize: 10, PollInterval: time.Hour, HeartbeatInterval: 5 * time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		var output bytes.Buffer
		encoder := sse.NewEncoder()
		result := make(chan error, 1)
		go func() {
			result <- follow.FollowWithHeartbeat(
				ctx, followScope(), 0,
				func([]protocolevent.ProtocolEvent) error {
					return errors.New("unexpected business event")
				},
				func(occurredAt time.Time) error {
					err := encoder.Heartbeat(&output, occurredAt)
					cancel()
					return err
				},
			)
		}()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("follow result=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("heartbeat follow did not stop")
		}
		value := output.String()
		if !strings.HasPrefix(value, ": ping ") || !strings.HasSuffix(value, "Z\n\n") ||
			strings.Contains(value, "id:") || strings.Contains(value, "event:") ||
			strings.Contains(value, "data:") {
			t.Fatalf("heartbeat frame=%q", value)
		}
		reader.mu.Lock()
		highCalls := reader.highCalls
		reader.mu.Unlock()
		if highCalls != 2 {
			t.Fatalf("heartbeat caused database reads: high watermark calls=%d", highCalls)
		}
		if notifier.activeCount() != 0 {
			t.Fatalf("heartbeat cancellation leaked subscription: %d", notifier.activeCount())
		}
	})

	t.Run("writer failure closes without another business event", func(t *testing.T) {
		reader := newFollowReader()
		notifier := newFakeFollowNotifier()
		follow, err := sse.NewCatchUpFollow(reader, notifier, sse.FollowPolicy{
			PageSize: 10, PollInterval: time.Hour, HeartbeatInterval: time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		failure := errors.New("heartbeat write failed")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		err = follow.FollowWithHeartbeat(
			ctx, followScope(), 0, func([]protocolevent.ProtocolEvent) error { return nil },
			func(time.Time) error { return failure },
		)
		if !errors.Is(err, failure) || notifier.activeCount() != 0 {
			t.Fatalf("heartbeat failure=%v active=%d", err, notifier.activeCount())
		}
	})
}
