package aap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"unicode/utf8"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/principal"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

var (
	ErrRunInvalid             = errors.New("AAP Run input is invalid")
	ErrRunNotFound            = errors.New("AAP Run was not found")
	ErrRunIdempotencyConflict = errors.New("AAP Run idempotency key conflicts with another request")
)

var runIDNamespace = uuid.MustParse("7be892be-fbe5-4ce6-8873-a879dd34c828")

type RunMessageStarter interface {
	SendMessage(context.Context, chat.SendMessageInput) (chat.SendMessageResult, error)
}

type RunStore interface {
	GetAgentRun(context.Context, string, string) (execution.AgentRun, error)
}

type InitialRunLifecycle interface {
	RecordStartedAgentRun(context.Context, execution.AgentRun) (execution.ProtocolRunLifecycleResult, error)
}

type RunEventReader interface {
	ReadRunAfter(context.Context, protocolevent.RunScope, int64, int) ([]protocolevent.ProtocolEvent, error)
}

type RunDispatcher interface {
	DispatchRun(RunDispatch) error
}

type RunDispatch struct {
	WorkspaceID    string
	ConversationID string
	RunID          string
	MessageID      string
	ActorID        string
}

type RunService struct {
	messages   RunMessageStarter
	runs       RunStore
	lifecycle  InitialRunLifecycle
	events     RunEventReader
	dispatcher RunDispatcher
	receipts   CommandReceiptLedger
	// Optional dual-mode REQUEST_PASSTHROUGH attach (RootScopeAgentRun).
	attacher *outboundidentity.BindingAttacher
	outbound AgentOutboundLoader
	bootID   string
}

func (service *RunService) ConfigureCommandReceipts(ledger CommandReceiptLedger) error {
	if service == nil || ledger == nil || service.receipts != nil {
		return ErrCommandReceiptInvalid
	}
	service.receipts = ledger
	return nil
}

func NewRunService(
	messages RunMessageStarter,
	runs RunStore,
	lifecycle InitialRunLifecycle,
	events RunEventReader,
	dispatcher RunDispatcher,
) (*RunService, error) {
	if messages == nil || runs == nil || lifecycle == nil || events == nil || dispatcher == nil {
		return nil, errors.New("AAP Run service dependencies are required")
	}
	return &RunService{
		messages: messages, runs: runs, lifecycle: lifecycle,
		events: events, dispatcher: dispatcher,
	}, nil
}

type CreateRunInput struct {
	Scope          ConversationScope
	ConversationID string
	Text           string
	Metadata       map[string]string
	IdempotencyKey string
	TraceID        string
	Principal      agentaccessauth.AAPAccessTokenPrincipal
	Authorization  agentaccessauth.AAPAuthorizationDecision
	// OutboundCredentialsRaw is write-only envelope material for BindingAttacher.
	// Never persisted, logged, or included in request hash. Nil when absent.
	OutboundCredentialsRaw json.RawMessage
}

type CreateRunResult struct {
	Run           execution.AgentRun
	AcceptedEvent protocolevent.ProtocolEvent
	Idempotent    bool
	DispatchError error
}

