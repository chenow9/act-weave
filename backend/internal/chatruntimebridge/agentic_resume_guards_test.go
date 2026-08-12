package chatruntimebridge_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/execution"
)

// TestAgenticResume_RefusesAForeignCheckpointBeforeOpeningAnItem pins the order
// two things happen in. The engine already refuses a checkpoint that belongs to
// another run, but it does so after the turn has opened the assistant item, and a
// refusal that early never completes or fails it: the client is left holding an
// item nothing will ever finish. Ownership is decidable from the ID alone, so it
// has to be decided before any protocol event exists.
func TestAgenticResume_RefusesAForeignCheckpointBeforeOpeningAnItem(t *testing.T) {
	h := newAgenticHITLFixture(t, []*schema.AgenticMessage{
		agenticToolCall("call_1", "wire_money", `{"q":"x"}`),
		agenticmsg.AssistantText("transfer done"),
	})
	snapshot := h.pause(t)
	// Re-point the checkpoint at another run in the same workspace, which is the
	// case the store's workspace cross-check cannot catch.
	const foreignRun = "b22ce000-0000-4000-8000-00000000dead"
	foreign := bytes.Replace(snapshot,
		[]byte("agent_run/"+testRunUUID), []byte("agent_run/"+foreignRun), 1)
	if bytes.Equal(foreign, snapshot) {
		t.Fatalf("the checkpoint id was not re-pointed, so nothing is under test: %s", snapshot)
	}

	opensBefore := h.f.sinks.opens.Load()
	callsBefore := h.f.mdl.calls.Load()
	err := h.bridge.ContinueAfterConfirmation(context.Background(), h.job(),
		foreign, json.RawMessage(`{"ok":true}`))
	if err == nil {
		t.Fatal("a checkpoint belonging to another run was accepted")
	}
	if got := h.f.sinks.opens.Load(); got != opensBefore {
		t.Fatalf("%d assistant item(s) were opened for a refused resume, want 0: "+
			"the client is left with an item nothing will finish", got-opensBefore)
	}
	if got := h.f.mdl.calls.Load(); got != callsBefore {
		t.Fatalf("the refused resume still reached the model %d time(s)", got-callsBefore)
	}
}

// TestAgenticResume_RefusesToolResultsThatCannotFitTheWindow covers an asymmetry
// a resume used to have: it assembles nothing, so it also preflighted nothing,
// and a tool answering with tens of thousands of lines went straight to the
// provider on a turn the user had already approved. The initial turn answers the
// same overload with CONTEXT_REQUIRED_INPUT_TOO_LARGE; a resume that answers with
// a raw upstream error is not the same product behaviour.
func TestAgenticResume_RefusesToolResultsThatCannotFitTheWindow(t *testing.T) {
	h := newAgenticHITLFixture(t, []*schema.AgenticMessage{
		agenticToolCall("call_1", "wire_money", `{"q":"x"}`),
		agenticmsg.AssistantText("transfer done"),
	})
	snapshot := h.pause(t)

	oversized := json.RawMessage(`{"rows":"` + strings.Repeat("overflow ", 200_000) + `"}`)
	opensBefore := h.f.sinks.opens.Load()
	callsBefore := h.f.mdl.calls.Load()
	err := h.bridge.ContinueAfterConfirmation(context.Background(), h.job(),
		snapshot, oversized)
	if err == nil {
		t.Fatal("a tool result far larger than the context window was accepted")
	}
	var ctxErr *execution.ContextError
	if !errors.As(err, &ctxErr) {
		t.Fatalf("resume failed with %v, want a context error the client can act on", err)
	}
	if ctxErr.Code != execution.ErrCodeContextRequiredInputTooLarge {
		t.Fatalf("resume failed with %q, want %q — the same code the initial turn "+
			"returns for the same overload", ctxErr.Code, execution.ErrCodeContextRequiredInputTooLarge)
	}
	if got := h.f.mdl.calls.Load(); got != callsBefore {
		t.Fatalf("the oversized resume still reached the model %d time(s)", got-callsBefore)
	}
	if got := h.f.sinks.opens.Load(); got != opensBefore {
		t.Fatalf("%d assistant item(s) were opened for a refused resume, want 0",
			got-opensBefore)
	}
}
