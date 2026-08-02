package agentdelegation

import (
	"fmt"
	"strings"
)

// DetectCycle returns ErrCycle when following directed edges from roots
// produces a back-edge (A→B→A etc.). Self-loops are ErrSelfLoop.
func DetectCycle(edges []GraphEdgeSnapshot) error {
	adj := map[string][]string{}
	for _, e := range edges {
		caller := strings.TrimSpace(e.CallerAgentID)
		target := strings.TrimSpace(e.TargetAgentID)
		if caller == "" || target == "" {
			continue
		}
		if caller == target {
			return fmt.Errorf("%w: %s", ErrSelfLoop, caller)
		}
		adj[caller] = append(adj[caller], target)
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(string) error
	visit = func(n string) error {
		color[n] = gray
		for _, next := range adj[n] {
			switch color[next] {
			case gray:
				return fmt.Errorf("%w: involving %s → %s", ErrCycle, n, next)
			case white:
				if err := visit(next); err != nil {
					return err
				}
			}
		}
		color[n] = black
		return nil
	}
	for node := range adj {
		if color[node] == white {
			if err := visit(node); err != nil {
				return err
			}
		}
	}
	// Also visit nodes that only appear as targets.
	for _, e := range edges {
		t := strings.TrimSpace(e.TargetAgentID)
		if t != "" && color[t] == white {
			if err := visit(t); err != nil {
				return err
			}
		}
	}
	return nil
}

// MaxReachableDepth returns the longest path length (edges) from rootAgentID.
func MaxReachableDepth(rootAgentID string, edges []GraphEdgeSnapshot) int {
	adj := map[string][]string{}
	for _, e := range edges {
		adj[e.CallerAgentID] = append(adj[e.CallerAgentID], e.TargetAgentID)
	}
	var dfs func(string, map[string]bool) int
	dfs = func(n string, stack map[string]bool) int {
		if stack[n] {
			return 0 // cycle guarded elsewhere
		}
		stack[n] = true
		best := 0
		for _, next := range adj[n] {
			d := 1 + dfs(next, stack)
			if d > best {
				best = d
			}
		}
		delete(stack, n)
		return best
	}
	return dfs(rootAgentID, map[string]bool{})
}

// ValidateGraphForRoot checks cycle, max depth, and that all targets are present.
func ValidateGraphForRoot(rootAgentID string, edges []GraphEdgeSnapshot, maxDepth int, activeAgents map[string]bool) error {
	rootAgentID = strings.TrimSpace(rootAgentID)
	if rootAgentID == "" {
		return ErrInvalid
	}
	if err := DetectCycle(edges); err != nil {
		return err
	}
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	if d := MaxReachableDepth(rootAgentID, edges); d > maxDepth {
		return fmt.Errorf("%w: reachable depth %d > %d", ErrDepthExceeded, d, maxDepth)
	}
	for _, e := range edges {
		if activeAgents != nil {
			if !activeAgents[e.CallerAgentID] {
				return fmt.Errorf("%w: caller %s", ErrAgentUnavailable, e.CallerAgentID)
			}
			if e.Protocol == ProtocolInternal || e.Protocol == "" {
				if !activeAgents[e.TargetAgentID] {
					return fmt.Errorf("%w: target %s", ErrAgentUnavailable, e.TargetAgentID)
				}
			}
		}
	}
	return nil
}

// EdgesFromRoot returns edges reachable from root within maxDepth (edge count).
func EdgesFromRoot(rootAgentID string, all []GraphEdgeSnapshot, maxDepth int) []GraphEdgeSnapshot {
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	adj := map[string][]GraphEdgeSnapshot{}
	for _, e := range all {
		adj[e.CallerAgentID] = append(adj[e.CallerAgentID], e)
	}
	seenEdge := map[string]bool{}
	var out []GraphEdgeSnapshot
	type frame struct {
		agent string
		depth int
	}
	queue := []frame{{agent: rootAgentID, depth: 0}}
	visitedAgentDepth := map[string]int{rootAgentID: 0}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			continue
		}
		for _, e := range adj[cur.agent] {
			key := e.BindingID
			if key == "" {
				key = e.CallerAgentID + "→" + e.CallableName
			}
			if seenEdge[key] {
				continue
			}
			seenEdge[key] = true
			out = append(out, e)
			nextDepth := cur.depth + 1
			if prev, ok := visitedAgentDepth[e.TargetAgentID]; !ok || nextDepth < prev {
				visitedAgentDepth[e.TargetAgentID] = nextDepth
				queue = append(queue, frame{agent: e.TargetAgentID, depth: nextDepth})
			}
		}
	}
	return out
}
