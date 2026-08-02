package agentdelegation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Residual #6 (unit): cancel terminal is sticky — Finalize retry with SUCCEEDED
// or FAILED must not overwrite CANCELLED.
func TestAuditedAgentTool_Cancel_TerminalNotOverwritten(t *testing.T) {
	t.Parallel()
	audit := &memAudit{}
	children := &memChildRuns{}
	// Blocking until cancel.
	blocking := &blockingInner{}
	edge := GraphEdgeSnapshot{
		BindingID: "cancel-b", CallerAgentID: uuid.Must(uuid.NewV7()).String(),
		TargetAgentID: uuid.Must(uuid.NewV7()).String(),
		CallableName:  "slow", Mode: ModeTask, Version: 1,
	}
	tool, err := NewAuditedAgentTool(AgentToolConfig{
		Inner: blocking, Name: "slow", Edge: edge, Audit: audit,
		DefaultCallerAgentID: edge.CallerAgentID, ChildRuns: children,
		DefaultTaskTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, parent := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithRunContext(ctx, &RunContext{
		WorkspaceID: ws, ParentRunID: parent, RootRunID: parent, RunID: parent,
		CallerAgentID: edge.CallerAgentID, Budget: NewBudget(),
	})
	done := make(chan string, 1)
	go func() {
		out, _ := tool.InvokableRun(ctx, `{"request":"x"}`)
		done <- out
	}()
	// Allow dispatch then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for cancel")
	}
	audit.mu.Lock()
	var delID string
	for _, d := range audit.rows {
		if d.Status != StatusCancelled {
			t.Fatalf("status=%s want CANCELLED", d.Status)
		}
		delID = d.ID
		if d.ChildRunID != nil {
			if children.finished[*d.ChildRunID] != StatusCancelled {
				t.Fatalf("child finish=%s", children.finished[*d.ChildRunID])
			}
		}
	}
	audit.mu.Unlock()
	if delID == "" {
		t.Fatal("no delegation")
	}
	// Retry finalize as SUCCEEDED — sticky: different terminal → ErrConflict.
	_, err = audit.FinalizeDelegation(context.Background(), FinalizeDelegationInput{
		WorkspaceID: ws, DelegationID: delID, StepID: uuid.Must(uuid.NewV7()).String(),
		Status: StatusSucceeded, OutputSummary: json.RawMessage(`{"ok":true}`),
		OutputPayload: json.RawMessage(`{"result":"nope"}`),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict on SUCCEEDED overwrite, got %v", err)
	}
	// FAILED overwrite also ErrConflict; original CANCELLED sticky.
	_, err = audit.FinalizeDelegation(context.Background(), FinalizeDelegationInput{
		WorkspaceID: ws, DelegationID: delID, StepID: uuid.Must(uuid.NewV7()).String(),
		Status: StatusFailed, ErrorCode: "X", OutputSummary: json.RawMessage(`{}`),
		OutputPayload: json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict on FAILED overwrite, got %v", err)
	}
	audit.mu.Lock()
	st := ""
	for _, d := range audit.rows {
		if d.ID == delID {
			st = d.Status
		}
	}
	audit.mu.Unlock()
	if st != StatusCancelled {
		t.Fatalf("sticky status overwritten to %s", st)
	}
}
