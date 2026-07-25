package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"actweave/backend/internal/audit"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/storedobject"
	"actweave/backend/internal/workspace"

	"github.com/gin-gonic/gin"
)

type AuditQuery interface {
	Query(context.Context, workspace.Role, audit.QueryInput) ([]audit.Event, error)
	Get(context.Context, workspace.Role, string, string, bool) (audit.Event, error)
	OpenPayload(context.Context, string, string, string, workspace.Role) (storedobject.OpenedObject, error)
}

type AuditExporter interface {
	Create(context.Context, audit.CreateExportInput) (audit.Export, error)
	Get(context.Context, string, string, workspace.Role) (audit.Export, error)
	DownloadURL(context.Context, string, string, string, workspace.Role, time.Duration) (*url.URL, error)
}

type AuditRoutes struct {
	authorizer WorkspaceAuthorizer
	queries    AuditQuery
	exports    AuditExporter
}

func NewAuditRoutes(authorizer WorkspaceAuthorizer, queries AuditQuery, exports AuditExporter) (*AuditRoutes, error) {
	if authorizer == nil || queries == nil || exports == nil {
		return nil, errors.New("audit route dependencies are required")
	}
	return &AuditRoutes{authorizer: authorizer, queries: queries, exports: exports}, nil
}

func (r *AuditRoutes) RegisterV1(v1 V1Routes) {
	group := v1.Protected
	group.GET("/workspaces/:wid/audit-events", r.listEvents)
	group.GET("/workspaces/:wid/audit-events/:id", r.getEvent)
	group.POST("/workspaces/:wid/audit-exports", r.createExport)
	group.GET("/workspaces/:wid/audit-exports/:id", r.getExport)
}

func (r *AuditRoutes) workspaceContext(c *gin.Context, action authz.Action) (authz.WorkspaceContext, bool) {
	principal, _ := PrincipalFrom(c.Request.Context())
	value, err := r.authorizer.AuthorizeWorkspace(c.Request.Context(), principal.UserID, c.Param("wid"), action)
	if err != nil {
		RespondError(c, err)
		return authz.WorkspaceContext{}, false
	}
	return value, true
}

type auditEventDTO struct {
	ID            string          `json:"id"`
	OccurredAt    time.Time       `json:"occurredAt"`
	ActorType     string          `json:"actorType"`
	ActorID       string          `json:"actorId,omitempty"`
	ActorDisplay  string          `json:"actorDisplay"`
	Action        string          `json:"action"`
	ResourceType  string          `json:"resourceType"`
	ResourceID    string          `json:"resourceId,omitempty"`
	Result        string          `json:"result"`
	RequestID     string          `json:"requestId,omitempty"`
	TraceID       string          `json:"traceId,omitempty"`
	SourceIP      string          `json:"sourceIp,omitempty"`
	UserAgent     string          `json:"userAgent,omitempty"`
	Changes       json.RawMessage `json:"changes"`
	Metadata      json.RawMessage `json:"metadata"`
	Payload       json.RawMessage `json:"payload,omitempty"`
	SchemaVersion string          `json:"schemaVersion"`
}

func auditEventDTOFor(value audit.Event) auditEventDTO {
	sourceIP := ""
	if value.SourceIP.IsValid() {
		sourceIP = value.SourceIP.String()
	}
	return auditEventDTO{ID: value.ID, OccurredAt: value.OccurredAt,
		ActorType: value.ActorType, ActorID: value.ActorID, ActorDisplay: value.ActorDisplay,
		Action: value.Action, ResourceType: value.ResourceType, ResourceID: value.ResourceID,
		Result: value.Result, RequestID: value.RequestID, TraceID: value.TraceID,
		SourceIP: sourceIP, UserAgent: value.UserAgent, Changes: value.Changes,
		Metadata: value.Metadata, SchemaVersion: value.SchemaVersion}
}

func (r *AuditRoutes) listEvents(c *gin.Context) {
	workspaceContext, ok := r.workspaceContext(c, authz.ActionView)
	if !ok {
		return
	}
	input, err := auditQueryFromRequest(c)
	if err != nil {
		RespondError(c, err)
		return
	}
	input.WorkspaceID = c.Param("wid")
	input.IncludeSensitive = canManageAuditRole(workspaceContext.Role)
	values, err := r.queries.Query(c.Request.Context(), workspaceContext.Role, input)
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]auditEventDTO, len(values))
	for index := range values {
		items[index] = auditEventDTOFor(values[index])
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (r *AuditRoutes) getEvent(c *gin.Context) {
	workspaceContext, ok := r.workspaceContext(c, authz.ActionView)
	if !ok {
		return
	}
	includeSensitive := canManageAuditRole(workspaceContext.Role)
	value, err := r.queries.Get(c.Request.Context(), workspaceContext.Role,
		c.Param("wid"), c.Param("id"), includeSensitive)
	if err != nil {
		RespondError(c, err)
		return
	}
	dto := auditEventDTOFor(value)
	if includeSensitive && value.PayloadObjectID != "" {
		opened, err := r.queries.OpenPayload(c.Request.Context(), c.Param("wid"), value.ID, actor(c), workspaceContext.Role)
		if err != nil {
			RespondError(c, err)
			return
		}
		defer opened.Body.Close()
		payload, err := io.ReadAll(io.LimitReader(opened.Body, 8<<20))
		if err != nil || !json.Valid(payload) {
			RespondError(c, audit.ErrInvalid)
			return
		}
		dto.Payload = append(json.RawMessage(nil), payload...)
	}
	c.JSON(http.StatusOK, dto)
}

func auditQueryFromRequest(c *gin.Context) (audit.QueryInput, error) {
	limit, err := optionalPositiveInt(c.Query("limit"), 100, 500)
	if err != nil {
		return audit.QueryInput{}, audit.ErrInvalid
	}
	input := audit.QueryInput{ActorType: c.Query("actorType"), ActorID: c.Query("actorId"),
		ResourceType: c.Query("resourceType"), ResourceID: c.Query("resourceId"),
		Action: c.Query("action"), RequestID: c.Query("requestId"), TraceID: c.Query("traceId"),
		BeforeID: c.Query("beforeId"), Limit: limit}
	for _, value := range c.QueryArray("result") {
		input.Results = append(input.Results, strings.Split(value, ",")...)
	}
	if value := strings.TrimSpace(c.Query("occurredFrom")); value != "" {
		input.OccurredFrom, err = time.Parse(time.RFC3339, value)
		if err != nil {
			return audit.QueryInput{}, audit.ErrInvalid
		}
	}
	if value := strings.TrimSpace(c.Query("occurredUntil")); value != "" {
		input.OccurredUntil, err = time.Parse(time.RFC3339, value)
		if err != nil {
			return audit.QueryInput{}, audit.ErrInvalid
		}
	}
	if value := strings.TrimSpace(c.Query("beforeOccurredAt")); value != "" {
		input.BeforeOccurredAt, err = time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return audit.QueryInput{}, audit.ErrInvalid
		}
	}
	return input, nil
}

