package agentaudit

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildTimeline_AgentDelegationNesting(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fin := base.Add(2 * time.Second)
	runs := []RunFact{{
		ID: "r1", TraceID: "t1", Status: "SUCCEEDED", StartedAt: base, FinishedAt: &fin,
		TriggeredByType: "USER", TriggeredByID: "u1",
		ModelSnapshot: json.RawMessage(`{"modelName":"m"}`),
	}}
	messages := []MessageFact{
		{ID: "m1", RunID: "r1", Role: "USER", Content: "hi", CreatedAt: base},
		{ID: "m2", RunID: "r1", Role: "ASSISTANT", Content: "done", CreatedAt: fin},
	}
	delStepID := "s-del"
	steps := []StepFact{
		{
			ID: "s-model", RunID: "r1", SequenceNo: 1, StepType: "MODEL", Status: "SUCCEEDED",
			StartedAt: base.Add(100 * time.Millisecond), FinishedAt: ts(base.Add(200 * time.Millisecond)),
			ModelTurn: map[string]any{"reasoning": "think"}, AgentID: "a-root",
		},
		{
			ID: delStepID, RunID: "r1", SequenceNo: 2, StepType: "AGENT_DELEGATION", Status: "SUCCEEDED",
			StartedAt: base.Add(300 * time.Millisecond), FinishedAt: ts(base.Add(1500 * time.Millisecond)),
			InputSummary: json.RawMessage(`{
				"callerAgentId":"a-root","targetAgentId":"a-child","mode":"INLINE",
				"protocol":"INTERNAL","origin":"INTERNAL","depth":1,"callableName":"call_b",
				"delegationId":"d1"
			}`),
			OutputSummary: json.RawMessage(`{"ok":true}`),
			AgentID:       "a-root", DelegationID: "d1",
		},
		{
			ID: "s-child-model", RunID: "r1", SequenceNo: 3, StepType: "MODEL", Status: "SUCCEEDED",
			StartedAt: base.Add(400 * time.Millisecond), FinishedAt: ts(base.Add(500 * time.Millisecond)),
			ModelTurn: map[string]any{"reasoning": "child"}, AgentID: "a-child",
			DelegationID: "d1", ParentStepID: delStepID,
		},
		{
			ID: "s-child-tool", RunID: "r1", SequenceNo: 4, StepType: "TOOL", Status: "SUCCEEDED",
			StartedAt: base.Add(600 * time.Millisecond), FinishedAt: ts(base.Add(900 * time.Millisecond)),
			ToolName: "lookup", ToolParams: json.RawMessage(`{"q":1}`), ToolResult: json.RawMessage(`{"ok":true}`),
			ToolPayloadAvailable: true, AgentID: "a-child", DelegationID: "d1", ParentStepID: delStepID,
		},
	}
	detail := BuildTimeline(runs, messages, steps, true)
	// Top-level should nest children under agent_delegation.
	var foundDel bool
	for _, s := range detail.Steps {
		if s.Type == "agent_delegation" {
			foundDel = true
			if s.Mode != "INLINE" || s.Protocol != "INTERNAL" {
				t.Fatalf("meta: %+v", s)
			}
			if len(s.Children) < 2 {
				t.Fatalf("expected nested children, got %d (%+v)", len(s.Children), s.Children)
			}
		}
		// Child steps should not also appear as roots when nested.
		if s.Type == "tool" && s.ParentStepID == delStepID {
			t.Fatal("tool should be nested, not root")
		}
	}
	if !foundDel {
		t.Fatalf("missing agent_delegation: %+v", detail.Steps)
	}
}

func TestBuildTimeline_UnknownStepNotDropped(t *testing.T) {
	t.Parallel()
	base := time.Now().UTC()
	detail := BuildTimeline(
		[]RunFact{{ID: "r", TraceID: "t", Status: "SUCCEEDED", StartedAt: base}},
		nil,
		[]StepFact{{ID: "s", RunID: "r", SequenceNo: 1, StepType: "FUTURE_KIND", Status: "SUCCEEDED", StartedAt: base}},
		false,
	)
	if len(detail.Steps) != 1 || detail.Steps[0].Type != "unknown" {
		t.Fatalf("%+v", detail.Steps)
	}
}

func ts(t time.Time) *time.Time { return &t }
