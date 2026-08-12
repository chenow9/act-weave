package a2ui

import (
	"fmt"
	"sort"
	"strings"
)

// DiagnosticReason classifies why a surface was rejected. It is the metric label
// for a2ui_catalog_invalid.
type DiagnosticReason string

const (
	ReasonSchema         DiagnosticReason = "schema"
	ReasonGraph          DiagnosticReason = "graph"
	ReasonChartSemantics DiagnosticReason = "chart_semantics"
	ReasonUnknownCatalog DiagnosticReason = "unknown_catalog"
)

// maxDiagnosticText bounds echoed identifiers so a model cannot inflate logs.
const maxDiagnosticText = 96

// Diagnostic explains a surface rejection precisely enough to fix the prompt.
//
// It carries structure only. Pointer, Keyword, Component and Expected are
// derived from the catalog or from JSON member names — never from payload
// values. The surface subtree is exempt from sensitive-key scanning (KD-11) and
// may legitimately hold user data, so echoing its values here would defeat that
// exemption.
type Diagnostic struct {
	Reason    DiagnosticReason
	Pointer   string
	Keyword   string
	Component string
	Expected  string
}

func (d *Diagnostic) Error() string {
	if d == nil {
		return ""
	}
	parts := []string{"a2ui: " + string(d.Reason)}
	if d.Pointer != "" {
		parts = append(parts, "at "+d.Pointer)
	}
	if d.Component != "" {
		parts = append(parts, "component "+d.Component)
	}
	if d.Keyword != "" {
		parts = append(parts, "keyword "+d.Keyword)
	}
	if d.Expected != "" {
		parts = append(parts, "expected "+d.Expected)
	}
	return strings.Join(parts, "; ")
}

func newDiagnostic(reason DiagnosticReason, pointer, keyword, component, expected string) *Diagnostic {
	return &Diagnostic{
		Reason:    reason,
		Pointer:   truncateDiagnosticText(pointer),
		Keyword:   keyword,
		Component: component,
		Expected:  truncateDiagnosticText(expected),
	}
}

func truncateDiagnosticText(value string) string {
	if len(value) <= maxDiagnosticText {
		return value
	}
	return value[:maxDiagnosticText] + "…"
}

func pointerAppend(pointer string, segments ...any) string {
	var builder strings.Builder
	builder.WriteString(pointer)
	for _, segment := range segments {
		builder.WriteString("/")
		builder.WriteString(escapePointerSegment(fmt.Sprint(segment)))
	}
	return builder.String()
}

func escapePointerSegment(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

// describeSchema summarizes what a catalog schema node accepts. Every string it
// produces originates in the catalog, never in the payload.
func describeSchema(node any, resolve func(string) (map[string]any, bool), depth int) string {
	schema, ok := node.(map[string]any)
	if !ok || depth > 3 {
		return ""
	}
	if ref, ok := schema["$ref"].(string); ok {
		name := ref[strings.LastIndex(ref, "/")+1:]
		if resolved, found := resolve(ref); found && depth < 3 {
			if described := describeSchema(resolved, resolve, depth+1); described != "" {
				return fmt.Sprintf("%s (%s)", name, described)
			}
		}
		return name
	}
	if expected, exists := schema["const"]; exists {
		return fmt.Sprintf("%q", fmt.Sprint(expected))
	}
	if values, ok := schema["enum"].([]any); ok {
		options := make([]string, 0, len(values))
		for _, value := range values {
			options = append(options, fmt.Sprint(value))
		}
		return "one of " + strings.Join(options, "|")
	}
	if branches, ok := schema["oneOf"].([]any); ok {
		options := make([]string, 0, len(branches))
		for _, branch := range branches {
			if described := describeSchema(branch, resolve, depth+1); described != "" {
				options = append(options, described)
			}
		}
		if len(options) > 0 {
			return "one of " + strings.Join(options, " | ")
		}
	}
	return describeSchemaType(schema, resolve, depth)
}

func describeSchemaType(schema map[string]any, resolve func(string) (map[string]any, bool), depth int) string {
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "array":
		if item := describeSchema(schema["items"], resolve, depth+1); item != "" {
			return "array of " + item
		}
		return "array"
	case "string":
		if pattern, ok := schema["pattern"].(string); ok {
			return "string matching " + pattern
		}
		return "string"
	case "object":
		return describeObjectMembers(schema, resolve, depth)
	case "":
		return ""
	default:
		return typeName
	}
}

// describeObjectMembers renders an object as its member names, marking optional
// ones and naming a member's type when that type is itself composite. Scalar
// members are left bare: their name already says enough.
func describeObjectMembers(
	schema map[string]any,
	resolve func(string) (map[string]any, bool),
	depth int,
) string {
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return "object"
	}
	required := make(map[string]struct{})
	for _, field := range requiredFields(schema) {
		required[field] = struct{}{}
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	members := make([]string, 0, len(names))
	for _, name := range names {
		member := name
		if _, isRequired := required[name]; !isRequired {
			member += "?"
		}
		if described := describeSchema(properties[name], resolve, depth+1); isCompositeDescription(described) {
			member += ": " + described
		}
		members = append(members, member)
	}
	return "{" + strings.Join(members, ", ") + "}"
}

func isCompositeDescription(described string) bool {
	return strings.HasPrefix(described, "array of") || strings.HasPrefix(described, "one of")
}

// requiredFields returns the declared required member names, sorted for stable
// diagnostics.
func requiredFields(schema map[string]any) []string {
	values, _ := schema["required"].([]any)
	fields := make([]string, 0, len(values))
	for _, value := range values {
		if name, ok := value.(string); ok {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}
