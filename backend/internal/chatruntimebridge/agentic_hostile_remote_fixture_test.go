package chatruntimebridge_test

import (
	"strings"
	"testing"

	"actweave/backend/internal/agentdelegation"
)

// TestHostileRemoteFixturesExerciseRemotePolicyNotIDValidation is the permanent
// guard for R11-5. The hostile-remote fixtures in the strict matrix previously
// carried remote binding IDs ending in "h1"/"h2", which are not hex, so
// validateRemoteSemantics rejected them at its very first check
// (canonicalUUID) and the egress policy checks that the cases claim to cover
// (endpoint scheme, userinfo, allowlist, agent card, auth secret ref) never ran.
// Both reasons surface to the bridge as the same AGENTIC_GRAPH_SNAPSHOT_REQUIRED
// string, so the matrix stayed green while testing nothing.
//
// This test reads the underlying parse error instead of the bridge's collapsed
// code, and fails if any fixture regresses back to being rejected for its ID.
func TestHostileRemoteFixturesExerciseRemotePolicyNotIDValidation(t *testing.T) {
	base := newAgenticFixture(t, nil)
	goodGraph := string(base.run.AgentGraphSnapshot)

	for _, tc := range []struct {
		name       string
		graph      string
		wantSubstr string
	}{
		{
			name:       "file_scheme",
			graph:      injectHostileRemote(t, goodGraph, "file:///etc/passwd", []string{"1.1.1.1"}, ""),
			wantSubstr: "remote policy",
		},
		{
			name:       "http_scheme",
			graph:      injectHostileRemote(t, goodGraph, "http://1.1.1.1/a2a", []string{"1.1.1.1"}, ""),
			wantSubstr: "remote policy",
		},
		{
			name:       "userinfo",
			graph:      injectHostileRemote(t, goodGraph, "https://user:pass@1.1.1.1/a2a", []string{"1.1.1.1"}, ""),
			wantSubstr: "remote policy",
		},
		{
			name:       "empty_allowlist",
			graph:      injectHostileRemote(t, goodGraph, "https://1.1.1.1/a2a", []string{}, ""),
			wantSubstr: "remote policy",
		},
		{
			name:       "mismatched_allowlist",
			graph:      injectHostileRemote(t, goodGraph, "https://1.1.1.1/a2a", []string{"8.8.8.8"}, ""),
			wantSubstr: "remote policy",
		},
		{
			name:       "agent_card_file",
			graph:      injectHostileRemote(t, goodGraph, "https://1.1.1.1/a2a", []string{"1.1.1.1"}, "file:///tmp/card"),
			wantSubstr: "remote policy",
		},
		{
			name:       "bad_secret_ref",
			graph:      injectHostileRemoteSecret(t, goodGraph, "not-a-secret-ref"),
			wantSubstr: "authSecretRef",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := agentdelegation.ParseSnapshot(testWSUUID, []byte(tc.graph))
			if err == nil {
				t.Fatal("hostile remote fixture must be rejected by snapshot parsing")
			}
			if strings.Contains(err.Error(), "must be canonical UUID") {
				t.Fatalf("fixture is rejected for its remote id, so the policy check never runs: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("rejection reason %q must contain %q", err.Error(), tc.wantSubstr)
			}
		})
	}

	// Positive control: with a policy-clean endpoint the same injected remote
	// parses, which proves the fixture's remote id, caller binding and callable
	// name are all valid and that the rejections above are caused only by the
	// endpoint/secret policy under test.
	if _, err := agentdelegation.ParseSnapshot(testWSUUID,
		[]byte(injectHostileRemote(t, goodGraph, "https://1.1.1.1/a2a", []string{"1.1.1.1"}, ""))); err != nil {
		t.Fatalf("policy-clean remote fixture must parse: %v", err)
	}
}
