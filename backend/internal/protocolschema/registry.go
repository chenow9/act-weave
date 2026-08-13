package protocolschema

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

const (
	SpecVersion     = "1.0"
	ProtocolVersion = "2026-08-11"
	schemaDirectory = "schemas/aap/v1"
)

//go:embed schemas/aap/v1/*.json
var schemaFiles embed.FS

var schemaNames = []string{
	"common.schema.json",
	"content-part.schema.json",
	"delta.schema.json",
	"error.schema.json",
	"event-envelope.schema.json",
	"events.schema.json",
	"interaction.schema.json",
	"item.schema.json",
	"run.schema.json",
	"transport-signal.schema.json",
}

// SchemaRef identifies a stable JSON Schema document and optional fragment.
type SchemaRef struct {
	DocumentID string
	Fragment   string
}

func (r SchemaRef) String() string { return r.DocumentID + r.Fragment }

var eventDataSchemas = map[string]SchemaRef{
	"run.accepted":          eventDefinition("runAcceptedData"),
	"run.started":           eventDefinition("runStartedData"),
	"run.waiting":           eventDefinition("runWaitingData"),
	"run.resumed":           eventDefinition("runResumedData"),
	"run.completed":         eventDefinition("runCompletedData"),
	"run.failed":            eventDefinition("runFailedData"),
	"run.cancelled":         eventDefinition("runCancelledData"),
	"item.started":          eventDefinition("itemStartedData"),
	"item.delta":            eventDefinition("itemDeltaData"),
	"item.completed":        eventDefinition("itemCompletedData"),
	"interaction.requested": eventDefinition("interactionRequestedData"),
	"interaction.resolved":  eventDefinition("interactionResolvedData"),
	"interaction.expired":   eventDefinition("interactionExpiredData"),
	"usage.updated":         eventDefinition("usageUpdatedData"),
}

var forbiddenPublicPropertyNames = []string{
	"authorization",
	"cookie",
	"password",
	"secret",
	"token",
	"resumetoken",
	"signedurl",
	"chainofthought",
}

func eventDefinition(name string) SchemaRef {
	return SchemaRef{
		DocumentID: "https://schemas.actweave.dev/aap/v1/events.schema.json",
		Fragment:   "#/$defs/" + name,
	}
}

// Document returns a defensive copy of an embedded schema document.
func Document(name string) ([]byte, error) {
	name = strings.TrimSpace(name)
	if !contains(schemaNames, name) {
		return nil, fs.ErrNotExist
	}
	value, err := schemaFiles.ReadFile(schemaDirectory + "/" + name)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(value), nil
}

func DocumentNames() []string { return append([]string(nil), schemaNames...) }

func EventTypes() []string {
	values := make([]string, 0, len(eventDataSchemas))
	for eventType := range eventDataSchemas {
		values = append(values, eventType)
	}
	sort.Strings(values)
	return values
}

func EventDataSchema(eventType string) (SchemaRef, bool) {
	value, exists := eventDataSchemas[strings.TrimSpace(eventType)]
	return value, exists
}

func ForbiddenPublicPropertyNames() []string {
	return append([]string(nil), forbiddenPublicPropertyNames...)
}

// ValidateRegistry verifies the embedded documents and every event-type
// mapping. Runtime payload validation and recursive sensitive-value scanning
// are intentionally implemented at the EventAppender boundary in M2.
func ValidateRegistry() error {
	if !sort.StringsAreSorted(schemaNames) {
		return errors.New("schema document names must be sorted")
	}
	documentsByID := make(map[string]map[string]any, len(schemaNames))
	for _, name := range schemaNames {
		raw, err := Document(name)
		if err != nil {
			return fmt.Errorf("read schema %s: %w", name, err)
		}
		var document map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&document); err != nil {
			return fmt.Errorf("decode schema %s: %w", name, err)
		}
		if document["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
			return fmt.Errorf("schema %s does not use draft 2020-12", name)
		}
		id, ok := document["$id"].(string)
		if !ok || id != "https://schemas.actweave.dev/aap/v1/"+name {
			return fmt.Errorf("schema %s has unstable $id", name)
		}
		if _, duplicate := documentsByID[id]; duplicate {
			return fmt.Errorf("duplicate schema $id %s", id)
		}
		documentsByID[id] = document
		if err := rejectDeclaredSensitiveProperties(name, document); err != nil {
			return err
		}
	}
	for eventType, ref := range eventDataSchemas {
		if !validEventType(eventType) {
			return fmt.Errorf("invalid event type %q", eventType)
		}
		document, exists := documentsByID[ref.DocumentID]
		if !exists {
			return fmt.Errorf("event %s references unknown document %s", eventType, ref.DocumentID)
		}
		definition := strings.TrimPrefix(ref.Fragment, "#/$defs/")
		definitions, ok := document["$defs"].(map[string]any)
		if !ok {
			return fmt.Errorf("event schema document has no $defs")
		}
		if _, exists := definitions[definition]; !exists {
			return fmt.Errorf("event %s references unknown definition %s", eventType, definition)
		}
	}
	return validateEnvelopePolicy(documentsByID)
}

func validateEnvelopePolicy(documents map[string]map[string]any) error {
	document := documents["https://schemas.actweave.dev/aap/v1/event-envelope.schema.json"]
	value, ok := document["x-actweave-forbidden-property-names"].([]any)
	if !ok || len(value) != len(forbiddenPublicPropertyNames) {
		return errors.New("event envelope forbidden-property policy is missing or incomplete")
	}
	for index, expected := range forbiddenPublicPropertyNames {
		actual, ok := value[index].(string)
		if !ok || actual != expected {
			return errors.New("event envelope forbidden-property policy is unstable")
		}
	}
	return nil
}

func rejectDeclaredSensitiveProperties(name string, value any) error {
	switch typed := value.(type) {
	case map[string]any:
		if properties, ok := typed["properties"].(map[string]any); ok {
			for property := range properties {
				if forbiddenPublicProperty(property) {
					return fmt.Errorf("schema %s declares forbidden public property %q", name, property)
				}
			}
		}
		for _, nested := range typed {
			if err := rejectDeclaredSensitiveProperties(name, nested); err != nil {
				return err
			}
		}
	case []any:
		for _, nested := range typed {
			if err := rejectDeclaredSensitiveProperties(name, nested); err != nil {
				return err
			}
		}
	}
	return nil
}

func forbiddenPublicProperty(value string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(value))
	for _, forbidden := range forbiddenPublicPropertyNames {
		if normalized == forbidden {
			return true
		}
	}
	return false
}

func validEventType(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, character := range part {
			if (character < 'a' || character > 'z') && character != '_' {
				return false
			}
		}
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
