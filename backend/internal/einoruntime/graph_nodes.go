package einoruntime

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflowtranslator"

	"github.com/cloudwego/eino/compose"
)

// WorkflowToolCall is the invoker request for a workflow Tool node.
type WorkflowToolCall struct {
	ToolID              string
	Input               map[string]any
	NodeID              string
	TraceID             string
	WorkflowID          string
	WorkspaceID         string
	UserID              string
	ActorType           string
	AgentRunID          string
	WorkflowExecutionID string
	ExecutionStepID     string
}

// WorkflowToolInvoker is the thin tool surface used by eino_core Tool nodes.
// workflowruntime.ToolInvoker adapts to this interface.
type WorkflowToolInvoker interface {
	Invoke(ctx context.Context, call WorkflowToolCall) (map[string]any, error)
}

// nodeDeps are closed over by per-node lambdas at graph build time.
type nodeDeps struct {
	invoker          WorkflowToolInvoker
	revisionResolver WorkflowRevisionResolver
	checkPointStore  compose.CheckPointStore
	engine           string
	// branchLabels maps condition node ID → known out-edge branch labels.
	branchLabels map[string][]string
	// foreachControllers maps plan node ID → controlling ForEach node ID
	// (empty when not under a ForEach scope). Mirrors plan_runner
	// buildForEachControllers (PR13d).
	foreachControllers map[string]string
}

// loopBodyFn runs one body-node iteration under a loop-scoped GraphScope and
// returns that iteration's nodeOutputs entry (not yet aggregated).
// st is the parent GraphState already held by ProcessState (do not re-enter).
type loopBodyFn func(st *GraphState, scope GraphScope) (map[string]any, error)

// nestedGraphRunner builds a recursive CoreGraphRunner for SubWorkflow nodes,
// sharing invoker / store / resolver with the parent graph.
func (d nodeDeps) nestedGraphRunner() *CoreGraphRunner {
	return NewCoreGraphRunner(CoreGraphRunnerConfig{
		Invoker:          d.invoker,
		RevisionResolver: d.revisionResolver,
		CheckPointStore:  d.checkPointStore,
		Engine:           d.engine,
	})
}

func buildStartLambda(node workflowtranslator.GraphNode) *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, in GraphInput) (GraphToken, error) {
		err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
			// Seed identity from invoke input when GenLocalState produced a blank state
			// (or when a context-held state was only partially filled).
			if st.ExecutionID == "" {
				st.ExecutionID = in.ExecutionID
			}
			if st.TraceID == "" {
				st.TraceID = in.TraceID
			}
			if st.WorkspaceID == "" {
				st.WorkspaceID = in.WorkspaceID
			}
			if st.WorkflowID == "" {
				st.WorkflowID = in.WorkflowID
			}
			if st.WorkflowVersion == "" {
				st.WorkflowVersion = in.WorkflowVersion
			}
			if st.UserID == "" {
				st.UserID = in.UserID
			}
			if st.ActorType == "" {
				st.ActorType = in.ActorType
			}
			if st.AgentRunID == "" {
				st.AgentRunID = in.AgentRunID
			}
			if st.WorkflowExecutionID == "" {
				st.WorkflowExecutionID = in.WorkflowExecutionID
			}
			if st.Trigger == "" {
				st.Trigger = in.Trigger
			}
			// TrialMode is sticky once set from invoke input (模拟试运行).
			if in.TrialMode {
				st.TrialMode = true
			}
			if st.StartedAt.IsZero() {
				st.StartedAt = in.StartedAt
				if st.StartedAt.IsZero() {
					st.StartedAt = time.Now().UTC()
				}
			}
			if len(st.Scope.Input) == 0 && len(in.Input) > 0 {
				st.Scope.Input = cloneAnyMap(in.Input)
			}
			if st.Scope.Input == nil {
				st.Scope.Input = map[string]any{}
			}
			if st.Scope.NodeOutputs == nil {
				st.Scope.NodeOutputs = map[string]map[string]any{}
			}
			if st.Scope.WorkflowVars == nil {
				st.Scope.WorkflowVars = map[string]any{}
			}
			if st.SelectedBranches == nil {
				st.SelectedBranches = map[string]string{}
			}
			if st.InputSummary == "" {
				st.InputSummary = summarizeValue(st.Scope.Input)
			}
			if st.Status == "" {
				st.Status = domain.ExecutionSuccess
			}

			// Bootstrap system steps (aligned with PlanRunner preamble).
			if len(st.Steps) == 0 {
				st.Steps = append(st.Steps,
					newGraphStep(st.ExecutionID, "Auth Check", "", "", domain.ExecutionStepPassed, "JWT claims", "user="+st.UserID),
					newGraphStep(st.ExecutionID, "Workspace Load", "", "", domain.ExecutionStepPassed, st.WorkspaceID, "workspace loaded"),
					newGraphStep(st.ExecutionID, "Workflow Decision", "", "", domain.ExecutionStepPassed, st.WorkflowID, "workflow.graph.v1 -> eino_core"),
				)
			}

			if workflowVars, ok := node.Config["workflowVars"].(map[string]any); ok {
				resolved, err := resolveMapValues(workflowVars, st.Scope)
				if err != nil {
					return err
				}
				st.Scope.WorkflowVars = mergeAnyMaps(st.Scope.WorkflowVars, resolved)
			}

			st.Steps = append(st.Steps, newGraphStep(
				st.ExecutionID,
				nodeLabel(node.Config, "Start"),
				node.ID,
				node.Type,
				domain.ExecutionStepPassed,
				summarizeValue(st.Scope.Input),
				"variables available",
			))
			st.Scope.NodeOutputs[node.ID] = cloneAnyMap(st.Scope.Input)
			return nil
		})
		if err != nil {
			return nil, err
		}
		return newGraphToken(node.ID), nil
	})
}

