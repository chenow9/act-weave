// Package metrics provides process-local AAP observability counters and gauges.
// Values are labeled only with stable index dimensions (workspace/agent/client/run
// codes), never message bodies, tool payloads, or credentials.
package metrics

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Label keys permitted on AAP metrics (dashboard / alert join keys).
var allowedLabelKeys = map[string]struct{}{
	"workspace_id": {},
	"agent_id":     {},
	"client_id":    {},
	"run_id":       {},
	"principal_id": {},
	"operation":    {},
	"reason":       {},
	"scope":        {},
	"code":         {},
	"result":       {},
	// agentrun.Factory Enqueue route (PR15): values are only "eino" | "legacy".
	"engine": {},
	// A2UI catalog signals. Both are normalized against fixed vocabularies in
	// a2ui.go before they reach here, so cardinality stays bounded.
	"keyword":    {},
	"chart_type": {},
}

// AAPCollector is the shared AAP metrics surface for one process.
// Multi-replica deployments scrape each instance and aggregate externally.
type AAPCollector struct {
	// Counters
	protocolEventAppendTotal      atomic.Uint64
	protocolEventAppendErrors     atomic.Uint64
	protocolEventSequenceConflict atomic.Uint64
	sseReconnectTotal             atomic.Uint64
	sseReplayEventsTotal          atomic.Uint64
	sseSlowConsumerDisconnects    atomic.Uint64
	tokenIssueTotal               atomic.Uint64
	tokenIssueFailureTotal        atomic.Uint64
	authorizationDeniedTotal      atomic.Uint64
	fanoutNotifyFailureTotal      atomic.Uint64
	// Eino checkpoint GC (PR3 / D15): operational counters only.
	einoCheckpointCleanupRunsTotal    atomic.Uint64
	einoCheckpointCleanupDeletedTotal atomic.Uint64
	einoCheckpointCleanupErrorsTotal  atomic.Uint64

	// Gauges (absolute)
	sseActiveConnections  atomic.Int64
	runWaitingInteraction atomic.Int64

	// Latency / lag samples (milliseconds) — last-window max for ops signals.
	mu                    sync.Mutex
	protocolAppendLatency msWindow
	sseReplayLag          msWindow
	waitingApprovalAge    msWindow
	credentialLastUsedAge msWindow

	// Dimensioned counters for dashboard drill-down (bounded cardinality).
	labeled labeledCounters
}

type msWindow struct {
	// Simple ring of last N samples for p50/p95-ish ops views without deps.
	samples []int64
	next    int
	filled  bool
}

const windowSize = 64

func (w *msWindow) observe(ms int64) {
	if ms < 0 {
		ms = 0
	}
	if len(w.samples) != windowSize {
		w.samples = make([]int64, windowSize)
	}
	w.samples[w.next] = ms
	w.next = (w.next + 1) % windowSize
	if w.next == 0 {
		w.filled = true
	}
}

func (w *msWindow) snapshot() (count int, max, p95 int64) {
	if len(w.samples) == 0 {
		return 0, 0, 0
	}
	n := windowSize
	if !w.filled {
		n = w.next
	}
	if n == 0 {
		return 0, 0, 0
	}
	copySamples := append([]int64(nil), w.samples[:n]...)
	sort.Slice(copySamples, func(i, j int) bool { return copySamples[i] < copySamples[j] })
	count = n
	max = copySamples[n-1]
	idx := int(float64(n-1) * 0.95)
	if idx < 0 {
		idx = 0
	}
	p95 = copySamples[idx]
	return count, max, p95
}

type labeledCounters struct {
	mu   sync.Mutex
	data map[string]*atomic.Uint64 // key = metric|k=v|k=v
}

