package smartdag

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestFailureFeedbackJSONRoundTrip(t *testing.T) {
	t.Parallel()
	original := FailureFeedback{
		Source:        FailureSourceCompile,
		WorkflowID:    testWorkflowID,
		CompilationID: "118f1f2e-7b5a-7c3d-8e9f-1234567890c1",
		Issues: []FailureIssue{
			{
				Code:            "MAPPING_INVALID",
				NodeID:          "tool-1",
				Message:         "input mapping missing orderId",
				SuggestedAction: SuggestedActionEditMapping,
			},
		},
		MissingCapabilities: []MissingCapability{
			{ID: "cap-1", Name: "退款 API", Reason: "catalog 无匹配", SuggestedProtocol: "openapi"},
		},
		RawSummary: "compile failed on tool mapping",
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseFailureFeedback(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed == nil {
		t.Fatal("expected non-nil feedback")
	}
	if parsed.Source != FailureSourceCompile || parsed.WorkflowID != testWorkflowID {
		t.Fatalf("parsed identity: %+v", parsed)
	}
	if len(parsed.Issues) != 1 || parsed.Issues[0].Code != "MAPPING_INVALID" {
		t.Fatalf("issues: %+v", parsed.Issues)
	}
	if parsed.CompilationID != original.CompilationID {
		t.Fatalf("compilationId: %s", parsed.CompilationID)
	}
	// Re-marshal preserves issues as array (not null).
	again, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(again), `"issues"`) {
		t.Fatalf("expected issues key: %s", again)
	}
}

func TestParseFailureFeedbackEmptyIsNil(t *testing.T) {
	t.Parallel()
	for _, raw := range []json.RawMessage{nil, json.RawMessage(""), json.RawMessage("null"), json.RawMessage("  ")} {
		fb, err := ParseFailureFeedback(raw)
		if err != nil || fb != nil {
			t.Fatalf("raw=%q want nil,nil got %+v %v", raw, fb, err)
		}
	}
}

func TestParseFailureFeedbackRejectsInvalid(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{`,
		`{"source":"compile"}`, // missing workflowId
		`{"source":"unknown","workflowId":"118f1f2e-7b5a-7c3d-8e9f-123456789012"}`,
		`{"source":"compile","workflowId":"not-a-uuid"}`,
		`{"source":"trial","workflowId":"118f1f2e-7b5a-7c3d-8e9f-123456789012","issues":[{"code":"X","message":"y","suggestedAction":"hack_system"}]}`,
	}
	for _, raw := range cases {
		_, err := ParseFailureFeedback(json.RawMessage(raw))
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("raw=%s want ErrInvalid, got %v", raw, err)
		}
	}
}

func TestFormatRevisionMessageIncludesIssues(t *testing.T) {
	t.Parallel()
	msg := FormatRevisionMessage(&FailureFeedback{
		Source:     FailureSourceTrial,
		WorkflowID: testWorkflowID,
		Issues: []FailureIssue{
			{Code: "TIMEOUT", Message: "tool step timed out", NodeID: "tool-1"},
		},
		RawSummary: "trial failed",
	})
	if !strings.Contains(msg, "试运行") || !strings.Contains(msg, "TIMEOUT") || !strings.Contains(msg, "trial failed") {
		t.Fatalf("message missing expected content: %s", msg)
	}
	if !strings.Contains(msg, "不要发布") {
		t.Fatalf("message should remind no publish: %s", msg)
	}
}

func TestRevisedFromUI(t *testing.T) {
	t.Parallel()
	ui := RevisedFromUI(&FailureFeedback{
		Source:        FailureSourceCompile,
		WorkflowID:    testWorkflowID,
		CompilationID: "118f1f2e-7b5a-7c3d-8e9f-1234567890c1",
		Issues:        []FailureIssue{{Code: "TOOL_NOT_FOUND", Message: "missing"}},
	})
	if ui["source"] != FailureSourceCompile || ui["workflowId"] != testWorkflowID {
		t.Fatalf("ui: %+v", ui)
	}
	if ui["compilationId"] == nil {
		t.Fatal("expected compilationId")
	}
	codes, ok := ui["issueCodes"].([]string)
	if !ok || len(codes) != 1 || codes[0] != "TOOL_NOT_FOUND" {
		t.Fatalf("issueCodes: %+v", ui["issueCodes"])
	}
}
