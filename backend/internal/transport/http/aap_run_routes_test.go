package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/protocolschema"
)

const (
	aapRunWorkspaceID    = "d41f1f2e-7b5a-7c3d-8e9f-123456789001"
	aapRunAgentID        = "d41f1f2e-7b5a-7c3d-8e9f-123456789002"
	aapRunServiceID      = "d41f1f2e-7b5a-7c3d-8e9f-123456789003"
	aapRunSubjectAID     = "d41f1f2e-7b5a-7c3d-8e9f-123456789004"
	aapRunSubjectBID     = "d41f1f2e-7b5a-7c3d-8e9f-123456789005"
	aapRunClientID       = "d41f1f2e-7b5a-7c3d-8e9f-123456789006"
	aapRunGrantID        = "d41f1f2e-7b5a-7c3d-8e9f-123456789007"
	aapRunTokenID        = "d41f1f2e-7b5a-7c3d-8e9f-123456789008"
	aapRunKeyExplicit    = "d41f1f2e-7b5a-7c3d-8e9f-123456789009"
	aapRunKeyImplicit    = "d41f1f2e-7b5a-7c3d-8e9f-12345678900a"
	aapRunConversationID = "d41f1f2e-7b5a-7c3d-8e9f-12345678900b"
	aapRunImplicitCID    = "d41f1f2e-7b5a-7c3d-8e9f-12345678900c"
	aapRunID             = "d41f1f2e-7b5a-7c3d-8e9f-12345678900d"
	aapRunEventID        = "d41f1f2e-7b5a-7c3d-8e9f-12345678900e"
	aapRunItemID         = "d41f1f2e-7b5a-7c3d-8e9f-12345678900f"
)

