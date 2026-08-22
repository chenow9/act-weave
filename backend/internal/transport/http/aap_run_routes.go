package httptransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/config"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/transport/sse"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var (
	ErrAAPRunEventsRequestInvalid       = errors.New("AAP Run events request is invalid")
	ErrAAPInteractionDecisionReqInvalid = errors.New("AAP Interaction decision request is invalid")
)

type AAPRunApplication interface {
	Create(context.Context, aap.CreateRunInput) (aap.CreateRunResult, error)
}

type AAPRunCancellationApplication interface {
	Cancel(context.Context, aap.CancelRunInput) (aap.CancelRunResult, error)
}

type AAPInteractionDecisionApplication interface {
	Decide(context.Context, aap.DecideInteractionInput) (aap.DecideInteractionResult, error)
}

type AAPRunResourceReader interface {
	GetAgentRun(context.Context, string, string) (execution.AgentRun, error)
}

type AAPRunItemReader interface {
	ListForRun(context.Context, string, string, string) ([]protocolevent.RunItemProjection, error)
}

// AAPCreateRunFileLookup loads file status for createRun input_file validation (IC-06).
type AAPCreateRunFileLookup interface {
	GetFile(context.Context, string, string) (aapfile.File, error)
	// PromoteRetentionOnReference is optional; implementations may no-op.
	PromoteRetentionOnReference(context.Context, string, string) error
}

type AAPRunRoutes struct {
	authorizer    AAPDataPlaneAuthorizer
	conversations AAPConversationApplication
	runs          AAPRunApplication
	reader        AAPRunResourceReader
	items         AAPRunItemReader
	attacher      *AAPEventCatchUp
	canceller     AAPRunCancellationApplication
	decider       AAPInteractionDecisionApplication
	quota         agentaccess.DataPlaneQuota
	// outboundAttachConfigured is true when the Run application is wired with
	// BindingAttacher (fail-closed when credentials present but not configured).
	outboundAttachConfigured bool
	// filesGate + fileLookup gate input_file createRun (KD-23 / READY checks).
	filesGate  *config.AgentAccessFilesConfig
	fileLookup AAPCreateRunFileLookup
}

func (routes *AAPRunRoutes) ConfigureCommandQuota(quota agentaccess.DataPlaneQuota) error {
	if routes == nil || quota == nil || routes.quota != nil {
		return ErrAAPCreateRunInvalid
	}
	routes.quota = quota
	return nil
}

func NewAAPRunRoutes(
	authorizer AAPDataPlaneAuthorizer,
	conversations AAPConversationApplication,
	runs AAPRunApplication,
	reader AAPRunResourceReader,
	items AAPRunItemReader,
	attacher *AAPEventCatchUp,
	cancellers ...AAPRunCancellationApplication,
) (*AAPRunRoutes, error) {
	if authorizer == nil || conversations == nil || runs == nil || reader == nil ||
		items == nil || attacher == nil || len(cancellers) > 1 ||
		(len(cancellers) == 1 && cancellers[0] == nil) {
		return nil, ErrAAPCreateRunInvalid
	}
	var canceller AAPRunCancellationApplication
	if len(cancellers) == 1 {
		canceller = cancellers[0]
	}
	return &AAPRunRoutes{
		authorizer: authorizer, conversations: conversations, runs: runs,
		reader: reader, items: items, attacher: attacher, canceller: canceller,
	}, nil
}

// ConfigureInteractionDecisions enables the optional interaction command
// surface without forcing read-only deployments to construct a mutation
// service. It must be called before route registration.
func (routes *AAPRunRoutes) ConfigureInteractionDecisions(
	decider AAPInteractionDecisionApplication,
) error {
	if routes == nil || decider == nil || routes.decider != nil {
		return ErrAAPInteractionDecisionReqInvalid
	}
	routes.decider = decider
	return nil
}

// ConfigureOutboundAttach marks that the Run application is wired for
// REQUEST_PASSTHROUGH vault attach. Without this, any non-empty
// outboundCredentials body fails closed (no silent drop).
func (routes *AAPRunRoutes) ConfigureOutboundAttach() {
	if routes != nil {
		routes.outboundAttachConfigured = true
	}
}

