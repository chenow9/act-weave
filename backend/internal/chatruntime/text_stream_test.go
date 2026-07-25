package chatruntime_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/modelconfig"
)

func TestTextDeltaBatching(t *testing.T) {
	t.Run("size batching preserves split UTF-8", func(t *testing.T) {
		content := strings.Repeat("你🙂a", 700)
		events := make(chan chatruntime.ModelTextStreamEvent, len([]byte(content))+1)
		for _, value := range []byte(content) {
			events <- chatruntime.ModelTextStreamEvent{Text: []byte{value}}
		}
		events <- chatruntime.ModelTextStreamEvent{Done: true, FinishReason: "stop"}
		close(events)
		stream := &testModelTextStream{events: events}
		adapterCalls := 0
		runner, err := chatruntime.NewStreamingTextRunner(
			chatruntime.ModelTextStreamAdapterFunc(func(
				context.Context,
				modelconfig.Config,
				chatruntime.CompletionRequest,
			) (chatruntime.ModelTextStream, error) {
				adapterCalls++
				return stream, nil
			}),
			chatruntime.TextDeltaBatchPolicy{
				FlushInterval: 50 * time.Millisecond, FlushBytes: 1024,
				MaxTextBytes: chatruntime.DefaultTextStreamMaxBytes,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		sink := &recordingTextDeltaSink{}
		result, err := runner.Run(context.Background(), modelconfig.Config{},
			chatruntime.CompletionRequest{}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Completed || result.Text != content || result.FinishReason != "stop" ||
			result.DeltaCount != len(sink.emissions) || adapterCalls != 1 || stream.closeCalls != 1 {
			t.Fatalf("stream result=%+v sink=%+v adapter=%d close=%d",
				result, sink, adapterCalls, stream.closeCalls)
		}
		if len(sink.emissions) >= len([]byte(content))/4 || len(sink.emissions) < 2 {
			t.Fatalf("emitted token-sized deltas: chunks=%d bytes=%d", len(sink.emissions), len(content))
		}
		var merged strings.Builder
		for _, emission := range sink.emissions {
			if !utf8.ValidString(emission.Text) || len([]byte(emission.Text)) > 1027 || emission.Index != 0 {
				t.Fatalf("invalid UTF-8/size delta bytes=%d value=%q", len(emission.Text), emission.Text)
			}
			merged.WriteString(emission.Text)
		}
		if merged.String() != content || sink.completion == nil || sink.completion.Text != content ||
			sink.failure != nil {
			t.Fatalf("delta/final snapshot mismatch merged=%d final=%+v failure=%+v",
				merged.Len(), sink.completion, sink.failure)
		}
	})

	t.Run("time batching flushes without waiting for another token", func(t *testing.T) {
		events := make(chan chatruntime.ModelTextStreamEvent)
		stream := &testModelTextStream{events: events}
		runner, err := chatruntime.NewStreamingTextRunner(
			chatruntime.ModelTextStreamAdapterFunc(func(
				context.Context,
				modelconfig.Config,
				chatruntime.CompletionRequest,
			) (chatruntime.ModelTextStream, error) {
				return stream, nil
			}),
			chatruntime.TextDeltaBatchPolicy{
				FlushInterval: 20 * time.Millisecond, FlushBytes: 8 << 10,
				MaxTextBytes: chatruntime.DefaultTextStreamMaxBytes,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			events <- chatruntime.ModelTextStreamEvent{Text: []byte("hello")}
			time.Sleep(35 * time.Millisecond)
			events <- chatruntime.ModelTextStreamEvent{Text: []byte(" world")}
			events <- chatruntime.ModelTextStreamEvent{Done: true, FinishReason: "stop"}
			close(events)
		}()
		sink := &recordingTextDeltaSink{}
		result, err := runner.Run(context.Background(), modelconfig.Config{},
			chatruntime.CompletionRequest{}, sink)
		if err != nil || result.Text != "hello world" || len(sink.emissions) != 2 ||
			sink.emissions[0].Text != "hello" || sink.emissions[1].Text != " world" {
			t.Fatalf("time batching result=%+v emissions=%+v err=%v", result, sink.emissions, err)
		}
	})
}

func TestDeltaSnapshotEquivalence(t *testing.T) {
	t.Run("protocol sink maps batches without promoting notify failure", func(t *testing.T) {
		events := make(chan chatruntime.ModelTextStreamEvent, 3)
		events <- chatruntime.ModelTextStreamEvent{Text: []byte("hello")}
		events <- chatruntime.ModelTextStreamEvent{Text: []byte(" world")}
		events <- chatruntime.ModelTextStreamEvent{Done: true, FinishReason: "stop"}
		close(events)
		runner := newTestStreamingTextRunner(t, &testModelTextStream{events: events}, 1024)
		notifyFailure := errors.New("notify failed after commit")
		projector := &recordingMessageDeltaProjector{notifyError: notifyFailure}
		finalizer := &recordingTextStreamFinalizer{}
		observer := &recordingProtocolNotifyObserver{}
		sink, err := chatruntime.NewProtocolMessageTextSink(
			projector, finalizer, observer, chat.ProtocolMessageContext{}, "message-id",
		)
		if err != nil {
			t.Fatal(err)
		}
		result, err := runner.Run(context.Background(), modelconfig.Config{},
			chatruntime.CompletionRequest{}, sink)
		if err != nil || !result.Completed || result.Text != "hello world" ||
			len(projector.inputs) != 1 || projector.inputs[0].Text != "hello world" ||
			finalizer.completion == nil || finalizer.completion.Text != result.Text ||
			finalizer.failure != nil || len(observer.errors) != 1 ||
			!errors.Is(observer.errors[0], notifyFailure) {
			t.Fatalf("protocol sink result=%+v inputs=%+v completion=%+v failure=%+v observed=%v err=%v",
				result, projector.inputs, finalizer.completion, finalizer.failure, observer.errors, err)
		}
	})

	t.Run("provider interruption finalizes explicit failure", func(t *testing.T) {
		events := make(chan chatruntime.ModelTextStreamEvent, 2)
		events <- chatruntime.ModelTextStreamEvent{Text: []byte("partial ")}
		events <- chatruntime.ModelTextStreamEvent{Text: []byte("answer")}
		close(events)
		runner := newTestStreamingTextRunner(t, &testModelTextStream{events: events}, 1024)
		sink := &recordingTextDeltaSink{}
		result, err := runner.Run(context.Background(), modelconfig.Config{},
			chatruntime.CompletionRequest{}, sink)
		if !errors.Is(err, chatruntime.ErrTextStreamInterrupted) || result.Completed ||
			result.Text != "partial answer" || sink.completion != nil || sink.failure == nil ||
			sink.failure.Code != "MODEL_STREAM_INTERRUPTED" ||
			sink.failure.PartialText != "partial answer" || !sink.failure.Retryable {
			t.Fatalf("interrupted result=%+v failure=%+v err=%v", result, sink.failure, err)
		}
	})

	t.Run("invalid or incomplete UTF-8 never reaches a delta", func(t *testing.T) {
		for name, chunks := range map[string][][]byte{
			"invalid":    {{0xff}},
			"incomplete": {{0xe4}, {0xbd}},
		} {
			t.Run(name, func(t *testing.T) {
				events := make(chan chatruntime.ModelTextStreamEvent, len(chunks))
				for _, chunk := range chunks {
					events <- chatruntime.ModelTextStreamEvent{Text: chunk}
				}
				close(events)
				runner := newTestStreamingTextRunner(t, &testModelTextStream{events: events}, 1024)
				sink := &recordingTextDeltaSink{}
				_, err := runner.Run(context.Background(), modelconfig.Config{},
					chatruntime.CompletionRequest{}, sink)
				if !errors.Is(err, chatruntime.ErrTextStreamUTF8) || len(sink.emissions) != 0 ||
					sink.failure == nil || sink.failure.Code != "MODEL_STREAM_INVALID_UTF8" ||
					sink.failure.Retryable {
					t.Fatalf("UTF-8 failure emissions=%+v failure=%+v err=%v",
						sink.emissions, sink.failure, err)
				}
			})
		}
	})

	t.Run("public snapshot size is bounded", func(t *testing.T) {
		events := make(chan chatruntime.ModelTextStreamEvent, 2)
		events <- chatruntime.ModelTextStreamEvent{Text: []byte(strings.Repeat("a", 1025))}
		events <- chatruntime.ModelTextStreamEvent{Done: true}
		close(events)
		runner, err := chatruntime.NewStreamingTextRunner(
			chatruntime.ModelTextStreamAdapterFunc(func(
				context.Context,
				modelconfig.Config,
				chatruntime.CompletionRequest,
			) (chatruntime.ModelTextStream, error) {
				return &testModelTextStream{events: events}, nil
			}),
			chatruntime.TextDeltaBatchPolicy{
				FlushInterval: 50 * time.Millisecond, FlushBytes: 1024, MaxTextBytes: 1024,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		sink := &recordingTextDeltaSink{}
		_, err = runner.Run(context.Background(), modelconfig.Config{},
			chatruntime.CompletionRequest{}, sink)
		if !errors.Is(err, chatruntime.ErrTextStreamTooLarge) || sink.failure == nil ||
			sink.failure.Code != "MODEL_STREAM_TOO_LARGE" || sink.failure.Retryable {
			t.Fatalf("oversize failure=%+v err=%v", sink.failure, err)
		}
	})

	t.Run("adapter open failure is an explicit item failure", func(t *testing.T) {
		openFailure := errors.New("provider unavailable")
		runner, err := chatruntime.NewStreamingTextRunner(
			chatruntime.ModelTextStreamAdapterFunc(func(
				context.Context,
				modelconfig.Config,
				chatruntime.CompletionRequest,
			) (chatruntime.ModelTextStream, error) {
				return nil, openFailure
			}),
			chatruntime.DefaultTextDeltaBatchPolicy(),
		)
		if err != nil {
			t.Fatal(err)
		}
		sink := &recordingTextDeltaSink{}
		_, err = runner.Run(context.Background(), modelconfig.Config{},
			chatruntime.CompletionRequest{}, sink)
		if !errors.Is(err, openFailure) || sink.failure == nil ||
			sink.failure.Code != "MODEL_STREAM_OPEN_FAILED" || !sink.failure.Retryable {
			t.Fatalf("open failure=%+v err=%v", sink.failure, err)
		}
	})

	for name, policy := range map[string]chatruntime.TextDeltaBatchPolicy{
		"interval": {FlushInterval: 19 * time.Millisecond, FlushBytes: 1024, MaxTextBytes: 1024},
		"bytes":    {FlushInterval: 20 * time.Millisecond, FlushBytes: 512, MaxTextBytes: 1024},
	} {
		t.Run("invalid policy "+name, func(t *testing.T) {
			if _, err := chatruntime.NewTextDeltaBatcher(policy); !errors.Is(err, chatruntime.ErrTextStreamInvalid) {
				t.Fatalf("invalid policy error=%v", err)
			}
		})
	}
}

type testModelTextStream struct {
	events     <-chan chatruntime.ModelTextStreamEvent
	closeCalls int
}

func (stream *testModelTextStream) Events() <-chan chatruntime.ModelTextStreamEvent {
	return stream.events
}

func (stream *testModelTextStream) Close() error {
	stream.closeCalls++
	return nil
}

type recordingTextDeltaSink struct {
	emissions  []chatruntime.TextDeltaEmission
	completion *chatruntime.TextStreamCompletion
	failure    *chatruntime.TextStreamFailure
}

func (sink *recordingTextDeltaSink) EmitTextDelta(
	_ context.Context,
	emission chatruntime.TextDeltaEmission,
) error {
	sink.emissions = append(sink.emissions, emission)
	return nil
}

func (sink *recordingTextDeltaSink) CompleteText(
	_ context.Context,
	completion chatruntime.TextStreamCompletion,
) error {
	sink.completion = &completion
	return nil
}

func (sink *recordingTextDeltaSink) FailText(
	_ context.Context,
	failure chatruntime.TextStreamFailure,
) error {
	sink.failure = &failure
	return nil
}

func newTestStreamingTextRunner(
	t *testing.T,
	stream chatruntime.ModelTextStream,
	flushBytes int,
) *chatruntime.StreamingTextRunner {
	t.Helper()
	runner, err := chatruntime.NewStreamingTextRunner(
		chatruntime.ModelTextStreamAdapterFunc(func(
			context.Context,
			modelconfig.Config,
			chatruntime.CompletionRequest,
		) (chatruntime.ModelTextStream, error) {
			return stream, nil
		}),
		chatruntime.TextDeltaBatchPolicy{
			FlushInterval: 50 * time.Millisecond, FlushBytes: flushBytes,
			MaxTextBytes: chatruntime.DefaultTextStreamMaxBytes,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

type recordingMessageDeltaProjector struct {
	inputs      []chat.ProjectMessageDeltaInput
	notifyError error
}

func (projector *recordingMessageDeltaProjector) ProjectDelta(
	_ context.Context,
	input chat.ProjectMessageDeltaInput,
) (chat.ProtocolMessageProjectionResult, error) {
	projector.inputs = append(projector.inputs, input)
	return chat.ProtocolMessageProjectionResult{NotifyError: projector.notifyError}, nil
}

type recordingTextStreamFinalizer struct {
	completion *chatruntime.TextStreamCompletion
	failure    *chatruntime.TextStreamFailure
}

func (finalizer *recordingTextStreamFinalizer) CompleteStreamedText(
	_ context.Context,
	completion chatruntime.TextStreamCompletion,
) error {
	finalizer.completion = &completion
	return nil
}

func (finalizer *recordingTextStreamFinalizer) FailStreamedText(
	_ context.Context,
	failure chatruntime.TextStreamFailure,
) error {
	finalizer.failure = &failure
	return nil
}

type recordingProtocolNotifyObserver struct{ errors []error }

func (observer *recordingProtocolNotifyObserver) ObserveProtocolNotifyError(err error) {
	observer.errors = append(observer.errors, err)
}
