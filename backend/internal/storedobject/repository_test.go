package storedobject_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/storedobject"
	"github.com/google/uuid"
)

const (
	storedObjectOwnerID          = "d18f1f2e-7b5a-7c3d-8e9f-123456789001"
	storedObjectWorkspaceID      = "d18f1f2e-7b5a-7c3d-8e9f-123456789002"
	storedObjectOtherWorkspaceID = "d18f1f2e-7b5a-7c3d-8e9f-123456789003"
	storedObjectPermanentID      = "d18f1f2e-7b5a-7c3d-8e9f-123456789004"
	storedObjectExpiringID       = "d18f1f2e-7b5a-7c3d-8e9f-123456789005"
	storedObjectDuplicateID      = "d18f1f2e-7b5a-7c3d-8e9f-123456789006"
	storedObjectSHA              = "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
)

func TestStoredObjectMigration(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateTo(t, 26)
	if !version.Applied || version.Number != 26 || version.Dirty {
		t.Fatalf("migration version = %+v", version)
	}
	db := testDatabase.Open(t)
	insertStoredObjectWorkspaces(t, db)
	assertStoredObjectSchema(t, db)
	assertStoredObjectConstraints(t, db)

	version = testDatabase.MigrateTo(t, 25)
	if !version.Applied || version.Number != 25 || version.Dirty {
		t.Fatalf("rollback version = %+v", version)
	}
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('public.stored_objects') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("stored_objects remained after rollback: exists=%v err=%v", exists, err)
	}
	version = testDatabase.MigrateTo(t, 26)
	if !version.Applied || version.Number != 26 || version.Dirty {
		t.Fatalf("reapply version = %+v", version)
	}
}

func TestStoredObjectRepositoryWorkspaceIsolationAndRetention(t *testing.T) {
	repository, db := newStoredObjectRepository(t)
	ctx := context.Background()
	permanent, err := repository.Create(ctx, permanentStoredObjectInput(storedObjectPermanentID, "chat/message-1.json"))
	if err != nil {
		t.Fatalf("create permanent object metadata: %v", err)
	}
	if permanent.RetentionMode != storedobject.RetentionPermanent ||
		permanent.RetentionUntil != nil || permanent.SHA256 != storedObjectSHA ||
		permanent.CreatedAt.IsZero() {
		t.Fatalf("unexpected permanent object: %+v", permanent)
	}
	retentionUntil := time.Now().UTC().Add(time.Hour)
	expiringInput := permanentStoredObjectInput(storedObjectExpiringID, "audit/export-1.zip")
	expiringInput.Kind = storedobject.KindAuditExport
	expiringInput.ContentType = "application/zip"
	expiringInput.Classification = storedobject.ClassificationInternal
	expiringInput.EncryptionKeyID = ""
	expiringInput.RetentionMode = storedobject.RetentionExpiring
	expiringInput.RetentionUntil = &retentionUntil
	expiring, err := repository.Create(ctx, expiringInput)
	if err != nil || expiring.RetentionUntil == nil ||
		!expiring.RetentionUntil.Equal(retentionUntil.Truncate(time.Microsecond)) {
		t.Fatalf("create expiring object metadata: %+v err=%v", expiring, err)
	}

	if _, err := repository.Get(ctx, storedObjectOtherWorkspaceID, permanent.ID); !errors.Is(err, storedobject.ErrNotFound) {
		t.Fatalf("cross-workspace get error = %v", err)
	}
	byKey, err := repository.GetByKey(ctx, storedObjectWorkspaceID, permanent.Bucket, permanent.ObjectKey)
	if err != nil || byKey.ID != permanent.ID {
		t.Fatalf("get object by scoped key: %+v err=%v", byKey, err)
	}
	listed, err := repository.List(ctx, storedobject.ListInput{
		WorkspaceID:    storedObjectWorkspaceID,
		RetentionMode:  storedobject.RetentionPermanent,
		Classification: storedobject.ClassificationSensitive,
		Limit:          20,
	})
	if err != nil || len(listed) != 1 || listed[0].ID != permanent.ID {
		t.Fatalf("list permanent sensitive objects: %+v err=%v", listed, err)
	}

	duplicate := permanentStoredObjectInput(storedObjectDuplicateID, permanent.ObjectKey)
	duplicate.WorkspaceID = storedObjectOtherWorkspaceID
	if _, err := repository.Create(ctx, duplicate); !errors.Is(err, storedobject.ErrConflict) {
		t.Fatalf("duplicate physical object key error = %v", err)
	}
	if _, err := db.Exec(`UPDATE stored_objects SET size_bytes=size_bytes+1 WHERE id=$1`, permanent.ID); err == nil {
		t.Fatal("stored object metadata update was allowed")
	}
	if _, err := db.Exec(`DELETE FROM stored_objects WHERE id=$1`, permanent.ID); err == nil {
		t.Fatal("permanent stored object metadata deletion was allowed")
	}
	if _, err := repository.Create(ctx, storedobject.CreateInput{
		ID: storedObjectDuplicateID, WorkspaceID: storedObjectWorkspaceID,
		Bucket: "actweave-objects", ObjectKey: "../escape",
		Kind: storedobject.KindChatMessage, ContentType: "application/json",
		SHA256: storedObjectSHA, Classification: storedobject.ClassificationSensitive,
		RetentionMode: storedobject.RetentionPermanent,
		CreatedByType: storedobject.CreatorUser, CreatedByID: storedObjectOwnerID,
	}); !errors.Is(err, storedobject.ErrInvalid) {
		t.Fatalf("unsafe object key error = %v", err)
	}
	invalidPermanent := permanentStoredObjectInput(storedObjectDuplicateID, "chat/invalid.json")
	invalidPermanent.RetentionUntil = &retentionUntil
	if _, err := repository.Create(ctx, invalidPermanent); !errors.Is(err, storedobject.ErrInvalid) {
		t.Fatalf("permanent retention_until error = %v", err)
	}
}

