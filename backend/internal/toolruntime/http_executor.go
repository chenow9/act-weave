package toolruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"actweave/backend/internal/execution"
)

const (
	defaultHTTPTimeout          = 4 * time.Second
	maximumHTTPTimeout          = 2 * time.Minute
	defaultHTTPResponseMaxBytes = int64(1 << 20)
	maximumHTTPResponseMaxBytes = int64(16 << 20)
)

type HTTPExecutor struct {
	client *http.Client
	mutex  sync.Mutex
	active map[string]context.CancelFunc
	// fileDownloads optionally injects wire-only download URLs for x-actweave-file
	// schema nodes (IC-09 / KD-22). Nil is a no-op (existing tools unchanged).
	fileDownloads FileDownloadEnricher
}

func NewHTTPExecutor(client *http.Client) *HTTPExecutor {
	if client == nil {
		client = &http.Client{}
	}
	return &HTTPExecutor{client: client, active: make(map[string]context.CancelFunc)}
}

// ConfigureFileDownloads attaches the AAP file download enricher. Safe to call
// once during process bootstrap before the server accepts traffic.
func (executor *HTTPExecutor) ConfigureFileDownloads(enricher FileDownloadEnricher) *HTTPExecutor {
	if executor == nil {
		return nil
	}
	executor.fileDownloads = enricher
	return executor
}

// NewExecutorRegistry is the phase-one application registry. It deliberately
// registers the real HTTP executor only; unavailable future kinds are rejected.
func NewExecutorRegistry(client *http.Client) (*execution.Registry, error) {
	return NewExecutorRegistryWith(NewHTTPExecutor(client))
}

// NewExecutorRegistryWith registers the provided HTTP executor instance so
// callers can ConfigureFileDownloads on the same pointer after construction.
func NewExecutorRegistryWith(httpExec *HTTPExecutor) (*execution.Registry, error) {
	if httpExec == nil {
		httpExec = NewHTTPExecutor(nil)
	}
	return execution.NewRegistry(httpExec)
}

func (*HTTPExecutor) Kind() string { return execution.ExecutorTypeHTTP }

func (*HTTPExecutor) Capabilities() execution.ExecutorFeatures {
	return execution.ExecutorFeatures{Cancel: true}
}

func (executor *HTTPExecutor) Invoke(
	ctx context.Context,
	request execution.InvocationRequest,
	sink execution.InvocationEventSink,
) (execution.InvocationResult, error) {
	result := execution.InvocationResult{InvocationID: request.InvocationID, TraceID: request.TraceID}
	action, policy, input, endpoint, headers, err := prepareHTTPInvocation(request)
	if err != nil {
		return result, err
	}
	// KD-22: wire-only file download enrichment. Mutates a deep-copied wireInput
	// and download headers only — request.Input / pipeline persistence stay scrubbed.
	wireInput := input
	if executor != nil && executor.fileDownloads != nil {
		createdBy := strings.TrimSpace(request.ActorID)
		if createdBy == "" {
			createdBy = "system:tool-invoke"
		}
		enriched, enrichErr := executor.fileDownloads.EnrichFileDownloads(ctx, FileDownloadEnrichRequest{
			WorkspaceID: request.Snapshot.WorkspaceID,
			CreatedBy:   createdBy,
			InputSchema: request.Snapshot.InputSchema,
			Input:       input,
		})
		if enrichErr != nil {
			return result, enrichErr
		}
		if enriched.WireInput != nil {
			wireInput = enriched.WireInput
		}
		if len(enriched.Headers) > 0 {
			if headers == nil {
				headers = make(map[string]string, len(enriched.Headers))
			}
			for name, value := range enriched.Headers {
				headers[name] = value
			}
		}
	}
	invocationContext, cancel := context.WithTimeout(ctx, policy.Timeout)
	if err := executor.register(request.InvocationID, cancel); err != nil {
		cancel()
		return result, err
	}
	defer func() {
		executor.unregister(request.InvocationID)
		cancel()
	}()

	result.StartedAt = time.Now().UTC()
	if err := emitInvocationEvent(invocationContext, sink, request.InvocationID, execution.EventStarted, ""); err != nil {
		return executor.finish(ctx, sink, result, execution.NewError(
			execution.ErrorCodeEventSink, "INTERNAL", false, 0, err,
		))
	}
	httpRequest, err := buildSnapshotHTTPRequest(invocationContext, action, wireInput, endpoint, headers)
	if err != nil {
		return executor.finish(ctx, sink, result, err)
	}
	networkPolicy, err := resolvedEgressPolicy(request.Connection.EgressPolicy, endpoint)
	if err != nil {
		return executor.finish(ctx, sink, result, err)
	}
	networkGuard, err := execution.NewHTTPNetworkGuard(networkPolicy, nil)
	if err != nil {
		return executor.finish(ctx, sink, result, err)
	}
	if err := networkGuard.ValidateURL(invocationContext, httpRequest.URL); err != nil {
		return executor.finish(ctx, sink, result, err)
	}
	client, err := networkGuard.ProtectClient(executor.client, append([]string(nil), request.Connection.SensitiveHeaderNames...))
	if err != nil {
		return executor.finish(ctx, sink, result, err)
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return executor.finish(ctx, sink, result, normalizeHTTPTransportError(invocationContext, err))
	}
	defer response.Body.Close()
	result.HTTPStatus = response.StatusCode
	result.ContentType = response.Header.Get("Content-Type")
	payload, err := readLimitedHTTPResponse(response.Body, policy.MaxResponseBytes)
	if err != nil {
		return executor.finish(ctx, sink, result, err)
	}
	result.Output = normalizeHTTPResponse(payload)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return executor.finish(ctx, sink, result, execution.NewError(
			execution.ErrorCodeUpstreamHTTP, "UPSTREAM", response.StatusCode >= 500,
			response.StatusCode, nil,
		))
	}
	return executor.finish(ctx, sink, result, nil)
}

