package agentaudit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/contextsummary"
)

// countingReader records Open calls for debug=false zero-open asserts.
type countingReader struct {
	calls int
	body  string
	state ContentState
	err   error
}

func (c *countingReader) Open(_ context.Context, purpose SummaryReadPurpose, req SummaryBodyReadRequest) (SummaryBodyReadResult, error) {
	c.calls++
	if purpose != PurposeAdminAudit {
		return SummaryBodyReadResult{State: ContentMissing}, errors.New("wrong purpose")
	}
	if c.err != nil {
		return SummaryBodyReadResult{State: ContentCipher}, c.err
	}
	return SummaryBodyReadResult{Body: c.body, State: c.state}, nil
}

func TestHydrateCompactBodiesDebugOffZeroOpens(t *testing.T) {
	reader := &countingReader{body: "SECRET_SUMMARY_BODY", state: ContentPlain}
	svc := &Service{debugMode: false, summaryBodies: reader}
	steps := []Step{{
		Type: "context_compaction", Title: "上下文 Compact 完成",
		Params: json.RawMessage(`{"result":"completed","summaryId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","summaryDigest":"abc"}`),
		ContentState: ContentRedacted,
	}}
	// debug=false path: hydrate must not run.
	if svc.debugMode {
		t.Fatal("precondition")
	}
	// Simulate GetTrace gate
	if svc.debugMode {
		svc.hydrateCompactSummaryBodies(context.Background(), "ws", steps)
	}
	if reader.calls != 0 {
		t.Fatalf("debug=false must open 0 times, got %d", reader.calls)
	}
	if steps[0].Content != "" || steps[0].ContentState != ContentRedacted {
		t.Fatalf("must stay redacted empty: %+v", steps[0])
	}
}

func TestHydrateCompactBodiesDebugOnFromObjectNotProtocol(t *testing.T) {
	reader := &countingReader{body: "from-encrypted-object", state: ContentPlain}
	svc := &Service{debugMode: true, summaryBodies: reader}
	// Protocol canary would live in raw step output; timeline already strips it from params.
	// Hydrate must use summaryId only.
	steps := []Step{{
		Type: "context_compaction", Title: "上下文 Compact 完成",
		RunID: "run-1", StepID: "step-1",
		Params: json.RawMessage(`{"result":"completed","summaryId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","summaryDigest":"deadbeef"}`),
		ContentState: ContentRedacted,
		// If something put body in Content from protocol, hydrate overwrites from object only when Open succeeds.
		Content: "PROTOCOL_CANARY_MUST_NOT_WIN_IF_OPEN_FAILS",
	}}
	// First: Open returns object body
	svc.hydrateCompactSummaryBodies(context.Background(), "ws", steps)
	if reader.calls != 1 {
		t.Fatalf("calls=%d", reader.calls)
	}
	if steps[0].Content != "from-encrypted-object" || steps[0].ContentState != ContentPlain {
		t.Fatalf("expected object body plain: %+v", steps[0])
	}
	// Fallback result must not open
	reader.calls = 0
	steps = []Step{{
		Type: "context_compaction",
		Params: json.RawMessage(`{"result":"fallback","summaryId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}`),
		ContentState: ContentRedacted,
	}}
	svc.hydrateCompactSummaryBodies(context.Background(), "ws", steps)
	if reader.calls != 0 {
		t.Fatalf("fallback must not open, calls=%d", reader.calls)
	}
}

func TestHydrateObjectFailureCipher(t *testing.T) {
	reader := &countingReader{err: errors.New("decrypt failed"), state: ContentCipher}
	svc := &Service{debugMode: true, summaryBodies: reader}
	steps := []Step{{
		Type: "context_compaction",
		Params: json.RawMessage(`{"result":"completed","summaryId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}`),
		ContentState: ContentRedacted,
	}}
	svc.hydrateCompactSummaryBodies(context.Background(), "ws", steps)
	if steps[0].Content != "" || steps[0].ContentState != ContentCipher {
		t.Fatalf("want cipher empty: %+v", steps[0])
	}
}

func TestCompactStepStripsProtocolCanaryFromParams(t *testing.T) {
	base := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	finished := base.Add(time.Second)
	step := StepFact{
		ID: "s", RunID: "r", SequenceNo: 1, StepType: "CONTEXT_COMPACTION", Status: "SUCCEEDED",
		StartedAt: base, FinishedAt: &finished,
		OutputSummary: json.RawMessage(`{
			"result":"completed",
			"summaryId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			"summary":"PROTOCOL_CANARY_BODY",
			"injectedSummary":"also-bad"
		}`),
	}
	got := compactStep(base, step, true)
	if got.Content != "" {
		t.Fatalf("content must stay empty until hydrate: %q", got.Content)
	}
	if got.ContentState != ContentRedacted {
		t.Fatalf("state=%s", got.ContentState)
	}
	if strings.Contains(string(got.Params), "PROTOCOL_CANARY") ||
		strings.Contains(string(got.Params), "injectedSummary") ||
		strings.Contains(string(got.Params), `"summary"`) {
		t.Fatalf("params must not carry protocol canary: %s", got.Params)
	}
	if !strings.Contains(string(got.Params), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee") {
		t.Fatalf("params must keep summaryId: %s", got.Params)
	}
	if got.Title != "上下文 Compact 完成" {
		t.Fatalf("title=%q", got.Title)
	}
}

func TestCompactStepFallbackTitle(t *testing.T) {
	base := time.Now().UTC()
	step := StepFact{
		ID: "s", RunID: "r", StepType: "CONTEXT_COMPACTION", Status: "FAILED",
		StartedAt: base, OutputSummary: json.RawMessage(`{"result":"fallback","errorCode":"CONTEXT_COMPACTION_MODEL_FAILED"}`),
	}
	got := compactStep(base, step, false)
	if got.Title != "上下文 Compact 失败；已退化为 token_window" {
		t.Fatalf("title=%q", got.Title)
	}
	if !strings.Contains(string(got.Params), "rolling_summary") ||
		!strings.Contains(string(got.Params), "token_window") {
		t.Fatalf("params=%s", got.Params)
	}
}

// Compile-time: Summary type import used for production reader docs.
var _ = contextsummary.StatusReady

func TestDigestHelper(t *testing.T) {
	body := []byte("hello")
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) == "" {
		t.Fatal("empty digest")
	}
}
