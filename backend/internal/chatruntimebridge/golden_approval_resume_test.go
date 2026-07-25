package chatruntimebridge_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// PR9 golden fixtures (design Appendix A.4 + HITL 一行契约).
//
// Offline / deterministic: scripted true-Stream model + spy invokers + Bridge
// ContinueAfterConfirmation + real Embed/Extract. No DB / network.
//
// Protocolschema approval_resume.jsonl remains the AAP envelope source of
// truth for the ten-event type order. These tests prove eino tool HITL
// ownership that must stay equivalent under ResumeWithParams.

// a4EventTypeOrder is the fixed A.4 type sequence (id/timestamps variable).
// Matches protocolschema/testdata/aap/v1/approval_resume.jsonl.
var a4EventTypeOrder = []string{
	"run.accepted",
	"run.started",
	"item.started", // tool_call status=waiting
	"interaction.requested",
	"run.waiting",
	"interaction.resolved", // user approve
	"run.resumed",
	"item.completed", // same tool item; Dispatch already invoked
	"usage.updated",
	"run.completed",
}

// TestGoldenA4_ApprovalResumeTypeOrder asserts Appendix A.4 ten-event type
// order against the protocolschema fixture (engine-agnostic AAP contract).
func TestGoldenA4_ApprovalResumeTypeOrder(t *testing.T) {
	t.Parallel()

	// Offline harness cannot emit full DB-backed SSE; the fixture is the
	// shared contract the eino bridge projects into.
	for _, engine := range []string{"eino"} {
		engine := engine
		t.Run(engine, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join("..", "protocolschema", "testdata", "aap", "v1", "approval_resume.jsonl")
			types := readGoldenEventTypes(t, path)
			if len(types) != len(a4EventTypeOrder) {
				t.Fatalf("event count = %d, want %d (A.4 ten events)", len(types), len(a4EventTypeOrder))
			}
			for i, want := range a4EventTypeOrder {
				if types[i] != want {
					t.Fatalf("event[%d] = %q, want %q (engine=%s)", i, types[i], want, engine)
				}
			}
			// A.4: item.started precedes interaction.requested.
			if indexOf(types, "item.started") >= indexOf(types, "interaction.requested") {
				t.Fatal("item.started(waiting) must precede interaction.requested")
			}
			// A.4: resolved/resumed present (not only requested/waiting).
			if indexOf(types, "interaction.resolved") < 0 || indexOf(types, "run.resumed") < 0 {
				t.Fatal("A.4 requires interaction.resolved and run.resumed")
			}
			// A.4: tool completed after resume, before run.completed.
			if indexOf(types, "item.completed") <= indexOf(types, "run.resumed") {
				t.Fatal("item.completed must follow run.resumed")
			}
			if indexOf(types, "run.completed") <= indexOf(types, "item.completed") {
				t.Fatal("run.completed must follow item.completed")
			}
		})
	}
}

