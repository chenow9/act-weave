package chatruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"actweave/backend/internal/modelconfig"
)

var (
	ErrTextStreamInvalid     = errors.New("model text stream is invalid")
	ErrTextStreamInterrupted = errors.New("model text stream ended before completion")
	ErrTextStreamUTF8        = errors.New("model text stream contains invalid UTF-8")
	ErrTextStreamTooLarge    = errors.New("model text stream exceeds the public text limit")
)

const (
	DefaultTextDeltaFlushInterval = 30 * time.Millisecond
	DefaultTextDeltaFlushBytes    = 4 << 10
	DefaultTextStreamMaxBytes     = 1 << 20
)

type ModelTextStreamEvent struct {
	Text         []byte
	Done         bool
	FinishReason string
	Err          error
}

type ModelTextStream interface {
	Events() <-chan ModelTextStreamEvent
	Close() error
}

// ModelTextStreamAdapter is the provider boundary. OpenAI, Anthropic, or a
// future internal provider can expose its wire stream through the same event
// channel without leaking provider frames into AAP mapping.
type ModelTextStreamAdapter interface {
	StreamText(
		context.Context,
		modelconfig.Config,
		CompletionRequest,
	) (ModelTextStream, error)
}

type ModelTextStreamAdapterFunc func(
	context.Context,
	modelconfig.Config,
	CompletionRequest,
) (ModelTextStream, error)

func (function ModelTextStreamAdapterFunc) StreamText(
	ctx context.Context,
	config modelconfig.Config,
	request CompletionRequest,
) (ModelTextStream, error) {
	return function(ctx, config, request)
}

type TextDeltaBatchPolicy struct {
	FlushInterval time.Duration
	FlushBytes    int
	MaxTextBytes  int
}

func DefaultTextDeltaBatchPolicy() TextDeltaBatchPolicy {
	return TextDeltaBatchPolicy{
		FlushInterval: DefaultTextDeltaFlushInterval,
		FlushBytes:    DefaultTextDeltaFlushBytes,
		MaxTextBytes:  DefaultTextStreamMaxBytes,
	}
}

type TextDeltaEmission struct {
	Index      int
	Text       string
	OccurredAt time.Time
}

type TextStreamCompletion struct {
	Text         string
	FinishReason string
	CompletedAt  time.Time
}

type TextStreamFailure struct {
	Code        string
	PartialText string
	Retryable   bool
	FailedAt    time.Time
}

type TextDeltaSink interface {
	EmitTextDelta(context.Context, TextDeltaEmission) error
	CompleteText(context.Context, TextStreamCompletion) error
	FailText(context.Context, TextStreamFailure) error
}

type TextStreamResult struct {
	Text         string
	FinishReason string
	DeltaCount   int
	Completed    bool
}

type TextDeltaBatcher struct {
	policy TextDeltaBatchPolicy
	now    func() time.Time
}

