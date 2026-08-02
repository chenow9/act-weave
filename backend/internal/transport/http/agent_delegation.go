package httptransport

import (
	"errors"
	"net/http"
	"strings"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/identity"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AgentDelegationRoutes manages internal bindings + A2A exposure/remote configs.
type AgentDelegationRoutes struct {
	authorizer    WorkspaceAuthorizer
	bindings      *agentdelegation.Service
	a2a           *a2agateway.Repository
	baseURL       string
	allowAuthNone bool
}

// AgentDelegationRouteOptions optional wiring for capabilities.
type AgentDelegationRouteOptions struct {
	// AllowAuthNone exposes AuthMode=NONE as selectable in UI/capabilities.
	// Production default is false (env ACTWEAVE_A2A_ALLOW_AUTH_NONE).
	AllowAuthNone bool
}

func NewAgentDelegationRoutes(
	authorizer WorkspaceAuthorizer,
	bindings *agentdelegation.Service,
	a2a *a2agateway.Repository,
	baseURL string,
	opts ...AgentDelegationRouteOptions,
) (*AgentDelegationRoutes, error) {
	if authorizer == nil || bindings == nil {
		return nil, errors.New("agent delegation routes require authorizer and bindings")
	}
	r := &AgentDelegationRoutes{authorizer: authorizer, bindings: bindings, a2a: a2a, baseURL: baseURL}
	if len(opts) > 0 {
		r.allowAuthNone = opts[0].AllowAuthNone
	}
	return r, nil
}

func (r *AgentDelegationRoutes) RegisterV1(v1 V1Routes) {
	g := v1.Protected
	// Capabilities (allowAuthNone etc.) — UI must not hard-disable NONE.
	g.GET("/workspaces/:wid/a2a/capabilities", r.getCapabilities)
	// Internal bindings
	g.GET("/workspaces/:wid/agents/:id/delegation-bindings", r.listBindings)
	g.POST("/workspaces/:wid/agents/:id/delegation-bindings", r.createBinding)
	g.PATCH("/workspaces/:wid/delegation-bindings/:bid", r.updateBinding)
	g.DELETE("/workspaces/:wid/delegation-bindings/:bid", r.disableBinding)
	// A2A exposure (inbound allowlist)
	g.GET("/workspaces/:wid/a2a/exposures", r.listExposures)
	g.POST("/workspaces/:wid/a2a/exposures", r.createExposure)
	g.PATCH("/workspaces/:wid/a2a/exposures/:eid", r.updateExposure)
	g.DELETE("/workspaces/:wid/a2a/exposures/:eid", r.disableExposure)
	g.GET("/workspaces/:wid/a2a/exposures/:eid/agent-card", r.previewCard)
	// A2A remote (outbound)
	g.GET("/workspaces/:wid/agents/:id/a2a-remotes", r.listRemotes)
	g.POST("/workspaces/:wid/agents/:id/a2a-remotes", r.createRemote)
	g.PATCH("/workspaces/:wid/a2a-remotes/:rid", r.updateRemote)
	g.DELETE("/workspaces/:wid/a2a-remotes/:rid", r.disableRemote)
}

func (r *AgentDelegationRoutes) getCapabilities(c *gin.Context) {
	wid := strings.TrimSpace(c.Param("wid"))
	if !validPathUUID(wid) {
		RespondError(c, identity.ErrInvalid)
		return
	}
	if !r.requireRead(c, wid) {
		return
	}
	// Server-authoritative feature flags for Console UI (never hardcode client-side).
	c.JSON(http.StatusOK, gin.H{
		"allowAuthNone": r.allowAuthNone,
		"authModes": func() []string {
			modes := []string{a2agateway.AuthModeAgentAccess}
			if r.allowAuthNone {
				modes = append(modes, a2agateway.AuthModeNone)
			}
			return modes
		}(),
		"softDisable": true, // enabled=false keeps row listable/re-enableable
	})
}

func (r *AgentDelegationRoutes) requireWrite(c *gin.Context, workspaceID string) (string, bool) {
	principal, ok := PrincipalFrom(c.Request.Context())
	if !ok {
		RespondError(c, authz.ErrDenied)
		return "", false
	}
	if _, err := r.authorizer.AuthorizeWorkspace(c.Request.Context(), principal.UserID, workspaceID, authz.ActionEdit); err != nil {
		RespondError(c, err)
		return "", false
	}
	return principal.UserID, true
}

func (r *AgentDelegationRoutes) requireRead(c *gin.Context, workspaceID string) bool {
	principal, ok := PrincipalFrom(c.Request.Context())
	if !ok {
		RespondError(c, authz.ErrDenied)
		return false
	}
	if _, err := r.authorizer.AuthorizeWorkspace(c.Request.Context(), principal.UserID, workspaceID, authz.ActionView); err != nil {
		RespondError(c, err)
		return false
	}
	return true
}

func (r *AgentDelegationRoutes) listBindings(c *gin.Context) {
	wid := strings.TrimSpace(c.Param("wid"))
	agentID := strings.TrimSpace(c.Param("id"))
	if !validPathUUID(wid) || !validPathUUID(agentID) {
		RespondError(c, identity.ErrInvalid)
		return
	}
	if !r.requireRead(c, wid) {
		return
	}
	items, err := r.bindings.ListBindings(c.Request.Context(), wid, agentID)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type createBindingBody struct {
	TargetAgentID string `json:"targetAgentId"`
	CallableName  string `json:"callableName"`
	Description   string `json:"description"`
	Mode          string `json:"mode"`
	ContextPolicy string `json:"contextPolicy"`
	Enabled       *bool  `json:"enabled"`
}

func (r *AgentDelegationRoutes) createBinding(c *gin.Context) {
	wid := strings.TrimSpace(c.Param("wid"))
	agentID := strings.TrimSpace(c.Param("id"))
	if !validPathUUID(wid) || !validPathUUID(agentID) {
		RespondError(c, identity.ErrInvalid)
		return
	}
	actor, ok := r.requireWrite(c, wid)
	if !ok {
		return
	}
	var body createBindingBody
	if err := c.ShouldBindJSON(&body); err != nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	value, err := r.bindings.CreateBinding(c.Request.Context(), agentdelegation.CreateBindingInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: wid, CallerAgentID: agentID,
		TargetAgentID: body.TargetAgentID, CallableName: body.CallableName,
		Description: body.Description, Mode: body.Mode, ContextPolicy: body.ContextPolicy,
		Enabled: enabled, ActorID: actor,
	})
	if err != nil {
		RespondError(c, mapDelegationErr(err))
		return
	}
	c.JSON(http.StatusCreated, value)
}

type updateBindingBody struct {
	ExpectedVersion int64   `json:"expectedVersion"`
	TargetAgentID   *string `json:"targetAgentId"`
	CallableName    *string `json:"callableName"`
	Description     *string `json:"description"`
	Mode            *string `json:"mode"`
	ContextPolicy   *string `json:"contextPolicy"`
	Enabled         *bool   `json:"enabled"`
}

func (r *AgentDelegationRoutes) updateBinding(c *gin.Context) {
	wid := strings.TrimSpace(c.Param("wid"))
	bid := strings.TrimSpace(c.Param("bid"))
	if !validPathUUID(wid) || !validPathUUID(bid) {
		RespondError(c, identity.ErrInvalid)
		return
	}
	actor, ok := r.requireWrite(c, wid)
	if !ok {
		return
	}
	var body updateBindingBody
	if err := c.ShouldBindJSON(&body); err != nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	value, err := r.bindings.UpdateBinding(c.Request.Context(), agentdelegation.UpdateBindingInput{
		WorkspaceID: wid, BindingID: bid, ExpectedVersion: body.ExpectedVersion,
		TargetAgentID: body.TargetAgentID, CallableName: body.CallableName, Description: body.Description,
		Mode: body.Mode, ContextPolicy: body.ContextPolicy, Enabled: body.Enabled, ActorID: actor,
	})
	if err != nil {
		RespondError(c, mapDelegationErr(err))
		return
	}
	c.JSON(http.StatusOK, value)
}

func (r *AgentDelegationRoutes) disableBinding(c *gin.Context) {
	wid := strings.TrimSpace(c.Param("wid"))
	bid := strings.TrimSpace(c.Param("bid"))
	if !validPathUUID(wid) || !validPathUUID(bid) {
		RespondError(c, identity.ErrInvalid)
		return
	}
	actor, ok := r.requireWrite(c, wid)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	_ = c.ShouldBindJSON(&body)
	if body.ExpectedVersion < 1 {
		RespondError(c, identity.ErrInvalid)
		return
	}
	if err := r.bindings.SoftDisable(c.Request.Context(), wid, bid, body.ExpectedVersion, actor); err != nil {
		RespondError(c, mapDelegationErr(err))
		return
	}
	c.Status(http.StatusNoContent)
}

// --- A2A exposure ---

func (r *AgentDelegationRoutes) listExposures(c *gin.Context) {
	if r.a2a == nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	wid := strings.TrimSpace(c.Param("wid"))
	if !validPathUUID(wid) || !r.requireRead(c, wid) {
		if !validPathUUID(wid) {
			RespondError(c, identity.ErrInvalid)
		}
		return
	}
	items, err := r.a2a.ListExposures(c.Request.Context(), wid)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (r *AgentDelegationRoutes) createExposure(c *gin.Context) {
	if r.a2a == nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	wid := strings.TrimSpace(c.Param("wid"))
	actor, ok := r.requireWrite(c, wid)
	if !ok {
		return
	}
	var body struct {
		AgentID           string `json:"agentId"`
		PublicName        string `json:"publicName"`
		PublicDescription string `json:"publicDescription"`
		AuthMode          string `json:"authMode"`
		Enabled           *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	authMode := strings.ToUpper(strings.TrimSpace(body.AuthMode))
	if authMode == "" {
		authMode = a2agateway.AuthModeAgentAccess
	}
	if authMode == a2agateway.AuthModeNone && !r.allowAuthNone {
		RespondError(c, identity.ErrInvalid)
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	value, err := r.a2a.CreateExposure(c.Request.Context(), a2agateway.CreateExposureInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: wid, AgentID: body.AgentID,
		PublicName: body.PublicName, PublicDescription: body.PublicDescription,
		AuthMode: authMode, Enabled: enabled, ActorID: actor,
	})
	if err != nil {
		RespondError(c, mapA2AErr(err))
		return
	}
	c.JSON(http.StatusCreated, value)
}

func (r *AgentDelegationRoutes) updateExposure(c *gin.Context) {
	if r.a2a == nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	wid := strings.TrimSpace(c.Param("wid"))
	eid := strings.TrimSpace(c.Param("eid"))
	actor, ok := r.requireWrite(c, wid)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion   int64   `json:"expectedVersion"`
		PublicName        *string `json:"publicName"`
		PublicDescription *string `json:"publicDescription"`
		AuthMode          *string `json:"authMode"`
		Enabled           *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	// Server-side gate: NONE only when allowAuthNone is true.
	// Covers explicit authMode=NONE and re-enabling an existing NONE exposure.
	if body.AuthMode != nil {
		am := strings.ToUpper(strings.TrimSpace(*body.AuthMode))
		if am == a2agateway.AuthModeNone && !r.allowAuthNone {
			RespondError(c, identity.ErrInvalid)
			return
		}
	}
	if body.Enabled != nil && *body.Enabled && !r.allowAuthNone {
		// Re-enable path: load current and reject if still NONE.
		if cur, gerr := r.a2a.GetExposure(c.Request.Context(), wid, eid); gerr == nil {
			nextMode := cur.AuthMode
			if body.AuthMode != nil {
				nextMode = strings.ToUpper(strings.TrimSpace(*body.AuthMode))
			}
			if nextMode == a2agateway.AuthModeNone {
				RespondError(c, identity.ErrInvalid)
				return
			}
		}
	}
	value, err := r.a2a.UpdateExposure(c.Request.Context(), a2agateway.UpdateExposureInput{
		WorkspaceID: wid, ExposureID: eid, ExpectedVersion: body.ExpectedVersion,
		PublicName: body.PublicName, PublicDescription: body.PublicDescription,
		AuthMode: body.AuthMode, Enabled: body.Enabled, ActorID: actor,
	})
	if err != nil {
		RespondError(c, mapA2AErr(err))
		return
	}
	c.JSON(http.StatusOK, value)
}

func (r *AgentDelegationRoutes) disableExposure(c *gin.Context) {
	if r.a2a == nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	wid := strings.TrimSpace(c.Param("wid"))
	eid := strings.TrimSpace(c.Param("eid"))
	actor, ok := r.requireWrite(c, wid)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := r.a2a.SoftDisableExposure(c.Request.Context(), wid, eid, body.ExpectedVersion, actor); err != nil {
		RespondError(c, mapA2AErr(err))
		return
	}
	c.Status(http.StatusNoContent)
}

func (r *AgentDelegationRoutes) previewCard(c *gin.Context) {
	if r.a2a == nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	wid := strings.TrimSpace(c.Param("wid"))
	eid := strings.TrimSpace(c.Param("eid"))
	if !r.requireRead(c, wid) {
		return
	}
	exp, err := r.a2a.GetExposure(c.Request.Context(), wid, eid)
	if err != nil {
		RespondError(c, mapA2AErr(err))
		return
	}
	c.JSON(http.StatusOK, a2agateway.BuildAgentCardForExposure(r.baseURL, exp))
}

// --- A2A remotes ---

func (r *AgentDelegationRoutes) listRemotes(c *gin.Context) {
	if r.a2a == nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	wid := strings.TrimSpace(c.Param("wid"))
	agentID := strings.TrimSpace(c.Param("id"))
	if !r.requireRead(c, wid) {
		return
	}
	items, err := r.a2a.ListRemotes(c.Request.Context(), wid, agentID)
	if err != nil {
		RespondError(c, err)
		return
	}
	// Never return auth secret material — AuthSecretRef is a reference only (OK).
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (r *AgentDelegationRoutes) createRemote(c *gin.Context) {
	if r.a2a == nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	wid := strings.TrimSpace(c.Param("wid"))
	agentID := strings.TrimSpace(c.Param("id"))
	actor, ok := r.requireWrite(c, wid)
	if !ok {
		return
	}
	var body struct {
		CallableName  string   `json:"callableName"`
		Description   string   `json:"description"`
		EndpointURL   string   `json:"endpointUrl"`
		AgentCardURL  string   `json:"agentCardUrl"`
		AllowedHosts  []string `json:"allowedHosts"`
		AuthSecretRef string   `json:"authSecretRef"`
		TimeoutMs     int      `json:"timeoutMs"`
		Enabled       *bool    `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	value, err := r.a2a.CreateRemote(c.Request.Context(), a2agateway.CreateRemoteInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: wid, CallerAgentID: agentID,
		CallableName: body.CallableName, Description: body.Description,
		EndpointURL: body.EndpointURL, AgentCardURL: body.AgentCardURL,
		AllowedHosts: body.AllowedHosts, AuthSecretRef: body.AuthSecretRef,
		TimeoutMs: body.TimeoutMs, Enabled: enabled, ActorID: actor,
	})
	if err != nil {
		RespondError(c, mapA2AErr(err))
		return
	}
	c.JSON(http.StatusCreated, value)
}

func (r *AgentDelegationRoutes) updateRemote(c *gin.Context) {
	if r.a2a == nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	wid := strings.TrimSpace(c.Param("wid"))
	rid := strings.TrimSpace(c.Param("rid"))
	actor, ok := r.requireWrite(c, wid)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64    `json:"expectedVersion"`
		CallableName    *string  `json:"callableName"`
		Description     *string  `json:"description"`
		EndpointURL     *string  `json:"endpointUrl"`
		AgentCardURL    *string  `json:"agentCardUrl"`
		AllowedHosts    []string `json:"allowedHosts"`
		AuthSecretRef   *string  `json:"authSecretRef"`
		TimeoutMs       *int     `json:"timeoutMs"`
		Enabled         *bool    `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	value, err := r.a2a.UpdateRemote(c.Request.Context(), a2agateway.UpdateRemoteInput{
		WorkspaceID: wid, BindingID: rid, ExpectedVersion: body.ExpectedVersion,
		CallableName: body.CallableName, Description: body.Description,
		EndpointURL: body.EndpointURL, AgentCardURL: body.AgentCardURL,
		AllowedHosts: body.AllowedHosts, AuthSecretRef: body.AuthSecretRef,
		TimeoutMs: body.TimeoutMs, Enabled: body.Enabled, ActorID: actor,
	})
	if err != nil {
		RespondError(c, mapA2AErr(err))
		return
	}
	c.JSON(http.StatusOK, value)
}

func (r *AgentDelegationRoutes) disableRemote(c *gin.Context) {
	if r.a2a == nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	wid := strings.TrimSpace(c.Param("wid"))
	rid := strings.TrimSpace(c.Param("rid"))
	actor, ok := r.requireWrite(c, wid)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := r.a2a.SoftDisableRemote(c.Request.Context(), wid, rid, body.ExpectedVersion, actor); err != nil {
		RespondError(c, mapA2AErr(err))
		return
	}
	c.Status(http.StatusNoContent)
}

func mapDelegationErr(err error) error {
	switch {
	case errors.Is(err, agentdelegation.ErrInvalid),
		errors.Is(err, agentdelegation.ErrSelfLoop),
		errors.Is(err, agentdelegation.ErrCycle),
		errors.Is(err, agentdelegation.ErrDuplicateAlias),
		errors.Is(err, agentdelegation.ErrNamespaceConflict),
		errors.Is(err, agentdelegation.ErrDuplicateTarget),
		errors.Is(err, agentdelegation.ErrAgentUnavailable):
		return identity.ErrInvalid
	case errors.Is(err, agentdelegation.ErrNotFound):
		return identity.ErrNotFound
	case errors.Is(err, agentdelegation.ErrConflict):
		return identity.ErrConflict
	default:
		return err
	}
}

func mapA2AErr(err error) error {
	switch {
	case errors.Is(err, a2agateway.ErrInvalid),
		errors.Is(err, a2agateway.ErrSSRFDenied),
		errors.Is(err, a2agateway.ErrNotAllowlisted):
		return identity.ErrInvalid
	case errors.Is(err, a2agateway.ErrNotFound):
		return identity.ErrNotFound
	case errors.Is(err, a2agateway.ErrConflict),
		errors.Is(err, a2agateway.ErrNamespaceConflict):
		return identity.ErrConflict
	default:
		return err
	}
}
