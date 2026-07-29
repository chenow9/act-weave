package contextsummary_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/contextsummary"
	"actweave/backend/internal/database/dbtest"
)

func TestSummaryClaimReadyImmutable(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	const (
		owner   = "c08f1f2e-7b5a-7c3d-8e9f-123456789001"
		ws      = "c08f1f2e-7b5a-7c3d-8e9f-123456789002"
		model   = "c08f1f2e-7b5a-7c3d-8e9f-123456789003"
		agent   = "c08f1f2e-7b5a-7c3d-8e9f-123456789004"
		session = "c08f1f2e-7b5a-7c3d-8e9f-123456789005"
		msgEnd  = "c08f1f2e-7b5a-7c3d-8e9f-123456789006"
		token   = "c08f1f2e-7b5a-7c3d-8e9f-123456789007"
		obj     = "c08f1f2e-7b5a-7c3d-8e9f-123456789008"
		hash    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	exec(t, db, `INSERT INTO users(id,username,display_name) VALUES($1,'s.owner','S')`, owner)
	exec(t, db, `INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES($1,'sum','S','SANDBOX',$2,$2,$2)`, ws, owner)
	exec(t, db, `INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES($1,$2,'m','openai','https://x','m',$3,$3)`, model, ws, owner)
	exec(t, db, `INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES($1,$2,'a',$3,$4,$4)`, agent, ws, model, owner)
	exec(t, db, `INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by) VALUES($1,$2,$3,'s',$4)`, session, ws, agent, owner)

	repo, err := contextsummary.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	claim := contextsummary.ClaimInput{
		WorkspaceID: ws, SessionID: session, CoverageEndMessageID: msgEnd,
		SourceDigest: hash, PolicyFingerprint: hash, PromptTemplateHash: hash,
		PromptTemplateVersion: "v1", OwnerToken: token, LeaseTTL: time.Minute,
	}
	s, claimed, err := repo.ClaimOrGet(context.Background(), claim)
	if err != nil || !claimed || s.Status != contextsummary.StatusBuilding {
		t.Fatalf("claim: %+v claimed=%v err=%v", s, claimed, err)
	}
	// Need real stored object for FK? content_object_id has no FK to stored_objects - only UUID.
	ready, err := repo.MarkReady(context.Background(), ws, s.ID, token, obj, hash, 12)
	if err != nil || ready.Status != contextsummary.StatusReady {
		t.Fatalf("ready: %+v err=%v", ready, err)
	}
	// READY immutable
	if _, err := db.Exec(`UPDATE chat_context_summaries SET status='FAILED', failure_code='x' WHERE id=$1`, s.ID); err == nil {
		t.Fatal("expected READY immutable")
	}
	// Same key returns READY, not a new claim
	again, claimed2, err := repo.ClaimOrGet(context.Background(), claim)
	if err != nil || claimed2 || again.Status != contextsummary.StatusReady {
		t.Fatalf("reuse: %+v claimed=%v err=%v", again, claimed2, err)
	}
	// Cross-workspace miss
	if _, err := repo.Get(context.Background(), "c08f1f2e-7b5a-7c3d-8e9f-123456789099", s.ID); err == nil {
		t.Fatal("expected not found cross workspace")
	}
	if !strings.Contains(ready.SourceDigest, "aaa") {
		t.Fatal("digest")
	}
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%v\n%s", err, q)
	}
}
