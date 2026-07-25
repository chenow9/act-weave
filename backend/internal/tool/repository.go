package tool

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var (
	ErrNotFound  = errors.New("tool resource not found")
	ErrConflict  = errors.New("tool resource conflict")
	ErrInvalid   = errors.New("invalid tool resource")
	ErrImmutable = errors.New("published tool version is immutable")
)

const toolColumns = `
	t.capability_id,t.workspace_id,t.provider_id,t.source_asset_id,t.default_connection_id,
	t.source_endpoint_id,c.name::TEXT,c.slug::TEXT,c.description,c.status,c.active_release_id,
	c.created_by,c.updated_by,c.created_at,c.updated_at,c.lock_version,c.deleted_at
`

const versionColumns = `
	v.id,v.workspace_id,v.capability_id,v.version_no,v.lifecycle_status,v.executor_type,
	v.provider_id,v.provider_asset_id,v.default_connection_id,v.handler_key,v.execution_profile_id,
	v.action_schema_version,v.action_config,v.input_schema,v.output_schema,v.error_mappings,
	v.runtime_policy,v.risk_level,v.side_effect_level,v.requires_confirmation,v.checksum,
	v.created_by,v.updated_by,v.created_at,v.updated_at,v.published_at,v.lock_version
`

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("tool repository database is required")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Create(ctx context.Context, input CreateInput) (Tool, Version, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Tool{}, Version{}, fmt.Errorf("begin create tool transaction: %w", err)
	}
	defer tx.Rollback()
	value, version, err := r.CreateInTransaction(ctx, tx, input)
	if err != nil {
		return Tool{}, Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return Tool{}, Version{}, mapWrite("commit create tool transaction", err)
	}
	return value, version, nil
}

// CreateInTransaction creates a Capability, Tool specialization, and initial
// Draft in the caller's transaction. It lets application services atomically
// compose Tool creation with source-domain updates such as OpenAPI generation.
func (r *Repository) CreateInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	input CreateInput,
) (Tool, Version, error) {
	if tx == nil {
		return Tool{}, Version{}, ErrInvalid
	}
	input = normalizeCreate(input)
	if !validCreate(input) {
		return Tool{}, Version{}, ErrInvalid
	}
	checksum, err := checksumDraft(input.Draft)
	if err != nil {
		return Tool{}, Version{}, ErrInvalid
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO capabilities(
			id,workspace_id,kind,name,slug,description,created_by,updated_by
		) VALUES($1,$2,'TOOL',$3,$4,$5,$6,$6)
	`, input.CapabilityID, input.WorkspaceID, input.Name, input.Slug,
		input.Description, input.CreatedBy); err != nil {
		return Tool{}, Version{}, mapWrite("create tool capability", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tools(
			capability_id,workspace_id,provider_id,source_asset_id,default_connection_id,source_endpoint_id
		) VALUES($1,$2,$3,$4,$5,$6)
	`, input.CapabilityID, input.WorkspaceID, input.ProviderID, input.SourceAssetID,
		input.DefaultConnectionID, input.SourceEndpointID); err != nil {
		return Tool{}, Version{}, mapWrite("create tool specialization", err)
	}
	version, err := insertDraftVersion(ctx, tx, input.InitialVersionID, input.WorkspaceID,
		input.CapabilityID, input.ProviderID, 1, input.Draft, checksum, input.CreatedBy)
	if err != nil {
		return Tool{}, Version{}, err
	}
	value, err := scanTool(tx.QueryRowContext(ctx, `
		SELECT `+toolColumns+`
		FROM tools t JOIN capabilities c
		  ON c.workspace_id=t.workspace_id AND c.id=t.capability_id
		WHERE t.workspace_id=$1 AND t.capability_id=$2
	`, input.WorkspaceID, input.CapabilityID))
	if err != nil {
		return Tool{}, Version{}, fmt.Errorf("read created tool: %w", err)
	}
	return value, version, nil
}

func (r *Repository) Get(ctx context.Context, workspaceID, capabilityID string) (Tool, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) {
		return Tool{}, ErrInvalid
	}
	value, err := scanTool(r.db.QueryRowContext(ctx, `
		SELECT `+toolColumns+`
		FROM tools t JOIN capabilities c
		  ON c.workspace_id=t.workspace_id AND c.id=t.capability_id
		WHERE t.workspace_id=$1 AND t.capability_id=$2 AND c.deleted_at IS NULL
	`, workspaceID, capabilityID))
	return value, mapRead("get tool", err)
}

