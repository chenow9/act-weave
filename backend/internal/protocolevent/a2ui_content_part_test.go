package protocolevent_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/a2ui"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

// TestA2UIContentPartSensitiveSurfaceKeysPass validates KD-11: form-like keys
// inside an a2ui surface must not trip PayloadValidator on item.completed.
func TestA2UIContentPartSensitiveSurfaceKeysPass(t *testing.T) {
	t.Parallel()
	validator := protocolevent.MustDefaultPayloadValidator()

	surface := map[string]any{
		"components": []any{
			map[string]any{"id": "pw", "type": "TextField", "name": "password"},
			map[string]any{"id": "tok", "type": "TextField", "name": "accessToken"},
			map[string]any{"id": "key", "type": "TextField", "name": "apiKey"},
			map[string]any{"id": "bearer", "type": "TextField", "name": "bearerToken"},
		},
		// Nested sensitive *keys* (the golden case from the design).
		"password":     "label-for-password-field",
		"accessToken":  "binding.path.token",
		"apiKey":       "binding.path.apiKey",
		"bearerToken":  "binding.path.bearer",
		"clientSecret": "not-a-server-secret",
	}
	surfaceRaw, err := json.Marshal(surface)
	if err != nil {
		t.Fatal(err)
	}

	item := protocolevent.MessageItem{
		ID:     uuid.NewString(),
		Type:   protocolevent.ItemTypeMessage,
		Status: protocolevent.ItemStatusCompleted,
		Role:   protocolevent.MessageRoleAssistant,
		Content: []protocolevent.ContentPart{
			protocolevent.TextContentPart{
				Type: protocolevent.ContentPartTypeText,
				Text: "Please complete the form.",
			},
			protocolevent.A2UIContentPart{
				Type:      protocolevent.ContentPartTypeA2UI,
				Version:   a2ui.EnvelopeVersionV0,
				Surface:   surfaceRaw,
				CatalogID: "standard",
			},
		},
	}
	if err := protocolevent.ValidateItem(item); err != nil {
		t.Fatalf("ValidateItem: %v", err)
	}
	data, err := json.Marshal(protocolevent.ItemSnapshotData{Item: item})
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.ValidateEventData(protocolevent.EventItemCompleted, data); err != nil {
		t.Fatalf("ValidateEventData(item.completed) rejected a2ui surface with form keys: %v", err)
	}

	// Sensitive keys outside surface must still be rejected.
	leaky := json.RawMessage(`{
		"item":{
			"id":"` + uuid.NewString() + `",
			"type":"message","status":"completed","role":"assistant",
			"content":[{"type":"text","text":"ok"}],
			"password":"must-reject"
		}
	}`)
	if err := validator.ValidateEventData(protocolevent.EventItemCompleted, leaky); !errors.Is(err, protocolevent.ErrSensitivePayload) {
		t.Fatalf("sensitive key outside surface error=%v", err)
	}
}

