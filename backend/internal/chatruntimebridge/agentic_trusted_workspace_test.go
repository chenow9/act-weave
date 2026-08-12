package chatruntimebridge_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
)

// The trusted-workspace binding is defence-in-depth for the checkpoint store:
// Get/Set/Delete cross-check the workspace parsed out of the checkpoint ID
// against it, but only when it is present. An entry point that forgets to bind
// it therefore loses the tenant check without anything failing — no error, no
// log, and every existing test still green. Task 4B-4 lost it on the initial
// turn exactly this way, by moving the dispatch out of drive() and leaving the
// binding behind in the classic branch.
//
// These tests observe the binding where it has to hold: inside the model call,
// which only runs once the engine has already been through the store.
func TestAgenticInitial_BindsTheTrustedWorkspace(t *testing.T) {
	f := newAgenticFixture(t, nil)
	job := f.job()
	if err := f.bridge(t).Execute(context.Background(), job); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	seen := f.mdl.trustedWorkspaces()
	if len(seen) == 0 {
		t.Fatal("the model was never called, so the turn proves nothing")
	}
	for i, ws := range seen {
		if ws != job.WorkspaceID {
			t.Fatalf("model call %d ran with trusted workspace %q, want %q: "+
				"the checkpoint store cross-check is silently disabled on this path",
				i, ws, job.WorkspaceID)
		}
	}
}

func TestAgenticResume_BindsTheTrustedWorkspace(t *testing.T) {
	h := newAgenticHITLFixture(t, []*schema.AgenticMessage{
		agenticToolCall("call_1", "wire_money", `{"q":"x"}`),
		agenticmsg.AssistantText("transfer done"),
	})
	snapshot := h.pause(t)
	job := h.job()
	before := len(h.f.mdl.trustedWorkspaces())
	if err := h.bridge.ContinueAfterConfirmation(context.Background(), job,
		snapshot, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatalf("ContinueAfterConfirmation: %v", err)
	}

	seen := h.f.mdl.trustedWorkspaces()
	if len(seen) <= before {
		t.Fatal("the resume never reached the model, so the turn proves nothing")
	}
	for i, ws := range seen[before:] {
		if ws != job.WorkspaceID {
			t.Fatalf("resumed model call %d ran with trusted workspace %q, want %q",
				i, ws, job.WorkspaceID)
		}
	}
}
