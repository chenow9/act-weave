package storedobject

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	BucketExecutions              = "actweave-executions"
	BucketAuditPackages           = "actweave-audit-packages"
	BucketToolTests               = "actweave-tool-tests"
	BucketConnectionVerifications = "actweave-connection-verifications"
	// BucketAAPStaging holds short-lived plaintext client uploads before
	// promote. Objects here are not encrypted; GC reaps abandoned keys.
	BucketAAPStaging = "actweave-aap-staging"
	// BucketAAPFiles holds permanent AAP_FILE / AAP_FILE_DERIVED objects
	// written exclusively via SecureStore after promote (never direct client PUT).
	BucketAAPFiles          = "actweave-aap-files"
	defaultMaxObjectBytes   = int64(512 << 20)
	maxPresignedDownloadTTL = 15 * time.Minute
	maxPresignedUploadTTL   = 15 * time.Minute
	objectSHA256MetadataKey = "actweave-sha256"
)

var (
	ErrObjectStorage = errors.New("stored object storage failure")
	ErrIntegrity     = errors.New("stored object integrity mismatch")
)

type MinIOConfig struct {
	Endpoint       string
	AccessKey      string
	SecretKey      string
	UseSSL         bool
	Region         string
	MaxObjectBytes int64
}

type PutInput struct {
	ID                 string
	WorkspaceID        string
	Kind               string
	ContentType        string
	StorageContentType string
	SizeBytes          int64
	SHA256             string
	EncryptionKeyID    string
	Classification     string
	RetentionMode      string
	RetentionUntil     *time.Time
	CreatedByType      string
	CreatedByID        string
	Reader             io.Reader
}

type ReadRequest struct {
	WorkspaceID string
	ObjectID    string
	ActorType   string
	ActorID     string
}

type ReadAuthorization struct {
	WorkspaceID    string
	ObjectID       string
	ActorType      string
	ActorID        string
	Classification string
	Kind           string
}

type ReadAuthorizer interface {
	AuthorizeStoredObjectRead(context.Context, ReadAuthorization) error
}

type OpenedObject struct {
	Metadata StoredObject
	Body     io.ReadCloser
}

type blobUpload struct {
	Size int64
}

type blobInfo struct {
	Size   int64
	SHA256 string
}

type blobBackend interface {
	Put(context.Context, string, string, io.Reader, int64, string, string) (blobUpload, error)
	Open(context.Context, string, string) (io.ReadCloser, blobInfo, error)
	Stat(context.Context, string, string) (blobInfo, error)
	Abort(context.Context, string, string) error
	Delete(context.Context, string, string) error
	PresignGet(context.Context, string, string, time.Duration) (*url.URL, error)
	// PresignPutWithHeaders must sign at least Content-Length (KD-17).
	// Do not use unbound PresignedPutObject as a production path.
	PresignPutWithHeaders(context.Context, string, string, time.Duration, http.Header) (*url.URL, error)
}

type ObjectStore struct {
	backend        blobBackend
	repository     *Repository
	authorizer     ReadAuthorizer
	maxObjectBytes int64
}

func NewMinIOStore(
	config MinIOConfig,
	repository *Repository,
	authorizer ReadAuthorizer,
) (*ObjectStore, error) {
	config.Endpoint = strings.TrimSpace(config.Endpoint)
	config.AccessKey = strings.TrimSpace(config.AccessKey)
	config.SecretKey = strings.TrimSpace(config.SecretKey)
	config.Region = strings.TrimSpace(config.Region)
	if config.Endpoint == "" || strings.Contains(config.Endpoint, "://") ||
		config.AccessKey == "" || config.SecretKey == "" {
		return nil, ErrInvalid
	}
	client, err := minio.New(config.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""),
		Secure: config.UseSSL,
		Region: config.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("create MinIO client: %w", err)
	}
	return newObjectStore(&minioBlobBackend{client: client}, repository, authorizer, config.MaxObjectBytes)
}

