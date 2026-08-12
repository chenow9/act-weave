package einoruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/modelapi"
	"actweave/backend/internal/modelconfig"
)

// TestAgenticReact_ClientToolSearchThenFunctionCall exercises a real
// adk.NewTypedChatModelAgent ReAct loop with a fake AgenticModel:
// client tool-search call → enhanced local search result → ordinary function
// call → tool result → final assistant text. No classic schema.Message.
func TestAgenticReact_ClientToolSearchThenFunctionCall(t *testing.T) {
	ctx := context.Background()

	echo := &countingTool{stubTool: stubTool{name: "echo_tool", desc: "echo a message", params: testParams()}}
	other := &stubTool{name: "other_tool", desc: "other", params: testParams()}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: echo, Exposure: ToolExposureDeferred},
		{Tool: other, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}

	mdl := &scriptedAgenticModel{
		responses: []*schema.AgenticMessage{
			// 1) client tool-search call
			agenticFunctionCall(ClientToolSearchToolName, "search-1", `{"query":"echo","max_results":5}`),
			// 2) ordinary function call after tools loaded
			agenticFunctionCall("echo_tool", "call-1", `{"q":"hello"}`),
			// 3) final text
			agenticmsg.AssistantText("echoed hello"),
		},
	}

	agent, err := BuildAgenticAgent(ctx, baseAgenticCfg(mdl, []tool.BaseTool{echo, other}, cat))
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
		t.Fatalf("finalText = %q", finalText)
	}
	if echo.calls.Load() != 1 {
		t.Fatalf("echo tool calls = %d, want 1", echo.calls.Load())
	}
	// 3 model turns: search, function, final.
	if mdl.calls.Load() != 3 {
		t.Fatalf("model calls = %d, want 3", mdl.calls.Load())
	}
}

type countingTool struct {
	stubTool
	calls atomic.Int64
}

func (t *countingTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	t.calls.Add(1)
	return t.stubTool.InvokableRun(ctx, args, opts...)
}