func TestAAPRunRoutes(t *testing.T) {
	reader := &aapRunRouteReader{}
	application := &aapRunRouteApplication{reader: reader}
	conversations := &aapRunRouteConversations{}
	authorizer := &aapRunRouteAuthorizer{}
	items := &aapRunRouteItems{}
	attacher, err := NewAAPEventCatchUp(reader)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewAAPRunRoutes(
		authorizer, conversations, application, reader, items, attacher,
	)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: aapRunRouteAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/agent-access/v1/workspaces/" + aapRunWorkspaceID +
		"/agents/" + aapRunAgentID + "/runs"

	t.Run("returns 202 only after a durable accepted Run", func(t *testing.T) {
		response := requestAAPRun(t, router, http.MethodPost, base, map[string]any{
			"conversationId": aapRunConversationID,
			"input": []any{map[string]any{
				"type": "message", "role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "查询"},
					map[string]any{"type": "text", "text": "订单状态"},
				},
			}},
			"stream": false, "metadata": map[string]string{"request": "order-1"},
		}, "subject-a", aapRunKeyExplicit, "application/json", "")
		if response.Code != http.StatusAccepted ||
			response.Header().Get("Location") != base+"/"+aapRunID ||
			!strings.Contains(response.Body.String(), `"status":"accepted"`) ||
			!strings.Contains(response.Body.String(), aapRunID+`/events`) ||
			!application.committedBeforeReturn || application.sideEffects != 1 {
			t.Fatalf("status=%d headers=%v body=%s committed=%v effects=%d",
				response.Code, response.Header(), response.Body.String(),
				application.committedBeforeReturn, application.sideEffects)
		}
		for _, forbidden := range []string{
			"modelSnapshot", "capabilitySnapshot", "authorizationSnapshot",
			"principalSnapshot", "requestHash", "grantId", "tokenId",
		} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Fatalf("accepted Run leaked %q: %s", forbidden, response.Body.String())
			}
		}
	})

	t.Run("same key attaches to the same Run from sequence zero", func(t *testing.T) {
		response := requestAAPRun(t, router, http.MethodPost, base, map[string]any{
			"conversationId": aapRunConversationID,
			"input": []any{map[string]any{
				"type": "message", "role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "查询"},
					map[string]any{"type": "text", "text": "订单状态"},
				},
			}},
			"stream": true, "metadata": map[string]string{"request": "order-1"},
		}, "subject-a", aapRunKeyExplicit, "text/event-stream", "")
		if response.Code != http.StatusOK ||
			!strings.HasPrefix(response.Header().Get("Content-Type"), "text/event-stream") ||
			strings.Count(response.Body.String(), "id: 1\n") != 1 ||
			!strings.Contains(response.Body.String(), "event: run.accepted\n") ||
			application.sideEffects != 1 {
			t.Fatalf("status=%d headers=%v body=%s effects=%d",
				response.Code, response.Header(), response.Body.String(), application.sideEffects)
		}
	})

	t.Run("implicitly creates the first permanent Conversation", func(t *testing.T) {
		response := requestAAPRun(t, router, http.MethodPost, base, map[string]any{
			"input": []any{map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "text", "text": "hello"}},
			}}, "stream": false,
		}, "subject-a", aapRunKeyImplicit, "application/json", "")
		if response.Code != http.StatusAccepted || conversations.createCalls != 1 ||
			application.last.ConversationID != aapRunImplicitCID ||
			authorizer.lastRunConversationID != aapRunImplicitCID {
			t.Fatalf("status=%d body=%s conversationCalls=%d input=%+v authorizedCID=%s",
				response.Code, response.Body.String(), conversations.createCalls,
				application.last, authorizer.lastRunConversationID)
		}
	})

	t.Run("rejects unsupported content and changed idempotent intent before streaming", func(t *testing.T) {
		unsupported := requestAAPRun(t, router, http.MethodPost, base, map[string]any{
			"conversationId": aapRunConversationID,
			"input": []any{map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "image", "text": "secret"}},
			}}, "stream": true,
		}, "subject-a", "d41f1f2e-7b5a-7c3d-8e9f-123456789010", "text/event-stream", "")
		assertAAPRouterError(t, unsupported, http.StatusUnprocessableEntity, "UNSUPPORTED_CONTENT_TYPE")
		if strings.Contains(unsupported.Header().Get("Content-Type"), "text/event-stream") {
			t.Fatalf("validation committed SSE headers: %v", unsupported.Header())
		}

		conflict := requestAAPRun(t, router, http.MethodPost, base, map[string]any{
			"conversationId": aapRunConversationID,
			"input": []any{map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "text", "text": "changed"}},
			}}, "stream": false, "metadata": map[string]string{"request": "order-1"},
		}, "subject-a", aapRunKeyExplicit, "application/json", "")
		assertAAPRouterError(t, conflict, http.StatusConflict, "IDEMPOTENCY_CONFLICT")
		if application.sideEffects != 2 { // explicit + the distinct implicit Run
			t.Fatalf("conflict executed another side effect: %d", application.sideEffects)
		}
	})

	t.Run("reads terminal Run and final ordered Item snapshots", func(t *testing.T) {
		finished := time.Now().UTC().Truncate(time.Millisecond)
		reader.run.Status = "SUCCEEDED"
		reader.run.LockVersion = 4
		reader.run.FinishedAt = &finished
		items.values = []protocolevent.RunItemProjection{{
			ID: aapRunItemID, WorkspaceID: aapRunWorkspaceID, AgentID: aapRunAgentID,
			RunID: aapRunID, Ordinal: 1,
			Snapshot: json.RawMessage(`{"id":"` + aapRunItemID +
				`","type":"message","status":"completed","role":"assistant","content":[{"type":"text","text":"订单已发货"}]}`),
		}}
		path := base + "/" + aapRunID
		response := requestAAPRun(t, router, http.MethodGet, path, nil,
			"subject-a", "", "application/json", "")
		if response.Code != http.StatusOK ||
			!strings.Contains(response.Body.String(), `"status":"completed"`) ||
			!strings.Contains(response.Body.String(), `"text":"订单已发货"`) ||
			response.Header().Get("ETag") == "" {
			t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
		}
		for _, forbidden := range []string{
			"model-secret", "authorizationSnapshot", "principalSnapshot", "grantId", "tokenId",
		} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Fatalf("Run read leaked %q: %s", forbidden, response.Body.String())
			}
		}
		conditional := requestAAPRun(t, router, http.MethodGet, path, nil,
			"subject-a", "", "application/json", response.Header().Get("ETag"))
		if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
			t.Fatalf("conditional status=%d body=%s", conditional.Code, conditional.Body.String())
		}
	})

	t.Run("conceals Subject and path scope before storage", func(t *testing.T) {
		beforeReads := reader.getCalls
		denied := requestAAPRun(t, router, http.MethodGet, base+"/"+aapRunID, nil,
			"subject-b", "", "application/json", "")
		assertAAPRouterError(t, denied, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		if reader.getCalls != beforeReads {
			t.Fatal("denied Subject reached Run storage")
		}

		wrongAgent := strings.Replace(base, aapRunAgentID,
			"d41f1f2e-7b5a-7c3d-8e9f-123456789099", 1) + "/" + aapRunID
		beforeAuth := authorizer.calls
		response := requestAAPRun(t, router, http.MethodGet, wrongAgent, nil,
			"subject-a", "", "application/json", "")
		assertAAPRouterError(t, response, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		if authorizer.calls != beforeAuth {
			t.Fatal("wrong Agent path reached authorizer")
		}
	})

	if _, err := NewAAPRunRoutes(nil, conversations, application, reader, items, attacher); err == nil {
		t.Fatal("expected nil authorizer rejection")
	}
	if _, err := NewAAPRunRoutes(authorizer, nil, application, reader, items, attacher); err == nil {
		t.Fatal("expected nil Conversation application rejection")
	}
}

func TestAAPRunRoutesOutboundCredentials(t *testing.T) {
	reader := &aapRunRouteReader{}
	application := &aapRunRouteApplication{reader: reader}
	conversations := &aapRunRouteConversations{}
	authorizer := &aapRunRouteAuthorizer{}
	items := &aapRunRouteItems{}
	attacher, err := NewAAPEventCatchUp(reader)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewAAPRunRoutes(
		authorizer, conversations, application, reader, items, attacher,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Without ConfigureOutboundAttach, credentials fail closed.
	routerClosed, err := NewRouter(Config{
		AgentAccessAuthenticator: aapRunRouteAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/agent-access/v1/workspaces/" + aapRunWorkspaceID +
		"/agents/" + aapRunAgentID + "/runs"
	canary := "CANARY-AAP-ROUTE-TOKEN"
	body := map[string]any{
		"conversationId": aapRunConversationID,
		"input": []any{map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "text", "text": "查询"}},
		}},
		"stream": false,
		"outboundCredentials": map[string]any{
			"schemaVersion": "outbound-credentials.v1",
			"bindings": []any{map[string]any{
				"connectionId": aapRunClientID, "credentialType": "ACCESS_TOKEN",
				"value": canary, "expiresAt": "2099-01-01T00:00:00Z",
			}},
		},
	}
	denied := requestAAPRun(t, routerClosed, http.MethodPost, base, body,
		"subject-a", "d41f1f2e-7b5a-7c3d-8e9f-1234567890b1", "application/json", "")
	if denied.Code != http.StatusBadRequest {
		t.Fatalf("unwired status=%d body=%s", denied.Code, denied.Body.String())
	}
	if application.sideEffects != 0 {
		t.Fatal("unwired path executed Create")
	}

	// With attach configured, createRun accepts envelope (service attaches / validates).
	routes.ConfigureOutboundAttach()
	routerOpen, err := NewRouter(Config{
		AgentAccessAuthenticator: aapRunRouteAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted := requestAAPRun(t, routerOpen, http.MethodPost, base, body,
		"subject-a", "d41f1f2e-7b5a-7c3d-8e9f-1234567890b2", "application/json", "")
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("wired status=%d body=%s", accepted.Code, accepted.Body.String())
	}
	if application.sideEffects != 1 {
		t.Fatalf("sideEffects=%d", application.sideEffects)
	}
	if len(application.last.OutboundCredentialsRaw) == 0 ||
		!strings.Contains(string(application.last.OutboundCredentialsRaw), canary) {
		t.Fatalf("credentials not threaded: %s", application.last.OutboundCredentialsRaw)
	}
	// Response must never echo Token.
	if strings.Contains(accepted.Body.String(), canary) ||
		strings.Contains(accepted.Body.String(), "outboundCredentials") {
		t.Fatalf("response leaked credentials: %s", accepted.Body.String())
	}
	// Without outboundCredentials still succeeds (Broker-only / no passthrough agents).
	plain := requestAAPRun(t, routerOpen, http.MethodPost, base, map[string]any{
		"conversationId": aapRunConversationID,
		"input": []any{map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "text", "text": "plain"}},
		}},
		"stream": false,
	}, "subject-a", "d41f1f2e-7b5a-7c3d-8e9f-1234567890b3", "application/json", "")
	if plain.Code != http.StatusAccepted {
		t.Fatalf("plain status=%d body=%s", plain.Code, plain.Body.String())
	}
}

