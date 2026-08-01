package aapfile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Repository persists aap_files and processing jobs.
type Repository struct {
	db *sql.DB
}

// NewRepository constructs a PostgreSQL-backed repository.
func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("aapfile repository database is required")
	}
	return &Repository{db: db}, nil
}

// fileColumns is the shared projection (unqualified for INSERT/UPDATE RETURNING).
const fileColumns = `
	id, workspace_id, agent_id, actor_type, actor_id, client_id,
	subject_type, subject_id, ownership_mode, ownership_policy_version,
	status, filename, declared_media_type, detected_media_type,
	size_bytes, sha256, staging_bucket, staging_object_key,
	staging_expires_at, staging_deleted_at, stored_object_id, purpose,
	error_code, error_message, processing_version,
	created_at, updated_at, ready_at, retention_until
`

// InsertFile inserts a new PENDING_UPLOAD (or other) file row.
func (r *Repository) InsertFile(ctx context.Context, file File) (File, error) {
	if r == nil || r.db == nil {
		return File{}, ErrInvalid
	}
	value, err := scanFile(r.db.QueryRowContext(ctx, `
		INSERT INTO aap_files (
			id, workspace_id, agent_id, actor_type, actor_id, client_id,
			subject_type, subject_id, ownership_mode, ownership_policy_version,
			status, filename, declared_media_type, detected_media_type,
			size_bytes, sha256, staging_bucket, staging_object_key,
			staging_expires_at, staging_deleted_at, stored_object_id, purpose,
			error_code, error_message, processing_version, retention_until
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,
			$19,$20,$21,$22,$23,$24,$25,$26
		)
		RETURNING `+fileColumns,
		file.ID, file.WorkspaceID, file.AgentID, file.ActorType, file.ActorID, file.ClientID,
		nullableStringPtr(file.SubjectType), nullableStringPtr(file.SubjectID),
		file.OwnershipMode, file.OwnershipPolicyVersion, file.Status,
		nullableStringPtr(file.Filename), file.DeclaredMediaType, nullableStringPtr(file.DetectedMediaType),
		file.SizeBytes, nullableStringPtr(file.SHA256), file.StagingBucket,
		nullableStringPtr(file.StagingObjectKey), file.StagingExpiresAt,
		nullableTimePtr(file.StagingDeletedAt), nullableStringPtr(file.StoredObjectID),
		file.Purpose, nullableStringPtr(file.ErrorCode), nullableStringPtr(file.ErrorMessage),
		file.ProcessingVersion, nullableTimePtr(file.RetentionUntil),
	))
	if err != nil {
		return File{}, mapWrite("insert aap file", err)
	}
	return value, nil
}

