package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/metrics"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/smartdag"
	"actweave/backend/internal/workflow"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type WorkflowStore interface {
	List(context.Context, string) ([]workflow.Workflow, error)
	Create(context.Context, workflow.CreateInput) (workflow.Workflow, workflow.Draft, error)
	Get(context.Context, string, string) (workflow.Workflow, error)
	UpdateMetadata(context.Context, string, string, workflow.MetadataUpdate) (workflow.Workflow, error)
	SoftDelete(context.Context, string, string, string, int64) error
	GetDraft(context.Context, string, string) (workflow.Draft, error)
	UpdateDraft(context.Context, string, string, workflow.DraftUpdate) (workflow.Draft, error)
	ListRevisions(context.Context, string, string) ([]workflow.Revision, error)
	DiffRevisions(context.Context, string, string, string, string) (workflow.RevisionDiff, error)
}

type WorkflowCompiler interface {
	Compile(context.Context, string, string, string) (workflow.Compilation, error)
}

type WorkflowTrialer interface {
	Run(context.Context, string, string, string, string, json.RawMessage) (workflow.TrialRun, error)
}

// WorkflowTrialerWithOutbound optionally accepts write-only credentials for trial.
type WorkflowTrialerWithOutbound interface {
	WorkflowTrialer
	RunWithOutbound(
		ctx context.Context,
		workspaceID, capabilityID, compilationID, startedBy string,
		input json.RawMessage,
		opts workflow.TrialOutboundOptions,
	) (workflow.TrialRun, error)
}

type WorkflowPublisher interface {
	Publish(context.Context, workflow.PublishWorkflowInput) (workflow.PublishWorkflowResult, error)
}

// WorkflowForcePublisher optionally skips trial (PLATFORM_ADMIN + config gate).
type WorkflowForcePublisher interface {
	ForcePublish(context.Context, workflow.ForcePublishWorkflowInput) (workflow.PublishWorkflowResult, error)
}

type WorkflowActivator interface {
	Activate(context.Context, workflow.ActivateRevisionInput) (workflow.ActivateRevisionResult, error)
}

type WorkflowReadiness interface {
	Get(context.Context, string, string) (workflow.Readiness, error)
}

type WorkflowGenerator interface {
	Generate(context.Context, smartdag.GenerateRequest) (smartdag.GenerateResult, error)
}

// WorkflowProductionExecutor starts production runs against an active published revision (WP2 / D4).
type WorkflowProductionExecutor interface {
	Execute(context.Context, workflow.ProductionExecuteInput) (workflow.ProductionExecuteResult, error)
}

// WorkflowExecutionConfirmationDecider resolves Console production Approval HITL.
type WorkflowExecutionConfirmationDecider interface {
	DecideStored(
		ctx context.Context,
		workspaceID, confirmationID, actorID, decision, idempotencyKey string,
		expectedLockVersion int64,
	) (execution.InteractionDecisionResult, error)
}

type WorkflowRoutes struct {
	authorizer    WorkspaceAuthorizer
	store         WorkflowStore
	compiler      WorkflowCompiler
	trials        WorkflowTrialer
	publisher     WorkflowPublisher
	activator     WorkflowActivator
	readiness     WorkflowReadiness
	generator     WorkflowGenerator
	production    WorkflowProductionExecutor
	confirmations WorkflowExecutionConfirmationDecider
}

type WorkflowDependencies struct {
	Authorizer WorkspaceAuthorizer
	Store      WorkflowStore
	Compiler   WorkflowCompiler
	Trials     WorkflowTrialer
	Publisher  WorkflowPublisher
	Activator  WorkflowActivator
	Readiness  WorkflowReadiness
	Generator  WorkflowGenerator
	// Production is optional only for older unit fixtures; production binary wires it.
	Production WorkflowProductionExecutor
	// Confirmations is optional; wired after InteractionDecisionService is ready.
	Confirmations WorkflowExecutionConfirmationDecider
}

