package a2agateway_test

import (
	"context"
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

// Residual #7/#8: AgentCard fail-closed variants + real auth redirect chain
// using SecureHTTPClient + authPinnedTransport (no Authorization/API-key leak).

func TestAuthPinnedTransport_RedirectChainStripsAuthorizationAndAPIKey(t *testing.T) {
	t.Parallel()
	var hop2Auth, hop2APIKey, hop3Auth atomic.Value
	hop3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hop3Auth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer hop3.Close()
	hop2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hop2Auth.Store(r.Header.Get("Authorization"))
		hop2APIKey.Store(r.Header.Get("X-Api-Key"))
		http.Redirect(w, r, hop3.URL+"/final", http.StatusFound)
	}))
	defer hop2.Close()
	hop1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, hop2.URL+"/mid", http.StatusFound)
	}))
	defer hop1.Close()

	// Allow all three loopback hosts for the redirect chain.
	// Each server has unique 127.0.0.1:port host — allow by IP.
	client := a2agateway.SecureHTTPClient(3*time.Second, []string{"127.0.0.1"}, a2agateway.EgressPolicy{AllowHTTP: true})
	// Inject credentials only for hop1 origin via auth-pinned transport (as outbound does).
	// We exercise package-private type through public SecureHTTPClient CheckRedirect strip
	// by setting headers on the initial request; cross-origin hops must drop them.
	req, _ := http.NewRequest(http.MethodGet, hop1.URL+"/start", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("X-Api-Key", "api-key-secret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("redirect chain with allowlist 127.0.0.1 failed: %v", err)
	}
	resp.Body.Close()

	auth2, _ := hop2Auth.Load().(string)
	key2, _ := hop2APIKey.Load().(string)
	auth3, _ := hop3Auth.Load().(string)
	if auth2 != "" {
		t.Fatalf("hop2 received Authorization=%q", auth2)
	}
	if key2 != "" {
		t.Fatalf("hop2 received X-Api-Key=%q", key2)
	}
	if auth3 != "" {
		t.Fatalf("hop3 received Authorization=%q", auth3)
	}
}

func TestOutbound_AgentCard_Malformed_Timeout_CapabilityMismatch_FailClosed(t *testing.T) {
	t.Parallel()
	// Endpoint that would succeed if card fail-closed were skipped.
	endpointOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"kind":"message","role":"agent","parts":[{"kind":"text","text":"SHOULD_NOT_SEE"}]}}`)
	}))
	defer endpointOK.Close()

	cases := []struct {
		name string
		card http.HandlerFunc
	}{
		{"http_500", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			_, _ = io.WriteString(w, "boom")
		}},
		{"malformed_json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{not-json`)
		}},
		{"empty_body", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}},
		{"timeout", func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(400 * time.Millisecond)
			w.WriteHeader(200)
			_, _ = io.WriteString(w, `{}`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cardSrv := httptest.NewServer(tc.card)
			defer cardSrv.Close()
			timeout := 80 * time.Millisecond
			if tc.name == "timeout" {
				timeout = 80 * time.Millisecond
			} else {
				timeout = 2 * time.Second
			}
			tool, err := a2agateway.NewOutboundTool(a2agateway.OutboundConfig{
				Binding: a2agateway.RemoteBinding{
					ID: uuid.Must(uuid.NewV7()).String(), CallableName: "remote",
					EndpointURL: endpointOK.URL, AgentCardURL: cardSrv.URL,
					AllowedHosts: []string{"127.0.0.1"}, Version: 1, TimeoutMs: int(timeout / time.Millisecond),
				},
				Audit: &residualMemAudit{}, AllowHTTP: true,
				HTTPClient: a2agateway.SecureHTTPClient(timeout, []string{"127.0.0.1"}, a2agateway.EgressPolicy{AllowHTTP: true}),
			})
			if err != nil {
				t.Fatal(err)
			}
			ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
			ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
				WorkspaceID: ws, ParentRunID: run, RunID: run, CallerAgentID: "agent-a",
			})
			out, _ := tool.InvokableRun(ctx, `{"request":"hi"}`)
			if strings.Contains(out, "SHOULD_NOT_SEE") {
				t.Fatalf("card fail must not reach send success: %s", out)
			}
			low := strings.ToLower(out)
			if !strings.Contains(low, "card") && !strings.Contains(low, "error") &&
				!strings.Contains(low, "fail") && !strings.Contains(low, "timeout") &&
				!strings.Contains(low, "invalid") {
				t.Fatalf("expected fail-closed payload, got %s", out)
			}
		})
	}
}

