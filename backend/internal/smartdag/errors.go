package smartdag

import (
	"errors"
	"fmt"
)

// Domain errors for smart-dag.v2 generate path (D2 / D3 / D15).
var (
	// ErrAgentModelRequired means the bound Agent has no usable modelConfig.
	// Transport maps this to HTTP 422 + code AGENT_MODEL_REQUIRED; no Draft write.
	ErrAgentModelRequired = errors.New("agent model is required for smart orchestration generation")

	// ErrAgentNotInWorkspace means agentId is missing or not in the request workspace.
	ErrAgentNotInWorkspace = errors.New("agent is not available in this workspace")

	// ErrModelConfigBypassRejected means a request tried to supply modelConfigId
	// outside the Agent binding (D2: no request-body model bypass).
	ErrModelConfigBypassRejected = errors.New("modelConfigId must not be supplied on generate requests")

	// ErrGuardRejected means the LLM graph failed deterministic guard.
	// Prior good Draft must not be clobbered (D3).
	ErrGuardRejected = errors.New("generated workflow graph failed guard")

	// ErrSessionNotFound means the generate session is missing or not in workspace.
	ErrSessionNotFound = errors.New("workflow generate session not found")

	// ErrSessionClosed means turns are rejected after POST ...:close (HTTP 409).
	ErrSessionClosed = errors.New("workflow generate session is closed")
)

// GuardError wraps ErrGuardRejected with a structured GuardReport.
type GuardError struct {
	Report GuardReport
}

func (e *GuardError) Error() string {
	if e == nil {
		return ErrGuardRejected.Error()
	}
	if e.Report.OK {
		return ErrGuardRejected.Error()
	}
	if len(e.Report.Violations) == 0 {
		return ErrGuardRejected.Error()
	}
	return fmt.Sprintf("%s: %s", ErrGuardRejected.Error(), e.Report.Violations[0].Code)
}

func (e *GuardError) Unwrap() error {
	return ErrGuardRejected
}

// NewGuardError builds a GuardError from a failed report.
func NewGuardError(report GuardReport) *GuardError {
	report.OK = false
	return &GuardError{Report: report}
}
