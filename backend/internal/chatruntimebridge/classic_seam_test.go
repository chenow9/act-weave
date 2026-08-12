package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/execution"
)

// Guards for the classic seam's containment (Task 4B-4).
//
// The chat path now has two turns and both are Agentic. What remains of the
// classic ChatModelAgent runtime is one legacy seam, kept only until Task 9 so
// that a confirmation which was already pending when the runtime changed can
// still be approved. It is the only place in the chat path allowed to read live
// agent and model config, which is exactly why its reachability has to be pinned:
// a turn that has frozen documents and reaches it instead resumes a half-executed
// conversation against whatever config happens to be current.

// TestClassicSeam_RefusesATurnWithoutResumeTargets is what keeps the seam off an
// initial turn. An initial chat turn has no checkpoint and no resume targets, so
// a future entry point that reaches for this function instead of the Agentic path
// fails closed here rather than quietly running the frozen path against live
// config. The refusal must land before any live read, or the damage is already
// done by the time it is reported: resumeDispatchAgents fails every live agent
// load with its own recognisable error, so a refusal that arrives after the read
// is reported as that error instead of this one.
func TestClassicSeam_RefusesATurnWithoutResumeTargets(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		checkpointID string
		targets      map[string]any
		wantContains string
	}{
		{
			name:         "no checkpoint",
			checkpointID: "  ",
			targets:      map[string]any{"interrupt-1": map[string]any{"ok": true}},
			wantContains: "checkpoint id is required",
		},
		{
			name:         "nil targets, as an initial turn would pass",
			checkpointID: "ws/w/agent_run/r/n1",
			targets:      nil,
			wantContains: "resume targets are required",
		},
		{
			name:         "empty targets",
			checkpointID: "ws/w/agent_run/r/n1",
			targets:      map[string]any{},
			wantContains: "resume targets are required",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			bridge := &Bridge{agents: resumeDispatchAgents{}}
			_, _, err := bridge.driveClassicResume(
				context.Background(),
				agentrun.Job{WorkspaceID: pauseWorkspaceID, RunID: pauseRunID},
				execution.AgentRun{ID: pauseRunID, WorkspaceID: pauseWorkspaceID, Status: "RUNNING"},
				test.checkpointID, test.targets)
			if err == nil {
				t.Fatal("the classic seam ran a turn that carries no checkpoint to restore")
			}
			if !strings.Contains(err.Error(), test.wantContains) {
				t.Fatalf("err = %v, want it to mention %q", err, test.wantContains)
			}
		})
	}
}

// TestClassicSeam_RefusesAFrozenRunBeforeLiveReads closes the gap left by an
// absent RuntimeGeneration marker. EffectiveRuntimeGeneration still defaults
// unmarked confirmations to classic (pre-Agentic in-flight must keep working),
// but Agentic pauses written before generation stamping also omit the marker —
// and those runs carry a frozen agent_graph_snapshot. Without this refusal the
// seam would load live config and then die inside adk after the user approved.
func TestClassicSeam_RefusesAFrozenRunBeforeLiveReads(t *testing.T) {
	t.Parallel()
	bridge := &Bridge{agents: resumeDispatchAgents{}}
	run := execution.AgentRun{
		ID: pauseRunID, WorkspaceID: pauseWorkspaceID, Status: "RUNNING",
		// Any non-empty freeze blob counts: root chat writes a full
		// agent_graph_snapshot.v1, never {} / null.
		AgentGraphSnapshot: json.RawMessage(`{"schemaVersion":"agent-graph-snapshot.v1"}`),
	}
	_, _, err := bridge.driveClassicResume(
		context.Background(),
		agentrun.Job{WorkspaceID: pauseWorkspaceID, RunID: pauseRunID},
		run,
		"ws/"+pauseWorkspaceID+"/agent_run/"+pauseRunID+"/nonce",
		map[string]any{"interrupt-1": map[string]any{"ok": true}},
	)
	if !errors.Is(err, ErrAgenticResumeClassicOnFrozenRun) {
		t.Fatalf("err = %v, want ErrAgenticResumeClassicOnFrozenRun", err)
	}
	if errors.Is(err, errResumeDispatchClassicSeam) {
		t.Fatal("the classic seam read live agent config before refusing the frozen run")
	}
	if got := executionErrorCode(err); got != "AGENTIC_RESUME_CLASSIC_ON_FROZEN_RUN" {
		t.Fatalf("executionErrorCode = %q", got)
	}
}

// TestClassicSeam_StillServesRunsWithoutAFrozenGraph is the other side: a true
// pre-Agentic confirmation has no freeze document, and the seam must still
// reach its live reads for that population until Task 9 removes it.
func TestClassicSeam_StillServesRunsWithoutAFrozenGraph(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"missing", "null", "{}"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			bridge := &Bridge{agents: resumeDispatchAgents{}}
			run := execution.AgentRun{
				ID: pauseRunID, WorkspaceID: pauseWorkspaceID, Status: "RUNNING",
			}
			switch name {
			case "null":
				run.AgentGraphSnapshot = json.RawMessage(`null`)
			case "{}":
				run.AgentGraphSnapshot = json.RawMessage(`{}`)
			}
			_, _, err := bridge.driveClassicResume(
				context.Background(),
				agentrun.Job{WorkspaceID: pauseWorkspaceID, RunID: pauseRunID},
				run,
				"ws/"+pauseWorkspaceID+"/agent_run/"+pauseRunID+"/nonce",
				map[string]any{"interrupt-1": map[string]any{"ok": true}},
			)
			if !errors.Is(err, errResumeDispatchClassicSeam) {
				t.Fatalf("err = %v, want the live-agent probe so the seam still serves classic runs", err)
			}
		})
	}
}

