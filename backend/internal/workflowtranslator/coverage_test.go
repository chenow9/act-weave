package workflowtranslator_test

import (
	"testing"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflowtranslator"
)

func TestCoverageMatrixCoreNativeIncludingApproval(t *testing.T) {
	t.Parallel()

	// Parallel (PR13a), HTTP (PR13b), SubWorkflow (PR13c), ForEach (PR13d) are eino_core-native.
	core := []string{"Start", "End", "Tool", "Condition", "Transform", "Approval", "Parallel", "HTTP", "SubWorkflow", "ForEach"}
	for _, nodeType := range core {
		coverage := workflowtranslator.CoverageFor(nodeType, workflowtranslator.EngineEinoCore)
		if coverage.Status != workflowtranslator.CoverageNative {
			t.Fatalf("%s under eino_core: want native, got %#v", nodeType, coverage)
		}
		if coverage.MinimumEngine != workflowtranslator.EngineEinoCore {
			t.Fatalf("%s MinimumEngine=%q", nodeType, coverage.MinimumEngine)
		}
		if !workflowtranslator.IsCoreNodeType(nodeType) {
			t.Fatalf("%s should be IsCoreNodeType", nodeType)
		}
		if workflowtranslator.IsAdvancedNodeType(nodeType) {
			t.Fatalf("%s must not be IsAdvancedNodeType after PR13d", nodeType)
		}
	}

	// Strategy C: Approval interrupt impact must mention interrupt / resume.
	approval := workflowtranslator.CoverageFor("Approval", workflowtranslator.EngineEinoCore)
	if approval.InterruptResumeImpact == "" {
		t.Fatal("Approval must document interrupt/resume impact")
	}
	if approval.Reason == "" {
		t.Fatal("Approval must document strategy C reason")
	}

	parallel := workflowtranslator.CoverageFor("Parallel", workflowtranslator.EngineEinoCore)
	if parallel.Reason == "" {
		t.Fatal("Parallel must document native fan-out/join reason")
	}

	http := workflowtranslator.CoverageFor("HTTP", workflowtranslator.EngineEinoCore)
	if http.Reason == "" {
		t.Fatal("HTTP must document native simulation reason")
	}

	sub := workflowtranslator.CoverageFor("SubWorkflow", workflowtranslator.EngineEinoCore)
	if sub.Reason == "" {
		t.Fatal("SubWorkflow must document nested CoreGraphRunner reason")
	}
	if sub.InterruptResumeImpact == "" {
		t.Fatal("SubWorkflow must document CompositeInterrupt impact")
	}

	foreach := workflowtranslator.CoverageFor("ForEach", workflowtranslator.EngineEinoCore)
	if foreach.Reason == "" {
		t.Fatal("ForEach must document scoped iteration reason")
	}
	if foreach.InterruptResumeImpact == "" {
		t.Fatal("ForEach must document loop-scope impact")
	}
}

func TestCoverageMatrixNoKnownAdvancedTypes(t *testing.T) {
	t.Parallel()

	// PR13d promoted ForEach; no known plan types remain MinimumEngine=eino-only.
	for _, nodeType := range []string{"Start", "End", "Tool", "Condition", "Transform", "Approval", "Parallel", "HTTP", "SubWorkflow", "ForEach"} {
		if workflowtranslator.IsAdvancedNodeType(nodeType) {
			t.Fatalf("%s should not be IsAdvancedNodeType after PR13d", nodeType)
		}
		full := workflowtranslator.CoverageFor(nodeType, workflowtranslator.EngineEino)
		if full.Status != workflowtranslator.CoverageNative {
			t.Fatalf("%s under eino: want native, got %#v", nodeType, full)
		}
	}
}

func TestCoverageUnknownNodeUnsupported(t *testing.T) {
	t.Parallel()

	for _, engine := range []string{workflowtranslator.EngineEinoCore, workflowtranslator.EngineEino} {
		coverage := workflowtranslator.CoverageFor("LLM", engine)
		if coverage.Status != workflowtranslator.CoverageUnsupported {
			t.Fatalf("LLM under %s: want unsupported, got %#v", engine, coverage)
		}
		if coverage.Reason == "" {
			t.Fatal("unsupported coverage must be actionable")
		}
	}
}

