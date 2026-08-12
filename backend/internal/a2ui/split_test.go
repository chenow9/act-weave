package a2ui_test

import (
	"encoding/json"
	"strings"
	"testing"

	"actweave/backend/internal/a2ui"
)

func TestSplitTextAndA2UI_NoFence(t *testing.T) {
	t.Parallel()
	text, payload, result := a2ui.SplitTextAndA2UI("hello plain")
	if result != a2ui.EmitNone || payload != nil || text != "hello plain" {
		t.Fatalf("got text=%q payload=%v result=%s", text, payload, result)
	}
}

func TestSplitTextAndA2UI_ValidWithText(t *testing.T) {
	t.Parallel()
	full := "Please confirm:\n\n" + a2ui.FenceStart + "\n" +
		`{"version":"a2ui-surface.v0","catalogId":"standard","surface":{"root":"form","password":{"label":"Password"}}}` +
		"\n" + a2ui.FenceEnd + "\n"
	text, payload, result := a2ui.SplitTextAndA2UI(full)
	if result != a2ui.EmitOK {
		t.Fatalf("result=%s", result)
	}
	if text != "Please confirm:" {
		t.Fatalf("text=%q", text)
	}
	if payload == nil || payload.Version != a2ui.EnvelopeVersionV0 || payload.CatalogID != "standard" {
		t.Fatalf("payload=%+v", payload)
	}
	if !strings.Contains(string(payload.Surface), `"password"`) {
		t.Fatalf("surface=%s", payload.Surface)
	}
}

func TestSplitTextAndA2UI_EmptyTextValidA2UI(t *testing.T) {
	t.Parallel()
	full := a2ui.FenceStart + `{"surface":{"root":"only"}}` + a2ui.FenceEnd
	text, payload, result := a2ui.SplitTextAndA2UI(full)
	if result != a2ui.EmitOKEmptyText || text != "" || payload == nil {
		t.Fatalf("text=%q payload=%v result=%s", text, payload, result)
	}
	if payload.Version != a2ui.EnvelopeVersionV0 {
		t.Fatalf("default version=%q", payload.Version)
	}
}

func TestSplitTextAndA2UI_InvalidJSON(t *testing.T) {
	t.Parallel()
	full := "intro\n" + a2ui.FenceStart + `{not-json` + a2ui.FenceEnd
	text, payload, result := a2ui.SplitTextAndA2UI(full)
	if result != a2ui.EmitInvalidJSON || payload != nil || text != "intro" {
		t.Fatalf("text=%q payload=%v result=%s", text, payload, result)
	}
}

func TestSplitTextAndA2UI_NonObjectSurface(t *testing.T) {
	t.Parallel()
	full := a2ui.FenceStart + `{"surface":["array"]}` + a2ui.FenceEnd
	_, payload, result := a2ui.SplitTextAndA2UI(full)
	if result != a2ui.EmitInvalidJSON || payload != nil {
		t.Fatalf("payload=%v result=%s", payload, result)
	}
}

func TestSplitTextAndA2UI_TooLarge(t *testing.T) {
	t.Parallel()
	// Build surface larger than MaxSurfaceBytes.
	padding := strings.Repeat("x", a2ui.MaxSurfaceBytes)
	surface := `{"x":"` + padding + `"}`
	if len(surface) <= a2ui.MaxSurfaceBytes {
		t.Fatalf("test surface not oversized: %d", len(surface))
	}
	full := a2ui.FenceStart + `{"surface":` + surface + `}` + a2ui.FenceEnd
	_, payload, result := a2ui.SplitTextAndA2UI(full)
	if result != a2ui.EmitTooLarge || payload != nil {
		t.Fatalf("payload=%v result=%s", payload, result)
	}
}