// TestAgenticopenaiRequestCapture_ClientToolSearch is the mandatory wire proof
// through the real pinned agenticopenai/OpenAI adapter (fake HTTP server is
// wire capture only — not a mock production shortcut). It requires the complete
// three-phase client tool-search ReAct loop, not merely two requests:
//
//	phase 1: model emits tool_search call search-1
//	phase 2: input carries exact valid tool_search_output (call_id search-1);
//	         model emits function call call-1 for a disclosed deferred function
//	phase 3: input carries function_call_output paired to call-1;
//	         model returns final assistant text exactly "all done"
//
// Every phase requires: store:false, parallel_tool_calls:false, protected
// prompt_cache_key, no previous_response_id / hosted search fields, and a
// top-level tools array of exactly the eight deferred catalog definitions plus
// one native client tool_search (cardinality 9, no duplicates / immediates /
// recursive native leakage). The <=5 search-output result set is asserted
// separately from that top-level catalog.
func TestAgenticopenaiRequestCapture_ClientToolSearch(t *testing.T) {
	ctx := context.Background()

	const (
		platformCacheKey = "capture-cache-key"
		searchCallID     = "search-1"
		searchItemID     = "tsc_1"
		functionCallID   = "call-1"
		functionItemID   = "fc_1"
		selectedToolName = "biz_tool_0"
		wantFinalText    = "all done"
		wantPhases       = 3
		wantSearchQuery  = "business"
		wantSearchMaxRes = float64(10) // JSON numbers decode as float64
		wantFunctionArgs = `{"q":"x"}`
	)

	// Frozen wire schema for every deferred definition (testParams → ToJSONSchema).
	// Adapter serializes description and parameters exactly; missing/empty fails.
	wantFrozenParams := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"q": map[string]any{
				"type":        "string",
				"description": "query",
			},
		},
		"required": []any{"q"},
	}

	var tools []tool.BaseTool
	var inputs []ToolCatalogBuildEntry
	expectedDeferredNames := make(map[string]struct{}, 8)
	expectedDeferredDesc := make(map[string]string, 8)
	for i := 0; i < 8; i++ {
		name := "biz_tool_" + itoa(i)
		desc := "business tool " + itoa(i)
		st := &stubTool{name: name, desc: desc, params: testParams()}
		tools = append(tools, st)
		inputs = append(inputs, ToolCatalogBuildEntry{Tool: st, Exposure: ToolExposureDeferred})
		expectedDeferredNames[name] = struct{}{}
		expectedDeferredDesc[name] = desc
	}
	// Keyword "business" matches all eight; executor clamps to MaxLoadedToolsPerSearch
	// and returns canonical-name sorted slice → biz_tool_0..biz_tool_4.
	expectedSearchResultNames := map[string]struct{}{
		"biz_tool_0": {}, "biz_tool_1": {}, "biz_tool_2": {},
		"biz_tool_3": {}, "biz_tool_4": {},
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
		// Only capture Responses API bodies.
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
			// Phase 1 model response: client tool_search call search-1.
			// Arguments as object (adapter json.Marshal → args string).
			_, _ = w.Write([]byte(`{
				"id":"resp_1","object":"response","status":"completed","model":"gpt-test",
				"output":[{
					"type":"tool_search_call","id":"tsc_1","call_id":"search-1",
					"status":"completed","execution":"client",
					"arguments":{"query":"business","max_results":10}
				}],
				"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}
			}`))
		case 2:
			// Phase 2 model response: ordinary function call call-1 for disclosed deferred tool.
			_, _ = w.Write([]byte(`{
				"id":"resp_2","object":"response","status":"completed","model":"gpt-test",
				"output":[{
					"type":"function_call","id":"fc_1","call_id":"call-1",
					"name":"biz_tool_0","arguments":"{\"q\":\"x\"}","status":"completed"
				}],
				"usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25}
			}`))
		default:
			// Phase 3 model response: final assistant text.
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

	// Empty credential path is allowed for local/test gateways.
	cfg := modelconfig.Config{
		Provider:  "openai",
		APIBase:   server.URL + "/v1",
		ModelName: "gpt-test",
	}
	// SecretOpener is required even when no CredentialSecretID is set.
	secrets := secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})
	// Real pinned adapter — not a production-result mock.
	am, err := modelapi.NewOpenAIAgenticModel(ctx, server.Client(), secrets, cfg)
	if err != nil {
		t.Fatalf("NewOpenAIAgenticModel: %v", err)
	}

	agent, err := BuildAgenticAgent(ctx, AgenticAgentBuildConfig{
		Name:                     "capture-agent",
		Instruction:              "Help with business tools. Do not list all tool names.",
		Model:                    am,
		Tools:                    tools,
		Catalog:                  cat,
		MaxIterations:            8,
		ToolSearchMode:           ToolSearchModeClientBounded,
		ClientToolSearchVerified: true,
		PromptCacheKey:           platformCacheKey,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Non-streaming Generate path so the wire can return plain JSON (not SSE).
	runner := adk.NewTypedRunner(adk.TypedRunnerConfig[*schema.AgenticMessage]{
		Agent:           agent,
		EnableStreaming: false,
		CheckPointStore: newMemCheckPointStore(),
	})
	iter := runner.Run(ctx, []*schema.AgenticMessage{agenticmsg.UserText("use a business tool")})

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
		mu.Lock()
		n := len(bodies)
		mu.Unlock()
		t.Fatalf("run error: %v (captured bodies=%d turns=%d)", lastErr, n, turn.Load())
	}
	if finalText != wantFinalText {
		t.Fatalf("finalText=%q want exactly %q (turns=%d)", finalText, wantFinalText, turn.Load())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != wantPhases {
		t.Fatalf("expected exactly %d request phases for client tool-search ReAct, got %d (server turns=%d)",
			wantPhases, len(bodies), turn.Load())
	}
	if turn.Load() != int64(wantPhases) {
		t.Fatalf("server response turns=%d want exactly %d", turn.Load(), wantPhases)
	}

	// --- Phase 1 (bodies[0]): initial catalog; model will emit tool_search search-1 ---
	assertPhaseTopLevelToolsCatalog(t, bodies[0], 0, expectedDeferredNames, expectedDeferredDesc, wantFrozenParams)
	assertNoInputItemType(t, bodies[0], 0, "tool_search_output")
	assertNoInputItemType(t, bodies[0], 0, "function_call_output")

	// --- Phase 2 (bodies[1]): tool_search_output for search-1; model emits call-1 ---
	assertPhaseTopLevelToolsCatalog(t, bodies[1], 1, expectedDeferredNames, expectedDeferredDesc, wantFrozenParams)
	assertExactToolSearchOutput(t, bodies[1], 1, searchCallID, expectedSearchResultNames, expectedDeferredNames, expectedDeferredDesc, wantFrozenParams)
	assertNoInputItemType(t, bodies[1], 1, "function_call_output")
	// Phase-2 input must retain the unique exact tool_search_call for search-1.
	assertExactToolSearchCall(t, bodies[1], 1, searchItemID, searchCallID, wantSearchQuery, wantSearchMaxRes)

	// --- Phase 3 (bodies[2]): function_call_output paired to call-1; final text ---
	assertPhaseTopLevelToolsCatalog(t, bodies[2], 2, expectedDeferredNames, expectedDeferredDesc, wantFrozenParams)
	assertExactToolSearchOutput(t, bodies[2], 2, searchCallID, expectedSearchResultNames, expectedDeferredNames, expectedDeferredDesc, wantFrozenParams)
	assertExactFunctionCallOutput(t, bodies[2], 2, functionCallID)
	assertExactFunctionCall(t, bodies[2], 2, functionItemID, functionCallID, selectedToolName, wantFunctionArgs)
	// Paired tool_search_call remains exact through phase 3.
	assertExactToolSearchCall(t, bodies[2], 2, searchItemID, searchCallID, wantSearchQuery, wantSearchMaxRes)

	// Every phase: protected fields required (not merely permitted).
	for i, body := range bodies {
		assertRequiredProtectedWireFields(t, body, i, platformCacheKey)
		assertNoHostedServerSideSearch(t, body, i)
		assertNoPreviousResponseID(t, body, i)
		assertToolSearchOutputCap(t, body, i)
		assertNoAllToolNameReminder(t, body, i, tools)
		assertToolsArrayNoRecursiveNativeDisclosure(t, body, i)
	}
}

// assertPhaseTopLevelToolsCatalog requires body["tools"] to be present with
// exact membership: every deferred catalog definition (defer_loading:true) plus
// exactly one native tool_search with execution:client — nothing else.
// Each deferred definition must carry exact frozen description and parameters
// schema; missing/empty fields fail closed.
// Cardinality must be len(expectedDeferred)+1. Missing tools array fails closed.
func assertPhaseTopLevelToolsCatalog(
	t *testing.T,
	body map[string]any,
	phase int,
	expectedDeferred map[string]struct{},
	expectedDesc map[string]string,
	wantParams map[string]any,
) {
	t.Helper()
	toolsRaw, ok := body["tools"]
	if !ok {
		t.Fatalf("phase %d body missing top-level tools array: keys=%v", phase, mapKeys(body))
	}
	toolsArr, ok := toolsRaw.([]any)
	if !ok {
		t.Fatalf("phase %d body tools has wrong type %T (want []any)", phase, toolsRaw)
	}
	wantLen := len(expectedDeferred) + 1 // eight deferred + one native tool_search
	if len(toolsArr) != wantLen {
		t.Fatalf("phase %d tools cardinality=%d want exactly %d (8 deferred + 1 tool_search)", phase, len(toolsArr), wantLen)
	}
	var searchCount int
	seenDeferred := make(map[string]struct{})
	for _, raw := range toolsArr {
		tm, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("phase %d tools entry not object: %T", phase, raw)
		}
		typ, ok := tm["type"].(string)
		if !ok || typ == "" {
			t.Fatalf("phase %d tools entry missing required string type: %#v", phase, tm)
		}
		switch typ {
		case "function":
			name, ok := tm["name"].(string)
			if !ok || name == "" {
				t.Fatalf("phase %d function tool missing name: %#v", phase, tm)
			}
			if name == ClientToolSearchToolName {
				t.Fatalf("phase %d native tool_search must not appear as type=function", phase)
			}
			if _, ok := expectedDeferred[name]; !ok {
				t.Fatalf("phase %d tools has unexpected function %q (immediate/foreign leakage)", phase, name)
			}
			if _, dup := seenDeferred[name]; dup {
				t.Fatalf("phase %d tools duplicate function %q", phase, name)
			}
			seenDeferred[name] = struct{}{}
			def, ok := tm["defer_loading"].(bool)
			if !ok || !def {
				t.Fatalf("phase %d function %q defer_loading=%v want exactly true", phase, name, tm["defer_loading"])
			}
			wantDesc, ok := expectedDesc[name]
			if !ok || wantDesc == "" {
				t.Fatalf("phase %d test setup missing frozen description for %q", phase, name)
			}
			assertExactFrozenFunctionDef(t, phase, "top-level tools", tm, name, wantDesc, wantParams)
		case "tool_search":
			searchCount++
			exec, ok := tm["execution"].(string)
			if !ok || exec != "client" {
				t.Fatalf("phase %d tool_search execution=%v want exactly client", phase, tm["execution"])
			}
		default:
			t.Fatalf("phase %d tools unexpected type %q", phase, typ)
		}
	}
	if searchCount != 1 {
		t.Fatalf("phase %d tools tool_search count=%d want exactly 1", phase, searchCount)
	}
	if len(seenDeferred) != len(expectedDeferred) {
		t.Fatalf("phase %d deferred functions=%d want %d", phase, len(seenDeferred), len(expectedDeferred))
	}
	for name := range expectedDeferred {
		if _, ok := seenDeferred[name]; !ok {
			t.Fatalf("phase %d tools missing deferred function %q", phase, name)
		}
	}
}

