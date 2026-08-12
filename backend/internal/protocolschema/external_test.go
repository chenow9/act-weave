package protocolschema

import (
	"encoding/json"
	"errors"
	"testing"
)

const externalRootDoc = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.test/v1/root.schema.json",
  "type": "object",
  "properties": {
    "kind": {"const": "widget"},
    "size": {"type": "string", "enum": ["sm", "lg"]},
    "count": {"type": "number", "minimum": 1, "maximum": 8},
    "parts": {"type": "array", "minItems": 1, "items": {"$ref": "parts.schema.json#/$defs/part"}}
  },
  "required": ["kind", "parts"],
  "additionalProperties": false
}`

const externalPartsDoc = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.test/v1/parts.schema.json",
  "$defs": {
    "part": {
      "type": "object",
      "properties": {
        "id": {"type": "string", "pattern": "^[a-z]+$"},
        "value": {"type": "number"}
      },
      "required": ["id"],
      "additionalProperties": false
    }
  }
}`

func compileTestSet(t *testing.T) *ExternalSchemaSet {
	t.Helper()
	set, err := CompileExternalSchemaSet([]byte(externalRootDoc), []byte(externalPartsDoc))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return set
}

func TestExternalSchemaSetAcceptsValidValue(t *testing.T) {
	set := compileTestSet(t)
	raw := json.RawMessage(`{"kind":"widget","size":"lg","count":3,"parts":[{"id":"abc","value":1.5}]}`)
	if err := set.Validate("", raw); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
}

func TestExternalSchemaSetRejectsViolations(t *testing.T) {
	set := compileTestSet(t)
	cases := map[string]string{
		"const mismatch":       `{"kind":"gadget","parts":[{"id":"abc"}]}`,
		"enum mismatch":        `{"kind":"widget","size":"xl","parts":[{"id":"abc"}]}`,
		"maximum exceeded":     `{"kind":"widget","count":9,"parts":[{"id":"abc"}]}`,
		"minimum violated":     `{"kind":"widget","count":0,"parts":[{"id":"abc"}]}`,
		"missing required":     `{"parts":[{"id":"abc"}]}`,
		"unknown property":     `{"kind":"widget","parts":[{"id":"abc"}],"extra":1}`,
		"empty array":          `{"kind":"widget","parts":[]}`,
		"cross-document ref":   `{"kind":"widget","parts":[{"id":"ABC"}]}`,
		"nested unknown":       `{"kind":"widget","parts":[{"id":"abc","nope":1}]}`,
		"nested missing":       `{"kind":"widget","parts":[{"value":1}]}`,
		"wrong type for parts": `{"kind":"widget","parts":{}}`,
		"not an object":        `["widget"]`,
		"trailing garbage":     `{"kind":"widget","parts":[{"id":"abc"}]} {}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if err := set.Validate("", json.RawMessage(payload)); !errors.Is(err, ErrSchemaViolation) {
				t.Fatalf("Validate = %v, want ErrSchemaViolation", err)
			}
		})
	}
}

func TestExternalSchemaSetValidatesFragment(t *testing.T) {
	set, err := CompileExternalSchemaSet([]byte(externalPartsDoc))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if err := set.Validate("#/$defs/part", json.RawMessage(`{"id":"abc"}`)); err != nil {
		t.Fatalf("Validate fragment = %v, want nil", err)
	}
	if err := set.Validate("#/$defs/part", json.RawMessage(`{"id":"ABC"}`)); !errors.Is(err, ErrSchemaViolation) {
		t.Fatalf("Validate fragment = %v, want ErrSchemaViolation", err)
	}
	if err := set.Validate("#/$defs/missing", json.RawMessage(`{}`)); !errors.Is(err, ErrExternalSchemaInvalid) {
		t.Fatalf("unknown fragment = %v, want ErrExternalSchemaInvalid", err)
	}
}

func TestExternalSchemaSetCompileErrors(t *testing.T) {
	if _, err := CompileExternalSchemaSet(); !errors.Is(err, ErrExternalSchemaInvalid) {
		t.Fatalf("empty set = %v", err)
	}
	if _, err := CompileExternalSchemaSet([]byte(`{"$id":"x"`)); !errors.Is(err, ErrExternalSchemaInvalid) {
		t.Fatalf("malformed json = %v", err)
	}
	if _, err := CompileExternalSchemaSet([]byte(`{"type":"object"}`)); !errors.Is(err, ErrExternalSchemaInvalid) {
		t.Fatalf("missing $id = %v", err)
	}
	duplicate := []byte(`{"$id":"https://example.test/v1/dup.json"}`)
	if _, err := CompileExternalSchemaSet(duplicate, duplicate); !errors.Is(err, ErrExternalSchemaInvalid) {
		t.Fatalf("duplicate $id = %v", err)
	}
}

func TestExternalSchemaSetNilReceiver(t *testing.T) {
	var set *ExternalSchemaSet
	if err := set.Validate("", json.RawMessage(`{}`)); !errors.Is(err, ErrExternalSchemaInvalid) {
		t.Fatalf("nil receiver = %v", err)
	}
}

// ValidateValue must see numbers as json.Number, which is what DecodeValue
// guarantees; decoding with plain json.Unmarshal would silently skip numeric
// keywords because the evaluator only matches json.Number.
func TestExternalSchemaSetValidateValueUsesDecodeValue(t *testing.T) {
	set := compileTestSet(t)
	value, err := DecodeValue(json.RawMessage(`{"kind":"widget","count":9,"parts":[{"id":"abc"}]}`))
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if err := set.ValidateValue("", value); !errors.Is(err, ErrSchemaViolation) {
		t.Fatalf("ValidateValue = %v, want ErrSchemaViolation", err)
	}
}
