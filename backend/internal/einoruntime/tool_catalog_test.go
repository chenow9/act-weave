package einoruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
)

type stubTool struct {
	name   string
	desc   string
	params map[string]*schema.ParameterInfo
}

func (t *stubTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{Name: t.name, Desc: t.desc}
	if t.params != nil {
		info.ParamsOneOf = schema.NewParamsOneOfByParams(t.params)
	}
	return info, nil
}

func (t *stubTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return `{"ok":true}`, nil
}

func testParams() map[string]*schema.ParameterInfo {
	return map[string]*schema.ParameterInfo{
		"q": {Type: schema.String, Desc: "query", Required: true},
	}
}

func TestBuildToolCatalog_DeterministicDigestAndSort(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Build in reverse alphabetical order; catalog must sort by name.
	inputs := []ToolCatalogBuildEntry{
		{Tool: &stubTool{name: "zeta_tool", desc: "z", params: testParams()}, Exposure: ToolExposureDeferred, Kind: ToolKindTool},
		{Tool: &stubTool{name: "alpha_tool", desc: "a", params: testParams()}, Exposure: ToolExposureDeferred, Kind: ToolKindTool},
		{Tool: &stubTool{name: "mid_tool", desc: "m", params: testParams()}, Exposure: ToolExposureImmediate, Kind: ToolKindTool, CapabilityID: "cap-1", PlatformControl: true},
	}
	c1, err := BuildToolCatalog(ctx, inputs)
	if err != nil {
		t.Fatal(err)
	}
	// Shuffle input order for second build.
	inputs2 := []ToolCatalogBuildEntry{inputs[2], inputs[0], inputs[1]}
	c2, err := BuildToolCatalog(ctx, inputs2)
	if err != nil {
		t.Fatal(err)
	}
	if c1.CatalogDigest() == "" || c1.CatalogDigest() != c2.CatalogDigest() {
		t.Fatalf("digests differ: %q vs %q", c1.CatalogDigest(), c2.CatalogDigest())
	}
	ents := c1.Entries()
	if len(ents) != 3 || ents[0].Name != "alpha_tool" || ents[1].Name != "mid_tool" || ents[2].Name != "zeta_tool" {
		t.Fatalf("entries not name-sorted: %+v", ents)
	}
	for _, e := range ents {
		if e.SchemaDigest == "" {
			t.Fatalf("empty schema digest for %q", e.Name)
		}
	}
	if c1.ImmediateCount() != 1 {
		t.Fatalf("immediate = %d", c1.ImmediateCount())
	}
	if c1.SchemaVersion() != ToolCatalogSchemaVersion {
		t.Fatalf("schema version = %q", c1.SchemaVersion())
	}
}

func TestBuildToolCatalog_DeepCopyImmutability(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	params := testParams()
	st := &stubTool{name: "mut_tool", desc: "original", params: params}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: st, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Mutate caller-owned params and tool fields after freeze.
	params["q"].Desc = "MUTATED"
	params["extra"] = &schema.ParameterInfo{Type: schema.String, Desc: "x"}
	st.name = "renamed"
	st.desc = "changed"

	ent, ok := cat.Entry("mut_tool")
	if !ok {
		t.Fatal("missing entry")
	}
	if ent.Description != "original" {
		t.Fatalf("description mutated: %q", ent.Description)
	}
	info, err := cat.ToolInfoCopy("mut_tool")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "mut_tool" || info.Desc != "original" {
		t.Fatalf("info copy mutated: %+v", info)
	}
	// Mutating returned copy must not affect catalog.
	info.Desc = "hacked"
	info2, _ := cat.ToolInfoCopy("mut_tool")
	if info2.Desc != "original" {
		t.Fatal("catalog info not independent")
	}
}

func TestBuildToolCatalog_DuplicateAndEmptyAndCollision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: &stubTool{name: "same", desc: "a"}},
		{Tool: &stubTool{name: "same", desc: "b"}},
	})
	if !errors.Is(err, ErrToolCatalogDuplicateName) {
		t.Fatalf("dup: %v", err)
	}
	_, err = BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: &stubTool{name: "  ", desc: "a"}},
	})
	if !errors.Is(err, ErrToolCatalogEmptyName) {
		t.Fatalf("empty: %v", err)
	}
	// Surrounding whitespace on raw name is non-canonical — fail closed (do not
	// freeze trimmed identity while ToolsNode would index the padded raw name).
	_, err = BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: &stubTool{name: " echo ", desc: "a"}},
	})
	if !errors.Is(err, ErrToolCatalogNonCanonicalName) {
		t.Fatalf("noncanonical padded name: %v", err)
	}
	_, err = BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: &stubTool{name: ClientToolSearchToolName, desc: "search"}},
	})
	if !errors.Is(err, ErrToolCatalogSearchNameCollision) {
		t.Fatalf("collision: %v", err)
	}
	_, err = BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: &stubTool{name: PlatformCatalogSearchToolName, desc: "search"}},
	})
	if !errors.Is(err, ErrToolCatalogSearchNameCollision) {
		t.Fatalf("platform search collision: %v", err)
	}
}

