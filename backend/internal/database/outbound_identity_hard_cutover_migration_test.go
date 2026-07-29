// Historical step-migration coverage was retired when migrations were squashed into 000001_init (see migrations_archive/).
// Historical step-migration tests were retired when migrations were squashed
// into 000001_init. See migrations_archive/ for the pre-squash chain.
package database_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"actweave/backend/internal/database"
	"actweave/backend/internal/database/dbtest"
)

// Fixed UUIDs for 000060 hard-cut fixtures. Values are not secret material.
const (
	cutUserID           = "018f60a0-0001-7000-8000-000000000001"
	cutWorkspaceID      = "018f60a0-0001-7000-8000-000000000002"
	cutHTTPProviderID   = "018f60a0-0001-7000-8000-000000000010"
	cutOtherProviderID  = "018f60a0-0001-7000-8000-000000000011"
	cutConnActiveID     = "018f60a0-0001-7000-8000-000000000020"
	cutConnSharedID     = "018f60a0-0001-7000-8000-000000000021"
	cutConnSoftDeleted  = "018f60a0-0001-7000-8000-000000000022"
	cutConnNoSecretID   = "018f60a0-0001-7000-8000-000000000023"
	cutConnNonTargetID  = "018f60a0-0001-7000-8000-000000000024"
	cutSecretSingleID   = "018f60a0-0001-7000-8000-000000000030"
	cutSecretSharedID   = "018f60a0-0001-7000-8000-000000000031"
	cutSecretSoftID     = "018f60a0-0001-7000-8000-000000000032"
	cutSecretModelID    = "018f60a0-0001-7000-8000-000000000033"
	cutSecretNonTarget  = "018f60a0-0001-7000-8000-000000000034"
	cutSecretModelOnly  = "018f60a0-0001-7000-8000-000000000035"
	cutVersionSingle    = "018f60a0-0001-7000-8000-000000000040"
	cutVersionSharedA   = "018f60a0-0001-7000-8000-000000000041"
	cutVersionSharedB   = "018f60a0-0001-7000-8000-000000000042" // revoked history
	cutVersionSoft      = "018f60a0-0001-7000-8000-000000000043"
	cutVersionModel     = "018f60a0-0001-7000-8000-000000000044"
	cutVersionNonTarget = "018f60a0-0001-7000-8000-000000000045"
	cutVersionModelOnly = "018f60a0-0001-7000-8000-000000000046"
	cutModelConfigID    = "018f60a0-0001-7000-8000-000000000050"
	cutModelConfigOnly  = "018f60a0-0001-7000-8000-000000000051"
)

func TestOutboundIdentityHardCutoverMigration_SchemaUpDownRollForward(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean version 60, got %+v", version)
	}
	db := testDatabase.Open(t)
	assertOutboundIdentityHardCutoverSchema(t, db, true)

	// Down only reverses schema; must not recreate secrets (none existed here).
	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean rollback to 59, got %+v", version)
	}
	assertOutboundIdentityHardCutoverSchema(t, db, false)

	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean reapply 60, got %+v", version)
	}
	assertOutboundIdentityHardCutoverSchema(t, db, true)
}

