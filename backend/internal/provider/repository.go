package provider

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const providerColumns = `
	p.id,p.workspace_id,p.name::TEXT,p.provider_kind,p.driver_key,p.transport,
	p.endpoint_config,p.driver_config,p.outbound_identity_policy_version,p.discovery_mode,p.status,p.last_synced_at,
	p.last_error_code,p.created_by,p.updated_by,p.created_at,p.updated_at,p.lock_version,p.deleted_at`

const assetColumns = `
	a.id,a.workspace_id,a.provider_id,a.asset_kind,a.external_id,a.name,a.description,
	a.input_schema,a.output_schema,a.metadata,a.source_revision,a.source_checksum,
	a.materialized_capability_id,a.status,a.discovered_at,a.last_seen_at`

const syncColumns = `
	r.id,r.workspace_id,r.provider_id,r.status,r.cursor_before,r.cursor_after,
	r.discovered_count,r.changed_count,r.error_summary,r.started_by,r.started_at,r.finished_at`

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("provider repository database is required")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Create(ctx context.Context, input NewProvider) (Provider, error) {
	input = normalizeNew(input)
	if !validNew(input) {
		return Provider{}, ErrInvalid
	}
	value, err := scanProvider(r.db.QueryRowContext(ctx, `
		WITH inserted AS (INSERT INTO capability_providers(
			id,workspace_id,name,provider_kind,driver_key,transport,endpoint_config,
			driver_config,discovery_mode,created_by,updated_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)
		RETURNING *) SELECT `+providerColumns+` FROM inserted p`,
		input.ID, input.WorkspaceID, input.Name, input.Kind, input.DriverKey, input.Transport,
		[]byte(input.EndpointConfig), []byte(input.DriverConfig), input.DiscoveryMode, input.CreatedBy))
	if err != nil {
		return Provider{}, mapWrite("create provider", err)
	}
	return value, nil
}

func (r *Repository) Get(ctx context.Context, workspaceID, providerID string) (Provider, error) {
	if !validUUID(workspaceID) || !validUUID(providerID) {
		return Provider{}, ErrInvalid
	}
	value, err := scanProvider(r.db.QueryRowContext(ctx, `SELECT `+providerColumns+`
		FROM capability_providers p WHERE p.workspace_id=$1 AND p.id=$2 AND p.deleted_at IS NULL`, workspaceID, providerID))
	return value, mapRead("get provider", err)
}

