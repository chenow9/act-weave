package agentaudit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildTimelineMissingReasoningAndRedaction(t *testing.T) {
	base := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	finished := base.Add(2 * time.Second)
	runs := []RunFact{{
		ID: "r1", TraceID: "trace-1", Status: "SUCCEEDED",
		TriggeredByType: "USER", TriggeredByID: "u1",
		ModelSnapshot: json.RawMessage(`{"modelName":"gpt-4o-mini"}`),
		StartedAt:     base, FinishedAt: &finished,
	}}
	messages := []MessageFact{
		{ID: "m1", RunID: "r1", Role: "USER", Content: "hello secret@example.com", CreatedAt: base},
		{ID: "m2", RunID: "r1", Role: "ASSISTANT", Content: "done", CreatedAt: finished},
	}
	steps := []StepFact{
		{
			ID: "s1", RunID: "r1", SequenceNo: 1, StepType: "MODEL", Status: "SUCCEEDED",
			StartedAt: base.Add(100 * time.Millisecond), FinishedAt: ptrTime(base.Add(200 * time.Millisecond)),
			ModelTurn: map[string]any{"content": "x"}, // no reasoning
		},
		{
			ID: "s2", RunID: "r1", SequenceNo: 2, StepType: "TOOL", Status: "SUCCEEDED",
			ToolName: "send_email", ToolParams: json.RawMessage(`{"to":"a@b.com","api_key":"sk_live"}`),
			ToolResult: json.RawMessage(`{"ok":true}`), ToolPayloadAvailable: true,
			StartedAt: base.Add(300 * time.Millisecond), FinishedAt: ptrTime(base.Add(500 * time.Millisecond)),
			InputSummary:  json.RawMessage(`{"callableName":"send_email","arguments":{"to":"a@b.com"}}`),
			OutputSummary: json.RawMessage(`{"ok":true}`),
		},
	}

	off := BuildTimeline(runs, messages, steps, false)
	if off.TraceID != "trace-1" || off.Status != "success" || off.Model != "gpt-4o-mini" {
		t.Fatalf("list fields: %+v", off)
	}
	var sawMissing, sawTool bool
	for _, step := range off.Steps {
		if step.Type == "reasoning" {
			sawMissing = true
			if step.Content != MissingReasoningText || step.ContentState != ContentMissing && step.ContentState != ContentRedacted {
				// missing reasoning with empty model turn -> ContentMissing
				if step.Content != MissingReasoningText {
					t.Fatalf("expected 无推理数据, got %q state=%s", step.Content, step.ContentState)
				}
			}
		}
		if step.Type == "tool" {
			sawTool = true
			if strings.Contains(string(step.Params), "sk_live") {
				t.Fatalf("debug off must not expose raw api_key: %s", step.Params)
			}
			if step.ParamsState != ContentRedacted {
				t.Fatalf("expected redacted params, got %s", step.ParamsState)
			}
		}
	}
	if !sawMissing || !sawTool {
		t.Fatalf("missing steps in timeline: %+v", off.Steps)
	}

	// With reasoning stored and debug on, expose plain.
	steps[0].ModelTurn = map[string]any{"reasoning": "think carefully"}
	on := BuildTimeline(runs, messages, steps, true)
	found := false
	for _, step := range on.Steps {
		if step.Type == "reasoning" && step.Content == "think carefully" && step.ContentState == ContentPlain {
			found = true
		}
		if step.Type == "tool" && step.ParamsState == ContentPlain {
			if !strings.Contains(string(step.Params), "sk_live") {
				t.Fatalf("debug on should expose raw params: %s", step.Params)
			}
		}
	}
	if !found {
		t.Fatalf("debug on should expose reasoning: %+v", on.Steps)
	}
}

func TestAggregateListItemStatus(t *testing.T) {
	base := time.Now().UTC()
	runs := []RunFact{
		{ID: "1", TraceID: "t", Status: "SUCCEEDED", StartedAt: base},
		{ID: "2", TraceID: "t", Status: "RUNNING", StartedAt: base.Add(time.Second)},
	}
	item := AggregateListItem(runs, 3)
	if item.Status != "running" || item.StepCount != 3 {
		t.Fatalf("unexpected aggregate: %+v", item)
	}
}

func TestPageTimelineSteps(t *testing.T) {
	detail := TraceDetail{
		TraceID: "t1",
		Steps: []Step{
			{Type: "input", Title: "用户输入", TimeOffsetMs: 0},
			{Type: "reasoning", Title: "大模型推理", TimeOffsetMs: 10},
			{Type: "tool", Title: "工具调用: a", TimeOffsetMs: 20},
			{Type: "tool", Title: "工具调用: b", TimeOffsetMs: 30},
			{Type: "output", Title: "最终输出", TimeOffsetMs: 40},
		},
	}
	page1 := PageTimelineSteps(detail, DetailFilter{Limit: 2, Offset: 0})
	if page1.StepTotal != 5 || page1.StepLimit != 2 || page1.StepOffset != 0 || !page1.HasMore || len(page1.Steps) != 2 {
		t.Fatalf("page1 = %+v", page1)
	}
	if page1.Steps[0].Type != "input" || page1.Steps[1].Type != "reasoning" {
		t.Fatalf("page1 order = %+v", page1.Steps)
	}
	page2 := PageTimelineSteps(detail, DetailFilter{Limit: 2, Offset: 2})
	if page2.StepTotal != 5 || page2.StepOffset != 2 || !page2.HasMore || len(page2.Steps) != 2 {
		t.Fatalf("page2 = %+v", page2)
	}
	if page2.Steps[0].Title != "工具调用: a" {
		t.Fatalf("page2 first = %+v", page2.Steps[0])
	}
	page3 := PageTimelineSteps(detail, DetailFilter{Limit: 2, Offset: 4})
	if page3.HasMore || len(page3.Steps) != 1 || page3.Steps[0].Type != "output" {
		t.Fatalf("page3 = %+v", page3)
	}
	// Default limit applies when limit omitted.
	def := PageTimelineSteps(detail, DetailFilter{})
	if def.StepLimit != DefaultDetailStepLimit || def.HasMore || len(def.Steps) != 5 {
		t.Fatalf("default page = total=%d limit=%d more=%v n=%d", def.StepTotal, def.StepLimit, def.HasMore, len(def.Steps))
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
