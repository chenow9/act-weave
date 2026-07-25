package agentaccess

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Credential struct {
	ID          string
	WorkspaceID string
	ClientID    string
	Type        CredentialType
	PublicHint  string
	ValidFrom   time.Time
	ExpiresAt   *time.Time
	LastUsedAt  *time.Time
	RevokedAt   *time.Time
	RevokedBy   string
	CreatedBy   string
	CreatedAt   time.Time
	LockVersion int64
}

// CredentialEvidence is a persistence-only DTO. Sensitive derived material is
// explicitly excluded from JSON so ordinary API/audit serialization cannot
// leak even hashes or thumbprints.
type CredentialEvidence struct {
	Credential
	SecretHash            []byte `json:"-"`
	JWKThumbprint         []byte `json:"-"`
	CertificateThumbprint []byte `json:"-"`
}

// ClientSecretAuthenticationRecord is persistence-only authentication state.
// SecretHash must never be serialized into an API, Audit, or log payload.
type ClientSecretAuthenticationRecord struct {
	WorkspaceID             string
	ClientID                string
	PublicClientID          string
	ServicePrincipalID      string
	ServicePrincipalVersion int64
	ClientStatus            Status
	ServicePrincipalStatus  Status
	AuthMethod              ClientAuthMethod
	TokenTTLSeconds         int
	CredentialID            string
	CredentialType          CredentialType
	SecretHash              []byte `json:"-"`
	ValidFrom               time.Time
	ExpiresAt               *time.Time
	RevokedAt               *time.Time
}

type CreateCredentialInput struct {
	ID, WorkspaceID, ClientID string
	Type                      CredentialType
	SecretHash                []byte
	JWKThumbprint             []byte
	CertificateThumbprint     []byte
	PublicHint                string
	ValidFrom                 time.Time
	ExpiresAt                 *time.Time
	ActorID                   string
}

func (repository *Repository) CreateCredential(
	ctx context.Context,
	input CreateCredentialInput,
) (Credential, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(input.ID) || !validRepositoryUUID(input.WorkspaceID) ||
		!validRepositoryUUID(input.ClientID) || !validRepositoryUUID(input.ActorID) ||
		input.ValidFrom.IsZero() || input.PublicHint == "" {
		return Credential{}, ErrRepositoryInvalid
	}
	if _, ok := ParseCredentialType(string(input.Type)); !ok {
		return Credential{}, ErrRepositoryInvalid
	}
	evidence, err := scanCredentialEvidence(repository.db.QueryRowContext(ctx, `
		INSERT INTO agent_access_credentials(
		 id,workspace_id,client_id,credential_type,secret_hash,jwk_thumbprint,
		 certificate_thumbprint,public_hint,valid_from,expires_at,created_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id,workspace_id,client_id,credential_type,public_hint,
		 valid_from,expires_at,last_used_at,revoked_at,revoked_by,created_by,created_at,
		 lock_version,secret_hash,jwk_thumbprint,certificate_thumbprint
	`, input.ID, input.WorkspaceID, input.ClientID, input.Type,
		nullableRepositoryBytes(input.SecretHash), nullableRepositoryBytes(input.JWKThumbprint),
		nullableRepositoryBytes(input.CertificateThumbprint), input.PublicHint,
		input.ValidFrom.UTC(), input.ExpiresAt, input.ActorID))
	return evidence.Credential, mapRepositoryWrite("create Agent Access Credential", err)
}

func (repository *Repository) GetCredential(
	ctx context.Context,
	workspaceID, clientID, credentialID string,
) (Credential, error) {
	evidence, err := repository.GetCredentialEvidence(ctx, workspaceID, clientID, credentialID)
	return evidence.Credential, err
}

func (repository *Repository) GetCredentialEvidence(
	ctx context.Context,
	workspaceID, clientID, credentialID string,
) (CredentialEvidence, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		!validRepositoryUUID(credentialID) {
		return CredentialEvidence{}, ErrRepositoryInvalid
	}
	value, err := scanCredentialEvidence(repository.db.QueryRowContext(ctx, credentialSelect+`
		WHERE workspace_id=$1 AND client_id=$2 AND id=$3`, workspaceID, clientID, credentialID))
	return value, mapRepositoryRead("get Agent Access Credential", err)
}

