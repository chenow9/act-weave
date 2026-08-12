package einoruntime

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
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

	// toolBudgetCountRunLocalKey is the gob-safe per-run counter stored via
	// adk.SetRunLocalValue so each Run has an independent budget that survives
	// interrupt/resume and does not leak across concurrent runs.
	toolBudgetCountRunLocalKey = "einoruntime.tool_budget.count"
)

// ErrToolBudgetExceeded is the sentinel for budget exhaustion (model rounds
// or tool invocations).
var ErrToolBudgetExceeded = errors.New("tool budget exceeded")

// ErrToolBudgetMaxInvalid is returned when MaxToolInvocations is negative or >16.
var ErrToolBudgetMaxInvalid = errors.New("einoruntime tool budget max must be 0 (default 16) or 1..16")

// ErrToolBudgetState is returned when run-local budget state is missing/corrupt
// or cannot be read/written (fail closed — never fall back to a shared counter).
var ErrToolBudgetState = errors.New("einoruntime tool budget run-local state error")

// normalizeMaxToolInvocations maps 0 → DefaultMaxToolInvocations (16).
// Accepts 1..16; rejects negative and >16.
func normalizeMaxToolInvocations(max int) (int, error) {
	if max == 0 {
		return DefaultMaxToolInvocations, nil
	}
	if max < 0 || max > DefaultMaxToolInvocations {
		return 0, fmt.Errorf("%w: got %d", ErrToolBudgetMaxInvalid, max)
	}
	return max, nil
}

// tryAcquireRunLocalBudget increments the per-run gob-safe integer counter in
// adk run-local state. Returns (allowed, err). On any state error, fails closed
// (does not silently switch to a global/shared counter).
func tryAcquireRunLocalBudget(ctx context.Context, max int) (bool, error) {
	v, found, err := adk.GetRunLocalValue(ctx, toolBudgetCountRunLocalKey)
	if err != nil {
		return false, fmt.Errorf("%w: get: %v", ErrToolBudgetState, err)
	}
	n := 0
	if found {
		switch x := v.(type) {
		case int:
			n = x
		case int64:
			// gob may restore integers as int64 depending on encoder path.
			n = int(x)
		case int32:
			n = int(x)
		default:
			return false, fmt.Errorf("%w: corrupt counter type %T", ErrToolBudgetState, v)
		}
		if n < 0 {
			return false, fmt.Errorf("%w: negative counter %d", ErrToolBudgetState, n)
		}
	}
	if n >= max {
		return false, nil
	}
	n++
	if err := adk.SetRunLocalValue(ctx, toolBudgetCountRunLocalKey, n); err != nil {
		return false, fmt.Errorf("%w: set: %v", ErrToolBudgetState, err)
	}
	return true, nil
}

// NewToolBudgetMiddleware builds a ToolsNode middleware that hard-caps total
// tool executions at max across all four ToolsNode execution interfaces:
// Invokable, Streamable, EnhancedInvokable, and EnhancedStreamable.
//
// Excess ordinary Invokable / Streamable calls return a TOOL_BUDGET_EXCEEDED
// tool result (JSON string / single-chunk stream) without invoking the
// underlying tool. Excess EnhancedInvokable / EnhancedStreamable calls
// (including the client tool-search executor) return ErrToolBudgetExceeded
// without invoking the underlying tool.
//
// All four hooks share the same per-run counter and key so every execution
// path counts toward the cap (D3). A single tool execution is charged only
// once — ToolsNode dispatches to exactly one interface per call (or converts
// after that interface's middleware already ran), so dual-interface tools are
// not double-charged.
//
// The counter lives in adk run-local state (checkpoint-serialized): each Run
// starts at zero, concurrent runs do not interfere (even when sharing this
// middleware instance), and Resume continues the prior count.
//
// Limit contract (no silent clamping):
//   - 0 → DefaultMaxToolInvocations (16)
//   - 1..16 accepted as-is
//   - negative or >16 → ErrToolBudgetMaxInvalid
func NewToolBudgetMiddleware(max int) (compose.ToolMiddleware, error) {
	max, err := normalizeMaxToolInvocations(max)
	if err != nil {
		return compose.ToolMiddleware{}, err
	}
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				ok, err := tryAcquireRunLocalBudget(ctx, max)
				if err != nil {
					return nil, err
				}
				if !ok {
					return &compose.ToolOutput{
						Result: formatToolErrorResult(ToolBudgetExceededCode, ToolBudgetExceededMessage),
					}, nil
				}
				return next(ctx, input)
			}
		},
		Streamable: func(next compose.StreamableToolEndpoint) compose.StreamableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.StreamToolOutput, error) {
				ok, err := tryAcquireRunLocalBudget(ctx, max)
				if err != nil {
					return nil, err
				}
				if !ok {
					// Same budget-exceeded payload as Invokable, as a single stream chunk.
					return &compose.StreamToolOutput{
						Result: schema.StreamReaderFromArray([]string{
							formatToolErrorResult(ToolBudgetExceededCode, ToolBudgetExceededMessage),
						}),
					}, nil
				}
				return next(ctx, input)
			}
		},
		EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
				ok, err := tryAcquireRunLocalBudget(ctx, max)
				if err != nil {
					return nil, err
				}
				if !ok {
					// Fail closed for enhanced tools (incl. tool_search) so the
					// agent run surfaces budget exhaustion rather than a wrong-kind
					// tool-search result.
					return nil, errors.Join(ErrToolBudgetExceeded, errors.New(ToolBudgetExceededMessage))
				}
				return next(ctx, input)
			}
		},
		EnhancedStreamable: func(next compose.EnhancedStreamableToolEndpoint) compose.EnhancedStreamableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedStreamableToolOutput, error) {
				ok, err := tryAcquireRunLocalBudget(ctx, max)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, errors.Join(ErrToolBudgetExceeded, errors.New(ToolBudgetExceededMessage))
				}
				return next(ctx, input)
			}
		},
	}, nil
}

// Production builders (BuildAgenticAgent / BuildChatModelAgent) use only
// NewToolBudgetMiddleware (run-local counter). Counter-only construction for
// unit tests lives in budget_test.go as newToolBudgetMiddlewareWithCounter —
// it is intentionally not part of the production package API.
