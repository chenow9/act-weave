package chatruntime_test

import (
	"encoding/json"
	"testing"

	"actweave/backend/internal/chatruntime"
)

func TestParseCapabilitySnapshot(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaVersion":"capability-snapshot.v1",
		"releases":[
			{
				"capabilityId":"cap-1","releaseId":"rel-1","kind":"TOOL",
				"callableName":"demo.lookup","callableDescription":"lookup",
				"inputSchema":{"type":"object","properties":{"q":{"type":"string"}}},
				"sideEffectLevel":"READ"
			},
			{
				"capabilityId":"cap-dup","releaseId":"rel-dup","kind":"TOOL",
				"callableName":"demo.lookup","callableDescription":"duplicate name dropped"
			},
			{
				"capabilityId":"","releaseId":"rel-x","kind":"TOOL",
				"callableName":"missing-ids-dropped"
			}
		]
	}`)
	caps, err := chatruntime.ParseCapabilitySnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 1 {
		t.Fatalf("caps = %+v, want 1 (dedupe + drop incomplete)", caps)
	}
	if caps[0].CallableName != "demo.lookup" || caps[0].SideEffectLevel != "READ" {
		t.Fatalf("cap = %+v", caps[0])
	}

	empty, err := chatruntime.ParseCapabilitySnapshot(json.RawMessage(`{"releases":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if empty != nil && len(empty) != 0 {
		t.Fatalf("empty releases = %+v", empty)
	}
	nilCaps, err := chatruntime.ParseCapabilitySnapshot(nil)
	if err != nil || nilCaps != nil {
		t.Fatalf("nil snapshot = %+v err=%v", nilCaps, err)
	}
}
