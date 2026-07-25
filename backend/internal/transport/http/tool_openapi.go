package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/openapiimport"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/tool"

	"github.com/gin-gonic/gin"
)

type ToolStore interface {
	List(context.Context, string) ([]tool.Tool, error)
	Create(context.Context, tool.CreateInput) (tool.Tool, tool.Version, error)
	Get(context.Context, string, string) (tool.Tool, error)
	UpdateMetadata(context.Context, string, string, tool.MetadataUpdate) (tool.Tool, error)
	SoftDelete(context.Context, string, string, string, int64) error
	GetVersion(context.Context, string, string, string) (tool.Version, error)
	ListVersions(context.Context, string, string) ([]tool.Version, error)
	UpdateDraft(context.Context, string, string, string, tool.DraftUpdate) (tool.Version, error)
	CreateDraftFromPublished(context.Context, string, string, string, string, string) (tool.Version, error)
}
type ToolTestRunner interface {
	Run(context.Context, tool.RunToolTestInput) (tool.TestRecord, error)
}
type ToolTestConnectionResolver interface {
	ResolveTestConnection(context.Context, string, string, string, string) (execution.ConnectionSnapshot, execution.CredentialReference, error)
}
type ToolPublisher interface {
	Publish(context.Context, tool.PublishToolInput) (tool.PublishToolResult, error)
}
type ToolInvoker interface {
	Invoke(context.Context, execution.InvokeRequest) (execution.PipelineResult, error)
}
type OpenAPIStore interface {
	List(context.Context, string) ([]openapiimport.Import, error)
	Get(context.Context, string, string) (openapiimport.Import, error)
	ListEndpoints(context.Context, string, string) ([]openapiimport.Endpoint, error)
	Delete(context.Context, string, string) error
}
type OpenAPIImporter interface {
	Import(context.Context, openapiimport.ProviderImportRequest) (openapiimport.ParseOutcome, error)
}
type OpenAPIFileImporter interface {
	ImportFile(context.Context, openapiimport.FileImportRequest) (openapiimport.ParseOutcome, error)
}
type OpenAPIGenerator interface {
	Generate(context.Context, openapiimport.GenerateToolsRequest) ([]openapiimport.GeneratedTool, error)
}

type ToolOpenAPIRoutes struct {
	authorizer      WorkspaceAuthorizer
	tools           ToolStore
	tests           ToolTestRunner
	testConnections ToolTestConnectionResolver
	publisher       ToolPublisher
	invoker         ToolInvoker
	imports         OpenAPIStore
	importer        OpenAPIImporter
	fileImporter    OpenAPIFileImporter
	generator       OpenAPIGenerator
}
type ToolOpenAPIDependencies struct {
	Authorizer      WorkspaceAuthorizer
	Tools           ToolStore
	Tests           ToolTestRunner
	TestConnections ToolTestConnectionResolver
	Publisher       ToolPublisher
	Invoker         ToolInvoker
	Imports         OpenAPIStore
	Importer        OpenAPIImporter
	FileImporter    OpenAPIFileImporter
	Generator       OpenAPIGenerator
}

