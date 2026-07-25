package httptransport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/connection"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/openapiimport"
	"actweave/backend/internal/provider"
	"actweave/backend/internal/tool"
	"actweave/backend/internal/workspace"

	"github.com/google/uuid"
)

func TestV1ToolLifecyclePublishesExactVersionAndForwardsIdempotency(t *testing.T) {
	f := newToolOpenAPIFixture(t)
	created := f.request(t, http.MethodPost, f.base+"/tools", map[string]any{
		"providerId": f.providerID, "defaultConnectionId": f.connectionID, "name": "Get orders", "slug": "get-orders", "description": "Get orders",
		"draft": validToolDraftRequest(f.connectionID),
	}, f.token, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create tool status=%d body=%s", created.Code, created.Body.String())
	}
	var createdBody struct {
		Tool  toolDTO    `json:"tool"`
		Draft versionDTO `json:"draft"`
	}
	decodeResponse(t, created.Body.Bytes(), &createdBody)
	if createdBody.Tool.ID == "" || createdBody.Draft.LifecycleStatus != "DRAFT" {
		t.Fatalf("created=%+v", createdBody)
	}

	rejected := f.request(t, http.MethodPost, f.base+"/tools", map[string]any{"agentId": uuid.NewString(), "providerId": f.providerID}, f.token, nil)
	assertErrorResponse(t, rejected, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	tested := f.request(t, http.MethodPost, f.base+"/tools/"+createdBody.Tool.ID+"/versions/"+createdBody.Draft.ID+":test", map[string]any{"connectionId": f.connectionID, "input": map[string]any{}}, f.token, nil)
	if tested.Code != http.StatusOK || !strings.Contains(tested.Body.String(), `"status":"SUCCEEDED"`) {
		t.Fatalf("test tool status=%d body=%s", tested.Code, tested.Body.String())
	}
	version := f.request(t, http.MethodGet, f.base+"/tools/"+createdBody.Tool.ID+"/versions/"+createdBody.Draft.ID, nil, f.token, nil)
	var testedVersion versionDTO
	decodeResponse(t, version.Body.Bytes(), &testedVersion)
	if testedVersion.LifecycleStatus != "TESTED" {
		t.Fatalf("tested version=%+v", testedVersion)
	}
	published := f.request(t, http.MethodPost, f.base+"/tools/"+createdBody.Tool.ID+"/versions/"+createdBody.Draft.ID+":publish", map[string]any{"callableName": "get_orders", "callableDescription": "Get orders", "lockVersion": testedVersion.LockVersion}, f.token, nil)
	if published.Code != http.StatusOK || !strings.Contains(published.Body.String(), `"lifecycleStatus":"PUBLISHED"`) {
		t.Fatalf("publish status=%d body=%s", published.Code, published.Body.String())
	}
	var publishedBody struct {
		ReleaseID string     `json:"releaseId"`
		Version   versionDTO `json:"version"`
	}
	decodeResponse(t, published.Body.Bytes(), &publishedBody)
	if publishedBody.ReleaseID == "" || publishedBody.Version.ID != createdBody.Draft.ID {
		t.Fatalf("published=%+v", publishedBody)
	}
	newDraft := f.request(t, http.MethodPost, f.base+"/tools/"+createdBody.Tool.ID+"/versions", map[string]any{"sourceVersionId": createdBody.Draft.ID}, f.token, nil)
	if newDraft.Code != http.StatusCreated || !strings.Contains(newDraft.Body.String(), `"versionNo":2`) {
		t.Fatalf("new draft status=%d body=%s", newDraft.Code, newDraft.Body.String())
	}

	invoked := f.requestWithHeader(t, http.MethodPost, f.base+"/tools/"+createdBody.Tool.ID+":invoke", map[string]any{"releaseId": publishedBody.ReleaseID, "connectionId": f.connectionID, "input": map[string]any{}}, f.token, "Idempotency-Key", "tool-call-1")
	if invoked.Code != http.StatusOK || !strings.Contains(invoked.Body.String(), `"cached":false`) {
		t.Fatalf("invoke status=%d body=%s", invoked.Code, invoked.Body.String())
	}
	captured := f.invoker.last()
	if captured.IdempotencyKey != "tool-call-1" || captured.ReleaseID != publishedBody.ReleaseID || captured.CapabilityID != createdBody.Tool.ID {
		t.Fatalf("captured invocation=%+v", captured)
	}
}

func TestV1OpenAPIImportGenerateAndDelete(t *testing.T) {
	f := newToolOpenAPIFixture(t)
	created := f.request(t, http.MethodPost, f.base+"/openapi-imports", map[string]any{"providerId": f.providerID}, f.token, nil)
	if created.Code != http.StatusAccepted || !strings.Contains(created.Body.String(), `"status":"SUCCEEDED"`) {
		t.Fatalf("create import status=%d body=%s", created.Code, created.Body.String())
	}
	var createdBody struct {
		Import importDTO `json:"import"`
	}
	decodeResponse(t, created.Body.Bytes(), &createdBody)
	detail := f.request(t, http.MethodGet, f.base+"/openapi-imports/"+createdBody.Import.ID, nil, f.token, nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("import detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var detailBody struct {
		Endpoints []endpointDTO `json:"endpoints"`
	}
	decodeResponse(t, detail.Body.Bytes(), &detailBody)
	if len(detailBody.Endpoints) != 1 || !detailBody.Endpoints[0].Ready {
		t.Fatalf("endpoints=%+v", detailBody.Endpoints)
	}
	generated := f.request(t, http.MethodPost, f.base+"/openapi-imports/"+createdBody.Import.ID+":generate-tools", map[string]any{"endpointIds": []string{detailBody.Endpoints[0].ID}}, f.token, nil)
	if generated.Code != http.StatusCreated || !strings.Contains(generated.Body.String(), `"lifecycleStatus":"DRAFT"`) {
		t.Fatalf("generate status=%d body=%s", generated.Code, generated.Body.String())
	}

	rejected := f.request(t, http.MethodPost, f.base+"/openapi-imports", map[string]any{"providerId": f.providerID, "agentId": uuid.NewString()}, f.token, nil)
	assertErrorResponse(t, rejected, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	second := f.request(t, http.MethodPost, f.base+"/openapi-imports", map[string]any{"providerId": f.providerID}, f.token, nil)
	var secondBody struct {
		Import importDTO `json:"import"`
	}
	decodeResponse(t, second.Body.Bytes(), &secondBody)
	deleted := f.request(t, http.MethodDelete, f.base+"/openapi-imports/"+secondBody.Import.ID, nil, f.token, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete import status=%d body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestV1OpenAPIFileUploadBindsProviderAndConnection(t *testing.T) {
	f := newToolOpenAPIFixture(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("providerId", f.providerID); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("connectionId", f.connectionID); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "neiops-openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte(`{"openapi":"3.0.3","info":{"title":"neiops","version":"1"},"paths":{}}`)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, f.base+"/openapi-imports/__command/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+f.token)
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"providerId":"`+f.providerID+`"`) ||
		!strings.Contains(response.Body.String(), `"connectionId":"`+f.connectionID+`"`) {
		t.Fatalf("upload import status=%d body=%s", response.Code, response.Body.String())
	}
}

func validToolDraftRequest(connectionID string) map[string]any {
	return map[string]any{"defaultConnectionId": connectionID, "actionSchemaVersion": "http.v1", "actionConfig": map[string]any{"method": "GET", "path": "/orders", "parameters": []any{}}, "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"}, "errorMappings": map[string]any{}, "runtimePolicy": map[string]any{"timeoutMs": 1000, "maxResponseBytes": 1048576, "retryCount": 0}, "riskLevel": "LOW", "sideEffectLevel": "READ", "requiresConfirmation": false}
}

type apiTestExecutor struct{}

func (apiTestExecutor) Kind() string                             { return execution.ExecutorTypeHTTP }
func (apiTestExecutor) Capabilities() execution.ExecutorFeatures { return execution.ExecutorFeatures{} }
func (apiTestExecutor) Invoke(_ context.Context, request execution.InvocationRequest, _ execution.InvocationEventSink) (execution.InvocationResult, error) {
	now := time.Now().UTC()
	return execution.InvocationResult{InvocationID: request.InvocationID, TraceID: request.TraceID, Output: json.RawMessage(`{"orders":[]}`), HTTPStatus: 200, ContentType: "application/json", StartedAt: now, FinishedAt: now.Add(time.Millisecond), Latency: time.Millisecond}, nil
}
func (apiTestExecutor) Cancel(context.Context, execution.InvocationRef) error { return nil }

type apiTestArtifacts struct{ db *sql.DB }

func (s apiTestArtifacts) WriteToolTestArtifact(ctx context.Context, a tool.ToolTestArtifact) (string, error) {
	payload, _ := json.Marshal(a)
	return insertFixtureObject(ctx, s.db, a.WorkspaceID, "TOOL_TEST_PAYLOAD", payload, a.TestedBy, true)
}

type apiPublishEvents struct{}

func (apiPublishEvents) AppendToolReleasePublished(context.Context, *sql.Tx, tool.ToolReleasePublishedEvent) error {
	return nil
}

type captureInvoker struct {
	mu      sync.Mutex
	request execution.InvokeRequest
}

func (i *captureInvoker) Invoke(_ context.Context, r execution.InvokeRequest) (execution.PipelineResult, error) {
	i.mu.Lock()
	i.request = r
	i.mu.Unlock()
	return execution.PipelineResult{InvocationResult: execution.InvocationResult{InvocationID: r.InvocationID, TraceID: r.TraceID, Output: json.RawMessage(`{"ok":true}`), HTTPStatus: 200}, Attempts: 1}, nil
}
func (i *captureInvoker) last() execution.InvokeRequest {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.request
}

type fixtureImporter struct {
	db         *sql.DB
	repository *openapiimport.Repository
}

func (i fixtureImporter) Import(ctx context.Context, r openapiimport.ProviderImportRequest) (openapiimport.ParseOutcome, error) {
	content := []byte(`{"openapi":"3.0.0"}`)
	rawID, err := insertFixtureObject(ctx, i.db, r.WorkspaceID, "OPENAPI_SOURCE", content, r.CreatedBy, false)
	if err != nil {
		return openapiimport.ParseOutcome{}, err
	}
	digest := sha256.Sum256(content)
	uri := "https://api.example/openapi.json"
	_, err = i.repository.CreatePending(ctx, openapiimport.CreatePendingInput{ID: r.ImportID, WorkspaceID: r.WorkspaceID, ProviderID: &r.ProviderID, ConnectionID: r.ConnectionID, SourceType: "URL", SourceURI: &uri, FileName: "openapi.json", RawObjectID: rawID, ContentSHA256: hex.EncodeToString(digest[:]), ParserVersion: "fixture.v1", CreatedBy: r.CreatedBy})
	if err != nil {
		return openapiimport.ParseOutcome{}, err
	}
	if _, err = i.repository.MarkParsing(ctx, r.WorkspaceID, r.ImportID); err != nil {
		return openapiimport.ParseOutcome{}, err
	}
	endpoint := openapiimport.Endpoint{ID: uuid.NewString(), WorkspaceID: r.WorkspaceID, ImportID: r.ImportID, Method: "GET", Path: "/orders", OperationID: "getOrders", Summary: "Get orders", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`), OutputSchema: json.RawMessage(`{"type":"object"}`), Issues: json.RawMessage(`[]`), Ready: true}
	completed, endpoints, err := i.repository.Complete(ctx, r.WorkspaceID, r.ImportID, openapiimport.CompleteParseInput{Endpoints: []openapiimport.Endpoint{endpoint}})
	return openapiimport.ParseOutcome{Import: completed, Endpoints: endpoints}, err
}

func (i fixtureImporter) ImportFile(ctx context.Context, r openapiimport.FileImportRequest) (openapiimport.ParseOutcome, error) {
	return i.Import(ctx, openapiimport.ProviderImportRequest{
		ImportID: r.ImportID, WorkspaceID: r.WorkspaceID, ProviderID: r.ProviderID,
		ConnectionID: r.ConnectionID, CreatedBy: r.CreatedBy,
	})
}

func insertFixtureObject(ctx context.Context, db *sql.DB, wid, kind string, content []byte, actor string, sensitive bool) (string, error) {
	id := uuid.NewString()
	digest := sha256.Sum256(content)
	classification, key := "INTERNAL", any(nil)
	if sensitive {
		classification, key = "SENSITIVE", "fixture-key"
	}
	_, err := db.ExecContext(ctx, `INSERT INTO stored_objects(id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,encryption_key_id,classification,retention_mode,created_by_type,created_by_id)VALUES($1,$2,'api-fixture',$3,$4,'application/json',$5,$6,$7,$8,'PERMANENT','USER',$9)`, id, wid, "objects/"+id, kind, len(content), hex.EncodeToString(digest[:]), key, classification, actor)
	return id, err
}

type toolOpenAPIFixture struct {
	*v1AuthFixture
	workspaceID, providerID, connectionID, base, token string
	invoker                                            *captureInvoker
}

func newToolOpenAPIFixture(t *testing.T) *toolOpenAPIFixture {
	t.Helper()
	f := newV1AuthFixture(t)
	ctx := context.Background()
	wid := uuid.NewString()
	workspaces, err := workspace.NewRepository(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workspaces.Create(ctx, workspace.NewWorkspace{ID: wid, Slug: "tools-" + wid[:8], DisplayName: "Tools", Mode: workspace.ModeProduction, OwnerUserID: v1AdminUserID, CreatedBy: v1AdminUserID}); err != nil {
		t.Fatal(err)
	}
	authorizer, err := authz.NewService(workspaces)
	if err != nil {
		t.Fatal(err)
	}
	providers, err := provider.NewRepository(f.db)
	if err != nil {
		t.Fatal(err)
	}
	pid := uuid.NewString()
	outboundDriver := json.RawMessage(`{
		"outboundIdentity":{
			"schemaVersion":"outbound-identity.v1",
			"supportedModes":["REQUEST_PASSTHROUGH"],
			"supportedSubjectTypes":["USER"],
			"requestPassthrough":{
				"credentialTypes":["ACCESS_TOKEN"],
				"businessInjection":{"headerName":"Authorization","prefix":"Bearer"}
			}
		}
	}`)
	if _, err = providers.Create(ctx, provider.NewProvider{ID: pid, WorkspaceID: wid, Name: "API", Kind: provider.KindHTTPOpenAPI, DriverKey: "http_openapi", Transport: "HTTP", EndpointConfig: json.RawMessage(`{"baseUrl":"https://api.example","sourceUri":"https://api.example/openapi.json"}`), DriverConfig: outboundDriver, DiscoveryMode: "ON_DEMAND", CreatedBy: v1AdminUserID}); err != nil {
		t.Fatal(err)
	}
	connections, err := connection.NewRepository(f.db)
	if err != nil {
		t.Fatal(err)
	}
	cid := uuid.NewString()
	createdConnection, err := connections.Create(ctx, connection.NewConnection{
		ID: cid, WorkspaceID: wid, ProviderID: pid, Name: "API test", Alias: "api-test", Environment: "TEST",
		OutboundIdentity: json.RawMessage(`{"schemaVersion":"outbound-connection.v1","mode":"REQUEST_PASSTHROUGH","requestPassthrough":{"maxResidenceSeconds":600}}`),
		GrantedScopes:    json.RawMessage(`[]`), Policy: json.RawMessage(`{}`), MigrationState: connection.MigrationStateNone, CreatedBy: v1AdminUserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connections.RecordVerification(ctx, connection.NewVerification{ID: uuid.NewString(), WorkspaceID: wid, ConnectionID: cid, Status: "SUCCEEDED", Diagnostics: json.RawMessage(`{"code":"OK"}`), TestedBy: v1AdminUserID, ExpectedLockVersion: createdConnection.LockVersion}); err != nil {
		t.Fatal(err)
	}
	tools, err := tool.NewRepository(f.db)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := execution.NewRegistry(apiTestExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	tests, err := tool.NewTestService(tools, registry, apiTestArtifacts{f.db})
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := tool.NewPublishService(tools, authorizer, apiPublishEvents{})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := tool.NewInvocationResolver(f.db)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &captureInvoker{}
	imports, err := openapiimport.NewRepository(f.db)
	if err != nil {
		t.Fatal(err)
	}
	generator, err := openapiimport.NewGenerationService(f.db, tools, func() (openapiimport.ToolIDs, error) {
		return openapiimport.ToolIDs{CapabilityID: uuid.NewString(), VersionID: uuid.NewString()}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	importer := fixtureImporter{f.db, imports}
	routes, err := NewToolOpenAPIRoutes(ToolOpenAPIDependencies{Authorizer: authorizer, Tools: tools, Tests: tests, TestConnections: resolver, Publisher: publisher, Invoker: invoker, Imports: imports, Importer: importer, FileImporter: importer, Generator: generator})
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{Authenticator: f.auth, Registrars: []V1RouteRegistrar{f.authRoutes, routes}})
	if err != nil {
		t.Fatal(err)
	}
	f.router = router
	login := f.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{"username": v1AdminName, "password": v1AdminPass}, "", nil)
	return &toolOpenAPIFixture{v1AuthFixture: f, workspaceID: wid, providerID: pid, connectionID: cid, base: "/api/v1/workspaces/" + wid, token: decodeTokenResponse(t, login).AccessToken, invoker: invoker}
}

func (f *toolOpenAPIFixture) requestWithHeader(t *testing.T, method, path string, body any, token, name, value string) *httptest.ResponseRecorder {
	t.Helper()
	payload, _ := json.Marshal(body)
	request := httptest.NewRequest(method, path, strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set(name, value)
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	return response
}
