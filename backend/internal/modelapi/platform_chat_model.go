package modelapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/secret"
)

const (
	// defaultModelTimeout covers non-streaming Generate body reads when the
	// client uses an overall Timeout (legacy path). Smart-dag graph turns can
	// exceed 60s under load; keep aligned with FE sendTurn (210s).
	defaultModelTimeout = 210 * time.Second
	// streamResponseHeaderTimeout bounds only the wait for response headers.
	// The full SSE body is governed by the caller's context (bridge 5m), not
	// http.Client.Timeout — an overall Client.Timeout aborts mid-stream and can
	// leave agent runs stuck if fail paths share a cancelled context.
	// Graph generation via Generate also uses this client; headers can stall
	// while the upstream model plans a large JSON graph.
	streamResponseHeaderTimeout = 210 * time.Second
	maxModelResponseBytes       = 2 << 20
	streamPipeBuffer            = 64
)

// NewStreamingHTTPClient returns an HTTP client safe for long-lived SSE streams.
// It must not set Client.Timeout (covers the whole body read). Callers should
// cancel via request context instead.
//
// When ACTWEAVE_DUMP_MODEL_HTTP=1, request bodies and whether the SSE response
// contains reasoning_content are logged to stderr (local diagnosis only).
func NewStreamingHTTPClient() *http.Client {
	base := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: streamResponseHeaderTimeout,
		// Idle/expect timeouts stay at Go defaults; body is context-bound.
	}
	var rt http.RoundTripper = base
	if strings.TrimSpace(os.Getenv("ACTWEAVE_DUMP_MODEL_HTTP")) == "1" {
		rt = &dumpModelHTTPTransport{base: base}
	}
	return &http.Client{
		Timeout:   0,
		Transport: rt,
	}
}

// dumpModelHTTPTransport logs chat completion request bodies and whether the
// upstream SSE includes reasoning_content. Enabled only via ACTWEAVE_DUMP_MODEL_HTTP.
type dumpModelHTTPTransport struct {
	base http.RoundTripper
}

func (t *dumpModelHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req != nil && req.Body != nil && strings.Contains(req.URL.Path, "chat/completions") {
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err == nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
			// Compact: flag presence of reasoning_effort / tools, not full secret body.
			hasEffort := bytes.Contains(body, []byte("reasoning_effort"))
			hasTools := bytes.Contains(body, []byte(`"tools"`))
			fmt.Fprintf(os.Stderr, "[model-http] POST %s bytes=%d reasoning_effort=%v tools=%v body=%s\n",
				req.URL.String(), len(body), hasEffort, hasTools, truncateForDump(body, 800))
		}
	}
	resp, err := base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	if strings.Contains(req.URL.Path, "chat/completions") {
		raw, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr == nil {
			rcCount := bytes.Count(raw, []byte("reasoning_content"))
			fmt.Fprintf(os.Stderr, "[model-http] response SSE bytes=%d reasoning_content_hits=%d\n",
				len(raw), rcCount)
			resp.Body = io.NopCloser(bytes.NewReader(raw))
		}
	}
	return resp, err
}

