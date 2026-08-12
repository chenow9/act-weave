package connection

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

const (
	connOwnerID           = "028f1f2e-7b5a-7c3d-8e9f-1234567890ab"
	connWorkspaceID       = "028f1f2e-7b5a-7c3d-8e9f-1234567890ac"
	connOtherSpaceID      = "028f1f2e-7b5a-7c3d-8e9f-1234567890ad"
	connProviderID        = "028f1f2e-7b5a-7c3d-8e9f-1234567890ae"
	connSecretID          = "028f1f2e-7b5a-7c3d-8e9f-1234567890af"
	connSecretVersionID   = "028f1f2e-7b5a-7c3d-8e9f-1234567890b0"
	connID                = "028f1f2e-7b5a-7c3d-8e9f-1234567890b1"
	connVerificationID    = "028f1f2e-7b5a-7c3d-8e9f-1234567890b2"
	connDeleteActorID     = "028f1f2e-7b5a-7c3d-8e9f-1234567890b3"
	connModelID           = "028f1f2e-7b5a-7c3d-8e9f-1234567890b4"
	connAgentID           = "028f1f2e-7b5a-7c3d-8e9f-1234567890b5"
	connDisabledAgentID   = "028f1f2e-7b5a-7c3d-8e9f-1234567890b6"
	connToolCapID         = "028f1f2e-7b5a-7c3d-8e9f-1234567890b7"
	connVersionCapID      = "028f1f2e-7b5a-7c3d-8e9f-1234567890b8"
	connDeletedToolID     = "028f1f2e-7b5a-7c3d-8e9f-1234567890b9"
	connToolVersionID     = "028f1f2e-7b5a-7c3d-8e9f-1234567890ba"
	connToolDefaultID     = "028f1f2e-7b5a-7c3d-8e9f-1234567890bb"
	connVersionDefaultID  = "028f1f2e-7b5a-7c3d-8e9f-1234567890bc"
	connEnabledBindingID  = "028f1f2e-7b5a-7c3d-8e9f-1234567890bd"
	connDisabledBindingID = "028f1f2e-7b5a-7c3d-8e9f-1234567890be"
	connDeletedToolConnID = "028f1f2e-7b5a-7c3d-8e9f-1234567890bf"
)

func TestConnectionMigrationRepositoryAndVerification(t *testing.T) {
	testDatabase := dbtest.New(t)
	// Repository requires 000060 outbound identity columns.
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 20 || version.Dirty {
		t.Fatalf("unexpected migration: %+v", version)
	}
	db := testDatabase.Open(t)
	insertConnectionFixtures(t, db)
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.Create(context.Background(), validConnection())
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if created.Status != StatusUnverified || created.MigrationState != MigrationStateNone ||
		created.OutboundIdentityPolicyVersion != 1 || created.MachineCredentialConfigured {
		t.Fatalf("unexpected connection DTO: %+v", created)
	}
	if created.CredentialConfigured {
		t.Fatalf("dual-mode create must not attach legacy business credentials: %+v", created)
	}
	encoded, _ := json.Marshal(created)
	for _, forbidden := range []string{"credential-value", "ciphertext", "nonce", "machineCredentialSecretId"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, encoded)
		}
	}
	read, err := repository.Get(context.Background(), connWorkspaceID, connID)
	if err != nil || read.ID != connID {
		t.Fatalf("get connection: %+v %v", read, err)
	}
	if _, err := repository.Get(context.Background(), connOtherSpaceID, connID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected scoped miss, got %v", err)
	}

	bad := validConnection()
	bad.ID = "028f1f2e-7b5a-7c3d-8e9f-1234567890b3"
	bad.Name = "Bad"
	bad.Alias = "bad"
	bad.OutboundIdentity = json.RawMessage(`{"schemaVersion":"outbound-connection.v1","mode":"REQUEST_PASSTHROUGH","requestPassthrough":{"maxResidenceSeconds":600},"tokenValue":"credential-value"}`)
	if _, err := repository.Create(context.Background(), bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected sensitive outbound identity rejection, got %v", err)
	}
	latency := 17
	verification, err := repository.RecordVerification(context.Background(), NewVerification{
		ID: connVerificationID, WorkspaceID: connWorkspaceID, ConnectionID: connID, Status: "SUCCEEDED",
		Diagnostics: json.RawMessage(`{"protocol":"HTTP","result":"reachable"}`), LatencyMS: &latency, TestedBy: connOwnerID,
	})
	if err != nil || verification.LatencyMS == nil || *verification.LatencyMS != 17 {
		t.Fatalf("record verification: %+v %v", verification, err)
	}
	read, err = repository.Get(context.Background(), connWorkspaceID, connID)
	if err != nil || read.Status != StatusVerified || read.LastVerifiedAt == nil || read.LastErrorCode != nil ||
		read.MigrationState != MigrationStateNone {
		t.Fatalf("verification state not persisted: %+v %v", read, err)
	}
}

