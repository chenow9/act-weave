package a2ui

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// promptComponentOrder controls how components are presented to the model.
// Chart leads because charts are the primary use case; a component missing here
// is a build error, so the prompt cannot silently omit part of the catalog.
var promptComponentOrder = []string{
	"Chart",
	"Column", "Row", "Card", "Text", "Divider",
	"TextField", "ChoicePicker", "CheckBox", "DateTimeInput", "Button",
}

// BuildPromptAppendix renders the model instructions from the catalog itself.
//
// The prompt is derived rather than hand-written because the two drifted before:
// the previous appendix advertised shapes the parser never accepted. Anything
// the model is told here is, by construction, what validation enforces.
var BuildPromptAppendix = sync.OnceValue(func() string {
	catalog := loadCatalog()
	if catalog.err != nil {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "\n\n## A2UI surfaces (%s)\n\n", PromptTemplateV2)
	writePromptIntro(&out, catalog)
	writePromptComponents(&out, catalog)
	writePromptDataBinding(&out)
	writePromptRules(&out)
	return out.String()
})

func writePromptIntro(out *strings.Builder, catalog compiledCatalog) {
	if catalog.instructions != "" {
		out.WriteString(catalog.instructions)
		out.WriteString("\n\n")
	}
	out.WriteString("Attach at most one fenced block per reply, after your prose:\n\n")
	out.WriteString(FenceStart + "\n")
	out.WriteString(`{"components":[` + "\n")
	out.WriteString(`  {"id":"root","component":"Column","children":["title1","chart1"]},` + "\n")
	out.WriteString(`  {"id":"title1","component":"Text","text":"季度营收","variant":"heading"},` + "\n")
	out.WriteString(`  {"id":"chart1","component":"Chart","chartType":"bar","unit":"万元",` + "\n")
	out.WriteString(`   "series":[{"points":[{"label":"Q1","value":120},{"label":"Q2","value":150}]}]}` + "\n")
	out.WriteString("]}\n")
	out.WriteString(FenceEnd + "\n\n")
	out.WriteString("The fence body is the surface object itself — there is no wrapper. " +
		"Never emit surfaceId or catalogId; the platform assigns them.\n\n")
	// Live runs lost surfaces to a single unbalanced brace at the end of a long
	// multi-line body, never to the catalog. Saying so is the only fix allowed:
	// the parser must not start guessing where an object was meant to end.
	out.WriteString("Close every bracket: the body must parse as one complete JSON object, " +
		"with no comments and no prose inside the fence. A body that does not parse is " +
		"dropped whole and the user sees your prose alone.\n\n")
}

func writePromptComponents(out *strings.Builder, catalog compiledCatalog) {
	var components strings.Builder
	components.WriteString("### Components\n\n")
	components.WriteString("Every component also takes a required `id` and `component`.\n")
	for _, name := range promptComponentOrder {
		schema, ok := catalog.components[name].(map[string]any)
		if !ok {
			continue
		}
		properties, _ := schema["properties"].(map[string]any)
		required := requiredFields(schema)
		fmt.Fprintf(&components, "- **%s** — required: %s", name, strings.Join(withoutCommon(required), ", "))
		if optional := optionalFields(properties, required); len(optional) > 0 {
			fmt.Fprintf(&components, "; optional: %s", strings.Join(optional, ", "))
		}
		components.WriteString("\n")
		writePromptPropertyDetails(&components, properties)
	}
	components.WriteString("\n")

	rendered := components.String()
	out.WriteString(rendered)
	writePromptValueTypes(out, catalog, rendered)
}

// promptValueTypeOrder is the shared $defs the component list refers to by name.
// Naming them once and defining them once keeps the appendix from repeating the
// same expansion on every label, title and value member.
var promptValueTypeOrder = []string{
	"DynamicString", "DynamicBoolean", "ChoiceValue",
	"ChildList", "ComponentId", "DataBinding", "ChartSeries", "ChartPoint", "ChoiceOption",
}

// dontExpandRefs makes describeSchema print a reference by name instead of
// inlining it, which is what keeps the component list compact.
func dontExpandRefs(string) (map[string]any, bool) { return nil, false }

// promptScalarTypes are named types a model needs no help with: a plain string or
// boolean satisfies them, and the binding alternative is covered once under Data
// binding. Repeating them on every label and title was most of the prompt's size.
var promptScalarTypes = map[string]struct{}{
	"DynamicString": {}, "DynamicBoolean": {}, "ChoiceValue": {},
	"ChildList": {}, "ComponentId": {},
	"string": {}, "boolean": {}, "number": {}, "integer": {}, "object": {}, "array": {},
	"": {},
}

