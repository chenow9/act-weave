package agentdelegation_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentdelegation"
)

const (
	remotePolicyAgentID = "d44ce000-0000-4000-8000-000000000004"
	remotePolicyModelID = "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1"
)

func remotePolicyGraph(t *testing.T, remote agentdelegation.FrozenRemoteBinding) json.RawMessage {
	t.Helper()
	snap := agentdelegation.GraphSnapshotV1{
		SchemaVersion: agentdelegation.GraphSnapshotSchemaV1,
		RootAgentID:   remotePolicyAgentID,
		MaxDepth:      2, MaxTotal: 8, MaxPerBinding: 3,
		Nodes: []agentdelegation.GraphNodeSnapshot{{
			AgentID: remotePolicyAgentID, ModelConfigID: remotePolicyModelID, ModelConfigLockVer: 1,
			ModelSnapshot:      producerNodeModelSnap(remotePolicyModelID),
			AgentSnapshot:      producerNodeAgentSnap(remotePolicyAgentID, remotePolicyModelID),
			CapabilitySnapshot: producerNodeCapSnap(), Depth: 0,
		}},
		Edges: []agentdelegation.GraphEdgeSnapshot{},
		FrozenRemotesByCaller: map[string][]agentdelegation.FrozenRemoteBinding{
			remotePolicyAgentID: {remote},
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

func baseRemote() agentdelegation.FrozenRemoteBinding {
	return agentdelegation.FrozenRemoteBinding{
		ID: "b22ce000-0000-4000-8000-0000000000a1", CallerAgentID: remotePolicyAgentID,
		CallableName: "remote_call",
		EndpointURL:  "https://partner.example.com/a2a",
		AllowedHosts: []string{"partner.example.com"},
		TimeoutMs:    5000, Version: 1,
	}
}

// ParseSnapshot documents itself as pure frozen-document validation that runs
// before anything is constructed, so it must not emit network traffic for
// attacker-supplied host names (no cancellation, no timeout, no rate limit —
// a DoS amplifier). A host that provably cannot resolve must still parse:
// .invalid is reserved by RFC 2606 and has no DNS answer anywhere, so if the
// resolve layer ever leaks back into parsing this test fails.
func TestParseSnapshot_RemotePolicyIsSyntaxOnlyNoDNS(t *testing.T) {
	rem := baseRemote()
	rem.EndpointURL = "https://never-resolves.invalid/a2a"
	rem.AllowedHosts = []string{"never-resolves.invalid"}

	start := time.Now()
	snap, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, remotePolicyGraph(t, rem))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("syntax-only remote policy must accept an unresolvable but well-formed host: %v", err)
	}
	if snap == nil {
		t.Fatal("snapshot parsed as absent")
	}
	// A real lookup for .invalid costs at least a resolver round trip; the syntax
	// layer is pure string work.
	if elapsed > 250*time.Millisecond {
		t.Fatalf("parse took %v — that is resolver latency, not syntax validation", elapsed)
	}

	// The syntax layer still enforces the full non-DNS policy set.
	for name, mutate := range map[string]func(*agentdelegation.FrozenRemoteBinding){
		"http_scheme": func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL = "http://partner.example.com/a2a"
		},
		"userinfo": func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL = "https://user:pw@partner.example.com/a2a"
		},
		"host_not_in_allowlist": func(r *agentdelegation.FrozenRemoteBinding) {
			r.AllowedHosts = []string{"other.example.com"}
		},
		"empty_allowlist": func(r *agentdelegation.FrozenRemoteBinding) {
			r.AllowedHosts = nil
		},
		"literal_private_ip": func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL = "https://169.254.169.254/latest/meta-data"
			r.AllowedHosts = []string{"169.254.169.254"}
		},
		"loopback_literal": func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL = "https://127.0.0.1/a2a"
			r.AllowedHosts = []string{"127.0.0.1"}
		},
		// An explicitly allowlisted blocked name is deliberately still accepted by
		// the syntax layer (a2agateway.CreateRemote has always let an operator
		// allowlist an internal name); it is the resolve layer that refuses the
		// link-local answer at dial time. What must never pass is a blocked name
		// that the allowlist does not cover.
		"blocked_metadata_name_not_allowlisted": func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL = "https://metadata.google.internal/computeMetadata/v1"
		},
		"internal_suffix_not_allowlisted": func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL = "https://vault.internal/secret"
		},
		"localhost_not_allowlisted": func(r *agentdelegation.FrozenRemoteBinding) {
			r.EndpointURL = "https://localhost/a2a"
		},
		"agent_card_file_scheme": func(r *agentdelegation.FrozenRemoteBinding) {
			r.AgentCardURL = "file:///etc/passwd"
		},
	} {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			hostile := baseRemote()
			mutate(&hostile)
			if _, err := agentdelegation.ParseSnapshot(
				testFreezeWorkspaceID, remotePolicyGraph(t, hostile),
			); err == nil {
				t.Fatal("expected fail closed")
			}
		})
	}
}