func TestConnectionSoftDeleteProtectsActiveExecutionReferences(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 20 || version.Dirty {
		t.Fatalf("unexpected migration: %+v", version)
	}
	db := testDatabase.Open(t)
	insertConnectionDeleteFixtures(t, db)
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name         string
		connectionID string
	}{
		{name: "tool default", connectionID: connToolDefaultID},
		{name: "tool version default", connectionID: connVersionDefaultID},
		{name: "enabled agent binding", connectionID: connEnabledBindingID},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := repository.SoftDelete(context.Background(), connWorkspaceID,
				test.connectionID, connDeleteActorID, 1); !errors.Is(err, ErrConflict) {
				t.Fatalf("expected referenced connection conflict, got %v", err)
			}
			assertConnectionDeleteState(t, db, test.connectionID, false, connOwnerID, 1)
		})
	}

	if err := repository.SoftDelete(context.Background(), connWorkspaceID,
		connDisabledBindingID, connDeleteActorID, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale lock conflict, got %v", err)
	}
	assertConnectionDeleteState(t, db, connDisabledBindingID, false, connOwnerID, 1)
	if err := repository.SoftDelete(context.Background(), connWorkspaceID,
		connDisabledBindingID, connDeleteActorID, 1); err != nil {
		t.Fatalf("disabled binding should not block soft delete: %v", err)
	}
	assertConnectionDeleteState(t, db, connDisabledBindingID, true, connDeleteActorID, 2)

	if err := repository.SoftDelete(context.Background(), connWorkspaceID,
		connDeletedToolConnID, connDeleteActorID, 1); err != nil {
		t.Fatalf("deleted tool should not block soft delete: %v", err)
	}
	assertConnectionDeleteState(t, db, connDeletedToolConnID, true, connDeleteActorID, 2)
	if err := repository.SoftDelete(context.Background(), connWorkspaceID,
		connDeletedToolConnID, connDeleteActorID, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected repeated delete to report not found, got %v", err)
	}
}

func insertConnectionDeleteFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,username,display_name) VALUES
		 ($1,'connection.delete.owner','Connection Delete Owner'),
		 ($2,'connection.delete.actor','Connection Delete Actor')`, []any{connOwnerID, connDeleteActorID}},
		{`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		 VALUES($1,'connection-delete','Connection Delete','PRODUCTION',$2,$2,$2)`, []any{connWorkspaceID, connOwnerID}},
		{`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		 VALUES($1,$2,'Connection Delete Model','OPENAI_COMPATIBLE','https://models.example/v1','delete-model',$3,$3)`, []any{connModelID, connWorkspaceID, connOwnerID}},
		{`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES
		 ($1,$3,'Connection Agent',$4,$5,$5),
		 ($2,$3,'Connection Disabled Agent',$4,$5,$5)`, []any{connAgentID, connDisabledAgentID, connWorkspaceID, connModelID, connOwnerID}},
		{`INSERT INTO capability_providers(id,workspace_id,name,provider_kind,driver_key,transport,created_by,updated_by)
		 VALUES($1,$2,'Connection Delete Provider','HTTP_OPENAPI','http_openapi','HTTP',$3,$3)`, []any{connProviderID, connWorkspaceID, connOwnerID}},
		{`INSERT INTO service_connections(id,workspace_id,provider_id,name,alias,environment,auth_mode,created_by,updated_by) VALUES
		 ($1,$6,$7,'Tool Default Connection','tool-default','TEST','NONE',$8,$8),
		 ($2,$6,$7,'Version Default Connection','version-default','TEST','NONE',$8,$8),
		 ($3,$6,$7,'Enabled Binding Connection','enabled-binding','TEST','NONE',$8,$8),
		 ($4,$6,$7,'Disabled Binding Connection','disabled-binding','TEST','NONE',$8,$8),
		 ($5,$6,$7,'Deleted Tool Connection','deleted-tool','TEST','NONE',$8,$8)`, []any{
			connToolDefaultID, connVersionDefaultID, connEnabledBindingID, connDisabledBindingID,
			connDeletedToolConnID, connWorkspaceID, connProviderID, connOwnerID,
		}},
		{`INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by) VALUES
		 ($1,$4,'TOOL','Connection Default Tool','connection-default-tool',$5,$5),
		 ($2,$4,'TOOL','Connection Version Tool','connection-version-tool',$5,$5),
		 ($3,$4,'TOOL','Connection Deleted Tool','connection-deleted-tool',$5,$5)`, []any{
			connToolCapID, connVersionCapID, connDeletedToolID, connWorkspaceID, connOwnerID,
		}},
		{`INSERT INTO tools(capability_id,workspace_id,provider_id,default_connection_id) VALUES
		 ($1,$4,$5,$6),($2,$4,$5,NULL),($3,$4,$5,$7)`, []any{
			connToolCapID, connVersionCapID, connDeletedToolID, connWorkspaceID, connProviderID,
			connToolDefaultID, connDeletedToolConnID,
		}},
		{`INSERT INTO tool_versions(
		 id,workspace_id,capability_id,version_no,lifecycle_status,executor_type,provider_id,
		 default_connection_id,action_schema_version,action_config,input_schema,output_schema,
		 error_mappings,runtime_policy,risk_level,side_effect_level,checksum,created_by,updated_by)
		 VALUES($1,$2,$3,1,'DRAFT','HTTP',$4,$5,'http.v1','{}','{}','{}','{}','{}',
		 'LOW','READ',$6,$7,$7)`, []any{
			connToolVersionID, connWorkspaceID, connVersionCapID, connProviderID,
			connVersionDefaultID, strings.Repeat("a", 64), connOwnerID,
		}},
		{`INSERT INTO agent_capability_bindings(
		 workspace_id,agent_id,capability_id,version_policy,connection_id,enabled,bound_by)
		 VALUES($1,$2,$4,'FOLLOW_ACTIVE',$5,TRUE,$6),
		       ($1,$3,$4,'FOLLOW_ACTIVE',$7,FALSE,$6)`, []any{
			connWorkspaceID, connAgentID, connDisabledAgentID, connToolCapID,
			connEnabledBindingID, connOwnerID, connDisabledBindingID,
		}},
		{`UPDATE capabilities SET deleted_at=clock_timestamp() WHERE id=$1`, []any{connDeletedToolID}},
	}
	for index, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("insert connection delete fixture %d: %v", index, err)
		}
	}
}

func assertConnectionDeleteState(t *testing.T, db *sql.DB, connectionID string, deleted bool, updatedBy string, lockVersion int64) {
	t.Helper()
	var deletedAt sql.NullTime
	var actualUpdatedBy string
	var actualLockVersion int64
	if err := db.QueryRow(`
		SELECT deleted_at,updated_by,lock_version FROM service_connections
		WHERE workspace_id=$1 AND id=$2
	`, connWorkspaceID, connectionID).Scan(&deletedAt, &actualUpdatedBy, &actualLockVersion); err != nil {
		t.Fatal(err)
	}
	if deletedAt.Valid != deleted || actualUpdatedBy != updatedBy || actualLockVersion != lockVersion {
		t.Fatalf("unexpected delete state for %s: deleted=%t updatedBy=%s lockVersion=%d",
			connectionID, deletedAt.Valid, actualUpdatedBy, actualLockVersion)
	}
}

func insertConnectionFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,username,display_name) VALUES($1,'connection.owner','Connection Owner')`, []any{connOwnerID}},
		{`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		 ($1,'connection-workspace','Connection Workspace','PRODUCTION',$3,$3,$3),($2,'connection-other','Connection Other','SANDBOX',$3,$3,$3)`, []any{connWorkspaceID, connOtherSpaceID, connOwnerID}},
		{`INSERT INTO capability_providers(id,workspace_id,name,provider_kind,driver_key,transport,created_by,updated_by)
		 VALUES($1,$2,'Connection Provider','HTTP_OPENAPI','http_openapi','HTTP',$3,$3)`, []any{connProviderID, connWorkspaceID, connOwnerID}},
		{`INSERT INTO secrets(id,workspace_id,name,kind,created_by,updated_by) VALUES($1,$2,'Connection Credential','API_KEY',$3,$3)`, []any{connSecretID, connWorkspaceID, connOwnerID}},
		{`INSERT INTO secret_versions(id,workspace_id,secret_id,version_no,ciphertext,nonce,key_id,fingerprint,created_by)
		 VALUES($1,$2,$3,1,$4,$5,'local-v1','hmac-sha256:connection',$6)`, []any{connSecretVersionID, connWorkspaceID, connSecretID, []byte("ciphertext"), []byte("nonce"), connOwnerID}},
		{`UPDATE secrets SET active_version_id=$2 WHERE id=$1`, []any{connSecretID, connSecretVersionID}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("insert connection fixture: %v", err)
		}
	}
}

func validConnection() NewConnection {
	return NewConnection{
		ID: connID, WorkspaceID: connWorkspaceID, ProviderID: connProviderID, Name: "Orders Work", Alias: "work", Environment: "PRODUCTION",
		OutboundIdentity: json.RawMessage(`{
			"schemaVersion":"outbound-connection.v1",
			"mode":"REQUEST_PASSTHROUGH",
			"requestPassthrough":{"maxResidenceSeconds":600}
		}`),
		MachineCredentialSecretID: nil,
		GrantedScopes:             json.RawMessage(`["orders:read"]`),
		Policy:                    json.RawMessage(`{"timeoutMs":5000}`),
		MigrationState:            MigrationStateNone,
		CreatedBy:                 connOwnerID,
	}
}
func stringPtr(value string) *string { return &value }
func assertConnectionTablesMissing(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"connection_verifications", "service_connections"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("expected %s absent", table)
		}
	}
}
