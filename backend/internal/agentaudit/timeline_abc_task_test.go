package agentaudit

import (
	"encoding/json"
	"testing"
	"time"
)

// Residual #1: timeline nests A→B TASK → C TASK via parent_delegation_id,
// and surfaces TIMED_OUT on the nested frame + stats.
func TestBuildTimeline_ABC_TASK_ParentDelegationID_AndTimedOut(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	fin := base.Add(3 * time.Second)
	runs := []RunFact{
		{
			ID: "r-root", TraceID: "t-abc", Status: "SUCCEEDED", StartedAt: base, FinishedAt: &fin,
			TriggeredByType: "USER", TriggeredByID: "u1",
			ModelSnapshot: json.RawMessage(`{"modelName":"m"}`),
		},
		{
			ID: "r-b", TraceID: "t-abc", Status: "SUCCEEDED", StartedAt: base.Add(200 * time.Millisecond), FinishedAt: &fin,
			TriggeredByType: "SYSTEM", TriggeredByID: "a",
		},
		{
			ID: "r-c", TraceID: "t-abc", Status: "TIMED_OUT", StartedAt: base.Add(500 * time.Millisecond),
			FinishedAt: ts(base.Add(2 * time.Second)), TriggeredByType: "SYSTEM", TriggeredByID: "b",
		},
	}
	delAB, delBC := "d-ab", "d-bc"
	stepAB, stepBC := "s-ab", "s-bc"
	steps := []StepFact{
		{
			ID: stepAB, RunID: "r-root", SequenceNo: 1, StepType: "AGENT_DELEGATION", Status: "SUCCEEDED",
			StartedAt: base.Add(100 * time.Millisecond), FinishedAt: ts(base.Add(2800 * time.Millisecond)),
			InputSummary: json.RawMessage(`{
				"callerAgentId":"A","targetAgentId":"B","mode":"TASK","protocol":"INTERNAL",
				"origin":"INTERNAL","depth":1,"callableName":"call_b","delegationId":"d-ab"
			}`),
			OutputSummary: json.RawMessage(`{"ok":true,"status":"SUCCEEDED"}`),
			AgentID:       "A", DelegationID: delAB, ChildRunID: "r-b", Mode: "TASK",
		},
		{
			ID: stepBC, RunID: "r-b", SequenceNo: 1, StepType: "AGENT_DELEGATION", Status: "TIMED_OUT",
			StartedAt: base.Add(400 * time.Millisecond), FinishedAt: ts(base.Add(2 * time.Second)),
			InputSummary: json.RawMessage(`{
				"callerAgentId":"B","targetAgentId":"C","mode":"TASK","protocol":"INTERNAL",
				"origin":"INTERNAL","depth":2,"callableName":"call_c","delegationId":"d-bc"
			}`),
			OutputSummary: json.RawMessage(`{"ok":false,"status":"TIMED_OUT","errorCode":"DELEGATION_TIMED_OUT"}`),
			AgentID:       "B", DelegationID: delBC, ParentDelegationID: delAB, ChildRunID: "r-c",
			Mode:                "TASK",
			DelegationErrorCode: "DELEGATION_TIMED_OUT",
		},
		{
			ID: "s-c-model", RunID: "r-c", SequenceNo: 1, StepType: "MODEL", Status: "TIMED_OUT",
			StartedAt: base.Add(600 * time.Millisecond), FinishedAt: ts(base.Add(1900 * time.Millisecond)),
			AgentID: "C", DelegationID: delBC,
		},
	}
	detail := BuildTimeline(runs, nil, steps, false)

	// Find root A→B and ensure B→C is nested under it via ParentDelegationID.
	var ab *Step
	for i := range detail.Steps {
		if detail.Steps[i].Type == "agent_delegation" && detail.Steps[i].DelegationID == delAB {
			ab = &detail.Steps[i]
			break
		}
	}
	if ab == nil {
		t.Fatalf("missing A→B root: %+v", detail.Steps)
	}
	var foundBC bool
	var walk func([]Step)
	walk = func(kids []Step) {
		for _, c := range kids {
			if c.Type == "agent_delegation" && c.DelegationID == delBC {
				foundBC = true
				if c.ParentDelegationID != delAB {
					t.Fatalf("B→C ParentDelegationID=%q want %s", c.ParentDelegationID, delAB)
				}
				if c.Status != "TIMED_OUT" && c.Status != "timed_out" {
					// Status may be upper-case from step fact.
					if c.Status != "TIMED_OUT" {
						// Title must surface TIMED_OUT.
						if !containsFold(c.Title, "TIMED_OUT") && c.Status != "TIMED_OUT" {
							// Accept either status field or title annotation.
							if c.Status != "TIMED_OUT" && !containsFold(c.Title, "TIMED") {
								t.Fatalf("B→C status/title missing TIMED_OUT: status=%q title=%q", c.Status, c.Title)
							}
						}
					}
				}
			}
			if len(c.Children) > 0 {
				walk(c.Children)
			}
		}
	}
	walk(ab.Children)
	if !foundBC {
		// B→C must not appear as a root sibling when ParentDelegationID links to AB.
		for _, s := range detail.Steps {
			if s.Type == "agent_delegation" && s.DelegationID == delBC {
				t.Fatalf("B→C should be nested under A→B, not root: %+v", detail.Steps)
			}
		}
		t.Fatalf("B→C not found under A→B children: %+v", ab.Children)
	}

	// Stats include TIMED_OUT as error-class for list aggregation.
	item := AggregateListItem(runs, len(steps))
	if item.Status != "error" {
		t.Fatalf("aggregate with TIMED_OUT child run status=%q want error", item.Status)
	}
}

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub ||
		len(sub) > 0 && (indexFold(s, sub) >= 0))
}

func indexFold(s, sub string) int {
	// simple case-insensitive contains
	ls, lsub := make([]byte, len(s)), make([]byte, len(sub))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		ls[i] = c
	}
	for i := 0; i < len(sub); i++ {
		c := sub[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		lsub[i] = c
	}
	// reuse strings.Contains via conversion
	return indexBytes(ls, lsub)
}

func indexBytes(s, sub []byte) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		ok := true
		for j := 0; j < len(sub); j++ {
			if s[i+j] != sub[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}
