package smartdag

import (
	"errors"
	"fmt"
	"strings"
)

// FailureStage classifies where a generate-session failure occurred.
// Not a new DB column; derived for HTTP/GET from stable error codes (ZKL-56 §4.4.1).
type FailureStage string

const (
	FailureStageSession      FailureStage = "SESSION"
	FailureStageModelCall    FailureStage = "MODEL_CALL"
	FailureStageOutputParse  FailureStage = "OUTPUT_PARSE"
	FailureStageGuard        FailureStage = "GUARD"
	FailureStageDraftPersist FailureStage = "DRAFT_PERSIST"
	FailureStageUnknown      FailureStage = "UNKNOWN"
)

// Stable public error codes (ZKL-56 tech design §6.2).
const (
	CodeSessionClosed              = "SESSION_CLOSED"
	CodeTurnInProgress             = "SMART_DAG_TURN_IN_PROGRESS"
	CodeSessionVersionConflict     = "SMART_DAG_SESSION_VERSION_CONFLICT"
	CodeAgentModelRequired         = "AGENT_MODEL_REQUIRED"
	CodeModelTimeout               = "SMART_DAG_MODEL_TIMEOUT"
	CodeModelUnavailable           = "SMART_DAG_MODEL_UNAVAILABLE"
	CodeOutputInvalid              = "SMART_DAG_OUTPUT_INVALID"
	CodeGuardRejected              = "GUARD_REJECTED"
	CodeDraftConflict              = "SMART_DAG_DRAFT_CONFLICT"
	CodeDraftPersistFailed         = "SMART_DAG_DRAFT_PERSIST_FAILED"
	CodeUnknownFailure             = "SMART_DAG_UNKNOWN_FAILURE"
	// Historical generic turn failure persisted before ZKL-56; GET maps UNKNOWN/false.
	CodeHistoricalFailed = "FAILED"
)

// Domain errors for smart-dag.v2 generate path (D2 / D3 / D15 / ZKL-56).
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

	// ErrTurnInProgress means another turn/close holds the session advisory lock.
	ErrTurnInProgress = errors.New("workflow generate session turn is already in progress")

	// ErrSessionVersionConflict means expectedSessionLockVersion did not match.
	ErrSessionVersionConflict = errors.New("workflow generate session version conflict")

	// ErrModelTimeout means the model call exceeded its deadline.
	ErrModelTimeout = errors.New("smart dag model call timed out")

	// ErrModelUnavailable means the model provider was unavailable.
	ErrModelUnavailable = errors.New("smart dag model is unavailable")

	// ErrOutputInvalid means the model output could not be parsed into a graph.
	ErrOutputInvalid = errors.New("smart dag model output is invalid")

	// ErrDraftConflict means Draft CAS lost (user edited during generation).
	ErrDraftConflict = errors.New("workflow draft version conflict during smart dag persist")

	// ErrDraftPersistFailed means Draft/Turn unit-of-work failed for infrastructure reasons.
	ErrDraftPersistFailed = errors.New("smart dag draft persist failed")
)

// TurnFailure is the typed failure surface for generate turns (no new DB columns).
// Internal Cause is for logs only and must never enter public ErrorDTO.
type TurnFailure struct {
	Stage     FailureStage
	Code      string
	Retryable bool
	Message   string // safe public message
	Cause     error  // internal only
}

func (e *TurnFailure) Error() string {
	if e == nil {
		return CodeUnknownFailure
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Cause)
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return e.Code
}

func (e *TurnFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Is allows errors.Is(err, ErrModelTimeout) style checks via stable Code.
func (e *TurnFailure) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	sent := sentinelForCode(e.Code)
	return sent != nil && errors.Is(sent, target)
}

