package httptransport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
)

func TestAAPDataPlaneAcceptanceGoldenTracesThroughHTTP(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"text", "tool_success", "workflow_tool", "approval_resume"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			events, source := loadAAPGoldenHTTPTrace(t, name)
			first, last := events[0], events[len(events)-1]
			reader := &goldenHTTPEventReader{scope: protocolevent.RunScope{
				WorkspaceID: first.WorkspaceID, AgentID: first.AgentID,
				ConversationID: first.ConversationID, RunID: first.RunID,
			}, events: events}
			attacher, err := NewAAPEventCatchUp(reader)
			if err != nil {
				t.Fatal(err)
			}
			runReader := &goldenHTTPRunReader{run: execution.AgentRun{
				ID: first.RunID, WorkspaceID: first.WorkspaceID, AgentID: first.AgentID,
				SessionID: first.ConversationID, Status: goldenInternalRunStatus(last.Type),
				StartedAt: first.OccurredAt, LockVersion: int64(len(events)), TraceID: first.TraceID,
			}}
			routes, err := NewAAPRunRoutes(
				goldenHTTPAuthorizer{}, &aapRunRouteConversations{}, &aapRunRouteApplication{},
				runReader, &aapRunRouteItems{}, attacher,
			)
			if err != nil {
				t.Fatal(err)
			}
			router, err := NewRouter(Config{
				Authenticator:            goldenUserTokenAuthenticator{},
				AgentAccessAuthenticator: goldenAAPAuthenticator{scope: reader.scope},
				AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
			})
			if err != nil {
				t.Fatal(err)
			}
			path := "/api/agent-access/v1/workspaces/" + first.WorkspaceID +
				"/agents/" + first.AgentID + "/runs/" + first.RunID + "/events"
			response := requestGoldenHTTPTrace(router, path, "aap-access-token")
			if response.Code != http.StatusOK ||
				!strings.HasPrefix(response.Header().Get("Content-Type"), "text/event-stream") ||
				response.Header().Get(AAPProtocolVersionHeader) == "" {
				t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
			actual := extractGoldenSSEData(t, response.Body.Bytes())
			if len(actual) != len(source) {
				t.Fatalf("events=%d want=%d body=%s", len(actual), len(source), response.Body.String())
			}
			for index := range source {
				if !sameGoldenJSON(actual[index], source[index]) {
					t.Fatalf("event %d differs\nactual=%s\nwant=%s", index+1, actual[index], source[index])
				}
			}

			// The management/user JWT authenticator may accept this credential,
			// but the isolated AAP middleware must still reject it.
			userToken := requestGoldenHTTPTrace(router, path, "old-user-jwt")
			if userToken.Code != http.StatusUnauthorized || runReader.calls != 1 {
				t.Fatalf("old user token entered AAP: status=%d reads=%d body=%s",
					userToken.Code, runReader.calls, userToken.Body.String())
			}
		})
	}
}

type goldenHTTPEventReader struct {
	scope  protocolevent.RunScope
	events []protocolevent.ProtocolEvent
}

func (reader *goldenHTTPEventReader) HighWatermark(
	_ context.Context,
	scope protocolevent.RunScope,
) (int64, error) {
	if scope != reader.scope {
		return 0, protocolevent.ErrRunScopeNotFound
	}
	return int64(len(reader.events)), nil
}

func (reader *goldenHTTPEventReader) ReadRunAfter(
	_ context.Context,
	scope protocolevent.RunScope,
	after int64,
	limit int,
) ([]protocolevent.ProtocolEvent, error) {
	if scope != reader.scope {
		return nil, protocolevent.ErrRunScopeNotFound
	}
	if after >= int64(len(reader.events)) {
		return nil, nil
	}
	end := min(int(after)+limit, len(reader.events))
	return append([]protocolevent.ProtocolEvent(nil), reader.events[after:end]...), nil
}

type goldenHTTPRunReader struct {
	run   execution.AgentRun
	calls int
}

func (reader *goldenHTTPRunReader) GetAgentRun(
	_ context.Context,
	workspaceID, runID string,
) (execution.AgentRun, error) {
	reader.calls++
	if workspaceID != reader.run.WorkspaceID || runID != reader.run.ID {
		return execution.AgentRun{}, execution.ErrRunNotFound
	}
	return reader.run, nil
}

type goldenAAPAuthenticator struct{ scope protocolevent.RunScope }

func (authenticator goldenAAPAuthenticator) VerifyAccessToken(
	_ context.Context,
	token string,
) (agentaccessauth.AAPAccessTokenPrincipal, error) {
	if token != "aap-access-token" {
		return agentaccessauth.AAPAccessTokenPrincipal{}, errors.New("invalid AAP access token")
	}
	now := time.Now().UTC()
	return agentaccessauth.AAPAccessTokenPrincipal{
		PrincipalID:        "55000000-0000-4000-8000-000000000491",
		ServicePrincipalID: "55000000-0000-4000-8000-000000000492",
		AuthorizedParty:    "awcl_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		WorkspaceID:        authenticator.scope.WorkspaceID, AgentID: authenticator.scope.AgentID,
		Scopes: []string{"event:read"}, SecurityVersion: 1,
		TokenID:  "55000000-0000-4000-8000-000000000493",
		IssuedAt: now.Add(-time.Minute), NotBefore: now.Add(-time.Minute),
		ExpiresAt: now.Add(10 * time.Minute),
	}, nil
}

