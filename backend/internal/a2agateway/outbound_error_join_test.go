package a2agateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"

	"github.com/google/uuid"
)

// TestOutboundFinalizeJoinsCallError: finalize+outbox failures still produce a client
// result, but the public JSON must stay stable (no inject/outbox/SQL detail leak).
func TestOutboundFinalizeJoinsCallError(t *testing.T) {
	t.Parallel()
	audit := &joinFailAudit{failFinalize: true}
	var outboxN int
	// Unreachable host forces call failure after attempt.
	tool, err := a2agateway.NewOutboundTool(a2agateway.OutboundConfig{
		Binding: a2agateway.RemoteBinding{
			ID: uuid.Must(uuid.NewV7()).String(), CallableName: "remote",
			EndpointURL: "https://127.0.0.1:1/a2a", TimeoutMs: 200, Enabled: true,
			AllowedHosts: []string{"127.0.0.1"},
		},
		Audit:           audit,
		AllowHTTP:       true,
		FinalizeRetries: 1,
		EnqueueFinalizeOutbox: func(ctx context.Context, workspaceID, delegationID, stepID string, payload json.RawMessage) error {
			outboxN++
			return errors.New("inject: outbox failed")
		},
		// Use a client that fails immediately without real network if needed —
		// SecureHTTPClient will dial 127.0.0.1:1 and fail connection refused.
		HTTPClient: a2agateway.SecureHTTPClient(200*time.Millisecond, []string{"127.0.0.1"}, a2agateway.EgressPolicy{AllowHTTP: true}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	rc := &agentdelegation.RunContext{
		WorkspaceID: ws, ParentRunID: run, RootRunID: run, RunID: run,
		CallerAgentID: uuid.Must(uuid.NewV7()).String(), Depth: 0,
		Budget: agentdelegation.NewBudget(),
	}
	out, invErr := tool.InvokableRun(agentdelegation.WithRunContext(context.Background(), rc), `{"q":"x"}`)
	_ = invErr
	// Public client payload: stable code + traceRef only.
	if !strings.Contains(out, "errorCode") || !strings.Contains(out, "traceRef") {
		t.Fatalf("want public error shape, out=%s", out)
	}
	lower := strings.ToLower(out)
	for _, bad := range []string{"inject:", "outbox failed", "sql:", "pq:"} {
		if strings.Contains(lower, bad) {
			t.Fatalf("public result leaked internal detail %q: %s", bad, out)
		}
	}
	// Join path still exercises finalize retries (outbox enqueue attempted).
	if outboxN < 1 && audit.finalizeCalls < 1 {
		t.Fatalf("expected finalize and/or outbox join path; finalizeCalls=%d outboxN=%d", audit.finalizeCalls, outboxN)
	}
}

type joinFailAudit struct {
	failFinalize  bool
	finalizeCalls int
	rows          map[string]agentdelegation.Delegation
}

func (j *joinFailAudit) CreateDelegationAndStep(_ context.Context, in agentdelegation.CreateDelegationInput) (agentdelegation.Delegation, bool, error) {
	if j.rows == nil {
		j.rows = map[string]agentdelegation.Delegation{}
	}
	d := agentdelegation.Delegation{
		ID: in.ID, WorkspaceID: in.WorkspaceID, ParentRunID: in.ParentRunID,
		Status: agentdelegation.StatusRunning, StepID: in.StepID,
	}
	j.rows[in.IdempotencyKey] = d
	return d, false, nil
}
func (j *joinFailAudit) FinalizeDelegation(context.Context, agentdelegation.FinalizeDelegationInput) (agentdelegation.Delegation, error) {
	j.finalizeCalls++
	if j.failFinalize {
		return agentdelegation.Delegation{}, errors.New("inject: finalize failed")
	}
	return agentdelegation.Delegation{}, nil
}
func (j *joinFailAudit) SetChildRunID(context.Context, string, string, string) error { return nil }
func (j *joinFailAudit) RecordDispatchAttempt(context.Context, string, string) error {
	return nil
}
func (j *joinFailAudit) AccumulateModelTokens(context.Context, string, string, agentdelegation.TokenUsage) error {
	return nil
}
