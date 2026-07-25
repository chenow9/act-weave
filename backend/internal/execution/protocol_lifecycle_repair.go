package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/protocolevent"
)

// ProtocolLifecycleRepair closes the dual-transaction gap between domain Run
// creation and Protocol Event projection (Chat Message/Run commit, then
// RecordStartedAgentRun). Deterministic event IDs make the repair idempotent.
type ProtocolLifecycleRepair struct {
	runs      *RunRepository
	lifecycle *ProtocolRunLifecycleService
	events    *protocolevent.EventReader
}

func NewProtocolLifecycleRepair(
	runs *RunRepository,
	lifecycle *ProtocolRunLifecycleService,
	events *protocolevent.EventReader,
) (*ProtocolLifecycleRepair, error) {
	if runs == nil || lifecycle == nil || events == nil {
		return nil, errors.New("protocol lifecycle repair dependencies are required")
	}
	return &ProtocolLifecycleRepair{runs: runs, lifecycle: lifecycle, events: events}, nil
}

// EnsureStartedEvents projects run.accepted + run.started when a RUNNING Run
// exists without a protocol stream (or without the initial pair). Safe to call
// repeatedly after process crash between domain commit and protocol UOW.
func (repair *ProtocolLifecycleRepair) EnsureStartedEvents(
	ctx context.Context,
	workspaceID, runID string,
) (ProtocolRunLifecycleResult, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	runID = strings.TrimSpace(runID)
	if repair == nil || repair.runs == nil || repair.lifecycle == nil || repair.events == nil ||
		ctx == nil || !invocationValidUUID(workspaceID) || !invocationValidUUID(runID) {
		return ProtocolRunLifecycleResult{}, ErrRunInvalid
	}
	run, err := repair.runs.GetAgentRun(ctx, workspaceID, runID)
	if err != nil {
		return ProtocolRunLifecycleResult{}, err
	}
	if run.Status != "RUNNING" && run.Status != "WAITING_CONFIRMATION" &&
		run.Status != "SUCCEEDED" && run.Status != "FAILED" && run.Status != "CANCELLED" {
		return ProtocolRunLifecycleResult{}, ErrRunInvalid
	}
	// Only repair the dual-tx gap for Runs that still expect the initial pair.
	// Terminal runs without events are out of scope (would need full replay).
	if run.Status != "RUNNING" && run.Status != "WAITING_CONFIRMATION" {
		return ProtocolRunLifecycleResult{Run: run}, nil
	}
	scope := protocolevent.RunScope{
		WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
		ConversationID: run.SessionID, RunID: run.ID,
	}
	events, err := repair.events.ReadRunAfter(ctx, scope, 0, 2)
	if errors.Is(err, protocolevent.ErrRunScopeNotFound) {
		// Domain fact exists; protocol stream missing — classic dual-tx crash.
		if run.Status != "RUNNING" {
			// Waiting without protocol start is still repairable if status was
			// advanced only in domain; prefer projecting from current snapshot.
			// RecordStartedAgentRun requires RUNNING — skip non-running gap.
			return ProtocolRunLifecycleResult{}, fmt.Errorf(
				"protocol lifecycle repair: run %s status %s has no stream: %w",
				run.ID, run.Status, ErrRunConflict,
			)
		}
		return repair.lifecycle.RecordStartedAgentRun(ctx, run)
	}
	if err != nil {
		return ProtocolRunLifecycleResult{}, err
	}
	if len(events) >= 2 &&
		events[0].Type == protocolevent.EventRunAccepted &&
		events[1].Type == protocolevent.EventRunStarted {
		return ProtocolRunLifecycleResult{Run: run, Events: events}, nil
	}
	if len(events) == 0 && run.Status == "RUNNING" {
		return repair.lifecycle.RecordStartedAgentRun(ctx, run)
	}
	// Partial stream with unexpected head — do not invent events.
	return ProtocolRunLifecycleResult{Run: run, Events: events}, nil
}
