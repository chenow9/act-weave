package httptransport

import (
	"encoding/json"
	"testing"
	"time"

	"actweave/backend/internal/tool"
)

func TestLatestTestDTOFor_NullAndSummary(t *testing.T) {
	t.Parallel()
	if latestTestDTOFor(nil) != nil {
		t.Fatal("nil summary must map to null latestTest")
	}
	code := "TOOL_TEST_FAILED"
	dto := latestTestDTOFor(&tool.LatestTestSummary{
		Status: "FAILED", TestedAt: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		TestedBy: "user-1", ErrorCode: &code,
	})
	if dto == nil || dto.Status != "FAILED" || dto.ErrorCode == nil || *dto.ErrorCode != code {
		t.Fatalf("dto=%+v", dto)
	}
	// Ensure DTO marshals only safe fields.
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"requestSummary", "responseSummary", "rawObjectId", "request", "response"} {
		if _, ok := body[banned]; ok {
			t.Fatalf("banned field %s present: %s", banned, raw)
		}
	}
}

func TestToolDTO_LatestTestNullByDefault(t *testing.T) {
	t.Parallel()
	dto := toolDTOFor(tool.Tool{CapabilityID: "c1", Name: "n", Status: "PUBLISHED"})
	raw, _ := json.Marshal(dto)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	// json null for pointer nil
	if body["latestTest"] != nil {
		t.Fatalf("latestTest should be null without summary, got %v", body["latestTest"])
	}
}
