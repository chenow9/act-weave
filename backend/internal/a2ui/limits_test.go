package a2ui_test

import (
	"testing"

	"actweave/backend/internal/a2ui"
)

func TestLimitsConstants(t *testing.T) {
	t.Parallel()
	if a2ui.MaxSurfaceBytes != 65536 {
		t.Fatalf("MaxSurfaceBytes=%d, want 65536", a2ui.MaxSurfaceBytes)
	}
	if a2ui.EnvelopeVersionV0 != "a2ui-surface.v0" {
		t.Fatalf("EnvelopeVersionV0=%q", a2ui.EnvelopeVersionV0)
	}
	if a2ui.PromptTemplateV1 != "a2ui-prompt.v1" {
		t.Fatalf("PromptTemplateV1=%q", a2ui.PromptTemplateV1)
	}
	if a2ui.FenceStart != "<<<A2UI>>>" || a2ui.FenceEnd != "<<<END_A2UI>>>" {
		t.Fatalf("fence markers start=%q end=%q", a2ui.FenceStart, a2ui.FenceEnd)
	}
}