func NewToolOpenAPIRoutes(d ToolOpenAPIDependencies) (*ToolOpenAPIRoutes, error) {
	if d.Authorizer == nil || d.Tools == nil || d.Tests == nil || d.TestConnections == nil || d.Publisher == nil || d.Invoker == nil || d.Imports == nil || d.Importer == nil || d.FileImporter == nil || d.Generator == nil {
		return nil, errors.New("tool openapi route dependencies are required")
	}
	return &ToolOpenAPIRoutes{
		authorizer: d.Authorizer, tools: d.Tools, tests: d.Tests,
		testConnections: d.TestConnections, publisher: d.Publisher, invoker: d.Invoker,
		imports: d.Imports, importer: d.Importer, fileImporter: d.FileImporter, generator: d.Generator,
	}, nil
}
func (r *ToolOpenAPIRoutes) RegisterV1(v1 V1Routes) {
	g := v1.Protected
	g.GET("/workspaces/:wid/tools", r.listTools)
	g.POST("/workspaces/:wid/tools", r.createTool)
	g.GET("/workspaces/:wid/tools/:id", r.getTool)
	g.PATCH("/workspaces/:wid/tools/:id", r.updateTool)
	g.DELETE("/workspaces/:wid/tools/:id", r.deleteTool)
	g.GET("/workspaces/:wid/tools/:id/versions", r.listVersions)
	g.POST("/workspaces/:wid/tools/:id/versions", r.createVersion)
	g.GET("/workspaces/:wid/tools/:id/versions/:vid", r.getVersion)
	g.PATCH("/workspaces/:wid/tools/:id/versions/:vid", r.updateVersion)
	g.POST("/workspaces/:wid/tools/:id/versions/:vid/__command/test", r.testVersion)
	g.POST("/workspaces/:wid/tools/:id/versions/:vid/__command/publish", r.publishVersion)
	g.POST("/workspaces/:wid/tools/:id/__command/invoke", r.invokeTool)
	g.GET("/workspaces/:wid/openapi-imports", r.listImports)
	g.POST("/workspaces/:wid/openapi-imports", r.createImport)
	g.POST("/workspaces/:wid/openapi-imports/__command/upload", r.uploadImport)
	g.GET("/workspaces/:wid/openapi-imports/:id", r.getImport)
	g.DELETE("/workspaces/:wid/openapi-imports/:id", r.deleteImport)
	g.POST("/workspaces/:wid/openapi-imports/:id/__command/generate-tools", r.generateTools)
}
func (r *ToolOpenAPIRoutes) authorize(c *gin.Context, a authz.Action) bool {
	p, _ := PrincipalFrom(c.Request.Context())
	if _, e := r.authorizer.AuthorizeWorkspace(c.Request.Context(), p.UserID, c.Param("wid"), a); e != nil {
		RespondError(c, e)
		return false
	}
	return true
}

