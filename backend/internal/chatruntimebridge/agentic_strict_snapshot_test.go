package chatruntimebridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
)

// TestAgenticInitial_StrictRawSnapshotMatrix drives real Bridge.Execute(targets=nil)
// against raw JSON mutations that must not be healed by TrimSpace/last-key-wins.
func TestAgenticInitial_StrictRawSnapshotMatrix(t *testing.T) {
	base := newAgenticFixture(t, nil)
	goodModel := string(base.run.ModelSnapshot)
	goodGraph := string(base.run.AgentGraphSnapshot)

	type row struct {
		name       string
		mutate     func(*agenticFixture)
		wantSubstr string
	}
	rows := []row{
		// --- model snapshot ---
		{
			name: "model_id_surrounding_whitespace",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = json.RawMessage(strings.Replace(
					goodModel,
					`"id":"`+testModelUUID+`"`,
					`"id":" `+testModelUUID+` "`,
					1,
				))
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "model_id_uppercase_uuid",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = json.RawMessage(strings.Replace(
					goodModel,
					testModelUUID,
					strings.ToUpper(testModelUUID),
					1,
				))
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "model_duplicate_root_id",
			mutate: func(f *agenticFixture) {
				// last-key-wins would otherwise keep a valid id
				f.run.ModelSnapshot = json.RawMessage(
					`{"id":"00000000-0000-4000-8000-000000000099","id":"` + testModelUUID + `",` +
						`"provider":"openai","apiBase":"https://api.example.com/v1","modelName":"gpt-test",` +
						`"status":"VERIFIED","lockVersion":1,"options":{},"runtimeCapabilities":{},` +
						`"agenticCapabilities":` + string(base.cfg.AgenticCapabilities) + `}`,
				)
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "model_missing_options",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = json.RawMessage(removeModelKey(t, goodModel, "options"))
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "model_missing_runtimeCapabilities",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = json.RawMessage(removeModelKey(t, goodModel, "runtimeCapabilities"))
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "model_null_options",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = json.RawMessage(setModelKey(t, goodModel, "options", `null`))
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "model_duplicate_capability_protocol",
			mutate: func(f *agenticFixture) {
				// Nested duplicate protocol inside agenticCapabilities
				caps := string(base.cfg.AgenticCapabilities)
				// inject second protocol key after first
				caps = strings.Replace(caps, `"protocol":"openai-responses-v1"`,
					`"protocol":"forged","protocol":"openai-responses-v1"`, 1)
				f.run.ModelSnapshot = replaceAgenticCaps(t, goodModel, caps)
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "model_missing_lockVersion",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = json.RawMessage(removeModelKey(t, goodModel, "lockVersion"))
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "model_null_agenticCapabilities",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = json.RawMessage(setModelKey(t, goodModel, "agenticCapabilities", `null`))
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "model_trailing_json",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = json.RawMessage(goodModel + `{"extra":true}`)
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "model_unknown_field",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = json.RawMessage(setModelKey(t, goodModel, "evilField", `1`))
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "model_outer_whitespace",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = json.RawMessage(" " + goodModel)
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		// --- graph snapshot ---
		{
			name: "graph_edges_null",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(setGraphKey(t, goodGraph, "edges", `null`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "graph_edges_missing",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(removeGraphKey(t, goodGraph, "edges"))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "graph_maxDepth_missing",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(removeGraphKey(t, goodGraph, "maxDepth"))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "graph_maxTotalDelegations_missing",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(removeGraphKey(t, goodGraph, "maxTotalDelegations"))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "graph_maxPerBinding_missing",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(removeGraphKey(t, goodGraph, "maxPerBinding"))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "graph_builtAt_missing",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(removeGraphKey(t, goodGraph, "builtAt"))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "graph_node_unknown_field",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectGraphNodeField(t, goodGraph, "forgedNodeField", `1`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "graph_foreign_remote_caller",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectGraphForeignRemote(t, goodGraph))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "graph_remote_entry_null",
			mutate: func(f *agenticFixture) {
				s := strings.Replace(goodGraph,
					`"`+testAgentUUID+`":[]`,
					`"`+testAgentUUID+`":null`, 1)
				f.run.AgentGraphSnapshot = json.RawMessage(s)
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "graph_edge_empty_object",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(setGraphKey(t, goodGraph, "edges", `[{}]`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "graph_duplicate_schemaVersion_forged_first",
			mutate: func(f *agenticFixture) {
				s := strings.Replace(goodGraph, `"schemaVersion":"agent_graph_snapshot.v1"`,
					`"schemaVersion":"forged.v1","schemaVersion":"agent_graph_snapshot.v1"`, 1)
				f.run.AgentGraphSnapshot = json.RawMessage(s)
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "graph_duplicate_schemaVersion_legit_first",
			mutate: func(f *agenticFixture) {
				s := strings.Replace(goodGraph, `"schemaVersion":"agent_graph_snapshot.v1"`,
					`"schemaVersion":"agent_graph_snapshot.v1","schemaVersion":"forged.v1"`, 1)
				f.run.AgentGraphSnapshot = json.RawMessage(s)
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "graph_trailing_garbage",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(goodGraph + `0`)
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "graph_outer_whitespace",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage("\n" + goodGraph)
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		// --- cycle 6: nested node / edge / remote contracts ---
		{
			name: "node_model_forged_only",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(mutateGraphNodeNested(t, goodGraph, "modelSnapshot", `{"forged":"not-a-model"}`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "node_model_missing_id",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(mutateGraphNodeNestedKey(t, goodGraph, "modelSnapshot", "id", "", true))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "node_model_unknown_field",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(mutateGraphNodeNestedKey(t, goodGraph, "modelSnapshot", "forgedField", `1`, false))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "node_model_null_options",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(mutateGraphNodeNestedKey(t, goodGraph, "modelSnapshot", "options", `null`, false))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "node_agent_unknown_field",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(mutateGraphNodeNestedKey(t, goodGraph, "agentSnapshot", "evil", `true`, false))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "node_agent_forged_schema",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(mutateGraphNodeNestedKey(t, goodGraph, "agentSnapshot", "schemaVersion", `"forged.v9"`, false))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "node_cap_forged_schema",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(mutateGraphNodeNestedKey(t, goodGraph, "capabilitySnapshot", "schemaVersion", `"forged-cap.v9"`, false))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "node_cap_unknown_field",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(mutateGraphNodeNestedKey(t, goodGraph, "capabilitySnapshot", "marker", `"x"`, false))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "edge_protocol_forged",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectValidEdgeThenMutate(t, f, "protocol", `"FORGED"`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "edge_mode_forged",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectValidEdgeThenMutate(t, f, "mode", `"FORGED_MODE"`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "edge_context_policy_forged",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectValidEdgeThenMutate(t, f, "contextPolicy", `"NONE"`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "remote_duplicate_binding",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectDupRemote(t, goodGraph))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		// --- cycle 7: domain-semantic ---
		{
			name: "semantic_root_depth_nonzero",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(setGraphNodeDepth(t, goodGraph, 0, 1))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_empty_api_base",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(mutateGraphNodeNestedKey(t, goodGraph, "modelSnapshot", "apiBase", `""`, false))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_edge_protocol_a2a",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectValidEdgeThenMutate(t, f, "protocol", `"A2A"`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_edge_context_summary",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectValidEdgeThenMutate(t, f, "contextPolicy", `"SUMMARY"`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_remote_file_scheme",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectHostileRemote(t, goodGraph,
					`file:///etc/passwd`, []string{"1.1.1.1"}, ""))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_remote_http_scheme",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectHostileRemote(t, goodGraph,
					`http://1.1.1.1/a2a`, []string{"1.1.1.1"}, ""))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_remote_userinfo",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectHostileRemote(t, goodGraph,
					`https://user:pass@1.1.1.1/a2a`, []string{"1.1.1.1"}, ""))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_remote_empty_allowlist",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectHostileRemote(t, goodGraph,
					`https://1.1.1.1/a2a`, []string{}, ""))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_cap_forged_kind",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectGraphCapRelease(t, goodGraph, "FORGED_KIND", "LOW", "NONE",
					"c33ce000-0000-4000-8000-0000000000c1"))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_cap_noncanonical_id",
			mutate: func(f *agenticFixture) {
				// non-canonical UUID fixture: uppercase is the invariant under test.
				f.run.AgentGraphSnapshot = json.RawMessage(injectGraphCapRelease(t, goodGraph, "TOOL", "LOW", "NONE",
					"C33CE000-0000-4000-8000-0000000000C1"))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_cap_forged_risk",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectGraphCapRelease(t, goodGraph, "TOOL", "SUPER", "NONE",
					"c33ce000-0000-4000-8000-0000000000c1"))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_cap_forged_side_effect",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectGraphCapRelease(t, goodGraph, "TOOL", "LOW", "DESTROY",
					"c33ce000-0000-4000-8000-0000000000c1"))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_remote_mismatched_allowlist",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectHostileRemote(t, goodGraph,
					`https://1.1.1.1/a2a`, []string{"8.8.8.8"}, ""))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_remote_agent_card_file",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectHostileRemote(t, goodGraph,
					`https://1.1.1.1/a2a`, []string{"1.1.1.1"}, `file:///tmp/card`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_remote_bad_secret_ref",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectHostileRemoteSecret(t, goodGraph, "not-a-secret-ref"))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_edge_context_selected",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectValidEdgeThenMutate(t, f, "contextPolicy", `"SELECTED_MESSAGES"`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_self_edge",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectGraphSelfEdge(t, goodGraph))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_orphan_node",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectGraphOrphan(t, goodGraph))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_cycle",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(injectGraphCycle(t, f))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "semantic_child_depth_mismatch",
			mutate: func(f *agenticFixture) {
				// Two-node edge graph with child depth wrong.
				raw := injectValidEdgeThenMutate(t, f, "protocol", `"INTERNAL"`) // still valid edge
				// re-parse and set child depth to 0
				f.run.AgentGraphSnapshot = json.RawMessage(setGraphNodeDepth(t, raw, 1, 0))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		// --- cycle 8–9: lock-version identity (complete 3-layer matrix) ---
		// Each of node / model / agent lock × missing|null|wrong-type|zero|negative.
		{
			name: "lock_node_missing",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockNode, ""))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_node_null",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockNode, `null`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_node_wrong_type",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockNode, `"1"`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_node_zero",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockNode, `0`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_node_negative",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockNode, `-1`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_model_missing",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockModel, ""))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_model_null",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockModel, `null`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_model_wrong_type",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockModel, `"1"`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_model_zero",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockModel, `0`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_model_negative",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockModel, `-1`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_agent_missing",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockAgent, ""))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_agent_null",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockAgent, `null`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_agent_wrong_type",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockAgent, `"1"`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_agent_zero",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockAgent, `0`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_agent_negative",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeMutateOneLock(t, goodGraph, bridgeLockAgent, `-1`))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		// Exact equality coverage
		{
			name: "lock_node_model_mismatch",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeSetThreeLocks(t, goodGraph, 3, 1, 3))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_node_agent_mismatch",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeSetThreeLocks(t, goodGraph, 3, 3, 1))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_three_way_divergent_3_1_2",
			mutate: func(f *agenticFixture) {
				f.run.AgentGraphSnapshot = json.RawMessage(bridgeSetThreeLocks(t, goodGraph, 3, 1, 2))
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
		{
			name: "lock_missing_node_with_nested_present",
			mutate: func(f *agenticFixture) {
				// Reviewer fail-open case: node lock gone; model=1 agent=2.
				g := bridgeSetThreeLocks(t, goodGraph, 1, 1, 2)
				g = removeGraphNodeKey(t, g, "modelConfigLockVersion")
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantSubstr: "AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		},
	}

	for _, tc := range rows {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := newAgenticFixture(t, tc.mutate)
			// Prove mutation applied (not accidentally still good).
			if string(f.run.ModelSnapshot) == goodModel && string(f.run.AgentGraphSnapshot) == goodGraph {
				t.Fatal("mutation did not change frozen input")
			}
			err := f.bridge(t).Execute(context.Background(), f.job())
			if err == nil {
				t.Fatal("expected fail closed")
			}
			// Typed error family for graph failures — not migration-pending or later errors.
			if tc.wantSubstr == "AGENTIC_GRAPH_SNAPSHOT_REQUIRED" {
				if !errors.Is(err, chatruntimebridge.ErrAgenticGraphSnapshotRequired) {
					t.Fatalf("err=%v want errors.Is ErrAgenticGraphSnapshotRequired", err)
				}
				if strings.Contains(err.Error(), "AGENTIC_DELEGATION_MIGRATION_PENDING") {
					t.Fatalf("must not classify invalid graph as migration-pending: %v", err)
				}
			} else if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("err=%v want %q", err, tc.wantSubstr)
			}
			// Zero construction side effects: agentic/classic builders, provider model,
			// sink open, assembly/manifest insert. Fail-path protocol notices are OK.
			if f.agentic.calls.Load() != 0 || f.classic.calls.Load() != 0 ||
				f.mdl.calls.Load() != 0 || f.sinks.opens.Load() != 0 {
				t.Fatalf("side effects agentic=%d classic=%d model=%d sink=%d",
					f.agentic.calls.Load(), f.classic.calls.Load(), f.mdl.calls.Load(), f.sinks.opens.Load())
			}
			if f.assemblies.inserts.Load() != 0 {
				t.Fatalf("assembly inserts=%d", f.assemblies.inserts.Load())
			}
		})
	}
}

// TestAgenticInitial_LockIdentity_ValidThreeLayerMatch is the permanent positive
// control for three-layer lock equality on the real Bridge.Execute path.
//
// The graph lock triple alone is not sufficient: run.ModelSnapshot.lockVersion
// and run.AgentSnapshot.modelConfigLockVersion must agree with it too, so the
// positive control moves all five in lockstep. (A graph-only bump to 7 is
// exactly the cross-snapshot divergence asserted in
// TestAgenticInitial_CrossSnapshotModelIdentity_FailClosed.)
func TestAgenticInitial_LockIdentity_ValidThreeLayerMatch(t *testing.T) {
	// The default fixture already matches at the lowest lock a VERIFIED config can
	// hold (RecordVerification's CAS makes that 2); this also proves lock=7.
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.cfg.LockVersion = 7
		f.cfg.AgenticCapabilities = mustAgenticCaps(t, f.cfg)
		f.run.ModelSnapshot = marshalTestModelSnapshot(t, f.cfg)
		f.run.AgentSnapshot = testRunAgentSnapshotWithLock(7)
		f.run.AgentGraphSnapshot = json.RawMessage(bridgeSetThreeLocks(t, string(f.run.AgentGraphSnapshot), 7, 7, 7))
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("matching three-layer locks must proceed: %v", err)
	}
	if f.agentic.calls.Load() != 1 {
		t.Fatalf("agentic=%d", f.agentic.calls.Load())
	}
	if f.classic.calls.Load() != 0 {
		t.Fatal("classic must not run")
	}
}

