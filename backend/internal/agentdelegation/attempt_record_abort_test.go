package agentdelegation

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAttemptRecordAbort_ClassifiesContext(t *testing.T) {
	t.Parallel()
	auditErr := errors.New("sql: connection reset")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	status, code, _ := attemptRecordAbort(canceled, context.Canceled)
	if status != StatusCancelled || code != "DELEGATION_CANCELLED" {
		t.Fatalf("canceled: status=%s code=%s", status, code)
	}

	deadline, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	<-deadline.Done()
	status, code, _ = attemptRecordAbort(deadline, context.DeadlineExceeded)
	if status != StatusTimedOut || code != "DELEGATION_TIMED_OUT" {
		t.Fatalf("deadline: status=%s code=%s", status, code)
	}

	status, code, msg := attemptRecordAbort(context.Background(), auditErr)
	if status != StatusFailed || code != "DELEGATION_ATTEMPT_RECORD_FAILED" {
		t.Fatalf("audit fail: status=%s code=%s", status, code)
	}
	if msg != auditErr.Error() {
		t.Fatalf("audit fail msg=%q", msg)
	}
}

// blockingAttemptAudit waits in RecordDispatchAttempt until ctx is done, then
// returns ctx.Err(). Create/Finalize/SetChildRunID stay on memAudit so TASK
// child start can complete before the attempt-record window.
type blockingAttemptAudit struct {
	inner   *memAudit
	entered chan struct{}
	once    sync.Once
}

func newBlockingAttemptAudit() *blockingAttemptAudit {
	return &blockingAttemptAudit{inner: &memAudit{}, entered: make(chan struct{})}
}

func (a *blockingAttemptAudit) CreateDelegationAndStep(ctx context.Context, in CreateDelegationInput) (Delegation, bool, error) {
	return a.inner.CreateDelegationAndStep(ctx, in)
}
func (a *blockingAttemptAudit) FinalizeDelegation(ctx context.Context, in FinalizeDelegationInput) (Delegation, error) {
	return a.inner.FinalizeDelegation(ctx, in)
}
func (a *blockingAttemptAudit) SetChildRunID(ctx context.Context, workspaceID, delegationID, childRunID string) error {
	return a.inner.SetChildRunID(ctx, workspaceID, delegationID, childRunID)
}
func (a *blockingAttemptAudit) AccumulateModelTokens(ctx context.Context, workspaceID, delegationID string, usage TokenUsage) error {
	return a.inner.AccumulateModelTokens(ctx, workspaceID, delegationID, usage)
}
func (a *blockingAttemptAudit) RecordDispatchAttempt(ctx context.Context, _, _ string) error {
	a.once.Do(func() { close(a.entered) })
	<-ctx.Done()
	return ctx.Err()
}

