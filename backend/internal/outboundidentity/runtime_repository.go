package outboundidentity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RuntimeRepository persists instance / affinity routing metadata only.
// It never stores Token, Vault keys, Token expiry, or credential locators.
type RuntimeRepository struct {
	db  *sql.DB
	now func() time.Time
}

// NewRuntimeRepository constructs a repository over the shared DB.
func NewRuntimeRepository(db *sql.DB) (*RuntimeRepository, error) {
	if db == nil {
		return nil, errors.New("outbound runtime repository database is required")
	}
	return &RuntimeRepository{
		db:  db,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

// WithClock overrides time for tests.
func (r *RuntimeRepository) WithClock(now func() time.Time) *RuntimeRepository {
	if r == nil {
		return nil
	}
	if now != nil {
		r.now = now
	}
	return r
}

// RegisterInstance upserts this boot's routing registration.
// internalAddress and publicKey must come from deploy/boot config, never request input.
func (r *RuntimeRepository) RegisterInstance(ctx context.Context, inst RuntimeInstance) error {
	if r == nil || r.db == nil || ctx == nil {
		return ErrCredentialInvalid
	}
	inst.InstanceID = strings.TrimSpace(inst.InstanceID)
	inst.BootID = strings.TrimSpace(inst.BootID)
	inst.InternalAddress = strings.TrimSpace(inst.InternalAddress)
	if inst.WorkspaceScope == "" {
		inst.WorkspaceScope = "cluster"
	}
	if !inst.Valid() {
		return ErrCredentialInvalid
	}
	if err := validateInternalAddress(inst.InternalAddress); err != nil {
		return err
	}
	now := r.now().UTC()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO outbound_runtime_instances (
			instance_id, boot_id, workspace_scope, internal_address, routing_public_key,
			heartbeat_at, draining, started_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,FALSE,$6,$6)
		ON CONFLICT (instance_id, boot_id) DO UPDATE SET
			workspace_scope = EXCLUDED.workspace_scope,
			internal_address = EXCLUDED.internal_address,
			routing_public_key = EXCLUDED.routing_public_key,
			heartbeat_at = EXCLUDED.heartbeat_at,
			draining = FALSE,
			updated_at = EXCLUDED.updated_at
	`, inst.InstanceID, inst.BootID, inst.WorkspaceScope, inst.InternalAddress,
		inst.RoutingPublicKey, now)
	if err != nil {
		return fmt.Errorf("register runtime instance: %w", err)
	}
	return nil
}

// Heartbeat refreshes liveness for this boot.
func (r *RuntimeRepository) Heartbeat(ctx context.Context, instanceID, bootID string) error {
	if r == nil || r.db == nil || ctx == nil {
		return ErrCredentialInvalid
	}
	instanceID = strings.TrimSpace(instanceID)
	bootID = strings.TrimSpace(bootID)
	if instanceID == "" || bootID == "" {
		return ErrCredentialInvalid
	}
	now := r.now().UTC()
	res, err := r.db.ExecContext(ctx, `
		UPDATE outbound_runtime_instances
		SET heartbeat_at = $3, updated_at = $3
		WHERE instance_id = $1 AND boot_id = $2
	`, instanceID, bootID, now)
	if err != nil {
		return fmt.Errorf("runtime heartbeat: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCredentialExpired
	}
	return nil
}

// SetDraining marks the boot as draining (no new affinity claims should target it).
func (r *RuntimeRepository) SetDraining(ctx context.Context, instanceID, bootID string, draining bool) error {
	if r == nil || r.db == nil || ctx == nil {
		return ErrCredentialInvalid
	}
	instanceID = strings.TrimSpace(instanceID)
	bootID = strings.TrimSpace(bootID)
	if instanceID == "" || bootID == "" {
		return ErrCredentialInvalid
	}
	now := r.now().UTC()
	res, err := r.db.ExecContext(ctx, `
		UPDATE outbound_runtime_instances
		SET draining = $3, updated_at = $4
		WHERE instance_id = $1 AND boot_id = $2
	`, instanceID, bootID, draining, now)
	if err != nil {
		return fmt.Errorf("runtime set draining: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCredentialExpired
	}
	return nil
}

// GetInstance loads one registration row.
func (r *RuntimeRepository) GetInstance(ctx context.Context, instanceID, bootID string) (RuntimeInstance, error) {
	var zero RuntimeInstance
	if r == nil || r.db == nil || ctx == nil {
		return zero, ErrCredentialInvalid
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT instance_id, boot_id, workspace_scope, internal_address, routing_public_key,
		       heartbeat_at, draining, started_at, updated_at
		FROM outbound_runtime_instances
		WHERE instance_id = $1 AND boot_id = $2
	`, strings.TrimSpace(instanceID), strings.TrimSpace(bootID))
	return scanRuntimeInstance(row)
}

// DeleteInstance removes a boot registration. Affinities referencing it must be
// cleaned first (or this fails on FK RESTRICT) — callers reconcile stale first.
func (r *RuntimeRepository) DeleteInstance(ctx context.Context, instanceID, bootID string) error {
	if r == nil || r.db == nil || ctx == nil {
		return ErrCredentialInvalid
	}
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM outbound_runtime_instances
		WHERE instance_id = $1 AND boot_id = $2
	`, strings.TrimSpace(instanceID), strings.TrimSpace(bootID))
	if err != nil {
		return fmt.Errorf("delete runtime instance: %w", err)
	}
	return nil
}

// ClaimAffinity CAS-inserts affinity for a REQUEST_PASSTHROUGH root.
// Idempotent for the same owner; concurrent different owners lose with ErrAffinityConflict.
func (r *RuntimeRepository) ClaimAffinity(ctx context.Context, req AffinityClaimRequest) (RuntimeAffinity, error) {
	var zero RuntimeAffinity
	if r == nil || r.db == nil || ctx == nil {
		return zero, ErrCredentialInvalid
	}
	if !req.RequiresPassthrough {
		// Pure BROKER_OBO / no-cred must not create affinity.
		return zero, ErrCredentialInvalid
	}
	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	req.RootScopeID = strings.TrimSpace(req.RootScopeID)
	req.OwnerInstanceID = strings.TrimSpace(req.OwnerInstanceID)
	req.OwnerBootID = strings.TrimSpace(req.OwnerBootID)
	if req.RootScopeType == RootScopeDebugAttachment {
		return zero, ErrCredentialInvalid
	}
	if !req.RootScopeType.Valid() || req.WorkspaceID == "" || req.RootScopeID == "" ||
		req.OwnerInstanceID == "" || req.OwnerBootID == "" || req.RootDeadlineAt.IsZero() {
		return zero, ErrCredentialInvalid
	}
	if _, err := uuid.Parse(req.WorkspaceID); err != nil {
		return zero, ErrCredentialInvalid
	}
	if _, err := uuid.Parse(req.RootScopeID); err != nil {
		return zero, ErrCredentialInvalid
	}
	now := r.now().UTC()
	deadline := req.RootDeadlineAt.UTC()
	if !deadline.After(now) {
		return zero, ErrCredentialInvalid
	}
	maxDeadline := now.Add(DefaultAffinityMaxDeadline)
	if deadline.After(maxDeadline) {
		deadline = maxDeadline
	}

	// Ensure owner instance row exists (FK).
	if _, err := r.GetInstance(ctx, req.OwnerInstanceID, req.OwnerBootID); err != nil {
		return zero, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, fmt.Errorf("claim affinity begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Serialize claims for this root.
	if _, err := tx.ExecContext(ctx, `
		SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2 || ':' || $3))
	`, req.WorkspaceID, string(req.RootScopeType), req.RootScopeID); err != nil {
		return zero, fmt.Errorf("claim affinity lock: %w", err)
	}

	var existing RuntimeAffinity
	row := tx.QueryRowContext(ctx, `
		SELECT workspace_id, root_scope_type, root_scope_id::TEXT, owner_instance_id, owner_boot_id,
		       root_deadline_at, created_at, updated_at
		FROM outbound_runtime_affinities
		WHERE workspace_id = $1 AND root_scope_type = $2 AND root_scope_id = $3
		FOR UPDATE
	`, req.WorkspaceID, string(req.RootScopeType), req.RootScopeID)
	scanErr := scanRuntimeAffinity(row, &existing)
	if scanErr == nil {
		if existing.OwnedBy(req.OwnerInstanceID, req.OwnerBootID) {
			// Same owner re-claim: renew deadline if later.
			if deadline.After(existing.RootDeadlineAt) {
				if _, err := tx.ExecContext(ctx, `
					UPDATE outbound_runtime_affinities
					SET root_deadline_at = $4, updated_at = $5
					WHERE workspace_id = $1 AND root_scope_type = $2 AND root_scope_id = $3
				`, req.WorkspaceID, string(req.RootScopeType), req.RootScopeID, deadline, now); err != nil {
					return zero, fmt.Errorf("renew affinity: %w", err)
				}
				existing.RootDeadlineAt = deadline
				existing.UpdatedAt = now
			}
			if err := tx.Commit(); err != nil {
				return zero, err
			}
			return existing, nil
		}
		// Different owner — do not steal.
		return zero, ErrAffinityConflict
	}
	if !errors.Is(scanErr, sql.ErrNoRows) {
		return zero, scanErr
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO outbound_runtime_affinities (
			workspace_id, root_scope_type, root_scope_id,
			owner_instance_id, owner_boot_id, root_deadline_at, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$7)
	`, req.WorkspaceID, string(req.RootScopeType), req.RootScopeID,
		req.OwnerInstanceID, req.OwnerBootID, deadline, now)
	if err != nil {
		return zero, fmt.Errorf("insert affinity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return zero, err
	}
	return RuntimeAffinity{
		WorkspaceID: req.WorkspaceID, RootScopeType: req.RootScopeType, RootScopeID: req.RootScopeID,
		OwnerInstanceID: req.OwnerInstanceID, OwnerBootID: req.OwnerBootID,
		RootDeadlineAt: deadline, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// GetAffinity loads affinity metadata for a root (no credential inference).
func (r *RuntimeRepository) GetAffinity(ctx context.Context, workspaceID string, rootType RootScopeType, rootID string) (RuntimeAffinity, error) {
	var zero RuntimeAffinity
	if r == nil || r.db == nil || ctx == nil {
		return zero, ErrCredentialInvalid
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT workspace_id, root_scope_type, root_scope_id::TEXT, owner_instance_id, owner_boot_id,
		       root_deadline_at, created_at, updated_at
		FROM outbound_runtime_affinities
		WHERE workspace_id = $1 AND root_scope_type = $2 AND root_scope_id = $3
	`, strings.TrimSpace(workspaceID), string(rootType), strings.TrimSpace(rootID))
	var out RuntimeAffinity
	if err := scanRuntimeAffinity(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return zero, ErrAffinityNotFound
		}
		return zero, err
	}
	return out, nil
}

// DeleteAffinity removes affinity for a root. Idempotent.
func (r *RuntimeRepository) DeleteAffinity(ctx context.Context, workspaceID string, rootType RootScopeType, rootID string) error {
	if r == nil || r.db == nil || ctx == nil {
		return ErrCredentialInvalid
	}
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM outbound_runtime_affinities
		WHERE workspace_id = $1 AND root_scope_type = $2 AND root_scope_id = $3
	`, strings.TrimSpace(workspaceID), string(rootType), strings.TrimSpace(rootID))
	if err != nil {
		return fmt.Errorf("delete affinity: %w", err)
	}
	return nil
}

// DeleteAffinitiesForOwner removes all affinities owned by a boot (shutdown / fail-close).
func (r *RuntimeRepository) DeleteAffinitiesForOwner(ctx context.Context, instanceID, bootID string) (int64, error) {
	if r == nil || r.db == nil || ctx == nil {
		return 0, ErrCredentialInvalid
	}
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM outbound_runtime_affinities
		WHERE owner_instance_id = $1 AND owner_boot_id = $2
	`, strings.TrimSpace(instanceID), strings.TrimSpace(bootID))
	if err != nil {
		return 0, fmt.Errorf("delete affinities for owner: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ListStaleAffinities returns affinities whose owner is missing/stale/draining
// or whose root deadline has passed. Used by the reconciler only — never to steal claims.
func (r *RuntimeRepository) ListStaleAffinities(ctx context.Context, staleAfter time.Duration, limit int) ([]RuntimeAffinity, error) {
	if r == nil || r.db == nil || ctx == nil {
		return nil, ErrCredentialInvalid
	}
	if limit < 1 || limit > 1000 {
		return nil, ErrCredentialInvalid
	}
	if staleAfter <= 0 {
		staleAfter = DefaultHeartbeatStaleAfter
	}
	now := r.now().UTC()
	cutoff := now.Add(-staleAfter)
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.workspace_id, a.root_scope_type, a.root_scope_id::TEXT, a.owner_instance_id, a.owner_boot_id,
		       a.root_deadline_at, a.created_at, a.updated_at
		FROM outbound_runtime_affinities a
		LEFT JOIN outbound_runtime_instances i
		  ON i.instance_id = a.owner_instance_id AND i.boot_id = a.owner_boot_id
		WHERE a.root_deadline_at <= $1
		   OR i.instance_id IS NULL
		   OR i.draining = TRUE
		   OR i.heartbeat_at <= $2
		ORDER BY a.root_deadline_at, a.workspace_id, a.root_scope_id
		LIMIT $3
	`, now, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("list stale affinities: %w", err)
	}
	defer rows.Close()
	out := make([]RuntimeAffinity, 0)
	for rows.Next() {
		var a RuntimeAffinity
		if err := scanRuntimeAffinity(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// scanners
// ---------------------------------------------------------------------------

type scannable interface {
	Scan(dest ...any) error
}

func scanRuntimeInstance(row scannable) (RuntimeInstance, error) {
	var inst RuntimeInstance
	err := row.Scan(
		&inst.InstanceID, &inst.BootID, &inst.WorkspaceScope, &inst.InternalAddress, &inst.RoutingPublicKey,
		&inst.HeartbeatAt, &inst.Draining, &inst.StartedAt, &inst.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RuntimeInstance{}, ErrCredentialExpired
		}
		return RuntimeInstance{}, fmt.Errorf("scan runtime instance: %w", err)
	}
	return inst, nil
}

func scanRuntimeAffinity(row scannable, out *RuntimeAffinity) error {
	var rootType string
	err := row.Scan(
		&out.WorkspaceID, &rootType, &out.RootScopeID, &out.OwnerInstanceID, &out.OwnerBootID,
		&out.RootDeadlineAt, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return err
	}
	out.RootScopeType = RootScopeType(rootType)
	return nil
}

// validateInternalAddress rejects obviously request-like or empty addresses.
// Full mTLS / allowlist enforcement lives at the forwarder (workload identity).
func validateInternalAddress(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" || len(addr) > 512 {
		return ErrCredentialInvalid
	}
	// Reject userinfo / credential-looking forms.
	if strings.Contains(addr, "@") {
		return ErrCredentialInvalid
	}
	lower := strings.ToLower(addr)
	if strings.HasPrefix(lower, "http://") {
		// Internal mesh should be https or raw host:port — plain http is rejected.
		return ErrCredentialInvalid
	}
	return nil
}

// Sentinel errors for affinity routing (mapped to stable codes where needed).
var (
	// ErrAffinityNotFound means no passthrough affinity exists for the root.
	ErrAffinityNotFound = errors.New("outbound runtime affinity not found")
	// ErrAffinityConflict means another owner already holds the root affinity.
	ErrAffinityConflict = errors.New("outbound runtime affinity owned by another instance")
)
