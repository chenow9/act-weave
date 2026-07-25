package einoruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/adk"
)

// DefaultCheckpointTTL is the provisional TTL used by Set when the caller
// does not supply an absolute expiry via SetWithExpiresAt.
//
// D15 (shared confirmation clock): this duration MUST equal
// execution.DefaultConfirmationTTLSeconds (600s). Mid-crash checkpoints
// without a confirmation row use the same default as confirmation policy.
// Callers that pause for HITL MUST pass confirmation.ExpiresAt via
// SetWithExpiresAt / TouchExpiresAt instead of relying on this default.
// There is intentionally no separate checkpointTTLHours config knob.
const DefaultCheckpointTTL = 600 * time.Second

// DefaultCheckpointCleanupBatchLimit bounds a single cleanup DELETE pass.
const DefaultCheckpointCleanupBatchLimit = 500

// PostgresCheckPointStore persists Eino gob checkpoint blobs in PostgreSQL
// with multi-tenant ID prefix validation.
//
// It implements adk.CheckPointStore and adk.CheckPointDeleter (both aliases
// of internal/core interfaces).
type PostgresCheckPointStore struct {
	db         *sql.DB
	defaultTTL time.Duration
	now        func() time.Time
}

// Ensure compile-time interface satisfaction.
var (
	_ adk.CheckPointStore   = (*PostgresCheckPointStore)(nil)
	_ adk.CheckPointDeleter = (*PostgresCheckPointStore)(nil)
)

// NewPostgresCheckPointStore builds a store using DefaultCheckpointTTL.
func NewPostgresCheckPointStore(db *sql.DB) (*PostgresCheckPointStore, error) {
	return NewPostgresCheckPointStoreWithTTL(db, DefaultCheckpointTTL)
}

// NewPostgresCheckPointStoreWithTTL builds a store with a custom default TTL
// for the CheckPointStore.Set interface method. ttl must be positive.
// Prefer the DefaultCheckpointTTL constructor in production so the default
// stays aligned with confirmation policy (D15).
func NewPostgresCheckPointStoreWithTTL(db *sql.DB, ttl time.Duration) (*PostgresCheckPointStore, error) {
	if db == nil {
		return nil, errors.New("einoruntime checkpoint store database is required")
	}
	if ttl <= 0 {
		return nil, errors.New("einoruntime checkpoint store default TTL must be positive")
	}
	return &PostgresCheckPointStore{
		db:         db,
		defaultTTL: ttl,
		now:        func() time.Time { return time.Now().UTC() },
	}, nil
}

// Get loads a checkpoint by full multi-tenant ID.
// Returns (nil, false, nil) when the row does not exist.
// Returns an error when the ID fails prefix validation.
func (s *PostgresCheckPointStore) Get(ctx context.Context, checkPointID string) ([]byte, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, errors.New("einoruntime checkpoint store is not initialized")
	}
	parsed, err := ParseCheckpointID(checkPointID)
	if err != nil {
		return nil, false, err
	}
	if err := matchTrustedWorkspace(ctx, parsed.WorkspaceID); err != nil {
		return nil, false, err
	}

	var payload []byte
	err = s.db.QueryRowContext(ctx, `
		SELECT payload
		  FROM eino_checkpoints
		 WHERE checkpoint_id = $1
		   AND workspace_id = $2
	`, parsed.Raw, parsed.WorkspaceID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get eino checkpoint %q: %w", parsed.Raw, err)
	}
	return payload, true, nil
}

// Set upserts a checkpoint using the store's default TTL for expires_at.
// Prefer SetWithExpiresAt when confirmation expiry is known (D15 / HITL pause).
func (s *PostgresCheckPointStore) Set(ctx context.Context, checkPointID string, checkPoint []byte) error {
	if s == nil {
		return errors.New("einoruntime checkpoint store is not initialized")
	}
	expiresAt := s.now().Add(s.defaultTTL)
	return s.SetWithExpiresAt(ctx, checkPointID, checkPoint, expiresAt)
}

