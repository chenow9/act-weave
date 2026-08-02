package a2agateway

import "context"

// executionLeaseKey carries fencing proof into the real ExecuteRun chain.
type executionLeaseKey struct{}

// ExecutionFence is owner/token/generation proof for inbound execution.
// All terminal writes for task/run/delegation under this claim must re-validate.
// Values are plain strings so context.WithoutCancel still carries the proof.
type ExecutionFence struct {
	WorkspaceID string
	TaskID      string
	RunID       string
	Owner       string
	Token       string
	Generation  int64
	// Repo optional for AssertHeld helpers; atomic SQL paths use fields directly.
	Repo *Repository
	// AssertHeld is optional non-atomic check; prefer Repo.FencedTransitionAgentRun.
	AssertHeld func(ctx context.Context) error
}

// WithExecutionFence attaches lease fencing proof to ctx for ExecuteRun.
func WithExecutionFence(ctx context.Context, f ExecutionFence) context.Context {
	return context.WithValue(ctx, executionLeaseKey{}, f)
}

// ExecutionFenceFrom returns fencing proof when present (survives WithoutCancel).
func ExecutionFenceFrom(ctx context.Context) (ExecutionFence, bool) {
	f, ok := ctx.Value(executionLeaseKey{}).(ExecutionFence)
	return f, ok && f.Token != "" && f.Owner != "" && f.Generation > 0
}
