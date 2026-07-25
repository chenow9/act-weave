package httptransport

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"actweave/backend/internal/smartdag"

	"github.com/gin-gonic/gin"
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

func TestMapErrorSmartDagStableCodes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{smartdag.ErrTurnInProgress, http.StatusConflict, smartdag.CodeTurnInProgress},
		{smartdag.ErrSessionVersionConflict, http.StatusConflict, smartdag.CodeSessionVersionConflict},
		{smartdag.ErrModelTimeout, http.StatusGatewayTimeout, smartdag.CodeModelTimeout},
		{smartdag.ErrModelUnavailable, http.StatusServiceUnavailable, smartdag.CodeModelUnavailable},
		{smartdag.ErrOutputInvalid, http.StatusUnprocessableEntity, smartdag.CodeOutputInvalid},
		{smartdag.ErrDraftConflict, http.StatusConflict, smartdag.CodeDraftConflict},
		{smartdag.ErrDraftPersistFailed, http.StatusServiceUnavailable, smartdag.CodeDraftPersistFailed},
		{smartdag.NewTurnFailure(smartdag.CodeUnknownFailure, errors.New("internal boom")), http.StatusInternalServerError, smartdag.CodeUnknownFailure},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()
			mapped := mapError(tc.err)
			if mapped.status != tc.status || mapped.code != tc.code {
				t.Fatalf("mapError=%+v want status=%d code=%s", mapped, tc.status, tc.code)
			}
			// Public message must not leak internal cause text.
			if mapped.message == "internal boom" {
				t.Fatalf("leaked internal cause in message: %q", mapped.message)
			}
		})
	}
}

func TestMappedRetryableSmartDag(t *testing.T) {
	t.Parallel()
	if !mappedRetryable(mappedError{status: 409, code: smartdag.CodeSessionVersionConflict}) {
		t.Fatal("SESSION_VERSION_CONFLICT should be retryable")
	}
	if mappedRetryable(mappedError{status: 409, code: smartdag.CodeTurnInProgress}) {
		t.Fatal("TURN_IN_PROGRESS should not be retryable")
	}
	if !mappedRetryable(mappedError{status: 422, code: smartdag.CodeGuardRejected}) {
		t.Fatal("GUARD_REJECTED should be retryable")
	}
	if mappedRetryable(mappedError{status: 500, code: smartdag.CodeUnknownFailure}) {
		t.Fatal("UNKNOWN_FAILURE should not be retryable")
	}
}

func TestRespondSmartDagTurnError_IncludesDetailAndLegacyGuard(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)

	report := smartdag.GuardReport{OK: false, Violations: []smartdag.GuardViolation{{Code: "X", Message: "m"}}}
	RespondSmartDagTurnError(c, smartdag.NewGuardError(report), smartDagTurnErrorContext{
		SessionID: "sess-1", TurnID: "turn-1", GenerationID: "gen-1",
		AgentID: "agent-1", TraceID: "trace-1", GuardReport: &report,
		SessionStatus: "OPEN",
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// Legacy top-level fields.
	if body["sessionId"] != "sess-1" || body["turnId"] != "turn-1" || body["generationId"] != "gen-1" {
		t.Fatalf("legacy fields missing: %v", body)
	}
	if _, ok := body["guardReport"]; !ok {
		t.Fatal("legacy guardReport missing")
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != smartdag.CodeGuardRejected {
		t.Fatalf("code=%v", errObj["code"])
	}
	if errObj["retryable"] != true {
		t.Fatalf("retryable=%v", errObj["retryable"])
	}
	details, _ := errObj["details"].([]any)
	if len(details) != 1 {
		t.Fatalf("details=%v", details)
	}
	d0, _ := details[0].(map[string]any)
	if d0["kind"] != "SMART_DAG_TURN_FAILURE" || d0["stage"] != "GUARD" {
		t.Fatalf("detail=%v", d0)
	}
}

func TestGenerateTurnDTO_DerivesFailureStage(t *testing.T) {
	t.Parallel()
	// Historical FAILED → UNKNOWN/false
	dto := generateTurnDTOFor(smartdag.GenerateTurn{
		ID: "t1", Status: smartdag.TurnStatusFailed, ErrorCode: "FAILED",
	})
	if dto.FailureStage == nil || *dto.FailureStage != "UNKNOWN" {
		t.Fatalf("historical FAILED stage=%v", dto.FailureStage)
	}
	if dto.Retryable == nil || *dto.Retryable {
		t.Fatalf("historical FAILED retryable=%v", dto.Retryable)
	}
	// GUARD_REJECTED → GUARD/true
	dto = generateTurnDTOFor(smartdag.GenerateTurn{
		ID: "t2", Status: smartdag.TurnStatusGuardRejected, ErrorCode: "GUARD_REJECTED",
	})
	if dto.FailureStage == nil || *dto.FailureStage != "GUARD" || dto.Retryable == nil || !*dto.Retryable {
		t.Fatalf("guard dto stage=%v retryable=%v", dto.FailureStage, dto.Retryable)
	}
	// Success has no failure projection.
	dto = generateTurnDTOFor(smartdag.GenerateTurn{
		ID: "t3", Status: smartdag.TurnStatusSucceeded,
	})
	if dto.FailureStage != nil || dto.Retryable != nil {
		t.Fatalf("success should omit failure fields")
	}
}
