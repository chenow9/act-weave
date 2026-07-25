// Package chatruntimebridge implements agentrun.Runtime on top of einoruntime.
//
// Production Enqueue / Continue always use this package via agentrun.Factory.
// Nested einoChatResume is required to resume; chatLoop-only snapshots are
// rejected by application ContinueDispatcher.
//
// Highlights:
//   - Outer confirmation snapshot stays tool-resume-request.v1
//   - Nested einoChatResume carries interruptIds + stable einoCheckpointId
//   - Continue uses adk.Runner.ResumeWithParams; tool InvokeResolved stays 0
//     after platform Dispatch already invoked once
//   - True Stream deltas project via einoruntime.ProtocolProjector
//   - MODEL agent_run_steps + optional reasoning audit when AgentAuditDebug
//   - TOOL agent_run_steps (args + result) via PipelineTool OnToolComplete
//
// Golden fixtures (offline, no DB):
//   - golden_stream_delta_test: A.1/A.2 true Stream item.delta + tool success
//   - golden_approval_resume_test: A.4 tool HITL ownership + resume contract
//
// Rollback = previous binary / drain traffic (see runbook).
package chatruntimebridge
