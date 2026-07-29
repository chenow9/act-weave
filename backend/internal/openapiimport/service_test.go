package openapiimport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/domain"
)

const (
	parseOwnerID       = "0b8f1f2e-7b5a-7c3d-8e9f-123456789001"
	parseWorkspaceID   = "0b8f1f2e-7b5a-7c3d-8e9f-123456789002"
	parseOtherSpaceID  = "0b8f1f2e-7b5a-7c3d-8e9f-123456789003"
	parseImportOneID   = "0b8f1f2e-7b5a-7c3d-8e9f-123456789004"
	parseImportTwoID   = "0b8f1f2e-7b5a-7c3d-8e9f-123456789005"
	parseImportThreeID = "0b8f1f2e-7b5a-7c3d-8e9f-123456789006"
	parseEndpointOneID = "0b8f1f2e-7b5a-7c3d-8e9f-123456789007"
	parseEndpointTwoID = "0b8f1f2e-7b5a-7c3d-8e9f-123456789008"
	parseEndpointTriID = "0b8f1f2e-7b5a-7c3d-8e9f-123456789009"
	parseRawObjectOne  = "0b8f1f2e-7b5a-7c3d-8e9f-12345678900a"
	parseRawObjectTwo  = "0b8f1f2e-7b5a-7c3d-8e9f-12345678900b"
	parseRawObjectTri  = "0b8f1f2e-7b5a-7c3d-8e9f-12345678900c"
)

func TestParseAndPersistOpenAPIDocument(t *testing.T) {
	repository, _ := newParseRepositoryTest(t)
	service, err := NewParseService(repository, KinOpenAPIParser{}, sequenceIDs(
		parseEndpointOneID, parseEndpointTwoID,
	))
	if err != nil {
		t.Fatal(err)
	}
	document := validOpenAPIDocument()
	outcome, err := service.Parse(context.Background(), ParseRequest{
		ImportID: parseImportOneID, WorkspaceID: parseWorkspaceID, SourceType: SourceTypeRaw,
		FileName: "orders.yaml", RawObjectID: parseRawObjectOne, Content: document, CreatedBy: parseOwnerID,
	})
	if err != nil {
		t.Fatalf("parse and persist openapi: %v", err)
	}
	if outcome.Import.Status != ImportStatusSucceeded || outcome.Import.ParserVersion != CurrentParserVersion ||
		outcome.Import.TotalEndpoints != 1 || outcome.Import.ReadyEndpoints != 1 || outcome.Import.IssueCount != 1 {
		t.Fatalf("unexpected completed import: %+v", outcome.Import)
	}
	if outcome.DuplicateOfID != nil || len(outcome.Endpoints) != 1 {
		t.Fatalf("unexpected first parse outcome: %+v", outcome)
	}
	digest := sha256.Sum256(document)
	if outcome.Import.ContentSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("unexpected content checksum: %s", outcome.Import.ContentSHA256)
	}
	assertPersistedEndpointSchemas(t, outcome.Endpoints[0])

	stored, err := repository.Get(context.Background(), parseWorkspaceID, parseImportOneID)
	if err != nil || stored.Status != ImportStatusSucceeded || stored.RawObjectID != parseRawObjectOne {
		t.Fatalf("read completed import: %+v err=%v", stored, err)
	}
	if _, err := repository.Get(context.Background(), parseOtherSpaceID, parseImportOneID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workspace import miss, got %v", err)
	}
	endpoints, err := repository.ListEndpoints(context.Background(), parseWorkspaceID, parseImportOneID)
	if err != nil || len(endpoints) != 1 || endpoints[0].ID != parseEndpointOneID {
		t.Fatalf("list persisted endpoints: %+v err=%v", endpoints, err)
	}

	duplicate, err := service.Parse(context.Background(), ParseRequest{
		ImportID: parseImportTwoID, WorkspaceID: parseWorkspaceID, SourceType: SourceTypeFile,
		FileName: "orders-copy.yaml", RawObjectID: parseRawObjectTwo, Content: document, CreatedBy: parseOwnerID,
	})
	if err != nil {
		t.Fatalf("persist duplicate content import: %v", err)
	}
	if duplicate.DuplicateOfID == nil || *duplicate.DuplicateOfID != parseImportOneID ||
		duplicate.Import.ID != parseImportTwoID || duplicate.Import.Status != ImportStatusSucceeded {
		t.Fatalf("expected duplicate checksum recognition without overwriting history: %+v", duplicate)
	}
}

