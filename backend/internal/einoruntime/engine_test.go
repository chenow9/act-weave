package einoruntime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// scriptedStreamModel is a true-streaming BaseChatModel for engine tests.
// Stream uses schema.Pipe multi-chunk sends — never StreamReaderFromArray(Generate()).
type scriptedStreamModel struct {
	mu            sync.Mutex
	turns         []scriptedTurn
	streamCalls   atomic.Int64
	generateCalls atomic.Int64
}

type scriptedTurn struct {
	// contentChunks are sequential Stream content deltas (true multi-chunk).
	contentChunks []string
	// toolCalls, when non-empty, are emitted on the last chunk of this turn
	// (or alone if contentChunks is empty).
	toolCalls []schema.ToolCall
}

func (m *scriptedStreamModel) nextTurn() (scriptedTurn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.turns) == 0 {
		return scriptedTurn{}, errors.New("scriptedStreamModel: no more turns")
	}
	turn := m.turns[0]
	m.turns = m.turns[1:]
	return turn, nil
}

func (m *scriptedStreamModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.generateCalls.Add(1)
	// Generate concatenates the next turn (used only if streaming is off).
	turn, err := m.nextTurn()
	if err != nil {
		return nil, err
	}
	return &schema.Message{
		Role:      schema.Assistant,
		Content:   strings.Join(turn.contentChunks, ""),
		ToolCalls: turn.toolCalls,
	}, nil
}

func (m *scriptedStreamModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.streamCalls.Add(1)
	turn, err := m.nextTurn()
	if err != nil {
		return nil, err
	}

	// True multi-chunk stream via Pipe — proves D14 / PR6 fake streaming model.
	sr, sw := schema.Pipe[*schema.Message](8)
	go func() {
		defer sw.Close()
		chunks := turn.contentChunks
		if len(chunks) == 0 && len(turn.toolCalls) > 0 {
			// Tool-call-only turn: single chunk with tool calls.
			_ = sw.Send(&schema.Message{
				Role:      schema.Assistant,
				ToolCalls: turn.toolCalls,
			}, nil)
			return
		}
		for i, piece := range chunks {
			msg := &schema.Message{
				Role:    schema.Assistant,
				Content: piece,
			}
			// Attach tool calls on the final content chunk when present.
			if i == len(chunks)-1 && len(turn.toolCalls) > 0 {
				msg.ToolCalls = turn.toolCalls
			}
			if closed := sw.Send(msg, nil); closed {
				return
			}
		}
		// If toolCalls set without content, already handled above.
		if len(chunks) > 0 && len(turn.toolCalls) > 0 {
			// already attached on last chunk
		}
	}()
	return sr, nil
}

// Ensure compile-time BaseChatModel.
var _ model.BaseChatModel = (*scriptedStreamModel)(nil)

func TestEnsureAgentRunCheckpointID_StableOncePerRun(t *testing.T) {
	t.Parallel()
	ws, run := "ws-engine", "run-42"

	first, err := EnsureAgentRunCheckpointID(ws, run, "")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	parsed, err := ParseCheckpointID(first)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.WorkspaceID != ws || parsed.OwnerID != run || parsed.Kind != CheckpointKindAgentRun {
		t.Fatalf("parsed = %+v", parsed)
	}

	// Reuse: same run + existing → identical ID (stable once-per-run).
	second, err := EnsureAgentRunCheckpointID(ws, run, first)
	if err != nil {
		t.Fatalf("ensure existing: %v", err)
	}
	if second != first {
		t.Fatalf("checkpoint not stable: first=%q second=%q", first, second)
	}

	// Fresh allocate without existing → different nonce.
	third, err := EnsureAgentRunCheckpointID(ws, run, "")
	if err != nil {
		t.Fatalf("allocate again: %v", err)
	}
	if third == first {
		t.Fatal("expected new nonce when existing empty")
	}
}

func TestEnsureAgentRunCheckpointID_RejectsMismatched(t *testing.T) {
	t.Parallel()
	id, err := FormatCheckpointID("ws-a", CheckpointKindAgentRun, "run-1", "n1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureAgentRunCheckpointID("ws-b", "run-1", id); err == nil {
		t.Fatal("expected workspace mismatch error")
	}
	if _, err := EnsureAgentRunCheckpointID("ws-a", "run-2", id); err == nil {
		t.Fatal("expected owner mismatch error")
	}
}

