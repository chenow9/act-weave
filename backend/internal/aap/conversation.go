package aap

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"unicode/utf8"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"

	"github.com/google/uuid"
)

var (
	ErrConversationInvalid             = errors.New("AAP Conversation input is invalid")
	ErrConversationNotFound            = errors.New("AAP Conversation was not found")
	ErrConversationIdempotencyConflict = errors.New("AAP Conversation idempotency key conflicts with another request")
)

var conversationIDNamespace = uuid.MustParse("0a66be54-943b-4e94-8c1c-8820586dc2a5")

type ConversationStore interface {
	CreateSession(context.Context, chat.CreateSessionInput) (chat.Session, error)
	GetSessionForPrincipal(context.Context, chat.Access, string) (chat.Session, error)
}

type ConversationRunReader interface {
	ListAgentRunsForConversation(context.Context, string, string, string, int) ([]execution.AgentRun, error)
}

type ConversationService struct {
	store    ConversationStore
	runs     ConversationRunReader
	receipts CommandReceiptLedger
}

func (service *ConversationService) ConfigureCommandReceipts(ledger CommandReceiptLedger) error {
	if service == nil || ledger == nil || service.receipts != nil {
		return ErrCommandReceiptInvalid
	}
	service.receipts = ledger
	return nil
}

func NewConversationService(
	store ConversationStore,
	runs ConversationRunReader,
) (*ConversationService, error) {
	if store == nil || runs == nil {
		return nil, errors.New("AAP Conversation store and Run reader are required")
	}
	return &ConversationService{store: store, runs: runs}, nil
}

type ConversationScope struct {
	WorkspaceID string
	AgentID     string
}

type CreateConversationInput struct {
	Scope          ConversationScope
	Principal      agentaccessauth.AAPAccessTokenPrincipal
	Authorization  agentaccessauth.AAPAuthorizationDecision
	Title          string
	IdempotencyKey string
}

type CreateConversationResult struct {
	Conversation chat.Session
	Idempotent   bool
}

type GetConversationInput struct {
	Scope          ConversationScope
	Principal      agentaccessauth.AAPAccessTokenPrincipal
	Authorization  agentaccessauth.AAPAuthorizationDecision
	ConversationID string
	RunLimit       int
}

type ConversationView struct {
	Conversation chat.Session
	Runs         []execution.AgentRun
}

// Create is also the application boundary used by Run creation when no
// Conversation ID is supplied. Its deterministic resource ID makes creation
// durable and idempotent even before M8-T8 adds the shared command receipt
// repository: a retry resolves the same permanent chat_sessions fact.
func (service *ConversationService) Create(
	ctx context.Context,
	input CreateConversationInput,
) (CreateConversationResult, error) {
	input = normalizeCreateConversation(input)
	if service == nil || service.store == nil || service.runs == nil || ctx == nil ||
		!validConversationScope(input.Scope) || !validConversationTitle(input.Title) ||
		!canonicalUUID(input.IdempotencyKey) ||
		!validConversationAuthorization(input.Scope, input.Principal, input.Authorization,
			agentaccessauth.ActionConversationCreate, "") {
		return CreateConversationResult{}, ErrConversationInvalid
	}
	identity, err := invocationIdentity(input.Scope.WorkspaceID, input.Principal)
	if err != nil {
		return CreateConversationResult{}, ErrConversationInvalid
	}
	receiptKey := commandReceiptKey(
		input.Scope, input.Principal, input.Authorization,
		CommandConversationCreate, input.IdempotencyKey,
	)
	requestHash, err := commandRequestHash(struct {
		Title string `json:"title"`
	}{Title: input.Title})
	if err != nil {
		return CreateConversationResult{}, err
	}
	if err := observeCommand(ctx, service.receipts, receiptKey, requestHash); err != nil {
		return CreateConversationResult{}, err
	}
	clientID := input.Authorization.Snapshot.ClientID
	conversationID := deterministicConversationID(
		input.Scope, input.Principal, clientID, input.IdempotencyKey,
	)
	ownership := chat.Ownership{
		Identity: identity, ClientID: clientID, Mode: chat.OwnershipSubjectOwned,
		PolicyVersion: input.Authorization.Snapshot.AgentPolicyVersion,
	}
	created, err := service.store.CreateSession(ctx, chat.CreateSessionInput{
		ID: conversationID, WorkspaceID: input.Scope.WorkspaceID,
		AgentID: input.Scope.AgentID, Title: input.Title, Ownership: &ownership,
	})
	if err == nil {
		if err := completeCommand(ctx, service.receipts, receiptKey, requestHash,
			"CONVERSATION", created.ID, created.LockVersion); err != nil {
			return CreateConversationResult{}, err
		}
		return CreateConversationResult{Conversation: created}, nil
	}
	if !errors.Is(err, chat.ErrConflict) {
		return CreateConversationResult{}, err
	}
	existing, getErr := service.store.GetSessionForPrincipal(ctx, ownership.Access(false), conversationID)
	if getErr != nil || !sameConversationCreation(existing, input.Scope, ownership, input.Title) {
		return CreateConversationResult{}, ErrConversationIdempotencyConflict
	}
	if err := completeCommand(ctx, service.receipts, receiptKey, requestHash,
		"CONVERSATION", existing.ID, existing.LockVersion); err != nil {
		return CreateConversationResult{}, err
	}
	return CreateConversationResult{Conversation: existing, Idempotent: true}, nil
}

