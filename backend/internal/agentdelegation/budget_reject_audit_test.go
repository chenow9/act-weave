package agentdelegation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// TestAuditedAgentTool_BudgetRejectWritesFailedEvidence covers total / binding / depth
// budget rejections: FAILED row+step, attempt/retry 0, stable error codes, Inner=0,
// and idempotent replay without duplicate evidence.
func TestAuditedAgentTool_BudgetRejectWritesFailedEvidence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		setup    func(b *Budget)
		wantCode string
		depth    int
	}{
		{
			name: "total",
			setup: func(b *Budget) {
				b.MaxTotal = 0
			},
			wantCode: "DELEGATION_TOTAL_BUDGET_EXCEEDED",
			depth:    0,
		},
		{
			name: "binding",
			setup: func(b *Budget) {
				b.MaxTotal = 10
				b.MaxPerBinding = 0
			},
			wantCode: "DELEGATION_BINDING_BUDGET_EXCEEDED",
			depth:    0,
		},
		{
			name: "depth",
			setup: func(b *Budget) {
				b.MaxDepth = 0
				b.MaxTotal = 10
			},
			wantCode: "DELEGATION_DEPTH_EXCEEDED",
			depth:    0, // next depth becomes 1 > MaxDepth 0
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			audit := &memAudit{}
			inner := &stubInner{result: "should-not-run"}
			edge := GraphEdgeSnapshot{
				BindingID: "bind-" + tc.name, CallableName: "c", Version: 1, Mode: ModeInline,
				CallerAgentID: uuid.Must(uuid.NewV7()).String(),
				TargetAgentID: uuid.Must(uuid.NewV7()).String(),
			}
			tool, err := NewAuditedAgentTool(AgentToolConfig{
				Inner: inner, Name: "c", Edge: edge, Audit: audit, FinalizeRetries: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			budget := NewBudget()
			tc.setup(budget)
			ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
			ctx := WithRunContext(context.Background(), &RunContext{
				WorkspaceID: ws, ParentRunID: run, RunID: run, RootRunID: run,
				CallerAgentID: edge.CallerAgentID, Depth: tc.depth, Budget: budget,
			})
			out, err := tool.InvokableRun(ctx, `{"request":"x"}`)
			if err != nil {
				t.Fatal(err)
			}
			if inner.calls != 0 {
				t.Fatalf("Inner must not run on budget reject, calls=%d", inner.calls)
			}
			code, _ := parseDelegationErrorJSON(t, out)
			if code != tc.wantCode {
				t.Fatalf("tool errorCode=%s want %s out=%s", code, tc.wantCode, out)
			}
			// No budget slot consumed.
			total, _ := budget.Snapshot()
			if total != 0 {
				t.Fatalf("budget total=%d want 0 (no reservation)", total)
			}
			func() {
				audit.mu.Lock()
				defer audit.mu.Unlock()
				if len(audit.rows) != 1 {
					t.Fatalf("want 1 FAILED evidence row, got %d", len(audit.rows))
				}
				for _, d := range audit.rows {
					if d.Status != StatusFailed {
						t.Fatalf("status=%s want FAILED", d.Status)
					}
					if d.ErrorCode != tc.wantCode {
						t.Fatalf("row errorCode=%s want %s", d.ErrorCode, tc.wantCode)
					}
					if d.AttemptCount != 0 || d.RetryCount != 0 {
						t.Fatalf("attempt=%d retry=%d want 0/0", d.AttemptCount, d.RetryCount)
					}
					if d.StepID == "" {
						t.Fatal("missing step id")
					}
				}
			}()

			// Idempotent replay: same tool call id / binding → still one row.
			// Must not hold audit.mu across InvokableRun (CreateDelegationAndStep locks it).
			out2, err := tool.InvokableRun(ctx, `{"request":"x"}`)
			if err != nil {
				t.Fatal(err)
			}
			code2, _ := parseDelegationErrorJSON(t, out2)
			if code2 != tc.wantCode {
				t.Fatalf("replay code=%s", code2)
			}
			if inner.calls != 0 {
				t.Fatal("replay must not call Inner")
			}
			audit.mu.Lock()
			n := len(audit.rows)
			audit.mu.Unlock()
			if n != 1 {
				t.Fatalf("idempotent replay created duplicate evidence: %d rows", n)
			}
		})
	}
}

