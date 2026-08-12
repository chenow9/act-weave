// Fail-closed contract for the shared strict JSON boundary used by every
// frozen-identity strict parser (agentdelegation.ParseSnapshot,
// chatruntimebridge model/run-capability/run-agent snapshot parsers).
//
// Every exported function is covered on both the accept and the reject path.
// The primitive must be fail-closed standalone: callers keep their own guards
// (defense in depth), but correctness may not depend on them.
package strictjson

import (
	"encoding/json"
	"strings"
	"testing"
)

// rejectCase is one input plus the fail-closed expectation for it.
type rejectCase struct {
	name       string
	raw        string
	wantReject bool
}

// TestDecodeBoolExactRejectsNullAndNonBooleans locks the repair11 defect:
// DecodeBoolExact(`null`) returned (false, nil), so an absent/explicitly-null
// boolean became a decided false instead of a rejected document.
func TestDecodeBoolExactRejectsNullAndNonBooleans(t *testing.T) {
	for _, tc := range []struct {
		name       string
		raw        string
		wantReject bool
		wantValue  bool
	}{
		{name: "true", raw: `true`, wantValue: true},
		{name: "false", raw: `false`},
		{name: "leading whitespace", raw: "  true", wantValue: true},
		{name: "trailing whitespace", raw: "false\n\t"},
		{name: "null", raw: `null`, wantReject: true},
		{name: "quoted true", raw: `"true"`, wantReject: true},
		{name: "quoted false", raw: `"false"`, wantReject: true},
		{name: "number one", raw: `1`, wantReject: true},
		{name: "number zero", raw: `0`, wantReject: true},
		{name: "object", raw: `{}`, wantReject: true},
		{name: "array", raw: `[]`, wantReject: true},
		{name: "capitalized", raw: `True`, wantReject: true},
		{name: "uppercase", raw: `TRUE`, wantReject: true},
		{name: "trailing bool", raw: `true true`, wantReject: true},
		{name: "trailing garbage", raw: `true garbage`, wantReject: true},
		{name: "trailing bracket", raw: `true]`, wantReject: true},
		{name: "trailing comma", raw: `false,`, wantReject: true},
		{name: "empty", raw: ``, wantReject: true},
		{name: "whitespace only", raw: `   `, wantReject: true},
		{name: "prefix of true", raw: `tru`, wantReject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := DecodeBoolExact(json.RawMessage(tc.raw))
			if (err != nil) != tc.wantReject {
				t.Fatalf("DecodeBoolExact(%q): err=%v wantReject=%t", tc.raw, err, tc.wantReject)
			}
			if err != nil {
				// A rejected document must never hand back a usable decision.
				if value {
					t.Fatalf("DecodeBoolExact(%q) rejected but returned true", tc.raw)
				}
				return
			}
			if value != tc.wantValue {
				t.Fatalf("DecodeBoolExact(%q)=%t want %t", tc.raw, value, tc.wantValue)
			}
		})
	}
}

// TestRejectDuplicateKeys covers duplicate keys at every nesting level plus the
// repair11 trailing-data defect: json.Decoder.More() reports false for a stray
// closing delimiter, so `{"a":1}]` passed both the More() and the Token()!=nil
// checks and was accepted.
func TestRejectDuplicateKeys(t *testing.T) {
	for _, tc := range []rejectCase{
		{name: "clean object", raw: `{"a":1}`},
		{name: "empty object", raw: `{}`},
		{name: "nested clean", raw: `{"o":{"a":1,"b":2},"l":[{"a":1},{"a":2}]}`},
		{name: "scalar root number", raw: `1`},
		{name: "scalar root string", raw: `"x"`},
		{name: "scalar root bool", raw: `true`},
		{name: "scalar root null", raw: `null`},
		{name: "array root", raw: `[1,2,3]`},
		{name: "surrounding whitespace", raw: " \n{\"a\":1}\t "},
		{name: "duplicate top level", raw: `{"a":1,"a":2}`, wantReject: true},
		{name: "duplicate same value", raw: `{"a":1,"a":1}`, wantReject: true},
		{name: "duplicate nested object", raw: `{"o":{"a":1,"a":2}}`, wantReject: true},
		{name: "duplicate inside array element", raw: `{"l":[{"a":1,"a":2}]}`, wantReject: true},
		{name: "duplicate in root array element", raw: `[{"a":1,"a":2}]`, wantReject: true},
		{name: "duplicate deep", raw: `{"a":{"b":{"c":[{"d":1,"d":2}]}}}`, wantReject: true},
		{name: "trailing second object", raw: `{"a":1} {"b":2}`, wantReject: true},
		{name: "trailing second object no space", raw: `{"a":1}{"b":2}`, wantReject: true},
		{name: "trailing garbage", raw: `{"a":1}garbage`, wantReject: true},
		{name: "trailing close bracket", raw: `{"a":1}]`, wantReject: true},
		{name: "trailing close brace", raw: `{"a":1}}`, wantReject: true},
		{name: "trailing comma", raw: `{"a":1},`, wantReject: true},
		{name: "trailing scalar", raw: `{"a":1} 7`, wantReject: true},
		{name: "array trailing bracket", raw: `[1]]`, wantReject: true},
		{name: "scalar trailing bracket", raw: `1]`, wantReject: true},
		{name: "truncated object", raw: `{"a":1`, wantReject: true},
		{name: "truncated array", raw: `[1`, wantReject: true},
		{name: "empty input", raw: ``, wantReject: true},
		{name: "whitespace only", raw: "  \n ", wantReject: true},
		{name: "malformed", raw: `{"a":}`, wantReject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RejectDuplicateKeys([]byte(tc.raw))
			if (err != nil) != tc.wantReject {
				t.Fatalf("RejectDuplicateKeys(%q): err=%v wantReject=%t", tc.raw, err, tc.wantReject)
			}
		})
	}
}

