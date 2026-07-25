package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/principal"

	"github.com/getkin/kin-openapi/openapi3"
)

const (
	ErrorCodeResolve        = "INVOCATION_RESOLVE_FAILED"
	ErrorCodeInputSchema    = "INVOCATION_INPUT_SCHEMA_FAILED"
	ErrorCodeOutputSchema   = "INVOCATION_OUTPUT_SCHEMA_FAILED"
	ErrorCodeConfirmation   = "INVOCATION_CONFIRMATION_REQUIRED"
	ErrorCodeIdempotency    = "INVOCATION_IDEMPOTENCY_CONFLICT"
	ErrorCodeRateLimited    = "INVOCATION_RATE_LIMITED"
	ErrorCodeRecord         = "INVOCATION_RECORD_FAILED"
	InvocationRetentionMode = "PERMANENT"
)

type InvokeRequest struct {
	InvocationID          string
	WorkspaceID           string
	CapabilityID          string
	ReleaseID             string
	ActorType             string
	ActorID               string
	TraceID               string
	Input                 json.RawMessage
	ExplicitConnectionID  string
	PlanConnectionID      string
	BindingConnectionID   string
	PlanHash              string
	ConfirmationID        string
	IdempotencyKey        string
	AgentRunID            string
	WorkflowExecutionID   string
	ExecutionStepID       string
	PrincipalSnapshot     *principal.ExecutionSnapshot `json:"-"`
	AuthorizationSnapshot json.RawMessage              `json:"-"`
	// OutboundCredentialsRaw is write-only envelope material for top-level
	// DIRECT_INVOCATION REQUEST_PASSTHROUGH attach. Nested AgentRun/Workflow
	// roots inherit vault bindings from their parent attach; leave empty there.
	OutboundCredentialsRaw json.RawMessage `json:"-"`
}

type ResolveRequest struct {
	WorkspaceID          string
	CapabilityID         string
	ReleaseID            string
	ExplicitConnectionID string
	PlanConnectionID     string
	BindingConnectionID  string
}

type ResolvedInvocation struct {
	Snapshot               ReleaseSnapshot
	Connection             ConnectionSnapshot
	Credential             CredentialReference
	RiskLevel              string
	SideEffectLevel        string
	RequiresConfirmation   bool
	Idempotent             bool
	SupportsIdempotencyKey bool
	RetryCount             int
}

type InvocationResolver interface {
	ResolveInvocation(context.Context, ResolveRequest) (ResolvedInvocation, error)
}

type InvocationAuthorizer interface {
	AuthorizeInvocation(context.Context, string, string) error
}

type ConfirmationVerifier interface {
	VerifyInvocationConfirmation(context.Context, ConfirmationCheck) error
}

type ConfirmationCheck struct {
	WorkspaceID            string
	ConfirmationID         string
	RunID                  string
	TargetItemID           string
	ReleaseID              string
	ConnectionID           string
	PlanHash               string
	InputHash              string
	InteractionBindingHash string
	ActorID                string
	PrincipalSnapshot      *principal.ExecutionSnapshot
}

type IdempotencyState string

const (
	IdempotencyNew      IdempotencyState = "NEW"
	IdempotencyCached   IdempotencyState = "CACHED"
	IdempotencyConflict IdempotencyState = "CONFLICT"
)

type IdempotencyDecision struct {
	State  IdempotencyState
	Result InvocationResult
}

type IdempotencyStore interface {
	BeginInvocation(context.Context, IdempotencyRequest) (IdempotencyDecision, error)
	CompleteInvocation(context.Context, IdempotencyRequest, InvocationResult) error
	FailInvocation(context.Context, IdempotencyRequest, string) error
}

type IdempotencyRequest struct {
	WorkspaceID   string
	ToolVersionID string
	Key           string
	InputHash     string
}

type InvocationLimiter interface {
	AllowInvocation(context.Context, LimitRequest) error
}

type LimitRequest struct {
	WorkspaceID   string
	ActorID       string
	ToolVersionID string
}

