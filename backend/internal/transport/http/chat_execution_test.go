package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/authn"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/identity"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/workflow"
	"actweave/backend/internal/workflowcompiler"
	"actweave/backend/internal/workspace"

	"github.com/google/uuid"
)

func TestV1ChatSessionMessageArchiveAndPermanentOriginal(t *testing.T) {
	fixture := newChatExecutionAPIFixture(t)
	created := fixture.request(http.MethodPost, fixture.base+"/chat/sessions", map[string]any{
		"agentId": fixture.agentID, "title": "Orders",
	}, fixture.adminToken, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create session status=%d body=%s", created.Code, created.Body.String())
	}
	var session chatSessionDTO
	decodeResponse(t, created.Body.Bytes(), &session)
	if session.ID == "" || session.Status != "ACTIVE" || session.LockVersion != 1 {
		t.Fatalf("created session=%+v", session)
	}
	sent := fixture.request(http.MethodPost, fixture.base+"/chat/sessions/"+session.ID+"/messages", map[string]any{
		"content": "Keep this exact order request.",
	}, fixture.adminToken, nil)
	if sent.Code != http.StatusAccepted {
		t.Fatalf("send message status=%d body=%s", sent.Code, sent.Body.String())
	}
	var sentBody struct {
		Session chatSessionDTO `json:"session"`
		Message chatMessageDTO `json:"message"`
		RunID   string         `json:"runId"`
	}
	decodeResponse(t, sent.Body.Bytes(), &sentBody)
	if sentBody.Message.Content != "Keep this exact order request." ||
		sentBody.Message.Status != "PROCESSING" || sentBody.RunID == "" ||
		sentBody.Session.LockVersion != 2 {
		t.Fatalf("sent=%+v", sentBody)
	}
	detail := fixture.request(http.MethodGet, fixture.base+"/chat/sessions/"+session.ID, nil, fixture.adminToken, nil)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "Keep this exact order request.") {
		t.Fatalf("session detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	otherDetail := fixture.request(http.MethodGet, fixture.base+"/chat/sessions/"+session.ID, nil, fixture.otherToken, nil)
	assertErrorResponse(t, otherDetail, http.StatusNotFound, "NOT_FOUND")
	messageDelete := fixture.request(http.MethodDelete, fixture.base+"/chat/sessions/"+session.ID+"/messages/"+sentBody.Message.ID, nil, fixture.adminToken, nil)
	if messageDelete.Code != http.StatusNotFound {
		t.Fatalf("message delete route exists: status=%d body=%s", messageDelete.Code, messageDelete.Body.String())
	}
	archived := fixture.request(http.MethodPost, fixture.base+"/chat/sessions/"+session.ID+":archive", map[string]any{
		"lockVersion": sentBody.Session.LockVersion,
	}, fixture.adminToken, nil)
	if archived.Code != http.StatusOK || !strings.Contains(archived.Body.String(), `"status":"ARCHIVED"`) {
		t.Fatalf("archive status=%d body=%s", archived.Code, archived.Body.String())
	}
	rejected := fixture.request(http.MethodPost, fixture.base+"/chat/sessions/"+session.ID+"/messages", map[string]any{
		"content": "must not be accepted",
	}, fixture.adminToken, nil)
	assertErrorResponse(t, rejected, http.StatusConflict, "CONFLICT")
	list := fixture.request(http.MethodGet, fixture.base+"/chat/sessions", nil, fixture.adminToken, nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), session.ID) {
		t.Fatalf("session list status=%d body=%s", list.Code, list.Body.String())
	}
	otherList := fixture.request(http.MethodGet, fixture.base+"/chat/sessions", nil, fixture.otherToken, nil)
	if otherList.Code != http.StatusOK || strings.Contains(otherList.Body.String(), session.ID) {
		t.Fatalf("other user session list status=%d body=%s", otherList.Code, otherList.Body.String())
	}
}

