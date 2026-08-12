package chatruntimebridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// memStore is an in-memory adk.CheckPointStore for bridge resume tests.
type memStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemStore() *memStore {
	return &memStore{data: make(map[string][]byte)}
}

func (m *memStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[id]
	if !ok {
		return nil, false, nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out, true, nil
}

func (m *memStore) Set(_ context.Context, id string, checkPoint []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]byte, len(checkPoint))
	copy(out, checkPoint)
	m.data[id] = out
	return nil
}

type spyInvoker struct {
	calls atomic.Int64
}

func (s *spyInvoker) InvokeResolved(
	_ context.Context,
	request execution.InvokeRequest,
	_ execution.ResolvedInvocation,
) (execution.PipelineResult, error) {
	s.calls.Add(1)
	return execution.PipelineResult{
		InvocationResult: execution.InvocationResult{
			InvocationID: request.InvocationID,
			Output:       json.RawMessage(`{"answer":42}`),
			HTTPStatus:   200,
		},
	}, nil
}

type scriptedTurn struct {
	contentChunks []string
	toolCalls     []schema.ToolCall
}

// scriptedStreamModel emits true multi-delta streams (and optional tool calls).
type scriptedStreamModel struct {
	mu    sync.Mutex
	turns []scriptedTurn
}

func (m *scriptedStreamModel) nextTurn() scriptedTurn {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.turns) == 0 {
		return scriptedTurn{contentChunks: []string{""}}
	}
	turn := m.turns[0]
	m.turns = m.turns[1:]
	return turn
}

func (m *scriptedStreamModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	turn := m.nextTurn()
	return &schema.Message{
		Role:      schema.Assistant,
		Content:   join(turn.contentChunks),
		ToolCalls: turn.toolCalls,
	}, nil
}

func (m *scriptedStreamModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	turn := m.nextTurn()
	sr, sw := schema.Pipe[*schema.Message](8)
	go func() {
		defer sw.Close()
		if len(turn.contentChunks) == 0 && len(turn.toolCalls) > 0 {
			_ = sw.Send(&schema.Message{Role: schema.Assistant, ToolCalls: turn.toolCalls}, nil)
			return
		}
		for i, piece := range turn.contentChunks {
			msg := &schema.Message{Role: schema.Assistant, Content: piece}
			if i == len(turn.contentChunks)-1 && len(turn.toolCalls) > 0 {
				msg.ToolCalls = turn.toolCalls
			}
			_ = sw.Send(msg, nil)
		}
	}()
	return sr, nil
}

var _ model.BaseChatModel = (*scriptedStreamModel)(nil)

func join(parts []string) string {
	out := ""
	for _, p := range parts {
		out += p
	}
	return out
}

func baseToolConfig(pipeline einoruntime.ResolvedInvoker, requiresConfirmation bool) einoruntime.PipelineToolConfig {
	return einoruntime.PipelineToolConfig{
		Info: &schema.ToolInfo{
			Name: "demo_tool",
			Desc: "demo",
		},
		Pipeline:             pipeline,
		RequiresConfirmation: requiresConfirmation,
		WorkspaceID:          "ws-1",
		CapabilityID:         "cap-1",
		ReleaseID:            "rel-1",
		ActorType:            "USER",
		ActorID:              "user-1",
		TraceID:              "trace-1",
		AgentRunID:           "run-1",
		InvocationID:         "inv-fixed",
		StepID:               "step-1",
		Resolved: execution.ResolvedInvocation{
			Snapshot: execution.ReleaseSnapshot{
				WorkspaceID:  "ws-1",
				CapabilityID: "cap-1",
				ReleaseID:    "rel-1",
			},
			RequiresConfirmation: requiresConfirmation,
		},
	}
}

