// Package sessioncontext holds versioned session-context policy documents
// shared by workspace and agent configuration (ZKL-74).
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

	ModeTokenWindow    = "token_window"
	ModeRollingSummary = "rolling_summary"
	ModeDisabled       = "disabled"
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
}

// SummaryPolicy holds optional rolling-summary knobs.
type SummaryPolicy struct {
	MaxTokens           *int64 `json:"maxTokens,omitempty"`
	MinEvictedTurns     *int64 `json:"minEvictedTurns,omitempty"`
	MaxGenerationPasses *int64 `json:"maxGenerationPasses,omitempty"`
}

// ParsePolicy validates and normalizes a raw JSON object.
// Empty object means unset/inherit and returns `{}`.
func ParsePolicy(raw json.RawMessage) (PolicyDocument, json.RawMessage, error) {
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
	}
	for key := range top {
		if _, ok := allowed[key]; !ok {
			return PolicyDocument{}, nil, fmt.Errorf("%w: unknown field %q", ErrInvalidPolicy, key)
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

	var doc PolicyDocument
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return PolicyDocument{}, nil, ErrInvalidPolicy
	}

	doc.SchemaVersion = strings.TrimSpace(doc.SchemaVersion)
	doc.Mode = strings.TrimSpace(doc.Mode)
	if doc.SchemaVersion != PolicySchemaV1 {
		return PolicyDocument{}, nil, fmt.Errorf("%w: unsupported schemaVersion", ErrInvalidPolicy)
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

	normalized, err := json.Marshal(doc)
	if err != nil {
		return PolicyDocument{}, nil, ErrInvalidPolicy
	}
	return doc, json.RawMessage(normalized), nil
}

// NormalizePolicyRaw returns canonical JSON for storage.
func NormalizePolicyRaw(raw json.RawMessage) (json.RawMessage, error) {
	_, normalized, err := ParsePolicy(raw)
	return normalized, err
}
