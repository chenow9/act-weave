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
	if a2ui.EnvelopeVersionV1 != "a2ui-surface.v1" {
		t.Fatalf("EnvelopeVersionV1=%q", a2ui.EnvelopeVersionV1)
	}
	if a2ui.PromptTemplateV2 != "a2ui-prompt.v2" {
		t.Fatalf("PromptTemplateV2=%q", a2ui.PromptTemplateV2)
	}
	if a2ui.FenceStart != "<<<A2UI>>>" || a2ui.FenceEnd != "<<<END_A2UI>>>" {
		t.Fatalf("fence markers start=%q end=%q", a2ui.FenceStart, a2ui.FenceEnd)
	}
}