// assertExactFrozenFunctionDef requires type=function, exact name, exact non-empty
// description, and exact parameters schema object (deep equal). Missing/empty fail.
func assertExactFrozenFunctionDef(
	t *testing.T,
	phase int,
	where string,
	tm map[string]any,
	wantName, wantDesc string,
	wantParams map[string]any,
) {
	t.Helper()
	typ, ok := tm["type"].(string)
	if !ok || typ != "function" {
		t.Fatalf("phase %d %s %q type=%v want exactly function", phase, where, wantName, tm["type"])
	}
	name, ok := tm["name"].(string)
	if !ok || name != wantName {
		t.Fatalf("phase %d %s name=%v want exactly %q", phase, where, tm["name"], wantName)
	}
	desc, ok := tm["description"].(string)
	if !ok || desc == "" {
		t.Fatalf("phase %d %s %q missing/empty description: %T %#v", phase, where, wantName, tm["description"], tm["description"])
	}
	if desc != wantDesc {
		t.Fatalf("phase %d %s %q description=%q want exactly %q", phase, where, wantName, desc, wantDesc)
	}
	params, ok := tm["parameters"].(map[string]any)
	if !ok || params == nil {
		t.Fatalf("phase %d %s %q parameters missing/wrong type %T", phase, where, wantName, tm["parameters"])
	}
	if !jsonDeepEqual(params, wantParams) {
		t.Fatalf("phase %d %s %q parameters mismatch:\n got=%s\nwant=%s",
			phase, where, wantName, mustJSON(params), mustJSON(wantParams))
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return string(b)
}

// jsonDeepEqual compares via round-trip JSON so map/slice/number forms match
// the Responses wire decode (float64 numbers, []any arrays).
func jsonDeepEqual(a, b any) bool {
	ab, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	var an, bn any
	if err := json.Unmarshal(ab, &an); err != nil {
		return false
	}
	if err := json.Unmarshal(bb, &bn); err != nil {
		return false
	}
	return jsonValuesEqual(an, bn)
}

func jsonValuesEqual(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			if !jsonValuesEqual(v, bv[k]) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !jsonValuesEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case nil:
		return b == nil
	default:
		return false
	}
}

