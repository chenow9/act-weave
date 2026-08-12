package a2ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func surfaceJSON(t *testing.T, components string, extra ...string) json.RawMessage {
	t.Helper()
	body := `"components":[` + components + `]`
	for _, fragment := range extra {
		body += "," + fragment
	}
	return json.RawMessage("{" + body + "}")
}

func chartComponent(chartType string, series string, extra ...string) string {
	component := fmt.Sprintf(`{"id":"c1","component":"Chart","chartType":%q,"series":%s`, chartType, series)
	for _, fragment := range extra {
		component += "," + fragment
	}
	return component + "}"
}

const singleSeries = `[{"points":[{"label":"A","value":10},{"label":"B","value":20}]}]`

func rootColumn(children ...string) string {
	quoted := make([]string, 0, len(children))
	for _, child := range children {
		quoted = append(quoted, fmt.Sprintf("%q", child))
	}
	return `{"id":"root","component":"Column","children":[` + strings.Join(quoted, ",") + `]}`
}

func assertValid(t *testing.T, surface json.RawMessage) {
	t.Helper()
	if diagnostic := ValidateSurface(CatalogID, surface); diagnostic != nil {
		t.Fatalf("ValidateSurface = %v, want nil", diagnostic)
	}
}

func assertDiagnostic(t *testing.T, surface json.RawMessage, reason DiagnosticReason, pointer, keyword string) *Diagnostic {
	t.Helper()
	diagnostic := ValidateSurface(CatalogID, surface)
	if diagnostic == nil {
		t.Fatal("ValidateSurface = nil, want a diagnostic")
	}
	if diagnostic.Reason != reason {
		t.Fatalf("Reason = %s, want %s (%v)", diagnostic.Reason, reason, diagnostic)
	}
	if pointer != "" && diagnostic.Pointer != pointer {
		t.Fatalf("Pointer = %s, want %s (%v)", diagnostic.Pointer, pointer, diagnostic)
	}
	if keyword != "" && diagnostic.Keyword != keyword {
		t.Fatalf("Keyword = %s, want %s (%v)", diagnostic.Keyword, keyword, diagnostic)
	}
	return diagnostic
}

func TestCompiledCatalogIsAvailable(t *testing.T) {
	if catalog := loadCatalog(); catalog.err != nil {
		t.Fatalf("catalog failed to compile: %v", catalog.err)
	}
}

func TestValidateSurfaceAcceptsEveryChartType(t *testing.T) {
	for _, chartType := range []string{"bar", "hbar", "line", "area", "pie", "donut"} {
		t.Run(chartType, func(t *testing.T) {
			assertValid(t, surfaceJSON(t,
				rootColumn("c1")+","+chartComponent(chartType, singleSeries,
					`"title":"Revenue"`, `"unit":"万元"`, `"valueFormat":"compact"`)))
		})
	}
}

func TestValidateSurfaceAcceptsMultiSeriesAndStacked(t *testing.T) {
	multi := `[{"name":"2025","points":[{"label":"Q1","value":1},{"label":"Q2","value":2}]},` +
		`{"name":"2026","points":[{"label":"Q1","value":3},{"label":"Q2","value":4}]}]`
	for _, chartType := range []string{"bar", "hbar", "area"} {
		assertValid(t, surfaceJSON(t,
			rootColumn("c1")+","+chartComponent(chartType, multi, `"stacked":true`)))
	}
	assertValid(t, surfaceJSON(t, rootColumn("c1")+","+chartComponent("line", multi)))
}

func TestValidateSurfaceAcceptsBoundSeries(t *testing.T) {
	assertValid(t, surfaceJSON(t,
		rootColumn("c1")+","+chartComponent("bar", `{"path":"/revenue"}`),
		`"dataModel":{"revenue":`+singleSeries+`}`))
}

func TestValidateSurfaceAcceptsFullFormAndMaterializedFields(t *testing.T) {
	components := strings.Join([]string{
		rootColumn("card1"),
		`{"id":"card1","component":"Card","child":"col1","title":"预约登记"}`,
		`{"id":"col1","component":"Column","children":["t1","f1","f2","f3","f4","d1","b1"],"align":"stretch"}`,
		`{"id":"t1","component":"Text","text":"请填写以下信息","variant":"heading"}`,
		`{"id":"f1","component":"TextField","label":"姓名","variant":"shortText","required":true}`,
		`{"id":"f2","component":"CheckBox","label":"接收通知","value":true}`,
		`{"id":"f3","component":"ChoicePicker","label":"城市","options":[{"value":"sh","label":"上海"}],"multiple":false}`,
		`{"id":"f4","component":"DateTimeInput","label":"日期","mode":"date"}`,
		`{"id":"d1","component":"Divider"}`,
		`{"id":"b1","component":"Button","label":"提交","variant":"primary"}`,
	}, ",")
	assertValid(t, surfaceJSON(t, components,
		`"surfaceId":"019ff3f0-bfdd-7b38-9c53-f90bf5812478:item_1"`,
		`"catalogId":`+fmt.Sprintf("%q", CatalogID)))
}