// writePromptPropertyDetails spells out only what a model cannot guess: closed
// value sets and composite shapes.
func writePromptPropertyDetails(out *strings.Builder, properties map[string]any) {
	for _, field := range sortedKeys(properties) {
		if field == "id" || field == "component" {
			continue
		}
		described := describeSchema(properties[field], dontExpandRefs, 0)
		if _, skip := promptScalarTypes[described]; skip {
			continue
		}
		fmt.Fprintf(out, "    - `%s`: %s\n", field, described)
	}
}

// writePromptValueTypes defines the composite types the component list actually
// referenced by name, so nothing is defined that is never mentioned.
func writePromptValueTypes(out *strings.Builder, catalog compiledCatalog, rendered string) {
	// A type named only by another type's definition still needs defining, so the
	// scan text grows as definitions are emitted.
	definitions := make([]string, 0, len(promptValueTypeOrder))
	for _, name := range promptValueTypeOrder {
		definition, ok := catalog.definitions[name]
		if !ok || !strings.Contains(rendered, name) {
			continue
		}
		described := describeSchema(definition, dontExpandRefs, 0)
		if described == "" {
			continue
		}
		definitions = append(definitions, fmt.Sprintf("- `%s`: %s", name, described))
		rendered += described
	}
	if len(definitions) == 0 {
		return
	}
	out.WriteString("### Value types\n\n")
	out.WriteString(strings.Join(definitions, "\n"))
	out.WriteString("\n\n")
}

func withoutCommon(fields []string) []string {
	kept := make([]string, 0, len(fields))
	for _, field := range fields {
		if field == "id" || field == "component" {
			continue
		}
		kept = append(kept, field)
	}
	if len(kept) == 0 {
		return []string{"(none beyond id and component)"}
	}
	return kept
}

func optionalFields(properties map[string]any, required []string) []string {
	requiredSet := make(map[string]struct{}, len(required))
	for _, field := range required {
		requiredSet[field] = struct{}{}
	}
	optional := make([]string, 0, len(properties))
	for field := range properties {
		if _, isRequired := requiredSet[field]; isRequired {
			continue
		}
		optional = append(optional, field)
	}
	sort.Strings(optional)
	return optional
}

func writePromptDataBinding(out *strings.Builder) {
	out.WriteString("### Data binding\n\n")
	out.WriteString("Any member above may be replaced by `{\"path\":\"/pointer\"}`, a JSON Pointer " +
		"into the surface `dataModel`. Use it to share one dataset between components:\n\n")
	out.WriteString("```json\n")
	out.WriteString(`{"components":[{"id":"root","component":"Chart","chartType":"line",` + "\n")
	out.WriteString(`  "series":{"path":"/trend"}}],` + "\n")
	out.WriteString(`  "dataModel":{"trend":[{"name":"2026","points":[{"label":"Jan","value":9}]}]}}` + "\n")
	out.WriteString("```\n\n")
}

func writePromptRules(out *strings.Builder) {
	rules := []string{
		fmt.Sprintf("Exactly one component must have id %q; it is the tree root.", RootID),
		"Ids are unique, match `[A-Za-z_][A-Za-z0-9_]*`, and every component must be " +
			"reachable from root. No cycles.",
		fmt.Sprintf("At most %d components, nested at most %d levels deep.", MaxComponents, MaxTreeDepth),
		fmt.Sprintf("At most %d series per chart and %d points per series. Multi-series charts "+
			"need a name on every series and the same number of points in each.",
			MaxChartSeries, MaxChartPoints),
		"`pie` and `donut` take exactly one series with non-negative values. " +
			"`stacked` applies only to `bar`, `hbar` and `area`.",
		"Never describe styling: no colors, sizes, fonts, legends or axes. " +
			"Provide semantic data only; the client decides how it looks.",
		"Unknown properties are rejected outright — the whole surface is dropped and the " +
			"user sees your prose alone. Use only the members listed above.",
		"Buttons are display-only: nothing is submitted and no callback runs.",
	}
	out.WriteString("### Rules\n\n")
	for _, rule := range rules {
		fmt.Fprintf(out, "- %s\n", rule)
	}
}

// AppendPromptRules appends the catalog-derived rules to a system instruction.
// Idempotent for a single drive() inject: callers must not re-apply on resume
// rebuilds (resume skips history assembly and reuses the frozen agent graph).
func AppendPromptRules(instruction string) string {
	// Avoid accidental double-append if a caller reuses an already-injected string.
	// Check before any mutation so a second call is a pure no-op.
	if strings.Contains(instruction, PromptTemplateV2) ||
		strings.Contains(instruction, FenceStart) {
		return instruction
	}
	appendix := BuildPromptAppendix()
	if appendix == "" {
		return instruction
	}
	instruction = strings.TrimRight(instruction, " \t\r\n")
	if instruction == "" {
		return strings.TrimSpace(appendix)
	}
	return instruction + appendix
}