type bridgeLockTarget int

const (
	bridgeLockNode bridgeLockTarget = iota
	bridgeLockModel
	bridgeLockAgent
)

func bridgeMutateOneLock(t *testing.T, graphJSON string, target bridgeLockTarget, val string) string {
	t.Helper()
	raw := bridgeSetThreeLocks(t, graphJSON, 1, 1, 1)
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || len(nodes) == 0 {
		t.Fatal(err)
	}
	switch target {
	case bridgeLockNode:
		if val == "" {
			delete(nodes[0], "modelConfigLockVersion")
		} else {
			nodes[0]["modelConfigLockVersion"] = json.RawMessage(val)
		}
	case bridgeLockModel:
		var ms map[string]json.RawMessage
		if err := json.Unmarshal(nodes[0]["modelSnapshot"], &ms); err != nil {
			t.Fatal(err)
		}
		if val == "" {
			delete(ms, "lockVersion")
		} else {
			ms["lockVersion"] = json.RawMessage(val)
		}
		b, _ := json.Marshal(ms)
		nodes[0]["modelSnapshot"] = b
	case bridgeLockAgent:
		var as map[string]json.RawMessage
		if err := json.Unmarshal(nodes[0]["agentSnapshot"], &as); err != nil {
			t.Fatal(err)
		}
		if val == "" {
			delete(as, "modelConfigLockVer")
		} else {
			as["modelConfigLockVer"] = json.RawMessage(val)
		}
		b, _ := json.Marshal(as)
		nodes[0]["agentSnapshot"] = b
	}
	nb, _ := json.Marshal(nodes)
	m["nodes"] = nb
	out, _ := json.Marshal(m)
	return string(out)
}

