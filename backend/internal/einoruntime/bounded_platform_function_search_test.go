package einoruntime

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

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/modelapi"
	"actweave/backend/internal/modelconfig"
)

func basePlatformAgenticCfg(mdl model.AgenticModel, tools []tool.BaseTool, cat *ToolCatalogSnapshot) AgenticAgentBuildConfig {
	return AgenticAgentBuildConfig{
		Name:                     "test-platform",
		Instruction:              "You are a test agent.",
		Model:                    mdl,
		Tools:                    tools,
		Catalog:                  cat,
		MaxIterations:            0,
		ToolSearchMode:           ToolSearchModePlatformBounded,
		ClientToolSearchVerified: false,
		FunctionCallingVerified:  true,
		PromptCacheKey:           "cache-key-platform-1",
	}
}

func TestIsRedactedSearchToolName(t *testing.T) {
	t.Parallel()
	if !IsRedactedSearchToolName(ClientToolSearchToolName) {
		t.Fatal("native tool_search must be redacted")
	}
	if !IsRedactedSearchToolName(PlatformCatalogSearchToolName) {
		t.Fatal("actweave_catalog_search must be redacted")
	}
	if !IsRedactedSearchToolName("  " + PlatformCatalogSearchToolName + "  ") {
		t.Fatal("trimmed platform search name must be redacted")
	}
	if IsRedactedSearchToolName("echo_tool") {
		t.Fatal("business tool must not be treated as search")
	}
}

func TestExecuteBoundedToolSearch_PlatformMaxLoaded5(t *testing.T) {
	t.Parallel()
	names := []string{"alpha_one", "alpha_two", "beta_one", "beta_two", "gamma_one", "delta_one", "epsilon_one"}
	cat, _ := buildTestCatalog(t, names...)

	first, newly, err := executeBoundedToolSearch(cat, `{"query":"select:alpha_one,alpha_two,beta_one,beta_two,gamma_one"}`, nil, MaxLoadedToolsPerSearch)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 5 || len(newly) != 5 {
		t.Fatalf("first load: tools=%d newly=%d", len(first), len(newly))
	}
	loaded := mergeLoadedDeferredToolNames(nil, newly)
	if len(loaded) != 5 {
		t.Fatalf("loaded unique=%d", len(loaded))
	}
	// At cap: helper must not select more names.
	if _, extra, err := executeBoundedToolSearch(cat, `{"query":"select:delta_one"}`, loaded, MaxLoadedToolsPerSearch); !errors.Is(err, ErrToolSearchLoadCapExceeded) {
		t.Fatalf("at cap 5: err=%v extra=%v", err, extra)
	}
	if _, extra, err := executeBoundedToolSearch(cat, `{"query":"delta","max_results":5}`, loaded, MaxLoadedToolsPerSearch); !errors.Is(err, ErrToolSearchLoadCapExceeded) {
		t.Fatalf("at cap keyword: err=%v extra=%v", err, extra)
	}
	// Uniqueness: already-loaded names are omitted under a remaining room of 1.
	four := loaded[:4]
	got, newly2, err := executeBoundedToolSearch(cat, `{"query":"select:alpha_one,delta_one"}`, four, MaxLoadedToolsPerSearch)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(newly2) != 1 || newly2[0] != "delta_one" {
		t.Fatalf("room=1 uniqueness: got=%v newly=%v", namesOf(got), newly2)
	}
}

