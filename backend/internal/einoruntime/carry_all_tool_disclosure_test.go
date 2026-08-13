package einoruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestCarryAll_RefusesMoreThanHardLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var inputs []ToolCatalogBuildEntry
	var tools []tool.BaseTool
	for i := 0; i < CarryAllHardLimit+1; i++ {
		st := &stubTool{name: "biz_" + itoa(i), desc: "d", params: testParams()}
		inputs = append(inputs, ToolCatalogBuildEntry{Tool: st, Exposure: ToolExposureDeferred})
		tools = append(tools, st)
	}
	cat, err := BuildToolCatalog(ctx, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCarryAllToolDisclosureMiddleware(cat); !errors.Is(err, ErrAgenticCarryAllTooLarge) {
		t.Fatalf("middleware: %v", err)
	}
	cfg := AgenticAgentBuildConfig{
		Name:                     "carry",
		Model:                    &scriptedAgenticModel{},
		Tools:                    tools,
		Catalog:                  cat,
		ToolSearchMode:           ToolSearchModeCarryAll,
		FunctionCallingVerified:  true,
		ClientToolSearchVerified: false,
		PromptCacheKey:           "k",
	}
	if _, err := BuildAgenticAgent(ctx, cfg); !errors.Is(err, ErrAgenticCarryAllTooLarge) {
		t.Fatalf("builder: %v", err)
	}
}

func TestCarryAll_BeforeModelRewriteStateFullSchemaNoSearch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := &stubTool{name: "zeta_tool", desc: "z", params: testParams()}
	b := &stubTool{name: "alpha_tool", desc: "a", params: testParams()}
	ctrl := &stubTool{name: "ctrl_tool", desc: "c", params: testParams()}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: a, Exposure: ToolExposureDeferred},
		{Tool: b, Exposure: ToolExposureDeferred},
		{Tool: ctrl, Exposure: ToolExposureImmediate, PlatformControl: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Catalog rows stay deferred even though carry-all discloses full schema.
	ent, ok := cat.Entry("alpha_tool")
	if !ok || ent.Exposure != ToolExposureDeferred {
		t.Fatalf("catalog overlay must not rewrite exposure: %+v", ent)
	}
	mw, err := NewCarryAllToolDisclosureMiddleware(cat)
	if err != nil {
		t.Fatal(err)
	}
	ia, _ := a.Info(ctx)
	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{
		Messages:          []*schema.AgenticMessage{schema.UserAgenticMessage("hi")},
		ToolInfos:         []*schema.ToolInfo{ia},
		DeferredToolInfos: []*schema.ToolInfo{ia},
	}
	_, out, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.DeferredToolInfos != nil {
		t.Fatalf("DeferredToolInfos=%v", out.DeferredToolInfos)
	}
	if len(out.ToolInfos) != 3 {
		t.Fatalf("ToolInfos len=%d", len(out.ToolInfos))
	}
	want := []string{"alpha_tool", "ctrl_tool", "zeta_tool"}
	for i, ti := range out.ToolInfos {
		if ti.Name != want[i] {
			t.Fatalf("order[%d]=%q want %q", i, ti.Name, want[i])
		}
		if ti.Name == PlatformCatalogSearchToolName {
			t.Fatal("must not register catalog search")
		}
	}
	stateBad := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{
		ToolInfos: []*schema.ToolInfo{{Name: PlatformCatalogSearchToolName, Desc: "x"}},
	}
	if _, _, err := mw.BeforeModelRewriteState(ctx, stateBad, nil); !errors.Is(err, ErrModelToolCatalogMismatch) {
		t.Fatalf("search name in carry-all state: %v", err)
	}
}

func TestCarryAll_BeforeAgentNoSearchTool(t *testing.T) {
	t.Parallel()
	cat, _ := buildTestCatalog(t, "alpha_tool")
	mw, err := NewCarryAllToolDisclosureMiddleware(cat)
	if err != nil {
		t.Fatal(err)
	}
	runCtx := &adk.ChatModelAgentContext{
		Tools:          []tool.BaseTool{&stubTool{name: "alpha_tool", desc: "a"}},
		ToolSearchTool: clientToolSearchToolInfo(),
	}
	_, out, err := mw.BeforeAgent(context.Background(), runCtx)
	if err != nil {
		t.Fatal(err)
	}
	if out.ToolSearchTool != nil {
		t.Fatal("ToolSearchTool must be nil")
	}
	if len(out.Tools) != 1 {
		t.Fatalf("must not append search executor: %d", len(out.Tools))
	}
}

func TestCarryAll_AllowsHardLimitWithPlatformControl(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var inputs []ToolCatalogBuildEntry
	var tools []tool.BaseTool
	for i := 0; i < CarryAllHardLimit; i++ {
		st := &stubTool{name: "biz_" + itoa(i), desc: "d", params: testParams()}
		inputs = append(inputs, ToolCatalogBuildEntry{Tool: st, Exposure: ToolExposureDeferred})
		tools = append(tools, st)
	}
	ctrl := &stubTool{name: "ctrl_ok", desc: "c", params: testParams()}
	inputs = append(inputs, ToolCatalogBuildEntry{Tool: ctrl, Exposure: ToolExposureImmediate, PlatformControl: true})
	tools = append(tools, ctrl)
	cat, err := BuildToolCatalog(ctx, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if n := businessToolCount(cat); n != CarryAllHardLimit {
		t.Fatalf("business=%d", n)
	}
	if _, err := NewCarryAllToolDisclosureMiddleware(cat); err != nil {
		t.Fatal(err)
	}
	cfg := AgenticAgentBuildConfig{
		Name:                     "carry",
		Model:                    &scriptedAgenticModel{},
		Tools:                    tools,
		Catalog:                  cat,
		ToolSearchMode:           ToolSearchModeCarryAll,
		FunctionCallingVerified:  true,
		ClientToolSearchVerified: false,
		PromptCacheKey:           "k",
	}
	if _, err := BuildAgenticAgent(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}