type SecretInjector interface {
	WithInjectedConnection(context.Context, ConnectionSnapshot, CredentialReference, func(ConnectionSnapshot) error) error
}

type InvocationRecord struct {
	InvocationID          string
	WorkspaceID           string
	CapabilityID          string
	ReleaseID             string
	ToolVersionID         string
	ProviderID            string
	ConnectionID          string
	ActorType             string
	ActorID               string
	TraceID               string
	IdempotencyKey        string
	Status                string
	InputSummary          json.RawMessage
	OutputSummary         json.RawMessage
	Input                 json.RawMessage `json:"-"`
	Output                json.RawMessage `json:"-"`
	RetentionMode         string
	Attempts              int
	Latency               time.Duration
	ErrorCode             string
	AgentRunID            string
	WorkflowExecutionID   string
	ExecutionStepID       string
	PrincipalSnapshot     *principal.ExecutionSnapshot `json:"-"`
	AuthorizationSnapshot json.RawMessage              `json:"-"`
}

type InvocationRecorder interface {
	InvocationStarted(context.Context, InvocationRecord) error
	InvocationFinished(context.Context, InvocationRecord) error
}

type RetryWaiter interface {
	WaitBeforeRetry(context.Context, int) error
}

type RetryWaiterFunc func(context.Context, int) error

func (function RetryWaiterFunc) WaitBeforeRetry(ctx context.Context, attempt int) error {
	return function(ctx, attempt)
}

type PipelineResult struct {
	InvocationResult
	Attempts int
	Cached   bool
}

type InvocationPipeline struct {
	authorizer    InvocationAuthorizer
	resolver      InvocationResolver
	confirmations ConfirmationVerifier
	idempotency   IdempotencyStore
	limiter       InvocationLimiter
	injector      SecretInjector
	executors     *Registry
	recorder      InvocationRecorder
	retryWaiter   RetryWaiter
}

func NewInvocationPipeline(
	authorizer InvocationAuthorizer,
	resolver InvocationResolver,
	confirmations ConfirmationVerifier,
	idempotency IdempotencyStore,
	limiter InvocationLimiter,
	injector SecretInjector,
	executors *Registry,
	recorder InvocationRecorder,
	retryWaiter RetryWaiter,
) (*InvocationPipeline, error) {
	if authorizer == nil || resolver == nil || confirmations == nil || idempotency == nil ||
		limiter == nil || injector == nil || executors == nil || recorder == nil || retryWaiter == nil {
		return nil, errors.New("invocation pipeline dependencies are required")
	}
	return &InvocationPipeline{
		authorizer: authorizer, resolver: resolver, confirmations: confirmations,
		idempotency: idempotency, limiter: limiter, injector: injector,
		executors: executors, recorder: recorder, retryWaiter: retryWaiter,
	}, nil
}

func (pipeline *InvocationPipeline) Invoke(ctx context.Context, request InvokeRequest) (PipelineResult, error) {
	request = normalizeInvokeRequest(request)
	if !validInvokeRequest(request) {
		return PipelineResult{}, NewError(ErrorCodeInvalidRequest, "VALIDATION", false, 0, nil)
	}
	if err := pipeline.authorizeInvoke(ctx, request); err != nil {
		return PipelineResult{}, err
	}
	resolved, err := pipeline.resolver.ResolveInvocation(ctx, ResolveRequest{
		WorkspaceID: request.WorkspaceID, CapabilityID: request.CapabilityID, ReleaseID: request.ReleaseID,
		ExplicitConnectionID: request.ExplicitConnectionID, PlanConnectionID: request.PlanConnectionID,
		BindingConnectionID: request.BindingConnectionID,
	})
	if err != nil {
		return PipelineResult{}, stablePipelineError(ErrorCodeResolve, "RESOLUTION", err)
	}
	return pipeline.invokeResolved(ctx, request, resolved)
}