func bridgeSetThreeLocks(t *testing.T, graphJSON string, node, model, agent int64) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graphJSON), &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || len(nodes) == 0 {
		t.Fatal(err)
	}
	nRaw, _ := json.Marshal(node)
	mRaw, _ := json.Marshal(model)
	aRaw, _ := json.Marshal(agent)
	nodes[0]["modelConfigLockVersion"] = nRaw
	var ms map[string]json.RawMessage
	if err := json.Unmarshal(nodes[0]["modelSnapshot"], &ms); err != nil {
		t.Fatal(err)
	}
	ms["lockVersion"] = mRaw
	msb, _ := json.Marshal(ms)
	nodes[0]["modelSnapshot"] = msb
	var as map[string]json.RawMessage
	if err := json.Unmarshal(nodes[0]["agentSnapshot"], &as); err != nil {
		t.Fatal(err)
	}
	as["modelConfigLockVer"] = aRaw
	asb, _ := json.Marshal(as)
	nodes[0]["agentSnapshot"] = asb
	nb, _ := json.Marshal(nodes)
	m["nodes"] = nb
	out, _ := json.Marshal(m)
	return string(out)
}

// TestAgenticInitial_StrictValidEmptyGraphStillProceeds is a positive control.
func TestAgenticInitial_StrictValidEmptyGraphStillProceeds(t *testing.T) {
	f := newAgenticFixture(t, nil)
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatal(err)
	}
	if f.agentic.calls.Load() != 1 {
		t.Fatalf("agentic=%d", f.agentic.calls.Load())
	}
}