// TestEngineResume_NoSecondInvoke proves design §3.6.3 via Engine.Resume:
// interrupt (Invoke=0) → inject Dispatch result via Targets → Resume (Invoke still 0).
func TestEngineResume_NoSecondInvoke(t *testing.T) {
	ctx := context.Background()
	spy := &spyInvoker{}
	pt, err := einoruntime.NewPipelineTool(baseToolConfig(spy, true))
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
			// After resume: final assistant reply with multi-delta true stream.
			{contentChunks: []string{"Hel", "lo"}},
		},
	}

	agent, err := einoruntime.BuildChatModelAgent(ctx, einoruntime.AgentBuildConfig{
		Name:  "hitl-agent",
		Model: fake,
		Tools: []tool.BaseTool{pt},
	})
	if err != nil {
		t.Fatalf("BuildChatModelAgent: %v", err)
	}

	store := newMemStore()
	engine := einoruntime.NewEngine(einoruntime.EngineConfig{Store: store})
	first, err := engine.Run(ctx, agent, einoruntime.RunInput{
		WorkspaceID: "ws-1",
		RunID:       "run-hitl",
		Messages:    []*schema.Message{schema.UserMessage("confirm me")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !first.Interrupted {
		t.Fatal("expected Interrupted=true")
	}
	if len(first.InterruptContextIDs) == 0 {
		t.Fatal("expected interrupt ids")
	}
	if got := spy.calls.Load(); got != 0 {
		t.Fatalf("after interrupt InvokeResolved = %d, want 0", got)
	}

	// Platform Dispatch result (already InvokeResolved elsewhere).
	platformResult := `{"ok":true,"confirmed":true,"output":{"dispatched":true}}`
	meta := chatruntimebridge.EinoChatResume{
		ResumeSchemaVersion: chatruntimebridge.EinoChatResumeSchemaVersion,
		SessionID:           "sess-1",
		UserMessageID:       "msg-1",
		ActorID:             "actor-1",
		EinoCheckpointID:    first.CheckpointID,
		InterruptIDs:        first.InterruptContextIDs,
		RootInterruptID:     firstRoot(first),
		GatedToolCallID:     "call_confirm",
		InterruptKind:       chatruntimebridge.InterruptKindToolConfirmation,
	}
	// Embed/extract round-trip used by continue dispatcher.
	outer := json.RawMessage(`{"schemaVersion":"tool-resume-request.v1","invocationId":"inv-fixed"}`)
	embedded, err := chatruntimebridge.EmbedEinoChatResume(outer, meta)
	if err != nil {
		t.Fatal(err)
	}
	extracted, ok := chatruntimebridge.ExtractEinoChatResume(embedded)
	if !ok {
		t.Fatal("extract after embed failed")
	}

	// Simulate ResultSnapshot from ToolConfirmationResumeExecutor.
	resultSnap := json.RawMessage(`{
		"invocationId":"inv-fixed",
		"traceId":"trace-1",
		"output":{"dispatched":true},
		"httpStatus":200,
		"attempts":1,
		"cached":false
	}`)
	targets := map[string]any{}
	for _, id := range extracted.EffectiveInterruptIDs() {
		targets[id] = string(mustToolResult(resultSnap))
	}
	// Prefer injecting the same payload ResumeWithParams would receive.
	for id := range targets {
		targets[id] = platformResult
	}

	// Rebuild agent (same tools) for resume — production bridge does this too.
	agent2, err := einoruntime.BuildChatModelAgent(ctx, einoruntime.AgentBuildConfig{
		Name:  "hitl-agent",
		Model: fake,
		Tools: []tool.BaseTool{pt},
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := &einoruntime.RecordingProjector{}
	second, err := engine.Resume(ctx, agent2, einoruntime.ResumeInput{
		WorkspaceID:  "ws-1",
		RunID:        "run-hitl",
		CheckpointID: first.CheckpointID,
		Targets:      targets,
		Projector:    rec,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if second.Interrupted {
		t.Fatalf("unexpected re-interrupt: ids=%v", second.InterruptContextIDs)
	}
	if got := spy.calls.Load(); got != 0 {
		t.Fatalf("after resume InvokeResolved = %d, want 0 (platform already invoked)", got)
	}
	if second.FinalAssistantText == "" && rec.JoinedDeltas() == "" {
		t.Fatal("expected assistant text after resume")
	}
}

func firstRoot(r *einoruntime.RunResult) string {
	if len(r.RootCauseInterruptIDs) > 0 {
		return r.RootCauseInterruptIDs[0]
	}
	if len(r.InterruptContextIDs) > 0 {
		return r.InterruptContextIDs[0]
	}
	return ""
}

func mustToolResult(snap json.RawMessage) string {
	// Exported via package behaviour; re-implement minimal for test assertion path.
	return string(snap)
}

func TestBuildResumeTargetsViaExtract(t *testing.T) {
	t.Parallel()
	meta := chatruntimebridge.EinoChatResume{
		ResumeSchemaVersion: chatruntimebridge.EinoChatResumeSchemaVersion,
		SessionID:           "s",
		UserMessageID:       "m",
		ActorID:             "a",
		EinoCheckpointID:    "ws/ws-1/agent_run/run-1/n1",
		InterruptIDs:        []string{"agent:a;tool:c1"},
		RootInterruptID:     "agent:a;tool:c1",
		InterruptKind:       chatruntimebridge.InterruptKindToolConfirmation,
	}
	if !meta.Valid() {
		t.Fatal("meta should be valid")
	}
	ids := meta.EffectiveInterruptIDs()
	if len(ids) != 1 {
		t.Fatalf("ids = %v", ids)
	}
}

func TestStreamDeltaRecorder_RecordsDeltas(t *testing.T) {
	t.Parallel()
	rec := &chatruntimebridge.StreamDeltaRecorder{}
	ctx := context.Background()
	if err := rec.OnTextDelta(ctx, "Hel"); err != nil {
		t.Fatal(err)
	}
	if err := rec.OnTextDelta(ctx, "lo"); err != nil {
		t.Fatal(err)
	}
	if err := rec.OnTextComplete(ctx, "Hello"); err != nil {
		t.Fatal(err)
	}
	if rec.DeltaCount() != 2 {
		t.Fatalf("delta count = %d", rec.DeltaCount())
	}
	if rec.Joined() != "Hello" {
		t.Fatalf("joined = %q", rec.Joined())
	}
}

func TestToolResultForModel_ViaContinueTargets(t *testing.T) {
	t.Parallel()
	// Ensure platform result snapshot maps into a confirmed tool JSON for the model.
	// Exercised through embed+extract+EffectiveInterruptIDs only here; mapping is
	// covered by engine resume using platformResult string.
	meta := chatruntimebridge.EinoChatResume{
		ResumeSchemaVersion: chatruntimebridge.EinoChatResumeSchemaVersion,
		SessionID:           "s", UserMessageID: "m", ActorID: "a",
		EinoCheckpointID: "ws/w/agent_run/r/n",
		RootInterruptID:  "agent:x;tool:y",
		InterruptKind:    chatruntimebridge.InterruptKindToolConfirmation,
	}
	outer := json.RawMessage(`{"schemaVersion":"tool-resume-request.v1"}`)
	embedded, err := chatruntimebridge.EmbedEinoChatResume(outer, meta)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := chatruntimebridge.ExtractEinoChatResume(embedded)
	if !ok || got.RootInterruptID != meta.RootInterruptID {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestStreamDeltaRecorder_FailIncomplete(t *testing.T) {
	t.Parallel()
	sink := &chatruntimebridge.RecordingTextDeltaSink{}
	rec := &chatruntimebridge.StreamDeltaRecorder{Sink: sink}
	ctx := context.Background()
	if err := rec.OnTextDelta(ctx, "partial"); err != nil {
		t.Fatal(err)
	}
	if err := rec.FailIncomplete(ctx, "WAITING_CONFIRMATION", true); err != nil {
		t.Fatal(err)
	}
	if sink.Failure == nil || sink.Failure.Code != "WAITING_CONFIRMATION" {
		t.Fatalf("failure = %+v", sink.Failure)
	}
	if sink.Failure.PartialText != "partial" {
		t.Fatalf("partial = %q", sink.Failure.PartialText)
	}
	// Second fail is a no-op.
	if err := rec.FailIncomplete(ctx, "OTHER", false); err != nil {
		t.Fatal(err)
	}
}

// TestStreamDeltaRecorder_FailsTextStreamedAfterACompletion covers a turn that
// produces more than one assistant message: the Agentic engine completes each of
// them onto the same item, so "did this turn already complete" was true from the
// first one onward and every later interrupt or error silently skipped FailText.
// A turn that says something, calls a tool, then says more and gets interrupted
// would leave that trailing text with no terminal marker at all.
func TestStreamDeltaRecorder_FailsTextStreamedAfterACompletion(t *testing.T) {
	t.Parallel()
	sink := &chatruntimebridge.RecordingTextDeltaSink{}
	rec := &chatruntimebridge.StreamDeltaRecorder{Sink: sink}
	ctx := context.Background()
	for _, step := range []func() error{
		func() error { return rec.OnTextDelta(ctx, "let me check") },
		func() error { return rec.OnTextComplete(ctx, "let me check") },
		func() error { return rec.OnTextDelta(ctx, "the transfer needs") },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	if err := rec.FailIncomplete(ctx, "WAITING_CONFIRMATION", true); err != nil {
		t.Fatal(err)
	}
	if sink.Failure == nil {
		t.Fatal("text streamed after the first completion was never terminated")
	}
	if sink.Failure.Code != "WAITING_CONFIRMATION" {
		t.Fatalf("failure code = %q, want WAITING_CONFIRMATION", sink.Failure.Code)
	}
	// Only the unfinished tail: the earlier text was already reported complete.
	if sink.Failure.PartialText != "the transfer needs" {
		t.Fatalf("partial = %q, want only the text that never completed",
			sink.Failure.PartialText)
	}
}

// TestStreamDeltaRecorder_DoesNotFailAFullyCompletedTurn is the other side of the
// same comparison: a turn whose last word was completed must not also be failed.
func TestStreamDeltaRecorder_DoesNotFailAFullyCompletedTurn(t *testing.T) {
	t.Parallel()
	sink := &chatruntimebridge.RecordingTextDeltaSink{}
	rec := &chatruntimebridge.StreamDeltaRecorder{Sink: sink}
	ctx := context.Background()
	if err := rec.OnTextDelta(ctx, "done"); err != nil {
		t.Fatal(err)
	}
	if err := rec.OnTextComplete(ctx, "done"); err != nil {
		t.Fatal(err)
	}
	if err := rec.FailIncomplete(ctx, "MODEL_STREAM_INTERRUPTED", true); err != nil {
		t.Fatal(err)
	}
	if sink.Failure != nil {
		t.Fatalf("a completed turn was also failed: %+v", sink.Failure)
	}
}

// --- Bridge.ContinueAfterConfirmation drives real buildResumeTargets ---

type bridgeSessions struct {
	messages []chat.Message
}

func (s *bridgeSessions) GetSession(_ context.Context, workspaceID, sessionID string) (chat.Session, error) {
	return chat.Session{ID: sessionID, WorkspaceID: workspaceID, LockVersion: 1}, nil
}
func (s *bridgeSessions) ListMessages(context.Context, string, string) ([]chat.Message, error) {
	return s.messages, nil
}
func (s *bridgeSessions) ListMessagesReversePage(
	_ context.Context, _, _ string, limit int, cursor *chat.MessagePageCursor,
) (chat.MessagePage, error) {
	msgs := append([]chat.Message(nil), s.messages...)
	// newest first
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	start := 0
	if cursor != nil {
		for i, m := range msgs {
			if m.ID == cursor.ID {
				start = i + 1
				break
			}
		}
	}
	if start > len(msgs) {
		start = len(msgs)
	}
	end := start + limit
	hasMore := end < len(msgs)
	if end > len(msgs) {
		end = len(msgs)
	}
	page := chat.MessagePage{Messages: msgs[start:end], HasMore: hasMore}
	if hasMore && len(page.Messages) > 0 {
		last := page.Messages[len(page.Messages)-1]
		page.NextCursor = &chat.MessagePageCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}
func (s *bridgeSessions) GetMessage(_ context.Context, _, messageID string) (chat.Message, error) {
	for _, m := range s.messages {
		if m.ID == messageID {
			return m, nil
		}
	}
	return chat.Message{}, chat.ErrNotFound
}

type bridgeResults struct {
	mu      sync.Mutex
	content string
}

func (r *bridgeResults) RecordAssistantResult(_ context.Context, in chat.RecordAssistantResultInput) (chat.RecordAssistantResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.content = in.Content
	return chat.RecordAssistantResult{Message: chat.Message{
		ID: in.AssistantMessageID, WorkspaceID: in.WorkspaceID, SessionID: in.SessionID,
		Role: "ASSISTANT", Content: in.Content, Status: "COMPLETED", RunID: in.RunID,
		CreatedAt: time.Now().UTC(),
	}}, nil
}

type bridgeAgents struct{}

func (bridgeAgents) Get(_ context.Context, workspaceID, agentID string) (agent.Agent, error) {
	return agent.Agent{
		ID: agentID, WorkspaceID: workspaceID, ModelConfigID: "model-1",
		Status: agent.StatusActive,
	}, nil
}
func (bridgeAgents) ListPromptRevisions(context.Context, string, string) ([]agent.PromptRevision, error) {
	return nil, nil
}

type bridgeModels struct{}

func (bridgeModels) Get(_ context.Context, workspaceID, id string) (modelconfig.Config, error) {
	return modelconfig.Config{
		ID: id, WorkspaceID: workspaceID, Status: modelconfig.StatusVerified,
		Provider: "openai", ModelName: "fake",
	}, nil
}

type bridgeRuns struct {
	run execution.AgentRun
}

func (r *bridgeRuns) GetAgentRun(_ context.Context, workspaceID, runID string) (execution.AgentRun, error) {
	out := r.run
	out.WorkspaceID = workspaceID
	out.ID = runID
	return out, nil
}

type bridgeEvents struct{}

func (bridgeEvents) Record(context.Context, chatruntime.ProtocolRecord) error { return nil }

type bridgeToolInvoker struct {
	spy *spyInvoker
	// free means the tool runs without HITL. Zero value keeps the historical
	// confirm-gated behaviour that the continue tests rely on.
	free bool
}

func (t *bridgeToolInvoker) ResolveInvocation(_ context.Context, req execution.ResolveRequest) (execution.ResolvedInvocation, error) {
	if t.free {
		return execution.ResolvedInvocation{
			Snapshot: execution.ReleaseSnapshot{
				WorkspaceID: req.WorkspaceID, CapabilityID: req.CapabilityID,
				ReleaseID: req.ReleaseID, ProviderID: testResumeProviderUUID,
			},
			Connection: execution.ConnectionSnapshot{
				ID: testConnUUID, WorkspaceID: req.WorkspaceID, Environment: "TEST",
				ProviderID: testResumeProviderUUID,
			},
			RequiresConfirmation: false,
			RiskLevel:            "LOW",
			SideEffectLevel:      "NONE",
		}, nil
	}
	return execution.ResolvedInvocation{
		Snapshot: execution.ReleaseSnapshot{
			WorkspaceID: req.WorkspaceID, CapabilityID: req.CapabilityID, ReleaseID: req.ReleaseID,
		},
		Connection: execution.ConnectionSnapshot{
			ID: "conn-1", WorkspaceID: req.WorkspaceID, Environment: "DEV",
		},
		RequiresConfirmation: true,
		RiskLevel:            "HIGH",
		SideEffectLevel:      "WRITE",
	}, nil
}

func (t *bridgeToolInvoker) InvokeResolved(
	ctx context.Context, req execution.InvokeRequest, resolved execution.ResolvedInvocation,
) (execution.PipelineResult, error) {
	if t.spy == nil {
		return execution.PipelineResult{}, errors.New("spy missing")
	}
	return t.spy.InvokeResolved(ctx, req, resolved)
}

// TestBridgeContinueAfterConfirmation_NoSecondInvoke proves production continue:
// real buildResumeTargets (Targets keys = interrupt IDs) + Bridge.ContinueAfterConfirmation
// + spy invoker Invoke=0 after resume.
func TestBridgeContinueAfterConfirmation_NoSecondInvoke(t *testing.T) {
	t.Skip("classic ChatModelAgent HITL continue removed in Task 9; rewrite against Agentic resume")
	ctx := context.Background()
	spy := &spyInvoker{}
	pt, err := einoruntime.NewPipelineTool(baseToolConfig(spy, true))
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
						Name: "demo_tool", Arguments: `{"x":1}`,
					},
				}},
			},
			// After Bridge resume: final multi-delta true stream.
			{contentChunks: []string{"Done", "-ok"}},
		},
	}

	// First run via Engine to allocate checkpoint + interrupt IDs (same store Bridge uses).
	agent1, err := einoruntime.BuildChatModelAgent(ctx, einoruntime.AgentBuildConfig{
		Name: "agent-agent-1", Model: fake, Tools: []tool.BaseTool{pt},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore()
	engine := einoruntime.NewEngine(einoruntime.EngineConfig{Store: store})
	first, err := engine.Run(ctx, agent1, einoruntime.RunInput{
		WorkspaceID: "ws-1", RunID: "run-bridge-hitl",
		Messages: []*schema.Message{schema.UserMessage("confirm me")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !first.Interrupted || len(first.InterruptContextIDs) == 0 {
		t.Fatalf("expected interrupt with ids, got %+v", first)
	}
	if got := spy.calls.Load(); got != 0 {
		t.Fatalf("after interrupt Invoke=%d, want 0", got)
	}

	// Production continue snapshot: outer tool-resume-request + nested einoChatResume.
	meta := chatruntimebridge.EinoChatResume{
		ResumeSchemaVersion: chatruntimebridge.EinoChatResumeSchemaVersion,
		SessionID:           "sess-1",
		UserMessageID:       "msg-1",
		ActorID:             "actor-1",
		EinoCheckpointID:    first.CheckpointID,
		InterruptIDs:        first.InterruptContextIDs,
		RootInterruptID:     firstRoot(first),
		GatedToolCallID:     "call_confirm",
		InterruptKind:       chatruntimebridge.InterruptKindToolConfirmation,
	}
	outer := json.RawMessage(`{"schemaVersion":"tool-resume-request.v1","invocationId":"inv-fixed"}`)
	requestSnap, err := chatruntimebridge.EmbedEinoChatResume(outer, meta)
	if err != nil {
		t.Fatal(err)
	}
	// Platform Dispatch ResultSnapshot (sole Invoke already done outside bridge).
	resultSnap := json.RawMessage(`{
		"invocationId":"inv-fixed",
		"traceId":"trace-1",
		"output":{"dispatched":true},
		"httpStatus":200,
		"attempts":1,
		"cached":false
	}`)

	// Assert production mapping: Targets keys == interrupt IDs from interrupt result.
	// (buildResumeTargets is unexported; prove via ContinueAfterConfirmation behaviour +
	// EffectiveInterruptIDs contract used by production path.)
	ids := meta.EffectiveInterruptIDs()
	if len(ids) == 0 {
		t.Fatal("expected interrupt ids for targets")
	}
	for _, id := range first.InterruptContextIDs {
		found := false
		for _, got := range ids {
			if got == id {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("interrupt id %q missing from EffectiveInterruptIDs %v", id, ids)
		}
	}

	capSnap := json.RawMessage(`{
		"schemaVersion":"capability-snapshot.v1",
		"releases":[{
			"capabilityId":"cap-1","releaseId":"rel-1","kind":"TOOL",
			"callableName":"demo_tool","callableDescription":"demo",
			"inputSchema":{"type":"object","properties":{"x":{"type":"number"}}},
			"riskLevel":"HIGH","sideEffectLevel":"WRITE",
			"requiresConfirmation":true,"connectionId":"conn-1"
		}]
	}`)
	results := &bridgeResults{}
	sink := &chatruntimebridge.RecordingTextDeltaSink{}
	bridge, err := chatruntimebridge.NewBridge(chatruntimebridge.Dependencies{
		Sessions: &bridgeSessions{messages: []chat.Message{{
			ID: "msg-1", Role: "USER", Content: "confirm me", Status: "COMPLETED",
		}}},
		Results: results,
		Agents:  bridgeAgents{},
		Models:  bridgeModels{},
		Runs: &bridgeRuns{run: execution.AgentRun{
			ID: "run-bridge-hitl", WorkspaceID: "ws-1", SessionID: "sess-1",
			AgentID: "agent-1", Status: "RUNNING", CapabilitySnapshot: capSnap,
			TriggeredByType: "USER", TriggeredByID: "user-1", TraceID: "trace-1",
			LockVersion: 1,
		}},
		Events:      bridgeEvents{},
		ToolInvoker: &bridgeToolInvoker{spy: spy},
		TextSinkFactory: func(context.Context, chatruntimebridge.TextSinkArgs) (chatruntime.TextDeltaSink, error) {
			return sink, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	job := agentrun.Job{
		WorkspaceID: "ws-1", SessionID: "sess-1", RunID: "run-bridge-hitl",
		UserMessageID: "msg-1", ActorID: "actor-1",
	}
	if err := bridge.ContinueAfterConfirmation(ctx, job, requestSnap, resultSnap); err != nil {
		t.Fatalf("ContinueAfterConfirmation: %v", err)
	}
	if got := spy.calls.Load(); got != 0 {
		t.Fatalf("after Bridge continue InvokeResolved=%d, want 0 (platform already invoked)", got)
	}
	results.mu.Lock()
	content := results.content
	results.mu.Unlock()
	if content == "" {
		t.Fatal("expected assistant content after Bridge continue")
	}
	// True stream deltas projected to protocol sink (D14 production path).
	if len(sink.EmissionTexts()) == 0 && content != "" {
		// Engine may deliver via FinalAssistantText; Sink should still see deltas
		// when model Stream multi-chunk path is used.
		t.Logf("assistant content=%q sink emissions=%v (complete=%v)", content, sink.EmissionTexts(), sink.Completion != nil)
	}
	if len(sink.EmissionTexts()) == 0 {
		t.Fatalf("expected protocol Sink item.delta emissions, got none; content=%q", content)
	}
}
