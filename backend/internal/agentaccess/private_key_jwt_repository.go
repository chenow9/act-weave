package agentaccess

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type PrivateKeyJWTCredentialRecord struct {
	CredentialID  string
	JWKThumbprint []byte `json:"-"`
	ValidFrom     time.Time
	ExpiresAt     *time.Time
	RevokedAt     *time.Time
}

type PrivateKeyJWTAuthenticationRecord struct {
	WorkspaceID             string
	ClientID                string
	PublicClientID          string
	ServicePrincipalID      string
	ServicePrincipalVersion int64
	ClientStatus            Status
	ServicePrincipalStatus  Status
	AuthMethod              ClientAuthMethod
	JWKSURI                 string
	TokenTTLSeconds         int
	Credentials             []PrivateKeyJWTCredentialRecord
}

func (repository *Repository) FindPrivateKeyJWTAuthentication(
	ctx context.Context,
	publicClientID string,
) (PrivateKeyJWTAuthenticationRecord, error) {
	if repository == nil || repository.db == nil || ctx == nil || publicClientID == "" {
		return PrivateKeyJWTAuthenticationRecord{}, ErrRepositoryInvalid
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT c.workspace_id,c.id,c.client_id,c.service_principal_id,
		       p.security_version,c.status,p.status,c.auth_method,c.jwks_uri,c.token_ttl_seconds,
		       k.id,k.jwk_thumbprint,k.valid_from,k.expires_at,k.revoked_at
		FROM agent_access_clients c
		JOIN service_principals p
		  ON p.id=c.service_principal_id AND p.workspace_id=c.workspace_id
		JOIN agent_access_credentials k
		  ON k.client_id=c.id AND k.workspace_id=c.workspace_id AND k.credential_type='jwk'
		WHERE c.client_id=$1
		ORDER BY k.valid_from,k.id
	`, publicClientID)
	if err != nil {
		return PrivateKeyJWTAuthenticationRecord{}, fmt.Errorf("find private_key_jwt authentication: %w", err)
	}
	defer rows.Close()
	var result PrivateKeyJWTAuthenticationRecord
	for rows.Next() {
		var workspaceID, clientID, resolvedPublicID, principalID string
		var securityVersion int64
		var clientStatus, principalStatus, authMethod string
		var jwksURI sql.NullString
		var tokenTTL int
		var credential PrivateKeyJWTCredentialRecord
		var expiresAt, revokedAt sql.NullTime
		if err := rows.Scan(
			&workspaceID, &clientID, &resolvedPublicID, &principalID, &securityVersion,
			&clientStatus, &principalStatus, &authMethod, &jwksURI, &tokenTTL,
			&credential.CredentialID, &credential.JWKThumbprint, &credential.ValidFrom,
			&expiresAt, &revokedAt,
		); err != nil {
			return PrivateKeyJWTAuthenticationRecord{}, fmt.Errorf("scan private_key_jwt authentication: %w", err)
		}
		parsedClientStatus, ok := ParseStatus(clientStatus)
		if !ok {
			return PrivateKeyJWTAuthenticationRecord{}, ErrRepositoryInvalid
		}
		parsedPrincipalStatus, ok := ParseStatus(principalStatus)
		if !ok {
			return PrivateKeyJWTAuthenticationRecord{}, ErrRepositoryInvalid
		}
		parsedAuthMethod, ok := ParseClientAuthMethod(authMethod)
		if !ok {
			return PrivateKeyJWTAuthenticationRecord{}, ErrRepositoryInvalid
		}
		if len(result.Credentials) == 0 {
			result = PrivateKeyJWTAuthenticationRecord{
				WorkspaceID: workspaceID, ClientID: clientID, PublicClientID: resolvedPublicID,
				ServicePrincipalID: principalID, ServicePrincipalVersion: securityVersion,
				ClientStatus: parsedClientStatus, ServicePrincipalStatus: parsedPrincipalStatus,
				AuthMethod: parsedAuthMethod, JWKSURI: jwksURI.String, TokenTTLSeconds: tokenTTL,
				Credentials: make([]PrivateKeyJWTCredentialRecord, 0, 2),
			}
		} else if result.WorkspaceID != workspaceID || result.ClientID != clientID ||
			result.ServicePrincipalID != principalID || result.PublicClientID != resolvedPublicID {
			return PrivateKeyJWTAuthenticationRecord{}, ErrRepositoryInvalid
		}
		credential.JWKThumbprint = append([]byte(nil), credential.JWKThumbprint...)
		credential.ExpiresAt, credential.RevokedAt = repositoryTimePointer(expiresAt), repositoryTimePointer(revokedAt)
		result.Credentials = append(result.Credentials, credential)
	}
	if err := rows.Err(); err != nil {
		return PrivateKeyJWTAuthenticationRecord{}, fmt.Errorf("iterate private_key_jwt authentication: %w", err)
	}
	if len(result.Credentials) == 0 {
		return PrivateKeyJWTAuthenticationRecord{}, ErrRepositoryNotFound
	}
	return result, nil
}

func (repository *Repository) RecordPrivateKeyJWTAuthenticated(
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
		  AND c.auth_method='private_key_jwt' AND k.credential_type='jwk'
		  AND k.jwk_thumbprint IS NOT NULL AND octet_length(k.jwk_thumbprint)=32
		  AND k.valid_from <= $3 AND (k.expires_at IS NULL OR k.expires_at > $3)
		  AND k.revoked_at IS NULL
		  AND (k.last_used_at IS NULL OR k.last_used_at < $3 - interval '1 minute')
	`, credentialID, publicClientID, usedAt.UTC())
	if err != nil {
		return mapRepositoryWrite("record private_key_jwt authentication", err)
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
		   AND c.auth_method='private_key_jwt' AND k.credential_type='jwk'
		   AND k.jwk_thumbprint IS NOT NULL AND octet_length(k.jwk_thumbprint)=32
		   AND k.valid_from <= $3 AND (k.expires_at IS NULL OR k.expires_at > $3)
		   AND k.revoked_at IS NULL
		)
	`, credentialID, publicClientID, usedAt.UTC()).Scan(&current); err != nil {
		return mapRepositoryRead("recheck private_key_jwt authentication", err)
	}
	if !current {
		return ErrRepositoryNotFound
	}
	return nil
}

func (repository *Repository) ClaimClientAssertionJTI(
	ctx context.Context,
	clientID string,
	jtiHash []byte,
	expiresAt, now time.Time,
) (bool, error) {
	return repository.claimTokenJTI(ctx, "agent_access_client_assertion_jtis",
		"Client Assertion JTI", clientID, jtiHash, expiresAt, now)
}

func (repository *Repository) ClaimSubjectTokenJTI(
	ctx context.Context,
	clientID string,
	jtiHash []byte,
	expiresAt, now time.Time,
) (bool, error) {
	return repository.claimTokenJTI(ctx, "agent_access_subject_token_jtis",
		"Subject Token JTI", clientID, jtiHash, expiresAt, now)
}

func (repository *Repository) claimTokenJTI(
	ctx context.Context,
	table, label, clientID string,
	jtiHash []byte,
	expiresAt, now time.Time,
) (bool, error) {
	allowed := map[string]bool{
		"agent_access_client_assertion_jtis": true,
		"agent_access_subject_token_jtis":    true,
	}
	if repository == nil || repository.db == nil || ctx == nil || !allowed[table] ||
		!validRepositoryUUID(clientID) || len(jtiHash) != 32 || expiresAt.IsZero() ||
		now.IsZero() || !expiresAt.After(now) {
		return false, ErrRepositoryInvalid
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin %s claim: %w", label, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM `+table+` WHERE client_id=$1 AND expires_at <= $2`,
		clientID, now.UTC()); err != nil {
		return false, fmt.Errorf("prune %ss: %w", label, err)
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO `+table+`(client_id,jti_hash,expires_at,created_at)
		 VALUES($1,$2,$3,$4)
		 ON CONFLICT (client_id,jti_hash) DO NOTHING`,
		clientID, append([]byte(nil), jtiHash...), expiresAt.UTC(), now.UTC())
	if err != nil {
		return false, mapRepositoryWrite("claim "+label, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count > 1 {
		return false, errors.New("invalid " + label + " claim count")
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit %s claim: %w", label, err)
	}
	return count == 1, nil
}
