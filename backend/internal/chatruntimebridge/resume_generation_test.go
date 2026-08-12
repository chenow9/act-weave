package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"
)

// Both runtimes write into the one CheckPointStore that application.Open shares
// between them, and a checkpoint written by one cannot be restored by the other:
// the classic runner persists schema.Message state while the Agentic runner
// persists *schema.AgenticMessage state. Measured against the shared store, a
// classic agent resuming an Agentic checkpoint fails inside adk with
// "no child agents leading to interrupted agent were found" — no tool is
// invoked, but the run dies after the user has already approved and the reason
// names nothing an operator can act on.
//
// These tests pin the routing that replaces that: the runtime that pauses stamps
// itself into the confirmation, and resume refuses anything it cannot carry
// before it loads or builds a thing.

func resumeGenerationSnapshot(t *testing.T, generation string, stamp bool) json.RawMessage {
	t.Helper()
	meta := EinoChatResume{
		ResumeSchemaVersion: EinoChatResumeSchemaVersion,
		SessionID:           pauseSessionID,
		UserMessageID:       pauseMsgID,
		ActorID:             pauseUserID,
		EinoCheckpointID:    "ws/" + pauseWorkspaceID + "/agent_run/" + pauseRunID + "/nonce-gen",
		InterruptIDs:        []string{"agent:a;tool:call_1"},
		RootInterruptID:     "agent:a;tool:call_1",
		GatedToolCallID:     "call_1",
		InterruptKind:       InterruptKindToolConfirmation,
	}
	if stamp {
		meta.RuntimeGeneration = generation
	}
	snapshot, err := EmbedEinoChatResume(json.RawMessage(`{"schemaVersion":"tool-resume-request.v1"}`), meta)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	return snapshot
}

// resumeRoutingBridge returns a Bridge whose run is NOT resumable. Any leg that
// gets past generation routing therefore fails with "not resumable", and any leg
// stopped by routing fails with a generation error — which error comes back is
// the proof of ordering, without needing a spy on every downstream dependency.
func resumeRoutingBridge() *Bridge {
	return &Bridge{
		runs: &pauseRuns{run: execution.AgentRun{
			ID: pauseRunID, WorkspaceID: pauseWorkspaceID, SessionID: pauseSessionID,
			Status: "SUCCEEDED",
		}},
		now:             func() time.Time { return time.Now().UTC() },
		pendingConfirms: make(map[string][]einoruntime.PendingConfirmInterrupt),
	}
}

func resumeRoutingJob() agentrun.Job {
	return agentrun.Job{
		WorkspaceID: pauseWorkspaceID, SessionID: pauseSessionID, RunID: pauseRunID,
		UserMessageID: pauseMsgID, ActorID: pauseUserID,
	}
}

