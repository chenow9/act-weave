package a2agateway

import (
	"context"
	"net/http"
	"strings"
)

// AccessTokenVerifier validates AAP access tokens (agent access plane).
// Implemented by agentaccessauth.AAPAccessTokenVerifier.
type AccessTokenVerifier interface {
	VerifyAccessToken(ctx context.Context, value string) (AccessTokenClaims, error)
}

// AccessTokenClaims is the subset of AAP token claims needed for A2A inbound authz.
type AccessTokenClaims struct {
	PrincipalID        string
	ServicePrincipalID string
	WorkspaceID        string
	AgentID            string
	Scopes             []string
}

// AgentAccessAuth enforces AGENT_ACCESS mode via real AAP access token verification.
// NONE mode is only permitted when AllowAuthNone is true (explicit dev/test gate).
type AgentAccessAuth struct {
	Verifier AccessTokenVerifier
	// RequiredScopes: at least one must be present (default: run:create).
	RequiredScopes []string
	// AllowAuthNone permits exposure AuthMode=NONE (dev/test only).
	AllowAuthNone bool
}

// DefaultInboundScopes required for production A2A invoke.
var DefaultInboundScopes = []string{"run:create", "aap.run.create"}

func (a AgentAccessAuth) Authorize(ctx context.Context, r *http.Request, exp Exposure) (actorType, actorID string, err error) {
	if exp.AuthMode == AuthModeNone {
		if !a.AllowAuthNone {
			return "", "", ErrAuthRejected
		}
		return "SYSTEM", exp.ID, nil
	}
	if a.Verifier == nil {
		return "", "", ErrAuthRejected
	}
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if raw == "" {
		return "", "", ErrAuthRejected
	}
	const bearer = "Bearer "
	token := raw
	if len(raw) > len(bearer) && strings.EqualFold(raw[:len(bearer)], bearer) {
		token = strings.TrimSpace(raw[len(bearer):])
	}
	if token == "" {
		return "", "", ErrAuthRejected
	}
	claims, err := a.Verifier.VerifyAccessToken(ctx, token)
	if err != nil {
		return "", "", ErrAuthRejected
	}
	// Fail closed: claims MUST bind workspace and agent to the exposure.
	if strings.TrimSpace(claims.WorkspaceID) == "" || claims.WorkspaceID != exp.WorkspaceID {
		return "", "", ErrAuthRejected
	}
	if strings.TrimSpace(claims.AgentID) == "" || claims.AgentID != exp.AgentID {
		return "", "", ErrAuthRejected
	}
	scopes := a.RequiredScopes
	if len(scopes) == 0 {
		scopes = DefaultInboundScopes
	}
	if !hasAnyScope(claims.Scopes, scopes) {
		return "", "", ErrAuthRejected
	}
	actorID = firstNonEmpty(claims.ServicePrincipalID, claims.PrincipalID)
	if actorID == "" {
		return "", "", ErrAuthRejected
	}
	return "SERVICE_PRINCIPAL", actorID, nil
}

func hasAnyScope(have, need []string) bool {
	for _, n := range need {
		for _, h := range have {
			if h == n {
				return true
			}
		}
	}
	return false
}

// AccessTokenVerifierFunc adapts a function to AccessTokenVerifier.
type AccessTokenVerifierFunc func(ctx context.Context, value string) (AccessTokenClaims, error)

func (f AccessTokenVerifierFunc) VerifyAccessToken(ctx context.Context, value string) (AccessTokenClaims, error) {
	return f(ctx, value)
}