type aapRunRouteAuthenticator struct{}

func (aapRunRouteAuthenticator) VerifyAccessToken(
	_ context.Context,
	value string,
) (agentaccessauth.AAPAccessTokenPrincipal, error) {
	principalID := aapRunSubjectAID
	if value == "subject-b" {
		principalID = aapRunSubjectBID
	} else if value != "subject-a" {
		return agentaccessauth.AAPAccessTokenPrincipal{}, errors.New("invalid AAP Run token")
	}
	now := time.Now().UTC()
	return agentaccessauth.AAPAccessTokenPrincipal{
		PrincipalID: principalID, ServicePrincipalID: aapRunServiceID,
		AuthorizedParty: "awcl_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", WorkspaceID: aapRunWorkspaceID,
		AgentID: aapRunAgentID,
		Scopes: []string{
			"conversation:create", "run:create", "run:read", "run:cancel", "interaction:decide",
		},
		SecurityVersion: 1,
		TokenID:         aapRunTokenID, IssuedAt: now.Add(-time.Minute), NotBefore: now.Add(-time.Minute),
		ExpiresAt: now.Add(10 * time.Minute),
	}, nil
}

type aapRunRouteAuthorizer struct {
	calls                 int
	lastRunConversationID string
}

func (authorizer *aapRunRouteAuthorizer) Authorize(
	_ context.Context,
	request agentaccessauth.AAPAuthorizationRequest,
) (agentaccessauth.AAPAuthorizationDecision, error) {
	authorizer.calls++
	if (request.Action == agentaccessauth.ActionRunRead || request.Action == agentaccessauth.ActionRunCancel ||
		request.Action == agentaccessauth.ActionInteractionDecide) &&
		request.Principal.PrincipalID == aapRunSubjectBID {
		return agentaccessauth.AAPAuthorizationDecision{}, agentaccessauth.ErrAAPAuthorizationNotVisible
	}
	requiredScope := "conversation:create"
	resourceType := agentaccessauth.ResourceNone
	ownershipMode := ""
	if request.Action == agentaccessauth.ActionRunCreate {
		requiredScope, resourceType, ownershipMode = "run:create", agentaccessauth.ResourceConversation, "SUBJECT_OWNED"
		authorizer.lastRunConversationID = request.Resource.ID
	} else if request.Action == agentaccessauth.ActionRunRead {
		requiredScope, resourceType, ownershipMode = "run:read", agentaccessauth.ResourceRun, "SUBJECT_OWNED"
	} else if request.Action == agentaccessauth.ActionRunCancel {
		requiredScope, resourceType, ownershipMode = "run:cancel", agentaccessauth.ResourceRun, "SUBJECT_OWNED"
	} else if request.Action == agentaccessauth.ActionInteractionDecide {
		requiredScope, resourceType, ownershipMode = "interaction:decide", agentaccessauth.ResourceInteraction, "SUBJECT_OWNED"
	}
	snapshot := agentaccessauth.AAPAuthorizationSnapshot{
		SpecVersion: "aap.authorization.v1", WorkspaceID: request.Principal.WorkspaceID,
		AgentID: request.Principal.AgentID, ClientID: aapRunClientID,
		AuthorizedParty:    request.Principal.AuthorizedParty,
		ServicePrincipalID: request.Principal.ServicePrincipalID,
		SubjectID:          request.Principal.PrincipalID, GrantID: aapRunGrantID,
		Action: request.Action, RequiredScope: requiredScope,
		TokenScopes: []string{requiredScope}, GrantScopes: []string{requiredScope},
		AgentPolicyScopes: []string{requiredScope}, EffectiveScopes: []string{requiredScope},
		TokenSecurityVersion: 1, ResolvedSecurityVersion: 1,
		WorkspaceVersion: 1, ClientVersion: 1, GrantVersion: 7, AgentPolicyVersion: 11,
		TokenID: request.Principal.TokenID, ResourceType: resourceType,
		ResourceID: request.Resource.ID, OwnershipMode: ownershipMode,
		OwnershipPolicyVersion: 11, AuthorizedAt: time.Now().UTC(),
	}
	if request.Action == agentaccessauth.ActionConversationCreate {
		snapshot.OwnershipPolicyVersion = 0
	}
	return agentaccessauth.AAPAuthorizationDecision{
		EffectiveScopes: []string{requiredScope}, Snapshot: snapshot,
	}, nil
}