func TestRequireArrayRejectsTrailingDataAndWrongTypes(t *testing.T) {
	for _, tc := range []rejectCase{
		{name: "empty array", raw: `[]`},
		{name: "single element", raw: `[1]`},
		{name: "nested", raw: `[[1],{"a":2},"s",null]`},
		{name: "surrounding whitespace", raw: " \n[1]\t "},
		{name: "null", raw: `null`, wantReject: true},
		{name: "object", raw: `{}`, wantReject: true},
		{name: "quoted array", raw: `"[]"`, wantReject: true},
		{name: "number", raw: `1`, wantReject: true},
		{name: "empty", raw: ``, wantReject: true},
		{name: "whitespace only", raw: `  `, wantReject: true},
		// repair11 defect: only raw[0]=='[' was checked.
		{name: "trailing garbage", raw: `[1] garbage`, wantReject: true},
		{name: "trailing array", raw: `[1][2]`, wantReject: true},
		{name: "trailing bracket", raw: `[1]]`, wantReject: true},
		{name: "trailing comma", raw: `[1],`, wantReject: true},
		{name: "truncated", raw: `[1`, wantReject: true},
		{name: "malformed element", raw: `[1,]`, wantReject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RequireArray(json.RawMessage(tc.raw))
			if (err != nil) != tc.wantReject {
				t.Fatalf("RequireArray(%q): err=%v wantReject=%t", tc.raw, err, tc.wantReject)
			}
		})
	}
}

func TestRequireObjectRejectsTrailingDataAndWrongTypes(t *testing.T) {
	for _, tc := range []rejectCase{
		{name: "empty object", raw: `{}`},
		{name: "single field", raw: `{"a":1}`},
		{name: "nested", raw: `{"a":{"b":[1,2]},"c":null}`},
		{name: "surrounding whitespace", raw: " \n{\"a\":1}\t "},
		// RequireObject is a structural gate; duplicate-key rejection stays the
		// job of RejectDuplicateKeys / DecodeObjectMap.
		{name: "duplicate keys still structural", raw: `{"a":1,"a":2}`},
		{name: "null", raw: `null`, wantReject: true},
		{name: "array", raw: `[]`, wantReject: true},
		{name: "quoted object", raw: `"{}"`, wantReject: true},
		{name: "number", raw: `1`, wantReject: true},
		{name: "empty", raw: ``, wantReject: true},
		{name: "whitespace only", raw: `  `, wantReject: true},
		// repair11 defect: only raw[0]=='{' was checked.
		{name: "trailing garbage", raw: `{} garbage`, wantReject: true},
		{name: "trailing object", raw: `{}{}`, wantReject: true},
		{name: "trailing brace", raw: `{}}`, wantReject: true},
		{name: "trailing comma", raw: `{},`, wantReject: true},
		{name: "truncated", raw: `{"a":1`, wantReject: true},
		{name: "malformed value", raw: `{"a":}`, wantReject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RequireObject(json.RawMessage(tc.raw))
			if (err != nil) != tc.wantReject {
				t.Fatalf("RequireObject(%q): err=%v wantReject=%t", tc.raw, err, tc.wantReject)
			}
		})
	}
}