// ConfigureFiles enables createRun input_file authorization, READY checks,
// RuntimeMultimodal fail-closed (KD-23), and retention promote on success.
func (routes *AAPRunRoutes) ConfigureFiles(
	gate *config.AgentAccessFilesConfig,
	lookup AAPCreateRunFileLookup,
) error {
	if routes == nil || gate == nil || lookup == nil || routes.filesGate != nil {
		return ErrAAPCreateRunInvalid
	}
	routes.filesGate = gate
	routes.fileLookup = lookup
	return nil
}

func (routes *AAPRunRoutes) RegisterAgentAccessV1(v1 AgentAccessV1Routes) {
	base := "/workspaces/:wid/agents/:aid/runs"
	v1.Protected.POST(base, routes.createRun)
	v1.Protected.GET(base+"/:rid", routes.getRun)
	v1.Protected.GET(base+"/:rid/events", routes.getRunEvents)
	if routes.canceller != nil {
		v1.Protected.POST(base+"/:rid/__command/cancel", routes.cancelRun)
	}
	if routes.decider != nil {
		v1.Protected.POST(base+"/:rid/interactions/:iid/__command/decide", routes.decideInteraction)
	}
}

func (routes *AAPRunRoutes) createRun(c *gin.Context) {
	caller, ok := aapBoundPrincipal(c)
	if !ok {
		return
	}
	requestContext, contextOK := RequestContextFrom(c.Request.Context())
	if !contextOK || !isJSONMediaType(c.GetHeader("Content-Type")) {
		RespondError(c, ErrAAPCreateRunInvalid)
		return
	}
	// Strip write-only outboundCredentials before business decode so Token never
	// hits DisallowUnknownFields / metadata / durable request hashing.
	split, splitErr := ReadOutboundCredentialsBody(c)
	if splitErr != nil {
		RespondError(c, mapOutboundEntryError(splitErr))
		return
	}
	defer split.Zero()

	var request AAPCreateRunRequest
	if err := DecodeBusinessJSON(split.BusinessJSON, &request); err != nil {
		RespondError(c, ErrAAPCreateRunInvalid)
		return
	}
	if err := validateCreateRunRequest(request); err != nil {
		RespondError(c, err)
		return
	}
	idempotencyKey := strings.ToLower(strings.TrimSpace(c.GetHeader("Idempotency-Key")))
	if parsed, err := uuid.Parse(idempotencyKey); err != nil || parsed.String() != idempotencyKey {
		RespondError(c, ErrAAPCreateRunInvalid)
		return
	}
	if request.Stream && !acceptsEventStream(c.GetHeader("Accept")) {
		RespondError(c, ErrAAPCreateRunInvalid)
		return
	}
	// Fail closed: never silently drop write-only tokens when attach is not wired.
	creds := split.CredentialsRaw
	split.CredentialsRaw = nil
	if len(creds) > 0 && !routes.outboundAttachConfigured {
		_ = outboundidentity.ZeroCredentialsRaw(creds)
		RespondError(c, outboundidentity.ErrCredentialInvalid)
		return
	}
	scope := aap.ConversationScope{WorkspaceID: c.Param("wid"), AgentID: c.Param("aid")}
	conversationID := strings.TrimSpace(request.ConversationID)
	quotaApplied := false
	if conversationID == "" {
		_, creationAuthorization, authorized := authorizeAAPRequest(
			c, routes.authorizer, agentaccessauth.ActionConversationCreate,
			agentaccessauth.AAPAuthorizationResource{},
		)
		if !authorized {
			_ = outboundidentity.ZeroCredentialsRaw(creds)
			return
		}
		if !enforceAAPCommandQuota(
			c, routes.quota, agentaccess.QuotaRunCreate, caller, creationAuthorization,
		) {
			_ = outboundidentity.ZeroCredentialsRaw(creds)
			return
		}
		quotaApplied = true
		created, err := routes.conversations.Create(c.Request.Context(), aap.CreateConversationInput{
			Scope: scope, Principal: caller, Authorization: creationAuthorization,
			Title: "", IdempotencyKey: idempotencyKey,
		})
		if err != nil {
			_ = outboundidentity.ZeroCredentialsRaw(creds)
			RespondError(c, err)
			return
		}
		conversationID = created.Conversation.ID
	}
	_, authorization, authorized := authorizeAAPRequest(
		c, routes.authorizer, agentaccessauth.ActionRunCreate,
		agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceConversation, ID: conversationID,
		},
	)
	if !authorized {
		_ = outboundidentity.ZeroCredentialsRaw(creds)
		return
	}
	if !quotaApplied && !enforceAAPCommandQuota(
		c, routes.quota, agentaccess.QuotaRunCreate, caller, authorization,
	) {
		_ = outboundidentity.ZeroCredentialsRaw(creds)
		return
	}
	parts := aapRunContentParts(request.Input)
	// KD-IR-7: files HTTP gate is required for any input_file. Image/mixed
	// still need RuntimeMultimodal; document-only does not.
	if aap.HasInputFilePart(parts) {
		if routes.filesGate == nil || !routes.filesGate.AllowsWorkspace(scope.WorkspaceID) {
			_ = outboundidentity.ZeroCredentialsRaw(creds)
			RespondError(c, aap.ErrFileRuntimeUnavailable)
			return
		}
		if err := routes.authorizeCreateRunFiles(c, caller, scope, parts); err != nil {
			_ = outboundidentity.ZeroCredentialsRaw(creds)
			if !errors.Is(err, errAAPCreateRunAuthResponded) {
				RespondError(c, err)
			}
			return
		}
		if createRunHasVisionInputFile(parts) && !routes.filesGate.RuntimeMultimodal {
			_ = outboundidentity.ZeroCredentialsRaw(creds)
			RespondError(c, aap.ErrFileRuntimeUnavailable)
			return
		}
	}
	result, err := routes.runs.Create(c.Request.Context(), aap.CreateRunInput{
		Scope: scope, ConversationID: conversationID,
		Text: aapRunInputText(request.Input), Parts: parts,
		Metadata: cloneRunMetadata(request.Metadata), IdempotencyKey: idempotencyKey,
		TraceID: requestContext.TraceID, Principal: caller, Authorization: authorization,
		OutboundCredentialsRaw: creds,
	})
	_ = outboundidentity.ZeroCredentialsRaw(creds)
	if err != nil {
		RespondError(c, err)
		return
	}
	// KD-16: first successful createRun referencing READY files may promote retention.
	if aap.HasInputFilePart(parts) && routes.fileLookup != nil {
		for _, part := range parts {
			if part.Type != "input_file" {
				continue
			}
			_ = routes.fileLookup.PromoteRetentionOnReference(
				c.Request.Context(), scope.WorkspaceID, part.FileID,
			)
		}
	}
	transportResult := AAPCreateRunResult{
		RunID: result.Run.ID, ConversationID: result.Run.SessionID,
		AcceptedEvent: result.AcceptedEvent, Idempotent: result.Idempotent,
	}
	if validateCreateRunResult(AAPCreateRunScope{
		WorkspaceID: scope.WorkspaceID, AgentID: scope.AgentID,
	}, transportResult, sse.NewEncoder()) != nil {
		RespondError(c, ErrAAPRunCreationInvalid)
		return
	}
	if !request.Stream {
		c.Header("Location", aapRunLink(scope, result.Run.ID))
		c.JSON(http.StatusAccepted, gin.H{
			"run": aapAcceptedRunDTO(result.Run),
			"links": gin.H{
				"self": aapRunLink(scope, result.Run.ID),
				"events": aapRunEventsLink(AAPCreateRunScope{
					WorkspaceID: scope.WorkspaceID, AgentID: scope.AgentID,
				}, result.Run.ID),
			},
			"idempotent": result.Idempotent,
		})
		return
	}
	binding, err := agentaccessauth.StreamBindingFromAuthorization(caller, authorization.Snapshot)
	if err != nil {
		RespondError(c, err)
		return
	}
	routes.attacher.StreamFrom(c, protocolevent.RunScope{
		WorkspaceID: scope.WorkspaceID, AgentID: scope.AgentID,
		ConversationID: conversationID, RunID: result.Run.ID,
	}, 0, AAPStreamSession{
		Connection: sse.ConnectionIdentity{
			ClientID: binding.ClientID, SubjectID: binding.SubjectID, RunID: result.Run.ID,
		},
		Authorization: &binding,
	})
}

