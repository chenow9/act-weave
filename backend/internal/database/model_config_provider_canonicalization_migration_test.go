package database_test

import (
	"database/sql"
	"testing"
	"time"

	"actweave/backend/internal/database"
	"actweave/backend/internal/database/dbtest"
	_ "github.com/lib/pq"
)

// Fixture identity for the provider canonicalization migration (000020).
const (
	pcOwnerID     = "a18f1f2e-7b5a-7c3d-8e9f-b23456789001"
	pcWorkspaceID = "a18f1f2e-7b5a-7c3d-8e9f-b23456789002"
)

// pcLegacyCaps is a capability document shaped like a real verified one: its
// verifiedConfigDigest is bound to the legacy provider spelling, which is exactly
// the staleness the migration must destroy when the provider text changes.
const pcLegacyCaps = `{"schemaVersion":"agentic-model.v1",` +
	`"protocol":"openai-responses-v1","streaming":true,"usage":true,` +
	`"toolSearchModes":["client"],"verifiedAdapter":"agenticopenai/v0.2.2",` +
	`"verifiedLockVersion":3,` +
	`"verifiedConfigDigest":"1111111111111111111111111111111111111111111111111111111111111111",` +
	`"verifiedAt":"2026-01-01T00:00:00Z"}`

type pcRow struct {
	id string
	// seeded pre-migration state
	provider string
	status   string
	caps     string
	verified bool
	latency  sql.NullInt64
	code     sql.NullString
	lock     int64
	deleted  bool
	// expectation
	wantProvider string
	wantStatus   string
	wantChanged  bool
}

type pcState struct {
	provider  string
	status    string
	caps      string
	verified  sql.NullTime
	latency   sql.NullInt64
	code      sql.NullString
	lock      int64
	updatedAt time.Time
}

