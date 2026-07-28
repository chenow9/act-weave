package storedobject

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"testing"
	"time"

	"actweave/backend/internal/authz"
	"github.com/google/uuid"
)

func TestPromptPreviewPutOpenPromotePurgeLifecycle(t *testing.T) {
	repository, db := newMinIOStoreRepository(t)
	backend := newFakeBlobBackend()
	objects, err := newObjectStore(backend, repository, &recordingReadAuthorizer{}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := NewLocalChunkCipher("preview-lifecycle-key-v1", objectEncryptionTestKey)
	if err != nil {
		t.Fatal(err)
	}
	secure, err := NewSecureStore(objects, cipher)
	if err != nil {
		t.Fatal(err)
	}

	inputID := uuid.Must(uuid.NewV7()).String()
	outputID := uuid.Must(uuid.NewV7()).String()
	payloadIn := []byte("preview input body canary-PREVIEW-IN")
	payloadOut := []byte("preview output body canary-PREVIEW-OUT")
	expires := time.Now().UTC().Add(30 * 24 * time.Hour)

	putPreview := func(id, kind string, payload []byte) StoredObject {
		t.Helper()
		input := securePutInput(id, kind, payload)
		input.RetentionMode = RetentionExpiring
		input.RetentionUntil = &expires
		created, putErr := secure.Put(context.Background(), input)
		if putErr != nil {
			t.Fatalf("put %s: %v", kind, putErr)
		}
		if created.RetentionMode != RetentionExpiring || created.RetentionUntil == nil ||
			created.EncryptionKeyID == "" || created.BodyPurgedAt != nil {
			t.Fatalf("unexpected preview metadata: %+v", created)
		}
		return created
	}

	_ = putPreview(inputID, KindPromptPreviewInput, payloadIn)
	_ = putPreview(outputID, KindPromptPreviewOutput, payloadOut)

	// General Open/Presign must fail closed for preview kinds (even unexpired).
	if _, err := secure.Open(context.Background(), ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: outputID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("preview open error = %v want ErrNotFound", err)
	}
	if _, err := secure.PresignDownload(context.Background(), ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: outputID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	}, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("preview presign error = %v want ErrNotFound", err)
	}

	// Illegal retention policies fail before write.
	bad := securePutInput(uuid.Must(uuid.NewV7()).String(), KindPromptPreviewInput, payloadIn)
	bad.RetentionMode = RetentionPermanent
	if err := validateRetentionPolicy(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("permanent preview put policy error = %v", err)
	}
	bad.RetentionMode = RetentionExpiring
	bad.RetentionUntil = &expires
	bad.Classification = ClassificationInternal
	if err := validateRetentionPolicy(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("internal preview put policy error = %v", err)
	}

	// Promote output once inside a transaction.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	promoted, err := repository.PromotePreviewInTx(context.Background(), tx, minioStoreWorkspaceID, outputID)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("promote preview: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if promoted.RetentionMode != RetentionPermanent || promoted.RetentionUntil != nil ||
		promoted.BodyPurgedAt != nil || promoted.Kind != KindPromptPreviewOutput {
		t.Fatalf("unexpected promoted metadata: %+v", promoted)
	}

	// Second promote fails; permanent still not general-downloadable.
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PromotePreviewInTx(context.Background(), tx, minioStoreWorkspaceID, outputID); !errors.Is(err, ErrConflict) {
		_ = tx.Rollback()
		t.Fatalf("second promote error = %v", err)
	}
	_ = tx.Rollback()
	if _, err := secure.Open(context.Background(), ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: outputID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("promoted preview still openable: %v", err)
	}

	// Non-preview cannot use promote.
	permanentID := uuid.Must(uuid.NewV7()).String()
	permanentPayload := []byte("permanent prompt input")
	if _, err := secure.Put(context.Background(), securePutInput(permanentID, KindPromptRunInput, permanentPayload)); err != nil {
		t.Fatal(err)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PromotePreviewInTx(context.Background(), tx, minioStoreWorkspaceID, permanentID); !errors.Is(err, ErrConflict) {
		_ = tx.Rollback()
		t.Fatalf("promote permanent kind error = %v", err)
	}
	_ = tx.Rollback()

	// Expire the unpromoted input and purge body; open still denied; purge is idempotent.
	forcePreviewExpired(t, db, inputID)
	if err := objects.PurgeBody(context.Background(), minioStoreWorkspaceID, inputID); err != nil {
		t.Fatalf("purge body: %v", err)
	}
	purged, err := repository.Get(context.Background(), minioStoreWorkspaceID, inputID)
	if err != nil || purged.BodyPurgedAt == nil {
		t.Fatalf("expected body_purged_at set: %+v err=%v", purged, err)
	}
	if backend.hasPhysicalObject(purged.Bucket, purged.ObjectKey) {
		t.Fatal("expected blob deleted after purge")
	}
	if err := objects.PurgeBody(context.Background(), minioStoreWorkspaceID, inputID); err != nil {
		t.Fatalf("idempotent purge: %v", err)
	}
	// Missing blob still succeeds finalize path via second object without blob.
	orphanID := uuid.Must(uuid.NewV7()).String()
	orphan := putPreview(orphanID, KindPromptPreviewInput, []byte("orphan preview"))
	forcePreviewExpired(t, db, orphanID)
	backend.mu.Lock()
	delete(backend.objects, orphan.Bucket+"/"+orphan.ObjectKey)
	backend.mu.Unlock()
	if err := objects.PurgeBody(context.Background(), minioStoreWorkspaceID, orphanID); err != nil {
		t.Fatalf("purge absent blob: %v", err)
	}

	// Permanent content still readable.
	opened, err := secure.Open(context.Background(), ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: permanentID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	})
	if err != nil {
		t.Fatalf("permanent open: %v", err)
	}
	read, err := io.ReadAll(opened.Body)
	_ = opened.Body.Close()
	if err != nil || !bytes.Equal(read, permanentPayload) {
		t.Fatalf("permanent content mismatch: %q err=%v", read, err)
	}
}

