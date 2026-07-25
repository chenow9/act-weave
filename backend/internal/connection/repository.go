package connection

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
	ErrNotFound = errors.New("connection not found")
	ErrConflict = errors.New("connection conflict")
	ErrInvalid  = errors.New("invalid connection")
)

const connectionColumns = `
	c.id, c.workspace_id, c.provider_id, c.name::TEXT, c.alias::TEXT,
	c.environment, c.external_account_ref, c.auth_mode, c.auth_config,
	c.credential_secret_id,
	(v.id IS NOT NULL AND v.revoked_at IS NULL),
	CASE WHEN v.revoked_at IS NULL THEN v.fingerprint ELSE NULL END,
	c.granted_scopes, c.policy, c.status, c.last_verified_at, c.last_error_code,
	c.outbound_identity, c.outbound_identity_policy_version, c.migration_state,
	c.machine_credential_secret_id,
	(mv.id IS NOT NULL AND mv.revoked_at IS NULL),
	c.created_by, c.updated_by, c.created_at, c.updated_at, c.lock_version, c.deleted_at
`

// connectionSecretJoins attaches legacy business credential and machine credential versions.
const connectionSecretJoins = `
	LEFT JOIN secrets AS s ON s.workspace_id=c.workspace_id AND s.id=c.credential_secret_id
	LEFT JOIN secret_versions AS v
	  ON v.workspace_id=s.workspace_id AND v.secret_id=s.id AND v.id=s.active_version_id
	LEFT JOIN secrets AS ms ON ms.workspace_id=c.workspace_id AND ms.id=c.machine_credential_secret_id
	LEFT JOIN secret_versions AS mv
	  ON mv.workspace_id=ms.workspace_id AND mv.secret_id=ms.id AND mv.id=ms.active_version_id
`

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("connection repository database is required")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Create(ctx context.Context, input NewConnection) (Connection, error) {
	input = normalizeNew(input)
	if validateNew(input) != nil {
		return Connection{}, ErrInvalid
	}
	created, err := scanConnection(r.db.QueryRowContext(ctx, `
		WITH inserted AS (
			INSERT INTO service_connections (
				id, workspace_id, provider_id, name, alias, environment,
				external_account_ref, auth_mode, auth_config, credential_secret_id,
				granted_scopes, policy, outbound_identity, outbound_identity_policy_version,
				migration_state, machine_credential_secret_id, status, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULL,$10,$11,$12,1,$13,$14,'UNVERIFIED',$15,$15)
			RETURNING *
		)
		SELECT `+connectionColumns+`
		FROM inserted AS c
		`+connectionSecretJoins+`
	`, input.ID, input.WorkspaceID, input.ProviderID, input.Name, input.Alias,
		input.Environment, input.ExternalAccountRef, input.AuthMode, []byte(input.AuthConfig),
		[]byte(input.GrantedScopes), []byte(input.Policy), nullableJSON(input.OutboundIdentity),
		input.MigrationState, input.MachineCredentialSecretID, input.CreatedBy))
	if err != nil {
		return Connection{}, mapWriteError("create connection", err)
	}
	return created, nil
}

func (r *Repository) Get(ctx context.Context, workspaceID, connectionID string) (Connection, error) {
	if !validUUID(workspaceID) || !validUUID(connectionID) {
		return Connection{}, ErrInvalid
	}
	value, err := scanConnection(r.db.QueryRowContext(ctx, `
		SELECT `+connectionColumns+`
		FROM service_connections AS c
		`+connectionSecretJoins+`
		WHERE c.workspace_id=$1 AND c.id=$2 AND c.deleted_at IS NULL
	`, workspaceID, connectionID))
	if errors.Is(err, sql.ErrNoRows) {
		return Connection{}, ErrNotFound
	}
	if err != nil {
		return Connection{}, fmt.Errorf("get connection: %w", err)
	}
	return value, nil
}

