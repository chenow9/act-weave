package contextwindow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Agentic estimator constants (platform-frozen, not user options).
const (
	// EstimatorVersionAgenticOpenAIResponsesV1 is the Agentic-only estimator version.
	// Classic EstimatorVersion remains "contextwindow-estimator.v1".
	EstimatorVersionAgenticOpenAIResponsesV1 = "contextwindow-estimator.agentic-openai-responses.v1"

	// AgenticMaxIterations is fixed/validated at 8 (D3 / D7).
	AgenticMaxIterations = 8
	// AgenticMaxLoadedToolsPerSearch is fixed at 5.
	AgenticMaxLoadedToolsPerSearch = 5
	// AgenticMaxLoadedDefinitionsPerRun is 5 * 8 = 40.
	AgenticMaxLoadedDefinitionsPerRun = AgenticMaxIterations * AgenticMaxLoadedToolsPerSearch

	// Prompt-cache canonical constants (exact pins; arbitrary strings rejected).
	PromptCacheProviderProtocolOpenAIResponsesV1 = "openai-responses-v1"
	PromptCacheAdapterAgenticOpenAIV022          = "agenticopenai/v0.2.2"

	// Conservative framing overheads for dynamic load reserve (overflow-safe).
	agenticSearchCallOutputGroupTokens int64 = 96
	agenticLoadedToolFramingTokens     int64 = 12
	agenticResponsesTurnFramingTokens  int64 = 32
)

// Agentic hard-preflight error codes / sentinels.
var (
	// ErrMandatoryInputTooLarge is returned when mandatory input exceeds the
	// safe ceiling after dynamic reserve. Non-retryable. Never truncate the
	// current user message and never fall back to full tools.
	ErrMandatoryInputTooLarge = errors.New("MODEL_CONTEXT_MANDATORY_TOO_LARGE")
	// ErrDynamicToolReserveExceeded is returned when actual loaded definitions
	// exceed the frozen MaxLoadedToolCount.
	ErrDynamicToolReserveExceeded = errors.New("MODEL_DYNAMIC_TOOL_RESERVE_EXCEEDED")
	// ErrAgenticEstimatorInvalid is returned for invalid Agentic estimator input.
	ErrAgenticEstimatorInvalid = errors.New("invalid agentic estimator input")
)

// ToolMetadata is deferred-visible metadata (name/description/envelope; no parameters).
type ToolMetadata struct {
	Name        string
	Description string
}

// ToolExposureEstimate is the Agentic estimator input (design §9.1).
//
// MaxLoadedToolCount is derived internally as min(deferredCount, 5*8=40).
// Callers cannot lower it: MaxLoadedTools must be 0 (omit) or exactly the
// derived value; any other nonzero value is rejected.
type ToolExposureEstimate struct {
	Immediate        []ToolSchema
	DeferredMetadata []ToolMetadata
	// LoadCandidates are full schemas for deferred tools. Identities must be an
	// exact one-to-one match with DeferredMetadata names (no missing/extra/dup).
	LoadCandidates []ToolSchema
	// MaxLoadedTools is non-user-tunable. 0 means derive; nonzero must equal
	// min(len(DeferredMetadata), AgenticMaxLoadedDefinitionsPerRun).
	MaxLoadedTools int
}

// AgenticEstimateResult is the Agentic-only estimation outcome.
// ToolsTokens is the compatibility sum of the three tool components.
type AgenticEstimateResult struct {
	Profile          string
	TokenizerVersion string
	EstimatorVersion string

	SystemTokens   int64
	MessagesTokens int64
	// ToolsTokens = ImmediateToolsTokens + DeferredMetadataTokens + DynamicToolLoadReserveTokens
	ToolsTokens                  int64
	ImmediateToolsTokens         int64
	DeferredMetadataTokens       int64
	DynamicToolLoadReserveTokens int64
	FramingTokens                int64
	FixedOverhead                int64
	TotalTokens                  int64
	// InitialVisibleTokens is system + messages + immediate + deferred metadata +
	// framing + fixed (excludes dynamic reserve). Used for cache/token comparisons.
	InitialVisibleTokens int64

	ImmediateToolCount int
	DeferredToolCount  int
	MaxLoadedToolCount int
	MessageCount       int
}

