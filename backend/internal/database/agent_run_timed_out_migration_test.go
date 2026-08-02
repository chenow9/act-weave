package database_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

// TestMigrateTo9_RunningToTimedOutAllowed proves 000009 itself introduces complete
// TIMED_OUT semantics (status check + permanent-snapshot transition matrix).
// A database stopped at version 9 must accept RUNNING→TIMED_OUT; rolling back to
// version 8 must reject it again (down restores pre-TIMED_OUT baseline).
func TestMigrateTo9_RunningToTimedOutAllowed(t *testing.T) {
	testDatabase := dbtest.New(t)
	dsn := testDatabase.DSN()

	version := testDatabase.MigrateTo(t, 9)
	if !version.Applied || version.Number != 9 || version.Dirty {
		t.Fatalf("expected clean version 9, got %+v", version)
	}

	db := openTimedOutMigrationDB(t, dsn)
	fx := seedTimedOutMigrationFixture(t, db)

	// At version 9: RUNNING → TIMED_OUT must succeed (trigger + CHECK + finish_state).
	if _, err := db.Exec(`
		UPDATE agent_runs
		SET status = 'TIMED_OUT',
		    finished_at = CURRENT_TIMESTAMP,
		    lock_version = lock_version + 1
		WHERE id = $1 AND workspace_id = $2 AND status = 'RUNNING'
	`, fx.runID, fx.workspaceID); err != nil {
		t.Fatalf("RUNNING→TIMED_OUT must be allowed at migration version 9: %v", err)
	}
	var status string
	var finishedAt sql.NullTime
	if err := db.QueryRow(`
		SELECT status, finished_at FROM agent_runs WHERE id=$1 AND workspace_id=$2
	`, fx.runID, fx.workspaceID).Scan(&status, &finishedAt); err != nil {
		t.Fatalf("read run: %v", err)
	}
	if status != "TIMED_OUT" || !finishedAt.Valid {
		t.Fatalf("want TIMED_OUT with finished_at, got status=%s finished=%v", status, finishedAt.Valid)
	}

	// Seed a second RUNNING run to exercise down/up without leaving only TIMED_OUT rows
	// (down rewrites status CHECK excluding TIMED_OUT and would fail if all runs are TIMED_OUT).
	run2 := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO agent_runs(
			id, workspace_id, session_id, agent_id, status, trigger_type,
			triggered_by_type, triggered_by_id, trace_id,
			model_snapshot, capability_snapshot, context_policy_snapshot
		) VALUES (
			$1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'trace-to-2',
			'{}'::jsonb,'{}'::jsonb,'{}'::jsonb
		)
	`, run2, fx.workspaceID, fx.sessionID, fx.agentID, fx.ownerID); err != nil {
		t.Fatalf("insert second run: %v", err)
	}
	// Delete/finish TIMED_OUT row is blocked (permanent). Keep it — down may fail if
	// status constraint is rewritten while TIMED_OUT exists. Prefer: migrate down only
	// after verifying with a clean path using a fresh DB for down check.
	_ = db.Close()

	// Fresh isolate: version 8 must reject RUNNING→TIMED_OUT; then up to 9 allows it.
	h2 := dbtest.New(t)
	v8 := h2.MigrateTo(t, 8)
	if v8.Number != 8 || v8.Dirty {
		t.Fatalf("expected version 8, got %+v", v8)
	}
	db8 := h2.Open(t)
	fx8 := seedTimedOutMigrationFixture(t, db8)
	_, err := db8.Exec(`
		UPDATE agent_runs
		SET status = 'TIMED_OUT',
		    finished_at = CURRENT_TIMESTAMP,
		    lock_version = lock_version + 1
		WHERE id = $1 AND workspace_id = $2 AND status = 'RUNNING'
	`, fx8.runID, fx8.workspaceID)
	if err == nil {
		t.Fatal("RUNNING→TIMED_OUT must be rejected at migration version 8 (pre-000009)")
	}

	// Up to 9 on same DB: transition must now succeed.
	v9 := h2.MigrateTo(t, 9)
	if v9.Number != 9 || v9.Dirty {
		t.Fatalf("expected version 9 after up, got %+v", v9)
	}
	if _, err := db8.Exec(`
		UPDATE agent_runs
		SET status = 'TIMED_OUT',
		    finished_at = CURRENT_TIMESTAMP,
		    lock_version = lock_version + 1
		WHERE id = $1 AND workspace_id = $2 AND status = 'RUNNING'
	`, fx8.runID, fx8.workspaceID); err != nil {
		t.Fatalf("after migrate 8→9, RUNNING→TIMED_OUT must succeed: %v", err)
	}

	// Down 9→8 consistency: with a RUNNING row (no TIMED_OUT present), down succeeds
	// and again rejects TIMED_OUT transitions.
	h3 := dbtest.New(t)
	_ = h3.MigrateTo(t, 9)
	db3 := h3.Open(t)
	fx3 := seedTimedOutMigrationFixture(t, db3)
	_ = db3.Close()
	vDown := h3.MigrateTo(t, 8)
	if vDown.Number != 8 || vDown.Dirty {
		t.Fatalf("down 9→8: expected version 8, got %+v", vDown)
	}
	db3 = h3.Open(t)
	_, err = db3.Exec(`
		UPDATE agent_runs
		SET status = 'TIMED_OUT',
		    finished_at = CURRENT_TIMESTAMP,
		    lock_version = lock_version + 1
		WHERE id = $1 AND workspace_id = $2 AND status = 'RUNNING'
	`, fx3.runID, fx3.workspaceID)
	if err == nil {
		t.Fatal("after down to version 8, RUNNING→TIMED_OUT must be rejected again")
	}
	// Re-up to 9 restores TIMED_OUT transition.
	_ = h3.MigrateTo(t, 9)
	if _, err := db3.Exec(`
		UPDATE agent_runs
		SET status = 'TIMED_OUT',
		    finished_at = CURRENT_TIMESTAMP,
		    lock_version = lock_version + 1
		WHERE id = $1 AND workspace_id = $2 AND status = 'RUNNING'
	`, fx3.runID, fx3.workspaceID); err != nil {
		t.Fatalf("after down/up to 9, RUNNING→TIMED_OUT must succeed: %v", err)
	}
	_ = db3.Close()

	// Latest still allows TIMED_OUT (000016 re-applies same matrix idempotently).
	hLatest := dbtest.New(t)
	vLatest := hLatest.MigrateToLatest(t)
	if vLatest.Number < 9 {
		t.Fatalf("latest version=%d", vLatest.Number)
	}
	dbL := hLatest.Open(t)
	fxL := seedTimedOutMigrationFixture(t, dbL)
	if _, err := dbL.Exec(`
		UPDATE agent_runs
		SET status = 'TIMED_OUT',
		    finished_at = CURRENT_TIMESTAMP,
		    lock_version = lock_version + 1
		WHERE id = $1 AND workspace_id = $2 AND status = 'RUNNING'
	`, fxL.runID, fxL.workspaceID); err != nil {
		t.Fatalf("latest migrations must still allow RUNNING→TIMED_OUT: %v", err)
	}
	_ = dbL.Close()
}

type timedOutFixture struct {
	ownerID, workspaceID, modelID, agentID, sessionID, runID string
}

func openTimedOutMigrationDB(t *testing.T, dsn string) *sql.DB {
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

func seedTimedOutMigrationFixture(t *testing.T, db *sql.DB) timedOutFixture {
	t.Helper()
	fx := timedOutFixture{
		ownerID:     uuid.Must(uuid.NewV7()).String(),
		workspaceID: uuid.Must(uuid.NewV7()).String(),
		modelID:     uuid.Must(uuid.NewV7()).String(),
		agentID:     uuid.Must(uuid.NewV7()).String(),
		sessionID:   uuid.Must(uuid.NewV7()).String(),
		runID:       uuid.Must(uuid.NewV7()).String(),
	}
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,$2,'TO Owner')`,
		fx.ownerID, "to-"+fx.ownerID[:8]); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,$2,'TO Space','PRODUCTION',$3,$3,$3)
	`, fx.workspaceID, "to-"+fx.workspaceID[:8], fx.ownerID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'TO Model','openai','https://models.example.test','to-model',$3,$3)
	`, fx.modelID, fx.workspaceID, fx.ownerID); err != nil {
		t.Fatalf("insert model: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'TO Agent',$3,$4,$4)
	`, fx.agentID, fx.workspaceID, fx.modelID, fx.ownerID); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		VALUES($1,$2,$3,'TO session',$4)
	`, fx.sessionID, fx.workspaceID, fx.agentID, fx.ownerID); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_runs(
			id, workspace_id, session_id, agent_id, status, trigger_type,
			triggered_by_type, triggered_by_id, trace_id,
			model_snapshot, capability_snapshot, context_policy_snapshot
		) VALUES (
			$1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'trace-to',
			'{}'::jsonb,'{}'::jsonb,'{}'::jsonb
		)
	`, fx.runID, fx.workspaceID, fx.sessionID, fx.agentID, fx.ownerID); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	return fx
}
