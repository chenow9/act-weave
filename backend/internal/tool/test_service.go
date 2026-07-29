package tool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/principal"

	"github.com/getkin/kin-openapi/openapi3"
)

const (
	TestErrorInputSchema    = "TOOL_TEST_INPUT_SCHEMA_FAILED"
	TestErrorResponseSchema = "TOOL_TEST_RESPONSE_SCHEMA_FAILED"
	TestErrorMappings       = "TOOL_TEST_ERROR_MAPPINGS_FAILED"
	TestErrorRuntimePolicy  = "TOOL_TEST_RUNTIME_POLICY_FAILED"
	TestErrorArtifactWrite  = "TOOL_TEST_ARTIFACT_WRITE_FAILED"
	TestRetentionPermanent  = "PERMANENT"
)

type ToolTestArtifact struct {
	TestID        string
	WorkspaceID   string
	ToolVersionID string
	Request       json.RawMessage
	Response      json.RawMessage
	ErrorCode     string
	RetentionMode string
	TestedBy      string
}

type ToolTestArtifactStore interface {
	WriteToolTestArtifact(context.Context, ToolTestArtifact) (string, error)
}

type RunToolTestInput struct {
	TestID       string
	WorkspaceID  string
	CapabilityID string
	VersionID    string
	TraceID      string
	TestedBy     string
	Connection   execution.ConnectionSnapshot
	Credential   execution.CredentialReference
	Input        json.RawMessage
	// CredentialsRaw is write-only outbound-credentials.v1 material for
	// REQUEST_PASSTHROUGH attach. Never logged or persisted on TestRecord.
	CredentialsRaw json.RawMessage `json:"-"`
}

type TestService struct {
	repository *Repository
	executors  *execution.Registry
	artifacts  ToolTestArtifactStore
	injector   execution.SecretInjector
	// attacher is optional; required when dual-mode REQUEST_PASSTHROUGH tests
	// supply an outboundCredentials envelope.
	attacher *outboundidentity.BindingAttacher
	bootID   string
}

func NewTestService(repository *Repository, executors *execution.Registry, artifacts ToolTestArtifactStore) (*TestService, error) {
	if repository == nil || executors == nil || artifacts == nil {
		return nil, errors.New("tool test service dependencies are required")
	}
	return &TestService{repository: repository, executors: executors, artifacts: artifacts}, nil
}

// NewTestServiceWithInjector builds the production Tool test service. Keeping
// the basic constructor allows isolated tests with public/no-auth endpoints,
// while production always uses the same secret-injection boundary as normal
// invocations.
func NewTestServiceWithInjector(
	repository *Repository,
	executors *execution.Registry,
	artifacts ToolTestArtifactStore,
	injector execution.SecretInjector,
) (*TestService, error) {
	service, err := NewTestService(repository, executors, artifacts)
	if err != nil {
		return nil, err
	}
	if injector == nil {
		return nil, errors.New("tool test secret injector is required")
	}
	service.injector = injector
	return service, nil
}

// WithBindingAttacher enables Vault attach for REQUEST_PASSTHROUGH tool tests.
// bootID must match the RuntimeCredentialVault process boot used by the dual-mode injector.
func (service *TestService) WithBindingAttacher(attacher *outboundidentity.BindingAttacher, bootID string) *TestService {
	if service != nil {
		service.attacher = attacher
		service.bootID = strings.TrimSpace(bootID)
	}
	return service
}

// TestRunResult is the interactive test outcome: a persisted redacted record plus
// raw response body for the immediate HTTP response (not stored in summaries).
type TestRunResult struct {
	Record       TestRecord
	RequestBody  json.RawMessage
	ResponseBody json.RawMessage
}

// MaxTestResponseBodyPreviewBytes caps body returned to the console UI.
const MaxTestResponseBodyPreviewBytes = 64 << 10 // 64 KiB

