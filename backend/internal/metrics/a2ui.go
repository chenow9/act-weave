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
)

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
	}
}

func normalizeA2UIResult(result string) string {
	switch result {
	case A2UIResultNone, A2UIResultOK, A2UIResultOKEmptyText, A2UIResultInvalidJSON,
		A2UIResultTooLarge, A2UIResultStrippedDisabled, A2UIResultTruncated,
		A2UIResultProjectionRejected, A2UIResultProjectionOff:
		return result
	default:
		return "unknown"
	}
}
