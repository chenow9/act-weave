package modelconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// RuntimeCapabilitiesSchemaV1 is the only supported model runtime capability schema.
	RuntimeCapabilitiesSchemaV1 = "model-runtime.v1"

	OutputTokenLimitModeMaxTokens           = "max_tokens"
	OutputTokenLimitModeMaxCompletionTokens = "max_completion_tokens"

	TokenizerProfileO200kBase      = "o200k_base"
	TokenizerProfileCL100kBase     = "cl100k_base"
	TokenizerProfileByteUpperBound = "byte_upper_bound"
)

// knownTokenizerProfiles is the controlled registry used at config write time.
// Estimator implementations land later; unknown profiles must fail closed here.
var knownTokenizerProfiles = map[string]struct{}{
	TokenizerProfileO200kBase:      {},
	TokenizerProfileCL100kBase:     {},
	TokenizerProfileByteUpperBound: {},
}

// ParseRuntimeCapabilities validates and normalizes a raw JSON object.
// Empty object or nil means "unset" and returns empty raw `{}` with a zero value.
// Unknown fields, unknown versions, and illegal budgets return ErrInvalid.
func ParseRuntimeCapabilities(raw json.RawMessage) (RuntimeCapabilities, json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return RuntimeCapabilities{}, json.RawMessage(`{}`), nil
	}
	if !json.Valid(raw) {
		return RuntimeCapabilities{}, nil, ErrInvalid
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		return RuntimeCapabilities{}, nil, ErrInvalid
	}
	if len(top) == 0 {
		return RuntimeCapabilities{}, json.RawMessage(`{}`), nil
	}

	allowed := map[string]struct{}{
		"schemaVersion":              {},
		"contextWindowTokens":        {},
		"defaultOutputReserveTokens": {},
		"outputTokenLimitMode":       {},
		"tokenizerProfile":           {},
		"tokenizerVersion":           {},
	}
	for key := range top {
		if _, ok := allowed[key]; !ok {
			return RuntimeCapabilities{}, nil, fmt.Errorf("%w: unknown runtimeCapabilities field %q", ErrInvalid, key)
		}
	}

	var doc RuntimeCapabilities
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return RuntimeCapabilities{}, nil, ErrInvalid
	}

	doc.SchemaVersion = strings.TrimSpace(doc.SchemaVersion)
	doc.OutputTokenLimitMode = strings.TrimSpace(doc.OutputTokenLimitMode)
	doc.TokenizerProfile = strings.TrimSpace(doc.TokenizerProfile)
	doc.TokenizerVersion = strings.TrimSpace(doc.TokenizerVersion)

	if doc.SchemaVersion != RuntimeCapabilitiesSchemaV1 {
		return RuntimeCapabilities{}, nil, fmt.Errorf("%w: unsupported runtimeCapabilities schemaVersion", ErrInvalid)
	}
	if doc.ContextWindowTokens <= 0 {
		return RuntimeCapabilities{}, nil, fmt.Errorf("%w: contextWindowTokens must be > 0", ErrInvalid)
	}
	if doc.DefaultOutputReserveTokens <= 0 || doc.DefaultOutputReserveTokens >= doc.ContextWindowTokens {
		return RuntimeCapabilities{}, nil, fmt.Errorf("%w: defaultOutputReserveTokens must be in (0, contextWindowTokens)", ErrInvalid)
	}
	switch doc.OutputTokenLimitMode {
	case OutputTokenLimitModeMaxTokens, OutputTokenLimitModeMaxCompletionTokens:
	default:
		return RuntimeCapabilities{}, nil, fmt.Errorf("%w: unsupported outputTokenLimitMode", ErrInvalid)
	}
	if _, ok := knownTokenizerProfiles[doc.TokenizerProfile]; !ok {
		return RuntimeCapabilities{}, nil, fmt.Errorf("%w: unknown tokenizerProfile", ErrInvalid)
	}
	if doc.TokenizerVersion == "" {
		return RuntimeCapabilities{}, nil, fmt.Errorf("%w: tokenizerVersion is required", ErrInvalid)
	}

	normalized, err := json.Marshal(doc)
	if err != nil {
		return RuntimeCapabilities{}, nil, ErrInvalid
	}
	return doc, json.RawMessage(normalized), nil
}

// NormalizeRuntimeCapabilitiesRaw returns a canonical JSON object for storage.
// Empty / unset becomes `{}`. Invalid documents return ErrInvalid.
func NormalizeRuntimeCapabilitiesRaw(raw json.RawMessage) (json.RawMessage, error) {
	_, normalized, err := ParseRuntimeCapabilities(raw)
	return normalized, err
}

// IsKnownTokenizerProfile reports whether profile is in the write-time registry.
func IsKnownTokenizerProfile(profile string) bool {
	_, ok := knownTokenizerProfiles[strings.TrimSpace(profile)]
	return ok
}
