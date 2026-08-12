package chatruntimebridge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
)

// agenticTurnFunc performs one Agentic engine call: Run for an initial turn,
// Resume for a confirmation. Everything around the call — the sink, the stream
// projector, interrupt handling, the terminal text — is identical for both, and
// lives in runAgenticTurn so the two turns cannot drift apart in how they report
// a stream failure, a pause, or an empty answer.
type agenticTurnFunc func(
	ctx context.Context,
	agent adk.TypedAgent[*schema.AgenticMessage],
) (*einoruntime.AgenticRunResult, error)

// runAgenticTurn executes one Agentic turn and projects its outcome.
//
// The sink is opened before the engine call: the assistant message identity has
// to exist before any progressive output can be attributed to it, and a resume
// has no assembly phase during which it could be opened (AAP A.1).
//
// A pause is stamped with RuntimeGenerationAgentic. This is the only runtime that
// can restore the checkpoint it just wrote, and the stamp is what routes the
// confirmation back here instead of to the classic seam.
func (b *Bridge) runAgenticTurn(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	built adk.TypedAgent[*schema.AgenticMessage],
	call agenticTurnFunc,
) (text string, streamMessageID string, err error) {
	projector := &StreamDeltaRecorder{
		Now: b.now,
		ModelTurnHook: func(hookCtx context.Context, turn einoruntime.ModelTurn) error {
			return b.recordModelTurn(hookCtx, job, run, turn)
		},
	}
	if b.textSinkFactory != nil {
		messageID, idErr := newRuntimeID()
		if idErr != nil {
			return "", "", idErr
		}
		streamMessageID = messageID
		sink, sinkErr := b.textSinkFactory(ctx, TextSinkArgs{
			Job: job, Run: run, MessageID: messageID,
		})
		if sinkErr != nil {
			return "", streamMessageID, fmt.Errorf("open stream text sink: %w", sinkErr)
		}
		projector.Sink = sink
	}

	result, err := call(ctx, built)
	if err != nil {
		_ = projector.FailIncomplete(ctx, "MODEL_STREAM_INTERRUPTED", true)
		return "", streamMessageID, err
	}
	if result == nil {
		_ = projector.FailIncomplete(ctx, "MODEL_STREAM_INTERRUPTED", true)
		return "", streamMessageID, errors.New("einoruntime agentic engine returned nil result")
	}
	if result.Err != nil {
		_ = projector.FailIncomplete(ctx, "MODEL_STREAM_INTERRUPTED", true)
		return "", streamMessageID, result.Err
	}
	if result.Interrupted {
		_ = projector.FailIncomplete(ctx, "WAITING_CONFIRMATION", true)
		if err := b.pauseForInterrupt(ctx, job, run, &einoruntime.RunResult{
			CheckpointID:          result.CheckpointID,
			Interrupted:           true,
			InterruptContextIDs:   result.InterruptContextIDs,
			RootCauseInterruptIDs: result.RootCauseInterruptIDs,
			FinalAssistantText:    result.FinalAssistantText,
		}, RuntimeGenerationAgentic); err != nil {
			return "", streamMessageID, err
		}
		return "", streamMessageID, ErrWaitingConfirmation
	}
	text = strings.TrimSpace(result.FinalAssistantText)
	if text == "" {
		text = strings.TrimSpace(projector.Joined())
	}
	return text, streamMessageID, nil
}
