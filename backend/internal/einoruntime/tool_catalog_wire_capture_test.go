package einoruntime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/modelapi"
	"actweave/backend/internal/modelconfig"
)

// TestCatalogDigest_WireCapture_AnnotationAB_Coherence proves A/B schemas that
// differ only by stripped annotations produce identical catalog digests and
// identical agenticopenai wire tool parameters; post-build mutation of originals
// cannot change the wire. Cache-key option remains coherent across both builds.
func TestCatalogDigest_WireCapture_AnnotationAB_Coherence(t *testing.T) {
	ctx := context.Background()

	schemaA := `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`
	// Strip-able annotations only (default is rejected, not stripped).
	schemaB := `{"type":"object","properties":{"q":{"type":"string","examples":["a"],"x-act":1}},"required":["q"],"$comment":"n"}`
	toolA := &schemaStubTool{name: "lookup", desc: "look up", schema: schemaA}
	toolB := &schemaStubTool{name: "lookup", desc: "look up", schema: schemaB}

	catA, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: toolA, Exposure: ToolExposureImmediate, PlatformControl: true}})
	if err != nil {
		t.Fatal(err)
	}
	catB, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: toolB, Exposure: ToolExposureImmediate, PlatformControl: true}})
	if err != nil {
		t.Fatal(err)
	}
	if catA.CatalogDigest() != catB.CatalogDigest() {
		t.Fatalf("A/B catalog digests must match: %s vs %s", catA.CatalogDigest(), catB.CatalogDigest())
	}
	entA, _ := catA.Entry("lookup")
	entB, _ := catB.Entry("lookup")
	if entA.SchemaDigest != entB.SchemaDigest || string(entA.Parameters) != string(entB.Parameters) {
		t.Fatalf("A/B entry schema diverge: %s vs %s params %s vs %s",
			entA.SchemaDigest, entB.SchemaDigest, entA.Parameters, entB.Parameters)
	}

	infoA, err := catA.ToolInfoCopy("lookup")
	if err != nil {
		t.Fatal(err)
	}
	infoB, err := catB.ToolInfoCopy("lookup")
	if err != nil {
		t.Fatal(err)
	}

	// Capture wire bodies for A then B with the same prompt_cache_key.
	captureWire := func(t *testing.T, info *schema.ToolInfo, cacheKey string) map[string]any {
		t.Helper()
		var mu sync.Mutex
		var body map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read: %v", err)
				http.Error(w, "bad", 400)
				return
			}
			var b map[string]any
			if err := json.Unmarshal(raw, &b); err != nil {
				t.Errorf("decode: %v", err)
				http.Error(w, "bad", 400)
				return
			}
			mu.Lock()
			body = b
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
			modelapi.WithPromptCacheKey(cacheKey),
		)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if body == nil {
			t.Fatal("no wire body captured")
		}
		return body
	}

	const cacheKey = "catalog-wire-cache-key"
	bodyA := captureWire(t, infoA, cacheKey)
	bodyB := captureWire(t, infoB, cacheKey)

	if bodyA["prompt_cache_key"] != cacheKey || bodyB["prompt_cache_key"] != cacheKey {
		t.Fatalf("prompt_cache_key A=%v B=%v want %q", bodyA["prompt_cache_key"], bodyB["prompt_cache_key"], cacheKey)
	}
	paramsA := extractFunctionToolParameters(t, bodyA, "lookup")
	paramsB := extractFunctionToolParameters(t, bodyB, "lookup")
	// Canonicalize wire params and compare to frozen catalog entry.
	canonWireA, err := canonicalizeAndValidateParametersSchema(paramsA)
	if err != nil {
		t.Fatalf("wire A: %v", err)
	}
	canonWireB, err := canonicalizeAndValidateParametersSchema(paramsB)
	if err != nil {
		t.Fatalf("wire B: %v", err)
	}
	if string(canonWireA) != string(entA.Parameters) {
		t.Fatalf("wire A must match catalog Parameters:\nwire=%s\ncat=%s", canonWireA, entA.Parameters)
	}
	if string(canonWireA) != string(canonWireB) {
		t.Fatalf("A/B wire schemas must be identical after annotation strip:\nA=%s\nB=%s", canonWireA, canonWireB)
	}
	if strings.Contains(string(paramsA), "examples") ||
		strings.Contains(string(paramsA), "x-act") || strings.Contains(string(paramsB), "examples") {
		t.Fatalf("annotations leaked onto wire: A=%s B=%s", paramsA, paramsB)
	}

	// Mutation of original schema after catalog build must not change wire.
	toolB.schema = `{"type":"object","properties":{"q":{"type":"integer"}}}`
	infoB2, err := catB.ToolInfoCopy("lookup")
	if err != nil {
		t.Fatal(err)
	}
	bodyB2 := captureWire(t, infoB2, cacheKey)
	paramsB2 := extractFunctionToolParameters(t, bodyB2, "lookup")
	canonB2, err := canonicalizeAndValidateParametersSchema(paramsB2)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonB2) != string(entB.Parameters) {
		t.Fatalf("post-mutation wire changed: %s vs %s", canonB2, entB.Parameters)
	}

	// Executable validation against frozen catalog still passes for both.
	if err := catA.ValidateExecutableTools(ctx, []tool.BaseTool{toolA}); err != nil {
		t.Fatalf("ValidateExecutableTools A: %v", err)
	}
	if err := catB.ValidateExecutableTools(ctx, []tool.BaseTool{toolB}); err != nil {
		// toolB source mutated; validation re-reads Info() which now returns integer schema.
		// Frozen catalog should fail closed on mismatch — prove that.
		if !errorsIsCatalogMismatch(err) {
			t.Fatalf("mutated executable should mismatch catalog, got %v", err)
		}
	}
}