func TestValidateSurfaceRejectsUnknownCatalog(t *testing.T) {
	surface := surfaceJSON(t, rootColumn("t1")+`,{"id":"t1","component":"Text","text":"hi"}`)
	diagnostic := ValidateSurface("https://catalog.actweave.dev/standard/v2/catalog.json", surface)
	if diagnostic == nil || diagnostic.Reason != ReasonUnknownCatalog {
		t.Fatalf("ValidateSurface = %v, want unknown_catalog", diagnostic)
	}
	diagnostic = ValidateSurface(CatalogID, surfaceJSON(t,
		rootColumn("t1")+`,{"id":"t1","component":"Text","text":"hi"}`,
		`"catalogId":"https://evil.test/catalog.json"`))
	if diagnostic == nil || diagnostic.Pointer != "/catalogId" {
		t.Fatalf("declared catalogId mismatch = %v", diagnostic)
	}
}

func TestValidateSurfaceSchemaFailures(t *testing.T) {
	cases := []struct {
		name    string
		surface json.RawMessage
		pointer string
		keyword string
	}{
		{
			name:    "unknown surface key",
			surface: surfaceJSON(t, rootColumn("t1")+`,{"id":"t1","component":"Text","text":"x"}`, `"sendDataModel":true`),
			pointer: "/sendDataModel",
			keyword: "additionalProperties",
		},
		{
			name:    "unknown component type",
			surface: surfaceJSON(t, rootColumn("x1")+`,{"id":"x1","component":"PieChart","data":[]}`),
			pointer: "/components/1/component",
			keyword: "const",
		},
		{
			name:    "missing required member",
			surface: surfaceJSON(t, rootColumn("c1")+`,{"id":"c1","component":"Chart","chartType":"bar"}`),
			pointer: "/components/1",
			keyword: "required",
		},
		{
			name:    "alias property rejected",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("bar", singleSeries, `"chartData":[]`)),
			pointer: "/components/1/chartData",
			keyword: "additionalProperties",
		},
		{
			name:    "visual property rejected",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("bar", singleSeries, `"colors":["#f00"]`)),
			pointer: "/components/1/colors",
			keyword: "additionalProperties",
		},
		{
			name:    "chartType outside enum",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("radar", singleSeries)),
			pointer: "/components/1/chartType",
			keyword: "enum",
		},
		{
			name:    "button action rejected",
			surface: surfaceJSON(t, rootColumn("b1")+`,{"id":"b1","component":"Button","label":"Go","action":{"event":{"name":"go"}}}`),
			pointer: "/components/1/action",
			keyword: "additionalProperties",
		},
		{
			name:    "point missing value",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("bar", `[{"points":[{"label":"A"}]}]`)),
			pointer: "/components/1/series",
			keyword: "oneOf",
		},
		{
			name:    "too many series",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("bar", manySeries(9))),
			pointer: "/components/1/series",
			keyword: "oneOf",
		},
		{
			name:    "empty components",
			surface: json.RawMessage(`{"components":[]}`),
			pointer: "/components",
			keyword: "minItems",
		},
		{
			name:    "components not an array",
			surface: json.RawMessage(`{"components":{}}`),
			pointer: "/components",
			keyword: "type",
		},
		{
			name:    "missing components",
			surface: json.RawMessage(`{"dataModel":{}}`),
			pointer: "",
			keyword: "required",
		},
		{
			name:    "dataModel not an object",
			surface: surfaceJSON(t, rootColumn("t1")+`,{"id":"t1","component":"Text","text":"x"}`, `"dataModel":[]`),
			pointer: "/dataModel",
			keyword: "type",
		},
		{
			name:    "invalid component id",
			surface: surfaceJSON(t, rootColumn("9bad")+`,{"id":"9bad","component":"Text","text":"x"}`),
			pointer: "/components/0/children",
			keyword: "$ref",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertDiagnostic(t, testCase.surface, ReasonSchema, testCase.pointer, testCase.keyword)
		})
	}
}

func manySeries(count int) string {
	entries := make([]string, 0, count)
	for index := 0; index < count; index++ {
		entries = append(entries, fmt.Sprintf(`{"name":"s%d","points":[{"label":"A","value":1}]}`, index))
	}
	return "[" + strings.Join(entries, ",") + "]"
}

