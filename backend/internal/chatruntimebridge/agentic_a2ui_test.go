package chatruntimebridge_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"actweave/backend/internal/a2ui"
	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/protocolevent"

	"github.com/cloudwego/eino/schema"
)

// TestAgenticInitial_A2UIRulesReachTheWireOnce pins the merge of main's A2UI
// prompt injection onto the Agentic frozen system message. The appendix must
// appear, the frozen prompt must still appear exactly once, and adk must not
// prepend a second system copy.
func TestAgenticInitial_A2UIRulesReachTheWireOnce(t *testing.T) {
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.run.ContextPolicySnapshot = mustA2UISnapshot(t, true)
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if f.mdl.lastInput == nil {
		t.Fatal("model was never called")
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

	raw, err := json.Marshal(f.mdl.lastInput)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(raw), testFrozenPrompt); got != 1 {
		t.Fatalf("frozen prompt text appears %d times, want exactly 1: %s", got, raw)
	}
	if !strings.Contains(string(raw), a2ui.PromptTemplateV2) || !strings.Contains(string(raw), "A2UI surfaces") {
		t.Fatalf("A2UI appendix missing from Agentic wire: %s", raw)
	}
	if strings.Count(string(raw), a2ui.PromptTemplateV2) != 1 {
		t.Fatalf("A2UI appendix appended more than once: %s", raw)
	}
}

// TestAgenticInitial_A2UIFenceHiddenFromStreamAndExtracted pins stream masking
// and terminal extract on the Agentic path after the merge. Deltas hide the
// fence; durable content keeps the surface.
func TestAgenticInitial_A2UIFenceHiddenFromStreamAndExtracted(t *testing.T) {
	full := "季度营收如下。\n\n" + a2ui.FenceStart + catalogSurface + a2ui.FenceEnd + "\n有问题再说。"
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.run.ContextPolicySnapshot = mustA2UISnapshot(t, true)
		f.mdl.responses = []*schema.AgenticMessage{agenticmsg.AssistantText(full)}
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sink, _, ok := f.sinks.last()
	if !ok {
		t.Fatal("no text sink opened")
	}
	var shown strings.Builder
	for _, emission := range sink.Emissions {
		shown.WriteString(emission.Text)
	}
	if strings.Contains(shown.String(), a2ui.FenceStart) || strings.Contains(shown.String(), "components") {
		t.Fatalf("fence leaked into Agentic stream deltas: %q", shown.String())
	}
	if !strings.Contains(shown.String(), "季度营收如下。") || !strings.Contains(shown.String(), "有问题再说。") {
		t.Fatalf("prose missing from Agentic stream deltas: %q", shown.String())
	}

	parts, err := chat.ParseMessageContentParts(f.results.content)
	if err != nil {
		t.Fatalf("durable content is not message-content.v1: %q err=%v", f.results.content, err)
	}
	var sawA2UI bool
	for _, p := range parts {
		if _, ok := p.(protocolevent.A2UIContentPart); ok {
			sawA2UI = true
			break
		}
	}
	if !sawA2UI {
		t.Fatalf("durable assistant content lost the A2UI surface: %q", f.results.content)
	}
}