type aapRunRouteConversations struct {
	createCalls int
}

func (conversations *aapRunRouteConversations) Create(
	_ context.Context,
	input aap.CreateConversationInput,
) (aap.CreateConversationResult, error) {
	conversations.createCalls++
	now := time.Now().UTC()
	return aap.CreateConversationResult{Conversation: chat.Session{
		ID: aapRunImplicitCID, WorkspaceID: input.Scope.WorkspaceID,
		AgentID: input.Scope.AgentID, Status: "ACTIVE", CreatedAt: now,
		UpdatedAt: now, LockVersion: 1,
	}}, nil
}

func (*aapRunRouteConversations) Get(
	context.Context,
	aap.GetConversationInput,
) (aap.ConversationView, error) {
	return aap.ConversationView{}, aap.ErrConversationNotFound
}

type aapRunRouteApplication struct {
	reader                *aapRunRouteReader
	cached                map[string]aap.CreateRunInput
	results               map[string]aap.CreateRunResult
	last                  aap.CreateRunInput
	sideEffects           int
	committedBeforeReturn bool
}

func (application *aapRunRouteApplication) Create(
	_ context.Context,
	input aap.CreateRunInput,
) (aap.CreateRunResult, error) {
	// Clone write-only raw so transport ZeroCredentialsRaw after Create cannot
	// erase the observation under test.
	copyCreds := append(json.RawMessage(nil), input.OutboundCredentialsRaw...)
	application.last = input
	application.last.OutboundCredentialsRaw = copyCreds
	if application.cached == nil {
		application.cached = make(map[string]aap.CreateRunInput)
		application.results = make(map[string]aap.CreateRunResult)
	}
	if cached, exists := application.cached[input.IdempotencyKey]; exists {
		if input.ConversationID != cached.ConversationID ||
			input.Text != cached.Text || !reflect.DeepEqual(input.Metadata, cached.Metadata) {
			return aap.CreateRunResult{}, aap.ErrRunIdempotencyConflict
		}
		result := application.results[input.IdempotencyKey]
		result.Idempotent = true
		return result, nil
	}
	copyInput := input
	copyInput.Metadata = cloneRunMetadata(input.Metadata)
	copyInput.OutboundCredentialsRaw = copyCreds
	application.cached[input.IdempotencyKey] = copyInput
	application.sideEffects++
	started := time.Now().UTC().Truncate(time.Millisecond)
	application.reader.run = execution.AgentRun{
		ID: aapRunID, WorkspaceID: input.Scope.WorkspaceID, AgentID: input.Scope.AgentID,
		SessionID: input.ConversationID, Status: "RUNNING", TriggerType: "API",
		StartedAt: started, LockVersion: 1,
		ModelSnapshot: json.RawMessage(`{"apiKey":"model-secret"}`),
	}
	accepted := aapRunRouteAcceptedEvent(input.ConversationID, started)
	application.reader.events = []protocolevent.ProtocolEvent{accepted}
	application.committedBeforeReturn = true
	result := aap.CreateRunResult{Run: application.reader.run, AcceptedEvent: accepted}
	application.results[input.IdempotencyKey] = result
	return result, nil
}

