// Package aapfile owns the AAP file domain facts and upload state machine.
// HTTP routes, auth scopes, and pipeline workers land in later IC items.
package aapfile

import (
	"errors"
	"strings"
	"time"
)

// Lifecycle statuses for aap_files.status.
const (
	StatusPendingUpload = "PENDING_UPLOAD"
	StatusUploaded      = "UPLOADED"
	StatusProcessing    = "PROCESSING"
	StatusReady         = "READY"
	StatusFailed        = "FAILED"
	StatusExpired       = "EXPIRED"
)

// Ownership modes (v1 create always writes SUBJECT_OWNED; KD-15).
const (
	OwnershipSubjectOwned = "SUBJECT_OWNED"
	OwnershipPolicyShared = "POLICY_SHARED"
)

const (
	ActorServicePrincipal = "SERVICE_PRINCIPAL"
	SubjectExternal       = "EXTERNAL_SUBJECT"
)

// File purposes.
const (
	PurposeGeneral     = "GENERAL"
	PurposeVision      = "VISION"
	PurposeDocument    = "DOCUMENT"
	PurposeToolInput   = "TOOL_INPUT"
	PurposeAgentOutput = "AGENT_OUTPUT"
)

// Outbound ingest / publish limits (design §5.1).
const (
	MaxPublishTextBytes     = 256 << 10
	MaxOutboundFilesPerTurn = 8
)

// PublishAttachmentToolName is the v1 text-only platform publish tool.
const PublishAttachmentToolName = "actweave.publish_attachment"

// ReadAttachmentToolName is the v1 inbound PDF read tool.
const ReadAttachmentToolName = "actweave.read_attachment"

// MaxReadTextBytes is the UTF-8 cap on read_attachment result text (KD-IR-10).
const MaxReadTextBytes = MaxPublishTextBytes

// PDF read tool page limits (KD-IR-10).
const (
	PDFDefaultEndPage  = 10
	PDFMaxPagesPerCall = 20
	PDFExtractTimeout  = 15 * time.Second
)

// PublishAttachmentMediaTypes is the v1 tool mediaType enum (text only).
var PublishAttachmentMediaTypes = []string{
	"text/plain", "text/csv", "text/markdown", "application/json",
}

// Built-in processing stages.
const (
	StagePromote    = "promote"
	StageMIMEDetect = "mime_detect"
	StageVirusScan  = "virus_scan"
	// StageWebhookPrefix + processor_id forms webhook stage names (e.g. webhook:partner-dlp).
	StageWebhookPrefix = "webhook:"
)

// Job statuses.
const (
	JobPending   = "PENDING"
	JobRunning   = "RUNNING"
	JobDelivered = "DELIVERED"
	JobSucceeded = "SUCCEEDED"
	JobFailed    = "FAILED"
	JobSkipped   = "SKIPPED"
	JobTimedOut  = "TIMED_OUT"
)

// Error codes written onto aap_files / returned by the service.
const (
	ErrorCodeIntegrityMismatch = "FILE_INTEGRITY_MISMATCH"
	ErrorCodeMediaTypeMismatch = "FILE_MEDIA_TYPE_MISMATCH"
	ErrorCodeMediaTypeDenied   = "FILE_MEDIA_TYPE_DENIED"
	ErrorCodeSizeExceeded      = "FILE_SIZE_EXCEEDED"
	ErrorCodeUploadExpired     = "FILE_UPLOAD_EXPIRED"
	ErrorCodeProcessingFailed  = "FILE_PROCESSING_FAILED"
	ErrorCodeStagingNotFound   = "FILE_STAGING_NOT_FOUND"
	ErrorCodeNotFound          = "FILE_NOT_FOUND"
	ErrorCodeNotReady          = "FILE_NOT_READY"
	ErrorCodeInvalid           = "FILE_INVALID"
	ErrorCodeConflict          = "FILE_CONFLICT"
	ErrorCodePendingLimit      = "FILE_PENDING_LIMIT"
	ErrorCodeFeatureDisabled   = "FILE_FEATURE_DISABLED"
	ErrorCodeOutboundTurnLimit = "FILE_OUTBOUND_TURN_LIMIT"
	// Processor callback errors (design §5.5.5 / §5.10.2).
	ErrorCodeCallbackLate         = "FILE_PROCESSOR_CALLBACK_LATE"
	ErrorCodeCallbackInvalid      = "FILE_PROCESSOR_CALLBACK_INVALID"
	ErrorCodeCallbackUnauthorized = "FILE_PROCESSOR_CALLBACK_UNAUTHORIZED"
	ErrorCodeArtifactTooLarge     = "FILE_PROCESSOR_ARTIFACT_TOO_LARGE"
	ErrorCodeWebhookSSRF          = "FILE_PROCESSOR_SSRF_DENIED"
	ErrorCodeWebhookDelivery      = "FILE_PROCESSOR_DELIVERY_FAILED"
)

