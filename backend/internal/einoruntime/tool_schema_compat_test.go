package einoruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestCanonicalParametersSchema_ObjectAndDeterminism(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"integer"}},"required":["a"]}`)
	a, err := canonicalizeAndValidateParametersSchema(raw)
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalizeAndValidateParametersSchema(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("not deterministic: %s vs %s", a, b)
	}
	// Map keys are sorted by encoding/json.
	if !strings.Contains(string(a), `"a"`) || !json.Valid(a) {
		t.Fatalf("canonical=%s", a)
	}
}

func TestCanonicalParametersSchema_Rejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"array root", `[]`, ErrToolSchemaInvalidRoot},
		{"string root", `"x"`, ErrToolSchemaInvalidRoot},
		{"json null", `null`, ErrToolSchemaInvalidRoot},
		{"duplicate key", `{"type":"object","type":"object"}`, ErrToolSchemaDuplicateKey},
		{"nested duplicate key", `{"type":"object","properties":{"a":{"type":"string","type":"number"}}}`, ErrToolSchemaDuplicateKey},
		{"external ref", `{"$ref":"https://example.com/schema.json"}`, ErrToolSchemaExternalRef},
		{"local ref", `{"$ref":"#/definitions/foo"}`, ErrToolSchemaUnsafeRef},
		{"default keyword", `{"type":"object","properties":{"q":{"type":"string","default":"x"}}}`, ErrToolSchemaUnsupportedKeyword},
		{"unsupported allOf", `{"type":"object","allOf":[]}`, ErrToolSchemaUnsupportedKeyword},
		{"non object type", `{"type":"string"}`, ErrToolSchemaInvalidRoot},
		{"type number", `{"type":7}`, ErrToolSchemaInvalidValue},
		{"nested type number", `{"type":"object","properties":{"x":{"type":7}}}`, ErrToolSchemaInvalidValue},
		{"string minProperties", `{"type":"object","minProperties":"1"}`, ErrToolSchemaInvalidValue},
		{"negative minLength", `{"type":"object","properties":{"s":{"type":"string","minLength":-1}}}`, ErrToolSchemaInvalidValue},
		{"noninteger maxItems", `{"type":"object","properties":{"a":{"type":"array","maxItems":1.5}}}`, ErrToolSchemaInvalidValue},
		{"minLength > maxLength", `{"type":"object","properties":{"s":{"type":"string","minLength":5,"maxLength":2}}}`, ErrToolSchemaInvalidValue},
		{"minimum > maximum", `{"type":"object","properties":{"n":{"type":"number","minimum":10,"maximum":1}}}`, ErrToolSchemaInvalidValue},
		{"duplicate required", `{"type":"object","properties":{"a":{"type":"string"}},"required":["a","a"]}`, ErrToolSchemaInvalidValue},
		{"required not in properties", `{"type":"object","properties":{"a":{"type":"string"}},"required":["b"]}`, ErrToolSchemaInvalidValue},
		{"required non-string", `{"type":"object","properties":{"a":{"type":"string"}},"required":[1]}`, ErrToolSchemaInvalidValue},
		{"uppercase type", `{"type":"Object"}`, ErrToolSchemaInvalidRoot},
		{"nonsense type", `{"type":"object","properties":{"x":{"type":"STRING"}}}`, ErrToolSchemaInvalidValue},
		{"empty type array", `{"type":"object","properties":{"x":{"type":[]}}}`, ErrToolSchemaInvalidValue},
		{"dup type array", `{"type":"object","properties":{"x":{"type":["string","string"]}}}`, ErrToolSchemaInvalidValue},
		{"invalid pattern", `{"type":"object","properties":{"x":{"type":"string","pattern":"["}}}`, ErrToolSchemaInvalidValue},
		{"empty enum", `{"type":"object","properties":{"x":{"type":"string","enum":[]}}}`, ErrToolSchemaInvalidValue},
		{"dup enum", `{"type":"object","properties":{"x":{"type":"string","enum":["a","a"]}}}`, ErrToolSchemaInvalidValue},
		{"const type mismatch", `{"type":"object","properties":{"x":{"type":"string","const":1}}}`, ErrToolSchemaInvalidValue},
		// Numeric semantic uniqueness: 1, 1.0, 1e0 are duplicates.
		{"numeric enum dup 1 and 1.0", `{"type":"object","properties":{"x":{"type":"number","enum":[1,1.0]}}}`, ErrToolSchemaInvalidValue},
		{"numeric enum dup 1 and 1e0", `{"type":"object","properties":{"x":{"type":"number","enum":[1,1e0]}}}`, ErrToolSchemaInvalidValue},
		{"numeric enum dup -0 and 0", `{"type":"object","properties":{"x":{"type":"number","enum":[-0,0]}}}`, ErrToolSchemaInvalidValue},
		// Union type arrays: every enum/const must match at least one allowed type.
		{"union enum bool not allowed", `{"type":"object","properties":{"x":{"type":["string","integer"],"enum":[true]}}}`, ErrToolSchemaInvalidValue},
		{"union const bool not allowed", `{"type":"object","properties":{"x":{"type":["string","integer"],"const":true}}}`, ErrToolSchemaInvalidValue},
		{"nested enum type mismatch", `{"type":"object","properties":{"n":{"type":"object","properties":{"x":{"type":"string","enum":[1]}}}}}`, ErrToolSchemaInvalidValue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := canonicalizeAndValidateParametersSchema(json.RawMessage(tc.raw))
			if err == nil || !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			// Errors must not embed full schema secrets/bodies beyond keyword names.
			if strings.Contains(err.Error(), "example.com/secret") {
				t.Fatalf("leaky error: %v", err)
			}
		})
	}
}

func TestCanonicalParametersSchema_RawByteCapBeforeParse(t *testing.T) {
	t.Parallel()
	// Oversized raw payload rejected before parse even if it is not valid JSON.
	huge := []byte(strings.Repeat("x", MaxToolSchemaBytes+1))
	_, err := canonicalizeAndValidateParametersSchema(json.RawMessage(huge))
	if !errors.Is(err, ErrToolSchemaTooLarge) {
		t.Fatalf("expected ErrToolSchemaTooLarge, got %v", err)
	}
	// Whitespace padding counts toward the raw byte cap *before* TrimSpace.
	// N spaces + "{}" that would be fine after trim must reject when total > cap.
	padded := append(bytes.Repeat([]byte(" "), MaxToolSchemaBytes-1), []byte(`{}`)...)
	// len = MaxToolSchemaBytes+1
	if len(padded) != MaxToolSchemaBytes+1 {
		padded = append(bytes.Repeat([]byte(" "), MaxToolSchemaBytes), []byte(`{}`)...)
	}
	if len(padded) <= MaxToolSchemaBytes {
		t.Fatalf("fixture len=%d want > %d", len(padded), MaxToolSchemaBytes)
	}
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(padded)); !errors.Is(err, ErrToolSchemaTooLarge) {
		t.Fatalf("whitespace-padded oversize: %v", err)
	}
	// Exact N raw bytes at the cap of a valid tiny schema accepted (spaces only
	// if total still under/at cap after construction of valid object).
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	// Exact boundary: raw length == MaxToolSchemaBytes of valid JSON object is
	// accepted when content is a valid object; N+1 rejected above.
	// Build a valid schema of exact byte length MaxToolSchemaBytes.
	// {"type":"object","properties":{"p":{"type":"string","description":"<pad>"}}}
	prefix := `{"type":"object","properties":{"p":{"type":"string","description":"`
	suffix := `"}}}`
	overhead := len(prefix) + len(suffix)
	if overhead >= MaxToolSchemaBytes {
		t.Fatal("fixture overhead exceeds cap")
	}
	padN := MaxToolSchemaBytes - overhead
	exact := prefix + strings.Repeat("z", padN) + suffix
	if len(exact) != MaxToolSchemaBytes {
		t.Fatalf("exact len=%d want %d", len(exact), MaxToolSchemaBytes)
	}
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(exact)); err != nil {
		t.Fatalf("exact N raw bytes must accept: %v", err)
	}
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(exact + " ")); !errors.Is(err, ErrToolSchemaTooLarge) {
		t.Fatalf("N+1 raw bytes must reject: %v", err)
	}
}

