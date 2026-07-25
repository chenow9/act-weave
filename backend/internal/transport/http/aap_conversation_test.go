package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"
)

const (
	aapConversationWorkspaceID = "c38f1f2e-7b5a-7c3d-8e9f-123456789001"
	aapConversationAgentID     = "c38f1f2e-7b5a-7c3d-8e9f-123456789002"
	aapConversationServiceID   = "c38f1f2e-7b5a-7c3d-8e9f-123456789003"
	aapConversationSubjectAID  = "c38f1f2e-7b5a-7c3d-8e9f-123456789004"
	aapConversationSubjectBID  = "c38f1f2e-7b5a-7c3d-8e9f-123456789005"
	aapConversationClientID    = "c38f1f2e-7b5a-7c3d-8e9f-123456789006"
	aapConversationGrantID     = "c38f1f2e-7b5a-7c3d-8e9f-123456789007"
	aapConversationTokenID     = "c38f1f2e-7b5a-7c3d-8e9f-123456789008"
	aapConversationKeyOne      = "c38f1f2e-7b5a-7c3d-8e9f-123456789009"
	aapConversationKeyTwo      = "c38f1f2e-7b5a-7c3d-8e9f-12345678900a"
	aapConversationRunID       = "c38f1f2e-7b5a-7c3d-8e9f-12345678900b"
)

