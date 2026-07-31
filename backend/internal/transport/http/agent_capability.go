package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/capability"

	"github.com/gin-gonic/gin"
)

type AgentStore interface {
	Create(context.Context, agent.NewAgent) (agent.Agent, agent.PromptRevision, error)
	GetSummary(context.Context, string, string) (agent.Summary, error)
	ListSummaries(context.Context, string) ([]agent.Summary, error)
	Update(context.Context, string, string, agent.UpdateAgent) (agent.Agent, error)
	SoftDelete(context.Context, string, string, string, int64) error
}

type AgentPromptService interface {
	Run(context.Context, string, string, string, string, string, string) (agent.PromptRun, string, error)
	Accept(context.Context, string, string, string, int64) (agent.PromptRun, agent.PromptRevision, error)
	RunCreatePreview(context.Context, string, string, string, string, string) (agent.PromptRun, string, error)
}

type AgentCurrentPromptReader interface {
	// GetCurrent(workspaceID, agentID, actorID, actorDisplay)
	GetCurrent(context.Context, string, string, string, string) (agent.CurrentPrompt, error)
}

type AgentCreationService interface {
	Create(context.Context, agent.NewAgent, string) (agent.CreateAgentResult, error)
}

type CapabilityStore interface {
	ListCatalog(context.Context, string) ([]capability.CatalogItem, error)
	ListBindings(context.Context, string, string) ([]capability.Binding, error)
}

type AgentCapabilityCatalog interface {
	ListForAgent(context.Context, string, string) ([]capability.Descriptor, error)
}

type BindingWriter interface {
	Bind(context.Context, capability.BindInput) (capability.Binding, error)
	Unbind(context.Context, string, string, string, int64) error
}

type AgentCapabilityRoutes struct {
	authorizer     WorkspaceAuthorizer
	agents         AgentStore
	prompts        AgentPromptService
	currentPrompts AgentCurrentPromptReader
	creation       AgentCreationService
	capabilities   CapabilityStore
	catalog        AgentCapabilityCatalog
	bindings       BindingWriter
}

func NewAgentCapabilityRoutes(authorizer WorkspaceAuthorizer, agents AgentStore, prompts AgentPromptService,
	capabilities CapabilityStore, catalog AgentCapabilityCatalog, bindings BindingWriter,
) (*AgentCapabilityRoutes, error) {
	if authorizer == nil || agents == nil || prompts == nil || capabilities == nil || catalog == nil || bindings == nil {
		return nil, errors.New("agent capability route dependencies are required")
	}
	return &AgentCapabilityRoutes{authorizer: authorizer, agents: agents, prompts: prompts,
		capabilities: capabilities, catalog: catalog, bindings: bindings}, nil
}

// WithCurrentPromptReader installs the on-demand current prompt query (ZKL-69).
func (r *AgentCapabilityRoutes) WithCurrentPromptReader(reader AgentCurrentPromptReader) *AgentCapabilityRoutes {
	if r != nil {
		r.currentPrompts = reader
	}
	return r
}

// WithCreationService installs optional create-preview source linking (ZKL-69).
func (r *AgentCapabilityRoutes) WithCreationService(creation AgentCreationService) *AgentCapabilityRoutes {
	if r != nil {
		r.creation = creation
	}
	return r
}

func (r *AgentCapabilityRoutes) RegisterV1(v1 V1Routes) {
	g := v1.Protected
	g.GET("/workspaces/:wid/agents", r.listAgents)
	g.POST("/workspaces/:wid/agents", r.createAgent)
	g.GET("/workspaces/:wid/agents/:id", r.getAgent)
	g.PATCH("/workspaces/:wid/agents/:id", r.updateAgent)
	g.DELETE("/workspaces/:wid/agents/:id", r.deleteAgent)
	g.POST("/workspaces/:wid/agents/:id/__command/enhance-prompt", r.enhancePrompt)
	g.POST("/workspaces/:wid/agents/__command/preview-prompt-enhancement", r.previewCreatePromptEnhancement)
	g.GET("/workspaces/:wid/agents/:id/prompt-revisions/current", r.getCurrentPrompt)
	g.GET("/workspaces/:wid/agents/:id/capabilities", r.listAgentCapabilities)
	g.PUT("/workspaces/:wid/agents/:id/capabilities/:capabilityId", r.bindCapability)
	g.DELETE("/workspaces/:wid/agents/:id/capabilities/:capabilityId", r.unbindCapability)
	g.GET("/workspaces/:wid/capabilities", r.listCapabilities)
}

