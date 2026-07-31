package smartdag

import (
	"math"
	"testing"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflowcompiler"
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

func TestNormalizeCandidateGraphBreaksProgressPollCycle(t *testing.T) {
	t.Parallel()
	// Classic smart-dag poll shape that Eino DAG / compiler reject:
	// get_progress → condition ⇄ get_progress (running) + completed → report + failed → end
	graph := domain.WorkflowGraphDraft{
		SchemaVersion: SchemaVersion,
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start", Position: domain.WorkflowPosition{X: 0, Y: 0}},
			{ID: "create_task", Type: "Tool", Position: domain.WorkflowPosition{X: 100, Y: 0}, Data: map[string]any{"toolId": testToolID}},
			{ID: "get_progress", Type: "Tool", Position: domain.WorkflowPosition{X: 200, Y: 0}, Data: map[string]any{"toolId": testToolID}},
			{ID: "progress_check", Type: "Condition", Position: domain.WorkflowPosition{X: 300, Y: 0}, Data: map[string]any{"expression": "status == \"completed\""}},
			{ID: "create_report", Type: "Tool", Position: domain.WorkflowPosition{X: 400, Y: 0}, Data: map[string]any{"toolId": testToolID}},
			{ID: "end", Type: "End", Position: domain.WorkflowPosition{X: 500, Y: 0}},
		},
		Edges: []domain.WorkflowGraphEdge{
			{ID: "e1", SourceNodeID: "start", TargetNodeID: "create_task"},
			{ID: "e2", SourceNodeID: "create_task", TargetNodeID: "get_progress"},
			{ID: "e3", SourceNodeID: "get_progress", TargetNodeID: "progress_check"},
			{ID: "e4", SourceNodeID: "progress_check", TargetNodeID: "create_report", Data: map[string]any{"branch": "completed"}},
			{ID: "e5", SourceNodeID: "progress_check", TargetNodeID: "get_progress", Data: map[string]any{"branch": "running"}},
			{ID: "e6", SourceNodeID: "progress_check", TargetNodeID: "end", Data: map[string]any{"branch": "failed"}},
			{ID: "e7", SourceNodeID: "create_report", TargetNodeID: "end"},
		},
	}

	out := NormalizeCandidateGraph(graph)

	// Running back-edge must be removed.
	for _, e := range out.Edges {
		if e.SourceNodeID == "progress_check" && e.TargetNodeID == "get_progress" {
			t.Fatalf("poll back-edge still present: %+v", e)
		}
	}
	// Forward branches kept.
	hasCompleted, hasFailed := false, false
	for _, e := range out.Edges {
		if e.SourceNodeID == "progress_check" && e.TargetNodeID == "create_report" {
			hasCompleted = true
		}
		if e.SourceNodeID == "progress_check" && e.TargetNodeID == "end" {
			hasFailed = true
		}
	}
	if !hasCompleted || !hasFailed {
		t.Fatalf("expected completed+failed branches kept, edges=%+v", out.Edges)
	}

	// Must pass Guard (DAG + Condition dual-branch) and compile without graph-cycle.
	report := GuardGraph(out, GuardOptions{CatalogToolIDs: CatalogToolIDSet([]string{testToolID})})
	if !report.OK {
		t.Fatalf("normalized poll graph should pass guard: %+v", report.Violations)
	}
	if leftover := detectCycleNodeIDs(out); len(leftover) > 0 {
		t.Fatalf("still cyclic after normalize: %v", leftover)
	}
	// Publish path: workflowcompiler must not reject for cycles (graph stage).
	compilation := workflowcompiler.New().Compile("wf-poll", "1", out)
	for _, issue := range compilation.Issues {
		if issue.Code == "graph-cycle-detected" {
			t.Fatalf("cycle still rejected by compiler: %+v", compilation.Issues)
		}
	}
	if compilation.Status != domain.WorkflowCompilationValid {
		t.Fatalf("expected Valid compilation after cycle break, status=%s issues=%+v", compilation.Status, compilation.Issues)
	}
}

