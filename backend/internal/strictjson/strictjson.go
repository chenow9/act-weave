// Package strictjson provides fail-closed JSON decoding helpers for frozen
// identity boundaries (duplicate keys, trailing bytes, exact object shapes).
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

// MaxNestingDepth bounds how deeply RejectDuplicateKeys descends into a
// document. The scan is recursive, and json.Decoder.Token — unlike
// json.Unmarshal and json.Valid — applies no nesting limit of its own, so
// without this cap the depth of the recursion is chosen by the attacker. A
// document of roughly 4 MB of nested arrays reached `fatal error: stack
// overflow`, which recover() cannot catch: the process dies rather than the
// request failing. The 1 MiB HTTP body cap does not bound this, because
// OpenAPI import accepts 4 MiB and snapshots re-read from the database are not
// size-capped at all.
//
// 256 is chosen from measurement, not taste: the deepest JSON document in this
// repository nests 11 levels (an agent-grant JSON Schema), and the fixed part
// of a graph snapshot spends 7 levels before it reaches an imported
// inputSchema. 256 leaves more than twenty times the observed headroom while
// bounding the recursion to a few hundred stack frames, and it stays below the
// standard library's own 10 000-level scanner limit so this package can never
// be the more permissive layer.
const MaxNestingDepth = 256

// RejectDuplicateKeys scans JSON and rejects duplicate object keys at any nesting
// level. Order of conflicting keys is irrelevant. Nesting beyond
// MaxNestingDepth is rejected rather than recursed into.
func RejectDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := rejectDupValue(dec, 1); err != nil {
		return err
	}
	return requireEndOfStream(dec, "JSON value")
}

// requireEndOfStream accepts only a clean end of input after the root value.
// json.Decoder.More reports false for a stray ']' or '}', and Token returns a
// syntax error rather than nil for that input, so "no more values" and "not
// parseable as another value" are both indistinguishable from success unless
// io.EOF is required explicitly. Anything else (second value, stray delimiter,
// comma, arbitrary bytes) is trailing data.
func requireEndOfStream(dec *json.Decoder, what string) error {
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing data after %s", what)
	}
	return nil
}

func rejectDupValue(dec *json.Decoder, depth int) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	if depth > MaxNestingDepth {
		return fmt.Errorf("JSON nesting exceeds %d levels", MaxNestingDepth)
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("object key must be a string")
			}
			if _, dup := seen[key]; dup {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := rejectDupValue(dec, depth+1); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := end.(json.Delim); !ok || d != '}' {
			return fmt.Errorf("expected end of object")
		}
		return nil
	case '[':
		for dec.More() {
			if err := rejectDupValue(dec, depth+1); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := end.(json.Delim); !ok || d != ']' {
			return fmt.Errorf("expected end of array")
		}
		return nil
	default:
		return fmt.Errorf("unexpected delimiter %v", delim)
	}
}

// jsonWhitespace is the complete RFC 8259 "ws" production. bytes.TrimSpace
// strips everything unicode.IsSpace accepts — NBSP, vertical tab, form feed,
// U+2028, U+3000 — none of which JSON allows between tokens. Trimming with it
// meant DecodeBoolExact("\u00a0true") returned (true, nil): the primitive
// accepted a document no JSON parser would.
const jsonWhitespace = " \t\n\r"

// trimJSONSpace removes only the whitespace JSON itself permits around a value.
func trimJSONSpace(raw []byte) []byte {
	return bytes.Trim(raw, jsonWhitespace)
}

// IsNull reports whether raw is JSON null (optional surrounding whitespace).
func IsNull(raw json.RawMessage) bool {
	return bytes.Equal(trimJSONSpace(raw), []byte("null"))
}

// IsEmptyObject reports whether raw is exactly {}.
func IsEmptyObject(raw json.RawMessage) bool {
	return bytes.Equal(trimJSONSpace(raw), []byte("{}"))
}

// DecodeObjectMap unmarshals a JSON object into a field map after duplicate-key
// rejection. raw must be a single object with no trailing data.
func DecodeObjectMap(raw json.RawMessage) (map[string]json.RawMessage, error) {
	raw = trimJSONSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty JSON")
	}
	if raw[0] != '{' {
		return nil, fmt.Errorf("JSON root must be an object")
	}
	if err := RejectDuplicateKeys(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var top map[string]json.RawMessage
	if err := dec.Decode(&top); err != nil {
		return nil, err
	}
	if err := requireEndOfStream(dec, "JSON object"); err != nil {
		return nil, err
	}
	if top == nil {
		return nil, fmt.Errorf("JSON object is null")
	}
	return top, nil
}

// RequirePresentNonNull ensures key exists and is not JSON null.
func RequirePresentNonNull(top map[string]json.RawMessage, key string) (json.RawMessage, error) {
	v, ok := top[key]
	if !ok {
		return nil, fmt.Errorf("missing field %q", key)
	}
	if IsNull(v) {
		return nil, fmt.Errorf("field %q must not be null", key)
	}
	return v, nil
}

