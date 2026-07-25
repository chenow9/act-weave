package openapiimport_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
)

const (
	importOwnerID       = "0a8f1f2e-7b5a-7c3d-8e9f-123456789001"
	importWorkspaceID   = "0a8f1f2e-7b5a-7c3d-8e9f-123456789002"
	importOtherSpaceID  = "0a8f1f2e-7b5a-7c3d-8e9f-123456789003"
	importProviderID    = "0a8f1f2e-7b5a-7c3d-8e9f-123456789004"
	importProviderTwoID = "0a8f1f2e-7b5a-7c3d-8e9f-123456789005"
	importOtherProvider = "0a8f1f2e-7b5a-7c3d-8e9f-123456789006"
	importConnectionID  = "0a8f1f2e-7b5a-7c3d-8e9f-123456789007"
	importConnectionTwo = "0a8f1f2e-7b5a-7c3d-8e9f-123456789008"
	importRecordID      = "0a8f1f2e-7b5a-7c3d-8e9f-123456789009"
	importEndpointID    = "0a8f1f2e-7b5a-7c3d-8e9f-12345678900a"
	importEndpointTwoID = "0a8f1f2e-7b5a-7c3d-8e9f-12345678900b"
	importCapabilityID  = "0a8f1f2e-7b5a-7c3d-8e9f-12345678900c"
	importOtherCapID    = "0a8f1f2e-7b5a-7c3d-8e9f-12345678900d"
	importRawObjectID   = "0a8f1f2e-7b5a-7c3d-8e9f-12345678900e"
	importContentSHA256 = "a948904f2f0f479b8f8197694b30184b0d2ed1c1cd2a1ec0fb85d299a192a447"
)

