package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"time"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/metrics"
	"actweave/backend/internal/smartdag"

	"github.com/gin-gonic/gin"
)

// GenerateSessionService is the domain surface for multi-turn generate sessions (D15).
type GenerateSessionService interface {
	CreateSession(context.Context, smartdag.CreateSessionRequest) (smartdag.GenerateSession, error)
	ApplySessionTurn(context.Context, smartdag.ApplySessionTurnRequest) (smartdag.ApplySessionTurnResult, error)
	GetSession(context.Context, string, string) (smartdag.SessionDetail, error)
	CloseSession(context.Context, string, string) (smartdag.GenerateSession, error)
	CloseSessionWith(context.Context, smartdag.CloseSessionRequest) (smartdag.GenerateSession, error)
}

// GenerateSessionRoutes registers Console additive workflow-generate-sessions APIs.
type GenerateSessionRoutes struct {
	authorizer WorkspaceAuthorizer
	sessions   GenerateSessionService
}

// GenerateSessionDependencies wires generate session HTTP handlers.
type GenerateSessionDependencies struct {
	Authorizer WorkspaceAuthorizer
	Sessions   GenerateSessionService
}

// NewGenerateSessionRoutes constructs route handlers.
func NewGenerateSessionRoutes(deps GenerateSessionDependencies) (*GenerateSessionRoutes, error) {
	if deps.Authorizer == nil || deps.Sessions == nil {
		return nil, errors.New("generate session route dependencies are required")
	}
	return &GenerateSessionRoutes{authorizer: deps.Authorizer, sessions: deps.Sessions}, nil
}

// RegisterV1 mounts generate-session routes on Console v1 protected group.
func (r *GenerateSessionRoutes) RegisterV1(v1 V1Routes) {
	group := v1.Protected
	group.POST("/workspaces/:wid/workflow-generate-sessions", r.createSession)
	group.GET("/workspaces/:wid/workflow-generate-sessions/:sid", r.getSession)
	group.POST("/workspaces/:wid/workflow-generate-sessions/:sid/turns", r.applyTurn)
	// close via colonCommandAdapter → .../__command/close
	group.POST("/workspaces/:wid/workflow-generate-sessions/:sid/__command/close", r.closeSession)
}

func (r *GenerateSessionRoutes) authorize(c *gin.Context, action authz.Action) bool {
	principal, _ := PrincipalFrom(c.Request.Context())
	if _, err := r.authorizer.AuthorizeWorkspace(
		c.Request.Context(), principal.UserID, c.Param("wid"), action,
	); err != nil {
		RespondError(c, err)
		return false
	}
	return true
}

type createGenerateSessionRequest struct {
	AgentID     string          `json:"agentId"`
	WorkflowID  string          `json:"workflowId"`
	Constraints json.RawMessage `json:"constraints"`
	// ModelConfigID is rejected (D2 no body bypass).
	ModelConfigID string `json:"modelConfigId"`
}

func (r *GenerateSessionRoutes) createSession(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var request createGenerateSessionRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, smartdag.ErrInvalid)
		return
	}
	session, err := r.sessions.CreateSession(c.Request.Context(), smartdag.CreateSessionRequest{
		WorkspaceID:          c.Param("wid"),
		AgentID:              request.AgentID,
		RequestModelConfigID: request.ModelConfigID,
		WorkflowID:           request.WorkflowID,
		Constraints:          request.Constraints,
		CreatedBy:            actor(c),
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, generateSessionDTOFor(session))
}

type applyGenerateTurnRequest struct {
	Message  string          `json:"message"`
	Feedback json.RawMessage `json:"feedback"`
	// ModelConfigID is rejected (D2 no body bypass).
	ModelConfigID string `json:"modelConfigId"`
	// ExpectedSessionLockVersion is optional for old clients (ZKL-56 T4 / checklist #3–4).
	// New frontend always sends it; omission is accepted for publish compatibility.
	ExpectedSessionLockVersion *int64 `json:"expectedSessionLockVersion"`
}

