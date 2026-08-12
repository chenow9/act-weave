package database_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database"
	"actweave/backend/internal/database/dbtest"
	_ "github.com/lib/pq"
)

const (
	scOwnerID     = "618f1f2e-7b5a-7c3d-8e9f-a23456789001"
	scWorkspaceID = "618f1f2e-7b5a-7c3d-8e9f-a23456789002"
	scOtherWSID   = "618f1f2e-7b5a-7c3d-8e9f-a23456789003"
	scModelID     = "618f1f2e-7b5a-7c3d-8e9f-a23456789004"
	scOtherModel  = "618f1f2e-7b5a-7c3d-8e9f-a23456789005"
	scAgentID     = "618f1f2e-7b5a-7c3d-8e9f-a23456789006"
	scOtherAgent  = "618f1f2e-7b5a-7c3d-8e9f-a23456789007"
	scSessionID   = "618f1f2e-7b5a-7c3d-8e9f-a23456789008"
	scOtherSess   = "618f1f2e-7b5a-7c3d-8e9f-a23456789009"
	scRunID       = "618f1f2e-7b5a-7c3d-8e9f-a2345678900a"
	scOtherRunID  = "618f1f2e-7b5a-7c3d-8e9f-a2345678900b"
	scAssemblyID  = "618f1f2e-7b5a-7c3d-8e9f-a2345678900c"
	scAssembly2ID = "618f1f2e-7b5a-7c3d-8e9f-a2345678900d"
	scHashA       = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	scHashB       = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	scHashC       = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	scHashD       = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	scHashE       = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	scHashF       = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
)

func TestSessionContextContractsMigration(t *testing.T) {
	testDatabase := dbtest.New(t)
	dsn := testDatabase.DSN()

	// up to latest (000001 + 000002)
	applyMigrations(t, dsn, func(migrator *database.Migrator) {
		if err := migrator.Up(); err != nil {
			t.Fatalf("apply migrations up: %v", err)
		}
		assertMigrationVersion(t, migrator, 20) // includes later additive migrations
	})

	db := openSessionContextDB(t, dsn)
	insertSessionContextFixtures(t, db)
	assertSessionContextColumns(t, db)
	assertSessionContextDefaults(t, db)
	assertSessionContextJSONObjectConstraints(t, db)
	assertSessionContextAssemblyUniquenessAndIsolation(t, db)
	assertSessionContextAssemblyImmutable(t, db)
	assertAgentSnapshotImmutable(t, db)
	assertNoBodyColumnsOnAssembly(t, db)
	_ = db.Close()

	// Roll back all additive migrations (2..latest) to baseline 000001, then up again.
	// Latest is 20 → 19 down steps leaves version 1.
	applyMigrations(t, dsn, func(migrator *database.Migrator) {
		if err := migrator.Down(19); err != nil {
			t.Fatalf("roll back session context migration: %v", err)
		}
		assertMigrationVersion(t, migrator, 1)
	})
	assertSessionContextObjectsAbsent(t, dsn)

	applyMigrations(t, dsn, func(migrator *database.Migrator) {
		if err := migrator.Up(); err != nil {
			t.Fatalf("re-apply session context migration: %v", err)
		}
		assertMigrationVersion(t, migrator, 20)
	})
	db = openSessionContextDB(t, dsn)
	assertSessionContextColumns(t, db)
	_ = db.Close()
}

func openSessionContextDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Fatalf("ping db: %v", err)
	}
	return db
}

func insertSessionContextFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'sc.owner','SC Owner')`, scOwnerID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'sc-space','SC Space','PRODUCTION',$3,$3,$3),
		($2,'sc-other','SC Other','SANDBOX',$3,$3,$3)
	`, scWorkspaceID, scOtherWSID, scOwnerID); err != nil {
		t.Fatalf("insert workspaces: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES
		($1,$3,'SC Model','openai','https://models.example.test','sc-model',$5,$5),
		($2,$4,'SC Other Model','openai','https://models.example.test','sc-other-model',$5,$5)
	`, scModelID, scOtherModel, scWorkspaceID, scOtherWSID, scOwnerID); err != nil {
		t.Fatalf("insert model configs: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES
		($1,$3,'SC Agent',$5,$7,$7),
		($2,$4,'SC Other Agent',$6,$7,$7)
	`, scAgentID, scOtherAgent, scWorkspaceID, scOtherWSID, scModelID, scOtherModel, scOwnerID); err != nil {
		t.Fatalf("insert agents: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by) VALUES
		($1,$3,$5,'SC session',$7),
		($2,$4,$6,'SC other session',$7)
	`, scSessionID, scOtherSess, scWorkspaceID, scOtherWSID, scAgentID, scOtherAgent, scOwnerID); err != nil {
		t.Fatalf("insert sessions: %v", err)
	}
	for _, row := range []struct {
		runID, workspaceID, sessionID, agentID string
	}{
		{scRunID, scWorkspaceID, scSessionID, scAgentID},
		{scOtherRunID, scOtherWSID, scOtherSess, scOtherAgent},
	} {
		if _, err := db.Exec(`
			INSERT INTO agent_runs(
				id,workspace_id,session_id,agent_id,status,trigger_type,
				triggered_by_type,triggered_by_id,trace_id,
				model_snapshot,capability_snapshot,context_policy_snapshot
			) VALUES (
				$1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'trace-sc',
				'{}'::jsonb,'{}'::jsonb,'{}'::jsonb
			)
		`, row.runID, row.workspaceID, row.sessionID, row.agentID, scOwnerID); err != nil {
			t.Fatalf("insert agent run %s: %v", row.runID, err)
		}
	}
}

func assertSessionContextColumns(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, item := range []struct {
		table, column string
	}{
		{"model_configs", "runtime_capabilities"},
		{"workspaces", "context_policy"},
		{"agents", "context_policy"},
		{"agent_runs", "agent_snapshot"},
	} {
		var dataType, udtName, nullable, columnDefault string
		if err := db.QueryRow(`
			SELECT data_type, udt_name, is_nullable, COALESCE(column_default, '')
			FROM information_schema.columns
			WHERE table_schema='public' AND table_name=$1 AND column_name=$2
		`, item.table, item.column).Scan(&dataType, &udtName, &nullable, &columnDefault); err != nil {
			t.Fatalf("query %s.%s: %v", item.table, item.column, err)
		}
		if dataType != "jsonb" || udtName != "jsonb" || nullable != "NO" {
			t.Fatalf("%s.%s type=%q udt=%q nullable=%q", item.table, item.column, dataType, udtName, nullable)
		}
		if !strings.Contains(columnDefault, "'{}'") && !strings.Contains(columnDefault, "'{}'::jsonb") {
			// PostgreSQL may normalize default expression; accept jsonb '{}'
			if !strings.Contains(strings.ToLower(columnDefault), "jsonb") || !strings.Contains(columnDefault, "{}") {
				t.Fatalf("%s.%s unexpected default %q", item.table, item.column, columnDefault)
			}
		}
	}

	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('public.agent_run_context_assemblies') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("query assemblies table: %v", err)
	}
	if !exists {
		t.Fatal("expected agent_run_context_assemblies table")
	}
}

func assertSessionContextDefaults(t *testing.T, db *sql.DB) {
	t.Helper()
	var modelCaps, workspacePolicy, agentPolicy, agentSnapshot string
	if err := db.QueryRow(`SELECT runtime_capabilities::text FROM model_configs WHERE id=$1`, scModelID).Scan(&modelCaps); err != nil {
		t.Fatalf("read model runtime_capabilities default: %v", err)
	}
	if err := db.QueryRow(`SELECT context_policy::text FROM workspaces WHERE id=$1`, scWorkspaceID).Scan(&workspacePolicy); err != nil {
		t.Fatalf("read workspace context_policy default: %v", err)
	}
	if err := db.QueryRow(`SELECT context_policy::text FROM agents WHERE id=$1`, scAgentID).Scan(&agentPolicy); err != nil {
		t.Fatalf("read agent context_policy default: %v", err)
	}
	if err := db.QueryRow(`SELECT agent_snapshot::text FROM agent_runs WHERE id=$1`, scRunID).Scan(&agentSnapshot); err != nil {
		t.Fatalf("read agent_snapshot default: %v", err)
	}
	for name, value := range map[string]string{
		"model runtime_capabilities": modelCaps,
		"workspace context_policy":   workspacePolicy,
		"agent context_policy":       agentPolicy,
		"agent_snapshot":             agentSnapshot,
	} {
		if value != "{}" {
			t.Fatalf("expected empty object default for %s, got %q", name, value)
		}
	}
}

func assertSessionContextJSONObjectConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	assertStatementFails(t, db, `UPDATE model_configs SET runtime_capabilities='[]'::jsonb WHERE id=$1`, scModelID)
	assertStatementFails(t, db, `UPDATE workspaces SET context_policy='"x"'::jsonb WHERE id=$1`, scWorkspaceID)
	assertStatementFails(t, db, `UPDATE agents SET context_policy='1'::jsonb WHERE id=$1`, scAgentID)
	assertStatementFails(t, db, `UPDATE agent_runs SET agent_snapshot='[]'::jsonb WHERE id=$1`, scRunID)
	// valid object writes are allowed on config columns (not assembly)
	if _, err := db.Exec(`UPDATE model_configs SET runtime_capabilities='{"schemaVersion":"model-runtime.v1"}'::jsonb WHERE id=$1`, scModelID); err != nil {
		t.Fatalf("valid runtime_capabilities update: %v", err)
	}
	if _, err := db.Exec(`UPDATE model_configs SET runtime_capabilities='{}'::jsonb WHERE id=$1`, scModelID); err != nil {
		t.Fatalf("reset runtime_capabilities: %v", err)
	}
}

func assertSessionContextAssemblyUniquenessAndIsolation(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO agent_run_context_assemblies(
			id,workspace_id,run_id,session_id,mode,
			policy_snapshot_hash,model_snapshot_hash,capability_snapshot_hash,agent_snapshot_hash,
			estimator_profile,estimator_version,
			hard_input_ceiling_tokens,output_reserve_tokens,safety_margin_tokens,tools_overhead_tokens,
			system_prompt_hash,included_segments,omitted_prefix_count,
			assembly_digest,estimated_total_tokens
		) VALUES (
			$1,$2,$3,$4,'token_window',
			$5,$5,$5,$5,
			'o200k_base','2026-01',
			100000,4096,2048,512,
			$6,'[]'::jsonb,0,
			$7,12000
		)
	`, scAssemblyID, scWorkspaceID, scRunID, scSessionID, scHashA, scHashB, scHashC); err != nil {
		t.Fatalf("insert assembly: %v", err)
	}

	// same (workspace_id, run_id) rejected
	assertStatementFails(t, db, `
		INSERT INTO agent_run_context_assemblies(
			id,workspace_id,run_id,mode,
			policy_snapshot_hash,model_snapshot_hash,capability_snapshot_hash,agent_snapshot_hash,
			estimator_profile,estimator_version,
			hard_input_ceiling_tokens,output_reserve_tokens,safety_margin_tokens,tools_overhead_tokens,
			system_prompt_hash,assembly_digest,estimated_total_tokens
		) VALUES (
			$1,$2,$3,'token_window',
			$4,$4,$4,$4,
			'o200k_base','2026-01',
			1,1,1,1,
			$4,$4,1
		)
	`, scAssembly2ID, scWorkspaceID, scRunID, scHashD)

	// cross-workspace run reference rejected (composite FK)
	assertStatementFails(t, db, `
		INSERT INTO agent_run_context_assemblies(
			id,workspace_id,run_id,mode,
			policy_snapshot_hash,model_snapshot_hash,capability_snapshot_hash,agent_snapshot_hash,
			estimator_profile,estimator_version,
			hard_input_ceiling_tokens,output_reserve_tokens,safety_margin_tokens,tools_overhead_tokens,
			system_prompt_hash,assembly_digest,estimated_total_tokens
		) VALUES (
			$1,$2,$3,'token_window',
			$4,$4,$4,$4,
			'o200k_base','2026-01',
			1,1,1,1,
			$4,$4,1
		)
	`, scAssembly2ID, scWorkspaceID, scOtherRunID, scHashE)

	// other workspace can insert its own run assembly
	if _, err := db.Exec(`
		INSERT INTO agent_run_context_assemblies(
			id,workspace_id,run_id,session_id,mode,
			policy_snapshot_hash,model_snapshot_hash,capability_snapshot_hash,agent_snapshot_hash,
			estimator_profile,estimator_version,
			hard_input_ceiling_tokens,output_reserve_tokens,safety_margin_tokens,tools_overhead_tokens,
			system_prompt_hash,assembly_digest,estimated_total_tokens
		) VALUES (
			$1,$2,$3,$4,'token_window',
			$5,$5,$5,$5,
			'o200k_base','2026-01',
			1,1,1,1,
			$5,$5,1
		)
	`, scAssembly2ID, scOtherWSID, scOtherRunID, scOtherSess, scHashF); err != nil {
		t.Fatalf("insert other-workspace assembly: %v", err)
	}

	// array/object checks
	assertStatementFails(t, db, `
		INSERT INTO agent_run_context_assemblies(
			id,workspace_id,run_id,mode,
			policy_snapshot_hash,model_snapshot_hash,capability_snapshot_hash,agent_snapshot_hash,
			estimator_profile,estimator_version,
			hard_input_ceiling_tokens,output_reserve_tokens,safety_margin_tokens,tools_overhead_tokens,
			system_prompt_hash,included_segments,assembly_digest,estimated_total_tokens
		) VALUES (
			'618f1f2e-7b5a-7c3d-8e9f-a2345678900e',$1,$2,'token_window',
			$3,$3,$3,$3,
			'o200k_base','2026-01',
			1,1,1,1,
			$3,'{}'::jsonb,$3,1
		)
	`, scWorkspaceID, scRunID, scHashA)
}

func assertSessionContextAssemblyImmutable(t *testing.T, db *sql.DB) {
	t.Helper()
	assertStatementFails(t, db, `
		UPDATE agent_run_context_assemblies SET mode='rolling_summary' WHERE id=$1
	`, scAssemblyID)
	assertStatementFails(t, db, `
		DELETE FROM agent_run_context_assemblies WHERE id=$1
	`, scAssemblyID)
}

func assertAgentSnapshotImmutable(t *testing.T, db *sql.DB) {
	t.Helper()
	// Bump lock_version so failure is caused by agent_snapshot immutability,
	// not the lock_version CAS guard.
	assertStatementFails(t, db, `
		UPDATE agent_runs
		SET agent_snapshot='{"schemaVersion":"run.v2"}'::jsonb,
		    lock_version = lock_version + 1
		WHERE id=$1
	`, scRunID)
}

func assertNoBodyColumnsOnAssembly(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, forbidden := range []string{
		"content", "body", "prompt", "system_prompt", "message_body",
		"summary_text", "summary_body", "raw_prompt", "provider_body",
	} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema='public'
				  AND table_name='agent_run_context_assemblies'
				  AND column_name=$1
			)
		`, forbidden).Scan(&exists); err != nil {
			t.Fatalf("query forbidden column %s: %v", forbidden, err)
		}
		if exists {
			t.Fatalf("agent_run_context_assemblies must not store body column %q", forbidden)
		}
	}
}

func assertSessionContextObjectsAbsent(t *testing.T, dsn string) {
	t.Helper()
	db := openSessionContextDB(t, dsn)
	defer db.Close()
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('public.agent_run_context_assemblies') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("query rolled-back assemblies: %v", err)
	}
	if exists {
		t.Fatal("expected agent_run_context_assemblies to be dropped after down")
	}
	for _, item := range []struct{ table, column string }{
		{"model_configs", "runtime_capabilities"},
		{"workspaces", "context_policy"},
		{"agents", "context_policy"},
		{"agent_runs", "agent_snapshot"},
	} {
		var colExists bool
		if err := db.QueryRow(`
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema='public' AND table_name=$1 AND column_name=$2
			)
		`, item.table, item.column).Scan(&colExists); err != nil {
			t.Fatalf("query rolled-back column %s.%s: %v", item.table, item.column, err)
		}
		if colExists {
			t.Fatalf("expected %s.%s dropped after down", item.table, item.column)
		}
	}
}

func assertStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected statement to fail: %s", strings.TrimSpace(query))
	}
}
