package contextwindow_test

import (
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/contextwindow"
)

func TestAssembleInjectsUntrustedSummaryNotSystem(t *testing.T) {
	base := time.Now().UTC()
	prior := []contextwindow.Turn{
		{User: contextwindow.HistoryMessage{ID: "u1", Role: "USER", Content: "old", CreatedAt: base}},
		{User: contextwindow.HistoryMessage{ID: "u2", Role: "USER", Content: "new", CreatedAt: base.Add(time.Second)}},
	}
	plan, err := contextwindow.AssembleTokenWindow(contextwindow.AssemblerInput{
		ModelContextWindowTokens: 8000,
		OutputReserveTokens:      500,
		SafetyMarginTokens:       50,
		TokenizerProfile:         contextwindow.ProfileByteUpperBound,
		SystemPrompt:             "sys",
		PriorTurns:               prior,
		CurrentUser:              contextwindow.HistoryMessage{ID: "uc", Role: "USER", Content: "now"},
		OptionalSummary:          "earlier topic was cats",
		PolicyMode:               "rolling_summary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.SummaryInjected {
		t.Fatal("expected summary injected")
	}
	// SYSTEM then summary ASSISTANT then history then current USER
	if len(plan.PromptMessages) < 3 {
		t.Fatalf("msgs: %+v", plan.PromptMessages)
	}
	if plan.PromptMessages[0].Role != contextwindow.RoleSystem {
		t.Fatalf("first must be system: %+v", plan.PromptMessages[0])
	}
	if plan.PromptMessages[1].Role != contextwindow.RoleAssistant {
		t.Fatalf("summary must be assistant: %+v", plan.PromptMessages[1])
	}
	if !strings.Contains(plan.PromptMessages[1].Content, contextwindow.UntrustedSummaryPrefix) {
		t.Fatalf("missing untrusted prefix")
	}
}
