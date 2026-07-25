package einoruntime

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"actweave/backend/internal/metrics"
)

// CheckpointCleanupConfig bounds the expired-checkpoint GC loop.
// Interval defaults align with recovery workers (not a business TTL knob).
type CheckpointCleanupConfig struct {
	// Interval between cleanup passes. Must be positive.
	Interval time.Duration
	// BatchLimit caps rows deleted per pass (1..1000).
	BatchLimit int
}

// DefaultCheckpointCleanupConfig is a conservative single-process baseline.
// Interval is operational only — business expiry is still confirmation clock (D15).
func DefaultCheckpointCleanupConfig() CheckpointCleanupConfig {
	return CheckpointCleanupConfig{
		Interval:   30 * time.Second,
		BatchLimit: DefaultCheckpointCleanupBatchLimit,
	}
}

// CheckpointCleanupWorker periodically deletes eino_checkpoints rows whose
// expires_at has passed. Safe for multi-replica (DELETE is idempotent).
//
// Construction is always safe; wiring into application.go is optional until
// the Eino production path needs it. Tests use RunOnce without Start.
type CheckpointCleanupWorker struct {
	store     *PostgresCheckPointStore
	collector *metrics.AAPCollector
	config    CheckpointCleanupConfig
	logger    *slog.Logger
	now       func() time.Time

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewCheckpointCleanupWorker builds a GC worker. store is required.
// collector and logger may be nil (metrics/logs skipped or defaulted).
func NewCheckpointCleanupWorker(
	store *PostgresCheckPointStore,
	config CheckpointCleanupConfig,
	collector *metrics.AAPCollector,
	logger *slog.Logger,
) (*CheckpointCleanupWorker, error) {
	if store == nil {
		return nil, errors.New("checkpoint cleanup store is required")
	}
	if config.Interval <= 0 || config.BatchLimit < 1 || config.BatchLimit > 1000 {
		return nil, errors.New("checkpoint cleanup config is invalid")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if collector == nil {
		collector = metrics.Default()
	}
	return &CheckpointCleanupWorker{
		store:     store,
		collector: collector,
		config:    config,
		logger:    logger,
		now:       func() time.Time { return time.Now().UTC() },
	}, nil
}

// Start launches the background loop. Idempotent; a second call is a no-op.
func (w *CheckpointCleanupWorker) Start(parent context.Context) {
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

// Stop cancels the loop and waits for exit.
func (w *CheckpointCleanupWorker) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	cancel := w.cancel
	done := w.done
	w.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
	w.mu.Lock()
	w.cancel = nil
	w.done = nil
	w.mu.Unlock()
}

func (w *CheckpointCleanupWorker) loop(ctx context.Context) {
	defer close(w.done)
	// Run once promptly on boot, then on interval.
	w.runOnce(ctx)
	ticker := time.NewTicker(w.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

// RunOnce exposes a single cleanup pass for tests and manual ops.
func (w *CheckpointCleanupWorker) RunOnce(ctx context.Context) {
	if w == nil {
		return
	}
	w.runOnce(ctx)
}

func (w *CheckpointCleanupWorker) runOnce(ctx context.Context) {
	if w == nil || ctx == nil || ctx.Err() != nil {
		return
	}
	now := w.now()
	deleted, err := w.store.DeleteExpired(ctx, now, w.config.BatchLimit)
	if err != nil {
		w.logger.Warn("eino checkpoint cleanup failed",
			"component", "einoruntime.checkpoint_cleanup",
			"error", err.Error(),
		)
		if w.collector != nil {
			w.collector.ObserveEinoCheckpointCleanup(0, false)
		}
		return
	}
	if w.collector != nil {
		w.collector.ObserveEinoCheckpointCleanup(deleted, true)
	}
	if deleted > 0 {
		w.logger.Info("eino checkpoint cleanup deleted expired rows",
			"component", "einoruntime.checkpoint_cleanup",
			"deleted", deleted,
		)
	}
}