func buildToolLambda(node workflowtranslator.GraphNode, deps nodeDeps) *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, _ GraphToken) (GraphToken, error) {
		if controllerID := foreachControllerFor(deps, node); controllerID != "" {
			return runLoopControlledNode(ctx, node, controllerID, deps, func(st *GraphState, scope GraphScope) (map[string]any, error) {
				result, _, err := invokeToolWithStep(ctx, node, deps, st, scope)
				return result, err
			})
		}
		var out GraphToken
		err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
			result, stepInput, err := invokeToolWithStep(ctx, node, deps, st, st.Scope)
			if err != nil {
				step := newGraphStep(st.ExecutionID, "Runtime Call", node.ID, node.Type, domain.ExecutionStepFailed, stepInput, "")
				step.ErrorMessage = err.Error()
				st.Steps = append(st.Steps, step)
				st.Status = domain.ExecutionFailed
				st.ErrorMessage = err.Error()
				return err
			}
			st.Steps = append(st.Steps, newGraphStep(
				st.ExecutionID, "Runtime Call", node.ID, node.Type,
				domain.ExecutionStepPassed, stepInput, summarizeValue(result),
			))
			st.Scope.NodeOutputs[node.ID] = result
			out = newGraphToken(node.ID)
			return nil
		})
		return out, err
	})
}

func invokeToolWithStep(
	ctx context.Context,
	node workflowtranslator.GraphNode,
	deps nodeDeps,
	st *GraphState,
	scope GraphScope,
) (map[string]any, string, error) {
	toolID, _ := node.Config["toolId"].(string)
	resolvedInput, err := resolveToolInput(node.Config, scope)
	if err != nil {
		return nil, "", err
	}
	stepInput := "standard ToolInvocation toolId=" + toolID
	if st != nil && st.WorkspaceID != "" {
		stepInput += " workspaceId=" + st.WorkspaceID
	}
	if st != nil {
		stepInput += " traceId=" + st.TraceID
	}
	stepInput += " input=" + summarizeValue(resolvedInput)

	if deps.invoker == nil {
		return nil, stepInput, fmt.Errorf("workflow tool invoker is not configured")
	}
	if st == nil {
		return nil, stepInput, fmt.Errorf("workflow graph state is not available")
	}

	// Only forward a durable WorkflowExecutionID. Falling back to the ephemeral
	// graph ExecutionID breaks tool_invocations.workflow_execution_id FK when
	// chat/AAP invoke a published WORKFLOW without a pre-created execution row
	// (trial and production :execute always set a real id).
	result, err := deps.invoker.Invoke(ctx, WorkflowToolCall{
		ToolID:              toolID,
		Input:               resolvedInput,
		NodeID:              node.ID,
		TraceID:             st.TraceID,
		WorkflowID:          st.WorkflowID,
		WorkspaceID:         st.WorkspaceID,
		UserID:              st.UserID,
		ActorType:           st.ActorType,
		AgentRunID:          st.AgentRunID,
		WorkflowExecutionID: strings.TrimSpace(st.WorkflowExecutionID),
	})
	if err != nil {
		return nil, stepInput, err
	}
	return normalizeToolResult(result), stepInput, nil
}

func buildTransformLambda(node workflowtranslator.GraphNode, deps nodeDeps) *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, _ GraphToken) (GraphToken, error) {
		if controllerID := foreachControllerFor(deps, node); controllerID != "" {
			return runLoopControlledNode(ctx, node, controllerID, deps, func(_ *GraphState, scope GraphScope) (map[string]any, error) {
				template, _ := node.Config["template"].(string)
				output, err := renderTemplate(template, scope)
				if err != nil {
					return nil, err
				}
				return map[string]any{"result": output}, nil
			})
		}
		var out GraphToken
		err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
			template, _ := node.Config["template"].(string)
			output, err := renderTemplate(template, st.Scope)
			if err != nil {
				return err
			}
			st.Steps = append(st.Steps, newGraphStep(
				st.ExecutionID,
				nodeLabel(node.Config, "Transform"),
				node.ID,
				node.Type,
				domain.ExecutionStepPassed,
				template,
				output,
			))
			st.Scope.NodeOutputs[node.ID] = map[string]any{"result": output}
			out = newGraphToken(node.ID)
			return nil
		})
		return out, err
	})
}

func buildConditionLambda(node workflowtranslator.GraphNode, deps nodeDeps) *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, _ GraphToken) (GraphToken, error) {
		// plan_runner rejects Condition inside ForEach loops.
		if controllerID := foreachControllerFor(deps, node); controllerID != "" {
			return nil, fmt.Errorf("foreach-controlled Condition nodes are not supported in trial run")
		}
		var out GraphToken
		err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
			expression, _ := node.Config["expression"].(string)
			if expression == "" {
				expression, _ = node.Config["condition"].(string)
			}
			matched, err := evaluateCondition(expression, st.Scope)
			if err != nil {
				return err
			}
			selected, err := selectConditionBranch(node.ID, matched, deps.branchLabels[node.ID])
			if err != nil {
				return err
			}
			st.SelectedBranches[node.ID] = selected
			st.Steps = append(st.Steps, newGraphStep(
				st.ExecutionID,
				nodeLabel(node.Config, "Condition"),
				node.ID,
				node.Type,
				domain.ExecutionStepPassed,
				expression,
				"condition routed to "+selected,
			))
			st.Scope.NodeOutputs[node.ID] = map[string]any{"branch": selected, "result": matched}
			out = newGraphToken(node.ID)
			return nil
		})
		return out, err
	})
}