func TestPlatformSearchExecutor_LoadCapJSONWithoutSelecting(t *testing.T) {
	t.Parallel()
	names := []string{"a_tool", "b_tool", "c_tool", "d_tool", "e_tool", "f_tool"}
	cat, _ := buildTestCatalog(t, names...)
	mw, err := NewBoundedPlatformFunctionSearchMiddleware(cat)
	if err != nil {
		t.Fatal(err)
	}
	exec := mw.Executor().(tool.InvokableTool)

	// Seed session via runner so the executor pre-check sees 5 loaded names.
	ctx := context.Background()
	loaded := names[:5]
	mdl := &scriptedAgenticModel{
		responses: []*schema.AgenticMessage{
			agenticFunctionCall(PlatformCatalogSearchToolName, "search-cap",
				`{"query":"select:f_tool","max_results":1}`),
			agenticmsg.AssistantText("stopped"),
		},
	}
	tools := make([]tool.BaseTool, 0, len(names))
	for _, n := range names {
		tools = append(tools, &stubTool{name: n, desc: "tool " + n + " for testing", params: testParams()})
	}
	agent, err := BuildAgenticAgent(ctx, basePlatformAgenticCfg(mdl, tools, cat))
	if err != nil {
		t.Fatal(err)
	}
	var sawCap bool
	mdl.onCall = func(call int, input []*schema.AgenticMessage, _ []model.Option) {
		if call < 2 {
			return
		}
		blob := flattenAgenticInput(input)
		if strings.Contains(blob, `"code":"TOOL_SEARCH_LOAD_CAP"`) || strings.Contains(blob, ToolSearchLoadCapCode) {
			sawCap = true
		}
		if strings.Contains(blob, "f_tool") && !strings.Contains(blob, ToolSearchLoadCapCode) {
			t.Errorf("second search selected f_tool: %s", blob)
		}
	}
	runner := adk.NewTypedRunner(adk.TypedRunnerConfig[*schema.AgenticMessage]{
		Agent:           agent,
		EnableStreaming: false,
	})
	iter := runner.Run(ctx, []*schema.AgenticMessage{agenticmsg.UserText("search again")},
		adk.WithSessionValues(map[string]any{
			sessionKeyLoadedDeferredToolNames: loaded,
		}),
	)
	var runErr error
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev != nil && ev.Err != nil {
			runErr = ev.Err
		}
	}
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	if !sawCap {
		t.Fatal("expected TOOL_SEARCH_LOAD_CAP in second-turn model input")
	}
	_ = exec
}

func TestPlatformSearch_ZeroHitJSON(t *testing.T) {
	t.Parallel()
	cat, _ := buildTestCatalog(t, "weather_get")
	mw, err := NewBoundedPlatformFunctionSearchMiddleware(cat)
	if err != nil {
		t.Fatal(err)
	}
	exec := mw.Executor().(tool.InvokableTool)
	out, err := exec.InvokableRun(context.Background(), `{"query":"zzzznonexistent"}`)
	if err != nil {
		t.Fatal(err)
	}
	var got platformCatalogSearchOK
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v raw=%s", err, out)
	}
	if !got.OK || got.Count != 0 || got.LoadedNames == nil || len(got.LoadedNames) != 0 {
		t.Fatalf("zero hit: %+v raw=%s", got, out)
	}
	if strings.Contains(out, "weather_get") {
		t.Fatalf("zero hit invented a tool: %s", out)
	}
}

func TestPlatformSearch_Collision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: &stubTool{name: PlatformCatalogSearchToolName, desc: "biz"}, Exposure: ToolExposureDeferred},
	})
	if !errors.Is(err, ErrToolCatalogSearchNameCollision) {
		t.Fatalf("catalog: %v", err)
	}
}

func TestPlatformMiddleware_BeforeModelRewriteStateHidesUnloaded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	alpha := &stubTool{name: "alpha_tool", desc: "alpha", params: testParams()}
	beta := &stubTool{name: "beta_tool", desc: "beta", params: testParams()}
	ctrl := &stubTool{name: "ctrl_tool", desc: "ctrl", params: testParams()}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: alpha, Exposure: ToolExposureDeferred},
		{Tool: beta, Exposure: ToolExposureDeferred},
		{Tool: ctrl, Exposure: ToolExposureImmediate, PlatformControl: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	mw, err := NewBoundedPlatformFunctionSearchMiddleware(cat)
	if err != nil {
		t.Fatal(err)
	}
	ia, _ := alpha.Info(ctx)
	ib, _ := beta.Info(ctx)
	ic, _ := ctrl.Info(ctx)
	msg0 := schema.UserAgenticMessage("hi")
	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{
		Messages:          []*schema.AgenticMessage{msg0},
		ToolInfos:         []*schema.ToolInfo{ia, ib, ic, platformCatalogSearchToolInfo()},
		DeferredToolInfos: []*schema.ToolInfo{ia, ib},
	}
	_, out, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Messages) != 1 || out.Messages[0] != msg0 {
		t.Fatal("messages must be untouched")
	}
	if out.DeferredToolInfos != nil {
		t.Fatalf("DeferredToolInfos=%v", out.DeferredToolInfos)
	}
	seen := map[string]bool{}
	for _, ti := range out.ToolInfos {
		seen[ti.Name] = true
	}
	if !seen[PlatformCatalogSearchToolName] || !seen["ctrl_tool"] {
		t.Fatalf("visible=%v", seen)
	}
	if seen["alpha_tool"] || seen["beta_tool"] {
		t.Fatalf("unloaded deferred leaked: %v", seen)
	}
	stateBad := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{
		ToolInfos: []*schema.ToolInfo{{Name: "nope", Desc: "x"}},
	}
	if _, _, err := mw.BeforeModelRewriteState(ctx, stateBad, nil); !errors.Is(err, ErrModelToolCatalogMismatch) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestPlatformMiddleware_BeforeAgentClearsToolSearchTool(t *testing.T) {
	t.Parallel()
	cat, _ := buildTestCatalog(t, "weather_get")
	mw, err := NewBoundedPlatformFunctionSearchMiddleware(cat)
	if err != nil {
		t.Fatal(err)
	}
	runCtx := &adk.ChatModelAgentContext{
		Instruction:    "sys",
		Tools:          []tool.BaseTool{&stubTool{name: "weather_get", desc: "x"}},
		ToolSearchTool: clientToolSearchToolInfo(),
		ReturnDirectly: map[string]bool{"x": true},
	}
	origTools := runCtx.Tools
	_, out, err := mw.BeforeAgent(context.Background(), runCtx)
	if err != nil {
		t.Fatal(err)
	}
	if out == runCtx {
		t.Fatal("must copy")
	}
	if out.ToolSearchTool != nil {
		t.Fatal("ToolSearchTool must be nil")
	}
	if len(out.Tools) != 2 {
		t.Fatalf("tools len=%d", len(out.Tools))
	}
	if len(origTools) != 1 {
		t.Fatal("original Tools mutated")
	}
	if out.Instruction != "sys" {
		t.Fatalf("instruction mutated: %q", out.Instruction)
	}
}