// GetFile loads a file by workspace + id.
func (r *Repository) GetFile(ctx context.Context, workspaceID, fileID string) (File, error) {
	if r == nil || r.db == nil || !validUUID(workspaceID) || !validUUID(fileID) {
		return File{}, ErrInvalid
	}
	value, err := scanFile(r.db.QueryRowContext(ctx, `
		SELECT `+fileColumns+` FROM aap_files
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, fileID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return File{}, ErrNotFound
		}
		return File{}, fmt.Errorf("get aap file: %w", err)
	}
	return value, nil
}

// CompleteUploadCAS transitions PENDING_UPLOAD → UPLOADED, enqueues promote,
// and bumps processing_version. Returns the updated file and the new job.
func (r *Repository) CompleteUploadCAS(
	ctx context.Context,
	workspaceID, fileID string,
	expectedVersion int64,
	detectedMediaType *string,
) (File, ProcessingJob, error) {
	if r == nil || r.db == nil {
		return File{}, ProcessingJob{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, ProcessingJob{}, fmt.Errorf("begin complete upload: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	file, err := scanFile(tx.QueryRowContext(ctx, `
		SELECT `+fileColumns+` FROM aap_files
		WHERE workspace_id=$1 AND id=$2
		FOR UPDATE
	`, workspaceID, fileID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return File{}, ProcessingJob{}, ErrNotFound
		}
		return File{}, ProcessingJob{}, fmt.Errorf("lock aap file for complete: %w", err)
	}
	// Idempotent: already UPLOADED or later with promote job present.
	if file.Status != StatusPendingUpload {
		if file.Status == StatusUploaded || file.Status == StatusProcessing ||
			file.Status == StatusReady {
			job, jobErr := r.getJobTx(ctx, tx, workspaceID, fileID, StagePromote)
			if jobErr != nil {
				return File{}, ProcessingJob{}, jobErr
			}
			if err := tx.Commit(); err != nil {
				return File{}, ProcessingJob{}, err
			}
			return file, job, nil
		}
		return File{}, ProcessingJob{}, ErrConflict
	}
	if file.ProcessingVersion != expectedVersion {
		return File{}, ProcessingJob{}, ErrConflict
	}

	updated, err := scanFile(tx.QueryRowContext(ctx, `
		UPDATE aap_files SET
			status=$3,
			detected_media_type=COALESCE($4, detected_media_type),
			processing_version=processing_version+1,
			updated_at=clock_timestamp()
		WHERE workspace_id=$1 AND id=$2 AND status=$5 AND processing_version=$6
		RETURNING `+fileColumns,
		workspaceID, fileID, StatusUploaded, nullableStringPtr(detectedMediaType),
		StatusPendingUpload, expectedVersion,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return File{}, ProcessingJob{}, ErrConflict
		}
		return File{}, ProcessingJob{}, mapWrite("complete aap file cas", err)
	}

	jobID, err := uuid.NewV7()
	if err != nil {
		return File{}, ProcessingJob{}, fmt.Errorf("allocate promote job id: %w", err)
	}
	job, err := scanJob(tx.QueryRowContext(ctx, `
		INSERT INTO aap_file_processing_jobs (
			id, workspace_id, file_id, stage, status, attempt, result
		) VALUES ($1,$2,$3,$4,$5,0,'{}'::JSONB)
		RETURNING
			id, workspace_id, file_id, stage, status, attempt,
			claim_token, claim_expires_at, available_at, deadline_at, delivery_id,
			last_error_code, result, created_at, updated_at
	`, jobID.String(), workspaceID, fileID, StagePromote, JobPending))
	if err != nil {
		return File{}, ProcessingJob{}, mapWrite("enqueue promote job", err)
	}
	if err := tx.Commit(); err != nil {
		return File{}, ProcessingJob{}, fmt.Errorf("commit complete upload: %w", err)
	}
	return updated, job, nil
}

// MarkFileFailed sets terminal FAILED with error code (no permanent object).
func (r *Repository) MarkFileFailed(
	ctx context.Context,
	workspaceID, fileID, errorCode, errorMessage string,
	expectedVersion int64,
) (File, error) {
	if r == nil || r.db == nil {
		return File{}, ErrInvalid
	}
	value, err := scanFile(r.db.QueryRowContext(ctx, `
		UPDATE aap_files SET
			status=$3,
			error_code=$4,
			error_message=$5,
			processing_version=processing_version+1,
			updated_at=clock_timestamp()
		WHERE workspace_id=$1 AND id=$2
		  AND status IN ($6,$7,$8)
		  AND ($9=0 OR processing_version=$9)
		  AND stored_object_id IS NULL
		RETURNING `+fileColumns,
		workspaceID, fileID, StatusFailed, errorCode, errorMessage,
		StatusPendingUpload, StatusUploaded, StatusProcessing, expectedVersion,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return File{}, ErrConflict
		}
		return File{}, mapWrite("mark aap file failed", err)
	}
	return value, nil
}

// ApplyPromoteSuccess writes permanent object id, optionally clears staging
// markers (when stagingDeleted), marks promote SUCCEEDED, enqueues mime_detect
// SUCCEEDED (inline detect), and sets READY when no further required stages remain.
// When stagingDeleted is false, staging_object_key is retained for GC (KD-21 dual-fail).
func (r *Repository) ApplyPromoteSuccess(
	ctx context.Context,
	workspaceID, fileID, storedObjectID, computedSHA256, detectedMediaType string,
	expectedVersion int64,
	ready bool,
	retentionUntil *time.Time,
	stagingDeleted bool,
) (File, error) {
	if r == nil || r.db == nil {
		return File{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, fmt.Errorf("begin promote success: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	status := StatusProcessing
	var readyAt any
	if ready {
		status = StatusReady
		readyAt = time.Now().UTC()
	}

	updated, err := scanFile(tx.QueryRowContext(ctx, `
		UPDATE aap_files SET
			stored_object_id=$3,
			sha256=$4,
			detected_media_type=COALESCE(NULLIF($5,''), detected_media_type),
			status=$6,
			ready_at=CASE WHEN $7::TIMESTAMPTZ IS NOT NULL THEN $7::TIMESTAMPTZ ELSE ready_at END,
			staging_object_key=CASE WHEN $12 THEN NULL ELSE staging_object_key END,
			staging_deleted_at=CASE WHEN $12 THEN clock_timestamp() ELSE staging_deleted_at END,
			retention_until=COALESCE($8, retention_until),
			processing_version=processing_version+1,
			updated_at=clock_timestamp(),
			error_code=NULL,
			error_message=NULL
		WHERE workspace_id=$1 AND id=$2
		  AND status IN ($9,$10)
		  AND stored_object_id IS NULL
		  AND processing_version=$11
		RETURNING `+fileColumns,
		workspaceID, fileID, storedObjectID, computedSHA256, detectedMediaType,
		status, readyAt, nullableTimePtr(retentionUntil),
		StatusUploaded, StatusProcessing, expectedVersion, stagingDeleted,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return File{}, ErrConflict
		}
		return File{}, mapWrite("apply promote success", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE aap_file_processing_jobs SET
			status=$4,
			attempt=attempt+1,
			last_error_code=NULL,
			updated_at=clock_timestamp(),
			result=COALESCE(result,'{}'::JSONB) || jsonb_build_object('sha256', $5::text)
		WHERE workspace_id=$1 AND file_id=$2 AND stage=$3
	`, workspaceID, fileID, StagePromote, JobSucceeded, computedSHA256); err != nil {
		return File{}, mapWrite("succeed promote job", err)
	}

	// Ensure mime_detect stage exists and is SUCCEEDED for the in-process path.
	mimeJobID, err := uuid.NewV7()
	if err != nil {
		return File{}, fmt.Errorf("allocate mime_detect job id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO aap_file_processing_jobs (
			id, workspace_id, file_id, stage, status, attempt, result
		) VALUES ($1,$2,$3,$4,$5,1,jsonb_build_object('mediaType', $6::text))
		ON CONFLICT (workspace_id, file_id, stage) DO UPDATE SET
			status=EXCLUDED.status,
			attempt=aap_file_processing_jobs.attempt+1,
			result=EXCLUDED.result,
			updated_at=clock_timestamp(),
			last_error_code=NULL
	`, mimeJobID.String(), workspaceID, fileID, StageMIMEDetect, JobSucceeded, detectedMediaType); err != nil {
		return File{}, mapWrite("upsert mime_detect job", err)
	}

	if err := tx.Commit(); err != nil {
		return File{}, fmt.Errorf("commit promote success: %w", err)
	}
	return updated, nil
}

// MarkPromoteFailed marks file FAILED and promote job FAILED without permanent object.
func (r *Repository) MarkPromoteFailed(
	ctx context.Context,
	workspaceID, fileID, errorCode, errorMessage string,
	expectedVersion int64,
) (File, error) {
	if r == nil || r.db == nil {
		return File{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, fmt.Errorf("begin promote fail: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	updated, err := scanFile(tx.QueryRowContext(ctx, `
		UPDATE aap_files SET
			status=$3,
			error_code=$4,
			error_message=$5,
			processing_version=processing_version+1,
			updated_at=clock_timestamp()
		WHERE workspace_id=$1 AND id=$2
		  AND status IN ($6,$7)
		  AND stored_object_id IS NULL
		  AND processing_version=$8
		RETURNING `+fileColumns,
		workspaceID, fileID, StatusFailed, errorCode, errorMessage,
		StatusUploaded, StatusProcessing, expectedVersion,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return File{}, ErrConflict
		}
		return File{}, mapWrite("mark promote failed file", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE aap_file_processing_jobs SET
			status=$4,
			attempt=attempt+1,
			last_error_code=$5,
			updated_at=clock_timestamp()
		WHERE workspace_id=$1 AND file_id=$2 AND stage=$3
	`, workspaceID, fileID, StagePromote, JobFailed, errorCode); err != nil {
		return File{}, mapWrite("mark promote job failed", err)
	}
	if err := tx.Commit(); err != nil {
		return File{}, fmt.Errorf("commit promote fail: %w", err)
	}
	return updated, nil
}

// GetJob loads a processing job by stage.
func (r *Repository) GetJob(
	ctx context.Context,
	workspaceID, fileID, stage string,
) (ProcessingJob, error) {
	if r == nil || r.db == nil {
		return ProcessingJob{}, ErrInvalid
	}
	return r.getJobTx(ctx, r.db, workspaceID, fileID, stage)
}

// ListJobs returns all processing jobs for a file (ordered by created_at).
func (r *Repository) ListJobs(
	ctx context.Context,
	workspaceID, fileID string,
) ([]ProcessingJob, error) {
	if r == nil || r.db == nil || !validUUID(workspaceID) || !validUUID(fileID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			id, workspace_id, file_id, stage, status, attempt,
			claim_token, claim_expires_at, available_at, deadline_at, delivery_id,
			last_error_code, result, created_at, updated_at
		FROM aap_file_processing_jobs
		WHERE workspace_id=$1 AND file_id=$2
		ORDER BY created_at ASC, stage ASC
	`, workspaceID, fileID)
	if err != nil {
		return nil, fmt.Errorf("list aap file jobs: %w", err)
	}
	defer rows.Close()
	var jobs []ProcessingJob
	for rows.Next() {
		job, scanErr := scanJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if jobs == nil {
		jobs = []ProcessingJob{}
	}
	return jobs, nil
}

// CountPendingUploads counts PENDING_UPLOAD rows in a workspace.
func (r *Repository) CountPendingUploads(ctx context.Context, workspaceID string) (int, error) {
	if r == nil || r.db == nil || !validUUID(workspaceID) {
		return 0, ErrInvalid
	}
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM aap_files
		WHERE workspace_id=$1 AND status=$2
	`, workspaceID, StatusPendingUpload).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count pending aap files: %w", err)
	}
	return count, nil
}

