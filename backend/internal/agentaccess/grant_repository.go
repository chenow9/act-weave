package agentaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type AgentGrant struct {
	ID, WorkspaceID, ClientID, AgentID string
	Scopes                             []AgentScope
	Policy                             GrantPolicy
	Status                             GrantStatus
	ValidFrom                          time.Time
	ExpiresAt                          *time.Time
	RevokedAt                          *time.Time
	RevokedBy                          string
	CreatedBy                          string
	UpdatedBy                          string
	CreatedAt                          time.Time
	UpdatedAt                          time.Time
	LockVersion                        int64
}

type CreateGrantInput struct {
	ID, WorkspaceID, ClientID, AgentID string
	Scopes                             []AgentScope
	Policy                             GrantPolicy
	ValidFrom                          time.Time
	ExpiresAt                          *time.Time
	ActorID                            string
}

func (repository *Repository) CreateGrant(
	ctx context.Context,
	input CreateGrantInput,
) (AgentGrant, error) {
	if repository == nil || repository.db == nil || !validCreateGrantInput(ctx, input) {
		return AgentGrant{}, ErrRepositoryInvalid
	}
	return createGrantWithQueryer(ctx, repository.db, input)
}

type grantQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func validCreateGrantInput(ctx context.Context, input CreateGrantInput) bool {
	return ctx != nil && validRepositoryUUID(input.ID) && validRepositoryUUID(input.WorkspaceID) &&
		validRepositoryUUID(input.ClientID) && validRepositoryUUID(input.AgentID) &&
		validRepositoryUUID(input.ActorID) && !input.ValidFrom.IsZero()
}

func createGrantWithQueryer(
	ctx context.Context,
	queryer grantQueryRower,
	input CreateGrantInput,
) (AgentGrant, error) {
	configuration := GrantConfiguration{Scopes: input.Scopes, Policy: input.Policy}
	raw, err := json.Marshal(configuration)
	if err != nil {
		return AgentGrant{}, ErrRepositoryInvalid
	}
	validated, err := ValidateGrantConfiguration(raw)
	if err != nil {
		return AgentGrant{}, err
	}
	scopes, _ := json.Marshal(validated.Scopes)
	policy, _ := json.Marshal(validated.Policy)
	value, err := scanGrant(queryer.QueryRowContext(ctx, `
		INSERT INTO agent_access_grants(
		 id,workspace_id,client_id,agent_id,scopes,policy,valid_from,expires_at,created_by,updated_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
		RETURNING id,workspace_id,client_id,agent_id,scopes,policy,status,valid_from,
		 expires_at,revoked_at,revoked_by,created_by,updated_by,created_at,updated_at,lock_version
	`, input.ID, input.WorkspaceID, input.ClientID, input.AgentID, scopes, policy,
		input.ValidFrom.UTC(), input.ExpiresAt, input.ActorID))
	return value, mapRepositoryWrite("create Agent Access Grant", err)
}

func (repository *Repository) GetGrant(
	ctx context.Context,
	workspaceID, clientID, agentID, grantID string,
) (AgentGrant, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		!validRepositoryUUID(agentID) || !validRepositoryUUID(grantID) {
		return AgentGrant{}, ErrRepositoryInvalid
	}
	value, err := scanGrant(repository.db.QueryRowContext(ctx, grantSelect+`
		WHERE workspace_id=$1 AND client_id=$2 AND agent_id=$3 AND id=$4`,
		workspaceID, clientID, agentID, grantID))
	return value, mapRepositoryRead("get Agent Access Grant", err)
}

func (repository *Repository) GetGrantByID(
	ctx context.Context,
	workspaceID, clientID, grantID string,
) (AgentGrant, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		!validRepositoryUUID(grantID) {
		return AgentGrant{}, ErrRepositoryInvalid
	}
	value, err := scanGrant(repository.db.QueryRowContext(ctx, grantSelect+`
		WHERE workspace_id=$1 AND client_id=$2 AND id=$3`, workspaceID, clientID, grantID))
	return value, mapRepositoryRead("get Agent Access Grant", err)
}