// TestBuildToolCatalog_NonCanonicalNameRejectedAndToolsNodeIdentity proves the
// adversarial case: raw name " echo " must not freeze as "echo". Catalog build,
// executable validation, partition, and Agentic builder all reject it. A real
// compose.ToolsNode indexes by the raw Info().Name, so accepting a trimmed
// catalog name while keeping the padded executable would make model call "echo"
// return unknown at routing time.
func TestBuildToolCatalog_NonCanonicalNameRejectedAndToolsNodeIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	padded := &stubTool{name: " echo ", desc: "echo tool", params: testParams()}
	// Catalog build fails closed.
	_, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: padded, Exposure: ToolExposureDeferred},
	})
	if !errors.Is(err, ErrToolCatalogNonCanonicalName) {
		t.Fatalf("BuildToolCatalog padded: %v", err)
	}

	// Canonical tool freezes and re-validates; ToolsNode indexes exact raw name.
	echo := &stubTool{name: "echo", desc: "echo tool", params: testParams()}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: echo, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.ValidateExecutableTools(ctx, []tool.BaseTool{echo}); err != nil {
		t.Fatalf("validate canonical: %v", err)
	}
	// Live padded sibling must not pass validation against the frozen "echo" catalog.
	if err := cat.ValidateExecutableTools(ctx, []tool.BaseTool{padded}); !errors.Is(err, ErrToolCatalogNonCanonicalName) &&
		!errors.Is(err, ErrModelToolCatalogMismatch) {
		t.Fatalf("validate padded against frozen: %v", err)
	}
	// Partition rejects noncanonical raw names.
	paddedInfo, _ := padded.Info(ctx)
	if _, _, err := cat.PartitionToolInfos([]*schema.ToolInfo{paddedInfo}, nil, ClientToolSearchToolName); err == nil ||
		(!errors.Is(err, ErrToolCatalogNonCanonicalName) && !errors.Is(err, ErrModelToolCatalogMismatch)) {
		t.Fatalf("partition padded: %v", err)
	}

	// Real ToolsNode: indexes by raw Name. Canonical "echo" is callable as "echo".
	// Padded tool alone would only be found under " echo ", not "echo".
	nodeOK, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{
		Tools:               []tool.BaseTool{echo},
		ExecuteSequentially: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := nodeOK.Invoke(ctx, schema.AssistantMessage("", []schema.ToolCall{
		{ID: "c1", Function: schema.FunctionCall{Name: "echo", Arguments: `{"q":"x"}`}},
	}))
	if err != nil {
		t.Fatalf("ToolsNode invoke echo: %v", err)
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, `"ok":true`) {
		t.Fatalf("ToolsNode msgs=%v", msgs)
	}
	// Padded-only ToolsNode: model-visible canonical "echo" is unknown.
	nodePad, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{
		Tools:               []tool.BaseTool{padded},
		ExecuteSequentially: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = nodePad.Invoke(ctx, schema.AssistantMessage("", []schema.ToolCall{
		{ID: "c2", Function: schema.FunctionCall{Name: "echo", Arguments: `{"q":"x"}`}},
	}))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("padded ToolsNode must miss canonical echo: err=%v", err)
	}
	// Builder: frozen canonical catalog + padded tools fails closed.
	mdl := &scriptedAgenticModel{responses: []*schema.AgenticMessage{agenticmsg.AssistantText("x")}}
	_, err = BuildAgenticAgent(ctx, baseAgenticCfg(mdl, []tool.BaseTool{padded}, cat))
	if err == nil {
		t.Fatal("BuildAgenticAgent must reject padded executable against frozen catalog")
	}
	if !errors.Is(err, ErrToolCatalogNonCanonicalName) && !errors.Is(err, ErrModelToolCatalogMismatch) {
		t.Fatalf("builder err=%v", err)
	}
}

