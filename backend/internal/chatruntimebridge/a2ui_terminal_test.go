package chatruntimebridge_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/a2ui"
	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/sessioncontext"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// password surface golden: preflight path identical to marshalProjectionItem must pass.
func TestGoldenA2UI_PasswordSurfacePreflightPass(t *testing.T) {
	t.Parallel()
	surface := map[string]any{
		"password":     "label-for-password-field",
		"accessToken":  "binding.path.token",
		"apiKey":       "binding.path.apiKey",
		"bearerToken":  "binding.path.bearer",
		"clientSecret": "form-field-name-not-secret",
	}
	surfaceRaw, err := json.Marshal(surface)
	if err != nil {
		t.Fatal(err)
	}
	text := "Please complete the form."
	durable, err := a2ui.SerializeAssistantDurable(text, &a2ui.Payload{
		Version: a2ui.EnvelopeVersionV1, CatalogID: a2ui.CatalogID, Surface: surfaceRaw,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Shipped extract/serialize functions produce this durable shape.
	parts, err := chat.ParseMessageContentParts(durable)
	if err != nil {
		t.Fatalf("ParseMessageContentParts: %v", err)
	}
	messageID := uuid.NewString()
	item := protocolevent.MessageItem{
		ID: messageID, Type: protocolevent.ItemTypeMessage,
		Status: protocolevent.ItemStatusCompleted, Role: protocolevent.MessageRoleAssistant,
		Content: parts,
	}
	if err := protocolevent.ValidateProjectionItem(
		item, protocolevent.EventItemCompleted, protocolevent.MustDefaultPayloadValidator(),
	); err != nil {
		t.Fatalf("preflight ValidateProjectionItem: %v", err)
	}

	// MapCompleted path (CompleteProjected rehydrate) must accept the same durable body.
	msg := chat.Message{
		ID: messageID, WorkspaceID: uuid.NewString(), SessionID: uuid.NewString(),
		RunID: uuid.NewString(), Role: "ASSISTANT", Content: durable,
		ContentSHA256: contentSHA256Hex(durable), ContentLength: int64(len([]byte(durable))),
		Status: "EXECUTED", CreatedAt: time.Now().UTC(),
	}
	mapped, err := chat.NewProtocolMessageMapper(nil).MapCompleted(context.Background(), msg, "")
	if err != nil {
		t.Fatalf("MapCompleted: %v", err)
	}
	if len(mapped.Content) != 2 {
		t.Fatalf("parts=%d want 2", len(mapped.Content))
	}
	if err := protocolevent.ValidateProjectionItem(
		mapped, protocolevent.EventItemCompleted, protocolevent.MustDefaultPayloadValidator(),
	); err != nil {
		t.Fatalf("MapCompleted item failed shared preflight: %v", err)
	}
}

// catalogSurface is a minimal conformant surface for write-path tests.
const catalogSurface = `{"components":[{"id":"root","component":"Text","text":"Hello"}]}`

func TestGoldenA2UI_EmptyTextValidSurface(t *testing.T) {
	t.Parallel()
	full := a2ui.FenceStart + catalogSurface + a2ui.FenceEnd
	prepared := a2ui.PrepareAssistantContent(full, a2ui.PrepareOptions{
		EnableA2UI: true, ProjectionEnabled: true,
		SurfaceID: a2ui.SurfaceIDFor(uuid.NewString()),
	})
	if !prepared.AttachedA2UI || prepared.Result != a2ui.EmitOKEmptyText {
		t.Fatalf("prepared=%+v", prepared)
	}
	parts, err := chat.ParseMessageContentParts(prepared.Content)
	if err != nil || len(parts) != 2 {
		t.Fatalf("parts=%v err=%v", parts, err)
	}
	textPart, ok := parts[0].(protocolevent.TextContentPart)
	if !ok || textPart.Text != "" {
		t.Fatalf("text part=%+v", parts[0])
	}
	if _, ok := parts[1].(protocolevent.A2UIContentPart); !ok {
		t.Fatalf("a2ui part=%T", parts[1])
	}
	item := protocolevent.MessageItem{
		ID: uuid.NewString(), Type: protocolevent.ItemTypeMessage,
		Status: protocolevent.ItemStatusCompleted, Role: protocolevent.MessageRoleAssistant,
		Content: parts,
	}
	if err := protocolevent.ValidateProjectionItem(
		item, protocolevent.EventItemCompleted, protocolevent.MustDefaultPayloadValidator(),
	); err != nil {
		t.Fatalf("preflight: %v", err)
	}
}

func TestGoldenA2UI_InvalidEmptyFallbackKeepsRaw(t *testing.T) {
	t.Parallel()
	full := a2ui.FenceStart + `{not-json` + a2ui.FenceEnd
	prepared := a2ui.PrepareAssistantContent(full, a2ui.PrepareOptions{
		EnableA2UI: true, ProjectionEnabled: true,
	})
	if prepared.AttachedA2UI || prepared.Result != a2ui.EmitInvalidJSON {
		t.Fatalf("prepared=%+v", prepared)
	}
	if prepared.Content != full {
		t.Fatalf("fallback content=%q want raw full", prepared.Content)
	}
	if strings.TrimSpace(prepared.Content) == "" {
		t.Fatal("RecordAssistantResult requires non-empty Content")
	}
}

func TestGoldenA2UI_NextTurnHistoryOmitsSurface(t *testing.T) {
	t.Parallel()
	surface := `{"root":"form","password":{"label":"Secret"},"schemaVersion":"must-not-leak"}`
	durable, err := a2ui.SerializeAssistantDurable("Prior reply", &a2ui.Payload{
		Version: a2ui.EnvelopeVersionV1, Surface: json.RawMessage(surface),
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := chat.JoinTextPartsFromDurable(durable)
	if joined != "Prior reply" {
		t.Fatalf("joined=%q", joined)
	}
	if strings.Contains(joined, "password") || strings.Contains(joined, "schemaVersion") ||
		strings.Contains(joined, "surface") || strings.Contains(joined, "a2ui") {
		t.Fatalf("surface leaked into model history text: %q", joined)
	}

	emptyDurable, err := a2ui.SerializeAssistantDurable("", &a2ui.Payload{
		Surface: json.RawMessage(`{"root":"only"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := chat.JoinTextPartsFromDurable(emptyDurable); got != "" {
		t.Fatalf("empty join=%q", got)
	}
}

func TestCompleteRun_MaterializesA2UIEnvelope(t *testing.T) {
	// Sensitive-looking labels inside the surface must survive the write path
	// (KD-11): the surface subtree is exempt from sensitive-key scanning.
	surfaceBody := `{"components":[` +
		`{"id":"root","component":"Column","children":["f1","f2"]},` +
		`{"id":"f1","component":"TextField","label":"password","variant":"password"},` +
		`{"id":"f2","component":"TextField","label":"accessToken"}]}`
	modelOut := "Please fill:\n" + a2ui.FenceStart + surfaceBody + a2ui.FenceEnd

	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.run.ContextPolicySnapshot = mustA2UISnapshot(t, true)
		f.mdl.responses = []*schema.AgenticMessage{agenticmsg.AssistantText(modelOut)}
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := f.results.content
	if !strings.Contains(content, a2ui.MessageContentSchemaVersion) {
		t.Fatalf("want v1 envelope, got %s", content)
	}
	if strings.Contains(content, a2ui.FenceStart) {
		t.Fatalf("fence must not remain in durable content: %s", content)
	}
	parts, err := chat.ParseMessageContentParts(content)
	if err != nil || len(parts) != 2 {
		t.Fatalf("parts=%v err=%v content=%s", parts, err, content)
	}
	a2uiPart, ok := parts[1].(protocolevent.A2UIContentPart)
	if !ok || !strings.Contains(string(a2uiPart.Surface), "password") {
		t.Fatalf("a2ui part=%+v", parts[1])
	}
	item := protocolevent.MessageItem{
		ID: f.results.messageID, Type: protocolevent.ItemTypeMessage,
		Status: protocolevent.ItemStatusCompleted, Role: protocolevent.MessageRoleAssistant,
		Content: parts,
	}
	if err := protocolevent.ValidateProjectionItem(
		item, protocolevent.EventItemCompleted, protocolevent.MustDefaultPayloadValidator(),
	); err != nil {
		t.Fatalf("recorded durable preflight: %v", err)
	}
}

func TestCompleteRun_CapabilityOffNoExtractAttach(t *testing.T) {
	modelOut := "Just text, no UI."
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.mdl.responses = []*schema.AgenticMessage{agenticmsg.AssistantText(modelOut)}
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := f.results.content; got != modelOut {
		t.Fatalf("want plain %q, got %q", modelOut, got)
	}
}

func TestCompleteRun_ProjectionEnvOffStripsOnly(t *testing.T) {
	prev := os.Getenv(a2ui.EnvProjection)
	t.Setenv(a2ui.EnvProjection, "off")
	t.Cleanup(func() { _ = os.Setenv(a2ui.EnvProjection, prev) })

	modelOut := "Hi\n" + a2ui.FenceStart + catalogSurface + a2ui.FenceEnd
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.run.ContextPolicySnapshot = mustA2UISnapshot(t, true)
		f.mdl.responses = []*schema.AgenticMessage{agenticmsg.AssistantText(modelOut)}
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := f.results.content; got != "Hi" {
		t.Fatalf("want stripped plain Hi, got %q", got)
	}
	if strings.Contains(f.results.content, "schemaVersion") {
		t.Fatal("projection off must not persist v1 envelope")
	}
}

func mustA2UISnapshot(t *testing.T, enable bool) json.RawMessage {
	t.Helper()
	enableJSON := "false"
	if enable {
		enableJSON = "true"
	}
	_, raw, err := sessioncontext.Resolve(sessioncontext.ResolveInput{
		WorkspacePolicy: json.RawMessage(`{
			"schemaVersion":"session-context-policy.v1",
			"mode":"token_window",
			"maxInputTokens":100000,
			"outputReserveTokens":4096,
			"safetyMarginTokens":2048
		}`),
		AgentPolicy: json.RawMessage(`{
			"schemaVersion":"session-context-policy.v2",
			"aap":{"enableA2UI":` + enableJSON + `}
		}`),
		ContextWindowTokens:        128000,
		DefaultOutputReserveTokens: 4096,
		OutputTokenLimitMode:       "max_tokens",
		TokenizerProfile:           "o200k_base",
		TokenizerVersion:           "2026-01",
		GateEnabled:                true,
		RolloutVersion:             "agentic-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sessioncontext.EnableA2UIFromSnapshot(raw) != enable {
		t.Fatalf("EnableA2UIFromSnapshot mismatch")
	}
	return raw
}

func contentSHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
