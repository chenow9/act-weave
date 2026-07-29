package sessioncontext_test

import (
	"encoding/json"
	"errors"
	"testing"

	"actweave/backend/internal/sessioncontext"
)

func TestParseResolvedSnapshotLegacy(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"memory":false,"maxTurns":10}`),
	} {
		doc, err := sessioncontext.ParseResolvedSnapshot(raw)
		if err != nil || doc.Mode != sessioncontext.ModeLegacy {
			t.Fatalf("legacy parse raw=%s doc=%+v err=%v", raw, doc, err)
		}
	}
}

func TestParseResolvedSnapshotUnknownVersion(t *testing.T) {
	_, err := sessioncontext.ParseResolvedSnapshot(json.RawMessage(`{"schemaVersion":"session-context.v9"}`))
	if !errors.Is(err, sessioncontext.ErrUnsupportedSnapshot) {
		t.Fatalf("expected unsupported, got %v", err)
	}
}

func TestResolvePriorityAndClamp(t *testing.T) {
	ws := json.RawMessage(`{"schemaVersion":"session-context-policy.v1","mode":"token_window","maxInputTokens":50000,"outputReserveTokens":1000,"safetyMarginTokens":1000}`)
	ag := json.RawMessage(`{"schemaVersion":"session-context-policy.v1","maxInputTokens":20000,"outputReserveTokens":5000}`)
	doc, raw, err := sessioncontext.Resolve(sessioncontext.ResolveInput{
		WorkspacePolicy:            ws,
		AgentPolicy:                ag,
		ContextWindowTokens:        128000,
		DefaultOutputReserveTokens: 4096,
		OutputTokenLimitMode:       "max_tokens",
		TokenizerProfile:           "o200k_base",
		TokenizerVersion:           "2026-01",
		GateEnabled:                true,
		RolloutVersion:             "context-window-test",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Agent maxInputTokens=20000 tightens workspace 50000.
	if doc.EffectiveMaxInputTokens != 20000 {
		t.Fatalf("effective max: %d", doc.EffectiveMaxInputTokens)
	}
	// Reserve is max(model default 4096, agent 5000)=5000
	if doc.OutputReserveTokens != 5000 {
		t.Fatalf("reserve: %d", doc.OutputReserveTokens)
	}
	if doc.SchemaVersion != sessioncontext.SnapshotSchemaV1 {
		t.Fatalf("schema: %s", doc.SchemaVersion)
	}
	if !json.Valid(raw) {
		t.Fatal("invalid raw")
	}
	parsed, err := sessioncontext.ParseResolvedSnapshot(raw)
	if err != nil || parsed.EffectiveMaxInputTokens != 20000 {
		t.Fatalf("round-trip: %+v err=%v", parsed, err)
	}
}
