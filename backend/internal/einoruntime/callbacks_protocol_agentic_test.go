package einoruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

type agenticProjectorSpy struct {
	deltas    []string
	completed []string
	turns     []ModelTurn
	deltaErr  error
}

func (s *agenticProjectorSpy) OnTextDelta(_ context.Context, delta string) error {
	s.deltas = append(s.deltas, delta)
	return s.deltaErr
}

func (s *agenticProjectorSpy) OnTextComplete(_ context.Context, full string) error {
	s.completed = append(s.completed, full)
	return nil
}

func (s *agenticProjectorSpy) OnModelTurn(_ context.Context, turn ModelTurn) error {
	s.turns = append(s.turns, turn)
	return nil
}

func agenticTextChunk(text string) *schema.AgenticMessage {
	return &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlockChunk(
				&schema.AssistantGenText{Text: text}, &schema.StreamingMeta{Index: 0}),
		},
	}
}

func agenticReasoningChunk(text string) *schema.AgenticMessage {
	return &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlockChunk(
				&schema.Reasoning{Text: text}, &schema.StreamingMeta{Index: 0}),
		},
	}
}

// TestProjectAgenticChunkDelta_NeverProjectsReasoningAsPublicText is the one
// projection rule with a privacy consequence. A turn streams reasoning chunks
// interleaved with text chunks; forwarding a chunk wholesale would publish the
// model's private reasoning to the end user as ordinary answer text.
func TestProjectAgenticChunkDelta_NeverProjectsReasoningAsPublicText(t *testing.T) {
	t.Parallel()
	spy := &agenticProjectorSpy{}
	ctx := context.Background()

	if err := projectAgenticChunkDelta(ctx, agenticReasoningChunk("secret plan"), spy); err != nil {
		t.Fatalf("reasoning chunk: %v", err)
	}
	if len(spy.deltas) != 0 {
		t.Fatalf("reasoning was projected as public text: %q", spy.deltas)
	}

	if err := projectAgenticChunkDelta(ctx, agenticTextChunk("hello "), spy); err != nil {
		t.Fatalf("text chunk: %v", err)
	}
	if err := projectAgenticChunkDelta(ctx, agenticTextChunk("world"), spy); err != nil {
		t.Fatalf("text chunk: %v", err)
	}
	if got := strings.Join(spy.deltas, ""); got != "hello world" {
		t.Fatalf("projected deltas = %q, want %q", got, "hello world")
	}
	// Chunk order is the delivery order; a client renders them as they arrive.
	if len(spy.deltas) != 2 {
		t.Fatalf("chunks were coalesced into %d deltas, want one per chunk", len(spy.deltas))
	}
}

func TestProjectAgenticChunkDelta_ToleratesNoProjector(t *testing.T) {
	t.Parallel()
	if err := projectAgenticChunkDelta(context.Background(), agenticTextChunk("x"), nil); err != nil {
		t.Fatalf("nil projector must be a no-op, got %v", err)
	}
}

// TestProjectAgenticChunkDelta_PropagatesProjectorFailure keeps a failed
// delivery from being swallowed: the drain aborts on this error, so a turn whose
// deltas could not be delivered is not reported as a completed turn.
func TestProjectAgenticChunkDelta_PropagatesProjectorFailure(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sink is gone")
	spy := &agenticProjectorSpy{deltaErr: sentinel}
	err := projectAgenticChunkDelta(context.Background(), agenticTextChunk("x"), spy)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the projector's error", err)
	}
}

// TestAgenticModelTurn_CarriesUsageAndToolEvidence pins the MODEL evidence a
// turn leaves behind. Tool-only turns are exactly the ones an operator cannot
// reconstruct from the transcript, and cached prompt tokens are the only
// observable proof that the cache-stable prompt is being rewarded upstream.
func TestAgenticModelTurn_CarriesUsageAndToolEvidence(t *testing.T) {
	t.Parallel()
	message := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{Text: "answer"}),
			schema.NewContentBlock(&schema.FunctionToolCall{
				CallID: "call_9", Name: "lookup", Arguments: `{}`,
			}),
		},
		ResponseMeta: &schema.AgenticResponseMeta{
			TokenUsage: &schema.TokenUsage{
				PromptTokens: 120, CompletionTokens: 7, TotalTokens: 127,
				PromptTokenDetails:      schema.PromptTokenDetails{CachedTokens: 96},
				CompletionTokensDetails: schema.CompletionTokensDetails{ReasoningTokens: 3},
			},
		},
	}
	turn, text, err := agenticModelTurn(message)
	if err != nil {
		t.Fatalf("agenticModelTurn: %v", err)
	}
	if text != "answer" {
		t.Fatalf("text = %q", text)
	}
	if !turn.TokensKnown {
		t.Fatal("usage was reported but the turn claims tokens are unknown")
	}
	if turn.PromptTokens != 120 || turn.CompletionTokens != 7 || turn.TotalTokens != 127 {
		t.Fatalf("token counts lost: %+v", turn)
	}
	if turn.CachedPromptTokens != 96 {
		t.Fatalf("cached prompt tokens = %d, want 96", turn.CachedPromptTokens)
	}
	if turn.ReasoningTokens != 3 {
		t.Fatalf("reasoning tokens = %d, want 3", turn.ReasoningTokens)
	}
	if !turn.HasToolCalls || len(turn.ToolCallIDs) != 1 || turn.ToolCallIDs[0] != "call_9" {
		t.Fatalf("tool evidence lost: %+v", turn)
	}
}

// TestProjectAgenticModelTurn_ReportsToolOnlyTurns guards the case where there
// is no text to complete: the turn must still reach the audit trail.
func TestProjectAgenticModelTurn_ReportsToolOnlyTurns(t *testing.T) {
	t.Parallel()
	spy := &agenticProjectorSpy{}
	message := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{
				CallID: "call_1", Name: "wire", Arguments: `{}`,
			}),
		},
	}
	if err := projectAgenticModelTurn(context.Background(), message, spy); err != nil {
		t.Fatalf("projectAgenticModelTurn: %v", err)
	}
	if len(spy.completed) != 0 {
		t.Fatalf("a tool-only turn completed item text: %q", spy.completed)
	}
	if len(spy.turns) != 1 || !spy.turns[0].HasToolCalls {
		t.Fatalf("tool-only turn left no MODEL evidence: %+v", spy.turns)
	}
}
