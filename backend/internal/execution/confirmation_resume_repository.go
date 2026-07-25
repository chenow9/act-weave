package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

var (
	ErrConfirmationResumeNotFound  = errors.New("confirmation resume checkpoint not found")
	ErrConfirmationResumeConflict  = errors.New("confirmation resume checkpoint conflict")
	ErrConfirmationResumeInvalid   = errors.New("invalid confirmation resume checkpoint")
	ErrConfirmationResumeExecuting = errors.New("confirmation resume side effect already executing")
)

const confirmationResumeColumns = `
	crc.confirmation_id,crc.workspace_id,crc.kind,crc.run_id,crc.target_item_id,crc.execution_id,
	crc.agent_run_step_id,crc.execution_step_id,crc.node_id,
	crc.run_wait_lock_version,crc.execution_wait_lock_version,crc.status,
	crc.snapshot_schema_version,crc.request_snapshot,crc.resolved_snapshot,
	crc.input_payload,crc.input_hash,crc.plan_hash,crc.interaction_binding_hash,crc.terminal_on_success,
	crc.result_snapshot,crc.error_code,crc.claim_id,crc.claim_expires_at,
	crc.created_at,crc.started_at,crc.completed_at,crc.lock_version
`

type ConfirmationResumeRepository struct {
	db *sql.DB
}

func NewConfirmationResumeRepository(db *sql.DB) (*ConfirmationResumeRepository, error) {
	if db == nil {
		return nil, errors.New("confirmation resume repository database is required")
	}
	return &ConfirmationResumeRepository{db: db}, nil
}

func (repository *ConfirmationResumeRepository) begin(ctx context.Context) (*sql.Tx, error) {
	return repository.db.BeginTx(ctx, nil)
}

func (repository *ConfirmationResumeRepository) createInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	input ConfirmationResumeCheckpoint,
) (ConfirmationResumeCheckpoint, error) {
	if tx == nil {
		return ConfirmationResumeCheckpoint{}, ErrConfirmationResumeInvalid
	}
	value, err := scanConfirmationResume(tx.QueryRowContext(ctx, `
		INSERT INTO confirmation_resume_checkpoints AS crc(
			confirmation_id,workspace_id,kind,run_id,target_item_id,execution_id,
			agent_run_step_id,execution_step_id,node_id,run_wait_lock_version,
			execution_wait_lock_version,status,snapshot_schema_version,
			request_snapshot,resolved_snapshot,input_payload,input_hash,plan_hash,interaction_binding_hash,
			terminal_on_success,result_snapshot,created_at,lock_version
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'PENDING',$12,$13,$14,$15,$16,$17,$18,$19,'{}',$20,1)
		RETURNING `+confirmationResumeColumns,
		input.ConfirmationID, input.WorkspaceID, input.Kind,
		nullableConfirmationString(input.RunID), input.TargetItemID, nullableConfirmationString(input.ExecutionID),
		nullableConfirmationString(input.AgentRunStepID), nullableConfirmationString(input.ExecutionStepID),
		input.NodeID, nullableInt64(input.RunWaitLockVersion), nullableInt64(input.ExecutionWaitLockVersion),
		input.SnapshotSchemaVersion, []byte(input.RequestSnapshot), []byte(input.ResolvedSnapshot),
		[]byte(input.Input), input.InputHash, nullableConfirmationString(input.PlanHash),
		input.InteractionBindingHash, input.TerminalOnSuccess, input.CreatedAt,
	))
	if err != nil {
		return ConfirmationResumeCheckpoint{}, mapConfirmationResumeWrite("create confirmation resume checkpoint", err)
	}
	return value, nil
}

func (repository *ConfirmationResumeRepository) Get(
	ctx context.Context,
	workspaceID, confirmationID string,
) (ConfirmationResumeCheckpoint, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	confirmationID = strings.TrimSpace(confirmationID)
	if !invocationValidUUID(workspaceID) || !invocationValidUUID(confirmationID) {
		return ConfirmationResumeCheckpoint{}, ErrConfirmationResumeInvalid
	}
	return repository.getWith(ctx, repository.db, workspaceID, confirmationID, false)
}

func (repository *ConfirmationResumeRepository) getForUpdate(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, confirmationID string,
) (ConfirmationResumeCheckpoint, error) {
	if tx == nil {
		return ConfirmationResumeCheckpoint{}, ErrConfirmationResumeInvalid
	}
	return repository.getWith(ctx, tx, workspaceID, confirmationID, true)
}