// SetWithExpiresAt upserts a checkpoint with an explicit absolute expiry.
//
// D15: callers writing a HITL pause checkpoint MUST pass the same absolute
// instant as the corresponding confirmation/interaction expiresAt (or the
// confirmation-policy ExpiresIn applied to the same "now" used for that row).
// Do not invent a separate checkpoint TTL.
func (s *PostgresCheckPointStore) SetWithExpiresAt(
	ctx context.Context,
	checkPointID string,
	checkPoint []byte,
	expiresAt time.Time,
) error {
	if s == nil || s.db == nil {
		return errors.New("einoruntime checkpoint store is not initialized")
	}
	if checkPoint == nil {
		return errors.New("eino checkpoint payload is required")
	}
	if expiresAt.IsZero() {
		return errors.New("eino checkpoint expires_at is required")
	}
	parsed, err := ParseCheckpointID(checkPointID)
	if err != nil {
		return err
	}
	if err := matchTrustedWorkspace(ctx, parsed.WorkspaceID); err != nil {
		return err
	}

	now := s.now()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO eino_checkpoints (
			checkpoint_id, workspace_id, kind, owner_id, payload,
			created_at, updated_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $6, $7)
		ON CONFLICT (checkpoint_id) DO UPDATE SET
			workspace_id = EXCLUDED.workspace_id,
			kind         = EXCLUDED.kind,
			owner_id     = EXCLUDED.owner_id,
			payload      = EXCLUDED.payload,
			updated_at   = EXCLUDED.updated_at,
			expires_at   = EXCLUDED.expires_at
		WHERE eino_checkpoints.workspace_id = EXCLUDED.workspace_id
	`, parsed.Raw, parsed.WorkspaceID, parsed.Kind, parsed.OwnerID,
		checkPoint, now, expiresAt.UTC())
	if err != nil {
		return fmt.Errorf("set eino checkpoint %q: %w", parsed.Raw, err)
	}
	return nil
}

// TouchExpiresAt renews only expires_at (and updated_at) of an existing
// checkpoint without changing the payload.
//
// D15: when confirmation policy extends expiry, pass the new confirmation
// expiresAt so checkpoint and confirmation stay on the same business clock.
// Returns sql.ErrNoRows (wrapped) when the checkpoint does not exist.
func (s *PostgresCheckPointStore) TouchExpiresAt(
	ctx context.Context,
	checkPointID string,
	expiresAt time.Time,
) error {
	if s == nil || s.db == nil {
		return errors.New("einoruntime checkpoint store is not initialized")
	}
	if expiresAt.IsZero() {
		return errors.New("eino checkpoint expires_at is required")
	}
	parsed, err := ParseCheckpointID(checkPointID)
	if err != nil {
		return err
	}
	if err := matchTrustedWorkspace(ctx, parsed.WorkspaceID); err != nil {
		return err
	}

	now := s.now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE eino_checkpoints
		   SET expires_at = $3,
		       updated_at = $4
		 WHERE checkpoint_id = $1
		   AND workspace_id = $2
	`, parsed.Raw, parsed.WorkspaceID, expiresAt.UTC(), now)
	if err != nil {
		return fmt.Errorf("touch eino checkpoint expires_at %q: %w", parsed.Raw, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("touch eino checkpoint expires_at %q: rows affected: %w", parsed.Raw, err)
	}
	if affected == 0 {
		return fmt.Errorf("touch eino checkpoint expires_at %q: %w", parsed.Raw, sql.ErrNoRows)
	}
	return nil
}

// Delete removes a checkpoint by full multi-tenant ID (CheckPointDeleter).
// Missing rows are not an error.
func (s *PostgresCheckPointStore) Delete(ctx context.Context, checkPointID string) error {
	if s == nil || s.db == nil {
		return errors.New("einoruntime checkpoint store is not initialized")
	}
	parsed, err := ParseCheckpointID(checkPointID)
	if err != nil {
		return err
	}
	if err := matchTrustedWorkspace(ctx, parsed.WorkspaceID); err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
		DELETE FROM eino_checkpoints
		 WHERE checkpoint_id = $1
		   AND workspace_id = $2
	`, parsed.Raw, parsed.WorkspaceID)
	if err != nil {
		return fmt.Errorf("delete eino checkpoint %q: %w", parsed.Raw, err)
	}
	return nil
}

// DeleteExpired removes rows with expires_at <= now (same boundary as
// execution confirmation ExpireDue: expired confirmation cannot resume).
// limit caps the batch size (1..1000). Returns the number of deleted rows.
func (s *PostgresCheckPointStore) DeleteExpired(ctx context.Context, now time.Time, limit int) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("einoruntime checkpoint store is not initialized")
	}
	if now.IsZero() {
		return 0, errors.New("eino checkpoint cleanup now is required")
	}
	if limit < 1 || limit > 1000 {
		return 0, errors.New("eino checkpoint cleanup limit must be between 1 and 1000")
	}

	result, err := s.db.ExecContext(ctx, `
		WITH due AS (
			SELECT checkpoint_id
			  FROM eino_checkpoints
			 WHERE expires_at <= $1
			 ORDER BY expires_at, checkpoint_id
			 LIMIT $2
		)
		DELETE FROM eino_checkpoints AS c
		 USING due
		 WHERE c.checkpoint_id = due.checkpoint_id
	`, now.UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired eino checkpoints: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete expired eino checkpoints: rows affected: %w", err)
	}
	return affected, nil
}

// DefaultExpiresAt returns now + DefaultCheckpointTTL for the rare mid-crash
// path where no confirmation row exists yet (D15 fallback).
func DefaultExpiresAt(now time.Time) time.Time {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return now.UTC().Add(DefaultCheckpointTTL)
}