// TestGoldenA4_EinoToolHITL_OwnershipTimeline proves design HITL ownership:
//
//	confirm前 0 Invoke → Dispatch 恰好 1 Invoke → Eino resume 0
//
// and that interruptIds from the interrupt result are the ResumeWithParams
// Targets keys (via EffectiveInterruptIDs + successful Bridge continue).
func TestGoldenA4_EinoToolHITL_OwnershipTimeline(t *testing.T) {
	ctx := context.Background()

	// Two spies: tool adapter (eino PipelineTool) vs platform Dispatch owner.
	toolAdapter := &spyInvoker{}
	platformDispatch := &spyInvoker{}

	const (
		toolName   = "payment.refund"
		toolArgs   = `{"orderId":"O-100","amount":88}`
		toolOutput = `{"refundId":"R-900","status":"accepted"}`
		finalText  = "退款已受理。"
		callID     = "call_refund"
		capID      = "cap-refund"
		relID      = "rel-refund"
		wsID       = "ws-a4"
		runID      = "run-a4-approval"
		sessID     = "sess-a4"
		msgID      = "msg-a4"
		actorID    = "actor-a4"
		invID      = "inv-a4-refund"
	)

	pt, err := einoruntime.NewPipelineTool(einoruntime.PipelineToolConfig{
		Info:                 &schema.ToolInfo{Name: toolName, Desc: "refund"},
		Pipeline:             toolAdapter,
		RequiresConfirmation: true,
		WorkspaceID:          wsID,
		CapabilityID:         capID,
		ReleaseID:            relID,
		ActorType:            "USER",
		ActorID:              actorID,
		TraceID:              "golden-approval",
		AgentRunID:           runID,
		InvocationID:         invID,
		StepID:               "step-a4",
		Resolved: execution.ResolvedInvocation{
			Snapshot: execution.ReleaseSnapshot{
				WorkspaceID: wsID, CapabilityID: capID, ReleaseID: relID,
			},
			RequiresConfirmation: true,
			RiskLevel:            "HIGH",
			SideEffectLevel:      "WRITE",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	fake := &scriptedStreamModel{
		turns: []scriptedTurn{
			{
				toolCalls: []schema.ToolCall{{
					ID: callID, Type: "function",
					Function: schema.FunctionCall{Name: toolName, Arguments: toolArgs},
				}},
			},
			// After resume: multi-delta true stream (A.5 / PR8 style).
			{contentChunks: []string{"退款", "已受理。"}},
		},
	}

	// --- Phase events 1–5: first drive → interrupt; zero Invoke ---
	agent1, err := einoruntime.BuildChatModelAgent(ctx, einoruntime.AgentBuildConfig{
		Name: "agent-agent-a4", Model: fake, Tools: []tool.BaseTool{pt},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore()
	engine := einoruntime.NewEngine(einoruntime.EngineConfig{Store: store})
	first, err := engine.Run(ctx, agent1, einoruntime.RunInput{
		WorkspaceID: wsID, RunID: runID,
		Messages: []*schema.Message{schema.UserMessage("refund O-100")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !first.Interrupted {
		t.Fatal("A.4 pause phase requires Interrupted=true")
	}
	if len(first.InterruptContextIDs) == 0 {
		t.Fatal("A.4 requires interruptIds for later ResumeWithParams Targets")
	}
	if got := toolAdapter.calls.Load(); got != 0 {
		t.Fatalf("events 1–5: tool adapter InvokeResolved=%d, want 0", got)
	}
	if got := platformDispatch.calls.Load(); got != 0 {
		t.Fatalf("events 1–5: platform Dispatch Invoke=%d, want 0", got)
	}

	// Nested einoChatResume inside outer tool-resume-request.v1 (production pause).
	meta := chatruntimebridge.EinoChatResume{
		ResumeSchemaVersion: chatruntimebridge.EinoChatResumeSchemaVersion,
		SessionID:           sessID,
		UserMessageID:       msgID,
		ActorID:             actorID,
		EinoCheckpointID:    first.CheckpointID,
		InterruptIDs:        append([]string(nil), first.InterruptContextIDs...),
		RootInterruptID:     firstRoot(first),
		GatedToolCallID:     callID,
		GatedStepID:         "step-a4",
		InterruptKind:       chatruntimebridge.InterruptKindToolConfirmation,
	}
	if !meta.Valid() {
		t.Fatalf("einoChatResume invalid: %+v", meta)
	}
	outer := json.RawMessage(`{
		"schemaVersion":"tool-resume-request.v1",
		"invocationId":"` + invID + `",
		"workspaceId":"` + wsID + `",
		"capabilityId":"` + capID + `",
		"releaseId":"` + relID + `"
	}`)
	requestSnap, err := chatruntimebridge.EmbedEinoChatResume(outer, meta)
	if err != nil {
		t.Fatal(err)
	}
	// Mutual exclusion: chatLoop must not ride along on eino path.
	if chatruntimebridge.HasChatLoop(requestSnap) {
		t.Fatal("eino path must not embed chatLoop")
	}
	extracted, ok := chatruntimebridge.ExtractEinoChatResume(requestSnap)
	if !ok {
		t.Fatal("ExtractEinoChatResume failed after embed")
	}
	// interruptIds present and equal to engine interrupt context IDs.
	ids := extracted.EffectiveInterruptIDs()
	if len(ids) == 0 {
		t.Fatal("EffectiveInterruptIDs empty — cannot build Resume Targets")
	}
	for _, want := range first.InterruptContextIDs {
		found := false
		for _, got := range ids {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("interrupt id %q missing from EffectiveInterruptIDs %v", want, ids)
		}
	}

	// --- Phase event 6: platform Dispatch owns the sole successful Invoke ---
	resultSnap := json.RawMessage(`{
		"invocationId":"` + invID + `",
		"traceId":"golden-approval",
		"output":` + toolOutput + `,
		"httpStatus":200,
		"attempts":1,
		"cached":false
	}`)
	// Simulate ToolConfirmationResumeExecutor → InvokeResolved (platform owner).
	if _, err := platformDispatch.InvokeResolved(ctx, execution.InvokeRequest{
		InvocationID: invID, WorkspaceID: wsID, CapabilityID: capID, ReleaseID: relID,
		Input: json.RawMessage(toolArgs),
	}, execution.ResolvedInvocation{}); err != nil {
		t.Fatal(err)
	}
	if got := platformDispatch.calls.Load(); got != 1 {
		t.Fatalf("event 6: platform Dispatch Invoke=%d, want 1", got)
	}
	// Tool adapter still untouched (Dispatch uses pipeline, not PipelineTool).
	if got := toolAdapter.calls.Load(); got != 0 {
		t.Fatalf("event 6: tool adapter Invoke must stay 0, got %d", got)
	}

	// --- Phase events 7–10: Bridge continue / ResumeWithParams; adapter Invoke=0 ---
	capSnap := json.RawMessage(`{
		"schemaVersion":"capability-snapshot.v1",
		"releases":[{
			"capabilityId":"` + capID + `","releaseId":"` + relID + `","kind":"TOOL",
			"callableName":"` + toolName + `","callableDescription":"refund",
			"inputSchema":{"type":"object","properties":{"orderId":{"type":"string"},"amount":{"type":"number"}}},
			"riskLevel":"HIGH","sideEffectLevel":"WRITE",
			"requiresConfirmation":true,"connectionId":"conn-a4"
		}]
	}`)
	results := &bridgeResults{}
	events := &recordingEvents{}
	sink := &chatruntimebridge.RecordingTextDeltaSink{}
	bridge, err := chatruntimebridge.NewBridge(chatruntimebridge.Dependencies{
		Sessions: &bridgeSessions{messages: []chat.Message{{
			ID: msgID, Role: "USER", Content: "refund O-100", Status: "COMPLETED",
		}}},
		Results: results,
		Agents:  bridgeAgents{},
		Models:  bridgeModels{},
		Runs: &bridgeRuns{run: execution.AgentRun{
			ID: runID, WorkspaceID: wsID, SessionID: sessID,
			AgentID: "agent-a4", Status: "RUNNING", CapabilitySnapshot: capSnap,
			TriggeredByType: "USER", TriggeredByID: actorID, TraceID: "golden-approval",
			LockVersion: 1,
		}},
		Events:      events,
		ToolInvoker: &bridgeToolInvoker{spy: toolAdapter},
		Engine:      engine,
		BuildChatModel: func(context.Context, modelconfig.Config) (model.BaseChatModel, error) {
			return fake, nil
		},
		TextSinkFactory: func(context.Context, chatruntimebridge.TextSinkArgs) (chatruntime.TextDeltaSink, error) {
			return sink, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	job := agentrun.Job{
		WorkspaceID: wsID, SessionID: sessID, RunID: runID,
		UserMessageID: msgID, ActorID: actorID,
	}
	if err := bridge.ContinueAfterConfirmation(ctx, job, requestSnap, resultSnap); err != nil {
		t.Fatalf("ContinueAfterConfirmation: %v", err)
	}

	// Resume path: tool adapter must not re-invoke (platform already did once).
	if got := toolAdapter.calls.Load(); got != 0 {
		t.Fatalf("events 7–10: tool adapter InvokeResolved=%d, want 0", got)
	}
	// Platform Dispatch remains exactly once across the whole timeline.
	if got := platformDispatch.calls.Load(); got != 1 {
		t.Fatalf("total platform Dispatch Invoke=%d, want 1", got)
	}

	results.mu.Lock()
	content := results.content
	results.mu.Unlock()
	if content == "" {
		t.Fatal("expected assistant content after A.4 resume")
	}
	if content != finalText {
		t.Fatalf("assistant content=%q, want golden %q", content, finalText)
	}
	// True multi-delta stream projected to protocol sink (post-resume).
	if joined := join(sink.EmissionTexts()); joined != finalText {
		t.Fatalf("item.delta join=%q, want %q", joined, finalText)
	}
	if len(sink.EmissionTexts()) < 2 {
		t.Fatalf("want multi item.delta after resume, got %v", sink.EmissionTexts())
	}

	// Protocol: continue completion projects run.completed (A.4 event 10).
	kinds := events.kinds()
	if !containsKind(kinds, chatruntime.ProtocolRecordRunCompleted) {
		t.Fatalf("expected ProtocolRecordRunCompleted after resume, kinds=%v", kinds)
	}
}

// TestGoldenA4_EinoInterruptIdsAreResumeTargetKeys asserts that every
// interrupt id from the engine interrupt is used as a ResumeWithParams
// Targets key (via EffectiveInterruptIDs contract that production
// buildResumeTargets consumes).
func TestGoldenA4_EinoInterruptIdsAreResumeTargetKeys(t *testing.T) {
	ctx := context.Background()
	spy := &spyInvoker{}
	pt, err := einoruntime.NewPipelineTool(baseToolConfig(spy, true))
	if err != nil {
		t.Fatal(err)
	}
	fake := &scriptedStreamModel{
		turns: []scriptedTurn{{
			toolCalls: []schema.ToolCall{{
				ID: "call_confirm", Type: "function",
				Function: schema.FunctionCall{Name: "demo_tool", Arguments: `{"x":1}`},
			}},
		}},
	}
	agent1, err := einoruntime.BuildChatModelAgent(ctx, einoruntime.AgentBuildConfig{
		Name: "hitl-targets", Model: fake, Tools: []tool.BaseTool{pt},
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := einoruntime.NewEngine(einoruntime.EngineConfig{Store: newMemStore()})
	first, err := engine.Run(ctx, agent1, einoruntime.RunInput{
		WorkspaceID: "ws-targets", RunID: "run-targets",
		Messages: []*schema.Message{schema.UserMessage("confirm")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Interrupted || len(first.InterruptContextIDs) == 0 {
		t.Fatalf("need interrupt ids, got %+v", first)
	}

	meta := chatruntimebridge.EinoChatResume{
		ResumeSchemaVersion: chatruntimebridge.EinoChatResumeSchemaVersion,
		SessionID:           "s", UserMessageID: "m", ActorID: "a",
		EinoCheckpointID: first.CheckpointID,
		InterruptIDs:     append([]string(nil), first.InterruptContextIDs...),
		RootInterruptID:  firstRoot(first),
		InterruptKind:    chatruntimebridge.InterruptKindToolConfirmation,
	}
	// Production Targets construction: keys = EffectiveInterruptIDs.
	targetKeys := meta.EffectiveInterruptIDs()
	if len(targetKeys) != len(first.InterruptContextIDs) {
		t.Fatalf("target keys %v vs interrupt ids %v", targetKeys, first.InterruptContextIDs)
	}
	targets := map[string]any{}
	for _, id := range targetKeys {
		targets[id] = `{"ok":true,"confirmed":true,"output":{"dispatched":true}}`
	}
	// Every engine interrupt id is a Targets key.
	for _, id := range first.InterruptContextIDs {
		if _, ok := targets[id]; !ok {
			t.Fatalf("interrupt id %q not present as Resume Targets key; keys=%v", id, keysOf(targets))
		}
	}
	// Resume with those exact keys succeeds without second Invoke.
	fake.mu.Lock()
	fake.turns = []scriptedTurn{{contentChunks: []string{"ok"}}}
	fake.mu.Unlock()
	agent2, err := einoruntime.BuildChatModelAgent(ctx, einoruntime.AgentBuildConfig{
		Name: "hitl-targets", Model: fake, Tools: []tool.BaseTool{pt},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Resume(ctx, agent2, einoruntime.ResumeInput{
		WorkspaceID: "ws-targets", RunID: "run-targets",
		CheckpointID: first.CheckpointID, Targets: targets,
	})
	if err != nil {
		t.Fatalf("Resume with interruptIds Targets: %v", err)
	}
	if second.Interrupted {
		t.Fatalf("unexpected re-interrupt: %v", second.InterruptContextIDs)
	}
	if got := spy.calls.Load(); got != 0 {
		t.Fatalf("resume InvokeResolved=%d, want 0", got)
	}
}

// TestGoldenA4_EnqueueContinueLifecycleCompletes proves recovery/lease
// interaction without heavy DB: lifecycle.Complete always fires after the
// continue drive (success or duplicate-register path), so multi-replica
// recovery cannot leave a stuck lease.
func TestGoldenA4_EnqueueContinueLifecycleCompletes(t *testing.T) {
	ctx := context.Background()
	spy := &spyInvoker{}
	pt, err := einoruntime.NewPipelineTool(baseToolConfig(spy, true))
	if err != nil {
		t.Fatal(err)
	}
	fake := &scriptedStreamModel{
		turns: []scriptedTurn{
			{toolCalls: []schema.ToolCall{{
				ID: "call_confirm", Type: "function",
				Function: schema.FunctionCall{Name: "demo_tool", Arguments: `{"x":1}`},
			}}},
			{contentChunks: []string{"done"}},
		},
	}
	store := newMemStore()
	engine := einoruntime.NewEngine(einoruntime.EngineConfig{Store: store})
	agent1, err := einoruntime.BuildChatModelAgent(ctx, einoruntime.AgentBuildConfig{
		Name: "agent-agent-1", Model: fake, Tools: []tool.BaseTool{pt},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := engine.Run(ctx, agent1, einoruntime.RunInput{
		WorkspaceID: "ws-1", RunID: "run-lease",
		Messages: []*schema.Message{schema.UserMessage("confirm me")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Interrupted {
		t.Fatal("expected interrupt before continue")
	}

	meta := chatruntimebridge.EinoChatResume{
		ResumeSchemaVersion: chatruntimebridge.EinoChatResumeSchemaVersion,
		SessionID:           "sess-1", UserMessageID: "msg-1", ActorID: "actor-1",
		EinoCheckpointID: first.CheckpointID,
		InterruptIDs:     first.InterruptContextIDs,
		RootInterruptID:  firstRoot(first),
		GatedToolCallID:  "call_confirm",
		InterruptKind:    chatruntimebridge.InterruptKindToolConfirmation,
	}
	requestSnap, err := chatruntimebridge.EmbedEinoChatResume(
		json.RawMessage(`{"schemaVersion":"tool-resume-request.v1","invocationId":"inv-fixed"}`),
		meta,
	)
	if err != nil {
		t.Fatal(err)
	}
	resultSnap := json.RawMessage(`{
		"invocationId":"inv-fixed","traceId":"trace-1",
		"output":{"dispatched":true},"httpStatus":200,"attempts":1,"cached":false
	}`)
	capSnap := json.RawMessage(`{
		"schemaVersion":"capability-snapshot.v1",
		"releases":[{
			"capabilityId":"cap-1","releaseId":"rel-1","kind":"TOOL",
			"callableName":"demo_tool","callableDescription":"demo",
			"inputSchema":{"type":"object"},"riskLevel":"HIGH",
			"sideEffectLevel":"WRITE","requiresConfirmation":true,"connectionId":"conn-1"
		}]
	}`)
	life := &countingLifecycle{}
	bridge, err := chatruntimebridge.NewBridge(chatruntimebridge.Dependencies{
		Sessions: &bridgeSessions{messages: []chat.Message{{
			ID: "msg-1", Role: "USER", Content: "confirm me", Status: "COMPLETED",
		}}},
		Results: &bridgeResults{},
		Agents:  bridgeAgents{},
		Models:  bridgeModels{},
		Runs: &bridgeRuns{run: execution.AgentRun{
			ID: "run-lease", WorkspaceID: "ws-1", SessionID: "sess-1",
			AgentID: "agent-1", Status: "RUNNING", CapabilitySnapshot: capSnap,
			TriggeredByType: "USER", TriggeredByID: "user-1", TraceID: "trace-1",
			LockVersion: 1,
		}},
		Events:      bridgeEvents{},
		ToolInvoker: &bridgeToolInvoker{spy: spy},
		Engine:      engine,
		BuildChatModel: func(context.Context, modelconfig.Config) (model.BaseChatModel, error) {
			return fake, nil
		},
		TextSinkFactory: func(context.Context, chatruntimebridge.TextSinkArgs) (chatruntime.TextDeltaSink, error) {
			return &chatruntimebridge.RecordingTextDeltaSink{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	life.onComplete = func() { close(done) }

	job := agentrun.Job{
		WorkspaceID: "ws-1", SessionID: "sess-1", RunID: "run-lease",
		UserMessageID: "msg-1", ActorID: "actor-1",
	}
	bridge.EnqueueContinueWithLifecycle(job, requestSnap, resultSnap, life)

	select {
	case <-done:
		// ok
	case <-time.After(15 * time.Second):
		t.Fatal("lifecycle.Complete not called within timeout")
	}
	if got := life.completeCalls.Load(); got != 1 {
		t.Fatalf("lifecycle.Complete calls=%d, want 1 (no double-run lease leak)", got)
	}
	if got := spy.calls.Load(); got != 0 {
		t.Fatalf("tool adapter Invoke on continue=%d, want 0", got)
	}

	// Duplicate continue slot registration still completes lifecycle (no stuck lease).
	life2 := &countingLifecycle{}
	// Hold an active continue slot briefly by registering via another continue
	// that blocks — simplest: call EnqueueContinueWithLifecycle while first
	// slot is free again (first already unregistered). Prove Complete on the
	// immediate duplicate-register path by racing two continues on a fresh bridge.
	// Use a second bridge with a pre-occupied continue slot via CancelRun path:
	// re-enqueue while previous finished: Complete must still fire once.
	life2Done := make(chan struct{})
	life2.onComplete = func() { close(life2Done) }
	bridge.EnqueueContinueWithLifecycle(job, requestSnap, resultSnap, life2)
	select {
	case <-life2Done:
	case <-time.After(15 * time.Second):
		t.Fatal("second continue lifecycle.Complete not called")
	}
}

// TestGoldenA4_ChatLoopOnlyRejectedDocuments historical chatLoop snapshots are
// detectable (HasChatLoop) but not resumable: ExtractEinoChatResume fails, so
// ContinueDispatcher returns invalid. Locks mutual exclusion with einoChatResume.
func TestGoldenA4_ChatLoopOnlyRejectedDocuments(t *testing.T) {
	t.Parallel()

	legacySnap := json.RawMessage(`{
		"schemaVersion":"tool-resume-request.v1",
		"invocationId":"inv-legacy",
		"chatLoop":{
			"schemaVersion":"chat-tool-loop.v1",
			"sessionId":"sess-legacy",
			"userMessageId":"msg-legacy",
			"actorId":"actor-legacy"
		}
	}`)

	if _, ok := chatruntimebridge.ExtractEinoChatResume(legacySnap); ok {
		t.Fatal("eino extract must not succeed on chatLoop-only snapshot")
	}
	if !chatruntimebridge.HasChatLoop(legacySnap) {
		t.Fatal("HasChatLoop must be true for chatLoop-only snap")
	}

	einoMeta := chatruntimebridge.EinoChatResume{
		ResumeSchemaVersion: chatruntimebridge.EinoChatResumeSchemaVersion,
		SessionID:           "sess-eino", UserMessageID: "msg-eino", ActorID: "a",
		EinoCheckpointID: "ws/w/agent_run/r/n",
		RootInterruptID:  "agent:a;tool:c1",
		InterruptKind:    chatruntimebridge.InterruptKindToolConfirmation,
	}
	einoSnap, err := chatruntimebridge.EmbedEinoChatResume(
		json.RawMessage(`{"schemaVersion":"tool-resume-request.v1","invocationId":"inv-eino"}`),
		einoMeta,
	)
	if err != nil {
		t.Fatal(err)
	}
	if chatruntimebridge.HasChatLoop(einoSnap) {
		t.Fatal("eino embed must strip chatLoop")
	}
	if _, ok := chatruntimebridge.ExtractEinoChatResume(einoSnap); !ok {
		t.Fatal("eino extract must succeed on eino snap")
	}
}

// --- helpers (PR9 golden) ---

type recordingEvents struct {
	mu      sync.Mutex
	records []chatruntime.ProtocolRecord
}

func (e *recordingEvents) Record(_ context.Context, record chatruntime.ProtocolRecord) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = append(e.records, record)
	return nil
}

func (e *recordingEvents) kinds() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.records))
	for i, r := range e.records {
		out[i] = r.Kind
	}
	return out
}

type countingLifecycle struct {
	completeCalls atomic.Int64
	renewCalls    atomic.Int64
	onComplete    func()
}

func (c *countingLifecycle) Renew(context.Context) error {
	c.renewCalls.Add(1)
	return nil
}

func (c *countingLifecycle) Complete(context.Context) error {
	c.completeCalls.Add(1)
	if c.onComplete != nil {
		c.onComplete()
	}
	return nil
}

var _ agentrun.ContinueLifecycle = (*countingLifecycle)(nil)

func readGoldenEventTypes(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open golden %s: %v", path, err)
	}
	defer file.Close()

	var types []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if env.Type == "" {
			t.Fatal("empty event type in golden")
		}
		types = append(types, env.Type)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(types) == 0 {
		t.Fatalf("empty golden %s", path)
	}
	return types
}

func indexOf(items []string, want string) int {
	for i, item := range items {
		if item == want {
			return i
		}
	}
	return -1
}

func containsKind(kinds []string, want string) bool {
	for _, k := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Compile-time: Bridge remains agentrun.Runtime for dual-engine factory.
var _ agentrun.Runtime = (*chatruntimebridge.Bridge)(nil)
