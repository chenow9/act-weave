// Package chatruntime provides chat AgentRun support surfaces used by the
// production eino path (chatruntimebridge) and HTTP composition:
//
//   - Messenger: SendMessage → agentrun.Runtime.Enqueue
//   - Protocol projection helpers (native recorder, text stream sink, auxiliary)
//   - Capability snapshot parsing for run-pinned tool releases
//   - Shared contracts (StepStore, ModelTurnRecorder, ProtocolRecord, …)
//
// Agent orchestration (model turns, tools, confirmation pause/resume) is
// implemented by chatruntimebridge + einoruntime, not in this package.
package chatruntime
