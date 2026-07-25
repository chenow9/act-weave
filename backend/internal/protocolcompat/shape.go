// Package protocolcompat extracts structural fingerprints from AAP JSON Schemas
// and detects breaking vs additive changes against a frozen baseline.
package protocolcompat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Shape is a simplified structural view used for compatibility comparison.
// It is intentionally coarser than full JSON Schema but catches the breaking
// changes listed in the AAP v1 ADR.
type Shape struct {
	// Path is document name or document#/$defs/name.
	Path string `json:"path"`
	// Types is the normalized type set (e.g. ["string"], ["object"], ["object","null"]).
	Types []string `json:"types,omitempty"`
	// Required lists required property names for objects.
	Required []string `json:"required,omitempty"`
	// Properties maps property name → nested shape path key (relative).
	Properties map[string]PropertyShape `json:"properties,omitempty"`
	// Enum is the closed set of allowed values when present.
	Enum []string `json:"enum,omitempty"`
	// Const is a fixed value when present.
	Const *string `json:"const,omitempty"`
	// AdditionalProperties records whether unknown props are allowed (default true in AAP).
	AdditionalProperties *bool `json:"additionalProperties,omitempty"`
}

// PropertyShape is a property-level structural summary.
type PropertyShape struct {
	Types    []string `json:"types,omitempty"`
	Required bool     `json:"required"`
	Enum     []string `json:"enum,omitempty"`
	Const    *string  `json:"const,omitempty"`
	Ref      string   `json:"ref,omitempty"`
}

// Baseline is the frozen structural snapshot for one schema set version.
type Baseline struct {
	ProtocolVersion string           `json:"protocolVersion"`
	SpecVersion     string           `json:"specVersion"`
	SchemaSetSHA256 string           `json:"schemaSetSha256"`
	Documents       map[string]Shape `json:"documents"` // path → shape
}

// LoadSchemaDocuments reads *.schema.json files from dir (excluding checksum files).
func LoadSchemaDocuments(dir string) (map[string]map[string]any, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]any)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".schema.json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		out[name] = doc
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no schema documents in %s", dir)
	}
	return out, nil
}

// ExtractBaseline builds a baseline from raw schema documents.
func ExtractBaseline(protocolVersion, specVersion, setSHA string, docs map[string]map[string]any) Baseline {
	shapes := make(map[string]Shape)
	names := make([]string, 0, len(docs))
	for name := range docs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		doc := docs[name]
		root := extractShape(name, doc)
		shapes[name] = root
		if defs, ok := doc["$defs"].(map[string]any); ok {
			defNames := make([]string, 0, len(defs))
			for defName := range defs {
				defNames = append(defNames, defName)
			}
			sort.Strings(defNames)
			for _, defName := range defNames {
				defSchema, ok := defs[defName].(map[string]any)
				if !ok {
					continue
				}
				path := name + "#/$defs/" + defName
				shapes[path] = extractShape(path, defSchema)
			}
		}
	}
	return Baseline{
		ProtocolVersion: protocolVersion,
		SpecVersion:     specVersion,
		SchemaSetSHA256: setSHA,
		Documents:       shapes,
	}
}

func extractShape(path string, schema map[string]any) Shape {
	shape := Shape{Path: path}
	shape.Types = normalizeTypes(schema)
	if req, ok := schema["required"].([]any); ok {
		for _, item := range req {
			if s, ok := item.(string); ok {
				shape.Required = append(shape.Required, s)
			}
		}
		sort.Strings(shape.Required)
	}
	if enum, ok := schema["enum"].([]any); ok {
		for _, item := range enum {
			shape.Enum = append(shape.Enum, fmt.Sprint(item))
		}
		sort.Strings(shape.Enum)
	}
	if c, ok := schema["const"]; ok {
		value := fmt.Sprint(c)
		shape.Const = &value
	}
	if ap, ok := schema["additionalProperties"]; ok {
		switch typed := ap.(type) {
		case bool:
			shape.AdditionalProperties = &typed
		default:
			// schema object for additionalProperties → treat as allowed with constraint
			allow := true
			shape.AdditionalProperties = &allow
		}
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		shape.Properties = make(map[string]PropertyShape, len(props))
		requiredSet := make(map[string]struct{}, len(shape.Required))
		for _, name := range shape.Required {
			requiredSet[name] = struct{}{}
		}
		for propName, raw := range props {
			propSchema, _ := raw.(map[string]any)
			ps := PropertyShape{}
			if propSchema != nil {
				ps.Types = normalizeTypes(propSchema)
				if enum, ok := propSchema["enum"].([]any); ok {
					for _, item := range enum {
						ps.Enum = append(ps.Enum, fmt.Sprint(item))
					}
					sort.Strings(ps.Enum)
				}
				if c, ok := propSchema["const"]; ok {
					value := fmt.Sprint(c)
					ps.Const = &value
				}
				if ref, ok := propSchema["$ref"].(string); ok {
					ps.Ref = ref
				}
			}
			_, ps.Required = requiredSet[propName]
			shape.Properties[propName] = ps
		}
	}
	return shape
}

func normalizeTypes(schema map[string]any) []string {
	if schema == nil {
		return nil
	}
	if ref, ok := schema["$ref"].(string); ok {
		return []string{"$ref:" + ref}
	}
	if oneOf, ok := schema["oneOf"].([]any); ok {
		var types []string
		for _, branch := range oneOf {
			branchMap, _ := branch.(map[string]any)
			types = append(types, normalizeTypes(branchMap)...)
		}
		return uniqueSorted(types)
	}
	switch typed := schema["type"].(type) {
	case string:
		return []string{typed}
	case []any:
		var types []string
		for _, item := range typed {
			if s, ok := item.(string); ok {
				types = append(types, s)
			}
		}
		return uniqueSorted(types)
	default:
		if _, ok := schema["enum"]; ok {
			return []string{"enum"}
		}
		if _, ok := schema["const"]; ok {
			return []string{"const"}
		}
		if _, ok := schema["properties"]; ok {
			return []string{"object"}
		}
		return nil
	}
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// WriteBaselineJSON writes the baseline with stable key order (via encoding/json maps).
func WriteBaselineJSON(path string, baseline Baseline) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

// ReadBaselineJSON loads a previously frozen baseline.
func ReadBaselineJSON(path string) (Baseline, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Baseline{}, err
	}
	var baseline Baseline
	if err := json.Unmarshal(raw, &baseline); err != nil {
		return Baseline{}, err
	}
	return baseline, nil
}
