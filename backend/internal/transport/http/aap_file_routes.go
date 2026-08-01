package httptransport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/config"
	"actweave/backend/internal/metrics"
	"actweave/backend/internal/storedobject"

	"github.com/gin-gonic/gin"
)

// Stable SYSTEM actor id for download-token SecureStore Open (must be a UUID).
const aapFileSystemDownloadActorID = "019f0000-0000-7000-8000-00000000f11e"

var (
	// ErrAAPFilesFeatureDisabled is returned when files gate is closed (404 conceal).
	ErrAAPFilesFeatureDisabled = errors.New("AAP files feature is disabled")
)

// AAPFileContentOpener streams permanent AAP_FILE ciphertext via SecureStore.
type AAPFileContentOpener interface {
	Open(context.Context, storedobject.ReadRequest) (storedobject.OpenedObject, error)
}

// AAPFileApplication is the create/complete application boundary.
type AAPFileApplication interface {
	Create(context.Context, aap.CreateFileInput) (aap.CreateFileResult, error)
	Complete(context.Context, aap.CompleteFileInput) (aap.CompleteFileResult, error)
	Domain() *aapfile.Service
}

// AAPFileRoutes registers AAP File REST endpoints (IC-04) and processor callback (IC-05).
type AAPFileRoutes struct {
	authorizer AAPDataPlaneAuthorizer
	files      AAPFileApplication
	content    AAPFileContentOpener
	quota      agentaccess.DataPlaneQuota
	gate       *config.AgentAccessFilesConfig
	callback   AAPFileCallbackService
	secrets    AAPFileSecretResolver
}

// NewAAPFileRoutes constructs File routes. content may be nil when only
// metadata routes are exercised in tests; content/download then 503.
func NewAAPFileRoutes(
	authorizer AAPDataPlaneAuthorizer,
	files AAPFileApplication,
	content AAPFileContentOpener,
	gate *config.AgentAccessFilesConfig,
) (*AAPFileRoutes, error) {
	if authorizer == nil || files == nil {
		return nil, aapfile.ErrInvalid
	}
	return &AAPFileRoutes{
		authorizer: authorizer, files: files, content: content, gate: gate,
	}, nil
}

// ConfigureCommandQuota enables data-plane rate limits for file operations.
func (routes *AAPFileRoutes) ConfigureCommandQuota(quota agentaccess.DataPlaneQuota) error {
	if routes == nil || quota == nil || routes.quota != nil {
		return aapfile.ErrInvalid
	}
	routes.quota = quota
	return nil
}

// RegisterAgentAccessV1 registers File routes under /api/agent-access/v1.
func (routes *AAPFileRoutes) RegisterAgentAccessV1(v1 AgentAccessV1Routes) {
	base := "/workspaces/:wid/agents/:aid/files"
	// Protected (Bearer) lifecycle routes.
	v1.Protected.POST(base, routes.createFile)
	v1.Protected.POST(base+"/:fid/__command/complete", routes.completeFile)
	v1.Protected.GET(base+"/:fid", routes.getFile)
	v1.Protected.GET(base+"/:fid/content", routes.getFileContent)
	v1.Protected.POST(base+"/:fid/__command/download", routes.mintDownload)
	// Public token proxy — no Bearer; token row is the credential.
	v1.Public.GET("/files/downloads/:tid", routes.downloadByToken)
	// Processor callback — no Bearer; HMAC + delivery_id (IC-05).
	routes.registerProcessorCallback(v1)
}

// ---- DTOs (GET must never expose upload / presign / downloadUrl) ----

type aapCreateFileRequest struct {
	Filename  string `json:"filename"`
	MediaType string `json:"mediaType"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
	Purpose   string `json:"purpose"`
}

type aapCompleteFileRequest struct {
	SHA256 string `json:"sha256"`
}

type aapFileUploadDTO struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expiresAt"`
}

type aapFileErrorDTO struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

type aapFileProcessingDTO struct {
	Version int64                      `json:"version"`
	Stages  []aapFileProcessingStageDTO `json:"stages"`
}

type aapFileProcessingStageDTO struct {
	Stage  string `json:"stage"`
	Status string `json:"status"`
}

type aapFileLinksDTO struct {
	Content string `json:"content"`
}