func TestOutboundIdentityHardCutoverMigration_SuccessfulPhysicalDelete(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean version 59, got %+v", version)
	}
	db := testDatabase.Open(t)
	seedHardCutBase(t, db)
	// Success path fixtures:
	// - single-ref active HTTP connection secret
	// - multi target connections sharing one secret
	// - soft-deleted target with secret + revoked version history
	// - target with no secret
	// - model-only secret (must survive; not a candidate)
	// - non-target MCP connection secret (must survive when not shared with target candidates)
	seedSecretWithVersions(t, db, cutSecretSingleID, cutVersionSingle, "", false)
	seedSecretWithVersions(t, db, cutSecretSharedID, cutVersionSharedA, cutVersionSharedB, true)
	seedSecretWithVersions(t, db, cutSecretSoftID, cutVersionSoft, "", false)
	seedSecretWithVersions(t, db, cutSecretModelOnly, cutVersionModelOnly, "", false)
	seedSecretWithVersions(t, db, cutSecretNonTarget, cutVersionNonTarget, "", false)

	seedHTTPConnection(t, db, cutConnActiveID, "active-conn", cutSecretSingleID, false, "VERIFIED")
	seedHTTPConnection(t, db, cutConnSharedID, "shared-conn-a", cutSecretSharedID, false, "VERIFIED")
	seedHTTPConnection(t, db, cutConnNoSecretID, "no-secret-conn", "", false, "UNVERIFIED")
	// Second target sharing cutSecretSharedID via soft-deleted row.
	seedHTTPConnection(t, db, cutConnSoftDeleted, "soft-conn", cutSecretSoftID, true, "DISABLED")
	// Also share shared secret from a second active connection.
	seedHTTPConnection(t, db, "018f60a0-0001-7000-8000-000000000025", "shared-conn-b", cutSecretSharedID, false, "ERROR")

	seedNonTargetConnection(t, db, cutConnNonTargetID, "mcp-conn", cutSecretNonTarget)
	seedModelConfig(t, db, cutModelConfigOnly, cutSecretModelOnly)

	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean hard cut 60, got %+v", version)
	}

	// Target secrets physically deleted.
	assertSecretGone(t, db, cutSecretSingleID)
	assertSecretGone(t, db, cutSecretSharedID)
	assertSecretGone(t, db, cutSecretSoftID)
	// Non-candidate secrets preserved.
	assertSecretPresent(t, db, cutSecretModelOnly)
	assertSecretPresent(t, db, cutSecretNonTarget)

	// Active targets disabled + migration required; credential cleared.
	assertConnectionCutState(t, db, cutConnActiveID, "DISABLED", "MIGRATION_REQUIRED", true)
	assertConnectionCutState(t, db, cutConnSharedID, "DISABLED", "MIGRATION_REQUIRED", true)
	assertConnectionCutState(t, db, "018f60a0-0001-7000-8000-000000000025", "DISABLED", "MIGRATION_REQUIRED", true)
	assertConnectionCutState(t, db, cutConnNoSecretID, "DISABLED", "MIGRATION_REQUIRED", true)
	// Soft-deleted: migration required, credential cleared, status unchanged DISABLED.
	assertConnectionCutState(t, db, cutConnSoftDeleted, "DISABLED", "MIGRATION_REQUIRED", true)

	// Non-target connection unchanged and still has secret.
	var nonTargetSecret sql.NullString
	var nonTargetStatus, nonTargetMigration string
	if err := db.QueryRow(`
		SELECT credential_secret_id::text, status, migration_state
		FROM service_connections WHERE id=$1
	`, cutConnNonTargetID).Scan(&nonTargetSecret, &nonTargetStatus, &nonTargetMigration); err != nil {
		t.Fatalf("read non-target connection: %v", err)
	}
	if !nonTargetSecret.Valid || nonTargetSecret.String != cutSecretNonTarget {
		t.Fatalf("non-target secret mutated: %v", nonTargetSecret)
	}
	if nonTargetMigration != "NONE" {
		t.Fatalf("non-target migration_state=%s", nonTargetMigration)
	}

	// Audit: one SYSTEM event with aggregate counts only (no secret ids).
	var action, actorType, metadata string
	if err := db.QueryRow(`
		SELECT action, actor_type, metadata::text
		FROM audit_events
		WHERE workspace_id=$1 AND action='outbound.identity.legacy_secret.deleted'
	`, cutWorkspaceID).Scan(&action, &actorType, &metadata); err != nil {
		t.Fatalf("read audit event: %v", err)
	}
	if actorType != "SYSTEM" {
		t.Fatalf("actor_type=%s", actorType)
	}
	for _, banned := range []string{
		cutSecretSingleID, cutSecretSharedID, cutSecretSoftID,
		"fingerprint", "secret-name", `"name"`,
	} {
		if strings.Contains(metadata, banned) {
			t.Fatalf("audit metadata leaked %q: %s", banned, metadata)
		}
	}
	if !strings.Contains(metadata, "deletedSecretCount") || !strings.Contains(metadata, "targetConnectionCount") {
		t.Fatalf("audit missing aggregate keys: %s", metadata)
	}

	// Down does not restore secrets.
	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected down to 59, got %+v", version)
	}
	assertSecretGone(t, db, cutSecretSingleID)
	assertSecretGone(t, db, cutSecretSharedID)
	assertSecretPresent(t, db, cutSecretModelOnly)

	// Roll-forward 59 → 60 again after data loss: still succeeds (no candidates left).
	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected roll-forward reapply 60, got %+v", version)
	}
}

