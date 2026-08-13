package einoruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// testExecuteBoundedToolSearch is a test helper for the no-session path (nil loaded set).
func testExecuteBoundedToolSearch(catalog *ToolCatalogSnapshot, args string) ([]*schema.ToolInfo, error) {
	infos, _, err := executeBoundedToolSearch(catalog, args, nil, MaxLoadedDefinitionsPerRun)
	return infos, err
}

func buildTestCatalog(t *testing.T, names ...string) (*ToolCatalogSnapshot, []tool.BaseTool) {
	t.Helper()
	ctx := context.Background()
	var inputs []ToolCatalogBuildEntry
	var exec []tool.BaseTool
	for _, n := range names {
		st := &stubTool{name: n, desc: "tool " + n + " for testing", params: testParams()}
		inputs = append(inputs, ToolCatalogBuildEntry{Tool: st, Exposure: ToolExposureDeferred})
		exec = append(exec, st)
	}
	cat, err := BuildToolCatalog(ctx, inputs)
	if err != nil {
		t.Fatal(err)
	}
	return cat, exec
}

func TestBoundedMiddleware_BeforeAgentCopyAndToolSearchTool(t *testing.T) {
	t.Parallel()
	cat, _ := buildTestCatalog(t, "weather_get", "stock_quote")
	mw, err := NewBoundedClientToolSearchMiddleware(cat)
	if err != nil {
		t.Fatal(err)
	}
	origTool := &stubTool{name: "weather_get", desc: "x"}
	runCtx := &adk.ChatModelAgentContext{
		Instruction:    "sys",
		Tools:          []tool.BaseTool{origTool},
		ReturnDirectly: map[string]bool{"x": true},
	}
	// Keep references to detect mutation.
	origToolsSlice := runCtx.Tools
	origRD := runCtx.ReturnDirectly

	_, out, err := mw.BeforeAgent(context.Background(), runCtx)
	if err != nil {
		t.Fatal(err)
	}
	if out == runCtx {
		t.Fatal("BeforeAgent must return a copy, not the same pointer")
	}
	if len(out.Tools) != 2 {
		t.Fatalf("tools len = %d, want 2 (business + search)", len(out.Tools))
	}
	// Original slice/map not mutated.
	if len(origToolsSlice) != 1 {
		t.Fatal("original Tools slice mutated")
	}
	if _, ok := origRD[ClientToolSearchToolName]; ok {
		t.Fatal("original ReturnDirectly mutated")
	}
	if out.ToolSearchTool == nil || out.ToolSearchTool.Name != ClientToolSearchToolName {
		t.Fatalf("ToolSearchTool = %+v", out.ToolSearchTool)
	}
	// Search executor Info matches ToolSearchTool contract.
	execInfo, err := mw.Executor().Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if execInfo.Name != out.ToolSearchTool.Name || execInfo.Desc != out.ToolSearchTool.Desc {
		t.Fatal("executor Info must match ToolSearchTool contract")
	}
	// No prompt injection possible here — Messages not in runCtx. Ensure Instruction unchanged.
	if out.Instruction != "sys" {
		t.Fatalf("instruction mutated: %q", out.Instruction)
	}
}

func TestBoundedMiddleware_BeforeModelRewriteState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, exec := buildTestCatalog(t, "alpha", "beta")
	// Add one immediate.
	imm := &stubTool{name: "ctrl", desc: "control", params: testParams()}
	cat2, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: exec[0], Exposure: ToolExposureDeferred},
		{Tool: exec[1], Exposure: ToolExposureDeferred},
		{Tool: imm, Exposure: ToolExposureImmediate, PlatformControl: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	mw, err := NewBoundedClientToolSearchMiddleware(cat2)
	if err != nil {
		t.Fatal(err)
	}

	ia, _ := exec[0].Info(ctx)
	ib, _ := exec[1].Info(ctx)
	ii, _ := imm.Info(ctx)
	search := clientToolSearchToolInfo()

	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{
		Messages:  []*schema.AgenticMessage{schema.UserAgenticMessage("hi")},
		ToolInfos: []*schema.ToolInfo{ii, ia, ib, search},
	}
	// Snapshot messages pointer to ensure no injection.
	msg0 := state.Messages[0]

	_, out, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Messages) != 1 || out.Messages[0] != msg0 {
		t.Fatal("messages must be untouched (no prompt injection)")
	}
	if len(out.ToolInfos) != 1 || out.ToolInfos[0].Name != "ctrl" {
		t.Fatalf("ToolInfos = %+v", out.ToolInfos)
	}
	if len(out.DeferredToolInfos) != 2 {
		t.Fatalf("DeferredToolInfos len = %d", len(out.DeferredToolInfos))
	}
	for _, ti := range out.ToolInfos {
		if ti.Name == ClientToolSearchToolName {
			t.Fatal("search tool must not be in ToolInfos")
		}
	}
	for _, ti := range out.DeferredToolInfos {
		if ti.Name == ClientToolSearchToolName {
			t.Fatal("search tool must not be in DeferredToolInfos")
		}
	}
	// Idempotent second call.
	_, out2, err := mw.BeforeModelRewriteState(ctx, out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out2.ToolInfos) != 1 || len(out2.DeferredToolInfos) != 2 {
		t.Fatal("idempotent partition failed")
	}
	// Catalog mismatch.
	stateBad := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{
		ToolInfos: []*schema.ToolInfo{{Name: "nope", Desc: "x"}},
	}
	if _, _, err := mw.BeforeModelRewriteState(ctx, stateBad, nil); !errors.Is(err, ErrModelToolCatalogMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
}

