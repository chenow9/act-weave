package modelconfig_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/modelconfig"
)

func TestParseRuntimeCapabilitiesEmptyIsUnset(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`null`)} {
		doc, normalized, err := modelconfig.ParseRuntimeCapabilities(raw)
		if err != nil {
			t.Fatalf("empty parse: %v", err)
		}
		if doc.SchemaVersion != "" || string(normalized) != "{}" {
			t.Fatalf("expected empty unset, got doc=%+v raw=%s", doc, normalized)
		}
	}
}

func TestParseRuntimeCapabilitiesValid(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaVersion":"model-runtime.v1",
		"contextWindowTokens":128000,
		"defaultOutputReserveTokens":4096,
		"outputTokenLimitMode":"max_tokens",
		"tokenizerProfile":"o200k_base",
		"tokenizerVersion":"2026-01"
	}`)
	doc, normalized, err := modelconfig.ParseRuntimeCapabilities(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.ContextWindowTokens != 128000 || doc.TokenizerProfile != "o200k_base" {
		t.Fatalf("unexpected doc: %+v", doc)
	}
	if !json.Valid(normalized) || !strings.Contains(string(normalized), "model-runtime.v1") {
		t.Fatalf("unexpected normalized: %s", normalized)
	}
}

func TestParseRuntimeCapabilitiesRejects(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"unknown field", `{"schemaVersion":"model-runtime.v1","contextWindowTokens":1,"defaultOutputReserveTokens":1,"outputTokenLimitMode":"max_tokens","tokenizerProfile":"o200k_base","tokenizerVersion":"v","extra":1}`},
		{"unknown version", `{"schemaVersion":"model-runtime.v9","contextWindowTokens":10,"defaultOutputReserveTokens":1,"outputTokenLimitMode":"max_tokens","tokenizerProfile":"o200k_base","tokenizerVersion":"v"}`},
		{"budget amplify", `{"schemaVersion":"model-runtime.v1","contextWindowTokens":10,"defaultOutputReserveTokens":10,"outputTokenLimitMode":"max_tokens","tokenizerProfile":"o200k_base","tokenizerVersion":"v"}`},
		{"unknown tokenizer", `{"schemaVersion":"model-runtime.v1","contextWindowTokens":10,"defaultOutputReserveTokens":1,"outputTokenLimitMode":"max_tokens","tokenizerProfile":"guess-me","tokenizerVersion":"v"}`},
		{"unknown mode", `{"schemaVersion":"model-runtime.v1","contextWindowTokens":10,"defaultOutputReserveTokens":1,"outputTokenLimitMode":"n/a","tokenizerProfile":"o200k_base","tokenizerVersion":"v"}`},
		{"array", `[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := modelconfig.ParseRuntimeCapabilities(json.RawMessage(tc.raw))
			if err == nil || !errors.Is(err, modelconfig.ErrInvalid) {
				t.Fatalf("expected ErrInvalid, got %v", err)
			}
		})
	}
}
