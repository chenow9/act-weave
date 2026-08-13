// Package sessioncontext holds versioned session-context policy documents
// shared by workspace and agent configuration (ZKL-74 / ZKL-81).
package sessioncontext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrInvalidPolicy is returned for unknown fields, versions, or illegal budgets.
	ErrInvalidPolicy = errors.New("invalid session context policy")
)

const (
	PolicySchemaV1 = "session-context-policy.v1"
	PolicySchemaV2 = "session-context-policy.v2"

	ModeTokenWindow    = "token_window"
	ModeRollingSummary = "rolling_summary"
	ModeDisabled       = "disabled"
)

// PolicyScope selects agent vs workspace validation rules.
type PolicyScope int

const (
	// PolicyScopeAgent allows aap.includeCompactionSummary and aap.enableA2UI (v2).
	PolicyScopeAgent PolicyScope = iota
	// PolicyScopeWorkspace rejects aap disclosure fields.
	PolicyScopeWorkspace
)

// PolicyDocument is the patch document stored on workspace/agent context_policy.
// Zero / omitted numeric fields mean inherit; maxInputTokens/maxRecentTurns of 0
// mean "no extra clamp" when the document is fully set.
type PolicyDocument struct {
	SchemaVersion       string         `json:"schemaVersion,omitempty"`
	Mode                string         `json:"mode,omitempty"`
	MaxInputTokens      *int64         `json:"maxInputTokens,omitempty"`
	OutputReserveTokens *int64         `json:"outputReserveTokens,omitempty"`
	SafetyMarginTokens  *int64         `json:"safetyMarginTokens,omitempty"`
	MaxRecentTurns      *int64         `json:"maxRecentTurns,omitempty"`
	Summary             *SummaryPolicy `json:"summary,omitempty"`
	// AAP is Agent-only (policy v2). Workspace documents must not set it.
	AAP *AAPPolicy `json:"aap,omitempty"`
}

// SummaryPolicy holds optional rolling-summary knobs.
type SummaryPolicy struct {
	MaxTokens           *int64 `json:"maxTokens,omitempty"`
	MinEvictedTurns     *int64 `json:"minEvictedTurns,omitempty"`
	MaxGenerationPasses *int64 `json:"maxGenerationPasses,omitempty"`
}

// AAPPolicy is Agent-level AAP disclosure / capability flags (v2, agent-only).
// Missing includeCompactionSummary / enableA2UI normalize to false when aap is present.
type AAPPolicy struct {
	IncludeCompactionSummary *bool `json:"includeCompactionSummary,omitempty"`
	// EnableA2UI allows the agent to emit additive a2ui content parts (default false).
	EnableA2UI *bool `json:"enableA2UI,omitempty"`
}

// ParsePolicy validates and normalizes a raw JSON object as an Agent policy
// (allows aap on v2). Prefer ParsePolicyScoped for workspace.
func ParsePolicy(raw json.RawMessage) (PolicyDocument, json.RawMessage, error) {
	return ParsePolicyScoped(raw, PolicyScopeAgent)
}