func (service *TestService) Run(ctx context.Context, input RunToolTestInput) (TestRunResult, error) {
	input = normalizeRunToolTest(input)
	if !validRunToolTest(input) {
		return TestRunResult{}, ErrInvalid
	}
	version, err := service.repository.GetVersion(ctx, input.WorkspaceID, input.CapabilityID, input.VersionID)
	if err != nil {
		return TestRunResult{}, err
	}
	if version.LifecycleStatus == "PUBLISHED" {
		return TestRunResult{}, ErrImmutable
	}
	executor, err := service.executors.Resolve(version.ExecutorType)
	if err != nil {
		return TestRunResult{}, err
	}
	// Attach write-only passthrough credentials and pin dual-mode invoke context
	// before the shared SecretInjector boundary (same order as workflow trial).
	invokeCtx, cleanup, attachErr := service.prepareOutboundInvoke(ctx, input)
	if attachErr != nil {
		_ = outboundidentity.ZeroCredentialsRaw(input.CredentialsRaw)
		return TestRunResult{}, attachErr
	}
	defer cleanup()
	_ = outboundidentity.ZeroCredentialsRaw(input.CredentialsRaw)
	input.CredentialsRaw = nil

	inputSchemaPassed := validateSchemaValue(invokeCtx, version.InputSchema, input.Input, false)
	errorMappingsPassed := validateErrorMappings(version.ErrorMappings)
	runtimePolicyPassed := validateTestRuntimePolicy(version.RuntimePolicy)
	result := execution.InvocationResult{InvocationID: input.TestID, TraceID: input.TraceID}
	var invocationError error
	if !inputSchemaPassed {
		invocationError = execution.NewError(TestErrorInputSchema, "VALIDATION", false, 0, nil)
	} else {
		invoke := func(connection execution.ConnectionSnapshot) error {
			result, invocationError = executor.Invoke(invokeCtx, execution.InvocationRequest{
				InvocationID: input.TestID,
				TraceID:      input.TraceID,
				Snapshot: execution.ReleaseSnapshot{
					WorkspaceID: input.WorkspaceID, CapabilityID: input.CapabilityID,
					ToolVersionID: version.ID, ExecutorType: version.ExecutorType,
					ProviderID: version.ProviderID, ActionSchemaVersion: version.ActionSchemaVersion,
					ActionConfig: cloneRaw(version.ActionConfig), InputSchema: cloneRaw(version.InputSchema),
					OutputSchema: cloneRaw(version.OutputSchema), ErrorMappings: cloneRaw(version.ErrorMappings),
					RuntimePolicy: cloneRaw(version.RuntimePolicy), Checksum: version.Checksum,
				},
				Connection: connection,
				Input:      cloneRaw(input.Input),
			}, nil)
			return invocationError
		}
		if service.injector != nil {
			invocationError = service.injector.WithInjectedConnection(
				invokeCtx, input.Connection, input.Credential, invoke,
			)
		} else {
			invocationError = invoke(input.Connection)
		}
	}
	connectivityPassed := result.HTTPStatus > 0 || invocationError == nil
	responseSchemaPassed := invocationError == nil && validateSchemaValue(ctx, version.OutputSchema, result.Output, true)
	status := "FAILED"
	errorCode := testFailureCode(invocationError, inputSchemaPassed, responseSchemaPassed, errorMappingsPassed, runtimePolicyPassed)
	if invocationError == nil && inputSchemaPassed && connectivityPassed && responseSchemaPassed &&
		errorMappingsPassed && runtimePolicyPassed {
		status, errorCode = "SUCCEEDED", ""
	}
	requestSummary := summarizeTestRequest(input.Input)
	responseSummary := summarizeTestResponse(result)
	artifactID, err := service.artifacts.WriteToolTestArtifact(ctx, ToolTestArtifact{
		TestID: input.TestID, WorkspaceID: input.WorkspaceID, ToolVersionID: version.ID,
		Request: cloneRaw(input.Input), Response: cloneRaw(result.Output), ErrorCode: errorCode,
		RetentionMode: TestRetentionPermanent, TestedBy: input.TestedBy,
	})
	if err != nil {
		return TestRunResult{}, execution.NewError(TestErrorArtifactWrite, "INTERNAL", false, 0, err)
	}
	latency := durationMilliseconds(result.Latency)
	var storedErrorCode *string
	if errorCode != "" {
		storedErrorCode = &errorCode
	}
	record, err := service.repository.RecordTest(ctx, RecordTestInput{
		ID: input.TestID, WorkspaceID: input.WorkspaceID, ToolVersionID: version.ID,
		VersionChecksum: version.Checksum, ExpectedVersionLock: version.LockVersion,
		Status: status, ConnectivityPassed: connectivityPassed,
		ResponseSchemaPassed: responseSchemaPassed, ErrorMappingPassed: errorMappingsPassed,
		RuntimePolicyPassed: runtimePolicyPassed, RequestSummary: requestSummary,
		ResponseSummary: responseSummary, LatencyMS: &latency, ErrorCode: storedErrorCode,
		RawObjectID: &artifactID, TestedBy: input.TestedBy,
	})
	if err != nil {
		return TestRunResult{}, err
	}
	return TestRunResult{
		Record:       record,
		RequestBody:  cloneRaw(input.Input),
		ResponseBody: previewTestResponseBody(result.Output),
	}, nil
}

