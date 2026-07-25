package agentaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type ManagementCommandState string

const (
	ManagementCommandPending   ManagementCommandState = "PENDING"
	ManagementCommandCompleted ManagementCommandState = "COMPLETED"
)

type ManagementCommand struct {
	WorkspaceID, ActorID, IdempotencyKey string
	Operation                            string
	RequestHash                          []byte
	State                                ManagementCommandState
	ResponseStatus                       int
	ResponseBody                         json.RawMessage
	CreatedAt                            time.Time
	CompletedAt                          *time.Time
}

type ClaimManagementCommandInput struct {
	WorkspaceID, ActorID, IdempotencyKey string
	Operation                            string
	RequestHash                          []byte
}

func (repository *Repository) ClaimManagementCommand(
	ctx context.Context,
	input ClaimManagementCommandInput,
) (ManagementCommand, bool, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(input.WorkspaceID) || !validRepositoryUUID(input.ActorID) ||
		!validRepositoryUUID(input.IdempotencyKey) || len(input.RequestHash) != 32 ||
		strings.TrimSpace(input.Operation) != input.Operation || input.Operation == "" ||
		len(input.Operation) > 255 {
		return ManagementCommand{}, false, ErrRepositoryInvalid
	}
	value, err := scanManagementCommand(repository.db.QueryRowContext(ctx, `
		INSERT INTO agent_access_management_commands(
		 workspace_id,actor_id,idempotency_key,operation,request_hash
		) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (workspace_id,actor_id,idempotency_key) DO NOTHING
		RETURNING workspace_id,actor_id,idempotency_key,operation,request_hash,state,
		 response_status,response_body,created_at,completed_at
	`, input.WorkspaceID, input.ActorID, input.IdempotencyKey, input.Operation,
		append([]byte(nil), input.RequestHash...)))
	if err == nil {
		return value, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ManagementCommand{}, false, mapRepositoryWrite("claim Agent Access management command", err)
	}
	value, err = repository.GetManagementCommand(
		ctx, input.WorkspaceID, input.ActorID, input.IdempotencyKey,
	)
	return value, false, err
}

func (repository *Repository) GetManagementCommand(
	ctx context.Context,
	workspaceID, actorID, idempotencyKey string,
) (ManagementCommand, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(actorID) ||
		!validRepositoryUUID(idempotencyKey) {
		return ManagementCommand{}, ErrRepositoryInvalid
	}
	value, err := scanManagementCommand(repository.db.QueryRowContext(ctx, `
		SELECT workspace_id,actor_id,idempotency_key,operation,request_hash,state,
		 response_status,response_body,created_at,completed_at
		FROM agent_access_management_commands
		WHERE workspace_id=$1 AND actor_id=$2 AND idempotency_key=$3
	`, workspaceID, actorID, idempotencyKey))
	return value, mapRepositoryRead("get Agent Access management command", err)
}

func (repository *Repository) CompleteManagementCommand(
	ctx context.Context,
	workspaceID, actorID, idempotencyKey string,
	requestHash []byte,
	responseStatus int,
	responseBody json.RawMessage,
) (ManagementCommand, error) {
	var object map[string]json.RawMessage
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(actorID) ||
		!validRepositoryUUID(idempotencyKey) || len(requestHash) != 32 ||
		responseStatus < 200 || responseStatus > 299 ||
		json.Unmarshal(responseBody, &object) != nil || object == nil {
		return ManagementCommand{}, ErrRepositoryInvalid
	}
	value, err := scanManagementCommand(repository.db.QueryRowContext(ctx, `
		UPDATE agent_access_management_commands
		SET state='COMPLETED',response_status=$5,response_body=$6,
		 completed_at=clock_timestamp()
		WHERE workspace_id=$1 AND actor_id=$2 AND idempotency_key=$3
		 AND request_hash=$4 AND state='PENDING'
		RETURNING workspace_id,actor_id,idempotency_key,operation,request_hash,state,
		 response_status,response_body,created_at,completed_at
	`, workspaceID, actorID, idempotencyKey, requestHash, responseStatus, responseBody))
	if errors.Is(err, sql.ErrNoRows) {
		return ManagementCommand{}, ErrRepositoryConflict
	}
	return value, mapRepositoryWrite("complete Agent Access management command", err)
}

func scanManagementCommand(scanner repositoryScanner) (ManagementCommand, error) {
	var value ManagementCommand
	var state string
	var responseStatus sql.NullInt64
	var responseBody []byte
	var completedAt sql.NullTime
	if err := scanner.Scan(
		&value.WorkspaceID, &value.ActorID, &value.IdempotencyKey, &value.Operation,
		&value.RequestHash, &state, &responseStatus, &responseBody,
		&value.CreatedAt, &completedAt,
	); err != nil {
		return ManagementCommand{}, err
	}
	value.State = ManagementCommandState(state)
	if value.State != ManagementCommandPending && value.State != ManagementCommandCompleted {
		return ManagementCommand{}, ErrRepositoryInvalid
	}
	value.RequestHash = append([]byte(nil), value.RequestHash...)
	value.ResponseStatus = int(responseStatus.Int64)
	value.ResponseBody = append(json.RawMessage(nil), responseBody...)
	value.CompletedAt = repositoryTimePointer(completedAt)
	return value, nil
}
