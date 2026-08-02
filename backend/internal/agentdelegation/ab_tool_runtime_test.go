package agentdelegation_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/einoruntime"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// Offline A→B→tool scenario: parent model calls AgentTool "call_b";
// child model calls a real TOOL; audit prewrite/finalize; parent final text
// is only the parent's synthesis (child tool text does not leak as parent stream).

func TestAB_TextAndToolDelegation_AgentToolPath(t *testing.T) {
	ctx := context.Background()
	audit := &memAuditWriter{}

	// Child tool: records invocation (B's TOOL).
	childToolCalls := 0
	childTool := &stubInvokable{
		name: "lookup_sku",
		desc: "lookup product",
		run: func(_ context.Context, args string) (string, error) {
			childToolCalls++
			return `{"ok":true,"sku":"SKU-1","qty":2}`, nil
		},
	}

	// Child agent B: first turn tool call, second turn final text.
	childModel := &scriptedModel{turns: []scriptedTurn{
		{toolCalls: []schema.ToolCall{{
			ID: "c1", Type: "function",
			Function: schema.FunctionCall{Name: "lookup_sku", Arguments: `{"sku":"SKU-1"}`},
		}}},
		{content: "Found SKU-1 qty=2"},
	}}
	childAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name: "agent-b", Description: "helper that uses tools",
		Instruction: "You are B. Use lookup_sku then answer.",
		Model:       childModel, MaxIterations: 4,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{childTool}, ExecuteSequentially: true,
			},
			EmitInternalEvents: false,
		},
	})
	if err != nil {
		t.Fatalf("child agent: %v", err)
	}

	edge := agentdelegation.GraphEdgeSnapshot{
		BindingID:     uuid.Must(uuid.NewV7()).String(),
		CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		CallableName:  "call_b", Description: "delegate to B",
		Mode: agentdelegation.ModeInline, ContextPolicy: agentdelegation.ContextTaskOnly,
		Version: 1, Protocol: agentdelegation.ProtocolInternal,
	}
	agentTool := adk.NewAgentTool(ctx, childAgent)
	inv, ok := agentTool.(tool.InvokableTool)
	if !ok {
		t.Fatal("NewAgentTool not invokable")
	}
	audited, err := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
		Inner: inv, Name: "call_b", Description: edge.Description,
		Edge: edge, Audit: audit, DefaultCallerAgentID: edge.CallerAgentID,
		Protocol: agentdelegation.ProtocolInternal, Origin: agentdelegation.OriginInternal,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Parent A: first turn call call_b, second turn synthesize.
	parentModel := &scriptedModel{turns: []scriptedTurn{
		{toolCalls: []schema.ToolCall{{
			ID: "p1", Type: "function",
			Function: schema.FunctionCall{Name: "call_b", Arguments: `{"request":"lookup SKU-1"}`},
		}}},
		{content: "Root summary: B reported SKU-1 available."},
	}}
	parent, err := einoruntime.BuildChatModelAgent(ctx, einoruntime.AgentBuildConfig{
		Name: "agent-a", Instruction: "You are A. Delegate via call_b.",
		Model: parentModel, Tools: []tool.BaseTool{audited},
		MaxIterations: 4, MaxToolInvocations: 8,
	})
	if err != nil {
		t.Fatalf("parent: %v", err)
	}

	ws := uuid.Must(uuid.NewV7()).String()
	runID := uuid.Must(uuid.NewV7()).String()
	budget := agentdelegation.NewBudget()
	runCtx := agentdelegation.WithRunContext(ctx, &agentdelegation.RunContext{
		WorkspaceID: ws, ParentRunID: runID, RootRunID: runID,
		CallerAgentID: edge.CallerAgentID, Depth: 0, Budget: budget,
	})

	engine := einoruntime.NewEngine(einoruntime.EngineConfig{})
	result, err := engine.Run(runCtx, parent, einoruntime.RunInput{
		WorkspaceID: ws, RunID: runID,
		Messages: []*schema.Message{schema.UserMessage("please check SKU-1 via helper")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Interrupted || result.Err != nil {
		t.Fatalf("interrupted=%v err=%v", result.Interrupted, result.Err)
	}
	if childToolCalls != 1 {
		t.Fatalf("child tool calls=%d want 1", childToolCalls)
	}
	final := strings.TrimSpace(result.FinalAssistantText)
	if !strings.Contains(final, "Root summary") {
		t.Fatalf("parent final missing synthesis: %q", final)
	}
	// Child raw tool JSON must not be the parent final output.
	if strings.Contains(final, `"sku":"SKU-1"`) && !strings.Contains(final, "Root summary") {
		t.Fatalf("child tool payload leaked as parent final: %q", final)
	}

	// Audit: one SUCCESSFUL INTERNAL INLINE delegation on parent chain.
	// AGENT_DELEGATION is attributed to caller A; toolCallID comes from Eino ToolsNode (p1).
	rows := audit.snapshot()
	if len(rows) != 1 {
		t.Fatalf("audit rows=%d want 1: %+v", len(rows), rows)
	}
	for _, d := range rows {
		if d.Status != agentdelegation.StatusSucceeded {
			t.Fatalf("delegation status=%s", d.Status)
		}
		if d.Mode != agentdelegation.ModeInline || d.Protocol != agentdelegation.ProtocolInternal {
			t.Fatalf("mode/protocol = %s/%s", d.Mode, d.Protocol)
		}
		if d.Depth != 1 {
			t.Fatalf("depth=%d", d.Depth)
		}
		if d.CallerAgentID != edge.CallerAgentID {
			t.Fatalf("caller=%s want A", d.CallerAgentID)
		}
		if d.ParentRunID != runID {
			t.Fatalf("parent_run_id=%s want %s", d.ParentRunID, runID)
		}
		// Stable Eino tool call id from parent model turn (not a random UUID).
		if d.ToolCallID != "p1" {
			t.Fatalf("tool_call_id=%q want p1 (Eino ToolsNode id)", d.ToolCallID)
		}
		if d.StepID == "" {
			t.Fatal("missing step_id on AGENT_DELEGATION")
		}
	}
	if total, _ := budget.Snapshot(); total != 1 {
		t.Fatalf("budget total=%d", total)
	}
}

func TestAB_NestedABC_BudgetAndAudit(t *testing.T) {
	ctx := context.Background()
	audit := &memAuditWriter{}

	// C: pure text
	cModel := &scriptedModel{turns: []scriptedTurn{{content: "C says hi"}}}
	cAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name: "agent-c", Description: "leaf", Instruction: "be brief",
		Model: cModel, MaxIterations: 2,
		ToolsConfig: adk.ToolsConfig{EmitInternalEvents: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	edgeBC := agentdelegation.GraphEdgeSnapshot{
		BindingID: "b-bc", CallerAgentID: "B", TargetAgentID: "C",
		CallableName: "call_c", Mode: agentdelegation.ModeInline, Version: 1,
		Protocol: agentdelegation.ProtocolInternal,
	}
	toolC := adk.NewAgentTool(ctx, cAgent)
	audC, err := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
		Inner: toolC.(tool.InvokableTool), Name: "call_c", Edge: edgeBC, Audit: audit,
		DefaultCallerAgentID: "B", Protocol: agentdelegation.ProtocolInternal, Origin: agentdelegation.OriginInternal,
	})
	if err != nil {
		t.Fatal(err)
	}

	// B: calls C then answers
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
		CallableName: "call_b", Mode: agentdelegation.ModeInline, Version: 1,
		Protocol: agentdelegation.ProtocolInternal,
	}
	toolB := adk.NewAgentTool(ctx, bAgent)
	audB, err := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
		Inner: toolB.(tool.InvokableTool), Name: "call_b", Edge: edgeAB, Audit: audit,
		DefaultCallerAgentID: "A", Protocol: agentdelegation.ProtocolInternal, Origin: agentdelegation.OriginInternal,
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
		WorkspaceID: ws, ParentRunID: runID, RootRunID: runID,
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
	if result.FinalAssistantText != "A done" {
		t.Fatalf("final=%q", result.FinalAssistantText)
	}
	rows := audit.snapshot()
	// A→B and B→C
	if len(rows) != 2 {
		t.Fatalf("want 2 delegations, got %d %+v", len(rows), rows)
	}
	depths := map[int]int{}
	for _, d := range rows {
		if d.Status != agentdelegation.StatusSucceeded {
			t.Fatalf("status=%s", d.Status)
		}
		depths[d.Depth]++
	}
	if depths[1] != 1 || depths[2] != 1 {
		t.Fatalf("depths=%v", depths)
	}
}

