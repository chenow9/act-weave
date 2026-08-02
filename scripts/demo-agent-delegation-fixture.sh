#!/usr/bin/env bash
# Real Bridge+Postgres A→B fixture (not hand-inserted steps).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/backend"

echo "==> gofmt (delegation / A2A feature surface only)"
# Only packages and files owned by AgentTool/A2A/audit-delegation work.
# Do not fail on pre-existing baseline alignment in unrelated shared files.
gofmt -l \
  ./internal/agentdelegation \
  ./internal/a2agateway \
  ./internal/chatruntimebridge/bridge.go \
  ./internal/chatruntimebridge/a2a_inbound.go \
  ./internal/chatruntimebridge/child_run.go \
  ./internal/chatruntimebridge/delegation.go \
  ./internal/chatruntimebridge/nested_audit_model.go \
  ./internal/chatruntimebridge/child_finish_terminal_test.go \
  ./internal/chatruntimebridge/child_run_freeze_test.go \
  ./internal/chatruntimebridge/bridge_delegation_db_test.go \
  ./internal/chatruntimebridge/nested_audit_model_test.go \
  ./internal/chatruntimebridge/parent_step_test.go \
  ./internal/chatruntimebridge/merge_stream_residual_test.go \
  ./internal/chatruntimebridge/namespace_start_conflict_test.go \
  ./internal/agentaudit/models.go \
  ./internal/agentaudit/service.go \
  ./internal/agentaudit/timeline.go \
  ./internal/agentaudit/depth_json_test.go \
  ./internal/agentaudit/timeline_abc_task_test.go \
  ./internal/agentaudit/timeline_delegation_db_test.go \
  ./internal/agentaudit/timeline_delegation_test.go \
  ./internal/agentaudit/title_path_test.go \
  ./internal/execution/run_models.go \
  ./internal/execution/run_repository.go \
  ./internal/einoruntime/callbacks_protocol.go \
  ./internal/einoruntime/tool_only_model_turn_test.go \
  ./internal/transport/http/agent_delegation.go \
  ./internal/transport/http/agent_delegation_residual_test.go \
  | tee /tmp/gofmt-del.txt
if [[ -s /tmp/gofmt-del.txt ]]; then
  echo "gofmt needed" >&2
  cat /tmp/gofmt-del.txt >&2
  exit 1
fi

echo "==> Bridge+Postgres Eino A→B TASK+INLINE (real MODEL+TOOL write path)"
go test ./internal/chatruntimebridge/ -count=1 -timeout 180s -v \
  -run 'TestBridge_EinoAB_TASK_PersistsChildModelAndTool|TestBridge_EinoAB_INLINE_PersistsNestedModelAndToolOnParentRun'

echo "==> nestedAuditModel fail-closed + TASK parent_step guard"
go test ./internal/chatruntimebridge/ -count=1 -timeout 60s -v \
  -run 'TestNestedAuditModel_|TestSameRunParentStep|TestAuditedAgentTool_TASK'

echo "==> Timeline JOIN + nested MODEL assert (no pseudo skip)"
go test ./internal/agentaudit/ -count=1 -timeout 120s -v \
  -run 'TestService_TimelineJoinsDelegationTable'

echo "==> Finalize outbox restart recovery + inbound lease + heartbeat + cancel"
go test ./internal/a2agateway/ -count=1 -timeout 180s -v \
  -run 'TestFinalizeWorker_RestartRecovery|TestInbound_ConcurrentExecutionLease|TestInboundLease_Heartbeat|TestCancelInbound_Propagates|TestNewOutboundTool_RejectsDefaultTransport|TestSecureHTTPClient|TestAuthPinned|TestOutbound_Attribution|TestOutbound_AgentCard'

echo "==> tool-only MODEL turn + usage tokens"
go test ./internal/einoruntime/ -count=1 -timeout 60s -v \
  -run 'TestNotifyModelTurn_'

echo "==> A→B→C TASK parent_delegation + namespace"
go test ./internal/agentdelegation/ -count=1 -timeout 120s -v \
  -run 'TestABC_TASK_|TestCallableNamespace_|TestAuditedAgentTool_Cancel|TestAB_TextAndToolDelegation'

echo "==> graph freeze remotes + namespace start path"
go test ./internal/chatruntimebridge/ -count=1 -timeout 60s -v \
  -run 'TestAssertCombinedCallableNamespace|TestMergeStreamMessages|TestNestedAudit'

echo "==> Memory Eino A→B path (AgentTool synthesis)"
go test ./internal/agentdelegation/ -count=1 -timeout 120s -v \
  -run 'TestAB_TextAndToolDelegation_AgentToolPath'

echo "==> Fixture OK — A→B→B MODEL+TOOL + tokens/lease/cancel/freeze paths via Bridge+DB"