func replaceAgenticCaps(t *testing.T, modelJSON, caps string) json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(modelJSON), &m); err != nil {
		t.Fatal(err)
	}
	m["agenticCapabilities"] = json.RawMessage(caps)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func removeModelKey(t *testing.T, modelJSON, key string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(modelJSON), &m); err != nil {
		t.Fatal(err)
	}
	delete(m, key)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func setModelKey(t *testing.T, modelJSON, key, val string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(modelJSON), &m); err != nil {
		t.Fatal(err)
	}
	m[key] = json.RawMessage(val)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func setGraphKey(t *testing.T, graphJSON, key, val string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graphJSON), &m); err != nil {
		t.Fatal(err)
	}
	m[key] = json.RawMessage(val)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func removeGraphKey(t *testing.T, graphJSON, key string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graphJSON), &m); err != nil {
		t.Fatal(err)
	}
	delete(m, key)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func injectGraphNodeField(t *testing.T, graphJSON, key, val string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graphJSON), &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || len(nodes) == 0 {
		t.Fatal(err)
	}
	nodes[0][key] = json.RawMessage(val)
	raw, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	m["nodes"] = raw
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func injectGraphForeignRemote(t *testing.T, graphJSON string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graphJSON), &m); err != nil {
		t.Fatal(err)
	}
	var rem map[string]json.RawMessage
	if err := json.Unmarshal(m["frozenRemotesByCaller"], &rem); err != nil {
		t.Fatal(err)
	}
	rem["ffffffff-ffff-4fff-8fff-ffffffffffff"] = json.RawMessage(`[]`)
	raw, err := json.Marshal(rem)
	if err != nil {
		t.Fatal(err)
	}
	m["frozenRemotesByCaller"] = raw
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func mutateGraphNodeNested(t *testing.T, graphJSON, nest, val string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graphJSON), &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || len(nodes) == 0 {
		t.Fatal(err)
	}
	nodes[0][nest] = json.RawMessage(val)
	raw, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	m["nodes"] = raw
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func mutateGraphNodeNestedKey(t *testing.T, graphJSON, nest, key, val string, remove bool) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graphJSON), &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || len(nodes) == 0 {
		t.Fatal(err)
	}
	var nestObj map[string]json.RawMessage
	if err := json.Unmarshal(nodes[0][nest], &nestObj); err != nil {
		t.Fatal(err)
	}
	if remove {
		delete(nestObj, key)
	} else {
		nestObj[key] = json.RawMessage(val)
	}
	rawNest, err := json.Marshal(nestObj)
	if err != nil {
		t.Fatal(err)
	}
	nodes[0][nest] = rawNest
	raw, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	m["nodes"] = raw
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// injectValidEdgeThenMutate builds a two-node graph with one INTERNAL edge, then
// mutates a single edge field to a hostile value (for closed-enum tests).
func injectValidEdgeThenMutate(t *testing.T, f *agenticFixture, field, val string) string {
	t.Helper()
	childID := "f77ce000-0000-4000-8000-000000000077"
	nodeModel := producerNodeModelSnap(testModelUUID)
	capSnap := emptyCapSnap()
	snap := agentdelegation.GraphSnapshotV1{
		SchemaVersion: agentdelegation.GraphSnapshotSchemaV1,
		RootAgentID:   testAgentUUID,
		MaxDepth:      2, MaxTotal: 8, MaxPerBinding: 3,
		Nodes: []agentdelegation.GraphNodeSnapshot{
			{AgentID: testAgentUUID, ModelConfigID: testModelUUID, ModelConfigLockVer: testModelLockVersion,
				ModelSnapshot: nodeModel, AgentSnapshot: producerNodeAgentSnap(testAgentUUID, testModelUUID),
				CapabilitySnapshot: capSnap, Depth: 0},
			{AgentID: childID, ModelConfigID: testModelUUID, ModelConfigLockVer: testModelLockVersion,
				ModelSnapshot: nodeModel, AgentSnapshot: producerNodeAgentSnap(childID, testModelUUID),
				CapabilitySnapshot: capSnap, Depth: 1},
		},
		Edges: []agentdelegation.GraphEdgeSnapshot{{
			BindingID: "a11ce000-0000-4000-8000-0000000000e1", CallerAgentID: testAgentUUID, TargetAgentID: childID,
			CallableName: "child", Mode: agentdelegation.ModeTask,
			ContextPolicy: agentdelegation.ContextTaskOnly,
			Protocol:      agentdelegation.ProtocolInternal, Version: 1,
		}},
		FrozenRemotesByCaller: map[string][]agentdelegation.FrozenRemoteBinding{
			testAgentUUID: {}, childID: {},
		},
		RemotesFrozen: true,
		BuiltAt:       time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}
	raw, err := agentdelegation.SnapshotJSON(snap)
	if err != nil {
		t.Fatal(err)
	}
	// Prove valid first, then mutate.
	if _, err := agentdelegation.ParseSnapshot(testWSUUID, raw); err != nil {
		t.Fatalf("valid edge fixture: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var edges []map[string]json.RawMessage
	if err := json.Unmarshal(m["edges"], &edges); err != nil || len(edges) == 0 {
		t.Fatal(err)
	}
	edges[0][field] = json.RawMessage(val)
	eraw, err := json.Marshal(edges)
	if err != nil {
		t.Fatal(err)
	}
	m["edges"] = eraw
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	_ = f
	return string(out)
}

func removeGraphNodeKey(t *testing.T, graphJSON, key string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graphJSON), &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || len(nodes) == 0 {
		t.Fatal(err)
	}
	delete(nodes[0], key)
	raw, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	m["nodes"] = raw
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func setGraphNodeKey(t *testing.T, graphJSON, key, val string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graphJSON), &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || len(nodes) == 0 {
		t.Fatal(err)
	}
	nodes[0][key] = json.RawMessage(val)
	raw, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	m["nodes"] = raw
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func injectHostileRemoteSecret(t *testing.T, graphJSON, secretRef string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graphJSON), &m); err != nil {
		t.Fatal(err)
	}
	entry := `{
		"id":"b22ce000-0000-4000-8000-0000000000f2",
		"callerAgentId":"` + testAgentUUID + `",
		"callableName":"hostile_secret",
		"endpointUrl":"https://1.1.1.1/a2a",
		"allowedHosts":["1.1.1.1"],
		"authSecretRef":` + mustJSONString(secretRef) + `,
		"timeoutMs":1000,"version":1
	}`
	m["frozenRemotesByCaller"] = json.RawMessage(`{"` + testAgentUUID + `":[` + entry + `]}`)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func injectGraphSelfEdge(t *testing.T, graphJSON string) string {
	t.Helper()
	return setGraphKey(t, graphJSON, "edges", `[{
		"bindingId":"a11ce000-0000-4000-8000-0000000000e5",
		"callerAgentId":"`+testAgentUUID+`","targetAgentId":"`+testAgentUUID+`",
		"callableName":"self","mode":"TASK","contextPolicy":"TASK_ONLY",
		"version":1,"protocol":"INTERNAL"
	}]`)
}

func injectGraphOrphan(t *testing.T, graphJSON string) string {
	t.Helper()
	orphan := "d44ce000-0000-4000-8000-000000000099"
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graphJSON), &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil {
		t.Fatal(err)
	}
	// The lock must agree with producerNodeModelSnap/producerNodeAgentSnap or
	// the node is rejected by the lock cross-bind check and the reachability
	// invariant this fixture exists for never runs.
	extra := map[string]json.RawMessage{
		"agentId":                json.RawMessage(`"` + orphan + `"`),
		"depth":                  json.RawMessage(`0`),
		"modelConfigId":          json.RawMessage(`"` + testModelUUID + `"`),
		"modelConfigLockVersion": json.RawMessage(itoa(testModelLockVersion)),
		"modelSnapshot":          producerNodeModelSnap(testModelUUID),
		"agentSnapshot":          producerNodeAgentSnap(orphan, testModelUUID),
		"capabilitySnapshot":     emptyCapSnap(),
	}
	nodes = append(nodes, extra)
	raw, _ := json.Marshal(nodes)
	m["nodes"] = raw
	var rem map[string]json.RawMessage
	_ = json.Unmarshal(m["frozenRemotesByCaller"], &rem)
	rem[orphan] = json.RawMessage(`[]`)
	rawRem, _ := json.Marshal(rem)
	m["frozenRemotesByCaller"] = rawRem
	out, _ := json.Marshal(m)
	return string(out)
}

