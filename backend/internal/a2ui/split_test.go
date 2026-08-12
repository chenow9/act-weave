package a2ui_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"actweave/backend/internal/a2ui"
)

// validSurface is catalog-conformant; Split accepts any JSON object, but the
// write path only attaches surfaces that pass ValidateSurface.
const validSurface = `{"components":[` +
	`{"id":"root","component":"Column","children":["t1"]},` +
	`{"id":"t1","component":"Text","text":"Hi"}]}`

const validChartSurface = `{"components":[` +
	`{"id":"root","component":"Chart","chartType":"bar",` +
	`"series":[{"points":[{"label":"Q1","value":12}]}]}]}`

const testSurfaceID = "msg:019ff3f0-bfdd-7b38-9c53-f90bf5812478"

func prepareOptions() a2ui.PrepareOptions {
	return a2ui.PrepareOptions{EnableA2UI: true, ProjectionEnabled: true, SurfaceID: testSurfaceID}
}

func TestSplitTextAndA2UI_NoFence(t *testing.T) {
	t.Parallel()
	text, payload, result := a2ui.SplitTextAndA2UI("hello plain")
	if result != a2ui.EmitNone || payload != nil || text != "hello plain" {
		t.Fatalf("got text=%q payload=%v result=%s", text, payload, result)
	}
}

func TestSplitTextAndA2UI_ValidWithText(t *testing.T) {
	t.Parallel()
	full := "Please confirm:\n\n" + a2ui.FenceStart + "\n" + validSurface + "\n" + a2ui.FenceEnd + "\n"
	text, payload, result := a2ui.SplitTextAndA2UI(full)
	if result != a2ui.EmitOK {
		t.Fatalf("result=%s", result)
	}
	if text != "Please confirm:" {
		t.Fatalf("text=%q", text)
	}
	if payload == nil {
		t.Fatal("payload=nil")
	}
	// Identity is assigned by the write path, never read from model output.
	if payload.Version != "" || payload.CatalogID != "" {
		t.Fatalf("split must not assign identity: %+v", payload)
	}
	if !strings.Contains(string(payload.Surface), `"components"`) {
		t.Fatalf("surface=%s", payload.Surface)
	}
}

// TestSplitTextAndA2UI_FenceBodyIsTheSurface pins the contract that removed the
// bare-versus-wrapped ambiguity: whatever is inside the fence is the surface,
// so a wrapper is preserved verbatim and later rejected by validation.
func TestSplitTextAndA2UI_FenceBodyIsTheSurface(t *testing.T) {
	t.Parallel()
	wrapped := `{"version":"x","catalogId":"standard","surface":{"components":[]}}`
	full := a2ui.FenceStart + wrapped + a2ui.FenceEnd
	_, payload, result := a2ui.SplitTextAndA2UI(full)
	if result != a2ui.EmitOKEmptyText || payload == nil {
		t.Fatalf("payload=%v result=%s", payload, result)
	}
	if string(payload.Surface) != wrapped {
		t.Fatalf("surface=%s, want the fence body unchanged", payload.Surface)
	}
	prepared := a2ui.PrepareAssistantContent(full, prepareOptions())
	if prepared.AttachedA2UI || prepared.Result != a2ui.EmitCatalogInvalid {
		t.Fatalf("wrapper must be rejected: %+v", prepared)
	}
	if prepared.Diagnostic == nil || prepared.Diagnostic.Keyword != "additionalProperties" {
		t.Fatalf("diagnostic=%v", prepared.Diagnostic)
	}
}

