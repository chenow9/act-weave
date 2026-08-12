package httptransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/a2ui"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/transport/sse"

	"github.com/gin-gonic/gin"
)

type ChatStore interface {
	ListSessions(context.Context, string, string, int) ([]chat.Session, error)
	CreateSession(context.Context, chat.CreateSessionInput) (chat.Session, error)
	GetSession(context.Context, string, string) (chat.Session, error)
	ArchiveSession(context.Context, string, string, int64) (chat.Session, error)
	ListMessages(context.Context, string, string) ([]chat.Message, error)
}

type ChatMessenger interface {
	SendMessage(context.Context, chat.SendMessageInput) (chat.SendMessageResult, error)
}

type ChatContentReader interface {
	ReadPermanentChat(context.Context, string, string, string) (string, error)
}

type RunReader interface {
	GetAgentRun(context.Context, string, string) (execution.AgentRun, error)
	ListAgentRunSteps(context.Context, string, string) ([]execution.AgentRunStep, error)
	GetWorkflowExecution(context.Context, string, string) (execution.WorkflowExecution, error)
	ListWorkflowExecutions(context.Context, string, execution.WorkflowExecutionFilter) ([]execution.WorkflowExecution, error)
	ListExecutionSteps(context.Context, string, string) ([]execution.ExecutionStep, error)
}

type ChatConfirmationMutator interface {
	Confirm(context.Context, chat.ConfirmChatConfirmationInput) (chat.ConfirmedChatConfirmation, error)
	Cancel(context.Context, chat.CancelChatConfirmationInput) (chat.CancelledChatConfirmation, error)
}

// ErrAgentRunEventStreamNotReady means the AgentRun exists (and is authorized)
// but protocol_event_streams is not available yet. Console must not surface this
// as the same ambiguous 404 used for missing runs (PR-U2 / design §7.2).
var ErrAgentRunEventStreamNotReady = errors.New("agent run event stream is not ready")

type ChatExecutionRoutes struct {
	authorizer     WorkspaceAuthorizer
	chats          ChatStore
	messages       ChatMessenger
	content        ChatContentReader
	runs           RunReader
	protocolEvents AAPProtocolEventReader
	eventCatchUp   *AAPEventCatchUp
	confirmations  ChatConfirmationMutator
	// debugAttachments is optional: nil disables debug credential attach route.
	debugAttachments *outboundidentity.DebugAttachmentStore
	vault            outboundidentity.CredentialVault
	bootID           string
}

type ChatExecutionDependencies struct {
	Authorizer        WorkspaceAuthorizer
	Chats             ChatStore
	Messages          ChatMessenger
	Content           ChatContentReader
	Runs              RunReader
	ProtocolEvents    AAPProtocolEventReader
	EventFollower     AAPProtocolEventFollower
	StreamPolicy      *sse.BackpressurePolicy
	StreamLimiter     sse.ConnectionLimiter
	StreamRevalidator AAPStreamRevalidator
	Confirmations     ChatConfirmationMutator
	// DebugAttachments / Vault / BootID wire 运行调试台 one-shot attach (#11).
	DebugAttachments *outboundidentity.DebugAttachmentStore
	Vault            outboundidentity.CredentialVault
	BootID           string
}

func NewChatExecutionRoutes(dependencies ChatExecutionDependencies) (*ChatExecutionRoutes, error) {
	if dependencies.Authorizer == nil || dependencies.Chats == nil || dependencies.Messages == nil ||
		dependencies.Content == nil || dependencies.Runs == nil || dependencies.ProtocolEvents == nil ||
		dependencies.Confirmations == nil {
		return nil, errors.New("chat execution route dependencies are required")
	}
	var catchUp *AAPEventCatchUp
	var err error
	if dependencies.EventFollower == nil {
		catchUp, err = NewAAPEventCatchUp(dependencies.ProtocolEvents)
	} else {
		catchUp, err = NewAAPEventCatchUp(dependencies.ProtocolEvents, dependencies.EventFollower)
	}
	if err != nil {
		return nil, err
	}
	if (dependencies.StreamPolicy == nil) != (dependencies.StreamLimiter == nil) {
		return nil, errors.New("chat execution stream policy and limiter must be configured together")
	}
	if dependencies.StreamPolicy != nil {
		if err := catchUp.ConfigureBackpressure(*dependencies.StreamPolicy, dependencies.StreamLimiter); err != nil {
			return nil, err
		}
	}
	if dependencies.StreamRevalidator != nil {
		if err := catchUp.ConfigureRevalidator(dependencies.StreamRevalidator); err != nil {
			return nil, err
		}
	}
	return &ChatExecutionRoutes{
		authorizer: dependencies.Authorizer, chats: dependencies.Chats,
		messages: dependencies.Messages, content: dependencies.Content,
		runs: dependencies.Runs, protocolEvents: dependencies.ProtocolEvents,
		eventCatchUp: catchUp, confirmations: dependencies.Confirmations,
		debugAttachments: dependencies.DebugAttachments,
		vault:            dependencies.Vault,
		bootID:           strings.TrimSpace(dependencies.BootID),
	}, nil
}