func TestAgenticReact_PlatformSearchThenFunctionCall(t *testing.T) {
	ctx := context.Background()
	echo := &countingTool{stubTool: stubTool{name: "echo_tool", desc: "echo a message", params: testParams()}}
	other := &stubTool{name: "other_tool", desc: "other unused", params: testParams()}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: echo, Exposure: ToolExposureDeferred},
		{Tool: other, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}
	mdl := &scriptedAgenticModel{
		responses: []*schema.AgenticMessage{
			agenticFunctionCall(PlatformCatalogSearchToolName, "search-1", `{"query":"select:echo_tool","max_results":1}`),
			agenticFunctionCall("echo_tool", "call-1", `{"q":"hello"}`),
			agenticmsg.AssistantText("echoed hello"),
		},
	}
	agent, err := BuildAgenticAgent(ctx, basePlatformAgenticCfg(mdl, []tool.BaseTool{echo, other}, cat))
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewTypedRunner(adk.TypedRunnerConfig[*schema.AgenticMessage]{
		Agent:           agent,
		EnableStreaming: true,
	})
	iter := runner.Run(ctx, []*schema.AgenticMessage{agenticmsg.UserText("please echo hello")})
	var finalText string
	var errs []error
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev == nil {
			t.Fatal("nil event")
		}
		if ev.Err != nil {
			errs = append(errs, ev.Err)
			continue
		}
		if ev.Output != nil && ev.Output.MessageOutput != nil {
			msg, err := ev.Output.MessageOutput.GetMessage()
			if err == nil && msg != nil {
				if text, err := agenticmsg.ExtractAssistantText(msg); err == nil && text != "" {
					finalText = text
				}
			}
		}
	}
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if finalText != "echoed hello" {
		t.Fatalf("finalText=%q", finalText)
	}
	if echo.calls.Load() != 1 {
		t.Fatalf("echo calls=%d", echo.calls.Load())
	}
	if mdl.calls.Load() != 3 {
		t.Fatalf("model calls=%d", mdl.calls.Load())
	}
}