func TestParseFailurePersistsStableStatus(t *testing.T) {
	repository, _ := newParseRepositoryTest(t)
	service, err := NewParseService(repository, KinOpenAPIParser{}, sequenceIDs(parseEndpointOneID))
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := service.Parse(context.Background(), ParseRequest{
		ImportID: parseImportOneID, WorkspaceID: parseWorkspaceID, SourceType: SourceTypeRaw,
		FileName: "invalid.yaml", RawObjectID: parseRawObjectOne,
		Content: []byte("not: [valid: openapi: super-secret"), CreatedBy: parseOwnerID,
	})
	var failure *ParseFailure
	if !errors.As(err, &failure) || err.Error() != ParseErrorCode || failure.Code() != ParseErrorCode {
		t.Fatalf("expected stable redacted parse failure, got %T %v", err, err)
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("parse error leaked source content: %v", err)
	}
	if outcome.Import.Status != ImportStatusFailed || outcome.Import.IssueCount != 1 ||
		outcome.Import.TotalEndpoints != 0 || outcome.Import.ReadyEndpoints != 0 {
		t.Fatalf("unexpected failed import state: %+v", outcome.Import)
	}
	stored, getErr := repository.Get(context.Background(), parseWorkspaceID, parseImportOneID)
	if getErr != nil || stored.Status != ImportStatusFailed {
		t.Fatalf("failed state was not persisted: %+v err=%v", stored, getErr)
	}
	endpoints, listErr := repository.ListEndpoints(context.Background(), parseWorkspaceID, parseImportOneID)
	if listErr != nil || len(endpoints) != 0 {
		t.Fatalf("failed parse persisted partial endpoints: %+v err=%v", endpoints, listErr)
	}
}

func TestParseRunsOutsideDatabaseTransaction(t *testing.T) {
	repository, db := newParseRepositoryTest(t)
	parser := parserStub{
		version: "probe-parser.v1",
		parse: func(ctx context.Context, _ ParseInput) (ParseResult, error) {
			probeContext, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			result, err := db.ExecContext(probeContext, `
				UPDATE openapi_imports SET updated_at=clock_timestamp()
				WHERE workspace_id=$1 AND id=$2 AND status='PARSING'
			`, parseWorkspaceID, parseImportOneID)
			if err != nil {
				return ParseResult{}, err
			}
			rows, err := result.RowsAffected()
			if err != nil || rows != 1 {
				return ParseResult{}, errors.New("parser could not independently update parsing row")
			}
			return ParseResult{Endpoints: []domain.OpenAPIEndpoint{{
				Method: "GET", Path: "/health", OperationID: "health", Summary: "Health", Ready: true,
			}}}, nil
		},
	}
	service, err := NewParseService(repository, parser, sequenceIDs(parseEndpointOneID))
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := service.Parse(context.Background(), ParseRequest{
		ImportID: parseImportOneID, WorkspaceID: parseWorkspaceID, SourceType: SourceTypeRaw,
		FileName: "probe.yaml", RawObjectID: parseRawObjectOne,
		Content: []byte("parser probe content"), CreatedBy: parseOwnerID,
	})
	if err != nil || outcome.Import.Status != ImportStatusSucceeded || outcome.Import.ParserVersion != parser.version {
		t.Fatalf("parser transaction probe failed: %+v err=%v", outcome, err)
	}
}