func (r *GenerateSessionRoutes) applyTurn(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var request applyGenerateTurnRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, smartdag.ErrInvalid)
		return
	}
	startedAt := time.Now()
	requestContext, _ := RequestContextFrom(c.Request.Context())
	traceID := requestContext.TraceID
	result, err := r.sessions.ApplySessionTurn(c.Request.Context(), smartdag.ApplySessionTurnRequest{
		WorkspaceID:                c.Param("wid"),
		SessionID:                  c.Param("sid"),
		Message:                    request.Message,
		RequestModelConfigID:       request.ModelConfigID,
		CreatedBy:                  actor(c),
		TraceID:                    traceID,
		Feedback:                   request.Feedback,
		ExpectedSessionLockVersion: request.ExpectedSessionLockVersion,
	})
	latency := time.Since(startedAt)
	if err != nil {
		// Attach guardReport when available for GUARD_REJECTED responses.
		// Legacy top-level guard fields are retained; standard details also include stage.
		var guardErr *smartdag.GuardError
		if errors.As(err, &guardErr) {
			metrics.SmartDag().ObserveGenerate("guard_rejected", latency)
			slog.Info("smart-dag generate guard rejected",
				"event", "smartdag.generate.guard_rejected",
				"workspace_id", c.Param("wid"),
				"session_id", result.SessionID,
				"agent_id", result.AgentID,
				"generation_id", result.GenerationID,
				"prompt_hash", result.Audit.PromptHash,
				"trace_id", traceID,
				"duration_ms", latency.Milliseconds(),
			)
			RespondSmartDagTurnError(c, err, smartDagTurnErrorContext{
				SessionID:    result.SessionID,
				TurnID:       result.TurnID,
				GenerationID: result.GenerationID,
				AgentID:      result.AgentID,
				PromptHash:   result.Audit.PromptHash,
				TraceID:      traceID,
				GuardReport:  &guardErr.Report,
			})
			return
		}
		resultCode := "failed"
		if tf, ok := smartdag.AsTurnFailure(err); ok && tf != nil {
			resultCode = strings.ToLower(tf.Code)
		} else if errors.Is(err, smartdag.ErrAgentModelRequired) {
			resultCode = "agent_model_required"
		}
		metrics.SmartDag().ObserveGenerate(resultCode, latency)
		slog.Warn("smart-dag generate turn failed",
			"event", "smartdag.generate.failed",
			"workspace_id", c.Param("wid"),
			"session_id", c.Param("sid"),
			"result", resultCode,
			"trace_id", traceID,
			"duration_ms", latency.Milliseconds(),
		)
		RespondSmartDagTurnError(c, err, smartDagTurnErrorContext{
			SessionID:    firstNonEmpty(result.SessionID, c.Param("sid")),
			TurnID:       result.TurnID,
			GenerationID: result.GenerationID,
			AgentID:      result.AgentID,
			TraceID:      traceID,
		})
		return
	}

	metrics.SmartDag().ObserveGenerate("succeeded", latency)
	workflowID := result.Workflow.CapabilityID
	slog.Info("smart-dag generate turn succeeded",
		"event", "smartdag.generate.succeeded",
		"workspace_id", c.Param("wid"),
		"session_id", result.SessionID,
		"turn_id", result.TurnID,
		"generation_id", result.GenerationID,
		"workflow_id", workflowID,
		"agent_id", result.AgentID,
		"model_config_id", result.ModelConfigID,
		"prompt_id", result.Audit.PromptID,
		"prompt_hash", result.Audit.PromptHash,
		"draft_version", result.DraftVersion,
		"trace_id", traceID,
		"duration_ms", latency.Milliseconds(),
	)

	payload := gin.H{
		"sessionId":           result.SessionID,
		"turnId":              result.TurnID,
		"generationId":        result.GenerationID,
		"workflow":            workflowDTOFor(result.Workflow),
		"draft":               draftDTOFor(result.Draft),
		"assistantMessage":    result.AssistantMessage,
		"reasoningSteps":      result.ReasoningSteps,
		"missingCapabilities": result.MissingCapabilities,
		"nodeExplanations":    result.NodeExplanations,
		"availableToolIds":    result.AvailableToolIDs,
		"selectedToolIds":     result.SelectedToolIDs,
		"confidence":          result.Confidence,
		"guardReport":         result.GuardReport,
		"draftVersion":        result.DraftVersion,
		"promptId":            result.Audit.PromptID,
		"promptHash":          result.Audit.PromptHash,
		"agentId":             result.AgentID,
		"modelConfigId":       result.ModelConfigID,
		"generatedBy":         result.GeneratedBy,
		"traceId":             traceID,
	}
	c.Header("ETag", workflowDraftETag(result.Draft))
	status := http.StatusOK
	if result.DraftVersion == 1 {
		status = http.StatusCreated
	}
	c.JSON(status, payload)
}

