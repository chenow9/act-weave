package httptransport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/metrics"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/protocolschema"
	"actweave/backend/internal/transport/sse"

	"github.com/gin-gonic/gin"
)

var (
	ErrAAPEventCursorInvalid = errors.New("AAP event replay cursor is invalid")
	ErrAAPEventStreamInvalid = errors.New("AAP persisted event stream is invalid")
)

type AAPProtocolEventReader interface {
	ReadRunAfter(context.Context, protocolevent.RunScope, int64, int) ([]protocolevent.ProtocolEvent, error)
	HighWatermark(context.Context, protocolevent.RunScope) (int64, error)
}

type AAPProtocolEventFollower interface {
	Follow(
		context.Context,
		protocolevent.RunScope,
		int64,
		func([]protocolevent.ProtocolEvent) error,
	) error
}

type aapHeartbeatFollower interface {
	FollowWithHeartbeat(
		context.Context,
		protocolevent.RunScope,
		int64,
		func([]protocolevent.ProtocolEvent) error,
		func(time.Time) error,
	) error
}

type AAPStreamRevalidator interface {
	Validate(context.Context, agentaccessauth.StreamBinding) error
	Monitor(context.Context, agentaccessauth.StreamBinding) error
}

type AAPStreamSession struct {
	Connection    sse.ConnectionIdentity
	Authorization *agentaccessauth.StreamBinding
}

// AAPEventCatchUp writes a stable, finite snapshot of committed Run events.
// Live follow is deliberately layered on top of this boundary in M4-T3.
type AAPEventCatchUp struct {
	reader       AAPProtocolEventReader
	encoder      *sse.Encoder
	follower     AAPProtocolEventFollower
	backpressure *sse.BackpressurePolicy
	connections  sse.ConnectionLimiter
	revalidator  AAPStreamRevalidator
	now          func() time.Time
}

func (handler *AAPEventCatchUp) ConfigureRevalidator(
	revalidator AAPStreamRevalidator,
) error {
	if handler == nil || revalidator == nil {
		return agentaccessauth.ErrStreamRevalidationInvalid
	}
	handler.revalidator = revalidator
	return nil
}

func (handler *AAPEventCatchUp) ConfigureBackpressure(
	policy sse.BackpressurePolicy,
	connections sse.ConnectionLimiter,
) error {
	if handler == nil || connections == nil || policy.Validate() != nil {
		return sse.ErrBackpressureInvalid
	}
	handler.backpressure = &policy
	handler.connections = connections
	return nil
}