type toolDTO struct {
	ID                  string    `json:"id"`
	ProviderID          string    `json:"providerId"`
	SourceAssetID       *string   `json:"sourceAssetId,omitempty"`
	DefaultConnectionID *string   `json:"defaultConnectionId,omitempty"`
	SourceEndpointID    *string   `json:"sourceEndpointId,omitempty"`
	Name                string    `json:"name"`
	Slug                string    `json:"slug"`
	Description         string    `json:"description"`
	Status              string    `json:"status"`
	ActiveReleaseID     *string   `json:"activeReleaseId,omitempty"`
	CreatedBy           string    `json:"createdBy"`
	UpdatedBy           string    `json:"updatedBy"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
	LockVersion         int64     `json:"lockVersion"`
}

func toolDTOFor(v tool.Tool) toolDTO {
	return toolDTO{v.CapabilityID, v.ProviderID, v.SourceAssetID, v.DefaultConnectionID, v.SourceEndpointID, v.Name, v.Slug, v.Description, v.Status, v.ActiveReleaseID, v.CreatedBy, v.UpdatedBy, v.CreatedAt, v.UpdatedAt, v.LockVersion}
}

type versionDTO struct {
	ID                   string          `json:"id"`
	VersionNo            int             `json:"versionNo"`
	LifecycleStatus      string          `json:"lifecycleStatus"`
	ExecutorType         string          `json:"executorType"`
	ProviderAssetID      *string         `json:"providerAssetId,omitempty"`
	DefaultConnectionID  *string         `json:"defaultConnectionId,omitempty"`
	ActionSchemaVersion  string          `json:"actionSchemaVersion"`
	ActionConfig         json.RawMessage `json:"actionConfig"`
	InputSchema          json.RawMessage `json:"inputSchema"`
	OutputSchema         json.RawMessage `json:"outputSchema"`
	ErrorMappings        json.RawMessage `json:"errorMappings"`
	RuntimePolicy        json.RawMessage `json:"runtimePolicy"`
	RiskLevel            string          `json:"riskLevel"`
	SideEffectLevel      string          `json:"sideEffectLevel"`
	RequiresConfirmation bool            `json:"requiresConfirmation"`
	Checksum             string          `json:"checksum"`
	CreatedBy            string          `json:"createdBy"`
	UpdatedBy            string          `json:"updatedBy"`
	PublishedAt          *time.Time      `json:"publishedAt,omitempty"`
	LockVersion          int64           `json:"lockVersion"`
}

func versionDTOFor(v tool.Version) versionDTO {
	return versionDTO{v.ID, v.VersionNo, v.LifecycleStatus, v.ExecutorType, v.ProviderAssetID, v.DefaultConnectionID, v.ActionSchemaVersion, v.ActionConfig, v.InputSchema, v.OutputSchema, v.ErrorMappings, v.RuntimePolicy, v.RiskLevel, v.SideEffectLevel, v.RequiresConfirmation, v.Checksum, v.CreatedBy, v.UpdatedBy, v.PublishedAt, v.LockVersion}
}

type draftRequest struct {
	ProviderAssetID      *string         `json:"providerAssetId"`
	DefaultConnectionID  *string         `json:"defaultConnectionId"`
	ActionSchemaVersion  string          `json:"actionSchemaVersion"`
	ActionConfig         json.RawMessage `json:"actionConfig"`
	InputSchema          json.RawMessage `json:"inputSchema"`
	OutputSchema         json.RawMessage `json:"outputSchema"`
	ErrorMappings        json.RawMessage `json:"errorMappings"`
	RuntimePolicy        json.RawMessage `json:"runtimePolicy"`
	RiskLevel            string          `json:"riskLevel"`
	SideEffectLevel      string          `json:"sideEffectLevel"`
	RequiresConfirmation bool            `json:"requiresConfirmation"`
}

func (q draftRequest) spec() tool.DraftSpec {
	return tool.DraftSpec{ProviderAssetID: q.ProviderAssetID, DefaultConnectionID: q.DefaultConnectionID, ActionSchemaVersion: q.ActionSchemaVersion, ActionConfig: q.ActionConfig, InputSchema: q.InputSchema, OutputSchema: q.OutputSchema, ErrorMappings: q.ErrorMappings, RuntimePolicy: q.RuntimePolicy, RiskLevel: q.RiskLevel, SideEffectLevel: q.SideEffectLevel, RequiresConfirmation: q.RequiresConfirmation}
}

type createToolRequest struct {
	ProviderID          string       `json:"providerId"`
	SourceAssetID       *string      `json:"sourceAssetId"`
	DefaultConnectionID *string      `json:"defaultConnectionId"`
	SourceEndpointID    *string      `json:"sourceEndpointId"`
	Name                string       `json:"name"`
	Slug                string       `json:"slug"`
	Description         string       `json:"description"`
	Draft               draftRequest `json:"draft"`
}

func (r *ToolOpenAPIRoutes) listTools(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	v, e := r.tools.List(c.Request.Context(), c.Param("wid"))
	if e != nil {
		RespondError(c, e)
		return
	}
	items := make([]toolDTO, len(v))
	for i := range v {
		items[i] = toolDTOFor(v[i])
	}
	c.JSON(200, gin.H{"items": items})
}
func (r *ToolOpenAPIRoutes) createTool(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var q createToolRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, tool.ErrInvalid)
		return
	}
	id, e := newV7()
	if e != nil {
		RespondError(c, e)
		return
	}
	vid, e := newV7()
	if e != nil {
		RespondError(c, e)
		return
	}
	v, d, e := r.tools.Create(c.Request.Context(), tool.CreateInput{CapabilityID: id, InitialVersionID: vid, WorkspaceID: c.Param("wid"), ProviderID: q.ProviderID, SourceAssetID: q.SourceAssetID, DefaultConnectionID: q.DefaultConnectionID, SourceEndpointID: q.SourceEndpointID, Name: q.Name, Slug: q.Slug, Description: q.Description, Draft: q.Draft.spec(), CreatedBy: actor(c)})
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(201, gin.H{"tool": toolDTOFor(v), "draft": versionDTOFor(d)})
}
func (r *ToolOpenAPIRoutes) getTool(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	v, e := r.tools.Get(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, toolDTOFor(v))
}

type updateToolRequest struct {
	Name                *string `json:"name"`
	Slug                *string `json:"slug"`
	Description         *string `json:"description"`
	Status              *string `json:"status"`
	SourceAssetID       *string `json:"sourceAssetId"`
	DefaultConnectionID *string `json:"defaultConnectionId"`
	LockVersion         int64   `json:"lockVersion"`
}

func (r *ToolOpenAPIRoutes) updateTool(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var q updateToolRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, tool.ErrInvalid)
		return
	}
	old, e := r.tools.Get(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if e != nil {
		RespondError(c, e)
		return
	}
	if q.Name != nil {
		old.Name = *q.Name
	}
	if q.Slug != nil {
		old.Slug = *q.Slug
	}
	if q.Description != nil {
		old.Description = *q.Description
	}
	if q.Status != nil {
		old.Status = *q.Status
	}
	if q.SourceAssetID != nil {
		old.SourceAssetID = q.SourceAssetID
	}
	if q.DefaultConnectionID != nil {
		old.DefaultConnectionID = q.DefaultConnectionID
	}
	v, e := r.tools.UpdateMetadata(c.Request.Context(), c.Param("wid"), c.Param("id"), tool.MetadataUpdate{Name: old.Name, Slug: old.Slug, Description: old.Description, Status: old.Status, SourceAssetID: old.SourceAssetID, DefaultConnectionID: old.DefaultConnectionID, UpdatedBy: actor(c), ExpectedLockVersion: q.LockVersion})
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, toolDTOFor(v))
}
func (r *ToolOpenAPIRoutes) deleteTool(c *gin.Context) {
	if !r.authorize(c, authz.ActionDelete) {
		return
	}
	lock, e := strconv.ParseInt(c.Query("lockVersion"), 10, 64)
	if e == nil && lock > 0 {
		e = r.tools.SoftDelete(c.Request.Context(), c.Param("wid"), c.Param("id"), actor(c), lock)
	} else {
		e = tool.ErrInvalid
	}
	if e != nil {
		RespondError(c, e)
		return
	}
	c.Status(204)
}
func (r *ToolOpenAPIRoutes) listVersions(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	v, e := r.tools.ListVersions(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if e != nil {
		RespondError(c, e)
		return
	}
	items := make([]versionDTO, len(v))
	for i := range v {
		items[i] = versionDTOFor(v[i])
	}
	c.JSON(200, gin.H{"items": items})
}

type createVersionRequest struct {
	SourceVersionID string `json:"sourceVersionId"`
}

func (r *ToolOpenAPIRoutes) createVersion(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var q createVersionRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, tool.ErrInvalid)
		return
	}
	id, e := newV7()
	if e != nil {
		RespondError(c, e)
		return
	}
	v, e := r.tools.CreateDraftFromPublished(c.Request.Context(), c.Param("wid"), c.Param("id"), q.SourceVersionID, id, actor(c))
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(201, versionDTOFor(v))
}
func (r *ToolOpenAPIRoutes) getVersion(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	v, e := r.tools.GetVersion(c.Request.Context(), c.Param("wid"), c.Param("id"), c.Param("vid"))
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, versionDTOFor(v))
}

type updateVersionRequest struct {
	Draft           draftRequest `json:"draft"`
	LifecycleStatus string       `json:"lifecycleStatus"`
	LockVersion     int64        `json:"lockVersion"`
}

func (r *ToolOpenAPIRoutes) updateVersion(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var q updateVersionRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, tool.ErrInvalid)
		return
	}
	v, e := r.tools.UpdateDraft(c.Request.Context(), c.Param("wid"), c.Param("id"), c.Param("vid"), tool.DraftUpdate{Spec: q.Draft.spec(), LifecycleStatus: q.LifecycleStatus, UpdatedBy: actor(c), ExpectedLockVersion: q.LockVersion})
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, versionDTOFor(v))
}

type testVersionRequest struct {
	ConnectionID string          `json:"connectionId"`
	Input        json.RawMessage `json:"input"`
}

type toolTestDTO struct {
	ID                   string          `json:"id"`
	Status               string          `json:"status"`
	ConnectivityPassed   bool            `json:"connectivityPassed"`
	ResponseSchemaPassed bool            `json:"responseSchemaPassed"`
	ErrorMappingPassed   bool            `json:"errorMappingPassed"`
	RuntimePolicyPassed  bool            `json:"runtimePolicyPassed"`
	RequestSummary       json.RawMessage `json:"requestSummary"`
	ResponseSummary      json.RawMessage `json:"responseSummary"`
	LatencyMS            *int            `json:"latencyMs,omitempty"`
	ErrorCode            *string         `json:"errorCode,omitempty"`
	TestedBy             string          `json:"testedBy"`
	TestedAt             time.Time       `json:"testedAt"`
}

func toolTestDTOFor(v tool.TestRecord) toolTestDTO {
	return toolTestDTO{v.ID, v.Status, v.ConnectivityPassed, v.ResponseSchemaPassed,
		v.ErrorMappingPassed, v.RuntimePolicyPassed, v.RequestSummary, v.ResponseSummary,
		v.LatencyMS, v.ErrorCode, v.TestedBy, v.TestedAt}
}

func (r *ToolOpenAPIRoutes) testVersion(c *gin.Context) {
	if !r.authorize(c, authz.ActionTest) {
		return
	}
	// Strip write-only outboundCredentials before business decode (same contract as AAP).
	split, splitErr := ReadOutboundCredentialsBody(c)
	if splitErr != nil {
		RespondError(c, mapOutboundEntryError(splitErr))
		return
	}
	defer split.Zero()
	var q testVersionRequest
	if DecodeBusinessJSON(split.BusinessJSON, &q) != nil {
		RespondError(c, tool.ErrInvalid)
		return
	}
	connection, credential, e := r.testConnections.ResolveTestConnection(c.Request.Context(), c.Param("wid"), c.Param("id"), c.Param("vid"), q.ConnectionID)
	if e != nil {
		RespondError(c, e)
		return
	}
	id, e := newV7()
	if e != nil {
		RespondError(c, e)
		return
	}
	rc, _ := RequestContextFrom(c.Request.Context())
	// Transfer write-only envelope ownership into the test service attach boundary.
	// split.Zero must not clear the transferred slice.
	creds := split.CredentialsRaw
	split.CredentialsRaw = nil
	v, e := r.tests.Run(c.Request.Context(), tool.RunToolTestInput{
		TestID: id, WorkspaceID: c.Param("wid"), CapabilityID: c.Param("id"), VersionID: c.Param("vid"),
		TraceID: rc.TraceID, TestedBy: actor(c), Connection: connection, Credential: credential,
		Input: q.Input, CredentialsRaw: creds,
	})
	_ = outboundidentity.ZeroCredentialsRaw(creds)
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, toolTestDTOFor(v))
}

type publishVersionRequest struct {
	CallableName        string `json:"callableName"`
	CallableDescription string `json:"callableDescription"`
	LockVersion         int64  `json:"lockVersion"`
}

func (r *ToolOpenAPIRoutes) publishVersion(c *gin.Context) {
	if !r.authorize(c, authz.ActionPublish) {
		return
	}
	var q publishVersionRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, tool.ErrInvalid)
		return
	}
	rid, e := newV7()
	if e != nil {
		RespondError(c, e)
		return
	}
	eid, e := newV7()
	if e != nil {
		RespondError(c, e)
		return
	}
	v, e := r.publisher.Publish(c.Request.Context(), tool.PublishToolInput{ReleaseID: rid, EventID: eid, WorkspaceID: c.Param("wid"), CapabilityID: c.Param("id"), VersionID: c.Param("vid"), CallableName: q.CallableName, CallableDescription: q.CallableDescription, PublishedBy: actor(c), ExpectedVersionLock: q.LockVersion})
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, gin.H{"releaseId": v.Release.ID, "releaseNo": v.Release.ReleaseNo, "version": versionDTOFor(v.Version), "testId": v.Test.ID})
}

type invokeToolRequest struct {
	ReleaseID      string          `json:"releaseId"`
	ConnectionID   string          `json:"connectionId"`
	ConfirmationID string          `json:"confirmationId"`
	PlanHash       string          `json:"planHash"`
	Input          json.RawMessage `json:"input"`
}

func (r *ToolOpenAPIRoutes) invokeTool(c *gin.Context) {
	if !r.authorize(c, authz.ActionExecute) {
		return
	}
	split, splitErr := ReadOutboundCredentialsBody(c)
	if splitErr != nil {
		RespondError(c, mapOutboundEntryError(splitErr))
		return
	}
	defer split.Zero()
	var q invokeToolRequest
	if DecodeBusinessJSON(split.BusinessJSON, &q) != nil {
		RespondError(c, tool.ErrInvalid)
		return
	}
	id, e := newV7()
	if e != nil {
		RespondError(c, e)
		return
	}
	rc, _ := RequestContextFrom(c.Request.Context())
	// Transfer write-only envelope for direct-invocation Vault attach (same
	// REQUEST_PASSTHROUGH contract as tool test / workflow trial).
	creds := split.CredentialsRaw
	split.CredentialsRaw = nil
	v, e := r.invoker.Invoke(c.Request.Context(), execution.InvokeRequest{
		InvocationID: id, WorkspaceID: c.Param("wid"), CapabilityID: c.Param("id"),
		ReleaseID: q.ReleaseID, ActorType: "USER", ActorID: actor(c), TraceID: rc.TraceID,
		Input: q.Input, ExplicitConnectionID: q.ConnectionID, PlanHash: q.PlanHash,
		ConfirmationID: q.ConfirmationID, IdempotencyKey: c.GetHeader("Idempotency-Key"),
		OutboundCredentialsRaw: creds,
	})
	_ = outboundidentity.ZeroCredentialsRaw(creds)
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(200, gin.H{"invocationId": v.InvocationID, "output": v.Output, "httpStatus": v.HTTPStatus, "attempts": v.Attempts, "cached": v.Cached})
}

type importDTO struct {
	ID             string    `json:"id"`
	ProviderID     *string   `json:"providerId,omitempty"`
	ConnectionID   *string   `json:"connectionId,omitempty"`
	SourceType     string    `json:"sourceType"`
	SourceURI      *string   `json:"sourceUri,omitempty"`
	SourceRevision *string   `json:"sourceRevision,omitempty"`
	FileName       string    `json:"fileName"`
	ContentSHA256  string    `json:"contentSha256"`
	ParserVersion  string    `json:"parserVersion"`
	Status         string    `json:"status"`
	TotalEndpoints int       `json:"totalEndpoints"`
	ReadyEndpoints int       `json:"readyEndpoints"`
	IssueCount     int       `json:"issueCount"`
	CreatedBy      string    `json:"createdBy"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

func importDTOFor(v openapiimport.Import) importDTO {
	return importDTO{v.ID, v.ProviderID, v.ConnectionID, v.SourceType, v.SourceURI, v.SourceRevision, v.FileName, v.ContentSHA256, v.ParserVersion, v.Status, v.TotalEndpoints, v.ReadyEndpoints, v.IssueCount, v.CreatedBy, v.CreatedAt, v.UpdatedAt}
}

type endpointDTO struct {
	ID                    string          `json:"id"`
	Method                string          `json:"method"`
	Path                  string          `json:"path"`
	OperationID           string          `json:"operationId"`
	Summary               string          `json:"summary"`
	InputSchema           json.RawMessage `json:"inputSchema"`
	OutputSchema          json.RawMessage `json:"outputSchema"`
	Issues                json.RawMessage `json:"issues"`
	Ready                 bool            `json:"ready"`
	GeneratedCapabilityID *string         `json:"generatedCapabilityId,omitempty"`
}

func endpointDTOFor(v openapiimport.Endpoint) endpointDTO {
	return endpointDTO{v.ID, v.Method, v.Path, v.OperationID, v.Summary, v.InputSchema, v.OutputSchema, v.Issues, v.Ready, v.GeneratedCapabilityID}
}
func (r *ToolOpenAPIRoutes) listImports(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	v, e := r.imports.List(c.Request.Context(), c.Param("wid"))
	if e != nil {
		RespondError(c, e)
		return
	}
	items := make([]importDTO, len(v))
	for i := range v {
		items[i] = importDTOFor(v[i])
	}
	c.JSON(200, gin.H{"items": items})
}

type createImportRequest struct {
	ProviderID   string  `json:"providerId"`
	ConnectionID *string `json:"connectionId"`
}

func (r *ToolOpenAPIRoutes) createImport(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var q createImportRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, openapiimport.ErrInvalid)
		return
	}
	id, e := newV7()
	if e != nil {
		RespondError(c, e)
		return
	}
	v, e := r.importer.Import(c.Request.Context(), openapiimport.ProviderImportRequest{ImportID: id, WorkspaceID: c.Param("wid"), ProviderID: q.ProviderID, ConnectionID: q.ConnectionID, CreatedBy: actor(c)})
	if e != nil {
		RespondError(c, e)
		return
	}
	c.JSON(202, gin.H{"import": importDTOFor(v.Import), "duplicateOfId": v.DuplicateOfID})
}