func (r *AgentCapabilityRoutes) authorize(c *gin.Context, action authz.Action) bool {
	principal, _ := PrincipalFrom(c.Request.Context())
	if _, err := r.authorizer.AuthorizeWorkspace(c.Request.Context(), principal.UserID, c.Param("wid"), action); err != nil {
		RespondError(c, err)
		return false
	}
	return true
}

type agentDTO struct {
	ID                      string          `json:"id"`
	Name                    string          `json:"name"`
	RoleDescription         string          `json:"roleDescription"`
	CurrentPromptRevisionID *string         `json:"currentPromptRevisionId,omitempty"`
	ModelConfigID           string          `json:"modelConfigId"`
	IsDefault               bool            `json:"isDefault"`
	Status                  agent.Status    `json:"status"`
	ContextPolicy           json.RawMessage `json:"contextPolicy"`
	ToolsCount              int             `json:"toolsCount"`
	WorkflowsCount          int             `json:"workflowsCount"`
	CreatedBy               string          `json:"createdBy"`
	UpdatedBy               string          `json:"updatedBy"`
	CreatedAt               time.Time       `json:"createdAt"`
	UpdatedAt               time.Time       `json:"updatedAt"`
	LockVersion             int64           `json:"lockVersion"`
}

func agentSummaryDTO(v agent.Summary) agentDTO {
	policy := v.ContextPolicy
	if len(policy) == 0 {
		policy = json.RawMessage(`{}`)
	}
	return agentDTO{v.ID, v.Name, v.RoleDescription, v.CurrentPromptRevisionID, v.ModelConfigID,
		v.IsDefault, v.Status, policy, v.ToolsCount, v.WorkflowsCount, v.CreatedBy, v.UpdatedBy,
		v.CreatedAt, v.UpdatedAt, v.LockVersion}
}

func agentValueDTO(v agent.Agent) agentDTO { return agentSummaryDTO(agent.Summary{Agent: v}) }

type createAgentRequest struct {
	Name                     string `json:"name"`
	RoleDescription          string `json:"roleDescription"`
	ModelConfigID            string `json:"modelConfigId"`
	IsDefault                bool   `json:"isDefault"`
	SystemPrompt             string `json:"systemPrompt"`
	SourcePromptPreviewRunID string `json:"sourcePromptPreviewRunId,omitempty"`
}

type createAgentResponse struct {
	agentDTO
	InitialPromptRevision *initialPromptRevisionDTO `json:"initialPromptRevision,omitempty"`
	SourcePromptPreview   *sourcePromptPreviewDTO   `json:"sourcePromptPreview,omitempty"`
}

type initialPromptRevisionDTO struct {
	ID         string `json:"id"`
	RevisionNo int    `json:"revisionNo"`
	Source     string `json:"source"`
}

type sourcePromptPreviewDTO struct {
	RunID  string `json:"runId"`
	Linked bool   `json:"linked"`
	Reason string `json:"reason,omitempty"`
}

