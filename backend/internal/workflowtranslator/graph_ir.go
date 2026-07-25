package workflowtranslator

import (
	"encoding/json"
	"fmt"
	"strings"

	"actweave/backend/internal/domain"
)

// GraphNode is one intermediate IR node projected from ExecutionPlanNode.
// Compose node key (PR11) is ID (= plan NodeID).
type GraphNode struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Dependencies   []string       `json:"dependencies,omitempty"`
	IncomingBranch string         `json:"incomingBranch,omitempty"`
	Config         map[string]any `json:"config,omitempty"`
}

// GraphEdge is a control dependency edge. Branch is non-empty when the edge
// is taken from a Condition via IncomingBranch (GraphBranch selection in PR11).
type GraphEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Branch string `json:"branch,omitempty"`
}

// BranchTarget maps a Condition out-edge to a dependent plan node.
type BranchTarget struct {
	Branch     string `json:"branch"`
	TargetNode string `json:"targetNode"`
}

// GraphIR is the intermediate representation for later eino compose build.
// It does not invoke compose.Compile; PR11 graph_builder consumes this.
type GraphIR struct {
	// WorkflowID is copied from the compiled plan.
	WorkflowID string `json:"workflowId"`
	// Engine is the coverage evaluation engine used when building IR.
	Engine string `json:"engine"`
	// Nodes preserve plan order; key for compose = Node.ID.
	Nodes []GraphNode `json:"nodes"`
	// Edges are derived from Dependencies (+ IncomingBranch on the target).
	Edges []GraphEdge `json:"edges,omitempty"`
	// Branches groups Condition node ID → out-edge targets for GraphBranch.
	Branches map[string][]BranchTarget `json:"branches,omitempty"`
	// Coverage is the plan-level coverage under Engine.
	Coverage PlanCoverage `json:"coverage"`
}

// BuildGraphIR projects plan into Graph IR under engine (default eino_core).
//
// Translation is pure: configs are deep-cloned; plan is never mutated.
// Returns an error when plan contains nodes that are unsupported under engine
// (so callers can fall back to wrapper without partial IR).
// Use BuildGraphIRLoose to always materialize IR even with unsupported nodes.
func BuildGraphIR(plan domain.CompiledExecutionPlan, engine string) (GraphIR, error) {
	ir := buildGraphIR(plan, engine)
	if len(ir.Coverage.Unsupported) > 0 {
		return GraphIR{}, fmt.Errorf(
			"workflowtranslator: plan %q has unsupported node types under %s: %s",
			plan.WorkflowID,
			ir.Engine,
			strings.Join(ir.Coverage.Unsupported, ", "),
		)
	}
	return ir, nil
}

// BuildGraphIRLoose always returns IR + coverage, including unsupported types.
// Useful for diagnostics (trial nodeCoverage) without failing the translate.
func BuildGraphIRLoose(plan domain.CompiledExecutionPlan, engine string) GraphIR {
	return buildGraphIR(plan, engine)
}

func buildGraphIR(plan domain.CompiledExecutionPlan, engine string) GraphIR {
	engine = normalizeEngine(engine)
	coverage := EvaluateCoverage(plan, engine)

	nodes := make([]GraphNode, 0, len(plan.Nodes))
	nodeTypes := make(map[string]string, len(plan.Nodes))
	for _, node := range plan.Nodes {
		nodes = append(nodes, GraphNode{
			ID:             node.NodeID,
			Type:           node.Type,
			Dependencies:   cloneStrings(node.Dependencies),
			IncomingBranch: node.IncomingBranch,
			Config:         cloneConfig(node.Config),
		})
		nodeTypes[node.NodeID] = node.Type
	}

	edges := make([]GraphEdge, 0)
	branches := map[string][]BranchTarget{}
	seenBranch := map[string]map[string]struct{}{} // conditionID → branch+target

	for _, node := range plan.Nodes {
		for _, dep := range node.Dependencies {
			edge := GraphEdge{From: dep, To: node.NodeID}
			if node.IncomingBranch != "" && nodeTypes[dep] == "Condition" {
				edge.Branch = node.IncomingBranch
				if seenBranch[dep] == nil {
					seenBranch[dep] = map[string]struct{}{}
				}
				key := node.IncomingBranch + "\x00" + node.NodeID
				if _, ok := seenBranch[dep][key]; !ok {
					seenBranch[dep][key] = struct{}{}
					branches[dep] = append(branches[dep], BranchTarget{
						Branch:     node.IncomingBranch,
						TargetNode: node.NodeID,
					})
				}
			}
			edges = append(edges, edge)
		}
	}

	if len(branches) == 0 {
		branches = nil
	}

	return GraphIR{
		WorkflowID: plan.WorkflowID,
		Engine:     engine,
		Nodes:      nodes,
		Edges:      edges,
		Branches:   branches,
		Coverage:   coverage,
	}
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

// cloneConfig deep-clones node config via JSON so IR is isolated from plan.
func cloneConfig(config map[string]any) map[string]any {
	if len(config) == 0 {
		return nil
	}
	raw, err := json.Marshal(config)
	if err != nil {
		// Fall back to shallow copy of top-level keys if config is not JSON-safe.
		out := make(map[string]any, len(config))
		for k, v := range config {
			out[k] = v
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		copied := make(map[string]any, len(config))
		for k, v := range config {
			copied[k] = v
		}
		return copied
	}
	return out
}