func buildApprovalLambda(node workflowtranslator.GraphNode) *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, _ GraphToken) (GraphToken, error) {
		wasInterrupted, _, stPrev := compose.GetInterruptState[ApprovalInterruptState](ctx)
		isTarget, hasData, decision := compose.GetResumeContext[ApprovalDecision](ctx)

		if wasInterrupted {
			if !isTarget {
				// Sibling resume target — re-interrupt to preserve state.
				return nil, compose.StatefulInterrupt(ctx, approvalInterruptInfo, stPrev)
			}
			if !hasData {
				return nil, fmt.Errorf("eino workflow approval resume missing decision data")
			}
			switch strings.ToLower(strings.TrimSpace(decision.Decision)) {
			case ApprovalDecisionConfirmed:
				err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
					resolvedAt := time.Now().UTC()
					requestedAt := time.Time{}
					if prev, ok := st.Scope.NodeOutputs[node.ID]; ok {
						if raw, _ := prev["requestedAt"].(string); raw != "" {
							if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
								requestedAt = t
							}
						}
					}
					st.Steps = append(st.Steps, newGraphStep(
						st.ExecutionID,
						"Approval Confirmed",
						node.ID,
						node.Type,
						domain.ExecutionStepPassed,
						st.PendingApprovalReason,
						approvalAuditSummary(ApprovalDecisionConfirmed, st.UserID, requestedAt, decision.ResolvedBy, resolvedAt),
					))
					st.Scope.NodeOutputs[node.ID] = map[string]any{
						"approval":    ApprovalDecisionConfirmed,
						"decision":    ApprovalDecisionConfirmed,
						"requestedBy": st.UserID,
						"resolvedBy":  decision.ResolvedBy,
						"resolvedAt":  resolvedAt.Format(time.RFC3339Nano),
					}
					if !requestedAt.IsZero() {
						st.Scope.NodeOutputs[node.ID]["requestedAt"] = requestedAt.Format(time.RFC3339Nano)
					}
					st.Status = domain.ExecutionSuccess
					st.ErrorMessage = ""
					st.PendingApprovalNodeID = ""
					st.ReachedTerminal = false
					return nil
				})
				if err != nil {
					return nil, err
				}
				return newGraphToken(node.ID), nil
			case ApprovalDecisionCancelled:
				err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
					resolvedAt := time.Now().UTC()
					st.Steps = append(st.Steps, newGraphStep(
						st.ExecutionID,
						"Approval Cancelled",
						node.ID,
						node.Type,
						domain.ExecutionStepCancelled,
						st.PendingApprovalReason,
						approvalAuditSummary(ApprovalDecisionCancelled, st.UserID, time.Time{}, decision.ResolvedBy, resolvedAt),
					))
					st.Status = domain.ExecutionFailed
					st.ErrorMessage = "workflow approval cancelled"
					st.OutputSummary = "Workflow approval was cancelled"
					st.ReachedTerminal = true
					return fmt.Errorf("workflow approval cancelled")
				})
				return nil, err
			default:
				return nil, fmt.Errorf("unsupported approval decision %q", decision.Decision)
			}
		}

		// TrialMode (模拟试运行): auto-confirm Approval without StatefulInterrupt so
		// compile→trial→publish can finish on graphs that include Approval (D11:
		// production still pauses for real confirmation).
		var trialAuto bool
		_ = compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
			trialAuto = st != nil && st.TrialMode
			return nil
		})
		if trialAuto {
			err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
				reason, _ := node.Config["reason"].(string)
				requestedAt := time.Now().UTC()
				resolvedAt := requestedAt
				resolvedBy := "trial-auto"
				st.Steps = append(st.Steps, newGraphStep(
					st.ExecutionID,
					nodeLabel(node.Config, "Approval"),
					node.ID,
					node.Type,
					domain.ExecutionStepPassed,
					reason,
					approvalAuditSummary(ApprovalDecisionConfirmed, st.UserID, requestedAt, resolvedBy, resolvedAt),
				))
				st.Scope.NodeOutputs[node.ID] = map[string]any{
					"approval":    ApprovalDecisionConfirmed,
					"decision":    ApprovalDecisionConfirmed,
					"requestedBy": st.UserID,
					"requestedAt": requestedAt.Format(time.RFC3339Nano),
					"resolvedBy":  resolvedBy,
					"resolvedAt":  resolvedAt.Format(time.RFC3339Nano),
					"trialAuto":   true,
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			return newGraphToken(node.ID), nil
		}

		// First run: record waiting step, then StatefulInterrupt (no whole-plan re-run).
		var interruptState ApprovalInterruptState
		err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
			reason, _ := node.Config["reason"].(string)
			requestedAt := time.Now().UTC()
			st.Steps = append(st.Steps, newGraphStep(
				st.ExecutionID,
				nodeLabel(node.Config, "Approval"),
				node.ID,
				node.Type,
				domain.ExecutionStepWaitingApproval,
				reason,
				approvalAuditSummary("pending", st.UserID, requestedAt, "", time.Time{}),
			))
			st.Status = domain.ExecutionApproval
			st.ReachedTerminal = true
			st.PendingApprovalNodeID = node.ID
			st.PendingApprovalReason = reason
			st.OutputSummary = "Workflow trial run is blocked by Approval node"
			st.Scope.NodeOutputs[node.ID] = map[string]any{
				"approval":    "pending",
				"decision":    "pending",
				"requestedBy": st.UserID,
				"requestedAt": requestedAt.Format(time.RFC3339Nano),
			}
			// Result Return step for approval pause (aligned with executors_core finish).
			st.Steps = append(st.Steps, newGraphStep(
				st.ExecutionID,
				"Result Return",
				"",
				"",
				domain.ExecutionStepWaitingApproval,
				st.WorkflowID,
				"waiting for approval.result",
			))
			interruptState = ApprovalInterruptState{
				SchemaVersion: ApprovalInterruptSchemaVersion,
				NodeID:        node.ID,
				ExecutionID:   st.ExecutionID,
				WorkflowID:    st.WorkflowID,
				WorkspaceID:   st.WorkspaceID,
				Reason:        reason,
				RequestedBy:   st.UserID,
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return nil, compose.StatefulInterrupt(ctx, approvalInterruptInfo, interruptState)
	})
}

