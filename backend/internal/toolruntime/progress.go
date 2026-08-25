package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/execution"
)

const (
	progressModePoll         = "poll"
	defaultProgressInterval  = 2 * time.Second
	minProgressInterval      = 200 * time.Millisecond
	maxProgressInterval      = 10 * time.Second
	progressErrorPollFailed  = "PROGRESS_POLL_FAILED"
	progressErrorPollTimeout = "PROGRESS_POLL_TIMEOUT"
	progressErrorPollInvalid = "PROGRESS_POLL_INVALID"
)

var doneWhenExpr = regexp.MustCompile(
	`^\s*(\$?\.?[\w.]+)\s*==\s*(?:'([^']*)'|"([^"]*)"|(\S+))\s*$`,
)

type httpProgressConfig struct {
	Mode        string `json:"mode"`
	Path        string `json:"path"`
	Method      string `json:"method,omitempty"`
	IntervalMS  int    `json:"intervalMs,omitempty"`
	DoneWhen    string `json:"doneWhen"`
	PercentPath string `json:"percentPath,omitempty"`
	MessagePath string `json:"messagePath,omitempty"`
	FailClosed  bool   `json:"failClosed,omitempty"`
}

type resolvedProgressSpec struct {
	Path        string
	Method      string
	Interval    time.Duration
	DonePath    string
	DoneEquals  string
	PercentPath string
	MessagePath string
	FailClosed  bool
}

func (action httpActionConfig) progressConfig() *httpProgressConfig {
	if action.Progress != nil {
		return action.Progress
	}
	return action.XProgress
}

func parseHTTPProgressSpec(action httpActionConfig) (*resolvedProgressSpec, error) {
	raw := action.progressConfig()
	if raw == nil {
		return nil, nil
	}
	mode := strings.ToLower(strings.TrimSpace(raw.Mode))
	path := strings.TrimSpace(raw.Path)
	if mode == "" && path == "" && strings.TrimSpace(raw.DoneWhen) == "" {
		return nil, nil
	}
	if mode != progressModePoll {
		return nil, execution.NewError(execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, nil)
	}
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, execution.NewError(execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, nil)
	}
	method := strings.ToUpper(strings.TrimSpace(raw.Method))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead {
		return nil, execution.NewError(execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, nil)
	}
	pred, ok := parseDoneWhen(raw.DoneWhen)
	if !ok {
		return nil, execution.NewError(execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, nil)
	}
	interval := defaultProgressInterval
	if raw.IntervalMS > 0 {
		interval = time.Duration(raw.IntervalMS) * time.Millisecond
	}
	if interval < minProgressInterval {
		interval = minProgressInterval
	}
	if interval > maxProgressInterval {
		interval = maxProgressInterval
	}
	return &resolvedProgressSpec{
		Path:        path,
		Method:      method,
		Interval:    interval,
		DonePath:    pred.path,
		DoneEquals:  pred.equals,
		PercentPath: normalizeJSONPath(raw.PercentPath),
		MessagePath: normalizeJSONPath(raw.MessagePath),
		FailClosed:  raw.FailClosed,
	}, nil
}

type doneWhenPred struct {
	path   string
	equals string
}

func parseDoneWhen(raw string) (doneWhenPred, bool) {
	match := doneWhenExpr.FindStringSubmatch(raw)
	if match == nil {
		return doneWhenPred{}, false
	}
	equals := match[2]
	if equals == "" {
		equals = match[3]
	}
	if equals == "" {
		equals = match[4]
	}
	path := normalizeJSONPath(match[1])
	if path == "" || equals == "" {
		return doneWhenPred{}, false
	}
	return doneWhenPred{path: path, equals: equals}, true
}

func normalizeJSONPath(raw string) string {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "$.")
	value = strings.TrimPrefix(value, ".")
	return value
}

func decodeJSONObject(raw json.RawMessage) (map[string]any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, false
	}
	return object, true
}

func overlayJSONObject(base map[string]any, raw json.RawMessage) map[string]any {
	out := make(map[string]any, len(base)+4)
	for key, value := range base {
		out[key] = value
	}
	extra, ok := decodeJSONObject(raw)
	if !ok {
		return out
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func readJSONPathValue(document map[string]any, path string) (any, bool) {
	path = normalizeJSONPath(path)
	if path == "" || document == nil {
		return nil, false
	}
	var current any = document
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			return nil, false
		}
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, exists := object[part]
		if !exists {
			return nil, false
		}
		current = next
	}
	return current, true
}

func jsonValueEquals(value any, want string) bool {
	switch typed := value.(type) {
	case nil:
		return strings.EqualFold(want, "null")
	case bool:
		return strconv.FormatBool(typed) == strings.ToLower(want)
	case float64:
		return stringifyJSONNumber(typed) == want
	case json.Number:
		return typed.String() == want
	case string:
		return typed == want
	default:
		return fmt.Sprint(value) == want
	}
}

func stringifyJSONNumber(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func doneWhenSatisfied(document map[string]any, spec *resolvedProgressSpec) bool {
	if spec == nil || document == nil {
		return false
	}
	value, ok := readJSONPathValue(document, spec.DonePath)
	if !ok {
		return false
	}
	return jsonValueEquals(value, spec.DoneEquals)
}

func progressFromDocument(document map[string]any, spec *resolvedProgressSpec) (current float64, total *float64, unit, message string) {
	unit = "polls"
	if spec.PercentPath != "" {
		unit = "percent"
		totalVal := 100.0
		total = &totalVal
		if value, ok := readJSONPathValue(document, spec.PercentPath); ok {
			current = jsonNumber(value)
		}
	}
	if spec.MessagePath != "" {
		if value, ok := readJSONPathValue(document, spec.MessagePath); ok {
			message = strings.TrimSpace(fmt.Sprint(value))
		}
	}
	return current, total, unit, message
}

func jsonNumber(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed
	default:
		return 0
	}
}

var progressSleep = func(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func progressFailureCode(err error) string {
	if err == nil {
		return progressErrorPollFailed
	}
	if execution.ErrorCode(err) == execution.ErrorCodeTimeout ||
		execution.ErrorCode(err) == execution.ErrorCodeCanceled {
		return progressErrorPollTimeout
	}
	if execution.ErrorCode(err) == execution.ErrorCodeInvalidSnapshot ||
		execution.ErrorCode(err) == execution.ErrorCodeInvalidRequest {
		return progressErrorPollInvalid
	}
	return progressErrorPollFailed
}
