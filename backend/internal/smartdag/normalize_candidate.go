package smartdag

import (
	"fmt"
	"math"
	"sort"
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
// empty/alternate edge field names, missing ports/ids, collapsed layout (all at 0,0),
// and poll/retry back-edges that form cycles (runtime is Eino DAG / acyclic only).
// It does not invent business structure beyond breaking cycles into compileable DAGs:
// if edges cannot be recovered from the payload, empty/invalid edges are dropped and
// Guard must reject connectivity failures.
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
		// Self-loops always form a cycle; drop early.
		if normalized.SourceNodeID == normalized.TargetNodeID {
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

	// Runtime (Eino AllPredecessor) and workflowcompiler require a DAG. Smart orchestration
	// often emits progress-poll loops (Condition "running" → get_progress). Break those
	// back-edges and repair Condition dual-branch / dangling outs so publish can succeed.
	graph.Edges = breakCycleEdges(graph.Nodes, graph.Edges)
	graph.Edges = ensureAcyclicBranchConnectivity(graph.Nodes, graph.Edges)

	if needsAutoLayout(graph.Nodes) {
		graph.Nodes = autoLayoutNodes(graph.Nodes, graph.Edges)
	}
	return graph
}

// breakCycleEdges removes edges that participate in directed cycles until the graph is a DAG.
// Preference order for which edge to drop when a cycle is found:
//  1. Condition/Approval outs whose branch looks like a poll/retry loop (running/pending/…)
//  2. Any Condition/Approval out that closes a cycle
//  3. Any remaining edge inside the Kahn residual (cycle component)
//
// Uses Kahn residual nodes (not DFS tree back-edges): DFS back-edge choice depends on
// visit order and can drop the forward progress→condition edge while keeping the poll loop.
func breakCycleEdges(nodes []domain.WorkflowGraphNode, edges []domain.WorkflowGraphEdge) []domain.WorkflowGraphEdge {
	if len(edges) == 0 {
		return edges
	}
	nodeType := map[string]string{}
	for _, n := range nodes {
		nodeType[n.ID] = strings.TrimSpace(n.Type)
	}

	// Bounded iterations: at most one edge removed per pass.
	for pass := 0; pass < len(edges)+2; pass++ {
		cycleIdx := findKahnResidualEdgeIndexes(edges)
		if len(cycleIdx) == 0 {
			return edges
		}
		// Score each residual edge; drop the highest-priority (lowest score) one.
		best := cycleIdx[0]
		bestScore := cycleEdgeDropScore(edges[best], nodeType[edges[best].SourceNodeID])
		for _, idx := range cycleIdx[1:] {
			score := cycleEdgeDropScore(edges[idx], nodeType[edges[idx].SourceNodeID])
			if score < bestScore || (score == bestScore && idx < best) {
				best, bestScore = idx, score
			}
		}
		edges = append(edges[:best], edges[best+1:]...)
	}
	return edges
}

// findKahnResidualEdgeIndexes returns indexes of edges whose both endpoints remain after
// Kahn topological peel (i.e. edges that sit on or hang off directed cycles).
func findKahnResidualEdgeIndexes(edges []domain.WorkflowGraphEdge) []int {
	inDegree := map[string]int{}
	outgoing := map[string][]int{} // node → edge indexes
	for i, e := range edges {
		src, tgt := e.SourceNodeID, e.TargetNodeID
		if src == "" || tgt == "" {
			continue
		}
		if _, ok := inDegree[src]; !ok {
			inDegree[src] = 0
		}
		if _, ok := inDegree[tgt]; !ok {
			inDegree[tgt] = 0
		}
		inDegree[tgt]++
		outgoing[src] = append(outgoing[src], i)
	}
	if len(inDegree) == 0 {
		return nil
	}
	queue := make([]string, 0, len(inDegree))
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	remaining := len(inDegree)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		remaining--
		for _, ei := range outgoing[id] {
			tgt := edges[ei].TargetNodeID
			inDegree[tgt]--
			if inDegree[tgt] == 0 {
				queue = append(queue, tgt)
				sort.Strings(queue)
			}
		}
	}
	if remaining == 0 {
		return nil
	}
	// Nodes still with indegree > 0 are on cycles (or only reachable from cycles).
	// Prefer edges with BOTH ends residual — these are the cycle-closing edges.
	residual := map[string]bool{}
	for id, deg := range inDegree {
		if deg > 0 {
			residual[id] = true
		}
	}
	var both []int
	var one []int
	for i, e := range edges {
		srcRes := residual[e.SourceNodeID]
		tgtRes := residual[e.TargetNodeID]
		if srcRes && tgtRes {
			both = append(both, i)
		} else if srcRes || tgtRes {
			one = append(one, i)
		}
	}
	if len(both) > 0 {
		return both
	}
	return one
}

