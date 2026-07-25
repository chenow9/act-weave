package httptransport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/smartdag"
	"actweave/backend/internal/tool"
	"actweave/backend/internal/workflow"
	"actweave/backend/internal/workflowcompiler"
	"actweave/backend/internal/workspace"

	"github.com/google/uuid"
)

func TestV1ProductionExecuteReturns202AndIdempotentReplay(t *testing.T) {
	fixture := newWorkflowAPIFixture(t)
	created := fixture.request(http.MethodPost, fixture.base, map[string]any{
		"name": "Production execute", "slug": "production-execute", "description": "Prod",
		"schemaVersion": "workflow.graph.v1", "graph": workflowAPIGraph(false),
	}, nil)
	var createdBody struct {
		Workflow workflowDTO `json:"workflow"`
	}
	decodeResponse(t, created.Body.Bytes(), &createdBody)
	// Fixture executor does not require a real active revision id; use any UUID.
	revisionID := uuid.NewString()
	executePath := fixture.base + "/" + createdBody.Workflow.ID + "/revisions/" + revisionID + ":execute"
	first := fixture.request(http.MethodPost, executePath, map[string]any{
		"input": map[string]any{"orderId": "P-1"}, "trigger": "console",
	}, map[string]string{"Idempotency-Key": "prod-key-1"})
	if first.Code != http.StatusAccepted {
		t.Fatalf("execute status=%d body=%s", first.Code, first.Body.String())
	}
	var firstBody productionExecuteDTO
	decodeResponse(t, first.Body.Bytes(), &firstBody)
	if firstBody.ExecutionID == "" || firstBody.WorkflowID != createdBody.Workflow.ID ||
		firstBody.RevisionID != revisionID || firstBody.Status == "" || firstBody.TraceID == "" {
		t.Fatalf("execute body=%+v", firstBody)
	}
	second := fixture.request(http.MethodPost, executePath, map[string]any{
		"input": map[string]any{"orderId": "P-1"}, "trigger": "console",
	}, map[string]string{"Idempotency-Key": "prod-key-1"})
	if second.Code != http.StatusAccepted {
		t.Fatalf("idempotent execute status=%d body=%s", second.Code, second.Body.String())
	}
	var secondBody productionExecuteDTO
	decodeResponse(t, second.Body.Bytes(), &secondBody)
	if secondBody.ExecutionID != firstBody.ExecutionID {
		t.Fatalf("idempotent replay changed executionId first=%s second=%s",
			firstBody.ExecutionID, secondBody.ExecutionID)
	}
	// Non-active marker (fixture convention).
	inactive := fixture.request(http.MethodPost,
		fixture.base+"/"+createdBody.Workflow.ID+"/revisions/00000000-0000-4000-8000-000000000001:execute",
		map[string]any{"input": map[string]any{}}, nil)
	assertErrorResponse(t, inactive, http.StatusConflict, "REVISION_NOT_EXECUTABLE")
}

