package protocolschema

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"actweave/backend/internal/protocolschema/generated"
)

func TestAAPV1Schemas(t *testing.T) {
	if err := ValidateRegistry(); err != nil {
		t.Fatalf("validate AAP v1 schema registry: %v", err)
	}
	expectedTypes := []string{
		"interaction.expired", "interaction.requested", "interaction.resolved",
		"item.completed", "item.delta", "item.started",
		"run.accepted", "run.cancelled", "run.completed", "run.failed",
		"run.resumed", "run.started", "run.waiting", "usage.updated",
	}
	if actual := EventTypes(); !reflect.DeepEqual(actual, expectedTypes) {
		t.Fatalf("unexpected event types: %#v", actual)
	}
	for _, eventType := range expectedTypes {
		ref, exists := EventDataSchema(eventType)
		if !exists || ref.DocumentID == "" || !strings.HasPrefix(ref.Fragment, "#/$defs/") {
			t.Fatalf("event %s has no stable schema ref: %+v", eventType, ref)
		}
	}
	if _, exists := EventDataSchema("future.event"); exists {
		t.Fatal("unknown future event unexpectedly claimed by v1 registry")
	}
}

func TestAAPV1DocumentsAreEmbeddedAndDefensive(t *testing.T) {
	for _, name := range DocumentNames() {
		first, err := Document(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var value map[string]any
		if err := json.Unmarshal(first, &value); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		first[0] = 'x'
		second, err := Document(name)
		if err != nil || len(second) == 0 || second[0] != '{' {
			t.Fatalf("document %s was not returned defensively: %v", name, err)
		}
	}
	if _, err := Document("missing.schema.json"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist, got %v", err)
	}
}

func TestAAPV1SchemaPolicyRejectsSensitiveDeclaredProperties(t *testing.T) {
	for _, property := range []string{
		"authorization", "Authorization", "resume_token", "signed-url", "password", "secret",
	} {
		if !forbiddenPublicProperty(property) {
			t.Fatalf("expected %q to be forbidden", property)
		}
	}
	for _, property := range []string{"eventId", "accessPolicy", "tokenCount", "secretiveNotice"} {
		if forbiddenPublicProperty(property) {
			t.Fatalf("expected %q to remain allowed", property)
		}
	}
	bad := map[string]any{"properties": map[string]any{"resumeToken": map[string]any{"type": "string"}}}
	if err := rejectDeclaredSensitiveProperties("bad.schema.json", bad); err == nil {
		t.Fatal("expected forbidden property declaration to fail")
	}
}

func TestAAPV1SchemaChecksums(t *testing.T) {
	// Live embedded documents must match the generated Schema Registry checksums
	// produced by `make generate` / `go run ./cmd/protocolgen`.
	setHash := sha256.New()
	for _, name := range DocumentNames() {
		raw, err := Document(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		documentHash := sha256.Sum256(raw)
		actual := fmt.Sprintf("%x", documentHash)
		expected, ok := generated.DocumentSHA256[name]
		if !ok {
			t.Fatalf("generated checksum missing for %s (run make generate)", name)
		}
		if actual != expected {
			t.Fatalf("schema checksum drift for %s: live=%s generated=%s (run make generate)", name, actual, expected)
		}
		_, _ = setHash.Write(raw)
	}
	if actual := fmt.Sprintf("%x", setHash.Sum(nil)); actual != generated.SchemaSetSHA256 {
		t.Fatalf("schema-set checksum drift: live=%s generated=%s (run make generate)", actual, generated.SchemaSetSHA256)
	}
	// Catalog drift guard: generated event types must match the registry map.
	if len(generated.EventTypes) != len(EventTypes()) {
		t.Fatalf("event type count drift: generated=%d registry=%d", len(generated.EventTypes), len(EventTypes()))
	}
	for _, eventType := range generated.EventTypes {
		if _, ok := EventDataSchema(eventType); !ok {
			t.Fatalf("generated event type %q missing from registry map", eventType)
		}
	}
}
