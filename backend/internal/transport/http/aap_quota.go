package httptransport

import (
	"strconv"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"

	"github.com/gin-gonic/gin"
)

func enforceAAPCommandQuota(
	c *gin.Context,
	quota agentaccess.DataPlaneQuota,
	operation agentaccess.DataPlaneQuotaOperation,
	principal agentaccessauth.AAPAccessTokenPrincipal,
	authorization agentaccessauth.AAPAuthorizationDecision,
) bool {
	if quota == nil {
		return true
	}
	decision, err := quota.Allow(c.Request.Context(), agentaccess.DataPlaneQuotaRequest{
		Operation: operation, WorkspaceID: principal.WorkspaceID,
		AgentID: principal.AgentID, ClientID: authorization.Snapshot.ClientID,
		ServicePrincipalID: principal.ServicePrincipalID, SubjectID: principal.PrincipalID,
	})
	if decision.Limit > 0 {
		c.Header("RateLimit-Limit", strconv.Itoa(decision.Limit))
		c.Header("RateLimit-Remaining", strconv.Itoa(max(decision.Remaining, 0)))
		reset := int64(time.Until(decision.ResetAt).Seconds())
		if reset < 0 {
			reset = 0
		}
		c.Header("RateLimit-Reset", strconv.FormatInt(reset, 10))
	}
	if err != nil {
		if decision.RetryAfter > 0 {
			c.Header("Retry-After", decision.RetryAfterSeconds())
		}
		RespondError(c, err)
		return false
	}
	return true
}