// assertExactToolSearchOutput finds exactly one tool_search_output in input and
// requires type, execution:"client", status:"completed", call_id, and a bounded
// tools array whose members are exactly expectedSearchResults with frozen
// description and parameters. Does not conflate with top-level tools.
func assertExactToolSearchOutput(
	t *testing.T,
	body map[string]any,
	phase int,
	wantCallID string,
	expectedSearchResults map[string]struct{},
	catalogDeferred map[string]struct{},
	expectedDesc map[string]string,
	wantParams map[string]any,
) {
	t.Helper()
	input, ok := body["input"].([]any)
	if !ok {
		t.Fatalf("phase %d input missing or wrong type %T", phase, body["input"])
	}
	var found []map[string]any
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ == "tool_search_output" {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		t.Fatalf("phase %d expected exactly 1 tool_search_output in input, got %d", phase, len(found))
	}
	out := found[0]
	if typ, ok := out["type"].(string); !ok || typ != "tool_search_output" {
		t.Fatalf("phase %d tool_search_output.type=%v want tool_search_output", phase, out["type"])
	}
	if exec, ok := out["execution"].(string); !ok || exec != "client" {
		t.Fatalf("phase %d tool_search_output.execution=%v want exactly client", phase, out["execution"])
	}
	if status, ok := out["status"].(string); !ok || status != "completed" {
		t.Fatalf("phase %d tool_search_output.status=%v want exactly completed", phase, out["status"])
	}
	if callID, ok := out["call_id"].(string); !ok || callID != wantCallID {
		t.Fatalf("phase %d tool_search_output.call_id=%v want exactly %q", phase, out["call_id"], wantCallID)
	}
	toolsRaw, ok := out["tools"]
	if !ok {
		t.Fatalf("phase %d tool_search_output missing tools field", phase)
	}
	toolsOut, ok := toolsRaw.([]any)
	if !ok {
		t.Fatalf("phase %d tool_search_output.tools wrong type %T", phase, toolsRaw)
	}
	if len(toolsOut) == 0 {
		t.Fatalf("phase %d tool_search_output.tools empty (scenario must return selected deferred defs)", phase)
	}
	if len(toolsOut) > MaxLoadedToolsPerSearch {
		t.Fatalf("phase %d tool_search_output has %d tools (>%d)", phase, len(toolsOut), MaxLoadedToolsPerSearch)
	}
	if len(toolsOut) != len(expectedSearchResults) {
		t.Fatalf("phase %d tool_search_output tools cardinality=%d want exactly %d (bounded search result, not full catalog)",
			phase, len(toolsOut), len(expectedSearchResults))
	}
	seen := make(map[string]struct{})
	for _, raw := range toolsOut {
		tm, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("phase %d tool_search_output entry not object: %T", phase, raw)
		}
		name, ok := tm["name"].(string)
		if !ok || name == "" {
			t.Fatalf("phase %d tool_search_output entry missing name: %#v", phase, tm)
		}
		if name == ClientToolSearchToolName {
			t.Fatalf("phase %d tool_search_output recursively disclosed native executor", phase)
		}
		if _, ok := catalogDeferred[name]; !ok {
			t.Fatalf("phase %d tool_search_output unexpected tool %q (not in deferred catalog)", phase, name)
		}
		if _, ok := expectedSearchResults[name]; !ok {
			t.Fatalf("phase %d tool_search_output tool %q not in expected scenario result set", phase, name)
		}
		if _, dup := seen[name]; dup {
			t.Fatalf("phase %d tool_search_output duplicate %q", phase, name)
		}
		seen[name] = struct{}{}
		wantDesc, ok := expectedDesc[name]
		if !ok || wantDesc == "" {
			t.Fatalf("phase %d test setup missing frozen description for search result %q", phase, name)
		}
		assertExactFrozenFunctionDef(t, phase, "tool_search_output", tm, name, wantDesc, wantParams)
	}
	for name := range expectedSearchResults {
		if _, ok := seen[name]; !ok {
			t.Fatalf("phase %d tool_search_output missing expected selected tool %q", phase, name)
		}
	}
	// Fail closed: search-output set must not equal full catalog (would conflate layers).
	if len(expectedSearchResults) >= len(catalogDeferred) {
		t.Fatalf("test setup error: search result set must be strictly smaller than full catalog")
	}
}