// TestModelConfigProviderCanonicalizationMigration covers R11-2's data migration:
// every legacy provider spelling in the closed alias set is canonicalized;
// changed rows lose their stale agentic capability document and verification
// evidence and are forced back to re-verification; already-canonical and
// unknown-provider rows are left untouched; and the down migration restores the
// exact pre-migration state except for rows edited after the migration, which its
// compare-and-swap guard must skip.
func TestModelConfigProviderCanonicalizationMigration(t *testing.T) {
	testDatabase := dbtest.New(t)
	dsn := testDatabase.DSN()

	// Stop at 000019: model_configs still holds legacy provider spellings and the
	// agentic_capabilities column already exists.
	applyMigrations(t, dsn, func(migrator *database.Migrator) {
		if err := migrator.To(19); err != nil {
			t.Fatalf("to 19: %v", err)
		}
		assertMigrationVersion(t, migrator, 19)
	})

	rows := []pcRow{
		{
			id: "a18f1f2e-7b5a-7c3d-8e9f-b23456789101", provider: "OPENAI_COMPATIBLE",
			status: "VERIFIED", caps: pcLegacyCaps, verified: true,
			latency: sql.NullInt64{Int64: 12, Valid: true}, lock: 3,
			wantProvider: "openai-compatible", wantStatus: "UNVERIFIED", wantChanged: true,
		},
		{
			id: "a18f1f2e-7b5a-7c3d-8e9f-b23456789102", provider: "OpenAI Compatible",
			status: "VERIFIED", caps: pcLegacyCaps, verified: true,
			latency: sql.NullInt64{Int64: 34, Valid: true}, lock: 7,
			wantProvider: "openai-compatible", wantStatus: "UNVERIFIED", wantChanged: true,
		},
		{
			id: "a18f1f2e-7b5a-7c3d-8e9f-b23456789103", provider: "openai_compatible",
			status: "UNVERIFIED", caps: "{}", lock: 1,
			wantProvider: "openai-compatible", wantStatus: "UNVERIFIED", wantChanged: true,
		},
		{
			// DISABLED is an operator kill switch: canonicalized but never re-opened.
			id: "a18f1f2e-7b5a-7c3d-8e9f-b23456789104", provider: "OPENAI-COMPATIBLE",
			status: "DISABLED", caps: "{}", lock: 2,
			wantProvider: "openai-compatible", wantStatus: "DISABLED", wantChanged: true,
		},
		{
			id: "a18f1f2e-7b5a-7c3d-8e9f-b23456789105", provider: "OPENAI",
			status: "ERROR", caps: "{}", verified: true,
			latency: sql.NullInt64{Int64: 20, Valid: true},
			code:    sql.NullString{String: "MODEL_CONFIG_NETWORK_ERROR", Valid: true}, lock: 5,
			wantProvider: "openai", wantStatus: "UNVERIFIED", wantChanged: true,
		},
		{
			id: "a18f1f2e-7b5a-7c3d-8e9f-b23456789106", provider: "OpenAI",
			status: "UNVERIFIED", caps: "{}", lock: 1,
			wantProvider: "openai", wantStatus: "UNVERIFIED", wantChanged: true,
		},
		{
			// Padded values are rejected by the agentic provider check (which
			// demands a value identical to its trimmed form), so they are not
			// canonical either.
			id: "a18f1f2e-7b5a-7c3d-8e9f-b23456789107", provider: "  openai-compatible  ",
			status: "UNVERIFIED", caps: "{}", lock: 4,
			wantProvider: "openai-compatible", wantStatus: "UNVERIFIED", wantChanged: true,
		},
		{
			// Already canonical: keeps its verified capability document untouched.
			id: "a18f1f2e-7b5a-7c3d-8e9f-b23456789108", provider: "openai",
			status: "VERIFIED", caps: pcLegacyCaps, verified: true,
			latency: sql.NullInt64{Int64: 8, Valid: true}, lock: 6,
			wantProvider: "openai", wantStatus: "VERIFIED", wantChanged: false,
		},
		{
			// Outside the closed alias set: never guessed at, left exactly as-is.
			id: "a18f1f2e-7b5a-7c3d-8e9f-b23456789109", provider: "anthropic",
			status: "VERIFIED", caps: pcLegacyCaps, verified: true,
			latency: sql.NullInt64{Int64: 9, Valid: true}, lock: 2,
			wantProvider: "anthropic", wantStatus: "VERIFIED", wantChanged: false,
		},
		{
			// Soft-deleted rows are canonicalized too, so a legacy spelling can
			// never come back into the column.
			id: "a18f1f2e-7b5a-7c3d-8e9f-b23456789110", provider: "OPENAI_COMPATIBLE",
			status: "UNVERIFIED", caps: "{}", lock: 1, deleted: true,
			wantProvider: "openai-compatible", wantStatus: "UNVERIFIED", wantChanged: true,
		},
	}

	db := openACDB(t, dsn)
	seedProviderCanonicalizationFixtures(t, db, rows)
	before := map[string]pcState{}
	for _, row := range rows {
		before[row.id] = readModelConfigState(t, db, row.id)
	}
	_ = db.Close()

	applyMigrations(t, dsn, func(migrator *database.Migrator) {
		if err := migrator.To(20); err != nil {
			t.Fatalf("to 20: %v", err)
		}
		assertMigrationVersion(t, migrator, 20)
	})

	db = openACDB(t, dsn)
	for _, row := range rows {
		got := readModelConfigState(t, db, row.id)
		pre := before[row.id]
		if got.provider != row.wantProvider {
			t.Fatalf("%s provider: got %q want %q", row.id, got.provider, row.wantProvider)
		}
		if got.status != row.wantStatus {
			t.Fatalf("%s status: got %q want %q", row.id, got.status, row.wantStatus)
		}
		if !row.wantChanged {
			// Untouched rows keep evidence, capabilities, lock, and updated_at.
			if got.caps != pre.caps || got.lock != pre.lock ||
				got.verified != pre.verified || got.latency != pre.latency || got.code != pre.code ||
				!got.updatedAt.Equal(pre.updatedAt) {
				t.Fatalf("%s must be untouched: pre=%+v got=%+v", row.id, pre, got)
			}
			continue
		}
		// Changed provider ⇒ new WireConfigDigest ⇒ the stored capability
		// document and all verification evidence are stale and must be gone.
		if got.caps != "{}" {
			t.Fatalf("%s stale agentic capabilities survived: %s", row.id, got.caps)
		}
		if got.verified.Valid || got.latency.Valid || got.code.Valid {
			t.Fatalf("%s stale verification evidence survived: %+v", row.id, got)
		}
		if got.lock != pre.lock+1 {
			t.Fatalf("%s lock_version: got %d want %d (stale CAS snapshots must not apply)",
				row.id, got.lock, pre.lock+1)
		}
		if !got.updatedAt.After(pre.updatedAt) {
			t.Fatalf("%s updated_at must advance: pre=%v got=%v", row.id, pre.updatedAt, got.updatedAt)
		}
	}

	// No canonicalized row may still carry a verifiedConfigDigest anywhere.
	var staleDigests int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM model_configs
		WHERE provider IN ('openai', 'openai-compatible')
		  AND agentic_capabilities ? 'verifiedConfigDigest'
		  AND id IN (SELECT model_config_id FROM model_config_provider_canonicalizations)
	`).Scan(&staleDigests); err != nil {
		t.Fatal(err)
	}
	if staleDigests != 0 {
		t.Fatalf("canonicalized rows still carry verifiedConfigDigest: %d", staleDigests)
	}

	// The recording table holds exactly the rewritten rows with their pre-state.
	var recorded int
	if err := db.QueryRow(`SELECT COUNT(*) FROM model_config_provider_canonicalizations`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	wantRecorded := 0
	for _, row := range rows {
		if row.wantChanged {
			wantRecorded++
		}
	}
	if recorded != wantRecorded {
		t.Fatalf("recorded canonicalizations: got %d want %d", recorded, wantRecorded)
	}
	for _, row := range rows {
		if !row.wantChanged {
			var present int
			if err := db.QueryRow(`
				SELECT COUNT(*) FROM model_config_provider_canonicalizations WHERE model_config_id=$1
			`, row.id).Scan(&present); err != nil {
				t.Fatal(err)
			}
			if present != 0 {
				t.Fatalf("%s must not be recorded as canonicalized", row.id)
			}
			continue
		}
		var legacyProvider, canonicalProvider, legacyStatus string
		var legacyLock, canonicalLock int64
		if err := db.QueryRow(`
			SELECT legacy_provider, canonical_provider, legacy_status, legacy_lock_version, canonical_lock_version
			FROM model_config_provider_canonicalizations WHERE model_config_id=$1
		`, row.id).Scan(&legacyProvider, &canonicalProvider, &legacyStatus, &legacyLock, &canonicalLock); err != nil {
			t.Fatalf("read recording for %s: %v", row.id, err)
		}
		pre := before[row.id]
		if legacyProvider != pre.provider || canonicalProvider != row.wantProvider ||
			legacyStatus != pre.status || legacyLock != pre.lock || canonicalLock != pre.lock+1 {
			t.Fatalf("%s recording mismatch: legacy=%q canonical=%q status=%q locks=%d/%d pre=%+v",
				row.id, legacyProvider, canonicalProvider, legacyStatus, legacyLock, canonicalLock, pre)
		}
	}
	_ = db.Close()

	// Down restores the exact pre-migration state and removes the recording table.
	applyMigrations(t, dsn, func(migrator *database.Migrator) {
		if err := migrator.Down(1); err != nil {
			t.Fatalf("down 1: %v", err)
		}
		assertMigrationVersion(t, migrator, 19)
	})

	db = openACDB(t, dsn)
	for _, row := range rows {
		got := readModelConfigState(t, db, row.id)
		pre := before[row.id]
		if got.provider != pre.provider || got.status != pre.status || got.caps != pre.caps ||
			got.verified != pre.verified || got.latency != pre.latency || got.code != pre.code ||
			got.lock != pre.lock || !got.updatedAt.Equal(pre.updatedAt) {
			t.Fatalf("%s down did not restore pre-migration state: pre=%+v got=%+v", row.id, pre, got)
		}
	}
	var recordingTableExists bool
	if err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema='public' AND table_name='model_config_provider_canonicalizations'
		)
	`).Scan(&recordingTableExists); err != nil {
		t.Fatal(err)
	}
	if recordingTableExists {
		t.Fatal("down must drop model_config_provider_canonicalizations")
	}
	_ = db.Close()

	// Re-apply: the migration is repeatable after a rollback.
	applyMigrations(t, dsn, func(migrator *database.Migrator) {
		if err := migrator.To(20); err != nil {
			t.Fatalf("re-up to 20: %v", err)
		}
		assertMigrationVersion(t, migrator, 20)
	})

	// Compare-and-swap guard: a row edited after the migration keeps the newer
	// state on rollback instead of being clobbered by the legacy snapshot.
	edited := rows[0].id
	db = openACDB(t, dsn)
	if _, err := db.Exec(`
		UPDATE model_configs
		SET model_name = 'edited-after-migration',
		    lock_version = lock_version + 1,
		    updated_at = clock_timestamp()
		WHERE id = $1
	`, edited); err != nil {
		t.Fatal(err)
	}
	afterEdit := readModelConfigState(t, db, edited)
	_ = db.Close()

	applyMigrations(t, dsn, func(migrator *database.Migrator) {
		if err := migrator.Down(1); err != nil {
			t.Fatalf("down 1 after edit: %v", err)
		}
		assertMigrationVersion(t, migrator, 19)
	})

	db = openACDB(t, dsn)
	t.Cleanup(func() { _ = db.Close() })
	skipped := readModelConfigState(t, db, edited)
	if skipped.provider != afterEdit.provider || skipped.lock != afterEdit.lock ||
		skipped.status != afterEdit.status || skipped.caps != afterEdit.caps ||
		!skipped.updatedAt.Equal(afterEdit.updatedAt) {
		t.Fatalf("down clobbered a row edited after the migration: after edit=%+v got=%+v",
			afterEdit, skipped)
	}
	// Rows untouched since the migration are still restored in the same run.
	for _, row := range rows[1:] {
		if !row.wantChanged {
			continue
		}
		got := readModelConfigState(t, db, row.id)
		pre := before[row.id]
		if got.provider != pre.provider || got.status != pre.status || got.caps != pre.caps ||
			got.lock != pre.lock {
			t.Fatalf("%s must still be restored while a sibling row is skipped: pre=%+v got=%+v",
				row.id, pre, got)
		}
	}
}