// SumReadyBytes sums size_bytes of READY files in a workspace.
func (r *Repository) SumReadyBytes(ctx context.Context, workspaceID string) (int64, error) {
	if r == nil || r.db == nil || !validUUID(workspaceID) {
		return 0, ErrInvalid
	}
	var total sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(size_bytes), 0) FROM aap_files
		WHERE workspace_id=$1 AND status=$2
	`, workspaceID, StatusReady).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("sum ready aap file bytes: %w", err)
	}
	if !total.Valid {
		return 0, nil
	}
	return total.Int64, nil
}

// InsertDownloadToken inserts an opaque download token row.
func (r *Repository) InsertDownloadToken(
	ctx context.Context,
	token DownloadToken,
) (DownloadToken, error) {
	if r == nil || r.db == nil {
		return DownloadToken{}, ErrInvalid
	}
	value, err := scanDownloadToken(r.db.QueryRowContext(ctx, `
		INSERT INTO aap_file_download_tokens (
			id, workspace_id, file_id, purpose, jti, single_use,
			consumed_at, max_bytes, expires_at, created_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING
			id, workspace_id, file_id, purpose, jti, single_use,
			consumed_at, max_bytes, expires_at, created_at, created_by
	`, token.ID, token.WorkspaceID, token.FileID, token.Purpose, token.JTI,
		token.SingleUse, nullableTimePtr(token.ConsumedAt), nullableInt64Ptr(token.MaxBytes),
		token.ExpiresAt, token.CreatedBy,
	))
	if err != nil {
		return DownloadToken{}, mapWrite("insert download token", err)
	}
	return value, nil
}

// GetDownloadToken loads a token by id.
func (r *Repository) GetDownloadToken(ctx context.Context, tokenID string) (DownloadToken, error) {
	if r == nil || r.db == nil || !validUUID(tokenID) {
		return DownloadToken{}, ErrInvalid
	}
	value, err := scanDownloadToken(r.db.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, file_id, purpose, jti, single_use,
			consumed_at, max_bytes, expires_at, created_at, created_by
		FROM aap_file_download_tokens
		WHERE id=$1
	`, tokenID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DownloadToken{}, ErrNotFound
		}
		return DownloadToken{}, fmt.Errorf("get download token: %w", err)
	}
	return value, nil
}

// ConsumeDownloadToken CAS-marks single_use token as consumed.
// Fails closed when already consumed, expired, missing, or not single_use.
func (r *Repository) ConsumeDownloadToken(
	ctx context.Context,
	tokenID string,
) (DownloadToken, error) {
	if r == nil || r.db == nil || !validUUID(tokenID) {
		return DownloadToken{}, ErrInvalid
	}
	value, err := scanDownloadToken(r.db.QueryRowContext(ctx, `
		UPDATE aap_file_download_tokens SET
			consumed_at=clock_timestamp()
		WHERE id=$1 AND single_use=true AND consumed_at IS NULL
		  AND expires_at > clock_timestamp()
		RETURNING
			id, workspace_id, file_id, purpose, jti, single_use,
			consumed_at, max_bytes, expires_at, created_at, created_by
	`, tokenID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DownloadToken{}, ErrNotFound
		}
		return DownloadToken{}, mapWrite("consume download token", err)
	}
	return value, nil
}