// pinnedFunctionCallOutputWire is the exact agenticopenai v0.2.2 wire for a
// plain text InvokableRun result of {"ok":true}: ADK wraps the string into a
// FunctionToolResult text block; the adapter emits an input_text array (not a
// bare string, not an object, not unrelated content).
var pinnedFunctionCallOutputWire = []any{
	map[string]any{
		"type": "input_text",
		"text": `{"ok":true}`,
	},
}

// assertExactFunctionCallOutput requires exactly one function_call_output in
// input paired to wantCallID (order-sensitive call/result pairing by call_id).
// Output must equal the scenario's exact pinned wire representation of
// {"ok":true} and rejects unrelated strings/arrays/objects.
func assertExactFunctionCallOutput(t *testing.T, body map[string]any, phase int, wantCallID string) {
	t.Helper()
	input, ok := body["input"].([]any)
	if !ok {
		t.Fatalf("phase %d input missing or wrong type %T", phase, body["input"])
	}
	var found []map[string]any
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ == "function_call_output" {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		t.Fatalf("phase %d expected exactly 1 function_call_output in input, got %d", phase, len(found))
	}
	out := found[0]
	if typ, ok := out["type"].(string); !ok || typ != "function_call_output" {
		t.Fatalf("phase %d function_call_output.type=%v want exactly function_call_output", phase, out["type"])
	}
	if callID, ok := out["call_id"].(string); !ok || callID == "" {
		t.Fatalf("phase %d function_call_output.call_id missing/empty: %v", phase, out["call_id"])
	} else if callID != wantCallID {
		t.Fatalf("phase %d function_call_output.call_id=%v want exactly %q", phase, out["call_id"], wantCallID)
	}
	// Exact pinned wire: [{"type":"input_text","text":"{\"ok\":true}"}].
	// Reject bare string {"ok":true}, wrong arrays, objects, null, or empty.
	output, has := out["output"]
	if !has {
		t.Fatalf("phase %d function_call_output missing output field", phase)
	}
	if _, isStr := output.(string); isStr {
		t.Fatalf("phase %d function_call_output.output is bare string %q; want pinned input_text array", phase, output)
	}
	if output == nil {
		t.Fatalf("phase %d function_call_output.output is null", phase)
	}
	if !jsonDeepEqual(output, pinnedFunctionCallOutputWire) {
		t.Fatalf("phase %d function_call_output.output=%#v want exactly pinned %s",
			phase, output, mustJSON(pinnedFunctionCallOutputWire))
	}
}

