package chatruntimebridge

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolResultForModel_ResumeResultSnapshot(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"invocationId":"inv-1",
		"traceId":"tr",
		"output":{"k":"v"},
		"httpStatus":200,
		"attempts":1,
		"cached":false
	}`)
	got := toolResultForModel(snap)
	if !strings.Contains(got, `"ok":true`) {
		t.Fatalf("got %s", got)
	}
	if !strings.Contains(got, `"confirmed":true`) {
		t.Fatalf("missing confirmed: %s", got)
	}
	if !strings.Contains(got, `"k":"v"`) {
		t.Fatalf("missing output: %s", got)
	}
}

func TestBuildResumeTargets(t *testing.T) {
	t.Parallel()
	meta := EinoChatResume{
		ResumeSchemaVersion: EinoChatResumeSchemaVersion,
		SessionID:           "s", UserMessageID: "m", ActorID: "a",
		EinoCheckpointID: "ws/w/agent_run/r/n",
		InterruptIDs:     []string{"id-1", "id-2"},
		RootInterruptID:  "id-1",
		InterruptKind:    InterruptKindToolConfirmation,
	}
	targets, err := buildResumeTargets(meta, json.RawMessage(`{"invocationId":"i","output":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %v", targets)
	}
	for id, payload := range targets {
		s, ok := payload.(string)
		if !ok || !strings.Contains(s, "confirmed") {
			t.Fatalf("target %s payload = %#v", id, payload)
		}
	}
}

func TestBuildResumeTargets_MissingIDs(t *testing.T) {
	t.Parallel()
	_, err := buildResumeTargets(EinoChatResume{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestBuildResumeTargets_A4InterruptIDsAsKeys proves production mapping used by
// Bridge.ContinueAfterConfirmation: ResumeWithParams Targets keys are exactly
// the interrupt IDs persisted in einoChatResume (design A.4 / §3.6.3).
func TestBuildResumeTargets_A4InterruptIDsAsKeys(t *testing.T) {
	t.Parallel()
	// Vendor fully-qualified interrupt addresses (engine InterruptContextIDs).
	interruptIDs := []string{
		"agent:agent-a4;tool:call_refund",
	}
	meta := EinoChatResume{
		ResumeSchemaVersion: EinoChatResumeSchemaVersion,
		SessionID:           "sess-a4",
		UserMessageID:       "msg-a4",
		ActorID:             "actor-a4",
		EinoCheckpointID:    "ws/ws-a4/agent_run/run-a4/nonce-1",
		InterruptIDs:        interruptIDs,
		RootInterruptID:     interruptIDs[0],
		GatedToolCallID:     "call_refund",
		InterruptKind:       InterruptKindToolConfirmation,
	}
	resultSnap := json.RawMessage(`{
		"invocationId":"inv-a4",
		"output":{"refundId":"R-900","status":"accepted"},
		"httpStatus":200
	}`)
	targets, err := buildResumeTargets(meta, resultSnap)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets len=%d, want 1", len(targets))
	}
	payload, ok := targets[interruptIDs[0]]
	if !ok {
		t.Fatalf("Targets missing interrupt key %q; keys=%v", interruptIDs[0], targetKeys(targets))
	}
	s, ok := payload.(string)
	if !ok {
		t.Fatalf("target payload type %T, want string", payload)
	}
	if !strings.Contains(s, `"confirmed":true`) {
		t.Fatalf("payload missing confirmed: %s", s)
	}
	if !strings.Contains(s, `"refundId"`) {
		t.Fatalf("payload missing dispatch output: %s", s)
	}
	// Root-only fallback path also keys by interrupt id.
	rootOnly := meta
	rootOnly.InterruptIDs = nil
	targets2, err := buildResumeTargets(rootOnly, resultSnap)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := targets2[interruptIDs[0]]; !ok {
		t.Fatalf("root fallback missing key %q", interruptIDs[0])
	}
}

func targetKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
