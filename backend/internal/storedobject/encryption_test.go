package storedobject

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
)

var objectEncryptionTestKey = []byte("0123456789abcdef0123456789abcdef")

func TestEncryptionStreamsRandomizedAuthenticatedCiphertext(t *testing.T) {
	cipher, err := NewLocalChunkCipher("object-local-v1", objectEncryptionTestKey)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("streamed-sensitive-payload/"), 7000)
	binding := CipherBinding{WorkspaceID: minioStoreWorkspaceID,
		ObjectID: minioStoreObjectID, Kind: KindModelTurn}
	first := encryptForTest(t, cipher, binding, payload)
	second := encryptForTest(t, cipher, binding, payload)
	if bytes.Equal(first, second) || bytes.Contains(first, payload[:1024]) {
		t.Fatal("object encryption reused ciphertext or exposed plaintext")
	}
	decrypted, err := cipher.Decrypt(context.Background(), binding, io.NopCloser(bytes.NewReader(first)))
	if err != nil {
		t.Fatal(err)
	}
	read, err := io.ReadAll(decrypted)
	_ = decrypted.Close()
	if err != nil || !bytes.Equal(read, payload) {
		t.Fatalf("decrypt streamed object: bytes=%d err=%v", len(read), err)
	}
	wrongBinding := binding
	wrongBinding.ObjectID = minioStoreMismatchID
	wrong, err := cipher.Decrypt(context.Background(), wrongBinding, io.NopCloser(bytes.NewReader(first)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(wrong); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong binding decryption error = %v", err)
	}
	_ = wrong.Close()
}

func TestEncryptionSecureStoreRoundTripAndKeyRotation(t *testing.T) {
	repository, _ := newMinIOStoreRepository(t)
	backend := newFakeBlobBackend()
	objects, _ := newObjectStore(backend, repository, &recordingReadAuthorizer{}, 1<<20)
	oldCipher, _ := NewLocalChunkCipher("object-local-v1", objectEncryptionTestKey)
	secure, err := NewSecureStore(objects, oldCipher)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("private model turn"), 8000)
	input := securePutInput(minioStoreObjectID, KindModelTurn, payload)
	created, err := secure.Put(context.Background(), input)
	if err != nil {
		t.Fatalf("put encrypted permanent object: %v", err)
	}
	if created.EncryptionKeyID != oldCipher.KeyID() ||
		created.RetentionMode != RetentionPermanent || created.SHA256 == input.SHA256 ||
		created.SizeBytes <= input.SizeBytes {
		t.Fatalf("unexpected encrypted metadata: %+v", created)
	}
	raw := backend.objectContent(created.Bucket, created.ObjectKey)
	if len(raw) == 0 || bytes.Contains(raw, payload[:1024]) {
		t.Fatal("object backend contains plaintext")
	}

	newCipher, _ := NewLocalChunkCipher("object-local-v2", []byte("abcdef0123456789abcdef0123456789"))
	rotated, err := NewSecureStore(objects, newCipher, oldCipher)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := rotated.Open(context.Background(), ReadRequest{
		WorkspaceID: input.WorkspaceID, ObjectID: input.ID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := io.ReadAll(opened.Body)
	_ = opened.Body.Close()
	if err != nil || !bytes.Equal(plaintext, payload) {
		t.Fatalf("read old key after rotation: bytes=%d err=%v", len(plaintext), err)
	}
	if _, err := rotated.PresignDownload(context.Background(), ReadRequest{
		WorkspaceID: input.WorkspaceID, ObjectID: input.ID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	}, time.Minute); !errors.Is(err, ErrInvalid) {
		t.Fatalf("encrypted raw URL error = %v", err)
	}
}

func TestEncryptionRejectsPlaintextHashMismatchAndTampering(t *testing.T) {
	repository, _ := newMinIOStoreRepository(t)
	backend := newFakeBlobBackend()
	objects, _ := newObjectStore(backend, repository, &recordingReadAuthorizer{}, 1<<20)
	cipher, _ := NewLocalChunkCipher("object-local-v1", objectEncryptionTestKey)
	secure, _ := NewSecureStore(objects, cipher)
	payload := []byte("sensitive tool invocation")
	bad := securePutInput(minioStoreMismatchID, KindToolInvocationPayload, payload)
	bad.SHA256 = strings.Repeat("0", 64)
	if _, err := secure.Put(context.Background(), bad); !errors.Is(err, ErrObjectStorage) &&
		!errors.Is(err, ErrIntegrity) {
		t.Fatalf("plaintext hash mismatch error = %v", err)
	}
	if _, err := repository.Get(context.Background(), bad.WorkspaceID, bad.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hash mismatch committed metadata: %v", err)
	}

	good := securePutInput(minioStoreObjectID, KindToolInvocationPayload, payload)
	created, err := secure.Put(context.Background(), good)
	if err != nil {
		t.Fatal(err)
	}
	backend.tamperObject(created.Bucket, created.ObjectKey)
	opened, err := secure.Open(context.Background(), ReadRequest{
		WorkspaceID: good.WorkspaceID, ObjectID: good.ID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	})
	if err == nil {
		_, err = io.ReadAll(opened.Body)
		_ = opened.Body.Close()
	}
	if !errors.Is(err, ErrIntegrity) && !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered ciphertext open error = %v", err)
	}
}

