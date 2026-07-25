package chatruntimebridge

import (
	"context"
	"strings"
	"sync"
	"time"

	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
)

// TextSinkArgs carries identity for one assistant text stream (D14).
type TextSinkArgs struct {
	Job       agentrun.Job
	Run       execution.AgentRun
	MessageID string
}

// TextSinkFactory builds a chatruntime.TextDeltaSink for true Stream → item.delta
// projection (ProtocolMessageTextSink-compatible). Production wires a factory that
// returns ProtocolMessageTextSink; tests inject recording sinks.
type TextSinkFactory func(ctx context.Context, args TextSinkArgs) (chatruntime.TextDeltaSink, error)

// StreamDeltaRecorder is the bridge-side ProtocolProjector that records true
// Stream content chunks (item.delta equivalent) and optionally forwards to a
// chatruntime.TextDeltaSink (ProtocolMessageTextSink-compatible batching).
//
// D14: deltas come from real model Stream chunks via einoruntime.ProjectAgentEvent —
// never fabricated from a single Generate.
//
// When ModelTurnHook is set (and/or the recorder is used as ModelTurnObserver),
// each assistant model turn with content or reasoning is reported for audit.
type StreamDeltaRecorder struct {
	mu     sync.Mutex
	deltas []string
	// Sink is optional; when set, each non-empty delta is flushed as a text emission.
	// Production drive always sets Sink when TextSinkFactory is configured.
	Sink chatruntime.TextDeltaSink
	// ModelTurnHook is optional; called once per assistant model turn with
	// aggregated content + reasoning (eino → agent audit MODEL step path).
	ModelTurnHook func(ctx context.Context, turn einoruntime.ModelTurn) error
	// Now overrides time for tests.
	Now func() time.Time
	// index is the next delta index for TextDeltaEmission.
	index int
	// completed is true after successful OnTextComplete (skip FailText).
	completed bool
	// failed is true after FailIncomplete.
	failed bool
	// modelTurns accumulates turns for tests / post-drive inspection.
	modelTurns []einoruntime.ModelTurn
}

// Ensure compile-time interface satisfaction.
var (
	_ einoruntime.ProtocolProjector = (*StreamDeltaRecorder)(nil)
	_ einoruntime.ModelTurnObserver = (*StreamDeltaRecorder)(nil)
)

// OnTextDelta records and optionally sinks one content chunk.
//
// TextDelta.Index is the Message content-part index (legacy TextDeltaBatcher always
// uses 0 for a single assistant text part). It is NOT a monotonic stream counter —
// using 1,2,3… makes applyCurrentItemDelta reject with ErrRunItemInvalid after the
// first delta (Index >= len(Content)).
func (r *StreamDeltaRecorder) OnTextDelta(ctx context.Context, delta string) error {
	if r == nil || delta == "" {
		return nil
	}
	r.mu.Lock()
	r.deltas = append(r.deltas, delta)
	r.index++ // stream emission count only (observability / tests)
	sink := r.Sink
	nowFn := r.Now
	r.mu.Unlock()

	if sink == nil {
		return nil
	}
	occurred := time.Now().UTC()
	if nowFn != nil {
		occurred = nowFn().UTC()
	}
	return sink.EmitTextDelta(ctx, chatruntime.TextDeltaEmission{
		Index: 0, Text: delta, OccurredAt: occurred,
	})
}

// OnTextComplete optionally finalizes the text stream sink.
func (r *StreamDeltaRecorder) OnTextComplete(ctx context.Context, full string) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	sink := r.Sink
	nowFn := r.Now
	r.completed = true
	r.mu.Unlock()
	if sink == nil {
		return nil
	}
	completedAt := time.Now().UTC()
	if nowFn != nil {
		completedAt = nowFn().UTC()
	}
	return sink.CompleteText(ctx, chatruntime.TextStreamCompletion{
		Text: full, CompletedAt: completedAt,
	})
}

// OnModelTurn records one assistant model turn and optionally forwards to ModelTurnHook.
func (r *StreamDeltaRecorder) OnModelTurn(ctx context.Context, turn einoruntime.ModelTurn) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	r.modelTurns = append(r.modelTurns, turn)
	hook := r.ModelTurnHook
	r.mu.Unlock()
	if hook == nil {
		return nil
	}
	return hook(ctx, turn)
}