func TestV1SendMessageEventsStreamReadyImmediately(t *testing.T) {
	// PR-U2: POST messages 202 commits agent_runs + protocol_event_streams;
	// immediate GET events must not 404 as "missing run/stream".
	fixture := newChatExecutionAPIFixture(t)
	created := fixture.request(http.MethodPost, fixture.base+"/chat/sessions", map[string]any{
		"agentId": fixture.agentID, "title": "Stream ready",
	}, fixture.adminToken, nil)
	var session chatSessionDTO
	decodeResponse(t, created.Body.Bytes(), &session)
	sent := fixture.request(http.MethodPost, fixture.base+"/chat/sessions/"+session.ID+"/messages", map[string]any{
		"content": "subscribe immediately",
	}, fixture.adminToken, nil)
	if sent.Code != http.StatusAccepted {
		t.Fatalf("send message status=%d body=%s", sent.Code, sent.Body.String())
	}
	var sentBody struct {
		RunID string `json:"runId"`
	}
	decodeResponse(t, sent.Body.Bytes(), &sentBody)
	if sentBody.RunID == "" {
		t.Fatal("expected runId on accepted send")
	}
	// No async Execute / event append — only SendMessage path must ready the stream.
	stream := fixture.request(http.MethodGet, fixture.base+"/agent-runs/"+sentBody.RunID+"/events?follow=false",
		nil, fixture.adminToken, nil)
	if stream.Code != http.StatusOK {
		t.Fatalf("immediate events status=%d body=%s (want 200, not ambiguous 404)",
			stream.Code, stream.Body.String())
	}
	if !strings.HasPrefix(stream.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("events content-type=%q body=%s", stream.Header().Get("Content-Type"), stream.Body.String())
	}
	missingRun := fixture.request(http.MethodGet, fixture.base+"/agent-runs/"+uuid.NewString()+"/events?follow=false",
		nil, fixture.adminToken, nil)
	assertErrorResponse(t, missingRun, http.StatusNotFound, "NOT_FOUND")
}