func TestValidateExecutableTools_WhitespaceDescriptionRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Unchanged tool with surrounding whitespace on description must re-validate
	// against its own frozen snapshot (symmetric canonicalization).
	st := &stubTool{name: "ws_tool", desc: "  padded description  ", params: testParams()}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: st, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}
	ent, ok := cat.Entry("ws_tool")
	if !ok {
		t.Fatal("missing entry")
	}
	if ent.Description != "padded description" {
		t.Fatalf("frozen description not trimmed: %q", ent.Description)
	}
	info, err := cat.ToolInfoCopy("ws_tool")
	if err != nil {
		t.Fatal(err)
	}
	if info.Desc != "padded description" {
		t.Fatalf("frozen ToolInfo desc not trimmed: %q", info.Desc)
	}
	// Live tool still has whitespace; ValidateExecutableTools must accept it.
	if err := cat.ValidateExecutableTools(ctx, []tool.BaseTool{st}); err != nil {
		t.Fatalf("round-trip validation with whitespace desc: %v", err)
	}
	// Partition of live Info (with whitespace) must also succeed.
	live, _ := st.Info(ctx)
	if _, _, err := cat.PartitionToolInfos([]*schema.ToolInfo{live}, nil, ClientToolSearchToolName); err != nil {
		t.Fatalf("partition round-trip with whitespace: %v", err)
	}
}

func TestValidateExecutableTools_MismatchFailClose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := &stubTool{name: "a", desc: "da", params: testParams()}
	b := &stubTool{name: "b", desc: "db", params: testParams()}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: a, Exposure: ToolExposureDeferred},
		{Tool: b, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Happy path.
	if err := cat.ValidateExecutableTools(ctx, []tool.BaseTool{a, b}); err != nil {
		t.Fatal(err)
	}
	// Count mismatch.
	if err := cat.ValidateExecutableTools(ctx, []tool.BaseTool{a}); !errors.Is(err, ErrToolCatalogCountMismatch) {
		t.Fatalf("count: %v", err)
	}
	// Unknown tool.
	c := &stubTool{name: "c", desc: "dc", params: testParams()}
	if err := cat.ValidateExecutableTools(ctx, []tool.BaseTool{a, c}); !errors.Is(err, ErrToolCatalogUnknownTool) {
		t.Fatalf("unknown: %v", err)
	}
	// Schema mutation.
	mut := &stubTool{name: "a", desc: "da", params: map[string]*schema.ParameterInfo{
		"q": {Type: schema.Integer, Desc: "changed type", Required: true},
	}}
	if err := cat.ValidateExecutableTools(ctx, []tool.BaseTool{mut, b}); !errors.Is(err, ErrToolCatalogSchemaMismatch) {
		t.Fatalf("schema: %v", err)
	}
	// Description change.
	mutDesc := &stubTool{name: "a", desc: "CHANGED", params: testParams()}
	if err := cat.ValidateExecutableTools(ctx, []tool.BaseTool{mutDesc, b}); !errors.Is(err, ErrToolCatalogSchemaMismatch) {
		t.Fatalf("desc: %v", err)
	}
}

