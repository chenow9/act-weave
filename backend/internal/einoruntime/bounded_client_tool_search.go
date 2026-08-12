package einoruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// Client tool-search hard bounds (D3).
const (
	// ClientToolSearchToolName is the stable native tool-search function name.
	// Collision with any business/platform catalog tool fails at build.
	ClientToolSearchToolName = "tool_search"

	// MaxLoadedToolsPerSearch is the hard ceiling of definitions returned per search.
	MaxLoadedToolsPerSearch = 5

	// MaxToolSearchCallsPerRun is implied by MaxIterations=8 with ParallelToolCalls=false.
	MaxToolSearchCallsPerRun = DefaultMaxIterations

	// MaxLoadedDefinitionsPerRun is the structural worst-case load ceiling (8×5).
	MaxLoadedDefinitionsPerRun = MaxToolSearchCallsPerRun * MaxLoadedToolsPerSearch
)

// Tool-search executor argument/result errors.
var (
	// ErrToolSearchInvalidArgs is returned for malformed / rejected search arguments.
	ErrToolSearchInvalidArgs = errors.New("einoruntime tool search invalid arguments")
	// ErrToolSearchNotDeferred is returned when direct select names an immediate
	// (or otherwise non-deferred) catalog tool, or the native search executor.
	// Immediate/platform-control tools and the executor itself are never
	// disclosable via native tool_search.
	ErrToolSearchNotDeferred = errors.New("einoruntime tool search: tool is not deferred / not disclosable")
	// ErrToolSearchLoadCapExceeded is returned when a search would load definitions
	// that push the run-local cumulative loaded-deferred count above
	// MaxLoadedDefinitionsPerRun (40).
	ErrToolSearchLoadCapExceeded = errors.New("einoruntime tool search: cumulative loaded definitions cap exceeded")
	// ErrToolSearchAlreadyLoaded is returned when a direct select names only tools
	// that were already loaded in this run (stable typed omit/reject semantics).
	ErrToolSearchAlreadyLoaded = errors.New("einoruntime tool search: selected tools already loaded")
	// ErrToolSearchLoadedStateInvalid is returned when checkpoint/session loaded-set
	// state is present but corrupt (wrong type, nil elements, non-string, empty/
	// noncanonical names, duplicates, >40). Only truly absent state means empty.
	// Fail closed — never silently skip/reset.
	ErrToolSearchLoadedStateInvalid = errors.New("einoruntime tool search: loaded-state invalid")
)

// Session key for the run-local loaded deferred tool name list. Stored under ADK
// SessionValues and gob-persisted with the Task2 checkpoint (runSession.Values).
// Concurrent runs isolate via distinct sessions; native/immediate never enter.
const sessionKeyLoadedDeferredToolNames = "actweave.agentic.loaded_deferred_tool_names.v1"

// BoundedClientToolSearchMiddleware implements
// adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage] for production
// client-executed OpenAI native tool search with a hard max of 5 loaded
// definitions per search (D3).
//
// It does NOT use Eino stock dynamic tool-search middleware (which injects all
// tool names and whose select: path bypasses max_results).
//
// Concurrency: a frozen middleware/catalog is safe for concurrent runs; neither
// the catalog nor this middleware mutates shared state during a search.
type BoundedClientToolSearchMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	catalog    *ToolCatalogSnapshot
	searchInfo *schema.ToolInfo
	executor   tool.BaseTool
}

// NewBoundedClientToolSearchMiddleware constructs the middleware over a frozen catalog.
// catalog may be empty (no deferred tools) but must be non-nil.
func NewBoundedClientToolSearchMiddleware(catalog *ToolCatalogSnapshot) (*BoundedClientToolSearchMiddleware, error) {
	if catalog == nil {
		return nil, fmt.Errorf("%w: catalog is required", ErrToolCatalogInvalid)
	}
	// Reject catalog collision with the search tool name (defense in depth).
	if catalog.hasName(ClientToolSearchToolName) {
		return nil, fmt.Errorf("%w: %q", ErrToolCatalogSearchNameCollision, ClientToolSearchToolName)
	}
	info := clientToolSearchToolInfo()
	exec := &boundedClientToolSearchExecutor{catalog: catalog, info: info}
	return &BoundedClientToolSearchMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{},
		catalog:                           catalog,
		searchInfo:                        info,
		executor:                          exec,
	}, nil
}

