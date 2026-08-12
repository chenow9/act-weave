package a2ui

import (
	"bytes"
	"embed"
)

// Catalog and surface schema identifiers. Per A2UI convention the catalogId is
// the catalog document URI and equals its $id.
const (
	CatalogID       = "https://catalog.actweave.dev/standard/v1/catalog.json"
	SurfaceSchemaID = "https://catalog.actweave.dev/standard/v1/surface.schema.json"

	// EnvelopeVersionV1 marks a catalog-conformant surface. EnvelopeVersionV0
	// predates the catalog contract and was never published; the write path
	// stops emitting it once validation lands.
	EnvelopeVersionV1 = "a2ui-surface.v1"

	PromptTemplateV2 = "a2ui-prompt.v2"
)

const (
	catalogFile       = "catalogs/standard/v1/catalog.json"
	surfaceSchemaFile = "catalogs/standard/v1/surface.schema.json"
)

//go:embed catalogs/standard/v1/*.json
var catalogFiles embed.FS

// CatalogDocument returns the component catalog schema.
func CatalogDocument() ([]byte, error) { return readCatalogFile(catalogFile) }

// SurfaceSchemaDocument returns the surface envelope schema. It references the
// catalog through a relative $ref, so both documents must be registered
// together for validation to resolve.
func SurfaceSchemaDocument() ([]byte, error) { return readCatalogFile(surfaceSchemaFile) }

// RegisteredCatalogIDs lists the catalogs this build can validate against.
// Advertised on the AAP agent profile as a2ui.catalogIds.
func RegisteredCatalogIDs() []string { return []string{CatalogID} }

// CatalogRegistered reports whether a surface may declare this catalogId.
func CatalogRegistered(catalogID string) bool { return catalogID == CatalogID }

// CatalogComponentNames lists the components this catalog defines, sorted.
// Renderers dispatch on exactly these names, so tests and the TypeScript
// generator read them from here rather than restating them.
func CatalogComponentNames() []string {
	catalog := loadCatalog()
	if catalog.err != nil {
		return nil
	}
	return catalog.componentNames()
}

// CatalogEnums maps component name → property name → the values that property
// accepts, for every closed value set in the catalog.
func CatalogEnums() map[string]map[string][]string {
	catalog := loadCatalog()
	if catalog.err != nil {
		return nil
	}
	enums := make(map[string]map[string][]string, len(catalog.components))
	for _, name := range catalog.componentNames() {
		schema, _ := catalog.components[name].(map[string]any)
		properties, _ := schema["properties"].(map[string]any)
		for _, field := range sortedKeys(properties) {
			property, _ := properties[field].(map[string]any)
			values := enumStrings(property["enum"])
			if len(values) == 0 {
				continue
			}
			if enums[name] == nil {
				enums[name] = make(map[string][]string)
			}
			enums[name][field] = values
		}
	}
	return enums
}

// CatalogChildMember names a component member that references other components.
type CatalogChildMember struct {
	// Member is the property name, such as "children" or "child".
	Member string
	// List is true when the member holds several ids rather than one.
	List bool
}

// CatalogChildMembers maps component name → the members through which it
// references children, sorted by member name.
//
// A renderer walks the component graph without knowing which component is a
// container, so it needs this as a fact from the catalog rather than a list of
// its own that a new container component would silently invalidate.
func CatalogChildMembers() map[string][]CatalogChildMember {
	catalog := loadCatalog()
	if catalog.err != nil {
		return nil
	}
	members := make(map[string][]CatalogChildMember)
	for _, name := range catalog.componentNames() {
		schema, _ := catalog.components[name].(map[string]any)
		properties, _ := schema["properties"].(map[string]any)
		for _, field := range sortedKeys(properties) {
			if field == "id" {
				continue
			}
			property, _ := properties[field].(map[string]any)
			switch reference, _ := property["$ref"].(string); reference {
			case "#/$defs/ChildList":
				members[name] = append(members[name], CatalogChildMember{Member: field, List: true})
			case "#/$defs/ComponentId":
				members[name] = append(members[name], CatalogChildMember{Member: field})
			}
		}
	}
	return members
}

func enumStrings(raw any) []string {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil
		}
		values = append(values, text)
	}
	return values
}

func readCatalogFile(name string) ([]byte, error) {
	value, err := catalogFiles.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(value), nil
}
