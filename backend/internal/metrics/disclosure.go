package metrics

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Bounded disclosure / verification labels. Mode and code only — never tool
// names, search queries, schemas, or workspace / agent / model ids.
const (
	DisclosureModeClientBounded   = "client_bounded"
	DisclosureModePlatformBounded = "platform_bounded"
	DisclosureModeCarryAll        = "carry_all"
	DisclosureModeNone            = "none"

	DisclosureCodeNotRolledOut     = "MODEL_TOOL_DISCLOSURE_NOT_ROLLED_OUT"
	DisclosureCodeCarryAllTooLarge = "MODEL_TOOL_CARRY_ALL_TOO_LARGE"
	DisclosureCodeSearchLoadCap    = "TOOL_SEARCH_LOAD_CAP"

	DisclosureGateBind          = "bind"
	DisclosureGateRunStart      = "run_start"
	DisclosureGateSetDisclosure = "set_disclosure"

	DisclosurePhaseAuth            = "auth"
	DisclosurePhaseResponses       = "responses"
	DisclosurePhaseToolSearch      = "tool_search"
	DisclosurePhaseFunctionCalling = "function_calling"
	DisclosurePhaseResult          = "result"

	DisclosureOutcomeOK      = "ok"
	DisclosureOutcomeError   = "error"
	DisclosureOutcomeSkipped = "skipped"
	DisclosureOutcomeLoadCap = "load_cap"

	DisclosureToolCallingNative     = "native_client_search"
	DisclosureToolCallingFunction   = "function_calling"
	DisclosureToolCallingNone       = "none"
	DisclosureToolCallingUnverified = "unverified"
)

// DisclosureCollector holds process-local tool-disclosure metrics.
// Labels are closed enums (mode / code / phase / outcome / tool_calling / gate).
type DisclosureCollector struct {
	mu   sync.Mutex
	data map[string]*atomic.Uint64

	searchLoadedLast  [2]atomic.Uint64
	searchLoadedSum   [2]atomic.Uint64
	searchLoadedCount [2]atomic.Uint64

	verifyDurationLast  [4]atomic.Uint64
	verifyDurationSum   [4]atomic.Uint64
	verifyDurationCount [4]atomic.Uint64
}

var defaultDisclosure = NewDisclosureCollector()

// Disclosure returns the process-default disclosure metrics collector.
func Disclosure() *DisclosureCollector { return defaultDisclosure }

// NewDisclosureCollector constructs an empty collector (tests).
func NewDisclosureCollector() *DisclosureCollector { return &DisclosureCollector{} }

func (c *DisclosureCollector) add(metric string, labels map[string]string, delta uint64) {
	if c == nil {
		return
	}
	key := disclosureLabeledKey(metric, labels)
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
		if len(c.data) >= 256 {
			return
		}
		counter = &atomic.Uint64{}
		c.data[key] = counter
	}
	counter.Add(delta)
}

func (c *DisclosureCollector) get(metric string, labels map[string]string) uint64 {
	if c == nil {
		return 0
	}
	key := disclosureLabeledKey(metric, labels)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data == nil {
		return 0
	}
	counter, ok := c.data[key]
	if !ok {
		return 0
	}
	return counter.Load()
}

// ObserveModeRun records one assembled run (root or child) by disclosure mode.
func (c *DisclosureCollector) ObserveModeRun(mode, toolCalling string) {
	c.add("agentic_disclosure_mode_runs_total", map[string]string{
		"mode":         normalizeDisclosureMode(mode),
		"tool_calling": normalizeDisclosureToolCalling(toolCalling),
	}, 1)
}

// ObserveRejected records a fail-closed disclosure rejection by stable code.
func (c *DisclosureCollector) ObserveRejected(code string) {
	c.add("agentic_disclosure_rejected_total", map[string]string{
		"code": normalizeDisclosureCode(code),
	}, 1)
}

// ObserveCarryAllRejected records a carry-all hard-limit rejection by gate.
func (c *DisclosureCollector) ObserveCarryAllRejected(gate string) {
	c.add("agentic_carry_all_rejected_total", map[string]string{
		"gate": normalizeDisclosureGate(gate),
	}, 1)
}

