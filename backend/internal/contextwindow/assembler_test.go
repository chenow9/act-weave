package contextwindow_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/contextwindow"
)

func TestAssembleTokenWindowEmptyHistory(t *testing.T) {
	plan, err := contextwindow.AssembleTokenWindow(contextwindow.AssemblerInput{
		ModelContextWindowTokens: 8000,
		OutputReserveTokens:      1000,
		SafetyMarginTokens:       100,
		TokenizerProfile:         contextwindow.ProfileByteUpperBound,
		SystemPrompt:             "You are helpful.",
		CurrentUser:              contextwindow.HistoryMessage{ID: "u-cur", Role: "USER", Content: "hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SelectedTurnCount != 0 || plan.OmittedTurnCount != 0 {
		t.Fatalf("plan: %+v", plan)
	}
	if plan.EstimatedTotalTokens <= 0 || plan.EstimatedTotalTokens > plan.EffectiveInputTokens {
		t.Fatalf("budget: %+v", plan)
	}
	// SYSTEM + current USER only in prompt
	if len(plan.PromptMessages) != 2 {
		t.Fatalf("prompt msgs: %+v", plan.PromptMessages)
	}
}

func TestAssembleTokenWindowSelectsRecentSuffix(t *testing.T) {
	base := time.Now().UTC()
	// Three short turns; budget should keep some recent ones.
	prior := []contextwindow.Turn{
		{User: contextwindow.HistoryMessage{ID: "u1", Role: "USER", Content: "old", CreatedAt: base}},
		{User: contextwindow.HistoryMessage{ID: "u2", Role: "USER", Content: "mid", CreatedAt: base.Add(time.Second)},
			Assistants: []contextwindow.HistoryMessage{{ID: "a2", Role: "ASSISTANT", Content: "mid-a", CreatedAt: base.Add(2 * time.Second)}}},
		{User: contextwindow.HistoryMessage{ID: "u3", Role: "USER", Content: "new", CreatedAt: base.Add(3 * time.Second)}},
	}
	plan, err := contextwindow.AssembleTokenWindow(contextwindow.AssemblerInput{
		ModelContextWindowTokens: 4000,
		OutputReserveTokens:      500,
		SafetyMarginTokens:       50,
		MaxRecentTurns:           2,
		TokenizerProfile:         contextwindow.ProfileByteUpperBound,
		SystemPrompt:             "sys",
		PriorTurns:               prior,
		CurrentUser:              contextwindow.HistoryMessage{ID: "uc", Role: "USER", Content: "now"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SelectedTurnCount != 2 || plan.OmittedTurnCount != 1 {
		t.Fatalf("expected 2 selected / 1 omitted, got %+v", plan)
	}
	// Continuous suffix: last two turns u2,u3 not u1,u3.
	if plan.IncludedMessages[0].ID != "u2" {
		t.Fatalf("expected continuous suffix starting u2, got %+v", plan.IncludedMessages)
	}
}

func TestAssembleTokenWindowMandatoryTooLarge(t *testing.T) {
	_, err := contextwindow.AssembleTokenWindow(contextwindow.AssemblerInput{
		ModelContextWindowTokens: 100,
		OutputReserveTokens:      50,
		SafetyMarginTokens:       40,
		TokenizerProfile:         contextwindow.ProfileByteUpperBound,
		SystemPrompt:             strings.Repeat("S", 5000),
		CurrentUser:              contextwindow.HistoryMessage{ID: "u", Role: "USER", Content: strings.Repeat("U", 5000)},
	})
	if !errors.Is(err, contextwindow.ErrRequiredInputTooLarge) {
		t.Fatalf("got %v", err)
	}
}

func TestAssembleTokenWindowStopsAtFirstUnfitTurn(t *testing.T) {
	// Middle turn is huge; older turns must not be pulled in.
	base := time.Now().UTC()
	huge := strings.Repeat("漢", 2000)
	prior := []contextwindow.Turn{
		{User: contextwindow.HistoryMessage{ID: "u1", Role: "USER", Content: "tiny-old", CreatedAt: base}},
		{User: contextwindow.HistoryMessage{ID: "u2", Role: "USER", Content: huge, CreatedAt: base.Add(time.Second)}},
		{User: contextwindow.HistoryMessage{ID: "u3", Role: "USER", Content: "tiny-new", CreatedAt: base.Add(2 * time.Second)}},
	}
	plan, err := contextwindow.AssembleTokenWindow(contextwindow.AssemblerInput{
		ModelContextWindowTokens: 2000,
		OutputReserveTokens:      200,
		SafetyMarginTokens:       50,
		TokenizerProfile:         contextwindow.ProfileByteUpperBound,
		SystemPrompt:             "sys",
		PriorTurns:               prior,
		CurrentUser:              contextwindow.HistoryMessage{ID: "uc", Role: "USER", Content: "now"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Should include u3 and maybe not u2; must not include u1 if u2 was skipped.
	for _, m := range plan.IncludedMessages {
		if m.ID == "u1" {
			t.Fatalf("must not skip over unfit middle turn: %+v", plan.IncludedMessages)
		}
	}
}
