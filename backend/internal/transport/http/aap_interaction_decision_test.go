package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
)

const (
	aapInteractionID  = "d41f1f2e-7b5a-7c3d-8e9f-123456789030"
	aapInteractionKey = "d41f1f2e-7b5a-7c3d-8e9f-123456789031"
	interactionTarget = "d41f1f2e-7b5a-7c3d-8e9f-123456789032"
)

func TestAAPInteractionDecision(t *testing.T) {
	reader := &aapRunRouteReader{run: cancellableAAPRun()}
	attacher, err := NewAAPEventCatchUp(&createRunEventReader{})
	if err != nil {
		t.Fatal(err)
	}
	decider := &aapInteractionDecisionApplication{}
	routes, err := NewAAPRunRoutes(
		&aapRunRouteAuthorizer{}, &aapRunRouteConversations{},
		&aapRunRouteApplication{}, reader, &aapRunRouteItems{}, attacher,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.ConfigureInteractionDecisions(decider); err != nil {
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
		"/agents/" + aapRunAgentID + "/runs/" + aapRunID +
		"/interactions/" + aapInteractionID + ":decide"

	accepted := requestAAPInteractionDecision(
		t, router, path, "subject-a", aapInteractionKey, `"1"`,
		map[string]any{"decision": "approve"},
	)
	if accepted.Code != http.StatusOK || accepted.Header().Get("ETag") != `"2"` ||
		!strings.Contains(accepted.Body.String(), `"status":"approved"`) ||
		!strings.Contains(accepted.Body.String(), `"idempotent":false`) {
		t.Fatalf("status=%d headers=%v body=%s", accepted.Code, accepted.Header(), accepted.Body.String())
	}
	decider.mu.Lock()
	first := decider.last
	decider.mu.Unlock()
	if first.ExpectedVersion != 1 || first.InteractionID != aapInteractionID ||
		first.Authorization.Snapshot.RequiredScope != "interaction:decide" ||
		first.Authorization.Snapshot.ResourceType != "INTERACTION" {
		t.Fatalf("decision input=%+v authorization=%+v", first, first.Authorization.Snapshot)
	}

	repeated := requestAAPInteractionDecision(
		t, router, path, "subject-a", aapInteractionKey, `"1"`,
		map[string]any{"decision": "approve"},
	)
	if repeated.Code != http.StatusOK ||
		!strings.Contains(repeated.Body.String(), `"idempotent":true`) {
		t.Fatalf("repeated status=%d body=%s", repeated.Code, repeated.Body.String())
	}

	changed := requestAAPInteractionDecision(
		t, router, path, "subject-a", aapInteractionKey, `"1"`,
		map[string]any{"decision": "cancel"},
	)
	assertAAPRouterError(t, changed, http.StatusConflict, "IDEMPOTENCY_CONFLICT")

	for name, etag := range map[string]string{
		"missing": "", "weak": `W/"1"`, "unquoted": "1", "list": `"1","2"`,
	} {
		t.Run("rejects "+name+" If-Match", func(t *testing.T) {
			response := requestAAPInteractionDecision(
				t, router, path, "subject-a",
				"d41f1f2e-7b5a-7c3d-8e9f-123456789033", etag,
				map[string]any{"decision": "approve"},
			)
			assertAAPRouterError(t, response, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
		})
	}

	denied := requestAAPInteractionDecision(
		t, router, path, "subject-b",
		"d41f1f2e-7b5a-7c3d-8e9f-123456789034", `"1"`,
		map[string]any{"decision": "approve"},
	)
	assertAAPRouterError(t, denied, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

type aapInteractionDecisionApplication struct {
	mu       sync.Mutex
	last     aap.DecideInteractionInput
	accepted *aap.DecideInteractionInput
}

func (application *aapInteractionDecisionApplication) Decide(
	_ context.Context,
	input aap.DecideInteractionInput,
) (aap.DecideInteractionResult, error) {
	application.mu.Lock()
	defer application.mu.Unlock()
	application.last = input
	if application.accepted != nil {
		if application.accepted.IdempotencyKey != input.IdempotencyKey {
			return aap.DecideInteractionResult{}, execution.ErrInteractionAlreadyResolved
		}
		if application.accepted.Decision != input.Decision ||
			application.accepted.ExpectedVersion != input.ExpectedVersion {
			return aap.DecideInteractionResult{}, execution.ErrInteractionIdempotencyConflict
		}
		return aapInteractionDecisionResult(true), nil
	}
	copy := input
	application.accepted = &copy
	return aapInteractionDecisionResult(false), nil
}

func aapInteractionDecisionResult(idempotent bool) aap.DecideInteractionResult {
	return aap.DecideInteractionResult{
		Interaction: protocolevent.Interaction{
			ID: aapInteractionID, Kind: protocolevent.InteractionKindApproval,
			Status:       protocolevent.InteractionStatusApproved,
			TargetItemID: interactionTarget, RunID: aapRunID,
			ReleaseID: "d41f1f2e-7b5a-7c3d-8e9f-123456789035",
			InputHash: strings.Repeat("a", 64), Title: "Approve action",
			Reason: "external side effect",
			Risk: protocolevent.InteractionRisk{
				Level: protocolevent.RiskLevelMedium, Reasons: []string{"external_side_effect"},
			},
			InputSummary: json.RawMessage(`{"safe":true}`),
			AllowedDecisions: []protocolevent.InteractionDecision{
				protocolevent.InteractionDecisionApprove,
				protocolevent.InteractionDecisionDecline,
				protocolevent.InteractionDecisionCancel,
			},
			RequiredDecider: protocolevent.RequiredDeciderSameExternalSubject,
			Version:         2, ExpiresAt: time.Now().UTC().Add(time.Minute),
		},
		Idempotent: idempotent,
	}
}

func requestAAPInteractionDecision(
	t *testing.T,
	handler http.Handler,
	path, token, idempotencyKey, ifMatch string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
