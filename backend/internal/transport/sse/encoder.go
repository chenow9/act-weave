package sse

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/protocolschema"

	"github.com/google/uuid"
)

const ContentType = "text/event-stream; charset=utf-8"

var (
	ErrEncoderInvalid = errors.New("AAP SSE encoder input is invalid")
	stableSignalCode  = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	protocolEventType = regexp.MustCompile(`^[a-z][a-z_]*\.[a-z][a-z_]*$`)
)

type Encoder struct {
	validator *protocolevent.PayloadValidator
}

func NewEncoder() *Encoder {
	return &Encoder{validator: protocolevent.MustDefaultPayloadValidator()}
}

func (encoder *Encoder) ContentType() string { return ContentType }

// Encode writes one persisted Protocol Event. The event's numeric Run cursor
// is the SSE id and the public event type is repeated verbatim in event/data.
func (encoder *Encoder) Encode(writer io.Writer, event protocolevent.ProtocolEvent) error {
	if encoder == nil || encoder.validator == nil || writer == nil ||
		validateProtocolEvent(event, encoder.validator) != nil {
		return ErrEncoderInvalid
	}
	payload, err := compactJSONObject(event.Payload)
	if err != nil {
		return ErrEncoderInvalid
	}
	frame := fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, payload)
	_, err = io.WriteString(writer, frame)
	return err
}

type StreamError struct {
	Code      string           `json:"code"`
	Message   string           `json:"message"`
	Retryable bool             `json:"retryable"`
	RequestID string           `json:"requestId,omitempty"`
	TraceID   string           `json:"traceId,omitempty"`
	Details   []map[string]any `json:"details,omitempty"`
}

type StreamErrorSignal struct {
	SpecVersion string      `json:"specVersion"`
	Type        string      `json:"type"`
	OccurredAt  time.Time   `json:"occurredAt"`
	Error       StreamError `json:"error"`
}

func NewStreamErrorSignal(
	code, message string,
	retryable bool,
	requestID, traceID string,
	details []map[string]any,
	occurredAt time.Time,
) StreamErrorSignal {
	clonedDetails := make([]map[string]any, 0, len(details))
	for _, detail := range details {
		cloned := make(map[string]any, len(detail))
		for key, value := range detail {
			cloned[key] = value
		}
		clonedDetails = append(clonedDetails, cloned)
	}
	return StreamErrorSignal{
		SpecVersion: "1.0", Type: "stream.error", OccurredAt: occurredAt.UTC(),
		Error: StreamError{
			Code: strings.TrimSpace(code), Message: strings.TrimSpace(message),
			Retryable: retryable, RequestID: strings.TrimSpace(requestID),
			TraceID: strings.TrimSpace(traceID), Details: clonedDetails,
		},
	}
}

// EncodeStreamError writes a transport-only signal. It intentionally omits an
// SSE id, so consumers cannot advance the persisted Run cursor from it.
func (encoder *Encoder) EncodeStreamError(writer io.Writer, signal StreamErrorSignal) error {
	if encoder == nil || encoder.validator == nil || writer == nil {
		return ErrEncoderInvalid
	}
	raw, err := json.Marshal(signal)
	if err != nil || validateStreamError(signal, raw, encoder.validator) != nil {
		return ErrEncoderInvalid
	}
	payload, err := compactJSONObject(raw)
	if err != nil {
		return ErrEncoderInvalid
	}
	_, err = fmt.Fprintf(writer, "event: stream.error\ndata: %s\n\n", payload)
	return err
}

func (encoder *Encoder) Heartbeat(writer io.Writer, occurredAt time.Time) error {
	if encoder == nil || writer == nil || occurredAt.IsZero() {
		return ErrEncoderInvalid
	}
	_, err := fmt.Fprintf(writer, ": ping %s\n\n", occurredAt.UTC().Format(time.RFC3339Nano))
	return err
}