// ParsePolicyScoped validates policy with Agent vs Workspace field rules.
func ParsePolicyScoped(raw json.RawMessage, scope PolicyScope) (PolicyDocument, json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return PolicyDocument{}, json.RawMessage(`{}`), nil
	}
	if !json.Valid(raw) {
		return PolicyDocument{}, nil, ErrInvalidPolicy
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		return PolicyDocument{}, nil, ErrInvalidPolicy
	}
	if len(top) == 0 {
		return PolicyDocument{}, json.RawMessage(`{}`), nil
	}

	allowed := map[string]struct{}{
		"schemaVersion":       {},
		"mode":                {},
		"maxInputTokens":      {},
		"outputReserveTokens": {},
		"safetyMarginTokens":  {},
		"maxRecentTurns":      {},
		"summary":             {},
		"aap":                 {},
	}
	for key := range top {
		if _, ok := allowed[key]; !ok {
			return PolicyDocument{}, nil, fmt.Errorf("%w: unknown field %q", ErrInvalidPolicy, key)
		}
	}
	// Workspace baseline must never carry AAP disclosure.
	if scope == PolicyScopeWorkspace {
		if _, ok := top["aap"]; ok {
			return PolicyDocument{}, nil, fmt.Errorf("%w: aap is agent-only", ErrInvalidPolicy)
		}
	}
	if summaryRaw, ok := top["summary"]; ok && !bytes.Equal(bytes.TrimSpace(summaryRaw), []byte("null")) {
		var summaryObj map[string]json.RawMessage
		if err := json.Unmarshal(summaryRaw, &summaryObj); err != nil || summaryObj == nil {
			return PolicyDocument{}, nil, fmt.Errorf("%w: summary must be an object", ErrInvalidPolicy)
		}
		summaryAllowed := map[string]struct{}{
			"maxTokens":           {},
			"minEvictedTurns":     {},
			"maxGenerationPasses": {},
		}
		for key := range summaryObj {
			if _, ok := summaryAllowed[key]; !ok {
				return PolicyDocument{}, nil, fmt.Errorf("%w: unknown summary field %q", ErrInvalidPolicy, key)
			}
		}
	}
	if aapRaw, ok := top["aap"]; ok && !bytes.Equal(bytes.TrimSpace(aapRaw), []byte("null")) {
		var aapObj map[string]json.RawMessage
		if err := json.Unmarshal(aapRaw, &aapObj); err != nil || aapObj == nil {
			return PolicyDocument{}, nil, fmt.Errorf("%w: aap must be an object", ErrInvalidPolicy)
		}
		aapAllowed := map[string]struct{}{
			"includeCompactionSummary": {},
			"enableA2UI":               {},
		}
		for key := range aapObj {
			if _, ok := aapAllowed[key]; !ok {
				return PolicyDocument{}, nil, fmt.Errorf("%w: unknown aap field %q", ErrInvalidPolicy, key)
			}
		}
	}

	var doc PolicyDocument
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return PolicyDocument{}, nil, ErrInvalidPolicy
	}

	doc.SchemaVersion = strings.TrimSpace(doc.SchemaVersion)
	doc.Mode = strings.TrimSpace(doc.Mode)
	switch doc.SchemaVersion {
	case PolicySchemaV1, PolicySchemaV2:
	default:
		return PolicyDocument{}, nil, fmt.Errorf("%w: unsupported schemaVersion", ErrInvalidPolicy)
	}
	// aap only legal on v2 agent policy.
	if doc.AAP != nil {
		if doc.SchemaVersion != PolicySchemaV2 {
			return PolicyDocument{}, nil, fmt.Errorf("%w: aap requires schemaVersion %s", ErrInvalidPolicy, PolicySchemaV2)
		}
		if scope != PolicyScopeAgent {
			return PolicyDocument{}, nil, fmt.Errorf("%w: aap is agent-only", ErrInvalidPolicy)
		}
	}
	// v1 must not smuggle aap via empty object after decode.
	if doc.SchemaVersion == PolicySchemaV1 {
		doc.AAP = nil
	}
	if doc.Mode != "" {
		switch doc.Mode {
		case ModeTokenWindow, ModeRollingSummary, ModeDisabled:
		default:
			return PolicyDocument{}, nil, fmt.Errorf("%w: unsupported mode", ErrInvalidPolicy)
		}
	}
	if doc.MaxInputTokens != nil && *doc.MaxInputTokens < 0 {
		return PolicyDocument{}, nil, fmt.Errorf("%w: maxInputTokens must be >= 0", ErrInvalidPolicy)
	}
	if doc.OutputReserveTokens != nil && *doc.OutputReserveTokens < 0 {
		return PolicyDocument{}, nil, fmt.Errorf("%w: outputReserveTokens must be >= 0", ErrInvalidPolicy)
	}
	if doc.SafetyMarginTokens != nil && *doc.SafetyMarginTokens < 0 {
		return PolicyDocument{}, nil, fmt.Errorf("%w: safetyMarginTokens must be >= 0", ErrInvalidPolicy)
	}
	if doc.MaxRecentTurns != nil && *doc.MaxRecentTurns < 0 {
		return PolicyDocument{}, nil, fmt.Errorf("%w: maxRecentTurns must be >= 0", ErrInvalidPolicy)
	}
	if doc.Summary != nil {
		if doc.Summary.MaxTokens != nil && *doc.Summary.MaxTokens <= 0 {
			return PolicyDocument{}, nil, fmt.Errorf("%w: summary.maxTokens must be > 0", ErrInvalidPolicy)
		}
		if doc.Summary.MinEvictedTurns != nil && *doc.Summary.MinEvictedTurns < 0 {
			return PolicyDocument{}, nil, fmt.Errorf("%w: summary.minEvictedTurns must be >= 0", ErrInvalidPolicy)
		}
		if doc.Summary.MaxGenerationPasses != nil && *doc.Summary.MaxGenerationPasses <= 0 {
			return PolicyDocument{}, nil, fmt.Errorf("%w: summary.maxGenerationPasses must be > 0", ErrInvalidPolicy)
		}
	}

	// When both are set, reserve cannot exceed max input if max is a positive clamp.
	if doc.MaxInputTokens != nil && *doc.MaxInputTokens > 0 &&
		doc.OutputReserveTokens != nil && *doc.OutputReserveTokens >= *doc.MaxInputTokens {
		return PolicyDocument{}, nil, fmt.Errorf("%w: outputReserveTokens must be < maxInputTokens when maxInputTokens > 0", ErrInvalidPolicy)
	}

	// Agent v2: missing aap flags normalize to false when aap is present.
	// Absent aap remains nil (read as false at snapshot time).
	if doc.SchemaVersion == PolicySchemaV2 && doc.AAP != nil {
		f := false
		if doc.AAP.IncludeCompactionSummary == nil {
			doc.AAP.IncludeCompactionSummary = &f
		}
		if doc.AAP.EnableA2UI == nil {
			doc.AAP.EnableA2UI = &f
		}
	}

	normalized, err := json.Marshal(doc)
	if err != nil {
		return PolicyDocument{}, nil, ErrInvalidPolicy
	}
	return doc, json.RawMessage(normalized), nil
}

