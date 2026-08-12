// Package strictjson provides fail-closed JSON decoding helpers for frozen
// identity boundaries (duplicate keys, trailing bytes, exact object shapes).
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// RejectDuplicateKeys scans JSON and rejects duplicate object keys at any nesting
// level. Order of conflicting keys is irrelevant.
func RejectDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := rejectDupValue(dec); err != nil {
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

func rejectDupValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
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
			if err := rejectDupValue(dec); err != nil {
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
			if err := rejectDupValue(dec); err != nil {
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

// IsNull reports whether raw is JSON null (optional surrounding whitespace).
func IsNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// IsEmptyObject reports whether raw is exactly {}.
func IsEmptyObject(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("{}"))
}

// DecodeObjectMap unmarshals a JSON object into a field map after duplicate-key
// rejection. raw must be a single object with no trailing data.
func DecodeObjectMap(raw json.RawMessage) (map[string]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
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
// string value and no type coercion.
func DecodeStringExact(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", fmt.Errorf("expected JSON string")
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

// DecodeInt64Exact decodes a JSON number as int64 (rejects floats and strings).
func DecodeInt64Exact(raw json.RawMessage) (int64, error) {
	raw = bytes.TrimSpace(raw)
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
	switch trimmed := bytes.TrimSpace(raw); {
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
	raw = bytes.TrimSpace(raw)
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
	raw = bytes.TrimSpace(raw)
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