// PurgeExpiredDownloadTokens deletes expired download token rows (IC-07).
// Also removes long-consumed tokens whose expires_at has passed. Returns
// deleted row count. Safe to call from GC/ops loops; uses expires_at index.
func (r *Repository) PurgeExpiredDownloadTokens(
	ctx context.Context,
	limit int,
) (int, error) {
	if r == nil || r.db == nil {
		return 0, ErrInvalid
	}
	if limit <= 0 {
		limit = DefaultDownloadTokenPurgeBatch
	}
	if limit > 5000 {
		limit = 5000
	}
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM aap_file_download_tokens
		WHERE id IN (
			SELECT id FROM aap_file_download_tokens
			WHERE expires_at <= clock_timestamp()
			ORDER BY expires_at ASC
			LIMIT $1
		)
	`, limit)
	if err != nil {
		return 0, fmt.Errorf("purge expired download tokens: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("purge expired download tokens rows: %w", err)
	}
	return int(n), nil
}

// ListStagingGCCandidates returns files with residual staging blobs eligible for
// GC per design §5.4.3 / KD-21:
//
//	staging_object_key IS NOT NULL AND staging_deleted_at IS NULL AND (
//	  expired PENDING_UPLOAD | FAILED|EXPIRED | stored_object_id set | promote abandoned
//	)
func (r *Repository) ListStagingGCCandidates(
	ctx context.Context,
	limit, maxPromoteAttempts int,
) ([]File, error) {
	if r == nil || r.db == nil {
		return nil, ErrInvalid
	}
	if limit <= 0 {
		limit = DefaultStagingGCBatch
	}
	if limit > 1000 {
		limit = 1000
	}
	if maxPromoteAttempts <= 0 {
		maxPromoteAttempts = DefaultMaxPromoteAttempts
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+fileColumns+`
		FROM aap_files
		WHERE staging_object_key IS NOT NULL
		  AND staging_deleted_at IS NULL
		  AND (
			(status = $1 AND staging_expires_at < clock_timestamp())
			OR status IN ($2, $3)
			OR stored_object_id IS NOT NULL
			OR EXISTS (
				SELECT 1 FROM aap_file_processing_jobs j
				WHERE j.workspace_id = aap_files.workspace_id
				  AND j.file_id = aap_files.id
				  AND j.stage = $4
				  AND j.attempt >= $5
			)
		  )
		ORDER BY staging_expires_at ASC NULLS FIRST, created_at ASC, id ASC
		LIMIT $6
	`, StatusPendingUpload, StatusFailed, StatusExpired, StagePromote, maxPromoteAttempts, limit)
	if err != nil {
		return nil, fmt.Errorf("list staging gc candidates: %w", err)
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		file, scanErr := scanFileRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, file)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []File{}
	}
	return out, nil
}

// MarkStagingCleared confirms staging blob gone: sets staging_deleted_at and
// clears staging_object_key. When expirePending is true and status is still
// PENDING_UPLOAD, also transitions to EXPIRED (upload window elapsed).
// CAS on processing_version. Idempotent when already cleared.
func (r *Repository) MarkStagingCleared(
	ctx context.Context,
	workspaceID, fileID string,
	expectedVersion int64,
	expirePending bool,
) (File, error) {
	if r == nil || r.db == nil || !validUUID(workspaceID) || !validUUID(fileID) {
		return File{}, ErrInvalid
	}
	// Already cleared → return current row (idempotent).
	current, err := r.GetFile(ctx, workspaceID, fileID)
	if err != nil {
		return File{}, err
	}
	if current.StagingDeletedAt != nil &&
		(current.StagingObjectKey == nil || strings.TrimSpace(*current.StagingObjectKey) == "") {
		return current, nil
	}

	value, err := scanFile(r.db.QueryRowContext(ctx, `
		UPDATE aap_files SET
			staging_object_key=NULL,
			staging_deleted_at=clock_timestamp(),
			status=CASE
				WHEN $4 AND status=$5 THEN $6
				ELSE status
			END,
			error_code=CASE
				WHEN $4 AND status=$5 THEN COALESCE(error_code, $7)
				ELSE error_code
			END,
			error_message=CASE
				WHEN $4 AND status=$5 THEN COALESCE(error_message, 'staging upload expired')
				ELSE error_message
			END,
			processing_version=processing_version+1,
			updated_at=clock_timestamp()
		WHERE workspace_id=$1 AND id=$2
		  AND staging_object_key IS NOT NULL
		  AND staging_deleted_at IS NULL
		  AND ($3=0 OR processing_version=$3)
		RETURNING `+fileColumns,
		workspaceID, fileID, expectedVersion, expirePending,
		StatusPendingUpload, StatusExpired, ErrorCodeUploadExpired,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Concurrent clear or version race — re-read.
			return r.GetFile(ctx, workspaceID, fileID)
		}
		return File{}, mapWrite("mark staging cleared", err)
	}
	return value, nil
}

// SumStagingOrphanBytes sums size_bytes of residual staging leftovers that are
// not READY quota (FAILED/EXPIRED or promote-success residual without delete).
// Used for aap_file_staging_orphan_bytes gauge (low-cardinality process total).
func (r *Repository) SumStagingOrphanBytes(ctx context.Context) (int64, error) {
	if r == nil || r.db == nil {
		return 0, ErrInvalid
	}
	var total sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(size_bytes), 0) FROM aap_files
		WHERE staging_object_key IS NOT NULL
		  AND staging_deleted_at IS NULL
	`).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("sum staging orphan bytes: %w", err)
	}
	if !total.Valid {
		return 0, nil
	}
	return total.Int64, nil
}

