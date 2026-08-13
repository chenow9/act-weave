package agentdelegation_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentdelegation"
)

// testFreezeWorkspaceID is the tenant that owns every freeze under test.
// ParseSnapshot requires it so frozen remote authSecretRef values are checked
// against the owning workspace (a2agateway.CreateRemote parity).
const testFreezeWorkspaceID = "a11ce000-0000-4000-8000-00000000000f"

// Producer-shaped nested node snapshots (matches snapshotAgentNode /
// capabilitySnapshotJSON). Not the root run.ModelSnapshot shape.
func producerNodeModelSnap(modelID string) json.RawMessage {
	return json.RawMessage(`{` +
		`"id":"` + modelID + `",` +
		`"provider":"openai",` +
		`"apiBase":"https://api.example.com/v1",` +
		`"modelName":"gpt-test",` +
		`"options":{},` +
		`"credentialSecretId":null,` +
		`"lockVersion":1,` +
		`"status":"VERIFIED",` +
		`"agenticCapabilities":{},` +
		`"runtimeCapabilities":{},` +
		`"toolDisclosurePolicy":{}` +
		`}`)
}

func producerNodeAgentSnap(agentID, modelID string) json.RawMessage {
	return json.RawMessage(`{` +
		`"schemaVersion":"agent-binding.v1",` +
		`"agentId":"` + agentID + `",` +
		`"name":"Agent",` +
		`"roleDescription":"",` +
		`"promptRevisionId":"",` +
		`"promptRevisionHash":"",` +
		`"modelConfigId":"` + modelID + `",` +
		`"modelConfigLockVer":1` +
		`}`)
}

