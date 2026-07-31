package openapiimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/provider"
)

const (
	providerImportOwnerID      = "0c8f1f2e-7b5a-7c3d-8e9f-123456789001"
	providerImportWorkspaceID  = "0c8f1f2e-7b5a-7c3d-8e9f-123456789002"
	providerImportOtherSpaceID = "0c8f1f2e-7b5a-7c3d-8e9f-123456789003"
	providerImportProviderID   = "0c8f1f2e-7b5a-7c3d-8e9f-123456789004"
	providerImportMCPID        = "0c8f1f2e-7b5a-7c3d-8e9f-123456789005"
	providerImportBadID        = "0c8f1f2e-7b5a-7c3d-8e9f-123456789006"
	providerImportOtherProvID  = "0c8f1f2e-7b5a-7c3d-8e9f-123456789007"
	providerImportConnectionID = "0c8f1f2e-7b5a-7c3d-8e9f-123456789008"
	providerImportUnverifiedID = "0c8f1f2e-7b5a-7c3d-8e9f-123456789009"
	providerImportWrongConnID  = "0c8f1f2e-7b5a-7c3d-8e9f-12345678900a"
	providerImportSecretID     = "0c8f1f2e-7b5a-7c3d-8e9f-12345678900b"
	providerImportSecretVerID  = "0c8f1f2e-7b5a-7c3d-8e9f-12345678900c"
	providerImportRecordID     = "0c8f1f2e-7b5a-7c3d-8e9f-12345678900d"
	providerImportEndpointID   = "0c8f1f2e-7b5a-7c3d-8e9f-12345678900e"
	providerImportRawObjectID  = "0c8f1f2e-7b5a-7c3d-8e9f-12345678900f"
	providerImportMigrationID  = "0c8f1f2e-7b5a-7c3d-8e9f-123456789010"
	providerImportMigrationRaw = "0c8f1f2e-7b5a-7c3d-8e9f-123456789011"
)