func TestPartitionToolInfos_IdempotentAndFailClose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := &stubTool{name: "alpha", desc: "weather alpha", params: testParams()}
	b := &stubTool{name: "beta", desc: "stock beta", params: testParams()}
	imm := &stubTool{name: "ctrl", desc: "platform", params: testParams()}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: a, Exposure: ToolExposureDeferred},
		{Tool: b, Exposure: ToolExposureDeferred},
		{Tool: imm, Exposure: ToolExposureImmediate, PlatformControl: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ia, _ := a.Info(ctx)
	ib, _ := b.Info(ctx)
	ii, _ := imm.Info(ctx)
	search := clientToolSearchToolInfo()

	// First partition: all tools may arrive in ToolInfos (genToolInfos) with deferred empty.
	im1, def1, err := cat.PartitionToolInfos([]*schema.ToolInfo{ii, ia, ib, search}, nil, ClientToolSearchToolName)
	if err != nil {
		t.Fatal(err)
	}
	if len(im1) != 1 || im1[0].Name != "ctrl" {
		t.Fatalf("immediate = %+v", im1)
	}
	if len(def1) != 2 || def1[0].Name != "alpha" || def1[1].Name != "beta" {
		t.Fatalf("deferred = %+v", def1)
	}
	// Idempotent: re-partition from already-rewritten state (immediate in ToolInfos, deferred in Deferred).
	im2, def2, err := cat.PartitionToolInfos(im1, def1, ClientToolSearchToolName)
	if err != nil {
		t.Fatal(err)
	}
	if len(im2) != 1 || len(def2) != 2 {
		t.Fatalf("idempotent sizes %d %d", len(im2), len(def2))
	}
	// One identical cross-side same-name occurrence is the Eino resume collapse case.
	imX, defX, err := cat.PartitionToolInfos(
		[]*schema.ToolInfo{ii, ia}, // alpha also on immediate side
		[]*schema.ToolInfo{ia, ib}, // alpha on deferred side (identical)
		ClientToolSearchToolName,
	)
	if err != nil {
		t.Fatalf("cross-side identical collapse: %v", err)
	}
	if len(imX) != 1 || len(defX) != 2 {
		t.Fatalf("cross-side sizes immediate=%d deferred=%d", len(imX), len(defX))
	}
	// Same-side duplicate must FAIL (even with matching schema).
	if _, _, err := cat.PartitionToolInfos([]*schema.ToolInfo{ia, ia, ib, ii}, nil, ClientToolSearchToolName); !errors.Is(err, ErrToolCatalogDuplicateInPartition) {
		t.Fatalf("same-side dup: %v", err)
	}
	if _, _, err := cat.PartitionToolInfos(nil, []*schema.ToolInfo{ia, ia, ib, ii}, ClientToolSearchToolName); !errors.Is(err, ErrToolCatalogDuplicateInPartition) {
		t.Fatalf("same-side deferred dup: %v", err)
	}
	// Cross-side mismatch (same name, different schema) fails closed.
	mut := &schema.ToolInfo{Name: "alpha", Desc: "weather alpha", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"q": {Type: schema.Integer, Desc: "changed", Required: true},
	})}
	if _, _, err := cat.PartitionToolInfos([]*schema.ToolInfo{mut}, []*schema.ToolInfo{ia, ib, ii}, ClientToolSearchToolName); !errors.Is(err, ErrToolCatalogSchemaMismatch) {
		t.Fatalf("cross-side schema mismatch: %v", err)
	}
	// Missing tool.
	if _, _, err := cat.PartitionToolInfos([]*schema.ToolInfo{ia}, nil, ClientToolSearchToolName); !errors.Is(err, ErrToolCatalogMissingTool) {
		t.Fatalf("missing: %v", err)
	}
	// Unknown.
	unk := &schema.ToolInfo{Name: "nope", Desc: "x"}
	if _, _, err := cat.PartitionToolInfos([]*schema.ToolInfo{ia, ib, ii, unk}, nil, ClientToolSearchToolName); !errors.Is(err, ErrToolCatalogUnknownTool) {
		t.Fatalf("unknown: %v", err)
	}
	// Nil info.
	if _, _, err := cat.PartitionToolInfos([]*schema.ToolInfo{nil}, nil, ClientToolSearchToolName); !errors.Is(err, ErrModelToolCatalogMismatch) {
		t.Fatalf("nil: %v", err)
	}
}

