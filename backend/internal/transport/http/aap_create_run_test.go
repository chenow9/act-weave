package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/protocolschema"
	"actweave/backend/internal/transport/sse"

	"github.com/gin-gonic/gin"
)

const (
	createRunID           = "77000000-0000-4000-8000-000000000401"
	createConversationID  = "33000000-0000-4000-8000-000000000401"
	createEventStreamID   = "55000000-0000-4000-8000-000000000401"
	createAcceptedEventID = "aa000000-0000-4000-8000-000000000401"
	createFailedEventID   = "aa000000-0000-4000-8000-000000000402"
	createIdempotencyKey  = "99000000-0000-4000-8000-000000000401"
)

func TestCreateRunStreamingAttach(t *testing.T) {
	t.Run("commits accepted before SSE and persists post-header failure", func(t *testing.T) {
		accepted := createRunProtocolEvent(t, 1, protocolevent.EventRunAccepted)
		failed := createRunProtocolEvent(t, 2, protocolevent.EventRunFailed)
		reader := &createRunEventReader{}
		creator := newFakeAAPRunCreator(reader, accepted)
		follower := &createRunFollower{reader: reader, event: failed}
		handler := newTestCreateRunHandler(t, creator, reader, follower)
		response := requestCreateRun(t, handler, validCreateRunRequest(true), createIdempotencyKey, "text/event-stream")
		if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/event-stream") {
			t.Fatalf("stream status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
		}
		body := response.Body.String()
		acceptedAt, failedAt := strings.Index(body, "event: run.accepted\n"), strings.Index(body, "event: run.failed\n")
		if acceptedAt < 0 || failedAt <= acceptedAt || strings.Contains(body, "item.delta") ||
			strings.Contains(body, "event: stream.error") {
			t.Fatalf("stream did not expose accepted then persistent failure: %s", body)
		}
		if !creator.committed || creator.sideEffects != 1 || follower.startedBeforeCommit {
			t.Fatalf("creator/follower order committed=%v sideEffects=%d followerBeforeCommit=%v",
				creator.committed, creator.sideEffects, follower.startedBeforeCommit)
		}
	})

	t.Run("non-streaming returns accepted snapshot and events link", func(t *testing.T) {
		accepted := createRunProtocolEvent(t, 1, protocolevent.EventRunAccepted)
		reader := &createRunEventReader{}
		creator := newFakeAAPRunCreator(reader, accepted)
		handler := newTestCreateRunHandler(t, creator, reader, nil)
		response := requestCreateRun(t, handler, validCreateRunRequest(false), createIdempotencyKey, "application/json")
		if response.Code != http.StatusAccepted ||
			response.Header().Get("ActWeave-Protocol-Version") != protocolschema.ProtocolVersion ||
			!strings.Contains(response.Body.String(), `"status":"accepted"`) ||
			!strings.Contains(response.Body.String(), createRunID+`/events`) ||
			strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") {
			t.Fatalf("async status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
		}
	})

	t.Run("idempotent retry attaches to the same committed Run from zero", func(t *testing.T) {
		accepted := createRunProtocolEvent(t, 1, protocolevent.EventRunAccepted)
		reader := &createRunEventReader{}
		creator := newFakeAAPRunCreator(reader, accepted)
		handler := newTestCreateRunHandler(t, creator, reader, nil)
		first := requestCreateRun(t, handler, validCreateRunRequest(true), createIdempotencyKey, "text/event-stream")
		second := requestCreateRun(t, handler, validCreateRunRequest(true), createIdempotencyKey, "text/event-stream")
		changedTransport := requestCreateRun(t, handler, validCreateRunRequest(false), createIdempotencyKey, "application/json")
		if first.Code != http.StatusOK || second.Code != http.StatusOK ||
			changedTransport.Code != http.StatusAccepted ||
			!strings.Contains(changedTransport.Body.String(), `"idempotent":true`) ||
			first.Body.String() != second.Body.String() || creator.calls != 3 || creator.sideEffects != 1 ||
			strings.Count(second.Body.String(), "id: 1\n") != 1 {
			t.Fatalf("idempotent attach first=%s second=%s changed=%s calls=%d sideEffects=%d",
				first.Body.String(), second.Body.String(), changedTransport.Body.String(),
				creator.calls, creator.sideEffects)
		}
	})

	t.Run("validation and idempotency errors remain JSON before SSE headers", func(t *testing.T) {
		accepted := createRunProtocolEvent(t, 1, protocolevent.EventRunAccepted)
		reader := &createRunEventReader{}
		creator := newFakeAAPRunCreator(reader, accepted)
		handler := newTestCreateRunHandler(t, creator, reader, nil)
		invalid := validCreateRunRequest(true)
		invalid.Input[0].Content[0].Type = "image"
		response := requestCreateRun(t, handler, invalid, createIdempotencyKey, "text/event-stream")
		assertCreateRunJSONError(t, response, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
		if creator.calls != 0 {
			t.Fatalf("creator called for invalid request: %d", creator.calls)
		}

		first := requestCreateRun(t, handler, validCreateRunRequest(false), createIdempotencyKey, "application/json")
		if first.Code != http.StatusAccepted {
			t.Fatalf("first idempotent request=%d body=%s", first.Code, first.Body.String())
		}
		conflict := validCreateRunRequest(false)
		conflict.Metadata["businessRequestId"] = "different"
		response = requestCreateRun(t, handler, conflict, createIdempotencyKey, "application/json")
		assertCreateRunJSONError(t, response, http.StatusConflict, "IDEMPOTENCY_CONFLICT")
	})

	t.Run("invalid creator result fails before headers", func(t *testing.T) {
		accepted := createRunProtocolEvent(t, 1, protocolevent.EventRunAccepted)
		accepted.Sequence = 2
		reader := &createRunEventReader{}
		creator := newFakeAAPRunCreator(reader, accepted)
		handler := newTestCreateRunHandler(t, creator, reader, nil)
		response := requestCreateRun(t, handler, validCreateRunRequest(true), createIdempotencyKey, "text/event-stream")
		assertCreateRunJSONError(t, response, http.StatusInternalServerError, "INTERNAL_ERROR")
	})
}

type fakeAAPRunCreator struct {
	mu          sync.Mutex
	reader      *createRunEventReader
	accepted    protocolevent.ProtocolEvent
	cached      *AAPCreateRunCommand
	calls       int
	sideEffects int
	committed   bool
}

func newFakeAAPRunCreator(
	reader *createRunEventReader,
	accepted protocolevent.ProtocolEvent,
) *fakeAAPRunCreator {
	return &fakeAAPRunCreator{reader: reader, accepted: accepted}
}

func (creator *fakeAAPRunCreator) CreateRun(
	_ context.Context,
	command AAPCreateRunCommand,
) (AAPCreateRunResult, error) {
	creator.mu.Lock()
	defer creator.mu.Unlock()
	creator.calls++
	if creator.cached != nil {
		if !sameCreateRunIntent(*creator.cached, command) {
			return AAPCreateRunResult{}, ErrAAPRunIdempotencyConflict
		}
		return AAPCreateRunResult{
			RunID: createRunID, ConversationID: createConversationID,
			AcceptedEvent: creator.accepted, Idempotent: true,
		}, nil
	}
	cached := command
	creator.cached = &cached
	creator.sideEffects++
	creator.reader.append(creator.accepted)
	creator.committed = true
	return AAPCreateRunResult{
		RunID: createRunID, ConversationID: createConversationID,
		AcceptedEvent: creator.accepted,
	}, nil
}

func sameCreateRunIntent(left, right AAPCreateRunCommand) bool {
	// TraceID is observability context for the current attempt and is not part
	// of the normalized idempotency request hash.
	left.TraceID, right.TraceID = "", ""
	return reflect.DeepEqual(left, right)
}

type createRunEventReader struct {
	mu     sync.Mutex
	events []protocolevent.ProtocolEvent
}

func (reader *createRunEventReader) append(event protocolevent.ProtocolEvent) {
	reader.mu.Lock()
	reader.events = append(reader.events, event)
	reader.mu.Unlock()
}

func (reader *createRunEventReader) HighWatermark(
	_ context.Context,
	_ protocolevent.RunScope,
) (int64, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return int64(len(reader.events)), nil
}

func (reader *createRunEventReader) ReadRunAfter(
	_ context.Context,
	_ protocolevent.RunScope,
	after int64,
	limit int,
) ([]protocolevent.ProtocolEvent, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	start := int(after)
	if start >= len(reader.events) {
		return nil, nil
	}
	end := min(start+limit, len(reader.events))
	return append([]protocolevent.ProtocolEvent(nil), reader.events[start:end]...), nil
}

type createRunFollower struct {
	reader              *createRunEventReader
	event               protocolevent.ProtocolEvent
	startedBeforeCommit bool
}

func (follower *createRunFollower) Follow(
	_ context.Context,
	_ protocolevent.RunScope,
	cursor int64,
	deliver func([]protocolevent.ProtocolEvent) error,
) error {
	if cursor != 1 {
		return errors.New("streaming attach did not start after accepted")
	}
	follower.reader.mu.Lock()
	follower.startedBeforeCommit = len(follower.reader.events) == 0
	follower.reader.mu.Unlock()
	follower.reader.append(follower.event)
	return deliver([]protocolevent.ProtocolEvent{follower.event})
}

func newTestCreateRunHandler(
	t *testing.T,
	creator AAPRunCreator,
	reader AAPProtocolEventReader,
	follower AAPProtocolEventFollower,
) *AAPCreateRunHandler {
	t.Helper()
	var attacher *AAPEventCatchUp
	var err error
	if follower == nil {
		attacher, err = NewAAPEventCatchUp(reader)
	} else {
		attacher, err = NewAAPEventCatchUp(reader, follower)
	}
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewAAPCreateRunHandler(creator, attacher)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func requestCreateRun(
	t *testing.T,
	handler *AAPCreateRunHandler,
	body AAPCreateRunRequest,
	idempotencyKey, accept string,
) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(requestContextMiddleware())
	router.POST("/runs", func(c *gin.Context) {
		handler.Create(c, AAPCreateRunScope{
			WorkspaceID: catchUpWorkspaceID, AgentID: catchUpAgentID,
		}, AAPStreamSession{Connection: sseCreateRunIdentity()})
	})
	request := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", accept)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.Header.Set("X-Request-ID", "request-create-run")
	request.Header.Set("X-Trace-ID", "trace-create-run")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func validCreateRunRequest(stream bool) AAPCreateRunRequest {
	return AAPCreateRunRequest{
		ConversationID: createConversationID,
		Input: []AAPRunInputItem{{
			Type: "message", Role: "user",
			Content: []AAPRunContentPart{{Type: "text", Text: "查询订单状态"}},
		}},
		Stream: stream, Metadata: map[string]string{"businessRequestId": "ORD-401"},
	}
}

func sseCreateRunIdentity() (identity sse.ConnectionIdentity) {
	identity.ClientID = "client-create-run"
	identity.SubjectID = "subject-create-run"
	identity.RunID = createRunID
	return identity
}

func assertCreateRunJSONError(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	code string,
) {
	t.Helper()
	if response.Code != status || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") ||
		!strings.Contains(response.Body.String(), `"code":"`+code+`"`) ||
		strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func createRunProtocolEvent(
	t *testing.T,
	sequence int64,
	eventType string,
) protocolevent.ProtocolEvent {
	t.Helper()
	startedAt := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	run := protocolevent.Run{
		ID: createRunID, ConversationID: createConversationID, AgentID: catchUpAgentID,
		Status: protocolevent.RunStatusAccepted, Trigger: protocolevent.RunTriggerAPI,
		StartedAt: startedAt,
	}
	eventID := createAcceptedEventID
	if eventType == protocolevent.EventRunFailed {
		completedAt := startedAt.Add(time.Second)
		run.Status, run.CompletedAt = protocolevent.RunStatusFailed, &completedAt
		run.Error = &protocolevent.ProtocolError{
			Code: "MODEL_PROVIDER_FAILED", Message: "The model request failed.", Retryable: true,
		}
		eventID = createFailedEventID
	}
	built, err := protocolevent.BuildProtocolEvent(protocolevent.NewProtocolEvent{
		ID: eventID, EventStreamID: createEventStreamID,
		WorkspaceID: catchUpWorkspaceID, AgentID: catchUpAgentID,
		ConversationID: createConversationID, RunID: createRunID,
		Type: eventType, SpecVersion: protocolschema.SpecVersion,
		TraceID: "trace-create-run", OccurredAt: startedAt.Add(time.Duration(sequence-1) * time.Second),
	}, protocolevent.RunSnapshotData{Run: run})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"specVersion": built.SpecVersion, "type": built.Type, "eventId": built.ID,
		"streamId": "run:" + built.RunID, "sequence": sequence,
		"occurredAt": built.OccurredAt, "workspaceId": built.WorkspaceID,
		"agentId": built.AgentID, "conversationId": built.ConversationID,
		"runId": built.RunID, "traceId": built.TraceID, "data": json.RawMessage(built.Data),
	})
	if err != nil {
		t.Fatal(err)
	}
	return protocolevent.ProtocolEvent{
		ID: built.ID, EventStreamID: built.EventStreamID, StreamID: "run:" + built.RunID,
		WorkspaceID: built.WorkspaceID, AgentID: built.AgentID,
		ConversationID: built.ConversationID, RunID: built.RunID,
		Type: built.Type, SpecVersion: built.SpecVersion, TraceID: built.TraceID,
		Sequence: sequence, OccurredAt: built.OccurredAt, Data: built.Data, Payload: payload,
	}
}
