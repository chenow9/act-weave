package execution

import (
	"context"

	"actweave/backend/internal/outboundidentity"
)

// RuntimeRouterContinuationGate adapts outboundidentity.RuntimeRouter to
// OutboundContinuationGate without exporting router internals into recovery.
type RuntimeRouterContinuationGate struct {
	Router *outboundidentity.RuntimeRouter
}

// GateAgentRunContinuation implements OutboundContinuationGate.
func (g RuntimeRouterContinuationGate) GateAgentRunContinuation(
	ctx context.Context, workspaceID, runID string,
) (OutboundGateResult, error) {
	if g.Router == nil {
		return OutboundGateResult{Allow: true}, nil
	}
	gate, err := g.Router.GateContinuation(ctx, workspaceID, runID)
	if err != nil {
		return OutboundGateResult{}, err
	}
	return OutboundGateResult{
		Allow:      gate.Allow,
		Skip:       gate.Skip,
		FailClosed: gate.FailClosed,
		ReasonCode: gate.Decision.ReasonCode,
	}, nil
}

// Ensure interface compliance.
var _ OutboundContinuationGate = RuntimeRouterContinuationGate{}
