package openapiimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var (
	ErrNotFound = errors.New("openapi import not found")
	ErrConflict = errors.New("openapi import conflict")
	ErrInvalid  = errors.New("invalid openapi import")
)

const importColumns = `
	i.id,i.workspace_id,i.provider_id,i.connection_id,i.source_type,i.source_uri,
	i.source_revision,i.file_name,i.raw_object_id,i.content_sha256,i.parser_version,i.status,
	i.total_endpoints,i.ready_endpoints,i.issue_count,i.created_by,i.created_at,i.updated_at
`

const endpointColumns = `
	e.id,e.workspace_id,e.import_id,e.method,e.path,e.operation_id,e.summary,
	e.input_schema,e.output_schema,e.issues,e.ready,e.generated_capability_id
`

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("openapi import repository database is required")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) CreatePending(ctx context.Context, input CreatePendingInput) (Import, error) {
	input = normalizeCreatePending(input)
	if !validCreatePending(input) {
		return Import{}, ErrInvalid
	}
	value, err := scanImport(r.db.QueryRowContext(ctx, `
		INSERT INTO openapi_imports AS i(
		 id,workspace_id,provider_id,connection_id,source_type,source_uri,source_revision,file_name,
		 raw_object_id,content_sha256,parser_version,status,created_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'PENDING',$12)
		RETURNING `+importColumns,
		input.ID, input.WorkspaceID, input.ProviderID, input.ConnectionID,
		input.SourceType, input.SourceURI, input.SourceRevision, input.FileName, input.RawObjectID,
		input.ContentSHA256, input.ParserVersion, input.CreatedBy))
	if err != nil {
		return Import{}, mapWrite("create pending openapi import", err)
	}
	return value, nil
}

func (r *Repository) Get(ctx context.Context, workspaceID, importID string) (Import, error) {
	if !validUUID(workspaceID) || !validUUID(importID) {
		return Import{}, ErrInvalid
	}
	value, err := scanImport(r.db.QueryRowContext(ctx, `
		SELECT `+importColumns+` FROM openapi_imports i
		WHERE i.workspace_id=$1 AND i.id=$2
	`, workspaceID, importID))
	return value, mapRead("get openapi import", err)
}