func NewWorkflowRoutes(dependencies WorkflowDependencies) (*WorkflowRoutes, error) {
	if dependencies.Authorizer == nil || dependencies.Store == nil || dependencies.Compiler == nil ||
		dependencies.Trials == nil || dependencies.Publisher == nil || dependencies.Activator == nil ||
		dependencies.Readiness == nil || dependencies.Generator == nil {
		return nil, errors.New("workflow route dependencies are required")
	}
	return &WorkflowRoutes{
		authorizer:    dependencies.Authorizer,
		store:         dependencies.Store,
		compiler:      dependencies.Compiler,
		trials:        dependencies.Trials,
		publisher:     dependencies.Publisher,
		activator:     dependencies.Activator,
		readiness:     dependencies.Readiness,
		generator:     dependencies.Generator,
		production:    dependencies.Production,
		confirmations: dependencies.Confirmations,
	}, nil
}

// ConfigureExecutionConfirmations enables production Approval confirm/cancel routes.
func (r *WorkflowRoutes) ConfigureExecutionConfirmations(decider WorkflowExecutionConfirmationDecider) error {
	if r == nil {
		return errors.New("workflow routes are required")
	}
	r.confirmations = decider
	return nil
}

func (r *WorkflowRoutes) RegisterV1(v1 V1Routes) {
	group := v1.Protected
	group.GET("/workspaces/:wid/workflows", r.listWorkflows)
	group.POST("/workspaces/:wid/workflows", r.createWorkflow)
	group.POST("/workspaces/:wid/workflows/__command/generate", r.generateWorkflow)
	group.GET("/workspaces/:wid/workflows/:id", r.getWorkflow)
	group.PATCH("/workspaces/:wid/workflows/:id", r.updateWorkflow)
	group.DELETE("/workspaces/:wid/workflows/:id", r.deleteWorkflow)
	group.GET("/workspaces/:wid/workflows/:id/draft", r.getDraft)
	group.PUT("/workspaces/:wid/workflows/:id/draft", r.updateDraft)
	group.POST("/workspaces/:wid/workflows/:id/draft/__command/compile", r.compileDraft)
	group.POST("/workspaces/:wid/workflows/:id/compilations/:cid/__command/trial", r.trialCompilation)
	group.POST("/workspaces/:wid/workflows/:id/compilations/:cid/__command/publish", r.publishCompilation)
	group.POST("/workspaces/:wid/workflows/:id/compilations/:cid/__command/force-publish", r.forcePublishCompilation)
	group.GET("/workspaces/:wid/workflows/:id/revisions", r.listRevisions)
	group.GET("/workspaces/:wid/workflows/:id/revisions/__command/diff", r.diffRevisions)
	group.POST("/workspaces/:wid/workflows/:id/revisions/:rid/__command/activate", r.activateRevision)
	group.POST("/workspaces/:wid/workflows/:id/revisions/:rid/__command/execute", r.executeRevision)
	group.GET("/workspaces/:wid/workflows/:id/readiness", r.getReadiness)
	// Production Approval HITL (WorkflowExecution-only pauses).
	group.POST("/workspaces/:wid/execution-confirmations/:cid/__command/confirm", r.confirmExecutionConfirmation)
	group.POST("/workspaces/:wid/execution-confirmations/:cid/__command/cancel", r.cancelExecutionConfirmation)
}

func (r *WorkflowRoutes) authorize(c *gin.Context, action authz.Action) bool {
	principal, _ := PrincipalFrom(c.Request.Context())
	if _, err := r.authorizer.AuthorizeWorkspace(
		c.Request.Context(), principal.UserID, c.Param("wid"), action,
	); err != nil {
		RespondError(c, err)
		return false
	}
	return true
}