func (executor *HTTPExecutor) Cancel(_ context.Context, reference execution.InvocationRef) error {
	invocationID := strings.TrimSpace(reference.InvocationID)
	if invocationID == "" {
		return execution.NewError(execution.ErrorCodeInvalidRequest, "VALIDATION", false, 0, nil)
	}
	executor.mutex.Lock()
	cancel, exists := executor.active[invocationID]
	executor.mutex.Unlock()
	if !exists {
		return execution.ErrInvocationNotActive
	}
	cancel()
	return nil
}

func (executor *HTTPExecutor) register(invocationID string, cancel context.CancelFunc) error {
	executor.mutex.Lock()
	defer executor.mutex.Unlock()
	if _, exists := executor.active[invocationID]; exists {
		return execution.NewError(execution.ErrorCodeConflict, "CONFLICT", false, 0, nil)
	}
	executor.active[invocationID] = cancel
	return nil
}

func (executor *HTTPExecutor) unregister(invocationID string) {
	executor.mutex.Lock()
	delete(executor.active, invocationID)
	executor.mutex.Unlock()
}

func (*HTTPExecutor) finish(
	ctx context.Context,
	sink execution.InvocationEventSink,
	result execution.InvocationResult,
	invocationError error,
) (execution.InvocationResult, error) {
	result.FinishedAt = time.Now().UTC()
	result.Latency = result.FinishedAt.Sub(result.StartedAt)
	eventType, errorCode := execution.EventCompleted, ""
	if invocationError != nil {
		eventType, errorCode = execution.EventFailed, execution.ErrorCode(invocationError)
	}
	if err := emitInvocationEvent(context.WithoutCancel(ctx), sink, result.InvocationID, eventType, errorCode); err != nil && invocationError == nil {
		return result, execution.NewError(execution.ErrorCodeEventSink, "INTERNAL", false, 0, err)
	}
	return result, invocationError
}

type httpActionConfig struct {
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	Parameters  []httpParameter `json:"parameters"`
	RequestBody *httpBodyConfig `json:"requestBody,omitempty"`
}

type httpParameter struct {
	Name     string `json:"name"`
	In       string `json:"in"`
	Input    string `json:"input,omitempty"`
	Required bool   `json:"required,omitempty"`
}

type httpBodyConfig struct {
	Input       string   `json:"input,omitempty"`
	Parameters  []string `json:"parameters,omitempty"`
	ContentType string   `json:"contentType,omitempty"`
}

type httpRuntimePolicy struct {
	TimeoutMS        int64 `json:"timeoutMs"`
	MaxResponseBytes int64 `json:"maxResponseBytes"`
}

type resolvedHTTPPolicy struct {
	Timeout          time.Duration
	MaxResponseBytes int64
}

