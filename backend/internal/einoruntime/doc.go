// Package einoruntime hosts the Eino-backed execution kernel for ACTWEAVE
// (CheckPointStore, ChatModelAgent engine, tool adapters).
//
// PR2 delivers PostgresCheckPointStore + multi-tenant checkpoint ID parsing.
// PR3 adds confirmation-aligned expires_at write/renew APIs and an expired-row
// cleanup worker. PR5 delivers PipelineTool (InvokableTool) with
// interrupt-before-invoke and resume-without-reinvoke HITL ownership, plus
// gob-registered ToolConfirmInterruptState.
//
// PR6 delivers ChatModelAgent assembly + Engine:
//   - agent_builder: MaxIterations=8, ExecuteSequentially, budget middleware
//   - budget: tool invocation hard-cap 16 → TOOL_BUDGET_EXCEEDED
//   - engine: once-per-run checkpoint ID, Runner EnableStreaming, interrupt
//     InterruptContext ID capture
//   - callbacks_protocol: ProtocolProjector text delta hooks (item.delta)
//
// PR7 adds Engine.Resume (ResumeWithParams) and PipelineTool
// ConfirmInterruptHook so chatruntimebridge can embed einoChatResume and
// continue without a second InvokeResolved.
//
// PR8 adds offline golden + DryRun CI fixtures (Appendix A.1–A.2, §7.2):
//   - dryrun_invoker: DryRunToolInvoker records tool name/args without
//     entering InvocationPipeline (no network, no DB writes)
//   - golden_text_tool_test: true multi-delta text + tool success + DryRun
//     fixture compare (picked up by standard go test)
//
// PR9 A.4 tool approval_resume golden lives in chatruntimebridge
// (golden_approval_resume_test): interrupt → platform Dispatch once →
// ResumeWithParams Targets keys = interruptIds, tool adapter Invoke=0.
//
// PR11 delivers eino_core true node graph + Approval StatefulInterrupt:
//   - graph_builder / graph_nodes / graph_cache: GraphIR → compose Graph
//     (Start/Tool/Condition/Transform/Approval/End); cache by CacheKey
//   - Approval node: compose.StatefulInterrupt + compose resume (no whole-plan
//     re-run); ApprovalInterruptState gob-registered (IDs only)
//   - CoreGraphRunner.Invoke / ResumeApproval; workflowruntime EinoCoreRunner
//     replaces fake NativeGraphRunner single-Lambda semantics
//   - Production default remains wrapper (factory scaffolding only)
//
// PR13a adds Parallel as eino_core-native fan-out/join barrier:
//   - Parallel lambda records branches/branchCount (plan_runner-aligned outputs)
//   - Branch successors are ordinary DAG edges; AllPredecessor joins at End
//
// PR13b adds HTTP as eino_core-native simulation lambda:
//   - Mirrors plan_runner.executeHTTPNode (method/endpoint/request/status=ok)
//   - No real network egress; Passed step + nodeOutputs for End/ref paths
//
// PR13c adds SubWorkflow as eino_core-native nested graph:
//   - Resolves published child plan via WorkflowRevisionResolver
//   - Recursive CoreGraphRunner.Invoke; nodeOutputs align with plan_runner
//   - Nested Approval bubbles via compose.CompositeInterrupt +
//     SubWorkflowInterruptState (child checkpoint + interrupt IDs)
//
// PR13d adds ForEach as eino_core-native scoped iteration:
//   - ForEach lambda seeds items/count/itemAlias (plan_runner-aligned)
//   - Body successors loop sequentially with GraphScope.ForeachItem/Alias
//   - Aggregated {items, count} outputs + optional controller output mapping
//
// Checkpoint ID format (locked):
//
//	ws/{workspaceID}/agent_run/{runID}/{nonce}
//	ws/{workspaceID}/workflow_exec/{executionID}/{nonce}
//
// D15 — Checkpoint TTL = confirmation expiry clock:
//
//   - expires_at on every row is required.
//   - HITL pause writers MUST call SetWithExpiresAt / TouchExpiresAt with the
//     same absolute time as confirmation.ExpiresAt (no separate
//     checkpointTTLHours knob).
//   - Set falls back to DefaultCheckpointTTL (600s), equal to
//     execution.DefaultConfirmationTTLSeconds, for rare mid-crash rows.
//   - CheckpointCleanupWorker deletes expires_at <= now() so expired
//     confirmations cannot resume against a still-resident checkpoint blob.
//
// D5 / §3.6.3 — Tool HITL Invoke ownership:
//
//   - First run, no confirm → PipelineTool.InvokeResolved once
//   - First run, needs confirm → StatefulInterrupt only (0 Invoke)
//   - Approve/Dispatch → ToolConfirmationResumeExecutor only (1 Invoke)
//   - Eino resume → return GetResumeContext data only (0 Invoke)
package einoruntime