func (r *Repository) List(ctx context.Context, workspaceID string) ([]Tool, error) {
	if !validUUID(workspaceID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+toolColumns+` FROM tools t
		JOIN capabilities c ON c.workspace_id=t.workspace_id AND c.id=t.capability_id
		WHERE t.workspace_id=$1 AND c.deleted_at IS NULL ORDER BY c.created_at,c.id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	defer rows.Close()
	values := make([]Tool, 0)
	for rows.Next() {
		value, err := scanTool(rows)
		if err != nil {
			return nil, fmt.Errorf("scan tool: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *Repository) SoftDelete(ctx context.Context, workspaceID, capabilityID, deletedBy string, expectedLockVersion int64) error {
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(deletedBy) || expectedLockVersion < 1 {
		return ErrInvalid
	}
	result, err := r.db.ExecContext(ctx, `UPDATE capabilities c SET deleted_at=clock_timestamp(),updated_by=$3,
		updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND kind='TOOL' AND deleted_at IS NULL AND lock_version=$4
		AND NOT EXISTS(SELECT 1 FROM agent_capability_bindings b WHERE b.workspace_id=c.workspace_id AND b.capability_id=c.id)`, workspaceID, capabilityID, deletedBy, expectedLockVersion)
	if err != nil {
		return mapWrite("soft delete tool", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 1 {
		return nil
	}
	value, err := r.Get(ctx, workspaceID, capabilityID)
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if value.LockVersion != expectedLockVersion {
		return ErrConflict
	}
	return ErrConflict
}

func (r *Repository) UpdateMetadata(ctx context.Context, workspaceID, capabilityID string, input MetadataUpdate) (Tool, error) {
	input = normalizeMetadata(input)
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validMetadata(input) {
		return Tool{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Tool{}, fmt.Errorf("begin update tool metadata transaction: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE capabilities SET name=$3,slug=$4,description=$5,status=$6,
			updated_by=$7,updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND kind='TOOL' AND deleted_at IS NULL AND lock_version=$8
	`, workspaceID, capabilityID, input.Name, input.Slug, input.Description,
		input.Status, input.UpdatedBy, input.ExpectedLockVersion)
	if err != nil {
		return Tool{}, mapWrite("update tool capability metadata", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Tool{}, fmt.Errorf("read updated tool metadata count: %w", err)
	}
	if rows != 1 {
		return Tool{}, r.classifyToolWrite(ctx, workspaceID, capabilityID)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE tools SET source_asset_id=$3,default_connection_id=$4
		WHERE workspace_id=$1 AND capability_id=$2
	`, workspaceID, capabilityID, input.SourceAssetID, input.DefaultConnectionID); err != nil {
		return Tool{}, mapWrite("update tool source metadata", err)
	}
	value, err := scanTool(tx.QueryRowContext(ctx, `
		SELECT `+toolColumns+`
		FROM tools t JOIN capabilities c
		  ON c.workspace_id=t.workspace_id AND c.id=t.capability_id
		WHERE t.workspace_id=$1 AND t.capability_id=$2
	`, workspaceID, capabilityID))
	if err != nil {
		return Tool{}, fmt.Errorf("read updated tool: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Tool{}, mapWrite("commit update tool metadata transaction", err)
	}
	return value, nil
}

func (r *Repository) GetVersion(ctx context.Context, workspaceID, capabilityID, versionID string) (Version, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(versionID) {
		return Version{}, ErrInvalid
	}
	value, err := scanVersion(r.db.QueryRowContext(ctx, `
		SELECT `+versionColumns+` FROM tool_versions v
		WHERE v.workspace_id=$1 AND v.capability_id=$2 AND v.id=$3
	`, workspaceID, capabilityID, versionID))
	return value, mapRead("get tool version", err)
}

func (r *Repository) ListVersions(ctx context.Context, workspaceID, capabilityID string) ([]Version, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+versionColumns+` FROM tool_versions v
		WHERE v.workspace_id=$1 AND v.capability_id=$2 ORDER BY v.version_no,v.id
	`, workspaceID, capabilityID)
	if err != nil {
		return nil, fmt.Errorf("list tool versions: %w", err)
	}
	defer rows.Close()
	values := make([]Version, 0)
	for rows.Next() {
		value, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("scan tool version: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *Repository) UpdateDraft(ctx context.Context, workspaceID, capabilityID, versionID string, input DraftUpdate) (Version, error) {
	input = normalizeDraftUpdate(input)
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(versionID) || !validDraftUpdate(input) {
		return Version{}, ErrInvalid
	}
	checksum, err := checksumDraft(input.Spec)
	if err != nil {
		return Version{}, ErrInvalid
	}
	value, err := scanVersion(r.db.QueryRowContext(ctx, `
		UPDATE tool_versions v SET
			lifecycle_status=$4,provider_asset_id=$5,default_connection_id=$6,
			action_schema_version=$7,action_config=$8,input_schema=$9,output_schema=$10,
			error_mappings=$11,runtime_policy=$12,risk_level=$13,side_effect_level=$14,
			requires_confirmation=$15,checksum=$16,updated_by=$17,
			updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND capability_id=$2 AND id=$3
		  AND lifecycle_status<>'PUBLISHED' AND lock_version=$18
		RETURNING `+versionColumns,
		workspaceID, capabilityID, versionID, input.LifecycleStatus,
		input.Spec.ProviderAssetID, input.Spec.DefaultConnectionID,
		input.Spec.ActionSchemaVersion, []byte(input.Spec.ActionConfig),
		[]byte(input.Spec.InputSchema), []byte(input.Spec.OutputSchema),
		[]byte(input.Spec.ErrorMappings), []byte(input.Spec.RuntimePolicy),
		input.Spec.RiskLevel, input.Spec.SideEffectLevel, input.Spec.RequiresConfirmation,
		checksum, input.UpdatedBy, input.ExpectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, r.classifyVersionWrite(ctx, workspaceID, capabilityID, versionID)
	}
	if err != nil {
		return Version{}, mapWrite("update tool draft version", err)
	}
	return value, nil
}

func (r *Repository) CreateDraftFromPublished(
	ctx context.Context,
	workspaceID, capabilityID, sourceVersionID, newVersionID, createdBy string,
) (Version, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(sourceVersionID) ||
		!validUUID(newVersionID) || !validUUID(createdBy) {
		return Version{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Version{}, fmt.Errorf("begin copy tool draft transaction: %w", err)
	}
	defer tx.Rollback()
	var ignored string
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM capabilities
		WHERE workspace_id=$1 AND id=$2 AND kind='TOOL' AND deleted_at IS NULL FOR UPDATE
	`, workspaceID, capabilityID).Scan(&ignored); errors.Is(err, sql.ErrNoRows) {
		return Version{}, ErrNotFound
	} else if err != nil {
		return Version{}, fmt.Errorf("lock tool for draft copy: %w", err)
	}
	var mutableExists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM tool_versions
		WHERE workspace_id=$1 AND capability_id=$2 AND lifecycle_status<>'PUBLISHED')
	`, workspaceID, capabilityID).Scan(&mutableExists); err != nil {
		return Version{}, fmt.Errorf("check existing mutable tool version: %w", err)
	}
	if mutableExists {
		return Version{}, ErrConflict
	}
	var nextVersion int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version_no),0)+1 FROM tool_versions
		WHERE workspace_id=$1 AND capability_id=$2
	`, workspaceID, capabilityID).Scan(&nextVersion); err != nil {
		return Version{}, fmt.Errorf("allocate tool version number: %w", err)
	}
	value, err := scanVersion(tx.QueryRowContext(ctx, `
		INSERT INTO tool_versions AS v(
			id,workspace_id,capability_id,version_no,lifecycle_status,executor_type,
			provider_id,provider_asset_id,default_connection_id,handler_key,execution_profile_id,
			action_schema_version,action_config,input_schema,output_schema,error_mappings,
			runtime_policy,risk_level,side_effect_level,requires_confirmation,checksum,
			created_by,updated_by
		)
		SELECT $4,workspace_id,capability_id,$5,'DRAFT',executor_type,
			provider_id,provider_asset_id,default_connection_id,handler_key,execution_profile_id,
			action_schema_version,action_config,input_schema,output_schema,error_mappings,
			runtime_policy,risk_level,side_effect_level,requires_confirmation,checksum,$6,$6
		FROM tool_versions
		WHERE workspace_id=$1 AND capability_id=$2 AND id=$3 AND lifecycle_status='PUBLISHED'
		RETURNING `+versionColumns,
		workspaceID, capabilityID, sourceVersionID, newVersionID, nextVersion, createdBy))
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	if err != nil {
		return Version{}, mapWrite("copy published tool version", err)
	}
	if err := tx.Commit(); err != nil {
		return Version{}, mapWrite("commit copy tool draft transaction", err)
	}
	return value, nil
}

