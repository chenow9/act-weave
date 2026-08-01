package aap

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/agentaccessauth"
)

const (
	CommandConversationCreate = "conversation.create"
	CommandRunCreate          = "run.create"
	CommandRunCancel          = "run.cancel"
	CommandInteractionDecide  = "interaction.decide"
	CommandFileCreate         = "file.create"
	CommandFileComplete       = "file.complete"
	MinimumCommandReceiptTTL  = 24 * time.Hour
)

var (
	ErrCommandReceiptInvalid       = errors.New("AAP command receipt is invalid")
	ErrCommandIdempotencyConflict  = errors.New("AAP command idempotency key conflicts with another request")
	ErrCommandReceiptStateConflict = errors.New("AAP command receipt state conflicts with completion")
)

type CommandReceiptKey struct {
	WorkspaceID        string
	AgentID            string
	ClientID           string
	ServicePrincipalID string
	SubjectID          string
	Operation          string
	IdempotencyKey     string
}

type ObserveCommandInput struct {
	Key         CommandReceiptKey
	RequestHash []byte
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

type CompleteCommandInput struct {
	Key             CommandReceiptKey
	RequestHash     []byte
	ResourceType    string
	ResourceID      string
	ResponseVersion int64
}

type CommandReceipt struct {
	Key             CommandReceiptKey
	RequestHash     []byte
	ResourceType    string
	ResourceID      string
	ResponseVersion int64
	CreatedAt       time.Time
	ExpiresAt       time.Time
	Created         bool
}

type CommandReceiptLedger interface {
	Observe(context.Context, ObserveCommandInput) (CommandReceipt, error)
	Complete(context.Context, CompleteCommandInput) (CommandReceipt, error)
}

type CommandReceiptRepository struct {
	db  *sql.DB
	now func() time.Time
	ttl time.Duration
}

func NewCommandReceiptRepository(db *sql.DB) (*CommandReceiptRepository, error) {
	if db == nil {
		return nil, ErrCommandReceiptInvalid
	}
	return &CommandReceiptRepository{
		db: db, now: func() time.Time { return time.Now().UTC() },
		ttl: MinimumCommandReceiptTTL,
	}, nil
}

func (repository *CommandReceiptRepository) Observe(
	ctx context.Context,
	input ObserveCommandInput,
) (CommandReceipt, error) {
	if repository == nil || repository.db == nil || ctx == nil {
		return CommandReceipt{}, ErrCommandReceiptInvalid
	}
	input.Key = normalizeCommandReceiptKey(input.Key)
	if input.CreatedAt.IsZero() {
		input.CreatedAt = repository.now().UTC()
	} else {
		input.CreatedAt = input.CreatedAt.UTC()
	}
	if input.ExpiresAt.IsZero() {
		input.ExpiresAt = input.CreatedAt.Add(repository.ttl)
	} else {
		input.ExpiresAt = input.ExpiresAt.UTC()
	}
	if !validCommandReceiptKey(input.Key) || len(input.RequestHash) != 32 ||
		input.ExpiresAt.Before(input.CreatedAt.Add(MinimumCommandReceiptTTL)) {
		return CommandReceipt{}, ErrCommandReceiptInvalid
	}
	result, err := repository.db.ExecContext(ctx, `
		INSERT INTO agent_access_data_commands(
		 workspace_id,agent_id,client_id,service_principal_id,subject_id,
		 operation,idempotency_key,request_hash,created_at,expires_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT DO NOTHING
	`, input.Key.WorkspaceID, input.Key.AgentID, input.Key.ClientID,
		input.Key.ServicePrincipalID, input.Key.SubjectID, input.Key.Operation,
		input.Key.IdempotencyKey, input.RequestHash, input.CreatedAt, input.ExpiresAt)
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("observe AAP command receipt: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("classify AAP command receipt: %w", err)
	}
	receipt, err := repository.get(ctx, input.Key)
	if err != nil {
		return CommandReceipt{}, err
	}
	if !bytes.Equal(receipt.RequestHash, input.RequestHash) {
		return CommandReceipt{}, ErrCommandIdempotencyConflict
	}
	receipt.Created = affected == 1
	return receipt, nil
}

func (repository *CommandReceiptRepository) Complete(
	ctx context.Context,
	input CompleteCommandInput,
) (CommandReceipt, error) {
	input.Key = normalizeCommandReceiptKey(input.Key)
	input.ResourceType = strings.ToUpper(strings.TrimSpace(input.ResourceType))
	input.ResourceID = strings.ToLower(strings.TrimSpace(input.ResourceID))
	if repository == nil || repository.db == nil || ctx == nil ||
		!validCommandReceiptKey(input.Key) || len(input.RequestHash) != 32 ||
		!validCommandResource(input.ResourceType, input.ResourceID) || input.ResponseVersion < 1 {
		return CommandReceipt{}, ErrCommandReceiptInvalid
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE agent_access_data_commands
		SET resource_type=$9,resource_id=$10,response_version=$11
		WHERE workspace_id=$1 AND agent_id=$2 AND client_id=$3
		  AND service_principal_id=$4 AND subject_id=$5 AND operation=$6
		  AND idempotency_key=$7 AND request_hash=$8
		  AND (resource_id IS NULL OR (resource_type=$9 AND resource_id=$10))
	`, input.Key.WorkspaceID, input.Key.AgentID, input.Key.ClientID,
		input.Key.ServicePrincipalID, input.Key.SubjectID, input.Key.Operation,
		input.Key.IdempotencyKey, input.RequestHash, input.ResourceType,
		input.ResourceID, input.ResponseVersion)
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("complete AAP command receipt: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("classify AAP command completion: %w", err)
	}
	if affected != 1 {
		if existing, getErr := repository.get(ctx, input.Key); getErr == nil &&
			!bytes.Equal(existing.RequestHash, input.RequestHash) {
			return CommandReceipt{}, ErrCommandIdempotencyConflict
		}
		return CommandReceipt{}, ErrCommandReceiptStateConflict
	}
	return repository.get(ctx, input.Key)
}

func (repository *CommandReceiptRepository) get(
	ctx context.Context,
	key CommandReceiptKey,
) (CommandReceipt, error) {
	var value CommandReceipt
	value.Key = key
	var resourceType, resourceID sql.NullString
	var responseVersion sql.NullInt64
	err := repository.db.QueryRowContext(ctx, `
		SELECT request_hash,resource_type,resource_id,response_version,created_at,expires_at
		FROM agent_access_data_commands
		WHERE workspace_id=$1 AND agent_id=$2 AND client_id=$3
		  AND service_principal_id=$4 AND subject_id=$5 AND operation=$6
		  AND idempotency_key=$7
	`, key.WorkspaceID, key.AgentID, key.ClientID, key.ServicePrincipalID,
		key.SubjectID, key.Operation, key.IdempotencyKey).Scan(
		&value.RequestHash, &resourceType, &resourceID, &responseVersion,
		&value.CreatedAt, &value.ExpiresAt,
	)
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("read AAP command receipt: %w", err)
	}
	value.RequestHash = append([]byte(nil), value.RequestHash...)
	value.ResourceType, value.ResourceID = resourceType.String, resourceID.String
	value.ResponseVersion = responseVersion.Int64
	return value, nil
}

func normalizeCommandReceiptKey(value CommandReceiptKey) CommandReceiptKey {
	value.WorkspaceID = strings.ToLower(strings.TrimSpace(value.WorkspaceID))
	value.AgentID = strings.ToLower(strings.TrimSpace(value.AgentID))
	value.ClientID = strings.ToLower(strings.TrimSpace(value.ClientID))
	value.ServicePrincipalID = strings.ToLower(strings.TrimSpace(value.ServicePrincipalID))
	value.SubjectID = strings.ToLower(strings.TrimSpace(value.SubjectID))
	value.Operation = strings.ToLower(strings.TrimSpace(value.Operation))
	value.IdempotencyKey = strings.ToLower(strings.TrimSpace(value.IdempotencyKey))
	return value
}

func validCommandReceiptKey(value CommandReceiptKey) bool {
	if !canonicalUUID(value.WorkspaceID) || !canonicalUUID(value.AgentID) ||
		!canonicalUUID(value.ClientID) || !canonicalUUID(value.ServicePrincipalID) ||
		!canonicalUUID(value.SubjectID) || !canonicalUUID(value.IdempotencyKey) {
		return false
	}
	switch value.Operation {
	case CommandConversationCreate, CommandRunCreate, CommandRunCancel, CommandInteractionDecide,
		CommandFileCreate, CommandFileComplete:
		return true
	default:
		return false
	}
}

func validCommandResource(resourceType, resourceID string) bool {
	if !canonicalUUID(resourceID) {
		return false
	}
	switch resourceType {
	case "CONVERSATION", "RUN", "INTERACTION", "FILE":
		return true
	default:
		return false
	}
}

func commandReceiptKey(
	scope ConversationScope,
	principal agentaccessauth.AAPAccessTokenPrincipal,
	authorization agentaccessauth.AAPAuthorizationDecision,
	operation, idempotencyKey string,
) CommandReceiptKey {
	return CommandReceiptKey{
		WorkspaceID: scope.WorkspaceID, AgentID: scope.AgentID,
		ClientID:           authorization.Snapshot.ClientID,
		ServicePrincipalID: principal.ServicePrincipalID, SubjectID: principal.PrincipalID,
		Operation: operation, IdempotencyKey: idempotencyKey,
	}
}
