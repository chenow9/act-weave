package a2agateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"actweave/backend/internal/agentdelegation"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"github.com/a2aproject/a2a-go/a2asrv/eventqueue"
	"github.com/google/uuid"
)

// inboundFreezeMaterializer freezes root agent snapshots without writing agent_runs.
// Used before the claim TX so catalog DB reads do not share the claim connection.
type inboundFreezeMaterializer interface {
	MaterializeFreeze(ctx context.Context, req InboundRunRequest) (InboundFreeze, error)
}

// inboundTxAuthorityPreparer inserts the authority agent_run on the claim transaction.
type inboundTxAuthorityPreparer interface {
	PrepareRunInTx(ctx context.Context, tx *sql.Tx, req InboundRunRequest, freeze InboundFreeze) (runID string, err error)
}

// AgentRunner starts an internal AgentRun for an inbound A2A message.
// PrepareRun must create a durable agent_run before audit prewrite;
// ExecuteRun dispatches work only after audit succeeds.
//
// InterruptRun cancels in-process work only and MUST NOT persist agent_run status.
// CancelRun may durable-cancel a standalone candidate run (unused claim) that is not
// part of an inbound four-object terminal transaction. Inbound cancel durable state
// is owned exclusively by AtomicInboundCancel / FencedInboundTerminal /
// AtomicUnownedInboundCleanup.
type AgentRunner interface {
	PrepareRun(ctx context.Context, req InboundRunRequest) (runID string, err error)
	ExecuteRun(ctx context.Context, req InboundRunRequest, runID string) (InboundRunResult, error)
	// InterruptRun stops in-flight execution without writing agent_runs.
	InterruptRun(ctx context.Context, workspaceID, runID string) error
	// CancelRun durable-cancels a run outside fenced inbound terminal TX (candidates only).
	CancelRun(ctx context.Context, workspaceID, runID string) error
}

// InboundRunRequest is the mapped A2A → internal run request.
type InboundRunRequest struct {
	WorkspaceID     string
	AgentID         string
	UserText        string
	ExternalTaskID  string
	ExternalContext string
	ExternalMessage string
	TraceID         string
	ActorType       string
	ActorID         string
	// IdempotencyKey for external retries.
	IdempotencyKey string
}

// InboundRunResult is the internal run outcome.
type InboundRunResult struct {
	RunID         string
	DelegationID  string
	AssistantText string
	Status        string
	ErrorCode     string
	ErrorMessage  string
}

// AuthChecker validates inbound A2A credentials for an exposure.
type AuthChecker interface {
	// Authorize returns actor type/id or error. Empty Authorization when AuthMode=NONE is ok.
	Authorize(ctx context.Context, r *http.Request, exp Exposure) (actorType, actorID string, err error)
}

// HeaderPresenceAuth only checks Authorization presence for AGENT_ACCESS (tests).
// Production must use AgentAccessAuth with real token verification.
type HeaderPresenceAuth struct{}

func (HeaderPresenceAuth) Authorize(_ context.Context, r *http.Request, exp Exposure) (string, string, error) {
	if exp.AuthMode == AuthModeNone {
		return "SYSTEM", exp.ID, nil
	}
	if strings.TrimSpace(r.Header.Get("Authorization")) == "" {
		return "", "", ErrAuthRejected
	}
	return "SERVICE_PRINCIPAL", exp.ID, nil
}

// InboundGateway serves A2A JSON-RPC for allowlisted agents.
type InboundGateway struct {
	repo    *Repository
	audit   agentdelegation.AuditWriter
	runner  AgentRunner
	auth    AuthChecker
	baseURL string
	// LeaseTTL is the execution ownership window (default 2m). Injectable for tests.
	LeaseTTL time.Duration
	mu       sync.Mutex
	// a2a task id → internal run id
	activeRun map[string]string
}

// InboundGatewayOption configures optional gateway behavior.
type InboundGatewayOption func(*InboundGateway)

// WithLeaseTTL sets inbound execution lease duration (must be >0).
func WithLeaseTTL(d time.Duration) InboundGatewayOption {
	return func(g *InboundGateway) {
		if d > 0 {
			g.LeaseTTL = d
		}
	}
}

func NewInboundGateway(
	repo *Repository,
	audit agentdelegation.AuditWriter,
	runner AgentRunner,
	baseURL string,
	auth AuthChecker,
	opts ...InboundGatewayOption,
) (*InboundGateway, error) {
	if repo == nil || audit == nil || runner == nil {
		return nil, ErrInvalid
	}
	// Auth is required fail-closed: never silently fall back to presence-only auth.
	if auth == nil {
		return nil, fmt.Errorf("%w: AuthChecker is required", ErrInvalid)
	}
	g := &InboundGateway{
		repo: repo, audit: audit, runner: runner, auth: auth,
		baseURL:   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		LeaseTTL:  2 * time.Minute,
		activeRun: map[string]string{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(g)
		}
	}
	return g, nil
}

func (g *InboundGateway) leaseTTL() time.Duration {
	if g != nil && g.LeaseTTL > 0 {
		return g.LeaseTTL
	}
	return 2 * time.Minute
}

// Register mounts card + invoke on net/http ServeMux (also used under Gin wrap).
func (g *InboundGateway) Register(mux *http.ServeMux) {
	if mux == nil || g == nil {
		return
	}
	cardPath := "GET /a2a/workspaces/{wid}/agents/{agentId}" + a2asrv.WellKnownAgentCardPath
	mux.HandleFunc(cardPath, g.handleAgentCard)
	mux.HandleFunc("POST /a2a/workspaces/{wid}/agents/{agentId}/invoke", g.handleInvoke)
	mux.HandleFunc("POST /a2a/workspaces/{wid}/agents/{agentId}/cancel", g.handleCancelHTTP)
}

