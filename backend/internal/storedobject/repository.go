package storedobject

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var (
	ErrNotFound = errors.New("stored object not found")
	ErrConflict = errors.New("stored object conflict")
	ErrInvalid  = errors.New("invalid stored object")
)

var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

const storedObjectColumns = `
	so.id,so.workspace_id,so.bucket,so.object_key,so.kind,so.content_type,
	so.size_bytes,so.sha256,so.encryption_key_id,so.classification,
	so.retention_mode,so.retention_until,so.created_by_type,so.created_by_id,
	so.created_at
`

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("stored object repository database is required")
	}
	return &Repository{db: db}, nil
}

func (repository *Repository) Create(
	ctx context.Context,
	input CreateInput,
) (StoredObject, error) {
	input = normalizeCreate(input)
	if !validCreate(input) {
		return StoredObject{}, ErrInvalid
	}
	value, err := scanStoredObject(repository.db.QueryRowContext(ctx, `
		INSERT INTO stored_objects AS so(
		 id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
		 encryption_key_id,classification,retention_mode,retention_until,
		 created_by_type,created_by_id
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING `+storedObjectColumns,
		input.ID, input.WorkspaceID, input.Bucket, input.ObjectKey, input.Kind,
		input.ContentType, input.SizeBytes, input.SHA256,
		nullableString(input.EncryptionKeyID), input.Classification,
		input.RetentionMode, nullableTime(input.RetentionUntil),
		input.CreatedByType, input.CreatedByID,
	))
	if err != nil {
		return StoredObject{}, mapWrite("create stored object", err)
	}
	return value, nil
}

func (repository *Repository) Get(
	ctx context.Context,
	workspaceID, objectID string,
) (StoredObject, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	objectID = strings.TrimSpace(objectID)
	if !validUUID(workspaceID) || !validUUID(objectID) {
		return StoredObject{}, ErrInvalid
	}
	value, err := scanStoredObject(repository.db.QueryRowContext(ctx, `
		SELECT `+storedObjectColumns+` FROM stored_objects so
		WHERE so.workspace_id=$1 AND so.id=$2
	`, workspaceID, objectID))
	return value, mapRead("get stored object", err)
}

func (repository *Repository) GetByKey(
	ctx context.Context,
	workspaceID, bucket, objectKey string,
) (StoredObject, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	bucket = strings.TrimSpace(bucket)
	if !validUUID(workspaceID) || !validBucket(bucket) || !validObjectKey(objectKey) {
		return StoredObject{}, ErrInvalid
	}
	value, err := scanStoredObject(repository.db.QueryRowContext(ctx, `
		SELECT `+storedObjectColumns+` FROM stored_objects so
		WHERE so.workspace_id=$1 AND so.bucket=$2 AND so.object_key=$3
	`, workspaceID, bucket, objectKey))
	return value, mapRead("get stored object by key", err)
}

