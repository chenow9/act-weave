package einoruntime

import (
	"context"
	"fmt"
	"strings"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflowtranslator"

	"github.com/cloudwego/eino/compose"
)

// GraphBuildConfig configures true node-graph compilation for eino_core.
type GraphBuildConfig struct {
	// Invoker runs Tool nodes. Required when the plan contains Tool nodes;
	// optional for pure transform/condition graphs in tests.
	Invoker WorkflowToolInvoker
	// RevisionResolver resolves published child plans for SubWorkflow nodes
	// (PR13c). Required when the plan contains SubWorkflow nodes.
	RevisionResolver WorkflowRevisionResolver
	// CheckPointStore enables Approval interrupt persistence. Required for
	// Approval resume; optional for simple invoke-only graphs.
	CheckPointStore compose.CheckPointStore
	// Engine is the coverage/build engine (default eino_core).
	Engine string
}

// WorkflowRevisionResolver resolves a published child workflow plan for
// SubWorkflow nodes (aligned with workflowruntime.WorkflowRevisionResolver).
type WorkflowRevisionResolver interface {
	ResolvePublishedRevision(workflowID string) (domain.WorkflowRevision, error)
}

// CompiledWorkflowGraph is a compiled compose Runnable plus the IR it was built from.
type CompiledWorkflowGraph struct {
	IR       workflowtranslator.GraphIR
	Runnable compose.Runnable[GraphInput, GraphResult]
	// EngineVersion is the graph-build code version baked into the cache key.
	EngineVersion string
}

// BuildWorkflowGraph projects plan → GraphIR (coverage-checked) → true compose Graph.
//
// Core nodes: Start, End, Tool, Condition, Transform, Approval, Parallel (PR13a),
// HTTP (PR13b simulation), SubWorkflow (PR13c nested + CompositeInterrupt),
// ForEach (PR13d scoped sequential iteration).
func BuildWorkflowGraph(
	ctx context.Context,
	plan domain.CompiledExecutionPlan,
	cfg GraphBuildConfig,
) (*CompiledWorkflowGraph, error) {
	engine := strings.TrimSpace(cfg.Engine)
	if engine == "" {
		engine = workflowtranslator.EngineEinoCore
	}
	ir, err := workflowtranslator.BuildGraphIR(plan, engine)
	if err != nil {
		return nil, err
	}
	runnable, err := compileGraphIR(ctx, ir, cfg)
	if err != nil {
		return nil, err
	}
	return &CompiledWorkflowGraph{
		IR:            ir,
		Runnable:      runnable,
		EngineVersion: workflowtranslator.GraphEngineVersion,
	}, nil
}

// BuildWorkflowGraphFromIR compiles a pre-built GraphIR (already coverage-checked).
func BuildWorkflowGraphFromIR(
	ctx context.Context,
	ir workflowtranslator.GraphIR,
	cfg GraphBuildConfig,
) (*CompiledWorkflowGraph, error) {
	if len(ir.Coverage.Unsupported) > 0 {
		return nil, fmt.Errorf(
			"einoruntime: GraphIR has unsupported node types under %s: %s",
			ir.Engine,
			strings.Join(ir.Coverage.Unsupported, ", "),
		)
	}
	runnable, err := compileGraphIR(ctx, ir, cfg)
	if err != nil {
		return nil, err
	}
	return &CompiledWorkflowGraph{
		IR:            ir,
		Runnable:      runnable,
		EngineVersion: workflowtranslator.GraphEngineVersion,
	}, nil
}

// composeNodeKey maps a plan NodeID to a compose graph key.
//
// Eino reserves compose.START ("start") and compose.END ("end"), so plan nodes
// (which commonly use those ids) are namespaced. Scope NodeOutputs still use
// the original plan NodeID for value_resolver path compatibility.
func composeNodeKey(planNodeID string) string {
	return "n:" + planNodeID
}