func TestAAPConversationRoutes(t *testing.T) {
	store := &aapConversationStore{sessions: make(map[string]chat.Session)}
	runs := &aapConversationRuns{byConversation: make(map[string][]execution.AgentRun)}
	service, err := aap.NewConversationService(store, runs)
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &aapConversationAuthorizer{}
	routes, err := NewAAPConversationRoutes(authorizer, service)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: aapConversationTokenAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/agent-access/v1/workspaces/" + aapConversationWorkspaceID +
		"/agents/" + aapConversationAgentID + "/conversations"

	t.Run("creates permanent Subject-owned Conversation idempotently", func(t *testing.T) {
		created := requestAAPConversation(t, router, http.MethodPost, base,
			map[string]any{"title": "  Order support  "}, "external-a", aapConversationKeyOne, "")
		if created.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", created.Code, created.Body.String())
		}
		var first aapCreateConversationResponse
		if err := json.Unmarshal(created.Body.Bytes(), &first); err != nil {
			t.Fatal(err)
		}
		if first.Idempotent || first.Conversation.ID == "" || first.Conversation.Title != "Order support" ||
			first.Conversation.AgentID != aapConversationAgentID || first.Conversation.Status != "active" ||
			first.Conversation.Runs == nil || len(first.Conversation.Runs) != 0 ||
			created.Header().Get("Location") != base+"/"+first.Conversation.ID ||
			created.Header().Get("ETag") != `"conversation:1"` {
			t.Fatalf("response=%+v headers=%v", first, created.Header())
		}
		stored := store.sessions[first.Conversation.ID]
		if stored.Ownership.Identity.Actor.Type != principal.TypeServicePrincipal ||
			stored.Ownership.Identity.Actor.ID != aapConversationServiceID ||
			stored.Ownership.Identity.Subject == nil ||
			stored.Ownership.Identity.Subject.Type != principal.TypeExternalSubject ||
			stored.Ownership.Identity.Subject.ID != aapConversationSubjectAID ||
			stored.Ownership.ClientID != aapConversationClientID ||
			stored.Ownership.Mode != chat.OwnershipSubjectOwned || stored.Ownership.PolicyVersion != 11 ||
			stored.CreatedBy != "" {
			t.Fatalf("stored ownership=%+v", stored)
		}

		replay := requestAAPConversation(t, router, http.MethodPost, base,
			map[string]any{"title": "Order support"}, "external-a", aapConversationKeyOne, "")
		var repeated aapCreateConversationResponse
		if err := json.Unmarshal(replay.Body.Bytes(), &repeated); err != nil {
			t.Fatal(err)
		}
		if replay.Code != http.StatusOK || !repeated.Idempotent ||
			repeated.Conversation.ID != first.Conversation.ID || len(store.sessions) != 1 {
			t.Fatalf("replay status=%d response=%+v sessions=%d", replay.Code, repeated, len(store.sessions))
		}

		conflict := requestAAPConversation(t, router, http.MethodPost, base,
			map[string]any{"title": "Changed request"}, "external-a", aapConversationKeyOne, "")
		assertAAPRouterError(t, conflict, http.StatusConflict, "IDEMPOTENCY_CONFLICT")
		if len(store.sessions) != 1 {
			t.Fatalf("conflicting request created another Conversation: %d", len(store.sessions))
		}

		second := requestAAPConversation(t, router, http.MethodPost, base,
			map[string]any{"title": "Order support"}, "external-a", aapConversationKeyTwo, "")
		var secondBody aapCreateConversationResponse
		if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
			t.Fatal(err)
		}
		if second.Code != http.StatusCreated || secondBody.Conversation.ID == first.Conversation.ID ||
			len(store.sessions) != 2 {
			t.Fatalf("second status=%d response=%+v sessions=%d", second.Code, secondBody, len(store.sessions))
		}
	})

	conversationID := findAAPConversationByTitle(t, store, "Order support")
	now := time.Now().UTC().Truncate(time.Millisecond)
	finished := now.Add(time.Second)
	runs.byConversation[conversationID] = []execution.AgentRun{{
		ID: aapConversationRunID, WorkspaceID: aapConversationWorkspaceID,
		AgentID: aapConversationAgentID, SessionID: conversationID,
		Status: "SUCCEEDED", ErrorCode: "", StartedAt: now, FinishedAt: &finished,
		LockVersion: 3, ModelSnapshot: json.RawMessage(`{"apiKey":"model-secret"}`),
		CapabilitySnapshot: json.RawMessage(`{"connectionId":"connection-secret"}`),
	}}

	t.Run("reads only the caller-owned Conversation and public Run summaries", func(t *testing.T) {
		path := base + "/" + conversationID
		response := requestAAPConversation(t, router, http.MethodGet, path, nil, "external-a", "", "")
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var body aapConversationDTO
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.ID != conversationID || body.AgentID != aapConversationAgentID ||
			len(body.Runs) != 1 || body.Runs[0].ID != aapConversationRunID ||
			body.Runs[0].Status != "completed" || body.Runs[0].Version != 3 ||
			body.Runs[0].CompletedAt == nil || response.Header().Get("ETag") != `"conversation:1"` {
			t.Fatalf("body=%+v headers=%v", body, response.Header())
		}
		for _, forbidden := range []string{
			"model-secret", "connection-secret", "modelSnapshot", "capabilitySnapshot",
			"authorizationSnapshot", "actorId", "subjectId", "clientId", "grantId",
			"pendingConfirmationId", "createdBy",
		} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Fatalf("Conversation leaked %q: %s", forbidden, response.Body.String())
			}
		}
		conditional := requestAAPConversation(t, router, http.MethodGet, path, nil,
			"external-a", "", response.Header().Get("ETag"))
		if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
			t.Fatalf("conditional status=%d body=%s", conditional.Code, conditional.Body.String())
		}
	})

	t.Run("conceals another Subject and path scope before domain reads", func(t *testing.T) {
		beforeGets := store.getCalls
		otherSubject := requestAAPConversation(t, router, http.MethodGet, base+"/"+conversationID,
			nil, "external-b", "", "")
		assertAAPRouterError(t, otherSubject, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		if store.getCalls != beforeGets {
			t.Fatalf("denied Subject reached Conversation store")
		}

		wrongAgent := strings.Replace(base, aapConversationAgentID,
			"c38f1f2e-7b5a-7c3d-8e9f-123456789099", 1) + "/" + conversationID
		beforeAuthorizations := authorizer.calls
		response := requestAAPConversation(t, router, http.MethodGet, wrongAgent,
			nil, "external-a", "", "")
		assertAAPRouterError(t, response, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		if authorizer.calls != beforeAuthorizations {
			t.Fatalf("wrong Agent path reached authorizer")
		}
	})

	t.Run("fails closed if a stored Conversation points to another Agent", func(t *testing.T) {
		value := store.sessions[conversationID]
		value.AgentID = "c38f1f2e-7b5a-7c3d-8e9f-123456789099"
		store.sessions[conversationID] = value
		response := requestAAPConversation(t, router, http.MethodGet, base+"/"+conversationID,
			nil, "external-a", "", "")
		assertAAPRouterError(t, response, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		value.AgentID = aapConversationAgentID
		store.sessions[conversationID] = value
	})

	t.Run("supports a pure Service Principal without inventing a Subject", func(t *testing.T) {
		response := requestAAPConversation(t, router, http.MethodPost, base,
			map[string]any{"title": "Service-owned"}, "service", "c38f1f2e-7b5a-7c3d-8e9f-12345678900c", "")
		if response.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		serviceID := findAAPConversationByTitle(t, store, "Service-owned")
		if store.sessions[serviceID].Ownership.Identity.Subject != nil ||
			store.sessions[serviceID].Ownership.Identity.Actor.ID != aapConversationServiceID {
			t.Fatalf("pure Service ownership=%+v", store.sessions[serviceID].Ownership)
		}
	})

	t.Run("validates request and exposes no delete surface", func(t *testing.T) {
		missingKey := requestAAPConversation(t, router, http.MethodPost, base,
			map[string]any{"title": "No key"}, "external-a", "", "")
		assertAAPRouterError(t, missingKey, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
		unknownField := requestAAPConversation(t, router, http.MethodPost, base,
			map[string]any{"title": "test", "agentId": "switch-agent"},
			"external-a", aapConversationKeyTwo, "")
		assertAAPRouterError(t, unknownField, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
		tooLong := requestAAPConversation(t, router, http.MethodPost, base,
			map[string]any{"title": strings.Repeat("界", 257)}, "external-a",
			"c38f1f2e-7b5a-7c3d-8e9f-12345678900d", "")
		assertAAPRouterError(t, tooLong, http.StatusUnprocessableEntity, "VALIDATION_ERROR")

		deleted := requestAAPConversation(t, router, http.MethodDelete, base+"/"+conversationID,
			nil, "external-a", "", "")
		assertAAPRouterError(t, deleted, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		if _, ok := store.sessions[conversationID]; !ok {
			t.Fatal("unsupported DELETE changed permanent Conversation")
		}
	})

	if _, err := aap.NewConversationService(nil, runs); err == nil {
		t.Fatal("expected nil Conversation store rejection")
	}
	if _, err := aap.NewConversationService(store, nil); err == nil {
		t.Fatal("expected nil Run reader rejection")
	}
	if _, err := NewAAPConversationRoutes(nil, service); err == nil {
		t.Fatal("expected nil authorizer rejection")
	}
	if _, err := NewAAPConversationRoutes(authorizer, nil); err == nil {
		t.Fatal("expected nil Conversation application rejection")
	}
}

type aapConversationAuthorizer struct {
	calls int
}

func (authorizer *aapConversationAuthorizer) Authorize(
	_ context.Context,
	request agentaccessauth.AAPAuthorizationRequest,
) (agentaccessauth.AAPAuthorizationDecision, error) {
	authorizer.calls++
	if request.Action == agentaccessauth.ActionConversationRead &&
		request.Principal.PrincipalID == aapConversationSubjectBID {
		return agentaccessauth.AAPAuthorizationDecision{}, agentaccessauth.ErrAAPAuthorizationNotVisible
	}
	requiredScope := "conversation:create"
	resourceType := agentaccessauth.ResourceNone
	ownershipMode, ownershipVersion := "", int64(0)
	if request.Action == agentaccessauth.ActionConversationRead {
		requiredScope = "conversation:read"
		resourceType = agentaccessauth.ResourceConversation
		ownershipMode, ownershipVersion = "SUBJECT_OWNED", 11
	}
	snapshot := agentaccessauth.AAPAuthorizationSnapshot{
		SpecVersion: "aap.authorization.v1", WorkspaceID: request.Principal.WorkspaceID,
		AgentID: request.Principal.AgentID, ClientID: aapConversationClientID,
		AuthorizedParty:    request.Principal.AuthorizedParty,
		ServicePrincipalID: request.Principal.ServicePrincipalID,
		SubjectID:          request.Principal.PrincipalID, GrantID: aapConversationGrantID,
		Action: request.Action, RequiredScope: requiredScope,
		TokenScopes: []string{requiredScope}, GrantScopes: []string{requiredScope},
		AgentPolicyScopes: []string{requiredScope}, EffectiveScopes: []string{requiredScope},
		TokenSecurityVersion: 1, ResolvedSecurityVersion: 1,
		WorkspaceVersion: 1, ClientVersion: 1, GrantVersion: 7, AgentPolicyVersion: 11,
		TokenID: request.Principal.TokenID, ResourceType: resourceType,
		ResourceID: request.Resource.ID, OwnershipMode: ownershipMode,
		OwnershipPolicyVersion: ownershipVersion, AuthorizedAt: time.Now().UTC(),
	}
	return agentaccessauth.AAPAuthorizationDecision{
		EffectiveScopes: []string{requiredScope}, Snapshot: snapshot,
	}, nil
}

type aapConversationTokenAuthenticator struct{}

func (aapConversationTokenAuthenticator) VerifyAccessToken(
	_ context.Context,
	value string,
) (agentaccessauth.AAPAccessTokenPrincipal, error) {
	principalID := aapConversationSubjectAID
	switch value {
	case "external-a":
	case "external-b":
		principalID = aapConversationSubjectBID
	case "service":
		principalID = aapConversationServiceID
	default:
		return agentaccessauth.AAPAccessTokenPrincipal{}, errors.New("invalid Conversation token")
	}
	now := time.Now().UTC()
	return agentaccessauth.AAPAccessTokenPrincipal{
		PrincipalID: principalID, ServicePrincipalID: aapConversationServiceID,
		AuthorizedParty: "awcl_aap_conversation_client",
		WorkspaceID:     aapConversationWorkspaceID, AgentID: aapConversationAgentID,
		Scopes: []string{"conversation:create", "conversation:read"}, SecurityVersion: 1,
		TokenID: aapConversationTokenID, IssuedAt: now.Add(-time.Minute),
		NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(10 * time.Minute),
	}, nil
}

type aapConversationStore struct {
	sessions map[string]chat.Session
	getCalls int
}

func (store *aapConversationStore) CreateSession(
	_ context.Context,
	input chat.CreateSessionInput,
) (chat.Session, error) {
	if _, exists := store.sessions[input.ID]; exists {
		return chat.Session{}, chat.ErrConflict
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	value := chat.Session{
		ID: input.ID, WorkspaceID: input.WorkspaceID, AgentID: input.AgentID,
		Title: strings.TrimSpace(input.Title), Status: "ACTIVE",
		CreatedAt: now, UpdatedAt: now, LockVersion: 1, Ownership: *input.Ownership,
	}
	store.sessions[input.ID] = value
	return value, nil
}

func (store *aapConversationStore) GetSessionForPrincipal(
	_ context.Context,
	access chat.Access,
	conversationID string,
) (chat.Session, error) {
	store.getCalls++
	value, exists := store.sessions[conversationID]
	if !exists || value.Ownership.ClientID != access.ClientID ||
		value.Ownership.Identity.Actor != access.Identity.Actor ||
		!sameAAPConversationSubject(value.Ownership.Identity.Subject, access.Identity.Subject) ||
		(value.Ownership.Mode == chat.OwnershipPolicyShared && !access.AllowPolicyShared) {
		return chat.Session{}, chat.ErrNotFound
	}
	return value, nil
}

type aapConversationRuns struct {
	byConversation map[string][]execution.AgentRun
}

func (runs *aapConversationRuns) ListAgentRunsForConversation(
	_ context.Context,
	workspaceID, agentID, conversationID string,
	limit int,
) ([]execution.AgentRun, error) {
	if workspaceID != aapConversationWorkspaceID || agentID != aapConversationAgentID ||
		limit < 1 || limit > 100 {
		return nil, execution.ErrRunInvalid
	}
	values := runs.byConversation[conversationID]
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]execution.AgentRun(nil), values...), nil
}

func requestAAPConversation(
	t *testing.T,
	handler http.Handler,
	method, path string,
	body any,
	token, idempotencyKey, ifNoneMatchValue string,
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
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if ifNoneMatchValue != "" {
		request.Header.Set("If-None-Match", ifNoneMatchValue)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func findAAPConversationByTitle(
	t *testing.T,
	store *aapConversationStore,
	title string,
) string {
	t.Helper()
	for id, value := range store.sessions {
		if value.Title == title {
			return id
		}
	}
	t.Fatalf("Conversation %q not found", title)
	return ""
}

func sameAAPConversationSubject(left, right *principal.Ref) bool {
	return (left == nil && right == nil) ||
		(left != nil && right != nil && *left == *right)
}