func TestV1ExecutionListDetailAndSSEContinuation(t *testing.T) {
	fixture := newChatExecutionAPIFixture(t)
	created := fixture.request(http.MethodPost, fixture.base+"/chat/sessions", map[string]any{
		"agentId": fixture.agentID, "title": "Events",
	}, fixture.adminToken, nil)
	var session chatSessionDTO
	decodeResponse(t, created.Body.Bytes(), &session)
	sent := fixture.request(http.MethodPost, fixture.base+"/chat/sessions/"+session.ID+"/messages", map[string]any{
		"content": "stream this run",
	}, fixture.adminToken, nil)
	var sentBody struct {
		RunID string `json:"runId"`
	}
	decodeResponse(t, sent.Body.Bytes(), &sentBody)
	for _, input := range []execution.AppendRunEventInput{
		{ID: uuid.NewString(), WorkspaceID: fixture.workspaceID, RunID: sentBody.RunID, EventType: "RUN_STARTED", Payload: json.RawMessage(`{"stage":"start"}`)},
		{ID: uuid.NewString(), WorkspaceID: fixture.workspaceID, RunID: sentBody.RunID, EventType: "STEP_STARTED", Payload: json.RawMessage(`{"step":"model"}`)},
	} {
		if _, err := fixture.events.Append(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	run := fixture.request(http.MethodGet, fixture.base+"/agent-runs/"+sentBody.RunID, nil, fixture.adminToken, nil)
	if run.Code != http.StatusOK || !strings.Contains(run.Body.String(), `"status":"RUNNING"`) {
		t.Fatalf("run detail status=%d body=%s", run.Code, run.Body.String())
	}
	stream := fixture.request(http.MethodGet, fixture.base+"/agent-runs/"+sentBody.RunID+"/events", nil, fixture.adminToken,
		map[string]string{"Last-Event-ID": "1"})
	if stream.Code != http.StatusOK || !strings.HasPrefix(stream.Header().Get("Content-Type"), "text/event-stream") ||
		strings.Contains(stream.Body.String(), "RUN_STARTED") ||
		!strings.Contains(stream.Body.String(), "id: 2\nevent: item.started") ||
		!strings.Contains(stream.Body.String(), `"type":"item.started"`) {
		t.Fatalf("SSE replay status=%d headers=%v body=%s", stream.Code, stream.Header(), stream.Body.String())
	}
	invalidCursor := fixture.request(http.MethodGet, fixture.base+"/agent-runs/"+sentBody.RunID+"/events", nil, fixture.adminToken,
		map[string]string{"Last-Event-ID": "bad"})
	assertErrorResponse(t, invalidCursor, http.StatusUnprocessableEntity, "REPLAY_CURSOR_INVALID")

	workflowID, executionID := fixture.createSuccessfulTrialExecution(t)
	list := fixture.request(http.MethodGet, fixture.base+"/executions?status=SUCCEEDED&workflowId="+workflowID, nil, fixture.adminToken, nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), executionID) {
		t.Fatalf("execution list status=%d body=%s", list.Code, list.Body.String())
	}
	detail := fixture.request(http.MethodGet, fixture.base+"/executions/"+executionID, nil, fixture.adminToken, nil)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"triggerType":"TRIAL"`) {
		t.Fatalf("execution detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	otherDetail := fixture.request(http.MethodGet, fixture.base+"/executions/"+executionID, nil, fixture.otherToken, nil)
	assertErrorResponse(t, otherDetail, http.StatusNotFound, "NOT_FOUND")
}

func TestV1ConfirmationIsRequesterOnlyAndIdempotent(t *testing.T) {
	fixture := newChatExecutionAPIFixture(t)
	confirmationID := fixture.confirmations.add(v1AdminUserID)
	other := fixture.request(http.MethodPost, fixture.base+"/confirmations/"+confirmationID+":confirm", map[string]any{
		"resumeToken": fixture.confirmations.token, "lockVersion": 1,
	}, fixture.otherToken, nil)
	assertErrorResponse(t, other, http.StatusForbidden, "FORBIDDEN")
	confirmed := fixture.request(http.MethodPost, fixture.base+"/confirmations/"+confirmationID+":confirm", map[string]any{
		"resumeToken": fixture.confirmations.token, "lockVersion": 1,
	}, fixture.adminToken, nil)
	if confirmed.Code != http.StatusOK || !strings.Contains(confirmed.Body.String(), `"status":"CONFIRMED"`) ||
		!strings.Contains(confirmed.Body.String(), `"cached":false`) {
		t.Fatalf("confirm status=%d body=%s", confirmed.Code, confirmed.Body.String())
	}
	repeated := fixture.request(http.MethodPost, fixture.base+"/confirmations/"+confirmationID+":confirm", map[string]any{
		"resumeToken": fixture.confirmations.token, "lockVersion": 1,
	}, fixture.adminToken, nil)
	if repeated.Code != http.StatusOK || !strings.Contains(repeated.Body.String(), `"cached":true`) || fixture.confirmations.effects != 1 {
		t.Fatalf("repeat confirm status=%d effects=%d body=%s", repeated.Code, fixture.confirmations.effects, repeated.Body.String())
	}
	cancelID := fixture.confirmations.add(v1AdminUserID)
	cancelled := fixture.request(http.MethodPost, fixture.base+"/confirmations/"+cancelID+":cancel", map[string]any{"lockVersion": 1}, fixture.adminToken, nil)
	repeatedCancel := fixture.request(http.MethodPost, fixture.base+"/confirmations/"+cancelID+":cancel", map[string]any{"lockVersion": 1}, fixture.adminToken, nil)
	if cancelled.Code != http.StatusOK || repeatedCancel.Code != http.StatusOK ||
		!strings.Contains(cancelled.Body.String(), `"status":"CANCELLED"`) ||
		!strings.Contains(repeatedCancel.Body.String(), `"cached":true`) {
		t.Fatalf("cancel status=%d/%d bodies=%s %s", cancelled.Code, repeatedCancel.Code, cancelled.Body.String(), repeatedCancel.Body.String())
	}
}

type chatAPISnapshots struct{ modelID string }

func (source chatAPISnapshots) SnapshotAgentRun(context.Context, string, string) (execution.AgentRunSnapshots, error) {
	return execution.AgentRunSnapshots{SchemaVersion: "run.v1",
		Model:        json.RawMessage(`{"modelConfigId":"` + source.modelID + `"}`),
		Capabilities: json.RawMessage(`{"releases":[]}`), ContextPolicy: json.RawMessage(`{}`)}, nil
}

type chatAPIRunAuthorization struct{}

func (chatAPIRunAuthorization) AuthorizeRun(context.Context, string, string, string, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{"decision":"ALLOW"}`), nil
}

type chatAPIContent struct{}

func (chatAPIContent) ReadPermanentChat(context.Context, string, string, string) (string, error) {
	return "", errors.New("unexpected stored content read")
}

type fakeConfirmationState struct {
	requestedBy string
	status      string
}

type fakeChatConfirmations struct {
	mu      sync.Mutex
	token   string
	states  map[string]*fakeConfirmationState
	effects int
}

func newFakeChatConfirmations() *fakeChatConfirmations {
	return &fakeChatConfirmations{token: strings.Repeat("r", 40), states: map[string]*fakeConfirmationState{}}
}

func (service *fakeChatConfirmations) add(requestedBy string) string {
	service.mu.Lock()
	defer service.mu.Unlock()
	id := uuid.NewString()
	service.states[id] = &fakeConfirmationState{requestedBy: requestedBy, status: execution.ConfirmationStatusPending}
	return id
}