func TestPartitionToolInfos_SearchExecutorValidatedBeforeExclusion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := &stubTool{name: "alpha", desc: "weather alpha", params: testParams()}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: a, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}
	ia, _ := a.Info(ctx)
	goodSearch := clientToolSearchToolInfo()

	// Happy path: one well-formed platform search executor is validated then excluded.
	im, def, err := cat.PartitionToolInfos([]*schema.ToolInfo{ia, goodSearch}, nil, ClientToolSearchToolName)
	if err != nil {
		t.Fatalf("valid search: %v", err)
	}
	if len(im) != 0 || len(def) != 1 || def[0].Name != "alpha" {
		t.Fatalf("search must not appear in partition: imm=%+v def=%+v", im, def)
	}
	// Search must not be treated as deferred business tool (no recursive disclose).
	for _, ti := range def {
		if ti.Name == ClientToolSearchToolName {
			t.Fatal("search executor leaked into deferred partition")
		}
	}

	// Duplicate search executor on same side fails (validation before exclusion).
	if _, _, err := cat.PartitionToolInfos(
		[]*schema.ToolInfo{ia, goodSearch, clientToolSearchToolInfo()},
		nil,
		ClientToolSearchToolName,
	); !errors.Is(err, ErrToolCatalogDuplicateInPartition) {
		t.Fatalf("duplicate search: %v", err)
	}
	// Duplicate search on deferred side fails too.
	if _, _, err := cat.PartitionToolInfos(
		[]*schema.ToolInfo{ia},
		[]*schema.ToolInfo{goodSearch, clientToolSearchToolInfo()},
		ClientToolSearchToolName,
	); !errors.Is(err, ErrToolCatalogDuplicateInPartition) {
		t.Fatalf("duplicate search deferred: %v", err)
	}

	// Malformed search executor (wrong parameter schema) fails closed.
	malformed := &schema.ToolInfo{
		Name: ClientToolSearchToolName,
		Desc: "looks like search",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"q": {Type: schema.String, Desc: "not the platform contract", Required: true},
		}),
	}
	if _, _, err := cat.PartitionToolInfos(
		[]*schema.ToolInfo{ia, malformed},
		nil,
		ClientToolSearchToolName,
	); !errors.Is(err, ErrToolCatalogSearchExecutorInvalid) {
		t.Fatalf("malformed search schema: %v", err)
	}
	// Empty description fails.
	emptyDesc := &schema.ToolInfo{
		Name:        ClientToolSearchToolName,
		Desc:        "   ",
		ParamsOneOf: goodSearch.ParamsOneOf,
	}
	if _, _, err := cat.PartitionToolInfos(
		[]*schema.ToolInfo{ia, emptyDesc},
		nil,
		ClientToolSearchToolName,
	); !errors.Is(err, ErrToolCatalogSearchExecutorInvalid) {
		t.Fatalf("empty search desc: %v", err)
	}
	// Description mismatch (non-empty but not the frozen platform text) fails.
	wrongDesc := &schema.ToolInfo{
		Name:        ClientToolSearchToolName,
		Desc:        "arbitrary non-empty description that is not the platform contract",
		ParamsOneOf: goodSearch.ParamsOneOf,
	}
	if _, _, err := cat.PartitionToolInfos(
		[]*schema.ToolInfo{ia, wrongDesc},
		nil,
		ClientToolSearchToolName,
	); !errors.Is(err, ErrToolCatalogSearchExecutorInvalid) {
		t.Fatalf("description mismatch: %v", err)
	}
	// Unexpected Extra metadata fails (even with otherwise-canonical fields).
	withExtra := clientToolSearchToolInfo()
	withExtra.Extra = map[string]any{"spoof": "yes", "api_key": "sk-secret"}
	if _, _, err := cat.PartitionToolInfos(
		[]*schema.ToolInfo{ia, withExtra},
		nil,
		ClientToolSearchToolName,
	); !errors.Is(err, ErrToolCatalogSearchExecutorInvalid) {
		t.Fatalf("unexpected Extra: %v", err)
	}
	// Mismatched Extra on an otherwise empty-looking map key still fails.
	withEmptyishExtra := clientToolSearchToolInfo()
	withEmptyishExtra.Extra = map[string]any{"": ""}
	if _, _, err := cat.PartitionToolInfos(
		[]*schema.ToolInfo{ia, withEmptyishExtra},
		nil,
		ClientToolSearchToolName,
	); !errors.Is(err, ErrToolCatalogSearchExecutorInvalid) {
		t.Fatalf("mismatched Extra: %v", err)
	}
	// Cross-partition native executor duplication fails (unlike ordinary tools).
	// One well-formed executor on ToolInfos + one on DeferredToolInfos is illegal.
	if _, _, err := cat.PartitionToolInfos(
		[]*schema.ToolInfo{ia, clientToolSearchToolInfo()},
		[]*schema.ToolInfo{clientToolSearchToolInfo()},
		ClientToolSearchToolName,
	); !errors.Is(err, ErrToolCatalogDuplicateInPartition) {
		t.Fatalf("cross-partition search executor duplicate: %v", err)
	}
	// Name is fail-closed: surrounding whitespace on the executor name is
	// non-canonical and must be rejected (ToolsNode indexes raw Name).
	wsNameSearch := clientToolSearchToolInfo()
	wsNameSearch.Name = "  " + ClientToolSearchToolName + "  "
	if _, _, err := cat.PartitionToolInfos(
		[]*schema.ToolInfo{ia, wsNameSearch},
		nil,
		ClientToolSearchToolName,
	); err == nil ||
		(!errors.Is(err, ErrToolCatalogNonCanonicalName) &&
			!errors.Is(err, ErrModelToolCatalogMismatch) &&
			!errors.Is(err, ErrToolCatalogSearchExecutorInvalid)) {
		t.Fatalf("whitespace-padded search executor name must fail: %v", err)
	}
	// Description still uses trim canonicalization: leading/trailing space on
	// description of a well-formed executor still passes.
	wsDescSearch := clientToolSearchToolInfo()
	wsDescSearch.Desc = "  " + wsDescSearch.Desc + "  "
	if _, _, err := cat.PartitionToolInfos(
		[]*schema.ToolInfo{ia, wsDescSearch},
		nil,
		ClientToolSearchToolName,
	); err != nil {
		t.Fatalf("description whitespace on legitimate executor must pass: %v", err)
	}
	// Missing query param (nil ParamsOneOf) fails.
	// Use the frozen description so failure is specifically the schema contract.
	noParams := &schema.ToolInfo{Name: ClientToolSearchToolName, Desc: goodSearch.Desc}
	if _, _, err := cat.PartitionToolInfos(
		[]*schema.ToolInfo{ia, noParams},
		nil,
		ClientToolSearchToolName,
	); !errors.Is(err, ErrToolCatalogSearchExecutorInvalid) {
		t.Fatalf("nil params search: %v", err)
	}
}