func NewTextDeltaBatcher(policy TextDeltaBatchPolicy) (*TextDeltaBatcher, error) {
	if policy.FlushInterval < 20*time.Millisecond || policy.FlushInterval > 50*time.Millisecond ||
		policy.FlushBytes < 1<<10 || policy.FlushBytes > 8<<10 ||
		policy.MaxTextBytes < policy.FlushBytes || policy.MaxTextBytes > DefaultTextStreamMaxBytes {
		return nil, ErrTextStreamInvalid
	}
	return &TextDeltaBatcher{
		policy: policy, now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (batcher *TextDeltaBatcher) Consume(
	ctx context.Context,
	stream ModelTextStream,
	sink TextDeltaSink,
) (TextStreamResult, error) {
	if batcher == nil || stream == nil || sink == nil || batcher.now == nil {
		return TextStreamResult{}, ErrTextStreamInvalid
	}
	events := stream.Events()
	if events == nil {
		return TextStreamResult{}, ErrTextStreamInvalid
	}
	defer stream.Close()
	ticker := time.NewTicker(batcher.policy.FlushInterval)
	defer ticker.Stop()

	var completeText, deltaBuffer, runeBuffer []byte
	deltaCount := 0
	flush := func(flushContext context.Context) error {
		if len(deltaBuffer) == 0 {
			return nil
		}
		text := string(deltaBuffer)
		if !utf8.ValidString(text) {
			return ErrTextStreamUTF8
		}
		if err := sink.EmitTextDelta(flushContext, TextDeltaEmission{
			Index: 0, Text: text, OccurredAt: batcher.now().UTC(),
		}); err != nil {
			return err
		}
		deltaBuffer = deltaBuffer[:0]
		deltaCount++
		return nil
	}
	fail := func(code string, retryable bool, cause error) (TextStreamResult, error) {
		failureContext := ctx
		if ctx.Err() != nil {
			failureContext = context.WithoutCancel(ctx)
		}
		failure := TextStreamFailure{
			Code: code, PartialText: string(completeText), Retryable: retryable,
			FailedAt: batcher.now().UTC(),
		}
		failureErr := sink.FailText(failureContext, failure)
		if cause == nil {
			cause = ErrTextStreamInterrupted
		}
		return TextStreamResult{Text: string(completeText), DeltaCount: deltaCount},
			errors.Join(cause, failureErr)
	}
	consume := func(raw []byte) error {
		runeBuffer = append(runeBuffer, raw...)
		for len(runeBuffer) > 0 {
			if !utf8.FullRune(runeBuffer) {
				return nil
			}
			runeValue, size := utf8.DecodeRune(runeBuffer)
			if runeValue == utf8.RuneError && size == 1 {
				return ErrTextStreamUTF8
			}
			if len(completeText)+size > batcher.policy.MaxTextBytes {
				return ErrTextStreamTooLarge
			}
			completeText = append(completeText, runeBuffer[:size]...)
			deltaBuffer = append(deltaBuffer, runeBuffer[:size]...)
			runeBuffer = runeBuffer[size:]
			if len(deltaBuffer) >= batcher.policy.FlushBytes {
				if err := flush(ctx); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return fail("MODEL_STREAM_CANCELLED", true, ctx.Err())
		case <-ticker.C:
			if err := flush(ctx); err != nil {
				return fail("MODEL_STREAM_SINK_FAILED", true, err)
			}
		case event, open := <-events:
			if !open {
				if len(runeBuffer) != 0 {
					return fail("MODEL_STREAM_INVALID_UTF8", false, ErrTextStreamUTF8)
				}
				return fail("MODEL_STREAM_INTERRUPTED", true, ErrTextStreamInterrupted)
			}
			if event.Err != nil {
				return fail("MODEL_STREAM_FAILED", true, event.Err)
			}
			if len(event.Text) > 0 {
				if err := consume(event.Text); err != nil {
					code := "MODEL_STREAM_FAILED"
					retryable := true
					if errors.Is(err, ErrTextStreamUTF8) {
						code, retryable = "MODEL_STREAM_INVALID_UTF8", false
					} else if errors.Is(err, ErrTextStreamTooLarge) {
						code, retryable = "MODEL_STREAM_TOO_LARGE", false
					}
					return fail(code, retryable, err)
				}
			}
			if !event.Done {
				continue
			}
			if len(runeBuffer) != 0 {
				return fail("MODEL_STREAM_INVALID_UTF8", false, ErrTextStreamUTF8)
			}
			if err := flush(ctx); err != nil {
				return fail("MODEL_STREAM_SINK_FAILED", true, err)
			}
			completion := TextStreamCompletion{
				Text: string(completeText), FinishReason: strings.TrimSpace(event.FinishReason),
				CompletedAt: batcher.now().UTC(),
			}
			if completion.Text == "" {
				return fail("MODEL_STREAM_EMPTY", true, ErrTextStreamInterrupted)
			}
			if err := sink.CompleteText(ctx, completion); err != nil {
				return fail("MODEL_STREAM_SINK_FAILED", true, err)
			}
			return TextStreamResult{
				Text: completion.Text, FinishReason: completion.FinishReason,
				DeltaCount: deltaCount, Completed: true,
			}, nil
		}
	}
}

type StreamingTextRunner struct {
	adapter ModelTextStreamAdapter
	batcher *TextDeltaBatcher
	now     func() time.Time
}

func NewStreamingTextRunner(
	adapter ModelTextStreamAdapter,
	policy TextDeltaBatchPolicy,
) (*StreamingTextRunner, error) {
	if adapter == nil {
		return nil, ErrTextStreamInvalid
	}
	batcher, err := NewTextDeltaBatcher(policy)
	if err != nil {
		return nil, err
	}
	return &StreamingTextRunner{
		adapter: adapter, batcher: batcher, now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (runner *StreamingTextRunner) Run(
	ctx context.Context,
	config modelconfig.Config,
	request CompletionRequest,
	sink TextDeltaSink,
) (TextStreamResult, error) {
	if runner == nil || runner.adapter == nil || runner.batcher == nil || sink == nil {
		return TextStreamResult{}, ErrTextStreamInvalid
	}
	stream, err := runner.adapter.StreamText(ctx, config, request)
	if err != nil {
		failureContext := ctx
		if ctx.Err() != nil {
			failureContext = context.WithoutCancel(ctx)
		}
		failure := TextStreamFailure{
			Code: "MODEL_STREAM_OPEN_FAILED", Retryable: true, FailedAt: runner.now().UTC(),
		}
		return TextStreamResult{}, errors.Join(err, sink.FailText(failureContext, failure))
	}
	return runner.batcher.Consume(ctx, stream, sink)
}

func (policy TextDeltaBatchPolicy) String() string {
	return fmt.Sprintf("interval=%s bytes=%d max=%d",
		policy.FlushInterval, policy.FlushBytes, policy.MaxTextBytes)
}
