package agentaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

const (
	DefaultAccessTokenTTLSeconds = 600
	MinimumAccessTokenTTLSeconds = 300
	MaximumAccessTokenTTLSeconds = 900
)

type ClientCredentialsTokenGrantRecord struct {
	GrantID                 string
	WorkspaceID             string
	ClientID                string
	PublicClientID          string
	ServicePrincipalID      string
	ServicePrincipalVersion int64
	AgentID                 string
	Scopes                  []AgentScope
	TokenTTLSeconds         int
	GrantExpiresAt          *time.Time
}

// ResolveClientCredentialsTokenGrant performs the post-authentication state
// check in one database snapshot. It deliberately includes the Credential so
// a revocation racing with Client authentication cannot result in a new token.
func (repository *Repository) ResolveClientCredentialsTokenGrant(
	ctx context.Context,
	workspaceID, clientID, publicClientID, servicePrincipalID, credentialID, agentID string,
	servicePrincipalVersion int64,
	at time.Time,
) (ClientCredentialsTokenGrantRecord, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		publicClientID == "" || !validRepositoryUUID(servicePrincipalID) ||
		!validRepositoryUUID(credentialID) || !validRepositoryUUID(agentID) ||
		servicePrincipalVersion < 1 || at.IsZero() {
		return ClientCredentialsTokenGrantRecord{}, ErrRepositoryInvalid
	}
	var value ClientCredentialsTokenGrantRecord
	var scopes []byte
	var expiresAt sql.NullTime
	err := repository.db.QueryRowContext(ctx, `
		SELECT g.id,c.workspace_id,c.id,c.client_id,p.id,p.security_version,g.agent_id,
		       g.scopes,c.token_ttl_seconds,g.expires_at
		FROM agent_access_clients c
		JOIN service_principals p
		  ON p.workspace_id=c.workspace_id AND p.id=c.service_principal_id
		JOIN agent_access_credentials k
		  ON k.workspace_id=c.workspace_id AND k.client_id=c.id
		JOIN agent_access_grants g
		  ON g.workspace_id=c.workspace_id AND g.client_id=c.id AND g.agent_id=$6
		JOIN agents a
		  ON a.workspace_id=g.workspace_id AND a.id=g.agent_id
		JOIN workspaces w ON w.id=c.workspace_id
		WHERE c.workspace_id=$1 AND c.id=$2 AND c.client_id=$3
		  AND p.id=$4 AND k.id=$5 AND p.security_version=$7
		  AND c.status='ACTIVE' AND p.status='ACTIVE'
		  AND w.status='ACTIVE' AND w.deleted_at IS NULL
		  AND a.status='ACTIVE' AND a.deleted_at IS NULL
		  AND g.status='ACTIVE' AND g.valid_from <= $8
		  AND (g.expires_at IS NULL OR g.expires_at > $8)
		  AND k.valid_from <= $8 AND (k.expires_at IS NULL OR k.expires_at > $8)
		  AND k.revoked_at IS NULL
		  AND (
		    (c.auth_method='client_secret_basic' AND k.credential_type='client_secret'
		      AND k.secret_hash IS NOT NULL AND octet_length(k.secret_hash)=32)
		    OR
		    (c.auth_method='private_key_jwt' AND k.credential_type='jwk'
		      AND k.jwk_thumbprint IS NOT NULL AND octet_length(k.jwk_thumbprint)=32)
		  )
		  AND c.token_ttl_seconds BETWEEN $9 AND $10
	`, workspaceID, clientID, publicClientID, servicePrincipalID, credentialID, agentID,
		servicePrincipalVersion, at.UTC(), MinimumAccessTokenTTLSeconds,
		MaximumAccessTokenTTLSeconds).Scan(
		&value.GrantID, &value.WorkspaceID, &value.ClientID, &value.PublicClientID,
		&value.ServicePrincipalID, &value.ServicePrincipalVersion, &value.AgentID,
		&scopes, &value.TokenTTLSeconds, &expiresAt,
	)
	if err != nil {
		return ClientCredentialsTokenGrantRecord{}, mapRepositoryRead("resolve Client Credentials token Grant", err)
	}
	configurationRaw, err := json.Marshal(map[string]json.RawMessage{
		"scopes": scopes, "policy": json.RawMessage(`{}`),
	})
	if err != nil {
		return ClientCredentialsTokenGrantRecord{}, ErrRepositoryInvalid
	}
	configuration, err := ValidateGrantConfiguration(configurationRaw)
	if err != nil {
		return ClientCredentialsTokenGrantRecord{}, ErrRepositoryInvalid
	}
	value.Scopes = append([]AgentScope(nil), configuration.Scopes...)
	value.GrantExpiresAt = repositoryTimePointer(expiresAt)
	return value, nil
}

func validAccessTokenTTLSeconds(value int) bool {
	return value >= MinimumAccessTokenTTLSeconds && value <= MaximumAccessTokenTTLSeconds
}
