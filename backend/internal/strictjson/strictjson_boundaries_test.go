// Resource and encoding-canonicality boundaries for the shared strict JSON
// primitive. The original hostile matrix covered shape, type and duplicate-key
// behaviour but had no resource dimension at all, and no test decided what the
// primitive should do with byte sequences that encoding/json silently repairs.
package strictjson

import (
	"encoding/json"
	"strings"
	"testing"
)

// nestedArrays builds depth levels of nested JSON arrays: depth=2 is "[[]]".
func nestedArrays(depth int) string {
	return strings.Repeat("[", depth) + strings.Repeat("]", depth)
}

// nestedObjects builds depth levels of nested JSON objects: depth=2 is
// `{"a":{}}`.
func nestedObjects(depth int) string {
	return strings.Repeat(`{"a":`, depth-1) + "{}" + strings.Repeat("}", depth-1)
}

// TestRejectDuplicateKeysBoundsNestingDepth pins both sides of the cap added
// for the unbounded-recursion defect. rejectDupValue recursed once per nesting
// level with no limit, and json.Decoder.Token applies none of its own, so a
// ~4 MB document of nested arrays produced `fatal error: stack overflow` — a
// process kill that recover() cannot intercept.
func TestRejectDuplicateKeysBoundsNestingDepth(t *testing.T) {
	for _, tc := range []struct {
		name       string
		raw        string
		wantReject bool
	}{
		{name: "arrays one level", raw: nestedArrays(1)},
		{name: "arrays at the cap", raw: nestedArrays(MaxNestingDepth)},
		{name: "arrays one past the cap", raw: nestedArrays(MaxNestingDepth + 1), wantReject: true},
		{name: "objects at the cap", raw: nestedObjects(MaxNestingDepth)},
		{name: "objects one past the cap", raw: nestedObjects(MaxNestingDepth + 1), wantReject: true},
		// The exact shape that killed the process before the cap existed.
		{name: "two million array levels", raw: nestedArrays(2_000_000), wantReject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RejectDuplicateKeys([]byte(tc.raw))
			if (err != nil) != tc.wantReject {
				t.Fatalf("RejectDuplicateKeys(%d levels): err=%v wantReject=%t",
					strings.Count(tc.raw, "[")+strings.Count(tc.raw, "{"), err, tc.wantReject)
			}
			if err != nil && !strings.Contains(err.Error(), "nesting exceeds") {
				t.Fatalf("depth rejection must say so, got %v", err)
			}
		})
	}
}

// TestDecodeObjectMapInheritsTheNestingCap proves the cap is not bypassed by
// the wrapper that every strict parser actually calls.
func TestDecodeObjectMapInheritsTheNestingCap(t *testing.T) {
	if _, err := DecodeObjectMap(json.RawMessage(nestedObjects(MaxNestingDepth))); err != nil {
		t.Fatalf("a document at the cap must be accepted: %v", err)
	}
	_, err := DecodeObjectMap(json.RawMessage(nestedObjects(MaxNestingDepth + 1)))
	if err == nil {
		t.Fatal("DecodeObjectMap must reject nesting past the cap")
	}
	if !strings.Contains(err.Error(), "nesting exceeds") {
		t.Fatalf("depth rejection must say so, got %v", err)
	}
}

// TestNestingCapClearsRealDocuments guards the other direction: a cap tightened
// to the point of rejecting legitimate input would be a fail-closed bug of its
// own. The deepest JSON document in this repository nests 11 levels, and the
// fixed part of a graph snapshot spends 7 before reaching an imported schema.
func TestNestingCapClearsRealDocuments(t *testing.T) {
	const observedDeepestRealDocument = 11
	if MaxNestingDepth < 20*observedDeepestRealDocument {
		t.Fatalf("MaxNestingDepth=%d leaves too little headroom over the deepest real document (%d levels)",
			MaxNestingDepth, observedDeepestRealDocument)
	}
	// Below the standard library's own scanner limit, so this package can never
	// be the more permissive layer.
	if MaxNestingDepth >= 10_000 {
		t.Fatalf("MaxNestingDepth=%d is not stricter than encoding/json's 10000-level limit", MaxNestingDepth)
	}
	snapshotShaped := `{"nodes":[{"capabilitySnapshot":{"releases":[{"inputSchema":` +
		nestedObjects(observedDeepestRealDocument) + `}]}}]}`
	if err := RejectDuplicateKeys([]byte(snapshotShaped)); err != nil {
		t.Fatalf("a realistically deep snapshot must be accepted: %v", err)
	}
}