func injectGraphCycle(t *testing.T, f *agenticFixture) string {
	t.Helper()
	// Build valid two-node edge graph then add back-edge for cycle.
	raw := injectValidEdgeThenMutate(t, f, "protocol", `"INTERNAL"`)
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	var edges []map[string]json.RawMessage
	if err := json.Unmarshal(m["edges"], &edges); err != nil || len(edges) == 0 {
		t.Fatal(err)
	}
	back := map[string]json.RawMessage{}
	for k, v := range edges[0] {
		back[k] = v
	}
	back["bindingId"] = json.RawMessage(`"a11ce000-0000-4000-8000-0000000000c9"`)
	back["callerAgentId"] = edges[0]["targetAgentId"]
	back["targetAgentId"] = edges[0]["callerAgentId"]
	back["callableName"] = json.RawMessage(`"back_edge"`)
	edges = append(edges, back)
	eraw, _ := json.Marshal(edges)
	m["edges"] = eraw
	out, _ := json.Marshal(m)
	return string(out)
}

func setGraphNodeDepth(t *testing.T, graphJSON string, idx, depth int) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graphJSON), &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || idx >= len(nodes) {
		t.Fatal(err)
	}
	depthRaw, err := json.Marshal(depth)
	if err != nil {
		t.Fatal(err)
	}
	nodes[idx]["depth"] = depthRaw
	raw, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	m["nodes"] = raw
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func injectHostileRemote(t *testing.T, graphJSON, endpoint string, hosts []string, agentCard string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graphJSON), &m); err != nil {
		t.Fatal(err)
	}
	hostsRaw, _ := json.Marshal(hosts)
	cardField := ""
	if agentCard != "" {
		cardField = `,"agentCardUrl":` + mustJSONString(agentCard)
	}
	entry := `{
		"id":"b22ce000-0000-4000-8000-0000000000f1",
		"callerAgentId":"` + testAgentUUID + `",
		"callableName":"hostile_remote",
		"endpointUrl":` + mustJSONString(endpoint) + `,
		"allowedHosts":` + string(hostsRaw) + cardField + `,
		"timeoutMs":1000,"version":1
	}`
	m["frozenRemotesByCaller"] = json.RawMessage(`{"` + testAgentUUID + `":[` + entry + `]}`)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func mustJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func injectGraphCapRelease(t *testing.T, graphJSON, kind, risk, side, capID string) string {
	t.Helper()
	cap := `{
		"schemaVersion":"capability-snapshot.v1",
		"releases":[{
			"capabilityId":"` + capID + `",
			"releaseId":"c33ce000-0000-4000-8000-0000000000d1",
			"kind":"` + kind + `","callableName":"x","callableDescription":"",
			"inputSchema":{},"outputSchema":{},
			"riskLevel":"` + risk + `","sideEffectLevel":"` + side + `",
			"requiresConfirmation":false
		}]
	}`
	return mutateGraphNodeNested(t, graphJSON, "capabilitySnapshot", cap)
}