func seedProviderCanonicalizationFixtures(t *testing.T, db *sql.DB, rows []pcRow) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO users(id,username,display_name) VALUES($1,'provider.canon.owner','PC')`,
		pcOwnerID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'provider-canon-space','PC','SANDBOX',$2,$2,$2)
	`, pcWorkspaceID, pcOwnerID); err != nil {
		t.Fatal(err)
	}
	for index, row := range rows {
		var latency any
		if row.latency.Valid {
			latency = row.latency.Int64
		}
		var code any
		if row.code.Valid {
			code = row.code.String
		}
		// last_verified_at / deleted_at must be >= created_at (DB checks), so both
		// come from the database clock rather than an invented timestamp.
		if _, err := db.Exec(`
			INSERT INTO model_configs(
				id,workspace_id,name,provider,api_base,model_name,
				status,agentic_capabilities,last_verified_at,last_latency_ms,last_error_code,
				created_by,updated_by,lock_version,deleted_at
			) VALUES (
				$1,$2,$3,$4,'https://models.example','m',
				$5,$6::jsonb,
				CASE WHEN $7::bool THEN clock_timestamp() ELSE NULL END,
				$8,$9,$10,$10,$11,
				CASE WHEN $12::bool THEN clock_timestamp() ELSE NULL END
			)
		`,
			row.id, pcWorkspaceID, "pc-model-"+string(rune('a'+index)), row.provider,
			row.status, row.caps, row.verified, latency, code, pcOwnerID, row.lock, row.deleted,
		); err != nil {
			t.Fatalf("seed %s (provider %q): %v", row.id, row.provider, err)
		}
	}
}

func readModelConfigState(t *testing.T, db *sql.DB, id string) pcState {
	t.Helper()
	var state pcState
	if err := db.QueryRow(`
		SELECT provider, status, agentic_capabilities::text, last_verified_at,
		       last_latency_ms, last_error_code, lock_version, updated_at
		FROM model_configs WHERE id=$1
	`, id).Scan(&state.provider, &state.status, &state.caps, &state.verified,
		&state.latency, &state.code, &state.lock, &state.updatedAt); err != nil {
		t.Fatalf("read model config %s: %v", id, err)
	}
	return state
}
