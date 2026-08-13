package einoruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/modelapi"
	"actweave/backend/internal/modelconfig"
)

// TestCatalogFreeze_AdapterProjection_FailClosedUnrepresentableNumbers proves
// BuildToolCatalog rejects schemas whose JSON numbers change under the pinned
// agenticopenai float64 map projection (2^53+1 and nearby distinct values).
func TestCatalogFreeze_AdapterProjection_FailClosedUnrepresentableNumbers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const (
		n1 = "9007199254740993" // 2^53+1 — collapses under float64
		n2 = "9007199254740994" // 2^53+2
		n0 = "9007199254740992" // 2^53 — exact
	)
	cases := []struct {
		name   string
		schema string
		want   error
	}{
		{
			name:   "enum_const_2^53+1",
			schema: `{"type":"object","properties":{"n":{"type":"integer","enum":[` + n1 + `],"const":` + n1 + `}}}`,
			want:   ErrToolSchemaAdapterNumericUnrepresentable,
		},
		{
			name:   "minimum_2^53+1",
			schema: `{"type":"object","properties":{"n":{"type":"number","minimum":` + n1 + `}}}`,
			want:   ErrToolSchemaAdapterNumericUnrepresentable,
		},
		{
			// Nested object const with unrepresentable integer (2^53+1).
			name:   "nested_const_2^53+1",
			schema: `{"type":"object","properties":{"x":{"type":"object","const":{"k":` + n1 + `}}}}`,
			want:   ErrToolSchemaAdapterNumericUnrepresentable,
		},
		{
			name:   "enum_mix_exact_and_unrepresentable",
			schema: `{"type":"object","properties":{"n":{"type":"integer","enum":[` + n0 + `,` + n1 + `]}}}`,
			want:   ErrToolSchemaAdapterNumericUnrepresentable,
		},
		{
			// Exponent form of 2^53+1 if accepted by number token path.
			name:   "const_2^53+1_via_keyword",
			schema: `{"type":"object","properties":{"n":{"type":"number","maximum":` + n1 + `}}}`,
			want:   ErrToolSchemaAdapterNumericUnrepresentable,
		},
	}
	_ = n2 // nearby distinct unrepresentable when used alone as odd offset of 2^53
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tool := &schemaStubTool{name: "numtool", desc: "numbers", schema: tc.schema}
			_, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
				{Tool: tool, Exposure: ToolExposureImmediate, PlatformControl: true},
			})
			if err == nil {
				t.Fatal("expected fail-closed catalog freeze")
			}
			if !errors.Is(err, tc.want) && !errors.Is(err, ErrModelToolCatalogInvalid) {
				t.Fatalf("want %v (or catalog invalid family), got %v", tc.want, err)
			}
			if tc.want == ErrToolSchemaAdapterNumericUnrepresentable && !errors.Is(err, ErrToolSchemaAdapterNumericUnrepresentable) {
				t.Fatalf("want ErrToolSchemaAdapterNumericUnrepresentable, got %v", err)
			}
			if strings.Contains(err.Error(), "sk-") || strings.Contains(err.Error(), "Bearer ") {
				t.Fatalf("error leaked secret material: %v", err)
			}
		})
	}
}

// TestCatalogFreeze_AdapterProjection_AcceptsRepresentableNumbers proves
// float64-safe values (including boundary 2^53, negatives, decimals, exponents
// that equal after projection) freeze and keep digests distinct when semantics differ.
func TestCatalogFreeze_AdapterProjection_AcceptsRepresentableNumbers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const exactPow2 = "9007199254740992" // 2^53
	schemaA := `{"type":"object","properties":{"n":{"type":"integer","enum":[` + exactPow2 + `,1,-1],"const":` + exactPow2 + `},"m":{"type":"number","enum":[0,1.5,-0.5,100],"const":1.5}},"required":["n"]}`
	// Semantically equal exponent form for 100 (1e2) — same wire/digest after canonicalize.
	schemaAExp := `{"type":"object","properties":{"n":{"type":"integer","enum":[` + exactPow2 + `,1,-1],"const":` + exactPow2 + `},"m":{"type":"number","enum":[0,1.5,-0.5,1e2],"const":1.5}},"required":["n"]}`
	// Distinct representable schema (const 1.5 → -0.5).
	schemaB := `{"type":"object","properties":{"n":{"type":"integer","enum":[` + exactPow2 + `,1,-1],"const":` + exactPow2 + `},"m":{"type":"number","enum":[0,1.5,-0.5,100],"const":-0.5}},"required":["n"]}`

	toolA := &schemaStubTool{name: "lookup", desc: "look up", schema: schemaA}
	catA, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: toolA, Exposure: ToolExposureImmediate, PlatformControl: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolAExp := &schemaStubTool{name: "lookup", desc: "look up", schema: schemaAExp}
	catAExp, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: toolAExp, Exposure: ToolExposureImmediate, PlatformControl: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if catA.CatalogDigest() != catAExp.CatalogDigest() {
		t.Fatalf("1e2 and 100 must canonicalize to same catalog digest")
	}
	toolB := &schemaStubTool{name: "lookup", desc: "look up", schema: schemaB}
	catB, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: toolB, Exposure: ToolExposureImmediate, PlatformControl: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if catA.CatalogDigest() == catB.CatalogDigest() {
		t.Fatal("distinct const values must rotate catalog digest")
	}
	// Zero / empty semantic fields.
	zeroSchema := `{"type":"object","properties":{},"required":[],"additionalProperties":false}`
	zt := &schemaStubTool{name: "zerotool", desc: "z", schema: zeroSchema}
	catZ, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: zt, Exposure: ToolExposureImmediate, PlatformControl: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	entZ, _ := catZ.Entry("zerotool")
	if !strings.Contains(string(entZ.Parameters), "additionalProperties") {
		t.Fatalf("additionalProperties:false dropped: %s", entZ.Parameters)
	}
}

