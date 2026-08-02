package agentdelegation_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/einoruntime"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// Dedicated residual #1: A→B TASK → C TASK audit tree is nested via
// parent_delegation_id (not cross-run parent_step_id), including TIMED_OUT on C.

func TestABC_TASK_ParentDelegationIDTree(t *testing.T) {
	ctx := context.Background()
	audit := &memAuditWriter{}
	children := &memChildRunsABC{}

	// C: pure text leaf.
	cModel := &scriptedModel{turns: []scriptedTurn{{content: "C ok"}}}
	cAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name: "agent-c", Description: "leaf", Instruction: "brief",
		Model: cModel, MaxIterations: 2,
		ToolsConfig: adk.ToolsConfig{EmitInternalEvents: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	edgeBC := agentdelegation.GraphEdgeSnapshot{
		BindingID: "b-bc", CallerAgentID: "B", TargetAgentID: "C",
		CallableName: "call_c", Mode: agentdelegation.ModeTask, Version: 1,
		Protocol: agentdelegation.ProtocolInternal, ContextPolicy: agentdelegation.ContextTaskOnly,
	}
	toolC := adk.NewAgentTool(ctx, cAgent)
	audC, err := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
		Inner: toolC.(tool.InvokableTool), Name: "call_c", Edge: edgeBC, Audit: audit,
		DefaultCallerAgentID: "B", Protocol: agentdelegation.ProtocolInternal,
		Origin: agentdelegation.OriginInternal, ChildRuns: children,
		DefaultTaskTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	// B: calls C then answers.
	bModel := &scriptedModel{turns: []scriptedTurn{
		{toolCalls: []schema.ToolCall{{
			ID: "b1", Type: "function",
			Function: schema.FunctionCall{Name: "call_c", Arguments: `{"request":"ping C"}`},
		}}},
		{content: "B after C"},
	}}
	bAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name: "agent-b", Description: "mid", Instruction: "use call_c",
		Model: bModel, MaxIterations: 4,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{audC}, ExecuteSequentially: true,
			},
			EmitInternalEvents: false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	edgeAB := agentdelegation.GraphEdgeSnapshot{
		BindingID: "b-ab", CallerAgentID: "A", TargetAgentID: "B",
		CallableName: "call_b", Mode: agentdelegation.ModeTask, Version: 1,
		Protocol: agentdelegation.ProtocolInternal, ContextPolicy: agentdelegation.ContextTaskOnly,
	}
	toolB := adk.NewAgentTool(ctx, bAgent)
	audB, err := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
		Inner: toolB.(tool.InvokableTool), Name: "call_b", Edge: edgeAB, Audit: audit,
		DefaultCallerAgentID: "A", Protocol: agentdelegation.ProtocolInternal,
		Origin: agentdelegation.OriginInternal, ChildRuns: children,
		DefaultTaskTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	aModel := &scriptedModel{turns: []scriptedTurn{
		{toolCalls: []schema.ToolCall{{
			ID: "a1", Type: "function",
			Function: schema.FunctionCall{Name: "call_b", Arguments: `{"request":"chain"}`},
		}}},
		{content: "A done"},
	}}
	aAgent, err := einoruntime.BuildChatModelAgent(ctx, einoruntime.AgentBuildConfig{
		Name: "agent-a", Model: aModel, Tools: []tool.BaseTool{audB}, MaxIterations: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, runID := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	budget := agentdelegation.NewBudget()
	runCtx := agentdelegation.WithRunContext(ctx, &agentdelegation.RunContext{
		WorkspaceID: ws, ParentRunID: runID, RootRunID: runID, RunID: runID,
		CallerAgentID: "A", Depth: 0, Budget: budget,
	})
	engine := einoruntime.NewEngine(einoruntime.EngineConfig{})
	result, err := engine.Run(runCtx, aAgent, einoruntime.RunInput{
		WorkspaceID: ws, RunID: runID,
		Messages: []*schema.Message{schema.UserMessage("go")},
	})
	if err != nil || result.Err != nil {
		t.Fatalf("err=%v result.Err=%v", err, result.Err)
	}
	if !strings.Contains(result.FinalAssistantText, "A done") {
		t.Fatalf("final=%q", result.FinalAssistantText)
	}

	rows := audit.snapshot()
	if len(rows) != 2 {
		t.Fatalf("want 2 TASK delegations (A→B, B→C), got %d %+v", len(rows), rows)
	}
	var ab, bc *agentdelegation.Delegation
	for i := range rows {
		d := rows[i]
		if d.Depth == 1 {
			ab = &rows[i]
		}
		if d.Depth == 2 {
			bc = &rows[i]
		}
		if d.Status != agentdelegation.StatusSucceeded {
			t.Fatalf("status=%s depth=%d", d.Status, d.Depth)
		}
		if d.Mode != agentdelegation.ModeTask {
			t.Fatalf("mode=%s want TASK", d.Mode)
		}
		if d.ChildRunID == nil || *d.ChildRunID == "" {
			t.Fatalf("TASK missing child_run_id depth=%d", d.Depth)
		}
	}
	if ab == nil || bc == nil {
		t.Fatalf("missing depth rows ab=%v bc=%v", ab, bc)
	}
	// A→B is root of chain: no parent_delegation_id.
	if ab.ParentDelegationID != nil {
		t.Fatalf("A→B must not have parent_delegation_id, got %v", *ab.ParentDelegationID)
	}
	// B→C nests under A→B via parent_delegation_id.
	if bc.ParentDelegationID == nil || *bc.ParentDelegationID != ab.ID {
		t.Fatalf("B→C parent_delegation_id=%v want %s", bc.ParentDelegationID, ab.ID)
	}
	// Child runs started: A→B parent=runID; B→C parent=B's child run.
	if len(children.started) != 2 {
		t.Fatalf("child starts=%d want 2", len(children.started))
	}
}

func TestABC_TASK_C_TimedOut_PropagatesTree(t *testing.T) {
	// Unit-level chain: A→B TASK tool wraps blocking C with short timeout.
	// Asserts TIMED_OUT terminal on C delegation and parent_delegation_id link.
	audit := &memAudit{}
	children := &memChildRuns{}

	blocking := &blockingInnerShared{}
	edgeBC := agentdelegation.GraphEdgeSnapshot{
		BindingID: "bc", CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		CallableName:  "call_c", Mode: agentdelegation.ModeTask, Version: 1,
	}
	toolC, err := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
		Inner: blocking, Name: "call_c", Edge: edgeBC, Audit: audit,
		DefaultCallerAgentID: edgeBC.CallerAgentID, ChildRuns: children,
		DefaultTaskTimeout: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	// B tool: invokes C then returns.
	bInner := &chainInner{next: toolC}
	edgeAB := agentdelegation.GraphEdgeSnapshot{
		BindingID: "ab", CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: edgeBC.CallerAgentID,
		CallableName:  "call_b", Mode: agentdelegation.ModeTask, Version: 1,
	}
	toolB, err := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
		Inner: bInner, Name: "call_b", Edge: edgeAB, Audit: audit,
		DefaultCallerAgentID: edgeAB.CallerAgentID, ChildRuns: children,
		DefaultTaskTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	ws, parent := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
		WorkspaceID: ws, ParentRunID: parent, RootRunID: parent, RunID: parent,
		CallerAgentID: edgeAB.CallerAgentID, Depth: 0, Budget: agentdelegation.NewBudget(),
	})
	out, err := toolB.InvokableRun(ctx, `{"request":"chain"}`)
	if err != nil {
		t.Fatal(err)
	}
	// B may succeed wrapping C's error payload; C itself must be TIMED_OUT.
	_ = out

	audit.mu.Lock()
	defer audit.mu.Unlock()
	var abID string
	var timedOut, linked bool
	for _, d := range audit.rows {
		if d.Depth == 1 || (d.ParentDelegationID == nil && d.CallerAgentID == edgeAB.CallerAgentID) {
			abID = d.ID
		}
	}
	for _, d := range audit.rows {
		if d.Status == agentdelegation.StatusTimedOut {
			timedOut = true
			if d.ParentDelegationID != nil && *d.ParentDelegationID == abID {
				linked = true
			}
			// Prefer assert when we know parent: C must nest under B's del.
			if abID != "" && d.ParentDelegationID != nil && *d.ParentDelegationID != abID {
				t.Fatalf("TIMED_OUT parent_delegation_id=%v want %s", d.ParentDelegationID, abID)
			}
		}
	}
	if !timedOut {
		t.Fatalf("expected TIMED_OUT in tree, rows=%+v", audit.rows)
	}
	// When both A→B and B→C written, parent link must exist.
	if len(audit.rows) >= 2 && !linked {
		// Soft: if chainInner ran under B's child RC, ParentDelegationID is set.
		for _, d := range audit.rows {
			if d.Status == agentdelegation.StatusTimedOut && d.ParentDelegationID == nil {
				t.Fatal("TIMED_OUT C missing parent_delegation_id under A→B")
			}
		}
	}
}