func (service *fakeChatConfirmations) value(id string, state *fakeConfirmationState) chat.ChatConfirmation {
	return chat.ChatConfirmation{ID: id, WorkspaceID: "fixture", SessionID: uuid.Nil.String(),
		RunID: uuid.Nil.String(), ExecutionConfirmationID: uuid.Nil.String(), TargetType: execution.ResumeKindTool,
		TargetReleaseID: uuid.Nil.String(), RiskLevel: "HIGH", RiskReasons: []string{"high_risk"},
		InputSummary: json.RawMessage(`{"action":"test"}`), Status: state.status,
		RequestedBy: state.requestedBy, ExecutionLockVersion: 1,
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
}

func (service *fakeChatConfirmations) Confirm(_ context.Context, input chat.ConfirmChatConfirmationInput) (chat.ConfirmedChatConfirmation, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state := service.states[input.ConfirmationID]
	if state == nil {
		return chat.ConfirmedChatConfirmation{}, chat.ErrNotFound
	}
	if state.requestedBy != input.ActorID {
		return chat.ConfirmedChatConfirmation{}, execution.ErrConfirmationRequesterMismatch
	}
	if input.ResumeToken != service.token || input.ExpectedExecutionLockVersion != 1 {
		return chat.ConfirmedChatConfirmation{}, execution.ErrConfirmationTokenInvalid
	}
	cached := state.status == execution.ConfirmationStatusConfirmed
	if !cached {
		if state.status != execution.ConfirmationStatusPending {
			return chat.ConfirmedChatConfirmation{}, chat.ErrConflict
		}
		state.status = execution.ConfirmationStatusConfirmed
		service.effects++
	}
	value := service.value(input.ConfirmationID, state)
	value.ConfirmedBy = input.ActorID
	return chat.ConfirmedChatConfirmation{Confirmation: value, Cached: cached}, nil
}

func (service *fakeChatConfirmations) Cancel(_ context.Context, input chat.CancelChatConfirmationInput) (chat.CancelledChatConfirmation, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state := service.states[input.ConfirmationID]
	if state == nil {
		return chat.CancelledChatConfirmation{}, chat.ErrNotFound
	}
	if state.requestedBy != input.ActorID {
		return chat.CancelledChatConfirmation{}, execution.ErrConfirmationRequesterMismatch
	}
	if input.ExpectedExecutionLockVersion != 1 {
		return chat.CancelledChatConfirmation{}, chat.ErrConflict
	}
	cached := state.status == execution.ConfirmationStatusCancelled
	if !cached {
		if state.status != execution.ConfirmationStatusPending {
			return chat.CancelledChatConfirmation{}, chat.ErrConflict
		}
		state.status = execution.ConfirmationStatusCancelled
	}
	return chat.CancelledChatConfirmation{Confirmation: service.value(input.ConfirmationID, state), Cached: cached}, nil
}

type chatExecutionAPIFixture struct {
	router        http.Handler
	base          string
	workspaceID   string
	agentID       string
	adminToken    string
	otherToken    string
	events        *execution.RunEventRepository
	confirmations *fakeChatConfirmations
	workflowStore *workflow.Repository
}

func newChatExecutionAPIFixture(t *testing.T) *chatExecutionAPIFixture {
	t.Helper()
	authFixture := newV1AuthFixture(t)
	adminLogin := authFixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": v1AdminName, "password": v1AdminPass,
	}, "", nil)
	adminTokens := decodeTokenResponse(t, adminLogin)
	otherID := uuid.NewString()
	if _, err := authFixture.service.CreateUser(context.Background(), authn.CreateUserRequest{
		ID: otherID, Username: "v1.chat.other", DisplayName: "Chat Other",
		Password: "Chat-other-password-1", Status: identity.StatusActive,
		PlatformRole: identity.PlatformRoleUser, Locale: "zh-CN", Timezone: "Asia/Singapore",
	}); err != nil {
		t.Fatal(err)
	}
	otherLogin := authFixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "v1.chat.other", "password": "Chat-other-password-1",
	}, "", nil)
	otherTokens := decodeTokenResponse(t, otherLogin)

	ctx := context.Background()
	workspaceID := uuid.NewString()
	workspaces, err := workspace.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workspaces.Create(ctx, workspace.NewWorkspace{ID: workspaceID,
		Slug: "chat-api-" + workspaceID[:8], DisplayName: "Chat API", Mode: workspace.ModeProduction,
		OwnerUserID: v1AdminUserID, CreatedBy: v1AdminUserID}); err != nil {
		t.Fatal(err)
	}
	if _, err = workspaces.AddMember(ctx, workspace.NewMember{WorkspaceID: workspaceID,
		UserID: otherID, Role: workspace.RoleOperator, InvitedBy: v1AdminUserID}); err != nil {
		t.Fatal(err)
	}
	authorizer, err := authz.NewService(workspaces)
	if err != nil {
		t.Fatal(err)
	}
	models, err := modelconfig.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	modelID := uuid.NewString()
	if _, err = models.Create(ctx, modelconfig.NewConfig{ID: modelID, WorkspaceID: workspaceID,
		Name: "Chat Model", Provider: "test", APIBase: "https://models.example/v1",
		ModelName: "chat-model", Options: json.RawMessage(`{}`), CreatedBy: v1AdminUserID}); err != nil {
		t.Fatal(err)
	}
	agents, err := agent.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	agentID := uuid.NewString()
	if _, _, err = agents.Create(ctx, agent.NewAgent{ID: agentID, WorkspaceID: workspaceID,
		Name: "Chat Agent", ModelConfigID: modelID, InitialRevisionID: uuid.NewString(),
		InitialPrompt: "Help with orders.", PromptSource: "MANUAL", CreatedBy: v1AdminUserID}); err != nil {
		t.Fatal(err)
	}
	chatRepository, err := chat.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := execution.NewRunRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	runService, err := execution.NewRunService(runs, chatAPISnapshots{modelID}, chatAPIRunAuthorization{})
	if err != nil {
		t.Fatal(err)
	}
	chatService, err := chat.NewService(chatRepository, runs, runService)
	if err != nil {
		t.Fatal(err)
	}
	eventRepository, err := execution.NewRunEventRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	events := eventRepository
	protocolEvents, err := protocolevent.NewEventReader(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	confirmations := newFakeChatConfirmations()
	routes, err := NewChatExecutionRoutes(ChatExecutionDependencies{Authorizer: authorizer,
		Chats: chatRepository, Messages: chatService, Content: chatAPIContent{}, Runs: runs,
		ProtocolEvents: protocolEvents, Confirmations: confirmations})
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{Authenticator: authFixture.auth,
		Registrars: []V1RouteRegistrar{authFixture.authRoutes, routes}})
	if err != nil {
		t.Fatal(err)
	}
	workflowStore, err := workflow.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	return &chatExecutionAPIFixture{router: router, base: "/api/v1/workspaces/" + workspaceID,
		workspaceID: workspaceID, agentID: agentID, adminToken: adminTokens.AccessToken,
		otherToken: otherTokens.AccessToken, events: events, confirmations: confirmations,
		workflowStore: workflowStore}
}