type workflowDTO struct {
	ID                  string    `json:"id"`
	CurrentDraftID      string    `json:"currentDraftId"`
	ActiveRevisionID    *string   `json:"activeRevisionId,omitempty"`
	LatestCompilationID *string   `json:"latestCompilationId,omitempty"`
	Name                string    `json:"name"`
	Slug                string    `json:"slug"`
	Description         string    `json:"description"`
	Status              string    `json:"status"`
	CreatedBy           string    `json:"createdBy"`
	UpdatedBy           string    `json:"updatedBy"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
	LockVersion         int64     `json:"lockVersion"`
	NodeCount           int       `json:"nodeCount"`
	EdgeCount           int       `json:"edgeCount"`
}

func workflowDTOFor(value workflow.Workflow) workflowDTO {
	return workflowDTO{
		ID: value.CapabilityID, CurrentDraftID: value.CurrentDraftID,
		ActiveRevisionID: value.ActiveRevisionID, LatestCompilationID: value.LatestCompilationID,
		Name: value.Name, Slug: value.Slug, Description: value.Description, Status: value.Status,
		CreatedBy: value.CreatedBy, UpdatedBy: value.UpdatedBy,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, LockVersion: value.LockVersion,
		NodeCount: value.NodeCount, EdgeCount: value.EdgeCount,
	}
}

type draftDTO struct {
	ID            string          `json:"id"`
	DraftVersion  int64           `json:"draftVersion"`
	SchemaVersion string          `json:"schemaVersion"`
	Graph         json.RawMessage `json:"graph"`
	GraphHash     string          `json:"graphHash"`
	UpdatedBy     string          `json:"updatedBy"`
	UpdatedAt     time.Time       `json:"updatedAt"`
	LockVersion   int64           `json:"lockVersion"`
}

func draftDTOFor(value workflow.Draft) draftDTO {
	return draftDTO{value.ID, value.DraftVersion, value.SchemaVersion, value.Graph,
		value.GraphHash, value.UpdatedBy, value.UpdatedAt, value.LockVersion}
}

type compilationDTO struct {
	ID              string          `json:"id"`
	DraftID         string          `json:"draftId"`
	DraftVersion    int64           `json:"draftVersion"`
	GraphHash       string          `json:"graphHash"`
	CompilerVersion string          `json:"compilerVersion"`
	Status          string          `json:"status"`
	Spec            json.RawMessage `json:"spec"`
	Plan            json.RawMessage `json:"plan"`
	Issues          json.RawMessage `json:"issues"`
	PlanHash        string          `json:"planHash"`
	CompiledBy      string          `json:"compiledBy"`
	CompiledAt      time.Time       `json:"compiledAt"`
}

func compilationDTOFor(value workflow.Compilation) compilationDTO {
	return compilationDTO{value.ID, value.DraftID, value.DraftVersion, value.GraphHash,
		value.CompilerVersion, value.Status, value.Spec, value.Plan, value.Issues,
		value.PlanHash, value.CompiledBy, value.CompiledAt}
}

type trialDTO struct {
	ID            string     `json:"id"`
	CompilationID string     `json:"compilationId"`
	ExecutionID   string     `json:"executionId"`
	Status        string     `json:"status"`
	InputHash     string     `json:"inputHash"`
	StartedBy     string     `json:"startedBy"`
	StartedAt     time.Time  `json:"startedAt"`
	FinishedAt    *time.Time `json:"finishedAt,omitempty"`
}

func trialDTOFor(value workflow.TrialRun) trialDTO {
	return trialDTO{value.ID, value.CompilationID, value.ExecutionID, value.Status,
		value.InputHash, value.StartedBy, value.StartedAt, value.FinishedAt}
}

type revisionDTO struct {
	ID                  string          `json:"id"`
	RevisionNo          int             `json:"revisionNo"`
	SourceCompilationID string          `json:"sourceCompilationId"`
	DraftSnapshot       json.RawMessage `json:"draftSnapshot"`
	SpecSnapshot        json.RawMessage `json:"specSnapshot"`
	PlanSnapshot        json.RawMessage `json:"planSnapshot"`
	PlanHash            string          `json:"planHash"`
	Status              string          `json:"status"`
	PublishNote         string          `json:"publishNote"`
	CreatedBy           string          `json:"createdBy"`
	CreatedAt           time.Time       `json:"createdAt"`
	ActivatedAt         *time.Time      `json:"activatedAt,omitempty"`
	RetiredAt           *time.Time      `json:"retiredAt,omitempty"`
}

func revisionDTOFor(value workflow.Revision) revisionDTO {
	return revisionDTO{value.ID, value.RevisionNo, value.SourceCompilationID,
		value.DraftSnapshot, value.SpecSnapshot, value.PlanSnapshot, value.PlanHash,
		value.Status, value.PublishNote, value.CreatedBy, value.CreatedAt,
		value.ActivatedAt, value.RetiredAt}
}

type createWorkflowRequest struct {
	Name          string          `json:"name"`
	Slug          string          `json:"slug"`
	Description   string          `json:"description"`
	SchemaVersion string          `json:"schemaVersion"`
	Graph         json.RawMessage `json:"graph"`
}

type generateWorkflowRequest struct {
	Goal string `json:"goal"`
}

func (r *WorkflowRoutes) listWorkflows(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	values, err := r.store.List(c.Request.Context(), c.Param("wid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]workflowDTO, len(values))
	for index := range values {
		items[index] = workflowDTOFor(values[index])
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (r *WorkflowRoutes) createWorkflow(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var request createWorkflowRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, workflow.ErrInvalid)
		return
	}
	capabilityID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	draftID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	value, draft, err := r.store.Create(c.Request.Context(), workflow.CreateInput{
		CapabilityID: capabilityID, DraftID: draftID, WorkspaceID: c.Param("wid"),
		Name: request.Name, Slug: request.Slug, Description: request.Description,
		SchemaVersion: request.SchemaVersion, Graph: request.Graph, CreatedBy: actor(c),
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.Header("ETag", workflowDraftETag(draft))
	c.JSON(http.StatusCreated, gin.H{"workflow": workflowDTOFor(value), "draft": draftDTOFor(draft)})
}

func (r *WorkflowRoutes) generateWorkflow(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var request generateWorkflowRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, smartdag.ErrInvalid)
		return
	}
	result, err := r.generator.Generate(c.Request.Context(), smartdag.GenerateRequest{
		WorkspaceID: c.Param("wid"), Goal: request.Goal, CreatedBy: actor(c),
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.Header("ETag", workflowDraftETag(result.Draft))
	c.JSON(http.StatusCreated, gin.H{
		"workflow":            workflowDTOFor(result.Workflow),
		"draft":               draftDTOFor(result.Draft),
		"reasoningSteps":      result.ReasoningSteps,
		"missingCapabilities": result.MissingCapabilities,
		"nodeExplanations":    result.NodeExplanations,
		"availableToolIds":    result.AvailableToolIDs,
		"selectedToolIds":     result.SelectedToolIDs,
		"reasoning":           result.Reasoning,
		"confidence":          result.Confidence,
	})
}

func (r *WorkflowRoutes) getWorkflow(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	value, err := r.store.Get(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, workflowDTOFor(value))
}

type updateWorkflowRequest struct {
	Name        *string `json:"name"`
	Slug        *string `json:"slug"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
	LockVersion int64   `json:"lockVersion"`
}