func (r *Repository) List(ctx context.Context, workspaceID string, providerID *string) ([]Connection, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	providerID = optionalID(providerID)
	if !validUUID(workspaceID) || (providerID != nil && !validUUID(*providerID)) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+connectionColumns+`
		FROM service_connections AS c
		`+connectionSecretJoins+`
		WHERE c.workspace_id=$1 AND c.deleted_at IS NULL
		  AND ($2::UUID IS NULL OR c.provider_id=$2)
		ORDER BY c.created_at,c.id
	`, workspaceID, providerID)
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	defer rows.Close()
	values := make([]Connection, 0)
	for rows.Next() {
		value, err := scanConnection(rows)
		if err != nil {
			return nil, fmt.Errorf("scan connection: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate connections: %w", err)
	}
	return values, nil
}

func (r *Repository) Update(ctx context.Context, workspaceID, connectionID string, input UpdateConnection) (Connection, error) {
	workspaceID, connectionID = strings.TrimSpace(workspaceID), strings.TrimSpace(connectionID)
	input = normalizeUpdate(input)
	if !validUUID(workspaceID) || !validUUID(connectionID) || validateUpdate(input) != nil {
		return Connection{}, ErrInvalid
	}
	// Metadata-only path: EDITOR may change name/alias/environment/scopes/policy labels
	// without touching identity, machine secret, or policy version.
	if input.MetadataOnly {
		value, err := scanConnection(r.db.QueryRowContext(ctx, `
			WITH updated AS (
				UPDATE service_connections SET
					name=$3,alias=$4,environment=$5,external_account_ref=$6,
					granted_scopes=$7,policy=$8,updated_by=$9,
					updated_at=clock_timestamp(),lock_version=lock_version+1
				WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL AND lock_version=$10
				RETURNING *
			)
			SELECT `+connectionColumns+`
			FROM updated AS c
			`+connectionSecretJoins+`
		`, workspaceID, connectionID, input.Name, input.Alias, input.Environment,
			input.ExternalAccountRef, []byte(input.GrantedScopes), []byte(input.Policy),
			input.UpdatedBy, input.ExpectedLockVersion))
		if errors.Is(err, sql.ErrNoRows) {
			return Connection{}, r.classifyMissingOrConflict(ctx, workspaceID, connectionID)
		}
		if err != nil {
			return Connection{}, mapWriteError("update connection metadata", err)
		}
		return value, nil
	}

	policyBump := 0
	if input.IncrementPolicyVersion {
		policyBump = 1
	}
	migrationStateExpr := `migration_state`
	if input.KeepMigrationState {
		migrationStateExpr = `CASE WHEN migration_state='MIGRATION_REQUIRED' THEN 'MIGRATION_REQUIRED' ELSE migration_state END`
	}
	// Identity mutation: never reintroduce legacy credential_secret_id; clear it.
	value, err := scanConnection(r.db.QueryRowContext(ctx, `
		WITH updated AS (
			UPDATE service_connections SET
				name=$3,alias=$4,environment=$5,external_account_ref=$6,
				credential_secret_id=NULL,
				granted_scopes=$7,policy=$8,
				outbound_identity=$9,
				outbound_identity_policy_version=outbound_identity_policy_version+$10,
				machine_credential_secret_id=CASE
					WHEN $11::BOOLEAN THEN NULL
					WHEN $12::UUID IS NOT NULL THEN $12
					ELSE machine_credential_secret_id
				END,
				migration_state=`+migrationStateExpr+`,
				status='UNVERIFIED',
				last_verified_at=NULL,last_error_code=NULL,updated_by=$13,
				updated_at=clock_timestamp(),lock_version=lock_version+1
			WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL AND lock_version=$14
			RETURNING *
		)
		SELECT `+connectionColumns+`
		FROM updated AS c
		`+connectionSecretJoins+`
	`, workspaceID, connectionID, input.Name, input.Alias, input.Environment,
		input.ExternalAccountRef, []byte(input.GrantedScopes), []byte(input.Policy),
		nullableJSON(input.OutboundIdentity), policyBump,
		input.ClearMachineCredential, input.MachineCredentialSecretID,
		input.UpdatedBy, input.ExpectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return Connection{}, r.classifyMissingOrConflict(ctx, workspaceID, connectionID)
	}
	if err != nil {
		return Connection{}, mapWriteError("update connection", err)
	}
	return value, nil
}

func (r *Repository) SoftDelete(ctx context.Context, workspaceID, connectionID, deletedBy string, expectedLockVersion int64) error {
	workspaceID, connectionID, deletedBy = strings.TrimSpace(workspaceID), strings.TrimSpace(connectionID), strings.TrimSpace(deletedBy)
	if !validUUID(workspaceID) || !validUUID(connectionID) || !validUUID(deletedBy) || expectedLockVersion < 1 {
		return ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin soft delete connection transaction: %w", err)
	}
	defer tx.Rollback()

	var lockVersion int64
	err = tx.QueryRowContext(ctx, `
		SELECT lock_version FROM service_connections
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL
		FOR UPDATE
	`, workspaceID, connectionID).Scan(&lockVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock connection for soft delete: %w", err)
	}
	if lockVersion != expectedLockVersion {
		return ErrConflict
	}

	var referenced bool
	if err := tx.QueryRowContext(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				FROM tools t
				JOIN capabilities c
				  ON c.workspace_id=t.workspace_id AND c.id=t.capability_id
				WHERE t.workspace_id=$1 AND t.default_connection_id=$2
				  AND c.deleted_at IS NULL
			) OR EXISTS (
				SELECT 1
				FROM tool_versions v
				JOIN capabilities c
				  ON c.workspace_id=v.workspace_id AND c.id=v.capability_id
				WHERE v.workspace_id=$1 AND v.default_connection_id=$2
				  AND c.deleted_at IS NULL
			) OR EXISTS (
				SELECT 1
				FROM agent_capability_bindings b
				WHERE b.workspace_id=$1 AND b.connection_id=$2 AND b.enabled
			)
	`, workspaceID, connectionID).Scan(&referenced); err != nil {
		return fmt.Errorf("check connection execution references: %w", err)
	}
	if referenced {
		return ErrConflict
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE service_connections SET deleted_at=clock_timestamp(),updated_by=$3,
			updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL AND lock_version=$4
	`, workspaceID, connectionID, deletedBy, expectedLockVersion)
	if err != nil {
		return mapWriteError("soft delete connection", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted connection count: %w", err)
	}
	if rows != 1 {
		return ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return mapWriteError("commit soft delete connection", err)
	}
	return nil
}

func (r *Repository) RecordVerification(ctx context.Context, input NewVerification) (Verification, error) {
	input = normalizeVerification(input)
	if validateVerification(input) != nil {
		return Verification{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Verification{}, fmt.Errorf("begin connection verification transaction: %w", err)
	}
	defer tx.Rollback()
	verification, err := scanVerification(tx.QueryRowContext(ctx, `
		INSERT INTO connection_verifications (
			id, workspace_id, connection_id, status, diagnostics,
			latency_ms, tested_by, raw_object_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, workspace_id, connection_id, status, diagnostics,
			latency_ms, tested_by, tested_at, raw_object_id
	`, input.ID, input.WorkspaceID, input.ConnectionID, input.Status,
		[]byte(input.Diagnostics), input.LatencyMS, input.TestedBy, input.RawObjectID))
	if err != nil {
		return Verification{}, mapWriteError("record connection verification", err)
	}
	connectionStatus := StatusError
	var errorCode any = "CONNECTION_VERIFICATION_FAILED"
	clearMigration := false
	if input.Status == "SUCCEEDED" {
		connectionStatus = StatusVerified
		errorCode = nil
		// Config-level verification success atomically clears MIGRATION_REQUIRED (T5=A path).
		clearMigration = true
	} else if input.ErrorCode != nil {
		errorCode = *input.ErrorCode
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE service_connections
		SET status=$3, last_verified_at=$4, last_error_code=$5,
			migration_state=CASE WHEN $8 THEN 'NONE' ELSE migration_state END,
			updated_by=$6, updated_at=clock_timestamp(), lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL
		  AND ($7 = 0 OR lock_version = $7)
	`, input.WorkspaceID, input.ConnectionID, connectionStatus,
		verification.TestedAt, errorCode, input.TestedBy, input.ExpectedLockVersion, clearMigration)
	if err != nil {
		return Verification{}, mapWriteError("update verified connection", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM service_connections
				WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL
			)
		`, input.WorkspaceID, input.ConnectionID).Scan(&exists); err != nil {
			return Verification{}, fmt.Errorf("classify verified connection write: %w", err)
		}
		if exists {
			return Verification{}, ErrConflict
		}
		return Verification{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return Verification{}, mapWriteError("commit connection verification", err)
	}
	return verification, nil
}

type rowScanner interface{ Scan(...any) error }

func scanConnection(row rowScanner) (Connection, error) {
	var value Connection
	var authConfig, scopes, policy, outboundIdentity []byte
	var fingerprint sql.NullString
	err := row.Scan(&value.ID, &value.WorkspaceID, &value.ProviderID, &value.Name, &value.Alias,
		&value.Environment, &value.ExternalAccountRef, &value.AuthMode, &authConfig,
		&value.CredentialSecretID, &value.CredentialConfigured, &fingerprint,
		&scopes, &policy, &value.Status, &value.LastVerifiedAt, &value.LastErrorCode,
		&outboundIdentity, &value.OutboundIdentityPolicyVersion, &value.MigrationState,
		&value.MachineCredentialSecretID, &value.MachineCredentialConfigured,
		&value.CreatedBy, &value.UpdatedBy, &value.CreatedAt, &value.UpdatedAt,
		&value.LockVersion, &value.DeletedAt)
	value.AuthConfig = append(json.RawMessage(nil), authConfig...)
	value.GrantedScopes = append(json.RawMessage(nil), scopes...)
	value.Policy = append(json.RawMessage(nil), policy...)
	if len(outboundIdentity) > 0 && string(outboundIdentity) != "null" {
		value.OutboundIdentity = append(json.RawMessage(nil), outboundIdentity...)
	}
	if fingerprint.Valid {
		value.CredentialFingerprint = fingerprint.String
	}
	return value, err
}

func scanVerification(row rowScanner) (Verification, error) {
	var value Verification
	var diagnostics []byte
	err := row.Scan(&value.ID, &value.WorkspaceID, &value.ConnectionID, &value.Status,
		&diagnostics, &value.LatencyMS, &value.TestedBy, &value.TestedAt, &value.RawObjectID)
	value.Diagnostics = append(json.RawMessage(nil), diagnostics...)
	return value, err
}

func normalizeNew(input NewConnection) NewConnection {
	input.ID, input.WorkspaceID, input.ProviderID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.ProviderID)
	input.Name, input.Alias, input.Environment = strings.TrimSpace(input.Name), strings.TrimSpace(input.Alias), strings.TrimSpace(input.Environment)
	// Placeholder for NOT NULL legacy column; not an active scheme.
	if strings.TrimSpace(input.AuthMode) == "" {
		input.AuthMode = "OUTBOUND_IDENTITY"
	}
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	input.MachineCredentialSecretID = optionalID(input.MachineCredentialSecretID)
	input.AuthConfig = defaultJSON(input.AuthConfig, `{}`)
	input.GrantedScopes = defaultJSON(input.GrantedScopes, `[]`)
	input.Policy = defaultJSON(input.Policy, `{}`)
	if strings.TrimSpace(input.MigrationState) == "" {
		input.MigrationState = MigrationStateNone
	}
	return input
}

func validateNew(input NewConnection) error {
	if !validUUID(input.ID) || !validUUID(input.WorkspaceID) || !validUUID(input.ProviderID) ||
		input.Name == "" || input.Alias == "" || !validUUID(input.CreatedBy) ||
		(input.MachineCredentialSecretID != nil && !validUUID(*input.MachineCredentialSecretID)) ||
		!oneOf(input.Environment, "PRODUCTION", "STAGING", "DEVELOPMENT", "TEST") ||
		!oneOf(input.MigrationState, MigrationStateNone, MigrationStateMigrationRequired) ||
		len(input.OutboundIdentity) == 0 || !jsonKind(input.OutboundIdentity, '{') ||
		!jsonKind(input.AuthConfig, '{') || !jsonKind(input.GrantedScopes, '[') || !jsonKind(input.Policy, '{') ||
		containsSensitiveKey(input.AuthConfig) || containsSensitiveKey(input.OutboundIdentity) {
		return ErrInvalid
	}
	return nil
}

func normalizeVerification(input NewVerification) NewVerification {
	input.ID, input.WorkspaceID, input.ConnectionID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.ConnectionID)
	input.Status, input.TestedBy = strings.TrimSpace(input.Status), strings.TrimSpace(input.TestedBy)
	input.Diagnostics = defaultJSON(input.Diagnostics, `{}`)
	input.ErrorCode = optionalText(input.ErrorCode)
	return input
}

