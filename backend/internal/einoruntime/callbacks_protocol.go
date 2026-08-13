package einoruntime

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// ProtocolProjector is the PR6 testable surface for streaming protocol hooks.
//
// Full NativeProtocolRecorder / run_events wiring lands in PR7
// (chatruntimebridge projector). For PR6 we only need to prove true Stream
// multi-delta delivery (D14 / appendix A.1 item.delta).
type ProtocolProjector interface {
	// OnTextDelta is called for each non-empty assistant content chunk from
	// a true model Stream (item.delta equivalent).
	OnTextDelta(ctx context.Context, delta string) error
	// OnTextComplete is called once per assistant stream after all deltas,
	// with the concatenated text (item.completed content equivalent).
	// Empty content turns (reasoning-only / tool-only) skip this hook.
	OnTextComplete(ctx context.Context, full string) error
}

// ModelTurn is the aggregated assistant model-turn observation for audit/debug.
// Content is public assistant text; Reasoning is provider reasoning_content when
// present (may be empty). ReasoningTokens is from usage.completion_tokens_details
// when the provider reports it (even if reasoning_content text is omitted).
// Token fields come from schema.Message.ResponseMeta.Usage (stream: Eino max
// aggregation, not naive sum of cumulative partials).
// One ModelTurn maps to one ADK assistant MessageOutput (including tool-only turns).
type ModelTurn struct {
	Content          string
	Reasoning        string
	ReasoningTokens  int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	// CachedPromptTokens is the prompt prefix the provider served from its KV
	// cache. Reported by the Agentic path only, where it is the sole evidence
	// that prompt-cache-stable assembly is actually being rewarded upstream;
	// the classic projection leaves it zero.
	CachedPromptTokens int
	// TokensKnown is true only when the provider reported usage for this turn.
	TokensKnown bool
	// HasToolCalls is true when the assistant message requested tool invocation
	// (tool-only MODEL turns must still be audited even with empty content).
	HasToolCalls bool
	ToolCallIDs  []string
	// ToolSearchMode and ToolCalling are expand-only builder fields. Empty
	// keeps the historical MODEL_TURN payload.
	ToolSearchMode string
	ToolCalling    string
}

// ModelTurnObserver is an optional ProtocolProjector extension. When the
// projector implements it, ProjectAgentEvent reports each assistant model turn
// after streaming (or non-stream) aggregation — including reasoning-only turns.
//
// Used by chatruntimebridge to persist agent_run_steps MODEL evidence for the
// platform-admin audit timeline.
type ModelTurnObserver interface {
	OnModelTurn(ctx context.Context, turn ModelTurn) error
}

// NopProjector is a no-op ProtocolProjector.
type NopProjector struct{}

func (NopProjector) OnTextDelta(context.Context, string) error    { return nil }
func (NopProjector) OnTextComplete(context.Context, string) error { return nil }

// RecordingProjector records text deltas for unit tests.
type RecordingProjector struct {
	Deltas     []string
	Completed  []string
	ModelTurns []ModelTurn
}

// OnTextDelta appends a content delta.
func (r *RecordingProjector) OnTextDelta(_ context.Context, delta string) error {
	if r == nil {
		return nil
	}
	r.Deltas = append(r.Deltas, delta)
	return nil
}

// OnTextComplete records the concatenated assistant text for one model turn.
func (r *RecordingProjector) OnTextComplete(_ context.Context, full string) error {
	if r == nil {
		return nil
	}
	r.Completed = append(r.Completed, full)
	return nil
}

// OnModelTurn records aggregated content + reasoning for one model turn.
func (r *RecordingProjector) OnModelTurn(_ context.Context, turn ModelTurn) error {
	if r == nil {
		return nil
	}
	r.ModelTurns = append(r.ModelTurns, turn)
	return nil
}

// JoinedDeltas returns all recorded deltas concatenated in order.
func (r *RecordingProjector) JoinedDeltas() string {
	if r == nil {
		return ""
	}
	return strings.Join(r.Deltas, "")
}