// buildParallelLambda is the eino_core fan-out/join barrier (PR13a).
//
// Semantics align with plan_runner.executeParallelNode outputs (branches,
// branchCount, trace) so End/ref paths keep working. True concurrency of
// branch successors is the compose DAG (AllPredecessor): after Parallel
// completes, every plan node that depends only on Parallel becomes ready and
// may run concurrently; shared successors join when all predecessors finish.
//
// Unlike wrapper's sequential-simulation mode, mode is "graph-fanout".
// When under a ForEach controller (PR13d), the barrier body runs per item
// and results are aggregated like other loop-controlled nodes.
func buildParallelLambda(node workflowtranslator.GraphNode, deps nodeDeps) *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, _ GraphToken) (GraphToken, error) {
		if controllerID := foreachControllerFor(deps, node); controllerID != "" {
			return runLoopControlledNode(ctx, node, controllerID, deps, func(_ *GraphState, _ GraphScope) (map[string]any, error) {
				return parallelNodeOutput(node), nil
			})
		}
		var out GraphToken
		err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
			output := parallelNodeOutput(node)
			branchTrace, _ := output["trace"].(string)
			st.Steps = append(st.Steps, newGraphStep(
				st.ExecutionID,
				nodeLabel(node.Config, "Parallel"),
				node.ID,
				node.Type,
				domain.ExecutionStepPassed,
				summarizeValue(output["branches"]),
				branchTrace,
			))
			st.Scope.NodeOutputs[node.ID] = output
			out = newGraphToken(node.ID)
			return nil
		})
		return out, err
	})
}

func parallelNodeOutput(node workflowtranslator.GraphNode) map[string]any {
	branches := configStringSlice(node.Config["branches"])
	branchTrace := "graph fan-out branches=" + strings.Join(branches, " | ")
	return map[string]any{
		"branches":    branches,
		"branchCount": len(branches),
		"trace":       branchTrace,
		"mode":        "graph-fanout",
	}
}

// buildHTTPLambda is the eino_core HTTP simulation node (PR13b).
//
// Mirrors plan_runner.executeHTTPNode: records method/endpoint, optionally
// resolves input/inputMapping, emits a Passed step with input summary
// "METHOD endpoint" and output summarizing the resolved request, and writes
// nodeOutputs {method, endpoint, request, status:"ok"}. No real network
// egress — same dry semantics as the wrapper interpreter.
// When under a ForEach controller (PR13d), runs once per item with scoped input.
func buildHTTPLambda(node workflowtranslator.GraphNode, deps nodeDeps) *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, _ GraphToken) (GraphToken, error) {
		if controllerID := foreachControllerFor(deps, node); controllerID != "" {
			return runLoopControlledNode(ctx, node, controllerID, deps, func(_ *GraphState, scope GraphScope) (map[string]any, error) {
				return httpNodeOutput(node, scope)
			})
		}
		var out GraphToken
		err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
			output, err := httpNodeOutput(node, st.Scope)
			if err != nil {
				return err
			}
			method, _ := output["method"].(string)
			endpoint, _ := output["endpoint"].(string)
			st.Steps = append(st.Steps, newGraphStep(
				st.ExecutionID,
				nodeLabel(node.Config, "HTTP"),
				node.ID,
				node.Type,
				domain.ExecutionStepPassed,
				fmt.Sprintf("%s %s", method, endpoint),
				summarizeValue(output["request"]),
			))
			st.Scope.NodeOutputs[node.ID] = output
			out = newGraphToken(node.ID)
			return nil
		})
		return out, err
	})
}

func httpNodeOutput(node workflowtranslator.GraphNode, scope GraphScope) (map[string]any, error) {
	method, _ := node.Config["method"].(string)
	endpoint, _ := node.Config["endpoint"].(string)
	input, err := resolveOptionalNodeInput(node.Config, scope)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"method":   method,
		"endpoint": endpoint,
		"request":  input,
		"status":   "ok",
	}, nil
}

// buildForEachLambda is the eino_core ForEach seed node (PR13d).
//
// Aligns with plan_runner.executeForEachNode: resolve collection, record
// items/count/itemAlias/concurrency (+ optional __configuredOutput for body
// loop-output mapping). Body successors identified by foreachControllers run
// sequential per-item iterations via runLoopControlledNode.
func buildForEachLambda(node workflowtranslator.GraphNode) *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, _ GraphToken) (GraphToken, error) {
		var out GraphToken
		err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
			collectionValue, err := resolveValue(node.Config["collection"], st.Scope)
			if err != nil {
				return err
			}
			items, err := normalizeCollection(collectionValue)
			if err != nil {
				return err
			}
			itemAlias, _ := node.Config["itemAlias"].(string)
			concurrency := normalizeConcurrency(node.Config["concurrency"])
			st.Steps = append(st.Steps, newGraphStep(
				st.ExecutionID,
				nodeLabel(node.Config, "ForEach"),
				node.ID,
				node.Type,
				domain.ExecutionStepPassed,
				summarizeValue(collectionValue),
				fmt.Sprintf("items=%d alias=%s concurrency=%d", len(items), itemAlias, concurrency),
			))
			st.Scope.NodeOutputs[node.ID] = map[string]any{
				"items":              items,
				"count":              len(items),
				"itemAlias":          itemAlias,
				"concurrency":        concurrency,
				"__configuredOutput": node.Config["output"],
			}
			out = newGraphToken(node.ID)
			return nil
		})
		return out, err
	})
}

