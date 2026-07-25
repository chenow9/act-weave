package audit_test

import (
	"database/sql"
	"strings"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

const (
	auditMigrationUserID      = "a38f1f2e-7b5a-7c3d-8e9f-123456789001"
	auditMigrationWorkspaceID = "a38f1f2e-7b5a-7c3d-8e9f-123456789002"
	auditMigrationEventID     = "a38f1f2e-7b5a-7c3d-8e9f-123456789003"
	auditMigrationOutboxID    = "a38f1f2e-7b5a-7c3d-8e9f-123456789004"
	auditMigrationExportID    = "a38f1f2e-7b5a-7c3d-8e9f-123456789005"
)

func TestAuditMigration(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateTo(t, 30)
	if !version.Applied || version.Number != 30 || version.Dirty {
		t.Fatalf("audit migration version = %+v", version)
	}
	db := testDatabase.Open(t)
	insertAuditMigrationFixtures(t, db)

	for _, table := range []string{"audit_events", "audit_events_default", "outbox_events", "audit_exports"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("expected table %s: exists=%v err=%v", table, exists, err)
		}
	}
	for _, index := range []string{
		"audit_events_workspace_occurred_idx", "audit_events_workspace_actor_occurred_idx",
		"audit_events_workspace_resource_occurred_idx", "audit_events_request_id_idx",
		"audit_events_trace_id_idx", "outbox_events_unpublished_available_idx",
		"outbox_events_workspace_aggregate_idx", "audit_exports_workspace_requested_idx",
		"audit_exports_pending_idx",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass('public.' || $1) IS NOT NULL`, index).Scan(&exists); err != nil || !exists {
			t.Fatalf("expected index %s: exists=%v err=%v", index, exists, err)
		}
	}

	if _, err := db.Exec(`
		INSERT INTO audit_events(
		 id,workspace_id,actor_type,actor_id,actor_display,action,resource_type,
		 resource_id,result,request_id,trace_id,source_ip,user_agent,changes,metadata,schema_version
		) VALUES($1,$2,'USER',$3,'Owner','tool.release.published','TOOL',$4,
		 'SUCCESS','request-audit-1','trace-audit-1','203.0.113.10','audit-test',
		 '{"status":{"before":"TESTED","after":"PUBLISHED"}}',
		 '{"releaseNo":1}','audit.v1')
	`, auditMigrationEventID, auditMigrationWorkspaceID, auditMigrationUserID,
		"a38f1f2e-7b5a-7c3d-8e9f-123456789099"); err != nil {
		t.Fatalf("insert audit event: %v", err)
	}
	if _, err := db.Exec(`UPDATE audit_events SET result='FAILURE' WHERE id=$1`, auditMigrationEventID); err == nil {
		t.Fatal("audit event update was allowed")
	}
	if _, err := db.Exec(`DELETE FROM audit_events WHERE id=$1`, auditMigrationEventID); err == nil {
		t.Fatal("audit event delete was allowed")
	}
	if _, err := db.Exec(`
		INSERT INTO audit_events(
		 id,actor_type,actor_display,action,resource_type,result,changes,metadata,schema_version
		) VALUES('a38f1f2e-7b5a-7c3d-8e9f-123456789006','SYSTEM','Authentication',
		 'authentication.login.failed','AUTHENTICATION','FAILURE','{}','{}','audit.v1')
	`); err != nil {
		t.Fatalf("insert platform audit event: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO audit_events(
		 id,actor_type,actor_display,action,resource_type,result,changes,metadata,schema_version
		) VALUES('a38f1f2e-7b5a-7c3d-8e9f-123456789007','SYSTEM','bad',
		 'NOT STABLE','AUTHENTICATION','FAILURE','{}','{}','audit.v1')
	`); err == nil {
		t.Fatal("unstable audit action was accepted")
	}

	if _, err := db.Exec(`
		INSERT INTO outbox_events(
		 id,workspace_id,aggregate_type,aggregate_id,event_type,payload,
		 schema_version,idempotency_key
		) VALUES($1,$2,'TOOL',$3,'tool.release.published',
		 '{"releaseId":"a38f1f2e-7b5a-7c3d-8e9f-123456789099"}',
		 'tool.release.v1','tool-release:a38f1f2e-7b5a-7c3d-8e9f-123456789099')
	`, auditMigrationOutboxID, auditMigrationWorkspaceID,
		"a38f1f2e-7b5a-7c3d-8e9f-123456789099"); err != nil {
		t.Fatalf("insert outbox event: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO audit_exports(id,workspace_id,filter_snapshot,requested_by,expires_at)
		VALUES($1,$2,'{"result":["FAILURE","DENIED"]}',$3,clock_timestamp()+interval '1 hour')
	`, auditMigrationExportID, auditMigrationWorkspaceID, auditMigrationUserID); err != nil {
		t.Fatalf("insert audit export: %v", err)
	}

	for _, forbidden := range []string{"previous_hash", "event_hash", "signature", "compliance_status"} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM information_schema.columns
			 WHERE table_schema='public' AND table_name='audit_events' AND column_name=$1)
		`, forbidden).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("audit compliance placeholder column exists: %s", forbidden)
		}
	}
	var partitioned bool
	if err := db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM pg_partitioned_table p
		 JOIN pg_class c ON c.oid=p.partrelid WHERE c.relname='audit_events')
	`).Scan(&partitioned); err != nil || !partitioned {
		t.Fatalf("audit events partitioning: exists=%v err=%v", partitioned, err)
	}

	version = testDatabase.MigrateTo(t, 29)
	if !version.Applied || version.Number != 29 || version.Dirty {
		t.Fatalf("audit migration rollback = %+v", version)
	}
	for _, table := range []string{"audit_events", "outbox_events", "audit_exports"} {
		var relation sql.NullString
		if err := db.QueryRow(`SELECT to_regclass('public.' || $1)::text`, table).Scan(&relation); err != nil {
			t.Fatal(err)
		}
		if relation.Valid || strings.TrimSpace(relation.String) != "" {
			t.Fatalf("table %s remained after rollback: %+v", table, relation)
		}
	}
	version = testDatabase.MigrateTo(t, 30)
	if !version.Applied || version.Number != 30 || version.Dirty {
		t.Fatalf("audit migration reapply = %+v", version)
	}
}

func insertAuditMigrationFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO users(id,username,display_name)
		VALUES($1,'audit.migration.owner','Audit Migration Owner')
	`, auditMigrationUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'audit-migration','Audit Migration','PRODUCTION',$2,$2,$2)
	`, auditMigrationWorkspaceID, auditMigrationUserID); err != nil {
		t.Fatal(err)
	}
}
