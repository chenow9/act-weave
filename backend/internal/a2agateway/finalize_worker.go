package a2agateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"actweave/backend/internal/agentdelegation"
)

// FinalizeWorker drains agent_run_delegation_finalize_outbox with SKIP LOCKED leases.
type FinalizeWorker struct {
	repo   *Repository
	audit  agentdelegation.AuditWriter
	logger *slog.Logger
	owner  string
	// Interval between poll cycles.
	Interval time.Duration
	// Batch size per claim.
	Batch int
	// Lease duration for claimed rows.
	Lease time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewFinalizeWorker(repo *Repository, audit agentdelegation.AuditWriter, logger *slog.Logger) (*FinalizeWorker, error) {
	if repo == nil || audit == nil {
		return nil, ErrInvalid
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &FinalizeWorker{
		repo: repo, audit: audit, logger: logger,
		owner: "finalize-worker", Interval: 2 * time.Second, Batch: 16, Lease: 30 * time.Second,
	}, nil
}

// Start begins background drain loops. Safe to call once.
func (w *FinalizeWorker) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	w.cancel = cancel
	w.done = make(chan struct{})
	go w.loop(runCtx)
}

// Stop cancels the worker and waits for the loop to exit.
func (w *FinalizeWorker) Stop(ctx context.Context) {
	w.mu.Lock()
	cancel := w.cancel
	done := w.done
	w.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
		}
	}
}

func (w *FinalizeWorker) loop(ctx context.Context) {
	defer close(w.done)
	// Immediate drain on start (process-restart recovery).
	w.drainOnce(ctx)
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.drainOnce(ctx)
		}
	}
}

// DrainOnce is exported for tests (synchronous recovery).
func (w *FinalizeWorker) DrainOnce(ctx context.Context) {
	w.drainOnce(ctx)
}

func (w *FinalizeWorker) drainOnce(ctx context.Context) {
	rows, err := w.repo.ClaimFinalizeOutboxBatch(ctx, w.owner, w.Batch, w.Lease)
	if err != nil {
		w.logger.Warn("finalize outbox claim failed", "error", err.Error())
		return
	}
	for _, row := range rows {
		w.processRow(ctx, row)
	}
}

func (w *FinalizeWorker) processRow(ctx context.Context, row FinalizeOutboxRow) {
	// Fenced inbound terminal outbox: re-apply only under original owner/token/generation.
	// Stale generation after reclaim → FencedInboundTerminal returns ErrConflict → drop (ack).
	var envelope FencedTerminalOutboxPayload
	if err := json.Unmarshal(row.Payload, &envelope); err == nil &&
		envelope.Kind == FencedTerminalOutboxKind {
		in := envelope.Fenced
		if in.WorkspaceID == "" {
			in.WorkspaceID = row.WorkspaceID
		}
		if in.DelegationID == "" {
			in.DelegationID = row.DelegationID
		}
		if in.StepID == "" {
			in.StepID = row.StepID
		}
		if err := w.repo.FencedInboundTerminal(ctx, in); err != nil {
			// Conflict = generation lost / already terminal by peer: ack and drop (never unfenced).
			if errors.Is(err, ErrConflict) {
				_ = w.repo.DeleteFinalizeOutboxClaimed(ctx, row.WorkspaceID, row.DelegationID, row.ClaimedBy, row.ClaimToken)
				w.logger.Info("fenced outbox dropped after conflict (stale or concurrent terminal)",
					"delegation_id", row.DelegationID)
				return
			}
			if nerr := w.repo.NackFinalizeOutbox(ctx, row.WorkspaceID, row.DelegationID, err.Error(),
				row.ClaimedBy, row.ClaimToken, row.Attempts); nerr != nil {
				w.logger.Warn("fenced outbox nack rejected",
					"delegation_id", row.DelegationID, "error", nerr.Error())
			}
			return
		}
		_ = w.repo.DeleteFinalizeOutboxClaimed(ctx, row.WorkspaceID, row.DelegationID, row.ClaimedBy, row.ClaimToken)
		return
	}

	// Unfenced finalize path: internal/outbound tools only (never used for stale inbound lease).
	var in agentdelegation.FinalizeDelegationInput
	if err := json.Unmarshal(row.Payload, &in); err != nil {
		_ = w.repo.NackFinalizeOutbox(ctx, row.WorkspaceID, row.DelegationID,
			"payload unmarshal: "+err.Error(), row.ClaimedBy, row.ClaimToken, row.Attempts)
		return
	}
	if in.WorkspaceID == "" {
		in.WorkspaceID = row.WorkspaceID
	}
	if in.DelegationID == "" {
		in.DelegationID = row.DelegationID
	}
	if in.StepID == "" {
		in.StepID = row.StepID
	}
	if _, err := w.audit.FinalizeDelegation(ctx, in); err != nil {
		if nerr := w.repo.NackFinalizeOutbox(ctx, row.WorkspaceID, row.DelegationID, err.Error(),
			row.ClaimedBy, row.ClaimToken, row.Attempts); nerr != nil {
			w.logger.Warn("finalize outbox nack rejected (stale claim)",
				"delegation_id", row.DelegationID, "error", nerr.Error())
		} else {
			w.logger.Warn("finalize outbox retry scheduled",
				"delegation_id", row.DelegationID, "attempts", row.Attempts+1, "error", err.Error())
		}
		return
	}
	// Ack only with claim token — stale workers cannot delete a reclaimed row.
	if err := w.repo.DeleteFinalizeOutboxClaimed(ctx, row.WorkspaceID, row.DelegationID, row.ClaimedBy, row.ClaimToken); err != nil {
		w.logger.Warn("finalize outbox delete rejected (stale claim)",
			"delegation_id", row.DelegationID, "error", err.Error())
	}
}
