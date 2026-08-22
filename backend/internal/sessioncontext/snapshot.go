package sessioncontext

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrUnsupportedSnapshot is returned for explicit unknown snapshot schema versions.
	ErrUnsupportedSnapshot = errors.New("CONTEXT_SNAPSHOT_UNSUPPORTED")
	// ErrInvalidSnapshot is returned for malformed recognized snapshots.
	ErrInvalidSnapshot = errors.New("invalid context snapshot")
)

const (
	// SnapshotSchemaV1 is the fully resolved context policy snapshot on agent runs (ZKL-74).
	SnapshotSchemaV1 = "session-context.v1"
	// SnapshotSchemaV2 adds LLM compact knobs, AAP disclosure, and compaction gate sources (ZKL-81).
	SnapshotSchemaV2 = "session-context.v2"

	// Platform-frozen 80%/60% basis points — not configurable by Agent/workspace/request.
	TriggerBps = int64(8000)
	TargetBps  = int64(6000)

	// Default delay budgets (T5-A).
	DefaultTotalTimeoutMs   = int64(45000)
	DefaultPerPassTimeoutMs = int64(20000)
	DefaultClaimWaitMs      = int64(1000)

	// Default compact template identity until IC-06 pins content hash.
	DefaultCompactionTemplateVersion = "context-compaction.v1"
)

// ResolvedSnapshot is the immutable run-level session-context document (v1 or v2).
type ResolvedSnapshot struct {
	SchemaVersion            string          `json:"schemaVersion"`
	Mode                     string          `json:"mode"`
	ModelContextWindowTokens int64           `json:"modelContextWindowTokens"`
	EffectiveMaxInputTokens  int64           `json:"effectiveMaxInputTokens"`
	OutputReserveTokens      int64           `json:"outputReserveTokens"`
	SafetyMarginTokens       int64           `json:"safetyMarginTokens"`
	MaxRecentTurns           int64           `json:"maxRecentTurns"`
	TokenizerProfile         string          `json:"tokenizerProfile"`
	TokenizerVersion         string          `json:"tokenizerVersion"`
	OutputTokenLimitMode     string          `json:"outputTokenLimitMode"`
	Summary                  json.RawMessage `json:"summary"`
	// Compaction is present only on v2 (platform-frozen 80/60 + timeouts + template).
	Compaction *CompactionSnapshot `json:"compaction,omitempty"`
	// AAP disclosure is present only on v2; default false.
	AAP     *AAPSnapshot    `json:"aap,omitempty"`
	Sources SnapshotSources `json:"sources"`
}

// CompactionSnapshot freezes compact algorithm and delay budgets for a run.
type CompactionSnapshot struct {
	TriggerBps          int64  `json:"triggerBps"`
	TargetBps           int64  `json:"targetBps"`
	MaxSummaryTokens    int64  `json:"maxSummaryTokens"`
	MinEvictedTurns     int64  `json:"minEvictedTurns"`
	MaxGenerationPasses int64  `json:"maxGenerationPasses"`
	TemplateVersion     string `json:"templateVersion"`
	TemplateHash        string `json:"templateHash"`
	TotalTimeoutMs      int64  `json:"totalTimeoutMs"`
	PerPassTimeoutMs    int64  `json:"perPassTimeoutMs"`
	ClaimWaitMs         int64  `json:"claimWaitMs"`
}

// AAPSnapshot freezes AAP disclosure / capability flags for a run (v2).
type AAPSnapshot struct {
	IncludeCompactionSummary bool `json:"includeCompactionSummary"`
	// EnableA2UI freezes whether the run may emit additive a2ui content parts.
	EnableA2UI bool `json:"enableA2UI"`
	// EnableOutboundAttachments freezes output_file capability. omitempty so
	// false is absent on existing A2UI/compaction v2 snapshots.
	EnableOutboundAttachments bool `json:"enableOutboundAttachments,omitempty"`
	// EnableInboundRead freezes actweave.read_attachment. omitempty so false
	// is absent on existing A2UI/compaction/outbound v2 snapshots.
	EnableInboundRead bool `json:"enableInboundRead,omitempty"`
}

