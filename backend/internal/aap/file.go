package aap

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"

	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/agentaccessauth"

	"github.com/google/uuid"
)

// FileService is the AAP application boundary for File commands (IC-04).
// It wraps aapfile.Service with command receipts for create/complete.
type FileService struct {
	files    *aapfile.Service
	receipts CommandReceiptLedger
}

// NewFileService constructs the application File service.
func NewFileService(files *aapfile.Service) (*FileService, error) {
	if files == nil {
		return nil, aapfile.ErrInvalid
	}
	return &FileService{files: files}, nil
}

// ConfigureCommandReceipts enables durable create/complete idempotency.
func (service *FileService) ConfigureCommandReceipts(ledger CommandReceiptLedger) error {
	if service == nil || ledger == nil || service.receipts != nil {
		return ErrCommandReceiptInvalid
	}
	service.receipts = ledger
	return nil
}

// Domain returns the underlying domain service (get/mint/content helpers).
func (service *FileService) Domain() *aapfile.Service {
	if service == nil {
		return nil
	}
	return service.files
}

// CreateFileInput is the HTTP create-upload-intent command.
type CreateFileInput struct {
	Scope          ConversationScope
	Principal      agentaccessauth.AAPAccessTokenPrincipal
	Authorization  agentaccessauth.AAPAuthorizationDecision
	Filename       string
	MediaType      string
	SizeBytes      int64
	SHA256         string
	Purpose        string
	IdempotencyKey string
}

// CreateFileResult is returned after create (upload only here).
type CreateFileResult struct {
	Intent     aapfile.UploadIntent
	Idempotent bool
}

// Create allocates a PENDING_UPLOAD file + presigned PUT (receipt-backed).
func (service *FileService) Create(
	ctx context.Context,
	input CreateFileInput,
) (CreateFileResult, error) {
	if service == nil || service.files == nil || ctx == nil {
		return CreateFileResult{}, aapfile.ErrInvalid
	}
	input.Scope.WorkspaceID = strings.TrimSpace(input.Scope.WorkspaceID)
	input.Scope.AgentID = strings.TrimSpace(input.Scope.AgentID)
	input.IdempotencyKey = strings.ToLower(strings.TrimSpace(input.IdempotencyKey))
	if !validConversationScope(input.Scope) || !canonicalUUID(input.IdempotencyKey) {
		return CreateFileResult{}, aapfile.ErrInvalid
	}
	if !validFileCreateAuthorization(input.Scope, input.Principal, input.Authorization) {
		return CreateFileResult{}, aapfile.ErrInvalid
	}

	receiptKey := commandReceiptKey(
		input.Scope, input.Principal, input.Authorization,
		CommandFileCreate, input.IdempotencyKey,
	)
	requestHash, err := FileCreateCommandRequestHash(FileCreateRequestHashInput{
		MediaType: input.MediaType, SizeBytes: input.SizeBytes,
		SHA256: input.SHA256, Purpose: input.Purpose, Filename: input.Filename,
	})
	if err != nil {
		return CreateFileResult{}, err
	}
	if err := observeCommand(ctx, service.receipts, receiptKey, requestHash); err != nil {
		return CreateFileResult{}, err
	}

	fileID := deterministicFileID(input)
	intent, err := service.files.CreateUploadIntent(ctx, aapfile.CreateUploadIntentInput{
		Scope: aapfile.Scope{
			WorkspaceID: input.Scope.WorkspaceID, AgentID: input.Scope.AgentID,
		},
		Principal:          input.Principal,
		ClientID:           input.Authorization.Snapshot.ClientID,
		AgentPolicyVersion: input.Authorization.Snapshot.AgentPolicyVersion,
		FileID:             fileID,
		Filename:           input.Filename,
		MediaType:          input.MediaType,
		SizeBytes:          input.SizeBytes,
		SHA256:             input.SHA256,
		Purpose:            input.Purpose,
	})
	if err == nil {
		if completeErr := completeCommand(ctx, service.receipts, receiptKey, requestHash,
			"FILE", intent.File.ID, intent.File.ProcessingVersion); completeErr != nil {
			return CreateFileResult{}, completeErr
		}
		return CreateFileResult{Intent: intent}, nil
	}
	if !errors.Is(err, aapfile.ErrConflict) {
		return CreateFileResult{}, err
	}
	// Idempotent replay: re-presign when still pending.
	replay, replayErr := service.files.PresignUpload(ctx, input.Scope.WorkspaceID, fileID)
	if replayErr != nil {
		return CreateFileResult{}, aapfile.ErrConflict
	}
	if !sameFileCreateIntent(replay.File, input) {
		return CreateFileResult{}, ErrCommandIdempotencyConflict
	}
	if completeErr := completeCommand(ctx, service.receipts, receiptKey, requestHash,
		"FILE", replay.File.ID, replay.File.ProcessingVersion); completeErr != nil {
		return CreateFileResult{}, completeErr
	}
	return CreateFileResult{Intent: replay, Idempotent: true}, nil
}

