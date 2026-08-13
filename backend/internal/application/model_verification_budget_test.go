package application

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
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
// The three probes run sequentially inside one Verify call
// (probeAgenticCapabilities: stream, tool-search, then function-calling on a
// capability miss), so the outer budget must be at least their sum. The inner
// budgets are the probe contract and must stay as they are — this test pins
// them so the relation can never be "fixed" by shrinking a probe budget
// instead of raising the outer one.
func TestModelVerificationOuterBudgetCoversInnerProbeBudgets(t *testing.T) {
	if agenticProbeResponsesStreamBudget != 30*time.Second {
		t.Fatalf("inner Responses-stream probe budget must stay 30s, got %v",
			agenticProbeResponsesStreamBudget)
	}
	if agenticProbeClientToolSearchBudget != 45*time.Second {
		t.Fatalf("inner client tool_search probe budget must stay 45s, got %v",
			agenticProbeClientToolSearchBudget)
	}
	if agenticProbeFunctionCallingBudget != 30*time.Second {
		t.Fatalf("inner function-calling probe budget must stay 30s, got %v",
			agenticProbeFunctionCallingBudget)
	}

	innerTotal := agenticProbeResponsesStreamBudget + agenticProbeClientToolSearchBudget + agenticProbeFunctionCallingBudget
	defaultOuter := modelVerificationTimeout(config.RuntimeConfig{})
	if defaultOuter < innerTotal {
		t.Fatalf("default outer verification budget %v cannot contain sequential inner budgets %v",
			defaultOuter, innerTotal)
	}
	// The default also has to leave room for the GET /models auth probe that runs
	// before the agentic probes and has no deadline of its own (30+45+30+15).
	if defaultOuter <= innerTotal {
		t.Fatalf("default outer budget %v leaves nothing for the auth/connectivity probe (inner total %v)",
			defaultOuter, innerTotal)
	}
	if defaultOuter != 120*time.Second {
		t.Fatalf("default outer verification budget: got %v want 120s", defaultOuter)
	}
}

// TestVerifierAuthProbeFailsClosedOnStalledBody pins the auth/connectivity probe
// against a fail-open path: the probe used to discard the error from draining the
// response body, so an upstream that answered 200 on the status line and then
// never delivered the body was reported as authenticated. Verification is a gate;
// an upstream that cannot complete a 4KB GET has not demonstrated connectivity
// and must not pass this probe.
func TestVerifierAuthProbeFailsClosedOnStalledBody(t *testing.T) {
	stall := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Promise a body, commit the 200, then never deliver it.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-stall
	}))
	// Cleanup is LIFO: release the handler before Close waits on it.
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(stall) })

	// A context-bound client is honoured verbatim, so only the drain and the
	// context can end this probe — this is about the drain, not the client.
	verifier := &modelConfigVerifier{client: &http.Client{Timeout: 0}, egress: loopbackModelEgress}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	err := verifier.probeAuthConnectivity(ctx, newVerifierConfig(server.URL))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("auth probe reported success against an upstream that never delivered its body")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled body must be classified as the context deadline, got %v", err)
	}
	// The failure must come from the context, proving the drain stays bounded by
	// the verification budget instead of hanging on its own.
	if elapsed < time.Second {
		t.Fatalf("probe failed in %v, before the context deadline could bind", elapsed)
	}
}

// TestVerifierAuthProbeClassifiesStatusBeforeBody pins the ordering half of the
// same fix: a rejected upstream is reported as a rejection even when its body
// never completes, rather than being reclassified as a network/timeout failure.
func TestVerifierAuthProbeClassifiesStatusBeforeBody(t *testing.T) {
	stall := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusUnauthorized)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-stall
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(stall) })

	verifier := &modelConfigVerifier{client: &http.Client{Timeout: 0}, egress: loopbackModelEgress}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	err := verifier.probeAuthConnectivity(ctx, newVerifierConfig(server.URL))
	if !errors.Is(err, modelconfig.ErrUpstreamAuthentication) {
		t.Fatalf("401 with a stalled body must classify as authentication, got %v", err)
	}
	// Classification happens on the status line, so it cannot wait for the body.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("401 classification waited %v on the body", elapsed)
	}
}