// A frozen graph must not be able to reach another tenant's secret. The write
// path (a2agateway.CreateRemote) always checks the ref against the owning
// workspace, so the freeze consumer has to do the same with a real workspace id
// rather than an empty string that silently disabled the check.
func TestParseSnapshot_AuthSecretRefBoundToRunWorkspace(t *testing.T) {
	const otherWorkspace = "a11ce000-0000-4000-8000-0000000000ff"
	const secretID = "e55ce000-0000-4000-8000-000000000005"

	own := baseRemote()
	own.AuthSecretRef = "secret:" + testFreezeWorkspaceID + ":" + secretID
	if _, err := agentdelegation.ParseSnapshot(
		testFreezeWorkspaceID, remotePolicyGraph(t, own),
	); err != nil {
		t.Fatalf("same-workspace secret ref must be accepted: %v", err)
	}

	foreign := baseRemote()
	foreign.AuthSecretRef = "secret:" + otherWorkspace + ":" + secretID
	if _, err := agentdelegation.ParseSnapshot(
		testFreezeWorkspaceID, remotePolicyGraph(t, foreign),
	); err == nil {
		t.Fatal("cross-workspace secret ref must fail closed")
	}

	// An unavailable or malformed workspace binding must fail closed for any
	// non-empty ref instead of degrading to "no tenant check".
	for _, ws := range []string{"", "   ", "not-a-uuid", "{" + testFreezeWorkspaceID + "}"} {
		if _, err := agentdelegation.ParseSnapshot(ws, remotePolicyGraph(t, own)); err == nil {
			t.Fatalf("workspace %q must fail closed", ws)
		}
	}

	// A malformed ref shape is rejected and never echoed back.
	malformed := baseRemote()
	malformed.AuthSecretRef = "secret:" + testFreezeWorkspaceID + ":not-a-uuid"
	_, err := agentdelegation.ParseSnapshot(testFreezeWorkspaceID, remotePolicyGraph(t, malformed))
	if err == nil {
		t.Fatal("malformed secret ref must fail closed")
	}
	if strings.Contains(err.Error(), secretID) {
		t.Fatalf("error echoed the secret id: %v", err)
	}

	// A remote with no outbound auth stays valid.
	none := baseRemote()
	if _, err := agentdelegation.ParseSnapshot(
		testFreezeWorkspaceID, remotePolicyGraph(t, none),
	); err != nil {
		t.Fatalf("absent secret ref must be accepted: %v", err)
	}
}

// ParseSnapshot takes no context by design (it must never block on I/O), so the
// caller's cancellation cannot be honoured — which is only safe because there
// is nothing to cancel. Parsing an already-cancelled request must still work.
func TestParseSnapshot_UnaffectedByCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = ctx
	if _, err := agentdelegation.ParseSnapshot(
		testFreezeWorkspaceID, remotePolicyGraph(t, baseRemote()),
	); err != nil {
		t.Fatalf("parse must be pure and independent of request lifetime: %v", err)
	}
}