// NormalizePolicyRaw returns canonical JSON for Agent storage (legacy entrypoint).
func NormalizePolicyRaw(raw json.RawMessage) (json.RawMessage, error) {
	return NormalizeAgentPolicyRaw(raw)
}

// NormalizeAgentPolicyRaw validates Agent context_policy (v1/v2 + optional aap).
func NormalizeAgentPolicyRaw(raw json.RawMessage) (json.RawMessage, error) {
	_, normalized, err := ParsePolicyScoped(raw, PolicyScopeAgent)
	return normalized, err
}

// NormalizeWorkspacePolicyRaw validates workspace baseline (rejects aap).
func NormalizeWorkspacePolicyRaw(raw json.RawMessage) (json.RawMessage, error) {
	_, normalized, err := ParsePolicyScoped(raw, PolicyScopeWorkspace)
	return normalized, err
}

// IncludeCompactionSummary reports Agent disclosure defaulting to false.
func (doc PolicyDocument) IncludeCompactionSummary() bool {
	if doc.AAP == nil || doc.AAP.IncludeCompactionSummary == nil {
		return false
	}
	return *doc.AAP.IncludeCompactionSummary
}

// EnableA2UI reports whether the agent may emit additive A2UI (default false).
func (doc PolicyDocument) EnableA2UI() bool {
	if doc.AAP == nil || doc.AAP.EnableA2UI == nil {
		return false
	}
	return *doc.AAP.EnableA2UI
}