func (repository *ConfirmationResumeRepository) getWith(
	ctx context.Context,
	queryer confirmationQueryRower,
	workspaceID, confirmationID string,
	forUpdate bool,
) (ConfirmationResumeCheckpoint, error) {
	suffix := ""
	if forUpdate {
		suffix = " FOR UPDATE OF crc"
	}
	value, err := scanConfirmationResume(queryer.QueryRowContext(ctx, `
		SELECT `+confirmationResumeColumns+`
		FROM confirmation_resume_checkpoints crc
		WHERE crc.workspace_id=$1 AND crc.confirmation_id=$2
	`+suffix, workspaceID, confirmationID))
	return value, mapConfirmationResumeRead("get confirmation resume checkpoint", err)
}

func (repository *ConfirmationResumeRepository) listDueInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	now time.Time,
	limit int,
) ([]ConfirmationResumeCheckpoint, error) {
	if tx == nil || now.IsZero() || limit <= 0 || limit > 1000 {
		return nil, ErrConfirmationResumeInvalid
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT `+confirmationResumeColumns+`
		FROM confirmation_resume_checkpoints crc
		JOIN execution_confirmations ec
		  ON ec.workspace_id=crc.workspace_id AND ec.id=crc.confirmation_id
		WHERE crc.status='PENDING' AND ec.status='PENDING' AND ec.expires_at<=$1
		ORDER BY ec.expires_at,crc.confirmation_id
		FOR UPDATE OF crc,ec SKIP LOCKED
		LIMIT $2
	`, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("list due confirmation resume checkpoints: %w", err)
	}
	defer rows.Close()
	values := make([]ConfirmationResumeCheckpoint, 0)
	for rows.Next() {
		value, err := scanConfirmationResume(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan due confirmation resume checkpoints: %w", err)
	}
	return values, nil
}

func (repository *ConfirmationResumeRepository) claimInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, confirmationID, claimID string,
	now time.Time,
	lease time.Duration,
) (confirmationResumeClaim, error) {
	if tx == nil || !invocationValidUUID(workspaceID) || !invocationValidUUID(confirmationID) ||
		!invocationValidUUID(claimID) || now.IsZero() || lease < time.Second || lease > time.Minute {
		return confirmationResumeClaim{}, ErrConfirmationResumeInvalid
	}
	var checkpoint ConfirmationResumeCheckpoint
	var previousStatus string
	row := tx.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT confirmation_id,status
			FROM confirmation_resume_checkpoints
			WHERE workspace_id=$1 AND confirmation_id=$2
			  AND (status='PENDING' OR (status='CLAIMED' AND claim_expires_at<=$4))
			FOR UPDATE
		), updated AS (
			UPDATE confirmation_resume_checkpoints crc
			SET status='CLAIMED',claim_id=$3,claim_expires_at=$5,lock_version=lock_version+1
			FROM candidate
			WHERE crc.confirmation_id=candidate.confirmation_id
			RETURNING `+confirmationResumeColumns+`
		)
		SELECT updated.*,candidate.status FROM updated JOIN candidate USING (confirmation_id)
	`, workspaceID, confirmationID, claimID, now.UTC(), now.UTC().Add(lease))
	var runID, targetItemID, executionID, agentStepID, executionStepID, planHash, bindingHash, errorCode, storedClaimID sql.NullString
	var runLock, executionLock sql.NullInt64
	var claimExpires, startedAt, completedAt sql.NullTime
	var requestSnapshot, resolvedSnapshot, inputPayload, resultSnapshot []byte
	err := row.Scan(
		&checkpoint.ConfirmationID, &checkpoint.WorkspaceID, &checkpoint.Kind,
		&runID, &targetItemID, &executionID, &agentStepID, &executionStepID, &checkpoint.NodeID,
		&runLock, &executionLock, &checkpoint.Status, &checkpoint.SnapshotSchemaVersion,
		&requestSnapshot, &resolvedSnapshot, &inputPayload, &checkpoint.InputHash,
		&planHash, &bindingHash, &checkpoint.TerminalOnSuccess, &resultSnapshot, &errorCode,
		&storedClaimID, &claimExpires, &checkpoint.CreatedAt, &startedAt,
		&completedAt, &checkpoint.LockVersion, &previousStatus,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return confirmationResumeClaim{}, repository.classifyClaim(ctx, workspaceID, confirmationID, now)
	}
	if err != nil {
		return confirmationResumeClaim{}, mapConfirmationResumeWrite("claim confirmation resume checkpoint", err)
	}
	hydrateConfirmationResume(&checkpoint, runID, targetItemID, executionID, agentStepID, executionStepID,
		runLock, executionLock, planHash, bindingHash, errorCode, storedClaimID, claimExpires,
		startedAt, completedAt, requestSnapshot, resolvedSnapshot, inputPayload, resultSnapshot)
	return confirmationResumeClaim{Checkpoint: checkpoint, Recovered: previousStatus == ResumeStatusClaimed}, nil
}