// TestClassicSeam_HasExactlyOneCaller pins the containment structurally. The
// seam's live config reads are legitimate only for a checkpoint that predates
// frozen identity, and ContinueAfterConfirmation establishes that by inspecting
// the checkpoint's runtime generation first. A second caller would be a second
// place where that precondition has to be re-established correctly — and the
// failure of getting it wrong is silent: the run resumes, against the wrong
// config, and only the answer is subtly different.
func TestClassicSeam_HasExactlyOneCaller(t *testing.T) {
	t.Parallel()
	callers := countCallSites(t, "b.driveClassicResume(")
	if callers != 1 {
		t.Fatalf("the classic seam has %d callers, want exactly 1 (ContinueAfterConfirmation's classic branch)", callers)
	}
}

// TestChatInitialEntryNamesTheAgenticRuntime pins the other half of the 4B-4
// closure. Execute used to call the classic seam, which dispatched to the
// Agentic path from inside its own first lines: anything added above that
// dispatch — a live agent read, a live model read — silently ran on the frozen
// path too. The initial turn now names its runtime at the entry point.
func TestChatInitialEntryNamesTheAgenticRuntime(t *testing.T) {
	t.Parallel()
	body := functionBody(t, "func (b *Bridge) Execute(")
	if !strings.Contains(body, "driveAgenticInitial") {
		t.Errorf("Execute no longer names the Agentic runtime:\n%s", body)
	}
	if strings.Contains(body, "driveClassicResume") {
		t.Errorf("the initial chat turn can reach the classic seam:\n%s", body)
	}
}

// TestAgenticTurnsBuildTheAgentInKnownPlaces: root turns share
// buildAgenticAgentFromPlan (4B-2); Task 5 children use buildAgenticChildAgent.
// A third call site would be a drift risk (resume vs initial, or classic rebuild).
func TestAgenticTurnsBuildTheAgentInKnownPlaces(t *testing.T) {
	t.Parallel()
	sites := countCallSites(t, "einoruntime.BuildAgenticAgent(")
	if sites != 2 {
		t.Fatalf("the typed agent is built in %d places, want exactly 2 (root plan + child)", sites)
	}
	root := strings.Count(functionBody(t, "func (b *Bridge) buildAgenticAgentFromPlan("), "einoruntime.BuildAgenticAgent(")
	child := strings.Count(functionBody(t, "func (b *Bridge) buildAgenticChildAgent("), "einoruntime.BuildAgenticAgent(")
	if root != 1 || child != 1 {
		t.Fatalf("root builder sites=%d child builder sites=%d, want 1 each", root, child)
	}
}

// TestAgenticTurnBindsTheTrustedWorkspaceOnce keeps the checkpoint store's tenant
// cross-check at the single seam both Agentic turns pass through. The check is a
// no-op when the binding is absent, so an entry point that forgets it loses the
// guard silently — which is how 4B-4 dropped it from the initial turn while
// moving the dispatch out of drive(). Binding it once, where both paths must go,
// also gives it to whatever delegation or SmartDAG entry reuses runAgenticTurn.
func TestAgenticTurnBindsTheTrustedWorkspaceOnce(t *testing.T) {
	t.Parallel()
	const binding = "einoruntime.WithTrustedWorkspaceID"
	if got := strings.Count(functionBody(t, "func (b *Bridge) runAgenticTurn("), binding); got != 1 {
		t.Fatalf("runAgenticTurn binds the trusted workspace %d times, want exactly 1", got)
	}
	for _, decl := range []string{
		"func (b *Bridge) driveAgenticInitial(",
		"func (b *Bridge) driveAgenticResume(",
	} {
		body := functionBody(t, decl)
		if got := strings.Count(body, binding); got != 0 {
			t.Errorf("%s binds the trusted workspace itself (%d occurrences); the binding "+
				"belongs to runAgenticTurn so the two Agentic paths cannot diverge", decl, got)
		}
		if got := strings.Count(body, "b.runAgenticTurn("); got != 1 {
			t.Errorf("%s reaches runAgenticTurn %d times, want exactly 1: a path that "+
				"bypasses it also bypasses the binding", decl, got)
		}
	}
}

// countCallSites counts occurrences of a call expression across the package's
// non-test sources.
func countCallSites(t *testing.T, expression string) int {
	t.Helper()
	total := 0
	for _, name := range packageSources(t) {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		total += strings.Count(string(source), expression)
	}
	return total
}

// functionBody returns the source of the function whose declaration starts with
// declaration, so a guard can assert on what that function reaches.
func functionBody(t *testing.T, declaration string) string {
	t.Helper()
	for _, name := range packageSources(t) {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		at := strings.Index(text, declaration)
		if at < 0 {
			continue
		}
		depth := 0
		for i := at; i < len(text); i++ {
			switch text[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return text[at : i+1]
				}
			}
		}
		t.Fatalf("%s: unbalanced braces after %q", name, declaration)
	}
	t.Fatalf("no function declared as %q; this guard has stopped guarding anything", declaration)
	return ""
}

func packageSources(t *testing.T) []string {
	t.Helper()
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, name := range entries {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		t.Fatal("no package sources found")
	}
	return out
}
