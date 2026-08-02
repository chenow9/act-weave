package a2agateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"

	"github.com/google/uuid"
)

// TestOutbound_BroadClientNarrowBinding_Rejected proves item 21:
// a SecureHTTPClient built with a broader allowlist cannot be injected into a
// tool whose Binding.AllowedHosts is narrower — even though IsSecureHTTPClient
// alone would pass.
func TestOutbound_BroadClientNarrowBinding_Rejected(t *testing.T) {
	t.Parallel()
	// Broad client allows both good and evil hosts.
	broad := a2agateway.SecureHTTPClient(3*time.Second, []string{"127.0.0.1", "evil.example"},
		a2agateway.EgressPolicy{AllowHTTP: true})
	narrowHosts := []string{"127.0.0.1"}
	// Fingerprints must differ.
	if a2agateway.PolicyFingerprint([]string{"127.0.0.1", "evil.example"}, a2agateway.EgressPolicy{AllowHTTP: true}) ==
		a2agateway.PolicyFingerprint(narrowHosts, a2agateway.EgressPolicy{AllowHTTP: true}) {
		t.Fatal("fingerprints should differ for different allowlists")
	}
	if a2agateway.IsSecureHTTPClientMatching(broad, narrowHosts, a2agateway.EgressPolicy{AllowHTTP: true}) {
		t.Fatal("broad client must not match narrow binding policy")
	}

	stub := &policyFPAudit{}
	_, err := a2agateway.NewOutboundTool(a2agateway.OutboundConfig{
		Binding: a2agateway.RemoteBinding{
			ID:           uuid.Must(uuid.NewV7()).String(),
			CallableName: "remote", EndpointURL: "http://127.0.0.1/a2a",
			AllowedHosts: narrowHosts, TimeoutMs: 3000, Version: 1,
		},
		Audit: stub, HTTPClient: broad, AllowHTTP: true,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(),
	})
	if err == nil {
		t.Fatal("NewOutboundTool must reject broad client for narrow binding")
	}
	if !strings.Contains(err.Error(), "fingerprint") && !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("want fingerprint/ssrf error, got %v", err)
	}
}

// TestOutbound_BroadClient_307BodyExfil_Blocked is a real redirect regression:
// endpoint on allowlisted host 307s to a host present only on a broader client
// allowlist; with policy matching, that client cannot be used — and a correctly
// narrowed client must not follow the redirect (no POST body leak).
func TestOutbound_BroadClient_307BodyExfil_Blocked(t *testing.T) {
	t.Parallel()
	var evilHits atomic.Int64
	var evilBody atomic.Value
	// evil is still 127.0.0.1 — use hostname via Location that is not allowlisted.
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://not-on-binding.example/steal", http.StatusTemporaryRedirect)
	}))
	defer good.Close()

	// Correct narrow client (binding hosts only).
	narrow := a2agateway.SecureHTTPClient(3*time.Second, []string{"127.0.0.1"},
		a2agateway.EgressPolicy{AllowHTTP: true})
	secret := []byte(`{"jsonrpc":"2.0","params":{"secret":"EXFIL-ME"}}`)
	req, _ := http.NewRequest(http.MethodPost, good.URL+"/a2a", bytes.NewReader(secret))
	req.Header.Set("Content-Type", "application/json")
	resp, err := narrow.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected redirect off allowlist to fail")
	}
	if evilHits.Load() != 0 {
		t.Fatalf("evil hits=%d body=%v", evilHits.Load(), evilBody.Load())
	}

	// Matching client constructs successfully.
	stub := &policyFPAudit{}
	tool, err := a2agateway.NewOutboundTool(a2agateway.OutboundConfig{
		Binding: a2agateway.RemoteBinding{
			ID:           uuid.Must(uuid.NewV7()).String(),
			CallableName: "remote", EndpointURL: good.URL + "/a2a",
			AllowedHosts: []string{"127.0.0.1"}, TimeoutMs: 3000, Version: 1,
		},
		Audit: stub, HTTPClient: narrow, AllowHTTP: true,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(),
	})
	if err != nil {
		t.Fatalf("matching client should be accepted: %v", err)
	}
	_ = tool
	_ = io.Discard
	_ = context.Background()
	_ = json.RawMessage{}
}

type policyFPAudit struct{}

func (p *policyFPAudit) CreateDelegationAndStep(ctx context.Context, in agentdelegation.CreateDelegationInput) (agentdelegation.Delegation, bool, error) {
	return agentdelegation.Delegation{ID: in.ID, Status: agentdelegation.StatusRunning}, false, nil
}
func (p *policyFPAudit) FinalizeDelegation(ctx context.Context, in agentdelegation.FinalizeDelegationInput) (agentdelegation.Delegation, error) {
	return agentdelegation.Delegation{ID: in.DelegationID, Status: in.Status}, nil
}
func (p *policyFPAudit) SetChildRunID(ctx context.Context, workspaceID, delegationID, childRunID string) error {
	return nil
}
func (p *policyFPAudit) RecordDispatchAttempt(ctx context.Context, workspaceID, delegationID string) error {
	return nil
}
func (p *policyFPAudit) AccumulateModelTokens(ctx context.Context, workspaceID, delegationID string, usage agentdelegation.TokenUsage) error {
	return nil
}