func (fixture *chatExecutionAPIFixture) request(
	method, path string,
	body any,
	token string,
	headers map[string]string,
) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(body)
	if body == nil {
		payload = nil
	}
	request := httptest.NewRequest(method, path, strings.NewReader(string(payload)))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Request-ID", "request-v1-chat-test")
	request.Header.Set("X-Trace-ID", "trace-v1-chat-test")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	fixture.router.ServeHTTP(response, request)
	return response
}

func (fixture *chatExecutionAPIFixture) createSuccessfulTrialExecution(t *testing.T) (string, string) {
	t.Helper()
	graph, _ := json.Marshal(workflowAPIGraph(false))
	workflowID, draftID := uuid.NewString(), uuid.NewString()
	if _, _, err := fixture.workflowStore.Create(context.Background(), workflow.CreateInput{
		CapabilityID: workflowID, DraftID: draftID, WorkspaceID: fixture.workspaceID,
		Name: "Execution Workflow", Slug: "execution-" + workflowID[:8],
		SchemaVersion: "workflow.graph.v1", Graph: graph, CreatedBy: v1AdminUserID,
	}); err != nil {
		t.Fatal(err)
	}
	compiler, _ := workflow.NewCompilationService(fixture.workflowStore, workflowcompiler.New())
	compilation, err := compiler.Compile(context.Background(), fixture.workspaceID, workflowID, v1AdminUserID)
	if err != nil {
		t.Fatal(err)
	}
	trials, _ := workflow.NewTrialService(fixture.workflowStore, workflowAPITrialRunner{})
	trial, err := trials.Run(context.Background(), fixture.workspaceID, workflowID,
		compilation.ID, v1AdminUserID, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	return workflowID, trial.ExecutionID
}