// SearchToolInfo returns the strict ToolInfo contract used for both ToolsNode
// execution and runCtx.ToolSearchTool (execution:"client" on the wire).
func (m *BoundedClientToolSearchMiddleware) SearchToolInfo() *schema.ToolInfo {
	if m == nil {
		return nil
	}
	// Defensive copy so callers cannot mutate the middleware's contract.
	cp, err := deepCopyToolInfo(m.searchInfo)
	if err != nil {
		// Fall back to a fresh construction if copy fails.
		return clientToolSearchToolInfo()
	}
	return cp
}

// Executor returns the enhanced local search tool for ToolsNode registration.
func (m *BoundedClientToolSearchMiddleware) Executor() tool.BaseTool {
	if m == nil {
		return nil
	}
	return m.executor
}

// BeforeAgent never mutates the supplied run context; it returns a copy with:
//   - the enhanced local search executor appended to Tools (ToolsNode only)
//   - ToolSearchTool set to the exact same strict ToolInfo contract
//
// It does not inject reminders or full tool-name lists into messages.
// Register this handler last among tool-search configuration handlers so it is
// the final authority for ToolSearchTool.
//
// When session values are already present (fresh Run with WithSessionValues, or
// Resume after checkpoint restore), absent-or-strict loaded-state is validated
// against the frozen catalog here so corrupt state cannot wait for a tool_search
// call. Session may still be empty at this hook on some ADK paths; the
// BeforeModelRewriteState pre-model boundary always re-validates.
func (m *BoundedClientToolSearchMiddleware) BeforeAgent(
	ctx context.Context,
	runCtx *adk.ChatModelAgentContext,
) (context.Context, *adk.ChatModelAgentContext, error) {
	if runCtx == nil {
		return ctx, runCtx, nil
	}
	// Fail closed on present-but-invalid loaded-state before graph build / model.
	// Truly absent session key is fine (empty loaded set).
	if err := m.validateSessionLoadedState(ctx); err != nil {
		return ctx, runCtx, err
	}
	n := *runCtx
	// Copy Tools slice; do not mutate caller's backing array.
	n.Tools = make([]tool.BaseTool, len(runCtx.Tools), len(runCtx.Tools)+1)
	copy(n.Tools, runCtx.Tools)
	n.Tools = append(n.Tools, m.executor)
	// Copy ReturnDirectly map if present so later handlers cannot race the original.
	if runCtx.ReturnDirectly != nil {
		n.ReturnDirectly = make(map[string]bool, len(runCtx.ReturnDirectly))
		for k, v := range runCtx.ReturnDirectly {
			n.ReturnDirectly[k] = v
		}
	}
	// Exact same ToolInfo contract as the executor (fresh copy for safety).
	n.ToolSearchTool = m.SearchToolInfo()
	return ctx, &n, nil
}

// BeforeModelRewriteState reconstructs the full catalog from
// state.ToolInfos and state.DeferredToolInfos with partition-aware validation
// against the frozen snapshot, then partitions/sorts by catalog exposure.
//
// Immediate tools → ToolInfos only; deferred → DeferredToolInfos only.
// The local search executor's ToolInfo is absent from both.
// Idempotent after Eino persists rewritten state (one identical cross-side
// same-name occurrence is the resume collapse case; same-side duplicates fail).
//
// Pre-model loaded-state gate: every model invocation (Run and Resume) validates
// absent-or-strict loaded deferred names against the frozen catalog BEFORE the
// model is called. Wrong/unknown/immediate/native/corrupt state returns
// ErrToolSearchLoadedStateInvalid with zero model progress — models cannot
// bypass via a direct final answer without tool_search.
func (m *BoundedClientToolSearchMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.TypedChatModelAgentState[*schema.AgenticMessage],
	_ *adk.TypedModelContext[*schema.AgenticMessage],
) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	if state == nil {
		return ctx, state, fmt.Errorf("%w: nil agent state", ErrModelToolCatalogMismatch)
	}
	// Loaded-state first: fail before partition rewrite / model invocation.
	// Does not mutate session values (read-only validation; no double store).
	if err := m.validateSessionLoadedState(ctx); err != nil {
		return ctx, state, err
	}
	// No prompt/message injection — leave state.Messages untouched.
	immediate, deferred, err := m.catalog.PartitionToolInfos(
		state.ToolInfos,
		state.DeferredToolInfos,
		ClientToolSearchToolName,
	)
	if err != nil {
		return ctx, state, err
	}
	state.ToolInfos = immediate
	state.DeferredToolInfos = deferred
	return ctx, state, nil
}