// TestAuditedAgentTool_BudgetReject_NoContextNoAudit: missing workspace/run/caller
// must not invent audit rows (cannot scope tenant-safe evidence).
func TestAuditedAgentTool_BudgetReject_NoContextNoAudit(t *testing.T) {
	t.Parallel()
	audit := &memAudit{}
	inner := &stubInner{result: "x"}
	edge := GraphEdgeSnapshot{BindingID: "b", CallableName: "c", Version: 1, Mode: ModeInline,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(), TargetAgentID: uuid.Must(uuid.NewV7()).String()}
	tool, err := NewAuditedAgentTool(AgentToolConfig{Inner: inner, Name: "c", Edge: edge, Audit: audit, FinalizeRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	budget := NewBudget()
	budget.MaxTotal = 0
	// Budget present but no workspace/run/caller on context.
	ctx := WithRunContext(context.Background(), &RunContext{Budget: budget})
	out, err := tool.InvokableRun(ctx, `{"request":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls != 0 {
		t.Fatal("inner")
	}
	code, _ := parseDelegationErrorJSON(t, out)
	if code != "DELEGATION_TOTAL_BUDGET_EXCEEDED" {
		t.Fatalf("code=%s", code)
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if len(audit.rows) != 0 {
		t.Fatalf("must not forge audit without tenant context, rows=%d", len(audit.rows))
	}
}

func TestAuditedAgentTool_BudgetBlocksDispatch_HasEvidence(t *testing.T) {
	// Supersedes the weak BudgetBlocksDispatch: also require FAILED evidence.
	t.Parallel()
	audit := &memAudit{}
	inner := &stubInner{result: "x"}
	edge := GraphEdgeSnapshot{BindingID: "b1", CallableName: "c", Version: 1, Mode: ModeInline,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(), TargetAgentID: uuid.Must(uuid.NewV7()).String()}
	tool, err := NewAuditedAgentTool(AgentToolConfig{Inner: inner, Name: "c", Edge: edge, Audit: audit, FinalizeRetries: 1})
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
	if parsed["ok"] != false || parsed["errorCode"] != "DELEGATION_TOTAL_BUDGET_EXCEEDED" {
		t.Fatalf("out=%s", out)
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if len(audit.rows) != 1 {
		t.Fatalf("rows=%d", len(audit.rows))
	}
	for _, d := range audit.rows {
		if d.Status != StatusFailed || d.ErrorCode != "DELEGATION_TOTAL_BUDGET_EXCEEDED" {
			t.Fatalf("%+v", d)
		}
		if d.AttemptCount != 0 {
			t.Fatalf("attempt=%d", d.AttemptCount)
		}
	}
}

// TestBudgetReject_ConcurrentReplayDoesNotFinalizeRunningFirstCall:
// first call same toolCallID has RecordDispatchAttempt and Inner blocked;
// second same-key call is budget-rejected; must not finalize RUNNING, not call Inner,
// return IDEMPOTENT_REPLAY; first then succeeds with unique SUCCEEDED attempt=1.
func TestBudgetReject_ConcurrentReplayDoesNotFinalizeRunningFirstCall(t *testing.T) {
	t.Parallel()
	audit := &memAudit{}
	entered := make(chan struct{})
	release := make(chan struct{})
	var innerCalls atomic.Int64
	inner := &gateInner{
		entered: entered, release: release, result: `{"answer":"first-ok"}`,
		calls: &innerCalls,
	}
	edge := GraphEdgeSnapshot{
		BindingID: "bind-race", CallableName: "c", Version: 1, Mode: ModeInline,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "c", Edge: edge, Audit: audit, FinalizeRetries: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// MaxTotal=1: first consumes the only slot after dispatch attempt; second reserve fails.
	budget := NewBudget()
	budget.MaxTotal = 1
	ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	// Same missing-tool-call-id → same idempotency key for both InvokableRun calls.
	rc := &RunContext{
		WorkspaceID: ws, ParentRunID: run, RunID: run, RootRunID: run,
		CallerAgentID: edge.CallerAgentID, Depth: 0, Budget: budget,
	}
	ctx := WithRunContext(context.Background(), rc)

	firstOut := make(chan string, 1)
	firstErr := make(chan error, 1)
	go func() {
		out, err := tool.InvokableRun(ctx, `{"request":"first"}`)
		firstOut <- out
		firstErr <- err
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first Inner did not enter")
	}
	// First has reserved + RecordDispatchAttempt; still RUNNING mid-Inner.
	audit.mu.Lock()
	if len(audit.rows) != 1 {
		audit.mu.Unlock()
		t.Fatalf("want 1 row after first prewrite, got %d", len(audit.rows))
	}
	var firstStatus string
	var firstAttempts int
	for _, d := range audit.rows {
		firstStatus = d.Status
		firstAttempts = d.AttemptCount
	}
	audit.mu.Unlock()
	if firstStatus != StatusRunning {
		t.Fatalf("first status=%s want RUNNING", firstStatus)
	}
	if firstAttempts != 1 {
		t.Fatalf("first attempt=%d want 1", firstAttempts)
	}

	// Second: budget rejects, Create replays RUNNING — must not finalize, not Inner.
	out2, err2 := tool.InvokableRun(ctx, `{"request":"second"}`)
	if err2 != nil {
		t.Fatal(err2)
	}
	code2, _ := parseDelegationErrorJSON(t, out2)
	if code2 != "DELEGATION_IDEMPOTENT_REPLAY" {
		t.Fatalf("second code=%s want DELEGATION_IDEMPOTENT_REPLAY out=%s", code2, out2)
	}
	if strings.Contains(out2, "BUDGET") {
		t.Fatalf("second must not rebrand as budget reject: %s", out2)
	}
	if innerCalls.Load() != 1 {
		t.Fatalf("Inner calls=%d want 1 (second must not dispatch)", innerCalls.Load())
	}
	audit.mu.Lock()
	if len(audit.rows) != 1 {
		audit.mu.Unlock()
		t.Fatalf("second must not add rows: %d", len(audit.rows))
	}
	for _, d := range audit.rows {
		if d.Status != StatusRunning {
			audit.mu.Unlock()
			t.Fatalf("second must not finalize RUNNING → %s", d.Status)
		}
	}
	audit.mu.Unlock()

	close(release)
	select {
	case out := <-firstOut:
		if err := <-firstErr; err != nil {
			t.Fatal(err)
		}
		if out != `{"answer":"first-ok"}` {
			t.Fatalf("first out=%s", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first did not complete")
	}

	audit.mu.Lock()
	defer audit.mu.Unlock()
	if len(audit.rows) != 1 {
		t.Fatalf("final rows=%d", len(audit.rows))
	}
	for _, d := range audit.rows {
		if d.Status != StatusSucceeded {
			t.Fatalf("final status=%s want SUCCEEDED (no terminal conflict)", d.Status)
		}
		if d.AttemptCount != 1 {
			t.Fatalf("final attempt=%d want 1", d.AttemptCount)
		}
	}
}

// TestBudgetReject_ReplaySucceededReturnsStoredResult: budget full but idempotent
// key already SUCCEEDED → return stored result, not budget error.
func TestBudgetReject_ReplaySucceededReturnsStoredResult(t *testing.T) {
	t.Parallel()
	key := IdempotencyKey("run-s", "missing-tool-call-id", 1, "bind-s")
	audit := &memAudit{rows: map[string]Delegation{
		key: {
			ID: "d-ok", Status: StatusSucceeded, IdempotencyKey: key, StepID: "s1",
			OutputPayload: json.RawMessage(`{"result":"stored-prior"}`),
			AttemptCount:  1,
		},
	}}
	inner := &stubInner{result: "must-not-run"}
	edge := GraphEdgeSnapshot{
		BindingID: "bind-s", CallableName: "c", Version: 1, Mode: ModeInline,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "c", Edge: edge, Audit: audit, FinalizeRetries: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	budget := NewBudget()
	budget.MaxTotal = 0 // reserve always fails → budget reject path
	ctx := WithRunContext(context.Background(), &RunContext{
		WorkspaceID: uuid.Must(uuid.NewV7()).String(), ParentRunID: "run-s", RunID: "run-s",
		CallerAgentID: edge.CallerAgentID, Budget: budget,
	})
	out, err := tool.InvokableRun(ctx, `{"request":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "stored-prior" {
		t.Fatalf("want stored result, got %q", out)
	}
	if inner.calls != 0 {
		t.Fatal("Inner must not run")
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if len(audit.rows) != 1 || audit.rows[key].Status != StatusSucceeded {
		t.Fatalf("must not rewrite: %+v", audit.rows)
	}
}

// TestBudgetReject_AuditPrewriteFailureReturnsAuditCode: Create fails → AUDIT_PREWRITE, not budget claim.
func TestBudgetReject_AuditPrewriteFailureReturnsAuditCode(t *testing.T) {
	t.Parallel()
	audit := &failCreateAudit{}
	inner := &stubInner{result: "x"}
	edge := GraphEdgeSnapshot{
		BindingID: "b-pre", CallableName: "c", Version: 1, Mode: ModeInline,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "c", Edge: edge, Audit: audit, FinalizeRetries: 1,
	})
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
	code, msg := parseDelegationErrorJSON(t, out)
	if code != "DELEGATION_AUDIT_PREWRITE_FAILED" {
		t.Fatalf("code=%s want DELEGATION_AUDIT_PREWRITE_FAILED out=%s", code, out)
	}
	if !strings.Contains(msg, "budget") && !strings.Contains(strings.ToLower(msg), "prewrite") {
		// message should retain causality (budget context and/or prewrite failure)
		t.Logf("msg=%s", msg)
	}
	if inner.calls != 0 {
		t.Fatal("Inner")
	}
}

// TestBudgetReject_FinalizeFailureSurfacedInResponse: new budget-reject row finalize fails → not silent.
func TestBudgetReject_FinalizeFailureSurfacedInResponse(t *testing.T) {
	t.Parallel()
	audit := &failFinalizeAfterCreateAudit{}
	inner := &stubInner{result: "x"}
	edge := GraphEdgeSnapshot{
		BindingID: "b-fin", CallableName: "c", Version: 1, Mode: ModeInline,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: inner, Name: "c", Edge: edge, Audit: audit, FinalizeRetries: 1,
	})
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
	code, msg := parseDelegationErrorJSON(t, out)
	// Stable budget code retained via errors.Join; message must mention finalize.
	if code != "DELEGATION_TOTAL_BUDGET_EXCEEDED" {
		t.Fatalf("code=%s want budget code retained out=%s", code, out)
	}
	if !strings.Contains(strings.ToLower(msg), "finalize") {
		t.Fatalf("message must include finalize causality: %s", msg)
	}
	if inner.calls != 0 {
		t.Fatal("Inner")
	}
}

type gateInner struct {
	entered chan struct{}
	release chan struct{}
	result  string
	calls   *atomic.Int64
	once    sync.Once
}

func (g *gateInner) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "gate", Desc: "d"}, nil
}
func (g *gateInner) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	g.calls.Add(1)
	g.once.Do(func() { close(g.entered) })
	<-g.release
	return g.result, nil
}

