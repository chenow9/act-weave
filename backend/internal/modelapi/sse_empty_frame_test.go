package modelapi

import (
	"io"
	"strings"
	"testing"
)

func TestCopySSEDroppingEmptyFrames(t *testing.T) {
	in := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed"}`,
		"",
		"",
		"",
	}, "\n")
	var out strings.Builder
	if err := copySSEDroppingEmptyFrames(&out, strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Count(got, "event:") != 2 {
		t.Fatalf("events=%q", got)
	}
	if strings.Contains(got, "event: \n\n") || strings.HasSuffix(got, "\n\n\n") {
		t.Fatalf("still has empty frame: %q", got)
	}
	if !strings.Contains(got, `"response.created"`) || !strings.Contains(got, `"response.completed"`) {
		t.Fatalf("lost payload: %q", got)
	}
}

func TestCopySSEKeepsDoneWithoutEmptyJSON(t *testing.T) {
	in := "event: ping\ndata: [DONE]\n\n"
	var out strings.Builder
	if err := copySSEDroppingEmptyFrames(&out, strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("DONE-only frame should be dropped: %q", out.String())
	}
}

func TestFilterEmptySSEFramesRoundTrip(t *testing.T) {
	raw, err := io.ReadAll(filterEmptySSEFrames(io.NopCloser(strings.NewReader(
		"event: a\ndata: {\"ok\":true}\n\n\n",
	))))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"ok":true`) {
		t.Fatalf("got %q", raw)
	}
}
