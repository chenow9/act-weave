package smartdag

import (
	"context"
	"reflect"
	"testing"
)

func TestDefaultSystemPromptActiveAndStableHash(t *testing.T) {
	t.Parallel()
	store := NewMemorySystemPromptStore()
	active, err := store.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != DefaultSystemPromptID || active.Version != DefaultSystemPromptVersion {
		t.Fatalf("unexpected active prompt identity: %+v", active)
	}
	if active.Content == "" {
		t.Fatal("active prompt content must be non-empty")
	}
	wantHash := PromptHash(active.Content)
	if active.Hash != wantHash {
		t.Fatalf("hash mismatch: got %s want %s", active.Hash, wantHash)
	}
	// Stability: same content → same hash across calls.
	if PromptHash(active.Content) != wantHash {
		t.Fatal("prompt hash is not stable for fixed content")
	}
	// Re-read still active.
	again, err := store.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again.Hash != active.Hash || again.ID != active.ID {
		t.Fatalf("active prompt changed unexpectedly: %+v vs %+v", again, active)
	}
}

func TestAuditMetaFromPrompt(t *testing.T) {
	t.Parallel()
	prompt := DefaultSystemPrompt()
	meta := AuditMetaFromPrompt(prompt)
	if meta.PromptID != prompt.ID || meta.PromptHash != prompt.Hash {
		t.Fatalf("audit meta mismatch: %+v vs %+v", meta, prompt)
	}
	if len(meta.PromptHash) != 64 {
		t.Fatalf("promptHash must be sha256 hex (64 chars), got %d", len(meta.PromptHash))
	}
}

func TestGenerateRequestTypesDoNotAcceptUserSystemPrompt(t *testing.T) {
	t.Parallel()
	// Surface check: v2 request and GraphModelInput must not expose a user-editable systemPrompt field.
	for _, typ := range []reflect.Type{
		reflect.TypeOf(GenerateRequestV2{}),
		reflect.TypeOf(ApplyTurnRequest{}),
		reflect.TypeOf(GenerateRequest{}), // legacy rules path also has no system prompt override
	} {
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Field(i).Name
			switch name {
			case "SystemPrompt", "SystemPromptID", "UserSystemPrompt", "systemPrompt":
				t.Fatalf("%s must not accept user system prompt field %s", typ.Name(), name)
			}
		}
		if err := RejectsUserSystemPromptOverride(false); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPromptHashChangesWithContent(t *testing.T) {
	t.Parallel()
	a := PromptHash("alpha")
	b := PromptHash("beta")
	if a == b {
		t.Fatal("different content must produce different hashes")
	}
}