// InvokeResolved executes an already persisted immutable resolution snapshot.
// It deliberately bypasses the mutable resolver while retaining authorization,
// confirmation, schema, rate-limit, idempotency and recording checks.
func (pipeline *InvocationPipeline) InvokeResolved(
	ctx context.Context,
	request InvokeRequest,
	resolved ResolvedInvocation,
) (PipelineResult, error) {
	request = normalizeInvokeRequest(request)
	if !validInvokeRequest(request) {
		return PipelineResult{}, NewError(ErrorCodeInvalidRequest, "VALIDATION", false, 0, nil)
	}
	if err := pipeline.authorizeInvoke(ctx, request); err != nil {
		return PipelineResult{}, err
	}
	return pipeline.invokeResolved(ctx, request, resolved)
}

// authorizeInvoke applies actor-type-aware workspace authorization.
// USER → membership/role check via InvocationAuthorizer.
// SERVICE_PRINCIPAL → PrincipalSnapshot already required+validated by validInvokeRequest
// (AAP grant binding pinned on the AgentRun); service principal IDs are not workspace members.
// SYSTEM → internal actor; no user membership.
func (pipeline *InvocationPipeline) authorizeInvoke(ctx context.Context, request InvokeRequest) error {
	switch request.ActorType {
	case "SERVICE_PRINCIPAL", "SYSTEM":
		return nil
	default:
		return pipeline.authorizer.AuthorizeInvocation(ctx, request.ActorID, request.WorkspaceID)
	}
}

