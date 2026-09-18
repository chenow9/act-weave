package chatruntimebridge_test

import (
	"testing"
	"time"

	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/einoruntime"
)

// TestNewBridge_MaxToolInvocationsContract locks the production-wide budget
// invariant at the bridge boundary: 0 → default 32; 1..64 valid; -1 and 65 fail
// closed (never silently defaulted or clamped).
func TestNewBridge_MaxToolInvocationsContract(t *testing.T) {
	t.Parallel()

	// Reuse construction stubs from continue_test.go (same external test package).
	base := func(maxTools int) chatruntimebridge.Dependencies {
		return chatruntimebridge.Dependencies{
			Sessions:           &bridgeSessions{},
			Results:            &bridgeResults{},
			Agents:             bridgeAgents{},
			Models:             bridgeModels{},
			Runs:               &bridgeRuns{},
			Events:             bridgeEvents{},
			AgenticEngine:      einoruntime.NewAgenticEngine(einoruntime.AgenticEngineConfig{}),
			MaxToolInvocations: maxTools,
		}
	}

	// Boundaries that must succeed.
	for _, max := range []int{0, 1, 16, 32, 64} {
		b, err := chatruntimebridge.NewBridge(base(max))
		if err != nil {
			t.Fatalf("MaxToolInvocations=%d: unexpected error: %v", max, err)
		}
		if b == nil {
			t.Fatalf("MaxToolInvocations=%d: nil bridge", max)
		}
	}

	// Adversarial invalid values must fail closed.
	for _, max := range []int{-1, 65, -3, 100} {
		b, err := chatruntimebridge.NewBridge(base(max))
		if err == nil {
			t.Fatalf("MaxToolInvocations=%d: expected error, got bridge=%v", max, b)
		}
		if b != nil {
			t.Fatalf("MaxToolInvocations=%d: expected nil bridge on error", max)
		}
	}
}

func TestNewBridge_RunTimeoutContract(t *testing.T) {
	t.Parallel()
	base := func(timeout time.Duration) chatruntimebridge.Dependencies {
		return chatruntimebridge.Dependencies{
			Sessions:      &bridgeSessions{},
			Results:       &bridgeResults{},
			Agents:        bridgeAgents{},
			Models:        bridgeModels{},
			Runs:          &bridgeRuns{},
			Events:        bridgeEvents{},
			AgenticEngine: einoruntime.NewAgenticEngine(einoruntime.AgenticEngineConfig{}),
			RunTimeout:    timeout,
		}
	}
	for _, timeout := range []time.Duration{0, time.Millisecond, 5 * time.Minute, 30 * time.Minute} {
		b, err := chatruntimebridge.NewBridge(base(timeout))
		if err != nil {
			t.Fatalf("RunTimeout=%s: unexpected error: %v", timeout, err)
		}
		if b == nil {
			t.Fatalf("RunTimeout=%s: nil bridge", timeout)
		}
	}
	b, err := chatruntimebridge.NewBridge(base(-time.Second))
	if err == nil || b != nil {
		t.Fatalf("negative RunTimeout must fail closed, bridge=%v err=%v", b, err)
	}
}
