package einoruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/metrics"
)

const (
	// PlatformCatalogSearchToolName is the ordinary function used for
	// platform-bounded catalog search. Collision with any catalog tool fails at build.
	PlatformCatalogSearchToolName = "actweave_catalog_search"

	// ToolSearchLoadCapCode is the stable JSON error code when a platform search
	// would exceed the run-local loaded-definition ceiling.
	ToolSearchLoadCapCode = "TOOL_SEARCH_LOAD_CAP"
)

// IsRedactedSearchToolName reports whether name is a platform-owned search
// function whose query and loadedNames must not enter public projection.
func IsRedactedSearchToolName(name string) bool {
	return isReservedSearchToolName(name)
}

// BoundedPlatformFunctionSearchMiddleware discloses tools via an ordinary
// function (actweave_catalog_search). It never sets ToolSearchTool or
// DeferredToolInfos — those surfaces leak catalog names before a search.
type BoundedPlatformFunctionSearchMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	catalog    *ToolCatalogSnapshot
	searchInfo *schema.ToolInfo
	executor   tool.BaseTool
}

// NewBoundedPlatformFunctionSearchMiddleware constructs the middleware over a
// frozen catalog. catalog may be empty but must be non-nil.
func NewBoundedPlatformFunctionSearchMiddleware(catalog *ToolCatalogSnapshot) (*BoundedPlatformFunctionSearchMiddleware, error) {
	if catalog == nil {
		return nil, fmt.Errorf("%w: catalog is required", ErrToolCatalogInvalid)
	}
	if catalog.hasName(PlatformCatalogSearchToolName) {
		return nil, fmt.Errorf("%w: %q", ErrToolCatalogSearchNameCollision, PlatformCatalogSearchToolName)
	}
	if catalog.hasName(ClientToolSearchToolName) {
		return nil, fmt.Errorf("%w: %q", ErrToolCatalogSearchNameCollision, ClientToolSearchToolName)
	}
	info := platformCatalogSearchToolInfo()
	exec := &boundedPlatformCatalogSearchExecutor{catalog: catalog, info: info}
	return &BoundedPlatformFunctionSearchMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{},
		catalog:                           catalog,
		searchInfo:                        info,
		executor:                          exec,
	}, nil
}

// SearchToolInfo returns a defensive copy of the ordinary search function contract.
func (m *BoundedPlatformFunctionSearchMiddleware) SearchToolInfo() *schema.ToolInfo {
	if m == nil {
		return nil
	}
	cp, err := deepCopyToolInfo(m.searchInfo)
	if err != nil {
		return platformCatalogSearchToolInfo()
	}
	return cp
}

// Executor returns the ordinary search function for ToolsNode registration.
func (m *BoundedPlatformFunctionSearchMiddleware) Executor() tool.BaseTool {
	if m == nil {
		return nil
	}
	return m.executor
}

// BeforeAgent appends the search executor to Tools and clears ToolSearchTool.
func (m *BoundedPlatformFunctionSearchMiddleware) BeforeAgent(
	ctx context.Context,
	runCtx *adk.ChatModelAgentContext,
) (context.Context, *adk.ChatModelAgentContext, error) {
	if runCtx == nil {
		return ctx, runCtx, nil
	}
	if err := m.validateSessionLoadedState(ctx); err != nil {
		return ctx, runCtx, err
	}
	n := *runCtx
	n.Tools = make([]tool.BaseTool, len(runCtx.Tools), len(runCtx.Tools)+1)
	copy(n.Tools, runCtx.Tools)
	n.Tools = append(n.Tools, m.executor)
	if runCtx.ReturnDirectly != nil {
		n.ReturnDirectly = make(map[string]bool, len(runCtx.ReturnDirectly))
		for k, v := range runCtx.ReturnDirectly {
			n.ReturnDirectly[k] = v
		}
	}
	n.ToolSearchTool = nil
	return ctx, &n, nil
}

