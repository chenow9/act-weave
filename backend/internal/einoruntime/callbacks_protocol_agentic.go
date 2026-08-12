package einoruntime

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
)

// Projection of Agentic (typed) turns onto ProtocolProjector.
//
// The classic engine projects adk.AgentEvent via ProjectAgentEvent. The Agentic
// runtime carries *schema.AgenticMessage, whose text lives in content blocks
// rather than a Content field, so it cannot reuse that path — but it owes the
// client the same protocol: progressive item.delta under one item identity,
// item text completion, and one MODEL turn of audit evidence per assistant turn.
//
// Reasoning is never projected as public text. agenticmsg owns which blocks are
// public (assistantPublicText); this layer only forwards what it is handed.

// projectAgenticChunkDelta emits one streaming chunk's public text as a delta.
//
// Chunks with no public text are ordinary — a turn also streams reasoning and
// tool-call chunks — and are skipped rather than treated as failures. Chunks are
// already validated by the caller, so an extraction error here means the chunk
// carries a block outside the assistant matrix and fails closed.
func projectAgenticChunkDelta(
	ctx context.Context,
	chunk *schema.AgenticMessage,
	projector ProtocolProjector,
) error {
	if projector == nil || chunk == nil {
		return nil
	}
	if chunk.Role != schema.AgenticRoleTypeAssistant {
		return nil
	}
	text, err := agenticmsg.ExtractAssistantChunkText(chunk)
	if err != nil {
		return err
	}
	if text == "" {
		return nil
	}
	return projector.OnTextDelta(ctx, text)
}

// projectAgenticModelTurn reports a completed streamed assistant turn: the item
// text completion plus the MODEL evidence for the audit timeline.
//
// It takes the concatenated message rather than the chunks because usage and
// assembled tool calls only exist after concatenation.
func projectAgenticModelTurn(
	ctx context.Context,
	message *schema.AgenticMessage,
	projector ProtocolProjector,
) error {
	if projector == nil || message == nil {
		return nil
	}
	if message.Role != schema.AgenticRoleTypeAssistant {
		return nil
	}
	turn, text, err := agenticModelTurn(message)
	if err != nil {
		return err
	}
	if text != "" {
		if err := projector.OnTextComplete(ctx, text); err != nil {
			return err
		}
	}
	return notifyModelTurn(ctx, projector, turn)
}

// projectAgenticCompleteMessage projects a non-streaming assistant turn as a
// single delta plus completion, so a provider that does not stream still
// produces the same item timeline as one that does.
func projectAgenticCompleteMessage(
	ctx context.Context,
	message *schema.AgenticMessage,
	projector ProtocolProjector,
) error {
	if projector == nil || message == nil {
		return nil
	}
	if message.Role != schema.AgenticRoleTypeAssistant {
		return nil
	}
	turn, text, err := agenticModelTurn(message)
	if err != nil {
		return err
	}
	if text != "" {
		if err := projector.OnTextDelta(ctx, text); err != nil {
			return err
		}
		if err := projector.OnTextComplete(ctx, text); err != nil {
			return err
		}
	}
	return notifyModelTurn(ctx, projector, turn)
}

// agenticModelTurn derives the audit evidence for one complete assistant
// message. A turn with no public text is still evidence: tool-only and
// reasoning-only turns are exactly the ones an operator cannot reconstruct from
// the transcript.
func agenticModelTurn(message *schema.AgenticMessage) (ModelTurn, string, error) {
	text, err := agenticmsg.ExtractAssistantText(message)
	if err != nil && !errors.Is(err, agenticmsg.ErrNoAssistantText) {
		return ModelTurn{}, "", err
	}
	reasoning, err := agenticmsg.ExtractReasoningText(message)
	if err != nil {
		return ModelTurn{}, "", err
	}
	calls, err := agenticmsg.FunctionCalls(message)
	if err != nil {
		return ModelTurn{}, "", err
	}
	usage, err := agenticmsg.Usage(message)
	if err != nil {
		return ModelTurn{}, "", err
	}

	turn := ModelTurn{Content: text, Reasoning: strings.TrimSpace(reasoning)}
	for _, call := range calls {
		turn.HasToolCalls = true
		if id := strings.TrimSpace(call.CallID); id != "" {
			turn.ToolCallIDs = append(turn.ToolCallIDs, id)
		}
	}
	if usage != (agenticmsg.TokenUsage{}) {
		turn.TokensKnown = true
		turn.PromptTokens = usage.PromptTokens
		turn.CompletionTokens = usage.CompletionTokens
		turn.TotalTokens = usage.TotalTokens
		turn.ReasoningTokens = usage.ReasoningTokens
		turn.CachedPromptTokens = usage.CachedTokens
	}
	return turn, text, nil
}
