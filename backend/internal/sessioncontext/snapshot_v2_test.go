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
	if doc.AAP == nil || doc.AAP.IncludeCompactionSummary || doc.AAP.EnableA2UI {
		t.Fatalf("default disclosure/capability false: %+v", doc.AAP)
	}
}

// KD-12: enableA2UI=true with compaction gate off still emits full v2 + platform-default
// compaction, while sources.compactionGateEnabled remains false (compact path stays off).
func TestResolveEnableA2UIGateOffEmitsV2WithPlatformDefaultCompaction(t *testing.T) {
	ag := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"mode":"token_window",
		"aap":{"enableA2UI":true}
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
		CompactionGateEnabled:      false,
		CompactionRolloutVersion:   "",
		AgentLockVersion:           3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != sessioncontext.SnapshotSchemaV2 {
		t.Fatalf("schema=%s want v2", doc.SchemaVersion)
	}
	if doc.Compaction == nil {
		t.Fatal("expected platform-default compaction block")
	}
	if doc.Compaction.TriggerBps != sessioncontext.TriggerBps ||
		doc.Compaction.TargetBps != sessioncontext.TargetBps {
		t.Fatalf("bps: %+v", doc.Compaction)
	}
	if doc.Compaction.TotalTimeoutMs != sessioncontext.DefaultTotalTimeoutMs ||
		doc.Compaction.PerPassTimeoutMs != sessioncontext.DefaultPerPassTimeoutMs ||
		doc.Compaction.ClaimWaitMs != sessioncontext.DefaultClaimWaitMs {
		t.Fatalf("timeouts: %+v", doc.Compaction)
	}
	if doc.Compaction.MaxSummaryTokens != 2048 || doc.Compaction.MinEvictedTurns != 4 ||
		doc.Compaction.MaxGenerationPasses != 2 {
		t.Fatalf("platform summary knobs: %+v", doc.Compaction)
	}
	if doc.Compaction.TemplateVersion != sessioncontext.DefaultCompactionTemplateVersion ||
		doc.Compaction.TemplateHash != sessioncontext.DefaultCompactionTemplateHash() {
		t.Fatalf("template: %+v", doc.Compaction)
	}
	if doc.AAP == nil || !doc.AAP.EnableA2UI {
		t.Fatalf("aap.enableA2UI: %+v", doc.AAP)
	}
	if doc.AAP.IncludeCompactionSummary {
		t.Fatalf("include should remain false: %+v", doc.AAP)
	}
	// Gate remains false — placeholder compaction must not open compact runtime.
	if doc.Sources.CompactionGateEnabled {
		t.Fatalf("compactionGateEnabled must stay false: %+v", doc.Sources)
	}
	if !doc.Sources.GateEnabled {
		t.Fatalf("session-context gate should still reflect input: %+v", doc.Sources)
	}

	parsed, err := sessioncontext.ParseResolvedSnapshot(raw)
	if err != nil || parsed.SchemaVersion != sessioncontext.SnapshotSchemaV2 {
		t.Fatalf("round-trip: %+v err=%v", parsed, err)
	}
	if !sessioncontext.EnableA2UIFromSnapshot(raw) {
		t.Fatal("EnableA2UIFromSnapshot expected true")
	}
	if sessioncontext.IncludeCompactionSummaryFromSnapshot(raw) {
		t.Fatal("include from snapshot expected false")
	}
	if parsed.Sources.CompactionGateEnabled {
		t.Fatal("parsed sources.compactionGateEnabled must be false")
	}
}