// CompleteFileInput is the fast-path complete command.
type CompleteFileInput struct {
	Scope          ConversationScope
	Principal      agentaccessauth.AAPAccessTokenPrincipal
	Authorization  agentaccessauth.AAPAuthorizationDecision
	FileID         string
	SHA256         string
	IdempotencyKey string
}

// CompleteFileResult is returned after complete (no promote wait).
type CompleteFileResult struct {
	Result     aapfile.CompleteUploadResult
	Idempotent bool
}

// Complete runs the fast-path complete (stat staging, CAS, enqueue promote).
func (service *FileService) Complete(
	ctx context.Context,
	input CompleteFileInput,
) (CompleteFileResult, error) {
	if service == nil || service.files == nil || ctx == nil {
		return CompleteFileResult{}, aapfile.ErrInvalid
	}
	input.Scope.WorkspaceID = strings.TrimSpace(input.Scope.WorkspaceID)
	input.Scope.AgentID = strings.TrimSpace(input.Scope.AgentID)
	input.FileID = strings.ToLower(strings.TrimSpace(input.FileID))
	input.IdempotencyKey = strings.ToLower(strings.TrimSpace(input.IdempotencyKey))
	if !validConversationScope(input.Scope) || !canonicalUUID(input.FileID) ||
		!canonicalUUID(input.IdempotencyKey) {
		return CompleteFileResult{}, aapfile.ErrInvalid
	}
	if !validFileResourceAuthorization(
		input.Scope, input.Principal, input.Authorization,
		agentaccessauth.ActionFileComplete, input.FileID,
	) {
		return CompleteFileResult{}, aapfile.ErrInvalid
	}

	receiptKey := commandReceiptKey(
		input.Scope, input.Principal, input.Authorization,
		CommandFileComplete, input.IdempotencyKey,
	)
	requestHash, err := FileCompleteCommandRequestHash(FileCompleteRequestHashInput{
		FileID: input.FileID, SHA256: input.SHA256,
	})
	if err != nil {
		return CompleteFileResult{}, err
	}
	if err := observeCommand(ctx, service.receipts, receiptKey, requestHash); err != nil {
		return CompleteFileResult{}, err
	}

	// Snapshot status before complete for idempotent classification.
	before, beforeErr := service.files.GetFile(ctx, input.Scope.WorkspaceID, input.FileID)
	wasAdvanced := beforeErr == nil && before.Status != aapfile.StatusPendingUpload

	result, err := service.files.CompleteUpload(ctx, aapfile.CompleteUploadInput{
		Scope: aapfile.Scope{
			WorkspaceID: input.Scope.WorkspaceID, AgentID: input.Scope.AgentID,
		},
		FileID: input.FileID, SHA256: input.SHA256,
	})
	if err != nil {
		return CompleteFileResult{}, err
	}
	if completeErr := completeCommand(ctx, service.receipts, receiptKey, requestHash,
		"FILE", result.File.ID, result.File.ProcessingVersion); completeErr != nil {
		return CompleteFileResult{}, completeErr
	}
	return CompleteFileResult{Result: result, Idempotent: wasAdvanced}, nil
}