func newObjectStore(
	backend blobBackend,
	repository *Repository,
	authorizer ReadAuthorizer,
	maxObjectBytes int64,
) (*ObjectStore, error) {
	if backend == nil || repository == nil || authorizer == nil {
		return nil, errors.New("stored object backend, repository, and authorizer are required")
	}
	if maxObjectBytes == 0 {
		maxObjectBytes = defaultMaxObjectBytes
	}
	if maxObjectBytes < 1 || maxObjectBytes > 5<<40 {
		return nil, ErrInvalid
	}
	return &ObjectStore{
		backend: backend, repository: repository, authorizer: authorizer,
		maxObjectBytes: maxObjectBytes,
	}, nil
}

func (store *ObjectStore) Put(ctx context.Context, input PutInput) (StoredObject, error) {
	metadata, expectedSHA256, storageContentType, err := store.preparePut(input)
	if err != nil {
		return StoredObject{}, err
	}
	if _, err := store.repository.Get(ctx, metadata.WorkspaceID, metadata.ID); err == nil {
		return StoredObject{}, ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return StoredObject{}, err
	}

	hasher := sha256.New()
	counter := &countingReader{reader: io.TeeReader(input.Reader, hasher)}
	uploaded, err := store.backend.Put(
		ctx, metadata.Bucket, metadata.ObjectKey, counter, metadata.SizeBytes,
		storageContentType, expectedSHA256,
	)
	if err != nil {
		return StoredObject{}, store.abortFailure(ctx, metadata, err)
	}
	actualSHA256, err := verifyUploadedStream(input.Reader, counter.count, uploaded.Size,
		metadata.SizeBytes, hasher, expectedSHA256)
	if err != nil {
		return StoredObject{}, store.cleanupFailure(ctx, metadata, "verify uploaded object", err)
	}
	metadata.SHA256 = actualSHA256
	created, err := store.repository.Create(ctx, metadata)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			existing, getErr := store.repository.Get(ctx, metadata.WorkspaceID, metadata.ID)
			if getErr == nil && existing.Bucket == metadata.Bucket && existing.ObjectKey == metadata.ObjectKey {
				if sameStoredObjectMetadata(existing, metadata) {
					return existing, nil
				}
				return StoredObject{}, ErrConflict
			}
		}
		return StoredObject{}, store.cleanupFailure(ctx, metadata, "commit object metadata", err)
	}
	return created, nil
}

func sameStoredObjectMetadata(existing StoredObject, expected CreateInput) bool {
	if existing.ID != expected.ID || existing.WorkspaceID != expected.WorkspaceID ||
		existing.Bucket != expected.Bucket || existing.ObjectKey != expected.ObjectKey ||
		existing.Kind != expected.Kind || existing.ContentType != expected.ContentType ||
		existing.SizeBytes != expected.SizeBytes || existing.SHA256 != expected.SHA256 ||
		existing.EncryptionKeyID != expected.EncryptionKeyID ||
		existing.Classification != expected.Classification ||
		existing.RetentionMode != expected.RetentionMode ||
		existing.CreatedByType != expected.CreatedByType || existing.CreatedByID != expected.CreatedByID {
		return false
	}
	if existing.RetentionUntil == nil || expected.RetentionUntil == nil {
		return existing.RetentionUntil == nil && expected.RetentionUntil == nil
	}
	return existing.RetentionUntil.Equal(expected.RetentionUntil.UTC().Truncate(time.Microsecond))
}

func (store *ObjectStore) abortFailure(
	ctx context.Context,
	metadata CreateInput,
	cause error,
) error {
	cleanupErr := store.backend.Abort(context.WithoutCancel(ctx), metadata.Bucket, metadata.ObjectKey)
	if cleanupErr != nil {
		return fmt.Errorf("upload object: %w: %v; abort failed: %v", ErrObjectStorage, cause, cleanupErr)
	}
	return fmt.Errorf("upload object: %w: %v", ErrObjectStorage, cause)
}

