package einoruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// Tool exposure and kind constants for the frozen capability catalog (D4/D5).
const (
	// ToolExposureImmediate tools are sent with full schema on every model turn.
	ToolExposureImmediate = "immediate"
	// ToolExposureDeferred tools are deferred (name/desc visible; schema on demand).
	ToolExposureDeferred = "deferred"

	ToolKindTool     = "tool"
	ToolKindWorkflow = "workflow"
	ToolKindAgent    = "agent"
	ToolKindA2A      = "a2a"

	// ToolCatalogSchemaVersion is the frozen catalog snapshot schema version.
	ToolCatalogSchemaVersion = "tool-catalog.v1"

	// MaxImmediatePlatformTools is the hard ceiling for immediate platform-control tools (D4).
	MaxImmediatePlatformTools = 8

	// ModelToolCatalogMismatchCode is the stable error family code for catalog fail-close.
	ModelToolCatalogMismatchCode = "MODEL_TOOL_CATALOG_MISMATCH"
)

// Sentinel errors for catalog construction and runtime validation.
// Match with errors.Is; all wrap or equal ErrModelToolCatalogMismatch for the mismatch family.
var (
	// ErrModelToolCatalogMismatch is the stable MODEL_TOOL_CATALOG_MISMATCH family root.
	ErrModelToolCatalogMismatch = errors.New(ModelToolCatalogMismatchCode)

	// ErrToolCatalogInvalid is returned when catalog construction inputs are invalid.
	ErrToolCatalogInvalid = errors.New("einoruntime tool catalog invalid")
	// ErrToolCatalogDuplicateName is returned when two entries share a canonical name.
	ErrToolCatalogDuplicateName = errors.New("einoruntime tool catalog duplicate name")
	// ErrToolCatalogEmptyName is returned when a tool name is empty after trim.
	ErrToolCatalogEmptyName = errors.New("einoruntime tool catalog empty name")
	// ErrToolCatalogNonCanonicalName is returned when a raw tool name has surrounding
	// whitespace (raw != TrimSpace(raw)). Eino ToolsNode indexes by the raw Info().Name;
	// freezing a trimmed canonical name while the executable keeps padded identity would
	// make model/catalog "echo" lookups miss the ToolsNode index for " echo ".
	ErrToolCatalogNonCanonicalName = errors.New("einoruntime tool catalog non-canonical name")
	// ErrToolCatalogUnknownTool is returned when runtime state carries a tool not in the catalog.
	ErrToolCatalogUnknownTool = fmt.Errorf("%w: unknown tool", ErrModelToolCatalogMismatch)
	// ErrToolCatalogMissingTool is returned when an expected catalog tool is absent from state/tools.
	ErrToolCatalogMissingTool = fmt.Errorf("%w: missing tool", ErrModelToolCatalogMismatch)
	// ErrToolCatalogSchemaMismatch is returned when a tool's schema/info digest diverges.
	ErrToolCatalogSchemaMismatch = fmt.Errorf("%w: schema digest mismatch", ErrModelToolCatalogMismatch)
	// ErrToolCatalogCountMismatch is returned when executable tools count ≠ catalog entry count.
	ErrToolCatalogCountMismatch = fmt.Errorf("%w: tool count mismatch", ErrModelToolCatalogMismatch)
	// ErrToolCatalogDigestMismatch is returned when the overall catalog digest differs.
	ErrToolCatalogDigestMismatch = fmt.Errorf("%w: catalog digest mismatch", ErrModelToolCatalogMismatch)
	// ErrToolCatalogSearchNameCollision is returned when a business tool uses the search tool name.
	ErrToolCatalogSearchNameCollision = errors.New("einoruntime tool catalog search tool name collision")
	// ErrToolCatalogDuplicateInPartition is returned when the same name appears more than once
	// within ToolInfos or within DeferredToolInfos (same-side duplicate).
	ErrToolCatalogDuplicateInPartition = fmt.Errorf("%w: duplicate tool within partition", ErrModelToolCatalogMismatch)
	// ErrToolCatalogBusinessImmediate is returned when a business kind is immediate without
	// an explicit platform-control classification.
	ErrToolCatalogBusinessImmediate = errors.New("einoruntime tool catalog: business tools must be deferred unless platform control")
	// ErrToolCatalogSearchExecutorInvalid is returned when platform search-executor
	// metadata appearing in a partition is malformed (wrong schema/desc) before exclusion.
	ErrToolCatalogSearchExecutorInvalid = fmt.Errorf("%w: invalid search executor metadata", ErrModelToolCatalogMismatch)
)

// ToolCatalogEntry is one frozen, secret-free tool metadata record (D5).
// Parameters is canonical JSON of the parameter schema (object). Digests exclude
// credentials, connection runtime values, and execution closures.
//
// Callers receive defensive copies via ToolCatalogSnapshot.Entries / Entry;
// mutating a returned entry must not affect the live catalog.
type ToolCatalogEntry struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Parameters      json.RawMessage `json:"parameters"` // canonical JSON object
	Exposure        string          `json:"exposure"`   // immediate | deferred
	CapabilityID    string          `json:"capabilityId,omitempty"`
	RevisionID      string          `json:"revisionId,omitempty"`
	Kind            string          `json:"kind"` // tool | workflow | agent | a2a
	SchemaDigest    string          `json:"schemaDigest"`
	PlatformControl bool            `json:"platformControl,omitempty"`
}

