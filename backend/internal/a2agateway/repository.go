package a2agateway

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("a2agateway: database required")
	}
	return &Repository{db: db}, nil
}

// DB exposes the underlying pool for principal-scoped protocol TaskStore adapters.
func (r *Repository) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

const exposureColumns = `
	e.id, e.workspace_id, e.agent_id, e.public_name, e.public_description,
	e.enabled, e.card_overrides, e.auth_mode, e.version,
	COALESCE(e.created_by::text,''), COALESCE(e.updated_by::text,''),
	e.created_at, e.updated_at, e.deleted_at
`

const remoteColumns = `
	r.id, r.workspace_id, r.caller_agent_id, r.callable_name, r.description,
	r.endpoint_url, COALESCE(r.agent_card_url,''), r.allowed_hosts,
	COALESCE(r.auth_secret_ref,''), r.timeout_ms, r.enabled, r.version,
	COALESCE(r.created_by::text,''), COALESCE(r.updated_by::text,''),
	r.created_at, r.updated_at, r.deleted_at
`

type CreateExposureInput struct {
	ID, WorkspaceID, AgentID, PublicName, PublicDescription, AuthMode, ActorID string
	Enabled                                                                    bool
	CardOverrides                                                              json.RawMessage
}

type UpdateExposureInput struct {
	WorkspaceID, ExposureID, ActorID string
	ExpectedVersion                  int64
	PublicName, PublicDescription    *string
	Enabled                          *bool
	AuthMode                         *string
	CardOverrides                    json.RawMessage
}

type CreateRemoteInput struct {
	ID, WorkspaceID, CallerAgentID, CallableName, Description string
	EndpointURL, AgentCardURL, AuthSecretRef, ActorID         string
	AllowedHosts                                              []string
	TimeoutMs                                                 int
	Enabled                                                   bool
}

type UpdateRemoteInput struct {
	WorkspaceID, BindingID, ActorID string
	ExpectedVersion                 int64
	CallableName, Description       *string
	EndpointURL, AgentCardURL       *string
	AuthSecretRef                   *string
	AllowedHosts                    []string
	TimeoutMs                       *int
	Enabled                         *bool
}

func (r *Repository) CreateExposure(ctx context.Context, in CreateExposureInput) (Exposure, error) {
	in.ID = strings.TrimSpace(in.ID)
	in.WorkspaceID = strings.TrimSpace(in.WorkspaceID)
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.PublicName = strings.TrimSpace(in.PublicName)
	in.AuthMode = strings.ToUpper(strings.TrimSpace(in.AuthMode))
	if in.AuthMode == "" {
		in.AuthMode = AuthModeAgentAccess
	}
	if !validUUID(in.ID) || !validUUID(in.WorkspaceID) || !validUUID(in.AgentID) || in.PublicName == "" {
		return Exposure{}, ErrInvalid
	}
	if in.AuthMode != AuthModeAgentAccess && in.AuthMode != AuthModeNone {
		return Exposure{}, ErrInvalid
	}
	card := mustObject(in.CardOverrides)
	value, err := scanExposure(r.db.QueryRowContext(ctx, `
		INSERT INTO agent_a2a_exposures AS e(
			id, workspace_id, agent_id, public_name, public_description,
			enabled, card_overrides, auth_mode, version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,1,$9,$9)
		RETURNING `+exposureColumns,
		in.ID, in.WorkspaceID, in.AgentID, in.PublicName, strings.TrimSpace(in.PublicDescription),
		in.Enabled, []byte(card), in.AuthMode, nullUUID(in.ActorID)))
	return value, mapWrite("create exposure", err)
}

func (r *Repository) UpdateExposure(ctx context.Context, in UpdateExposureInput) (Exposure, error) {
	if !validUUID(in.WorkspaceID) || !validUUID(in.ExposureID) || in.ExpectedVersion < 1 {
		return Exposure{}, ErrInvalid
	}
	cur, err := r.GetExposure(ctx, in.WorkspaceID, in.ExposureID)
	if err != nil {
		return Exposure{}, err
	}
	if cur.Version != in.ExpectedVersion {
		return Exposure{}, ErrConflict
	}
	if in.PublicName != nil {
		cur.PublicName = strings.TrimSpace(*in.PublicName)
	}
	if in.PublicDescription != nil {
		cur.PublicDescription = strings.TrimSpace(*in.PublicDescription)
	}
	if in.Enabled != nil {
		cur.Enabled = *in.Enabled
	}
	if in.AuthMode != nil {
		cur.AuthMode = strings.ToUpper(strings.TrimSpace(*in.AuthMode))
	}
	card := cur.CardOverrides
	if len(in.CardOverrides) > 0 {
		card = mustObject(in.CardOverrides)
	}
	if cur.PublicName == "" || (cur.AuthMode != AuthModeAgentAccess && cur.AuthMode != AuthModeNone) {
		return Exposure{}, ErrInvalid
	}
	value, err := scanExposure(r.db.QueryRowContext(ctx, `
		UPDATE agent_a2a_exposures e SET
			public_name=$4, public_description=$5, enabled=$6, card_overrides=$7,
			auth_mode=$8, updated_by=$9, updated_at=clock_timestamp(), version=version+1
		WHERE workspace_id=$1 AND id=$2 AND version=$3 AND deleted_at IS NULL
		RETURNING `+exposureColumns,
		in.WorkspaceID, in.ExposureID, in.ExpectedVersion,
		cur.PublicName, cur.PublicDescription, cur.Enabled, []byte(mustObject(card)),
		cur.AuthMode, nullUUID(in.ActorID)))
	if errors.Is(err, sql.ErrNoRows) {
		return Exposure{}, ErrConflict
	}
	return value, mapWrite("update exposure", err)
}

