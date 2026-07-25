package agentaccess

import (
	"context"
	"encoding/json"
	"time"
)

type AAPAuthorizationStateRecord struct {
	WorkspaceID            string
	AgentID                string
	ClientID               string
	PublicClientID         string
	ServicePrincipalID     string
	CurrentSecurityVersion int64
	GrantID                string
	GrantScopes            []AgentScope
	GrantPolicy            GrantPolicy
	WorkspaceVersion       int64
	ClientVersion          int64
	GrantVersion           int64
	AgentPolicyVersion     int64
}

// ResolveAAPAuthorizationState is a data-plane query. Its first predicates are
// the Workspace and Agent from the verified Token; it never joins Workspace
// membership or derives external permissions from an internal User role.
func (repository *Repository) ResolveAAPAuthorizationState(
	ctx context.Context,
	workspaceID, agentID, publicClientID, servicePrincipalID string,
	at time.Time,
) (AAPAuthorizationStateRecord, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(agentID) ||
		publicClientID == "" || !validRepositoryUUID(servicePrincipalID) || at.IsZero() {
		return AAPAuthorizationStateRecord{}, ErrRepositoryInvalid
	}
	var value AAPAuthorizationStateRecord
	var scopes, policy []byte
	err := repository.db.QueryRowContext(ctx, `
		SELECT w.id,g.agent_id,c.id,c.client_id,p.id,p.security_version,g.id,
		       g.scopes,g.policy,w.lock_version,c.lock_version,g.lock_version,a.lock_version
		FROM agent_access_clients c
		JOIN service_principals p
		  ON p.workspace_id=c.workspace_id AND p.id=c.service_principal_id
		JOIN agent_access_grants g
		  ON g.workspace_id=c.workspace_id AND g.client_id=c.id
		JOIN agents a
		  ON a.workspace_id=g.workspace_id AND a.id=g.agent_id
		JOIN workspaces w ON w.id=c.workspace_id
		WHERE c.workspace_id=$1 AND g.agent_id=$2
		  AND c.client_id=$3 AND p.id=$4
		  AND c.status='ACTIVE' AND p.status='ACTIVE'
		  AND w.status='ACTIVE' AND w.deleted_at IS NULL
		  AND a.status='ACTIVE' AND a.deleted_at IS NULL
		  AND g.status='ACTIVE' AND g.valid_from <= $5
		  AND (g.expires_at IS NULL OR g.expires_at > $5)
	`, workspaceID, agentID, publicClientID, servicePrincipalID, at.UTC()).Scan(
		&value.WorkspaceID, &value.AgentID, &value.ClientID, &value.PublicClientID,
		&value.ServicePrincipalID, &value.CurrentSecurityVersion, &value.GrantID,
		&scopes, &policy, &value.WorkspaceVersion, &value.ClientVersion,
		&value.GrantVersion, &value.AgentPolicyVersion,
	)
	if err != nil {
		return AAPAuthorizationStateRecord{}, mapRepositoryRead("resolve AAP authorization state", err)
	}
	configurationRaw, err := json.Marshal(map[string]json.RawMessage{
		"scopes": scopes, "policy": policy,
	})
	if err != nil {
		return AAPAuthorizationStateRecord{}, ErrRepositoryInvalid
	}
	configuration, err := ValidateGrantConfiguration(configurationRaw)
	if err != nil {
		return AAPAuthorizationStateRecord{}, ErrRepositoryInvalid
	}
	value.GrantScopes = append([]AgentScope(nil), configuration.Scopes...)
	value.GrantPolicy = configuration.Policy
	return value, nil
}
