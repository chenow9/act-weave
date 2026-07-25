package workflowtranslator

import (
	"fmt"
	"strings"

	"actweave/backend/internal/domain"
)

// Engine mode names (aligned with config.WorkflowEngine*).
//
// Production name after Load (P0): "eino" (compose CoreGraphRunner).
// "eino_core" is a historical alias — same runner as "eino" in
// workflowruntime.NewCompiledPlanExecutor. Coverage treats both as compose.
const (
	EngineEinoCore = "eino_core"
	EngineEino     = "eino"
)

// CoverageStatus classifies a plan node type against a target engine.
type CoverageStatus string

const (
	// CoverageNative means the node type maps to a true compose graph node
	// under the requested engine (eino_core or eino).
	CoverageNative CoverageStatus = "native"
	// CoverageUnsupported means the node cannot run under the requested
	// engine; callers must use wrapper PlanRunner (or a higher engine).
	CoverageUnsupported CoverageStatus = "unsupported"
)

// NodeCoverage is one row of the §4.3 coverage matrix for a node type.
type NodeCoverage struct {
	NodeType string         `json:"nodeType"`
	Status   CoverageStatus `json:"status"`
	// MinimumEngine is the lowest engine mode that treats this type as native
	// (eino_core for core nodes; eino for advanced). Empty when unknown.
	MinimumEngine string `json:"minimumEngine,omitempty"`
	// Reason is a short, stable explanation for diagnostics / trial UI.
	Reason string `json:"reason,omitempty"`
	// InterruptResumeImpact documents HITL behaviour for this node type.
	InterruptResumeImpact string `json:"interruptResumeImpact,omitempty"`
}

// PlanCoverage is the coverage projection of every distinct node type in a plan.
type PlanCoverage struct {
	// Engine is the mode used for evaluation (eino_core or eino).
	Engine string `json:"engine"`
	// ByType maps node type → coverage row (only types present in the plan,
	// plus callers may also use CoverageFor on the package matrix).
	ByType map[string]NodeCoverage `json:"byType"`
	// Unsupported lists node types that are not native under Engine.
	Unsupported []string `json:"unsupported,omitempty"`
}

// CoverageFor returns the matrix row for nodeType under engine.
// Unknown types are CoverageUnsupported with an actionable reason.
func CoverageFor(nodeType, engine string) NodeCoverage {
	nodeType = strings.TrimSpace(nodeType)
	engine = normalizeEngine(engine)
	base := matrixRow(nodeType)
	return projectCoverage(base, engine)
}

// EvaluateCoverage classifies every distinct node type in plan under engine.
func EvaluateCoverage(plan domain.CompiledExecutionPlan, engine string) PlanCoverage {
	engine = normalizeEngine(engine)
	byType := make(map[string]NodeCoverage)
	var unsupported []string
	seenUnsupported := map[string]struct{}{}

	for _, node := range plan.Nodes {
		nodeType := strings.TrimSpace(node.Type)
		if nodeType == "" {
			nodeType = "<empty>"
		}
		if _, ok := byType[nodeType]; ok {
			continue
		}
		row := CoverageFor(nodeType, engine)
		byType[nodeType] = row
		if row.Status != CoverageNative {
			if _, seen := seenUnsupported[nodeType]; !seen {
				seenUnsupported[nodeType] = struct{}{}
				unsupported = append(unsupported, nodeType)
			}
		}
	}

	return PlanCoverage{
		Engine:      engine,
		ByType:      byType,
		Unsupported: unsupported,
	}
}

// SupportsEinoCore reports whether every node in plan is native under eino_core
// (Start/End/Tool/Condition/Transform/Approval/Parallel/HTTP/SubWorkflow/ForEach;
// unknown types fail).
func SupportsEinoCore(plan domain.CompiledExecutionPlan) bool {
	return len(EvaluateCoverage(plan, EngineEinoCore).Unsupported) == 0
}

// SupportsEino reports whether every node in plan is native under full eino
// mode (core + advanced known types).
func SupportsEino(plan domain.CompiledExecutionPlan) bool {
	return len(EvaluateCoverage(plan, EngineEino).Unsupported) == 0
}

// IsCoreNodeType reports whether nodeType is in the eino_core native set
// (including Approval under strategy C, Parallel under PR13a, HTTP under PR13b,
// SubWorkflow under PR13c, ForEach under PR13d).
func IsCoreNodeType(nodeType string) bool {
	return matrixRow(strings.TrimSpace(nodeType)).MinimumEngine == EngineEinoCore
}

