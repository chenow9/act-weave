package httptransport

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/agentaudit"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/identity"

	"github.com/gin-gonic/gin"
)

// AgentAuditQuery is the read-model used by PLATFORM_ADMIN agent full-trace APIs.
type AgentAuditQuery interface {
	DebugMode() bool
	ListTraces(context.Context, string, agentaudit.ListFilter) (agentaudit.ListResult, error)
	GetTrace(context.Context, string, string, agentaudit.DetailFilter) (agentaudit.TraceDetail, error)
}

type AgentAuditRoutes struct {
	queries AgentAuditQuery
}

func NewAgentAuditRoutes(queries AgentAuditQuery) (*AgentAuditRoutes, error) {
	if queries == nil {
		return nil, errors.New("agent audit queries are required")
	}
	return &AgentAuditRoutes{queries: queries}, nil
}

func (r *AgentAuditRoutes) RegisterV1(v1 V1Routes) {
	group := v1.Protected
	group.GET("/workspaces/:wid/agent-audit/traces", r.listTraces)
	group.GET("/workspaces/:wid/agent-audit/traces/:traceId", r.getTrace)
}

func (r *AgentAuditRoutes) requirePlatformAdmin(c *gin.Context) bool {
	principal, ok := PrincipalFrom(c.Request.Context())
	if !ok || principal.PlatformRole != string(identity.PlatformRoleAdmin) {
		RespondError(c, authz.ErrDenied)
		return false
	}
	return true
}

func (r *AgentAuditRoutes) listTraces(c *gin.Context) {
	if !r.requirePlatformAdmin(c) {
		return
	}
	workspaceID := strings.TrimSpace(c.Param("wid"))
	if !validPathUUID(workspaceID) {
		RespondError(c, identity.ErrInvalid)
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	if status != "" && status != "success" && status != "error" && status != "running" {
		RespondError(c, identity.ErrInvalid)
		return
	}
	from, to := strings.TrimSpace(c.Query("from")), strings.TrimSpace(c.Query("to"))
	if !validAuditDate(from) || !validAuditDate(to) {
		RespondError(c, identity.ErrInvalid)
		return
	}
	if page, err := strconv.Atoi(c.Query("page")); err == nil && page > 0 {
		if limit <= 0 {
			limit = 10
		}
		offset = (page - 1) * limit
	}
	result, err := r.queries.ListTraces(c.Request.Context(), workspaceID, agentaudit.ListFilter{
		Query: c.Query("q"), Status: status, From: from, To: to, Limit: limit, Offset: offset,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func validAuditDate(value string) bool {
	if value == "" {
		return true
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func (r *AgentAuditRoutes) getTrace(c *gin.Context) {
	if !r.requirePlatformAdmin(c) {
		return
	}
	workspaceID := strings.TrimSpace(c.Param("wid"))
	traceID := strings.TrimSpace(c.Param("traceId"))
	if !validPathUUID(workspaceID) || traceID == "" {
		RespondError(c, identity.ErrInvalid)
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	// page is 1-based convenience; offset wins when both are set.
	if offset <= 0 {
		if page, err := strconv.Atoi(c.Query("page")); err == nil && page > 0 {
			if limit <= 0 {
				limit = agentaudit.DefaultDetailStepLimit
			}
			offset = (page - 1) * limit
		}
	}
	detail, err := r.queries.GetTrace(c.Request.Context(), workspaceID, traceID, agentaudit.DetailFilter{
		Limit: limit, Offset: offset,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}