func (service *RunService) Create(
	ctx context.Context,
	input CreateRunInput,
) (CreateRunResult, error) {
	input = normalizeCreateRun(input)
	if service == nil || service.messages == nil || service.runs == nil ||
		service.lifecycle == nil || service.events == nil || service.dispatcher == nil || ctx == nil ||
		!validCreateRunInput(input) {
		_ = outboundidentity.ZeroCredentialsRaw(input.OutboundCredentialsRaw)
		return CreateRunResult{}, ErrRunInvalid
	}
	// Always wipe write-only envelope material before return (success or failure).
	defer func() {
		_ = outboundidentity.ZeroCredentialsRaw(input.OutboundCredentialsRaw)
		input.OutboundCredentialsRaw = nil
	}()
	receiptKey := commandReceiptKey(
		input.Scope, input.Principal, input.Authorization, CommandRunCreate, input.IdempotencyKey,
	)
	// Request hash intentionally excludes Token / expiresAt / locator material.
	requestHash, err := commandRequestHash(struct {
		ConversationID string            `json:"conversationId"`
		Text           string            `json:"text"`
		Metadata       map[string]string `json:"metadata"`
	}{ConversationID: input.ConversationID, Text: input.Text, Metadata: input.Metadata})
	if err != nil {
		return CreateRunResult{}, err
	}
	if err := observeCommand(ctx, service.receipts, receiptKey, requestHash); err != nil {
		return CreateRunResult{}, err
	}
	identity, err := invocationIdentity(input.Scope.WorkspaceID, input.Principal)
	if err != nil {
		return CreateRunResult{}, ErrRunInvalid
	}
	executionPrincipal, err := input.Authorization.Snapshot.ExecutionPrincipalSnapshot(identity)
	if err != nil {
		return CreateRunResult{}, ErrRunInvalid
	}
	authorizationJSON, err := input.Authorization.Snapshot.JSON()
	if err != nil {
		return CreateRunResult{}, ErrRunInvalid
	}
	runID := deterministicRunID(input)
	messageID := deterministicRunMessageID(runID)
	summary, err := canonicalRunInputSummary(input, messageID)
	if err != nil {
		return CreateRunResult{}, ErrRunInvalid
	}
	if existing, loadErr := service.runs.GetAgentRun(ctx, input.Scope.WorkspaceID, runID); loadErr == nil {
		// Replay: attach path discards plaintext when vault alive; never rebinds.
		if _, attachErr := service.attachOutboundForRun(
			ctx, input, runID, executionPrincipal, true,
		); attachErr != nil {
			return CreateRunResult{}, attachErr
		}
		result, replayErr := service.replay(ctx, input, existing, executionPrincipal, summary)
		if replayErr == nil {
			replayErr = completeCommand(ctx, service.receipts, receiptKey, requestHash,
				"RUN", result.Run.ID, result.Run.LockVersion)
		}
		return result, replayErr
	} else if !errors.Is(loadErr, execution.ErrRunNotFound) {
		return CreateRunResult{}, loadErr
	}

	// Attach REQUEST_PASSTHROUGH under RootScopeAgentRun before durable Run create.
	cleanupAttach, attachErr := service.attachOutboundForRun(
		ctx, input, runID, executionPrincipal, false,
	)
	if attachErr != nil {
		return CreateRunResult{}, attachErr
	}
	// Zero plaintext as soon as vault owns copies (or pure-broker path skipped attach).
	_ = outboundidentity.ZeroCredentialsRaw(input.OutboundCredentialsRaw)
	input.OutboundCredentialsRaw = nil

	allowShared := input.Authorization.Snapshot.OwnershipMode == "POLICY_SHARED"
	access := chat.Access{
		Identity: identity, ClientID: input.Authorization.Snapshot.ClientID,
		AllowPolicyShared: allowShared,
	}
	sent, err := service.messages.SendMessage(ctx, chat.SendMessageInput{
		MessageID: messageID, RunID: runID, WorkspaceID: input.Scope.WorkspaceID,
		SessionID: input.ConversationID, Content: input.Text, TraceID: input.TraceID,
		RunInputSummary: summary, AuthorizationSnapshot: authorizationJSON,
		Access: &access, PrincipalSnapshot: &executionPrincipal,
	})
	if err != nil {
		if errors.Is(err, execution.ErrRunConflict) || errors.Is(err, chat.ErrConflict) {
			if existing, loadErr := service.runs.GetAgentRun(ctx, input.Scope.WorkspaceID, runID); loadErr == nil {
				// Concurrent winner owns the Run; discard this request's attach.
				cleanupAttach()
				result, replayErr := service.replay(ctx, input, existing, executionPrincipal, summary)
				if replayErr == nil {
					replayErr = completeCommand(ctx, service.receipts, receiptKey, requestHash,
						"RUN", result.Run.ID, result.Run.LockVersion)
				}
				return result, replayErr
			}
		}
		cleanupAttach()
		return CreateRunResult{}, err
	}
	run, err := service.runs.GetAgentRun(ctx, input.Scope.WorkspaceID, runID)
	if err != nil || sent.Message.RunID != runID || sent.Session.ID != input.ConversationID {
		cleanupAttach()
		if err != nil {
			return CreateRunResult{}, err
		}
		return CreateRunResult{}, ErrRunInvalid
	}
	lifecycle, err := service.lifecycle.RecordStartedAgentRun(ctx, run)
	if err != nil || len(lifecycle.Events) < 2 ||
		lifecycle.Events[0].Type != protocolevent.EventRunAccepted {
		cleanupAttach()
		if err != nil {
			return CreateRunResult{}, err
		}
		return CreateRunResult{}, ErrRunInvalid
	}
	// Successful create: keep vault root for tool invokes (no cleanupAttach).
	dispatchErr := service.dispatcher.DispatchRun(RunDispatch{
		WorkspaceID: input.Scope.WorkspaceID, ConversationID: input.ConversationID,
		RunID: runID, MessageID: messageID, ActorID: input.Principal.ServicePrincipalID,
	})
	result := CreateRunResult{
		Run: run, AcceptedEvent: lifecycle.Events[0], DispatchError: dispatchErr,
	}
	if err := completeCommand(ctx, service.receipts, receiptKey, requestHash,
		"RUN", run.ID, run.LockVersion); err != nil {
		return CreateRunResult{}, err
	}
	return result, nil
}

