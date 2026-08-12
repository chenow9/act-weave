package aapfile_test

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

	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/storedobject"

	"github.com/google/uuid"
)

const (
	testOwnerID     = "a18f1f2e-7b5a-7c3d-8e9f-123456789001"
	testWorkspaceID = "a18f1f2e-7b5a-7c3d-8e9f-123456789002"
	testModelID     = "a18f1f2e-7b5a-7c3d-8e9f-123456789003"
	testAgentID     = "a18f1f2e-7b5a-7c3d-8e9f-123456789004"
	testClientID    = "a18f1f2e-7b5a-7c3d-8e9f-123456789005"
	testServiceID   = "a18f1f2e-7b5a-7c3d-8e9f-123456789006"
	testSubjectID   = "a18f1f2e-7b5a-7c3d-8e9f-123456789007"
)

// Minimal valid PNG (1x1).
var pngBytes = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, 0x00, 0x00, 0x00,
	0x0c, 0x49, 0x44, 0x41, 0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
	0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x05, 0xfe, 0xd4, 0xef, 0x00, 0x00,
	0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestCreateCompletePromoteHappyPath(t *testing.T) {
	service, staging, secure, _ := newAAPFileService(t)
	ctx := context.Background()

	content := pngBytes
	sum := sha256.Sum256(content)
	declared := hex.EncodeToString(sum[:])

	intent, err := service.CreateUploadIntent(ctx, createCCInput(len(content), declared))
	if err != nil {
		t.Fatalf("CreateUploadIntent: %v", err)
	}
	file := intent.File
	if file.Status != aapfile.StatusPendingUpload {
		t.Fatalf("status=%s want PENDING_UPLOAD", file.Status)
	}
	if file.OwnershipMode != aapfile.OwnershipSubjectOwned {
		t.Fatalf("ownership_mode=%s want SUBJECT_OWNED", file.OwnershipMode)
	}
	if file.SubjectType != nil || file.SubjectID != nil {
		t.Fatalf("CC create must have null subject, got type=%v id=%v", file.SubjectType, file.SubjectID)
	}
	if file.ActorType != aapfile.ActorServicePrincipal || file.ActorID != testServiceID {
		t.Fatalf("actor=%s/%s want SERVICE_PRINCIPAL/%s", file.ActorType, file.ActorID, testServiceID)
	}
	if file.OwnershipPolicyVersion != 7 {
		t.Fatalf("ownership_policy_version=%d want 7 (AgentPolicyVersion)", file.OwnershipPolicyVersion)
	}
	if intent.UploadURL == "" || intent.UploadHeaders["Content-Length"] == "" {
		t.Fatalf("presign missing: url=%q headers=%v", intent.UploadURL, intent.UploadHeaders)
	}
	if file.StagingObjectKey == nil {
		t.Fatal("staging_object_key required")
	}

	// Client PUT into staging.
	staging.put(file.StagingBucket, *file.StagingObjectKey, content)

	complete, err := service.CompleteUpload(ctx, aapfile.CompleteUploadInput{
		Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
		FileID: file.ID,
	})
	if err != nil {
		t.Fatalf("CompleteUpload: %v", err)
	}
	if complete.File.Status != aapfile.StatusUploaded {
		t.Fatalf("after complete status=%s want UPLOADED", complete.File.Status)
	}
	if complete.File.ProcessingVersion != file.ProcessingVersion+1 {
		t.Fatalf("processing_version=%d want %d", complete.File.ProcessingVersion, file.ProcessingVersion+1)
	}
	if complete.Job.Stage != aapfile.StagePromote || complete.Job.Status != aapfile.JobPending {
		t.Fatalf("promote job=%+v", complete.Job)
	}

	// Fast path: secure store must not have been called yet.
	if secure.putCalls != 0 {
		t.Fatalf("CompleteUpload must not SecureStore.Put, putCalls=%d", secure.putCalls)
	}

	promoted, err := service.Promote(ctx, testWorkspaceID, file.ID)
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if promoted.Status != aapfile.StatusReady {
		t.Fatalf("after promote status=%s want READY", promoted.Status)
	}
	if promoted.StoredObjectID == nil || *promoted.StoredObjectID == "" {
		t.Fatal("stored_object_id required after promote")
	}
	if promoted.SHA256 == nil || *promoted.SHA256 != declared {
		t.Fatalf("sha256=%v want %s", promoted.SHA256, declared)
	}
	if promoted.StagingObjectKey != nil {
		t.Fatalf("staging key should be cleared, got %v", promoted.StagingObjectKey)
	}
	if promoted.StagingDeletedAt == nil {
		t.Fatal("staging_deleted_at required")
	}
	if secure.putCalls != 1 {
		t.Fatalf("SecureStore.Put calls=%d want 1", secure.putCalls)
	}
	if secure.lastKind != storedobject.KindAAPFile {
		t.Fatalf("kind=%s want AAP_FILE", secure.lastKind)
	}
	if secure.lastClass != storedobject.ClassificationSensitive {
		t.Fatalf("classification=%s want SENSITIVE", secure.lastClass)
	}
	if secure.lastRetention != storedobject.RetentionExpiring {
		t.Fatalf("retention=%s want EXPIRING", secure.lastRetention)
	}

	got, err := service.GetFile(ctx, testWorkspaceID, file.ID)
	if err != nil || got.Status != aapfile.StatusReady {
		t.Fatalf("GetFile: %+v err=%v", got, err)
	}
}

