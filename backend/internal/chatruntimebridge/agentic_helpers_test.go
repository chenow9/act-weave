package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/contextwindow"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type stubTool struct {
	name   string
	desc   string
	params map[string]any
}

func (s *stubTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: s.name,
		Desc: s.desc,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"q": {Type: schema.String, Desc: "q"},
		}),
	}, nil
}

func (s *stubTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return `{"ok":true}`, nil
}

var _ tool.InvokableTool = (*stubTool)(nil)

func TestToolExposureFromCatalog_DeferredOnly(t *testing.T) {
	ctx := context.Background()
	tools := []tool.BaseTool{
		&stubTool{name: "alpha", desc: "tool a"},
		&stubTool{name: "beta", desc: "tool b"},
	}
	cat, err := buildFrozenToolCatalog(ctx, tools, json.RawMessage(`{
		"schemaVersion":"capability-snapshot.v1",
		"releases":[
			{"capabilityId":"c1","releaseId":"r1","kind":"TOOL","callableName":"alpha","callableDescription":"tool a","inputSchema":{"type":"object"},"riskLevel":"LOW","sideEffectLevel":"NONE"},
			{"capabilityId":"c2","releaseId":"r2","kind":"TOOL","callableName":"beta","callableDescription":"tool b","inputSchema":{"type":"object"},"riskLevel":"LOW","sideEffectLevel":"NONE"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	exp := toolExposureFromCatalog(cat)
	if len(exp.Immediate) != 0 {
		t.Fatalf("business tools must be deferred, immediate=%d", len(exp.Immediate))
	}
	if len(exp.DeferredMetadata) != 2 || len(exp.LoadCandidates) != 2 {
		t.Fatalf("deferred meta=%d cands=%d", len(exp.DeferredMetadata), len(exp.LoadCandidates))
	}
	// Digests stable
	d1 := cat.CatalogDigest()
	d2 := cat.CatalogDigest()
	if d1 == "" || d1 != d2 || len(d1) != 64 {
		t.Fatalf("digest=%q", d1)
	}
	// No secrets in digest/catalog JSON
	raw, _ := json.Marshal(cat.Entries())
	if strings.Contains(string(raw), "sk-") || strings.Contains(string(raw), "secret") {
		t.Fatalf("catalog leaked secret-like content: %s", raw)
	}
}

// Lock identity follows the persisted verification CAS relation: the capability
// document stamps the pre-CAS lock and the row lands one higher, so a live pair
// is (verifiedLockVersion=N, lockVersion=N+1). Both a snapshot that drifted past
// its evidence and one that claims evidence for its own current lock are stale.
func TestRequireVerifiedAgenticSnapshot_RejectsEmptyAndStale(t *testing.T) {
	buildSnap := func(cfg modelconfig.Config, lock int64, caps json.RawMessage) json.RawMessage {
		raw, err := json.Marshal(map[string]any{
			"id": cfg.ID, "provider": cfg.Provider, "apiBase": cfg.APIBase,
			"modelName": cfg.ModelName, "options": cfg.Options, "lockVersion": lock,
			"agenticCapabilities": caps,
			"runtimeCapabilities": json.RawMessage(`{}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	cfg := modelconfig.Config{
		ID:       "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1",
		Provider: "openai", APIBase: "https://api.example.com/v1", ModelName: "m",
		Options: json.RawMessage(`{}`), RuntimeCapabilities: json.RawMessage(`{}`),
		LockVersion: 3, Status: modelconfig.StatusVerified,
	}
	digest := modelconfig.WireConfigDigest(cfg)
	doc, err := modelconfig.CanonicalAgenticCapabilities(time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC), 1, digest)
	if err != nil {
		t.Fatal(err)
	}
	// Drifted: evidence stamped at lock 1 (pair lock 2) but the config is at 3.
	caps, _ := json.Marshal(doc)
	cfg.AgenticCapabilities = caps
	if err := requireVerifiedAgenticSnapshot(buildSnap(cfg, 3, caps), cfg); err == nil {
		t.Fatal("expected stale lock reject for drifted evidence")
	}
	// Self-referential: evidence claims the config's own current lock. This can
	// never be persisted (CAS always bumps) and must not be accepted.
	selfCfg := cfg
	selfCfg.LockVersion = 2
	selfDoc, err := modelconfig.CanonicalAgenticCapabilities(
		time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC), 2, modelconfig.WireConfigDigest(selfCfg))
	if err != nil {
		t.Fatal(err)
	}
	selfCaps, _ := json.Marshal(selfDoc)
	selfCfg.AgenticCapabilities = selfCaps
	if err := requireVerifiedAgenticSnapshot(buildSnap(selfCfg, 2, selfCaps), selfCfg); err == nil {
		t.Fatal("expected reject when verifiedLockVersion equals the config lock")
	}
	// Empty agentic capabilities.
	emptyCfg := cfg
	emptyCfg.AgenticCapabilities = json.RawMessage(`{}`)
	if err := requireVerifiedAgenticSnapshot(
		buildSnap(emptyCfg, 3, json.RawMessage(`{}`)), emptyCfg,
	); err == nil {
		t.Fatal("expected empty agentic reject")
	}
	// Positive control: the exact pair the verification CAS writes.
	okCfg := cfg
	okCfg.LockVersion = 2
	okDoc, err := modelconfig.CanonicalAgenticCapabilities(
		time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC), 1, modelconfig.WireConfigDigest(okCfg))
	if err != nil {
		t.Fatal(err)
	}
	okCaps, _ := json.Marshal(okDoc)
	okSnap := buildSnap(okCfg, 2, okCaps)
	parsed, _, err := parseModelSnapshot(okSnap)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireVerifiedAgenticSnapshot(okSnap, parsed); err != nil {
		t.Fatalf("valid snapshot: %v", err)
	}
}