func cycleEdgeDropScore(edge domain.WorkflowGraphEdge, sourceType string) int {
	branch := ""
	if edge.Data != nil {
		if b, ok := edge.Data["branch"].(string); ok {
			branch = strings.ToLower(strings.TrimSpace(b))
		}
	}
	port := strings.ToLower(strings.TrimSpace(edge.SourcePort))
	// Poll/retry branch labels commonly produced by LLMs for async progress loops.
	if isPollLoopBranch(branch) || isPollLoopBranch(port) {
		return 0
	}
	switch sourceType {
	case "Condition", "Approval":
		return 1
	default:
		return 2
	}
}

func isPollLoopBranch(label string) bool {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "running", "pending", "in_progress", "in-progress", "processing",
		"retry", "loop", "continue", "wait", "polling", "not_ready", "not-ready",
		"incomplete", "busy":
		return true
	default:
		return false
	}
}

// ensureAcyclicBranchConnectivity repairs connectivity after back-edge removal:
// Condition needs ≥2 outs and at most one non-default branch (compiler rule);
// non-End nodes need an out-edge. Routes orphans to End.
func ensureAcyclicBranchConnectivity(nodes []domain.WorkflowGraphNode, edges []domain.WorkflowGraphEdge) []domain.WorkflowGraphEdge {
	if len(nodes) == 0 {
		return edges
	}
	endID := ""
	nodeType := map[string]string{}
	for _, n := range nodes {
		id := strings.TrimSpace(n.ID)
		if id == "" {
			continue
		}
		nodeType[id] = strings.TrimSpace(n.Type)
		if endID == "" && strings.TrimSpace(n.Type) == "End" {
			endID = id
		}
	}
	if endID == "" {
		return edges
	}

	outDegree := map[string]int{}
	for _, e := range edges {
		outDegree[e.SourceNodeID]++
	}

	// Condition with a single remaining out-edge after poll-loop drop: add default → End.
	for _, n := range nodes {
		id := strings.TrimSpace(n.ID)
		if nodeType[id] != "Condition" {
			continue
		}
		if outDegree[id] >= 2 {
			continue
		}
		// Avoid duplicate Condition→End.
		if hasEdge(edges, id, endID) {
			// Still need a second out if only one edge total; if only End exists once and deg<2, nothing else to attach.
			if outDegree[id] == 0 {
				// fall through to add
			} else {
				// deg==1 and already points at End: still invalid for dual-branch; leave for Guard.
				continue
			}
		}
		edges = append(edges, domain.WorkflowGraphEdge{
			ID:           fmt.Sprintf("e_%s_%s_default", id, endID),
			SourceNodeID: id,
			SourcePort:   "false",
			TargetNodeID: endID,
			TargetPort:   "input",
			Data:         map[string]any{"branch": "default"},
			UI:           map[string]any{},
		})
		outDegree[id]++
	}

	// Compiler: multi-out Condition must have exactly one non-default branch + a default.
	// LLM often emits completed + failed (both non-default) without default.
	edges = normalizeConditionDefaultBranches(nodes, edges)

	// Non-End nodes that lost their only out-edge (rare): route to End.
	outDegree = map[string]int{}
	for _, e := range edges {
		outDegree[e.SourceNodeID]++
	}
	for _, n := range nodes {
		id := strings.TrimSpace(n.ID)
		if id == "" || nodeType[id] == "End" {
			continue
		}
		if outDegree[id] > 0 {
			continue
		}
		port := "output"
		if nodeType[id] == "Condition" {
			port = "false"
		}
		if nodeType[id] == "Approval" {
			port = "rejected"
		}
		data := map[string]any{}
		if nodeType[id] == "Condition" {
			data["branch"] = "default"
		}
		edges = append(edges, domain.WorkflowGraphEdge{
			ID:           fmt.Sprintf("e_%s_%s_repair", id, endID),
			SourceNodeID: id,
			SourcePort:   port,
			TargetNodeID: endID,
			TargetPort:   "input",
			Data:         data,
			UI:           map[string]any{},
		})
		outDegree[id]++
	}
	return edges
}

