package chat

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

type Service struct {
	repository *Repository
	runs       *execution.RunRepository
	runService *execution.RunService
	content    PermanentContentStore
	inlineMax  int
	audit      AuditSink
}

const DefaultInlineContentBytes = 16 << 10

type PermanentContentInput struct {
	ObjectID      string
	WorkspaceID   string
	Content       []byte
	CreatedByType string
	CreatedByID   string
}

type PermanentContentStore interface {
	PutPermanentChat(context.Context, PermanentContentInput) (string, error)
}

type AuditEvent struct {
	EventID      string
	WorkspaceID  string
	ActorType    string
	ActorID      string
	Action       string
	ResourceType string
	ResourceID   string
	Result       string
	TraceID      string
	Metadata     map[string]any
}

type AuditSink interface {
	AppendChatAuditEvent(context.Context, *sql.Tx, AuditEvent) error
}

type ServiceOption func(*Service) error

func WithPermanentContent(store PermanentContentStore, inlineMaxBytes int) ServiceOption {
	return func(service *Service) error {
		if store == nil || inlineMaxBytes < 1 {
			return ErrInvalid
		}
		service.content = store
		service.inlineMax = inlineMaxBytes
		return nil
	}
}

func WithAuditSink(sink AuditSink) ServiceOption {
	return func(service *Service) error {
		if sink == nil {
			return ErrInvalid
		}
		service.audit = sink
		return nil
	}
}

