package chatruntimebridge_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// TestAgenticInitial_SystemPromptReachesTheWireExactlyOnce guards the duplicate
// system prompt defect found by the real-path E2E: the assembled message list
// already leads with the frozen system prompt, and passing the same text as
// AgenticAgentBuildConfig.Instruction made adk prepend a second copy, so every
// Agentic turn carried it twice.
//
// It was invisible from the outside because nothing failed: the run succeeded,
// and the audited description of the request still described one copy —
// ContextAssemblyRecord stores a single SystemPromptHash and
// EstimateAgenticRequest takes the system prompt as one out-of-band term. The
// preflight therefore under-counted the real input by a whole system prompt,
// which is exactly the budget the frozen assembly exists to bound.
//
// Asserting on the model input rather than the builder config is deliberate:
// the defect lived in what adk does with Instruction, so a test that inspects
// the config we pass could not have seen it.
func TestAgenticInitial_SystemPromptReachesTheWireExactlyOnce(t *testing.T) {
	f := newAgenticFixture(t, nil)
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if f.mdl.lastInput == nil {
		t.Fatal("model was never called, so nothing about the wire was proven")
	}

	systemMessages := 0
	for i, msg := range f.mdl.lastInput {
		if msg == nil {
			t.Fatalf("model input %d is nil", i)
		}
		if msg.Role != schema.AgenticRoleTypeSystem {
			continue
		}
		systemMessages++
		if i != 0 {
			t.Errorf("system message at index %d; the frozen prompt must lead the input", i)
		}
	}
	if systemMessages != 1 {
		raw, _ := json.Marshal(f.mdl.lastInput)
		t.Fatalf("model input carries %d system messages, want exactly 1: %s", systemMessages, raw)
	}

	// A second copy could also arrive outside the system role (an adk
	// instructions field, or folded into another message), so pin the text too.
	raw, err := json.Marshal(f.mdl.lastInput)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(raw), testFrozenPrompt); got != 1 {
		t.Fatalf("frozen prompt text appears %d times in the model input, want exactly 1: %s", got, raw)
	}
}