func prepareHTTPInvocation(request execution.InvocationRequest) (
	httpActionConfig,
	resolvedHTTPPolicy,
	map[string]any,
	*url.URL,
	map[string]string,
	error,
) {
	snapshot, connection := request.Snapshot, request.Connection
	if strings.TrimSpace(request.InvocationID) == "" ||
		strings.TrimSpace(snapshot.WorkspaceID) == "" ||
		strings.TrimSpace(snapshot.CapabilityID) == "" ||
		strings.TrimSpace(snapshot.ToolVersionID) == "" ||
		strings.TrimSpace(snapshot.ProviderID) == "" ||
		!strings.EqualFold(strings.TrimSpace(snapshot.ExecutorType), execution.ExecutorTypeHTTP) ||
		strings.TrimSpace(snapshot.ActionSchemaVersion) != "http.v1" ||
		strings.TrimSpace(connection.ID) == "" ||
		connection.WorkspaceID != snapshot.WorkspaceID || connection.ProviderID != snapshot.ProviderID {
		return httpActionConfig{}, resolvedHTTPPolicy{}, nil, nil, nil,
			execution.NewError(execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, nil)
	}
	var action httpActionConfig
	if err := json.Unmarshal(snapshot.ActionConfig, &action); err != nil {
		return action, resolvedHTTPPolicy{}, nil, nil, nil,
			execution.NewError(execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, err)
	}
	action.Method = strings.ToUpper(strings.TrimSpace(action.Method))
	action.Path = strings.TrimSpace(action.Path)
	if action.Method == "" {
		action.Method = http.MethodGet
	}
	if !validHTTPMethod(action.Method) || action.Path == "" || !strings.HasPrefix(action.Path, "/") || strings.HasPrefix(action.Path, "//") {
		return action, resolvedHTTPPolicy{}, nil, nil, nil,
			execution.NewError(execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, nil)
	}
	var runtimePolicy httpRuntimePolicy
	if len(snapshot.RuntimePolicy) > 0 && json.Unmarshal(snapshot.RuntimePolicy, &runtimePolicy) != nil {
		return action, resolvedHTTPPolicy{}, nil, nil, nil,
			execution.NewError(execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, nil)
	}
	policy := resolvedHTTPPolicy{Timeout: defaultHTTPTimeout, MaxResponseBytes: defaultHTTPResponseMaxBytes}
	if runtimePolicy.TimeoutMS > 0 {
		policy.Timeout = time.Duration(runtimePolicy.TimeoutMS) * time.Millisecond
	}
	if runtimePolicy.MaxResponseBytes > 0 {
		policy.MaxResponseBytes = runtimePolicy.MaxResponseBytes
	}
	if policy.Timeout <= 0 || policy.Timeout > maximumHTTPTimeout ||
		policy.MaxResponseBytes <= 0 || policy.MaxResponseBytes > maximumHTTPResponseMaxBytes {
		return action, policy, nil, nil, nil,
			execution.NewError(execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, nil)
	}
	// Apply InputSchema defaults when the model/user omits optional params
	// (e.g. pageNum/pageSize). Mirrors execution.normalizeToolInput so HTTP
	// tools stay seamless for end users who never know upstream API params.
	inputPayload := append(json.RawMessage(nil), request.Input...)
	if normalized, ok := execution.NormalizeToolInput(snapshot.InputSchema, inputPayload); ok {
		inputPayload = normalized
	}
	input := make(map[string]any)
	if len(inputPayload) > 0 {
		if json.Unmarshal(inputPayload, &input) != nil || input == nil {
			return action, policy, nil, nil, nil,
				execution.NewError(execution.ErrorCodeInvalidRequest, "VALIDATION", false, 0, nil)
		}
	}
	endpoint, err := url.Parse(strings.TrimSpace(connection.BaseURL))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil {
		return action, policy, nil, nil, nil,
			execution.NewError(execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, err)
	}
	endpoint.Fragment = ""
	headers := make(map[string]string, len(connection.Headers))
	for name, value := range connection.Headers {
		headers[name] = value
	}
	return action, policy, input, endpoint, headers, nil
}

