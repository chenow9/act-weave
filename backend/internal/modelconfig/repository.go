package modelconfig

import (
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

var (
	ErrNotFound = errors.New("model config not found")
	ErrConflict = errors.New("model config conflict")
	ErrInvalid  = errors.New("invalid model config")
	ErrInUse    = errors.New("model config is in use")
	// ErrAgenticCapabilitiesReadOnly is returned when a create/update request
	// includes any presence of agenticCapabilities (null, {}, or data). Mapped
	// to HTTP 400 — not 422 — without changing other model validation.
	ErrAgenticCapabilitiesReadOnly = errors.New("agenticCapabilities is read-only")
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
	m.agentic_capabilities,
	m.tool_disclosure_policy,
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
	input, err := normalizeNewConfig(input)
	if err != nil {
		return Config{}, err
	}
	if err := validateNewConfig(input); err != nil {
		return Config{}, err
	}
	config, err := scanConfig(r.db.QueryRowContext(ctx, `
		WITH inserted AS (
			INSERT INTO model_configs (
				id, workspace_id, name, provider, api_base, model_name,
				credential_secret_id, options, runtime_capabilities,
				tool_disclosure_policy, created_by, updated_by
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, '{}'::jsonb, $10, $10)
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
	input, providerErr := normalizeUpdateConfig(input)
	if providerErr != nil {
		return Config{}, providerErr
	}
	if !validUUID(workspaceID) || !validUUID(configID) || validateUpdateConfig(input) != nil {
		return Config{}, ErrInvalid
	}
	// Any user mutation that can change wire behavior atomically clears *all*
	// verification evidence in the same CAS update (D10 / Task 3 fix):
	// agentic_capabilities={}, last_verified_at/latency/error NULL, status
	// UNVERIFIED (or DISABLED when explicitly requested).
	status := StatusUnverified
	if input.Status == StatusDisabled {
		status = StatusDisabled
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
				agentic_capabilities = '{}'::jsonb,
				tool_disclosure_policy = '{}'::jsonb,
				last_verified_at = NULL,
				last_latency_ms = NULL,
				last_error_code = NULL,
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
		status,
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
	caps, err := NormalizeAgenticCapabilitiesRaw(input.AgenticCapabilities)
	if err != nil {
		return Config{}, ErrInvalid
	}
	policy := json.RawMessage(`{}`)
	if input.Status == StatusVerified {
		normalized, normErr := NormalizeToolDisclosurePolicyRaw(input.ToolDisclosurePolicy)
		if normErr != nil {
			return Config{}, ErrInvalid
		}
		policy = normalized
	}
	if !validUUID(input.WorkspaceID) || !validUUID(input.ConfigID) ||
		!validUUID(input.VerifiedBy) || input.ExpectedLockVersion < 1 || input.LatencyMS < 0 ||
		(input.Status != StatusVerified && input.Status != StatusError) ||
		(input.Status == StatusVerified && (input.ErrorCode != nil || IsUnverifiedAgenticCapabilities(caps))) ||
		(input.Status == StatusError && (input.ErrorCode == nil || !validVerificationErrorCode(*input.ErrorCode) || !IsUnverifiedAgenticCapabilities(caps))) {
		return Config{}, ErrInvalid
	}
	// On VERIFIED, capability lock identity must match the CAS expected version
	// and VerifiedAt must be non-zero (shared with last_verified_at at UTC second).
	var lastVerifiedAt any
	if input.Status == StatusVerified {
		doc, _, parseErr := ParseAgenticCapabilities(caps)
		if parseErr != nil || doc.VerifiedLockVersion != input.ExpectedLockVersion {
			return Config{}, ErrInvalid
		}
		if err := validateVerificationPolicy(doc, policy); err != nil {
			return Config{}, ErrInvalid
		}
		if input.VerifiedAt.IsZero() {
			return Config{}, ErrInvalid
		}
		// Persist the same UTC-second timestamp into last_verified_at so the
		// read invariant (capability.VerifiedAt == LastVerifiedAt @ second) holds.
		// SQL uses GREATEST(created_at, $9) so model_configs_verification_time_check
		// (last_verified_at >= created_at) still holds when truncation lands on the
		// same second as create (sub-second created_at can otherwise exceed truncated evidence).
		lastVerifiedAt = input.VerifiedAt.UTC().Truncate(time.Second)
		// Capability document must already stamp the same second.
		if !doc.VerifiedAt.UTC().Truncate(time.Second).Equal(lastVerifiedAt.(time.Time)) {
			return Config{}, ErrInvalid
		}
	} else {
		// ERROR path: canonical verification-attempt timestamp (UTC second).
		// Prefer caller VerifiedAt when set; otherwise clock. Always non-nil.
		// tool_disclosure_policy is left unchanged (see UPDATE CASE).
		if !input.VerifiedAt.IsZero() {
			lastVerifiedAt = input.VerifiedAt.UTC().Truncate(time.Second)
		} else {
			lastVerifiedAt = time.Now().UTC().Truncate(time.Second)
		}
	}
	config, err := scanConfig(r.db.QueryRowContext(ctx, `
		WITH updated AS (
			UPDATE model_configs
			SET status = $3,
				last_verified_at = GREATEST(created_at, $9::timestamptz),
				last_latency_ms = $4,
				last_error_code = $5,
				agentic_capabilities = $6::jsonb,
				tool_disclosure_policy = CASE
					WHEN $3 = 'ERROR' THEN tool_disclosure_policy
					ELSE $10::jsonb
				END,
				updated_by = $7,
				updated_at = clock_timestamp(),
				lock_version = lock_version + 1
			WHERE workspace_id = $1 AND id = $2
			  AND deleted_at IS NULL AND lock_version = $8
			RETURNING *
		)
		SELECT `+configColumns+`
		FROM updated AS m
		LEFT JOIN secrets AS s
		  ON s.workspace_id = m.workspace_id
		 AND s.id = m.credential_secret_id
	`, input.WorkspaceID, input.ConfigID, input.Status, input.LatencyMS,
		input.ErrorCode, []byte(caps), input.VerifiedBy, input.ExpectedLockVersion, lastVerifiedAt, []byte(policy)))
	if errors.Is(err, sql.ErrNoRows) {
		return Config{}, r.classifyMissingOrConflict(ctx, input.WorkspaceID, input.ConfigID)
	}
	if err != nil {
		return Config{}, mapWriteError("record model config verification", err)
	}
	return config, nil
}

func validateVerificationPolicy(doc AgenticCapabilities, policy json.RawMessage) error {
	parsed, _, err := ParseToolDisclosurePolicy(policy)
	if err != nil {
		return err
	}
	switch doc.ToolCalling {
	case ToolCallingFunctionCalling:
		if parsed.Mode != DisclosureModePlatformOnDemand && parsed.Mode != DisclosureModeCarryAll {
			return ErrInvalid
		}
		return nil
	default:
		if !IsUnsetToolDisclosurePolicy(policy) {
			return ErrInvalid
		}
		return nil
	}
}

func (r *Repository) UpdateDisclosurePolicy(ctx context.Context, input DisclosurePolicyUpdate) (Config, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ConfigID = strings.TrimSpace(input.ConfigID)
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	if !validUUID(input.WorkspaceID) || !validUUID(input.ConfigID) ||
		!validUUID(input.UpdatedBy) || input.ExpectedLockVersion < 1 {
		return Config{}, ErrInvalid
	}
	policyDoc, policyRaw, err := ParseToolDisclosurePolicy(input.Policy)
	if err != nil || policyDoc.Mode == "" {
		return Config{}, ErrToolDisclosureInvalid
	}
	current, err := r.Get(ctx, input.WorkspaceID, input.ConfigID)
	if err != nil {
		return Config{}, err
	}
	if current.LockVersion != input.ExpectedLockVersion {
		return Config{}, ErrConflict
	}
	if err := AssertDisclosureWritable(current); err != nil {
		return Config{}, err
	}
	caps, _, err := ParseAgenticCapabilities(current.AgenticCapabilities)
	if err != nil {
		return Config{}, ErrToolDisclosureInvalid
	}
	caps.VerifiedLockVersion = current.LockVersion
	restamped, err := json.Marshal(caps)
	if err != nil {
		return Config{}, ErrInvalid
	}
	config, err := scanConfig(r.db.QueryRowContext(ctx, `
		WITH updated AS (
			UPDATE model_configs
			SET tool_disclosure_policy = $3::jsonb,
				agentic_capabilities = $4::jsonb,
				updated_by = $5,
				updated_at = clock_timestamp(),
				lock_version = lock_version + 1
			WHERE workspace_id = $1 AND id = $2
			  AND deleted_at IS NULL AND lock_version = $6
			  AND status = 'VERIFIED'
			RETURNING *
		)
		SELECT `+configColumns+`
		FROM updated AS m
		LEFT JOIN secrets AS s
		  ON s.workspace_id = m.workspace_id
		 AND s.id = m.credential_secret_id
	`, input.WorkspaceID, input.ConfigID, []byte(policyRaw), restamped,
		input.UpdatedBy, input.ExpectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return Config{}, r.classifyMissingOrConflict(ctx, input.WorkspaceID, input.ConfigID)
	}
	if err != nil {
		return Config{}, mapWriteError("update model config disclosure policy", err)
	}
	return config, nil
}

func (r *Repository) ListAgentsByModelConfig(ctx context.Context, workspaceID, configID string) ([]string, error) {
	workspaceID, configID = strings.TrimSpace(workspaceID), strings.TrimSpace(configID)
	if !validUUID(workspaceID) || !validUUID(configID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id
		FROM agents
		WHERE workspace_id = $1 AND model_config_id = $2 AND deleted_at IS NULL
		ORDER BY id
	`, workspaceID, configID)
	if err != nil {
		return nil, fmt.Errorf("list agents by model config: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan agent id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agents by model config: %w", err)
	}
	return ids, nil
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
	var agenticCapabilities []byte
	var toolDisclosurePolicy []byte
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
		&agenticCapabilities,
		&toolDisclosurePolicy,
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
	if err != nil {
		return Config{}, err
	}
	config.Options = append(json.RawMessage(nil), options...)
	config.RuntimeCapabilities = append(json.RawMessage(nil), runtimeCapabilities...)
	// Strict ParseAgenticCapabilities on every read/list — malformed/corrupt JSONB
	// fails closed and never projects. `{}` unverified remains readable.
	raw := json.RawMessage(agenticCapabilities)
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	doc, normalized, parseErr := ParseAgenticCapabilities(raw)
	if parseErr != nil {
		return Config{}, fmt.Errorf("%w: corrupt agentic_capabilities", ErrInvalid)
	}
	config.AgenticCapabilities = normalized
	if err := validateAgenticCapabilitiesCrossField(config, doc); err != nil {
		return Config{}, err
	}
	policyRaw := json.RawMessage(toolDisclosurePolicy)
	if len(policyRaw) == 0 {
		policyRaw = json.RawMessage(`{}`)
	}
	_, policyNormalized, policyErr := ParseToolDisclosurePolicy(policyRaw)
	if policyErr != nil {
		return Config{}, fmt.Errorf("%w: corrupt tool_disclosure_policy", ErrInvalid)
	}
	config.ToolDisclosurePolicy = policyNormalized
	return config, nil
}

// validateAgenticCapabilitiesCrossField enforces projection invariants:
//
// VERIFIED requires all of:
//   - nonempty verified capability document
//   - non-nil LastVerifiedAt
//   - non-nil nonnegative LastLatencyMS
//   - nil LastErrorCode
//   - verifiedLockVersion matches pre-increment lock identity
//     (LockVersion == VerifiedLockVersion+1 after CAS)
//   - verifiedConfigDigest matches WireConfigDigest of the current row
//   - capability VerifiedAt equals LastVerifiedAt at UTC second precision
//     (documented rule: both are written from the same truncated UTC second
//     at verification CAS; any drift means corrupt evidence and fails Get/List)
//
// UNVERIFIED and DISABLED require:
//   - caps {}
//   - last_verified_at, last_latency_ms, last_error_code all nil
//
// ERROR requires:
//   - caps {}
//   - non-nil LastVerifiedAt (canonical verification attempt time)
//   - non-nil nonnegative LastLatencyMS
//   - non-nil LastErrorCode from the exact stable verification allowlist
//     (not arbitrary uppercase text)
//
// Verification CAS increments lock_version on both success and failure, so a
// successful verify stamps VerifiedLockVersion = ExpectedLockVersion and then
// stores LockVersion = ExpectedLockVersion+1.
func validateAgenticCapabilitiesCrossField(config Config, doc AgenticCapabilities) error {
	unverified := IsUnverifiedAgenticCapabilities(config.AgenticCapabilities)
	switch config.Status {
	case StatusVerified:
		if unverified {
			return fmt.Errorf("%w: VERIFIED requires nonempty agentic capabilities", ErrInvalid)
		}
		// Read evidence: nonnil LastVerifiedAt, nonnil nonnegative LastLatencyMS, nil LastErrorCode.
		if config.LastVerifiedAt == nil {
			return fmt.Errorf("%w: VERIFIED requires LastVerifiedAt", ErrInvalid)
		}
		if config.LastLatencyMS == nil {
			return fmt.Errorf("%w: VERIFIED requires LastLatencyMS", ErrInvalid)
		}
		if *config.LastLatencyMS < 0 {
			return fmt.Errorf("%w: VERIFIED LastLatencyMS must be nonnegative", ErrInvalid)
		}
		if config.LastErrorCode != nil {
			return fmt.Errorf("%w: VERIFIED requires nil LastErrorCode", ErrInvalid)
		}
		// Lock identity: verified against N, then CAS set lock to N+1.
		if !AgenticCapabilityLockMatches(doc, config.LockVersion) {
			return fmt.Errorf("%w: verifiedLockVersion does not match lock identity", ErrInvalid)
		}
		if doc.VerifiedConfigDigest != WireConfigDigest(config) {
			return fmt.Errorf("%w: verifiedConfigDigest does not match current config", ErrInvalid)
		}
		// Canonical capability VerifiedAt must equal stored LastVerifiedAt at UTC second.
		capAt := doc.VerifiedAt.UTC().Truncate(time.Second)
		rowAt := config.LastVerifiedAt.UTC().Truncate(time.Second)
		if !capAt.Equal(rowAt) {
			return fmt.Errorf("%w: verifiedAt does not match LastVerifiedAt at UTC second", ErrInvalid)
		}
		return nil
	case StatusError:
		if !unverified {
			return fmt.Errorf("%w: ERROR cannot carry verified agentic capabilities", ErrInvalid)
		}
		if config.LastVerifiedAt == nil {
			return fmt.Errorf("%w: ERROR requires LastVerifiedAt", ErrInvalid)
		}
		if config.LastLatencyMS == nil {
			return fmt.Errorf("%w: ERROR requires LastLatencyMS", ErrInvalid)
		}
		if *config.LastLatencyMS < 0 {
			return fmt.Errorf("%w: ERROR LastLatencyMS must be nonnegative", ErrInvalid)
		}
		if config.LastErrorCode == nil {
			return fmt.Errorf("%w: ERROR requires LastErrorCode", ErrInvalid)
		}
		if !validVerificationErrorCode(*config.LastErrorCode) {
			return fmt.Errorf("%w: ERROR LastErrorCode not in stable allowlist", ErrInvalid)
		}
		return nil
	case StatusUnverified, StatusDisabled:
		if !unverified {
			return fmt.Errorf("%w: %s cannot carry verified agentic capabilities", ErrInvalid, config.Status)
		}
		if config.LastVerifiedAt != nil {
			return fmt.Errorf("%w: %s requires nil LastVerifiedAt", ErrInvalid, config.Status)
		}
		if config.LastLatencyMS != nil {
			return fmt.Errorf("%w: %s requires nil LastLatencyMS", ErrInvalid, config.Status)
		}
		if config.LastErrorCode != nil {
			return fmt.Errorf("%w: %s requires nil LastErrorCode", ErrInvalid, config.Status)
		}
		return nil
	default:
		// Unknown status: fail closed.
		return fmt.Errorf("%w: unknown model config status %q", ErrInvalid, config.Status)
	}
}

// normalizeNewConfig trims identity fields and canonicalizes the provider.
// Canonicalization is fail-closed (CanonicalProvider), so a provider outside the
// closed alias set aborts the create instead of being stored verbatim.
func normalizeNewConfig(input NewConfig) (NewConfig, error) {
	input.ID = strings.TrimSpace(input.ID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	provider, err := CanonicalProvider(input.Provider)
	if err != nil {
		return NewConfig{}, err
	}
	input.Provider = provider
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
	return input, nil
}

// normalizeUpdateConfig mirrors normalizeNewConfig: the provider is canonicalized
// fail-closed, because Update rewrites model_configs.provider and therefore the
// row's WireConfigDigest identity.
func normalizeUpdateConfig(input UpdateConfig) (UpdateConfig, error) {
	input.Name = strings.TrimSpace(input.Name)
	provider, err := CanonicalProvider(input.Provider)
	if err != nil {
		return UpdateConfig{}, err
	}
	input.Provider = provider
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
	return input, nil
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
	if !IsUnsetToolDisclosurePolicy(input.ToolDisclosurePolicy) {
		return ErrToolDisclosureInvalid
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
	// VERIFIED/ERROR are verification-owned. User Update may only leave
	// UNVERIFIED (default after clearing evidence) or DISABLED.
	if input.Status != StatusUnverified && input.Status != StatusDisabled && input.Status != "" {
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
		"agenticCapabilities", "toolSearchModes", "verifiedAdapter",
		"verifiedLockVersion", "verifiedConfigDigest",
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

// validVerificationErrorCode accepts only the exact stable verification allowlist.
// Arbitrary uppercase/text (e.g. "SOMETHING_ELSE", "ERROR") is rejected.
func validVerificationErrorCode(value string) bool {
	switch value {
	case ErrorCodeVerificationTimeout,
		ErrorCodeNetwork,
		ErrorCodeAuthentication,
		ErrorCodeUpstream,
		ErrorCodeResponsesUnsupported,
		ErrorCodeToolSearchUnsupported,
		ErrorCodeAgenticStreamInvalid,
		ErrorCodeAgenticUsageInvalid:
		return true
	default:
		return false
	}
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
