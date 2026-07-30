package storedobject

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestChatContextSummaryBucketAndRetentionMatrix(t *testing.T) {
	if got, err := bucketForKind(KindChatContextSummary); err != nil || got != BucketExecutions {
		t.Fatalf("bucketForKind CHAT_CONTEXT_SUMMARY=%q err=%v", got, err)
	}
	if !requiresPermanentSensitiveContent(KindChatContextSummary) {
		t.Fatal("CHAT_CONTEXT_SUMMARY must require permanent sensitive content")
	}
	expires := time.Now().UTC().Add(time.Hour)
	bad := securePutInput(minioStoreObjectID, KindChatContextSummary, []byte("x"))
	bad.RetentionMode, bad.RetentionUntil = RetentionExpiring, &expires
	if err := validateRetentionPolicy(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expiring policy err=%v", err)
	}
	bad.RetentionMode, bad.RetentionUntil = RetentionPermanent, nil
	bad.Classification = ClassificationInternal
	if err := validateRetentionPolicy(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("internal policy err=%v", err)
	}
	// Valid permanent sensitive is allowed by policy (encryption happens in SecureStore).
	ok := securePutInput(minioStoreObjectID, KindChatContextSummary, []byte("summary body"))
	if err := validateRetentionPolicy(ok); err != nil {
		t.Fatalf("valid summary policy: %v", err)
	}
}

func TestChatContextSummarySecurePutEncryptedNoPresign(t *testing.T) {
	repository, _ := newMinIOStoreRepository(t)
	backend := newFakeBlobBackend()
	objects, err := newObjectStore(backend, repository, &recordingReadAuthorizer{}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := NewLocalChunkCipher("summary-kind-key-v1", objectEncryptionTestKey)
	if err != nil {
		t.Fatal(err)
	}
	secure, err := NewSecureStore(objects, cipher)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("encrypted chat context summary")
	created, err := secure.Put(context.Background(), securePutInput(minioStoreObjectID, KindChatContextSummary, body))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if created.Kind != KindChatContextSummary || created.EncryptionKeyID == "" ||
		created.RetentionMode != RetentionPermanent || created.Bucket != BucketExecutions {
		t.Fatalf("metadata=%+v", created)
	}
	if _, err := secure.PresignDownload(context.Background(), ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: minioStoreObjectID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	}, time.Minute); !errors.Is(err, ErrInvalid) {
		t.Fatalf("presign err=%v", err)
	}
	opened, err := secure.Open(context.Background(), ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: minioStoreObjectID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	read, err := io.ReadAll(opened.Body)
	_ = opened.Body.Close()
	if err != nil || !bytes.Equal(read, body) {
		t.Fatalf("roundtrip %q err=%v", read, err)
	}
	// Same-ID different content conflict path via ObjectStore.Put early Get
	if _, err := secure.Put(context.Background(), securePutInput(minioStoreObjectID, KindChatContextSummary, []byte("other"))); !errors.Is(err, ErrConflict) {
		t.Fatalf("second put err=%v want conflict", err)
	}
}