func TestDecodeObjectMap(t *testing.T) {
	for _, tc := range []rejectCase{
		{name: "clean", raw: `{"a":1,"b":"x"}`},
		{name: "empty object", raw: `{}`},
		{name: "explicit null member", raw: `{"a":null}`},
		{name: "surrounding whitespace", raw: " \n{\"a\":1}\t "},
		{name: "duplicate key", raw: `{"a":1,"a":2}`, wantReject: true},
		{name: "duplicate nested key", raw: `{"o":{"a":1,"a":2}}`, wantReject: true},
		{name: "trailing garbage", raw: `{"a":1}garbage`, wantReject: true},
		{name: "two roots", raw: `{"a":1}{"b":2}`, wantReject: true},
		{name: "trailing bracket", raw: `{"a":1}]`, wantReject: true},
		{name: "array root", raw: `[1]`, wantReject: true},
		{name: "null root", raw: `null`, wantReject: true},
		{name: "string root", raw: `"x"`, wantReject: true},
		{name: "number root", raw: `7`, wantReject: true},
		{name: "empty", raw: ``, wantReject: true},
		{name: "whitespace only", raw: "  \t", wantReject: true},
		{name: "truncated", raw: `{"a":1`, wantReject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			top, err := DecodeObjectMap(json.RawMessage(tc.raw))
			if (err != nil) != tc.wantReject {
				t.Fatalf("DecodeObjectMap(%q): err=%v wantReject=%t", tc.raw, err, tc.wantReject)
			}
			if err != nil {
				if top != nil {
					t.Fatalf("DecodeObjectMap(%q) rejected but returned a map", tc.raw)
				}
				return
			}
			if top == nil {
				t.Fatalf("DecodeObjectMap(%q) accepted with nil map", tc.raw)
			}
		})
	}
}

func TestDecodeObjectMapKeepsRawMemberBytes(t *testing.T) {
	top, err := DecodeObjectMap(json.RawMessage(`{"s":"v","n":9,"o":{"k":true},"l":[1],"z":null}`))
	if err != nil {
		t.Fatalf("decode object map: %v", err)
	}
	if len(top) != 5 {
		t.Fatalf("member count=%d want 5", len(top))
	}
	for key, want := range map[string]string{
		"s": `"v"`, "n": `9`, "o": `{"k":true}`, "l": `[1]`, "z": `null`,
	} {
		if got := strings.TrimSpace(string(top[key])); got != want {
			t.Fatalf("member %q=%s want %s", key, got, want)
		}
	}
}

func TestRequirePresentNonNull(t *testing.T) {
	top, err := DecodeObjectMap(json.RawMessage(`{"present":1,"nulled":null,"falsey":false,"empty":""}`))
	if err != nil {
		t.Fatalf("decode object map: %v", err)
	}
	for _, tc := range []struct {
		key        string
		wantReject bool
	}{
		{key: "present"},
		{key: "falsey"},
		{key: "empty"},
		{key: "nulled", wantReject: true},
		{key: "absent", wantReject: true},
		{key: "", wantReject: true},
		{key: "Present", wantReject: true}, // keys are case sensitive
	} {
		value, err := RequirePresentNonNull(top, tc.key)
		if (err != nil) != tc.wantReject {
			t.Fatalf("RequirePresentNonNull(%q): err=%v wantReject=%t", tc.key, err, tc.wantReject)
		}
		if err != nil && value != nil {
			t.Fatalf("RequirePresentNonNull(%q) rejected but returned %s", tc.key, value)
		}
	}
	// A nil map has no present fields.
	if _, err := RequirePresentNonNull(nil, "present"); err == nil {
		t.Fatal("RequirePresentNonNull(nil map) must fail closed")
	}
}

