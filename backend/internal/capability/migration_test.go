// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
package capability_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
)

const (
	capOwnerID      = "058f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	capWorkspaceID  = "058f1f2e-7b5a-7c3d-8e9f-1234567890a2"
	capOtherSpaceID = "058f1f2e-7b5a-7c3d-8e9f-1234567890a3"
	capabilityID    = "058f1f2e-7b5a-7c3d-8e9f-1234567890a4"
	capSecondID     = "058f1f2e-7b5a-7c3d-8e9f-1234567890a5"
	capOtherID      = "058f1f2e-7b5a-7c3d-8e9f-1234567890a6"
	releaseID       = "058f1f2e-7b5a-7c3d-8e9f-1234567890a7"
	releaseSecondID = "058f1f2e-7b5a-7c3d-8e9f-1234567890a8"
	releaseOtherID  = "058f1f2e-7b5a-7c3d-8e9f-1234567890a9"
	sourceID        = "058f1f2e-7b5a-7c3d-8e9f-1234567890aa"
	releaseChecksum = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
)

func TestCapabilityMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean capability migration version 11, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertCapabilityFixtures(t, db)
	assertCapabilityConstraints(t, db)

}

func insertCapabilityFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'cap.owner','Capability Owner')`, capOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'capability-workspace','Capability Workspace','PRODUCTION',$3,$3,$3),
		($2,'capability-other','Capability Other','SANDBOX',$3,$3,$3)
	`, capWorkspaceID, capOtherSpaceID, capOwnerID); err != nil {
		t.Fatal(err)
	}
}

func assertCapabilityConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, values := range []struct{ id, workspace, kind, name, slug string }{
		{capabilityID, capWorkspaceID, "TOOL", "Get Order", "get-order"},
		{capSecondID, capWorkspaceID, "WORKFLOW", "Get Order Workflow", "get-order-workflow"},
		{capOtherID, capOtherSpaceID, "TOOL", "Other Get Order", "other-get-order"},
	} {
		if _, err := db.Exec(`
			INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by)
			VALUES($1,$2,$3,$4,$5,$6,$6)
		`, values.id, values.workspace, values.kind, values.name, values.slug, capOwnerID); err != nil {
			t.Fatalf("insert capability: %v", err)
		}
	}
	insertRelease(t, db, releaseID, capWorkspaceID, capabilityID, 1, "get_order")
	insertRelease(t, db, releaseSecondID, capWorkspaceID, capSecondID, 1, "get_order")
	insertRelease(t, db, releaseOtherID, capOtherSpaceID, capOtherID, 1, "get_order")
	if _, err := db.Exec(`UPDATE capabilities SET active_release_id=$2 WHERE id=$1`, capabilityID, releaseID); err != nil {
		t.Fatalf("activate release: %v", err)
	}
	if _, err := db.Exec(`UPDATE capabilities SET active_release_id=$2 WHERE id=$1`, capOtherID, releaseOtherID); err != nil {
		t.Fatalf("activate same callable in other workspace: %v", err)
	}
	assertCapabilityStatementFails(t, db, `UPDATE capabilities SET active_release_id=$2 WHERE id=$1`, capSecondID, releaseSecondID)
	assertCapabilityStatementFails(t, db, `UPDATE capabilities SET active_release_id=$2 WHERE id=$1`, capabilityID, releaseOtherID)
	assertCapabilityStatementFails(t, db, `UPDATE capability_releases SET callable_description='changed' WHERE id=$1`, releaseID)
	assertCapabilityStatementFails(t, db, `UPDATE capability_releases SET retired_at=clock_timestamp() WHERE id=$1`, releaseID)
	assertCapabilityStatementFails(t, db, `DELETE FROM capability_releases WHERE id=$1`, releaseID)

	if _, err := db.Exec(`UPDATE capabilities SET active_release_id=NULL WHERE id=$1`, capabilityID); err != nil {
		t.Fatalf("clear active release: %v", err)
	}
	if _, err := db.Exec(`UPDATE capability_releases SET retired_at=clock_timestamp() WHERE id=$1`, releaseID); err != nil {
		t.Fatalf("retire inactive release: %v", err)
	}
	assertCapabilityStatementFails(t, db, `UPDATE capability_releases SET retired_at=clock_timestamp() WHERE id=$1`, releaseID)
	assertCapabilityStatementFails(t, db, `UPDATE capabilities SET active_release_id=$2 WHERE id=$1`, capabilityID, releaseID)

	assertCapabilityStatementFails(t, db, `
		INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by)
		VALUES($1,$2,'INTERNAL','Invalid Kind','invalid-kind',$3,$3)
	`, sourceID, capWorkspaceID, capOwnerID)
}

func insertRelease(t *testing.T, db *sql.DB, id, workspaceID, capabilityID string, releaseNo int, callable string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO capability_releases(
			id,workspace_id,capability_id,release_no,source_type,source_id,
			callable_name,callable_description,input_schema,output_schema,risk_level,
			side_effect_level,requires_confirmation,checksum,published_by
		) VALUES($1,$2,$3,$4,'TOOL_VERSION',$5,$6,'Callable','{"type":"object"}',
			'{"type":"object"}','LOW','READ',FALSE,$7,$8)
	`, id, workspaceID, capabilityID, releaseNo, sourceID, callable, releaseChecksum, capOwnerID); err != nil {
		t.Fatalf("insert release: %v", err)
	}
}

func assertCapabilityStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected capability statement to fail: %s", strings.TrimSpace(query))
	}
}

func assertCapabilityTablesMissing(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, table := range []string{"capability_releases", "capabilities"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("expected rolled-back %s to be absent", table)
		}
	}
	var assetConstraint bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='provider_assets_materialized_capability_fk')
	`).Scan(&assetConstraint); err != nil {
		t.Fatal(err)
	}
	if assetConstraint {
		t.Fatal("provider asset capability FK survived rollback")
	}
}