func TestSplitTextAndA2UI_EmptyTextValidA2UI(t *testing.T) {
	t.Parallel()
	full := a2ui.FenceStart + validSurface + a2ui.FenceEnd
	text, payload, result := a2ui.SplitTextAndA2UI(full)
	if result != a2ui.EmitOKEmptyText || text != "" || payload == nil {
		t.Fatalf("text=%q payload=%v result=%s", text, payload, result)
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
	for _, body := range []string{`["array"]`, `"text"`, `42`, `null`, `true`} {
		full := a2ui.FenceStart + body + a2ui.FenceEnd
		_, payload, result := a2ui.SplitTextAndA2UI(full)
		if result != a2ui.EmitInvalidJSON || payload != nil {
			t.Fatalf("body=%s payload=%v result=%s", body, payload, result)
		}
	}
}

func TestSplitTextAndA2UI_TooLarge(t *testing.T) {
	t.Parallel()
	padding := strings.Repeat("x", a2ui.MaxSurfaceBytes)
	surface := `{"x":"` + padding + `"}`
	if len(surface) <= a2ui.MaxSurfaceBytes {
		t.Fatalf("test surface not oversized: %d", len(surface))
	}
	full := a2ui.FenceStart + surface + a2ui.FenceEnd
	_, payload, result := a2ui.SplitTextAndA2UI(full)
	if result != a2ui.EmitTooLarge || payload != nil {
		t.Fatalf("payload=%v result=%s", payload, result)
	}
}

func TestSplitTextAndA2UI_TruncatedMultiFence(t *testing.T) {
	t.Parallel()
	full := "A\n" + a2ui.FenceStart + `{"a":1}` + a2ui.FenceEnd +
		"\n" + a2ui.FenceStart + `{"b":2}` + a2ui.FenceEnd
	text, payload, result := a2ui.SplitTextAndA2UI(full)
	if result != a2ui.EmitTruncated {
		t.Fatalf("result=%s", result)
	}
	if payload == nil || !strings.Contains(string(payload.Surface), `"a"`) {
		t.Fatalf("payload=%v", payload)
	}
	if !strings.Contains(text, "A") {
		t.Fatalf("text=%q", text)
	}
}

func TestSplitTextAndA2UI_IncompleteFenceLeftRaw(t *testing.T) {
	t.Parallel()
	full := "x " + a2ui.FenceStart + `{"components":[]}`
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

	surface := json.RawMessage(validSurface)
	durable, err := a2ui.SerializeAssistantDurable("Fill form", &a2ui.Payload{
		Version: a2ui.EnvelopeVersionV1, CatalogID: a2ui.CatalogID, Surface: surface,
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
	if env.Parts[1].Type != "a2ui" || env.Parts[1].Version != a2ui.EnvelopeVersionV1 {
		t.Fatalf("a2ui part=%+v", env.Parts[1])
	}
	if env.Parts[1].CatalogID != a2ui.CatalogID {
		t.Fatalf("catalogId=%q", env.Parts[1].CatalogID)
	}

	// Empty text + a2ui still produces non-empty envelope (KD-16).
	empty, err := a2ui.SerializeAssistantDurable("", &a2ui.Payload{Surface: json.RawMessage(validSurface)})
	if err != nil || strings.TrimSpace(empty) == "" {
		t.Fatalf("empty text envelope=%q err=%v", empty, err)
	}
	if !strings.Contains(empty, `"text":""`) {
		t.Fatalf("want empty text field in %s", empty)
	}
	if !strings.Contains(empty, a2ui.EnvelopeVersionV1) {
		t.Fatalf("want v1 default version in %s", empty)
	}
}

func TestAppendPromptRules(t *testing.T) {
	t.Parallel()
	base := "You are helpful."
	once := a2ui.AppendPromptRules(base)
	if !strings.Contains(once, a2ui.PromptTemplateV2) || !strings.Contains(once, a2ui.FenceStart) {
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
	full := "Confirm\n" + a2ui.FenceStart + validSurface + a2ui.FenceEnd

	// Enable + projection on → attach.
	got := a2ui.PrepareAssistantContent(full, prepareOptions())
	if !got.AttachedA2UI || got.Result != a2ui.EmitOK {
		t.Fatalf("attach path: %+v", got)
	}
	if !strings.Contains(got.Content, a2ui.MessageContentSchemaVersion) {
		t.Fatalf("want v1 envelope, got %s", got.Content)
	}

	// Empty text + valid a2ui.
	onlyFence := a2ui.FenceStart + validSurface + a2ui.FenceEnd
	empty := a2ui.PrepareAssistantContent(onlyFence, prepareOptions())
	if !empty.AttachedA2UI || empty.Result != a2ui.EmitOKEmptyText || empty.Text != "" {
		t.Fatalf("empty text path: %+v", empty)
	}
	if strings.TrimSpace(empty.Content) == "" {
		t.Fatal("envelope must be non-empty for RecordAssistantResult")
	}

	// Invalid → fallback raw when outer empty.
	bad := a2ui.FenceStart + `{bad` + a2ui.FenceEnd
	degraded := a2ui.PrepareAssistantContent(bad, prepareOptions())
	if degraded.AttachedA2UI || degraded.Result != a2ui.EmitInvalidJSON || degraded.Content != bad {
		t.Fatalf("invalid fallback: %+v", degraded)
	}

	// Capability off strips fence.
	off := a2ui.PrepareAssistantContent(full, a2ui.PrepareOptions{ProjectionEnabled: true})
	if off.AttachedA2UI || off.Result != a2ui.EmitStrippedDisabled || off.Content != "Confirm" {
		t.Fatalf("disabled strip: %+v", off)
	}

	// Env projection off.
	projOff := a2ui.PrepareAssistantContent(full, a2ui.PrepareOptions{EnableA2UI: true})
	if projOff.AttachedA2UI || projOff.Result != a2ui.EmitProjectionOff {
		t.Fatalf("projection off: %+v", projOff)
	}

	// Degrade after preflight.
	deg := a2ui.DegradeToTextOnly(full, got)
	if deg.AttachedA2UI || deg.Result != a2ui.EmitProjectionRejected || deg.Content != "Confirm" {
		t.Fatalf("degrade: %+v", deg)
	}
}

// TestPrepareAssistantContent_CatalogInvalidDegrades covers the rule that makes
// strictness safe: a non-conformant surface costs the surface, not the reply.
func TestPrepareAssistantContent_CatalogInvalidDegrades(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"unknown component":  `{"components":[{"id":"root","component":"PieChart","data":[]}]}`,
		"missing root":       `{"components":[{"id":"a1","component":"Text","text":"x"}]}`,
		"visual property":    `{"components":[{"id":"root","component":"Text","text":"x","color":"#f00"}]}`,
		"dangling child":     `{"components":[{"id":"root","component":"Column","children":["nope"]}]}`,
		"pie two series":     `{"components":[{"id":"root","component":"Chart","chartType":"pie","series":[{"name":"a","points":[{"label":"A","value":1}]},{"name":"b","points":[{"label":"B","value":2}]}]}]}`,
		"legacy chart shape": `{"components":[{"id":"root","component":"Chart","chartType":"bar","labels":["A"],"series":[{"name":"s","data":[1]}]}]}`,
	}
	for name, surface := range cases {
		t.Run(name, func(t *testing.T) {
			full := "Here you go:\n" + a2ui.FenceStart + surface + a2ui.FenceEnd
			prepared := a2ui.PrepareAssistantContent(full, prepareOptions())
			if prepared.AttachedA2UI {
				t.Fatalf("surface must not be attached: %+v", prepared)
			}
			if prepared.Result != a2ui.EmitCatalogInvalid {
				t.Fatalf("result=%s want catalog_invalid", prepared.Result)
			}
			if prepared.Diagnostic == nil {
				t.Fatal("want a diagnostic")
			}
			// The prose survives; only the surface is dropped.
			if prepared.Content != "Here you go:" {
				t.Fatalf("content=%q", prepared.Content)
			}
		})
	}
}

// TestPrepareAssistantContent_MaterializesIdentity checks that the persisted
// surface carries platform identity, which is what makes it consumable by a
// conforming A2UI renderer.
func TestPrepareAssistantContent_MaterializesIdentity(t *testing.T) {
	t.Parallel()
	full := a2ui.FenceStart + validChartSurface + a2ui.FenceEnd
	prepared := a2ui.PrepareAssistantContent(full, prepareOptions())
	if !prepared.AttachedA2UI || prepared.Payload == nil {
		t.Fatalf("prepared=%+v", prepared)
	}
	var surface struct {
		SurfaceID  string `json:"surfaceId"`
		CatalogID  string `json:"catalogId"`
		Components []any  `json:"components"`
	}
	if err := json.Unmarshal(prepared.Payload.Surface, &surface); err != nil {
		t.Fatal(err)
	}
	if surface.SurfaceID != testSurfaceID {
		t.Fatalf("surfaceId=%q want %q", surface.SurfaceID, testSurfaceID)
	}
	if surface.CatalogID != a2ui.CatalogID {
		t.Fatalf("catalogId=%q", surface.CatalogID)
	}
	if len(surface.Components) != 1 {
		t.Fatalf("components=%d", len(surface.Components))
	}
	if prepared.Payload.Version != a2ui.EnvelopeVersionV1 {
		t.Fatalf("version=%q", prepared.Payload.Version)
	}
	// A materialized surface must still satisfy the contract it was checked against.
	if diagnostic := a2ui.ValidateSurface(a2ui.CatalogID, prepared.Payload.Surface); diagnostic != nil {
		t.Fatalf("materialized surface no longer valid: %v", diagnostic)
	}
}

// TestPrepareAssistantContent_MissingSurfaceIDDegrades keeps identity mandatory:
// a surface with no id cannot be addressed by a renderer, so it is not persisted.
func TestPrepareAssistantContent_MissingSurfaceIDDegrades(t *testing.T) {
	t.Parallel()
	full := "Text\n" + a2ui.FenceStart + validSurface + a2ui.FenceEnd
	prepared := a2ui.PrepareAssistantContent(full, a2ui.PrepareOptions{
		EnableA2UI: true, ProjectionEnabled: true,
	})
	if prepared.AttachedA2UI || prepared.Result != a2ui.EmitCatalogInvalid {
		t.Fatalf("prepared=%+v", prepared)
	}
	if prepared.Content != "Text" {
		t.Fatalf("content=%q", prepared.Content)
	}
}

// TestPrepareAssistantContent_TooLargeAfterMaterializeDegrades separates a size
// problem from a contract problem: the surface was conformant, so it reports
// too_large rather than catalog_invalid.
func TestPrepareAssistantContent_TooLargeAfterMaterializeDegrades(t *testing.T) {
	t.Parallel()
	// Every member has its own length cap, so an oversized *valid* surface has to
	// be assembled from charts at their structural limits.
	surface := maxedOutChartSurface(t)
	if len(surface) <= a2ui.MaxSurfaceBytes {
		t.Fatalf("fixture is not oversized: %d bytes", len(surface))
	}
	full := "Report\n" + a2ui.FenceStart + surface + a2ui.FenceEnd
	prepared := a2ui.PrepareAssistantContent(full, prepareOptions())
	if prepared.AttachedA2UI {
		t.Fatalf("oversized surface must not be attached: %s", prepared.Result)
	}
	if prepared.Result != a2ui.EmitTooLarge {
		t.Fatalf("result=%s want too_large", prepared.Result)
	}
	if prepared.Content != "Report" {
		t.Fatalf("content=%q", prepared.Content)
	}
}

// maxedOutChartSurface builds a catalog-conformant surface that exceeds the wire
// limit: charts at 8 series x 64 points with maximum-length labels.
func maxedOutChartSurface(t *testing.T) string {
	t.Helper()
	label := strings.Repeat("L", 48)
	points := make([]string, 0, 64)
	for index := 0; index < 64; index++ {
		points = append(points, fmt.Sprintf(`{"label":"%s%02d","value":%d}`, label[:46], index, index))
	}
	series := make([]string, 0, 8)
	for index := 0; index < 8; index++ {
		series = append(series, fmt.Sprintf(`{"name":"series-%d","points":[%s]}`,
			index, strings.Join(points, ",")))
	}
	charts := make([]string, 0, 4)
	children := make([]string, 0, 4)
	for index := 0; index < 4; index++ {
		id := fmt.Sprintf("chart%d", index)
		children = append(children, `"`+id+`"`)
		charts = append(charts, fmt.Sprintf(
			`{"id":%q,"component":"Chart","chartType":"line","series":[%s]}`,
			id, strings.Join(series, ",")))
	}
	return `{"components":[{"id":"root","component":"Column","children":[` +
		strings.Join(children, ",") + `]},` + strings.Join(charts, ",") + `]}`
}

// TestMaterializeSurfaceOverwritesModelIdentity: a model that guesses identity
// must not be able to impersonate another surface or catalog.
func TestMaterializeSurfaceOverwritesModelIdentity(t *testing.T) {
	t.Parallel()
	claimed := `{"surfaceId":"someone-elses","catalogId":"` + a2ui.CatalogID +
		`","components":[{"id":"root","component":"Divider"}]}`
	materialized, err := a2ui.MaterializeSurface(json.RawMessage(claimed), testSurfaceID)
	if err != nil {
		t.Fatal(err)
	}
	var surface struct {
		SurfaceID string `json:"surfaceId"`
		CatalogID string `json:"catalogId"`
	}
	if err := json.Unmarshal(materialized, &surface); err != nil {
		t.Fatal(err)
	}
	if surface.SurfaceID != testSurfaceID || surface.CatalogID != a2ui.CatalogID {
		t.Fatalf("identity not overwritten: %+v", surface)
	}
}

func TestMaterializeSurfaceRejectsBadInput(t *testing.T) {
	t.Parallel()
	if _, err := a2ui.MaterializeSurface(json.RawMessage(validSurface), ""); err == nil {
		t.Fatal("empty surfaceId must fail")
	}
	if _, err := a2ui.MaterializeSurface(json.RawMessage(validSurface), "has space"); err == nil {
		t.Fatal("malformed surfaceId must fail")
	}
	if _, err := a2ui.MaterializeSurface(json.RawMessage(`[]`), testSurfaceID); err == nil {
		t.Fatal("non-object surface must fail")
	}
}

func TestSurfaceIDFor(t *testing.T) {
	t.Parallel()
	if got := a2ui.SurfaceIDFor("019ff3f0-bfdd-7b38-9c53-f90bf5812478"); got != testSurfaceID {
		t.Fatalf("SurfaceIDFor=%q", got)
	}
	if got := a2ui.SurfaceIDFor("  "); got != "" {
		t.Fatalf("blank message id must yield empty surface id, got %q", got)
	}
}

func TestChartTypesIn(t *testing.T) {
	t.Parallel()
	surface := json.RawMessage(`{"components":[` +
		`{"id":"root","component":"Column","children":["c1","c2"]},` +
		`{"id":"c1","component":"Chart","chartType":"line","series":[]},` +
		`{"id":"c2","component":"Chart","chartType":"donut","series":[]}]}`)
	types := a2ui.ChartTypesIn(surface)
	if len(types) != 2 || types[0] != "line" || types[1] != "donut" {
		t.Fatalf("ChartTypesIn=%v", types)
	}
	if got := a2ui.ChartTypesIn(json.RawMessage(`not json`)); got != nil {
		t.Fatalf("malformed surface=%v", got)
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
