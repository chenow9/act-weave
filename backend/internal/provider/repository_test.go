package provider

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

const (
	deleteOwnerID      = "038f1f2e-7b5a-7c3d-8e9f-1234567890ab"
	deleteWorkspaceID  = "038f1f2e-7b5a-7c3d-8e9f-1234567890ac"
	deleteProviderID   = "038f1f2e-7b5a-7c3d-8e9f-1234567890ad"
	deleteConnectionID = "038f1f2e-7b5a-7c3d-8e9f-1234567890ae"
	deletedConnection  = "038f1f2e-7b5a-7c3d-8e9f-1234567890af"
	deleteCapabilityID = "038f1f2e-7b5a-7c3d-8e9f-1234567890b0"
)

func TestSoftDeleteRejectsActiveConnectionWithoutSideEffects(t *testing.T) {
	repository, db := newProviderDeleteTest(t)

	if err := repository.SoftDelete(
		context.Background(), deleteWorkspaceID, deleteProviderID, deleteOwnerID, 1,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected active connection conflict, got %v", err)
	}
	assertProviderDeleteState(t, db, false, 1)
	assertConnectionState(t, db, deleteConnectionID, false, "VERIFIED", true, sql.NullString{}, 7)
}

func TestSoftDeleteRejectsActiveToolWithoutSideEffects(t *testing.T) {
	repository, db := newProviderDeleteTest(t)
	softDeleteConnectionFixture(t, db, deleteConnectionID)
	if _, err := db.Exec(`
		INSERT INTO capabilities(
			id,workspace_id,kind,name,slug,created_by,updated_by
		) VALUES($1,$2,'TOOL','Provider Delete Tool','provider-delete-tool',$3,$3)
	`, deleteCapabilityID, deleteWorkspaceID, deleteOwnerID); err != nil {
		t.Fatalf("insert provider tool capability: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO tools(capability_id,workspace_id,provider_id)
		VALUES($1,$2,$3)
	`, deleteCapabilityID, deleteWorkspaceID, deleteProviderID); err != nil {
		t.Fatalf("insert provider tool: %v", err)
	}

	if err := repository.SoftDelete(
		context.Background(), deleteWorkspaceID, deleteProviderID, deleteOwnerID, 1,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected active tool conflict, got %v", err)
	}
	assertProviderDeleteState(t, db, false, 1)
	var capabilityDeleted bool
	if err := db.QueryRow(`
		SELECT deleted_at IS NOT NULL FROM capabilities WHERE id=$1
	`, deleteCapabilityID).Scan(&capabilityDeleted); err != nil {
		t.Fatalf("read capability after provider delete conflict: %v", err)
	}
	if capabilityDeleted {
		t.Fatal("provider delete conflict mutated dependent capability")
	}
}

func TestSoftDeleteWithoutDependenciesPreservesOptimisticLock(t *testing.T) {
	repository, db := newProviderDeleteTest(t)
	softDeleteConnectionFixture(t, db, deleteConnectionID)

	if err := repository.SoftDelete(
		context.Background(), deleteWorkspaceID, deleteProviderID, deleteOwnerID, 2,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale provider delete conflict, got %v", err)
	}
	assertProviderDeleteState(t, db, false, 1)

	if err := repository.SoftDelete(
		context.Background(), deleteWorkspaceID, deleteProviderID, deleteOwnerID, 1,
	); err != nil {
		t.Fatalf("soft delete provider without dependencies: %v", err)
	}
	assertProviderDeleteState(t, db, true, 2)
	assertConnectionState(t, db, deleteConnectionID, true, "VERIFIED", true, sql.NullString{}, 7)
	assertConnectionState(t, db, deletedConnection, true, "VERIFIED", true, sql.NullString{}, 11)
	if _, err := repository.Get(context.Background(), deleteWorkspaceID, deleteProviderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted provider to be hidden, got %v", err)
	}
}

func newProviderDeleteTest(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,username,display_name) VALUES($1,'provider.delete.owner','Provider Delete Owner')`, []any{deleteOwnerID}},
		{`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		 VALUES($1,'provider-delete-workspace','Provider Delete Workspace','PRODUCTION',$2,$2,$2)`, []any{deleteWorkspaceID, deleteOwnerID}},
		{`INSERT INTO capability_providers(
			id,workspace_id,name,provider_kind,driver_key,transport,endpoint_config,
			driver_config,discovery_mode,created_by,updated_by
		 ) VALUES($1,$2,'Provider To Delete','HTTP_OPENAPI','http_openapi','HTTP',
			'{"serviceBaseUrl":"https://service.example","schemaVersion":2}','{}','MANUAL',$3,$3)`, []any{deleteProviderID, deleteWorkspaceID, deleteOwnerID}},
		{`INSERT INTO service_connections(
			id,workspace_id,provider_id,name,alias,environment,auth_mode,auth_config,
			status,last_verified_at,created_by,updated_by,lock_version
		 ) VALUES($1,$2,$3,'Active Connection','active','PRODUCTION','NONE','{}',
			'VERIFIED',clock_timestamp(),$4,$4,7)`, []any{deleteConnectionID, deleteWorkspaceID, deleteProviderID, deleteOwnerID}},
		{`INSERT INTO service_connections(
			id,workspace_id,provider_id,name,alias,environment,auth_mode,auth_config,
			status,last_verified_at,created_by,updated_by,lock_version,deleted_at
		 ) VALUES($1,$2,$3,'Deleted Connection','deleted','PRODUCTION','NONE','{}',
			'VERIFIED',clock_timestamp(),$4,$4,11,clock_timestamp())`, []any{deletedConnection, deleteWorkspaceID, deleteProviderID, deleteOwnerID}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("insert provider delete fixture: %v", err)
		}
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatalf("create provider repository: %v", err)
	}
	return repository, db
}