func buildSnapshotHTTPRequest(
	ctx context.Context,
	action httpActionConfig,
	input map[string]any,
	baseURL *url.URL,
	connectionHeaders map[string]string,
) (*http.Request, error) {
	endpoint := *baseURL
	resolvedPath := action.Path
	consumed := make(map[string]bool)
	parameters := append([]httpParameter(nil), action.Parameters...)
	configuredPath := make(map[string]bool)
	for _, parameter := range parameters {
		if strings.EqualFold(parameter.In, "path") {
			configuredPath[parameter.Name] = true
		}
	}
	for _, name := range pathPlaceholders(action.Path) {
		if !configuredPath[name] {
			parameters = append(parameters, httpParameter{Name: name, In: "path", Input: name, Required: true})
		}
	}
	query := endpoint.Query()
	requestHeaders := make(http.Header)
	bodyValues := make(map[string]any)
	for _, parameter := range parameters {
		name, location := strings.TrimSpace(parameter.Name), strings.ToLower(strings.TrimSpace(parameter.In))
		inputName := strings.TrimSpace(parameter.Input)
		if inputName == "" {
			inputName = name
		}
		if name == "" || inputName == "" {
			return nil, execution.NewError(execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, nil)
		}
		value, exists := input[inputName]
		if !exists || value == nil {
			if parameter.Required || location == "path" {
				return nil, execution.NewError(execution.ErrorCodeInvalidRequest, "VALIDATION", false, 0, nil)
			}
			continue
		}
		consumed[inputName] = true
		switch location {
		case "path":
			resolvedPath = strings.ReplaceAll(resolvedPath, "{"+name+"}", url.PathEscape(fmt.Sprint(value)))
		case "query":
			addURLValues(query, name, value)
		case "header":
			if !validHTTPHeaderName(name) {
				return nil, execution.NewError(execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, nil)
			}
			requestHeaders.Set(name, fmt.Sprint(value))
		case "body":
			bodyValues[name] = value
		default:
			return nil, execution.NewError(execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, nil)
		}
	}
	// Residual input keys not declared in action.parameters become query params.
	// OpenAPI imports often omit query (pageNum/pageSize); tests still need them on the wire.
	for key, value := range input {
		if consumed[key] || value == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "body") {
			continue
		}
		addURLValues(query, key, value)
		consumed[key] = true
	}
	if strings.Contains(resolvedPath, "{") || strings.Contains(resolvedPath, "}") {
		return nil, execution.NewError(execution.ErrorCodeInvalidRequest, "VALIDATION", false, 0, nil)
	}
	setEscapedURLPath(&endpoint, joinEscapedPath(endpoint.EscapedPath(), resolvedPath))
	endpoint.RawQuery = query.Encode()
	body, contentType, err := buildSnapshotHTTPBody(action.RequestBody, input, consumed, bodyValues)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, action.Method, endpoint.String(), body)
	if err != nil {
		return nil, execution.NewError(execution.ErrorCodeInvalidRequest, "VALIDATION", false, 0, err)
	}
	for name, values := range requestHeaders {
		request.Header[name] = append([]string(nil), values...)
	}
	for name, value := range connectionHeaders {
		if !validHTTPHeaderName(name) {
			return nil, execution.NewError(execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, nil)
		}
		request.Header.Set(name, value)
	}
	if body != nil {
		request.Header.Set("Content-Type", contentType)
	}
	request.Header.Set("Accept", "application/json")
	return request, nil
}

func buildSnapshotHTTPBody(
	config *httpBodyConfig,
	input map[string]any,
	consumed map[string]bool,
	parameterValues map[string]any,
) (io.Reader, string, error) {
	var value any
	contentType := "application/json"
	if config != nil {
		if strings.TrimSpace(config.ContentType) != "" {
			contentType = strings.TrimSpace(config.ContentType)
		}
		switch {
		case strings.TrimSpace(config.Input) != "":
			var exists bool
			value, exists = input[strings.TrimSpace(config.Input)]
			if !exists {
				return nil, "", execution.NewError(execution.ErrorCodeInvalidRequest, "VALIDATION", false, 0, nil)
			}
		case len(config.Parameters) > 0:
			body := make(map[string]any, len(config.Parameters))
			for _, name := range config.Parameters {
				name = strings.TrimSpace(name)
				item, exists := input[name]
				if !exists {
					return nil, "", execution.NewError(execution.ErrorCodeInvalidRequest, "VALIDATION", false, 0, nil)
				}
				body[name] = item
			}
			value = body
		}
	}
	if value == nil && len(parameterValues) > 0 {
		value = parameterValues
	}
	if value == nil {
		if body, exists := input["body"]; exists && !consumed["body"] {
			value = body
		}
	}
	if value == nil {
		return nil, "", nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, "", execution.NewError(execution.ErrorCodeInvalidRequest, "VALIDATION", false, 0, err)
	}
	return bytes.NewReader(payload), contentType, nil
}

func readLimitedHTTPResponse(body io.Reader, limit int64) ([]byte, error) {
	reader := &io.LimitedReader{R: body, N: limit + 1}
	payload, err := io.ReadAll(reader)
	if err != nil {
		return nil, execution.NewError(execution.ErrorCodeResponseRead, "UPSTREAM", true, 0, err)
	}
	if int64(len(payload)) > limit {
		return nil, execution.NewError(execution.ErrorCodeResponseTooLarge, "POLICY", false, 0, nil)
	}
	return payload, nil
}

