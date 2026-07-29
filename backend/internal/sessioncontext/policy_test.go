package sessioncontext_test

import (
	"encoding/json"
	"errors"
	"testing"

	"actweave/backend/internal/sessioncontext"
)

func TestParsePolicyEmpty(t *testing.T) {
	_, raw, err := sessioncontext.ParsePolicy(json.RawMessage(`{}`))
	if err != nil || string(raw) != "{}" {
		t.Fatalf("empty: raw=%s err=%v", raw, err)
	}
}

func TestParsePolicyValidTokenWindow(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v1",
		"mode":"token_window",
		"maxInputTokens":0,
		"outputReserveTokens":4096,
		"safetyMarginTokens":2048,
		"maxRecentTurns":0
	}`)
	doc, normalized, err := sessioncontext.ParsePolicy(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Mode != sessioncontext.ModeTokenWindow {
		t.Fatalf("mode: %s", doc.Mode)
	}
	if string(normalized) == "" {
		t.Fatal("expected normalized body")
	}
}

func TestParsePolicyRejects(t *testing.T) {
	cases := []string{
		`{"schemaVersion":"session-context-policy.v1","mode":"token_window","hack":true}`,
		`{"schemaVersion":"other.v1","mode":"token_window"}`,
		`{"schemaVersion":"session-context-policy.v1","mode":"full_history"}`,
		`{"schemaVersion":"session-context-policy.v1","mode":"token_window","maxInputTokens":100,"outputReserveTokens":100}`,
		`{"schemaVersion":"session-context-policy.v1","summary":{"evil":1}}`,
	}
	for _, raw := range cases {
		_, _, err := sessioncontext.ParsePolicy(json.RawMessage(raw))
		if err == nil || !errors.Is(err, sessioncontext.ErrInvalidPolicy) {
			t.Fatalf("expected ErrInvalidPolicy for %s, got %v", raw, err)
		}
	}
}