func (r *WorkflowRoutes) updateWorkflow(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var request updateWorkflowRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, workflow.ErrInvalid)
		return
	}
	current, err := r.store.Get(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if err != nil {
		RespondError(c, err)
		return
	}
	if request.Name != nil {
		current.Name = *request.Name
	}
	if request.Slug != nil {
		current.Slug = *request.Slug
	}
	if request.Description != nil {
		current.Description = *request.Description
	}
	if request.Status != nil {
		current.Status = *request.Status
	}
	value, err := r.store.UpdateMetadata(c.Request.Context(), c.Param("wid"), c.Param("id"), workflow.MetadataUpdate{
		Name: current.Name, Slug: current.Slug, Description: current.Description,
		Status: current.Status, UpdatedBy: actor(c), ExpectedLockVersion: request.LockVersion,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, workflowDTOFor(value))
}

func (r *WorkflowRoutes) deleteWorkflow(c *gin.Context) {
	if !r.authorize(c, authz.ActionDelete) {
		return
	}
	lockVersion, err := strconv.ParseInt(c.Query("lockVersion"), 10, 64)
	if err == nil && lockVersion > 0 {
		err = r.store.SoftDelete(c.Request.Context(), c.Param("wid"), c.Param("id"), actor(c), lockVersion)
	} else {
		err = workflow.ErrInvalid
	}
	if err != nil {
		RespondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (r *WorkflowRoutes) getDraft(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	value, err := r.store.GetDraft(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if err != nil {
		RespondError(c, err)
		return
	}
	c.Header("ETag", workflowDraftETag(value))
	c.JSON(http.StatusOK, draftDTOFor(value))
}

type updateWorkflowDraftRequest struct {
	SchemaVersion string          `json:"schemaVersion"`
	Graph         json.RawMessage `json:"graph"`
	DraftVersion  int64           `json:"draftVersion"`
	LockVersion   int64           `json:"lockVersion"`
}

func (r *WorkflowRoutes) updateDraft(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var request updateWorkflowDraftRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, workflow.ErrInvalid)
		return
	}
	current, err := r.store.GetDraft(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if err != nil {
		RespondError(c, err)
		return
	}
	if c.GetHeader("If-Match") != workflowDraftETag(current) ||
		request.DraftVersion != current.DraftVersion || request.LockVersion != current.LockVersion {
		RespondError(c, workflow.ErrConflict)
		return
	}
	value, err := r.store.UpdateDraft(c.Request.Context(), c.Param("wid"), c.Param("id"), workflow.DraftUpdate{
		SchemaVersion: request.SchemaVersion, Graph: request.Graph, UpdatedBy: actor(c),
		ExpectedDraftVersion: request.DraftVersion, ExpectedLockVersion: request.LockVersion,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.Header("ETag", workflowDraftETag(value))
	c.JSON(http.StatusOK, draftDTOFor(value))
}

func workflowDraftETag(value workflow.Draft) string {
	return fmt.Sprintf(`"draft-%d-%d"`, value.DraftVersion, value.LockVersion)
}

func (r *WorkflowRoutes) compileDraft(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	value, err := r.compiler.Compile(c.Request.Context(), c.Param("wid"), c.Param("id"), actor(c))
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, compilationDTOFor(value))
}

type trialWorkflowRequest struct {
	Input json.RawMessage `json:"input"`
}

func (r *WorkflowRoutes) trialCompilation(c *gin.Context) {
	if !r.authorize(c, authz.ActionTest) {
		return
	}
	// Same write-only envelope contract as AAP / direct / tool test.
	split, splitErr := ReadOutboundCredentialsBody(c)
	if splitErr != nil {
		RespondError(c, mapOutboundEntryError(splitErr))
		return
	}
	defer split.Zero()
	var request trialWorkflowRequest
	if len(split.BusinessJSON) > 0 {
		if DecodeBusinessJSON(split.BusinessJSON, &request) != nil {
			RespondError(c, workflow.ErrInvalid)
			return
		}
	}
	var value workflow.TrialRun
	var err error
	if withOut, ok := r.trials.(WorkflowTrialerWithOutbound); ok {
		value, err = withOut.RunWithOutbound(
			c.Request.Context(), c.Param("wid"), c.Param("id"), c.Param("cid"), actor(c),
			request.Input, workflow.TrialOutboundOptions{
				CredentialsRaw: split.CredentialsRaw,
				ActorType:      "USER",
			},
		)
	} else {
		// Fail closed when passthrough envelope present but service cannot attach.
		if len(split.CredentialsRaw) > 0 {
			RespondError(c, outboundidentity.ErrCredentialRequired)
			return
		}
		value, err = r.trials.Run(c.Request.Context(), c.Param("wid"), c.Param("id"),
			c.Param("cid"), actor(c), request.Input)
	}
	requestContext, _ := RequestContextFrom(c.Request.Context())
	if err != nil {
		metrics.SmartDag().ObserveTrial("failed")
		slog.Warn("workflow trial failed",
			"event", "workflow.trial.failed",
			"workspace_id", c.Param("wid"),
			"workflow_id", c.Param("id"),
			"compilation_id", c.Param("cid"),
			"trace_id", requestContext.TraceID,
		)
		RespondError(c, err)
		return
	}
	metrics.SmartDag().ObserveTrial("succeeded")
	slog.Info("workflow trial succeeded",
		"event", "workflow.trial.succeeded",
		"workspace_id", c.Param("wid"),
		"workflow_id", c.Param("id"),
		"compilation_id", c.Param("cid"),
		"trace_id", requestContext.TraceID,
	)
	c.JSON(http.StatusOK, trialDTOFor(value))
}

type executeRevisionRequest struct {
	Input   json.RawMessage `json:"input"`
	Trigger string          `json:"trigger"`
}

type productionExecuteDTO struct {
	ExecutionID             string `json:"executionId"`
	WorkflowID              string `json:"workflowId"`
	RevisionID              string `json:"revisionId"`
	Status                  string `json:"status"`
	TraceID                 string `json:"traceId"`
	ConfirmationID          string `json:"confirmationId,omitempty"`
	ResumeToken             string `json:"resumeToken,omitempty"`
	ConfirmationLockVersion int64  `json:"confirmationLockVersion,omitempty"`
}

// executeRevision is production :execute (D4/D11) — separate from trial.
func (r *WorkflowRoutes) executeRevision(c *gin.Context) {
	if r.production == nil {
		RespondError(c, workflow.ErrInvalid)
		return
	}
	if !r.authorize(c, authz.ActionExecute) {
		return
	}
	var request executeRevisionRequest
	// Allow empty body → {} input / default trigger.
	// Production must not accept independent Token fields — inherit from AgentRun root only.
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		body, readErr := io.ReadAll(io.LimitReader(c.Request.Body, MaxOutboundEntryBodyBytes+1))
		if readErr != nil || len(body) > MaxOutboundEntryBodyBytes {
			RespondError(c, workflow.ErrInvalid)
			return
		}
		if err := RejectOutboundCredentialsInProductionBody(body); err != nil {
			RespondError(c, err)
			return
		}
		if len(body) > 0 {
			if DecodeBusinessJSON(body, &request) != nil {
				RespondError(c, workflow.ErrInvalid)
				return
			}
		}
	}
	requestContext, _ := RequestContextFrom(c.Request.Context())
	traceID := strings.TrimSpace(requestContext.TraceID)
	value, err := r.production.Execute(c.Request.Context(), workflow.ProductionExecuteInput{
		WorkspaceID: c.Param("wid"), WorkflowID: c.Param("id"), RevisionID: c.Param("rid"),
		ActorID: actor(c), TraceID: traceID, Trigger: request.Trigger, Input: request.Input,
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		metrics.SmartDag().ObserveExecute("failed")
		slog.Warn("workflow production execute failed",
			"event", "workflow.production_execute.failed",
			"workspace_id", c.Param("wid"),
			"workflow_id", c.Param("id"),
			"revision_id", c.Param("rid"),
			"trace_id", traceID,
		)
		RespondError(c, err)
		return
	}
	metrics.SmartDag().ObserveExecute("succeeded")
	slog.Info("workflow production execute accepted",
		"event", "workflow.production_execute.succeeded",
		"workspace_id", c.Param("wid"),
		"workflow_id", value.WorkflowID,
		"revision_id", value.RevisionID,
		"execution_id", value.ExecutionID,
		"trace_id", value.TraceID,
	)
	c.JSON(http.StatusAccepted, productionExecuteDTO{
		ExecutionID: value.ExecutionID, WorkflowID: value.WorkflowID,
		RevisionID: value.RevisionID, Status: value.Status, TraceID: value.TraceID,
		ConfirmationID:          value.ConfirmationID,
		ResumeToken:             value.ResumeToken,
		ConfirmationLockVersion: value.ConfirmationLockVersion,
	})
}

type executionConfirmationDecisionRequest struct {
	LockVersion int64 `json:"lockVersion"`
}

type executionConfirmationDecisionDTO struct {
	ConfirmationID string `json:"confirmationId"`
	Status         string `json:"status"`
	Decision       string `json:"decision"`
	ResumeStatus   string `json:"resumeStatus,omitempty"`
	ExecutionID    string `json:"executionId,omitempty"`
	Cached         bool   `json:"cached"`
}

func (r *WorkflowRoutes) confirmExecutionConfirmation(c *gin.Context) {
	r.decideExecutionConfirmation(c, execution.InteractionDecisionApprove)
}

func (r *WorkflowRoutes) cancelExecutionConfirmation(c *gin.Context) {
	r.decideExecutionConfirmation(c, execution.InteractionDecisionCancel)
}

func (r *WorkflowRoutes) decideExecutionConfirmation(c *gin.Context, decision string) {
	if r.confirmations == nil {
		RespondError(c, workflow.ErrInvalid)
		return
	}
	if !r.authorize(c, authz.ActionExecute) {
		return
	}
	var request executionConfirmationDecisionRequest
	if decodeJSON(c, &request) != nil || request.LockVersion <= 0 {
		RespondError(c, workflow.ErrInvalid)
		return
	}
	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if idempotencyKey == "" {
		idempotencyKey = uuid.NewString()
	}
	result, err := r.confirmations.DecideStored(
		c.Request.Context(),
		c.Param("wid"), c.Param("cid"), actor(c), decision, idempotencyKey, request.LockVersion,
	)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, executionConfirmationDecisionDTO{
		ConfirmationID: result.Confirmation.ID,
		Status:         result.Confirmation.Status,
		Decision:       result.Decision,
		ResumeStatus:   result.ResumeStatus,
		ExecutionID:    result.Confirmation.ExecutionID,
		Cached:         result.Cached,
	})
}

type publishWorkflowRequest struct {
	CallableName         string `json:"callableName"`
	CallableDescription  string `json:"callableDescription"`
	RiskLevel            string `json:"riskLevel"`
	SideEffectLevel      string `json:"sideEffectLevel"`
	RequiresConfirmation bool   `json:"requiresConfirmation"`
	PublishNote          string `json:"publishNote"`
}

func (r *WorkflowRoutes) publishCompilation(c *gin.Context) {
	if !r.authorize(c, authz.ActionPublish) {
		return
	}
	var request publishWorkflowRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, workflow.ErrInvalid)
		return
	}
	revisionID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	releaseID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	eventID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	value, err := r.publisher.Publish(c.Request.Context(), workflow.PublishWorkflowInput{
		RevisionID: revisionID, ReleaseID: releaseID, EventID: eventID,
		WorkspaceID: c.Param("wid"), CapabilityID: c.Param("id"), CompilationID: c.Param("cid"),
		CallableName: request.CallableName, CallableDescription: request.CallableDescription,
		RiskLevel: request.RiskLevel, SideEffectLevel: request.SideEffectLevel,
		RequiresConfirmation: request.RequiresConfirmation, PublishNote: request.PublishNote,
		PublishedBy: actor(c),
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"revision": revisionDTOFor(value.Revision), "releaseId": value.Release.ID,
		"releaseNo": value.Release.ReleaseNo, "trialId": value.Trial.ID,
	})
}

type forcePublishWorkflowRequest struct {
	publishWorkflowRequest
	Reason string `json:"reason"`
}

// forcePublishCompilation is a PLATFORM_ADMIN escape hatch: skips real trial run,
// still freezes revision + capability_release. Requires tools.allowForcePublish
// (shared gate with tool force-publish) and a non-empty reason (≥8 chars).
func (r *WorkflowRoutes) forcePublishCompilation(c *gin.Context) {
	if !platformAdmin(c) {
		RespondError(c, authz.ErrDenied)
		return
	}
	if !r.authorize(c, authz.ActionPublish) {
		return
	}
	forcer, ok := r.publisher.(WorkflowForcePublisher)
	if !ok {
		RespondError(c, workflow.ErrForcePublishDisabled)
		return
	}
	var request forcePublishWorkflowRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, workflow.ErrInvalid)
		return
	}
	revisionID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	releaseID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	eventID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	trialID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	executionID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	value, err := forcer.ForcePublish(c.Request.Context(), workflow.ForcePublishWorkflowInput{
		PublishWorkflowInput: workflow.PublishWorkflowInput{
			RevisionID: revisionID, ReleaseID: releaseID, EventID: eventID,
			WorkspaceID: c.Param("wid"), CapabilityID: c.Param("id"), CompilationID: c.Param("cid"),
			CallableName: request.CallableName, CallableDescription: request.CallableDescription,
			RiskLevel: request.RiskLevel, SideEffectLevel: request.SideEffectLevel,
			RequiresConfirmation: request.RequiresConfirmation, PublishNote: request.PublishNote,
			PublishedBy: actor(c),
		},
		TrialID: trialID, ExecutionID: executionID, Reason: request.Reason,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"revision": revisionDTOFor(value.Revision), "releaseId": value.Release.ID,
		"releaseNo": value.Release.ReleaseNo, "trialId": value.Trial.ID,
		"force": true, "forceReason": request.Reason,
	})
}

