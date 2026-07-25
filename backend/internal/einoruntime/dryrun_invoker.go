package einoruntime

import (
	"context"
	"encoding/json"
	"sync"

	"actweave/backend/internal/execution"
)

// DryRunToolCall is one recorded InvokeResolved call (design §7.2).
//
// Order is call order (serial ToolsNode / D11). Used by CI offline fixture
// compare to assert tool name / args parity without hitting the network or
// writing tool invocation rows.
type DryRunToolCall struct {
	// InvocationID is the invoke request identity.
	InvocationID string
	// CapabilityID is the pinned capability from the request.
	CapabilityID string
	// ReleaseID is the pinned release from the request.
	ReleaseID string
	// ToolName is the callable name when known (NameByCapability map).
	ToolName string
	// Args is the model-supplied tool arguments JSON object.
	Args json.RawMessage
}

// DryRunToolInvoker records tool invoke args and returns scripted outputs.
//
// Design §7.2 (CI offline fixture compare):
//   - No network, no DB writes, never enters InvocationPipeline.
//   - Records tool names (via NameByCapability) + args in call order.
//   - Returns fixed outputs so model rounds remain deterministic under a
//     scripted Stream model.
//
// Implements ResolvedInvoker so PipelineTool can use it in golden / DryRun
// tests. Production code must not wire this into the live agent path.
type DryRunToolInvoker struct {
	mu sync.Mutex

	// NameByCapability maps capabilityID → callable name for Call records.
	// Optional; when missing, ToolName on the recorded call is empty.
	NameByCapability map[string]string

	// OutputsByCapability maps capabilityID → raw JSON tool output body.
	// When missing, DefaultOutput is used (or {"dryRun":true}).
	OutputsByCapability map[string]json.RawMessage

	// DefaultOutput is returned when OutputsByCapability has no entry.
	DefaultOutput json.RawMessage

	// Calls is the ordered record of InvokeResolved invocations.
	Calls []DryRunToolCall
}

// Ensure compile-time ResolvedInvoker satisfaction.
var _ ResolvedInvoker = (*DryRunToolInvoker)(nil)

// InvokeResolved records the call and returns a synthetic success result.
// It never performs side effects (design §7.2: 永不进 InvocationPipeline).
func (d *DryRunToolInvoker) InvokeResolved(
	_ context.Context,
	request execution.InvokeRequest,
	_ execution.ResolvedInvocation,
) (execution.PipelineResult, error) {
	if d == nil {
		return execution.PipelineResult{
			InvocationResult: execution.InvocationResult{
				InvocationID: request.InvocationID,
				Output:       json.RawMessage(`{"dryRun":true}`),
				HTTPStatus:   200,
			},
		}, nil
	}

	args := request.Input
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	} else {
		// Defensive copy so callers cannot mutate recorded history.
		copied := make(json.RawMessage, len(args))
		copy(copied, args)
		args = copied
	}

	toolName := ""
	if d.NameByCapability != nil {
		toolName = d.NameByCapability[request.CapabilityID]
	}

	d.mu.Lock()
	d.Calls = append(d.Calls, DryRunToolCall{
		InvocationID: request.InvocationID,
		CapabilityID: request.CapabilityID,
		ReleaseID:    request.ReleaseID,
		ToolName:     toolName,
		Args:         args,
	})
	output := d.lookupOutputLocked(request.CapabilityID)
	d.mu.Unlock()

	return execution.PipelineResult{
		InvocationResult: execution.InvocationResult{
			InvocationID: request.InvocationID,
			Output:       output,
			HTTPStatus:   200,
		},
	}, nil
}

func (d *DryRunToolInvoker) lookupOutputLocked(capabilityID string) json.RawMessage {
	if d.OutputsByCapability != nil {
		if out, ok := d.OutputsByCapability[capabilityID]; ok && len(out) > 0 {
			copied := make(json.RawMessage, len(out))
			copy(copied, out)
			return copied
		}
	}
	if len(d.DefaultOutput) > 0 {
		copied := make(json.RawMessage, len(d.DefaultOutput))
		copy(copied, d.DefaultOutput)
		return copied
	}
	return json.RawMessage(`{"dryRun":true}`)
}

// RecordedCalls returns a snapshot of ordered DryRun tool calls.
func (d *DryRunToolInvoker) RecordedCalls() []DryRunToolCall {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]DryRunToolCall, len(d.Calls))
	copy(out, d.Calls)
	return out
}

// RecordedToolNames returns callable names in call order (empty string when unknown).
func (d *DryRunToolInvoker) RecordedToolNames() []string {
	calls := d.RecordedCalls()
	names := make([]string, len(calls))
	for i, c := range calls {
		names[i] = c.ToolName
	}
	return names
}

// RecordedArgsJSON returns args JSON strings in call order.
func (d *DryRunToolInvoker) RecordedArgsJSON() []string {
	calls := d.RecordedCalls()
	args := make([]string, len(calls))
	for i, c := range calls {
		args[i] = string(c.Args)
	}
	return args
}

// CallCount returns the number of recorded InvokeResolved calls.
func (d *DryRunToolInvoker) CallCount() int {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.Calls)
}
