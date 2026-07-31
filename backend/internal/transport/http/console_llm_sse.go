package httptransport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	ssetransport "actweave/backend/internal/transport/sse"

	"github.com/gin-gonic/gin"
)

// consoleLLMSSEHeartbeat is shorter than typical proxy idle timeouts so the
// connection stays warm while a long LLM call runs in the background.
const consoleLLMSSEHeartbeat = 15 * time.Second

// wantsConsoleLLMSSE reports whether the client asked for a streaming response.
func wantsConsoleLLMSSE(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	return acceptsEventStream(c.GetHeader("Accept"))
}

// consoleSSEWriter writes lightweight Console LLM job frames (not AAP protocol events).
type consoleSSEWriter struct {
	writer  http.ResponseWriter
	flusher http.Flusher
	seq     int64
}

func beginConsoleLLMSSE(c *gin.Context) (*consoleSSEWriter, error) {
	if c == nil {
		return nil, fmt.Errorf("missing gin context")
	}
	request, _ := RequestContextFrom(c.Request.Context())
	if err := ssetransport.ApplyHeaders(c.Writer.Header(), request.RequestID, request.TraceID); err != nil {
		// Fall back to minimal anti-buffering headers when request/trace ids are empty in tests.
		c.Header("Content-Type", ssetransport.ContentType)
		c.Header("Cache-Control", "no-cache, no-transform")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
	}
	c.Status(http.StatusOK)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming is not supported")
	}
	flusher.Flush()
	return &consoleSSEWriter{writer: c.Writer, flusher: flusher}, nil
}

func (w *consoleSSEWriter) Event(name string, data any) error {
	if w == nil || w.writer == nil {
		return fmt.Errorf("sse writer is nil")
	}
	w.seq++
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	// Compact single-line data frames.
	frame := fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", w.seq, strings.TrimSpace(name), payload)
	if _, err := w.writer.Write([]byte(frame)); err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}

func (w *consoleSSEWriter) Heartbeat() error {
	if w == nil || w.writer == nil {
		return fmt.Errorf("sse writer is nil")
	}
	if _, err := fmt.Fprintf(w.writer, ": ping %s\n\n", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}

// consoleMappedErrorBody builds a JSON-compatible error payload for SSE "failed" events.
func consoleMappedErrorBody(c *gin.Context, err error) gin.H {
	mapped := mapError(err)
	request, _ := RequestContextFrom(c.Request.Context())
	return gin.H{
		"error": ErrorDTO{
			Code:      mapped.code,
			Message:   mapped.message,
			RequestID: request.RequestID,
			TraceID:   request.TraceID,
			Retryable: mappedRetryable(mapped),
			Details:   []map[string]any{},
		},
	}
}

// streamConsoleLLMJob runs work with a context that is not cancelled when the
// client disconnects, while streaming heartbeats on the request connection.
// onResult is only invoked when the client is still connected.
func streamConsoleLLMJob[T any](
	c *gin.Context,
	work func(ctx context.Context) (T, error),
	onStarted func(stream *consoleSSEWriter) error,
	onResult func(stream *consoleSSEWriter, value T, err error) error,
) {
	stream, err := beginConsoleLLMSSE(c)
	if err != nil {
		RespondError(c, err)
		return
	}
	if onStarted != nil {
		if err := onStarted(stream); err != nil {
			return
		}
	}

	type outcome struct {
		value T
		err   error
	}
	// Detach LLM work from the HTTP request cancel so gateway/client disconnect
	// does not abort model generation mid-flight.
	workCtx := context.WithoutCancel(c.Request.Context())
	results := make(chan outcome, 1)
	go func() {
		value, workErr := work(workCtx)
		results <- outcome{value: value, err: workErr}
	}()

	ticker := time.NewTicker(consoleLLMSSEHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			// Client/gateway closed the stream; background work continues.
			return
		case out := <-results:
			if onResult != nil {
				_ = onResult(stream, out.value, out.err)
			}
			return
		case <-ticker.C:
			if err := stream.Heartbeat(); err != nil {
				return
			}
		}
	}
}