func truncateForDump(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// SecretOpener opens an active workspace secret and exposes its plaintext for
// the duration of use. *secret.Service satisfies this interface.
type SecretOpener interface {
	WithActiveSecret(ctx context.Context, workspaceID, secretID string, use func([]byte) error) error
}

// PlatformChatModel is an OpenAI-compatible ChatModel backed by modelconfig.
//
// Generate and Stream both apply model.GetCommonOptions (Tools, Temperature,
// MaxTokens, Model, ToolChoice, …). Stream talks true SSE/chunked upstream —
// never StreamReaderFromArray(Generate()) as the production default.
type PlatformChatModel struct {
	client  *http.Client
	secrets SecretOpener
	config  modelconfig.Config
	// tools are bound via WithTools (copy-on-write). Call-time model.WithTools
	// options override these when present.
	tools []*schema.ToolInfo
}

// Ensure compile-time interface satisfaction.
var (
	_ model.BaseChatModel        = (*PlatformChatModel)(nil)
	_ model.ToolCallingChatModel = (*PlatformChatModel)(nil)
	_ SecretOpener               = (*secret.Service)(nil)
)

// NewPlatformChatModel builds a ToolCallingChatModel for the given config.
// secrets is required even when the config has no CredentialSecretID so the
// secret path stays uniform for callers.
func NewPlatformChatModel(
	client *http.Client,
	secrets SecretOpener,
	config modelconfig.Config,
) (*PlatformChatModel, error) {
	if secrets == nil {
		return nil, errors.New("modelapi secrets are required")
	}
	if strings.TrimSpace(config.APIBase) == "" {
		return nil, errors.New("modelapi API base is required")
	}
	if strings.TrimSpace(config.ModelName) == "" {
		return nil, errors.New("modelapi model name is required")
	}
	if client == nil {
		// Prefer streaming-safe client: Generate still completes well under
		// ResponseHeaderTimeout + request context; Stream needs no overall Timeout.
		client = NewStreamingHTTPClient()
	}
	return &PlatformChatModel{
		client:  client,
		secrets: secrets,
		config:  config,
	}, nil
}

// WithTools returns a concurrent-safe copy with the given tools bound.
// This is not the only tool-binding path: Generate/Stream also honor
// model.WithTools via GetCommonOptions.
func (m *PlatformChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	if m == nil {
		return nil, errors.New("modelapi chat model is nil")
	}
	clone := *m
	if tools == nil {
		clone.tools = nil
	} else {
		clone.tools = append([]*schema.ToolInfo(nil), tools...)
	}
	return &clone, nil
}

// Generate performs a non-streaming OpenAI-compatible chat completion.
func (m *PlatformChatModel) Generate(
	ctx context.Context,
	input []*schema.Message,
	opts ...model.Option,
) (*schema.Message, error) {
	body, err := m.buildRequestBody(input, false, opts...)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	var result *schema.Message
	invoke := func(token []byte) error {
		respBody, status, err := m.doRequest(ctx, encoded, token)
		if err != nil {
			return err
		}
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			return fmt.Errorf("model completion returned HTTP_STATUS_%d", status)
		}
		msg, err := parseGenerateResponse(respBody)
		if err != nil {
			return err
		}
		result = msg
		return nil
	}
	if err := m.withCredential(ctx, invoke); err != nil {
		return nil, err
	}
	return result, nil
}

// Stream performs a true streaming OpenAI-compatible chat completion (SSE /
// chunked). Chunks are partial *schema.Message values suitable for
// schema.ConcatMessages.
func (m *PlatformChatModel) Stream(
	ctx context.Context,
	input []*schema.Message,
	opts ...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	body, err := m.buildRequestBody(input, true, opts...)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	// Open the HTTP response before returning the reader so setup errors
	// surface from Stream itself (not only mid-stream).
	var resp *http.Response
	open := func(token []byte) error {
		r, err := m.doStreamRequest(ctx, encoded, token)
		if err != nil {
			return err
		}
		if r.StatusCode < http.StatusOK || r.StatusCode >= http.StatusMultipleChoices {
			defer r.Body.Close()
			limited, _ := io.ReadAll(io.LimitReader(r.Body, 4<<10))
			return fmt.Errorf("model stream returned HTTP_STATUS_%d: %s", r.StatusCode, strings.TrimSpace(string(limited)))
		}
		resp = r
		return nil
	}
	if err := m.withCredential(ctx, open); err != nil {
		return nil, err
	}

	sr, sw := schema.Pipe[*schema.Message](streamPipeBuffer)
	go func() {
		defer sw.Close()
		defer resp.Body.Close()
		// Unblock body reads if the caller cancels while we are mid-stream.
		stop := context.AfterFunc(ctx, func() { _ = resp.Body.Close() })
		defer stop()
		if err := readSSEStream(ctx, resp.Body, sw); err != nil {
			// Only surface if the reader is still open; Close follows via defer.
			_ = sw.Send(nil, err)
		}
	}()
	return sr, nil
}

func (m *PlatformChatModel) withCredential(ctx context.Context, invoke func(token []byte) error) error {
	if m.config.CredentialSecretID == nil || strings.TrimSpace(*m.config.CredentialSecretID) == "" {
		return invoke(nil)
	}
	return m.secrets.WithActiveSecret(ctx, m.config.WorkspaceID, *m.config.CredentialSecretID, invoke)
}

