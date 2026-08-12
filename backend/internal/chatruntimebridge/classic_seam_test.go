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
)

// Task 9: the classic ChatModelAgent seam is gone. These guards pin the
// refusal surface and the absence of classic entry points in production code.

func TestClassicResumeIsRemoved(t *testing.T) {
	t.Parallel()
	bridge := &Bridge{}
	err := bridge.ContinueAfterConfirmation(
		context.Background(),
		agentrun.Job{WorkspaceID: pauseWorkspaceID, RunID: pauseRunID},
		mustEmbedClassicResume(t),
		json.RawMessage(`{"ok":true}`),
	)
	if !errors.Is(err, ErrClassicResumeRemoved) {
		t.Fatalf("err = %v, want ErrClassicResumeRemoved", err)
	}
	if got := executionErrorCode(err); got != "CLASSIC_RESUME_REMOVED" {
		t.Fatalf("executionErrorCode = %q", got)
	}
}

func TestAbsentRuntimeGenerationRefusesAsClassicRemoved(t *testing.T) {
	t.Parallel()
	meta := EinoChatResume{
		ResumeSchemaVersion: EinoChatResumeSchemaVersion,
		SessionID:           "s",
		UserMessageID:       "u",
		ActorID:             "a",
		EinoCheckpointID:    "ws/" + pauseWorkspaceID + "/agent_run/" + pauseRunID + "/n",
		InterruptIDs:        []string{"i1"},
		RootInterruptID:     "i1",
		InterruptKind:       InterruptKindToolConfirmation,
		// RuntimeGeneration intentionally omitted.
	}
	snap, err := EmbedEinoChatResume(json.RawMessage(`{"schemaVersion":"tool-resume-request.v1"}`), meta)
	if err != nil {
		t.Fatal(err)
	}
	err = (&Bridge{}).ContinueAfterConfirmation(
		context.Background(),
		agentrun.Job{WorkspaceID: pauseWorkspaceID, RunID: pauseRunID},
		snap,
		json.RawMessage(`{"ok":true}`),
	)
	if !errors.Is(err, ErrClassicResumeRemoved) {
		t.Fatalf("err = %v, want ErrClassicResumeRemoved for unmarked resume", err)
	}
}

func mustEmbedClassicResume(t *testing.T) json.RawMessage {
	t.Helper()
	meta := EinoChatResume{
		ResumeSchemaVersion: EinoChatResumeSchemaVersion,
		SessionID:           "s",
		UserMessageID:       "u",
		ActorID:             "a",
		EinoCheckpointID:    "ws/" + pauseWorkspaceID + "/agent_run/" + pauseRunID + "/n",
		InterruptIDs:        []string{"i1"},
		RootInterruptID:     "i1",
		InterruptKind:       InterruptKindToolConfirmation,
		RuntimeGeneration:   RuntimeGenerationClassic,
	}
	snap, err := EmbedEinoChatResume(json.RawMessage(`{"schemaVersion":"tool-resume-request.v1"}`), meta)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestProductionCodeHasNoClassicSeamSymbols(t *testing.T) {
	t.Parallel()
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"driveClassicResume",
		"attachDelegationTools(",
		"BuildChatModel",
		"func NewEinoOpenAIChatModel",
		"BuildChatModelAgent(",
	}
	for _, name := range entries {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, needle := range forbidden {
			if strings.Contains(text, needle) {
				t.Errorf("%s still contains %q after Task 9", name, needle)
			}
		}
	}
}

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

func TestAgenticTurnBindsTheTrustedWorkspaceOnce(t *testing.T) {
	t.Parallel()
	const binding = "einoruntime.WithTrustedWorkspaceID"
	if got := strings.Count(functionBody(t, "func (b *Bridge) runAgenticTurn("), binding); got != 1 {
		t.Fatalf("runAgenticTurn binds trusted workspace %d times, want 1", got)
	}
	for _, name := range []string{
		"func (b *Bridge) driveAgenticInitial(",
		"func (b *Bridge) driveAgenticResume(",
	} {
		if strings.Contains(functionBody(t, name), binding) {
			t.Errorf("%s must not re-bind trusted workspace (owned by runAgenticTurn)", name)
		}
	}
}

func TestContinueAfterConfirmationNeverCallsClassic(t *testing.T) {
	t.Parallel()
	body := functionBody(t, "func (b *Bridge) ContinueAfterConfirmation(")
	if strings.Contains(body, "driveClassicResume") {
		t.Fatal("ContinueAfterConfirmation still names driveClassicResume")
	}
	if !strings.Contains(body, "ErrClassicResumeRemoved") {
		t.Fatal("ContinueAfterConfirmation must refuse classic with ErrClassicResumeRemoved")
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
