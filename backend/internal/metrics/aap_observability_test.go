package metrics_test

import (
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/metrics"
)

// TestAAPObservability is the M10-T4 gate for process-local AAP metrics:
// design §12.3 counters/gauges, label allowlist (Client/Agent/Run join keys),
// and Prometheus text exposition without secret-bearing dimensions.
func TestAAPObservability(t *testing.T) {
	t.Run("CollectsDesignSection123Series", testAAPMetricsDesignSeries)
	t.Run("LabelsOnlyAllowlistedIndexKeys", testAAPMetricsLabelAllowlist)
	t.Run("PrometheusTextExposesOpsSignals", testAAPMetricsPrometheusText)
	t.Run("DashboardJoinKeysLocateClientAgentRun", testAAPMetricsDashboardJoin)
}

func testAAPMetricsDesignSeries(t *testing.T) {
	c := metrics.NewAAPCollector()
	labels := map[string]string{
		"workspace_id": "ws-obs-1",
		"agent_id":     "ag-obs-1",
		"client_id":    "cl-obs-1",
		"run_id":       "run-obs-1",
	}

	c.ObserveProtocolEventAppend(12*time.Millisecond, labels)
	c.ObserveProtocolEventAppendError(labels)
	c.ObserveSequenceConflict(labels)
	c.SetSSEActiveConnections(3)
	c.ObserveSSEReconnect(labels)
	c.ObserveSSEReplay(42, 150*time.Millisecond, labels)
	c.ObserveSSESlowConsumerDisconnect(labels)
	c.SetRunWaitingInteraction(2)
	c.ObserveWaitingApprovalAge(45 * time.Minute)
	c.ObserveTokenIssue(true, labels)
	c.ObserveTokenIssue(false, map[string]string{"client_id": "cl-obs-1", "reason": "CREDENTIAL_REJECTED"})
	c.ObserveCredentialLastUsedAge(72 * time.Hour)
	c.ObserveAuthorizationDenied(map[string]string{
		"client_id": "cl-obs-1", "reason": "SCOPE_MISSING", "scope": "run:create",
	})
	c.ObserveFanoutNotifyFailure(labels)

	s := c.Snapshot()
	assertUint(t, "protocol_event_append_total", s.ProtocolEventAppendTotal, 1)
	assertUint(t, "protocol_event_append_errors_total", s.ProtocolEventAppendErrors, 1)
	assertUint(t, "protocol_event_sequence_conflict_total", s.ProtocolEventSequenceConflict, 1)
	assertInt(t, "sse_active_connections", s.SSEActiveConnections, 3)
	assertUint(t, "sse_reconnect_total", s.SSEReconnectTotal, 1)
	assertUint(t, "sse_replay_events_total", s.SSEReplayEventsTotal, 42)
	assertUint(t, "sse_slow_consumer_disconnect_total", s.SSESlowConsumerDisconnects, 1)
	assertInt(t, "run_waiting_interaction", s.RunWaitingInteraction, 2)
	assertUint(t, "token_issue_total", s.TokenIssueTotal, 1)
	assertUint(t, "token_issue_failure_total", s.TokenIssueFailureTotal, 1)
	assertUint(t, "authorization_denied_total", s.AuthorizationDeniedTotal, 1)
	assertUint(t, "fanout_notify_failure_total", s.FanoutNotifyFailureTotal, 1)

	if s.ProtocolAppendLatencyCount < 1 || s.ProtocolAppendLatencyMaxMs < 12 {
		t.Fatalf("append latency window not recorded: %+v", s)
	}
	if s.SSEReplayLagCount < 1 || s.SSEReplayLagMaxMs < 150 {
		t.Fatalf("replay lag window not recorded: %+v", s)
	}
	if s.WaitingApprovalAgeCount < 1 || s.WaitingApprovalAgeMaxMs < int64((45*time.Minute).Milliseconds()) {
		t.Fatalf("waiting approval age not recorded: %+v", s)
	}
	if s.CredentialLastUsedCount < 1 || s.CredentialLastUsedMaxMs < int64((72*time.Hour).Milliseconds()) {
		t.Fatalf("credential last used age not recorded: %+v", s)
	}
}