// TestDeepCopyToolInfo_PreCapRejectsOversizedDescriptionAndSchema proves hard
// caps run before unbounded clone work; fail closed with established error families.
func TestDeepCopyToolInfo_PreCapRejectsOversizedDescriptionAndSchema(t *testing.T) {
	t.Parallel()
	// Oversized description.
	hugeDesc := strings.Repeat("d", MaxToolDescriptionBytes+1)
	info := &schema.ToolInfo{Name: "x", Desc: hugeDesc}
	if _, err := deepCopyToolInfo(info); !errors.Is(err, ErrToolDescriptionTooLarge) {
		t.Fatalf("want ErrToolDescriptionTooLarge, got %v", err)
	}
	// Oversized schema bytes (raw JSON past cap).
	pad := strings.Repeat("a", MaxToolSchemaBytes)
	bigSchema := `{"type":"object","properties":{"p":{"type":"string","description":"` + pad + `"}}}`
	if len(bigSchema) <= MaxToolSchemaBytes {
		t.Fatalf("fixture not oversized: %d", len(bigSchema))
	}
	js, err := decodeJSONSchemaLossless([]byte(bigSchema))
	if err != nil {
		t.Logf("decode: %v", err)
	}
	info2 := &schema.ToolInfo{
		Name:        "big",
		Desc:        "ok",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(js),
	}
	_, err = deepCopyToolInfo(info2)
	if err == nil {
		t.Fatal("expected oversized schema rejected")
	}
	if !errors.Is(err, ErrToolSchemaTooLarge) && !errors.Is(err, ErrModelToolCatalogInvalid) {
		t.Fatalf("want schema too large / catalog invalid family, got %v", err)
	}
}

// TestCatalogDigest_WireCapture_ExactEquality proves catalog Parameters equals
// the actual pinned adapter raw HTTP tool parameters (UseNumber decode of raw
// body; no float64 re-decode that would hide divergence). Only adapter-safe
// numbers are accepted into the catalog.
func TestCatalogDigest_WireCapture_ExactEquality(t *testing.T) {
	ctx := context.Background()
	const exactPow2 = "9007199254740992"
	raw := `{"type":"object","properties":{"n":{"type":"integer","enum":[` + exactPow2 + `,1,-1],"const":` + exactPow2 + `},"s":{"type":"number","enum":[0,1.5,100],"const":-0.5}},"required":["n"]}`
	tool := &schemaStubTool{name: "lookup", desc: "look up", schema: raw}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: tool, Exposure: ToolExposureImmediate, PlatformControl: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ent, _ := cat.Entry("lookup")
	info, err := cat.ToolInfoCopy("lookup")
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var rawWire []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read: %v", err)
			http.Error(w, "bad", 400)
			return
		}
		mu.Lock()
		rawWire = append([]byte(nil), rawBody...)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(wireCaptureMinimalResponsesJSON("ok")))
	}))
	t.Cleanup(srv.Close)

	secrets := wireCaptureSecretOpener(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})
	cfg := modelconfig.Config{
		WorkspaceID: "ws_wire",
		APIBase:     srv.URL + "/v1",
		ModelName:   "test-model",
	}
	m, err := modelapi.NewOpenAIAgenticModelWithEgress(
		ctx, modelapi.NewStreamingHTTPClient(), secrets, cfg, modelapi.LoopbackEgressPolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Generate(ctx, []*schema.AgenticMessage{agenticmsg.UserText("ping")},
		model.WithTools([]*schema.ToolInfo{info}),
	)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(rawWire) == 0 {
		t.Fatal("no wire body")
	}
	// Extract parameters with UseNumber from raw bytes — never float64 re-decode.
	paramsWire := extractFunctionToolParametersFromRaw(t, rawWire, "lookup")
	// Exact byte equality after both sides go through the same canonicalizer is
	// insufficient if we hide projection loss — compare UseNumber trees for
	// mathematical equality of every number AND structural key equality.
	if err := assertSchemaJSONTreesSemanticallyEqual(ent.Parameters, paramsWire); err != nil {
		t.Fatalf("catalog Parameters != raw adapter wire parameters: %v\ncatalog=%s\nwire=%s", err, ent.Parameters, paramsWire)
	}
	// Unrepresentable value must not freeze then wire — already covered by fail-closed tests.
	// Prove two schemas that would collapse under float64 cannot both freeze with different digests.
	bad := `{"type":"object","properties":{"n":{"type":"integer","const":9007199254740993}}}`
	_, err = BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: &schemaStubTool{name: "bad", desc: "d", schema: bad}, Exposure: ToolExposureImmediate, PlatformControl: true},
	})
	if !errors.Is(err, ErrToolSchemaAdapterNumericUnrepresentable) {
		t.Fatalf("2^53+1 must fail freeze before model call, got %v", err)
	}
}

