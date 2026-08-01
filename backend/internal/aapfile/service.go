package aapfile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/metrics"
	"actweave/backend/internal/storedobject"

	"github.com/google/uuid"
)

// pipelineStore is the optional persistence surface for follow-on stages and
// READY evaluation side-effects beyond the basic FileStore.
type pipelineStore interface {
	ListEnabledProcessors(context.Context, string) ([]WorkspaceFileProcessor, error)
	InsertJob(context.Context, ProcessingJob) (ProcessingJob, error)
	MarkFileReadyCAS(context.Context, string, string, int64) (File, error)
	MarkFileProcessingFailedCAS(context.Context, string, string, string, string, int64) (File, error)
	GetJobByDeliveryID(context.Context, string) (ProcessingJob, error)
	GetProcessor(context.Context, string, string) (WorkspaceFileProcessor, error)
	ApplyCallbackCAS(context.Context, string, string, []byte) (ProcessingJob, File, error)
	InsertArtifact(context.Context, FileArtifact) (FileArtifact, error)
}

// VirusScanConfig controls the optional virus_scan stage (config.AgentAccessFiles.VirusScan).
type VirusScanConfig struct {
	Enabled  bool
	Required bool
}

// PipelineOptions are process-level knobs used by Service follow-on enqueue.
type PipelineOptions struct {
	VirusScan VirusScanConfig
}

// StagingStore is the staging-bucket surface used by create/complete/promote.
// Tests inject fakes; production wraps MinIO ObjectStore/backend.
type StagingStore interface {
	Stat(ctx context.Context, bucket, key string) (BlobInfo, error)
	Open(ctx context.Context, bucket, key string) (io.ReadCloser, BlobInfo, error)
	Delete(ctx context.Context, bucket, key string) error
	PresignPutWithHeaders(
		ctx context.Context,
		bucket, key string,
		ttl time.Duration,
		headers http.Header,
	) (*url.URL, error)
}

// SecurePutter writes permanent encrypted AAP_FILE objects (SecureStore.Put).
type SecurePutter interface {
	Put(ctx context.Context, input storedobject.PutInput) (storedobject.StoredObject, error)
}

// FileStore is the persistence surface used by Service.
type FileStore interface {
	InsertFile(context.Context, File) (File, error)
	GetFile(context.Context, string, string) (File, error)
	CompleteUploadCAS(context.Context, string, string, int64, *string) (File, ProcessingJob, error)
	MarkFileFailed(context.Context, string, string, string, string, int64) (File, error)
	ApplyPromoteSuccess(context.Context, string, string, string, string, string, int64, bool, *time.Time, bool) (File, error)
	MarkPromoteFailed(context.Context, string, string, string, string, int64) (File, error)
	GetJob(context.Context, string, string, string) (ProcessingJob, error)
	ListJobs(context.Context, string, string) ([]ProcessingJob, error)
	CountPendingUploads(context.Context, string) (int, error)
	SumReadyBytes(context.Context, string) (int64, error)
	InsertDownloadToken(context.Context, DownloadToken) (DownloadToken, error)
	GetDownloadToken(context.Context, string) (DownloadToken, error)
	ConsumeDownloadToken(context.Context, string) (DownloadToken, error)
	PurgeExpiredDownloadTokens(context.Context, int) (int, error)
}

// Service implements create intent / complete (fast path) / get / promote /
// download tokens. Complete is authorize-less at this layer (Authorize at HTTP).
type Service struct {
	store         FileStore
	staging       StagingStore
	secure        SecurePutter
	maxBytes      int64
	stagingTTL    time.Duration
	presignTTL    time.Duration
	retention     time.Duration
	maxPending    int
	maxReadyBytes int64
	virusScan     VirusScanConfig
	metrics       *metrics.AAPFileCollector
	now           func() time.Time
	newID         func() (uuid.UUID, error)
}

// ServiceOption configures Service defaults.
type ServiceOption func(*Service) error

// WithMaxBytes overrides the default 25 MiB limit.
func WithMaxBytes(max int64) ServiceOption {
	return func(s *Service) error {
		if max < 1 {
			return errors.New("aapfile max bytes must be positive")
		}
		s.maxBytes = max
		return nil
	}
}

// WithClock injects a clock for tests.
func WithClock(now func() time.Time) ServiceOption {
	return func(s *Service) error {
		if now == nil {
			return errors.New("aapfile clock is required")
		}
		s.now = now
		return nil
	}
}

// WithIDGenerator injects UUID allocation (defaults to uuid.NewV7).
func WithIDGenerator(gen func() (uuid.UUID, error)) ServiceOption {
	return func(s *Service) error {
		if gen == nil {
			return errors.New("aapfile id generator is required")
		}
		s.newID = gen
		return nil
	}
}

// WithMaxPendingPerWorkspace overrides the PENDING_UPLOAD concurrency cap.
func WithMaxPendingPerWorkspace(max int) ServiceOption {
	return func(s *Service) error {
		if max < 0 {
			return errors.New("aapfile max pending must be non-negative")
		}
		s.maxPending = max
		return nil
	}
}

// WithMaxReadyBytesPerWorkspace overrides the READY total-bytes quota.
func WithMaxReadyBytesPerWorkspace(max int64) ServiceOption {
	return func(s *Service) error {
		if max < 0 {
			return errors.New("aapfile max ready bytes must be non-negative")
		}
		s.maxReadyBytes = max
		return nil
	}
}

// WithVirusScan configures the optional virus_scan pipeline stage.
func WithVirusScan(cfg VirusScanConfig) ServiceOption {
	return func(s *Service) error {
		s.virusScan = cfg
		return nil
	}
}

// WithMetrics injects an AAP file metrics collector (defaults to process-wide).
func WithMetrics(collector *metrics.AAPFileCollector) ServiceOption {
	return func(s *Service) error {
		if collector == nil {
			return errors.New("aapfile metrics collector is required")
		}
		s.metrics = collector
		return nil
	}
}