func TestPersistParseCompletionIsConditionalAndAtomic(t *testing.T) {
	repository, db := newParseRepositoryTest(t)
	checksum := strings.Repeat("a", 64)
	created, err := repository.CreatePending(context.Background(), CreatePendingInput{
		ID: parseImportThreeID, WorkspaceID: parseWorkspaceID, SourceType: SourceTypeRaw,
		FileName: "atomic.yaml", RawObjectID: parseRawObjectTri, ContentSHA256: checksum,
		ParserVersion: CurrentParserVersion, CreatedBy: parseOwnerID,
	})
	if err != nil || created.Status != ImportStatusPending {
		t.Fatalf("create pending import: %+v err=%v", created, err)
	}
	validEndpoint := Endpoint{
		ID: parseEndpointOneID, WorkspaceID: parseWorkspaceID, ImportID: parseImportThreeID,
		Method: "GET", Path: "/orders", InputSchema: json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`), Issues: json.RawMessage(`[]`), Ready: true,
	}
	if _, _, err := repository.Complete(context.Background(), parseWorkspaceID, parseImportThreeID,
		CompleteParseInput{Endpoints: []Endpoint{validEndpoint}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected completion before parsing to conflict, got %v", err)
	}
	if _, err := repository.MarkParsing(context.Background(), parseWorkspaceID, parseImportThreeID); err != nil {
		t.Fatal(err)
	}
	duplicateEndpoint := validEndpoint
	duplicateEndpoint.ID = parseEndpointTwoID
	if _, _, err := repository.Complete(context.Background(), parseWorkspaceID, parseImportThreeID,
		CompleteParseInput{Endpoints: []Endpoint{validEndpoint, duplicateEndpoint}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected duplicate endpoint transaction to roll back, got %v", err)
	}
	var endpointCount int
	if err := db.QueryRow(`SELECT count(*) FROM openapi_endpoints WHERE import_id=$1`, parseImportThreeID).Scan(&endpointCount); err != nil {
		t.Fatal(err)
	}
	if endpointCount != 0 {
		t.Fatalf("failed completion left partial endpoints: %d", endpointCount)
	}
	stillParsing, err := repository.Get(context.Background(), parseWorkspaceID, parseImportThreeID)
	if err != nil || stillParsing.Status != ImportStatusParsing {
		t.Fatalf("failed completion changed import state: %+v err=%v", stillParsing, err)
	}
	completed, endpoints, err := repository.Complete(context.Background(), parseWorkspaceID, parseImportThreeID,
		CompleteParseInput{Endpoints: []Endpoint{validEndpoint}, ImportIssueCount: 2})
	if err != nil || completed.Status != ImportStatusSucceeded || completed.IssueCount != 2 || len(endpoints) != 1 {
		t.Fatalf("complete parse atomically: %+v endpoints=%+v err=%v", completed, endpoints, err)
	}
	if _, _, err := repository.Complete(context.Background(), parseWorkspaceID, parseImportThreeID,
		CompleteParseInput{Endpoints: []Endpoint{validEndpoint}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected repeated completion conflict, got %v", err)
	}
}

func newParseRepositoryTest(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("unexpected migration version: %+v", version)
	}
	db := testDatabase.Open(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'parse.owner','Parse Owner')`, parseOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'parse-workspace','Parse Workspace','PRODUCTION',$3,$3,$3),
		($2,'parse-other','Parse Other','SANDBOX',$3,$3,$3)
	`, parseWorkspaceID, parseOtherSpaceID, parseOwnerID); err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db
}

func validOpenAPIDocument() []byte {
	return []byte(`
openapi: 3.0.3
info: { title: Orders, version: 1.0.0 }
paths:
  /orders/{orderId}:
    get:
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
                  ok: { type: boolean }
`)
}

func assertPersistedEndpointSchemas(t *testing.T, endpoint Endpoint) {
	t.Helper()
	if endpoint.Method != "GET" || endpoint.Path != "/orders/{orderId}" || endpoint.OperationID != "" || !endpoint.Ready {
		t.Fatalf("unexpected endpoint metadata: %+v", endpoint)
	}
	var input map[string]any
	if err := json.Unmarshal(endpoint.InputSchema, &input); err != nil {
		t.Fatal(err)
	}
	properties, ok := input["properties"].(map[string]any)
	if !ok {
		t.Fatalf("missing input properties: %s", endpoint.InputSchema)
	}
	orderID, ok := properties["orderId"].(map[string]any)
	if !ok || orderID["type"] != "string" || orderID["x-actweave-location"] != "path" ||
		orderID["x-actweave-parameter-name"] != "orderId" {
		t.Fatalf("path mapping not preserved in schema: %+v", orderID)
	}
	limit, ok := properties["limit"].(map[string]any)
	if !ok || limit["type"] != "integer" || limit["x-actweave-location"] != "query" {
		t.Fatalf("query mapping not preserved in schema: %+v", limit)
	}
	var output map[string]any
	if err := json.Unmarshal(endpoint.OutputSchema, &output); err != nil {
		t.Fatal(err)
	}
	outputProperties, ok := output["properties"].(map[string]any)
	if !ok || outputProperties["ok"].(map[string]any)["type"] != "boolean" {
		t.Fatalf("response schema not persisted: %s", endpoint.OutputSchema)
	}
}

type parserStub struct {
	version string
	parse   func(context.Context, ParseInput) (ParseResult, error)
}

func (parser parserStub) Version() string { return parser.version }
func (parser parserStub) Parse(ctx context.Context, input ParseInput) (ParseResult, error) {
	return parser.parse(ctx, input)
}

func sequenceIDs(ids ...string) IDGenerator {
	var mutex sync.Mutex
	index := 0
	return func() (string, error) {
		mutex.Lock()
		defer mutex.Unlock()
		if index >= len(ids) {
			return "", errors.New("endpoint id sequence exhausted")
		}
		value := ids[index]
		index++
		return value, nil
	}
}