func (repository *Repository) List(
	ctx context.Context,
	input ListInput,
) ([]StoredObject, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Kind = strings.ToUpper(strings.TrimSpace(input.Kind))
	input.Classification = strings.ToUpper(strings.TrimSpace(input.Classification))
	input.RetentionMode = strings.ToUpper(strings.TrimSpace(input.RetentionMode))
	if !validUUID(input.WorkspaceID) || input.Limit <= 0 || input.Limit > 500 ||
		(input.Kind != "" && !validKind(input.Kind)) ||
		(input.Classification != "" && !validClassification(input.Classification)) ||
		(input.RetentionMode != "" && !validRetentionMode(input.RetentionMode)) {
		return nil, ErrInvalid
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT `+storedObjectColumns+` FROM stored_objects so
		WHERE so.workspace_id=$1
		  AND ($2::text='' OR so.kind=$2)
		  AND ($3::text='' OR so.classification=$3)
		  AND ($4::text='' OR so.retention_mode=$4)
		ORDER BY so.created_at DESC,so.id
		LIMIT $5
	`, input.WorkspaceID, input.Kind, input.Classification, input.RetentionMode, input.Limit)
	if err != nil {
		return nil, fmt.Errorf("list stored objects: %w", err)
	}
	defer rows.Close()
	values := make([]StoredObject, 0)
	for rows.Next() {
		value, err := scanStoredObject(rows)
		if err != nil {
			return nil, fmt.Errorf("scan stored object list: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan stored object list: %w", err)
	}
	return values, nil
}

type scanner interface{ Scan(...any) error }

func scanStoredObject(row scanner) (StoredObject, error) {
	var value StoredObject
	var encryptionKeyID sql.NullString
	var retentionUntil sql.NullTime
	err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.Bucket, &value.ObjectKey,
		&value.Kind, &value.ContentType, &value.SizeBytes, &value.SHA256,
		&encryptionKeyID, &value.Classification, &value.RetentionMode,
		&retentionUntil, &value.CreatedByType, &value.CreatedByID, &value.CreatedAt,
	)
	if err != nil {
		return StoredObject{}, err
	}
	value.EncryptionKeyID = encryptionKeyID.String
	if retentionUntil.Valid {
		timestamp := retentionUntil.Time.UTC()
		value.RetentionUntil = &timestamp
	}
	value.CreatedAt = value.CreatedAt.UTC()
	return value, nil
}

func normalizeCreate(input CreateInput) CreateInput {
	input.ID = strings.TrimSpace(input.ID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Bucket = strings.TrimSpace(input.Bucket)
	input.Kind = strings.ToUpper(strings.TrimSpace(input.Kind))
	input.ContentType = strings.TrimSpace(input.ContentType)
	input.SHA256 = strings.ToLower(strings.TrimSpace(input.SHA256))
	input.EncryptionKeyID = strings.TrimSpace(input.EncryptionKeyID)
	input.Classification = strings.ToUpper(strings.TrimSpace(input.Classification))
	input.RetentionMode = strings.ToUpper(strings.TrimSpace(input.RetentionMode))
	input.CreatedByType = strings.ToUpper(strings.TrimSpace(input.CreatedByType))
	input.CreatedByID = strings.TrimSpace(input.CreatedByID)
	return input
}

func validCreate(input CreateInput) bool {
	if !validUUID(input.ID) || !validUUID(input.WorkspaceID) ||
		!validUUID(input.CreatedByID) || !validBucket(input.Bucket) ||
		!validObjectKey(input.ObjectKey) || !validKind(input.Kind) ||
		input.ContentType == "" || len(input.ContentType) > 255 || input.SizeBytes < 0 ||
		!validHash(input.SHA256) || !validClassification(input.Classification) ||
		!validRetentionMode(input.RetentionMode) || !validCreatorType(input.CreatedByType) {
		return false
	}
	if input.RetentionMode == RetentionPermanent {
		return input.RetentionUntil == nil
	}
	return input.RetentionUntil != nil && !input.RetentionUntil.IsZero()
}

func validBucket(value string) bool {
	return bucketPattern.MatchString(value) && !strings.Contains(value, "..") &&
		!strings.Contains(value, ".-") && !strings.Contains(value, "-.")
}

func validObjectKey(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 1024 ||
		strings.HasPrefix(value, "/") || strings.Contains(value, `\`) {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validKind(value string) bool {
	switch value {
	case KindOpenAPISource, KindPromptRunInput, KindPromptRunOutput, KindModelTurn,
		KindChatMessage, KindToolTestPayload, KindToolInvocationPayload,
		KindExecutionCheckpoint, KindAuditEventPayload, KindAuditExport:
		return true
	default:
		return false
	}
}

func validClassification(value string) bool {
	switch value {
	case ClassificationPublic, ClassificationInternal, ClassificationSensitive, ClassificationRestricted:
		return true
	default:
		return false
	}
}

func validRetentionMode(value string) bool {
	return value == RetentionPermanent || value == RetentionExpiring
}

func validCreatorType(value string) bool {
	return value == CreatorUser || value == CreatorServicePrincipal || value == CreatorSystem
}

func validHash(value string) bool {
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

func validUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
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
		case "23505", "40001", "55000":
			return fmt.Errorf("%s (%s): %w", operation, databaseError.Constraint, ErrConflict)
		case "23502", "23503", "23514", "22P02":
			return fmt.Errorf("%s (%s): %w", operation, databaseError.Constraint, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
