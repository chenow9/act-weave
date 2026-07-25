package httptransport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
)

func TestAAPRunEventsRoute(t *testing.T) {
	events := &createRunEventReader{}
	events.append(createRunProtocolEvent(t, 1, protocolevent.EventRunAccepted))
	events.append(createRunProtocolEvent(t, 2, protocolevent.EventRunFailed))
	attacher, err := NewAAPEventCatchUp(events)
	if err != nil {
		t.Fatal(err)
	}
	runReader := &aapRunEventsResourceReader{run: execution.AgentRun{
		ID: createRunID, WorkspaceID: catchUpWorkspaceID, AgentID: catchUpAgentID,
		SessionID: createConversationID, Status: "FAILED", StartedAt: time.Now().UTC(),
		LockVersion: 2,
	}}
	authorizer := &aapRunEventsAuthorizer{}
	routes, err := NewAAPRunRoutes(
		authorizer, &aapRunRouteConversations{}, &aapRunRouteApplication{},
		runReader, &aapRunRouteItems{}, attacher,
	)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: aapRunEventsAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agent-access/v1/workspaces/" + catchUpWorkspaceID +
		"/agents/" + catchUpAgentID + "/runs/" + createRunID + "/events"

	t.Run("replays committed events and closes on terminal state", func(t *testing.T) {
		response := requestAAPRunEvents(router, path, "fresh-a", "", "text/event-stream")
		if response.Code != http.StatusOK ||
			!strings.HasPrefix(response.Header().Get("Content-Type"), "text/event-stream") ||
			strings.Count(response.Body.String(), "id: 1\n") != 1 ||
			strings.Count(response.Body.String(), "id: 2\n") != 1 ||
			!strings.Contains(response.Body.String(), "event: run.accepted\n") ||
			!strings.Contains(response.Body.String(), "event: run.failed\n") ||
			strings.Contains(response.Body.String(), "event: stream.error\n") ||
			authorizer.lastAction != agentaccessauth.ActionEventRead {
			t.Fatalf("status=%d headers=%v body=%s action=%s",
				response.Code, response.Header(), response.Body.String(), authorizer.lastAction)
		}
	})

	t.Run("reconnects with a renewed token and Last-Event-ID", func(t *testing.T) {
		response := requestAAPRunEvents(router, path, "renewed-a", "1", "text/event-stream")
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "id: 1\n") ||
			strings.Count(response.Body.String(), "id: 2\n") != 1 ||
			authorizer.lastTokenID != "55000000-0000-4000-8000-000000000499" {
			t.Fatalf("status=%d body=%s token=%s",
				response.Code, response.Body.String(), authorizer.lastTokenID)
		}
	})

	t.Run("rejects invalid and cross-Run cursors before SSE headers", func(t *testing.T) {
		for _, cursor := range []string{"3", "-1", "1.0", " 1"} {
			response := requestAAPRunEvents(router, path, "fresh-a", cursor, "text/event-stream")
			assertAAPRouterError(t, response, http.StatusUnprocessableEntity, "REPLAY_CURSOR_INVALID")
			if strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") {
				t.Fatalf("cursor %q committed SSE headers: %v", cursor, response.Header())
			}
		}
	})

	t.Run("conceals another Subject before loading the Run", func(t *testing.T) {
		before := runReader.calls
		response := requestAAPRunEvents(router, path, "subject-b", "", "text/event-stream")
		assertAAPRouterError(t, response, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		if runReader.calls != before {
			t.Fatal("denied Subject reached Run storage")
		}
	})

	t.Run("does not accept access tokens in the query string", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, path+"?access_token=fresh-a", nil)
		request.Header.Set("Accept", "text/event-stream")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		assertAAPRouterError(t, response, http.StatusUnauthorized, "UNAUTHENTICATED")

		request = httptest.NewRequest(http.MethodGet, path+"?access_token=leaked", nil)
		request.Header.Set("Authorization", "Bearer fresh-a")
		request.Header.Set("Accept", "text/event-stream")
		response = httptest.NewRecorder()
		router.ServeHTTP(response, request)
		assertAAPRouterError(t, response, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
		if strings.Contains(response.Body.String(), "leaked") {
			t.Fatalf("query token was reflected: %s", response.Body.String())
		}
	})

	t.Run("requires the SSE media type and fixes Workspace Agent scope", func(t *testing.T) {
		response := requestAAPRunEvents(router, path, "fresh-a", "", "application/json")
		assertAAPRouterError(t, response, http.StatusUnprocessableEntity, "VALIDATION_ERROR")

		wrongAgent := strings.Replace(path, catchUpAgentID,
			"11000000-0000-4000-8000-000000000499", 1)
		before := authorizer.calls
		response = requestAAPRunEvents(router, wrongAgent, "fresh-a", "", "text/event-stream")
		assertAAPRouterError(t, response, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		if authorizer.calls != before {
			t.Fatal("wrong Agent scope reached authorizer")
		}
	})
}