func TestAuditedAgentTool_TASK_AttemptRecordCancelIsCancelled(t *testing.T) {
	t.Parallel()
	audit := newBlockingAttemptAudit()
	children := &memChildRuns{}
	inner := &stubInner{result: "must-not-run"}
	edge := GraphEdgeSnapshot{
		BindingID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		CallableName:  "call_b", Mode: ModeTask, Version: 1,
		ContextPolicy: ContextTaskOnly,
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "call_b", Edge: edge, Audit: audit,
		DefaultCallerAgentID: edge.CallerAgentID, ChildRuns: children,
		DefaultTaskTimeout: 8 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, parent := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithRunContext(ctx, &RunContext{
		WorkspaceID: ws, ParentRunID: parent, RootRunID: parent, RunID: parent,
		CallerAgentID: edge.CallerAgentID, Budget: NewBudget(),
	})
	done := make(chan string, 1)
	go func() {
		out, _ := tool.InvokableRun(ctx, `{"request":"x"}`)
		done <- out
	}()
	select {
	case <-audit.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("RecordDispatchAttempt never entered")
	}
	cancel()
	var out string
	select {
	case out = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("InvokableRun hung after cancel")
	}
	if inner.calls != 0 {
		t.Fatalf("Inner must not run after attempt-record abort, calls=%d", inner.calls)
	}
	code, _ := parseDelegationErrorJSON(t, out)
	if code != "DELEGATION_CANCELLED" {
		t.Fatalf("tool errorCode=%s want DELEGATION_CANCELLED body=%s", code, out)
	}
	assertAttemptRecordTerminals(t, audit.inner, children, StatusCancelled)
}

func TestAuditedAgentTool_TASK_AttemptRecordTimeoutIsTimedOut(t *testing.T) {
	t.Parallel()
	audit := newBlockingAttemptAudit()
	children := &memChildRuns{}
	inner := &stubInner{result: "must-not-run"}
	edge := GraphEdgeSnapshot{
		BindingID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		CallableName:  "call_b_to", Mode: ModeTask, Version: 1,
		ContextPolicy: ContextTaskOnly,
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "call_b_to", Edge: edge, Audit: audit,
		DefaultCallerAgentID: edge.CallerAgentID, ChildRuns: children,
		DefaultTaskTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, parent := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	ctx := WithRunContext(context.Background(), &RunContext{
		WorkspaceID: ws, ParentRunID: parent, RootRunID: parent, RunID: parent,
		CallerAgentID: edge.CallerAgentID, Budget: NewBudget(),
	})
	out, _ := tool.InvokableRun(ctx, `{"request":"x"}`)
	if inner.calls != 0 {
		t.Fatalf("Inner must not run after attempt-record abort, calls=%d", inner.calls)
	}
	code, _ := parseDelegationErrorJSON(t, out)
	if code != "DELEGATION_TIMED_OUT" {
		t.Fatalf("tool errorCode=%s want DELEGATION_TIMED_OUT body=%s", code, out)
	}
	assertAttemptRecordTerminals(t, audit.inner, children, StatusTimedOut)
}

type blockingStartChild struct {
	entered chan struct{}
	once    sync.Once
}

func (s *blockingStartChild) StartChild(ctx context.Context, _ ChildRunStartInput) (string, error) {
	s.once.Do(func() { close(s.entered) })
	<-ctx.Done()
	return "", ctx.Err()
}
func (s *blockingStartChild) FinishChild(context.Context, string, string, string, string, json.RawMessage) error {
	return nil
}
func (s *blockingStartChild) CancelChild(context.Context, string, string) error { return nil }

func TestAuditedAgentTool_TASK_StartChildCancelIsCancelled(t *testing.T) {
	t.Parallel()
	audit := &memAudit{}
	children := &blockingStartChild{entered: make(chan struct{})}
	inner := &stubInner{result: "must-not-run"}
	edge := GraphEdgeSnapshot{
		BindingID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		CallableName:  "call_start", Mode: ModeTask, Version: 1,
		ContextPolicy: ContextTaskOnly,
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "call_start", Edge: edge, Audit: audit,
		DefaultCallerAgentID: edge.CallerAgentID, ChildRuns: children,
		DefaultTaskTimeout: 8 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, parent := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithRunContext(ctx, &RunContext{
		WorkspaceID: ws, ParentRunID: parent, RootRunID: parent, RunID: parent,
		CallerAgentID: edge.CallerAgentID, Budget: NewBudget(),
	})
	done := make(chan string, 1)
	go func() {
		out, _ := tool.InvokableRun(ctx, `{"request":"x"}`)
		done <- out
	}()
	select {
	case <-children.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("StartChild never entered")
	}
	cancel()
	var out string
	select {
	case out = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("InvokableRun hung after StartChild cancel")
	}
	if inner.calls != 0 {
		t.Fatalf("Inner must not run, calls=%d", inner.calls)
	}
	code, _ := parseDelegationErrorJSON(t, out)
	if code != "DELEGATION_CANCELLED" {
		t.Fatalf("tool errorCode=%s want DELEGATION_CANCELLED body=%s", code, out)
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if len(audit.rows) != 1 {
		t.Fatalf("delegations=%d want 1", len(audit.rows))
	}
	for _, d := range audit.rows {
		if d.Status != StatusCancelled {
			t.Fatalf("delegation status=%s want CANCELLED", d.Status)
		}
	}
}

type blockingLinkAudit struct {
	inner   *memAudit
	entered chan struct{}
	once    sync.Once
}

func newBlockingLinkAudit() *blockingLinkAudit {
	return &blockingLinkAudit{inner: &memAudit{}, entered: make(chan struct{})}
}

func (a *blockingLinkAudit) CreateDelegationAndStep(ctx context.Context, in CreateDelegationInput) (Delegation, bool, error) {
	return a.inner.CreateDelegationAndStep(ctx, in)
}
func (a *blockingLinkAudit) FinalizeDelegation(ctx context.Context, in FinalizeDelegationInput) (Delegation, error) {
	return a.inner.FinalizeDelegation(ctx, in)
}
func (a *blockingLinkAudit) RecordDispatchAttempt(ctx context.Context, workspaceID, delegationID string) error {
	return a.inner.RecordDispatchAttempt(ctx, workspaceID, delegationID)
}
func (a *blockingLinkAudit) AccumulateModelTokens(ctx context.Context, workspaceID, delegationID string, usage TokenUsage) error {
	return a.inner.AccumulateModelTokens(ctx, workspaceID, delegationID, usage)
}
func (a *blockingLinkAudit) SetChildRunID(ctx context.Context, _, _, _ string) error {
	a.once.Do(func() { close(a.entered) })
	<-ctx.Done()
	return ctx.Err()
}

func TestAuditedAgentTool_TASK_SetChildRunIDCancelIsCancelled(t *testing.T) {
	t.Parallel()
	audit := newBlockingLinkAudit()
	children := &memChildRuns{}
	inner := &stubInner{result: "must-not-run"}
	edge := GraphEdgeSnapshot{
		BindingID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		CallableName:  "call_link", Mode: ModeTask, Version: 1,
		ContextPolicy: ContextTaskOnly,
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "call_link", Edge: edge, Audit: audit,
		DefaultCallerAgentID: edge.CallerAgentID, ChildRuns: children,
		DefaultTaskTimeout: 8 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, parent := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithRunContext(ctx, &RunContext{
		WorkspaceID: ws, ParentRunID: parent, RootRunID: parent, RunID: parent,
		CallerAgentID: edge.CallerAgentID, Budget: NewBudget(),
	})
	done := make(chan string, 1)
	go func() {
		out, _ := tool.InvokableRun(ctx, `{"request":"x"}`)
		done <- out
	}()
	select {
	case <-audit.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("SetChildRunID never entered")
	}
	cancel()
	var out string
	select {
	case out = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("InvokableRun hung after SetChildRunID cancel")
	}
	if inner.calls != 0 {
		t.Fatalf("Inner must not run, calls=%d", inner.calls)
	}
	code, _ := parseDelegationErrorJSON(t, out)
	if code != "DELEGATION_CANCELLED" {
		t.Fatalf("tool errorCode=%s want DELEGATION_CANCELLED body=%s", code, out)
	}
	assertAttemptRecordTerminals(t, audit.inner, children, StatusCancelled)
}

func TestAuditedAgentTool_TASK_SetChildRunIDLinkFailureIsFailed(t *testing.T) {
	t.Parallel()
	audit := &failLinkAudit{inner: &memAudit{}, err: errors.New("link store down")}
	children := &memChildRuns{}
	inner := &stubInner{result: "must-not-run"}
	edge := GraphEdgeSnapshot{
		BindingID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		CallableName:  "call_link_fail", Mode: ModeTask, Version: 1,
		ContextPolicy: ContextTaskOnly,
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "call_link_fail", Edge: edge, Audit: audit,
		DefaultCallerAgentID: edge.CallerAgentID, ChildRuns: children,
		DefaultTaskTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, parent := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	ctx := WithRunContext(context.Background(), &RunContext{
		WorkspaceID: ws, ParentRunID: parent, RootRunID: parent, RunID: parent,
		CallerAgentID: edge.CallerAgentID, Budget: NewBudget(),
	})
	out, _ := tool.InvokableRun(ctx, `{"request":"x"}`)
	if inner.calls != 0 {
		t.Fatal("Inner must not run")
	}
	code, _ := parseDelegationErrorJSON(t, out)
	if code == "DELEGATION_CANCELLED" || code == "DELEGATION_TIMED_OUT" {
		t.Fatalf("plain link failure remapped to %s", code)
	}
	assertAttemptRecordTerminals(t, audit.inner, children, StatusFailed)
}

type failLinkAudit struct {
	inner *memAudit
	err   error
}

func (a *failLinkAudit) CreateDelegationAndStep(ctx context.Context, in CreateDelegationInput) (Delegation, bool, error) {
	return a.inner.CreateDelegationAndStep(ctx, in)
}
func (a *failLinkAudit) FinalizeDelegation(ctx context.Context, in FinalizeDelegationInput) (Delegation, error) {
	return a.inner.FinalizeDelegation(ctx, in)
}
func (a *failLinkAudit) RecordDispatchAttempt(ctx context.Context, workspaceID, delegationID string) error {
	return a.inner.RecordDispatchAttempt(ctx, workspaceID, delegationID)
}
func (a *failLinkAudit) AccumulateModelTokens(ctx context.Context, workspaceID, delegationID string, usage TokenUsage) error {
	return a.inner.AccumulateModelTokens(ctx, workspaceID, delegationID, usage)
}
func (a *failLinkAudit) SetChildRunID(context.Context, string, string, string) error {
	return a.err
}

func TestAuditedAgentTool_TASK_AttemptRecordAuditFailureIsFailed(t *testing.T) {
	t.Parallel()
	audit := &failAttemptAudit{inner: &memAudit{}, err: errors.New("audit store down")}
	children := &memChildRuns{}
	inner := &stubInner{result: "must-not-run"}
	edge := GraphEdgeSnapshot{
		BindingID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		CallableName:  "call_b_fail", Mode: ModeTask, Version: 1,
		ContextPolicy: ContextTaskOnly,
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "call_b_fail", Edge: edge, Audit: audit,
		DefaultCallerAgentID: edge.CallerAgentID, ChildRuns: children,
		DefaultTaskTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, parent := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	ctx := WithRunContext(context.Background(), &RunContext{
		WorkspaceID: ws, ParentRunID: parent, RootRunID: parent, RunID: parent,
		CallerAgentID: edge.CallerAgentID, Budget: NewBudget(),
	})
	out, _ := tool.InvokableRun(ctx, `{"request":"x"}`)
	if inner.calls != 0 {
		t.Fatal("Inner must not run")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["errorCode"] == "DELEGATION_CANCELLED" || parsed["errorCode"] == "DELEGATION_TIMED_OUT" {
		t.Fatalf("plain audit failure remapped to %v", parsed["errorCode"])
	}
	assertAttemptRecordTerminals(t, audit.inner, children, StatusFailed)
}

type failAttemptAudit struct {
	inner *memAudit
	err   error
}

func (a *failAttemptAudit) CreateDelegationAndStep(ctx context.Context, in CreateDelegationInput) (Delegation, bool, error) {
	return a.inner.CreateDelegationAndStep(ctx, in)
}
func (a *failAttemptAudit) FinalizeDelegation(ctx context.Context, in FinalizeDelegationInput) (Delegation, error) {
	return a.inner.FinalizeDelegation(ctx, in)
}
func (a *failAttemptAudit) SetChildRunID(ctx context.Context, workspaceID, delegationID, childRunID string) error {
	return a.inner.SetChildRunID(ctx, workspaceID, delegationID, childRunID)
}
func (a *failAttemptAudit) AccumulateModelTokens(ctx context.Context, workspaceID, delegationID string, usage TokenUsage) error {
	return a.inner.AccumulateModelTokens(ctx, workspaceID, delegationID, usage)
}
func (a *failAttemptAudit) RecordDispatchAttempt(context.Context, string, string) error {
	return a.err
}

func assertAttemptRecordTerminals(t *testing.T, audit *memAudit, children *memChildRuns, want string) {
	t.Helper()
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if len(audit.rows) != 1 {
		t.Fatalf("delegations=%d want 1", len(audit.rows))
	}
	var childID string
	for _, d := range audit.rows {
		if d.Status != want {
			t.Fatalf("delegation status=%s want %s", d.Status, want)
		}
		if d.ChildRunID == nil || *d.ChildRunID == "" {
			t.Fatal("TASK child_run_id missing")
		}
		childID = *d.ChildRunID
	}
	children.mu.Lock()
	defer children.mu.Unlock()
	if got := children.finished[childID]; got != want {
		t.Fatalf("child finish=%s want %s", got, want)
	}
}