func TestA2UIContentPartDecodeAndValidate(t *testing.T) {
	t.Parallel()

	t.Run("roundTrip", func(t *testing.T) {
		raw := json.RawMessage(`{
			"type":"a2ui",
			"version":"a2ui-surface.v0",
			"catalogId":"standard",
			"surface":{"root":"form","password":{"label":"Password"}}
		}`)
		part, err := protocolevent.DecodeContentPart(raw)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		a2uiPart, ok := part.(protocolevent.A2UIContentPart)
		if !ok || a2uiPart.ContentKind() != protocolevent.ContentPartTypeA2UI {
			t.Fatalf("decoded type=%T kind=%v", part, part.ContentKind())
		}
		if a2uiPart.Version != a2ui.EnvelopeVersionV0 || a2uiPart.CatalogID != "standard" {
			t.Fatalf("version/catalog=%q/%q", a2uiPart.Version, a2uiPart.CatalogID)
		}
		if err := protocolevent.ValidateItem(protocolevent.MessageItem{
			ID: uuid.NewString(), Type: protocolevent.ItemTypeMessage,
			Status: protocolevent.ItemStatusCompleted, Role: protocolevent.MessageRoleAssistant,
			Content: []protocolevent.ContentPart{
				protocolevent.TextContentPart{Type: protocolevent.ContentPartTypeText, Text: ""},
				a2uiPart,
			},
		}); err != nil {
			t.Fatalf("ValidateItem: %v", err)
		}
		encoded, err := json.Marshal(a2uiPart)
		if err != nil {
			t.Fatal(err)
		}
		// surface keys must survive round-trip
		if !strings.Contains(string(encoded), `"password"`) {
			t.Fatalf("marshal lost surface keys: %s", encoded)
		}
	})

	t.Run("nonObjectSurfaceRejected", func(t *testing.T) {
		cases := []string{
			`{"type":"a2ui","surface":[]}`,
			`{"type":"a2ui","surface":"not-object"}`,
			`{"type":"a2ui","surface":null}`,
			`{"type":"a2ui","surface":42}`,
			`{"type":"a2ui"}`,
		}
		for _, payload := range cases {
			_, err := protocolevent.DecodeContentPart(json.RawMessage(payload))
			if !errors.Is(err, protocolevent.ErrModelInvalid) {
				t.Fatalf("payload %s error=%v, want ErrModelInvalid", payload, err)
			}
		}
		// Constructed non-object surface via ValidateItem path.
		bad := protocolevent.A2UIContentPart{
			Type:    protocolevent.ContentPartTypeA2UI,
			Surface: json.RawMessage(`"string-surface"`),
		}
		if err := protocolevent.ValidateItem(protocolevent.MessageItem{
			ID: uuid.NewString(), Type: protocolevent.ItemTypeMessage,
			Status: protocolevent.ItemStatusCompleted, Role: protocolevent.MessageRoleAssistant,
			Content: []protocolevent.ContentPart{bad},
		}); !errors.Is(err, protocolevent.ErrModelInvalid) {
			t.Fatalf("ValidateItem non-object surface error=%v", err)
		}
	})

	t.Run("oversizedSurfaceRejected", func(t *testing.T) {
		// Build a surface object larger than MaxSurfaceBytes.
		// {"x":"<padding>"} overhead is small; padding fills the rest.
		overhead := len(`{"x":""}`)
		padding := strings.Repeat("a", a2ui.MaxSurfaceBytes-overhead+1)
		surface := json.RawMessage(`{"x":"` + padding + `"}`)
		if len(surface) <= a2ui.MaxSurfaceBytes {
			t.Fatalf("test surface not oversized: len=%d", len(surface))
		}
		raw, err := json.Marshal(map[string]any{
			"type":    "a2ui",
			"surface": json.RawMessage(surface),
		})
		if err != nil {
			// map with RawMessage may re-encode; build wire JSON manually.
			raw = json.RawMessage(`{"type":"a2ui","surface":` + string(surface) + `}`)
		}
		_, err = protocolevent.DecodeContentPart(raw)
		if !errors.Is(err, protocolevent.ErrModelInvalid) {
			t.Fatalf("oversized decode error=%v, want ErrModelInvalid", err)
		}
		part := protocolevent.A2UIContentPart{
			Type:    protocolevent.ContentPartTypeA2UI,
			Surface: surface,
		}
		if err := protocolevent.ValidateItem(protocolevent.MessageItem{
			ID: uuid.NewString(), Type: protocolevent.ItemTypeMessage,
			Status: protocolevent.ItemStatusCompleted, Role: protocolevent.MessageRoleAssistant,
			Content: []protocolevent.ContentPart{part},
		}); !errors.Is(err, protocolevent.ErrModelInvalid) {
			t.Fatalf("oversized ValidateItem error=%v", err)
		}
	})

	t.Run("maxSizeBoundaryAccepted", func(t *testing.T) {
		// Exactly MaxSurfaceBytes of a valid object must pass.
		// Minimal object: {} is 2 bytes; pad with a key.
		// Build {"k":"<pad>"} where total len == MaxSurfaceBytes.
		const prefix = `{"k":"`
		const suffix = `"}`
		padLen := a2ui.MaxSurfaceBytes - len(prefix) - len(suffix)
		if padLen < 0 {
			t.Fatal("MaxSurfaceBytes too small for test construction")
		}
		surface := json.RawMessage(prefix + strings.Repeat("b", padLen) + suffix)
		if len(surface) != a2ui.MaxSurfaceBytes {
			t.Fatalf("boundary surface len=%d want %d", len(surface), a2ui.MaxSurfaceBytes)
		}
		raw := json.RawMessage(`{"type":"a2ui","surface":` + string(surface) + `}`)
		part, err := protocolevent.DecodeContentPart(raw)
		if err != nil {
			t.Fatalf("boundary decode: %v", err)
		}
		if part.ContentKind() != protocolevent.ContentPartTypeA2UI {
			t.Fatalf("kind=%v", part.ContentKind())
		}
	})
}

func TestParseContentPartTypeA2UI(t *testing.T) {
	t.Parallel()
	if got := protocolevent.ParseContentPartType("a2ui"); got != protocolevent.ContentPartTypeA2UI {
		t.Fatalf("ParseContentPartType(a2ui)=%v", got)
	}
}