// pinnedToolSearchCallArguments is the exact agenticopenai re-emit form for the
// scenario's tool_search_call. The adapter does json.Marshal on the decoded
// Arguments map (encoding/json sorts map keys), then re-emits as json.RawMessage
// so the wire is a single JSON object with exactly query + max_results — not a
// string, not loose multi-representation, and no extra keys.
var pinnedToolSearchCallArguments = map[string]any{
	"max_results": float64(10),
	"query":       "business",
}

// assertExactToolSearchCall requires exactly one tool_search_call in input with
// the pinned adapter wire fields: type, id, call_id, status, execution, and one
// exact arguments object. Missing/empty fields fail closed. Does not invent
// fields the adapter never sets. Does not accept multiple loose representations.
func assertExactToolSearchCall(
	t *testing.T,
	body map[string]any,
	phase int,
	wantItemID, wantCallID, wantQuery string,
	wantMaxResults float64,
) {
	t.Helper()
	input, ok := body["input"].([]any)
	if !ok {
		t.Fatalf("phase %d input missing or wrong type %T", phase, body["input"])
	}
	var found []map[string]any
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ == "tool_search_call" {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		t.Fatalf("phase %d expected exactly 1 tool_search_call in input, got %d", phase, len(found))
	}
	m := found[0]
	if typ, ok := m["type"].(string); !ok || typ != "tool_search_call" {
		t.Fatalf("phase %d tool_search_call.type=%v want exactly tool_search_call", phase, m["type"])
	}
	if id, ok := m["id"].(string); !ok || id == "" {
		t.Fatalf("phase %d tool_search_call.id missing/empty: %v", phase, m["id"])
	} else if id != wantItemID {
		t.Fatalf("phase %d tool_search_call.id=%v want exactly %q", phase, m["id"], wantItemID)
	}
	if callID, ok := m["call_id"].(string); !ok || callID == "" {
		t.Fatalf("phase %d tool_search_call.call_id missing/empty: %v", phase, m["call_id"])
	} else if callID != wantCallID {
		t.Fatalf("phase %d tool_search_call.call_id=%v want exactly %q", phase, m["call_id"], wantCallID)
	}
	if status, ok := m["status"].(string); !ok || status == "" {
		t.Fatalf("phase %d tool_search_call.status missing/empty: %v", phase, m["status"])
	} else if status != "completed" {
		t.Fatalf("phase %d tool_search_call.status=%v want exactly completed", phase, m["status"])
	}
	if exec, ok := m["execution"].(string); !ok || exec == "" {
		t.Fatalf("phase %d tool_search_call.execution missing/empty: %v", phase, m["execution"])
	} else if exec != "client" {
		t.Fatalf("phase %d tool_search_call.execution=%v want exactly client", phase, m["execution"])
	}
	// Exact pinned arguments object only — reject string/double-encoded forms
	// and any extra/missing keys beyond the scenario contract.
	argsRaw, has := m["arguments"]
	if !has {
		t.Fatalf("phase %d tool_search_call missing arguments field", phase)
	}
	if _, isStr := argsRaw.(string); isStr {
		t.Fatalf("phase %d tool_search_call.arguments is string %q; want exact pinned JSON object", phase, argsRaw)
	}
	argsObj, ok := argsRaw.(map[string]any)
	if !ok {
		t.Fatalf("phase %d tool_search_call.arguments type=%T want object map (raw=%#v)", phase, argsRaw, argsRaw)
	}
	// Build expected from scenario params; must match pinned wire keys exactly.
	wantArgs := map[string]any{
		"max_results": wantMaxResults,
		"query":       wantQuery,
	}
	if len(argsObj) != len(wantArgs) {
		t.Fatalf("phase %d tool_search_call.arguments keys=%v want exactly %v (no extras)",
			phase, mapKeys(argsObj), mapKeys(wantArgs))
	}
	if !jsonDeepEqual(argsObj, wantArgs) {
		t.Fatalf("phase %d tool_search_call.arguments=%s want exactly %s",
			phase, mustJSON(argsObj), mustJSON(wantArgs))
	}
	// Guard: pinned scenario constants must stay aligned with wantArgs.
	if !jsonDeepEqual(wantArgs, pinnedToolSearchCallArguments) {
		t.Fatalf("phase %d test setup: wantArgs diverged from pinnedToolSearchCallArguments", phase)
	}
}