// ToolCatalogSnapshot is an immutable, deterministic tool catalog for one run (D5).
// All fields are private; use accessors that return deep defensive copies.
// External mutation of caller slices/ToolInfo after Build must not change the snapshot.
// Do not deserialize into a trusted live catalog without BuildToolCatalog (or an
// equivalent validating constructor).
type ToolCatalogSnapshot struct {
	schemaVersion string
	entries       []ToolCatalogEntry // canonical name ascending; private frozen copies
	catalogDigest string
	byName        map[string]*frozenCatalogTool // name → frozen entry payload
}

// frozenCatalogTool holds immutable deep copies used at runtime.
type frozenCatalogTool struct {
	entry ToolCatalogEntry
	info  *schema.ToolInfo // defensive deep copy
}

// ToolCatalogBuildEntry is one input to BuildToolCatalog.
// Tool is required; Info() is read once at the freeze boundary.
type ToolCatalogBuildEntry struct {
	// Tool is the executable tool whose Info() is frozen. Required.
	Tool tool.BaseTool
	// Exposure is immediate or deferred. Business tools must be deferred unless
	// PlatformControl is true (D4). Empty defaults to deferred.
	Exposure string
	// CapabilityID is an optional stable capability identifier (secret-free).
	CapabilityID string
	// RevisionID is an optional release/revision identifier (secret-free).
	RevisionID string
	// Kind is tool | workflow | agent | a2a. Empty defaults to tool.
	Kind string
	// PlatformControl marks an explicit, non-spoofable platform-control tool.
	// Only platform-control tools may use exposure=immediate. Never inferred
	// from name or description.
	PlatformControl bool
}

// BuildToolCatalog freezes a deterministic, immutable catalog from the given
// executable tools. It does not trust callers to assemble an internally
// consistent snapshot: raw names must already be canonical (no surrounding
// whitespace), uniqueness is enforced, Info is deep copied, and digests are
// computed from secret-free canonical metadata only. Description whitespace is
// still normalized separately; executable identity must equal the frozen name.
func BuildToolCatalog(ctx context.Context, inputs []ToolCatalogBuildEntry) (*ToolCatalogSnapshot, error) {
	if len(inputs) == 0 {
		return &ToolCatalogSnapshot{
			schemaVersion: ToolCatalogSchemaVersion,
			entries:       []ToolCatalogEntry{},
			catalogDigest: digestCanonicalJSON([]any{}),
			byName:        map[string]*frozenCatalogTool{},
		}, nil
	}

	type pending struct {
		entry ToolCatalogEntry
		info  *schema.ToolInfo
	}
	pendingByName := make(map[string]pending, len(inputs))

	for i, in := range inputs {
		if in.Tool == nil {
			return nil, fmt.Errorf("%w: entry[%d] tool is nil", ErrToolCatalogInvalid, i)
		}
		info, err := in.Tool.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("%w: entry[%d] Info: %v", ErrToolCatalogInvalid, i, err)
		}
		if info == nil {
			return nil, fmt.Errorf("%w: entry[%d] Info is nil", ErrToolCatalogInvalid, i)
		}
		name, err := requireCanonicalToolName(info.Name)
		if err != nil {
			return nil, fmt.Errorf("entry[%d]: %w", i, err)
		}
		if isReservedSearchToolName(name) {
			return nil, fmt.Errorf("%w: %q", ErrToolCatalogSearchNameCollision, name)
		}
		if _, exists := pendingByName[name]; exists {
			return nil, fmt.Errorf("%w: %q", ErrToolCatalogDuplicateName, name)
		}

		exposure := strings.TrimSpace(in.Exposure)
		if exposure == "" {
			exposure = ToolExposureDeferred
		}
		if exposure != ToolExposureImmediate && exposure != ToolExposureDeferred {
			return nil, fmt.Errorf("%w: entry %q exposure %q", ErrToolCatalogInvalid, name, exposure)
		}

		kind := strings.TrimSpace(in.Kind)
		if kind == "" {
			kind = ToolKindTool
		}
		switch kind {
		case ToolKindTool, ToolKindWorkflow, ToolKindAgent, ToolKindA2A:
		default:
			return nil, fmt.Errorf("%w: entry %q kind %q", ErrToolCatalogInvalid, name, kind)
		}

		// Business kinds may not be immediate unless explicitly platform-control.
		// PlatformControl is the only non-spoofable classification channel.
		platform := in.PlatformControl
		if exposure == ToolExposureImmediate && !platform {
			return nil, fmt.Errorf("%w: entry %q kind %q", ErrToolCatalogBusinessImmediate, name, kind)
		}
		// Deferred platform-control is allowed (e.g. rare); immediate requires platform.
		if exposure == ToolExposureImmediate && platform {
			// counted later against MaxImmediatePlatformTools at agent build
		}

		// Cap-first freeze: description + schema limits are enforced BEFORE any
		// unbounded ToolInfo JSON deep-copy/parse allocation under our control.
		// Name is already canonical (raw == TrimSpace(raw)). Description is still
		// trimmed for digest identity; name must remain identical to the executable.
		desc := strings.TrimSpace(info.Desc)
		if err := validateToolDescriptionLimits(desc); err != nil {
			return nil, fmt.Errorf("entry %q: %w", name, err)
		}
		// Schema byte/depth/node/enum/const caps run inside canonicalize on the
		// raw ToJSONSchema marshal — before rebuilding ParamsOneOf.
		paramsRaw, err := canonicalParametersJSON(info)
		if err != nil {
			// Prefer typed MODEL_TOOL_CATALOG_INVALID family when applicable.
			if errors.Is(err, ErrModelToolCatalogInvalid) {
				return nil, fmt.Errorf("entry %q: %w", name, err)
			}
			return nil, fmt.Errorf("%w: entry %q parameters: %v", ErrToolCatalogInvalid, name, err)
		}
		// Independent frozen ToolInfo: name/desc + ParamsOneOf rebuilt from the
		// same canonical schema bytes that feed digests/search/wire (no float64
		// JSON-round-trip clone of attacker-controlled schema).
		frozenInfo := &schema.ToolInfo{Name: name, Desc: desc}
		if err := applyCanonicalParamsOneOf(frozenInfo, paramsRaw); err != nil {
			return nil, fmt.Errorf("%w: entry %q canonical ParamsOneOf: %v", ErrToolCatalogInvalid, name, err)
		}

		entry := ToolCatalogEntry{
			Name:            name,
			Description:     desc,
			Parameters:      paramsRaw,
			Exposure:        exposure,
			CapabilityID:    strings.TrimSpace(in.CapabilityID),
			RevisionID:      strings.TrimSpace(in.RevisionID),
			Kind:            kind,
			PlatformControl: platform,
		}
		entry.SchemaDigest = digestToolSchema(entry)
		pendingByName[name] = pending{entry: entry, info: frozenInfo}
	}

	names := make([]string, 0, len(pendingByName))
	for n := range pendingByName {
		names = append(names, n)
	}
	sort.Strings(names)

	entries := make([]ToolCatalogEntry, 0, len(names))
	byName := make(map[string]*frozenCatalogTool, len(names))
	for _, n := range names {
		p := pendingByName[n]
		// Re-copy entry Parameters so the slice and maps are independent.
		ent := p.entry
		if ent.Parameters != nil {
			ent.Parameters = append(json.RawMessage(nil), ent.Parameters...)
		}
		entries = append(entries, ent)
		byName[n] = &frozenCatalogTool{
			entry: ent,
			info:  p.info,
		}
	}

	if err := validateDeferredCatalogBudgets(entries); err != nil {
		return nil, err
	}

	snap := &ToolCatalogSnapshot{
		schemaVersion: ToolCatalogSchemaVersion,
		entries:       entries,
		catalogDigest: digestCatalogEntries(entries),
		byName:        byName,
	}
	return snap, nil
}