// EstimateAgenticRequest estimates a Responses/Agentic request with deferred-aware
// tool accounting. Classic EstimateRequest is unchanged.
//
// MaxIterations is fixed at 8. MaxLoadedToolCount is always derived as
// min(deferredCount, 40) — callers cannot lower the structural bound.
func (e *Estimator) EstimateAgenticRequest(
	system string,
	exposure ToolExposureEstimate,
	messages []Message,
) (AgenticEstimateResult, error) {
	if e == nil || e.tokenizer == nil {
		return AgenticEstimateResult{}, ErrUnavailableProfile
	}
	deferredCount := len(exposure.DeferredMetadata)
	maxLoaded := DeriveMaxLoadedToolCount(deferredCount)
	if exposure.MaxLoadedTools < 0 || exposure.MaxLoadedTools > AgenticMaxLoadedDefinitionsPerRun {
		return AgenticEstimateResult{}, fmt.Errorf("%w: MaxLoadedTools out of range", ErrAgenticEstimatorInvalid)
	}
	// Reject conflicting nonzero input (caller cannot lower/raise the derived bound).
	if exposure.MaxLoadedTools != 0 && exposure.MaxLoadedTools != maxLoaded {
		return AgenticEstimateResult{}, fmt.Errorf(
			"%w: MaxLoadedTools is not user-tunable; expected 0 or derived %d",
			ErrAgenticEstimatorInvalid, maxLoaded,
		)
	}
	// Exact one-to-one deferred metadata ↔ full load candidate identities.
	if err := validateDeferredLoadIdentity(exposure); err != nil {
		return AgenticEstimateResult{}, err
	}

	result := AgenticEstimateResult{
		Profile:            e.tokenizer.Profile(),
		TokenizerVersion:   e.tokenizer.Version(),
		EstimatorVersion:   EstimatorVersionAgenticOpenAIResponsesV1,
		FixedOverhead:      fixedRequestOverhead + tokensPerReplyPriming,
		MessageCount:       len(messages),
		ImmediateToolCount: len(exposure.Immediate),
		DeferredToolCount:  deferredCount,
		MaxLoadedToolCount: maxLoaded,
	}

	sysTokens, err := e.estimateMessage(Message{Role: RoleSystem, Content: system})
	if err != nil {
		return AgenticEstimateResult{}, err
	}
	if strings.TrimSpace(system) != "" {
		result.SystemTokens = sysTokens
	}

	var msgTotal int64
	for _, msg := range messages {
		n, err := e.estimateMessage(msg)
		if err != nil {
			return AgenticEstimateResult{}, err
		}
		msgTotal, err = addInt64(msgTotal, n)
		if err != nil {
			return AgenticEstimateResult{}, err
		}
	}
	result.MessagesTokens = msgTotal

	// Immediate full schemas.
	var immediateTotal int64
	for _, tool := range exposure.Immediate {
		n, err := e.estimateTool(tool)
		if err != nil {
			return AgenticEstimateResult{}, err
		}
		immediateTotal, err = addInt64(immediateTotal, n)
		if err != nil {
			return AgenticEstimateResult{}, err
		}
	}
	result.ImmediateToolsTokens = immediateTotal

	// Deferred visible metadata only (name/description/envelope; no parameters).
	var deferredMetaTotal int64
	for _, meta := range exposure.DeferredMetadata {
		n, err := e.estimateDeferredMetadata(meta)
		if err != nil {
			return AgenticEstimateResult{}, err
		}
		deferredMetaTotal, err = addInt64(deferredMetaTotal, n)
		if err != nil {
			return AgenticEstimateResult{}, err
		}
	}
	result.DeferredMetadataTokens = deferredMetaTotal

	// Dynamic load reserve: worst-case largest deltas + full 8 search groups when deferred>0.
	reserve, err := e.estimateDynamicToolLoadReserve(exposure, maxLoaded)
	if err != nil {
		return AgenticEstimateResult{}, err
	}
	result.DynamicToolLoadReserveTokens = reserve

	toolsSum, err := addInt64(immediateTotal, deferredMetaTotal)
	if err != nil {
		return AgenticEstimateResult{}, err
	}
	toolsSum, err = addInt64(toolsSum, reserve)
	if err != nil {
		return AgenticEstimateResult{}, err
	}
	result.ToolsTokens = toolsSum

	// Framing: system + messages + immediate + deferred metadata envelopes.
	framing := int64(0)
	if strings.TrimSpace(system) != "" {
		framing += tokensPerMessage
	}
	framing, err = addInt64(framing, tokensPerMessage*int64(len(messages)))
	if err != nil {
		return AgenticEstimateResult{}, err
	}
	framing, err = addInt64(framing, tokensPerMessage*int64(len(exposure.Immediate)))
	if err != nil {
		return AgenticEstimateResult{}, err
	}
	framing, err = addInt64(framing, tokensPerMessage*int64(len(exposure.DeferredMetadata)))
	if err != nil {
		return AgenticEstimateResult{}, err
	}
	result.FramingTokens = framing

	// Total includes reserve (conservative preflight).
	total, err := addInt64(result.SystemTokens, result.MessagesTokens)
	if err != nil {
		return AgenticEstimateResult{}, err
	}
	total, err = addInt64(total, result.ToolsTokens)
	if err != nil {
		return AgenticEstimateResult{}, err
	}
	total, err = addInt64(total, result.FixedOverhead)
	if err != nil {
		return AgenticEstimateResult{}, err
	}
	result.TotalTokens = total

	// Initial visible excludes dynamic reserve (provider billable initial input proxy).
	visibleTools, err := addInt64(immediateTotal, deferredMetaTotal)
	if err != nil {
		return AgenticEstimateResult{}, err
	}
	visible, err := addInt64(result.SystemTokens, result.MessagesTokens)
	if err != nil {
		return AgenticEstimateResult{}, err
	}
	visible, err = addInt64(visible, visibleTools)
	if err != nil {
		return AgenticEstimateResult{}, err
	}
	visible, err = addInt64(visible, result.FixedOverhead)
	if err != nil {
		return AgenticEstimateResult{}, err
	}
	result.InitialVisibleTokens = visible
	return result, nil
}

