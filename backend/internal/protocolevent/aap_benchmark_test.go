package protocolevent_test

import (
	"encoding/json"
	"testing"
	"time"

	"actweave/backend/internal/protocolevent"
)

// BenchmarkAAPProtocolEventBuild validates the append-path CPU cost of building
// a protocol event envelope + schema data without DB I/O.
// Matches -bench 'AAP|ProtocolEvent'.
func BenchmarkAAPProtocolEventBuild(b *testing.B) {
	itemID := "a38f1f2e-7b5a-7c3d-8e9f-1234567890b1"
	input := protocolevent.NewProtocolEvent{
		ID:             "a38f1f2e-7b5a-7c3d-8e9f-1234567890b2",
		EventStreamID:  "a38f1f2e-7b5a-7c3d-8e9f-1234567890b3",
		WorkspaceID:    "a38f1f2e-7b5a-7c3d-8e9f-1234567890b4",
		AgentID:        "a38f1f2e-7b5a-7c3d-8e9f-1234567890b5",
		ConversationID: "a38f1f2e-7b5a-7c3d-8e9f-1234567890b6",
		RunID:          "a38f1f2e-7b5a-7c3d-8e9f-1234567890b7",
		Type:           protocolevent.EventItemDelta,
		SpecVersion:    "1.0",
		TraceID:        "trace-bench-append",
		ItemID:         itemID,
		OccurredAt:     time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC),
	}
	data := protocolevent.ItemDeltaData{
		ItemID: itemID,
		Delta: protocolevent.TextDelta{
			Type: protocolevent.DeltaTypeText, Index: 0, Text: "bench",
		},
	}
	validator := protocolevent.MustDefaultPayloadValidator()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		built, err := protocolevent.BuildProtocolEvent(input, data)
		if err != nil {
			b.Fatal(err)
		}
		if err := validator.ValidateEventData(built.Type, built.Data); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAAPProtocolEventPayloadValidate isolates sensitive-scan + schema validation.
func BenchmarkAAPProtocolEventPayloadValidate(b *testing.B) {
	payload, err := json.Marshal(map[string]any{
		"itemId": "a38f1f2e-7b5a-7c3d-8e9f-1234567890b1",
		"delta": map[string]any{
			"type": "text", "index": 0, "text": "hello from capacity bench",
		},
	})
	if err != nil {
		b.Fatal(err)
	}
	validator := protocolevent.MustDefaultPayloadValidator()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validator.ValidateEventData(protocolevent.EventItemDelta, payload); err != nil {
			b.Fatal(err)
		}
	}
}