// SchemaVersion returns the frozen catalog schema version string.
func (c *ToolCatalogSnapshot) SchemaVersion() string {
	if c == nil {
		return ""
	}
	return c.schemaVersion
}

// CatalogDigest returns the frozen overall catalog digest.
func (c *ToolCatalogSnapshot) CatalogDigest() string {
	if c == nil {
		return ""
	}
	return c.catalogDigest
}

// Entries returns a deep defensive copy of all frozen entries (name-sorted).
// Mutating the returned slice, entry fields, or Parameters bytes must not affect
// the live catalog.
func (c *ToolCatalogSnapshot) Entries() []ToolCatalogEntry {
	if c == nil || len(c.entries) == 0 {
		return nil
	}
	out := make([]ToolCatalogEntry, len(c.entries))
	for i, e := range c.entries {
		out[i] = copyCatalogEntry(e)
	}
	return out
}

// Entry returns a defensive copy of the catalog entry for name, or false if absent.
func (c *ToolCatalogSnapshot) Entry(name string) (ToolCatalogEntry, bool) {
	if c == nil {
		return ToolCatalogEntry{}, false
	}
	ft, ok := c.byName[strings.TrimSpace(name)]
	if !ok || ft == nil {
		return ToolCatalogEntry{}, false
	}
	return copyCatalogEntry(ft.entry), true
}

// ToolInfoCopy returns a deep defensive copy of the frozen ToolInfo for name.
func (c *ToolCatalogSnapshot) ToolInfoCopy(name string) (*schema.ToolInfo, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: nil catalog", ErrToolCatalogInvalid)
	}
	ft, ok := c.byName[strings.TrimSpace(name)]
	if !ok || ft == nil {
		return nil, fmt.Errorf("%w: %q", ErrToolCatalogUnknownTool, name)
	}
	return deepCopyToolInfo(ft.info)
}

// SchemaDigest returns the frozen schema digest for name.
func (c *ToolCatalogSnapshot) SchemaDigest(name string) (string, bool) {
	ent, ok := c.Entry(name)
	if !ok {
		return "", false
	}
	return ent.SchemaDigest, true
}

// Names returns canonical names in sorted order (defensive copy of the name list).
func (c *ToolCatalogSnapshot) Names() []string {
	if c == nil || len(c.entries) == 0 {
		return nil
	}
	out := make([]string, len(c.entries))
	for i, e := range c.entries {
		out[i] = e.Name
	}
	return out
}

// Len returns the number of catalog entries.
func (c *ToolCatalogSnapshot) Len() int {
	if c == nil {
		return 0
	}
	return len(c.entries)
}

// ImmediateCount returns the number of immediate-exposure entries.
func (c *ToolCatalogSnapshot) ImmediateCount() int {
	if c == nil {
		return 0
	}
	n := 0
	for _, e := range c.entries {
		if e.Exposure == ToolExposureImmediate {
			n++
		}
	}
	return n
}