// BeforeModelRewriteState puts only the search function, immediate
// platform-control tools, and already-loaded deferred tools into ToolInfos.
// Unloaded deferred names never enter ToolInfos or DeferredToolInfos.
func (m *BoundedPlatformFunctionSearchMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.TypedChatModelAgentState[*schema.AgenticMessage],
	_ *adk.TypedModelContext[*schema.AgenticMessage],
) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	if state == nil {
		return ctx, state, fmt.Errorf("%w: nil agent state", ErrModelToolCatalogMismatch)
	}
	if err := m.validateSessionLoadedState(ctx); err != nil {
		return ctx, state, err
	}
	if err := rejectUnknownDisclosureNames(m.catalog, state.ToolInfos, state.DeferredToolInfos, PlatformCatalogSearchToolName); err != nil {
		return ctx, state, err
	}
	if err := rejectMalformedSearchInfo(state.ToolInfos, state.DeferredToolInfos, PlatformCatalogSearchToolName, validatePlatformCatalogSearchInfo); err != nil {
		return ctx, state, err
	}
	loaded, err := loadedDeferredToolNamesFromSession(ctx, MaxLoadedToolsPerSearch)
	if err != nil {
		return ctx, state, err
	}
	visible, err := platformVisibleToolInfos(m.catalog, m.searchInfo, loaded)
	if err != nil {
		return ctx, state, err
	}
	state.ToolInfos = visible
	state.DeferredToolInfos = nil
	return ctx, state, nil
}

func (m *BoundedPlatformFunctionSearchMiddleware) validateSessionLoadedState(ctx context.Context) error {
	if m == nil {
		return nil
	}
	names, err := loadedDeferredToolNamesFromSession(ctx, MaxLoadedToolsPerSearch)
	if err != nil {
		return err
	}
	return validateLoadedNamesAgainstCatalog(m.catalog, names)
}

// PlatformCatalogSearchEstimate returns the search function identity used for
// token accounting. Name, description, and parameters match the wire contract.
func PlatformCatalogSearchEstimate() (name, desc string, parameters json.RawMessage) {
	info := platformCatalogSearchToolInfo()
	params, err := canonicalParametersJSON(info)
	if err != nil {
		params = json.RawMessage(`{"type":"object"}`)
	}
	return info.Name, info.Desc, params
}

func platformCatalogSearchToolInfo() *schema.ToolInfo {
	return &schema.ToolInfo{
		Name: PlatformCatalogSearchToolName,
		Desc: "Search the authorized tool catalog for tools matching a query. " +
			"Use keywords to search, or \"select:name1,name2\" for direct selection. " +
			"At most 5 tools are returned per search. At most 5 distinct business tools can be loaded in this run.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": {
				Type:     schema.String,
				Desc:     "Query to find deferred tools. Use \"select:<tool_name>[,...]\" for direct selection, or keywords to search name and description.",
				Required: true,
			},
			"max_results": {
				Type:     schema.Integer,
				Desc:     "Maximum number of results to return (default and hard ceiling: 5). Values above 5 are clamped; zero/negative are rejected.",
				Required: false,
			},
		}),
	}
}

func validatePlatformCatalogSearchInfo(info *schema.ToolInfo) error {
	if info == nil {
		return fmt.Errorf("nil ToolInfo")
	}
	expected := platformCatalogSearchToolInfo()
	name, err := requireCanonicalToolName(info.Name)
	if err != nil {
		return err
	}
	if name != PlatformCatalogSearchToolName {
		return fmt.Errorf("name %q != %q", name, PlatformCatalogSearchToolName)
	}
	liveDesc := strings.TrimSpace(info.Desc)
	expDesc := strings.TrimSpace(expected.Desc)
	if liveDesc == "" {
		return fmt.Errorf("empty description")
	}
	if liveDesc != expDesc {
		return fmt.Errorf("description does not match platform catalog search contract")
	}
	if len(info.Extra) > 0 {
		return fmt.Errorf("unexpected Extra metadata on platform catalog search")
	}
	liveParams, err := canonicalParametersJSON(info)
	if err != nil {
		return fmt.Errorf("parameters: %v", err)
	}
	expParams, err := canonicalParametersJSON(expected)
	if err != nil {
		return fmt.Errorf("expected parameters: %v", err)
	}
	live := ToolCatalogEntry{Name: name, Description: liveDesc, Parameters: liveParams}
	exp := ToolCatalogEntry{Name: PlatformCatalogSearchToolName, Description: expDesc, Parameters: expParams}
	if digestToolSchema(live) != digestToolSchema(exp) {
		return fmt.Errorf("parameter schema does not match platform catalog search")
	}
	return nil
}

