package modelapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/eino/schema/openai"
	"github.com/openai/openai-go/v3/responses"

	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/modelconfig"
)

func TestMapAgenticOptionsReasoning(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		raw     string
		want    responses.ReasoningEffort
		apply   bool
		wantErr string
	}{
		{name: "empty defaults high", raw: "", want: responses.ReasoningEffortHigh, apply: true},
		{name: "null defaults high", raw: "null", want: responses.ReasoningEffortHigh, apply: true},
		{name: "omitted key defaults high", raw: `{}`, want: responses.ReasoningEffortHigh, apply: true},
		{name: "high", raw: `{"reasoningEffort":"high"}`, want: responses.ReasoningEffortHigh, apply: true},
		{name: "medium case", raw: `{"reasoningEffort":"MEDIUM"}`, want: responses.ReasoningEffortMedium, apply: true},
		{name: "low", raw: `{"reasoningEffort":"low"}`, want: responses.ReasoningEffortLow, apply: true},
		{name: "minimal", raw: `{"reasoningEffort":"minimal"}`, want: responses.ReasoningEffortMinimal, apply: true},
		{name: "xhigh", raw: `{"reasoningEffort":"xhigh"}`, want: responses.ReasoningEffortXhigh, apply: true},
		{name: "none skips", raw: `{"reasoningEffort":"none"}`, apply: false},
		{name: "off skips", raw: `{"reasoningEffort":"off"}`, apply: false},
		{name: "unknown fails closed", raw: `{"reasoningEffort":"extreme"}`, wantErr: "invalid reasoningEffort"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := mapAgenticOptions(json.RawMessage(tc.raw))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v want contains %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !tc.apply {
				if got.Reasoning != nil {
					t.Fatalf("expected no reasoning, got %#v", got.Reasoning)
				}
				return
			}
			if got.Reasoning == nil || got.Reasoning.Effort != tc.want {
				t.Fatalf("got %#v want effort %q", got.Reasoning, tc.want)
			}
		})
	}
}