func (m *PlatformChatModel) resolveOptions(opts ...model.Option) *model.Options {
	base := &model.Options{}
	if len(m.tools) > 0 {
		base.Tools = m.tools
	}
	if name := strings.TrimSpace(m.config.ModelName); name != "" {
		modelName := name
		base.Model = &modelName
	}
	return model.GetCommonOptions(base, opts...)
}

func (m *PlatformChatModel) buildRequestBody(
	input []*schema.Message,
	stream bool,
	opts ...model.Option,
) (map[string]any, error) {
	if len(input) == 0 {
		return nil, errors.New("model messages are required")
	}
	common := m.resolveOptions(opts...)

	messages, err := mapMessagesToOpenAI(input)
	if err != nil {
		return nil, err
	}

	modelName := strings.TrimSpace(m.config.ModelName)
	if common.Model != nil && strings.TrimSpace(*common.Model) != "" {
		modelName = strings.TrimSpace(*common.Model)
	}
	if modelName == "" {
		return nil, errors.New("model name is required")
	}

	body := map[string]any{
		"model":    modelName,
		"messages": messages,
	}
	if stream {
		body["stream"] = true
	}

	if common.Temperature != nil {
		body["temperature"] = *common.Temperature
	}
	if common.TopP != nil {
		body["top_p"] = *common.TopP
	}
	if common.MaxTokens != nil {
		body["max_tokens"] = *common.MaxTokens
	}
	if len(common.Stop) > 0 {
		body["stop"] = common.Stop
	}

	tools := common.Tools
	if len(tools) > 0 {
		wireTools, err := mapToolsToOpenAI(tools)
		if err != nil {
			return nil, err
		}
		body["tools"] = wireTools
		if choice := mapToolChoice(common.ToolChoice, common.AllowedToolNames); choice != nil {
			body["tool_choice"] = choice
		} else {
			body["tool_choice"] = "auto"
		}
	} else if common.ToolChoice != nil && *common.ToolChoice == schema.ToolChoiceForbidden {
		body["tool_choice"] = "none"
	}

	// Merge static modelconfig.Options without clobbering reserved keys.
	if len(m.config.Options) > 0 {
		var options map[string]any
		if json.Unmarshal(m.config.Options, &options) == nil {
			for key, value := range options {
				switch key {
				case "model", "messages", "tools", "tool_choice", "stream":
					continue
				default:
					if _, exists := body[key]; !exists {
						body[key] = value
					}
				}
			}
		}
	}
	return body, nil
}