// hasName reports whether name is present in the frozen catalog (internal).
func (c *ToolCatalogSnapshot) hasName(name string) bool {
	if c == nil {
		return false
	}
	_, ok := c.byName[strings.TrimSpace(name)]
	return ok
}

// ValidateExecutableTools revalidates tools one-to-one against the frozen catalog.
// Fails if name, count, info/schema digest, duplicates, or overall digest differ.
func (c *ToolCatalogSnapshot) ValidateExecutableTools(ctx context.Context, tools []tool.BaseTool) error {
	if c == nil {
		return fmt.Errorf("%w: nil catalog", ErrToolCatalogInvalid)
	}
	type seen struct {
		name   string
		digest string
	}
	var found []seen
	nameSet := make(map[string]struct{}, len(tools))

	for i, t := range tools {
		if t == nil {
			return fmt.Errorf("%w: tools[%d] is nil", ErrModelToolCatalogMismatch, i)
		}
		info, err := t.Info(ctx)
		if err != nil {
			return fmt.Errorf("%w: tools[%d] Info: %v", ErrModelToolCatalogMismatch, i, err)
		}
		if info == nil {
			return fmt.Errorf("%w: tools[%d] Info is nil", ErrModelToolCatalogMismatch, i)
		}
		// Fail closed on noncanonical raw names so frozen/model-visible identity
		// cannot diverge from ToolsNode's raw-name index.
		name, nameErr := requireCanonicalToolName(info.Name)
		if nameErr != nil {
			return fmt.Errorf("%w: tools[%d]: %w", ErrModelToolCatalogMismatch, i, nameErr)
		}
		if isReservedSearchToolName(name) {
			return fmt.Errorf("%w: tools must not include search executor %q", ErrToolCatalogSearchNameCollision, name)
		}
		if _, dup := nameSet[name]; dup {
			return fmt.Errorf("%w: duplicate executable tool %q", ErrModelToolCatalogMismatch, name)
		}
		nameSet[name] = struct{}{}

		ft, ok := c.byName[name]
		if !ok {
			return fmt.Errorf("%w: %q", ErrToolCatalogUnknownTool, name)
		}
		// Recompute schema digest from live Info with the same canonicalization
		// used at freeze (canonical name + trimmed description).
		liveDesc := strings.TrimSpace(info.Desc)
		paramsRaw, err := canonicalParametersJSON(info)
		if err != nil {
			return fmt.Errorf("%w: tool %q parameters: %v", ErrToolCatalogSchemaMismatch, name, err)
		}
		live := ToolCatalogEntry{
			Name:        name,
			Description: liveDesc,
			Parameters:  paramsRaw,
		}
		liveDigest := digestToolSchema(live)
		if liveDigest != ft.entry.SchemaDigest {
			return fmt.Errorf("%w: tool %q", ErrToolCatalogSchemaMismatch, name)
		}
		// Description is part of info digest identity for mismatch detection.
		if liveDesc != ft.entry.Description {
			return fmt.Errorf("%w: tool %q description changed", ErrToolCatalogSchemaMismatch, name)
		}
		found = append(found, seen{name: name, digest: liveDigest})
	}

	if len(found) != len(c.entries) {
		return fmt.Errorf("%w: executable=%d catalog=%d", ErrToolCatalogCountMismatch, len(found), len(c.entries))
	}
	// Ensure every catalog name is present.
	for _, e := range c.entries {
		if _, ok := nameSet[e.Name]; !ok {
			return fmt.Errorf("%w: %q", ErrToolCatalogMissingTool, e.Name)
		}
	}
	// Overall digest stability: recomputed from frozen entries must match.
	if dig := digestCatalogEntries(c.entries); dig != c.catalogDigest {
		return fmt.Errorf("%w: frozen snapshot self-digest diverged", ErrToolCatalogDigestMismatch)
	}
	return nil
}