// boundedPlatformCatalogSearchExecutor is an ordinary InvokableTool. Results
// are FunctionToolResult JSON, never ToolSearchResult parts.
type boundedPlatformCatalogSearchExecutor struct {
	catalog *ToolCatalogSnapshot
	info    *schema.ToolInfo
}

func (t *boundedPlatformCatalogSearchExecutor) Info(_ context.Context) (*schema.ToolInfo, error) {
	return deepCopyToolInfo(t.info)
}

func (t *boundedPlatformCatalogSearchExecutor) InvokableRun(
	ctx context.Context,
	argumentsInJSON string,
	_ ...tool.Option,
) (string, error) {
	already, err := loadedDeferredToolNamesFromSession(ctx, MaxLoadedToolsPerSearch)
	if err != nil {
		observePlatformSearch(ctx, metrics.DisclosureOutcomeError)
		return "", err
	}
	if err := validateLoadedNamesAgainstCatalog(t.catalog, already); err != nil {
		observePlatformSearch(ctx, metrics.DisclosureOutcomeError)
		return "", err
	}
	// At-cap must not select new names — return stable JSON before the helper.
	if len(already) >= MaxLoadedToolsPerSearch {
		observePlatformSearch(ctx, metrics.DisclosureOutcomeLoadCap)
		return platformSearchLoadCapJSON(), nil
	}
	matches, newlyLoaded, err := executeBoundedToolSearch(t.catalog, argumentsInJSON, already, MaxLoadedToolsPerSearch)
	if err != nil {
		if errIsToolSearchLoadCap(err) {
			observePlatformSearch(ctx, metrics.DisclosureOutcomeLoadCap)
			return platformSearchLoadCapJSON(), nil
		}
		observePlatformSearch(ctx, metrics.DisclosureOutcomeError)
		return "", err
	}
	if len(newlyLoaded) > 0 {
		merged := mergeLoadedDeferredToolNames(already, newlyLoaded)
		if len(merged) > MaxLoadedToolsPerSearch {
			observePlatformSearch(ctx, metrics.DisclosureOutcomeLoadCap)
			return platformSearchLoadCapJSON(), nil
		}
		storeLoadedDeferredToolNames(ctx, merged)
	}
	names := make([]string, 0, len(matches))
	for _, info := range matches {
		if info == nil || info.Name == "" {
			continue
		}
		names = append(names, info.Name)
	}
	sort.Strings(names)
	if names == nil {
		names = []string{}
	}
	payload, err := json.Marshal(platformCatalogSearchOK{
		OK:          true,
		Count:       len(names),
		LoadedNames: names,
	})
	if err != nil {
		observePlatformSearch(ctx, metrics.DisclosureOutcomeError)
		return "", fmt.Errorf("%w: marshal search result: %v", ErrToolSearchInvalidArgs, err)
	}
	observePlatformSearch(ctx, metrics.DisclosureOutcomeOK)
	observePlatformSearchLoaded(ctx, len(names))
	return string(payload), nil
}

func observePlatformSearch(ctx context.Context, outcome string) {
	if isVerificationProbe(ctx) {
		return
	}
	metrics.Disclosure().ObserveSearchCall(metrics.DisclosureModePlatformBounded, outcome)
	if outcome == metrics.DisclosureOutcomeLoadCap {
		metrics.Disclosure().ObserveRejected(metrics.DisclosureCodeSearchLoadCap)
	}
}