type aapRunRouteReader struct {
	run      execution.AgentRun
	events   []protocolevent.ProtocolEvent
	getCalls int
}

func (reader *aapRunRouteReader) GetAgentRun(
	_ context.Context,
	workspaceID, runID string,
) (execution.AgentRun, error) {
	reader.getCalls++
	if workspaceID != aapRunWorkspaceID || runID != aapRunID || reader.run.ID == "" {
		return execution.AgentRun{}, execution.ErrRunNotFound
	}
	return reader.run, nil
}

func (reader *aapRunRouteReader) HighWatermark(
	_ context.Context,
	scope protocolevent.RunScope,
) (int64, error) {
	if scope.WorkspaceID != aapRunWorkspaceID || scope.AgentID != aapRunAgentID ||
		scope.RunID != aapRunID {
		return 0, protocolevent.ErrRunScopeNotFound
	}
	return int64(len(reader.events)), nil
}

func (reader *aapRunRouteReader) ReadRunAfter(
	_ context.Context,
	scope protocolevent.RunScope,
	after int64,
	limit int,
) ([]protocolevent.ProtocolEvent, error) {
	if scope.WorkspaceID != aapRunWorkspaceID || scope.AgentID != aapRunAgentID ||
		scope.RunID != aapRunID || after < 0 || limit < 1 {
		return nil, protocolevent.ErrRunScopeNotFound
	}
	if int(after) >= len(reader.events) {
		return []protocolevent.ProtocolEvent{}, nil
	}
	end := min(int(after)+limit, len(reader.events))
	return append([]protocolevent.ProtocolEvent(nil), reader.events[int(after):end]...), nil
}

