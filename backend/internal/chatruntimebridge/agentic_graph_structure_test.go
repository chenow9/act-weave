package chatruntimebridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/chatruntimebridge"
)

// Bridge-level structure and remote-policy coverage for the frozen graph.
//
// The parser package owns most of these shapes, but Bridge.Execute is where a
// gap actually costs something: the parser returning an error is only useful if
// the initial path treats it as fail-closed rather than falling back to live
// topology. Task 5 lifted AGENTIC_DELEGATION_MIGRATION_PENDING: valid remotes
// with Audit reach attach; missing Audit fails closed with "Audit required".
//
// Every remote here is built by one shared producer-shaped builder. That
// pairing makes rejections meaningful: the builder emits remotes the parser
// accepts, so a rejection can only come from the field under test.

const (
	structRemoteID     = "b22ce000-0000-4000-8000-0000000000a1"
	structOtherAgentID = "d44ce000-0000-4000-8000-0000000000bb"
	structSecretID     = "e55ce000-0000-4000-8000-00000000005a"
)

// validFrozenRemote is a frozen A2A remote that satisfies every policy the
// freeze consumer enforces: HTTPS, no userinfo, host covered by the allowlist,
// a legal agent card on the same host, and a secret reference owned by the
// run's own workspace.
func validFrozenRemote() agentdelegation.FrozenRemoteBinding {
	return agentdelegation.FrozenRemoteBinding{
		ID:            structRemoteID,
		CallerAgentID: testAgentUUID,
		CallableName:  "partner_call",
		EndpointURL:   "https://partner.example.com/a2a",
		AgentCardURL:  "https://partner.example.com/.well-known/agent.json",
		AllowedHosts:  []string{"partner.example.com"},
		AuthSecretRef: "secret:" + testWSUUID + ":" + structSecretID,
		TimeoutMs:     5000,
		Version:       1,
	}
}

