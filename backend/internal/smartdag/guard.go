package smartdag

import (
	"fmt"
	"strings"

	"actweave/backend/internal/domain"
)

// DefaultMaxNodes is the MVP scale limit for generated graphs (D8 scale guard).
const DefaultMaxNodes = 64

// Allowed MVP node types for smart-dag.v2 generation (D8). SubWorkflow is
// explicitly rejected (D9).
var d8NodeTypes = map[string]struct{}{
	"Start":     {},
	"Tool":      {},
	"Transform": {},
	"Condition": {},
	"Approval":  {},
	"End":       {},
}

// GuardOptions configures deterministic graph validation before Draft persist.
type GuardOptions struct {
	// CatalogToolIDs is the set of tool capability IDs allowed on Tool nodes.
	// Nil or empty means no tools are allowed (any toolId fails).
	CatalogToolIDs map[string]struct{}
	// MaxNodes caps node count; zero uses DefaultMaxNodes.
	MaxNodes int
}

// GuardViolation is one deterministic failure reason.
type GuardViolation struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	NodeID    string `json:"nodeId,omitempty"`
	FieldPath string `json:"fieldPath,omitempty"`
}

// GuardReport is the structured result of GuardGraph (D3).
type GuardReport struct {
	OK         bool             `json:"ok"`
	Violations []GuardViolation `json:"violations"`
}

