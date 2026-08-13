package modelconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	ToolDisclosureSchemaV1         = "tool-disclosure.v1"
	DisclosureModePlatformOnDemand = "platform_on_demand"
	DisclosureModeCarryAll         = "carry_all"

	ToolDisclosureUIHidden      = "hidden"
	ToolDisclosureUIBinary      = "binary"
	ToolDisclosureUIUnavailable = "unavailable"
	ToolDisclosureUIUnverified  = "unverified"

	ErrorCodeToolDisclosureInvalid      = "MODEL_TOOL_DISCLOSURE_INVALID"
	ErrorCodeToolCarryAllTooLarge       = "MODEL_TOOL_CARRY_ALL_TOO_LARGE"
	ErrorCodeToolCarryAllSoft           = "MODEL_TOOL_CARRY_ALL_SOFT"
	ErrorCodeToolDisclosureNotRolledOut = "MODEL_TOOL_DISCLOSURE_NOT_ROLLED_OUT"

	CarryAllSoftLimit = 5
	CarryAllHardLimit = 8
)

var (
	ErrToolDisclosureInvalid      = fmt.Errorf("%w: %s", ErrInvalid, ErrorCodeToolDisclosureInvalid)
	ErrToolCarryAllTooLarge       = fmt.Errorf("%w: %s", ErrInvalid, ErrorCodeToolCarryAllTooLarge)
	ErrToolDisclosureNotRolledOut = fmt.Errorf("%w: %s", ErrInvalid, ErrorCodeToolDisclosureNotRolledOut)
)

// ToolDisclosurePolicy is the strict user-writable disclosure document.
// Empty object "{}" means unset. Only platform_on_demand and carry_all are valid.
type ToolDisclosurePolicy struct {
	SchemaVersion string `json:"schemaVersion,omitempty"`
	Mode          string `json:"mode,omitempty"`
}

// CarryAllTooLargeError is returned when any referencing Agent's catalog
// exceeds CarryAllHardLimit. Unwraps to ErrToolCarryAllTooLarge.
type CarryAllTooLargeError struct {
	AgentID string
	Count   int
	Limit   int
}

func (e CarryAllTooLargeError) Error() string {
	return ErrorCodeToolCarryAllTooLarge
}

func (e CarryAllTooLargeError) Unwrap() error {
	return ErrToolCarryAllTooLarge
}

// AsCarryAllTooLarge reports whether err is (or wraps) a CarryAllTooLargeError.
func AsCarryAllTooLarge(err error) (CarryAllTooLargeError, bool) {
	var typed CarryAllTooLargeError
	if errors.As(err, &typed) {
		return typed, true
	}
	return CarryAllTooLargeError{}, false
}

// IsUnsetToolDisclosurePolicy reports whether raw is the unset form.
// Only empty/absent or exact `{}` count; JSON null is not unset.
func IsUnsetToolDisclosurePolicy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || bytes.Equal(raw, []byte("{}"))
}

