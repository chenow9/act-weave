package execution

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"actweave/backend/internal/logging"
	"actweave/backend/internal/metrics"
)

// RuntimeContinuation re-drives chat/workflow continue after SUCCEEDED tool
// dispatch when the in-process EnqueueContinue path was lost.
type RuntimeContinuation interface {
	ContinueApprovedInteraction(context.Context, InteractionDecisionResult) error
}

// RecoveryWorkerConfig bounds the production recovery loop.
type RecoveryWorkerConfig struct {
	Interval   time.Duration
	BatchLimit int
	// LifecycleRepairAge skips brand-new runs still in the dual-tx window.
	LifecycleRepairAge time.Duration
}

// DefaultRecoveryWorkerConfig is the single-process production baseline.
func DefaultRecoveryWorkerConfig() RecoveryWorkerConfig {
	return RecoveryWorkerConfig{
		Interval:           15 * time.Second,
		BatchLimit:         50,
		LifecycleRepairAge: 30 * time.Second,
	}
}

// AffinityStaleReconciler is the optional T2=A stale-affinity fail-closed loop.
// Implementations must not take Tool side-effect claims.
type AffinityStaleReconciler interface {
	ReconcileOnce(ctx context.Context, limit int) (int, error)
}

// RecoveryWorker periodically re-drives approved continuation dispatch and
// dual-transaction protocol lifecycle gaps. Safe to run multi-replica: claim
// CAS and deterministic event IDs prevent duplicate side effects.
//
// When affinityReconciler is set, each pass also converges owner-lost
// REQUEST_PASSTHROUGH roots to fail-closed without reclaiming side effects.
type RecoveryWorker struct {
	recovery           *ContinuationRecoveryService
	repair             *ProtocolLifecycleRepair
	continuation       RuntimeContinuation
	affinityReconciler AffinityStaleReconciler
	collector          *metrics.AAPCollector
	config             RecoveryWorkerConfig
	logger             *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewRecoveryWorker builds a worker. continuation may be nil (dispatch-only).
func NewRecoveryWorker(
	recovery *ContinuationRecoveryService,
	repair *ProtocolLifecycleRepair,
	continuation RuntimeContinuation,
	config RecoveryWorkerConfig,
	logger *slog.Logger,
) (*RecoveryWorker, error) {
	if recovery == nil || repair == nil {
		return nil, errors.New("recovery worker dependencies are required")
	}
	if config.Interval <= 0 || config.BatchLimit < 1 || config.BatchLimit > 1000 {
		return nil, errors.New("recovery worker config is invalid")
	}
	if config.LifecycleRepairAge < 0 {
		return nil, errors.New("recovery worker lifecycle age is invalid")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &RecoveryWorker{
		recovery: recovery, repair: repair, continuation: continuation,
		collector: metrics.Default(), config: config, logger: logger,
	}, nil
}

// WithAffinityReconciler attaches the outbound stale-affinity fail-closed loop.
func (worker *RecoveryWorker) WithAffinityReconciler(r AffinityStaleReconciler) *RecoveryWorker {
	if worker != nil {
		worker.affinityReconciler = r
	}
	return worker
}

// Start launches the background loop. Idempotent; second call is a no-op.
func (worker *RecoveryWorker) Start(parent context.Context) {
	if worker == nil {
		return
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	worker.cancel = cancel
	worker.done = make(chan struct{})
	go worker.loop(ctx)
}

// Stop cancels the loop and waits for exit.
func (worker *RecoveryWorker) Stop() {
	if worker == nil {
		return
	}
	worker.mu.Lock()
	cancel := worker.cancel
	done := worker.done
	worker.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
	worker.mu.Lock()
	worker.cancel = nil
	worker.done = nil
	worker.mu.Unlock()
}

func (worker *RecoveryWorker) loop(ctx context.Context) {
	defer close(worker.done)
	// Run once promptly on boot, then on interval.
	worker.runOnce(ctx)
	ticker := time.NewTicker(worker.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			worker.runOnce(ctx)
		}
	}
}

// RunOnce exposes a single recovery pass for tests.
func (worker *RecoveryWorker) RunOnce(ctx context.Context) {
	if worker == nil {
		return
	}
	worker.runOnce(ctx)
}

func (worker *RecoveryWorker) runOnce(ctx context.Context) {
	if ctx == nil || ctx.Err() != nil {
		return
	}
	// Owner-loss fail-close first so we never reclaim passthrough side effects.
	worker.reconcileStaleAffinities(ctx)
	worker.recoverContinuations(ctx)
	worker.repairLifecycleGaps(ctx)
	worker.observeWaitingApprovals(ctx)
}

func (worker *RecoveryWorker) reconcileStaleAffinities(ctx context.Context) {
	if worker.affinityReconciler == nil {
		return
	}
	if _, err := worker.affinityReconciler.ReconcileOnce(ctx, worker.config.BatchLimit); err != nil {
		logging.AAPWarn(worker.logger, "outbound affinity reconcile failed",
			"event", "outbound.affinity.reconcile_failed",
			"error_code", "OUTBOUND_CREDENTIAL_EXPIRED")
	}
}

func (worker *RecoveryWorker) recoverContinuations(ctx context.Context) {
	results, err := worker.recovery.RecoverPendingDispatches(ctx, worker.config.BatchLimit)
	if err != nil {
		// Allowlisted fields only — never attach raw err.Error().
		logging.AAPWarn(worker.logger, "aap recovery dispatch list failed",
			"event", "aap.recovery.dispatch_list_failed", "error_code", "DISPATCH_LIST_FAILED")
		return
	}
	for _, result := range results {
		if result.DispatchError != nil {
			logging.AAPWarn(worker.logger, "aap recovery dispatch item failed",
				"event", "aap.recovery.dispatch_failed",
				"workspace_id", result.Confirmation.WorkspaceID,
				"confirmation_id", result.Confirmation.ID,
				"error_code", "DISPATCH_FAILED")
		}
	}
	if worker.continuation == nil {
		return
	}
	awaiting, err := worker.recovery.ListSucceededAwaitingRuntimeContinue(ctx, worker.config.BatchLimit)
	if err != nil {
		logging.AAPWarn(worker.logger, "aap recovery runtime-continue list failed",
			"event", "aap.recovery.runtime_continue_list_failed", "error_code", "RUNTIME_CONTINUE_LIST_FAILED")
		return
	}
	for _, item := range awaiting {
		// ContinueApprovedInteraction acquires the shared multi-replica lease
		// (same path as normal approval) — do not claim here or the second
		// claim loses and recovery would skip the continue drive.
		decision := DecisionResultFromRecovery(item)
		if contErr := worker.continuation.ContinueApprovedInteraction(ctx, decision); contErr != nil {
			if errors.Is(contErr, ErrRuntimeContinueNotClaimed) {
				// Another replica owns the lease — expected under multi-replica.
				continue
			}
			logging.AAPWarn(worker.logger, "aap recovery runtime continue failed",
				"event", "aap.recovery.runtime_continue_failed",
				"workspace_id", item.WorkspaceID,
				"confirmation_id", item.ConfirmationID,
				"run_id", item.RunID,
				"error_code", "RUNTIME_CONTINUE_FAILED")
		}
	}
}

func (worker *RecoveryWorker) repairLifecycleGaps(ctx context.Context) {
	candidates, err := worker.repair.ListRunsMissingStartedEvents(
		ctx, worker.config.BatchLimit, worker.config.LifecycleRepairAge,
	)
	if err != nil {
		logging.AAPWarn(worker.logger, "aap recovery lifecycle list failed",
			"event", "aap.recovery.lifecycle_list_failed", "error_code", "LIFECYCLE_LIST_FAILED")
		return
	}
	for _, candidate := range candidates {
		if _, repairErr := worker.repair.EnsureStartedEvents(
			ctx, candidate.WorkspaceID, candidate.RunID,
		); repairErr != nil {
			logging.AAPWarn(worker.logger, "aap recovery lifecycle repair failed",
				"event", "aap.recovery.lifecycle_repair_failed",
				"workspace_id", candidate.WorkspaceID,
				"run_id", candidate.RunID,
				"error_code", "LIFECYCLE_REPAIR_FAILED")
		}
	}
}

func (worker *RecoveryWorker) observeWaitingApprovals(ctx context.Context) {
	if worker.collector == nil || worker.recovery == nil || worker.recovery.db == nil {
		return
	}
	var count int64
	var maxAgeSeconds float64
	// Age = NOW() - MIN(created_at) for PENDING approvals (not "AT MIN" — invalid SQL).
	err := worker.recovery.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(EXTRACT(EPOCH FROM (NOW() - MIN(ec.created_at))), 0)
		FROM execution_confirmations ec
		WHERE ec.status = 'PENDING'
	`).Scan(&count, &maxAgeSeconds)
	if err != nil {
		return
	}
	worker.collector.SetRunWaitingInteraction(count)
	if maxAgeSeconds > 0 {
		worker.collector.ObserveWaitingApprovalAge(time.Duration(maxAgeSeconds * float64(time.Second)))
	}
	// Credential last-used age for ACTIVE secrets (ops alert: stale / unused).
	var credAgeSeconds float64
	credErr := worker.recovery.db.QueryRowContext(ctx, `
		SELECT COALESCE(EXTRACT(EPOCH FROM (NOW() - MIN(last_used_at))), 0)
		FROM agent_access_credentials
		WHERE revoked_at IS NULL
		  AND last_used_at IS NOT NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
	`).Scan(&credAgeSeconds)
	if credErr == nil && credAgeSeconds > 0 {
		worker.collector.ObserveCredentialLastUsedAge(
			time.Duration(credAgeSeconds * float64(time.Second)),
		)
	}
}

// RecoverableRunRef identifies a domain run that may need protocol repair.
type RecoverableRunRef struct {
	WorkspaceID string
	RunID       string
}

// ListRunsMissingStartedEvents finds RUNNING/WAITING_CONFIRMATION runs older
// than minAge whose protocol stream is missing or has fewer than two events
// (the dual-transaction crash window between domain commit and RecordStarted).
// Age is based on agent_runs.started_at (the table has no created_at column).
func (repair *ProtocolLifecycleRepair) ListRunsMissingStartedEvents(
	ctx context.Context,
	limit int,
	minAge time.Duration,
) ([]RecoverableRunRef, error) {
	if repair == nil || repair.runs == nil || repair.runs.db == nil || ctx == nil ||
		limit < 1 || limit > 1000 || minAge < 0 {
		return nil, ErrRunInvalid
	}
	cutoff := time.Now().UTC().Add(-minAge)
	rows, err := repair.runs.db.QueryContext(ctx, `
		SELECT ar.workspace_id, ar.id
		FROM agent_runs ar
		WHERE ar.status IN ('RUNNING', 'WAITING_CONFIRMATION')
		  AND ar.started_at <= $1
		  AND (
		    NOT EXISTS (
		      SELECT 1 FROM protocol_event_streams pes
		      WHERE pes.workspace_id = ar.workspace_id AND pes.run_id = ar.id
		    )
		    OR (
		      SELECT COUNT(*) FROM protocol_events pe
		      WHERE pe.workspace_id = ar.workspace_id AND pe.run_id = ar.id
		    ) < 2
		  )
		ORDER BY ar.started_at, ar.id
		LIMIT $2
	`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("list runs missing protocol start: %w", err)
	}
	defer rows.Close()
	out := make([]RecoverableRunRef, 0)
	for rows.Next() {
		var ref RecoverableRunRef
		if scanErr := rows.Scan(&ref.WorkspaceID, &ref.RunID); scanErr != nil {
			return nil, scanErr
		}
		ref.WorkspaceID = strings.TrimSpace(ref.WorkspaceID)
		ref.RunID = strings.TrimSpace(ref.RunID)
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runs missing protocol start: %w", err)
	}
	return out, nil
}
