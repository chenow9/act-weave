package agentaudit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDelegationTitle_OriginAwarePaths(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// EXTERNAL inbound: external → target (not caller → target → external)
	ext := delegationStep(base, StepFact{
		ID: "s1", RunID: "r1", SequenceNo: 1, StepType: "AGENT_DELEGATION", Status: "SUCCEEDED",
		StartedAt: base, CallerAgentID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		TargetAgentID:    "11111111-2222-3333-4444-555555555555",
		ExternalAgentRef: "service-principal:actor",
		Origin:           "EXTERNAL", Protocol: "A2A", Mode: "TASK", Depth: 0,
		InputSummary: json.RawMessage(`{"callableName":"a2a.inbound"}`),
	}, true)
	if !strings.Contains(ext.Title, "service-principal:actor") {
		t.Fatalf("EXTERNAL title missing ext ref: %s", ext.Title)
	}
	if !strings.Contains(ext.Title, "11111111") {
		t.Fatalf("EXTERNAL title missing target: %s", ext.Title)
	}
	// Must not be caller → target only without external as left side.
	if strings.Contains(ext.Title, "aaaaaaaa → 11111111") && !strings.Contains(ext.Title, "service-principal") {
		t.Fatalf("EXTERNAL must not use internal path alone: %s", ext.Title)
	}
	// Prefer ext → target form
	if !strings.Contains(ext.Title, "service-principal:actor → 11111111") {
		t.Fatalf("want external→target path, got %s", ext.Title)
	}
	if ext.Depth != 0 {
		t.Fatalf("depth=%d", ext.Depth)
	}

	// INTERNAL: caller → target
	in := delegationStep(base, StepFact{
		ID: "s2", RunID: "r1", SequenceNo: 2, StepType: "AGENT_DELEGATION", Status: "SUCCEEDED",
		StartedAt: base, CallerAgentID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		TargetAgentID: "11111111-2222-3333-4444-555555555555",
		Origin:        "INTERNAL", Protocol: "INTERNAL", Mode: "INLINE", Depth: 1,
		InputSummary: json.RawMessage(`{"callableName":"call_b"}`),
	}, true)
	if !strings.Contains(in.Title, "aaaaaaaa → 11111111") {
		t.Fatalf("INTERNAL title path: %s", in.Title)
	}

	// outbound A2A: caller → external
	out := delegationStep(base, StepFact{
		ID: "s3", RunID: "r1", SequenceNo: 3, StepType: "AGENT_DELEGATION", Status: "SUCCEEDED",
		StartedAt: base, CallerAgentID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ExternalAgentRef: "https://agent.example.com/a2a",
		Origin:           "INTERNAL", Protocol: "A2A", Mode: "TASK", Depth: 1,
		InputSummary: json.RawMessage(`{"callableName":"remote"}`),
	}, true)
	if !strings.Contains(out.Title, "aaaaaaaa → https://agent.example.com/a2a") {
		t.Fatalf("outbound A2A title path: %s", out.Title)
	}
}