func injectDupRemote(t *testing.T, graphJSON string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graphJSON), &m); err != nil {
		t.Fatal(err)
	}
	// Two remotes with same id for the root agent (public IP so SSRF policy applies).
	rid := "b22ce000-0000-4000-8000-0000000000d1"
	entry := `{
		"id":"` + rid + `","callerAgentId":"` + testAgentUUID + `",
		"callableName":"remote_a","endpointUrl":"https://1.1.1.1/a2a",
		"allowedHosts":["1.1.1.1"],"timeoutMs":1000,"version":1
	}`
	entry2 := `{
		"id":"` + rid + `","callerAgentId":"` + testAgentUUID + `",
		"callableName":"remote_b","endpointUrl":"https://1.1.1.1/a2a",
		"allowedHosts":["1.1.1.1"],"timeoutMs":1000,"version":1
	}`
	m["frozenRemotesByCaller"] = json.RawMessage(`{"` + testAgentUUID + `":[` + entry + `,` + entry2 + `]}`)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// Silence unused imports if any.
var (
	_ = context.Background
	_ = agentrun.Job{}
	_ = execution.AgentRun{}
	_ = modelconfig.Config{}
	_ = chatruntimebridge.ErrAgenticModelSnapshotRequired
	_ = time.Time{}
	_ = agentdelegation.GraphSnapshotSchemaV1
)
