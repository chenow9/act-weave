package agentdelegation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Repository persists bindings and authoritative delegation audit rows.
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("agentdelegation: database is required")
	}
	return &Repository{db: db}, nil
}

const bindingColumns = `
	b.id, b.workspace_id, b.caller_agent_id, b.target_agent_id, b.callable_name,
	b.description, b.mode, b.context_policy, b.enabled, b.version,
	COALESCE(b.created_by::text,''), COALESCE(b.updated_by::text,''),
	b.created_at, b.updated_at, b.deleted_at
`

func (r *Repository) CreateBinding(ctx context.Context, input CreateBindingInput) (Binding, error) {
	input = normalizeCreate(input)
	if err := validateCreate(input); err != nil {
		return Binding{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Binding{}, err
	}
	defer tx.Rollback()

	if err := r.assertAgentsActive(ctx, tx, input.WorkspaceID, input.CallerAgentID, input.TargetAgentID); err != nil {
		return Binding{}, err
	}
	// Disabled rows do not occupy namespace; enable/re-enable path locks and checks.
	if input.Enabled {
		// Workspace-level graph lock serializes concurrent A→B vs B→A cycle checks.
		if err := lockWorkspaceGraph(ctx, tx, input.WorkspaceID); err != nil {
			return Binding{}, err
		}
		// Concurrent-safe combined namespace lock (internal + A2A remote for same caller).
		if err := lockCallableNamespace(ctx, tx, input.WorkspaceID, input.CallerAgentID, input.CallableName); err != nil {
			return Binding{}, err
		}
		if err := r.assertCallableNamespaceFree(ctx, tx, input.WorkspaceID, input.CallerAgentID, input.CallableName, ""); err != nil {
			return Binding{}, err
		}
		// Soft-check cycle among enabled + this new edge (under graph advisory lock).
		edges, err := r.listEnabledEdgesTx(ctx, tx, input.WorkspaceID)
		if err != nil {
			return Binding{}, err
		}
		edges = append(edges, GraphEdgeSnapshot{
			BindingID: input.ID, CallerAgentID: input.CallerAgentID, TargetAgentID: input.TargetAgentID,
			CallableName: input.CallableName, Mode: input.Mode, ContextPolicy: input.ContextPolicy,
			Version: 1, Protocol: ProtocolInternal,
		})
		if err := DetectCycle(edges); err != nil {
			return Binding{}, err
		}
	}

	row := tx.QueryRowContext(ctx, `
		INSERT INTO agent_delegation_bindings AS b(
			id, workspace_id, caller_agent_id, target_agent_id, callable_name,
			description, mode, context_policy, enabled, version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,1,$10,$10)
		RETURNING `+bindingColumns,
		input.ID, input.WorkspaceID, input.CallerAgentID, input.TargetAgentID,
		input.CallableName, input.Description, input.Mode, input.ContextPolicy,
		input.Enabled, nullUUID(input.ActorID),
	)
	value, err := scanBinding(row)
	if err != nil {
		return Binding{}, mapWrite("create binding", err)
	}
	if err := tx.Commit(); err != nil {
		return Binding{}, mapWrite("commit binding", err)
	}
	return value, nil
}

func (r *Repository) UpdateBinding(ctx context.Context, input UpdateBindingInput) (Binding, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.BindingID = strings.TrimSpace(input.BindingID)
	if !validUUID(input.WorkspaceID) || !validUUID(input.BindingID) || input.ExpectedVersion < 1 {
		return Binding{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Binding{}, err
	}
	defer tx.Rollback()

	current, err := scanBinding(tx.QueryRowContext(ctx, `
		SELECT `+bindingColumns+` FROM agent_delegation_bindings b
		WHERE b.workspace_id=$1 AND b.id=$2 AND b.deleted_at IS NULL
		FOR UPDATE
	`, input.WorkspaceID, input.BindingID))
	if errors.Is(err, sql.ErrNoRows) {
		return Binding{}, ErrNotFound
	}
	if err != nil {
		return Binding{}, err
	}
	if current.Version != input.ExpectedVersion {
		return Binding{}, ErrConflict
	}
	if input.TargetAgentID != nil {
		tid := strings.TrimSpace(*input.TargetAgentID)
		if !validUUID(tid) {
			return Binding{}, ErrInvalid
		}
		current.TargetAgentID = tid
	}
	if input.CallableName != nil {
		current.CallableName = normalizeCallableName(*input.CallableName)
	}
	if input.Description != nil {
		current.Description = strings.TrimSpace(*input.Description)
	}
	if input.Mode != nil {
		current.Mode = strings.ToUpper(strings.TrimSpace(*input.Mode))
	}
	if input.ContextPolicy != nil {
		current.ContextPolicy = strings.ToUpper(strings.TrimSpace(*input.ContextPolicy))
	}
	if input.Enabled != nil {
		current.Enabled = *input.Enabled
	}
	if current.CallableName == "" || !validMode(current.Mode) || !validContextPolicy(current.ContextPolicy) {
		return Binding{}, ErrInvalid
	}
	// Disabled rows may edit description/mode/callable without holding the active
	// namespace; re-enable or enabled writes must check namespace.
	needsNamespace := current.Enabled
	if current.Enabled {
		// Serialize graph mutations for the whole workspace before cycle detection.
		if err := lockWorkspaceGraph(ctx, tx, input.WorkspaceID); err != nil {
			return Binding{}, err
		}
	}
	if err := lockCallableNamespace(ctx, tx, input.WorkspaceID, current.CallerAgentID, current.CallableName); err != nil {
		return Binding{}, err
	}
	if needsNamespace {
		if err := r.assertCallableNamespaceFree(ctx, tx, input.WorkspaceID, current.CallerAgentID, current.CallableName, current.ID); err != nil {
			return Binding{}, err
		}
	}
	// If re-enabling / enabled, re-check cycle with all other enabled edges.
	if current.Enabled {
		edges, err := r.listEnabledEdgesTx(ctx, tx, input.WorkspaceID)
		if err != nil {
			return Binding{}, err
		}
		// Replace self if present.
		filtered := make([]GraphEdgeSnapshot, 0, len(edges)+1)
		for _, e := range edges {
			if e.BindingID != current.ID {
				filtered = append(filtered, e)
			}
		}
		filtered = append(filtered, GraphEdgeSnapshot{
			BindingID: current.ID, CallerAgentID: current.CallerAgentID, TargetAgentID: current.TargetAgentID,
			CallableName: current.CallableName, Mode: current.Mode, ContextPolicy: current.ContextPolicy,
			Version: current.Version + 1, Protocol: ProtocolInternal,
		})
		if err := DetectCycle(filtered); err != nil {
			return Binding{}, err
		}
	}

	if err := r.assertAgentsActive(ctx, tx, input.WorkspaceID, current.CallerAgentID, current.TargetAgentID); err != nil {
		return Binding{}, err
	}
	value, err := scanBinding(tx.QueryRowContext(ctx, `
		UPDATE agent_delegation_bindings b SET
			target_agent_id=$4, callable_name=$5, description=$6, mode=$7, context_policy=$8, enabled=$9,
			updated_by=$10, updated_at=clock_timestamp(), version=version+1
		WHERE workspace_id=$1 AND id=$2 AND version=$3 AND deleted_at IS NULL
		RETURNING `+bindingColumns,
		input.WorkspaceID, input.BindingID, input.ExpectedVersion,
		current.TargetAgentID, current.CallableName, current.Description, current.Mode, current.ContextPolicy,
		current.Enabled, nullUUID(input.ActorID),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Binding{}, ErrConflict
	}
	if err != nil {
		return Binding{}, mapWrite("update binding", err)
	}
	if err := tx.Commit(); err != nil {
		return Binding{}, mapWrite("commit binding update", err)
	}
	return value, nil
}

// SoftDisable sets enabled=false without deleted_at so the row stays listable/editable/re-enableable.
func (r *Repository) SoftDisable(ctx context.Context, workspaceID, bindingID string, expectedVersion int64, actorID string) error {
	workspaceID, bindingID = strings.TrimSpace(workspaceID), strings.TrimSpace(bindingID)
	if !validUUID(workspaceID) || !validUUID(bindingID) || expectedVersion < 1 {
		return ErrInvalid
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE agent_delegation_bindings SET
			enabled=FALSE, updated_by=$4,
			updated_at=clock_timestamp(), version=version+1
		WHERE workspace_id=$1 AND id=$2 AND version=$3 AND deleted_at IS NULL
	`, workspaceID, bindingID, expectedVersion, nullUUID(actorID))
	if err != nil {
		return mapWrite("soft disable binding", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) GetBinding(ctx context.Context, workspaceID, bindingID string) (Binding, error) {
	if !validUUID(workspaceID) || !validUUID(bindingID) {
		return Binding{}, ErrInvalid
	}
	value, err := scanBinding(r.db.QueryRowContext(ctx, `
		SELECT `+bindingColumns+` FROM agent_delegation_bindings b
		WHERE b.workspace_id=$1 AND b.id=$2 AND b.deleted_at IS NULL
	`, workspaceID, bindingID))
	if errors.Is(err, sql.ErrNoRows) {
		return Binding{}, ErrNotFound
	}
	return value, err
}

func (r *Repository) ListBindings(ctx context.Context, workspaceID, callerAgentID string) ([]Binding, error) {
	if !validUUID(workspaceID) {
		return nil, ErrInvalid
	}
	callerAgentID = strings.TrimSpace(callerAgentID)
	var rows *sql.Rows
	var err error
	if callerAgentID == "" {
		rows, err = r.db.QueryContext(ctx, `
			SELECT `+bindingColumns+` FROM agent_delegation_bindings b
			WHERE b.workspace_id=$1 AND b.deleted_at IS NULL
			ORDER BY b.created_at ASC, b.id ASC
		`, workspaceID)
	} else {
		if !validUUID(callerAgentID) {
			return nil, ErrInvalid
		}
		rows, err = r.db.QueryContext(ctx, `
			SELECT `+bindingColumns+` FROM agent_delegation_bindings b
			WHERE b.workspace_id=$1 AND b.caller_agent_id=$2 AND b.deleted_at IS NULL
			ORDER BY b.created_at ASC, b.id ASC
		`, workspaceID, callerAgentID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Binding, 0)
	for rows.Next() {
		v, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListEnabledForCaller returns enabled bindings for graph build.
func (r *Repository) ListEnabledForCaller(ctx context.Context, workspaceID, callerAgentID string) ([]Binding, error) {
	if !validUUID(workspaceID) || !validUUID(callerAgentID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+bindingColumns+` FROM agent_delegation_bindings b
		WHERE b.workspace_id=$1 AND b.caller_agent_id=$2
		  AND b.enabled AND b.deleted_at IS NULL
		ORDER BY b.callable_name ASC, b.id ASC
	`, workspaceID, callerAgentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Binding, 0)
	for rows.Next() {
		v, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListAllEnabled returns all enabled internal edges for a workspace (cycle checks / snapshots).
func (r *Repository) ListAllEnabled(ctx context.Context, workspaceID string) ([]Binding, error) {
	if !validUUID(workspaceID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+bindingColumns+` FROM agent_delegation_bindings b
		WHERE b.workspace_id=$1 AND b.enabled AND b.deleted_at IS NULL
		ORDER BY b.caller_agent_id, b.callable_name, b.id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Binding, 0)
	for rows.Next() {
		v, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repository) listEnabledEdgesTx(ctx context.Context, tx *sql.Tx, workspaceID string) ([]GraphEdgeSnapshot, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, caller_agent_id, target_agent_id, callable_name, description,
		       mode, context_policy, version
		FROM agent_delegation_bindings
		WHERE workspace_id=$1 AND enabled AND deleted_at IS NULL
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GraphEdgeSnapshot
	for rows.Next() {
		var e GraphEdgeSnapshot
		if err := rows.Scan(&e.BindingID, &e.CallerAgentID, &e.TargetAgentID, &e.CallableName,
			&e.Description, &e.Mode, &e.ContextPolicy, &e.Version); err != nil {
			return nil, err
		}
		e.Protocol = ProtocolInternal
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Repository) assertAgentsActive(ctx context.Context, tx *sql.Tx, workspaceID, callerID, targetID string) error {
	var count int
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)::int FROM agents
		WHERE workspace_id=$1 AND id = ANY($2::uuid[])
		  AND deleted_at IS NULL AND status='ACTIVE'
	`, workspaceID, pq.Array([]string{callerID, targetID})).Scan(&count)
	if err != nil {
		return err
	}
	if count != 2 {
		return ErrAgentUnavailable
	}
	return nil
}

// lockWorkspaceGraph serializes enabled-graph mutations (cycle check) for a workspace.
// Must be taken before DetectCycle so concurrent A→B and B→A cannot both pass.
func lockWorkspaceGraph(ctx context.Context, tx *sql.Tx, workspaceID string) error {
	key := "agent_delegation_graph|" + strings.TrimSpace(workspaceID)
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, key)
	return err
}

// lockCallableNamespace serializes create/update of the same (workspace, caller, callable_name).
func lockCallableNamespace(ctx context.Context, tx *sql.Tx, workspaceID, callerAgentID, callableName string) error {
	key := workspaceID + "|" + callerAgentID + "|" + strings.ToLower(strings.TrimSpace(callableName))
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, key)
	return err
}

// assertCallableNamespaceFree ensures no other enabled internal binding, A2A remote,
// or capability callable_name collides for this caller (case-insensitive).
// excludeBindingID skips self on update. Only ENABLED peers block the name;
// disabled rows may keep/edit names without reclaiming the active namespace.
func (r *Repository) assertCallableNamespaceFree(
	ctx context.Context, tx *sql.Tx, workspaceID, callerAgentID, callableName, excludeBindingID string,
) error {
	callableName = normalizeCallableName(callableName)
	var n int
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)::int FROM agent_delegation_bindings
		WHERE workspace_id=$1 AND caller_agent_id=$2 AND lower(btrim(callable_name))=lower(btrim($3))
		  AND enabled AND deleted_at IS NULL
		  AND ($4 = '' OR id::text <> $4)
	`, workspaceID, callerAgentID, callableName, strings.TrimSpace(excludeBindingID)).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrDuplicateAlias
	}
	// Cross-source: A2A remotes for same caller.
	err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*)::int FROM agent_a2a_remote_bindings
		WHERE workspace_id=$1 AND caller_agent_id=$2 AND lower(btrim(callable_name))=lower(btrim($3))
		  AND enabled AND deleted_at IS NULL
	`, workspaceID, callerAgentID, callableName).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrNamespaceConflict
	}
	// Cross-source: capability bindings for this agent (callable_name case-insensitive).
	// Prefer active release when set; otherwise pinned_release_id for PINNED policy.
	err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*)::int
		FROM agent_capability_bindings acb
		JOIN capabilities c
		  ON c.workspace_id = acb.workspace_id AND c.id = acb.capability_id
		JOIN capability_releases cr
		  ON cr.workspace_id = acb.workspace_id
		 AND cr.capability_id = acb.capability_id
		 AND cr.id = COALESCE(acb.pinned_release_id, c.active_release_id)
		WHERE acb.workspace_id=$1 AND acb.agent_id=$2 AND acb.enabled
		  AND c.deleted_at IS NULL
		  AND lower(btrim(cr.callable_name)) = lower(btrim($3))
	`, workspaceID, callerAgentID, callableName).Scan(&n)
	if err != nil {
		return fmt.Errorf("capability namespace check: %w", err)
	}
	if n > 0 {
		return ErrNamespaceConflict
	}
	return nil
}

