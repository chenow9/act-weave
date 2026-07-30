package storedobject

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	minioStoreOwnerID     = "e18f1f2e-7b5a-7c3d-8e9f-123456789001"
	minioStoreWorkspaceID = "e18f1f2e-7b5a-7c3d-8e9f-123456789002"
	minioStoreObjectID    = "e18f1f2e-7b5a-7c3d-8e9f-123456789003"
	minioStoreMismatchID  = "e18f1f2e-7b5a-7c3d-8e9f-123456789004"
)

func TestMinIOStoreStreamsVerifiesAuthorizesAndSigns(t *testing.T) {
	repository, _ := newMinIOStoreRepository(t)
	backend := newFakeBlobBackend()
	authorizer := &recordingReadAuthorizer{}
	store, err := newObjectStore(backend, repository, authorizer, 1024)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"message":"permanent chat content"}`)
	input := minIOPutInput(minioStoreObjectID, payload)
	created, err := store.Put(context.Background(), input)
	if err != nil {
		t.Fatalf("put stored object: %v", err)
	}
	wantKey := minioStoreWorkspaceID + "/openapi-source/" + minioStoreObjectID
	if created.Bucket != BucketExecutions || created.ObjectKey != wantKey ||
		created.SizeBytes != int64(len(payload)) || backend.putCalls != 1 {
		t.Fatalf("unexpected stored object: %+v backend=%+v", created, backend)
	}

	request := ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: minioStoreObjectID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	}
	opened, err := store.Open(context.Background(), request)
	if err != nil {
		t.Fatalf("open stored object: %v", err)
	}
	read, err := io.ReadAll(opened.Body)
	closeErr := opened.Body.Close()
	if err != nil || closeErr != nil || !bytes.Equal(read, payload) {
		t.Fatalf("read verified object: content=%q err=%v close=%v", read, err, closeErr)
	}
	if authorizer.calls != 1 || authorizer.last.Classification != ClassificationInternal ||
		authorizer.last.Kind != KindOpenAPISource {
		t.Fatalf("read authorization context: %+v calls=%d", authorizer.last, authorizer.calls)
	}
	signed, err := store.PresignDownload(context.Background(), request, 5*time.Minute)
	if err != nil || signed.Scheme != "https" || signed.Query().Get("expires") != "300" {
		t.Fatalf("presign download: %v err=%v", signed, err)
	}
	if _, err := store.PresignDownload(context.Background(), request, 16*time.Minute); !errors.Is(err, ErrInvalid) {
		t.Fatalf("overlong signed URL error = %v", err)
	}
}

func TestMinIOStoreControlledBucketMapping(t *testing.T) {
	tests := map[string]string{
		KindOpenAPISource: BucketExecutions, KindPromptRunInput: BucketExecutions,
		KindPromptRunOutput: BucketExecutions, KindPromptPreviewInput: BucketExecutions,
		KindPromptPreviewOutput: BucketExecutions, KindModelTurn: BucketExecutions,
		KindChatMessage: BucketExecutions, KindToolInvocationPayload: BucketExecutions,
		KindExecutionCheckpoint: BucketExecutions, KindChatContextSummary: BucketExecutions,
		KindToolTestPayload: BucketToolTests,
		KindAuditEventPayload: BucketAuditPackages, KindAuditExport: BucketAuditPackages,
	}
	for kind, want := range tests {
		got, err := bucketForKind(kind)
		if err != nil || got != want {
			t.Fatalf("bucketForKind(%s)=%q,%v want %q", kind, got, err, want)
		}
	}
	if _, err := bucketForKind("CALLER_CONTROLLED_BUCKET"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kind bucket error = %v", err)
	}
}

func TestMinIOStoreRejectsMismatchAndLeavesNoMetadata(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload []byte
		size    int64
		hash    string
	}{
		{name: "hash mismatch", payload: []byte("payload"), size: 7, hash: strings.Repeat("0", 64)},
		{name: "declared stream has extra bytes", payload: []byte("payload-extra"), size: 7,
			hash: sha256Hex([]byte("payload"))},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, _ := newMinIOStoreRepository(t)
			backend := newFakeBlobBackend()
			store, _ := newObjectStore(backend, repository, &recordingReadAuthorizer{}, 1024)
			input := minIOPutInput(minioStoreMismatchID, test.payload)
			input.SizeBytes, input.SHA256 = test.size, test.hash
			if _, err := store.Put(context.Background(), input); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("integrity error = %v", err)
			}
			if backend.hasObject(input.WorkspaceID, input.ID) || backend.deleteCalls != 1 {
				t.Fatalf("mismatched object was not removed: %+v", backend)
			}
			if _, err := repository.Get(context.Background(), input.WorkspaceID, input.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("mismatched upload committed metadata: %v", err)
			}
		})
	}
}

func TestMinIOStoreAuthorizesBeforeStorageAccessAndVerifiesReads(t *testing.T) {
	repository, _ := newMinIOStoreRepository(t)
	backend := newFakeBlobBackend()
	store, _ := newObjectStore(backend, repository, &recordingReadAuthorizer{}, 1024)
	payload := []byte("verified payload")
	if _, err := store.Put(context.Background(), minIOPutInput(minioStoreObjectID, payload)); err != nil {
		t.Fatal(err)
	}

	denied := errors.New("read denied")
	deniedStore, _ := newObjectStore(backend, repository,
		&recordingReadAuthorizer{err: denied}, 1024)
	request := ReadRequest{WorkspaceID: minioStoreWorkspaceID, ObjectID: minioStoreObjectID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID}
	openCalls, statCalls := backend.openCalls, backend.statCalls
	if _, err := deniedStore.Open(context.Background(), request); !errors.Is(err, denied) {
		t.Fatalf("denied open error = %v", err)
	}
	if _, err := deniedStore.PresignDownload(context.Background(), request, time.Minute); !errors.Is(err, denied) {
		t.Fatalf("denied presign error = %v", err)
	}
	if backend.openCalls != openCalls || backend.statCalls != statCalls {
		t.Fatalf("storage accessed before authorization: open/stat=%d/%d", backend.openCalls, backend.statCalls)
	}

	backend.corruptReads = true
	opened, err := store.Open(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(opened.Body); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("corrupt streamed read error = %v", err)
	}
	_ = opened.Body.Close()
}

func TestMinIOStoreMetadataCommitRaceIsIdempotent(t *testing.T) {
	repository, _ := newMinIOStoreRepository(t)
	backend := newFakeBlobBackend()
	store, _ := newObjectStore(backend, repository, &recordingReadAuthorizer{}, 1024)
	payload := []byte("race payload")
	input := minIOPutInput(minioStoreObjectID, payload)
	backend.afterPut = func(bucket, key, checksum string, size int64) {
		backend.afterPut = nil
		_, err := repository.Create(context.Background(), CreateInput{
			ID: input.ID, WorkspaceID: input.WorkspaceID, Bucket: bucket, ObjectKey: key,
			Kind: input.Kind, ContentType: input.ContentType, SizeBytes: size, SHA256: checksum,
			Classification: input.Classification, RetentionMode: input.RetentionMode,
			CreatedByType: input.CreatedByType, CreatedByID: input.CreatedByID,
		})
		if err != nil {
			t.Errorf("insert racing metadata: %v", err)
		}
	}
	created, err := store.Put(context.Background(), input)
	if err != nil || created.ID != input.ID || backend.deleteCalls != 0 {
		t.Fatalf("idempotent metadata race: %+v backend=%+v err=%v", created, backend, err)
	}
	if _, err := store.Put(context.Background(), input); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeat committed put error = %v", err)
	}
	if backend.deleteCalls != 0 || !backend.hasObject(input.WorkspaceID, input.ID) {
		t.Fatal("repeat put removed the committed object")
	}
}

func TestMinIOStoreIntegration(t *testing.T) {
	repository, _ := newMinIOStoreRepository(t)
	config := MinIOConfig{
		Endpoint: "127.0.0.1:9000", AccessKey: "actweave",
		SecretKey: "actweave-minio-dev", MaxObjectBytes: 1 << 20,
	}
	client, err := minio.New(config.Endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	exists, err := client.BucketExists(context.Background(), BucketExecutions)
	if err != nil || !exists {
		t.Skipf("local MinIO integration bucket unavailable: exists=%v err=%v", exists, err)
	}
	store, err := NewMinIOStore(config, repository, &recordingReadAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("real MinIO streamed payload")
	input := minIOPutInput(minioStoreObjectID, payload)
	created, err := store.Put(context.Background(), input)
	if err != nil {
		t.Fatalf("real MinIO put: %v", err)
	}
	t.Cleanup(func() {
		_ = client.RemoveObject(context.Background(), created.Bucket, created.ObjectKey, minio.RemoveObjectOptions{})
	})
	opened, err := store.Open(context.Background(), ReadRequest{
		WorkspaceID: input.WorkspaceID, ObjectID: input.ID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	})
	if err != nil {
		t.Fatalf("real MinIO open: %v", err)
	}
	read, err := io.ReadAll(opened.Body)
	_ = opened.Body.Close()
	if err != nil || !bytes.Equal(read, payload) {
		t.Fatalf("real MinIO verified read: %q err=%v", read, err)
	}
	signed, err := store.PresignDownload(context.Background(), ReadRequest{
		WorkspaceID: input.WorkspaceID, ObjectID: input.ID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	}, time.Minute)
	if err != nil {
		t.Fatalf("real MinIO presign: %v", err)
	}
	response, err := http.Get(signed.String()) // #nosec G107 -- URL is generated by the local test MinIO.
	if err != nil {
		t.Fatalf("download signed object: %v", err)
	}
	defer response.Body.Close()
	downloaded, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(downloaded, payload) {
		t.Fatalf("signed download status/content=%d/%q err=%v", response.StatusCode, downloaded, err)
	}
}

type recordingReadAuthorizer struct {
	mu    sync.Mutex
	calls int
	last  ReadAuthorization
	err   error
}

func (authorizer *recordingReadAuthorizer) AuthorizeStoredObjectRead(
	_ context.Context,
	request ReadAuthorization,
) error {
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	authorizer.calls++
	authorizer.last = request
	return authorizer.err
}

type fakeBlob struct {
	content []byte
	info    blobInfo
}

type fakeBlobBackend struct {
	mu           sync.Mutex
	objects      map[string]fakeBlob
	putCalls     int
	openCalls    int
	statCalls    int
	deleteCalls  int
	abortCalls   int
	corruptReads bool
	afterPut     func(bucket, key, checksum string, size int64)
}

func newFakeBlobBackend() *fakeBlobBackend {
	return &fakeBlobBackend{objects: make(map[string]fakeBlob)}
}

func (backend *fakeBlobBackend) Put(
	_ context.Context,
	bucket, key string,
	reader io.Reader,
	size int64,
	_ string,
	checksum string,
) (blobUpload, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.putCalls++
	physicalKey := bucket + "/" + key
	if _, exists := backend.objects[physicalKey]; exists {
		return blobUpload{}, errors.New("object already exists")
	}
	var buffer bytes.Buffer
	written, err := io.CopyN(&buffer, reader, size)
	if err != nil && !(size == 0 && errors.Is(err, io.EOF)) {
		return blobUpload{}, err
	}
	backend.objects[physicalKey] = fakeBlob{
		content: append([]byte(nil), buffer.Bytes()...),
		info:    blobInfo{Size: written, SHA256: checksum},
	}
	if backend.afterPut != nil {
		backend.afterPut(bucket, key, checksum, written)
	}
	return blobUpload{Size: written}, nil
}

func (backend *fakeBlobBackend) Open(
	_ context.Context,
	bucket, key string,
) (io.ReadCloser, blobInfo, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.openCalls++
	value, exists := backend.objects[bucket+"/"+key]
	if !exists {
		return nil, blobInfo{}, errors.New("not found")
	}
	content := append([]byte(nil), value.content...)
	if backend.corruptReads && len(content) > 0 {
		content[0] ^= 0xff
	}
	return io.NopCloser(bytes.NewReader(content)), value.info, nil
}

func (backend *fakeBlobBackend) Stat(_ context.Context, bucket, key string) (blobInfo, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.statCalls++
	value, exists := backend.objects[bucket+"/"+key]
	if !exists {
		return blobInfo{}, errors.New("not found")
	}
	return value.info, nil
}

func (backend *fakeBlobBackend) Abort(context.Context, string, string) error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.abortCalls++
	return nil
}

func (backend *fakeBlobBackend) Delete(_ context.Context, bucket, key string) error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.deleteCalls++
	delete(backend.objects, bucket+"/"+key)
	return nil
}

func (backend *fakeBlobBackend) PresignGet(
	_ context.Context,
	bucket, key string,
	ttl time.Duration,
) (*url.URL, error) {
	return url.Parse(fmt.Sprintf("https://objects.example.test/%s/%s?expires=%d",
		bucket, key, int(ttl.Seconds())))
}

func (backend *fakeBlobBackend) hasObject(workspaceID, objectID string) bool {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	key := BucketExecutions + "/" + workspaceID + "/openapi-source/" + objectID
	_, exists := backend.objects[key]
	return exists
}

func minIOPutInput(id string, payload []byte) PutInput {
	return PutInput{
		ID: id, WorkspaceID: minioStoreWorkspaceID, Kind: KindOpenAPISource,
		ContentType: "application/json", SizeBytes: int64(len(payload)),
		SHA256: sha256Hex(payload), Classification: ClassificationInternal,
		RetentionMode: RetentionPermanent,
		CreatedByType: CreatorUser, CreatedByID: minioStoreOwnerID,
		Reader: bytes.NewReader(payload),
	}
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func newMinIOStoreRepository(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name)
		VALUES($1,'minio.store.owner','MinIO Store Owner')`, minioStoreOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(
		id,slug,display_name,mode,owner_user_id,created_by,updated_by
		) VALUES($1,'minio-store','MinIO Store','PRODUCTION',$2,$2,$2)`,
		minioStoreWorkspaceID, minioStoreOwnerID); err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db
}