func (r *Repository) List(ctx context.Context, workspaceID string) ([]Provider, error) {
	if !validUUID(workspaceID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+providerColumns+`
		FROM capability_providers p WHERE p.workspace_id=$1 AND p.deleted_at IS NULL ORDER BY p.created_at,p.id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()
	values := make([]Provider, 0)
	for rows.Next() {
		value, err := scanProvider(rows)
		if err != nil {
			return nil, fmt.Errorf("scan provider: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate providers: %w", err)
	}
	return values, nil
}

func (r *Repository) Update(ctx context.Context, workspaceID, providerID string, input UpdateProvider) (Provider, error) {
	input = normalizeUpdate(input)
	if !validUUID(workspaceID) || !validUUID(providerID) || !validUpdate(input) {
		return Provider{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Provider{}, fmt.Errorf("begin update provider: %w", err)
	}
	defer tx.Rollback()
	var currentLock int64
	var currentEndpointConfig, currentDriverConfig []byte
	err = tx.QueryRowContext(ctx, `
		SELECT lock_version, endpoint_config, driver_config
		FROM capability_providers
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL
		FOR UPDATE
	`, workspaceID, providerID).Scan(&currentLock, &currentEndpointConfig, &currentDriverConfig)
	if errors.Is(err, sql.ErrNoRows) {
		return Provider{}, ErrNotFound
	}
	if err != nil {
		return Provider{}, fmt.Errorf("lock provider for update: %w", err)
	}
	if currentLock != input.ExpectedLockVersion {
		return Provider{}, ErrConflict
	}
	executionContractUnchanged := providerExecutionContractEqual(
		currentEndpointConfig, currentDriverConfig, input.EndpointConfig, input.DriverConfig,
	)
	policyBump := 0
	if !executionContractUnchanged {
		policyBump = 1
	}
	value, err := scanProvider(tx.QueryRowContext(ctx, `
		UPDATE capability_providers p SET name=$3,driver_key=$4,transport=$5,
			endpoint_config=$6,driver_config=$7,discovery_mode=$8,status='ACTIVE',
			outbound_identity_policy_version=outbound_identity_policy_version+$11,
			last_error_code=NULL,updated_by=$9,updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL AND lock_version=$10
		RETURNING `+providerColumns, workspaceID, providerID, input.Name, input.DriverKey, input.Transport,
		[]byte(input.EndpointConfig), []byte(input.DriverConfig), input.DiscoveryMode, input.UpdatedBy, input.ExpectedLockVersion, policyBump))
	if err != nil {
		return Provider{}, mapWrite("update provider", err)
	}
	if !executionContractUnchanged {
		if _, err := tx.ExecContext(ctx, `
			UPDATE service_connections
			SET status='UNVERIFIED',last_verified_at=NULL,last_error_code='PROVIDER_CONTRACT_CHANGED',
				updated_at=clock_timestamp(),lock_version=lock_version+1
			WHERE workspace_id=$1 AND provider_id=$2 AND deleted_at IS NULL
		`, workspaceID, providerID); err != nil {
			return Provider{}, mapWrite("invalidate provider connections", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Provider{}, mapWrite("commit provider update", err)
	}
	return value, nil
}

func (r *Repository) SoftDelete(ctx context.Context, workspaceID, providerID, actorID string, lockVersion int64) error {
	if !validUUID(workspaceID) || !validUUID(providerID) || !validUUID(actorID) || lockVersion < 1 {
		return ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin soft delete provider: %w", err)
	}
	defer tx.Rollback()
	var currentLock int64
	var hasActiveConnection bool
	var hasActiveTool bool
	err = tx.QueryRowContext(ctx, `
		SELECT p.lock_version,
			EXISTS(
				SELECT 1 FROM service_connections connection
				WHERE connection.workspace_id=p.workspace_id
				  AND connection.provider_id=p.id
				  AND connection.deleted_at IS NULL
			),
			EXISTS(
				SELECT 1 FROM tools tool
				JOIN capabilities capability
				  ON capability.workspace_id=tool.workspace_id
				 AND capability.id=tool.capability_id
				WHERE tool.workspace_id=p.workspace_id
				  AND tool.provider_id=p.id
				  AND capability.deleted_at IS NULL
			)
		FROM capability_providers p
		WHERE p.workspace_id=$1 AND p.id=$2 AND p.deleted_at IS NULL
		FOR UPDATE OF p
	`, workspaceID, providerID).Scan(&currentLock, &hasActiveConnection, &hasActiveTool)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock provider for delete: %w", err)
	}
	if currentLock != lockVersion || hasActiveConnection || hasActiveTool {
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE capability_providers SET deleted_at=clock_timestamp(),updated_by=$3,
		updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL AND lock_version=$4`, workspaceID, providerID, actorID, lockVersion)
	if err != nil {
		return mapWrite("soft delete provider", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted provider count: %w", err)
	}
	if rows != 1 {
		return ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return mapWrite("commit soft delete provider", err)
	}
	return nil
}

func (r *Repository) ListAssets(ctx context.Context, workspaceID, providerID string) ([]Asset, error) {
	if !validUUID(workspaceID) || !validUUID(providerID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+assetColumns+` FROM provider_assets a
		WHERE a.workspace_id=$1 AND a.provider_id=$2 ORDER BY a.external_id,a.id`, workspaceID, providerID)
	if err != nil {
		return nil, fmt.Errorf("list provider assets: %w", err)
	}
	defer rows.Close()
	values := make([]Asset, 0)
	for rows.Next() {
		value, err := scanAsset(rows)
		if err != nil {
			return nil, fmt.Errorf("scan provider asset: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider assets: %w", err)
	}
	return values, nil
}

func (r *Repository) GetAsset(ctx context.Context, workspaceID, providerID, assetID string) (Asset, error) {
	if !validUUID(workspaceID) || !validUUID(providerID) || !validUUID(assetID) {
		return Asset{}, ErrInvalid
	}
	value, err := scanAsset(r.db.QueryRowContext(ctx, `SELECT `+assetColumns+` FROM provider_assets a
		WHERE a.workspace_id=$1 AND a.provider_id=$2 AND a.id=$3`, workspaceID, providerID, assetID))
	return value, mapRead("get provider asset", err)
}

func (r *Repository) beginSync(ctx context.Context, run SyncRun) (SyncRun, error) {
	value, err := scanSync(r.db.QueryRowContext(ctx, `WITH inserted AS (INSERT INTO provider_sync_runs(
		id,workspace_id,provider_id,status,cursor_before,error_summary,started_by
	)VALUES($1,$2,$3,'RUNNING',$4,'{}',$5) RETURNING *) SELECT `+syncColumns+` FROM inserted r`,
		run.ID, run.WorkspaceID, run.ProviderID, run.CursorBefore, run.StartedBy))
	if err != nil {
		return SyncRun{}, mapWrite("begin provider sync", err)
	}
	return value, nil
}

func (r *Repository) completeSync(ctx context.Context, run SyncRun, assets []Asset) (SyncRun, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return SyncRun{}, fmt.Errorf("begin provider sync completion: %w", err)
	}
	defer tx.Rollback()
	// A successful discovery snapshot is authoritative for non-materialized
	// assets. Mark the previous snapshot stale first; assets seen below are
	// reactivated in the same transaction.
	if _, err := tx.ExecContext(ctx, `
		UPDATE provider_assets SET status='STALE'
		WHERE workspace_id=$1 AND provider_id=$2
		  AND materialized_capability_id IS NULL AND status <> 'STALE'
	`, run.WorkspaceID, run.ProviderID); err != nil {
		return SyncRun{}, mapWrite("mark missing provider assets stale", err)
	}
	changed := 0
	for _, asset := range assets {
		asset.InputSchema = defaultObject(asset.InputSchema)
		asset.OutputSchema = defaultObject(asset.OutputSchema)
		asset.Metadata = defaultObject(asset.Metadata)
		if !validAsset(asset) {
			return SyncRun{}, ErrInvalid
		}
		var oldChecksum string
		err := tx.QueryRowContext(ctx, `SELECT source_checksum FROM provider_assets WHERE provider_id=$1 AND asset_kind=$2 AND external_id=$3`, run.ProviderID, asset.Kind, asset.ExternalID).Scan(&oldChecksum)
		if errors.Is(err, sql.ErrNoRows) {
			changed++
		} else if err != nil {
			return SyncRun{}, fmt.Errorf("read provider asset checksum: %w", err)
		} else if oldChecksum != asset.SourceChecksum {
			changed++
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO provider_assets(
			id,workspace_id,provider_id,asset_kind,external_id,name,description,input_schema,output_schema,
			metadata,source_revision,source_checksum
		)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT(provider_id,asset_kind,external_id) DO UPDATE SET
			name=EXCLUDED.name,description=EXCLUDED.description,input_schema=EXCLUDED.input_schema,
			output_schema=EXCLUDED.output_schema,metadata=EXCLUDED.metadata,source_revision=EXCLUDED.source_revision,
			source_checksum=EXCLUDED.source_checksum,status=CASE WHEN provider_assets.materialized_capability_id IS NULL THEN 'ACTIVE' ELSE 'MATERIALIZED' END,
			last_seen_at=clock_timestamp()`, asset.ID, run.WorkspaceID, run.ProviderID, asset.Kind, asset.ExternalID,
			asset.Name, asset.Description, []byte(asset.InputSchema), []byte(asset.OutputSchema), []byte(asset.Metadata), nullable(asset.SourceRevision), asset.SourceChecksum)
		if err != nil {
			return SyncRun{}, mapWrite("upsert provider asset", err)
		}
	}
	value, err := scanSync(tx.QueryRowContext(ctx, `UPDATE provider_sync_runs r SET status='SUCCEEDED',cursor_after=$2,
		discovered_count=$3,changed_count=$4,finished_at=clock_timestamp() WHERE id=$1 AND status='RUNNING' RETURNING `+syncColumns,
		run.ID, run.CursorAfter, len(assets), changed))
	if err != nil {
		return SyncRun{}, mapWrite("complete provider sync", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE capability_providers SET last_synced_at=clock_timestamp(),last_error_code=NULL,
		status='ACTIVE',updated_at=clock_timestamp(),lock_version=lock_version+1 WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL`, run.WorkspaceID, run.ProviderID); err != nil {
		return SyncRun{}, mapWrite("update synced provider", err)
	}
	if err := tx.Commit(); err != nil {
		return SyncRun{}, mapWrite("commit provider sync", err)
	}
	return value, nil
}

func (r *Repository) failSync(ctx context.Context, run SyncRun, code string) (SyncRun, error) {
	summary, _ := json.Marshal(map[string]string{"code": code})
	value, err := scanSync(r.db.QueryRowContext(ctx, `WITH failed AS (
		UPDATE provider_sync_runs SET status='FAILED',error_summary=$2,finished_at=clock_timestamp()
		WHERE id=$1 AND status='RUNNING' RETURNING *
	), provider_update AS (
		UPDATE capability_providers SET last_error_code=$3,updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$4 AND id=$5 RETURNING id
	) SELECT `+syncColumns+` FROM failed r`, run.ID, summary, code, run.WorkspaceID, run.ProviderID))
	if err != nil {
		return SyncRun{}, mapWrite("fail provider sync", err)
	}
	return value, nil
}

// providerExecutionContractEqual deliberately excludes v2 discovery metadata.
// OpenAPI document availability is a discovery concern and must not invalidate
// verified runtime Connections when service endpoint/authentication semantics
// are unchanged.
func providerExecutionContractEqual(currentEndpoint, currentDriver, nextEndpoint, nextDriver json.RawMessage) bool {
	currentEndpointCanonical, currentEndpointOK := canonicalExecutionEndpoint(currentEndpoint)
	nextEndpointCanonical, nextEndpointOK := canonicalExecutionEndpoint(nextEndpoint)
	currentDriverCanonical, currentDriverOK := canonicalJSONObject(currentDriver)
	nextDriverCanonical, nextDriverOK := canonicalJSONObject(nextDriver)
	return currentEndpointOK && nextEndpointOK && currentDriverOK && nextDriverOK &&
		bytes.Equal(currentEndpointCanonical, nextEndpointCanonical) && bytes.Equal(currentDriverCanonical, nextDriverCanonical)
}

func canonicalExecutionEndpoint(raw json.RawMessage) ([]byte, bool) {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, false
	}
	if schemaVersion, ok := value["schemaVersion"].(float64); ok && schemaVersion == 2 {
		delete(value, "discovery")
		// These are legacy discovery aliases and are ignored by the v2 parser.
		delete(value, "sourceUri")
		delete(value, "sourceRevision")
		delete(value, "baseUrl")
		delete(value, "url")
	}
	encoded, err := json.Marshal(value)
	return encoded, err == nil
}

func canonicalJSONObject(raw json.RawMessage) ([]byte, bool) {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, false
	}
	encoded, err := json.Marshal(value)
	return encoded, err == nil
}