// ProjectAgentEvent consumes one ADK AgentEvent and drives projector hooks.
//
// For streaming assistant output (EnableStreaming + true Stream), each content
// chunk becomes OnTextDelta; reasoning_content is aggregated separately and
// never mixed into public text deltas. Tool-only / empty content chunks still
// contribute reasoning when present. Non-stream assistant messages emit a
// single delta + complete for completeness.
//
// When projector implements ModelTurnObserver, OnModelTurn is called once per
// assistant MessageOutput with aggregated Content + Reasoning.
//
// MessageStream is fully drained (and SetAutomaticClose) so copies do not leak.
func ProjectAgentEvent(ctx context.Context, event *adk.AgentEvent, projector ProtocolProjector) error {
	if event == nil || projector == nil {
		return nil
	}
	if event.Output == nil || event.Output.MessageOutput == nil {
		return nil
	}
	mv := event.Output.MessageOutput
	// Assistant model turns only — tool result events are projected in PR7.
	if mv.Role != "" && mv.Role != schema.Assistant {
		if mv.IsStreaming && mv.MessageStream != nil {
			mv.MessageStream.SetAutomaticClose()
			// Drain to avoid blocking the peer stream copy if any.
			_, _ = drainMessageStream(mv.MessageStream)
		}
		return nil
	}

	if mv.IsStreaming && mv.MessageStream != nil {
		mv.MessageStream.SetAutomaticClose()
		agg, err := projectStreamDeltas(ctx, mv.MessageStream, projector)
		if err != nil {
			return err
		}
		if agg.Content != "" {
			if err := projector.OnTextComplete(ctx, agg.Content); err != nil {
				return err
			}
		}
		return notifyModelTurn(ctx, projector, agg)
	}

	if mv.Message != nil {
		content := mv.Message.Content
		reasoning := strings.TrimSpace(mv.Message.ReasoningContent)
		turn := ModelTurn{Content: content, Reasoning: reasoning}
		if mv.Message.ResponseMeta != nil && mv.Message.ResponseMeta.Usage != nil {
			u := mv.Message.ResponseMeta.Usage
			turn.TokensKnown = true
			turn.PromptTokens = u.PromptTokens
			turn.CompletionTokens = u.CompletionTokens
			turn.TotalTokens = u.TotalTokens
			turn.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
		}
		if len(mv.Message.ToolCalls) > 0 {
			turn.HasToolCalls = true
			for _, tc := range mv.Message.ToolCalls {
				if tc.ID != "" {
					turn.ToolCallIDs = append(turn.ToolCallIDs, tc.ID)
				}
			}
		}
		if content != "" {
			if err := projector.OnTextDelta(ctx, content); err != nil {
				return err
			}
			if err := projector.OnTextComplete(ctx, content); err != nil {
				return err
			}
		}
		return notifyModelTurn(ctx, projector, turn)
	}
	return nil
}

func notifyModelTurn(ctx context.Context, projector ProtocolProjector, turn ModelTurn) error {
	observer, ok := projector.(ModelTurnObserver)
	if !ok {
		return nil
	}
	// Record tool-only / usage-only MODEL turns (empty content+reasoning still audit).
	// Only skip truly empty framing with no tool calls and no usage evidence.
	if strings.TrimSpace(turn.Content) == "" && strings.TrimSpace(turn.Reasoning) == "" &&
		!turn.HasToolCalls && !turn.TokensKnown {
		return nil
	}
	turn.Content = strings.TrimSpace(turn.Content)
	turn.Reasoning = strings.TrimSpace(turn.Reasoning)
	return observer.OnModelTurn(ctx, turn)
}

func projectStreamDeltas(
	ctx context.Context,
	stream *schema.StreamReader[*schema.Message],
	projector ProtocolProjector,
) (ModelTurn, error) {
	var contentBuf strings.Builder
	var reasoningBuf strings.Builder
	var turn ModelTurn
	// Always drain/close so the producer Pipe goroutine cannot block forever on Send
	// after we abort mid-stream (e.g. projector error).
	defer func() {
		if stream != nil {
			stream.Close()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			turn.Content = contentBuf.String()
			turn.Reasoning = reasoningBuf.String()
			return turn, err
		}
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			turn.Content = contentBuf.String()
			turn.Reasoning = reasoningBuf.String()
			return turn, err
		}
		if chunk == nil {
			continue
		}
		if piece := chunk.ReasoningContent; piece != "" {
			reasoningBuf.WriteString(piece)
		}
		// Eino stream usage is cumulative/max-style (take max, not sum of partials).
		if chunk.ResponseMeta != nil && chunk.ResponseMeta.Usage != nil {
			u := chunk.ResponseMeta.Usage
			turn.TokensKnown = true
			if u.PromptTokens > turn.PromptTokens {
				turn.PromptTokens = u.PromptTokens
			}
			if u.CompletionTokens > turn.CompletionTokens {
				turn.CompletionTokens = u.CompletionTokens
			}
			if u.TotalTokens > turn.TotalTokens {
				turn.TotalTokens = u.TotalTokens
			}
			if n := u.CompletionTokensDetails.ReasoningTokens; n > turn.ReasoningTokens {
				turn.ReasoningTokens = n
			}
		}
		for _, tc := range chunk.ToolCalls {
			if tc.ID == "" {
				continue
			}
			turn.HasToolCalls = true
			found := false
			for _, id := range turn.ToolCallIDs {
				if id == tc.ID {
					found = true
					break
				}
			}
			if !found {
				turn.ToolCallIDs = append(turn.ToolCallIDs, tc.ID)
			}
		}
		if chunk.Content == "" {
			continue
		}
		if err := projector.OnTextDelta(ctx, chunk.Content); err != nil {
			turn.Content = contentBuf.String()
			turn.Reasoning = reasoningBuf.String()
			return turn, err
		}
		contentBuf.WriteString(chunk.Content)
	}
	turn.Content = contentBuf.String()
	turn.Reasoning = reasoningBuf.String()
	return turn, nil
}

func drainMessageStream(stream *schema.StreamReader[*schema.Message]) (string, error) {
	var b strings.Builder
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return b.String(), nil
		}
		if err != nil {
			return b.String(), err
		}
		if chunk != nil {
			b.WriteString(chunk.Content)
		}
	}
}