func (r *GenerateSessionRoutes) getSession(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	detail, err := r.sessions.GetSession(c.Request.Context(), c.Param("wid"), c.Param("sid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	turns := make([]generateTurnDTO, len(detail.Turns))
	for i, turn := range detail.Turns {
		turns[i] = generateTurnDTOFor(turn)
	}
	body := gin.H{
		"session": generateSessionDTOFor(detail.Session),
		"turns":   turns,
	}
	if detail.DraftVersion != nil {
		body["draftVersion"] = *detail.DraftVersion
	}
	if detail.Session.WorkflowID != nil {
		body["workflowId"] = *detail.Session.WorkflowID
	}
	if detail.Workflow != nil {
		body["workflow"] = workflowDTOFor(*detail.Workflow)
	}
	if detail.Draft != nil {
		body["draft"] = draftDTOFor(*detail.Draft)
	}
	c.JSON(http.StatusOK, body)
}

type closeGenerateSessionRequest struct {
	// ExpectedSessionLockVersion is optional; old clients may send {} (ZKL-56).
	ExpectedSessionLockVersion *int64 `json:"expectedSessionLockVersion"`
}

func (r *GenerateSessionRoutes) closeSession(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	// Optional body; empty/{} remains valid for old clients.
	var request closeGenerateSessionRequest
	if c.Request.ContentLength != 0 {
		if decodeJSON(c, &request) != nil {
			// Tolerate empty body decode issues by treating as no expected version.
			request = closeGenerateSessionRequest{}
		}
	}
	session, err := r.sessions.CloseSessionWith(c.Request.Context(), smartdag.CloseSessionRequest{
		WorkspaceID:                c.Param("wid"),
		SessionID:                  c.Param("sid"),
		ExpectedSessionLockVersion: request.ExpectedSessionLockVersion,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"sessionId":   session.ID,
		"status":      session.Status,
		"closedAt":    session.ClosedAt,
		"lockVersion": session.LockVersion,
	})
}

type generateSessionDTO struct {
	SessionID     string          `json:"sessionId"`
	AgentID       string          `json:"agentId"`
	ModelConfigID string          `json:"modelConfigId"`
	WorkflowID    *string         `json:"workflowId,omitempty"`
	Status        string          `json:"status"`
	LockVersion   int64           `json:"lockVersion"`
	PromptID      string          `json:"promptId,omitempty"`
	PromptHash    string          `json:"promptHash,omitempty"`
	Constraints   json.RawMessage `json:"constraints,omitempty"`
	CreatedAt     time.Time       `json:"createdAt,omitempty"`
	UpdatedAt     time.Time       `json:"updatedAt,omitempty"`
	ClosedAt      *time.Time      `json:"closedAt,omitempty"`
}

func generateSessionDTOFor(session smartdag.GenerateSession) generateSessionDTO {
	return generateSessionDTO{
		SessionID:     session.ID,
		AgentID:       session.AgentID,
		ModelConfigID: session.ModelConfigID,
		WorkflowID:    session.WorkflowID,
		Status:        session.Status,
		LockVersion:   session.LockVersion,
		PromptID:      session.PromptID,
		PromptHash:    session.PromptHash,
		Constraints:   session.Constraints,
		CreatedAt:     session.CreatedAt,
		UpdatedAt:     session.UpdatedAt,
		ClosedAt:      session.ClosedAt,
	}
}

type generateTurnDTO struct {
	TurnID           string               `json:"turnId"`
	TurnIndex        int                  `json:"turnIndex"`
	UserMessage      string               `json:"userMessage"`
	AssistantMessage string               `json:"assistantMessage,omitempty"`
	GenerationID     string               `json:"generationId"`
	GuardOK          bool                 `json:"guardOk"`
	GuardReport      smartdag.GuardReport `json:"guardReport,omitempty"`
	DraftVersion     *int64               `json:"draftVersion,omitempty"`
	Status           string               `json:"status"`
	ErrorCode        string               `json:"errorCode,omitempty"`
	// FailureStage / Retryable are derived from errorCode at read time (no backfill).
	FailureStage *string    `json:"failureStage,omitempty"`
	Retryable    *bool      `json:"retryable,omitempty"`
	PromptID     string     `json:"promptId,omitempty"`
	PromptHash   string     `json:"promptHash,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
}

func generateTurnDTOFor(turn smartdag.GenerateTurn) generateTurnDTO {
	dto := generateTurnDTO{
		TurnID:           turn.ID,
		TurnIndex:        turn.TurnIndex,
		UserMessage:      turn.UserMessage,
		AssistantMessage: turn.AssistantMessage,
		GenerationID:     turn.GenerationID,
		GuardOK:          turn.GuardOK,
		GuardReport:      turn.GuardReport,
		DraftVersion:     turn.DraftVersion,
		Status:           turn.Status,
		ErrorCode:        turn.ErrorCode,
		PromptID:         turn.PromptID,
		PromptHash:       turn.PromptHash,
		CreatedAt:        turn.CreatedAt,
	}
	// Only terminal non-success turns expose failure projection.
	if turn.Status == smartdag.TurnStatusFailed || turn.Status == smartdag.TurnStatusGuardRejected ||
		strings.TrimSpace(turn.ErrorCode) != "" {
		stage, retryable, _ := smartdag.ClassifyTurnErrorCode(turn.ErrorCode)
		stageStr := string(stage)
		dto.FailureStage = &stageStr
		dto.Retryable = &retryable
	}
	return dto
}

// smartDagTurnErrorContext carries additive public correlation for turn failures.
type smartDagTurnErrorContext struct {
	SessionID          string
	SessionStatus      string
	SessionLockVersion *int64
	TurnID             string
	GenerationID       string
	AgentID            string
	PromptHash         string
	TraceID            string
	GuardReport        *smartdag.GuardReport
}

// RespondSmartDagTurnError writes a standard ErrorDTO with SMART_DAG_TURN_FAILURE
// details, while retaining legacy Guard top-level fields for one compatibility version.
func RespondSmartDagTurnError(c *gin.Context, err error, ctx smartDagTurnErrorContext) {
	mapped := mapError(err)
	_, file, line, _ := runtime.Caller(1)
	c.Set(requestFailureKey, requestFailure{err: err, mapped: mapped, file: file, line: line})
	request, _ := RequestContextFrom(c.Request.Context())
	retryable := mappedRetryable(mapped)
	stage := string(smartdag.FailureStageUnknown)
	code := mapped.code
	message := mapped.message
	if tf, ok := smartdag.AsTurnFailure(err); ok && tf != nil {
		retryable = tf.Retryable
		stage = string(tf.Stage)
		code = tf.Code
		if strings.TrimSpace(tf.Message) != "" {
			message = tf.Message
		}
	} else if errors.Is(err, smartdag.ErrGuardRejected) {
		stage = string(smartdag.FailureStageGuard)
		retryable = true
	}
	detail := map[string]any{
		"kind":  "SMART_DAG_TURN_FAILURE",
		"stage": stage,
	}
	if strings.TrimSpace(ctx.SessionID) != "" {
		detail["sessionId"] = ctx.SessionID
	}
	if strings.TrimSpace(ctx.SessionStatus) != "" {
		detail["sessionStatus"] = ctx.SessionStatus
	}
	if ctx.SessionLockVersion != nil {
		detail["sessionLockVersion"] = *ctx.SessionLockVersion
	}
	if strings.TrimSpace(ctx.TurnID) != "" {
		detail["turnId"] = ctx.TurnID
	}
	if strings.TrimSpace(ctx.GenerationID) != "" {
		detail["generationId"] = ctx.GenerationID
	}
	body := gin.H{
		"error": ErrorDTO{
			Code:      code,
			Message:   message,
			RequestID: request.RequestID,
			TraceID:   firstNonEmpty(request.TraceID, ctx.TraceID),
			Retryable: retryable,
			Details:   []map[string]any{detail},
		},
	}
	// Legacy Guard top-level fields retained one version (ZKL-56 §6.1).
	if ctx.GuardReport != nil {
		body["guardReport"] = *ctx.GuardReport
	}
	if strings.TrimSpace(ctx.SessionID) != "" {
		body["sessionId"] = ctx.SessionID
	}
	if strings.TrimSpace(ctx.TurnID) != "" {
		body["turnId"] = ctx.TurnID
	}
	if strings.TrimSpace(ctx.GenerationID) != "" {
		body["generationId"] = ctx.GenerationID
	}
	if strings.TrimSpace(ctx.AgentID) != "" {
		body["agentId"] = ctx.AgentID
	}
	if strings.TrimSpace(ctx.PromptHash) != "" {
		body["promptHash"] = ctx.PromptHash
	}
	if strings.TrimSpace(ctx.TraceID) != "" {
		body["traceId"] = ctx.TraceID
	}
	c.AbortWithStatusJSON(mapped.status, body)
}

// RespondErrorWithDetails maps err and merges extra top-level fields into the error response.
// Used for GUARD_REJECTED to include guardReport without changing mapError contract.
func RespondErrorWithDetails(c *gin.Context, err error, details gin.H) {
	mapped := mapError(err)
	_, file, line, _ := runtime.Caller(1)
	c.Set(requestFailureKey, requestFailure{err: err, mapped: mapped, file: file, line: line})
	request, _ := RequestContextFrom(c.Request.Context())
	body := gin.H{
		"error": ErrorDTO{
			Code:      mapped.code,
			Message:   mapped.message,
			RequestID: request.RequestID,
			TraceID:   request.TraceID,
			Retryable: mappedRetryable(mapped),
			Details:   []map[string]any{},
		},
	}
	for key, value := range details {
		body[key] = value
	}
	c.AbortWithStatusJSON(mapped.status, body)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
