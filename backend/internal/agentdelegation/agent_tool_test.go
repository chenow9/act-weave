package agentdelegation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

type memAudit struct {
	mu   sync.Mutex
	rows map[string]Delegation
}

func (m *memAudit) CreateDelegationAndStep(_ context.Context, in CreateDelegationInput) (Delegation, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]Delegation{}
	}
	if existing, ok := m.rows[in.IdempotencyKey]; ok {
		return existing, true, nil
	}
	d := Delegation{
		ID: in.ID, WorkspaceID: in.WorkspaceID, ParentRunID: in.ParentRunID,
		ParentDelegationID: in.ParentDelegationID,
		CallerAgentID:      in.CallerAgentID, Mode: in.Mode, Protocol: in.Protocol,
		Origin: in.Origin, Depth: in.Depth, BindingVersion: in.BindingVersion,
		ToolCallID: in.ToolCallID, IdempotencyKey: in.IdempotencyKey,
		Status: StatusRunning, StepID: in.StepID,
		InputSummary: in.InputSummary, InputPayload: in.InputPayload,
	}
	if in.TargetAgentID != nil {
		d.TargetAgentID = in.TargetAgentID
	}
	m.rows[in.IdempotencyKey] = d
	return d, false, nil
}

func (m *memAudit) FinalizeDelegation(_ context.Context, in FinalizeDelegationInput) (Delegation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, d := range m.rows {
		if d.ID == in.DelegationID {
			if isTerminal(d.Status) {
				// Sticky: same status is idempotent; different terminal → ErrConflict.
				if d.Status != in.Status {
					return Delegation{}, fmt.Errorf("%w: delegation already %s, cannot finalize as %s",
						ErrConflict, d.Status, in.Status)
				}
				return d, nil
			}
			d.Status = in.Status
			d.OutputSummary = in.OutputSummary
			d.OutputPayload = in.OutputPayload
			d.ErrorCode = in.ErrorCode
			d.ErrorMessage = in.ErrorMessage
			d.StepID = in.StepID
			if in.ChildRunID != nil {
				d.ChildRunID = in.ChildRunID
			}
			m.rows[k] = d
			return d, nil
		}
	}
	return Delegation{}, ErrNotFound
}

func (m *memAudit) SetChildRunID(_ context.Context, _, delegationID, childRunID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, d := range m.rows {
		if d.ID == delegationID {
			if d.ChildRunID != nil && *d.ChildRunID != "" && *d.ChildRunID != childRunID {
				return errors.New("child_run_id already set")
			}
			id := childRunID
			d.ChildRunID = &id
			m.rows[k] = d
			return nil
		}
	}
	return ErrNotFound
}

func (m *memAudit) RecordDispatchAttempt(_ context.Context, _, delegationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, d := range m.rows {
		if d.ID == delegationID {
			d.AttemptCount++
			if d.AttemptCount > 0 {
				d.RetryCount = d.AttemptCount - 1
			}
			m.rows[k] = d
			return nil
		}
	}
	return ErrNotFound
}

func (m *memAudit) AccumulateModelTokens(_ context.Context, _, _ string, _ TokenUsage) error {
	return nil
}

type stubInner struct {
	result string
	err    error
	calls  int
}

func (s *stubInner) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "inner", Desc: "inner"}, nil
}
func (s *stubInner) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	s.calls++
	return s.result, s.err
}

func TestAuditedAgentTool_FailClosedWithoutAudit(t *testing.T) {
	t.Parallel()
	inner := &stubInner{result: "ok"}
	// nil audit rejected at construction
	if _, err := NewAuditedAgentTool(AgentToolConfig{Inner: inner}); err == nil {
		t.Fatal("expected error")
	}
}