func (repository *Repository) ListGrants(
	ctx context.Context,
	workspaceID, clientID string,
) ([]AgentGrant, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) {
		return nil, ErrRepositoryInvalid
	}
	rows, err := repository.db.QueryContext(ctx, grantSelect+`
		WHERE workspace_id=$1 AND client_id=$2 ORDER BY created_at,id`, workspaceID, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]AgentGrant, 0)
	for rows.Next() {
		value, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func (repository *Repository) RevokeGrantCAS(
	ctx context.Context,
	workspaceID, clientID, agentID, grantID, actorID string,
	expectedLockVersion int64,
	revokedAt time.Time,
) (AgentGrant, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		!validRepositoryUUID(agentID) || !validRepositoryUUID(grantID) ||
		!validRepositoryUUID(actorID) || expectedLockVersion < 1 || revokedAt.IsZero() {
		return AgentGrant{}, ErrRepositoryInvalid
	}
	current, err := repository.GetGrant(ctx, workspaceID, clientID, agentID, grantID)
	if err != nil {
		return AgentGrant{}, err
	}
	if current.LockVersion != expectedLockVersion {
		return AgentGrant{}, ErrRepositoryConflict
	}
	value, err := scanGrant(repository.db.QueryRowContext(ctx, `
		UPDATE agent_access_grants
		SET status='REVOKED',revoked_at=$3,revoked_by=$4,updated_at=clock_timestamp(),updated_by=$4,
		 lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND client_id=$6 AND agent_id=$7
		 AND lock_version=$5 AND status='ACTIVE'
		RETURNING id,workspace_id,client_id,agent_id,scopes,policy,status,valid_from,
		 expires_at,revoked_at,revoked_by,created_by,updated_by,created_at,updated_at,lock_version
	`, workspaceID, grantID, revokedAt.UTC(), actorID, expectedLockVersion, clientID, agentID))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentGrant{}, ErrRepositoryConflict
	}
	return value, mapRepositoryWrite("revoke Agent Access Grant", err)
}

const grantSelect = `
	SELECT id,workspace_id,client_id,agent_id,scopes,policy,status,valid_from,
	 expires_at,revoked_at,revoked_by,created_by,updated_by,created_at,updated_at,lock_version
	FROM agent_access_grants `

func scanGrant(scanner repositoryScanner) (AgentGrant, error) {
	var value AgentGrant
	var scopes, policy []byte
	var status string
	var expires, revoked sql.NullTime
	var revokedBy sql.NullString
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &value.ClientID, &value.AgentID,
		&scopes, &policy, &status, &value.ValidFrom, &expires, &revoked,
		&revokedBy, &value.CreatedBy, &value.UpdatedBy, &value.CreatedAt,
		&value.UpdatedAt, &value.LockVersion,
	)
	if err != nil {
		return AgentGrant{}, err
	}
	configurationRaw, err := json.Marshal(map[string]json.RawMessage{
		"scopes": scopes, "policy": policy,
	})
	if err != nil {
		return AgentGrant{}, ErrRepositoryInvalid
	}
	configuration, err := ValidateGrantConfiguration(configurationRaw)
	if err != nil {
		return AgentGrant{}, ErrRepositoryInvalid
	}
	value.Scopes, value.Policy = configuration.Scopes, configuration.Policy
	value.Status = GrantStatus(status)
	if value.Status != GrantStatusActive && value.Status != GrantStatusRevoked {
		return AgentGrant{}, ErrRepositoryInvalid
	}
	value.ExpiresAt, value.RevokedAt = repositoryTimePointer(expires), repositoryTimePointer(revoked)
	value.RevokedBy = revokedBy.String
	return value, nil
}

type AccessBinding struct {
	Principal ServicePrincipal
	Client    AgentAccessClient
	Grant     AgentGrant
}

func (repository *Repository) ResolveAccess(
	ctx context.Context,
	workspaceID, publicClientID, agentID string,
	at time.Time,
) (AccessBinding, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(agentID) ||
		publicClientID == "" || at.IsZero() {
		return AccessBinding{}, ErrRepositoryInvalid
	}
	var principalID, clientID, grantID string
	err := repository.db.QueryRowContext(ctx, `
		SELECT p.id,c.id,g.id
		FROM agent_access_clients c
		JOIN service_principals p
		  ON p.workspace_id=c.workspace_id AND p.id=c.service_principal_id
		JOIN agent_access_grants g
		  ON g.workspace_id=c.workspace_id AND g.client_id=c.id AND g.agent_id=$3
		JOIN agents a
		  ON a.workspace_id=g.workspace_id AND a.id=g.agent_id
		JOIN workspaces w ON w.id=c.workspace_id
		WHERE c.workspace_id=$1 AND c.client_id=$2
		  AND c.status='ACTIVE' AND p.status='ACTIVE' AND g.status='ACTIVE'
		  AND a.status='ACTIVE' AND a.deleted_at IS NULL
		  AND w.status='ACTIVE' AND w.deleted_at IS NULL
		  AND g.valid_from <= $4 AND (g.expires_at IS NULL OR g.expires_at > $4)
	`, workspaceID, publicClientID, agentID, at.UTC()).Scan(&principalID, &clientID, &grantID)
	if err != nil {
		return AccessBinding{}, mapRepositoryRead("resolve Agent Access binding", err)
	}
	principal, err := repository.GetServicePrincipal(ctx, workspaceID, principalID)
	if err != nil {
		return AccessBinding{}, err
	}
	client, err := repository.GetClient(ctx, workspaceID, clientID)
	if err != nil {
		return AccessBinding{}, err
	}
	grant, err := repository.GetGrant(ctx, workspaceID, clientID, agentID, grantID)
	if err != nil {
		return AccessBinding{}, err
	}
	return AccessBinding{Principal: principal, Client: client, Grant: grant}, nil
}
