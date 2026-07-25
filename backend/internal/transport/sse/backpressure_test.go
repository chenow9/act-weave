package sse_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"actweave/backend/internal/transport/sse"
)

func TestSlowConsumerIsolation(t *testing.T) {
	t.Run("write timeout disconnects only slow consumer and event replays", func(t *testing.T) {
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()
		deadlineSet := make(chan struct{}, 1)
		writer, err := sse.NewDeadlineWriter(server, func(deadline time.Time) error {
			select {
			case deadlineSet <- struct{}{}:
			default:
			}
			return server.SetWriteDeadline(deadline)
		}, 20*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		event := persistedSSEEvent(t, "slow consumer replay")
		slowResult := make(chan error, 1)
		go func() { slowResult <- sse.NewEncoder().Encode(writer, event) }()
		select {
		case <-deadlineSet:
		case <-time.After(time.Second):
			t.Fatal("slow writer did not set deadline")
		}

		var fast bytes.Buffer
		if err := sse.NewEncoder().Encode(&fast, event); err != nil {
			t.Fatalf("fast consumer was blocked: %v", err)
		}
		if !bytes.Contains(fast.Bytes(), []byte("id: 42\n")) {
			t.Fatalf("fast consumer lost event: %q", fast.String())
		}
		select {
		case err := <-slowResult:
			if !errors.Is(err, sse.ErrSlowConsumer) {
				t.Fatalf("slow consumer error=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("slow consumer was not disconnected")
		}

		// The same persisted event remains replayable after the slow connection
		// is gone; transport timeout has no mutation path into the Event Store.
		var replay bytes.Buffer
		if err := sse.NewEncoder().Encode(&replay, event); err != nil || replay.String() != fast.String() {
			t.Fatalf("event replay changed after timeout: err=%v replay=%q", err, replay.String())
		}
	})

	t.Run("client subject and Run limits are atomic and observable", func(t *testing.T) {
		policy := sse.DefaultBackpressurePolicy()
		policy.MaxConnectionsPerClient = 2
		policy.MaxConnectionsPerSubject = 2
		policy.MaxConnectionsPerRun = 1
		limiter, err := sse.NewInMemoryConnectionLimiter(policy)
		if err != nil {
			t.Fatal(err)
		}
		first, err := limiter.Acquire(context.Background(), sse.ConnectionIdentity{
			ClientID: "client-a", SubjectID: "subject-a", RunID: "run-a",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := limiter.Acquire(context.Background(), sse.ConnectionIdentity{
			ClientID: "client-b", SubjectID: "subject-b", RunID: "run-a",
		}); !errors.Is(err, sse.ErrConnectionLimitExceeded) {
			t.Fatalf("Run limit error=%v", err)
		}
		second, err := limiter.Acquire(context.Background(), sse.ConnectionIdentity{
			ClientID: "client-a", SubjectID: "subject-a", RunID: "run-b",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := limiter.Acquire(context.Background(), sse.ConnectionIdentity{
			ClientID: "client-a", SubjectID: "subject-c", RunID: "run-c",
		}); !errors.Is(err, sse.ErrConnectionLimitExceeded) {
			t.Fatalf("Client limit error=%v", err)
		}
		if _, err := limiter.Acquire(context.Background(), sse.ConnectionIdentity{
			ClientID: "client-c", SubjectID: "subject-a", RunID: "run-c",
		}); !errors.Is(err, sse.ErrConnectionLimitExceeded) {
			t.Fatalf("Subject limit error=%v", err)
		}
		if stats := limiter.Stats(); stats.Active != 2 || stats.Acquired != 2 || stats.Rejected != 3 {
			t.Fatalf("limiter stats before release=%+v", stats)
		}
		if err := first.Close(); err != nil {
			t.Fatal(err)
		}
		if err := first.Close(); err != nil {
			t.Fatal(err)
		}
		third, err := limiter.Acquire(context.Background(), sse.ConnectionIdentity{
			ClientID: "client-b", SubjectID: "subject-b", RunID: "run-a",
		})
		if err != nil {
			t.Fatalf("released Run slot was not reusable: %v", err)
		}
		_ = second.Close()
		_ = third.Close()
		if stats := limiter.Stats(); stats.Active != 0 || stats.Acquired != 3 ||
			stats.Released != 3 || stats.Rejected != 3 {
			t.Fatalf("limiter final stats=%+v", stats)
		}
	})

	t.Run("bounded policy rejects unbounded pages", func(t *testing.T) {
		policy := sse.DefaultBackpressurePolicy()
		policy.MaxPendingEvents = 501
		if err := policy.Validate(); !errors.Is(err, sse.ErrBackpressureInvalid) {
			t.Fatalf("unbounded policy error=%v", err)
		}
	})
}