// aapFileDTO is the public File resource. Sensitive acceptance forbids upload/
// presign/downloadUrl fields on this type.
type aapFileDTO struct {
	Object             string               `json:"object"`
	ID                 string               `json:"id"`
	AgentID            string               `json:"agentId"`
	Status             string               `json:"status"`
	Filename           string               `json:"filename,omitempty"`
	MediaType          string               `json:"mediaType"`
	DetectedMediaType  string               `json:"detectedMediaType,omitempty"`
	SizeBytes          int64                `json:"sizeBytes"`
	SHA256             string               `json:"sha256,omitempty"`
	Purpose            string               `json:"purpose"`
	Error              *aapFileErrorDTO     `json:"error,omitempty"`
	Processing         aapFileProcessingDTO `json:"processing"`
	Artifacts          []any                `json:"artifacts"`
	Links              aapFileLinksDTO      `json:"links"`
	CreatedAt          time.Time            `json:"createdAt"`
	UpdatedAt          time.Time            `json:"updatedAt"`
	ReadyAt            *time.Time           `json:"readyAt,omitempty"`
}

type aapCreateFileResponse struct {
	File       aapFileDTO        `json:"file"`
	Upload     *aapFileUploadDTO `json:"upload,omitempty"`
	Idempotent bool              `json:"idempotent"`
}

type aapCompleteFileResponse struct {
	File       aapFileDTO `json:"file"`
	Idempotent bool       `json:"idempotent"`
}

type aapGetFileResponse struct {
	File aapFileDTO `json:"file"`
}

type aapMintDownloadResponse struct {
	// Token is the opaque download token id (not a MinIO key / JWT).
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	// URL is a relative AAP path for the token proxy (no live MinIO credentials).
	URL string `json:"url"`
}

// ---- handlers ----