// SnapshotSources records which policy layers contributed to the resolved snapshot.
type SnapshotSources struct {
	WorkspacePolicyVersion   int64  `json:"workspacePolicyVersion,omitempty"`
	AgentPolicyVersion       int64  `json:"agentPolicyVersion,omitempty"`
	RolloutVersion           string `json:"rolloutVersion,omitempty"`
	GateEnabled              bool   `json:"gateEnabled"`
	CompactionGateEnabled    bool   `json:"compactionGateEnabled,omitempty"`
	CompactionRolloutVersion string `json:"compactionRolloutVersion,omitempty"`
}

// Legacy modes for D5-A.
const (
	ModeLegacy = "legacy"
)

// ParseResolvedSnapshot classifies a stored context_policy_snapshot.
// - {} / empty / known legacy placeholders → legacy (ok, ModeLegacy)
// - session-context.v1 / session-context.v2 → validated ResolvedSnapshot
// - explicit unknown schemaVersion → ErrUnsupportedSnapshot
func ParseResolvedSnapshot(raw json.RawMessage) (ResolvedSnapshot, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("{}")) {
		return ResolvedSnapshot{SchemaVersion: "", Mode: ModeLegacy}, nil
	}
	if !json.Valid(raw) {
		return ResolvedSnapshot{}, ErrInvalidSnapshot
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		return ResolvedSnapshot{}, ErrInvalidSnapshot
	}
	// Known unversioned historical placeholders (memory/maxTurns) → legacy.
	if _, hasSchema := top["schemaVersion"]; !hasSchema {
		if isRecognizedLegacyPlaceholder(top) {
			return ResolvedSnapshot{SchemaVersion: "", Mode: ModeLegacy}, nil
		}
		// Empty-like object already handled; other unversioned objects are legacy for safety.
		return ResolvedSnapshot{SchemaVersion: "", Mode: ModeLegacy}, nil
	}
	var schema string
	if err := json.Unmarshal(top["schemaVersion"], &schema); err != nil {
		return ResolvedSnapshot{}, ErrInvalidSnapshot
	}
	schema = strings.TrimSpace(schema)
	switch schema {
	case SnapshotSchemaV1, SnapshotSchemaV2:
	default:
		return ResolvedSnapshot{}, fmt.Errorf("%w: %s", ErrUnsupportedSnapshot, schema)
	}
	var doc ResolvedSnapshot
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return ResolvedSnapshot{}, ErrInvalidSnapshot
	}
	if doc.ModelContextWindowTokens <= 0 || doc.EffectiveMaxInputTokens <= 0 {
		return ResolvedSnapshot{}, ErrInvalidSnapshot
	}
	if doc.OutputReserveTokens < 0 || doc.SafetyMarginTokens < 0 || doc.MaxRecentTurns < 0 {
		return ResolvedSnapshot{}, ErrInvalidSnapshot
	}
	switch doc.Mode {
	case ModeTokenWindow, ModeRollingSummary, ModeDisabled:
	default:
		return ResolvedSnapshot{}, ErrInvalidSnapshot
	}
	if schema == SnapshotSchemaV1 {
		// v1 must not carry compaction/aap; tolerate omitempty nil.
		if doc.Compaction != nil || doc.AAP != nil {
			return ResolvedSnapshot{}, ErrInvalidSnapshot
		}
		return doc, nil
	}
	// v2: compaction block required with frozen 80/60 and positive timeouts.
	if doc.Compaction == nil {
		return ResolvedSnapshot{}, ErrInvalidSnapshot
	}
	if doc.Compaction.TriggerBps != TriggerBps || doc.Compaction.TargetBps != TargetBps {
		return ResolvedSnapshot{}, ErrInvalidSnapshot
	}
	if doc.Compaction.TotalTimeoutMs <= 0 || doc.Compaction.PerPassTimeoutMs <= 0 ||
		doc.Compaction.ClaimWaitMs <= 0 || doc.Compaction.MaxSummaryTokens <= 0 ||
		doc.Compaction.MaxGenerationPasses <= 0 ||
		strings.TrimSpace(doc.Compaction.TemplateVersion) == "" ||
		len(strings.TrimSpace(doc.Compaction.TemplateHash)) != 64 {
		return ResolvedSnapshot{}, ErrInvalidSnapshot
	}
	if doc.AAP == nil {
		doc.AAP = &AAPSnapshot{IncludeCompactionSummary: false, EnableA2UI: false}
	}
	return doc, nil
}

