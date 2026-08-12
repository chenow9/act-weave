package agentdelegation_test

import (
	"encoding/json"
	"strings"
	"testing"

	"actweave/backend/internal/agentdelegation"
)

// TestParseSnapshot_NegativeZeroRejected covers the two integer fields where
// zero is a legal value, so "-0" was not masked by a domain check the way the
// three lock fields are: node depth (the root's depth is 0) and remote
// timeoutMs (only negatives are rejected).
//
// strictjson.DecodeInt64Exact returned (0, nil) for "-0", so the frozen
// snapshot admitted two byte sequences for one value at a boundary whose entire
// purpose is that it does not.
func TestParseSnapshot_NegativeZeroRejected(t *testing.T) {
	base := validEmptyGraph(t)
	remoteBase := validRemoteGraph(t)

	// ParseSnapshot collapses the primitive's error into a per-field message, so
	// the field name is the strongest attribution available here. The paired
	// positive controls below carry the rest: the same value spelled "0" is
	// accepted, which is what makes these rejections about the encoding.
	for _, tc := range []struct {
		name       string
		raw        string
		wantReason string
	}{
		{"node_depth", mutateNodeField(t, base, "depth", `-0`), "nodes[0].depth invalid"},
		{"remote_timeout_ms", mutateRemoteField(t, remoteBase, "timeoutMs", `-0`), ".timeoutMs invalid"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.raw, `-0`) {
				t.Fatalf("mutation did not write -0: %s", tc.raw)
			}
			_, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, json.RawMessage(tc.raw))
			if err == nil {
				t.Fatal("-0 is a second encoding of a legal value and must fail closed")
			}
			if !strings.Contains(err.Error(), tc.wantReason) {
				t.Fatalf("rejection must name the field, got: %v", err)
			}
		})
	}

	// Positive controls: the canonical spelling of the same value parses, so
	// the rejections above are about the encoding and not about zero.
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"node_depth_zero", mutateNodeField(t, base, "depth", `0`)},
		{"remote_timeout_ms_zero", mutateRemoteField(t, remoteBase, "timeoutMs", `0`)},
	} {
		tc := tc
		t.Run(tc.name+"_accepted", func(t *testing.T) {
			if _, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, json.RawMessage(tc.raw)); err != nil {
				t.Fatalf("canonical zero must still be accepted: %v", err)
			}
		})
	}
}
