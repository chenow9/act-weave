package aapfile_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/metrics"
)

func TestStagingGCAfterPromoteIntegrityFailureLeavesNoOrphan(t *testing.T) {
	// AC-14 / IC-11: promote fail → GC → staging blob and markers gone.
	service, staging, secure, db := newAAPFileService(t)
	ctx := context.Background()
	collector := metrics.NewAAPFileCollector()

	content := pngBytes
	wrong := strings.Repeat("ab", 32)
	intent, err := service.CreateUploadIntent(ctx, createCCInput(len(content), wrong))
	if err != nil {
		t.Fatal(err)
	}
	bucket := intent.File.StagingBucket
	key := *intent.File.StagingObjectKey
	staging.put(bucket, key, content)

	if _, err := service.CompleteUpload(ctx, aapfile.CompleteUploadInput{
		Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
		FileID: intent.File.ID,
	}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := service.Promote(ctx, testWorkspaceID, intent.File.ID); err == nil {
		t.Fatal("expected promote integrity failure")
	}
	if secure.putCalls != 0 {
		t.Fatalf("secure put must not run, calls=%d", secure.putCalls)
	}
	// Staging blob still present after promote fail (left for GC).
	if !staging.has(bucket, key) {
		t.Fatal("staging blob must remain after promote fail before GC")
	}
	got, err := service.GetFile(ctx, testWorkspaceID, intent.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != aapfile.StatusFailed || got.StagingObjectKey == nil {
		t.Fatalf("pre-gc file=%+v", got)
	}

	repo, err := aapfile.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := aapfile.NewStagingGCWorker(repo, staging, aapfile.StagingGCConfig{
		Interval:           30 * time.Second,
		BatchLimit:         50,
		MaxPromoteAttempts: aapfile.DefaultMaxPromoteAttempts,
		Metrics:            collector,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Cleared < 1 {
		t.Fatalf("expected at least one cleared, got %+v", result)
	}
	if staging.has(bucket, key) {
		t.Fatal("staging object must be gone after GC")
	}
	after, err := service.GetFile(ctx, testWorkspaceID, intent.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.StagingObjectKey != nil {
		t.Fatalf("staging_object_key must be null, got %v", after.StagingObjectKey)
	}
	if after.StagingDeletedAt == nil {
		t.Fatal("staging_deleted_at required after GC")
	}
	if after.Status != aapfile.StatusFailed {
		t.Fatalf("status=%s want FAILED (not mutated to EXPIRED)", after.Status)
	}
	if after.StoredObjectID != nil {
		t.Fatalf("no permanent object, got %v", after.StoredObjectID)
	}
	snap := collector.Snapshot()
	if snap["aap_file_staging_orphan_bytes"] != 0 {
		t.Fatalf("orphan gauge after full clear: %d", snap["aap_file_staging_orphan_bytes"])
	}
}

func TestStagingGCExpiresPendingAndClearsStaging(t *testing.T) {
	service, staging, _, db := newAAPFileService(t)
	ctx := context.Background()

	// Force short staging TTL via clock skew on row after create.
	intent, err := service.CreateUploadIntent(ctx, createCCInput(len(pngBytes), ""))
	if err != nil {
		t.Fatal(err)
	}
	bucket := intent.File.StagingBucket
	key := *intent.File.StagingObjectKey
	staging.put(bucket, key, pngBytes)

	// Expire staging window in DB.
	if _, err := db.Exec(`
		UPDATE aap_files SET staging_expires_at=clock_timestamp() - interval '1 minute'
		WHERE id=$1
	`, intent.File.ID); err != nil {
		t.Fatal(err)
	}

	repo, err := aapfile.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := aapfile.NewStagingGCWorker(repo, staging, aapfile.DefaultStagingGCConfig())
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cleared < 1 {
		t.Fatalf("expected clear, got %+v", result)
	}
	if staging.has(bucket, key) {
		t.Fatal("staging blob should be deleted")
	}
	got, err := service.GetFile(ctx, testWorkspaceID, intent.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != aapfile.StatusExpired {
		t.Fatalf("status=%s want EXPIRED", got.Status)
	}
	if got.StagingObjectKey != nil || got.StagingDeletedAt == nil {
		t.Fatalf("staging markers not cleared: key=%v deleted=%v", got.StagingObjectKey, got.StagingDeletedAt)
	}
	if got.ErrorCode == nil || *got.ErrorCode != aapfile.ErrorCodeUploadExpired {
		t.Fatalf("error_code=%v", got.ErrorCode)
	}
}

func TestStagingGCMissingObjectIsOK(t *testing.T) {
	service, staging, _, db := newAAPFileService(t)
	ctx := context.Background()

	intent, err := service.CreateUploadIntent(ctx, createCCInput(len(pngBytes), ""))
	if err != nil {
		t.Fatal(err)
	}
	// No staging.put — object already absent.
	if _, err := db.Exec(`
		UPDATE aap_files SET
			status=$2,
			staging_expires_at=clock_timestamp() - interval '1 minute'
		WHERE id=$1
	`, intent.File.ID, aapfile.StatusFailed); err != nil {
		t.Fatal(err)
	}

	repo, err := aapfile.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := aapfile.NewStagingGCWorker(repo, staging, aapfile.DefaultStagingGCConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("missing object must be ok: %v", err)
	}
	got, err := service.GetFile(ctx, testWorkspaceID, intent.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StagingDeletedAt == nil || got.StagingObjectKey != nil {
		t.Fatalf("expected markers cleared: %+v", got)
	}
}

func TestStagingGCClearsPromoteSuccessResidual(t *testing.T) {
	// stored_object_id set but staging key retained (dual-fail path) → GC clears.
	service, staging, _, db := newAAPFileService(t)
	ctx := context.Background()

	content := pngBytes
	sum := sha256Hex(content)
	intent, err := service.CreateUploadIntent(ctx, createCCInput(len(content), sum))
	if err != nil {
		t.Fatal(err)
	}
	bucket := intent.File.StagingBucket
	key := *intent.File.StagingObjectKey
	staging.put(bucket, key, content)
	if _, err := service.CompleteUpload(ctx, aapfile.CompleteUploadInput{
		Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
		FileID: intent.File.ID,
	}); err != nil {
		t.Fatal(err)
	}
	promoted, err := service.Promote(ctx, testWorkspaceID, intent.File.ID)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted.StoredObjectID == nil {
		t.Fatal("stored_object_id required")
	}
	// Happy path already deleted staging; re-introduce residual key to simulate dual-fail DB state.
	if _, err := db.Exec(`
		UPDATE aap_files SET
			staging_object_key=$2,
			staging_deleted_at=NULL
		WHERE id=$1
	`, intent.File.ID, key); err != nil {
		t.Fatal(err)
	}
	// Put blob back so GC has something to delete.
	staging.put(bucket, key, content)

	repo, err := aapfile.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := aapfile.NewStagingGCWorker(repo, staging, aapfile.DefaultStagingGCConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if staging.has(bucket, key) {
		t.Fatal("residual staging must be deleted")
	}
	got, err := service.GetFile(ctx, testWorkspaceID, intent.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StagingObjectKey != nil || got.StagingDeletedAt == nil {
		t.Fatalf("markers: key=%v deleted=%v", got.StagingObjectKey, got.StagingDeletedAt)
	}
	if got.StoredObjectID == nil {
		t.Fatal("stored_object_id must remain")
	}
}

func TestAAPFileMetricsNoFileIDLabels(t *testing.T) {
	c := metrics.NewAAPFileCollector()
	c.IncCreate()
	c.IncComplete()
	c.ObservePromoteDuration(42)
	c.IncProcessing("promote", "succeeded")
	c.IncProcessing("promote", "failed")
	c.IncDownload("client_content", "ok")
	c.SetPendingUploadGauge(3)
	c.SetStagingOrphanBytes(1024)

	snap := c.Snapshot()
	required := []string{
		"aap_file_create_total",
		"aap_file_complete_total",
		"aap_file_promote_duration_ms",
		"aap_file_processing_promote_succeeded",
		"aap_file_processing_promote_failed",
		"aap_file_download_ok",
		"aap_file_pending_upload_gauge",
		"aap_file_staging_orphan_bytes",
	}
	for _, key := range required {
		if _, ok := snap[key]; !ok {
			t.Fatalf("missing metric key %q in %#v", key, snap)
		}
	}
	if snap["aap_file_create_total"] != 1 || snap["aap_file_complete_total"] != 1 {
		t.Fatalf("create/complete totals: %#v", snap)
	}
	if snap["aap_file_promote_duration_ms"] != 42 {
		t.Fatalf("promote duration: %d", snap["aap_file_promote_duration_ms"])
	}
	if snap["aap_file_pending_upload_gauge"] != 3 || snap["aap_file_staging_orphan_bytes"] != 1024 {
		t.Fatalf("gauges: %#v", snap)
	}
	for key := range snap {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "file_id") || strings.Contains(lower, "filename") ||
			strings.Contains(lower, "download_token") {
			t.Fatalf("high-cardinality label leaked in key %q", key)
		}
	}
}

func TestCreateCompleteIncrementsMetrics(t *testing.T) {
	db := openMigratedDB(t)
	insertFixtures(t, db)
	repo, err := aapfile.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	staging := newFakeStaging()
	secure := &fakeSecure{}
	collector := metrics.NewAAPFileCollector()
	service, err := aapfile.NewService(repo, staging, secure, aapfile.WithMetrics(collector))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	intent, err := service.CreateUploadIntent(ctx, createCCInput(len(pngBytes), ""))
	if err != nil {
		t.Fatal(err)
	}
	staging.put(intent.File.StagingBucket, *intent.File.StagingObjectKey, pngBytes)
	if _, err := service.CompleteUpload(ctx, aapfile.CompleteUploadInput{
		Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
		FileID: intent.File.ID,
	}); err != nil {
		t.Fatal(err)
	}
	snap := collector.Snapshot()
	if snap["aap_file_create_total"] != 1 {
		t.Fatalf("create_total=%d", snap["aap_file_create_total"])
	}
	if snap["aap_file_complete_total"] != 1 {
		t.Fatalf("complete_total=%d", snap["aap_file_complete_total"])
	}
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