// TestValidatePlatformSearchExecutor_CanonicalContractRoundTrip proves the
// legitimate platform executor (build path) is accepted, and that name/desc/
// schema/Extra form one frozen identity.
func TestValidatePlatformSearchExecutor_CanonicalContractRoundTrip(t *testing.T) {
	t.Parallel()
	// Legitimate frozen executor passes.
	if err := validatePlatformSearchExecutorInfo(clientToolSearchToolInfo()); err != nil {
		t.Fatalf("legitimate executor: %v", err)
	}
	// Empty Extra map is treated as absent (no unexpected keys).
	emptyExtra := clientToolSearchToolInfo()
	emptyExtra.Extra = map[string]any{}
	if err := validatePlatformSearchExecutorInfo(emptyExtra); err != nil {
		t.Fatalf("empty Extra map: %v", err)
	}
	// Description character flip fails.
	bad := clientToolSearchToolInfo()
	bad.Desc = bad.Desc + "!"
	if err := validatePlatformSearchExecutorInfo(bad); err == nil {
		t.Fatal("description suffix must fail")
	}
	// Extra key fails.
	bad = clientToolSearchToolInfo()
	bad.Extra = map[string]any{"x": 1}
	if err := validatePlatformSearchExecutorInfo(bad); err == nil {
		t.Fatal("Extra must fail")
	}
}

func TestToolCatalog_ConcurrencySafe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tools := make([]ToolCatalogBuildEntry, 0, 20)
	exec := make([]tool.BaseTool, 0, 20)
	for i := 0; i < 20; i++ {
		st := &stubTool{name: "tool_" + itoa(i), desc: "desc " + itoa(i), params: testParams()}
		tools = append(tools, ToolCatalogBuildEntry{Tool: st, Exposure: ToolExposureDeferred})
		exec = append(exec, st)
	}
	cat, err := BuildToolCatalog(ctx, tools)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cat.ValidateExecutableTools(ctx, exec); err != nil {
				t.Errorf("validate: %v", err)
			}
			infos := make([]*schema.ToolInfo, 0, len(exec))
			for _, e := range exec {
				info, _ := e.Info(ctx)
				infos = append(infos, info)
			}
			if _, _, err := cat.PartitionToolInfos(infos, nil, ClientToolSearchToolName); err != nil {
				t.Errorf("partition: %v", err)
			}
			if _, err := cat.ToolInfoCopy("tool_0"); err != nil {
				t.Errorf("copy: %v", err)
			}
		}()
	}
	wg.Wait()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

func TestDigestHasNoSecrets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Tool Info with Extra that looks secret — Extra must not affect digest.
	st := &stubTool{name: "sec", desc: "d", params: testParams()}
	info, _ := st.Info(ctx)
	info.Extra = map[string]any{"api_key": "sk-secret", "password": "hunter2"}
	// Build via custom path: BuildToolCatalog reads Info() which doesn't set Extra.
	// Manually verify deepCopy drops Extra.
	cp, err := deepCopyToolInfo(info)
	if err != nil {
		t.Fatal(err)
	}
	if cp.Extra != nil {
		t.Fatalf("Extra not stripped: %v", cp.Extra)
	}
	// Catalog digest from two builds with same tools is equal and does not embed secret strings.
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: st, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}
	if containsSecret(cat.CatalogDigest(), "sk-secret") || containsSecret(cat.CatalogDigest(), "hunter2") {
		t.Fatal("digest embeds secrets")
	}
	for _, e := range cat.Entries() {
		if containsSecret(string(e.Parameters), "sk-secret") {
			t.Fatal("parameters embed secrets")
		}
	}
}