func softDeleteConnectionFixture(t *testing.T, db *sql.DB, connectionID string) {
	t.Helper()
	if _, err := db.Exec(`
		UPDATE service_connections SET deleted_at=clock_timestamp() WHERE id=$1
	`, connectionID); err != nil {
		t.Fatalf("soft delete connection fixture: %v", err)
	}
}

func assertProviderDeleteState(t *testing.T, db *sql.DB, deleted bool, lockVersion int64) {
	t.Helper()
	var actualDeleted bool
	var actualLock int64
	if err := db.QueryRow(`
		SELECT deleted_at IS NOT NULL,lock_version
		FROM capability_providers WHERE id=$1
	`, deleteProviderID).Scan(&actualDeleted, &actualLock); err != nil {
		t.Fatalf("read provider delete state: %v", err)
	}
	if actualDeleted != deleted || actualLock != lockVersion {
		t.Fatalf(
			"unexpected provider delete state: deleted=%v lock=%d; want deleted=%v lock=%d",
			actualDeleted, actualLock, deleted, lockVersion,
		)
	}
}

func assertConnectionState(
	t *testing.T,
	db *sql.DB,
	connectionID string,
	deleted bool,
	status string,
	verified bool,
	errorCode sql.NullString,
	lockVersion int64,
) {
	t.Helper()
	var actualDeleted bool
	var actualStatus string
	var actualVerifiedAt sql.NullTime
	var actualErrorCode sql.NullString
	var actualLock int64
	if err := db.QueryRow(`
		SELECT deleted_at IS NOT NULL,status,last_verified_at,last_error_code,lock_version
		FROM service_connections WHERE id=$1
	`, connectionID).Scan(
		&actualDeleted, &actualStatus, &actualVerifiedAt, &actualErrorCode, &actualLock,
	); err != nil {
		t.Fatalf("read connection delete state: %v", err)
	}
	if actualDeleted != deleted || actualStatus != status || actualVerifiedAt.Valid != verified ||
		actualErrorCode != errorCode || actualLock != lockVersion {
		t.Fatalf(
			"unexpected connection state: deleted=%v status=%s verified=%v error=%v lock=%d",
			actualDeleted, actualStatus, actualVerifiedAt.Valid, actualErrorCode, actualLock,
		)
	}
}