// foreachControllerFor returns the controlling ForEach node ID for a body node
// (empty when not under ForEach). ForEach itself is never loop-controlled
// (mirrors plan_runner: controllerID != "" && node.Type != "ForEach").
func foreachControllerFor(deps nodeDeps, node workflowtranslator.GraphNode) string {
	if node.Type == "ForEach" {
		return ""
	}
	if deps.foreachControllers == nil {
		return ""
	}
	return deps.foreachControllers[node.ID]
}

// runLoopControlledNode mirrors plan_runner.executeLoopControlledNode:
// sequential iteration over controller items, scoped ForeachItem/Alias, and
// aggregated {items, count} nodeOutputs. Per-item body steps are not emitted
// (wrapper also discards iteration steps); one loop-completed step is recorded.
func runLoopControlledNode(
	ctx context.Context,
	node workflowtranslator.GraphNode,
	controllerID string,
	deps nodeDeps,
	body loopBodyFn,
) (GraphToken, error) {
	var out GraphToken
	err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
		controllerOutput := st.Scope.NodeOutputs[controllerID]
		if controllerOutput == nil {
			return fmt.Errorf("foreach controller %s has no nodeOutputs", controllerID)
		}
		items, err := normalizeCollection(controllerOutput["items"])
		if err != nil {
			return err
		}
		alias := foreachAliasForController(controllerID, st.Scope.NodeOutputs)
		results := make([]any, 0, len(items))
		for index, item := range items {
			iterationScope := GraphScope{
				Input:        cloneAnyMap(st.Scope.Input),
				WorkflowVars: cloneAnyMap(st.Scope.WorkflowVars),
				NodeOutputs:  loopScopeNodeOutputs(st, deps.foreachControllers, controllerID, index),
				ForeachItem:  item,
				ForeachAlias: alias,
			}
			itemOut, err := body(st, iterationScope)
			if err != nil {
				return err
			}
			results = append(results, itemOut)
		}

		loopOutput := map[string]any{
			"items": results,
			"count": len(results),
		}
		st.Steps = append(st.Steps, newGraphStep(
			st.ExecutionID,
			nodeLabel(node.Config, node.Type),
			node.ID,
			node.Type,
			domain.ExecutionStepPassed,
			fmt.Sprintf("foreach=%s items=%d", controllerID, len(items)),
			fmt.Sprintf("loop completed items=%d", len(items)),
		))
		st.Scope.NodeOutputs[node.ID] = loopOutput

		if configuredOutput, ok := foreachConfiguredOutput(controllerID, st.Scope.NodeOutputs); ok && configuredOutput != nil {
			resolvedOutput, err := resolveValue(configuredOutput, GraphScope{
				Input:        cloneAnyMap(st.Scope.Input),
				WorkflowVars: cloneAnyMap(st.Scope.WorkflowVars),
				NodeOutputs:  cloneNodeOutputsMap(st.Scope.NodeOutputs),
			})
			if err != nil {
				return err
			}
			controllerOut := cloneAnyMap(st.Scope.NodeOutputs[controllerID])
			if controllerOut == nil {
				controllerOut = map[string]any{}
			}
			delete(controllerOut, "__configuredOutput")
			if outputMap, ok := resolvedOutput.(map[string]any); ok {
				for key, value := range outputMap {
					controllerOut[key] = value
				}
			} else {
				controllerOut["result"] = resolvedOutput
			}
			st.Scope.NodeOutputs[controllerID] = controllerOut
		}

		out = newGraphToken(node.ID)
		return nil
	})
	return out, err
}

func normalizeCollection(value any) ([]any, error) {
	switch typed := value.(type) {
	case []any:
		return typed, nil
	case nil:
		return []any{}, nil
	}
	collection := reflect.ValueOf(value)
	if collection.Kind() != reflect.Slice && collection.Kind() != reflect.Array {
		return nil, fmt.Errorf("foreach collection must resolve to an array")
	}
	items := make([]any, 0, collection.Len())
	for index := 0; index < collection.Len(); index++ {
		items = append(items, collection.Index(index).Interface())
	}
	return items, nil
}

func normalizeConcurrency(value any) int {
	switch typed := value.(type) {
	case int:
		if typed > 0 {
			return typed
		}
	case float64:
		if typed > 0 {
			return int(typed)
		}
	}
	return 1
}

func foreachAliasForController(controllerID string, outputs map[string]map[string]any) string {
	if controllerOutput, ok := outputs[controllerID]; ok {
		if alias, ok := controllerOutput["itemAlias"].(string); ok {
			return alias
		}
	}
	return ""
}

func foreachConfiguredOutput(controllerID string, outputs map[string]map[string]any) (any, bool) {
	controllerOutput, ok := outputs[controllerID]
	if !ok {
		return nil, false
	}
	output, ok := controllerOutput["__configuredOutput"]
	return output, ok
}

func cloneNodeOutputsMap(outputs map[string]map[string]any) map[string]map[string]any {
	cloned := make(map[string]map[string]any, len(outputs))
	for nodeID, output := range outputs {
		cloned[nodeID] = cloneAnyMap(output)
	}
	return cloned
}

