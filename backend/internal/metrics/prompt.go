package metrics

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// PromptCollector holds bounded ZKL-69 prompt/preview metrics.
// Labels are limited to stable result/code/reason enums — never bodies or IDs.
type PromptCollector struct {
	readSuccess atomic.Uint64
	readDenied  atomic.Uint64
	readError   atomic.Uint64

	previewSuccess atomic.Uint64
	previewError   atomic.Uint64
	previewTimeout atomic.Uint64

	promotionLinked    atomic.Uint64
	promotionManual    atomic.Uint64
	promotionIntegrity atomic.Uint64

	purgeSuccess atomic.Uint64
	purgeError   atomic.Uint64
	purgeAbsent  atomic.Uint64

	purgeBacklog           atomic.Int64
	purgeOldestOverdueSecs atomic.Int64
}

var defaultPrompt = &PromptCollector{}

// DefaultPrompt returns the process-wide prompt metrics collector.
func DefaultPrompt() *PromptCollector { return defaultPrompt }

func (c *PromptCollector) IncPromptRead(result string) {
	switch boundResult(result) {
	case "success":
		c.readSuccess.Add(1)
	case "denied":
		c.readDenied.Add(1)
	default:
		c.readError.Add(1)
	}
}

func (c *PromptCollector) IncPreview(result, _ string) {
	switch boundResult(result) {
	case "success":
		c.previewSuccess.Add(1)
	case "timeout":
		c.previewTimeout.Add(1)
	default:
		c.previewError.Add(1)
	}
}

func (c *PromptCollector) ObservePreviewDuration(string, int64) {}

func (c *PromptCollector) IncPromotion(result, reason string) {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case "LINKED", "AI_ASSISTED":
		c.promotionLinked.Add(1)
	case "SOURCE_NOT_ELIGIBLE", "MANUAL":
		c.promotionManual.Add(1)
	default:
		if boundResult(result) == "error" {
			c.promotionIntegrity.Add(1)
			return
		}
		c.promotionManual.Add(1)
	}
}

func (c *PromptCollector) IncPurge(result, code string) {
	switch boundResult(result) {
	case "success":
		if strings.EqualFold(code, "ABSENT") {
			c.purgeAbsent.Add(1)
			return
		}
		c.purgeSuccess.Add(1)
	default:
		c.purgeError.Add(1)
	}
}

func (c *PromptCollector) SetPurgeBacklog(n int64) {
	if n < 0 {
		n = 0
	}
	c.purgeBacklog.Store(n)
}

func (c *PromptCollector) SetPurgeOldestOverdueSeconds(n int64) {
	if n < 0 {
		n = 0
	}
	c.purgeOldestOverdueSecs.Store(n)
}

// RenderPromptMetrics returns Prometheus text exposition for prompt metrics.
func (c *PromptCollector) RenderPromptMetrics() string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	writeCounter(&b, "agent_prompt_read_total", "Current prompt reads", map[string]uint64{
		`result="success"`: c.readSuccess.Load(),
		`result="denied"`:  c.readDenied.Load(),
		`result="error"`:   c.readError.Load(),
	})
	writeCounter(&b, "agent_prompt_preview_total", "Create-preview attempts", map[string]uint64{
		`result="success"`: c.previewSuccess.Load(),
		`result="error"`:   c.previewError.Load(),
		`result="timeout"`: c.previewTimeout.Load(),
	})
	writeCounter(&b, "agent_prompt_preview_duration_ms", "Create-preview duration placeholder", map[string]uint64{
		`result="success"`: 0,
	})
	writeCounter(&b, "agent_prompt_preview_promotion_total", "Create-preview promotions", map[string]uint64{
		`result="success",reason="LINKED"`:              c.promotionLinked.Load(),
		`result="success",reason="SOURCE_NOT_ELIGIBLE"`: c.promotionManual.Load(),
		`result="error",reason="INTEGRITY"`:             c.promotionIntegrity.Load(),
	})
	writeCounter(&b, "agent_prompt_preview_purge_total", "Preview body purge outcomes", map[string]uint64{
		`result="success",code="OK"`:     c.purgeSuccess.Load(),
		`result="success",code="ABSENT"`: c.purgeAbsent.Load(),
		`result="error",code="FAILED"`:   c.purgeError.Load(),
	})
	b.WriteString("# HELP agent_prompt_preview_purge_backlog Objects awaiting purge\n")
	b.WriteString("# TYPE agent_prompt_preview_purge_backlog gauge\n")
	fmt.Fprintf(&b, "agent_prompt_preview_purge_backlog %d\n", c.purgeBacklog.Load())
	b.WriteString("# HELP agent_prompt_preview_purge_oldest_overdue_seconds Age of oldest overdue preview body\n")
	b.WriteString("# TYPE agent_prompt_preview_purge_oldest_overdue_seconds gauge\n")
	fmt.Fprintf(&b, "agent_prompt_preview_purge_oldest_overdue_seconds %d\n", c.purgeOldestOverdueSecs.Load())
	return b.String()
}

func writeCounter(b *strings.Builder, name, help string, series map[string]uint64) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	for labels, value := range series {
		fmt.Fprintf(b, "%s{%s} %d\n", name, labels, value)
	}
}

func boundResult(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "success", "ok":
		return "success"
	case "denied", "forbidden":
		return "denied"
	case "timeout":
		return "timeout"
	default:
		return "error"
	}
}