// ServeHTTP implements http.Handler for mounting under Gin Any routes.
func (g *InboundGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 || parts[0] != "a2a" || parts[1] != "workspaces" || parts[3] != "agents" {
		http.NotFound(w, r)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), pathKey{}, pathVals{
		wid: parts[2], agentID: parts[4],
	}))
	rest := strings.Join(parts[5:], "/")
	switch {
	case r.Method == http.MethodGet && (rest == strings.TrimPrefix(a2asrv.WellKnownAgentCardPath, "/") ||
		rest == ".well-known/agent-card.json"):
		g.handleAgentCard(w, r)
	case r.Method == http.MethodPost && rest == "invoke":
		g.handleInvoke(w, r)
	case r.Method == http.MethodPost && rest == "cancel":
		g.handleCancelHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

type pathKey struct{}
type pathVals struct{ wid, agentID string }

func pathFrom(r *http.Request) (wid, agentID string) {
	if v, ok := r.Context().Value(pathKey{}).(pathVals); ok {
		return v.wid, v.agentID
	}
	return r.PathValue("wid"), r.PathValue("agentId")
}

func (g *InboundGateway) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	workspaceID, agentID := pathFrom(r)
	exp, err := g.repo.GetExposureByAgent(r.Context(), workspaceID, agentID)
	if err != nil || !exp.Enabled {
		http.Error(w, `{"error":"not allowlisted"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(g.buildCard(exp))
}

func (g *InboundGateway) buildCard(exp Exposure) *a2a.AgentCard {
	url := fmt.Sprintf("%s/a2a/workspaces/%s/agents/%s/invoke", g.baseURL, exp.WorkspaceID, exp.AgentID)
	card := &a2a.AgentCard{
		Name: exp.PublicName, Description: exp.PublicDescription, URL: url,
		PreferredTransport: a2a.TransportProtocolJSONRPC,
		DefaultInputModes:  []string{"text"}, DefaultOutputModes: []string{"text"},
		ProtocolVersion: "0.3",
		// Agent own version (exposure version), distinct from protocolVersion.
		Version:      fmt.Sprintf("%d", exp.Version),
		Capabilities: a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{{
			ID: "default", Name: exp.PublicName,
			Description: firstNonEmpty(exp.PublicDescription, "ActWeave agent"),
			Tags:        []string{"actweave"},
		}},
	}
	// Declare security so clients know AGENT_ACCESS requires Bearer credentials.
	switch strings.ToUpper(strings.TrimSpace(exp.AuthMode)) {
	case AuthModeAgentAccess:
		card.SecuritySchemes = a2a.NamedSecuritySchemes{
			a2a.SecuritySchemeName("bearer"): a2a.HTTPAuthSecurityScheme{
				Scheme:       "bearer",
				BearerFormat: "opaque",
				Description:  "ActWeave agent-access bearer token",
			},
		}
		card.Security = []a2a.SecurityRequirements{
			{a2a.SecuritySchemeName("bearer"): a2a.SecuritySchemeScopes{}},
		}
	case AuthModeNone:
		// Explicit empty schemes: unauthenticated exposure (dev/test).
	}
	return card
}

// MaxInboundRequestBytes is the hard ceiling for A2A invoke/cancel request bodies.
const MaxInboundRequestBytes = 1 << 20 // 1 MiB

func (g *InboundGateway) handleInvoke(w http.ResponseWriter, r *http.Request) {
	workspaceID, agentID := pathFrom(r)
	traceRef := uuid.Must(uuid.NewV7()).String()
	// Bound body before JSON-RPC: Content-Length and chunked both become 413
	// when oversize; executor is never constructed on oversize.
	if !boundInboundRequestBody(w, r, MaxInboundRequestBytes, traceRef) {
		return
	}
	exp, err := g.repo.GetExposureByAgent(r.Context(), workspaceID, agentID)
	if err != nil || !exp.Enabled {
		writePublicHTTPError(w, http.StatusNotFound, "A2A_NOT_ALLOWLISTED",
			"agent not allowlisted", traceRef)
		return
	}
	actorType, actorID, err := g.auth.Authorize(r.Context(), r, exp)
	if err != nil {
		writePublicHTTPError(w, http.StatusUnauthorized, "A2A_UNAUTHORIZED",
			"unauthorized", traceRef)
		return
	}
	// Principal-scoped protocol TaskStore so tasks/get and tasks/cancel survive
	// request boundaries, restarts, and multi-replica gateways.
	store, storeErr := NewPostgresTaskStore(g.repo.DB(), workspaceID, exp.ID)
	if storeErr != nil {
		writePublicHTTPError(w, http.StatusInternalServerError, "A2A_TASK_STORE",
			"task store unavailable", traceRef)
		return
	}
	// Bind actor into request context for TaskStore Save/Get/List fail-closed filtering.
	r = r.WithContext(WithTaskActor(r.Context(), actorType, actorID))
	executor := &inboundExecutor{
		gateway: g, exposure: exp,
		workspaceID: workspaceID, agentID: agentID,
		actorType: actorType, actorID: actorID,
	}
	handler := a2asrv.NewHandler(executor, a2asrv.WithTaskStore(store))
	a2asrv.NewJSONRPCHandler(handler).ServeHTTP(w, r)
}

func (g *InboundGateway) handleCancelHTTP(w http.ResponseWriter, r *http.Request) {
	workspaceID, agentID := pathFrom(r)
	traceRef := uuid.Must(uuid.NewV7()).String()
	if !boundInboundRequestBody(w, r, MaxInboundRequestBytes, traceRef) {
		return
	}
	exp, err := g.repo.GetExposureByAgent(r.Context(), workspaceID, agentID)
	if err != nil || !exp.Enabled {
		writePublicHTTPError(w, http.StatusNotFound, "A2A_NOT_ALLOWLISTED",
			"agent not allowlisted", traceRef)
		return
	}
	actorType, actorID, err := g.auth.Authorize(r.Context(), r, exp)
	if err != nil {
		writePublicHTTPError(w, http.StatusUnauthorized, "A2A_UNAUTHORIZED",
			"unauthorized", traceRef)
		return
	}
	var body struct {
		TaskID string `json:"taskId"`
	}
	if decErr := json.NewDecoder(r.Body).Decode(&body); decErr != nil {
		writePublicHTTPError(w, http.StatusBadRequest, "A2A_INVALID",
			"invalid request body", traceRef)
		return
	}
	taskID := strings.TrimSpace(body.TaskID)
	if taskID == "" {
		writePublicHTTPError(w, http.StatusBadRequest, "A2A_INVALID",
			"taskId required", traceRef)
		return
	}
	// Durable cancel: exposure + principal + external task; never cancel another actor's authority.
	if cerr := g.CancelInbound(r.Context(), workspaceID, exp.ID, actorType, actorID, taskID); cerr != nil {
		// Spec-safe: unknown/foreign task → not found (no existence leak).
		if errors.Is(cerr, ErrNotFound) {
			writePublicHTTPError(w, http.StatusNotFound, "A2A_TASK_NOT_FOUND",
				"task not found", traceRef)
			return
		}
		// Never echo internal cancel/DB/lease details to the client.
		writePublicHTTPError(w, http.StatusInternalServerError, "A2A_CANCEL_FAILED",
			"cancel failed", traceRef)
		return
	}
	runID := ""
	if task, gerr := g.repo.GetInboundTaskByExposureTask(r.Context(), workspaceID, exp.ID, actorType, actorID, taskID); gerr == nil {
		runID = task.RunID
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true, "taskId": taskID, "runId": runID, "exposureId": exp.ID, "traceRef": traceRef,
	})
}

// boundInboundRequestBody reads at most max+1 bytes (works for Content-Length and
// chunked). Oversize → HTTP 413 with stable public error; returns false.
// On success, replaces r.Body with a bytes reader of the bounded payload.
func boundInboundRequestBody(w http.ResponseWriter, r *http.Request, max int64, traceRef string) bool {
	if r == nil {
		writePublicHTTPError(w, http.StatusBadRequest, "A2A_INVALID", "invalid request", traceRef)
		return false
	}
	if r.ContentLength > max {
		writePublicHTTPError(w, http.StatusRequestEntityTooLarge, "A2A_BODY_TOO_LARGE",
			"request body too large", traceRef)
		return false
	}
	if r.Body == nil {
		r.Body = io.NopCloser(bytes.NewReader(nil))
		r.ContentLength = 0
		return true
	}
	defer r.Body.Close()
	// max+1 detect oversize for chunked / unknown Content-Length.
	limited := io.LimitReader(r.Body, max+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		writePublicHTTPError(w, http.StatusBadRequest, "A2A_INVALID",
			"invalid request body", traceRef)
		return false
	}
	if int64(len(data)) > max {
		writePublicHTTPError(w, http.StatusRequestEntityTooLarge, "A2A_BODY_TOO_LARGE",
			"request body too large", traceRef)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	// Clear Transfer-Encoding so downstream sees a fixed body.
	r.Header.Del("Transfer-Encoding")
	return true
}

// writePublicHTTPError emits a stable client-safe JSON error (no internal details).
func writePublicHTTPError(w http.ResponseWriter, status int, code, message, traceRef string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": code, "message": message, "traceRef": traceRef,
	})
}

// publicAgentText is the only allowed shape for agent-role texts written to the
// A2A event queue / JSON-RPC result path. Never include SQL/DB/audit/outbox/lease causes.
func publicAgentText(code, message, traceRef string) string {
	if strings.TrimSpace(traceRef) == "" {
		traceRef = uuid.Must(uuid.NewV7()).String()
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "request failed"
	}
	code = strings.TrimSpace(code)
	if code == "" {
		code = "A2A_FAILED"
	}
	return fmt.Sprintf("%s: %s (traceRef=%s)", code, message, traceRef)
}

func writePublicAgent(ctx context.Context, q eventqueue.Queue, code, message, traceRef string) error {
	return q.Write(ctx, a2a.NewMessage(a2a.MessageRoleAgent, a2a.TextPart{
		Text: publicAgentText(code, message, traceRef),
	}))
}

// writePublicAgentForTask attaches the a2asrv TaskID/ContextID so official
// message/send responses carry the server-minted TaskID (alias proof surface).
func writePublicAgentForTask(ctx context.Context, q eventqueue.Queue, reqCtx *a2asrv.RequestContext, code, message, traceRef string) error {
	text := publicAgentText(code, message, traceRef)
	if reqCtx != nil {
		return q.Write(ctx, a2a.NewMessageForTask(a2a.MessageRoleAgent, reqCtx, a2a.TextPart{Text: text}))
	}
	return q.Write(ctx, a2a.NewMessage(a2a.MessageRoleAgent, a2a.TextPart{Text: text}))
}

func writeAgentTextForTask(ctx context.Context, q eventqueue.Queue, reqCtx *a2asrv.RequestContext, text string) error {
	if reqCtx != nil {
		return q.Write(ctx, a2a.NewMessageForTask(a2a.MessageRoleAgent, reqCtx, a2a.TextPart{Text: text}))
	}
	return q.Write(ctx, a2a.NewMessage(a2a.MessageRoleAgent, a2a.TextPart{Text: text}))
}

// inboundExternalAgentRef builds the durable external actor identity for inbound roots.
// Format: service-principal:<actorID> (USER actors use user:<id>).
func inboundExternalAgentRef(actorType, actorID string) string {
	actorID = strings.TrimSpace(actorID)
	switch strings.ToUpper(strings.TrimSpace(actorType)) {
	case "USER":
		return "user:" + actorID
	case "SYSTEM":
		return "system:" + actorID
	default:
		return "service-principal:" + actorID
	}
}

type inboundExecutor struct {
	gateway              *InboundGateway
	exposure             Exposure
	workspaceID, agentID string
	actorType, actorID   string
}

func (e *inboundExecutor) Execute(ctx context.Context, reqCtx *a2asrv.RequestContext, q eventqueue.Queue) error {
	// Ensure TaskStore operations in this execution see the authenticated principal.
	ctx = WithTaskActor(ctx, e.actorType, e.actorID)

	text, extMsgID, extCtx, extTask := "", "", "", ""
	if reqCtx != nil && reqCtx.Message != nil {
		text = messageText(reqCtx.Message)
		extMsgID = string(reqCtx.Message.ID)
		extCtx = string(reqCtx.Message.ContextID)
	}
	if reqCtx != nil {
		extTask = string(reqCtx.TaskID)
	}
	if strings.TrimSpace(text) == "" {
		return writeFinalTaskStatus(ctx, q, reqCtx, a2a.TaskStateFailed, "empty message")
	}

	// Persist submitted → working so non-blocking clients get a TaskID and
	// cross-request GetTask/CancelTask see a durable RUNNING protocol task.
	if err := writeTaskStatus(ctx, q, reqCtx, a2a.TaskStateSubmitted, nil, false); err != nil {
		return err
	}
	if err := writeTaskStatus(ctx, q, reqCtx, a2a.TaskStateWorking, nil, false); err != nil {
		return err
	}

	// Durable external idempotency: exposure + principal + contextId + messageId when messageId
	// is present (A2A message semantics). Does NOT depend on server-minted taskId —
	// two independent gateways may each generate a different taskId for the same
	// client retry and still hit one durable task/run/delegation.
	extKey := ExternalIdempotencyKey(extTask, extCtx, extMsgID)
	if extKey == "" {
		// No external ids: still durable-unique per request to avoid collisions.
		extKey = "ephemeral:" + uuid.Must(uuid.NewV7()).String()
	}
	bodyHash := RequestBodyHash(text)
	traceRef := uuid.Must(uuid.NewV7()).String()

	// Claim-first under advisory lock: prepare agent_run only when this key is new.
	// Concurrent same-body retries and body-hash conflicts never create shadow runs
	// (no prepare → no CANCELLED candidate traces polluting audit stats).
	runReq := InboundRunRequest{
		WorkspaceID: e.workspaceID, AgentID: e.agentID, UserText: text,
		ExternalTaskID: extTask, ExternalContext: extCtx, ExternalMessage: extMsgID,
		TraceID:   uuid.Must(uuid.NewV7()).String(),
		ActorType: e.actorType, ActorID: e.actorID, IdempotencyKey: extKey,
	}
	// Freeze catalog outside the claim TX so MaxOpenConns=1 cannot deadlock while
	// the claim advisory lock holds the pool connection. Durable agent_run insert
	// happens on the same TX as inbound task + alias (no orphan on rollback).
	var freeze InboundFreeze
	var freezeErr error
	if fr, ok := e.gateway.runner.(inboundFreezeMaterializer); ok {
		freeze, freezeErr = fr.MaterializeFreeze(ctx, runReq)
	}
	task, taskReplay, claimErr := e.gateway.repo.ClaimInboundTaskWithPrepare(ctx, InboundTask{
		WorkspaceID: e.workspaceID, ExposureID: e.exposure.ID, AgentID: e.agentID,
		ActorType: e.actorType, ActorID: e.actorID,
		ExternalKey: extKey, ExternalTaskID: extTask, ExternalContextID: extCtx,
		ExternalMessageID: extMsgID, RequestHash: bodyHash, Status: "RUNNING",
	}, func(c context.Context, tx *sql.Tx) (string, error) {
		if freezeErr != nil {
			return "", freezeErr
		}
		if pr, ok := e.gateway.runner.(inboundTxAuthorityPreparer); ok {
			return pr.PrepareRunInTx(c, tx, runReq, freeze)
		}
		// Fail closed: cross-connection PrepareRun under claim lock is not allowed.
		return "", fmt.Errorf("%w: runner must support transactional authority prepare", ErrUnsupported)
	})
	if claimErr != nil {
		code, msg := "A2A_CLAIM_FAILED", "inbound claim failed"
		if errors.Is(claimErr, ErrConflict) {
			code, msg = "A2A_CLAIM_CONFLICT", "request body does not match durable idempotent record"
		} else if strings.Contains(strings.ToLower(claimErr.Error()), "prepare") || strings.Contains(strings.ToLower(claimErr.Error()), "freeze") {
			code, msg = "A2A_PREPARE_FAILED", "agent run prepare failed"
		}
		// Keep internalCause out of client text; log-friendly detail is in claimErr for tests/ops.
		_ = claimErr
		return writeFinalTaskStatus(ctx, q, reqCtx, a2a.TaskStateFailed, publicAgentText(code, msg, traceRef))
	}
	runID := task.RunID
	_ = taskReplay
	mapKey := e.exposure.ID + ":" + e.actorType + ":" + e.actorID + ":" + firstNonEmpty(extTask, task.ExternalTaskID)
	if mapKey != e.exposure.ID+":" {
		e.gateway.mu.Lock()
		e.gateway.activeRun[mapKey] = runID
		e.gateway.mu.Unlock()
		defer func() {
			e.gateway.mu.Lock()
			delete(e.gateway.activeRun, mapKey)
			e.gateway.mu.Unlock()
		}()
	}

	// Delegation idempotency is also based on external key + exposure (not fresh run id).
	toolCallID := firstNonEmpty(extMsgID, extTask, "inbound")
	idem := agentdelegation.IdempotencyKey(
		e.exposure.ID+":"+extKey, toolCallID, e.exposure.Version, e.exposure.ID,
	)
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	inputSummary, _ := json.Marshal(map[string]any{
		"source": "a2agateway.inbound", "protocol": agentdelegation.ProtocolA2A,
		"origin": agentdelegation.OriginExternal, "requestPreview": truncate(text, 500),
		"remoteTaskId": extTask, "remoteContextId": extCtx, "remoteMessageId": extMsgID,
		"callableName": "a2a.inbound", "callerAgentId": e.agentID, "targetAgentId": e.agentID,
		"mode": agentdelegation.ModeTask, "depth": 0, "externalKey": extKey,
	})
	inputPayload, _ := json.Marshal(map[string]any{"request": truncate(text, 8000)})
	// External inbound identity: service-principal:<actorID> (not the invoke URL).
	// Target is the exposed agent; depth stays 0 for EXTERNAL root.
	extRef := inboundExternalAgentRef(e.actorType, e.actorID)
	targetAgentID := e.agentID
	del, delReplay, preErr := e.gateway.audit.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: e.workspaceID, ParentRunID: runID,
		CallerAgentID: e.agentID, TargetAgentID: &targetAgentID, ExternalAgentRef: &extRef,
		Mode: agentdelegation.ModeTask, Protocol: agentdelegation.ProtocolA2A,
		Origin: agentdelegation.OriginExternal, Depth: 0, BindingVersion: e.exposure.Version,
		ToolCallID: toolCallID, IdempotencyKey: idem,
		InputSummary: inputSummary, InputPayload: inputPayload,
		StepID: stepID, AgentID: e.agentID,
	})
	if preErr != nil {
		// Never cleanup on concurrent idempotent races: unique (workspace, key) losers
		// must converge to replay (CreateDelegationAndStep), not destroy the winner.
		// Only identity-mismatch / non-replay conflicts may fail this request without
		// touching winner authority (no cleanup of shared task/run).
		if errors.Is(preErr, agentdelegation.ErrConflict) {
			return writeFinalTaskStatus(ctx, q, reqCtx, a2a.TaskStateFailed,
				publicAgentText("A2A_AUDIT_CONFLICT", "delegation prewrite conflict", traceRef))
		}
		// True prewrite failure with no peer winner: unowned cleanup of this claim.
		_ = e.cleanupFailedPreDispatch(ctx, runID, "", "",
			"audit prewrite failed", preErr, nil, task.ID)
		return writeFinalTaskStatus(ctx, q, reqCtx, a2a.TaskStateFailed,
			publicAgentText("A2A_AUDIT_PREWRITE_FAILED", "audit prewrite failed", traceRef))
	}
	if bindErr := e.gateway.repo.BindInboundTaskDelegation(ctx, e.workspaceID, task.ID, del.ID); bindErr != nil {
		// Pre-lease: if peer already bound same del, treat as progress; else cleanup only
		// when this is a true ownership failure without concurrent winner.
		if !delReplay {
			_ = e.cleanupFailedPreDispatch(ctx, runID, del.ID, firstNonEmpty(del.StepID, stepID),
				"bind inbound delegation failed", bindErr, nil, task.ID)
		}
		return writeFinalTaskStatus(ctx, q, reqCtx, a2a.TaskStateFailed,
			publicAgentText("A2A_BIND_FAILED", "bind inbound delegation failed", traceRef))
	}
	if delReplay && isTerminalDelegation(del.Status) {
		reply := extractResultFromPayload(del.OutputPayload)
		if reply == "" {
			reply = "(idempotent replay)"
		}
		state := mapDelegationStatusToTaskState(del.Status)
		return writeFinalTaskStatus(ctx, q, reqCtx, state, redactSecrets(reply))
	}

	// Recoverable execution lease: first claim or expired-lease reclaim.
	leaseTTL := e.gateway.leaseTTL()
	lease, claimExecErr := e.gateway.repo.ClaimInboundExecution(ctx, e.workspaceID, task.ID, "inbound-"+e.exposure.ID, leaseTTL)
	if claimExecErr != nil {
		return writeFinalTaskStatus(ctx, q, reqCtx, a2a.TaskStateFailed,
			publicAgentText("A2A_EXEC_CLAIM_FAILED", "execution claim failed", traceRef))
	}
	if !lease.Owned {
		if isTerminalDelegation(del.Status) {
			reply := extractResultFromPayload(del.OutputPayload)
			state := mapDelegationStatusToTaskState(del.Status)
			return writeFinalTaskStatus(ctx, q, reqCtx, state, redactSecrets(firstNonEmpty(reply, "(idempotent replay)")))
		}
		// Another worker owns execution: keep protocol Task WORKING for GetTask
		// pollers, but complete this RPC with an agent Message (Message ends the
		// SendMessage execution without terminalizing the durable Task).
		_ = writeTaskStatus(ctx, q, reqCtx, a2a.TaskStateWorking, nil, false)
		return writeAgentTextForTask(ctx, q, reqCtx,
			publicAgentText("A2A_IN_PROGRESS", "in progress; durable ownership held by another worker", traceRef))
	}

	// DispatchAuditor is required for production audit fail-closed (on AuditWriter).
	if e.gateway.audit == nil {
		_ = e.cleanupFailedPreDispatch(ctx, runID, del.ID, firstNonEmpty(del.StepID, stepID),
			"dispatch auditor required", fmt.Errorf("AuditWriter is nil"), &lease, task.ID)
		return writeFinalTaskStatus(ctx, q, reqCtx, a2a.TaskStateFailed,
			publicAgentText("A2A_AUDITOR_REQUIRED", "dispatch auditor required", traceRef))
	}
	if aerr := e.gateway.audit.RecordDispatchAttempt(ctx, e.workspaceID, del.ID); aerr != nil {
		_ = e.cleanupFailedPreDispatch(ctx, runID, del.ID, firstNonEmpty(del.StepID, stepID),
			"dispatch attempt record failed", aerr, &lease, task.ID)
		return writeFinalTaskStatus(ctx, q, reqCtx, a2a.TaskStateFailed,
			publicAgentText("A2A_DISPATCH_ATTEMPT_FAILED", "dispatch attempt record failed", traceRef))
	}

	// Re-validate lease AFTER attempt record (may have exceeded TTL during audit write).
	// Stale owner must not start ExecuteRun / model dispatch.
	if heldErr := e.gateway.repo.AssertInboundExecutionHeld(ctx, e.workspaceID, task.ID, lease.Owner, lease.Token, lease.Generation); heldErr != nil {
		_ = e.cleanupFailedPreDispatch(ctx, runID, del.ID, firstNonEmpty(del.StepID, stepID),
			"lease lost before execute", heldErr, &lease, task.ID)
		return writeFinalTaskStatus(ctx, q, reqCtx, a2a.TaskStateFailed,
			publicAgentText("A2A_LEASE_LOST", "lease lost before execute", traceRef))
	}

	// Lease heartbeat + fence into execute chain (values survive WithoutCancel).
	execCtx, execCancel := context.WithCancel(ctx)
	defer execCancel()
	fence := ExecutionFence{
		WorkspaceID: e.workspaceID, TaskID: task.ID, RunID: runID,
		Owner: lease.Owner, Token: lease.Token, Generation: lease.Generation,
		Repo: e.gateway.repo,
		AssertHeld: func(c context.Context) error {
			return e.gateway.repo.AssertInboundExecutionHeld(c, e.workspaceID, task.ID, lease.Owner, lease.Token, lease.Generation)
		},
	}
	execCtx = WithExecutionFence(execCtx, fence)
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		interval := leaseTTL / 3
		if interval < 50*time.Millisecond {
			interval = 50 * time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-execCtx.Done():
				return
			case <-ticker.C:
				if err := e.gateway.repo.RenewInboundExecutionLease(execCtx, e.workspaceID, task.ID, lease.Owner, lease.Token, leaseTTL); err != nil {
					execCancel()
					return
				}
			}
		}
	}()

	result, execErr := e.gateway.runner.ExecuteRun(execCtx, runReq, runID)
	// Capture cancel/timeout causality BEFORE execCancel(): post-execute cleanup
	// always cancels execCtx and would mis-classify ordinary runner/model errors
	// as CANCELLED if we re-read execCtx.Err() afterward.
	parentDeadline := ctx.Err() == context.DeadlineExceeded
	parentCancelled := ctx.Err() != nil && !parentDeadline
	execDeadline := execCtx.Err() == context.DeadlineExceeded
	// Only treat execCtx cancel as authority cancel when the parent request
	// was cancelled (or deadline). Heartbeat/lease loss also cancel execCtx
	// but runner errors in that race stay FAILED unless parent is done.
	execCancelledByParent := parentCancelled || parentDeadline
	execCancel()
	<-hbDone
	if result.RunID == "" {
		result.RunID = runID
	}

	status := agentdelegation.StatusSucceeded
	errCode, errMsg := "", ""
	// Internal audit may retain a truncated cause; client path never sees raw SQL/lease text.
	internalCause := firstNonEmpty(result.ErrorMessage, errString(execErr))
	if execErr != nil || result.Status == "FAILED" || result.ErrorCode != "" {
		status = agentdelegation.StatusFailed
		errCode = firstNonEmpty(result.ErrorCode, "A2A_INBOUND_FAILED")
		errMsg = publicStatusMessage(errCode)
		// Prefer explicit runner status, then parent/exec deadline, then parent cancel.
		// Do not treat cleanup-induced execCtx cancel as CANCELLED.
		if result.Status == "TIMED_OUT" || execDeadline || parentDeadline {
			status = agentdelegation.StatusTimedOut
			errCode = "A2A_INBOUND_TIMED_OUT"
			errMsg = publicStatusMessage(errCode)
		} else if result.Status == "CANCELLED" || execCancelledByParent {
			status = agentdelegation.StatusCancelled
			errCode = "A2A_INBOUND_CANCELLED"
			errMsg = publicStatusMessage(errCode)
		}
	} else if result.Status == "CANCELLED" {
		status = agentdelegation.StatusCancelled
		errCode = firstNonEmpty(result.ErrorCode, "A2A_INBOUND_CANCELLED")
		errMsg = publicStatusMessage(errCode)
	} else if result.Status == "TIMED_OUT" {
		status = agentdelegation.StatusTimedOut
		errCode = firstNonEmpty(result.ErrorCode, "A2A_INBOUND_TIMED_OUT")
		errMsg = publicStatusMessage(errCode)
	}
	// Audit payload: public message + separate internalCause field (not client-facing).
	outSum, _ := json.Marshal(map[string]any{
		"ok": status == agentdelegation.StatusSucceeded, "status": status,
		"errorCode": errCode, "message": errMsg, "traceRef": traceRef,
		"internalCause": truncate(internalCause, 500),
	})
	outPay, _ := json.Marshal(map[string]any{"result": truncate(result.AssistantText, 16000)})
	runOut, _ := json.Marshal(map[string]any{
		"source": "a2a.inbound", "assistantPreview": truncate(result.AssistantText, 500),
		"errorCode": errCode, "traceRef": traceRef,
	})
	runErrCode := ""
	if status == agentdelegation.StatusFailed {
		runErrCode = firstNonEmpty(errCode, "A2A_INBOUND_FAILED")
	}
	// Single atomic TX: run + task + delegation + step under lease generation.
	finCtx := context.WithoutCancel(ctx)
	finCtx = WithExecutionFence(finCtx, fence)
	fencedIn := FencedTerminalInput{
		WorkspaceID: e.workspaceID, TaskID: task.ID, RunID: runID,
		Owner: lease.Owner, Token: lease.Token, Generation: lease.Generation,
		TaskStatus: status, RunStatus: status,
		ExpectedRunStatus: "RUNNING",
		RunOutputSummary:  runOut, RunErrorCode: runErrCode,
		DelegationID: del.ID, StepID: firstNonEmpty(del.StepID, stepID),
		DelStatus: status, DelOutputSummary: outSum, DelOutputPayload: outPay,
		DelErrorCode: errCode, DelErrorMessage: errMsg,
		RemoteTaskID: extTask, RemoteContextID: extCtx, RemoteMessageID: extMsgID,
		RemoteEndpointRef: extRef, ProtocolStatus: status,
	}
	if ferr := e.gateway.repo.FencedInboundTerminal(finCtx, fencedIn); ferr != nil {
		// P0: stale owner / lease lost / conflict MUST NOT enqueue unfenced finalize outbox.
		// That would let a reclaimed generation's worker write A's terminal into delegation/step.
		if errors.Is(ferr, ErrConflict) {
			return writeFinalTaskStatus(ctx, q, reqCtx, a2a.TaskStateFailed,
				publicAgentText("A2A_FENCED_CONFLICT", "execution ownership lost", traceRef))
		}
		// Retryable only: re-apply via fenced terminal command (same owner/token/generation).
		if qerr := e.gateway.repo.EnqueueFencedTerminalOutbox(finCtx, fencedIn); qerr != nil {
			return writeFinalTaskStatus(ctx, q, reqCtx, a2a.TaskStateFailed,
				publicAgentText("A2A_TERMINAL_DEFER_FAILED", "fenced terminal deferred finalize failed", traceRef))
		}
		// Deferred recovery still publishes a terminal protocol status for clients.
		return writeFinalTaskStatus(ctx, q, reqCtx, mapDelegationStatusToTaskState(status),
			publicAgentText("A2A_TERMINAL_DEFERRED", "fenced terminal deferred", traceRef))
	}
	_ = e.gateway.repo.DeleteFinalizeOutbox(finCtx, e.workspaceID, del.ID)

	if status != agentdelegation.StatusSucceeded {
		return writeFinalTaskStatus(ctx, q, reqCtx, mapDelegationStatusToTaskState(status),
			publicAgentText(errCode, errMsg, traceRef))
	}
	reply := firstNonEmpty(result.AssistantText, "(empty)")
	return writeFinalTaskStatus(ctx, q, reqCtx, a2a.TaskStateCompleted, redactSecrets(reply))
}

func writeTaskStatus(ctx context.Context, q eventqueue.Queue, reqCtx *a2asrv.RequestContext, state a2a.TaskState, msg *a2a.Message, final bool) error {
	if q == nil {
		return nil
	}
	event := a2a.NewStatusUpdateEvent(reqCtx, state, msg)
	event.Final = final
	return q.Write(ctx, event)
}

func writeFinalTaskStatus(ctx context.Context, q eventqueue.Queue, reqCtx *a2asrv.RequestContext, state a2a.TaskState, text string) error {
	var msg *a2a.Message
	if strings.TrimSpace(text) != "" {
		if reqCtx != nil {
			msg = a2a.NewMessageForTask(a2a.MessageRoleAgent, reqCtx, a2a.TextPart{Text: text})
		} else {
			msg = a2a.NewMessage(a2a.MessageRoleAgent, a2a.TextPart{Text: text})
		}
	}
	// Map non-spec TIMED_OUT style to failed (a2a has no timed-out state).
	if state == a2a.TaskStateUnknown {
		state = a2a.TaskStateFailed
	}
	return writeTaskStatus(ctx, q, reqCtx, state, msg, true)
}

func mapDelegationStatusToTaskState(status string) a2a.TaskState {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case agentdelegation.StatusSucceeded:
		return a2a.TaskStateCompleted
	case agentdelegation.StatusCancelled:
		return a2a.TaskStateCanceled
	case agentdelegation.StatusTimedOut:
		// Protocol has no timed_out; surface as failed with error text.
		return a2a.TaskStateFailed
	case agentdelegation.StatusFailed:
		return a2a.TaskStateFailed
	default:
		return a2a.TaskStateFailed
	}
}

func publicStatusMessage(code string) string {
	switch code {
	case "A2A_INBOUND_TIMED_OUT":
		return "inbound execution timed out"
	case "A2A_INBOUND_CANCELLED":
		return "inbound execution cancelled"
	case "A2A_INBOUND_FAILED":
		return "inbound execution failed"
	default:
		return "inbound execution failed"
	}
}

// cleanupFailedPreDispatch atomically terminals run+task+delegation+step.
// - With lease: FencedInboundTerminal only (never force-mark, never unfenced finalize/outbox).
// - Without lease: AtomicUnownedInboundCleanup under unowned/expired lease gate.
// Detailed cause is stored only in durable audit fields — never returned to clients.
// Returns true when cleanup fully applied.
func (e *inboundExecutor) cleanupFailedPreDispatch(
	ctx context.Context, runID, delID, stepID, prefix string, cause error,
	lease *ExecutionLease, taskID string,
) bool {
	finCtx := context.WithoutCancel(ctx)
	publicMsg := strings.TrimSpace(prefix)
	if publicMsg == "" {
		publicMsg = "predispatch failed"
	}
	// Audit-only internal cause (not client-facing).
	internal := truncate(publicMsg+": "+errString(cause), 500)
	outSum, _ := json.Marshal(map[string]any{
		"ok": false, "status": agentdelegation.StatusFailed,
		"errorCode": "A2A_INBOUND_PREDISPATCH_FAILED", "message": publicMsg,
		"internalCause": internal,
	})
	outPay, _ := json.Marshal(map[string]any{"result": publicMsg})
	runOut, _ := json.Marshal(map[string]any{
		"source": "a2a.inbound", "errorCode": "A2A_INBOUND_PREDISPATCH_FAILED", "message": publicMsg,
	})

	var cleanupErr error
	if lease != nil && lease.Owned && lease.Token != "" && lease.Generation > 0 && taskID != "" {
		// Post-claim: one fenced TX. On conflict (lost lease) do NOT force or unfenced outbox.
		cleanupErr = e.gateway.repo.FencedInboundTerminal(finCtx, FencedTerminalInput{
			WorkspaceID: e.workspaceID, TaskID: taskID, RunID: runID,
			Owner: lease.Owner, Token: lease.Token, Generation: lease.Generation,
			TaskStatus: agentdelegation.StatusFailed, RunStatus: agentdelegation.StatusFailed,
			ExpectedRunStatus: "RUNNING",
			RunOutputSummary:  runOut, RunErrorCode: "A2A_INBOUND_PREDISPATCH_FAILED",
			DelegationID: delID, StepID: stepID,
			DelStatus: agentdelegation.StatusFailed, DelOutputSummary: outSum, DelOutputPayload: outPay,
			DelErrorCode: "A2A_INBOUND_PREDISPATCH_FAILED", DelErrorMessage: publicMsg,
			ProtocolStatus: agentdelegation.StatusFailed,
		})
		if cleanupErr != nil && errors.Is(cleanupErr, ErrConflict) {
			// Lease lost: unowned cleanup only if no live foreign lease (expired/unowned gate).
			if uerr := e.gateway.repo.AtomicUnownedInboundCleanup(finCtx, UnownedCleanupInput{
				WorkspaceID: e.workspaceID, TaskID: taskID, RunID: runID,
				DelegationID: delID, StepID: stepID, Status: agentdelegation.StatusFailed,
				RunOutputSummary: runOut, RunErrorCode: "A2A_INBOUND_PREDISPATCH_FAILED",
				DelStatus: agentdelegation.StatusFailed, DelOutputSummary: outSum, DelOutputPayload: outPay,
				DelErrorCode: "A2A_INBOUND_PREDISPATCH_FAILED", DelErrorMessage: publicMsg,
			}); uerr != nil {
				cleanupErr = errors.Join(cleanupErr, uerr)
			} else {
				cleanupErr = nil
			}
		} else if cleanupErr != nil {
			// Retryable (still own lease): fenced outbox only.
			if qerr := e.gateway.repo.EnqueueFencedTerminalOutbox(finCtx, FencedTerminalInput{
				WorkspaceID: e.workspaceID, TaskID: taskID, RunID: runID,
				Owner: lease.Owner, Token: lease.Token, Generation: lease.Generation,
				TaskStatus: agentdelegation.StatusFailed, RunStatus: agentdelegation.StatusFailed,
				ExpectedRunStatus: "RUNNING",
				RunOutputSummary:  runOut, RunErrorCode: "A2A_INBOUND_PREDISPATCH_FAILED",
				DelegationID: delID, StepID: stepID,
				DelStatus: agentdelegation.StatusFailed, DelOutputSummary: outSum, DelOutputPayload: outPay,
				DelErrorCode: "A2A_INBOUND_PREDISPATCH_FAILED", DelErrorMessage: publicMsg,
			}); qerr != nil {
				cleanupErr = errors.Join(cleanupErr, qerr)
			}
		}
	} else if taskID != "" && e.gateway.repo != nil {
		cleanupErr = e.gateway.repo.AtomicUnownedInboundCleanup(finCtx, UnownedCleanupInput{
			WorkspaceID: e.workspaceID, TaskID: taskID, RunID: runID,
			DelegationID: delID, StepID: stepID, Status: agentdelegation.StatusFailed,
			RunOutputSummary: runOut, RunErrorCode: "A2A_INBOUND_PREDISPATCH_FAILED",
			DelStatus: agentdelegation.StatusFailed, DelOutputSummary: outSum, DelOutputPayload: outPay,
			DelErrorCode: "A2A_INBOUND_PREDISPATCH_FAILED", DelErrorMessage: publicMsg,
		})
	} else {
		cleanupErr = fmt.Errorf("cleanup skipped: missing task/lease")
	}
	// Internal join for logs only — never surface to clients.
	_ = errors.Join(cause, cleanupErr)
	return cleanupErr == nil
}

func (e *inboundExecutor) Cancel(ctx context.Context, reqCtx *a2asrv.RequestContext, q eventqueue.Queue) error {
	if reqCtx == nil {
		return nil
	}
	ctx = WithTaskActor(ctx, e.actorType, e.actorID)
	traceRef := uuid.Must(uuid.NewV7()).String()
	taskID := string(reqCtx.TaskID)
	if err := e.gateway.CancelInbound(ctx, e.workspaceID, e.exposure.ID, e.actorType, e.actorID, taskID); err != nil {
		// Foreign/unknown task: TaskNotFound-equivalent without existence leak.
		if errors.Is(err, ErrNotFound) {
			return a2a.ErrTaskNotFound
		}
		_ = writeFinalTaskStatus(ctx, q, reqCtx, a2a.TaskStateFailed,
			publicAgentText("A2A_CANCEL_FAILED", "cancel failed", traceRef))
		return nil
	}
	event := a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateCanceled, nil)
	event.Final = true
	return q.Write(ctx, event)
}

// CancelInbound durable-cancels task + run + delegation + step for the authenticated
// principal only. Foreign actors receive ErrNotFound (no existence leak).
// Durable terminal is a single AtomicInboundCancel transaction (invalidates lease generation).
// InterruptRun is best-effort only and never writes agent_runs. Already-terminal tasks are
// idempotent no-ops (never SUCCEEDED→CANCELLED).
func (g *InboundGateway) CancelInbound(ctx context.Context, workspaceID, exposureID, actorType, actorID, externalTaskID string) error {
	workspaceID, exposureID, externalTaskID = strings.TrimSpace(workspaceID), strings.TrimSpace(exposureID), strings.TrimSpace(externalTaskID)
	actorType, actorID = strings.ToUpper(strings.TrimSpace(actorType)), strings.TrimSpace(actorID)
	if workspaceID == "" || exposureID == "" || externalTaskID == "" || actorType == "" || actorID == "" {
		return ErrInvalid
	}
	mapKey := exposureID + ":" + actorType + ":" + actorID + ":" + externalTaskID
	defer func() {
		g.mu.Lock()
		delete(g.activeRun, mapKey)
		g.mu.Unlock()
	}()

	task, err := g.repo.GetInboundTaskByExposureTask(ctx, workspaceID, exposureID, actorType, actorID, externalTaskID)
	if err != nil {
		g.mu.Lock()
		runID := g.activeRun[mapKey]
		g.mu.Unlock()
		if runID != "" && g.runner != nil {
			// Interrupt only — no durable write without an inbound task row.
			_ = g.runner.InterruptRun(ctx, workspaceID, runID)
		}
		return err
	}
	// Already terminal: idempotent no-op (never rewrite SUCCEEDED → CANCELLED).
	switch strings.ToUpper(task.Status) {
	case agentdelegation.StatusSucceeded, agentdelegation.StatusFailed,
		agentdelegation.StatusCancelled, agentdelegation.StatusTimedOut:
		return nil
	}

	runID := task.RunID
	if runID == "" {
		g.mu.Lock()
		runID = g.activeRun[mapKey]
		g.mu.Unlock()
	}
	// Stop in-process work first (no durable transition).
	if runID != "" && g.runner != nil {
		_ = g.runner.InterruptRun(ctx, workspaceID, runID)
	}
	if task.ID == "" {
		return ErrInvalid
	}
	if terr := g.repo.AtomicInboundCancel(context.WithoutCancel(ctx), workspaceID, task.ID); terr != nil {
		return fmt.Errorf("atomic cancel inbound: %w", terr)
	}
	// Post-commit interrupt is observational only; never reverse durable cancel
	// and never report hook failure as cancel failure.
	if runID != "" && g.runner != nil {
		_ = g.runner.InterruptRun(context.WithoutCancel(ctx), workspaceID, runID)
	}
	return nil
}

func isTerminalDelegation(status string) bool {
	switch status {
	case agentdelegation.StatusSucceeded, agentdelegation.StatusFailed,
		agentdelegation.StatusCancelled, agentdelegation.StatusTimedOut:
		return true
	default:
		return false
	}
}

func extractResultFromPayload(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(payload, &m) == nil {
		if v, ok := m["result"].(string); ok {
			return v
		}
	}
	return string(payload)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func redactSecrets(s string) string {
	lower := strings.ToLower(s)
	if strings.Contains(lower, "authorization:") || strings.Contains(lower, "bearer ") {
		return "[redacted]"
	}
	return s
}

func BuildAgentCardForExposure(baseURL string, exp Exposure) *a2a.AgentCard {
	g := &InboundGateway{baseURL: strings.TrimRight(baseURL, "/")}
	return g.buildCard(exp)
}