func TestOutboundIdentityHardCutoverMigration_BlocksModelConfigShare_ZeroMutation(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	_ = testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	seedHardCutBase(t, db)
	seedSecretWithVersions(t, db, cutSecretModelID, cutVersionModel, "", false)
	seedHTTPConnection(t, db, cutConnActiveID, "shared-with-model", cutSecretModelID, false, "VERIFIED")
	seedModelConfig(t, db, cutModelConfigID, cutSecretModelID)

	if err := attemptMigrateTo(t, testDatabase, 60); err == nil {
		t.Fatal("expected migration blocked by model_configs share")
	}
	assertHardCutBlockedWithZeroMutation(t, db, cutSecretModelID, cutConnActiveID, "VERIFIED", cutSecretModelID)
}

func TestOutboundIdentityHardCutoverMigration_BlocksNonTargetShare_ZeroMutation(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	_ = testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	seedHardCutBase(t, db)
	seedSecretWithVersions(t, db, cutSecretNonTarget, cutVersionNonTarget, "", false)
	// Same secret on HTTP target and MCP non-target → must block before any mutation.
	seedHTTPConnection(t, db, cutConnActiveID, "http-shared", cutSecretNonTarget, false, "VERIFIED")
	seedNonTargetConnection(t, db, cutConnNonTargetID, "mcp-shared", cutSecretNonTarget)

	if err := attemptMigrateTo(t, testDatabase, 60); err == nil {
		t.Fatal("expected migration blocked by non-target connection share")
	}
	assertHardCutBlockedWithZeroMutation(t, db, cutSecretNonTarget, cutConnActiveID, "VERIFIED", cutSecretNonTarget)
}

func TestOutboundIdentityHardCutoverMigration_NoSecretTargetsStillDisable(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	_ = testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	seedHardCutBase(t, db)
	seedHTTPConnection(t, db, cutConnNoSecretID, "open-api-none", "", false, "VERIFIED")

	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("expected clean 60, got %+v", version)
	}
	assertConnectionCutState(t, db, cutConnNoSecretID, "DISABLED", "MIGRATION_REQUIRED", true)

	// Audit still written for the workspace with zero secret deletes.
	var metadata string
	if err := db.QueryRow(`
		SELECT metadata::text FROM audit_events
		WHERE workspace_id=$1 AND action='outbound.identity.legacy_secret.deleted'
	`, cutWorkspaceID).Scan(&metadata); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !strings.Contains(metadata, `"deletedSecretCount": 0`) &&
		!strings.Contains(metadata, `"deletedSecretCount":0`) {
		t.Fatalf("expected zero secret delete count, got %s", metadata)
	}
}