// DeriveMaxLoadedToolCount returns min(deferredCount, 5*8=40). Platform-frozen.
func DeriveMaxLoadedToolCount(deferredCount int) int {
	if deferredCount <= 0 {
		return 0
	}
	if deferredCount > AgenticMaxLoadedDefinitionsPerRun {
		return AgenticMaxLoadedDefinitionsPerRun
	}
	return deferredCount
}

// deferredEnvelopeKey is the canonical identity for metadata↔candidate bijection:
// trimmed name + description (visible envelope). Name-only matching is insufficient.
func deferredEnvelopeKey(name, description string) string {
	return strings.TrimSpace(name) + "\x00" + description
}

// validateDeferredLoadIdentity enforces exact one-to-one identity between
// DeferredMetadata and LoadCandidates by canonical name AND description/visible
// envelope. Missing/extra/duplicate/mismatched fail closed (never invent zero deltas).
func validateDeferredLoadIdentity(exposure ToolExposureEstimate) error {
	meta := exposure.DeferredMetadata
	cands := exposure.LoadCandidates
	// Zero tools: both empty is fine.
	if len(meta) == 0 && len(cands) == 0 {
		return nil
	}
	if len(meta) != len(cands) {
		return fmt.Errorf(
			"%w: deferred metadata count %d != load candidates %d (exact 1:1 required)",
			ErrAgenticEstimatorInvalid, len(meta), len(cands),
		)
	}
	// Index metadata by name; require unique names and store description envelope.
	metaByName := make(map[string]ToolMetadata, len(meta))
	metaKeys := make(map[string]struct{}, len(meta))
	for _, m := range meta {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			return fmt.Errorf("%w: deferred metadata name must be nonempty", ErrAgenticEstimatorInvalid)
		}
		if _, dup := metaByName[name]; dup {
			return fmt.Errorf("%w: duplicate deferred metadata name %q", ErrAgenticEstimatorInvalid, name)
		}
		metaByName[name] = m
		metaKeys[deferredEnvelopeKey(name, m.Description)] = struct{}{}
	}
	candByName := make(map[string]ToolSchema, len(cands))
	for _, c := range cands {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			return fmt.Errorf("%w: load candidate name must be nonempty", ErrAgenticEstimatorInvalid)
		}
		if _, dup := candByName[name]; dup {
			return fmt.Errorf("%w: duplicate load candidate name %q", ErrAgenticEstimatorInvalid, name)
		}
		m, ok := metaByName[name]
		if !ok {
			return fmt.Errorf("%w: load candidate %q has no deferred metadata", ErrAgenticEstimatorInvalid, name)
		}
		// Visible envelope identity: name + description must match exactly.
		if m.Description != c.Description {
			return fmt.Errorf(
				"%w: deferred metadata/candidate description mismatch for %q",
				ErrAgenticEstimatorInvalid, name,
			)
		}
		key := deferredEnvelopeKey(name, c.Description)
		if _, ok := metaKeys[key]; !ok {
			return fmt.Errorf("%w: load candidate envelope mismatch for %q", ErrAgenticEstimatorInvalid, name)
		}
		candByName[name] = c
	}
	for name := range metaByName {
		if _, ok := candByName[name]; !ok {
			return fmt.Errorf("%w: deferred metadata %q missing full load candidate", ErrAgenticEstimatorInvalid, name)
		}
	}
	return nil
}