// DecodeStringExact decodes a JSON string with no surrounding whitespace in the
// string value, no type coercion, and no encoding repair.
//
// encoding/json silently rewrites invalid UTF-8 bytes and unpaired surrogate
// escapes to U+FFFD, so `"\ud800"`, `"\udc00"` and a raw 0xFF byte all decoded
// to the same Go string. Every caller of this function is reading a frozen
// identity — id, provider, modelName, promptRevisionHash, callableName,
// capabilityId, schemaVersion — where distinct wire bytes collapsing onto one
// value is exactly the property that must not hold. Reject instead of repair.
func DecodeStringExact(raw json.RawMessage) (string, error) {
	raw = trimJSONSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", fmt.Errorf("expected JSON string")
	}
	if err := requireLosslessJSONString(raw); err != nil {
		return "", err
	}
	var s string
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&s); err != nil {
		return "", err
	}
	if err := requireEndOfStream(dec, "string"); err != nil {
		return "", err
	}
	return s, nil
}

// requireLosslessJSONString rejects the two wire forms that encoding/json would
// decode to U+FFFD rather than to the bytes it was given: invalid UTF-8, and a
// \uXXXX escape naming half of a surrogate pair. The check runs on the wire
// bytes because after decoding the substitution is indistinguishable from a
// string that genuinely contained U+FFFD.
func requireLosslessJSONString(raw []byte) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("JSON string is not valid UTF-8")
	}
	for i := 0; i < len(raw); {
		if raw[i] != '\\' {
			i++
			continue
		}
		if i+1 >= len(raw) {
			// Trailing backslash; leave the syntax error to the decoder.
			return nil
		}
		if raw[i+1] != 'u' {
			// Any other escape (including \\) consumes two bytes, so a 'u'
			// after an escaped backslash is a literal letter, not an escape.
			i += 2
			continue
		}
		if i+6 > len(raw) {
			return nil
		}
		code, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 32)
		if err != nil {
			return nil
		}
		i += 6
		switch {
		case code >= 0xDC00 && code <= 0xDFFF:
			return fmt.Errorf("JSON string contains an unpaired low surrogate escape \\u%04X", code)
		case code >= 0xD800 && code <= 0xDBFF:
			low, ok := readSurrogateEscape(raw, i)
			if !ok || low < 0xDC00 || low > 0xDFFF {
				return fmt.Errorf("JSON string contains an unpaired high surrogate escape \\u%04X", code)
			}
			i += 6
		}
	}
	return nil
}

// readSurrogateEscape reads a \uXXXX escape starting at i, if there is one.
func readSurrogateEscape(raw []byte, i int) (uint64, bool) {
	if i+6 > len(raw) || raw[i] != '\\' || raw[i+1] != 'u' {
		return 0, false
	}
	code, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 32)
	if err != nil {
		return 0, false
	}
	return code, true
}

// DecodeInt64Exact decodes a JSON number as int64 (rejects floats and strings).
func DecodeInt64Exact(raw json.RawMessage) (int64, error) {
	raw = trimJSONSpace(raw)
	if len(raw) == 0 {
		return 0, fmt.Errorf("expected JSON integer")
	}
	// Reject quoted strings and non-numeric.
	if raw[0] == '"' {
		return 0, fmt.Errorf("expected JSON integer, got string")
	}
	var n json.Number
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&n); err != nil {
		return 0, err
	}
	if err := requireEndOfStream(dec, "number"); err != nil {
		return 0, err
	}
	i, err := n.Int64()
	if err != nil {
		return 0, fmt.Errorf("expected JSON integer: %w", err)
	}
	// "-0" parses to 0, so two encodings named the same value at a boundary
	// whose whole job is to reject alternative encodings. Requiring the literal
	// to match the canonical rendering of the parsed value closes that without
	// enumerating spellings.
	if strconv.FormatInt(i, 10) != n.String() {
		return 0, fmt.Errorf("expected JSON integer in canonical form, got %s", n.String())
	}
	return i, nil
}

// DecodeBoolExact decodes a JSON boolean. Only the exact literals true and
// false (with optional surrounding whitespace) are accepted.
//
// json.Unmarshal treats a null literal as "leave the destination untouched" and
// reports no error, so decoding through it turned `null` into (false, nil) — a
// missing-value that silently became a decided false. Matching the two literals
// directly keeps null, quoted booleans, numbers, containers, empty input, and
// trailing data all in the reject path.
func DecodeBoolExact(raw json.RawMessage) (bool, error) {
	switch trimmed := trimJSONSpace(raw); {
	case bytes.Equal(trimmed, []byte("true")):
		return true, nil
	case bytes.Equal(trimmed, []byte("false")):
		return false, nil
	default:
		return false, fmt.Errorf("expected JSON boolean")
	}
}

// RequireArray ensures raw is exactly one well-formed JSON array (including
// empty []) with no trailing data. Null is illegal.
func RequireArray(raw json.RawMessage) error {
	raw = trimJSONSpace(raw)
	if IsNull(raw) {
		return fmt.Errorf("array must not be null")
	}
	if len(raw) == 0 || raw[0] != '[' {
		return fmt.Errorf("expected JSON array")
	}
	// A leading '[' alone accepted truncated and trailing-garbage documents.
	if !json.Valid(raw) {
		return fmt.Errorf("expected a single well-formed JSON array")
	}
	return nil
}

// RequireObject ensures raw is exactly one well-formed JSON object (including
// {}) with no trailing data. Null is illegal.
func RequireObject(raw json.RawMessage) error {
	raw = trimJSONSpace(raw)
	if IsNull(raw) {
		return fmt.Errorf("object must not be null")
	}
	if len(raw) == 0 || raw[0] != '{' {
		return fmt.Errorf("expected JSON object")
	}
	// A leading '{' alone accepted truncated and trailing-garbage documents.
	if !json.Valid(raw) {
		return fmt.Errorf("expected a single well-formed JSON object")
	}
	return nil
}
