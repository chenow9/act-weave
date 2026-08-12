package a2ui

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"actweave/backend/internal/protocolschema"
)

type compiledCatalog struct {
	schemas      *protocolschema.ExternalSchemaSet
	definitions  map[string]any
	components   map[string]any
	instructions string
	err          error
}

// catalog compilation is expensive and every assistant message needs it, so the
// decoded documents are shared for the process lifetime.
var loadCatalog = sync.OnceValue(func() compiledCatalog {
	surfaceDocument, err := SurfaceSchemaDocument()
	if err != nil {
		return compiledCatalog{err: err}
	}
	catalogDocument, err := CatalogDocument()
	if err != nil {
		return compiledCatalog{err: err}
	}
	schemas, err := protocolschema.CompileExternalSchemaSet(surfaceDocument, catalogDocument)
	if err != nil {
		return compiledCatalog{err: err}
	}
	decoded, err := protocolschema.DecodeValue(catalogDocument)
	if err != nil {
		return compiledCatalog{err: err}
	}
	document, _ := decoded.(map[string]any)
	definitions, _ := document["$defs"].(map[string]any)
	components, _ := document["components"].(map[string]any)
	instructions, _ := document["instructions"].(string)
	return compiledCatalog{
		schemas:      schemas,
		definitions:  definitions,
		components:   components,
		instructions: instructions,
	}
})

func (catalog compiledCatalog) resolve(ref string) (map[string]any, bool) {
	switch {
	case strings.HasPrefix(ref, "#/$defs/"):
		resolved, ok := catalog.definitions[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
		return resolved, ok
	case strings.HasPrefix(ref, "#/components/"):
		resolved, ok := catalog.components[strings.TrimPrefix(ref, "#/components/")].(map[string]any)
		return resolved, ok
	default:
		return nil, false
	}
}

func (catalog compiledCatalog) componentNames() []string {
	names := make([]string, 0, len(catalog.components))
	for name := range catalog.components {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ValidateSurface checks a surface against the platform catalog in three layers:
// catalog JSON Schema, component graph structure, then chart semantics that
// JSON Schema cannot express. A nil return means the surface may be persisted.
//
// Callers degrade to text-only on any diagnostic; the run still succeeds.
func ValidateSurface(catalogID string, surface json.RawMessage) *Diagnostic {
	catalog := loadCatalog()
	if catalog.err != nil {
		return newDiagnostic(ReasonSchema, "", "catalog_unavailable", "", "")
	}
	if catalogID != "" && !CatalogRegistered(catalogID) {
		return newDiagnostic(ReasonUnknownCatalog, "/catalogId", "const", "", CatalogID)
	}

	decoded, err := protocolschema.DecodeValue(surface)
	if err != nil {
		return newDiagnostic(ReasonSchema, "", "type", "", "object")
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		return newDiagnostic(ReasonSchema, "", "type", "", "object")
	}

	if err := catalog.schemas.ValidateValue("", decoded); err != nil {
		return catalog.localizeSchemaFailure(root)
	}

	components, _ := root["components"].([]any)
	if diagnostic := validateComponentGraph(components); diagnostic != nil {
		return diagnostic
	}
	return validateChartSemantics(components, root["dataModel"])
}

// localizeSchemaFailure narrows a boolean schema rejection to a pointer,
// keyword and expectation. The shared evaluator reports only pass/fail, so the
// surface is re-validated piecewise; this runs on the rejection path only.
func (catalog compiledCatalog) localizeSchemaFailure(root map[string]any) *Diagnostic {
	surfaceProperties := map[string]struct{}{
		"surfaceId": {}, "catalogId": {}, "components": {}, "dataModel": {},
	}
	for name := range root {
		if _, allowed := surfaceProperties[name]; !allowed {
			return newDiagnostic(ReasonSchema, pointerAppend("", name), "additionalProperties", "",
				"one of surfaceId|catalogId|components|dataModel")
		}
	}
	if declared, exists := root["catalogId"]; exists {
		if text, ok := declared.(string); !ok || text != CatalogID {
			return newDiagnostic(ReasonSchema, "/catalogId", "const", "", CatalogID)
		}
	}
	rawComponents, exists := root["components"]
	if !exists {
		return newDiagnostic(ReasonSchema, "", "required", "", "components")
	}
	components, ok := rawComponents.([]any)
	if !ok {
		return newDiagnostic(ReasonSchema, "/components", "type", "", "array of components")
	}
	if len(components) == 0 {
		return newDiagnostic(ReasonSchema, "/components", "minItems", "", "at least 1 component")
	}
	if _, isObject := root["dataModel"]; isObject {
		if _, ok := root["dataModel"].(map[string]any); !ok {
			return newDiagnostic(ReasonSchema, "/dataModel", "type", "", "object")
		}
	}
	for index, raw := range components {
		pointer := pointerAppend("/components", index)
		if diagnostic := catalog.localizeComponent(pointer, raw); diagnostic != nil {
			return diagnostic
		}
	}
	return newDiagnostic(ReasonSchema, "/components", "maxItems", "",
		"at most 64 components")
}

func (catalog compiledCatalog) localizeComponent(pointer string, raw any) *Diagnostic {
	component, ok := raw.(map[string]any)
	if !ok {
		return newDiagnostic(ReasonSchema, pointer, "type", "", "object")
	}
	name, _ := component["component"].(string)
	schema, known := catalog.components[name].(map[string]any)
	if !known {
		return newDiagnostic(ReasonSchema, pointerAppend(pointer, "component"), "const", "",
			"one of "+strings.Join(catalog.componentNames(), "|"))
	}
	if catalog.validateNode("#/components/"+name, component) {
		return nil
	}

	properties, _ := schema["properties"].(map[string]any)
	for _, field := range requiredFields(schema) {
		if _, exists := component[field]; !exists {
			return newDiagnostic(ReasonSchema, pointer, "required", name, field)
		}
	}
	for field := range component {
		if _, allowed := properties[field]; !allowed {
			return newDiagnostic(ReasonSchema, pointerAppend(pointer, field), "additionalProperties", name,
				"one of "+strings.Join(sortedKeys(properties), "|"))
		}
	}
	for _, field := range sortedKeys(properties) {
		value, exists := component[field]
		if !exists {
			continue
		}
		if catalog.validateNode("#/components/"+name+"/properties/"+field, value) {
			continue
		}
		propertySchema := properties[field]
		return newDiagnostic(ReasonSchema, pointerAppend(pointer, field),
			assertionKeyword(propertySchema), name,
			describeSchema(propertySchema, catalog.resolve, 0))
	}
	return newDiagnostic(ReasonSchema, pointer, "schema", name, "")
}

// validateNode validates a value against any addressable node of the catalog
// document, down to a single member schema.
func (catalog compiledCatalog) validateNode(fragment string, value any) bool {
	return catalog.schemas.ValidateValueIn(CatalogID, fragment, value) == nil
}

// assertionKeyword names the constraint a member most likely violated, for the
// a2ui_catalog_invalid keyword label.
func assertionKeyword(propertySchema any) string {
	schema, ok := propertySchema.(map[string]any)
	if !ok {
		return "schema"
	}
	for _, keyword := range []string{
		"$ref", "const", "enum", "oneOf", "pattern", "maxLength", "minLength",
		"maximum", "minimum", "maxItems", "minItems", "items", "type",
	} {
		if _, exists := schema[keyword]; exists {
			return keyword
		}
	}
	return "schema"
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