// normalizeConditionDefaultBranches ensures each multi-out Condition has a default branch
// and at most one non-default branch (matches workflowcompiler.validateConditionBranches).
func normalizeConditionDefaultBranches(nodes []domain.WorkflowGraphNode, edges []domain.WorkflowGraphEdge) []domain.WorkflowGraphEdge {
	conditionIDs := map[string]struct{}{}
	for _, n := range nodes {
		if strings.TrimSpace(n.Type) == "Condition" {
			if id := strings.TrimSpace(n.ID); id != "" {
				conditionIDs[id] = struct{}{}
			}
		}
	}
	if len(conditionIDs) == 0 {
		return edges
	}

	// Collect outgoing edge indexes per Condition.
	outs := map[string][]int{}
	for i, e := range edges {
		if _, ok := conditionIDs[e.SourceNodeID]; ok {
			outs[e.SourceNodeID] = append(outs[e.SourceNodeID], i)
		}
	}

	for _, indexes := range outs {
		if len(indexes) <= 1 {
			continue
		}
		hasDefault := false
		var nonDefault []int
		for _, ei := range indexes {
			branch := edgeBranch(edges[ei])
			if branch == "default" {
				hasDefault = true
				continue
			}
			if branch != "" {
				nonDefault = append(nonDefault, ei)
			}
		}
		// Prefer demoting failed/false/reject edges to default; else last non-default.
		demote := -1
		if !hasDefault {
			for _, ei := range nonDefault {
				if isFailureBranch(edgeBranch(edges[ei])) {
					demote = ei
					break
				}
			}
			if demote < 0 && len(nonDefault) > 0 {
				demote = nonDefault[len(nonDefault)-1]
			}
			if demote < 0 && len(indexes) > 0 {
				// No branch labels at all: last out becomes default.
				demote = indexes[len(indexes)-1]
			}
			if demote >= 0 {
				if edges[demote].Data == nil {
					edges[demote].Data = map[string]any{}
				}
				edges[demote].Data["branch"] = "default"
				hasDefault = true
			}
		}
		// After ensuring default, demote extra non-default branches to default-only one non-default.
		// Re-scan: keep first success-like non-default; convert the rest to empty/default isn't right —
		// compiler allows only ONE non-default. Convert extras to "default" is invalid (multiple defaults ok?).
		// Looking at compiler: multiple defaults not checked; only nonDefaultCount > 1 fails.
		// So extra non-defaults must become default (duplicate defaults allowed by compiler).
		nonDefault = nonDefault[:0]
		for _, ei := range indexes {
			branch := edgeBranch(edges[ei])
			if branch != "" && branch != "default" {
				nonDefault = append(nonDefault, ei)
			}
		}
		if len(nonDefault) <= 1 {
			continue
		}
		// Keep the best "success" branch as the sole non-default; demote others.
		keep := nonDefault[0]
		for _, ei := range nonDefault {
			if isSuccessBranch(edgeBranch(edges[ei])) {
				keep = ei
				break
			}
		}
		for _, ei := range nonDefault {
			if ei == keep {
				continue
			}
			if edges[ei].Data == nil {
				edges[ei].Data = map[string]any{}
			}
			edges[ei].Data["branch"] = "default"
		}
	}
	return edges
}

func edgeBranch(edge domain.WorkflowGraphEdge) string {
	if edge.Data == nil {
		return ""
	}
	b, _ := edge.Data["branch"].(string)
	return strings.ToLower(strings.TrimSpace(b))
}

func isFailureBranch(branch string) bool {
	switch branch {
	case "failed", "fail", "false", "error", "reject", "rejected", "no", "timeout":
		return true
	default:
		return false
	}
}

func isSuccessBranch(branch string) bool {
	switch branch {
	case "completed", "success", "true", "yes", "pass", "ok", "approved":
		return true
	default:
		return false
	}
}

func hasEdge(edges []domain.WorkflowGraphEdge, sourceID, targetID string) bool {
	for _, e := range edges {
		if e.SourceNodeID == sourceID && e.TargetNodeID == targetID {
			return true
		}
	}
	return false
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
