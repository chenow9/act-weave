package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Continuation recovery covers the post-commit path after an approved Interaction
// decision. Dispatch (tool/workflow side effect) is durable via checkpoint claim
// CAS; this service re-drives Dispatch without relying on in-memory callbacks.

var (
	ErrContinuationRecoveryInvalid = errors.New("continuation recovery input is invalid")
	ErrContinuationRecoveryNothing = errors.New("no recoverable approved continuation")
	ErrRuntimeContinueNotClaimed   = errors.New("runtime continue lease was not acquired")
)

// DefaultRuntimeContinueLease bounds how long one replica owns a continue drive.
// Must cover chatruntime EnqueueContinue's 5-minute timeout plus a small buffer so
// a live continue cannot be reclaimed mid-flight. Owners renew before expiry and
// call CompleteRuntimeContinue when the drive finishes (success or failure).
const DefaultRuntimeContinueLease = 6 * time.Minute

// MaxRuntimeContinueLease is the upper bound accepted by Claim/Renew.
const MaxRuntimeContinueLease = 10 * time.Minute

// RecoverableApprovedContinuation is an approved decision whose durable
// checkpoint has not yet reached a terminal resume status.
type RecoverableApprovedContinuation struct {
	WorkspaceID    string
	ConfirmationID string
	RunID          string
	TargetItemID   string
	Checkpoint     ConfirmationResumeCheckpoint
	Confirmation   ExecutionConfirmation
}

// OutboundContinuationGate gates multi-replica continue claims for roots that
// hold REQUEST_PASSTHROUGH affinity (T2=A). Pure Broker / no-affinity roots
// always Allow. Implementations must never transport Tokens.
//
// Nil gate preserves pre-outbound multi-replica reclaim behaviour.
type OutboundContinuationGate interface {
	// GateAgentRunContinuation returns allow/skip/fail-closed for workspace+run.
	GateAgentRunContinuation(ctx context.Context, workspaceID, runID string) (OutboundGateResult, error)
}

// OutboundGateResult is the affinity decision surface for recovery workers.
type OutboundGateResult struct {
	Allow      bool // RouteNone or RouteLocal
	Skip       bool // another live owner — do not steal claim
	FailClosed bool // owner lost / expired — OUTBOUND_CREDENTIAL_EXPIRED
	ReasonCode string
}

// ErrOutboundCredentialExpired is returned when a passthrough root's owner is
// lost and recovery must fail closed without taking a side-effect claim.
var ErrOutboundCredentialExpired = errors.New("outbound credential expired for affinity root")

// ContinuationRecoveryService re-dispatches approved confirmations after
// process crash, notify loss, or failed in-process wake-up.
type ContinuationRecoveryService struct {
	db            *sql.DB
	confirmations *ConfirmationService
	resumes       *ConfirmationResumeService
	decisions     *InteractionDecisionService
	outboundGate  OutboundContinuationGate
	now           func() time.Time
}

