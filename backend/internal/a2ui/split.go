package a2ui

import (
	"bytes"
	"encoding/json"
	"strings"
)

// EmitResult classifies terminal extract outcomes (design §3.3 / observability §10).
type EmitResult string

const (
	EmitNone               EmitResult = "none"
	EmitOK                 EmitResult = "ok"
	EmitOKEmptyText        EmitResult = "ok_empty_text"
	EmitInvalidJSON        EmitResult = "invalid_json"
	EmitTooLarge           EmitResult = "too_large"
	EmitStrippedDisabled   EmitResult = "stripped_disabled"
	EmitTruncated          EmitResult = "truncated"
	EmitProjectionRejected EmitResult = "projection_rejected"
	EmitProjectionOff      EmitResult = "projection_off"
	EmitCatalogInvalid     EmitResult = "catalog_invalid"
)

// Payload is an A2UI surface extracted from model output fences. Version and
// CatalogID are filled in by the write path after validation, never by the
// model; see MaterializeSurface.
type Payload struct {
	Version   string
	CatalogID string
	Surface   json.RawMessage // required JSON object; raw surface bytes
}

// SplitTextAndA2UI extracts the first fenced A2UI block from model terminal text.
//
// Rules (design §3.3 / §4.4):
//   - No fence → (full, nil, none)
//   - Incomplete fence (start without end) → leave raw full, none
//   - Multiple fences → first only; result truncated when a second start remains
//   - Bad JSON / non-object surface → (outer text, nil, invalid_json)
//   - Surface over MaxSurfaceBytes → (outer text, nil, too_large)
//   - Valid + empty outer text → ( "", payload, ok_empty_text )
//
// A well-formed JSON object is not yet a usable surface: catalog conformance is
// checked separately by ValidateSurface.
//
// Outer text is the content outside the first fence pair (surrounding whitespace
// at the fence boundary is trimmed). Callers must apply fallback policy when
// result is not ok/ok_empty_text/truncated and outer text is empty.
func SplitTextAndA2UI(full string) (text string, payload *Payload, result EmitResult) {
	start := strings.Index(full, FenceStart)
	if start < 0 {
		return full, nil, EmitNone
	}
	afterStart := start + len(FenceStart)
	relEnd := strings.Index(full[afterStart:], FenceEnd)
	if relEnd < 0 {
		// Incomplete fence — do not strip; preserve model raw output.
		return full, nil, EmitNone
	}
	endAbs := afterStart + relEnd
	inner := strings.TrimSpace(full[afterStart:endAbs])
	afterEnd := endAbs + len(FenceEnd)

	truncated := strings.Contains(full[afterEnd:], FenceStart)

	before := strings.TrimRight(full[:start], " \t\r\n")
	after := strings.TrimLeft(full[afterEnd:], " \t\r\n")
	switch {
	case before != "" && after != "":
		text = before + "\n" + after
	case before != "":
		text = before
	default:
		text = after
	}

	if inner == "" || !json.Valid([]byte(inner)) {
		return text, nil, EmitInvalidJSON
	}
	// The fence body is the surface itself. There is deliberately no envelope
	// form to unwrap: version, catalogId and surfaceId are platform-owned, and
	// guessing between "bare surface" and "wrapped surface" is what made the
	// previous parser ambiguous. A model-emitted wrapper now fails catalog
	// validation with a pointer at the offending key.
	surface := bytes.TrimSpace([]byte(inner))
	if len(surface) > MaxSurfaceBytes {
		return text, nil, EmitTooLarge
	}
	if surface[0] != '{' {
		return text, nil, EmitInvalidJSON
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(surface, &object); err != nil || object == nil {
		return text, nil, EmitInvalidJSON
	}
	payload = &Payload{Surface: append(json.RawMessage(nil), surface...)}
	if truncated {
		if strings.TrimSpace(text) == "" {
			return "", payload, EmitTruncated
		}
		return text, payload, EmitTruncated
	}
	if strings.TrimSpace(text) == "" {
		return "", payload, EmitOKEmptyText
	}
	return text, payload, EmitOK
}

// NonEmptyOrFallback returns text when non-empty after trim; otherwise full.
// Used for degrade paths so RecordAssistantResult always receives non-empty Content.
func NonEmptyOrFallback(text, full string) string {
	if strings.TrimSpace(text) != "" {
		return text
	}
	if strings.TrimSpace(full) != "" {
		return full
	}
	return text
}
