package smartdag

import (
	"fmt"
	"math"
	"strings"

	"actweave/backend/internal/domain"
)

// layout defaults for candidate graphs (readable canvas; centers ≥ 80px).
const (
	layoutStartX    = 120.0
	layoutStartY    = 160.0
	layoutColumnGap = 280.0
	layoutRowGap    = 180.0
)

// NormalizeCandidateGraph repairs common LLM graph defects before GuardGraph:
// empty/alternate edge field names, missing ports/ids, and collapsed layout (all at 0,0).
// It does not invent business structure: if edges cannot be recovered from the payload,
// empty/invalid edges are dropped and Guard must reject connectivity failures.
func NormalizeCandidateGraph(graph domain.WorkflowGraphDraft) domain.WorkflowGraphDraft {
	if strings.TrimSpace(graph.SchemaVersion) == "" {
		graph.SchemaVersion = SchemaVersion
	}
	if graph.UI == nil {
		graph.UI = map[string]any{}
	}
	if graph.Viewport.Zoom == 0 && graph.Viewport.X == 0 && graph.Viewport.Y == 0 {
		graph.Viewport = domain.CanvasViewport{X: 0, Y: 0, Zoom: 1}
	}

	nodeIDs := make(map[string]struct{}, len(graph.Nodes))
	for i := range graph.Nodes {
		node := &graph.Nodes[i]
		node.ID = strings.TrimSpace(node.ID)
		node.Type = strings.TrimSpace(node.Type)
		if node.Label == "" {
			node.Label = node.Type
			if node.Label == "" {
				node.Label = node.ID
			}
		}
		if node.Data == nil {
			node.Data = map[string]any{}
		}
		if node.UI == nil {
			node.UI = map[string]any{}
		}
		if len(node.Ports) == 0 {
			node.Ports = defaultPortsForType(node.Type)
		}
		if node.ID != "" {
			nodeIDs[node.ID] = struct{}{}
		}
	}

	edges := make([]domain.WorkflowGraphEdge, 0, len(graph.Edges))
	seen := map[string]struct{}{}
	for i, edge := range graph.Edges {
		normalized := normalizeEdge(edge, i)
		if normalized.SourceNodeID == "" || normalized.TargetNodeID == "" {
			continue
		}
		if _, ok := nodeIDs[normalized.SourceNodeID]; !ok {
			continue
		}
		if _, ok := nodeIDs[normalized.TargetNodeID]; !ok {
			continue
		}
		key := normalized.SourceNodeID + "|" + normalized.SourcePort + "->" + normalized.TargetNodeID + "|" + normalized.TargetPort
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if normalized.ID == "" {
			normalized.ID = fmt.Sprintf("e_%s_%s_%d", normalized.SourceNodeID, normalized.TargetNodeID, i)
		}
		if normalized.Data == nil {
			normalized.Data = map[string]any{}
		}
		if normalized.UI == nil {
			normalized.UI = map[string]any{}
		}
		// Condition dual-branch: fill branch labels when missing and ports hint true/false.
		if src := nodeByID(graph.Nodes, normalized.SourceNodeID); src != nil && src.Type == "Condition" {
			if _, has := normalized.Data["branch"]; !has {
				port := strings.ToLower(normalized.SourcePort)
				switch port {
				case "true", "yes", "pass", "ok", "success":
					normalized.Data["branch"] = "true"
				case "false", "no", "fail", "reject", "default":
					if port == "default" {
						normalized.Data["branch"] = "default"
					} else {
						normalized.Data["branch"] = "default"
					}
				}
			}
		}
		edges = append(edges, normalized)
	}
	graph.Edges = edges

	if needsAutoLayout(graph.Nodes) {
		graph.Nodes = autoLayoutNodes(graph.Nodes, graph.Edges)
	}
	return graph
}

func normalizeEdge(edge domain.WorkflowGraphEdge, index int) domain.WorkflowGraphEdge {
	edge.ID = strings.TrimSpace(edge.ID)
	edge.SourceNodeID = strings.TrimSpace(edge.SourceNodeID)
	edge.TargetNodeID = strings.TrimSpace(edge.TargetNodeID)
	edge.SourcePort = strings.TrimSpace(edge.SourcePort)
	edge.TargetPort = strings.TrimSpace(edge.TargetPort)
	if edge.SourcePort == "" {
		edge.SourcePort = "output"
	}
	if edge.TargetPort == "" {
		edge.TargetPort = "input"
	}
	// Recover from data{} aliases some models emit instead of top-level fields.
	if edge.Data != nil {
		if edge.SourceNodeID == "" {
			edge.SourceNodeID = firstString(edge.Data, "sourceNodeId", "source", "from", "sourceId")
		}
		if edge.TargetNodeID == "" {
			edge.TargetNodeID = firstString(edge.Data, "targetNodeId", "target", "to", "targetId")
		}
		if edge.SourcePort == "output" {
			if p := firstString(edge.Data, "sourcePort", "fromPort", "outPort"); p != "" {
				edge.SourcePort = p
			}
		}
		if edge.TargetPort == "input" {
			if p := firstString(edge.Data, "targetPort", "toPort", "inPort"); p != "" {
				edge.TargetPort = p
			}
		}
	}
	_ = index
	return edge
}