// GuardGraph validates a candidate workflow.graph.v1 against catalog + D8 rules.
// Pure function: no I/O, no Draft side effects.
func GuardGraph(graph domain.WorkflowGraphDraft, opts GuardOptions) GuardReport {
	maxNodes := opts.MaxNodes
	if maxNodes <= 0 {
		maxNodes = DefaultMaxNodes
	}
	catalog := opts.CatalogToolIDs
	if catalog == nil {
		catalog = map[string]struct{}{}
	}

	var violations []GuardViolation

	schema := strings.TrimSpace(graph.SchemaVersion)
	if schema != SchemaVersion {
		violations = append(violations, GuardViolation{
			Code:      "INVALID_SCHEMA_VERSION",
			Message:   fmt.Sprintf("schemaVersion must be %q, got %q", SchemaVersion, graph.SchemaVersion),
			FieldPath: "schemaVersion",
		})
	}

	if len(graph.Nodes) == 0 {
		violations = append(violations, GuardViolation{
			Code:      "EMPTY_GRAPH",
			Message:   "graph must contain at least Start and End nodes",
			FieldPath: "nodes",
		})
	}
	if len(graph.Nodes) > maxNodes {
		violations = append(violations, GuardViolation{
			Code:      "MAX_NODES_EXCEEDED",
			Message:   fmt.Sprintf("graph has %d nodes; max allowed is %d", len(graph.Nodes), maxNodes),
			FieldPath: "nodes",
		})
	}

	hasStart, hasEnd := false, false
	for _, node := range graph.Nodes {
		nodeType := strings.TrimSpace(node.Type)
		if nodeType == "SubWorkflow" {
			violations = append(violations, GuardViolation{
				Code:      "SUBWORKFLOW_FORBIDDEN",
				Message:   "SubWorkflow nodes are not allowed in smart orchestration MVP (D9)",
				NodeID:    node.ID,
				FieldPath: "type",
			})
			continue
		}
		if _, ok := d8NodeTypes[nodeType]; !ok {
			violations = append(violations, GuardViolation{
				Code:      "INVALID_NODE_TYPE",
				Message:   fmt.Sprintf("node type %q is not in D8 allow-list", node.Type),
				NodeID:    node.ID,
				FieldPath: "type",
			})
		}
		switch nodeType {
		case "Start":
			hasStart = true
		case "End":
			hasEnd = true
		case "Tool":
			toolID := toolIDFromNode(node)
			if toolID == "" {
				violations = append(violations, GuardViolation{
					Code:      "MISSING_TOOL_ID",
					Message:   "Tool node requires data.toolId",
					NodeID:    node.ID,
					FieldPath: "data.toolId",
				})
				continue
			}
			if _, ok := catalog[toolID]; !ok {
				violations = append(violations, GuardViolation{
					Code:      "HALLUCINATED_TOOL_ID",
					Message:   fmt.Sprintf("toolId %q is not in the published workspace catalog", toolID),
					NodeID:    node.ID,
					FieldPath: "data.toolId",
				})
			}
		}
	}

	if len(graph.Nodes) > 0 && !hasStart {
		violations = append(violations, GuardViolation{
			Code:      "MISSING_START",
			Message:   "graph requires a Start node",
			FieldPath: "nodes",
		})
	}
	if len(graph.Nodes) > 0 && !hasEnd {
		violations = append(violations, GuardViolation{
			Code:      "MISSING_END",
			Message:   "graph requires an End node",
			FieldPath: "nodes",
		})
	}

	// Connectivity: reject empty / dangling / ghost edges and disconnected nodes (canvas §3.5).
	nodeIDs := make(map[string]struct{}, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if id := strings.TrimSpace(node.ID); id != "" {
			nodeIDs[id] = struct{}{}
		}
	}
	outDegree := make(map[string]int, len(graph.Nodes))
	inDegree := make(map[string]int, len(graph.Nodes))
	validEdges := 0
	for i, edge := range graph.Edges {
		src := strings.TrimSpace(edge.SourceNodeID)
		tgt := strings.TrimSpace(edge.TargetNodeID)
		if src == "" || tgt == "" {
			violations = append(violations, GuardViolation{
				Code:      "EMPTY_EDGE",
				Message:   fmt.Sprintf("edge[%d] has empty sourceNodeId or targetNodeId", i),
				FieldPath: fmt.Sprintf("edges[%d]", i),
			})
			continue
		}
		if _, ok := nodeIDs[src]; !ok {
			violations = append(violations, GuardViolation{
				Code:      "GHOST_EDGE",
				Message:   fmt.Sprintf("edge source %q does not exist", src),
				FieldPath: fmt.Sprintf("edges[%d].sourceNodeId", i),
			})
			continue
		}
		if _, ok := nodeIDs[tgt]; !ok {
			violations = append(violations, GuardViolation{
				Code:      "GHOST_EDGE",
				Message:   fmt.Sprintf("edge target %q does not exist", tgt),
				FieldPath: fmt.Sprintf("edges[%d].targetNodeId", i),
			})
			continue
		}
		if src == tgt {
			violations = append(violations, GuardViolation{
				Code:      "SELF_LOOP",
				Message:   "self-loop edges are not allowed",
				NodeID:    src,
				FieldPath: fmt.Sprintf("edges[%d]", i),
			})
			continue
		}
		validEdges++
		outDegree[src]++
		inDegree[tgt]++
	}
	if len(graph.Nodes) >= 2 && validEdges == 0 {
		violations = append(violations, GuardViolation{
			Code:      "NO_EDGES",
			Message:   "graph with multiple nodes requires at least one valid edge",
			FieldPath: "edges",
		})
	}
	for _, node := range graph.Nodes {
		id := strings.TrimSpace(node.ID)
		if id == "" {
			continue
		}
		switch strings.TrimSpace(node.Type) {
		case "End":
			// End may have zero out-edges.
		default:
			if outDegree[id] == 0 && len(graph.Nodes) > 1 {
				violations = append(violations, GuardViolation{
					Code:      "NO_OUT_EDGE",
					Message:   fmt.Sprintf("non-End node %q has no outgoing edge", id),
					NodeID:    id,
					FieldPath: "edges",
				})
			}
		}
		switch strings.TrimSpace(node.Type) {
		case "Start":
			// Start may have zero in-edges.
		default:
			if inDegree[id] == 0 && len(graph.Nodes) > 1 {
				violations = append(violations, GuardViolation{
					Code:      "NO_IN_EDGE",
					Message:   fmt.Sprintf("non-Start node %q has no incoming edge", id),
					NodeID:    id,
					FieldPath: "edges",
				})
			}
		}
		if strings.TrimSpace(node.Type) == "Condition" && outDegree[id] < 2 {
			violations = append(violations, GuardViolation{
				Code:      "CONDITION_BRANCHES",
				Message:   fmt.Sprintf("Condition node %q requires at least two outgoing edges", id),
				NodeID:    id,
				FieldPath: "edges",
			})
		}
	}

	return GuardReport{
		OK:         len(violations) == 0,
		Violations: violations,
	}
}

// CatalogToolIDSet builds a set from published tool capability IDs.
func CatalogToolIDSet(ids []string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		set[id] = struct{}{}
	}
	return set
}

func toolIDFromNode(node domain.WorkflowGraphNode) string {
	if node.Data == nil {
		return ""
	}
	raw, ok := node.Data["toolId"]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