func compileGraphIR(
	ctx context.Context,
	ir workflowtranslator.GraphIR,
	cfg GraphBuildConfig,
) (compose.Runnable[GraphInput, GraphResult], error) {
	if len(ir.Nodes) == 0 {
		return nil, fmt.Errorf("einoruntime: GraphIR has no nodes")
	}

	branchLabels := map[string][]string{}
	for condID, targets := range ir.Branches {
		for _, t := range targets {
			branchLabels[condID] = append(branchLabels[condID], t.Branch)
		}
	}
	engine := strings.TrimSpace(cfg.Engine)
	if engine == "" {
		engine = workflowtranslator.EngineEinoCore
	}
	deps := nodeDeps{
		invoker:            cfg.Invoker,
		revisionResolver:   cfg.RevisionResolver,
		checkPointStore:    cfg.CheckPointStore,
		engine:             engine,
		branchLabels:       branchLabels,
		foreachControllers: buildForEachControllersFromIR(ir),
	}

	g := compose.NewGraph[GraphInput, GraphResult](
		compose.WithGenLocalState(func(ctx context.Context) *GraphState {
			// Prefer a caller-provided holder so interrupt paths can read partial state.
			if held := graphStateFromContext(ctx); held != nil {
				return held
			}
			return &GraphState{
				Scope: GraphScope{
					Input:        map[string]any{},
					NodeOutputs:  map[string]map[string]any{},
					WorkflowVars: map[string]any{},
				},
				SelectedBranches: map[string]string{},
				Status:           domain.ExecutionSuccess,
			}
		}),
	)

	endNodeIDs := make([]string, 0, 1)
	for _, node := range ir.Nodes {
		if err := addNodeToGraph(g, node, deps); err != nil {
			return nil, err
		}
		if node.Type == "End" {
			endNodeIDs = append(endNodeIDs, node.ID)
		}
	}

	// Control edges: non-branch dependencies.
	// Branch edges are wired via AddBranch below.
	for _, edge := range ir.Edges {
		if edge.Branch != "" {
			// Condition → branch target is controlled by GraphBranch.
			continue
		}
		from := composeNodeKey(edge.From)
		to := composeNodeKey(edge.To)
		if err := g.AddEdge(from, to); err != nil {
			return nil, fmt.Errorf("add edge %s → %s: %w", from, to, err)
		}
	}

	// Condition branches (keys are plan node IDs; map to compose keys).
	for condID, targets := range ir.Branches {
		if len(targets) == 0 {
			continue
		}
		endNodes := make(map[string]bool, len(targets))
		mappedTargets := make([]workflowtranslator.BranchTarget, len(targets))
		for i, t := range targets {
			key := composeNodeKey(t.TargetNode)
			endNodes[key] = true
			mappedTargets[i] = workflowtranslator.BranchTarget{
				Branch:     t.Branch,
				TargetNode: key,
			}
		}
		branch := compose.NewGraphBranch(branchCondition(condID, mappedTargets), endNodes)
		if err := g.AddBranch(composeNodeKey(condID), branch); err != nil {
			return nil, fmt.Errorf("add branch from %s: %w", condID, err)
		}
	}

	// START → entry nodes (no plan dependencies).
	for _, node := range ir.Nodes {
		if len(node.Dependencies) == 0 {
			key := composeNodeKey(node.ID)
			if err := g.AddEdge(compose.START, key); err != nil {
				return nil, fmt.Errorf("add START → %s: %w", key, err)
			}
		}
	}

	// End nodes → compose.END.
	if len(endNodeIDs) == 0 {
		return nil, fmt.Errorf("einoruntime: plan has no End node")
	}
	for _, endID := range endNodeIDs {
		key := composeNodeKey(endID)
		if err := g.AddEdge(key, compose.END); err != nil {
			return nil, fmt.Errorf("add %s → END: %w", key, err)
		}
	}

	compileOpts := []compose.GraphCompileOption{
		compose.WithNodeTriggerMode(compose.AllPredecessor),
		compose.WithGraphName("actweave_eino_core"),
	}
	if cfg.CheckPointStore != nil {
		compileOpts = append(compileOpts, compose.WithCheckPointStore(cfg.CheckPointStore))
	}

	runnable, err := g.Compile(ctx, compileOpts...)
	if err != nil {
		return nil, fmt.Errorf("compile eino_core graph: %w", err)
	}
	return runnable, nil
}

