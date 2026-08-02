package agentdelegation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/agentdelegation"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// memBudgetAudit is a concurrency-safe in-memory AuditWriter for budget race tests.
type memBudgetAudit struct {
	mu       sync.Mutex
	rows     map[string]agentdelegation.Delegation // idempotency key → row
	byID     map[string]string                     // del id → idem key
	attempts atomic.Int64
	creates  atomic.Int64
	failPre  bool
	failAtt  bool
}

func (m *memBudgetAudit) CreateDelegationAndStep(_ context.Context, in agentdelegation.CreateDelegationInput) (agentdelegation.Delegation, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]agentdelegation.Delegation{}
		m.byID = map[string]string{}
	}
	if existing, ok := m.rows[in.IdempotencyKey]; ok {
		return existing, true, nil
	}
	if m.failPre {
		return agentdelegation.Delegation{}, false, fmt.Errorf("prewrite failed")
	}
	m.creates.Add(1)
	d := agentdelegation.Delegation{
		ID: in.ID, Status: agentdelegation.StatusRunning, StepID: in.StepID,
		IdempotencyKey: in.IdempotencyKey, ToolCallID: in.ToolCallID,
		ErrorCode: "", AttemptCount: 0, RetryCount: 0,
	}
	m.rows[in.IdempotencyKey] = d
	m.byID[in.ID] = in.IdempotencyKey
	return d, false, nil
}
func (m *memBudgetAudit) FinalizeDelegation(_ context.Context, in agentdelegation.FinalizeDelegationInput) (agentdelegation.Delegation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.byID[in.DelegationID]
	if !ok {
		return agentdelegation.Delegation{}, agentdelegation.ErrNotFound
	}
	d := m.rows[key]
	switch d.Status {
	case agentdelegation.StatusSucceeded, agentdelegation.StatusFailed,
		agentdelegation.StatusCancelled, agentdelegation.StatusTimedOut:
		return d, nil
	}
	d.Status = in.Status
	d.ErrorCode = in.ErrorCode
	d.ErrorMessage = in.ErrorMessage
	d.OutputSummary = in.OutputSummary
	d.StepID = in.StepID
	m.rows[key] = d
	return d, nil
}
func (m *memBudgetAudit) SetChildRunID(context.Context, string, string, string) error { return nil }
func (m *memBudgetAudit) RecordDispatchAttempt(_ context.Context, _, delegationID string) error {
	if m.failAtt {
		return fmt.Errorf("attempt record failed")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.byID[delegationID]
	if !ok {
		return agentdelegation.ErrNotFound
	}
	d := m.rows[key]
	d.AttemptCount++
	if d.AttemptCount > 0 {
		d.RetryCount = d.AttemptCount - 1
	}
	m.rows[key] = d
	m.attempts.Add(1)
	return nil
}
func (m *memBudgetAudit) AccumulateModelTokens(context.Context, string, string, agentdelegation.TokenUsage) error {
	return nil
}

func (m *memBudgetAudit) snapshotRows() []agentdelegation.Delegation {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]agentdelegation.Delegation, 0, len(m.rows))
	for _, d := range m.rows {
		out = append(out, d)
	}
	return out
}

type slowInner struct {
	delay time.Duration
	calls atomic.Int64
}

func (s *slowInner) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "inner", Desc: "d"}, nil
}
func (s *slowInner) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	s.calls.Add(1)
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	return "ok", nil
}