func TestMapAgenticOptionsValidation(t *testing.T) {
	t.Parallel()
	if _, err := mapAgenticOptions(json.RawMessage(`{"temperature":3}`)); err == nil || !strings.Contains(err.Error(), "temperature") {
		t.Fatalf("expected temperature range error, got %v", err)
	}
	if _, err := mapAgenticOptions(json.RawMessage(`{"topP":1.5}`)); err == nil || !strings.Contains(err.Error(), "topP") {
		t.Fatalf("expected topP range error, got %v", err)
	}
	if _, err := mapAgenticOptions(json.RawMessage(`{"maxTokens":0}`)); err == nil || !strings.Contains(err.Error(), "maxTokens") {
		t.Fatalf("expected maxTokens error, got %v", err)
	}
	if _, err := mapAgenticOptions(json.RawMessage(`{"unknownKey":1}`)); err == nil || !strings.Contains(err.Error(), "unknown option") {
		t.Fatalf("expected unknown option error, got %v", err)
	}
	if _, err := mapAgenticOptions(json.RawMessage(`{"parallelToolCalls":true}`)); !errors.Is(err, ErrAgenticParallelToolCallsFixed) {
		t.Fatalf("expected fixed parallel error, got %v", err)
	}
	if _, err := mapAgenticOptions(json.RawMessage(`{"parallel_tool_calls":true}`)); !errors.Is(err, ErrAgenticParallelToolCallsFixed) {
		t.Fatalf("expected fixed parallel snake error, got %v", err)
	}
	if _, err := mapAgenticOptions(json.RawMessage(`{"apiVersion":"2024-10-21"}`)); !errors.Is(err, ErrAgenticAPIVersionUnsupported) {
		t.Fatalf("expected apiVersion error, got %v", err)
	}
	if _, err := mapAgenticOptions(json.RawMessage(`{"api_version":"2024-10-21"}`)); !errors.Is(err, ErrAgenticAPIVersionUnsupported) {
		t.Fatalf("expected api_version error, got %v", err)
	}
	if _, err := mapAgenticOptions(json.RawMessage(`{"topP":0.5,"top_p":0.5}`)); !errors.Is(err, ErrAgenticOptionAliasConflict) {
		t.Fatalf("expected alias conflict, got %v", err)
	}
	if _, err := mapAgenticOptions(json.RawMessage(`{"maxTokens":10,"max_tokens":10}`)); !errors.Is(err, ErrAgenticOptionAliasConflict) {
		t.Fatalf("expected maxTokens alias conflict, got %v", err)
	}
	if _, err := mapAgenticOptions(json.RawMessage(`{"parallelToolCalls":false,"parallel_tool_calls":false}`)); !errors.Is(err, ErrAgenticOptionAliasConflict) {
		t.Fatalf("expected parallel alias conflict, got %v", err)
	}

	got, err := mapAgenticOptions(json.RawMessage(`{"temperature":0.2,"top_p":0.9,"max_tokens":128,"parallel_tool_calls":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Temperature == nil || *got.Temperature != 0.2 {
		t.Fatalf("temperature=%v", got.Temperature)
	}
	if got.TopP == nil || *got.TopP != 0.9 {
		t.Fatalf("topP=%v", got.TopP)
	}
	if got.MaxTokens == nil || *got.MaxTokens != 128 {
		t.Fatalf("maxTokens=%v", got.MaxTokens)
	}
}

func TestNewOpenAIAgenticModelValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	secrets := secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})
	if _, err := NewOpenAIAgenticModel(ctx, nil, nil, testConfig("http://example.com/v1")); err == nil {
		t.Fatal("expected secrets required")
	}
	if _, err := NewOpenAIAgenticModel(ctx, nil, secrets, modelconfig.Config{ModelName: "m"}); err == nil ||
		!strings.Contains(err.Error(), "API base") {
		t.Fatalf("expected API base error, got %v", err)
	}
	if _, err := NewOpenAIAgenticModel(ctx, nil, secrets, modelconfig.Config{APIBase: "http://example.com/v1"}); err == nil ||
		!strings.Contains(err.Error(), "model name") {
		t.Fatalf("expected model name error, got %v", err)
	}
	cfg := testConfig("http://example.com/v1")
	cfg.Options = json.RawMessage(`{"reasoningEffort":"extreme"}`)
	if _, err := NewOpenAIAgenticModel(ctx, nil, secrets, cfg); err == nil || !strings.Contains(err.Error(), "reasoningEffort") {
		t.Fatalf("expected reasoningEffort validation error, got %v", err)
	}
}

// TestValidateAgenticAPIBase covers strict construction-time API base rules.
// openai-go applies WithBaseURL lazily; these checks must reject bad bases
// before adapter construction / network.
func TestValidateAgenticAPIBase(t *testing.T) {
	t.Parallel()

	valid := []string{
		"https://api.openai.com/v1",
		"https://api.openai.com/v1/",
		"http://localhost:8080/v1",
		"https://gateway.example.com/openai/v1",
		"http://127.0.0.1:9000",
		"https://api.example.com", // no path is fine
		"https://[::1]/v1",
		"https://[2001:db8::1]:8443/v1",
		"http://192.168.0.1:1/v1",
		"https://api.example.com:65535/v1/",
	}
	for _, base := range valid {
		base := base
		t.Run("valid_"+base, func(t *testing.T) {
			t.Parallel()
			got, err := validateAgenticAPIBase(base)
			if err != nil {
				t.Fatalf("err=%v", err)
			}
			if got != base {
				t.Fatalf("got %q want %q", got, base)
			}
		})
	}

	// Whitespace is trimmed; returned value is trimmed form.
	got, err := validateAgenticAPIBase("  https://api.openai.com/v1  ")
	if err != nil || got != "https://api.openai.com/v1" {
		t.Fatalf("trim: got=%q err=%v", got, err)
	}

	invalid := []struct {
		name string
		base string
		// substrings that must NOT appear in the error (sensitive leak check)
		mustNotContain []string
	}{
		{name: "empty", base: ""},
		{name: "whitespace", base: "   "},
		{name: "relative path", base: "/v1"},
		{name: "relative hostless", base: "example.com/v1"},
		{name: "scheme relative", base: "//example.com/v1"},
		{name: "ftp scheme", base: "ftp://example.com/v1"},
		{name: "file scheme", base: "file:///tmp/x"},
		{name: "javascript scheme", base: "javascript:alert(1)"},
		{name: "missing host", base: "http://"},
		{name: "https missing host", base: "https://"},
		{name: "empty hostname with port", base: "https://:443/v1"},
		{name: "userinfo password", base: "https://user:s3cret-pass@api.example.com/v1", mustNotContain: []string{"s3cret-pass", "user:s3cret"}},
		{name: "userinfo user only", base: "https://apiuser@api.example.com/v1", mustNotContain: []string{"apiuser@"}},
		{name: "query string", base: "https://api.example.com/v1?api_key=sk-leaked"},
		{name: "query empty value", base: "https://api.example.com/v1?"},
		{name: "fragment", base: "https://api.example.com/v1#section"},
		{name: "query and fragment", base: "https://api.example.com/v1?x=1#y"},
		{name: "malformed", base: "https://exa mple.com/v1"},
		{name: "port out of range high", base: "https://example.com:99999/v1"},
		{name: "port zero", base: "https://example.com:0/v1"},
		{name: "port non-numeric", base: "https://example.com:abc/v1"},
		{name: "control char in host", base: "https://exam\x00ple.com/v1"},
		{name: "opaque form", base: "https:example.com/v1"},
	}
	for _, tc := range invalid {
		tc := tc
		t.Run("invalid_"+tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := validateAgenticAPIBase(tc.base)
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.base == "" || strings.TrimSpace(tc.base) == "" {
				// Empty keeps the historical non-typed message.
				if !strings.Contains(err.Error(), "API base is required") {
					t.Fatalf("empty: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrAgenticInvalidAPIBase) {
				t.Fatalf("err=%v want ErrAgenticInvalidAPIBase", err)
			}
			msg := err.Error()
			for _, leak := range tc.mustNotContain {
				if leak != "" && strings.Contains(msg, leak) {
					t.Fatalf("error leaked sensitive component %q: %v", leak, err)
				}
			}
			// Never echo the supplied URL (including userinfo-bearing forms).
			if strings.Contains(msg, tc.base) {
				t.Fatalf("error echoed supplied URL: %v", err)
			}
		})
	}
}

func TestNewOpenAIAgenticModelRejectsInvalidAPIBaseBeforeNetwork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cases := []struct {
		name string
		base string
		leak []string
	}{
		{name: "relative", base: "/v1"},
		{name: "userinfo", base: "https://user:super-secret-token@evil.example/v1", leak: []string{"super-secret-token", "user:super"}},
		{name: "query", base: "https://api.example.com/v1?key=sk-query-secret"},
		{name: "fragment", base: "https://api.example.com/v1#frag"},
		{name: "ftp", base: "ftp://api.example.com/v1"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Per-subtest opener: must not be called for invalid bases (proves
			// validation happens before credential resolution / construction).
			var opened atomic.Int64
			secrets := secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
				opened.Add(1)
				return errors.New("secrets must not be opened for invalid API base")
			})
			cfg := testConfig(tc.base)
			_, err := NewOpenAIAgenticModel(ctx, NewStreamingHTTPClient(), secrets, cfg)
			if err == nil {
				t.Fatal("expected construction error")
			}
			if !errors.Is(err, ErrAgenticInvalidAPIBase) {
				t.Fatalf("err=%v want ErrAgenticInvalidAPIBase", err)
			}
			if opened.Load() != 0 {
				t.Fatalf("secret opener called %d times; validation must run first", opened.Load())
			}
			for _, leak := range tc.leak {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("error leaked %q: %v", leak, err)
				}
			}
		})
	}

	// Valid base still constructs (no network call at construction time).
	t.Run("valid_constructs", func(t *testing.T) {
		t.Parallel()
		okSecrets := secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
			return nil
		})
		m, err := NewOpenAIAgenticModel(ctx, NewStreamingHTTPClient(), okSecrets, testConfig("https://api.example.com/v1"))
		if err != nil || m == nil {
			t.Fatalf("valid base construct: m=%v err=%v", m, err)
		}
	})
}

func TestNewOpenAIAgenticModelRejectsAzure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	secrets := secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})
	for _, provider := range []string{"azure", "azure_openai", "Azure_OpenAI", "azure-openai"} {
		cfg := testConfig("https://example.openai.azure.com")
		cfg.Provider = provider
		_, err := NewOpenAIAgenticModel(ctx, nil, secrets, cfg)
		if !errors.Is(err, ErrAgenticAzureUnsupported) {
			t.Fatalf("provider=%q: got %v want ErrAgenticAzureUnsupported", provider, err)
		}
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "sk-") {
			t.Fatalf("secret leaked in azure error: %v", err)
		}
	}
}

func TestNewOpenAIAgenticModelWithSecretAndEmptyKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	secretID := "019f8f43-5b4d-7ac5-acb2-c74434338e99"
	cfg := testConfig("http://example.com/v1")
	cfg.WorkspaceID = "019f8f43-5b4d-7ac5-acb2-c74434338e97"
	cfg.CredentialSecretID = &secretID
	cfg.Options = json.RawMessage(`{"reasoningEffort":"high"}`)

	var opened bool
	secrets := secretOpenerFunc(func(_ context.Context, workspaceID, id string, use func([]byte) error) error {
		opened = true
		if workspaceID != cfg.WorkspaceID || id != secretID {
			t.Fatalf("secret open ids: workspace=%s id=%s", workspaceID, id)
		}
		return use([]byte("sk-test-key"))
	})
	m, err := NewOpenAIAgenticModel(ctx, NewStreamingHTTPClient(), secrets, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if m == nil || !opened {
		t.Fatalf("m=%v opened=%v", m, opened)
	}
	if _, ok := m.(*guardedAgenticModel); !ok {
		t.Fatalf("expected guardedAgenticModel, got %T", m)
	}

	// Empty credential path (compatible gateways).
	cfgNoSecret := testConfig("http://example.com/v1")
	m2, err := NewOpenAIAgenticModel(ctx, NewStreamingHTTPClient(), secrets, cfgNoSecret)
	if err != nil || m2 == nil {
		t.Fatalf("empty key path: m=%v err=%v", m2, err)
	}
}

func TestNewOpenAIAgenticModelResponsesRequestSemantics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var gotBody map[string]any
	var gotAuth string
	var gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			http.Error(w, "bad", 400)
			return
		}
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Errorf("decode body: %v body=%s", err, string(raw))
			http.Error(w, "bad", 400)
			return
		}
		assertProtectedRequestFields(t, gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalResponsesJSON("hello from responses")))
	}))
	t.Cleanup(server.Close)

	secretID := "sec-agentic-1"
	cfg := testConfig(server.URL + "/v1")
	cfg.CredentialSecretID = &secretID
	cfg.Options = json.RawMessage(`{"reasoningEffort":"high","temperature":0.1,"maxTokens":64}`)

	secrets := secretOpenerFunc(func(_ context.Context, _, _ string, use func([]byte) error) error {
		return use([]byte("sk-agentic-test"))
	})
	m, err := NewOpenAIAgenticModel(ctx, NewStreamingHTTPClient(), secrets, cfg)
	if err != nil {
		t.Fatal(err)
	}

	msg, err := m.Generate(ctx, []*schema.AgenticMessage{agenticmsg.UserText("ping")})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	text, err := agenticmsg.ExtractAssistantText(msg)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if text != "hello from responses" {
		t.Fatalf("text=%q", text)
	}
	if !strings.Contains(gotPath, "responses") {
		t.Fatalf("path=%q want responses", gotPath)
	}
	if !strings.Contains(gotAuth, "sk-agentic-test") {
		t.Fatalf("authorization missing resolved key")
	}
	if gotBody["model"] != "test-model" {
		t.Fatalf("model=%v", gotBody["model"])
	}
	reasoning, _ := gotBody["reasoning"].(map[string]any)
	if reasoning == nil || reasoning["effort"] != "high" {
		t.Fatalf("reasoning=%v", gotBody["reasoning"])
	}
	if maxOut, ok := gotBody["max_output_tokens"].(float64); !ok || maxOut != 64 {
		t.Fatalf("max_output_tokens=%v", gotBody["max_output_tokens"])
	}
	if temp, ok := gotBody["temperature"].(float64); !ok || temp < 0.099 || temp > 0.101 {
		t.Fatalf("temperature=%v", gotBody["temperature"])
	}
	usage, err := agenticmsg.Usage(msg)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.PromptTokens != 10 || usage.CachedTokens != 3 || usage.ReasoningTokens != 2 || usage.CompletionTokens != 5 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestGuardedAgenticModelAdversarialGenerateAndStream(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	type capture struct {
		mu     sync.Mutex
		bodies []map[string]any
	}
	var cap capture

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			http.Error(w, "bad", 400)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode body: %v", err)
			http.Error(w, "bad", 400)
			return
		}
		cap.mu.Lock()
		cap.bodies = append(cap.bodies, body)
		cap.mu.Unlock()
		assertProtectedRequestFields(t, body)

		// Stream requests set stream=true; respond with SSE.
		if stream, _ := body["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(minimalResponsesSSE("stream-ok")))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalResponsesJSON("generate-ok")))
	}))
	t.Cleanup(server.Close)

	secrets := secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})
	cfg := testConfig(server.URL + "/v1")
	m, err := NewOpenAIAgenticModel(ctx, NewStreamingHTTPClient(), secrets, cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Input carrying previous-response metadata that must not leak onto the wire.
	metaInput := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{Text: "prior"}),
		},
		ResponseMeta: &schema.AgenticResponseMeta{
			OpenAIExtension: &openai.ResponseMetaExtension{
				ID:                 "resp_from_message_meta",
				PreviousResponseID: "resp_prev_from_meta",
			},
		},
		Extra: map[string]any{
			"_eino_ext_agenticopenai_auto_cached": true,
		},
	}
	input := []*schema.AgenticMessage{metaInput, agenticmsg.UserText("continue")}

	// Adversarial call-time options attempting to enable store/parallel/previous_response_id.
	adversarial := []model.Option{
		agenticopenai.WithResponsesStore(true),
		agenticopenai.WithResponsesParallelToolCalls(true),
		agenticopenai.WithHeadPreviousResponseID("resp_from_option"),
		agenticopenai.WithExtraFields(map[string]any{
			"store":                true,
			"parallel_tool_calls":  true,
			"previous_response_id": "resp_from_extra_fields",
			"service_tier":         "default", // legitimate extra should survive wire rewrite only for protected keys
		}),
		// Common tool options must remain usable.
		model.WithTools([]*schema.ToolInfo{
			{Name: "lookup", Desc: "look up", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"q": {Type: schema.String, Desc: "query"},
			})},
		}),
		model.WithDeferredTools([]*schema.ToolInfo{
			{Name: "deferred_tool", Desc: "deferred", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{})},
		}),
		model.WithToolSearchTool(&schema.ToolInfo{
			Name: "tool_search",
			Desc: "search tools",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"query": {Type: schema.String, Desc: "search query"},
			}),
		}),
		WithPromptCacheKey("cache-key-adversarial"),
	}

	// --- Generate ---
	msg, err := m.Generate(ctx, input, adversarial...)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if text, err := agenticmsg.ExtractAssistantText(msg); err != nil || text != "generate-ok" {
		t.Fatalf("generate text=%v err=%v", text, err)
	}

	// --- Stream ---
	sr, err := m.Stream(ctx, input, adversarial...)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var chunks []*schema.AgenticMessage
	for {
		chunk, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("stream recv: %v", err)
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) == 0 {
		t.Fatal("expected stream chunks")
	}
	concat, err := agenticmsg.ConcatStream(chunks)
	if err != nil {
		t.Fatalf("concat: %v", err)
	}
	if text, err := agenticmsg.ExtractAssistantText(concat); err != nil || text != "stream-ok" {
		t.Fatalf("stream text=%v err=%v", text, err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.bodies) != 2 {
		t.Fatalf("captured %d bodies want 2", len(cap.bodies))
	}
	for i, body := range cap.bodies {
		assertProtectedRequestFields(t, body)
		if body["prompt_cache_key"] != "cache-key-adversarial" {
			t.Fatalf("body[%d] prompt_cache_key=%v", i, body["prompt_cache_key"])
		}
		// Tools must still be present (common options preserved).
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) < 3 {
			t.Fatalf("body[%d] tools missing/short: %v", i, body["tools"])
		}
		var sawLookup, sawDeferred, sawToolSearch bool
		for _, rawTool := range tools {
			tm, _ := rawTool.(map[string]any)
			if tm == nil {
				continue
			}
			switch tm["type"] {
			case "function":
				if tm["name"] == "lookup" {
					sawLookup = true
				}
				if tm["name"] == "deferred_tool" {
					sawDeferred = true
					if def, _ := tm["defer_loading"].(bool); !def {
						t.Fatalf("body[%d] deferred_tool missing defer_loading", i)
					}
				}
			case "tool_search":
				sawToolSearch = true
				if tm["execution"] != "client" {
					t.Fatalf("body[%d] tool_search execution=%v", i, tm["execution"])
				}
			}
		}
		if !sawLookup || !sawDeferred || !sawToolSearch {
			t.Fatalf("body[%d] tools incomplete lookup=%v deferred=%v search=%v tools=%v",
				i, sawLookup, sawDeferred, sawToolSearch, body["tools"])
		}
		// Non-protected extra field may remain.
		if body["service_tier"] != "default" {
			t.Fatalf("body[%d] service_tier=%v (legitimate extra should remain)", i, body["service_tier"])
		}
	}
}

func TestWithPromptCacheKeyOnRequest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		assertProtectedRequestFields(t, gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalResponsesJSON("ok")))
	}))
	t.Cleanup(server.Close)

	secrets := secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})
	cfg := testConfig(server.URL + "/v1")
	m, err := NewOpenAIAgenticModel(ctx, NewStreamingHTTPClient(), secrets, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Generate(ctx, []*schema.AgenticMessage{agenticmsg.UserText("hi")}, WithPromptCacheKey("run-cache-key-abc"))
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["prompt_cache_key"] != "run-cache-key-abc" {
		t.Fatalf("prompt_cache_key=%v", gotBody["prompt_cache_key"])
	}
	// Ensure helper type is model.Option
	var _ model.Option = WithPromptCacheKey("x")
}

func TestProtectedPromptCacheKey_ExtraFieldsCannotOverride_GenerateAndStream(t *testing.T) {
	// Platform-owned key on context must win over WithExtraFields and WithPromptCacheKey.
	t.Parallel()
	ctx := context.Background()
	const platformKey = "platform-owned-cache-key"
	var mu sync.Mutex
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		assertProtectedRequestFields(t, body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "responses") && r.Header.Get("Accept") == "text/event-stream" {
			// Stream path may still get non-SSE JSON from our minimal fixture;
			// agenticopenai may use stream or non-stream depending on call.
		}
		_, _ = w.Write([]byte(minimalResponsesJSON("ok")))
	}))
	t.Cleanup(server.Close)

	secrets := secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})
	m, err := NewOpenAIAgenticModel(ctx, NewStreamingHTTPClient(), secrets, testConfig(server.URL+"/v1"))
	if err != nil {
		t.Fatal(err)
	}

	adversarial := []model.Option{
		WithPromptCacheKey("attacker-option-key"),
		agenticopenai.WithExtraFields(map[string]any{
			"prompt_cache_key": "attacker-extra-fields-key",
			"store":            true,
		}),
	}
	// Platform wrapper attaches protected key (as forcedPromptCacheModel does).
	pctx := WithProtectedPromptCacheKey(ctx, platformKey)

	// Generate
	_, err = m.Generate(pctx, []*schema.AgenticMessage{agenticmsg.UserText("hi")}, adversarial...)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Stream
	sr, err := m.Stream(pctx, []*schema.AgenticMessage{agenticmsg.UserText("hi")}, adversarial...)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for {
		_, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("stream recv: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("captured %d bodies want ≥2", len(bodies))
	}
	for i, body := range bodies {
		if body["prompt_cache_key"] != platformKey {
			t.Fatalf("body[%d] prompt_cache_key=%v want platform %q (ExtraFields must not win)", i, body["prompt_cache_key"], platformKey)
		}
		if body["store"] != false {
			t.Fatalf("body[%d] store=%v", i, body["store"])
		}
	}
}

func TestProtectedPromptCacheKey_AbsentPreservesTask1Behavior(t *testing.T) {
	// Without protected context key, WithPromptCacheKey still sets the wire key
	// and ExtraFields can overwrite it (Task 1 behavior for non-platform callers).
	t.Parallel()
	ctx := context.Background()
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalResponsesJSON("ok")))
	}))
	t.Cleanup(server.Close)
	secrets := secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})
	m, err := NewOpenAIAgenticModel(ctx, NewStreamingHTTPClient(), secrets, testConfig(server.URL+"/v1"))
	if err != nil {
		t.Fatal(err)
	}
	// No protected key: ExtraFields overwrites typed option (known ExtraFields last-write).
	_, err = m.Generate(ctx, []*schema.AgenticMessage{agenticmsg.UserText("hi")},
		WithPromptCacheKey("typed-key"),
		agenticopenai.WithExtraFields(map[string]any{"prompt_cache_key": "extra-wins"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["prompt_cache_key"] != "extra-wins" {
		// Without platform protection, ExtraFields JSON-set wins — documents Task 1 residual.
		t.Fatalf("without protected key, ExtraFields should win: got %v", gotBody["prompt_cache_key"])
	}
}

func TestGuardedAgenticModelRejectsInvalidConversation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalResponsesJSON("should-not-run")))
	}))
	t.Cleanup(server.Close)

	secrets := secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})
	m, err := NewOpenAIAgenticModel(ctx, NewStreamingHTTPClient(), secrets, testConfig(server.URL+"/v1"))
	if err != nil {
		t.Fatal(err)
	}

	// Unpaired tool result must fail closed before network.
	_, err = m.Generate(ctx, []*schema.AgenticMessage{
		{
			Role: schema.AgenticRoleTypeUser,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.FunctionToolResult{
					CallID: "no-prior-call", Name: "f",
					Content: []*schema.FunctionToolResultContentBlock{
						{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: "x"}},
					},
				}),
			},
		},
	})
	if !errors.Is(err, agenticmsg.ErrUnpairedToolResult) {
		t.Fatalf("Generate unpaired: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("network hit on invalid conversation")
	}

	// Unsupported server tool must fail closed.
	_, err = m.Generate(ctx, []*schema.AgenticMessage{
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.ServerToolCall{Name: "web_search", CallID: "s1"}),
			},
		},
	})
	if !errors.Is(err, agenticmsg.ErrUnsupportedBlock) {
		t.Fatalf("Generate server tool: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("network hit on unsupported block")
	}

	// Assistant text carrying OpenAI refusal extension must fail closed with no network.
	// The pinned adapter can emit Refusal; the protocol does not project it and would
	// otherwise drop it on replay/extract.
	refusalInput := []*schema.AgenticMessage{
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.AssistantGenText{
					Text: "partial",
					OpenAIExtension: &openai.AssistantGenTextExtension{
						Refusal: &openai.OutputRefusal{Reason: "policy"},
					},
				}),
			},
		},
	}
	_, err = m.Generate(ctx, refusalInput)
	if !errors.Is(err, agenticmsg.ErrUnsupportedBlock) {
		t.Fatalf("Generate refusal: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("network hit on refusal Generate")
	}
	_, err = m.Stream(ctx, refusalInput)
	if !errors.Is(err, agenticmsg.ErrUnsupportedBlock) {
		t.Fatalf("Stream refusal: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("network hit on refusal Stream")
	}
}

func TestNewOpenAIAgenticModelSecretNotInError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	secretID := "sec-err"
	cfg := testConfig("http://127.0.0.1:1") // connection refused
	cfg.CredentialSecretID = &secretID
	const secretPlain = "sk-super-secret-value-do-not-leak"
	secrets := secretOpenerFunc(func(_ context.Context, _, _ string, use func([]byte) error) error {
		return use([]byte(secretPlain))
	})
	m, err := NewOpenAIAgenticModel(ctx, NewStreamingHTTPClient(), secrets, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Generate(ctx, []*schema.AgenticMessage{agenticmsg.UserText("x")})
	if err == nil {
		t.Fatal("expected generate error")
	}
	if strings.Contains(err.Error(), secretPlain) {
		t.Fatalf("secret leaked in error: %v", err)
	}
}

func assertProtectedRequestFields(t *testing.T, body map[string]any) {
	t.Helper()
	if store, ok := body["store"].(bool); !ok || store {
		t.Fatalf("store must be false, got %v", body["store"])
	}
	// parallel_tool_calls must be false when present; platform always forces it.
	if p, ok := body["parallel_tool_calls"].(bool); !ok || p {
		t.Fatalf("parallel_tool_calls must be false, got %v", body["parallel_tool_calls"])
	}
	if _, ok := body["previous_response_id"]; ok {
		t.Fatalf("previous_response_id must be absent, got %v", body["previous_response_id"])
	}
}

func TestResponsesGuardRejectsRedirects307And308(t *testing.T) {
	t.Parallel()

	for _, code := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			var secondHits atomic.Int64
			var secondBody snipBody
			second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				secondHits.Add(1)
				raw, _ := io.ReadAll(r.Body)
				secondBody.set(raw)
				// Intentionally a non-/responses path so a naive path-only
				// rewrite would skip guarding if the redirect were followed.
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			t.Cleanup(second.Close)

			firstHits := atomic.Int64{}
			var firstBody snipBody
			first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				firstHits.Add(1)
				raw, _ := io.ReadAll(r.Body)
				firstBody.set(raw)
				// Redirect to a different origin+path that is not /responses.
				http.Redirect(w, r, second.URL+"/evil-not-responses", code)
			}))
			t.Cleanup(first.Close)

			secrets := secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
				return nil
			})
			cfg := testConfig(first.URL + "/v1")
			// Adversarial call-time options that would be dangerous if an
			// unguarded body followed the redirect.
			m, err := NewOpenAIAgenticModel(ctx, NewStreamingHTTPClient(), secrets, cfg)
			if err != nil {
				t.Fatal(err)
			}
			_, err = m.Generate(ctx, []*schema.AgenticMessage{agenticmsg.UserText("ping")},
				agenticopenai.WithExtraFields(map[string]any{
					"store":                true,
					"parallel_tool_calls":  true,
					"previous_response_id": "resp_should_not_leak",
				}),
			)
			if err == nil {
				t.Fatal("expected redirect rejection error")
			}
			if !errors.Is(err, ErrAgenticHTTPRedirect) && !strings.Contains(err.Error(), ErrAgenticHTTPRedirect.Error()) {
				t.Fatalf("expected ErrAgenticHTTPRedirect, got %v", err)
			}
			if secondHits.Load() != 0 {
				t.Fatalf("second endpoint was reached %d times; redirect must not be followed", secondHits.Load())
			}
			if secondBody.get() != nil {
				t.Fatalf("unguarded body reached second endpoint: %s", secondBody.get())
			}
			if firstHits.Load() < 1 {
				t.Fatal("expected first endpoint to be contacted")
			}
			// First hop body must still have been guarded (store=false etc.).
			var body map[string]any
			if raw := firstBody.get(); raw != nil {
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatalf("first body not JSON: %v raw=%s", err, raw)
				}
				assertProtectedRequestFields(t, body)
			}
		})
	}
}

// snipBody is a tiny mutex-guarded body capture for parallel redirect tests.
type snipBody struct {
	mu  sync.Mutex
	raw []byte
}

func (s *snipBody) set(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.raw = append([]byte(nil), b...)
}

func (s *snipBody) get() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.raw == nil {
		return nil
	}
	return append([]byte(nil), s.raw...)
}

func TestResponsesGuardInvalidJSONDoesNotCallBase(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: `{not json`},
		{name: "JSON array", body: `[]`},
		{name: "JSON null", body: `null`},
		{name: "JSON string", body: `"hello"`},
		{name: "empty", body: ``},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var baseHits atomic.Int64
			base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				baseHits.Add(1)
				return nil, errors.New("base must not be called")
			})
			tr := &responsesGuardTransport{base: base}
			req, err := http.NewRequest(http.MethodPost, "http://example.com/v1/responses", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := tr.RoundTrip(req)
			if resp != nil {
				t.Fatalf("expected nil response, got %v", resp)
			}
			if !errors.Is(err, ErrAgenticInvalidResponsesBody) {
				t.Fatalf("err=%v want ErrAgenticInvalidResponsesBody", err)
			}
			if baseHits.Load() != 0 {
				t.Fatalf("base transport invoked %d times", baseHits.Load())
			}
		})
	}
}

func TestResponsesGuardNormalObjectRewritesAndCallsBase(t *testing.T) {
	t.Parallel()
	var gotBody map[string]any
	var gotMethod string
	var gotPath string
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotMethod = req.Method
		gotPath = req.URL.Path
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			return nil, err
		}
		// Verify GetBody returns the same guarded body.
		if req.GetBody == nil {
			return nil, errors.New("GetBody must be set for safe retry")
		}
		rc, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		retryRaw, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		var retry map[string]any
		if err := json.Unmarshal(retryRaw, &retry); err != nil {
			return nil, err
		}
		if retry["store"] != false {
			return nil, errors.New("GetBody store not false")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})
	tr := &responsesGuardTransport{base: base}
	req, err := http.NewRequest(http.MethodPost, "http://example.com/v1/responses", strings.NewReader(
		`{"store":true,"parallel_tool_calls":true,"previous_response_id":"resp_x","model":"m"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if gotMethod != http.MethodPost || !strings.HasSuffix(gotPath, "/responses") {
		t.Fatalf("method=%q path=%q", gotMethod, gotPath)
	}
	assertProtectedRequestFields(t, gotBody)
	if gotBody["model"] != "m" {
		t.Fatalf("model=%v", gotBody["model"])
	}
}

func TestWrapClientWithResponsesGuardsDoesNotMutateCallerClient(t *testing.T) {
	t.Parallel()
	orig := NewStreamingHTTPClient()
	// Ensure original starts without our reject hook so we can detect mutation.
	orig.CheckRedirect = nil
	origTransport := orig.Transport
	wrapped := wrapClientWithResponsesGuards(orig)
	if wrapped == orig {
		t.Fatal("expected shallow copy, got same pointer")
	}
	if wrapped.CheckRedirect == nil {
		t.Fatal("wrapped client must set CheckRedirect")
	}
	// Original client must not gain the reject hook or guard transport.
	if orig.CheckRedirect != nil {
		t.Fatal("caller client CheckRedirect was mutated")
	}
	if orig.Transport != origTransport {
		t.Fatal("caller client Transport pointer was mutated")
	}
	if _, ok := orig.Transport.(*responsesGuardTransport); ok {
		t.Fatal("caller client Transport was mutated to guard transport")
	}
	// Wrapped CheckRedirect rejects.
	if err := wrapped.CheckRedirect(&http.Request{}, nil); !errors.Is(err, ErrAgenticHTTPRedirect) {
		t.Fatalf("CheckRedirect=%v", err)
	}
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func minimalResponsesJSON(text string) string {
	// Minimal OpenAI Responses API object accepted by openai-go v3 decoder.
	payload := map[string]any{
		"id":     "resp_test_1",
		"object": "response",
		"status": "completed",
		"model":  "test-model",
		"output": []map[string]any{
			{
				"type":   "message",
				"id":     "msg_1",
				"status": "completed",
				"role":   "assistant",
				"content": []map[string]any{
					{
						"type":        "output_text",
						"text":        text,
						"annotations": []any{},
					},
				},
			},
		},
		"usage": map[string]any{
			"input_tokens":  10,
			"output_tokens": 5,
			"total_tokens":  15,
			"input_tokens_details": map[string]any{
				"cached_tokens": 3,
			},
			"output_tokens_details": map[string]any{
				"reasoning_tokens": 2,
			},
		},
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

// TestGuardedModelRejectsIncompleteReasoningBeforeNetwork proves Generate and
// Stream use strict ValidateConversation (never stream-fragment relaxation)
// and reject incomplete reasoning before any wire traffic.
func TestGuardedModelRejectsIncompleteReasoningBeforeNetwork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var wireCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wireCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalResponsesJSON("should-not-reach")))
	}))
	t.Cleanup(server.Close)

	secrets := secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})
	cfg := testConfig(server.URL + "/v1")
	m, err := NewOpenAIAgenticModel(ctx, NewStreamingHTTPClient(), secrets, cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Incomplete reasoning: no ResponseMeta.OpenAIExtension (would be silently
	// skipped by the pinned adapter). StreamingMeta must not relax input checks.
	incomplete := []*schema.AgenticMessage{
		agenticmsg.UserText("hi"),
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(&schema.Reasoning{Text: "think"}, &schema.StreamingMeta{Index: 0}),
			},
		},
	}

	_, err = m.Generate(ctx, incomplete)
	if err == nil || !errors.Is(err, agenticmsg.ErrMalformedBlock) {
		t.Fatalf("Generate: want ErrMalformedBlock, got %v", err)
	}
	if wireCount.Load() != 0 {
		t.Fatalf("Generate wired %d times; want 0", wireCount.Load())
	}

	_, err = m.Stream(ctx, incomplete)
	if err == nil || !errors.Is(err, agenticmsg.ErrMalformedBlock) {
		t.Fatalf("Stream: want ErrMalformedBlock, got %v", err)
	}
	if wireCount.Load() != 0 {
		t.Fatalf("Stream wired %d times; want 0", wireCount.Load())
	}

	// Cross-kind pairing also rejected before network.
	unmarkedSearchCall := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{
				CallID: "c1", Name: "lookup", Arguments: `{}`,
			}),
		},
	}
	wrongResult := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
				CallID: "c1", Name: "lookup",
				Result: &schema.ToolSearchResult{},
			}),
		},
	}
	_, err = m.Generate(ctx, []*schema.AgenticMessage{unmarkedSearchCall, wrongResult})
	if err == nil || !errors.Is(err, agenticmsg.ErrWrongKindToolResult) {
		t.Fatalf("Generate wrong-kind: %v", err)
	}
	if wireCount.Load() != 0 {
		t.Fatalf("wrong-kind wired %d times; want 0", wireCount.Load())
	}

	// Result name mismatch rejected before network (Generate and Stream).
	nameMismatchResult := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolResult{
				CallID: "c1", Name: "other",
				Content: []*schema.FunctionToolResultContentBlock{
					{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: "x"}},
				},
			}),
		},
	}
	_, err = m.Generate(ctx, []*schema.AgenticMessage{unmarkedSearchCall, nameMismatchResult})
	if err == nil || !errors.Is(err, agenticmsg.ErrToolResultNameMismatch) {
		t.Fatalf("Generate name mismatch: %v", err)
	}
	if wireCount.Load() != 0 {
		t.Fatalf("name-mismatch Generate wired %d times; want 0", wireCount.Load())
	}
	_, err = m.Stream(ctx, []*schema.AgenticMessage{unmarkedSearchCall, nameMismatchResult})
	if err == nil || !errors.Is(err, agenticmsg.ErrToolResultNameMismatch) {
		t.Fatalf("Stream name mismatch: %v", err)
	}
	if wireCount.Load() != 0 {
		t.Fatalf("name-mismatch Stream wired %d times; want 0", wireCount.Load())
	}

	// Blank result name rejected before network.
	blankNameResult := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolResult{
				CallID: "c1", Name: "",
				Content: []*schema.FunctionToolResultContentBlock{
					{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: "x"}},
				},
			}),
		},
	}
	_, err = m.Generate(ctx, []*schema.AgenticMessage{unmarkedSearchCall, blankNameResult})
	if err == nil || !errors.Is(err, agenticmsg.ErrMalformedBlock) {
		t.Fatalf("Generate blank name: %v", err)
	}
	if wireCount.Load() != 0 {
		t.Fatalf("blank-name Generate wired %d times; want 0", wireCount.Load())
	}

	// Duplicate JSON keys in tool arguments rejected before network.
	dupArgs := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{
				CallID: "c2", Name: "lookup", Arguments: `{"a":1,"a":2}`,
			}),
		},
	}
	_, err = m.Generate(ctx, []*schema.AgenticMessage{agenticmsg.UserText("hi"), dupArgs})
	if err == nil || !errors.Is(err, agenticmsg.ErrInvalidToolArguments) {
		t.Fatalf("Generate dup args: %v", err)
	}
	if wireCount.Load() != 0 {
		t.Fatalf("dup-args Generate wired %d times; want 0", wireCount.Load())
	}

	// Stream-only TextAnnotation.Index on complete input rejected before network.
	indexedAnno := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{
				Text: "cited",
				OpenAIExtension: &openai.AssistantGenTextExtension{
					Annotations: []*openai.TextAnnotation{
						{
							Index: 2,
							Type:  openai.TextAnnotationTypeURLCitation,
							URLCitation: &openai.TextAnnotationURLCitation{
								URL: "http://example.com", Title: "ex",
							},
						},
					},
				},
			}),
		},
	}
	_, err = m.Generate(ctx, []*schema.AgenticMessage{agenticmsg.UserText("hi"), indexedAnno})
	if err == nil || !errors.Is(err, agenticmsg.ErrStreamOnlyField) {
		t.Fatalf("Generate indexed annotation: want ErrStreamOnlyField, got %v", err)
	}
	if wireCount.Load() != 0 {
		t.Fatalf("indexed-anno Generate wired %d times; want 0", wireCount.Load())
	}
	_, err = m.Stream(ctx, []*schema.AgenticMessage{agenticmsg.UserText("hi"), indexedAnno})
	if err == nil || !errors.Is(err, agenticmsg.ErrStreamOnlyField) {
		t.Fatalf("Stream indexed annotation: want ErrStreamOnlyField, got %v", err)
	}
	if wireCount.Load() != 0 {
		t.Fatalf("indexed-anno Stream wired %d times; want 0", wireCount.Load())
	}

	// Stream-only ReasoningContent.Index on complete input rejected before network.
	rIdx := 0
	indexedReasoning := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.Reasoning{
				Text: "think",
				OpenAIExtension: &openai.ReasoningExtension{
					Content: []*openai.ReasoningContent{
						{Text: "raw", Index: &rIdx},
					},
				},
			}),
		},
		ResponseMeta: &schema.AgenticResponseMeta{
			OpenAIExtension: &openai.ResponseMetaExtension{ID: "resp_idx"},
		},
	}
	_, err = m.Generate(ctx, []*schema.AgenticMessage{agenticmsg.UserText("hi"), indexedReasoning})
	if err == nil || !errors.Is(err, agenticmsg.ErrStreamOnlyField) {
		t.Fatalf("Generate indexed reasoning: want ErrStreamOnlyField, got %v", err)
	}
	if wireCount.Load() != 0 {
		t.Fatalf("indexed-reasoning Generate wired %d times; want 0", wireCount.Load())
	}
	_, err = m.Stream(ctx, []*schema.AgenticMessage{agenticmsg.UserText("hi"), indexedReasoning})
	if err == nil || !errors.Is(err, agenticmsg.ErrStreamOnlyField) {
		t.Fatalf("Stream indexed reasoning: want ErrStreamOnlyField, got %v", err)
	}
	if wireCount.Load() != 0 {
		t.Fatalf("indexed-reasoning Stream wired %d times; want 0", wireCount.Load())
	}
}

