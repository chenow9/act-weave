package einoruntime

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestNotifyModelTurn_RecordsToolOnlyAndUsage(t *testing.T) {
	t.Parallel()
	rec := &RecordingProjector{}
	// Tool-only: empty content/reasoning but has tool calls + usage.
	msg := &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID: "tc1", Type: "function",
			Function: schema.FunctionCall{Name: "lookup", Arguments: `{}`},
		}},
		ResponseMeta: &schema.ResponseMeta{
			Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
		},
	}
	ev := &adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Role: schema.Assistant, Message: msg,
	}}}
	if err := ProjectAgentEvent(context.Background(), ev, rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.ModelTurns) != 1 {
		t.Fatalf("want tool-only model turn recorded, got %d", len(rec.ModelTurns))
	}
	turn := rec.ModelTurns[0]
	if !turn.HasToolCalls || !turn.TokensKnown {
		t.Fatalf("turn=%+v", turn)
	}
	if turn.PromptTokens != 10 || turn.CompletionTokens != 3 || turn.TotalTokens != 13 {
		t.Fatalf("tokens=%+v", turn)
	}
}

func TestNotifyModelTurn_SkipsTrulyEmpty(t *testing.T) {
	t.Parallel()
	rec := &RecordingProjector{}
	ev := &adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Role: schema.Assistant, Message: schema.AssistantMessage("", nil),
	}}}
	if err := ProjectAgentEvent(context.Background(), ev, rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.ModelTurns) != 0 {
		t.Fatalf("empty turn should skip, got %+v", rec.ModelTurns)
	}
}