// CountPendingUploadsGlobal counts all PENDING_UPLOAD rows (process gauge).
func (r *Repository) CountPendingUploadsGlobal(ctx context.Context) (int64, error) {
	if r == nil || r.db == nil {
		return 0, ErrInvalid
	}
	var count int64
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM aap_files WHERE status=$1
	`, StatusPendingUpload).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count pending uploads global: %w", err)
	}
	return count, nil
}

// scanFileRow scans a File from a multi-row result set.
func scanFileRow(row scannable) (File, error) {
	var (
		file                                                File
		subjectType, subjectID, filename, detected, sha256  sql.NullString
		stagingKey, storedObjectID, errorCode, errorMessage sql.NullString
		stagingDeletedAt, readyAt, retentionUntil           sql.NullTime
	)
	err := row.Scan(
		&file.ID, &file.WorkspaceID, &file.AgentID, &file.ActorType, &file.ActorID, &file.ClientID,
		&subjectType, &subjectID, &file.OwnershipMode, &file.OwnershipPolicyVersion,
		&file.Status, &filename, &file.DeclaredMediaType, &detected,
		&file.SizeBytes, &sha256, &file.StagingBucket, &stagingKey,
		&file.StagingExpiresAt, &stagingDeletedAt, &storedObjectID, &file.Purpose,
		&errorCode, &errorMessage, &file.ProcessingVersion,
		&file.CreatedAt, &file.UpdatedAt, &readyAt, &retentionUntil,
	)
	if err != nil {
		return File{}, err
	}
	file.SubjectType = nullStringPtr(subjectType)
	file.SubjectID = nullStringPtr(subjectID)
	file.Filename = nullStringPtr(filename)
	file.DetectedMediaType = nullStringPtr(detected)
	file.SHA256 = nullStringPtr(sha256)
	file.StagingObjectKey = nullStringPtr(stagingKey)
	file.StoredObjectID = nullStringPtr(storedObjectID)
	file.ErrorCode = nullStringPtr(errorCode)
	file.ErrorMessage = nullStringPtr(errorMessage)
	file.StagingDeletedAt = nullTimePtr(stagingDeletedAt)
	file.ReadyAt = nullTimePtr(readyAt)
	file.RetentionUntil = nullTimePtr(retentionUntil)
	return file, nil
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (r *Repository) getJobTx(
	ctx context.Context,
	q queryRower,
	workspaceID, fileID, stage string,
) (ProcessingJob, error) {
	job, err := scanJob(q.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, file_id, stage, status, attempt,
			claim_token, claim_expires_at, available_at, deadline_at, delivery_id,
			last_error_code, result, created_at, updated_at
		FROM aap_file_processing_jobs
		WHERE workspace_id=$1 AND file_id=$2 AND stage=$3
	`, workspaceID, fileID, stage))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProcessingJob{}, ErrNotFound
		}
		return ProcessingJob{}, fmt.Errorf("get aap file job: %w", err)
	}
	return job, nil
}

func scanFile(row *sql.Row) (File, error) {
	return scanFileRow(row)
}

type scannable interface {
	Scan(dest ...any) error
}

func scanJob(row scannable) (ProcessingJob, error) {
	var (
		job                               ProcessingJob
		claimToken, deliveryID, lastError sql.NullString
		claimExpires, deadline            sql.NullTime
		result                            []byte
	)
	err := row.Scan(
		&job.ID, &job.WorkspaceID, &job.FileID, &job.Stage, &job.Status, &job.Attempt,
		&claimToken, &claimExpires, &job.AvailableAt, &deadline, &deliveryID,
		&lastError, &result, &job.CreatedAt, &job.UpdatedAt,
	)
	if err != nil {
		return ProcessingJob{}, err
	}
	job.ClaimToken = nullStringPtr(claimToken)
	job.DeliveryID = nullStringPtr(deliveryID)
	job.LastErrorCode = nullStringPtr(lastError)
	job.ClaimExpiresAt = nullTimePtr(claimExpires)
	job.DeadlineAt = nullTimePtr(deadline)
	job.Result = append([]byte(nil), result...)
	return job, nil
}

func scanDownloadToken(row scannable) (DownloadToken, error) {
	var (
		token              DownloadToken
		consumedAt         sql.NullTime
		maxBytes           sql.NullInt64
	)
	err := row.Scan(
		&token.ID, &token.WorkspaceID, &token.FileID, &token.Purpose, &token.JTI,
		&token.SingleUse, &consumedAt, &maxBytes, &token.ExpiresAt, &token.CreatedAt,
		&token.CreatedBy,
	)
	if err != nil {
		return DownloadToken{}, err
	}
	token.ConsumedAt = nullTimePtr(consumedAt)
	if maxBytes.Valid {
		v := maxBytes.Int64
		token.MaxBytes = &v
	}
	return token, nil
}

