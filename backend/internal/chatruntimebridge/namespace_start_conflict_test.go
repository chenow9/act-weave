package chatruntimebridge

import (
	"strings"
	"testing"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
)

// Residual #12: start/attach path rejects combined internal+remote+capability
// callable_name conflicts (fail closed before tool dispatch).
func TestAssertCombinedCallableNamespace_StartPathConflicts(t *testing.T) {
	t.Parallel()

	// Capability vs internal binding.
	names := map[string]string{"lookup": "capability"}
	err := assertCombinedCallableNamespace(names, "A", []agentdelegation.GraphEdgeSnapshot{
		{CallerAgentID: "A", CallableName: "lookup", BindingID: "b1"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "callable_name conflict") {
		t.Fatalf("capability vs internal: %v", err)
	}

	// Internal vs remote.
	names = map[string]string{}
	err = assertCombinedCallableNamespace(names, "A",
		[]agentdelegation.GraphEdgeSnapshot{
			{CallerAgentID: "A", CallableName: "call_b", BindingID: "b2"},
		},
		[]a2agateway.RemoteBinding{{ID: "r1", CallableName: "call_b"}},
	)
	if err == nil || !strings.Contains(err.Error(), "remote") {
		t.Fatalf("internal vs remote: %v", err)
	}

	// Distinct names succeed and fill the map.
	names = map[string]string{"cap_a": "capability"}
	err = assertCombinedCallableNamespace(names, "A",
		[]agentdelegation.GraphEdgeSnapshot{
			{CallerAgentID: "A", CallableName: "call_b", BindingID: "b3"},
			{CallerAgentID: "B", CallableName: "call_b", BindingID: "b-other"}, // other caller ignored
		},
		[]a2agateway.RemoteBinding{{ID: "r2", CallableName: "remote_x"}},
	)
	if err != nil {
		t.Fatalf("distinct names: %v", err)
	}
	if names["call_b"] != "internal_binding" || names["remote_x"] != "a2a_remote" {
		t.Fatalf("map not updated: %v", names)
	}
}
