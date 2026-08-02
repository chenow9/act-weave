package sse_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/transport/sse"
)

// Soak defaults (aligned with DefaultBackpressurePolicy / DefaultFollowPolicy).
const (
	soakReplayEvents         = 100_000
	soakReplayPageSize       = 100
	soakMultiClients         = 8
	soakEventsPerClientRun   = 200
	soakSlowWriteTimeout     = 25 * time.Millisecond
	soakHeartbeatInterval    = 5 * time.Millisecond
	soakPollInterval         = 2 * time.Millisecond
	soakGoroutineLeakBudget  = 32 // process noise allowance after GC
	soakFastPathMaxLatencyMs = 50 // slow consumer must not stall fast path this long
)

// TestAAPSoak is the M10-T5 capacity / long-connection gate for AAP SSE transport.
// Scenarios: large Replay, multi-client Follow, slow-consumer isolation,
// waiting-approval long connection, Token reconnect, and resource stability.
func TestAAPSoak(t *testing.T) {
	t.Run("Replay100kEventsNoGap", testAAPSoakReplay100k)
	t.Run("MultiClientFollow", testAAPSoakMultiClientFollow)
	t.Run("SlowConsumerDoesNotBlockFastPath", testAAPSoakSlowConsumerIsolation)
	t.Run("WaitingApprovalLongConnectionAndResume", testAAPSoakWaitingApprovalLongConnection)
	t.Run("TokenReconnectResumesSameCursor", testAAPSoakTokenReconnect)
	t.Run("ResourceStabilityLimiterAndGoroutines", testAAPSoakResourceStability)
	t.Run("CapacityDefaultsDocumented", testAAPSoakCapacityDefaults)
}

