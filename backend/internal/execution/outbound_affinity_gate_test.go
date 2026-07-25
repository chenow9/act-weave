package execution

import (
	"context"
	"testing"
)

type stubOutboundGate struct {
	result OutboundGateResult
	err    error
}

func (s stubOutboundGate) GateAgentRunContinuation(ctx context.Context, workspaceID, runID string) (OutboundGateResult, error) {
	return s.result, s.err
}

func TestOutboundGateResultShapes(t *testing.T) {
	gate := stubOutboundGate{result: OutboundGateResult{FailClosed: true, ReasonCode: "OUTBOUND_CREDENTIAL_EXPIRED"}}
	res, err := gate.GateAgentRunContinuation(context.Background(), "ws", "run")
	if err != nil || !res.FailClosed {
		t.Fatalf("fail closed: %+v %v", res, err)
	}
	gate = stubOutboundGate{result: OutboundGateResult{Skip: true}}
	res, err = gate.GateAgentRunContinuation(context.Background(), "ws", "run")
	if err != nil || !res.Skip || res.Allow {
		t.Fatalf("skip: %+v %v", res, err)
	}
	gate = stubOutboundGate{result: OutboundGateResult{Allow: true}}
	res, err = gate.GateAgentRunContinuation(context.Background(), "ws", "run")
	if err != nil || !res.Allow {
		t.Fatalf("allow: %+v %v", res, err)
	}
	// Nil router adapter allows (pure broker path).
	adapter := RuntimeRouterContinuationGate{}
	res, err = adapter.GateAgentRunContinuation(context.Background(), "ws", "run")
	if err != nil || !res.Allow {
		t.Fatalf("nil router allow: %+v %v", res, err)
	}
}
