package agentdelegation

import "errors"

var (
	ErrInvalid        = errors.New("agent delegation request is invalid")
	ErrNotFound       = errors.New("agent delegation binding not found")
	ErrConflict       = errors.New("agent delegation binding conflict")
	ErrCycle          = errors.New("agent delegation graph contains a cycle")
	ErrSelfLoop       = errors.New("agent delegation self-loop is not allowed")
	ErrDuplicateAlias = errors.New("callable_name already used by caller")
	// ErrNamespaceConflict is cross-source (internal binding vs A2A remote) callable collision.
	ErrNamespaceConflict     = errors.New("callable_name conflicts with another tool source for this agent")
	ErrDuplicateTarget       = errors.New("target agent already bound for caller")
	ErrAgentUnavailable      = errors.New("agent is missing, disabled, or out of workspace")
	ErrDepthExceeded         = errors.New("agent delegation depth budget exceeded")
	ErrTotalBudgetExceeded   = errors.New("agent delegation total budget exceeded")
	ErrBindingBudgetExceeded = errors.New("agent delegation per-binding budget exceeded")
	ErrAuditPrewriteFailed   = errors.New("agent delegation audit prewrite failed")
	ErrIdempotentReplay      = errors.New("agent delegation idempotent replay")
	ErrCancelled             = errors.New("agent delegation cancelled")
	ErrTimedOut              = errors.New("agent delegation timed out")
	ErrProtocolUnsupported   = errors.New("a2a protocol feature is not supported")
)