func (r *WorkflowRoutes) listRevisions(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	values, err := r.store.ListRevisions(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]revisionDTO, len(values))
	for index := range values {
		items[index] = revisionDTOFor(values[index])
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (r *WorkflowRoutes) diffRevisions(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	value, err := r.store.DiffRevisions(c.Request.Context(), c.Param("wid"), c.Param("id"),
		c.Query("from"), c.Query("to"))
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"from": revisionDTOFor(value.From), "to": revisionDTOFor(value.To),
		"changes": gin.H{
			"draft": value.DraftChanged, "spec": value.SpecChanged,
			"plan": value.PlanChanged, "planHash": value.PlanHashChanged,
		},
	})
}

func (r *WorkflowRoutes) activateRevision(c *gin.Context) {
	if !r.authorize(c, authz.ActionPublish) {
		return
	}
	eventID, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	value, err := r.activator.Activate(c.Request.Context(), workflow.ActivateRevisionInput{
		EventID: eventID, WorkspaceID: c.Param("wid"), CapabilityID: c.Param("id"),
		RevisionID: c.Param("rid"), ActivatedBy: actor(c),
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"revision": revisionDTOFor(value.Revision), "releaseId": value.Release.ID,
		"releaseNo": value.Release.ReleaseNo, "eventType": value.Event.Type,
	})
}

