package agentaudit

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestStepDepthZeroAlwaysSerialized proves item 15: EXTERNAL root depth=0 must
// appear in API JSON (not dropped by omitempty).
func TestStepDepthZeroAlwaysSerialized(t *testing.T) {
	t.Parallel()
	step := Step{
		Type:     "agent_delegation",
		Title:    "inbound root",
		Depth:    0,
		Origin:   "EXTERNAL",
		Protocol: "A2A",
		Status:   "SUCCEEDED",
	}
	raw, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"depth":0`) {
		t.Fatalf("depth=0 missing from JSON: %s", s)
	}
	// Round-trip preserves 0.
	var back Step
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Depth != 0 {
		t.Fatalf("round-trip depth=%d", back.Depth)
	}
}

func TestBuildTimeline_ExternalRootDepthZeroInJSON(t *testing.T) {
	t.Parallel()
	// Envelope that mirrors audit detail API: steps array with depth=0 root.
	type detailEnvelope struct {
		Steps []Step `json:"steps"`
	}
	step := Step{Type: "agent_delegation", Title: "A2A inbound", Depth: 0, Origin: "EXTERNAL"}
	raw, err := json.Marshal(detailEnvelope{Steps: []Step{step}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"depth":0`) {
		t.Fatalf("API detail JSON missing depth:0: %s", raw)
	}
}