func TestV1WorkflowLifecycleUsesCASAndImmutableCompilationIDs(t *testing.T) {
	fixture := newWorkflowAPIFixture(t)
	created := fixture.request(http.MethodPost, fixture.base, map[string]any{
		"name": "Order approval", "slug": "order-approval", "description": "Approve orders",
		"schemaVersion": "workflow.graph.v1", "graph": workflowAPIGraph(false),
	}, nil)
	if created.Code != http.StatusCreated || created.Header().Get("ETag") == "" {
		t.Fatalf("create workflow status=%d headers=%v body=%s", created.Code, created.Header(), created.Body.String())
	}
	var createdBody struct {
		Workflow workflowDTO `json:"workflow"`
		Draft    draftDTO    `json:"draft"`
	}
	decodeResponse(t, created.Body.Bytes(), &createdBody)
	if createdBody.Workflow.ID == "" || createdBody.Draft.DraftVersion != 1 || createdBody.Draft.LockVersion != 1 ||
		createdBody.Workflow.NodeCount != 2 || createdBody.Workflow.EdgeCount != 1 {
		t.Fatalf("created workflow=%+v draft=%+v", createdBody.Workflow, createdBody.Draft)
	}

	rejectedLegacy := fixture.request(http.MethodPut, fixture.base+"/"+createdBody.Workflow.ID+"/draft", map[string]any{
		"schemaVersion": "workflow.graph.v1", "graph": workflowAPIGraph(true),
		"draftVersion": 1, "lockVersion": 1, "dsl": map[string]any{},
	}, map[string]string{"If-Match": created.Header().Get("ETag")})
	assertErrorResponse(t, rejectedLegacy, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	rejectedCanvas := fixture.request(http.MethodPost, fixture.base, map[string]any{
		"name": "Legacy", "slug": "legacy", "schemaVersion": "workflow.graph.v1",
		"graph": workflowAPIGraph(false), "canvasGraph": map[string]any{},
	}, nil)
	assertErrorResponse(t, rejectedCanvas, http.StatusUnprocessableEntity, "VALIDATION_ERROR")

	missingPrecondition := fixture.request(http.MethodPut, fixture.base+"/"+createdBody.Workflow.ID+"/draft", map[string]any{
		"schemaVersion": "workflow.graph.v1", "graph": workflowAPIGraph(true),
		"draftVersion": 1, "lockVersion": 1,
	}, nil)
	assertErrorResponse(t, missingPrecondition, http.StatusConflict, "CONFLICT")

	updated := fixture.request(http.MethodPut, fixture.base+"/"+createdBody.Workflow.ID+"/draft", map[string]any{
		"schemaVersion": "workflow.graph.v1", "graph": workflowAPIGraph(true),
		"draftVersion": 1, "lockVersion": 1,
	}, map[string]string{"If-Match": created.Header().Get("ETag")})
	if updated.Code != http.StatusOK || updated.Header().Get("ETag") == created.Header().Get("ETag") {
		t.Fatalf("update draft status=%d headers=%v body=%s", updated.Code, updated.Header(), updated.Body.String())
	}
	var updatedDraft draftDTO
	decodeResponse(t, updated.Body.Bytes(), &updatedDraft)
	if updatedDraft.DraftVersion != 2 || updatedDraft.LockVersion != 2 {
		t.Fatalf("updated draft=%+v", updatedDraft)
	}
	stale := fixture.request(http.MethodPut, fixture.base+"/"+createdBody.Workflow.ID+"/draft", map[string]any{
		"schemaVersion": "workflow.graph.v1", "graph": workflowAPIGraph(false),
		"draftVersion": 1, "lockVersion": 1,
	}, map[string]string{"If-Match": created.Header().Get("ETag")})
	assertErrorResponse(t, stale, http.StatusConflict, "CONFLICT")

	firstCompilation := fixture.request(http.MethodPost, fixture.base+"/"+createdBody.Workflow.ID+"/draft:compile", nil, nil)
	if firstCompilation.Code != http.StatusCreated {
		t.Fatalf("compile status=%d body=%s", firstCompilation.Code, firstCompilation.Body.String())
	}
	var compilationOne compilationDTO
	decodeResponse(t, firstCompilation.Body.Bytes(), &compilationOne)
	if compilationOne.ID == "" || compilationOne.DraftVersion != 2 || compilationOne.Status != "VALID" {
		t.Fatalf("first compilation=%+v", compilationOne)
	}
	trialOne := fixture.request(http.MethodPost, fixture.base+"/"+createdBody.Workflow.ID+"/compilations/"+compilationOne.ID+":trial", map[string]any{
		"input": map[string]any{"orderId": "order-1"},
	}, nil)
	if trialOne.Code != http.StatusOK || !strings.Contains(trialOne.Body.String(), `"status":"SUCCEEDED"`) {
		t.Fatalf("trial status=%d body=%s", trialOne.Code, trialOne.Body.String())
	}
	var trialOneValue trialDTO
	decodeResponse(t, trialOne.Body.Bytes(), &trialOneValue)
	var executionCompilationID, executionStatus string
	var executionHasRevision bool
	if err := fixture.db.QueryRow(`
		SELECT compilation_id,status,revision_id IS NOT NULL
		FROM workflow_executions WHERE id=$1
	`, trialOneValue.ExecutionID).Scan(
		&executionCompilationID, &executionStatus, &executionHasRevision,
	); err != nil || executionCompilationID != compilationOne.ID ||
		executionStatus != "SUCCEEDED" || executionHasRevision {
		t.Fatalf("trial execution source compilation=%s status=%s hasRevision=%t err=%v",
			executionCompilationID, executionStatus, executionHasRevision, err)
	}
	ready := fixture.request(http.MethodGet, fixture.base+"/"+createdBody.Workflow.ID+"/readiness", nil, nil)
	if ready.Code != http.StatusOK || !strings.Contains(ready.Body.String(), `"stage":"PUBLISH_READY"`) {
		t.Fatalf("readiness status=%d body=%s", ready.Code, ready.Body.String())
	}
	firstPublish := fixture.publish(createdBody.Workflow.ID, compilationOne.ID, "first")
	if firstPublish.Code != http.StatusCreated {
		t.Fatalf("first publish status=%d body=%s", firstPublish.Code, firstPublish.Body.String())
	}
	var publishedOne struct {
		Revision  revisionDTO `json:"revision"`
		ReleaseID string      `json:"releaseId"`
	}
	decodeResponse(t, firstPublish.Body.Bytes(), &publishedOne)
	if publishedOne.Revision.SourceCompilationID != compilationOne.ID || publishedOne.ReleaseID == "" {
		t.Fatalf("first publish=%+v", publishedOne)
	}

	draftRead := fixture.request(http.MethodGet, fixture.base+"/"+createdBody.Workflow.ID+"/draft", nil, nil)
	var currentDraft draftDTO
	decodeResponse(t, draftRead.Body.Bytes(), &currentDraft)
	secondDraft := fixture.request(http.MethodPut, fixture.base+"/"+createdBody.Workflow.ID+"/draft", map[string]any{
		"schemaVersion": "workflow.graph.v1", "graph": workflowAPIGraph(false),
		"draftVersion": currentDraft.DraftVersion, "lockVersion": currentDraft.LockVersion,
	}, map[string]string{"If-Match": draftRead.Header().Get("ETag")})
	if secondDraft.Code != http.StatusOK {
		t.Fatalf("second draft status=%d body=%s", secondDraft.Code, secondDraft.Body.String())
	}
	stalePublish := fixture.publish(createdBody.Workflow.ID, compilationOne.ID, "stale")
	assertErrorResponse(t, stalePublish, http.StatusConflict, "CONFLICT")

	secondCompilation := fixture.request(http.MethodPost, fixture.base+"/"+createdBody.Workflow.ID+"/draft:compile", nil, nil)
	var compilationTwo compilationDTO
	decodeResponse(t, secondCompilation.Body.Bytes(), &compilationTwo)
	if secondCompilation.Code != http.StatusCreated || compilationTwo.ID == compilationOne.ID {
		t.Fatalf("second compilation status=%d value=%+v", secondCompilation.Code, compilationTwo)
	}
	trialTwo := fixture.request(http.MethodPost, fixture.base+"/"+createdBody.Workflow.ID+"/compilations/"+compilationTwo.ID+":trial", map[string]any{"input": map[string]any{}}, nil)
	if trialTwo.Code != http.StatusOK {
		t.Fatalf("second trial status=%d body=%s", trialTwo.Code, trialTwo.Body.String())
	}
	secondPublish := fixture.publish(createdBody.Workflow.ID, compilationTwo.ID, "second")
	if secondPublish.Code != http.StatusCreated {
		t.Fatalf("second publish status=%d body=%s", secondPublish.Code, secondPublish.Body.String())
	}
	var publishedTwo struct {
		Revision revisionDTO `json:"revision"`
	}
	decodeResponse(t, secondPublish.Body.Bytes(), &publishedTwo)

	revisions := fixture.request(http.MethodGet, fixture.base+"/"+createdBody.Workflow.ID+"/revisions", nil, nil)
	if revisions.Code != http.StatusOK || !strings.Contains(revisions.Body.String(), `"revisionNo":2`) {
		t.Fatalf("revisions status=%d body=%s", revisions.Code, revisions.Body.String())
	}
	diff := fixture.request(http.MethodGet, fixture.base+"/"+createdBody.Workflow.ID+"/revisions:diff?from="+
		publishedOne.Revision.ID+"&to="+publishedTwo.Revision.ID, nil, nil)
	if diff.Code != http.StatusOK || !strings.Contains(diff.Body.String(), `"draft":true`) {
		t.Fatalf("diff status=%d body=%s", diff.Code, diff.Body.String())
	}
	activated := fixture.request(http.MethodPost, fixture.base+"/"+createdBody.Workflow.ID+"/revisions/"+
		publishedOne.Revision.ID+":activate", nil, nil)
	if activated.Code != http.StatusOK || !strings.Contains(activated.Body.String(), `"eventType":"workflow.release.rolled_back"`) {
		t.Fatalf("activate status=%d body=%s", activated.Code, activated.Body.String())
	}
	list := fixture.request(http.MethodGet, fixture.base, nil, nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), createdBody.Workflow.ID) {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
}

func TestV1SmartDAGGeneratesFormalDraftAndUsesWorkflowLifecycle(t *testing.T) {
	fixture := newWorkflowAPIFixture(t)
	generated := fixture.request(http.MethodPost, fixture.base+":generate", map[string]any{
		"goal": "整理供应商准入材料并返回评审摘要",
	}, nil)
	if generated.Code != http.StatusCreated || generated.Header().Get("ETag") == "" {
		t.Fatalf("generate status=%d headers=%v body=%s", generated.Code, generated.Header(), generated.Body.String())
	}
	var generatedBody struct {
		Workflow            workflowDTO                  `json:"workflow"`
		Draft               draftDTO                     `json:"draft"`
		ReasoningSteps      []smartdag.ReasoningStep     `json:"reasoningSteps"`
		MissingCapabilities []smartdag.MissingCapability `json:"missingCapabilities"`
		Confidence          int                          `json:"confidence"`
	}
	decodeResponse(t, generated.Body.Bytes(), &generatedBody)
	if generatedBody.Workflow.ID == "" || generatedBody.Draft.SchemaVersion != smartdag.SchemaVersion ||
		len(generatedBody.ReasoningSteps) != 5 || generatedBody.Confidence == 0 ||
		!strings.Contains(string(generatedBody.Draft.Graph), `"generatedBy":"smart-dag.v1"`) {
		t.Fatalf("unexpected generated workflow response: %+v graph=%s", generatedBody, generatedBody.Draft.Graph)
	}

	compiled := fixture.request(http.MethodPost, fixture.base+"/"+generatedBody.Workflow.ID+"/draft:compile", nil, nil)
	if compiled.Code != http.StatusCreated {
		t.Fatalf("compile generated draft status=%d body=%s", compiled.Code, compiled.Body.String())
	}
	var compilation compilationDTO
	decodeResponse(t, compiled.Body.Bytes(), &compilation)
	if compilation.Status != "VALID" {
		t.Fatalf("generated draft did not compile: %+v", compilation)
	}
	trial := fixture.request(http.MethodPost, fixture.base+"/"+generatedBody.Workflow.ID+"/compilations/"+compilation.ID+":trial", map[string]any{
		"input": map[string]any{"supplierId": "SUP-1"},
	}, nil)
	if trial.Code != http.StatusOK || !strings.Contains(trial.Body.String(), `"status":"SUCCEEDED"`) {
		t.Fatalf("trial generated draft status=%d body=%s", trial.Code, trial.Body.String())
	}
	published := fixture.publish(generatedBody.Workflow.ID, compilation.ID, "publish generated workflow")
	if published.Code != http.StatusCreated {
		t.Fatalf("publish generated draft status=%d body=%s", published.Code, published.Body.String())
	}
}

type workflowAPITrialRunner struct{}

func (workflowAPITrialRunner) Run(
	_ context.Context,
	request workflow.TrialExecutionRequest,
) (workflow.TrialExecutionResult, error) {
	return workflow.TrialExecutionResult{ExecutionID: request.ExecutionID, Status: workflow.TrialExecutionSucceeded}, nil
}

type workflowAPIEvents struct{}

func (workflowAPIEvents) AppendWorkflowReleasePublished(
	context.Context,
	*sql.Tx,
	workflow.WorkflowReleasePublishedEvent,
) error {
	return nil
}

func (workflowAPIEvents) AppendWorkflowRevisionActivated(
	context.Context,
	*sql.Tx,
	workflow.WorkflowRevisionActivatedEvent,
) error {
	return nil
}

type workflowAPIFixture struct {
	router http.Handler
	db     *sql.DB
	base   string
	token  string
}

func newWorkflowAPIFixture(t *testing.T) *workflowAPIFixture {
	t.Helper()
	authFixture := newV1AuthFixture(t)
	login := authFixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": v1AdminName, "password": v1AdminPass,
	}, "", nil)
	tokens := decodeTokenResponse(t, login)
	workspaceID := uuid.NewString()
	workspaces, err := workspace.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workspaces.Create(context.Background(), workspace.NewWorkspace{
		ID: workspaceID, Slug: "workflows-" + workspaceID[:8], DisplayName: "Workflows",
		Mode: workspace.ModeProduction, OwnerUserID: v1AdminUserID, CreatedBy: v1AdminUserID,
	}); err != nil {
		t.Fatal(err)
	}
	authorizer, err := authz.NewService(workspaces)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := workflow.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := workflow.NewCompilationService(repository, workflowcompiler.New())
	if err != nil {
		t.Fatal(err)
	}
	trials, err := workflow.NewTrialService(repository, workflowAPITrialRunner{})
	if err != nil {
		t.Fatal(err)
	}
	events := workflowAPIEvents{}
	publisher, err := workflow.NewPublishService(repository, authorizer, events)
	if err != nil {
		t.Fatal(err)
	}
	activator, err := workflow.NewActivationService(repository, authorizer, events)
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := workflow.NewReadinessService(repository)
	if err != nil {
		t.Fatal(err)
	}
	toolRepository, err := tool.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	generator, err := smartdag.NewService(toolRepository, repository, smartdag.UUIDv7Generator)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewWorkflowRoutes(WorkflowDependencies{
		Authorizer: authorizer, Store: repository, Compiler: compiler, Trials: trials,
		Publisher: publisher, Activator: activator, Readiness: readiness, Generator: generator,
		Production: workflowAPIProductionExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{Authenticator: authFixture.auth, Registrars: []V1RouteRegistrar{authFixture.authRoutes, routes}})
	if err != nil {
		t.Fatal(err)
	}
	return &workflowAPIFixture{router: router, db: authFixture.db,
		base: "/api/v1/workspaces/" + workspaceID + "/workflows", token: tokens.AccessToken}
}

