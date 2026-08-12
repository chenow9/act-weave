package protocolschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrExternalSchemaInvalid reports a malformed external schema set. It is
// distinct from ErrSchemaViolation, which reports an invalid *value*.
var ErrExternalSchemaInvalid = errors.New("EXTERNAL_SCHEMA_INVALID")

// ExternalSchemaSet validates values against schema documents that live outside
// the stable AAP registry, reusing the same draft-2020-12 subset evaluator.
//
// Callers must compile once and reuse: CompileExternalSchemaSet decodes every
// document, which is far too costly to repeat per request.
//
// Only the keyword subset implemented by validateSchemaValue is evaluated.
// Anything outside it is ignored rather than rejected, so schema authors must
// assert their keyword usage separately.
type ExternalSchemaSet struct {
	registry validationRegistry
	rootID   string
}

// CompileExternalSchemaSet decodes documents into a self-contained registry.
// The first document is the root; later documents are only reachable through
// $ref. Every document must declare a unique $id, and relative $refs resolve
// against the referring document's $id path, so cross-referencing documents
// must share a directory prefix.
func CompileExternalSchemaSet(documents ...[]byte) (*ExternalSchemaSet, error) {
	if len(documents) == 0 {
		return nil, fmt.Errorf("%w: no documents", ErrExternalSchemaInvalid)
	}
	set := &ExternalSchemaSet{
		registry: validationRegistry{documents: make(map[string]map[string]any, len(documents))},
	}
	for index, raw := range documents {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var document map[string]any
		if err := decoder.Decode(&document); err != nil {
			return nil, fmt.Errorf("%w: decode document %d: %v", ErrExternalSchemaInvalid, index, err)
		}
		id, ok := document["$id"].(string)
		if !ok || id == "" {
			return nil, fmt.Errorf("%w: document %d has no $id", ErrExternalSchemaInvalid, index)
		}
		if _, duplicate := set.registry.documents[id]; duplicate {
			return nil, fmt.Errorf("%w: duplicate $id %s", ErrExternalSchemaInvalid, id)
		}
		set.registry.documents[id] = document
		if index == 0 {
			set.rootID = id
		}
	}
	return set, nil
}

// Validate checks raw against the root document, or against a local fragment of
// it such as "#/$defs/anyComponent" when fragment is non-empty.
func (set *ExternalSchemaSet) Validate(fragment string, raw json.RawMessage) error {
	if set == nil || set.registry.documents == nil {
		return ErrExternalSchemaInvalid
	}
	value, err := decodeValidationJSON(raw)
	if err != nil {
		return ErrSchemaViolation
	}
	return set.ValidateValue(fragment, value)
}

// ValidateValue is Validate for an already-decoded value. The value must have
// been decoded with json.Decoder.UseNumber, since numeric keywords compare
// against json.Number.
func (set *ExternalSchemaSet) ValidateValue(fragment string, value any) error {
	if set == nil {
		return ErrExternalSchemaInvalid
	}
	return set.ValidateValueIn(set.rootID, fragment, value)
}

// ValidateValueIn validates against a fragment of a specific document in the
// set. The fragment is a JSON Pointer resolved segment by segment, so any node
// is addressable — including a single member schema such as
// "#/components/Chart/properties/chartType".
func (set *ExternalSchemaSet) ValidateValueIn(documentID, fragment string, value any) error {
	if set == nil || set.registry.documents == nil {
		return ErrExternalSchemaInvalid
	}
	schema, resolvedDocument, ok := resolveExternalNode(set, documentID, fragment)
	if !ok {
		return ErrExternalSchemaInvalid
	}
	if validateSchemaValue(set.registry, resolvedDocument, schema, value) != nil {
		return ErrSchemaViolation
	}
	return nil
}

// DecodeValue decodes JSON the way the evaluator expects (numbers as
// json.Number), so callers can validate once and then walk the same value.
func DecodeValue(raw json.RawMessage) (any, error) {
	return decodeValidationJSON(raw)
}

func resolveExternalNode(
	set *ExternalSchemaSet,
	documentID string,
	fragment string,
) (map[string]any, string, bool) {
	if documentID == "" {
		documentID = set.rootID
	}
	if fragment == "" {
		document, exists := set.registry.documents[documentID]
		return document, documentID, exists
	}
	return resolveValidationRef(set.registry, documentID, fragment)
}