func TestCompleteUploadSizeMismatchFails(t *testing.T) {
	service, staging, secure, _ := newAAPFileService(t)
	ctx := context.Background()

	intent, err := service.CreateUploadIntent(ctx, createCCInput(len(pngBytes), ""))
	if err != nil {
		t.Fatal(err)
	}
	// Put wrong size.
	staging.put(intent.File.StagingBucket, *intent.File.StagingObjectKey, []byte("short"))

	_, err = service.CompleteUpload(ctx, aapfile.CompleteUploadInput{
		Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
		FileID: intent.File.ID,
	})
	if err == nil || !errors.Is(err, aapfile.ErrFailed) {
		t.Fatalf("expected size mismatch ErrFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), aapfile.ErrorCodeIntegrityMismatch) {
		t.Fatalf("error should mention FILE_INTEGRITY_MISMATCH, got %v", err)
	}
	if secure.putCalls != 0 {
		t.Fatal("must not put permanent object on size mismatch")
	}

	got, err := service.GetFile(ctx, testWorkspaceID, intent.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != aapfile.StatusFailed {
		t.Fatalf("status=%s want FAILED", got.Status)
	}
	if got.ErrorCode == nil || *got.ErrorCode != aapfile.ErrorCodeIntegrityMismatch {
		t.Fatalf("error_code=%v", got.ErrorCode)
	}
	if got.StoredObjectID != nil {
		t.Fatalf("stored_object_id must be null, got %v", got.StoredObjectID)
	}
}

func TestPromoteSHA256MismatchFailsWithoutPermanentObject(t *testing.T) {
	service, staging, secure, db := newAAPFileService(t)
	ctx := context.Background()

	content := pngBytes
	// Declare wrong hash at create.
	wrong := strings.Repeat("ab", 32)
	intent, err := service.CreateUploadIntent(ctx, createCCInput(len(content), wrong))
	if err != nil {
		t.Fatal(err)
	}
	staging.put(intent.File.StagingBucket, *intent.File.StagingObjectKey, content)

	complete, err := service.CompleteUpload(ctx, aapfile.CompleteUploadInput{
		Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
		FileID: intent.File.ID,
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if complete.File.Status != aapfile.StatusUploaded {
		t.Fatalf("status=%s", complete.File.Status)
	}

	promoted, err := service.Promote(ctx, testWorkspaceID, intent.File.ID)
	if err == nil || !errors.Is(err, aapfile.ErrFailed) {
		t.Fatalf("expected integrity fail, got file=%+v err=%v", promoted, err)
	}
	if !strings.Contains(err.Error(), aapfile.ErrorCodeIntegrityMismatch) {
		t.Fatalf("error=%v", err)
	}
	if secure.putCalls != 0 {
		t.Fatalf("SecureStore.Put must not run on sha256 mismatch, calls=%d", secure.putCalls)
	}

	got, err := service.GetFile(ctx, testWorkspaceID, intent.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != aapfile.StatusFailed {
		t.Fatalf("status=%s want FAILED", got.Status)
	}
	if got.StoredObjectID != nil {
		t.Fatalf("no permanent object, got %v", got.StoredObjectID)
	}
	if got.ErrorCode == nil || *got.ErrorCode != aapfile.ErrorCodeIntegrityMismatch {
		t.Fatalf("error_code=%v", got.ErrorCode)
	}
	// Staging left for GC (key still present on the file row).
	if got.StagingObjectKey == nil {
		t.Fatal("staging key should remain for GC after promote integrity failure")
	}

	repo, err := aapfile.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	job, err := repo.GetJob(ctx, testWorkspaceID, intent.File.ID, aapfile.StagePromote)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != aapfile.JobFailed {
		t.Fatalf("promote job status=%s want FAILED", job.Status)
	}
	if job.LastErrorCode == nil || *job.LastErrorCode != aapfile.ErrorCodeIntegrityMismatch {
		t.Fatalf("job last_error_code=%v", job.LastErrorCode)
	}
}

func TestOwnershipSubjectOwnedRules(t *testing.T) {
	service, _, _, _ := newAAPFileService(t)
	ctx := context.Background()

	t.Run("client_credentials_null_subject", func(t *testing.T) {
		intent, err := service.CreateUploadIntent(ctx, createCCInput(len(pngBytes), ""))
		if err != nil {
			t.Fatal(err)
		}
		f := intent.File
		if f.OwnershipMode != aapfile.OwnershipSubjectOwned {
			t.Fatalf("mode=%s", f.OwnershipMode)
		}
		if f.SubjectType != nil || f.SubjectID != nil {
			t.Fatalf("subject must be null for CC: %v %v", f.SubjectType, f.SubjectID)
		}
		if f.ActorType != aapfile.ActorServicePrincipal || f.ActorID != testServiceID {
			t.Fatalf("actor=%s/%s", f.ActorType, f.ActorID)
		}
		if f.ClientID != testClientID {
			t.Fatalf("client_id=%s", f.ClientID)
		}
		if f.OwnershipPolicyVersion != 7 {
			t.Fatalf("policy_version=%d want AgentPolicyVersion 7", f.OwnershipPolicyVersion)
		}
	})

	t.Run("token_exchange_external_subject", func(t *testing.T) {
		input := createCCInput(len(pngBytes), "")
		input.Principal.PrincipalID = testSubjectID // TE: subject != SP
		intent, err := service.CreateUploadIntent(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		f := intent.File
		if f.OwnershipMode != aapfile.OwnershipSubjectOwned {
			t.Fatalf("mode=%s want SUBJECT_OWNED (never POLICY_SHARED on create)", f.OwnershipMode)
		}
		if f.SubjectType == nil || *f.SubjectType != aapfile.SubjectExternal {
			t.Fatalf("subject_type=%v want EXTERNAL_SUBJECT", f.SubjectType)
		}
		if f.SubjectID == nil || *f.SubjectID != testSubjectID {
			t.Fatalf("subject_id=%v want %s", f.SubjectID, testSubjectID)
		}
		if f.ActorType != aapfile.ActorServicePrincipal || f.ActorID != testServiceID {
			t.Fatalf("actor must remain SP: %s/%s", f.ActorType, f.ActorID)
		}
		if f.OwnershipPolicyVersion != 7 {
			t.Fatalf("policy_version=%d", f.OwnershipPolicyVersion)
		}
	})

	t.Run("never_policy_shared_on_create", func(t *testing.T) {
		// Even if caller somehow expected shared, create path hardcodes SUBJECT_OWNED.
		intent, err := service.CreateUploadIntent(ctx, createCCInput(8, ""))
		if err != nil {
			// size 8 with media type png is fine; use real size
			intent, err = service.CreateUploadIntent(ctx, createCCInput(len(pngBytes), ""))
			if err != nil {
				t.Fatal(err)
			}
		}
		if intent.File.OwnershipMode == aapfile.OwnershipPolicyShared {
			t.Fatal("v1 create must never write POLICY_SHARED")
		}
	})
}

func TestCompleteUploadIdempotent(t *testing.T) {
	service, staging, _, _ := newAAPFileService(t)
	ctx := context.Background()
	intent, err := service.CreateUploadIntent(ctx, createCCInput(len(pngBytes), ""))
	if err != nil {
		t.Fatal(err)
	}
	staging.put(intent.File.StagingBucket, *intent.File.StagingObjectKey, pngBytes)

	first, err := service.CompleteUpload(ctx, aapfile.CompleteUploadInput{
		Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
		FileID: intent.File.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CompleteUpload(ctx, aapfile.CompleteUploadInput{
		Scope:  aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
		FileID: intent.File.ID,
	})
	if err != nil {
		t.Fatalf("idempotent complete: %v", err)
	}
	if second.File.Status != aapfile.StatusUploaded {
		t.Fatalf("status=%s", second.File.Status)
	}
	if second.File.ProcessingVersion != first.File.ProcessingVersion {
		t.Fatalf("version mutated on idempotent complete: %d vs %d",
			second.File.ProcessingVersion, first.File.ProcessingVersion)
	}
	if second.Job.ID != first.Job.ID {
		t.Fatalf("job id changed on idempotent complete")
	}
}

func TestCreateRejectsDisallowedMediaAndOversize(t *testing.T) {
	service, _, _, _ := newAAPFileService(t)
	ctx := context.Background()

	bad := createCCInput(10, "")
	bad.MediaType = "application/zip"
	if _, err := service.CreateUploadIntent(ctx, bad); err == nil {
		t.Fatal("expected media type denied")
	}

	big := createCCInput(int(aapfile.DefaultMaxBytes)+1, "")
	if _, err := service.CreateUploadIntent(ctx, big); err == nil {
		t.Fatal("expected size exceeded")
	}
}

func TestMigrationAAPFilesTablesExist(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 20 || version.Dirty {
		t.Fatalf("expected migration 18, got %+v", version)
	}
	db := testDatabase.Open(t)
	for _, table := range []string{
		"aap_files", "aap_file_artifacts", "aap_file_processing_jobs",
		"aap_file_download_tokens", "aap_workspace_file_processors",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s missing: exists=%v err=%v", table, exists, err)
		}
	}
	// Kind check accepts AAP_FILE.
	var checkDef string
	if err := db.QueryRow(`
		SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conname='stored_objects_kind_check'
	`).Scan(&checkDef); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(checkDef, "AAP_FILE") || !strings.Contains(checkDef, "AAP_FILE_DERIVED") {
		t.Fatalf("kind check missing AAP kinds: %s", checkDef)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func createCCInput(size int, sha string) aapfile.CreateUploadIntentInput {
	return aapfile.CreateUploadIntentInput{
		Scope: aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID},
		Principal: agentaccessauth.AAPAccessTokenPrincipal{
			PrincipalID:        testServiceID,
			ServicePrincipalID: testServiceID,
			WorkspaceID:        testWorkspaceID,
			AgentID:            testAgentID,
		},
		ClientID:           testClientID,
		AgentPolicyVersion: 7,
		Filename:           "pixel.png",
		MediaType:          "image/png",
		SizeBytes:          int64(size),
		SHA256:             sha,
		Purpose:            aapfile.PurposeGeneral,
	}
}

func newAAPFileService(t *testing.T) (*aapfile.Service, *fakeStaging, *fakeSecure, *sql.DB) {
	t.Helper()
	db := openMigratedDB(t)
	insertFixtures(t, db)
	repo, err := aapfile.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	staging := newFakeStaging()
	secure := &fakeSecure{}
	service, err := aapfile.NewService(repo, staging, secure)
	if err != nil {
		t.Fatal(err)
	}
	return service, staging, secure, db
}

func openMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 20 || version.Dirty {
		t.Fatalf("migrate: %+v", version)
	}
	return testDatabase.Open(t)
}

func insertFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'aapfile.owner','Owner')`, testOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'aapfile-ws','AAP File WS','SANDBOX',$2,$2,$2)
	`, testWorkspaceID, testOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(
		 id,workspace_id,name,provider,api_base,model_name,created_by,updated_by
		) VALUES ($1,$2,'m','openai','https://models.example.test','m',$3,$3)
	`, testModelID, testWorkspaceID, testOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'agent',$3,$4,$4)
	`, testAgentID, testWorkspaceID, testModelID, testOwnerID); err != nil {
		t.Fatal(err)
	}
}

type fakeStaging struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newFakeStaging() *fakeStaging {
	return &fakeStaging{objects: make(map[string][]byte)}
}

func (f *fakeStaging) key(bucket, objectKey string) string {
	return bucket + "/" + objectKey
}

func (f *fakeStaging) put(bucket, objectKey string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[f.key(bucket, objectKey)] = append([]byte(nil), body...)
}

func (f *fakeStaging) Stat(_ context.Context, bucket, key string) (aapfile.BlobInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, ok := f.objects[f.key(bucket, key)]
	if !ok {
		return aapfile.BlobInfo{}, errors.New("staging object not found")
	}
	return aapfile.BlobInfo{Size: int64(len(body))}, nil
}

func (f *fakeStaging) Open(_ context.Context, bucket, key string) (io.ReadCloser, aapfile.BlobInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, ok := f.objects[f.key(bucket, key)]
	if !ok {
		return nil, aapfile.BlobInfo{}, errors.New("staging object not found")
	}
	cp := append([]byte(nil), body...)
	return io.NopCloser(bytes.NewReader(cp)), aapfile.BlobInfo{Size: int64(len(cp))}, nil
}

func (f *fakeStaging) Delete(_ context.Context, bucket, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, f.key(bucket, key))
	return nil
}

func (f *fakeStaging) has(bucket, key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[f.key(bucket, key)]
	return ok
}

func (f *fakeStaging) PresignPutWithHeaders(
	_ context.Context,
	bucket, key string,
	ttl time.Duration,
	headers http.Header,
) (*url.URL, error) {
	if strings.TrimSpace(headers.Get("Content-Length")) == "" {
		return nil, errors.New("Content-Length required")
	}
	return url.Parse(fmt.Sprintf(
		"https://staging.example.test/%s/%s?ttl=%d&cl=%s",
		bucket, key, int(ttl.Seconds()), headers.Get("Content-Length"),
	))
}

type fakeSecure struct {
	mu            sync.Mutex
	putCalls      int
	lastKind      string
	lastClass     string
	lastRetention string
	objects       map[string][]byte
}

func (f *fakeSecure) Put(_ context.Context, input storedobject.PutInput) (storedobject.StoredObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putCalls++
	f.lastKind = input.Kind
	f.lastClass = input.Classification
	f.lastRetention = input.RetentionMode
	if f.objects == nil {
		f.objects = make(map[string][]byte)
	}
	if input.Reader == nil {
		return storedobject.StoredObject{}, errors.New("reader required")
	}
	body, err := io.ReadAll(input.Reader)
	if err != nil {
		return storedobject.StoredObject{}, err
	}
	if int64(len(body)) != input.SizeBytes {
		return storedobject.StoredObject{}, fmt.Errorf("size mismatch put %d vs %d", len(body), input.SizeBytes)
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if input.SHA256 != "" && input.SHA256 != got {
		return storedobject.StoredObject{}, errors.New("sha256 mismatch on put")
	}
	f.objects[input.ID] = body
	id := input.ID
	if id == "" {
		id = uuid.Must(uuid.NewV7()).String()
	}
	return storedobject.StoredObject{
		ID: id, WorkspaceID: input.WorkspaceID, Kind: input.Kind,
		ContentType: input.ContentType, SizeBytes: input.SizeBytes, SHA256: got,
		Classification: input.Classification, RetentionMode: input.RetentionMode,
		RetentionUntil: input.RetentionUntil,
		CreatedByType:  input.CreatedByType, CreatedByID: input.CreatedByID,
		Bucket:    storedobject.BucketAAPFiles,
		ObjectKey: storedobject.AAPPermanentObjectKey(input.WorkspaceID, id),
	}, nil
}
