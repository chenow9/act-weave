package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"actweave/backend/internal/metrics"
	"actweave/backend/internal/storedobject"
	"github.com/google/uuid"
)

// PreviewPurgeConfig bounds the multi-replica safe purge loop.
type PreviewPurgeConfig struct {
	Interval   time.Duration
	BatchLimit int
	ClaimLease time.Duration
	Metrics    *metrics.PromptCollector
}

// DefaultPreviewPurgeConfig is the production baseline (TD2-A).
func DefaultPreviewPurgeConfig() PreviewPurgeConfig {
	return PreviewPurgeConfig{
		Interval:   300 * time.Second,
		BatchLimit: 100,
		ClaimLease: 120 * time.Second,
	}
}

// PreviewBodyPurger deletes ciphertext for claimed preview objects.
type PreviewBodyPurger interface {
	PurgeBody(ctx context.Context, workspaceID, objectID string) error
}

// PreviewPurgeWorker claims expired EXPIRING preview objects and deletes bodies.
type PreviewPurgeWorker struct {
	db      *sql.DB
	purger  PreviewBodyPurger
	config  PreviewPurgeConfig
	logger  *slog.Logger
	metrics *metrics.PromptCollector

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewPreviewPurgeWorker(
	db *sql.DB,
	purger PreviewBodyPurger,
	config PreviewPurgeConfig,
	logger *slog.Logger,
) (*PreviewPurgeWorker, error) {
	if db == nil || purger == nil {
		return nil, errors.New("preview purge worker database and purger are required")
	}
	if config.Interval < 10*time.Second || config.Interval > time.Hour {
		return nil, errors.New("preview purge interval must be between 10s and 1h")
	}
	if config.BatchLimit < 1 || config.BatchLimit > 1000 {
		return nil, errors.New("preview purge batch limit must be 1..1000")
	}
	if config.ClaimLease < 10*time.Second {
		return nil, errors.New("preview purge claim lease must be at least 10s")
	}
	if logger == nil {
		logger = slog.Default()
	}
	collector := config.Metrics
	if collector == nil {
		collector = metrics.DefaultPrompt()
	}
	return &PreviewPurgeWorker{
		db: db, purger: purger, config: config, logger: logger, metrics: collector,
	}, nil
}

func (w *PreviewPurgeWorker) Start(parent context.Context) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	w.cancel = cancel
	w.done = make(chan struct{})
	go w.loop(ctx)
}

func (w *PreviewPurgeWorker) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	cancel := w.cancel
	done := w.done
	w.cancel = nil
	w.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

func (w *PreviewPurgeWorker) loop(ctx context.Context) {
	defer close(w.done)
	// Run once on start for recovery/backlog visibility.
	_, _ = w.RunOnce(ctx)
	ticker := time.NewTicker(w.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = w.RunOnce(ctx)
		}
	}
}

// RunOnce claims up to batchLimit overdue preview objects and purges them.
func (w *PreviewPurgeWorker) RunOnce(ctx context.Context) (int, error) {
	if w == nil {
		return 0, errors.New("preview purge worker is nil")
	}
	w.refreshBacklog(ctx)
	claims, err := w.claimBatch(ctx)
	if err != nil {
		return 0, err
	}
	purged := 0
	for _, claim := range claims {
		if err := w.purger.PurgeBody(ctx, claim.WorkspaceID, claim.ObjectID); err != nil {
			w.metrics.IncPurge("error", "FAILED")
			_ = w.markClaimFailure(ctx, claim, "PURGE_FAILED")
			w.logger.Warn("preview body purge failed",
				"workspace_id", claim.WorkspaceID, "object_id", claim.ObjectID,
				"error_code", "PURGE_FAILED")
			continue
		}
		w.metrics.IncPurge("success", "OK")
		_ = w.maybeMarkRunContentPurged(ctx, claim.WorkspaceID, claim.ObjectID)
		purged++
	}
	return purged, nil
}

type purgeClaim struct {
	WorkspaceID string
	ObjectID    string
	Token       string
}

