package storedobject

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func TestAAPFileKindsExistAndNotForcedPermanent(t *testing.T) {
	for _, kind := range []string{KindAAPFile, KindAAPFileDerived} {
		if !validKind(kind) {
			t.Fatalf("validKind(%s) = false", kind)
		}
		if requiresPermanentSensitiveContent(kind) {
			t.Fatalf("%s must not be in requiresPermanentSensitiveContent", kind)
		}
		bucket, err := bucketForKind(kind)
		if err != nil || bucket != BucketAAPFiles {
			t.Fatalf("bucketForKind(%s)=%q err=%v want %q", kind, bucket, err, BucketAAPFiles)
		}
	}
	// v1 default policy: SENSITIVE + EXPIRING is accepted (not forced PERMANENT).
	expires := time.Now().UTC().Add(30 * 24 * time.Hour)
	for _, kind := range []string{KindAAPFile, KindAAPFileDerived} {
		input := PutInput{
			Kind: kind, Classification: ClassificationSensitive,
			RetentionMode: RetentionExpiring, RetentionUntil: &expires,
		}
		if err := validateRetentionPolicy(input); err != nil {
			t.Fatalf("AAP default EXPIRING policy for %s: %v", kind, err)
		}
	}
	// Forced-permanent kinds still reject EXPIRING (sanity: set is selective).
	if !requiresPermanentSensitiveContent(KindChatMessage) {
		t.Fatal("CHAT_MESSAGE should remain forced permanent")
	}
}

func TestAAPObjectKeyHelpers(t *testing.T) {
	workspaceID := "e18f1f2e-7b5a-7c3d-8e9f-123456789002"
	fileID := "e18f1f2e-7b5a-7c3d-8e9f-123456789099"
	objectID := "e18f1f2e-7b5a-7c3d-8e9f-123456789003"

	staging := AAPStagingObjectKey(workspaceID, fileID)
	wantStaging := workspaceID + "/aap-staging/" + fileID
	if staging != wantStaging {
		t.Fatalf("staging key = %q want %q", staging, wantStaging)
	}
	permanent := AAPPermanentObjectKey(workspaceID, objectID)
	wantPermanent := workspaceID + "/aap-file/" + objectID
	if permanent != wantPermanent {
		t.Fatalf("permanent key = %q want %q", permanent, wantPermanent)
	}

	// preparePut key shape for KindAAPFile must match design permanent key.
	repository, _ := newMinIOStoreRepository(t)
	store, err := newObjectStore(newFakeBlobBackend(), repository, &recordingReadAuthorizer{}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	meta, _, _, err := store.preparePut(PutInput{
		ID: objectID, WorkspaceID: workspaceID, Kind: KindAAPFile,
		ContentType: "image/png", SizeBytes: 1, SHA256: strings.Repeat("a", 64),
		Classification: ClassificationSensitive, RetentionMode: RetentionExpiring,
		RetentionUntil: &expiresForAAPTest, CreatedByType: CreatorSystem,
		CreatedByID: minioStoreOwnerID, Reader: strings.NewReader("x"),
	})
	if err != nil {
		t.Fatalf("preparePut AAP_FILE: %v", err)
	}
	if meta.Bucket != BucketAAPFiles || meta.ObjectKey != wantPermanent {
		t.Fatalf("preparePut metadata bucket/key = %q/%q want %q/%q",
			meta.Bucket, meta.ObjectKey, BucketAAPFiles, wantPermanent)
	}
}

var expiresForAAPTest = time.Now().UTC().Add(30 * 24 * time.Hour)

// TestMinIOPresignPutWithHeadersBindsContentLength exercises the real
// minioBlobBackend.PresignPutWithHeaders (PresignHeader), not a reimplementation.
func TestMinIOPresignPutWithHeadersBindsContentLength(t *testing.T) {
	client, err := minio.New("127.0.0.1:9000", &minio.Options{
		Creds:  credentials.NewStaticV4("actweave", "actweave-minio-dev", ""),
		Secure: false,
		// Region avoids a live bucket-location lookup so the unit test does not
		// require the AAP staging bucket to already exist.
		Region: "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := &minioBlobBackend{client: client}

	headers := make(http.Header)
	headers.Set("Content-Length", "12345")
	headers.Set("Content-Type", "image/png")
	signed, err := backend.PresignPutWithHeaders(
		context.Background(),
		BucketAAPStaging,
		AAPStagingObjectKey(minioStoreWorkspaceID, minioStoreObjectID),
		time.Minute,
		headers,
	)
	if err != nil {
		t.Fatalf("PresignPutWithHeaders: %v", err)
	}
	signedHeaders := strings.ToLower(signed.Query().Get("X-Amz-SignedHeaders"))
	if !strings.Contains(signedHeaders, "content-length") {
		t.Fatalf("X-Amz-SignedHeaders=%q missing content-length", signedHeaders)
	}
	if !strings.Contains(signedHeaders, "content-type") {
		t.Fatalf("X-Amz-SignedHeaders=%q missing content-type", signedHeaders)
	}

	// Reject missing Content-Length (unsigned PUT must not be a production path).
	if _, err := backend.PresignPutWithHeaders(
		context.Background(), BucketAAPStaging, "k", time.Minute, http.Header{},
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing Content-Length error = %v want ErrInvalid", err)
	}
	if _, err := backend.PresignPutWithHeaders(
		context.Background(), BucketAAPStaging, "k", time.Minute,
		http.Header{"Content-Type": []string{"image/png"}},
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Content-Type only error = %v want ErrInvalid", err)
	}

	// ObjectStore path also requires Content-Length and uses the backend.
	repository, _ := newMinIOStoreRepository(t)
	store, err := newObjectStore(backend, repository, &recordingReadAuthorizer{}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	storeURL, err := store.PresignPutWithHeaders(
		context.Background(), BucketAAPStaging,
		AAPStagingObjectKey(minioStoreWorkspaceID, minioStoreObjectID),
		5*time.Minute, headers,
	)
	if err != nil {
		t.Fatalf("ObjectStore.PresignPutWithHeaders: %v", err)
	}
	storeSigned := strings.ToLower(storeURL.Query().Get("X-Amz-SignedHeaders"))
	if !strings.Contains(storeSigned, "content-length") {
		t.Fatalf("store signed headers = %q missing content-length", storeSigned)
	}
	if _, err := store.PresignPutWithHeaders(
		context.Background(), BucketAAPStaging, "k", time.Minute, nil,
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("store missing Content-Length error = %v", err)
	}
}

func TestAAPBootstrapBuckets(t *testing.T) {
	buckets := AAPBootstrapBuckets()
	if len(buckets) != 2 || buckets[0] != BucketAAPStaging || buckets[1] != BucketAAPFiles {
		t.Fatalf("AAPBootstrapBuckets = %v", buckets)
	}
	if BucketAAPStaging != "actweave-aap-staging" || BucketAAPFiles != "actweave-aap-files" {
		t.Fatalf("bucket constants staging=%q files=%q", BucketAAPStaging, BucketAAPFiles)
	}
}
