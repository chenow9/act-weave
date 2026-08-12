package metrics_test

import (
	"strings"
	"testing"

	"actweave/backend/internal/metrics"
)

func TestObserveA2UIEmitClassifies(t *testing.T) {
	collector := metrics.NewAAPCollector()
	for _, result := range []string{
		metrics.A2UIResultOK,
		metrics.A2UIResultCatalogInvalid,
		metrics.A2UIResultProjectionRejected,
		"totally-made-up",
	} {
		collector.ObserveA2UIEmit(result)
	}
	text := collector.PrometheusText()
	for _, series := range []string{
		`a2ui_emit_total`,
		`result="ok"`,
		`result="catalog_invalid"`,
		`a2ui_extract_ok_total`,
		`a2ui_degraded_text_total`,
		`a2ui_preflight_fail_total`,
	} {
		if !strings.Contains(text, series) {
			t.Fatalf("exposition missing %q:\n%s", series, text)
		}
	}
	// An unknown result must collapse rather than create a new series.
	if !strings.Contains(text, `result="unknown"`) {
		t.Fatalf("unknown result not normalized:\n%s", text)
	}
	if strings.Contains(text, "totally-made-up") {
		t.Fatalf("unbounded label leaked into exposition:\n%s", text)
	}
}

// TestObserveA2UICatalogInvalidBoundsLabels keeps the metric's cardinality tied
// to the validator's vocabulary; a diagnostic must never widen it.
func TestObserveA2UICatalogInvalidBoundsLabels(t *testing.T) {
	collector := metrics.NewAAPCollector()
	collector.ObserveA2UICatalogInvalid(metrics.A2UIReasonSchema, "additionalProperties")
	collector.ObserveA2UICatalogInvalid(metrics.A2UIReasonGraph, "reachable")
	collector.ObserveA2UICatalogInvalid(metrics.A2UIReasonChartSemantics, "seriesCount")
	collector.ObserveA2UICatalogInvalid("made-up-reason", "made-up-keyword")

	text := collector.PrometheusText()
	for _, series := range []string{
		`a2ui_catalog_invalid_total`,
		`reason="schema"`,
		`keyword="additionalProperties"`,
		`reason="graph"`,
		`keyword="reachable"`,
		`reason="chart_semantics"`,
		`keyword="seriesCount"`,
		`reason="unknown"`,
		`keyword="unknown"`,
	} {
		if !strings.Contains(text, series) {
			t.Fatalf("exposition missing %q:\n%s", series, text)
		}
	}
	if strings.Contains(text, "made-up") {
		t.Fatalf("unbounded label leaked into exposition:\n%s", text)
	}
}

func TestObserveA2UIChartEmitted(t *testing.T) {
	collector := metrics.NewAAPCollector()
	for _, chartType := range []string{"bar", "hbar", "line", "area", "pie", "donut", "radar"} {
		collector.ObserveA2UIChartEmitted(chartType)
	}
	text := collector.PrometheusText()
	for _, chartType := range []string{"bar", "hbar", "line", "area", "pie", "donut"} {
		if !strings.Contains(text, `chart_type="`+chartType+`"`) {
			t.Fatalf("exposition missing chart_type %q:\n%s", chartType, text)
		}
	}
	if strings.Contains(text, `chart_type="radar"`) {
		t.Fatalf("chart type outside the catalog must normalize:\n%s", text)
	}
	if !strings.Contains(text, `chart_type="unknown"`) {
		t.Fatalf("unknown chart type not normalized:\n%s", text)
	}
}

func TestA2UIObserversTolerateNilCollector(t *testing.T) {
	var collector *metrics.AAPCollector
	collector.ObserveA2UIEmit(metrics.A2UIResultOK)
	collector.ObserveA2UICatalogInvalid(metrics.A2UIReasonSchema, "type")
	collector.ObserveA2UIChartEmitted("bar")
}
