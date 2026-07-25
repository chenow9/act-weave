package httptransport

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
)

func TestAAPCancelRun(t *testing.T) {
	eventReader := &createRunEventReader{}
	attacher, err := NewAAPEventCatchUp(eventReader)
	if err != nil {
		t.Fatal(err)
	}
	runReader := &aapRunRouteReader{run: cancellableAAPRun()}
	canceller := &aapCancelRunApplication{run: runReader.run}
	authorizer := &aapRunRouteAuthorizer{}
	items := &aapRunRouteItems{values: []protocolevent.RunItemProjection{}}
	routes, err := NewAAPRunRoutes(
		authorizer, &aapRunRouteConversations{}, &aapRunRouteApplication{},
		runReader, items, attacher, canceller,
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
		"/agents/" + aapRunAgentID + "/runs/" + aapRunID
	path := base + ":cancel"

	t.Run("commits cancellation and returns the terminal Run", func(t *testing.T) {
		response := requestAAPRun(t, router, http.MethodPost, path, map[string]any{},
			"subject-a", "d41f1f2e-7b5a-7c3d-8e9f-123456789020", "application/json", "")
		if response.Code != http.StatusOK ||
			!strings.Contains(response.Body.String(), `"status":"cancelled"`) ||
			!strings.Contains(response.Body.String(), `"idempotent":false`) ||
			response.Header().Get("ETag") == "" || canceller.calls != 1 ||
			!canceller.runtimeNotified || canceller.last.Authorization.Snapshot.RequiredScope != "run:cancel" {
			t.Fatalf("status=%d headers=%v body=%s calls=%d runtime=%v input=%+v",
				response.Code, response.Header(), response.Body.String(), canceller.calls,
				canceller.runtimeNotified, canceller.last)
		}
	})

	t.Run("terminal repeated cancellation is idempotent", func(t *testing.T) {
		response := requestAAPRun(t, router, http.MethodPost, path, nil,
			"subject-a", "d41f1f2e-7b5a-7c3d-8e9f-123456789020", "application/json", "")
		if response.Code != http.StatusOK ||
			!strings.Contains(response.Body.String(), `"idempotent":true`) || canceller.calls != 2 {
			t.Fatalf("status=%d body=%s calls=%d", response.Code, response.Body.String(), canceller.calls)
		}
	})

	t.Run("completed and failed Runs are not cancellable", func(t *testing.T) {
		for _, status := range []string{"SUCCEEDED", "FAILED"} {
			canceller.run = cancellableAAPRun()
			canceller.run.Status = status
			finished := time.Now().UTC()
			canceller.run.FinishedAt = &finished
			response := requestAAPRun(t, router, http.MethodPost, path, nil,
				"subject-a", "d41f1f2e-7b5a-7c3d-8e9f-123456789021", "application/json", "")
			assertAAPRouterError(t, response, http.StatusConflict, "RUN_NOT_CANCELLABLE")
		}
	})

	t.Run("validates command shape before mutation", func(t *testing.T) {
		before := canceller.calls
		missingKey := requestAAPRun(t, router, http.MethodPost, path, nil,
			"subject-a", "", "application/json", "")
		assertAAPRouterError(t, missingKey, http.StatusUnprocessableEntity, "VALIDATION_ERROR")

		unknownBody := requestAAPRun(t, router, http.MethodPost, path,
			map[string]any{"reason": "override"}, "subject-a",
			"d41f1f2e-7b5a-7c3d-8e9f-123456789022", "application/json", "")
		assertAAPRouterError(t, unknownBody, http.StatusUnprocessableEntity, "VALIDATION_ERROR")

		wrongCommand := requestAAPRun(t, router, http.MethodPost, base+":stop", nil,
			"subject-a", "d41f1f2e-7b5a-7c3d-8e9f-123456789022", "application/json", "")
		assertAAPRouterError(t, wrongCommand, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		if canceller.calls != before {
			t.Fatalf("invalid command mutated Run: before=%d after=%d", before, canceller.calls)
		}
	})

	t.Run("conceals another Subject before cancellation", func(t *testing.T) {
		before := canceller.calls
		response := requestAAPRun(t, router, http.MethodPost, path, nil,
			"subject-b", "d41f1f2e-7b5a-7c3d-8e9f-123456789023", "application/json", "")
		assertAAPRouterError(t, response, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		if canceller.calls != before {
			t.Fatal("denied Subject reached cancellation service")
		}
	})
}

type aapCancelRunApplication struct {
	run             execution.AgentRun
	last            aap.CancelRunInput
	calls           int
	runtimeNotified bool
}

func (application *aapCancelRunApplication) Cancel(
	_ context.Context,
	input aap.CancelRunInput,
) (aap.CancelRunResult, error) {
	application.calls++
	application.last = input
	if application.run.Status == "SUCCEEDED" || application.run.Status == "FAILED" {
		return aap.CancelRunResult{}, aap.ErrRunNotCancellable
	}
	if application.run.Status == "CANCELLED" {
		return aap.CancelRunResult{Run: application.run, Idempotent: true}, nil
	}
	application.run.Status = "CANCELLED"
	application.run.LockVersion++
	finished := time.Now().UTC()
	application.run.FinishedAt = &finished
	application.runtimeNotified = true
	return aap.CancelRunResult{
		Run: application.run,
		CancelledEvent: protocolevent.ProtocolEvent{
			Type: protocolevent.EventRunCancelled,
		},
	}, nil
}

func cancellableAAPRun() execution.AgentRun {
	return execution.AgentRun{
		ID: aapRunID, WorkspaceID: aapRunWorkspaceID, AgentID: aapRunAgentID,
		SessionID: aapRunConversationID, Status: "RUNNING", TriggerType: "API",
		StartedAt: time.Now().UTC().Add(-time.Second), LockVersion: 1,
	}
}
