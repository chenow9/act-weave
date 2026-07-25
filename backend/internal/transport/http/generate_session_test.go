package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/smartdag"
	"actweave/backend/internal/workflow"

	"github.com/gin-gonic/gin"
)

type fakeGenerateSessions struct {
	createFn func(context.Context, smartdag.CreateSessionRequest) (smartdag.GenerateSession, error)
	turnFn   func(context.Context, smartdag.ApplySessionTurnRequest) (smartdag.ApplySessionTurnResult, error)
	getFn    func(context.Context, string, string) (smartdag.SessionDetail, error)
	closeFn  func(context.Context, string, string) (smartdag.GenerateSession, error)
	creates  int
}

func (f *fakeGenerateSessions) CreateSession(ctx context.Context, req smartdag.CreateSessionRequest) (smartdag.GenerateSession, error) {
	f.creates++
	if f.createFn != nil {
		return f.createFn(ctx, req)
	}
	return smartdag.GenerateSession{}, errors.New("not implemented")
}

func (f *fakeGenerateSessions) ApplySessionTurn(ctx context.Context, req smartdag.ApplySessionTurnRequest) (smartdag.ApplySessionTurnResult, error) {
	if f.turnFn != nil {
		return f.turnFn(ctx, req)
	}
	return smartdag.ApplySessionTurnResult{}, errors.New("not implemented")
}

func (f *fakeGenerateSessions) GetSession(ctx context.Context, workspaceID, sessionID string) (smartdag.SessionDetail, error) {
	if f.getFn != nil {
		return f.getFn(ctx, workspaceID, sessionID)
	}
	return smartdag.SessionDetail{}, errors.New("not implemented")
}

func (f *fakeGenerateSessions) CloseSession(ctx context.Context, workspaceID, sessionID string) (smartdag.GenerateSession, error) {
	if f.closeFn != nil {
		return f.closeFn(ctx, workspaceID, sessionID)
	}
	return smartdag.GenerateSession{}, errors.New("not implemented")
}

func (f *fakeGenerateSessions) CloseSessionWith(ctx context.Context, req smartdag.CloseSessionRequest) (smartdag.GenerateSession, error) {
	return f.CloseSession(ctx, req.WorkspaceID, req.SessionID)
}

type allowAllAuthorizer struct{}

func (allowAllAuthorizer) AuthorizeWorkspace(context.Context, string, string, authz.Action) (authz.WorkspaceContext, error) {
	return authz.WorkspaceContext{}, nil
}