func (r *ChatExecutionRoutes) RegisterV1(v1 V1Routes) {
	group := v1.Protected
	group.GET("/workspaces/:wid/chat/sessions", r.listSessions)
	group.POST("/workspaces/:wid/chat/sessions", r.createSession)
	group.GET("/workspaces/:wid/chat/sessions/:sid", r.getSession)
	group.POST("/workspaces/:wid/chat/sessions/:sid/__command/archive", r.archiveSession)
	group.POST("/workspaces/:wid/chat/sessions/:sid/messages", r.sendMessage)
	// 运行调试台 one-shot outbound credential attach (checklist #11).
	group.POST("/workspaces/:wid/chat/sessions/:sid/outbound-credentials", r.attachOutboundCredentials)
	group.GET("/workspaces/:wid/agent-runs/:rid", r.getAgentRun)
	group.GET("/workspaces/:wid/agent-runs/:rid/events", r.getAgentRunEvents)
	group.GET("/workspaces/:wid/executions", r.listExecutions)
	group.GET("/workspaces/:wid/executions/:id", r.getExecution)
	// E1 (D13): production execution events — protocol-shaped SSE projection.
	group.GET("/workspaces/:wid/executions/:id/events", r.getExecutionEvents)
	group.POST("/workspaces/:wid/confirmations/:id/__command/confirm", r.confirm)
	group.POST("/workspaces/:wid/confirmations/:id/__command/cancel", r.cancel)
}

func (r *ChatExecutionRoutes) authorize(c *gin.Context, action authz.Action) bool {
	principal, _ := PrincipalFrom(c.Request.Context())
	if _, err := r.authorizer.AuthorizeWorkspace(
		c.Request.Context(), principal.UserID, c.Param("wid"), action,
	); err != nil {
		RespondError(c, err)
		return false
	}
	return true
}