func TestRetentionPolicyForcesPermanentSensitiveBusinessContent(t *testing.T) {
	for _, kind := range []string{
		KindPromptRunInput, KindPromptRunOutput, KindModelTurn, KindChatMessage,
		KindToolTestPayload, KindToolInvocationPayload, KindExecutionCheckpoint,
	} {
		base := securePutInput(minioStoreObjectID, kind, []byte("content"))
		expires := time.Now().UTC().Add(time.Hour)
		base.RetentionMode, base.RetentionUntil = RetentionExpiring, &expires
		if err := validateRetentionPolicy(base); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s expiring policy error = %v", kind, err)
		}
		base.RetentionMode, base.RetentionUntil = RetentionPermanent, nil
		base.Classification = ClassificationInternal
		if err := validateRetentionPolicy(base); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s internal policy error = %v", kind, err)
		}
	}
	expires := time.Now().UTC().Add(time.Hour)
	audit := securePutInput(minioStoreObjectID, KindAuditExport, []byte("export"))
	audit.Classification = ClassificationInternal
	audit.RetentionMode, audit.RetentionUntil = RetentionExpiring, &expires
	if err := validateRetentionPolicy(audit); err != nil {
		t.Fatalf("valid expiring audit export: %v", err)
	}
	audit.RetentionMode, audit.RetentionUntil = RetentionPermanent, nil
	if err := validateRetentionPolicy(audit); !errors.Is(err, ErrInvalid) {
		t.Fatalf("permanent audit export error = %v", err)
	}
}

func TestRetentionSecurityPolicyMigration(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 3 || version.Dirty {
		t.Fatalf("migration version = %+v", version)
	}
	db := testDatabase.Open(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name)
		VALUES($1,'retention.owner','Retention Owner')`, minioStoreOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(
		id,slug,display_name,mode,owner_user_id,created_by,updated_by
		) VALUES($1,'retention-policy','Retention Policy','PRODUCTION',$2,$2,$2)`,
		minioStoreWorkspaceID, minioStoreOwnerID); err != nil {
		t.Fatal(err)
	}
	for _, constraint := range []string{
		"stored_objects_classification_encryption_check",
		"stored_objects_permanent_content_policy_check",
		"stored_objects_openapi_retention_check",
		"stored_objects_audit_export_retention_check",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname=$1)`, constraint).Scan(&exists); err != nil || !exists {
			t.Fatalf("security constraint %s: exists=%v err=%v", constraint, exists, err)
		}
	}
	invalid := []string{
		`INSERT INTO stored_objects(id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,classification,retention_mode,created_by_type,created_by_id)
		 VALUES('e18f1f2e-7b5a-7c3d-8e9f-123456789010','` + minioStoreWorkspaceID + `','actweave-executions','bad/chat','CHAT_MESSAGE','text/plain',1,'` + strings.Repeat("a", 64) + `','SENSITIVE','PERMANENT','USER','` + minioStoreOwnerID + `')`,
		`INSERT INTO stored_objects(id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,classification,retention_mode,retention_until,created_by_type,created_by_id)
		 VALUES('e18f1f2e-7b5a-7c3d-8e9f-123456789011','` + minioStoreWorkspaceID + `','actweave-executions','bad/prompt','PROMPT_RUN_INPUT','text/plain',1,'` + strings.Repeat("a", 64) + `','INTERNAL','EXPIRING',clock_timestamp()+interval '1 day','USER','` + minioStoreOwnerID + `')`,
	}
	for _, statement := range invalid {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("expected policy statement failure: %s", statement)
		}
	}
}

func encryptForTest(
	t *testing.T,
	cipher StreamCipher,
	binding CipherBinding,
	payload []byte,
) []byte {
	t.Helper()
	encrypted, err := cipher.Encrypt(context.Background(), binding,
		bytes.NewReader(payload), int64(len(payload)), sha256Hex(payload))
	if err != nil {
		t.Fatal(err)
	}
	read, err := io.ReadAll(encrypted.Reader)
	_ = encrypted.Reader.Close()
	if err != nil || int64(len(read)) != encrypted.Size {
		t.Fatalf("encrypt stream: bytes=%d size=%d err=%v", len(read), encrypted.Size, err)
	}
	return read
}

func securePutInput(id, kind string, payload []byte) PutInput {
	return PutInput{
		ID: id, WorkspaceID: minioStoreWorkspaceID, Kind: kind,
		ContentType: "application/json", SizeBytes: int64(len(payload)),
		SHA256: sha256Hex(payload), Classification: ClassificationSensitive,
		RetentionMode: RetentionPermanent, CreatedByType: CreatorUser,
		CreatedByID: minioStoreOwnerID, Reader: bytes.NewReader(payload),
	}
}

func (backend *fakeBlobBackend) objectContent(bucket, key string) []byte {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return append([]byte(nil), backend.objects[bucket+"/"+key].content...)
}

func (backend *fakeBlobBackend) tamperObject(bucket, key string) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	physicalKey := bucket + "/" + key
	value := backend.objects[physicalKey]
	if len(value.content) > 0 {
		value.content[len(value.content)-1] ^= 0xff
	}
	backend.objects[physicalKey] = value
}