func (w *PreviewPurgeWorker) claimBatch(ctx context.Context) ([]purgeClaim, error) {
	token := uuid.Must(uuid.NewV7()).String()
	leaseSeconds := int(w.config.ClaimLease / time.Second)
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, workspace_id FROM stored_objects
		WHERE kind IN ('PROMPT_PREVIEW_INPUT','PROMPT_PREVIEW_OUTPUT')
		  AND retention_mode='EXPIRING'
		  AND body_purged_at IS NULL
		  AND retention_until IS NOT NULL
		  AND retention_until <= clock_timestamp()
		  AND (purge_claim_token IS NULL OR purge_claim_expires_at <= clock_timestamp())
		  AND (purge_next_attempt_at IS NULL OR purge_next_attempt_at <= clock_timestamp())
		ORDER BY purge_next_attempt_at NULLS FIRST, retention_until, id
		FOR UPDATE SKIP LOCKED
		LIMIT $1
	`, w.config.BatchLimit)
	if err != nil {
		return nil, fmt.Errorf("claim preview purge candidates: %w", err)
	}
	defer rows.Close()

	type candidate struct{ id, workspaceID string }
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.workspaceID); err != nil {
			return nil, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	claims := make([]purgeClaim, 0, len(candidates))
	for _, item := range candidates {
		claimToken := uuid.Must(uuid.NewV7()).String()
		if _, err := tx.ExecContext(ctx, `
			UPDATE stored_objects
			SET purge_claim_token=$3::uuid,
				purge_claim_expires_at=clock_timestamp() + ($4::text || ' seconds')::interval,
				purge_attempts=purge_attempts+1
			WHERE workspace_id=$1 AND id=$2
			  AND body_purged_at IS NULL
			  AND retention_mode='EXPIRING'
		`, item.workspaceID, item.id, claimToken, leaseSeconds); err != nil {
			return nil, fmt.Errorf("set purge claim: %w", err)
		}
		claims = append(claims, purgeClaim{
			WorkspaceID: item.workspaceID, ObjectID: item.id, Token: claimToken,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	_ = token
	return claims, nil
}

func (w *PreviewPurgeWorker) markClaimFailure(ctx context.Context, claim purgeClaim, code string) error {
	// Exponential-ish backoff starting at 5 minutes, capped at 6 hours.
	_, err := w.db.ExecContext(ctx, `
		UPDATE stored_objects
		SET purge_claim_token=NULL,
			purge_claim_expires_at=NULL,
			purge_last_error_code=$3,
			purge_next_attempt_at=clock_timestamp() + LEAST(
				interval '5 minutes' * power(2, GREATEST(purge_attempts-1, 0)),
				interval '6 hours'
			)
		WHERE workspace_id=$1 AND id=$2
		  AND body_purged_at IS NULL
	`, claim.WorkspaceID, claim.ObjectID, strings.ToUpper(strings.TrimSpace(code)))
	return err
}

func (w *PreviewPurgeWorker) maybeMarkRunContentPurged(ctx context.Context, workspaceID, objectID string) error {
	_, err := w.db.ExecContext(ctx, `
		UPDATE prompt_runs pr
		SET content_purged_at=clock_timestamp()
		WHERE pr.workspace_id=$1
		  AND pr.operation_type='CREATE_PREVIEW'
		  AND pr.promoted_at IS NULL
		  AND pr.content_purged_at IS NULL
		  AND (pr.input_object_id=$2 OR pr.output_object_id=$2)
		  AND EXISTS (
			SELECT 1 FROM stored_objects si
			WHERE si.workspace_id=pr.workspace_id AND si.id=pr.input_object_id
			  AND si.body_purged_at IS NOT NULL
		  )
		  AND (
			pr.output_object_id IS NULL
			OR EXISTS (
				SELECT 1 FROM stored_objects so
				WHERE so.workspace_id=pr.workspace_id AND so.id=pr.output_object_id
				  AND so.body_purged_at IS NOT NULL
			)
		  )
	`, workspaceID, objectID)
	return err
}

func (w *PreviewPurgeWorker) refreshBacklog(ctx context.Context) {
	var backlog int64
	var oldest sql.NullFloat64
	err := w.db.QueryRowContext(ctx, `
		SELECT count(*),
			EXTRACT(EPOCH FROM (clock_timestamp() - min(retention_until)))
		FROM stored_objects
		WHERE kind IN ('PROMPT_PREVIEW_INPUT','PROMPT_PREVIEW_OUTPUT')
		  AND retention_mode='EXPIRING'
		  AND body_purged_at IS NULL
		  AND retention_until IS NOT NULL
		  AND retention_until <= clock_timestamp()
	`).Scan(&backlog, &oldest)
	if err != nil {
		return
	}
	w.metrics.SetPurgeBacklog(backlog)
	if oldest.Valid && oldest.Float64 > 0 {
		w.metrics.SetPurgeOldestOverdueSeconds(int64(oldest.Float64))
	} else {
		w.metrics.SetPurgeOldestOverdueSeconds(0)
	}
}

// Ensure storedobject.Repository satisfies nothing here; ObjectStore.PurgeBody is the purger.
var _ PreviewBodyPurger = (*storedobject.ObjectStore)(nil)