func producerNodeCapSnap() json.RawMessage {
	return json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[]}`)
}

func validEmptyGraph(t *testing.T) json.RawMessage {
	t.Helper()
	agentID := "d44ce000-0000-4000-8000-000000000004"
	modelID := "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	snap := agentdelegation.GraphSnapshotV1{
		SchemaVersion: agentdelegation.GraphSnapshotSchemaV1,
		RootAgentID:   agentID,
		MaxDepth:      2, MaxTotal: 8, MaxPerBinding: 3,
		Nodes: []agentdelegation.GraphNodeSnapshot{{
			AgentID: agentID, ModelConfigID: modelID, ModelConfigLockVer: 1,
			ModelSnapshot:      producerNodeModelSnap(modelID),
			AgentSnapshot:      producerNodeAgentSnap(agentID, modelID),
			CapabilitySnapshot: producerNodeCapSnap(), Depth: 0,
		}},
		Edges:                 []agentdelegation.GraphEdgeSnapshot{},
		FrozenRemotesByCaller: map[string][]agentdelegation.FrozenRemoteBinding{agentID: {}},
		RemotesFrozen:         true,
		BuiltAt:               time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}
	raw, err := agentdelegation.SnapshotJSON(snap)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func validEdgeGraph(t *testing.T) json.RawMessage {
	t.Helper()
	root := "d44ce000-0000-4000-8000-000000000004"
	child := "d44ce000-0000-4000-8000-000000000005"
	modelID := "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	snap := agentdelegation.GraphSnapshotV1{
		SchemaVersion: agentdelegation.GraphSnapshotSchemaV1,
		RootAgentID:   root,
		MaxDepth:      2, MaxTotal: 8, MaxPerBinding: 3,
		Nodes: []agentdelegation.GraphNodeSnapshot{
			{AgentID: root, ModelConfigID: modelID, ModelConfigLockVer: 1,
				ModelSnapshot:      producerNodeModelSnap(modelID),
				AgentSnapshot:      producerNodeAgentSnap(root, modelID),
				CapabilitySnapshot: producerNodeCapSnap(), Depth: 0},
			{AgentID: child, ModelConfigID: modelID, ModelConfigLockVer: 1,
				ModelSnapshot:      producerNodeModelSnap(modelID),
				AgentSnapshot:      producerNodeAgentSnap(child, modelID),
				CapabilitySnapshot: producerNodeCapSnap(), Depth: 1},
		},
		Edges: []agentdelegation.GraphEdgeSnapshot{{
			BindingID: "a11ce000-0000-4000-8000-0000000000b1", CallerAgentID: root, TargetAgentID: child,
			CallableName: "call_child", Mode: agentdelegation.ModeTask,
			ContextPolicy: agentdelegation.ContextTaskOnly, Version: 1,
			Protocol: agentdelegation.ProtocolInternal,
		}},
		FrozenRemotesByCaller: map[string][]agentdelegation.FrozenRemoteBinding{
			root: {}, child: {},
		},
		RemotesFrozen: true,
		BuiltAt:       time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}
	raw, err := agentdelegation.SnapshotJSON(snap)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func validRemoteGraph(t *testing.T) json.RawMessage {
	t.Helper()
	agentID := "d44ce000-0000-4000-8000-000000000004"
	modelID := "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	snap := agentdelegation.GraphSnapshotV1{
		SchemaVersion: agentdelegation.GraphSnapshotSchemaV1,
		RootAgentID:   agentID,
		MaxDepth:      2, MaxTotal: 8, MaxPerBinding: 3,
		Nodes: []agentdelegation.GraphNodeSnapshot{{
			AgentID: agentID, ModelConfigID: modelID, ModelConfigLockVer: 1,
			ModelSnapshot:      producerNodeModelSnap(modelID),
			AgentSnapshot:      producerNodeAgentSnap(agentID, modelID),
			CapabilitySnapshot: producerNodeCapSnap(), Depth: 0,
		}},
		Edges: []agentdelegation.GraphEdgeSnapshot{},
		FrozenRemotesByCaller: map[string][]agentdelegation.FrozenRemoteBinding{
			agentID: {{
				ID: "b22ce000-0000-4000-8000-0000000000a1", CallerAgentID: agentID, CallableName: "remote_call",
				EndpointURL: "https://1.1.1.1/a2a", AllowedHosts: []string{"1.1.1.1"},
				TimeoutMs: 5000, Version: 1,
			}},
		},
		RemotesFrozen: true,
		BuiltAt:       time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}
	raw, err := agentdelegation.SnapshotJSON(snap)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseSnapshot_StrictRejectsNullContainersAndDuplicates(t *testing.T) {
	base := validEmptyGraph(t)
	// Prove producer emits explicit [] not null for edges and required budgets.
	if strings.Contains(string(base), `"edges":null`) {
		t.Fatalf("SnapshotJSON must not emit edges:null: %s", base)
	}
	if !strings.Contains(string(base), `"edges":[]`) {
		t.Fatalf("SnapshotJSON must emit edges:[]: %s", base)
	}
	for _, req := range []string{"maxDepth", "maxTotalDelegations", "maxPerBinding", "builtAt"} {
		if !strings.Contains(string(base), `"`+req+`"`) {
			t.Fatalf("SnapshotJSON missing %s: %s", req, base)
		}
	}

	if _, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, base); err != nil {
		t.Fatalf("valid empty: %v", err)
	}

	cases := []struct {
		name string
		raw  string
	}{
		{"edges_null", setKey(t, base, "edges", `null`)},
		{"edges_missing", removeJSONKey(t, base, "edges")},
		{"nodes_null", forceNodesNull(t, base)},
		{"maxDepth_missing", removeJSONKey(t, base, "maxDepth")},
		{"maxDepth_null", setKey(t, base, "maxDepth", `null`)},
		{"foreign_remotes_caller", injectForeignRemoteCaller(t, base)},
		{"node_unknown_field", injectNodeUnknownField(t, base)},
		{"edge_empty_object", forceEdgesEmptyObject(t, base)},
		{"remotes_caller_null", forceRemoteCallerNull(t, base)},
		{"duplicate_schema_forged_first", forceDupSchema(t, base, true)},
		{"duplicate_schema_legit_first", forceDupSchema(t, base, false)},
		{"trailing_garbage", string(base) + `{"x":1}`},
		{"outer_whitespace", " " + string(base)},
		{"unknown_top_field", injectTopField(t, base, "evil", `true`)},
		{"unrecognized_schema", strings.Replace(string(base), agentdelegation.GraphSnapshotSchemaV1, "agent_graph_snapshot.v9", 1)},
		{"extra_rejected", injectTopField(t, base, "extra", `{}`)},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, json.RawMessage(tc.raw))
			if err == nil {
				t.Fatalf("expected reject for %s raw=%s", tc.name, truncate(tc.raw, 160))
			}
		})
	}
}

// TestParseSnapshot_NodeNestedClosedContracts covers cycle-6 nested schemas.
func TestParseSnapshot_NodeNestedClosedContracts(t *testing.T) {
	base := validEmptyGraph(t)
	edgeBase := validEdgeGraph(t)
	remoteBase := validRemoteGraph(t)

	// Positive round-trips.
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
	}{
		{"empty", base},
		{"edge", edgeBase},
		{"remote", remoteBase},
	} {
		t.Run("valid_"+tc.name, func(t *testing.T) {
			got, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, tc.raw)
			if err != nil || got == nil {
				t.Fatalf("err=%v", err)
			}
		})
	}

	// Nullable credentialSecretId:null is legal for node producer.
	t.Run("valid_cred_null", func(t *testing.T) {
		if !strings.Contains(string(base), `"credentialSecretId":null`) {
			t.Fatal("fixture must include credentialSecretId:null")
		}
		if _, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, base); err != nil {
			t.Fatal(err)
		}
	})

	type mut struct {
		name string
		raw  string
	}
	mutations := []mut{
		// Node modelSnapshot
		{"node_model_forged_only", mutateNodeNested(t, base, "modelSnapshot", `{"forged":"not-a-model"}`)},
		{"node_model_missing_id", mutateNodeNestedKey(t, base, "modelSnapshot", "id", "", true)},
		{"node_model_null_id", mutateNodeNestedKey(t, base, "modelSnapshot", "id", `null`, false)},
		{"node_model_wrong_type_id", mutateNodeNestedKey(t, base, "modelSnapshot", "id", `1`, false)},
		{"node_model_unknown_field", mutateNodeNestedKey(t, base, "modelSnapshot", "forgedField", `true`, false)},
		{"node_model_missing_options", mutateNodeNestedKey(t, base, "modelSnapshot", "options", "", true)},
		{"node_model_null_options", mutateNodeNestedKey(t, base, "modelSnapshot", "options", `null`, false)},
		{"node_model_missing_credential", mutateNodeNestedKey(t, base, "modelSnapshot", "credentialSecretId", "", true)},
		{"node_model_missing_status", mutateNodeNestedKey(t, base, "modelSnapshot", "status", "", true)},
		{"node_model_missing_agentic", mutateNodeNestedKey(t, base, "modelSnapshot", "agenticCapabilities", "", true)},
		{"node_model_missing_runtime", mutateNodeNestedKey(t, base, "modelSnapshot", "runtimeCapabilities", "", true)},
		{"node_model_missing_policy", mutateNodeNestedKey(t, base, "modelSnapshot", "toolDisclosurePolicy", "", true)},
		{"node_model_id_mismatch", mutateNodeNestedKey(t, base, "modelSnapshot", "id", `"00000000-0000-4000-8000-000000000099"`, false)},
		{"node_model_dup_id", forceNodeModelDupKey(t, base)},
		// Lock-version identity (cycle 8)
		{"node_lock_missing", removeNodeField(t, base, "modelConfigLockVersion")},
		{"node_lock_null", setNodeField(t, base, "modelConfigLockVersion", `null`)},
		{"node_lock_wrong_type", setNodeField(t, base, "modelConfigLockVersion", `"1"`)},
		{"node_lock_zero", setNodeField(t, base, "modelConfigLockVersion", `0`)},
		{"node_lock_vs_model_mismatch", forceLockMismatch(t, base, "model")},
		{"node_lock_vs_agent_mismatch", forceLockMismatch(t, base, "agent")},
		// Node agentSnapshot
		{"node_agent_unknown_field", mutateNodeNestedKey(t, base, "agentSnapshot", "evil", `1`, false)},
		{"node_agent_missing_agentId", mutateNodeNestedKey(t, base, "agentSnapshot", "agentId", "", true)},
		{"node_agent_wrong_agentId", mutateNodeNestedKey(t, base, "agentSnapshot", "agentId", `"ffffffff-ffff-4fff-8fff-ffffffffffff"`, false)},
		{"node_agent_forged_schema", mutateNodeNestedKey(t, base, "agentSnapshot", "schemaVersion", `"forged-binding.v9"`, false)},
		{"node_agent_missing_modelConfigId", mutateNodeNestedKey(t, base, "agentSnapshot", "modelConfigId", "", true)},
		// Node capabilitySnapshot
		{"node_cap_forged_schema", mutateNodeNestedKey(t, base, "capabilitySnapshot", "schemaVersion", `"forged-cap.v9"`, false)},
		{"node_cap_unknown_field", mutateNodeNestedKey(t, base, "capabilitySnapshot", "marker", `"x"`, false)},
		{"node_cap_null_releases", mutateNodeNestedKey(t, base, "capabilitySnapshot", "releases", `null`, false)},
		{"node_cap_missing_releases", mutateNodeNestedKey(t, base, "capabilitySnapshot", "releases", "", true)},
		// Edge enums / uniqueness
		{"edge_protocol_forged", mutateEdgeField(t, edgeBase, "protocol", `"FORGED_PROTO"`)},
		{"edge_mode_forged", mutateEdgeField(t, edgeBase, "mode", `"FORGED_MODE"`)},
		{"edge_context_forged", mutateEdgeField(t, edgeBase, "contextPolicy", `"NONE"`)},
		{"edge_dup_binding", forceDupEdgeBinding(t, edgeBase)},
		{"edge_dup_callable", forceDupEdgeCallable(t, edgeBase)},
		// Remote uniqueness
		{"remote_dup_id", forceDupRemote(t, remoteBase, true)},
		{"remote_dup_callable", forceDupRemote(t, remoteBase, false)},
		{"remote_empty_object", forceRemoteEmptyObject(t, remoteBase)},
		{"remote_caller_mismatch", mutateRemoteField(t, remoteBase, "callerAgentId", `"ffffffff-ffff-4fff-8fff-ffffffffffff"`)},
	}

	for _, tc := range mutations {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.raw == string(base) || tc.raw == string(edgeBase) || tc.raw == string(remoteBase) {
				t.Fatal("mutation did not change raw")
			}
			_, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, json.RawMessage(tc.raw))
			if err == nil {
				t.Fatalf("expected reject for %s", tc.name)
			}
		})
	}
}

func TestParseSnapshot_ExplicitEmptySucceeds(t *testing.T) {
	raw := validEmptyGraph(t)
	got, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, raw)
	if err != nil || got == nil {
		t.Fatalf("err=%v", err)
	}
	if got.Edges == nil || len(got.Edges) != 0 {
		t.Fatalf("edges=%v", got.Edges)
	}
	if got.FrozenRemotesByCaller == nil {
		t.Fatal("remotes map nil")
	}
}

func TestSnapshotJSON_NeverEmitsNullContainers(t *testing.T) {
	snap := agentdelegation.GraphSnapshotV1{
		SchemaVersion: agentdelegation.GraphSnapshotSchemaV1,
		RootAgentID:   "a",
		// Nodes empty invalid for integrity but marshal shape check:
		Nodes: nil, Edges: nil, FrozenRemotesByCaller: nil, RemotesFrozen: true,
	}
	raw, err := agentdelegation.SnapshotJSON(snap)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, `"edges":null`) || strings.Contains(s, `"nodes":null`) ||
		strings.Contains(s, `"frozenRemotesByCaller":null`) {
		t.Fatalf("null containers: %s", s)
	}
	if !strings.Contains(s, `"edges":[]`) || !strings.Contains(s, `"nodes":[]`) {
		t.Fatalf("expected explicit empty arrays: %s", s)
	}
}

func forceRemoteCallerNull(t *testing.T, base json.RawMessage) string {
	t.Helper()
	s := string(base)
	s = strings.Replace(s, `"d44ce000-0000-4000-8000-000000000004":[]`, `"d44ce000-0000-4000-8000-000000000004":null`, 1)
	return s
}

func forceNodesNull(t *testing.T, base json.RawMessage) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	m["nodes"] = json.RawMessage(`null`)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func removeJSONKey(t *testing.T, base json.RawMessage, key string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, key)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func injectTopField(t *testing.T, base json.RawMessage, key, val string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	m[key] = json.RawMessage(val)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func setKey(t *testing.T, base json.RawMessage, key, val string) string {
	t.Helper()
	return injectTopField(t, base, key, val)
}

func injectForeignRemoteCaller(t *testing.T, base json.RawMessage) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
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

func injectNodeUnknownField(t *testing.T, base json.RawMessage) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || len(nodes) == 0 {
		t.Fatal(err)
	}
	nodes[0]["forgedNodeField"] = json.RawMessage(`true`)
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

func forceEdgesEmptyObject(t *testing.T, base json.RawMessage) string {
	t.Helper()
	return setKey(t, base, "edges", `[{}]`)
}

func forceDupSchema(t *testing.T, base json.RawMessage, forgedFirst bool) string {
	t.Helper()
	s := string(base)
	if forgedFirst {
		return strings.Replace(s, `"schemaVersion":"agent_graph_snapshot.v1"`,
			`"schemaVersion":"forged.v1","schemaVersion":"agent_graph_snapshot.v1"`, 1)
	}
	return strings.Replace(s, `"schemaVersion":"agent_graph_snapshot.v1"`,
		`"schemaVersion":"agent_graph_snapshot.v1","schemaVersion":"forged.v1"`, 1)
}

func removeNodeField(t *testing.T, base json.RawMessage, key string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
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

func setNodeField(t *testing.T, base json.RawMessage, key, val string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
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

// forceLockMismatch leaves node.modelConfigLockVersion=1 and diverges nested lock.
// which=model|agent|both
func forceLockMismatch(t *testing.T, base json.RawMessage, which string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || len(nodes) == 0 {
		t.Fatal(err)
	}
	if which == "model" || which == "both" {
		var ms map[string]json.RawMessage
		if err := json.Unmarshal(nodes[0]["modelSnapshot"], &ms); err != nil {
			t.Fatal(err)
		}
		ms["lockVersion"] = json.RawMessage(`2`)
		rawMS, _ := json.Marshal(ms)
		nodes[0]["modelSnapshot"] = rawMS
	}
	if which == "agent" || which == "both" {
		var as map[string]json.RawMessage
		if err := json.Unmarshal(nodes[0]["agentSnapshot"], &as); err != nil {
			t.Fatal(err)
		}
		as["modelConfigLockVer"] = json.RawMessage(`2`)
		rawAS, _ := json.Marshal(as)
		nodes[0]["agentSnapshot"] = rawAS
	}
	// Keep node lock at 1 (required present).
	nodes[0]["modelConfigLockVersion"] = json.RawMessage(`1`)
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

func mutateNodeNested(t *testing.T, base json.RawMessage, nest, val string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
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

func mutateNodeNestedKey(t *testing.T, base json.RawMessage, nest, key, val string, remove bool) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
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

func forceNodeModelDupKey(t *testing.T, base json.RawMessage) string {
	t.Helper()
	// Inject duplicate "id" inside modelSnapshot via string replace.
	s := string(base)
	// first occurrence of id inside node model
	old := `"id":"c08f1f2e-7b5a-7c3d-8e9f-1234567890a1"`
	if !strings.Contains(s, old) {
		t.Fatal("model id not found")
	}
	// only replace inside modelSnapshot: replace first modelSnap id occurrence after modelSnapshot
	idx := strings.Index(s, `"modelSnapshot":`)
	if idx < 0 {
		t.Fatal("no modelSnapshot")
	}
	head, tail := s[:idx], s[idx:]
	tail = strings.Replace(tail, old, `"id":"00000000-0000-4000-8000-000000000099","id":"c08f1f2e-7b5a-7c3d-8e9f-1234567890a1"`, 1)
	return head + tail
}

func mutateEdgeField(t *testing.T, base json.RawMessage, key, val string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	var edges []map[string]json.RawMessage
	if err := json.Unmarshal(m["edges"], &edges); err != nil || len(edges) == 0 {
		t.Fatal(err)
	}
	edges[0][key] = json.RawMessage(val)
	raw, err := json.Marshal(edges)
	if err != nil {
		t.Fatal(err)
	}
	m["edges"] = raw
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func forceDupEdgeBinding(t *testing.T, base json.RawMessage) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	var edges []map[string]json.RawMessage
	if err := json.Unmarshal(m["edges"], &edges); err != nil || len(edges) == 0 {
		t.Fatal(err)
	}
	// Second edge with same bindingId, different callable.
	dup := map[string]json.RawMessage{}
	for k, v := range edges[0] {
		dup[k] = v
	}
	dup["callableName"] = json.RawMessage(`"other_call"`)
	edges = append(edges, dup)
	raw, err := json.Marshal(edges)
	if err != nil {
		t.Fatal(err)
	}
	m["edges"] = raw
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func forceDupEdgeCallable(t *testing.T, base json.RawMessage) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	var edges []map[string]json.RawMessage
	if err := json.Unmarshal(m["edges"], &edges); err != nil || len(edges) == 0 {
		t.Fatal(err)
	}
	dup := map[string]json.RawMessage{}
	for k, v := range edges[0] {
		dup[k] = v
	}
	dup["bindingId"] = json.RawMessage(`"a11ce000-0000-4000-8000-0000000000b2"`)
	edges = append(edges, dup)
	raw, err := json.Marshal(edges)
	if err != nil {
		t.Fatal(err)
	}
	m["edges"] = raw
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func forceDupRemote(t *testing.T, base json.RawMessage, byID bool) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	var rem map[string][]map[string]json.RawMessage
	if err := json.Unmarshal(m["frozenRemotesByCaller"], &rem); err != nil {
		t.Fatal(err)
	}
	agentID := "d44ce000-0000-4000-8000-000000000004"
	list := rem[agentID]
	if len(list) == 0 {
		t.Fatal("no remotes")
	}
	dup := map[string]json.RawMessage{}
	for k, v := range list[0] {
		dup[k] = v
	}
	if byID {
		dup["callableName"] = json.RawMessage(`"other_remote"`)
	} else {
		dup["id"] = json.RawMessage(`"b22ce000-0000-4000-8000-0000000000a2"`)
	}
	list = append(list, dup)
	rem[agentID] = list
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

func forceRemoteEmptyObject(t *testing.T, base json.RawMessage) string {
	t.Helper()
	agentID := "d44ce000-0000-4000-8000-000000000004"
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	m["frozenRemotesByCaller"] = json.RawMessage(`{"` + agentID + `":[{}]}`)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func mutateRemoteField(t *testing.T, base json.RawMessage, key, val string) string {
	t.Helper()
	agentID := "d44ce000-0000-4000-8000-000000000004"
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	var rem map[string][]map[string]json.RawMessage
	if err := json.Unmarshal(m["frozenRemotesByCaller"], &rem); err != nil {
		t.Fatal(err)
	}
	list := rem[agentID]
	list[0][key] = json.RawMessage(val)
	rem[agentID] = list
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