// ValidateBindingConnection provides the production compatibility check
// required by capability.BindingService.
func (r *Repository) ValidateBindingConnection(ctx context.Context, workspaceID, capabilityID, connectionID string) error {
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(connectionID) {
		return ErrInvalid
	}
	var compatible bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM tools t
			JOIN service_connections c
			  ON c.workspace_id=t.workspace_id AND c.provider_id=t.provider_id
			WHERE t.workspace_id=$1 AND t.capability_id=$2 AND c.id=$3 AND c.deleted_at IS NULL
		)
	`, workspaceID, capabilityID, connectionID).Scan(&compatible); err != nil {
		return fmt.Errorf("validate tool binding connection: %w", err)
	}
	if !compatible {
		return ErrInvalid
	}
	return nil
}

type rowScanner interface{ Scan(...any) error }

func scanTool(row rowScanner) (Tool, error) {
	var value Tool
	err := row.Scan(&value.CapabilityID, &value.WorkspaceID, &value.ProviderID,
		&value.SourceAssetID, &value.DefaultConnectionID, &value.SourceEndpointID,
		&value.Name, &value.Slug, &value.Description, &value.Status, &value.ActiveReleaseID,
		&value.CreatedBy, &value.UpdatedBy, &value.CreatedAt, &value.UpdatedAt,
		&value.LockVersion, &value.DeletedAt)
	return value, err
}

func scanVersion(row rowScanner) (Version, error) {
	var value Version
	var actionConfig, inputSchema, outputSchema, errorMappings, runtimePolicy []byte
	err := row.Scan(&value.ID, &value.WorkspaceID, &value.CapabilityID, &value.VersionNo,
		&value.LifecycleStatus, &value.ExecutorType, &value.ProviderID,
		&value.ProviderAssetID, &value.DefaultConnectionID, &value.HandlerKey,
		&value.ExecutionProfileID, &value.ActionSchemaVersion, &actionConfig,
		&inputSchema, &outputSchema, &errorMappings, &runtimePolicy, &value.RiskLevel,
		&value.SideEffectLevel, &value.RequiresConfirmation, &value.Checksum,
		&value.CreatedBy, &value.UpdatedBy, &value.CreatedAt, &value.UpdatedAt,
		&value.PublishedAt, &value.LockVersion)
	value.ActionConfig = append(json.RawMessage(nil), actionConfig...)
	value.InputSchema = append(json.RawMessage(nil), inputSchema...)
	value.OutputSchema = append(json.RawMessage(nil), outputSchema...)
	value.ErrorMappings = append(json.RawMessage(nil), errorMappings...)
	value.RuntimePolicy = append(json.RawMessage(nil), runtimePolicy...)
	return value, err
}

func insertDraftVersion(ctx context.Context, tx *sql.Tx, id, workspaceID, capabilityID,
	providerID string, versionNo int, spec DraftSpec, checksum, createdBy string,
) (Version, error) {
	value, err := scanVersion(tx.QueryRowContext(ctx, `
		INSERT INTO tool_versions AS v(
			id,workspace_id,capability_id,version_no,lifecycle_status,executor_type,
			provider_id,provider_asset_id,default_connection_id,action_schema_version,
			action_config,input_schema,output_schema,error_mappings,runtime_policy,
			risk_level,side_effect_level,requires_confirmation,checksum,created_by,updated_by
		) VALUES($1,$2,$3,$4,'DRAFT','HTTP',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$18)
		RETURNING `+versionColumns,
		id, workspaceID, capabilityID, versionNo, providerID, spec.ProviderAssetID,
		spec.DefaultConnectionID, spec.ActionSchemaVersion, []byte(spec.ActionConfig),
		[]byte(spec.InputSchema), []byte(spec.OutputSchema), []byte(spec.ErrorMappings),
		[]byte(spec.RuntimePolicy), spec.RiskLevel, spec.SideEffectLevel,
		spec.RequiresConfirmation, checksum, createdBy))
	if err != nil {
		return Version{}, mapWrite("create tool draft version", err)
	}
	return value, nil
}

func normalizeCreate(input CreateInput) CreateInput {
	input.CapabilityID, input.InitialVersionID = strings.TrimSpace(input.CapabilityID), strings.TrimSpace(input.InitialVersionID)
	input.WorkspaceID, input.ProviderID = strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.ProviderID)
	input.SourceAssetID, input.DefaultConnectionID = optionalID(input.SourceAssetID), optionalID(input.DefaultConnectionID)
	input.SourceEndpointID = optionalID(input.SourceEndpointID)
	input.Name, input.Slug = strings.TrimSpace(input.Name), strings.ToLower(strings.TrimSpace(input.Slug))
	input.Description, input.CreatedBy = strings.TrimSpace(input.Description), strings.TrimSpace(input.CreatedBy)
	input.Draft = normalizeDraft(input.Draft)
	return input
}

func validCreate(input CreateInput) bool {
	return validUUID(input.CapabilityID) && validUUID(input.InitialVersionID) &&
		validUUID(input.WorkspaceID) && validUUID(input.ProviderID) && validUUID(input.CreatedBy) &&
		validOptionalID(input.SourceAssetID) && validOptionalID(input.DefaultConnectionID) &&
		validOptionalID(input.SourceEndpointID) && input.Name != "" && input.Slug != "" &&
		validDraft(input.Draft)
}

func normalizeMetadata(input MetadataUpdate) MetadataUpdate {
	input.Name, input.Slug = strings.TrimSpace(input.Name), strings.ToLower(strings.TrimSpace(input.Slug))
	input.Description, input.Status = strings.TrimSpace(input.Description), strings.TrimSpace(input.Status)
	input.SourceAssetID, input.DefaultConnectionID = optionalID(input.SourceAssetID), optionalID(input.DefaultConnectionID)
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	return input
}

func validMetadata(input MetadataUpdate) bool {
	return input.Name != "" && input.Slug != "" &&
		(input.Status == "ACTIVE" || input.Status == "DISABLED") && validUUID(input.UpdatedBy) &&
		validOptionalID(input.SourceAssetID) && validOptionalID(input.DefaultConnectionID) &&
		input.ExpectedLockVersion > 0
}

func normalizeDraftUpdate(input DraftUpdate) DraftUpdate {
	input.Spec = normalizeDraft(input.Spec)
	input.LifecycleStatus, input.UpdatedBy = strings.TrimSpace(input.LifecycleStatus), strings.TrimSpace(input.UpdatedBy)
	return input
}

func validDraftUpdate(input DraftUpdate) bool {
	return (input.LifecycleStatus == "DRAFT" || input.LifecycleStatus == "REVIEW") &&
		validUUID(input.UpdatedBy) && input.ExpectedLockVersion > 0 && validDraft(input.Spec)
}

func normalizeDraft(spec DraftSpec) DraftSpec {
	spec.ProviderAssetID, spec.DefaultConnectionID = optionalID(spec.ProviderAssetID), optionalID(spec.DefaultConnectionID)
	spec.ActionSchemaVersion = strings.TrimSpace(spec.ActionSchemaVersion)
	spec.ActionConfig = normalizeJSONObject(spec.ActionConfig)
	spec.InputSchema = normalizeJSONObject(spec.InputSchema)
	spec.OutputSchema = normalizeJSONObject(spec.OutputSchema)
	spec.ErrorMappings = normalizeJSONObject(spec.ErrorMappings)
	spec.RuntimePolicy = normalizeJSONObject(spec.RuntimePolicy)
	spec.RiskLevel, spec.SideEffectLevel = strings.TrimSpace(spec.RiskLevel), strings.TrimSpace(spec.SideEffectLevel)
	return spec
}

func validDraft(spec DraftSpec) bool {
	return validOptionalID(spec.ProviderAssetID) && validOptionalID(spec.DefaultConnectionID) &&
		spec.ActionSchemaVersion != "" && validJSONObject(spec.ActionConfig) &&
		validJSONObject(spec.InputSchema) && validJSONObject(spec.OutputSchema) &&
		validJSONObject(spec.ErrorMappings) && validJSONObject(spec.RuntimePolicy) &&
		oneOf(spec.RiskLevel, "LOW", "MEDIUM", "HIGH", "CRITICAL") &&
		oneOf(spec.SideEffectLevel, "NONE", "READ", "WRITE", "IRREVERSIBLE") &&
		!containsSensitiveKey(spec.ActionConfig) && !containsSensitiveKey(spec.RuntimePolicy)
}

func checksumDraft(spec DraftSpec) (string, error) {
	payload := struct {
		ProviderAssetID      *string         `json:"providerAssetId,omitempty"`
		DefaultConnectionID  *string         `json:"defaultConnectionId,omitempty"`
		ActionSchemaVersion  string          `json:"actionSchemaVersion"`
		ActionConfig         json.RawMessage `json:"actionConfig"`
		InputSchema          json.RawMessage `json:"inputSchema"`
		OutputSchema         json.RawMessage `json:"outputSchema"`
		ErrorMappings        json.RawMessage `json:"errorMappings"`
		RuntimePolicy        json.RawMessage `json:"runtimePolicy"`
		RiskLevel            string          `json:"riskLevel"`
		SideEffectLevel      string          `json:"sideEffectLevel"`
		RequiresConfirmation bool            `json:"requiresConfirmation"`
	}{
		spec.ProviderAssetID, spec.DefaultConnectionID, spec.ActionSchemaVersion,
		spec.ActionConfig, spec.InputSchema, spec.OutputSchema, spec.ErrorMappings,
		spec.RuntimePolicy, spec.RiskLevel, spec.SideEffectLevel, spec.RequiresConfirmation,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeJSONObject(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	var decoded map[string]any
	if json.Unmarshal(value, &decoded) != nil || decoded == nil {
		return append(json.RawMessage(nil), value...)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return append(json.RawMessage(nil), value...)
	}
	return encoded
}

func validJSONObject(value json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func containsSensitiveKey(value json.RawMessage) bool {
	var decoded any
	if json.Unmarshal(value, &decoded) != nil {
		return true
	}
	var walk func(any) bool
	walk = func(current any) bool {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
				for _, forbidden := range []string{"password", "secretvalue", "tokenvalue", "apikeyvalue", "authorization", "refreshtoken"} {
					if strings.Contains(normalized, forbidden) {
						return true
					}
				}
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(decoded)
}

func (r *Repository) classifyToolWrite(ctx context.Context, workspaceID, capabilityID string) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM capabilities
		WHERE workspace_id=$1 AND id=$2 AND kind='TOOL' AND deleted_at IS NULL)
	`, workspaceID, capabilityID).Scan(&exists); err != nil {
		return fmt.Errorf("classify tool write: %w", err)
	}
	if exists {
		return ErrConflict
	}
	return ErrNotFound
}

func (r *Repository) classifyVersionWrite(ctx context.Context, workspaceID, capabilityID, versionID string) error {
	var status string
	var lockVersion int64
	err := r.db.QueryRowContext(ctx, `
		SELECT lifecycle_status,lock_version FROM tool_versions
		WHERE workspace_id=$1 AND capability_id=$2 AND id=$3
	`, workspaceID, capabilityID, versionID).Scan(&status, &lockVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("classify tool version write: %w", err)
	}
	if status == "PUBLISHED" {
		return ErrImmutable
	}
	return ErrConflict
}

func validUUID(value string) bool        { _, err := uuid.Parse(value); return err == nil }
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
func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func mapRead(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

func mapWrite(operation string, err error) error {
	var pg *pq.Error
	if errors.As(err, &pg) {
		if pg.Code == "55000" {
			return fmt.Errorf("%s: %w", operation, ErrImmutable)
		}
		if pg.Code == "23505" {
			return fmt.Errorf("%s: %w", operation, ErrConflict)
		}
		if pg.Code.Class() == "23" {
			return fmt.Errorf("%s: %w", operation, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
