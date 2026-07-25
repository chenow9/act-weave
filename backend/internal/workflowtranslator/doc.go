// Package workflowtranslator is a pure CompiledExecutionPlan → intermediate
// Graph IR translator for the Eino workflow runtime path (design §4).
//
// Responsibilities (PR10 + PR13a–d coverage updates):
//   - Coverage matrix: classify plan node types as native vs unsupported
//     for engine modes eino_core and eino (Approval + Parallel + HTTP +
//     SubWorkflow + ForEach are native under eino_core after PR13d)
//   - Graph IR: project plan nodes / dependencies / IncomingBranch into a
//     structure suitable for later compose graph build (PR11)
//   - Cache key: stable key from workspace / revision / planHash / engineVersion
//
// This package has no side effects: no network, no DB, no Eino compose compile.
// True compose graph build lives in einoruntime (graph_builder / graph_cache);
// production executor selection in workflowruntime factory.
package workflowtranslator
