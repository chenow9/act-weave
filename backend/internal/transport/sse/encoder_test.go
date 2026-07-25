package sse_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/transport/sse"
)

const (
	sseWorkspaceID    = "e18f1f2e-7b5a-7c3d-8e9f-123456789001"
	sseAgentID        = "e18f1f2e-7b5a-7c3d-8e9f-123456789002"
	sseConversationID = "e18f1f2e-7b5a-7c3d-8e9f-123456789003"
	sseRunID          = "e18f1f2e-7b5a-7c3d-8e9f-123456789004"
	sseStreamID       = "e18f1f2e-7b5a-7c3d-8e9f-123456789005"
	sseEventID        = "e18f1f2e-7b5a-7c3d-8e9f-123456789006"
	sseItemID         = "e18f1f2e-7b5a-7c3d-8e9f-123456789007"
)

func TestAAPEncoder(t *testing.T) {
	t.Run("protocol event uses one JSON data line", func(t *testing.T) {
		encoder := sse.NewEncoder()
		event := persistedSSEEvent(t, "正在处理\n下一行")
		var output bytes.Buffer
		if err := encoder.Encode(&output, event); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(output.String(), "\n")
		if len(lines) != 5 || lines[0] != "id: 42" ||
			lines[1] != "event: item.delta" || !strings.HasPrefix(lines[2], "data: {") ||
			lines[3] != "" || lines[4] != "" {
			t.Fatalf("unexpected SSE frame lines=%q", lines)
		}
		var wire map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[2], "data: ")), &wire); err != nil {
			t.Fatal(err)
		}
		if wire["type"] != event.Type || int64(wire["sequence"].(float64)) != event.Sequence {
			t.Fatalf("wire envelope=%+v", wire)
		}
		data := wire["data"].(map[string]any)
		delta := data["delta"].(map[string]any)
		if delta["text"] != "正在处理\n下一行" {
			t.Fatalf("newline/UTF-8 changed: %+v", delta)
		}
	})

	t.Run("pretty payload is compacted and mismatch is rejected", func(t *testing.T) {
		encoder := sse.NewEncoder()
		event := persistedSSEEvent(t, "ok")
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, event.Payload, "", "  "); err != nil {
			t.Fatal(err)
		}
		event.Payload = pretty.Bytes()
		var output bytes.Buffer
		if err := encoder.Encode(&output, event); err != nil {
			t.Fatal(err)
		}
		if strings.Count(output.String(), "\n") != 4 {
			t.Fatalf("pretty JSON was not encoded as one data line: %q", output.String())
		}
		for name, mutate := range map[string]func(*protocolevent.ProtocolEvent){
			"zero sequence":   func(value *protocolevent.ProtocolEvent) { value.Sequence = 0 },
			"event mismatch":  func(value *protocolevent.ProtocolEvent) { value.Type = protocolevent.EventItemCompleted },
			"stream mismatch": func(value *protocolevent.ProtocolEvent) { value.StreamID = "run:" + sseItemID },
			"missing payload": func(value *protocolevent.ProtocolEvent) { value.Payload = nil },
		} {
			name, mutate := name, mutate
			t.Run(name, func(t *testing.T) {
				invalid := persistedSSEEvent(t, "ok")
				mutate(&invalid)
				if err := encoder.Encode(io.Discard, invalid); !errors.Is(err, sse.ErrEncoderInvalid) {
					t.Fatalf("invalid event error=%v", err)
				}
			})
		}
	})

	t.Run("additive event is forwarded", func(t *testing.T) {
		encoder := sse.NewEncoder()
		event := persistedSSEEvent(t, "ok")
		event.Type = "future.annotation"
		var envelope map[string]any
		if err := json.Unmarshal(event.Payload, &envelope); err != nil {
			t.Fatal(err)
		}
		envelope["type"] = event.Type
		envelope["data"] = map[string]any{"annotation": map[string]any{"kind": "additive"}}
		payload, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		event.Payload = payload
		event.Data = json.RawMessage(`{"annotation":{"kind":"additive"}}`)
		var output bytes.Buffer
		if err := encoder.Encode(&output, event); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "event: future.annotation\n") {
			t.Fatalf("additive event was not forwarded: %q", output.String())
		}
	})

	t.Run("transport error has no cursor", func(t *testing.T) {
		encoder := sse.NewEncoder()
		signal := sse.NewStreamErrorSignal(
			"STREAM_TIMEOUT", "上游连接中断\n请重连", true,
			"request-1", "trace-1", []map[string]any{{"retryAfterMs": 1000}},
			time.Date(2026, 7, 20, 8, 9, 10, 123456000, time.FixedZone("UTC+8", 8*60*60)),
		)
		var output bytes.Buffer
		if err := encoder.EncodeStreamError(&output, signal); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(output.String(), "\n")
		if len(lines) != 4 || lines[0] != "event: stream.error" ||
			!strings.HasPrefix(lines[1], "data: {") || strings.HasPrefix(output.String(), "id:") {
			t.Fatalf("unexpected stream error frame=%q", output.String())
		}
		payload := strings.TrimPrefix(lines[1], "data: ")
		for _, forbidden := range []string{`"eventId"`, `"sequence"`, `"streamId"`} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("stream error gained cursor field %s: %s", forbidden, payload)
			}
		}
		if !strings.Contains(payload, `"occurredAt":"2026-07-20T00:09:10.123456Z"`) {
			t.Fatalf("stream error time was not UTC: %s", payload)
		}
	})

	t.Run("unsafe stream error is rejected", func(t *testing.T) {
		encoder := sse.NewEncoder()
		cases := []sse.StreamErrorSignal{
			sse.NewStreamErrorSignal("bad-code", "message", false, "request", "trace", nil, time.Now()),
			sse.NewStreamErrorSignal("STREAM_FAILED", "Bearer abcdefghijklmnop", false, "request", "trace", nil, time.Now()),
			sse.NewStreamErrorSignal("STREAM_FAILED", "message", false, "request", "trace",
				[]map[string]any{{"authorization": "Basic abcdefghijklmnop"}}, time.Now()),
			sse.NewStreamErrorSignal("STREAM_FAILED", "message", false, "request", "trace", nil, time.Time{}),
		}
		for index, signal := range cases {
			if err := encoder.EncodeStreamError(io.Discard, signal); !errors.Is(err, sse.ErrEncoderInvalid) {
				t.Fatalf("unsafe signal[%d] error=%v", index, err)
			}
		}
	})

	t.Run("heartbeat is a comment", func(t *testing.T) {
		encoder := sse.NewEncoder()
		var output bytes.Buffer
		at := time.Date(2026, 7, 20, 4, 5, 6, 7000, time.UTC)
		if err := encoder.Heartbeat(&output, at); err != nil {
			t.Fatal(err)
		}
		if output.String() != ": ping 2026-07-20T04:05:06.000007Z\n\n" ||
			strings.Contains(output.String(), "id:") || strings.Contains(output.String(), "data:") {
			t.Fatalf("heartbeat=%q", output.String())
		}
		if err := encoder.Heartbeat(io.Discard, time.Time{}); !errors.Is(err, sse.ErrEncoderInvalid) {
			t.Fatalf("zero heartbeat error=%v", err)
		}
	})

	t.Run("response headers disable buffering and transformation", func(t *testing.T) {
		header := make(http.Header)
		header.Set("Content-Encoding", "gzip")
		if err := sse.ApplyHeaders(header, "request-42", "trace-42"); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			"Content-Type": sse.ContentType, "Cache-Control": "no-cache, no-transform",
			"Connection": "keep-alive", "X-Accel-Buffering": "no",
			"X-Request-ID": "request-42", "X-Trace-ID": "trace-42",
		}
		for key, value := range want {
			if header.Get(key) != value {
				t.Fatalf("header %s=%q want=%q", key, header.Get(key), value)
			}
		}
		if header.Get("Content-Encoding") != "" {
			t.Fatalf("gzip remained enabled: %v", header)
		}
		if err := sse.ApplyHeaders(header, "bad\r\nid", "trace"); !errors.Is(err, sse.ErrEncoderInvalid) {
			t.Fatalf("unsafe header error=%v", err)
		}
	})

	t.Run("writer errors are preserved", func(t *testing.T) {
		encoder := sse.NewEncoder()
		failure := errors.New("write failed")
		writer := errorWriter{err: failure}
		if err := encoder.Encode(writer, persistedSSEEvent(t, "ok")); !errors.Is(err, failure) {
			t.Fatalf("event writer error=%v", err)
		}
		if err := encoder.EncodeStreamError(writer, sse.NewStreamErrorSignal(
			"STREAM_FAILED", "failed", false, "request", "trace", nil, time.Now(),
		)); !errors.Is(err, failure) {
			t.Fatalf("signal writer error=%v", err)
		}
		if err := encoder.Heartbeat(writer, time.Now()); !errors.Is(err, failure) {
			t.Fatalf("heartbeat writer error=%v", err)
		}
	})
}

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }

func persistedSSEEvent(t testing.TB, text string) protocolevent.ProtocolEvent {
	t.Helper()
	occurredAt := time.Date(2026, 7, 20, 1, 2, 3, 456000000, time.UTC)
	data := protocolevent.ItemDeltaData{
		ItemID: sseItemID,
		Delta: protocolevent.TextDelta{
			Type: protocolevent.DeltaTypeText, Index: 0, Text: text,
		},
	}
	newEvent, err := protocolevent.BuildProtocolEvent(protocolevent.NewProtocolEvent{
		ID: sseEventID, EventStreamID: sseStreamID,
		WorkspaceID: sseWorkspaceID, AgentID: sseAgentID,
		ConversationID: sseConversationID, RunID: sseRunID,
		Type: protocolevent.EventItemDelta, SpecVersion: "1.0", TraceID: "trace-sse-42",
		ItemID: sseItemID, OccurredAt: occurredAt,
	}, data)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(struct {
		SpecVersion    string          `json:"specVersion"`
		Type           string          `json:"type"`
		EventID        string          `json:"eventId"`
		StreamID       string          `json:"streamId"`
		Sequence       int64           `json:"sequence"`
		OccurredAt     time.Time       `json:"occurredAt"`
		WorkspaceID    string          `json:"workspaceId"`
		AgentID        string          `json:"agentId"`
		ConversationID string          `json:"conversationId"`
		RunID          string          `json:"runId"`
		TraceID        string          `json:"traceId"`
		Data           json.RawMessage `json:"data"`
	}{
		SpecVersion: "1.0", Type: newEvent.Type, EventID: newEvent.ID,
		StreamID: "run:" + newEvent.RunID, Sequence: 42, OccurredAt: occurredAt,
		WorkspaceID: newEvent.WorkspaceID, AgentID: newEvent.AgentID,
		ConversationID: newEvent.ConversationID, RunID: newEvent.RunID,
		TraceID: newEvent.TraceID, Data: newEvent.Data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return protocolevent.ProtocolEvent{
		ID: newEvent.ID, EventStreamID: newEvent.EventStreamID, StreamID: "run:" + newEvent.RunID,
		WorkspaceID: newEvent.WorkspaceID, AgentID: newEvent.AgentID,
		ConversationID: newEvent.ConversationID, RunID: newEvent.RunID,
		Type: newEvent.Type, SpecVersion: newEvent.SpecVersion, TraceID: newEvent.TraceID,
		ItemID: newEvent.ItemID, Sequence: 42, OccurredAt: occurredAt,
		Data: newEvent.Data, Payload: payload,
	}
}