func assertOutboundIdentityHardCutoverSchema(t *testing.T, db *sql.DB, wantPresent bool) {
	t.Helper()
	var providerCol, connIdentity, connPolicy, connMigration, connMachine bool
	var instances, affinities bool
	if err := db.QueryRow(`
		SELECT
		 EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='capability_providers' AND column_name='outbound_identity_policy_version'),
		 EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='service_connections' AND column_name='outbound_identity'),
		 EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='service_connections' AND column_name='outbound_identity_policy_version'),
		 EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='service_connections' AND column_name='migration_state'),
		 EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='service_connections' AND column_name='machine_credential_secret_id'),
		 EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='outbound_runtime_instances'),
		 EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='outbound_runtime_affinities')
	`).Scan(&providerCol, &connIdentity, &connPolicy, &connMigration, &connMachine, &instances, &affinities); err != nil {
		t.Fatalf("schema probe: %v", err)
	}
	if providerCol != wantPresent || connIdentity != wantPresent || connPolicy != wantPresent ||
		connMigration != wantPresent || connMachine != wantPresent ||
		instances != wantPresent || affinities != wantPresent {
		t.Fatalf("schema present=%t got provider=%t identity=%t policy=%t migration=%t machine=%t instances=%t affinities=%t",
			wantPresent, providerCol, connIdentity, connPolicy, connMigration, connMachine, instances, affinities)
	}
	if !wantPresent {
		return
	}
	// Legacy evidence columns remain for phase-1.
	var legacyAuthMode, legacyAuthConfig, legacyCredential bool
	if err := db.QueryRow(`
		SELECT
		 EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='service_connections' AND column_name='auth_mode'),
		 EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='service_connections' AND column_name='auth_config'),
		 EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='service_connections' AND column_name='credential_secret_id')
	`).Scan(&legacyAuthMode, &legacyAuthConfig, &legacyCredential); err != nil {
		t.Fatalf("legacy columns: %v", err)
	}
	if !legacyAuthMode || !legacyAuthConfig || !legacyCredential {
		t.Fatal("000060 must retain auth_mode/auth_config/credential_secret_id as read-only evidence")
	}
}

func seedHardCutBase(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users (id, username, display_name) VALUES ($1,'cut.owner','Cut Owner')`, cutUserID); err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (id, slug, display_name, mode, owner_user_id, created_by, updated_by)
		VALUES ($1,'cut-ws','Cut Workspace','PRODUCTION',$2,$2,$2)
	`, cutWorkspaceID, cutUserID); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_members (workspace_id, user_id, role, invited_by)
		VALUES ($1,$2,'OWNER',$2)
	`, cutWorkspaceID, cutUserID); err != nil {
		t.Fatalf("member: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO capability_providers (
			id, workspace_id, name, provider_kind, driver_key, transport,
			endpoint_config, created_by, updated_by
		) VALUES
			($1,$3,'HTTP Provider','HTTP_OPENAPI','http_openapi','HTTP','{"baseUrl":"https://api.example"}',$4,$4),
			($2,$3,'MCP Provider','MCP_SERVER','mcp','HTTP','{"baseUrl":"https://mcp.example"}',$4,$4)
	`, cutHTTPProviderID, cutOtherProviderID, cutWorkspaceID, cutUserID); err != nil {
		t.Fatalf("providers: %v", err)
	}
}