// IsAdvancedNodeType reports whether nodeType requires full eino mode
// (no known plan types remain advanced after PR13d; unknown types are not advanced).
func IsAdvancedNodeType(nodeType string) bool {
	return matrixRow(strings.TrimSpace(nodeType)).MinimumEngine == EngineEino
}

// matrixRow is the static §4.3 matrix independent of evaluation engine.
// MinimumEngine = eino_core → native on both eino_core and eino.
// MinimumEngine = eino      → unsupported on eino_core, native on eino.
func matrixRow(nodeType string) NodeCoverage {
	switch nodeType {
	case "Start", "End", "Transform", "Tool", "Condition":
		return NodeCoverage{
			NodeType:              nodeType,
			MinimumEngine:         EngineEinoCore,
			Reason:                "core plan node; native compose graph under eino_core",
			InterruptResumeImpact: "completes within the graph turn; no HITL interrupt",
		}
	case "Approval":
		// Strategy C: Approval is native interrupt on eino_core (not deferred).
		return NodeCoverage{
			NodeType:              "Approval",
			MinimumEngine:         EngineEinoCore,
			Reason:                "Approval is native under strategy C (StatefulInterrupt + product HITL)",
			InterruptResumeImpact: "compose.StatefulInterrupt; platform confirmation + compose resume (not whole-plan re-run)",
		}
	case "Parallel":
		// PR13a: Parallel is a fan-out/join barrier in the true compose DAG
		// (AllPredecessor). Branch nodes are ordinary plan successors of Parallel.
		return NodeCoverage{
			NodeType:              "Parallel",
			MinimumEngine:         EngineEinoCore,
			Reason:                "Parallel fan-out/join is native under eino_core compose DAG (PR13a)",
			InterruptResumeImpact: "completes within the graph turn; branch nodes join at shared successors",
		}
	case "HTTP":
		// PR13b: HTTP is a compose lambda that mirrors plan_runner simulation
		// (method/endpoint/request/status=ok; no real network egress).
		return NodeCoverage{
			NodeType:              "HTTP",
			MinimumEngine:         EngineEinoCore,
			Reason:                "HTTP simulation lambda is native under eino_core (PR13b; no real network)",
			InterruptResumeImpact: "completes within the graph turn; dry status=ok like plan_runner",
		}
	case "SubWorkflow":
		// PR13c: nested plan via recursive CoreGraphRunner; nested Approval
		// bubbles with compose.CompositeInterrupt (strategy C).
		return NodeCoverage{
			NodeType:              "SubWorkflow",
			MinimumEngine:         EngineEinoCore,
			Reason:                "SubWorkflow nested CoreGraphRunner is native under eino_core (PR13c)",
			InterruptResumeImpact: "nested Approval bubbles via compose.CompositeInterrupt; child checkpoint + interrupt IDs in SubWorkflowInterruptState",
		}
	case "ForEach":
		// PR13d: ForEach seeds collection metadata; body successors run with
		// loop-scoped ForeachItem/ForeachAlias (plan_runner-aligned sequential).
		return NodeCoverage{
			NodeType:              "ForEach",
			MinimumEngine:         EngineEinoCore,
			Reason:                "ForEach scoped iteration is native under eino_core (PR13d)",
			InterruptResumeImpact: "completes within the graph turn; body nodes loop sequentially with scoped outputs",
		}
	default:
		return NodeCoverage{
			NodeType:              nodeType,
			MinimumEngine:         "",
			Reason:                fmt.Sprintf("no compiled-plan → Graph IR mapping registered for node type %q", nodeType),
			InterruptResumeImpact: "cannot run under eino_core/eino until explicitly mapped",
		}
	}
}

func projectCoverage(base NodeCoverage, engine string) NodeCoverage {
	out := base
	switch {
	case base.MinimumEngine == "":
		out.Status = CoverageUnsupported
	case engine == EngineEino:
		// Full eino mode: anything with a known MinimumEngine is native.
		out.Status = CoverageNative
	case engine == EngineEinoCore:
		if base.MinimumEngine == EngineEinoCore {
			out.Status = CoverageNative
		} else {
			out.Status = CoverageUnsupported
			if out.Reason == "" {
				out.Reason = "requires full eino mode"
			}
		}
	default:
		// Unknown engine → fail closed.
		out.Status = CoverageUnsupported
		out.Reason = fmt.Sprintf("unknown engine %q; only eino_core and eino are evaluated", engine)
	}
	return out
}

func normalizeEngine(engine string) string {
	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "" {
		return EngineEinoCore
	}
	return engine
}
