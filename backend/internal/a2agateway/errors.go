package a2agateway

import "errors"

var (
	ErrInvalid        = errors.New("a2a gateway request is invalid")
	ErrNotFound       = errors.New("a2a resource not found")
	ErrConflict       = errors.New("a2a resource conflict")
	ErrNotAllowlisted = errors.New("agent is not allowlisted for a2a")
	ErrAuthRejected   = errors.New("a2a authentication rejected")
	ErrSSRFDenied     = errors.New("a2a outbound target denied by ssrf policy")
	ErrRemoteFailed   = errors.New("a2a remote agent failed")
	ErrTimeout        = errors.New("a2a remote call timed out")
	ErrCancelled      = errors.New("a2a call cancelled")
	ErrUnsupported    = errors.New("a2a capability not supported")
	ErrCardInvalid    = errors.New("a2a agent card invalid")
	// ErrNamespaceConflict: callable_name collides with internal binding or another remote.
	ErrNamespaceConflict = errors.New("callable_name conflicts with another tool source for this agent")
)
