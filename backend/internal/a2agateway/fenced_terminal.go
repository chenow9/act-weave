package a2agateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FencedTerminalKind marks durable outbox payloads that must re-run FencedInboundTerminal
// (never unfenced FinalizeDelegation). Stale generation naturally rejects.
const FencedTerminalOutboxKind = "fenced_inbound_terminal"

// FencedTerminalOutboxPayload is the only outbox form allowed after a fenced
// production path encounters a *retryable* error. Conflict/lease-lost never enqueue.
type FencedTerminalOutboxPayload struct {
	Kind   string              `json:"kind"`
	Fenced FencedTerminalInput `json:"fenced"`
}

// FencedTerminalInput is one atomic inbound completion under lease ownership.
// All of agent_run / inbound task / delegation / AGENT_DELEGATION step are written
// in a single transaction gated by owner+token+generation+RUNNING+unexpired lease.
type FencedTerminalInput struct {
	WorkspaceID string
	TaskID      string
	RunID       string
	// Lease proof
	Owner      string
	Token      string
	Generation int64
	// Terminal statuses
	TaskStatus string // SUCCEEDED|FAILED|CANCELLED|TIMED_OUT
	RunStatus  string // same set for agent_runs
	// Optional expected run CAS; when ExpectedRunStatus empty, any RUNNING/PENDING.
	ExpectedRunStatus   string
	ExpectedLockVersion int64 // 0 = ignore lock version (still status-gated)
	RunOutputSummary    json.RawMessage
	RunErrorCode        string // only for FAILED run
	// Delegation finalize: must match task.delegation_id when task has one.
	// Empty means "use task-bound delegation only".
	DelegationID                                                                      string
	StepID                                                                            string
	DelStatus                                                                         string
	DelOutputSummary                                                                  json.RawMessage
	DelOutputPayload                                                                  json.RawMessage
	DelErrorCode                                                                      string
	DelErrorMessage                                                                   string
	RemoteTaskID, RemoteContextID, RemoteMessageID, RemoteEndpointRef, ProtocolStatus string
}

