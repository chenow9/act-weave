package einoruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
)

// toolBudgetCounter is a test-only explicit counter for unit tests that cannot
// install ADK run-local state. Production code uses NewToolBudgetMiddleware only.
type toolBudgetCounter struct {
	mu  sync.Mutex
	n   int
	max int
}

func (c *toolBudgetCounter) tryAcquire() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n >= c.max {
		return false
	}
	c.n++
	return true
}

func (c *toolBudgetCounter) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// newToolBudgetMiddlewareWithCounter is a test-only helper that closes over an
// explicit shared counter instead of run-local state. Not part of production API.
func newToolBudgetMiddlewareWithCounter(max int) (compose.ToolMiddleware, *toolBudgetCounter, error) {
	max, err := normalizeMaxToolInvocations(max)
	if err != nil {
		return compose.ToolMiddleware{}, nil, err
	}
	counter := &toolBudgetCounter{max: max}
	mw := compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				if !counter.tryAcquire() {
					return &compose.ToolOutput{
						Result: formatToolErrorResult(ToolBudgetExceededCode, ToolBudgetExceededMessage),
					}, nil
				}
				return next(ctx, input)
			}
		},
		Streamable: func(next compose.StreamableToolEndpoint) compose.StreamableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.StreamToolOutput, error) {
				if !counter.tryAcquire() {
					return &compose.StreamToolOutput{
						Result: schema.StreamReaderFromArray([]string{
							formatToolErrorResult(ToolBudgetExceededCode, ToolBudgetExceededMessage),
						}),
					}, nil
				}
				return next(ctx, input)
			}
		},
		EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
				if !counter.tryAcquire() {
					return nil, errors.Join(ErrToolBudgetExceeded, errors.New(ToolBudgetExceededMessage))
				}
				return next(ctx, input)
			}
		},
		EnhancedStreamable: func(next compose.EnhancedStreamableToolEndpoint) compose.EnhancedStreamableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedStreamableToolOutput, error) {
				if !counter.tryAcquire() {
					return nil, errors.Join(ErrToolBudgetExceeded, errors.New(ToolBudgetExceededMessage))
				}
				return next(ctx, input)
			}
		},
	}
	return mw, counter, nil
}

func TestToolBudgetMiddleware_StopsAfterCap(t *testing.T) {
	t.Parallel()
	const cap = 16
	mw, counter, err := newToolBudgetMiddlewareWithCounter(cap)
	if err != nil {
		t.Fatal(err)
	}

	var underlyingCalls int
	endpoint := mw.Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		underlyingCalls++
		return &compose.ToolOutput{Result: `{"ok":true}`}, nil
	})

	ctx := context.Background()
	for i := 0; i < cap; i++ {
		out, err := endpoint(ctx, &compose.ToolInput{Name: "t", Arguments: `{}`, CallID: "c"})
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		if out == nil || out.Result != `{"ok":true}` {
			t.Fatalf("call %d: expected underlying result, got %#v", i+1, out)
		}
	}
	if underlyingCalls != cap {
		t.Fatalf("underlyingCalls = %d, want %d", underlyingCalls, cap)
	}
	if counter.Count() != cap {
		t.Fatalf("counter = %d, want %d", counter.Count(), cap)
	}

	// 17th call: budget exceeded tool-result JSON, no underlying invoke.
	out, err := endpoint(ctx, &compose.ToolInput{Name: "t", Arguments: `{}`, CallID: "c17"})
	if err != nil {
		t.Fatalf("budget call: %v", err)
	}
	if underlyingCalls != cap {
		t.Fatalf("underlying must not be called past cap; got %d", underlyingCalls)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out.Result), &body); err != nil {
		t.Fatalf("result JSON: %v", err)
	}
	if body["ok"] != false || body["errorCode"] != ToolBudgetExceededCode {
		t.Fatalf("budget result = %v", body)
	}
	if !strings.Contains(fmtString(body["message"]), "budget") {
		t.Fatalf("message = %v", body["message"])
	}
}

func TestToolBudgetMiddleware_DefaultCap(t *testing.T) {
	t.Parallel()
	mw, counter, err := newToolBudgetMiddlewareWithCounter(0) // default 16
	if err != nil {
		t.Fatal(err)
	}
	endpoint := mw.Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: "ok"}, nil
	})
	ctx := context.Background()
	for i := 0; i < DefaultMaxToolInvocations+1; i++ {
		_, _ = endpoint(ctx, &compose.ToolInput{Name: "t", Arguments: `{}`})
	}
	if counter.Count() != DefaultMaxToolInvocations {
		t.Fatalf("counter = %d, want %d", counter.Count(), DefaultMaxToolInvocations)
	}
}

func TestToolBudgetMiddleware_RejectsInvalidMax(t *testing.T) {
	t.Parallel()
	if _, err := NewToolBudgetMiddleware(17); !errors.Is(err, ErrToolBudgetMaxInvalid) {
		t.Fatalf("production 17: %v", err)
	}
	if _, err := NewToolBudgetMiddleware(-1); !errors.Is(err, ErrToolBudgetMaxInvalid) {
		t.Fatalf("production -1: %v", err)
	}
	if _, _, err := newToolBudgetMiddlewareWithCounter(17); !errors.Is(err, ErrToolBudgetMaxInvalid) {
		t.Fatalf("test helper 17: %v", err)
	}
	if _, _, err := newToolBudgetMiddlewareWithCounter(-5); !errors.Is(err, ErrToolBudgetMaxInvalid) {
		t.Fatalf("test helper -5: %v", err)
	}
	// Zero and 1..16 still accepted.
	if _, err := NewToolBudgetMiddleware(0); err != nil {
		t.Fatalf("zero: %v", err)
	}
	if _, err := NewToolBudgetMiddleware(1); err != nil {
		t.Fatalf("1: %v", err)
	}
	if _, err := NewToolBudgetMiddleware(16); err != nil {
		t.Fatalf("16: %v", err)
	}
}

func TestToolBudgetMiddleware_EnhancedCountsSearchCalls(t *testing.T) {
	t.Parallel()
	const cap = 3
	mw, counter, err := newToolBudgetMiddlewareWithCounter(cap)
	if err != nil {
		t.Fatal(err)
	}

	var underlying int
	endpoint := mw.EnhancedInvokable(func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
		underlying++
		return &compose.EnhancedInvokableToolOutput{
			Result: &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: "ok"}}},
		}, nil
	})

	ctx := context.Background()
	for i := 0; i < cap; i++ {
		out, err := endpoint(ctx, &compose.ToolInput{Name: ClientToolSearchToolName, Arguments: `{"query":"x"}`, CallID: "c"})
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		if out == nil {
			t.Fatalf("call %d: nil output", i+1)
		}
	}
	if underlying != cap {
		t.Fatalf("underlying = %d, want %d", underlying, cap)
	}
	// Next search call fails at cap.
	_, err = endpoint(ctx, &compose.ToolInput{Name: ClientToolSearchToolName, Arguments: `{"query":"y"}`, CallID: "c4"})
	if err == nil {
		t.Fatal("expected budget error on enhanced path")
	}
	if !errors.Is(err, ErrToolBudgetExceeded) {
		t.Fatalf("err = %v, want ErrToolBudgetExceeded", err)
	}
	if underlying != cap {
		t.Fatalf("underlying must not run past cap; got %d", underlying)
	}
	if counter.Count() != cap {
		t.Fatalf("counter = %d, want %d", counter.Count(), cap)
	}
}

