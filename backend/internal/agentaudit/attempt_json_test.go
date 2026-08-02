package agentaudit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestStepAttemptRetryZeroSerializedForDelegation: pre-dispatch failure (0/0)
// must appear in API JSON for real AGENT_DELEGATION steps.
func TestStepAttemptRetryZeroSerializedForDelegation(t *testing.T) {
	t.Parallel()
	zero := 0
	step := Step{
		Type:         "agent_delegation",
		Title:        "Agent 调用: call_b",
		Depth:        1,
		AttemptCount: &zero,
		RetryCount:   &zero,
	}
	raw, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"attemptCount":0`) {
		t.Fatalf("attemptCount:0 missing: %s", s)
	}
	if !strings.Contains(s, `"retryCount":0`) {
		t.Fatalf("retryCount:0 missing: %s", s)
	}
}

// TestStepAttemptRetryOmittedForNonDelegation: tool/model steps must not
// surface fake attempt/retry zeros.
func TestStepAttemptRetryOmittedForNonDelegation(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{"tool", "model", "reasoning", "output"} {
		step := Step{Type: typ, Title: typ, TimeOffsetMs: 1}
		raw, err := json.Marshal(step)
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		if strings.Contains(s, "attemptCount") || strings.Contains(s, "retryCount") {
			t.Fatalf("%s must omit attempt/retry, got %s", typ, s)
		}
	}
}

// TestBuildTimeline_DelegationZeroAttemptsInJSON builds a real AGENT_DELEGATION
// frame with 0 attempts and asserts JSON still carries attemptCount/retryCount.
func TestBuildTimeline_DelegationZeroAttemptsInJSON(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fin := base.Add(time.Second)
	detail := BuildTimeline(
		[]RunFact{{
			ID: "run-1", TraceID: "t1", Status: "FAILED", StartedAt: base, FinishedAt: &fin,
			TriggeredByType: "USER", TriggeredByID: "u1",
			ModelSnapshot: json.RawMessage(`{"modelName":"m"}`),
		}},
		nil,
		[]StepFact{{
			ID: "step-del", RunID: "run-1", SequenceNo: 1,
			StepType: "AGENT_DELEGATION", Status: "FAILED",
			StartedAt: base, FinishedAt: &fin, DelegationStatus: "FAILED",
			CallerAgentID: "a", TargetAgentID: "b", Mode: "INLINE",
			Protocol: "INTERNAL", Origin: "INTERNAL", Depth: 1,
			AttemptCount: 0, RetryCount: 0,
			DelegationErrorCode: "DELEGATION_FAILED",
			InputSummary:        json.RawMessage(`{"callableName":"call_b"}`),
		}},
		true,
	)
	var del *Step
	for i := range detail.Steps {
		if detail.Steps[i].Type == "agent_delegation" {
			del = &detail.Steps[i]
			break
		}
	}
	if del == nil {
		t.Fatalf("missing agent_delegation: %+v", detail.Steps)
	}
	if del.AttemptCount == nil || *del.AttemptCount != 0 {
		t.Fatalf("AttemptCount=%v", del.AttemptCount)
	}
	if del.RetryCount == nil || *del.RetryCount != 0 {
		t.Fatalf("RetryCount=%v", del.RetryCount)
	}
	raw, err := json.Marshal(del)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"attemptCount":0`) || !strings.Contains(string(raw), `"retryCount":0`) {
		t.Fatalf("JSON missing zero attempts: %s", raw)
	}

	// Non-delegation sibling in same timeline JSON envelope omits fields.
	tool := Step{Type: "tool", Title: "t", TimeOffsetMs: 2}
	env, _ := json.Marshal([]Step{*del, tool})
	var back []Step
	if err := json.Unmarshal(env, &back); err != nil {
		t.Fatal(err)
	}
	if len(back) != 2 {
		t.Fatalf("len=%d", len(back))
	}
	if back[1].AttemptCount != nil || back[1].RetryCount != nil {
		t.Fatalf("tool step must not have attempt/retry: %+v", back[1])
	}
}