// assertExactFunctionCall requires exactly one function_call in input with
// type, id, call_id, status, name, and exact arguments. Missing/empty fails.
func assertExactFunctionCall(
	t *testing.T,
	body map[string]any,
	phase int,
	wantItemID, wantCallID, wantName, wantArgs string,
) {
	t.Helper()
	input, ok := body["input"].([]any)
	if !ok {
		t.Fatalf("phase %d input missing or wrong type %T", phase, body["input"])
	}
	var found []map[string]any
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ == "function_call" {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		t.Fatalf("phase %d expected exactly 1 function_call in input, got %d", phase, len(found))
	}
	m := found[0]
	if typ, ok := m["type"].(string); !ok || typ != "function_call" {
		t.Fatalf("phase %d function_call.type=%v want exactly function_call", phase, m["type"])
	}
	if id, ok := m["id"].(string); !ok || id == "" {
		t.Fatalf("phase %d function_call.id missing/empty: %v", phase, m["id"])
	} else if id != wantItemID {
		t.Fatalf("phase %d function_call.id=%v want exactly %q", phase, m["id"], wantItemID)
	}
	if callID, ok := m["call_id"].(string); !ok || callID == "" {
		t.Fatalf("phase %d function_call.call_id missing/empty: %v", phase, m["call_id"])
	} else if callID != wantCallID {
		t.Fatalf("phase %d function_call.call_id=%v want exactly %q", phase, m["call_id"], wantCallID)
	}
	if status, ok := m["status"].(string); !ok || status == "" {
		t.Fatalf("phase %d function_call.status missing/empty: %v", phase, m["status"])
	} else if status != "completed" {
		t.Fatalf("phase %d function_call.status=%v want exactly completed", phase, m["status"])
	}
	if name, ok := m["name"].(string); !ok || name == "" {
		t.Fatalf("phase %d function_call.name missing/empty: %v", phase, m["name"])
	} else if name != wantName {
		t.Fatalf("phase %d function_call.name=%v want exactly %q", phase, m["name"], wantName)
	}
	args, ok := m["arguments"].(string)
	if !ok || args == "" {
		t.Fatalf("phase %d function_call.arguments missing/empty: %T %#v", phase, m["arguments"], m["arguments"])
	}
	// Exact arguments payload (JSON object equality, not only string equality).
	if !jsonDeepEqual(json.RawMessage(args), json.RawMessage(wantArgs)) {
		t.Fatalf("phase %d function_call.arguments=%q want exactly %q (semantic)", phase, args, wantArgs)
	}
}

// coerceWireJSONObject normalizes adapter-emitted arguments (object or JSON string)
// into a map for exact field assertions.
func coerceWireJSONObject(raw any) (map[string]any, error) {
	switch v := raw.(type) {
	case map[string]any:
		return v, nil
	case string:
		if v == "" {
			return nil, fmt.Errorf("empty string")
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(v), &m); err != nil {
			return nil, err
		}
		return m, nil
	case json.RawMessage:
		var m map[string]any
		if err := json.Unmarshal(v, &m); err != nil {
			return nil, err
		}
		return m, nil
	default:
		// Re-marshal then unmarshal for unexpected but JSON-compatible forms.
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("type %T: %w", raw, err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		return m, nil
	}
}

func assertNoInputItemType(t *testing.T, body map[string]any, phase int, forbiddenType string) {
	t.Helper()
	input, _ := body["input"].([]any)
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ == forbiddenType {
			t.Fatalf("phase %d input must not contain type=%q yet", phase, forbiddenType)
		}
	}
}

func assertRequiredProtectedWireFields(t *testing.T, body map[string]any, i int, cacheKey string) {
	t.Helper()
	// store must be present and exactly boolean false (omission fails).
	store, ok := body["store"].(bool)
	if !ok || store {
		t.Fatalf("body[%d] store must be exactly boolean false, got %T %#v", i, body["store"], body["store"])
	}
	// parallel_tool_calls must be present and exactly boolean false.
	ptc, ok := body["parallel_tool_calls"].(bool)
	if !ok || ptc {
		t.Fatalf("body[%d] parallel_tool_calls must be exactly boolean false, got %T %#v", i, body["parallel_tool_calls"], body["parallel_tool_calls"])
	}
	key, ok := body["prompt_cache_key"].(string)
	if !ok || key != cacheKey {
		t.Fatalf("body[%d] prompt_cache_key=%v want exactly %q", i, body["prompt_cache_key"], cacheKey)
	}
}