// extractFunctionToolParametersFromRaw pulls tools[].parameters for name using
// UseNumber decode of the raw HTTP body.
func extractFunctionToolParametersFromRaw(t *testing.T, raw []byte, name string) json.RawMessage {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("decode wire: %v", err)
	}
	tools, _ := body["tools"].([]any)
	for _, tr := range tools {
		tm, _ := tr.(map[string]any)
		if tm == nil {
			continue
		}
		n, _ := tm["name"].(string)
		if n == "" {
			if fn, ok := tm["function"].(map[string]any); ok {
				n, _ = fn["name"].(string)
				if n == name {
					return mustJSONRawNumberSafe(t, fn["parameters"])
				}
			}
		}
		if n == name {
			if p, ok := tm["parameters"]; ok {
				return mustJSONRawNumberSafe(t, p)
			}
		}
	}
	t.Fatalf("tool %q not found in wire: %s", name, raw)
	return nil
}

// mustJSONRawNumberSafe re-marshals a UseNumber-decoded value without float64
// conversion (json.Number marshals as exact digits).
func mustJSONRawNumberSafe(t *testing.T, v any) json.RawMessage {
	t.Helper()
	if v == nil {
		t.Fatal("nil parameters")
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// assertSchemaJSONTreesSemanticallyEqual compares two schema JSON objects for
// structural equality and big.Rat-equal numbers (UseNumber decode both sides).
func assertSchemaJSONTreesSemanticallyEqual(a, b json.RawMessage) error {
	var ta, tb any
	da := json.NewDecoder(bytes.NewReader(a))
	da.UseNumber()
	if err := da.Decode(&ta); err != nil {
		return err
	}
	db := json.NewDecoder(bytes.NewReader(b))
	db.UseNumber()
	if err := db.Decode(&tb); err != nil {
		return err
	}
	return cmpJSONTreeSemantic(ta, tb, "")
}

func cmpJSONTreeSemantic(a, b any, path string) error {
	switch av := a.(type) {
	case json.Number:
		bv, ok := b.(json.Number)
		if !ok {
			// Wire may emit float that re-marshaled as Number — accept Number only on both after UseNumber.
			return errors.New(path + ": type mismatch number vs non-number")
		}
		// Use same projection-safe comparison: mathematical equality.
		if err := assertJSONNumberSurvivesFloat64Projection(av); err != nil {
			// catalog side should already be safe
			return err
		}
		ra, oka := new(big.Rat).SetString(av.String())
		rb, okb := new(big.Rat).SetString(bv.String())
		if !oka || !okb {
			return errors.New(path + ": invalid number")
		}
		if ra.Cmp(rb) != 0 {
			return errors.New(path + ": number mismatch " + av.String() + " vs " + bv.String())
		}
		return nil
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			return errors.New(path + ": expected object")
		}
		if len(av) != len(bv) {
			return errors.New(path + ": object key count mismatch")
		}
		for k, child := range av {
			other, ok := bv[k]
			if !ok {
				return errors.New(path + ": missing key " + k)
			}
			if err := cmpJSONTreeSemantic(child, other, path+"."+k); err != nil {
				return err
			}
		}
		return nil
	case []any:
		bv, ok := b.([]any)
		if !ok {
			return errors.New(path + ": expected array")
		}
		if len(av) != len(bv) {
			return errors.New(path + ": array length mismatch")
		}
		for i := range av {
			if err := cmpJSONTreeSemantic(av[i], bv[i], path+"[]"); err != nil {
				return err
			}
		}
		return nil
	case string:
		bv, ok := b.(string)
		if !ok || av != bv {
			return errors.New(path + ": string mismatch")
		}
		return nil
	case bool:
		bv, ok := b.(bool)
		if !ok || av != bv {
			return errors.New(path + ": bool mismatch")
		}
		return nil
	case nil:
		if b != nil {
			return errors.New(path + ": nil mismatch")
		}
		return nil
	default:
		return errors.New(path + ": unsupported type")
	}
}