func NewAAPEventCatchUp(
	reader AAPProtocolEventReader,
	followers ...AAPProtocolEventFollower,
) (*AAPEventCatchUp, error) {
	if reader == nil || len(followers) > 1 || (len(followers) == 1 && followers[0] == nil) {
		return nil, errors.New("AAP event reader is required")
	}
	var follower AAPProtocolEventFollower
	if len(followers) == 1 {
		follower = followers[0]
	}
	return &AAPEventCatchUp{
		reader: reader, encoder: sse.NewEncoder(), follower: follower,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

// Stream validates all inputs and the first database page before committing
// SSE headers. Later read/validation failures are transport signals and never
// masquerade as persisted Protocol Events.
func (handler *AAPEventCatchUp) Stream(
	c *gin.Context,
	scope protocolevent.RunScope,
	sessions ...AAPStreamSession,
) {
	handler.stream(c, scope, nil, false, sessions...)
}

// StreamFrom is used by POST /runs attach semantics. Unlike a reconnecting GET,
// an idempotent POST attach always begins at the explicit server cursor (zero
// in v1) and never accepts a caller-supplied Last-Event-ID for another Run.
func (handler *AAPEventCatchUp) StreamFrom(
	c *gin.Context,
	scope protocolevent.RunScope,
	cursor int64,
	sessions ...AAPStreamSession,
) {
	handler.stream(c, scope, &cursor, true, sessions...)
}

func (handler *AAPEventCatchUp) stream(
	c *gin.Context,
	scope protocolevent.RunScope,
	fixedCursor *int64,
	forceFollow bool,
	sessions ...AAPStreamSession,
) {
	if handler == nil || handler.reader == nil || handler.encoder == nil || c == nil {
		if c != nil {
			RespondError(c, ErrAAPEventStreamInvalid)
		}
		return
	}
	var streamLease sse.ConnectionLease
	if handler.connections != nil {
		if len(sessions) != 1 {
			RespondError(c, sse.ErrBackpressureInvalid)
			return
		}
		lease, err := handler.connections.Acquire(c.Request.Context(), sessions[0].Connection)
		if err != nil {
			RespondError(c, err)
			return
		}
		if stats, ok := handler.connections.(interface{ Stats() sse.ConnectionLimiterStats }); ok {
			metrics.Default().SetSSEActiveConnections(int64(stats.Stats().Active))
		}
		streamLease = lease
		defer func() {
			_ = lease.Close()
			if stats, ok := handler.connections.(interface{ Stats() sse.ConnectionLimiterStats }); ok {
				metrics.Default().SetSSEActiveConnections(int64(stats.Stats().Active))
			}
		}()
	}
	if handler.revalidator != nil && (len(sessions) != 1 || sessions[0].Authorization == nil) {
		RespondError(c, agentaccessauth.ErrStreamRevalidationInvalid)
		return
	}
	batchSize, err := optionalPositiveInt(c.Query("limit"), 100, 500)
	if err != nil {
		RespondError(c, ErrAAPEventCursorInvalid)
		return
	}
	if handler.backpressure != nil && batchSize > handler.backpressure.MaxPendingEvents {
		batchSize = handler.backpressure.MaxPendingEvents
	}
	followLive := true
	if !forceFollow {
		followLive, err = parseAAPFollow(c.Query("follow"))
		if err != nil {
			RespondError(c, err)
			return
		}
	}
	if handler.revalidator != nil {
		if err := handler.revalidator.Validate(c.Request.Context(), *sessions[0].Authorization); err != nil {
			RespondError(c, err)
			return
		}
	}
	streamStarted := handler.now()
	var cursor int64
	if fixedCursor == nil {
		cursor, err = parseAAPLastEventID(c.GetHeader("Last-Event-ID"))
		if err != nil {
			RespondError(c, err)
			return
		}
		if cursor > 0 {
			metrics.Default().ObserveSSEReconnect(map[string]string{
				"workspace_id": scope.WorkspaceID,
				"agent_id":     scope.AgentID,
				"run_id":       scope.RunID,
			})
		}
	} else {
		cursor = *fixedCursor
		if cursor < 0 {
			RespondError(c, ErrAAPEventCursorInvalid)
			return
		}
	}
	highWatermark, err := handler.reader.HighWatermark(c.Request.Context(), scope)
	if err != nil {
		RespondError(c, err)
		return
	}
	// A v1 Cursor is numeric and scoped by this endpoint. Values beyond this
	// Run's committed watermark are rejected instead of being treated as a
	// cursor copied from another Run or from uncommitted state.
	if highWatermark < 0 || cursor > highWatermark {
		RespondError(c, ErrAAPEventCursorInvalid)
		return
	}

	firstFrame, nextCursor, err := handler.readEncodedPage(
		c.Request.Context(), scope, cursor, highWatermark, batchSize,
	)
	if err != nil {
		RespondError(c, err)
		return
	}
	replayEvents := 0
	if nextCursor > cursor {
		replayEvents = int(nextCursor - cursor)
	}
	if replayEvents > 0 || cursor > 0 {
		metrics.Default().ObserveSSEReplay(replayEvents, time.Since(streamStarted), map[string]string{
			"workspace_id": scope.WorkspaceID,
			"agent_id":     scope.AgentID,
			"run_id":       scope.RunID,
		})
	}
	streamWriter := io.Writer(c.Writer)
	if handler.backpressure != nil {
		controller := http.NewResponseController(c.Writer)
		deadlineWriter, writerErr := sse.NewDeadlineWriter(
			c.Writer, controller.SetWriteDeadline, handler.backpressure.WriteTimeout,
		)
		if writerErr != nil {
			RespondError(c, writerErr)
			return
		}
		streamWriter = deadlineWriter
	}
	request, ok := RequestContextFrom(c.Request.Context())
	if !ok || sse.ApplyHeaders(c.Writer.Header(), request.RequestID, request.TraceID) != nil {
		RespondError(c, ErrAAPEventStreamInvalid)
		return
	}
	c.Header("ActWeave-Protocol-Version", protocolschema.ProtocolVersion)
	c.Status(http.StatusOK)
	streamContext := c.Request.Context()
	var stopRevalidation context.CancelCauseFunc
	var revalidationDone <-chan error
	if handler.revalidator != nil {
		var monitored context.Context
		monitored, stopRevalidation = context.WithCancelCause(streamContext)
		streamContext = monitored
		done := make(chan error, 1)
		revalidationDone = done
		binding := *sessions[0].Authorization
		go func() {
			monitorErr := handler.revalidator.Monitor(monitored, binding)
			if monitorErr != nil && !errors.Is(monitorErr, context.Canceled) &&
				!errors.Is(monitorErr, context.DeadlineExceeded) {
				stopRevalidation(monitorErr)
			}
			done <- monitorErr
		}()
		defer func() {
			stopRevalidation(context.Canceled)
			<-revalidationDone
		}()
	}
	if !handler.writePage(c, streamWriter, firstFrame) {
		return
	}
	cursor = nextCursor
	batches := 0
	if len(firstFrame) > 0 {
		batches = 1
	}

	for cursor < highWatermark {
		if authErr := streamRevalidationError(streamContext); authErr != nil {
			handler.writeStreamError(c, streamWriter, agentaccessauth.StreamErrorCode(authErr))
			return
		}
		if handler.backpressure != nil && batches >= handler.backpressure.MaxCatchUpBatches {
			handler.writeStreamError(c, streamWriter, "CATCH_UP_LIMIT_REACHED")
			return
		}
		frame, next, pageErr := handler.readEncodedPage(
			c.Request.Context(), scope, cursor, highWatermark, batchSize,
		)
		if pageErr != nil {
			handler.writeStreamError(c, streamWriter, "STREAM_READ_FAILED")
			return
		}
		if !handler.writePage(c, streamWriter, frame) {
			return
		}
		cursor = next
		batches++
	}
	if !followLive || handler.follower == nil {
		return
	}
	deliver := func(events []protocolevent.ProtocolEvent) error {
		var frame bytes.Buffer
		for _, event := range events {
			if encodeErr := handler.encoder.Encode(&frame, event); encodeErr != nil {
				return ErrAAPEventStreamInvalid
			}
		}
		if _, writeErr := streamWriter.Write(frame.Bytes()); writeErr != nil {
			if errors.Is(writeErr, sse.ErrSlowConsumer) {
				metrics.Default().ObserveSSESlowConsumerDisconnect(map[string]string{
					"workspace_id": scope.WorkspaceID,
					"agent_id":     scope.AgentID,
					"run_id":       scope.RunID,
				})
			}
			return writeErr
		}
		c.Writer.Flush()
		return nil
	}
	if heartbeatFollower, ok := handler.follower.(aapHeartbeatFollower); ok {
		err = heartbeatFollower.FollowWithHeartbeat(
			streamContext, scope, cursor, deliver,
			func(occurredAt time.Time) error {
				if refresher, ok := streamLease.(interface{ Refresh(context.Context) error }); ok {
					_ = refresher.Refresh(streamContext)
				}
				if heartbeatErr := handler.encoder.Heartbeat(streamWriter, occurredAt); heartbeatErr != nil {
					return heartbeatErr
				}
				c.Writer.Flush()
				return nil
			},
		)
	} else {
		err = handler.follower.Follow(streamContext, scope, cursor, deliver)
	}
	if authErr := streamRevalidationError(streamContext); authErr != nil {
		handler.writeStreamError(c, streamWriter, agentaccessauth.StreamErrorCode(authErr))
		return
	}
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		handler.writeStreamError(c, streamWriter, "STREAM_FOLLOW_FAILED")
	}
}

func streamRevalidationError(ctx context.Context) error {
	cause := context.Cause(ctx)
	if cause == nil || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return nil
	}
	return cause
}