// Processor / callback limits (design §5.5.5).
const (
	CallbackBodyMaxBytes      = 384 << 10 // 384 KiB wire body
	CallbackArtifactMaxBytes  = 256 << 10 // 256 KiB decoded total
	CallbackSignatureSkew     = 5 * time.Minute
	DefaultWebhookTimeout     = 10 * time.Second
	DefaultWebhookCallbackTTL = 30 * time.Second
	SignatureHeaderName       = "X-ActWeave-Signature"
	ProcessorSpecVersion      = "file-processor.v1"
	ProcessorEventUploaded    = "file.uploaded"
)

// Sentinel errors for callback HTTP mapping.
var (
	ErrCallbackLate         = errors.New("aap file processor callback late")
	ErrCallbackUnauthorized = errors.New("aap file processor callback unauthorized")
	ErrArtifactTooLarge     = errors.New("aap file processor artifact too large")
)

// Download token purposes (KD-13).
const (
	DownloadPurposeClientContent     = "client_content"
	DownloadPurposeToolInvoke        = "tool_invoke"
	DownloadPurposeProcessorDelivery = "processor_delivery"
)

// Download token TTLs (design §5.5.4).
const (
	DefaultClientContentTokenTTL     = 5 * time.Minute
	DefaultToolInvokeTokenTTL        = 5 * time.Minute
	DefaultProcessorDeliveryTokenTTL = 10 * time.Minute
	MaxDownloadTokenTTL              = 15 * time.Minute
)

// SDKPreferDownloadTokenBytes is the soft threshold above which clients should
// prefer path B (:download token proxy) over Bearer content (design §5.5.4).
// Server never enforces this; it may log a non-secret suggestion only (IC-07).
const SDKPreferDownloadTokenBytes int64 = 4 << 20 // 4 MiB

// DefaultDownloadTokenPurgeBatch is the max rows deleted per purge call (IC-07).
const DefaultDownloadTokenPurgeBatch = 500

// Staging GC / retention purge defaults (IC-11 / KD-21 / design §5.4.3).
const (
	DefaultStagingGCBatch           = 100
	DefaultStagingGCInterval        = 60 * time.Second
	DefaultMaxPromoteAttempts       = 10
	DefaultRetentionPurgeBatch      = 100
	DefaultRetentionPurgeInterval   = 5 * time.Minute
	DefaultRetentionPurgeClaimLease = 2 * time.Minute
)

// Default workspace quotas (design §5.5.7).
const (
	DefaultMaxPendingPerWorkspace    = 20
	DefaultMaxReadyBytesPerWorkspace = int64(5) << 30 // 5 GiB
)

// Defaults from design §6.5.
const (
	DefaultMaxBytes      int64 = 25 << 20 // 25 MiB
	DefaultStagingTTL          = 15 * time.Minute
	DefaultPresignTTL          = 15 * time.Minute
	DefaultRetention           = 30 * 24 * time.Hour
	DefaultRetentionDays       = 30
)

// MaxOutboundTurnBytes is the per-run IngestGenerated byte budget (same as maxBytes).
const MaxOutboundTurnBytes = DefaultMaxBytes

const (
	MediaTypePNG    = "image/png"
	MediaTypeJPEG   = "image/jpeg"
	MediaTypeWEBP   = "image/webp"
	MediaTypeGIF    = "image/gif"
	MediaTypePDF    = "application/pdf"
	MediaTypeDoc    = "application/msword"
	MediaTypeDocx   = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	MediaTypeXls    = "application/vnd.ms-excel"
	MediaTypeXlsx   = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	MediaTypeZip    = "application/zip"
	MediaTypeZipAlt = "application/x-zip-compressed"
	mediaTypeOLE    = "application/x-ole-storage"
)

// DefaultInboundMediaTypes is the advertised + enforced inbound MIME list, stable order.
func DefaultInboundMediaTypes() []string {
	return []string{
		MediaTypePNG, MediaTypeJPEG, MediaTypeWEBP, MediaTypeGIF, MediaTypePDF,
		MediaTypeDoc, MediaTypeDocx, MediaTypeXls, MediaTypeXlsx, MediaTypeZip,
	}
}

// AllowedMediaTypes is the inbound MIME allowlist (KD-12).
var AllowedMediaTypes = map[string]struct{}{
	MediaTypePNG:    {},
	MediaTypeJPEG:   {},
	MediaTypeWEBP:   {},
	MediaTypeGIF:    {},
	MediaTypePDF:    {},
	MediaTypeDoc:    {},
	MediaTypeDocx:   {},
	MediaTypeXls:    {},
	MediaTypeXlsx:   {},
	MediaTypeZip:    {},
	MediaTypeZipAlt: {},
}

