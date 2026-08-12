package chatruntimebridge_test

import (
	"testing"

	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/einoruntime"
)

// TestNewBridge_MaxToolInvocationsContract locks the production-wide budget
// invariant at the bridge boundary: 0 → default 16; 1..16 valid; -1 and 17 fail
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
	for _, max := range []int{0, 1, 16} {
		b, err := chatruntimebridge.NewBridge(base(max))
		if err != nil {
			t.Fatalf("MaxToolInvocations=%d: unexpected error: %v", max, err)
		}
		if b == nil {
			t.Fatalf("MaxToolInvocations=%d: nil bridge", max)
		}
	}

	// Adversarial invalid values must fail closed.
	for _, max := range []int{-1, 17, -3, 100} {
		b, err := chatruntimebridge.NewBridge(base(max))
		if err == nil {
			t.Fatalf("MaxToolInvocations=%d: expected error, got bridge=%v", max, b)
		}
		if b != nil {
			t.Fatalf("MaxToolInvocations=%d: expected nil bridge on error", max)
		}
	}
}
