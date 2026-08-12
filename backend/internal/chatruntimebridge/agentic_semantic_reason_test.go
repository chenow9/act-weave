package chatruntimebridge_test

import (
	"strings"
	"testing"

	"actweave/backend/internal/agentdelegation"
)

// TestAgenticSemanticCellsRejectForTheirOwnInvariant is the reason-level
// companion to the semantic_* rows of TestAgenticInitial_StrictRawSnapshotMatrix.
//
// Bridge.Execute collapses every graph rejection into the single code
// AGENTIC_GRAPH_SNAPSHOT_REQUIRED, so the matrix cannot tell "the self-edge
// check fired" from "the fixture's bindingId was not hex and the parser gave up
// three checks earlier". That is exactly how the R11-5 remote fixtures, the
// self_edge cell and the orphan_node cell all stayed green while covering
// nothing.
//
// These cells build their graphs with the same helpers the matrix uses, so a
// fixture that drifts back into being rejected by an earlier gate fails here
// with the real parse error instead of passing silently next door.
func TestAgenticSemanticCellsRejectForTheirOwnInvariant(t *testing.T) {
	base := newAgenticFixture(t, nil)
	goodGraph := string(base.run.AgentGraphSnapshot)

	// Positive control: the unmutated fixture parses, so every rejection below
	// is caused by the single mutation the cell applies and not by the fixture.
	if _, err := agentdelegation.ParseSnapshot(testWSUUID, []byte(goodGraph)); err != nil {
		t.Fatalf("baseline graph fixture must parse: %v", err)
	}

	for _, tc := range []struct {
		name       string
		graph      string
		wantReason string
	}{
		{
			name:       "semantic_self_edge",
			graph:      injectGraphSelfEdge(t, goodGraph),
			wantReason: "edges[0] self-edge forbidden",
		},
		{
			name:       "semantic_orphan_node",
			graph:      injectGraphOrphan(t, goodGraph),
			wantReason: "not reachable from root",
		},
		{
			name:       "semantic_cycle",
			graph:      injectGraphCycle(t, base),
			wantReason: "graph cycle detected",
		},
		{
			name:       "semantic_root_depth_nonzero",
			graph:      setGraphNodeDepth(t, goodGraph, 0, 1),
			wantReason: "root node depth must be 0",
		},
		{
			name: "semantic_child_depth_mismatch",
			graph: setGraphNodeDepth(t,
				injectValidEdgeThenMutate(t, base, "protocol", `"INTERNAL"`), 1, 0),
			wantReason: "depth 0 != shortest-path depth 1",
		},
		{
			name:       "semantic_empty_api_base",
			graph:      mutateGraphNodeNestedKey(t, goodGraph, "modelSnapshot", "apiBase", `""`, false),
			wantReason: "nodes[0].modelSnapshot.apiBase invalid",
		},
		{
			name:       "semantic_edge_protocol_a2a",
			graph:      injectValidEdgeThenMutate(t, base, "protocol", `"A2A"`),
			wantReason: `edges[0].protocol "A2A" not in closed enum`,
		},
		{
			name:       "semantic_edge_context_summary",
			graph:      injectValidEdgeThenMutate(t, base, "contextPolicy", `"SUMMARY"`),
			wantReason: `edges[0].contextPolicy "SUMMARY" not in closed enum`,
		},
		{
			name:       "semantic_edge_context_selected",
			graph:      injectValidEdgeThenMutate(t, base, "contextPolicy", `"SELECTED_MESSAGES"`),
			wantReason: `edges[0].contextPolicy "SELECTED_MESSAGES" not in closed enum`,
		},
		{
			name: "semantic_cap_forged_kind",
			graph: injectGraphCapRelease(t, goodGraph, "FORGED_KIND", "LOW", "NONE",
				"c33ce000-0000-4000-8000-0000000000c1"),
			wantReason: `releases[0].kind "FORGED_KIND" not in closed domain`,
		},
		{
			name: "semantic_cap_forged_risk",
			graph: injectGraphCapRelease(t, goodGraph, "TOOL", "SUPER", "NONE",
				"c33ce000-0000-4000-8000-0000000000c1"),
			wantReason: `releases[0].riskLevel "SUPER" not in closed domain`,
		},
		{
			name: "semantic_cap_forged_side_effect",
			graph: injectGraphCapRelease(t, goodGraph, "TOOL", "LOW", "DESTROY",
				"c33ce000-0000-4000-8000-0000000000c1"),
			wantReason: `releases[0].sideEffectLevel "DESTROY" not in closed domain`,
		},
		{
			// non-canonical UUID fixture: uppercase is the invariant under test.
			name: "semantic_cap_noncanonical_id",
			graph: injectGraphCapRelease(t, goodGraph, "TOOL", "LOW", "NONE",
				"C33CE000-0000-4000-8000-0000000000C1"),
			wantReason: "releases[0].capabilityId must be canonical UUID",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := agentdelegation.ParseSnapshot(testWSUUID, []byte(tc.graph))
			if err == nil {
				t.Fatal("mutated graph must be rejected by snapshot parsing")
			}
			if !strings.Contains(err.Error(), tc.wantReason) {
				t.Fatalf("cell was rejected for the wrong invariant:\n got: %v\nwant substring: %q",
					err, tc.wantReason)
			}
		})
	}
}