type chatSessionDTO struct {
	ID                    string    `json:"id"`
	AgentID               string    `json:"agentId"`
	Title                 string    `json:"title"`
	Status                string    `json:"status"`
	LatestRunID           string    `json:"latestRunId,omitempty"`
	PendingConfirmationID string    `json:"pendingConfirmationId,omitempty"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
	LockVersion           int64     `json:"lockVersion"`
}

func chatSessionDTOFor(value chat.Session) chatSessionDTO {
	return chatSessionDTO{value.ID, value.AgentID, value.Title, value.Status,
		value.LatestRunID, value.PendingConfirmationID, value.CreatedAt, value.UpdatedAt,
		value.LockVersion}
}

type chatMessageDTO struct {
	ID            string `json:"id"`
	Role          string `json:"role"`
	Content       string `json:"content"`
	ContentSHA256 string `json:"contentSha256"`
	ContentLength int64  `json:"contentLength"`
	// A2UI carries the renderable surfaces of this message, absent unless the
	// durable body is an aap.message-content.v1 envelope holding a current-version
	// a2ui part. It is a separate channel from Content on purpose: Content stays
	// the text a human wrote or read, and contentSha256/contentLength keep
	// describing exactly that (KD-13).
	A2UI           []json.RawMessage `json:"a2ui,omitempty"`
	Status         string            `json:"status"`
	RunID          string            `json:"runId,omitempty"`
	ConfirmationID string            `json:"confirmationId,omitempty"`
	CreatedAt      time.Time         `json:"createdAt"`
}

func (r *ChatExecutionRoutes) messageDTO(
	ctx context.Context,
	value chat.Message,
	actorID string,
) (chatMessageDTO, error) {
	content := value.Content
	if content == "" && value.ContentObjectID != "" {
		loaded, err := r.content.ReadPermanentChat(ctx, value.WorkspaceID, value.ContentObjectID, actorID)
		if err != nil {
			return chatMessageDTO{}, err
		}
		content = loaded
	}
	// KD-13: Console history is text-first. Never return raw aap.message-content.v1
	// envelope JSON (including a2ui surface) as the markdown body. Display
	// contentSha256/contentLength must describe the projected content string.
	surfaces := chat.A2UISurfacesFromDurable(content, a2ui.EnvelopeVersionV1)
	content = chat.JoinTextPartsFromDurable(content)
	digest := sha256.Sum256([]byte(content))
	return chatMessageDTO{
		ID: value.ID, Role: value.Role, Content: content,
		ContentSHA256: hex.EncodeToString(digest[:]),
		ContentLength: int64(len([]byte(content))),
		A2UI:          surfaces,
		Status:        value.Status, RunID: value.RunID,
		ConfirmationID: value.ConfirmationID, CreatedAt: value.CreatedAt,
	}, nil
}

func (r *ChatExecutionRoutes) listSessions(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	limit, err := optionalPositiveInt(c.Query("limit"), 50, 200)
	if err != nil {
		RespondError(c, chat.ErrInvalid)
		return
	}
	values, err := r.chats.ListSessions(c.Request.Context(), c.Param("wid"), actor(c), limit)
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]chatSessionDTO, len(values))
	for index := range values {
		items[index] = chatSessionDTOFor(values[index])
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type createChatSessionRequest struct {
	AgentID string `json:"agentId"`
	Title   string `json:"title"`
}

func (r *ChatExecutionRoutes) createSession(c *gin.Context) {
	if !r.authorize(c, authz.ActionExecute) {
		return
	}
	var request createChatSessionRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, chat.ErrInvalid)
		return
	}
	id, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	value, err := r.chats.CreateSession(c.Request.Context(), chat.CreateSessionInput{
		ID: id, WorkspaceID: c.Param("wid"), AgentID: request.AgentID,
		Title: request.Title, CreatedBy: actor(c),
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, chatSessionDTOFor(value))
}

func (r *ChatExecutionRoutes) ownSession(c *gin.Context) (chat.Session, bool) {
	value, err := r.chats.GetSession(c.Request.Context(), c.Param("wid"), c.Param("sid"))
	if err != nil {
		RespondError(c, err)
		return chat.Session{}, false
	}
	if value.CreatedBy != actor(c) {
		RespondError(c, authz.ErrNotVisible)
		return chat.Session{}, false
	}
	return value, true
}

func (r *ChatExecutionRoutes) getSession(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	value, ok := r.ownSession(c)
	if !ok {
		return
	}
	messages, err := r.chats.ListMessages(c.Request.Context(), c.Param("wid"), value.ID)
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]chatMessageDTO, len(messages))
	for index := range messages {
		items[index], err = r.messageDTO(c.Request.Context(), messages[index], actor(c))
		if err != nil {
			RespondError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"session": chatSessionDTOFor(value), "messages": items})
}

type archiveChatSessionRequest struct {
	LockVersion int64 `json:"lockVersion"`
}

func (r *ChatExecutionRoutes) archiveSession(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	if _, ok := r.ownSession(c); !ok {
		return
	}
	var request archiveChatSessionRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, chat.ErrInvalid)
		return
	}
	value, err := r.chats.ArchiveSession(c.Request.Context(), c.Param("wid"), c.Param("sid"), request.LockVersion)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, chatSessionDTOFor(value))
}

type sendChatMessageRequest struct {
	Content string `json:"content"`
	// OutboundCredentialAttachmentId is the one-shot locator from
	// POST .../outbound-credentials. Message body never carries Token values.
	OutboundCredentialAttachmentId string `json:"outboundCredentialAttachmentId,omitempty"`
}

func (r *ChatExecutionRoutes) sendMessage(c *gin.Context) {
	if !r.authorize(c, authz.ActionExecute) {
		return
	}
	if _, ok := r.ownSession(c); !ok {
		return
	}
	var request sendChatMessageRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, chat.ErrInvalid)
		return
	}
	messageID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	runID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	// Consume debug locator and move vault root DEBUG_ATTACHMENT → AGENT_RUN.
	if locator := strings.TrimSpace(request.OutboundCredentialAttachmentId); locator != "" {
		if r.debugAttachments == nil || r.vault == nil {
			RespondError(c, outboundidentity.ErrCredentialRequired)
			return
		}
		att, consumeErr := r.debugAttachments.Consume(
			locator, c.Param("wid"), c.Param("sid"), actor(c),
		)
		if consumeErr != nil {
			RespondError(c, mapOutboundEntryError(consumeErr))
			return
		}
		from := outboundidentity.RootScope{
			BootID: att.OwnerBootID, WorkspaceID: att.WorkspaceID,
			SubjectType: outboundidentity.SubjectTypeUser, SubjectID: att.ActorID,
			RootScopeType: outboundidentity.RootScopeDebugAttachment,
			RootScopeID:   att.RootScopeID,
		}
		to := outboundidentity.RootScope{
			BootID: att.OwnerBootID, WorkspaceID: att.WorkspaceID,
			SubjectType: outboundidentity.SubjectTypeUser, SubjectID: att.ActorID,
			RootScopeType: outboundidentity.RootScopeAgentRun, RootScopeID: runID,
		}
		if moveErr := r.vault.MoveRoot(from, to); moveErr != nil {
			// Destroy leftover debug root; do not leave Token stranded.
			r.vault.CleanupRoot(from)
			RespondError(c, mapOutboundEntryError(moveErr))
			return
		}
	}
	requestContext, _ := RequestContextFrom(c.Request.Context())
	value, err := r.messages.SendMessage(c.Request.Context(), chat.SendMessageInput{
		MessageID: messageID, RunID: runID, WorkspaceID: c.Param("wid"),
		SessionID: c.Param("sid"), Content: request.Content, CreatedBy: actor(c),
		TraceID: requestContext.TraceID,
	})
	if err != nil {
		// Message failed after move: clean agent-run vault entries.
		if r.vault != nil && strings.TrimSpace(request.OutboundCredentialAttachmentId) != "" {
			r.vault.CleanupRoot(outboundidentity.RootScope{
				BootID: r.bootID, WorkspaceID: c.Param("wid"),
				SubjectType: outboundidentity.SubjectTypeUser, SubjectID: actor(c),
				RootScopeType: outboundidentity.RootScopeAgentRun, RootScopeID: runID,
			})
		}
		RespondError(c, err)
		return
	}
	message, err := r.messageDTO(c.Request.Context(), value.Message, actor(c))
	if err != nil {
		RespondError(c, err)
		return
	}
	// Response never echoes attachment locator or Token.
	c.JSON(http.StatusAccepted, gin.H{
		"session": chatSessionDTOFor(value.Session), "message": message, "runId": runID,
	})
}

type attachOutboundCredentialsRequest struct {
	// Credentials is write-only outbound-credentials.v1 (values never logged).
	Credentials json.RawMessage `json:"outboundCredentials"`
}

func (r *ChatExecutionRoutes) attachOutboundCredentials(c *gin.Context) {
	if !r.authorize(c, authz.ActionExecute) {
		return
	}
	if r.debugAttachments == nil || r.vault == nil {
		RespondError(c, outboundidentity.ErrCredentialRequired)
		return
	}
	session, ok := r.ownSession(c)
	if !ok {
		return
	}
	if strings.EqualFold(session.Status, "ARCHIVED") {
		RespondError(c, chat.ErrInvalid)
		return
	}
	// USER only — no EXTERNAL_SUBJECT simulation, no SYSTEM.
	split, splitErr := ReadOutboundCredentialsBody(c)
	if splitErr != nil {
		RespondError(c, mapOutboundEntryError(splitErr))
		return
	}
	defer split.Zero()
	if len(split.CredentialsRaw) == 0 {
		RespondError(c, outboundidentity.ErrCredentialRequired)
		return
	}
	// Parse envelope to ensure schema; actual vault attach for debug uses
	// DEBUG_ATTACHMENT root. Full BindingAttacher allowlist is deferred when
	// agent requirements are unknown at attach time — values stay in vault only
	// after strict envelope parse + size limits.
	envelope, parseErr := outboundidentity.ParseCredentialsEnvelope(split.CredentialsRaw)
	if parseErr != nil {
		RespondError(c, mapOutboundEntryError(parseErr))
		return
	}
	rootID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	bootID := r.bootID
	if bootID == "" {
		bootID = r.vault.BootID()
	}
	// Attach each binding under debug root (simplified allowlist: max envelope
	// rules already enforced by ParseCredentialsEnvelope).
	bindings := make([]outboundidentity.AttachBinding, 0, len(envelope.Bindings))
	now := time.Now().UTC()
	for _, b := range envelope.Bindings {
		if b.ExpiresAt.IsZero() || !b.ExpiresAt.After(now) {
			RespondError(c, outboundidentity.ErrCredentialExpired)
			return
		}
		bindings = append(bindings, outboundidentity.AttachBinding{
			Key: outboundidentity.VaultKey{
				BootID: bootID, WorkspaceID: c.Param("wid"),
				SubjectType: outboundidentity.SubjectTypeUser, SubjectID: actor(c),
				RootScopeType: outboundidentity.RootScopeDebugAttachment,
				RootScopeID:   rootID,
				ConnectionID:  b.ConnectionID, ConnectionPolicyVersion: 1,
			},
			CredentialType: b.CredentialType,
			Value:          append([]byte(nil), b.Value...),
			ExpiresAt:      b.ExpiresAt,
		})
		// Zero source value copies in envelope as we go.
		for i := range b.Value {
			b.Value[i] = 0
		}
	}
	if err := r.vault.Attach(bindings); err != nil {
		RespondError(c, mapOutboundEntryError(err))
		return
	}
	locator, expiresAt, err := r.debugAttachments.IssueLocator(
		c.Param("wid"), c.Param("sid"), actor(c), bootID, rootID,
		outboundidentity.MaxDebugAttachmentTTL,
	)
	if err != nil {
		r.vault.CleanupRoot(outboundidentity.RootScope{
			BootID: bootID, WorkspaceID: c.Param("wid"),
			SubjectType: outboundidentity.SubjectTypeUser, SubjectID: actor(c),
			RootScopeType: outboundidentity.RootScopeDebugAttachment, RootScopeID: rootID,
		})
		RespondError(c, mapOutboundEntryError(err))
		return
	}
	// Response: locator only (no Token). Client puts locator on next message.
	c.JSON(http.StatusCreated, gin.H{
		"outboundCredentialAttachmentId": locator,
		"expiresAt":                      expiresAt.UTC().Format(time.RFC3339),
	})
}

type agentRunDTO struct {
	ID                 string          `json:"id"`
	SessionID          string          `json:"sessionId,omitempty"`
	AgentID            string          `json:"agentId"`
	Status             string          `json:"status"`
	TriggerType        string          `json:"triggerType"`
	TriggeredByType    string          `json:"triggeredByType"`
	TriggeredByID      string          `json:"triggeredById"`
	TraceID            string          `json:"traceId"`
	ModelSnapshot      json.RawMessage `json:"modelSnapshot"`
	CapabilitySnapshot json.RawMessage `json:"capabilitySnapshot"`
	InputSummary       json.RawMessage `json:"inputSummary"`
	OutputSummary      json.RawMessage `json:"outputSummary"`
	ErrorCode          string          `json:"errorCode,omitempty"`
	StartedAt          time.Time       `json:"startedAt"`
	FinishedAt         *time.Time      `json:"finishedAt,omitempty"`
	LockVersion        int64           `json:"lockVersion"`
}

func agentRunDTOFor(value execution.AgentRun) agentRunDTO {
	return agentRunDTO{value.ID, value.SessionID, value.AgentID, value.Status,
		value.TriggerType, value.TriggeredByType, value.TriggeredByID, value.TraceID,
		value.ModelSnapshot, value.CapabilitySnapshot, value.InputSummary, value.OutputSummary,
		value.ErrorCode, value.StartedAt, value.FinishedAt, value.LockVersion}
}

type runStepDTO struct {
	ID                  string          `json:"id"`
	SequenceNo          int             `json:"sequenceNo"`
	StepType            string          `json:"stepType"`
	Status              string          `json:"status"`
	CapabilityReleaseID string          `json:"capabilityReleaseId,omitempty"`
	InputSummary        json.RawMessage `json:"inputSummary"`
	OutputSummary       json.RawMessage `json:"outputSummary"`
	ErrorCode           string          `json:"errorCode,omitempty"`
	StartedAt           time.Time       `json:"startedAt"`
	FinishedAt          *time.Time      `json:"finishedAt,omitempty"`
}

func runStepDTOFor(value execution.AgentRunStep) runStepDTO {
	return runStepDTO{value.ID, value.SequenceNo, value.StepType, value.Status,
		value.CapabilityReleaseID, value.InputSummary, value.OutputSummary,
		value.ErrorCode, value.StartedAt, value.FinishedAt}
}

func (r *ChatExecutionRoutes) getAgentRun(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	value, err := r.runs.GetAgentRun(c.Request.Context(), c.Param("wid"), c.Param("rid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	if value.TriggeredByType == "USER" && value.TriggeredByID != actor(c) {
		RespondError(c, authz.ErrNotVisible)
		return
	}
	steps, err := r.runs.ListAgentRunSteps(c.Request.Context(), c.Param("wid"), value.ID)
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]runStepDTO, len(steps))
	for index := range steps {
		items[index] = runStepDTOFor(steps[index])
	}
	c.JSON(http.StatusOK, gin.H{"run": agentRunDTOFor(value), "steps": items})
}

func (r *ChatExecutionRoutes) getAgentRunEvents(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	run, err := r.runs.GetAgentRun(c.Request.Context(), c.Param("wid"), c.Param("rid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	if run.TriggeredByType == "USER" && run.TriggeredByID != actor(c) {
		RespondError(c, authz.ErrNotVisible)
		return
	}
	scope := protocolevent.RunScope{
		WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
		ConversationID: run.SessionID, RunID: run.ID,
	}
	// Run is known; distinguish stream-not-ready from run-not-found (PR-U2 B).
	// Primary fix is Ensure on SendMessage (A); this is an explicit fallback.
	if _, err := r.protocolEvents.HighWatermark(c.Request.Context(), scope); errors.Is(err, protocolevent.ErrRunScopeNotFound) {
		c.Header("Retry-After", "1")
		RespondError(c, ErrAgentRunEventStreamNotReady)
		return
	} else if err != nil {
		RespondError(c, err)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	r.eventCatchUp.Stream(c, scope, AAPStreamSession{
		Connection: sse.ConnectionIdentity{
			ClientID:  "user-api:" + principal.UserID,
			SubjectID: "user:" + principal.UserID, RunID: run.ID,
		},
		Authorization: &agentaccessauth.StreamBinding{
			WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
			ClientID:    "user-api:" + principal.UserID,
			PrincipalID: principal.UserID, SubjectID: "user:" + principal.UserID,
			SecurityVersion: 1, TokenExpiresAt: principal.TokenExpiresAt,
		},
	})
}

type workflowExecutionDTO struct {
	ID              string          `json:"id"`
	WorkflowID      string          `json:"workflowId"`
	RevisionID      string          `json:"revisionId,omitempty"`
	AgentRunID      string          `json:"agentRunId,omitempty"`
	TriggerType     string          `json:"triggerType"`
	TriggeredByType string          `json:"triggeredByType"`
	TriggeredByID   string          `json:"triggeredById"`
	TraceID         string          `json:"traceId"`
	Status          string          `json:"status"`
	InputSummary    json.RawMessage `json:"inputSummary"`
	OutputSummary   json.RawMessage `json:"outputSummary"`
	ErrorCode       string          `json:"errorCode,omitempty"`
	StartedAt       time.Time       `json:"startedAt"`
	FinishedAt      *time.Time      `json:"finishedAt,omitempty"`
	LockVersion     int64           `json:"lockVersion"`
}

func workflowExecutionDTOFor(value execution.WorkflowExecution) workflowExecutionDTO {
	return workflowExecutionDTO{value.ID, value.WorkflowID, value.RevisionID, value.AgentRunID,
		value.TriggerType, value.TriggeredByType, value.TriggeredByID, value.TraceID,
		value.Status, value.InputSummary, value.OutputSummary, value.ErrorCode,
		value.StartedAt, value.FinishedAt, value.LockVersion}
}

type executionStepDTO struct {
	ID            string          `json:"id"`
	NodeID        string          `json:"nodeId"`
	NodeType      string          `json:"nodeType"`
	SequenceNo    int             `json:"sequenceNo"`
	Status        string          `json:"status"`
	InputSummary  json.RawMessage `json:"inputSummary"`
	OutputSummary json.RawMessage `json:"outputSummary"`
	ErrorCode     string          `json:"errorCode,omitempty"`
	StartedAt     time.Time       `json:"startedAt"`
	FinishedAt    *time.Time      `json:"finishedAt,omitempty"`
}

func executionStepDTOFor(value execution.ExecutionStep) executionStepDTO {
	return executionStepDTO{value.ID, value.NodeID, value.NodeType, value.SequenceNo,
		value.Status, value.InputSummary, value.OutputSummary, value.ErrorCode,
		value.StartedAt, value.FinishedAt}
}

func (r *ChatExecutionRoutes) listExecutions(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	filter, err := executionFilter(c)
	if err != nil {
		RespondError(c, err)
		return
	}
	values, err := r.runs.ListWorkflowExecutions(c.Request.Context(), c.Param("wid"), filter)
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]workflowExecutionDTO, len(values))
	for index := range values {
		items[index] = workflowExecutionDTOFor(values[index])
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func executionFilter(c *gin.Context) (execution.WorkflowExecutionFilter, error) {
	limit, err := optionalPositiveInt(c.Query("limit"), 50, 200)
	if err != nil {
		return execution.WorkflowExecutionFilter{}, execution.ErrRunInvalid
	}
	filter := execution.WorkflowExecutionFilter{
		Status: c.Query("status"), TraceID: c.Query("traceId"),
		WorkflowID: c.Query("workflowId"), Limit: limit,
	}
	if value := strings.TrimSpace(c.Query("startedAfter")); value != "" {
		parsed, parseErr := time.Parse(time.RFC3339, value)
		if parseErr != nil {
			return execution.WorkflowExecutionFilter{}, execution.ErrRunInvalid
		}
		filter.StartedAfter = &parsed
	}
	if value := strings.TrimSpace(c.Query("startedBefore")); value != "" {
		parsed, parseErr := time.Parse(time.RFC3339, value)
		if parseErr != nil {
			return execution.WorkflowExecutionFilter{}, execution.ErrRunInvalid
		}
		filter.StartedBefore = &parsed
	}
	return filter, nil
}

func (r *ChatExecutionRoutes) getExecution(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	value, err := r.runs.GetWorkflowExecution(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if err != nil {
		RespondError(c, err)
		return
	}
	if value.TriggeredByType == "USER" && value.TriggeredByID != actor(c) {
		RespondError(c, authz.ErrNotVisible)
		return
	}
	steps, err := r.runs.ListExecutionSteps(c.Request.Context(), c.Param("wid"), value.ID)
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]executionStepDTO, len(steps))
	for index := range steps {
		items[index] = executionStepDTOFor(steps[index])
	}
	c.JSON(http.StatusOK, gin.H{"execution": workflowExecutionDTOFor(value), "steps": items})
}

// getExecutionEvents projects durable WorkflowExecution state into protocol-shaped
// SSE frames (E1 / D13). It does not require agent_runs / protocol_event_streams
// (standalone Workflow Execution path); frames use the same type vocabulary as
// protocolevent (run.accepted / run.started / run.completed / …).
func (r *ChatExecutionRoutes) getExecutionEvents(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	value, err := r.runs.GetWorkflowExecution(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if err != nil {
		RespondError(c, err)
		return
	}
	if value.TriggeredByType == "USER" && value.TriggeredByID != actor(c) {
		RespondError(c, authz.ErrNotVisible)
		return
	}
	steps, err := r.runs.ListExecutionSteps(c.Request.Context(), c.Param("wid"), value.ID)
	if err != nil {
		RespondError(c, err)
		return
	}
	frames := projectExecutionProtocolEvents(value, steps)
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	for _, frame := range frames {
		if _, writeErr := c.Writer.Write([]byte(frame)); writeErr != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func projectExecutionProtocolEvents(
	value execution.WorkflowExecution,
	steps []execution.ExecutionStep,
) []string {
	status := mapExecutionProtocolStatus(value.Status)
	trigger := mapExecutionProtocolTrigger(value.TriggerType)
	startedAt := value.StartedAt.UTC().Format(time.RFC3339Nano)
	runBase := map[string]any{
		"id": value.ID, "status": string(protocolevent.RunStatusAccepted),
		"trigger": trigger, "startedAt": startedAt,
		"workflowId": value.WorkflowID, "revisionId": value.RevisionID,
		"workflowExecutionId": value.ID,
	}
	if value.AgentRunID != "" {
		runBase["agentRunId"] = value.AgentRunID
	}
	frames := make([]string, 0, 4+len(steps)*2)
	seq := int64(1)
	frames = append(frames, encodeExecutionSSEFrame(seq, protocolevent.EventRunAccepted, value.TraceID, map[string]any{
		"run": cloneRunMap(runBase, string(protocolevent.RunStatusAccepted)),
	}))
	seq++
	frames = append(frames, encodeExecutionSSEFrame(seq, protocolevent.EventRunStarted, value.TraceID, map[string]any{
		"run": cloneRunMap(runBase, string(protocolevent.RunStatusRunning)),
	}))
	seq++
	for _, step := range steps {
		itemStatus := mapStepProtocolStatus(step.Status)
		item := map[string]any{
			"id": step.ID, "type": string(protocolevent.ItemTypeWorkflowStep),
			"status": itemStatus, "nodeId": step.NodeID, "nodeType": step.NodeType,
			"workflowExecutionId": value.ID, "stepSequence": step.SequenceNo,
		}
		frames = append(frames, encodeExecutionSSEFrame(seq, protocolevent.EventItemStarted, value.TraceID, map[string]any{
			"item": item,
		}))
		seq++
		if isTerminalStepStatus(step.Status) {
			frames = append(frames, encodeExecutionSSEFrame(seq, protocolevent.EventItemCompleted, value.TraceID, map[string]any{
				"item": item,
			}))
			seq++
		}
	}
	terminalType, terminalStatus, ok := mapExecutionTerminalEvent(value.Status)
	if !ok {
		// Non-terminal: surface current status as run.started snapshot only.
		_ = status
		return frames
	}
	terminalRun := cloneRunMap(runBase, terminalStatus)
	if value.FinishedAt != nil {
		terminalRun["completedAt"] = value.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	if terminalType == protocolevent.EventRunFailed && value.ErrorCode != "" {
		terminalRun["error"] = map[string]any{
			"code": value.ErrorCode, "message": "Workflow execution failed", "retryable": false,
		}
	}
	frames = append(frames, encodeExecutionSSEFrame(seq, terminalType, value.TraceID, map[string]any{
		"run": terminalRun,
	}))
	return frames
}

func encodeExecutionSSEFrame(sequence int64, eventType, traceID string, data map[string]any) string {
	payload := map[string]any{
		"type": eventType, "specVersion": "1.0", "sequence": sequence,
		"traceId": traceID, "data": data,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		body = []byte(`{"type":"run.failed","specVersion":"1.0","data":{"error":{"code":"EVENT_ENCODE_FAILED"}}}`)
		eventType = protocolevent.EventRunFailed
	}
	return "id: " + strconv.FormatInt(sequence, 10) + "\nevent: " + eventType + "\ndata: " + string(body) + "\n\n"
}

func cloneRunMap(base map[string]any, status string) map[string]any {
	out := make(map[string]any, len(base)+1)
	for key, value := range base {
		out[key] = value
	}
	out["status"] = status
	return out
}

func mapExecutionProtocolStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PENDING":
		return string(protocolevent.RunStatusAccepted)
	case "RUNNING":
		return string(protocolevent.RunStatusRunning)
	case "WAITING_CONFIRMATION":
		return string(protocolevent.RunStatusWaitingInteraction)
	case "SUCCEEDED":
		return string(protocolevent.RunStatusCompleted)
	case "FAILED":
		return string(protocolevent.RunStatusFailed)
	case "CANCELLED":
		return string(protocolevent.RunStatusCancelled)
	default:
		return string(protocolevent.RunStatusUnknown)
	}
}

func mapExecutionProtocolTrigger(triggerType string) string {
	switch strings.ToUpper(strings.TrimSpace(triggerType)) {
	case "API":
		return string(protocolevent.RunTriggerAPI)
	case "CONSOLE", "TRIAL", "CHAT", "MESSAGE":
		return string(protocolevent.RunTriggerSystem)
	case "WORKFLOW", "AGENT":
		return string(protocolevent.RunTriggerWorkflow)
	default:
		return string(protocolevent.RunTriggerSystem)
	}
}

func mapExecutionTerminalEvent(status string) (eventType, protocolStatus string, ok bool) {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCEEDED":
		return protocolevent.EventRunCompleted, string(protocolevent.RunStatusCompleted), true
	case "FAILED":
		return protocolevent.EventRunFailed, string(protocolevent.RunStatusFailed), true
	case "CANCELLED":
		return protocolevent.EventRunCancelled, string(protocolevent.RunStatusCancelled), true
	case "WAITING_CONFIRMATION":
		return protocolevent.EventRunWaiting, string(protocolevent.RunStatusWaitingInteraction), true
	default:
		return "", "", false
	}
}

func mapStepProtocolStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCEEDED":
		return string(protocolevent.ItemStatusCompleted)
	case "FAILED":
		return string(protocolevent.ItemStatusFailed)
	case "CANCELLED":
		return string(protocolevent.ItemStatusCancelled)
	case "WAITING_CONFIRMATION":
		return string(protocolevent.ItemStatusWaiting)
	default:
		return string(protocolevent.ItemStatusInProgress)
	}
}

func isTerminalStepStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCEEDED", "FAILED", "SKIPPED", "CANCELLED":
		return true
	default:
		return false
	}
}

type confirmRequest struct {
	ResumeToken string `json:"resumeToken"`
	LockVersion int64  `json:"lockVersion"`
}

func (r *ChatExecutionRoutes) confirm(c *gin.Context) {
	if !r.authorize(c, authz.ActionExecute) {
		return
	}
	var request confirmRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, execution.ErrConfirmationInvalid)
		return
	}
	value, err := r.confirmations.Confirm(c.Request.Context(), chat.ConfirmChatConfirmationInput{
		WorkspaceID: c.Param("wid"), ConfirmationID: c.Param("id"), ActorID: actor(c),
		ResumeToken: request.ResumeToken, IdempotencyKey: c.GetHeader("Idempotency-Key"),
		ExpectedExecutionLockVersion: request.LockVersion,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, confirmationDTOFor(
		value.Confirmation, value.Cached, value.Resume.Checkpoint.Status,
	))
}

type cancelRequest struct {
	LockVersion int64 `json:"lockVersion"`
}

func (r *ChatExecutionRoutes) cancel(c *gin.Context) {
	if !r.authorize(c, authz.ActionExecute) {
		return
	}
	var request cancelRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, execution.ErrConfirmationInvalid)
		return
	}
	value, err := r.confirmations.Cancel(c.Request.Context(), chat.CancelChatConfirmationInput{
		WorkspaceID: c.Param("wid"), ConfirmationID: c.Param("id"), ActorID: actor(c),
		IdempotencyKey:               c.GetHeader("Idempotency-Key"),
		ExpectedExecutionLockVersion: request.LockVersion,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, confirmationDTOFor(value.Confirmation, value.Cached, value.Checkpoint.Status))
}

type confirmationDTO struct {
	ID              string          `json:"id"`
	SessionID       string          `json:"sessionId"`
	RunID           string          `json:"runId"`
	TargetType      string          `json:"targetType"`
	TargetReleaseID string          `json:"targetReleaseId"`
	RiskLevel       string          `json:"riskLevel"`
	RiskReasons     []string        `json:"riskReasons"`
	InputSummary    json.RawMessage `json:"inputSummary"`
	Status          string          `json:"status"`
	RequestedBy     string          `json:"requestedBy"`
	ConfirmedBy     string          `json:"confirmedBy,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
	ExpiresAt       time.Time       `json:"expiresAt"`
	ConfirmedAt     *time.Time      `json:"confirmedAt,omitempty"`
	LockVersion     int64           `json:"lockVersion"`
	Cached          bool            `json:"cached"`
	ResumeStatus    string          `json:"resumeStatus,omitempty"`
}

func confirmationDTOFor(value chat.ChatConfirmation, cached bool, resumeStatus string) confirmationDTO {
	return confirmationDTO{value.ID, value.SessionID, value.RunID, value.TargetType,
		value.TargetReleaseID, value.RiskLevel, value.RiskReasons, value.InputSummary,
		value.Status, value.RequestedBy, value.ConfirmedBy, value.CreatedAt, value.ExpiresAt,
		value.ConfirmedAt, value.ExecutionLockVersion, cached, resumeStatus}
}

func optionalPositiveInt(value string, fallback, maximum int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > maximum {
		return 0, errors.New("invalid positive integer")
	}
	return parsed, nil
}
