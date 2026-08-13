package modelconfig_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/modelconfig"
)

func TestParseAgenticCapabilitiesEmptyIsUnverified(t *testing.T) {
	// Only absent/default raw `{}` (or empty) means unverified — not JSON null.
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`  {}  `)} {
		doc, normalized, err := modelconfig.ParseAgenticCapabilities(raw)
		if err != nil {
			t.Fatalf("empty parse: %v", err)
		}
		if doc.SchemaVersion != "" || string(normalized) != "{}" {
			t.Fatalf("expected unverified empty, got doc=%+v raw=%s", doc, normalized)
		}
	}
}

func TestParseAgenticCapabilitiesRejectsJSONNull(t *testing.T) {
	_, _, err := modelconfig.ParseAgenticCapabilities(json.RawMessage(`null`))
	if err == nil || !errors.Is(err, modelconfig.ErrInvalid) {
		t.Fatalf("JSON null must reject, got %v", err)
	}
	if modelconfig.IsUnverifiedAgenticCapabilities(json.RawMessage(`null`)) {
		t.Fatal("null must not be treated as unverified")
	}
}

func TestParseAgenticCapabilitiesValid(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	digest := strings.Repeat("a", 64)
	docIn, err := modelconfig.CanonicalAgenticCapabilities(at, 3, digest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(docIn)
	if err != nil {
		t.Fatal(err)
	}
	// Canonical marshal must emit exact …Z seconds form.
	if !strings.Contains(string(raw), `"verifiedAt":"2026-08-10T12:00:00Z"`) {
		t.Fatalf("non-canonical verifiedAt wire form: %s", raw)
	}
	doc, normalized, err := modelconfig.ParseAgenticCapabilities(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.SchemaVersion != modelconfig.AgenticCapabilitiesSchemaV1 ||
		doc.Protocol != modelconfig.AgenticProtocolOpenAIResponsesV1 ||
		doc.VerifiedAdapter != modelconfig.VerifiedAdapterAgenticOpenAIV022 ||
		doc.VerifiedLockVersion != 3 || doc.VerifiedConfigDigest != digest ||
		!doc.Streaming || !doc.Usage {
		t.Fatalf("unexpected doc: %+v", doc)
	}
	if !json.Valid(normalized) || !strings.Contains(string(normalized), "agentic-model.v1") {
		t.Fatalf("unexpected normalized: %s", normalized)
	}
}

func TestParseAgenticCapabilitiesRejectsNonCanonicalTimestamps(t *testing.T) {
	digest := strings.Repeat("b", 64)
	base := func(at string) string {
		return `{"schemaVersion":"agentic-model.v1","protocol":"openai-responses-v1","streaming":true,"usage":true,"toolSearchModes":["client"],"reasoningReplay":"encrypted-or-none","verifiedAdapter":"agenticopenai/v0.2.2","verifiedAt":"` + at + `","verifiedLockVersion":1,"verifiedConfigDigest":"` + digest + `"}`
	}
	for _, at := range []string{
		"2026-08-10T12:00:00+00:00", // offset spelling
		"2026-08-10T12:00:00.000Z",  // fractional seconds
		"2026-08-10T12:00:00.0Z",
		"2026-08-10T13:00:00+01:00", // non-UTC offset
		"2026-08-10 12:00:00Z",      // space
		"2026-08-10T12:00:00z",      // lowercase z
		"2026-08-10T12:00:00.123456789Z",
	} {
		t.Run(at, func(t *testing.T) {
			_, _, err := modelconfig.ParseAgenticCapabilities(json.RawMessage(base(at)))
			if err == nil || !errors.Is(err, modelconfig.ErrInvalid) {
				t.Fatalf("expected reject non-canonical timestamp %q, got %v", at, err)
			}
		})
	}
	// Exact Z form accepted.
	_, _, err := modelconfig.ParseAgenticCapabilities(json.RawMessage(base("2026-08-10T12:00:00Z")))
	if err != nil {
		t.Fatalf("canonical Z must accept: %v", err)
	}
}

func TestParseAgenticCapabilitiesRejects(t *testing.T) {
	at := "2026-08-10T12:00:00Z"
	digest := strings.Repeat("b", 64)
	base := map[string]any{
		"schemaVersion":        "agentic-model.v1",
		"protocol":             "openai-responses-v1",
		"streaming":            true,
		"usage":                true,
		"toolSearchModes":      []string{"client"},
		"reasoningReplay":      "encrypted-or-none",
		"verifiedAdapter":      "agenticopenai/v0.2.2",
		"verifiedAt":           at,
		"verifiedLockVersion":  1,
		"verifiedConfigDigest": digest,
	}
	mustJSON := func(m map[string]any) string {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	cases := []struct {
		name string
		raw  string
	}{
		{"unknown field", func() string {
			m := copyMap(base)
			m["extra"] = 1
			return mustJSON(m)
		}()},
		{"unknown version", func() string {
			m := copyMap(base)
			m["schemaVersion"] = "agentic-model.v9"
			return mustJSON(m)
		}()},
		{"wrong protocol", func() string {
			m := copyMap(base)
			m["protocol"] = "chat-completions"
			return mustJSON(m)
		}()},
		{"streaming false", func() string {
			m := copyMap(base)
			m["streaming"] = false
			return mustJSON(m)
		}()},
		{"usage false", func() string {
			m := copyMap(base)
			m["usage"] = false
			return mustJSON(m)
		}()},
		{"hosted mode", func() string {
			m := copyMap(base)
			m["toolSearchModes"] = []string{"hosted"}
			return mustJSON(m)
		}()},
		{"duplicate modes", func() string {
			m := copyMap(base)
			m["toolSearchModes"] = []string{"client", "client"}
			return mustJSON(m)
		}()},
		{"client_bounded mixed", func() string {
			m := copyMap(base)
			m["toolSearchModes"] = []string{"client_bounded"}
			return mustJSON(m)
		}()},
		{"empty modes", func() string {
			m := copyMap(base)
			m["toolSearchModes"] = []string{}
			return mustJSON(m)
		}()},
		{"bad adapter", func() string {
			m := copyMap(base)
			m["verifiedAdapter"] = "agenticopenai/v9.9.9"
			return mustJSON(m)
		}()},
		{"lock zero", func() string {
			m := copyMap(base)
			m["verifiedLockVersion"] = 0
			return mustJSON(m)
		}()},
		{"digest short", func() string {
			m := copyMap(base)
			m["verifiedConfigDigest"] = "abc"
			return mustJSON(m)
		}()},
		{"digest uppercase", func() string {
			m := copyMap(base)
			m["verifiedConfigDigest"] = strings.Repeat("A", 64)
			return mustJSON(m)
		}()},
		{"null field", `{"schemaVersion":null,"protocol":"openai-responses-v1","streaming":true,"usage":true,"toolSearchModes":["client"],"reasoningReplay":"encrypted-or-none","verifiedAdapter":"agenticopenai/v0.2.2","verifiedAt":"2026-08-10T12:00:00Z","verifiedLockVersion":1,"verifiedConfigDigest":"` + digest + `"}`},
		{"array root", `[]`},
		{"missing field", `{"schemaVersion":"agentic-model.v1","protocol":"openai-responses-v1","streaming":true,"usage":true,"toolSearchModes":["client"],"reasoningReplay":"encrypted-or-none","verifiedAdapter":"agenticopenai/v0.2.2","verifiedAt":"2026-08-10T12:00:00Z","verifiedLockVersion":1}`},
		{"duplicate key", `{"schemaVersion":"agentic-model.v1","schemaVersion":"agentic-model.v1","protocol":"openai-responses-v1","streaming":true,"usage":true,"toolSearchModes":["client"],"reasoningReplay":"encrypted-or-none","verifiedAdapter":"agenticopenai/v0.2.2","verifiedAt":"2026-08-10T12:00:00Z","verifiedLockVersion":1,"verifiedConfigDigest":"` + digest + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := modelconfig.ParseAgenticCapabilities(json.RawMessage(tc.raw))
			if err == nil || !errors.Is(err, modelconfig.ErrInvalid) {
				t.Fatalf("expected ErrInvalid, got %v", err)
			}
		})
	}
}

func TestParseAgenticCapabilitiesV1NormalizeRemainsV1Bytes(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	digest := strings.Repeat("c", 64)
	docIn, err := modelconfig.CanonicalAgenticCapabilities(at, 4, digest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(docIn)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "toolCalling") {
		t.Fatalf("canonical v1 write must not emit toolCalling: %s", raw)
	}
	doc, normalized, err := modelconfig.ParseAgenticCapabilities(raw)
	if err != nil {
		t.Fatalf("parse v1: %v", err)
	}
	if doc.ToolCalling != modelconfig.ToolCallingNativeClientSearch {
		t.Fatalf("v1 must fill toolCalling in memory, got %q", doc.ToolCalling)
	}
	if strings.Contains(string(normalized), "toolCalling") {
		t.Fatalf("v1 normalize must remain v1 bytes: %s", normalized)
	}
	if !strings.Contains(string(normalized), `"schemaVersion":"agentic-model.v1"`) {
		t.Fatalf("v1 normalize lost schema: %s", normalized)
	}
	if string(normalized) != string(raw) {
		t.Fatalf("v1 normalize must round-trip canonical bytes\n got %s\nwant %s", normalized, raw)
	}
}

func TestParseAgenticCapabilitiesV2Rules(t *testing.T) {
	digest := strings.Repeat("d", 64)
	base := func(extra string) string {
		return `{"schemaVersion":"agentic-model.v2","protocol":"openai-responses-v1","streaming":true,"usage":true,` + extra + `"reasoningReplay":"encrypted-or-none","verifiedAdapter":"agenticopenai/v0.2.2","verifiedAt":"2026-08-10T12:00:00Z","verifiedLockVersion":2,"verifiedConfigDigest":"` + digest + `"}`
	}

	t.Run("function_calling omits toolSearchModes", func(t *testing.T) {
		doc, normalized, err := modelconfig.ParseAgenticCapabilities(json.RawMessage(base(`"toolCalling":"function_calling",`)))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if doc.ToolCalling != modelconfig.ToolCallingFunctionCalling || len(doc.ToolSearchModes) != 0 {
			t.Fatalf("unexpected doc: %+v", doc)
		}
		if strings.Contains(string(normalized), "toolSearchModes") {
			t.Fatalf("normalize must omit toolSearchModes: %s", normalized)
		}
	})
	t.Run("none omits empty toolSearchModes", func(t *testing.T) {
		doc, normalized, err := modelconfig.ParseAgenticCapabilities(json.RawMessage(base(`"toolCalling":"none","toolSearchModes":[],`)))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if doc.ToolCalling != modelconfig.ToolCallingNone || doc.ToolSearchModes != nil {
			t.Fatalf("unexpected doc: %+v", doc)
		}
		if strings.Contains(string(normalized), "toolSearchModes") {
			t.Fatalf("normalize must omit empty toolSearchModes: %s", normalized)
		}
	})
	t.Run("native requires client modes", func(t *testing.T) {
		doc, normalized, err := modelconfig.ParseAgenticCapabilities(json.RawMessage(base(`"toolCalling":"native_client_search","toolSearchModes":["client"],`)))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if doc.ToolCalling != modelconfig.ToolCallingNativeClientSearch ||
			len(doc.ToolSearchModes) != 1 || doc.ToolSearchModes[0] != modelconfig.AgenticToolSearchModeClient {
			t.Fatalf("unexpected doc: %+v", doc)
		}
		if !strings.Contains(string(normalized), `"toolCalling":"native_client_search"`) ||
			!strings.Contains(string(normalized), `"schemaVersion":"agentic-model.v2"`) {
			t.Fatalf("v2 native normalize: %s", normalized)
		}
	})

	rejects := []struct {
		name string
		raw  string
	}{
		{"unknown field", base(`"toolCalling":"function_calling","extra":1,`)},
		{"missing toolCalling", `{"schemaVersion":"agentic-model.v2","protocol":"openai-responses-v1","streaming":true,"usage":true,"reasoningReplay":"encrypted-or-none","verifiedAdapter":"agenticopenai/v0.2.2","verifiedAt":"2026-08-10T12:00:00Z","verifiedLockVersion":2,"verifiedConfigDigest":"` + digest + `"}`},
		{"case fold toolCalling", base(`"toolCalling":"Function_Calling",`)},
		{"native missing modes", base(`"toolCalling":"native_client_search",`)},
		{"function_calling with client modes", base(`"toolCalling":"function_calling","toolSearchModes":["client"],`)},
		{"v1 with toolCalling", `{"schemaVersion":"agentic-model.v1","protocol":"openai-responses-v1","streaming":true,"usage":true,"toolCalling":"native_client_search","toolSearchModes":["client"],"reasoningReplay":"encrypted-or-none","verifiedAdapter":"agenticopenai/v0.2.2","verifiedAt":"2026-08-10T12:00:00Z","verifiedLockVersion":2,"verifiedConfigDigest":"` + digest + `"}`},
	}
	for _, tc := range rejects {
		t.Run("reject "+tc.name, func(t *testing.T) {
			_, _, err := modelconfig.ParseAgenticCapabilities(json.RawMessage(tc.raw))
			if err == nil || !errors.Is(err, modelconfig.ErrInvalid) {
				t.Fatalf("expected ErrInvalid, got %v", err)
			}
		})
	}
}

func TestWireConfigDigestStableAndSensitiveFree(t *testing.T) {
	a := modelconfig.Config{
		Provider: "openai", APIBase: "https://api.example/v1", ModelName: "gpt",
		Options: json.RawMessage(`{"temperature":0}`), RuntimeCapabilities: json.RawMessage(`{}`),
	}
	b := a
	if modelconfig.WireConfigDigest(a) != modelconfig.WireConfigDigest(b) {
		t.Fatal("digest not stable")
	}
	b.ModelName = "other"
	if modelconfig.WireConfigDigest(a) == modelconfig.WireConfigDigest(b) {
		t.Fatal("model name change must rotate digest")
	}
	d := modelconfig.WireConfigDigest(a)
	if len(d) != 64 || strings.Contains(d, "api") {
		t.Fatalf("unexpected digest %q", d)
	}
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
