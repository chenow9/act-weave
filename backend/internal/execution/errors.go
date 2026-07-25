package execution

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidExecutor                   = errors.New("invalid executor")
	ErrExecutorExists                    = errors.New("executor already registered")
	ErrExecutorNotFound                  = errors.New("EXECUTOR_NOT_AVAILABLE")
	ErrInvocationNotActive               = errors.New("INVOCATION_NOT_ACTIVE")
	ErrToolInvocationNotFound            = errors.New("tool invocation not found")
	ErrToolInvocationConflict            = errors.New("tool invocation state conflict")
	ErrToolInvocationInvalid             = errors.New("invalid tool invocation")
	ErrToolInvocationIdempotencyConflict = errors.New("tool invocation idempotency conflict")
	ErrRunNotFound                       = errors.New("run record not found")
	ErrRunConflict                       = errors.New("run state conflict")
	ErrRunInvalid                        = errors.New("invalid run record")
)

const (
	ErrorCodeInvalidRequest   = "EXECUTION_INVALID_REQUEST"
	ErrorCodeInvalidSnapshot  = "EXECUTION_INVALID_SNAPSHOT"
	ErrorCodeEventSink        = "EXECUTION_EVENT_SINK_ERROR"
	ErrorCodeConflict         = "EXECUTION_CONFLICT"
	ErrorCodeCanceled         = "EXECUTION_CANCELED"
	ErrorCodeTimeout          = "EXECUTION_TIMEOUT"
	ErrorCodeUpstream         = "EXECUTION_UPSTREAM_ERROR"
	ErrorCodeUpstreamHTTP     = "EXECUTION_UPSTREAM_HTTP_ERROR"
	ErrorCodeResponseTooLarge = "EXECUTION_RESPONSE_TOO_LARGE"
	ErrorCodeResponseRead     = "EXECUTION_RESPONSE_READ_ERROR"
	ErrorCodeEgressDenied     = "EXECUTION_EGRESS_DENIED"
	ErrorCodeCredential       = "EXECUTION_CREDENTIAL_ERROR"
)

type Error struct {
	Code       string
	Category   string
	Retryable  bool
	HTTPStatus int
	cause      error
}

func NewError(code, category string, retryable bool, httpStatus int, cause error) *Error {
	return &Error{Code: code, Category: category, Retryable: retryable, HTTPStatus: httpStatus, cause: cause}
}

func (err *Error) Error() string {
	if err == nil {
		return ""
	}
	if err.HTTPStatus > 0 {
		return fmt.Sprintf("%s (HTTP %d)", err.Code, err.HTTPStatus)
	}
	return err.Code
}

func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func ErrorCode(err error) string {
	var executionError *Error
	if errors.As(err, &executionError) {
		return executionError.Code
	}
	return ""
}