// loopScopeNodeOutputs projects parent NodeOutputs for one ForEach iteration:
// non-loop nodes keep full output; sibling loop-controlled nodes expose the
// per-item entry at index when their output is {items:[...]}.
func loopScopeNodeOutputs(
	st *GraphState,
	controllers map[string]string,
	controllerID string,
	index int,
) map[string]map[string]any {
	outputs := make(map[string]map[string]any, len(st.Scope.NodeOutputs))
	for nodeID, output := range st.Scope.NodeOutputs {
		nodeControllerID := ""
		if controllers != nil {
			nodeControllerID = controllers[nodeID]
		}
		if nodeID == controllerID || nodeControllerID == "" || nodeControllerID != controllerID {
			outputs[nodeID] = output
			continue
		}
		itemOutput, ok := loopItemOutput(output, index)
		if !ok {
			outputs[nodeID] = output
			continue
		}
		outputs[nodeID] = itemOutput
	}
	return outputs
}

func loopItemOutput(output map[string]any, index int) (map[string]any, bool) {
	rawItems, ok := output["items"]
	if !ok {
		return nil, false
	}
	items, err := normalizeCollection(rawItems)
	if err != nil || index >= len(items) {
		return nil, false
	}
	itemOutput, ok := items[index].(map[string]any)
	if !ok {
		return nil, false
	}
	return itemOutput, true
}

// buildForEachControllersFromIR mirrors plan_runner.buildForEachControllers
// using GraphIR nodes (same dependency / type rules).
func buildForEachControllersFromIR(ir workflowtranslator.GraphIR) map[string]string {
	nodeTypes := make(map[string]string, len(ir.Nodes))
	for _, node := range ir.Nodes {
		nodeTypes[node.ID] = node.Type
	}
	controllers := map[string]string{}
	for _, node := range ir.Nodes {
		controllers[node.ID] = foreachControllerForNode(node, controllers, nodeTypes)
	}
	return controllers
}

func foreachControllerForNode(
	node workflowtranslator.GraphNode,
	controllers map[string]string,
	nodeTypes map[string]string,
) string {
	if node.Type == "End" || node.Type == "Approval" {
		return ""
	}
	controllerID := ""
	hasDirectControllerDependency := false
	hasIndirectControllerDependency := false
	for _, dep := range node.Dependencies {
		dependencyControllerID := controllers[dep]
		if nodeTypes[dep] == "ForEach" {
			dependencyControllerID = dep
			hasDirectControllerDependency = true
		} else if dependencyControllerID != "" {
			hasIndirectControllerDependency = true
		}
		if dependencyControllerID == "" {
			return ""
		}
		if controllerID == "" {
			controllerID = dependencyControllerID
			continue
		}
		if controllerID != dependencyControllerID {
			return ""
		}
	}
	if hasDirectControllerDependency && hasIndirectControllerDependency {
		return ""
	}
	return controllerID
}

// buildSubWorkflowLambda is the eino_core nested workflow node (PR13c).
//
// Aligns with plan_runner.executeSubWorkflowNode for non-approval nested runs:
// resolve published revision → recursive CoreGraphRunner.Invoke → merge child
// steps under parent execution IDs and write nodeOutputs
// {workflowId, revisionId, input, status, output}.
//
// Nested Approval (or other StatefulInterrupt) is funneled with
// compose.CompositeInterrupt so root-cause interrupt IDs bubble to the parent
// Invoke result (strategy C nested resume surface).
//
// Resume follows compose composite-conduit rules: isResumeTarget may be true
// because a descendant (child Approval) is targeted while hasData is false on
// the SubWorkflow node itself. Decision is taken from GetResumeContext when
// present, else from PendingApprovalDecision (set by CoreGraphRunner.ResumeApproval).
func buildSubWorkflowLambda(node workflowtranslator.GraphNode, deps nodeDeps) *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, _ GraphToken) (GraphToken, error) {
		wasInterrupted, hasState, prev := compose.GetInterruptState[SubWorkflowInterruptState](ctx)
		if wasInterrupted {
			if !hasState {
				return nil, fmt.Errorf("eino subworkflow interrupt missing state for node %s", node.ID)
			}
			isTarget, hasData, decision := compose.GetResumeContext[ApprovalDecision](ctx)
			if !isTarget {
				// Sibling resume target — re-interrupt to preserve nested state.
				return nil, compose.StatefulInterrupt(ctx, subWorkflowInterruptInfo, prev)
			}
			if !hasData {
				// Descendant is the resume target (typical nested Approval path).
				pending, ok := PendingApprovalDecision(ctx)
				if !ok || strings.TrimSpace(pending.Decision) == "" {
					return nil, compose.StatefulInterrupt(ctx, subWorkflowInterruptInfo, prev)
				}
				decision = pending
			}
			return resumeNestedSubWorkflow(ctx, node, deps, prev, decision)
		}
		return runNestedSubWorkflow(ctx, node, deps)
	})
}

type subWorkflowParentIdentity struct {
	executionID         string
	traceID             string
	workspaceID         string
	workflowID          string
	userID              string
	actorType           string
	agentRunID          string
	workflowExecutionID string
	input               map[string]any
}

func readSubWorkflowParent(ctx context.Context, node workflowtranslator.GraphNode) (subWorkflowParentIdentity, error) {
	var id subWorkflowParentIdentity
	err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
		input, err := resolveOptionalNodeInput(node.Config, st.Scope)
		if err != nil {
			return err
		}
		if input == nil {
			input = map[string]any{}
		}
		id = subWorkflowParentIdentity{
			executionID:         st.ExecutionID,
			traceID:             st.TraceID,
			workspaceID:         st.WorkspaceID,
			workflowID:          st.WorkflowID,
			userID:              st.UserID,
			actorType:           st.ActorType,
			agentRunID:          st.AgentRunID,
			workflowExecutionID: defaultString(st.WorkflowExecutionID, st.ExecutionID),
			input:               input,
		}
		return nil
	})
	return id, err
}

