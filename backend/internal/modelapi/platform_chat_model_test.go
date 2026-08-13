package modelapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/tooltranslator"
)

func newTestPlatformChatModel(
	client *http.Client,
	secrets SecretOpener,
	config modelconfig.Config,
) (*PlatformChatModel, error) {
	return NewPlatformChatModelWithEgress(
		context.Background(), client, secrets, config, testModelEgress,
	)
}

type secretOpenerFunc func(context.Context, string, string, func([]byte) error) error

func (fn secretOpenerFunc) WithActiveSecret(
	ctx context.Context,
	workspaceID, secretID string,
	use func([]byte) error,
) error {
	return fn(ctx, workspaceID, secretID, use)
}

func testConfig(apiBase string) modelconfig.Config {
	return modelconfig.Config{
		WorkspaceID: "ws_test",
		APIBase:     apiBase,
		ModelName:   "test-model",
	}
}

func TestGenerateHonorsToolsFromOptions(t *testing.T) {
	t.Parallel()

	var sawTools atomic.Bool
	var sawTemp atomic.Bool
	var sawMaxTokens atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
			http.Error(w, "bad", 400)
			return
		}
		if body["stream"] != nil {
			t.Errorf("generate must not set stream: %#v", body["stream"])
		}
		tools, _ := body["tools"].([]any)
		if len(tools) != 1 {
			t.Errorf("expected 1 tool, got %#v", body["tools"])
		} else {
			tool := tools[0].(map[string]any)
			fn := tool["function"].(map[string]any)
			if fn["name"] != "lookup" {
				t.Errorf("unexpected tool name %#v", fn["name"])
			}
			sawTools.Store(true)
		}
		if body["tool_choice"] != "auto" {
			t.Errorf("expected tool_choice auto, got %#v", body["tool_choice"])
		}
		if body["temperature"] != 0.2 {
			// JSON numbers decode as float64
			if f, ok := body["temperature"].(float64); !ok || f != 0.2 {
				t.Errorf("expected temperature 0.2, got %#v", body["temperature"])
			} else {
				sawTemp.Store(true)
			}
		} else {
			sawTemp.Store(true)
		}
		if f, ok := body["max_tokens"].(float64); !ok || f != 128 {
			t.Errorf("expected max_tokens 128, got %#v", body["max_tokens"])
		} else {
			sawMaxTokens.Store(true)
		}
		if body["model"] != "override-model" {
			t.Errorf("expected model override, got %#v", body["model"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"finish_reason": "stop",
					"message": map[string]any{
						"role":    "assistant",
						"content": "ok",
					},
				},
			},
		})
	}))
	defer server.Close()

	cm, err := newTestPlatformChatModel(server.Client(), secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		t.Fatal("secret should not be opened without CredentialSecretID")
		return nil
	}), testConfig(server.URL+"/v1"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	tool := &schema.ToolInfo{
		Name: "lookup",
		Desc: "look things up",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"q": {Type: schema.String, Desc: "query", Required: true},
		}),
	}
	msg, err := cm.Generate(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "hi"},
	},
		model.WithTools([]*schema.ToolInfo{tool}),
		model.WithTemperature(0.2),
		model.WithMaxTokens(128),
		model.WithModel("override-model"),
	)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if msg.Content != "ok" {
		t.Fatalf("content=%q", msg.Content)
	}
	if !sawTools.Load() || !sawTemp.Load() || !sawMaxTokens.Load() {
		t.Fatalf("options not fully applied tools=%v temp=%v max=%v", sawTools.Load(), sawTemp.Load(), sawMaxTokens.Load())
	}
}