func normalizeHTTPResponse(payload []byte) json.RawMessage {
	if len(payload) == 0 {
		return json.RawMessage(`null`)
	}
	if json.Valid(payload) {
		buffer := bytes.NewBuffer(make([]byte, 0, len(payload)))
		if json.Compact(buffer, payload) == nil {
			return append(json.RawMessage(nil), buffer.Bytes()...)
		}
	}
	encoded, _ := json.Marshal(string(payload))
	return encoded
}

func normalizeHTTPTransportError(ctx context.Context, err error) error {
	var executionError *execution.Error
	if errors.As(err, &executionError) {
		return executionError
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return execution.NewError(execution.ErrorCodeTimeout, "TIMEOUT", true, 0, context.DeadlineExceeded)
	case errors.Is(ctx.Err(), context.Canceled):
		return execution.NewError(execution.ErrorCodeCanceled, "CANCELED", false, 0, context.Canceled)
	default:
		return execution.NewError(execution.ErrorCodeUpstream, "UPSTREAM", true, 0, err)
	}
}

func resolvedEgressPolicy(policy execution.EgressPolicy, endpoint *url.URL) (execution.EgressPolicy, error) {
	resolved := execution.EgressPolicy{
		AllowedHosts: append([]string(nil), policy.AllowedHosts...),
		AllowedPorts: append([]int(nil), policy.AllowedPorts...),
		AllowedCIDRs: append([]string(nil), policy.AllowedCIDRs...),
		MaxRedirects: policy.MaxRedirects,
	}
	if endpoint == nil || endpoint.Hostname() == "" {
		return resolved, execution.NewError(execution.ErrorCodeEgressDenied, "POLICY", false, 0, nil)
	}
	if len(resolved.AllowedHosts) == 0 {
		resolved.AllowedHosts = []string{endpoint.Hostname()}
	}
	if len(resolved.AllowedPorts) == 0 {
		port := 80
		if endpoint.Scheme == "https" {
			port = 443
		}
		if endpoint.Port() != "" {
			parsed, err := strconv.Atoi(endpoint.Port())
			if err != nil {
				return resolved, execution.NewError(execution.ErrorCodeEgressDenied, "POLICY", false, 0, err)
			}
			port = parsed
		}
		resolved.AllowedPorts = []int{port}
	}
	return resolved, nil
}

func emitInvocationEvent(ctx context.Context, sink execution.InvocationEventSink, invocationID, eventType, errorCode string) error {
	if sink == nil {
		return nil
	}
	return sink.Emit(ctx, execution.InvocationEvent{
		InvocationID: invocationID, Type: eventType, ErrorCode: errorCode, OccurredAt: time.Now().UTC(),
	})
}

func validHTTPMethod(method string) bool {
	if method == "" {
		return false
	}
	for _, character := range method {
		if character > unicode.MaxASCII || !isHTTPTokenByte(byte(character)) {
			return false
		}
	}
	return method != http.MethodConnect && method != http.MethodTrace
}

func validHTTPHeaderName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "Host") {
		return false
	}
	for index := 0; index < len(name); index++ {
		if !isHTTPTokenByte(name[index]) {
			return false
		}
	}
	return true
}

func isHTTPTokenByte(value byte) bool {
	if value >= '0' && value <= '9' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' {
		return true
	}
	return strings.ContainsRune("!#$%&'*+-.^_`|~", rune(value))
}

func pathPlaceholders(path string) []string {
	values := make([]string, 0)
	for {
		start := strings.IndexByte(path, '{')
		if start < 0 {
			return values
		}
		end := strings.IndexByte(path[start+1:], '}')
		if end < 0 {
			return values
		}
		name := path[start+1 : start+1+end]
		if name != "" {
			values = append(values, name)
		}
		path = path[start+end+2:]
	}
}

func addURLValues(values url.Values, name string, value any) {
	switch items := value.(type) {
	case []any:
		for _, item := range items {
			values.Add(name, fmt.Sprint(item))
		}
	case []string:
		for _, item := range items {
			values.Add(name, item)
		}
	default:
		values.Set(name, fmt.Sprint(value))
	}
}

func joinEscapedPath(basePath, actionPath string) string {
	basePath = strings.TrimRight(basePath, "/")
	actionPath = strings.TrimLeft(actionPath, "/")
	if basePath == "" {
		return "/" + actionPath
	}
	return basePath + "/" + actionPath
}

func setEscapedURLPath(endpoint *url.URL, escapedPath string) {
	decoded, err := url.PathUnescape(escapedPath)
	if err != nil {
		endpoint.Path = escapedPath
		endpoint.RawPath = ""
		return
	}
	endpoint.Path = decoded
	endpoint.RawPath = escapedPath
}