// validateSessionLoadedState is the production pre-model/pre-search gate for
// run-local loaded deferred tool names. Truly absent session key → ok (empty).
// Present-but-corrupt or catalog-mismatched → ErrToolSearchLoadedStateInvalid.
// Read-only: never writes or clears session state.
func (m *BoundedClientToolSearchMiddleware) validateSessionLoadedState(ctx context.Context) error {
	if m == nil {
		return nil
	}
	names, err := loadedDeferredToolNamesFromSession(ctx)
	if err != nil {
		return err
	}
	return validateLoadedNamesAgainstCatalog(m.catalog, names)
}

// --- search tool info & executor ---------------------------------------------

func clientToolSearchToolInfo() *schema.ToolInfo {
	return &schema.ToolInfo{
		Name: ClientToolSearchToolName,
		Desc: "Search the authorized tool catalog for tools matching a query. " +
			"Use keywords to search, or \"select:name1,name2\" for direct selection. " +
			"At most 5 tools are returned per search.",
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

// boundedClientToolSearchExecutor is an EnhancedInvokableTool that searches the
// frozen catalog and returns schema.ToolResult with exactly one
// ToolPartTypeToolSearchResult part.
type boundedClientToolSearchExecutor struct {
	catalog *ToolCatalogSnapshot
	info    *schema.ToolInfo
}

func (t *boundedClientToolSearchExecutor) Info(_ context.Context) (*schema.ToolInfo, error) {
	return deepCopyToolInfo(t.info)
}

func (t *boundedClientToolSearchExecutor) InvokableRun(
	ctx context.Context,
	toolArgument *schema.ToolArgument,
	_ ...tool.Option,
) (*schema.ToolResult, error) {
	if toolArgument == nil {
		return nil, fmt.Errorf("%w: nil tool argument", ErrToolSearchInvalidArgs)
	}
	// Run-local loaded-name set (checkpointed via ADK SessionValues).
	// Corrupt/malformed state fails closed with a stable typed error (no silent reset).
	// After structural decode, every name must exist in the frozen catalog as a
	// deferred business definition (reject unknown / immediate / platform-control /
	// native tool_search). Applies on normal run and checkpoint resume alike.
	already, err := loadedDeferredToolNamesFromSession(ctx)
	if err != nil {
		// Propagate stable catalog/search state error; no provider/body disclosure.
		return nil, err
	}
	if err := validateLoadedNamesAgainstCatalog(t.catalog, already); err != nil {
		return nil, err
	}
	matches, newlyLoaded, err := executeBoundedToolSearch(t.catalog, toolArgument.Text, already)
	if err != nil {
		return nil, err
	}
	// Atomically update the run-local set before returning output.
	if len(newlyLoaded) > 0 {
		merged := mergeLoadedDeferredToolNames(already, newlyLoaded)
		if len(merged) > MaxLoadedDefinitionsPerRun {
			return nil, fmt.Errorf("%w: cumulative %d > %d", ErrToolSearchLoadCapExceeded, len(merged), MaxLoadedDefinitionsPerRun)
		}
		storeLoadedDeferredToolNames(ctx, merged)
	}
	return &schema.ToolResult{
		Parts: []schema.ToolOutputPart{{
			Type: schema.ToolPartTypeToolSearchResult,
			ToolSearchResult: &schema.ToolSearchResult{
				Tools: matches,
			},
		}},
	}, nil
}

// executeBoundedToolSearch parses strict args, searches/selects, and returns
// at most MaxLoadedToolsPerSearch defensive ToolInfo copies, canonical-name sorted.
// alreadyLoaded filters/omits names previously loaded in this run. newlyLoaded
// is the subset of returned names not already in alreadyLoaded (for atomic update).
// When alreadyLoaded is nil (no session / unit tests), no uniqueness filter applies.
func executeBoundedToolSearch(catalog *ToolCatalogSnapshot, argumentsJSON string, alreadyLoaded []string) ([]*schema.ToolInfo, []string, error) {
	if catalog == nil {
		return nil, nil, fmt.Errorf("%w: nil catalog", ErrToolCatalogInvalid)
	}
	args, err := parseStrictToolSearchArgs(argumentsJSON)
	if err != nil {
		return nil, nil, err
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return nil, nil, fmt.Errorf("%w: query is required", ErrToolSearchInvalidArgs)
	}

	// Effective max: min(requested, 5); omitted → 5. Zero/negative rejected.
	maxResults := MaxLoadedToolsPerSearch
	if args.MaxResults != nil {
		if *args.MaxResults <= 0 {
			return nil, nil, fmt.Errorf("%w: max_results must be a positive integer", ErrToolSearchInvalidArgs)
		}
		if *args.MaxResults < maxResults {
			maxResults = *args.MaxResults
		}
		// values > 5 are clamped (never honored above the hard ceiling)
	}

	loadedSet := make(map[string]struct{}, len(alreadyLoaded))
	for _, n := range alreadyLoaded {
		n = strings.TrimSpace(n)
		if n != "" {
			loadedSet[n] = struct{}{}
		}
	}

	var selected []string
	isSelect := strings.HasPrefix(query, "select:")
	if isSelect {
		selected, err = parseSelectNames(strings.TrimPrefix(query, "select:"))
		if err != nil {
			return nil, nil, err
		}
		// Direct selection: only deferred catalog tools are disclosable.
		// Validate ALL names before filtering/capping so unauthorized / immediate /
		// executor selections never silently skip. Unknown → ErrToolCatalogUnknownTool;
		// immediate/executor → ErrToolSearchNotDeferred.
		for _, name := range selected {
			if err := requireDeferredDisclosable(catalog, name); err != nil {
				return nil, nil, err
			}
		}
		// Omit already-loaded direct selections (stable typed semantics).
		if len(loadedSet) > 0 {
			filtered := make([]string, 0, len(selected))
			sawNew := false
			for _, name := range selected {
				if _, loaded := loadedSet[name]; loaded {
					continue
				}
				sawNew = true
				filtered = append(filtered, name)
			}
			if !sawNew && len(selected) > 0 {
				return nil, nil, fmt.Errorf("%w: all selected tools already loaded", ErrToolSearchAlreadyLoaded)
			}
			selected = filtered
		}
		if len(selected) > maxResults {
			selected = selected[:maxResults]
		}
	} else {
		// Keyword: over-fetch then filter already-loaded so we still fill maxResults.
		candidates := keywordSearchCatalog(query, maxResults+len(loadedSet), catalog)
		selected = make([]string, 0, maxResults)
		for _, name := range candidates {
			if _, loaded := loadedSet[name]; loaded {
				continue
			}
			selected = append(selected, name)
			if len(selected) >= maxResults {
				break
			}
		}
	}

	// Cumulative hard cap: refuse to return definitions that would exceed 40.
	if len(loadedSet) > 0 {
		room := MaxLoadedDefinitionsPerRun - len(loadedSet)
		if room <= 0 {
			return nil, nil, fmt.Errorf("%w: already at cap %d", ErrToolSearchLoadCapExceeded, MaxLoadedDefinitionsPerRun)
		}
		if len(selected) > room {
			if isSelect {
				return nil, nil, fmt.Errorf("%w: select would exceed cap", ErrToolSearchLoadCapExceeded)
			}
			selected = selected[:room]
		}
	}

	// Build defensive copies; sort by canonical name; never include executor.
	infos, err := catalog.LookupInfos(selected)
	if err != nil {
		return nil, nil, err
	}
	if len(infos) > MaxLoadedToolsPerSearch {
		infos = infos[:MaxLoadedToolsPerSearch]
	}
	// Verify every result still matches its frozen digest.
	newlyLoaded := make([]string, 0, len(infos))
	for _, info := range infos {
		if info == nil {
			return nil, nil, fmt.Errorf("%w: nil result ToolInfo", ErrModelToolCatalogMismatch)
		}
		// Native/immediate never enter the loaded set (defense: only deferred).
		if info.Name == ClientToolSearchToolName {
			return nil, nil, fmt.Errorf("%w: search executor must not load", ErrToolSearchNotDeferred)
		}
		paramsRaw, err := canonicalParametersJSON(info)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: result %q: %v", ErrToolCatalogSchemaMismatch, info.Name, err)
		}
		live := ToolCatalogEntry{Name: info.Name, Description: info.Desc, Parameters: paramsRaw}
		want, ok := catalog.SchemaDigest(info.Name)
		if !ok || digestToolSchema(live) != want {
			return nil, nil, fmt.Errorf("%w: result %q", ErrToolCatalogSchemaMismatch, info.Name)
		}
		if _, already := loadedSet[info.Name]; !already {
			newlyLoaded = append(newlyLoaded, info.Name)
		}
	}
	return infos, newlyLoaded, nil
}

// loadedDeferredToolNamesFromSession returns the run-local loaded name list.
// Truly absent session state means empty (nil, nil). Present-but-corrupt state
// returns ErrToolSearchLoadedStateInvalid (fail closed; never silent skip/reset).
func loadedDeferredToolNamesFromSession(ctx context.Context) ([]string, error) {
	if ctx == nil {
		return nil, nil
	}
	v, ok := adk.GetSessionValue(ctx, sessionKeyLoadedDeferredToolNames)
	if !ok {
		// Truly absent: empty loaded set.
		return nil, nil
	}
	// Key present: decode strictly. nil value is corrupt (not "absent").
	return decodeLoadedDeferredToolNames(v)
}

// decodeLoadedDeferredToolNames is the strict loaded-state decoder.
// Rejects wrong type, nil elements, non-string, noncanonical/empty names,
// duplicates, >40 entries, and other malformed checkpoint payloads.
func decodeLoadedDeferredToolNames(v any) ([]string, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: nil loaded-state value", ErrToolSearchLoadedStateInvalid)
	}
	var raw []any
	switch typed := v.(type) {
	case []string:
		// Fast path: validate string slice strictly.
		return validateLoadedNameList(typed)
	case []any:
		raw = typed
	default:
		return nil, fmt.Errorf("%w: wrong loaded-state type %T", ErrToolSearchLoadedStateInvalid, v)
	}
	if len(raw) > MaxLoadedDefinitionsPerRun {
		return nil, fmt.Errorf("%w: loaded-state length %d > %d", ErrToolSearchLoadedStateInvalid, len(raw), MaxLoadedDefinitionsPerRun)
	}
	out := make([]string, 0, len(raw))
	for i, item := range raw {
		if item == nil {
			return nil, fmt.Errorf("%w: nil element at %d", ErrToolSearchLoadedStateInvalid, i)
		}
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%w: non-string element at %d (%T)", ErrToolSearchLoadedStateInvalid, i, item)
		}
		out = append(out, s)
	}
	return validateLoadedNameList(out)
}

