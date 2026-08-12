package agentdelegation_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentdelegation"
)

// TestParseSnapshot_SemanticDomainContract covers cycle-7 domain-semantic
// failures (topology, enums, remote policy, capability IDs). Cumulative with
// cycle 1–6 structural tests.
func TestParseSnapshot_SemanticDomainContract(t *testing.T) {
	base := validEmptyGraph(t)
	edgeBase := validEdgeGraph(t)
	remoteBase := validRemoteGraph(t)

	// Positive controls still succeed.
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
	}{
		{"empty", base},
		{"edge", edgeBase},
		{"remote", remoteBase},
	} {
		t.Run("valid_"+tc.name, func(t *testing.T) {
			if _, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, tc.raw); err != nil {
				t.Fatalf("err=%v", err)
			}
		})
	}

	type mut struct {
		name string
		raw  string
		// wantReason is a substring of the real ParseSnapshot error. It is
		// mandatory: asserting only "some error was returned" is what let a
		// fixture with a non-hex UUID be rejected by an earlier gate while the
		// cell kept claiming to cover a later invariant.
		wantReason string
	}
	mutations := []mut{
		// Topology
		{"root_depth_nonzero", setNodeDepth(t, base, 0, 1),
			"root node depth must be 0"},
		{"child_depth_mismatch", setNodeDepth(t, edgeBase, 1, 0),
			"depth 0 != shortest-path depth 1"},
		{"orphan_node", injectOrphanNode(t, base),
			"not reachable from root"},
		{"cycle_ab", injectCycle(t, edgeBase),
			"graph cycle detected"},
		{"self_edge", injectSelfEdge(t, base),
			"edges[0] self-edge forbidden"},
		// The BFS in validateTopologySemantics never hands out a distance above
		// maxDepth, so an over-deep node is always caught by the shortest-path
		// equality check first; the separate n.Depth > maxDepth branch cannot
		// be reached from a fixture. Pin the reason that really fires.
		{"depth_exceeds_max", setNodeDepth(t, edgeBase, 1, 99),
			"depth 99 != shortest-path depth 1"},
		// Identity
		{"model_config_id_uppercase", mutateNodeField(t, base, "modelConfigId",
			`"`+strings.ToUpper("c08f1f2e-7b5a-7c3d-8e9f-1234567890a1")+`"`),
			"nodes[0].modelConfigId must be canonical UUID"},
		{"empty_api_base", mutateNodeNestedKey(t, base, "modelSnapshot", "apiBase", `""`, false),
			"nodes[0].modelSnapshot.apiBase invalid"},
		// Internal edge enums — remote-only values forbidden on edges
		{"edge_protocol_a2a", mutateEdgeField(t, edgeBase, "protocol", `"A2A"`),
			`edges[0].protocol "A2A" not in closed enum`},
		{"edge_context_summary", mutateEdgeField(t, edgeBase, "contextPolicy", `"SUMMARY"`),
			`edges[0].contextPolicy "SUMMARY" not in closed enum`},
		{"edge_context_selected", mutateEdgeField(t, edgeBase, "contextPolicy", `"SELECTED_MESSAGES"`),
			`edges[0].contextPolicy "SELECTED_MESSAGES" not in closed enum`},
		{"edge_mode_forged", mutateEdgeField(t, edgeBase, "mode", `"PARALLEL"`),
			`edges[0].mode "PARALLEL" not in closed enum`},
		// Remote policy (reviewer variants)
		{"remote_http_scheme", mutateRemoteField(t, remoteBase, "endpointUrl", `"http://1.1.1.1/a2a"`),
			"remote policy: egressguard: ssrf denied: http only allowed for loopback"},
		{"remote_file_scheme", mutateRemoteField(t, remoteBase, "endpointUrl", `"file:///etc/passwd"`),
			"remote policy: egressguard: invalid"},
		{"remote_userinfo", mutateRemoteField(t, remoteBase, "endpointUrl", `"https://user:pass@1.1.1.1/a2a"`),
			"remote policy: egressguard: ssrf denied: userinfo not allowed"},
		{"remote_empty_allowlist", mutateRemoteField(t, remoteBase, "allowedHosts", `[]`),
			"remote policy: egressguard: invalid: allowedHosts must be non-empty"},
		{"remote_mismatched_allowlist", mutateRemoteField(t, remoteBase, "allowedHosts", `["8.8.8.8"]`),
			`remote policy: egressguard: ssrf denied: host "1.1.1.1" not in allowlist`},
		{"remote_agent_card_file", mutateRemoteField(t, remoteBase, "agentCardUrl", `"file:///tmp/card"`),
			"remote policy: agentCardURL: egressguard: invalid"},
		{"remote_bad_secret_ref", mutateRemoteField(t, remoteBase, "authSecretRef", `"not-a-secret-ref"`),
			"authSecretRef must be secret:<workspaceId>:<secretId>"},
		{"remote_secret_bad_uuid", mutateRemoteField(t, remoteBase, "authSecretRef",
			`"secret:not-uuid:also-bad"`),
			"authSecretRef workspace/secret must be UUIDs"},
		// Capability forged enums/IDs. capabilityId and releaseId are checked
		// before the closed-domain enums, so both must be canonical here or the
		// enum check under test never runs.
		{"cap_forged_kind", injectCapRelease(t, base, map[string]any{
			"capabilityId": "c33ce000-0000-4000-8000-0000000000c1",
			"releaseId":    "c33ce000-0000-4000-8000-0000000000e1",
			"kind":         "FORGED_KIND", "callableName": "x", "callableDescription": "",
			"inputSchema": map[string]any{}, "outputSchema": map[string]any{},
			"riskLevel": "LOW", "sideEffectLevel": "NONE", "requiresConfirmation": false,
		}), `releases[0].kind "FORGED_KIND" not in closed domain`},
		{"cap_forged_risk", injectCapRelease(t, base, map[string]any{
			"capabilityId": "c33ce000-0000-4000-8000-0000000000c1",
			"releaseId":    "c33ce000-0000-4000-8000-0000000000e1",
			"kind":         "TOOL", "callableName": "x", "callableDescription": "",
			"inputSchema": map[string]any{}, "outputSchema": map[string]any{},
			"riskLevel": "SUPER", "sideEffectLevel": "NONE", "requiresConfirmation": false,
		}), `releases[0].riskLevel "SUPER" not in closed domain`},
		{"cap_forged_side_effect", injectCapRelease(t, base, map[string]any{
			"capabilityId": "c33ce000-0000-4000-8000-0000000000c1",
			"releaseId":    "c33ce000-0000-4000-8000-0000000000e1",
			"kind":         "TOOL", "callableName": "x", "callableDescription": "",
			"inputSchema": map[string]any{}, "outputSchema": map[string]any{},
			"riskLevel": "LOW", "sideEffectLevel": "DESTROY", "requiresConfirmation": false,
		}), `releases[0].sideEffectLevel "DESTROY" not in closed domain`},
		{"cap_noncanonical_id", injectCapRelease(t, base, map[string]any{
			// non-canonical UUID fixture: uppercase is the invariant under test.
			"capabilityId": "C33CE000-0000-4000-8000-0000000000C1",
			"releaseId":    "c33ce000-0000-4000-8000-0000000000e1",
			"kind":         "TOOL", "callableName": "x", "callableDescription": "",
			"inputSchema": map[string]any{}, "outputSchema": map[string]any{},
			"riskLevel": "LOW", "sideEffectLevel": "NONE", "requiresConfirmation": false,
		}), "releases[0].capabilityId must be canonical UUID"},
	}

	for _, tc := range mutations {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.raw == string(base) || tc.raw == string(edgeBase) || tc.raw == string(remoteBase) {
				t.Fatal("mutation did not change raw")
			}
			if tc.wantReason == "" {
				t.Fatal("every cell must name the invariant it expects to fire")
			}
			_, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, json.RawMessage(tc.raw))
			if err == nil {
				t.Fatalf("expected semantic reject for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantReason) {
				t.Fatalf("%s was rejected for the wrong invariant:\n got: %v\nwant substring: %q",
					tc.name, err, tc.wantReason)
			}
		})
	}
}