func (store *ObjectStore) Open(
	ctx context.Context,
	request ReadRequest,
) (OpenedObject, error) {
	metadata, err := store.authorizedMetadata(ctx, request)
	if err != nil {
		return OpenedObject{}, err
	}
	body, info, err := store.backend.Open(ctx, metadata.Bucket, metadata.ObjectKey)
	if err != nil {
		return OpenedObject{}, fmt.Errorf("open stored object: %w: %v", ErrObjectStorage, err)
	}
	if err := verifyBlobInfo(metadata, info); err != nil {
		_ = body.Close()
		return OpenedObject{}, err
	}
	return OpenedObject{
		Metadata: metadata,
		Body: &verifiedReadCloser{
			body: body, expectedSize: metadata.SizeBytes, expectedSHA256: metadata.SHA256,
			hasher: sha256.New(),
		},
	}, nil
}

func (store *ObjectStore) PresignDownload(
	ctx context.Context,
	request ReadRequest,
	ttl time.Duration,
) (*url.URL, error) {
	if ttl < time.Second || ttl > maxPresignedDownloadTTL {
		return nil, ErrInvalid
	}
	metadata, err := store.authorizedMetadata(ctx, request)
	if err != nil {
		return nil, err
	}
	info, err := store.backend.Stat(ctx, metadata.Bucket, metadata.ObjectKey)
	if err != nil {
		return nil, fmt.Errorf("stat stored object before signing: %w: %v", ErrObjectStorage, err)
	}
	if err := verifyBlobInfo(metadata, info); err != nil {
		return nil, err
	}
	signed, err := store.backend.PresignGet(ctx, metadata.Bucket, metadata.ObjectKey, ttl)
	if err != nil {
		return nil, fmt.Errorf("presign stored object: %w: %v", ErrObjectStorage, err)
	}
	return signed, nil
}

func (store *ObjectStore) preparePut(input PutInput) (CreateInput, string, string, error) {
	if input.Reader == nil || input.SizeBytes < 0 || input.SizeBytes > store.maxObjectBytes {
		return CreateInput{}, "", "", ErrInvalid
	}
	bucket, err := bucketForKind(strings.ToUpper(strings.TrimSpace(input.Kind)))
	if err != nil {
		return CreateInput{}, "", "", err
	}
	objectID := strings.TrimSpace(input.ID)
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	if !validUUID(objectID) || !validUUID(workspaceID) {
		return CreateInput{}, "", "", ErrInvalid
	}
	normalizedKind := strings.ToUpper(strings.TrimSpace(input.Kind))
	expectedSHA256 := strings.ToLower(strings.TrimSpace(input.SHA256))
	if expectedSHA256 != "" && !validHash(expectedSHA256) {
		return CreateInput{}, "", "", ErrInvalid
	}
	storageContentType := strings.TrimSpace(input.StorageContentType)
	if storageContentType == "" {
		storageContentType = strings.TrimSpace(input.ContentType)
	}
	if storageContentType == "" || len(storageContentType) > 255 {
		return CreateInput{}, "", "", ErrInvalid
	}
	objectKey := workspaceID + "/" + strings.ToLower(strings.ReplaceAll(normalizedKind, "_", "-")) + "/" + objectID
	metadata := CreateInput{
		ID: objectID, WorkspaceID: workspaceID, Bucket: bucket, ObjectKey: objectKey,
		Kind: normalizedKind, ContentType: input.ContentType, SizeBytes: input.SizeBytes,
		SHA256: expectedSHA256, EncryptionKeyID: input.EncryptionKeyID,
		Classification: input.Classification, RetentionMode: input.RetentionMode,
		RetentionUntil: input.RetentionUntil, CreatedByType: input.CreatedByType,
		CreatedByID: input.CreatedByID,
	}
	metadata = normalizeCreate(metadata)
	if metadata.SHA256 == "" {
		metadata.SHA256 = strings.Repeat("0", 64)
	}
	if !validCreate(metadata) {
		return CreateInput{}, "", "", ErrInvalid
	}
	metadata.SHA256 = expectedSHA256
	return metadata, expectedSHA256, storageContentType, nil
}

