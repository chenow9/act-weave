package chatruntimebridge_test

import (
	"context"
	"strings"
	"testing"

	"actweave/backend/internal/a2ui"
	"actweave/backend/internal/chatruntimebridge"
)

const fenceSurface = `{"components":[{"id":"root","component":"Chart","chartType":"bar",` +
	`"series":[{"points":[{"label":"Q1","value":1}]}]}]}`

// A client sees prose only, while the recorder keeps the model's output whole for
// extraction. Both properties matter: the first is what the user reads, the second
// is where the surface comes from.
func TestStreamDeltaRecorderHidesTheFenceFromDeltasOnly(t *testing.T) {
	t.Parallel()

	sink := &chatruntimebridge.RecordingTextDeltaSink{}
	recorder := &chatruntimebridge.StreamDeltaRecorder{
		Sink: sink, Preview: &a2ui.FencePreview{},
	}
	ctx := context.Background()
	chunks := []string{
		"季度营收如下。", "\n\n<<<", "A2UI>>>", fenceSurface[:20], fenceSurface[20:],
		"<<<END_", "A2UI>>>", "有问题再说。",
	}
	for _, chunk := range chunks {
		if err := recorder.OnTextDelta(ctx, chunk); err != nil {
			t.Fatal(err)
		}
	}

	var shown strings.Builder
	for _, emission := range sink.Emissions {
		shown.WriteString(emission.Text)
	}
	if shown.String() != "季度营收如下。\n\n有问题再说。" {
		t.Fatalf("client saw %q", shown.String())
	}
	if strings.Contains(shown.String(), "<") || strings.Contains(shown.String(), "chartType") {
		t.Fatalf("fence leaked into the preview: %q", shown.String())
	}

	// The terminal path falls back to the recorded deltas when the engine reports
	// no final text, so the fence has to still be there.
	joined := recorder.Joined()
	if !strings.Contains(joined, a2ui.FenceStart) || !strings.Contains(joined, "chartType") {
		t.Fatalf("recorded deltas lost the surface: %q", joined)
	}
	if _, payload, result := a2ui.SplitTextAndA2UI(joined); payload == nil || result != a2ui.EmitOK {
		t.Fatalf("extraction from recorded deltas: result=%s payload=%v", result, payload)
	}
}

// Without a preview nothing changes: a run whose model was never told about the
// fence must stream exactly as before.
func TestStreamDeltaRecorderWithoutPreviewStreamsVerbatim(t *testing.T) {
	t.Parallel()

	sink := &chatruntimebridge.RecordingTextDeltaSink{}
	recorder := &chatruntimebridge.StreamDeltaRecorder{Sink: sink}
	if err := recorder.OnTextDelta(context.Background(), a2ui.FenceStart+"x"); err != nil {
		t.Fatal(err)
	}
	if len(sink.Emissions) != 1 || sink.Emissions[0].Text != a2ui.FenceStart+"x" {
		t.Fatalf("emissions = %+v", sink.Emissions)
	}
}

// An interrupt mid-surface reports the prose as partial text. Half a fence is not
// something a client can render or a human can read.
func TestStreamDeltaRecorderMasksPartialTextOnFailure(t *testing.T) {
	t.Parallel()

	sink := &chatruntimebridge.RecordingTextDeltaSink{}
	recorder := &chatruntimebridge.StreamDeltaRecorder{
		Sink: sink, Preview: &a2ui.FencePreview{},
	}
	ctx := context.Background()
	for _, chunk := range []string{"正在整理。", a2ui.FenceStart, fenceSurface[:18]} {
		if err := recorder.OnTextDelta(ctx, chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.FailIncomplete(ctx, "MODEL_STREAM_INTERRUPTED", true); err != nil {
		t.Fatal(err)
	}
	if sink.Failure == nil {
		t.Fatal("no failure recorded")
	}
	if sink.Failure.PartialText != "正在整理。" {
		t.Fatalf("partial text = %q", sink.Failure.PartialText)
	}
}