func (e *Estimator) estimateDeferredMetadata(meta ToolMetadata) (int64, error) {
	var b strings.Builder
	b.WriteString("tool\n")
	b.WriteString(meta.Name)
	b.WriteByte('\n')
	b.WriteString(meta.Description)
	b.WriteByte('\n')
	// Envelope only — no parameters body.
	n, err := e.tokenizer.CountText(b.String())
	if err != nil {
		return 0, err
	}
	n, err = addInt64(n, tokensPerMessage)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// estimateDynamicToolLoadReserve covers cumulative worst case
// min(deferredCount, 5*MaxIterations) largest positive (full-visible) deltas,
// plus — when any deferred tools exist — all 8 client search call/output groups
// and Responses-turn framing (repeated searches can still consume eight turns),
// plus loaded-tool framing for the frozen max-loaded count.
// Does not guess which tools will load. Zero deferred → zero search groups.
func (e *Estimator) estimateDynamicToolLoadReserve(exposure ToolExposureEstimate, maxLoaded int) (int64, error) {
	deferredCount := len(exposure.DeferredMetadata)
	if deferredCount == 0 {
		return 0, nil
	}

	// Index full schemas by name (identity already validated 1:1).
	fullByName := make(map[string]ToolSchema, len(exposure.LoadCandidates))
	for _, t := range exposure.LoadCandidates {
		fullByName[strings.TrimSpace(t.Name)] = t
	}

	type delta struct {
		name  string
		delta int64
	}
	deltas := make([]delta, 0, deferredCount)
	for _, meta := range exposure.DeferredMetadata {
		name := strings.TrimSpace(meta.Name)
		metaTokens, err := e.estimateDeferredMetadata(meta)
		if err != nil {
			return 0, err
		}
		full, ok := fullByName[name]
		if !ok {
			// Defensive: identity validation should have rejected this.
			return 0, fmt.Errorf("%w: missing full schema for %q", ErrAgenticEstimatorInvalid, name)
		}
		fullTokens, err := e.estimateTool(full)
		if err != nil {
			return 0, err
		}
		d := fullTokens - metaTokens
		if d < 0 {
			d = 0 // negative delta clamp
		}
		deltas = append(deltas, delta{name: name, delta: d})
	}

	// Stable sort: delta desc, then name asc for equal deltas.
	sort.SliceStable(deltas, func(i, j int) bool {
		if deltas[i].delta != deltas[j].delta {
			return deltas[i].delta > deltas[j].delta
		}
		return deltas[i].name < deltas[j].name
	})

	take := maxLoaded
	if take > len(deltas) {
		take = len(deltas)
	}
	if take > AgenticMaxLoadedDefinitionsPerRun {
		take = AgenticMaxLoadedDefinitionsPerRun
	}

	var schemaReserve int64
	for i := 0; i < take; i++ {
		var err error
		schemaReserve, err = addInt64(schemaReserve, deltas[i].delta)
		if err != nil {
			return 0, err
		}
	}

	// If any deferred tools exist, reserve all 8 search call/output + turn framing
	// groups regardless of fewer loaded definitions (repeated searches still cost 8 turns).
	numSearchGroups := AgenticMaxIterations

	searchOverhead, err := mulInt64(int64(numSearchGroups), agenticSearchCallOutputGroupTokens)
	if err != nil {
		return 0, err
	}
	loadedFraming, err := mulInt64(int64(take), agenticLoadedToolFramingTokens)
	if err != nil {
		return 0, err
	}
	turnFraming, err := mulInt64(int64(numSearchGroups), agenticResponsesTurnFramingTokens)
	if err != nil {
		return 0, err
	}

	total, err := addInt64(schemaReserve, searchOverhead)
	if err != nil {
		return 0, err
	}
	total, err = addInt64(total, loadedFraming)
	if err != nil {
		return 0, err
	}
	total, err = addInt64(total, turnFraming)
	if err != nil {
		return 0, err
	}
	return total, nil
}

// AgenticPreflightInput is the hard preflight input for Agentic assemblies.
type AgenticPreflightInput struct {
	ModelContextWindowTokens int64
	OutputReserveTokens      int64
	SafetyMarginTokens       int64
	// DynamicReserveTokens is DynamicToolLoadReserveTokens from the estimate.
	DynamicReserveTokens int64
	// MandatoryTokens is system + immediate + deferred metadata + current user
	// (+ framing/fixed) WITHOUT dynamic reserve.
	MandatoryTokens int64
	// MaxLoadedToolCount is the frozen assembly bound.
	MaxLoadedToolCount int
	// ActualLoadedToolCount is optional; when > MaxLoadedToolCount returns reserve exceeded.
	ActualLoadedToolCount int
}

// AgenticPreflightResult is the hard-ceiling preflight outcome.
type AgenticPreflightResult struct {
	SafeInputCeiling int64
	MandatoryTokens  int64
	DynamicReserve   int64
}

// PreflightAgenticMandatory computes safe ceiling and rejects when mandatory
// input exceeds it. Never truncates the current user message.
//
// safe_ceiling = model_context - output_reserve - safety_margin - dynamic_reserve
//
// Structural bounds (typed ErrAgenticEstimatorInvalid):
//   - MaxLoadedToolCount must be in [0, 40] (platform hard max; 0 = no-tools run)
//   - ActualLoadedToolCount must be in [0, 40]
//   - ActualLoadedToolCount must not exceed MaxLoadedToolCount
//
// Zero/no-tools behavior: MaxLoadedToolCount=0 and ActualLoadedToolCount=0 is
// valid (no deferred tools / nothing loaded). ActualLoadedToolCount=0 with a
// positive MaxLoaded is also valid (nothing loaded yet). Values outside [0,40]
// or Actual > Max are rejected before ceiling arithmetic.
//
// When both counts are in range and Actual > Max, returns
// ErrDynamicToolReserveExceeded (stable reserve error).
func PreflightAgenticMandatory(in AgenticPreflightInput) (AgenticPreflightResult, error) {
	if in.ModelContextWindowTokens <= 0 || in.OutputReserveTokens < 0 || in.SafetyMarginTokens < 0 ||
		in.DynamicReserveTokens < 0 || in.MandatoryTokens < 0 {
		return AgenticPreflightResult{}, ErrAgenticEstimatorInvalid
	}
	// Structural bounds: MaxLoaded and ActualLoaded must be in [0, 40].
	const platformMaxLoaded = AgenticMaxLoadedDefinitionsPerRun // 40
	if in.MaxLoadedToolCount < 0 || in.MaxLoadedToolCount > platformMaxLoaded {
		return AgenticPreflightResult{}, ErrAgenticEstimatorInvalid
	}
	if in.ActualLoadedToolCount < 0 || in.ActualLoadedToolCount > platformMaxLoaded {
		return AgenticPreflightResult{}, ErrAgenticEstimatorInvalid
	}
	// Actual must not exceed Max (including when Max is 0 and Actual is positive).
	if in.ActualLoadedToolCount > in.MaxLoadedToolCount {
		return AgenticPreflightResult{}, ErrDynamicToolReserveExceeded
	}

	// ceiling = context - output - safety - dynamic
	ceiling := in.ModelContextWindowTokens
	var err error
	ceiling, err = subInt64(ceiling, in.OutputReserveTokens)
	if err != nil {
		return AgenticPreflightResult{}, err
	}
	ceiling, err = subInt64(ceiling, in.SafetyMarginTokens)
	if err != nil {
		return AgenticPreflightResult{}, err
	}
	ceiling, err = subInt64(ceiling, in.DynamicReserveTokens)
	if err != nil {
		return AgenticPreflightResult{}, err
	}
	if ceiling <= 0 {
		return AgenticPreflightResult{}, ErrMandatoryInputTooLarge
	}
	if in.MandatoryTokens > ceiling {
		return AgenticPreflightResult{}, ErrMandatoryInputTooLarge
	}
	return AgenticPreflightResult{
		SafeInputCeiling: ceiling,
		MandatoryTokens:  in.MandatoryTokens,
		DynamicReserve:   in.DynamicReserveTokens,
	}, nil
}

// PromptCacheKeyInput is the secret-free, non-PII input for design §9.4 keys.
// All fields must already be canonical; no whitespace/case normalization is applied.
type PromptCacheKeyInput struct {
	ProviderProtocol   string
	ModelConfigID      string
	ModelLockVersion   int64
	PromptRevisionHash string
	CatalogDigest      string
	AdapterVersion     string
}

// BuildAgenticPromptCacheKey builds:
//
//	aw:agentic:v1:<sha256(provider_protocol|model_config_id|model_lock_version|prompt_revision_hash|catalog_digest|adapter_version)>
//
// Strict canonical inputs only:
//   - ProviderProtocol must be exactly openai-responses-v1
//   - AdapterVersion must be exactly agenticopenai/v0.2.2
//   - ModelConfigID must be a canonical UUID (not name-like/PII)
//   - ModelLockVersion > 0
//   - PromptRevisionHash and CatalogDigest must be exact lowercase 64-hex
//     (no whitespace/case normalization)
//
// No workspace/run/user/session/name/time components.
func BuildAgenticPromptCacheKey(in PromptCacheKeyInput) (string, error) {
	if in.ProviderProtocol != PromptCacheProviderProtocolOpenAIResponsesV1 {
		return "", fmt.Errorf("%w: provider protocol must be exactly %q", ErrAgenticEstimatorInvalid, PromptCacheProviderProtocolOpenAIResponsesV1)
	}
	if in.AdapterVersion != PromptCacheAdapterAgenticOpenAIV022 {
		return "", fmt.Errorf("%w: adapter version must be exactly %q", ErrAgenticEstimatorInvalid, PromptCacheAdapterAgenticOpenAIV022)
	}
	if in.ModelLockVersion < 1 {
		return "", fmt.Errorf("%w: model lock version must be >= 1", ErrAgenticEstimatorInvalid)
	}
	if !isCanonicalUUID(in.ModelConfigID) {
		return "", fmt.Errorf("%w: model_config_id must be a canonical UUID", ErrAgenticEstimatorInvalid)
	}
	if !isExactLowerHex64(in.PromptRevisionHash) {
		return "", fmt.Errorf("%w: prompt_revision_hash must be exact lowercase 64-hex", ErrAgenticEstimatorInvalid)
	}
	if !isExactLowerHex64(in.CatalogDigest) {
		return "", fmt.Errorf("%w: catalog_digest must be exact lowercase 64-hex", ErrAgenticEstimatorInvalid)
	}
	payload := strings.Join([]string{
		in.ProviderProtocol,
		in.ModelConfigID,
		fmt.Sprintf("%d", in.ModelLockVersion),
		in.PromptRevisionHash,
		in.CatalogDigest,
		in.AdapterVersion,
	}, "|")
	sum := sha256.Sum256([]byte(payload))
	return "aw:agentic:v1:" + hex.EncodeToString(sum[:]), nil
}

func isExactLowerHex64(v string) bool {
	if len(v) != 64 {
		return false
	}
	// Reject any whitespace or case variants — no normalization.
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func isCanonicalUUID(v string) bool {
	// Canonical UUID form only (google/uuid Parse accepts many; require hyphenated
	// lowercase 8-4-4-4-12 that round-trips via Parse + String).
	id, err := uuid.Parse(v)
	if err != nil {
		return false
	}
	return id.String() == v
}

// Overflow-safe arithmetic helpers.
func addInt64(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, fmt.Errorf("%w: add overflow", ErrBudgetOverflow)
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, fmt.Errorf("%w: add underflow", ErrBudgetOverflow)
	}
	return a + b, nil
}

func subInt64(a, b int64) (int64, error) {
	if b > 0 && a < math.MinInt64+b {
		return 0, fmt.Errorf("%w: sub underflow", ErrBudgetOverflow)
	}
	if b < 0 && a > math.MaxInt64+b {
		return 0, fmt.Errorf("%w: sub overflow", ErrBudgetOverflow)
	}
	return a - b, nil
}

func mulInt64(a, b int64) (int64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}
	if a > 0 {
		if b > 0 {
			if a > math.MaxInt64/b {
				return 0, fmt.Errorf("%w: mul overflow", ErrBudgetOverflow)
			}
		} else if b < math.MinInt64/a {
			return 0, fmt.Errorf("%w: mul underflow", ErrBudgetOverflow)
		}
	} else {
		if b > 0 {
			if a < math.MinInt64/b {
				return 0, fmt.Errorf("%w: mul underflow", ErrBudgetOverflow)
			}
		} else if a != 0 && b < math.MaxInt64/a {
			return 0, fmt.Errorf("%w: mul overflow", ErrBudgetOverflow)
		}
	}
	return a * b, nil
}