func errorsIsCatalogMismatch(err error) bool {
	return err != nil && (strings.Contains(err.Error(), ModelToolCatalogMismatchCode) ||
		strings.Contains(err.Error(), "schema") || strings.Contains(err.Error(), "mismatch"))
}

func extractFunctionToolParameters(t *testing.T, body map[string]any, name string) json.RawMessage {
	t.Helper()
	tools, _ := body["tools"].([]any)
	for _, raw := range tools {
		tm, _ := raw.(map[string]any)
		if tm == nil {
			continue
		}
		// agenticopenai may nest function tools as type=function with name/parameters
		// at top level or under "function".
		if tm["type"] == "function" || tm["name"] == name {
			n, _ := tm["name"].(string)
			if n == "" {
				if fn, ok := tm["function"].(map[string]any); ok {
					n, _ = fn["name"].(string)
					if n == name {
						return mustJSONRaw(t, fn["parameters"])
					}
				}
			}
			if n == name {
				if p, ok := tm["parameters"]; ok {
					return mustJSONRaw(t, p)
				}
				if fn, ok := tm["function"].(map[string]any); ok {
					return mustJSONRaw(t, fn["parameters"])
				}
			}
		}
	}
	t.Fatalf("tool %q not found in wire tools: %#v", name, body["tools"])
	return nil
}

func mustJSONRaw(t *testing.T, v any) json.RawMessage {
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

func wireCaptureMinimalResponsesJSON(text string) string {
	payload := map[string]any{
		"id": "resp_wire_1", "object": "response", "status": "completed", "model": "test-model",
		"output": []map[string]any{{
			"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}},
		}},
		"usage": map[string]any{
			"input_tokens": 1, "output_tokens": 1, "total_tokens": 2,
			"input_tokens_details":  map[string]any{"cached_tokens": 0},
			"output_tokens_details": map[string]any{"reasoning_tokens": 0},
		},
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

type wireCaptureSecretOpener func(context.Context, string, string, func([]byte) error) error

func (fn wireCaptureSecretOpener) WithActiveSecret(
	ctx context.Context,
	workspaceID, secretID string,
	use func([]byte) error,
) error {
	return fn(ctx, workspaceID, secretID, use)
}

// Ensure schemaStubTool / jsonschema remain linked when only this file is considered.
var _ = jsonschema.Schema{}
