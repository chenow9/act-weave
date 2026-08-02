package agentdelegation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// TestTASK_StartChildFailure_AttemptCountZero: StartChild fails before attempt —
// attempt_count stays 0 and Inner is never called.
func TestTASK_StartChildFailure_AttemptCountZero(t *testing.T) {
	t.Parallel()
	audit := &countingMemAudit{}
	inner := &countingInner{}
	child := &failStartChild{}
	edge := GraphEdgeSnapshot{
		BindingID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(), CallableName: "child",
		Mode: ModeTask, ContextPolicy: ContextTaskOnly, Version: 1, Protocol: ProtocolInternal,
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "child", Description: "d", Edge: edge, Audit: audit,
		ChildRuns: child, FinalizeRetries: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	rc := &RunContext{
		WorkspaceID: ws, ParentRunID: run, RootRunID: run, RunID: run,
		CallerAgentID: edge.CallerAgentID, Depth: 0, Budget: NewBudget(),
	}
	out, _ := tool.InvokableRun(WithRunContext(context.Background(), rc), `{}`)
	if inner.calls != 0 {
		t.Fatalf("Inner must not run; calls=%d", inner.calls)
	}
	if audit.attempts != 0 {
		t.Fatalf("attempt_count recorded=%d want 0", audit.attempts)
	}
	if !strings.Contains(out, "start") && !strings.Contains(out, "child") {
		t.Logf("out=%s", out)
	}
}

// TestTASK_AttemptRecordFailure_CleansChildAndNoInner
func TestTASK_AttemptRecordFailure_CleansChildAndNoInner(t *testing.T) {
	t.Parallel()
	audit := &countingMemAudit{failAttempt: true}
	inner := &countingInner{}
	child := &okChildRuns{}
	edge := GraphEdgeSnapshot{
		BindingID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(), CallableName: "child",
		Mode: ModeTask, ContextPolicy: ContextTaskOnly, Version: 1, Protocol: ProtocolInternal,
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "child", Description: "d", Edge: edge, Audit: audit,
		ChildRuns: child, FinalizeRetries: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	rc := &RunContext{
		WorkspaceID: ws, ParentRunID: run, RootRunID: run, RunID: run,
		CallerAgentID: edge.CallerAgentID, Depth: 0, Budget: NewBudget(),
	}
	out, _ := tool.InvokableRun(WithRunContext(context.Background(), rc), `{}`)
	if inner.calls != 0 {
		t.Fatalf("Inner must not run after attempt fail; calls=%d", inner.calls)
	}
	if child.started != 1 {
		t.Fatalf("child started=%d want 1", child.started)
	}
	if child.finished != 1 {
		t.Fatalf("child finished=%d want 1 (cleanup)", child.finished)
	}
	if child.lastStatus != StatusFailed {
		t.Fatalf("child status=%s want FAILED", child.lastStatus)
	}
	if !strings.Contains(out, "dispatch attempt") && !strings.Contains(out, "attempt") {
		t.Fatalf("want attempt error in out=%s", out)
	}
}

// TestFinalizeJoinsInvokeError surfaces both invoke and finalize errors.
func TestFinalizeJoinsInvokeError(t *testing.T) {
	t.Parallel()
	audit := &countingMemAudit{failFinalize: true}
	inner := &countingInner{err: errors.New("inject: invoke failed")}
	edge := GraphEdgeSnapshot{
		BindingID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(), CallableName: "child",
		Mode: ModeInline, ContextPolicy: ContextTaskOnly, Version: 1, Protocol: ProtocolInternal,
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "child", Description: "d", Edge: edge, Audit: audit,
		FinalizeRetries: 1,
		EnqueueFinalizeOutbox: func(ctx context.Context, workspaceID, delegationID, stepID string, payload json.RawMessage) error {
			return errors.New("inject: outbox failed")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	rc := &RunContext{
		WorkspaceID: ws, ParentRunID: run, RootRunID: run, RunID: run,
		CallerAgentID: edge.CallerAgentID, Depth: 0, Budget: NewBudget(),
	}
	out, _ := tool.InvokableRun(WithRunContext(context.Background(), rc), `{}`)
	if !strings.Contains(out, "invoke failed") {
		t.Fatalf("want invoke error in out=%s", out)
	}
	if !strings.Contains(out, "finalize") && !strings.Contains(out, "outbox") {
		t.Fatalf("want finalize/outbox error in out=%s", out)
	}
}

type countingMemAudit struct {
	mu           sync.Mutex
	rows         map[string]Delegation
	attempts     int
	failAttempt  bool
	failFinalize bool
}

func (m *countingMemAudit) CreateDelegationAndStep(_ context.Context, in CreateDelegationInput) (Delegation, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]Delegation{}
	}
	if d, ok := m.rows[in.IdempotencyKey]; ok {
		return d, true, nil
	}
	d := Delegation{
		ID: in.ID, WorkspaceID: in.WorkspaceID, ParentRunID: in.ParentRunID,
		Status: StatusRunning, StepID: in.StepID,
	}
	m.rows[in.IdempotencyKey] = d
	return d, false, nil
}
func (m *countingMemAudit) FinalizeDelegation(_ context.Context, in FinalizeDelegationInput) (Delegation, error) {
	if m.failFinalize {
		return Delegation{}, errors.New("inject: finalize failed")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, d := range m.rows {
		if d.ID == in.DelegationID {
			d.Status = in.Status
			m.rows[k] = d
			return d, nil
		}
	}
	return Delegation{ID: in.DelegationID, Status: in.Status}, nil
}
func (m *countingMemAudit) SetChildRunID(_ context.Context, _, _, _ string) error { return nil }
func (m *countingMemAudit) RecordDispatchAttempt(_ context.Context, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAttempt {
		return errors.New("inject: attempt record failed")
	}
	m.attempts++
	return nil
}
func (m *countingMemAudit) AccumulateModelTokens(context.Context, string, string, TokenUsage) error {
	return nil
}

type countingInner struct {
	calls int
	err   error
}

func (c *countingInner) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "inner"}, nil
}
func (c *countingInner) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	c.calls++
	return "ok", c.err
}

type failStartChild struct{}

func (f *failStartChild) StartChild(context.Context, ChildRunStartInput) (string, error) {
	return "", errors.New("inject: start child failed")
}
func (f *failStartChild) FinishChild(context.Context, string, string, string, string, json.RawMessage) error {
	return nil
}
func (f *failStartChild) CancelChild(context.Context, string, string) error { return nil }

type okChildRuns struct {
	started, finished int
	lastStatus        string
}

func (o *okChildRuns) StartChild(_ context.Context, _ ChildRunStartInput) (string, error) {
	o.started++
	return uuid.Must(uuid.NewV7()).String(), nil
}
func (o *okChildRuns) FinishChild(_ context.Context, _, _, status, _ string, _ json.RawMessage) error {
	o.finished++
	o.lastStatus = status
	return nil
}
func (o *okChildRuns) CancelChild(context.Context, string, string) error { return nil }
