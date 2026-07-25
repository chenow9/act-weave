package httptransport

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/execution"

	"github.com/gin-gonic/gin"
)

type AAPConversationApplication interface {
	Create(context.Context, aap.CreateConversationInput) (aap.CreateConversationResult, error)
	Get(context.Context, aap.GetConversationInput) (aap.ConversationView, error)
}

type AAPConversationRoutes struct {
	authorizer    AAPDataPlaneAuthorizer
	conversations AAPConversationApplication
	quota         agentaccess.DataPlaneQuota
}

func (routes *AAPConversationRoutes) ConfigureCommandQuota(quota agentaccess.DataPlaneQuota) error {
	if routes == nil || quota == nil || routes.quota != nil {
		return aap.ErrConversationInvalid
	}
	routes.quota = quota
	return nil
}

func NewAAPConversationRoutes(
	authorizer AAPDataPlaneAuthorizer,
	conversations AAPConversationApplication,
) (*AAPConversationRoutes, error) {
	if authorizer == nil || conversations == nil {
		return nil, aap.ErrConversationInvalid
	}
	return &AAPConversationRoutes{authorizer: authorizer, conversations: conversations}, nil
}

func (routes *AAPConversationRoutes) RegisterAgentAccessV1(v1 AgentAccessV1Routes) {
	base := "/workspaces/:wid/agents/:aid/conversations"
	v1.Protected.POST(base, routes.createConversation)
	v1.Protected.GET(base+"/:cid", routes.getConversation)
}

type aapCreateConversationRequest struct {
	Title string `json:"title"`
}

type aapConversationDTO struct {
	Object      string             `json:"object"`
	ID          string             `json:"id"`
	AgentID     string             `json:"agentId"`
	Title       string             `json:"title"`
	Status      string             `json:"status"`
	Version     int64              `json:"version"`
	LatestRunID string             `json:"latestRunId,omitempty"`
	CreatedAt   time.Time          `json:"createdAt"`
	UpdatedAt   time.Time          `json:"updatedAt"`
	Runs        []aapRunSummaryDTO `json:"runs"`
}

type aapRunSummaryDTO struct {
	Object      string     `json:"object"`
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Version     int64      `json:"version"`
	ErrorCode   string     `json:"errorCode,omitempty"`
	StartedAt   time.Time  `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

type aapCreateConversationResponse struct {
	Conversation aapConversationDTO `json:"conversation"`
	Idempotent   bool               `json:"idempotent"`
}

func (routes *AAPConversationRoutes) createConversation(c *gin.Context) {
	caller, authorization, ok := authorizeAAPRequest(
		c, routes.authorizer, agentaccessauth.ActionConversationCreate,
		agentaccessauth.AAPAuthorizationResource{},
	)
	if !ok {
		return
	}
	var request aapCreateConversationRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, aap.ErrConversationInvalid)
		return
	}
	if !canonicalHTTPUUID(strings.ToLower(strings.TrimSpace(c.GetHeader("Idempotency-Key")))) ||
		!enforceAAPCommandQuota(c, routes.quota, agentaccess.QuotaConversationCreate, caller, authorization) {
		if !c.IsAborted() {
			RespondError(c, aap.ErrConversationInvalid)
		}
		return
	}
	result, err := routes.conversations.Create(c.Request.Context(), aap.CreateConversationInput{
		Scope:     aap.ConversationScope{WorkspaceID: c.Param("wid"), AgentID: c.Param("aid")},
		Principal: caller, Authorization: authorization,
		Title: request.Title, IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	status := http.StatusCreated
	if result.Idempotent {
		status = http.StatusOK
	}
	location := "/api/agent-access/v1/workspaces/" + c.Param("wid") +
		"/agents/" + c.Param("aid") + "/conversations/" + result.Conversation.ID
	c.Header("Location", location)
	setAAPConversationETag(c, result.Conversation.LockVersion)
	c.JSON(status, aapCreateConversationResponse{
		Conversation: aapConversationDTOFor(result.Conversation, nil),
		Idempotent:   result.Idempotent,
	})
}

func (routes *AAPConversationRoutes) getConversation(c *gin.Context) {
	caller, authorization, ok := authorizeAAPRequest(
		c, routes.authorizer, agentaccessauth.ActionConversationRead,
		agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceConversation, ID: strings.TrimSpace(c.Param("cid")),
		},
	)
	if !ok {
		return
	}
	view, err := routes.conversations.Get(c.Request.Context(), aap.GetConversationInput{
		Scope:     aap.ConversationScope{WorkspaceID: c.Param("wid"), AgentID: c.Param("aid")},
		Principal: caller, Authorization: authorization,
		ConversationID: c.Param("cid"), RunLimit: 50,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	etag := aapConversationETag(view.Conversation.LockVersion)
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, no-cache")
	if ifNoneMatch(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.JSON(http.StatusOK, aapConversationDTOFor(view.Conversation, view.Runs))
}

func aapConversationDTOFor(value chat.Session, runs []execution.AgentRun) aapConversationDTO {
	items := make([]aapRunSummaryDTO, len(runs))
	for index := range runs {
		items[index] = aapRunSummaryDTOFor(runs[index])
	}
	return aapConversationDTO{
		Object: "conversation", ID: value.ID, AgentID: value.AgentID,
		Title: value.Title, Status: strings.ToLower(value.Status), Version: value.LockVersion,
		LatestRunID: value.LatestRunID, CreatedAt: value.CreatedAt.UTC(),
		UpdatedAt: value.UpdatedAt.UTC(), Runs: items,
	}
}

func aapRunSummaryDTOFor(value execution.AgentRun) aapRunSummaryDTO {
	var completedAt *time.Time
	if value.FinishedAt != nil {
		finished := value.FinishedAt.UTC()
		completedAt = &finished
	}
	return aapRunSummaryDTO{
		Object: "run", ID: value.ID, Status: publicAAPRunStatus(value.Status),
		Version: value.LockVersion, ErrorCode: value.ErrorCode,
		StartedAt: value.StartedAt.UTC(), CompletedAt: completedAt,
	}
}

func publicAAPRunStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PENDING":
		return "accepted"
	case "RUNNING":
		return "running"
	case "WAITING_CONFIRMATION":
		return "waiting_interaction"
	case "SUCCEEDED":
		return "completed"
	case "FAILED":
		return "failed"
	case "CANCELLED":
		return "cancelled"
	default:
		return "unknown"
	}
}

func setAAPConversationETag(c *gin.Context, version int64) {
	c.Header("ETag", aapConversationETag(version))
}

func aapConversationETag(version int64) string {
	return `"conversation:` + strconv.FormatInt(version, 10) + `"`
}
