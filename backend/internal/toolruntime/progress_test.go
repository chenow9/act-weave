package toolruntime

import (
	"encoding/json"
	"testing"
)

func TestParseDoneWhenAndJSONPath(t *testing.T) {
	t.Parallel()
	pred, ok := parseDoneWhen("$.status == 'SUCCESS'")
	if !ok || pred.path != "status" || pred.equals != "SUCCESS" {
		t.Fatalf("pred=%+v ok=%v", pred, ok)
	}
	pred, ok = parseDoneWhen(`status == "done"`)
	if !ok || pred.path != "status" || pred.equals != "done" {
		t.Fatalf("quoted pred=%+v", pred)
	}
	if _, ok := parseDoneWhen(""); ok {
		t.Fatal("empty doneWhen must fail")
	}

	doc := map[string]any{"status": "SUCCESS", "percent": float64(40), "nested": map[string]any{"msg": "切片"}}
	spec := &resolvedProgressSpec{DonePath: "status", DoneEquals: "SUCCESS", PercentPath: "percent", MessagePath: "nested.msg"}
	if !doneWhenSatisfied(doc, spec) {
		t.Fatal("expected done")
	}
	current, total, unit, message := progressFromDocument(doc, spec)
	if current != 40 || total == nil || *total != 100 || unit != "percent" || message != "切片" {
		t.Fatalf("progress current=%v total=%v unit=%s message=%q", current, total, unit, message)
	}
}

func TestParseHTTPProgressSpec(t *testing.T) {
	t.Parallel()
	spec, err := parseHTTPProgressSpec(httpActionConfig{})
	if err != nil || spec != nil {
		t.Fatalf("empty: spec=%v err=%v", spec, err)
	}
	spec, err = parseHTTPProgressSpec(httpActionConfig{
		XProgress: &httpProgressConfig{
			Mode: "poll", Path: "/tasks/{taskId}/progress",
			DoneWhen: "$.status == 'SUCCESS'", PercentPath: "$.percent", IntervalMS: 500,
		},
	})
	if err != nil || spec == nil || spec.Path != "/tasks/{taskId}/progress" || spec.PercentPath != "percent" {
		t.Fatalf("x-actweave-progress: %+v err=%v", spec, err)
	}
	if _, err := parseHTTPProgressSpec(httpActionConfig{
		Progress: &httpProgressConfig{Mode: "sse", Path: "/p", DoneWhen: "status == 'x'"},
	}); err == nil {
		t.Fatal("expected invalid mode")
	}
}

func TestOverlayJSONObjectPrefersResult(t *testing.T) {
	t.Parallel()
	base := map[string]any{"taskId": "from-input", "q": "keep"}
	got := overlayJSONObject(base, json.RawMessage(`{"taskId":"from-result","status":"RUNNING"}`))
	if got["taskId"] != "from-result" || got["q"] != "keep" || got["status"] != "RUNNING" {
		t.Fatalf("overlay=%v", got)
	}
}