func (r *Repository) SoftDisableExposure(ctx context.Context, workspaceID, id string, version int64, actorID string) error {
	// Soft-disable: enabled=false only; keep row visible for edit/re-enable.
	res, err := r.db.ExecContext(ctx, `
		UPDATE agent_a2a_exposures SET enabled=FALSE,
			updated_by=$4, updated_at=clock_timestamp(), version=version+1
		WHERE workspace_id=$1 AND id=$2 AND version=$3 AND deleted_at IS NULL
	`, workspaceID, id, version, nullUUID(actorID))
	if err != nil {
		return mapWrite("disable exposure", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) GetExposure(ctx context.Context, workspaceID, id string) (Exposure, error) {
	v, err := scanExposure(r.db.QueryRowContext(ctx, `
		SELECT `+exposureColumns+` FROM agent_a2a_exposures e
		WHERE e.workspace_id=$1 AND e.id=$2 AND e.deleted_at IS NULL
	`, workspaceID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Exposure{}, ErrNotFound
	}
	return v, err
}

func (r *Repository) GetExposureByAgent(ctx context.Context, workspaceID, agentID string) (Exposure, error) {
	v, err := scanExposure(r.db.QueryRowContext(ctx, `
		SELECT `+exposureColumns+` FROM agent_a2a_exposures e
		WHERE e.workspace_id=$1 AND e.agent_id=$2 AND e.deleted_at IS NULL
	`, workspaceID, agentID))
	if errors.Is(err, sql.ErrNoRows) {
		return Exposure{}, ErrNotFound
	}
	return v, err
}

func (r *Repository) ListExposures(ctx context.Context, workspaceID string) ([]Exposure, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+exposureColumns+` FROM agent_a2a_exposures e
		WHERE e.workspace_id=$1 AND e.deleted_at IS NULL
		ORDER BY e.created_at ASC, e.id ASC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Exposure, 0)
	for rows.Next() {
		v, err := scanExposure(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repository) ListEnabledExposures(ctx context.Context, workspaceID string) ([]Exposure, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+exposureColumns+` FROM agent_a2a_exposures e
		WHERE e.workspace_id=$1 AND e.enabled AND e.deleted_at IS NULL
		ORDER BY e.public_name ASC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Exposure, 0)
	for rows.Next() {
		v, err := scanExposure(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repository) CreateRemote(ctx context.Context, in CreateRemoteInput) (RemoteBinding, error) {
	in.ID = strings.TrimSpace(in.ID)
	in.WorkspaceID = strings.TrimSpace(in.WorkspaceID)
	in.CallerAgentID = strings.TrimSpace(in.CallerAgentID)
	in.CallableName = normalizeName(in.CallableName)
	in.EndpointURL = strings.TrimSpace(in.EndpointURL)
	in.AgentCardURL = strings.TrimSpace(in.AgentCardURL)
	in.AuthSecretRef = strings.TrimSpace(in.AuthSecretRef)
	if in.TimeoutMs <= 0 {
		in.TimeoutMs = 60000
	}
	if !validUUID(in.ID) || !validUUID(in.WorkspaceID) || !validUUID(in.CallerAgentID) ||
		in.CallableName == "" || in.EndpointURL == "" {
		return RemoteBinding{}, ErrInvalid
	}
	if err := validateAuthSecretRef(in.WorkspaceID, in.AuthSecretRef); err != nil {
		return RemoteBinding{}, err
	}
	if err := validateRemoteAllowlist(ctx, in.EndpointURL, in.AgentCardURL, in.AllowedHosts); err != nil {
		return RemoteBinding{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return RemoteBinding{}, err
	}
	defer tx.Rollback()
	// Disabled remotes do not occupy namespace; enable/re-enable checks on write.
	if in.Enabled {
		if err := lockAndAssertCallableNamespace(ctx, tx, in.WorkspaceID, in.CallerAgentID, in.CallableName, ""); err != nil {
			return RemoteBinding{}, err
		}
	}
	hosts, _ := json.Marshal(in.AllowedHosts)
	if hosts == nil {
		hosts = []byte("[]")
	}
	value, err := scanRemote(tx.QueryRowContext(ctx, `
		INSERT INTO agent_a2a_remote_bindings AS r(
			id, workspace_id, caller_agent_id, callable_name, description,
			endpoint_url, agent_card_url, allowed_hosts, auth_secret_ref,
			timeout_ms, enabled, version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,1,$12,$12)
		RETURNING `+remoteColumns,
		in.ID, in.WorkspaceID, in.CallerAgentID, in.CallableName, strings.TrimSpace(in.Description),
		in.EndpointURL, nullStr(in.AgentCardURL), hosts, nullStr(in.AuthSecretRef),
		in.TimeoutMs, in.Enabled, nullUUID(in.ActorID)))
	if err != nil {
		return RemoteBinding{}, mapWrite("create remote binding", err)
	}
	if err := tx.Commit(); err != nil {
		return RemoteBinding{}, mapWrite("commit remote", err)
	}
	return value, nil
}

func (r *Repository) UpdateRemote(ctx context.Context, in UpdateRemoteInput) (RemoteBinding, error) {
	if !validUUID(in.WorkspaceID) || !validUUID(in.BindingID) || in.ExpectedVersion < 1 {
		return RemoteBinding{}, ErrInvalid
	}
	cur, err := r.GetRemote(ctx, in.WorkspaceID, in.BindingID)
	if err != nil {
		return RemoteBinding{}, err
	}
	if cur.Version != in.ExpectedVersion {
		return RemoteBinding{}, ErrConflict
	}
	if in.CallableName != nil {
		cur.CallableName = normalizeName(*in.CallableName)
	}
	if in.Description != nil {
		cur.Description = strings.TrimSpace(*in.Description)
	}
	if in.EndpointURL != nil {
		cur.EndpointURL = strings.TrimSpace(*in.EndpointURL)
	}
	if in.AgentCardURL != nil {
		cur.AgentCardURL = strings.TrimSpace(*in.AgentCardURL)
	}
	if in.AuthSecretRef != nil {
		cur.AuthSecretRef = strings.TrimSpace(*in.AuthSecretRef)
	}
	if in.AllowedHosts != nil {
		cur.AllowedHosts = in.AllowedHosts
	}
	if in.TimeoutMs != nil {
		cur.TimeoutMs = *in.TimeoutMs
	}
	if in.Enabled != nil {
		cur.Enabled = *in.Enabled
	}
	if err := validateAuthSecretRef(in.WorkspaceID, cur.AuthSecretRef); err != nil {
		return RemoteBinding{}, err
	}
	if err := validateRemoteAllowlist(ctx, cur.EndpointURL, cur.AgentCardURL, cur.AllowedHosts); err != nil {
		return RemoteBinding{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return RemoteBinding{}, err
	}
	defer tx.Rollback()
	// Disabled remotes may edit fields without reclaiming active namespace;
	// re-enable / enabled writes must check.
	if cur.Enabled {
		if err := lockAndAssertCallableNamespace(ctx, tx, in.WorkspaceID, cur.CallerAgentID, cur.CallableName, in.BindingID); err != nil {
			return RemoteBinding{}, err
		}
	}
	hosts, _ := json.Marshal(cur.AllowedHosts)
	value, err := scanRemote(tx.QueryRowContext(ctx, `
		UPDATE agent_a2a_remote_bindings r SET
			callable_name=$4, description=$5, endpoint_url=$6, agent_card_url=$7,
			allowed_hosts=$8, auth_secret_ref=$9, timeout_ms=$10, enabled=$11,
			updated_by=$12, updated_at=clock_timestamp(), version=version+1
		WHERE workspace_id=$1 AND id=$2 AND version=$3 AND deleted_at IS NULL
		RETURNING `+remoteColumns,
		in.WorkspaceID, in.BindingID, in.ExpectedVersion,
		cur.CallableName, cur.Description, cur.EndpointURL, nullStr(cur.AgentCardURL),
		hosts, nullStr(cur.AuthSecretRef), cur.TimeoutMs, cur.Enabled, nullUUID(in.ActorID)))
	if errors.Is(err, sql.ErrNoRows) {
		return RemoteBinding{}, ErrConflict
	}
	if err != nil {
		return RemoteBinding{}, mapWrite("update remote", err)
	}
	if err := tx.Commit(); err != nil {
		return RemoteBinding{}, mapWrite("commit remote update", err)
	}
	return value, nil
}

// validateAuthSecretRef enforces secret:<workspaceUUID>:<secretUUID> and that the
// embedded workspace matches the binding workspace (fail-closed cross-tenant).
// Empty ref is allowed (no outbound auth).
func validateAuthSecretRef(bindingWorkspaceID, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	parts := strings.Split(ref, ":")
	if len(parts) != 3 || parts[0] != "secret" {
		return fmt.Errorf("%w: authSecretRef must be secret:<workspaceId>:<secretId>", ErrInvalid)
	}
	refWS, secretID := strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
	if !validUUID(refWS) || !validUUID(secretID) {
		return fmt.Errorf("%w: authSecretRef workspace/secret must be UUIDs", ErrInvalid)
	}
	if !strings.EqualFold(refWS, strings.TrimSpace(bindingWorkspaceID)) {
		return fmt.Errorf("%w: authSecretRef workspace must match binding workspace", ErrInvalid)
	}
	return nil
}

// validateRemoteAllowlist requires non-empty allowedHosts and that both endpoint
// and optional agentCardURL pass SSRF + explicit host allowlist coverage.
func validateRemoteAllowlist(ctx context.Context, endpointURL, agentCardURL string, allowedHosts []string) error {
	hosts := make([]string, 0, len(allowedHosts))
	for _, h := range allowedHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			hosts = append(hosts, h)
		}
	}
	if len(hosts) == 0 {
		return fmt.Errorf("%w: allowedHosts must be non-empty", ErrInvalid)
	}
	if err := ValidateOutboundURLCtx(ctx, endpointURL, hosts, EgressPolicy{}); err != nil {
		return err
	}
	if card := strings.TrimSpace(agentCardURL); card != "" {
		if err := ValidateOutboundURLCtx(ctx, card, hosts, EgressPolicy{}); err != nil {
			return fmt.Errorf("agentCardURL: %w", err)
		}
	}
	return nil
}

// lockAndAssertCallableNamespace serializes and checks internal+remote+capability
// callable names for a caller (case-insensitive). excludeRemoteID skips self on update.
// Only ENABLED peers block; disabled remotes may edit freely until re-enable.
func lockAndAssertCallableNamespace(ctx context.Context, tx *sql.Tx, workspaceID, callerAgentID, callableName, excludeRemoteID string) error {
	key := workspaceID + "|" + callerAgentID + "|" + strings.ToLower(strings.TrimSpace(callableName))
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, key); err != nil {
		return err
	}
	var n int
	// Internal bindings
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)::int FROM agent_delegation_bindings
		WHERE workspace_id=$1 AND caller_agent_id=$2 AND lower(btrim(callable_name))=lower(btrim($3))
		  AND enabled AND deleted_at IS NULL
	`, workspaceID, callerAgentID, callableName).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrNamespaceConflict
	}
	// Other remotes
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)::int FROM agent_a2a_remote_bindings
		WHERE workspace_id=$1 AND caller_agent_id=$2 AND lower(btrim(callable_name))=lower(btrim($3))
		  AND enabled AND deleted_at IS NULL
		  AND ($4 = '' OR id::text <> $4)
	`, workspaceID, callerAgentID, callableName, strings.TrimSpace(excludeRemoteID)).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrConflict
	}
	// Capability bindings for this agent.
	if err := tx.QueryRowContext(ctx, `
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
	`, workspaceID, callerAgentID, callableName).Scan(&n); err != nil {
		return fmt.Errorf("capability namespace check: %w", err)
	}
	if n > 0 {
		return ErrNamespaceConflict
	}
	return nil
}

