package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/protocolschema"
	"actweave/backend/internal/transport/sse"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	catchUpWorkspaceID    = "11000000-0000-4000-8000-000000000101"
	catchUpAgentID        = "22000000-0000-4000-8000-000000000101"
	catchUpConversationID = "33000000-0000-4000-8000-000000000101"
	catchUpRunID          = "44000000-0000-4000-8000-000000000101"
	catchUpStreamID       = "55000000-0000-4000-8000-000000000101"
	catchUpItemID         = "66000000-0000-4000-8000-000000000101"
)

func TestAAPEventCatchUp(t *testing.T) {
	scope := catchUpScope()

	t.Run("replays stable pages after cursor", func(t *testing.T) {
		reader := newCatchUpReader(t, 5)
		response := requestCatchUp(t, reader, scope, "0", "2")
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		for key, want := range map[string]string{
			"Content-Type": sse.ContentType, "Cache-Control": "no-cache, no-transform",
			"Connection": "keep-alive", "X-Accel-Buffering": "no",
			"X-Request-ID": "request-catch-up", "X-Trace-ID": "trace-catch-up",
			"ActWeave-Protocol-Version": protocolschema.ProtocolVersion,
		} {
			if got := response.Header().Get(key); got != want {
				t.Fatalf("header %s=%q want=%q", key, got, want)
			}
		}
		body := response.Body.String()
		for sequence := 1; sequence <= 5; sequence++ {
			if strings.Count(body, "id: "+strconv.Itoa(sequence)+"\n") != 1 ||
				!strings.Contains(body, `"sequence":`+strconv.Itoa(sequence)) {
				t.Fatalf("sequence %d missing/duplicated: %s", sequence, body)
			}
		}
		wantCalls := []catchUpReadCall{{after: 0, limit: 2}, {after: 2, limit: 2}, {after: 4, limit: 1}}
		if !slices.Equal(reader.calls, wantCalls) {
			t.Fatalf("read calls=%+v want=%+v", reader.calls, wantCalls)
		}
	})

	t.Run("continues strictly after Last-Event-ID", func(t *testing.T) {
		reader := newCatchUpReader(t, 5)
		response := requestCatchUp(t, reader, scope, "3", "500")
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		body := response.Body.String()
		for _, absent := range []string{"id: 1\n", "id: 2\n", "id: 3\n"} {
			if strings.Contains(body, absent) {
				t.Fatalf("cursor replayed old event %q: %s", absent, body)
			}
		}
		if !strings.Contains(body, "id: 4\n") || !strings.Contains(body, "id: 5\n") {
			t.Fatalf("tail missing: %s", body)
		}
	})

	t.Run("rejects malformed negative and cross Run cursors before headers", func(t *testing.T) {
		for _, cursor := range []string{"bad", "-1", "+1", " 1", "1 ", "9223372036854775808", "6"} {
			reader := newCatchUpReader(t, 5)
			response := requestCatchUp(t, reader, scope, cursor, "2")
			assertCatchUpJSONError(t, response, http.StatusUnprocessableEntity, "REPLAY_CURSOR_INVALID")
		}
	})

	t.Run("enforces page bounds", func(t *testing.T) {
		for _, limit := range []string{"0", "-1", "501", "bad"} {
			reader := newCatchUpReader(t, 5)
			response := requestCatchUp(t, reader, scope, "0", limit)
			assertCatchUpJSONError(t, response, http.StatusUnprocessableEntity, "REPLAY_CURSOR_INVALID")
		}
	})

	t.Run("uses complete scope", func(t *testing.T) {
		reader := newCatchUpReader(t, 2)
		wrong := scope
		wrong.AgentID = uuid.NewString()
		response := requestCatchUp(t, reader, wrong, "0", "2")
		assertCatchUpJSONError(t, response, http.StatusNotFound, "NOT_FOUND")
		if len(reader.calls) != 0 {
			t.Fatalf("events read after scope mismatch: %+v", reader.calls)
		}
	})

	t.Run("rejects a gap before committing SSE headers", func(t *testing.T) {
		reader := newCatchUpReader(t, 3)
		reader.events[0], reader.events[1] = reader.events[1], reader.events[0]
		response := requestCatchUp(t, reader, scope, "0", "2")
		assertCatchUpJSONError(t, response, http.StatusInternalServerError, "INTERNAL_ERROR")
	})

	t.Run("uses a cursorless transport signal after headers", func(t *testing.T) {
		reader := newCatchUpReader(t, 3)
		reader.readErrors[2] = errors.New("database unavailable")
		response := requestCatchUp(t, reader, scope, "0", "2")
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != sse.ContentType {
			t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
		}
		body := response.Body.String()
		if strings.Count(body, "id: ") != 2 || !strings.Contains(body, "event: stream.error\n") ||
			!strings.Contains(body, `"code":"STREAM_READ_FAILED"`) {
			t.Fatalf("post-header failure=%s", body)
		}
		transportFrame := body[strings.Index(body, "event: stream.error\n"):]
		if strings.Contains(transportFrame, "id: ") || strings.Contains(transportFrame, `"sequence"`) {
			t.Fatalf("transport signal advanced cursor: %s", transportFrame)
		}
	})

	t.Run("initial read failure remains JSON", func(t *testing.T) {
		reader := newCatchUpReader(t, 3)
		reader.readErrors[0] = errors.New("database unavailable")
		response := requestCatchUp(t, reader, scope, "0", "2")
		assertCatchUpJSONError(t, response, http.StatusInternalServerError, "INTERNAL_ERROR")
	})

	t.Run("bounds pending page and catch-up batches", func(t *testing.T) {
		reader := newCatchUpReader(t, 5)
		policy := sse.DefaultBackpressurePolicy()
		policy.MaxPendingEvents = 2
		policy.MaxCatchUpBatches = 1
		limiter, err := sse.NewInMemoryConnectionLimiter(policy)
		if err != nil {
			t.Fatal(err)
		}
		handler, err := NewAAPEventCatchUp(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := handler.ConfigureBackpressure(policy, limiter); err != nil {
			t.Fatal(err)
		}
		session := AAPStreamSession{Connection: sse.ConnectionIdentity{
			ClientID: "client", SubjectID: "subject", RunID: scope.RunID,
		}}
		response := requestCatchUpHandler(t, handler, scope, "0", "500", session)
		if response.Code != http.StatusOK || strings.Count(response.Body.String(), "id: ") != 2 ||
			!strings.Contains(response.Body.String(), `"code":"CATCH_UP_LIMIT_REACHED"`) {
			t.Fatalf("bounded catch-up status=%d body=%s", response.Code, response.Body.String())
		}
		if len(reader.calls) != 1 || reader.calls[0].limit != 2 {
			t.Fatalf("unbounded read calls=%+v", reader.calls)
		}
		if stats := limiter.Stats(); stats.Active != 0 || stats.Acquired != 1 || stats.Released != 1 {
			t.Fatalf("connection lease was not released: %+v", stats)
		}
	})
}

type catchUpReadCall struct {
	after int64
	limit int
}

type fakeCatchUpReader struct {
	scope      protocolevent.RunScope
	events     []protocolevent.ProtocolEvent
	readErrors map[int64]error
	calls      []catchUpReadCall
}

func newCatchUpReader(t *testing.T, count int) *fakeCatchUpReader {
	t.Helper()
	return &fakeCatchUpReader{
		scope: catchUpScope(), events: catchUpEvents(t, count),
		readErrors: make(map[int64]error),
	}
}

func (reader *fakeCatchUpReader) HighWatermark(
	_ context.Context,
	scope protocolevent.RunScope,
) (int64, error) {
	if scope != reader.scope {
		return 0, protocolevent.ErrRunScopeNotFound
	}
	return int64(len(reader.events)), nil
}

func (reader *fakeCatchUpReader) ReadRunAfter(
	_ context.Context,
	scope protocolevent.RunScope,
	after int64,
	limit int,
) ([]protocolevent.ProtocolEvent, error) {
	if scope != reader.scope {
		return nil, protocolevent.ErrRunScopeNotFound
	}
	reader.calls = append(reader.calls, catchUpReadCall{after: after, limit: limit})
	if err := reader.readErrors[after]; err != nil {
		return nil, err
	}
	start := int(after)
	if start >= len(reader.events) {
		return nil, nil
	}
	end := min(start+limit, len(reader.events))
	return append([]protocolevent.ProtocolEvent(nil), reader.events[start:end]...), nil
}

func requestCatchUp(
	t *testing.T,
	reader AAPProtocolEventReader,
	scope protocolevent.RunScope,
	cursor, limit string,
) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := NewAAPEventCatchUp(reader)
	if err != nil {
		t.Fatal(err)
	}
	return requestCatchUpHandler(t, handler, scope, cursor, limit)
}