func (repository *Repository) FindClientSecretAuthentication(
	ctx context.Context,
	credentialID string,
) (ClientSecretAuthenticationRecord, error) {
	if repository == nil || repository.db == nil || ctx == nil || !validRepositoryUUID(credentialID) {
		return ClientSecretAuthenticationRecord{}, ErrRepositoryInvalid
	}
	var value ClientSecretAuthenticationRecord
	var clientStatus, principalStatus, authMethod, credentialType string
	var expiresAt, revokedAt sql.NullTime
	err := repository.db.QueryRowContext(ctx, `
		SELECT c.workspace_id,c.id,c.client_id,c.service_principal_id,
		       p.security_version,c.status,p.status,c.auth_method,c.token_ttl_seconds,
		       k.id,k.credential_type,k.secret_hash,k.valid_from,k.expires_at,k.revoked_at
		FROM agent_access_credentials k
		JOIN agent_access_clients c
		  ON c.id=k.client_id AND c.workspace_id=k.workspace_id
		JOIN service_principals p
		  ON p.id=c.service_principal_id AND p.workspace_id=c.workspace_id
		WHERE k.id=$1
	`, credentialID).Scan(
		&value.WorkspaceID, &value.ClientID, &value.PublicClientID,
		&value.ServicePrincipalID, &value.ServicePrincipalVersion,
		&clientStatus, &principalStatus, &authMethod, &value.TokenTTLSeconds,
		&value.CredentialID, &credentialType, &value.SecretHash, &value.ValidFrom,
		&expiresAt, &revokedAt,
	)
	if err != nil {
		return ClientSecretAuthenticationRecord{}, mapRepositoryRead("find Client Secret authentication", err)
	}
	var ok bool
	if value.ClientStatus, ok = ParseStatus(clientStatus); !ok {
		return ClientSecretAuthenticationRecord{}, ErrRepositoryInvalid
	}
	if value.ServicePrincipalStatus, ok = ParseStatus(principalStatus); !ok {
		return ClientSecretAuthenticationRecord{}, ErrRepositoryInvalid
	}
	if value.AuthMethod, ok = ParseClientAuthMethod(authMethod); !ok {
		return ClientSecretAuthenticationRecord{}, ErrRepositoryInvalid
	}
	if value.CredentialType, ok = ParseCredentialType(credentialType); !ok {
		return ClientSecretAuthenticationRecord{}, ErrRepositoryInvalid
	}
	value.ExpiresAt, value.RevokedAt = repositoryTimePointer(expiresAt), repositoryTimePointer(revokedAt)
	value.SecretHash = append([]byte(nil), value.SecretHash...)
	return value, nil
}

