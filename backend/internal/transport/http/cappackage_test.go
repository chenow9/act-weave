package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/cappackage"
	"actweave/backend/internal/connection"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/openapiimport"
	"actweave/backend/internal/provider"
	"actweave/backend/internal/tool"
	"actweave/backend/internal/workflow"
	"actweave/backend/internal/workspace"

	"github.com/google/uuid"
)

func TestV1CapabilityPackageExportPreviewImportRoundTrip(t *testing.T) {
	f := newPackageHTTPFixture(t)
	created := f.request(t, http.MethodPost, f.base+"/tools", map[string]any{
		"providerId": f.providerID, "defaultConnectionId": f.connectionID,
		"name": "Get orders", "slug": "get-orders", "description": "Get orders",
		"draft": validToolDraftRequest(f.connectionID),
	}, f.token, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create tool status=%d body=%s", created.Code, created.Body.String())
	}
	var createdBody struct {
		Tool struct {
			ID string `json:"id"`
		} `json:"tool"`
	}
	decodeResponse(t, created.Body.Bytes(), &createdBody)

	exported := f.request(t, http.MethodGet, f.base+"/tools/"+createdBody.Tool.ID+"/__command/export", nil, f.token, nil)
	if exported.Code != http.StatusOK || !strings.Contains(exported.Body.String(), "slug: get-orders") {
		t.Fatalf("export status=%d body=%s", exported.Code, exported.Body.String())
	}
	if got := exported.Header().Get("Content-Type"); !strings.Contains(got, "yaml") {
		t.Fatalf("content type=%s", got)
	}
	if !strings.Contains(exported.Header().Get("Content-Disposition"), "get-orders.actweave.yaml") {
		t.Fatalf("disposition=%s", exported.Header().Get("Content-Disposition"))
	}

	preview := f.request(t, http.MethodPost, f.base+"/packages/__command/preview", map[string]any{
		"yaml": exported.Body.String(),
	}, f.token, nil)
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"action":"update-draft"`) {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}

	imported := f.request(t, http.MethodPost, f.base+"/packages/__command/import", map[string]any{
		"yaml": exported.Body.String(),
	}, f.token, nil)
	if imported.Code != http.StatusOK || !strings.Contains(imported.Body.String(), `"action":"update-draft"`) {
		t.Fatalf("import status=%d body=%s", imported.Code, imported.Body.String())
	}

	createYAML := strings.ReplaceAll(exported.Body.String(), "slug: get-orders", "slug: get-orders-copy")
	createYAML = strings.ReplaceAll(createYAML, "name: Get orders", "name: Get orders copy")
	createdImport := f.request(t, http.MethodPost, f.base+"/packages/__command/import", map[string]any{
		"yaml": createYAML,
	}, f.token, nil)
	if createdImport.Code != http.StatusOK || !strings.Contains(createdImport.Body.String(), `"action":"create"`) {
		t.Fatalf("create import status=%d body=%s", createdImport.Code, createdImport.Body.String())
	}
}

func TestV1CapabilityPackagePreviewBlocksUnknownProviderAndSecrets(t *testing.T) {
	f := newPackageHTTPFixture(t)
	autoBound := f.request(t, http.MethodPost, f.base+"/packages/__command/preview", map[string]any{
		"yaml": validPackageYAML("missing-provider", "no-such-provider", "api-test"),
	}, f.token, nil)
	if autoBound.Code != http.StatusOK || !strings.Contains(autoBound.Body.String(), `"canImport":true`) {
		t.Fatalf("unique-connection auto-bind status=%d body=%s", autoBound.Code, autoBound.Body.String())
	}
	secretYAML := strings.ReplaceAll(
		validPackageYAML("secret-tool", "API", "api-test"),
		"retryCount: 0",
		"authorization: Bearer leaked\n        retryCount: 0",
	)
	rejected := f.request(t, http.MethodPost, f.base+"/packages/__command/preview", map[string]any{
		"yaml": secretYAML,
	}, f.token, nil)
	assertErrorResponse(t, rejected, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
}

func TestV1CapabilityPackagePreviewAutoBindsForeignProviderToUniqueConnection(t *testing.T) {
	f := newPackageHTTPFixture(t)
	yaml := validPackageYAML("get-orders", "Foreign API", "prod")
	ok := f.request(t, http.MethodPost, f.base+"/packages/__command/preview", map[string]any{
		"yaml": yaml,
	}, f.token, nil)
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), `"canImport":true`) {
		t.Fatalf("auto-bind preview status=%d body=%s", ok.Code, ok.Body.String())
	}

	imported := f.request(t, http.MethodPost, f.base+"/packages/__command/import", map[string]any{
		"yaml": yaml,
	}, f.token, nil)
	if imported.Code != http.StatusOK || !strings.Contains(imported.Body.String(), `"action":"create"`) {
		t.Fatalf("auto-bind import status=%d body=%s", imported.Code, imported.Body.String())
	}
}

func TestV1CapabilityPackagePreviewRemapsForeignProviderWhenMultipleConnections(t *testing.T) {
	f := newPackageHTTPFixture(t)
	otherID := uuid.NewString()
	if _, err := f.connections.Create(t.Context(), connection.NewConnection{
		ID: otherID, WorkspaceID: f.workspaceID, ProviderID: f.providerID,
		Name: "API other", Alias: "api-other", Environment: "TEST",
		OutboundIdentity: json.RawMessage(`{"schemaVersion":"outbound-connection.v1","mode":"REQUEST_PASSTHROUGH","requestPassthrough":{"maxResidenceSeconds":600}}`),
		GrantedScopes:    json.RawMessage(`[]`), Policy: json.RawMessage(`{}`),
		MigrationState: connection.MigrationStateNone, CreatedBy: v1AdminUserID,
	}); err != nil {
		t.Fatal(err)
	}
	yaml := validPackageYAML("get-orders", "Foreign API", "prod")
	blocked := f.request(t, http.MethodPost, f.base+"/packages/__command/preview", map[string]any{
		"yaml": yaml,
	}, f.token, nil)
	if blocked.Code != http.StatusOK || !strings.Contains(blocked.Body.String(), `"action":"blocked"`) {
		t.Fatalf("blocked foreign preview status=%d body=%s", blocked.Code, blocked.Body.String())
	}

	payload := map[string]any{
		"yaml": yaml,
		"connectionBindings": []map[string]any{{
			"provider":           "Foreign API",
			"connection":         "prod",
			"targetConnectionId": f.connectionID,
		}},
	}
	ok := f.request(t, http.MethodPost, f.base+"/packages/__command/preview", payload, f.token, nil)
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), `"canImport":true`) {
		t.Fatalf("remapped preview status=%d body=%s", ok.Code, ok.Body.String())
	}

	imported := f.request(t, http.MethodPost, f.base+"/packages/__command/import", payload, f.token, nil)
	if imported.Code != http.StatusOK || !strings.Contains(imported.Body.String(), `"action":"create"`) {
		t.Fatalf("remapped import status=%d body=%s", imported.Code, imported.Body.String())
	}
}

func TestV1CapabilityPackageWorkflowExportRemapsToolSlug(t *testing.T) {
	f := newPackageHTTPFixture(t)
	created := f.request(t, http.MethodPost, f.base+"/tools", map[string]any{
		"providerId": f.providerID, "defaultConnectionId": f.connectionID,
		"name": "Get orders", "slug": "get-orders", "description": "Get orders",
		"draft": validToolDraftRequest(f.connectionID),
	}, f.token, nil)
	var createdBody struct {
		Tool struct {
			ID string `json:"id"`
		} `json:"tool"`
	}
	decodeResponse(t, created.Body.Bytes(), &createdBody)

	graph, _ := json.Marshal(map[string]any{
		"schemaVersion": "workflow.graph.v1",
		"nodes": []any{
			map[string]any{"id": "start", "type": "Start", "label": "Start", "data": map[string]any{}},
			map[string]any{"id": "call", "type": "Tool", "label": "Call", "data": map[string]any{"toolId": createdBody.Tool.ID}},
		},
		"edges": []any{
			map[string]any{"id": "e1", "sourceNodeId": "start", "targetNodeId": "call"},
		},
	})
	workflowID := uuid.NewString()
	draftID := uuid.NewString()
	if _, _, err := f.workflows.Create(t.Context(), workflow.CreateInput{
		CapabilityID: workflowID, DraftID: draftID, WorkspaceID: f.workspaceID,
		Name: "Order flow", Slug: "order-flow", Description: "flow",
		SchemaVersion: "workflow.graph.v1", Graph: graph, CreatedBy: v1AdminUserID,
	}); err != nil {
		t.Fatal(err)
	}
	exported := f.request(t, http.MethodGet, f.base+"/workflows/"+workflowID+"/__command/export", nil, f.token, nil)
	if exported.Code != http.StatusOK || !strings.Contains(exported.Body.String(), "toolSlug: get-orders") {
		t.Fatalf("workflow export status=%d body=%s", exported.Code, exported.Body.String())
	}
	if strings.Contains(exported.Body.String(), createdBody.Tool.ID) {
		t.Fatalf("export leaked tool uuid: %s", exported.Body.String())
	}
}

type packageHTTPFixture struct {
	*toolOpenAPIFixture
	workflows   *workflow.Repository
	connections *connection.Repository
}

func newPackageHTTPFixture(t *testing.T) *packageHTTPFixture {
	t.Helper()
	base := newV1AuthFixture(t)
	ctx := t.Context()
	wid := uuid.NewString()
	workspaces, err := workspace.NewRepository(base.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workspaces.Create(ctx, workspace.NewWorkspace{
		ID: wid, Slug: "pkg-" + wid[:8], DisplayName: "Packages",
		Mode: workspace.ModeProduction, OwnerUserID: v1AdminUserID, CreatedBy: v1AdminUserID,
	}); err != nil {
		t.Fatal(err)
	}
	authorizer, err := authz.NewService(workspaces)
	if err != nil {
		t.Fatal(err)
	}
	providers, err := provider.NewRepository(base.db)
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
	if _, err = providers.Create(ctx, provider.NewProvider{
		ID: pid, WorkspaceID: wid, Name: "API", Kind: provider.KindHTTPOpenAPI,
		DriverKey: "http_openapi", Transport: "HTTP",
		EndpointConfig: json.RawMessage(`{"baseUrl":"https://api.example"}`),
		DriverConfig:   outboundDriver, DiscoveryMode: "ON_DEMAND", CreatedBy: v1AdminUserID,
	}); err != nil {
		t.Fatal(err)
	}
	connections, err := connection.NewRepository(base.db)
	if err != nil {
		t.Fatal(err)
	}
	cid := uuid.NewString()
	createdConnection, err := connections.Create(ctx, connection.NewConnection{
		ID: cid, WorkspaceID: wid, ProviderID: pid, Name: "API test", Alias: "api-test", Environment: "TEST",
		OutboundIdentity: json.RawMessage(`{"schemaVersion":"outbound-connection.v1","mode":"REQUEST_PASSTHROUGH","requestPassthrough":{"maxResidenceSeconds":600}}`),
		GrantedScopes:    json.RawMessage(`[]`), Policy: json.RawMessage(`{}`),
		MigrationState: connection.MigrationStateNone, CreatedBy: v1AdminUserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connections.RecordVerification(ctx, connection.NewVerification{
		ID: uuid.NewString(), WorkspaceID: wid, ConnectionID: cid, Status: "SUCCEEDED",
		Diagnostics: json.RawMessage(`{"code":"OK"}`), TestedBy: v1AdminUserID,
		ExpectedLockVersion: createdConnection.LockVersion,
	}); err != nil {
		t.Fatal(err)
	}
	tools, err := tool.NewRepository(base.db)
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := workflow.NewRepository(base.db)
	if err != nil {
		t.Fatal(err)
	}
	packageService, err := cappackage.NewService(tools, workflows, providers, connections, nil)
	if err != nil {
		t.Fatal(err)
	}
	packageRoutes, err := NewPackageRoutes(authorizer, packageService)
	if err != nil {
		t.Fatal(err)
	}
	toolRoutes, err := NewToolOpenAPIRoutes(ToolOpenAPIDependencies{
		Authorizer: authorizer, Tools: tools, Tests: &unusedToolTests{}, TestConnections: unusedResolver{},
		Publisher: unusedPublisher{}, Invoker: &captureInvoker{}, Imports: unusedImports{},
		Importer: unusedImporter{}, FileImporter: unusedImporter{}, Generator: unusedGenerator{},
	})
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{Authenticator: base.auth, Registrars: []V1RouteRegistrar{base.authRoutes, toolRoutes, packageRoutes}})
	if err != nil {
		t.Fatal(err)
	}
	base.router = router
	login := base.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": v1AdminName, "password": v1AdminPass,
	}, "", nil)
	return &packageHTTPFixture{
		toolOpenAPIFixture: &toolOpenAPIFixture{
			v1AuthFixture: base, workspaceID: wid, providerID: pid, connectionID: cid,
			base: "/api/v1/workspaces/" + wid, token: decodeTokenResponse(t, login).AccessToken,
		},
		workflows:   workflows,
		connections: connections,
	}
}

func validPackageYAML(slug, providerName, alias string) string {
	return strings.TrimSpace(`
apiVersion: actweave.package/v1
kind: Package
metadata:
  name: `+slug+`
spec:
  tools:
    - slug: `+slug+`
      name: `+slug+`
      provider: `+providerName+`
      connection: `+alias+`
      riskLevel: LOW
      sideEffectLevel: READ
      action:
        schemaVersion: http.v1
        config:
          method: GET
          path: /orders
      inputSchema:
        type: object
      outputSchema:
        type: object
      runtimePolicy:
        timeoutMs: 1000
        maxResponseBytes: 1048576
        retryCount: 0
`) + "\n"
}

type unusedToolTests struct{}

func (unusedToolTests) Run(context.Context, tool.RunToolTestInput) (tool.TestRunResult, error) {
	return tool.TestRunResult{}, tool.ErrInvalid
}

type unusedResolver struct{}

func (unusedResolver) ResolveTestConnection(context.Context, string, string, string, string) (execution.ConnectionSnapshot, execution.CredentialReference, error) {
	return execution.ConnectionSnapshot{}, execution.CredentialReference{}, tool.ErrInvalid
}

type unusedPublisher struct{}

func (unusedPublisher) Publish(context.Context, tool.PublishToolInput) (tool.PublishToolResult, error) {
	return tool.PublishToolResult{}, tool.ErrInvalid
}
func (unusedPublisher) ForcePublish(context.Context, tool.ForcePublishToolInput) (tool.PublishToolResult, error) {
	return tool.PublishToolResult{}, tool.ErrInvalid
}

type unusedImports struct{}

func (unusedImports) List(context.Context, string) ([]openapiimport.Import, error) {
	return nil, nil
}
func (unusedImports) Get(context.Context, string, string) (openapiimport.Import, error) {
	return openapiimport.Import{}, openapiimport.ErrNotFound
}
func (unusedImports) ListEndpoints(context.Context, string, string) ([]openapiimport.Endpoint, error) {
	return nil, nil
}
func (unusedImports) Delete(context.Context, string, string) error { return nil }

type unusedImporter struct{}

func (unusedImporter) Import(context.Context, openapiimport.ProviderImportRequest) (openapiimport.ParseOutcome, error) {
	return openapiimport.ParseOutcome{}, openapiimport.ErrInvalid
}
func (unusedImporter) ImportFile(context.Context, openapiimport.FileImportRequest) (openapiimport.ParseOutcome, error) {
	return openapiimport.ParseOutcome{}, openapiimport.ErrInvalid
}

type unusedGenerator struct{}

func (unusedGenerator) Generate(context.Context, openapiimport.GenerateToolsRequest) ([]openapiimport.GeneratedTool, error) {
	return nil, openapiimport.ErrInvalid
}