// --- test doubles ---

type memAuditWriter struct {
	mu   sync.Mutex
	rows map[string]agentdelegation.Delegation
}

func (m *memAuditWriter) CreateDelegationAndStep(_ context.Context, in agentdelegation.CreateDelegationInput) (agentdelegation.Delegation, bool, error) {
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

func (m *memAuditWriter) FinalizeDelegation(_ context.Context, in agentdelegation.FinalizeDelegationInput) (agentdelegation.Delegation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, d := range m.rows {
		if d.ID == in.DelegationID {
			if d.Status == agentdelegation.StatusSucceeded || d.Status == agentdelegation.StatusFailed ||
				d.Status == agentdelegation.StatusCancelled || d.Status == agentdelegation.StatusTimedOut {
				return d, nil
			}
			d.Status = in.Status
			d.OutputSummary = in.OutputSummary
			d.OutputPayload = in.OutputPayload
			d.ErrorCode = in.ErrorCode
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

func (m *memAuditWriter) SetChildRunID(_ context.Context, _, delegationID, childRunID string) error {
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
func (m *memAuditWriter) RecordDispatchAttempt(_ context.Context, _, delegationID string) error {
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
func (m *memAuditWriter) AccumulateModelTokens(context.Context, string, string, agentdelegation.TokenUsage) error {
	return nil
}

func (m *memAuditWriter) snapshot() []agentdelegation.Delegation {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]agentdelegation.Delegation, 0, len(m.rows))
	for _, d := range m.rows {
		out = append(out, d)
	}
	return out
}

type stubInvokable struct {
	name, desc string
	run        func(context.Context, string) (string, error)
}

func (s *stubInvokable) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: s.name, Desc: s.desc}, nil
}
func (s *stubInvokable) InvokableRun(ctx context.Context, in string, _ ...tool.Option) (string, error) {
	return s.run(ctx, in)
}

type scriptedTurn struct {
	content   string
	toolCalls []schema.ToolCall
}

type scriptedModel struct {
	turns []scriptedTurn
	i     int
	mu    sync.Mutex
}

func (m *scriptedModel) Generate(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.i >= len(m.turns) {
		return schema.AssistantMessage("", nil), nil
	}
	turn := m.turns[m.i]
	m.i++
	if len(turn.toolCalls) > 0 {
		return &schema.Message{Role: schema.Assistant, ToolCalls: turn.toolCalls}, nil
	}
	return schema.AssistantMessage(turn.content, nil), nil
}

func (m *scriptedModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	// Single-chunk stream
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		defer sw.Close()
		_ = sw.Send(msg, nil)
	}()
	return sr, nil
}

var _ model.BaseChatModel = (*scriptedModel)(nil)

// Ensure JSON import used.
var _ = json.RawMessage{}