func seedSecretWithVersions(t *testing.T, db *sql.DB, secretID, activeVersionID, revokedVersionID string, withRevoked bool) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO secrets (id, workspace_id, name, kind, created_by, updated_by)
		VALUES ($1,$2,$3,'API_KEY',$4,$4)
	`, secretID, cutWorkspaceID, "sec-"+secretID[len(secretID)-4:], cutUserID); err != nil {
		t.Fatalf("secret %s: %v", secretID, err)
	}
	if _, err := db.Exec(`
		INSERT INTO secret_versions (
			id, workspace_id, secret_id, version_no, ciphertext, nonce, key_id, fingerprint, created_by
		) VALUES ($1,$2,$3,1,E'\\x0102',E'\\x0304','key-1','fp-active',$4)
	`, activeVersionID, cutWorkspaceID, secretID, cutUserID); err != nil {
		t.Fatalf("active version: %v", err)
	}
	if withRevoked {
		if _, err := db.Exec(`
			INSERT INTO secret_versions (
				id, workspace_id, secret_id, version_no, ciphertext, nonce, key_id, fingerprint, created_by, revoked_at
			) VALUES ($1,$2,$3,2,E'\\x0506',E'\\x0708','key-1','fp-revoked',$4,clock_timestamp())
		`, revokedVersionID, cutWorkspaceID, secretID, cutUserID); err != nil {
			t.Fatalf("revoked version: %v", err)
		}
	}
	if _, err := db.Exec(`UPDATE secrets SET active_version_id=$1 WHERE id=$2`, activeVersionID, secretID); err != nil {
		t.Fatalf("activate version: %v", err)
	}
}

func seedHTTPConnection(t *testing.T, db *sql.DB, id, alias, secretID string, softDeleted bool, status string) {
	t.Helper()
	var secret any
	if secretID == "" {
		secret = nil
	} else {
		secret = secretID
	}
	var deleted any
	if softDeleted {
		deleted = "clock_timestamp()"
	}
	query := `
		INSERT INTO service_connections (
			id, workspace_id, provider_id, name, alias, environment,
			auth_mode, auth_config, credential_secret_id, status,
			created_by, updated_by, deleted_at
		) VALUES (
			$1,$2,$3,$4,$5,'PRODUCTION','API_KEY','{}'::jsonb,$6,$7,$8,$8,
			CASE WHEN $9 THEN clock_timestamp() ELSE NULL END
		)`
	if _, err := db.Exec(query, id, cutWorkspaceID, cutHTTPProviderID, alias, alias, secret, status, cutUserID, softDeleted); err != nil {
		t.Fatalf("http connection %s: %v", id, err)
	}
	_ = deleted
}

func seedNonTargetConnection(t *testing.T, db *sql.DB, id, alias, secretID string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO service_connections (
			id, workspace_id, provider_id, name, alias, environment,
			auth_mode, auth_config, credential_secret_id, status, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,'PRODUCTION','API_KEY','{}'::jsonb,$6,'VERIFIED',$7,$7)
	`, id, cutWorkspaceID, cutOtherProviderID, alias, alias, secretID, cutUserID); err != nil {
		t.Fatalf("non-target connection: %v", err)
	}
}

func seedModelConfig(t *testing.T, db *sql.DB, id, secretID string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO model_configs (
			id, workspace_id, name, provider, api_base, model_name,
			credential_secret_id, created_by, updated_by
		) VALUES ($1,$2,$3,'openai','https://api.openai.com','gpt-test',$4,$5,$5)
	`, id, cutWorkspaceID, "model-"+id[len(id)-4:], secretID, cutUserID); err != nil {
		t.Fatalf("model config: %v", err)
	}
}

func assertSecretGone(t *testing.T, db *sql.DB, secretID string) {
	t.Helper()
	var secrets, versions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM secrets WHERE id=$1`, secretID).Scan(&secrets); err != nil {
		t.Fatalf("count secrets: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM secret_versions WHERE secret_id=$1`, secretID).Scan(&versions); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if secrets != 0 || versions != 0 {
		t.Fatalf("secret %s still present secrets=%d versions=%d", secretID, secrets, versions)
	}
}