func TestValidateSurfaceGraphFailures(t *testing.T) {
	cases := []struct {
		name    string
		surface json.RawMessage
		pointer string
		keyword string
	}{
		{
			name:    "no root",
			surface: surfaceJSON(t, `{"id":"a1","component":"Text","text":"x"}`),
			pointer: "/components",
			keyword: "root",
		},
		{
			name: "duplicate id",
			surface: surfaceJSON(t, rootColumn("t1")+
				`,{"id":"t1","component":"Text","text":"x"},{"id":"t1","component":"Text","text":"y"}`),
			pointer: "/components/2/id",
			keyword: "unique",
		},
		{
			name:    "dangling children reference",
			surface: surfaceJSON(t, rootColumn("missing")),
			pointer: "/components/0/children/0",
			keyword: "reference",
		},
		{
			name: "dangling card child",
			surface: surfaceJSON(t, rootColumn("card1")+
				`,{"id":"card1","component":"Card","child":"nope"}`),
			pointer: "/components/1/child",
			keyword: "reference",
		},
		{
			name: "cycle",
			surface: surfaceJSON(t, rootColumn("a1")+
				`,{"id":"a1","component":"Column","children":["root"]}`),
			pointer: "/components/0",
			keyword: "acyclic",
		},
		{
			name: "unreachable component",
			surface: surfaceJSON(t, rootColumn("t1")+
				`,{"id":"t1","component":"Text","text":"x"},{"id":"orphan","component":"Text","text":"y"}`),
			pointer: "/components/2",
			keyword: "reachable",
		},
		{
			name:    "too deep",
			surface: deepColumnSurface(t, MaxTreeDepth+1),
			pointer: "",
			keyword: "maxDepth",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertDiagnostic(t, testCase.surface, ReasonGraph, testCase.pointer, testCase.keyword)
		})
	}
}

func deepColumnSurface(t *testing.T, depth int) json.RawMessage {
	t.Helper()
	components := make([]string, 0, depth)
	for level := 0; level < depth-1; level++ {
		id := "root"
		if level > 0 {
			id = fmt.Sprintf("n%d", level)
		}
		components = append(components,
			fmt.Sprintf(`{"id":%q,"component":"Column","children":["n%d"]}`, id, level+1))
	}
	components = append(components,
		fmt.Sprintf(`{"id":"n%d","component":"Text","text":"leaf"}`, depth-1))
	return surfaceJSON(t, strings.Join(components, ","))
}

func TestValidateSurfaceChartSemanticFailures(t *testing.T) {
	twoSeries := `[{"name":"a","points":[{"label":"A","value":1}]},{"name":"b","points":[{"label":"A","value":2}]}]`
	cases := []struct {
		name    string
		surface json.RawMessage
		pointer string
		keyword string
	}{
		{
			name:    "pie with two series",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("pie", twoSeries)),
			pointer: "/components/1/series",
			keyword: "seriesCount",
		},
		{
			name:    "donut with two series",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("donut", twoSeries)),
			pointer: "/components/1/series",
			keyword: "seriesCount",
		},
		{
			name:    "pie with negative value",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("pie", `[{"points":[{"label":"A","value":-1}]}]`)),
			pointer: "/components/1/series/0/points/0/value",
			keyword: "minimum",
		},
		{
			name:    "stacked line",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("line", singleSeries, `"stacked":true`)),
			pointer: "/components/1/stacked",
			keyword: "stackable",
		},
		{
			name:    "stacked pie",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("pie", singleSeries, `"stacked":true`)),
			pointer: "/components/1/stacked",
			keyword: "stackable",
		},
		{
			name: "multi series missing name",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("bar",
				`[{"name":"a","points":[{"label":"A","value":1}]},{"points":[{"label":"A","value":2}]}]`)),
			pointer: "/components/1/series/1",
			keyword: "required",
		},
		{
			name: "multi series misaligned points",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("bar",
				`[{"name":"a","points":[{"label":"A","value":1},{"label":"B","value":2}]},`+
					`{"name":"b","points":[{"label":"A","value":3}]}]`)),
			pointer: "/components/1/series/1/points",
			keyword: "pointsAligned",
		},
		{
			name:    "binding path missing",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("bar", `{"path":"/nope"}`), `"dataModel":{"revenue":[]}`),
			pointer: "/components/1/series/path",
			keyword: "resolvable",
		},
		{
			name:    "binding without dataModel",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("bar", `{"path":"/revenue"}`)),
			pointer: "/components/1/series/path",
			keyword: "resolvable",
		},
		{
			name:    "binding resolves to non-array",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("bar", `{"path":"/revenue"}`), `"dataModel":{"revenue":{}}`),
			pointer: "/dataModel/revenue",
			keyword: "type",
		},
		{
			name: "binding resolves to malformed series",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("bar", `{"path":"/revenue"}`),
				`"dataModel":{"revenue":[{"points":[{"label":"A"}]}]}`),
			pointer: "/dataModel/revenue/0",
			keyword: "$ref",
		},
		{
			name: "binding resolves to empty series",
			surface: surfaceJSON(t, rootColumn("c1")+","+chartComponent("bar", `{"path":"/revenue"}`),
				`"dataModel":{"revenue":[]}`),
			pointer: "/dataModel/revenue",
			keyword: "maxItems",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertDiagnostic(t, testCase.surface, ReasonChartSemantics, testCase.pointer, testCase.keyword)
		})
	}
}