type aapRunRouteItems struct {
	values []protocolevent.RunItemProjection
}

func (items *aapRunRouteItems) ListForRun(
	_ context.Context,
	workspaceID, agentID, runID string,
) ([]protocolevent.RunItemProjection, error) {
	if workspaceID != aapRunWorkspaceID || agentID != aapRunAgentID || runID != aapRunID {
		return nil, protocolevent.ErrRunItemNotFound
	}
	return append([]protocolevent.RunItemProjection(nil), items.values...), nil
}

func aapRunRouteAcceptedEvent(conversationID string, startedAt time.Time) protocolevent.ProtocolEvent {
	run := protocolevent.Run{
		ID: aapRunID, ConversationID: conversationID, AgentID: aapRunAgentID,
		Status: protocolevent.RunStatusAccepted, Trigger: protocolevent.RunTriggerAPI,
		StartedAt: startedAt,
	}
	data, _ := json.Marshal(protocolevent.RunSnapshotData{Run: run})
	payload, _ := json.Marshal(map[string]any{
		"specVersion": protocolschema.SpecVersion, "type": protocolevent.EventRunAccepted,
		"eventId": aapRunEventID, "streamId": "run:" + aapRunID, "sequence": 1,
		"occurredAt": startedAt, "workspaceId": aapRunWorkspaceID, "agentId": aapRunAgentID,
		"conversationId": conversationID, "runId": aapRunID, "traceId": "trace-aap-run",
		"data": json.RawMessage(data),
	})
	return protocolevent.ProtocolEvent{
		ID: aapRunEventID, EventStreamID: aapRunID, StreamID: "run:" + aapRunID,
		WorkspaceID: aapRunWorkspaceID, AgentID: aapRunAgentID,
		ConversationID: conversationID, RunID: aapRunID,
		Type: protocolevent.EventRunAccepted, SpecVersion: protocolschema.SpecVersion,
		TraceID: "trace-aap-run", Sequence: 1, OccurredAt: startedAt,
		Data: data, Payload: payload,
	}
}

func requestAAPRun(
	t *testing.T,
	handler http.Handler,
	method, path string,
	body any,
	token, idempotencyKey, accept, etag string,
) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", accept)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