// TestParseSnapshot_ValidCapabilityReleaseRoundTrip ensures a producer-shaped
// release with closed enums/canonical UUIDs is accepted.
func TestParseSnapshot_ValidCapabilityReleaseRoundTrip(t *testing.T) {
	raw := injectCapRelease(t, validEmptyGraph(t), map[string]any{
		"capabilityId": "c33ce000-0000-4000-8000-0000000000c1",
		"releaseId":    "c33ce000-0000-4000-8000-0000000000d1",
		"kind":         "TOOL", "callableName": "search", "callableDescription": "s",
		"inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"},
		"riskLevel": "LOW", "sideEffectLevel": "NONE", "requiresConfirmation": false,
	})
	if _, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, json.RawMessage(raw)); err != nil {
		t.Fatal(err)
	}
}

func setNodeDepth(t *testing.T, base json.RawMessage, idx, depth int) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil || idx >= len(nodes) {
		t.Fatal(err)
	}
	nodes[idx]["depth"] = json.RawMessage(itoa(depth))
	raw, _ := json.Marshal(nodes)
	m["nodes"] = raw
	out, _ := json.Marshal(m)
	return string(out)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func mutateNodeField(t *testing.T, base json.RawMessage, key, val string) string {
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
	// Keep nested model id in sync when mutating modelConfigId for structural cross-bind,
	// so we hit the semantic noncanonical path on modelConfigId itself.
	if key == "modelConfigId" {
		// leave modelSnapshot.id as old so structural cross-bind fails first OR
		// update both to uppercase — semantic rejects uppercase on modelConfigId.
		var ms map[string]json.RawMessage
		_ = json.Unmarshal(nodes[0]["modelSnapshot"], &ms)
		ms["id"] = json.RawMessage(val)
		rawMS, _ := json.Marshal(ms)
		nodes[0]["modelSnapshot"] = rawMS
		var as map[string]json.RawMessage
		_ = json.Unmarshal(nodes[0]["agentSnapshot"], &as)
		as["modelConfigId"] = json.RawMessage(val)
		rawAS, _ := json.Marshal(as)
		nodes[0]["agentSnapshot"] = rawAS
	}
	raw, _ := json.Marshal(nodes)
	m["nodes"] = raw
	out, _ := json.Marshal(m)
	return string(out)
}