func (m *PlatformChatModel) doRequest(ctx context.Context, encoded, token []byte) ([]byte, int, error) {
	target, err := modelChatCompletionsURL(m.config.APIBase)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(encoded))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if len(token) > 0 {
		req.Header.Set("Authorization", "Bearer "+string(token))
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func (m *PlatformChatModel) doStreamRequest(ctx context.Context, encoded, token []byte) (*http.Response, error) {
	target, err := modelChatCompletionsURL(m.config.APIBase)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if len(token) > 0 {
		req.Header.Set("Authorization", "Bearer "+string(token))
	}
	return m.client.Do(req)
}

// --- wire mapping ------------------------------------------------------------

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIToolCall struct {
	Index    *int               `json:"index,omitempty"`
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function openAIFunctionCall `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

func mapMessagesToOpenAI(input []*schema.Message) ([]openAIMessage, error) {
	out := make([]openAIMessage, 0, len(input))
	for i, msg := range input {
		if msg == nil {
			return nil, fmt.Errorf("model message at index %d is nil", i)
		}
		role := strings.TrimSpace(string(msg.Role))
		if role == "" {
			return nil, fmt.Errorf("model message at index %d has empty role", i)
		}
		wire := openAIMessage{
			Role:       role,
			Content:    msg.Content,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
		}
		if len(msg.ToolCalls) > 0 {
			wire.ToolCalls = make([]openAIToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				typ := strings.TrimSpace(tc.Type)
				if typ == "" {
					typ = "function"
				}
				wire.ToolCalls = append(wire.ToolCalls, openAIToolCall{
					Index: tc.Index,
					ID:    tc.ID,
					Type:  typ,
					Function: openAIFunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}
		out = append(out, wire)
	}
	return out, nil
}

func mapToolsToOpenAI(tools []*schema.ToolInfo) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(tools))
	for i, tool := range tools {
		if tool == nil {
			return nil, fmt.Errorf("tool at index %d is nil", i)
		}
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return nil, fmt.Errorf("tool at index %d has empty name", i)
		}
		fn := map[string]any{"name": name}
		if desc := strings.TrimSpace(tool.Desc); desc != "" {
			fn["description"] = desc
		}
		params, err := toolParamsJSON(tool)
		if err != nil {
			return nil, fmt.Errorf("tool %q parameters: %w", name, err)
		}
		fn["parameters"] = params
		out = append(out, map[string]any{
			"type":     "function",
			"function": fn,
		})
	}
	return out, nil
}

func toolParamsJSON(tool *schema.ToolInfo) (map[string]any, error) {
	empty := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	if tool == nil || tool.ParamsOneOf == nil {
		return empty, nil
	}
	js, err := tool.ParamsOneOf.ToJSONSchema()
	if err != nil {
		return nil, err
	}
	if js == nil {
		return empty, nil
	}
	raw, err := json.Marshal(js)
	if err != nil {
		return nil, err
	}
	// JSON Schema boolean true/false (and empty-schema round-trips that
	// re-serialize as bool) are valid in some libraries but not as OpenAI
	// function.parameters, which must be a JSON object.
	var asBool bool
	if err := json.Unmarshal(raw, &asBool); err == nil {
		return empty, nil
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	if params == nil || len(params) == 0 {
		return empty, nil
	}
	if _, ok := params["type"]; !ok {
		params["type"] = "object"
	}
	if props, ok := params["properties"]; !ok || props == nil {
		if t, _ := params["type"].(string); t == "object" {
			params["properties"] = map[string]any{}
		}
	}
	return params, nil
}

func mapToolChoice(choice *schema.ToolChoice, allowed []string) any {
	if choice == nil {
		return nil
	}
	switch *choice {
	case schema.ToolChoiceForbidden:
		return "none"
	case schema.ToolChoiceAllowed:
		return "auto"
	case schema.ToolChoiceForced:
		if len(allowed) == 1 && strings.TrimSpace(allowed[0]) != "" {
			return map[string]any{
				"type": "function",
				"function": map[string]string{
					"name": strings.TrimSpace(allowed[0]),
				},
			}
		}
		return "required"
	default:
		return "auto"
	}
}

func parseGenerateResponse(responseBody []byte) (*schema.Message, error) {
	var raw struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role             string           `json:"role"`
				Content          *string          `json:"content"`
				ReasoningContent *string          `json:"reasoning_content"`
				ToolCalls        []openAIToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &raw); err != nil || len(raw.Choices) == 0 {
		return nil, errors.New("model completion returned no content")
	}
	choice := raw.Choices[0]
	content := ""
	if choice.Message.Content != nil {
		content = *choice.Message.Content
	}
	reasoning := ""
	if choice.Message.ReasoningContent != nil {
		reasoning = *choice.Message.ReasoningContent
	}
	toolCalls := mapToolCallsFromOpenAI(choice.Message.ToolCalls)
	if content == "" && reasoning == "" && len(toolCalls) == 0 {
		return nil, errors.New("model completion returned no content")
	}
	role := schema.RoleType(strings.TrimSpace(choice.Message.Role))
	if role == "" {
		role = schema.Assistant
	}
	msg := &schema.Message{
		Role:             role,
		Content:          content,
		ReasoningContent: reasoning,
		ToolCalls:        toolCalls,
	}
	if fr := strings.TrimSpace(choice.FinishReason); fr != "" {
		msg.ResponseMeta = &schema.ResponseMeta{FinishReason: fr}
	}
	return msg, nil
}

func mapToolCallsFromOpenAI(values []openAIToolCall) []schema.ToolCall {
	if len(values) == 0 {
		return nil
	}
	out := make([]schema.ToolCall, 0, len(values))
	for _, call := range values {
		typ := strings.TrimSpace(call.Type)
		if typ == "" {
			typ = "function"
		}
		name := strings.TrimSpace(call.Function.Name)
		args := call.Function.Arguments
		// For non-stream complete responses, require a name (skip incomplete).
		// Stream deltas may carry name-less argument fragments; those use the
		// stream-specific path with Index preserved.
		if name == "" && call.Index == nil {
			continue
		}
		if name != "" && strings.TrimSpace(args) == "" {
			args = "{}"
		}
		out = append(out, schema.ToolCall{
			Index: call.Index,
			ID:    strings.TrimSpace(call.ID),
			Type:  typ,
			Function: schema.FunctionCall{
				Name:      name,
				Arguments: args,
			},
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// --- SSE stream parsing ------------------------------------------------------

func readSSEStream(ctx context.Context, body io.Reader, sw *schema.StreamWriter[*schema.Message]) error {
	reader := bufio.NewReaderSize(body, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Flush a trailing data line without final newline if present.
				if payload := strings.TrimSpace(line); strings.HasPrefix(payload, "data:") {
					if sendErr := dispatchSSEData(strings.TrimSpace(strings.TrimPrefix(payload, "data:")), sw); sendErr != nil {
						return sendErr
					}
				}
				return nil
			}
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			return nil
		}
		if err := dispatchSSEData(data, sw); err != nil {
			return err
		}
	}
}

func dispatchSSEData(data string, sw *schema.StreamWriter[*schema.Message]) error {
	msg, ok, err := parseStreamChunk([]byte(data))
	if err != nil {
		return err
	}
	if !ok || msg == nil {
		return nil
	}
	if closed := sw.Send(msg, nil); closed {
		return errors.New("model stream reader closed")
	}
	return nil
}

func parseStreamChunk(raw []byte) (*schema.Message, bool, error) {
	var chunk struct {
		Choices []struct {
			Delta struct {
				Role             string           `json:"role"`
				Content          *string          `json:"content"`
				ReasoningContent *string          `json:"reasoning_content"`
				ToolCalls        []openAIToolCall `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return nil, false, fmt.Errorf("model stream chunk decode: %w", err)
	}
	if len(chunk.Choices) == 0 {
		return nil, false, nil
	}
	choice := chunk.Choices[0]
	msg := &schema.Message{}
	has := false
	if role := strings.TrimSpace(choice.Delta.Role); role != "" {
		msg.Role = schema.RoleType(role)
		has = true
	}
	if choice.Delta.Content != nil && *choice.Delta.Content != "" {
		msg.Content = *choice.Delta.Content
		has = true
	}
	if choice.Delta.ReasoningContent != nil && *choice.Delta.ReasoningContent != "" {
		msg.ReasoningContent = *choice.Delta.ReasoningContent
		has = true
	}
	if len(choice.Delta.ToolCalls) > 0 {
		// Stream tool-call deltas must preserve Index for ConcatMessages merge;
		// do not drop name-less argument fragments.
		msg.ToolCalls = make([]schema.ToolCall, 0, len(choice.Delta.ToolCalls))
		for _, call := range choice.Delta.ToolCalls {
			typ := strings.TrimSpace(call.Type)
			msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
				Index: call.Index,
				ID:    strings.TrimSpace(call.ID),
				Type:  typ,
				Function: schema.FunctionCall{
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				},
			})
		}
		has = true
	}
	if choice.FinishReason != nil {
		if fr := strings.TrimSpace(*choice.FinishReason); fr != "" {
			msg.ResponseMeta = &schema.ResponseMeta{FinishReason: fr}
			has = true
		}
	}
	if !has {
		return nil, false, nil
	}
	// Default role so ConcatMessages can merge pure-content deltas after the
	// first role-bearing chunk; empty Role is allowed mid-stream and will
	// inherit from earlier chunks via ConcatMessages.
	return msg, true, nil
}

func modelChatCompletionsURL(apiBase string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(apiBase))
	if err != nil || parsed == nil || parsed.User != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("invalid model API base")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/chat/completions"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