func TestStreamYieldsMultipleContentDeltas(t *testing.T) {
	t.Parallel()

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad", 400)
			return
		}
		if body["stream"] != true {
			t.Errorf("stream request must set stream=true, got %#v", body["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		// Multi-chunk SSE fixture — not a single Generate wrap.
		chunks := []string{
			`{"choices":[{"delta":{"role":"assistant","content":"Hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo "}}]}`,
			`{"choices":[{"delta":{"content":"world"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		}
		for _, c := range chunks {
			_, _ = io.WriteString(w, "data: "+c+"\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	cm, err := newTestPlatformChatModel(server.Client(), noopSecrets(), testConfig(server.URL+"/v1"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	reader, err := cm.Stream(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "hi"},
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer reader.Close()

	var parts []string
	var finish string
	for {
		chunk, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if chunk.Content != "" {
			parts = append(parts, chunk.Content)
		}
		if chunk.ResponseMeta != nil && chunk.ResponseMeta.FinishReason != "" {
			finish = chunk.ResponseMeta.FinishReason
		}
	}
	if len(parts) < 3 {
		t.Fatalf("expected multi-chunk content deltas, got %v (requestCount=%d)", parts, requestCount.Load())
	}
	if strings.Join(parts, "") != "Hello world" {
		t.Fatalf("concat content=%q parts=%v", strings.Join(parts, ""), parts)
	}
	if finish != "stop" {
		t.Fatalf("finish=%q", finish)
	}
	if requestCount.Load() != 1 {
		t.Fatalf("expected single upstream stream request, got %d", requestCount.Load())
	}
}

func TestStreamToolCallArgumentDeltasAccumulate(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search","arguments":""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"actweave\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		}
		for _, c := range chunks {
			_, _ = io.WriteString(w, "data: "+c+"\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	cm, err := newTestPlatformChatModel(server.Client(), noopSecrets(), testConfig(server.URL+"/v1"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	reader, err := cm.Stream(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "search"},
	}, model.WithTools([]*schema.ToolInfo{{Name: "search", Desc: "search"}}))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer reader.Close()

	var chunks []*schema.Message
	for {
		chunk, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) < 3 {
		t.Fatalf("expected multiple tool_call deltas, got %d", len(chunks))
	}
	merged, err := schema.ConcatMessages(chunks)
	if err != nil {
		t.Fatalf("concat: %v", err)
	}
	if len(merged.ToolCalls) != 1 {
		t.Fatalf("tool calls=%+v", merged.ToolCalls)
	}
	tc := merged.ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "search" {
		t.Fatalf("unexpected tool call %+v", tc)
	}
	if tc.Function.Arguments != `{"q":"actweave"}` {
		t.Fatalf("arguments=%q", tc.Function.Arguments)
	}
	if merged.ResponseMeta == nil || merged.ResponseMeta.FinishReason != "tool_calls" {
		t.Fatalf("finish meta=%+v", merged.ResponseMeta)
	}
}

func TestWithToolsReturnsIndependentCopy(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		// Bound-via-WithTools path (no call-time WithTools option).
		tools, _ := body["tools"].([]any)
		content := "none"
		if len(tools) > 0 {
			names := make([]string, 0, len(tools))
			for _, raw := range tools {
				fn := raw.(map[string]any)["function"].(map[string]any)
				names = append(names, fn["name"].(string))
			}
			content = strings.Join(names, ",")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": content},
			}},
		})
	}))
	defer server.Close()

	base, err := newTestPlatformChatModel(server.Client(), noopSecrets(), testConfig(server.URL+"/v1"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	withA, err := base.WithTools([]*schema.ToolInfo{{Name: "tool_a"}})
	if err != nil {
		t.Fatalf("with tools a: %v", err)
	}
	withB, err := base.WithTools([]*schema.ToolInfo{{Name: "tool_b"}})
	if err != nil {
		t.Fatalf("with tools b: %v", err)
	}
	if withA == base || withB == base || withA == withB {
		t.Fatalf("WithTools must return distinct instances")
	}

	// Mutating the slice passed to WithTools after the call must not affect the model.
	toolsC := []*schema.ToolInfo{{Name: "tool_c"}}
	withC, err := base.WithTools(toolsC)
	if err != nil {
		t.Fatalf("with tools c: %v", err)
	}
	toolsC[0] = &schema.ToolInfo{Name: "mutated"}

	msgA, err := withA.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "x"}})
	if err != nil {
		t.Fatalf("gen a: %v", err)
	}
	msgB, err := withB.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "x"}})
	if err != nil {
		t.Fatalf("gen b: %v", err)
	}
	msgC, err := withC.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "x"}})
	if err != nil {
		t.Fatalf("gen c: %v", err)
	}
	// Base without tools still works and does not pick up children tools.
	msgBase, err := base.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "x"}})
	if err != nil {
		t.Fatalf("gen base: %v", err)
	}

	if msgA.Content != "tool_a" || msgB.Content != "tool_b" || msgC.Content != "tool_c" {
		t.Fatalf("unexpected contents a=%q b=%q c=%q", msgA.Content, msgB.Content, msgC.Content)
	}
	if msgBase.Content != "none" {
		t.Fatalf("base should have no tools, content=%q", msgBase.Content)
	}
}

func TestSecretPathAttachesBearerToken(t *testing.T) {
	t.Parallel()

	var sawAuth atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth.Store(r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": "secret-ok"},
			}},
		})
	}))
	defer server.Close()

	secretID := "sec_1"
	cfg := testConfig(server.URL + "/v1")
	cfg.CredentialSecretID = &secretID
	cfg.WorkspaceID = "ws_abc"

	var openedWorkspace, openedSecret string
	secrets := secretOpenerFunc(func(_ context.Context, workspaceID, id string, use func([]byte) error) error {
		openedWorkspace = workspaceID
		openedSecret = id
		return use([]byte("tok_test_123"))
	})

	cm, err := newTestPlatformChatModel(server.Client(), secrets, cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	msg, err := cm.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if msg.Content != "secret-ok" {
		t.Fatalf("content=%q", msg.Content)
	}
	if openedWorkspace != "ws_abc" || openedSecret != "sec_1" {
		t.Fatalf("secret open ws=%q id=%q", openedWorkspace, openedSecret)
	}
	if auth, _ := sawAuth.Load().(string); auth != "Bearer tok_test_123" {
		t.Fatalf("authorization=%q", auth)
	}
}

func TestNewPlatformChatModelValidation(t *testing.T) {
	t.Parallel()
	if _, err := newTestPlatformChatModel(nil, nil, testConfig("http://example.com/v1")); err == nil {
		t.Fatal("expected secrets required")
	}
	if _, err := newTestPlatformChatModel(nil, noopSecrets(), modelconfig.Config{ModelName: "m"}); err == nil {
		t.Fatal("expected api base required")
	}
	if _, err := newTestPlatformChatModel(nil, noopSecrets(), modelconfig.Config{APIBase: "http://example.com/v1"}); err == nil {
		t.Fatal("expected model name required")
	}
}

func TestGenerateToolCallsResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "tool_calls",
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"id":   "c1",
						"type": "function",
						"function": map[string]any{
							"name":      "lookup",
							"arguments": `{"q":"x"}`,
						},
					}},
				},
			}},
		})
	}))
	defer server.Close()

	cm, err := newTestPlatformChatModel(server.Client(), noopSecrets(), testConfig(server.URL+"/v1"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	msg, err := cm.Generate(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "hi"},
	}, model.WithTools([]*schema.ToolInfo{{Name: "lookup"}}))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("tool calls=%+v", msg.ToolCalls)
	}
	if msg.ResponseMeta == nil || msg.ResponseMeta.FinishReason != "tool_calls" {
		t.Fatalf("meta=%+v", msg.ResponseMeta)
	}
}

func TestGenerateWithEmptyObjectToolSchemaDoesNotFailParameters(t *testing.T) {
	t.Parallel()
	// Regression: WORKFLOW release input_schema {} (and boolean JSON Schema)
	// used to make mapToolsToOpenAI fail with:
	//   tool "aftersales_r3_9399" parameters: cannot unmarshal bool into map
	var sawParams atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad", 400)
			return
		}
		tools, _ := body["tools"].([]any)
		if len(tools) != 1 {
			t.Errorf("expected 1 tool, got %#v", body["tools"])
		} else if tool, ok := tools[0].(map[string]any); ok {
			fn, _ := tool["function"].(map[string]any)
			params, ok := fn["parameters"].(map[string]any)
			if !ok {
				t.Errorf("parameters must be object, got %#v", fn["parameters"])
			} else {
				sawParams.Store(true)
				if params["type"] != "object" {
					t.Errorf("parameters.type=%v", params["type"])
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": "ok"},
			}},
		})
	}))
	defer server.Close()

	cm, err := newTestPlatformChatModel(server.Client(), noopSecrets(), testConfig(server.URL+"/v1"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	// Build ToolInfo the same way chatruntimebridge does for bound WORKFLOW.
	tool, err := tooltranslator.ToToolInfo(tooltranslator.NewCapability(
		"aftersales_r3_9399", "published workflow", json.RawMessage(`{}`),
	))
	if err != nil {
		t.Fatalf("ToToolInfo: %v", err)
	}
	msg, err := cm.Generate(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "你好"},
	}, model.WithTools([]*schema.ToolInfo{tool}))
	if err != nil {
		t.Fatalf("generate must not fail on empty WORKFLOW schema: %v", err)
	}
	if msg.Content != "ok" {
		t.Fatalf("content=%q", msg.Content)
	}
	if !sawParams.Load() {
		t.Fatal("expected OpenAI tools.parameters object on wire")
	}
}

func TestGenerateMapsUserInputMultiContentToImageURL(t *testing.T) {
	t.Parallel()

	var sawMulti atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		msgs, _ := body["messages"].([]any)
		if len(msgs) == 0 {
			t.Error("missing messages")
			return
		}
		user, _ := msgs[0].(map[string]any)
		content, ok := user["content"].([]any)
		if !ok || len(content) != 2 {
			t.Errorf("want multimodal content array, got %T %v", user["content"], user["content"])
			return
		}
		textPart, _ := content[0].(map[string]any)
		imgPart, _ := content[1].(map[string]any)
		if textPart["type"] != "text" || textPart["text"] != "describe" {
			t.Errorf("text part: %v", textPart)
		}
		if imgPart["type"] != "image_url" {
			t.Errorf("image part type: %v", imgPart)
		}
		imgURL, _ := imgPart["image_url"].(map[string]any)
		url, _ := imgURL["url"].(string)
		if !strings.HasPrefix(url, "data:image/png;base64,") {
			t.Errorf("image url: %q", url)
		}
		// Wire must not include external download hosts from assembly.
		if strings.Contains(url, "http://") || strings.Contains(url, "https://") {
			t.Errorf("must use data URL not http: %q", url)
		}
		sawMulti.Store(true)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": "a cat"},
			}},
		})
	}))
	defer server.Close()

	cm, err := newTestPlatformChatModel(server.Client(), noopSecrets(), testConfig(server.URL+"/v1"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	b64 := "iVBORw0KGgo="
	msg, err := cm.Generate(context.Background(), []*schema.Message{{
		Role: schema.User,
		UserInputMultiContent: []schema.MessageInputPart{
			{Type: schema.ChatMessagePartTypeText, Text: "describe"},
			{
				Type: schema.ChatMessagePartTypeImageURL,
				Image: &schema.MessageInputImage{
					MessagePartCommon: schema.MessagePartCommon{
						Base64Data: &b64,
						MIMEType:   "image/png",
					},
				},
			},
		},
	}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if msg.Content != "a cat" || !sawMulti.Load() {
		t.Fatalf("content=%q sawMulti=%v", msg.Content, sawMulti.Load())
	}
}

func TestMapMessagesRejectsUnsupportedMultimodalPart(t *testing.T) {
	t.Parallel()
	_, err := mapMessagesToOpenAI([]*schema.Message{{
		Role: schema.User,
		UserInputMultiContent: []schema.MessageInputPart{
			{Type: schema.ChatMessagePartTypeFileURL},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "unsupported multimodal") {
		t.Fatalf("want unsupported part error, got %v", err)
	}
}

func noopSecrets() SecretOpener {
	return secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return errors.New("secret open unexpected")
	})
}