type goldenUserTokenAuthenticator struct{}

func (goldenUserTokenAuthenticator) AuthenticateAccessToken(
	_ context.Context,
	token string,
) (Principal, error) {
	if token != "old-user-jwt" {
		return Principal{}, errors.New("invalid user token")
	}
	return Principal{UserID: "55000000-0000-4000-8000-000000000499"}, nil
}

type goldenHTTPAuthorizer struct{}

func (goldenHTTPAuthorizer) Authorize(
	_ context.Context,
	request agentaccessauth.AAPAuthorizationRequest,
) (agentaccessauth.AAPAuthorizationDecision, error) {
	if request.Action != agentaccessauth.ActionEventRead || request.Resource.ID == "" {
		return agentaccessauth.AAPAuthorizationDecision{}, agentaccessauth.ErrAAPAuthorizationNotVisible
	}
	now := time.Now().UTC()
	return agentaccessauth.AAPAuthorizationDecision{
		EffectiveScopes: []string{"event:read"},
		Snapshot: agentaccessauth.AAPAuthorizationSnapshot{
			SpecVersion: "aap.authorization.v1", WorkspaceID: request.Principal.WorkspaceID,
			AgentID:            request.Principal.AgentID,
			ClientID:           "55000000-0000-4000-8000-000000000494",
			AuthorizedParty:    request.Principal.AuthorizedParty,
			ServicePrincipalID: request.Principal.ServicePrincipalID,
			SubjectID:          request.Principal.PrincipalID,
			GrantID:            "55000000-0000-4000-8000-000000000495",
			Action:             request.Action, RequiredScope: "event:read",
			TokenScopes: []string{"event:read"}, GrantScopes: []string{"event:read"},
			AgentPolicyScopes: []string{"event:read"}, EffectiveScopes: []string{"event:read"},
			TokenSecurityVersion: 1, ResolvedSecurityVersion: 1,
			WorkspaceVersion: 1, ClientVersion: 1, GrantVersion: 1, AgentPolicyVersion: 1,
			TokenID: request.Principal.TokenID, ResourceType: agentaccessauth.ResourceRun,
			ResourceID: request.Resource.ID, OwnershipMode: "SUBJECT_OWNED",
			OwnershipPolicyVersion: 1, AuthorizedAt: now,
		},
	}, nil
}

func loadAAPGoldenHTTPTrace(
	t *testing.T,
	name string,
) ([]protocolevent.ProtocolEvent, []json.RawMessage) {
	t.Helper()
	path := filepath.Join("..", "..", "protocolschema", "testdata", "aap", "v1", name+".jsonl")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var events []protocolevent.ProtocolEvent
	var source []json.RawMessage
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	for scanner.Scan() {
		line := append(json.RawMessage(nil), scanner.Bytes()...)
		var envelope struct {
			SpecVersion    string          `json:"specVersion"`
			Type           string          `json:"type"`
			EventID        string          `json:"eventId"`
			StreamID       string          `json:"streamId"`
			Sequence       int64           `json:"sequence"`
			OccurredAt     time.Time       `json:"occurredAt"`
			WorkspaceID    string          `json:"workspaceId"`
			AgentID        string          `json:"agentId"`
			ConversationID string          `json:"conversationId"`
			RunID          string          `json:"runId"`
			TraceID        string          `json:"traceId"`
			Data           json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			t.Fatal(err)
		}
		events = append(events, protocolevent.ProtocolEvent{
			ID: envelope.EventID, EventStreamID: envelope.RunID, StreamID: envelope.StreamID,
			WorkspaceID: envelope.WorkspaceID, AgentID: envelope.AgentID,
			ConversationID: envelope.ConversationID, RunID: envelope.RunID,
			Type: envelope.Type, SpecVersion: envelope.SpecVersion, TraceID: envelope.TraceID,
			Sequence: envelope.Sequence, OccurredAt: envelope.OccurredAt,
			Data: envelope.Data, Payload: line,
		})
		source = append(source, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("golden trace is empty")
	}
	return events, source
}

func requestGoldenHTTPTrace(
	handler http.Handler,
	path, token string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "text/event-stream")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func extractGoldenSSEData(t *testing.T, body []byte) []json.RawMessage {
	t.Helper()
	var values []json.RawMessage
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			values = append(values, json.RawMessage(strings.TrimPrefix(scanner.Text(), "data: ")))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

func sameGoldenJSON(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil &&
		mustCanonicalJSON(leftValue) == mustCanonicalJSON(rightValue)
}

func mustCanonicalJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func goldenInternalRunStatus(eventType string) string {
	switch eventType {
	case protocolevent.EventRunCompleted:
		return "SUCCEEDED"
	case protocolevent.EventRunFailed:
		return "FAILED"
	case protocolevent.EventRunCancelled:
		return "CANCELLED"
	default:
		return "RUNNING"
	}
}