// NewService constructs the domain service.
func NewService(
	store FileStore,
	staging StagingStore,
	secure SecurePutter,
	options ...ServiceOption,
) (*Service, error) {
	if store == nil || staging == nil || secure == nil {
		return nil, errors.New("aapfile store, staging, and secure putter are required")
	}
	service := &Service{
		store: store, staging: staging, secure: secure,
		maxBytes: DefaultMaxBytes, stagingTTL: DefaultStagingTTL,
		presignTTL: DefaultPresignTTL, retention: DefaultRetention,
		maxPending: DefaultMaxPendingPerWorkspace,
		maxReadyBytes: DefaultMaxReadyBytesPerWorkspace,
		metrics: metrics.DefaultAAPFile(),
		now:     func() time.Time { return time.Now().UTC() },
		newID:   uuid.NewV7,
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("aapfile service option is required")
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

// Scope identifies workspace+agent for file APIs.
type Scope struct {
	WorkspaceID string
	AgentID     string
}

// CreateUploadIntentInput carries create-intent facts (ownership from principal).
type CreateUploadIntentInput struct {
	Scope     Scope
	Principal agentaccessauth.AAPAccessTokenPrincipal
	// ClientID and AgentPolicyVersion normally come from Authorization.Snapshot.
	// Create always writes SUBJECT_OWNED (KD-15); never POLICY_SHARED in v1.
	ClientID           string
	AgentPolicyVersion int64
	// FileID is optional; when set (idempotent create), used as the durable id.
	FileID    string
	Filename  string
	MediaType string
	SizeBytes int64
	SHA256    string // optional client-declared hex
	Purpose   string
}

// CreateUploadIntent allocates fileId + staging key + presigned PUT headers.
func (s *Service) CreateUploadIntent(
	ctx context.Context,
	input CreateUploadIntentInput,
) (UploadIntent, error) {
	if s == nil || s.store == nil || s.staging == nil || ctx == nil {
		return UploadIntent{}, ErrInvalid
	}
	input = normalizeCreateInput(input)
	if err := validateCreateInput(input, s.maxBytes); err != nil {
		return UploadIntent{}, err
	}

	ownership, err := buildOwnership(input)
	if err != nil {
		return UploadIntent{}, err
	}

	// PENDING concurrency cap (design §5.5.7).
	if s.maxPending > 0 {
		pending, countErr := s.store.CountPendingUploads(ctx, input.Scope.WorkspaceID)
		if countErr != nil {
			return UploadIntent{}, countErr
		}
		if pending >= s.maxPending {
			return UploadIntent{}, ErrPendingLimit
		}
	}
	// READY total-bytes quota (soft gate at create).
	if s.maxReadyBytes > 0 {
		readyBytes, sumErr := s.store.SumReadyBytes(ctx, input.Scope.WorkspaceID)
		if sumErr != nil {
			return UploadIntent{}, sumErr
		}
		if readyBytes+input.SizeBytes > s.maxReadyBytes {
			return UploadIntent{}, fmt.Errorf("%w: %s", ErrFailed, ErrorCodeSizeExceeded)
		}
	}

	var fileIDStr string
	if input.FileID != "" {
		if !validUUID(input.FileID) {
			return UploadIntent{}, ErrInvalid
		}
		fileIDStr = input.FileID
	} else {
		fileID, err := s.newID()
		if err != nil {
			return UploadIntent{}, fmt.Errorf("allocate file id: %w", err)
		}
		fileIDStr = fileID.String()
	}
	now := s.now().UTC()
	stagingKey := storedobject.AAPStagingObjectKey(input.Scope.WorkspaceID, fileIDStr)
	expiresAt := now.Add(s.stagingTTL)
	retentionUntil := now.Add(s.retention)

	var filename *string
	if input.Filename != "" {
		filename = &input.Filename
	}
	var declaredSHA *string
	if input.SHA256 != "" {
		declaredSHA = &input.SHA256
	}
	stagingKeyCopy := stagingKey

	file := File{
		ID:                     fileIDStr,
		WorkspaceID:            input.Scope.WorkspaceID,
		AgentID:                input.Scope.AgentID,
		ActorType:              ownership.ActorType,
		ActorID:                ownership.ActorID,
		ClientID:               ownership.ClientID,
		SubjectType:            ownership.SubjectType,
		SubjectID:              ownership.SubjectID,
		OwnershipMode:          OwnershipSubjectOwned,
		OwnershipPolicyVersion: ownership.PolicyVersion,
		Status:                 StatusPendingUpload,
		Filename:               filename,
		DeclaredMediaType:      input.MediaType,
		SizeBytes:              input.SizeBytes,
		SHA256:                 declaredSHA,
		StagingBucket:          storedobject.BucketAAPStaging,
		StagingObjectKey:       &stagingKeyCopy,
		StagingExpiresAt:       expiresAt,
		Purpose:                input.Purpose,
		ProcessingVersion:      1,
		RetentionUntil:         &retentionUntil,
	}

	created, err := s.store.InsertFile(ctx, file)
	if err != nil {
		return UploadIntent{}, err
	}
	if s.metrics != nil {
		s.metrics.IncCreate()
	}
	return s.presignForFile(ctx, created)
}

// PresignUpload regenerates a staging PUT for an existing PENDING_UPLOAD file
// (idempotent create replay). Never appears on GET DTO surfaces.
func (s *Service) PresignUpload(ctx context.Context, workspaceID, fileID string) (UploadIntent, error) {
	if s == nil || s.store == nil || s.staging == nil || ctx == nil {
		return UploadIntent{}, ErrInvalid
	}
	file, err := s.store.GetFile(ctx, workspaceID, fileID)
	if err != nil {
		return UploadIntent{}, err
	}
	if file.Status != StatusPendingUpload {
		// Already advanced: return file without upload (caller decides response shape).
		return UploadIntent{File: file}, nil
	}
	if !file.StagingExpiresAt.After(s.now().UTC()) {
		return UploadIntent{}, ErrExpired
	}
	return s.presignForFile(ctx, file)
}

func (s *Service) presignForFile(ctx context.Context, file File) (UploadIntent, error) {
	if file.StagingObjectKey == nil || strings.TrimSpace(*file.StagingObjectKey) == "" {
		return UploadIntent{}, fmt.Errorf("%w: missing staging key", ErrFailed)
	}
	stagingKey := *file.StagingObjectKey
	headers := http.Header{}
	headers.Set("Content-Type", file.DeclaredMediaType)
	headers.Set("Content-Length", strconv.FormatInt(file.SizeBytes, 10))
	signed, err := s.staging.PresignPutWithHeaders(
		ctx, file.StagingBucket, stagingKey, s.presignTTL, headers,
	)
	if err != nil {
		return UploadIntent{}, fmt.Errorf("presign aap staging put: %w", err)
	}
	return UploadIntent{
		File:      file,
		UploadURL: signed.String(),
		UploadHeaders: map[string]string{
			"Content-Type":   file.DeclaredMediaType,
			"Content-Length": strconv.FormatInt(file.SizeBytes, 10),
		},
		ExpiresAt: file.StagingExpiresAt.UTC(),
	}, nil
}

// CompleteUploadInput is the fast-path complete (no full-body encrypt).
type CompleteUploadInput struct {
	Scope  Scope
	FileID string
	// Optional re-declaration of sha256 (normalized lower hex).
	SHA256 string
}

// CompleteUploadResult is the file after CAS + promote enqueue.
type CompleteUploadResult struct {
	File File
	Job  ProcessingJob
}

// CompleteUpload stats staging, validates size / optional magic MIME, CAS to
// UPLOADED, and enqueues stage=promote. Does not encrypt the body.
func (s *Service) CompleteUpload(
	ctx context.Context,
	input CompleteUploadInput,
) (CompleteUploadResult, error) {
	if s == nil || s.store == nil || s.staging == nil || ctx == nil {
		return CompleteUploadResult{}, ErrInvalid
	}
	input.Scope.WorkspaceID = strings.TrimSpace(input.Scope.WorkspaceID)
	input.Scope.AgentID = strings.TrimSpace(input.Scope.AgentID)
	input.FileID = strings.TrimSpace(input.FileID)
	input.SHA256 = strings.ToLower(strings.TrimSpace(input.SHA256))
	if !validUUID(input.Scope.WorkspaceID) || !validUUID(input.Scope.AgentID) ||
		!validUUID(input.FileID) {
		return CompleteUploadResult{}, ErrInvalid
	}
	if input.SHA256 != "" && !validSHA256Hex(input.SHA256) {
		return CompleteUploadResult{}, ErrInvalid
	}

	file, err := s.store.GetFile(ctx, input.Scope.WorkspaceID, input.FileID)
	if err != nil {
		return CompleteUploadResult{}, err
	}
	if file.WorkspaceID != input.Scope.WorkspaceID || file.AgentID != input.Scope.AgentID {
		return CompleteUploadResult{}, ErrNotFound
	}

	// Idempotent complete for already-advanced files.
	if file.Status != StatusPendingUpload {
		if file.Status == StatusUploaded || file.Status == StatusProcessing ||
			file.Status == StatusReady {
			job, jobErr := s.store.GetJob(ctx, file.WorkspaceID, file.ID, StagePromote)
			if jobErr != nil {
				return CompleteUploadResult{}, jobErr
			}
			return CompleteUploadResult{File: file, Job: job}, nil
		}
		if file.Status == StatusExpired {
			return CompleteUploadResult{}, ErrExpired
		}
		return CompleteUploadResult{}, ErrConflict
	}

	now := s.now().UTC()
	if !file.StagingExpiresAt.After(now) {
		failed, markErr := s.store.MarkFileFailed(
			ctx, file.WorkspaceID, file.ID, ErrorCodeUploadExpired,
			"staging upload expired", file.ProcessingVersion,
		)
		if markErr == nil {
			file = failed
		}
		return CompleteUploadResult{}, ErrExpired
	}
	if file.StagingObjectKey == nil || strings.TrimSpace(*file.StagingObjectKey) == "" {
		return CompleteUploadResult{}, fmt.Errorf("%w: missing staging key", ErrFailed)
	}
	stagingKey := *file.StagingObjectKey

	info, err := s.staging.Stat(ctx, file.StagingBucket, stagingKey)
	if err != nil {
		return CompleteUploadResult{}, fmt.Errorf("%w: %v", ErrFailed, err)
	}
	if info.Size != file.SizeBytes {
		failed, markErr := s.store.MarkFileFailed(
			ctx, file.WorkspaceID, file.ID, ErrorCodeIntegrityMismatch,
			fmt.Sprintf("staging size %d does not match declared %d", info.Size, file.SizeBytes),
			file.ProcessingVersion,
		)
		if markErr == nil {
			_ = failed
		}
		return CompleteUploadResult{}, fmt.Errorf("%w: %s", ErrFailed, ErrorCodeIntegrityMismatch)
	}

	// Optional magic sample (first 512 bytes) for MIME mismatch.
	var detected *string
	if sample, sampleErr := s.readStagingSample(ctx, file.StagingBucket, stagingKey, 512); sampleErr == nil && len(sample) > 0 {
		magic := DetectMediaTypeFromSample(sample)
		if !mediaTypesCompatible(file.DeclaredMediaType, magic) {
			failed, markErr := s.store.MarkFileFailed(
				ctx, file.WorkspaceID, file.ID, ErrorCodeMediaTypeMismatch,
				fmt.Sprintf("declared %s does not match magic %s", file.DeclaredMediaType, magic),
				file.ProcessingVersion,
			)
			if markErr == nil {
				_ = failed
			}
			return CompleteUploadResult{}, fmt.Errorf("%w: %s", ErrFailed, ErrorCodeMediaTypeMismatch)
		}
		if AllowedMediaType(magic) {
			detected = &magic
		}
	}

	// Persist optional complete-time sha256 declaration onto the row via failure path only;
	// promote uses create-time or complete-time declared hash from file.SHA256 if set.
	// If complete supplies a different sha256, update via failed path if conflict — for
	// v1 we require match with create declaration when both set.
	if input.SHA256 != "" && file.SHA256 != nil && *file.SHA256 != "" && *file.SHA256 != input.SHA256 {
		return CompleteUploadResult{}, fmt.Errorf("%w: %s", ErrFailed, ErrorCodeIntegrityMismatch)
	}

	updated, job, err := s.store.CompleteUploadCAS(
		ctx, file.WorkspaceID, file.ID, file.ProcessingVersion, detected,
	)
	if err != nil {
		return CompleteUploadResult{}, err
	}
	if s.metrics != nil {
		s.metrics.IncComplete()
	}
	return CompleteUploadResult{File: updated, Job: job}, nil
}

// GetFile returns the file fact by workspace + id (no auth matrix here).
func (s *Service) GetFile(ctx context.Context, workspaceID, fileID string) (File, error) {
	if s == nil || s.store == nil || ctx == nil {
		return File{}, ErrInvalid
	}
	workspaceID = strings.TrimSpace(workspaceID)
	fileID = strings.TrimSpace(fileID)
	if !validUUID(workspaceID) || !validUUID(fileID) {
		return File{}, ErrInvalid
	}
	return s.store.GetFile(ctx, workspaceID, fileID)
}

// retentionClearer is optional store surface for KD-16 retention promote.
type retentionClearer interface {
	ClearRetentionUntilCAS(context.Context, string, string, int64) (File, error)
}

// PromoteRetentionOnReference clears EXPIRING retention on first successful
// createRun reference of a READY file (KD-16). Idempotent when already permanent.
func (s *Service) PromoteRetentionOnReference(ctx context.Context, workspaceID, fileID string) error {
	if s == nil || s.store == nil || ctx == nil {
		return ErrInvalid
	}
	workspaceID = strings.TrimSpace(workspaceID)
	fileID = strings.TrimSpace(fileID)
	if !validUUID(workspaceID) || !validUUID(fileID) {
		return ErrInvalid
	}
	file, err := s.store.GetFile(ctx, workspaceID, fileID)
	if err != nil {
		return err
	}
	if file.Status != StatusReady {
		return ErrNotReady
	}
	if file.RetentionUntil == nil {
		return nil
	}
	clearer, ok := s.store.(retentionClearer)
	if !ok {
		return nil
	}
	_, err = clearer.ClearRetentionUntilCAS(ctx, workspaceID, fileID, file.ProcessingVersion)
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}

// ListProcessingStages returns non-secret stage projections for GET file.
func (s *Service) ListProcessingStages(
	ctx context.Context,
	workspaceID, fileID string,
) ([]ProcessingStage, error) {
	if s == nil || s.store == nil || ctx == nil {
		return nil, ErrInvalid
	}
	jobs, err := s.store.ListJobs(ctx, workspaceID, fileID)
	if err != nil {
		return nil, err
	}
	stages := make([]ProcessingStage, 0, len(jobs))
	for _, job := range jobs {
		stages = append(stages, ProcessingStage{Stage: job.Stage, Status: job.Status})
	}
	return stages, nil
}

// MintDownloadTokenInput creates an opaque download token for a READY file.
type MintDownloadTokenInput struct {
	Scope     Scope
	FileID    string
	Purpose   string
	SingleUse bool
	// CreatedBy is a stable non-secret actor label (e.g. SP id).
	CreatedBy string
	TTL       time.Duration
}

// MintDownloadTokenResult is returned to HTTP for path B mint.
type MintDownloadTokenResult struct {
	Token DownloadToken
	File  File
}

// MintDownloadToken inserts an opaque DB token for a READY file (KD-13).
func (s *Service) MintDownloadToken(
	ctx context.Context,
	input MintDownloadTokenInput,
) (MintDownloadTokenResult, error) {
	if s == nil || s.store == nil || ctx == nil {
		return MintDownloadTokenResult{}, ErrInvalid
	}
	input.Scope.WorkspaceID = strings.TrimSpace(input.Scope.WorkspaceID)
	input.Scope.AgentID = strings.TrimSpace(input.Scope.AgentID)
	input.FileID = strings.TrimSpace(input.FileID)
	input.Purpose = strings.ToLower(strings.TrimSpace(input.Purpose))
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	if !validUUID(input.Scope.WorkspaceID) || !validUUID(input.Scope.AgentID) ||
		!validUUID(input.FileID) || input.CreatedBy == "" {
		return MintDownloadTokenResult{}, ErrInvalid
	}
	switch input.Purpose {
	case DownloadPurposeClientContent, DownloadPurposeToolInvoke, DownloadPurposeProcessorDelivery:
	default:
		return MintDownloadTokenResult{}, ErrInvalid
	}
	ttl := input.TTL
	if ttl <= 0 {
		switch input.Purpose {
		case DownloadPurposeProcessorDelivery:
			ttl = DefaultProcessorDeliveryTokenTTL
		default:
			ttl = DefaultClientContentTokenTTL
		}
	}
	if ttl > MaxDownloadTokenTTL {
		ttl = MaxDownloadTokenTTL
	}

	file, err := s.store.GetFile(ctx, input.Scope.WorkspaceID, input.FileID)
	if err != nil {
		return MintDownloadTokenResult{}, err
	}
	if file.WorkspaceID != input.Scope.WorkspaceID || file.AgentID != input.Scope.AgentID {
		return MintDownloadTokenResult{}, ErrNotFound
	}
	if file.Status != StatusReady || file.StoredObjectID == nil ||
		strings.TrimSpace(*file.StoredObjectID) == "" {
		return MintDownloadTokenResult{}, ErrNotReady
	}

	tokenID, err := s.newID()
	if err != nil {
		return MintDownloadTokenResult{}, fmt.Errorf("allocate download token id: %w", err)
	}
	jti, err := s.newID()
	if err != nil {
		return MintDownloadTokenResult{}, fmt.Errorf("allocate download jti: %w", err)
	}
	now := s.now().UTC()
	maxBytes := file.SizeBytes
	token := DownloadToken{
		ID: tokenID.String(), WorkspaceID: file.WorkspaceID, FileID: file.ID,
		Purpose: input.Purpose, JTI: jti.String(), SingleUse: input.SingleUse,
		MaxBytes: &maxBytes, ExpiresAt: now.Add(ttl), CreatedBy: input.CreatedBy,
	}
	// tool_invoke and processor_delivery are always single-use (§5.5.4 / IC-07).
	switch input.Purpose {
	case DownloadPurposeToolInvoke, DownloadPurposeProcessorDelivery:
		token.SingleUse = true
	}
	created, err := s.store.InsertDownloadToken(ctx, token)
	if err != nil {
		return MintDownloadTokenResult{}, err
	}
	return MintDownloadTokenResult{Token: created, File: file}, nil
}

// ResolveDownloadToken loads a non-expired, unconsumed token and its file.
// Does not consume single_use (caller must ConsumeDownloadToken before stream).
func (s *Service) ResolveDownloadToken(
	ctx context.Context,
	tokenID string,
) (DownloadToken, File, error) {
	return s.ResolveDownloadTokenForPurpose(ctx, tokenID, "")
}

// ResolveDownloadTokenForPurpose is ResolveDownloadToken with optional purpose
// binding. When expectedPurpose is non-empty it must match the token purpose
// exactly; mismatch conceals as ErrNotFound (IC-07).
func (s *Service) ResolveDownloadTokenForPurpose(
	ctx context.Context,
	tokenID string,
	expectedPurpose string,
) (DownloadToken, File, error) {
	if s == nil || s.store == nil || ctx == nil {
		return DownloadToken{}, File{}, ErrInvalid
	}
	tokenID = strings.TrimSpace(tokenID)
	if !validUUID(tokenID) {
		return DownloadToken{}, File{}, ErrInvalid
	}
	expectedPurpose = strings.ToLower(strings.TrimSpace(expectedPurpose))
	if expectedPurpose != "" {
		switch expectedPurpose {
		case DownloadPurposeClientContent, DownloadPurposeToolInvoke, DownloadPurposeProcessorDelivery:
		default:
			return DownloadToken{}, File{}, ErrInvalid
		}
	}
	token, err := s.store.GetDownloadToken(ctx, tokenID)
	if err != nil {
		return DownloadToken{}, File{}, err
	}
	// Reject unknown/legacy purpose values even if the row exists.
	switch token.Purpose {
	case DownloadPurposeClientContent, DownloadPurposeToolInvoke, DownloadPurposeProcessorDelivery:
	default:
		return DownloadToken{}, File{}, ErrNotFound
	}
	if expectedPurpose != "" && token.Purpose != expectedPurpose {
		return DownloadToken{}, File{}, ErrNotFound
	}
	now := s.now().UTC()
	if !token.ExpiresAt.After(now) {
		return DownloadToken{}, File{}, ErrNotFound
	}
	if token.ConsumedAt != nil {
		return DownloadToken{}, File{}, ErrNotFound
	}
	file, err := s.store.GetFile(ctx, token.WorkspaceID, token.FileID)
	if err != nil {
		return DownloadToken{}, File{}, err
	}
	if file.StoredObjectID == nil || strings.TrimSpace(*file.StoredObjectID) == "" {
		return DownloadToken{}, File{}, ErrNotReady
	}
	// processor_delivery runs while the file is still PROCESSING (webhook stages
	// complete before READY). client_content / tool_invoke require READY.
	switch token.Purpose {
	case DownloadPurposeProcessorDelivery:
		if file.Status != StatusReady && file.Status != StatusProcessing {
			return DownloadToken{}, File{}, ErrNotReady
		}
	default:
		if file.Status != StatusReady {
			return DownloadToken{}, File{}, ErrNotReady
		}
	}
	return token, file, nil
}

// ConsumeDownloadToken marks a single_use token consumed (CAS).
// Concurrent double-consume: only one CAS winner; loser gets ErrNotFound.
func (s *Service) ConsumeDownloadToken(ctx context.Context, tokenID string) error {
	if s == nil || s.store == nil || ctx == nil {
		return ErrInvalid
	}
	tokenID = strings.TrimSpace(tokenID)
	if !validUUID(tokenID) {
		return ErrInvalid
	}
	token, err := s.store.GetDownloadToken(ctx, tokenID)
	if err != nil {
		return err
	}
	if !token.SingleUse {
		return nil
	}
	now := s.now().UTC()
	if !token.ExpiresAt.After(now) || token.ConsumedAt != nil {
		return ErrNotFound
	}
	_, err = s.store.ConsumeDownloadToken(ctx, tokenID)
	return err
}

// PurgeExpiredDownloadTokens deletes expired download token rows.
// Intended for ops/GC loops (IC-07; full staging GC is IC-11).
func (s *Service) PurgeExpiredDownloadTokens(ctx context.Context, limit int) (int, error) {
	if s == nil || s.store == nil || ctx == nil {
		return 0, ErrInvalid
	}
	if limit <= 0 {
		limit = DefaultDownloadTokenPurgeBatch
	}
	return s.store.PurgeExpiredDownloadTokens(ctx, limit)
}

// ValidDownloadPurpose reports whether purpose is one of the KD-13 values.
func ValidDownloadPurpose(purpose string) bool {
	switch strings.ToLower(strings.TrimSpace(purpose)) {
	case DownloadPurposeClientContent, DownloadPurposeToolInvoke, DownloadPurposeProcessorDelivery:
		return true
	default:
		return false
	}
}

// Promote runs the promote stage in-process (worker entry for IC-02 tests).
// Steps: open staging → hash → verify declared sha256 → SecureStore.Put →
// set stored_object_id → clear staging markers → mime_detect → READY if no
// further required stages (v1: none beyond mime_detect).
func (s *Service) Promote(ctx context.Context, workspaceID, fileID string) (File, error) {
	if s == nil || s.store == nil || s.staging == nil || s.secure == nil || ctx == nil {
		return File{}, ErrInvalid
	}
	workspaceID = strings.TrimSpace(workspaceID)
	fileID = strings.TrimSpace(fileID)
	if !validUUID(workspaceID) || !validUUID(fileID) {
		return File{}, ErrInvalid
	}

	file, err := s.store.GetFile(ctx, workspaceID, fileID)
	if err != nil {
		return File{}, err
	}
	// No-op success when already promoted.
	if file.StoredObjectID != nil && strings.TrimSpace(*file.StoredObjectID) != "" {
		return file, nil
	}
	if file.Status != StatusUploaded && file.Status != StatusProcessing {
		return File{}, ErrConflict
	}
	if file.StagingObjectKey == nil || strings.TrimSpace(*file.StagingObjectKey) == "" {
		return File{}, fmt.Errorf("%w: missing staging key", ErrFailed)
	}
	stagingKey := *file.StagingObjectKey

	body, info, err := s.staging.Open(ctx, file.StagingBucket, stagingKey)
	if err != nil {
		return File{}, fmt.Errorf("open staging for promote: %w", err)
	}
	defer body.Close()

	if info.Size != file.SizeBytes {
		return s.failPromote(ctx, file, ErrorCodeIntegrityMismatch,
			fmt.Sprintf("staging size %d does not match declared %d", info.Size, file.SizeBytes))
	}

	hasher := sha256.New()
	limited := io.LimitReader(body, file.SizeBytes+1)
	written, err := io.Copy(hasher, limited)
	if err != nil {
		return File{}, fmt.Errorf("hash staging body: %w", err)
	}
	if written != file.SizeBytes {
		return s.failPromote(ctx, file, ErrorCodeIntegrityMismatch,
			fmt.Sprintf("read size %d does not match declared %d", written, file.SizeBytes))
	}
	computed := hex.EncodeToString(hasher.Sum(nil))

	if file.SHA256 != nil && strings.TrimSpace(*file.SHA256) != "" {
		declared := strings.ToLower(strings.TrimSpace(*file.SHA256))
		if declared != computed {
			return s.failPromote(ctx, file, ErrorCodeIntegrityMismatch,
				"declared sha256 does not match staging body")
		}
	}

	// Re-open for SecureStore.Put (body already consumed for hashing).
	body2, _, err := s.staging.Open(ctx, file.StagingBucket, stagingKey)
	if err != nil {
		return File{}, fmt.Errorf("re-open staging for put: %w", err)
	}
	defer body2.Close()

	// Sample for mime_detect: peek first bytes then reconstruct full reader.
	sample := make([]byte, 512)
	n, readErr := io.ReadFull(body2, sample)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return File{}, fmt.Errorf("sample staging for mime: %w", readErr)
	}
	sample = sample[:n]
	detected := DetectMediaTypeFromSample(sample)
	if !AllowedMediaType(detected) {
		// Fall back to declared when magic is weak but declared is allowlisted.
		if AllowedMediaType(file.DeclaredMediaType) {
			detected = file.DeclaredMediaType
		}
	}
	fullReader := io.MultiReader(bytes.NewReader(sample), body2)

	objectID, err := s.newID()
	if err != nil {
		return File{}, fmt.Errorf("allocate stored object id: %w", err)
	}
	retentionUntil := s.now().UTC().Add(s.retention)
	if file.RetentionUntil != nil {
		retentionUntil = file.RetentionUntil.UTC()
	}

	put, err := s.secure.Put(ctx, storedobject.PutInput{
		ID: objectID.String(), WorkspaceID: file.WorkspaceID,
		Kind: storedobject.KindAAPFile, ContentType: file.DeclaredMediaType,
		SizeBytes: file.SizeBytes, SHA256: computed,
		Classification: storedobject.ClassificationSensitive,
		RetentionMode:  storedobject.RetentionExpiring,
		RetentionUntil: &retentionUntil,
		CreatedByType:  storedobject.CreatorServicePrincipal,
		CreatedByID:    file.ActorID,
		Reader:         fullReader,
	})
	if err != nil {
		return File{}, fmt.Errorf("secure store put aap file: %w", err)
	}

	// Best-effort staging delete; on failure retain staging_object_key for GC (KD-21).
	stagingDeleted := true
	if delErr := s.staging.Delete(ctx, file.StagingBucket, stagingKey); delErr != nil {
		stagingDeleted = false
	}

	// Promote always lands in PROCESSING; mime_detect is recorded SUCCEEDED inline.
	// Follow-on stages (virus_scan / webhooks) are enqueued when the store supports it.
	// READY is decided only when required stages are terminal (EvaluateReady).
	updated, err := s.store.ApplyPromoteSuccess(
		ctx, file.WorkspaceID, file.ID, put.ID, computed, detected,
		file.ProcessingVersion, false, &retentionUntil, stagingDeleted,
	)
	if err != nil {
		return File{}, err
	}
	if err := s.enqueueFollowOnStages(ctx, updated); err != nil {
		return File{}, err
	}
	return s.EvaluateReady(ctx, updated.WorkspaceID, updated.ID)
}

func (s *Service) failPromote(
	ctx context.Context,
	file File,
	code, message string,
) (File, error) {
	failed, err := s.store.MarkPromoteFailed(
		ctx, file.WorkspaceID, file.ID, code, message, file.ProcessingVersion,
	)
	if err != nil {
		return File{}, err
	}
	return failed, fmt.Errorf("%w: %s", ErrFailed, code)
}

// pipeline returns the optional pipeline store when FileStore implements it.
func (s *Service) pipeline() pipelineStore {
	if s == nil || s.store == nil {
		return nil
	}
	p, _ := s.store.(pipelineStore)
	return p
}

// enqueueFollowOnStages inserts virus_scan + webhook jobs after promote.
// mime_detect is already SUCCEEDED via ApplyPromoteSuccess.
func (s *Service) enqueueFollowOnStages(ctx context.Context, file File) error {
	pipe := s.pipeline()
	if pipe == nil {
		return nil
	}
	if s.virusScan.Enabled {
		_, err := pipe.InsertJob(ctx, ProcessingJob{
			WorkspaceID: file.WorkspaceID,
			FileID:      file.ID,
			Stage:       StageVirusScan,
			Status:      JobPending,
			Result:      MarshalJobMeta(s.virusScan.Required, nil),
		})
		if err != nil {
			return fmt.Errorf("enqueue virus_scan: %w", err)
		}
	}
	processors, err := pipe.ListEnabledProcessors(ctx, file.WorkspaceID)
	if err != nil {
		return fmt.Errorf("list processors: %w", err)
	}
	for _, proc := range processors {
		if !processorWantsUploaded(proc) {
			continue
		}
		_, err := pipe.InsertJob(ctx, ProcessingJob{
			WorkspaceID: file.WorkspaceID,
			FileID:      file.ID,
			Stage:       WebhookStageName(proc.ProcessorID),
			Status:      JobPending,
			Result: MarshalJobMeta(proc.Required, map[string]any{
				"processorId": proc.ProcessorID,
			}),
		})
		if err != nil {
			return fmt.Errorf("enqueue webhook %s: %w", proc.ProcessorID, err)
		}
	}
	return nil
}

func processorWantsUploaded(proc WorkspaceFileProcessor) bool {
	if len(proc.Events) == 0 {
		return true
	}
	for _, event := range proc.Events {
		if strings.TrimSpace(event) == ProcessorEventUploaded {
			return true
		}
	}
	return false
}

// EvaluateReady sets READY when stored_object_id is set and every required
// stage is SUCCEEDED|SKIPPED; sets FAILED when a required stage FAILED|TIMED_OUT.
func (s *Service) EvaluateReady(ctx context.Context, workspaceID, fileID string) (File, error) {
	if s == nil || s.store == nil || ctx == nil {
		return File{}, ErrInvalid
	}
	file, err := s.store.GetFile(ctx, workspaceID, fileID)
	if err != nil {
		return File{}, err
	}
	if file.Status == StatusReady || file.Status == StatusFailed || file.Status == StatusExpired {
		return file, nil
	}
	if file.StoredObjectID == nil || strings.TrimSpace(*file.StoredObjectID) == "" {
		return file, nil
	}
	jobs, err := s.store.ListJobs(ctx, workspaceID, fileID)
	if err != nil {
		return File{}, err
	}
	requiredFailed := false
	requiredOpen := false
	for _, job := range jobs {
		required := JobResultRequired(job.Result, job.Stage)
		if !required {
			continue
		}
		switch job.Status {
		case JobSucceeded, JobSkipped:
			// ok
		case JobFailed, JobTimedOut:
			requiredFailed = true
		default:
			// PENDING, RUNNING, DELIVERED, …
			requiredOpen = true
		}
	}
	pipe := s.pipeline()
	if requiredFailed {
		if pipe == nil {
			// Best-effort without CAS helper: leave PROCESSING (worker uses Repository).
			return file, nil
		}
		failed, markErr := pipe.MarkFileProcessingFailedCAS(
			ctx, file.WorkspaceID, file.ID,
			ErrorCodeProcessingFailed, "required processing stage failed",
			file.ProcessingVersion,
		)
		if markErr != nil {
			if errors.Is(markErr, ErrConflict) {
				return s.store.GetFile(ctx, workspaceID, fileID)
			}
			return File{}, markErr
		}
		return failed, nil
	}
	if requiredOpen {
		return file, nil
	}
	// All required stages terminal success → READY.
	if pipe == nil {
		// Memory/test stores without pipeline: ApplyPromoteSuccess ready path used historically.
		// Try re-read; if still PROCESSING, attempt ApplyPromoteSuccess-style ready via conflict.
		return file, nil
	}
	ready, markErr := pipe.MarkFileReadyCAS(ctx, file.WorkspaceID, file.ID, file.ProcessingVersion)
	if markErr != nil {
		if errors.Is(markErr, ErrConflict) {
			return s.store.GetFile(ctx, workspaceID, fileID)
		}
		return File{}, markErr
	}
	return ready, nil
}

// HandleProcessorCallback applies a verified partner callback for deliveryID.
type HandleProcessorCallbackInput struct {
	DeliveryID  string
	Body        []byte
	// Signature already verified by HTTP layer; body is raw JSON.
}

// HandleProcessorCallbackResult is returned after CAS.
type HandleProcessorCallbackResult struct {
	Job  ProcessingJob
	File File
}

// HandleProcessorCallback CAS-updates the job and re-evaluates READY.
func (s *Service) HandleProcessorCallback(
	ctx context.Context,
	input HandleProcessorCallbackInput,
) (HandleProcessorCallbackResult, error) {
	if s == nil || s.store == nil || ctx == nil {
		return HandleProcessorCallbackResult{}, ErrInvalid
	}
	pipe := s.pipeline()
	if pipe == nil {
		return HandleProcessorCallbackResult{}, ErrInvalid
	}
	input.DeliveryID = strings.TrimSpace(input.DeliveryID)
	if !validUUID(input.DeliveryID) {
		return HandleProcessorCallbackResult{}, ErrInvalid
	}
	parsed, err := ParseCallbackBody(input.Body)
	if err != nil {
		return HandleProcessorCallbackResult{}, err
	}
	if _, err := DecodedArtifactBytes(parsed); err != nil {
		return HandleProcessorCallbackResult{}, err
	}
	newStatus := JobSucceeded
	if parsed.Status == "failed" {
		newStatus = JobFailed
	}
	resultExtra := map[string]any{
		"callbackStatus": parsed.Status,
		"processorId":    parsed.ProcessorID,
	}
	if parsed.Attributes != nil {
		resultExtra["attributes"] = parsed.Attributes
	}
	resultJSON, _ := json.Marshal(resultExtra)
	job, file, err := pipe.ApplyCallbackCAS(ctx, input.DeliveryID, newStatus, resultJSON)
	if err != nil {
		return HandleProcessorCallbackResult{}, err
	}
	// Persist small base64 artifacts as AAP_FILE_DERIVED when present.
	if newStatus == JobSucceeded && len(parsed.Artifacts) > 0 && s.secure != nil {
		_ = s.storeCallbackArtifacts(ctx, file, parsed)
	}
	evaluated, evalErr := s.EvaluateReady(ctx, file.WorkspaceID, file.ID)
	if evalErr != nil {
		return HandleProcessorCallbackResult{Job: job, File: file}, nil
	}
	return HandleProcessorCallbackResult{Job: job, File: evaluated}, nil
}

func (s *Service) storeCallbackArtifacts(
	ctx context.Context,
	file File,
	parsed ProcessorCallbackBody,
) error {
	pipe := s.pipeline()
	if pipe == nil {
		return nil
	}
	for _, art := range parsed.Artifacts {
		rawB64 := strings.TrimSpace(art.ContentBase64)
		if rawB64 == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(rawB64)
		if err != nil {
			// Try raw URL encoding.
			decoded, err = base64.RawStdEncoding.DecodeString(rawB64)
			if err != nil {
				continue
			}
		}
		if len(decoded) > CallbackArtifactMaxBytes {
			return ErrArtifactTooLarge
		}
		objectID, err := s.newID()
		if err != nil {
			return err
		}
		sum := sha256.Sum256(decoded)
		put, err := s.secure.Put(ctx, storedobject.PutInput{
			ID: objectID.String(), WorkspaceID: file.WorkspaceID,
			Kind: storedobject.KindAAPFileDerived, ContentType: art.MediaType,
			SizeBytes: int64(len(decoded)), SHA256: hex.EncodeToString(sum[:]),
			Classification: storedobject.ClassificationSensitive,
			RetentionMode:  storedobject.RetentionExpiring,
			RetentionUntil: file.RetentionUntil,
			CreatedByType:  storedobject.CreatorSystem,
			CreatedByID:    file.ActorID,
			Reader:         bytes.NewReader(decoded),
		})
		if err != nil {
			return err
		}
		kind := strings.TrimSpace(art.Kind)
		if kind == "" {
			kind = "PROCESSOR_ARTIFACT"
		}
		media := strings.TrimSpace(art.MediaType)
		if media == "" {
			media = "application/octet-stream"
		}
		_, _ = pipe.InsertArtifact(ctx, FileArtifact{
			WorkspaceID: file.WorkspaceID, FileID: file.ID,
			Kind: kind, MediaType: media, StoredObjectID: put.ID,
			ProcessorID: parsed.ProcessorID,
		})
	}
	return nil
}

// LookupDeliveryForCallback loads job + processor for HMAC verification.
func (s *Service) LookupDeliveryForCallback(
	ctx context.Context,
	deliveryID string,
) (ProcessingJob, WorkspaceFileProcessor, File, error) {
	if s == nil || s.store == nil {
		return ProcessingJob{}, WorkspaceFileProcessor{}, File{}, ErrInvalid
	}
	pipe := s.pipeline()
	if pipe == nil {
		return ProcessingJob{}, WorkspaceFileProcessor{}, File{}, ErrInvalid
	}
	deliveryID = strings.TrimSpace(deliveryID)
	if !validUUID(deliveryID) {
		return ProcessingJob{}, WorkspaceFileProcessor{}, File{}, ErrInvalid
	}
	job, err := pipe.GetJobByDeliveryID(ctx, deliveryID)
	if err != nil {
		return ProcessingJob{}, WorkspaceFileProcessor{}, File{}, err
	}
	processorID := ProcessorIDFromStage(job.Stage)
	if processorID == "" {
		// Fallback from result JSON.
		var meta struct {
			ProcessorID string `json:"processorId"`
		}
		_ = json.Unmarshal(job.Result, &meta)
		processorID = meta.ProcessorID
	}
	if processorID == "" {
		return ProcessingJob{}, WorkspaceFileProcessor{}, File{}, ErrNotFound
	}
	proc, err := pipe.GetProcessor(ctx, job.WorkspaceID, processorID)
	if err != nil {
		return ProcessingJob{}, WorkspaceFileProcessor{}, File{}, err
	}
	file, err := s.store.GetFile(ctx, job.WorkspaceID, job.FileID)
	if err != nil {
		return ProcessingJob{}, WorkspaceFileProcessor{}, File{}, err
	}
	return job, proc, file, nil
}

func (s *Service) readStagingSample(
	ctx context.Context,
	bucket, key string,
	limit int64,
) ([]byte, error) {
	body, _, err := s.staging.Open(ctx, bucket, key)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	buf := make([]byte, limit)
	n, err := io.ReadFull(io.LimitReader(body, limit), buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return buf[:n], nil
}

type ownershipFacts struct {
	ActorType     string
	ActorID       string
	ClientID      string
	SubjectType   *string
	SubjectID     *string
	PolicyVersion int64
}

func buildOwnership(input CreateUploadIntentInput) (ownershipFacts, error) {
	sp := strings.TrimSpace(input.Principal.ServicePrincipalID)
	principalID := strings.TrimSpace(input.Principal.PrincipalID)
	clientID := strings.TrimSpace(input.ClientID)
	if !validUUID(sp) || !validUUID(principalID) || !validUUID(clientID) {
		return ownershipFacts{}, ErrInvalid
	}
	if input.AgentPolicyVersion < 1 {
		return ownershipFacts{}, ErrInvalid
	}
	facts := ownershipFacts{
		ActorType:     ActorServicePrincipal,
		ActorID:       sp,
		ClientID:      clientID,
		PolicyVersion: input.AgentPolicyVersion,
	}
	// Client Credentials: PrincipalID == ServicePrincipalID → null subject.
	// Token Exchange: External Subject principal id → subject populated.
	if principalID != sp {
		subjectType := SubjectExternal
		facts.SubjectType = &subjectType
		id := principalID
		facts.SubjectID = &id
	}
	return facts, nil
}

func normalizeCreateInput(input CreateUploadIntentInput) CreateUploadIntentInput {
	input.Scope.WorkspaceID = strings.TrimSpace(input.Scope.WorkspaceID)
	input.Scope.AgentID = strings.TrimSpace(input.Scope.AgentID)
	input.ClientID = strings.TrimSpace(input.ClientID)
	input.FileID = strings.ToLower(strings.TrimSpace(input.FileID))
	input.Filename = strings.TrimSpace(input.Filename)
	input.MediaType = strings.TrimSpace(input.MediaType)
	input.SHA256 = strings.ToLower(strings.TrimSpace(input.SHA256))
	input.Purpose = strings.ToUpper(strings.TrimSpace(input.Purpose))
	input.Principal.PrincipalID = strings.TrimSpace(input.Principal.PrincipalID)
	input.Principal.ServicePrincipalID = strings.TrimSpace(input.Principal.ServicePrincipalID)
	if input.Purpose == "" {
		input.Purpose = PurposeGeneral
	}
	if media, err := NormalizeMediaType(input.MediaType); err == nil {
		input.MediaType = media
	}
	return input
}

func validateCreateInput(input CreateUploadIntentInput, maxBytes int64) error {
	if !validUUID(input.Scope.WorkspaceID) || !validUUID(input.Scope.AgentID) {
		return ErrInvalid
	}
	if input.SizeBytes < 1 {
		return ErrInvalid
	}
	if input.SizeBytes > maxBytes {
		return fmt.Errorf("%w: %s", ErrFailed, ErrorCodeSizeExceeded)
	}
	if !AllowedMediaType(input.MediaType) {
		return fmt.Errorf("%w: %s", ErrFailed, ErrorCodeMediaTypeDenied)
	}
	if input.SHA256 != "" && !validSHA256Hex(input.SHA256) {
		return ErrInvalid
	}
	switch input.Purpose {
	case PurposeGeneral, PurposeVision, PurposeDocument, PurposeToolInput:
	default:
		return ErrInvalid
	}
	if input.AgentPolicyVersion < 1 || !validUUID(input.ClientID) {
		return ErrInvalid
	}
	if !validUUID(input.Principal.PrincipalID) || !validUUID(input.Principal.ServicePrincipalID) {
		return ErrInvalid
	}
	return nil
}

func validSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
