package openapiimport

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

type FileImportReferenceValidator interface {
	ValidateFileImportReferences(context.Context, string, string, *string) error
}

type FileImportRequest struct {
	ImportID     string
	WorkspaceID  string
	ProviderID   string
	ConnectionID *string
	FileName     string
	ContentType  string
	Content      []byte
	CreatedBy    string
}

type FileImportService struct {
	references FileImportReferenceValidator
	rawStore   PermanentOpenAPIRawStore
	parser     *ParseService
	maxBytes   int64
}

func NewFileImportService(
	references FileImportReferenceValidator,
	rawStore PermanentOpenAPIRawStore,
	parser *ParseService,
	maxBytes int64,
) (*FileImportService, error) {
	if references == nil || rawStore == nil || parser == nil || maxBytes < 0 || maxBytes > maximumOpenAPIDocumentMaxBytes {
		return nil, errors.New("valid openapi file import dependencies are required")
	}
	if maxBytes == 0 {
		maxBytes = DefaultOpenAPIDocumentMaxBytes
	}
	return &FileImportService{references: references, rawStore: rawStore, parser: parser, maxBytes: maxBytes}, nil
}

func (s *FileImportService) ImportFile(ctx context.Context, request FileImportRequest) (ParseOutcome, error) {
	request.ImportID = strings.TrimSpace(request.ImportID)
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.ProviderID = strings.TrimSpace(request.ProviderID)
	request.ConnectionID = normalizeOptional(request.ConnectionID)
	request.FileName = strings.TrimSpace(filepath.Base(strings.TrimSpace(request.FileName)))
	request.ContentType = strings.TrimSpace(request.ContentType)
	request.CreatedBy = strings.TrimSpace(request.CreatedBy)
	request.Content = append([]byte(nil), request.Content...)
	if !validUUID(request.ImportID) || !validUUID(request.WorkspaceID) || !validUUID(request.ProviderID) ||
		!validUUID(request.CreatedBy) || request.FileName == "" || request.FileName == "." ||
		len(request.Content) == 0 || int64(len(request.Content)) > s.maxBytes ||
		(request.ConnectionID != nil && !validUUID(*request.ConnectionID)) {
		return ParseOutcome{}, ErrInvalid
	}
	if request.ContentType == "" {
		request.ContentType = "application/octet-stream"
	}
	if err := s.references.ValidateFileImportReferences(ctx, request.WorkspaceID, request.ProviderID, request.ConnectionID); err != nil {
		return ParseOutcome{}, err
	}
	rawObjectID, err := s.rawStore.StorePermanentOpenAPI(ctx, request.WorkspaceID, request.ContentType, request.Content)
	if err != nil {
		return ParseOutcome{}, err
	}
	return s.parser.Parse(ctx, ParseRequest{
		ImportID: request.ImportID, WorkspaceID: request.WorkspaceID,
		ProviderID: &request.ProviderID, ConnectionID: request.ConnectionID,
		SourceType: SourceTypeFile, FileName: request.FileName, RawObjectID: rawObjectID,
		Content: request.Content, CreatedBy: request.CreatedBy,
	})
}