func (pipeline *InvocationPipeline) invokeResolved(
	ctx context.Context,
	request InvokeRequest,
	resolved ResolvedInvocation,
) (PipelineResult, error) {
	if resolved.Snapshot.WorkspaceID != request.WorkspaceID ||
		resolved.Snapshot.CapabilityID != request.CapabilityID || resolved.Snapshot.ReleaseID != request.ReleaseID ||
		resolved.Connection.WorkspaceID != request.WorkspaceID || resolved.Connection.ProviderID != resolved.Snapshot.ProviderID ||
		resolved.RetryCount < 0 || resolved.RetryCount > 10 {
		return PipelineResult{}, NewError(ErrorCodeResolve, "RESOLUTION", false, 0, nil)
	}
	// Project workflow/agent bag onto tool inputSchema when additionalProperties
	// is false so multi-Tool smart-dag.v2 graphs can share trial/execute input
	// without INVOCATION_INPUT_SCHEMA_FAILED on downstream tools.
	if projected, ok := projectInputOntoSchema(resolved.Snapshot.InputSchema, request.Input); ok {
		request.Input = projected
	}
	inputHash := invocationInputHash(request, resolved.Connection.ID)
	if !validateInvocationSchema(ctx, resolved.Snapshot.InputSchema, request.Input, false) {
		return PipelineResult{}, NewError(ErrorCodeInputSchema, "VALIDATION", false, 0, nil)
	}
	if resolved.RequiresConfirmation {
		if request.ConfirmationID == "" {
			return PipelineResult{}, NewError(ErrorCodeConfirmation, "CONFIRMATION", false, 0, nil)
		}
		if err := pipeline.confirmations.VerifyInvocationConfirmation(ctx, ConfirmationCheck{
			WorkspaceID: request.WorkspaceID, ConfirmationID: request.ConfirmationID,
			ReleaseID: request.ReleaseID, ConnectionID: resolved.Connection.ID,
			PlanHash:  request.PlanHash,
			InputHash: inputHash, ActorID: request.ActorID,
			PrincipalSnapshot: request.PrincipalSnapshot,
		}); err != nil {
			return PipelineResult{}, stablePipelineError(ErrorCodeConfirmation, "CONFIRMATION", err)
		}
	}
	idempotencyRequest := IdempotencyRequest{
		WorkspaceID: request.WorkspaceID, ToolVersionID: resolved.Snapshot.ToolVersionID,
		Key: request.IdempotencyKey, InputHash: inputHash,
	}
	if request.IdempotencyKey != "" {
		decision, err := pipeline.idempotency.BeginInvocation(ctx, idempotencyRequest)
		if err != nil {
			return PipelineResult{}, stablePipelineError(ErrorCodeIdempotency, "IDEMPOTENCY", err)
		}
		switch decision.State {
		case IdempotencyCached:
			return PipelineResult{InvocationResult: decision.Result, Cached: true}, nil
		case IdempotencyConflict:
			return PipelineResult{}, NewError(ErrorCodeIdempotency, "IDEMPOTENCY", false, 0, nil)
		case IdempotencyNew:
		default:
			return PipelineResult{}, NewError(ErrorCodeIdempotency, "IDEMPOTENCY", false, 0, nil)
		}
	}
	if err := pipeline.limiter.AllowInvocation(ctx, LimitRequest{
		WorkspaceID: request.WorkspaceID, ActorID: request.ActorID,
		ToolVersionID: resolved.Snapshot.ToolVersionID,
	}); err != nil {
		pipeline.failIdempotency(ctx, idempotencyRequest, request.IdempotencyKey, ErrorCodeRateLimited)
		return PipelineResult{}, stablePipelineError(ErrorCodeRateLimited, "RATE_LIMIT", err)
	}
	executor, err := pipeline.executors.Resolve(resolved.Snapshot.ExecutorType)
	if err != nil {
		pipeline.failIdempotency(ctx, idempotencyRequest, request.IdempotencyKey, ErrorCodeResolve)
		return PipelineResult{}, stablePipelineError(ErrorCodeResolve, "RESOLUTION", err)
	}
	startedRecord := baseInvocationRecord(request, resolved, inputHash)
	if err := pipeline.recorder.InvocationStarted(ctx, startedRecord); err != nil {
		pipeline.failIdempotency(ctx, idempotencyRequest, request.IdempotencyKey, ErrorCodeRecord)
		return PipelineResult{}, stablePipelineError(ErrorCodeRecord, "INTERNAL", err)
	}
	maxAttempts := 1
	if retriesAllowed(request, resolved) {
		maxAttempts += resolved.RetryCount
	}
	var result InvocationResult
	var invocationError error
	attempts := 0
	// Attach non-secret principal/root context only after confirmation so
	// credential acquisition (Broker/Vault) cannot run earlier.
	injectCtx := WithOutboundInvokeContext(ctx, outboundInvokeContextFromRequest(request))
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attempts = attempt
		invocationError = pipeline.injector.WithInjectedConnection(injectCtx, resolved.Connection, resolved.Credential, func(connection ConnectionSnapshot) error {
			if request.IdempotencyKey != "" && resolved.SupportsIdempotencyKey {
				connection.Headers = cloneHeaders(connection.Headers)
				connection.Headers["Idempotency-Key"] = request.IdempotencyKey
			}
			var invokeError error
			result, invokeError = executor.Invoke(ctx, InvocationRequest{
				InvocationID: request.InvocationID, TraceID: request.TraceID,
				Snapshot: resolved.Snapshot, Connection: connection, Input: append(json.RawMessage(nil), request.Input...),
				ActorType: request.ActorType, ActorID: request.ActorID, AgentRunID: request.AgentRunID,
			}, nil)
			return invokeError
		})
		invocationError = mapConfiguredHTTPError(resolved.Snapshot.ErrorMappings, result, invocationError)
		if invocationError == nil || attempt == maxAttempts || !isRetryableExecutionError(invocationError) {
			break
		}
		if err := pipeline.retryWaiter.WaitBeforeRetry(ctx, attempt); err != nil {
			invocationError = normalizeContextError(err)
			break
		}
	}
	if invocationError == nil && !validateInvocationSchema(ctx, resolved.Snapshot.OutputSchema, result.Output, true) {
		invocationError = NewError(ErrorCodeOutputSchema, "VALIDATION", false, 0, nil)
	}
	finishedRecord := finishedInvocationRecord(startedRecord, result, attempts, invocationError)
	if err := pipeline.recorder.InvocationFinished(context.WithoutCancel(ctx), finishedRecord); err != nil && invocationError == nil {
		invocationError = stablePipelineError(ErrorCodeRecord, "INTERNAL", err)
	}
	if request.IdempotencyKey != "" {
		if invocationError == nil {
			if err := pipeline.idempotency.CompleteInvocation(context.WithoutCancel(ctx), idempotencyRequest, result); err != nil {
				return PipelineResult{}, stablePipelineError(ErrorCodeIdempotency, "IDEMPOTENCY", err)
			}
		} else {
			pipeline.failIdempotency(context.WithoutCancel(ctx), idempotencyRequest, request.IdempotencyKey, ErrorCode(invocationError))
		}
	}
	return PipelineResult{InvocationResult: result, Attempts: attempts}, invocationError
}