func (routes *AAPFileRoutes) createFile(c *gin.Context) {
	if !routes.requireFilesGate(c) {
		return
	}
	caller, authorization, ok := authorizeAAPRequest(
		c, routes.authorizer, agentaccessauth.ActionFileCreate,
		agentaccessauth.AAPAuthorizationResource{},
	)
	if !ok {
		return
	}
	var request aapCreateFileRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, aapfile.ErrInvalid)
		return
	}
	if !canonicalHTTPUUID(strings.ToLower(strings.TrimSpace(c.GetHeader("Idempotency-Key")))) ||
		!enforceAAPCommandQuota(c, routes.quota, agentaccess.QuotaFileCreate, caller, authorization) {
		if !c.IsAborted() {
			RespondError(c, aapfile.ErrInvalid)
		}
		return
	}
	result, err := routes.files.Create(c.Request.Context(), aap.CreateFileInput{
		Scope: aap.ConversationScope{
			WorkspaceID: c.Param("wid"), AgentID: c.Param("aid"),
		},
		Principal: caller, Authorization: authorization,
		Filename: request.Filename, MediaType: request.MediaType,
		SizeBytes: request.SizeBytes, SHA256: request.SHA256, Purpose: request.Purpose,
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	status := http.StatusCreated
	if result.Idempotent {
		status = http.StatusOK
	}
	dto := aapFileDTOFor(result.Intent.File, nil, c.Param("wid"), c.Param("aid"))
	response := aapCreateFileResponse{File: dto, Idempotent: result.Idempotent}
	if result.Intent.UploadURL != "" {
		response.Upload = &aapFileUploadDTO{
			Method: "PUT", URL: result.Intent.UploadURL,
			Headers: result.Intent.UploadHeaders, ExpiresAt: result.Intent.ExpiresAt.UTC(),
		}
	}
	location := "/api/agent-access/v1/workspaces/" + c.Param("wid") +
		"/agents/" + c.Param("aid") + "/files/" + result.Intent.File.ID
	c.Header("Location", location)
	c.JSON(status, response)
}

func (routes *AAPFileRoutes) completeFile(c *gin.Context) {
	if !routes.requireFilesGate(c) {
		return
	}
	fileID := strings.TrimSpace(c.Param("fid"))
	caller, authorization, ok := authorizeAAPRequest(
		c, routes.authorizer, agentaccessauth.ActionFileComplete,
		agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceFile, ID: fileID,
		},
	)
	if !ok {
		return
	}
	var request aapCompleteFileRequest
	// Body is optional; empty body is valid complete.
	if c.Request.ContentLength != 0 {
		if decodeJSON(c, &request) != nil {
			RespondError(c, aapfile.ErrInvalid)
			return
		}
	}
	if !canonicalHTTPUUID(strings.ToLower(strings.TrimSpace(c.GetHeader("Idempotency-Key")))) ||
		!enforceAAPCommandQuota(c, routes.quota, agentaccess.QuotaFileComplete, caller, authorization) {
		if !c.IsAborted() {
			RespondError(c, aapfile.ErrInvalid)
		}
		return
	}
	result, err := routes.files.Complete(c.Request.Context(), aap.CompleteFileInput{
		Scope: aap.ConversationScope{
			WorkspaceID: c.Param("wid"), AgentID: c.Param("aid"),
		},
		Principal: caller, Authorization: authorization,
		FileID: fileID, SHA256: request.SHA256,
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	stages, _ := routes.domainStages(c, c.Param("wid"), fileID)
	c.JSON(http.StatusOK, aapCompleteFileResponse{
		File: aapFileDTOFor(result.Result.File, stages, c.Param("wid"), c.Param("aid")),
		Idempotent: result.Idempotent,
	})
}

func (routes *AAPFileRoutes) getFile(c *gin.Context) {
	if !routes.requireFilesGate(c) {
		return
	}
	fileID := strings.TrimSpace(c.Param("fid"))
	_, _, ok := authorizeAAPRequest(
		c, routes.authorizer, agentaccessauth.ActionFileRead,
		agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceFile, ID: fileID,
		},
	)
	if !ok {
		return
	}
	domain := routes.files.Domain()
	if domain == nil {
		RespondError(c, aapfile.ErrInvalid)
		return
	}
	file, err := domain.GetFile(c.Request.Context(), c.Param("wid"), fileID)
	if err != nil {
		RespondError(c, err)
		return
	}
	if file.AgentID != c.Param("aid") {
		RespondError(c, aapfile.ErrNotFound)
		return
	}
	stages, _ := domain.ListProcessingStages(c.Request.Context(), c.Param("wid"), fileID)
	c.Header("Cache-Control", "private, no-cache")
	c.JSON(http.StatusOK, aapGetFileResponse{
		File: aapFileDTOFor(file, stages, c.Param("wid"), c.Param("aid")),
	})
}

func (routes *AAPFileRoutes) getFileContent(c *gin.Context) {
	if !routes.requireFilesGate(c) {
		return
	}
	fileID := strings.TrimSpace(c.Param("fid"))
	caller, authorization, ok := authorizeAAPRequest(
		c, routes.authorizer, agentaccessauth.ActionFileContent,
		agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceFile, ID: fileID,
		},
	)
	if !ok {
		return
	}
	// Rate limit: DataPlaneQuota file.content (design §5.5.4 path A).
	if !enforceAAPCommandQuota(c, routes.quota, agentaccess.QuotaFileContent, caller, authorization) {
		return
	}
	if routes.content == nil {
		RespondError(c, errors.New("AAP file content opener is unavailable"))
		return
	}
	domain := routes.files.Domain()
	if domain == nil {
		RespondError(c, aapfile.ErrInvalid)
		return
	}
	file, err := domain.GetFile(c.Request.Context(), c.Param("wid"), fileID)
	if err != nil {
		RespondError(c, err)
		return
	}
	if file.AgentID != c.Param("aid") {
		RespondError(c, aapfile.ErrNotFound)
		return
	}
	if file.Status != aapfile.StatusReady || file.StoredObjectID == nil ||
		strings.TrimSpace(*file.StoredObjectID) == "" {
		RespondError(c, aapfile.ErrNotReady)
		return
	}
	// Soft SDK hint only (IC-07): large files should prefer :download path B.
	// Never logs object keys, tokens, or MinIO URLs.
	if file.SizeBytes > aapfile.SDKPreferDownloadTokenBytes {
		slog.Info("aap file content prefers download token for large files",
			"workspace_id", file.WorkspaceID,
			"agent_id", file.AgentID,
			"size_bytes", file.SizeBytes,
			"threshold_bytes", aapfile.SDKPreferDownloadTokenBytes,
		)
	}
	opened, err := routes.content.Open(c.Request.Context(), storedobject.ReadRequest{
		WorkspaceID: file.WorkspaceID,
		ObjectID:    *file.StoredObjectID,
		ActorType:   storedobject.CreatorServicePrincipal,
		ActorID:     caller.ServicePrincipalID,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	defer opened.Body.Close()
	contentType := file.DeclaredMediaType
	if file.DetectedMediaType != nil && *file.DetectedMediaType != "" {
		contentType = *file.DetectedMediaType
	}
	// Content path ops: reverse proxies must not buffer full bodies
	// (nginx: proxy_buffering off; proxy_read_timeout >= 120s). See frontend/nginx.conf.
	setAAPFileStreamHeaders(c, contentType)
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, opened.Body)
}

func (routes *AAPFileRoutes) mintDownload(c *gin.Context) {
	if !routes.requireFilesGate(c) {
		return
	}
	fileID := strings.TrimSpace(c.Param("fid"))
	caller, authorization, ok := authorizeAAPRequest(
		c, routes.authorizer, agentaccessauth.ActionFileContent,
		agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceFile, ID: fileID,
		},
	)
	if !ok {
		return
	}
	// Rate limit: DataPlaneQuota file.download (mint path; design §5.14).
	if !enforceAAPCommandQuota(c, routes.quota, agentaccess.QuotaFileDownload, caller, authorization) {
		return
	}
	domain := routes.files.Domain()
	if domain == nil {
		RespondError(c, aapfile.ErrInvalid)
		return
	}
	minted, err := domain.MintDownloadToken(c.Request.Context(), aapfile.MintDownloadTokenInput{
		Scope: aapfile.Scope{
			WorkspaceID: c.Param("wid"), AgentID: c.Param("aid"),
		},
		FileID: fileID, Purpose: aapfile.DownloadPurposeClientContent,
		SingleUse: false, CreatedBy: caller.ServicePrincipalID,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	// Response exposes only opaque token id + relative AAP path (no MinIO keys).
	c.JSON(http.StatusOK, aapMintDownloadResponse{
		Token: minted.Token.ID, ExpiresAt: minted.Token.ExpiresAt.UTC(),
		URL: "/api/agent-access/v1/files/downloads/" + minted.Token.ID,
	})
}

func (routes *AAPFileRoutes) downloadByToken(c *gin.Context) {
	// Global files master switch only (no principal); conceal when disabled.
	if routes.gate == nil || !routes.gate.Enabled {
		RespondError(c, ErrAAPFilesFeatureDisabled)
		return
	}
	if routes.content == nil {
		RespondError(c, errors.New("AAP file content opener is unavailable"))
		return
	}
	domain := routes.files.Domain()
	if domain == nil {
		RespondError(c, aapfile.ErrInvalid)
		return
	}
	tokenID := strings.TrimSpace(c.Param("tid"))
	token, file, err := domain.ResolveDownloadToken(c.Request.Context(), tokenID)
	if err != nil {
		// Conceal invalid/expired/consumed/purpose mismatch as not found.
		if errors.Is(err, aapfile.ErrNotFound) || errors.Is(err, aapfile.ErrInvalid) {
			metrics.DefaultAAPFile().IncDownload("unknown", "not_found")
			RespondError(c, aapfile.ErrNotFound)
			return
		}
		metrics.DefaultAAPFile().IncDownload("unknown", "error")
		RespondError(c, err)
		return
	}
	if !aapfile.ValidDownloadPurpose(token.Purpose) {
		metrics.DefaultAAPFile().IncDownload("unknown", "purpose_denied")
		RespondError(c, aapfile.ErrNotFound)
		return
	}
	// Consume single_use before streaming so concurrent races fail closed (CAS).
	if token.SingleUse {
		if err := domain.ConsumeDownloadToken(c.Request.Context(), token.ID); err != nil {
			metrics.DefaultAAPFile().IncDownload(token.Purpose, "consumed")
			RespondError(c, aapfile.ErrNotFound)
			return
		}
	}
	opened, err := routes.content.Open(c.Request.Context(), storedobject.ReadRequest{
		WorkspaceID: file.WorkspaceID,
		ObjectID:    *file.StoredObjectID,
		ActorType:   storedobject.CreatorSystem,
		ActorID:     aapFileSystemDownloadActorID,
	})
	if err != nil {
		metrics.DefaultAAPFile().IncDownload(token.Purpose, "error")
		RespondError(c, err)
		return
	}
	defer opened.Body.Close()
	contentType := file.DeclaredMediaType
	if file.DetectedMediaType != nil && *file.DetectedMediaType != "" {
		contentType = *file.DetectedMediaType
	}
	// Token proxy stream: same reverse-proxy buffering rules as Bearer content.
	setAAPFileStreamHeaders(c, contentType)
	c.Status(http.StatusOK)
	body := io.Reader(opened.Body)
	if token.MaxBytes != nil && *token.MaxBytes > 0 {
		body = io.LimitReader(opened.Body, *token.MaxBytes)
	}
	_, _ = io.Copy(c.Writer, body)
	metrics.DefaultAAPFile().IncDownload(token.Purpose, "ok")
}

// setAAPFileStreamHeaders applies content headers for long file bodies.
// Gateways must disable response buffering (e.g. nginx proxy_buffering off)
// and allow proxy_read_timeout >= 120s for up to DefaultMaxBytes decrypt streams.
func setAAPFileStreamHeaders(c *gin.Context, contentType string) {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "private, no-store")
	// Hints nginx and compatible proxies not to buffer the full response.
	c.Header("X-Accel-Buffering", "no")
}

// ---- helpers ----

func (routes *AAPFileRoutes) requireFilesGate(c *gin.Context) bool {
	if routes == nil || routes.gate == nil || !routes.gate.Enabled {
		RespondError(c, ErrAAPFilesFeatureDisabled)
		return false
	}
	principal, ok := AAPPrincipalFrom(c.Request.Context())
	if !ok {
		RespondError(c, ErrUnauthenticated)
		return false
	}
	if !routes.gate.AllowsClient(principal.AuthorizedParty) {
		RespondError(c, ErrAAPFilesFeatureDisabled)
		return false
	}
	if !routes.gate.AllowsWorkspace(principal.WorkspaceID) {
		RespondError(c, ErrAAPFilesFeatureDisabled)
		return false
	}
	if wid := strings.TrimSpace(c.Param("wid")); wid != "" && !routes.gate.AllowsWorkspace(wid) {
		RespondError(c, ErrAAPFilesFeatureDisabled)
		return false
	}
	return true
}

func (routes *AAPFileRoutes) domainStages(
	c *gin.Context,
	workspaceID, fileID string,
) ([]aapfile.ProcessingStage, error) {
	domain := routes.files.Domain()
	if domain == nil {
		return nil, nil
	}
	return domain.ListProcessingStages(c.Request.Context(), workspaceID, fileID)
}

func aapFileDTOFor(
	file aapfile.File,
	stages []aapfile.ProcessingStage,
	workspaceID, agentID string,
) aapFileDTO {
	stageDTOs := make([]aapFileProcessingStageDTO, 0, len(stages))
	for _, stage := range stages {
		stageDTOs = append(stageDTOs, aapFileProcessingStageDTO{
			Stage: stage.Stage, Status: stage.Status,
		})
	}
	if stageDTOs == nil {
		stageDTOs = []aapFileProcessingStageDTO{}
	}
	dto := aapFileDTO{
		Object: "file", ID: file.ID, AgentID: file.AgentID,
		Status: publicAAPFileStatus(file.Status), MediaType: file.DeclaredMediaType,
		SizeBytes: file.SizeBytes, Purpose: file.Purpose,
		Processing: aapFileProcessingDTO{
			Version: file.ProcessingVersion, Stages: stageDTOs,
		},
		Artifacts: []any{},
		Links: aapFileLinksDTO{
			Content: "/api/agent-access/v1/workspaces/" + workspaceID +
				"/agents/" + agentID + "/files/" + file.ID + "/content",
		},
		CreatedAt: file.CreatedAt.UTC(), UpdatedAt: file.UpdatedAt.UTC(),
	}
	if file.Filename != nil {
		dto.Filename = *file.Filename
	}
	if file.DetectedMediaType != nil {
		dto.DetectedMediaType = *file.DetectedMediaType
	}
	if file.SHA256 != nil {
		dto.SHA256 = *file.SHA256
	}
	if file.ErrorCode != nil && *file.ErrorCode != "" {
		msg := ""
		if file.ErrorMessage != nil {
			msg = *file.ErrorMessage
		}
		dto.Error = &aapFileErrorDTO{
			Code: *file.ErrorCode, Message: msg,
			Retryable: *file.ErrorCode == aapfile.ErrorCodeNotReady,
		}
	}
	if file.ReadyAt != nil {
		ready := file.ReadyAt.UTC()
		dto.ReadyAt = &ready
	}
	return dto
}

func publicAAPFileStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case aapfile.StatusPendingUpload:
		return "pending_upload"
	case aapfile.StatusUploaded:
		return "uploaded"
	case aapfile.StatusProcessing:
		return "processing"
	case aapfile.StatusReady:
		return "ready"
	case aapfile.StatusFailed:
		return "failed"
	case aapfile.StatusExpired:
		return "expired"
	default:
		return "unknown"
	}
}