func (c *labeledCounters) add(metric string, labels map[string]string, delta uint64) {
	key := labeledKey(metric, labels)
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data == nil {
		c.data = make(map[string]*atomic.Uint64)
	}
	counter, ok := c.data[key]
	if !ok {
		// Bound cardinality to avoid unbounded label explosion.
		if len(c.data) >= 10_000 {
			return
		}
		counter = &atomic.Uint64{}
		c.data[key] = counter
	}
	counter.Add(delta)
}

func (c *labeledCounters) snapshot() map[string]uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]uint64, len(c.data))
	for key, counter := range c.data {
		out[key] = counter.Load()
	}
	return out
}

func labeledKey(metric string, labels map[string]string) string {
	metric = strings.TrimSpace(metric)
	if metric == "" {
		return ""
	}
	if len(labels) == 0 {
		return metric
	}
	keys := make([]string, 0, len(labels))
	for key, value := range labels {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if _, ok := allowedLabelKeys[key]; !ok || value == "" {
			// Drop forbidden / empty labels rather than record secrets.
			continue
		}
		// Hard cap value length (IDs/codes only).
		if len(value) > 128 {
			value = value[:128]
		}
		keys = append(keys, key+"="+value)
	}
	if len(keys) == 0 {
		return metric
	}
	sort.Strings(keys)
	return metric + "|" + strings.Join(keys, "|")
}

// Global default collector for process wiring.
var defaultCollector = NewAAPCollector()

func Default() *AAPCollector { return defaultCollector }

func NewAAPCollector() *AAPCollector {
	return &AAPCollector{}
}

// --- Counter / gauge APIs ----------------------------------------------------

func (c *AAPCollector) ObserveProtocolEventAppend(latency time.Duration, labels map[string]string) {
	if c == nil {
		return
	}
	c.protocolEventAppendTotal.Add(1)
	c.mu.Lock()
	c.protocolAppendLatency.observe(latency.Milliseconds())
	c.mu.Unlock()
	c.labeled.add("protocol_event_append_total", labels, 1)
}

func (c *AAPCollector) ObserveProtocolEventAppendError(labels map[string]string) {
	if c == nil {
		return
	}
	c.protocolEventAppendErrors.Add(1)
	c.labeled.add("protocol_event_append_errors_total", labels, 1)
}

func (c *AAPCollector) ObserveSequenceConflict(labels map[string]string) {
	if c == nil {
		return
	}
	c.protocolEventSequenceConflict.Add(1)
	c.labeled.add("protocol_event_sequence_conflict_total", labels, 1)
}

func (c *AAPCollector) SetSSEActiveConnections(n int64) {
	if c == nil {
		return
	}
	c.sseActiveConnections.Store(n)
}

func (c *AAPCollector) ObserveSSEReconnect(labels map[string]string) {
	if c == nil {
		return
	}
	c.sseReconnectTotal.Add(1)
	c.labeled.add("sse_reconnect_total", labels, 1)
}

func (c *AAPCollector) ObserveSSEReplay(events int, lag time.Duration, labels map[string]string) {
	if c == nil {
		return
	}
	if events > 0 {
		c.sseReplayEventsTotal.Add(uint64(events))
	}
	c.mu.Lock()
	c.sseReplayLag.observe(lag.Milliseconds())
	c.mu.Unlock()
	c.labeled.add("sse_replay_events_total", labels, uint64(max(events, 0)))
}

func (c *AAPCollector) ObserveSSESlowConsumerDisconnect(labels map[string]string) {
	if c == nil {
		return
	}
	c.sseSlowConsumerDisconnects.Add(1)
	c.labeled.add("sse_slow_consumer_disconnect_total", labels, 1)
}

func (c *AAPCollector) SetRunWaitingInteraction(n int64) {
	if c == nil {
		return
	}
	c.runWaitingInteraction.Store(n)
}

func (c *AAPCollector) ObserveWaitingApprovalAge(age time.Duration) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.waitingApprovalAge.observe(age.Milliseconds())
	c.mu.Unlock()
}

