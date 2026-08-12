package metrics

// A2UI emit result labels (design §10). Keep values aligned with a2ui.EmitResult.
const (
	A2UIResultNone               = "none"
	A2UIResultOK                 = "ok"
	A2UIResultOKEmptyText        = "ok_empty_text"
	A2UIResultInvalidJSON        = "invalid_json"
	A2UIResultTooLarge           = "too_large"
	A2UIResultStrippedDisabled   = "stripped_disabled"
	A2UIResultTruncated          = "truncated"
	A2UIResultProjectionRejected = "projection_rejected"
	A2UIResultProjectionOff      = "projection_off"
	A2UIResultCatalogInvalid     = "catalog_invalid"
)

// A2UI catalog rejection reasons. Keep aligned with a2ui.DiagnosticReason.
const (
	A2UIReasonSchema         = "schema"
	A2UIReasonGraph          = "graph"
	A2UIReasonChartSemantics = "chart_semantics"
	A2UIReasonUnknownCatalog = "unknown_catalog"
)

// a2uiInvalidKeywords bounds the keyword label to the vocabulary the catalog
// validator emits, so a diagnostic can never widen the metric's cardinality.
var a2uiInvalidKeywords = map[string]struct{}{
	"$ref": {}, "additionalProperties": {}, "const": {}, "enum": {}, "items": {},
	"maxItems": {}, "maxLength": {}, "maximum": {}, "minItems": {}, "minLength": {},
	"minimum": {}, "oneOf": {}, "pattern": {}, "required": {}, "schema": {}, "type": {},
	"acyclic": {}, "maxDepth": {}, "reachable": {}, "reference": {}, "root": {}, "unique": {},
	"pointsAligned": {}, "resolvable": {}, "seriesCount": {}, "stackable": {},
	"catalog_unavailable": {},
}

var a2uiChartTypes = map[string]struct{}{
	"bar": {}, "hbar": {}, "line": {}, "area": {}, "pie": {}, "donut": {},
}

// ObserveA2UIEmit records one terminal completeRun A2UI extract classification.
// result must be a stable low-cardinality label (see A2UIResult* constants).
func (c *AAPCollector) ObserveA2UIEmit(result string) {
	if c == nil {
		return
	}
	result = normalizeA2UIResult(result)
	c.labeled.add("a2ui_emit_total", map[string]string{"result": result}, 1)
	switch result {
	case A2UIResultOK, A2UIResultOKEmptyText, A2UIResultTruncated:
		c.labeled.add("a2ui_extract_ok_total", nil, 1)
	case A2UIResultInvalidJSON, A2UIResultTooLarge:
		c.labeled.add("a2ui_extract_fail_total", nil, 1)
	case A2UIResultProjectionRejected:
		c.labeled.add("a2ui_preflight_fail_total", nil, 1)
		c.labeled.add("a2ui_degraded_text_total", nil, 1)
	case A2UIResultStrippedDisabled, A2UIResultProjectionOff:
		c.labeled.add("a2ui_degraded_text_total", nil, 1)
	case A2UIResultCatalogInvalid:
		c.labeled.add("a2ui_degraded_text_total", nil, 1)
	}
}

// ObserveA2UICatalogInvalid records one surface rejected by catalog validation.
// This is the signal prompt work is tuned against: a sustained non-zero rate
// means the model and the catalog disagree.
func (c *AAPCollector) ObserveA2UICatalogInvalid(reason, keyword string) {
	if c == nil {
		return
	}
	c.labeled.add("a2ui_catalog_invalid_total", map[string]string{
		"reason":  normalizeA2UIReason(reason),
		"keyword": normalizeA2UIInvalidKeyword(keyword),
	}, 1)
}

// ObserveA2UIChartEmitted records one chart in a persisted surface, so chart
// adoption is measurable per type.
func (c *AAPCollector) ObserveA2UIChartEmitted(chartType string) {
	if c == nil {
		return
	}
	label := "unknown"
	if _, known := a2uiChartTypes[chartType]; known {
		label = chartType
	}
	c.labeled.add("a2ui_chart_emitted_total", map[string]string{"chart_type": label}, 1)
}

func normalizeA2UIResult(result string) string {
	switch result {
	case A2UIResultNone, A2UIResultOK, A2UIResultOKEmptyText, A2UIResultInvalidJSON,
		A2UIResultTooLarge, A2UIResultStrippedDisabled, A2UIResultTruncated,
		A2UIResultProjectionRejected, A2UIResultProjectionOff, A2UIResultCatalogInvalid:
		return result
	default:
		return "unknown"
	}
}

func normalizeA2UIReason(reason string) string {
	switch reason {
	case A2UIReasonSchema, A2UIReasonGraph, A2UIReasonChartSemantics, A2UIReasonUnknownCatalog:
		return reason
	default:
		return "unknown"
	}
}

func normalizeA2UIInvalidKeyword(keyword string) string {
	if _, known := a2uiInvalidKeywords[keyword]; known {
		return keyword
	}
	return "unknown"
}
