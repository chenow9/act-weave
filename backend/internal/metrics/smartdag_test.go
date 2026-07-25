package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestSmartDagMetricsPrometheusAndObserve(t *testing.T) {
	t.Parallel()
	c := NewSmartDagCollector()
	c.ObserveGenerate("succeeded", 12*time.Millisecond)
	c.ObserveGenerate("guard_rejected", 5*time.Millisecond)
	c.ObserveGenerate("agent_model_required", 0)
	c.ObserveTrial("succeeded")
	c.ObserveTrial("failed")
	c.ObserveExecute("succeeded")
	c.ObserveExecute("failed")

	text := c.PrometheusText()
	for _, needle := range []string{
		`smartdag_generate_total{result="succeeded"} 1`,
		`smartdag_generate_total{result="guard_rejected"} 1`,
		`smartdag_generate_total{result="agent_model_required"} 1`,
		`smartdag_guard_reject_total 1`,
		`workflow_trial_total{result="succeeded"} 1`,
		`workflow_trial_total{result="failed"} 1`,
		`workflow_production_execute_total{result="succeeded"} 1`,
		`workflow_production_execute_total{result="failed"} 1`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("missing %q in:\n%s", needle, text)
		}
	}
}