func nullableInt64Ptr(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func mapWrite(operation string, err error) error {
	if err == nil {
		return nil
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch pqErr.Code {
		case "23505":
			return fmt.Errorf("%s (%s): %w", operation, pqErr.Constraint, ErrConflict)
		case "23503", "23514", "22P02":
			return fmt.Errorf("%s (%s): %w", operation, pqErr.Constraint, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func validUUID(value string) bool {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil && parsed.String() == value
}

func nullableStringPtr(value *string) any {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return strings.TrimSpace(*value)
}

func nullableTimePtr(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	v := value.String
	return &v
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	v := value.Time.UTC()
	return &v
}

// ---------------------------------------------------------------------------
// Pipeline claim / stage / processor / callback (IC-05)
// ---------------------------------------------------------------------------

// ClaimNextJob claims the next claimable processing job with FOR UPDATE SKIP LOCKED.
// Claimable:
//   - PENDING with available_at <= now and (no claim or claim expired)
//   - DELIVERED with deadline_at <= now (timeout sweep) and claim expired/null
func (r *Repository) ClaimNextJob(
	ctx context.Context,
	claimToken string,
	lease time.Duration,
) (ProcessingJob, bool, error) {
	if r == nil || r.db == nil || !validUUID(claimToken) {
		return ProcessingJob{}, false, ErrInvalid
	}
	if lease < 100*time.Millisecond || lease > 15*time.Minute {
		return ProcessingJob{}, false, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ProcessingJob{}, false, fmt.Errorf("begin claim file job: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	job, err := scanJob(tx.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT id FROM aap_file_processing_jobs
			WHERE (
				(status = $3 AND available_at <= clock_timestamp())
				OR (status = $4 AND deadline_at IS NOT NULL AND deadline_at <= clock_timestamp())
			)
			  AND (claim_token IS NULL OR claim_expires_at IS NULL OR claim_expires_at <= clock_timestamp())
			ORDER BY available_at ASC, created_at ASC, id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE aap_file_processing_jobs j
		SET claim_token=$1,
			claim_expires_at=clock_timestamp()+($2 * interval '1 millisecond'),
			status=CASE
				WHEN j.status=$3 THEN $5
				ELSE j.status
			END,
			attempt=CASE WHEN j.status=$3 THEN j.attempt+1 ELSE j.attempt END,
			updated_at=clock_timestamp()
		FROM candidate c
		WHERE j.id=c.id
		RETURNING
			j.id, j.workspace_id, j.file_id, j.stage, j.status, j.attempt,
			j.claim_token, j.claim_expires_at, j.available_at, j.deadline_at, j.delivery_id,
			j.last_error_code, j.result, j.created_at, j.updated_at
	`, claimToken, lease.Milliseconds(), JobPending, JobDelivered, JobRunning))
	if errors.Is(err, sql.ErrNoRows) {
		return ProcessingJob{}, false, nil
	}
	if err != nil {
		return ProcessingJob{}, false, mapWrite("claim file processing job", err)
	}
	if err := tx.Commit(); err != nil {
		return ProcessingJob{}, false, fmt.Errorf("commit claim file job: %w", err)
	}
	return job, true, nil
}

// ReleaseJobClaim clears claim fields and optionally reschedules available_at (retry).
func (r *Repository) ReleaseJobClaim(
	ctx context.Context,
	jobID, claimToken string,
	nextAvailable *time.Time,
	lastErrorCode string,
	status string,
) error {
	if r == nil || r.db == nil || !validUUID(jobID) || !validUUID(claimToken) {
		return ErrInvalid
	}
	if status == "" {
		status = JobPending
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE aap_file_processing_jobs SET
			status=$3,
			claim_token=NULL,
			claim_expires_at=NULL,
			available_at=COALESCE($4, available_at),
			last_error_code=COALESCE(NULLIF($5,''), last_error_code),
			updated_at=clock_timestamp()
		WHERE id=$1 AND claim_token=$2
	`, jobID, claimToken, status, nullableTimePtr(nextAvailable), lastErrorCode)
	return mapWrite("release file job claim", err)
}

// CompleteClaimedJob marks a claimed job terminal and clears the claim.
func (r *Repository) CompleteClaimedJob(
	ctx context.Context,
	jobID, claimToken, status string,
	lastErrorCode string,
	result []byte,
) (ProcessingJob, error) {
	if r == nil || r.db == nil || !validUUID(jobID) || !validUUID(claimToken) {
		return ProcessingJob{}, ErrInvalid
	}
	if !IsTerminalJobStatus(status) && status != JobDelivered {
		return ProcessingJob{}, ErrInvalid
	}
	if len(result) == 0 {
		result = []byte("{}")
	}
	job, err := scanJob(r.db.QueryRowContext(ctx, `
		UPDATE aap_file_processing_jobs SET
			status=$3,
			claim_token=NULL,
			claim_expires_at=NULL,
			last_error_code=NULLIF($4,''),
			result=CASE WHEN $5::JSONB = '{}'::JSONB THEN result ELSE $5::JSONB END,
			updated_at=clock_timestamp()
		WHERE id=$1 AND claim_token=$2
		RETURNING
			id, workspace_id, file_id, stage, status, attempt,
			claim_token, claim_expires_at, available_at, deadline_at, delivery_id,
			last_error_code, result, created_at, updated_at
	`, jobID, claimToken, status, lastErrorCode, result))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProcessingJob{}, ErrConflict
		}
		return ProcessingJob{}, mapWrite("complete claimed file job", err)
	}
	return job, nil
}

// MarkJobDelivered sets DELIVERED + delivery_id + deadline for a claimed webhook job.
func (r *Repository) MarkJobDelivered(
	ctx context.Context,
	jobID, claimToken, deliveryID string,
	deadline time.Time,
	result []byte,
) (ProcessingJob, error) {
	if r == nil || r.db == nil || !validUUID(jobID) || !validUUID(claimToken) || !validUUID(deliveryID) {
		return ProcessingJob{}, ErrInvalid
	}
	if deadline.IsZero() {
		return ProcessingJob{}, ErrInvalid
	}
	if len(result) == 0 {
		result = []byte("{}")
	}
	job, err := scanJob(r.db.QueryRowContext(ctx, `
		UPDATE aap_file_processing_jobs SET
			status=$3,
			delivery_id=$4,
			deadline_at=$5,
			claim_token=NULL,
			claim_expires_at=NULL,
			last_error_code=NULL,
			result=COALESCE(result,'{}'::JSONB) || $6::JSONB,
			updated_at=clock_timestamp()
		WHERE id=$1 AND claim_token=$2 AND status=$7
		RETURNING
			id, workspace_id, file_id, stage, status, attempt,
			claim_token, claim_expires_at, available_at, deadline_at, delivery_id,
			last_error_code, result, created_at, updated_at
	`, jobID, claimToken, JobDelivered, deliveryID, deadline.UTC(), result, JobRunning))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProcessingJob{}, ErrConflict
		}
		return ProcessingJob{}, mapWrite("mark file job delivered", err)
	}
	return job, nil
}

// InsertJob inserts a processing job if the stage is not already present.
func (r *Repository) InsertJob(ctx context.Context, job ProcessingJob) (ProcessingJob, error) {
	if r == nil || r.db == nil {
		return ProcessingJob{}, ErrInvalid
	}
	if job.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return ProcessingJob{}, fmt.Errorf("allocate job id: %w", err)
		}
		job.ID = id.String()
	}
	if job.Status == "" {
		job.Status = JobPending
	}
	if len(job.Result) == 0 {
		job.Result = []byte("{}")
	}
	value, err := scanJob(r.db.QueryRowContext(ctx, `
		INSERT INTO aap_file_processing_jobs (
			id, workspace_id, file_id, stage, status, attempt, result
		) VALUES ($1,$2,$3,$4,$5,$6,$7::JSONB)
		ON CONFLICT (workspace_id, file_id, stage) DO UPDATE SET
			updated_at=aap_file_processing_jobs.updated_at
		RETURNING
			id, workspace_id, file_id, stage, status, attempt,
			claim_token, claim_expires_at, available_at, deadline_at, delivery_id,
			last_error_code, result, created_at, updated_at
	`, job.ID, job.WorkspaceID, job.FileID, job.Stage, job.Status, job.Attempt, job.Result))
	if err != nil {
		return ProcessingJob{}, mapWrite("insert file processing job", err)
	}
	return value, nil
}