// ObserveSearchCall records one catalog search attempt. outcome is
// ok | load_cap | error. mode is client_bounded | platform_bounded.
func (c *DisclosureCollector) ObserveSearchCall(mode, outcome string) {
	c.add("agentic_tool_search_calls_total", map[string]string{
		"mode":    normalizeSearchMode(mode),
		"outcome": normalizeSearchOutcome(outcome),
	}, 1)
}

// ObserveSearchLoaded records how many definitions a successful search returned.
func (c *DisclosureCollector) ObserveSearchLoaded(mode string, loaded int) {
	if c == nil {
		return
	}
	idx, ok := searchModeIndex(mode)
	if !ok {
		return
	}
	if loaded < 0 {
		loaded = 0
	}
	c.searchLoadedLast[idx].Store(uint64(loaded))
	c.searchLoadedSum[idx].Add(uint64(loaded))
	c.searchLoadedCount[idx].Add(1)
}

// ObserveVerification records one probe phase (or the terminal result).
// Labels are phase / outcome / tool_calling only.
func (c *DisclosureCollector) ObserveVerification(phase, outcome, toolCalling string, latency time.Duration) {
	c.add("model_verification_total", map[string]string{
		"phase":        normalizeVerificationPhase(phase),
		"outcome":      normalizeVerificationOutcome(outcome),
		"tool_calling": normalizeDisclosureToolCalling(toolCalling),
	}, 1)
	if c == nil {
		return
	}
	idx, ok := verificationPhaseIndex(phase)
	if !ok || latency < 0 {
		return
	}
	ms := uint64(latency.Milliseconds())
	c.verifyDurationLast[idx].Store(ms)
	c.verifyDurationSum[idx].Add(ms)
	c.verifyDurationCount[idx].Add(1)
}

// PrometheusText renders disclosure metrics in Prometheus text format.
// Unknown / hostile labels are never emitted.
func (c *DisclosureCollector) PrometheusText() string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	helpType := func(name, help, typ string) {
		b.WriteString("# HELP ")
		b.WriteString(name)
		b.WriteString(" ")
		b.WriteString(help)
		b.WriteByte('\n')
		b.WriteString("# TYPE ")
		b.WriteString(name)
		b.WriteString(" ")
		b.WriteString(typ)
		b.WriteByte('\n')
	}

	helpType("agentic_disclosure_mode_runs_total", "Agentic runs assembled by disclosure mode", "counter")
	for _, mode := range disclosureModes {
		for _, calling := range disclosureToolCallings {
			fmt.Fprintf(&b, "agentic_disclosure_mode_runs_total{mode=%q,tool_calling=%q} %d\n",
				mode, calling, c.get("agentic_disclosure_mode_runs_total", map[string]string{
					"mode": mode, "tool_calling": calling,
				}))
		}
	}

	helpType("agentic_disclosure_rejected_total", "Disclosure fail-closed rejections by stable code", "counter")
	for _, code := range disclosureCodes {
		fmt.Fprintf(&b, "agentic_disclosure_rejected_total{code=%q} %d\n",
			code, c.get("agentic_disclosure_rejected_total", map[string]string{"code": code}))
	}

	helpType("agentic_carry_all_rejected_total", "Carry-all hard-limit rejections by gate", "counter")
	for _, gate := range disclosureGates {
		fmt.Fprintf(&b, "agentic_carry_all_rejected_total{gate=%q} %d\n",
			gate, c.get("agentic_carry_all_rejected_total", map[string]string{"gate": gate}))
	}

	helpType("agentic_tool_search_calls_total", "Bounded tool-search attempts by mode and outcome", "counter")
	for _, mode := range searchModes {
		for _, outcome := range searchOutcomes {
			fmt.Fprintf(&b, "agentic_tool_search_calls_total{mode=%q,outcome=%q} %d\n",
				mode, outcome, c.get("agentic_tool_search_calls_total", map[string]string{
					"mode": mode, "outcome": outcome,
				}))
		}
	}

	helpType("agentic_tool_search_loaded_tools", "Definitions returned by a successful bounded search", "gauge")
	for i, mode := range []string{DisclosureModeClientBounded, DisclosureModePlatformBounded} {
		last, sum, count := uint64(0), uint64(0), uint64(0)
		if c != nil {
			last = c.searchLoadedLast[i].Load()
			sum = c.searchLoadedSum[i].Load()
			count = c.searchLoadedCount[i].Load()
		}
		fmt.Fprintf(&b, "agentic_tool_search_loaded_tools_last{mode=%q} %d\n", mode, last)
		fmt.Fprintf(&b, "agentic_tool_search_loaded_tools_sum{mode=%q} %d\n", mode, sum)
		fmt.Fprintf(&b, "agentic_tool_search_loaded_tools_count{mode=%q} %d\n", mode, count)
	}

	helpType("model_verification_total", "Model verification probe outcomes by phase", "counter")
	for _, phase := range verificationPhases {
		for _, outcome := range verificationOutcomes {
			for _, calling := range disclosureToolCallings {
				fmt.Fprintf(&b, "model_verification_total{phase=%q,outcome=%q,tool_calling=%q} %d\n",
					phase, outcome, calling, c.get("model_verification_total", map[string]string{
						"phase": phase, "outcome": outcome, "tool_calling": calling,
					}))
			}
		}
	}

	helpType("model_verification_duration_ms", "Model verification probe phase latency in milliseconds", "gauge")
	for i, phase := range verificationPhaseDurations {
		last, sum, count := uint64(0), uint64(0), uint64(0)
		if c != nil {
			last = c.verifyDurationLast[i].Load()
			sum = c.verifyDurationSum[i].Load()
			count = c.verifyDurationCount[i].Load()
		}
		fmt.Fprintf(&b, "model_verification_duration_ms_last{phase=%q} %d\n", phase, last)
		fmt.Fprintf(&b, "model_verification_duration_ms_sum{phase=%q} %d\n", phase, sum)
		fmt.Fprintf(&b, "model_verification_duration_ms_count{phase=%q} %d\n", phase, count)
	}
	return b.String()
}

