package sse_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/transport/sse"
)

// BenchmarkAAPSSECatchUpReplay measures paged catch-up drain cost for a large
// synthetic Run (default soak page size). Matches -bench 'AAP|SSE'.
func BenchmarkAAPSSECatchUpReplay(b *testing.B) {
	const events = 10_000
	scope := soakScope("bench-replay")
	policy := sse.FollowPolicy{
		PageSize: 100, PollInterval: time.Hour, HeartbeatInterval: time.Hour,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		reader := newSyntheticFollowReader(scope, events, true)
		notifier := newFakeFollowNotifier()
		follow, err := sse.NewCatchUpFollow(reader, notifier, policy)
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		var count int64
		if err := follow.Follow(context.Background(), scope, 0, func(page []protocolevent.ProtocolEvent) error {
			count += int64(len(page))
			return nil
		}); err != nil {
			b.Fatal(err)
		}
		if count != events {
			b.Fatalf("count=%d want=%d", count, events)
		}
	}
}

// BenchmarkAAPSSEEncodePage measures encoding a full page of events (transport cost).
// Uses a fixed persisted envelope (sequence must match payload) repeated pageSize times.
func BenchmarkAAPSSEEncodePage(b *testing.B) {
	event := persistedSSEEvent(b, "bench-page")
	encoder := sse.NewEncoder()
	const pageSize = 100
	var buf bytes.Buffer
	b.ReportAllocs()
	b.SetBytes(int64(pageSize * len(event.Payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		for j := 0; j < pageSize; j++ {
			if err := encoder.Encode(&buf, event); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkAAPMultiClientLimiterAcquireRelease exercises connection lease churn.
func BenchmarkAAPMultiClientLimiterAcquireRelease(b *testing.B) {
	policy := sse.DefaultBackpressurePolicy()
	limiter, err := sse.NewInMemoryConnectionLimiter(policy)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		identity := sse.ConnectionIdentity{
			ClientID:  fmt.Sprintf("cl-%d", i%policy.MaxConnectionsPerClient),
			SubjectID: fmt.Sprintf("sub-%d", i%policy.MaxConnectionsPerSubject),
			RunID:     fmt.Sprintf("run-%d", i),
		}
		lease, err := limiter.Acquire(context.Background(), identity)
		if err != nil {
			// Hit per-run unique so only client/subject caps matter; rotate by closing.
			b.Fatal(err)
		}
		if err := lease.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
