package einoruntime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/modelapi"
)

// scriptedAgenticModel is a fake model.AgenticModel for builder/engine tests.
type scriptedAgenticModel struct {
	mu        sync.Mutex
	responses []*schema.AgenticMessage
	calls     atomic.Int64
	lastOpts  []model.Option
	onCall    func(call int, input []*schema.AgenticMessage, opts []model.Option)
}

func (m *scriptedAgenticModel) next(input []*schema.AgenticMessage, opts []model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := int(m.calls.Add(1))
	m.lastOpts = append([]model.Option(nil), opts...)
	if m.onCall != nil {
		m.onCall(n, input, opts)
	}
	if len(m.responses) == 0 {
		return nil, errors.New("scriptedAgenticModel: no more responses")
	}
	resp := m.responses[0]
	m.responses = m.responses[1:]
	return resp, nil
}

func (m *scriptedAgenticModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	return m.next(input, opts)
}

func (m *scriptedAgenticModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	msg, err := m.next(input, opts)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{msg}), nil
}

var _ model.AgenticModel = (*scriptedAgenticModel)(nil)

func baseAgenticCfg(mdl model.AgenticModel, tools []tool.BaseTool, cat *ToolCatalogSnapshot) AgenticAgentBuildConfig {
	return AgenticAgentBuildConfig{
		Name:                     "test-agentic",
		Instruction:              "You are a test agent.",
		Model:                    mdl,
		Tools:                    tools,
		Catalog:                  cat,
		MaxIterations:            0, // normalize to 8
		ToolSearchMode:           ToolSearchModeClientBounded,
		ClientToolSearchVerified: true,
		PromptCacheKey:           "cache-key-run-1",
	}
}

func TestBuildAgenticAgent_RejectsUnverifiedCapability(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &stubTool{name: "t1", desc: "d", params: testParams()}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: st, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := baseAgenticCfg(&scriptedAgenticModel{}, []tool.BaseTool{st}, cat)
	cfg.ClientToolSearchVerified = false
	_, err = BuildAgenticAgent(ctx, cfg)
	if !errors.Is(err, ErrAgenticClientToolSearchUnverified) {
		t.Fatalf("got %v", err)
	}
}

func TestBuildAgenticAgent_RejectsWrongMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &stubTool{name: "t1", desc: "d", params: testParams()}
	cat, _ := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: st, Exposure: ToolExposureDeferred}})
	cfg := baseAgenticCfg(&scriptedAgenticModel{}, []tool.BaseTool{st}, cat)
	cfg.ToolSearchMode = "hosted"
	_, err := BuildAgenticAgent(ctx, cfg)
	if !errors.Is(err, ErrAgenticToolSearchMode) {
		t.Fatalf("got %v", err)
	}
	cfg.ToolSearchMode = ""
	_, err = BuildAgenticAgent(ctx, cfg)
	if !errors.Is(err, ErrAgenticToolSearchMode) {
		t.Fatalf("empty mode: %v", err)
	}
}

func TestBuildAgenticAgent_RejectsNon8Iterations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mdl := &scriptedAgenticModel{responses: []*schema.AgenticMessage{agenticmsg.AssistantText("ok")}}
	cfg := baseAgenticCfg(mdl, nil, nil)
	cfg.MaxIterations = 20
	_, err := BuildAgenticAgent(ctx, cfg)
	if !errors.Is(err, ErrAgenticMaxIterations) {
		t.Fatalf("got %v", err)
	}
	cfg.MaxIterations = 0
	agent, err := BuildAgenticAgent(ctx, cfg)
	if err != nil || agent == nil {
		t.Fatalf("zero maxiter: %v", err)
	}
	cfg.MaxIterations = 8
	if _, err := BuildAgenticAgent(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestBuildAgenticAgent_RejectsTooManyImmediate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var inputs []ToolCatalogBuildEntry
	var tools []tool.BaseTool
	for i := 0; i < MaxImmediatePlatformTools+1; i++ {
		st := &stubTool{name: "imm_" + itoa(i), desc: "d", params: testParams()}
		inputs = append(inputs, ToolCatalogBuildEntry{Tool: st, Exposure: ToolExposureImmediate, PlatformControl: true})
		tools = append(tools, st)
	}
	cat, err := BuildToolCatalog(ctx, inputs)
	if err != nil {
		t.Fatal(err)
	}
	cfg := baseAgenticCfg(&scriptedAgenticModel{}, tools, cat)
	_, err = BuildAgenticAgent(ctx, cfg)
	if !errors.Is(err, ErrAgenticTooManyImmediate) {
		t.Fatalf("got %v", err)
	}
}