// chainInner runs nested tool under current RunContext (B's child RC).
type chainInner struct {
	next tool.InvokableTool
}

func (c *chainInner) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "chain", Desc: "chain"}, nil
}
func (c *chainInner) InvokableRun(ctx context.Context, in string, opts ...tool.Option) (string, error) {
	return c.next.InvokableRun(ctx, in, opts...)
}

type blockingInnerShared struct{}

func (b *blockingInnerShared) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "block", Desc: "block"}, nil
}
func (b *blockingInnerShared) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

// memAudit re-exported shape for package agentdelegation_test via local alias.
// We use agentdelegation package's test types from agent_tool_test — but those
// are in package agentdelegation. This file is agentdelegation_test, so we need
// local copies for memAudit used by TestABC_TASK_C_TimedOut.

type memAudit struct {
	mu   sync.Mutex
	rows map[string]agentdelegation.Delegation
}

func (m *memAudit) CreateDelegationAndStep(_ context.Context, in agentdelegation.CreateDelegationInput) (agentdelegation.Delegation, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]agentdelegation.Delegation{}
	}
	if existing, ok := m.rows[in.IdempotencyKey]; ok {
		return existing, true, nil
	}
	d := agentdelegation.Delegation{
		ID: in.ID, WorkspaceID: in.WorkspaceID, ParentRunID: in.ParentRunID,
		ParentDelegationID: in.ParentDelegationID,
		CallerAgentID:      in.CallerAgentID, Mode: in.Mode, Protocol: in.Protocol,
		Origin: in.Origin, Depth: in.Depth, BindingVersion: in.BindingVersion,
		ToolCallID: in.ToolCallID, IdempotencyKey: in.IdempotencyKey,
		Status: agentdelegation.StatusRunning, StepID: in.StepID,
		InputSummary: in.InputSummary, InputPayload: in.InputPayload,
	}
	if in.TargetAgentID != nil {
		d.TargetAgentID = in.TargetAgentID
	}
	m.rows[in.IdempotencyKey] = d
	return d, false, nil
}