func TestAuditedAgentTool_PrewriteThenInvoke(t *testing.T) {
	t.Parallel()
	audit := &memAudit{}
	inner := &stubInner{result: `{"answer":"from-child"}`}
	edge := GraphEdgeSnapshot{
		BindingID:     uuid.Must(uuid.NewV7()).String(),
		CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		CallableName:  "call_helper", Mode: ModeInline, Version: 1,
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "call_helper", Edge: edge, Audit: audit,
		DefaultCallerAgentID: edge.CallerAgentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws := uuid.Must(uuid.NewV7()).String()
	run := uuid.Must(uuid.NewV7()).String()
	ctx := WithRunContext(context.Background(), &RunContext{
		WorkspaceID: ws, ParentRunID: run, RootRunID: run,
		CallerAgentID: edge.CallerAgentID, Depth: 0, Budget: NewBudget(),
	})
	out, err := tool.InvokableRun(ctx, `{"request":"do work"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"answer":"from-child"}` {
		t.Fatalf("out=%s", out)
	}
	if inner.calls != 1 {
		t.Fatalf("calls=%d", inner.calls)
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if len(audit.rows) != 1 {
		t.Fatalf("rows=%d", len(audit.rows))
	}
	for _, d := range audit.rows {
		if d.Status != StatusSucceeded {
			t.Fatalf("status=%s", d.Status)
		}
	}
}

func TestAuditedAgentTool_BudgetBlocksDispatch(t *testing.T) {
	t.Parallel()
	audit := &memAudit{}
	inner := &stubInner{result: "x"}
	edge := GraphEdgeSnapshot{BindingID: "b1", CallableName: "c", Version: 1, Mode: ModeInline,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(), TargetAgentID: uuid.Must(uuid.NewV7()).String()}
	tool, err := NewAuditedAgentTool(AgentToolConfig{Inner: inner, Name: "c", Edge: edge, Audit: audit})
	if err != nil {
		t.Fatal(err)
	}
	budget := NewBudget()
	budget.MaxTotal = 0
	ctx := WithRunContext(context.Background(), &RunContext{
		WorkspaceID:   uuid.Must(uuid.NewV7()).String(),
		ParentRunID:   uuid.Must(uuid.NewV7()).String(),
		CallerAgentID: edge.CallerAgentID, Budget: budget,
	})
	out, err := tool.InvokableRun(ctx, `{"request":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls != 0 {
		t.Fatal("must not dispatch")
	}
	var parsed map[string]any
	_ = json.Unmarshal([]byte(out), &parsed)
	if parsed["ok"] != false {
		t.Fatalf("out=%s", out)
	}
}

func TestAuditedAgentTool_IdempotentNoRedisp(t *testing.T) {
	t.Parallel()
	audit := &memAudit{}
	// Pre-seed terminal success
	key := "run:tc:1:b"
	audit.rows = map[string]Delegation{
		key: {ID: "d1", Status: StatusSucceeded, OutputPayload: json.RawMessage(`prior`), IdempotencyKey: key, StepID: "s1"},
	}
	// Force known tool call id by using same idempotency through wrapper is random —
	// instead test memAudit replay path via CreateDelegationAndStep directly.
	d, replay, err := audit.CreateDelegationAndStep(context.Background(), CreateDelegationInput{
		ID: "d2", WorkspaceID: uuid.Must(uuid.NewV7()).String(),
		ParentRunID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		Mode: ModeInline, Protocol: ProtocolInternal, Origin: OriginInternal,
		Depth: 1, BindingVersion: 1, ToolCallID: "tc", IdempotencyKey: key,
		StepID: uuid.Must(uuid.NewV7()).String(), AgentID: uuid.Must(uuid.NewV7()).String(),
	})
	if err != nil || !replay {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
	if string(d.OutputPayload) != "prior" {
		t.Fatalf("%s", d.OutputPayload)
	}
}

func TestAuditedAgentTool_AuditFailureNoDispatch(t *testing.T) {
	t.Parallel()
	inner := &stubInner{result: "x"}
	failAudit := &failAuditWriter{}
	edge := GraphEdgeSnapshot{BindingID: "b", CallableName: "c", Version: 1, Mode: ModeInline,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(), TargetAgentID: uuid.Must(uuid.NewV7()).String()}
	tool, err := NewAuditedAgentTool(AgentToolConfig{Inner: inner, Name: "c", Edge: edge, Audit: failAudit})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithRunContext(context.Background(), &RunContext{
		WorkspaceID:   uuid.Must(uuid.NewV7()).String(),
		ParentRunID:   uuid.Must(uuid.NewV7()).String(),
		CallerAgentID: edge.CallerAgentID, Budget: NewBudget(),
	})
	out, _ := tool.InvokableRun(ctx, `{"request":"x"}`)
	if inner.calls != 0 {
		t.Fatal("dispatch after audit fail")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(out)
	}
	if parsed["ok"] != false {
		t.Fatalf("%v", parsed)
	}
	if parsed["errorCode"] != "DELEGATION_AUDIT_PREWRITE_FAILED" {
		t.Fatalf("errorCode=%v", parsed["errorCode"])
	}
}

type failAuditWriter struct{}

func (f *failAuditWriter) RecordDispatchAttempt(context.Context, string, string) error { return nil }
func (f *failAuditWriter) AccumulateModelTokens(context.Context, string, string, TokenUsage) error {
	return nil
}
func (f *failAuditWriter) CreateDelegationAndStep(context.Context, CreateDelegationInput) (Delegation, bool, error) {
	return Delegation{}, false, errors.New("db down")
}
func (f *failAuditWriter) FinalizeDelegation(context.Context, FinalizeDelegationInput) (Delegation, error) {
	return Delegation{}, errors.New("db down")
}
func (f *failAuditWriter) SetChildRunID(context.Context, string, string, string) error {
	return errors.New("db down")
}

type memChildRuns struct {
	mu       sync.Mutex
	started  []ChildRunStartInput
	finished map[string]string // id → status
	cancel   map[string]bool
}

func (m *memChildRuns) StartChild(_ context.Context, in ChildRunStartInput) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.finished == nil {
		m.finished = map[string]string{}
	}
	id := uuid.Must(uuid.NewV7()).String()
	m.started = append(m.started, in)
	m.finished[id] = StatusRunning
	return id, nil
}

func (m *memChildRuns) FinishChild(_ context.Context, _, childRunID, status, _ string, _ json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finished[childRunID] = status
	return nil
}

func (m *memChildRuns) CancelChild(_ context.Context, _, childRunID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel == nil {
		m.cancel = map[string]bool{}
	}
	m.cancel[childRunID] = true
	m.finished[childRunID] = StatusCancelled
	return nil
}

func TestAuditedAgentTool_TASK_NoCrossRunParentStepID(t *testing.T) {
	t.Parallel()
	audit := &memAudit{}
	children := &memChildRuns{}
	var nestedRC *RunContext
	inner := &stubInner{result: "ok"}
	// wrap to capture child context
	capturing := &contextCapturingInner{inner: inner, capture: &nestedRC}
	edge := GraphEdgeSnapshot{
		BindingID:     uuid.Must(uuid.NewV7()).String(),
		CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		CallableName:  "call_task", Mode: ModeTask, Version: 1,
		ContextPolicy: ContextTaskOnly,
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: capturing, Name: "call_task", Edge: edge, Audit: audit,
		DefaultCallerAgentID: edge.CallerAgentID, ChildRuns: children,
		DefaultTaskTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	parent := uuid.Must(uuid.NewV7()).String()
	ctx := WithRunContext(context.Background(), &RunContext{
		WorkspaceID: uuid.Must(uuid.NewV7()).String(), ParentRunID: parent, RootRunID: parent, RunID: parent,
		CallerAgentID: edge.CallerAgentID, Depth: 0, Budget: NewBudget(),
	})
	if _, err := tool.InvokableRun(ctx, `{"request":"x"}`); err != nil {
		t.Fatal(err)
	}
	if nestedRC == nil {
		t.Fatal("expected nested run context")
	}
	if nestedRC.ParentStepID != nil {
		t.Fatalf("TASK must not set ParentStepID (cross-run FK); got %v", *nestedRC.ParentStepID)
	}
	if nestedRC.ParentDelegationID == nil {
		t.Fatal("expected ParentDelegationID for nesting via delegation_id")
	}
	if nestedRC.RunID == parent {
		t.Fatal("TASK RunID should be child run, not parent")
	}
}

type contextCapturingInner struct {
	inner   *stubInner
	capture **RunContext
}

func (c *contextCapturingInner) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return c.inner.Info(ctx)
}
func (c *contextCapturingInner) InvokableRun(ctx context.Context, in string, opts ...tool.Option) (string, error) {
	if rc, ok := RunContextFrom(ctx); ok {
		*c.capture = rc
	}
	return c.inner.InvokableRun(ctx, in, opts...)
}

func TestAuditedAgentTool_TASK_CreatesIndependentChildRun(t *testing.T) {
	t.Parallel()
	audit := &memAudit{}
	children := &memChildRuns{}
	inner := &stubInner{result: `{"from":"task-child"}`}
	edge := GraphEdgeSnapshot{
		BindingID:     uuid.Must(uuid.NewV7()).String(),
		CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		CallableName:  "call_task", Mode: ModeTask, Version: 1,
		ContextPolicy: ContextTaskOnly,
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "call_task", Edge: edge, Audit: audit,
		DefaultCallerAgentID: edge.CallerAgentID, ChildRuns: children,
		DefaultTaskTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, parent := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	ctx := WithRunContext(context.Background(), &RunContext{
		WorkspaceID: ws, ParentRunID: parent, RootRunID: parent, RunID: parent,
		CallerAgentID: edge.CallerAgentID, Depth: 0, Budget: NewBudget(),
	})
	out, err := tool.InvokableRun(ctx, `{"request":"do task"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"from":"task-child"}` {
		t.Fatalf("out=%s", out)
	}
	if len(children.started) != 1 {
		t.Fatalf("child starts=%d", len(children.started))
	}
	if children.started[0].ParentRunID != parent {
		t.Fatalf("parent_run_id=%s", children.started[0].ParentRunID)
	}
	if children.started[0].TargetAgentID != edge.TargetAgentID {
		t.Fatalf("target=%s", children.started[0].TargetAgentID)
	}
	// Delegation must have child_run_id linked and SUCCEEDED.
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if len(audit.rows) != 1 {
		t.Fatalf("rows=%d", len(audit.rows))
	}
	for _, d := range audit.rows {
		if d.Status != StatusSucceeded {
			t.Fatalf("status=%s", d.Status)
		}
		if d.ChildRunID == nil || *d.ChildRunID == "" {
			t.Fatal("child_run_id not set on delegation")
		}
		if d.Mode != ModeTask {
			t.Fatalf("mode=%s", d.Mode)
		}
		if children.finished[*d.ChildRunID] != StatusSucceeded {
			t.Fatalf("child finish status=%s", children.finished[*d.ChildRunID])
		}
	}
}

func TestAuditedAgentTool_TASK_Timeout(t *testing.T) {
	t.Parallel()
	audit := &memAudit{}
	children := &memChildRuns{}
	inner := &stubInner{result: "never"}
	// Inner that blocks until ctx done.
	blocking := &blockingInner{}
	edge := GraphEdgeSnapshot{
		BindingID: "bt", CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		CallableName:  "slow", Mode: ModeTask, Version: 1,
	}
	_ = inner
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: blocking, Name: "slow", Edge: edge, Audit: audit,
		DefaultCallerAgentID: edge.CallerAgentID, ChildRuns: children,
		DefaultTaskTimeout: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithRunContext(context.Background(), &RunContext{
		WorkspaceID:   uuid.Must(uuid.NewV7()).String(),
		ParentRunID:   uuid.Must(uuid.NewV7()).String(),
		CallerAgentID: edge.CallerAgentID, Budget: NewBudget(),
	})
	out, err := tool.InvokableRun(ctx, `{"request":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	_ = json.Unmarshal([]byte(out), &parsed)
	if parsed["ok"] != false {
		t.Fatalf("expected error payload, got %s", out)
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	for _, d := range audit.rows {
		if d.Status != StatusTimedOut {
			t.Fatalf("status=%s want TIMED_OUT", d.Status)
		}
	}
}

func TestAuditedAgentTool_IdempotentRetrySameToolCallID(t *testing.T) {
	t.Parallel()
	audit := &memAudit{}
	inner := &stubInner{result: "first"}
	edge := GraphEdgeSnapshot{
		BindingID: "idem-b", CallableName: "c", Version: 3, Mode: ModeInline,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "c", Edge: edge, Audit: audit,
		DefaultCallerAgentID: edge.CallerAgentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Without ToolsNode, extractToolCallID returns the stable sentinel
	// "missing-tool-call-id" so retries share the same idempotency key.
	ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	ctx := WithRunContext(context.Background(), &RunContext{
		WorkspaceID: ws, ParentRunID: run, RootRunID: run, RunID: run,
		CallerAgentID: edge.CallerAgentID, Budget: NewBudget(),
	})
	out1, err := tool.InvokableRun(ctx, `{"request":"once"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out1 != "first" || inner.calls != 1 {
		t.Fatalf("first call out=%s calls=%d", out1, inner.calls)
	}
	// Retry with same stable tool-call identity must not re-dispatch.
	out2, err := tool.InvokableRun(ctx, `{"request":"once"}`)
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 {
		t.Fatalf("retry redispatched calls=%d", inner.calls)
	}
	// Replay returns prior result payload ("first" extracted from {"result":"first"}).
	if out2 != "first" && !strings.Contains(out2, "first") {
		t.Fatalf("replay out=%s", out2)
	}
	// Prove idempotency key used stable toolCallID.
	wantKey := IdempotencyKey(run, "missing-tool-call-id", 3, "idem-b")
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if _, ok := audit.rows[wantKey]; !ok {
		t.Fatalf("expected idempotency key %q in %v", wantKey, keysOf(audit.rows))
	}
}

func keysOf(m map[string]Delegation) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

type blockingInner struct{}

func (b *blockingInner) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "slow", Desc: "blocks"}, nil
}
func (b *blockingInner) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
