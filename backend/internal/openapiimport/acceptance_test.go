package openapiimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/provider"
	"actweave/backend/internal/tool"
)

const (
	acceptFileImportID        = "0e8f1f2e-7b5a-7c3d-8e9f-123456789001"
	acceptFileEndpointID      = "0e8f1f2e-7b5a-7c3d-8e9f-123456789002"
	acceptFileRawObjectID     = "0e8f1f2e-7b5a-7c3d-8e9f-123456789003"
	acceptURLImportID         = "0e8f1f2e-7b5a-7c3d-8e9f-123456789004"
	acceptURLEndpointID       = "0e8f1f2e-7b5a-7c3d-8e9f-123456789005"
	acceptURLRawObjectID      = "0e8f1f2e-7b5a-7c3d-8e9f-123456789006"
	acceptBlockedImportID     = "0e8f1f2e-7b5a-7c3d-8e9f-123456789007"
	acceptIssuesImportID      = "0e8f1f2e-7b5a-7c3d-8e9f-123456789008"
	acceptIssuesEndpointA     = "0e8f1f2e-7b5a-7c3d-8e9f-123456789009"
	acceptIssuesEndpointB     = "0e8f1f2e-7b5a-7c3d-8e9f-12345678900a"
	acceptIssuesRawObjectID   = "0e8f1f2e-7b5a-7c3d-8e9f-12345678900b"
	acceptConcurrentImportID  = "0e8f1f2e-7b5a-7c3d-8e9f-12345678900c"
	acceptConcurrentEndpoint  = "0e8f1f2e-7b5a-7c3d-8e9f-12345678900d"
	acceptConcurrentRaw       = "0e8f1f2e-7b5a-7c3d-8e9f-12345678900e"
	acceptConcurrentCapA      = "0e8f1f2e-7b5a-7c3d-8e9f-12345678900f"
	acceptConcurrentVerA      = "0e8f1f2e-7b5a-7c3d-8e9f-123456789010"
	acceptConcurrentCapB      = "0e8f1f2e-7b5a-7c3d-8e9f-123456789011"
	acceptConcurrentVerB      = "0e8f1f2e-7b5a-7c3d-8e9f-123456789012"
	acceptAuthenticatedImport = "0e8f1f2e-7b5a-7c3d-8e9f-123456789013"
	acceptAuthenticatedEP     = "0e8f1f2e-7b5a-7c3d-8e9f-123456789014"
	acceptAuthenticatedRaw    = "0e8f1f2e-7b5a-7c3d-8e9f-123456789015"
)

