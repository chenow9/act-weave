package openapiimport

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"actweave/backend/internal/tool"
)

const (
	generatedCapabilityOneID = "0d8f1f2e-7b5a-7c3d-8e9f-123456789001"
	generatedVersionOneID    = "0d8f1f2e-7b5a-7c3d-8e9f-123456789002"
	generatedCapabilityTwoID = "0d8f1f2e-7b5a-7c3d-8e9f-123456789003"
	generatedVersionTwoID    = "0d8f1f2e-7b5a-7c3d-8e9f-123456789004"
	generatedImportID        = "0d8f1f2e-7b5a-7c3d-8e9f-123456789005"
	generatedEndpointOneID   = "0d8f1f2e-7b5a-7c3d-8e9f-123456789006"
	generatedEndpointTwoID   = "0d8f1f2e-7b5a-7c3d-8e9f-123456789007"
	generatedRawObjectID     = "0d8f1f2e-7b5a-7c3d-8e9f-123456789008"
)

func TestGenerateToolsCreatesReviewableWorkspaceDraft(t *testing.T) {
	importRepository, _, db := newProviderImportTest(t)
	parseService, err := NewParseService(importRepository, KinOpenAPIParser{}, sequenceIDs(generatedEndpointOneID))
	if err != nil {
		t.Fatal(err)
	}
	connectionID, sourceURI := providerImportConnectionID, "https://orders.example.test/openapi.yaml"
	parsed, err := parseService.Parse(context.Background(), ParseRequest{
		ImportID: generatedImportID, WorkspaceID: providerImportWorkspaceID,
		ProviderID: pointerString(providerImportProviderID), ConnectionID: &connectionID,
		SourceType: SourceTypeURL, SourceURI: &sourceURI, SourceRevision: pointerString("etag-1"),
		FileName: "orders.yaml", RawObjectID: generatedRawObjectID,
		Content: validGenerationDocument(false), CreatedBy: providerImportOwnerID,
	})
	if err != nil || len(parsed.Endpoints) != 1 {
		t.Fatalf("prepare parsed import: %+v err=%v", parsed, err)
	}
	toolRepository, err := tool.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewGenerationService(db, toolRepository, sequenceToolIDs(ToolIDs{
		CapabilityID: generatedCapabilityOneID, VersionID: generatedVersionOneID,
	}))
	if err != nil {
		t.Fatal(err)
	}
	generated, err := service.Generate(context.Background(), GenerateToolsRequest{
		WorkspaceID: providerImportWorkspaceID, ImportID: generatedImportID,
		EndpointIDs: []string{generatedEndpointOneID}, CreatedBy: providerImportOwnerID,
	})
	if err != nil {
		t.Fatalf("generate tool draft: %v", err)
	}
	if len(generated) != 1 {
		t.Fatalf("expected one generated tool, got %+v", generated)
	}
	result := generated[0]
	if result.EndpointID != generatedEndpointOneID || result.Tool.CapabilityID != generatedCapabilityOneID ||
		result.Tool.WorkspaceID != providerImportWorkspaceID || result.Tool.ProviderID != providerImportProviderID ||
		result.Tool.SourceEndpointID == nil || *result.Tool.SourceEndpointID != generatedEndpointOneID ||
		result.Tool.DefaultConnectionID == nil || *result.Tool.DefaultConnectionID != providerImportConnectionID {
		t.Fatalf("unexpected generated tool metadata: %+v", result.Tool)
	}
	if result.Draft.ID != generatedVersionOneID || result.Draft.LifecycleStatus != "DRAFT" ||
		result.Draft.VersionNo != 1 || result.Draft.ExecutorType != "HTTP" ||
		result.Draft.ActionSchemaVersion != "http.v1" || result.Draft.PublishedAt != nil {
		t.Fatalf("unexpected generated draft: %+v", result.Draft)
	}
	assertJSONEqual(t, parsed.Endpoints[0].InputSchema, result.Draft.InputSchema)
	assertJSONEqual(t, parsed.Endpoints[0].OutputSchema, result.Draft.OutputSchema)
	var action struct {
		Method     string `json:"method"`
		Path       string `json:"path"`
		Parameters []struct {
			Name     string `json:"name"`
			In       string `json:"in"`
			Input    string `json:"input"`
			Required bool   `json:"required"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(result.Draft.ActionConfig, &action); err != nil {
		t.Fatal(err)
	}
	if action.Method != "GET" || action.Path != "/orders/{orderId}" || len(action.Parameters) != 2 ||
		action.Parameters[0].Name != "limit" || action.Parameters[0].In != "query" ||
		action.Parameters[1].Name != "orderId" || action.Parameters[1].In != "path" ||
		!action.Parameters[1].Required {
		t.Fatalf("HTTP action mapping was not fixed from endpoint schema: %+v", action)
	}
	var activeReleaseID *string
	if err := db.QueryRow(`SELECT active_release_id FROM capabilities WHERE id=$1`, generatedCapabilityOneID).Scan(&activeReleaseID); err != nil {
		t.Fatal(err)
	}
	if activeReleaseID != nil {
		t.Fatalf("generated draft must require independent test/publish, active=%v", activeReleaseID)
	}
	var linkedCapability string
	if err := db.QueryRow(`SELECT generated_capability_id FROM openapi_endpoints WHERE id=$1`, generatedEndpointOneID).Scan(&linkedCapability); err != nil {
		t.Fatal(err)
	}
	if linkedCapability != generatedCapabilityOneID {
		t.Fatalf("endpoint generation link not persisted: %s", linkedCapability)
	}
	if _, err := service.Generate(context.Background(), GenerateToolsRequest{
		WorkspaceID: providerImportWorkspaceID, ImportID: generatedImportID,
		EndpointIDs: []string{generatedEndpointOneID}, CreatedBy: providerImportOwnerID,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected repeat generation conflict, got %v", err)
	}
	var hasAgentID bool
	if err := db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM information_schema.columns
		 WHERE table_schema='public' AND table_name IN ('capabilities','tools','tool_versions')
		 AND column_name='agent_id')
	`).Scan(&hasAgentID); err != nil {
		t.Fatal(err)
	}
	if hasAgentID {
		t.Fatal("generated tool tables must not have agent ownership")
	}
}

func TestGenerateToolsBatchFailureRollsBackAllEndpoints(t *testing.T) {
	importRepository, _, db := newProviderImportTest(t)
	parseService, err := NewParseService(importRepository, KinOpenAPIParser{}, sequenceIDs(
		generatedEndpointOneID, generatedEndpointTwoID,
	))
	if err != nil {
		t.Fatal(err)
	}
	sourceURI := "https://orders.example.test/openapi.yaml"
	parsed, err := parseService.Parse(context.Background(), ParseRequest{
		ImportID: generatedImportID, WorkspaceID: providerImportWorkspaceID,
		ProviderID: pointerString(providerImportProviderID), SourceType: SourceTypeURL,
		SourceURI: &sourceURI, FileName: "orders.yaml", RawObjectID: generatedRawObjectID,
		Content: validGenerationDocument(true), CreatedBy: providerImportOwnerID,
	})
	if err != nil || len(parsed.Endpoints) != 2 {
		t.Fatalf("prepare batch import: %+v err=%v", parsed, err)
	}
	toolRepository, _ := tool.NewRepository(db)
	service, _ := NewGenerationService(db, toolRepository, sequenceToolIDs(
		ToolIDs{CapabilityID: generatedCapabilityOneID, VersionID: generatedVersionOneID},
		ToolIDs{CapabilityID: generatedCapabilityOneID, VersionID: generatedVersionTwoID},
	))
	_, err = service.Generate(context.Background(), GenerateToolsRequest{
		WorkspaceID: providerImportWorkspaceID, ImportID: generatedImportID,
		EndpointIDs: []string{generatedEndpointOneID, generatedEndpointTwoID},
		CreatedBy:   providerImportOwnerID,
	})
	if err == nil {
		t.Fatal("expected second generated tool failure")
	}
	var capabilityCount, toolCount, linkedCount int
	if err := db.QueryRow(`SELECT count(*) FROM capabilities WHERE id=$1`, generatedCapabilityOneID).Scan(&capabilityCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM tools WHERE capability_id=$1`, generatedCapabilityOneID).Scan(&toolCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`
		SELECT count(*) FROM openapi_endpoints
		WHERE import_id=$1 AND generated_capability_id IS NOT NULL
	`, generatedImportID).Scan(&linkedCount); err != nil {
		t.Fatal(err)
	}
	if capabilityCount != 0 || toolCount != 0 || linkedCount != 0 {
		t.Fatalf("batch failure left partial state: capabilities=%d tools=%d linked=%d", capabilityCount, toolCount, linkedCount)
	}
	if _, err := service.Generate(context.Background(), GenerateToolsRequest{
		WorkspaceID: providerImportOtherSpaceID, ImportID: generatedImportID,
		EndpointIDs: []string{generatedEndpointOneID}, CreatedBy: providerImportOwnerID,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workspace import miss, got %v", err)
	}
}

func validGenerationDocument(includeSecond bool) []byte {
	secondPath := ""
	if includeSecond {
		secondPath = `
  /orders:
    post:
      operationId: createOrder
      summary: Create order
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [sku]
              properties:
                sku: { type: string }
      responses:
        "201":
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
`
	}
	return []byte(`
openapi: 3.0.3
info: { title: Generated Orders, version: 1.0.0 }
paths:
  /orders/{orderId}:
    get:
      operationId: getOrder
      summary: Get order
      parameters:
        - { name: orderId, in: path, required: true, schema: { type: string } }
        - { name: limit, in: query, schema: { type: integer } }
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
` + secondPath)
}

func sequenceToolIDs(ids ...ToolIDs) ToolIDGenerator {
	var mutex sync.Mutex
	index := 0
	return func() (ToolIDs, error) {
		mutex.Lock()
		defer mutex.Unlock()
		if index >= len(ids) {
			return ToolIDs{}, errors.New("tool id sequence exhausted")
		}
		value := ids[index]
		index++
		return value, nil
	}
}

func pointerString(value string) *string { return &value }

func assertJSONEqual(t *testing.T, expected, actual json.RawMessage) {
	t.Helper()
	var expectedValue, actualValue any
	if json.Unmarshal(expected, &expectedValue) != nil || json.Unmarshal(actual, &actualValue) != nil {
		t.Fatalf("invalid JSON comparison: expected=%s actual=%s", expected, actual)
	}
	expectedJSON, _ := json.Marshal(expectedValue)
	actualJSON, _ := json.Marshal(actualValue)
	if string(expectedJSON) != string(actualJSON) {
		t.Fatalf("JSON differs: expected=%s actual=%s", expectedJSON, actualJSON)
	}
}
