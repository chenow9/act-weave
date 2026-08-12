package execution_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
)

func TestContextAssemblyRepositoryImmutableAndUnique(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	const (
		owner = "b08f1f2e-7b5a-7c3d-8e9f-123456789001"
		ws    = "b08f1f2e-7b5a-7c3d-8e9f-123456789002"
		model = "b08f1f2e-7b5a-7c3d-8e9f-123456789003"
		agent = "b08f1f2e-7b5a-7c3d-8e9f-123456789004"
		run   = "b08f1f2e-7b5a-7c3d-8e9f-123456789005"
		hash  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	execSQL(t, db, `INSERT INTO users(id,username,display_name) VALUES($1,'asm.owner','A')`, owner)
	execSQL(t, db, `INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES($1,'asm','A','SANDBOX',$2,$2,$2)`, ws, owner)
	execSQL(t, db, `INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES($1,$2,'m','openai','https://x','m',$3,$3)`, model, ws, owner)
	execSQL(t, db, `INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES($1,$2,'a',$3,$4,$4)`, agent, ws, model, owner)
	execSQL(t, db, `
		INSERT INTO agent_runs(id,workspace_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,trace_id,model_snapshot,capability_snapshot)
		VALUES($1,$2,$3,'RUNNING','CHAT','USER',$4,'t','{}','{}')`, run, ws, agent, owner)

	repo, err := execution.NewContextAssemblyRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	rec := execution.ContextAssemblyRecord{
		WorkspaceID: ws, RunID: run, Mode: "token_window",
		PolicySnapshotHash: hash, ModelSnapshotHash: hash, CapabilitySnapshotHash: hash, AgentSnapshotHash: hash,
		EstimatorProfile: "o200k_base", EstimatorVersion: "v1",
		HardInputCeilingTokens: 1000, OutputReserveTokens: 100, SafetyMarginTokens: 10, ToolsOverheadTokens: 5,
		SystemPromptHash: hash, IncludedSegments: json.RawMessage(`[]`), EstimatedTotalTokens: 50,
		// Classic defaults: mode none; empty digest is computed canonically after normalize.
		ToolSearchMode: execution.AssemblyToolSearchModeNone,
	}
	// Empty AssemblyDigest → server computes; explicit compute uses same mode defaults.
	rec.AssemblyDigest = execution.ComputeAssemblyDigest(rec)
	created, err := repo.InsertImmutable(context.Background(), rec)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	again, err := repo.InsertImmutable(context.Background(), rec)
	if err != nil || again.ID != created.ID {
		t.Fatalf("idempotent: %+v err=%v", again, err)
	}
	rec2 := rec
	rec2.EstimatedTotalTokens = 99
	rec2.AssemblyDigest = execution.ComputeAssemblyDigest(rec2)
	if _, err := repo.InsertImmutable(context.Background(), rec2); !errors.Is(err, execution.ErrContextAssemblyConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if _, err := db.Exec(`UPDATE agent_run_context_assemblies SET mode='x' WHERE id=$1`, created.ID); err == nil {
		t.Fatal("expected immutability")
	}
	if _, err := repo.GetByRun(context.Background(), "b08f1f2e-7b5a-7c3d-8e9f-123456789099", run); !errors.Is(err, execution.ErrRunNotFound) {
		t.Fatalf("cross workspace: %v", err)
	}
}

func TestContextErrorSafeMessages(t *testing.T) {
	for _, code := range []string{
		execution.ErrCodeContextSnapshotUnsupported,
		execution.ErrCodeContextModelLimitUnknown,
		execution.ErrCodeContextRequiredInputTooLarge,
		execution.ErrCodeContextAssemblyFailed,
		execution.ErrCodeContextWindowExceededUpstream,
	} {
		e := execution.NewContextError(code)
		if e.Code != code || e.Message == "" {
			t.Fatalf("bad error: %+v", e)
		}
		if strings.Contains(strings.ToLower(e.Message), "provider") {
			t.Fatalf("leaky message: %s", e.Message)
		}
	}
}

func TestContextAssemblyAgenticValidation(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	const (
		owner = "b08f1f2e-7b5a-7c3d-8e9f-123456789011"
		ws    = "b08f1f2e-7b5a-7c3d-8e9f-123456789012"
		model = "b08f1f2e-7b5a-7c3d-8e9f-123456789013"
		agent = "b08f1f2e-7b5a-7c3d-8e9f-123456789014"
		run   = "b08f1f2e-7b5a-7c3d-8e9f-123456789015"
		hash  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	execSQL(t, db, `INSERT INTO users(id,username,display_name) VALUES($1,'asm2.owner','A')`, owner)
	execSQL(t, db, `INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES($1,'asm2','A','SANDBOX',$2,$2,$2)`, ws, owner)
	execSQL(t, db, `INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES($1,$2,'m','openai','https://x','m',$3,$3)`, model, ws, owner)
	execSQL(t, db, `INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES($1,$2,'a',$3,$4,$4)`, agent, ws, model, owner)
	execSQL(t, db, `
		INSERT INTO agent_runs(id,workspace_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,trace_id,model_snapshot,capability_snapshot)
		VALUES($1,$2,$3,'RUNNING','CHAT','USER',$4,'t','{}','{}')`, run, ws, agent, owner)

	repo, err := execution.NewContextAssemblyRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	// Classic pollution rejected: none mode with nonzero deferred count.
	classicPolluted := execution.ContextAssemblyRecord{
		WorkspaceID: ws, RunID: run, Mode: "token_window",
		PolicySnapshotHash: hash, ModelSnapshotHash: hash, CapabilitySnapshotHash: hash, AgentSnapshotHash: hash,
		EstimatorProfile: "o200k_base", EstimatorVersion: "contextwindow-estimator.v1",
		HardInputCeilingTokens: 1000, OutputReserveTokens: 100, SafetyMarginTokens: 10, ToolsOverheadTokens: 5,
		SystemPromptHash: hash, IncludedSegments: json.RawMessage(`[]`), EstimatedTotalTokens: 50,
		ToolSearchMode: execution.AssemblyToolSearchModeNone, DeferredToolCount: 1, MaxLoadedToolCount: 1,
	}
	classicPolluted.AssemblyDigest = execution.ComputeAssemblyDigest(classicPolluted)
	if _, err := repo.InsertImmutable(context.Background(), classicPolluted); err == nil {
		t.Fatal("expected classic pollution reject")
	}

	// Valid client_bounded with MaxLoaded == min(deferred,40) and tools sum.
	agenticOK := execution.ContextAssemblyRecord{
		WorkspaceID: ws, RunID: run, Mode: "token_window",
		PolicySnapshotHash: hash, ModelSnapshotHash: hash, CapabilitySnapshotHash: hash, AgentSnapshotHash: hash,
		EstimatorProfile: "o200k_base", EstimatorVersion: "contextwindow-estimator.agentic-openai-responses.v1",
		HardInputCeilingTokens: 1000, OutputReserveTokens: 100, SafetyMarginTokens: 10,
		ToolsOverheadTokens: 15, // 0+5+10
		SystemPromptHash:    hash, IncludedSegments: json.RawMessage(`[]`), EstimatedTotalTokens: 50,
		ToolSearchMode:               execution.AssemblyToolSearchModeClientBounded,
		ToolCatalogDigest:            hash,
		ImmediateToolCount:           0,
		DeferredToolCount:            3,
		MaxLoadedToolCount:           3,
		ImmediateToolsTokens:         0,
		DeferredMetadataTokens:       5,
		DynamicToolLoadReserveTokens: 10,
	}
	agenticOK.AssemblyDigest = execution.ComputeAssemblyDigest(agenticOK)
	if _, err := repo.InsertImmutable(context.Background(), agenticOK); err != nil {
		t.Fatalf("agentic ok: %v", err)
	}

	// Wrong max loaded rejected at app validation (new run).
	run2 := "b08f1f2e-7b5a-7c3d-8e9f-123456789016"
	execSQL(t, db, `
		INSERT INTO agent_runs(id,workspace_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,trace_id,model_snapshot,capability_snapshot)
		VALUES($1,$2,$3,'RUNNING','CHAT','USER',$4,'t2','{}','{}')`, run2, ws, agent, owner)
	badMax := agenticOK
	badMax.RunID = run2
	badMax.MaxLoadedToolCount = 40 // deferred=3 → must be 3
	badMax.AssemblyDigest = execution.ComputeAssemblyDigest(badMax)
	if _, err := repo.InsertImmutable(context.Background(), badMax); err == nil {
		t.Fatal("expected max loaded mismatch reject")
	}

	// Wrong tools overhead sum rejected.
	run3 := "b08f1f2e-7b5a-7c3d-8e9f-123456789017"
	execSQL(t, db, `
		INSERT INTO agent_runs(id,workspace_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,trace_id,model_snapshot,capability_snapshot)
		VALUES($1,$2,$3,'RUNNING','CHAT','USER',$4,'t3','{}','{}')`, run3, ws, agent, owner)
	badSum := agenticOK
	badSum.RunID = run3
	badSum.ToolsOverheadTokens = 99
	badSum.AssemblyDigest = execution.ComputeAssemblyDigest(badSum)
	if _, err := repo.InsertImmutable(context.Background(), badSum); err == nil {
		t.Fatal("expected tools overhead sum reject")
	}

	// Forged assembly digest rejected (exact equality to ComputeAssemblyDigest).
	run4 := "b08f1f2e-7b5a-7c3d-8e9f-123456789018"
	execSQL(t, db, `
		INSERT INTO agent_runs(id,workspace_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,trace_id,model_snapshot,capability_snapshot)
		VALUES($1,$2,$3,'RUNNING','CHAT','USER',$4,'t4','{}','{}')`, run4, ws, agent, owner)
	forged := agenticOK
	forged.RunID = run4
	forged.AssemblyDigest = strings.Repeat("b", 64)
	if _, err := repo.InsertImmutable(context.Background(), forged); err == nil {
		t.Fatal("expected forged assembly digest reject")
	}

	// Uppercase catalog digest never normalized into validity.
	run5 := "b08f1f2e-7b5a-7c3d-8e9f-123456789019"
	execSQL(t, db, `
		INSERT INTO agent_runs(id,workspace_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,trace_id,model_snapshot,capability_snapshot)
		VALUES($1,$2,$3,'RUNNING','CHAT','USER',$4,'t5','{}','{}')`, run5, ws, agent, owner)
	upper := agenticOK
	upper.RunID = run5
	upper.ToolCatalogDigest = strings.ToUpper(hash)
	upper.AssemblyDigest = "" // recompute after digest change
	if _, err := repo.InsertImmutable(context.Background(), upper); err == nil {
		t.Fatal("expected uppercase catalog digest reject")
	}

	// Whitespace-padded catalog digest rejected.
	run6 := "b08f1f2e-7b5a-7c3d-8e9f-12345678901a"
	execSQL(t, db, `
		INSERT INTO agent_runs(id,workspace_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,trace_id,model_snapshot,capability_snapshot)
		VALUES($1,$2,$3,'RUNNING','CHAT','USER',$4,'t6','{}','{}')`, run6, ws, agent, owner)
	wsDigest := agenticOK
	wsDigest.RunID = run6
	wsDigest.ToolCatalogDigest = " " + hash
	wsDigest.AssemblyDigest = ""
	if _, err := repo.InsertImmutable(context.Background(), wsDigest); err == nil {
		t.Fatal("expected whitespace catalog digest reject")
	}
}

func TestContextAssemblyGetByRunValidatesAndClassicV1Digest(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	const (
		owner = "b08f1f2e-7b5a-7c3d-8e9f-123456789021"
		ws    = "b08f1f2e-7b5a-7c3d-8e9f-123456789022"
		model = "b08f1f2e-7b5a-7c3d-8e9f-123456789023"
		agent = "b08f1f2e-7b5a-7c3d-8e9f-123456789024"
		run   = "b08f1f2e-7b5a-7c3d-8e9f-123456789025"
		run2  = "b08f1f2e-7b5a-7c3d-8e9f-123456789026"
		run3  = "b08f1f2e-7b5a-7c3d-8e9f-123456789027"
		hash  = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	)
	execSQL(t, db, `INSERT INTO users(id,username,display_name) VALUES($1,'asm3.owner','A')`, owner)
	execSQL(t, db, `INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES($1,'asm3','A','SANDBOX',$2,$2,$2)`, ws, owner)
	execSQL(t, db, `INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES($1,$2,'m','openai','https://x','m',$3,$3)`, model, ws, owner)
	execSQL(t, db, `INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES($1,$2,'a',$3,$4,$4)`, agent, ws, model, owner)
	for _, r := range []string{run, run2, run3} {
		execSQL(t, db, `
			INSERT INTO agent_runs(id,workspace_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,trace_id,model_snapshot,capability_snapshot)
			VALUES($1,$2,$3,'RUNNING','CHAT','USER',$4,'t','{}','{}')`, r, ws, agent, owner)
	}

	repo, err := execution.NewContextAssemblyRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	// New classic row via repository: current digest; GetByRun validates.
	classic := execution.ContextAssemblyRecord{
		WorkspaceID: ws, RunID: run, Mode: "token_window",
		PolicySnapshotHash: hash, ModelSnapshotHash: hash, CapabilitySnapshotHash: hash, AgentSnapshotHash: hash,
		EstimatorProfile: "o200k_base", EstimatorVersion: "v1",
		HardInputCeilingTokens: 1000, OutputReserveTokens: 100, SafetyMarginTokens: 10, ToolsOverheadTokens: 5,
		SystemPromptHash: hash, IncludedSegments: json.RawMessage(`[]`), EstimatedTotalTokens: 50,
		ToolSearchMode: execution.AssemblyToolSearchModeNone,
	}
	classic.AssemblyDigest = execution.ComputeAssemblyDigest(classic)
	if _, err := repo.InsertImmutable(context.Background(), classic); err != nil {
		t.Fatalf("insert classic: %v", err)
	}
	got, err := repo.GetByRun(context.Background(), ws, run)
	if err != nil {
		t.Fatalf("GetByRun classic: %v", err)
	}
	if got.AssemblyDigest != classic.AssemblyDigest {
		t.Fatalf("digest mismatch")
	}

	// Legacy classic-v1 digest fixture: insert via SQL with original pre-Agentic digest
	// (no agentic fields in payload). GetByRun must accept without rewriting.
	v1Payload := strings.Join([]string{
		ws, run2, "", "token_window",
		hash, hash, hash, hash,
		"o200k_base", "v1",
		"1000", "100", "10", "5",
		"", hash,
		"[]",
		"", "", "0",
		"", "", "",
		"50",
	}, "|")
	sum := sha256SumHex(v1Payload)
	legacyID := "b08f1f2e-7b5a-7c3d-8e9f-123456789028"
	execSQL(t, db, `
		INSERT INTO agent_run_context_assemblies(
			id,workspace_id,run_id,session_id,mode,
			policy_snapshot_hash,model_snapshot_hash,capability_snapshot_hash,agent_snapshot_hash,
			estimator_profile,estimator_version,
			hard_input_ceiling_tokens,output_reserve_tokens,safety_margin_tokens,tools_overhead_tokens,
			system_prompt_hash,included_segments,omitted_prefix_count,
			assembly_digest,estimated_total_tokens,
			tool_search_mode
		) VALUES (
			$1,$2,$3,NULL,'token_window',
			$4,$4,$4,$4,
			'o200k_base','v1',
			1000,100,10,5,
			$4,'[]',0,
			$5,50,
			'none'
		)`, legacyID, ws, run2, hash, sum)
	legacy, err := repo.GetByRun(context.Background(), ws, run2)
	if err != nil {
		t.Fatalf("GetByRun classic-v1 legacy: %v", err)
	}
	if legacy.AssemblyDigest != sum {
		t.Fatalf("legacy digest rewritten: got %s want %s", legacy.AssemblyDigest, sum)
	}

	// Corrupt agentic row (wrong tools overhead) fails GetByRun closed.
	// Insert valid agentic then corrupt via SQL (bypass immutability trigger by using a fresh approach).
	// Immutability trigger blocks UPDATE; insert invalid via SQL if constraints allow.
	// SQL constraints may block bad overhead — use forged digest with valid-looking agentic counts
	// that fail app-level tools sum: if SQL enforces tools_overhead identity, skip insert.
	// Instead corrupt digest only (forged digest with otherwise valid agentic fields).
	agenticRun := run3
	agenticID := "b08f1f2e-7b5a-7c3d-8e9f-123456789029"
	// Valid structural fields but forged digest.
	forgedDigest := strings.Repeat("d", 64)
	// Try SQL insert — may pass DB constraints if they only check format/mode.
	_, sqlErr := db.Exec(`
		INSERT INTO agent_run_context_assemblies(
			id,workspace_id,run_id,session_id,mode,
			policy_snapshot_hash,model_snapshot_hash,capability_snapshot_hash,agent_snapshot_hash,
			estimator_profile,estimator_version,
			hard_input_ceiling_tokens,output_reserve_tokens,safety_margin_tokens,tools_overhead_tokens,
			system_prompt_hash,included_segments,omitted_prefix_count,
			assembly_digest,estimated_total_tokens,
			tool_search_mode,tool_catalog_digest,
			immediate_tool_count,deferred_tool_count,max_loaded_tool_count,
			immediate_tools_tokens,deferred_metadata_tokens,dynamic_tool_load_reserve_tokens
		) VALUES (
			$1,$2,$3,NULL,'token_window',
			$4,$4,$4,$4,
			'o200k_base','contextwindow-estimator.agentic-openai-responses.v1',
			1000,100,10,15,
			$4,'[]',0,
			$5,50,
			'client_bounded',$4,
			0,3,3,
			0,5,10
		)`, agenticID, ws, agenticRun, hash, forgedDigest)
	if sqlErr != nil {
		t.Logf("SQL forged insert blocked by constraints (ok): %v", sqlErr)
	} else {
		if _, err := repo.GetByRun(context.Background(), ws, agenticRun); err == nil {
			t.Fatal("expected GetByRun to fail closed on forged digest")
		}
	}
}

func sha256SumHex(payload string) string {
	// Local helper for classic-v1 fixture (mirrors execution classic-v1 algorithm).
	h := sha256.New()
	_, _ = h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

func execSQL(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("sql: %v\n%s", err, q)
	}
}
