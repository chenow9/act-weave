package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/contextwindow"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/sessioncontext"

	"github.com/cloudwego/eino/components/model"
)

func TestMaybeCompactGateOffNoop(t *testing.T) {
	b := &Bridge{}
	policy := sessioncontext.ResolvedSnapshot{
		SchemaVersion: sessioncontext.SnapshotSchemaV1,
		Mode:          sessioncontext.ModeTokenWindow,
		Sources:       sessioncontext.SnapshotSources{CompactionGateEnabled: false},
	}
	out := b.maybeCompactForInitialRun(context.Background(), agentrunJob{
		WorkspaceID: "11111111-1111-1111-1111-111111111111",
		SessionID:   "22222222-2222-2222-2222-222222222222",
		RunID:       "33333333-3333-3333-3333-333333333333",
	}, execution.AgentRun{}, policy, "sys", nil,
		contextwindow.HistoryMessage{ID: "c", Role: "USER", Content: "now", ContentHash: "h"},
		nil,
	)
	if !out.UsedTokenWindowOnly || out.OptionalSummary != "" || out.HardFail != nil {
		t.Fatalf("gate-off: %+v", out)
	}
}

func TestMaybeCompactShadowSkipsLLM(t *testing.T) {
	b := &Bridge{compact: &CompactDependencies{IsShadow: true}}
	policy := v2PolicyForTest(true)
	prior := largePriorTurns(8)
	out := b.maybeCompactForInitialRun(context.Background(), agentrunJob{
		WorkspaceID: "11111111-1111-1111-1111-111111111111",
		SessionID:   "22222222-2222-2222-2222-222222222222",
		RunID:       "33333333-3333-3333-3333-333333333333",
	}, execution.AgentRun{ModelSnapshot: json.RawMessage(`{"id":"m1","provider":"openai","modelName":"gpt"}`)},
		policy, "sys", nil,
		contextwindow.HistoryMessage{ID: "c08f1f2e-7b5a-7c3d-8e9f-1234567890f9", Role: "USER", Content: "now", ContentHash: "h"},
		prior,
	)
	if out.HardFail != nil {
		t.Fatalf("shadow hardfail: %v", out.HardFail)
	}
	if !out.UsedTokenWindowOnly {
		t.Fatalf("shadow must stay token_window: %+v", out)
	}
	if out.OptionalSummary != "" {
		t.Fatalf("shadow must not inject summary")
	}
}

func TestMaybeCompactMissingDepsFallback(t *testing.T) {
	// Triggered compact without Summaries/Runs → UsedTokenWindowOnly (no hard fail when lifecycle absent).
	b := &Bridge{compact: &CompactDependencies{}}
	policy := v2PolicyForTest(true)
	prior := largePriorTurns(12)
	out := b.maybeCompactForInitialRun(context.Background(), agentrunJob{
		WorkspaceID: "11111111-1111-1111-1111-111111111111",
		SessionID:   "22222222-2222-2222-2222-222222222222",
		RunID:       "33333333-3333-3333-3333-333333333333",
	}, execution.AgentRun{ModelSnapshot: json.RawMessage(`{"id":"m1","provider":"openai","modelName":"gpt"}`)},
		policy, "sys", nil,
		contextwindow.HistoryMessage{ID: "c08f1f2e-7b5a-7c3d-8e9f-1234567890f9", Role: "USER", Content: "now", ContentHash: "h"},
		prior,
	)
	if out.HardFail != nil {
		t.Fatalf("missing deps without Runs must not hard-fail: %v", out.HardFail)
	}
	if !out.UsedTokenWindowOnly {
		t.Fatalf("expected token_window fallback: %+v", out)
	}
}