func (pipeline *InvocationPipeline) failIdempotency(ctx context.Context, request IdempotencyRequest, key, code string) {
	if key != "" {
		_ = pipeline.idempotency.FailInvocation(ctx, request, code)
	}
}

func outboundInvokeContextFromRequest(request InvokeRequest) OutboundInvokeContext {
	ctx := OutboundInvokeContext{Principal: request.PrincipalSnapshot}
	// Nested Agent → Workflow → Tool: AgentRun wins as top-level root.
	if id := strings.TrimSpace(request.AgentRunID); id != "" {
		ctx.RootScopeType = outboundidentity.RootScopeAgentRun
		ctx.RootScopeID = id
		return ctx
	}
	if id := strings.TrimSpace(request.WorkflowExecutionID); id != "" {
		// Trial attach keys use WORKFLOW_TRIAL; production uses WORKFLOW_EXECUTION.
		// TraceID "workflow-trial/" is set by trial tool adapters / adapters.
		trace := strings.ToLower(strings.TrimSpace(request.TraceID))
		if strings.Contains(trace, "trial") {
			ctx.RootScopeType = outboundidentity.RootScopeWorkflowTrial
		} else {
			ctx.RootScopeType = outboundidentity.RootScopeWorkflowExecution
		}
		ctx.RootScopeID = id
		return ctx
	}
	ctx.RootScopeType = outboundidentity.RootScopeDirectInvocation
	ctx.RootScopeID = request.InvocationID
	return ctx
}

func normalizeInvokeRequest(request InvokeRequest) InvokeRequest {
	request.InvocationID = strings.TrimSpace(request.InvocationID)
	request.WorkspaceID, request.CapabilityID = strings.TrimSpace(request.WorkspaceID), strings.TrimSpace(request.CapabilityID)
	request.ReleaseID, request.ActorType = strings.TrimSpace(request.ReleaseID), strings.TrimSpace(request.ActorType)
	request.ActorID, request.TraceID = strings.TrimSpace(request.ActorID), strings.TrimSpace(request.TraceID)
	request.ExplicitConnectionID = strings.TrimSpace(request.ExplicitConnectionID)
	request.PlanConnectionID = strings.TrimSpace(request.PlanConnectionID)
	request.BindingConnectionID = strings.TrimSpace(request.BindingConnectionID)
	request.PlanHash = strings.ToLower(strings.TrimSpace(request.PlanHash))
	request.ConfirmationID, request.IdempotencyKey = strings.TrimSpace(request.ConfirmationID), strings.TrimSpace(request.IdempotencyKey)
	request.AgentRunID = strings.TrimSpace(request.AgentRunID)
	request.WorkflowExecutionID = strings.TrimSpace(request.WorkflowExecutionID)
	request.ExecutionStepID = strings.TrimSpace(request.ExecutionStepID)
	if len(request.Input) == 0 {
		request.Input = json.RawMessage(`{}`)
	} else {
		request.Input = append(json.RawMessage(nil), request.Input...)
	}
	return request
}