// IsLegacySnapshot reports whether the run should use the historical full-history path.
func IsLegacySnapshot(raw json.RawMessage) bool {
	doc, err := ParseResolvedSnapshot(raw)
	if err != nil {
		return false
	}
	return doc.Mode == ModeLegacy || doc.SchemaVersion == ""
}

// IsV2Snapshot reports whether the run freezes LLM compact knobs.
func IsV2Snapshot(raw json.RawMessage) bool {
	doc, err := ParseResolvedSnapshot(raw)
	if err != nil {
		return false
	}
	return doc.SchemaVersion == SnapshotSchemaV2
}

// IncludeCompactionSummaryFromSnapshot returns frozen T4-B disclosure (false if absent/legacy/v1).
func IncludeCompactionSummaryFromSnapshot(raw json.RawMessage) bool {
	doc, err := ParseResolvedSnapshot(raw)
	if err != nil || doc.AAP == nil {
		return false
	}
	return doc.AAP.IncludeCompactionSummary
}

// EnableA2UIFromSnapshot returns frozen A2UI capability (false if absent/legacy/v1/err).
func EnableA2UIFromSnapshot(raw json.RawMessage) bool {
	doc, err := ParseResolvedSnapshot(raw)
	if err != nil || doc.AAP == nil {
		return false
	}
	return doc.AAP.EnableA2UI
}

// EnableOutboundAttachmentsFromSnapshot returns frozen outbound-attachment
// capability (false if absent/legacy/v1/err).
func EnableOutboundAttachmentsFromSnapshot(raw json.RawMessage) bool {
	doc, err := ParseResolvedSnapshot(raw)
	if err != nil || doc.AAP == nil {
		return false
	}
	return doc.AAP.EnableOutboundAttachments
}

// EnableInboundReadFromSnapshot returns frozen inbound-read capability
// (false if absent/legacy/v1/err).
func EnableInboundReadFromSnapshot(raw json.RawMessage) bool {
	doc, err := ParseResolvedSnapshot(raw)
	if err != nil || doc.AAP == nil {
		return false
	}
	return doc.AAP.EnableInboundRead
}

func isRecognizedLegacyPlaceholder(top map[string]json.RawMessage) bool {
	// Historical test fixtures used memory/maxTurns without schemaVersion.
	if len(top) == 0 {
		return true
	}
	for key := range top {
		switch key {
		case "memory", "maxTurns":
		default:
			return false
		}
	}
	return true
}

// PlatformDefaultPolicy is the fail-closed platform default when no workspace/agent patch is set.
func PlatformDefaultPolicy() PolicyDocument {
	zero := int64(0)
	reserve := int64(4096)
	margin := int64(2048)
	return PolicyDocument{
		SchemaVersion:       PolicySchemaV1,
		Mode:                ModeTokenWindow,
		MaxInputTokens:      &zero,
		OutputReserveTokens: &reserve,
		SafetyMarginTokens:  &margin,
		MaxRecentTurns:      &zero,
	}
}

// ResolveInput feeds the hierarchical policy resolver.
type ResolveInput struct {
	WorkspacePolicy json.RawMessage
	AgentPolicy     json.RawMessage
	// InternalOverride is reserved for tests/migrations; never from public APIs in v1.
	InternalOverride json.RawMessage
	// Model capabilities (already parsed). Required for non-legacy resolution.
	ContextWindowTokens        int64
	DefaultOutputReserveTokens int64
	OutputTokenLimitMode       string
	TokenizerProfile           string
	TokenizerVersion           string
	WorkspaceLockVersion       int64
	AgentLockVersion           int64
	RolloutVersion             string
	GateEnabled                bool
	// Compaction gate is evaluated only at run creation; when true, emit session-context.v2.
	CompactionGateEnabled    bool
	CompactionRolloutVersion string
	// Optional override for template hash (tests). Empty → derived from template version.
	CompactionTemplateHash string
}