// PartitionToolInfos validates ToolInfos and DeferredToolInfos against the catalog
// with partition-aware duplicate rules, then returns (immediate, deferred) rebuilt
// exclusively from frozen defensive copies, sorted by canonical name within exposure.
//
// Rules:
//   - Within each side, duplicate canonical names fail (even if schemas match).
//   - Across sides, exactly one identical same-name entry on each side may be
//     collapsed as the pinned Eino resume idempotence case; mismatched digest fails.
//   - More than one occurrence on either side fails.
//   - Missing, unknown, nil, executor info (searchToolName), and catalog mismatch fail closed.
//   - searchToolName (if non-empty) is stripped and never returned in either list.
//   - Platform-owned native search executor (tool_search): validated against the
//     canonical frozen contract BEFORE exclusion. The union of both partitions may
//     contain at most one well-formed executor total — one on ToolInfos plus another
//     on DeferredToolInfos fails (unlike ordinary business tools, which may use the
//     cross-partition resume-duplicate exception). Same-side duplicates also fail.
func (c *ToolCatalogSnapshot) PartitionToolInfos(
	toolInfos []*schema.ToolInfo,
	deferredToolInfos []*schema.ToolInfo,
	searchToolName string,
) (immediate, deferred []*schema.ToolInfo, err error) {
	if c == nil {
		return nil, nil, fmt.Errorf("%w: nil catalog", ErrToolCatalogInvalid)
	}
	searchToolName = strings.TrimSpace(searchToolName)

	// Collect validated names per side (order of first appearance does not matter;
	// reconstruction always uses frozen catalog exposure).
	immSide, immSearch, err := c.collectPartitionSide(toolInfos, searchToolName, "ToolInfos")
	if err != nil {
		return nil, nil, err
	}
	defSide, defSearch, err := c.collectPartitionSide(deferredToolInfos, searchToolName, "DeferredToolInfos")
	if err != nil {
		return nil, nil, err
	}
	// Native executor is NOT eligible for the ordinary cross-partition resume
	// duplicate exception: at most one total across both partitions.
	if searchToolName != "" && immSearch+defSearch > 1 {
		return nil, nil, fmt.Errorf(
			"%w: %q across partitions (ToolInfos=%d DeferredToolInfos=%d; at most one total)",
			ErrToolCatalogDuplicateInPartition, searchToolName, immSearch, defSearch,
		)
	}

	// Cross-side: same name may appear at most once on each side (already enforced
	// within side). If present on both, digests must match the frozen catalog
	// (already validated per occurrence) — collapse as resume idempotence.
	// Union of names must equal the full catalog.
	union := make(map[string]struct{}, len(immSide)+len(defSide))
	for name := range immSide {
		union[name] = struct{}{}
	}
	for name := range defSide {
		union[name] = struct{}{}
	}

	if len(union) != len(c.entries) {
		for _, e := range c.entries {
			if _, ok := union[e.Name]; !ok {
				return nil, nil, fmt.Errorf("%w: %q", ErrToolCatalogMissingTool, e.Name)
			}
		}
		return nil, nil, fmt.Errorf("%w: state tool count %d != catalog %d", ErrToolCatalogCountMismatch, len(union), len(c.entries))
	}

	// Rebuild exclusively from frozen defensive copies, sorted by name within exposure.
	var immNames, defNames []string
	for _, e := range c.entries {
		if e.Exposure == ToolExposureImmediate {
			immNames = append(immNames, e.Name)
		} else {
			defNames = append(defNames, e.Name)
		}
	}
	immediate = make([]*schema.ToolInfo, 0, len(immNames))
	for _, n := range immNames {
		cp, err := deepCopyToolInfo(c.byName[n].info)
		if err != nil {
			return nil, nil, err
		}
		immediate = append(immediate, cp)
	}
	deferred = make([]*schema.ToolInfo, 0, len(defNames))
	for _, n := range defNames {
		cp, err := deepCopyToolInfo(c.byName[n].info)
		if err != nil {
			return nil, nil, err
		}
		deferred = append(deferred, cp)
	}
	return immediate, deferred, nil
}

// collectPartitionSide validates one partition side: no nils, no same-side
// duplicates, every name known, every digest matches frozen catalog.
// Platform search-executor entries (searchToolName) are validated for
// malformation and same-side duplicates BEFORE exclusion; they never enter the
// business/deferred name set and never recursively disclose themselves.
// Returns the set of canonical business names seen (excluding search tool) and
// the count of well-formed platform search executors on this side.
func (c *ToolCatalogSnapshot) collectPartitionSide(
	infos []*schema.ToolInfo,
	searchToolName string,
	sideLabel string,
) (map[string]struct{}, int, error) {
	seen := make(map[string]struct{}, len(infos))
	searchSeen := 0
	for _, info := range infos {
		if info == nil {
			return nil, 0, fmt.Errorf("%w: nil ToolInfo in %s", ErrModelToolCatalogMismatch, sideLabel)
		}
		// Reject noncanonical raw names before any partition membership logic so
		// executable identity cannot diverge from frozen/model-visible names.
		name, nameErr := requireCanonicalToolName(info.Name)
		if nameErr != nil {
			return nil, 0, fmt.Errorf("%w: in %s: %w", ErrModelToolCatalogMismatch, sideLabel, nameErr)
		}
		if searchToolName != "" && name == searchToolName {
			// Validate full canonical executor contract first; only then exclude.
			if err := validatePlatformSearchExecutorInfo(info); err != nil {
				return nil, 0, fmt.Errorf("%w: in %s: %v", ErrToolCatalogSearchExecutorInvalid, sideLabel, err)
			}
			searchSeen++
			if searchSeen > 1 {
				return nil, 0, fmt.Errorf("%w: %q in %s", ErrToolCatalogDuplicateInPartition, name, sideLabel)
			}
			// Platform-owned native search is not a business/deferred catalog tool.
			continue
		}
		if _, dup := seen[name]; dup {
			return nil, 0, fmt.Errorf("%w: %q in %s", ErrToolCatalogDuplicateInPartition, name, sideLabel)
		}

		ft, ok := c.byName[name]
		if !ok {
			return nil, 0, fmt.Errorf("%w: %q", ErrToolCatalogUnknownTool, name)
		}
		paramsRaw, err := canonicalParametersJSON(info)
		if err != nil {
			return nil, 0, fmt.Errorf("%w: tool %q: %v", ErrToolCatalogSchemaMismatch, name, err)
		}
		liveDesc := strings.TrimSpace(info.Desc)
		live := ToolCatalogEntry{Name: name, Description: liveDesc, Parameters: paramsRaw}
		if digestToolSchema(live) != ft.entry.SchemaDigest {
			return nil, 0, fmt.Errorf("%w: tool %q", ErrToolCatalogSchemaMismatch, name)
		}
		seen[name] = struct{}{}
	}
	return seen, searchSeen, nil
}