func (service *ConversationService) Get(
	ctx context.Context,
	input GetConversationInput,
) (ConversationView, error) {
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	if input.RunLimit <= 0 {
		input.RunLimit = 50
	}
	if service == nil || service.store == nil || service.runs == nil || ctx == nil ||
		!validConversationScope(input.Scope) || !canonicalUUID(input.ConversationID) ||
		input.RunLimit > 100 ||
		!validConversationAuthorization(input.Scope, input.Principal, input.Authorization,
			agentaccessauth.ActionConversationRead, input.ConversationID) {
		return ConversationView{}, ErrConversationInvalid
	}
	identity, err := invocationIdentity(input.Scope.WorkspaceID, input.Principal)
	if err != nil {
		return ConversationView{}, ErrConversationInvalid
	}
	allowShared := input.Authorization.Snapshot.OwnershipMode == "POLICY_SHARED"
	access := chat.Access{
		Identity: identity, ClientID: input.Authorization.Snapshot.ClientID,
		AllowPolicyShared: allowShared,
	}
	conversation, err := service.store.GetSessionForPrincipal(ctx, access, input.ConversationID)
	if err != nil {
		if errors.Is(err, chat.ErrNotFound) {
			return ConversationView{}, ErrConversationNotFound
		}
		return ConversationView{}, err
	}
	if conversation.WorkspaceID != input.Scope.WorkspaceID ||
		conversation.AgentID != input.Scope.AgentID {
		return ConversationView{}, ErrConversationNotFound
	}
	runs, err := service.runs.ListAgentRunsForConversation(
		ctx, input.Scope.WorkspaceID, input.Scope.AgentID, input.ConversationID, input.RunLimit,
	)
	if err != nil {
		if errors.Is(err, execution.ErrRunNotFound) {
			return ConversationView{}, ErrConversationNotFound
		}
		return ConversationView{}, err
	}
	return ConversationView{
		Conversation: conversation, Runs: append([]execution.AgentRun(nil), runs...),
	}, nil
}

func normalizeCreateConversation(input CreateConversationInput) CreateConversationInput {
	input.Scope.WorkspaceID = strings.TrimSpace(input.Scope.WorkspaceID)
	input.Scope.AgentID = strings.TrimSpace(input.Scope.AgentID)
	input.Title = strings.TrimSpace(input.Title)
	input.IdempotencyKey = strings.ToLower(strings.TrimSpace(input.IdempotencyKey))
	return input
}

