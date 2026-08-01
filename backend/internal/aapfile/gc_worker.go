package aapfile

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"actweave/backend/internal/logging"
	"actweave/backend/internal/metrics"
)

// StagingGCConfig bounds the staging orphan cleanup loop (IC-11 / §5.4.3).
type StagingGCConfig struct {
	Interval           time.Duration
	BatchLimit         int
	MaxPromoteAttempts int
	Metrics            *metrics.AAPFileCollector
	Logger             *slog.Logger
}

// DefaultStagingGCConfig is the production baseline (idle-safe when empty).
func DefaultStagingGCConfig() StagingGCConfig {
	return StagingGCConfig{
		Interval:           DefaultStagingGCInterval,
		BatchLimit:         DefaultStagingGCBatch,
		MaxPromoteAttempts: DefaultMaxPromoteAttempts,
	}
}

// StagingGCWorker deletes residual staging blobs and marks DB rows cleared.
// Safe to Start when files.enabled=false (loop idles with no candidates).
type StagingGCWorker struct {
	repo    *Repository
	staging StagingStore
	config  StagingGCConfig
	metrics *metrics.AAPFileCollector
	logger  *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewStagingGCWorker constructs a staging GC worker.
func NewStagingGCWorker(
	repo *Repository,
	staging StagingStore,
	config StagingGCConfig,
) (*StagingGCWorker, error) {
	if repo == nil || staging == nil {
		return nil, errors.New("aapfile staging gc repository and staging store are required")
	}
	if config.Interval <= 0 {
		config.Interval = DefaultStagingGCInterval
	}
	if config.BatchLimit <= 0 {
		config.BatchLimit = DefaultStagingGCBatch
	}
	if config.MaxPromoteAttempts <= 0 {
		config.MaxPromoteAttempts = DefaultMaxPromoteAttempts
	}
	if config.Interval < 5*time.Second || config.Interval > time.Hour {
		return nil, errors.New("aapfile staging gc interval out of range")
	}
	if config.BatchLimit > 1000 {
		return nil, errors.New("aapfile staging gc batch limit out of range")
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	collector := config.Metrics
	if collector == nil {
		collector = metrics.DefaultAAPFile()
	}
	return &StagingGCWorker{
		repo: repo, staging: staging, config: config,
		metrics: collector, logger: logger,
	}, nil
}

// Start begins the background GC loop.
func (w *StagingGCWorker) Start(parent context.Context) {
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
func (w *StagingGCWorker) Stop() {
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

func (w *StagingGCWorker) loop(ctx context.Context) {
	defer close(w.done)
	// One pass on start for recovery visibility.
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

// StagingGCResult is a single RunOnce summary (ops / tests).
type StagingGCResult struct {
	Candidates int
	Cleared    int
	Errors     int
}

// RunOnce selects §5.4.3 candidates, deletes staging (missing=ok), and marks
// staging_deleted_at / clears staging_object_key. Expired PENDING → EXPIRED.
func (w *StagingGCWorker) RunOnce(ctx context.Context) (StagingGCResult, error) {
	if w == nil || w.repo == nil || w.staging == nil {
		return StagingGCResult{}, ErrInvalid
	}
	w.refreshGauges(ctx)

	candidates, err := w.repo.ListStagingGCCandidates(
		ctx, w.config.BatchLimit, w.config.MaxPromoteAttempts,
	)
	if err != nil {
		return StagingGCResult{}, err
	}
	result := StagingGCResult{Candidates: len(candidates)}
	for _, file := range candidates {
		if err := w.clearOne(ctx, file); err != nil {
			result.Errors++
			logging.AAPWarn(w.logger, "aap file staging gc failed",
				"event", "aap.file.staging_gc.failed",
				"workspace_id", file.WorkspaceID,
				"file_id", file.ID,
				"file_status", file.Status,
				"error_code", "STAGING_GC_FAILED",
				"reason", truncateReason(err.Error()),
			)
			continue
		}
		result.Cleared++
		logging.AAPInfo(w.logger, "aap file staging gc cleared",
			"event", "aap.file.staging_gc.cleared",
			"workspace_id", file.WorkspaceID,
			"file_id", file.ID,
			"file_status", file.Status,
			"component", "staging_gc",
		)
	}
	w.refreshGauges(ctx)
	return result, nil
}

func (w *StagingGCWorker) clearOne(ctx context.Context, file File) error {
	if file.StagingObjectKey == nil || strings.TrimSpace(*file.StagingObjectKey) == "" {
		// Nothing to delete; still mark cleared if markers inconsistent.
		_, err := w.repo.MarkStagingCleared(ctx, file.WorkspaceID, file.ID, file.ProcessingVersion, false)
		return err
	}
	key := strings.TrimSpace(*file.StagingObjectKey)
	bucket := strings.TrimSpace(file.StagingBucket)
	// missing object = ok (design §5.4.3).
	if err := w.staging.Delete(ctx, bucket, key); err != nil && !isStagingAbsent(err) {
		return err
	}
	expirePending := file.Status == StatusPendingUpload
	_, err := w.repo.MarkStagingCleared(
		ctx, file.WorkspaceID, file.ID, file.ProcessingVersion, expirePending,
	)
	return err
}

func (w *StagingGCWorker) refreshGauges(ctx context.Context) {
	if pending, err := w.repo.CountPendingUploadsGlobal(ctx); err == nil {
		w.metrics.SetPendingUploadGauge(pending)
	}
	if orphan, err := w.repo.SumStagingOrphanBytes(ctx); err == nil {
		w.metrics.SetStagingOrphanBytes(orphan)
	}
}

func isStagingAbsent(err error) bool {
	if err == nil {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "not found") ||
		strings.Contains(message, "nosuchkey") ||
		strings.Contains(message, "no such key") ||
		strings.Contains(message, "404")
}

func truncateReason(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 80 {
		return value[:80]
	}
	return value
}
