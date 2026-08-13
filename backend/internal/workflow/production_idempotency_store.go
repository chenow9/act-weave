package workflow

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/execution"
)

// PostgresProductionIdempotencyStore persists production execute claims across
// process restarts and replicas. Claim is one autocommit statement; a losing
// replica reads the committed winner after PostgreSQL resolves the conflict.
type PostgresProductionIdempotencyStore struct {
	db         *sql.DB
	executions *execution.RunRepository
}

func NewPostgresProductionIdempotencyStore(
	db *sql.DB,
	executions *execution.RunRepository,
) (*PostgresProductionIdempotencyStore, error) {
	if db == nil || executions == nil {
		return nil, errors.New("workflow production idempotency database and execution repository are required")
	}
	return &PostgresProductionIdempotencyStore{db: db, executions: executions}, nil
}

func (store *PostgresProductionIdempotencyStore) ClaimExecution(
	ctx context.Context,
	workspaceID, actorID, key, requestHash string,
	input execution.StartWorkflowExecutionInput,
) (execution.WorkflowExecution, bool, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	actorID = strings.TrimSpace(actorID)
	key = strings.TrimSpace(key)
	requestHash = strings.TrimSpace(requestHash)
	if store == nil || store.db == nil || store.executions == nil ||
		!validUUID(workspaceID) || !validUUID(actorID) || !validUUID(input.ID) ||
		input.WorkspaceID != workspaceID || key == "" || len(key) > 255 || !validRequestHash(requestHash) {
		return execution.WorkflowExecution{}, false, ErrInvalid
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return execution.WorkflowExecution{}, false, fmt.Errorf("begin workflow production idempotency claim: %w", err)
	}
	defer tx.Rollback()

	var claimedID string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO workflow_production_idempotency(
		 workspace_id,actor_id,idempotency_key,request_hash,execution_id
		) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (workspace_id,actor_id,idempotency_key) DO NOTHING
		RETURNING execution_id::TEXT
	`, workspaceID, actorID, key, requestHash, input.ID).Scan(&claimedID)
	if err == nil {
		started, startErr := store.executions.StartWorkflowExecutionInTransaction(ctx, tx, input)
		if startErr != nil {
			return execution.WorkflowExecution{}, false, startErr
		}
		if err := tx.Commit(); err != nil {
			return execution.WorkflowExecution{}, false, fmt.Errorf("commit workflow production idempotency claim: %w", err)
		}
		return started, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return execution.WorkflowExecution{}, false, fmt.Errorf("claim workflow production idempotency: %w", err)
	}

	var existingHash string
	if err := tx.QueryRowContext(ctx, `
		SELECT request_hash,execution_id::TEXT
		FROM workflow_production_idempotency
		WHERE workspace_id=$1 AND actor_id=$2 AND idempotency_key=$3
	`, workspaceID, actorID, key).Scan(&existingHash, &claimedID); err != nil {
		return execution.WorkflowExecution{}, false, fmt.Errorf("read workflow production idempotency claim: %w", err)
	}
	if existingHash != requestHash {
		return execution.WorkflowExecution{}, false, ErrIdempotencyConflict
	}
	if err := tx.Commit(); err != nil {
		return execution.WorkflowExecution{}, false, fmt.Errorf("commit workflow production idempotency replay: %w", err)
	}
	existing, err := store.executions.GetWorkflowExecution(ctx, workspaceID, claimedID)
	if err != nil {
		return execution.WorkflowExecution{}, false, err
	}
	return existing, false, nil
}

func (store *PostgresProductionIdempotencyStore) Claim(
	ctx context.Context,
	workspaceID, actorID, key, requestHash, newExecutionID string,
) (string, bool, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	actorID = strings.TrimSpace(actorID)
	key = strings.TrimSpace(key)
	requestHash = strings.TrimSpace(requestHash)
	newExecutionID = strings.TrimSpace(newExecutionID)
	if store == nil || store.db == nil || !validUUID(workspaceID) || !validUUID(actorID) ||
		!validUUID(newExecutionID) || key == "" || len(key) > 255 || !validRequestHash(requestHash) {
		return "", false, ErrInvalid
	}

	var executionID string
	err := store.db.QueryRowContext(ctx, `
		INSERT INTO workflow_production_idempotency(
		 workspace_id,actor_id,idempotency_key,request_hash,execution_id
		) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (workspace_id,actor_id,idempotency_key) DO NOTHING
		RETURNING execution_id::TEXT
	`, workspaceID, actorID, key, requestHash, newExecutionID).Scan(&executionID)
	if err == nil {
		return executionID, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("claim workflow production idempotency: %w", err)
	}

	var existingHash string
	if err := store.db.QueryRowContext(ctx, `
		SELECT request_hash,execution_id::TEXT
		FROM workflow_production_idempotency
		WHERE workspace_id=$1 AND actor_id=$2 AND idempotency_key=$3
	`, workspaceID, actorID, key).Scan(&existingHash, &executionID); err != nil {
		return "", false, fmt.Errorf("read workflow production idempotency claim: %w", err)
	}
	if existingHash != requestHash {
		return "", false, ErrIdempotencyConflict
	}
	return executionID, false, nil
}

func validRequestHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}