func TestDecodeStringExact(t *testing.T) {
	for _, tc := range []struct {
		name       string
		raw        string
		wantReject bool
		want       string
	}{
		{name: "plain", raw: `"a"`, want: "a"},
		{name: "empty string", raw: `""`},
		{name: "unicode escape", raw: `"\u0041"`, want: "A"},
		{name: "inner whitespace preserved", raw: `" a "`, want: " a "},
		{name: "surrounding whitespace", raw: " \n\"a\"\t", want: "a"},
		{name: "null", raw: `null`, wantReject: true},
		{name: "number", raw: `1`, wantReject: true},
		{name: "bool", raw: `true`, wantReject: true},
		{name: "object", raw: `{}`, wantReject: true},
		{name: "array", raw: `[]`, wantReject: true},
		{name: "empty", raw: ``, wantReject: true},
		{name: "whitespace only", raw: `  `, wantReject: true},
		{name: "unterminated", raw: `"a`, wantReject: true},
		{name: "trailing string", raw: `"a" "b"`, wantReject: true},
		{name: "trailing garbage", raw: `"a" garbage`, wantReject: true},
		{name: "trailing bracket", raw: `"a"]`, wantReject: true},
		{name: "trailing comma", raw: `"a",`, wantReject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeStringExact(json.RawMessage(tc.raw))
			if (err != nil) != tc.wantReject {
				t.Fatalf("DecodeStringExact(%q): err=%v wantReject=%t", tc.raw, err, tc.wantReject)
			}
			if err != nil {
				if got != "" {
					t.Fatalf("DecodeStringExact(%q) rejected but returned %q", tc.raw, got)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("DecodeStringExact(%q)=%q want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestDecodeInt64Exact(t *testing.T) {
	for _, tc := range []struct {
		name       string
		raw        string
		wantReject bool
		want       int64
	}{
		{name: "one", raw: `1`, want: 1},
		{name: "zero", raw: `0`},
		{name: "negative", raw: `-1`, want: -1},
		{name: "max int64", raw: `9223372036854775807`, want: 9223372036854775807},
		{name: "min int64", raw: `-9223372036854775808`, want: -9223372036854775808},
		{name: "surrounding whitespace", raw: " \n12\t", want: 12},
		{name: "quoted", raw: `"1"`, wantReject: true},
		{name: "float", raw: `1.5`, wantReject: true},
		{name: "integral float", raw: `1.0`, wantReject: true},
		{name: "exponent", raw: `1e2`, wantReject: true},
		{name: "null", raw: `null`, wantReject: true},
		{name: "bool", raw: `true`, wantReject: true},
		{name: "object", raw: `{}`, wantReject: true},
		{name: "array", raw: `[]`, wantReject: true},
		{name: "empty", raw: ``, wantReject: true},
		{name: "whitespace only", raw: `  `, wantReject: true},
		{name: "overflow", raw: `9223372036854775808`, wantReject: true},
		{name: "underflow", raw: `-9223372036854775809`, wantReject: true},
		{name: "leading plus", raw: `+1`, wantReject: true},
		{name: "trailing number", raw: `1 2`, wantReject: true},
		{name: "trailing garbage", raw: `1 garbage`, wantReject: true},
		{name: "trailing bracket", raw: `1]`, wantReject: true},
		{name: "trailing comma", raw: `1,`, wantReject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeInt64Exact(json.RawMessage(tc.raw))
			if (err != nil) != tc.wantReject {
				t.Fatalf("DecodeInt64Exact(%q): err=%v wantReject=%t", tc.raw, err, tc.wantReject)
			}
			if err != nil {
				if got != 0 {
					t.Fatalf("DecodeInt64Exact(%q) rejected but returned %d", tc.raw, got)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("DecodeInt64Exact(%q)=%d want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestIsNull(t *testing.T) {
	for _, raw := range []string{`null`, ` null`, "null\n", " \tnull  "} {
		if !IsNull(json.RawMessage(raw)) {
			t.Fatalf("IsNull(%q)=false want true", raw)
		}
	}
	for _, raw := range []string{`"null"`, `NULL`, `Null`, `nul`, `nulls`, `{}`, `0`, `false`, ``, `null null`} {
		if IsNull(json.RawMessage(raw)) {
			t.Fatalf("IsNull(%q)=true want false", raw)
		}
	}
	if IsNull(nil) {
		t.Fatal("IsNull(nil)=true want false")
	}
}

func TestIsEmptyObject(t *testing.T) {
	for _, raw := range []string{`{}`, ` {}`, "{}\n", " \t{}  "} {
		if !IsEmptyObject(json.RawMessage(raw)) {
			t.Fatalf("IsEmptyObject(%q)=false want true", raw)
		}
	}
	for _, raw := range []string{`{ }`, `{"a":1}`, `"{}"`, `[]`, `null`, ``, `{}{}`} {
		if IsEmptyObject(json.RawMessage(raw)) {
			t.Fatalf("IsEmptyObject(%q)=true want false", raw)
		}
	}
	if IsEmptyObject(nil) {
		t.Fatal("IsEmptyObject(nil)=true want false")
	}
}

// TestRepair11ConfirmedDefects pins the four inputs that the primitive accepted
// before repair11, so a regression is reported as its own named failure instead
// of being buried in a table.
func TestRepair11ConfirmedDefects(t *testing.T) {
	if value, err := DecodeBoolExact(json.RawMessage(`null`)); err == nil {
		t.Fatalf("DecodeBoolExact(null) must reject, got (%t, nil)", value)
	}
	if err := RejectDuplicateKeys([]byte(`{"a":1}]`)); err == nil {
		t.Fatal(`RejectDuplicateKeys({"a":1}]) must reject trailing data`)
	}
	if err := RequireArray(json.RawMessage(`[1] garbage`)); err == nil {
		t.Fatal("RequireArray([1] garbage) must reject trailing data")
	}
	if err := RequireObject(json.RawMessage(`{} garbage`)); err == nil {
		t.Fatal("RequireObject({} garbage) must reject trailing data")
	}
}