func TestResumeGeneration_RoutesBeforeLoadingAnything(t *testing.T) {
	t.Parallel()
	toolResult := json.RawMessage(`{"ok":true}`)

	for _, test := range []struct {
		name       string
		generation string
		stamp      bool
		wantErr    error
		// wantPastRouting means the leg is expected to get past generation routing
		// into the shared run load, which this bridge then refuses with
		// "not resumable" regardless of which runtime it would have dispatched to.
		wantPastRouting bool
	}{
		{
			name:       "unknown generation is refused before the run is loaded",
			generation: "quantum", stamp: true,
			wantErr: ErrAgenticResumeGenerationMismatch,
		},
		{
			name:       "classic checkpoint is refused after Task 9",
			generation: RuntimeGenerationClassic, stamp: true,
			wantErr: ErrClassicResumeRemoved,
		},
		{
			name:       "agentic checkpoint is no longer refused by routing",
			generation: RuntimeGenerationAgentic, stamp: true,
			wantPastRouting: true,
		},
		{
			// Absent marker still means classic, but classic resume is removed.
			name:    "absent marker refuses as classic removed",
			stamp:   false,
			wantErr: ErrClassicResumeRemoved,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot := resumeGenerationSnapshot(t, test.generation, test.stamp)
			err := resumeRoutingBridge().ContinueAfterConfirmation(
				context.Background(), resumeRoutingJob(), snapshot, toolResult)
			if err == nil {
				t.Fatal("expected an error from this fixture")
			}
			if test.wantPastRouting {
				if errors.Is(err, ErrAgenticResumeGenerationMismatch) {
					t.Fatalf("checkpoint was refused by generation routing: %v", err)
				}
				if errors.Is(err, ErrClassicResumeRemoved) {
					t.Fatalf("agentic checkpoint was refused as classic: %v", err)
				}
				if !strings.Contains(err.Error(), "not resumable") {
					t.Fatalf("expected the shared run load to reject the status, got %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("err = %v, want %v", err, test.wantErr)
			}
			// Reaching the run load would have produced "not resumable" instead,
			// so its absence proves routing ran first.
			if strings.Contains(err.Error(), "not resumable") {
				t.Fatalf("routing ran after the run was loaded: %v", err)
			}
		})
	}
}

func TestResumeGeneration_EffectiveGenerationDefaultsToClassic(t *testing.T) {
	t.Parallel()
	if got := (EinoChatResume{}).EffectiveRuntimeGeneration(); got != RuntimeGenerationClassic {
		t.Fatalf("absent marker = %q, want %q", got, RuntimeGenerationClassic)
	}
	if got := (EinoChatResume{RuntimeGeneration: "   "}).EffectiveRuntimeGeneration(); got != RuntimeGenerationClassic {
		t.Fatalf("blank marker = %q, want %q", got, RuntimeGenerationClassic)
	}
	// An unrecognised value must survive intact so routing can refuse it by name
	// instead of it being normalised into a runtime that would mis-restore.
	if got := (EinoChatResume{RuntimeGeneration: "quantum"}).EffectiveRuntimeGeneration(); got != "quantum" {
		t.Fatalf("unknown marker = %q, want it returned verbatim", got)
	}
}

// TestPauseForInterrupt_StampsGenerationAndRefusesUnknown closes the producer end
// of the contract: a confirmation whose checkpoint cannot be routed back to a
// runtime would be unresumable for the entire life of the run, so it must never
// be persisted in the first place.
func TestPauseForInterrupt_StampsGenerationAndRefusesUnknown(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		generation string
		wantStamp  bool
	}{
		{name: "classic is stamped", generation: RuntimeGenerationClassic, wantStamp: true},
		{name: "agentic is stamped", generation: RuntimeGenerationAgentic, wantStamp: true},
		{name: "empty is refused", generation: ""},
		{name: "unknown is refused", generation: "quantum"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			confirms, bridge, run := newPauseGenerationHarness(t)
			interruptID := "agent:hitl-agent;tool:call_confirm"
			cpID := "ws/" + pauseWorkspaceID + "/agent_run/" + pauseRunID + "/nonce-stamp"
			err := bridge.pauseForInterrupt(context.Background(), resumeRoutingJob(), run,
				&einoruntime.RunResult{
					CheckpointID:          cpID,
					Interrupted:           true,
					InterruptContextIDs:   []string{interruptID},
					RootCauseInterruptIDs: []string{interruptID},
				}, test.generation)

			if !test.wantStamp {
				if err == nil {
					t.Fatal("an unroutable generation must not produce a confirmation")
				}
				if len(confirms.input.Resume.RequestSnapshot) != 0 {
					t.Fatalf("a confirmation was persisted anyway: %s",
						string(confirms.input.Resume.RequestSnapshot))
				}
				return
			}
			if err != nil {
				t.Fatalf("pauseForInterrupt: %v", err)
			}
			meta, ok := ExtractEinoChatResume(confirms.input.Resume.RequestSnapshot)
			if !ok {
				t.Fatalf("no einoChatResume in %s", string(confirms.input.Resume.RequestSnapshot))
			}
			if got := meta.EffectiveRuntimeGeneration(); got != test.generation {
				t.Fatalf("stamped generation = %q, want %q", got, test.generation)
			}

			// Closure: the persisted confirmation routes back to the runtime that
			// wrote it. This is the pause → persist → resume path, not two
			// independent unit assertions.
			routeErr := resumeRoutingBridge().ContinueAfterConfirmation(
				context.Background(), resumeRoutingJob(),
				confirms.input.Resume.RequestSnapshot, json.RawMessage(`{"ok":true}`))
			if errors.Is(routeErr, ErrAgenticResumeGenerationMismatch) {
				t.Fatalf("a stamped pause was refused as unroutable: %v", routeErr)
			}
		})
	}
}

