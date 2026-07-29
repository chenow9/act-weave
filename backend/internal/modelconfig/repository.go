package modelconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var (
	ErrNotFound = errors.New("model config not found")
	ErrConflict = errors.New("model config conflict")
	ErrInvalid  = errors.New("invalid model config")
	ErrInUse    = errors.New("model config is in use")
)

const configColumns = `
	m.id,
	m.workspace_id,
	m.name::TEXT,
	m.provider,
	m.api_base,
	m.model_name,
	m.credential_secret_id,
	(
		m.credential_secret_id IS NOT NULL
		AND s.active_version_id IS NOT NULL
		AND EXISTS (
			SELECT 1
			FROM secret_versions AS sv
			WHERE sv.workspace_id = s.workspace_id
			  AND sv.secret_id = s.id
			  AND sv.id = s.active_version_id
			  AND sv.revoked_at IS NULL
		)
	),
	m.options,
	m.runtime_capabilities,
	m.status,
	m.last_verified_at,
	m.last_latency_ms,
	m.last_error_code,
	m.created_by,
	m.updated_by,
	m.created_at,
	m.updated_at,
	m.lock_version,
	m.deleted_at
`

// UsageChecker lets the Agent domain participate in the same delete
// transaction once the agents table exists.
type UsageChecker interface {
	IsModelConfigInUse(context.Context, *sql.Tx, string, string) (bool, error)
}

type UsageCheckerFunc func(context.Context, *sql.Tx, string, string) (bool, error)

func (f UsageCheckerFunc) IsModelConfigInUse(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	configID string,
) (bool, error) {
	return f(ctx, tx, workspaceID, configID)
}

type Repository struct {
	db           *sql.DB
	usageChecker UsageChecker
}

func NewRepository(db *sql.DB, usageChecker ...UsageChecker) (*Repository, error) {
	if db == nil {
		return nil, errors.New("model config repository database is required")
	}
	if len(usageChecker) > 1 {
		return nil, errors.New("model config repository accepts at most one usage checker")
	}
	repository := &Repository{db: db}
	if len(usageChecker) == 1 {
		if usageChecker[0] == nil {
			return nil, errors.New("model config usage checker cannot be nil")
		}
		repository.usageChecker = usageChecker[0]
	}
	return repository, nil
}

func (r *Repository) Create(ctx context.Context, input NewConfig) (Config, error) {
	input = normalizeNewConfig(input)
	if err := validateNewConfig(input); err != nil {
		return Config{}, err
	}
	config, err := scanConfig(r.db.QueryRowContext(ctx, `
		WITH inserted AS (
			INSERT INTO model_configs (
				id, workspace_id, name, provider, api_base, model_name,
				credential_secret_id, options, runtime_capabilities, created_by, updated_by
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
			RETURNING *
		)
		SELECT `+configColumns+`
		FROM inserted AS m
		LEFT JOIN secrets AS s
		  ON s.workspace_id = m.workspace_id
		 AND s.id = m.credential_secret_id`,
		input.ID,
		input.WorkspaceID,
		input.Name,
		input.Provider,
		input.APIBase,
		input.ModelName,
		input.CredentialSecretID,
		[]byte(input.Options),
		[]byte(input.RuntimeCapabilities),
		input.CreatedBy,
	))
	if err != nil {
		return Config{}, mapWriteError("create model config", err)
	}
	return config, nil
}

func (r *Repository) Get(ctx context.Context, workspaceID string, configID string) (Config, error) {
	workspaceID, configID = strings.TrimSpace(workspaceID), strings.TrimSpace(configID)
	if !validUUID(workspaceID) || !validUUID(configID) {
		return Config{}, ErrInvalid
	}
	config, err := scanConfig(r.db.QueryRowContext(ctx, `
		SELECT `+configColumns+`
		FROM model_configs AS m
		LEFT JOIN secrets AS s
		  ON s.workspace_id = m.workspace_id
		 AND s.id = m.credential_secret_id
		WHERE m.workspace_id = $1 AND m.id = $2 AND m.deleted_at IS NULL
	`, workspaceID, configID))
	return config, mapReadError("get model config", err)
}

