package httptransport

import (
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/agentaudit"
	"actweave/backend/internal/audit"
	"actweave/backend/internal/authn"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/capability"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/connection"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/identity"
	"actweave/backend/internal/listpage"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/openapiimport"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/provider"
	"actweave/backend/internal/secret"
	"actweave/backend/internal/smartdag"
	"actweave/backend/internal/storedobject"
	"actweave/backend/internal/tool"
	ssetransport "actweave/backend/internal/transport/sse"
	"actweave/backend/internal/workflow"
	"actweave/backend/internal/workspace"

	"github.com/gin-gonic/gin"
)

var (
	ErrUnauthenticated                  = errors.New("request is not authenticated")
	ErrPasswordChangeRequired           = errors.New("password change is required before continuing")
	ErrAAPFeatureDisabled               = errors.New("AAP public surface is disabled")
	ErrAAPFeatureNotEnabledForWorkspace = errors.New("AAP is not enabled for this workspace")
	ErrAAPFeatureNotEnabledForClient    = errors.New("AAP is not enabled for this client")
)

type ErrorResponse struct {
	Error ErrorDTO `json:"error"`
}

type ErrorDTO struct {
	Code      string           `json:"code"`
	Message   string           `json:"message"`
	RequestID string           `json:"requestId"`
	TraceID   string           `json:"traceId"`
	Retryable bool             `json:"retryable"`
	Details   []map[string]any `json:"details"`
}

type mappedError struct {
	status  int
	code    string
	message string
}

const requestFailureKey = "actweave.request_failure"

// requestFailure holds only stable, low-sensitivity diagnostics for completion
// logs. It must never retain the raw error value or any err.Error() text.
type requestFailure struct {
	mapped    mappedError
	errorType string
	file      string
	line      int
}

func newRequestFailure(err error, mapped mappedError, file string, line int) requestFailure {
	return requestFailure{
		mapped:    mapped,
		errorType: fmt.Sprintf("%T", err),
		file:      file,
		line:      line,
	}
}

func RespondError(c *gin.Context, err error) {
	mapped := mapError(err)
	if isAAPRequest(c) {
		mapped = mapAAPError(err)
	}
	_, file, line, _ := runtime.Caller(1)
	c.Set(requestFailureKey, newRequestFailure(err, mapped, file, line))
	request, _ := RequestContextFrom(c.Request.Context())
	retryable := mappedRetryable(mapped)
	details := []map[string]any{}
	// Prefer TurnFailure public message when present (never include Cause).
	if tf, ok := smartdag.AsTurnFailure(err); ok && tf != nil {
		if strings.TrimSpace(tf.Message) != "" {
			mapped.message = tf.Message
		}
		retryable = tf.Retryable
		if mapped.code == "" {
			mapped.code = tf.Code
		}
	}
	c.AbortWithStatusJSON(mapped.status, ErrorResponse{Error: ErrorDTO{
		Code: mapped.code, Message: mapped.message,
		RequestID: request.RequestID, TraceID: request.TraceID,
		Retryable: retryable, Details: details,
	}})
}

func mappedRetryable(mapped mappedError) bool {
	// Domain §10 retryable overrides for statuses that are not globally retryable
	// (notably 409 OUTBOUND_CREDENTIAL_EXPIRED → retry with a new Run).
	switch mapped.code {
	case outboundidentity.CodeCredentialExpired,
		outboundidentity.CodeCredentialCapacityExceeded,
		outboundidentity.CodeBrokerUnavailable:
		return true
	// ZKL-56 §6.2 Smart DAG: retryable independent of HTTP status class.
	case smartdag.CodeSessionVersionConflict,
		smartdag.CodeModelTimeout,
		smartdag.CodeModelUnavailable,
		smartdag.CodeOutputInvalid,
		smartdag.CodeGuardRejected,
		smartdag.CodeDraftConflict,
		smartdag.CodeDraftPersistFailed:
		return true
	case smartdag.CodeSessionClosed,
		smartdag.CodeTurnInProgress,
		smartdag.CodeAgentModelRequired,
		smartdag.CodeUnknownFailure,
		"OPENAPI_IMPORT_INCOMPLETE":
		return false
	}
	return isRetryableHTTPStatus(mapped.status)
}

