package httptransport

import (
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
)

func TestProjectExecutionProtocolEventsShape(t *testing.T) {
	finished := time.Now().UTC()
	value := execution.WorkflowExecution{
		ID: "11111111-1111-4111-8111-111111111111", WorkspaceID: "ws",
		WorkflowID:  "22222222-2222-4222-8222-222222222222",
		RevisionID:  "33333333-3333-4333-8333-333333333333",
		TriggerType: "CONSOLE", TriggeredByType: "USER", TriggeredByID: "user",
		TraceID: "trace-e1", Status: "SUCCEEDED", StartedAt: finished.Add(-time.Second),
		FinishedAt: &finished,
	}
	steps := []execution.ExecutionStep{
		{
			ID: "44444444-4444-4444-8444-444444444444", ExecutionID: value.ID,
			NodeID: "start", NodeType: "Start", SequenceNo: 1, Status: "SUCCEEDED",
			StartedAt: finished.Add(-time.Second), FinishedAt: &finished,
		},
	}
	frames := projectExecutionProtocolEvents(value, steps)
	joined := strings.Join(frames, "")
	for _, want := range []string{
		"event: " + protocolevent.EventRunAccepted,
		"event: " + protocolevent.EventRunStarted,
		"event: " + protocolevent.EventItemStarted,
		"event: " + protocolevent.EventItemCompleted,
		"event: " + protocolevent.EventRunCompleted,
		`"workflowId":"` + value.WorkflowID + `"`,
		`"revisionId":"` + value.RevisionID + `"`,
		`"traceId":"trace-e1"`,
		`"status":"completed"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in SSE projection:\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, "id: 1\n") || !strings.Contains(joined, "id: 2\n") {
		t.Fatalf("expected sequenced SSE ids:\n%s", joined)
	}
}

func TestProjectExecutionProtocolEventsWaiting(t *testing.T) {
	value := execution.WorkflowExecution{
		ID: "11111111-1111-4111-8111-111111111111", WorkspaceID: "ws",
		WorkflowID:  "22222222-2222-4222-8222-222222222222",
		RevisionID:  "33333333-3333-4333-8333-333333333333",
		TriggerType: "API", Status: "WAITING_CONFIRMATION",
		TraceID: "trace-wait", StartedAt: time.Now().UTC(),
	}
	joined := strings.Join(projectExecutionProtocolEvents(value, nil), "")
	if !strings.Contains(joined, "event: "+protocolevent.EventRunWaiting) {
		t.Fatalf("expected run.waiting:\n%s", joined)
	}
}