func validInvokeRequest(request InvokeRequest) bool {
	validPrincipal := request.ActorType != "SERVICE_PRINCIPAL"
	if request.PrincipalSnapshot != nil {
		validPrincipal = request.PrincipalSnapshot.Validate() == nil &&
			request.PrincipalSnapshot.Identity.Actor.WorkspaceID == request.WorkspaceID &&
			string(request.PrincipalSnapshot.Identity.Actor.Type) == request.ActorType &&
			request.PrincipalSnapshot.Identity.Actor.ID == request.ActorID
	}
	return request.InvocationID != "" && request.WorkspaceID != "" && request.CapabilityID != "" &&
		request.ReleaseID != "" && request.ActorID != "" && request.TraceID != "" &&
		(request.ActorType == "USER" || request.ActorType == "SERVICE_PRINCIPAL" || request.ActorType == "SYSTEM") &&
		len(request.IdempotencyKey) <= 256 && (request.PlanHash == "" || validConfirmationHash(request.PlanHash)) &&
		jsonObject(request.Input) && validPrincipal &&
		(request.ExecutionStepID == "" || request.WorkflowExecutionID != "")
}

func retriesAllowed(request InvokeRequest, resolved ResolvedInvocation) bool {
	allowed := resolved.Idempotent || (request.IdempotencyKey != "" && resolved.SupportsIdempotencyKey)
	if resolved.RiskLevel == "HIGH" || resolved.RiskLevel == "CRITICAL" {
		allowed = allowed && request.IdempotencyKey != ""
	}
	return allowed
}

func isRetryableExecutionError(err error) bool {
	var executionError *Error
	return errors.As(err, &executionError) && executionError.Retryable
}

func stablePipelineError(code, category string, cause error) error {
	var executionError *Error
	if errors.As(cause, &executionError) && executionError.Code == code {
		return executionError
	}
	return NewError(code, category, false, 0, cause)
}

func normalizeContextError(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return NewError(ErrorCodeTimeout, "TIMEOUT", true, 0, context.DeadlineExceeded)
	case errors.Is(err, context.Canceled):
		return NewError(ErrorCodeCanceled, "CANCELED", false, 0, context.Canceled)
	default:
		return NewError(ErrorCodeUpstream, "UPSTREAM", true, 0, err)
	}
}

func validateInvocationSchema(ctx context.Context, schemaJSON, valueJSON json.RawMessage, response bool) bool {
	var schema openapi3.Schema
	if json.Unmarshal(schemaJSON, &schema) != nil || schema.Validate(ctx) != nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(valueJSON))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return false
	}
	if response {
		return schema.VisitJSON(value, openapi3.VisitAsResponse()) == nil
	}
	return schema.VisitJSON(value, openapi3.VisitAsRequest()) == nil
}