func assertSecretPresent(t *testing.T, db *sql.DB, secretID string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM secrets WHERE id=$1`, secretID).Scan(&count); err != nil {
		t.Fatalf("count secret: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected secret %s present, count=%d", secretID, count)
	}
}

func assertConnectionCutState(t *testing.T, db *sql.DB, id, status, migration string, credentialNil bool) {
	t.Helper()
	var gotStatus, gotMigration string
	var secret sql.NullString
	if err := db.QueryRow(`
		SELECT status, migration_state, credential_secret_id::text
		FROM service_connections WHERE id=$1
	`, id).Scan(&gotStatus, &gotMigration, &secret); err != nil {
		t.Fatalf("read connection %s: %v", id, err)
	}
	if gotStatus != status || gotMigration != migration {
		t.Fatalf("connection %s status=%s migration=%s want %s/%s", id, gotStatus, gotMigration, status, migration)
	}
	if credentialNil && secret.Valid {
		t.Fatalf("connection %s still has credential_secret_id=%s", id, secret.String)
	}
	if !credentialNil && !secret.Valid {
		t.Fatalf("connection %s lost credential unexpectedly", id)
	}
}

func assertConnectionUnchangedLegacy(t *testing.T, db *sql.DB, id, status, secretID string) {
	t.Helper()
	// migration_state column may not exist if migration failed before DDL... but DDL is first.
	// On failed data DO block after DDL, columns exist. For blocked preflight after schema add,
	// version stays dirty or rolled back depending on transaction.
	// Our migration is one transaction: on failure nothing applies including schema.
	var gotStatus string
	var secret sql.NullString
	if err := db.QueryRow(`
		SELECT status, credential_secret_id::text FROM service_connections WHERE id=$1
	`, id).Scan(&gotStatus, &secret); err != nil {
		t.Fatalf("read connection: %v", err)
	}
	if gotStatus != status {
		t.Fatalf("status mutated to %s", gotStatus)
	}
	if !secret.Valid || secret.String != secretID {
		t.Fatalf("credential mutated: %v want %s", secret, secretID)
	}
}

func assertNoHardCutAudit(t *testing.T, db *sql.DB, workspaceID string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM audit_events
		WHERE workspace_id=$1 AND action='outbound.identity.legacy_secret.deleted'
	`, workspaceID).Scan(&count); err != nil {
		// Table may exist at 59.
		if strings.Contains(err.Error(), "does not exist") {
			return
		}
		t.Fatalf("audit count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no hard-cut audit, got %d", count)
	}
}

// assertHardCutBlockedWithZeroMutation proves a failed 000060 attempt did not
// delete secrets, clear credentials, change connection status, or write audit.
// golang-migrate may leave schema_migrations dirty at version 60 even when the
// SQL transaction rolled back; data and business state must still be unchanged.
func assertHardCutBlockedWithZeroMutation(
	t *testing.T,
	db *sql.DB,
	secretID, connectionID, wantStatus, wantSecretID string,
) {
	t.Helper()
	var version int
	var dirty bool
	if err := db.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if version == 60 && !dirty {
		t.Fatalf("blocked cutover must not commit clean version 60")
	}
	assertSecretPresent(t, db, secretID)
	assertConnectionUnchangedLegacy(t, db, connectionID, wantStatus, wantSecretID)
	assertNoHardCutAudit(t, db, cutWorkspaceID)

	// If DDL partially applied before dirty mark (should not with PG transactional
	// migrations), migration_state must not be MIGRATION_REQUIRED.
	var migrationCol bool
	if err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name='service_connections' AND column_name='migration_state'
		)
	`).Scan(&migrationCol); err != nil {
		t.Fatalf("migration_state column probe: %v", err)
	}
	if migrationCol {
		var migrationState string
		if err := db.QueryRow(`SELECT migration_state FROM service_connections WHERE id=$1`, connectionID).
			Scan(&migrationState); err != nil {
			t.Fatalf("read migration_state: %v", err)
		}
		if migrationState == "MIGRATION_REQUIRED" {
			t.Fatalf("blocked cutover mutated migration_state")
		}
	}
}

func attemptMigrateTo(t *testing.T, testDatabase *dbtest.Database, version uint) error {
	t.Helper()
	ctx := context.Background()
	migrator, err := database.Open(ctx, testDatabase.DSN())
	if err != nil {
		t.Fatalf("open migrator: %v", err)
	}
	defer migrator.Close()
	return migrator.To(version)
}
