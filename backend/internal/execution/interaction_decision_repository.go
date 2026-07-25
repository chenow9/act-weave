package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"
)

type InteractionDecisionRepository struct {
	db *sql.DB
}

func NewInteractionDecisionRepository(db *sql.DB) (*InteractionDecisionRepository, error) {
	if db == nil {
		return nil, errors.New("interaction decision repository database is required")
	}
	return &InteractionDecisionRepository{db: db}, nil
}

func (repository *InteractionDecisionRepository) begin(ctx context.Context) (*sql.Tx, error) {
	return repository.db.BeginTx(ctx, nil)
}

func (repository *InteractionDecisionRepository) get(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, confirmationID, principalHash, idempotencyKey string,
) (interactionDecisionCommand, error) {
	if tx == nil {
		return interactionDecisionCommand{}, ErrInteractionDecisionInvalid
	}
	var value interactionDecisionCommand
	err := tx.QueryRowContext(ctx, `
		SELECT workspace_id,confirmation_id,principal_binding_hash,idempotency_key,
		       request_hash,decision,expected_version,confirmation_status,
		       confirmation_version,created_at
		FROM interaction_decision_commands
		WHERE workspace_id=$1 AND confirmation_id=$2
		  AND principal_binding_hash=$3 AND idempotency_key=$4
	`, workspaceID, confirmationID, principalHash, idempotencyKey).Scan(
		&value.WorkspaceID, &value.ConfirmationID, &value.PrincipalBindingHash,
		&value.IdempotencyKey, &value.RequestHash, &value.Decision,
		&value.ExpectedVersion, &value.ConfirmationStatus,
		&value.ConfirmationVersion, &value.CreatedAt,
	)
	if err != nil {
		return interactionDecisionCommand{}, err
	}
	value.CreatedAt = value.CreatedAt.UTC()
	return value, nil
}

func (repository *InteractionDecisionRepository) create(
	ctx context.Context,
	tx *sql.Tx,
	value interactionDecisionCommand,
) error {
	if tx == nil {
		return ErrInteractionDecisionInvalid
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO interaction_decision_commands(
		 workspace_id,confirmation_id,principal_binding_hash,idempotency_key,
		 request_hash,decision,expected_version,confirmation_status,
		 confirmation_version,created_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, value.WorkspaceID, value.ConfirmationID, value.PrincipalBindingHash,
		value.IdempotencyKey, value.RequestHash, value.Decision,
		value.ExpectedVersion, value.ConfirmationStatus,
		value.ConfirmationVersion, value.CreatedAt.UTC())
	if err == nil {
		return nil
	}
	var databaseError *pq.Error
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return ErrInteractionIdempotencyConflict
	}
	return fmt.Errorf("record interaction decision command: %w", err)
}