// validateLoadedNameList enforces canonical name rules on a decoded string list:
// nonempty, trimmed-equal (no surrounding whitespace), no empties, no duplicates,
// length <= 40.
func validateLoadedNameList(names []string) ([]string, error) {
	if len(names) > MaxLoadedDefinitionsPerRun {
		return nil, fmt.Errorf("%w: loaded-state length %d > %d", ErrToolSearchLoadedStateInvalid, len(names), MaxLoadedDefinitionsPerRun)
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for i, n := range names {
		if n == "" {
			return nil, fmt.Errorf("%w: empty name at %d", ErrToolSearchLoadedStateInvalid, i)
		}
		// Noncanonical: surrounding whitespace / internal-only spaces after trim empty.
		trimmed := strings.TrimSpace(n)
		if trimmed == "" {
			return nil, fmt.Errorf("%w: blank name at %d", ErrToolSearchLoadedStateInvalid, i)
		}
		if trimmed != n {
			return nil, fmt.Errorf("%w: noncanonical name at %d", ErrToolSearchLoadedStateInvalid, i)
		}
		// Reject control characters / spaces inside names (canonical tool names are
		// non-empty printable identifiers without surrounding/embedded whitespace).
		for _, r := range n {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				return nil, fmt.Errorf("%w: noncanonical name at %d", ErrToolSearchLoadedStateInvalid, i)
			}
		}
		if _, dup := seen[n]; dup {
			return nil, fmt.Errorf("%w: duplicate name %q", ErrToolSearchLoadedStateInvalid, n)
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out, nil
}

// validateLoadedNamesAgainstCatalog enforces semantic catalog membership on a
// structurally-valid loaded-name list. Every name must exist in the current frozen
// catalog as exposure=deferred business definition. Rejects:
//   - unknown names (not in catalog)
//   - immediate / platform-control tools
//   - native tool_search executor name
//   - catalog-mismatched exposure
//
// Returns ErrToolSearchLoadedStateInvalid before any search/output/state mutation.
// Catalog may be nil only when the list is empty (defense).
func validateLoadedNamesAgainstCatalog(catalog *ToolCatalogSnapshot, names []string) error {
	if len(names) == 0 {
		return nil
	}
	if catalog == nil {
		return fmt.Errorf("%w: catalog required for loaded-state validation", ErrToolSearchLoadedStateInvalid)
	}
	for _, name := range names {
		// Native executor must never appear in the loaded deferred set.
		if name == ClientToolSearchToolName {
			return fmt.Errorf("%w: native %q in loaded-state", ErrToolSearchLoadedStateInvalid, ClientToolSearchToolName)
		}
		entry, ok := catalog.Entry(name)
		if !ok {
			return fmt.Errorf("%w: unknown loaded tool %q", ErrToolSearchLoadedStateInvalid, name)
		}
		// Only deferred business definitions are valid loaded-state members.
		if entry.Exposure != ToolExposureDeferred {
			return fmt.Errorf("%w: loaded tool %q exposure %q is not deferred", ErrToolSearchLoadedStateInvalid, name, entry.Exposure)
		}
		if entry.PlatformControl {
			// Platform-control tools are immediate by construction; belt-and-suspenders.
			return fmt.Errorf("%w: loaded tool %q is platform-control", ErrToolSearchLoadedStateInvalid, name)
		}
	}
	return nil
}

func storeLoadedDeferredToolNames(ctx context.Context, names []string) {
	if ctx == nil {
		return
	}
	// Deterministic serializable copy (sorted) for stable checkpoint blobs.
	cp := make([]string, len(names))
	copy(cp, names)
	sort.Strings(cp)
	adk.AddSessionValue(ctx, sessionKeyLoadedDeferredToolNames, cp)
}

func mergeLoadedDeferredToolNames(already, newly []string) []string {
	seen := make(map[string]struct{}, len(already)+len(newly))
	out := make([]string, 0, len(already)+len(newly))
	for _, n := range already {
		// already is trusted (decoded); still skip empties defensively.
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	for _, n := range newly {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// toolSearchArgs is the strict args object for client tool search.
// MaxResults is nil only when the field is omitted; explicit JSON null is invalid.
type toolSearchArgs struct {
	Query      string
	MaxResults *int
}

// parseStrictToolSearchArgs rejects unknown fields, duplicate JSON keys,
// non-object/trailing data, empty query, and invalid types.
// max_results: omission is valid (defaults to ceiling); explicit null, float,
// string, bool, object, and array are invalid. Integers only.
func parseStrictToolSearchArgs(raw string) (toolSearchArgs, error) {
	var zero toolSearchArgs
	s := strings.TrimSpace(raw)
	if s == "" {
		return zero, fmt.Errorf("%w: empty arguments", ErrToolSearchInvalidArgs)
	}

	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	// First pass: ensure a single JSON value that is an object, no trailing data,
	// and detect duplicate keys at every object nesting level.
	if err := decodeStrictJSONObjectNoDup(dec); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrToolSearchInvalidArgs, err)
	}
	if dec.More() {
		return zero, fmt.Errorf("%w: trailing data after JSON object", ErrToolSearchInvalidArgs)
	}

	// Second pass: decode with DisallowUnknownFields. Use json.RawMessage for
	// max_results so explicit null is distinguishable from omission (*int
	// conflates both to nil).
	dec2 := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec2.DisallowUnknownFields()
	var wire struct {
		Query      string          `json:"query"`
		MaxResults json.RawMessage `json:"max_results"`
	}
	if err := dec2.Decode(&wire); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrToolSearchInvalidArgs, err)
	}
	if dec2.More() {
		return zero, fmt.Errorf("%w: trailing data after JSON object", ErrToolSearchInvalidArgs)
	}

	args := toolSearchArgs{Query: wire.Query}
	if len(wire.MaxResults) > 0 {
		n, err := parseStrictMaxResults(wire.MaxResults)
		if err != nil {
			return zero, err
		}
		args.MaxResults = &n
	}
	return args, nil
}

// parseStrictMaxResults accepts only JSON integers (no null/float/string/bool/object/array).
func parseStrictMaxResults(raw json.RawMessage) (int, error) {
	trim := bytes.TrimSpace(raw)
	if len(trim) == 0 {
		return 0, fmt.Errorf("%w: max_results is empty", ErrToolSearchInvalidArgs)
	}
	// Explicit null is invalid (unlike omission).
	if bytes.Equal(trim, []byte("null")) {
		return 0, fmt.Errorf("%w: max_results must not be null", ErrToolSearchInvalidArgs)
	}
	// Reject non-number tokens early for clear errors.
	switch trim[0] {
	case '"', '{', '[', 't', 'f': // string, object, array, true, false
		return 0, fmt.Errorf("%w: max_results must be an integer", ErrToolSearchInvalidArgs)
	}
	// Decode as json.Number then require a pure integer representation.
	dec := json.NewDecoder(bytes.NewReader(trim))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return 0, fmt.Errorf("%w: max_results: %v", ErrToolSearchInvalidArgs, err)
	}
	if dec.More() {
		return 0, fmt.Errorf("%w: max_results trailing data", ErrToolSearchInvalidArgs)
	}
	num, ok := v.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%w: max_results must be an integer", ErrToolSearchInvalidArgs)
	}
	// Reject floats / scientific notation: only optional leading '-' + digits.
	s := num.String()
	if strings.ContainsAny(s, ".eE+") {
		// Allow a single leading '-' via the digit loop; '+' / '.' / 'e' fail.
		return 0, fmt.Errorf("%w: max_results must be an integer", ErrToolSearchInvalidArgs)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%w: max_results must be an integer", ErrToolSearchInvalidArgs)
	}
	return n, nil
}