func (r *Repository) List(ctx context.Context, workspaceID string) ([]Import, error) {
	if !validUUID(workspaceID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+importColumns+` FROM openapi_imports i WHERE i.workspace_id=$1 ORDER BY i.created_at DESC,i.id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list openapi imports: %w", err)
	}
	defer rows.Close()
	values := make([]Import, 0)
	for rows.Next() {
		value, err := scanImport(rows)
		if err != nil {
			return nil, fmt.Errorf("scan openapi import: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *Repository) Delete(ctx context.Context, workspaceID, importID string) error {
	if !validUUID(workspaceID) || !validUUID(importID) {
		return ErrInvalid
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM openapi_imports i WHERE workspace_id=$1 AND id=$2
		AND status IN ('SUCCEEDED','FAILED') AND NOT EXISTS(SELECT 1 FROM openapi_endpoints e
		WHERE e.workspace_id=i.workspace_id AND e.import_id=i.id AND e.generated_capability_id IS NOT NULL)`, workspaceID, importID)
	if err != nil {
		return mapWrite("delete openapi import", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 1 {
		return nil
	}
	_, err = r.Get(ctx, workspaceID, importID)
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return ErrConflict
}

func (r *Repository) FindLatestByChecksum(
	ctx context.Context,
	workspaceID, contentSHA256 string,
) (Import, error) {
	workspaceID, contentSHA256 = strings.TrimSpace(workspaceID), strings.TrimSpace(contentSHA256)
	if !validUUID(workspaceID) || !validSHA256(contentSHA256) {
		return Import{}, ErrInvalid
	}
	value, err := scanImport(r.db.QueryRowContext(ctx, `
		SELECT `+importColumns+` FROM openapi_imports i
		WHERE i.workspace_id=$1 AND i.content_sha256=$2 AND i.status='SUCCEEDED'
		ORDER BY i.created_at DESC,i.id DESC LIMIT 1
	`, workspaceID, contentSHA256))
	return value, mapRead("find openapi import by checksum", err)
}

func (r *Repository) MarkParsing(ctx context.Context, workspaceID, importID string) (Import, error) {
	if !validUUID(workspaceID) || !validUUID(importID) {
		return Import{}, ErrInvalid
	}
	value, err := scanImport(r.db.QueryRowContext(ctx, `
		UPDATE openapi_imports i SET status='PARSING',updated_at=clock_timestamp()
		WHERE i.workspace_id=$1 AND i.id=$2 AND i.status='PENDING'
		RETURNING `+importColumns,
		workspaceID, importID))
	if errors.Is(err, sql.ErrNoRows) {
		return Import{}, r.classifyStateWrite(ctx, workspaceID, importID)
	}
	if err != nil {
		return Import{}, mapWrite("mark openapi import parsing", err)
	}
	return value, nil
}

func (r *Repository) Complete(
	ctx context.Context,
	workspaceID, importID string,
	input CompleteParseInput,
) (Import, []Endpoint, error) {
	if !validUUID(workspaceID) || !validUUID(importID) || !validCompleteInput(workspaceID, importID, input) {
		return Import{}, nil, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Import{}, nil, fmt.Errorf("begin complete openapi parse transaction: %w", err)
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `
		SELECT status FROM openapi_imports WHERE workspace_id=$1 AND id=$2 FOR UPDATE
	`, workspaceID, importID).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return Import{}, nil, ErrNotFound
	} else if err != nil {
		return Import{}, nil, fmt.Errorf("lock openapi import for completion: %w", err)
	}
	if status != ImportStatusParsing {
		return Import{}, nil, ErrConflict
	}

	readyCount, issueCount := 0, input.ImportIssueCount
	createdEndpoints := make([]Endpoint, 0, len(input.Endpoints))
	for _, endpoint := range input.Endpoints {
		value, err := scanEndpoint(tx.QueryRowContext(ctx, `
			INSERT INTO openapi_endpoints AS e(
			 id,workspace_id,import_id,method,path,operation_id,summary,
			 input_schema,output_schema,issues,ready
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			RETURNING `+endpointColumns,
			endpoint.ID, endpoint.WorkspaceID, endpoint.ImportID, endpoint.Method,
			endpoint.Path, endpoint.OperationID, endpoint.Summary,
			[]byte(endpoint.InputSchema), []byte(endpoint.OutputSchema),
			[]byte(endpoint.Issues), endpoint.Ready))
		if err != nil {
			return Import{}, nil, mapWrite("persist parsed openapi endpoint", err)
		}
		createdEndpoints = append(createdEndpoints, value)
		if value.Ready {
			readyCount++
		}
		var endpointIssues []json.RawMessage
		if err := json.Unmarshal(value.Issues, &endpointIssues); err != nil {
			return Import{}, nil, fmt.Errorf("count persisted endpoint issues: %w", err)
		}
		issueCount += len(endpointIssues)
	}
	value, err := scanImport(tx.QueryRowContext(ctx, `
		UPDATE openapi_imports i SET status='SUCCEEDED',total_endpoints=$3,
		 ready_endpoints=$4,issue_count=$5,updated_at=clock_timestamp()
		WHERE i.workspace_id=$1 AND i.id=$2 AND i.status='PARSING'
		RETURNING `+importColumns,
		workspaceID, importID, len(createdEndpoints), readyCount, issueCount))
	if err != nil {
		return Import{}, nil, mapWrite("complete openapi import", err)
	}
	if err := tx.Commit(); err != nil {
		return Import{}, nil, mapWrite("commit openapi import completion", err)
	}
	return value, createdEndpoints, nil
}

func (r *Repository) Fail(ctx context.Context, workspaceID, importID string) (Import, error) {
	if !validUUID(workspaceID) || !validUUID(importID) {
		return Import{}, ErrInvalid
	}
	value, err := scanImport(r.db.QueryRowContext(ctx, `
		UPDATE openapi_imports i SET status='FAILED',total_endpoints=0,
		 ready_endpoints=0,issue_count=1,updated_at=clock_timestamp()
		WHERE i.workspace_id=$1 AND i.id=$2 AND i.status='PARSING'
		RETURNING `+importColumns,
		workspaceID, importID))
	if errors.Is(err, sql.ErrNoRows) {
		return Import{}, r.classifyStateWrite(ctx, workspaceID, importID)
	}
	if err != nil {
		return Import{}, mapWrite("fail openapi import", err)
	}
	return value, nil
}

func (r *Repository) ListEndpoints(ctx context.Context, workspaceID, importID string) ([]Endpoint, error) {
	if !validUUID(workspaceID) || !validUUID(importID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+endpointColumns+` FROM openapi_endpoints e
		WHERE e.workspace_id=$1 AND e.import_id=$2
		ORDER BY e.path,e.method,e.id
	`, workspaceID, importID)
	if err != nil {
		return nil, fmt.Errorf("list openapi endpoints: %w", err)
	}
	defer rows.Close()
	values := make([]Endpoint, 0)
	for rows.Next() {
		value, err := scanEndpoint(rows)
		if err != nil {
			return nil, fmt.Errorf("scan openapi endpoint: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *Repository) classifyStateWrite(ctx context.Context, workspaceID, importID string) error {
	var status string
	err := r.db.QueryRowContext(ctx, `
		SELECT status FROM openapi_imports WHERE workspace_id=$1 AND id=$2
	`, workspaceID, importID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("classify openapi import state: %w", err)
	}
	return ErrConflict
}

type rowScanner interface{ Scan(...any) error }

func scanImport(row rowScanner) (Import, error) {
	var value Import
	err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.ProviderID, &value.ConnectionID,
		&value.SourceType, &value.SourceURI, &value.SourceRevision, &value.FileName, &value.RawObjectID,
		&value.ContentSHA256, &value.ParserVersion, &value.Status,
		&value.TotalEndpoints, &value.ReadyEndpoints, &value.IssueCount,
		&value.CreatedBy, &value.CreatedAt, &value.UpdatedAt,
	)
	return value, err
}

func scanEndpoint(row rowScanner) (Endpoint, error) {
	var value Endpoint
	err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.ImportID, &value.Method, &value.Path,
		&value.OperationID, &value.Summary, &value.InputSchema, &value.OutputSchema,
		&value.Issues, &value.Ready, &value.GeneratedCapabilityID,
	)
	return value, err
}

func normalizeCreatePending(input CreatePendingInput) CreatePendingInput {
	input.ID = strings.TrimSpace(input.ID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.SourceType = strings.ToUpper(strings.TrimSpace(input.SourceType))
	input.FileName = strings.TrimSpace(input.FileName)
	input.RawObjectID = strings.TrimSpace(input.RawObjectID)
	input.ContentSHA256 = strings.ToLower(strings.TrimSpace(input.ContentSHA256))
	input.ParserVersion = strings.TrimSpace(input.ParserVersion)
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	input.ProviderID = normalizeOptional(input.ProviderID)
	input.ConnectionID = normalizeOptional(input.ConnectionID)
	input.SourceURI = normalizeOptional(input.SourceURI)
	input.SourceRevision = normalizeOptional(input.SourceRevision)
	return input
}

func normalizeOptional(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" {
		return nil
	}
	return &normalized
}

func validCreatePending(input CreatePendingInput) bool {
	if !validUUID(input.ID) || !validUUID(input.WorkspaceID) || !validUUID(input.RawObjectID) ||
		!validUUID(input.CreatedBy) || !validSHA256(input.ContentSHA256) ||
		input.FileName == "" || input.ParserVersion == "" {
		return false
	}
	if input.ProviderID != nil && !validUUID(*input.ProviderID) ||
		input.ConnectionID != nil && !validUUID(*input.ConnectionID) {
		return false
	}
	if input.ConnectionID != nil && input.ProviderID == nil {
		return false
	}
	switch input.SourceType {
	case SourceTypeURL:
		return input.SourceURI != nil
	case SourceTypeFile, SourceTypeRaw:
		return input.SourceURI == nil
	default:
		return false
	}
}

func validCompleteInput(workspaceID, importID string, input CompleteParseInput) bool {
	if input.ImportIssueCount < 0 {
		return false
	}
	for _, endpoint := range input.Endpoints {
		if !validUUID(endpoint.ID) || endpoint.WorkspaceID != workspaceID ||
			endpoint.ImportID != importID || strings.TrimSpace(endpoint.Method) == "" ||
			strings.TrimSpace(endpoint.Path) == "" || !validJSONObject(endpoint.InputSchema) ||
			!validJSONObject(endpoint.OutputSchema) || !validJSONArray(endpoint.Issues) {
			return false
		}
	}
	return true
}

func validJSONObject(value json.RawMessage) bool {
	var decoded any
	return json.Unmarshal(value, &decoded) == nil && func() bool {
		_, ok := decoded.(map[string]any)
		return ok
	}()
}

func validJSONArray(value json.RawMessage) bool {
	var decoded any
	return json.Unmarshal(value, &decoded) == nil && func() bool {
		_, ok := decoded.([]any)
		return ok
	}()
}

func validUUID(value string) bool { _, err := uuid.Parse(strings.TrimSpace(value)); return err == nil }

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func mapRead(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func mapWrite(operation string, err error) error {
	if err == nil {
		return nil
	}
	var databaseError *pq.Error
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23505":
			return fmt.Errorf("%s: %w", operation, ErrConflict)
		case "22001", "22P02", "23502", "23503", "23514":
			return fmt.Errorf("%s: %w", operation, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