type scanner interface{ Scan(...any) error }

func scanProvider(row scanner) (Provider, error) {
	var v Provider
	var kind string
	var endpoint, driver []byte
	err := row.Scan(&v.ID, &v.WorkspaceID, &v.Name, &kind, &v.DriverKey, &v.Transport, &endpoint, &driver, &v.OutboundIdentityPolicyVersion, &v.DiscoveryMode, &v.Status, &v.LastSyncedAt, &v.LastErrorCode, &v.CreatedBy, &v.UpdatedBy, &v.CreatedAt, &v.UpdatedAt, &v.LockVersion, &v.DeletedAt)
	v.Kind = Kind(kind)
	v.EndpointConfig = append(json.RawMessage(nil), endpoint...)
	v.DriverConfig = append(json.RawMessage(nil), driver...)
	if v.OutboundIdentityPolicyVersion <= 0 {
		v.OutboundIdentityPolicyVersion = 1
	}
	return v, err
}
func scanAsset(row scanner) (Asset, error) {
	var v Asset
	var input, output, metadata []byte
	var revision sql.NullString
	err := row.Scan(&v.ID, &v.WorkspaceID, &v.ProviderID, &v.Kind, &v.ExternalID, &v.Name, &v.Description, &input, &output, &metadata, &revision, &v.SourceChecksum, &v.MaterializedCapabilityID, &v.Status, &v.DiscoveredAt, &v.LastSeenAt)
	v.InputSchema = append(json.RawMessage(nil), input...)
	v.OutputSchema = append(json.RawMessage(nil), output...)
	v.Metadata = append(json.RawMessage(nil), metadata...)
	if revision.Valid {
		v.SourceRevision = revision.String
	}
	return v, err
}
func scanSync(row scanner) (SyncRun, error) {
	var v SyncRun
	var summary []byte
	err := row.Scan(&v.ID, &v.WorkspaceID, &v.ProviderID, &v.Status, &v.CursorBefore, &v.CursorAfter, &v.DiscoveredCount, &v.ChangedCount, &summary, &v.StartedBy, &v.StartedAt, &v.FinishedAt)
	v.ErrorSummary = append(json.RawMessage(nil), summary...)
	return v, err
}

