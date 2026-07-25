package protocolcompat

import (
	"testing"
)

func TestCompareBaselinesDetectsBreakingAndAdditive(t *testing.T) {
	t.Parallel()
	old := Baseline{
		SchemaSetSHA256: "old",
		Documents: map[string]Shape{
			"run.schema.json": {
				Path:     "run.schema.json",
				Types:    []string{"object"},
				Required: []string{"id", "status"},
				Properties: map[string]PropertyShape{
					"id":     {Types: []string{"string"}, Required: true},
					"status": {Types: []string{"string"}, Required: true, Enum: []string{"accepted", "running", "completed"}},
					"title":  {Types: []string{"string"}, Required: false},
				},
			},
		},
	}
	// Breaking: remove title, change id type, tighten required with new field, shrink enum.
	// Additive: new optional field note, new enum value cancelled on a different path.
	current := Baseline{
		SchemaSetSHA256: "new",
		Documents: map[string]Shape{
			"run.schema.json": {
				Path:     "run.schema.json",
				Types:    []string{"object"},
				Required: []string{"id", "status", "agentId"},
				Properties: map[string]PropertyShape{
					"id":      {Types: []string{"number"}, Required: true},
					"status":  {Types: []string{"string"}, Required: true, Enum: []string{"accepted", "running"}},
					"agentId": {Types: []string{"string"}, Required: true},
					"note":    {Types: []string{"string"}, Required: false},
				},
			},
			"extra.schema.json": {Path: "extra.schema.json", Types: []string{"object"}},
		},
	}
	report := CompareBaselines(old, current)
	if !report.HasBreaking() {
		t.Fatal("expected breaking findings")
	}
	codes := map[string]int{}
	for _, finding := range report.Breaking {
		codes[finding.Code]++
	}
	for _, want := range []string{"FIELD_REMOVED", "TYPE_CHANGED", "REQUIRED_ADDED", "ENUM_VALUE_REMOVED"} {
		if codes[want] == 0 {
			t.Fatalf("missing breaking code %s in %+v", want, report.Breaking)
		}
	}
	additive := map[string]int{}
	for _, finding := range report.Additive {
		additive[finding.Code]++
	}
	if additive["FIELD_ADDED"] == 0 || additive["SCHEMA_ADDED"] == 0 {
		t.Fatalf("expected additive field/schema: %+v", report.Additive)
	}
}

func TestCompareBaselinesAllowsAdditiveOptionalField(t *testing.T) {
	t.Parallel()
	old := Baseline{
		Documents: map[string]Shape{
			"run.schema.json": {
				Path:     "run.schema.json",
				Types:    []string{"object"},
				Required: []string{"id"},
				Properties: map[string]PropertyShape{
					"id":     {Types: []string{"string"}, Required: true},
					"status": {Types: []string{"string"}, Required: false, Enum: []string{"a", "b"}},
				},
			},
		},
	}
	current := Baseline{
		Documents: map[string]Shape{
			"run.schema.json": {
				Path:     "run.schema.json",
				Types:    []string{"object"},
				Required: []string{"id"},
				Properties: map[string]PropertyShape{
					"id":     {Types: []string{"string"}, Required: true},
					"status": {Types: []string{"string"}, Required: false, Enum: []string{"a", "b", "c"}},
					"label":  {Types: []string{"string"}, Required: false},
				},
			},
		},
	}
	report := CompareBaselines(old, current)
	if report.HasBreaking() {
		t.Fatalf("unexpected breaking: %+v", report.Breaking)
	}
	if len(report.Additive) == 0 {
		t.Fatal("expected additive findings")
	}
}

func TestExtractBaselineFromDocuments(t *testing.T) {
	t.Parallel()
	docs := map[string]map[string]any{
		"run.schema.json": {
			"$id":      "https://schemas.actweave.dev/aap/v1/run.schema.json",
			"type":     "object",
			"required": []any{"id", "status"},
			"properties": map[string]any{
				"id":     map[string]any{"type": "string"},
				"status": map[string]any{"type": "string", "enum": []any{"accepted", "running"}},
			},
			"$defs": map[string]any{
				"status": map[string]any{"type": "string", "enum": []any{"accepted", "running"}},
			},
		},
	}
	baseline := ExtractBaseline("2026-07-20", "1.0", "deadbeef", docs)
	if baseline.Documents["run.schema.json"].Properties["id"].Types[0] != "string" {
		t.Fatalf("unexpected shape: %+v", baseline.Documents["run.schema.json"])
	}
	if _, ok := baseline.Documents["run.schema.json#/$defs/status"]; !ok {
		t.Fatal("expected $defs/status shape")
	}
}
