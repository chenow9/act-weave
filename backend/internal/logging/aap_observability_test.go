package logging_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"actweave/backend/internal/logging"
)

// TestAAPObservability is the M10-T4 gate for AAP structured logging:
// only index IDs/codes appear in log output; message bodies, tool payloads,
// tokens, and secrets are dropped or redacted.
func TestAAPObservability(t *testing.T) {
	t.Run("AllowlistsIndexIDsAndCodesOnly", testAAPLogAllowlist)
	t.Run("DropsAndRedactsSecretsPayloadsTokens", testAAPLogDropsSecrets)
	t.Run("ErrorPathNeverEchoesSensitiveInput", testAAPLogErrorNoEcho)
	t.Run("DashboardFieldsLocateClientAgentRun", testAAPLogDashboardFields)
}

func testAAPLogAllowlist(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(logging.Config{Level: "info", Format: "json", Writer: &buf})

	logging.AAPInfo(logger, "protocol event appended",
		"event", "aap.protocol_event.append",
		"request_id", "req-obs-1",
		"trace_id", "tr-obs-1",
		"workspace_id", "ws-obs-1",
		"agent_id", "ag-obs-1",
		"client_id", "cl-obs-1",
		"principal_id", "sp-obs-1",
		"run_id", "run-obs-1",
		"event_id", "ev-obs-1",
		"error_code", "NONE",
		"sequence", 7,
		"duration_ms", 11,
		// Should be dropped:
		"message_body", "hello user secret content",
		"tool_payload", `{"args":{"password":"x"}}`,
		"access_token", "eyJhbGciOiJIUzI1NiJ9.payload.sig",
	)

	entry := decodeJSONLog(t, buf.Bytes())
	for _, key := range []string{
		"request_id", "trace_id", "workspace_id", "agent_id", "client_id",
		"principal_id", "run_id", "event_id", "error_code", "sequence", "duration_ms",
	} {
		if entry[key] == nil {
			t.Fatalf("expected allowlisted field %q in log: %#v", key, entry)
		}
	}
	for _, forbidden := range []string{"message_body", "tool_payload", "access_token"} {
		if _, ok := entry[forbidden]; ok {
			t.Fatalf("forbidden field %q present: %#v", forbidden, entry)
		}
	}
}

func testAAPLogDropsSecrets(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(logging.Config{Level: "info", Format: "json", Writer: &buf})

	// Keys that look sensitive by substring are rejected even if not in denylist of exact names.
	logging.AAPWarn(logger, "token issue failed",
		"event", "aap.token.issue.failed",
		"client_id", "cl-obs-2",
		"error_code", "CREDENTIAL_REJECTED",
		"reason", "INVALID_CLIENT",
		"client_secret", "awsk_super_secret_value",
		"authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig",
		"resume_token", "resume-abc-should-not-log",
		"password", "hunter2",
	)

	raw := buf.String()
	lower := strings.ToLower(raw)
	for _, forbidden := range []string{
		"awsk_super_secret", "eyjhbGciOi", "resume-abc", "hunter2",
		"client_secret", "authorization", "resume_token", "password",
	} {
		if strings.Contains(lower, strings.ToLower(forbidden)) {
			t.Fatalf("log leaked %q: %s", forbidden, raw)
		}
	}
	entry := decodeJSONLog(t, buf.Bytes())
	if entry["client_id"] != "cl-obs-2" || entry["error_code"] != "CREDENTIAL_REJECTED" {
		t.Fatalf("expected index fields retained: %#v", entry)
	}
}

func testAAPLogErrorNoEcho(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(logging.Config{Level: "info", Format: "json", Writer: &buf})

	err := errors.New("verify failed for password=super-secret and Bearer eyJhbGciOiJIUzI1NiJ9.x.y")
	logging.AAPError(logger, "authorization denied", err,
		"event", "aap.authorization.denied",
		"workspace_id", "ws-obs-3",
		"client_id", "cl-obs-3",
		"run_id", "run-obs-3",
		"error_code", "AUTHORIZATION_DENIED",
		"scope", "run:create",
		"reason", "SCOPE_MISSING",
	)

	raw := buf.String()
	lower := strings.ToLower(raw)
	for _, forbidden := range []string{"super-secret", "eyjhbGciOi", "password="} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("error log echoed sensitive input %q: %s", forbidden, raw)
		}
	}
	entry := decodeJSONLog(t, buf.Bytes())
	if entry["client_id"] != "cl-obs-3" || entry["run_id"] != "run-obs-3" {
		t.Fatalf("dashboard join fields missing: %#v", entry)
	}
}

func testAAPLogDashboardFields(t *testing.T) {
	// AAPAttrs is the pure filter used by callers before custom handlers.
	filtered := logging.AAPAttrs(
		"workspace_id", "ws-d",
		"agent_id", "ag-d",
		"client_id", "cl-d",
		"run_id", "run-d",
		"request_id", "req-d",
		"error_code", "SEQUENCE_CONFLICT",
		"prompt", "should drop",
		"tool_result", "should drop",
		slog.String("event", "aap.sequence.conflict"),
	)
	joined := map[string]any{}
	for i := 0; i+1 < len(filtered); i += 2 {
		key, ok := filtered[i].(string)
		if !ok {
			continue
		}
		joined[key] = filtered[i+1]
	}
	for _, key := range []string{"workspace_id", "agent_id", "client_id", "run_id", "request_id", "error_code", "event"} {
		if joined[key] == nil {
			t.Fatalf("missing dashboard field %q in %#v", key, joined)
		}
	}
	if _, ok := joined["prompt"]; ok {
		t.Fatalf("prompt must not pass allowlist: %#v", joined)
	}
}

func decodeJSONLog(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	// Pretty/source may emit multi-line; take last non-empty JSON object line.
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	var last []byte
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) > 0 && line[0] == '{' {
			last = line
		}
	}
	if len(last) == 0 {
		t.Fatalf("no JSON log line in output: %s", raw)
	}
	var entry map[string]any
	if err := json.Unmarshal(last, &entry); err != nil {
		t.Fatalf("decode JSON log: %v; output=%s", err, raw)
	}
	return entry
}