func NewService(
	repository *Repository,
	runs *execution.RunRepository,
	runService *execution.RunService,
	options ...ServiceOption,
) (*Service, error) {
	if repository == nil || runs == nil || runService == nil {
		return nil, errors.New("chat service repository and run services are required")
	}
	service := &Service{
		repository: repository, runs: runs, runService: runService,
		inlineMax: DefaultInlineContentBytes,
	}
	for _, option := range options {
		if option == nil {
			return nil, ErrInvalid
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (s *Service) SendMessage(
	ctx context.Context,
	input SendMessageInput,
) (SendMessageResult, error) {
	input.MessageID, input.RunID = strings.TrimSpace(input.MessageID), strings.TrimSpace(input.RunID)
	input.WorkspaceID, input.SessionID = strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.SessionID)
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	input.TraceID = strings.TrimSpace(input.TraceID)
	if !validUUID(input.MessageID) || !validUUID(input.RunID) ||
		!validUUID(input.WorkspaceID) || !validUUID(input.SessionID) ||
		strings.TrimSpace(input.Content) == "" || input.TraceID == "" {
		return SendMessageResult{}, ErrInvalid
	}
	access, err := sendMessageAccess(input)
	if err != nil {
		return SendMessageResult{}, err
	}
	session, err := s.repository.GetSessionForPrincipal(ctx, access, input.SessionID)
	if err != nil {
		return SendMessageResult{}, err
	}
	if session.Status != "ACTIVE" {
		return SendMessageResult{}, ErrConflict
	}
	// AC-18: reject concurrent SendMessage while a confirmation is pending or a run is active.
	if session.PendingConfirmationID != "" {
		return SendMessageResult{}, ErrConflict
	}
	if session.LatestRunID != "" {
		latestRun, runErr := s.runs.GetAgentRun(ctx, input.WorkspaceID, session.LatestRunID)
		if runErr == nil && (latestRun.Status == "RUNNING" || latestRun.Status == "WAITING_CONFIRMATION") {
			return SendMessageResult{}, ErrConflict
		}
	}
	hash := contentHash(input.Content)
	content, contentObjectID, contentLength, err := s.prepareMessageContent(ctx,
		input.MessageID, input.WorkspaceID, input.Content,
		string(access.Identity.Actor.Type), access.Identity.Actor.ID)
	if err != nil {
		return SendMessageResult{}, err
	}
	summary, err := json.Marshal(map[string]any{
		"messageId": input.MessageID, "contentSha256": hash, "contentLength": contentLength,
	})
	if err != nil {
		return SendMessageResult{}, err
	}
	if len(input.RunInputSummary) > 0 {
		summary = append(json.RawMessage(nil), input.RunInputSummary...)
	}
	prepared, err := s.runService.PrepareAgentRun(ctx, execution.StartAgentRunRequest{
		ID: input.RunID, WorkspaceID: input.WorkspaceID, SessionID: input.SessionID,
		AgentID: session.AgentID, TriggerType: "CHAT",
		TriggeredByType: string(access.Identity.Actor.Type),
		TriggeredByID:   access.Identity.Actor.ID, TraceID: input.TraceID, InputSummary: summary,
		AuthorizationSnapshot: input.AuthorizationSnapshot,
		PrincipalSnapshot:     input.PrincipalSnapshot,
	})
	if err != nil {
		return SendMessageResult{}, err
	}
	tx, err := s.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return SendMessageResult{}, err
	}
	defer tx.Rollback()
	if _, err := s.runs.StartAgentRunInTransaction(ctx, tx, prepared); err != nil {
		return SendMessageResult{}, err
	}
	// PR-U2: ensure protocol stream in the same request/tx as run create so
	// Console GET agent-runs/:id/events can HighWatermark before async Execute
	// calls RecordStarted. Idempotent with later lifecycle Ensure (streamID=runID).
	if _, err := protocolevent.EnsureRunEventStreamInTx(ctx, tx, input.RunID, protocolevent.RunScope{
		WorkspaceID: input.WorkspaceID, AgentID: session.AgentID,
		ConversationID: input.SessionID, RunID: input.RunID,
	}); err != nil {
		if errors.Is(err, protocolevent.ErrEventConflict) {
			return SendMessageResult{}, ErrConflict
		}
		if errors.Is(err, protocolevent.ErrProtocolUnitOfWorkInvalid) ||
			errors.Is(err, protocolevent.ErrReadInvalid) {
			return SendMessageResult{}, ErrInvalid
		}
		return SendMessageResult{}, err
	}
	createdBy := ""
	if access.Identity.Actor.Type == principal.TypeUser {
		createdBy = access.Identity.Actor.ID
	}
	message, err := s.repository.insertMessageInTransaction(ctx, tx, Message{
		ID: input.MessageID, WorkspaceID: input.WorkspaceID, SessionID: input.SessionID,
		Role: "USER", Content: content, ContentObjectID: contentObjectID,
		ContentSHA256: hash, ContentLength: contentLength,
		Status: "PROCESSING", RunID: input.RunID, CreatedBy: createdBy,
		Identity: access.Identity, ClientID: access.ClientID,
		OwnershipMode: session.Ownership.Mode,
		PolicyVersion: session.Ownership.PolicyVersion,
	})
	if err != nil {
		return SendMessageResult{}, err
	}
	updatedSession, err := s.repository.setLatestRunInTransaction(
		ctx, tx, input.WorkspaceID, input.SessionID, input.RunID, session.LockVersion,
	)
	if err != nil {
		return SendMessageResult{}, err
	}
	if s.audit != nil {
		eventID, err := newChatAuditEventID()
		if err != nil {
			return SendMessageResult{}, err
		}
		carrier := "INLINE"
		if contentObjectID != "" {
			carrier = "STORED_OBJECT"
		}
		if err := s.audit.AppendChatAuditEvent(ctx, tx, AuditEvent{
			EventID: eventID, WorkspaceID: input.WorkspaceID,
			ActorType: string(access.Identity.Actor.Type), ActorID: access.Identity.Actor.ID,
			Action:       "chat.message.sent",
			ResourceType: "CHAT_MESSAGE", ResourceID: input.MessageID, Result: "SUCCESS",
			TraceID: input.TraceID, Metadata: map[string]any{
				"sessionId": input.SessionID, "runId": input.RunID,
				"contentSha256": hash, "contentLength": contentLength, "carrier": carrier,
			},
		}); err != nil {
			return SendMessageResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return SendMessageResult{}, err
	}
	return SendMessageResult{Session: updatedSession, Message: message}, nil
}

func (s *Service) RecordAssistantResult(
	ctx context.Context,
	input RecordAssistantResultInput,
) (RecordAssistantResult, error) {
	input.AssistantMessageID = strings.TrimSpace(input.AssistantMessageID)
	input.WorkspaceID, input.SessionID = strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.SessionID)
	input.UserMessageID, input.RunID = strings.TrimSpace(input.UserMessageID), strings.TrimSpace(input.RunID)
	input.ExpectedRunStatus, input.RunStatus = strings.TrimSpace(input.ExpectedRunStatus), strings.TrimSpace(input.RunStatus)
	input.RunErrorCode = strings.TrimSpace(input.RunErrorCode)
	if !validUUID(input.AssistantMessageID) || !validUUID(input.WorkspaceID) ||
		!validUUID(input.SessionID) || !validUUID(input.UserMessageID) || !validUUID(input.RunID) ||
		strings.TrimSpace(input.Content) == "" || input.ExpectedRunLock <= 0 ||
		(input.RunStatus != "SUCCEEDED" && input.RunStatus != "FAILED") ||
		(input.RunStatus == "FAILED" && input.RunErrorCode == "") ||
		(input.RunStatus != "FAILED" && input.RunErrorCode != "") {
		return RecordAssistantResult{}, ErrInvalid
	}
	output := json.RawMessage(input.RunOutputSummary)
	if len(output) == 0 {
		output = json.RawMessage(`{}`)
	}
	session, err := s.repository.GetSession(ctx, input.WorkspaceID, input.SessionID)
	if err != nil {
		return RecordAssistantResult{}, err
	}
	systemActor := principal.Ref{
		WorkspaceID: input.WorkspaceID, Type: principal.TypeSystem, ID: RuntimeSystemPrincipalID,
	}
	messageIdentity, err := principal.NewInvocationIdentity(systemActor, session.Ownership.Identity.Subject)
	if err != nil {
		return RecordAssistantResult{}, ErrInvalid
	}
	content, contentObjectID, contentLength, err := s.prepareMessageContent(ctx,
		input.AssistantMessageID, input.WorkspaceID, input.Content,
		string(principal.TypeSystem), RuntimeSystemPrincipalID)
	if err != nil {
		return RecordAssistantResult{}, err
	}
	tx, err := s.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return RecordAssistantResult{}, err
	}
	defer tx.Rollback()
	if _, err := s.runs.TransitionAgentRunInTransaction(ctx, tx,
		input.WorkspaceID, input.RunID, execution.RunTransition{
			ExpectedStatus: input.ExpectedRunStatus, ExpectedLockVersion: input.ExpectedRunLock,
			NewStatus: input.RunStatus, OutputSummary: output, ErrorCode: input.RunErrorCode,
		}); err != nil {
		return RecordAssistantResult{}, err
	}
	messageStatus := "EXECUTED"
	if input.RunStatus == "FAILED" {
		messageStatus = "FAILED"
	}
	message, err := s.repository.insertMessageInTransaction(ctx, tx, Message{
		ID: input.AssistantMessageID, WorkspaceID: input.WorkspaceID,
		SessionID: input.SessionID, Role: "ASSISTANT", Content: content,
		ContentObjectID: contentObjectID, ContentSHA256: contentHash(input.Content),
		ContentLength: contentLength, Status: messageStatus, RunID: input.RunID,
		Identity: messageIdentity, ClientID: session.Ownership.ClientID,
		OwnershipMode: session.Ownership.Mode,
		PolicyVersion: session.Ownership.PolicyVersion,
	})
	if err != nil {
		return RecordAssistantResult{}, err
	}
	if err := s.repository.updateUserMessageInTransaction(ctx, tx, input.WorkspaceID,
		input.SessionID, input.UserMessageID, input.RunID, messageStatus); err != nil {
		return RecordAssistantResult{}, err
	}
	if s.audit != nil {
		eventID, err := newChatAuditEventID()
		if err != nil {
			return RecordAssistantResult{}, err
		}
		action, result := "execution.run.completed", "SUCCESS"
		if input.RunStatus == "FAILED" {
			action, result = "execution.run.failed", "FAILURE"
		}
		if err := s.audit.AppendChatAuditEvent(ctx, tx, AuditEvent{
			EventID: eventID, WorkspaceID: input.WorkspaceID,
			ActorType: "SYSTEM", ActorID: input.RunID, Action: action,
			ResourceType: "AGENT_RUN", ResourceID: input.RunID, Result: result,
			Metadata: map[string]any{
				"sessionId": input.SessionID, "assistantMessageId": input.AssistantMessageID,
				"status": input.RunStatus, "errorCode": input.RunErrorCode,
			},
		}); err != nil {
			return RecordAssistantResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return RecordAssistantResult{}, err
	}
	return RecordAssistantResult{Message: message}, nil
}

func sendMessageAccess(input SendMessageInput) (Access, error) {
	if input.Access == nil {
		return NewUserAccess(input.WorkspaceID, input.CreatedBy)
	}
	value := *input.Access
	if value.Validate(input.WorkspaceID) != nil ||
		(input.CreatedBy != "" &&
			(value.Identity.Actor.Type != principal.TypeUser || input.CreatedBy != value.Identity.Actor.ID)) {
		return Access{}, ErrInvalid
	}
	return value, nil
}

func newChatAuditEventID() (string, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

func contentHash(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func (s *Service) prepareMessageContent(
	ctx context.Context,
	messageID, workspaceID, content, createdByType, createdByID string,
) (string, string, int64, error) {
	contentLength := int64(len([]byte(content)))
	if contentLength <= int64(s.inlineMax) {
		return content, "", contentLength, nil
	}
	if s.content == nil {
		return "", "", 0, errors.New("chat permanent content store is required for oversized message")
	}
	objectID, err := s.content.PutPermanentChat(ctx, PermanentContentInput{
		ObjectID: messageID, WorkspaceID: workspaceID, Content: []byte(content),
		CreatedByType: createdByType, CreatedByID: createdByID,
	})
	if err != nil {
		return "", "", 0, err
	}
	if objectID != messageID {
		return "", "", 0, ErrConflict
	}
	return "", objectID, contentLength, nil
}