// FencedInboundTerminal atomically terminates run+task+delegation+step under lease.
// Returns ErrConflict when the caller no longer owns a valid lease (stale executor).
func (r *Repository) FencedInboundTerminal(ctx context.Context, in FencedTerminalInput) error {
	if r == nil || r.db == nil {
		return ErrInvalid
	}
	in.WorkspaceID = strings.TrimSpace(in.WorkspaceID)
	in.TaskID = strings.TrimSpace(in.TaskID)
	in.RunID = strings.TrimSpace(in.RunID)
	in.Owner = strings.TrimSpace(in.Owner)
	in.Token = strings.TrimSpace(in.Token)
	in.TaskStatus = strings.ToUpper(strings.TrimSpace(in.TaskStatus))
	in.RunStatus = strings.ToUpper(strings.TrimSpace(in.RunStatus))
	in.DelStatus = strings.ToUpper(strings.TrimSpace(in.DelStatus))
	in.DelegationID = strings.TrimSpace(in.DelegationID)
	in.StepID = strings.TrimSpace(in.StepID)
	if in.WorkspaceID == "" || in.TaskID == "" || in.RunID == "" ||
		in.Owner == "" || in.Token == "" || in.Generation < 1 {
		return ErrInvalid
	}
	if !validTerminalStatus(in.TaskStatus) || !validTerminalStatus(in.RunStatus) {
		return ErrInvalid
	}
	if len(in.RunOutputSummary) == 0 {
		in.RunOutputSummary = json.RawMessage(`{}`)
	}
	if len(in.DelOutputSummary) == 0 {
		in.DelOutputSummary = json.RawMessage(`{}`)
	}
	if len(in.DelOutputPayload) == 0 {
		in.DelOutputPayload = json.RawMessage(`{}`)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1) Lock task under full lease proof (no TOCTOU window after this).
	var taskRunID string
	var taskDelID sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT run_id::text, COALESCE(delegation_id::text,'')
		FROM agent_a2a_inbound_tasks
		WHERE workspace_id=$1 AND id=$2 AND status='RUNNING'
		  AND execute_owner=$3 AND execute_token=$4 AND execute_token <> ''
		  AND execute_generation=$5
		  AND execute_lease_until IS NOT NULL
		  AND execute_lease_until >= CURRENT_TIMESTAMP
		FOR UPDATE
	`, in.WorkspaceID, in.TaskID, in.Owner, in.Token, in.Generation).Scan(&taskRunID, &taskDelID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapWrite("lock fenced inbound task", err)
	}
	if taskRunID != in.RunID {
		return ErrConflict
	}

	// Post-claim fenced path requires a non-empty task-bound delegation.
	// Unbound tasks use AtomicUnownedInboundCleanup only (pre-bind / bind-fail).
	boundDel := ""
	if taskDelID.Valid {
		boundDel = strings.TrimSpace(taskDelID.String)
	}
	if boundDel == "" {
		return fmt.Errorf("%w: fenced terminal requires task.delegation_id (use unowned cleanup pre-bind)", ErrConflict)
	}
	if in.DelegationID != "" && in.DelegationID != boundDel {
		return fmt.Errorf("%w: fenced terminal delegation_id mismatch input=%s task=%s",
			ErrConflict, in.DelegationID, boundDel)
	}
	delIDStr := boundDel
	if !validTerminalStatus(in.DelStatus) {
		return ErrInvalid
	}

	// 2) Transition agent_run under the same TX (status CAS).
	if err := transitionRunTx(ctx, tx, in.WorkspaceID, in.RunID, in.RunStatus,
		in.ExpectedRunStatus, in.ExpectedLockVersion, in.RunOutputSummary, in.RunErrorCode); err != nil {
		return err
	}

	// 3) Finalize delegation + step while holding task lock.
	if delIDStr != "" {
		if err := finalizeDelegationTx(ctx, tx, fencedDelInput{
			WorkspaceID: in.WorkspaceID, DelegationID: delIDStr, StepID: in.StepID,
			ParentRunID: in.RunID,
			Status:      in.DelStatus, OutputSummary: in.DelOutputSummary, OutputPayload: in.DelOutputPayload,
			ErrorCode: in.DelErrorCode, ErrorMessage: in.DelErrorMessage,
			RemoteTaskID: in.RemoteTaskID, RemoteContextID: in.RemoteContextID,
			RemoteMessageID: in.RemoteMessageID, RemoteEndpointRef: in.RemoteEndpointRef,
			ProtocolStatus: firstNonEmpty(in.ProtocolStatus, in.DelStatus),
		}); err != nil {
			return err
		}
	}

	// 4) Mark task terminal; keep owner/token/generation for post-hoc audit.
	res, err := tx.ExecContext(ctx, `
		UPDATE agent_a2a_inbound_tasks
		SET status=$3, execute_finished_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP,
		    execute_lease_until=NULL
		WHERE workspace_id=$1 AND id=$2 AND status='RUNNING'
		  AND execute_owner=$4 AND execute_token=$5
		  AND execute_generation=$6
	`, in.WorkspaceID, in.TaskID, in.TaskStatus, in.Owner, in.Token, in.Generation)
	if err != nil {
		return mapWrite("fenced mark inbound task", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}

	if err := tx.Commit(); err != nil {
		return mapWrite("commit fenced inbound terminal", err)
	}
	return nil
}

// AtomicInboundCancel locks the inbound task and terminals run+task+delegation+step
// in one transaction. Invalidates lease (generation bump + clear token) so any
// in-flight FencedInboundTerminal for the old owner conflicts. Idempotent when
// already terminal (never SUCCEEDED→CANCELLED).
func (r *Repository) AtomicInboundCancel(ctx context.Context, workspaceID, taskID string) error {
	if r == nil || r.db == nil {
		return ErrInvalid
	}
	workspaceID, taskID = strings.TrimSpace(workspaceID), strings.TrimSpace(taskID)
	if workspaceID == "" || taskID == "" {
		return ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var runID, status string
	var delID sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT run_id::text, status, COALESCE(delegation_id::text,'')
		FROM agent_a2a_inbound_tasks
		WHERE workspace_id=$1 AND id=$2
		FOR UPDATE
	`, workspaceID, taskID).Scan(&runID, &status, &delID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	switch strings.ToUpper(status) {
	case "SUCCEEDED", "FAILED", "CANCELLED", "TIMED_OUT":
		// Sticky: already terminal — never rewrite.
		return tx.Commit()
	}

	outSum, _ := json.Marshal(map[string]any{"ok": false, "status": "CANCELLED", "cancelled": true, "source": "a2a.inbound.cancel"})
	outPay, _ := json.Marshal(map[string]any{"result": "(cancelled)"})

	// Invalidate lease so concurrent fenced executors cannot complete.
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_a2a_inbound_tasks
		SET execute_generation = execute_generation + 1,
		    execute_token = '',
		    execute_lease_until = NULL,
		    updated_at = CURRENT_TIMESTAMP
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, taskID); err != nil {
		return mapWrite("invalidate lease on cancel", err)
	}

	// Run → CANCELLED if still non-terminal.
	if err := transitionRunTxAllowAlready(ctx, tx, workspaceID, runID, "CANCELLED",
		"", 0, outSum, ""); err != nil {
		return err
	}

	if delID.Valid && strings.TrimSpace(delID.String) != "" {
		if err := finalizeDelegationTx(ctx, tx, fencedDelInput{
			WorkspaceID: workspaceID, DelegationID: delID.String, ParentRunID: runID,
			Status: "CANCELLED", OutputSummary: outSum, OutputPayload: outPay,
			ProtocolStatus: "CANCELLED",
		}); err != nil {
			return err
		}
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE agent_a2a_inbound_tasks
		SET status='CANCELLED', execute_finished_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP,
		    execute_lease_until=NULL, execute_token=''
		WHERE workspace_id=$1 AND id=$2
		  AND status NOT IN ('SUCCEEDED','FAILED','CANCELLED','TIMED_OUT')
	`, workspaceID, taskID)
	if err != nil {
		return mapWrite("atomic cancel mark task", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		// Concurrent winner may have terminalized — re-check sticky.
		var st string
		_ = tx.QueryRowContext(ctx, `SELECT status FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 AND id=$2`,
			workspaceID, taskID).Scan(&st)
		if !validTerminalStatus(st) {
			return ErrConflict
		}
		// Already terminal by concurrent path — commit no further writes needed.
		return tx.Commit()
	}
	return tx.Commit()
}

// AtomicUnownedInboundCleanup terminals run+task+delegation+step for a task that
// is still RUNNING and not held by a live lease (pre-claim bind/prepare failures).
// Never force-marks past an active foreign lease.
func (r *Repository) AtomicUnownedInboundCleanup(ctx context.Context, in UnownedCleanupInput) error {
	if r == nil || r.db == nil {
		return ErrInvalid
	}
	in.WorkspaceID = strings.TrimSpace(in.WorkspaceID)
	in.TaskID = strings.TrimSpace(in.TaskID)
	in.RunID = strings.TrimSpace(in.RunID)
	in.DelegationID = strings.TrimSpace(in.DelegationID)
	in.StepID = strings.TrimSpace(in.StepID)
	if in.WorkspaceID == "" || in.TaskID == "" || in.RunID == "" {
		return ErrInvalid
	}
	status := strings.ToUpper(strings.TrimSpace(in.Status))
	if status == "" {
		status = "FAILED"
	}
	if !validTerminalStatus(status) {
		return ErrInvalid
	}
	if len(in.RunOutputSummary) == 0 {
		in.RunOutputSummary = json.RawMessage(`{}`)
	}
	if len(in.DelOutputSummary) == 0 {
		in.DelOutputSummary = json.RawMessage(`{}`)
	}
	if len(in.DelOutputPayload) == 0 {
		in.DelOutputPayload = json.RawMessage(`{}`)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var taskRunID string
	var taskDelID sql.NullString
	// Only claim cleanup if no live foreign lease.
	err = tx.QueryRowContext(ctx, `
		SELECT run_id::text, COALESCE(delegation_id::text,'')
		FROM agent_a2a_inbound_tasks
		WHERE workspace_id=$1 AND id=$2 AND status='RUNNING'
		  AND (
		    execute_generation = 0
		    OR execute_token IS NULL OR execute_token = ''
		    OR execute_lease_until IS NULL
		    OR execute_lease_until < CURRENT_TIMESTAMP
		  )
		FOR UPDATE
	`, in.WorkspaceID, in.TaskID).Scan(&taskRunID, &taskDelID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapWrite("lock unowned inbound task", err)
	}
	if taskRunID != in.RunID {
		return ErrConflict
	}
	boundDel := ""
	if taskDelID.Valid {
		boundDel = strings.TrimSpace(taskDelID.String)
	}
	delID := boundDel
	if delID == "" {
		delID = in.DelegationID
	}
	if in.DelegationID != "" && boundDel != "" && in.DelegationID != boundDel {
		return fmt.Errorf("%w: cleanup delegation mismatch", ErrConflict)
	}

	// Strict: only RUNNING→terminal (or PENDING). Different already-terminal = ErrConflict + rollback.
	if err := transitionRunTx(ctx, tx, in.WorkspaceID, in.RunID, status,
		"RUNNING", 0, in.RunOutputSummary, in.RunErrorCode); err != nil {
		if errors.Is(err, ErrConflict) {
			if err2 := transitionRunTx(ctx, tx, in.WorkspaceID, in.RunID, status,
				"PENDING", 0, in.RunOutputSummary, in.RunErrorCode); err2 != nil {
				// Same-status sticky only (via allowAlready semantics with exact match).
				if err3 := transitionRunTxAllowAlready(ctx, tx, in.WorkspaceID, in.RunID, status,
					"", 0, in.RunOutputSummary, in.RunErrorCode); err3 != nil {
					return err3
				}
			}
		} else {
			return err
		}
	}

	if delID != "" {
		delStatus := status
		if in.DelStatus != "" {
			delStatus = strings.ToUpper(in.DelStatus)
		}
		if err := finalizeDelegationTx(ctx, tx, fencedDelInput{
			WorkspaceID: in.WorkspaceID, DelegationID: delID, StepID: in.StepID,
			ParentRunID: in.RunID,
			Status:      delStatus, OutputSummary: in.DelOutputSummary, OutputPayload: in.DelOutputPayload,
			ErrorCode: in.DelErrorCode, ErrorMessage: in.DelErrorMessage,
			ProtocolStatus: delStatus,
		}); err != nil {
			return err
		}
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE agent_a2a_inbound_tasks
		SET status=$3, execute_finished_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP,
		    execute_lease_until=NULL, execute_token='',
		    execute_generation = execute_generation + 1
		WHERE workspace_id=$1 AND id=$2 AND status='RUNNING'
	`, in.WorkspaceID, in.TaskID, status)
	if err != nil {
		return mapWrite("unowned cleanup mark task", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

// UnownedCleanupInput is pre-lease atomic cleanup for bind/prepare failures.
type UnownedCleanupInput struct {
	WorkspaceID, TaskID, RunID, DelegationID, StepID string
	Status                                           string // default FAILED
	RunOutputSummary                                 json.RawMessage
	RunErrorCode                                     string
	DelStatus                                        string
	DelOutputSummary, DelOutputPayload               json.RawMessage
	DelErrorCode, DelErrorMessage                    string
}

// EnqueueFencedTerminalOutbox stores a fenced re-apply command (not unfenced finalize).
func (r *Repository) EnqueueFencedTerminalOutbox(ctx context.Context, in FencedTerminalInput) error {
	if r == nil || r.db == nil {
		return ErrInvalid
	}
	in.WorkspaceID = strings.TrimSpace(in.WorkspaceID)
	delID := strings.TrimSpace(in.DelegationID)
	if in.WorkspaceID == "" || delID == "" || in.TaskID == "" || in.Token == "" || in.Generation < 1 {
		return ErrInvalid
	}
	payload, err := json.Marshal(FencedTerminalOutboxPayload{
		Kind: FencedTerminalOutboxKind, Fenced: in,
	})
	if err != nil {
		return err
	}
	return r.EnqueueFinalizeOutbox(ctx, in.WorkspaceID, delID, in.StepID, payload)
}

// FencedTransitionAgentRun atomically transitions agent_run only when the inbound
// task still holds a valid lease for this owner/token/generation.
func (r *Repository) FencedTransitionAgentRun(
	ctx context.Context,
	workspaceID, runID, taskID, owner, token string, generation int64,
	expectedStatus string, expectedLock int64,
	newStatus string, output json.RawMessage, errorCode string,
) error {
	if r == nil || r.db == nil {
		return ErrInvalid
	}
	workspaceID, runID, taskID = strings.TrimSpace(workspaceID), strings.TrimSpace(runID), strings.TrimSpace(taskID)
	owner, token = strings.TrimSpace(owner), strings.TrimSpace(token)
	newStatus = strings.ToUpper(strings.TrimSpace(newStatus))
	expectedStatus = strings.ToUpper(strings.TrimSpace(expectedStatus))
	if workspaceID == "" || runID == "" || taskID == "" || owner == "" || token == "" || generation < 1 {
		return ErrInvalid
	}
	if len(output) == 0 {
		output = json.RawMessage(`{}`)
	}
	errCode := sql.NullString{}
	if newStatus == "FAILED" && strings.TrimSpace(errorCode) != "" {
		errCode = sql.NullString{String: strings.TrimSpace(errorCode), Valid: true}
	}
	q := `
		UPDATE agent_runs ar SET
			status=$5, output_summary=$6, error_code=$7,
			finished_at=CURRENT_TIMESTAMP, lock_version=lock_version+1
		WHERE ar.workspace_id=$1 AND ar.id=$2
		  AND ar.status=$3 AND ($4::bigint = 0 OR ar.lock_version=$4)
		  AND EXISTS (
			SELECT 1 FROM agent_a2a_inbound_tasks t
			WHERE t.workspace_id=$1 AND t.id=$8
			  AND t.run_id = ar.id
			  AND t.status='RUNNING'
			  AND t.execute_owner=$9 AND t.execute_token=$10 AND t.execute_token <> ''
			  AND t.execute_generation=$11
			  AND t.execute_lease_until IS NOT NULL
			  AND t.execute_lease_until >= CURRENT_TIMESTAMP
		  )
	`
	res, err := r.db.ExecContext(ctx, q,
		workspaceID, runID, expectedStatus, expectedLock,
		newStatus, []byte(output), errCode,
		taskID, owner, token, generation,
	)
	if err != nil {
		return mapWrite("fenced transition agent run", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func validTerminalStatus(s string) bool {
	switch strings.ToUpper(s) {
	case "SUCCEEDED", "FAILED", "CANCELLED", "TIMED_OUT":
		return true
	default:
		return false
	}
}

type fencedDelInput struct {
	WorkspaceID, DelegationID, StepID, ParentRunID, Status                            string
	OutputSummary, OutputPayload                                                      json.RawMessage
	ErrorCode, ErrorMessage                                                           string
	RemoteTaskID, RemoteContextID, RemoteMessageID, RemoteEndpointRef, ProtocolStatus string
}

func finalizeDelegationTx(ctx context.Context, tx *sql.Tx, in fencedDelInput) error {
	in.Status = strings.ToUpper(strings.TrimSpace(in.Status))
	if in.DelegationID == "" || !validTerminalStatus(in.Status) {
		return ErrInvalid
	}
	var curStatus string
	var started sql.NullTime
	var parentRun string
	err := tx.QueryRowContext(ctx, `
		SELECT status, started_at, parent_run_id::text
		FROM agent_run_delegations
		WHERE workspace_id=$1 AND id=$2 FOR UPDATE
	`, in.WorkspaceID, in.DelegationID).Scan(&curStatus, &started, &parentRun)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// Parent run must match inbound run when provided (prevents cross-run step writes).
	if pr := strings.TrimSpace(in.ParentRunID); pr != "" && parentRun != pr {
		return fmt.Errorf("%w: delegation parent_run mismatch", ErrConflict)
	}
	already := curStatus == "SUCCEEDED" || curStatus == "FAILED" || curStatus == "CANCELLED" || curStatus == "TIMED_OUT"
	// Different terminal statuses are not idempotent — conflict to avoid mixed four-object state.
	if already && curStatus != in.Status {
		return fmt.Errorf("%w: delegation already %s, cannot finalize as %s", ErrConflict, curStatus, in.Status)
	}
	var latency any
	if started.Valid {
		ms := time.Since(started.Time).Milliseconds()
		if ms < 0 {
			ms = 0
		}
		latency = ms
	}
	errCode := sql.NullString{}
	if in.Status == "FAILED" && strings.TrimSpace(in.ErrorCode) != "" {
		errCode = sql.NullString{String: strings.TrimSpace(in.ErrorCode), Valid: true}
	} else if in.Status == "FAILED" {
		errCode = sql.NullString{String: "DELEGATION_FAILED", Valid: true}
	}
	if !already {
		res, err := tx.ExecContext(ctx, `
			UPDATE agent_run_delegations SET
				status=$3, output_summary=$4, output_payload=$5,
				error_code=$6, error_message=$7,
				remote_task_id=COALESCE(NULLIF($8,''), remote_task_id),
				remote_context_id=COALESCE(NULLIF($9,''), remote_context_id),
				remote_message_id=COALESCE(NULLIF($10,''), remote_message_id),
				remote_endpoint_ref=COALESCE(NULLIF($11,''), remote_endpoint_ref),
				protocol_status=COALESCE(NULLIF($12,''), protocol_status),
				latency_ms=$13,
				retry_count=GREATEST(0, attempt_count - 1),
				finished_at=GREATEST(COALESCE(started_at, NOW()), NOW())
			WHERE workspace_id=$1 AND id=$2 AND status IN ('PENDING','RUNNING')
		`, in.WorkspaceID, in.DelegationID, in.Status,
			[]byte(in.OutputSummary), []byte(in.OutputPayload),
			errCode, nullStr(in.ErrorMessage),
			in.RemoteTaskID, in.RemoteContextID, in.RemoteMessageID, in.RemoteEndpointRef,
			in.ProtocolStatus, latency)
		if err != nil {
			return mapWrite("fenced finalize delegation", err)
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
	}
	// Paired AGENT_DELEGATION step must belong to this delegation + parent run.
	stepID := strings.TrimSpace(in.StepID)
	if stepID == "" {
		var found sql.NullString
		_ = tx.QueryRowContext(ctx, `
			SELECT id::text FROM agent_run_steps
			WHERE workspace_id=$1 AND run_id=$2 AND step_type='AGENT_DELEGATION' AND delegation_id=$3
			ORDER BY sequence_no ASC LIMIT 1
		`, in.WorkspaceID, parentRun, in.DelegationID).Scan(&found)
		if found.Valid {
			stepID = found.String
		}
	}
	if stepID == "" {
		return fmt.Errorf("%w: AGENT_DELEGATION step missing for delegation %s", ErrConflict, in.DelegationID)
	}
	// Verify step is bound to this delegation (reject wrong-step hijack).
	var stepDel, stepRun string
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(delegation_id::text,''), run_id::text
		FROM agent_run_steps
		WHERE workspace_id=$1 AND id=$2 AND step_type='AGENT_DELEGATION'
	`, in.WorkspaceID, stepID).Scan(&stepDel, &stepRun)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: AGENT_DELEGATION step not found", ErrConflict)
	}
	if err != nil {
		return err
	}
	if stepDel != in.DelegationID || stepRun != parentRun {
		return fmt.Errorf("%w: step does not belong to delegation/run", ErrConflict)
	}
	stepStatus := in.Status
	if already {
		stepStatus = curStatus
	}
	stepErr := ""
	if stepStatus == "FAILED" {
		stepErr = firstNonEmpty(in.ErrorCode, "DELEGATION_FAILED")
	}
	outSum := in.OutputSummary
	if len(outSum) == 0 {
		outSum = json.RawMessage(`{}`)
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE agent_run_steps SET
			status=$3, output_summary=$4, error_code=$5,
			finished_at=GREATEST(started_at, NOW())
		WHERE workspace_id=$1 AND id=$2
		  AND delegation_id=$6
		  AND run_id=$7
		  AND (status='RUNNING' OR status=$3)
	`, in.WorkspaceID, stepID, stepStatus, []byte(outSum), nullStr(stepErr), in.DelegationID, parentRun)
	if err != nil {
		return mapWrite("fenced finalize delegation step", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		var st string
		if qerr := tx.QueryRowContext(ctx, `SELECT status FROM agent_run_steps WHERE workspace_id=$1 AND id=$2`,
			in.WorkspaceID, stepID).Scan(&st); qerr != nil || st != stepStatus {
			return fmt.Errorf("%w: AGENT_DELEGATION step not terminal (status=%s)", ErrConflict, st)
		}
	}
	return nil
}

func transitionRunTx(
	ctx context.Context, tx *sql.Tx,
	workspaceID, runID, newStatus, expectedStatus string, expectedLock int64,
	output json.RawMessage, errorCode string,
) error {
	return transitionRunTxInner(ctx, tx, workspaceID, runID, newStatus, expectedStatus, expectedLock, output, errorCode, false)
}

func transitionRunTxAllowAlready(
	ctx context.Context, tx *sql.Tx,
	workspaceID, runID, newStatus, expectedStatus string, expectedLock int64,
	output json.RawMessage, errorCode string,
) error {
	return transitionRunTxInner(ctx, tx, workspaceID, runID, newStatus, expectedStatus, expectedLock, output, errorCode, true)
}

func transitionRunTxInner(
	ctx context.Context, tx *sql.Tx,
	workspaceID, runID, newStatus, expectedStatus string, expectedLock int64,
	output json.RawMessage, errorCode string, allowAlreadyTerminal bool,
) error {
	newStatus = strings.ToUpper(strings.TrimSpace(newStatus))
	if len(output) == 0 {
		output = json.RawMessage(`{}`)
	}
	runErrCode := sql.NullString{}
	if newStatus == "FAILED" && strings.TrimSpace(errorCode) != "" {
		runErrCode = sql.NullString{String: strings.TrimSpace(errorCode), Valid: true}
	}
	expectedStatuses := []string{"RUNNING", "PENDING"}
	if s := strings.TrimSpace(expectedStatus); s != "" {
		expectedStatuses = []string{strings.ToUpper(s)}
	}
	var finished any
	if newStatus != "RUNNING" && newStatus != "PENDING" {
		finished = time.Now().UTC()
	}
	statusPlaceholders := make([]string, len(expectedStatuses))
	args := []any{workspaceID, runID, newStatus, []byte(output), runErrCode, finished}
	for i, st := range expectedStatuses {
		statusPlaceholders[i] = fmt.Sprintf("$%d", 7+i)
		args = append(args, st)
	}
	lockClause := ""
	if expectedLock > 0 {
		lockClause = fmt.Sprintf(" AND lock_version=$%d", 7+len(expectedStatuses))
		args = append(args, expectedLock)
	}
	q := fmt.Sprintf(`
		UPDATE agent_runs SET
			status=$3, output_summary=$4, error_code=$5,
			finished_at=COALESCE($6::timestamptz, finished_at),
			lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2
		  AND status IN (%s)%s
	`, strings.Join(statusPlaceholders, ","), lockClause)
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return mapWrite("fenced transition agent run", err)
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		return nil
	}
	if allowAlreadyTerminal {
		var cur string
		_ = tx.QueryRowContext(ctx, `SELECT status FROM agent_runs WHERE workspace_id=$1 AND id=$2`,
			workspaceID, runID).Scan(&cur)
		cur = strings.ToUpper(strings.TrimSpace(cur))
		// Same-status sticky only; different terminal = conflict (no mixed four-object state).
		if cur == newStatus {
			return nil
		}
		if validTerminalStatus(cur) {
			return fmt.Errorf("%w: agent_run already %s, cannot set %s", ErrConflict, cur, newStatus)
		}
	}
	return ErrConflict
}