// ModelTurns returns a copy of recorded model turns in order.
func (r *StreamDeltaRecorder) ModelTurns() []einoruntime.ModelTurn {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]einoruntime.ModelTurn, len(r.modelTurns))
	copy(out, r.modelTurns)
	return out
}

// FailIncomplete flushes FailText for an open stream that did not Complete
// (interrupt / hard error). Matches A.5 FailText semantics.
func (r *StreamDeltaRecorder) FailIncomplete(ctx context.Context, code string, retryable bool) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.completed || r.failed || r.Sink == nil || len(r.deltas) == 0 {
		r.mu.Unlock()
		return nil
	}
	sink := r.Sink
	nowFn := r.Now
	partial := strings.Join(r.deltas, "")
	r.failed = true
	r.mu.Unlock()

	failedAt := time.Now().UTC()
	if nowFn != nil {
		failedAt = nowFn().UTC()
	}
	if strings.TrimSpace(code) == "" {
		code = "MODEL_STREAM_INTERRUPTED"
	}
	return sink.FailText(ctx, chatruntime.TextStreamFailure{
		Code: code, PartialText: partial, Retryable: retryable, FailedAt: failedAt,
	})
}

// Deltas returns a copy of recorded content chunks in order.
func (r *StreamDeltaRecorder) Deltas() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.deltas))
	copy(out, r.deltas)
	return out
}

// Joined returns all recorded deltas concatenated.
func (r *StreamDeltaRecorder) Joined() string {
	return strings.Join(r.Deltas(), "")
}

// DeltaCount returns the number of recorded non-empty deltas.
func (r *StreamDeltaRecorder) DeltaCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.deltas)
}

// NopTextStreamFinalizer is a TextStreamFinalizer that accepts Complete/Fail
// without additional permanent writes. Permanent assistant content remains
// owned by Bridge.completeRun / failRun; this finalizer only unblocks the
// ProtocolMessageTextSink contract after item.delta projection.
type NopTextStreamFinalizer struct{}

// CompleteStreamedText implements chatruntime.TextStreamFinalizer.
func (NopTextStreamFinalizer) CompleteStreamedText(context.Context, chatruntime.TextStreamCompletion) error {
	return nil
}

// FailStreamedText implements chatruntime.TextStreamFinalizer.
func (NopTextStreamFinalizer) FailStreamedText(context.Context, chatruntime.TextStreamFailure) error {
	return nil
}

// NopTextDeltaSink is a TextDeltaSink that accepts emissions without writing
// protocol events. Used when item.started cannot be projected so Stream
// deltas do not fail the model drive; permanent content still lands via
// completeRun.
type NopTextDeltaSink struct{}

// EmitTextDelta implements chatruntime.TextDeltaSink.
func (NopTextDeltaSink) EmitTextDelta(context.Context, chatruntime.TextDeltaEmission) error {
	return nil
}

// CompleteText implements chatruntime.TextDeltaSink.
func (NopTextDeltaSink) CompleteText(context.Context, chatruntime.TextStreamCompletion) error {
	return nil
}

// FailText implements chatruntime.TextDeltaSink.
func (NopTextDeltaSink) FailText(context.Context, chatruntime.TextStreamFailure) error {
	return nil
}

// RecordingTextDeltaSink captures TextDeltaSink calls for unit/integration tests.
type RecordingTextDeltaSink struct {
	mu         sync.Mutex
	Emissions  []chatruntime.TextDeltaEmission
	Completion *chatruntime.TextStreamCompletion
	Failure    *chatruntime.TextStreamFailure
}

// EmitTextDelta records one delta emission.
func (s *RecordingTextDeltaSink) EmitTextDelta(_ context.Context, emission chatruntime.TextDeltaEmission) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Emissions = append(s.Emissions, emission)
	return nil
}

// CompleteText records successful stream completion.
func (s *RecordingTextDeltaSink) CompleteText(_ context.Context, completion chatruntime.TextStreamCompletion) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := completion
	s.Completion = &c
	return nil
}

// FailText records stream failure (interrupt / hard error).
func (s *RecordingTextDeltaSink) FailText(_ context.Context, failure chatruntime.TextStreamFailure) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f := failure
	s.Failure = &f
	return nil
}

// EmissionTexts returns recorded delta texts in order.
func (s *RecordingTextDeltaSink) EmissionTexts() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.Emissions))
	for i, e := range s.Emissions {
		out[i] = e.Text
	}
	return out
}