func TestNormalizeCandidateGraphRepairsConditionAfterOnlyPollEdgeDropped(t *testing.T) {
	t.Parallel()
	// Condition only had completed + running; after drop needs default→End.
	graph := domain.WorkflowGraphDraft{
		SchemaVersion: SchemaVersion,
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start", Position: domain.WorkflowPosition{X: 0, Y: 0}},
			{ID: "progress", Type: "Tool", Position: domain.WorkflowPosition{X: 100, Y: 0}, Data: map[string]any{"toolId": testToolID}},
			{ID: "check", Type: "Condition", Position: domain.WorkflowPosition{X: 200, Y: 0}},
			{ID: "done", Type: "Tool", Position: domain.WorkflowPosition{X: 300, Y: 0}, Data: map[string]any{"toolId": testToolID}},
			{ID: "end", Type: "End", Position: domain.WorkflowPosition{X: 400, Y: 0}},
		},
		Edges: []domain.WorkflowGraphEdge{
			{SourceNodeID: "start", TargetNodeID: "progress"},
			{SourceNodeID: "progress", TargetNodeID: "check"},
			{SourceNodeID: "check", TargetNodeID: "done", Data: map[string]any{"branch": "completed"}},
			{SourceNodeID: "check", TargetNodeID: "progress", Data: map[string]any{"branch": "running"}},
			{SourceNodeID: "done", TargetNodeID: "end"},
		},
	}
	out := NormalizeCandidateGraph(graph)
	outs := 0
	for _, e := range out.Edges {
		if e.SourceNodeID == "check" {
			outs++
		}
		if e.SourceNodeID == "check" && e.TargetNodeID == "progress" {
			t.Fatal("running back-edge should be removed")
		}
	}
	if outs < 2 {
		t.Fatalf("Condition should have ≥2 outs after repair, got %d edges=%+v", outs, out.Edges)
	}
	report := GuardGraph(out, GuardOptions{CatalogToolIDs: CatalogToolIDSet([]string{testToolID})})
	if !report.OK {
		t.Fatalf("expected guard pass after repair: %+v", report.Violations)
	}
}

func TestGuardGraphRejectsCycleWhenPresent(t *testing.T) {
	t.Parallel()
	// Bypass Normalize: feed a cyclic graph directly to Guard.
	graph := domain.WorkflowGraphDraft{
		SchemaVersion: SchemaVersion,
		Nodes: []domain.WorkflowGraphNode{
			{ID: "start", Type: "Start", Ports: defaultPortsForType("Start"), Data: map[string]any{}, UI: map[string]any{}},
			{ID: "a", Type: "Tool", Ports: defaultPortsForType("Tool"), Data: map[string]any{"toolId": testToolID}, UI: map[string]any{}},
			{ID: "b", Type: "Tool", Ports: defaultPortsForType("Tool"), Data: map[string]any{"toolId": testToolID}, UI: map[string]any{}},
			{ID: "end", Type: "End", Ports: defaultPortsForType("End"), Data: map[string]any{}, UI: map[string]any{}},
		},
		Edges: []domain.WorkflowGraphEdge{
			{ID: "e1", SourceNodeID: "start", SourcePort: "output", TargetNodeID: "a", TargetPort: "input"},
			{ID: "e2", SourceNodeID: "a", SourcePort: "output", TargetNodeID: "b", TargetPort: "input"},
			{ID: "e3", SourceNodeID: "b", SourcePort: "output", TargetNodeID: "a", TargetPort: "input"}, // cycle
			{ID: "e4", SourceNodeID: "b", SourcePort: "output", TargetNodeID: "end", TargetPort: "input"},
		},
	}
	report := GuardGraph(graph, GuardOptions{CatalogToolIDs: CatalogToolIDSet([]string{testToolID})})
	if report.OK || !hasViolationCode(report, "GRAPH_CYCLE") {
		t.Fatalf("expected GRAPH_CYCLE, got %+v", report)
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