func TestProviderImportMigrationRecordsSourceRevision(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("expected migration 15, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertProviderImportIdentityFixtures(t, db)
	if _, err := db.Exec(`
		INSERT INTO openapi_imports(
		 id,workspace_id,source_type,source_revision,file_name,raw_object_id,
		 content_sha256,parser_version,created_by)
		VALUES($1,$2,'RAW','git:abc123','migration.json',$3,$4,'parser.v1',$5)
	`, providerImportMigrationID, providerImportWorkspaceID, providerImportMigrationRaw,
		strings.Repeat("a", 64), providerImportOwnerID); err != nil {
		t.Fatalf("insert source revision: %v", err)
	}
	if _, err := db.Exec(`UPDATE openapi_imports SET source_revision=' ' WHERE id=$1`, providerImportMigrationID); err == nil {
		t.Fatal("expected blank source revision rejection")
	}
	var indexExists bool
	if err := db.QueryRow(`SELECT to_regclass('public.openapi_imports_workspace_provider_revision_idx') IS NOT NULL`).Scan(&indexExists); err != nil || !indexExists {
		t.Fatalf("expected provider revision index: exists=%v err=%v", indexExists, err)
	}

}

func TestProviderImportUsesScopedHTTPContextWithoutSecrets(t *testing.T) {
	repository, sourceRepository, db := newProviderImportTest(t)
	driver, err := provider.NewHTTPOpenAPIDriver(unusedHTTPDiscoverer{})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := provider.NewRegistry(driver)
	if err != nil {
		t.Fatal(err)
	}
	parseService, err := NewParseService(repository, KinOpenAPIParser{}, sequenceIDs(providerImportEndpointID))
	if err != nil {
		t.Fatal(err)
	}
	loader := &capturingProviderLoader{document: LoadedProviderDocument{
		Content: validProviderOpenAPIDocument(), FileName: "orders-openapi.yaml",
		RawObjectID: providerImportRawObjectID, SourceRevision: `etag:"live-2"`,
	}}
	service, err := NewProviderImportService(sourceRepository, registry, loader, parseService)
	if err != nil {
		t.Fatal(err)
	}
	connectionID := providerImportConnectionID
	outcome, err := service.Import(context.Background(), ProviderImportRequest{
		ImportID: providerImportRecordID, WorkspaceID: providerImportWorkspaceID,
		ProviderID: providerImportProviderID, ConnectionID: &connectionID, CreatedBy: providerImportOwnerID,
	})
	if err != nil {
		t.Fatalf("provider import: %v", err)
	}
	if outcome.Import.Status != ImportStatusSucceeded || outcome.Import.ProviderID == nil ||
		*outcome.Import.ProviderID != providerImportProviderID || outcome.Import.ConnectionID == nil ||
		*outcome.Import.ConnectionID != providerImportConnectionID || outcome.Import.SourceURI == nil ||
		*outcome.Import.SourceURI != "https://orders.example.test/openapi.yaml" ||
		outcome.Import.SourceRevision == nil || *outcome.Import.SourceRevision != `etag:"live-2"` {
		t.Fatalf("unexpected provider import result: %+v", outcome.Import)
	}
	if len(loader.sources) != 1 || loader.sources[0].Connection == nil ||
		loader.sources[0].Connection.ID != providerImportConnectionID ||
		!loader.sources[0].Connection.Configured || loader.sources[0].Connection.Alias != "primary" {
		t.Fatalf("loader did not receive minimal connection context: %+v", loader.sources)
	}
	serializedSource, err := json.Marshal(loader.sources[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"raw-provider-secret", providerImportSecretID, "fingerprint-secret"} {
		if strings.Contains(string(serializedSource), forbidden) {
			t.Fatalf("provider loader context leaked secret material %q: %s", forbidden, serializedSource)
		}
	}
	if len(outcome.Endpoints) != 1 || strings.Contains(string(outcome.Endpoints[0].InputSchema), "X-API-Key") ||
		strings.Contains(string(outcome.Endpoints[0].OutputSchema), "raw-provider-secret") {
		t.Fatalf("authentication details copied into endpoint contract: %+v", outcome.Endpoints)
	}
	var sourceRevision, checksum string
	if err := db.QueryRow(`SELECT source_revision,content_sha256 FROM openapi_imports WHERE id=$1`, providerImportRecordID).Scan(&sourceRevision, &checksum); err != nil {
		t.Fatal(err)
	}
	if sourceRevision != `etag:"live-2"` || len(checksum) != 64 {
		t.Fatalf("source revision/checksum not recorded: revision=%q checksum=%q", sourceRevision, checksum)
	}
}

func TestProviderImportRejectsUnavailableKindAndScope(t *testing.T) {
	repository, sourceRepository, _ := newProviderImportTest(t)
	driver, _ := provider.NewHTTPOpenAPIDriver(unusedHTTPDiscoverer{})
	registry, _ := provider.NewRegistry(driver)
	parseService, _ := NewParseService(repository, KinOpenAPIParser{}, sequenceIDs(providerImportEndpointID))
	loader := &capturingProviderLoader{document: LoadedProviderDocument{
		Content: validProviderOpenAPIDocument(), FileName: "orders.yaml",
		RawObjectID: providerImportRawObjectID,
	}}
	service, _ := NewProviderImportService(sourceRepository, registry, loader, parseService)

	otherWorkspaceRequest := ProviderImportRequest{
		ImportID: providerImportRecordID, WorkspaceID: providerImportWorkspaceID,
		ProviderID: providerImportOtherProvID, CreatedBy: providerImportOwnerID,
	}
	if _, err := service.Import(context.Background(), otherWorkspaceRequest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workspace provider miss, got %v", err)
	}
	mcpRequest := otherWorkspaceRequest
	mcpRequest.ProviderID = providerImportMCPID
	if _, err := service.Import(context.Background(), mcpRequest); !errors.Is(err, ErrProviderNotAvailable) {
		t.Fatalf("expected non-HTTP provider unavailable, got %v", err)
	}
	wrongConnection := providerImportWrongConnID
	wrongConnectionRequest := otherWorkspaceRequest
	wrongConnectionRequest.ProviderID = providerImportProviderID
	wrongConnectionRequest.ConnectionID = &wrongConnection
	if _, err := service.Import(context.Background(), wrongConnectionRequest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected provider-mismatched connection miss, got %v", err)
	}
	unverifiedConnection := providerImportUnverifiedID
	unverifiedRequest := wrongConnectionRequest
	unverifiedRequest.ConnectionID = &unverifiedConnection
	if _, err := service.Import(context.Background(), unverifiedRequest); !errors.Is(err, ErrProviderNotAvailable) {
		t.Fatalf("expected unverified connection unavailable, got %v", err)
	}
	badProviderRequest := otherWorkspaceRequest
	badProviderRequest.ProviderID = providerImportBadID
	if _, err := service.Import(context.Background(), badProviderRequest); !errors.Is(err, ErrProviderInvalid) {
		t.Fatalf("expected credential-bearing provider config rejection, got %v", err)
	}
	if len(loader.sources) != 0 {
		t.Fatalf("invalid provider request reached document loader: %+v", loader.sources)
	}
}

func newProviderImportTest(t *testing.T) (*Repository, *ProviderSourceRepository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("unexpected migration: %+v", version)
	}
	db := testDatabase.Open(t)
	insertProviderImportIdentityFixtures(t, db)
	insertProviderImportFixtures(t, db)
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	sourceRepository, err := NewProviderSourceRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return repository, sourceRepository, db
}

func insertProviderImportIdentityFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'provider.import.owner','Provider Import Owner')`, providerImportOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'provider-import','Provider Import','PRODUCTION',$3,$3,$3),
		($2,'provider-import-other','Provider Import Other','SANDBOX',$3,$3,$3)
	`, providerImportWorkspaceID, providerImportOtherSpaceID, providerImportOwnerID); err != nil {
		t.Fatal(err)
	}
}

func insertProviderImportFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	// HTTP_OPENAPI requires outbound-identity.v1 (hard cutover). Import path is
	// document discovery only — dual-mode still fails closed without Subject.
	httpDriver := `{"outboundIdentity":{"schemaVersion":"outbound-identity.v1","supportedModes":["REQUEST_PASSTHROUGH"],"supportedSubjectTypes":["USER"],"requestPassthrough":{"credentialTypes":["ACCESS_TOKEN"],"businessInjection":{"headerName":"Authorization","prefix":"Bearer"}}}}`
	if _, err := db.Exec(`
		INSERT INTO capability_providers(
		 id,workspace_id,name,provider_kind,driver_key,transport,endpoint_config,driver_config,
		 created_by,updated_by)
		VALUES
		($1,$5,'Orders HTTP','HTTP_OPENAPI','http_openapi','HTTP',
		 '{"sourceUri":"https://orders.example.test/openapi.yaml","sourceRevision":"configured-1"}',$8::jsonb,$6,$6),
		($2,$5,'Future MCP','MCP_SERVER','mcp','HTTP','{}','{}',$6,$6),
		($3,$5,'Bad HTTP','HTTP_OPENAPI','http_openapi','HTTP',
		 '{"sourceUri":"https://bad.example.test/openapi.yaml","apiKey":"raw-provider-secret"}',$8::jsonb,$6,$6),
		($4,$7,'Other HTTP','HTTP_OPENAPI','http_openapi','HTTP',
		 '{"sourceUri":"https://other.example.test/openapi.yaml"}',$8::jsonb,$6,$6)
	`, providerImportProviderID, providerImportMCPID, providerImportBadID,
		providerImportOtherProvID, providerImportWorkspaceID, providerImportOwnerID, providerImportOtherSpaceID,
		httpDriver); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO secrets(id,workspace_id,name,kind,created_by,updated_by)
		VALUES($1,$2,'Provider API Key','API_KEY',$3,$3)
	`, providerImportSecretID, providerImportWorkspaceID, providerImportOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`
		INSERT INTO secret_versions(
		 id,workspace_id,secret_id,version_no,ciphertext,nonce,key_id,fingerprint,created_by)
		VALUES($1,$2,$3,1,decode('0102','hex'),decode('0304','hex'),'test-key','fingerprint-secret',$4)
	`, providerImportSecretVerID, providerImportWorkspaceID, providerImportSecretID, providerImportOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE secrets SET active_version_id=$2 WHERE id=$1`, providerImportSecretID, providerImportSecretVerID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO service_connections(
		 id,workspace_id,provider_id,name,alias,environment,auth_mode,auth_config,
		 credential_secret_id,status,created_by,updated_by)
		VALUES
		($1,$4,$5,'Orders Primary','primary','TEST','API_KEY',
		 '{"headerName":"X-API-Key"}',$7,'VERIFIED',$8,$8),
		($2,$4,$5,'Orders Unverified','unverified','TEST','NONE','{}',NULL,'UNVERIFIED',$8,$8),
		($3,$4,$6,'Wrong Provider','wrong','TEST','NONE','{}',NULL,'VERIFIED',$8,$8)
	`, providerImportConnectionID, providerImportUnverifiedID, providerImportWrongConnID,
		providerImportWorkspaceID, providerImportProviderID, providerImportBadID,
		providerImportSecretID, providerImportOwnerID); err != nil {
		t.Fatal(err)
	}
}

func validProviderOpenAPIDocument() []byte {
	return []byte(`
openapi: 3.0.3
info: { title: Provider Orders, version: 1.0.0 }
components:
  securitySchemes:
    ApiKey:
      type: apiKey
      in: header
      name: X-API-Key
paths:
  /orders:
    get:
      operationId: listOrders
      security: [{ ApiKey: [] }]
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  total: { type: integer }
`)
}

type unusedHTTPDiscoverer struct{}

func (unusedHTTPDiscoverer) DiscoverHTTP(context.Context, provider.DiscoveryRequest) (provider.DiscoveryPage, error) {
	return provider.DiscoveryPage{}, nil
}

type capturingProviderLoader struct {
	sources  []ProviderImportSource
	document LoadedProviderDocument
}

func (loader *capturingProviderLoader) LoadProviderDocument(
	_ context.Context,
	source ProviderImportSource,
) (LoadedProviderDocument, error) {
	loader.sources = append(loader.sources, source)
	return loader.document, nil
}