func (r *Repository) SoftDisableRemote(ctx context.Context, workspaceID, id string, version int64, actorID string) error {
	// Soft-disable: enabled=false only; keep row visible for edit/re-enable.
	res, err := r.db.ExecContext(ctx, `
		UPDATE agent_a2a_remote_bindings SET enabled=FALSE,
			updated_by=$4, updated_at=clock_timestamp(), version=version+1
		WHERE workspace_id=$1 AND id=$2 AND version=$3 AND deleted_at IS NULL
	`, workspaceID, id, version, nullUUID(actorID))
	if err != nil {
		return mapWrite("disable remote", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) GetRemote(ctx context.Context, workspaceID, id string) (RemoteBinding, error) {
	v, err := scanRemote(r.db.QueryRowContext(ctx, `
		SELECT `+remoteColumns+` FROM agent_a2a_remote_bindings r
		WHERE r.workspace_id=$1 AND r.id=$2 AND r.deleted_at IS NULL
	`, workspaceID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return RemoteBinding{}, ErrNotFound
	}
	return v, err
}

func (r *Repository) ListRemotes(ctx context.Context, workspaceID, callerAgentID string) ([]RemoteBinding, error) {
	var rows *sql.Rows
	var err error
	if strings.TrimSpace(callerAgentID) == "" {
		rows, err = r.db.QueryContext(ctx, `
			SELECT `+remoteColumns+` FROM agent_a2a_remote_bindings r
			WHERE r.workspace_id=$1 AND r.deleted_at IS NULL
			ORDER BY r.created_at ASC, r.id ASC
		`, workspaceID)
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT `+remoteColumns+` FROM agent_a2a_remote_bindings r
			WHERE r.workspace_id=$1 AND r.caller_agent_id=$2 AND r.deleted_at IS NULL
			ORDER BY r.created_at ASC, r.id ASC
		`, workspaceID, callerAgentID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RemoteBinding, 0)
	for rows.Next() {
		v, err := scanRemote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repository) ListEnabledRemotesForCaller(ctx context.Context, workspaceID, callerAgentID string) ([]RemoteBinding, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+remoteColumns+` FROM agent_a2a_remote_bindings r
		WHERE r.workspace_id=$1 AND r.caller_agent_id=$2 AND r.enabled AND r.deleted_at IS NULL
		ORDER BY r.callable_name ASC
	`, workspaceID, callerAgentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RemoteBinding, 0)
	for rows.Next() {
		v, err := scanRemote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type scannable interface{ Scan(dest ...any) error }

func scanExposure(row scannable) (Exposure, error) {
	var e Exposure
	var createdBy, updatedBy string
	var deleted sql.NullTime
	var card []byte
	err := row.Scan(
		&e.ID, &e.WorkspaceID, &e.AgentID, &e.PublicName, &e.PublicDescription,
		&e.Enabled, &card, &e.AuthMode, &e.Version, &createdBy, &updatedBy,
		&e.CreatedAt, &e.UpdatedAt, &deleted,
	)
	if err != nil {
		return Exposure{}, err
	}
	e.CardOverrides = append(json.RawMessage(nil), card...)
	e.CreatedBy, e.UpdatedBy = createdBy, updatedBy
	e.CreatedAt, e.UpdatedAt = e.CreatedAt.UTC(), e.UpdatedAt.UTC()
	if deleted.Valid {
		t := deleted.Time.UTC()
		e.DeletedAt = &t
	}
	return e, nil
}

func scanRemote(row scannable) (RemoteBinding, error) {
	var b RemoteBinding
	var createdBy, updatedBy string
	var deleted sql.NullTime
	var hosts []byte
	err := row.Scan(
		&b.ID, &b.WorkspaceID, &b.CallerAgentID, &b.CallableName, &b.Description,
		&b.EndpointURL, &b.AgentCardURL, &hosts, &b.AuthSecretRef, &b.TimeoutMs,
		&b.Enabled, &b.Version, &createdBy, &updatedBy, &b.CreatedAt, &b.UpdatedAt, &deleted,
	)
	if err != nil {
		return RemoteBinding{}, err
	}
	_ = json.Unmarshal(hosts, &b.AllowedHosts)
	if b.AllowedHosts == nil {
		b.AllowedHosts = []string{}
	}
	b.CreatedBy, b.UpdatedBy = createdBy, updatedBy
	b.CreatedAt, b.UpdatedAt = b.CreatedAt.UTC(), b.UpdatedAt.UTC()
	if deleted.Valid {
		t := deleted.Time.UTC()
		b.DeletedAt = &t
	}
	return b, nil
}

func validUUID(v string) bool {
	_, err := uuid.Parse(strings.TrimSpace(v))
	return err == nil
}

func mustObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return json.RawMessage(`{}`)
	}
	out, _ := json.Marshal(m)
	return out
}

func nullUUID(v string) any {
	if !validUUID(v) {
		return nil
	}
	return strings.TrimSpace(v)
}

func nullStr(v string) any {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return v
}

func normalizeName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else if r == '-' || r == ' ' {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func mapWrite(op string, err error) error {
	if err == nil {
		return nil
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch pqErr.Code {
		case "23505":
			return fmt.Errorf("%s: %w", op, ErrConflict)
		case "23503":
			return fmt.Errorf("%s: %w", op, ErrNotFound)
		case "23514":
			return fmt.Errorf("%s: %w", op, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

// --- Inbound durable idempotency + finalize outbox ---

// InboundTask is a durable mapping from external A2A identity → internal run.
// ActorType+ActorID bind authority to the authenticated principal (AuthMode NONE
// uses SYSTEM + exposure id).
type InboundTask struct {
	ID                string
	WorkspaceID       string
	ExposureID        string
	AgentID           string
	ActorType         string
	ActorID           string
	ExternalKey       string
	ExternalTaskID    string
	ExternalContextID string
	ExternalMessageID string
	// RequestHash fingerprints the user text; claim replay must match.
	RequestHash  string
	RunID        string
	DelegationID string
	Status       string
	// ExecuteGeneration increments on each successful claim/reclaim.
	ExecuteGeneration int64
	ExecuteOwner      string
	ExecuteToken      string
}

// ExecutionLease is returned when a worker owns model dispatch.
type ExecutionLease struct {
	Owned      bool
	Owner      string
	Token      string
	Generation int64
}

// ExternalIdempotencyKey builds the durable external identity for inbound claim.
// When messageID is present (A2A message semantics), the key is contextId|messageId
// and does NOT include server-generated taskId — two replicas may mint different
// taskIds but still hit the same durable task when context+message+body match.
// Without messageID, fall back to task|context for legacy callers.
func ExternalIdempotencyKey(taskID, contextID, messageID string) string {
	taskID = strings.TrimSpace(taskID)
	contextID = strings.TrimSpace(contextID)
	messageID = strings.TrimSpace(messageID)
	if messageID != "" {
		return contextID + "|" + messageID
	}
	if taskID == "" && contextID == "" {
		return ""
	}
	return taskID + "|" + contextID + "|"
}

// RequestBodyHash returns a stable SHA-256 hex fingerprint of inbound user text.
func RequestBodyHash(userText string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(userText)))
	return hex.EncodeToString(sum[:])
}

// ClaimInboundTask inserts or returns existing mapping for (exposure, external_key).
// Prefer ClaimInboundTaskWithPrepare for production inbound (no shadow runs).
// When existing, request_hash must match (body identity); mismatch → ErrConflict
// without mutating the durable row. replay=true returns prior run_id.
func (r *Repository) ClaimInboundTask(ctx context.Context, in InboundTask) (task InboundTask, replay bool, err error) {
	return r.ClaimInboundTaskWithPrepare(ctx, in, nil)
}

// ClaimPrepareFunc inserts the authority agent_run on the claim transaction.
// It must use only the provided *sql.Tx for durable writes (and any reads that
// would otherwise check out another pool connection). Freezing / catalog reads
// that need a separate connection must happen before ClaimInboundTaskWithPrepare
// so MaxOpenConns=1 cannot deadlock under the claim advisory lock.
//
// On any error the claim transaction rolls back, so a successful prepare that is
// followed by task/alias/commit failure leaves no visible orphan agent_run.
type ClaimPrepareFunc func(ctx context.Context, tx *sql.Tx) (runID string, err error)

// ClaimInboundTaskWithPrepare serializes claim under a workspace/exposure/key
// advisory lock so concurrent retries do not create shadow agent_runs:
//   - existing + hash match → replay (no prepare); registers TaskID alias
//   - existing + hash mismatch → ErrConflict (no prepare, no new run)
//   - missing → prepare(tx) once, then insert authority task + alias (same TX)
//
// prepare may be nil when in.RunID is already set (tests / direct claim).
func (r *Repository) ClaimInboundTaskWithPrepare(
	ctx context.Context,
	in InboundTask,
	prepare ClaimPrepareFunc,
) (task InboundTask, replay bool, err error) {
	in.WorkspaceID = strings.TrimSpace(in.WorkspaceID)
	in.ExposureID = strings.TrimSpace(in.ExposureID)
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.ActorType = strings.ToUpper(strings.TrimSpace(in.ActorType))
	in.ActorID = strings.TrimSpace(in.ActorID)
	in.ExternalKey = strings.TrimSpace(in.ExternalKey)
	in.RunID = strings.TrimSpace(in.RunID)
	in.RequestHash = strings.TrimSpace(in.RequestHash)
	in.ExternalTaskID = strings.TrimSpace(in.ExternalTaskID)
	in.ExternalContextID = strings.TrimSpace(in.ExternalContextID)
	in.ExternalMessageID = strings.TrimSpace(in.ExternalMessageID)
	if in.ID == "" {
		in.ID = uuid.Must(uuid.NewV7()).String()
	}
	if in.ExternalKey == "" || in.WorkspaceID == "" || in.ExposureID == "" {
		return InboundTask{}, false, ErrInvalid
	}
	if in.ActorType == "" || in.ActorID == "" {
		return InboundTask{}, false, fmt.Errorf("%w: inbound claim requires actor_type and actor_id", ErrInvalid)
	}
	if in.Status == "" {
		in.Status = "RUNNING"
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return InboundTask{}, false, err
	}
	defer tx.Rollback()

	// Serialize concurrent claim/prepare for this principal+external identity.
	lockKey := "inbound_claim|" + in.WorkspaceID + "|" + in.ExposureID + "|" + in.ActorType + "|" + in.ActorID + "|" + in.ExternalKey
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, lockKey); err != nil {
		return InboundTask{}, false, mapWrite("inbound claim lock", err)
	}

	var del sql.NullString
	var existingHash string
	err = tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, exposure_id, agent_id, actor_type, actor_id, external_key,
		       external_task_id, external_context_id, external_message_id,
		       COALESCE(request_hash,''), run_id, COALESCE(delegation_id::text,''), status
		FROM agent_a2a_inbound_tasks
		WHERE workspace_id=$1 AND exposure_id=$2 AND actor_type=$3 AND actor_id=$4 AND external_key=$5
		FOR UPDATE
	`, in.WorkspaceID, in.ExposureID, in.ActorType, in.ActorID, in.ExternalKey).Scan(
		&task.ID, &task.WorkspaceID, &task.ExposureID, &task.AgentID, &task.ActorType, &task.ActorID, &task.ExternalKey,
		&task.ExternalTaskID, &task.ExternalContextID, &task.ExternalMessageID,
		&existingHash, &task.RunID, &del, &task.Status,
	)
	if err == nil {
		task.RequestHash = existingHash
		if del.Valid {
			task.DelegationID = del.String
		}
		// Body identity: mismatch must not prepare a candidate or mutate authority.
		if in.RequestHash != existingHash {
			if in.RequestHash != "" || existingHash != "" {
				return InboundTask{}, false, fmt.Errorf("%w: inbound request body hash mismatch (idempotent key collision)", ErrConflict)
			}
		}
		if in.RequestHash != "" && existingHash == "" {
			_, _ = tx.ExecContext(ctx, `
				UPDATE agent_a2a_inbound_tasks SET request_hash=$3
				WHERE workspace_id=$1 AND id=$2 AND COALESCE(request_hash,'')='' AND status='RUNNING'
			`, in.WorkspaceID, task.ID, in.RequestHash)
			task.RequestHash = in.RequestHash
		}
		// Register any newly observed external TaskID as alias of the authority row.
		if err := registerInboundTaskAliasTx(ctx, tx, in.WorkspaceID, in.ExposureID, in.ActorType, in.ActorID, in.ExternalTaskID, task.ID); err != nil {
			return InboundTask{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return InboundTask{}, false, mapWrite("commit inbound claim replay", err)
		}
		return task, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return InboundTask{}, false, err
	}

	// No durable mapping: create authoritative run + task + alias in one TX.
	// prepare must write agent_runs on this same tx (atomic with task/alias).
	runID := in.RunID
	if runID == "" {
		if prepare == nil {
			return InboundTask{}, false, ErrInvalid
		}
		runID, err = prepare(ctx, tx)
		if err != nil || strings.TrimSpace(runID) == "" {
			if err == nil {
				err = ErrInvalid
			}
			return InboundTask{}, false, err
		}
		runID = strings.TrimSpace(runID)
	}
	in.RunID = runID

	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_a2a_inbound_tasks(
			id, workspace_id, exposure_id, agent_id, actor_type, actor_id, external_key,
			external_task_id, external_context_id, external_message_id,
			request_hash, run_id, delegation_id, status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULL,$13)
	`, in.ID, in.WorkspaceID, in.ExposureID, in.AgentID, in.ActorType, in.ActorID, in.ExternalKey,
		in.ExternalTaskID, in.ExternalContextID, in.ExternalMessageID,
		in.RequestHash, in.RunID, in.Status)
	if err != nil {
		return InboundTask{}, false, mapWrite("claim inbound task insert", err)
	}
	if err := registerInboundTaskAliasTx(ctx, tx, in.WorkspaceID, in.ExposureID, in.ActorType, in.ActorID, in.ExternalTaskID, in.ID); err != nil {
		return InboundTask{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return InboundTask{}, false, mapWrite("commit inbound claim", err)
	}
	return in, false, nil
}

// registerInboundTaskAliasTx permanently maps external_task_id → inbound task for one principal.
// Empty externalTaskID is a no-op. Rebinding a TaskID to a different inbound
// task is rejected (tenant/task isolation).
func registerInboundTaskAliasTx(ctx context.Context, tx *sql.Tx, workspaceID, exposureID, actorType, actorID, externalTaskID, inboundTaskID string) error {
	externalTaskID = strings.TrimSpace(externalTaskID)
	actorType = strings.ToUpper(strings.TrimSpace(actorType))
	actorID = strings.TrimSpace(actorID)
	if externalTaskID == "" || workspaceID == "" || exposureID == "" || inboundTaskID == "" {
		return nil
	}
	if actorType == "" || actorID == "" {
		return fmt.Errorf("%w: alias requires actor", ErrInvalid)
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO agent_a2a_inbound_task_aliases(
			workspace_id, exposure_id, actor_type, actor_id, external_task_id, inbound_task_id
		) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (workspace_id, exposure_id, actor_type, actor_id, external_task_id) DO NOTHING
	`, workspaceID, exposureID, actorType, actorID, externalTaskID, inboundTaskID)
	if err != nil {
		return mapWrite("register inbound task alias", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	// Already present: must point at the same inbound task.
	var existing string
	err = tx.QueryRowContext(ctx, `
		SELECT inbound_task_id::text FROM agent_a2a_inbound_task_aliases
		WHERE workspace_id=$1 AND exposure_id=$2 AND actor_type=$3 AND actor_id=$4 AND external_task_id=$5
	`, workspaceID, exposureID, actorType, actorID, externalTaskID).Scan(&existing)
	if err != nil {
		return err
	}
	if existing != inboundTaskID {
		return fmt.Errorf("%w: external task id already bound to another inbound task", ErrConflict)
	}
	return nil
}

// RegisterInboundTaskAlias is the public non-tx form for tests/callers.
func (r *Repository) RegisterInboundTaskAlias(ctx context.Context, workspaceID, exposureID, actorType, actorID, externalTaskID, inboundTaskID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := registerInboundTaskAliasTx(ctx, tx, workspaceID, exposureID, actorType, actorID, externalTaskID, inboundTaskID); err != nil {
		return err
	}
	return tx.Commit()
}

// BindInboundTaskDelegation: RUNNING+NULL→id, or any status same-id idempotent.
// Different id overwrite is rejected without mutating the original binding.
func (r *Repository) BindInboundTaskDelegation(ctx context.Context, workspaceID, taskID, delegationID string) error {
	workspaceID, taskID, delegationID = strings.TrimSpace(workspaceID), strings.TrimSpace(taskID), strings.TrimSpace(delegationID)
	if workspaceID == "" || taskID == "" || delegationID == "" {
		return ErrInvalid
	}
	// Same-id idempotent (including terminal tasks).
	res, err := r.db.ExecContext(ctx, `
		UPDATE agent_a2a_inbound_tasks SET updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=$1 AND id=$2 AND delegation_id=$3
	`, workspaceID, taskID, delegationID)
	if err != nil {
		return mapWrite("bind inbound delegation idempotent", err)
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		return nil
	}
	// NULL → id only while RUNNING.
	res, err = r.db.ExecContext(ctx, `
		UPDATE agent_a2a_inbound_tasks SET delegation_id=$3, updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=$1 AND id=$2 AND status='RUNNING' AND delegation_id IS NULL
	`, workspaceID, taskID, delegationID)
	if err != nil {
		return mapWrite("bind inbound delegation", err)
	}
	n, _ = res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) UpdateInboundTaskStatus(ctx context.Context, workspaceID, taskID, status string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_a2a_inbound_tasks SET status=$3, updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, taskID, status)
	return mapWrite("update inbound task status", err)
}

func (r *Repository) GetInboundTaskByExternalTask(ctx context.Context, workspaceID, externalTaskID string) (InboundTask, error) {
	var task InboundTask
	var del sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, exposure_id, agent_id, external_key,
		       external_task_id, external_context_id, external_message_id,
		       run_id, COALESCE(delegation_id::text,''), status
		FROM agent_a2a_inbound_tasks
		WHERE workspace_id=$1 AND external_task_id=$2
		ORDER BY created_at DESC LIMIT 1
	`, workspaceID, strings.TrimSpace(externalTaskID)).Scan(
		&task.ID, &task.WorkspaceID, &task.ExposureID, &task.AgentID, &task.ExternalKey,
		&task.ExternalTaskID, &task.ExternalContextID, &task.ExternalMessageID,
		&task.RunID, &del, &task.Status,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return InboundTask{}, ErrNotFound
	}
	if err != nil {
		return InboundTask{}, err
	}
	if del.Valid {
		task.DelegationID = del.String
	}
	return task, nil
}

// EnqueueFinalizeOutbox stores a durable finalize payload for recovery.
func (r *Repository) EnqueueFinalizeOutbox(ctx context.Context, workspaceID, delegationID, stepID string, payload json.RawMessage) error {
	id := uuid.Must(uuid.NewV7()).String()
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO agent_run_delegation_finalize_outbox(
			id, workspace_id, delegation_id, step_id, payload, attempts, next_attempt_at
		) VALUES ($1,$2,$3,$4,$5,0,CURRENT_TIMESTAMP)
		ON CONFLICT (workspace_id, delegation_id) DO UPDATE SET
			payload=EXCLUDED.payload, step_id=EXCLUDED.step_id,
			next_attempt_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP,
			last_error=NULL, claimed_until=NULL, claimed_by=''
	`, id, workspaceID, delegationID, nullStr(stepID), []byte(payload))
	return mapWrite("enqueue finalize outbox", err)
}

func (r *Repository) DeleteFinalizeOutbox(ctx context.Context, workspaceID, delegationID string) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM agent_run_delegation_finalize_outbox
		WHERE workspace_id=$1 AND delegation_id=$2
	`, workspaceID, delegationID)
	return err
}

// ClaimInboundExecution takes recoverable ownership with owner/token/lease.
// First claim (generation=0) or reclaim when lease expired and still RUNNING.
// Concurrent claims: only one wins; others get Owned=false.
func (r *Repository) ClaimInboundExecution(ctx context.Context, workspaceID, taskID, owner string, lease time.Duration) (ExecutionLease, error) {
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	// Sub-second leases still need at least 1ms representation for INTERVAL.
	leaseMs := lease.Milliseconds()
	if leaseMs < 1 {
		leaseMs = 1
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = "worker"
	}
	token := uuid.Must(uuid.NewV7()).String()
	// Atomic claim: unowned OR expired lease.
	var gen int64
	err := r.db.QueryRowContext(ctx, `
		UPDATE agent_a2a_inbound_tasks
		SET execute_generation = execute_generation + 1,
		    execute_owner = $3,
		    execute_token = $4,
		    execute_lease_until = NOW() + ($5::text || ' milliseconds')::interval,
		    execute_started_at = COALESCE(execute_started_at, CURRENT_TIMESTAMP),
		    updated_at = CURRENT_TIMESTAMP
		WHERE workspace_id=$1 AND id=$2 AND status = 'RUNNING'
		  AND (
		    execute_generation = 0
		    OR execute_lease_until IS NULL
		    OR execute_lease_until < CURRENT_TIMESTAMP
		  )
		RETURNING execute_generation
	`, workspaceID, taskID, owner, token, leaseMs).Scan(&gen)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutionLease{Owned: false}, nil
	}
	if err != nil {
		return ExecutionLease{}, mapWrite("claim inbound execution", err)
	}
	return ExecutionLease{Owned: true, Owner: owner, Token: token, Generation: gen}, nil
}

// RenewInboundExecutionLease extends lease only for the current unexpired owner+token.
// Expired leases cannot be revived by a stale owner.
func (r *Repository) RenewInboundExecutionLease(ctx context.Context, workspaceID, taskID, owner, token string, lease time.Duration) error {
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	leaseMs := lease.Milliseconds()
	if leaseMs < 1 {
		leaseMs = 1
	}
	owner, token = strings.TrimSpace(owner), strings.TrimSpace(token)
	if owner == "" || token == "" {
		return ErrInvalid
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE agent_a2a_inbound_tasks
		SET execute_lease_until = NOW() + ($5::text || ' milliseconds')::interval,
		    updated_at = CURRENT_TIMESTAMP
		WHERE workspace_id=$1 AND id=$2 AND status='RUNNING'
		  AND execute_owner=$3 AND execute_token=$4
		  AND execute_token <> ''
		  AND execute_lease_until IS NOT NULL
		  AND execute_lease_until >= CURRENT_TIMESTAMP
	`, workspaceID, taskID, owner, token, leaseMs)
	if err != nil {
		return mapWrite("renew inbound lease", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// MarkInboundExecutionFinished completes only when the current owner still holds a
// valid (unexpired) lease on a RUNNING task. Empty token never bypasses proof.
// generation>0 additionally requires matching execute_generation.
func (r *Repository) MarkInboundExecutionFinished(ctx context.Context, workspaceID, taskID, status, owner, token string) error {
	return r.MarkInboundExecutionFinishedGen(ctx, workspaceID, taskID, status, owner, token, 0)
}

// MarkInboundExecutionFinishedGen is the generation-aware fence for terminal task write.
func (r *Repository) MarkInboundExecutionFinishedGen(ctx context.Context, workspaceID, taskID, status, owner, token string, generation int64) error {
	owner, token = strings.TrimSpace(owner), strings.TrimSpace(token)
	if owner == "" || token == "" {
		return ErrInvalid
	}
	status = strings.ToUpper(strings.TrimSpace(status))
	if status == "" {
		return ErrInvalid
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE agent_a2a_inbound_tasks
		SET status=$3, execute_finished_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP,
		    execute_lease_until=NULL
		WHERE workspace_id=$1 AND id=$2 AND status='RUNNING'
		  AND execute_owner=$4 AND execute_token=$5 AND execute_token <> ''
		  AND execute_lease_until IS NOT NULL
		  AND execute_lease_until >= CURRENT_TIMESTAMP
		  AND ($6::bigint = 0 OR execute_generation = $6)
	`, workspaceID, taskID, status, owner, token, generation)
	if err != nil {
		return mapWrite("mark inbound execution finished", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// AssertInboundExecutionHeld returns nil only while owner+token still own a valid lease.
// Used before agent_run / delegation terminal writes so a reclaimed worker cannot finalize.
func (r *Repository) AssertInboundExecutionHeld(ctx context.Context, workspaceID, taskID, owner, token string, generation int64) error {
	owner, token = strings.TrimSpace(owner), strings.TrimSpace(token)
	if owner == "" || token == "" {
		return ErrInvalid
	}
	var ok bool
	err := r.db.QueryRowContext(ctx, `
		SELECT TRUE FROM agent_a2a_inbound_tasks
		WHERE workspace_id=$1 AND id=$2 AND status='RUNNING'
		  AND execute_owner=$3 AND execute_token=$4 AND execute_token <> ''
		  AND execute_lease_until IS NOT NULL
		  AND execute_lease_until >= CURRENT_TIMESTAMP
		  AND ($5::bigint = 0 OR execute_generation = $5)
	`, workspaceID, taskID, owner, token, generation).Scan(&ok)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	return nil
}

// ForceMarkInboundTaskStatus sets terminal status without owner check (cancel path).
// Only non-terminal rows are updated; RowsAffected must be 1 or ErrConflict.
func (r *Repository) ForceMarkInboundTaskStatus(ctx context.Context, workspaceID, taskID, status string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE agent_a2a_inbound_tasks
		SET status=$3, execute_finished_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP,
		    execute_lease_until=NULL
		WHERE workspace_id=$1 AND id=$2 AND status NOT IN ('SUCCEEDED','FAILED','CANCELLED','TIMED_OUT')
	`, workspaceID, taskID, status)
	if err != nil {
		return mapWrite("force mark inbound task", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// GetInboundTaskByExposureTask looks up by exposure + principal + external task id
// (primary column or durable alias). Fail-closed: missing actor → not found.
// Different principals never observe each other's authority rows.
func (r *Repository) GetInboundTaskByExposureTask(ctx context.Context, workspaceID, exposureID, actorType, actorID, externalTaskID string) (InboundTask, error) {
	var task InboundTask
	var del sql.NullString
	var execGen int64
	externalTaskID = strings.TrimSpace(externalTaskID)
	actorType = strings.ToUpper(strings.TrimSpace(actorType))
	actorID = strings.TrimSpace(actorID)
	if actorType == "" || actorID == "" || externalTaskID == "" {
		return InboundTask{}, ErrNotFound
	}
	err := r.db.QueryRowContext(ctx, `
		SELECT t.id, t.workspace_id, t.exposure_id, t.agent_id, t.actor_type, t.actor_id, t.external_key,
		       t.external_task_id, t.external_context_id, t.external_message_id,
		       t.run_id, COALESCE(t.delegation_id::text,''), t.status, t.execute_generation
		FROM agent_a2a_inbound_tasks t
		WHERE t.workspace_id=$1 AND t.exposure_id=$2
		  AND t.actor_type=$3 AND t.actor_id=$4
		  AND (
		    t.external_task_id=$5
		    OR EXISTS (
		      SELECT 1 FROM agent_a2a_inbound_task_aliases a
		      WHERE a.workspace_id=t.workspace_id
		        AND a.exposure_id=t.exposure_id
		        AND a.actor_type=t.actor_type
		        AND a.actor_id=t.actor_id
		        AND a.external_task_id=$5
		        AND a.inbound_task_id=t.id
		    )
		  )
		ORDER BY t.created_at DESC
		LIMIT 1
	`, workspaceID, exposureID, actorType, actorID, externalTaskID).Scan(
		&task.ID, &task.WorkspaceID, &task.ExposureID, &task.AgentID, &task.ActorType, &task.ActorID, &task.ExternalKey,
		&task.ExternalTaskID, &task.ExternalContextID, &task.ExternalMessageID,
		&task.RunID, &del, &task.Status, &execGen,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return InboundTask{}, ErrNotFound
	}
	if err != nil {
		return InboundTask{}, err
	}
	if del.Valid {
		task.DelegationID = del.String
	}
	task.ExecuteGeneration = execGen
	return task, nil
}

// FinalizeOutboxRow is one durable finalize recovery item.
type FinalizeOutboxRow struct {
	ID              string
	WorkspaceID     string
	DelegationID    string
	StepID          string
	Payload         json.RawMessage
	Attempts        int
	ClaimToken      string
	ClaimGeneration int64
	ClaimedBy       string
}

// ClaimFinalizeOutboxBatch claims due rows with SKIP LOCKED lease + claim token.
func (r *Repository) ClaimFinalizeOutboxBatch(ctx context.Context, owner string, limit int, lease time.Duration) ([]FinalizeOutboxRow, error) {
	if limit <= 0 {
		limit = 16
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = "worker"
	}
	// Per-row claim tokens generated in SQL via gen_random_uuid when available;
	// fall back to app-side tokens by claiming then updating — use uuid v7 strings.
	rows, err := r.db.QueryContext(ctx, `
		UPDATE agent_run_delegation_finalize_outbox o
		SET claimed_until = NOW() + ($1::text || ' seconds')::interval,
		    claimed_by = $2,
		    claim_token = md5(random()::text || clock_timestamp()::text || o.id::text),
		    claim_generation = o.claim_generation + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE o.id IN (
			SELECT id FROM agent_run_delegation_finalize_outbox
			WHERE next_attempt_at <= CURRENT_TIMESTAMP
			  AND attempts < 32
			  AND (claimed_until IS NULL OR claimed_until < CURRENT_TIMESTAMP)
			ORDER BY next_attempt_at ASC, created_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT $3
		)
		RETURNING o.id, o.workspace_id, o.delegation_id, COALESCE(o.step_id::text,''), o.payload,
		          o.attempts, o.claim_token, o.claim_generation, o.claimed_by
	`, int(lease.Seconds()), owner, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FinalizeOutboxRow
	for rows.Next() {
		var row FinalizeOutboxRow
		var payload []byte
		if err := rows.Scan(&row.ID, &row.WorkspaceID, &row.DelegationID, &row.StepID, &payload,
			&row.Attempts, &row.ClaimToken, &row.ClaimGeneration, &row.ClaimedBy); err != nil {
			return nil, err
		}
		row.Payload = append(json.RawMessage(nil), payload...)
		out = append(out, row)
	}
	return out, rows.Err()
}

// DeleteFinalizeOutboxClaimed deletes only when claim owner+token still match.
func (r *Repository) DeleteFinalizeOutboxClaimed(ctx context.Context, workspaceID, delegationID, owner, token string) error {
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM agent_run_delegation_finalize_outbox
		WHERE workspace_id=$1 AND delegation_id=$2
		  AND claimed_by=$3 AND claim_token=$4
	`, workspaceID, delegationID, owner, token)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) NackFinalizeOutbox(ctx context.Context, workspaceID, delegationID, lastErr, owner, token string, attempts int) error {
	backoff := time.Duration(20*(attempts+1)) * time.Millisecond
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE agent_run_delegation_finalize_outbox
		SET attempts=$3, last_error=$4,
		    next_attempt_at = CURRENT_TIMESTAMP + ($5::text || ' milliseconds')::interval,
		    claimed_until=NULL, claimed_by='', claim_token='', updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=$1 AND delegation_id=$2
		  AND claimed_by=$6 AND claim_token=$7
	`, workspaceID, delegationID, attempts+1, truncate(lastErr, 500), int(backoff.Milliseconds()), owner, token)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// Ensure time import used.