// TestBudget_ParallelToolsNode_NeverExceedsLimits drives real Eino ToolsNode with
// ExecuteSequentially=false (default parallel) and many simultaneous AgentTool calls
// sharing one *Budget. Asserts no panic/race and hard total/per-binding caps.
func TestBudget_ParallelToolsNode_NeverExceedsLimits(t *testing.T) {
	ctx := context.Background()
	budget := agentdelegation.NewBudget()
	budget.MaxTotal = 5
	budget.MaxPerBinding = 2
	budget.MaxDepth = 4

	audit := &memBudgetAudit{}
	inner := &slowInner{delay: 5 * time.Millisecond}
	edge := agentdelegation.GraphEdgeSnapshot{
		BindingID: "bind-shared", CallableName: "call_shared", Version: 1,
		Mode: agentdelegation.ModeInline, Protocol: agentdelegation.ProtocolInternal,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
	}
	at, err := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
		Inner: inner, Name: "call_shared", Edge: edge, Audit: audit, FinalizeRetries: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// ToolsNode with default parallel execution (ExecuteSequentially=false).
	toolsNode, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{
		Tools: []tool.BaseTool{at},
		// Explicit false: production default is parallel.
		ExecuteSequentially: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	g := compose.NewGraph[*schema.Message, []*schema.Message]()
	if err := g.AddToolsNode("tools", toolsNode); err != nil {
		t.Fatal(err)
	}
	_ = g.AddEdge(compose.START, "tools")
	_ = g.AddEdge("tools", compose.END)
	runnable, err := g.Compile(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const nCalls = 20
	calls := make([]schema.ToolCall, nCalls)
	for i := 0; i < nCalls; i++ {
		calls[i] = schema.ToolCall{
			ID:   fmt.Sprintf("call_%d", i),
			Type: "function",
			Function: schema.FunctionCall{
				Name:      "call_shared",
				Arguments: `{"request":"x"}`,
			},
		}
	}
	ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	rc := &agentdelegation.RunContext{
		WorkspaceID: ws, ParentRunID: run, RootRunID: run, RunID: run,
		CallerAgentID: edge.CallerAgentID, Depth: 0, Budget: budget,
	}
	runCtx := agentdelegation.WithRunContext(ctx, rc)
	input := &schema.Message{Role: schema.Assistant, ToolCalls: calls}
	msgs, invErr := runnable.Invoke(runCtx, input)
	_ = msgs
	_ = invErr

	total, per := budget.Snapshot()
	if total > budget.MaxTotal {
		t.Fatalf("total used %d > MaxTotal %d", total, budget.MaxTotal)
	}
	if per["bind-shared"] > budget.MaxPerBinding {
		t.Fatalf("per-binding %d > MaxPerBinding %d", per["bind-shared"], budget.MaxPerBinding)
	}
	// Successful dispatches (inner calls) must equal attempts and must not exceed caps.
	if int(inner.calls.Load()) > budget.MaxTotal {
		t.Fatalf("inner calls %d > max total", inner.calls.Load())
	}
	if int(audit.attempts.Load()) > budget.MaxTotal {
		t.Fatalf("attempts %d > max total", audit.attempts.Load())
	}
	// Attempts only for real dispatch.
	if audit.attempts.Load() != inner.calls.Load() {
		t.Fatalf("attempts=%d inner=%d", audit.attempts.Load(), inner.calls.Load())
	}
	// Budget remaining reserved slots should equal committed dispatches (no leak).
	if total != int(audit.attempts.Load()) {
		t.Fatalf("budget total %d != attempts %d (leaked reservation?)", total, audit.attempts.Load())
	}

	// Every model-initiated tool call must leave exactly one success OR failure
	// AGENT_DELEGATION evidence (budget rejects write FAILED with attempt/retry 0).
	rows := audit.snapshotRows()
	if len(rows) != nCalls {
		t.Fatalf("evidence rows=%d want %d (one per model tool call)", len(rows), nCalls)
	}
	successN, failedBudgetN := 0, 0
	type auditStepJSON struct {
		Type         string `json:"type"`
		Status       string `json:"status"`
		ErrorCode    string `json:"errorCode"`
		AttemptCount *int   `json:"attemptCount"`
		RetryCount   *int   `json:"retryCount"`
	}
	var auditSteps []auditStepJSON
	for _, d := range rows {
		switch d.Status {
		case agentdelegation.StatusSucceeded:
			successN++
			if d.AttemptCount < 1 {
				t.Fatalf("succeeded row must have attempt>=1: %+v", d)
			}
			zero := d.AttemptCount
			retry := d.RetryCount
			auditSteps = append(auditSteps, auditStepJSON{
				Type: "agent_delegation", Status: d.Status, AttemptCount: &zero, RetryCount: &retry,
			})
		case agentdelegation.StatusFailed:
			failedBudgetN++
			if d.AttemptCount != 0 || d.RetryCount != 0 {
				t.Fatalf("budget fail must be 0/0: attempt=%d retry=%d", d.AttemptCount, d.RetryCount)
			}
			if !strings.Contains(d.ErrorCode, "BUDGET") && !strings.Contains(d.ErrorCode, "DEPTH") {
				t.Fatalf("failed evidence errorCode=%q", d.ErrorCode)
			}
			z0, z1 := 0, 0
			auditSteps = append(auditSteps, auditStepJSON{
				Type: "agent_delegation", Status: d.Status, ErrorCode: d.ErrorCode,
				AttemptCount: &z0, RetryCount: &z1,
			})
		default:
			t.Fatalf("unexpected status %s for toolCall=%s", d.Status, d.ToolCallID)
		}
	}
	if successN != int(inner.calls.Load()) {
		t.Fatalf("success evidence=%d inner=%d", successN, inner.calls.Load())
	}
	if failedBudgetN == 0 && nCalls > budget.MaxTotal {
		t.Fatal("expected some budget-failed evidence rows")
	}
	// agentaudit-style JSON must surface failed 0/0 frames.
	raw, err := json.Marshal(auditSteps)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"attemptCount":0`) || !strings.Contains(string(raw), `"retryCount":0`) {
		t.Fatalf("agentaudit JSON missing failed 0/0: %s", raw)
	}
	if !strings.Contains(string(raw), "BUDGET") && !strings.Contains(string(raw), "DEPTH") {
		t.Fatalf("agentaudit JSON missing budget error codes: %s", raw)
	}
	t.Logf("total=%d per=%v attempts=%d creates=%d success=%d failedBudget=%d",
		total, per, audit.attempts.Load(), audit.creates.Load(), successN, failedBudgetN)
}

// TestBudget_PreDispatchFailuresReleaseReservation ensures audit/child/attempt failures
// do not consume budget permanently, while successful dispatch does.
func TestBudget_PreDispatchFailuresReleaseReservation(t *testing.T) {
	t.Parallel()
	ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	edge := agentdelegation.GraphEdgeSnapshot{
		BindingID: "b-rel", CallableName: "c", Version: 1, Mode: agentdelegation.ModeInline,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(), TargetAgentID: uuid.Must(uuid.NewV7()).String(),
	}

	// 1) Audit prewrite failure releases.
	{
		budget := agentdelegation.NewBudget()
		budget.MaxTotal = 3
		inner := &slowInner{}
		tool, _ := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
			Inner: inner, Name: "c", Edge: edge, Audit: &memBudgetAudit{failPre: true}, FinalizeRetries: 1,
		})
		ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
			WorkspaceID: ws, ParentRunID: run, RunID: run, CallerAgentID: edge.CallerAgentID, Budget: budget,
		})
		_, _ = tool.InvokableRun(ctx, `{"request":"x"}`)
		total, _ := budget.Snapshot()
		if total != 0 {
			t.Fatalf("prewrite fail leaked reservation total=%d", total)
		}
		if inner.calls.Load() != 0 {
			t.Fatal("inner must not run")
		}
	}

	// 2) Attempt record failure releases (no consume).
	{
		budget := agentdelegation.NewBudget()
		budget.MaxTotal = 3
		inner := &slowInner{}
		tool, _ := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
			Inner: inner, Name: "c", Edge: edge, Audit: &memBudgetAudit{failAtt: true}, FinalizeRetries: 1,
		})
		ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
			WorkspaceID: ws, ParentRunID: run, RunID: run, CallerAgentID: edge.CallerAgentID, Budget: budget,
		})
		_, _ = tool.InvokableRun(ctx, `{"request":"x"}`)
		total, _ := budget.Snapshot()
		if total != 0 {
			t.Fatalf("attempt fail leaked reservation total=%d", total)
		}
		if inner.calls.Load() != 0 {
			t.Fatal("inner must not run on attempt fail")
		}
	}

	// 3) Successful dispatch keeps reservation (consumed).
	{
		budget := agentdelegation.NewBudget()
		budget.MaxTotal = 3
		audit := &memBudgetAudit{}
		inner := &slowInner{}
		tool, _ := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
			Inner: inner, Name: "c", Edge: edge, Audit: audit, FinalizeRetries: 1,
		})
		ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
			WorkspaceID: ws, ParentRunID: run, RunID: run, CallerAgentID: edge.CallerAgentID, Budget: budget,
		})
		out, err := tool.InvokableRun(ctx, `{"request":"x"}`)
		if err != nil {
			t.Fatal(err)
		}
		_ = out
		total, _ := budget.Snapshot()
		if total != 1 {
			t.Fatalf("success must consume 1 slot, total=%d", total)
		}
		if audit.attempts.Load() != 1 || inner.calls.Load() != 1 {
			t.Fatalf("attempts=%d inner=%d", audit.attempts.Load(), inner.calls.Load())
		}
	}
}

// TestBudget_ConcurrentCheckAndReserve_NoOversub uses pure goroutines (no Eino)
// as a tight race harness for CheckAndReserve/Release.
func TestBudget_ConcurrentCheckAndReserve_NoOversub(t *testing.T) {
	t.Parallel()
	b := agentdelegation.NewBudget()
	b.MaxTotal = 8
	b.MaxPerBinding = 3
	var okN, failN atomic.Int64
	var wg sync.WaitGroup
	const workers = 100
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", i%5)
			if err := b.CheckAndReserve(1, key); err != nil {
				failN.Add(1)
				return
			}
			okN.Add(1)
			// Half release (pre-dispatch fail simulation).
			if i%2 == 0 {
				b.Release(key)
				okN.Add(-1) // net: not consumed
			}
		}(i)
	}
	wg.Wait()
	total, per := b.Snapshot()
	if total > b.MaxTotal {
		t.Fatalf("total %d > max %d", total, b.MaxTotal)
	}
	for k, v := range per {
		if v > b.MaxPerBinding {
			t.Fatalf("key %s count %d > max per %d", k, v, b.MaxPerBinding)
		}
	}
	if total != int(okN.Load()) {
		// okN tracks net reserved after release of even workers.
		t.Fatalf("total=%d okN=%d failN=%d per=%v", total, okN.Load(), failN.Load(), per)
	}
}

// Ensure json import used if needed for future extensions.
var _ = json.RawMessage{}