func (handler *AAPEventCatchUp) readEncodedPage(
	ctx context.Context,
	scope protocolevent.RunScope,
	after, highWatermark int64,
	batchSize int,
) ([]byte, int64, error) {
	if after == highWatermark {
		return nil, after, nil
	}
	remaining := highWatermark - after
	limit := batchSize
	if remaining < int64(limit) {
		limit = int(remaining)
	}
	events, err := handler.reader.ReadRunAfter(ctx, scope, after, limit)
	if err != nil {
		return nil, after, err
	}
	if len(events) == 0 || len(events) > limit {
		return nil, after, ErrAAPEventStreamInvalid
	}
	var frame bytes.Buffer
	expected := after + 1
	for _, event := range events {
		if event.Sequence != expected || event.Sequence > highWatermark {
			return nil, after, ErrAAPEventStreamInvalid
		}
		if err := handler.encoder.Encode(&frame, event); err != nil {
			return nil, after, fmt.Errorf("encode AAP event page: %w", ErrAAPEventStreamInvalid)
		}
		expected++
	}
	return frame.Bytes(), expected - 1, nil
}

func (handler *AAPEventCatchUp) writePage(
	c *gin.Context,
	writer io.Writer,
	frame []byte,
) bool {
	if len(frame) == 0 {
		return true
	}
	if _, err := writer.Write(frame); err != nil {
		if errors.Is(err, sse.ErrSlowConsumer) {
			metrics.Default().ObserveSSESlowConsumerDisconnect(nil)
		}
		return false
	}
	c.Writer.Flush()
	return true
}

func (handler *AAPEventCatchUp) writeStreamError(
	c *gin.Context,
	writer io.Writer,
	code string,
) {
	request, _ := RequestContextFrom(c.Request.Context())
	signal := sse.NewStreamErrorSignal(
		code, "The event stream could not be continued.", true,
		request.RequestID, request.TraceID, nil, handler.now(),
	)
	_ = handler.encoder.EncodeStreamError(writer, signal)
	c.Writer.Flush()
}

func parseAAPLastEventID(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	if value != strings.TrimSpace(value) || len(value) > 19 {
		return 0, ErrAAPEventCursorInvalid
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, ErrAAPEventCursorInvalid
		}
	}
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor < 0 {
		return 0, ErrAAPEventCursorInvalid
	}
	return cursor, nil
}

func parseAAPFollow(value string) (bool, error) {
	switch value {
	case "", "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, ErrAAPEventCursorInvalid
	}
}
