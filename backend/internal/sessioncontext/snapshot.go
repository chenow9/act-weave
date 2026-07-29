package sessioncontext

import (
	"bytes"
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
	// SnapshotSchemaV1 is the fully resolved context policy snapshot on agent runs.
	SnapshotSchemaV1 = "session-context.v1"
)

// ResolvedSnapshot is the immutable run-level session-context.v1 document.
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
	Sources                  SnapshotSources `json:"sources"`
}

// SnapshotSources records which policy layers contributed to the resolved snapshot.
type SnapshotSources struct {
	WorkspacePolicyVersion int64  `json:"workspacePolicyVersion,omitempty"`
	AgentPolicyVersion     int64  `json:"agentPolicyVersion,omitempty"`
	RolloutVersion         string `json:"rolloutVersion,omitempty"`
	GateEnabled            bool   `json:"gateEnabled"`
}

// Legacy modes for D5-A.
const (
	ModeLegacy = "legacy"
)

// ParseResolvedSnapshot classifies a stored context_policy_snapshot.
// - {} / empty / known legacy placeholders → legacy (ok, ModeLegacy)
// - session-context.v1 → validated ResolvedSnapshot
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
	if schema != SnapshotSchemaV1 {
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
}

// Resolve merges policy layers and clamps against model hard capabilities.
// Priority: system hard constraints > internal override > agent > workspace > platform default.
func Resolve(input ResolveInput) (ResolvedSnapshot, json.RawMessage, error) {
	if input.ContextWindowTokens <= 0 || input.DefaultOutputReserveTokens <= 0 ||
		input.DefaultOutputReserveTokens >= input.ContextWindowTokens ||
		strings.TrimSpace(input.TokenizerProfile) == "" ||
		strings.TrimSpace(input.TokenizerVersion) == "" ||
		strings.TrimSpace(input.OutputTokenLimitMode) == "" {
		return ResolvedSnapshot{}, nil, errors.New("model runtime capabilities incomplete")
	}

	merged := PlatformDefaultPolicy()
	if patch, err := optionalPolicy(input.WorkspacePolicy); err != nil {
		return ResolvedSnapshot{}, nil, err
	} else if patch != nil {
		merged = applyPatch(merged, *patch)
	}
	if patch, err := optionalPolicy(input.AgentPolicy); err != nil {
		return ResolvedSnapshot{}, nil, err
	} else if patch != nil {
		merged = applyPatch(merged, *patch)
	}
	if patch, err := optionalPolicy(input.InternalOverride); err != nil {
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
		// Policy may only tighten reserve (raise floor), not lower below model default? 
		// Design: "配置只允许收紧模型硬能力" — for reserve, using max(modelDefault, policy) is safer.
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

	doc := ResolvedSnapshot{
		SchemaVersion:            SnapshotSchemaV1,
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
		Sources: SnapshotSources{
			WorkspacePolicyVersion: input.WorkspaceLockVersion,
			AgentPolicyVersion:     input.AgentLockVersion,
			RolloutVersion:         input.RolloutVersion,
			GateEnabled:            input.GateEnabled,
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return ResolvedSnapshot{}, nil, err
	}
	return doc, raw, nil
}

func optionalPolicy(raw json.RawMessage) (*PolicyDocument, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("{}")) {
		return nil, nil
	}
	doc, _, err := ParsePolicy(raw)
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
	return base
}