// ListEnabledProcessors returns enabled webhook processors ordered by processor_id.
func (r *Repository) ListEnabledProcessors(
	ctx context.Context,
	workspaceID string,
) ([]WorkspaceFileProcessor, error) {
	if r == nil || r.db == nil || !validUUID(workspaceID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, workspace_id, processor_id, type, url, secret_ref,
			timeout_ms, required, enabled, events, created_at
		FROM aap_workspace_file_processors
		WHERE workspace_id=$1 AND enabled=true
		ORDER BY processor_id ASC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list workspace file processors: %w", err)
	}
	defer rows.Close()
	var out []WorkspaceFileProcessor
	for rows.Next() {
		p, scanErr := scanProcessor(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []WorkspaceFileProcessor{}
	}
	return out, nil
}

// GetProcessor loads a processor by workspace + processor_id.
func (r *Repository) GetProcessor(
	ctx context.Context,
	workspaceID, processorID string,
) (WorkspaceFileProcessor, error) {
	if r == nil || r.db == nil || !validUUID(workspaceID) || strings.TrimSpace(processorID) == "" {
		return WorkspaceFileProcessor{}, ErrInvalid
	}
	value, err := scanProcessor(r.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, processor_id, type, url, secret_ref,
			timeout_ms, required, enabled, events, created_at
		FROM aap_workspace_file_processors
		WHERE workspace_id=$1 AND processor_id=$2
	`, workspaceID, processorID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkspaceFileProcessor{}, ErrNotFound
		}
		return WorkspaceFileProcessor{}, fmt.Errorf("get workspace file processor: %w", err)
	}
	return value, nil
}

// GetJobByDeliveryID loads a job by delivery correlation id.
func (r *Repository) GetJobByDeliveryID(
	ctx context.Context,
	deliveryID string,
) (ProcessingJob, error) {
	if r == nil || r.db == nil || !validUUID(deliveryID) {
		return ProcessingJob{}, ErrInvalid
	}
	job, err := scanJob(r.db.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, file_id, stage, status, attempt,
			claim_token, claim_expires_at, available_at, deadline_at, delivery_id,
			last_error_code, result, created_at, updated_at
		FROM aap_file_processing_jobs
		WHERE delivery_id=$1
		ORDER BY created_at DESC
		LIMIT 1
	`, deliveryID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProcessingJob{}, ErrNotFound
		}
		return ProcessingJob{}, fmt.Errorf("get job by delivery id: %w", err)
	}
	return job, nil
}