// NormalizeEdgesFromRawMaps maps loosely-typed edge objects (LLM variants) into domain edges.
func NormalizeEdgesFromRawMaps(raw []map[string]any) []domain.WorkflowGraphEdge {
	edges := make([]domain.WorkflowGraphEdge, 0, len(raw))
	for i, item := range raw {
		edge := domain.WorkflowGraphEdge{
			ID:           firstString(item, "id", "edgeId"),
			SourceNodeID: firstString(item, "sourceNodeId", "source", "from", "sourceId", "source_node_id"),
			TargetNodeID: firstString(item, "targetNodeId", "target", "to", "targetId", "target_node_id"),
			SourcePort:   firstString(item, "sourcePort", "fromPort", "outPort", "source_port", "output"),
			TargetPort:   firstString(item, "targetPort", "toPort", "inPort", "target_port", "input"),
			Data:         map[string]any{},
			UI:           map[string]any{},
		}
		if data, ok := item["data"].(map[string]any); ok {
			edge.Data = data
		}
		if ui, ok := item["ui"].(map[string]any); ok {
			edge.UI = ui
		}
		if branch := firstString(item, "branch", "label", "when"); branch != "" {
			if edge.Data == nil {
				edge.Data = map[string]any{}
			}
			if _, has := edge.Data["branch"]; !has {
				edge.Data["branch"] = branch
			}
		}
		edges = append(edges, normalizeEdge(edge, i))
	}
	return edges
}

func defaultPortsForType(nodeType string) []domain.WorkflowGraphPort {
	switch nodeType {
	case "Start":
		return []domain.WorkflowGraphPort{{Key: "output", Label: "Output", Direction: "output"}}
	case "End":
		return []domain.WorkflowGraphPort{{Key: "input", Label: "Input", Direction: "input"}}
	case "Condition":
		return []domain.WorkflowGraphPort{
			{Key: "input", Label: "Input", Direction: "input"},
			{Key: "true", Label: "True", Direction: "output"},
			{Key: "false", Label: "False", Direction: "output"},
		}
	case "Approval":
		return []domain.WorkflowGraphPort{
			{Key: "input", Label: "Input", Direction: "input"},
			{Key: "approved", Label: "Approved", Direction: "output"},
			{Key: "rejected", Label: "Rejected", Direction: "output"},
		}
	default:
		return []domain.WorkflowGraphPort{
			{Key: "input", Label: "Input", Direction: "input"},
			{Key: "output", Label: "Output", Direction: "output"},
		}
	}
}

func needsAutoLayout(nodes []domain.WorkflowGraphNode) bool {
	if len(nodes) == 0 {
		return false
	}
	// All at origin, or any pair closer than 80px (canvas edge-too-short rule).
	allZero := true
	for _, n := range nodes {
		if n.Position.X != 0 || n.Position.Y != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return true
	}
	for i := 0; i < len(nodes); i++ {
		for j := i + 1; j < len(nodes); j++ {
			dx := nodes[i].Position.X - nodes[j].Position.X
			dy := nodes[i].Position.Y - nodes[j].Position.Y
			if math.Hypot(dx, dy) < 80 {
				return true
			}
		}
	}
	return false
}

func autoLayoutNodes(nodes []domain.WorkflowGraphNode, edges []domain.WorkflowGraphEdge) []domain.WorkflowGraphNode {
	depth := buildDepthMap(nodes, edges)
	// Count siblings per depth for row packing.
	orderAtDepth := map[int][]string{}
	for _, n := range nodes {
		d := depth[n.ID]
		orderAtDepth[d] = append(orderAtDepth[d], n.ID)
	}
	// Stable order by original node index for siblings.
	indexOf := map[string]int{}
	for i, n := range nodes {
		indexOf[n.ID] = i
	}
	for d, ids := range orderAtDepth {
		// sort by original order
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				if indexOf[ids[j]] < indexOf[ids[i]] {
					ids[i], ids[j] = ids[j], ids[i]
				}
			}
		}
		orderAtDepth[d] = ids
	}
	siblingIndex := map[string]int{}
	for _, ids := range orderAtDepth {
		for i, id := range ids {
			siblingIndex[id] = i
		}
	}
	out := make([]domain.WorkflowGraphNode, len(nodes))
	copy(out, nodes)
	for i := range out {
		d := depth[out[i].ID]
		row := siblingIndex[out[i].ID]
		out[i].Position = domain.WorkflowPosition{
			X: layoutStartX + float64(d)*layoutColumnGap,
			Y: layoutStartY + float64(row)*layoutRowGap,
		}
	}
	return out
}

func buildDepthMap(nodes []domain.WorkflowGraphNode, edges []domain.WorkflowGraphEdge) map[string]int {
	incoming := map[string]int{}
	outgoing := map[string][]string{}
	for _, n := range nodes {
		incoming[n.ID] = 0
		outgoing[n.ID] = nil
	}
	for _, e := range edges {
		if _, ok := incoming[e.TargetNodeID]; ok {
			incoming[e.TargetNodeID]++
		}
		if _, ok := outgoing[e.SourceNodeID]; ok {
			outgoing[e.SourceNodeID] = append(outgoing[e.SourceNodeID], e.TargetNodeID)
		}
	}
	queue := make([]string, 0, len(nodes))
	depth := map[string]int{}
	for _, n := range nodes {
		if incoming[n.ID] == 0 {
			queue = append(queue, n.ID)
			depth[n.ID] = 0
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		d := depth[id]
		for _, next := range outgoing[id] {
			if cur, ok := depth[next]; !ok || cur < d+1 {
				depth[next] = d + 1
			}
			incoming[next]--
			if incoming[next] <= 0 {
				if _, seen := depth[next]; !seen {
					depth[next] = d + 1
				}
				queue = append(queue, next)
			}
		}
	}
	for i, n := range nodes {
		if _, ok := depth[n.ID]; !ok {
			depth[n.ID] = i
		}
	}
	return depth
}

func nodeByID(nodes []domain.WorkflowGraphNode, id string) *domain.WorkflowGraphNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, ok := m[key]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		default:
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}