// Resolve merges policy layers and clamps against model hard capabilities.
// Priority: system hard constraints > internal override > agent > workspace > platform default.
// When CompactionGateEnabled, writes session-context.v2 with frozen 80/60 and delay budgets.
// enableA2UI or enableOutboundAttachments also emit full v2 with platform-default compaction
// and sources.compactionGateEnabled=false (compact runtime stays off).
func Resolve(input ResolveInput) (ResolvedSnapshot, json.RawMessage, error) {
	if input.ContextWindowTokens <= 0 || input.DefaultOutputReserveTokens <= 0 ||
		input.DefaultOutputReserveTokens >= input.ContextWindowTokens ||
		strings.TrimSpace(input.TokenizerProfile) == "" ||
		strings.TrimSpace(input.TokenizerVersion) == "" ||
		strings.TrimSpace(input.OutputTokenLimitMode) == "" {
		return ResolvedSnapshot{}, nil, errors.New("model runtime capabilities incomplete")
	}

	merged := PlatformDefaultPolicy()
	var agentDoc *PolicyDocument
	if patch, err := optionalPolicy(input.WorkspacePolicy, PolicyScopeWorkspace); err != nil {
		return ResolvedSnapshot{}, nil, err
	} else if patch != nil {
		merged = applyPatch(merged, *patch)
	}
	if patch, err := optionalPolicy(input.AgentPolicy, PolicyScopeAgent); err != nil {
		return ResolvedSnapshot{}, nil, err
	} else if patch != nil {
		agentDoc = patch
		merged = applyPatch(merged, *patch)
	}
	if patch, err := optionalPolicy(input.InternalOverride, PolicyScopeAgent); err != nil {
		return ResolvedSnapshot{}, nil, err
	} else if patch != nil {
		merged = applyPatch(merged, *patch)
	}

	mode := merged.Mode
	if mode == "" {
		mode = ModeTokenWindow
	}
	reserve := input.DefaultOutputReserveTokens
	if merged.OutputReserveTokens != nil && *merged.OutputReserveTokens > 0 {
		// Policy may only tighten reserve (raise floor).
		if *merged.OutputReserveTokens > reserve {
			reserve = *merged.OutputReserveTokens
		}
	}
	if reserve >= input.ContextWindowTokens {
		reserve = input.ContextWindowTokens - 1
	}
	margin := int64(2048)
	if merged.SafetyMarginTokens != nil && *merged.SafetyMarginTokens >= 0 {
		margin = *merged.SafetyMarginTokens
	}
	maxRecent := int64(0)
	if merged.MaxRecentTurns != nil && *merged.MaxRecentTurns >= 0 {
		maxRecent = *merged.MaxRecentTurns
	}

	// hardInputCeiling = window - reserve - margin
	hardCeiling := input.ContextWindowTokens - reserve - margin
	if hardCeiling <= 0 {
		return ResolvedSnapshot{}, nil, errors.New("effective input budget non-positive")
	}
	effective := hardCeiling
	if merged.MaxInputTokens != nil && *merged.MaxInputTokens > 0 && *merged.MaxInputTokens < effective {
		effective = *merged.MaxInputTokens
	}

	var summary json.RawMessage
	if merged.Summary != nil {
		encoded, err := json.Marshal(merged.Summary)
		if err != nil {
			return ResolvedSnapshot{}, nil, err
		}
		summary = encoded
	} else {
		summary = json.RawMessage(`null`)
	}

	include := false
	enableA2UI := false
	enableOutbound := false
	enableInbound := false
	if agentDoc != nil {
		include = agentDoc.IncludeCompactionSummary()
		enableA2UI = agentDoc.EnableA2UI()
		enableOutbound = agentDoc.EnableOutboundAttachments()
		enableInbound = agentDoc.EnableInboundRead()
	}

	// Emit v2 when compaction is on or when A2UI / outbound / inbound-read need an aap freeze.
	// ParseResolvedSnapshot still requires a full compaction block on every v2 snapshot.
	schema := SnapshotSchemaV1
	var compaction *CompactionSnapshot
	var aap *AAPSnapshot
	if input.CompactionGateEnabled || enableA2UI || enableOutbound || enableInbound {
		schema = SnapshotSchemaV2
		// Platform defaults for summary knobs; agent summary tightens when present.
		// Gate-off + enableA2UI uses the same construction (platform defaults when no summary).
		maxSummary := int64(2048)
		minEvicted := int64(4)
		maxPasses := int64(2)
		if merged.Summary != nil {
			if merged.Summary.MaxTokens != nil && *merged.Summary.MaxTokens > 0 {
				maxSummary = *merged.Summary.MaxTokens
			}
			if merged.Summary.MinEvictedTurns != nil && *merged.Summary.MinEvictedTurns >= 0 {
				minEvicted = *merged.Summary.MinEvictedTurns
			}
			if merged.Summary.MaxGenerationPasses != nil && *merged.Summary.MaxGenerationPasses > 0 {
				maxPasses = *merged.Summary.MaxGenerationPasses
			}
		}
		templateHash := strings.TrimSpace(input.CompactionTemplateHash)
		if templateHash == "" {
			templateHash = DefaultCompactionTemplateHash()
		}
		compaction = &CompactionSnapshot{
			TriggerBps:          TriggerBps,
			TargetBps:           TargetBps,
			MaxSummaryTokens:    maxSummary,
			MinEvictedTurns:     minEvicted,
			MaxGenerationPasses: maxPasses,
			TemplateVersion:     DefaultCompactionTemplateVersion,
			TemplateHash:        templateHash,
			TotalTimeoutMs:      DefaultTotalTimeoutMs,
			PerPassTimeoutMs:    DefaultPerPassTimeoutMs,
			ClaimWaitMs:         DefaultClaimWaitMs,
		}
		aap = &AAPSnapshot{
			IncludeCompactionSummary: include,
			EnableA2UI:               enableA2UI,
		}
		if enableOutbound {
			aap.EnableOutboundAttachments = true
		}
		if enableInbound {
			aap.EnableInboundRead = true
		}
	}

	doc := ResolvedSnapshot{
		SchemaVersion:            schema,
		Mode:                     mode,
		ModelContextWindowTokens: input.ContextWindowTokens,
		EffectiveMaxInputTokens:  effective,
		OutputReserveTokens:      reserve,
		SafetyMarginTokens:       margin,
		MaxRecentTurns:           maxRecent,
		TokenizerProfile:         input.TokenizerProfile,
		TokenizerVersion:         input.TokenizerVersion,
		OutputTokenLimitMode:     input.OutputTokenLimitMode,
		Summary:                  summary,
		Compaction:               compaction,
		AAP:                      aap,
		Sources: SnapshotSources{
			WorkspacePolicyVersion: input.WorkspaceLockVersion,
			AgentPolicyVersion:     input.AgentLockVersion,
			RolloutVersion:         input.RolloutVersion,
			GateEnabled:            input.GateEnabled,
			// Compact runtime still keys off this flag — placeholder compaction must not flip it on.
			CompactionGateEnabled:    input.CompactionGateEnabled,
			CompactionRolloutVersion: strings.TrimSpace(input.CompactionRolloutVersion),
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return ResolvedSnapshot{}, nil, err
	}
	return doc, raw, nil
}

// DefaultCompactionTemplateHash is the platform hash of the default template version string.
func DefaultCompactionTemplateHash() string {
	sum := sha256.Sum256([]byte(DefaultCompactionTemplateVersion + "|platform"))
	return hex.EncodeToString(sum[:])
}

func optionalPolicy(raw json.RawMessage, scope PolicyScope) (*PolicyDocument, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("{}")) {
		return nil, nil
	}
	doc, _, err := ParsePolicyScoped(raw, scope)
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

func applyPatch(base PolicyDocument, patch PolicyDocument) PolicyDocument {
	if patch.Mode != "" {
		base.Mode = patch.Mode
	}
	if patch.MaxInputTokens != nil {
		base.MaxInputTokens = patch.MaxInputTokens
	}
	if patch.OutputReserveTokens != nil {
		base.OutputReserveTokens = patch.OutputReserveTokens
	}
	if patch.SafetyMarginTokens != nil {
		base.SafetyMarginTokens = patch.SafetyMarginTokens
	}
	if patch.MaxRecentTurns != nil {
		base.MaxRecentTurns = patch.MaxRecentTurns
	}
	if patch.Summary != nil {
		base.Summary = patch.Summary
	}
	if patch.AAP != nil {
		base.AAP = patch.AAP
	}
	// Prefer higher schema version when patch is explicit v2.
	if patch.SchemaVersion == PolicySchemaV2 {
		base.SchemaVersion = PolicySchemaV2
	}
	return base
}