func TestSplitTextAndA2UI_TruncatedMultiFence(t *testing.T) {
	t.Parallel()
	full := "A\n" + a2ui.FenceStart + `{"surface":{"a":1}}` + a2ui.FenceEnd +
		"\n" + a2ui.FenceStart + `{"surface":{"b":2}}` + a2ui.FenceEnd
	text, payload, result := a2ui.SplitTextAndA2UI(full)
	if result != a2ui.EmitTruncated {
		t.Fatalf("result=%s", result)
	}
	if payload == nil || !strings.Contains(string(payload.Surface), `"a"`) {
		t.Fatalf("payload=%v", payload)
	}
	// Outer text keeps second fence body as residual prose after first pair.
	if !strings.Contains(text, "A") {
		t.Fatalf("text=%q", text)
	}
}

func TestSplitTextAndA2UI_IncompleteFenceLeftRaw(t *testing.T) {
	t.Parallel()
	full := "x " + a2ui.FenceStart + `{"surface":{}}`
	text, payload, result := a2ui.SplitTextAndA2UI(full)
	if result != a2ui.EmitNone || payload != nil || text != full {
		t.Fatalf("text=%q payload=%v result=%s", text, payload, result)
	}
}

func TestNonEmptyOrFallback(t *testing.T) {
	t.Parallel()
	if got := a2ui.NonEmptyOrFallback("  hi  ", "full"); got != "  hi  " {
		t.Fatalf("got=%q", got)
	}
	if got := a2ui.NonEmptyOrFallback("  ", "full-raw"); got != "full-raw" {
		t.Fatalf("got=%q", got)
	}
}

func TestSerializeAssistantDurable(t *testing.T) {
	t.Parallel()
	plain, err := a2ui.SerializeAssistantDurable("hello", nil)
	if err != nil || plain != "hello" {
		t.Fatalf("plain=%q err=%v", plain, err)
	}

	surface := json.RawMessage(`{"root":"card","password":{"label":"Pw"}}`)
	durable, err := a2ui.SerializeAssistantDurable("Fill form", &a2ui.Payload{
		Version: a2ui.EnvelopeVersionV0, CatalogID: "standard", Surface: surface,
	})
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		SchemaVersion string `json:"schemaVersion"`
		Parts         []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Version   string          `json:"version"`
			CatalogID string          `json:"catalogId"`
			Surface   json.RawMessage `json:"surface"`
		} `json:"parts"`
	}
	if err := json.Unmarshal([]byte(durable), &env); err != nil {
		t.Fatal(err)
	}
	if env.SchemaVersion != a2ui.MessageContentSchemaVersion || len(env.Parts) != 2 {
		t.Fatalf("envelope=%+v", env)
	}
	if env.Parts[0].Type != "text" || env.Parts[0].Text != "Fill form" {
		t.Fatalf("text part=%+v", env.Parts[0])
	}
	if env.Parts[1].Type != "a2ui" || !strings.Contains(string(env.Parts[1].Surface), "password") {
		t.Fatalf("a2ui part=%+v", env.Parts[1])
	}

	// Empty text + a2ui still produces non-empty envelope (KD-16).
	empty, err := a2ui.SerializeAssistantDurable("", &a2ui.Payload{Surface: json.RawMessage(`{"root":"x"}`)})
	if err != nil || strings.TrimSpace(empty) == "" {
		t.Fatalf("empty text envelope=%q err=%v", empty, err)
	}
	if !strings.Contains(empty, `"text":""`) {
		t.Fatalf("want empty text field in %s", empty)
	}
}

func TestAppendPromptRules(t *testing.T) {
	t.Parallel()
	base := "You are helpful."
	once := a2ui.AppendPromptRules(base)
	if !strings.Contains(once, a2ui.PromptTemplateV1) || !strings.Contains(once, a2ui.FenceStart) {
		t.Fatalf("missing prompt rules: %s", once)
	}
	if !strings.Contains(once, base) {
		t.Fatalf("lost base instruction")
	}
	// Idempotent: second append is a no-op.
	twice := a2ui.AppendPromptRules(once)
	if twice != once {
		t.Fatalf("double inject changed instruction")
	}
}

