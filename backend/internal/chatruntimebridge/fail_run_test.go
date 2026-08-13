package chatruntimebridge

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/execution"
)

// failRunResults records FAILED assistant messages and rejects second write.
type failRunResults struct {
	mu       sync.Mutex
	messages []chat.Message
	calls    int
	failOnce bool
}

func (r *failRunResults) RecordAssistantResult(
	_ context.Context, in chat.RecordAssistantResultInput,
) (chat.RecordAssistantResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.failOnce {
		return chat.RecordAssistantResult{}, errors.New("persist failed")
	}
	msg := chat.Message{
		ID: in.AssistantMessageID, WorkspaceID: in.WorkspaceID, SessionID: in.SessionID,
		Role: "ASSISTANT", Content: in.Content, Status: "COMPLETED", RunID: in.RunID,
		CreatedAt: time.Now().UTC(),
	}
	r.messages = append(r.messages, msg)
	return chat.RecordAssistantResult{Message: msg}, nil
}

type failRunStore struct {
	mu  sync.Mutex
	run execution.AgentRun
}

func (s *failRunStore) GetAgentRun(_ context.Context, workspaceID, runID string) (execution.AgentRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.run
	out.WorkspaceID = workspaceID
	out.ID = runID
	return out, nil
}

func (s *failRunStore) markFailed(errorCode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.run.Status = "FAILED"
	s.run.ErrorCode = errorCode
	s.run.LockVersion++
}

type failRunResultsWithStatus struct {
	results *failRunResults
	store   *failRunStore
}

func (r *failRunResultsWithStatus) RecordAssistantResult(
	ctx context.Context, in chat.RecordAssistantResultInput,
) (chat.RecordAssistantResult, error) {
	out, err := r.results.RecordAssistantResult(ctx, in)
	if err == nil && in.RunStatus == "FAILED" {
		r.store.markFailed(in.RunErrorCode)
	}
	if err == nil && in.RunStatus == "SUCCEEDED" {
		r.store.mu.Lock()
		r.store.run.Status = "SUCCEEDED"
		r.store.run.LockVersion++
		r.store.mu.Unlock()
	}
	return out, err
}

type failRunEvents struct {
	mu      sync.Mutex
	records []chatruntime.ProtocolRecord
	failN   int // fail first N Record calls
	calls   int
}

func (e *failRunEvents) Record(_ context.Context, record chatruntime.ProtocolRecord) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	if e.failN > 0 {
		e.failN--
		return errors.New("protocol append failed")
	}
	e.records = append(e.records, record)
	return nil
}

func (e *failRunEvents) kinds() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.records))
	for _, r := range e.records {
		out = append(out, r.Kind)
	}
	return out
}

func TestFailRun_ProjectsRunFailedAfterDurableCommit(t *testing.T) {
	t.Parallel()
	const (
		ws  = "11000000-0000-4000-8000-0000000000f1"
		run = "22000000-0000-4000-8000-0000000000f1"
		sid = "33000000-0000-4000-8000-0000000000f1"
		uid = "44000000-0000-4000-8000-0000000000f1"
	)
	store := &failRunStore{run: execution.AgentRun{
		ID: run, WorkspaceID: ws, SessionID: sid, Status: "RUNNING",
		LockVersion: 1, TriggeredByType: "USER", TriggeredByID: uid, TraceID: "trace-f1",
	}}
	results := &failRunResults{}
	events := &failRunEvents{}
	b := &Bridge{
		results: &failRunResultsWithStatus{results: results, store: store},
		runs:    store,
		events:  events,
		logger:  slog.Default(),
		now:     func() time.Time { return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC) },
	}
	job := agentrun.Job{
		WorkspaceID: ws, SessionID: sid, RunID: run, UserMessageID: "msg-1", ActorID: uid,
	}
	cause := errors.New("model timeout")
	if err := b.failRun(context.Background(), job, store.run, cause); !errors.Is(err, cause) {
		t.Fatalf("failRun must return original cause, got %v", err)
	}
	if results.calls != 1 {
		t.Fatalf("RecordAssistantResult calls = %d, want 1", results.calls)
	}
	if len(results.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(results.messages))
	}
	finished, err := store.GetAgentRun(context.Background(), ws, run)
	if err != nil || finished.Status != "FAILED" {
		t.Fatalf("run status = %q err=%v, want FAILED", finished.Status, err)
	}
	kinds := events.kinds()
	if len(kinds) != 1 || kinds[0] != chatruntime.ProtocolRecordRunFailed {
		t.Fatalf("protocol kinds = %v, want [run.failed]", kinds)
	}
	rec := events.records[0]
	if rec.Message == nil || rec.Message.ID == "" {
		t.Fatal("protocol record missing failed assistant message")
	}
	if rec.ActorID != uid {
		t.Fatalf("ActorID = %q, want %q", rec.ActorID, uid)
	}
	if rec.Run.Status != "FAILED" {
		t.Fatalf("protocol Run.Status = %q", rec.Run.Status)
	}
}