func previewTestResponseBody(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) <= MaxTestResponseBodyPreviewBytes {
		return cloneRaw(raw)
	}
	// Truncate oversized bodies; keep valid-ish JSON string envelope for UI.
	preview := string(raw[:MaxTestResponseBodyPreviewBytes])
	return mustJSON(map[string]any{
		"truncated": true,
		"byteSize":  len(raw),
		"preview":   preview,
	})
}

func normalizeRunToolTest(input RunToolTestInput) RunToolTestInput {
	input.TestID, input.WorkspaceID = strings.TrimSpace(input.TestID), strings.TrimSpace(input.WorkspaceID)
	input.CapabilityID, input.VersionID = strings.TrimSpace(input.CapabilityID), strings.TrimSpace(input.VersionID)
	input.TraceID, input.TestedBy = strings.TrimSpace(input.TraceID), strings.TrimSpace(input.TestedBy)
	if len(input.Input) == 0 {
		input.Input = json.RawMessage(`{}`)
	} else {
		input.Input = cloneRaw(input.Input)
	}
	return input
}

// prepareOutboundInvoke attaches REQUEST_PASSTHROUGH envelope credentials (when
// required) and returns a context carrying OutboundInvokeContext for the dual-mode
// injector. Cleanup is always safe to call.
func (service *TestService) prepareOutboundInvoke(
	ctx context.Context,
	input RunToolTestInput,
) (context.Context, func(), error) {
	noop := func() {}
	if ctx == nil {
		ctx = context.Background()
	}
	mode := strings.ToUpper(strings.TrimSpace(input.Credential.OutboundMode))
	if mode == "" {
		mode = strings.ToUpper(strings.TrimSpace(input.Credential.AuthMode))
	}
	needsPassthrough := mode == string(outboundidentity.ModeRequestPassthrough)
	needsBroker := mode == string(outboundidentity.ModeBrokerOBO)
	hasEnvelope := len(input.CredentialsRaw) > 0 && string(input.CredentialsRaw) != "null"

	// Non-dual / mock fixture path: no vault attach and no dual-mode invoke context.
	if !needsPassthrough && !needsBroker && !hasEnvelope {
		return ctx, noop, nil
	}

	// Fail closed: never accept plaintext when attach cannot run.
	if hasEnvelope && (service == nil || service.attacher == nil) {
		return ctx, noop, outboundidentity.ErrCredentialInvalid
	}

	// Dual-mode REQUEST_PASSTHROUGH requires attach when BindingAttacher is wired
	// (production) or when a dual injector would otherwise fail closed at borrow.
	// Unit fixtures that omit both attacher and injector may still exercise
	// schema/publish paths against a mock executor without credentials.
	if needsPassthrough {
		if service.attacher == nil {
			if service.injector != nil {
				return ctx, noop, outboundidentity.ErrCredentialRequired
			}
			return ctx, noop, nil
		}
		if !hasEnvelope {
			return ctx, noop, outboundidentity.ErrCredentialRequired
		}
	}

	snapshot, err := principal.NewInternalExecutionSnapshot(
		input.WorkspaceID, principal.TypeUser, input.TestedBy,
	)
	if err != nil {
		return ctx, noop, outboundidentity.ErrSubjectRequired
	}
	bootID := strings.TrimSpace(service.bootID)
	if bootID == "" {
		bootID = "tool-test-boot"
	}
	rootDeadline := time.Now().UTC().Add(15 * time.Minute)
	affinityClaimed := false

	if needsPassthrough {
		requirements, reqErr := parseToolTestRequirements(input.Credential)
		if reqErr != nil {
			return ctx, noop, reqErr
		}
		views, viewErr := connectionViewsFromCredential(input.Connection, input.Credential, requirements)
		if viewErr != nil {
			return ctx, noop, viewErr
		}
		attachResult, attachErr := service.attacher.Attach(ctx, outboundidentity.BindingAttachInput{
			RawEnvelope:  input.CredentialsRaw,
			Requirements: requirements,
			Connections:  views,
			Context: outboundidentity.BindingAttachContext{
				BootID: bootID, WorkspaceID: input.WorkspaceID,
				SubjectType: outboundidentity.SubjectTypeUser, SubjectID: input.TestedBy,
				RootScopeType: outboundidentity.RootScopeToolTest,
				RootScopeID:   input.TestID,
				RootDeadline:  rootDeadline,
				// Runtime affinity is optional for short-lived tool tests (same as
				// workflow trial when RuntimeRepository is nil).
			},
		})
		if attachErr != nil {
			return ctx, noop, attachErr
		}
		affinityClaimed = attachResult.AffinityClaimed
	}

	cleanup := func() {
		if service.attacher != nil && (affinityClaimed || needsPassthrough) {
			service.attacher.CleanupRequest(context.WithoutCancel(ctx), outboundidentity.BindingAttachContext{
				BootID: bootID, WorkspaceID: input.WorkspaceID,
				SubjectType: outboundidentity.SubjectTypeUser, SubjectID: input.TestedBy,
				RootScopeType: outboundidentity.RootScopeToolTest,
				RootScopeID:   input.TestID,
			}, affinityClaimed)
		}
	}

	// Broker/passthrough dual-mode injectors require principal + root scope.
	if service.injector == nil && !needsPassthrough {
		return ctx, cleanup, nil
	}
	invokeCtx := execution.WithOutboundInvokeContext(ctx, execution.OutboundInvokeContext{
		BootID:        bootID,
		RootScopeType: outboundidentity.RootScopeToolTest,
		RootScopeID:   input.TestID,
		RootDeadline:  rootDeadline,
		Principal:     &snapshot,
	})
	return invokeCtx, cleanup, nil
}