func validConversationScope(scope ConversationScope) bool {
	return canonicalUUID(scope.WorkspaceID) && canonicalUUID(scope.AgentID)
}

func validConversationTitle(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= 256
}

func validConversationAuthorization(
	scope ConversationScope,
	caller agentaccessauth.AAPAccessTokenPrincipal,
	decision agentaccessauth.AAPAuthorizationDecision,
	action agentaccessauth.AAPAction,
	resourceID string,
) bool {
	snapshot := decision.Snapshot
	wantScope := "conversation:create"
	wantType := agentaccessauth.ResourceNone
	if action == agentaccessauth.ActionConversationRead {
		wantScope, wantType = "conversation:read", agentaccessauth.ResourceConversation
	} else if action == agentaccessauth.ActionRunCreate {
		wantScope, wantType = "run:create", agentaccessauth.ResourceConversation
	}
	if snapshot.SpecVersion != "aap.authorization.v1" || snapshot.Action != action ||
		snapshot.RequiredScope != wantScope || snapshot.WorkspaceID != scope.WorkspaceID ||
		snapshot.AgentID != scope.AgentID || snapshot.ServicePrincipalID != caller.ServicePrincipalID ||
		snapshot.SubjectID != caller.PrincipalID || snapshot.AuthorizedParty != caller.AuthorizedParty ||
		snapshot.TokenID != caller.TokenID || snapshot.ClientID == "" || snapshot.GrantID == "" ||
		snapshot.GrantVersion < 1 || snapshot.AgentPolicyVersion < 1 ||
		snapshot.ResourceType != wantType || snapshot.ResourceID != resourceID ||
		!containsScope(decision.EffectiveScopes, wantScope) {
		return false
	}
	if action == agentaccessauth.ActionConversationRead || action == agentaccessauth.ActionRunCreate {
		return snapshot.OwnershipMode == "SUBJECT_OWNED" || snapshot.OwnershipMode == "POLICY_SHARED"
	}
	return snapshot.OwnershipMode == "" && snapshot.OwnershipPolicyVersion == 0
}

func invocationIdentity(
	workspaceID string,
	caller agentaccessauth.AAPAccessTokenPrincipal,
) (principal.InvocationIdentity, error) {
	actor := principal.Ref{
		WorkspaceID: workspaceID, Type: principal.TypeServicePrincipal,
		ID: caller.ServicePrincipalID,
	}
	if caller.PrincipalID == caller.ServicePrincipalID {
		return principal.NewInvocationIdentity(actor, nil)
	}
	subject := principal.Ref{
		WorkspaceID: workspaceID, Type: principal.TypeExternalSubject, ID: caller.PrincipalID,
	}
	return principal.NewInvocationIdentity(actor, &subject)
}

func deterministicConversationID(
	scope ConversationScope,
	caller agentaccessauth.AAPAccessTokenPrincipal,
	clientID, idempotencyKey string,
) string {
	value := strings.Join([]string{
		"aap.conversation.create.v1", scope.WorkspaceID, scope.AgentID,
		clientID, caller.ServicePrincipalID, caller.PrincipalID, idempotencyKey,
	}, "\x00")
	return uuid.NewHash(sha256.New(), conversationIDNamespace, []byte(value), 8).String()
}

func sameConversationCreation(
	value chat.Session,
	scope ConversationScope,
	ownership chat.Ownership,
	title string,
) bool {
	if value.WorkspaceID != scope.WorkspaceID || value.AgentID != scope.AgentID ||
		value.Title != title || value.Ownership.ClientID != ownership.ClientID ||
		value.Ownership.Mode != ownership.Mode ||
		value.Ownership.Identity.Actor != ownership.Identity.Actor {
		return false
	}
	left, right := value.Ownership.Identity.Subject, ownership.Identity.Subject
	return (left == nil && right == nil) ||
		(left != nil && right != nil && *left == *right)
}

func containsScope(scopes []string, expected string) bool {
	for _, value := range scopes {
		if value == expected {
			return true
		}
	}
	return false
}

func canonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}