type aapRunEventsAuthenticator struct{}

func (aapRunEventsAuthenticator) VerifyAccessToken(
	_ context.Context,
	value string,
) (agentaccessauth.AAPAccessTokenPrincipal, error) {
	principalID := "55000000-0000-4000-8000-000000000491"
	tokenID := "55000000-0000-4000-8000-000000000498"
	switch value {
	case "fresh-a":
	case "renewed-a":
		tokenID = "55000000-0000-4000-8000-000000000499"
	case "subject-b":
		principalID = "55000000-0000-4000-8000-000000000492"
	default:
		return agentaccessauth.AAPAccessTokenPrincipal{}, errors.New("invalid Run events token")
	}
	now := time.Now().UTC()
	return agentaccessauth.AAPAccessTokenPrincipal{
		PrincipalID:        principalID,
		ServicePrincipalID: "55000000-0000-4000-8000-000000000493",
		AuthorizedParty:    "awcl_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		WorkspaceID:        catchUpWorkspaceID, AgentID: catchUpAgentID,
		Scopes: []string{"event:read"}, SecurityVersion: 1, TokenID: tokenID,
		IssuedAt: now.Add(-time.Minute), NotBefore: now.Add(-time.Minute),
		ExpiresAt: now.Add(10 * time.Minute),
	}, nil
}

type aapRunEventsAuthorizer struct {
	calls       int
	lastAction  agentaccessauth.AAPAction
	lastTokenID string
}

func (authorizer *aapRunEventsAuthorizer) Authorize(
	_ context.Context,
	request agentaccessauth.AAPAuthorizationRequest,
) (agentaccessauth.AAPAuthorizationDecision, error) {
	authorizer.calls++
	authorizer.lastAction = request.Action
	authorizer.lastTokenID = request.Principal.TokenID
	if request.Principal.PrincipalID == "55000000-0000-4000-8000-000000000492" {
		return agentaccessauth.AAPAuthorizationDecision{}, agentaccessauth.ErrAAPAuthorizationNotVisible
	}
	snapshot := agentaccessauth.AAPAuthorizationSnapshot{
		SpecVersion: "aap.authorization.v1", WorkspaceID: catchUpWorkspaceID,
		AgentID: catchUpAgentID, ClientID: "55000000-0000-4000-8000-000000000494",
		AuthorizedParty:    request.Principal.AuthorizedParty,
		ServicePrincipalID: request.Principal.ServicePrincipalID,
		SubjectID:          request.Principal.PrincipalID,
		GrantID:            "55000000-0000-4000-8000-000000000495",
		Action:             agentaccessauth.ActionEventRead, RequiredScope: "event:read",
		TokenScopes: []string{"event:read"}, GrantScopes: []string{"event:read"},
		AgentPolicyScopes: []string{"event:read"}, EffectiveScopes: []string{"event:read"},
		TokenSecurityVersion: 1, ResolvedSecurityVersion: 1,
		WorkspaceVersion: 1, ClientVersion: 1, GrantVersion: 2, AgentPolicyVersion: 3,
		TokenID: request.Principal.TokenID, ResourceType: agentaccessauth.ResourceRun,
		ResourceID: request.Resource.ID, OwnershipMode: "SUBJECT_OWNED",
		OwnershipPolicyVersion: 3, AuthorizedAt: time.Now().UTC(),
	}
	return agentaccessauth.AAPAuthorizationDecision{
		EffectiveScopes: []string{"event:read"}, Snapshot: snapshot,
	}, nil
}

type aapRunEventsResourceReader struct {
	run   execution.AgentRun
	calls int
}

func (reader *aapRunEventsResourceReader) GetAgentRun(
	_ context.Context,
	workspaceID, runID string,
) (execution.AgentRun, error) {
	reader.calls++
	if workspaceID != reader.run.WorkspaceID || runID != reader.run.ID {
		return execution.AgentRun{}, execution.ErrRunNotFound
	}
	return reader.run, nil
}

func requestAAPRunEvents(
	handler http.Handler,
	path, token, cursor, accept string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", accept)
	if cursor != "" {
		request.Header.Set("Last-Event-ID", cursor)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