// NewTurnFailure builds a TurnFailure using the approved §6.2 table defaults.
func NewTurnFailure(code string, cause error) *TurnFailure {
	meta := classifyCode(code)
	return &TurnFailure{
		Stage:     meta.Stage,
		Code:      meta.Code,
		Retryable: meta.Retryable,
		Message:   meta.Message,
		Cause:     cause,
	}
}

// failureMeta is the public projection of one stable code.
type failureMeta struct {
	Code      string
	Stage     FailureStage
	Retryable bool
	Message   string
	// HTTPStatus is used by transport mapping (0 = use default path).
	HTTPStatus int
}

// classifyCode implements tech design §6.2. Unknown / empty / historical FAILED
// map to UNKNOWN/false without backfill.
func classifyCode(code string) failureMeta {
	switch strings.TrimSpace(code) {
	case CodeSessionClosed:
		return failureMeta{
			Code: CodeSessionClosed, Stage: FailureStageSession, Retryable: false,
			Message: "会话已关闭，请新建会话后继续。", HTTPStatus: 409,
		}
	case CodeTurnInProgress:
		return failureMeta{
			Code: CodeTurnInProgress, Stage: FailureStageSession, Retryable: false,
			Message: "当前会话正在生成中，请稍后加载会话状态。", HTTPStatus: 409,
		}
	case CodeSessionVersionConflict:
		return failureMeta{
			Code: CodeSessionVersionConflict, Stage: FailureStageSession, Retryable: true,
			Message: "会话版本已变化，请先刷新会话再显式重试。", HTTPStatus: 409,
		}
	case CodeAgentModelRequired:
		return failureMeta{
			Code: CodeAgentModelRequired, Stage: FailureStageModelCall, Retryable: false,
			Message: "智能体未配置可用模型，请先修复 Agent 模型绑定。", HTTPStatus: 422,
		}
	case CodeModelTimeout:
		return failureMeta{
			Code: CodeModelTimeout, Stage: FailureStageModelCall, Retryable: true,
			Message: "模型生成超时，本轮未修改草稿。", HTTPStatus: 504,
		}
	case CodeModelUnavailable:
		return failureMeta{
			Code: CodeModelUnavailable, Stage: FailureStageModelCall, Retryable: true,
			Message: "模型服务暂不可用，本轮未修改草稿。", HTTPStatus: 503,
		}
	case CodeOutputInvalid:
		return failureMeta{
			Code: CodeOutputInvalid, Stage: FailureStageOutputParse, Retryable: true,
			Message: "模型输出无法解析为工作流图，本轮未修改草稿。", HTTPStatus: 422,
		}
	case CodeGuardRejected:
		return failureMeta{
			Code: CodeGuardRejected, Stage: FailureStageGuard, Retryable: true,
			Message: "生成图未通过校验，已保留上一合法草稿。", HTTPStatus: 422,
		}
	case CodeDraftConflict:
		return failureMeta{
			Code: CodeDraftConflict, Stage: FailureStageDraftPersist, Retryable: true,
			Message: "草稿版本冲突，本轮未覆盖现有草稿。", HTTPStatus: 409,
		}
	case CodeDraftPersistFailed:
		return failureMeta{
			Code: CodeDraftPersistFailed, Stage: FailureStageDraftPersist, Retryable: true,
			Message: "草稿保存失败，本轮未修改草稿。", HTTPStatus: 503,
		}
	case CodeHistoricalFailed, "":
		// Historical generic FAILED or empty: UNKNOWN / non-retryable; no backfill.
		return failureMeta{
			Code: firstNonEmptyCode(code, CodeHistoricalFailed), Stage: FailureStageUnknown, Retryable: false,
			Message: "生成失败，本轮未修改草稿。", HTTPStatus: 500,
		}
	case CodeUnknownFailure:
		return failureMeta{
			Code: CodeUnknownFailure, Stage: FailureStageUnknown, Retryable: false,
			Message: "生成失败，本轮未修改草稿。", HTTPStatus: 500,
		}
	default:
		// Unknown stable-looking codes fail closed as UNKNOWN for GET derivation.
		return failureMeta{
			Code: CodeUnknownFailure, Stage: FailureStageUnknown, Retryable: false,
			Message: "生成失败，本轮未修改草稿。", HTTPStatus: 500,
		}
	}
}