func testAAPMetricsLabelAllowlist(t *testing.T) {
	c := metrics.NewAAPCollector()
	// Forbidden labels (token/secret/payload) must be dropped, not recorded.
	c.ObserveAuthorizationDenied(map[string]string{
		"client_id":    "cl-safe",
		"reason":       "TOKEN_EXPIRED",
		"access_token": "eyJhbGciOiJIUzI1NiJ9.payload.sig",
		"password":     "super-secret",
		"payload":      `{"prompt":"raw thought"}`,
	})
	s := c.Snapshot()
	for key := range s.Labeled {
		lower := strings.ToLower(key)
		for _, forbidden := range []string{"access_token", "password", "payload", "eyj", "super-secret", "raw thought"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("labeled metrics leaked forbidden material %q in key %q", forbidden, key)
			}
		}
	}
	// Allowed reason/client still present.
	found := false
	for key, value := range s.Labeled {
		if strings.Contains(key, "authorization_denied_total") &&
			strings.Contains(key, "client_id=cl-safe") &&
			strings.Contains(key, "reason=TOKEN_EXPIRED") &&
			value == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected allowlisted labeled counter; got %#v", s.Labeled)
	}
}

func testAAPMetricsPrometheusText(t *testing.T) {
	c := metrics.NewAAPCollector()
	c.ObserveSequenceConflict(map[string]string{"run_id": "run-prome"})
	c.SetSSEActiveConnections(5)
	c.ObserveSSEReplay(10, 200*time.Millisecond, nil)
	c.ObserveWaitingApprovalAge(time.Hour)
	c.ObserveTokenIssue(false, map[string]string{"reason": "INVALID_CLIENT"})
	c.ObserveProtocolEventAppend(3*time.Millisecond, map[string]string{
		"workspace_id": "ws-p", "client_id": "cl-p", "run_id": "run-prome",
	})

	text := c.PrometheusText()
	for _, series := range []string{
		"aap_protocol_event_sequence_conflict_total",
		"aap_sse_active_connections",
		"aap_sse_replay_lag_p95_ms",
		"aap_run_waiting_interaction_age_p95_ms",
		"aap_token_issue_failure_total",
		"aap_credential_last_used_age_max_ms",
		"aap_fanout_notify_failure_total",
		"aap_authorization_denied_total",
	} {
		if !strings.Contains(text, series) {
			t.Fatalf("prometheus text missing %q:\n%s", series, text)
		}
	}
	// Labeled series must appear so dashboards can drill by Client/Run.
	if !strings.Contains(text, `client_id="cl-p"`) ||
		!strings.Contains(text, `run_id="run-prome"`) {
		t.Fatalf("prometheus text missing labeled drill-down:\n%s", text)
	}
	// No secret-like tokens in exposition.
	lower := strings.ToLower(text)
	for _, forbidden := range []string{"bearer ", "eyj", "awsk_", "password"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("prometheus text contains forbidden marker %q", forbidden)
		}
	}
}

func testAAPMetricsDashboardJoin(t *testing.T) {
	c := metrics.NewAAPCollector()
	// Simulate two clients / agents so dashboards can drill down.
	c.ObserveProtocolEventAppend(5*time.Millisecond, map[string]string{
		"workspace_id": "ws-1", "agent_id": "ag-a", "client_id": "cl-a", "run_id": "run-1",
	})
	c.ObserveProtocolEventAppend(8*time.Millisecond, map[string]string{
		"workspace_id": "ws-1", "agent_id": "ag-b", "client_id": "cl-b", "run_id": "run-2",
	})
	c.ObserveSSEReconnect(map[string]string{
		"workspace_id": "ws-1", "client_id": "cl-a", "run_id": "run-1",
	})

	s := c.Snapshot()
	var sawClientA, sawClientB, sawAgentA, sawRun1 bool
	for key := range s.Labeled {
		if strings.Contains(key, "client_id=cl-a") {
			sawClientA = true
		}
		if strings.Contains(key, "client_id=cl-b") {
			sawClientB = true
		}
		if strings.Contains(key, "agent_id=ag-a") {
			sawAgentA = true
		}
		if strings.Contains(key, "run_id=run-1") {
			sawRun1 = true
		}
	}
	if !sawClientA || !sawClientB || !sawAgentA || !sawRun1 {
		t.Fatalf("dashboard join keys incomplete: a=%v b=%v agent=%v run=%v labeled=%#v",
			sawClientA, sawClientB, sawAgentA, sawRun1, s.Labeled)
	}
}

func assertUint(t *testing.T, name string, got, want uint64) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %d want %d", name, got, want)
	}
}

func assertInt(t *testing.T, name string, got, want int64) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %d want %d", name, got, want)
	}
}
