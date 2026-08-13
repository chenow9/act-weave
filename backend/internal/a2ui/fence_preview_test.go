package a2ui_test

import (
	"strings"
	"testing"

	"actweave/backend/internal/a2ui"
)

// feed streams text through a preview in fixed-size chunks and returns what a
// client would have seen.
func feed(text string, chunk int) string {
	var preview a2ui.FencePreview
	var seen strings.Builder
	for start := 0; start < len(text); start += chunk {
		end := start + chunk
		if end > len(text) {
			end = len(text)
		}
		seen.WriteString(preview.Feed(text[start:end]))
	}
	seen.WriteString(preview.Flush())
	return seen.String()
}

func TestFencePreviewKeepsProseAndHidesTheSurface(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct{ full, want string }{
		"no fence": {
			full: "季度营收如下，请看图表。",
			want: "季度营收如下，请看图表。",
		},
		"prose then fence": {
			full: "季度营收如下。\n\n" + a2ui.FenceStart +
				`{"components":[{"id":"root","component":"Text","text":"x"}]}` + a2ui.FenceEnd,
			want: "季度营收如下。\n\n",
		},
		"prose on both sides": {
			full: "前面。" + a2ui.FenceStart + `{"components":[]}` + a2ui.FenceEnd + "后面。",
			want: "前面。后面。",
		},
		"two fences": {
			full: "a" + a2ui.FenceStart + `{"x":1}` + a2ui.FenceEnd + "b" +
				a2ui.FenceStart + `{"y":2}` + a2ui.FenceEnd + "c",
			want: "abc",
		},
		// An unclosed fence is a surface body that never ended. The terminal path
		// drops such a body whole, so the preview must not show its start either.
		"unclosed fence": {
			full: "正在整理。" + a2ui.FenceStart + `{"components":[{"id":"root"`,
			want: "正在整理。",
		},
		"only a surface": {
			full: a2ui.FenceStart + `{"components":[]}` + a2ui.FenceEnd,
			want: "",
		},
		// Text that merely starts like a marker is prose and must arrive whole.
		"marker lookalike": {
			full: "用 <<< 包起来，或写 <<<A2X>>> 这种。",
			want: "用 <<< 包起来，或写 <<<A2X>>> 这种。",
		},
		"trailing marker prefix": {
			full: "结束了 <<<A2U",
			want: "结束了 <<<A2U",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Every chunking of the same bytes must look identical to a client:
			// a marker split across chunks is the case that used to leak.
			for chunk := 1; chunk <= len(testCase.full); chunk++ {
				if got := feed(testCase.full, chunk); got != testCase.want {
					t.Fatalf("chunk=%d\n got %q\nwant %q", chunk, got, testCase.want)
				}
			}
			if got := a2ui.MaskFences(testCase.full); got != testCase.want {
				t.Fatalf("MaskFences got %q want %q", got, testCase.want)
			}
		})
	}
}

// Whatever the chunking, no fragment of a marker may reach a client — that is the
// whole point of withholding a tail rather than showing it and taking it back.
func TestFencePreviewNeverShowsAMarkerFragment(t *testing.T) {
	t.Parallel()

	full := "报表如下。" + a2ui.FenceStart +
		`{"components":[{"id":"root","component":"Chart","chartType":"bar"}]}` +
		a2ui.FenceEnd + "有问题再说。"
	for chunk := 1; chunk <= len(full); chunk++ {
		var preview a2ui.FencePreview
		for start := 0; start < len(full); start += chunk {
			end := start + chunk
			if end > len(full) {
				end = len(full)
			}
			visible := preview.Feed(full[start:end])
			if strings.Contains(visible, "<") || strings.Contains(visible, "components") {
				t.Fatalf("chunk=%d leaked %q", chunk, visible)
			}
		}
	}
}

// The surface body is dropped, not buffered for later: a completed run replaces
// the preview anyway, and a body has no meaning as prose.
func TestFencePreviewFlushWithholdsAnUnclosedBody(t *testing.T) {
	t.Parallel()

	var preview a2ui.FencePreview
	if got := preview.Feed("先说结论。" + a2ui.FenceStart + `{"compo`); got != "先说结论。" {
		t.Fatalf("visible=%q", got)
	}
	if got := preview.Flush(); got != "" {
		t.Fatalf("flush=%q want nothing while inside a fence", got)
	}
}

// A nil preview is the "no A2UI on this run" case and must change nothing.
func TestNilFencePreviewPassesTextThrough(t *testing.T) {
	t.Parallel()

	var preview *a2ui.FencePreview
	if got := preview.Feed(a2ui.FenceStart + "x"); got != a2ui.FenceStart+"x" {
		t.Fatalf("nil preview altered text: %q", got)
	}
	if got := preview.Flush(); got != "" {
		t.Fatalf("nil preview flushed %q", got)
	}
}
