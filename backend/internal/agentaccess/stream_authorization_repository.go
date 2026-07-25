package agentaccess

import (
	"context"
	"time"
)

type StreamAuthorizationStateRecord struct {
	WorkspaceID, AgentID, ClientID, GrantID, ServicePrincipalID string
	SecurityVersion                                             int64
}

// ResolveAAPStreamAuthorizationState scopes by Workspace and Agent before all
// other binding identifiers. Absence includes every disabled/revoked/expired
// state and is intentionally indistinguishable to the data-plane caller.
func (repository *Repository) ResolveAAPStreamAuthorizationState(
	ctx context.Context,
	workspaceID, agentID, clientID, grantID, servicePrincipalID string,
	at time.Time,
) (StreamAuthorizationStateRecord, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(agentID) ||
		!validRepositoryUUID(clientID) || !validRepositoryUUID(grantID) ||
		!validRepositoryUUID(servicePrincipalID) || at.IsZero() {
		return StreamAuthorizationStateRecord{}, ErrRepositoryInvalid
	}
	var value StreamAuthorizationStateRecord
	err := repository.db.QueryRowContext(ctx, `
		SELECT w.id,a.id,c.id,g.id,p.id,p.security_version
		FROM agent_access_clients c
		JOIN service_principals p
		  ON p.workspace_id=c.workspace_id AND p.id=c.service_principal_id
		JOIN agent_access_grants g
		  ON g.workspace_id=c.workspace_id AND g.client_id=c.id
		JOIN agents a
		  ON a.workspace_id=g.workspace_id AND a.id=g.agent_id
		JOIN workspaces w ON w.id=c.workspace_id
		WHERE c.workspace_id=$1 AND g.agent_id=$2
		  AND c.id=$3 AND g.id=$4 AND p.id=$5
		  AND c.status='ACTIVE' AND p.status='ACTIVE'
		  AND w.status='ACTIVE' AND w.deleted_at IS NULL
		  AND a.status='ACTIVE' AND a.deleted_at IS NULL
		  AND g.status='ACTIVE' AND g.valid_from <= $6
		  AND (g.expires_at IS NULL OR g.expires_at > $6)
	`, workspaceID, agentID, clientID, grantID, servicePrincipalID, at.UTC()).Scan(
		&value.WorkspaceID, &value.AgentID, &value.ClientID, &value.GrantID,
		&value.ServicePrincipalID, &value.SecurityVersion,
	)
	if err != nil {
		return StreamAuthorizationStateRecord{}, mapRepositoryRead("resolve AAP stream authorization state", err)
	}
	return value, nil
}