func (r *Repository) List(ctx context.Context, workspaceID string) ([]Config, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if !validUUID(workspaceID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+configColumns+`
		FROM model_configs AS m
		LEFT JOIN secrets AS s
		  ON s.workspace_id = m.workspace_id
		 AND s.id = m.credential_secret_id
		WHERE m.workspace_id = $1 AND m.deleted_at IS NULL
		ORDER BY m.created_at, m.id
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list model configs: %w", err)
	}
	defer rows.Close()
	configs := make([]Config, 0)
	for rows.Next() {
		config, err := scanConfig(rows)
		if err != nil {
			return nil, fmt.Errorf("scan model config: %w", err)
		}
		configs = append(configs, config)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate model configs: %w", err)
	}
	return configs, nil
}

func (r *Repository) Update(
	ctx context.Context,
	workspaceID string,
	configID string,
	input UpdateConfig,
) (Config, error) {
	workspaceID, configID = strings.TrimSpace(workspaceID), strings.TrimSpace(configID)
	input = normalizeUpdateConfig(input)
	if !validUUID(workspaceID) || !validUUID(configID) || validateUpdateConfig(input) != nil {
		return Config{}, ErrInvalid
	}
	config, err := scanConfig(r.db.QueryRowContext(ctx, `
		WITH updated AS (
			UPDATE model_configs
			SET name = $3,
				provider = $4,
				api_base = $5,
				model_name = $6,
				credential_secret_id = $7,
				options = $8,
				runtime_capabilities = $9,
				status = $10,
				updated_by = $11,
				updated_at = clock_timestamp(),
				lock_version = lock_version + 1
			WHERE workspace_id = $1 AND id = $2
			  AND deleted_at IS NULL AND lock_version = $12
			RETURNING *
		)
		SELECT `+configColumns+`
		FROM updated AS m
		LEFT JOIN secrets AS s
		  ON s.workspace_id = m.workspace_id
		 AND s.id = m.credential_secret_id
	`,
		workspaceID,
		configID,
		input.Name,
		input.Provider,
		input.APIBase,
		input.ModelName,
		input.CredentialSecretID,
		[]byte(input.Options),
		[]byte(input.RuntimeCapabilities),
		input.Status,
		input.UpdatedBy,
		input.ExpectedLockVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Config{}, r.classifyMissingOrConflict(ctx, workspaceID, configID)
	}
	if err != nil {
		return Config{}, mapWriteError("update model config", err)
	}
	return config, nil
}

func (r *Repository) RecordVerification(ctx context.Context, input VerificationUpdate) (Config, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ConfigID = strings.TrimSpace(input.ConfigID)
	input.VerifiedBy = strings.TrimSpace(input.VerifiedBy)
	input.ErrorCode = normalizeOptionalText(input.ErrorCode)
	if !validUUID(input.WorkspaceID) || !validUUID(input.ConfigID) ||
		!validUUID(input.VerifiedBy) || input.ExpectedLockVersion < 1 || input.LatencyMS < 0 ||
		(input.Status != StatusVerified && input.Status != StatusError) ||
		(input.Status == StatusVerified && input.ErrorCode != nil) ||
		(input.Status == StatusError && (input.ErrorCode == nil || !validStableCode(*input.ErrorCode))) {
		return Config{}, ErrInvalid
	}
	config, err := scanConfig(r.db.QueryRowContext(ctx, `
		WITH updated AS (
			UPDATE model_configs
			SET status = $3,
				last_verified_at = clock_timestamp(),
				last_latency_ms = $4,
				last_error_code = $5,
				updated_by = $6,
				updated_at = clock_timestamp(),
				lock_version = lock_version + 1
			WHERE workspace_id = $1 AND id = $2
			  AND deleted_at IS NULL AND lock_version = $7
			RETURNING *
		)
		SELECT `+configColumns+`
		FROM updated AS m
		LEFT JOIN secrets AS s
		  ON s.workspace_id = m.workspace_id
		 AND s.id = m.credential_secret_id
	`, input.WorkspaceID, input.ConfigID, input.Status, input.LatencyMS,
		input.ErrorCode, input.VerifiedBy, input.ExpectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return Config{}, r.classifyMissingOrConflict(ctx, input.WorkspaceID, input.ConfigID)
	}
	if err != nil {
		return Config{}, mapWriteError("record model config verification", err)
	}
	return config, nil
}

func (r *Repository) SoftDelete(
	ctx context.Context,
	workspaceID string,
	configID string,
	deletedBy string,
	expectedLockVersion int64,
) error {
	workspaceID, configID, deletedBy = strings.TrimSpace(workspaceID), strings.TrimSpace(configID), strings.TrimSpace(deletedBy)
	if !validUUID(workspaceID) || !validUUID(configID) || !validUUID(deletedBy) || expectedLockVersion < 1 {
		return ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete model config transaction: %w", err)
	}
	defer tx.Rollback()
	var lockVersion int64
	err = tx.QueryRowContext(ctx, `
		SELECT lock_version
		FROM model_configs
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
		FOR UPDATE
	`, workspaceID, configID).Scan(&lockVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock model config for deletion: %w", err)
	}
	if lockVersion != expectedLockVersion {
		return ErrConflict
	}
	var workspaceUsesConfig bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM workspaces
			WHERE id = $1 AND default_model_config_id = $2 AND deleted_at IS NULL
		)
	`, workspaceID, configID).Scan(&workspaceUsesConfig); err != nil {
		return fmt.Errorf("check workspace model config usage: %w", err)
	}
	if workspaceUsesConfig {
		return ErrInUse
	}
	if r.usageChecker != nil {
		inUse, err := r.usageChecker.IsModelConfigInUse(ctx, tx, workspaceID, configID)
		if err != nil {
			return fmt.Errorf("check agent model config usage: %w", err)
		}
		if inUse {
			return ErrInUse
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE model_configs
		SET deleted_at = clock_timestamp(),
			updated_by = $3,
			updated_at = clock_timestamp(),
			lock_version = lock_version + 1
		WHERE workspace_id = $1 AND id = $2
		  AND deleted_at IS NULL AND lock_version = $4
	`, workspaceID, configID, deletedBy, expectedLockVersion)
	if err != nil {
		return mapWriteError("soft delete model config", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted model config count: %w", err)
	}
	if rows != 1 {
		return ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return mapWriteError("commit delete model config transaction", err)
	}
	return nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanConfig(row rowScanner) (Config, error) {
	var config Config
	var options []byte
	var runtimeCapabilities []byte
	err := row.Scan(
		&config.ID,
		&config.WorkspaceID,
		&config.Name,
		&config.Provider,
		&config.APIBase,
		&config.ModelName,
		&config.CredentialSecretID,
		&config.CredentialConfigured,
		&options,
		&runtimeCapabilities,
		&config.Status,
		&config.LastVerifiedAt,
		&config.LastLatencyMS,
		&config.LastErrorCode,
		&config.CreatedBy,
		&config.UpdatedBy,
		&config.CreatedAt,
		&config.UpdatedAt,
		&config.LockVersion,
		&config.DeletedAt,
	)
	config.Options = append(json.RawMessage(nil), options...)
	config.RuntimeCapabilities = append(json.RawMessage(nil), runtimeCapabilities...)
	return config, err
}

func normalizeNewConfig(input NewConfig) NewConfig {
	input.ID = strings.TrimSpace(input.ID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Provider = strings.TrimSpace(input.Provider)
	input.APIBase = strings.TrimSpace(input.APIBase)
	input.ModelName = strings.TrimSpace(input.ModelName)
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	input.CredentialSecretID = normalizeOptionalID(input.CredentialSecretID)
	if len(input.Options) == 0 {
		input.Options = json.RawMessage(`{}`)
	} else {
		input.Options = append(json.RawMessage(nil), input.Options...)
	}
	if normalized, err := NormalizeRuntimeCapabilitiesRaw(input.RuntimeCapabilities); err == nil {
		input.RuntimeCapabilities = normalized
	}
	return input
}

func normalizeUpdateConfig(input UpdateConfig) UpdateConfig {
	input.Name = strings.TrimSpace(input.Name)
	input.Provider = strings.TrimSpace(input.Provider)
	input.APIBase = strings.TrimSpace(input.APIBase)
	input.ModelName = strings.TrimSpace(input.ModelName)
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	input.CredentialSecretID = normalizeOptionalID(input.CredentialSecretID)
	if len(input.Options) == 0 {
		input.Options = json.RawMessage(`{}`)
	} else {
		input.Options = append(json.RawMessage(nil), input.Options...)
	}
	if normalized, err := NormalizeRuntimeCapabilitiesRaw(input.RuntimeCapabilities); err == nil {
		input.RuntimeCapabilities = normalized
	}
	if input.Status == "" {
		input.Status = StatusUnverified
	}
	return input
}

func validateNewConfig(input NewConfig) error {
	if !validUUID(input.ID) || !validUUID(input.WorkspaceID) || input.Name == "" ||
		input.Provider == "" || input.APIBase == "" || input.ModelName == "" ||
		!validUUID(input.CreatedBy) || !validOptionalID(input.CredentialSecretID) ||
		!validJSONObject(input.Options) || containsSensitiveKey(input.Options) {
		return ErrInvalid
	}
	if _, err := NormalizeRuntimeCapabilitiesRaw(input.RuntimeCapabilities); err != nil {
		return ErrInvalid
	}
	// Runtime capabilities must never appear inside provider Options.
	if containsRuntimeCapabilityLeak(input.Options) {
		return ErrInvalid
	}
	return nil
}

func validateUpdateConfig(input UpdateConfig) error {
	if input.Name == "" || input.Provider == "" || input.APIBase == "" ||
		input.ModelName == "" || !validUUID(input.UpdatedBy) ||
		input.ExpectedLockVersion < 1 || !validStatus(input.Status) ||
		!validOptionalID(input.CredentialSecretID) || !validJSONObject(input.Options) ||
		containsSensitiveKey(input.Options) {
		return ErrInvalid
	}
	if _, err := NormalizeRuntimeCapabilitiesRaw(input.RuntimeCapabilities); err != nil {
		return ErrInvalid
	}
	if containsRuntimeCapabilityLeak(input.Options) {
		return ErrInvalid
	}
	return nil
}

func containsRuntimeCapabilityLeak(options json.RawMessage) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(options, &object) != nil || object == nil {
		return true
	}
	for _, forbidden := range []string{
		"runtimeCapabilities", "contextWindowTokens", "tokenizerProfile",
		"defaultOutputReserveTokens", "outputTokenLimitMode", "tokenizerVersion",
	} {
		if _, ok := object[forbidden]; ok {
			return true
		}
	}
	return false
}

func validStatus(status Status) bool {
	switch status {
	case StatusUnverified, StatusVerified, StatusError, StatusDisabled:
		return true
	default:
		return false
	}
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
				for _, forbidden := range []string{
					"password", "secretvalue", "tokenvalue", "apikey", "authorization", "refreshtoken",
				} {
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

func normalizeOptionalID(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func normalizeOptionalText(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func validStableCode(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func validOptionalID(value *string) bool {
	return value == nil || validUUID(*value)
}

func validUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func (r *Repository) classifyMissingOrConflict(
	ctx context.Context,
	workspaceID string,
	configID string,
) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM model_configs
			WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
		)
	`, workspaceID, configID).Scan(&exists); err != nil {
		return fmt.Errorf("classify model config update: %w", err)
	}
	if exists {
		return ErrConflict
	}
	return ErrNotFound
}

func mapReadError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func mapWriteError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var postgresError *pq.Error
	if errors.As(err, &postgresError) {
		switch postgresError.Code.Class() {
		case "22", "23":
			if postgresError.Code == "23505" {
				return fmt.Errorf("%s: %w", operation, ErrConflict)
			}
			return fmt.Errorf("%s: %w", operation, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
