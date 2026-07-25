package logging

import (
	"log/slog"
	"strings"
)

// AAP log field allowlist — only index identifiers and stable codes.
// Never log message bodies, tool payloads, secrets, or resume tokens.
var aapAllowedLogFields = map[string]struct{}{
	"event":                {},
	"request_id":           {},
	"trace_id":             {},
	"workspace_id":         {},
	"agent_id":             {},
	"client_id":            {},
	"principal_id":         {},
	"service_principal_id": {},
	"authorized_party":     {},
	"run_id":               {},
	"conversation_id":      {},
	"event_id":             {},
	"interaction_id":       {},
	"confirmation_id":      {},
	"stream_id":            {},
	"error_code":           {},
	"result":               {},
	"operation":            {},
	"reason":               {},
	"scope":                {},
	"method":               {},
	"path":                 {},
	"route":                {},
	"status":               {},
	"duration_ms":          {},
	"sequence":             {},
	"component":            {},
}

// Forbidden substrings in log field names (defense in depth).
var aapForbiddenFieldSubstrings = []string{
	"password", "secret", "token", "authorization", "cookie",
	"resume", "payload", "prompt", "body", "raw", "assertion",
}

// AAPAttrs filters attrs down to the AAP index allowlist. Unknown keys are dropped
// rather than logged, so callers cannot accidentally ship secret-bearing fields.
func AAPAttrs(attrs ...any) []any {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]any, 0, len(attrs))
	for i := 0; i < len(attrs); {
		switch value := attrs[i].(type) {
		case slog.Attr:
			if allowedAAPField(value.Key) {
				out = append(out, value.Key, sanitizeAAPValue(value.Value.Any()))
			}
			i++
		case string:
			if i+1 >= len(attrs) {
				i++
				continue
			}
			if allowedAAPField(value) {
				out = append(out, value, sanitizeAAPValue(attrs[i+1]))
			}
			i += 2
		default:
			// Skip non-key positions.
			i++
		}
	}
	return out
}

// AAPInfo logs an AAP operational event with allowlisted fields only.
func AAPInfo(logger *slog.Logger, msg string, attrs ...any) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info(msg, AAPAttrs(attrs...)...)
}

// AAPWarn logs an AAP warning with allowlisted fields only.
func AAPWarn(logger *slog.Logger, msg string, attrs ...any) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn(msg, AAPAttrs(attrs...)...)
}

// AAPError logs an AAP error with allowlisted fields only.
// The error value is reduced to a stable type/code when possible.
func AAPError(logger *slog.Logger, msg string, err error, attrs ...any) {
	if logger == nil {
		logger = slog.Default()
	}
	fields := AAPAttrs(attrs...)
	if err != nil {
		// Prefer error_code from attrs; never attach full err.Error() if it might
		// contain secrets — use a truncated stable string only when short.
		msgText := err.Error()
		if len(msgText) > 120 || looksSensitive(msgText) {
			fields = append(fields, "error_code", "INTERNAL_ERROR")
		} else {
			fields = append(fields, "error_code", msgText)
		}
	}
	logger.Error(msg, fields...)
}

func allowedAAPField(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	if _, ok := aapAllowedLogFields[normalized]; ok {
		return true
	}
	// slog groups use dots; allow group prefixes for known roots.
	if strings.Contains(normalized, ".") {
		root := strings.SplitN(normalized, ".", 2)[0]
		if _, ok := aapAllowedLogFields[root]; ok {
			return true
		}
	}
	for _, forbidden := range aapForbiddenFieldSubstrings {
		if strings.Contains(normalized, forbidden) {
			return false
		}
	}
	return false
}

func sanitizeAAPValue(value any) any {
	switch typed := value.(type) {
	case string:
		if looksSensitive(typed) || len(typed) > 256 {
			return "[redacted]"
		}
		return typed
	default:
		return value
	}
}

func looksSensitive(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"bearer ", "awsk_", "eyj", "-----begin", "password=", "secret=",
		"authorization:", "cookie:",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
