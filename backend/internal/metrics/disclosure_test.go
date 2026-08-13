package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestDisclosureMetricsPrometheusAndObserve(t *testing.T) {
	t.Parallel()
	c := NewDisclosureCollector()
	c.ObserveModeRun(DisclosureModeClientBounded, DisclosureToolCallingNative)
	c.ObserveModeRun(DisclosureModePlatformBounded, DisclosureToolCallingFunction)
	c.ObserveModeRun(DisclosureModeCarryAll, DisclosureToolCallingFunction)
	c.ObserveModeRun(DisclosureModeNone, DisclosureToolCallingNone)
	c.ObserveRejected(DisclosureCodeNotRolledOut)
	c.ObserveRejected(DisclosureCodeCarryAllTooLarge)
	c.ObserveCarryAllRejected(DisclosureGateRunStart)
	c.ObserveCarryAllRejected(DisclosureGateBind)
	c.ObserveCarryAllRejected(DisclosureGateSetDisclosure)
	c.ObserveSearchCall(DisclosureModePlatformBounded, DisclosureOutcomeLoadCap)
	c.ObserveSearchCall(DisclosureModeClientBounded, DisclosureOutcomeOK)
	c.ObserveSearchLoaded(DisclosureModePlatformBounded, 3)
	c.ObserveVerification(DisclosurePhaseResult, DisclosureOutcomeOK, DisclosureToolCallingFunction, 0)
	c.ObserveVerification(DisclosurePhaseAuth, DisclosureOutcomeOK, DisclosureToolCallingUnverified, 12*time.Millisecond)

	text := c.PrometheusText()
	for _, needle := range []string{
		`agentic_disclosure_mode_runs_total{mode="client_bounded",tool_calling="native_client_search"} 1`,
		`agentic_disclosure_mode_runs_total{mode="platform_bounded",tool_calling="function_calling"} 1`,
		`agentic_disclosure_mode_runs_total{mode="carry_all",tool_calling="function_calling"} 1`,
		`agentic_disclosure_mode_runs_total{mode="none",tool_calling="none"} 1`,
		`agentic_disclosure_rejected_total{code="MODEL_TOOL_DISCLOSURE_NOT_ROLLED_OUT"} 1`,
		`agentic_disclosure_rejected_total{code="MODEL_TOOL_CARRY_ALL_TOO_LARGE"} 1`,
		`agentic_carry_all_rejected_total{gate="run_start"} 1`,
		`agentic_carry_all_rejected_total{gate="bind"} 1`,
		`agentic_carry_all_rejected_total{gate="set_disclosure"} 1`,
		`agentic_tool_search_calls_total{mode="platform_bounded",outcome="load_cap"} 1`,
		`agentic_tool_search_calls_total{mode="client_bounded",outcome="ok"} 1`,
		`agentic_tool_search_loaded_tools_last{mode="platform_bounded"} 3`,
		`model_verification_total{phase="result",outcome="ok",tool_calling="function_calling"} 1`,
		`model_verification_duration_ms_count{phase="auth"} 1`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("missing %q in:\n%s", needle, text)
		}
	}
}

func TestDisclosureMetricsDropHostileLabels(t *testing.T) {
	t.Parallel()
	c := NewDisclosureCollector()
	c.ObserveModeRun("alpha_tool", "select:secret-query")
	c.ObserveRejected("workspace-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	c.ObserveSearchCall(`{"query":"leak"}`, "alpha_tool")
	c.ObserveVerification("gpt-5.4", "schema-body", "actweave_catalog_search", 0)

	text := c.PrometheusText()
	for _, leak := range []string{
		"alpha_tool", "select:secret-query", "workspace-aaaaaaaa",
		"secret-query", "gpt-5.4", "schema-body", "actweave_catalog_search",
	} {
		if strings.Contains(text, leak) {
			t.Fatalf("hostile label leaked %q:\n%s", leak, text)
		}
	}
	for _, needle := range []string{
		`agentic_disclosure_mode_runs_total{mode="unknown",tool_calling="unknown"} 1`,
		`agentic_disclosure_rejected_total{code="unknown"} 1`,
		`agentic_tool_search_calls_total{mode="unknown",outcome="unknown"} 1`,
		`model_verification_total{phase="unknown",outcome="unknown",tool_calling="unknown"} 1`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("unknown collapse missing %q in:\n%s", needle, text)
		}
	}
}

func TestDisclosureObserversTolerateNilCollector(t *testing.T) {
	t.Parallel()
	var c *DisclosureCollector
	c.ObserveModeRun(DisclosureModeNone, DisclosureToolCallingNone)
	c.ObserveRejected(DisclosureCodeNotRolledOut)
	c.ObserveCarryAllRejected(DisclosureGateBind)
	c.ObserveSearchCall(DisclosureModePlatformBounded, DisclosureOutcomeLoadCap)
	c.ObserveSearchLoaded(DisclosureModePlatformBounded, 1)
	c.ObserveVerification(DisclosurePhaseResult, DisclosureOutcomeOK, DisclosureToolCallingNone, 0)
	if text := c.PrometheusText(); text != "" {
		t.Fatalf("nil collector must render empty, got %q", text)
	}
}