func runNestedSubWorkflow(ctx context.Context, node workflowtranslator.GraphNode, deps nodeDeps) (GraphToken, error) {
	workflowID, _ := node.Config["workflowId"].(string)
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		return nil, fmt.Errorf("subworkflow node %s missing workflowId", node.ID)
	}
	if deps.revisionResolver == nil {
		return nil, fmt.Errorf("subworkflow runtime resolver is not configured")
	}

	parent, err := readSubWorkflowParent(ctx, node)
	if err != nil {
		return nil, err
	}

	revision, err := deps.revisionResolver.ResolvePublishedRevision(workflowID)
	if err != nil {
		return nil, err
	}

	childRunner := deps.nestedGraphRunner()
	childExecID := fmt.Sprintf("%s-sub-%s", parent.executionID, node.ID)
	childOut, err := childRunner.Invoke(ctx, WorkflowRunRequest{
		Plan:                revision.Plan,
		Input:               cloneAnyMap(parent.input),
		UserID:              parent.userID,
		WorkspaceID:         parent.workspaceID,
		WorkflowVersion:     revision.RevisionID,
		Trigger:             "Workflow SubWorkflow Run",
		ActorType:           parent.actorType,
		AgentRunID:          parent.agentRunID,
		WorkflowExecutionID: childExecID,
		RevisionID:          revision.RevisionID,
	})
	if err != nil && !childOut.Interrupted {
		return nil, err
	}
	if childOut.Interrupted {
		return bubbleSubWorkflowInterrupt(ctx, node, workflowID, revision.RevisionID, parent, childOut)
	}
	return applySubWorkflowSuccess(ctx, node, workflowID, revision.RevisionID, parent, childOut)
}

func resumeNestedSubWorkflow(
	ctx context.Context,
	node workflowtranslator.GraphNode,
	deps nodeDeps,
	prev SubWorkflowInterruptState,
	decision ApprovalDecision,
) (GraphToken, error) {
	if deps.revisionResolver == nil {
		return nil, fmt.Errorf("subworkflow runtime resolver is not configured")
	}
	parent, err := readSubWorkflowParent(ctx, node)
	if err != nil {
		return nil, err
	}
	workflowID := defaultString(prev.ChildWorkflowID, strings.TrimSpace(fmt.Sprint(node.Config["workflowId"])))
	revision, err := deps.revisionResolver.ResolvePublishedRevision(workflowID)
	if err != nil {
		return nil, err
	}
	revisionID := defaultString(prev.ChildRevisionID, revision.RevisionID)

	childRunner := deps.nestedGraphRunner()
	interruptIDs := append([]string(nil), prev.ChildInterruptIDs...)
	if len(interruptIDs) == 0 {
		return nil, fmt.Errorf("subworkflow resume missing child interrupt ids for node %s", node.ID)
	}
	childOut, err := childRunner.ResumeApproval(
		ctx,
		WorkflowRunRequest{
			Plan:                revision.Plan,
			Input:               cloneAnyMap(parent.input),
			UserID:              parent.userID,
			WorkspaceID:         parent.workspaceID,
			WorkflowVersion:     revisionID,
			Trigger:             "Workflow SubWorkflow Run",
			ActorType:           parent.actorType,
			AgentRunID:          parent.agentRunID,
			WorkflowExecutionID: defaultString(prev.ChildExecutionID, fmt.Sprintf("%s-sub-%s", parent.executionID, node.ID)),
			RevisionID:          revisionID,
			CheckPointID:        prev.ChildCheckPointID,
		},
		prev.ChildCheckPointID,
		decision,
		interruptIDs...,
	)
	if err != nil && !childOut.Interrupted {
		return nil, err
	}
	if childOut.Interrupted {
		return bubbleSubWorkflowInterrupt(ctx, node, workflowID, revisionID, parent, childOut)
	}
	return applySubWorkflowSuccess(ctx, node, workflowID, revisionID, parent, childOut)
}

func bubbleSubWorkflowInterrupt(
	ctx context.Context,
	node workflowtranslator.GraphNode,
	workflowID string,
	revisionID string,
	parent subWorkflowParentIdentity,
	childOut WorkflowRunResult,
) (GraphToken, error) {
	if childOut.InterruptErr == nil {
		return nil, fmt.Errorf("subworkflow node %s interrupted without raw interrupt error", node.ID)
	}

	interruptState := SubWorkflowInterruptState{
		SchemaVersion:     SubWorkflowInterruptSchemaVersion,
		NodeID:            node.ID,
		ChildWorkflowID:   workflowID,
		ChildRevisionID:   revisionID,
		ChildExecutionID:  childOut.Execution.ID,
		ChildCheckPointID: childOut.CheckPointID,
		ChildInterruptIDs: append([]string(nil), childOut.InterruptIDs...),
	}
	if childOut.Approval != nil {
		interruptState.ChildApprovalNodeID = childOut.Approval.NodeID
		interruptState.ChildApprovalReason = childOut.Approval.Reason
	} else if childOut.State != nil {
		interruptState.ChildApprovalNodeID = childOut.State.PendingApprovalNodeID
		interruptState.ChildApprovalReason = childOut.State.PendingApprovalReason
	}

	err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
		mergeChildStepsIntoParent(st, node.ID, childOut.Execution.Steps)
		st.Status = domain.ExecutionApproval
		st.ReachedTerminal = true
		st.OutputSummary = "Workflow trial run is blocked by Approval node"
		if interruptState.ChildApprovalNodeID != "" {
			// Surface nested approval on parent for projection helpers; root-cause
			// ApprovalInterruptState still carries the child's node id for resume.
			st.PendingApprovalNodeID = interruptState.ChildApprovalNodeID
			st.PendingApprovalReason = interruptState.ChildApprovalReason
		}
		st.Scope.NodeOutputs[node.ID] = map[string]any{
			"workflowId":  workflowID,
			"revisionId":  revisionID,
			"input":       cloneAnyMap(parent.input),
			"status":      string(domain.ExecutionApproval),
			"interrupted": true,
			"output":      childExecutionOutputMap(childOut.Execution),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return nil, compose.CompositeInterrupt(ctx, subWorkflowInterruptInfo, interruptState, childOut.InterruptErr)
}

func applySubWorkflowSuccess(
	ctx context.Context,
	node workflowtranslator.GraphNode,
	workflowID string,
	revisionID string,
	parent subWorkflowParentIdentity,
	childOut WorkflowRunResult,
) (GraphToken, error) {
	var out GraphToken
	err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
		mergeChildStepsIntoParent(st, node.ID, childOut.Execution.Steps)
		st.Steps = append(st.Steps, newGraphStep(
			st.ExecutionID,
			nodeLabel(node.Config, "SubWorkflow"),
			node.ID,
			node.Type,
			domain.ExecutionStepPassed,
			workflowID,
			fmt.Sprintf("revision=%s status=%s", revisionID, childOut.Execution.Status),
		))
		st.Scope.NodeOutputs[node.ID] = map[string]any{
			"workflowId": workflowID,
			"revisionId": revisionID,
			"input":      cloneAnyMap(parent.input),
			"status":     string(childOut.Execution.Status),
			"output":     childExecutionOutputMap(childOut.Execution),
		}
		// Nested Approval pause left parent Status=Approval + ReachedTerminal; clear so
		// End can finish as Success after resume (mirrors buildApprovalLambda confirm).
		if st.Status == domain.ExecutionApproval || st.PendingApprovalNodeID != "" {
			st.Status = domain.ExecutionSuccess
			st.ErrorMessage = ""
			st.PendingApprovalNodeID = ""
			st.PendingApprovalReason = ""
			st.ReachedTerminal = false
			if st.OutputSummary == "Workflow trial run is blocked by Approval node" {
				st.OutputSummary = ""
			}
		}
		out = newGraphToken(node.ID)
		return nil
	})
	return out, err
}