func TestPlatformRequestCapture_UnloadedNamesAbsent(t *testing.T) {
	ctx := context.Background()
	visible := "alpha_tool"
	hidden := []string{"beta_secret", "gamma_secret", "delta_secret"}
	var tools []tool.BaseTool
	var inputs []ToolCatalogBuildEntry
	allHidden := map[string]struct{}{}
	for _, n := range append([]string{visible}, hidden...) {
		st := &stubTool{name: n, desc: "tool " + n, params: testParams()}
		tools = append(tools, st)
		inputs = append(inputs, ToolCatalogBuildEntry{Tool: st, Exposure: ToolExposureDeferred})
		if n != visible {
			allHidden[n] = struct{}{}
		}
	}
	cat, err := BuildToolCatalog(ctx, inputs)
	if err != nil {
		t.Fatal(err)
	}

	type snap struct {
		tools     []string
		deferred  []string
		search    string
		inputText string
	}
	var (
		mu    sync.Mutex
		calls []snap
	)
	mdl := &scriptedAgenticModel{
		responses: []*schema.AgenticMessage{
			agenticFunctionCall(PlatformCatalogSearchToolName, "search-1",
				`{"query":"select:alpha_tool","max_results":1}`),
			agenticFunctionCall(visible, "call-1", `{"q":"x"}`),
			agenticmsg.AssistantText("done"),
		},
		onCall: func(_ int, input []*schema.AgenticMessage, opts []model.Option) {
			common := model.GetCommonOptions(nil, opts...)
			s := snap{inputText: flattenAgenticInput(input)}
			for _, ti := range common.Tools {
				if ti != nil {
					s.tools = append(s.tools, ti.Name)
				}
			}
			for _, ti := range common.DeferredTools {
				if ti != nil {
					s.deferred = append(s.deferred, ti.Name)
				}
			}
			if common.ToolSearchTool != nil {
				s.search = common.ToolSearchTool.Name
			}
			mu.Lock()
			calls = append(calls, s)
			mu.Unlock()
		},
	}
	agent, err := BuildAgenticAgent(ctx, basePlatformAgenticCfg(mdl, tools, cat))
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewTypedRunner(adk.TypedRunnerConfig[*schema.AgenticMessage]{
		Agent:           agent,
		EnableStreaming: false,
	})
	iter := runner.Run(ctx, []*schema.AgenticMessage{agenticmsg.UserText("use alpha")})
	var runErr error
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev != nil && ev.Err != nil {
			runErr = ev.Err
		}
	}
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) < 2 {
		t.Fatalf("calls=%d", len(calls))
	}
	for i, c := range calls {
		if c.search != "" {
			t.Fatalf("call %d ToolSearchTool=%q", i, c.search)
		}
		if len(c.deferred) != 0 {
			t.Fatalf("call %d DeferredTools=%v", i, c.deferred)
		}
		joined := strings.Join(c.tools, ",") + "\n" + c.inputText
		for name := range allHidden {
			if strings.Contains(joined, name) {
				t.Fatalf("call %d leaked unloaded %q in tools=%v text=%q", i, name, c.tools, c.inputText)
			}
		}
		hasSearch := false
		for _, n := range c.tools {
			if n == PlatformCatalogSearchToolName {
				hasSearch = true
			}
		}
		if !hasSearch {
			t.Fatalf("call %d missing search function: %v", i, c.tools)
		}
	}
	// After search, loaded business tool must appear.
	foundLoaded := false
	for _, n := range calls[len(calls)-1].tools {
		if n == visible {
			foundLoaded = true
		}
	}
	if !foundLoaded {
		t.Fatalf("loaded %q missing from later tools: %v", visible, calls[len(calls)-1].tools)
	}
}