// ClassifyTurnErrorCode projects a persisted turn error_code to stage/retryable.
// Used by GET Session/Turns; never mutates history rows.
func ClassifyTurnErrorCode(errorCode string) (stage FailureStage, retryable bool, code string) {
	meta := classifyCode(errorCode)
	// Preserve historical FAILED code string on GET when that was stored.
	if strings.TrimSpace(errorCode) == CodeHistoricalFailed {
		return FailureStageUnknown, false, CodeHistoricalFailed
	}
	if strings.TrimSpace(errorCode) == "" {
		return FailureStageUnknown, false, ""
	}
	// Known codes return themselves; unknown maps to UNKNOWN failure code.
	if meta.Code == CodeUnknownFailure && strings.TrimSpace(errorCode) != CodeUnknownFailure &&
		strings.TrimSpace(errorCode) != CodeHistoricalFailed && strings.TrimSpace(errorCode) != "" {
		// Unrecognized non-empty code still surfaces as UNKNOWN stage/false but
		// keep original for debugging display without inventing new states.
		return FailureStageUnknown, false, strings.TrimSpace(errorCode)
	}
	return meta.Stage, meta.Retryable, meta.Code
}

// AsTurnFailure extracts a TurnFailure from err, or classifies known sentinels.
func AsTurnFailure(err error) (*TurnFailure, bool) {
	if err == nil {
		return nil, false
	}
	var tf *TurnFailure
	if errors.As(err, &tf) && tf != nil {
		return tf, true
	}
	switch {
	case errors.Is(err, ErrSessionClosed):
		return NewTurnFailure(CodeSessionClosed, err), true
	case errors.Is(err, ErrTurnInProgress):
		return NewTurnFailure(CodeTurnInProgress, err), true
	case errors.Is(err, ErrSessionVersionConflict):
		return NewTurnFailure(CodeSessionVersionConflict, err), true
	case errors.Is(err, ErrAgentModelRequired):
		return NewTurnFailure(CodeAgentModelRequired, err), true
	case errors.Is(err, ErrModelTimeout):
		return NewTurnFailure(CodeModelTimeout, err), true
	case errors.Is(err, ErrModelUnavailable):
		return NewTurnFailure(CodeModelUnavailable, err), true
	case errors.Is(err, ErrOutputInvalid):
		return NewTurnFailure(CodeOutputInvalid, err), true
	case errors.Is(err, ErrGuardRejected):
		return NewTurnFailure(CodeGuardRejected, err), true
	case errors.Is(err, ErrDraftConflict):
		return NewTurnFailure(CodeDraftConflict, err), true
	case errors.Is(err, ErrDraftPersistFailed):
		return NewTurnFailure(CodeDraftPersistFailed, err), true
	default:
		return nil, false
	}
}

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

func sentinelForCode(code string) error {
	switch strings.TrimSpace(code) {
	case CodeSessionClosed:
		return ErrSessionClosed
	case CodeTurnInProgress:
		return ErrTurnInProgress
	case CodeSessionVersionConflict:
		return ErrSessionVersionConflict
	case CodeAgentModelRequired:
		return ErrAgentModelRequired
	case CodeModelTimeout:
		return ErrModelTimeout
	case CodeModelUnavailable:
		return ErrModelUnavailable
	case CodeOutputInvalid:
		return ErrOutputInvalid
	case CodeGuardRejected:
		return ErrGuardRejected
	case CodeDraftConflict:
		return ErrDraftConflict
	case CodeDraftPersistFailed:
		return ErrDraftPersistFailed
	default:
		return nil
	}
}

func firstNonEmptyCode(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return CodeUnknownFailure
}
