package chatruntimebridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"
)

const (
	testResumeUserUUID     = "d66ce000-0000-4000-8000-00000000000a"
	testResumeProviderUUID = "e77ce000-0000-4000-8000-00000000000b"
)

// confirmCapSnap is a capability snapshot whose single tool requires
// confirmation, so the first tool call pauses the run instead of executing.
func confirmCapSnap() json.RawMessage {
	return json.RawMessage(`{
		"schemaVersion":"capability-snapshot.v1",
		"releases":[{
			"capabilityId":"` + testCapUUID + `","releaseId":"` + testRelUUID + `","kind":"TOOL",
			"callableName":"wire_money","callableDescription":"move funds",
			"inputSchema":{"type":"object","properties":{"q":{"type":"string"}}},"outputSchema":{},
			"riskLevel":"HIGH","sideEffectLevel":"WRITE",
			"requiresConfirmation":true,"connectionId":"` + testConnUUID + `"
		}]
	}`)
}

func agenticToolCall(callID, name, args string) *schema.AgenticMessage {
	return &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{
				CallID: callID, Name: name, Arguments: args,
			}),
		},
	}
}

// resumeToolInvoker resolves the gated tool completely enough for the real
// confirmation policy, and counts invocations: an approved tool must be executed
// by the checkpoint's tool result on resume, never invoked a second time here.
type resumeToolInvoker struct {
	invocations atomic.Int64
}

func (i *resumeToolInvoker) ResolveInvocation(
	_ context.Context, req execution.ResolveRequest,
) (execution.ResolvedInvocation, error) {
	return execution.ResolvedInvocation{
		Snapshot: execution.ReleaseSnapshot{
			WorkspaceID: req.WorkspaceID, CapabilityID: req.CapabilityID,
			ReleaseID: req.ReleaseID, ProviderID: testResumeProviderUUID,
		},
		Connection: execution.ConnectionSnapshot{
			ID: testConnUUID, WorkspaceID: req.WorkspaceID, Environment: "TEST",
			ProviderID: testResumeProviderUUID,
		},
		RequiresConfirmation: true,
		RiskLevel:            "HIGH",
		SideEffectLevel:      "WRITE",
	}, nil
}

func (i *resumeToolInvoker) InvokeResolved(
	context.Context, execution.InvokeRequest, execution.ResolvedInvocation,
) (execution.PipelineResult, error) {
	i.invocations.Add(1)
	return execution.PipelineResult{}, nil
}

type resumeConfirmations struct {
	mu    sync.Mutex
	input chat.PrepareChatConfirmationInput
}

func (p *resumeConfirmations) Prepare(
	_ context.Context, input chat.PrepareChatConfirmationInput,
) (chat.PreparedChatConfirmation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.input = input
	return chat.PreparedChatConfirmation{
		Confirmation: chat.ChatConfirmation{
			ID: input.ID, WorkspaceID: input.WorkspaceID, SessionID: input.SessionID,
			RunID: input.Resume.Confirmation.RunID, Status: "PENDING",
			RiskLevel: input.RiskLevel, ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
		Prepared: execution.PreparedConfirmationResume{
			Checkpoint: execution.ConfirmationResumeCheckpoint{
				ConfirmationID:  input.Resume.Confirmation.ID,
				RequestSnapshot: input.Resume.RequestSnapshot,
			},
		},
	}, nil
}

func (p *resumeConfirmations) snapshot() json.RawMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.input.Resume.RequestSnapshot
}

type resumeTTL struct{}

func (resumeTTL) TouchExpiresAt(context.Context, string, time.Time) error { return nil }

// agenticHITLFixture is one run that pauses on a confirmation-required tool and
// is then resumed. The checkpoint store is shared by both engines exactly as
// application.Open shares it, so nothing about the restore is simulated.
type agenticHITLFixture struct {
	f       *agenticFixture
	bridge  *chatruntimebridge.Bridge
	confirm *resumeConfirmations
	runs    *agenticRuns
	invoker *resumeToolInvoker
}