func assertNoHostedServerSideSearch(t *testing.T, body map[string]any, i int) {
	t.Helper()
	// Hosted / server-side tool-search fields must never appear.
	for _, k := range []string{"tool_choice_search", "server_tool_search", "hosted_tool_search"} {
		if _, ok := body[k]; ok {
			t.Fatalf("body[%d] has forbidden hosted search field %q", i, k)
		}
	}
	toolsArr, ok := body["tools"].([]any)
	if !ok {
		// Top-level tools absence is already fatal in assertPhaseTopLevelToolsCatalog;
		// if this helper is used alone, missing tools is not a hosted-search signal.
		return
	}
	for _, raw := range toolsArr {
		tm, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("body[%d] tools entry not object: %T", i, raw)
		}
		if typ, _ := tm["type"].(string); typ == "tool_search" {
			if exec, ok := tm["execution"].(string); !ok || (exec != "" && exec != "client") {
				t.Fatalf("body[%d] tool_search execution=%v (hosted/server-side forbidden)", i, tm["execution"])
			}
		}
	}
}

func assertNoPreviousResponseID(t *testing.T, body map[string]any, i int) {
	t.Helper()
	if _, ok := body["previous_response_id"]; ok {
		t.Fatalf("body[%d] has previous_response_id", i)
	}
}

func assertToolsArrayNoRecursiveNativeDisclosure(t *testing.T, body map[string]any, i int) {
	t.Helper()
	toolsArr, ok := body["tools"].([]any)
	if !ok {
		t.Fatalf("body[%d] tools missing or wrong type %T (required on every phase)", i, body["tools"])
	}
	var searchCount int
	for _, raw := range toolsArr {
		tm, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("body[%d] tools entry not object: %T", i, raw)
		}
		typ, _ := tm["type"].(string)
		name, _ := tm["name"].(string)
		if typ == "function" && name == ClientToolSearchToolName {
			t.Fatalf("body[%d] tools recursively discloses tool_search as function", i)
		}
		if typ == "tool_search" {
			searchCount++
		}
	}
	if searchCount != 1 {
		t.Fatalf("body[%d] tools has %d tool_search entries (want exactly 1)", i, searchCount)
	}
}

func assertToolSearchOutputCap(t *testing.T, body map[string]any, i int) {
	t.Helper()
	input, ok := body["input"].([]any)
	if !ok {
		// Phase 1 may still have []any input; wrong type is fail-closed.
		if body["input"] != nil {
			if _, isArr := body["input"].([]any); !isArr {
				t.Fatalf("body[%d] input wrong type %T", i, body["input"])
			}
		}
		return
	}
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		if typ != "tool_search_output" {
			continue
		}
		tools, ok := m["tools"].([]any)
		if !ok {
			t.Fatalf("body[%d] tool_search_output missing tools array (malformed)", i)
		}
		if len(tools) > MaxLoadedToolsPerSearch {
			t.Fatalf("body[%d] tool_search_output has %d tools (>%d)", i, len(tools), MaxLoadedToolsPerSearch)
		}
	}
}

// assertNoAllToolNameReminder ensures we do not inject Eino stock middleware's
// full tool-name reminder into user/system input.
func assertNoAllToolNameReminder(t *testing.T, body map[string]any, i int, tools []tool.BaseTool) {
	t.Helper()
	// If every business tool name appears together in a single user/system text
	// blob, treat it as the forbidden stock reminder.
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		if err != nil || info == nil {
			continue
		}
		names = append(names, info.Name)
	}
	if len(names) < 3 {
		return
	}
	input, _ := body["input"].([]any)
	for _, item := range input {
		text := flattenInputText(item)
		if text == "" {
			continue
		}
		hits := 0
		for _, n := range names {
			if strings.Contains(text, n) {
				hits++
			}
		}
		if hits == len(names) {
			t.Fatalf("body[%d] input appears to list all tool names (stock reminder)", i)
		}
	}
}

func flattenInputText(item any) string {
	m, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	// Easy input message content may be string or array.
	if c, ok := m["content"].(string); ok {
		return c
	}
	if arr, ok := m["content"].([]any); ok {
		var b strings.Builder
		for _, p := range arr {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if s, ok := pm["text"].(string); ok {
				b.WriteString(s)
			}
		}
		return b.String()
	}
	return ""
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// secretOpenerFunc implements modelapi.SecretOpener for tests.
type secretOpenerFunc func(context.Context, string, string, func([]byte) error) error

func (fn secretOpenerFunc) WithActiveSecret(
	ctx context.Context, workspaceID, secretID string, use func([]byte) error,
) error {
	return fn(ctx, workspaceID, secretID, use)
}

var _ modelapi.SecretOpener = secretOpenerFunc(nil)