// projectInputOntoSchema drops keys not declared in schema.properties when the
// schema sets additionalProperties=false. Returns (input, false) when no
// projection is needed or schema/input cannot be parsed.
func projectInputOntoSchema(schemaJSON, inputJSON json.RawMessage) (json.RawMessage, bool) {
	if len(schemaJSON) == 0 || len(inputJSON) == 0 {
		return nil, false
	}
	var schema map[string]any
	if json.Unmarshal(schemaJSON, &schema) != nil {
		return nil, false
	}
	additional, hasAdditional := schema["additionalProperties"].(bool)
	if !hasAdditional || additional {
		return nil, false
	}
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
	}
	var input map[string]any
	if json.Unmarshal(inputJSON, &input) != nil || input == nil {
		return nil, false
	}
	projected := make(map[string]any, len(properties))
	changed := false
	for key, value := range input {
		if _, allowed := properties[key]; allowed {
			projected[key] = value
		} else {
			changed = true
		}
	}
	if !changed {
		return nil, false
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

func invocationInputHash(request InvokeRequest, connectionID string) string {
	canonical, _, err := canonicalConfirmationInput(request.Input)
	if err != nil {
		canonical = append(json.RawMessage(nil), request.Input...)
	}
	return boundConfirmationInputHash(request.ReleaseID, connectionID, canonical)
}

func baseInvocationRecord(request InvokeRequest, resolved ResolvedInvocation, inputHash string) InvocationRecord {
	return InvocationRecord{
		InvocationID: request.InvocationID, WorkspaceID: request.WorkspaceID,
		CapabilityID: request.CapabilityID, ReleaseID: request.ReleaseID,
		ToolVersionID: resolved.Snapshot.ToolVersionID, ProviderID: resolved.Snapshot.ProviderID,
		ConnectionID: resolved.Connection.ID, ActorType: request.ActorType, ActorID: request.ActorID,
		TraceID: request.TraceID, IdempotencyKey: request.IdempotencyKey,
		Status: "RUNNING", InputSummary: summarizeInvocationInput(request.Input, inputHash),
		Input: append(json.RawMessage(nil), request.Input...), RetentionMode: InvocationRetentionMode,
		AgentRunID: request.AgentRunID, WorkflowExecutionID: request.WorkflowExecutionID,
		ExecutionStepID: request.ExecutionStepID, PrincipalSnapshot: request.PrincipalSnapshot,
		AuthorizationSnapshot: append(json.RawMessage(nil), request.AuthorizationSnapshot...),
	}
}

func finishedInvocationRecord(started InvocationRecord, result InvocationResult, attempts int, err error) InvocationRecord {
	finished := started
	finished.Status, finished.Attempts, finished.Latency = "SUCCEEDED", attempts, result.Latency
	finished.Output = append(json.RawMessage(nil), result.Output...)
	finished.OutputSummary = summarizeInvocationOutput(result)
	if err != nil {
		finished.Status, finished.ErrorCode = "FAILED", ErrorCode(err)
		if finished.ErrorCode == "" {
			finished.ErrorCode = ErrorCodeUpstream
		}
	}
	return finished
}

func summarizeInvocationInput(input json.RawMessage, inputHash string) json.RawMessage {
	var object map[string]any
	_ = json.Unmarshal(input, &object)
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	encoded, _ := json.Marshal(map[string]any{"keys": keys, "byteSize": len(input), "sha256": inputHash})
	return encoded
}

func summarizeInvocationOutput(result InvocationResult) json.RawMessage {
	encoded, _ := json.Marshal(map[string]any{
		"byteSize": len(result.Output), "httpStatus": result.HTTPStatus, "contentType": result.ContentType,
	})
	return encoded
}

func mapConfiguredHTTPError(mappingsJSON json.RawMessage, result InvocationResult, current error) error {
	if current == nil || result.HTTPStatus == 0 {
		return current
	}
	var mappings map[string]struct {
		ErrorCode string `json:"errorCode"`
		Code      string `json:"code"`
		Retryable *bool  `json:"retryable"`
	}
	if json.Unmarshal(mappingsJSON, &mappings) != nil {
		return current
	}
	normalizedMappings := make(map[string]struct {
		ErrorCode string `json:"errorCode"`
		Code      string `json:"code"`
		Retryable *bool  `json:"retryable"`
	}, len(mappings))
	for key, mapping := range mappings {
		normalizedMappings[strings.ToUpper(strings.TrimSpace(key))] = mapping
	}
	keys := []string{strconv.Itoa(result.HTTPStatus), statusClass(result.HTTPStatus), "DEFAULT"}
	for _, key := range keys {
		mapping, exists := normalizedMappings[key]
		if !exists {
			continue
		}
		code := strings.TrimSpace(mapping.ErrorCode)
		if code == "" {
			code = strings.TrimSpace(mapping.Code)
		}
		if code == "" {
			return current
		}
		retryable := isRetryableExecutionError(current)
		if mapping.Retryable != nil {
			retryable = *mapping.Retryable
		}
		return NewError(code, "UPSTREAM", retryable, result.HTTPStatus, current)
	}
	return current
}

func statusClass(status int) string { return string(rune('0'+status/100)) + "XX" }

func cloneHeaders(headers map[string]string) map[string]string {
	cloned := make(map[string]string, len(headers)+1)
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

func jsonObject(value json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}