func (repository *ConfirmationResumeRepository) markExecuting(
	ctx context.Context,
	workspaceID, confirmationID, claimID string,
	now time.Time,
) (ConfirmationResumeCheckpoint, error) {
	value, err := scanConfirmationResume(repository.db.QueryRowContext(ctx, `
		UPDATE confirmation_resume_checkpoints crc
		SET status='EXECUTING',started_at=$4,lock_version=lock_version+1
		WHERE workspace_id=$1 AND confirmation_id=$2 AND status='CLAIMED'
		  AND claim_id=$3 AND claim_expires_at>$4
		RETURNING `+confirmationResumeColumns,
		workspaceID, confirmationID, claimID, now.UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		return ConfirmationResumeCheckpoint{}, repository.classifyClaim(ctx, workspaceID, confirmationID, now)
	}
	if err != nil {
		return ConfirmationResumeCheckpoint{}, mapConfirmationResumeWrite("start confirmation resume side effect", err)
	}
	return value, nil
}

func (repository *ConfirmationResumeRepository) completeInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	checkpoint ConfirmationResumeCheckpoint,
	status string,
	result json.RawMessage,
	errorCode string,
	now time.Time,
) (ConfirmationResumeCheckpoint, error) {
	if tx == nil || (status != ResumeStatusSucceeded && status != ResumeStatusFailed) ||
		(status == ResumeStatusFailed) != (strings.TrimSpace(errorCode) != "") {
		return ConfirmationResumeCheckpoint{}, ErrConfirmationResumeInvalid
	}
	result, err := canonicalInvocationObject(result)
	if err != nil {
		return ConfirmationResumeCheckpoint{}, ErrConfirmationResumeInvalid
	}
	value, err := scanConfirmationResume(tx.QueryRowContext(ctx, `
		UPDATE confirmation_resume_checkpoints crc
		SET status=$4,result_snapshot=$5,error_code=$6,completed_at=$7,
		 lock_version=lock_version+1
		WHERE workspace_id=$1 AND confirmation_id=$2 AND status='EXECUTING' AND claim_id=$3
		RETURNING `+confirmationResumeColumns,
		checkpoint.WorkspaceID, checkpoint.ConfirmationID, checkpoint.ClaimID,
		status, []byte(result), nullableConfirmationString(errorCode), now.UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		return ConfirmationResumeCheckpoint{}, ErrConfirmationResumeConflict
	}
	if err != nil {
		return ConfirmationResumeCheckpoint{}, mapConfirmationResumeWrite("complete confirmation resume checkpoint", err)
	}
	return value, nil
}

func (repository *ConfirmationResumeRepository) cancelInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, confirmationID string,
	now time.Time,
) (ConfirmationResumeCheckpoint, error) {
	if tx == nil {
		return ConfirmationResumeCheckpoint{}, ErrConfirmationResumeInvalid
	}
	value, err := scanConfirmationResume(tx.QueryRowContext(ctx, `
		UPDATE confirmation_resume_checkpoints crc
		SET status='CANCELLED',completed_at=$3,lock_version=lock_version+1
		WHERE workspace_id=$1 AND confirmation_id=$2 AND status='PENDING'
		RETURNING `+confirmationResumeColumns,
		workspaceID, confirmationID, now.UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		return ConfirmationResumeCheckpoint{}, ErrConfirmationResumeConflict
	}
	if err != nil {
		return ConfirmationResumeCheckpoint{}, mapConfirmationResumeWrite("cancel confirmation resume checkpoint", err)
	}
	return value, nil
}

func (repository *ConfirmationResumeRepository) classifyClaim(
	ctx context.Context,
	workspaceID, confirmationID string,
	now time.Time,
) error {
	checkpoint, err := repository.Get(ctx, workspaceID, confirmationID)
	if err != nil {
		return err
	}
	switch checkpoint.Status {
	case ResumeStatusSucceeded:
		return ErrConfirmationResumeConflict
	case ResumeStatusExecuting:
		return ErrConfirmationResumeExecuting
	case ResumeStatusClaimed:
		if checkpoint.ClaimExpiresAt != nil && checkpoint.ClaimExpiresAt.After(now) {
			return ErrConfirmationResumeConflict
		}
	}
	return ErrConfirmationResumeConflict
}

type confirmationResumeScanner interface{ Scan(...any) error }

func scanConfirmationResume(scanner confirmationResumeScanner) (ConfirmationResumeCheckpoint, error) {
	var value ConfirmationResumeCheckpoint
	var runID, targetItemID, executionID, agentStepID, executionStepID, planHash, bindingHash, errorCode, claimID sql.NullString
	var runLock, executionLock sql.NullInt64
	var claimExpires, startedAt, completedAt sql.NullTime
	var requestSnapshot, resolvedSnapshot, inputPayload, resultSnapshot []byte
	err := scanner.Scan(
		&value.ConfirmationID, &value.WorkspaceID, &value.Kind,
		&runID, &targetItemID, &executionID, &agentStepID, &executionStepID, &value.NodeID,
		&runLock, &executionLock, &value.Status, &value.SnapshotSchemaVersion,
		&requestSnapshot, &resolvedSnapshot, &inputPayload, &value.InputHash,
		&planHash, &bindingHash, &value.TerminalOnSuccess, &resultSnapshot, &errorCode,
		&claimID, &claimExpires, &value.CreatedAt, &startedAt,
		&completedAt, &value.LockVersion,
	)
	if err != nil {
		return value, err
	}
	hydrateConfirmationResume(&value, runID, targetItemID, executionID, agentStepID, executionStepID,
		runLock, executionLock, planHash, bindingHash, errorCode, claimID, claimExpires,
		startedAt, completedAt, requestSnapshot, resolvedSnapshot, inputPayload, resultSnapshot)
	return value, nil
}

func hydrateConfirmationResume(
	value *ConfirmationResumeCheckpoint,
	runID, targetItemID, executionID, agentStepID, executionStepID sql.NullString,
	runLock, executionLock sql.NullInt64,
	planHash, bindingHash, errorCode, claimID sql.NullString,
	claimExpires, startedAt, completedAt sql.NullTime,
	requestSnapshot, resolvedSnapshot, inputPayload, resultSnapshot []byte,
) {
	value.RunID, value.TargetItemID, value.ExecutionID = runID.String, targetItemID.String, executionID.String
	value.AgentRunStepID, value.ExecutionStepID = agentStepID.String, executionStepID.String
	value.PlanHash, value.InteractionBindingHash = planHash.String, bindingHash.String
	value.ErrorCode, value.ClaimID = errorCode.String, claimID.String
	if runLock.Valid {
		lock := runLock.Int64
		value.RunWaitLockVersion = &lock
	}
	if executionLock.Valid {
		lock := executionLock.Int64
		value.ExecutionWaitLockVersion = &lock
	}
	value.RequestSnapshot = append(json.RawMessage(nil), requestSnapshot...)
	value.ResolvedSnapshot = append(json.RawMessage(nil), resolvedSnapshot...)
	value.Input = append(json.RawMessage(nil), inputPayload...)
	value.ResultSnapshot = append(json.RawMessage(nil), resultSnapshot...)
	if claimExpires.Valid {
		timestamp := claimExpires.Time.UTC()
		value.ClaimExpiresAt = &timestamp
	}
	if startedAt.Valid {
		timestamp := startedAt.Time.UTC()
		value.StartedAt = &timestamp
	}
	if completedAt.Valid {
		timestamp := completedAt.Time.UTC()
		value.CompletedAt = &timestamp
	}
	value.CreatedAt = value.CreatedAt.UTC()
}

func mapConfirmationResumeRead(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConfirmationResumeNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func mapConfirmationResumeWrite(operation string, err error) error {
	var databaseError *pq.Error
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23505", "40001", "55000":
			return fmt.Errorf("%s: %w", operation, ErrConfirmationResumeConflict)
		case "23502", "23503", "23514", "22P02":
			return fmt.Errorf("%s: %w", operation, ErrConfirmationResumeInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