// TestModelVerificationTimeoutWiring locks how Open resolves the outer budget:
// omitted config defaults to 120s, an explicit value is used verbatim, and a
// hostile value is not repaired here but rejected by the consuming boundary.
func TestModelVerificationTimeoutWiring(t *testing.T) {
	for _, test := range []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{name: "omitted defaults to 120s", seconds: 0, want: 120 * time.Second},
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
			return modelconfig.AgenticCapabilities{ToolCalling: modelconfig.ToolCallingNativeClientSearch}, nil
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

// TestVerifierProbeClientCannotCutBelowTheBudgets closes the second half of the
// R11-1 defect. Raising the outer budget to 120s did not make the inner budgets
// reachable, because Open injects the shared 15s application HTTP client into
// the verifier: http.Client.Timeout applies per request and is not extended by
// the surrounding context, so every probe died at 15s and the configured budget
// was unreachable no matter what the operator set. Measured against a stalling
// upstream, verification returned MODEL_CONFIG_VERIFICATION_TIMEOUT in 15.05s.
func TestVerifierProbeClientCannotCutBelowTheBudgets(t *testing.T) {
	// The floor must stay tied to the inner budgets rather than a literal, so
	// raising a probe budget cannot silently make the floor too low again.
	if modelVerificationMinClientTimeout < agenticProbeClientToolSearchBudget {
		t.Fatalf("client timeout floor %v is below the largest inner probe budget %v",
			modelVerificationMinClientTimeout, agenticProbeClientToolSearchBudget)
	}

	sharedAppClient := &http.Client{Timeout: 15 * time.Second}
	longClient := &http.Client{Timeout: 10 * time.Minute}
	contextBoundClient := &http.Client{Timeout: 0}

	for _, test := range []struct {
		name     string
		injected *http.Client
		wantSame bool
	}{
		{name: "shared 15s app client is refused", injected: sharedAppClient},
		{name: "just below the floor is refused", injected: &http.Client{
			Timeout: modelVerificationMinClientTimeout - time.Millisecond,
		}},
		{name: "no client falls back", injected: nil},
		{name: "context-bound client is honoured", injected: contextBoundClient, wantSame: true},
		{name: "long client is honoured", injected: longClient, wantSame: true},
		{name: "exactly at the floor is honoured", injected: &http.Client{
			Timeout: modelVerificationMinClientTimeout,
		}, wantSame: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			verifier := &modelConfigVerifier{client: test.injected}
			got := verifier.probeHTTPClient()
			if got == nil {
				t.Fatal("probe client must never be nil")
			}
			if test.wantSame {
				if got != test.injected {
					t.Fatalf("injected client must be used verbatim, got a substitute")
				}
				return
			}
			if got == test.injected {
				t.Fatal("a client that cuts below the probe budgets must not be used")
			}
			// The substitute must itself be unable to cut a probe short.
			if got.Timeout != 0 && got.Timeout < modelVerificationMinClientTimeout {
				t.Fatalf("substitute client still cuts probes short: Timeout=%v", got.Timeout)
			}
			// The substitute is reused across probes. Verify runs several probes
			// back to back against one upstream, and every streaming client owns a
			// fresh transport with its own pool, so rebuilding per call would both
			// prevent connection reuse and leak an idle pool per probe.
			if again := verifier.probeHTTPClient(); again != got {
				t.Fatal("substitute client must be reused across probes, not rebuilt per call")
			}
			if verifier.probeHTTPClient().Transport != got.Transport {
				t.Fatal("substitute transport must be reused, or each probe gets its own connection pool")
			}
		})
	}

	// The reused fallback must be per verifier, not shared process-wide: two
	// verifiers may carry different upstreams and must not share a pool.
	first := &modelConfigVerifier{client: sharedAppClient}
	second := &modelConfigVerifier{client: sharedAppClient}
	if first.probeHTTPClient() == second.probeHTTPClient() {
		t.Fatal("distinct verifiers must not share one fallback client")
	}

	// Concurrent probes must observe one client (the -race gate covers the
	// once-guarded write; this pins the single-instance guarantee).
	shared := &modelConfigVerifier{client: sharedAppClient}
	clients := make(chan *http.Client, 8)
	var wg sync.WaitGroup
	for i := 0; i < cap(clients); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			clients <- shared.probeHTTPClient()
		}()
	}
	wg.Wait()
	close(clients)
	want := <-clients
	for got := range clients {
		if got != want {
			t.Fatal("concurrent probes must share one fallback client")
		}
	}

	// A nil receiver must not panic on the probe path.
	var nilVerifier *modelConfigVerifier
	if nilVerifier.probeHTTPClient() == nil {
		t.Fatal("nil verifier must still yield a usable probe client")
	}
}