// requireDeferredDisclosable ensures name is a deferred catalog tool eligible
// for native tool_search disclosure. Immediate tools and the native executor
// fail with ErrToolSearchNotDeferred; unknown names fail with ErrToolCatalogUnknownTool.
func requireDeferredDisclosable(catalog *ToolCatalogSnapshot, name string) error {
	if name == ClientToolSearchToolName {
		return fmt.Errorf("%w: cannot select search executor %q", ErrToolSearchNotDeferred, name)
	}
	ft, ok := catalog.byName[name]
	if !ok || ft == nil {
		return fmt.Errorf("%w: %q", ErrToolCatalogUnknownTool, name)
	}
	if ft.entry.Exposure != ToolExposureDeferred {
		return fmt.Errorf("%w: %q exposure %q", ErrToolSearchNotDeferred, name, ft.entry.Exposure)
	}
	return nil
}

// decodeStrictJSONObjectNoDup requires the next JSON value to be an object and
// rejects duplicate keys at any nesting level (mirrors agenticmsg strict args).
func decodeStrictJSONObjectNoDup(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return errors.New("arguments must be a JSON object")
	}
	return decodeStrictObjectBodyNoDup(dec)
}

func decodeStrictObjectBodyNoDup(dec *json.Decoder) error {
	seen := make(map[string]struct{})
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return errors.New("object key must be a string")
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("duplicate JSON key %q", key)
		}
		seen[key] = struct{}{}
		if err := decodeStrictJSONValueNoDup(dec); err != nil {
			return err
		}
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '}' {
		return errors.New("expected end of object")
	}
	return nil
}