func (service *RunService) replay(
	ctx context.Context,
	input CreateRunInput,
	run execution.AgentRun,
	principalSnapshot principal.ExecutionSnapshot,
	summary json.RawMessage,
) (CreateRunResult, error) {
	if !sameRunCreation(run, input, principalSnapshot, summary) {
		return CreateRunResult{}, ErrRunIdempotencyConflict
	}
	events, err := service.events.ReadRunAfter(ctx, protocolevent.RunScope{
		WorkspaceID: input.Scope.WorkspaceID, AgentID: input.Scope.AgentID,
		ConversationID: input.ConversationID, RunID: run.ID,
	}, 0, 2)
	if errors.Is(err, protocolevent.ErrRunScopeNotFound) && run.Status == "RUNNING" {
		lifecycle, lifecycleErr := service.lifecycle.RecordStartedAgentRun(ctx, run)
		if lifecycleErr != nil || len(lifecycle.Events) < 2 ||
			lifecycle.Events[0].Type != protocolevent.EventRunAccepted {
			if lifecycleErr != nil {
				return CreateRunResult{}, lifecycleErr
			}
			return CreateRunResult{}, ErrRunInvalid
		}
		dispatchErr := service.dispatcher.DispatchRun(RunDispatch{
			WorkspaceID: input.Scope.WorkspaceID, ConversationID: input.ConversationID,
			RunID: run.ID, MessageID: deterministicRunMessageID(run.ID),
			ActorID: input.Principal.ServicePrincipalID,
		})
		return CreateRunResult{
			Run: run, AcceptedEvent: lifecycle.Events[0], Idempotent: true,
			DispatchError: dispatchErr,
		}, nil
	}
	if err != nil {
		return CreateRunResult{}, err
	}
	if len(events) < 1 || events[0].Type != protocolevent.EventRunAccepted || events[0].Sequence != 1 {
		return CreateRunResult{}, ErrRunInvalid
	}
	return CreateRunResult{Run: run, AcceptedEvent: events[0], Idempotent: true}, nil
}

func normalizeCreateRun(input CreateRunInput) CreateRunInput {
	input.Scope.WorkspaceID = strings.TrimSpace(input.Scope.WorkspaceID)
	input.Scope.AgentID = strings.TrimSpace(input.Scope.AgentID)
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	input.Text = strings.TrimSpace(input.Text)
	input.IdempotencyKey = strings.ToLower(strings.TrimSpace(input.IdempotencyKey))
	input.TraceID = strings.TrimSpace(input.TraceID)
	cloned := make(map[string]string, len(input.Metadata))
	for key, value := range input.Metadata {
		cloned[key] = value
	}
	input.Metadata = cloned
	return input
}

func validCreateRunInput(input CreateRunInput) bool {
	return validConversationScope(input.Scope) && canonicalUUID(input.ConversationID) &&
		canonicalUUID(input.IdempotencyKey) && input.TraceID != "" && input.Text != "" &&
		utf8.ValidString(input.Text) && len([]byte(input.Text)) <= 64<<10 &&
		ValidateRunMetadata(input.Metadata) == nil &&
		validConversationAuthorization(input.Scope, input.Principal, input.Authorization,
			agentaccessauth.ActionRunCreate, input.ConversationID)
}

func deterministicRunID(input CreateRunInput) string {
	value := strings.Join([]string{
		"aap.run.create.v1", input.Scope.WorkspaceID, input.Scope.AgentID,
		input.Authorization.Snapshot.ClientID, input.Principal.ServicePrincipalID,
		input.Principal.PrincipalID, input.IdempotencyKey,
	}, "\x00")
	return uuid.NewHash(sha256.New(), runIDNamespace, []byte(value), 8).String()
}

func deterministicRunMessageID(runID string) string {
	return uuid.NewSHA1(runIDNamespace, []byte("message\x00"+runID)).String()
}

func canonicalRunInputSummary(input CreateRunInput, messageID string) (json.RawMessage, error) {
	request, err := json.Marshal(struct {
		ConversationID string            `json:"conversationId"`
		Text           string            `json:"text"`
		Metadata       map[string]string `json:"metadata"`
	}{input.ConversationID, input.Text, input.Metadata})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(request)
	return json.Marshal(map[string]any{
		"schemaVersion": "aap.run-input.v1", "requestHash": hex.EncodeToString(digest[:]),
		"messageId": messageID, "contentSha256": contentSHA256(input.Text),
		"contentLength": len([]byte(input.Text)),
	})
}

func sameRunCreation(
	run execution.AgentRun,
	input CreateRunInput,
	expected principal.ExecutionSnapshot,
	summary json.RawMessage,
) bool {
	if expected.Validate() != nil || !run.PrincipalSnapshot.SameDecisionPrincipal(expected) ||
		run.WorkspaceID != input.Scope.WorkspaceID || run.AgentID != input.Scope.AgentID ||
		run.SessionID != input.ConversationID || run.TriggerType != "API" ||
		run.TriggeredByType != "SERVICE_PRINCIPAL" ||
		run.TriggeredByID != input.Principal.ServicePrincipalID ||
		!sameJSONObject(run.InputSummary, summary) {
		return false
	}
	return true
}

func sameJSONObject(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil &&
		reflect.DeepEqual(leftValue, rightValue)
}

func contentSHA256(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