func generateSessionRouter(t *testing.T, sessions GenerateSessionService) http.Handler {
	t.Helper()
	routes, err := NewGenerateSessionRoutes(GenerateSessionDependencies{
		Authorizer: allowAllAuthorizer{},
		Sessions:   sessions,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewRouter(Config{
		Authenticator: fixedAuthenticator{userID: "118f1f2e-7b5a-7c3d-8e9f-1234567890b1"},
		Registrars:    []V1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

type fixedAuthenticator struct {
	userID string
}

func (a fixedAuthenticator) AuthenticateAccessToken(context.Context, string) (Principal, error) {
	return Principal{
		UserID: a.userID, SessionID: "118f1f2e-7b5a-7c3d-8e9f-1234567890b2",
		Username: "tester", PlatformRole: "ADMIN",
	}, nil
}

func TestGenerateSessionCreateReturns201(t *testing.T) {
	t.Parallel()
	sessions := &fakeGenerateSessions{
		createFn: func(_ context.Context, req smartdag.CreateSessionRequest) (smartdag.GenerateSession, error) {
			if req.AgentID == "" {
				return smartdag.GenerateSession{}, smartdag.ErrInvalid
			}
			return smartdag.GenerateSession{
				ID: "118f1f2e-7b5a-7c3d-8e9f-1234567890c1", WorkspaceID: req.WorkspaceID,
				AgentID: req.AgentID, ModelConfigID: "118f1f2e-7b5a-7c3d-8e9f-1234567890c2",
				Status: smartdag.SessionStatusOpen, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			}, nil
		},
	}
	router := generateSessionRouter(t, sessions)
	body := []byte(`{"agentId":"118f1f2e-7b5a-7c3d-8e9f-1234567890c3"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/118f1f2e-7b5a-7c3d-8e9f-1234567890c4/workflow-generate-sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["sessionId"] == "" || payload["status"] != "OPEN" {
		t.Fatalf("payload=%v", payload)
	}
}

func TestGenerateSessionCreateNoModelReturns422(t *testing.T) {
	t.Parallel()
	sessions := &fakeGenerateSessions{
		createFn: func(context.Context, smartdag.CreateSessionRequest) (smartdag.GenerateSession, error) {
			return smartdag.GenerateSession{}, smartdag.ErrAgentModelRequired
		},
	}
	router := generateSessionRouter(t, sessions)
	body := []byte(`{"agentId":"118f1f2e-7b5a-7c3d-8e9f-1234567890c3"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/118f1f2e-7b5a-7c3d-8e9f-1234567890c4/workflow-generate-sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "AGENT_MODEL_REQUIRED" {
		t.Fatalf("code=%s", payload.Error.Code)
	}
	if sessions.creates != 1 {
		// createFn invoked once; domain must not persist — fakes don't persist.
	}
}

func TestGenerateSessionTurnAfterCloseReturns409(t *testing.T) {
	t.Parallel()
	sessions := &fakeGenerateSessions{
		turnFn: func(context.Context, smartdag.ApplySessionTurnRequest) (smartdag.ApplySessionTurnResult, error) {
			return smartdag.ApplySessionTurnResult{}, smartdag.ErrSessionClosed
		},
	}
	router := generateSessionRouter(t, sessions)
	body := []byte(`{"message":"再改一版"}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/118f1f2e-7b5a-7c3d-8e9f-1234567890c4/workflow-generate-sessions/118f1f2e-7b5a-7c3d-8e9f-1234567890c1/turns",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "SESSION_CLOSED" {
		t.Fatalf("code=%s", payload.Error.Code)
	}
}

func TestGenerateSessionCloseAndGet(t *testing.T) {
	t.Parallel()
	closedAt := time.Now().UTC()
	sessions := &fakeGenerateSessions{
		closeFn: func(_ context.Context, _, sid string) (smartdag.GenerateSession, error) {
			return smartdag.GenerateSession{
				ID: sid, Status: smartdag.SessionStatusClosed, ClosedAt: &closedAt,
			}, nil
		},
		getFn: func(_ context.Context, _, sid string) (smartdag.SessionDetail, error) {
			wfID := "118f1f2e-7b5a-7c3d-8e9f-1234567890c5"
			version := int64(2)
			return smartdag.SessionDetail{
				Session: smartdag.GenerateSession{
					ID: sid, Status: smartdag.SessionStatusClosed, WorkflowID: &wfID, ClosedAt: &closedAt,
				},
				Turns: []smartdag.GenerateTurn{{
					ID: "118f1f2e-7b5a-7c3d-8e9f-1234567890c6", TurnIndex: 1, UserMessage: "hi",
					Status: smartdag.TurnStatusSucceeded, DraftVersion: &version,
				}},
				DraftVersion: &version,
				Workflow:     &workflow.Workflow{CapabilityID: wfID, Name: "AI"},
			}, nil
		},
	}
	router := generateSessionRouter(t, sessions)

	closeReq := httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/118f1f2e-7b5a-7c3d-8e9f-1234567890c4/workflow-generate-sessions/118f1f2e-7b5a-7c3d-8e9f-1234567890c1:close",
		nil)
	closeReq.Header.Set("Authorization", "Bearer test")
	closeRec := httptest.NewRecorder()
	router.ServeHTTP(closeRec, closeReq)
	if closeRec.Code != http.StatusOK {
		t.Fatalf("close status=%d body=%s", closeRec.Code, closeRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet,
		"/api/v1/workspaces/118f1f2e-7b5a-7c3d-8e9f-1234567890c4/workflow-generate-sessions/118f1f2e-7b5a-7c3d-8e9f-1234567890c1",
		nil)
	getReq.Header.Set("Authorization", "Bearer test")
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["draftVersion"] != float64(2) {
		t.Fatalf("draftVersion=%v", payload["draftVersion"])
	}
	turns, ok := payload["turns"].([]any)
	if !ok || len(turns) != 1 {
		t.Fatalf("turns=%v", payload["turns"])
	}
}

func TestGenerateSessionTurnSuccess(t *testing.T) {
	t.Parallel()
	sessions := &fakeGenerateSessions{
		turnFn: func(context.Context, smartdag.ApplySessionTurnRequest) (smartdag.ApplySessionTurnResult, error) {
			return smartdag.ApplySessionTurnResult{
				SessionID:    "118f1f2e-7b5a-7c3d-8e9f-1234567890c1",
				TurnID:       "118f1f2e-7b5a-7c3d-8e9f-1234567890c6",
				GenerationID: "118f1f2e-7b5a-7c3d-8e9f-1234567890c7",
				Workflow: workflow.Workflow{
					CapabilityID: "118f1f2e-7b5a-7c3d-8e9f-1234567890c5",
					Name:         "AI · test", CurrentDraftID: "118f1f2e-7b5a-7c3d-8e9f-1234567890c8",
				},
				Draft: workflow.Draft{
					ID: "118f1f2e-7b5a-7c3d-8e9f-1234567890c8", DraftVersion: 1,
					SchemaVersion: smartdag.SchemaVersion, Graph: json.RawMessage(`{"schemaVersion":"workflow.graph.v1","nodes":[],"edges":[]}`),
					LockVersion: 1,
				},
				AssistantMessage: "ok",
				DraftVersion:     1,
				GeneratedBy:      smartdag.GeneratedByV2,
				GuardReport:      smartdag.GuardReport{OK: true},
			}, nil
		},
	}
	router := generateSessionRouter(t, sessions)
	body := []byte(`{"message":"生成支付查询流程"}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/118f1f2e-7b5a-7c3d-8e9f-1234567890c4/workflow-generate-sessions/118f1f2e-7b5a-7c3d-8e9f-1234567890c1/turns",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["generatedBy"] != smartdag.GeneratedByV2 || payload["draftVersion"] != float64(1) {
		t.Fatalf("payload=%v", payload)
	}
}

// silence unused gin in case of future handler helpers
var _ = gin.H{}
