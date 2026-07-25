package chatruntimebridge

import (
	"strings"
	"testing"

	"actweave/backend/internal/einoruntime"
)

func TestBuildModelTurnAuditPayload(t *testing.T) {
	turn := einoruntime.ModelTurn{Content: " out ", Reasoning: " secret "}
	off := buildModelTurnAuditPayload(turn, true, false)
	if off["content"] != "out" {
		t.Fatalf("content trim = %#v", off["content"])
	}
	if _, ok := off["reasoning"]; ok {
		t.Fatalf("debug off must omit reasoning: %#v", off)
	}
	on := buildModelTurnAuditPayload(turn, true, true)
	if on["reasoning"] != "secret" {
		t.Fatalf("debug on must store reasoning: %#v", on)
	}
}

func TestReasoningTextForAuditTokensOnly(t *testing.T) {
	// Gateway often bills reasoning_tokens without emitting reasoning_content.
	got := reasoningTextForAudit(einoruntime.ModelTurn{ReasoningTokens: 33})
	if got == "" || !strings.Contains(got, "reasoning_tokens=33") {
		t.Fatalf("tokens-only audit text = %q", got)
	}
	if reasoningTextForAudit(einoruntime.ModelTurn{}) != "" {
		t.Fatal("empty turn must not invent reasoning")
	}
	if reasoningTextForAudit(einoruntime.ModelTurn{Reasoning: " plan ", ReasoningTokens: 9}) != "plan" {
		t.Fatal("prefer provider text over tokens note")
	}
}