// graphWithRemotes builds the same explicit-empty root graph the producer emits
// for a root chat run, except that the root caller carries the given remotes.
func graphWithRemotes(t *testing.T, remotes ...agentdelegation.FrozenRemoteBinding) json.RawMessage {
	t.Helper()
	list := remotes
	if list == nil {
		list = []agentdelegation.FrozenRemoteBinding{}
	}
	snap := agentdelegation.GraphSnapshotV1{
		SchemaVersion: agentdelegation.GraphSnapshotSchemaV1,
		RootAgentID:   testAgentUUID,
		MaxDepth:      agentdelegation.DefaultMaxDepth,
		MaxTotal:      agentdelegation.DefaultMaxTotalDelegations,
		MaxPerBinding: agentdelegation.DefaultMaxPerBinding,
		Nodes: []agentdelegation.GraphNodeSnapshot{{
			AgentID:            testAgentUUID,
			ModelConfigID:      testModelUUID,
			ModelConfigLockVer: testModelLockVersion,
			ModelSnapshot:      producerNodeModelSnap(testModelUUID),
			AgentSnapshot:      producerNodeAgentSnap(testAgentUUID, testModelUUID),
			CapabilitySnapshot: emptyCapSnap(),
			Depth:              0,
		}},
		Edges:                 []agentdelegation.GraphEdgeSnapshot{},
		FrozenRemotesByCaller: map[string][]agentdelegation.FrozenRemoteBinding{testAgentUUID: list},
		RemotesFrozen:         true,
		BuiltAt:               time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}
	raw, err := agentdelegation.SnapshotJSON(snap)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestAgenticInitial_PolicyValidRemoteRequiresDelegationAudit is the G8 positive
// control after Task 5: a policy-valid frozen remote is no longer "migration
// pending" — it is real wiring. Without DelegationDeps.Audit the attach path
// fails closed before any model construction; that is what proves the freeze
// was accepted as topology (not corrupt) and then refused for missing wiring.
func TestAgenticInitial_PolicyValidRemoteRequiresDelegationAudit(t *testing.T) {
	assertAuditRequired := func(t *testing.T, f *agenticFixture, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("a frozen remote without Audit must stop the initial path")
		}
		if errors.Is(err, chatruntimebridge.ErrAgenticGraphSnapshotRequired) {
			t.Fatalf("a policy-valid remote must not be reported as a corrupt freeze: %v", err)
		}
		if !strings.Contains(err.Error(), "Audit required") {
			t.Fatalf("err=%v want Audit required", err)
		}
		if f.agentic.calls.Load() != 0 || f.classic.calls.Load() != 0 ||
			f.mdl.calls.Load() != 0 || f.sinks.opens.Load() != 0 ||
			f.assemblies.inserts.Load() != 0 {
			t.Fatalf("side effects agentic=%d classic=%d model=%d sink=%d assembly=%d",
				f.agentic.calls.Load(), f.classic.calls.Load(), f.mdl.calls.Load(),
				f.sinks.opens.Load(), f.assemblies.inserts.Load())
		}
	}

	t.Run("fully_specified_remote", func(t *testing.T) {
		rem := validFrozenRemote()
		graph := graphWithRemotes(t, rem)
		if _, err := agentdelegation.ParseSnapshot(testWSUUID, graph); err != nil {
			t.Fatalf("the valid-remote fixture must parse: %v", err)
		}
		f := newAgenticFixture(t, func(f *agenticFixture) { f.run.AgentGraphSnapshot = graph })
		assertAuditRequired(t, f, f.bridge(t).Execute(context.Background(), f.job()))
	})

	t.Run("minimal_remote_no_card_no_secret", func(t *testing.T) {
		rem := validFrozenRemote()
		rem.AgentCardURL = ""
		rem.AuthSecretRef = ""
		f := newAgenticFixture(t, func(f *agenticFixture) {
			f.run.AgentGraphSnapshot = graphWithRemotes(t, rem)
		})
		assertAuditRequired(t, f, f.bridge(t).Execute(context.Background(), f.job()))
	})

	t.Run("wildcard_allowlist_entry", func(t *testing.T) {
		rem := validFrozenRemote()
		rem.AllowedHosts = []string{"*.example.com"}
		f := newAgenticFixture(t, func(f *agenticFixture) {
			f.run.AgentGraphSnapshot = graphWithRemotes(t, rem)
		})
		assertAuditRequired(t, f, f.bridge(t).Execute(context.Background(), f.job()))
	})

	t.Run("public_literal_ip_endpoint", func(t *testing.T) {
		rem := validFrozenRemote()
		rem.EndpointURL = "https://1.1.1.1/a2a"
		rem.AgentCardURL = ""
		rem.AllowedHosts = []string{"1.1.1.1"}
		f := newAgenticFixture(t, func(f *agenticFixture) {
			f.run.AgentGraphSnapshot = graphWithRemotes(t, rem)
		})
		assertAuditRequired(t, f, f.bridge(t).Execute(context.Background(), f.job()))
	})

	t.Run("zero_remotes_still_executes", func(t *testing.T) {
		f := newAgenticFixture(t, func(f *agenticFixture) {
			f.run.AgentGraphSnapshot = graphWithRemotes(t)
		})
		if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
			t.Fatalf("empty remote list must proceed: %v", err)
		}
		if f.agentic.calls.Load() != 1 || f.classic.calls.Load() != 0 {
			t.Fatalf("agentic=%d classic=%d", f.agentic.calls.Load(), f.classic.calls.Load())
		}
	})
}