func (r *AgentCapabilityRoutes) listAgents(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	values, err := r.agents.ListSummaries(c.Request.Context(), c.Param("wid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]agentDTO, len(values))
	for i := range values {
		items[i] = agentSummaryDTO(values[i])
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (r *AgentCapabilityRoutes) createAgent(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var request createAgentRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, agent.ErrInvalid)
		return
	}
	agentID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	revisionID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	input := agent.NewAgent{ID: agentID,
		WorkspaceID: c.Param("wid"), Name: request.Name, RoleDescription: request.RoleDescription,
		ModelConfigID: request.ModelConfigID, IsDefault: request.IsDefault,
		InitialRevisionID: revisionID, InitialPrompt: request.SystemPrompt,
		PromptSource: "MANUAL", CreatedBy: actor(c)}
	sourceRunID := strings.TrimSpace(request.SourcePromptPreviewRunID)
	if r.creation != nil && sourceRunID != "" {
		result, createErr := r.creation.Create(c.Request.Context(), input, sourceRunID)
		if createErr != nil {
			RespondError(c, createErr)
			return
		}
		response := createAgentResponse{agentDTO: agentValueDTO(result.Agent)}
		response.InitialPromptRevision = &initialPromptRevisionDTO{
			ID: result.Revision.ID, RevisionNo: result.Revision.RevisionNo, Source: result.Revision.Source,
		}
		response.SourcePromptPreview = &sourcePromptPreviewDTO{
			RunID: sourceRunID, Linked: result.SourceLinked, Reason: result.SourceReason,
		}
		c.JSON(http.StatusCreated, response)
		return
	}
	value, _, err := r.agents.Create(c.Request.Context(), input)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, agentValueDTO(value))
}

func (r *AgentCapabilityRoutes) getCurrentPrompt(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	if r.currentPrompts == nil {
		RespondError(c, errors.New("current prompt reader is not configured"))
		return
	}
	current, err := r.currentPrompts.GetCurrent(
		c.Request.Context(), c.Param("wid"), c.Param("id"), actor(c), actorDisplayName(c),
	)
	if err != nil {
		RespondError(c, err)
		return
	}
	setNoStoreHeaders(c)
	c.JSON(http.StatusOK, gin.H{
		"agentId":      current.AgentID,
		"revisionId":   current.RevisionID,
		"revisionNo":   current.RevisionNo,
		"systemPrompt": current.SystemPrompt,
		"source":       current.Source,
		"createdBy":    current.CreatedBy,
		"createdAt":    current.CreatedAt,
	})
}

type previewCreatePromptRequest struct {
	ModelConfigID string `json:"modelConfigId"`
	Input         string `json:"input"`
}

func (r *AgentCapabilityRoutes) previewCreatePromptEnhancement(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var request previewCreatePromptRequest
	if decodeJSON(c, &request) != nil || strings.TrimSpace(request.ModelConfigID) == "" ||
		strings.TrimSpace(request.Input) == "" {
		RespondError(c, agent.ErrInvalid)
		return
	}
	requestContext, _ := RequestContextFrom(c.Request.Context())
	workspaceID := c.Param("wid")
	modelConfigID := request.ModelConfigID
	input := request.Input
	traceID := requestContext.TraceID
	createdBy := actor(c)
	request.Input = ""

	// Minimal SSE path: keep the connection warm with heartbeats while the
	// model runs on a context that is not cancelled by client/gateway disconnect.
	if wantsConsoleLLMSSE(c) {
		type previewOutcome struct {
			run    agent.PromptRun
			output string
		}
		streamConsoleLLMJob(c,
			func(ctx context.Context) (previewOutcome, error) {
				run, output, err := r.prompts.RunCreatePreview(ctx, workspaceID, modelConfigID, input, traceID, createdBy)
				return previewOutcome{run: run, output: output}, err
			},
			func(stream *consoleSSEWriter) error {
				return stream.Event("started", gin.H{"status": "RUNNING", "preview": true})
			},
			func(stream *consoleSSEWriter, value previewOutcome, err error) error {
				if err != nil {
					return stream.Event("failed", consoleMappedErrorBody(c, err))
				}
				payload := createPreviewResponseDTO(value.run, value.output)
				return stream.Event("completed", payload)
			},
		)
		return
	}

	run, output, err := r.prompts.RunCreatePreview(c.Request.Context(), workspaceID,
		modelConfigID, input, traceID, createdBy)
	if err != nil {
		RespondError(c, err)
		return
	}
	setNoStoreHeaders(c)
	c.JSON(http.StatusOK, createPreviewResponseDTO(run, output))
}

func createPreviewResponseDTO(run agent.PromptRun, output string) gin.H {
	response := gin.H{
		"runId": run.ID, "status": run.Status, "preview": true, "output": output,
		"createdAt": run.CreatedAt,
	}
	if run.ExpiresAt != nil {
		response["expiresAt"] = *run.ExpiresAt
	}
	return response
}

func setNoStoreHeaders(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Pragma", "no-cache")
	c.Header("Vary", "Authorization")
}

func (r *AgentCapabilityRoutes) getAgent(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	value, err := r.agents.GetSummary(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, agentSummaryDTO(value))
}

type updateAgentRequest struct {
	Name            *string         `json:"name"`
	RoleDescription *string         `json:"roleDescription"`
	ModelConfigID   *string         `json:"modelConfigId"`
	Status          *agent.Status   `json:"status"`
	ContextPolicy   json.RawMessage `json:"contextPolicy"`
	LockVersion     int64           `json:"lockVersion"`
}

func (r *AgentCapabilityRoutes) updateAgent(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var request updateAgentRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, agent.ErrInvalid)
		return
	}
	current, err := r.agents.GetSummary(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if err != nil {
		RespondError(c, err)
		return
	}
	if request.Name != nil {
		current.Name = *request.Name
	}
	if request.RoleDescription != nil {
		current.RoleDescription = *request.RoleDescription
	}
	if request.ModelConfigID != nil {
		current.ModelConfigID = *request.ModelConfigID
	}
	if request.Status != nil {
		if *request.Status != agent.StatusActive && *request.Status != agent.StatusDisabled {
			RespondError(c, agent.ErrInvalid)
			return
		}
		current.Status = *request.Status
	}
	value, err := r.agents.Update(c.Request.Context(), c.Param("wid"), c.Param("id"), agent.UpdateAgent{
		Name: current.Name, RoleDescription: current.RoleDescription, ModelConfigID: current.ModelConfigID,
		Status: current.Status, ContextPolicy: request.ContextPolicy,
		ContextPolicySet: request.ContextPolicy != nil,
		UpdatedBy: actor(c), ExpectedLockVersion: request.LockVersion})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, agentValueDTO(value))
}

