package smartdag

import (
	"testing"

	"actweave/backend/internal/domain"
)

func TestGuardGraphValidPasses(t *testing.T) {
	t.Parallel()
	catalog := CatalogToolIDSet([]string{testToolID})
	report := GuardGraph(validD8Graph(testToolID), GuardOptions{
		CatalogToolIDs: catalog,
		MaxNodes:       16,
	})
	if !report.OK || len(report.Violations) != 0 {
		t.Fatalf("expected valid graph to pass, got %+v", report)
	}
}

func TestGuardGraphHallucinatedToolIDFails(t *testing.T) {
	t.Parallel()
	catalog := CatalogToolIDSet([]string{testToolID})
	graph := validD8Graph("118f1f2e-7b5a-7c3d-8e9f-999999999999")
	report := GuardGraph(graph, GuardOptions{CatalogToolIDs: catalog})
	if report.OK {
		t.Fatal("expected hallucinated toolId to fail")
	}
	if !hasViolationCode(report, "HALLUCINATED_TOOL_ID") {
		t.Fatalf("expected HALLUCINATED_TOOL_ID, got %+v", report.Violations)
	}
}

func TestGuardGraphInvalidNodeTypeFails(t *testing.T) {
	t.Parallel()
	graph := validD8Graph(testToolID)
	graph.Nodes = append(graph.Nodes, domain.WorkflowGraphNode{
		ID: "http-1", Type: "HTTP", Label: "Call API",
		Data: map[string]any{}, UI: map[string]any{},
	})
	report := GuardGraph(graph, GuardOptions{CatalogToolIDs: CatalogToolIDSet([]string{testToolID})})
	if report.OK || !hasViolationCode(report, "INVALID_NODE_TYPE") {
		t.Fatalf("expected INVALID_NODE_TYPE, got %+v", report)
	}
}

func TestGuardGraphSubWorkflowRejected(t *testing.T) {
	t.Parallel()
	graph := validD8Graph(testToolID)
	graph.Nodes = append(graph.Nodes, domain.WorkflowGraphNode{
		ID: "sub-1", Type: "SubWorkflow", Label: "Nested",
		Data: map[string]any{}, UI: map[string]any{},
	})
	report := GuardGraph(graph, GuardOptions{CatalogToolIDs: CatalogToolIDSet([]string{testToolID})})
	if report.OK || !hasViolationCode(report, "SUBWORKFLOW_FORBIDDEN") {
		t.Fatalf("expected SUBWORKFLOW_FORBIDDEN, got %+v", report)
	}
}

func TestGuardGraphMaxNodesFails(t *testing.T) {
	t.Parallel()
	graph := validD8Graph(testToolID)
	// valid graph already has 3 nodes; max 2 must fail
	report := GuardGraph(graph, GuardOptions{
		CatalogToolIDs: CatalogToolIDSet([]string{testToolID}),
		MaxNodes:       2,
	})
	if report.OK || !hasViolationCode(report, "MAX_NODES_EXCEEDED") {
		t.Fatalf("expected MAX_NODES_EXCEEDED, got %+v", report)
	}
}

func TestGuardGraphMissingStartFails(t *testing.T) {
	t.Parallel()
	graph := validD8Graph(testToolID)
	nodes := make([]domain.WorkflowGraphNode, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		if n.Type != "Start" {
			nodes = append(nodes, n)
		}
	}
	graph.Nodes = nodes
	report := GuardGraph(graph, GuardOptions{CatalogToolIDs: CatalogToolIDSet([]string{testToolID})})
	if report.OK || !hasViolationCode(report, "MISSING_START") {
		t.Fatalf("expected MISSING_START, got %+v", report)
	}
}

func TestGuardGraphMissingEndFails(t *testing.T) {
	t.Parallel()
	graph := validD8Graph(testToolID)
	nodes := make([]domain.WorkflowGraphNode, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		if n.Type != "End" {
			nodes = append(nodes, n)
		}
	}
	graph.Nodes = nodes
	report := GuardGraph(graph, GuardOptions{CatalogToolIDs: CatalogToolIDSet([]string{testToolID})})
	if report.OK || !hasViolationCode(report, "MISSING_END") {
		t.Fatalf("expected MISSING_END, got %+v", report)
	}
}

func TestGuardGraphInvalidSchemaVersionFails(t *testing.T) {
	t.Parallel()
	graph := validD8Graph(testToolID)
	graph.SchemaVersion = "workflow.graph.v0"
	report := GuardGraph(graph, GuardOptions{CatalogToolIDs: CatalogToolIDSet([]string{testToolID})})
	if report.OK || !hasViolationCode(report, "INVALID_SCHEMA_VERSION") {
		t.Fatalf("expected INVALID_SCHEMA_VERSION, got %+v", report)
	}
}

func TestGuardGraphMissingToolIDFails(t *testing.T) {
	t.Parallel()
	graph := validD8Graph(testToolID)
	for i := range graph.Nodes {
		if graph.Nodes[i].Type == "Tool" {
			graph.Nodes[i].Data = map[string]any{}
		}
	}
	report := GuardGraph(graph, GuardOptions{CatalogToolIDs: CatalogToolIDSet([]string{testToolID})})
	if report.OK || !hasViolationCode(report, "MISSING_TOOL_ID") {
		t.Fatalf("expected MISSING_TOOL_ID, got %+v", report)
	}
}

func validD8Graph(toolID string) domain.WorkflowGraphDraft {
	return domain.WorkflowGraphDraft{
		SchemaVersion: SchemaVersion,
		Nodes: []domain.WorkflowGraphNode{
			{
				ID: "start", Type: "Start", Label: "Start",
				Ports: []domain.WorkflowGraphPort{{Key: "output", Direction: "output"}},
				Data:  map[string]any{}, UI: map[string]any{},
			},
			{
				ID: "tool-1", Type: "Tool", Label: "Tool",
				Ports: []domain.WorkflowGraphPort{
					{Key: "input", Direction: "input"},
					{Key: "output", Direction: "output"},
				},
				Data: map[string]any{"toolId": toolID},
				UI:   map[string]any{},
			},
			{
				ID: "end", Type: "End", Label: "End",
				Ports: []domain.WorkflowGraphPort{{Key: "input", Direction: "input"}},
				Data:  map[string]any{}, UI: map[string]any{},
			},
		},
		Edges: []domain.WorkflowGraphEdge{
			{ID: "e1", SourceNodeID: "start", SourcePort: "output", TargetNodeID: "tool-1", TargetPort: "input"},
			{ID: "e2", SourceNodeID: "tool-1", SourcePort: "output", TargetNodeID: "end", TargetPort: "input"},
		},
		Viewport: domain.CanvasViewport{X: 0, Y: 0, Zoom: 1},
		UI:       map[string]any{},
	}
}

func hasViolationCode(report GuardReport, code string) bool {
	for _, v := range report.Violations {
		if v.Code == code {
			return true
		}
	}
	return false
}
