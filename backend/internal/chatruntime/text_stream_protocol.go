package chatruntime

import (
	"context"
	"strings"

	"actweave/backend/internal/chat"
)

type MessageDeltaProjector interface {
	ProjectDelta(
		context.Context,
		chat.ProjectMessageDeltaInput,
	) (chat.ProtocolMessageProjectionResult, error)
}

// TextStreamFinalizer owns the domain-specific final write: persist the final
// Assistant Message, then complete its Run Item, or persist an explicit failed
// Item for an interrupted provider stream.
type TextStreamFinalizer interface {
	CompleteStreamedText(context.Context, TextStreamCompletion) error
	FailStreamedText(context.Context, TextStreamFailure) error
}

type ProtocolNotifyErrorObserver interface {
	ObserveProtocolNotifyError(error)
}

// ProtocolMessageTextSink connects provider-neutral batching to the M3-T2
// Message Projector. A post-commit notify failure is observed separately and
// does not turn an already-persisted Delta into a model failure.
type ProtocolMessageTextSink struct {
	projector MessageDeltaProjector
	finalizer TextStreamFinalizer
	observer  ProtocolNotifyErrorObserver
	context   chat.ProtocolMessageContext
	messageID string
}

func NewProtocolMessageTextSink(
	projector MessageDeltaProjector,
	finalizer TextStreamFinalizer,
	observer ProtocolNotifyErrorObserver,
	messageContext chat.ProtocolMessageContext,
	messageID string,
) (*ProtocolMessageTextSink, error) {
	if projector == nil || finalizer == nil || strings.TrimSpace(messageID) == "" {
		return nil, ErrTextStreamInvalid
	}
	return &ProtocolMessageTextSink{
		projector: projector, finalizer: finalizer, observer: observer,
		context: messageContext, messageID: strings.TrimSpace(messageID),
	}, nil
}

func (sink *ProtocolMessageTextSink) EmitTextDelta(
	ctx context.Context,
	emission TextDeltaEmission,
) error {
	if sink == nil || sink.projector == nil {
		return ErrTextStreamInvalid
	}
	result, err := sink.projector.ProjectDelta(ctx, chat.ProjectMessageDeltaInput{
		Context: sink.context, MessageID: sink.messageID,
		Index: emission.Index, Text: emission.Text, OccurredAt: emission.OccurredAt,
	})
	if err != nil {
		return err
	}
	if result.NotifyError != nil && sink.observer != nil {
		sink.observer.ObserveProtocolNotifyError(result.NotifyError)
	}
	return nil
}

func (sink *ProtocolMessageTextSink) CompleteText(
	ctx context.Context,
	completion TextStreamCompletion,
) error {
	if sink == nil || sink.finalizer == nil {
		return ErrTextStreamInvalid
	}
	return sink.finalizer.CompleteStreamedText(ctx, completion)
}

func (sink *ProtocolMessageTextSink) FailText(
	ctx context.Context,
	failure TextStreamFailure,
) error {
	if sink == nil || sink.finalizer == nil {
		return ErrTextStreamInvalid
	}
	return sink.finalizer.FailStreamedText(ctx, failure)
}

var _ TextDeltaSink = (*ProtocolMessageTextSink)(nil)