func mergeChildStepsIntoParent(st *GraphState, parentNodeID string, childSteps []domain.ExecutionStepRecord) {
	for _, step := range childSteps {
		// Skip child system preamble that would double-count Auth/Workspace when
		// the parent already emitted them; keep all node-scoped + result steps.
		if step.NodeID == "" && (step.Name == "Auth Check" || step.Name == "Workspace Load" || step.Name == "Workflow Decision") {
			continue
		}
		step.ExecutionID = st.ExecutionID
		if step.NodeID != "" {
			step.ID = fmt.Sprintf("step-%s-subworkflow-%s-%s", st.ExecutionID, parentNodeID, step.NodeID)
		} else {
			step.ID = fmt.Sprintf("step-%s-subworkflow-%s-%s", st.ExecutionID, parentNodeID, step.ID)
		}
		st.Steps = append(st.Steps, step)
	}
}

func childExecutionOutputMap(execution domain.Execution) map[string]any {
	if strings.TrimSpace(execution.OutputSummary) == "" {
		return nil
	}
	return map[string]any{
		"summary": execution.OutputSummary,
	}
}

func buildEndLambda(node workflowtranslator.GraphNode) *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, _ GraphToken) (GraphResult, error) {
		var result GraphResult
		err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
			output, err := resolveValue(node.Config["output"], st.Scope)
			if err != nil {
				return err
			}
			st.Steps = append(st.Steps, newGraphStep(
				st.ExecutionID,
				nodeLabel(node.Config, "End"),
				node.ID,
				node.Type,
				domain.ExecutionStepPassed,
				summarizeValue(node.Config["output"]),
				summarizeValue(output),
			))
			st.Scope.NodeOutputs[node.ID] = map[string]any{"result": output}
			st.ReachedTerminal = true
			st.OutputSummary = summarizeValue(output)
			if st.Status == domain.ExecutionSuccess {
				st.Steps = append(st.Steps, newGraphStep(
					st.ExecutionID,
					"Result Return",
					"",
					"",
					domain.ExecutionStepPassed,
					st.WorkflowID,
					defaultString(st.OutputSummary, "workflow trial completed"),
				))
				if st.OutputSummary == "" {
					st.OutputSummary = "Workflow trial run completed"
				}
			}
			result = GraphResult{Execution: st.toExecution()}
			return nil
		})
		return result, err
	})
}

// configStringSlice normalizes Parallel branches (and similar) config values
// from []string or []any, matching workflowruntime.toStringSlice.
func configStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			items = append(items, fmt.Sprint(item))
		}
		return items
	default:
		return nil
	}
}

// branchCondition builds a GraphBranch condition that maps the selected branch
// label in GraphState to a target node ID.
func branchCondition(
	conditionNodeID string,
	targets []workflowtranslator.BranchTarget,
) compose.GraphBranchCondition[GraphToken] {
	labelToTarget := make(map[string]string, len(targets))
	for _, t := range targets {
		labelToTarget[t.Branch] = t.TargetNode
	}
	return func(ctx context.Context, _ GraphToken) (string, error) {
		var target string
		err := compose.ProcessState[*GraphState](ctx, func(_ context.Context, st *GraphState) error {
			label := st.SelectedBranches[conditionNodeID]
			if label == "" {
				// Fall back to condition node output.
				if out, ok := st.Scope.NodeOutputs[conditionNodeID]; ok {
					label, _ = out["branch"].(string)
				}
			}
			if label == "" {
				return fmt.Errorf("condition node %s has no selected branch", conditionNodeID)
			}
			next, ok := labelToTarget[label]
			if !ok {
				return fmt.Errorf("condition node %s selected branch %q has no target", conditionNodeID, label)
			}
			target = next
			return nil
		})
		return target, err
	}
}
