package a2ui

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	componentChart = "Chart"

	MaxChartSeries = 8
	MaxChartPoints = 64
)

var (
	singleSeriesChartTypes = map[string]bool{"pie": true, "donut": true}
	stackableChartTypes    = map[string]bool{"bar": true, "hbar": true, "area": true}
)

// validateChartSemantics enforces the cross-field chart rules JSON Schema cannot
// express. Input must already be schema-valid.
func validateChartSemantics(components []any, dataModel any) *Diagnostic {
	for index, raw := range components {
		component, _ := raw.(map[string]any)
		if name, _ := component["component"].(string); name != componentChart {
			continue
		}
		pointer := pointerAppend("/components", index)
		if diagnostic := validateChart(pointer, component, dataModel); diagnostic != nil {
			return diagnostic
		}
	}
	return nil
}

func validateChart(pointer string, chart map[string]any, dataModel any) *Diagnostic {
	chartType, _ := chart["chartType"].(string)

	if stacked, ok := chart["stacked"].(bool); ok && stacked && !stackableChartTypes[chartType] {
		return newDiagnostic(ReasonChartSemantics, pointerAppend(pointer, "stacked"), "stackable",
			componentChart, "stacked only with chartType bar|hbar|area")
	}

	series, seriesPointer, diagnostic := resolveSeries(pointer, chart, dataModel)
	if diagnostic != nil {
		return diagnostic
	}

	if singleSeriesChartTypes[chartType] && len(series) != 1 {
		return newDiagnostic(ReasonChartSemantics, seriesPointer, "seriesCount", componentChart,
			"exactly 1 series for chartType "+chartType)
	}

	pointCount := -1
	for index, entry := range series {
		entryPointer := pointerAppend(seriesPointer, index)
		points, _ := entry["points"].([]any)

		if len(series) > 1 {
			if name, _ := entry["name"].(string); name == "" {
				return newDiagnostic(ReasonChartSemantics, entryPointer, "required", componentChart,
					"name on every series when more than one is present")
			}
			if pointCount >= 0 && len(points) != pointCount {
				return newDiagnostic(ReasonChartSemantics, pointerAppend(entryPointer, "points"),
					"pointsAligned", componentChart,
					"the same number of points as the first series ("+strconv.Itoa(pointCount)+")")
			}
			pointCount = len(points)
		}

		if !singleSeriesChartTypes[chartType] {
			continue
		}
		for position, rawPoint := range points {
			point, _ := rawPoint.(map[string]any)
			number, ok := point["value"].(json.Number)
			if !ok {
				continue
			}
			if value, err := number.Float64(); err == nil && value < 0 {
				return newDiagnostic(ReasonChartSemantics,
					pointerAppend(entryPointer, "points", position, "value"), "minimum", componentChart,
					"a value >= 0 for chartType "+chartType)
			}
		}
	}
	return nil
}

// resolveSeries returns the series array, following a DataBinding into the
// dataModel when the chart binds rather than inlines its data. The bound value
// is held to the same shape as an inline series array.
func resolveSeries(
	pointer string,
	chart map[string]any,
	dataModel any,
) ([]map[string]any, string, *Diagnostic) {
	switch declared := chart["series"].(type) {
	case []any:
		return coerceSeries(declared), pointerAppend(pointer, "series"), nil
	case map[string]any:
		path, _ := declared["path"].(string)
		bound, found := resolveJSONPointer(dataModel, path)
		if !found {
			return nil, "", newDiagnostic(ReasonChartSemantics, pointerAppend(pointer, "series", "path"),
				"resolvable", componentChart, "a path present in dataModel")
		}
		values, ok := bound.([]any)
		if !ok {
			return nil, "", newDiagnostic(ReasonChartSemantics, "/dataModel"+path, "type",
				componentChart, "array of ChartSeries")
		}
		if diagnostic := validateBoundSeries("/dataModel"+path, values); diagnostic != nil {
			return nil, "", diagnostic
		}
		return coerceSeries(values), "/dataModel" + path, nil
	default:
		return nil, "", newDiagnostic(ReasonChartSemantics, pointerAppend(pointer, "series"), "type",
			componentChart, "array of ChartSeries or a DataBinding")
	}
}

// validateBoundSeries applies the catalog ChartSeries contract to data reached
// through a binding, which the schema pass cannot see.
func validateBoundSeries(pointer string, values []any) *Diagnostic {
	catalog := loadCatalog()
	if catalog.err != nil {
		return newDiagnostic(ReasonChartSemantics, pointer, "catalog_unavailable", componentChart, "")
	}
	if len(values) == 0 || len(values) > MaxChartSeries {
		return newDiagnostic(ReasonChartSemantics, pointer, "maxItems", componentChart,
			"between 1 and 8 series")
	}
	for index, value := range values {
		if catalog.validateNode("#/$defs/ChartSeries", value) {
			continue
		}
		return newDiagnostic(ReasonChartSemantics, pointerAppend(pointer, index), "$ref", componentChart,
			describeSchema(map[string]any{"$ref": "#/$defs/ChartSeries"}, catalog.resolve, 0))
	}
	return nil
}

func coerceSeries(values []any) []map[string]any {
	series := make([]map[string]any, 0, len(values))
	for _, value := range values {
		entry, _ := value.(map[string]any)
		series = append(series, entry)
	}
	return series
}

// resolveJSONPointer walks an RFC 6901 pointer. Array indices are supported so a
// binding can address a single series.
func resolveJSONPointer(root any, pointer string) (any, bool) {
	if pointer == "" || pointer == "/" {
		return root, root != nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	node := root
	for _, segment := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		switch typed := node.(type) {
		case map[string]any:
			value, exists := typed[segment]
			if !exists {
				return nil, false
			}
			node = value
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			node = typed[index]
		default:
			return nil, false
		}
	}
	return node, true
}