// TestDecodeStringExactRejectsLossyEncodings locks the substitution defect:
// encoding/json turns invalid UTF-8 and unpaired surrogate escapes into
// U+FFFD, so distinct wire bytes decoded to one Go string at a boundary whose
// purpose is frozen identity.
func TestDecodeStringExactRejectsLossyEncodings(t *testing.T) {
	for _, tc := range []struct {
		name       string
		raw        string
		wantReject bool
		want       string
	}{
		{name: "ascii", raw: `"a"`, want: "a"},
		{name: "multibyte utf8", raw: `"héllo"`, want: "héllo"},
		{name: "escaped bmp", raw: `"\u00e9"`, want: "é"},
		{name: "paired surrogates", raw: `"\ud83d\ude00"`, want: "\U0001F600"},
		{name: "genuine replacement char", raw: `"\ufffd"`, want: "\uFFFD"},
		{name: "escaped backslash then u", raw: `"\\u0041"`, want: `\u0041`},
		{name: "lone high surrogate", raw: `"\ud800"`, wantReject: true},
		{name: "lone low surrogate", raw: `"\udc00"`, wantReject: true},
		{name: "high surrogate then bmp", raw: `"\ud800\u0041"`, wantReject: true},
		{name: "reversed surrogate pair", raw: `"\ude00\ud83d"`, wantReject: true},
		{name: "high surrogate at end of string", raw: `"a\ud800"`, wantReject: true},
		{name: "raw invalid utf8 byte", raw: "\"a\xffb\"", wantReject: true},
		{name: "raw truncated multibyte", raw: "\"\xe4\xbd\"", wantReject: true},
		{name: "raw lone continuation byte", raw: "\"\x80\"", wantReject: true},
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

// TestDecodeStringExactNeverSubstitutes is the property the table above exists
// to protect: no accepted input may decode to a U+FFFD that was not written as
// one on the wire.
func TestDecodeStringExactNeverSubstitutes(t *testing.T) {
	for _, raw := range []string{`"\ud800"`, `"\udc00"`, "\"a\xffb\"", `"\ud800\ud800"`} {
		got, err := DecodeStringExact(json.RawMessage(raw))
		if err == nil {
			t.Fatalf("DecodeStringExact(%q) accepted and returned %q", raw, got)
		}
	}
	// The same bytes still decode lossily through encoding/json, which is why
	// the check has to exist rather than being delegated to it.
	var repaired string
	if err := json.Unmarshal([]byte(`"\ud800"`), &repaired); err != nil {
		t.Fatalf("encoding/json unexpectedly rejects a lone surrogate: %v", err)
	}
	if repaired != "\uFFFD" {
		t.Fatalf("encoding/json no longer substitutes U+FFFD (got %q); revisit this guard", repaired)
	}
}

// TestOnlyJSONWhitespaceIsTrimmed locks the trimming defect: bytes.TrimSpace
// strips the whole unicode.IsSpace set, so every entry point accepted leading
// NBSP / VT / FF / U+2028 / U+3000 that no JSON parser would.
func TestOnlyJSONWhitespaceIsTrimmed(t *testing.T) {
	jsonSpaces := map[string]string{"space": " ", "tab": "\t", "lf": "\n", "cr": "\r"}
	nonJSONSpaces := map[string]string{
		"nbsp": "\u00a0", "vertical tab": "\v", "form feed": "\f",
		"line separator": "\u2028", "ideographic space": "\u3000",
		"bom": "\ufeff",
	}

	for name, ws := range jsonSpaces {
		t.Run("accepts "+name, func(t *testing.T) {
			if got, err := DecodeBoolExact(json.RawMessage(ws + "true" + ws)); err != nil || !got {
				t.Fatalf("DecodeBoolExact(%q true)=(%t,%v) want (true,nil)", ws, got, err)
			}
			if got, err := DecodeInt64Exact(json.RawMessage(ws + "1" + ws)); err != nil || got != 1 {
				t.Fatalf("DecodeInt64Exact(%q 1)=(%d,%v) want (1,nil)", ws, got, err)
			}
			if got, err := DecodeStringExact(json.RawMessage(ws + `"a"` + ws)); err != nil || got != "a" {
				t.Fatalf("DecodeStringExact(%q \"a\")=(%q,%v) want (a,nil)", ws, got, err)
			}
			if err := RequireArray(json.RawMessage(ws + "[1]" + ws)); err != nil {
				t.Fatalf("RequireArray(%q [1])=%v want nil", ws, err)
			}
			if err := RequireObject(json.RawMessage(ws + "{}" + ws)); err != nil {
				t.Fatalf("RequireObject(%q {})=%v want nil", ws, err)
			}
			if _, err := DecodeObjectMap(json.RawMessage(ws + `{"a":1}` + ws)); err != nil {
				t.Fatalf("DecodeObjectMap(%q {...})=%v want nil", ws, err)
			}
			if !IsNull(json.RawMessage(ws + "null" + ws)) {
				t.Fatalf("IsNull(%q null)=false want true", ws)
			}
			if !IsEmptyObject(json.RawMessage(ws + "{}" + ws)) {
				t.Fatalf("IsEmptyObject(%q {})=false want true", ws)
			}
		})
	}

	for name, ws := range nonJSONSpaces {
		t.Run("rejects "+name, func(t *testing.T) {
			if got, err := DecodeBoolExact(json.RawMessage(ws + "true")); err == nil {
				t.Fatalf("DecodeBoolExact(%q true)=(%t,nil) want an error", ws, got)
			}
			if got, err := DecodeInt64Exact(json.RawMessage(ws + "1")); err == nil {
				t.Fatalf("DecodeInt64Exact(%q 1)=(%d,nil) want an error", ws, got)
			}
			if got, err := DecodeStringExact(json.RawMessage(ws + `"a"`)); err == nil {
				t.Fatalf("DecodeStringExact(%q \"a\")=(%q,nil) want an error", ws, got)
			}
			if err := RequireArray(json.RawMessage(ws + "[1]")); err == nil {
				t.Fatalf("RequireArray(%q [1])=nil want an error", ws)
			}
			if err := RequireObject(json.RawMessage(ws + "{}")); err == nil {
				t.Fatalf("RequireObject(%q {})=nil want an error", ws)
			}
			if _, err := DecodeObjectMap(json.RawMessage(ws + `{"a":1}`)); err == nil {
				t.Fatalf("DecodeObjectMap(%q {...})=nil want an error", ws)
			}
			if err := RejectDuplicateKeys([]byte(ws + `{"a":1}`)); err == nil {
				t.Fatalf("RejectDuplicateKeys(%q {...})=nil want an error", ws)
			}
			if IsNull(json.RawMessage(ws + "null")) {
				t.Fatalf("IsNull(%q null)=true want false", ws)
			}
			if IsEmptyObject(json.RawMessage(ws + "{}")) {
				t.Fatalf("IsEmptyObject(%q {})=true want false", ws)
			}
		})
	}
}

// TestDecodeInt64ExactRejectsNonCanonicalZero locks the "-0" defect. Three lock
// fields masked it behind their own `< 1` domain checks, but depth (root depth
// 0 is legal) and timeoutMs (only negatives are rejected) accepted "-0" as a
// second spelling of a legal value.
func TestDecodeInt64ExactRejectsNonCanonicalZero(t *testing.T) {
	for _, raw := range []string{`-0`, ` -0 `, "-0\n"} {
		got, err := DecodeInt64Exact(json.RawMessage(raw))
		if err == nil {
			t.Fatalf("DecodeInt64Exact(%q)=(%d,nil) want an error", raw, got)
		}
		if got != 0 {
			t.Fatalf("DecodeInt64Exact(%q) rejected but returned %d", raw, got)
		}
	}
	// The canonical spelling of the same value is still accepted, so this is a
	// rejection of the encoding and not of the value.
	if got, err := DecodeInt64Exact(json.RawMessage(`0`)); err != nil || got != 0 {
		t.Fatalf("DecodeInt64Exact(0)=(%d,%v) want (0,nil)", got, err)
	}
	for raw, want := range map[string]int64{
		`-1`: -1, `1`: 1, `-9223372036854775808`: -9223372036854775808,
	} {
		if got, err := DecodeInt64Exact(json.RawMessage(raw)); err != nil || got != want {
			t.Fatalf("DecodeInt64Exact(%s)=(%d,%v) want (%d,nil)", raw, got, err, want)
		}
	}
}
