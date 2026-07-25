package outboundidentity

import (
	"errors"
	"fmt"
)

// Stable error codes from technical design §10. Codes are the public vocabulary;
// never attach upstream Broker/business bodies, Secret names, Assertions, Token
// values, Vault locators, or credential fingerprints to these errors.
const (
	CodeIdentityPolicyInvalid              = "OUTBOUND_IDENTITY_POLICY_INVALID"
	CodeIdentityModeUnsupported            = "OUTBOUND_IDENTITY_MODE_UNSUPPORTED"
	CodeIdentityMigrationRequired          = "OUTBOUND_IDENTITY_MIGRATION_REQUIRED"
	CodeIdentityConnectionNotReady         = "OUTBOUND_IDENTITY_CONNECTION_NOT_READY"
	CodeIdentityPolicyChanged              = "OUTBOUND_IDENTITY_POLICY_CHANGED"
	CodeIdentityScopeNotAllowed            = "OUTBOUND_IDENTITY_SCOPE_NOT_ALLOWED"
	CodeIdentityChangeConfirmationRequired = "OUTBOUND_IDENTITY_CHANGE_CONFIRMATION_REQUIRED"
	CodeIdentityChangeConfirmationStale    = "OUTBOUND_IDENTITY_CHANGE_CONFIRMATION_STALE"
	CodeIdentityExecutorUnsupported        = "OUTBOUND_IDENTITY_EXECUTOR_UNSUPPORTED"
	CodeSubjectRequired                    = "OUTBOUND_SUBJECT_REQUIRED"
	CodeCredentialRequired                 = "OUTBOUND_CREDENTIAL_REQUIRED"
	CodeCredentialInvalid                  = "OUTBOUND_CREDENTIAL_INVALID"
	CodeCredentialTargetMismatch           = "OUTBOUND_CREDENTIAL_TARGET_MISMATCH"
	CodeCredentialExpired                  = "OUTBOUND_CREDENTIAL_EXPIRED"
	CodeCredentialCapacityExceeded         = "OUTBOUND_CREDENTIAL_CAPACITY_EXCEEDED"
	CodeBrokerDenied                       = "OUTBOUND_BROKER_DENIED"
	CodeBrokerUnavailable                  = "OUTBOUND_BROKER_UNAVAILABLE"
	CodeBusinessAuthorizationDenied        = "OUTBOUND_BUSINESS_AUTHORIZATION_DENIED"
	CodeTargetRejected                     = "OUTBOUND_TARGET_REJECTED"
)

// Error is the domain error surface for outbound identity. Message is a safe
// public default; details never retain upstream bodies.
type Error struct {
	Code      string
	Message   string
	Retryable bool
	cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.cause)
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.cause }

func (e *Error) Is(target error) bool {
	var other *Error
	if !errors.As(target, &other) {
		return false
	}
	return e != nil && other != nil && e.Code == other.Code
}

// NewError builds a stable outbound error. message must already be safe to return.
func NewError(code, message string, retryable bool) *Error {
	return &Error{Code: code, Message: message, Retryable: retryable}
}

// Wrap attaches a non-sensitive cause for local diagnostics. The cause string is
// not part of the public HTTP message.
func (e *Error) Wrap(cause error) *Error {
	if e == nil {
		return nil
	}
	clone := *e
	clone.cause = cause
	return &clone
}

