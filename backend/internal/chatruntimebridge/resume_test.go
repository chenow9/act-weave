package chatruntimebridge_test

import (
	"encoding/json"
	"testing"

	"actweave/backend/internal/chatruntimebridge"
)

func TestEmbedExtractEinoChatResume_RoundTrip(t *testing.T) {
	t.Parallel()
	outer := json.RawMessage(`{
		"schemaVersion":"tool-resume-request.v1",
		"invocationId":"inv-1",
		"workspaceId":"ws-1",
		"capabilityId":"cap-1",
		"releaseId":"rel-1",
		"actorType":"USER",
		"actorId":"actor-1",
		"traceId":"tr-1",
		"connectionId":"conn-1",
		"authorizationSnapshot":{}
	}`)
	meta := chatruntimebridge.EinoChatResume{
		ResumeSchemaVersion: chatruntimebridge.EinoChatResumeSchemaVersion,
		SessionID:           "sess-1",
		UserMessageID:       "msg-1",
		ActorID:             "actor-1",
		EinoCheckpointID:    "ws/ws-1/agent_run/run-1/nonce-1",
		InterruptIDs:        []string{"agent:demo;tool:call_1"},
		RootInterruptID:     "agent:demo;tool:call_1",
		GatedToolCallID:     "call_1",
		GatedStepID:         "step-1",
		InterruptKind:       chatruntimebridge.InterruptKindToolConfirmation,
	}
	embedded, err := chatruntimebridge.EmbedEinoChatResume(outer, meta)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := chatruntimebridge.ExtractEinoChatResume(embedded)
	if !ok {
		t.Fatal("expected extract ok")
	}
	if got.EinoCheckpointID != meta.EinoCheckpointID {
		t.Fatalf("checkpoint = %q", got.EinoCheckpointID)
	}
	if len(got.InterruptIDs) != 1 || got.InterruptIDs[0] != "agent:demo;tool:call_1" {
		t.Fatalf("interruptIds = %v", got.InterruptIDs)
	}
	if got.SessionID != "sess-1" || got.UserMessageID != "msg-1" {
		t.Fatalf("session/msg mismatch: %+v", got)
	}
	// Outer schema must remain tool-resume-request.v1.
	var object map[string]any
	if err := json.Unmarshal(embedded, &object); err != nil {
		t.Fatal(err)
	}
	if object["schemaVersion"] != "tool-resume-request.v1" {
		t.Fatalf("outer schemaVersion = %v", object["schemaVersion"])
	}
	if _, has := object["chatLoop"]; has {
		t.Fatal("chatLoop must not be present after embed")
	}
}

func TestEmbedEinoChatResume_RemovesChatLoop(t *testing.T) {
	t.Parallel()
	outer := json.RawMessage(`{
		"schemaVersion":"tool-resume-request.v1",
		"chatLoop":{"schemaVersion":"chat-tool-loop.v1","sessionId":"s"}
	}`)
	meta := chatruntimebridge.EinoChatResume{
		ResumeSchemaVersion: chatruntimebridge.EinoChatResumeSchemaVersion,
		SessionID:           "sess-1",
		UserMessageID:       "msg-1",
		ActorID:             "actor-1",
		EinoCheckpointID:    "ws/ws-1/agent_run/run-1/n1",
		RootInterruptID:     "agent:a;tool:c1",
		InterruptKind:       chatruntimebridge.InterruptKindToolConfirmation,
	}
	embedded, err := chatruntimebridge.EmbedEinoChatResume(outer, meta)
	if err != nil {
		t.Fatal(err)
	}
	if chatruntimebridge.HasChatLoop(embedded) {
		t.Fatal("chatLoop must be stripped when embedding einoChatResume")
	}
	if _, ok := chatruntimebridge.ExtractEinoChatResume(embedded); !ok {
		t.Fatal("expected eino extract ok")
	}
}

func TestExtractEinoChatResume_RejectsInvalid(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{}`,
		`{"einoChatResume":null}`,
		`{"einoChatResume":{"resumeSchemaVersion":"other"}}`,
		`{"einoChatResume":{
			"resumeSchemaVersion":"eino-chat-resume.v1",
			"sessionId":"s","userMessageId":"m","actorId":"a",
			"einoCheckpointId":""
		}}`,
		`{"einoChatResume":{
			"resumeSchemaVersion":"eino-chat-resume.v1",
			"sessionId":"s","userMessageId":"m","actorId":"a",
			"einoCheckpointId":"ws/w/agent_run/r/n"
		}}`,
	}
	for _, raw := range cases {
		if _, ok := chatruntimebridge.ExtractEinoChatResume(json.RawMessage(raw)); ok {
			t.Fatalf("expected reject for %s", raw)
		}
	}
}

func TestExtractEinoChatResume_PrefersEinoOverChatLoopPresence(t *testing.T) {
	t.Parallel()
	// If both were somehow present, ExtractEinoChatResume still succeeds when
	// eino fields are valid — dispatcher prefers this path first.
	// Embed path prevents this; this guards read preference.
	raw := json.RawMessage(`{
		"schemaVersion":"tool-resume-request.v1",
		"chatLoop":{"schemaVersion":"chat-tool-loop.v1","sessionId":"legacy"},
		"einoChatResume":{
			"resumeSchemaVersion":"eino-chat-resume.v1",
			"sessionId":"sess-eino",
			"userMessageId":"msg-eino",
			"actorId":"actor-eino",
			"einoCheckpointId":"ws/ws-1/agent_run/run-1/n1",
			"interruptIds":["agent:a;tool:c1"],
			"rootInterruptId":"agent:a;tool:c1",
			"interruptKind":"tool_confirmation"
		}
	}`)
	meta, ok := chatruntimebridge.ExtractEinoChatResume(raw)
	if !ok {
		t.Fatal("expected eino extract when both present")
	}
	if meta.SessionID != "sess-eino" {
		t.Fatalf("session = %q", meta.SessionID)
	}
	if !chatruntimebridge.HasChatLoop(raw) {
		t.Fatal("fixture should still have chatLoop for dual-presence check")
	}
}

func TestEffectiveInterruptIDs(t *testing.T) {
	t.Parallel()
	m := chatruntimebridge.EinoChatResume{RootInterruptID: "root-only"}
	if got := m.EffectiveInterruptIDs(); len(got) != 1 || got[0] != "root-only" {
		t.Fatalf("root fallback = %v", got)
	}
	m.InterruptIDs = []string{"a", "b"}
	if got := m.EffectiveInterruptIDs(); len(got) != 2 {
		t.Fatalf("ids = %v", got)
	}
}

func TestToolCallIDFromInterruptID(t *testing.T) {
	t.Parallel()
	got := chatruntimebridge.ToolCallIDFromInterruptID("agent:my-agent;tool:tool_call_abc123")
	if got != "tool_call_abc123" {
		t.Fatalf("got %q", got)
	}
	if chatruntimebridge.ToolCallIDFromInterruptID("agent:only") != "" {
		t.Fatal("expected empty")
	}
}