func (c *AAPCollector) ObserveTokenIssue(success bool, labels map[string]string) {
	if c == nil {
		return
	}
	if success {
		c.tokenIssueTotal.Add(1)
		c.labeled.add("token_issue_total", labels, 1)
		return
	}
	c.tokenIssueFailureTotal.Add(1)
	c.labeled.add("token_issue_failure_total", labels, 1)
}

func (c *AAPCollector) ObserveCredentialLastUsedAge(age time.Duration) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.credentialLastUsedAge.observe(age.Milliseconds())
	c.mu.Unlock()
}

func (c *AAPCollector) ObserveAuthorizationDenied(labels map[string]string) {
	if c == nil {
		return
	}
	c.authorizationDeniedTotal.Add(1)
	c.labeled.add("authorization_denied_total", labels, 1)
}

func (c *AAPCollector) ObserveFanoutNotifyFailure(labels map[string]string) {
	if c == nil {
		return
	}
	c.fanoutNotifyFailureTotal.Add(1)
	c.labeled.add("fanout_notify_failure_total", labels, 1)
}

// ObserveEinoCheckpointCleanup records one GC pass. deleted is rows removed;
// success=false increments the error counter (deleted is ignored).
func (c *AAPCollector) ObserveEinoCheckpointCleanup(deleted int64, success bool) {
	if c == nil {
		return
	}
	c.einoCheckpointCleanupRunsTotal.Add(1)
	if !success {
		c.einoCheckpointCleanupErrorsTotal.Add(1)
		return
	}
	if deleted > 0 {
		c.einoCheckpointCleanupDeletedTotal.Add(uint64(deleted))
	}
}

// ObserveAgentEngineEnqueue records one Factory.Enqueue route by engine.
// engine must be "eino", "legacy", or "refused" (other values are dropped).
// After PR16 production is eino-only; "legacy" remains accepted for historical
// scrapes, "refused" covers misconfigured nil-eino fail-closed.
// Emitted as labeled series aap_agent_engine_enqueue_total{engine="..."}.
func (c *AAPCollector) ObserveAgentEngineEnqueue(engine string) {
	if c == nil {
		return
	}
	engine = strings.ToLower(strings.TrimSpace(engine))
	switch engine {
	case "eino", "legacy", "refused":
	default:
		return
	}
	c.labeled.add("agent_engine_enqueue_total", map[string]string{"engine": engine}, 1)
}

// Snapshot is a point-in-time export for tests, dashboards, and text exposition.
type Snapshot struct {
	ProtocolEventAppendTotal      uint64 `json:"protocol_event_append_total"`
	ProtocolEventAppendErrors     uint64 `json:"protocol_event_append_errors_total"`
	ProtocolEventSequenceConflict uint64 `json:"protocol_event_sequence_conflict_total"`
	SSEActiveConnections          int64  `json:"sse_active_connections"`
	SSEReconnectTotal             uint64 `json:"sse_reconnect_total"`
	SSEReplayEventsTotal          uint64 `json:"sse_replay_events_total"`
	SSESlowConsumerDisconnects    uint64 `json:"sse_slow_consumer_disconnect_total"`
	RunWaitingInteraction         int64  `json:"run_waiting_interaction_total"`
	TokenIssueTotal               uint64 `json:"token_issue_total"`
	TokenIssueFailureTotal        uint64 `json:"token_issue_failure_total"`
	AuthorizationDeniedTotal      uint64 `json:"authorization_denied_total"`
	FanoutNotifyFailureTotal      uint64 `json:"fanout_notify_failure_total"`
	// Eino checkpoint GC (PR3 / D15).
	EinoCheckpointCleanupRunsTotal    uint64 `json:"eino_checkpoint_cleanup_runs_total"`
	EinoCheckpointCleanupDeletedTotal uint64 `json:"eino_checkpoint_cleanup_deleted_total"`
	EinoCheckpointCleanupErrorsTotal  uint64 `json:"eino_checkpoint_cleanup_errors_total"`

	ProtocolAppendLatencyCount int   `json:"protocol_event_append_latency_samples"`
	ProtocolAppendLatencyMaxMs int64 `json:"protocol_event_append_latency_max_ms"`
	ProtocolAppendLatencyP95Ms int64 `json:"protocol_event_append_latency_p95_ms"`
	SSEReplayLagCount          int   `json:"sse_replay_lag_samples"`
	SSEReplayLagMaxMs          int64 `json:"sse_replay_lag_max_ms"`
	SSEReplayLagP95Ms          int64 `json:"sse_replay_lag_p95_ms"`
	WaitingApprovalAgeCount    int   `json:"run_waiting_interaction_age_samples"`
	WaitingApprovalAgeMaxMs    int64 `json:"run_waiting_interaction_age_max_ms"`
	WaitingApprovalAgeP95Ms    int64 `json:"run_waiting_interaction_age_p95_ms"`
	CredentialLastUsedCount    int   `json:"credential_last_used_age_samples"`
	CredentialLastUsedMaxMs    int64 `json:"credential_last_used_age_max_ms"`

	Labeled map[string]uint64 `json:"labeled,omitempty"`
}