func (store *ObjectStore) authorizedMetadata(
	ctx context.Context,
	request ReadRequest,
) (StoredObject, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.ObjectID = strings.TrimSpace(request.ObjectID)
	request.ActorType = strings.ToUpper(strings.TrimSpace(request.ActorType))
	request.ActorID = strings.TrimSpace(request.ActorID)
	if !validUUID(request.WorkspaceID) || !validUUID(request.ObjectID) ||
		!validUUID(request.ActorID) || !validCreatorType(request.ActorType) {
		return StoredObject{}, ErrInvalid
	}
	metadata, err := store.repository.Get(ctx, request.WorkspaceID, request.ObjectID)
	if err != nil {
		return StoredObject{}, err
	}
	// Preview bodies are never a general download surface (product read is the
	// create-preview POST response or the current-revision SQL API after promote).
	if IsPromptPreview(metadata.Kind) || metadata.BodyUnavailable(time.Now().UTC()) {
		return StoredObject{}, ErrNotFound
	}
	if err := store.authorizer.AuthorizeStoredObjectRead(ctx, ReadAuthorization{
		WorkspaceID: request.WorkspaceID, ObjectID: request.ObjectID,
		ActorType: request.ActorType, ActorID: request.ActorID,
		Classification: metadata.Classification, Kind: metadata.Kind,
	}); err != nil {
		return StoredObject{}, err
	}
	return metadata, nil
}

// PurgeBody is the internal, idempotent body-delete primitive for expired
// EXPIRING objects (prompt-preview and AAP_FILE / AAP_FILE_DERIVED). It never
// decrypts, never presigns, and treats a missing blob as success. Metadata
// remains as a tombstone with body_purged_at.
func (store *ObjectStore) PurgeBody(ctx context.Context, workspaceID, objectID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	objectID = strings.TrimSpace(objectID)
	if !validUUID(workspaceID) || !validUUID(objectID) {
		return ErrInvalid
	}
	metadata, err := store.repository.Get(ctx, workspaceID, objectID)
	if err != nil {
		return err
	}
	if !IsExpiringBodyPurgeable(metadata.Kind) {
		return ErrInvalid
	}
	if metadata.BodyPurgedAt != nil {
		return nil
	}
	if metadata.RetentionMode != RetentionExpiring || metadata.RetentionUntil == nil ||
		metadata.RetentionUntil.After(time.Now().UTC()) {
		return ErrConflict
	}
	if err := store.backend.Delete(ctx, metadata.Bucket, metadata.ObjectKey); err != nil {
		// Absent object is success; other failures surface as storage errors.
		if !errors.Is(err, ErrNotFound) && !isBlobAbsent(err) {
			return fmt.Errorf("purge expiring body: %w: %v", ErrObjectStorage, err)
		}
	}
	tx, err := store.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin expiring body purge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := store.repository.MarkBodyPurgedInTx(ctx, tx, workspaceID, objectID, nil); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit expiring body purge: %w", err)
	}
	return nil
}

func isBlobAbsent(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "not found") ||
		strings.Contains(message, "nosuchkey") ||
		strings.Contains(message, "no such key") ||
		strings.Contains(message, "404")
}

func (store *ObjectStore) cleanupFailure(
	ctx context.Context,
	metadata CreateInput,
	operation string,
	cause error,
) error {
	cleanupErr := store.backend.Delete(context.WithoutCancel(ctx), metadata.Bucket, metadata.ObjectKey)
	if cleanupErr != nil {
		return fmt.Errorf("%s: %w: %v; cleanup failed: %v", operation, ErrObjectStorage, cause, cleanupErr)
	}
	return fmt.Errorf("%s: %w", operation, cause)
}

func verifyUploadedStream(
	original io.Reader,
	readBytes, uploadedBytes, expectedBytes int64,
	hasher hash.Hash,
	expectedSHA256 string,
) (string, error) {
	var extra [1]byte
	n, err := original.Read(extra[:])
	if n != 0 || !errors.Is(err, io.EOF) {
		return "", ErrIntegrity
	}
	actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
	if readBytes != expectedBytes || uploadedBytes != expectedBytes ||
		(expectedSHA256 != "" && actualSHA256 != expectedSHA256) {
		return "", ErrIntegrity
	}
	return actualSHA256, nil
}

