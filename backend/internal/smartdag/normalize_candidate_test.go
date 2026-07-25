package smartdag

import (
	"math"
	"testing"

	"actweave/backend/internal/domain"
)

func TestNormalizeCandidateGraphLaysOutZeroPositions(t *testing.T) {
	t.Parallel()
	graph := domain.WorkflowGraphDraft{
		SchemaVersion: SchemaVersion,
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start", Position: domain.WorkflowPosition{X: 0, Y: 0}},
			{ID: "tool", Type: "Tool", Position: domain.WorkflowPosition{X: 0, Y: 0}, Data: map[string]any{"toolId": testToolID}},
			{ID: "end", Type: "End", Position: domain.WorkflowPosition{X: 0, Y: 0}},
		},
		Edges: []domain.WorkflowGraphEdge{
			{SourceNodeID: "start", TargetNodeID: "tool"},
			{SourceNodeID: "tool", TargetNodeID: "end"},
		},
	}
	out := NormalizeCandidateGraph(graph)
	if out.Nodes[0].Position.X == 0 && out.Nodes[1].Position.X == 0 {
		t.Fatalf("expected auto-layout to spread nodes, got %+v", out.Nodes)
	}
	for i := 0; i < len(out.Nodes); i++ {
		for j := i + 1; j < len(out.Nodes); j++ {
			dx := out.Nodes[i].Position.X - out.Nodes[j].Position.X
			dy := out.Nodes[i].Position.Y - out.Nodes[j].Position.Y
			if math.Hypot(dx, dy) < 80 {
				t.Fatalf("nodes %s and %s still too close", out.Nodes[i].ID, out.Nodes[j].ID)
			}
		}
	}
	if out.Edges[0].ID == "" || out.Edges[0].SourcePort == "" {
		t.Fatalf("expected edge id/ports filled: %+v", out.Edges[0])
	}
}

func TestNormalizeEdgesFromRawMapsAliases(t *testing.T) {
	t.Parallel()
	edges := NormalizeEdgesFromRawMaps([]map[string]any{
		{"from": "start", "to": "tool", "branch": "true"},
		{"source": "tool", "target": "end"},
	})
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}
	if edges[0].SourceNodeID != "start" || edges[0].TargetNodeID != "tool" {
		t.Fatalf("alias from/to not mapped: %+v", edges[0])
	}
	if edges[0].Data["branch"] != "true" {
		t.Fatalf("expected branch true, got %+v", edges[0].Data)
	}
	if edges[1].SourceNodeID != "tool" || edges[1].TargetNodeID != "end" {
		t.Fatalf("alias source/target not mapped: %+v", edges[1])
	}
}

func TestGuardGraphEmptyEdgesFails(t *testing.T) {
	t.Parallel()
	graph := validD8Graph(testToolID)
	graph.Edges = []domain.WorkflowGraphEdge{
		{ID: "", SourceNodeID: "", TargetNodeID: ""},
		{ID: "", SourceNodeID: "", TargetNodeID: ""},
	}
	report := GuardGraph(graph, GuardOptions{CatalogToolIDs: CatalogToolIDSet([]string{testToolID})})
	if report.OK {
		t.Fatal("expected empty edges to fail guard")
	}
	if !hasViolationCode(report, "EMPTY_EDGE") && !hasViolationCode(report, "NO_EDGES") {
		t.Fatalf("expected EMPTY_EDGE or NO_EDGES, got %+v", report.Violations)
	}
}

func TestGuardGraphNoOutEdgeFails(t *testing.T) {
	t.Parallel()
	graph := validD8Graph(testToolID)
	// Drop the start→tool edge only.
	graph.Edges = []domain.WorkflowGraphEdge{
		{ID: "e2", SourceNodeID: "tool-1", SourcePort: "output", TargetNodeID: "end", TargetPort: "input"},
	}
	report := GuardGraph(graph, GuardOptions{CatalogToolIDs: CatalogToolIDSet([]string{testToolID})})
	if report.OK || !hasViolationCode(report, "NO_OUT_EDGE") {
		t.Fatalf("expected NO_OUT_EDGE, got %+v", report.Violations)
	}
}

func TestParseGraphFromModelContentRecoversEdgeAliases(t *testing.T) {
	t.Parallel()
	raw := `{
	  "schemaVersion": "workflow.graph.v1",
	  "nodes": [
	    {"id":"start","type":"Start","position":{"x":0,"y":0}},
	    {"id":"tool","type":"Tool","position":{"x":0,"y":0},"data":{"toolId":"` + testToolID + `"}},
	    {"id":"end","type":"End","position":{"x":0,"y":0}}
	  ],
	  "edges": [
	    {"from":"start","to":"tool"},
	    {"source":"tool","target":"end"}
	  ]
	}`
	graph, err := ParseGraphFromModelContent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Edges) != 2 {
		t.Fatalf("expected recovered edges, got %+v", graph.Edges)
	}
	if graph.Edges[0].SourceNodeID != "start" || graph.Edges[1].TargetNodeID != "end" {
		t.Fatalf("unexpected edges: %+v", graph.Edges)
	}
	// layout applied
	if graph.Nodes[0].Position.X == 0 && graph.Nodes[1].Position.X == 0 {
		t.Fatalf("expected layout after parse, positions still zero")
	}
}