func normalizeUpdate(input UpdateConnection) UpdateConnection {
	input.Name, input.Alias, input.Environment = strings.TrimSpace(input.Name), strings.TrimSpace(input.Alias), strings.TrimSpace(input.Environment)
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	input.ExternalAccountRef = optionalText(input.ExternalAccountRef)
	input.MachineCredentialSecretID = optionalID(input.MachineCredentialSecretID)
	input.GrantedScopes = defaultJSON(input.GrantedScopes, `[]`)
	input.Policy = defaultJSON(input.Policy, `{}`)
	return input
}

func validateUpdate(input UpdateConnection) error {
	if input.Name == "" || input.Alias == "" || !validUUID(input.UpdatedBy) ||
		input.ExpectedLockVersion < 1 ||
		(input.MachineCredentialSecretID != nil && !validUUID(*input.MachineCredentialSecretID)) ||
		!oneOf(input.Environment, "PRODUCTION", "STAGING", "DEVELOPMENT", "TEST") ||
		!jsonKind(input.GrantedScopes, '[') || !jsonKind(input.Policy, '{') {
		return ErrInvalid
	}
	if !input.MetadataOnly {
		if len(input.OutboundIdentity) == 0 || !jsonKind(input.OutboundIdentity, '{') ||
			containsSensitiveKey(input.OutboundIdentity) {
			return ErrInvalid
		}
	}
	return nil
}

