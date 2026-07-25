package httptransport

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"actweave/backend/internal/identity"
	"actweave/backend/internal/workspaceoverview"

	"github.com/gin-gonic/gin"
)

// WorkspaceOverviewMetrics loads multi-workspace overview KPIs and series.
type WorkspaceOverviewMetrics interface {
	Metrics(ctx context.Context, workspaceIDs []string, from, toInclusive time.Time) (workspaceoverview.Metrics, error)
	ListAccessibleWorkspaceIDs(ctx context.Context, userID string, platformAdmin bool) ([]string, error)
}

type WorkspaceOverviewRoutes struct {
	metrics WorkspaceOverviewMetrics
}

func NewWorkspaceOverviewRoutes(metrics WorkspaceOverviewMetrics) (*WorkspaceOverviewRoutes, error) {
	if metrics == nil {
		return nil, errors.New("workspace overview metrics are required")
	}
	return &WorkspaceOverviewRoutes{metrics: metrics}, nil
}

func (r *WorkspaceOverviewRoutes) RegisterV1(v1 V1Routes) {
	// Platform-wide overview: aggregates all workspaces the principal can see.
	v1.Protected.GET("/overview/metrics", r.getMetrics)
}

func (r *WorkspaceOverviewRoutes) getMetrics(c *gin.Context) {
	principal, ok := PrincipalFrom(c.Request.Context())
	if !ok || strings.TrimSpace(principal.UserID) == "" {
		RespondError(c, ErrUnauthenticated)
		return
	}
	platformAdmin := principal.PlatformRole == string(identity.PlatformRoleAdmin)
	ids, err := r.metrics.ListAccessibleWorkspaceIDs(c.Request.Context(), principal.UserID, platformAdmin)
	if err != nil {
		RespondError(c, err)
		return
	}

	from, to, err := parseOverviewDateRange(c.Query("from"), c.Query("to"))
	if err != nil {
		RespondError(c, identity.ErrInvalid)
		return
	}

	value, err := r.metrics.Metrics(c.Request.Context(), ids, from, to)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, value)
}

// parseOverviewDateRange accepts inclusive YYYY-MM-DD dates (or RFC3339).
// Empty values mean "use service default range".
func parseOverviewDateRange(fromRaw, toRaw string) (time.Time, time.Time, error) {
	fromRaw, toRaw = strings.TrimSpace(fromRaw), strings.TrimSpace(toRaw)
	var from, to time.Time
	var err error
	if fromRaw != "" {
		from, err = parseOverviewDate(fromRaw)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	if toRaw != "" {
		to, err = parseOverviewDate(toRaw)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	return from, to, nil
}

func parseOverviewDate(raw string) (time.Time, error) {
	if t, err := time.ParseInLocation("2006-01-02", raw, time.UTC); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, errors.New("invalid date")
}