// workflowAPIProductionExecutor is a fixture stub for production :execute HTTP tests.
// Full plan execution is covered by workflow package service tests.
type workflowAPIProductionExecutor struct{}

func (workflowAPIProductionExecutor) Execute(
	_ context.Context,
	input workflow.ProductionExecuteInput,
) (workflow.ProductionExecuteResult, error) {
	if input.RevisionID == "" || input.WorkflowID == "" {
		return workflow.ProductionExecuteResult{}, workflow.ErrInvalid
	}
	// Simulate active-only gate for a reserved non-active revision marker.
	if strings.HasPrefix(input.RevisionID, "00000000-") {
		return workflow.ProductionExecuteResult{}, workflow.ErrRevisionNotActive
	}
	executionID := uuid.NewString()
	if input.IdempotencyKey != "" {
		// Deterministic execution id for same key+hash-ish body so HTTP test can assert no double-run identity.
		executionID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(
			input.WorkspaceID+"|"+input.ActorID+"|"+input.IdempotencyKey+"|"+string(input.Input),
		)).String()
	}
	traceID := input.TraceID
	if traceID == "" {
		traceID = "trace-production-fixture"
	}
	return workflow.ProductionExecuteResult{
		ExecutionID: executionID, WorkflowID: input.WorkflowID, RevisionID: input.RevisionID,
		Status: workflow.ProductionStatusSucceeded, TraceID: traceID,
	}, nil
}