func TestOpenAPIImportAcceptanceFileAndGuardedURL(t *testing.T) {
	repository, sourceRepository, db := newProviderImportTest(t)
	parseService, err := NewParseService(repository, KinOpenAPIParser{}, sequenceIDs(
		acceptFileEndpointID, acceptURLEndpointID,
	))
	if err != nil {
		t.Fatal(err)
	}
	fileOutcome, err := parseService.Parse(context.Background(), ParseRequest{
		ImportID: acceptFileImportID, WorkspaceID: providerImportWorkspaceID,
		SourceType: SourceTypeFile, FileName: "uploaded-orders.yaml",
		RawObjectID: acceptFileRawObjectID, Content: validGenerationDocument(false),
		CreatedBy: providerImportOwnerID,
	})
	if err != nil || fileOutcome.Import.SourceType != SourceTypeFile ||
		fileOutcome.Import.Status != ImportStatusSucceeded || len(fileOutcome.Endpoints) != 1 {
		t.Fatalf("file import acceptance failed: %+v err=%v", fileOutcome, err)
	}

	urlDocument := []byte(strings.ReplaceAll(string(validGenerationDocument(false)), "Generated Orders", "URL Orders"))
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestCount++
		if request.URL.Path != "/openapi.yaml" || request.Header.Get("Authorization") != "" {
			t.Errorf("unexpected guarded URL request: path=%s authorization=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		response.Header().Set("Content-Type", "application/yaml")
		response.Header().Set("ETag", `"url-revision-2"`)
		_, _ = response.Write(urlDocument)
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(target.Port())
	if err != nil {
		t.Fatal(err)
	}
	endpointConfig, _ := json.Marshal(map[string]any{
		"sourceUri": server.URL + "/openapi.yaml",
		"egress": map[string]any{
			"AllowedHosts": []string{target.Hostname()}, "AllowedPorts": []int{port},
			"AllowedCIDRs": []string{"127.0.0.0/8"}, "MaxRedirects": 2,
		},
	})
	// encoding/json uses field names for execution.EgressPolicy; normalize them
	// to the API's lower-camel JSON contract before storing Provider config.
	endpointConfig = []byte(strings.NewReplacer(
		"AllowedHosts", "allowedHosts", "AllowedPorts", "allowedPorts",
		"AllowedCIDRs", "allowedCIDRs", "MaxRedirects", "maxRedirects",
	).Replace(string(endpointConfig)))
	if _, err := db.Exec(`UPDATE capability_providers SET endpoint_config=$2 WHERE id=$1`, providerImportProviderID, endpointConfig); err != nil {
		t.Fatal(err)
	}
	driver, _ := provider.NewHTTPOpenAPIDriver(unusedHTTPDiscoverer{})
	registry, _ := provider.NewRegistry(driver)
	rawStore := &recordingPermanentOpenAPIStore{id: acceptURLRawObjectID}
	loader, err := NewHTTPProviderDocumentLoader(nil, anonymousProviderAuthorizer{}, rawStore, 0)
	if err != nil {
		t.Fatal(err)
	}
	providerService, err := NewProviderImportService(sourceRepository, registry, loader, parseService)
	if err != nil {
		t.Fatal(err)
	}
	urlOutcome, err := providerService.Import(context.Background(), ProviderImportRequest{
		ImportID: acceptURLImportID, WorkspaceID: providerImportWorkspaceID,
		ProviderID: providerImportProviderID, CreatedBy: providerImportOwnerID,
	})
	if err != nil || urlOutcome.Import.Status != ImportStatusSucceeded ||
		urlOutcome.Import.SourceRevision == nil || *urlOutcome.Import.SourceRevision != `"url-revision-2"` ||
		len(urlOutcome.Endpoints) != 1 || requestCount != 1 {
		t.Fatalf("guarded URL import acceptance failed: %+v requests=%d err=%v", urlOutcome, requestCount, err)
	}
	if len(rawStore.records) != 1 || rawStore.records[0].WorkspaceID != providerImportWorkspaceID ||
		rawStore.records[0].ContentType != "application/yaml" || string(rawStore.records[0].Content) != string(urlDocument) {
		t.Fatalf("URL raw document was not permanently stored: %+v", rawStore.records)
	}

	blockedConfig, _ := json.Marshal(map[string]any{"sourceUri": server.URL + "/openapi.yaml"})
	if _, err := db.Exec(`UPDATE capability_providers SET endpoint_config=$2 WHERE id=$1`, providerImportProviderID, blockedConfig); err != nil {
		t.Fatal(err)
	}
	if _, err := providerService.Import(context.Background(), ProviderImportRequest{
		ImportID: acceptBlockedImportID, WorkspaceID: providerImportWorkspaceID,
		ProviderID: providerImportProviderID, CreatedBy: providerImportOwnerID,
	}); err == nil {
		t.Fatal("expected private URL without explicit CIDR to be denied")
	}
	if len(rawStore.records) != 1 {
		t.Fatalf("blocked URL must not write a raw object: %+v", rawStore.records)
	}
}

func TestOpenAPIImportAcceptanceDuplicateOperationAndInvalidSchema(t *testing.T) {
	repository, _, db := newProviderImportTest(t)
	parseService, err := NewParseService(repository, KinOpenAPIParser{}, sequenceIDs(
		acceptIssuesEndpointA, acceptIssuesEndpointB,
	))
	if err != nil {
		t.Fatal(err)
	}
	sourceURI := "https://orders.example.test/invalid-openapi.yaml"
	outcome, err := parseService.Parse(context.Background(), ParseRequest{
		ImportID: acceptIssuesImportID, WorkspaceID: providerImportWorkspaceID,
		ProviderID: pointerString(providerImportProviderID), SourceType: SourceTypeURL,
		SourceURI: &sourceURI, FileName: "invalid-openapi.yaml",
		RawObjectID: acceptIssuesRawObjectID, Content: duplicateAndInvalidSchemaDocument(),
		CreatedBy: providerImportOwnerID,
	})
	if err != nil {
		t.Fatalf("persist reviewable parser issues: %v", err)
	}
	if outcome.Import.Status != ImportStatusSucceeded || outcome.Import.TotalEndpoints != 2 ||
		outcome.Import.ReadyEndpoints != 0 || outcome.Import.IssueCount < 3 || len(outcome.Endpoints) != 2 {
		t.Fatalf("unexpected issue/readiness summary: %+v endpoints=%+v", outcome.Import, outcome.Endpoints)
	}
	for _, endpoint := range outcome.Endpoints {
		if endpoint.Ready || !strings.Contains(string(endpoint.Issues), "duplicate operationId: duplicateOperation") {
			t.Fatalf("duplicate operation endpoint remained ready: %+v", endpoint)
		}
	}
	if !strings.Contains(string(outcome.Endpoints[0].Issues), "array schema has no items") &&
		!strings.Contains(string(outcome.Endpoints[1].Issues), "array schema has no items") {
		t.Fatalf("invalid schema issue was not persisted: %+v", outcome.Endpoints)
	}
	toolRepository, _ := tool.NewRepository(db)
	generation, _ := NewGenerationService(db, toolRepository, sequenceToolIDs(
		ToolIDs{CapabilityID: generatedCapabilityOneID, VersionID: generatedVersionOneID},
	))
	if _, err := generation.Generate(context.Background(), GenerateToolsRequest{
		WorkspaceID: providerImportWorkspaceID, ImportID: acceptIssuesImportID,
		EndpointIDs: []string{acceptIssuesEndpointA, acceptIssuesEndpointB},
		CreatedBy:   providerImportOwnerID,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected non-ready endpoints to reject generation, got %v", err)
	}
	var generatedCount int
	if err := db.QueryRow(`SELECT count(*) FROM capabilities WHERE id=$1`, generatedCapabilityOneID).Scan(&generatedCount); err != nil {
		t.Fatal(err)
	}
	if generatedCount != 0 {
		t.Fatalf("invalid endpoint generation left capability: %d", generatedCount)
	}
}

func TestOpenAPIImportAcceptanceAuthenticatedURLKeepsSecretOutOfContracts(t *testing.T) {
	repository, sourceRepository, db := newProviderImportTest(t)
	document := []byte(strings.ReplaceAll(string(validGenerationDocument(false)), "Generated Orders", "Authenticated Orders"))
	var receivedSecret string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		receivedSecret = request.Header.Get("X-API-Key")
		response.Header().Set("Content-Type", "application/yaml")
		_, _ = response.Write(document)
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(target.Port())
	endpointConfig, _ := json.Marshal(map[string]any{"sourceUri": server.URL + "/openapi.yaml"})
	connectionPolicy, _ := json.Marshal(map[string]any{
		"egress": map[string]any{
			"allowedHosts": []string{target.Hostname()}, "allowedPorts": []int{port},
			"allowedCIDRs": []string{"127.0.0.0/8"},
		},
		"allowedCredentialHeaders": []string{"X-API-Key"},
	})
	if _, err := db.Exec(`UPDATE capability_providers SET endpoint_config=$2 WHERE id=$1`, providerImportProviderID, endpointConfig); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE service_connections SET policy=$2 WHERE id=$1`, providerImportConnectionID, connectionPolicy); err != nil {
		t.Fatal(err)
	}
	secretSource := &acceptanceSecretSource{value: []byte("provider-secret-value")}
	injector, err := execution.NewHTTPSecretInjector(secretSource)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := NewDatabaseProviderHeaderAuthorizer(db, injector)
	if err != nil {
		t.Fatal(err)
	}
	rawStore := &recordingPermanentOpenAPIStore{id: acceptAuthenticatedRaw}
	loader, err := NewHTTPProviderDocumentLoader(nil, authorizer, rawStore, 0)
	if err != nil {
		t.Fatal(err)
	}
	driver, _ := provider.NewHTTPOpenAPIDriver(unusedHTTPDiscoverer{})
	registry, _ := provider.NewRegistry(driver)
	parseService, _ := NewParseService(repository, KinOpenAPIParser{}, sequenceIDs(acceptAuthenticatedEP))
	service, _ := NewProviderImportService(sourceRepository, registry, loader, parseService)
	connectionID := providerImportConnectionID
	outcome, err := service.Import(context.Background(), ProviderImportRequest{
		ImportID: acceptAuthenticatedImport, WorkspaceID: providerImportWorkspaceID,
		ProviderID: providerImportProviderID, ConnectionID: &connectionID, CreatedBy: providerImportOwnerID,
	})
	if err != nil || outcome.Import.Status != ImportStatusSucceeded || len(outcome.Endpoints) != 1 {
		t.Fatalf("authenticated URL import failed: %+v err=%v", outcome, err)
	}
	if receivedSecret != "provider-secret-value" || secretSource.calls != 1 {
		t.Fatalf("credential was not minimally injected: header=%q calls=%d", receivedSecret, secretSource.calls)
	}
	serialized, _ := json.Marshal(outcome)
	if strings.Contains(string(serialized), "provider-secret-value") ||
		strings.Contains(string(outcome.Endpoints[0].InputSchema), "X-API-Key") {
		t.Fatalf("authenticated import leaked credential into contracts: %s", serialized)
	}
}

func TestOpenAPIImportAcceptanceConcurrentGenerationSingleWinner(t *testing.T) {
	repository, _, db := newProviderImportTest(t)
	parseService, _ := NewParseService(repository, KinOpenAPIParser{}, sequenceIDs(acceptConcurrentEndpoint))
	sourceURI := "https://orders.example.test/openapi.yaml"
	if _, err := parseService.Parse(context.Background(), ParseRequest{
		ImportID: acceptConcurrentImportID, WorkspaceID: providerImportWorkspaceID,
		ProviderID: pointerString(providerImportProviderID), SourceType: SourceTypeURL,
		SourceURI: &sourceURI, FileName: "concurrent.yaml", RawObjectID: acceptConcurrentRaw,
		Content: validGenerationDocument(false), CreatedBy: providerImportOwnerID,
	}); err != nil {
		t.Fatal(err)
	}
	toolRepository, _ := tool.NewRepository(db)
	generation, _ := NewGenerationService(db, toolRepository, sequenceToolIDs(
		ToolIDs{CapabilityID: acceptConcurrentCapA, VersionID: acceptConcurrentVerA},
		ToolIDs{CapabilityID: acceptConcurrentCapB, VersionID: acceptConcurrentVerB},
	))
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := generation.Generate(context.Background(), GenerateToolsRequest{
				WorkspaceID: providerImportWorkspaceID, ImportID: acceptConcurrentImportID,
				EndpointIDs: []string{acceptConcurrentEndpoint}, CreatedBy: providerImportOwnerID,
			})
			results <- err
		}()
	}
	group.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent generation result: %v", err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("expected one generation winner, got success=%d conflicts=%d", success, conflicts)
	}
	var toolCount, linkedCount int
	if err := db.QueryRow(`SELECT count(*) FROM tools WHERE source_endpoint_id=$1`, acceptConcurrentEndpoint).Scan(&toolCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM openapi_endpoints WHERE id=$1 AND generated_capability_id IS NOT NULL`, acceptConcurrentEndpoint).Scan(&linkedCount); err != nil {
		t.Fatal(err)
	}
	if toolCount != 1 || linkedCount != 1 {
		t.Fatalf("concurrent generation duplicated state: tools=%d links=%d", toolCount, linkedCount)
	}
}

func duplicateAndInvalidSchemaDocument() []byte {
	return []byte(`
openapi: 3.0.3
info: { title: Invalid Operations, version: 1.0.0 }
paths:
  /a:
    get:
      operationId: duplicateOperation
      parameters:
        - name: broken
          in: query
          schema: { type: array }
      responses:
        "200": { description: ok }
  /b:
    post:
      operationId: duplicateOperation
      responses:
        "200": { description: ok }
`)
}

type anonymousProviderAuthorizer struct{}

func (anonymousProviderAuthorizer) WithProviderHeaders(
	_ context.Context,
	source ProviderImportSource,
	invoke func(map[string]string, []string) error,
) error {
	if source.Connection != nil {
		return fmt.Errorf("anonymous authorizer received a connection")
	}
	return invoke(map[string]string{}, nil)
}

type permanentOpenAPIRecord struct {
	WorkspaceID string
	ContentType string
	Content     []byte
}

type recordingPermanentOpenAPIStore struct {
	id      string
	records []permanentOpenAPIRecord
}

type acceptanceSecretSource struct {
	value []byte
	calls int
}

func (source *acceptanceSecretSource) WithActiveSecret(
	_ context.Context,
	workspaceID, secretID string,
	invoke func([]byte) error,
) error {
	if workspaceID != providerImportWorkspaceID || secretID != providerImportSecretID {
		return fmt.Errorf("unexpected secret reference")
	}
	source.calls++
	return invoke(append([]byte(nil), source.value...))
}

func (store *recordingPermanentOpenAPIStore) StorePermanentOpenAPI(
	_ context.Context,
	workspaceID, contentType string,
	content []byte,
) (string, error) {
	store.records = append(store.records, permanentOpenAPIRecord{
		WorkspaceID: workspaceID, ContentType: contentType, Content: append([]byte(nil), content...),
	})
	return store.id, nil
}
