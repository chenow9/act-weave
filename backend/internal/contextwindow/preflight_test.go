package contextwindow_test

import (
	"strings"
	"testing"

	"actweave/backend/internal/contextwindow"
)

func turn(id, user string, assistants ...string) contextwindow.Turn {
	t := contextwindow.Turn{
		User: contextwindow.HistoryMessage{ID: id + "-u", Role: "USER", Content: user, ContentHash: "h"},
	}
	for i, a := range assistants {
		t.Assistants = append(t.Assistants, contextwindow.HistoryMessage{
			ID: id + "-a" + string(rune('0'+i)), Role: "ASSISTANT", Content: a, ContentHash: "h",
		})
	}
	return t
}

func TestPlanCompactionBoundary7999Vs8000(t *testing.T) {
	// Craft small system + large uncovered history to control occupancy.
	// Use high max input and many turns so we cross 80%.
	sys := "sys"
	current := contextwindow.HistoryMessage{ID: "cur", Role: "USER", Content: "now", ContentHash: "h"}
	// Build many turns until over 80% of a tight ceiling.
	var turns []contextwindow.Turn
	for i := 0; i < 40; i++ {
		turns = append(turns, turn("t"+string(rune('a'+i%26))+string(rune('0'+i/26)),
			strings.Repeat("user text ", 20),
			strings.Repeat("assistant text ", 20),
		))
	}
	// First estimate unconstrained to pick ceiling just around boundary.
	// Use EffectiveMaxInputTokens small enough.
	// First pass with huge ceiling to learn token count, then set ceiling so occupancy >= 80%.
	probe, err := contextwindow.PlanCompaction(contextwindow.PreflightInput{
		EffectiveMaxInputTokens: 1_000_000,
		TokenizerProfile:        "o200k_base",
		SystemPrompt:            sys,
		UncoveredTurns:          turns,
		CurrentUser:             current,
		MaxRecentTurns:          20,
	})
	if err != nil {
		t.Fatal(err)
	}
	// ceiling such that tokens/ceiling >= 0.80 → ceiling <= tokens*10000/8000 = tokens*1.25
	// Use floor(tokens * 10000 / 8000) as ceiling so bps >= 8000.
	ceiling := (probe.TriggerInputTokens * 10000) / 8000
	if ceiling <= 0 {
		t.Fatal("bad ceiling")
	}
	base := contextwindow.PreflightInput{
		EffectiveMaxInputTokens: ceiling,
		TokenizerProfile:        "o200k_base",
		SystemPrompt:            sys,
		UncoveredTurns:          turns,
		CurrentUser:             current,
		MaxRecentTurns:          20,
	}
	plan, err := contextwindow.PlanCompaction(base)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Triggered {
		t.Fatalf("expected trigger occupancy=%d tokens=%d ceiling=%d", plan.OccupancyBps, plan.TriggerInputTokens, ceiling)
	}
	if len(plan.CoverageTurns) == 0 {
		t.Fatal("expected coverage turns")
	}
	// 79.99% case: tiny history under ceiling.
	small := contextwindow.PreflightInput{
		EffectiveMaxInputTokens: 100000,
		TokenizerProfile:        "o200k_base",
		SystemPrompt:            sys,
		UncoveredTurns:          []contextwindow.Turn{turn("s1", "hi", "hello")},
		CurrentUser:             current,
		MaxRecentTurns:          20,
	}
	plan2, err := contextwindow.PlanCompaction(small)
	if err != nil {
		t.Fatal(err)
	}
	if plan2.Triggered {
		t.Fatalf("small history must not trigger: bps=%d", plan2.OccupancyBps)
	}
}

func TestTriggerAndTargetBpsHelpers(t *testing.T) {
	// Exactly 80%: tokens*10000 == ceiling*8000
	// 8/10 = 80%
	ok, err := contextwindow.Triggered(8, 10)
	if err != nil || !ok {
		t.Fatalf("80%% trigger: %v %v", ok, err)
	}
	// 7/10 = 70%
	ok, err = contextwindow.Triggered(7, 10)
	if err != nil || ok {
		t.Fatalf("70%% no trigger: %v %v", ok, err)
	}
	// Exactly 60%
	ok, err = contextwindow.TargetMet(6, 10)
	if err != nil || !ok {
		t.Fatalf("60%% target: %v %v", ok, err)
	}
	// 60.01% → 6001 bps on large numbers
	// 6001/10000 of ceiling 10000 = 6001 tokens
	ok, err = contextwindow.TargetMet(6001, 10000)
	if err != nil || ok {
		t.Fatalf("60.01%% must fail target: %v %v", ok, err)
	}
	ok, err = contextwindow.TargetMet(6000, 10000)
	if err != nil || !ok {
		t.Fatalf("60.00%% must pass: %v %v", ok, err)
	}
}