// ApplyHeaders applies the anti-buffering AAP SSE response contract. Request
// and Trace IDs are mandatory correlation values and may not contain controls.
func ApplyHeaders(header http.Header, requestID, traceID string) error {
	requestID, traceID = strings.TrimSpace(requestID), strings.TrimSpace(traceID)
	if header == nil || !validHeaderValue(requestID, 128) || !validHeaderValue(traceID, 128) {
		return ErrEncoderInvalid
	}
	header.Set("Content-Type", ContentType)
	header.Set("Cache-Control", "no-cache, no-transform")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	header.Set("X-Request-ID", requestID)
	header.Set("X-Trace-ID", traceID)
	header.Del("Content-Encoding")
	return nil
}

type eventEnvelope struct {
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
}

func validateProtocolEvent(
	event protocolevent.ProtocolEvent,
	validator *protocolevent.PayloadValidator,
) error {
	if event.Sequence < 1 || !protocolEventType.MatchString(event.Type) ||
		event.SpecVersion != "1.0" || event.StreamID != "run:"+event.RunID ||
		event.OccurredAt.IsZero() || strings.TrimSpace(event.TraceID) == "" ||
		!validUUID(event.ID) || !validUUID(event.EventStreamID) ||
		!validUUID(event.WorkspaceID) || !validUUID(event.AgentID) ||
		!validUUID(event.ConversationID) || !validUUID(event.RunID) ||
		len(event.Payload) == 0 || validator.ValidateEnvelopeSize(event.Payload) != nil ||
		protocolschema.ValidateDocument("event-envelope.schema.json", event.Payload) != nil {
		return ErrEncoderInvalid
	}
	var wire eventEnvelope
	if err := json.Unmarshal(event.Payload, &wire); err != nil ||
		wire.SpecVersion != event.SpecVersion || wire.Type != event.Type || wire.EventID != event.ID ||
		wire.StreamID != event.StreamID || wire.Sequence != event.Sequence ||
		!sameEventTime(wire.OccurredAt, event.OccurredAt) || wire.WorkspaceID != event.WorkspaceID ||
		wire.AgentID != event.AgentID || wire.ConversationID != event.ConversationID ||
		wire.RunID != event.RunID || wire.TraceID != event.TraceID ||
		validator.ValidateEventData(wire.Type, wire.Data) != nil {
		return ErrEncoderInvalid
	}
	return nil
}

func validateStreamError(
	signal StreamErrorSignal,
	raw json.RawMessage,
	validator *protocolevent.PayloadValidator,
) error {
	if signal.SpecVersion != "1.0" || signal.Type != "stream.error" || signal.OccurredAt.IsZero() ||
		!stableSignalCode.MatchString(signal.Error.Code) || signal.Error.Message == "" ||
		utf8.RuneCountInString(signal.Error.Message) > 2048 ||
		(signal.Error.RequestID != "" && !validHeaderValue(signal.Error.RequestID, 128)) ||
		(signal.Error.TraceID != "" && !validHeaderValue(signal.Error.TraceID, 128)) ||
		len(signal.Error.Details) > 100 ||
		protocolschema.ValidateDocument("transport-signal.schema.json", raw) != nil ||
		validator.ValidateEventData("stream.error", raw) != nil {
		return ErrEncoderInvalid
	}
	return nil
}

func compactJSONObject(raw json.RawMessage) (string, error) {
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil || value == nil {
		return "", ErrEncoderInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", ErrEncoderInvalid
	}
	compacted, err := json.Marshal(value)
	if err != nil || bytes.ContainsAny(compacted, "\n\r") {
		return "", ErrEncoderInvalid
	}
	return string(compacted), nil
}

func validHeaderValue(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validUUID(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}

// Postgres timestamptz is microsecond; some writers keep nanoseconds in JSON.
func sameEventTime(left, right time.Time) bool {
	delta := left.Sub(right)
	if delta < 0 {
		delta = -delta
	}
	return delta < time.Millisecond
}
