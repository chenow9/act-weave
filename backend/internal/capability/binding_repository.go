package capability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const bindingColumns = `
	b.workspace_id,b.agent_id,b.capability_id,b.version_policy,b.pinned_release_id,
	b.connection_id,b.execution_policy_id,b.enabled,b.config_overrides,b.bound_by,
	b.created_at,b.updated_at,b.lock_version
`

func (r *Repository) Bind(ctx context.Context, input BindInput) (Binding, error) {
	input = normalizeBinding(input)
	if !validBinding(input) {
		return Binding{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Binding{}, fmt.Errorf("begin capability binding transaction: %w", err)
	}
	defer tx.Rollback()
	var agentStatus, capabilityStatus, capabilityKind string
	var activeReleaseID sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT a.status,c.status,c.kind,c.active_release_id
		FROM agents a
		JOIN capabilities c ON c.workspace_id=a.workspace_id
		WHERE a.workspace_id=$1 AND a.id=$2 AND c.id=$3
		  AND a.deleted_at IS NULL AND c.deleted_at IS NULL
		FOR UPDATE OF a,c
	`, input.WorkspaceID, input.AgentID, input.CapabilityID).Scan(
		&agentStatus, &capabilityStatus, &capabilityKind, &activeReleaseID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Binding{}, ErrNotFound
	}
	if err != nil {
		return Binding{}, fmt.Errorf("lock capability binding targets: %w", err)
	}
	if agentStatus != "ACTIVE" || capabilityStatus != "ACTIVE" {
		return Binding{}, ErrUnavailable
	}
	// Formal agent_capability_bindings require a published release (D12 / P3.3).
	// WORKFLOW capabilities are created with the Draft and only gain
	// active_release_id on publish — reject unpublished Draft binds as 4xx.
	if capabilityKind == "WORKFLOW" && !activeReleaseID.Valid {
		return Binding{}, ErrUnavailable
	}
	if input.VersionPolicy == "FOLLOW_ACTIVE" && !activeReleaseID.Valid {
		// TOOL (and any other kind) FOLLOW_ACTIVE also needs an active release.
		return Binding{}, ErrUnavailable
	}
	if input.VersionPolicy == "PINNED" {
		var available bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM capability_releases
			WHERE workspace_id=$1 AND capability_id=$2 AND id=$3 AND retired_at IS NULL)
		`, input.WorkspaceID, input.CapabilityID, input.PinnedReleaseID).Scan(&available); err != nil {
			return Binding{}, fmt.Errorf("validate pinned capability release: %w", err)
		}
		if !available {
			return Binding{}, ErrUnavailable
		}
	}
	if input.ConnectionID != nil {
		var exists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM service_connections
			WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL)
		`, input.WorkspaceID, input.ConnectionID).Scan(&exists); err != nil {
			return Binding{}, fmt.Errorf("validate binding connection: %w", err)
		}
		if !exists {
			return Binding{}, ErrNotFound
		}
	}
	// When enabling, enforce case-insensitive combined namespace vs internal
	// bindings and A2A remotes for this agent (same workspace+caller).
	if input.Enabled {
		if err := assertCapabilityCallableNamespaceFree(ctx, tx, input); err != nil {
			return Binding{}, err
		}
	}
	var currentLock int64
	err = tx.QueryRowContext(ctx, `
		SELECT lock_version FROM agent_capability_bindings
		WHERE workspace_id=$1 AND agent_id=$2 AND capability_id=$3 FOR UPDATE
	`, input.WorkspaceID, input.AgentID, input.CapabilityID).Scan(&currentLock)
	switch {
	case errors.Is(err, sql.ErrNoRows) && input.ExpectedLockVersion != 0:
		return Binding{}, ErrConflict
	case errors.Is(err, sql.ErrNoRows):
		value, err := scanBinding(tx.QueryRowContext(ctx, `
			INSERT INTO agent_capability_bindings AS b(
				workspace_id,agent_id,capability_id,version_policy,pinned_release_id,
				connection_id,execution_policy_id,enabled,config_overrides,bound_by
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			RETURNING `+bindingColumns,
			input.WorkspaceID, input.AgentID, input.CapabilityID, input.VersionPolicy,
			input.PinnedReleaseID, input.ConnectionID, input.ExecutionPolicyID,
			input.Enabled, []byte(input.ConfigOverrides), input.BoundBy))
		if err != nil {
			return Binding{}, mapWrite("create capability binding", err)
		}
		if err := tx.Commit(); err != nil {
			return Binding{}, mapWrite("commit capability binding", err)
		}
		return value, nil
	case err != nil:
		return Binding{}, fmt.Errorf("lock capability binding: %w", err)
	case currentLock != input.ExpectedLockVersion:
		return Binding{}, ErrConflict
	}
	value, err := scanBinding(tx.QueryRowContext(ctx, `
		UPDATE agent_capability_bindings b
		SET version_policy=$4,pinned_release_id=$5,connection_id=$6,
			execution_policy_id=$7,enabled=$8,config_overrides=$9,bound_by=$10,
			updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND agent_id=$2 AND capability_id=$3 AND lock_version=$11
		RETURNING `+bindingColumns,
		input.WorkspaceID, input.AgentID, input.CapabilityID, input.VersionPolicy,
		input.PinnedReleaseID, input.ConnectionID, input.ExecutionPolicyID,
		input.Enabled, []byte(input.ConfigOverrides), input.BoundBy, input.ExpectedLockVersion))
	if err != nil {
		return Binding{}, mapWrite("update capability binding", err)
	}
	if err := tx.Commit(); err != nil {
		return Binding{}, mapWrite("commit capability binding", err)
	}
	return value, nil
}

// assertCapabilityCallableNamespaceFree locks and checks that the capability's
// callable_name does not collide with enabled internal agent_delegation_bindings
// or agent_a2a_remote_bindings for the same agent (case-insensitive).
func assertCapabilityCallableNamespaceFree(ctx context.Context, tx *sql.Tx, input BindInput) error {
	// Resolve callable name from the release that will be effective.
	var callable string
	err := tx.QueryRowContext(ctx, `
		SELECT lower(btrim(cr.callable_name))
		FROM capabilities c
		JOIN capability_releases cr
		  ON cr.workspace_id = c.workspace_id AND cr.capability_id = c.id
		 AND cr.id = COALESCE($3::uuid, c.active_release_id)
		WHERE c.workspace_id=$1 AND c.id=$2 AND c.deleted_at IS NULL
	`, input.WorkspaceID, input.CapabilityID, input.PinnedReleaseID).Scan(&callable)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnavailable
	}
	if err != nil {
		return fmt.Errorf("resolve capability callable_name: %w", err)
	}
	if callable == "" {
		return ErrInvalid
	}
	key := input.WorkspaceID + "|" + input.AgentID + "|" + callable
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, key); err != nil {
		return fmt.Errorf("capability namespace lock: %w", err)
	}
	var n int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)::int FROM agent_delegation_bindings
		WHERE workspace_id=$1 AND caller_agent_id=$2 AND enabled AND deleted_at IS NULL
		  AND lower(btrim(callable_name)) = $3
	`, input.WorkspaceID, input.AgentID, callable).Scan(&n); err != nil {
		return fmt.Errorf("capability vs internal binding namespace: %w", err)
	}
	if n > 0 {
		return ErrConflict
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)::int FROM agent_a2a_remote_bindings
		WHERE workspace_id=$1 AND caller_agent_id=$2 AND enabled AND deleted_at IS NULL
		  AND lower(btrim(callable_name)) = $3
	`, input.WorkspaceID, input.AgentID, callable).Scan(&n); err != nil {
		return fmt.Errorf("capability vs a2a remote namespace: %w", err)
	}
	if n > 0 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) Unbind(ctx context.Context, workspaceID, agentID, capabilityID string, expectedLockVersion int64) error {
	if !validUUID(workspaceID) || !validUUID(agentID) || !validUUID(capabilityID) || expectedLockVersion < 1 {
		return ErrInvalid
	}
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM agent_capability_bindings
		WHERE workspace_id=$1 AND agent_id=$2 AND capability_id=$3 AND lock_version=$4
	`, workspaceID, agentID, capabilityID, expectedLockVersion)
	if err != nil {
		return mapWrite("unbind capability", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read unbound capability count: %w", err)
	}
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) ListBindings(ctx context.Context, workspaceID, agentID string) ([]Binding, error) {
	if !validUUID(workspaceID) || !validUUID(agentID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+bindingColumns+` FROM agent_capability_bindings b
		WHERE b.workspace_id=$1 AND b.agent_id=$2
		ORDER BY b.created_at,b.capability_id
	`, workspaceID, agentID)
	if err != nil {
		return nil, fmt.Errorf("list capability bindings: %w", err)
	}
	defer rows.Close()
	values := make([]Binding, 0)
	for rows.Next() {
		value, err := scanBinding(rows)
		if err != nil {
			return nil, fmt.Errorf("scan capability binding: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// AgentModelConfigID loads the Agent's current model without importing the
// agent package. Missing or deleted Agents are ErrNotFound.
func (r *Repository) AgentModelConfigID(ctx context.Context, workspaceID, agentID string) (string, error) {
	if !validUUID(workspaceID) || !validUUID(agentID) {
		return "", ErrInvalid
	}
	var modelConfigID string
	err := r.db.QueryRowContext(ctx, `
		SELECT model_config_id::text FROM agents
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL
	`, workspaceID, agentID).Scan(&modelConfigID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get agent model config: %w", err)
	}
	return modelConfigID, nil
}

func (r *Repository) ListEnabledSelections(ctx context.Context, workspaceID, agentID string) ([]BindingSelection, error) {
	if !validUUID(workspaceID) || !validUUID(agentID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT capability_id,version_policy,pinned_release_id,connection_id
		FROM agent_capability_bindings
		WHERE workspace_id=$1 AND agent_id=$2 AND enabled
		ORDER BY capability_id
	`, workspaceID, agentID)
	if err != nil {
		return nil, fmt.Errorf("list enabled binding selections: %w", err)
	}
	defer rows.Close()
	values := make([]BindingSelection, 0)
	for rows.Next() {
		var value BindingSelection
		if err := rows.Scan(&value.CapabilityID, &value.VersionPolicy, &value.PinnedReleaseID, &value.ConnectionID); err != nil {
			return nil, fmt.Errorf("scan binding selection: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func scanBinding(row rowScanner) (Binding, error) {
	var value Binding
	var overrides []byte
	err := row.Scan(&value.WorkspaceID, &value.AgentID, &value.CapabilityID,
		&value.VersionPolicy, &value.PinnedReleaseID, &value.ConnectionID,
		&value.ExecutionPolicyID, &value.Enabled, &overrides, &value.BoundBy,
		&value.CreatedAt, &value.UpdatedAt, &value.LockVersion)
	value.ConfigOverrides = append(json.RawMessage(nil), overrides...)
	return value, err
}

func normalizeBinding(input BindInput) BindInput {
	input.WorkspaceID, input.AgentID, input.CapabilityID = strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.AgentID), strings.TrimSpace(input.CapabilityID)
	input.VersionPolicy, input.BoundBy = strings.TrimSpace(input.VersionPolicy), strings.TrimSpace(input.BoundBy)
	input.PinnedReleaseID, input.ConnectionID = optionalID(input.PinnedReleaseID), optionalID(input.ConnectionID)
	input.ExecutionPolicyID = optionalID(input.ExecutionPolicyID)
	if len(input.ConfigOverrides) == 0 {
		input.ConfigOverrides = json.RawMessage(`{}`)
	}
	return input
}

func validBinding(input BindInput) bool {
	if !validUUID(input.WorkspaceID) || !validUUID(input.AgentID) || !validUUID(input.CapabilityID) ||
		!validUUID(input.BoundBy) || !validOptionalID(input.ConnectionID) ||
		!validOptionalID(input.ExecutionPolicyID) || input.ExpectedLockVersion < 0 ||
		!validJSONObject(input.ConfigOverrides) {
		return false
	}
	return (input.VersionPolicy == "FOLLOW_ACTIVE" && input.PinnedReleaseID == nil) ||
		(input.VersionPolicy == "PINNED" && input.PinnedReleaseID != nil && validUUID(*input.PinnedReleaseID))
}

func validOptionalID(value *string) bool { return value == nil || validUUID(*value) }
func optionalID(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