var (
	disclosureModes            = []string{DisclosureModeClientBounded, DisclosureModePlatformBounded, DisclosureModeCarryAll, DisclosureModeNone, "unknown"}
	disclosureToolCallings     = []string{DisclosureToolCallingNative, DisclosureToolCallingFunction, DisclosureToolCallingNone, DisclosureToolCallingUnverified, "unknown"}
	disclosureCodes            = []string{DisclosureCodeNotRolledOut, DisclosureCodeCarryAllTooLarge, DisclosureCodeSearchLoadCap, "unknown"}
	disclosureGates            = []string{DisclosureGateBind, DisclosureGateRunStart, DisclosureGateSetDisclosure, "unknown"}
	searchModes                = []string{DisclosureModeClientBounded, DisclosureModePlatformBounded, "unknown"}
	searchOutcomes             = []string{DisclosureOutcomeOK, DisclosureOutcomeLoadCap, DisclosureOutcomeError, "unknown"}
	verificationPhases         = []string{DisclosurePhaseAuth, DisclosurePhaseResponses, DisclosurePhaseToolSearch, DisclosurePhaseFunctionCalling, DisclosurePhaseResult, "unknown"}
	verificationOutcomes       = []string{DisclosureOutcomeOK, DisclosureOutcomeError, DisclosureOutcomeSkipped, "unknown"}
	verificationPhaseDurations = []string{DisclosurePhaseAuth, DisclosurePhaseResponses, DisclosurePhaseToolSearch, DisclosurePhaseFunctionCalling}
)

var allowedDisclosureLabelKeys = map[string]struct{}{
	"mode":         {},
	"code":         {},
	"phase":        {},
	"outcome":      {},
	"tool_calling": {},
	"gate":         {},
}

