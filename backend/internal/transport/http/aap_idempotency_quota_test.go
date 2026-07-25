package httptransport

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
)

func TestAAPIdempotencyAndQuotaHTTP(t *testing.T) {
	reader := &aapRunRouteReader{}
	application := &aapRunRouteApplication{reader: reader}
	attacher, err := NewAAPEventCatchUp(reader)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewAAPRunRoutes(
		&aapRunRouteAuthorizer{}, &aapRunRouteConversations{}, application,
		reader, &aapRunRouteItems{}, attacher,
	)
	if err != nil {
		t.Fatal(err)
	}
	quota, err := agentaccess.NewInMemoryDataPlaneQuota(agentaccess.DataPlaneQuotaConfig{
		Window: time.Minute, MaxEntries: 100,
		Limits: map[agentaccess.DataPlaneQuotaOperation]int{
			agentaccess.QuotaRunCreate: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.ConfigureCommandQuota(quota); err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: aapRunRouteAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agent-access/v1/workspaces/" + aapRunWorkspaceID +
		"/agents/" + aapRunAgentID + "/runs"
	body := func(metadata map[string]string) map[string]any {
		return map[string]any{
			"conversationId": aapRunConversationID,
			"input": []any{map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "text", "text": "hello"}},
			}},
			"stream": false, "metadata": metadata,
		}
	}
	secret := requestAAPRun(t, router, http.MethodPost, path,
		body(map[string]string{"businessRequestId": "Bearer secret-access-token"}),
		"subject-a", "d41f1f2e-7b5a-7c3d-8e9f-123456789040", "application/json", "")
	assertAAPRouterError(t, secret, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	if application.sideEffects != 0 {
		t.Fatal("rejected Metadata consumed a Run side effect")
	}

	accepted := requestAAPRun(t, router, http.MethodPost, path,
		body(map[string]string{"businessRequestId": "ORDER-100"}),
		"subject-a", "d41f1f2e-7b5a-7c3d-8e9f-123456789041", "application/json", "")
	if accepted.Code != http.StatusAccepted || accepted.Header().Get("RateLimit-Limit") != "1" ||
		accepted.Header().Get("RateLimit-Remaining") != "0" {
		t.Fatalf("accepted status=%d headers=%v body=%s", accepted.Code, accepted.Header(), accepted.Body.String())
	}

	limited := requestAAPRun(t, router, http.MethodPost, path,
		body(map[string]string{"businessRequestId": "ORDER-101"}),
		"subject-a", "d41f1f2e-7b5a-7c3d-8e9f-123456789042", "application/json", "")
	assertAAPRouterError(t, limited, http.StatusTooManyRequests, "RATE_LIMITED")
	if limited.Header().Get("Retry-After") == "" ||
		limited.Header().Get("RateLimit-Remaining") != "0" || application.sideEffects != 1 ||
		strings.Contains(strings.ToLower(limited.Body.String()), "token") {
		t.Fatalf("limited headers=%v body=%s effects=%d", limited.Header(), limited.Body.String(), application.sideEffects)
	}
}