func parseToolTestRequirements(credential execution.CredentialReference) (outboundidentity.Requirements, error) {
	if len(credential.OutboundRequirements) == 0 || string(credential.OutboundRequirements) == "null" {
		return outboundidentity.Requirements{}, outboundidentity.ErrCredentialRequired
	}
	return outboundidentity.ParseRequirements(credential.OutboundRequirements)
}

func connectionViewsFromCredential(
	connection execution.ConnectionSnapshot,
	credential execution.CredentialReference,
	requirements outboundidentity.Requirements,
) ([]outboundidentity.ConnectionPolicyView, error) {
	reqByID := make(map[string]outboundidentity.RequirementConnection, len(requirements.Connections))
	for _, c := range requirements.Connections {
		reqByID[c.ConnectionID] = c
	}
	req, ok := reqByID[connection.ID]
	if !ok {
		// Fall back to first requirement when resolver only supplies one connection.
		if len(requirements.Connections) == 1 {
			req = requirements.Connections[0]
			ok = true
		}
	}
	if !ok {
		return nil, outboundidentity.ErrIdentityPolicyInvalid
	}
	mode := outboundidentity.Mode(strings.ToUpper(strings.TrimSpace(credential.OutboundMode)))
	if !mode.Valid() {
		mode = req.Mode
	}
	return []outboundidentity.ConnectionPolicyView{{
		ConnectionID:            connection.ID,
		ProviderID:              firstNonEmpty(connection.ProviderID, req.ProviderID),
		Mode:                    mode,
		ConnectionPolicyVersion: req.ConnectionPolicyVersion,
		ProviderContractVersion: req.ProviderContractVersion,
		Executable:              true, // ResolveTestConnection already assessed readiness.
	}}, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func validRunToolTest(input RunToolTestInput) bool {
	return validUUID(input.TestID) && validUUID(input.WorkspaceID) && validUUID(input.CapabilityID) &&
		validUUID(input.VersionID) && validUUID(input.TestedBy) && jsonObjectRaw(input.Input) &&
		strings.TrimSpace(input.Connection.ID) != "" && input.Connection.WorkspaceID == input.WorkspaceID
}

func validateSchemaValue(ctx context.Context, schemaJSON, valueJSON json.RawMessage, response bool) bool {
	// Missing / empty schemas: treat as "no contract" rather than reject every payload.
	// Incomplete OpenAPI imports often store {"type":"object","properties":{},"additionalProperties":false}
	// which would fail any real test input (pageNum/pageSize, path keys, etc.).
	if len(bytes.TrimSpace(schemaJSON)) == 0 || bytes.Equal(bytes.TrimSpace(schemaJSON), []byte("null")) ||
		bytes.Equal(bytes.TrimSpace(schemaJSON), []byte("{}")) {
		return jsonObjectOrAnyJSON(valueJSON)
	}
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
	if !response && isEmptyObjectRequestSchema(&schema) {
		// Allow free-form request objects when the tool contract has no declared properties.
		return true
	}
	if response {
		return schema.VisitJSON(value, openapi3.VisitAsResponse()) == nil
	}
	return schema.VisitJSON(value, openapi3.VisitAsRequest()) == nil
}

// isEmptyObjectRequestSchema reports schemas that declare no properties/required fields.
// These commonly come from OpenAPI operations that omitted parameters; rejecting extra
// keys with additionalProperties=false would make tool tests unusable.
func isEmptyObjectRequestSchema(schema *openapi3.Schema) bool {
	if schema == nil {
		return true
	}
	if len(schema.Required) > 0 {
		return false
	}
	if schema.Properties != nil && len(schema.Properties) > 0 {
		return false
	}
	// type omitted or object (possibly with allOf empty etc.)
	if schema.Type != nil && !schema.Type.Is("object") && schema.Type.Is("array") {
		return false
	}
	if schema.Type != nil && !schema.Type.Is("object") &&
		(schema.Type.Is("string") || schema.Type.Is("number") || schema.Type.Is("integer") || schema.Type.Is("boolean")) {
		return false
	}
	return true
}

func jsonObjectOrAnyJSON(valueJSON json.RawMessage) bool {
	if len(bytes.TrimSpace(valueJSON)) == 0 {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(valueJSON))
	decoder.UseNumber()
	var value any
	return decoder.Decode(&value) == nil
}

func validateErrorMappings(raw json.RawMessage) bool {
	var mappings map[string]json.RawMessage
	if json.Unmarshal(raw, &mappings) != nil || mappings == nil {
		return false
	}
	for protocolStatus, value := range mappings {
		if !validProtocolStatus(protocolStatus) {
			return false
		}
		var mapping struct {
			ErrorCode string `json:"errorCode"`
			Code      string `json:"code"`
		}
		if json.Unmarshal(value, &mapping) != nil {
			return false
		}
		code := strings.TrimSpace(mapping.ErrorCode)
		if code == "" {
			code = strings.TrimSpace(mapping.Code)
		}
		if !stableCodePattern.MatchString(code) {
			return false
		}
	}
	return true
}

func validProtocolStatus(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "DEFAULT" || value == "4XX" || value == "5XX" {
		return true
	}
	status, err := strconv.Atoi(value)
	return err == nil && status >= 100 && status <= 599
}

func validateTestRuntimePolicy(raw json.RawMessage) bool {
	var policy map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&policy) != nil || policy == nil {
		return false
	}
	return validPolicyInteger(policy, "timeoutMs", 1, 120000) &&
		validPolicyInteger(policy, "maxResponseBytes", 1, 16<<20) &&
		validPolicyInteger(policy, "retryCount", 0, 10)
}

