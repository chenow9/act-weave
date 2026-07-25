package metrics

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// SmartDagCollector holds process-local intelligent-orchestration metrics (P6.2).
// Labels are bounded result/reason codes only — never prompts or graph bodies.
type SmartDagCollector struct {
	generateSucceeded      atomic.Uint64
	generateGuardRejected  atomic.Uint64
	generateModelRequired  atomic.Uint64
	generateUpstreamFailed atomic.Uint64
	generateOtherFailed    atomic.Uint64
	guardRejectTotal       atomic.Uint64
	trialSucceeded         atomic.Uint64
	trialFailed            atomic.Uint64
	executeSucceeded       atomic.Uint64
	executeFailed          atomic.Uint64
	generateLatencyMS      atomic.Uint64 // last observed
	generateLatencyCount   atomic.Uint64
	generateLatencySumMS   atomic.Uint64
}

var defaultSmartDag = NewSmartDagCollector()

// SmartDag returns the process-default smart-dag metrics collector.
func SmartDag() *SmartDagCollector { return defaultSmartDag }

// NewSmartDagCollector constructs an empty collector (tests).
func NewSmartDagCollector() *SmartDagCollector { return &SmartDagCollector{} }

// ObserveGenerate records one generate-session turn outcome.
// result: succeeded | guard_rejected | agent_model_required | upstream_failed | failed
func (c *SmartDagCollector) ObserveGenerate(result string, latency time.Duration) {
	if c == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(result)) {
	case "succeeded", "success", "ok":
		c.generateSucceeded.Add(1)
	case "guard_rejected", "guard":
		c.generateGuardRejected.Add(1)
		c.guardRejectTotal.Add(1)
	case "agent_model_required":
		c.generateModelRequired.Add(1)
	case "upstream_failed", "upstream":
		c.generateUpstreamFailed.Add(1)
	default:
		c.generateOtherFailed.Add(1)
	}
	if latency > 0 {
		ms := uint64(latency.Milliseconds())
		c.generateLatencyMS.Store(ms)
		c.generateLatencyCount.Add(1)
		c.generateLatencySumMS.Add(ms)
	}
}

// ObserveGuardReject increments guard rejection (optionally with reason code, unused for cardinality).
func (c *SmartDagCollector) ObserveGuardReject(reason string) {
	if c == nil {
		return
	}
	c.guardRejectTotal.Add(1)
	_ = reason
}

// ObserveTrial records workflow trial outcome (result: succeeded|failed).
func (c *SmartDagCollector) ObserveTrial(result string) {
	if c == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(result), "succeeded") || strings.EqualFold(result, "ok") {
		c.trialSucceeded.Add(1)
		return
	}
	c.trialFailed.Add(1)
}

// ObserveExecute records production :execute outcome (result: succeeded|failed).
func (c *SmartDagCollector) ObserveExecute(result string) {
	if c == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(result), "succeeded") ||
		strings.EqualFold(result, "accepted") ||
		strings.EqualFold(result, "ok") {
		c.executeSucceeded.Add(1)
		return
	}
	c.executeFailed.Add(1)
}

// PrometheusText renders smart-dag metrics in Prometheus text format.
func (c *SmartDagCollector) PrometheusText() string {
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
	helpType("smartdag_generate_total", "Generate turn outcomes by result", "counter")
	fmt.Fprintf(&b, "smartdag_generate_total{result=\"succeeded\"} %d\n", c.generateSucceeded.Load())
	fmt.Fprintf(&b, "smartdag_generate_total{result=\"guard_rejected\"} %d\n", c.generateGuardRejected.Load())
	fmt.Fprintf(&b, "smartdag_generate_total{result=\"agent_model_required\"} %d\n", c.generateModelRequired.Load())
	fmt.Fprintf(&b, "smartdag_generate_total{result=\"upstream_failed\"} %d\n", c.generateUpstreamFailed.Load())
	fmt.Fprintf(&b, "smartdag_generate_total{result=\"failed\"} %d\n", c.generateOtherFailed.Load())

	helpType("smartdag_guard_reject_total", "Guard rejections on generate path", "counter")
	fmt.Fprintf(&b, "smartdag_guard_reject_total %d\n", c.guardRejectTotal.Load())

	helpType("smartdag_generate_latency_ms_last", "Last generate turn latency in milliseconds", "gauge")
	fmt.Fprintf(&b, "smartdag_generate_latency_ms_last %d\n", c.generateLatencyMS.Load())
	helpType("smartdag_generate_latency_ms_sum", "Sum of generate turn latencies in milliseconds", "counter")
	fmt.Fprintf(&b, "smartdag_generate_latency_ms_sum %d\n", c.generateLatencySumMS.Load())
	helpType("smartdag_generate_latency_count", "Count of generate latency samples", "counter")
	fmt.Fprintf(&b, "smartdag_generate_latency_count %d\n", c.generateLatencyCount.Load())

	helpType("workflow_trial_total", "Workflow trial outcomes", "counter")
	fmt.Fprintf(&b, "workflow_trial_total{result=\"succeeded\"} %d\n", c.trialSucceeded.Load())
	fmt.Fprintf(&b, "workflow_trial_total{result=\"failed\"} %d\n", c.trialFailed.Load())

	helpType("workflow_production_execute_total", "Production revision execute outcomes", "counter")
	fmt.Fprintf(&b, "workflow_production_execute_total{result=\"succeeded\"} %d\n", c.executeSucceeded.Load())
	fmt.Fprintf(&b, "workflow_production_execute_total{result=\"failed\"} %d\n", c.executeFailed.Load())
	return b.String()
}