// validatePlatformSearchExecutorInfo enforces the single canonical frozen
// platform-owned native tool_search contract across name, description,
// parameter schema, and metadata:
//   - Name: raw name must be canonical (no surrounding whitespace) and equal
//     ClientToolSearchToolName exactly
//   - Description: TrimSpace must equal the platform frozen description exactly
//     (description whitespace is still normalized; name is not)
//   - Parameters: canonical JSON schema digest must match clientToolSearchToolInfo
//   - Extra: must be absent (nil or empty); any unexpected key/value fails closed
//
// Arbitrary non-empty descriptions and ignored Extra metadata are NOT accepted.
func validatePlatformSearchExecutorInfo(info *schema.ToolInfo) error {
	if info == nil {
		return errors.New("nil ToolInfo")
	}
	expected := clientToolSearchToolInfo()

	name, err := requireCanonicalToolName(info.Name)
	if err != nil {
		return err
	}
	if name != ClientToolSearchToolName {
		return fmt.Errorf("name %q != %q", name, ClientToolSearchToolName)
	}
	// Description still uses trim canonicalization; content must match platform contract.
	liveDesc := strings.TrimSpace(info.Desc)
	expDesc := strings.TrimSpace(expected.Desc)
	if liveDesc == "" {
		return errors.New("empty description")
	}
	if liveDesc != expDesc {
		return errors.New("description does not match platform search executor contract")
	}

	// Extra is not part of the native executor contract; any non-empty metadata fails.
	if len(info.Extra) > 0 {
		return errors.New("unexpected Extra metadata on platform search executor")
	}

	liveParams, err := canonicalParametersJSON(info)
	if err != nil {
		return fmt.Errorf("parameters: %v", err)
	}
	expParams, err := canonicalParametersJSON(expected)
	if err != nil {
		return fmt.Errorf("expected parameters: %v", err)
	}
	// Full identity: name + description + parameters (deterministic digests).
	live := ToolCatalogEntry{Name: name, Description: liveDesc, Parameters: liveParams}
	exp := ToolCatalogEntry{Name: ClientToolSearchToolName, Description: expDesc, Parameters: expParams}
	if digestToolSchema(live) != digestToolSchema(exp) {
		return errors.New("parameter schema does not match platform search executor")
	}
	return nil
}

// isReservedSearchToolName reports whether name is a platform-owned search
// executor that must never appear as a catalog business tool.
func isReservedSearchToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case ClientToolSearchToolName, PlatformCatalogSearchToolName:
		return true
	default:
		return false
	}
}

// requireCanonicalToolName rejects empty and whitespace-padded raw tool names.
// Executable ToolsNode indexes Info().Name by the raw string; the frozen catalog
// and model-visible name must therefore equal that raw identity exactly.
func requireCanonicalToolName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", ErrToolCatalogEmptyName
	}
	if raw != name {
		return "", fmt.Errorf("%w: %q", ErrToolCatalogNonCanonicalName, raw)
	}
	return name, nil
}

// LookupInfos returns frozen ToolInfo copies for the given names (sorted),
// validating each name against the catalog. Duplicates and unauthorized names fail closed.
func (c *ToolCatalogSnapshot) LookupInfos(names []string) ([]*schema.ToolInfo, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: nil catalog", ErrToolCatalogInvalid)
	}
	seen := make(map[string]struct{}, len(names))
	var sorted []string
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, fmt.Errorf("%w: empty name in selection", ErrModelToolCatalogMismatch)
		}
		if isReservedSearchToolName(name) {
			return nil, fmt.Errorf("%w: cannot select search executor", ErrModelToolCatalogMismatch)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("%w: duplicate selected name %q", ErrModelToolCatalogMismatch, name)
		}
		if _, ok := c.byName[name]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrToolCatalogUnknownTool, name)
		}
		seen[name] = struct{}{}
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	out := make([]*schema.ToolInfo, 0, len(sorted))
	for _, n := range sorted {
		cp, err := deepCopyToolInfo(c.byName[n].info)
		if err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	return out, nil
}

// MarshalJSON serializes private frozen state via a snapshot DTO. Never use
// json.Unmarshal into ToolCatalogSnapshot as a trusted live catalog; rebuild
// with BuildToolCatalog (validating constructor) instead.
func (c *ToolCatalogSnapshot) MarshalJSON() ([]byte, error) {
	if c == nil {
		return []byte("null"), nil
	}
	type dto struct {
		SchemaVersion string             `json:"schemaVersion"`
		Entries       []ToolCatalogEntry `json:"entries"`
		CatalogDigest string             `json:"catalogDigest"`
	}
	return json.Marshal(dto{
		SchemaVersion: c.schemaVersion,
		Entries:       c.Entries(), // defensive copies
		CatalogDigest: c.catalogDigest,
	})
}

// --- digest / copy helpers ---------------------------------------------------

func copyCatalogEntry(e ToolCatalogEntry) ToolCatalogEntry {
	out := e
	if e.Parameters != nil {
		out.Parameters = append(json.RawMessage(nil), e.Parameters...)
	}
	return out
}