func verifyBlobInfo(metadata StoredObject, info blobInfo) error {
	if info.Size != metadata.SizeBytes ||
		(info.SHA256 != "" && !strings.EqualFold(info.SHA256, metadata.SHA256)) {
		return ErrIntegrity
	}
	return nil
}

func bucketForKind(kind string) (string, error) {
	switch kind {
	case KindToolTestPayload:
		return BucketToolTests, nil
	case KindAuditEventPayload, KindAuditExport:
		return BucketAuditPackages, nil
	case KindAAPFile, KindAAPFileDerived:
		return BucketAAPFiles, nil
	case KindOpenAPISource, KindPromptRunInput, KindPromptRunOutput,
		KindPromptPreviewInput, KindPromptPreviewOutput, KindModelTurn,
		KindChatMessage, KindToolInvocationPayload, KindExecutionCheckpoint,
		KindChatContextSummary:
		return BucketExecutions, nil
	default:
		return "", ErrInvalid
	}
}

// PresignPutWithHeaders returns a SigV4 presigned PUT URL that binds the
// provided headers into the signature (KD-17). Content-Length is required;
// Content-Type should be supplied by callers. Unbound PresignedPutObject is
// not used as a production path.
func (store *ObjectStore) PresignPutWithHeaders(
	ctx context.Context,
	bucket, objectKey string,
	ttl time.Duration,
	headers http.Header,
) (*url.URL, error) {
	bucket = strings.TrimSpace(bucket)
	objectKey = strings.TrimSpace(objectKey)
	if bucket == "" || objectKey == "" || ttl < time.Second || ttl > maxPresignedUploadTTL {
		return nil, ErrInvalid
	}
	if headers == nil || strings.TrimSpace(headers.Get("Content-Length")) == "" {
		return nil, ErrInvalid
	}
	signed, err := store.backend.PresignPutWithHeaders(ctx, bucket, objectKey, ttl, headers)
	if err != nil {
		return nil, fmt.Errorf("presign put with headers: %w: %v", ErrObjectStorage, err)
	}
	return signed, nil
}

// EnsureBuckets creates the given MinIO buckets if missing (idempotent).
// Intended for server bootstrap of AAP staging + permanent buckets.
func EnsureBuckets(ctx context.Context, config MinIOConfig, buckets ...string) error {
	config.Endpoint = strings.TrimSpace(config.Endpoint)
	config.AccessKey = strings.TrimSpace(config.AccessKey)
	config.SecretKey = strings.TrimSpace(config.SecretKey)
	config.Region = strings.TrimSpace(config.Region)
	if config.Endpoint == "" || strings.Contains(config.Endpoint, "://") ||
		config.AccessKey == "" || config.SecretKey == "" {
		return ErrInvalid
	}
	if len(buckets) == 0 {
		return ErrInvalid
	}
	client, err := minio.New(config.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""),
		Secure: config.UseSSL,
		Region: config.Region,
	})
	if err != nil {
		return fmt.Errorf("create MinIO client for bucket ensure: %w", err)
	}
	for _, bucket := range buckets {
		bucket = strings.TrimSpace(bucket)
		if bucket == "" || !validBucket(bucket) {
			return ErrInvalid
		}
		exists, err := client.BucketExists(ctx, bucket)
		if err != nil {
			return fmt.Errorf("check bucket %q: %w", bucket, err)
		}
		if exists {
			continue
		}
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: config.Region}); err != nil {
			// Concurrent bootstrap may create the bucket first; re-check.
			exists, checkErr := client.BucketExists(ctx, bucket)
			if checkErr == nil && exists {
				continue
			}
			return fmt.Errorf("create bucket %q: %w", bucket, err)
		}
	}
	return nil
}

// AAPBootstrapBuckets returns staging + permanent buckets for AAP file storage.
func AAPBootstrapBuckets() []string {
	return []string{BucketAAPStaging, BucketAAPFiles}
}

type countingReader struct {
	reader io.Reader
	count  int64
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	reader.count += int64(count)
	return count, err
}

type verifiedReadCloser struct {
	body            io.ReadCloser
	expectedSize    int64
	expectedSHA256  string
	readBytes       int64
	hasher          hash.Hash
	verified        bool
	verificationErr error
}