func TestNewCompactModelFromSnapshotRequiresBuilderAndID(t *testing.T) {
	_, err := NewCompactModelFromSnapshot(context.Background(), nil, execution.AgentRun{})
	if err == nil {
		t.Fatal("expected builder required")
	}
	_, err = NewCompactModelFromSnapshot(context.Background(),
		func(context.Context, modelconfig.Config) (model.AgenticModel, error) {
			return nil, errors.New("should not build")
		}, execution.AgentRun{ModelSnapshot: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected missing model id error")
	}
	// Valid snapshot id path reaches AgenticModel builder.
	called := false
	_, err = NewCompactModelFromSnapshot(context.Background(),
		func(_ context.Context, cfg modelconfig.Config) (model.AgenticModel, error) {
			called = true
			if cfg.ID != "model-1" {
				t.Fatalf("id=%s", cfg.ID)
			}
			return nil, errors.New("build refused")
		}, execution.AgentRun{
			WorkspaceID:   "11111111-1111-1111-1111-111111111111",
			ModelSnapshot: json.RawMessage(`{"id":"model-1","provider":"openai","modelName":"gpt-4o"}`),
		})
	if !called {
		t.Fatal("expected AgenticModel builder call")
	}
	if err == nil || !strings.Contains(err.Error(), "build refused") {
		t.Fatalf("err=%v", err)
	}
}

func TestTimeDurationMs(t *testing.T) {
	if timeDurationMs(0) != 0 || timeDurationMs(-1) != 0 {
		t.Fatal("non-positive")
	}
	if timeDurationMs(1000).Milliseconds() != 1000 {
		t.Fatal("1s")
	}
}

// D-06: production CompactDependencies surface is required for OptionalSummary success.
// Full Coordinator success needs DB-backed Runs/Summaries; incomplete deps must not panic.
func TestCompactDependenciesIncompleteFallsBack(t *testing.T) {
	b := &Bridge{compact: &CompactDependencies{IsShadow: false}}
	out := b.maybeCompactForInitialRun(context.Background(), agentrunJob{
		WorkspaceID: "11111111-1111-1111-1111-111111111111",
		SessionID:   "22222222-2222-2222-2222-222222222222",
		RunID:       "33333333-3333-3333-3333-333333333333",
	}, execution.AgentRun{ModelSnapshot: json.RawMessage(`{"id":"m1","provider":"openai","modelName":"gpt"}`)},
		v2PolicyForTest(true), "sys", nil,
		contextwindow.HistoryMessage{ID: "c08f1f2e-7b5a-7c3d-8e9f-1234567890f9", Role: "USER", Content: "now", ContentHash: "h"},
		largePriorTurns(12),
	)
	if out.HardFail != nil {
		t.Fatalf("hardfail: %v", out.HardFail)
	}
	if !out.UsedTokenWindowOnly || out.OptionalSummary != "" {
		t.Fatalf("incomplete deps must token_window without summary: %+v", out)
	}
}

func v2PolicyForTest(gate bool) sessioncontext.ResolvedSnapshot {
	return sessioncontext.ResolvedSnapshot{
		SchemaVersion:            sessioncontext.SnapshotSchemaV2,
		Mode:                     sessioncontext.ModeRollingSummary,
		ModelContextWindowTokens: 8000,
		EffectiveMaxInputTokens:  200, // tiny ceiling forces 80% trigger with large prior
		OutputReserveTokens:      0,
		SafetyMarginTokens:       0,
		MaxRecentTurns:           2,
		TokenizerProfile:         "o200k_base",
		Compaction: &sessioncontext.CompactionSnapshot{
			TriggerBps: sessioncontext.TriggerBps, TargetBps: sessioncontext.TargetBps,
			MaxSummaryTokens: 256, MinEvictedTurns: 1, MaxGenerationPasses: 2,
			TemplateVersion: sessioncontext.DefaultCompactionTemplateVersion,
			TemplateHash:    sessioncontext.DefaultCompactionTemplateHash(),
			TotalTimeoutMs:  45000, PerPassTimeoutMs: 20000, ClaimWaitMs: 1000,
		},
		AAP:     &sessioncontext.AAPSnapshot{IncludeCompactionSummary: false},
		Sources: sessioncontext.SnapshotSources{CompactionGateEnabled: gate, GateEnabled: true},
	}
}

func largePriorTurns(n int) []contextwindow.Turn {
	ids := []string{
		"a0000000-0000-7000-8000-000000000001",
		"a0000000-0000-7000-8000-000000000002",
		"a0000000-0000-7000-8000-000000000003",
		"a0000000-0000-7000-8000-000000000004",
		"a0000000-0000-7000-8000-000000000005",
		"a0000000-0000-7000-8000-000000000006",
		"a0000000-0000-7000-8000-000000000007",
		"a0000000-0000-7000-8000-000000000008",
		"a0000000-0000-7000-8000-000000000009",
		"a0000000-0000-7000-8000-00000000000a",
		"a0000000-0000-7000-8000-00000000000b",
		"a0000000-0000-7000-8000-00000000000c",
	}
	blob := strings.Repeat("history word ", 40)
	out := make([]contextwindow.Turn, 0, n)
	for i := 0; i < n && i < len(ids); i++ {
		aid := "b0000000-0000-7000-8000-0000000000" + ids[i][len(ids[i])-2:]
		out = append(out, contextwindow.Turn{
			User: contextwindow.HistoryMessage{
				ID: ids[i], Role: "USER", Content: blob, ContentHash: "h" + ids[i],
			},
			Assistants: []contextwindow.HistoryMessage{{
				ID: aid, Role: "ASSISTANT", Content: blob, ContentHash: "a" + ids[i],
			}},
		})
	}
	return out
}