// deepCopyToolInfo produces an independent ToolInfo using the cap-first,
// lossless canonical freeze path (no ToolInfo JSON round-trip through
// encoding/json that would convert enum/const numbers to float64 or allocate
// unbounded attacker-controlled schema trees before hard caps).
//
// Extra is dropped so secrets never enter the frozen catalog surface.
// Description and parameters schema limits are enforced before ParamsOneOf rebuild.
func deepCopyToolInfo(info *schema.ToolInfo) (*schema.ToolInfo, error) {
	if info == nil {
		return nil, fmt.Errorf("%w: nil ToolInfo", ErrToolCatalogInvalid)
	}
	desc := strings.TrimSpace(info.Desc)
	if err := validateToolDescriptionLimits(desc); err != nil {
		return nil, err
	}
	// Cap-first: canonicalize enforces raw-byte/depth/node/enum/const limits
	// on the schema before any ParamsOneOf rebuild allocation from canonical bytes.
	paramsRaw, err := canonicalParametersJSON(info)
	if err != nil {
		return nil, err
	}
	out := &schema.ToolInfo{
		Name: info.Name,
		Desc: desc,
		// Extra intentionally nil — secret-free catalog surface.
	}
	if err := applyCanonicalParamsOneOf(out, paramsRaw); err != nil {
		return nil, err
	}
	return out, nil
}

// canonicalParametersJSON returns deterministic JSON for the tool's parameter schema.
// Nil ParamsOneOf becomes an empty object. Non-object schemas fail closed.
// Applies OpenAI Agentic schema compatibility (allowlist, size/depth limits, no $ref).
// Schema body is never logged.
//
// Marshal of *jsonschema.Schema uses standard encoding/json; Enum/Const that still
// hold float64 (pre-lossless decode) may already be rounded. Callers that build
// ToolInfo from raw JSON must use decodeJSONSchemaLossless / applyCanonicalParamsOneOf
// so numbers remain json.Number before this marshal.
func canonicalParametersJSON(info *schema.ToolInfo) (json.RawMessage, error) {
	if info == nil {
		return nil, fmt.Errorf("nil tool info")
	}
	if info.ParamsOneOf == nil {
		return json.RawMessage(`{}`), nil
	}
	js, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		return nil, err
	}
	if js == nil {
		return json.RawMessage(`{}`), nil
	}
	// Require object-type parameter schemas for model tools.
	if t := strings.ToLower(strings.TrimSpace(js.Type)); t != "" && t != "object" {
		return nil, fmt.Errorf("%w: parameters schema type must be object, got %q", ErrToolSchemaInvalidRoot, js.Type)
	}
	// Pre-cap: reject oversized marshal candidates before full canonicalize parse.
	// json.Marshal of a hostile deep Schema can still allocate; depth/node caps are
	// enforced again inside canonicalizeAndValidateParametersSchema on the bytes.
	b, err := json.Marshal(js)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxToolSchemaBytes {
		return nil, ErrToolSchemaTooLarge
	}
	// OpenAI Agentic boundary: canonical UTF-8, allowlist, limits, no external $ref.
	return canonicalizeAndValidateParametersSchema(json.RawMessage(b))
}

// applyCanonicalParamsOneOf rebuilds info.ParamsOneOf from already-canonicalized
// parameters JSON so model-visible ToolInfo matches catalog entry.Parameters.
//
// Number preservation: jsonschema.Schema.UnmarshalJSON uses encoding/json without
// UseNumber, which converts enum/const integers to float64 (e.g. 9007199254740993
// → 9007199254740992). decodeJSONSchemaLossless restores json.Number into
// Enum/Const so digest/search/wire share one lossless canonical representation.
// Empty object becomes a concrete object schema.
func applyCanonicalParamsOneOf(info *schema.ToolInfo, canonical json.RawMessage) error {
	if info == nil {
		return fmt.Errorf("nil tool info")
	}
	raw := bytes.TrimSpace(canonical)
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	js, err := decodeJSONSchemaLossless(raw)
	if err != nil {
		return err
	}
	// Force a concrete object schema when the library path would re-serialize
	// vacuous schemas as JSON boolean true.
	if strings.TrimSpace(js.Type) == "" && (js.Properties == nil || js.Properties.Len() == 0) {
		js, err = decodeJSONSchemaLossless([]byte(`{"type":"object"}`))
		if err != nil {
			return fmt.Errorf("decode empty object schema: %w", err)
		}
	}
	info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(js)
	return nil
}

// decodeJSONSchemaLossless unmarshals parameters JSON into *jsonschema.Schema
// while preserving arbitrary JSON numbers in enum/const (UseNumber + restore).
// Prefer this over json.Unmarshal into Schema anywhere on the catalog/clone path.
func decodeJSONSchemaLossless(raw []byte) (*jsonschema.Schema, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	var numberTree any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&numberTree); err != nil {
		return nil, fmt.Errorf("decode canonical schema (UseNumber): %w", err)
	}
	js := &jsonschema.Schema{}
	if err := json.Unmarshal(raw, js); err != nil {
		return nil, fmt.Errorf("decode canonical schema: %w", err)
	}
	if err := restoreSchemaJSONNumbers(js, numberTree); err != nil {
		return nil, fmt.Errorf("restore lossless numbers: %w", err)
	}
	return js, nil
}