func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return []byte(raw)
}

func (r *Repository) classifyMissingOrConflict(ctx context.Context, workspaceID, connectionID string) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM service_connections WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL)
	`, workspaceID, connectionID).Scan(&exists); err != nil {
		return fmt.Errorf("classify connection write: %w", err)
	}
	if exists {
		return ErrConflict
	}
	return ErrNotFound
}

func validateVerification(input NewVerification) error {
	if !validUUID(input.ID) || !validUUID(input.WorkspaceID) || !validUUID(input.ConnectionID) ||
		!validUUID(input.TestedBy) || !oneOf(input.Status, "SUCCEEDED", "FAILED") ||
		!jsonKind(input.Diagnostics, '{') || containsSensitiveKey(input.Diagnostics) ||
		(input.LatencyMS != nil && *input.LatencyMS < 0) || input.ExpectedLockVersion < 0 ||
		(input.Status == "SUCCEEDED" && input.ErrorCode != nil) ||
		(input.ErrorCode != nil && !validStableCode(*input.ErrorCode)) {
		return ErrInvalid
	}
	return nil
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
					"password", "clientsecret", "apisecret", "privatekey", "credentialvalue",
					"secretvalue", "tokenvalue", "apikeyvalue", "authorization", "refreshtoken",
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

func defaultJSON(value json.RawMessage, fallback string) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(fallback)
	}
	return append(json.RawMessage(nil), value...)
}
func jsonKind(value json.RawMessage, prefix byte) bool {
	trimmed := strings.TrimSpace(string(value))
	return len(trimmed) > 0 && trimmed[0] == prefix && json.Valid(value)
}
func optionalID(value *string) *string {
	if value == nil {
		return nil
	}
	v := strings.TrimSpace(*value)
	if v == "" {
		return nil
	}
	return &v
}
func optionalText(value *string) *string {
	if value == nil {
		return nil
	}
	v := strings.TrimSpace(*value)
	if v == "" {
		return nil
	}
	return &v
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
func validUUID(value string) bool { _, err := uuid.Parse(value); return err == nil }
func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func mapWriteError(operation string, err error) error {
	var pg *pq.Error
	if errors.As(err, &pg) && pg.Code.Class() == "23" {
		if pg.Code == "23505" {
			return fmt.Errorf("%s: %w", operation, ErrConflict)
		}
		return fmt.Errorf("%s: %w", operation, ErrInvalid)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