func TestOpenAPIImportMigration(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateTo(t, 14)
	if !version.Applied || version.Number != 14 || version.Dirty {
		t.Fatalf("expected clean openapi import migration version 14, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertOpenAPIImportFixtures(t, db)
	assertOpenAPIImportSchema(t, db)
	assertOpenAPIImportConstraints(t, db)

	version = testDatabase.MigrateTo(t, 13)
	if !version.Applied || version.Number != 13 || version.Dirty {
		t.Fatalf("expected clean openapi import rollback version 13, got %+v", version)
	}
	assertOpenAPIImportTablesMissing(t, testDatabase.DSN())
	version = testDatabase.MigrateTo(t, 14)
	if !version.Applied || version.Number != 14 || version.Dirty {
		t.Fatalf("expected clean openapi import migration reapply, got %+v", version)
	}
}

func insertOpenAPIImportFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,username,display_name) VALUES($1,'openapi.owner','OpenAPI Owner')`, []any{importOwnerID}},
		{`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		 ($1,'openapi-space','OpenAPI Space','PRODUCTION',$3,$3,$3),
		 ($2,'openapi-other','OpenAPI Other','SANDBOX',$3,$3,$3)`, []any{importWorkspaceID, importOtherSpaceID, importOwnerID}},
		{`INSERT INTO capability_providers(id,workspace_id,name,provider_kind,driver_key,transport,created_by,updated_by) VALUES
		 ($1,$4,'OpenAPI Provider','HTTP_OPENAPI','http_openapi','HTTP',$6,$6),
		 ($2,$4,'OpenAPI Provider Two','HTTP_OPENAPI','http_openapi','HTTP',$6,$6),
		 ($3,$5,'Other OpenAPI Provider','HTTP_OPENAPI','http_openapi','HTTP',$6,$6)`, []any{importProviderID, importProviderTwoID, importOtherProvider, importWorkspaceID, importOtherSpaceID, importOwnerID}},
		{`INSERT INTO service_connections(id,workspace_id,provider_id,name,alias,environment,auth_mode,created_by,updated_by) VALUES
		 ($1,$3,$4,'OpenAPI Connection','primary','TEST','NONE',$6,$6),
		 ($2,$3,$5,'OpenAPI Connection Two','secondary','TEST','NONE',$6,$6)`, []any{importConnectionID, importConnectionTwo, importWorkspaceID, importProviderID, importProviderTwoID, importOwnerID}},
		{`INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by) VALUES
		 ($1,$3,'TOOL','Generated Tool','generated-tool',$4,$4),
		 ($2,$5,'TOOL','Other Generated Tool','other-generated-tool',$4,$4)`, []any{importCapabilityID, importOtherCapID, importWorkspaceID, importOwnerID, importOtherSpaceID}},
		{`INSERT INTO tools(capability_id,workspace_id,provider_id) VALUES($1,$2,$3)`, []any{importCapabilityID, importWorkspaceID, importProviderID}},
	}
	for index, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("insert openapi fixture %d: %v", index, err)
		}
	}
}

func assertOpenAPIImportSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	var hasAgentID bool
	if err := db.QueryRow(`
		SELECT EXISTS (
		 SELECT 1 FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='openapi_imports' AND column_name='agent_id'
		)
	`).Scan(&hasAgentID); err != nil {
		t.Fatal(err)
	}
	if hasAgentID {
		t.Fatal("openapi_imports must not bind an agent")
	}

	for _, indexName := range []string{
		"openapi_imports_workspace_status_created_idx",
		"openapi_imports_workspace_checksum_created_idx",
		"openapi_endpoints_workspace_import_ready_idx",
		"openapi_endpoints_workspace_generated_capability_idx",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+indexName).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected index %s", indexName)
		}
	}
}

func assertOpenAPIImportConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO openapi_imports(
		 id,workspace_id,provider_id,connection_id,source_type,source_uri,file_name,
		 raw_object_id,content_sha256,parser_version,status,total_endpoints,
		 ready_endpoints,issue_count,created_by)
		VALUES($1,$2,$3,$4,'URL','https://api.example.test/openapi.yaml','orders.yaml',
		 $5,$6,'kin-openapi/0.133.0','SUCCEEDED',1,1,0,$7)
	`, importRecordID, importWorkspaceID, importProviderID, importConnectionID,
		importRawObjectID, importContentSHA256, importOwnerID); err != nil {
		t.Fatalf("insert openapi import: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO openapi_endpoints(
		 id,workspace_id,import_id,method,path,operation_id,summary,input_schema,
		 output_schema,issues,ready)
		VALUES($1,$2,$3,'GET','/orders/{orderId}','getOrder','Get order',
		 '{"type":"object"}','{"type":"object"}','[]',TRUE)
	`, importEndpointID, importWorkspaceID, importRecordID); err != nil {
		t.Fatalf("insert openapi endpoint: %v", err)
	}

	if _, err := db.Exec(`
		UPDATE openapi_endpoints SET generated_capability_id=$2 WHERE id=$1
	`, importEndpointID, importCapabilityID); err != nil {
		t.Fatalf("track generated capability: %v", err)
	}
	if _, err := db.Exec(`UPDATE tools SET source_endpoint_id=$2 WHERE capability_id=$1`, importCapabilityID, importEndpointID); err != nil {
		t.Fatalf("track source endpoint from tool: %v", err)
	}

	assertOpenAPIStatementFails(t, db, `
		INSERT INTO openapi_endpoints(id,workspace_id,import_id,method,path,input_schema,output_schema)
		VALUES($1,$2,$3,'GET','/orders/{orderId}','{}','{}')
	`, importEndpointTwoID, importWorkspaceID, importRecordID)
	assertOpenAPIStatementFails(t, db, `
		INSERT INTO openapi_endpoints(id,workspace_id,import_id,method,path,input_schema,output_schema)
		VALUES($1,$2,$3,'get','/orders','{}','{}')
	`, importEndpointTwoID, importWorkspaceID, importRecordID)
	assertOpenAPIStatementFails(t, db, `
		INSERT INTO openapi_endpoints(id,workspace_id,import_id,method,path,input_schema,output_schema)
		VALUES($1,$2,$3,'POST','orders','{}','{}')
	`, importEndpointTwoID, importWorkspaceID, importRecordID)
	assertOpenAPIStatementFails(t, db, `
		INSERT INTO openapi_endpoints(id,workspace_id,import_id,method,path,input_schema,output_schema,issues)
		VALUES($1,$2,$3,'POST','/orders','{}','{}','{}')
	`, importEndpointTwoID, importWorkspaceID, importRecordID)
	assertOpenAPIStatementFails(t, db, `
		UPDATE openapi_endpoints SET generated_capability_id=$2 WHERE id=$1
	`, importEndpointID, importOtherCapID)

	assertOpenAPIStatementFails(t, db, `
		INSERT INTO openapi_imports(
		 id,workspace_id,provider_id,source_type,file_name,raw_object_id,
		 content_sha256,parser_version,created_by)
		VALUES($1,$2,$3,'FILE','other.yaml',$4,$5,'parser/v1',$6)
	`, importEndpointTwoID, importWorkspaceID, importOtherProvider, importRawObjectID, importContentSHA256, importOwnerID)
	assertOpenAPIStatementFails(t, db, `
		INSERT INTO openapi_imports(
		 id,workspace_id,provider_id,connection_id,source_type,file_name,raw_object_id,
		 content_sha256,parser_version,created_by)
		VALUES($1,$2,$3,$4,'RAW','raw.json',$5,$6,'parser/v1',$7)
	`, importEndpointTwoID, importWorkspaceID, importProviderID, importConnectionTwo,
		importRawObjectID, importContentSHA256, importOwnerID)
	assertOpenAPIStatementFails(t, db, `
		INSERT INTO openapi_imports(
		 id,workspace_id,source_type,source_uri,file_name,raw_object_id,
		 content_sha256,parser_version,created_by)
		VALUES($1,$2,'FILE','https://example.test/spec','file.yaml',$3,$4,'parser/v1',$5)
	`, importEndpointTwoID, importWorkspaceID, importRawObjectID, importContentSHA256, importOwnerID)
	assertOpenAPIStatementFails(t, db, `
		INSERT INTO openapi_imports(
		 id,workspace_id,source_type,file_name,raw_object_id,content_sha256,
		 parser_version,total_endpoints,ready_endpoints,created_by)
		VALUES($1,$2,'RAW','raw.json',$3,$4,'parser/v1',1,2,$5)
	`, importEndpointTwoID, importWorkspaceID, importRawObjectID, importContentSHA256, importOwnerID)
	assertOpenAPIStatementFails(t, db, `UPDATE tools SET source_endpoint_id=$2 WHERE capability_id=$1`, importCapabilityID, importEndpointTwoID)
}

func assertOpenAPIStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected openapi statement to fail: %s", strings.TrimSpace(query))
	}
}

func assertOpenAPIImportTablesMissing(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, table := range []string{"openapi_endpoints", "openapi_imports"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("expected rolled-back %s to be absent", table)
		}
	}
}