func deterministicFileID(input CreateFileInput) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"aap.file.create.v1",
		input.Scope.WorkspaceID,
		input.Scope.AgentID,
		input.Authorization.Snapshot.ClientID,
		input.Principal.ServicePrincipalID,
		input.Principal.PrincipalID,
		input.IdempotencyKey,
	}, "\x1f")))
	// UUIDv5-style: use first 16 bytes of SHA-256 with version/variant bits.
	var id uuid.UUID
	copy(id[:], sum[:16])
	id[6] = (id[6] & 0x0f) | 0x50 // version 5
	id[8] = (id[8] & 0x3f) | 0x80 // RFC 4122 variant
	return id.String()
}

func sameFileCreateIntent(file aapfile.File, input CreateFileInput) bool {
	media, err := aapfile.NormalizeMediaType(input.MediaType)
	if err != nil {
		media = strings.TrimSpace(input.MediaType)
	}
	if file.WorkspaceID != input.Scope.WorkspaceID || file.AgentID != input.Scope.AgentID ||
		file.DeclaredMediaType != media || file.SizeBytes != input.SizeBytes {
		return false
	}
	wantSHA := strings.ToLower(strings.TrimSpace(input.SHA256))
	gotSHA := ""
	if file.SHA256 != nil {
		gotSHA = *file.SHA256
	}
	if wantSHA != gotSHA {
		return false
	}
	wantName := strings.TrimSpace(input.Filename)
	gotName := ""
	if file.Filename != nil {
		gotName = *file.Filename
	}
	return wantName == gotName
}

func validFileCreateAuthorization(
	scope ConversationScope,
	caller agentaccessauth.AAPAccessTokenPrincipal,
	decision agentaccessauth.AAPAuthorizationDecision,
) bool {
	snapshot := decision.Snapshot
	return snapshot.SpecVersion == "aap.authorization.v1" &&
		snapshot.Action == agentaccessauth.ActionFileCreate &&
		snapshot.RequiredScope == "file:write" &&
		snapshot.WorkspaceID == scope.WorkspaceID &&
		snapshot.AgentID == scope.AgentID &&
		snapshot.ServicePrincipalID == caller.ServicePrincipalID &&
		snapshot.SubjectID == caller.PrincipalID &&
		snapshot.AuthorizedParty == caller.AuthorizedParty &&
		snapshot.TokenID == caller.TokenID &&
		snapshot.ClientID != "" && snapshot.GrantID != "" &&
		snapshot.GrantVersion >= 1 && snapshot.AgentPolicyVersion >= 1 &&
		snapshot.ResourceType == agentaccessauth.ResourceNone &&
		snapshot.ResourceID == "" &&
		containsScope(decision.EffectiveScopes, "file:write")
}

func validFileResourceAuthorization(
	scope ConversationScope,
	caller agentaccessauth.AAPAccessTokenPrincipal,
	decision agentaccessauth.AAPAuthorizationDecision,
	action agentaccessauth.AAPAction,
	fileID string,
) bool {
	if !validConversationScope(scope) || !canonicalUUID(fileID) {
		return false
	}
	wantScope := "file:write"
	if action == agentaccessauth.ActionFileRead || action == agentaccessauth.ActionFileContent {
		wantScope = "file:read"
	}
	snapshot := decision.Snapshot
	return snapshot.SpecVersion == "aap.authorization.v1" &&
		snapshot.Action == action &&
		snapshot.RequiredScope == wantScope &&
		snapshot.WorkspaceID == scope.WorkspaceID &&
		snapshot.AgentID == scope.AgentID &&
		snapshot.ServicePrincipalID == caller.ServicePrincipalID &&
		snapshot.SubjectID == caller.PrincipalID &&
		snapshot.AuthorizedParty == caller.AuthorizedParty &&
		snapshot.TokenID == caller.TokenID &&
		snapshot.ClientID != "" && snapshot.GrantID != "" &&
		snapshot.GrantVersion >= 1 && snapshot.AgentPolicyVersion >= 1 &&
		snapshot.ResourceType == agentaccessauth.ResourceFile &&
		snapshot.ResourceID == fileID &&
		containsScope(decision.EffectiveScopes, wantScope)
}