// ---------------------------------------------------------------------------
// Delegation audit
// ---------------------------------------------------------------------------

const delegationColumns = `
	d.id, d.workspace_id, d.parent_run_id, d.child_run_id, d.parent_delegation_id,
	d.caller_agent_id, d.target_agent_id, d.external_agent_ref, d.mode, d.protocol,
	d.origin, d.depth, d.binding_version, d.tool_call_id, d.idempotency_key, d.status,
	d.input_summary, d.input_payload, d.output_summary, d.output_payload,
	COALESCE(d.error_code,''), COALESCE(d.error_message,''),
	COALESCE(d.remote_task_id,''), COALESCE(d.remote_context_id,''),
	COALESCE(d.remote_message_id,''), COALESCE(d.remote_endpoint_ref,''),
	COALESCE(d.protocol_status,''), d.latency_ms,
	d.input_tokens, d.output_tokens, d.total_tokens, COALESCE(d.tokens_known,false),
	COALESCE(d.attempt_count,0), COALESCE(d.retry_count,0),
	d.started_at, d.finished_at, d.created_at
`

// loadIdempotentDelegationReplayFresh re-opens a short read for concurrent-loser replay
// after the insert TX was rolled back (PG aborts TX on unique violation).
func (r *Repository) loadIdempotentDelegationReplayFresh(ctx context.Context, input CreateDelegationInput) (Delegation, bool, error) {
	return loadIdempotentDelegationReplay(ctx, r.db, input)
}