func NewContinuationRecoveryService(
	confirmations *ConfirmationService,
	resumes *ConfirmationResumeService,
	decisions *InteractionDecisionService,
) (*ContinuationRecoveryService, error) {
	if confirmations == nil || confirmations.repository == nil || resumes == nil ||
		resumes.checkpoints == nil || decisions == nil {
		return nil, errors.New("continuation recovery dependencies are required")
	}
	if confirmations.repository.db != resumes.checkpoints.db {
		return nil, errors.New("continuation recovery requires shared database")
	}
	return &ContinuationRecoveryService{
		db: confirmations.repository.db, confirmations: confirmations,
		resumes: resumes, decisions: decisions,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

// WithOutboundGate attaches T2=A affinity gating. Optional; nil disables.
func (service *ContinuationRecoveryService) WithOutboundGate(gate OutboundContinuationGate) *ContinuationRecoveryService {
	if service != nil {
		service.outboundGate = gate
	}
	return service
}

// ListApprovedPendingDispatch returns CONFIRMED interactions whose tool/workflow
// checkpoint is still PENDING or has an expired CLAIMED lease.
func (service *ContinuationRecoveryService) ListApprovedPendingDispatch(
	ctx context.Context,
	limit int,
) ([]RecoverableApprovedContinuation, error) {
	if service == nil || service.db == nil || ctx == nil || limit < 1 || limit > 1000 {
		return nil, ErrContinuationRecoveryInvalid
	}
	now := service.now().UTC()
	rows, err := service.db.QueryContext(ctx, `
		SELECT crc.confirmation_id, crc.workspace_id, crc.kind, crc.run_id, crc.target_item_id,
		       crc.execution_id, crc.agent_run_step_id, crc.execution_step_id, crc.node_id,
		       crc.run_wait_lock_version, crc.execution_wait_lock_version, crc.status,
		       crc.snapshot_schema_version, crc.request_snapshot, crc.resolved_snapshot,
		       crc.input_payload, crc.input_hash, crc.plan_hash, crc.interaction_binding_hash,
		       crc.terminal_on_success, crc.result_snapshot, crc.error_code, crc.claim_id,
		       crc.claim_expires_at, crc.created_at, crc.started_at, crc.completed_at, crc.lock_version
		FROM confirmation_resume_checkpoints crc
		JOIN execution_confirmations ec
		  ON ec.workspace_id = crc.workspace_id AND ec.id = crc.confirmation_id
		WHERE ec.status = 'CONFIRMED'
		  AND (
		    crc.status = 'PENDING'
		    OR (crc.status = 'CLAIMED' AND (crc.claim_expires_at IS NULL OR crc.claim_expires_at <= $1))
		  )
		ORDER BY crc.created_at, crc.confirmation_id
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list recoverable approved continuations: %w", err)
	}
	defer rows.Close()
	out := make([]RecoverableApprovedContinuation, 0)
	for rows.Next() {
		checkpoint, scanErr := scanConfirmationResume(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		confirmation, getErr := service.confirmations.Get(
			ctx, checkpoint.WorkspaceID, checkpoint.ConfirmationID,
		)
		if getErr != nil {
			return nil, getErr
		}
		out = append(out, RecoverableApprovedContinuation{
			WorkspaceID: checkpoint.WorkspaceID, ConfirmationID: checkpoint.ConfirmationID,
			RunID: checkpoint.RunID, TargetItemID: checkpoint.TargetItemID,
			Checkpoint: checkpoint, Confirmation: confirmation,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recoverable approved continuations: %w", err)
	}
	return out, nil
}

// ListSucceededAwaitingRuntimeContinue finds approved interactions where the
// tool/workflow side effect finished but the parent Run is still RUNNING —
// the in-process EnqueueContinue path may have been lost after commit.
// Candidates with a live multi-replica continue lease are excluded.
func (service *ContinuationRecoveryService) ListSucceededAwaitingRuntimeContinue(
	ctx context.Context,
	limit int,
) ([]RecoverableApprovedContinuation, error) {
	if service == nil || service.db == nil || ctx == nil || limit < 1 || limit > 1000 {
		return nil, ErrContinuationRecoveryInvalid
	}
	now := service.now().UTC()
	rows, err := service.db.QueryContext(ctx, `
		SELECT crc.confirmation_id, crc.workspace_id, crc.kind, crc.run_id, crc.target_item_id,
		       crc.execution_id, crc.agent_run_step_id, crc.execution_step_id, crc.node_id,
		       crc.run_wait_lock_version, crc.execution_wait_lock_version, crc.status,
		       crc.snapshot_schema_version, crc.request_snapshot, crc.resolved_snapshot,
		       crc.input_payload, crc.input_hash, crc.plan_hash, crc.interaction_binding_hash,
		       crc.terminal_on_success, crc.result_snapshot, crc.error_code, crc.claim_id,
		       crc.claim_expires_at, crc.created_at, crc.started_at, crc.completed_at, crc.lock_version
		FROM confirmation_resume_checkpoints crc
		JOIN execution_confirmations ec
		  ON ec.workspace_id = crc.workspace_id AND ec.id = crc.confirmation_id
		JOIN agent_runs ar
		  ON ar.workspace_id = crc.workspace_id AND ar.id = crc.run_id
		LEFT JOIN runtime_continuation_claims rcc
		  ON rcc.workspace_id = crc.workspace_id AND rcc.confirmation_id = crc.confirmation_id
		WHERE ec.status = 'CONFIRMED'
		  AND crc.status = 'SUCCEEDED'
		  AND ar.status = 'RUNNING'
		  AND crc.run_id IS NOT NULL
		  AND (
		    rcc.confirmation_id IS NULL
		    OR rcc.claim_id IS NULL
		    OR rcc.claim_expires_at IS NULL
		    OR rcc.claim_expires_at <= $1
		  )
		ORDER BY crc.completed_at NULLS LAST, crc.confirmation_id
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list runtime-continue recoveries: %w", err)
	}
	defer rows.Close()
	out := make([]RecoverableApprovedContinuation, 0)
	for rows.Next() {
		checkpoint, scanErr := scanConfirmationResume(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		confirmation, getErr := service.confirmations.Get(
			ctx, checkpoint.WorkspaceID, checkpoint.ConfirmationID,
		)
		if getErr != nil {
			return nil, getErr
		}
		out = append(out, RecoverableApprovedContinuation{
			WorkspaceID: checkpoint.WorkspaceID, ConfirmationID: checkpoint.ConfirmationID,
			RunID: checkpoint.RunID, TargetItemID: checkpoint.TargetItemID,
			Checkpoint: checkpoint, Confirmation: confirmation,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtime-continue recoveries: %w", err)
	}
	return out, nil
}

// RuntimeContinueClaim is a multi-replica exclusive lease for one continue drive.
type RuntimeContinueClaim struct {
	ConfirmationID string
	WorkspaceID    string
	RunID          string
	ClaimID        string
	ClaimExpiresAt time.Time
}

// ClaimRuntimeContinue acquires (or reclaims an expired) durable lease so only one
// replica enqueues ContinueApprovedInteraction for a SUCCEEDED+RUNNING pair.
// Both the normal approval path and Recovery Worker must share this lease.
//
// When an OutboundContinuationGate is configured, REQUEST_PASSTHROUGH affinity
// roots are only claimable on the live owner boot. Other live owners skip;
// owner-loss fails closed without taking the claim.
func (service *ContinuationRecoveryService) ClaimRuntimeContinue(
	ctx context.Context,
	workspaceID, confirmationID, runID string,
	lease time.Duration,
) (RuntimeContinueClaim, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	confirmationID = strings.TrimSpace(confirmationID)
	runID = strings.TrimSpace(runID)
	if service == nil || service.db == nil || ctx == nil ||
		!invocationValidUUID(workspaceID) || !invocationValidUUID(confirmationID) ||
		!invocationValidUUID(runID) {
		return RuntimeContinueClaim{}, ErrContinuationRecoveryInvalid
	}
	if service.outboundGate != nil {
		gate, gateErr := service.outboundGate.GateAgentRunContinuation(ctx, workspaceID, runID)
		if gateErr != nil {
			return RuntimeContinueClaim{}, gateErr
		}
		if gate.FailClosed {
			return RuntimeContinueClaim{}, ErrOutboundCredentialExpired
		}
		if gate.Skip || !gate.Allow {
			// Another live owner holds passthrough affinity — do not steal.
			return RuntimeContinueClaim{}, ErrRuntimeContinueNotClaimed
		}
	}
	lease = normalizeRuntimeContinueLease(lease)
	now := service.now().UTC()
	expires := now.Add(lease)
	claimID := newRuntimeContinueClaimID()
	// Upsert: insert free row or CAS-reclaim when lease expired / empty.
	row := service.db.QueryRowContext(ctx, `
		INSERT INTO runtime_continuation_claims (
			confirmation_id, workspace_id, run_id, claim_id, claim_expires_at,
			created_at, updated_at, lock_version
		) VALUES ($1, $2, $3, $4, $5, $6, $6, 1)
		ON CONFLICT (confirmation_id) DO UPDATE
		SET claim_id = EXCLUDED.claim_id,
		    claim_expires_at = EXCLUDED.claim_expires_at,
		    run_id = EXCLUDED.run_id,
		    workspace_id = EXCLUDED.workspace_id,
		    updated_at = EXCLUDED.updated_at,
		    lock_version = runtime_continuation_claims.lock_version + 1
		WHERE runtime_continuation_claims.claim_id IS NULL
		   OR runtime_continuation_claims.claim_expires_at IS NULL
		   OR runtime_continuation_claims.claim_expires_at <= $6
		RETURNING confirmation_id, workspace_id, run_id, claim_id, claim_expires_at
	`, confirmationID, workspaceID, runID, claimID, expires, now)
	var out RuntimeContinueClaim
	var storedClaim sql.NullString
	var storedExpiry sql.NullTime
	err := row.Scan(
		&out.ConfirmationID, &out.WorkspaceID, &out.RunID, &storedClaim, &storedExpiry,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeContinueClaim{}, ErrRuntimeContinueNotClaimed
	}
	if err != nil {
		return RuntimeContinueClaim{}, fmt.Errorf("claim runtime continue: %w", err)
	}
	if !storedClaim.Valid || !storedExpiry.Valid || storedClaim.String != claimID {
		return RuntimeContinueClaim{}, ErrRuntimeContinueNotClaimed
	}
	out.ClaimID = storedClaim.String
	out.ClaimExpiresAt = storedExpiry.Time.UTC()
	return out, nil
}

// RenewRuntimeContinue extends an owned lease so long continues stay exclusive
// across the full chatruntime 5-minute timeout window.
func (service *ContinuationRecoveryService) RenewRuntimeContinue(
	ctx context.Context,
	confirmationID, claimID string,
	lease time.Duration,
) (RuntimeContinueClaim, error) {
	confirmationID = strings.TrimSpace(confirmationID)
	claimID = strings.TrimSpace(claimID)
	if service == nil || service.db == nil || ctx == nil ||
		!invocationValidUUID(confirmationID) || !invocationValidUUID(claimID) {
		return RuntimeContinueClaim{}, ErrContinuationRecoveryInvalid
	}
	lease = normalizeRuntimeContinueLease(lease)
	now := service.now().UTC()
	expires := now.Add(lease)
	row := service.db.QueryRowContext(ctx, `
		UPDATE runtime_continuation_claims
		SET claim_expires_at = $3,
		    updated_at = $4,
		    lock_version = lock_version + 1
		WHERE confirmation_id = $1
		  AND claim_id = $2
		  AND claim_expires_at IS NOT NULL
		  AND claim_expires_at > $4
		RETURNING confirmation_id, workspace_id, run_id, claim_id, claim_expires_at
	`, confirmationID, claimID, expires, now)
	var out RuntimeContinueClaim
	var storedClaim sql.NullString
	var storedExpiry sql.NullTime
	err := row.Scan(
		&out.ConfirmationID, &out.WorkspaceID, &out.RunID, &storedClaim, &storedExpiry,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeContinueClaim{}, ErrRuntimeContinueNotClaimed
	}
	if err != nil {
		return RuntimeContinueClaim{}, fmt.Errorf("renew runtime continue: %w", err)
	}
	if !storedClaim.Valid || !storedExpiry.Valid || storedClaim.String != claimID {
		return RuntimeContinueClaim{}, ErrRuntimeContinueNotClaimed
	}
	out.ClaimID = storedClaim.String
	out.ClaimExpiresAt = storedExpiry.Time.UTC()
	return out, nil
}

// CompleteRuntimeContinue releases an owned lease after the continue drive finishes
// (success, waiting-again, or failure) so recovery can re-drive sooner when needed.
func (service *ContinuationRecoveryService) CompleteRuntimeContinue(
	ctx context.Context,
	confirmationID, claimID string,
) error {
	confirmationID = strings.TrimSpace(confirmationID)
	claimID = strings.TrimSpace(claimID)
	if service == nil || service.db == nil || ctx == nil ||
		!invocationValidUUID(confirmationID) || !invocationValidUUID(claimID) {
		return ErrContinuationRecoveryInvalid
	}
	now := service.now().UTC()
	result, err := service.db.ExecContext(ctx, `
		UPDATE runtime_continuation_claims
		SET claim_id = NULL,
		    claim_expires_at = NULL,
		    updated_at = $3,
		    lock_version = lock_version + 1
		WHERE confirmation_id = $1
		  AND claim_id = $2
	`, confirmationID, claimID, now)
	if err != nil {
		return fmt.Errorf("complete runtime continue: %w", err)
	}
	rows, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("complete runtime continue rows: %w", rowsErr)
	}
	if rows == 0 {
		return ErrRuntimeContinueNotClaimed
	}
	return nil
}

func normalizeRuntimeContinueLease(lease time.Duration) time.Duration {
	if lease < time.Second || lease > MaxRuntimeContinueLease {
		return DefaultRuntimeContinueLease
	}
	return lease
}

func newRuntimeContinueClaimID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// RecoverDispatch re-drives post-commit Dispatch for one approved confirmation.
// Side effects remain claim-CAS protected (no duplicate tool/workflow execution
// when the checkpoint is already SUCCEEDED or EXECUTING).
func (service *ContinuationRecoveryService) RecoverDispatch(
	ctx context.Context,
	workspaceID, confirmationID string,
) (InteractionDecisionResult, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	confirmationID = strings.TrimSpace(confirmationID)
	if service == nil || service.decisions == nil || ctx == nil ||
		!invocationValidUUID(workspaceID) || !invocationValidUUID(confirmationID) {
		return InteractionDecisionResult{}, ErrContinuationRecoveryInvalid
	}
	confirmation, err := service.confirmations.Get(ctx, workspaceID, confirmationID)
	if err != nil {
		return InteractionDecisionResult{}, err
	}
	if confirmation.Status != ConfirmationStatusConfirmed {
		return InteractionDecisionResult{}, ErrContinuationRecoveryNothing
	}
	checkpoint, err := service.resumes.GetCheckpoint(ctx, workspaceID, confirmationID)
	if err != nil {
		return InteractionDecisionResult{}, err
	}
	result := InteractionDecisionResult{
		Confirmation: confirmation, Checkpoint: checkpoint,
		Decision: InteractionDecisionApprove, ResumeStatus: checkpoint.Status,
	}
	// Already terminal success — return cached facts; never re-execute.
	if checkpoint.Status == ResumeStatusSucceeded {
		result.Cached = true
		return result, nil
	}
	if checkpoint.Status == ResumeStatusFailed || checkpoint.Status == ResumeStatusCancelled {
		return InteractionDecisionResult{}, ErrConfirmationResumeConflict
	}
	return service.decisions.Dispatch(ctx, result), nil
}

// RecoverPendingDispatches re-drives Dispatch for a batch of recoverable approvals.
func (service *ContinuationRecoveryService) RecoverPendingDispatches(
	ctx context.Context,
	limit int,
) ([]InteractionDecisionResult, error) {
	items, err := service.ListApprovedPendingDispatch(ctx, limit)
	if err != nil {
		return nil, err
	}
	results := make([]InteractionDecisionResult, 0, len(items))
	for _, item := range items {
		result, recoverErr := service.RecoverDispatch(ctx, item.WorkspaceID, item.ConfirmationID)
		if recoverErr != nil {
			// Surface per-item errors as DispatchError without aborting the batch.
			results = append(results, InteractionDecisionResult{
				Confirmation: item.Confirmation, Checkpoint: item.Checkpoint,
				Decision: InteractionDecisionApprove, ResumeStatus: item.Checkpoint.Status,
				DispatchError: recoverErr,
			})
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

// DecisionResultFromRecovery builds the value passed to runtime continuation.
func DecisionResultFromRecovery(item RecoverableApprovedContinuation) InteractionDecisionResult {
	return InteractionDecisionResult{
		Confirmation: item.Confirmation, Checkpoint: item.Checkpoint,
		Decision: InteractionDecisionApprove, ResumeStatus: item.Checkpoint.Status,
		Cached: item.Checkpoint.Status == ResumeStatusSucceeded,
	}
}