func injectOrphanNode(t *testing.T, base json.RawMessage) string {
	t.Helper()
	orphan := "d44ce000-0000-4000-8000-000000000099"
	modelID := "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(m["nodes"], &nodes); err != nil {
		t.Fatal(err)
	}
	// Orphan at depth 0 (not reachable via edges from root).
	extra := map[string]json.RawMessage{
		"agentId":                json.RawMessage(`"` + orphan + `"`),
		"depth":                  json.RawMessage(`0`),
		"modelConfigId":          json.RawMessage(`"` + modelID + `"`),
		"modelConfigLockVersion": json.RawMessage(`1`),
		"modelSnapshot":          producerNodeModelSnap(modelID),
		"agentSnapshot":          producerNodeAgentSnap(orphan, modelID),
		"capabilitySnapshot":     producerNodeCapSnap(),
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

func injectCycle(t *testing.T, edgeBase json.RawMessage) string {
	t.Helper()
	// edgeBase is root -> child. Add child -> root.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(edgeBase, &m); err != nil {
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
	// Swap caller/target, new binding id.
	back["bindingId"] = json.RawMessage(`"a11ce000-0000-4000-8000-0000000000c9"`)
	back["callerAgentId"] = edges[0]["targetAgentId"]
	back["targetAgentId"] = edges[0]["callerAgentId"]
	back["callableName"] = json.RawMessage(`"back_edge"`)
	edges = append(edges, back)
	raw, _ := json.Marshal(edges)
	m["edges"] = raw
	out, _ := json.Marshal(m)
	return string(out)
}

func injectSelfEdge(t *testing.T, base json.RawMessage) string {
	t.Helper()
	agentID := "d44ce000-0000-4000-8000-000000000004"
	return setKey(t, base, "edges", `[{
		"bindingId":"a11ce000-0000-4000-8000-0000000000e5",
		"callerAgentId":"`+agentID+`","targetAgentId":"`+agentID+`",
		"callableName":"self","mode":"TASK","contextPolicy":"TASK_ONLY",
		"version":1,"protocol":"INTERNAL"
	}]`)
}

func injectCapRelease(t *testing.T, base json.RawMessage, release map[string]any) string {
	t.Helper()
	relRaw, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	cap := `{"schemaVersion":"capability-snapshot.v1","releases":[` + string(relRaw) + `]}`
	return mutateNodeNested(t, base, "capabilitySnapshot", cap)
}

// TestSnapshotJSON_ProducerSelfRoundTrip ensures SnapshotJSON output of a
// contract-valid graph re-parses (producer self-validation).
func TestSnapshotJSON_ProducerSelfRoundTrip(t *testing.T) {
	agentID := "d44ce000-0000-4000-8000-000000000004"
	childID := "d44ce000-0000-4000-8000-000000000005"
	modelID := "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	snap := agentdelegation.GraphSnapshotV1{
		SchemaVersion: agentdelegation.GraphSnapshotSchemaV1,
		RootAgentID:   agentID,
		MaxDepth:      2, MaxTotal: 8, MaxPerBinding: 3,
		Nodes: []agentdelegation.GraphNodeSnapshot{
			{AgentID: agentID, ModelConfigID: modelID, ModelConfigLockVer: 1, Depth: 0,
				ModelSnapshot:      producerNodeModelSnap(modelID),
				AgentSnapshot:      producerNodeAgentSnap(agentID, modelID),
				CapabilitySnapshot: producerNodeCapSnap()},
			{AgentID: childID, ModelConfigID: modelID, ModelConfigLockVer: 1, Depth: 1,
				ModelSnapshot:      producerNodeModelSnap(modelID),
				AgentSnapshot:      producerNodeAgentSnap(childID, modelID),
				CapabilitySnapshot: producerNodeCapSnap()},
		},
		Edges: []agentdelegation.GraphEdgeSnapshot{{
			BindingID:     "a11ce000-0000-4000-8000-0000000000b1",
			CallerAgentID: agentID, TargetAgentID: childID,
			CallableName: "call_child", Mode: agentdelegation.ModeTask,
			ContextPolicy: agentdelegation.ContextTaskOnly, Version: 1,
			Protocol: agentdelegation.ProtocolInternal,
		}},
		FrozenRemotesByCaller: map[string][]agentdelegation.FrozenRemoteBinding{
			agentID: {{
				ID: "b22ce000-0000-4000-8000-0000000000a1", CallerAgentID: agentID,
				CallableName: "remote_call", EndpointURL: "https://1.1.1.1/a2a",
				AllowedHosts: []string{"1.1.1.1"}, TimeoutMs: 5000, Version: 1,
			}},
			childID: {},
		},
		RemotesFrozen: true,
		BuiltAt:       time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}
	raw, err := agentdelegation.SnapshotJSON(snap)
	if err != nil {
		t.Fatal(err)
	}
	got, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, raw)
	if err != nil || got == nil {
		t.Fatalf("producer round-trip failed: %v", err)
	}
	if len(got.Edges) != 1 || len(got.FrozenRemotesByCaller[agentID]) != 1 {
		t.Fatalf("unexpected topology edges=%d remotes=%d", len(got.Edges), len(got.FrozenRemotesByCaller[agentID]))
	}
}