func TestStoredObjectRepositoryConcurrentPhysicalKeyUniqueness(t *testing.T) {
	repository, db := newStoredObjectRepository(t)
	db.SetMaxOpenConns(16)
	ctx := context.Background()
	const writers = 8
	results := make(chan error, writers)
	var wait sync.WaitGroup
	for range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			id := uuid.Must(uuid.NewV7()).String()
			_, err := repository.Create(ctx, permanentStoredObjectInput(id, "tool/shared-payload.json"))
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, storedobject.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent create error: %v", err)
		}
	}
	if successes != 1 || conflicts != writers-1 {
		t.Fatalf("concurrent creates successes/conflicts=%d/%d", successes, conflicts)
	}
}

func permanentStoredObjectInput(id, key string) storedobject.CreateInput {
	return storedobject.CreateInput{
		ID: id, WorkspaceID: storedObjectWorkspaceID,
		Bucket: "actweave-objects", ObjectKey: key,
		Kind: storedobject.KindChatMessage, ContentType: "application/json",
		SizeBytes: 42, SHA256: strings.ToUpper(storedObjectSHA),
		EncryptionKeyID: "metadata-test-key-v1",
		Classification:  storedobject.ClassificationSensitive,
		RetentionMode:   storedobject.RetentionPermanent,
		CreatedByType:   storedobject.CreatorUser, CreatedByID: storedObjectOwnerID,
	}
}

func newStoredObjectRepository(t *testing.T) (*storedobject.Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertStoredObjectWorkspaces(t, db)
	repository, err := storedobject.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db
}

func insertStoredObjectWorkspaces(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO users(id,username,display_name)
		VALUES($1,'stored.object.owner','Stored Object Owner')
	`, storedObjectOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES
		 ($1,'stored-object','Stored Object','PRODUCTION',$3,$3,$3),
		 ($2,'stored-object-other','Stored Object Other','SANDBOX',$3,$3,$3)
	`, storedObjectWorkspaceID, storedObjectOtherWorkspaceID, storedObjectOwnerID); err != nil {
		t.Fatal(err)
	}
}

func assertStoredObjectSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, index := range []string{
		"stored_objects_bucket_key_key",
		"stored_objects_workspace_kind_created_idx",
		"stored_objects_workspace_classification_created_idx",
		"stored_objects_expiring_retention_idx",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+index).Scan(&exists); err != nil || !exists {
			t.Fatalf("stored object index %s: exists=%v err=%v", index, exists, err)
		}
	}
	var deletedAt bool
	if err := db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='stored_objects' AND column_name='deleted_at')
	`).Scan(&deletedAt); err != nil || deletedAt {
		t.Fatalf("stored_objects deleted_at: exists=%v err=%v", deletedAt, err)
	}
}

func assertStoredObjectConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	base := permanentStoredObjectInput(storedObjectPermanentID, "migration/permanent.json")
	if _, err := db.Exec(`
		INSERT INTO stored_objects(
		 id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
		 classification,retention_mode,retention_until,created_by_type,created_by_id
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULL,$11,$12)
	`, base.ID, base.WorkspaceID, base.Bucket, base.ObjectKey, base.Kind,
		base.ContentType, base.SizeBytes, strings.ToLower(base.SHA256),
		base.Classification, base.RetentionMode, base.CreatedByType, base.CreatedByID); err != nil {
		t.Fatalf("insert permanent stored object: %v", err)
	}
	for _, statement := range []string{
		`UPDATE stored_objects SET object_key='changed' WHERE id='` + storedObjectPermanentID + `'`,
		`DELETE FROM stored_objects WHERE id='` + storedObjectPermanentID + `'`,
		`INSERT INTO stored_objects(id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,classification,retention_mode,retention_until,created_by_type,created_by_id)
		 VALUES('d18f1f2e-7b5a-7c3d-8e9f-123456789010','` + storedObjectWorkspaceID + `','actweave-objects','bad/permanent','CHAT_MESSAGE','text/plain',1,'` + storedObjectSHA + `','SENSITIVE','PERMANENT',clock_timestamp()+interval '1 day','USER','` + storedObjectOwnerID + `')`,
		`INSERT INTO stored_objects(id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,classification,retention_mode,retention_until,created_by_type,created_by_id)
		 VALUES('d18f1f2e-7b5a-7c3d-8e9f-123456789011','` + storedObjectWorkspaceID + `','actweave-objects','bad/hash','CHAT_MESSAGE','text/plain',1,'bad','SENSITIVE','EXPIRING',clock_timestamp()+interval '1 day','USER','` + storedObjectOwnerID + `')`,
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("expected stored object statement to fail: %s", statement)
		}
	}
}