func newPauseGenerationHarness(t *testing.T) (*pauseConfirmations, *Bridge, execution.AgentRun) {
	t.Helper()
	capSnap := json.RawMessage(`{
		"schemaVersion":"capability-snapshot.v1",
		"releases":[{
			"capabilityId":"cap-1","releaseId":"rel-1","kind":"TOOL",
			"callableName":"demo_tool","callableDescription":"demo",
			"inputSchema":{"type":"object"},"riskLevel":"HIGH",
			"sideEffectLevel":"WRITE","requiresConfirmation":true,"connectionId":"conn-1"
		}]
	}`)
	principalSnap, err := principal.NewInternalExecutionSnapshot(
		pauseWorkspaceID, principal.TypeUser, pauseUserID)
	if err != nil {
		t.Fatalf("principal: %v", err)
	}
	run := execution.AgentRun{
		ID: pauseRunID, WorkspaceID: pauseWorkspaceID, SessionID: pauseSessionID,
		Status: "RUNNING", CapabilitySnapshot: capSnap, LockVersion: 3,
		TriggeredByType: "USER", TriggeredByID: pauseUserID, TraceID: "trace-1",
		PrincipalSnapshot: principalSnap,
	}
	confirms := &pauseConfirmations{}
	bridge := &Bridge{
		sessions:        pauseSessions{},
		runs:            &pauseRuns{run: run},
		events:          &pauseEvents{},
		toolInvoker:     pauseToolInvoker{},
		confirmations:   confirms,
		checkpointTTL:   &touchTTL{},
		now:             func() time.Time { return time.Now().UTC() },
		pendingConfirms: make(map[string][]einoruntime.PendingConfirmInterrupt),
	}
	bridge.recordPending(pendingConfirmKey(pauseWorkspaceID, pauseRunID),
		einoruntime.PendingConfirmInterrupt{
			ToolName: "demo_tool", CapabilityID: "cap-1", InvocationID: "inv-fixed",
			StepID: "step-1", ArgsJSON: `{"x":1}`,
		})
	return confirms, bridge, run
}

func TestExecutionErrorCode_ResumeGenerationErrors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		err  error
		want string
	}{
		{ErrAgenticResumeGenerationMismatch, "AGENTIC_RESUME_GENERATION_MISMATCH"},
		{ErrAgenticResumeClassicOnFrozenRun, "AGENTIC_RESUME_CLASSIC_ON_FROZEN_RUN"},
		{ErrClassicResumeRemoved, "CLASSIC_RESUME_REMOVED"},
	} {
		if got := executionErrorCode(test.err); got != test.want {
			t.Fatalf("executionErrorCode(%v) = %q, want %q", test.err, got, test.want)
		}
		// Routing wraps the sentinel; the code must survive wrapping or the
		// operator sees INVOCATION_FAILED instead.
		wrapped := fmt.Errorf("outer: %w", test.err)
		if got := executionErrorCode(wrapped); got != test.want {
			t.Fatalf("executionErrorCode(wrapped %v) = %q, want %q", test.err, got, test.want)
		}
	}
}