// AllowedOutboundMediaTypes is the ingest allowlist: inbound types plus text types.
// Do not use this table for client create (validateCreateInput stays inbound-only).
var AllowedOutboundMediaTypes = map[string]struct{}{
	MediaTypePNG:       {},
	MediaTypeJPEG:      {},
	MediaTypeWEBP:      {},
	MediaTypeGIF:       {},
	MediaTypePDF:       {},
	MediaTypeDoc:       {},
	MediaTypeDocx:      {},
	MediaTypeXls:       {},
	MediaTypeXlsx:      {},
	MediaTypeZip:       {},
	MediaTypeZipAlt:    {},
	"text/plain":       {},
	"text/csv":         {},
	"text/markdown":    {},
	"application/json": {},
}

var (
	ErrInvalid         = errors.New("aap file input is invalid")
	ErrNotFound        = errors.New("aap file not found")
	ErrConflict        = errors.New("aap file conflict")
	ErrFailed          = errors.New("aap file operation failed")
	ErrExpired         = errors.New("aap file upload expired")
	ErrNotReady        = errors.New("aap file is not ready")
	ErrPendingLimit    = errors.New("aap file pending upload limit exceeded")
	ErrFeatureDisabled = errors.New("aap file feature disabled")
)

// File is the durable aap_files row.
type File struct {
	ID                     string
	WorkspaceID            string
	AgentID                string
	ActorType              string
	ActorID                string
	ClientID               string
	SubjectType            *string
	SubjectID              *string
	OwnershipMode          string
	OwnershipPolicyVersion int64
	Status                 string
	Filename               *string
	DeclaredMediaType      string
	DetectedMediaType      *string
	SizeBytes              int64
	SHA256                 *string
	StagingBucket          string
	StagingObjectKey       *string
	StagingExpiresAt       time.Time
	StagingDeletedAt       *time.Time
	StoredObjectID         *string
	Purpose                string
	SourceRunID            string
	ErrorCode              *string
	ErrorMessage           *string
	ProcessingVersion      int64
	CreatedAt              time.Time
	UpdatedAt              time.Time
	ReadyAt                *time.Time
	RetentionUntil         *time.Time
}

// ProcessingJob is one aap_file_processing_jobs row.
type ProcessingJob struct {
	ID             string
	WorkspaceID    string
	FileID         string
	Stage          string
	Status         string
	Attempt        int
	ClaimToken     *string
	ClaimExpiresAt *time.Time
	AvailableAt    time.Time
	DeadlineAt     *time.Time
	DeliveryID     *string
	LastErrorCode  *string
	Result         []byte
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// UploadIntent is returned by CreateUploadIntent (presign only appears here).
type UploadIntent struct {
	File      File
	UploadURL string
	// Headers that the client must send on the signed PUT (Content-Type, Content-Length).
	UploadHeaders map[string]string
	ExpiresAt     time.Time
}

// BlobInfo is a minimal staging object stat/open result.
type BlobInfo struct {
	Size   int64
	SHA256 string
}

// DownloadToken is an opaque aap_file_download_tokens row (not a JWT).
type DownloadToken struct {
	ID          string
	WorkspaceID string
	FileID      string
	Purpose     string
	JTI         string
	SingleUse   bool
	ConsumedAt  *time.Time
	MaxBytes    *int64
	ExpiresAt   time.Time
	CreatedAt   time.Time
	CreatedBy   string
}

// ProcessingStage is a public projection of a processing job (no secrets).
type ProcessingStage struct {
	Stage  string
	Status string
}

// WorkspaceFileProcessor is aap_workspace_file_processors config (KD-7).
// secret_ref is resolved via SecretResolver (tests may store the secret inline).
type WorkspaceFileProcessor struct {
	ID          string
	WorkspaceID string
	ProcessorID string
	Type        string
	URL         string
	SecretRef   string
	TimeoutMs   int
	Required    bool
	Enabled     bool
	Events      []string
	CreatedAt   time.Time
}

// FileArtifact is a derived pipeline product (aap_file_artifacts).
type FileArtifact struct {
	ID             string
	WorkspaceID    string
	FileID         string
	Kind           string
	MediaType      string
	StoredObjectID string
	ProcessorID    string
	CreatedAt      time.Time
}

// WebhookStageName builds the jobs.stage key for a workspace processor.
func WebhookStageName(processorID string) string {
	return StageWebhookPrefix + strings.TrimSpace(processorID)
}

// ProcessorIDFromStage extracts processor_id from webhook:<id>, or empty.
func ProcessorIDFromStage(stage string) string {
	stage = strings.TrimSpace(stage)
	if !strings.HasPrefix(stage, StageWebhookPrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(stage, StageWebhookPrefix))
}

// IsTerminalJobStatus reports whether a job will not progress further.
func IsTerminalJobStatus(status string) bool {
	switch status {
	case JobSucceeded, JobFailed, JobSkipped, JobTimedOut:
		return true
	default:
		return false
	}
}

// IsRequiredBuiltinStage reports stages that are always required when present.
func IsRequiredBuiltinStage(stage string) bool {
	switch stage {
	case StagePromote, StageMIMEDetect:
		return true
	default:
		return false
	}
}