type createAuditExportRequest struct {
	ActorType        string     `json:"actorType"`
	ActorID          string     `json:"actorId"`
	ResourceType     string     `json:"resourceType"`
	ResourceID       string     `json:"resourceId"`
	Action           string     `json:"action"`
	Results          []string   `json:"results"`
	RequestID        string     `json:"requestId"`
	TraceID          string     `json:"traceId"`
	OccurredFrom     *time.Time `json:"occurredFrom"`
	OccurredUntil    *time.Time `json:"occurredUntil"`
	ExpiresInSeconds int        `json:"expiresInSeconds"`
}

func (request createAuditExportRequest) filter() audit.QueryInput {
	value := audit.QueryInput{ActorType: request.ActorType, ActorID: request.ActorID,
		ResourceType: request.ResourceType, ResourceID: request.ResourceID, Action: request.Action,
		Results: request.Results, RequestID: request.RequestID, TraceID: request.TraceID}
	if request.OccurredFrom != nil {
		value.OccurredFrom = *request.OccurredFrom
	}
	if request.OccurredUntil != nil {
		value.OccurredUntil = *request.OccurredUntil
	}
	return value
}

func (r *AuditRoutes) createExport(c *gin.Context) {
	workspaceContext, ok := r.workspaceContext(c, authz.ActionManage)
	if !ok {
		return
	}
	var request createAuditExportRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, audit.ErrInvalid)
		return
	}
	if request.ExpiresInSeconds == 0 {
		request.ExpiresInSeconds = 3600
	}
	if request.ExpiresInSeconds < 60 || request.ExpiresInSeconds > 86400 {
		RespondError(c, audit.ErrInvalid)
		return
	}
	id, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	requestContext, _ := RequestContextFrom(c.Request.Context())
	ctx := audit.WithRequestContext(c.Request.Context(), audit.RequestContext{
		RequestID: requestContext.RequestID, TraceID: requestContext.TraceID,
		SourceIP: requestContext.SourceIP, UserAgent: requestContext.UserAgent,
	})
	value, err := r.exports.Create(ctx, audit.CreateExportInput{ID: id,
		WorkspaceID: c.Param("wid"), RequestedBy: actor(c), Role: workspaceContext.Role,
		Filter: request.filter(), ExpiresAt: time.Now().UTC().Add(time.Duration(request.ExpiresInSeconds) * time.Second)})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, auditExportDTOFor(value, ""))
}

type auditExportDTO struct {
	ID             string          `json:"id"`
	FilterSnapshot json.RawMessage `json:"filterSnapshot"`
	Status         string          `json:"status"`
	RequestedBy    string          `json:"requestedBy"`
	RequestedAt    time.Time       `json:"requestedAt"`
	CompletedAt    *time.Time      `json:"completedAt,omitempty"`
	ExpiresAt      time.Time       `json:"expiresAt"`
	ErrorCode      string          `json:"errorCode,omitempty"`
	DownloadURL    string          `json:"downloadUrl,omitempty"`
}

func auditExportDTOFor(value audit.Export, downloadURL string) auditExportDTO {
	return auditExportDTO{value.ID, value.FilterSnapshot, value.Status, value.RequestedBy,
		value.RequestedAt, value.CompletedAt, value.ExpiresAt, value.ErrorCode, downloadURL}
}

func (r *AuditRoutes) getExport(c *gin.Context) {
	workspaceContext, ok := r.workspaceContext(c, authz.ActionManage)
	if !ok {
		return
	}
	value, err := r.exports.Get(c.Request.Context(), c.Param("wid"), c.Param("id"), workspaceContext.Role)
	if err != nil {
		RespondError(c, err)
		return
	}
	downloadURL := ""
	if value.Status == "SUCCEEDED" {
		signed, err := r.exports.DownloadURL(c.Request.Context(), c.Param("wid"), value.ID,
			actor(c), workspaceContext.Role, 5*time.Minute)
		if err != nil {
			RespondError(c, err)
			return
		}
		downloadURL = signed.String()
	}
	c.JSON(http.StatusOK, auditExportDTOFor(value, downloadURL))
}

func canManageAuditRole(role workspace.Role) bool {
	return role == workspace.RoleOwner || role == workspace.RoleAdmin
}