func TestCanonicalParametersSchema_UnionEnumConstAndNumericUniqueness(t *testing.T) {
	t.Parallel()
	// Valid mixed union: string and integer enum values.
	ok := `{"type":"object","properties":{"x":{"type":["string","integer"],"enum":["a",1,2]}}}`
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(ok)); err != nil {
		t.Fatalf("valid mixed union enum: %v", err)
	}
	// Valid nested const matching union.
	okNested := `{"type":"object","properties":{"n":{"type":"object","properties":{"x":{"type":["number","null"],"const":null}}}}}`
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(okNested)); err != nil {
		t.Fatalf("valid nested null const: %v", err)
	}
	// Distinct non-numeric equivalents still unique.
	okDistinct := `{"type":"object","properties":{"x":{"type":"string","enum":["1","1.0"]}}}`
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(okDistinct)); err != nil {
		t.Fatalf("string '1' and '1.0' are distinct: %v", err)
	}
	// Distinct numbers that are not equivalents.
	okNums := `{"type":"object","properties":{"x":{"type":"number","enum":[1,2,3.5]}}}`
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(okNums)); err != nil {
		t.Fatalf("distinct numbers: %v", err)
	}
	// Integer type accepts mathematical integer representations 1 / 1.0 / 1e0 / -0.0.
	okIntForms := `{"type":"object","properties":{"x":{"type":"integer","enum":[1]}}}`
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(okIntForms)); err != nil {
		t.Fatalf("integer enum 1: %v", err)
	}
	okIntPointZero := `{"type":"object","properties":{"x":{"type":"integer","const":1.0}}}`
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(okIntPointZero)); err != nil {
		t.Fatalf("integer const 1.0 must accept via IsInt: %v", err)
	}
	okIntSci := `{"type":"object","properties":{"x":{"type":"integer","const":1e0}}}`
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(okIntSci)); err != nil {
		t.Fatalf("integer const 1e0 must accept via IsInt: %v", err)
	}
	okNegZero := `{"type":"object","properties":{"x":{"type":"integer","const":-0.0}}}`
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(okNegZero)); err != nil {
		t.Fatalf("integer const -0.0 must accept via IsInt: %v", err)
	}
	// Fractional integer rejects.
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(
		`{"type":"object","properties":{"x":{"type":"integer","const":1.5}}}`,
	)); !errors.Is(err, ErrToolSchemaInvalidValue) {
		t.Fatalf("integer const 1.5 must reject: %v", err)
	}
	// Nested object enum uniqueness: {n:1}, {n:1.0}, {n:1e0} are duplicates.
	nestedDup := `{"type":"object","properties":{"x":{"type":"object","enum":[{"n":1},{"n":1.0}]}}}`
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(nestedDup)); !errors.Is(err, ErrToolSchemaInvalidValue) {
		t.Fatalf("nested object numeric dup must reject: %v", err)
	}
	nestedDupSci := `{"type":"object","properties":{"x":{"type":"object","enum":[{"n":1},{"n":1e0}]}}}`
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(nestedDupSci)); !errors.Is(err, ErrToolSchemaInvalidValue) {
		t.Fatalf("nested object 1 vs 1e0 must reject: %v", err)
	}
	// Nested array uniqueness: [1] and [1.0] collide.
	arrDup := `{"type":"object","properties":{"x":{"type":"array","enum":[[1],[1.0]]}}}`
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(arrDup)); !errors.Is(err, ErrToolSchemaInvalidValue) {
		t.Fatalf("nested array numeric dup must reject: %v", err)
	}
	// Array order matters: [1,2] and [2,1] are distinct.
	arrOrder := `{"type":"object","properties":{"x":{"type":"array","enum":[[1,2],[2,1]]}}}`
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(arrOrder)); err != nil {
		t.Fatalf("array order distinct: %v", err)
	}
	// Valid nested object with distinct keys / distinct numbers.
	nestedOK := `{"type":"object","properties":{"x":{"type":"object","enum":[{"n":1},{"n":2},{"m":1}]}}}`
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(nestedOK)); err != nil {
		t.Fatalf("distinct nested objects: %v", err)
	}
	// Deeply nested adversarial: object inside array with numeric equivalents.
	deepDup := `{"type":"object","properties":{"x":{"type":"array","enum":[[{"a":{"b":1}}],[{"a":{"b":1.0}}]]}}}`
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(deepDup)); !errors.Is(err, ErrToolSchemaInvalidValue) {
		t.Fatalf("deep nested numeric dup must reject: %v", err)
	}
}