func decodeStrictArrayBodyNoDup(dec *json.Decoder) error {
	for dec.More() {
		if err := decodeStrictJSONValueNoDup(dec); err != nil {
			return err
		}
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != ']' {
		return errors.New("expected end of array")
	}
	return nil
}

func decodeStrictJSONValueNoDup(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); ok {
		switch delim {
		case '{':
			return decodeStrictObjectBodyNoDup(dec)
		case '[':
			return decodeStrictArrayBodyNoDup(dec)
		default:
			return fmt.Errorf("unexpected delimiter %v", delim)
		}
	}
	return nil
}

func parseSelectNames(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	var names []string
	for i, p := range parts {
		name := strings.TrimSpace(p)
		// Reject empty segments (including whitespace-only): select:a,,b,
		// select:,a, select:a,, select:  ,a, and bare select: with no names.
		// Never silently skip — empty segments are malformed selection.
		if name == "" {
			return nil, fmt.Errorf("%w: empty select segment at index %d", ErrToolSearchInvalidArgs, i)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("%w: duplicate selected name %q", ErrToolSearchInvalidArgs, name)
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		// raw with no commas and only whitespace becomes one empty part above;
		// keep this guard for defense if Split semantics change.
		return nil, fmt.Errorf("%w: select: requires at least one tool name", ErrToolSearchInvalidArgs)
	}
	return names, nil
}

// keywordSearchCatalog scores DEFERRED catalog entries by name + description
// only (deterministic), caps to maxResults, returns names sorted by score then name.
// Immediate/platform-control entries and the native executor are never disclosed.
// Final result ordering for the caller is canonical-name sorted via LookupInfos.
func keywordSearchCatalog(query string, maxResults int, catalog *ToolCatalogSnapshot) []string {
	keywords := parseSearchKeywords(query)
	if len(keywords) == 0 || catalog == nil {
		return nil
	}

	type scored struct {
		name  string
		score int
	}
	var scoredList []scored

	for _, e := range catalog.entries {
		// Partition/exposure is authoritative: only deferred definitions load.
		if e.Exposure != ToolExposureDeferred {
			continue
		}
		if e.Name == ClientToolSearchToolName {
			continue
		}
		nameParts := splitToolNameParts(e.Name)
		nameLower := strings.ToLower(e.Name)
		descLower := strings.ToLower(e.Description)

		totalScore := 0
		allRequiredFound := true
		for _, kw := range keywords {
			kwLower := strings.ToLower(kw.word)
			kwScore := 0
			for _, part := range nameParts {
				partLower := strings.ToLower(part)
				if partLower == kwLower {
					kwScore = maxInt(kwScore, 10)
				} else if strings.Contains(partLower, kwLower) {
					kwScore = maxInt(kwScore, 5)
				}
			}
			if strings.Contains(nameLower, kwLower) {
				kwScore = maxInt(kwScore, 3)
			}
			if descLower != "" && strings.Contains(descLower, kwLower) {
				kwScore = maxInt(kwScore, 2)
			}
			if kw.required && kwScore == 0 {
				allRequiredFound = false
				break
			}
			totalScore += kwScore
		}
		if !allRequiredFound || totalScore <= 0 {
			continue
		}
		scoredList = append(scoredList, scored{name: e.Name, score: totalScore})
	}

	sort.Slice(scoredList, func(i, j int) bool {
		if scoredList[i].score != scoredList[j].score {
			return scoredList[i].score > scoredList[j].score
		}
		return scoredList[i].name < scoredList[j].name
	})

	n := maxResults
	if n > len(scoredList) {
		n = len(scoredList)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, scoredList[i].name)
	}
	return out
}

