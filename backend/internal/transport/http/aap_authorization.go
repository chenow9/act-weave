package httptransport

import (
	"context"
	"errors"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/metrics"

	"github.com/gin-gonic/gin"
)

// AAPDataPlaneAuthorizer is the single authorization boundary shared by AAP
// Resource and Command routes. Handlers must not infer permission from JWT
// claims alone: Authorize re-reads current Client, Grant, Agent and Workspace
// state and produces the immutable authorization snapshot used downstream.
type AAPDataPlaneAuthorizer interface {
	Authorize(
		context.Context,
		agentaccessauth.AAPAuthorizationRequest,
	) (agentaccessauth.AAPAuthorizationDecision, error)
}

func authorizeAAPRequest(
	c *gin.Context,
	authorizer AAPDataPlaneAuthorizer,
	action agentaccessauth.AAPAction,
	resource agentaccessauth.AAPAuthorizationResource,
) (agentaccessauth.AAPAccessTokenPrincipal, agentaccessauth.AAPAuthorizationDecision, bool) {
	principal, ok := AAPPrincipalFrom(c.Request.Context())
	if !ok || authorizer == nil {
		RespondError(c, ErrUnauthenticated)
		return agentaccessauth.AAPAccessTokenPrincipal{}, agentaccessauth.AAPAuthorizationDecision{}, false
	}
	if principal.WorkspaceID != c.Param("wid") || principal.AgentID != c.Param("aid") {
		RespondError(c, agentaccessauth.ErrAAPAuthorizationNotVisible)
		return agentaccessauth.AAPAccessTokenPrincipal{}, agentaccessauth.AAPAuthorizationDecision{}, false
	}
	decision, err := authorizer.Authorize(c.Request.Context(), agentaccessauth.AAPAuthorizationRequest{
		Principal: principal, Action: action, Resource: resource,
	})
	if err != nil {
		if errors.Is(err, agentaccessauth.ErrAAPAuthorizationDenied) ||
			errors.Is(err, agentaccessauth.ErrAAPAuthorizationNotVisible) {
			metrics.Default().ObserveAuthorizationDenied(map[string]string{
				"workspace_id": principal.WorkspaceID,
				"agent_id":     principal.AgentID,
				"client_id":    principal.AuthorizedParty,
				"operation":    string(action),
				"reason":       authorizationDeniedReason(err),
			})
		}
		RespondError(c, err)
		return agentaccessauth.AAPAccessTokenPrincipal{}, agentaccessauth.AAPAuthorizationDecision{}, false
	}
	return principal, decision, true
}

func authorizationDeniedReason(err error) string {
	switch {
	case errors.Is(err, agentaccessauth.ErrAAPAuthorizationDenied):
		return "denied"
	case errors.Is(err, agentaccessauth.ErrAAPAuthorizationNotVisible):
		return "not_visible"
	default:
		return "unknown"
	}
}