func minimalResponsesSSE(text string) string {
	// Minimal SSE accepted by openai-go Responses streaming decoder.
	// response.output_text.delta carries assistant text; response.completed closes.
	delta := map[string]any{
		"type":            "response.output_text.delta",
		"content_index":   0,
		"delta":           text,
		"item_id":         "msg_1",
		"output_index":    0,
		"sequence_number": 1,
	}
	completed := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_test_stream",
			"object": "response",
			"status": "completed",
			"model":  "test-model",
			"output": []map[string]any{
				{
					"type":   "message",
					"id":     "msg_1",
					"status": "completed",
					"role":   "assistant",
					"content": []map[string]any{
						{
							"type":        "output_text",
							"text":        text,
							"annotations": []any{},
						},
					},
				},
			},
			"usage": map[string]any{
				"input_tokens":  3,
				"output_tokens": 2,
				"total_tokens":  5,
				"input_tokens_details": map[string]any{
					"cached_tokens": 0,
				},
				"output_tokens_details": map[string]any{
					"reasoning_tokens": 0,
				},
			},
		},
		"sequence_number": 2,
	}
	db, _ := json.Marshal(delta)
	cb, _ := json.Marshal(completed)
	var b strings.Builder
	b.WriteString("event: response.output_text.delta\n")
	b.WriteString("data: ")
	b.Write(db)
	b.WriteString("\n\n")
	b.WriteString("event: response.completed\n")
	b.WriteString("data: ")
	b.Write(cb)
	b.WriteString("\n\n")
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}