func (c *AAPCollector) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	c.mu.Lock()
	appendCount, appendMax, appendP95 := c.protocolAppendLatency.snapshot()
	lagCount, lagMax, lagP95 := c.sseReplayLag.snapshot()
	waitCount, waitMax, waitP95 := c.waitingApprovalAge.snapshot()
	credCount, credMax, _ := c.credentialLastUsedAge.snapshot()
	c.mu.Unlock()
	return Snapshot{
		ProtocolEventAppendTotal:          c.protocolEventAppendTotal.Load(),
		ProtocolEventAppendErrors:         c.protocolEventAppendErrors.Load(),
		ProtocolEventSequenceConflict:     c.protocolEventSequenceConflict.Load(),
		SSEActiveConnections:              c.sseActiveConnections.Load(),
		SSEReconnectTotal:                 c.sseReconnectTotal.Load(),
		SSEReplayEventsTotal:              c.sseReplayEventsTotal.Load(),
		SSESlowConsumerDisconnects:        c.sseSlowConsumerDisconnects.Load(),
		RunWaitingInteraction:             c.runWaitingInteraction.Load(),
		TokenIssueTotal:                   c.tokenIssueTotal.Load(),
		TokenIssueFailureTotal:            c.tokenIssueFailureTotal.Load(),
		AuthorizationDeniedTotal:          c.authorizationDeniedTotal.Load(),
		FanoutNotifyFailureTotal:          c.fanoutNotifyFailureTotal.Load(),
		EinoCheckpointCleanupRunsTotal:    c.einoCheckpointCleanupRunsTotal.Load(),
		EinoCheckpointCleanupDeletedTotal: c.einoCheckpointCleanupDeletedTotal.Load(),
		EinoCheckpointCleanupErrorsTotal:  c.einoCheckpointCleanupErrorsTotal.Load(),
		ProtocolAppendLatencyCount:        appendCount,
		ProtocolAppendLatencyMaxMs:        appendMax,
		ProtocolAppendLatencyP95Ms:        appendP95,
		SSEReplayLagCount:                 lagCount,
		SSEReplayLagMaxMs:                 lagMax,
		SSEReplayLagP95Ms:                 lagP95,
		WaitingApprovalAgeCount:           waitCount,
		WaitingApprovalAgeMaxMs:           waitMax,
		WaitingApprovalAgeP95Ms:           waitP95,
		CredentialLastUsedCount:           credCount,
		CredentialLastUsedMaxMs:           credMax,
		Labeled:                           c.labeled.snapshot(),
	}
}