type failCreateAudit struct{}

func (f *failCreateAudit) CreateDelegationAndStep(context.Context, CreateDelegationInput) (Delegation, bool, error) {
	return Delegation{}, false, fmt.Errorf("db down")
}
func (f *failCreateAudit) FinalizeDelegation(context.Context, FinalizeDelegationInput) (Delegation, error) {
	return Delegation{}, errors.New("unexpected finalize")
}
func (f *failCreateAudit) SetChildRunID(context.Context, string, string, string) error { return nil }
func (f *failCreateAudit) RecordDispatchAttempt(context.Context, string, string) error {
	return nil
}
func (f *failCreateAudit) AccumulateModelTokens(context.Context, string, string, TokenUsage) error {
	return nil
}

// failFinalizeAfterCreateAudit creates RUNNING rows but always fails FinalizeDelegation.
type failFinalizeAfterCreateAudit struct {
	mu   sync.Mutex
	rows map[string]Delegation
}

func (f *failFinalizeAfterCreateAudit) CreateDelegationAndStep(_ context.Context, in CreateDelegationInput) (Delegation, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows == nil {
		f.rows = map[string]Delegation{}
	}
	if existing, ok := f.rows[in.IdempotencyKey]; ok {
		return existing, true, nil
	}
	d := Delegation{
		ID: in.ID, Status: StatusRunning, StepID: in.StepID, IdempotencyKey: in.IdempotencyKey,
	}
	f.rows[in.IdempotencyKey] = d
	return d, false, nil
}
func (f *failFinalizeAfterCreateAudit) FinalizeDelegation(context.Context, FinalizeDelegationInput) (Delegation, error) {
	return Delegation{}, fmt.Errorf("finalize backend unavailable")
}
func (f *failFinalizeAfterCreateAudit) SetChildRunID(context.Context, string, string, string) error {
	return nil
}
func (f *failFinalizeAfterCreateAudit) RecordDispatchAttempt(context.Context, string, string) error {
	return nil
}
func (f *failFinalizeAfterCreateAudit) AccumulateModelTokens(context.Context, string, string, TokenUsage) error {
	return nil
}