// mapAAPError is the public Agent Access Protocol error vocabulary. It is kept
// separate from the management API mapper so neither surface accidentally
// inherits the other's stable codes or resource-visibility semantics.
func mapAAPError(err error) mappedError {
	if mapped, ok := mapOutboundIdentityError(err); ok {
		return mapped
	}
	switch {
	case errors.Is(err, ErrAAPProtocolVersionUnsupported):
		return mappedError{http.StatusBadRequest, "PROTOCOL_VERSION_UNSUPPORTED", "The requested protocol version is not supported."}
	case errors.Is(err, agentaccessauth.ErrTokenExpired):
		return mappedError{http.StatusUnauthorized, "TOKEN_EXPIRED", "The access token has expired."}
	case errors.Is(err, ErrUnauthenticated), errors.Is(err, authn.ErrInvalidCredentials),
		errors.Is(err, authn.ErrRefreshRejected),
		errors.Is(err, agentaccessauth.ErrInvalidAAPAccessToken):
		return mappedError{http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required."}
	case errors.Is(err, authz.ErrNotVisible),
		errors.Is(err, agentaccessauth.ErrAAPAuthorizationNotVisible),
		errors.Is(err, agentaccessauth.ErrSubjectOwnershipNotFound),
		errors.Is(err, ErrAAPFeatureDisabled),
		errors.Is(err, ErrAAPFeatureNotEnabledForWorkspace),
		errors.Is(err, ErrAAPFeatureNotEnabledForClient),
		isNotFound(err):
		// Feature-disabled and gray-deny use not-found semantics so clients cannot
		// probe rollout allowlists (same as authorization not-visible).
		return mappedError{http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested resource was not found."}
	case errors.Is(err, authz.ErrDenied),
		errors.Is(err, agentaccessauth.ErrAAPAuthorizationDenied),
		errors.Is(err, execution.ErrConfirmationRequesterMismatch),
		errors.Is(err, authn.ErrAccountLocked), errors.Is(err, authn.ErrAccountDisabled):
		return mappedError{http.StatusForbidden, "AGENT_ACCESS_DENIED", "The requested operation is not allowed."}
	case errors.Is(err, ssetransport.ErrConnectionLimitExceeded),
		errors.Is(err, agentaccessauth.ErrClientAuthenticationLimited),
		errors.Is(err, agentaccessauth.ErrTokenIssueLimited),
		errors.Is(err, agentaccess.ErrDataPlaneQuotaExceeded):
		return mappedError{http.StatusTooManyRequests, "RATE_LIMITED", "The request rate limit was exceeded."}
	case errors.Is(err, ErrAAPRunIdempotencyConflict),
		errors.Is(err, execution.ErrInteractionIdempotencyConflict),
		errors.Is(err, aap.ErrCommandIdempotencyConflict),
		errors.Is(err, aap.ErrConversationIdempotencyConflict),
		errors.Is(err, aap.ErrRunIdempotencyConflict):
		return mappedError{http.StatusConflict, "IDEMPOTENCY_CONFLICT", "The Idempotency-Key was already used for a different request."}
	case errors.Is(err, execution.ErrConfirmationExpired):
		return mappedError{http.StatusConflict, "INTERACTION_EXPIRED", "The interaction has expired."}
	case errors.Is(err, execution.ErrInteractionAlreadyResolved):
		return mappedError{http.StatusConflict, "INTERACTION_ALREADY_RESOLVED", "The interaction has already been resolved."}
	case errors.Is(err, execution.ErrInteractionDecisionBindingChanged):
		return mappedError{http.StatusConflict, "CONFLICT", "The interaction version or immutable binding has changed."}
	case errors.Is(err, aap.ErrRunNotCancellable):
		return mappedError{http.StatusConflict, "RUN_NOT_CANCELLABLE", "The Run is not cancellable."}
	case errors.Is(err, ErrAAPEventCursorInvalid):
		return mappedError{http.StatusUnprocessableEntity, "REPLAY_CURSOR_INVALID", "The replay cursor is not valid for this Run."}
	case errors.Is(err, agentaccessauth.ErrAAPAuthorizationUnavailable),
		errors.Is(err, agentaccessauth.ErrTokenServiceUnavailable),
		errors.Is(err, agentaccessauth.ErrClientAuthenticationUnavailable),
		errors.Is(err, ErrAAPAgentProfileUnavailable):
		return mappedError{http.StatusServiceUnavailable, "INTERNAL_ERROR", "The request could not be completed."}
	case isConflict(err):
		return mappedError{http.StatusConflict, "CONFLICT", "The resource state has changed or conflicts with this request."}
	case errors.Is(err, ErrAAPUnsupportedContentType):
		return mappedError{http.StatusUnprocessableEntity, "UNSUPPORTED_CONTENT_TYPE", "The requested content type is not supported."}
	case errors.Is(err, aap.ErrConversationInvalid), errors.Is(err, aap.ErrRunInvalid),
		errors.Is(err, aap.ErrRunCancelInvalid),
		errors.Is(err, aap.ErrInteractionDecisionInvalid), isInvalid(err):
		return mappedError{http.StatusUnprocessableEntity, "VALIDATION_ERROR", "The request is not valid."}
	default:
		return mappedError{http.StatusInternalServerError, "INTERNAL_ERROR", "The request could not be completed."}
	}
}

func isRetryableHTTPStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

// mapOutboundIdentityError reserves technical design §10 stable codes. Execution
// paths are wired in later checklist items; this mapping must already be unique
// and must never surface Secret names, Broker bodies, Assertions, Token values,
// or Vault locators in the public message.
func mapOutboundIdentityError(err error) (mappedError, bool) {
	var outbound *outboundidentity.Error
	if !errors.As(err, &outbound) || outbound == nil {
		return mappedError{}, false
	}
	message := strings.TrimSpace(outbound.Message)
	if message == "" {
		if sentinel := outboundidentity.SentinelByCode(outbound.Code); sentinel != nil {
			message = sentinel.Message
		} else {
			message = "The outbound identity request could not be completed."
		}
	}
	status, ok := outboundIdentityHTTPStatus(outbound.Code)
	if !ok {
		return mappedError{}, false
	}
	return mappedError{status: status, code: outbound.Code, message: message}, true
}

func outboundIdentityHTTPStatus(code string) (int, bool) {
	switch code {
	case outboundidentity.CodeIdentityPolicyInvalid,
		outboundidentity.CodeIdentityModeUnsupported,
		outboundidentity.CodeIdentityScopeNotAllowed,
		outboundidentity.CodeIdentityExecutorUnsupported,
		outboundidentity.CodeSubjectRequired,
		outboundidentity.CodeCredentialRequired,
		outboundidentity.CodeCredentialTargetMismatch,
		outboundidentity.CodeTargetRejected:
		return http.StatusUnprocessableEntity, true
	case outboundidentity.CodeIdentityMigrationRequired,
		outboundidentity.CodeIdentityConnectionNotReady,
		outboundidentity.CodeIdentityPolicyChanged,
		outboundidentity.CodeIdentityChangeConfirmationRequired,
		outboundidentity.CodeIdentityChangeConfirmationStale,
		outboundidentity.CodeCredentialExpired:
		return http.StatusConflict, true
	case outboundidentity.CodeCredentialInvalid:
		return http.StatusBadRequest, true
	case outboundidentity.CodeCredentialCapacityExceeded:
		return http.StatusTooManyRequests, true
	case outboundidentity.CodeBrokerDenied,
		outboundidentity.CodeBusinessAuthorizationDenied:
		return http.StatusForbidden, true
	case outboundidentity.CodeBrokerUnavailable:
		return http.StatusServiceUnavailable, true
	default:
		return 0, false
	}
}

func mapError(err error) mappedError {
	if mapped, ok := mapOutboundIdentityError(err); ok {
		return mapped
	}
	// ZKL-56: typed Smart DAG TurnFailure maps first so unknown internal errors
	// can still be safely projected without leaking Cause.
	if tf, ok := smartdag.AsTurnFailure(err); ok && tf != nil {
		switch tf.Code {
		case smartdag.CodeSessionClosed:
			return mappedError{http.StatusConflict, tf.Code, tf.Message}
		case smartdag.CodeTurnInProgress:
			return mappedError{http.StatusConflict, tf.Code, tf.Message}
		case smartdag.CodeSessionVersionConflict:
			return mappedError{http.StatusConflict, tf.Code, tf.Message}
		case smartdag.CodeAgentModelRequired:
			return mappedError{http.StatusUnprocessableEntity, tf.Code, tf.Message}
		case smartdag.CodeModelTimeout:
			return mappedError{http.StatusGatewayTimeout, tf.Code, tf.Message}
		case smartdag.CodeModelUnavailable:
			return mappedError{http.StatusServiceUnavailable, tf.Code, tf.Message}
		case smartdag.CodeOutputInvalid:
			return mappedError{http.StatusUnprocessableEntity, tf.Code, tf.Message}
		case smartdag.CodeGuardRejected:
			return mappedError{http.StatusUnprocessableEntity, tf.Code, tf.Message}
		case smartdag.CodeDraftConflict:
			return mappedError{http.StatusConflict, tf.Code, tf.Message}
		case smartdag.CodeDraftPersistFailed:
			return mappedError{http.StatusServiceUnavailable, tf.Code, tf.Message}
		default:
			return mappedError{http.StatusInternalServerError, smartdag.CodeUnknownFailure, "The generate request failed."}
		}
	}
	switch {
	case errors.Is(err, ErrAAPRunIdempotencyConflict),
		errors.Is(err, workflow.ErrIdempotencyConflict):
		return mappedError{http.StatusConflict, "IDEMPOTENCY_CONFLICT", "The Idempotency-Key was already used for a different request."}
	case errors.Is(err, workflow.ErrRevisionNotActive),
		errors.Is(err, workflow.ErrRevisionNotExecutable):
		return mappedError{http.StatusConflict, "REVISION_NOT_EXECUTABLE", "The workflow revision is not the active published revision or is not executable."}
	case errors.Is(err, openapiimport.ErrImportIncomplete):
		return mappedError{http.StatusConflict, "OPENAPI_IMPORT_INCOMPLETE", "The OpenAPI import is incomplete and cannot generate tools."}
	case errors.Is(err, smartdag.ErrAgentModelRequired):
		// D2: Agent without usable LLM cannot generate; 422 AGENT_MODEL_REQUIRED, no Draft.
		return mappedError{http.StatusUnprocessableEntity, smartdag.CodeAgentModelRequired, "The agent has no usable model configuration for smart orchestration generation."}
	case errors.Is(err, smartdag.ErrModelConfigBypassRejected):
		return mappedError{http.StatusUnprocessableEntity, "VALIDATION_ERROR", "modelConfigId must not be supplied on generate requests; use the agent binding."}
	case errors.Is(err, smartdag.ErrGuardRejected):
		return mappedError{http.StatusUnprocessableEntity, smartdag.CodeGuardRejected, "The generated workflow graph failed deterministic validation."}
	case errors.Is(err, smartdag.ErrSessionClosed):
		// P1.3.4: close 后 turn → 409
		return mappedError{http.StatusConflict, smartdag.CodeSessionClosed, "The workflow generate session is closed."}
	case errors.Is(err, smartdag.ErrTurnInProgress):
		return mappedError{http.StatusConflict, smartdag.CodeTurnInProgress, "A generate turn is already in progress for this session."}
	case errors.Is(err, smartdag.ErrSessionVersionConflict):
		return mappedError{http.StatusConflict, smartdag.CodeSessionVersionConflict, "The generate session version has changed; refresh and retry explicitly."}
	case errors.Is(err, smartdag.ErrModelTimeout):
		return mappedError{http.StatusGatewayTimeout, smartdag.CodeModelTimeout, "The model generation timed out; the draft was not modified."}
	case errors.Is(err, smartdag.ErrModelUnavailable):
		return mappedError{http.StatusServiceUnavailable, smartdag.CodeModelUnavailable, "The model service is temporarily unavailable; the draft was not modified."}
	case errors.Is(err, smartdag.ErrOutputInvalid):
		return mappedError{http.StatusUnprocessableEntity, smartdag.CodeOutputInvalid, "The model output could not be parsed into a workflow graph; the draft was not modified."}
	case errors.Is(err, smartdag.ErrDraftConflict):
		return mappedError{http.StatusConflict, smartdag.CodeDraftConflict, "The workflow draft version changed during generation; the draft was not overwritten."}
	case errors.Is(err, smartdag.ErrDraftPersistFailed):
		return mappedError{http.StatusServiceUnavailable, smartdag.CodeDraftPersistFailed, "Persisting the generated draft failed; the draft was not modified."}
	case errors.Is(err, smartdag.ErrSessionNotFound):
		return mappedError{http.StatusNotFound, "NOT_FOUND", "The requested resource was not found."}
	case errors.Is(err, smartdag.ErrAgentNotInWorkspace):
		return mappedError{http.StatusNotFound, "NOT_FOUND", "The requested resource was not found."}
	case errors.Is(err, ErrAgentRunEventStreamNotReady):
		return mappedError{http.StatusConflict, "EVENT_STREAM_NOT_READY", "The agent run event stream is not ready yet."}
	case errors.Is(err, ErrAgentAccessManagementCommandInProgress):
		return mappedError{http.StatusConflict, "COMMAND_IN_PROGRESS", "The idempotent management command is still in progress."}
	case errors.Is(err, agentaccessauth.ErrTokenExpired):
		return mappedError{http.StatusUnauthorized, "TOKEN_EXPIRED", "The access token has expired."}
	case agentaccessauth.IsStreamRevocation(err):
		return mappedError{http.StatusForbidden, "AUTHORIZATION_REVOKED", "The stream authorization is no longer valid."}
	case errors.Is(err, ssetransport.ErrConnectionLimitExceeded):
		return mappedError{http.StatusTooManyRequests, "STREAM_CONNECTION_LIMIT", "Too many event streams are active."}
	case errors.Is(err, ErrAAPEventCursorInvalid):
		return mappedError{http.StatusUnprocessableEntity, "REPLAY_CURSOR_INVALID", "The replay cursor is not valid for this Run."}
	case errors.Is(err, provider.ErrKindNotAvailable):
		return mappedError{http.StatusUnprocessableEntity, "PROVIDER_KIND_NOT_AVAILABLE", "The requested provider kind is not available."}
	case errors.Is(err, capability.ErrUnavailable):
		// P3.3: bind unpublished WORKFLOW (no active release) → 4xx, not 500.
		return mappedError{http.StatusConflict, "CAPABILITY_UNAVAILABLE", "The capability is not available for this operation (e.g. unpublished workflow has no active release)."}
	case errors.Is(err, authn.ErrAuthenticationUnavailable):
		// ZKL-63 HIGH-01 D2=A: fail closed on identity store / infrastructure errors.
		return mappedError{http.StatusServiceUnavailable, "AUTHENTICATION_UNAVAILABLE", "Authentication is temporarily unavailable."}
	case errors.Is(err, ErrUnauthenticated), errors.Is(err, authn.ErrAccessUnauthenticated),
		errors.Is(err, authn.ErrInvalidCredentials),
		errors.Is(err, authn.ErrRefreshRejected):
		return mappedError{http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required."}
	case errors.Is(err, ErrPasswordChangeRequired):
		// ZKL-63 HIGH-03 D5=A: authenticated but restricted until password change.
		return mappedError{http.StatusForbidden, "PASSWORD_CHANGE_REQUIRED", "Password change is required before continuing."}
	case errors.Is(err, authz.ErrNotVisible):
		return mappedError{http.StatusNotFound, "NOT_FOUND", "The requested resource was not found."}
	case errors.Is(err, agentaccessauth.ErrAAPAuthorizationNotVisible):
		return mappedError{http.StatusNotFound, "NOT_FOUND", "The requested resource was not found."}
	case errors.Is(err, authz.ErrDenied),
		errors.Is(err, agentaccessauth.ErrAAPAuthorizationDenied),
		errors.Is(err, execution.ErrConfirmationRequesterMismatch),
		errors.Is(err, authn.ErrAccountLocked), errors.Is(err, authn.ErrAccountDisabled):
		return mappedError{http.StatusForbidden, "FORBIDDEN", "This action is not permitted."}
	case errors.Is(err, agent.ErrPromptAuditUnavailable):
		return mappedError{http.StatusServiceUnavailable, agent.ErrorCodePromptAuditUnavailable,
			"Prompt read audit is temporarily unavailable."}
	case errors.Is(err, agent.ErrPromptModelUnavailable):
		return mappedError{http.StatusConflict, agent.ErrorCodePromptModelUnavailable,
			"The selected model is not available for prompt enhancement."}
	case errors.Is(err, agent.ErrPromptOutputInvalid):
		return mappedError{http.StatusBadGateway, agent.ErrorCodePromptOutputInvalid,
			"The model returned no usable prompt text."}
	case errors.Is(err, agent.ErrPromptGeneration):
		if strings.Contains(strings.ToLower(err.Error()), "timeout") ||
			strings.Contains(strings.ToLower(err.Error()), "deadline") {
			return mappedError{http.StatusGatewayTimeout, agent.ErrorCodePromptGenerationTimeout,
				"Prompt generation timed out."}
		}
		return mappedError{http.StatusBadGateway, agent.ErrorCodePromptGenerationFailed,
			"Prompt generation failed."}
	case errors.Is(err, agent.ErrPromptOutputMismatch):
		return mappedError{http.StatusConflict, "OUTPUT_MISMATCH",
			"The system prompt does not match the create-preview output."}
	case errors.Is(err, agent.ErrPromptPreviewIntegrity):
		return mappedError{http.StatusInternalServerError, agent.ErrorCodePreviewIntegrity,
			"The create-preview source could not be linked safely."}
	case errors.Is(err, tool.ErrForcePublishDisabled):
		return mappedError{http.StatusForbidden, "TOOL_FORCE_PUBLISH_DISABLED",
			"Tool force publish is disabled on this server."}
	case errors.Is(err, tool.ErrForceReasonRequired):
		return mappedError{http.StatusUnprocessableEntity, "TOOL_FORCE_REASON_REQUIRED",
			"Force publish requires a reason of at least 8 characters."}
	case errors.Is(err, workflow.ErrForcePublishDisabled):
		return mappedError{http.StatusForbidden, "WORKFLOW_FORCE_PUBLISH_DISABLED",
			"Workflow force publish is disabled on this server."}
	case errors.Is(err, workflow.ErrForceReasonRequired):
		return mappedError{http.StatusUnprocessableEntity, "WORKFLOW_FORCE_REASON_REQUIRED",
			"Force publish requires a reason of at least 8 characters."}
	case errors.Is(err, workflow.ErrNoSuccessfulTrial):
		return mappedError{http.StatusConflict, "WORKFLOW_TRIAL_REQUIRED",
			"Workflow must pass trial before publish (or use force-publish)."}
	case errors.Is(err, tool.ErrNoPassingTest):
		return mappedError{http.StatusConflict, "TOOL_TEST_REQUIRED",
			"tool must pass test before publish"}
	case isNotFound(err):
		return mappedError{http.StatusNotFound, "NOT_FOUND", "The requested resource was not found."}
	case isConflict(err):
		return mappedError{http.StatusConflict, "CONFLICT", "The resource state has changed or conflicts with this request."}
	case isInvalid(err):
		return mappedError{http.StatusUnprocessableEntity, "VALIDATION_ERROR", "The request is not valid."}
	default:
		return mappedError{http.StatusInternalServerError, "INTERNAL_ERROR", "The request could not be completed."}
	}
}

func isNotFound(err error) bool {
	return errors.Is(err, identity.ErrNotFound) || errors.Is(err, workspace.ErrNotFound) ||
		errors.Is(err, agent.ErrNotFound) || errors.Is(err, capability.ErrNotFound) ||
		errors.Is(err, modelconfig.ErrNotFound) || errors.Is(err, connection.ErrNotFound) ||
		errors.Is(err, secret.ErrNotFound) || errors.Is(err, tool.ErrNotFound) ||
		errors.Is(err, workflow.ErrNotFound) || errors.Is(err, chat.ErrNotFound) ||
		errors.Is(err, execution.ErrRunNotFound) || errors.Is(err, execution.ErrToolInvocationNotFound) ||
		errors.Is(err, execution.ErrConfirmationNotFound) ||
		errors.Is(err, execution.ErrConfirmationResumeNotFound) || errors.Is(err, storedobject.ErrNotFound) ||
		errors.Is(err, protocolevent.ErrRunScopeNotFound) ||
		errors.Is(err, openapiimport.ErrNotFound) || errors.Is(err, audit.ErrNotFound) ||
		errors.Is(err, agentaudit.ErrNotFound) ||
		errors.Is(err, provider.ErrNotFound) || errors.Is(err, agentaccess.ErrRepositoryNotFound) ||
		errors.Is(err, aap.ErrConversationNotFound) || errors.Is(err, aap.ErrRunNotFound) ||
		errors.Is(err, aap.ErrInteractionNotFound)
}

func isConflict(err error) bool {
	return errors.Is(err, identity.ErrConflict) || errors.Is(err, identity.ErrLastPlatformAdmin) ||
		errors.Is(err, workspace.ErrConflict) ||
		errors.Is(err, workspace.ErrLastOwner) || errors.Is(err, agent.ErrConflict) ||
		errors.Is(err, agent.ErrInUse) || errors.Is(err, capability.ErrConflict) ||
		errors.Is(err, capability.ErrCallableConflict) || errors.Is(err, modelconfig.ErrConflict) ||
		errors.Is(err, modelconfig.ErrInUse) || errors.Is(err, connection.ErrConflict) ||
		errors.Is(err, secret.ErrConflict) || errors.Is(err, tool.ErrConflict) ||
		errors.Is(err, tool.ErrImmutable) || errors.Is(err, workflow.ErrConflict) ||
		errors.Is(err, workflow.ErrTrialFailed) || errors.Is(err, workflow.ErrNoSuccessfulTrial) ||
		errors.Is(err, chat.ErrConflict) || errors.Is(err, execution.ErrRunConflict) ||
		errors.Is(err, execution.ErrToolInvocationConflict) ||
		errors.Is(err, execution.ErrToolInvocationIdempotencyConflict) ||
		errors.Is(err, execution.ErrConfirmationConflict) ||
		errors.Is(err, execution.ErrConfirmationExpired) ||
		errors.Is(err, execution.ErrConfirmationBindingChanged) ||
		errors.Is(err, execution.ErrInteractionDecisionBindingChanged) ||
		errors.Is(err, execution.ErrConfirmationResumeConflict) ||
		errors.Is(err, execution.ErrConfirmationResumeExecuting) ||
		errors.Is(err, storedobject.ErrConflict) || errors.Is(err, openapiimport.ErrConflict) ||
		errors.Is(err, audit.ErrConflict) || errors.Is(err, provider.ErrConflict) ||
		errors.Is(err, agentaccess.ErrRepositoryConflict) ||
		errors.Is(err, agentaccess.ErrLastActiveCredential) ||
		errors.Is(err, agentaccess.ErrRotationLimit)
}

func isInvalid(err error) bool {
	return errors.Is(err, identity.ErrInvalid) || errors.Is(err, workspace.ErrInvalid) ||
		errors.Is(err, agent.ErrInvalid) || errors.Is(err, capability.ErrInvalid) ||
		errors.Is(err, modelconfig.ErrInvalid) || errors.Is(err, connection.ErrInvalid) ||
		errors.Is(err, secret.ErrInvalid) || errors.Is(err, tool.ErrInvalid) ||
		errors.Is(err, listpage.ErrInvalid) ||
		errors.Is(err, workflow.ErrInvalid) || errors.Is(err, chat.ErrInvalid) ||
		errors.Is(err, execution.ErrRunInvalid) || errors.Is(err, execution.ErrToolInvocationInvalid) ||
		errors.Is(err, execution.ErrConfirmationInvalid) ||
		errors.Is(err, execution.ErrConfirmationTokenInvalid) ||
		errors.Is(err, execution.ErrConfirmationResumeInvalid) ||
		errors.Is(err, protocolevent.ErrReadInvalid) || errors.Is(err, ErrAAPEventCursorInvalid) ||
		errors.Is(err, ssetransport.ErrBackpressureInvalid) ||
		errors.Is(err, agentaccessauth.ErrStreamRevalidationInvalid) ||
		errors.Is(err, agentaccessauth.ErrAAPAuthorizationInvalid) ||
		errors.Is(err, ErrAAPCreateRunInvalid) || errors.Is(err, ErrAAPUnsupportedContentType) ||
		errors.Is(err, ErrAAPRunEventsRequestInvalid) ||
		errors.Is(err, ErrAAPInteractionDecisionReqInvalid) ||
		errors.Is(err, storedobject.ErrInvalid) || errors.Is(err, openapiimport.ErrInvalid) ||
		errors.Is(err, audit.ErrInvalid) || errors.Is(err, audit.ErrPayloadRequired) ||
		errors.Is(err, agentaudit.ErrInvalid) ||
		errors.Is(err, provider.ErrInvalid) || errors.Is(err, smartdag.ErrInvalid) ||
		errors.Is(err, agentaccess.ErrRepositoryInvalid) ||
		errors.Is(err, agentaccess.ErrManagementInvalid) ||
		errors.Is(err, agentaccess.ErrGrantConfigurationInvalid)
}