func requestCatchUpHandler(
	t *testing.T,
	handler *AAPEventCatchUp,
	scope protocolevent.RunScope,
	cursor, limit string,
	sessions ...AAPStreamSession,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(requestContextMiddleware())
	router.GET("/events", func(c *gin.Context) { handler.Stream(c, scope, sessions...) })
	request := httptest.NewRequest(http.MethodGet, "/events?limit="+limit, nil)
	request.Header.Set("X-Request-ID", "request-catch-up")
	request.Header.Set("X-Trace-ID", "trace-catch-up")
	if cursor != "" {
		request.Header.Set("Last-Event-ID", cursor)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(deadlineResponseRecorder{ResponseRecorder: response}, request)
	return response
}

type deadlineResponseRecorder struct{ *httptest.ResponseRecorder }

func (deadlineResponseRecorder) SetWriteDeadline(time.Time) error { return nil }

func assertCatchUpJSONError(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	code string,
) {
	t.Helper()
	if response.Code != status || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") ||
		response.Header().Get("Content-Type") == sse.ContentType ||
		!strings.Contains(response.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func catchUpScope() protocolevent.RunScope {
	return protocolevent.RunScope{
		WorkspaceID: catchUpWorkspaceID, AgentID: catchUpAgentID,
		ConversationID: catchUpConversationID, RunID: catchUpRunID,
	}
}

func catchUpEvents(t *testing.T, count int) []protocolevent.ProtocolEvent {
	t.Helper()
	events := make([]protocolevent.ProtocolEvent, 0, count)
	for index := 1; index <= count; index++ {
		occurredAt := time.Date(2026, 7, 20, 6, 0, index, index*1000, time.UTC)
		built, err := protocolevent.BuildProtocolEvent(protocolevent.NewProtocolEvent{
			ID: uuid.NewString(), EventStreamID: catchUpStreamID,
			WorkspaceID: catchUpWorkspaceID, AgentID: catchUpAgentID,
			ConversationID: catchUpConversationID, RunID: catchUpRunID,
			Type: protocolevent.EventItemDelta, SpecVersion: protocolschema.SpecVersion,
			TraceID: "trace-catch-up", ItemID: catchUpItemID, OccurredAt: occurredAt,
		}, protocolevent.ItemDeltaData{ItemID: catchUpItemID, Delta: protocolevent.TextDelta{
			Type: protocolevent.DeltaTypeText, Index: 0, Text: "片段 " + strconv.Itoa(index),
		}})
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(map[string]any{
			"specVersion": built.SpecVersion, "type": built.Type, "eventId": built.ID,
			"streamId": "run:" + built.RunID, "sequence": index, "occurredAt": occurredAt,
			"workspaceId": built.WorkspaceID, "agentId": built.AgentID,
			"conversationId": built.ConversationID, "runId": built.RunID,
			"traceId": built.TraceID, "data": json.RawMessage(built.Data),
		})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, protocolevent.ProtocolEvent{
			ID: built.ID, EventStreamID: built.EventStreamID, StreamID: "run:" + built.RunID,
			WorkspaceID: built.WorkspaceID, AgentID: built.AgentID,
			ConversationID: built.ConversationID, RunID: built.RunID,
			Type: built.Type, SpecVersion: built.SpecVersion, TraceID: built.TraceID,
			ItemID: built.ItemID, Sequence: int64(index), OccurredAt: occurredAt,
			Data: built.Data, Payload: payload,
		})
	}
	return events
}