// TestAgenticInitial_RemotePolicyRejectionsAtBridge is the G7 rejection face.
// Each row starts from the valid remote (HTTPS + allowlist + secret ownership)
// and changes one policy-relevant field. The rejection must be the
// corrupt-freeze family — never treated as a soft deferral that smuggles a
// hostile endpoint past the freeze contract.
func TestAgenticInitial_RemotePolicyRejectionsAtBridge(t *testing.T) {
	rows := []struct {
		name   string
		mutate func(*agentdelegation.FrozenRemoteBinding)
	}{
		{"private_10_8", func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL, r.AllowedHosts = "https://10.0.0.5/a2a", []string{"10.0.0.5"}
		}},
		{"private_192_168", func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL, r.AllowedHosts = "https://192.168.1.10/a2a", []string{"192.168.1.10"}
		}},
		{"private_172_16", func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL, r.AllowedHosts = "https://172.16.0.1/a2a", []string{"172.16.0.1"}
		}},
		{"loopback_ipv4", func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL, r.AllowedHosts = "https://127.0.0.1/a2a", []string{"127.0.0.1"}
		}},
		{"loopback_ipv6", func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL, r.AllowedHosts = "https://[::1]/a2a", []string{"::1"}
		}},
		{"cloud_metadata_link_local", func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL = "https://169.254.169.254/latest/meta-data/iam/security-credentials"
			r.AllowedHosts = []string{"169.254.169.254"}
		}},
		{"cgnat_100_64", func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL, r.AllowedHosts = "https://100.64.0.1/a2a", []string{"100.64.0.1"}
		}},
		{"cgnat_100_127_upper_bound", func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL, r.AllowedHosts = "https://100.127.255.254/a2a", []string{"100.127.255.254"}
		}},
		{"ipv6_unique_local_fd00", func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL, r.AllowedHosts = "https://[fd00::1]/a2a", []string{"fd00::1"}
		}},
		{"ipv6_link_local_fe80", func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL, r.AllowedHosts = "https://[fe80::1]/a2a", []string{"fe80::1"}
		}},
		{"unspecified_0_0_0_0", func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL, r.AllowedHosts = "https://0.0.0.0/a2a", []string{"0.0.0.0"}
		}},
		{"allowlist_only_whitespace", func(r *agentdelegation.FrozenRemoteBinding) {
			r.AllowedHosts = []string{"   "}
		}},
		{"allowlist_blank_and_padded_entries", func(r *agentdelegation.FrozenRemoteBinding) {
			r.AllowedHosts = []string{" partner.example.com ", ""}
		}},
		{"allowlist_empty_string_entry", func(r *agentdelegation.FrozenRemoteBinding) {
			r.AllowedHosts = []string{"partner.example.com", ""}
		}},
		{"agent_card_host_outside_allowlist", func(r *agentdelegation.FrozenRemoteBinding) {
			r.AgentCardURL = "https://attacker.example.net/.well-known/agent.json"
		}},
		{"agent_card_private_ip", func(r *agentdelegation.FrozenRemoteBinding) {
			r.AgentCardURL = "https://10.1.2.3/.well-known/agent.json"
		}},
		{"agent_card_http_scheme", func(r *agentdelegation.FrozenRemoteBinding) {
			r.AgentCardURL = "http://partner.example.com/.well-known/agent.json"
		}},
		{"agent_card_userinfo", func(r *agentdelegation.FrozenRemoteBinding) {
			r.AgentCardURL = "https://u:p@partner.example.com/.well-known/agent.json"
		}},
		{"secret_ref_other_workspace", func(r *agentdelegation.FrozenRemoteBinding) {
			r.AuthSecretRef = "secret:a11ce000-0000-4000-8000-0000000000ff:" + structSecretID
		}},
		{"endpoint_host_outside_allowlist", func(r *agentdelegation.FrozenRemoteBinding) {
			r.AllowedHosts = []string{"other.example.com"}
		}},
	}

	converted := make([]runLevelRow, 0, len(rows))
	for _, tc := range rows {
		tc := tc
		converted = append(converted, runLevelRow{
			name: tc.name,
			mutate: func(t *testing.T, f *agenticFixture) {
				rem := validFrozenRemote()
				tc.mutate(&rem)
				// Effectiveness guard: the row must actually differ from the
				// valid remote, otherwise it would assert a rejection that has
				// nothing to do with its name.
				if remoteFingerprint(t, rem) == remoteFingerprint(t, validFrozenRemote()) {
					t.Fatal("row did not change any remote field")
				}
				f.run.AgentGraphSnapshot = graphWithRemotes(t, rem)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		})
	}
	runRunLevelRows(t, converted)
}

