package einoruntime

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// CarryAllHardLimit is the maximum number of non-platform-control catalog
// tools that may be disclosed with full schema in carry-all mode.
const CarryAllHardLimit = MaxImmediatePlatformTools

// CarryAllToolDisclosureMiddleware puts every catalog business tool (full
// schema, name ascending) plus immediate platform-control tools into ToolInfos.
// Catalog rows stay deferred; this is a disclosure overlay only.
type CarryAllToolDisclosureMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	catalog *ToolCatalogSnapshot
}

// NewCarryAllToolDisclosureMiddleware constructs the overlay. catalog may be
// empty but must be non-nil. businessToolCount > CarryAllHardLimit is refused.
func NewCarryAllToolDisclosureMiddleware(catalog *ToolCatalogSnapshot) (*CarryAllToolDisclosureMiddleware, error) {
	if catalog == nil {
		return nil, fmt.Errorf("%w: catalog is required", ErrToolCatalogInvalid)
	}
	if n := businessToolCount(catalog); n > CarryAllHardLimit {
		return nil, fmt.Errorf("%w: %d > %d", ErrAgenticCarryAllTooLarge, n, CarryAllHardLimit)
	}
	if catalog.hasName(PlatformCatalogSearchToolName) {
		return nil, fmt.Errorf("%w: %q", ErrToolCatalogSearchNameCollision, PlatformCatalogSearchToolName)
	}
	if catalog.hasName(ClientToolSearchToolName) {
		return nil, fmt.Errorf("%w: %q", ErrToolCatalogSearchNameCollision, ClientToolSearchToolName)
	}
	return &CarryAllToolDisclosureMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{},
		catalog:                           catalog,
	}, nil
}

// BeforeAgent never registers actweave_catalog_search and clears ToolSearchTool.
func (m *CarryAllToolDisclosureMiddleware) BeforeAgent(
	ctx context.Context,
	runCtx *adk.ChatModelAgentContext,
) (context.Context, *adk.ChatModelAgentContext, error) {
	if runCtx == nil {
		return ctx, runCtx, nil
	}
	n := *runCtx
	if runCtx.Tools != nil {
		n.Tools = make([]tool.BaseTool, len(runCtx.Tools))
		copy(n.Tools, runCtx.Tools)
	}
	if runCtx.ReturnDirectly != nil {
		n.ReturnDirectly = make(map[string]bool, len(runCtx.ReturnDirectly))
		for k, v := range runCtx.ReturnDirectly {
			n.ReturnDirectly[k] = v
		}
	}
	n.ToolSearchTool = nil
	return ctx, &n, nil
}

// BeforeModelRewriteState discloses every catalog tool with full schema.
// DeferredToolInfos and ToolSearchTool stay nil.
func (m *CarryAllToolDisclosureMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.TypedChatModelAgentState[*schema.AgenticMessage],
	_ *adk.TypedModelContext[*schema.AgenticMessage],
) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	if state == nil {
		return ctx, state, fmt.Errorf("%w: nil agent state", ErrModelToolCatalogMismatch)
	}
	if m == nil || m.catalog == nil {
		return ctx, state, fmt.Errorf("%w: nil catalog", ErrToolCatalogInvalid)
	}
	if n := businessToolCount(m.catalog); n > CarryAllHardLimit {
		return ctx, state, fmt.Errorf("%w: %d > %d", ErrAgenticCarryAllTooLarge, n, CarryAllHardLimit)
	}
	if err := rejectUnknownDisclosureNames(m.catalog, state.ToolInfos, state.DeferredToolInfos, ""); err != nil {
		return ctx, state, err
	}
	infos, err := catalogFullToolInfos(m.catalog)
	if err != nil {
		return ctx, state, err
	}
	state.ToolInfos = infos
	state.DeferredToolInfos = nil
	return ctx, state, nil
}

func businessToolCount(catalog *ToolCatalogSnapshot) int {
	if catalog == nil {
		return 0
	}
	n := 0
	for _, e := range catalog.entries {
		if !e.PlatformControl {
			n++
		}
	}
	return n
}

func catalogFullToolInfos(catalog *ToolCatalogSnapshot) ([]*schema.ToolInfo, error) {
	if catalog == nil {
		return nil, fmt.Errorf("%w: nil catalog", ErrToolCatalogInvalid)
	}
	names := catalog.Names()
	out := make([]*schema.ToolInfo, 0, len(names))
	for _, n := range names {
		cp, err := catalog.ToolInfoCopy(n)
		if err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	return out, nil
}
