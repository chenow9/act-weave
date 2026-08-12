package application

import (
	"context"
	"testing"
	"time"

	"actweave/backend/internal/config"
	"actweave/backend/internal/modelconfig"
)

// TestModelVerificationOuterBudgetCoversInnerProbeBudgets is the R11-1
// regression test for the dead-code defect: Open used to hand
// VerificationService a 20s outer budget while the probes nested a 30s
// Responses-stream deadline and a 45s client tool_search deadline inside it, so
// neither inner deadline could ever fire and every slow upstream was reported as
// MODEL_CONFIG_VERIFICATION_TIMEOUT by the outer context instead.
//
// The two probes run sequentially inside one Verify call
// (probeAgenticCapabilities: probe A then probe B), so the outer budget must be
// at least their sum. The inner budgets are Task 3 contract and must stay as
// they are — this test pins them so the relation can never be "fixed" by
// shrinking a probe budget instead of raising the outer one.
func TestModelVerificationOuterBudgetCoversInnerProbeBudgets(t *testing.T) {
	if agenticProbeResponsesStreamBudget != 30*time.Second {
		t.Fatalf("inner Responses-stream probe budget must stay 30s, got %v",
			agenticProbeResponsesStreamBudget)
	}
	if agenticProbeClientToolSearchBudget != 45*time.Second {
		t.Fatalf("inner client tool_search probe budget must stay 45s, got %v",
			agenticProbeClientToolSearchBudget)
	}

	innerTotal := agenticProbeResponsesStreamBudget + agenticProbeClientToolSearchBudget
	defaultOuter := modelVerificationTimeout(config.RuntimeConfig{})
	if defaultOuter < innerTotal {
		t.Fatalf("default outer verification budget %v cannot contain sequential inner budgets %v",
			defaultOuter, innerTotal)
	}
	// The default also has to leave room for the GET /models auth probe that runs
	// before both agentic probes and has no deadline of its own.
	if defaultOuter <= innerTotal {
		t.Fatalf("default outer budget %v leaves nothing for the auth/connectivity probe (inner total %v)",
			defaultOuter, innerTotal)
	}
	if defaultOuter != 90*time.Second {
		t.Fatalf("default outer verification budget: got %v want 90s", defaultOuter)
	}
}

// TestModelVerificationTimeoutWiring locks how Open resolves the outer budget:
// omitted config defaults to 90s, an explicit value is used verbatim, and a
// hostile value is not repaired here but rejected by the consuming boundary.
func TestModelVerificationTimeoutWiring(t *testing.T) {
	for _, test := range []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{name: "omitted defaults to 90s", seconds: 0, want: 90 * time.Second},
		{name: "explicit smaller value honoured", seconds: 1, want: time.Second},
		{name: "explicit larger value honoured", seconds: 120, want: 120 * time.Second},
		{name: "maximum honoured", seconds: config.MaxModelVerificationTimeoutSeconds, want: 600 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := modelVerificationTimeout(config.RuntimeConfig{
				ModelVerification: config.ModelVerificationTuning{TimeoutSeconds: test.seconds},
			})
			if got != test.want {
				t.Fatalf("modelVerificationTimeout(%d): got %v want %v", test.seconds, got, test.want)
			}
		})
	}

	// A negative configured value must survive unchanged to the boundary so Open
	// fails instead of silently running with a defaulted or expired budget.
	negative := modelVerificationTimeout(config.RuntimeConfig{
		ModelVerification: config.ModelVerificationTuning{TimeoutSeconds: -1},
	})
	if negative != -time.Second {
		t.Fatalf("negative budget must not be repaired: got %v", negative)
	}
	verifier := modelconfig.VerifierFunc(
		func(context.Context, modelconfig.Config) (modelconfig.AgenticCapabilities, error) {
			t.Fatal("verifier must never run for a rejected budget")
			return modelconfig.AgenticCapabilities{}, nil
		},
	)
	if _, err := modelconfig.NewVerificationService(&modelconfig.Repository{}, verifier, negative); err == nil {
		t.Fatal("NewVerificationService must reject a negative outer budget")
	}
	if _, err := modelconfig.NewVerificationService(&modelconfig.Repository{}, verifier, 0); err == nil {
		t.Fatal("NewVerificationService must reject a zero outer budget")
	}
	if _, err := modelconfig.NewVerificationService(
		&modelconfig.Repository{}, verifier, modelVerificationTimeout(config.RuntimeConfig{}),
	); err != nil {
		t.Fatalf("NewVerificationService must accept the default budget: %v", err)
	}
}