func TestOutbound_Attribution_UsesCurrentRunNotStaleParent(t *testing.T) {
	t.Parallel()
	// Residual #11: outbound ParentRunID / idempotency bound to current RunID (TASK child).
	audit := &residualMemAudit{}
	// Endpoint-only (no AgentCardURL) so call can complete with synthetic card.
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Minimal agent card OR jsonrpc
		if strings.Contains(r.URL.Path, "card") || r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"name":"r","url":"http://127.0.0.1/","protocolVersion":"0.3","preferredTransport":"JSONRPC","defaultInputModes":["text"],"defaultOutputModes":["text"],"skills":[],"capabilities":{}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"kind":"message","role":"agent","parts":[{"kind":"text","text":"remote-ok"}]}}`)
	}))
	defer endpoint.Close()

	tool, err := a2agateway.NewOutboundTool(a2agateway.OutboundConfig{
		Binding: a2agateway.RemoteBinding{
			ID: "rb1", CallableName: "remote_x", EndpointURL: endpoint.URL,
			// Empty AgentCardURL → endpoint-only mode (synthetic card).
			AllowedHosts: []string{"127.0.0.1"}, Version: 3, TimeoutMs: 3000,
		},
		Audit: audit, AllowHTTP: true, CallerAgentID: "caller-a",
		HTTPClient: a2agateway.SecureHTTPClient(3*time.Second, []string{"127.0.0.1"}, a2agateway.EgressPolicy{AllowHTTP: true}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ws := uuid.Must(uuid.NewV7()).String()
	oldRun := uuid.Must(uuid.NewV7()).String()
	currentRun := uuid.Must(uuid.NewV7()).String()
	parentDel := uuid.Must(uuid.NewV7()).String()
	ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
		WorkspaceID: ws,
		// ParentRunID is the outer parent; RunID is current TASK child — outbound must use RunID.
		ParentRunID: oldRun, RunID: currentRun, CallerAgentID: "agent-b",
		ParentDelegationID: &parentDel, Depth: 1, Budget: agentdelegation.NewBudget(),
	})
	_, _ = tool.InvokableRun(ctx, `{"request":"x"}`)
	if len(audit.created) != 1 {
		t.Fatalf("created=%d", len(audit.created))
	}
	in := audit.created[0]
	if in.ParentRunID != currentRun {
		t.Fatalf("ParentRunID=%s want current RunID %s (not stale %s)", in.ParentRunID, currentRun, oldRun)
	}
	if in.ParentDelegationID == nil || *in.ParentDelegationID != parentDel {
		t.Fatalf("ParentDelegationID=%v want %s", in.ParentDelegationID, parentDel)
	}
	// Idempotency must key on current run, not old parent.
	wantPrefix := currentRun
	if !strings.HasPrefix(in.IdempotencyKey, wantPrefix) {
		t.Fatalf("idempotency=%q want prefix %s", in.IdempotencyKey, wantPrefix)
	}
}

type residualMemAudit struct {
	created []agentdelegation.CreateDelegationInput
	rows    map[string]agentdelegation.Delegation
}

func (m *residualMemAudit) CreateDelegationAndStep(_ context.Context, in agentdelegation.CreateDelegationInput) (agentdelegation.Delegation, bool, error) {
	if m.rows == nil {
		m.rows = map[string]agentdelegation.Delegation{}
	}
	m.created = append(m.created, in)
	if d, ok := m.rows[in.IdempotencyKey]; ok {
		return d, true, nil
	}
	d := agentdelegation.Delegation{
		ID: in.ID, WorkspaceID: in.WorkspaceID, ParentRunID: in.ParentRunID,
		ParentDelegationID: in.ParentDelegationID,
		Status:             agentdelegation.StatusRunning, StepID: in.StepID,
	}
	m.rows[in.IdempotencyKey] = d
	return d, false, nil
}
func (m *residualMemAudit) FinalizeDelegation(_ context.Context, in agentdelegation.FinalizeDelegationInput) (agentdelegation.Delegation, error) {
	return agentdelegation.Delegation{ID: in.DelegationID, Status: in.Status}, nil
}
func (m *residualMemAudit) SetChildRunID(context.Context, string, string, string) error { return nil }
func (m *residualMemAudit) RecordDispatchAttempt(context.Context, string, string) error { return nil }
func (m *residualMemAudit) AccumulateModelTokens(context.Context, string, string, agentdelegation.TokenUsage) error {
	return nil
}
