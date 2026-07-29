package execution_test

import (
	"context"
	"database/sql"
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
	}
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

func execSQL(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("sql: %v\n%s", err, q)
	}
}