func observePlatformSearchLoaded(ctx context.Context, loaded int) {
	if isVerificationProbe(ctx) {
		return
	}
	metrics.Disclosure().ObserveSearchLoaded(metrics.DisclosureModePlatformBounded, loaded)
}

func errIsToolSearchLoadCap(err error) bool {
	return errors.Is(err, ErrToolSearchLoadCapExceeded)
}

type platformCatalogSearchOK struct {
	OK          bool     `json:"ok"`
	Count       int      `json:"count"`
	LoadedNames []string `json:"loadedNames"`
}

type platformCatalogSearchErr struct {
	OK   bool   `json:"ok"`
	Code string `json:"code"`
}

func platformSearchLoadCapJSON() string {
	b, err := json.Marshal(platformCatalogSearchErr{OK: false, Code: ToolSearchLoadCapCode})
	if err != nil {
		return `{"ok":false,"code":"TOOL_SEARCH_LOAD_CAP"}`
	}
	return string(b)
}

func platformVisibleToolInfos(catalog *ToolCatalogSnapshot, searchInfo *schema.ToolInfo, loaded []string) ([]*schema.ToolInfo, error) {
	if catalog == nil {
		return nil, fmt.Errorf("%w: nil catalog", ErrToolCatalogInvalid)
	}
	search, err := deepCopyToolInfo(searchInfo)
	if err != nil {
		return nil, err
	}
	var extra []string
	for _, e := range catalog.entries {
		if e.Exposure == ToolExposureImmediate {
			extra = append(extra, e.Name)
		}
	}
	seen := make(map[string]struct{}, len(extra)+len(loaded))
	for _, n := range extra {
		seen[n] = struct{}{}
	}
	for _, n := range loaded {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		extra = append(extra, n)
	}
	sort.Strings(extra)
	out := make([]*schema.ToolInfo, 0, 1+len(extra))
	out = append(out, search)
	for _, n := range extra {
		cp, err := catalog.ToolInfoCopy(n)
		if err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	return out, nil
}

func rejectUnknownDisclosureNames(
	catalog *ToolCatalogSnapshot,
	toolInfos, deferred []*schema.ToolInfo,
	allowedSearch string,
) error {
	if catalog == nil {
		return fmt.Errorf("%w: nil catalog", ErrToolCatalogInvalid)
	}
	scan := func(infos []*schema.ToolInfo, side string) error {
		for _, info := range infos {
			if info == nil {
				return fmt.Errorf("%w: nil ToolInfo in %s", ErrModelToolCatalogMismatch, side)
			}
			name, err := requireCanonicalToolName(info.Name)
			if err != nil {
				return fmt.Errorf("%w: in %s: %w", ErrModelToolCatalogMismatch, side, err)
			}
			if allowedSearch != "" && name == allowedSearch {
				continue
			}
			if !catalog.hasName(name) {
				return fmt.Errorf("%w: %q", ErrToolCatalogUnknownTool, name)
			}
		}
		return nil
	}
	if err := scan(toolInfos, "ToolInfos"); err != nil {
		return err
	}
	return scan(deferred, "DeferredToolInfos")
}

func rejectMalformedSearchInfo(
	toolInfos, deferred []*schema.ToolInfo,
	searchName string,
	validate func(*schema.ToolInfo) error,
) error {
	if searchName == "" || validate == nil {
		return nil
	}
	seen := 0
	check := func(infos []*schema.ToolInfo, side string) error {
		for _, info := range infos {
			if info == nil {
				continue
			}
			name := strings.TrimSpace(info.Name)
			if name != searchName {
				continue
			}
			if err := validate(info); err != nil {
				return fmt.Errorf("%w: in %s: %v", ErrToolCatalogSearchExecutorInvalid, side, err)
			}
			seen++
			if seen > 1 {
				return fmt.Errorf("%w: %q", ErrToolCatalogDuplicateInPartition, searchName)
			}
		}
		return nil
	}
	if err := check(toolInfos, "ToolInfos"); err != nil {
		return err
	}
	return check(deferred, "DeferredToolInfos")
}