func TestFailRun_ProtocolFailureDoesNotRollbackDurableFAILED(t *testing.T) {
	t.Parallel()
	const (
		ws  = "11000000-0000-4000-8000-0000000000f2"
		run = "22000000-0000-4000-8000-0000000000f2"
		sid = "33000000-0000-4000-8000-0000000000f2"
		uid = "44000000-0000-4000-8000-0000000000f2"
	)
	store := &failRunStore{run: execution.AgentRun{
		ID: run, WorkspaceID: ws, SessionID: sid, Status: "RUNNING",
		LockVersion: 2, TriggeredByType: "USER", TriggeredByID: uid,
	}}
	results := &failRunResults{}
	events := &failRunEvents{failN: 1}
	b := &Bridge{
		results: &failRunResultsWithStatus{results: results, store: store},
		runs:    store,
		events:  events,
		logger:  slog.Default(),
		now:     time.Now,
	}
	job := agentrun.Job{WorkspaceID: ws, SessionID: sid, RunID: run, ActorID: uid}
	cause := errors.New("upstream boom")
	if err := b.failRun(context.Background(), job, store.run, cause); !errors.Is(err, cause) {
		t.Fatalf("must return cause even when protocol fails: %v", err)
	}
	finished, _ := store.GetAgentRun(context.Background(), ws, run)
	if finished.Status != "FAILED" {
		t.Fatalf("status = %q, durable FAILED must remain after protocol failure", finished.Status)
	}
	if len(results.messages) != 1 {
		t.Fatalf("messages = %d, want 1 (not rolled back)", len(results.messages))
	}
	if events.calls != 1 {
		t.Fatalf("protocol attempts = %d, want 1", events.calls)
	}
	if len(events.records) != 0 {
		t.Fatalf("successful protocol records = %d, want 0", len(events.records))
	}
}

func TestFailRun_ReentryDoesNotCreateSecondMessage(t *testing.T) {
	t.Parallel()
	const (
		ws  = "11000000-0000-4000-8000-0000000000f3"
		run = "22000000-0000-4000-8000-0000000000f3"
		sid = "33000000-0000-4000-8000-0000000000f3"
		uid = "44000000-0000-4000-8000-0000000000f3"
	)
	store := &failRunStore{run: execution.AgentRun{
		ID: run, WorkspaceID: ws, SessionID: sid, Status: "RUNNING",
		LockVersion: 1, TriggeredByType: "USER", TriggeredByID: uid,
	}}
	results := &failRunResults{}
	events := &failRunEvents{}
	b := &Bridge{
		results: &failRunResultsWithStatus{results: results, store: store},
		runs:    store,
		events:  events,
		logger:  slog.Default(),
		now:     time.Now,
	}
	job := agentrun.Job{WorkspaceID: ws, SessionID: sid, RunID: run, ActorID: uid}
	cause := errors.New("once")
	_ = b.failRun(context.Background(), job, store.run, cause)
	// Second call: run already FAILED — no second message, no second protocol
	// (no message pointer available without re-loading history).
	_ = b.failRun(context.Background(), job, store.run, cause)
	if results.calls != 1 {
		t.Fatalf("RecordAssistantResult calls = %d, want 1 (no reentry write)", results.calls)
	}
	if len(results.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(results.messages))
	}
	// First call projected once; second call has no message so no protocol re-append.
	if len(events.records) != 1 {
		t.Fatalf("protocol records = %d, want 1", len(events.records))
	}
}

func TestFailRun_UserCancelDoesNotForceFailed(t *testing.T) {
	t.Parallel()
	store := &failRunStore{run: execution.AgentRun{
		ID: "22000000-0000-4000-8000-0000000000f5", WorkspaceID: "ws",
		SessionID: "s", Status: "RUNNING", LockVersion: 2,
	}}
	results := &failRunResults{}
	events := &failRunEvents{}
	b := &Bridge{
		results: &failRunResultsWithStatus{results: results, store: store},
		runs:    store, events: events,
		logger: slog.Default(), now: time.Now,
	}
	job := agentrun.Job{WorkspaceID: "ws", SessionID: "s", RunID: store.run.ID, ActorID: "u"}
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(ErrRunCancelled)
	if err := b.failRun(ctx, job, store.run, context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("failRun must return the drive error, got %v", err)
	}
	if results.calls != 0 {
		t.Fatalf("user cancel must not persist FAILED, calls=%d", results.calls)
	}
	if store.run.Status != "RUNNING" {
		t.Fatalf("user cancel must leave durable status to the cancel API, got %s", store.run.Status)
	}
}

func TestFailRun_DoesNotTouchSucceededOrCancelledSemantics(t *testing.T) {
	t.Parallel()
	// completeRun path is separate; ensure failRun ignores non-RUNNING.
	store := &failRunStore{run: execution.AgentRun{
		ID: "22000000-0000-4000-8000-0000000000f4", WorkspaceID: "ws",
		SessionID: "s", Status: "SUCCEEDED", LockVersion: 5,
	}}
	results := &failRunResults{}
	events := &failRunEvents{}
	b := &Bridge{
		results: results, runs: store, events: events,
		logger: slog.Default(), now: time.Now,
	}
	job := agentrun.Job{WorkspaceID: "ws", SessionID: "s", RunID: store.run.ID, ActorID: "u"}
	_ = b.failRun(context.Background(), job, store.run, errors.New("late"))
	if results.calls != 0 {
		t.Fatalf("must not record assistant on non-RUNNING, calls=%d", results.calls)
	}
	if len(events.records) != 0 {
		t.Fatalf("must not project terminal on non-RUNNING")
	}
}