func TestEngine_TextOnlyTrueStreamMultiDelta(t *testing.T) {
	ctx := context.Background()
	fake := &scriptedStreamModel{
		turns: []scriptedTurn{
			{contentChunks: []string{"Hel", "lo, ", "world"}},
		},
	}
	agent, err := BuildChatModelAgent(ctx, AgentBuildConfig{
		Name:        "text-agent",
		Instruction: "You are a test agent.",
		Model:       fake,
	})
	if err != nil {
		t.Fatalf("BuildChatModelAgent: %v", err)
	}

	rec := &RecordingProjector{}
	engine := NewEngine(EngineConfig{})
	result, err := engine.Run(ctx, agent, RunInput{
		WorkspaceID: "ws-1",
		RunID:       "run-text",
		Messages:    []*schema.Message{schema.UserMessage("hi")},
		Projector:   rec,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Interrupted {
		t.Fatal("text path must not interrupt")
	}
	if fake.streamCalls.Load() < 1 {
		t.Fatal("expected true Stream to be called (EnableStreaming)")
	}
	if fake.generateCalls.Load() != 0 {
		t.Fatalf("Generate must not be the streaming path; generateCalls=%d", fake.generateCalls.Load())
	}
	if len(rec.Deltas) < 2 {
		t.Fatalf("want ≥2 text deltas from true multi-chunk Stream, got %v", rec.Deltas)
	}
	if got := rec.JoinedDeltas(); got != "Hello, world" {
		t.Fatalf("joined deltas = %q, want %q", got, "Hello, world")
	}
	if result.FinalAssistantText != "Hello, world" {
		t.Fatalf("FinalAssistantText = %q", result.FinalAssistantText)
	}
	if result.CheckpointID == "" {
		t.Fatal("expected checkpoint ID allocated once per run")
	}
	// Ensure same ID reused when passed back.
	again, err := EnsureAgentRunCheckpointID("ws-1", "run-text", result.CheckpointID)
	if err != nil || again != result.CheckpointID {
		t.Fatalf("stable ensure: %q %v", again, err)
	}
}

func TestEngine_ToolPath_InvokesPipelineToolOnce(t *testing.T) {
	ctx := context.Background()
	spy := &spyInvoker{}
	pt, err := NewPipelineTool(baseToolConfig(spy, false))
	if err != nil {
		t.Fatal(err)
	}

	fake := &scriptedStreamModel{
		turns: []scriptedTurn{
			{
				toolCalls: []schema.ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      "demo_tool",
						Arguments: `{"x":1}`,
					},
				}},
			},
			{contentChunks: []string{"tool ", "done"}},
		},
	}

	agent, err := BuildChatModelAgent(ctx, AgentBuildConfig{
		Name:  "tool-agent",
		Model: fake,
		Tools: []tool.BaseTool{pt},
	})
	if err != nil {
		t.Fatalf("BuildChatModelAgent: %v", err)
	}

	rec := &RecordingProjector{}
	engine := NewEngine(EngineConfig{Store: newMemCheckPointStore()})
	result, err := engine.Run(ctx, agent, RunInput{
		WorkspaceID: "ws-1",
		RunID:       "run-tool",
		Messages:    []*schema.Message{schema.UserMessage("use tool")},
		Projector:   rec,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Interrupted {
		t.Fatalf("unexpected interrupt: ids=%v", result.InterruptContextIDs)
	}
	if got := spy.calls.Load(); got != 1 {
		t.Fatalf("InvokeResolved = %d, want 1", got)
	}
	if rec.JoinedDeltas() != "tool done" {
		t.Fatalf("final text deltas = %q", rec.JoinedDeltas())
	}
}

func TestEngine_Interrupt_ReturnsInterruptContextIDs(t *testing.T) {
	ctx := context.Background()
	spy := &spyInvoker{}
	pt, err := NewPipelineTool(baseToolConfig(spy, true)) // RequiresConfirmation
	if err != nil {
		t.Fatal(err)
	}

	fake := &scriptedStreamModel{
		turns: []scriptedTurn{
			{
				toolCalls: []schema.ToolCall{{
					ID:   "call_confirm",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      "demo_tool",
						Arguments: `{"x":1}`,
					},
				}},
			},
		},
	}

	agent, err := BuildChatModelAgent(ctx, AgentBuildConfig{
		Name:  "hitl-agent",
		Model: fake,
		Tools: []tool.BaseTool{pt},
	})
	if err != nil {
		t.Fatalf("BuildChatModelAgent: %v", err)
	}

	store := newMemCheckPointStore()
	engine := NewEngine(EngineConfig{Store: store})
	result, err := engine.Run(ctx, agent, RunInput{
		WorkspaceID: "ws-1",
		RunID:       "run-hitl",
		Messages:    []*schema.Message{schema.UserMessage("confirm me")},
	})
	if err != nil {
		t.Fatalf("Run: %v (interrupt must not be hard error)", err)
	}
	if !result.Interrupted {
		t.Fatal("expected Interrupted=true")
	}
	if len(result.InterruptContextIDs) == 0 {
		t.Fatal("expected InterruptContextIDs for later einoChatResume")
	}
	if got := spy.calls.Load(); got != 0 {
		t.Fatalf("HITL first run InvokeResolved = %d, want 0", got)
	}
	// Checkpoint blob should be stored under the stable run checkpoint ID.
	if _, ok, _ := store.Get(ctx, result.CheckpointID); !ok {
		t.Fatalf("expected checkpoint saved at %q", result.CheckpointID)
	}
}