func TestPlanCompactionMaxRecentOnlyAffectsSuffix(t *testing.T) {
	// Many turns, maxRecent=0 → suffix empty, all coverage when triggered.
	var turns []contextwindow.Turn
	for i := 0; i < 30; i++ {
		turns = append(turns, turn("m"+string(rune('a'+i%26)), strings.Repeat("x", 80), strings.Repeat("y", 80)))
	}
	probe, err := contextwindow.PlanCompaction(contextwindow.PreflightInput{
		EffectiveMaxInputTokens: 1_000_000,
		TokenizerProfile:        "o200k_base",
		SystemPrompt:            "s",
		UncoveredTurns:          turns,
		CurrentUser:             contextwindow.HistoryMessage{ID: "c", Role: "USER", Content: "now", ContentHash: "h"},
		MaxRecentTurns:          0,
	})
	if err != nil {
		t.Fatal(err)
	}
	ceiling := (probe.TriggerInputTokens * 10000) / 8000
	plan, err := contextwindow.PlanCompaction(contextwindow.PreflightInput{
		EffectiveMaxInputTokens: ceiling,
		TokenizerProfile:        "o200k_base",
		SystemPrompt:            "s",
		UncoveredTurns:          turns,
		CurrentUser:             contextwindow.HistoryMessage{ID: "c", Role: "USER", Content: "now", ContentHash: "h"},
		MaxRecentTurns:          0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Triggered {
		t.Fatalf("expected triggered bps=%d", plan.OccupancyBps)
	}
	// With maxRecent=0, suffix should be empty (or minimal forced split).
	if len(plan.CoverageTurns)+len(plan.RawSuffixTurns) != len(turns) {
		t.Fatalf("coverage+suffix must partition uncovered: %d+%d != %d",
			len(plan.CoverageTurns), len(plan.RawSuffixTurns), len(turns))
	}
}

func TestPlanCompactionMandatoryTooLarge(t *testing.T) {
	_, err := contextwindow.PlanCompaction(contextwindow.PreflightInput{
		EffectiveMaxInputTokens: 10,
		TokenizerProfile:        "o200k_base",
		SystemPrompt:            strings.Repeat("SYSTEM ", 500),
		CurrentUser:             contextwindow.HistoryMessage{ID: "c", Role: "USER", Content: strings.Repeat("u", 500), ContentHash: "h"},
	})
	if err != contextwindow.ErrRequiredInputTooLarge {
		t.Fatalf("err=%v", err)
	}
}

func TestPlanCompactionDeterministic(t *testing.T) {
	in := contextwindow.PreflightInput{
		EffectiveMaxInputTokens: 4000,
		TokenizerProfile:        "o200k_base",
		SystemPrompt:            "sys",
		UncoveredTurns: []contextwindow.Turn{
			turn("a", strings.Repeat("u", 100), strings.Repeat("a", 100)),
			turn("b", strings.Repeat("u", 100), strings.Repeat("a", 100)),
			turn("c", strings.Repeat("u", 100), strings.Repeat("a", 100)),
			turn("d", strings.Repeat("u", 100), strings.Repeat("a", 100)),
			turn("e", strings.Repeat("u", 100), strings.Repeat("a", 100)),
			turn("f", strings.Repeat("u", 100), strings.Repeat("a", 100)),
			turn("g", strings.Repeat("u", 100), strings.Repeat("a", 100)),
			turn("h", strings.Repeat("u", 100), strings.Repeat("a", 100)),
		},
		CurrentUser:    contextwindow.HistoryMessage{ID: "c", Role: "USER", Content: "now", ContentHash: "h"},
		MaxRecentTurns: 2,
	}
	a, err := contextwindow.PlanCompaction(in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := contextwindow.PlanCompaction(in)
	if err != nil {
		t.Fatal(err)
	}
	if a.Triggered != b.Triggered || a.OccupancyBps != b.OccupancyBps ||
		len(a.CoverageTurns) != len(b.CoverageTurns) || len(a.RawSuffixTurns) != len(b.RawSuffixTurns) {
		t.Fatalf("non-deterministic: %+v vs %+v", a, b)
	}
}
