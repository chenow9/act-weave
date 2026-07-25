package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNewSupportsHumanReadableTextLogs(t *testing.T) {
	var output bytes.Buffer
	logger := New(Config{Level: "info", Format: "text", Writer: &output})
	logger.Info("request completed", "request_id", "request-1", "status", 200)

	line := output.String()
	if json.Valid(output.Bytes()) {
		t.Fatalf("text log must not be JSON: %s", line)
	}
	for _, expected := range []string{
		"INFO  request completed request_id=request-1 status=200 source=internal/logging/logger_test.go:",
	} {
		if !strings.Contains(line, expected) {
			t.Fatalf("text log missing %q: %s", expected, line)
		}
	}
}

func TestPrettyHandlerKeepsFieldsInlineAndIndentsStack(t *testing.T) {
	var output bytes.Buffer
	logger := New(Config{Level: "info", Format: "text", Writer: &output}).With("component", "http")
	logger.Info("request failed",
		"event", "http.request.failed",
		slog.Group("request", "method", "POST", "status", 500),
		"error", "first line\nextra detail",
		"stack", "first line\nsecond line\n",
	)

	line := output.String()
	for _, expected := range []string{
		`INFO  [http] request failed event=http.request.failed request.method=POST request.status=500 error="first line\nextra detail"`,
		"stack:\n    first line\n    second line\n",
	} {
		if !strings.Contains(line, expected) {
			t.Fatalf("pretty log missing %q:\n%s", expected, line)
		}
	}
}

func TestNewSupportsStructuredJSONLogs(t *testing.T) {
	var output bytes.Buffer
	logger := New(Config{Level: "info", Format: "json", Writer: &output})
	logger.Info("request completed", "request_id", "request-1", "status", 200)

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("decode JSON log: %v; output=%s", err, output.String())
	}
	if entry["level"] != "info" || entry["msg"] != "request completed" ||
		entry["request_id"] != "request-1" || entry["status"] != float64(200) || entry["source"] == nil {
		t.Fatalf("unexpected JSON log: %#v", entry)
	}
}

func TestNewDefaultsToJSONForExistingDeployments(t *testing.T) {
	var output bytes.Buffer
	New(Config{Level: "info", Writer: &output}).Info("compatible default")
	if !json.Valid(output.Bytes()) {
		t.Fatalf("empty format must preserve the previous JSON default: %s", output.String())
	}
}