func TestBuildToolCatalog_BusinessImmediateRejected_PlatformControlAllowed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, kind := range []string{ToolKindTool, ToolKindWorkflow, ToolKindAgent, ToolKindA2A} {
		kind := kind
		_, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
			{Tool: &stubTool{name: "biz_" + kind, desc: "d", params: testParams()}, Exposure: ToolExposureImmediate, Kind: kind},
		})
		if !errors.Is(err, ErrToolCatalogBusinessImmediate) {
			t.Fatalf("kind %s immediate without platform: %v", kind, err)
		}
	}
	// Explicit platform control immediate PASS.
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: &stubTool{name: "plat", desc: "d", params: testParams()}, Exposure: ToolExposureImmediate, Kind: ToolKindTool, PlatformControl: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ent, ok := cat.Entry("plat")
	if !ok || !ent.PlatformControl || ent.Exposure != ToolExposureImmediate {
		t.Fatalf("platform entry = %+v ok=%v", ent, ok)
	}
	// Mutating returned entry PlatformControl must not flip frozen classification.
	ent.PlatformControl = false
	ent.Exposure = ToolExposureDeferred
	ent2, _ := cat.Entry("plat")
	if !ent2.PlatformControl || ent2.Exposure != ToolExposureImmediate {
		t.Fatal("mutation flipped frozen platform classification")
	}
}

func TestToolCatalogSnapshot_AccessorMutationIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &stubTool{name: "mut", desc: "original", params: testParams()}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: st, Exposure: ToolExposureDeferred},
		{Tool: &stubTool{name: "ctrl", desc: "c", params: testParams()}, Exposure: ToolExposureImmediate, PlatformControl: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	digestBefore := cat.CatalogDigest()
	namesBefore := cat.Names()

	// Mutate returned Entries slice, Parameters bytes, and fields.
	ents := cat.Entries()
	ents[0].Description = "HACKED"
	ents[0].Exposure = ToolExposureImmediate
	ents[0].PlatformControl = true
	ents[0].Name = "renamed"
	if ents[0].Parameters != nil {
		ents[0].Parameters[0] = 'X'
	}
	// Mutate returned Names.
	names := cat.Names()
	names[0] = "hacked-name"
	// Mutate ToolInfo copy.
	info, err := cat.ToolInfoCopy("mut")
	if err != nil {
		t.Fatal(err)
	}
	info.Desc = "hacked-info"
	info.Name = "nope"

	// Partition / search / digest must be unchanged.
	if cat.CatalogDigest() != digestBefore {
		t.Fatal("digest changed after accessor mutation")
	}
	gotNames := cat.Names()
	if len(gotNames) != len(namesBefore) || gotNames[0] != namesBefore[0] {
		t.Fatalf("names mutated: %v vs %v", gotNames, namesBefore)
	}
	ia, _ := st.Info(ctx)
	ic, _ := cat.ToolInfoCopy("ctrl")
	im, def, err := cat.PartitionToolInfos([]*schema.ToolInfo{ia, ic}, nil, ClientToolSearchToolName)
	if err != nil {
		t.Fatalf("partition after mutation: %v", err)
	}
	if len(im) != 1 || im[0].Name != "ctrl" || len(def) != 1 || def[0].Name != "mut" || def[0].Desc != "original" {
		t.Fatalf("partition polluted: imm=%+v def=%+v", im, def)
	}
	// Concurrent reads + mutations of returned copies must not panic or corrupt.
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := cat.Entries()
			e[0].Description = "x"
			if e[0].Parameters != nil {
				_ = append(e[0].Parameters, 'z')
			}
			_ = cat.Names()
			_ = cat.CatalogDigest()
			_, _, _ = cat.PartitionToolInfos([]*schema.ToolInfo{ia, ic}, nil, ClientToolSearchToolName)
			cp, _ := cat.ToolInfoCopy("mut")
			if cp != nil {
				cp.Desc = "y"
			}
		}()
	}
	wg.Wait()
	if cat.CatalogDigest() != digestBefore {
		t.Fatal("digest corrupted under concurrent accessor mutation")
	}
}

