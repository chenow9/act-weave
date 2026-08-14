package einoruntime_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/metrics"
	"github.com/cloudwego/eino/adk"
)

func TestNewPostgresCheckPointStoreRejectsNilDB(t *testing.T) {
	t.Parallel()
	if _, err := einoruntime.NewPostgresCheckPointStore(nil); err == nil {
		t.Fatal("expected error for nil db")
	}
	if _, err := einoruntime.NewPostgresCheckPointStoreWithTTL(&sql.DB{}, 0); err == nil {
		t.Fatal("expected error for non-positive TTL")
	}
}

func TestDefaultCheckpointTTLMatchesConfirmationDefault(t *testing.T) {
	t.Parallel()
	// D15: single business clock — no separate checkpointTTLHours.
	want := time.Duration(execution.DefaultConfirmationTTLSeconds) * time.Second
	if einoruntime.DefaultCheckpointTTL != want {
		t.Fatalf("DefaultCheckpointTTL = %v, want %v (execution.DefaultConfirmationTTLSeconds=%d)",
			einoruntime.DefaultCheckpointTTL, want, execution.DefaultConfirmationTTLSeconds)
	}
	if execution.DefaultConfirmationTTLSeconds != 600 {
		t.Fatalf("DefaultConfirmationTTLSeconds = %d, want 600", execution.DefaultConfirmationTTLSeconds)
	}
	if got := einoruntime.DefaultExpiresAt(time.Unix(1_700_000_000, 0).UTC()); !got.Equal(
		time.Unix(1_700_000_000, 0).UTC().Add(600 * time.Second),
	) {
		t.Fatalf("DefaultExpiresAt unexpected: %v", got)
	}
}

