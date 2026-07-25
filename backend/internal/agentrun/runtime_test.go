package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// stubRuntime is a test double that satisfies Runtime.
type stubRuntime struct {
	mu        sync.Mutex
	enqueued  []Job
	continued []continuedCall
	cancelled [][2]string
	cancelErr error
}

type continuedCall struct {
	job             Job
	requestSnapshot json.RawMessage
	toolResult      json.RawMessage
	life            ContinueLifecycle
}

func (s *stubRuntime) Enqueue(job Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueued = append(s.enqueued, job)
}

func (s *stubRuntime) CancelRun(workspaceID, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelled = append(s.cancelled, [2]string{workspaceID, runID})
	return s.cancelErr
}

func (s *stubRuntime) EnqueueContinueWithLifecycle(
	job Job,
	requestSnapshot, toolResult json.RawMessage,
	life ContinueLifecycle,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.continued = append(s.continued, continuedCall{
		job: job, requestSnapshot: requestSnapshot, toolResult: toolResult, life: life,
	})
}

type stubLifecycle struct {
	renewed, completed int
}

func (l *stubLifecycle) Renew(context.Context) error {
	l.renewed++
	return nil
}

func (l *stubLifecycle) Complete(context.Context) error {
	l.completed++
	return nil
}

// Compile-time interface satisfaction for the package-local stub.
var _ Runtime = (*stubRuntime)(nil)
var _ ContinueLifecycle = (*stubLifecycle)(nil)

func TestRuntimeInterfaceContract(t *testing.T) {
	rt := &stubRuntime{}
	var facade Runtime = rt

	job := Job{
		WorkspaceID: "ws-1", SessionID: "sess-1", RunID: "run-1",
		UserMessageID: "msg-1", ActorID: "actor-1",
	}
	facade.Enqueue(job)
	if len(rt.enqueued) != 1 || rt.enqueued[0].RunID != "run-1" {
		t.Fatalf("Enqueue not recorded: %+v", rt.enqueued)
	}

	life := &stubLifecycle{}
	snap := json.RawMessage(`{"schema":"tool-resume-request.v1"}`)
	result := json.RawMessage(`{"ok":true}`)
	facade.EnqueueContinueWithLifecycle(job, snap, result, life)
	if len(rt.continued) != 1 {
		t.Fatalf("EnqueueContinueWithLifecycle not recorded: %+v", rt.continued)
	}
	if string(rt.continued[0].requestSnapshot) != string(snap) {
		t.Fatalf("request snapshot mismatch: %s", rt.continued[0].requestSnapshot)
	}
	if rt.continued[0].life == nil {
		t.Fatal("lifecycle must be passed through")
	}
	if err := rt.continued[0].life.Renew(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.continued[0].life.Complete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if life.renewed != 1 || life.completed != 1 {
		t.Fatalf("lifecycle hooks not invoked: %+v", life)
	}

	if err := facade.CancelRun("ws-1", "run-1"); err != nil {
		t.Fatal(err)
	}
	if len(rt.cancelled) != 1 || rt.cancelled[0] != [2]string{"ws-1", "run-1"} {
		t.Fatalf("CancelRun not recorded: %+v", rt.cancelled)
	}

	rt.cancelErr = errors.New("cancel failed")
	if err := facade.CancelRun("ws-1", "run-2"); err == nil {
		t.Fatal("expected cancel error")
	}
}

func TestJobZeroValueIsSafe(t *testing.T) {
	// Document that zero Job is a no-op for callers that filter empty fields
	// (legacy Executor returns early). Interface must accept zero Job without panic.
	rt := &stubRuntime{}
	rt.Enqueue(Job{})
	rt.EnqueueContinueWithLifecycle(Job{}, nil, nil, nil)
	if len(rt.enqueued) != 1 || len(rt.continued) != 1 {
		t.Fatal("zero Job must still reach implementation")
	}
}
