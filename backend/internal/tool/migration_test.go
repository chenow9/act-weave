// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package tool_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
)

const (
	toolOwnerID       = "088f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	toolWorkspaceID   = "088f1f2e-7b5a-7c3d-8e9f-1234567890a2"
	toolOtherSpaceID  = "088f1f2e-7b5a-7c3d-8e9f-1234567890a3"
	toolCapabilityID  = "088f1f2e-7b5a-7c3d-8e9f-1234567890a4"
	toolWorkflowCapID = "088f1f2e-7b5a-7c3d-8e9f-1234567890a5"
	toolOtherCapID    = "088f1f2e-7b5a-7c3d-8e9f-1234567890a6"
	toolProviderID    = "088f1f2e-7b5a-7c3d-8e9f-1234567890a7"
	toolProvider2ID   = "088f1f2e-7b5a-7c3d-8e9f-1234567890a8"
	toolOtherProvID   = "088f1f2e-7b5a-7c3d-8e9f-1234567890a9"
	toolAssetID       = "088f1f2e-7b5a-7c3d-8e9f-1234567890aa"
	toolOtherAssetID  = "088f1f2e-7b5a-7c3d-8e9f-1234567890ab"
	toolConnectionID  = "088f1f2e-7b5a-7c3d-8e9f-1234567890ac"
	toolOtherConnID   = "088f1f2e-7b5a-7c3d-8e9f-1234567890ad"
	toolDraftID       = "088f1f2e-7b5a-7c3d-8e9f-1234567890ae"
	toolPublishedID   = "088f1f2e-7b5a-7c3d-8e9f-1234567890af"
	toolTestID        = "088f1f2e-7b5a-7c3d-8e9f-1234567890b0"
	toolSourceID      = "088f1f2e-7b5a-7c3d-8e9f-1234567890b1"
	toolChecksum      = "6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b"
)

func TestToolMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean tool migration version 13, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertToolFixtures(t, db)
	assertToolMigrationConstraints(t, db)

}

func insertToolFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,username,display_name) VALUES($1,'tool.owner','Tool Owner')`, []any{toolOwnerID}},
		{`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		 ($1,'tool-workspace','Tool Workspace','PRODUCTION',$3,$3,$3),
		 ($2,'tool-other','Tool Other','SANDBOX',$3,$3,$3)`, []any{toolWorkspaceID, toolOtherSpaceID, toolOwnerID}},
		{`INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by) VALUES
		 ($1,$4,'TOOL','Orders Tool','orders-tool',$6,$6),
		 ($2,$4,'WORKFLOW','Orders Workflow','orders-workflow',$6,$6),
		 ($3,$5,'TOOL','Other Tool','other-tool',$6,$6)`, []any{toolCapabilityID, toolWorkflowCapID, toolOtherCapID, toolWorkspaceID, toolOtherSpaceID, toolOwnerID}},
		{`INSERT INTO capability_providers(id,workspace_id,name,provider_kind,driver_key,transport,created_by,updated_by) VALUES
		 ($1,$4,'Tool Provider','HTTP_OPENAPI','http_openapi','HTTP',$6,$6),
		 ($2,$4,'Tool Provider Two','HTTP_OPENAPI','http_openapi','HTTP',$6,$6),
		 ($3,$5,'Other Tool Provider','HTTP_OPENAPI','http_openapi','HTTP',$6,$6)`, []any{toolProviderID, toolProvider2ID, toolOtherProvID, toolWorkspaceID, toolOtherSpaceID, toolOwnerID}},
		{`INSERT INTO provider_assets(id,workspace_id,provider_id,asset_kind,external_id,name,source_checksum) VALUES
		 ($1,$3,$4,'TOOL','orders.get','Get Order','sha256:orders'),
		 ($2,$5,$6,'TOOL','other.get','Other Get','sha256:other')`, []any{toolAssetID, toolOtherAssetID, toolWorkspaceID, toolProviderID, toolOtherSpaceID, toolOtherProvID}},
		{`INSERT INTO service_connections(id,workspace_id,provider_id,name,alias,environment,auth_mode,created_by,updated_by) VALUES
		 ($1,$3,$4,'Tool Connection','primary','TEST','NONE',$6,$6),
		 ($2,$5,$7,'Other Connection','other','TEST','NONE',$6,$6)`, []any{toolConnectionID, toolOtherConnID, toolWorkspaceID, toolProviderID, toolOtherSpaceID, toolOwnerID, toolOtherProvID}},
	}
	for index, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("insert tool fixture %d: %v", index, err)
		}
	}
}

func assertToolMigrationConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO tools(capability_id,workspace_id,provider_id,source_asset_id,default_connection_id)
		VALUES($1,$2,$3,$4,$5)
	`, toolCapabilityID, toolWorkspaceID, toolProviderID, toolAssetID, toolConnectionID); err != nil {
		t.Fatalf("insert tool specialization: %v", err)
	}
	assertToolStatementFails(t, db, `
		INSERT INTO tools(capability_id,workspace_id,provider_id) VALUES($1,$2,$3)
	`, toolWorkflowCapID, toolWorkspaceID, toolProviderID)
	assertToolStatementFails(t, db, `
		UPDATE tools SET provider_id=$2,source_asset_id=$3 WHERE capability_id=$1
	`, toolCapabilityID, toolProvider2ID, toolAssetID)
	assertToolStatementFails(t, db, `
		UPDATE tools SET default_connection_id=$2 WHERE capability_id=$1
	`, toolCapabilityID, toolOtherConnID)

	insertToolVersion(t, db, toolDraftID, 1, "DRAFT", "HTTP", nil)
	if _, err := db.Exec(`
		UPDATE tool_versions SET action_config='{"method":"POST","path":"/orders"}',
			updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE id=$1
	`, toolDraftID); err != nil {
		t.Fatalf("update draft version: %v", err)
	}
	assertToolStatementFails(t, db, `
		INSERT INTO tool_versions(
		 id,workspace_id,capability_id,version_no,lifecycle_status,executor_type,provider_id,
		 action_schema_version,action_config,input_schema,output_schema,risk_level,
		 side_effect_level,checksum,created_by,updated_by)
		VALUES($1,$2,$3,2,'DRAFT','INTERNAL',$4,'http.v1','{}','{}','{}','LOW','READ',$5,$6,$6)
	`, toolPublishedID, toolWorkspaceID, toolCapabilityID, toolProviderID, toolChecksum, toolOwnerID)
	insertToolVersion(t, db, toolPublishedID, 2, "PUBLISHED", "HTTP", stringPointer("clock_timestamp()"))
	assertToolStatementFails(t, db, `UPDATE tool_versions SET action_config='{}' WHERE id=$1`, toolPublishedID)
	assertToolStatementFails(t, db, `DELETE FROM tool_versions WHERE id=$1`, toolPublishedID)

	if _, err := db.Exec(`
		INSERT INTO tool_tests(
		 id,workspace_id,tool_version_id,status,connectivity_passed,response_schema_passed,
		 error_mapping_passed,runtime_policy_passed,request_summary,response_summary,
		 latency_ms,tested_by)
		VALUES($1,$2,$3,'SUCCEEDED',TRUE,TRUE,TRUE,TRUE,'{"keys":["orderId"]}',
		 '{"status":200}',12,$4)
	`, toolTestID, toolWorkspaceID, toolDraftID, toolOwnerID); err != nil {
		t.Fatalf("insert version-scoped tool test: %v", err)
	}
	assertToolStatementFails(t, db, `UPDATE tool_tests SET latency_ms=13 WHERE id=$1`, toolTestID)
	assertToolStatementFails(t, db, `DELETE FROM tool_tests WHERE id=$1`, toolTestID)
	assertToolStatementFails(t, db, `
		INSERT INTO tool_tests(
		 id,workspace_id,tool_version_id,status,connectivity_passed,response_schema_passed,
		 error_mapping_passed,runtime_policy_passed,error_code,tested_by)
		VALUES($1,$2,$3,'FAILED',FALSE,FALSE,FALSE,FALSE,'TOOL_TEST_FAILED',$4)
	`, toolSourceID, toolOtherSpaceID, toolDraftID, toolOwnerID)
}

func insertToolVersion(t *testing.T, db *sql.DB, id string, versionNo int, status, executor string, publishedExpression *string) {
	t.Helper()
	publishedSQL := "NULL"
	if publishedExpression != nil {
		publishedSQL = *publishedExpression
	}
	query := `
		INSERT INTO tool_versions(
		 id,workspace_id,capability_id,version_no,lifecycle_status,executor_type,provider_id,
		 provider_asset_id,default_connection_id,action_schema_version,action_config,input_schema,
		 output_schema,error_mappings,runtime_policy,risk_level,side_effect_level,
		 requires_confirmation,checksum,created_by,updated_by,published_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'http.v1',
		 '{"method":"GET","path":"/orders/{orderId}"}','{"type":"object"}',
		 '{"type":"object"}','{}','{"timeoutMs":5000}','LOW','READ',FALSE,$10,$11,$11,` + publishedSQL + `)
	`
	if _, err := db.Exec(query, id, toolWorkspaceID, toolCapabilityID, versionNo, status,
		executor, toolProviderID, toolAssetID, toolConnectionID, toolChecksum, toolOwnerID); err != nil {
		t.Fatalf("insert %s tool version: %v", status, err)
	}
}

func assertToolStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected tool statement to fail: %s", strings.TrimSpace(query))
	}
}

func assertToolTablesMissing(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, table := range []string{"tool_tests", "tool_versions", "tools"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("expected rolled-back %s to be absent", table)
		}
	}
}

func stringPointer(value string) *string { return &value }