func TestPostgresCheckPointStoreRejectsInvalidIDs(t *testing.T) {
	t.Parallel()
	// Validation runs before any SQL; a non-nil *sql.DB that is not opened is fine.
	store, err := einoruntime.NewPostgresCheckPointStore(&sql.DB{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	invalid := []string{
		"",
		"no-prefix",
		"ws/only-two",
		"ws/ws-1/agent_run/run-1",
		"agent_run/run-1/nonce",
	}
	for _, id := range invalid {
		if _, _, err := store.Get(ctx, id); !errors.Is(err, einoruntime.ErrInvalidCheckpointID) {
			t.Fatalf("Get(%q): got %v, want ErrInvalidCheckpointID", id, err)
		}
		if err := store.Set(ctx, id, []byte("x")); !errors.Is(err, einoruntime.ErrInvalidCheckpointID) {
			t.Fatalf("Set(%q): got %v, want ErrInvalidCheckpointID", id, err)
		}
		if err := store.TouchExpiresAt(ctx, id, time.Now().UTC()); !errors.Is(err, einoruntime.ErrInvalidCheckpointID) {
			t.Fatalf("TouchExpiresAt(%q): got %v, want ErrInvalidCheckpointID", id, err)
		}
		if err := store.Delete(ctx, id); !errors.Is(err, einoruntime.ErrInvalidCheckpointID) {
			t.Fatalf("Delete(%q): got %v, want ErrInvalidCheckpointID", id, err)
		}
	}
}

func TestPostgresCheckPointStoreImplementsEinoInterfaces(t *testing.T) {
	t.Parallel()
	var store adk.CheckPointStore = (*einoruntime.PostgresCheckPointStore)(nil)
	var deleter adk.CheckPointDeleter = (*einoruntime.PostgresCheckPointStore)(nil)
	_ = store
	_ = deleter
}

func TestPostgresCheckPointStoreRoundTrip(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 23 || version.Dirty {
		t.Fatalf("expected clean latest migration 4, got %+v", version)
	}
	db := testDatabase.Open(t)
	store, err := einoruntime.NewPostgresCheckPointStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	agentID, err := einoruntime.FormatCheckpointID(
		"11111111-1111-1111-1111-111111111111",
		einoruntime.CheckpointKindAgentRun,
		"22222222-2222-2222-2222-222222222222",
		"nonce-agent-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	workflowID, err := einoruntime.FormatCheckpointID(
		"11111111-1111-1111-1111-111111111111",
		einoruntime.CheckpointKindWorkflowExecution,
		"33333333-3333-3333-3333-333333333333",
		"nonce-wf-1",
	)
	if err != nil {
		t.Fatal(err)
	}

	// Missing → not found.
	if payload, ok, err := store.Get(ctx, agentID); err != nil || ok || payload != nil {
		t.Fatalf("Get missing: payload=%v ok=%v err=%v", payload, ok, err)
	}

	payload := []byte{0x01, 0x02, gobMarker()}
	if err := store.Set(ctx, agentID, payload); err != nil {
		t.Fatalf("Set agent: %v", err)
	}
	got, ok, err := store.Get(ctx, agentID)
	if err != nil || !ok {
		t.Fatalf("Get agent: ok=%v err=%v", ok, err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload mismatch: got %v want %v", got, payload)
	}

	// Assert columns written correctly.
	var (
		workspaceID, kind, ownerID string
		expiresAt                  time.Time
	)
	if err := db.QueryRow(`
		SELECT workspace_id, kind, owner_id, expires_at
		  FROM eino_checkpoints
		 WHERE checkpoint_id = $1
	`, agentID).Scan(&workspaceID, &kind, &ownerID, &expiresAt); err != nil {
		t.Fatalf("scan columns: %v", err)
	}
	if workspaceID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("workspace_id = %q", workspaceID)
	}
	if kind != einoruntime.CheckpointKindAgentRun {
		t.Fatalf("kind = %q", kind)
	}
	if ownerID != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("owner_id = %q", ownerID)
	}
	if time.Until(expiresAt) < 9*time.Minute || time.Until(expiresAt) > 11*time.Minute {
		t.Fatalf("expires_at not ~DefaultCheckpointTTL from now: %v (until %v)", expiresAt, time.Until(expiresAt))
	}

	// Upsert with explicit expiry (D15: confirmation-aligned absolute clock).
	// Simulate confirmation.ExpiresAt = now + 30m written to the checkpoint.
	confirmationExpiresAt := time.Now().UTC().Add(30 * time.Minute)
	updated := []byte("updated-gob")
	if err := store.SetWithExpiresAt(ctx, agentID, updated, confirmationExpiresAt); err != nil {
		t.Fatalf("SetWithExpiresAt: %v", err)
	}
	got, ok, err = store.Get(ctx, agentID)
	if err != nil || !ok || string(got) != string(updated) {
		t.Fatalf("Get after upsert: ok=%v got=%q err=%v", ok, got, err)
	}
	if err := db.QueryRow(`SELECT expires_at FROM eino_checkpoints WHERE checkpoint_id=$1`, agentID).
		Scan(&expiresAt); err != nil {
		t.Fatal(err)
	}
	if expiresAt.Sub(confirmationExpiresAt).Abs() > time.Second {
		t.Fatalf("expires_at = %v, want ~%v (confirmation clock)", expiresAt, confirmationExpiresAt)
	}

	// TouchExpiresAt renews expiry without changing payload (confirmation extend).
	renewed := confirmationExpiresAt.Add(10 * time.Minute)
	if err := store.TouchExpiresAt(ctx, agentID, renewed); err != nil {
		t.Fatalf("TouchExpiresAt: %v", err)
	}
	got, ok, err = store.Get(ctx, agentID)
	if err != nil || !ok || string(got) != string(updated) {
		t.Fatalf("Get after TouchExpiresAt: ok=%v got=%q err=%v", ok, got, err)
	}
	if err := db.QueryRow(`SELECT expires_at FROM eino_checkpoints WHERE checkpoint_id=$1`, agentID).
		Scan(&expiresAt); err != nil {
		t.Fatal(err)
	}
	if expiresAt.Sub(renewed).Abs() > time.Second {
		t.Fatalf("expires_at after touch = %v, want ~%v", expiresAt, renewed)
	}

	// Touch missing is an error.
	missingID, err := einoruntime.FormatCheckpointID(
		"11111111-1111-1111-1111-111111111111",
		einoruntime.CheckpointKindAgentRun,
		"22222222-2222-2222-2222-222222222222",
		"nonce-missing",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TouchExpiresAt(ctx, missingID, renewed); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("TouchExpiresAt missing: got %v, want sql.ErrNoRows", err)
	}

	// Workflow path.
	if err := store.Set(ctx, workflowID, []byte("wf")); err != nil {
		t.Fatalf("Set workflow: %v", err)
	}
	if err := db.QueryRow(`SELECT kind FROM eino_checkpoints WHERE checkpoint_id=$1`, workflowID).
		Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != einoruntime.CheckpointKindWorkflowExecution {
		t.Fatalf("workflow kind = %q", kind)
	}

	// Trusted workspace mismatch.
	badCtx := einoruntime.WithTrustedWorkspaceID(ctx, "other-workspace")
	if _, _, err := store.Get(badCtx, agentID); !errors.Is(err, einoruntime.ErrInvalidCheckpointID) {
		t.Fatalf("trusted mismatch Get: %v", err)
	}
	// Trusted workspace match.
	goodCtx := einoruntime.WithTrustedWorkspaceID(ctx, "11111111-1111-1111-1111-111111111111")
	if _, ok, err := store.Get(goodCtx, agentID); err != nil || !ok {
		t.Fatalf("trusted match Get: ok=%v err=%v", ok, err)
	}

	// Delete.
	if err := store.Delete(ctx, agentID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, err := store.Get(ctx, agentID); err != nil || ok {
		t.Fatalf("Get after Delete: ok=%v err=%v", ok, err)
	}
	// Delete missing is OK.
	if err := store.Delete(ctx, agentID); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
}

func TestPostgresCheckPointStoreDeleteExpiredKeepsNonExpired(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 23 || version.Dirty {
		t.Fatalf("expected clean latest migration 4, got %+v", version)
	}
	db := testDatabase.Open(t)
	store, err := einoruntime.NewPostgresCheckPointStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()

	expiredID, err := einoruntime.FormatCheckpointID(
		"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		einoruntime.CheckpointKindAgentRun,
		"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		"nonce-expired",
	)
	if err != nil {
		t.Fatal(err)
	}
	liveID, err := einoruntime.FormatCheckpointID(
		"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		einoruntime.CheckpointKindAgentRun,
		"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		"nonce-live",
	)
	if err != nil {
		t.Fatal(err)
	}
	// Boundary: expires_at == now is treated as expired (matches confirmation ExpireDue).
	boundaryID, err := einoruntime.FormatCheckpointID(
		"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		einoruntime.CheckpointKindAgentRun,
		"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		"nonce-boundary",
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SetWithExpiresAt(ctx, expiredID, []byte("expired"), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWithExpiresAt(ctx, liveID, []byte("live"), now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWithExpiresAt(ctx, boundaryID, []byte("boundary"), now); err != nil {
		t.Fatal(err)
	}

	deleted, err := store.DeleteExpired(ctx, now, 100)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("DeleteExpired deleted=%d, want 2 (expired+boundary)", deleted)
	}

	if _, ok, err := store.Get(ctx, expiredID); err != nil || ok {
		t.Fatalf("expired should be gone: ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.Get(ctx, boundaryID); err != nil || ok {
		t.Fatalf("boundary should be gone: ok=%v err=%v", ok, err)
	}
	got, ok, err := store.Get(ctx, liveID)
	if err != nil || !ok || string(got) != "live" {
		t.Fatalf("live should remain: ok=%v got=%q err=%v", ok, got, err)
	}

	// Second pass is a no-op.
	deleted, err = store.DeleteExpired(ctx, now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("second DeleteExpired deleted=%d, want 0", deleted)
	}

	// Validation.
	if _, err := store.DeleteExpired(ctx, time.Time{}, 10); err == nil {
		t.Fatal("expected error for zero now")
	}
	if _, err := store.DeleteExpired(ctx, now, 0); err == nil {
		t.Fatal("expected error for zero limit")
	}
	if _, err := store.DeleteExpired(ctx, now, 1001); err == nil {
		t.Fatal("expected error for limit > 1000")
	}
}

func TestCheckpointCleanupWorkerDeletesExpiredAndRecordsMetrics(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 23 || version.Dirty {
		t.Fatalf("expected clean latest migration 4, got %+v", version)
	}
	db := testDatabase.Open(t)
	store, err := einoruntime.NewPostgresCheckPointStore(db)
	if err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewAAPCollector()
	worker, err := einoruntime.NewCheckpointCleanupWorker(
		store,
		einoruntime.DefaultCheckpointCleanupConfig(),
		collector,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()

	expiredID, err := einoruntime.FormatCheckpointID(
		"cccccccc-cccc-cccc-cccc-cccccccccccc",
		einoruntime.CheckpointKindWorkflowExecution,
		"dddddddd-dddd-dddd-dddd-dddddddddddd",
		"nonce-gc",
	)
	if err != nil {
		t.Fatal(err)
	}
	liveID, err := einoruntime.FormatCheckpointID(
		"cccccccc-cccc-cccc-cccc-cccccccccccc",
		einoruntime.CheckpointKindWorkflowExecution,
		"dddddddd-dddd-dddd-dddd-dddddddddddd",
		"nonce-keep",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWithExpiresAt(ctx, expiredID, []byte("gone"), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWithExpiresAt(ctx, liveID, []byte("keep"), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	worker.RunOnce(ctx)

	if _, ok, err := store.Get(ctx, expiredID); err != nil || ok {
		t.Fatalf("worker should delete expired: ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.Get(ctx, liveID); err != nil || !ok {
		t.Fatalf("worker should keep live: ok=%v err=%v", ok, err)
	}

	snap := collector.Snapshot()
	if snap.EinoCheckpointCleanupRunsTotal != 1 {
		t.Fatalf("runs_total=%d, want 1", snap.EinoCheckpointCleanupRunsTotal)
	}
	if snap.EinoCheckpointCleanupDeletedTotal != 1 {
		t.Fatalf("deleted_total=%d, want 1", snap.EinoCheckpointCleanupDeletedTotal)
	}
	if snap.EinoCheckpointCleanupErrorsTotal != 0 {
		t.Fatalf("errors_total=%d, want 0", snap.EinoCheckpointCleanupErrorsTotal)
	}

	// Config validation + nil-safe lifecycle.
	if _, err := einoruntime.NewCheckpointCleanupWorker(nil, einoruntime.DefaultCheckpointCleanupConfig(), nil, nil); err == nil {
		t.Fatal("expected nil store error")
	}
	badCfg := einoruntime.DefaultCheckpointCleanupConfig()
	badCfg.Interval = 0
	if _, err := einoruntime.NewCheckpointCleanupWorker(store, badCfg, nil, nil); err == nil {
		t.Fatal("expected invalid config error")
	}
	var nilWorker *einoruntime.CheckpointCleanupWorker
	nilWorker.Start(ctx)
	nilWorker.Stop()
	nilWorker.RunOnce(ctx)
}

// gobMarker returns a distinctive byte so payload assertions are non-trivial.
func gobMarker() byte { return 0x7e }