// loadIdempotentDelegationReplay loads the winner row for the same workspace+idempotency key,
// validates freeze identity, and attaches the paired step id.
// Returns (del, true, nil) on successful replay; (zero, false, err) on mismatch/load failure.
func loadIdempotentDelegationReplay(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, input CreateDelegationInput) (Delegation, bool, error) {
	existing, err := scanDelegation(q.QueryRowContext(ctx, `
		SELECT `+delegationColumns+` FROM agent_run_delegations d
		WHERE d.workspace_id=$1 AND d.idempotency_key=$2
	`, input.WorkspaceID, input.IdempotencyKey))
	if err != nil {
		return Delegation{}, false, err
	}
	if mismatch := delegationIdentityMismatch(existing, input); mismatch != "" {
		return Delegation{}, false, fmt.Errorf("%w: idempotent replay identity mismatch: %s", ErrConflict, mismatch)
	}
	var stepID sql.NullString
	_ = q.QueryRowContext(ctx, `
		SELECT id::text FROM agent_run_steps
		WHERE workspace_id=$1 AND run_id=$2 AND step_type=$3
		  AND input_summary->>'delegationId' = $4
		ORDER BY sequence_no ASC LIMIT 1
	`, input.WorkspaceID, existing.ParentRunID, StepTypeAgentDelegation, existing.ID).Scan(&stepID)
	if stepID.Valid {
		existing.StepID = stepID.String
	}
	return existing, true, nil
}