func (reader *verifiedReadCloser) Read(buffer []byte) (int, error) {
	if reader.verificationErr != nil {
		return 0, reader.verificationErr
	}
	count, err := reader.body.Read(buffer)
	if count > 0 {
		reader.readBytes += int64(count)
		_, _ = reader.hasher.Write(buffer[:count])
	}
	if errors.Is(err, io.EOF) && !reader.verified {
		reader.verified = true
		actualSHA256 := hex.EncodeToString(reader.hasher.Sum(nil))
		if reader.readBytes != reader.expectedSize || actualSHA256 != reader.expectedSHA256 {
			reader.verificationErr = ErrIntegrity
			if count == 0 {
				return 0, reader.verificationErr
			}
			return count, nil
		}
	}
	return count, err
}

func (reader *verifiedReadCloser) Close() error { return reader.body.Close() }

type minioBlobBackend struct{ client *minio.Client }

func (backend *minioBlobBackend) Put(
	ctx context.Context,
	bucket, objectKey string,
	reader io.Reader,
	size int64,
	contentType, expectedSHA256 string,
) (blobUpload, error) {
	options := minio.PutObjectOptions{ContentType: contentType}
	if expectedSHA256 != "" {
		options.UserMetadata = map[string]string{objectSHA256MetadataKey: expectedSHA256}
	}
	options.SetMatchETagExcept("*")
	info, err := backend.client.PutObject(ctx, bucket, objectKey, reader, size, options)
	return blobUpload{Size: info.Size}, err
}

func (backend *minioBlobBackend) Open(
	ctx context.Context,
	bucket, objectKey string,
) (io.ReadCloser, blobInfo, error) {
	object, err := backend.client.GetObject(ctx, bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, blobInfo{}, err
	}
	info, err := object.Stat()
	if err != nil {
		_ = object.Close()
		return nil, blobInfo{}, err
	}
	return object, minioObjectInfo(info), nil
}

func (backend *minioBlobBackend) Stat(
	ctx context.Context,
	bucket, objectKey string,
) (blobInfo, error) {
	info, err := backend.client.StatObject(ctx, bucket, objectKey, minio.StatObjectOptions{})
	return minioObjectInfo(info), err
}

func (backend *minioBlobBackend) Delete(
	ctx context.Context,
	bucket, objectKey string,
) error {
	return backend.client.RemoveObject(ctx, bucket, objectKey, minio.RemoveObjectOptions{})
}

func (backend *minioBlobBackend) Abort(
	ctx context.Context,
	bucket, objectKey string,
) error {
	return backend.client.RemoveIncompleteUpload(ctx, bucket, objectKey)
}

func (backend *minioBlobBackend) PresignGet(
	ctx context.Context,
	bucket, objectKey string,
	ttl time.Duration,
) (*url.URL, error) {
	return backend.client.PresignedGetObject(ctx, bucket, objectKey, ttl, url.Values{})
}

// PresignPutWithHeaders signs a PUT using minio.PresignHeader so extra headers
// (at minimum Content-Length) are part of the SigV4 signature. Callers must
// send the exact same header values on the upload request.
func (backend *minioBlobBackend) PresignPutWithHeaders(
	ctx context.Context,
	bucket, objectKey string,
	ttl time.Duration,
	headers http.Header,
) (*url.URL, error) {
	if headers == nil || strings.TrimSpace(headers.Get("Content-Length")) == "" {
		return nil, ErrInvalid
	}
	// Clone so we do not mutate the caller's map while signing.
	extra := headers.Clone()
	return backend.client.PresignHeader(
		ctx, http.MethodPut, bucket, objectKey, ttl, url.Values{}, extra,
	)
}

func minioObjectInfo(info minio.ObjectInfo) blobInfo {
	checksum := info.Metadata.Get("X-Amz-Meta-" + objectSHA256MetadataKey)
	for key, value := range info.UserMetadata {
		if strings.EqualFold(key, objectSHA256MetadataKey) {
			checksum = value
			break
		}
	}
	return blobInfo{Size: info.Size, SHA256: checksum}
}