func (fixture *workflowAPIFixture) request(
	method, path string,
	body any,
	headers map[string]string,
) *httptest.ResponseRecorder {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	request.Header.Set("X-Request-ID", "request-v1-workflow-test")
	request.Header.Set("X-Trace-ID", "trace-v1-workflow-test")
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

func (fixture *workflowAPIFixture) publish(workflowID, compilationID, note string) *httptest.ResponseRecorder {
	return fixture.request(http.MethodPost, fixture.base+"/"+workflowID+"/compilations/"+compilationID+":publish", map[string]any{
		"callableName": "approve_order", "callableDescription": "Approve an order",
		"riskLevel": "MEDIUM", "sideEffectLevel": "WRITE",
		"requiresConfirmation": true, "publishNote": note,
	}, nil)
}

func workflowAPIGraph(edited bool) map[string]any {
	graph := map[string]any{
		"schemaVersion": "workflow.graph.v1",
		"nodes": []any{
			map[string]any{"id": "start", "type": "Start", "data": map[string]any{"inputSchema": map[string]any{"type": "object"}}},
			map[string]any{"id": "end", "type": "End"},
		},
		"edges": []any{map[string]any{"id": "edge-1", "sourceNodeId": "start", "targetNodeId": "end"}},
	}
	if edited {
		graph["ui"] = map[string]any{"edited": true}
	}
	return graph
}