func normalizeNew(v NewProvider) NewProvider {
	v.ID = strings.TrimSpace(v.ID)
	v.WorkspaceID = strings.TrimSpace(v.WorkspaceID)
	v.Name = strings.TrimSpace(v.Name)
	v.DriverKey = strings.TrimSpace(v.DriverKey)
	v.Transport = strings.ToUpper(strings.TrimSpace(v.Transport))
	v.DiscoveryMode = strings.TrimSpace(v.DiscoveryMode)
	// SCHEDULED was exposed by earlier clients. Persist the canonical database
	// value while keeping those clients compatible.
	if v.DiscoveryMode == "SCHEDULED" {
		v.DiscoveryMode = "POLLING"
	}
	v.CreatedBy = strings.TrimSpace(v.CreatedBy)
	v.EndpointConfig = defaultObject(v.EndpointConfig)
	v.DriverConfig = defaultObject(v.DriverConfig)
	return v
}
func normalizeUpdate(v UpdateProvider) UpdateProvider {
	v.Name = strings.TrimSpace(v.Name)
	v.DriverKey = strings.TrimSpace(v.DriverKey)
	v.Transport = strings.ToUpper(strings.TrimSpace(v.Transport))
	v.DiscoveryMode = strings.TrimSpace(v.DiscoveryMode)
	v.UpdatedBy = strings.TrimSpace(v.UpdatedBy)
	v.EndpointConfig = defaultObject(v.EndpointConfig)
	v.DriverConfig = defaultObject(v.DriverConfig)
	return v
}
func validNew(v NewProvider) bool {
	return validUUID(v.ID) && validUUID(v.WorkspaceID) && validUUID(v.CreatedBy) && v.Name != "" && v.Kind != "" && v.DriverKey != "" && v.Transport != "" && validMode(v.DiscoveryMode) && safeObject(v.EndpointConfig) && safeObject(v.DriverConfig)
}
func validUpdate(v UpdateProvider) bool {
	return validUUID(v.UpdatedBy) && v.Name != "" && v.DriverKey != "" && v.Transport != "" && validMode(v.DiscoveryMode) && v.ExpectedLockVersion > 0 && safeObject(v.EndpointConfig) && safeObject(v.DriverConfig)
}
func validAsset(v Asset) bool {
	return validUUID(v.ID) && v.Kind != "" && v.ExternalID != "" && v.Name != "" && v.SourceChecksum != "" && safeObject(v.InputSchema) && safeObject(v.OutputSchema) && safeObject(v.Metadata)
}
func validMode(v string) bool           { return v == "MANUAL" || v == "ON_DEMAND" || v == "POLLING" }
func safeObject(v json.RawMessage) bool { return jsonObject(v) && !containsSensitiveKey(v) }
func defaultObject(v json.RawMessage) json.RawMessage {
	if len(v) == 0 {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), v...)
}
func validUUID(v string) bool { _, err := uuid.Parse(strings.TrimSpace(v)); return err == nil }
func nullable(v string) any {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return v
}
func (r *Repository) classify(ctx context.Context, wid, id string) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM capability_providers WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL)`, wid, id).Scan(&exists); err != nil {
		return fmt.Errorf("classify provider write: %w", err)
	}
	if exists {
		return ErrConflict
	}
	return ErrNotFound
}
func mapRead(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", op, err)
}
func mapWrite(op string, err error) error {
	var pg *pq.Error
	if errors.As(err, &pg) && pg.Code.Class() == "23" {
		if pg.Code == "23505" {
			return fmt.Errorf("%s: %w", op, ErrConflict)
		}
		return fmt.Errorf("%s: %w", op, ErrInvalid)
	}
	return fmt.Errorf("%s: %w", op, err)
}
