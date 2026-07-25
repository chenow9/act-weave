package agentrun

import (
	"context"
	"encoding/json"
)

// Job is the async AgentRun work unit shared by initial Enqueue and continue.
// Field semantics match chatruntime.Job (type-aliased there for zero-cost
// use from Messenger and bridge helpers).
type Job struct {
	WorkspaceID            string
	SessionID              string
	RunID                  string
	UserMessageID          string
	ActorID                string
	InitialEventsCommitted bool
}

// ContinueLifecycle is an optional multi-replica lease hook for continue drives.
// Application wires claim renew/complete so approval and recovery share one lease.
type ContinueLifecycle interface {
	// Renew extends the exclusive continue lease while the drive is still running.
	Renew(ctx context.Context) error
	// Complete releases the exclusive continue lease after the drive finishes.
	Complete(ctx context.Context) error
}

// Runtime is the production facade for Messenger / AAP / cancel / continuation.
// Implementations must be safe for concurrent Enqueue/Cancel/Continue.
//
// Production always uses the eino bridge (*chatruntimebridge.Bridge via
// *Factory). Rollback is previous binary / drain traffic (see runbook).
type Runtime interface {
	// Enqueue starts asynchronous execution for a CHAT AgentRun. It must not
	// block the caller's request path; failures are persisted as failed runs.
	Enqueue(job Job)
	// CancelRun interrupts an active in-process runtime job. A missing entry
	// means this process has no active job (already quiescent for that run).
	CancelRun(workspaceID, runID string) error
	// EnqueueContinueWithLifecycle schedules continue-after-confirmation
	// asynchronously with optional renew/complete hooks for the durable
	// runtime_continuation_claims lease.
	EnqueueContinueWithLifecycle(
		job Job,
		requestSnapshot, toolResult json.RawMessage,
		life ContinueLifecycle,
	)
}