func TestExecuteBoundedToolSearch_KeywordAndSelect(t *testing.T) {
	t.Parallel()
	names := []string{"weather_get", "weather_forecast", "stock_quote", "currency_convert", "calendar_list", "email_send"}
	cat, _ := buildTestCatalog(t, names...)

	// Keyword search.
	got, err := testExecuteBoundedToolSearch(cat, `{"query":"weather"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || len(got) > MaxLoadedToolsPerSearch {
		t.Fatalf("weather results = %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Name > got[i].Name {
			t.Fatal("results not name-sorted")
		}
	}

	// max_results=1
	got, err = testExecuteBoundedToolSearch(cat, `{"query":"weather","max_results":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("max_results=1 got %d", len(got))
	}

	// max_results > 5 clamped
	got, err = testExecuteBoundedToolSearch(cat, `{"query":"tool","max_results":100}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > MaxLoadedToolsPerSearch {
		t.Fatalf("clamp failed: %d", len(got))
	}

	// zero / negative rejected
	if _, err := testExecuteBoundedToolSearch(cat, `{"query":"weather","max_results":0}`); !errors.Is(err, ErrToolSearchInvalidArgs) {
		t.Fatalf("zero: %v", err)
	}
	if _, err := testExecuteBoundedToolSearch(cat, `{"query":"weather","max_results":-1}`); !errors.Is(err, ErrToolSearchInvalidArgs) {
		t.Fatalf("neg: %v", err)
	}

	// select: path
	got, err = testExecuteBoundedToolSearch(cat, `{"query":"select:stock_quote,email_send"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("select got %d", len(got))
	}
	// select > 5 capped
	sel := "select:" + strings.Join(names, ",")
	payload, _ := json.Marshal(map[string]string{"query": sel})
	got, err = testExecuteBoundedToolSearch(cat, string(payload))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxLoadedToolsPerSearch {
		t.Fatalf("select cap = %d", len(got))
	}

	// unauthorized select
	if _, err := testExecuteBoundedToolSearch(cat, `{"query":"select:nope"}`); !errors.Is(err, ErrToolCatalogUnknownTool) {
		t.Fatalf("unauth: %v", err)
	}
	// duplicate select
	if _, err := testExecuteBoundedToolSearch(cat, `{"query":"select:stock_quote,stock_quote"}`); !errors.Is(err, ErrToolSearchInvalidArgs) {
		t.Fatalf("dup select: %v", err)
	}
	// empty / whitespace-only select segments must reject (never silently skip)
	for _, q := range []string{
		`{"query":"select:stock_quote,,email_send"}`,
		`{"query":"select:,stock_quote"}`,
		`{"query":"select:stock_quote,"}`,
		`{"query":"select:stock_quote, ,email_send"}`,
		`{"query":"select:  "}`,
		`{"query":"select:"}`,
		`{"query":"select:,"}`,
		`{"query":"select:,,,"}`,
	} {
		if _, err := testExecuteBoundedToolSearch(cat, q); !errors.Is(err, ErrToolSearchInvalidArgs) {
			t.Fatalf("empty select segment %s: err=%v want ErrToolSearchInvalidArgs", q, err)
		}
	}

	// zero matches
	got, err = testExecuteBoundedToolSearch(cat, `{"query":"zzzznonexistent"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("zero matches want 0 got %d", len(got))
	}

	// never returns executor
	got, err = testExecuteBoundedToolSearch(cat, `{"query":"tool_search"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, ti := range got {
		if ti.Name == ClientToolSearchToolName {
			t.Fatal("executor returned")
		}
	}
}

func TestExecuteBoundedToolSearch_MalformedJSON(t *testing.T) {
	t.Parallel()
	cat, _ := buildTestCatalog(t, "a")
	cases := []string{
		``,
		`[]`,
		`"query"`,
		`{"query":"x","query":"y"}`,
		`{"query":"x","unknown":1}`,
		`{"query":"x"} trailing`,
		`{"query":123}`,
		`null`,
	}
	for _, c := range cases {
		if _, err := testExecuteBoundedToolSearch(cat, c); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
	// empty query
	if _, err := testExecuteBoundedToolSearch(cat, `{"query":"  "}`); !errors.Is(err, ErrToolSearchInvalidArgs) {
		t.Fatalf("empty query: %v", err)
	}
}

// TestExecuteBoundedToolSearch_StrictMaxResultsTypes proves omission is valid
// while explicit null and non-integer JSON types are rejected.
func TestExecuteBoundedToolSearch_StrictMaxResultsTypes(t *testing.T) {
	t.Parallel()
	cat, _ := buildTestCatalog(t, "weather_get", "stock_quote")

	// Omission → default ceiling, success.
	got, err := testExecuteBoundedToolSearch(cat, `{"query":"weather"}`)
	if err != nil {
		t.Fatalf("omission: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("omission: expected matches")
	}

	// Valid integer still works.
	got, err = testExecuteBoundedToolSearch(cat, `{"query":"weather","max_results":1}`)
	if err != nil || len(got) != 1 {
		t.Fatalf("int max_results: got=%d err=%v", len(got), err)
	}

	invalid := []string{
		`{"query":"x","max_results":null}`,
		`{"query":"x","max_results":1.5}`,
		`{"query":"x","max_results":1.0}`,
		`{"query":"x","max_results":"5"}`,
		`{"query":"x","max_results":true}`,
		`{"query":"x","max_results":false}`,
		`{"query":"x","max_results":{}}`,
		`{"query":"x","max_results":[]}`,
		`{"query":"x","max_results":[1]}`,
		`{"query":"x","max_results":{"n":1}}`,
	}
	for _, c := range invalid {
		if _, err := testExecuteBoundedToolSearch(cat, c); !errors.Is(err, ErrToolSearchInvalidArgs) {
			t.Fatalf("expected ErrToolSearchInvalidArgs for %s, got %v", c, err)
		}
	}

	// Duplicate key still rejected.
	if _, err := testExecuteBoundedToolSearch(cat, `{"query":"x","max_results":1,"max_results":2}`); err == nil {
		t.Fatal("duplicate max_results key must fail")
	}
	// Unknown field still rejected.
	if _, err := testExecuteBoundedToolSearch(cat, `{"query":"x","max_results":1,"extra":true}`); err == nil {
		t.Fatal("unknown field must fail")
	}
}

// TestExecuteBoundedToolSearch_DeferredOnlyExposure freezes partition/exposure:
// keyword search never returns immediate tools; direct select of immediate or
// the native executor fails with ErrToolSearchNotDeferred.
func TestExecuteBoundedToolSearch_DeferredOnlyExposure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	deferred := &stubTool{name: "control_plane_action", desc: "deferred business control action", params: testParams()}
	// Name/desc deliberately share keywords with the deferred tool so a naive
	// full-catalog scan would surface the immediate tool first.
	immediate := &stubTool{name: "control_plane_status", desc: "immediate platform control status", params: testParams()}
	other := &stubTool{name: "weather_get", desc: "weather", params: testParams()}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: deferred, Exposure: ToolExposureDeferred},
		{Tool: immediate, Exposure: ToolExposureImmediate, PlatformControl: true},
		{Tool: other, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Keyword "control" must return only the deferred tool, never immediate.
	got, err := testExecuteBoundedToolSearch(cat, `{"query":"control"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, ti := range got {
		if ti.Name == "control_plane_status" {
			t.Fatal("keyword search disclosed immediate tool")
		}
		if ti.Name == ClientToolSearchToolName {
			t.Fatal("keyword search disclosed native executor")
		}
	}
	if len(got) != 1 || got[0].Name != "control_plane_action" {
		names := make([]string, len(got))
		for i, ti := range got {
			names[i] = ti.Name
		}
		t.Fatalf("keyword control results = %v, want [control_plane_action]", names)
	}

	// Keyword matching only the immediate tool → zero results (not an error).
	got, err = testExecuteBoundedToolSearch(cat, `{"query":"status"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("immediate-only keyword should yield 0, got %d", len(got))
	}

	// Direct select of immediate → ErrToolSearchNotDeferred.
	if _, err := testExecuteBoundedToolSearch(cat, `{"query":"select:control_plane_status"}`); !errors.Is(err, ErrToolSearchNotDeferred) {
		t.Fatalf("select immediate: %v", err)
	}

	// Direct select mixing deferred + immediate fails (no silent skip).
	if _, err := testExecuteBoundedToolSearch(cat, `{"query":"select:control_plane_action,control_plane_status"}`); !errors.Is(err, ErrToolSearchNotDeferred) {
		t.Fatalf("select mixed: %v", err)
	}

	// Direct select of native executor → ErrToolSearchNotDeferred.
	if _, err := testExecuteBoundedToolSearch(cat, `{"query":"select:tool_search"}`); !errors.Is(err, ErrToolSearchNotDeferred) {
		t.Fatalf("select executor: %v", err)
	}
	if _, err := testExecuteBoundedToolSearch(cat, `{"query":"select:`+PlatformCatalogSearchToolName+`"}`); !errors.Is(err, ErrToolSearchNotDeferred) {
		t.Fatalf("select platform search: %v", err)
	}

	// Deferred select still works.
	got, err = testExecuteBoundedToolSearch(cat, `{"query":"select:control_plane_action,weather_get"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("select deferred got %d", len(got))
	}
	for _, ti := range got {
		if ti.Name == "control_plane_status" || ti.Name == ClientToolSearchToolName {
			t.Fatalf("select returned non-disclosable %q", ti.Name)
		}
	}

	// Partition exposure frozen: Entry() confirms immediate is still immediate.
	ent, ok := cat.Entry("control_plane_status")
	if !ok || ent.Exposure != ToolExposureImmediate {
		t.Fatalf("immediate exposure not frozen: ok=%v ent=%+v", ok, ent)
	}
	ent, ok = cat.Entry("control_plane_action")
	if !ok || ent.Exposure != ToolExposureDeferred {
		t.Fatalf("deferred exposure not frozen: ok=%v ent=%+v", ok, ent)
	}
}

func TestBoundedSearch_Concurrency(t *testing.T) {
	t.Parallel()
	cat, _ := buildTestCatalog(t, "alpha", "beta", "gamma", "delta", "epsilon", "zeta")
	mw, err := NewBoundedClientToolSearchMiddleware(cat)
	if err != nil {
		t.Fatal(err)
	}
	exec := mw.Executor().(interface {
		InvokableRun(context.Context, *schema.ToolArgument, ...tool.Option) (*schema.ToolResult, error)
	})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Distinct contexts without shared session: concurrent runs isolate.
			res, err := exec.InvokableRun(context.Background(), &schema.ToolArgument{Text: `{"query":"a"}`})
			if err != nil {
				t.Errorf("search: %v", err)
				return
			}
			if res == nil || len(res.Parts) != 1 || res.Parts[0].ToolSearchResult == nil {
				t.Errorf("bad result: %#v", res)
				return
			}
			if len(res.Parts[0].ToolSearchResult.Tools) > MaxLoadedToolsPerSearch {
				t.Errorf("over cap: %d", len(res.Parts[0].ToolSearchResult.Tools))
			}
		}()
	}
	wg.Wait()
}

func TestBoundedSearch_RunLocalLoadedUniqueness(t *testing.T) {
	t.Parallel()
	// 8 tools so keyword/select can exercise repeated loads.
	names := []string{"alpha_one", "alpha_two", "beta_one", "beta_two", "gamma_one", "delta_one", "epsilon_one", "zeta_one"}
	cat, _ := buildTestCatalog(t, names...)
	// Direct execute with explicit already-loaded set (simulates session).
	first, newly, err := executeBoundedToolSearch(cat, `{"query":"select:alpha_one,alpha_two","max_results":5}`, nil, MaxLoadedDefinitionsPerRun)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(newly) != 2 {
		t.Fatalf("first load: tools=%d newly=%d", len(first), len(newly))
	}
	loaded := mergeLoadedDeferredToolNames(nil, newly)
	// Repeat select of same tools → already loaded.
	if _, _, err := executeBoundedToolSearch(cat, `{"query":"select:alpha_one,alpha_two"}`, loaded, MaxLoadedDefinitionsPerRun); !errors.Is(err, ErrToolSearchAlreadyLoaded) {
		t.Fatalf("repeat select: %v", err)
	}
	// Keyword for alpha must omit already-loaded alpha_one/two.
	kw, newly2, err := executeBoundedToolSearch(cat, `{"query":"alpha","max_results":5}`, loaded, MaxLoadedDefinitionsPerRun)
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range kw {
		if info.Name == "alpha_one" || info.Name == "alpha_two" {
			t.Fatalf("keyword returned already-loaded %q", info.Name)
		}
	}
	loaded = mergeLoadedDeferredToolNames(loaded, newly2)
	// Different query still no duplicates.
	for _, info := range kw {
		for _, n := range loaded {
			if info.Name == n && (info.Name == "alpha_one" || info.Name == "alpha_two") {
				t.Fatal("duplicate schema load")
			}
		}
	}
	// Cumulative cap: pre-load 39 names, then select 2 more → reject.
	// Build a large catalog of 45 deferred tools.
	bigNames := make([]string, 0, 45)
	for i := 0; i < 45; i++ {
		bigNames = append(bigNames, fmt.Sprintf("tool_%03d", i))
	}
	bigCat, _ := buildTestCatalog(t, bigNames...)
	preloaded := bigNames[:39]
	// Select two more would exceed 40.
	if _, _, err := executeBoundedToolSearch(bigCat, `{"query":"select:tool_039,tool_040"}`, preloaded, MaxLoadedDefinitionsPerRun); !errors.Is(err, ErrToolSearchLoadCapExceeded) {
		t.Fatalf("cap exceed select: %v", err)
	}
	// At exactly 40, further keyword returns cap error.
	full := bigNames[:40]
	if _, _, err := executeBoundedToolSearch(bigCat, `{"query":"tool","max_results":5}`, full, MaxLoadedDefinitionsPerRun); !errors.Is(err, ErrToolSearchLoadCapExceeded) {
		t.Fatalf("at cap keyword: %v", err)
	}
	// Room for 1 more via keyword.
	almost := bigNames[:39]
	got, newly3, err := executeBoundedToolSearch(bigCat, `{"query":"tool","max_results":5}`, almost, MaxLoadedDefinitionsPerRun)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(newly3) != 1 {
		t.Fatalf("room=1: got=%d newly=%d", len(got), len(newly3))
	}
	// Per-search still <= 5 with empty loaded.
	got5, _, err := executeBoundedToolSearch(bigCat, `{"query":"tool","max_results":5}`, nil, MaxLoadedDefinitionsPerRun)
	if err != nil {
		t.Fatal(err)
	}
	if len(got5) > MaxLoadedToolsPerSearch {
		t.Fatalf("per-search cap: %d", len(got5))
	}
}

func TestBoundedSearch_ConcurrentRunsIsolateLoadedSets(t *testing.T) {
	t.Parallel()
	cat, _ := buildTestCatalog(t, "shared_a", "shared_b", "shared_c")
	// Two independent already-loaded sets must not interfere.
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			loaded := []string(nil)
			for round := 0; round < 3; round++ {
				infos, newly, err := executeBoundedToolSearch(cat, `{"query":"shared","max_results":1}`, loaded, MaxLoadedDefinitionsPerRun)
				if err != nil {
					errCh <- err
					return
				}
				loaded = mergeLoadedDeferredToolNames(loaded, newly)
				// No duplicates within a run.
				seen := map[string]struct{}{}
				for _, n := range loaded {
					if _, ok := seen[n]; ok {
						errCh <- fmt.Errorf("dup %q", n)
						return
					}
					seen[n] = struct{}{}
				}
				_ = infos
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestParseSelectNames_RejectsEmptySegments(t *testing.T) {
	t.Parallel()
	// Adversarial empty-segment cases; each must fail with ErrToolSearchInvalidArgs.
	bad := []string{
		"a,,b",
		",a",
		"a,",
		"a, ,b",
		"  ",
		"",
		",",
		",,,",
		"a,\t,b",
		"a,\n,b",
	}
	for _, raw := range bad {
		_, err := parseSelectNames(raw)
		if !errors.Is(err, ErrToolSearchInvalidArgs) {
			t.Fatalf("parseSelectNames(%q)=%v want ErrToolSearchInvalidArgs", raw, err)
		}
	}
	// Valid still works.
	got, err := parseSelectNames("stock_quote, email_send")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "stock_quote" || got[1] != "email_send" {
		t.Fatalf("got=%v", got)
	}
	// Duplicate still rejected.
	if _, err := parseSelectNames("a,a"); !errors.Is(err, ErrToolSearchInvalidArgs) {
		t.Fatalf("dup: %v", err)
	}
}