func TestCanonicalParametersSchema_LosslessNumericNoFloat64(t *testing.T) {
	t.Parallel()
	// 9007199254740993 is 2^53+1 — not exactly representable in float64.
	// Pinned agenticopenai projects schema numbers through float64; catalog freeze
	// must fail closed so digest never diverges from wire.
	const huge = "9007199254740993"
	raw := json.RawMessage(`{"type":"object","properties":{"n":{"type":"number","minimum":` + huge + `,"maximum":` + huge + `,"const":` + huge + `,"enum":[` + huge + `]}}}`)
	if _, err := canonicalizeAndValidateParametersSchema(raw); !errors.Is(err, ErrToolSchemaAdapterNumericUnrepresentable) {
		t.Fatalf("unrepresentable huge integer must fail adapter projection, got %v", err)
	}
	// Nested enum/const with huge integer also fails closed.
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(
		`{"type":"object","properties":{"x":{"type":"object","const":{"n":` + huge + `}}}}`,
	)); !errors.Is(err, ErrToolSchemaAdapterNumericUnrepresentable) {
		t.Fatalf("nested unrepresentable const must fail: %v", err)
	}
	// Exact float64 boundary 2^53 is representable and must survive.
	const exactPow2 = "9007199254740992" // 2^53
	okExact, err := canonicalizeAndValidateParametersSchema(json.RawMessage(
		`{"type":"object","properties":{"n":{"type":"integer","enum":[` + exactPow2 + `],"const":` + exactPow2 + `}}}`,
	))
	if err != nil {
		t.Fatalf("exact 2^53 must accept: %v", err)
	}
	if !strings.Contains(string(okExact), exactPow2) {
		t.Fatalf("exact 2^53 must remain in canonical schema: %s", okExact)
	}
	// Equivalent forms canonicalize deterministically (1 / 1.0 / 1e0 → same digest path).
	a, err := canonicalizeAndValidateParametersSchema(json.RawMessage(
		`{"type":"object","properties":{"n":{"type":"number","minimum":1}}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalizeAndValidateParametersSchema(json.RawMessage(
		`{"type":"object","properties":{"n":{"type":"number","minimum":1.0}}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	c, err := canonicalizeAndValidateParametersSchema(json.RawMessage(
		`{"type":"object","properties":{"n":{"type":"number","minimum":1e0}}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) || string(a) != string(c) {
		t.Fatalf("equivalent minima must canonicalize identically:\n%s\n%s\n%s", a, b, c)
	}
	// Negative zero policy: -0 / -0.0 → 0 in emission.
	neg0, err := canonicalizeAndValidateParametersSchema(json.RawMessage(
		`{"type":"object","properties":{"n":{"type":"number","const":-0}}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(neg0), "-0") {
		t.Fatalf("negative zero must canonicalize to 0: %s", neg0)
	}
	if !strings.Contains(string(neg0), `"const":0`) && !strings.Contains(string(neg0), `"const": 0`) {
		// json.Marshal emits "const":0 without space.
		if !bytes.Contains(neg0, []byte(`"const":0`)) {
			t.Fatalf("expected const 0: %s", neg0)
		}
	}
	// Bounds conflict still detected with Rat (no float) on representable values.
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(
		`{"type":"object","properties":{"n":{"type":"number","minimum":10,"maximum":1}}}`,
	)); !errors.Is(err, ErrToolSchemaInvalidValue) {
		t.Fatalf("minimum > maximum must reject: %v", err)
	}
	// Huge exponent under CPU cap rejected (not rounded).
	if _, err := canonicalizeAndValidateParametersSchema(json.RawMessage(
		`{"type":"object","properties":{"n":{"type":"number","minimum":1e10001}}}`,
	)); err == nil {
		t.Fatal("huge exponent must reject under CPU cap")
	}
	// Deterministic digest for representable schemas.
	again, err := canonicalizeAndValidateParametersSchema(json.RawMessage(
		`{"type":"object","properties":{"n":{"type":"integer","enum":[` + exactPow2 + `],"const":` + exactPow2 + `}}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if string(okExact) != string(again) {
		t.Fatalf("digest not deterministic:\n%s\n%s", okExact, again)
	}
}

func TestCanonicalParametersSchema_StripsAnnotationsKeepsValidation(t *testing.T) {
	t.Parallel()
	// examples / x-* still stripped; default is rejected (not stripped).
	raw := json.RawMessage(`{
		"type":"object",
		"properties":{"q":{"type":"string","examples":["ex"]}},
		"description":"ok",
		"x-vendor":true
	}`)
	out, err := canonicalizeAndValidateParametersSchema(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "x-vendor") || strings.Contains(string(out), "examples") {
		t.Fatalf("annotations not stripped: %s", out)
	}
	if !strings.Contains(string(out), `"q"`) {
		t.Fatalf("properties lost: %s", out)
	}
}

func TestCanonicalParametersSchema_RejectsDefault(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
	}{
		{"root_default", `{"type":"object","default":"TASK3_M_SECRET"}`},
		{"prop_default_string", `{"type":"object","properties":{"q":{"type":"string","default":"TASK3_M_SECRET"}}}`},
		{"nested_default_object", `{"type":"object","properties":{"o":{"type":"object","default":{"k":"TASK3_M_SECRET"}}}}`},
		{"nested_default_array", `{"type":"object","properties":{"a":{"type":"array","items":{"type":"string"},"default":["TASK3_M_SECRET"]}}}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := canonicalizeAndValidateParametersSchema(json.RawMessage(tc.raw))
			if err == nil || !errors.Is(err, ErrToolSchemaUnsupportedKeyword) {
				t.Fatalf("want unsupported keyword for default, got %v", err)
			}
			if strings.Contains(err.Error(), "TASK3_M_SECRET") {
				t.Fatalf("error leaked default secret: %v", err)
			}
		})
	}
	// Equivalent schema without default still digests stably.
	ok := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
	a, err := canonicalizeAndValidateParametersSchema(ok)
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalizeAndValidateParametersSchema(ok)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("digest not stable:\n%s\n%s", a, b)
	}
}

func TestDescriptionAndSchemaByteBoundaries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Description N accepted, N+1 rejected.
	okDesc := strings.Repeat("d", MaxToolDescriptionBytes)
	if err := validateToolDescriptionLimits(okDesc); err != nil {
		t.Fatal(err)
	}
	if err := validateToolDescriptionLimits(okDesc + "x"); !errors.Is(err, ErrToolDescriptionTooLarge) {
		t.Fatalf("desc N+1: %v", err)
	}

	// Property count boundary: exact platform limit 256 accepted, 257 rejected.
	buildPropsSchema := func(n int) json.RawMessage {
		var b strings.Builder
		b.WriteString(`{"type":"object","properties":{`)
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			// Short keys p000.. keep under raw byte cap.
			fmt.Fprintf(&b, `"%s":{"type":"string"}`, fmtProp(i))
		}
		b.WriteString(`}}`)
		return json.RawMessage(b.String())
	}
	if MaxSchemaPropertyCount != 256 {
		t.Fatalf("platform property limit changed: %d", MaxSchemaPropertyCount)
	}
	if _, err := canonicalizeAndValidateParametersSchema(buildPropsSchema(256)); err != nil {
		t.Fatalf("256 properties must pass: %v", err)
	}
	if _, err := canonicalizeAndValidateParametersSchema(buildPropsSchema(257)); !errors.Is(err, ErrToolSchemaTooManyProperties) {
		t.Fatalf("257 properties must reject: %v", err)
	}
	// Catalog path with 256 props also accepted.
	props := map[string]*schema.ParameterInfo{}
	for i := 0; i < 256; i++ {
		props[fmtProp(i)] = &schema.ParameterInfo{Type: schema.String, Desc: "x"}
	}
	toolOK := &stubTool{name: "boundary_ok", desc: "ok", params: props}
	if _, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: toolOK, Exposure: ToolExposureDeferred}}); err != nil {
		t.Fatalf("catalog 256 properties should pass: %v", err)
	}

	// Direct schema depth boundary: depth N accepted, N+1 rejected.
	// depth 1 = root object.
	var buildDepth func(d int) map[string]any
	buildDepth = func(d int) map[string]any {
		if d <= 1 {
			return map[string]any{"type": "object"}
		}
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"c": buildDepth(d - 1),
			},
		}
	}
	okDepth, err := json.Marshal(buildDepth(MaxSchemaNestingDepth))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalizeAndValidateParametersSchema(okDepth); err != nil {
		t.Fatalf("depth N should pass: %v", err)
	}
	badDepth, err := json.Marshal(buildDepth(MaxSchemaNestingDepth + 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalizeAndValidateParametersSchema(badDepth); !errors.Is(err, ErrToolSchemaTooDeep) {
		t.Fatalf("depth N+1: %v", err)
	}

	// enum/const value trees share MaxSchemaNestingDepth (cannot bypass via deep values).
	var buildValueDepth func(d int) any
	buildValueDepth = func(d int) any {
		if d <= 1 {
			return map[string]any{"leaf": true}
		}
		return map[string]any{"c": buildValueDepth(d - 1)}
	}
	okEnum, err := json.Marshal(map[string]any{
		"type": "object",
		"enum": []any{buildValueDepth(MaxSchemaNestingDepth)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalizeAndValidateParametersSchema(okEnum); err != nil {
		t.Fatalf("enum depth N should pass: %v", err)
	}
	badEnum, err := json.Marshal(map[string]any{
		"type": "object",
		"enum": []any{buildValueDepth(MaxSchemaNestingDepth + 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalizeAndValidateParametersSchema(badEnum); !errors.Is(err, ErrToolSchemaTooDeep) {
		t.Fatalf("enum depth N+1: %v", err)
	}
	okConst, err := json.Marshal(map[string]any{
		"type":  "object",
		"const": buildValueDepth(MaxSchemaNestingDepth),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalizeAndValidateParametersSchema(okConst); err != nil {
		t.Fatalf("const depth N should pass: %v", err)
	}
	badConst, err := json.Marshal(map[string]any{
		"type":  "object",
		"const": buildValueDepth(MaxSchemaNestingDepth + 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalizeAndValidateParametersSchema(badConst); !errors.Is(err, ErrToolSchemaTooDeep) {
		t.Fatalf("const depth N+1: %v", err)
	}
	// Nested arrays in const also count depth (no root type constraint so
	// array const values remain type-coherent).
	var buildArrDepth func(d int) any
	buildArrDepth = func(d int) any {
		if d <= 1 {
			return []any{1}
		}
		return []any{buildArrDepth(d - 1)}
	}
	okArr, err := json.Marshal(map[string]any{"const": buildArrDepth(MaxSchemaNestingDepth)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalizeAndValidateParametersSchema(okArr); err != nil {
		t.Fatalf("const array depth N: %v", err)
	}
	badArr, err := json.Marshal(map[string]any{"const": buildArrDepth(MaxSchemaNestingDepth + 1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalizeAndValidateParametersSchema(badArr); !errors.Is(err, ErrToolSchemaTooDeep) {
		t.Fatalf("const array depth N+1: %v", err)
	}

	// Schema byte limit: N accepted, N+1 rejected.
	// Build a description-sized string inside a property description via raw JSON.
	// Approximate: pad a property description until just under MaxToolSchemaBytes.
	pad := strings.Repeat("z", 1000)
	makeSchema := func(padLen int) json.RawMessage {
		return json.RawMessage(`{"type":"object","properties":{"p":{"type":"string","description":"` + strings.Repeat("z", padLen) + `"}}}`)
	}
	// Find boundary by binary search is heavy; use known-oversize.
	huge := makeSchema(MaxToolSchemaBytes) // description alone near limit → total over
	if _, err := canonicalizeAndValidateParametersSchema(huge); !errors.Is(err, ErrToolSchemaTooLarge) && !errors.Is(err, ErrModelToolCatalogInvalid) {
		// If description pad is invalid as JSON length, still require rejection.
		if err == nil {
			t.Fatal("expected oversized schema reject")
		}
	}
	_ = pad
}

func fmtProp(i int) string {
	return fmt.Sprintf("p%04d", i)
}

func TestStripDoesNotChangeValidationSemantics_RejectUnsupported(t *testing.T) {
	t.Parallel()
	// `not` is validation-semantic — must reject, not strip.
	_, err := canonicalizeAndValidateParametersSchema(json.RawMessage(`{"type":"object","not":{"type":"string"}}`))
	if !errors.Is(err, ErrToolSchemaUnsupportedKeyword) {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildToolCatalog_ExistingValidSchemasStillPass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: &stubTool{name: "alpha", desc: "a", params: testParams()}, Exposure: ToolExposureDeferred},
		{Tool: &stubTool{name: "beta", desc: "b", params: testParams()}, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cat.Len() != 2 || cat.CatalogDigest() == "" {
		t.Fatalf("catalog=%+v", cat)
	}
}