func TestPromptPreviewRejectsExpiredNonPreviewExpiringReads(t *testing.T) {
	// Audit export is EXPIRING and was previously readable until retention end.
	// After expiry, authorizedMetadata must fail closed before blob open.
	repository, _ := newMinIOStoreRepository(t)
	backend := newFakeBlobBackend()
	objects, err := newObjectStore(backend, repository, &recordingReadAuthorizer{}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.Must(uuid.NewV7()).String()
	payload := []byte("audit export bytes")
	expires := time.Now().UTC().Add(30 * time.Millisecond)
	input := minIOPutInput(id, payload)
	input.Kind = KindAuditExport
	input.ContentType = "application/zip"
	input.RetentionMode = RetentionExpiring
	input.RetentionUntil = &expires
	if _, err := objects.Put(context.Background(), input); err != nil {
		t.Fatalf("put audit export: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		meta, getErr := repository.Get(context.Background(), minioStoreWorkspaceID, id)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if meta.BodyUnavailable(time.Now().UTC()) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := objects.Open(context.Background(), ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: id,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired audit open error = %v", err)
	}
}

func TestWorkspaceReadAuthorizerDeniesPreviewKinds(t *testing.T) {
	authorizer, err := NewWorkspaceReadAuthorizer(&previewAlwaysAllowWorkspace{})
	if err != nil {
		t.Fatal(err)
	}
	err = authorizer.AuthorizeStoredObjectRead(context.Background(), ReadAuthorization{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: uuid.Must(uuid.NewV7()).String(),
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
		Classification: ClassificationSensitive, Kind: KindPromptPreviewOutput,
	})
	if !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("preview kind authorize error = %v", err)
	}
}

func forcePreviewExpired(t *testing.T, db *sql.DB, objectID string) {
	t.Helper()
	if _, err := db.Exec(`ALTER TABLE stored_objects DISABLE TRIGGER stored_objects_metadata_guard`); err != nil {
		t.Fatal(err)
	}
	// Keep retention_until > created_at (CHECK) while placing expiry in the past.
	if _, err := db.Exec(`
		UPDATE stored_objects
		SET created_at=clock_timestamp() - interval '2 hours',
			retention_until=clock_timestamp() - interval '1 hour'
		WHERE id=$1 AND kind IN ('PROMPT_PREVIEW_INPUT','PROMPT_PREVIEW_OUTPUT')
	`, objectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE stored_objects ENABLE TRIGGER stored_objects_metadata_guard`); err != nil {
		t.Fatal(err)
	}
}

func (backend *fakeBlobBackend) hasPhysicalObject(bucket, key string) bool {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	_, exists := backend.objects[bucket+"/"+key]
	return exists
}

type previewAlwaysAllowWorkspace struct{}

func (previewAlwaysAllowWorkspace) AuthorizeWorkspace(
	context.Context, string, string, authz.Action,
) (authz.WorkspaceContext, error) {
	return authz.WorkspaceContext{}, nil
}