// CreateDelegationAndStep fail-closed: inserts RUNNING delegation + AGENT_DELEGATION step.
// Idempotent on (workspace_id, idempotency_key): returns existing row without re-dispatch marker.
// Concurrent losers that hit the unique key converge to the same replay path as sequential retries.
func (r *Repository) CreateDelegationAndStep(ctx context.Context, input CreateDelegationInput) (Delegation, bool, error) {
	input = normalizeCreateDelegation(input)
	if err := validateCreateDelegation(input); err != nil {
		return Delegation{}, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Delegation{}, false, err
	}
	defer tx.Rollback()

	// Idempotent lookup first — must match freeze identity, not key alone.
	existing, replay, err := loadIdempotentDelegationReplay(ctx, tx, input)
	if err == nil && replay {
		if cerr := tx.Commit(); cerr != nil {
			return Delegation{}, false, cerr
		}
		return existing, true, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Delegation{}, false, err
	}

	// Parent run must be RUNNING (or WAITING_* for resume nested).
	var runStatus string
	if err := tx.QueryRowContext(ctx, `
		SELECT status FROM agent_runs WHERE workspace_id=$1 AND id=$2 FOR UPDATE
	`, input.WorkspaceID, input.ParentRunID).Scan(&runStatus); errors.Is(err, sql.ErrNoRows) {
		return Delegation{}, false, ErrNotFound
	} else if err != nil {
		return Delegation{}, false, err
	}
	switch strings.ToUpper(runStatus) {
	case "RUNNING", "WAITING_CONFIRMATION", "WAITING_INTERACTION":
	default:
		return Delegation{}, false, ErrInvalid
	}

	now := time.Now().UTC()
	inputSummary := mustObject(input.InputSummary)
	inputPayload := mustObject(input.InputPayload)
	empty := json.RawMessage(`{}`)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_run_delegations(
			id, workspace_id, parent_run_id, child_run_id, parent_delegation_id,
			caller_agent_id, target_agent_id, external_agent_ref, mode, protocol, origin,
			depth, binding_version, tool_call_id, idempotency_key, status,
			input_summary, input_payload, output_summary, output_payload, started_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'RUNNING',
			$16,$17,$18,$19,$20
		)
	`, input.ID, input.WorkspaceID, input.ParentRunID, nullUUIDPtr(input.ChildRunID),
		nullUUIDPtr(input.ParentDelegationID), input.CallerAgentID,
		nullUUIDPtr(input.TargetAgentID), nullStrPtr(input.ExternalAgentRef),
		input.Mode, input.Protocol, input.Origin, input.Depth, input.BindingVersion,
		input.ToolCallID, input.IdempotencyKey,
		[]byte(inputSummary), []byte(inputPayload), []byte(empty), []byte(empty), now)
	if err != nil {
		// Concurrent same (workspace, idempotency_key): loser must converge to winner replay.
		// Other unique conflicts (id/step/namespace) stay hard conflicts.
		// PostgreSQL aborts the TX after any statement error — rollback and re-read
		// winner outside the failed TX.
		mapped := mapWrite("insert delegation", err)
		_ = tx.Rollback()
		if errors.Is(mapped, ErrConflict) {
			del, replay, rerr := r.loadIdempotentDelegationReplayFresh(ctx, input)
			if rerr == nil && replay {
				return del, true, nil
			}
			if rerr != nil && !errors.Is(rerr, sql.ErrNoRows) {
				return Delegation{}, false, errors.Join(mapped, rerr)
			}
		}
		return Delegation{}, false, mapped
	}

	// Attach delegation id into step input for audit join.
	stepInput := map[string]any{}
	_ = json.Unmarshal(inputSummary, &stepInput)
	stepInput["delegationId"] = input.ID
	stepInput["mode"] = input.Mode
	stepInput["protocol"] = input.Protocol
	stepInput["origin"] = input.Origin
	stepInput["depth"] = input.Depth
	stepInput["bindingVersion"] = input.BindingVersion
	stepInput["toolCallId"] = input.ToolCallID
	if input.TargetAgentID != nil {
		stepInput["targetAgentId"] = *input.TargetAgentID
	}
	if input.ExternalAgentRef != nil {
		stepInput["externalAgentRef"] = *input.ExternalAgentRef
	}
	stepInput["callerAgentId"] = input.CallerAgentID
	stepInputBytes, _ := json.Marshal(stepInput)

	var sequence int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence_no),0)+1 FROM agent_run_steps
		WHERE workspace_id=$1 AND run_id=$2
	`, input.WorkspaceID, input.ParentRunID).Scan(&sequence); err != nil {
		return Delegation{}, false, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_run_steps(
			id, workspace_id, run_id, sequence_no, step_type, status,
			input_summary, agent_id, delegation_id, parent_step_id
		) VALUES ($1,$2,$3,$4,$5,'RUNNING',$6,$7,$8,$9)
	`, input.StepID, input.WorkspaceID, input.ParentRunID, sequence, StepTypeAgentDelegation,
		stepInputBytes, nullUUID(input.AgentID), input.ID, nullUUIDPtr(input.ParentStepID))
	if err != nil {
		return Delegation{}, false, mapWrite("insert AGENT_DELEGATION step", err)
	}

	value, err := scanDelegation(tx.QueryRowContext(ctx, `
		SELECT `+delegationColumns+` FROM agent_run_delegations d WHERE d.workspace_id=$1 AND d.id=$2
	`, input.WorkspaceID, input.ID))
	if err != nil {
		return Delegation{}, false, err
	}
	value.StepID = input.StepID
	if err := tx.Commit(); err != nil {
		return Delegation{}, false, mapWrite("commit delegation prewrite", err)
	}
	return value, false, nil
}

// FinalizeDelegation idempotently sets terminal status on delegation + step.
func (r *Repository) FinalizeDelegation(ctx context.Context, input FinalizeDelegationInput) (Delegation, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.DelegationID = strings.TrimSpace(input.DelegationID)
	input.StepID = strings.TrimSpace(input.StepID)
	input.Status = strings.ToUpper(strings.TrimSpace(input.Status))
	if !validUUID(input.WorkspaceID) || !validUUID(input.DelegationID) || !validTerminal(input.Status) {
		return Delegation{}, ErrInvalid
	}
	outSum := mustObject(input.OutputSummary)
	outPay := mustObject(input.OutputPayload)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Delegation{}, err
	}
	defer tx.Rollback()

	current, err := scanDelegation(tx.QueryRowContext(ctx, `
		SELECT `+delegationColumns+` FROM agent_run_delegations d
		WHERE d.workspace_id=$1 AND d.id=$2 FOR UPDATE
	`, input.WorkspaceID, input.DelegationID))
	if errors.Is(err, sql.ErrNoRows) {
		return Delegation{}, ErrNotFound
	}
	if err != nil {
		return Delegation{}, err
	}
	// Already terminal: only same-status is idempotent. Different terminal → ErrConflict
	// (delegation/step left unchanged — sticky). Terminal evidence is immutable (DB trigger);
	// reconcile step + optional linked child only — never rewrite delegation row.
	if isTerminal(current.Status) {
		if current.Status != input.Status {
			return Delegation{}, fmt.Errorf("%w: delegation already %s, cannot finalize as %s",
				ErrConflict, current.Status, input.Status)
		}
		stepID := strings.TrimSpace(input.StepID)
		if stepID == "" {
			var found sql.NullString
			_ = tx.QueryRowContext(ctx, `
				SELECT id::text FROM agent_run_steps
				WHERE workspace_id=$1 AND run_id=$2 AND step_type=$3 AND delegation_id=$4
				ORDER BY sequence_no ASC LIMIT 1
			`, input.WorkspaceID, current.ParentRunID, StepTypeAgentDelegation, current.ID).Scan(&found)
			if found.Valid {
				stepID = found.String
			}
		}
		if stepID == "" {
			return Delegation{}, fmt.Errorf("%w: AGENT_DELEGATION step missing for terminal delegation %s", ErrConflict, current.ID)
		}
		// Terminal step evidence is immutable once written. Same-status finalize:
		//   - RUNNING → one-shot reconcile to sticky terminal (only transition allowed)
		//   - already terminal matching → exact no-op (no finished_at/output rewrite)
		//   - already terminal mismatched → ErrConflict
		stepStatus := current.Status
		stepErr := ""
		if current.Status == StatusFailed {
			stepErr = firstNonEmpty(current.ErrorCode, "DELEGATION_FAILED")
		}
		outSum := mustObject(current.OutputSummary)
		var curStepStatus string
		var curOut []byte
		var curErrCode sql.NullString
		if serr := tx.QueryRowContext(ctx, `
			SELECT status, output_summary, error_code FROM agent_run_steps
			WHERE workspace_id=$1 AND id=$2 FOR UPDATE
		`, input.WorkspaceID, stepID).Scan(&curStepStatus, &curOut, &curErrCode); serr != nil {
			return Delegation{}, fmt.Errorf("%w: load AGENT_DELEGATION step: %v", ErrConflict, serr)
		}
		curStepStatus = strings.ToUpper(strings.TrimSpace(curStepStatus))
		if curStepStatus == "RUNNING" || curStepStatus == "PENDING" || curStepStatus == "QUEUED" {
			res, uerr := tx.ExecContext(ctx, `
				UPDATE agent_run_steps SET
					status=$4, output_summary=$5, error_code=$6,
					finished_at=GREATEST(started_at, NOW())
				WHERE workspace_id=$1 AND id=$2 AND status=$3
			`, input.WorkspaceID, stepID, curStepStatus, stepStatus, []byte(outSum), nullStr(stepErr))
			if uerr != nil {
				return Delegation{}, mapWrite("reconcile terminal delegation step", uerr)
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return Delegation{}, fmt.Errorf("%w: AGENT_DELEGATION step terminal reconcile affected %d rows", ErrConflict, n)
			}
		} else if isTerminal(curStepStatus) {
			if err := matchTerminalStepEvidence(curStepStatus, curOut, curErrCode, stepStatus, stepErr, outSum); err != nil {
				return Delegation{}, err
			}
			// Exact match: no-op (immutability trigger + no finished_at rewrite).
		} else {
			return Delegation{}, fmt.Errorf("%w: AGENT_DELEGATION step not finishable (status=%s)", ErrConflict, curStepStatus)
		}
		// Recover orphan TASK child: same-status outbox retry may finish linked child.
		childID := ""
		if current.ChildRunID != nil {
			childID = strings.TrimSpace(*current.ChildRunID)
		}
		if childID == "" && input.ChildRunID != nil {
			childID = strings.TrimSpace(*input.ChildRunID)
		}
		if childID != "" {
			if cerr := terminalLinkedChildRunTx(ctx, tx, input.WorkspaceID, childID, current.Status, stepErr); cerr != nil {
				return Delegation{}, cerr
			}
		}
		current.StepID = stepID
		if err := tx.Commit(); err != nil {
			return Delegation{}, err
		}
		return current, nil
	}

	var latency any
	if current.StartedAt != nil {
		ms := time.Since(*current.StartedAt).Milliseconds()
		if ms < 0 {
			ms = 0
		}
		latency = ms
	}
	errCode := sql.NullString{}
	if input.Status == StatusFailed && strings.TrimSpace(input.ErrorCode) != "" {
		errCode = sql.NullString{String: strings.TrimSpace(input.ErrorCode), Valid: true}
	} else if input.Status == StatusFailed {
		errCode = sql.NullString{String: "DELEGATION_FAILED", Valid: true}
	}

	// Token fields: only set when TokensKnown (never invent 0 for unknown).
	var inTok, outTok, totTok any
	if input.TokensKnown {
		if input.InputTokens != nil {
			inTok = *input.InputTokens
		}
		if input.OutputTokens != nil {
			outTok = *input.OutputTokens
		}
		if input.TotalTokens != nil {
			totTok = *input.TotalTokens
		}
	}
	// attempt/retry are authoritative only via RecordDispatchAttempt.
	// Finalize never overwrites them (leave DB counters intact).
	// Enforce invariant retry = max(0, attempt-1) on the way out.
	res, err := tx.ExecContext(ctx, `
		UPDATE agent_run_delegations SET
			status=$3, output_summary=$4, output_payload=$5,
			error_code=$6, error_message=$7,
			child_run_id=CASE WHEN child_run_id IS NULL THEN $8 ELSE child_run_id END,
			remote_task_id=COALESCE(NULLIF($9,''), remote_task_id),
			remote_context_id=COALESCE(NULLIF($10,''), remote_context_id),
			remote_message_id=COALESCE(NULLIF($11,''), remote_message_id),
			remote_endpoint_ref=COALESCE(NULLIF($12,''), remote_endpoint_ref),
			protocol_status=COALESCE(NULLIF($13,''), protocol_status),
			latency_ms=$14,
			input_tokens=CASE WHEN $15 THEN COALESCE($16::bigint, input_tokens) ELSE input_tokens END,
			output_tokens=CASE WHEN $15 THEN COALESCE($17::bigint, output_tokens) ELSE output_tokens END,
			total_tokens=CASE WHEN $15 THEN COALESCE($18::bigint, total_tokens) ELSE total_tokens END,
			tokens_known=CASE WHEN $15 THEN TRUE ELSE tokens_known END,
			retry_count=GREATEST(0, attempt_count - 1),
			finished_at=GREATEST(COALESCE(started_at, NOW()), NOW())
		WHERE workspace_id=$1 AND id=$2 AND status IN ('PENDING','RUNNING')
	`, input.WorkspaceID, input.DelegationID, input.Status,
		[]byte(outSum), []byte(outPay), errCode, nullStr(input.ErrorMessage),
		nullUUIDPtr(input.ChildRunID),
		input.RemoteTaskID, input.RemoteContextID, input.RemoteMessageID,
		input.RemoteEndpointRef, input.ProtocolStatus, latency,
		input.TokensKnown, inTok, outTok, totTok)
	if err != nil {
		return Delegation{}, mapWrite("finalize delegation", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Delegation{}, fmt.Errorf("%w: delegation terminal update affected %d rows", ErrConflict, n)
	}

	// Paired AGENT_DELEGATION step is required — never commit terminal without it.
	stepID := strings.TrimSpace(input.StepID)
	if stepID == "" {
		var found sql.NullString
		_ = tx.QueryRowContext(ctx, `
			SELECT id::text FROM agent_run_steps
			WHERE workspace_id=$1 AND run_id=$2 AND step_type=$3 AND delegation_id=$4
			ORDER BY sequence_no ASC LIMIT 1
		`, input.WorkspaceID, current.ParentRunID, StepTypeAgentDelegation, current.ID).Scan(&found)
		if found.Valid {
			stepID = found.String
		}
	}
	if stepID == "" {
		return Delegation{}, fmt.Errorf("%w: AGENT_DELEGATION step missing for delegation %s", ErrConflict, input.DelegationID)
	}
	stepStatus := input.Status
	stepErr := ""
	if input.Status == StatusFailed {
		stepErr = firstNonEmpty(input.ErrorCode, "DELEGATION_FAILED")
	}
	// First terminal write only from RUNNING (never rewrite already-terminal evidence).
	res, err = tx.ExecContext(ctx, `
		UPDATE agent_run_steps SET
			status=$4, output_summary=$5, error_code=$6,
			finished_at=GREATEST(started_at, NOW())
		WHERE workspace_id=$1 AND id=$2 AND status=$3
	`, input.WorkspaceID, stepID, "RUNNING", stepStatus,
		[]byte(outSum), nullStr(stepErr))
	if err != nil {
		return Delegation{}, mapWrite("finalize delegation step", err)
	}
	n, _ = res.RowsAffected()
	if n != 1 {
		// Sticky outbox race: step may already be terminal. Accept only exact
		// evidence match (status + error_code + normalized output_summary); never rewrite.
		var st string
		var curOut []byte
		var curErrCode sql.NullString
		if lerr := tx.QueryRowContext(ctx, `
			SELECT status, output_summary, error_code FROM agent_run_steps
			WHERE workspace_id=$1 AND id=$2
		`, input.WorkspaceID, stepID).Scan(&st, &curOut, &curErrCode); lerr != nil {
			return Delegation{}, fmt.Errorf("%w: AGENT_DELEGATION step update affected %d rows (load: %v)", ErrConflict, n, lerr)
		}
		if err := matchTerminalStepEvidence(st, curOut, curErrCode, stepStatus, stepErr, outSum); err != nil {
			return Delegation{}, err
		}
		// Exact match: leave step row untouched (no finished_at/output rewrite).
	}
	input.StepID = stepID

	// Same-TX TASK child terminal recovery: if ChildRunID is linked (or supplied),
	// finish the child agent_run here so outbox workers never leave RUNNING children.
	childID := ""
	if input.ChildRunID != nil {
		childID = strings.TrimSpace(*input.ChildRunID)
	}
	if childID == "" && current.ChildRunID != nil {
		childID = strings.TrimSpace(*current.ChildRunID)
	}
	if childID != "" {
		if cerr := terminalLinkedChildRunTx(ctx, tx, input.WorkspaceID, childID, input.Status, stepErr); cerr != nil {
			return Delegation{}, cerr
		}
	}

	value, err := scanDelegation(tx.QueryRowContext(ctx, `
		SELECT `+delegationColumns+` FROM agent_run_delegations d WHERE d.workspace_id=$1 AND d.id=$2
	`, input.WorkspaceID, input.DelegationID))
	if err != nil {
		return Delegation{}, err
	}
	value.StepID = input.StepID
	if err := tx.Commit(); err != nil {
		return Delegation{}, err
	}
	return value, nil
}

// matchTerminalStepEvidence requires status + error_code + normalized output_summary
// to equal the sticky terminal intent. Used when a step is already terminal and must
// not be rewritten (000018 immutability).
func matchTerminalStepEvidence(gotStatus string, gotOut []byte, gotErrCode sql.NullString, wantStatus, wantErr string, wantOut json.RawMessage) error {
	gotStatus = strings.ToUpper(strings.TrimSpace(gotStatus))
	wantStatus = strings.ToUpper(strings.TrimSpace(wantStatus))
	if !isTerminal(gotStatus) {
		return fmt.Errorf("%w: AGENT_DELEGATION step not terminal (status=%s)", ErrConflict, gotStatus)
	}
	if gotStatus != wantStatus {
		return fmt.Errorf("%w: AGENT_DELEGATION step already %s, expected %s", ErrConflict, gotStatus, wantStatus)
	}
	gotErr := ""
	if gotErrCode.Valid {
		gotErr = strings.TrimSpace(gotErrCode.String)
	}
	if gotErr != strings.TrimSpace(wantErr) {
		return fmt.Errorf("%w: AGENT_DELEGATION step error_code mismatch", ErrConflict)
	}
	if string(mustObject(gotOut)) != string(mustObject(wantOut)) {
		return fmt.Errorf("%w: AGENT_DELEGATION step output_summary mismatch", ErrConflict)
	}
	return nil
}

// terminalLinkedChildRunTx finishes a TASK child agent_run in the same TX as
// delegation/step terminal. Sticky: same terminal is ok; different terminal → ErrConflict;
// non-RUNNING/PENDING already terminal with matching status is a no-op.
func terminalLinkedChildRunTx(ctx context.Context, tx *sql.Tx, workspaceID, childRunID, delStatus, errCode string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	childRunID = strings.TrimSpace(childRunID)
	delStatus = strings.ToUpper(strings.TrimSpace(delStatus))
	if workspaceID == "" || childRunID == "" || !validTerminal(delStatus) {
		return nil
	}
	var status string
	var lockVersion int64
	err := tx.QueryRowContext(ctx, `
		SELECT status, lock_version FROM agent_runs
		WHERE workspace_id=$1 AND id=$2 FOR UPDATE
	`, workspaceID, childRunID).Scan(&status, &lockVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: linked child run not found", ErrNotFound)
	}
	if err != nil {
		return err
	}
	status = strings.ToUpper(status)
	if isTerminal(status) {
		if status != delStatus {
			return fmt.Errorf("%w: child run already %s, cannot terminal as %s", ErrConflict, status, delStatus)
		}
		return nil
	}
	switch status {
	case "RUNNING", "PENDING":
	default:
		return fmt.Errorf("%w: child run not finishable (status=%s)", ErrConflict, status)
	}
	// Permanent-snapshot: finished_at required for terminal; lock_version must increment.
	runErr := ""
	if delStatus == StatusFailed {
		runErr = firstNonEmpty(errCode, "DELEGATION_CHILD_FAILED")
	}
	out, _ := json.Marshal(map[string]any{
		"source": "agentdelegation.finalize", "status": delStatus, "errorCode": runErr,
	})
	res, err := tx.ExecContext(ctx, `
		UPDATE agent_runs SET
			status=$3,
			output_summary=$4,
			error_code=$5,
			finished_at=GREATEST(COALESCE(started_at, NOW()), NOW()),
			lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND status IN ('PENDING','RUNNING')
		  AND lock_version=$6
	`, workspaceID, childRunID, delStatus, out, nullStr(runErr), lockVersion)
	if err != nil {
		return mapWrite("terminal linked child run", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("%w: linked child run terminal update affected %d rows", ErrConflict, n)
	}
	return nil
}

func (r *Repository) GetByIdempotency(ctx context.Context, workspaceID, key string) (Delegation, error) {
	if !validUUID(workspaceID) || strings.TrimSpace(key) == "" {
		return Delegation{}, ErrInvalid
	}
	value, err := scanDelegation(r.db.QueryRowContext(ctx, `
		SELECT `+delegationColumns+` FROM agent_run_delegations d
		WHERE d.workspace_id=$1 AND d.idempotency_key=$2
	`, workspaceID, strings.TrimSpace(key)))
	if errors.Is(err, sql.ErrNoRows) {
		return Delegation{}, ErrNotFound
	}
	return value, err
}

// SetChildRunID sets child_run_id once (NULL → value) for TASK mode.
func (r *Repository) SetChildRunID(ctx context.Context, workspaceID, delegationID, childRunID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	delegationID = strings.TrimSpace(delegationID)
	childRunID = strings.TrimSpace(childRunID)
	if !validUUID(workspaceID) || !validUUID(delegationID) || !validUUID(childRunID) {
		return ErrInvalid
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE agent_run_delegations SET child_run_id=$3
		WHERE workspace_id=$1 AND id=$2 AND child_run_id IS NULL
		  AND status IN ('PENDING','RUNNING')
	`, workspaceID, delegationID, childRunID)
	if err != nil {
		return mapWrite("set child_run_id", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		// Already set to same value is ok.
		var existing sql.NullString
		_ = r.db.QueryRowContext(ctx, `
			SELECT child_run_id::text FROM agent_run_delegations
			WHERE workspace_id=$1 AND id=$2
		`, workspaceID, delegationID).Scan(&existing)
		if existing.Valid && existing.String == childRunID {
			return nil
		}
		return ErrConflict
	}
	return nil
}