// lastContent is the assistant text the run recorded on completion.
func (r *agenticResults) lastContent() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.content
}

func newAgenticHITLFixture(t *testing.T, responses []*schema.AgenticMessage) *agenticHITLFixture {
	t.Helper()
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.run.CapabilitySnapshot = confirmCapSnap()
		snap, err := principal.NewInternalExecutionSnapshot(
			testWSUUID, principal.TypeUser, testResumeUserUUID)
		if err != nil {
			t.Fatalf("principal: %v", err)
		}
		f.run.PrincipalSnapshot = snap
		f.run.TriggeredByID = testResumeUserUUID
		f.mdl.responses = responses
	})
	confirm := &resumeConfirmations{}
	invoker := &resumeToolInvoker{}
	runs := &agenticRuns{run: f.run}
	bridge, err := chatruntimebridge.NewBridge(chatruntimebridge.Dependencies{
		Sessions:          &agenticSessions{messages: f.messages},
		Results:           f.results,
		Agents:            f.agents,
		Models:            agenticModels{cfg: f.cfg},
		Runs:              runs,
		Events:            f.events,
		ToolInvoker:       invoker,
		AgenticEngine:     einoruntime.NewAgenticEngine(einoruntime.AgenticEngineConfig{Store: f.store}),
		BuildAgenticModel: f.agentic.Build,
		Assemblies:        f.assemblies,
		TextSinkFactory:   f.sinks.Factory,
		Confirmations:     confirm,
		CheckpointTTL:     resumeTTL{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &agenticHITLFixture{f: f, bridge: bridge, confirm: confirm, runs: runs, invoker: invoker}
}

// job carries the actor the principal snapshot names: the tool confirmation
// snapshots are only valid when the two agree.
func (h *agenticHITLFixture) job() agentrun.Job {
	job := h.f.job()
	job.ActorID = testResumeUserUUID
	return job
}

// pause runs the initial turn and returns the persisted confirmation snapshot.
func (h *agenticHITLFixture) pause(t *testing.T) json.RawMessage {
	t.Helper()
	err := h.bridge.Execute(context.Background(), h.job())
	if !errors.Is(err, chatruntimebridge.ErrWaitingConfirmation) {
		t.Fatalf("initial turn did not pause for confirmation: %v", err)
	}
	snapshot := h.confirm.snapshot()
	if len(snapshot) == 0 {
		t.Fatal("no confirmation request snapshot was persisted")
	}
	return snapshot
}

// TestAgenticResume_ConfirmedToolCompletesTheRun is the functional hole 4B-2
// closes. Before it, an Agentic run that paused for confirmation was handed to
// the classic seam on approval: the classic runner rebuilt a schema.Message
// agent and adk refused the restore with "no child agents leading to interrupted
// agent were found", so approving a tool killed the run. Nothing in the Agentic
// initial path could observe that, because the damage happened one request later.
func TestAgenticResume_ConfirmedToolCompletesTheRun(t *testing.T) {
	h := newAgenticHITLFixture(t, []*schema.AgenticMessage{
		agenticToolCall("call_1", "wire_money", `{"q":"x"}`),
		agenticmsg.AssistantText("transfer done"),
	})
	snapshot := h.pause(t)

	meta, ok := chatruntimebridge.ExtractEinoChatResume(snapshot)
	if !ok {
		t.Fatalf("no einoChatResume in %s", snapshot)
	}
	if got := meta.EffectiveRuntimeGeneration(); got != chatruntimebridge.RuntimeGenerationAgentic {
		t.Fatalf("checkpoint generation = %q, want %q",
			got, chatruntimebridge.RuntimeGenerationAgentic)
	}

	callsBefore := h.f.mdl.calls.Load()
	if err := h.bridge.ContinueAfterConfirmation(context.Background(), h.job(),
		snapshot, json.RawMessage(`{"ok":true,"transferred":42}`)); err != nil {
		t.Fatalf("ContinueAfterConfirmation: %v", err)
	}
	if h.f.mdl.calls.Load() <= callsBefore {
		t.Fatal("the resume never reached the model, so no restore was proven")
	}
	// The classic seam is the failure mode this path replaces: it must not be
	// entered even to build a model.
	if n := h.f.classic.calls.Load(); n != 0 {
		t.Fatalf("classic chat model builder was called %d times during an agentic resume", n)
	}
	if got := h.f.results.lastContent(); !strings.Contains(got, "transfer done") {
		t.Fatalf("resumed run recorded %q, want the post-confirmation answer", got)
	}
	// The approved tool's outcome travels in the checkpoint's resume target. A
	// second execution here would mean the user's single approval moved funds
	// twice — the reason resume feeds a result instead of re-running the tool.
	if n := h.invoker.invocations.Load(); n != 0 {
		t.Fatalf("the confirmed tool was invoked %d times during resume, want 0", n)
	}
}

// TestAgenticResume_RebuildsTheAgentThatPaused pins the invariant that makes a
// restore legitimate. adk resumes into a freshly built agent, so a rebuild that
// differs from the paused one resumes a half-executed conversation against a
// different wire. The system prompt is the observable edge of that: the frozen
// prompt already leads the checkpointed conversation, so passing it as
// Instruction on either side would put a second copy on every resumed turn —
// the D-3 defect, reintroduced on a path its initial-turn test cannot see.
func TestAgenticResume_RebuildsTheAgentThatPaused(t *testing.T) {
	h := newAgenticHITLFixture(t, []*schema.AgenticMessage{
		agenticToolCall("call_1", "wire_money", `{"q":"x"}`),
		agenticmsg.AssistantText("transfer done"),
	})
	snapshot := h.pause(t)
	if err := h.bridge.ContinueAfterConfirmation(context.Background(), h.job(),
		snapshot, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatalf("ContinueAfterConfirmation: %v", err)
	}

	input := h.f.mdl.inputSnapshot()
	if len(input) == 0 {
		t.Fatal("resume produced no model input")
	}
	systemMessages := 0
	for i, msg := range input {
		if msg == nil {
			t.Fatalf("resume model input %d is nil", i)
		}
		if msg.Role != schema.AgenticRoleTypeSystem {
			continue
		}
		systemMessages++
		if i != 0 {
			t.Errorf("system message at index %d of the resumed input; the frozen prompt must lead", i)
		}
	}
	if systemMessages != 1 {
		raw, _ := json.Marshal(input)
		t.Fatalf("resumed input carries %d system messages, want exactly 1: %s", systemMessages, raw)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(raw), testFrozenPrompt); got != 1 {
		t.Fatalf("frozen prompt appears %d times in the resumed input, want exactly 1: %s", got, raw)
	}
}

// TestAgenticResume_ValidatesFrozenSnapshotsBeforeRestoring guards the property
// that separates this path from the classic seam: a resume derives everything
// from the same frozen documents as the turn that paused, so a corrupted frozen
// document must stop the restore rather than being repaired from live state.
func TestAgenticResume_ValidatesFrozenSnapshotsBeforeRestoring(t *testing.T) {
	h := newAgenticHITLFixture(t, []*schema.AgenticMessage{
		agenticToolCall("call_1", "wire_money", `{"q":"x"}`),
		agenticmsg.AssistantText("transfer done"),
	})
	snapshot := h.pause(t)

	// Corrupt the frozen graph the way a forged or partially written run would be.
	// The classic seam would not have looked at it at all.
	h.runs.run.AgentGraphSnapshot = json.RawMessage(`{"schemaVersion":"agent-graph-snapshot.v1"}`)
	callsBefore := h.f.mdl.calls.Load()
	err := h.bridge.ContinueAfterConfirmation(context.Background(), h.job(),
		snapshot, json.RawMessage(`{"ok":true}`))
	if err == nil {
		t.Fatal("a corrupted frozen graph resumed anyway")
	}
	if h.f.mdl.calls.Load() != callsBefore {
		t.Fatal("the model was called despite an invalid frozen graph")
	}
}