func (r *ToolOpenAPIRoutes) uploadImport(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	const multipartOverhead = int64(1 << 20)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, openapiimport.DefaultOpenAPIDocumentMaxBytes+multipartOverhead)
	if err := c.Request.ParseMultipartForm(multipartOverhead); err != nil {
		RespondError(c, openapiimport.ErrInvalid)
		return
	}
	providerID := strings.TrimSpace(c.PostForm("providerId"))
	connectionID := strings.TrimSpace(c.PostForm("connectionId"))
	fileHeader, err := c.FormFile("file")
	if err != nil || fileHeader.Size <= 0 || fileHeader.Size > openapiimport.DefaultOpenAPIDocumentMaxBytes {
		RespondError(c, openapiimport.ErrInvalid)
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		RespondError(c, openapiimport.ErrInvalid)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, openapiimport.DefaultOpenAPIDocumentMaxBytes+1))
	if err != nil || int64(len(content)) > openapiimport.DefaultOpenAPIDocumentMaxBytes {
		RespondError(c, openapiimport.ErrInvalid)
		return
	}
	id, err := newV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	var optionalConnectionID *string
	if connectionID != "" {
		optionalConnectionID = &connectionID
	}
	contentType := strings.TrimSpace(fileHeader.Header.Get("Content-Type"))
	v, err := r.fileImporter.ImportFile(c.Request.Context(), openapiimport.FileImportRequest{
		ImportID: id, WorkspaceID: c.Param("wid"), ProviderID: providerID,
		ConnectionID: optionalConnectionID, FileName: fileHeader.Filename,
		ContentType: contentType, Content: content, CreatedBy: actor(c),
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"import": importDTOFor(v.Import), "duplicateOfId": v.DuplicateOfID})
}
func (r *ToolOpenAPIRoutes) getImport(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	v, e := r.imports.Get(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if e != nil {
		RespondError(c, e)
		return
	}
	endpoints, e := r.imports.ListEndpoints(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if e != nil {
		RespondError(c, e)
		return
	}
	items := make([]endpointDTO, len(endpoints))
	for i := range endpoints {
		items[i] = endpointDTOFor(endpoints[i])
	}
	c.JSON(200, gin.H{"import": importDTOFor(v), "endpoints": items})
}
func (r *ToolOpenAPIRoutes) deleteImport(c *gin.Context) {
	if !r.authorize(c, authz.ActionDelete) {
		return
	}
	e := r.imports.Delete(c.Request.Context(), c.Param("wid"), c.Param("id"))
	if e != nil {
		RespondError(c, e)
		return
	}
	c.Status(204)
}

type generateToolsRequest struct {
	EndpointIDs []string `json:"endpointIds"`
}

func (r *ToolOpenAPIRoutes) generateTools(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	var q generateToolsRequest
	if decodeJSON(c, &q) != nil {
		RespondError(c, openapiimport.ErrInvalid)
		return
	}
	v, e := r.generator.Generate(c.Request.Context(), openapiimport.GenerateToolsRequest{WorkspaceID: c.Param("wid"), ImportID: c.Param("id"), EndpointIDs: q.EndpointIDs, CreatedBy: actor(c)})
	if e != nil {
		RespondError(c, e)
		return
	}
	items := make([]gin.H, len(v))
	for i := range v {
		items[i] = gin.H{"endpointId": v[i].EndpointID, "tool": toolDTOFor(v[i].Tool), "draft": versionDTOFor(v[i].Draft)}
	}
	c.JSON(201, gin.H{"items": items})
}