// TestPauseCallSitesDeclareTheirGeneration guards the one link the behavioural
// tests above cannot reach: that each production pause passes the constant for
// the runtime it actually ran on. A wrong constant here would stamp a resumable
// marker onto an unresumable checkpoint, which no amount of routing can detect.
func TestPauseCallSitesDeclareTheirGeneration(t *testing.T) {
	t.Parallel()
	const call = "b.pauseForInterrupt("
	expected := map[string]string{
		"agentic_turn.go": "RuntimeGenerationAgentic",
	}
	counterpart := map[string]string{
		"RuntimeGenerationAgentic": "RuntimeGenerationClassic",
		"RuntimeGenerationClassic": "RuntimeGenerationAgentic",
	}

	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	seen := map[string]bool{}
	for _, file := range sources {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(source)
		if !strings.Contains(text, call) {
			continue
		}
		// A pause outside the two known runtimes would be stamped by whatever
		// constant its author picked, with nothing to catch a wrong choice.
		want, known := expected[file]
		if !known {
			t.Fatalf("%s pauses for confirmation but declares no runtime generation", file)
		}
		seen[file] = true
		for idx := 0; ; {
			at := strings.Index(text[idx:], call)
			if at < 0 {
				break
			}
			start := idx + at
			idx = start + len(call)
			// The constant is an argument of this call, so it may sit several
			// lines below the callee on a multi-line literal.
			end := start + 600
			if end > len(text) {
				end = len(text)
			}
			window := text[start:end]
			if !strings.Contains(window, want) {
				t.Fatalf("%s pauses without %s", file, want)
			}
			if strings.Contains(window, counterpart[want]) {
				t.Fatalf("%s pauses with the other runtime's generation", file)
			}
		}
	}
	for file := range expected {
		if !seen[file] {
			t.Fatalf("no pauseForInterrupt call found in %s", file)
		}
	}
}

// resumeDispatchAgents fails every agent load with a recognisable error. The
// classic seam loads the agent as its very first act, while the Agentic resume
// refuses a missing model builder before it ever gets there, so two
// distinguishable failures make the dispatch target observable without a real
// model, engine or upstream.
type resumeDispatchAgents struct{}

var errResumeDispatchClassicSeam = errors.New("classic-seam-loaded-the-live-agent")

func (resumeDispatchAgents) Get(context.Context, string, string) (agent.Agent, error) {
	return agent.Agent{}, errResumeDispatchClassicSeam
}

func (resumeDispatchAgents) ListPromptRevisions(
	context.Context, string, string,
) ([]agent.PromptRevision, error) {
	return nil, errResumeDispatchClassicSeam
}

// TestResumeGeneration_DispatchesToTheRuntimeThatPaused is the half of routing
// that "was it refused?" cannot see: that an accepted marker reaches the runtime
// it names. Sending an Agentic checkpoint to the classic seam is the failure this
// whole mechanism exists to prevent, and it is indistinguishable from correct
// behaviour until the confirmation is actually driven.
func TestResumeGeneration_DispatchesToTheRuntimeThatPaused(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		generation   string
		wantContains string
	}{
		{
			name:       "agentic checkpoint drives the agentic resume",
			generation: RuntimeGenerationAgentic,
			// Reached driveAgenticResume: only it refuses on the Agentic builder.
			wantContains: "agentic model builder is not configured",
		},
		{
			name:         "classic checkpoint is refused before any live read",
			generation:   RuntimeGenerationClassic,
			wantContains: ErrClassicResumeRemoved.Error(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			bridge := &Bridge{
				runs: &pauseRuns{run: execution.AgentRun{
					ID: pauseRunID, WorkspaceID: pauseWorkspaceID, SessionID: pauseSessionID,
					AgentID: "11111111-1111-4111-8111-111111111111", Status: "RUNNING",
				}},
				agents:          resumeDispatchAgents{},
				now:             func() time.Time { return time.Now().UTC() },
				pendingConfirms: make(map[string][]einoruntime.PendingConfirmInterrupt),
			}
			err := bridge.ContinueAfterConfirmation(
				context.Background(), resumeRoutingJob(),
				resumeGenerationSnapshot(t, test.generation, true),
				json.RawMessage(`{"ok":true}`))
			if err == nil {
				t.Fatal("expected the fixture to fail inside the dispatched runtime")
			}
			if !strings.Contains(err.Error(), test.wantContains) {
				t.Fatalf("dispatched to the wrong runtime: err = %v, want it to contain %q",
					err, test.wantContains)
			}
		})
	}
}