func TestEvaluateCoveragePlan(t *testing.T) {
	t.Parallel()

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-mix",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "tool", Type: "Tool"},
			{NodeID: "approval", Type: "Approval"},
			{NodeID: "http", Type: "HTTP"},
			{NodeID: "sub", Type: "SubWorkflow"},
			{NodeID: "foreach", Type: "ForEach"},
			{NodeID: "end", Type: "End"},
		},
	}

	core := workflowtranslator.EvaluateCoverage(plan, workflowtranslator.EngineEinoCore)
	if core.Engine != workflowtranslator.EngineEinoCore {
		t.Fatalf("engine=%q", core.Engine)
	}
	if len(core.Unsupported) != 0 {
		t.Fatalf("eino_core unsupported=%v (ForEach is native after PR13d)", core.Unsupported)
	}
	if core.ByType["Approval"].Status != workflowtranslator.CoverageNative {
		t.Fatalf("Approval must be native under eino_core: %#v", core.ByType["Approval"])
	}
	if core.ByType["HTTP"].Status != workflowtranslator.CoverageNative {
		t.Fatalf("HTTP must be native under eino_core: %#v", core.ByType["HTTP"])
	}
	if core.ByType["SubWorkflow"].Status != workflowtranslator.CoverageNative {
		t.Fatalf("SubWorkflow must be native under eino_core: %#v", core.ByType["SubWorkflow"])
	}
	if core.ByType["ForEach"].Status != workflowtranslator.CoverageNative {
		t.Fatalf("ForEach must be native under eino_core: %#v", core.ByType["ForEach"])
	}
	if !workflowtranslator.SupportsEinoCore(plan) {
		t.Fatal("known-types plan including ForEach must SupportsEinoCore after PR13d")
	}

	full := workflowtranslator.EvaluateCoverage(plan, workflowtranslator.EngineEino)
	if len(full.Unsupported) != 0 {
		t.Fatalf("eino unsupported=%v", full.Unsupported)
	}
	if !workflowtranslator.SupportsEino(plan) {
		t.Fatal("known-types plan must SupportsEino")
	}
}

func TestEvaluateCoverageCoreOnlySupportsEinoCore(t *testing.T) {
	t.Parallel()

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-core",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "tool", Type: "Tool"},
			{NodeID: "branch", Type: "Condition"},
			{NodeID: "approval", Type: "Approval"},
			{NodeID: "parallel", Type: "Parallel"},
			{NodeID: "http", Type: "HTTP"},
			{NodeID: "sub", Type: "SubWorkflow"},
			{NodeID: "foreach", Type: "ForEach"},
			{NodeID: "xform", Type: "Transform"},
			{NodeID: "end", Type: "End"},
		},
	}
	if !workflowtranslator.SupportsEinoCore(plan) {
		t.Fatal("core+Approval+Parallel+HTTP+SubWorkflow+ForEach plan must SupportsEinoCore")
	}
	if !workflowtranslator.SupportsEino(plan) {
		t.Fatal("core plan must also SupportsEino")
	}
}

func TestCoverageDefaultEngineIsEinoCore(t *testing.T) {
	t.Parallel()

	// Empty engine normalizes to eino_core evaluation.
	// ForEach is native under eino_core after PR13d.
	foreach := workflowtranslator.CoverageFor("ForEach", "")
	if foreach.Status != workflowtranslator.CoverageNative {
		t.Fatalf("ForEach with empty engine should evaluate as eino_core native, got %#v", foreach)
	}
	sub := workflowtranslator.CoverageFor("SubWorkflow", "")
	if sub.Status != workflowtranslator.CoverageNative {
		t.Fatalf("SubWorkflow with empty engine should evaluate as eino_core native, got %#v", sub)
	}
	http := workflowtranslator.CoverageFor("HTTP", "")
	if http.Status != workflowtranslator.CoverageNative {
		t.Fatalf("HTTP with empty engine should evaluate as eino_core native, got %#v", http)
	}
	start := workflowtranslator.CoverageFor("Start", "")
	if start.Status != workflowtranslator.CoverageNative {
		t.Fatalf("Start with empty engine should be native, got %#v", start)
	}
}