type searchKeyword struct {
	word     string
	required bool
}

func parseSearchKeywords(query string) []searchKeyword {
	parts := strings.Fields(query)
	var keywords []searchKeyword
	for _, p := range parts {
		if strings.HasPrefix(p, "+") {
			word := strings.TrimPrefix(p, "+")
			if word != "" {
				keywords = append(keywords, searchKeyword{word: word, required: true})
			}
		} else if p != "" {
			keywords = append(keywords, searchKeyword{word: p, required: false})
		}
	}
	return keywords
}

func splitToolNameParts(name string) []string {
	segments := strings.Split(name, "__")
	var parts []string
	for _, seg := range segments {
		for _, up := range strings.Split(seg, "_") {
			if up == "" {
				continue
			}
			parts = append(parts, splitCamelCaseParts(up)...)
		}
	}
	return parts
}

func splitCamelCaseParts(s string) []string {
	if s == "" {
		return nil
	}
	var parts []string
	runes := []rune(s)
	start := 0
	for i := 1; i < len(runes); i++ {
		if unicode.IsUpper(runes[i]) {
			if unicode.IsLower(runes[i-1]) {
				parts = append(parts, string(runes[start:i]))
				start = i
			} else if i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
				parts = append(parts, string(runes[start:i]))
				start = i
			}
		}
	}
	parts = append(parts, string(runes[start:]))
	return parts
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