// PrometheusText renders a minimal Prometheus exposition without external deps.
// Includes labeled series from Snapshot.Labeled so dashboards can drill down by
// Client/Agent/Run (allowlisted label keys only).
func (c *AAPCollector) PrometheusText() string {
	s := c.Snapshot()
	var b strings.Builder
	write := func(name string, value uint64) {
		b.WriteString(name)
		b.WriteByte(' ')
		b.WriteString(formatUint(value))
		b.WriteByte('\n')
	}
	writeGauge := func(name string, value int64) {
		b.WriteString(name)
		b.WriteByte(' ')
		b.WriteString(formatInt(value))
		b.WriteByte('\n')
	}
	write("aap_protocol_event_append_total", s.ProtocolEventAppendTotal)
	write("aap_protocol_event_append_errors_total", s.ProtocolEventAppendErrors)
	write("aap_protocol_event_sequence_conflict_total", s.ProtocolEventSequenceConflict)
	writeGauge("aap_sse_active_connections", s.SSEActiveConnections)
	write("aap_sse_reconnect_total", s.SSEReconnectTotal)
	write("aap_sse_replay_events_total", s.SSEReplayEventsTotal)
	write("aap_sse_slow_consumer_disconnect_total", s.SSESlowConsumerDisconnects)
	writeGauge("aap_run_waiting_interaction", s.RunWaitingInteraction)
	write("aap_token_issue_total", s.TokenIssueTotal)
	write("aap_token_issue_failure_total", s.TokenIssueFailureTotal)
	write("aap_authorization_denied_total", s.AuthorizationDeniedTotal)
	write("aap_fanout_notify_failure_total", s.FanoutNotifyFailureTotal)
	write("aap_eino_checkpoint_cleanup_runs_total", s.EinoCheckpointCleanupRunsTotal)
	write("aap_eino_checkpoint_cleanup_deleted_total", s.EinoCheckpointCleanupDeletedTotal)
	write("aap_eino_checkpoint_cleanup_errors_total", s.EinoCheckpointCleanupErrorsTotal)
	write("aap_protocol_event_append_latency_p95_ms", uint64(max(s.ProtocolAppendLatencyP95Ms, 0)))
	write("aap_sse_replay_lag_p95_ms", uint64(max(s.SSEReplayLagP95Ms, 0)))
	write("aap_run_waiting_interaction_age_p95_ms", uint64(max(s.WaitingApprovalAgeP95Ms, 0)))
	write("aap_credential_last_used_age_max_ms", uint64(max(s.CredentialLastUsedMaxMs, 0)))
	// Labeled drill-down: key format metric|k=v|k=v from labeledKey().
	if len(s.Labeled) > 0 {
		keys := make([]string, 0, len(s.Labeled))
		for key := range s.Labeled {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			name, labels := parseLabeledKey(key)
			if name == "" {
				continue
			}
			b.WriteString("aap_")
			b.WriteString(name)
			if labels != "" {
				b.WriteByte('{')
				b.WriteString(labels)
				b.WriteByte('}')
			}
			b.WriteByte(' ')
			b.WriteString(formatUint(s.Labeled[key]))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// parseLabeledKey converts "metric|k=v|k=v" into prometheus name + label set.
func parseLabeledKey(key string) (name, labelText string) {
	parts := strings.Split(key, "|")
	if len(parts) == 0 {
		return "", ""
	}
	name = strings.TrimSpace(parts[0])
	if name == "" {
		return "", ""
	}
	if len(parts) == 1 {
		return name, ""
	}
	labels := make([]string, 0, len(parts)-1)
	for _, part := range parts[1:] {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.TrimSpace(kv[0])
		v := strings.TrimSpace(kv[1])
		if k == "" || v == "" {
			continue
		}
		if _, ok := allowedLabelKeys[k]; !ok {
			continue
		}
		// Escape label values for Prometheus text format.
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		v = strings.ReplaceAll(v, "\n", `\n`)
		labels = append(labels, k+`="`+v+`"`)
	}
	return name, strings.Join(labels, ",")
}

func formatUint(v uint64) string {
	return strconv.FormatUint(v, 10)
}

func formatInt(v int64) string {
	return strconv.FormatInt(v, 10)
}
