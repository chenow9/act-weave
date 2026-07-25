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
// One ModelTurn maps to one ADK assistant MessageOutput.
type ModelTurn struct {
	Content         string
	Reasoning       string
	ReasoningTokens int
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
		content, reasoning, reasoningTokens, err := projectStreamDeltas(ctx, mv.MessageStream, projector)
		if err != nil {
			return err
		}
		if content != "" {
			if err := projector.OnTextComplete(ctx, content); err != nil {
				return err
			}
		}
		return notifyModelTurn(ctx, projector, ModelTurn{
			Content: content, Reasoning: reasoning, ReasoningTokens: reasoningTokens,
		})
	}

	if mv.Message != nil {
		content := mv.Message.Content
		reasoning := strings.TrimSpace(mv.Message.ReasoningContent)
		reasoningTokens := 0
		if mv.Message.ResponseMeta != nil && mv.Message.ResponseMeta.Usage != nil {
			reasoningTokens = mv.Message.ResponseMeta.Usage.CompletionTokensDetails.ReasoningTokens
		}
		if content != "" {
			if err := projector.OnTextDelta(ctx, content); err != nil {
				return err
			}
			if err := projector.OnTextComplete(ctx, content); err != nil {
				return err
			}
		}
		return notifyModelTurn(ctx, projector, ModelTurn{
			Content: content, Reasoning: reasoning, ReasoningTokens: reasoningTokens,
		})
	}
	return nil
}

func notifyModelTurn(ctx context.Context, projector ProtocolProjector, turn ModelTurn) error {
	observer, ok := projector.(ModelTurnObserver)
	if !ok {
		return nil
	}
	// Skip completely empty turns (no content, no reasoning) — e.g. pure
	// tool-call framing with no audit surface. Callers that need tool-only
	// MODEL steps can extend later.
	if strings.TrimSpace(turn.Content) == "" && strings.TrimSpace(turn.Reasoning) == "" {
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
) (content string, reasoning string, reasoningTokens int, err error) {
	var contentBuf strings.Builder
	var reasoningBuf strings.Builder
	// Always drain/close so the producer Pipe goroutine cannot block forever on Send
	// after we abort mid-stream (e.g. projector error).
	defer func() {
		if stream != nil {
			stream.Close()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return contentBuf.String(), reasoningBuf.String(), reasoningTokens, err
		}
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return contentBuf.String(), reasoningBuf.String(), reasoningTokens, err
		}
		if chunk == nil {
			continue
		}
		if piece := chunk.ReasoningContent; piece != "" {
			reasoningBuf.WriteString(piece)
		}
		if chunk.ResponseMeta != nil && chunk.ResponseMeta.Usage != nil {
			if n := chunk.ResponseMeta.Usage.CompletionTokensDetails.ReasoningTokens; n > reasoningTokens {
				reasoningTokens = n
			}
		}
		if chunk.Content == "" {
			continue
		}
		if err := projector.OnTextDelta(ctx, chunk.Content); err != nil {
			return contentBuf.String(), reasoningBuf.String(), reasoningTokens, err
		}
		contentBuf.WriteString(chunk.Content)
	}
	return contentBuf.String(), reasoningBuf.String(), reasoningTokens, nil
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