// ParseToolDisclosurePolicy validates and normalizes a raw JSON object.
// Empty object or empty/nil raw means unset and returns a zero value with raw `{}`.
// JSON null is rejected. Non-empty documents must be exactly schemaVersion+mode
// with canonical values; unknown fields, duplicate keys, and nulls fail closed.
func ParseToolDisclosurePolicy(raw json.RawMessage) (ToolDisclosurePolicy, json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ToolDisclosurePolicy{}, json.RawMessage(`{}`), nil
	}
	if bytes.Equal(raw, []byte("null")) {
		return ToolDisclosurePolicy{}, nil, fmt.Errorf("%w: toolDisclosurePolicy must not be JSON null", ErrToolDisclosureInvalid)
	}
	if !json.Valid(raw) {
		return ToolDisclosurePolicy{}, nil, ErrToolDisclosureInvalid
	}
	if raw[0] != '{' {
		return ToolDisclosurePolicy{}, nil, fmt.Errorf("%w: toolDisclosurePolicy must be a JSON object", ErrToolDisclosureInvalid)
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return ToolDisclosurePolicy{}, nil, fmt.Errorf("%w: %v", ErrToolDisclosureInvalid, err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		return ToolDisclosurePolicy{}, nil, ErrToolDisclosureInvalid
	}
	if len(top) == 0 {
		return ToolDisclosurePolicy{}, json.RawMessage(`{}`), nil
	}

	allowed := map[string]struct{}{
		"schemaVersion": {},
		"mode":          {},
	}
	for key := range top {
		if _, ok := allowed[key]; !ok {
			return ToolDisclosurePolicy{}, nil, fmt.Errorf("%w: unknown toolDisclosurePolicy field %q", ErrToolDisclosureInvalid, key)
		}
	}
	for _, required := range []string{"schemaVersion", "mode"} {
		if _, ok := top[required]; !ok {
			return ToolDisclosurePolicy{}, nil, fmt.Errorf("%w: missing toolDisclosurePolicy field %q", ErrToolDisclosureInvalid, required)
		}
		if bytes.Equal(bytes.TrimSpace(top[required]), []byte("null")) {
			return ToolDisclosurePolicy{}, nil, fmt.Errorf("%w: toolDisclosurePolicy field %q must not be null", ErrToolDisclosureInvalid, required)
		}
	}

	var doc ToolDisclosurePolicy
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return ToolDisclosurePolicy{}, nil, fmt.Errorf("%w: %v", ErrToolDisclosureInvalid, err)
	}
	if dec.More() {
		return ToolDisclosurePolicy{}, nil, fmt.Errorf("%w: trailing data after toolDisclosurePolicy", ErrToolDisclosureInvalid)
	}
	if doc.SchemaVersion != ToolDisclosureSchemaV1 {
		return ToolDisclosurePolicy{}, nil, fmt.Errorf("%w: unsupported toolDisclosurePolicy schemaVersion", ErrToolDisclosureInvalid)
	}
	switch doc.Mode {
	case DisclosureModePlatformOnDemand, DisclosureModeCarryAll:
	default:
		return ToolDisclosurePolicy{}, nil, fmt.Errorf("%w: unsupported toolDisclosurePolicy mode", ErrToolDisclosureInvalid)
	}

	normalized, err := json.Marshal(doc)
	if err != nil {
		return ToolDisclosurePolicy{}, nil, ErrToolDisclosureInvalid
	}
	return doc, json.RawMessage(normalized), nil
}

// NormalizeToolDisclosurePolicyRaw returns canonical JSON for storage.
// Empty / unset becomes `{}`. Invalid documents (including JSON null) fail closed.
func NormalizeToolDisclosurePolicyRaw(raw json.RawMessage) (json.RawMessage, error) {
	_, normalized, err := ParseToolDisclosurePolicy(raw)
	return normalized, err
}

// CanonicalToolDisclosurePolicy builds the only accepted non-empty policy document.
func CanonicalToolDisclosurePolicy(mode string) (json.RawMessage, error) {
	switch mode {
	case DisclosureModePlatformOnDemand, DisclosureModeCarryAll:
	default:
		return nil, fmt.Errorf("%w: unsupported toolDisclosurePolicy mode", ErrToolDisclosureInvalid)
	}
	return json.Marshal(ToolDisclosurePolicy{
		SchemaVersion: ToolDisclosureSchemaV1,
		Mode:          mode,
	})
}

// AssertDisclosureWritable reports whether set-disclosure may write a policy.
// Only VERIFIED rows whose caps parse as function_calling are writable.
func AssertDisclosureWritable(cfg Config) error {
	if cfg.Status != StatusVerified {
		return ErrToolDisclosureInvalid
	}
	caps, _, err := ParseAgenticCapabilities(cfg.AgenticCapabilities)
	if err != nil || caps.ToolCalling != ToolCallingFunctionCalling {
		return ErrToolDisclosureInvalid
	}
	return nil
}

// DeriveToolDisclosureUI maps status + capability document to the GET/list UI token.
func DeriveToolDisclosureUI(status Status, caps json.RawMessage) string {
	if status == StatusError || IsUnverifiedAgenticCapabilities(caps) {
		return ToolDisclosureUIUnverified
	}
	doc, _, err := ParseAgenticCapabilities(caps)
	if err != nil || doc.SchemaVersion == "" {
		return ToolDisclosureUIUnverified
	}
	switch doc.ToolCalling {
	case ToolCallingNativeClientSearch:
		return ToolDisclosureUIHidden
	case ToolCallingFunctionCalling:
		return ToolDisclosureUIBinary
	case ToolCallingNone:
		return ToolDisclosureUIUnavailable
	default:
		return ToolDisclosureUIUnverified
	}
}