// TestSensitiveFieldNamesStayValid keeps KD-11 intact: the surface subtree is
// exempt from sensitive-key scanning, so a legitimate credential form must pass
// catalog validation untouched.
func TestSensitiveFieldNamesStayValid(t *testing.T) {
	for _, label := range []string{"password", "accessToken", "apiKey", "bearerToken", "Authorization"} {
		t.Run(label, func(t *testing.T) {
			components := rootColumn("f1") +
				fmt.Sprintf(`,{"id":"f1","component":"TextField","label":%q,"placeholder":%q,"variant":"password"}`,
					label, label)
			assertValid(t, surfaceJSON(t, components))
		})
	}
}

// TestDiagnosticsNeverEchoPayloadValues guards the §5.2 constraint: diagnostics
// may name members and catalog expectations but must not carry surface values.
func TestDiagnosticsNeverEchoPayloadValues(t *testing.T) {
	const secret = "SENTINEL_VALUE_bearer_abcdefghijklmnop"
	surfaces := []json.RawMessage{
		surfaceJSON(t, rootColumn("c1")+","+chartComponent("radar", singleSeries, fmt.Sprintf(`"title":%q`, secret))),
		surfaceJSON(t, rootColumn("c1")+","+chartComponent("pie",
			fmt.Sprintf(`[{"name":%q,"points":[{"label":%q,"value":-5}]}]`, secret, secret))),
		surfaceJSON(t, rootColumn("f1")+
			fmt.Sprintf(`,{"id":"f1","component":"TextField","label":%q,"variant":"nope"}`, secret)),
		surfaceJSON(t, rootColumn("t1")+
			fmt.Sprintf(`,{"id":"t1","component":"Text","text":%q,"unknownField":%q}`, secret, secret)),
	}
	for index, surface := range surfaces {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			diagnostic := ValidateSurface(CatalogID, surface)
			if diagnostic == nil {
				t.Fatal("expected a diagnostic")
			}
			rendered := diagnostic.Error()
			if strings.Contains(rendered, secret) {
				t.Fatalf("diagnostic leaked a payload value: %s", rendered)
			}
			for _, field := range []string{diagnostic.Pointer, diagnostic.Keyword, diagnostic.Component, diagnostic.Expected} {
				if strings.Contains(field, secret) {
					t.Fatalf("diagnostic field leaked a payload value: %s", field)
				}
			}
		})
	}
}

func TestDiagnosticTextIsBounded(t *testing.T) {
	long := strings.Repeat("k", 400)
	surface := surfaceJSON(t, rootColumn("t1")+
		fmt.Sprintf(`,{"id":"t1","component":"Text","text":"x",%q:1}`, long))
	diagnostic := ValidateSurface(CatalogID, surface)
	if diagnostic == nil {
		t.Fatal("expected a diagnostic")
	}
	if len(diagnostic.Pointer) > maxDiagnosticText+len("…") {
		t.Fatalf("pointer not truncated: %d chars", len(diagnostic.Pointer))
	}
}

func TestValidateSurfaceRejectsNonObject(t *testing.T) {
	for _, payload := range []string{`[]`, `"text"`, `42`, `null`, `{`} {
		if diagnostic := ValidateSurface(CatalogID, json.RawMessage(payload)); diagnostic == nil {
			t.Fatalf("ValidateSurface(%s) = nil, want a diagnostic", payload)
		}
	}
}

func TestResolveJSONPointer(t *testing.T) {
	decoded, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	_ = decoded
	root := map[string]any{
		"a":     map[string]any{"b": []any{"zero", "one"}},
		"m~n/o": "escaped",
	}
	cases := map[string]struct {
		pointer string
		found   bool
		want    any
	}{
		"nested array":    {pointer: "/a/b/1", found: true, want: "one"},
		"object":          {pointer: "/a", found: true},
		"escaped segment": {pointer: "/m~0n~1o", found: true, want: "escaped"},
		"missing key":     {pointer: "/nope", found: false},
		"index overflow":  {pointer: "/a/b/9", found: false},
		"not a pointer":   {pointer: "a/b", found: false},
		"through scalar":  {pointer: "/a/b/0/x", found: false},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			value, found := resolveJSONPointer(root, testCase.pointer)
			if found != testCase.found {
				t.Fatalf("found = %v, want %v", found, testCase.found)
			}
			if testCase.want != nil && value != testCase.want {
				t.Fatalf("value = %v, want %v", value, testCase.want)
			}
		})
	}
}