func (r *AgentCapabilityRoutes) deleteAgent(c *gin.Context) {
	if !r.authorize(c, authz.ActionDelete) {
		return
	}
	lockVersion, err := strconv.ParseInt(c.Query("lockVersion"), 10, 64)
	if err == nil && lockVersion > 0 {
		err = r.agents.SoftDelete(c.Request.Context(), c.Param("wid"), c.Param("id"), actor(c), lockVersion)
	} else {
		err = agent.ErrInvalid
	}
	if err != nil {
		RespondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type enhancePromptRequest struct {
	Input       string `json:"input"`
	Preview     bool   `json:"preview"`
	LockVersion int64  `json:"lockVersion,omitempty"`
}

func (r *AgentCapabilityRoutes) enhancePrompt(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var request enhancePromptRequest
	if decodeJSON(c, &request) != nil || strings.TrimSpace(request.Input) == "" || (!request.Preview && request.LockVersion < 1) {
		RespondError(c, agent.ErrInvalid)
		return
	}
	operation := "ENHANCE"
	if request.Preview {
		operation = "PREVIEW"
	}
	requestContext, _ := RequestContextFrom(c.Request.Context())
	run, output, err := r.prompts.Run(c.Request.Context(), c.Param("wid"), c.Param("id"), operation,
		request.Input, requestContext.TraceID, actor(c))
	request.Input = ""
	if err != nil {
		RespondError(c, err)
		return
	}
	response := gin.H{"runId": run.ID, "status": run.Status, "preview": request.Preview, "output": output,
		"inputObjectId": run.InputObjectID, "outputObjectId": run.OutputObjectID}
	if !request.Preview {
		accepted, revision, err := r.prompts.Accept(c.Request.Context(), c.Param("wid"), run.ID, actor(c), request.LockVersion)
		if err != nil {
			RespondError(c, err)
			return
		}
		response["acceptedRevisionId"] = accepted.AcceptedRevisionID
		response["revisionNo"] = revision.RevisionNo
	}
	c.JSON(http.StatusOK, response)
}

type capabilityDTO struct {
	ID              string                   `json:"id"`
	Kind            string                   `json:"kind"`
	Name            string                   `json:"name"`
	Slug            string                   `json:"slug"`
	Description     string                   `json:"description"`
	Status          string                   `json:"status"`
	ActiveReleaseID *string                  `json:"activeReleaseId,omitempty"`
	BoundAgentCount int                      `json:"boundAgentCount"`
	ActiveRelease   *capabilityDescriptorDTO `json:"activeRelease,omitempty"`
	CreatedBy       string                   `json:"createdBy"`
	UpdatedBy       string                   `json:"updatedBy"`
	LockVersion     int64                    `json:"lockVersion"`
}

func capabilityDTOFor(v capability.CatalogItem) capabilityDTO {
	return capabilityDTO{v.ID, v.Kind, v.Name, v.Slug, v.Description, v.Status, v.ActiveReleaseID, v.BoundAgentCount, capabilityDescriptorDTOFor(v.ActiveRelease), v.CreatedBy, v.UpdatedBy, v.LockVersion}
}

type capabilityDescriptorDTO struct {
	CapabilityID         string          `json:"capabilityId"`
	ReleaseID            string          `json:"releaseId"`
	Kind                 string          `json:"kind"`
	CallableName         string          `json:"callableName"`
	CallableDescription  string          `json:"callableDescription"`
	InputSchema          json.RawMessage `json:"inputSchema"`
	OutputSchema         json.RawMessage `json:"outputSchema"`
	RiskLevel            string          `json:"riskLevel"`
	SideEffectLevel      string          `json:"sideEffectLevel"`
	RequiresConfirmation bool            `json:"requiresConfirmation"`
}

func capabilityDescriptorDTOFor(value *capability.Descriptor) *capabilityDescriptorDTO {
	if value == nil {
		return nil
	}
	return &capabilityDescriptorDTO{value.CapabilityID, value.ReleaseID, value.Kind, value.CallableName,
		value.CallableDescription, value.InputSchema, value.OutputSchema, value.RiskLevel,
		value.SideEffectLevel, value.RequiresConfirmation}
}

func (r *AgentCapabilityRoutes) listCapabilities(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	values, err := r.capabilities.ListCatalog(c.Request.Context(), c.Param("wid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]capabilityDTO, len(values))
	for i := range values {
		items[i] = capabilityDTOFor(values[i])
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type bindingDTO struct {
	CapabilityID      string                   `json:"capabilityId"`
	VersionPolicy     string                   `json:"versionPolicy"`
	PinnedReleaseID   *string                  `json:"pinnedReleaseId,omitempty"`
	ConnectionID      *string                  `json:"connectionId,omitempty"`
	ExecutionPolicyID *string                  `json:"executionPolicyId,omitempty"`
	Enabled           bool                     `json:"enabled"`
	ConfigOverrides   json.RawMessage          `json:"configOverrides"`
	ResolvedRelease   *capabilityDescriptorDTO `json:"resolvedRelease,omitempty"`
	LockVersion       int64                    `json:"lockVersion"`
}

func (r *AgentCapabilityRoutes) listAgentCapabilities(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	if _, err := r.agents.GetSummary(c.Request.Context(), c.Param("wid"), c.Param("id")); err != nil {
		RespondError(c, err)
		return
	}
	bindings, err := r.capabilities.ListBindings(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if err != nil {
		RespondError(c, err)
		return
	}
	descriptors, err := r.catalog.ListForAgent(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if err != nil {
		RespondError(c, err)
		return
	}
	resolved := make(map[string]capability.Descriptor, len(descriptors))
	for _, value := range descriptors {
		resolved[value.CapabilityID] = value
	}
	items := make([]bindingDTO, len(bindings))
	for i, value := range bindings {
		var descriptor *capabilityDescriptorDTO
		if candidate, ok := resolved[value.CapabilityID]; ok {
			descriptor = capabilityDescriptorDTOFor(&candidate)
		}
		items[i] = bindingDTO{value.CapabilityID, value.VersionPolicy, value.PinnedReleaseID, value.ConnectionID, value.ExecutionPolicyID, value.Enabled, value.ConfigOverrides, descriptor, value.LockVersion}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type bindCapabilityRequest struct {
	VersionPolicy     string          `json:"versionPolicy"`
	PinnedReleaseID   *string         `json:"pinnedReleaseId"`
	ConnectionID      *string         `json:"connectionId"`
	ExecutionPolicyID *string         `json:"executionPolicyId"`
	Enabled           bool            `json:"enabled"`
	ConfigOverrides   json.RawMessage `json:"configOverrides"`
	LockVersion       int64           `json:"lockVersion"`
}

func (r *AgentCapabilityRoutes) bindCapability(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var request bindCapabilityRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, capability.ErrInvalid)
		return
	}
	value, err := r.bindings.Bind(c.Request.Context(), capability.BindInput{WorkspaceID: c.Param("wid"), AgentID: c.Param("id"), CapabilityID: c.Param("capabilityId"), VersionPolicy: request.VersionPolicy, PinnedReleaseID: request.PinnedReleaseID, ConnectionID: request.ConnectionID, ExecutionPolicyID: request.ExecutionPolicyID, Enabled: request.Enabled, ConfigOverrides: request.ConfigOverrides, BoundBy: actor(c), ExpectedLockVersion: request.LockVersion})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, bindingDTO{CapabilityID: value.CapabilityID, VersionPolicy: value.VersionPolicy, PinnedReleaseID: value.PinnedReleaseID, ConnectionID: value.ConnectionID, ExecutionPolicyID: value.ExecutionPolicyID, Enabled: value.Enabled, ConfigOverrides: value.ConfigOverrides, LockVersion: value.LockVersion})
}

func (r *AgentCapabilityRoutes) unbindCapability(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	lockVersion, err := strconv.ParseInt(c.Query("lockVersion"), 10, 64)
	if err == nil && lockVersion > 0 {
		err = r.bindings.Unbind(c.Request.Context(), c.Param("wid"), c.Param("id"), c.Param("capabilityId"), lockVersion)
	} else {
		err = capability.ErrInvalid
	}
	if err != nil {
		RespondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