func TestBuildAgenticAgent_RejectsMaxToolInvocationsAbove16(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := baseAgenticCfg(&scriptedAgenticModel{}, nil, nil)
	cfg.MaxToolInvocations = 17
	_, err := BuildAgenticAgent(ctx, cfg)
	if !errors.Is(err, ErrToolBudgetMaxInvalid) {
		t.Fatalf("got %v", err)
	}
	cfg.MaxToolInvocations = -1
	_, err = BuildAgenticAgent(ctx, cfg)
	if !errors.Is(err, ErrToolBudgetMaxInvalid) {
		t.Fatalf("negative: %v", err)
	}
	cfg.MaxToolInvocations = 0
	if _, err := BuildAgenticAgent(ctx, cfg); err != nil {
		t.Fatalf("zero should normalize: %v", err)
	}
	cfg.MaxToolInvocations = 8
	if _, err := BuildAgenticAgent(ctx, cfg); err != nil {
		t.Fatalf("stricter limit ok: %v", err)
	}
}

func TestBuildAgenticAgent_CatalogMismatchAndCollision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := &stubTool{name: "a", desc: "d", params: testParams()}
	b := &stubTool{name: "b", desc: "d", params: testParams()}
	cat, _ := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: a, Exposure: ToolExposureDeferred}})
	cfg := baseAgenticCfg(&scriptedAgenticModel{}, []tool.BaseTool{a, b}, cat)
	_, err := BuildAgenticAgent(ctx, cfg)
	if !errors.Is(err, ErrModelToolCatalogMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	_, err = BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: &stubTool{name: ClientToolSearchToolName, desc: "x"}, Exposure: ToolExposureDeferred},
	})
	if !errors.Is(err, ErrToolCatalogSearchNameCollision) {
		t.Fatalf("collision: %v", err)
	}
}

// TestBuildAgenticAgent_FailClosedToolsCatalogMembership proves one-to-one
// executable tools ↔ catalog: nils are never filtered, non-empty catalogs reject
// empty/nil/all-nil/missing/extra/duplicate tools, and a truly empty catalog is
// the tool-less path.
func TestBuildAgenticAgent_FailClosedToolsCatalogMembership(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := &stubTool{name: "a", desc: "d", params: testParams()}
	b := &stubTool{name: "b", desc: "d", params: testParams()}
	catAB, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: a, Exposure: ToolExposureDeferred},
		{Tool: b, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}
	catA, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: a, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}
	emptyCat, err := BuildToolCatalog(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Non-empty catalog + Tools=nil
	cfg := baseAgenticCfg(&scriptedAgenticModel{}, nil, catA)
	if _, err := BuildAgenticAgent(ctx, cfg); !errors.Is(err, ErrToolCatalogCountMismatch) && !errors.Is(err, ErrModelToolCatalogMismatch) {
		t.Fatalf("nil tools vs non-empty catalog: %v", err)
	}

	// Non-empty catalog + empty tools slice
	cfg = baseAgenticCfg(&scriptedAgenticModel{}, []tool.BaseTool{}, catA)
	if _, err := BuildAgenticAgent(ctx, cfg); !errors.Is(err, ErrToolCatalogCountMismatch) && !errors.Is(err, ErrModelToolCatalogMismatch) {
		t.Fatalf("empty tools vs non-empty catalog: %v", err)
	}

	// All-nil tools
	cfg = baseAgenticCfg(&scriptedAgenticModel{}, []tool.BaseTool{nil, nil}, catA)
	if _, err := BuildAgenticAgent(ctx, cfg); !errors.Is(err, ErrAgenticNilTool) {
		t.Fatalf("all-nil tools: %v", err)
	}

	// Nil mixed in (must not silently drop and pass count check)
	cfg = baseAgenticCfg(&scriptedAgenticModel{}, []tool.BaseTool{a, nil, b}, catAB)
	if _, err := BuildAgenticAgent(ctx, cfg); !errors.Is(err, ErrAgenticNilTool) {
		t.Fatalf("mixed nil: %v", err)
	}

	// Missing executable
	cfg = baseAgenticCfg(&scriptedAgenticModel{}, []tool.BaseTool{a}, catAB)
	if _, err := BuildAgenticAgent(ctx, cfg); !errors.Is(err, ErrToolCatalogCountMismatch) && !errors.Is(err, ErrModelToolCatalogMismatch) {
		t.Fatalf("missing executable: %v", err)
	}

	// Extra executable
	cfg = baseAgenticCfg(&scriptedAgenticModel{}, []tool.BaseTool{a, b}, catA)
	if _, err := BuildAgenticAgent(ctx, cfg); !errors.Is(err, ErrModelToolCatalogMismatch) {
		t.Fatalf("extra executable: %v", err)
	}

	// Duplicate executable
	cfg = baseAgenticCfg(&scriptedAgenticModel{}, []tool.BaseTool{a, a}, catA)
	if _, err := BuildAgenticAgent(ctx, cfg); !errors.Is(err, ErrModelToolCatalogMismatch) {
		t.Fatalf("duplicate executable: %v", err)
	}

	// Happy path still works
	cfg = baseAgenticCfg(&scriptedAgenticModel{}, []tool.BaseTool{a, b}, catAB)
	if _, err := BuildAgenticAgent(ctx, cfg); err != nil {
		t.Fatalf("happy path: %v", err)
	}

	// Truly empty catalog + nil tools: tool-less path (no search middleware required)
	cfg = baseAgenticCfg(&scriptedAgenticModel{}, nil, emptyCat)
	cfg.ClientToolSearchVerified = false
	cfg.ToolSearchMode = ""
	agent, err := BuildAgenticAgent(ctx, cfg)
	if err != nil || agent == nil {
		t.Fatalf("empty catalog + nil tools: agent=%v err=%v", agent, err)
	}

	// Truly empty catalog + empty tools slice
	cfg = baseAgenticCfg(&scriptedAgenticModel{}, []tool.BaseTool{}, emptyCat)
	cfg.ClientToolSearchVerified = false
	if _, err := BuildAgenticAgent(ctx, cfg); err != nil {
		t.Fatalf("empty catalog + empty tools: %v", err)
	}

	// Empty catalog + non-empty tools fails (count mismatch) once tools path entered
	cfg = baseAgenticCfg(&scriptedAgenticModel{}, []tool.BaseTool{a}, emptyCat)
	if _, err := BuildAgenticAgent(ctx, cfg); !errors.Is(err, ErrToolCatalogCountMismatch) && !errors.Is(err, ErrModelToolCatalogMismatch) {
		t.Fatalf("empty catalog + tools: %v", err)
	}

	// Nil catalog + tools requires catalog
	cfg = baseAgenticCfg(&scriptedAgenticModel{}, []tool.BaseTool{a}, nil)
	if _, err := BuildAgenticAgent(ctx, cfg); !errors.Is(err, ErrAgenticCatalogRequired) {
		t.Fatalf("nil catalog + tools: %v", err)
	}
}

