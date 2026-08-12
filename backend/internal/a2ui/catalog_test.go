package a2ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
)

// supportedAssertionKeywords mirrors what protocolschema/validate.go evaluates.
// A keyword outside this set is silently ignored at runtime, which would turn an
// intended constraint into no validation at all.
var supportedAssertionKeywords = map[string]struct{}{
	"$ref": {}, "allOf": {}, "anyOf": {}, "oneOf": {}, "not": {},
	"const": {}, "enum": {}, "type": {},
	"required": {}, "properties": {}, "additionalProperties": {},
	"items": {}, "minItems": {}, "maxItems": {}, "uniqueItems": {},
	"minLength": {}, "maxLength": {}, "pattern": {}, "format": {},
	"minimum": {}, "maximum": {},
}

// ignoredAnnotationKeywords carry no assertion and are safe to appear anywhere.
var ignoredAnnotationKeywords = map[string]struct{}{
	"$schema": {}, "$id": {}, "$defs": {}, "title": {}, "description": {},
	"default": {}, "examples": {}, "deprecated": {},
}

var allowedCatalogRootKeys = map[string]struct{}{
	"$schema": {}, "$id": {}, "$defs": {}, "title": {}, "description": {},
	"catalogId": {}, "protocolVersion": {}, "instructions": {}, "components": {},
}

var expectedComponents = []string{
	"Button", "Card", "Chart", "CheckBox", "ChoicePicker", "Column",
	"DateTimeInput", "Divider", "Row", "Text", "TextField",
}

func decodeCatalog(t *testing.T) map[string]any {
	t.Helper()
	raw, err := CatalogDocument()
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	return decodeSchemaDocument(t, raw)
}

func decodeSchemaDocument(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	return document
}

func catalogComponents(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	components, ok := document["components"].(map[string]any)
	if !ok {
		t.Fatal("catalog has no components map")
	}
	return components
}

func TestCatalogIDMatchesSchemaID(t *testing.T) {
	document := decodeCatalog(t)
	if got := document["$id"]; got != CatalogID {
		t.Fatalf("catalog $id = %v, want %s", got, CatalogID)
	}
	if got := document["catalogId"]; got != CatalogID {
		t.Fatalf("catalogId = %v, want %s", got, CatalogID)
	}
	if got := document["$schema"]; got != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("catalog does not declare draft 2020-12: %v", got)
	}
	if got := document["protocolVersion"]; got != "1.0" {
		t.Fatalf("protocolVersion = %v, want 1.0", got)
	}
	if instructions, ok := document["instructions"].(string); !ok || instructions == "" {
		t.Fatal("catalog must carry instructions for the prompt generator")
	}
	for key := range document {
		if _, allowed := allowedCatalogRootKeys[key]; !allowed {
			t.Fatalf("catalog declares unexpected root key %q", key)
		}
	}
}

func TestCatalogUsesSupportedKeywords(t *testing.T) {
	document := decodeCatalog(t)
	definitions, _ := document["$defs"].(map[string]any)
	for name, definition := range definitions {
		walkSchemaKeywords(t, "$defs/"+name, definition)
	}
	for name, component := range catalogComponents(t, document) {
		walkSchemaKeywords(t, "components/"+name, component)
	}

	raw, err := SurfaceSchemaDocument()
	if err != nil {
		t.Fatalf("read surface schema: %v", err)
	}
	walkSchemaKeywords(t, "surface", decodeSchemaDocument(t, raw))
}

// walkSchemaKeywords fails on any keyword the runtime validator does not
// evaluate. It is schema-aware: keys of properties/$defs maps are member names,
// not keywords, and enum/const payloads are data rather than schemas.
func walkSchemaKeywords(t *testing.T, path string, node any) {
	t.Helper()
	schema, ok := node.(map[string]any)
	if !ok {
		return
	}
	for keyword, value := range schema {
		_, supported := supportedAssertionKeywords[keyword]
		_, ignored := ignoredAnnotationKeywords[keyword]
		if !supported && !ignored {
			t.Fatalf("%s uses unsupported schema keyword %q", path, keyword)
		}
		switch keyword {
		case "properties", "$defs":
			members, _ := value.(map[string]any)
			for name, member := range members {
				walkSchemaKeywords(t, path+"/"+keyword+"/"+name, member)
			}
		case "items", "not":
			walkSchemaKeywords(t, path+"/"+keyword, value)
		case "allOf", "anyOf", "oneOf":
			branches, _ := value.([]any)
			for index, branch := range branches {
				walkSchemaKeywords(t, fmt.Sprintf("%s/%s/%d", path, keyword, index), branch)
			}
		}
	}
}