func addNodeToGraph(
	g *compose.Graph[GraphInput, GraphResult],
	node workflowtranslator.GraphNode,
	deps nodeDeps,
) error {
	key := composeNodeKey(node.ID)
	var err error
	switch node.Type {
	case "Start":
		err = g.AddLambdaNode(key, buildStartLambda(node), compose.WithNodeName(key))
	case "Tool":
		err = g.AddLambdaNode(key, buildToolLambda(node, deps), compose.WithNodeName(key))
	case "Transform":
		err = g.AddLambdaNode(key, buildTransformLambda(node, deps), compose.WithNodeName(key))
	case "Condition":
		err = g.AddLambdaNode(key, buildConditionLambda(node, deps), compose.WithNodeName(key))
	case "Approval":
		err = g.AddLambdaNode(key, buildApprovalLambda(node), compose.WithNodeName(key))
	case "Parallel":
		err = g.AddLambdaNode(key, buildParallelLambda(node, deps), compose.WithNodeName(key))
	case "HTTP":
		err = g.AddLambdaNode(key, buildHTTPLambda(node, deps), compose.WithNodeName(key))
	case "SubWorkflow":
		err = g.AddLambdaNode(key, buildSubWorkflowLambda(node, deps), compose.WithNodeName(key))
	case "ForEach":
		err = g.AddLambdaNode(key, buildForEachLambda(node), compose.WithNodeName(key))
	case "End":
		err = g.AddLambdaNode(key, buildEndLambda(node), compose.WithNodeName(key))
	default:
		return fmt.Errorf("einoruntime: unsupported core node type %q (use wrapper or full eino)", node.Type)
	}
	if err != nil {
		return fmt.Errorf("add node %s (%s): %w", key, node.Type, err)
	}
	return nil
}

// graphStateHolderKey carries a *GraphState for GenLocalState + post-interrupt reads.
type graphStateHolderKey struct{}

// WithGraphStateHolder injects a state pointer that GenLocalState will return.
// Callers use this so Approval interrupt can still project domain.Execution
// after Invoke returns an interrupt error.
func WithGraphStateHolder(ctx context.Context, state *GraphState) context.Context {
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, graphStateHolderKey{}, state)
}

func graphStateFromContext(ctx context.Context) *GraphState {
	if ctx == nil {
		return nil
	}
	st, _ := ctx.Value(graphStateHolderKey{}).(*GraphState)
	return st
}

// pendingApprovalDecisionKey carries the platform ApprovalDecision on resume so
// composite SubWorkflow nodes (descendant is the compose target) can forward
// the decision into nested CoreGraphRunner.ResumeApproval.
type pendingApprovalDecisionKey struct{}

// WithPendingApprovalDecision injects the resume decision for nested SubWorkflow
// conduit resume (PR13c). Nil/empty decision is a no-op.
func WithPendingApprovalDecision(ctx context.Context, decision ApprovalDecision) context.Context {
	if ctx == nil {
		return ctx
	}
	if strings.TrimSpace(decision.Decision) == "" {
		return ctx
	}
	return context.WithValue(ctx, pendingApprovalDecisionKey{}, decision)
}

// PendingApprovalDecision returns a decision previously set by
// WithPendingApprovalDecision / CoreGraphRunner.ResumeApproval.
func PendingApprovalDecision(ctx context.Context) (ApprovalDecision, bool) {
	if ctx == nil {
		return ApprovalDecision{}, false
	}
	d, ok := ctx.Value(pendingApprovalDecisionKey{}).(ApprovalDecision)
	if !ok || strings.TrimSpace(d.Decision) == "" {
		return ApprovalDecision{}, false
	}
	return d, true
}
