package einoruntime

import (
	"context"
	"errors"
	"sync"

	"github.com/cloudwego/eino/compose"
)

// Budget defaults (D12) — aligned with chatruntime defaultMaxModelRounds /
// defaultMaxToolCalls and config.DefaultEinoMaxIterations /
// DefaultEinoMaxToolInvocations.
const (
	DefaultMaxIterations      = 8
	DefaultMaxToolInvocations = 16

	// ToolBudgetExceededCode is the user-safe errorCode in tool result JSON
	// (legacy chatruntime TOOL_BUDGET_EXCEEDED).
	ToolBudgetExceededCode = "TOOL_BUDGET_EXCEEDED"
	// ToolBudgetExceededMessage is the user-safe message string.
	ToolBudgetExceededMessage = "tool call budget exhausted"
)

// ErrToolBudgetExceeded is the sentinel for budget exhaustion (model rounds
// or tool invocations).
var ErrToolBudgetExceeded = errors.New("tool budget exceeded")

// toolBudgetCounter is a per-agent-run counter shared by the budget middleware.
type toolBudgetCounter struct {
	mu  sync.Mutex
	n   int
	max int
}

// tryAcquire returns true when a tool invocation is still within budget and
// increments the counter. False means the hard cap was already reached.
func (c *toolBudgetCounter) tryAcquire() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n >= c.max {
		return false
	}
	c.n++
	return true
}

// Count returns the number of allowed tool invocations so far.
func (c *toolBudgetCounter) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// NewToolBudgetMiddleware builds a ToolsNode middleware that hard-caps total
// tool InvokableRun calls at max (default 16). Excess calls return a
// TOOL_BUDGET_EXCEEDED tool result JSON without invoking the underlying tool
// (aligned with chatruntime executeToolRound budget short-circuit).
func NewToolBudgetMiddleware(max int) compose.ToolMiddleware {
	if max <= 0 {
		max = DefaultMaxToolInvocations
	}
	counter := &toolBudgetCounter{max: max}
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				if !counter.tryAcquire() {
					return &compose.ToolOutput{
						Result: formatToolErrorResult(ToolBudgetExceededCode, ToolBudgetExceededMessage),
					}, nil
				}
				return next(ctx, input)
			}
		},
	}
}

// NewToolBudgetMiddlewareWithCounter is like NewToolBudgetMiddleware but also
// returns the shared counter for tests / observability.
func NewToolBudgetMiddlewareWithCounter(max int) (compose.ToolMiddleware, *toolBudgetCounter) {
	if max <= 0 {
		max = DefaultMaxToolInvocations
	}
	counter := &toolBudgetCounter{max: max}
	mw := compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				if !counter.tryAcquire() {
					return &compose.ToolOutput{
						Result: formatToolErrorResult(ToolBudgetExceededCode, ToolBudgetExceededMessage),
					}, nil
				}
				return next(ctx, input)
			}
		},
	}
	return mw, counter
}