// TestCatalogComponentsAreFlat guards a non-obvious runtime constraint: the
// validator resolves additionalProperties against the properties of the same
// schema object only, and does not support unevaluatedProperties. Composing a
// component with allOf would therefore silently stop rejecting unknown fields.
func TestCatalogComponentsAreFlat(t *testing.T) {
	for name, component := range catalogComponents(t, decodeCatalog(t)) {
		schema, ok := component.(map[string]any)
		if !ok {
			t.Fatalf("component %s is not an object", name)
		}
		for _, forbidden := range []string{"allOf", "anyOf", "oneOf", "$ref"} {
			if _, exists := schema[forbidden]; exists {
				t.Fatalf("component %s composes with %q; additionalProperties would not be enforced", name, forbidden)
			}
		}
		if schema["type"] != "object" {
			t.Fatalf("component %s must declare type object", name)
		}
		if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
			t.Fatalf("component %s must set additionalProperties:false", name)
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("component %s has no properties", name)
		}
		for _, required := range []string{"id", "component"} {
			if _, exists := properties[required]; !exists {
				t.Fatalf("component %s must declare property %q", name, required)
			}
		}
		assertRequired(t, name, schema, "id", "component")
	}
}

func assertRequired(t *testing.T, name string, schema map[string]any, expected ...string) {
	t.Helper()
	values, _ := schema["required"].([]any)
	present := make(map[string]struct{}, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			present[text] = struct{}{}
		}
	}
	for _, field := range expected {
		if _, exists := present[field]; !exists {
			t.Fatalf("component %s must require %q", name, field)
		}
	}
}

func TestCatalogDiscriminatorRule(t *testing.T) {
	for name, component := range catalogComponents(t, decodeCatalog(t)) {
		schema, _ := component.(map[string]any)
		properties, _ := schema["properties"].(map[string]any)
		discriminator, ok := properties["component"].(map[string]any)
		if !ok {
			t.Fatalf("component %s has no component discriminator", name)
		}
		if got := discriminator["const"]; got != name {
			t.Fatalf("component %s declares component const %v", name, got)
		}
	}
}

func TestCatalogComponentSetIsStable(t *testing.T) {
	components := catalogComponents(t, decodeCatalog(t))
	names := make([]string, 0, len(components))
	for name := range components {
		names = append(names, name)
	}
	sort.Strings(names)
	if fmt.Sprint(names) != fmt.Sprint(expectedComponents) {
		t.Fatalf("component set changed: got %v, want %v", names, expectedComponents)
	}
}

// TestAnyComponentCoversEveryComponent keeps the dispatch union in sync with the
// components map; a component missing from anyComponent can never be used.
func TestAnyComponentCoversEveryComponent(t *testing.T) {
	document := decodeCatalog(t)
	definitions, _ := document["$defs"].(map[string]any)
	anyComponent, ok := definitions["anyComponent"].(map[string]any)
	if !ok {
		t.Fatal("catalog has no $defs/anyComponent")
	}
	branches, _ := anyComponent["oneOf"].([]any)
	referenced := make(map[string]struct{}, len(branches))
	for _, branch := range branches {
		object, _ := branch.(map[string]any)
		ref, _ := object["$ref"].(string)
		const prefix = "#/components/"
		if len(ref) <= len(prefix) || ref[:len(prefix)] != prefix {
			t.Fatalf("anyComponent branch has unexpected $ref %q", ref)
		}
		referenced[ref[len(prefix):]] = struct{}{}
	}
	components := catalogComponents(t, document)
	if len(referenced) != len(components) {
		t.Fatalf("anyComponent covers %d components, catalog defines %d", len(referenced), len(components))
	}
	for name := range components {
		if _, exists := referenced[name]; !exists {
			t.Fatalf("anyComponent is missing component %s", name)
		}
	}
}