func validPolicyInteger(policy map[string]any, name string, minimum, maximum int64) bool {
	value, exists := policy[name]
	if !exists {
		return true
	}
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	parsed, err := number.Int64()
	return err == nil && parsed >= minimum && parsed <= maximum
}

func testFailureCode(invocationError error, inputSchema, responseSchema, errorMappings, runtimePolicy bool) string {
	switch {
	case !inputSchema:
		return TestErrorInputSchema
	case invocationError != nil && execution.ErrorCode(invocationError) != "":
		return execution.ErrorCode(invocationError)
	case invocationError != nil:
		return execution.ErrorCodeUpstream
	case !responseSchema:
		return TestErrorResponseSchema
	case !errorMappings:
		return TestErrorMappings
	case !runtimePolicy:
		return TestErrorRuntimePolicy
	default:
		return execution.ErrorCodeUpstream
	}
}

func summarizeTestRequest(raw json.RawMessage) json.RawMessage {
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	digest := sha256.Sum256(raw)
	return mustJSON(map[string]any{
		"keys": keys, "byteSize": len(raw), "sha256": hex.EncodeToString(digest[:]),
	})
}

func summarizeTestResponse(result execution.InvocationResult) json.RawMessage {
	kind := "empty"
	if len(result.Output) > 0 {
		var value any
		if json.Unmarshal(result.Output, &value) == nil {
			switch value.(type) {
			case map[string]any:
				kind = "object"
			case []any:
				kind = "array"
			case string:
				kind = "string"
			case nil:
				kind = "null"
			default:
				kind = "scalar"
			}
		}
	}
	return mustJSON(map[string]any{
		"httpStatus": result.HTTPStatus, "contentType": result.ContentType,
		"byteSize": len(result.Output), "kind": kind,
	})
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func durationMilliseconds(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	milliseconds := duration.Milliseconds()
	if milliseconds > int64(^uint(0)>>1) {
		return int(^uint(0) >> 1)
	}
	return int(milliseconds)
}

func jsonObjectRaw(value json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}