func remoteFingerprint(t *testing.T, r agentdelegation.FrozenRemoteBinding) string {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestAgenticInitial_GraphStructureRejectionsAtBridge is the G9 structural face
// at the real entry point.
func TestAgenticInitial_GraphStructureRejectionsAtBridge(t *testing.T) {
	runRunLevelRows(t, []runLevelRow{
		{
			name: "root_agent_id_is_another_agent_but_internally_valid",
			mutate: func(t *testing.T, f *agenticFixture) {
				// Parses cleanly on its own; only the binding to run.AgentID is wrong.
				graph := explicitEmptyAgentGraph(t, structOtherAgentID, testModelUUID, f.run.ModelSnapshot)
				if _, err := agentdelegation.ParseSnapshot(testWSUUID, graph); err != nil {
					t.Fatalf("fixture must be internally valid: %v", err)
				}
				f.run.AgentGraphSnapshot = graph
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "root_agent_id_swapped_leaving_node_behind",
			mutate: func(t *testing.T, f *agenticFixture) {
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "rootAgentId",
					`"`+structOtherAgentID+`"`)
				assertGraphRootField(t, g, "rootAgentId", `"`+structOtherAgentID+`"`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "remotes_frozen_false",
			mutate: func(t *testing.T, f *agenticFixture) {
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "remotesFrozen", `false`)
				assertGraphRootField(t, g, "remotesFrozen", `false`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "remotes_frozen_wrong_type",
			mutate: func(t *testing.T, f *agenticFixture) {
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "remotesFrozen", `"true"`)
				assertGraphRootField(t, g, "remotesFrozen", `"true"`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "max_depth_zero",
			mutate: func(t *testing.T, f *agenticFixture) {
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "maxDepth", `0`)
				assertGraphRootField(t, g, "maxDepth", `0`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "max_depth_negative",
			mutate: func(t *testing.T, f *agenticFixture) {
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "maxDepth", `-1`)
				assertGraphRootField(t, g, "maxDepth", `-1`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "max_total_delegations_zero",
			mutate: func(t *testing.T, f *agenticFixture) {
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "maxTotalDelegations", `0`)
				assertGraphRootField(t, g, "maxTotalDelegations", `0`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "max_per_binding_zero",
			mutate: func(t *testing.T, f *agenticFixture) {
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "maxPerBinding", `0`)
				assertGraphRootField(t, g, "maxPerBinding", `0`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "built_at_garbage",
			mutate: func(t *testing.T, f *agenticFixture) {
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "builtAt", `"not-a-timestamp"`)
				assertGraphRootField(t, g, "builtAt", `"not-a-timestamp"`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "built_at_empty_string",
			mutate: func(t *testing.T, f *agenticFixture) {
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "builtAt", `""`)
				assertGraphRootField(t, g, "builtAt", `""`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "built_at_epoch_number",
			mutate: func(t *testing.T, f *agenticFixture) {
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "builtAt", `1754812800`)
				assertGraphRootField(t, g, "builtAt", `1754812800`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "root_unknown_field",
			mutate: func(t *testing.T, f *agenticFixture) {
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "forgedRootField", `"x"`)
				assertGraphRootField(t, g, "forgedRootField", `"x"`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "root_schema_version_forged",
			mutate: func(t *testing.T, f *agenticFixture) {
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "schemaVersion",
					`"agent_graph_snapshot.v2"`)
				assertGraphRootField(t, g, "schemaVersion", `"agent_graph_snapshot.v2"`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			// Every copy of the model UUID is uppercased, so the document is
			// self-consistent and canonical form is the only defect. Without this
			// row the single-field uppercase rows below could be satisfied by an
			// implementation that merely compares ids, never checking their form.
			name: "model_uuid_uppercase_in_every_copy",
			mutate: func(t *testing.T, f *agenticFixture) {
				upper := strings.ToUpper(testModelUUID)
				g := setGraphNodeKey(t, string(f.run.AgentGraphSnapshot), "modelConfigId", `"`+upper+`"`)
				g = mutateGraphNodeNestedKey(t, g, "modelSnapshot", "id", `"`+upper+`"`, false)
				g = mutateGraphNodeNestedKey(t, g, "agentSnapshot", "modelConfigId", `"`+upper+`"`, false)
				if got := readGraphNodeString(t, json.RawMessage(g), "modelConfigId"); got != upper {
					t.Fatalf("mutation read-back %q", got)
				}
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "agent_uuid_uppercase_in_every_copy",
			mutate: func(t *testing.T, f *agenticFixture) {
				upper := strings.ToUpper(testAgentUUID)
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "rootAgentId", `"`+upper+`"`)
				g = setGraphNodeKey(t, g, "agentId", `"`+upper+`"`)
				g = mutateGraphNodeNestedKey(t, g, "agentSnapshot", "agentId", `"`+upper+`"`, false)
				g = setGraphKey(t, g, "frozenRemotesByCaller", `{"`+upper+`":[]}`)
				assertGraphRootField(t, g, "rootAgentId", `"`+upper+`"`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "node_agent_id_uppercase_uuid",
			mutate: func(t *testing.T, f *agenticFixture) {
				upper := strings.ToUpper(testAgentUUID)
				g := setGraphNodeKey(t, string(f.run.AgentGraphSnapshot), "agentId", `"`+upper+`"`)
				if got := readGraphNodeString(t, json.RawMessage(g), "agentId"); got != upper {
					t.Fatalf("mutation read-back %q", got)
				}
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "node_agent_id_surrounding_whitespace",
			mutate: func(t *testing.T, f *agenticFixture) {
				padded := " " + testAgentUUID + " "
				g := setGraphNodeKey(t, string(f.run.AgentGraphSnapshot), "agentId", `"`+padded+`"`)
				if got := readGraphNodeString(t, json.RawMessage(g), "agentId"); got != padded {
					t.Fatalf("mutation read-back %q", got)
				}
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "node_model_config_id_uppercase_uuid",
			mutate: func(t *testing.T, f *agenticFixture) {
				upper := strings.ToUpper(testModelUUID)
				g := setGraphNodeKey(t, string(f.run.AgentGraphSnapshot), "modelConfigId", `"`+upper+`"`)
				if got := readGraphNodeString(t, json.RawMessage(g), "modelConfigId"); got != upper {
					t.Fatalf("mutation read-back %q", got)
				}
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "node_model_config_id_braced_uuid",
			mutate: func(t *testing.T, f *agenticFixture) {
				braced := "{" + testModelUUID + "}"
				g := setGraphNodeKey(t, string(f.run.AgentGraphSnapshot), "modelConfigId",
					mustJSONString(braced))
				if got := readGraphNodeString(t, json.RawMessage(g), "modelConfigId"); got != braced {
					t.Fatalf("mutation read-back %q", got)
				}
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "root_agent_id_uppercase_uuid",
			mutate: func(t *testing.T, f *agenticFixture) {
				upper := strings.ToUpper(testAgentUUID)
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "rootAgentId", `"`+upper+`"`)
				assertGraphRootField(t, g, "rootAgentId", `"`+upper+`"`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "nodes_empty_array",
			mutate: func(t *testing.T, f *agenticFixture) {
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "nodes", `[]`)
				assertGraphRootField(t, g, "nodes", `[]`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
		{
			name: "frozen_remotes_map_missing_root_caller",
			mutate: func(t *testing.T, f *agenticFixture) {
				g := setGraphKey(t, string(f.run.AgentGraphSnapshot), "frozenRemotesByCaller", `{}`)
				assertGraphRootField(t, g, "frozenRemotesByCaller", `{}`)
				f.run.AgentGraphSnapshot = json.RawMessage(g)
			},
			wantErr: chatruntimebridge.ErrAgenticGraphSnapshotRequired,
		},
	})
}

// assertGraphRootField re-reads a root-level key from the mutated bytes so a
// renamed or reshaped producer field cannot leave the row passing for the wrong
// reason (unknown field rejected instead of the property under test).
func assertGraphRootField(t *testing.T, graph, key, want string) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(graph), &m); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	got, ok := m[key]
	if !ok {
		t.Fatalf("root key %q missing after mutation", key)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(want) {
		t.Fatalf("root key %q read-back = %s want %s", key, got, want)
	}
}