func (r *WorkflowRoutes) getReadiness(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	value, err := r.readiness.Get(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, readinessDTOFor(value))
}

type readinessDTO struct {
	Stage              string                      `json:"stage"`
	CanCompile         bool                        `json:"canCompile"`
	CanTrial           bool                        `json:"canTrial"`
	CanPublish         bool                        `json:"canPublish"`
	CompilationID      *string                     `json:"compilationId,omitempty"`
	CompilationCurrent bool                        `json:"compilationCurrent"`
	CompilationValid   bool                        `json:"compilationValid"`
	TrialCurrent       bool                        `json:"trialCurrent"`
	TrialSuccessful    bool                        `json:"trialSuccessful"`
	Published          bool                        `json:"published"`
	ActiveRevisionID   *string                     `json:"activeRevisionId,omitempty"`
	Blockers           []workflow.ReadinessBlocker `json:"blockers"`
	UpdatedAt          time.Time                   `json:"updatedAt"`
}

func readinessDTOFor(value workflow.Readiness) readinessDTO {
	return readinessDTO{
		Stage: value.Stage, CanCompile: value.CanCompile, CanTrial: value.CanTrial,
		CanPublish: value.CanPublish, CompilationID: value.CompilationID,
		CompilationCurrent: value.CompilationCurrent, CompilationValid: value.CompilationValid,
		TrialCurrent: value.TrialCurrent, TrialSuccessful: value.TrialSuccessful,
		Published: value.Published, ActiveRevisionID: value.ActiveRevisionID,
		Blockers: value.Blockers, UpdatedAt: value.UpdatedAt,
	}
}
