package httptransport

import (
	"errors"
	"net/http"
	"testing"

	"actweave/backend/internal/smartdag"
)

func TestMapErrorAgentModelRequired(t *testing.T) {
	t.Parallel()
	mapped := mapError(smartdag.ErrAgentModelRequired)
	if mapped.status != http.StatusUnprocessableEntity || mapped.code != "AGENT_MODEL_REQUIRED" {
		t.Fatalf("mapError(ErrAgentModelRequired)=%+v", mapped)
	}
	// Wrapped should still map.
	mapped = mapError(errors.Join(errors.New("wrap"), smartdag.ErrAgentModelRequired))
	if mapped.code != "AGENT_MODEL_REQUIRED" {
		t.Fatalf("wrapped AGENT_MODEL_REQUIRED got code %q", mapped.code)
	}
}

func TestMapErrorGuardRejected(t *testing.T) {
	t.Parallel()
	report := smartdag.GuardReport{
		OK: false,
		Violations: []smartdag.GuardViolation{
			{Code: "HALLUCINATED_TOOL_ID", Message: "bad tool"},
		},
	}
	mapped := mapError(smartdag.NewGuardError(report))
	if mapped.status != http.StatusUnprocessableEntity || mapped.code != "GUARD_REJECTED" {
		t.Fatalf("mapError(GuardError)=%+v", mapped)
	}
}

func TestMapErrorModelConfigBypass(t *testing.T) {
	t.Parallel()
	mapped := mapError(smartdag.ErrModelConfigBypassRejected)
	if mapped.status != http.StatusUnprocessableEntity || mapped.code != "VALIDATION_ERROR" {
		t.Fatalf("mapError(bypass)=%+v", mapped)
	}
}

func TestMapErrorAgentNotInWorkspace(t *testing.T) {
	t.Parallel()
	mapped := mapError(smartdag.ErrAgentNotInWorkspace)
	if mapped.status != http.StatusNotFound || mapped.code != "NOT_FOUND" {
		t.Fatalf("mapError(agent not in workspace)=%+v", mapped)
	}
}

func TestMapErrorSessionClosed(t *testing.T) {
	t.Parallel()
	mapped := mapError(smartdag.ErrSessionClosed)
	if mapped.status != http.StatusConflict || mapped.code != "SESSION_CLOSED" {
		t.Fatalf("mapError(session closed)=%+v", mapped)
	}
}

func TestMapErrorSessionNotFound(t *testing.T) {
	t.Parallel()
	mapped := mapError(smartdag.ErrSessionNotFound)
	if mapped.status != http.StatusNotFound || mapped.code != "NOT_FOUND" {
		t.Fatalf("mapError(session not found)=%+v", mapped)
	}
}