func (r *Repository) ListByParentRun(ctx context.Context, workspaceID, parentRunID string) ([]Delegation, error) {
	if !validUUID(workspaceID) || !validUUID(parentRunID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+delegationColumns+` FROM agent_run_delegations d
		WHERE d.workspace_id=$1 AND d.parent_run_id=$2
		ORDER BY d.created_at ASC, d.id ASC
	`, workspaceID, parentRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Delegation, 0)
	for rows.Next() {
		v, err := scanDelegation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

type scannable interface {
	Scan(dest ...any) error
}

func scanBinding(row scannable) (Binding, error) {
	var b Binding
	var createdBy, updatedBy string
	var deletedAt sql.NullTime
	err := row.Scan(
		&b.ID, &b.WorkspaceID, &b.CallerAgentID, &b.TargetAgentID, &b.CallableName,
		&b.Description, &b.Mode, &b.ContextPolicy, &b.Enabled, &b.Version,
		&createdBy, &updatedBy, &b.CreatedAt, &b.UpdatedAt, &deletedAt,
	)
	if err != nil {
		return Binding{}, err
	}
	b.CreatedBy, b.UpdatedBy = createdBy, updatedBy
	b.CreatedAt, b.UpdatedAt = b.CreatedAt.UTC(), b.UpdatedAt.UTC()
	if deletedAt.Valid {
		t := deletedAt.Time.UTC()
		b.DeletedAt = &t
	}
	return b, nil
}

func scanDelegation(row scannable) (Delegation, error) {
	var d Delegation
	var childRun, parentDel, target sql.NullString
	var external sql.NullString
	var latency, inTok, outTok, totTok sql.NullInt64
	var tokensKnown bool
	var attempt, retry int
	var started, finished sql.NullTime
	var inSum, inPay, outSum, outPay []byte
	err := row.Scan(
		&d.ID, &d.WorkspaceID, &d.ParentRunID, &childRun, &parentDel,
		&d.CallerAgentID, &target, &external, &d.Mode, &d.Protocol,
		&d.Origin, &d.Depth, &d.BindingVersion, &d.ToolCallID, &d.IdempotencyKey, &d.Status,
		&inSum, &inPay, &outSum, &outPay,
		&d.ErrorCode, &d.ErrorMessage,
		&d.RemoteTaskID, &d.RemoteContextID, &d.RemoteMessageID, &d.RemoteEndpointRef,
		&d.ProtocolStatus, &latency,
		&inTok, &outTok, &totTok, &tokensKnown, &attempt, &retry,
		&started, &finished, &d.CreatedAt,
	)
	if err != nil {
		return Delegation{}, err
	}
	if childRun.Valid {
		s := childRun.String
		d.ChildRunID = &s
	}
	if parentDel.Valid {
		s := parentDel.String
		d.ParentDelegationID = &s
	}
	if target.Valid {
		s := target.String
		d.TargetAgentID = &s
	}
	if external.Valid {
		s := external.String
		d.ExternalAgentRef = &s
	}
	d.InputSummary = append(json.RawMessage(nil), inSum...)
	d.InputPayload = append(json.RawMessage(nil), inPay...)
	d.OutputSummary = append(json.RawMessage(nil), outSum...)
	d.OutputPayload = append(json.RawMessage(nil), outPay...)
	if latency.Valid {
		v := latency.Int64
		d.LatencyMs = &v
	}
	if tokensKnown {
		d.TokensKnown = true
		if inTok.Valid {
			v := inTok.Int64
			d.InputTokens = &v
		}
		if outTok.Valid {
			v := outTok.Int64
			d.OutputTokens = &v
		}
		if totTok.Valid {
			v := totTok.Int64
			d.TotalTokens = &v
		}
	}
	d.AttemptCount, d.RetryCount = attempt, retry
	if started.Valid {
		t := started.Time.UTC()
		d.StartedAt = &t
	}
	if finished.Valid {
		t := finished.Time.UTC()
		d.FinishedAt = &t
	}
	d.CreatedAt = d.CreatedAt.UTC()
	return d, nil
}

// RecordDispatchAttempt increments attempt_count for a real agent dispatch.
// Idempotent replay paths must NOT call this. Finalize-outbox retries must NOT call this.
func (r *Repository) RecordDispatchAttempt(ctx context.Context, workspaceID, delegationID string) error {
	workspaceID, delegationID = strings.TrimSpace(workspaceID), strings.TrimSpace(delegationID)
	if !validUUID(workspaceID) || !validUUID(delegationID) {
		return ErrInvalid
	}
	// PostgreSQL evaluates RHS with old row values: attempt_count+1 and
	// retry_count=old attempt_count (= new-1) in one statement.
	res, err := r.db.ExecContext(ctx, `
		UPDATE agent_run_delegations SET
			attempt_count = attempt_count + 1,
			retry_count = attempt_count
		WHERE workspace_id=$1 AND id=$2 AND status IN ('PENDING','RUNNING')
	`, workspaceID, delegationID)
	if err != nil {
		return mapWrite("record dispatch attempt", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// AccumulateModelTokens adds one MODEL turn's usage into the parent delegation totals.
// Only call when usage.Known; never invent zeros for unknown remote usage.
func (r *Repository) AccumulateModelTokens(ctx context.Context, workspaceID, delegationID string, usage TokenUsage) error {
	if !usage.Known {
		return nil
	}
	workspaceID, delegationID = strings.TrimSpace(workspaceID), strings.TrimSpace(delegationID)
	if !validUUID(workspaceID) || !validUUID(delegationID) {
		return ErrInvalid
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE agent_run_delegations SET
			input_tokens = COALESCE(input_tokens,0) + $3,
			output_tokens = COALESCE(output_tokens,0) + $4,
			total_tokens = COALESCE(total_tokens,0) + $5,
			tokens_known = TRUE
		WHERE workspace_id=$1 AND id=$2 AND status IN ('PENDING','RUNNING')
	`, workspaceID, delegationID, usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	if err != nil {
		return mapWrite("accumulate model tokens", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func normalizeCreate(in CreateBindingInput) CreateBindingInput {
	in.ID = strings.TrimSpace(in.ID)
	in.WorkspaceID = strings.TrimSpace(in.WorkspaceID)
	in.CallerAgentID = strings.TrimSpace(in.CallerAgentID)
	in.TargetAgentID = strings.TrimSpace(in.TargetAgentID)
	in.CallableName = normalizeCallableName(in.CallableName)
	in.Description = strings.TrimSpace(in.Description)
	in.Mode = strings.ToUpper(strings.TrimSpace(in.Mode))
	if in.Mode == "" {
		in.Mode = ModeInline
	}
	in.ContextPolicy = strings.ToUpper(strings.TrimSpace(in.ContextPolicy))
	if in.ContextPolicy == "" {
		in.ContextPolicy = ContextTaskOnly
	}
	in.ActorID = strings.TrimSpace(in.ActorID)
	return in
}

func validateCreate(in CreateBindingInput) error {
	if !validUUID(in.ID) || !validUUID(in.WorkspaceID) || !validUUID(in.CallerAgentID) || !validUUID(in.TargetAgentID) {
		return ErrInvalid
	}
	if in.CallerAgentID == in.TargetAgentID {
		return ErrSelfLoop
	}
	if in.CallableName == "" || !validMode(in.Mode) || !validContextPolicy(in.ContextPolicy) {
		return ErrInvalid
	}
	return nil
}

func normalizeCreateDelegation(in CreateDelegationInput) CreateDelegationInput {
	in.ID = strings.TrimSpace(in.ID)
	in.WorkspaceID = strings.TrimSpace(in.WorkspaceID)
	in.ParentRunID = strings.TrimSpace(in.ParentRunID)
	in.CallerAgentID = strings.TrimSpace(in.CallerAgentID)
	in.Mode = strings.ToUpper(strings.TrimSpace(in.Mode))
	in.Protocol = strings.ToUpper(strings.TrimSpace(in.Protocol))
	in.Origin = strings.ToUpper(strings.TrimSpace(in.Origin))
	in.ToolCallID = strings.TrimSpace(in.ToolCallID)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	in.StepID = strings.TrimSpace(in.StepID)
	in.AgentID = strings.TrimSpace(in.AgentID)
	if in.Mode == "" {
		in.Mode = ModeInline
	}
	if in.Protocol == "" {
		in.Protocol = ProtocolInternal
	}
	if in.Origin == "" {
		in.Origin = OriginInternal
	}
	// Depth 0 is valid for EXTERNAL root inbound; only negative is invalid/defaulted.
	if in.Depth < 0 {
		in.Depth = 1
	}
	if in.Depth == 0 && in.Origin != OriginExternal {
		// Internal/nested without explicit depth defaults to 1.
		in.Depth = 1
	}
	if in.BindingVersion <= 0 {
		in.BindingVersion = 1
	}
	return in
}

// delegationIdentityMismatch returns a non-empty reason when existing != input freeze identity.
func delegationIdentityMismatch(existing Delegation, in CreateDelegationInput) string {
	if existing.ParentRunID != in.ParentRunID {
		return "parentRunId"
	}
	if existing.CallerAgentID != in.CallerAgentID {
		return "callerAgentId"
	}
	exTarget, inTarget := "", ""
	if existing.TargetAgentID != nil {
		exTarget = *existing.TargetAgentID
	}
	if in.TargetAgentID != nil {
		inTarget = strings.TrimSpace(*in.TargetAgentID)
	}
	if exTarget != inTarget {
		return "targetAgentId"
	}
	exExt, inExt := "", ""
	if existing.ExternalAgentRef != nil {
		exExt = strings.TrimSpace(*existing.ExternalAgentRef)
	}
	if in.ExternalAgentRef != nil {
		inExt = strings.TrimSpace(*in.ExternalAgentRef)
	}
	if exExt != inExt {
		return "externalAgentRef"
	}
	if existing.Mode != in.Mode {
		return "mode"
	}
	if existing.Protocol != in.Protocol {
		return "protocol"
	}
	if existing.Origin != in.Origin {
		return "origin"
	}
	if existing.Depth != in.Depth {
		return "depth"
	}
	if existing.BindingVersion != in.BindingVersion {
		return "bindingVersion"
	}
	if existing.ToolCallID != in.ToolCallID {
		return "toolCallId"
	}
	exParentDel, inParentDel := "", ""
	if existing.ParentDelegationID != nil {
		exParentDel = *existing.ParentDelegationID
	}
	if in.ParentDelegationID != nil {
		inParentDel = strings.TrimSpace(*in.ParentDelegationID)
	}
	if exParentDel != inParentDel {
		return "parentDelegationId"
	}
	// Normalized input identity: only enforce when the caller supplies a non-empty payload.
	// Idempotent replay paths often omit InputPayload; empty must not conflict with stored.
	if len(bytes.TrimSpace(in.InputPayload)) > 0 && string(mustObject(in.InputPayload)) != "{}" {
		exPay := mustObject(existing.InputPayload)
		inPay := mustObject(in.InputPayload)
		if string(exPay) != string(inPay) {
			return "inputPayload"
		}
	}
	return ""
}

func validateCreateDelegation(in CreateDelegationInput) error {
	if !validUUID(in.ID) || !validUUID(in.WorkspaceID) || !validUUID(in.ParentRunID) ||
		!validUUID(in.CallerAgentID) || !validUUID(in.StepID) || in.IdempotencyKey == "" {
		return ErrInvalid
	}
	if !validMode(in.Mode) {
		return ErrInvalid
	}
	if in.Protocol != ProtocolInternal && in.Protocol != ProtocolA2A {
		return ErrInvalid
	}
	if in.Origin != OriginInternal && in.Origin != OriginExternal {
		return ErrInvalid
	}
	return nil
}

func normalizeCallableName(name string) string {
	name = strings.TrimSpace(name)
	// Stable tool-safe name: lowercase, alnum + underscore.
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else if r == '-' || r == ' ' {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func validMode(m string) bool {
	return m == ModeInline || m == ModeTask
}

func validContextPolicy(p string) bool {
	// Only TASK_ONLY is implemented; reject SUMMARY/SELECTED_MESSAGES at write path.
	return p == ContextTaskOnly
}

func validUUID(v string) bool {
	_, err := uuid.Parse(strings.TrimSpace(v))
	return err == nil
}

func validTerminal(s string) bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	default:
		return false
	}
}

func isTerminal(s string) bool {
	return validTerminal(strings.ToUpper(strings.TrimSpace(s)))
}

func mustObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return json.RawMessage(`{}`)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return out
}

func nullUUID(v string) any {
	v = strings.TrimSpace(v)
	if v == "" || !validUUID(v) {
		return nil
	}
	return v
}

func nullUUIDPtr(v *string) any {
	if v == nil {
		return nil
	}
	return nullUUID(*v)
}

func nullStr(v string) any {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return v
}

func nullStrPtr(v *string) any {
	if v == nil {
		return nil
	}
	return nullStr(*v)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func mapWrite(op string, err error) error {
	if err == nil {
		return nil
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch pqErr.Code {
		case "23505": // unique_violation
			if strings.Contains(pqErr.Constraint, "alias") {
				return fmt.Errorf("%s: %w", op, ErrDuplicateAlias)
			}
			if strings.Contains(pqErr.Constraint, "target") {
				return fmt.Errorf("%s: %w", op, ErrDuplicateTarget)
			}
			return fmt.Errorf("%s: %w", op, ErrConflict)
		case "23503":
			return fmt.Errorf("%s: %w", op, ErrNotFound)
		case "23514":
			return fmt.Errorf("%s: %w", op, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}
