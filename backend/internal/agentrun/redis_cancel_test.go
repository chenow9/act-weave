package agentrun

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/redisx"

	"github.com/alicebob/miniredis/v2"
)

type cancelProbeRuntime struct {
	mu      sync.Mutex
	cancels [][2]string
}

func (s *cancelProbeRuntime) Enqueue(Job) {}
func (s *cancelProbeRuntime) EnqueueContinueWithLifecycle(Job, json.RawMessage, json.RawMessage, ContinueLifecycle) {
}
func (s *cancelProbeRuntime) CancelRun(workspaceID, runID string) error {
	s.mu.Lock()
	s.cancels = append(s.cancels, [2]string{workspaceID, runID})
	s.mu.Unlock()
	return nil
}

func TestRedisCancelBusReachesOtherReplica(t *testing.T) {
	mini := miniredis.RunT(t)
	pub, err := redisx.Open(context.Background(), redisx.Config{Addr: mini.Addr(), KeyPrefix: "t"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := redisx.Open(context.Background(), redisx.Config{Addr: mini.Addr(), KeyPrefix: "t"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close(); _ = sub.Close() })

	local := &cancelProbeRuntime{}
	bus, err := NewRedisCancelBus(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	go bus.Listen(local)

	publisher, err := NewRedisCancelBus(context.Background(), pub)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = publisher.Close() })
	if err := publisher.Publish(context.Background(), "ws-1", "run-1"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		local.mu.Lock()
		n := len(local.cancels)
		local.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("cancel was not delivered to the other replica")
}