func TestAgenticopenaiRequestCapture_PlatformCatalogSearch(t *testing.T) {
	ctx := context.Background()
	const (
		platformCacheKey = "platform-capture-cache-key"
		searchCallID     = "search-1"
		functionCallID   = "call-1"
		visible          = "alpha_tool"
		wantFinalText    = "all done"
	)
	hidden := []string{"beta_secret", "gamma_secret"}

	var tools []tool.BaseTool
	var inputs []ToolCatalogBuildEntry
	for _, n := range append([]string{visible}, hidden...) {
		st := &stubTool{name: n, desc: "tool " + n, params: testParams()}
		tools = append(tools, st)
		inputs = append(inputs, ToolCatalogBuildEntry{Tool: st, Exposure: ToolExposureDeferred})
	}
	cat, err := BuildToolCatalog(ctx, inputs)
	if err != nil {
		t.Fatal(err)
	}

	var (
		mu     sync.Mutex
		bodies []map[string]any
	)
	turn := atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(r.URL.Path, "responses") {
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err == nil {
				mu.Lock()
				bodies = append(bodies, body)
				mu.Unlock()
			}
		}
		n := turn.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1:
			_, _ = w.Write([]byte(`{
				"id":"resp_1","object":"response","status":"completed","model":"gpt-test",
				"output":[{
					"type":"function_call","id":"fc_s","call_id":"search-1",
					"name":"actweave_catalog_search","arguments":"{\"query\":\"select:alpha_tool\",\"max_results\":1}","status":"completed"
				}],
				"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}
			}`))
		case 2:
			_, _ = w.Write([]byte(`{
				"id":"resp_2","object":"response","status":"completed","model":"gpt-test",
				"output":[{
					"type":"function_call","id":"fc_1","call_id":"call-1",
					"name":"alpha_tool","arguments":"{\"q\":\"x\"}","status":"completed"
				}],
				"usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25}
			}`))
		default:
			_, _ = w.Write([]byte(`{
				"id":"resp_3","object":"response","status":"completed","model":"gpt-test",
				"output":[{
					"type":"message","id":"msg_1","role":"assistant","status":"completed",
					"content":[{"type":"output_text","text":"all done","annotations":[]}]
				}],
				"usage":{"input_tokens":30,"output_tokens":3,"total_tokens":33}
			}`))
		}
	}))
	t.Cleanup(server.Close)

	cfg := modelconfig.Config{
		Provider:  "openai",
		APIBase:   server.URL + "/v1",
		ModelName: "gpt-test",
	}
	secrets := secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})
	am, err := modelapi.NewOpenAIAgenticModel(ctx, server.Client(), secrets, cfg)
	if err != nil {
		t.Fatalf("NewOpenAIAgenticModel: %v", err)
	}
	agent, err := BuildAgenticAgent(ctx, AgenticAgentBuildConfig{
		Name:                     "platform-capture",
		Instruction:              "Help. Do not list tool names.",
		Model:                    am,
		Tools:                    tools,
		Catalog:                  cat,
		MaxIterations:            8,
		ToolSearchMode:           ToolSearchModePlatformBounded,
		FunctionCallingVerified:  true,
		ClientToolSearchVerified: false,
		PromptCacheKey:           platformCacheKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewTypedRunner(adk.TypedRunnerConfig[*schema.AgenticMessage]{
		Agent:           agent,
		EnableStreaming: false,
		CheckPointStore: newMemCheckPointStore(),
	})
	iter := runner.Run(ctx, []*schema.AgenticMessage{agenticmsg.UserText("use alpha")})
	var lastErr error
	var finalText string
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev == nil {
			continue
		}
		if ev.Err != nil {
			lastErr = ev.Err
			break
		}
		if ev.Output != nil && ev.Output.MessageOutput != nil {
			msg, err := ev.Output.MessageOutput.GetMessage()
			if err == nil && msg != nil {
				if text, err := agenticmsg.ExtractAssistantText(msg); err == nil && text != "" {
					finalText = text
				}
			}
		}
	}
	if lastErr != nil {
		t.Fatalf("run error: %v (turns=%d)", lastErr, turn.Load())
	}
	if finalText != wantFinalText {
		t.Fatalf("finalText=%q", finalText)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("captured bodies=%d", len(bodies))
	}
	for i, body := range bodies {
		assertRequiredProtectedWireFields(t, body, i, platformCacheKey)
		assertNoHostedServerSideSearch(t, body, i)
		assertNoPreviousResponseID(t, body, i)
		assertNoInputItemType(t, body, i, "tool_search_output")
		assertNoInputItemType(t, body, i, "tool_search_call")
		blob, _ := json.Marshal(body)
		text := string(blob)
		for _, name := range hidden {
			if strings.Contains(text, name) {
				t.Fatalf("body[%d] leaked unloaded %q", i, name)
			}
		}
		if strings.Contains(text, `"type":"tool_search"`) {
			t.Fatalf("body[%d] has native tool_search", i)
		}
		toolsArr, _ := body["tools"].([]any)
		if len(toolsArr) == 0 {
			t.Fatalf("body[%d] missing tools", i)
		}
		var hasSearch, hasVisible, hasDefer bool
		for _, raw := range toolsArr {
			tm, _ := raw.(map[string]any)
			name, _ := tm["name"].(string)
			typ, _ := tm["type"].(string)
			if typ == "function" && name == PlatformCatalogSearchToolName {
				hasSearch = true
			}
			if name == visible {
				hasVisible = true
			}
			if def, _ := tm["defer_loading"].(bool); def {
				hasDefer = true
			}
		}
		if !hasSearch {
			t.Fatalf("body[%d] missing %s", i, PlatformCatalogSearchToolName)
		}
		if hasDefer {
			t.Fatalf("body[%d] leaked defer_loading", i)
		}
		if i == 0 && hasVisible {
			t.Fatalf("phase 0 disclosed %s before search", visible)
		}
		if i > 0 && !hasVisible {
			t.Fatalf("body[%d] missing loaded %s", i, visible)
		}
		_ = searchCallID
		_ = functionCallID
	}
}

func namesOf(infos []*schema.ToolInfo) []string {
	out := make([]string, 0, len(infos))
	for _, info := range infos {
		if info != nil {
			out = append(out, info.Name)
		}
	}
	return out
}

func flattenAgenticInput(input []*schema.AgenticMessage) string {
	var b strings.Builder
	for _, msg := range input {
		if msg == nil {
			continue
		}
		raw, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}