func (routes *AAPRunRoutes) getRun(c *gin.Context) {
	_, _, ok := authorizeAAPRequest(
		c, routes.authorizer, agentaccessauth.ActionRunRead,
		agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceRun, ID: strings.TrimSpace(c.Param("rid")),
		},
	)
	if !ok {
		return
	}
	run, err := routes.reader.GetAgentRun(c.Request.Context(), c.Param("wid"), c.Param("rid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	if run.WorkspaceID != c.Param("wid") || run.AgentID != c.Param("aid") {
		RespondError(c, agentaccessauth.ErrAAPAuthorizationNotVisible)
		return
	}
	items, err := routes.items.ListForRun(
		c.Request.Context(), c.Param("wid"), c.Param("aid"), run.ID,
	)
	if err != nil {
		RespondError(c, err)
		return
	}
	response, err := aapRunResourceDTOFor(run, items)
	if err != nil {
		RespondError(c, err)
		return
	}
	etag, err := aapRunResourceETag(response)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, no-cache")
	if ifNoneMatch(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (routes *AAPRunRoutes) getRunEvents(c *gin.Context) {
	if !acceptsEventStream(c.GetHeader("Accept")) || c.Request.URL.Query().Has("access_token") {
		RespondError(c, ErrAAPRunEventsRequestInvalid)
		return
	}
	caller, authorization, ok := authorizeAAPRequest(
		c, routes.authorizer, agentaccessauth.ActionEventRead,
		agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceRun, ID: strings.TrimSpace(c.Param("rid")),
		},
	)
	if !ok {
		return
	}
	if !enforceAAPCommandQuota(
		c, routes.quota, agentaccess.QuotaEventStream, caller, authorization,
	) {
		return
	}
	run, err := routes.reader.GetAgentRun(c.Request.Context(), c.Param("wid"), c.Param("rid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	if run.WorkspaceID != c.Param("wid") || run.AgentID != c.Param("aid") {
		RespondError(c, agentaccessauth.ErrAAPAuthorizationNotVisible)
		return
	}
	binding, err := agentaccessauth.StreamBindingFromAuthorization(caller, authorization.Snapshot)
	if err != nil {
		RespondError(c, err)
		return
	}
	routes.attacher.Stream(c, protocolevent.RunScope{
		WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
		ConversationID: run.SessionID, RunID: run.ID,
	}, AAPStreamSession{
		Connection: sse.ConnectionIdentity{
			ClientID: binding.ClientID, SubjectID: binding.SubjectID, RunID: run.ID,
		},
		Authorization: &binding,
	})
}

func (routes *AAPRunRoutes) cancelRun(c *gin.Context) {
	runID := strings.ToLower(strings.TrimSpace(c.Param("rid")))
	if parsed, err := uuid.Parse(runID); err != nil || parsed.String() != runID {
		RespondError(c, aap.ErrRunCancelInvalid)
		return
	}
	caller, authorization, authorized := authorizeAAPRequest(
		c, routes.authorizer, agentaccessauth.ActionRunCancel,
		agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceRun, ID: runID,
		},
	)
	if !authorized {
		return
	}
	idempotencyKey := strings.ToLower(strings.TrimSpace(c.GetHeader("Idempotency-Key")))
	if parsed, err := uuid.Parse(idempotencyKey); err != nil || parsed.String() != idempotencyKey {
		RespondError(c, aap.ErrRunCancelInvalid)
		return
	}
	if c.Request.ContentLength != 0 {
		if !isJSONMediaType(c.GetHeader("Content-Type")) {
			RespondError(c, aap.ErrRunCancelInvalid)
			return
		}
		var request struct{}
		if decodeJSON(c, &request) != nil {
			RespondError(c, aap.ErrRunCancelInvalid)
			return
		}
	}
	if !enforceAAPCommandQuota(
		c, routes.quota, agentaccess.QuotaRunCancel, caller, authorization,
	) {
		return
	}
	result, err := routes.canceller.Cancel(c.Request.Context(), aap.CancelRunInput{
		Scope: aap.ConversationScope{WorkspaceID: c.Param("wid"), AgentID: c.Param("aid")},
		RunID: runID, IdempotencyKey: idempotencyKey,
		Principal: caller, Authorization: authorization,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	items, err := routes.items.ListForRun(
		c.Request.Context(), c.Param("wid"), c.Param("aid"), result.Run.ID,
	)
	if err != nil {
		RespondError(c, err)
		return
	}
	resource, err := aapRunResourceDTOFor(result.Run, items)
	if err != nil {
		RespondError(c, err)
		return
	}
	etag, err := aapRunResourceETag(resource)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.Header("ETag", etag)
	c.JSON(http.StatusOK, gin.H{"run": resource, "idempotent": result.Idempotent})
}

type aapInteractionDecisionRequest struct {
	Decision string `json:"decision"`
}

func (routes *AAPRunRoutes) decideInteraction(c *gin.Context) {
	runID := strings.ToLower(strings.TrimSpace(c.Param("rid")))
	interactionID := strings.ToLower(strings.TrimSpace(c.Param("iid")))
	if !canonicalHTTPUUID(runID) || !canonicalHTTPUUID(interactionID) ||
		!isJSONMediaType(c.GetHeader("Content-Type")) {
		RespondError(c, ErrAAPInteractionDecisionReqInvalid)
		return
	}
	caller, authorization, authorized := authorizeAAPRequest(
		c, routes.authorizer, agentaccessauth.ActionInteractionDecide,
		agentaccessauth.AAPAuthorizationResource{
			Type: agentaccessauth.ResourceInteraction, ID: interactionID,
		},
	)
	if !authorized {
		return
	}
	var request aapInteractionDecisionRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, ErrAAPInteractionDecisionReqInvalid)
		return
	}
	request.Decision = strings.ToLower(strings.TrimSpace(request.Decision))
	if request.Decision != execution.InteractionDecisionApprove &&
		request.Decision != execution.InteractionDecisionDecline &&
		request.Decision != execution.InteractionDecisionCancel {
		RespondError(c, ErrAAPInteractionDecisionReqInvalid)
		return
	}
	idempotencyKey := strings.ToLower(strings.TrimSpace(c.GetHeader("Idempotency-Key")))
	if !canonicalHTTPUUID(idempotencyKey) {
		RespondError(c, ErrAAPInteractionDecisionReqInvalid)
		return
	}
	expectedVersion, err := parseAAPInteractionETag(c.GetHeader("If-Match"))
	if err != nil {
		RespondError(c, err)
		return
	}
	if !enforceAAPCommandQuota(
		c, routes.quota, agentaccess.QuotaInteractionDecide, caller, authorization,
	) {
		return
	}
	result, err := routes.decider.Decide(c.Request.Context(), aap.DecideInteractionInput{
		Scope: aap.ConversationScope{WorkspaceID: c.Param("wid"), AgentID: c.Param("aid")},
		RunID: runID, InteractionID: interactionID, Decision: request.Decision,
		ExpectedVersion: expectedVersion, IdempotencyKey: idempotencyKey,
		Principal: caller, Authorization: authorization,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	if result.Interaction.ID != interactionID || result.Interaction.RunID != runID ||
		result.Interaction.Version < 1 ||
		result.Interaction.Status == protocolevent.InteractionStatusPending {
		RespondError(c, ErrAAPInteractionDecisionReqInvalid)
		return
	}
	c.Header("ETag", fmt.Sprintf(`"%d"`, result.Interaction.Version))
	c.Header("Cache-Control", "private, no-cache")
	c.JSON(http.StatusOK, gin.H{
		"interaction": result.Interaction,
		"idempotent":  result.Idempotent,
		"links": gin.H{
			"run": aapRunLink(aap.ConversationScope{
				WorkspaceID: c.Param("wid"), AgentID: c.Param("aid"),
			}, runID),
			"events": aapRunEventsLink(AAPCreateRunScope{
				WorkspaceID: c.Param("wid"), AgentID: c.Param("aid"),
			}, runID),
		},
	})
}

func parseAAPInteractionETag(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' ||
		strings.Contains(value, ",") || strings.HasPrefix(value, "W/") {
		return 0, ErrAAPInteractionDecisionReqInvalid
	}
	version, err := strconv.ParseInt(value[1:len(value)-1], 10, 64)
	if err != nil || version < 1 || strconv.FormatInt(version, 10) != value[1:len(value)-1] {
		return 0, ErrAAPInteractionDecisionReqInvalid
	}
	return version, nil
}

func canonicalHTTPUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func aapBoundPrincipal(c *gin.Context) (agentaccessauth.AAPAccessTokenPrincipal, bool) {
	caller, ok := AAPPrincipalFrom(c.Request.Context())
	if !ok {
		RespondError(c, ErrUnauthenticated)
		return agentaccessauth.AAPAccessTokenPrincipal{}, false
	}
	if caller.WorkspaceID != c.Param("wid") || caller.AgentID != c.Param("aid") {
		RespondError(c, agentaccessauth.ErrAAPAuthorizationNotVisible)
		return agentaccessauth.AAPAccessTokenPrincipal{}, false
	}
	return caller, true
}

type aapRunResourceDTO struct {
	Object         string            `json:"object"`
	ID             string            `json:"id"`
	ConversationID string            `json:"conversationId"`
	AgentID        string            `json:"agentId"`
	Status         string            `json:"status"`
	Version        int64             `json:"version"`
	Error          *aapRunErrorDTO   `json:"error,omitempty"`
	StartedAt      time.Time         `json:"startedAt"`
	CompletedAt    *time.Time        `json:"completedAt,omitempty"`
	Items          []json.RawMessage `json:"items"`
	Links          aapRunLinksDTO    `json:"links"`
}

type aapRunErrorDTO struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type aapRunLinksDTO struct {
	Events string `json:"events"`
}

func aapAcceptedRunDTO(run execution.AgentRun) aapRunResourceDTO {
	return aapRunResourceDTO{
		Object: "run", ID: run.ID, ConversationID: run.SessionID, AgentID: run.AgentID,
		Status: "accepted", Version: run.LockVersion, StartedAt: run.StartedAt.UTC(),
		Items: []json.RawMessage{}, Links: aapRunLinksDTO{Events: aapRunEventsLink(
			AAPCreateRunScope{WorkspaceID: run.WorkspaceID, AgentID: run.AgentID}, run.ID,
		)},
	}
}

func aapRunResourceDTOFor(
	run execution.AgentRun,
	projections []protocolevent.RunItemProjection,
) (aapRunResourceDTO, error) {
	status := publicAAPRunStatus(run.Status)
	if status == "unknown" {
		return aapRunResourceDTO{}, ErrAAPRunCreationInvalid
	}
	items := make([]json.RawMessage, len(projections))
	for index, projection := range projections {
		if projection.WorkspaceID != run.WorkspaceID || projection.AgentID != run.AgentID ||
			projection.RunID != run.ID || len(projection.Snapshot) == 0 ||
			!json.Valid(projection.Snapshot) {
			return aapRunResourceDTO{}, ErrAAPRunCreationInvalid
		}
		items[index] = append(json.RawMessage(nil), projection.Snapshot...)
	}
	var completedAt *time.Time
	if run.FinishedAt != nil {
		value := run.FinishedAt.UTC()
		completedAt = &value
	}
	var publicError *aapRunErrorDTO
	if status == "failed" {
		if !executionStableErrorCode(run.ErrorCode) {
			return aapRunResourceDTO{}, ErrAAPRunCreationInvalid
		}
		publicError = &aapRunErrorDTO{
			Code: run.ErrorCode, Message: "Run failed.", Retryable: false,
		}
	}
	return aapRunResourceDTO{
		Object: "run", ID: run.ID, ConversationID: run.SessionID, AgentID: run.AgentID,
		Status: status, Version: run.LockVersion, Error: publicError,
		StartedAt: run.StartedAt.UTC(), CompletedAt: completedAt, Items: items,
		Links: aapRunLinksDTO{Events: aapRunEventsLink(
			AAPCreateRunScope{WorkspaceID: run.WorkspaceID, AgentID: run.AgentID}, run.ID,
		)},
	}, nil
}

func aapRunResourceETag(value aapRunResourceDTO) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return `"sha256:` + hex.EncodeToString(digest[:]) + `"`, nil
}

func aapRunInputText(items []AAPRunInputItem) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items[0].Content))
	for _, part := range items[0].Content {
		if strings.TrimSpace(part.Type) == "text" {
			parts = append(parts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(parts, "\n")
}

// authorizeCreateRunFiles enforces file.read + READY for each input_file (design §5.7 / IC-06).
// Cross-workspace or invisible files surface as FILE_NOT_FOUND (conceal).
func (routes *AAPRunRoutes) authorizeCreateRunFiles(
	c *gin.Context,
	caller agentaccessauth.AAPAccessTokenPrincipal,
	scope aap.ConversationScope,
	parts []aap.RunContentPart,
) error {
	if routes == nil || routes.authorizer == nil || routes.fileLookup == nil || c == nil {
		return aap.ErrFileRuntimeUnavailable
	}
	seen := make(map[string]struct{}, len(parts))
	for i := range parts {
		if parts[i].Type != "input_file" {
			continue
		}
		fileID := strings.ToLower(strings.TrimSpace(parts[i].FileID))
		if fileID == "" {
			return ErrAAPCreateRunInvalid
		}
		if _, dup := seen[fileID]; dup {
			continue
		}
		seen[fileID] = struct{}{}
		_, _, ok := authorizeAAPRequest(
			c, routes.authorizer, agentaccessauth.ActionFileRead,
			agentaccessauth.AAPAuthorizationResource{
				Type: agentaccessauth.ResourceFile, ID: fileID,
			},
		)
		if !ok {
			// authorizeAAPRequest already wrote the response; return a sentinel so
			// createRun does not double-RespondError.
			return errAAPCreateRunAuthResponded
		}
		file, err := routes.fileLookup.GetFile(c.Request.Context(), scope.WorkspaceID, fileID)
		if err != nil {
			if errors.Is(err, aapfile.ErrNotFound) {
				return aapfile.ErrNotFound
			}
			return err
		}
		if file.WorkspaceID != scope.WorkspaceID || file.AgentID != scope.AgentID {
			return aapfile.ErrNotFound
		}
		switch file.Status {
		case aapfile.StatusReady:
			// Echo media type when client omitted it (server may attach declared/detected).
			if parts[i].MediaType == "" {
				if file.DetectedMediaType != nil && *file.DetectedMediaType != "" {
					parts[i].MediaType = *file.DetectedMediaType
				} else {
					parts[i].MediaType = file.DeclaredMediaType
				}
			}
		case aapfile.StatusFailed:
			return fmt.Errorf("%w: %s", aapfile.ErrFailed, aapfile.ErrorCodeProcessingFailed)
		case aapfile.StatusProcessing, aapfile.StatusUploaded, aapfile.StatusPendingUpload:
			return aapfile.ErrNotReady
		default:
			return aapfile.ErrNotReady
		}
	}
	return nil
}

func createRunHasVisionInputFile(parts []aap.RunContentPart) bool {
	for _, part := range parts {
		if part.Type == "input_file" && aapfile.IsVisionMediaType(part.MediaType) {
			return true
		}
	}
	return false
}

// errAAPCreateRunAuthResponded signals that authorizeAAPRequest already aborted the response.
var errAAPCreateRunAuthResponded = errors.New("AAP create Run authorization already responded")

func aapRunLink(scope aap.ConversationScope, runID string) string {
	return "/api/agent-access/v1/workspaces/" + scope.WorkspaceID +
		"/agents/" + scope.AgentID + "/runs/" + runID
}

func executionStableErrorCode(value string) bool {
	if len(value) < 3 || len(value) > 64 || value[0] < 'A' || value[0] > 'Z' {
		return false
	}
	for _, character := range value[1:] {
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}