func TestResolveEnableA2UIFalseGateOffStaysV1(t *testing.T) {
	// enableA2UI false + gate off: behaviour identical to today (no v2 upgrade).
	doc, _, err := sessioncontext.Resolve(sessioncontext.ResolveInput{
		AgentPolicy: json.RawMessage(`{
			"schemaVersion":"session-context-policy.v2",
			"mode":"token_window",
			"aap":{"enableA2UI":false,"includeCompactionSummary":false}
		}`),
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
		t.Fatalf("expected v1 without compact/aap: schema=%s compaction=%v aap=%v",
			doc.SchemaVersion, doc.Compaction != nil, doc.AAP)
	}
}

func TestResolveEnableA2UIWithCompactionGateOn(t *testing.T) {
	ag := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"mode":"rolling_summary",
		"aap":{"enableA2UI":true,"includeCompactionSummary":true}
	}`)
	doc, raw, err := sessioncontext.Resolve(sessioncontext.ResolveInput{
		AgentPolicy:                ag,
		ContextWindowTokens:        128000,
		DefaultOutputReserveTokens: 4096,
		OutputTokenLimitMode:       "max_tokens",
		TokenizerProfile:           "o200k_base",
		TokenizerVersion:           "2026-01",
		GateEnabled:                true,
		CompactionGateEnabled:      true,
		CompactionRolloutVersion:   "context-compaction-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != sessioncontext.SnapshotSchemaV2 {
		t.Fatalf("schema=%s", doc.SchemaVersion)
	}
	if doc.AAP == nil || !doc.AAP.EnableA2UI || !doc.AAP.IncludeCompactionSummary {
		t.Fatalf("aap=%+v", doc.AAP)
	}
	if !doc.Sources.CompactionGateEnabled {
		t.Fatal("gate on must remain true")
	}
	if !sessioncontext.EnableA2UIFromSnapshot(raw) {
		t.Fatal("EnableA2UIFromSnapshot")
	}
}

func TestEnableA2UIFromSnapshotDefaults(t *testing.T) {
	if sessioncontext.EnableA2UIFromSnapshot(json.RawMessage(`{}`)) {
		t.Fatal("legacy should be false")
	}
	if sessioncontext.EnableA2UIFromSnapshot(json.RawMessage(`not-json`)) {
		t.Fatal("invalid should be false")
	}
	// v1 has no aap
	doc, raw, err := sessioncontext.Resolve(sessioncontext.ResolveInput{
		AgentPolicy:                json.RawMessage(`{"schemaVersion":"session-context-policy.v1","mode":"token_window"}`),
		ContextWindowTokens:        128000,
		DefaultOutputReserveTokens: 4096,
		OutputTokenLimitMode:       "max_tokens",
		TokenizerProfile:           "o200k_base",
		TokenizerVersion:           "2026-01",
		CompactionGateEnabled:      false,
		GateEnabled:                true,
	})
	if err != nil || doc.SchemaVersion != sessioncontext.SnapshotSchemaV1 {
		t.Fatalf("v1 setup: %+v err=%v", doc, err)
	}
	if sessioncontext.EnableA2UIFromSnapshot(raw) {
		t.Fatal("v1 enableA2UI false")
	}
}

// Frozen snapshot is independent of later agent policy edits (run create freeze).
func TestEnableA2UISnapshotFrozenAgainstAgentChange(t *testing.T) {
	agOn := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"mode":"token_window",
		"aap":{"enableA2UI":true}
	}`)
	_, frozen, err := sessioncontext.Resolve(sessioncontext.ResolveInput{
		AgentPolicy:                agOn,
		ContextWindowTokens:        128000,
		DefaultOutputReserveTokens: 4096,
		OutputTokenLimitMode:       "max_tokens",
		TokenizerProfile:           "o200k_base",
		TokenizerVersion:           "2026-01",
		CompactionGateEnabled:      false,
		GateEnabled:                true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sessioncontext.EnableA2UIFromSnapshot(frozen) {
		t.Fatal("frozen should be true")
	}
	// Re-resolve with enableA2UI off simulates agent patch after run create — frozen raw unchanged.
	agOff := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"mode":"token_window",
		"aap":{"enableA2UI":false}
	}`)
	_, later, err := sessioncontext.Resolve(sessioncontext.ResolveInput{
		AgentPolicy:                agOff,
		ContextWindowTokens:        128000,
		DefaultOutputReserveTokens: 4096,
		OutputTokenLimitMode:       "max_tokens",
		TokenizerProfile:           "o200k_base",
		TokenizerVersion:           "2026-01",
		CompactionGateEnabled:      false,
		GateEnabled:                true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sessioncontext.EnableA2UIFromSnapshot(later) {
		t.Fatal("new resolve with flag off should be false")
	}
	// Original frozen snapshot still reports true.
	if !sessioncontext.EnableA2UIFromSnapshot(frozen) {
		t.Fatal("in-flight run snapshot must not flip when agent policy changes")
	}
}

func TestParseResolvedSnapshotV2StillRequiresCompaction(t *testing.T) {
	// Do not relax ParseResolvedSnapshot: v2 without compaction is invalid even with aap.
	raw := json.RawMessage(`{
		"schemaVersion":"session-context.v2",
		"mode":"token_window",
		"modelContextWindowTokens":128000,
		"effectiveMaxInputTokens":100000,
		"outputReserveTokens":4096,
		"safetyMarginTokens":2048,
		"maxRecentTurns":0,
		"tokenizerProfile":"o200k_base",
		"tokenizerVersion":"2026-01",
		"outputTokenLimitMode":"max_tokens",
		"summary":null,
		"aap":{"includeCompactionSummary":false,"enableA2UI":true},
		"sources":{"gateEnabled":true,"compactionGateEnabled":false}
	}`)
	if _, err := sessioncontext.ParseResolvedSnapshot(raw); err == nil {
		t.Fatal("expected reject v2 without compaction block")
	}
	if sessioncontext.EnableA2UIFromSnapshot(raw) {
		t.Fatal("invalid snapshot must not report enableA2UI")
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