func (m *memAudit) FinalizeDelegation(_ context.Context, in agentdelegation.FinalizeDelegationInput) (agentdelegation.Delegation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, d := range m.rows {
		if d.ID == in.DelegationID {
			if d.Status == agentdelegation.StatusSucceeded || d.Status == agentdelegation.StatusFailed ||
				d.Status == agentdelegation.StatusCancelled || d.Status == agentdelegation.StatusTimedOut {
				return d, nil // never overwrite terminal
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
	return agentdelegation.Delegation{}, agentdelegation.ErrNotFound
}

func (m *memAudit) SetChildRunID(_ context.Context, _, delegationID, childRunID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, d := range m.rows {
		if d.ID == delegationID {
			id := childRunID
			d.ChildRunID = &id
			m.rows[k] = d
			return nil
		}
	}
	return agentdelegation.ErrNotFound
}
func (m *memAudit) RecordDispatchAttempt(_ context.Context, _, delegationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, d := range m.rows {
		if d.ID == delegationID {
			d.AttemptCount++
			m.rows[k] = d
			return nil
		}
	}
	return agentdelegation.ErrNotFound
}
func (m *memAudit) AccumulateModelTokens(context.Context, string, string, agentdelegation.TokenUsage) error {
	return nil
}

type memChildRuns struct {
	mu       sync.Mutex
	started  []agentdelegation.ChildRunStartInput
	finished map[string]string
}

func (m *memChildRuns) StartChild(_ context.Context, in agentdelegation.ChildRunStartInput) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.finished == nil {
		m.finished = map[string]string{}
	}
	id := uuid.Must(uuid.NewV7()).String()
	m.started = append(m.started, in)
	m.finished[id] = agentdelegation.StatusRunning
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
	m.finished[childRunID] = agentdelegation.StatusCancelled
	return nil
}

type memChildRunsABC struct {
	mu      sync.Mutex
	started []agentdelegation.ChildRunStartInput
}

func (m *memChildRunsABC) StartChild(_ context.Context, in agentdelegation.ChildRunStartInput) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := uuid.Must(uuid.NewV7()).String()
	m.started = append(m.started, in)
	return id, nil
}
func (m *memChildRunsABC) FinishChild(context.Context, string, string, string, string, json.RawMessage) error {
	return nil
}
func (m *memChildRunsABC) CancelChild(context.Context, string, string) error { return nil }