// restoreSchemaJSONNumbers walks a jsonschema.Schema in parallel with a
// UseNumber-decoded JSON tree and replaces float64 (and any non-json.Number
// numeric any values) in Enum/Const with the corresponding json.Number from the
// tree. Nested property/item schemas are restored recursively. No float64 path
// remains in the frozen ParamsOneOf surface for JSON numbers.
func restoreSchemaJSONNumbers(s *jsonschema.Schema, tree any) error {
	if s == nil {
		return nil
	}
	obj, _ := tree.(map[string]any)
	if obj == nil {
		return nil
	}
	// Enum: replace entire slice from UseNumber tree (preserves json.Number).
	if enumTree, ok := obj["enum"].([]any); ok {
		s.Enum = deepCopyJSONNumberTreeSlice(enumTree)
	}
	// Const: single value.
	if c, ok := obj["const"]; ok {
		s.Const = deepCopyJSONNumberTree(c)
	}
	// Default is stripped by canonicalize policy, but restore if present.
	if d, ok := obj["default"]; ok {
		s.Default = deepCopyJSONNumberTree(d)
	}
	// Nested properties.
	if s.Properties != nil {
		if propsTree, ok := obj["properties"].(map[string]any); ok {
			for pair := s.Properties.Oldest(); pair != nil; pair = pair.Next() {
				childTree, has := propsTree[pair.Key]
				if !has {
					continue
				}
				if err := restoreSchemaJSONNumbers(pair.Value, childTree); err != nil {
					return err
				}
			}
		}
	}
	// items
	if s.Items != nil {
		if err := restoreSchemaJSONNumbers(s.Items, obj["items"]); err != nil {
			return err
		}
	}
	// additionalProperties as schema object
	if s.AdditionalProperties != nil {
		if err := restoreSchemaJSONNumbers(s.AdditionalProperties, obj["additionalProperties"]); err != nil {
			return err
		}
	}
	// Combinators
	for i, sub := range s.AllOf {
		var child any
		if arr, ok := obj["allOf"].([]any); ok && i < len(arr) {
			child = arr[i]
		}
		if err := restoreSchemaJSONNumbers(sub, child); err != nil {
			return err
		}
	}
	for i, sub := range s.AnyOf {
		var child any
		if arr, ok := obj["anyOf"].([]any); ok && i < len(arr) {
			child = arr[i]
		}
		if err := restoreSchemaJSONNumbers(sub, child); err != nil {
			return err
		}
	}
	for i, sub := range s.OneOf {
		var child any
		if arr, ok := obj["oneOf"].([]any); ok && i < len(arr) {
			child = arr[i]
		}
		if err := restoreSchemaJSONNumbers(sub, child); err != nil {
			return err
		}
	}
	if s.Not != nil {
		if err := restoreSchemaJSONNumbers(s.Not, obj["not"]); err != nil {
			return err
		}
	}
	return nil
}

// deepCopyJSONNumberTree returns a defensive copy of a UseNumber-decoded JSON
// value, preserving json.Number and rejecting float64 if encountered (should not
// appear on the UseNumber path).
func deepCopyJSONNumberTree(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case json.Number:
		return t
	case string, bool:
		return t
	case float64:
		// Should not appear on UseNumber decode path; refuse silent float retention.
		// Convert via json.Number of the shortest representation only if finite —
		// callers must not rely on this; Prefer UseNumber source.
		return json.Number(strconv.FormatFloat(t, 'g', -1, 64))
	case []any:
		return deepCopyJSONNumberTreeSlice(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			out[k] = deepCopyJSONNumberTree(child)
		}
		return out
	default:
		return t
	}
}

func deepCopyJSONNumberTreeSlice(in []any) []any {
	out := make([]any, len(in))
	for i, child := range in {
		out[i] = deepCopyJSONNumberTree(child)
	}
	return out
}

// digestToolSchema is SHA-256 hex of secret-free name+description+parameters.
func digestToolSchema(e ToolCatalogEntry) string {
	payload := struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}{
		Name:        e.Name,
		Description: e.Description,
		Parameters:  e.Parameters,
	}
	return digestCanonicalJSON(payload)
}

// digestCatalogEntries is SHA-256 hex of the sorted catalog entry metadata
// (excluding SchemaDigest field itself to avoid circular self-hash).
func digestCatalogEntries(entries []ToolCatalogEntry) string {
	type wire struct {
		Name            string          `json:"name"`
		Description     string          `json:"description"`
		Parameters      json.RawMessage `json:"parameters"`
		Exposure        string          `json:"exposure"`
		CapabilityID    string          `json:"capabilityId,omitempty"`
		RevisionID      string          `json:"revisionId,omitempty"`
		Kind            string          `json:"kind"`
		SchemaDigest    string          `json:"schemaDigest"`
		PlatformControl bool            `json:"platformControl,omitempty"`
	}
	out := make([]wire, 0, len(entries))
	for _, e := range entries {
		out = append(out, wire{
			Name:            e.Name,
			Description:     e.Description,
			Parameters:      e.Parameters,
			Exposure:        e.Exposure,
			CapabilityID:    e.CapabilityID,
			RevisionID:      e.RevisionID,
			Kind:            e.Kind,
			SchemaDigest:    e.SchemaDigest,
			PlatformControl: e.PlatformControl,
		})
	}
	return digestCanonicalJSON(out)
}

func digestCanonicalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// Should not happen for our closed types; still produce a stable failure digest.
		sum := sha256.Sum256([]byte("marshal-error:" + err.Error()))
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