func testAAPSoakReplay100k(t *testing.T) {
	scope := soakScope("replay")
	reader := newSyntheticFollowReader(scope, soakReplayEvents, true)
	notifier := newFakeFollowNotifier()
	follow, err := sse.NewCatchUpFollow(reader, notifier, sse.FollowPolicy{
		PageSize: soakReplayPageSize, PollInterval: soakPollInterval, HeartbeatInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	beforeGoroutines := runtime.NumGoroutine()
	var delivered atomic.Int64
	var maxPage atomic.Int64
	started := time.Now()
	err = follow.Follow(context.Background(), scope, 0, func(page []protocolevent.ProtocolEvent) error {
		if int64(len(page)) > maxPage.Load() {
			maxPage.Store(int64(len(page)))
		}
		if len(page) > soakReplayPageSize {
			return fmt.Errorf("page size %d exceeds policy %d", len(page), soakReplayPageSize)
		}
		for _, event := range page {
			next := delivered.Add(1)
			if event.Sequence != next {
				return fmt.Errorf("gap: got sequence %d want %d", event.Sequence, next)
			}
		}
		return nil
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if delivered.Load() != soakReplayEvents {
		t.Fatalf("delivered=%d want=%d", delivered.Load(), soakReplayEvents)
	}
	if maxPage.Load() > int64(soakReplayPageSize) {
		t.Fatalf("max page=%d exceeds %d", maxPage.Load(), soakReplayPageSize)
	}
	// Paging only — never materialize full run in one deliver call.
	if reader.maxInFlight() > soakReplayPageSize {
		t.Fatalf("reader in-flight page %d exceeds policy", reader.maxInFlight())
	}
	t.Logf(
		"replay100k events=%d page=%d duration=%s throughput=%.0f events/s goroutines_delta=%d",
		soakReplayEvents, soakReplayPageSize, elapsed,
		float64(soakReplayEvents)/elapsed.Seconds(),
		runtime.NumGoroutine()-beforeGoroutines,
	)
}

func testAAPSoakMultiClientFollow(t *testing.T) {
	policy := sse.DefaultBackpressurePolicy()
	limiter, err := sse.NewInMemoryConnectionLimiter(policy)
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errs := make(chan error, soakMultiClients)
	var totalDelivered atomic.Int64
	started := time.Now()

	for client := 0; client < soakMultiClients; client++ {
		client := client
		wait.Add(1)
		go func() {
			defer wait.Done()
			scope := soakScope(fmt.Sprintf("client-%d", client))
			lease, err := limiter.Acquire(context.Background(), sse.ConnectionIdentity{
				ClientID:  fmt.Sprintf("cl-soak-%d", client),
				SubjectID: fmt.Sprintf("sub-soak-%d", client%4),
				RunID:     scope.RunID,
			})
			if err != nil {
				errs <- err
				return
			}
			defer lease.Close()

			reader := newSyntheticFollowReader(scope, soakEventsPerClientRun, true)
			notifier := newFakeFollowNotifier()
			follow, err := sse.NewCatchUpFollow(reader, notifier, sse.FollowPolicy{
				PageSize: 50, PollInterval: soakPollInterval, HeartbeatInterval: time.Hour,
			})
			if err != nil {
				errs <- err
				return
			}
			var count int64
			if err := follow.Follow(context.Background(), scope, 0, func(page []protocolevent.ProtocolEvent) error {
				count += int64(len(page))
				return nil
			}); err != nil {
				errs <- err
				return
			}
			if count != soakEventsPerClientRun {
				errs <- fmt.Errorf("client %d delivered=%d want=%d", client, count, soakEventsPerClientRun)
				return
			}
			totalDelivered.Add(count)
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	stats := limiter.Stats()
	if stats.Active != 0 {
		t.Fatalf("leaked connections: %+v", stats)
	}
	want := int64(soakMultiClients * soakEventsPerClientRun)
	if totalDelivered.Load() != want {
		t.Fatalf("total delivered=%d want=%d", totalDelivered.Load(), want)
	}
	t.Logf(
		"multiClient clients=%d events_each=%d total=%d duration=%s limiter=%+v",
		soakMultiClients, soakEventsPerClientRun, want, time.Since(started), stats,
	)
}

func testAAPSoakSlowConsumerIsolation(t *testing.T) {
	// Slow consumer blocks on net.Pipe; fast consumer must still encode quickly.
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	slowWriter, err := sse.NewDeadlineWriter(server, server.SetWriteDeadline, soakSlowWriteTimeout)
	if err != nil {
		t.Fatal(err)
	}
	event := persistedSSEEvent(t, "soak-slow-isolation")
	encoder := sse.NewEncoder()

	slowDone := make(chan error, 1)
	go func() { slowDone <- encoder.Encode(slowWriter, event) }()

	// Give the slow write a moment to block without reading client side.
	time.Sleep(5 * time.Millisecond)

	var fast bytes.Buffer
	fastStarted := time.Now()
	if err := encoder.Encode(&fast, event); err != nil {
		t.Fatalf("fast path blocked by slow consumer: %v", err)
	}
	fastLatency := time.Since(fastStarted)
	if fastLatency > soakFastPathMaxLatencyMs*time.Millisecond {
		t.Fatalf("fast path latency %s exceeds %dms budget (slow consumer interference)",
			fastLatency, soakFastPathMaxLatencyMs)
	}
	if !bytes.Contains(fast.Bytes(), []byte("id: 42\n")) {
		t.Fatalf("fast consumer lost event: %q", fast.String())
	}

	select {
	case err := <-slowDone:
		if !errors.Is(err, sse.ErrSlowConsumer) {
			t.Fatalf("slow consumer error=%v want ErrSlowConsumer", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow consumer was not disconnected")
	}

	// Event remains replayable after isolation.
	var replay bytes.Buffer
	if err := encoder.Encode(&replay, event); err != nil {
		t.Fatal(err)
	}
	if replay.String() != fast.String() {
		t.Fatalf("replay diverged after slow disconnect")
	}
	t.Logf("slowConsumerIsolation fast_latency=%s", fastLatency)
}

func testAAPSoakWaitingApprovalLongConnection(t *testing.T) {
	scope := soakScope("waiting")
	// Non-terminal prefix: started + waiting; connection stays open with heartbeats.
	reader := newFollowReader(
		soakEvent(scope, 1, protocolevent.EventRunStarted),
		soakEvent(scope, 2, protocolevent.EventRunWaiting),
	)
	notifier := newFakeFollowNotifier()
	follow, err := sse.NewCatchUpFollow(reader, notifier, sse.FollowPolicy{
		PageSize: 10, PollInterval: soakPollInterval, HeartbeatInterval: soakHeartbeatInterval,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var heartbeats atomic.Int64
	var lastSeq atomic.Int64
	result := make(chan error, 1)
	go func() {
		result <- follow.FollowWithHeartbeat(ctx, scope, 0,
			func(page []protocolevent.ProtocolEvent) error {
				for _, event := range page {
					lastSeq.Store(event.Sequence)
				}
				return nil
			},
			func(time.Time) error {
				heartbeats.Add(1)
				return nil
			},
		)
	}()

	// Wait until waiting state delivered and at least one heartbeat while open.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if lastSeq.Load() >= 2 && heartbeats.Load() >= 1 {
			break
		}
		time.Sleep(soakPollInterval)
	}
	if lastSeq.Load() != 2 {
		t.Fatalf("expected run.waiting at seq 2, last=%d", lastSeq.Load())
	}
	if heartbeats.Load() < 1 {
		t.Fatal("expected heartbeats while waiting for approval")
	}

	// Approval resume continues the long connection (still non-terminal), then complete.
	reader.append(soakEvent(scope, 3, protocolevent.EventRunResumed))
	notifier.notify()
	reader.append(soakEvent(scope, 4, protocolevent.EventRunCompleted))
	notifier.notify()

	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiting-approval follow did not complete after resume")
	}
	if lastSeq.Load() != 4 {
		t.Fatalf("final sequence=%d want=4", lastSeq.Load())
	}
	t.Logf("waitingApproval heartbeats=%d final_seq=%d", heartbeats.Load(), lastSeq.Load())
}

func testAAPSoakTokenReconnect(t *testing.T) {
	// Token expiry disconnects without advancing cursor; resume with fresh token.
	changes := agentaccessauth.NewInProcessSecurityChanges()
	defer changes.Close()
	authorizer := agentaccessauth.NewControlledStreamAuthorizer()
	revalidator, err := agentaccessauth.NewStreamRevalidator(
		authorizer, changes, agentaccessauth.RevalidationPolicy{Interval: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := agentaccessauth.StreamBinding{
		WorkspaceID:     "a0000000-0000-4000-8000-000000000001",
		AgentID:         "a0000000-0000-4000-8000-000000000002",
		ClientID:        "a0000000-0000-4000-8000-000000000003",
		GrantID:         "a0000000-0000-4000-8000-000000000004",
		PrincipalID:     "a0000000-0000-4000-8000-000000000005",
		SubjectID:       "a0000000-0000-4000-8000-000000000006",
		SecurityVersion: 1,
		TokenExpiresAt:  time.Now().UTC().Add(20 * time.Millisecond),
	}
	err = revalidator.Monitor(context.Background(), binding)
	if !errors.Is(err, agentaccessauth.ErrTokenExpired) {
		t.Fatalf("token expiry result=%v", err)
	}
	if agentaccessauth.StreamErrorCode(err) != "TOKEN_EXPIRED" {
		t.Fatalf("error code=%q", agentaccessauth.StreamErrorCode(err))
	}

	var signal bytes.Buffer
	if err := sse.NewEncoder().EncodeStreamError(&signal, sse.NewStreamErrorSignal(
		"TOKEN_EXPIRED", "access token expired", true,
		"req-soak-token", "trace-soak-token", nil, time.Now().UTC(),
	)); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(signal.Bytes(), []byte("id: ")) {
		t.Fatalf("token signal must not advance cursor: %s", signal.String())
	}

	// Resume from the same cursor after "renewal".
	const resumeCursor int64 = 7
	scope := soakScope("token-resume")
	reader := newSyntheticFollowReader(scope, 12, true)
	// Pre-seed high watermark already at 12; resume from 7 delivers 8..12.
	notifier := newFakeFollowNotifier()
	follow, err := sse.NewCatchUpFollow(reader, notifier, sse.FollowPolicy{
		PageSize: 5, PollInterval: soakPollInterval, HeartbeatInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	var first int64 = -1
	var last int64
	var count int64
	if err := follow.Follow(context.Background(), scope, resumeCursor, func(page []protocolevent.ProtocolEvent) error {
		for _, event := range page {
			if first < 0 {
				first = event.Sequence
			}
			last = event.Sequence
			count++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if first != resumeCursor+1 || last != 12 || count != 12-resumeCursor {
		t.Fatalf("resume sequences first=%d last=%d count=%d (cursor=%d)", first, last, count, resumeCursor)
	}
	t.Logf("tokenReconnect resumed_from=%d delivered=%d..%d", resumeCursor, first, last)
}

func testAAPSoakResourceStability(t *testing.T) {
	runtime.GC()
	before := runtime.NumGoroutine()
	policy := sse.DefaultBackpressurePolicy()
	limiter, err := sse.NewInMemoryConnectionLimiter(policy)
	if err != nil {
		t.Fatal(err)
	}

	const rounds = 20
	for round := 0; round < rounds; round++ {
		scope := soakScope(fmt.Sprintf("stable-%d", round))
		lease, err := limiter.Acquire(context.Background(), sse.ConnectionIdentity{
			ClientID:  fmt.Sprintf("cl-stable-%d", round%policy.MaxConnectionsPerClient),
			SubjectID: fmt.Sprintf("sub-stable-%d", round%policy.MaxConnectionsPerSubject),
			RunID:     scope.RunID,
		})
		if err != nil {
			t.Fatal(err)
		}
		reader := newSyntheticFollowReader(scope, 50, true)
		notifier := newFakeFollowNotifier()
		follow, err := sse.NewCatchUpFollow(reader, notifier, sse.FollowPolicy{
			PageSize: 25, PollInterval: soakPollInterval, HeartbeatInterval: time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := follow.Follow(context.Background(), scope, 0, func([]protocolevent.ProtocolEvent) error {
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
	}
	stats := limiter.Stats()
	if stats.Active != 0 {
		t.Fatalf("active leases after soak: %+v", stats)
	}
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after-before > soakGoroutineLeakBudget {
		t.Fatalf("goroutine growth %d -> %d exceeds budget %d", before, after, soakGoroutineLeakBudget)
	}
	t.Logf("resourceStability rounds=%d goroutines %d->%d limiter=%+v", rounds, before, after, stats)
}

func testAAPSoakCapacityDefaults(t *testing.T) {
	bp := sse.DefaultBackpressurePolicy()
	if err := bp.Validate(); err != nil {
		t.Fatal(err)
	}
	// Design §12.1 / checklist defaults — fail if defaults drift without intentional review.
	if bp.MaxPendingEvents != 100 || bp.MaxCatchUpBatches != 1000 ||
		bp.WriteTimeout != 5*time.Second ||
		bp.MaxConnectionsPerClient != 16 || bp.MaxConnectionsPerSubject != 8 ||
		bp.MaxConnectionsPerRun != 4 {
		t.Fatalf("backpressure defaults drifted: %+v", bp)
	}
	fp := sse.DefaultFollowPolicy()
	if fp.PageSize != 100 || fp.PollInterval != time.Second || fp.HeartbeatInterval != 15*time.Second {
		t.Fatalf("follow defaults drifted: %+v", fp)
	}
	rp := agentaccessauth.DefaultRevalidationPolicy()
	if rp.Interval != 60*time.Second {
		t.Fatalf("revalidation default drifted: %+v", rp)
	}
}

// --- helpers -----------------------------------------------------------------

func soakScope(suffix string) protocolevent.RunScope {
	// FNV-1a style hash over the full suffix so client-0..N get distinct Run IDs.
	var hash uint64 = 14695981039346656037
	for i := 0; i < len(suffix); i++ {
		hash ^= uint64(suffix[i])
		hash *= 1099511628211
	}
	conv := fmt.Sprintf("%012x", hash&0xffffffffffff)
	run := fmt.Sprintf("%012x", (hash>>16)&0xffffffffffff)
	return protocolevent.RunScope{
		WorkspaceID:    "b1000000-0000-4000-8000-00000000aa01",
		AgentID:        "b2000000-0000-4000-8000-00000000aa02",
		ConversationID: "b0000000-0000-4000-8000-" + conv,
		RunID:          "c0000000-0000-4000-8000-" + run,
	}
}

func soakEvent(scope protocolevent.RunScope, sequence int64, eventType string) protocolevent.ProtocolEvent {
	return protocolevent.ProtocolEvent{
		WorkspaceID: scope.WorkspaceID, AgentID: scope.AgentID,
		ConversationID: scope.ConversationID, RunID: scope.RunID,
		StreamID: "run:" + scope.RunID, Sequence: sequence, Type: eventType,
	}
}

// syntheticFollowReader generates sequences on demand (no full materialization of 100k events).
type syntheticFollowReader struct {
	scope     protocolevent.RunScope
	total     int64
	terminal  bool
	mu        sync.Mutex
	maxFlight int
}

func newSyntheticFollowReader(scope protocolevent.RunScope, total int64, terminal bool) *syntheticFollowReader {
	return &syntheticFollowReader{scope: scope, total: total, terminal: terminal}
}

func (reader *syntheticFollowReader) maxInFlight() int {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.maxFlight
}

func (reader *syntheticFollowReader) HighWatermark(
	_ context.Context,
	_ protocolevent.RunScope,
) (int64, error) {
	return reader.total, nil
}

func (reader *syntheticFollowReader) ReadRunAfter(
	_ context.Context,
	scope protocolevent.RunScope,
	after int64,
	limit int,
) ([]protocolevent.ProtocolEvent, error) {
	if after >= reader.total {
		return nil, nil
	}
	end := after + int64(limit)
	if end > reader.total {
		end = reader.total
	}
	page := make([]protocolevent.ProtocolEvent, 0, end-after)
	for seq := after + 1; seq <= end; seq++ {
		eventType := protocolevent.EventItemDelta
		if seq == 1 {
			eventType = protocolevent.EventRunStarted
		}
		if reader.terminal && seq == reader.total {
			eventType = protocolevent.EventRunCompleted
		}
		page = append(page, protocolevent.ProtocolEvent{
			WorkspaceID: scope.WorkspaceID, AgentID: scope.AgentID,
			ConversationID: scope.ConversationID, RunID: scope.RunID,
			StreamID: "run:" + scope.RunID, Sequence: seq, Type: eventType,
		})
	}
	reader.mu.Lock()
	if len(page) > reader.maxFlight {
		reader.maxFlight = len(page)
	}
	reader.mu.Unlock()
	return page, nil
}