func TestBuildChatModelAgent_RequiresModel(t *testing.T) {
	t.Parallel()
	_, err := BuildChatModelAgent(context.Background(), AgentBuildConfig{})
	if err == nil {
		t.Fatal("expected error when Model is nil")
	}
}

func TestBuildChatModelAgent_Defaults(t *testing.T) {
	t.Parallel()
	fake := &scriptedStreamModel{
		turns: []scriptedTurn{{contentChunks: []string{"ok"}}},
	}
	agent, err := BuildChatModelAgent(context.Background(), AgentBuildConfig{
		Name:  "defaults",
		Model: fake,
	})
	if err != nil {
		t.Fatal(err)
	}
	if agent == nil {
		t.Fatal("nil agent")
	}
	// Smoke: name is stored.
	if agent.Name(context.Background()) != "defaults" {
		t.Fatalf("Name = %q", agent.Name(context.Background()))
	}
}

func TestMapEngineError_MaxIterations(t *testing.T) {
	t.Parallel()
	err := mapEngineError(adk.ErrExceedMaxIterations)
	if !errors.Is(err, ErrToolBudgetExceeded) {
		t.Fatalf("want ErrToolBudgetExceeded family, got %v", err)
	}
}

func TestProjectAgentEvent_StreamDeltas(t *testing.T) {
	t.Parallel()
	// Build a synthetic streaming event without going through Runner.
	sr, sw := schema.Pipe[*schema.Message](4)
	go func() {
		defer sw.Close()
		_ = sw.Send(&schema.Message{Role: schema.Assistant, Content: "A"}, nil)
		_ = sw.Send(&schema.Message{Role: schema.Assistant, Content: "B"}, nil)
		_ = sw.Send(&schema.Message{Role: schema.Assistant, Content: "C"}, nil)
	}()

	event := adk.EventFromMessage(nil, sr, schema.Assistant, "")
	rec := &RecordingProjector{}
	if err := ProjectAgentEvent(context.Background(), event, rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Deltas) != 3 {
		t.Fatalf("deltas = %v", rec.Deltas)
	}
	if rec.JoinedDeltas() != "ABC" {
		t.Fatalf("joined = %q", rec.JoinedDeltas())
	}
	if len(rec.Completed) != 1 || rec.Completed[0] != "ABC" {
		t.Fatalf("completed = %v", rec.Completed)
	}
	if len(rec.ModelTurns) != 1 || rec.ModelTurns[0].Content != "ABC" || rec.ModelTurns[0].Reasoning != "" {
		t.Fatalf("model turns = %+v", rec.ModelTurns)
	}
}

func TestProjectAgentEvent_StreamReasoningAggregatedSeparately(t *testing.T) {
	t.Parallel()
	sr, sw := schema.Pipe[*schema.Message](8)
	go func() {
		defer sw.Close()
		_ = sw.Send(&schema.Message{Role: schema.Assistant, ReasoningContent: "think-1 "}, nil)
		_ = sw.Send(&schema.Message{Role: schema.Assistant, ReasoningContent: "think-2"}, nil)
		_ = sw.Send(&schema.Message{Role: schema.Assistant, Content: "final"}, nil)
	}()

	event := adk.EventFromMessage(nil, sr, schema.Assistant, "")
	rec := &RecordingProjector{}
	if err := ProjectAgentEvent(context.Background(), event, rec); err != nil {
		t.Fatal(err)
	}
	if rec.JoinedDeltas() != "final" {
		t.Fatalf("content deltas must not include reasoning: %q", rec.JoinedDeltas())
	}
	if len(rec.ModelTurns) != 1 {
		t.Fatalf("want 1 model turn, got %+v", rec.ModelTurns)
	}
	turn := rec.ModelTurns[0]
	if turn.Content != "final" || turn.Reasoning != "think-1 think-2" {
		t.Fatalf("turn = %+v", turn)
	}
}

func TestProjectAgentEvent_ReasoningOnlyTurn(t *testing.T) {
	t.Parallel()
	sr, sw := schema.Pipe[*schema.Message](4)
	go func() {
		defer sw.Close()
		_ = sw.Send(&schema.Message{Role: schema.Assistant, ReasoningContent: "hidden plan"}, nil)
	}()

	event := adk.EventFromMessage(nil, sr, schema.Assistant, "")
	rec := &RecordingProjector{}
	if err := ProjectAgentEvent(context.Background(), event, rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Deltas) != 0 || len(rec.Completed) != 0 {
		t.Fatalf("reasoning-only must not emit public text hooks: deltas=%v completed=%v", rec.Deltas, rec.Completed)
	}
	if len(rec.ModelTurns) != 1 || rec.ModelTurns[0].Reasoning != "hidden plan" {
		t.Fatalf("model turns = %+v", rec.ModelTurns)
	}
}