func TestBuildAgenticAgent_RequiresModelAndCacheKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, err := BuildAgenticAgent(ctx, AgenticAgentBuildConfig{PromptCacheKey: "k"})
	if !errors.Is(err, ErrAgenticModelRequired) {
		t.Fatalf("model: %v", err)
	}
	_, err = BuildAgenticAgent(ctx, AgenticAgentBuildConfig{Model: &scriptedAgenticModel{}})
	if !errors.Is(err, ErrAgenticPromptCacheKeyRequired) {
		t.Fatalf("cache key: %v", err)
	}
}

func TestPromptCacheKey_ForcedOnEveryCallAndNotOverridable(t *testing.T) {
	t.Parallel()
	const platformKey = "platform-cache-key-xyz"

	// Unit: appendPlatformPromptCacheKey places platform key after caller opts.
	opts := appendPlatformPromptCacheKey(
		[]model.Option{modelapi.WithPromptCacheKey("attacker-key")},
		platformKey,
	)
	if len(opts) != 2 {
		t.Fatalf("opts len = %d", len(opts))
	}

	// Unit: forcedPromptCacheModel always appends one option beyond caller opts.
	var gotCallerN, gotTotalN int
	spy := &optCountModel{
		onCall: func(callerOpts, totalOpts int) {
			gotCallerN = callerOpts
			gotTotalN = totalOpts
		},
	}
	forced := &forcedPromptCacheModel{inner: spy, key: platformKey}
	_, err := forced.Generate(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("x")},
		modelapi.WithPromptCacheKey("attacker-key"),
		model.WithTemperature(0.2),
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotCallerN != 2 || gotTotalN != 3 {
		t.Fatalf("caller=%d total=%d (want 2 and 3 — platform key appended last)", gotCallerN, gotTotalN)
	}

	// Integration: BuildAgenticAgent + Engine must invoke the model (WrapModel chain).
	ctx := context.Background()
	mdl := &scriptedAgenticModel{
		responses: []*schema.AgenticMessage{agenticmsg.AssistantText("ok")},
	}
	agent, err := BuildAgenticAgent(ctx, AgenticAgentBuildConfig{
		Name:           "cache-test",
		Model:          mdl,
		PromptCacheKey: platformKey,
		MaxIterations:  8,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := NewAgenticEngine(AgenticEngineConfig{})
	result, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws-1",
		RunID:       "run-cache",
		Messages:    []*schema.AgenticMessage{agenticmsg.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalAssistantText != "ok" {
		t.Fatalf("text = %q", result.FinalAssistantText)
	}
	if mdl.calls.Load() < 1 {
		t.Fatal("expected model call through WrapModel chain")
	}
	// Platform key option is present (at least one option beyond framework tools opts).
	if len(mdl.lastOpts) == 0 {
		t.Fatal("expected model options including forced prompt cache key")
	}
}

// optCountModel records how many opts the wrapper received vs passed through.
type optCountModel struct {
	onCall func(callerOpts, totalOpts int)
}

func (m *optCountModel) Generate(_ context.Context, _ []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	// forcedPromptCacheModel appends after caller opts; we cannot see "caller"
	// count here. The test measures via forcedPromptCacheModel by wrapping
	// differently — see TestPromptCacheKey which uses known caller count.
	if m.onCall != nil {
		// totalOpts is what this model sees; caller count is total-1 for our forced wrapper.
		m.onCall(len(opts)-1, len(opts))
	}
	return agenticmsg.AssistantText("ok"), nil
}

func (m *optCountModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{msg}), nil
}