func containsSecret(s, secret string) bool {
	return len(secret) > 0 && (s == secret || len(s) > 0 && stringIndex(s, secret) >= 0)
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// schemaStubTool freezes a raw JSON Schema parameters surface (for annotation/digest tests).
type schemaStubTool struct {
	name   string
	desc   string
	schema string
}

func (t *schemaStubTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	// Lossless decode so enum/const integers (e.g. 2^53+1) are not float64-rounded
	// before the catalog freeze path — mirrors production raw-JSON tool builders.
	js, err := decodeJSONSchemaLossless([]byte(t.schema))
	if err != nil {
		return nil, err
	}
	return &schema.ToolInfo{
		Name:        t.name,
		Desc:        t.desc,
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(js),
	}, nil
}

func (t *schemaStubTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return `{"ok":true}`, nil
}

func TestBuildToolCatalog_AnnotationStripIdenticalDigestAndModelSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// A: semantic schema only. B: same semantic + strip-able annotation noise (not default).
	schemaA := `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`
	schemaB := `{"type":"object","properties":{"q":{"type":"string","examples":["a"],"x-vendor":true}},"required":["q"],"$comment":"noise"}`
	toolA := &schemaStubTool{name: "lookup", desc: "look up", schema: schemaA}
	toolB := &schemaStubTool{name: "lookup", desc: "look up", schema: schemaB}
	catA, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: toolA, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}
	catB, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: toolB, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}
	if catA.CatalogDigest() != catB.CatalogDigest() {
		t.Fatalf("annotation-only diff must not rotate catalog digest: %s vs %s", catA.CatalogDigest(), catB.CatalogDigest())
	}
	entA, _ := catA.Entry("lookup")
	entB, _ := catB.Entry("lookup")
	if entA.SchemaDigest != entB.SchemaDigest {
		t.Fatalf("schema digests diverge: %s vs %s", entA.SchemaDigest, entB.SchemaDigest)
	}
	if string(entA.Parameters) != string(entB.Parameters) {
		t.Fatalf("canonical parameters diverge:\n%s\n%s", entA.Parameters, entB.Parameters)
	}
	// Model-visible ToolInfo ParamsOneOf must re-emit the same canonical schema.
	infoA, err := catA.ToolInfoCopy("lookup")
	if err != nil {
		t.Fatal(err)
	}
	infoB, err := catB.ToolInfoCopy("lookup")
	if err != nil {
		t.Fatal(err)
	}
	jsA, err := infoA.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	jsB, err := infoB.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	rawA, _ := json.Marshal(jsA)
	rawB, _ := json.Marshal(jsB)
	// Re-canonicalize both wire-facing schemas and compare to frozen entry bytes.
	canonA, err := canonicalizeAndValidateParametersSchema(rawA)
	if err != nil {
		t.Fatal(err)
	}
	canonB, err := canonicalizeAndValidateParametersSchema(rawB)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonA) != string(entA.Parameters) || string(canonB) != string(entB.Parameters) {
		t.Fatalf("model-visible schema must match catalog Parameters:\nentry=%s\ncanonA=%s\ncanonB=%s", entA.Parameters, canonA, canonB)
	}
	if strings.Contains(string(canonA), "examples") || strings.Contains(string(canonA), "x-vendor") {
		t.Fatalf("annotations leaked into model-visible schema: %s", canonA)
	}
	// Mutation of original tool schema after build cannot change frozen catalog.
	toolB.schema = `{"type":"object","properties":{"q":{"type":"integer"}}}`
	entB2, _ := catB.Entry("lookup")
	if string(entB2.Parameters) != string(entB.Parameters) {
		t.Fatal("post-build mutation of tool source changed frozen parameters")
	}
	infoB2, _ := catB.ToolInfoCopy("lookup")
	jsB2, _ := infoB2.ParamsOneOf.ToJSONSchema()
	rawB2, _ := json.Marshal(jsB2)
	if strings.Contains(string(rawB2), `"integer"`) {
		t.Fatal("post-build mutation leaked into ToolInfoCopy")
	}
}

func TestBuildToolCatalog_RejectsDefaultKeyword(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schema := `{"type":"object","properties":{"q":{"type":"string","default":"TASK3_M_SECRET"}},"required":["q"]}`
	tool := &schemaStubTool{name: "lookup", desc: "look up", schema: schema}
	_, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: tool, Exposure: ToolExposureDeferred}})
	if err == nil || !errors.Is(err, ErrToolSchemaUnsupportedKeyword) {
		t.Fatalf("want catalog reject default, got %v", err)
	}
	if strings.Contains(err.Error(), "TASK3_M_SECRET") {
		t.Fatalf("error leaked default secret: %v", err)
	}
}

func TestBuildToolCatalog_SemanticDiffRotatesDigest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := &schemaStubTool{name: "lookup", desc: "d", schema: `{"type":"object","properties":{"q":{"type":"string"}}}`}
	b := &schemaStubTool{name: "lookup", desc: "d", schema: `{"type":"object","properties":{"q":{"type":"integer"}}}`}
	catA, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: a, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}
	catB, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: b, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}
	if catA.CatalogDigest() == catB.CatalogDigest() {
		t.Fatal("semantic schema diff must rotate catalog digest")
	}
	entA, _ := catA.Entry("lookup")
	entB, _ := catB.Entry("lookup")
	if entA.SchemaDigest == entB.SchemaDigest {
		t.Fatal("semantic schema diff must rotate schema digest")
	}
}
