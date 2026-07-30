package sessioncontext_test

import (
	"encoding/json"
	"errors"
	"testing"

	"actweave/backend/internal/sessioncontext"
)

func TestResolveV2WhenCompactionGateEnabled(t *testing.T) {
	ag := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"mode":"rolling_summary",
		"maxRecentTurns":20,
		"summary":{"maxTokens":1024,"minEvictedTurns":2,"maxGenerationPasses":3},
		"aap":{"includeCompactionSummary":true}
	}`)
	doc, raw, err := sessioncontext.Resolve(sessioncontext.ResolveInput{
		AgentPolicy:                ag,
		ContextWindowTokens:        128000,
		DefaultOutputReserveTokens: 4096,
		OutputTokenLimitMode:       "max_tokens",
		TokenizerProfile:           "o200k_base",
		TokenizerVersion:           "2026-01",
		GateEnabled:                true,
		RolloutVersion:             "session-context-test",
		CompactionGateEnabled:      true,
		CompactionRolloutVersion:   "context-compaction-test",
		AgentLockVersion:           9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != sessioncontext.SnapshotSchemaV2 {
		t.Fatalf("schema=%s", doc.SchemaVersion)
	}
	if doc.Compaction == nil || doc.Compaction.TriggerBps != sessioncontext.TriggerBps ||
		doc.Compaction.TargetBps != sessioncontext.TargetBps {
		t.Fatalf("compaction=%+v", doc.Compaction)
	}
	if doc.Compaction.TotalTimeoutMs != sessioncontext.DefaultTotalTimeoutMs ||
		doc.Compaction.PerPassTimeoutMs != sessioncontext.DefaultPerPassTimeoutMs ||
		doc.Compaction.ClaimWaitMs != sessioncontext.DefaultClaimWaitMs {
		t.Fatalf("timeouts=%+v", doc.Compaction)
	}
	if doc.Compaction.MaxSummaryTokens != 1024 || doc.Compaction.MaxGenerationPasses != 3 {
		t.Fatalf("summary knobs=%+v", doc.Compaction)
	}
	if doc.AAP == nil || !doc.AAP.IncludeCompactionSummary {
		t.Fatalf("aap=%+v", doc.AAP)
	}
	if !doc.Sources.CompactionGateEnabled || doc.Sources.CompactionRolloutVersion != "context-compaction-test" {
		t.Fatalf("sources=%+v", doc.Sources)
	}
	// 80/60 not overridable via policy — already frozen constants.
	parsed, err := sessioncontext.ParseResolvedSnapshot(raw)
	if err != nil || parsed.SchemaVersion != sessioncontext.SnapshotSchemaV2 {
		t.Fatalf("round-trip: %+v err=%v", parsed, err)
	}
	if !sessioncontext.IncludeCompactionSummaryFromSnapshot(raw) {
		t.Fatal("include from snapshot")
	}
}

func TestResolveV1WhenCompactionGateOff(t *testing.T) {
	doc, _, err := sessioncontext.Resolve(sessioncontext.ResolveInput{
		AgentPolicy:                json.RawMessage(`{"schemaVersion":"session-context-policy.v1","mode":"token_window"}`),
		ContextWindowTokens:        128000,
		DefaultOutputReserveTokens: 4096,
		OutputTokenLimitMode:       "max_tokens",
		TokenizerProfile:           "o200k_base",
		TokenizerVersion:           "2026-01",
		GateEnabled:                true,
		CompactionGateEnabled:      false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != sessioncontext.SnapshotSchemaV1 || doc.Compaction != nil || doc.AAP != nil {
		t.Fatalf("expected v1 without compact: %+v", doc)
	}
}

func TestResolveV2DefaultDisclosureFalse(t *testing.T) {
	doc, _, err := sessioncontext.Resolve(sessioncontext.ResolveInput{
		AgentPolicy:                json.RawMessage(`{"schemaVersion":"session-context-policy.v1","mode":"rolling_summary"}`),
		ContextWindowTokens:        64000,
		DefaultOutputReserveTokens: 2048,
		OutputTokenLimitMode:       "max_tokens",
		TokenizerProfile:           "o200k_base",
		TokenizerVersion:           "2026-01",
		CompactionGateEnabled:      true,
		GateEnabled:                true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.AAP == nil || doc.AAP.IncludeCompactionSummary {
		t.Fatalf("default disclosure false: %+v", doc.AAP)
	}
}

func TestParseResolvedSnapshotUnknownVersionStillUnsupported(t *testing.T) {
	_, err := sessioncontext.ParseResolvedSnapshot(json.RawMessage(`{"schemaVersion":"session-context.v9","mode":"token_window","modelContextWindowTokens":1,"effectiveMaxInputTokens":1}`))
	if !errors.Is(err, sessioncontext.ErrUnsupportedSnapshot) {
		t.Fatalf("err=%v", err)
	}
}

func TestParseResolvedSnapshotV2RejectsWrongBps(t *testing.T) {
	// Build a valid v2 then tamper trigger.
	doc, raw, err := sessioncontext.Resolve(sessioncontext.ResolveInput{
		ContextWindowTokens:        128000,
		DefaultOutputReserveTokens: 4096,
		OutputTokenLimitMode:       "max_tokens",
		TokenizerProfile:           "o200k_base",
		TokenizerVersion:           "2026-01",
		CompactionGateEnabled:      true,
		GateEnabled:                true,
	})
	if err != nil || doc.Compaction == nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	comp := m["compaction"].(map[string]any)
	comp["triggerBps"] = 7999
	tampered, _ := json.Marshal(m)
	if _, err := sessioncontext.ParseResolvedSnapshot(tampered); err == nil {
		t.Fatal("expected reject wrong triggerBps")
	}
}

func TestEightySixtyNotInPolicyDocument(t *testing.T) {
	// Policy cannot carry trigger/target — unknown fields fail.
	raw := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"triggerBps":8000,
		"targetBps":6000
	}`)
	_, _, err := sessioncontext.ParsePolicyScoped(raw, sessioncontext.PolicyScopeAgent)
	if err == nil || !errors.Is(err, sessioncontext.ErrInvalidPolicy) {
		t.Fatalf("80/60 in policy err=%v", err)
	}
}