// RecordClientSecretAuthenticated updates last_used_at at most once per minute
// and rechecks all mutable authentication state after the hash verification.
func (repository *Repository) RecordClientSecretAuthenticated(
	ctx context.Context,
	credentialID, publicClientID string,
	usedAt time.Time,
) error {
	if repository == nil || repository.db == nil || ctx == nil || !validRepositoryUUID(credentialID) ||
		publicClientID == "" || usedAt.IsZero() {
		return ErrRepositoryInvalid
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE agent_access_credentials k
		SET last_used_at=$3
		FROM agent_access_clients c, service_principals p
		WHERE k.id=$1
		  AND k.client_id=c.id AND k.workspace_id=c.workspace_id
		  AND c.service_principal_id=p.id AND c.workspace_id=p.workspace_id
		  AND c.client_id=$2
		  AND c.status='ACTIVE' AND p.status='ACTIVE'
		  AND c.auth_method='client_secret_basic' AND k.credential_type='client_secret'
		  AND k.secret_hash IS NOT NULL AND octet_length(k.secret_hash)=32
		  AND k.valid_from <= $3 AND (k.expires_at IS NULL OR k.expires_at > $3)
		  AND k.revoked_at IS NULL
		  AND (k.last_used_at IS NULL OR k.last_used_at < $3 - interval '1 minute')
	`, credentialID, publicClientID, repositoryTime(usedAt))
	if err != nil {
		return mapRepositoryWrite("record Client Secret authentication", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 1 {
		return nil
	}
	var current bool
	if err := repository.db.QueryRowContext(ctx, `
		SELECT EXISTS (
		 SELECT 1
		 FROM agent_access_credentials k
		 JOIN agent_access_clients c ON c.id=k.client_id AND c.workspace_id=k.workspace_id
		 JOIN service_principals p ON p.id=c.service_principal_id AND p.workspace_id=c.workspace_id
		 WHERE k.id=$1 AND c.client_id=$2
		   AND c.status='ACTIVE' AND p.status='ACTIVE'
		   AND c.auth_method='client_secret_basic' AND k.credential_type='client_secret'
		   AND k.secret_hash IS NOT NULL AND octet_length(k.secret_hash)=32
		   AND k.valid_from <= $3 AND (k.expires_at IS NULL OR k.expires_at > $3)
		   AND k.revoked_at IS NULL
		)
	`, credentialID, publicClientID, usedAt.UTC()).Scan(&current); err != nil {
		return mapRepositoryRead("recheck Client Secret authentication", err)
	}
	if !current {
		return ErrRepositoryNotFound
	}
	return nil
}

func (repository *Repository) ListCredentials(
	ctx context.Context,
	workspaceID, clientID string,
) ([]Credential, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) {
		return nil, ErrRepositoryInvalid
	}
	rows, err := repository.db.QueryContext(ctx, credentialSelect+`
		WHERE workspace_id=$1 AND client_id=$2 ORDER BY created_at,id`, workspaceID, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]Credential, 0)
	for rows.Next() {
		value, err := scanCredentialEvidence(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value.Credential)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func (repository *Repository) RecordCredentialUsed(
	ctx context.Context,
	workspaceID, clientID, credentialID string,
	usedAt time.Time,
) error {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		!validRepositoryUUID(credentialID) || usedAt.IsZero() {
		return ErrRepositoryInvalid
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE agent_access_credentials SET last_used_at=$4
		WHERE workspace_id=$1 AND client_id=$2 AND id=$3
	`, workspaceID, clientID, credentialID, usedAt.UTC())
	if err != nil {
		return mapRepositoryWrite("record Agent Access Credential use", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrRepositoryNotFound
	}
	return nil
}

func (repository *Repository) RevokeCredentialCAS(
	ctx context.Context,
	workspaceID, clientID, credentialID, actorID string,
	expectedLockVersion int64,
	revokedAt time.Time,
) (Credential, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		!validRepositoryUUID(credentialID) || !validRepositoryUUID(actorID) ||
		expectedLockVersion < 1 || revokedAt.IsZero() {
		return Credential{}, ErrRepositoryInvalid
	}
	current, err := repository.GetCredential(ctx, workspaceID, clientID, credentialID)
	if err != nil {
		return Credential{}, err
	}
	if current.LockVersion != expectedLockVersion {
		return Credential{}, ErrRepositoryConflict
	}
	evidence, err := scanCredentialEvidence(repository.db.QueryRowContext(ctx, `
		UPDATE agent_access_credentials
		SET revoked_at=$4,revoked_by=$5,lock_version=lock_version+1
		WHERE workspace_id=$1 AND client_id=$2 AND id=$3 AND lock_version=$6 AND revoked_at IS NULL
		RETURNING id,workspace_id,client_id,credential_type,public_hint,
		 valid_from,expires_at,last_used_at,revoked_at,revoked_by,created_by,created_at,
		 lock_version,secret_hash,jwk_thumbprint,certificate_thumbprint
	`, workspaceID, clientID, credentialID, revokedAt.UTC(), actorID, expectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, ErrRepositoryConflict
	}
	return evidence.Credential, mapRepositoryWrite("revoke Agent Access Credential", err)
}

const credentialSelect = `
	SELECT id,workspace_id,client_id,credential_type,public_hint,
	 valid_from,expires_at,last_used_at,revoked_at,revoked_by,created_by,created_at,
	 lock_version,secret_hash,jwk_thumbprint,certificate_thumbprint
	FROM agent_access_credentials `

func scanCredentialEvidence(scanner repositoryScanner) (CredentialEvidence, error) {
	var value CredentialEvidence
	var credentialType string
	var expires, used, revoked sql.NullTime
	var revokedBy sql.NullString
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &value.ClientID, &credentialType,
		&value.PublicHint, &value.ValidFrom, &expires, &used, &revoked, &revokedBy,
		&value.CreatedBy, &value.CreatedAt, &value.LockVersion, &value.SecretHash,
		&value.JWKThumbprint, &value.CertificateThumbprint,
	)
	if err != nil {
		return CredentialEvidence{}, err
	}
	parsed, ok := ParseCredentialType(credentialType)
	if !ok {
		return CredentialEvidence{}, ErrRepositoryInvalid
	}
	value.Type = parsed
	value.ExpiresAt, value.LastUsedAt = repositoryTimePointer(expires), repositoryTimePointer(used)
	value.RevokedAt, value.RevokedBy = repositoryTimePointer(revoked), revokedBy.String
	value.SecretHash = append([]byte(nil), value.SecretHash...)
	value.JWKThumbprint = append([]byte(nil), value.JWKThumbprint...)
	value.CertificateThumbprint = append([]byte(nil), value.CertificateThumbprint...)
	return value, nil
}

func nullableRepositoryBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return append([]byte(nil), value...)
}