// Catalog of sentinel errors with safe default messages (technical design §10).
var (
	ErrIdentityPolicyInvalid = NewError(
		CodeIdentityPolicyInvalid,
		"The outbound identity policy is not valid.",
		false,
	)
	ErrIdentityModeUnsupported = NewError(
		CodeIdentityModeUnsupported,
		"The outbound identity mode is not supported.",
		false,
	)
	ErrIdentityMigrationRequired = NewError(
		CodeIdentityMigrationRequired,
		"The service connection must be migrated to a supported outbound identity mode.",
		false,
	)
	ErrIdentityConnectionNotReady = NewError(
		CodeIdentityConnectionNotReady,
		"The service connection is not ready for outbound identity execution.",
		false,
	)
	ErrIdentityPolicyChanged = NewError(
		CodeIdentityPolicyChanged,
		"The outbound identity policy has changed and the published plan must be recompiled.",
		false,
	)
	ErrIdentityScopeNotAllowed = NewError(
		CodeIdentityScopeNotAllowed,
		"One or more requested scopes are not allowed for this connection.",
		false,
	)
	ErrIdentityChangeConfirmationRequired = NewError(
		CodeIdentityChangeConfirmationRequired,
		"This outbound identity change requires a valid impact confirmation proof.",
		false,
	)
	ErrIdentityChangeConfirmationStale = NewError(
		CodeIdentityChangeConfirmationStale,
		"The outbound identity change confirmation is no longer valid.",
		false,
	)
	ErrIdentityExecutorUnsupported = NewError(
		CodeIdentityExecutorUnsupported,
		"This executor does not support user-scoped outbound identity.",
		false,
	)
	ErrSubjectRequired = NewError(
		CodeSubjectRequired,
		"A supported user subject is required for outbound identity.",
		false,
	)
	ErrCredentialRequired = NewError(
		CodeCredentialRequired,
		"A request-passthrough credential binding is required.",
		false,
	)
	ErrCredentialInvalid = NewError(
		CodeCredentialInvalid,
		"The outbound credential envelope is not valid.",
		false,
	)
	ErrCredentialTargetMismatch = NewError(
		CodeCredentialTargetMismatch,
		"An outbound credential binding does not match the required connections.",
		false,
	)
	// Retryable with a new Run (not the same Run rebinding).
	ErrCredentialExpired = NewError(
		CodeCredentialExpired,
		"The outbound credential is no longer available for this execution.",
		true,
	)
	ErrCredentialCapacityExceeded = NewError(
		CodeCredentialCapacityExceeded,
		"Outbound credential capacity was exceeded.",
		true,
	)
	ErrBrokerDenied = NewError(
		CodeBrokerDenied,
		"The token broker denied the subject credential exchange.",
		false,
	)
	ErrBrokerUnavailable = NewError(
		CodeBrokerUnavailable,
		"The token broker is temporarily unavailable.",
		true,
	)
	ErrBusinessAuthorizationDenied = NewError(
		CodeBusinessAuthorizationDenied,
		"The business API denied authorization for the current subject.",
		false,
	)
	ErrTargetRejected = NewError(
		CodeTargetRejected,
		"The outbound request target is not allowed for this connection.",
		false,
	)
)

// AllStableCodes returns the complete §10 vocabulary in stable order for tests
// and HTTP mapping completeness checks.
func AllStableCodes() []string {
	return []string{
		CodeIdentityPolicyInvalid,
		CodeIdentityModeUnsupported,
		CodeIdentityMigrationRequired,
		CodeIdentityConnectionNotReady,
		CodeIdentityPolicyChanged,
		CodeIdentityScopeNotAllowed,
		CodeIdentityChangeConfirmationRequired,
		CodeIdentityChangeConfirmationStale,
		CodeIdentityExecutorUnsupported,
		CodeSubjectRequired,
		CodeCredentialRequired,
		CodeCredentialInvalid,
		CodeCredentialTargetMismatch,
		CodeCredentialExpired,
		CodeCredentialCapacityExceeded,
		CodeBrokerDenied,
		CodeBrokerUnavailable,
		CodeBusinessAuthorizationDenied,
		CodeTargetRejected,
	}
}

// SentinelByCode returns the catalog sentinel for a stable code.
func SentinelByCode(code string) *Error {
	switch code {
	case CodeIdentityPolicyInvalid:
		return ErrIdentityPolicyInvalid
	case CodeIdentityModeUnsupported:
		return ErrIdentityModeUnsupported
	case CodeIdentityMigrationRequired:
		return ErrIdentityMigrationRequired
	case CodeIdentityConnectionNotReady:
		return ErrIdentityConnectionNotReady
	case CodeIdentityPolicyChanged:
		return ErrIdentityPolicyChanged
	case CodeIdentityScopeNotAllowed:
		return ErrIdentityScopeNotAllowed
	case CodeIdentityChangeConfirmationRequired:
		return ErrIdentityChangeConfirmationRequired
	case CodeIdentityChangeConfirmationStale:
		return ErrIdentityChangeConfirmationStale
	case CodeIdentityExecutorUnsupported:
		return ErrIdentityExecutorUnsupported
	case CodeSubjectRequired:
		return ErrSubjectRequired
	case CodeCredentialRequired:
		return ErrCredentialRequired
	case CodeCredentialInvalid:
		return ErrCredentialInvalid
	case CodeCredentialTargetMismatch:
		return ErrCredentialTargetMismatch
	case CodeCredentialExpired:
		return ErrCredentialExpired
	case CodeCredentialCapacityExceeded:
		return ErrCredentialCapacityExceeded
	case CodeBrokerDenied:
		return ErrBrokerDenied
	case CodeBrokerUnavailable:
		return ErrBrokerUnavailable
	case CodeBusinessAuthorizationDenied:
		return ErrBusinessAuthorizationDenied
	case CodeTargetRejected:
		return ErrTargetRejected
	default:
		return nil
	}
}