func TestToolBudgetMiddleware_SharedCounterInvokableAndEnhanced(t *testing.T) {
	t.Parallel()
	mw, counter, err := newToolBudgetMiddlewareWithCounter(2)
	if err != nil {
		t.Fatal(err)
	}
	inv := mw.Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: "ok"}, nil
	})
	enh := mw.EnhancedInvokable(func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
		return &compose.EnhancedInvokableToolOutput{Result: &schema.ToolResult{}}, nil
	})
	ctx := context.Background()
	if _, err := inv(ctx, &compose.ToolInput{Name: "a", Arguments: `{}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := enh(ctx, &compose.ToolInput{Name: ClientToolSearchToolName, Arguments: `{"query":"q"}`}); err != nil {
		t.Fatal(err)
	}
	if counter.Count() != 2 {
		t.Fatalf("counter = %d, want 2", counter.Count())
	}
	// Third call (either path) fails.
	out, err := inv(ctx, &compose.ToolInput{Name: "b", Arguments: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || !strings.Contains(out.Result, ToolBudgetExceededCode) {
		t.Fatalf("invokable budget result = %#v", out)
	}
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}

func TestNormalizeMaxToolInvocations(t *testing.T) {
	t.Parallel()
	n, err := normalizeMaxToolInvocations(0)
	if err != nil || n != DefaultMaxToolInvocations {
		t.Fatalf("zero: n=%d err=%v", n, err)
	}
	n, err = normalizeMaxToolInvocations(8)
	if err != nil || n != 8 {
		t.Fatalf("8: n=%d err=%v", n, err)
	}
	if _, err := normalizeMaxToolInvocations(17); !errors.Is(err, ErrToolBudgetMaxInvalid) {
		t.Fatalf("17: %v", err)
	}
	if _, err := normalizeMaxToolInvocations(-3); !errors.Is(err, ErrToolBudgetMaxInvalid) {
		t.Fatalf("neg: %v", err)
	}
}

// countingBudgetTool increments a shared atomic and returns ok JSON.
type countingBudgetTool struct {
	name  string
	calls *int64
}

func (t *countingBudgetTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.name, Desc: "budget counter",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"q": {Type: schema.String, Required: true},
		}),
	}, nil
}

func (t *countingBudgetTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	if t.calls != nil {
		atomic.AddInt64(t.calls, 1)
	}
	return `{"ok":true}`, nil
}

func TestToolBudget_PerRunIndependentSequential(t *testing.T) {
	// Two sequential Runs on the same built agent each get a fresh full budget.
	ctx := context.Background()
	const cap = 2
	var calls int64
	bt := &countingBudgetTool{name: "budget_tool", calls: &calls}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: bt, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}

	// Each run: 2 tool calls then final text. Budget cap=2 means both succeed.
	// Script for two full runs (4 tool-call responses + 2 finals).
	responses := make([]*schema.AgenticMessage, 0, 8)
	for i := 0; i < 2; i++ {
		responses = append(responses,
			agenticFunctionCall("budget_tool", fmt.Sprintf("r1-%d", i), `{"q":"x"}`),
		)
	}
	responses = append(responses, agenticmsg.AssistantText("run1-done"))
	for i := 0; i < 2; i++ {
		responses = append(responses,
			agenticFunctionCall("budget_tool", fmt.Sprintf("r2-%d", i), `{"q":"y"}`),
		)
	}
	responses = append(responses, agenticmsg.AssistantText("run2-done"))

	mdl := &scriptedAgenticModel{responses: responses}
	cfg := baseAgenticCfg(mdl, []tool.BaseTool{bt}, cat)
	cfg.MaxToolInvocations = cap
	agent, err := BuildAgenticAgent(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()})

	r1, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws-b", RunID: "run-b1",
		Messages: []*schema.AgenticMessage{agenticmsg.UserText("run1")},
	})
	if err != nil {
		t.Fatalf("run1: %v", err)
	}
	if r1.FinalAssistantText != "run1-done" {
		t.Fatalf("run1 text=%q err=%v", r1.FinalAssistantText, r1.Err)
	}
	after1 := atomic.LoadInt64(&calls)
	if after1 != int64(cap) {
		t.Fatalf("after run1 calls=%d want %d", after1, cap)
	}

	r2, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws-b", RunID: "run-b2",
		Messages: []*schema.AgenticMessage{agenticmsg.UserText("run2")},
	})
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if r2.FinalAssistantText != "run2-done" {
		t.Fatalf("run2 text=%q err=%v", r2.FinalAssistantText, r2.Err)
	}
	after2 := atomic.LoadInt64(&calls)
	if after2 != int64(cap*2) {
		t.Fatalf("after run2 calls=%d want %d (fresh budget each run)", after2, cap*2)
	}
}

func TestToolBudget_ConcurrentRunsDoNotInterfere(t *testing.T) {
	// Concurrent Runs on separate agent instances (Eino agents are not safe for
	// concurrent Run on one instance). Shared executable tool + independent
	// run-local budget counters prove budgets do not interfere under -race.
	ctx := context.Background()
	const cap = 3
	var calls int64
	bt := &countingBudgetTool{name: "budget_tool", calls: &calls}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: bt, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}

	buildAgent := func() *adk.TypedChatModelAgent[*schema.AgenticMessage] {
		// Each agent gets its own scripted model with enough responses for one run.
		responses := make([]*schema.AgenticMessage, 0, cap+1)
		for i := 0; i < cap; i++ {
			responses = append(responses, agenticFunctionCall("budget_tool", fmt.Sprintf("c-%d", i), `{"q":"x"}`))
		}
		responses = append(responses, agenticmsg.AssistantText("done"))
		mdl := &scriptedAgenticModel{responses: responses}
		cfg := baseAgenticCfg(mdl, []tool.BaseTool{bt}, cat)
		cfg.MaxToolInvocations = cap
		agent, err := BuildAgenticAgent(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return agent
	}
	agents := []*adk.TypedChatModelAgent[*schema.AgenticMessage]{buildAgent(), buildAgent()}
	engine := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()})

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := engine.Run(ctx, agents[i], AgenticRunInput{
				WorkspaceID: "ws-conc",
				RunID:       fmt.Sprintf("run-c-%d", i),
				Messages:    []*schema.AgenticMessage{agenticmsg.UserText("go")},
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent run: %v", err)
		}
	}
	// Each run uses its own full budget; both should complete cap tool calls.
	got := atomic.LoadInt64(&calls)
	if got != int64(cap*2) {
		t.Fatalf("calls=%d want %d (independent per-run budgets)", got, cap*2)
	}
}

// TestToolBudget_SharedProductionMiddlewareConcurrentIndependentBudgets proves
// concurrent independent Runs that share the exact same production
// NewToolBudgetMiddleware instance (same closed-over max + same run-local key)
// each receive a full independent budget. Single-action-per-turn is enforced, so
// cap is within MaxIterations=8 (one tool call per model turn).
// A build-local shared counter would yield only `cap` total underlying invocations;
// run-local state yields 2*cap. Race-tested.
func TestToolBudget_SharedProductionMiddlewareConcurrentIndependentBudgets(t *testing.T) {
	ctx := context.Background()
	const cap = 3 // one action per turn; well within MaxIterations=8
	var calls int64
	bt := &countingBudgetTool{name: "budget_tool", calls: &calls}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: bt, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}

	// ONE production middleware instance shared by both agents.
	sharedMW, err := NewToolBudgetMiddleware(cap)
	if err != nil {
		t.Fatal(err)
	}

	buildAgentSharingMW := func(runTag string) *adk.TypedChatModelAgent[*schema.AgenticMessage] {
		// One function call per model turn (single-action guard), then final text.
		responses := make([]*schema.AgenticMessage, 0, cap+1)
		for i := 0; i < cap; i++ {
			responses = append(responses, agenticFunctionCall("budget_tool", fmt.Sprintf("%s-%d", runTag, i), `{"q":"x"}`))
		}
		responses = append(responses, agenticmsg.AssistantText("shared-mw-done"))
		mdl := &scriptedAgenticModel{responses: responses}
		agent, err := buildAgenticAgentWithBudgetMW(ctx, baseAgenticCfg(mdl, []tool.BaseTool{bt}, cat), sharedMW)
		if err != nil {
			t.Fatal(err)
		}
		return agent
	}

	agents := []*adk.TypedChatModelAgent[*schema.AgenticMessage]{
		buildAgentSharingMW("a"),
		buildAgentSharingMW("b"),
	}
	engine := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()})

	var wg sync.WaitGroup
	type runOut struct {
		idx  int
		err  error
		n    int64 // calls after this run finishes (not atomic snapshot of just this run)
		text string
	}
	outs := make(chan runOut, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := engine.Run(ctx, agents[i], AgenticRunInput{
				WorkspaceID: "ws-shared-mw",
				RunID:       fmt.Sprintf("run-shared-%d", i),
				Messages:    []*schema.AgenticMessage{agenticmsg.UserText("go")},
			})
			text := ""
			if res != nil {
				text = res.FinalAssistantText
				if err == nil && res.Err != nil {
					err = res.Err
				}
			}
			outs <- runOut{idx: i, err: err, n: atomic.LoadInt64(&calls), text: text}
		}()
	}
	wg.Wait()
	close(outs)
	for o := range outs {
		if o.err != nil {
			t.Fatalf("run %d: %v", o.idx, o.err)
		}
		if o.text != "shared-mw-done" {
			t.Fatalf("run %d text=%q", o.idx, o.text)
		}
	}
	got := atomic.LoadInt64(&calls)
	if got != int64(cap*2) {
		t.Fatalf("shared production middleware underlying calls=%d want %d "+
			"(each of 2 concurrent runs must get a full %d budget; "+
			"shared build-local counter would cap total at %d)",
			got, cap*2, cap, cap)
	}
}

// buildAgenticAgentWithBudgetMW is a test helper that mirrors BuildAgenticAgent
// but injects a pre-built production budget middleware (to share one instance).
func buildAgenticAgentWithBudgetMW(
	ctx context.Context,
	cfg AgenticAgentBuildConfig,
	budgetMW compose.ToolMiddleware,
) (*adk.TypedChatModelAgent[*schema.AgenticMessage], error) {
	if cfg.Model == nil {
		return nil, ErrAgenticModelRequired
	}
	promptKey := strings.TrimSpace(cfg.PromptCacheKey)
	if promptKey == "" {
		return nil, ErrAgenticPromptCacheKeyRequired
	}
	tools := make([]tool.BaseTool, 0, len(cfg.Tools))
	for _, t := range cfg.Tools {
		if t != nil {
			tools = append(tools, t)
		}
	}
	maxIter := cfg.MaxIterations
	if maxIter == 0 {
		maxIter = DefaultMaxIterations
	}
	if maxIter != DefaultMaxIterations {
		return nil, fmt.Errorf("%w: got %d", ErrAgenticMaxIterations, cfg.MaxIterations)
	}
	unknown := cfg.UnknownToolsHandler
	if unknown == nil {
		unknown = defaultUnknownToolsHandler
	}
	var handlers []adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage]
	handlers = append(handlers, cfg.ExtraHandlers...)
	handlers = append(handlers, newPromptCacheKeyMiddleware(promptKey))
	if len(tools) > 0 {
		if !cfg.ClientToolSearchVerified {
			return nil, ErrAgenticClientToolSearchUnverified
		}
		if ToolSearchMode(strings.TrimSpace(string(cfg.ToolSearchMode))) != ToolSearchModeClientBounded {
			return nil, fmt.Errorf("%w: got %q", ErrAgenticToolSearchMode, cfg.ToolSearchMode)
		}
		if cfg.Catalog == nil {
			return nil, ErrAgenticCatalogRequired
		}
		if err := cfg.Catalog.ValidateExecutableTools(ctx, tools); err != nil {
			return nil, err
		}
		bounded, err := NewBoundedClientToolSearchMiddleware(cfg.Catalog)
		if err != nil {
			return nil, err
		}
		handlers = append(handlers, bounded)
	}
	middlewares := make([]compose.ToolMiddleware, 0, 1+len(cfg.ExtraToolMiddlewares))
	middlewares = append(middlewares, budgetMW)
	middlewares = append(middlewares, cfg.ExtraToolMiddlewares...)
	// Same production single-action boundary as BuildAgenticAgent.
	guardedModel := wrapSingleActionAgenticModel(cfg.Model)
	return adk.NewTypedChatModelAgent(ctx, &adk.TypedChatModelAgentConfig[*schema.AgenticMessage]{
		Name:          strings.TrimSpace(cfg.Name),
		Description:   strings.TrimSpace(cfg.Description),
		Instruction:   cfg.Instruction,
		Model:         guardedModel,
		MaxIterations: maxIter,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               tools,
				ExecuteSequentially: true,
				UnknownToolsHandler: unknown,
				ToolCallMiddlewares: middlewares,
			},
		},
		Handlers:            handlers,
		ModelRetryConfig:    nil,
		ModelFailoverConfig: nil,
	})
}

func TestToolBudget_OrdinaryAndSearchShareCount(t *testing.T) {
	// cap=2: one search + one ordinary tool; third fails.
	ctx := context.Background()
	const cap = 2
	var ordinaryCalls int64
	bt := &countingBudgetTool{name: "echo_budget", calls: &ordinaryCalls}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: bt, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}

	// search → ordinary → try ordinary again would hit budget on the tool path
	// (search counts as EnhancedInvokable). With cap=2: search+echo ok, then done.
	mdl := &scriptedAgenticModel{
		responses: []*schema.AgenticMessage{
			agenticFunctionCall(ClientToolSearchToolName, "ts-1", `{"query":"echo"}`),
			agenticFunctionCall("echo_budget", "e-1", `{"q":"hi"}`),
			agenticmsg.AssistantText("shared-ok"),
		},
	}
	cfg := baseAgenticCfg(mdl, []tool.BaseTool{bt}, cat)
	cfg.MaxToolInvocations = cap
	agent, err := BuildAgenticAgent(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()})
	result, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws-share", RunID: "run-share",
		Messages: []*schema.AgenticMessage{agenticmsg.UserText("search and echo")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalAssistantText != "shared-ok" {
		t.Fatalf("text=%q err=%v", result.FinalAssistantText, result.Err)
	}
	if atomic.LoadInt64(&ordinaryCalls) != 1 {
		t.Fatalf("ordinary calls=%d want 1", ordinaryCalls)
	}
}

func TestToolBudget_InterruptResumePreservesCount(t *testing.T) {
	// cap=2: HITL first call acquires (n=1) then interrupts. On resume the tool is
	// re-entered and acquires again (n=2). Budget is then full — subsequent echo
	// must not invoke. If resume reset the counter, HITL resume would be n=1 and
	// echo would succeed (echoCalls=1).
	ctx := context.Background()
	const cap = 2
	hitl := &agenticHITLTool{name: "hitl_budget"}
	var echoCalls int64
	echo := &countingBudgetTool{name: "echo_budget", calls: &echoCalls}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: hitl, Exposure: ToolExposureDeferred},
		{Tool: echo, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}

	mdl := &scriptedAgenticModel{
		responses: []*schema.AgenticMessage{
			agenticFunctionCall("hitl_budget", "h-1", `{"q":"need"}`),
			// After resume of HITL, model asks for echo — must be budget-blocked if preserved.
			agenticFunctionCall("echo_budget", "e-1", `{"q":"a"}`),
			agenticmsg.AssistantText("resume-done"),
		},
	}
	cfg := baseAgenticCfg(mdl, []tool.BaseTool{hitl, echo}, cat)
	cfg.MaxToolInvocations = cap
	agent, err := BuildAgenticAgent(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemCheckPointStore()
	engine := NewAgenticEngine(AgenticEngineConfig{Store: store})

	r1, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws-resume-b", RunID: "run-resume-b",
		Messages: []*schema.AgenticMessage{agenticmsg.UserText("start")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !r1.Interrupted || len(r1.InterruptContextIDs) == 0 {
		t.Fatalf("expected interrupt: %+v", r1)
	}

	targets := map[string]any{}
	for _, id := range r1.InterruptContextIDs {
		targets[id] = "yes"
	}
	r2, err := engine.Resume(ctx, agent, AgenticResumeInput{
		WorkspaceID:  "ws-resume-b",
		RunID:        "run-resume-b",
		CheckpointID: r1.CheckpointID,
		Targets:      targets,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := atomic.LoadInt64(&echoCalls); got != 0 {
		t.Fatalf("echoCalls=%d want 0 (resume must preserve budget so HITL resume fills cap=2)", got)
	}
	// HITL resume re-acquires budget (cap=2 filled); subsequent ordinary tool is
	// soft-rejected and model may still emit final text. Hard-fail unexpected errs.
	if r2.Err != nil && !errors.Is(r2.Err, ErrToolBudgetExceeded) {
		t.Fatalf("resume unexpected err=%v text=%q", r2.Err, r2.FinalAssistantText)
	}
	if r2.Err == nil && r2.FinalAssistantText != "resume-done" {
		t.Fatalf("resume text=%q want resume-done (or typed budget err)", r2.FinalAssistantText)
	}
}

// --- Streamable / EnhancedStreamable budget hooks ---

func TestToolBudgetMiddleware_StreamableStopsAfterCap(t *testing.T) {
	t.Parallel()
	const cap = 16
	mw, counter, err := newToolBudgetMiddlewareWithCounter(cap)
	if err != nil {
		t.Fatal(err)
	}
	var underlying int
	endpoint := mw.Streamable(func(ctx context.Context, input *compose.ToolInput) (*compose.StreamToolOutput, error) {
		underlying++
		return &compose.StreamToolOutput{
			Result: schema.StreamReaderFromArray([]string{`{"ok":true}`}),
		}, nil
	})
	ctx := context.Background()
	for i := 0; i < cap; i++ {
		out, err := endpoint(ctx, &compose.ToolInput{Name: "st", Arguments: `{}`, CallID: fmt.Sprintf("c%d", i)})
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		got := readAllStreamChunks(t, out.Result)
		if got != `{"ok":true}` {
			t.Fatalf("call %d result=%q", i+1, got)
		}
	}
	if underlying != cap {
		t.Fatalf("underlying=%d want %d", underlying, cap)
	}
	// 17th: budget JSON, no underlying invoke.
	out, err := endpoint(ctx, &compose.ToolInput{Name: "st", Arguments: `{}`, CallID: "c17"})
	if err != nil {
		t.Fatalf("budget call: %v", err)
	}
	if underlying != cap {
		t.Fatalf("underlying must not run past cap; got %d", underlying)
	}
	result := readAllStreamChunks(t, out.Result)
	if !strings.Contains(result, ToolBudgetExceededCode) {
		t.Fatalf("budget stream result=%q", result)
	}
	if counter.Count() != cap {
		t.Fatalf("counter=%d want %d", counter.Count(), cap)
	}
}

func TestToolBudgetMiddleware_EnhancedStreamableStopsAfterCap(t *testing.T) {
	t.Parallel()
	const cap = 16
	mw, counter, err := newToolBudgetMiddlewareWithCounter(cap)
	if err != nil {
		t.Fatal(err)
	}
	var underlying int
	endpoint := mw.EnhancedStreamable(func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedStreamableToolOutput, error) {
		underlying++
		return &compose.EnhancedStreamableToolOutput{
			Result: schema.StreamReaderFromArray([]*schema.ToolResult{{
				Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: "ok"}},
			}}),
		}, nil
	})
	ctx := context.Background()
	for i := 0; i < cap; i++ {
		out, err := endpoint(ctx, &compose.ToolInput{Name: "es", Arguments: `{}`, CallID: fmt.Sprintf("c%d", i)})
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		if out == nil || out.Result == nil {
			t.Fatalf("call %d nil output", i+1)
		}
		out.Result.Close()
	}
	if underlying != cap {
		t.Fatalf("underlying=%d want %d", underlying, cap)
	}
	_, err = endpoint(ctx, &compose.ToolInput{Name: "es", Arguments: `{}`, CallID: "c17"})
	if err == nil {
		t.Fatal("expected budget error on enhanced streamable path")
	}
	if !errors.Is(err, ErrToolBudgetExceeded) {
		t.Fatalf("err=%v want ErrToolBudgetExceeded", err)
	}
	if underlying != cap {
		t.Fatalf("underlying must not run past cap; got %d", underlying)
	}
	if counter.Count() != cap {
		t.Fatalf("counter=%d want %d", counter.Count(), cap)
	}
}

func TestToolBudgetMiddleware_AllFourInterfacesShareCounter(t *testing.T) {
	t.Parallel()
	mw, counter, err := newToolBudgetMiddlewareWithCounter(4)
	if err != nil {
		t.Fatal(err)
	}
	inv := mw.Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: "ok"}, nil
	})
	st := mw.Streamable(func(ctx context.Context, input *compose.ToolInput) (*compose.StreamToolOutput, error) {
		return &compose.StreamToolOutput{Result: schema.StreamReaderFromArray([]string{"ok"})}, nil
	})
	enh := mw.EnhancedInvokable(func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
		return &compose.EnhancedInvokableToolOutput{Result: &schema.ToolResult{}}, nil
	})
	es := mw.EnhancedStreamable(func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedStreamableToolOutput, error) {
		return &compose.EnhancedStreamableToolOutput{
			Result: schema.StreamReaderFromArray([]*schema.ToolResult{{}}),
		}, nil
	})
	ctx := context.Background()
	if _, err := inv(ctx, &compose.ToolInput{Name: "a", Arguments: `{}`}); err != nil {
		t.Fatal(err)
	}
	out, err := st(ctx, &compose.ToolInput{Name: "b", Arguments: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	readAllStreamChunks(t, out.Result)
	if _, err := enh(ctx, &compose.ToolInput{Name: "c", Arguments: `{}`}); err != nil {
		t.Fatal(err)
	}
	esOut, err := es(ctx, &compose.ToolInput{Name: "d", Arguments: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	esOut.Result.Close()
	if counter.Count() != 4 {
		t.Fatalf("counter=%d want 4 (one charge per interface invoke)", counter.Count())
	}
	// Fifth call on any path fails.
	out2, err := inv(ctx, &compose.ToolInput{Name: "e", Arguments: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if out2 == nil || !strings.Contains(out2.Result, ToolBudgetExceededCode) {
		t.Fatalf("5th invokable want budget result, got %#v", out2)
	}
}

func readAllStreamChunks(t *testing.T, sr *schema.StreamReader[string]) string {
	t.Helper()
	if sr == nil {
		t.Fatal("nil stream")
	}
	defer sr.Close()
	var b strings.Builder
	for {
		chunk, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Recv: %v", err)
		}
		b.WriteString(chunk)
	}
	return b.String()
}

// streamOnlyBudgetTool implements only StreamableTool (no InvokableRun).
type streamOnlyBudgetTool struct {
	name  string
	calls *int64
}

func (t *streamOnlyBudgetTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.name, Desc: "stream-only budget tool",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"q": {Type: schema.String, Required: true},
		}),
	}, nil
}

func (t *streamOnlyBudgetTool) StreamableRun(_ context.Context, _ string, _ ...tool.Option) (*schema.StreamReader[string], error) {
	if t.calls != nil {
		atomic.AddInt64(t.calls, 1)
	}
	return schema.StreamReaderFromArray([]string{`{"ok":true}`}), nil
}

// enhancedStreamOnlyBudgetTool implements only EnhancedStreamableTool.
type enhancedStreamOnlyBudgetTool struct {
	name  string
	calls *int64
}

func (t *enhancedStreamOnlyBudgetTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.name, Desc: "enhanced-stream-only budget tool",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"q": {Type: schema.String, Required: true},
		}),
	}, nil
}

func (t *enhancedStreamOnlyBudgetTool) StreamableRun(_ context.Context, _ *schema.ToolArgument, _ ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
	if t.calls != nil {
		atomic.AddInt64(t.calls, 1)
	}
	return schema.StreamReaderFromArray([]*schema.ToolResult{{
		Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: `{"ok":true}`}},
	}}), nil
}

// TestToolBudget_StreamableOnly_RealToolsNodeCap proves a streamable-only tool
// through real compose.ToolsNode is hard-capped at 16 (17th returns budget JSON,
// underlying not invoked). Uses WithCounter so ToolsNode does not need ADK
// run-local state; agent path below uses production run-local middleware.
func TestToolBudget_StreamableOnly_RealToolsNodeCap(t *testing.T) {
	ctx := context.Background()
	const cap = DefaultMaxToolInvocations // 16
	var calls int64
	st := &streamOnlyBudgetTool{name: "stream_only", calls: &calls}
	mw, counter, err := newToolBudgetMiddlewareWithCounter(cap)
	if err != nil {
		t.Fatal(err)
	}
	node, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{
		Tools:               []tool.BaseTool{st},
		ExecuteSequentially: true,
		ToolCallMiddlewares: []compose.ToolMiddleware{mw},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cap; i++ {
		msgs, err := node.Invoke(ctx, schema.AssistantMessage("", []schema.ToolCall{
			{ID: fmt.Sprintf("c-%d", i), Function: schema.FunctionCall{Name: "stream_only", Arguments: `{"q":"x"}`}},
		}))
		if err != nil {
			t.Fatalf("invoke %d: %v", i, err)
		}
		if len(msgs) != 1 || !strings.Contains(msgs[0].Content, `"ok":true`) {
			t.Fatalf("invoke %d msgs=%v", i, msgs)
		}
	}
	if atomic.LoadInt64(&calls) != int64(cap) {
		t.Fatalf("after %d invokes calls=%d", cap, calls)
	}
	// 17th via Stream path (streamable-only: Streamable middleware charged once).
	sr, err := node.Stream(ctx, schema.AssistantMessage("", []schema.ToolCall{
		{ID: "c-17", Function: schema.FunctionCall{Name: "stream_only", Arguments: `{"q":"x"}`}},
	}))
	if err != nil {
		t.Fatalf("stream 17: %v", err)
	}
	var streamMsgs [][]*schema.Message
	for {
		chunk, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("stream recv: %v", err)
		}
		streamMsgs = append(streamMsgs, chunk)
	}
	if atomic.LoadInt64(&calls) != int64(cap) {
		t.Fatalf("17th stream must not invoke underlying; calls=%d", calls)
	}
	if counter.Count() != cap {
		t.Fatalf("counter=%d want %d", counter.Count(), cap)
	}
	flat, err := schema.ConcatMessageArray(streamMsgs)
	if err != nil {
		t.Fatalf("concat: %v", err)
	}
	if len(flat) != 1 || !strings.Contains(flat[0].Content, ToolBudgetExceededCode) {
		t.Fatalf("17th stream messages=%v", flat)
	}
}

// TestToolBudget_StreamableOnly_AgentProductionMiddlewareCap exercises a real
// AgenticEngine Run with production NewToolBudgetMiddleware run-local state and
// single-action-per-turn: cap=2 success, 3rd streamable call returns budget JSON
// without underlying invoke, then final text.
func TestToolBudget_StreamableOnly_AgentProductionMiddlewareCap(t *testing.T) {
	ctx := context.Background()
	const cap = 2
	var calls int64
	st := &streamOnlyBudgetTool{name: "stream_agent", calls: &calls}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: st, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}
	// One call per model turn (single-action guard): 2 allowed, 3rd budget soft-result.
	mdl := &scriptedAgenticModel{
		responses: []*schema.AgenticMessage{
			agenticFunctionCall("stream_agent", "s-1", `{"q":"x"}`),
			agenticFunctionCall("stream_agent", "s-2", `{"q":"x"}`),
			agenticFunctionCall("stream_agent", "s-3", `{"q":"x"}`),
			agenticmsg.AssistantText("stream-cap-done"),
		},
	}
	cfg := baseAgenticCfg(mdl, []tool.BaseTool{st}, cat)
	cfg.MaxToolInvocations = cap
	agent, err := BuildAgenticAgent(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()})
	res, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws-stream-agent", RunID: "run-stream-agent",
		Messages: []*schema.AgenticMessage{agenticmsg.UserText("go")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := atomic.LoadInt64(&calls)
	if got != int64(cap) {
		t.Fatalf("stream-only agent underlying calls=%d want exactly %d", got, cap)
	}
	// Ordinary Streamable budget excess returns tool-result JSON (not hard error);
	// the scripted model then emits final text. Hard-fail if that path is broken.
	if res.Err != nil {
		t.Fatalf("stream-only agent unexpected hard err=%v text=%q", res.Err, res.FinalAssistantText)
	}
	if res.FinalAssistantText != "stream-cap-done" {
		t.Fatalf("stream-only agent text=%q want exactly stream-cap-done", res.FinalAssistantText)
	}
}

// TestToolBudget_EnhancedStreamableOnly_RealToolsNodeCap caps enhanced-stream-only
// tools at 16 through real compose.ToolsNode. Pinned Eino converts EnhancedStreamable
// → EnhancedInvokable for Invoke (conversion wraps the already-middleware-wrapped
// EnhancedStreamable endpoint), so Invoke charges EnhancedStreamable middleware once.
// Stream uses EnhancedStreamable directly. Drain every returned stream; assert the
// 17th budget failure at the point ToolsNode surfaces it (Stream returns error
// immediately with ErrToolBudgetExceeded for non-interrupt enhanced failures).
func TestToolBudget_EnhancedStreamableOnly_RealToolsNodeCap(t *testing.T) {
	ctx := context.Background()
	const cap = DefaultMaxToolInvocations
	var calls int64
	et := &enhancedStreamOnlyBudgetTool{name: "enh_stream_only", calls: &calls}
	mw, counter, err := newToolBudgetMiddlewareWithCounter(cap)
	if err != nil {
		t.Fatal(err)
	}
	node, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{
		Tools:               []tool.BaseTool{et},
		ExecuteSequentially: true,
		ToolCallMiddlewares: []compose.ToolMiddleware{mw},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 16 successes via Invoke (enhanced path: EnhancedStreamable → Invokable convert).
	for i := 0; i < cap; i++ {
		msgs, err := node.Invoke(ctx, schema.AssistantMessage("", []schema.ToolCall{
			{ID: fmt.Sprintf("e-%d", i), Function: schema.FunctionCall{Name: "enh_stream_only", Arguments: `{"q":"x"}`}},
		}))
		if err != nil {
			t.Fatalf("invoke %d: %v", i, err)
		}
		if len(msgs) != 1 {
			t.Fatalf("invoke %d: want 1 tool message, got %d", i, len(msgs))
		}
	}
	if atomic.LoadInt64(&calls) != int64(cap) {
		t.Fatalf("calls=%d want %d", calls, cap)
	}
	if counter.Count() != cap {
		t.Fatalf("counter after 16 invokes=%d want %d", counter.Count(), cap)
	}

	// 17th via Stream: EnhancedStreamable middleware must reject before underlying.
	// ToolsNode.Stream runs tasks first; budget error is non-interrupt → returns
	// immediately as error (no stream reader to drain).
	sr, err := node.Stream(ctx, schema.AssistantMessage("", []schema.ToolCall{
		{ID: "e-17", Function: schema.FunctionCall{Name: "enh_stream_only", Arguments: `{"q":"x"}`}},
	}))
	if err == nil {
		// If a reader is returned, drain fully — budget failure must still surface.
		drainErr := drainMessageArrayStream(sr)
		if drainErr == nil {
			t.Fatal("17th Stream returned nil error and drained cleanly; want ErrToolBudgetExceeded")
		}
		err = drainErr
	}
	if !errors.Is(err, ErrToolBudgetExceeded) {
		t.Fatalf("17th Stream err=%v want errors.Is ErrToolBudgetExceeded", err)
	}
	if atomic.LoadInt64(&calls) != int64(cap) {
		t.Fatalf("17th must not invoke underlying; calls=%d", calls)
	}
	if counter.Count() != cap {
		t.Fatalf("counter=%d want %d (17th must not charge past cap)", counter.Count(), cap)
	}

	// 17th via Invoke also fails closed with the same sentinel (enhanced convert path).
	_, err = node.Invoke(ctx, schema.AssistantMessage("", []schema.ToolCall{
		{ID: "e-18", Function: schema.FunctionCall{Name: "enh_stream_only", Arguments: `{"q":"x"}`}},
	}))
	if !errors.Is(err, ErrToolBudgetExceeded) {
		t.Fatalf("17th Invoke err=%v want ErrToolBudgetExceeded", err)
	}
	if atomic.LoadInt64(&calls) != int64(cap) {
		t.Fatalf("post-17th invoke calls=%d", calls)
	}

	// Direct EnhancedStreamable endpoint still hard-fails at cap.
	es := mw.EnhancedStreamable(func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedStreamableToolOutput, error) {
		t.Fatal("underlying must not run")
		return nil, nil
	})
	_, err = es(ctx, &compose.ToolInput{Name: "enh_stream_only", Arguments: `{}`})
	if !errors.Is(err, ErrToolBudgetExceeded) {
		t.Fatalf("direct enhanced streamable err=%v", err)
	}
}

// drainMessageArrayStream fully drains a ToolsNode Stream reader.
func drainMessageArrayStream(sr *schema.StreamReader[[]*schema.Message]) error {
	if sr == nil {
		return errors.New("nil stream reader")
	}
	for {
		_, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// TestToolBudget_EnhancedStreamableOnly_AgentProductionMiddlewareCap proves
// production NewToolBudgetMiddleware run-local budget through real
// BuildAgenticAgent + TypedRunner for an enhanced-streamable-only tool with
// one call per model turn (single-action guard). cap=2: 2 success, 3rd fails
// with ErrToolBudgetExceeded. (Full 16/17 is proved at ToolsNode Stream level
// because MaxIterations=8 makes 17 agent turns unreachable.)
func TestToolBudget_EnhancedStreamableOnly_AgentProductionMiddlewareCap(t *testing.T) {
	ctx := context.Background()
	const cap = 2
	var calls int64
	et := &enhancedStreamOnlyBudgetTool{name: "enh_stream_agent", calls: &calls}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: et, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}
	mdl := &scriptedAgenticModel{
		responses: []*schema.AgenticMessage{
			agenticFunctionCall("enh_stream_agent", "es-1", `{"q":"x"}`),
			agenticFunctionCall("enh_stream_agent", "es-2", `{"q":"x"}`),
			agenticFunctionCall("enh_stream_agent", "es-3", `{"q":"x"}`),
			agenticmsg.AssistantText("should-not-reach"),
		},
	}
	cfg := baseAgenticCfg(mdl, []tool.BaseTool{et}, cat)
	cfg.MaxToolInvocations = cap
	agent, err := BuildAgenticAgent(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cpID, err := EnsureAgentRunCheckpointID("ws-enh-stream-agent", "run-enh-stream-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewTypedRunner(adk.TypedRunnerConfig[*schema.AgenticMessage]{
		Agent:           agent,
		EnableStreaming: false, // Invoke path: enhanced convert + production run-local MW
		CheckPointStore: newMemCheckPointStore(),
	})
	iter := runner.Run(ctx, []*schema.AgenticMessage{agenticmsg.UserText("go")}, adk.WithCheckPointID(cpID))
	var hard error
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
			hard = ev.Err
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
	if hard == nil {
		t.Fatalf("expected budget hard failure on 3rd enhanced-streamable call; finalText=%q", finalText)
	}
	if !errors.Is(hard, ErrToolBudgetExceeded) {
		t.Fatalf("hard err=%v want ErrToolBudgetExceeded", hard)
	}
	got := atomic.LoadInt64(&calls)
	if got != int64(cap) {
		t.Fatalf("enhanced-stream agent underlying calls=%d want exactly %d", got, cap)
	}
	if finalText == "should-not-reach" {
		t.Fatal("must not reach final assistant text after budget hard failure")
	}
}

// TestToolBudget_EnhancedStreamableOnly_AgenticEngineStreamingSingleCallCap proves
// AgenticEngine (EnableStreaming:true) routes enhanced-streamable-only tools and
// enforces production run-local budget for single-call-per-turn sequences
// (avoids pinned multi-tool Stream race). cap=2: 2 success, 3rd fails hard.
func TestToolBudget_EnhancedStreamableOnly_AgenticEngineStreamingSingleCallCap(t *testing.T) {
	ctx := context.Background()
	const cap = 2
	var calls int64
	et := &enhancedStreamOnlyBudgetTool{name: "enh_stream_engine", calls: &calls}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: et, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}
	mdl := &scriptedAgenticModel{
		responses: []*schema.AgenticMessage{
			agenticFunctionCall("enh_stream_engine", "e-1", `{"q":"x"}`),
			agenticFunctionCall("enh_stream_engine", "e-2", `{"q":"x"}`),
			agenticFunctionCall("enh_stream_engine", "e-3", `{"q":"x"}`),
			agenticmsg.AssistantText("should-not-reach"),
		},
	}
	cfg := baseAgenticCfg(mdl, []tool.BaseTool{et}, cat)
	cfg.MaxToolInvocations = cap
	agent, err := BuildAgenticAgent(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()})
	res, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws-enh-engine", RunID: "run-enh-engine",
		Messages: []*schema.AgenticMessage{agenticmsg.UserText("go")},
	})
	var hard error
	if err != nil {
		hard = err
	} else if res != nil {
		hard = res.Err
	}
	if hard == nil {
		t.Fatalf("expected budget hard failure; res=%+v", res)
	}
	if !errors.Is(hard, ErrToolBudgetExceeded) {
		t.Fatalf("hard err=%v want ErrToolBudgetExceeded", hard)
	}
	if atomic.LoadInt64(&calls) != int64(cap) {
		t.Fatalf("calls=%d want %d", calls, cap)
	}
}

// TestToolBudget_SharedProductionMiddlewareConcurrentEnhancedStreamableIndependentBudgets
// race-tests concurrent Runs sharing one production NewToolBudgetMiddleware with
// enhanced-streamable-only tools (one call per turn): each run gets a full
// independent budget within MaxIterations.
func TestToolBudget_SharedProductionMiddlewareConcurrentEnhancedStreamableIndependentBudgets(t *testing.T) {
	ctx := context.Background()
	const cap = 3
	var calls int64
	et := &enhancedStreamOnlyBudgetTool{name: "budget_enh_stream", calls: &calls}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: et, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}
	sharedMW, err := NewToolBudgetMiddleware(cap)
	if err != nil {
		t.Fatal(err)
	}
	buildAgent := func(tag string) *adk.TypedChatModelAgent[*schema.AgenticMessage] {
		responses := make([]*schema.AgenticMessage, 0, cap+1)
		for i := 0; i < cap; i++ {
			responses = append(responses, agenticFunctionCall("budget_enh_stream", fmt.Sprintf("%s-%d", tag, i), `{"q":"x"}`))
		}
		responses = append(responses, agenticmsg.AssistantText("enh-stream-shared-done"))
		mdl := &scriptedAgenticModel{responses: responses}
		agent, err := buildAgenticAgentWithBudgetMW(ctx, baseAgenticCfg(mdl, []tool.BaseTool{et}, cat), sharedMW)
		if err != nil {
			t.Fatal(err)
		}
		return agent
	}
	agents := []*adk.TypedChatModelAgent[*schema.AgenticMessage]{buildAgent("a"), buildAgent("b")}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			cpID, err := EnsureAgentRunCheckpointID("ws-enh-stream-shared", fmt.Sprintf("run-es-%d", i), "")
			if err != nil {
				errs <- err
				return
			}
			runner := adk.NewTypedRunner(adk.TypedRunnerConfig[*schema.AgenticMessage]{
				Agent:           agents[i],
				EnableStreaming: false,
				CheckPointStore: newMemCheckPointStore(),
			})
			iter := runner.Run(ctx, []*schema.AgenticMessage{agenticmsg.UserText("go")}, adk.WithCheckPointID(cpID))
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
					errs <- ev.Err
					return
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
			if finalText != "enh-stream-shared-done" {
				errs <- fmt.Errorf("unexpected final text %q", finalText)
				return
			}
			errs <- nil
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent enhanced-streamable run: %v", err)
		}
	}
	got := atomic.LoadInt64(&calls)
	if got != int64(cap*2) {
		t.Fatalf("shared production middleware enhanced-streamable calls=%d want %d", got, cap*2)
	}
}

// TestToolBudget_SharedProductionMiddlewareConcurrentStreamableIndependentBudgets
// mirrors the invokable concurrent proof for streamable-only tools sharing one
// production NewToolBudgetMiddleware instance (one call per turn).
func TestToolBudget_SharedProductionMiddlewareConcurrentStreamableIndependentBudgets(t *testing.T) {
	ctx := context.Background()
	const cap = 3
	var calls int64
	st := &streamOnlyBudgetTool{name: "budget_stream", calls: &calls}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: st, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}
	sharedMW, err := NewToolBudgetMiddleware(cap)
	if err != nil {
		t.Fatal(err)
	}
	buildAgent := func(tag string) *adk.TypedChatModelAgent[*schema.AgenticMessage] {
		responses := make([]*schema.AgenticMessage, 0, cap+1)
		for i := 0; i < cap; i++ {
			responses = append(responses, agenticFunctionCall("budget_stream", fmt.Sprintf("%s-%d", tag, i), `{"q":"x"}`))
		}
		responses = append(responses, agenticmsg.AssistantText("stream-shared-done"))
		mdl := &scriptedAgenticModel{responses: responses}
		agent, err := buildAgenticAgentWithBudgetMW(ctx, baseAgenticCfg(mdl, []tool.BaseTool{st}, cat), sharedMW)
		if err != nil {
			t.Fatal(err)
		}
		return agent
	}
	agents := []*adk.TypedChatModelAgent[*schema.AgenticMessage]{buildAgent("a"), buildAgent("b")}
	engine := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := engine.Run(ctx, agents[i], AgenticRunInput{
				WorkspaceID: "ws-stream-shared",
				RunID:       fmt.Sprintf("run-ss-%d", i),
				Messages:    []*schema.AgenticMessage{agenticmsg.UserText("go")},
			})
			if err != nil {
				errs <- err
				return
			}
			if res != nil && res.Err != nil {
				errs <- res.Err
				return
			}
			errs <- nil
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent streamable run: %v", err)
		}
	}
	got := atomic.LoadInt64(&calls)
	if got != int64(cap*2) {
		t.Fatalf("shared production middleware streamable calls=%d want %d", got, cap*2)
	}
}

// TestToolBudget_StreamableInterruptResumePreservesCount uses HITL + streamable
// ordinary tool: cap=2, HITL acquires then interrupts; resume re-acquires (n=2);
// subsequent streamable tool must not invoke.
func TestToolBudget_StreamableInterruptResumePreservesCount(t *testing.T) {
	ctx := context.Background()
	const cap = 2
	hitl := &agenticHITLTool{name: "hitl_stream_budget"}
	var streamCalls int64
	st := &streamOnlyBudgetTool{name: "stream_budget", calls: &streamCalls}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: hitl, Exposure: ToolExposureDeferred},
		{Tool: st, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}
	mdl := &scriptedAgenticModel{
		responses: []*schema.AgenticMessage{
			agenticFunctionCall("hitl_stream_budget", "h-1", `{"q":"need"}`),
			agenticFunctionCall("stream_budget", "s-1", `{"q":"a"}`),
			agenticmsg.AssistantText("stream-resume-done"),
		},
	}
	cfg := baseAgenticCfg(mdl, []tool.BaseTool{hitl, st}, cat)
	cfg.MaxToolInvocations = cap
	agent, err := BuildAgenticAgent(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemCheckPointStore()
	engine := NewAgenticEngine(AgenticEngineConfig{Store: store})
	r1, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws-stream-resume", RunID: "run-stream-resume",
		Messages: []*schema.AgenticMessage{agenticmsg.UserText("start")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !r1.Interrupted || len(r1.InterruptContextIDs) == 0 {
		t.Fatalf("expected interrupt: %+v", r1)
	}
	targets := map[string]any{}
	for _, id := range r1.InterruptContextIDs {
		targets[id] = "yes"
	}
	r2, err := engine.Resume(ctx, agent, AgenticResumeInput{
		WorkspaceID:  "ws-stream-resume",
		RunID:        "run-stream-resume",
		CheckpointID: r1.CheckpointID,
		Targets:      targets,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := atomic.LoadInt64(&streamCalls); got != 0 {
		t.Fatalf("streamCalls=%d want 0 (resume must preserve budget so HITL resume fills cap=2)", got)
	}
	// After resume, HITL re-acquisition fills cap=2; the streamable tool must not
	// run, and the agent must still complete (budget soft-result path) or report
	// a typed budget error — never silently succeed with wrong accounting.
	if r2.Err != nil && !errors.Is(r2.Err, ErrToolBudgetExceeded) {
		t.Fatalf("resume unexpected err=%v text=%q", r2.Err, r2.FinalAssistantText)
	}
	if r2.Err == nil && r2.FinalAssistantText != "stream-resume-done" {
		t.Fatalf("resume text=%q want stream-resume-done (or typed budget err)", r2.FinalAssistantText)
	}
}

// runWithADKRunLocalState installs *adk.State on the compose context so production
// NewToolBudgetMiddleware can use adk.GetRunLocalValue / SetRunLocalValue without
// a full ChatModelAgent Run (which is capped at MaxIterations=8 turns).
func runWithADKRunLocalState(ctx context.Context, fn func(context.Context) error) error {
	g := compose.NewGraph[struct{}, struct{}](compose.WithGenLocalState(func(context.Context) *adk.State {
		return &adk.State{Extra: map[string]any{}}
	}))
	if err := g.AddLambdaNode("run", compose.InvokableLambda(func(ctx context.Context, _ struct{}) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})); err != nil {
		return err
	}
	if err := g.AddEdge(compose.START, "run"); err != nil {
		return err
	}
	if err := g.AddEdge("run", compose.END); err != nil {
		return err
	}
	r, err := g.Compile(ctx)
	if err != nil {
		return err
	}
	_, err = r.Invoke(ctx, struct{}{})
	return err
}

// TestToolBudget_EnhancedStreamable_ProductionRunLocal_RealToolsNodeStreamCap is the
// mandatory 16/17 acceptance proof for EnhancedStreamable with production
// NewToolBudgetMiddleware run-local state through real compose.ToolsNode.Stream.
// One tool call per Stream invocation (single-action structural invariant); drain
// and Close every returned stream. First 16 succeed; 17th is exact ErrToolBudgetExceeded
// without underlying invoke. Does not use the test-only counter helper.
func TestToolBudget_EnhancedStreamable_ProductionRunLocal_RealToolsNodeStreamCap(t *testing.T) {
	ctx := context.Background()
	const cap = DefaultMaxToolInvocations // 16
	var calls int64
	et := &enhancedStreamOnlyBudgetTool{name: "enh_stream_prod", calls: &calls}

	mw, err := NewToolBudgetMiddleware(cap)
	if err != nil {
		t.Fatal(err)
	}
	node, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{
		Tools:               []tool.BaseTool{et},
		ExecuteSequentially: true,
		ToolCallMiddlewares: []compose.ToolMiddleware{mw},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = runWithADKRunLocalState(ctx, func(runCtx context.Context) error {
		// 16 successes: one call per Stream invocation; drain + Close every stream.
		for i := 0; i < cap; i++ {
			sr, serr := node.Stream(runCtx, schema.AssistantMessage("", []schema.ToolCall{
				{ID: fmt.Sprintf("p-%d", i), Function: schema.FunctionCall{Name: "enh_stream_prod", Arguments: `{"q":"x"}`}},
			}))
			if serr != nil {
				return fmt.Errorf("stream %d: %w", i, serr)
			}
			if sr == nil {
				return fmt.Errorf("stream %d: nil reader", i)
			}
			// Drain fully then Close exactly once.
			if derr := drainMessageArrayStream(sr); derr != nil {
				sr.Close()
				return fmt.Errorf("stream %d drain: %w", i, derr)
			}
			sr.Close()
		}
		if got := atomic.LoadInt64(&calls); got != int64(cap) {
			return fmt.Errorf("after %d streams calls=%d", cap, got)
		}

		// 17th: exact budget error at ToolsNode surface; no underlying invoke.
		sr17, serr := node.Stream(runCtx, schema.AssistantMessage("", []schema.ToolCall{
			{ID: "p-17", Function: schema.FunctionCall{Name: "enh_stream_prod", Arguments: `{"q":"x"}`}},
		}))
		if serr == nil {
			// If a reader is returned, drain fully — budget failure must surface.
			drainErr := drainMessageArrayStream(sr17)
			sr17.Close()
			if drainErr == nil {
				return errors.New("17th Stream returned nil error and drained cleanly; want ErrToolBudgetExceeded")
			}
			serr = drainErr
		} else if sr17 != nil {
			sr17.Close()
		}
		if !errors.Is(serr, ErrToolBudgetExceeded) {
			return fmt.Errorf("17th Stream err=%v want errors.Is ErrToolBudgetExceeded", serr)
		}
		if got := atomic.LoadInt64(&calls); got != int64(cap) {
			return fmt.Errorf("17th must not invoke underlying; calls=%d", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&calls); got != int64(cap) {
		t.Fatalf("final calls=%d want %d", got, cap)
	}
}

// TestToolBudget_EnhancedStreamable_ProductionRunLocal_RealToolsNodeStreamCap_Race
// race-tests the production run-local ToolsNode Stream 16/17 proof.
func TestToolBudget_EnhancedStreamable_ProductionRunLocal_RealToolsNodeStreamCap_Race(t *testing.T) {
	// Reuse the same test body under -race by invoking it directly.
	TestToolBudget_EnhancedStreamable_ProductionRunLocal_RealToolsNodeStreamCap(t)
}