// ApplyCallbackCAS transitions DELIVERED → SUCCEEDED|FAILED when delivery_id matches.
// Bumps file processing_version. Idempotent when already terminal with same status.
func (r *Repository) ApplyCallbackCAS(
	ctx context.Context,
	deliveryID, newStatus string,
	result []byte,
) (ProcessingJob, File, error) {
	if r == nil || r.db == nil || !validUUID(deliveryID) {
		return ProcessingJob{}, File{}, ErrInvalid
	}
	if newStatus != JobSucceeded && newStatus != JobFailed {
		return ProcessingJob{}, File{}, ErrInvalid
	}
	if len(result) == 0 {
		result = []byte("{}")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ProcessingJob{}, File{}, fmt.Errorf("begin callback cas: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	job, err := scanJob(tx.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, file_id, stage, status, attempt,
			claim_token, claim_expires_at, available_at, deadline_at, delivery_id,
			last_error_code, result, created_at, updated_at
		FROM aap_file_processing_jobs
		WHERE delivery_id=$1
		FOR UPDATE
	`, deliveryID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProcessingJob{}, File{}, ErrNotFound
		}
		return ProcessingJob{}, File{}, fmt.Errorf("lock job for callback: %w", err)
	}

	// Idempotent replay: already terminal for this delivery.
	if job.Status == JobSucceeded || job.Status == JobFailed {
		file, fileErr := scanFile(tx.QueryRowContext(ctx, `
			SELECT `+fileColumns+` FROM aap_files WHERE workspace_id=$1 AND id=$2
		`, job.WorkspaceID, job.FileID))
		if fileErr != nil {
			return ProcessingJob{}, File{}, fileErr
		}
		if err := tx.Commit(); err != nil {
			return ProcessingJob{}, File{}, err
		}
		return job, file, nil
	}
	if job.Status == JobTimedOut {
		return ProcessingJob{}, File{}, ErrCallbackLate
	}
	if job.Status != JobDelivered {
		return ProcessingJob{}, File{}, ErrConflict
	}
	// Late if deadline already passed.
	if job.DeadlineAt != nil && !job.DeadlineAt.After(time.Now().UTC()) {
		// Leave TIMED_OUT to sweeper; still reject as late.
		return ProcessingJob{}, File{}, ErrCallbackLate
	}

	updatedJob, err := scanJob(tx.QueryRowContext(ctx, `
		UPDATE aap_file_processing_jobs SET
			status=$2,
			result=COALESCE(result,'{}'::JSONB) || $3::JSONB,
			last_error_code=CASE WHEN $2=$4 THEN last_error_code ELSE NULL END,
			updated_at=clock_timestamp()
		WHERE id=$1 AND status=$5 AND delivery_id=$6
		RETURNING
			id, workspace_id, file_id, stage, status, attempt,
			claim_token, claim_expires_at, available_at, deadline_at, delivery_id,
			last_error_code, result, created_at, updated_at
	`, job.ID, newStatus, result, JobFailed, JobDelivered, deliveryID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProcessingJob{}, File{}, ErrConflict
		}
		return ProcessingJob{}, File{}, mapWrite("apply callback job cas", err)
	}

	file, err := scanFile(tx.QueryRowContext(ctx, `
		UPDATE aap_files SET
			processing_version=processing_version+1,
			updated_at=clock_timestamp()
		WHERE workspace_id=$1 AND id=$2
		RETURNING `+fileColumns,
		updatedJob.WorkspaceID, updatedJob.FileID,
	))
	if err != nil {
		return ProcessingJob{}, File{}, mapWrite("bump file processing_version on callback", err)
	}
	if err := tx.Commit(); err != nil {
		return ProcessingJob{}, File{}, fmt.Errorf("commit callback cas: %w", err)
	}
	return updatedJob, file, nil
}

// MarkFileReadyCAS sets READY when PROCESSING and permanent object present.
func (r *Repository) MarkFileReadyCAS(
	ctx context.Context,
	workspaceID, fileID string,
	expectedVersion int64,
) (File, error) {
	if r == nil || r.db == nil {
		return File{}, ErrInvalid
	}
	value, err := scanFile(r.db.QueryRowContext(ctx, `
		UPDATE aap_files SET
			status=$3,
			ready_at=clock_timestamp(),
			error_code=NULL,
			error_message=NULL,
			processing_version=processing_version+1,
			updated_at=clock_timestamp()
		WHERE workspace_id=$1 AND id=$2
		  AND status=$4
		  AND stored_object_id IS NOT NULL
		  AND processing_version=$5
		RETURNING `+fileColumns,
		workspaceID, fileID, StatusReady, StatusProcessing, expectedVersion,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return File{}, ErrConflict
		}
		return File{}, mapWrite("mark file ready", err)
	}
	return value, nil
}

// ClearRetentionUntilCAS promotes retention after first successful createRun
// reference (KD-16). Idempotent when retention_until is already NULL.
func (r *Repository) ClearRetentionUntilCAS(
	ctx context.Context,
	workspaceID, fileID string,
	expectedVersion int64,
) (File, error) {
	if r == nil || r.db == nil {
		return File{}, ErrInvalid
	}
	value, err := scanFile(r.db.QueryRowContext(ctx, `
		UPDATE aap_files SET
			retention_until=NULL,
			processing_version=processing_version+1,
			updated_at=clock_timestamp()
		WHERE workspace_id=$1 AND id=$2
		  AND status=$3
		  AND processing_version=$4
		  AND retention_until IS NOT NULL
		RETURNING `+fileColumns,
		workspaceID, fileID, StatusReady, expectedVersion,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Already permanent or version race — caller reloads for idempotent no-op.
			return File{}, ErrConflict
		}
		return File{}, mapWrite("clear file retention", err)
	}
	return value, nil
}

// MarkFileProcessingFailedCAS marks PROCESSING/UPLOADED file FAILED even when
// a permanent object already exists (required stage failure after promote).
func (r *Repository) MarkFileProcessingFailedCAS(
	ctx context.Context,
	workspaceID, fileID, errorCode, errorMessage string,
	expectedVersion int64,
) (File, error) {
	if r == nil || r.db == nil {
		return File{}, ErrInvalid
	}
	value, err := scanFile(r.db.QueryRowContext(ctx, `
		UPDATE aap_files SET
			status=$3,
			error_code=$4,
			error_message=$5,
			processing_version=processing_version+1,
			updated_at=clock_timestamp()
		WHERE workspace_id=$1 AND id=$2
		  AND status IN ($6,$7)
		  AND processing_version=$8
		RETURNING `+fileColumns,
		workspaceID, fileID, StatusFailed, errorCode, errorMessage,
		StatusUploaded, StatusProcessing, expectedVersion,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return File{}, ErrConflict
		}
		return File{}, mapWrite("mark file processing failed", err)
	}
	return value, nil
}

// InsertArtifact inserts a derived artifact row.
func (r *Repository) InsertArtifact(ctx context.Context, art FileArtifact) (FileArtifact, error) {
	if r == nil || r.db == nil {
		return FileArtifact{}, ErrInvalid
	}
	if art.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return FileArtifact{}, err
		}
		art.ID = id.String()
	}
	var createdAt time.Time
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO aap_file_artifacts (
			id, workspace_id, file_id, kind, media_type, stored_object_id, processor_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING created_at
	`, art.ID, art.WorkspaceID, art.FileID, art.Kind, art.MediaType,
		art.StoredObjectID, art.ProcessorID,
	).Scan(&createdAt)
	if err != nil {
		return FileArtifact{}, mapWrite("insert file artifact", err)
	}
	art.CreatedAt = createdAt.UTC()
	return art, nil
}

func scanProcessor(row scannable) (WorkspaceFileProcessor, error) {
	var (
		p      WorkspaceFileProcessor
		events pq.StringArray
	)
	err := row.Scan(
		&p.ID, &p.WorkspaceID, &p.ProcessorID, &p.Type, &p.URL, &p.SecretRef,
		&p.TimeoutMs, &p.Required, &p.Enabled, &events, &p.CreatedAt,
	)
	if err != nil {
		return WorkspaceFileProcessor{}, err
	}
	p.Events = append([]string(nil), events...)
	p.CreatedAt = p.CreatedAt.UTC()
	return p, nil
}