func TestPrepareAssistantContent_Paths(t *testing.T) {
	t.Parallel()
	surfaceJSON := `{"version":"a2ui-surface.v0","surface":{"root":"form","accessToken":{"label":"Token"}}}`
	full := "Confirm\n" + a2ui.FenceStart + surfaceJSON + a2ui.FenceEnd

	// Enable + projection on → attach.
	got := a2ui.PrepareAssistantContent(full, a2ui.PrepareOptions{EnableA2UI: true, ProjectionEnabled: true})
	if !got.AttachedA2UI || got.Result != a2ui.EmitOK {
		t.Fatalf("attach path: %+v", got)
	}
	if !strings.Contains(got.Content, a2ui.MessageContentSchemaVersion) {
		t.Fatalf("want v1 envelope, got %s", got.Content)
	}

	// Empty text + valid a2ui.
	onlyFence := a2ui.FenceStart + `{"surface":{"root":"x"}}` + a2ui.FenceEnd
	empty := a2ui.PrepareAssistantContent(onlyFence, a2ui.PrepareOptions{EnableA2UI: true, ProjectionEnabled: true})
	if !empty.AttachedA2UI || empty.Result != a2ui.EmitOKEmptyText || empty.Text != "" {
		t.Fatalf("empty text path: %+v", empty)
	}
	if strings.TrimSpace(empty.Content) == "" {
		t.Fatal("envelope must be non-empty for RecordAssistantResult")
	}

	// Invalid → fallback raw when outer empty.
	bad := a2ui.FenceStart + `{bad` + a2ui.FenceEnd
	degraded := a2ui.PrepareAssistantContent(bad, a2ui.PrepareOptions{EnableA2UI: true, ProjectionEnabled: true})
	if degraded.AttachedA2UI || degraded.Result != a2ui.EmitInvalidJSON || degraded.Content != bad {
		t.Fatalf("invalid fallback: %+v", degraded)
	}

	// Capability off strips fence.
	off := a2ui.PrepareAssistantContent(full, a2ui.PrepareOptions{EnableA2UI: false, ProjectionEnabled: true})
	if off.AttachedA2UI || off.Result != a2ui.EmitStrippedDisabled || off.Content != "Confirm" {
		t.Fatalf("disabled strip: %+v", off)
	}

	// Env projection off.
	projOff := a2ui.PrepareAssistantContent(full, a2ui.PrepareOptions{EnableA2UI: true, ProjectionEnabled: false})
	if projOff.AttachedA2UI || projOff.Result != a2ui.EmitProjectionOff {
		t.Fatalf("projection off: %+v", projOff)
	}

	// Degrade after preflight.
	deg := a2ui.DegradeToTextOnly(full, got)
	if deg.AttachedA2UI || deg.Result != a2ui.EmitProjectionRejected || deg.Content != "Confirm" {
		t.Fatalf("degrade: %+v", deg)
	}
}

func TestProjectionEnabledEnv(t *testing.T) {
	t.Setenv(a2ui.EnvProjection, "")
	if !a2ui.ProjectionEnabled() {
		t.Fatal("default enabled")
	}
	t.Setenv(a2ui.EnvProjection, "off")
	if a2ui.ProjectionEnabled() {
		t.Fatal("off must disable")
	}
	t.Setenv(a2ui.EnvProjection, "OFF")
	if a2ui.ProjectionEnabled() {
		t.Fatal("OFF must disable")
	}
}

func TestSplitTextAndA2UI_BareSurfaceObject(t *testing.T) {
	t.Parallel()
	// Models often emit surface JSON directly (no envelope.surface wrapper).
	full := `Hello, pick a city:<<<A2UI>>>{"components":[{"id":"root","component":"Column"}]}<<<END_A2UI>>>`
	text, payload, result := a2ui.SplitTextAndA2UI(full)
	if result != a2ui.EmitOK {
		t.Fatalf("result=%s want ok", result)
	}
	if text != "Hello, pick a city:" {
		t.Fatalf("text=%q", text)
	}
	if payload == nil || !strings.Contains(string(payload.Surface), `"components"`) {
		t.Fatalf("payload surface=%v", payload)
	}
	prepared := a2ui.PrepareAssistantContent(full, a2ui.PrepareOptions{EnableA2UI: true, ProjectionEnabled: true})
	if !prepared.AttachedA2UI {
		t.Fatalf("expected attached a2ui, result=%s content=%s", prepared.Result, prepared.Content)
	}
}