func TestChartExcludesVisualProperties(t *testing.T) {
	components := catalogComponents(t, decodeCatalog(t))
	chart, _ := components["Chart"].(map[string]any)
	properties, _ := chart["properties"].(map[string]any)
	forbidden := []string{
		"color", "colors", "palette", "width", "height", "size",
		"legend", "legendPosition", "xAxis", "yAxis", "gridLines", "ticks",
		"font", "fontSize", "className", "style",
		"chartKind", "chartStyle", "type", "kind",
		"data", "chartData", "labels", "values", "datasets",
	}
	for _, name := range forbidden {
		if _, exists := properties[name]; exists {
			t.Fatalf("Chart must not expose %q: visual and alias properties belong to the renderer", name)
		}
	}
}

// TestButtonHasNoAction encodes the advertised actions:false capability in the
// schema itself, so a surface cannot structurally request a callback.
func TestButtonHasNoAction(t *testing.T) {
	components := catalogComponents(t, decodeCatalog(t))
	button, _ := components["Button"].(map[string]any)
	properties, _ := button["properties"].(map[string]any)
	for _, name := range []string{"action", "onClick", "event"} {
		if _, exists := properties[name]; exists {
			t.Fatalf("Button must not expose %q while actions are disabled", name)
		}
	}
}

func TestSurfaceSchemaShape(t *testing.T) {
	raw, err := SurfaceSchemaDocument()
	if err != nil {
		t.Fatalf("read surface schema: %v", err)
	}
	document := decodeSchemaDocument(t, raw)
	if got := document["$id"]; got != SurfaceSchemaID {
		t.Fatalf("surface $id = %v, want %s", got, SurfaceSchemaID)
	}
	if additional, ok := document["additionalProperties"].(bool); !ok || additional {
		t.Fatal("surface schema must set additionalProperties:false")
	}
	properties, _ := document["properties"].(map[string]any)
	for _, name := range []string{"surfaceId", "catalogId", "components", "dataModel"} {
		if _, exists := properties[name]; !exists {
			t.Fatalf("surface schema must declare %q", name)
		}
	}
	// sendDataModel implies a renderer-to-agent return channel, which does not
	// exist while actions are disabled.
	if _, exists := properties["sendDataModel"]; exists {
		t.Fatal("surface schema must not allow sendDataModel")
	}
	catalogID, _ := properties["catalogId"].(map[string]any)
	if got := catalogID["const"]; got != CatalogID {
		t.Fatalf("surface catalogId const = %v, want %s", got, CatalogID)
	}
	items, _ := properties["components"].(map[string]any)
	itemSchema, _ := items["items"].(map[string]any)
	if got := itemSchema["$ref"]; got != "catalog.json#/$defs/anyComponent" {
		t.Fatalf("components items $ref = %v", got)
	}
}

// Renderers walk children through these members, and graph.go reads them by
// name. A new container component introducing a third name would leave the graph
// validator silently ignoring its children, so the names stay pinned here.
func TestCatalogChildMembersUseTheKnownNames(t *testing.T) {
	members := CatalogChildMembers()
	if len(members) == 0 {
		t.Fatal("no component references children")
	}
	for component, list := range members {
		for _, member := range list {
			switch {
			case member.Member == "children" && member.List:
			case member.Member == "child" && !member.List:
			default:
				t.Errorf("%s references children through %q (list=%t); graph.go only reads "+
					`"children" (list) and "child" (single)`, component, member.Member, member.List)
			}
		}
	}
	for _, container := range []string{"Column", "Row", "Card"} {
		if len(members[container]) == 0 {
			t.Errorf("%s is a container but exposes no child member", container)
		}
	}
	for _, leaf := range []string{"Text", "Chart", "Divider", "Button"} {
		if len(members[leaf]) != 0 {
			t.Errorf("%s is a leaf but exposes child members %+v", leaf, members[leaf])
		}
	}
}

func TestRegisteredCatalogIDs(t *testing.T) {
	ids := RegisteredCatalogIDs()
	if len(ids) != 1 || ids[0] != CatalogID {
		t.Fatalf("RegisteredCatalogIDs = %v", ids)
	}
	if !CatalogRegistered(CatalogID) {
		t.Fatal("platform catalog must be registered")
	}
	if CatalogRegistered("https://catalog.actweave.dev/standard/v2/catalog.json") {
		t.Fatal("unknown catalog must not be registered")
	}
}
