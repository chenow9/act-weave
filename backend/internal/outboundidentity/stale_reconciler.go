package outboundidentity

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// AffinityExpireHook is invoked when a stale/lost-owner affinity is converged.
// Implementations must NOT take Tool side-effect claims or attempt Token recovery.
// They may mark the root execution failed with OUTBOUND_CREDENTIAL_EXPIRED.
type AffinityExpireHook func(ctx context.Context, affinity RuntimeAffinity) error

// StaleAffinityReconciler fails closed roots whose Vault owner is gone.
// It never steals affinity to another replica and never recovers Tokens.
//
// Vault plaintext dies with the owner process; this reconciler only converges
// durable execution state and deletes affinity routing rows. Local Vault
// cleanup for a still-running owner boot is done by execution terminal paths
// that know the full RootScope (including Subject).
type StaleAffinityReconciler struct {
	repo       *RuntimeRepository
	staleAfter time.Duration
	onExpire   AffinityExpireHook
	logger     *slog.Logger
}

// NewStaleAffinityReconciler constructs a reconciler.
func NewStaleAffinityReconciler(
	repo *RuntimeRepository,
	staleAfter time.Duration,
	onExpire AffinityExpireHook,
	logger *slog.Logger,
) (*StaleAffinityReconciler, error) {
	if repo == nil {
		return nil, errors.New("stale affinity reconciler repository is required")
	}
	if staleAfter <= 0 {
		staleAfter = DefaultHeartbeatStaleAfter
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &StaleAffinityReconciler{
		repo: repo, staleAfter: staleAfter, onExpire: onExpire, logger: logger,
	}, nil
}

// ReconcileOnce processes up to limit stale affinities.
// Returns the number of affinities deleted after fail-closed handling.
func (s *StaleAffinityReconciler) ReconcileOnce(ctx context.Context, limit int) (int, error) {
	if s == nil || s.repo == nil || ctx == nil {
		return 0, ErrCredentialInvalid
	}
	if limit < 1 {
		limit = 50
	}
	stale, err := s.repo.ListStaleAffinities(ctx, s.staleAfter, limit)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, affinity := range stale {
		// Optional domain fail-close (mark run failed). Must not claim tools.
		if s.onExpire != nil {
			if hookErr := s.onExpire(ctx, affinity); hookErr != nil {
				// Leave row for retry; do not steal or continue execution.
				s.logger.Warn("outbound affinity expire hook failed",
					"event", "outbound.affinity.expire_hook_failed",
					"workspace_id", affinity.WorkspaceID,
					"root_scope_type", string(affinity.RootScopeType),
					// root_scope_id is a UUID execution id — not a credential locator
					"root_scope_id", affinity.RootScopeID,
					"error_code", CodeCredentialExpired,
				)
				continue
			}
		}
		if err := s.repo.DeleteAffinity(ctx, affinity.WorkspaceID, affinity.RootScopeType, affinity.RootScopeID); err != nil {
			s.logger.Warn("outbound affinity delete failed",
				"event", "outbound.affinity.delete_failed",
				"workspace_id", affinity.WorkspaceID,
				"error_code", CodeCredentialExpired,
			)
			continue
		}
		removed++
	}
	return removed, nil
}

// FailClosedOnOwnerLoss is a helper for execution wiring: true when route is EXPIRED.
func FailClosedOnOwnerLoss(decision RouteDecision) bool {
	return decision.Kind == RouteExpired
}