func TestRequireVerifiedAgenticSnapshot_AcceptsV2Tiers(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	base := modelconfig.Config{
		ID:       "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1",
		Provider: "openai", APIBase: "https://api.example.com/v1", ModelName: "m",
		Options: json.RawMessage(`{}`), RuntimeCapabilities: json.RawMessage(`{}`),
		LockVersion: 2, Status: modelconfig.StatusVerified,
	}
	digest := modelconfig.WireConfigDigest(base)
	buildSnap := func(cfg modelconfig.Config, caps json.RawMessage) json.RawMessage {
		raw, err := json.Marshal(map[string]any{
			"id": cfg.ID, "provider": cfg.Provider, "apiBase": cfg.APIBase,
			"modelName": cfg.ModelName, "options": cfg.Options, "lockVersion": cfg.LockVersion,
			"agenticCapabilities": caps, "runtimeCapabilities": json.RawMessage(`{}`),
			"status": cfg.Status,
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	mustAccept := func(name string, raw json.RawMessage) {
		t.Helper()
		cfg := base
		cfg.AgenticCapabilities = raw
		if err := requireVerifiedAgenticSnapshot(buildSnap(cfg, raw), cfg); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !frozenCapsAreNative(cfg) && name == "v1" {
			t.Fatal("v1 must be native")
		}
	}

	v1, err := modelconfig.CanonicalAgenticCapabilities(at, 1, digest)
	if err != nil {
		t.Fatal(err)
	}
	v1Raw, _ := json.Marshal(v1)
	mustAccept("v1", v1Raw)
	if !frozenCapsAreNative(modelconfig.Config{AgenticCapabilities: v1Raw}) {
		t.Fatal("v1 must be native")
	}

	v2Native := modelconfig.AgenticCapabilities{
		SchemaVersion: modelconfig.AgenticCapabilitiesSchemaV2,
		Protocol:      modelconfig.AgenticProtocolOpenAIResponsesV1,
		Streaming:     true, Usage: true,
		ToolCalling:     modelconfig.ToolCallingNativeClientSearch,
		ToolSearchModes: []string{modelconfig.AgenticToolSearchModeClient},
		ReasoningReplay: modelconfig.AgenticReasoningReplayEncryptedOrNone,
		VerifiedAdapter: modelconfig.VerifiedAdapterAgenticOpenAIV022,
		VerifiedAt:      at, VerifiedLockVersion: 1, VerifiedConfigDigest: digest,
	}
	v2NativeRaw, _ := json.Marshal(v2Native)
	mustAccept("v2 native", v2NativeRaw)
	if !frozenCapsAreNative(modelconfig.Config{AgenticCapabilities: v2NativeRaw}) {
		t.Fatal("v2 native must be native")
	}

	for _, calling := range []string{modelconfig.ToolCallingFunctionCalling, modelconfig.ToolCallingNone} {
		doc, err := modelconfig.CanonicalAgenticCapabilitiesV2(calling, at, 1, digest)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(doc)
		mustAccept("v2 "+calling, raw)
		if frozenCapsAreNative(modelconfig.Config{AgenticCapabilities: raw}) {
			t.Fatalf("%s must not be native", calling)
		}
	}
}

func TestErrToolBearingNonNativeCodes(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	digest := strings.Repeat("ab", 32)
	fc, err := modelconfig.CanonicalAgenticCapabilitiesV2(modelconfig.ToolCallingFunctionCalling, at, 1, digest)
	if err != nil {
		t.Fatal(err)
	}
	fcRaw, _ := json.Marshal(fc)
	if err := errToolBearingNonNative(modelconfig.Config{AgenticCapabilities: fcRaw}); !errors.Is(err, modelconfig.ErrToolDisclosureRuntimePending) {
		t.Fatalf("FC: %v", err)
	}
	none, err := modelconfig.CanonicalAgenticCapabilitiesV2(modelconfig.ToolCallingNone, at, 1, digest)
	if err != nil {
		t.Fatal(err)
	}
	noneRaw, _ := json.Marshal(none)
	if err := errToolBearingNonNative(modelconfig.Config{AgenticCapabilities: noneRaw}); !errors.Is(err, modelconfig.ErrAgentModelToolsUnsupported) {
		t.Fatalf("none: %v", err)
	}
}

type stubAgenticModel struct{}

func (stubAgenticModel) Generate(context.Context, []*schema.AgenticMessage, ...model.Option) (*schema.AgenticMessage, error) {
	return nil, errors.New("unused")
}
func (stubAgenticModel) Stream(context.Context, []*schema.AgenticMessage, ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return nil, errors.New("unused")
}

var _ model.AgenticModel = stubAgenticModel{}

func TestBuildAgenticChildAgent_FailClosedAndToolless(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	base := modelconfig.Config{
		ID: "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1", LockVersion: 2,
	}
	digest := modelconfig.WireConfigDigest(base)
	emptyCaps := json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[]}`)
	toolCaps := json.RawMessage(`{
		"schemaVersion":"capability-snapshot.v1",
		"releases":[{
			"capabilityId":"a77ce000-0000-4000-8000-000000000007","releaseId":"b88ce000-0000-4000-8000-000000000008","kind":"TOOL",
			"callableName":"alpha","callableDescription":"tool a",
			"inputSchema":{"type":"object"},"outputSchema":{},
			"riskLevel":"LOW","sideEffectLevel":"NONE","requiresConfirmation":false
		}]
	}`)
	b := &Bridge{maxTools: einoruntime.DefaultMaxToolInvocations}
	ctx := context.Background()

	mustV2 := func(calling string) modelconfig.Config {
		doc, err := modelconfig.CanonicalAgenticCapabilitiesV2(calling, at, 1, digest)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(doc)
		cfg := base
		cfg.AgenticCapabilities = raw
		return cfg
	}

	toolless := agentdelegation.AgenticAgentParts{
		AgentID: "child", Name: "child", Description: "d", Instruction: "be brief",
		Model: stubAgenticModel{}, Tools: nil, CapabilitySnapshot: emptyCaps,
	}
	if _, err := b.buildAgenticChildAgent(ctx, toolless, mustV2(modelconfig.ToolCallingFunctionCalling), nil); err != nil {
		t.Fatalf("tool-less FC child: %v", err)
	}
	if _, err := b.buildAgenticChildAgent(ctx, toolless, mustV2(modelconfig.ToolCallingNone), nil); err != nil {
		t.Fatalf("tool-less none child: %v", err)
	}

	tools := []tool.BaseTool{&stubTool{name: "alpha", desc: "tool a"}}
	bearing := toolless
	bearing.Tools = tools
	bearing.CapabilitySnapshot = toolCaps
	if _, err := b.buildAgenticChildAgent(ctx, bearing, mustV2(modelconfig.ToolCallingFunctionCalling), tools); !errors.Is(err, modelconfig.ErrToolDisclosureRuntimePending) {
		t.Fatalf("tool-bearing FC child: %v", err)
	}
	if _, err := b.buildAgenticChildAgent(ctx, bearing, mustV2(modelconfig.ToolCallingNone), tools); !errors.Is(err, modelconfig.ErrAgentModelToolsUnsupported) {
		t.Fatalf("tool-bearing none child: %v", err)
	}
}

func TestBuildRunPromptCacheKey_NoPII(t *testing.T) {
	cfg := modelconfig.Config{
		ID: "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1", LockVersion: 3,
	}
	empty, err := einoruntime.BuildToolCatalog(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	key, err := buildRunPromptCacheKey(cfg, "system prompt text", empty)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "aw:agentic:v1:") {
		t.Fatalf("key=%q", key)
	}
	for _, s := range []string{"workspace", "session", "user@", "system prompt text"} {
		if strings.Contains(key, s) {
			t.Fatalf("key leaked %q", s)
		}
	}
	// Same inputs → same key
	key2, err := buildRunPromptCacheKey(cfg, "system prompt text", empty)
	if err != nil || key != key2 {
		t.Fatalf("unstable key")
	}
}

func TestToolExposure_EstimateMaxLoadedBounds(t *testing.T) {
	// 0 deferred → max 0; 5 → 5; 40 → 40; 41 → 40 derived
	for _, n := range []int{0, 1, 5, 40, 41} {
		meta := make([]contextwindow.ToolMetadata, n)
		cands := make([]contextwindow.ToolSchema, n)
		for i := 0; i < n; i++ {
			name := "t" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + string(rune('0'+i%10))
			// unique names
			name = "tool_" + string(rune('a'+i%26)) + "_" + itoaLocal(i)
			meta[i] = contextwindow.ToolMetadata{Name: name, Description: "d"}
			cands[i] = contextwindow.ToolSchema{
				Name: name, Description: "d",
				Parameters: json.RawMessage(`{"type":"object"}`),
			}
		}
		exp := contextwindow.ToolExposureEstimate{DeferredMetadata: meta, LoadCandidates: cands}
		est, err := contextwindow.NewEstimator("o200k_base")
		if err != nil {
			t.Fatal(err)
		}
		got, err := est.EstimateAgenticRequest("sys", exp, nil)
		if n > 40 {
			// 41 deferred is allowed; max loaded clamped to 40
			if err != nil {
				t.Fatalf("n=%d err=%v", n, err)
			}
			if got.MaxLoadedToolCount != 40 {
				t.Fatalf("n=%d max=%d", n, got.MaxLoadedToolCount)
			}
		} else {
			if err != nil {
				t.Fatalf("n=%d err=%v", n, err)
			}
			if got.MaxLoadedToolCount != n {
				t.Fatalf("n=%d max=%d", n, got.MaxLoadedToolCount)
			}
		}
		_ = execution.AssemblyToolSearchModeClientBounded
	}
}

func itoaLocal(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