func disclosureLabeledKey(metric string, labels map[string]string) string {
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
		if _, ok := allowedDisclosureLabelKeys[key]; !ok {
			continue
		}
		value = normalizeDisclosureLabel(key, value)
		if value == "" {
			continue
		}
		keys = append(keys, key+"="+value)
	}
	if len(keys) == 0 {
		return metric
	}
	// Stable order: mode, code, phase, outcome, tool_calling, gate.
	order := []string{"mode", "code", "phase", "outcome", "tool_calling", "gate"}
	sorted := make([]string, 0, len(keys))
	seen := map[string]string{}
	for _, kv := range keys {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		seen[parts[0]] = parts[1]
	}
	for _, key := range order {
		if value, ok := seen[key]; ok {
			sorted = append(sorted, key+"="+value)
		}
	}
	return metric + "|" + strings.Join(sorted, "|")
}

func normalizeDisclosureLabel(key, value string) string {
	switch key {
	case "mode":
		return normalizeDisclosureMode(value)
	case "code":
		return normalizeDisclosureCode(value)
	case "phase":
		return normalizeVerificationPhase(value)
	case "outcome":
		if v := normalizeSearchOutcome(value); v != "unknown" {
			return v
		}
		return normalizeVerificationOutcome(value)
	case "tool_calling":
		return normalizeDisclosureToolCalling(value)
	case "gate":
		return normalizeDisclosureGate(value)
	default:
		return ""
	}
}

func normalizeDisclosureMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case DisclosureModeClientBounded, DisclosureModePlatformBounded, DisclosureModeCarryAll, DisclosureModeNone:
		return mode
	default:
		return "unknown"
	}
}

func normalizeSearchMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case DisclosureModeClientBounded, DisclosureModePlatformBounded:
		return mode
	default:
		return "unknown"
	}
}

func normalizeDisclosureCode(code string) string {
	switch strings.TrimSpace(code) {
	case DisclosureCodeNotRolledOut, DisclosureCodeCarryAllTooLarge, DisclosureCodeSearchLoadCap:
		return code
	default:
		return "unknown"
	}
}

func normalizeDisclosureGate(gate string) string {
	switch strings.TrimSpace(gate) {
	case DisclosureGateBind, DisclosureGateRunStart, DisclosureGateSetDisclosure:
		return gate
	default:
		return "unknown"
	}
}

func normalizeDisclosureToolCalling(value string) string {
	switch strings.TrimSpace(value) {
	case DisclosureToolCallingNative, DisclosureToolCallingFunction, DisclosureToolCallingNone, DisclosureToolCallingUnverified:
		return value
	case "":
		return DisclosureToolCallingUnverified
	default:
		return "unknown"
	}
}

func normalizeVerificationPhase(phase string) string {
	switch strings.TrimSpace(phase) {
	case DisclosurePhaseAuth, DisclosurePhaseResponses, DisclosurePhaseToolSearch, DisclosurePhaseFunctionCalling, DisclosurePhaseResult:
		return phase
	default:
		return "unknown"
	}
}

func normalizeVerificationOutcome(outcome string) string {
	switch strings.TrimSpace(outcome) {
	case DisclosureOutcomeOK, DisclosureOutcomeError, DisclosureOutcomeSkipped:
		return outcome
	default:
		return "unknown"
	}
}

func normalizeSearchOutcome(outcome string) string {
	switch strings.TrimSpace(outcome) {
	case DisclosureOutcomeOK, DisclosureOutcomeLoadCap, DisclosureOutcomeError:
		return outcome
	default:
		return "unknown"
	}
}

func searchModeIndex(mode string) (int, bool) {
	switch normalizeSearchMode(mode) {
	case DisclosureModeClientBounded:
		return 0, true
	case DisclosureModePlatformBounded:
		return 1, true
	default:
		return 0, false
	}
}

func verificationPhaseIndex(phase string) (int, bool) {
	switch normalizeVerificationPhase(phase) {
	case DisclosurePhaseAuth:
		return 0, true
	case DisclosurePhaseResponses:
		return 1, true
	case DisclosurePhaseToolSearch:
		return 2, true
	case DisclosurePhaseFunctionCalling:
		return 3, true
	default:
		return 0, false
	}
}
