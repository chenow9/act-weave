package execution_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

func TestProtocolInteractionDecisionCommitsTerminalFactsAtomically(t *testing.T) {
	ctx := context.Background()
	db, runs, confirmations := newConfirmationResumeFixture(t)
	resolved, request := resumeToolResolution()
	requestSnapshot, resolvedSnapshot, err := execution.BuildToolConfirmationResumeSnapshots(request, resolved)
	if err != nil {
		t.Fatal(err)
	}
	var effects atomic.Int32
	registry, err := execution.NewConfirmationResumeRegistry(&failingDecisionExecutor{effects: &effects})
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := execution.NewConfirmationResumeRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	resumes, err := execution.NewConfirmationResumeService(checkpoints, confirmations, runs, registry)
	if err != nil {
		t.Fatal(err)
	}
	run, err := runs.GetAgentRun(ctx, executionWorkspaceID, executionAgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := resumes.Prepare(ctx, execution.PrepareConfirmationResumeInput{
		Confirmation: execution.RequestExecutionConfirmationInput{
			ID: resumeConfirmationID, WorkspaceID: executionWorkspaceID,
			RunID: executionAgentRunID, TargetItemID: resumeInvocationID,
			NodeID: "protocol-interaction", ReleaseID: invocationReleaseID,
			ConnectionID: invocationConnectionID, PlanHash: executionPlanHash,
			RequestedBy: executionOwnerID,
			Decision: resumeDecision(t, request.Input, invocationReleaseID,
				invocationConnectionID, "PRODUCTION", true),
		},
		Kind:                  execution.ResumeKindTool,
		SnapshotSchemaVersion: execution.ConfirmationResumeSnapshotVersion,
		RequestSnapshot:       requestSnapshot, ResolvedSnapshot: resolvedSnapshot,
		Input: request.Input, ExpectedRunLockVersion: run.LockVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	unit, err := protocolevent.NewProtocolUnitOfWork(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	interactionContext := execution.ProtocolInteractionContext{
		Scope: protocolevent.RunScope{
			WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
			ConversationID: run.SessionID, RunID: run.ID,
		},
		EventStreamID: run.ID, TraceID: run.TraceID,
	}
	target := protocolevent.ToolCallItem{
		ID: resumeInvocationID, Type: protocolevent.ItemTypeToolCall,
		Status: protocolevent.ItemStatusInProgress, Name: "bound-tool",
		Arguments: json.RawMessage(`{"safe":true}`),
	}
	startedID := uuid.Must(uuid.NewV7()).String()
	_, err = unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
		if _, err := transaction.EnsureRunEventStream(ctx, run.ID, interactionContext.Scope); err != nil {
			return err
		}
		if _, err := transaction.CreateRunItem(ctx, protocolevent.CreateRunItemInput{
			WorkspaceID: run.WorkspaceID, AgentID: run.AgentID, RunID: run.ID,
			Ordinal: 1, SourceType: protocolevent.SourceToolInvocation,
			SourceID: resumeInvocationID, Item: target, StartedAt: run.StartedAt,
		}); err != nil {
			return err
		}
		event, err := protocolevent.BuildProtocolEvent(protocolevent.NewProtocolEvent{
			ID: startedID, EventStreamID: run.ID, WorkspaceID: run.WorkspaceID,
			AgentID: run.AgentID, ConversationID: run.SessionID, RunID: run.ID,
			Type: protocolevent.EventItemStarted, SpecVersion: "1.0", TraceID: run.TraceID,
			ItemID: target.ID, OccurredAt: run.StartedAt,
		}, protocolevent.ItemSnapshotData{Item: target})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	presentation := execution.InteractionPresentation{
		TargetItemID: resumeInvocationID, Title: "Approve tool action",
		RiskLevel:    "medium",
		RiskReasons:  append([]string(nil), prepared.Requested.Confirmation.RiskReasons...),
		InputSummary: json.RawMessage(`{"safe":true}`),
		AllowedDecisions: []protocolevent.InteractionDecision{
			protocolevent.InteractionDecisionApprove,
			protocolevent.InteractionDecisionCancel,
		},
		RequiredDecider: protocolevent.RequiredDeciderActWeaveUser,
	}
	projector, err := execution.NewProtocolInteractionProjector(
		unit, execution.NewInteractionProtocolMapper(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projector.ProjectRequested(ctx, execution.ProjectInteractionRequestedInput{
		Context: interactionContext, Confirmation: prepared.Requested.Confirmation,
		Presentation: presentation, Ordinal: 2,
	}); err != nil {
		t.Fatal(err)
	}
	core, err := execution.NewInteractionDecisionService(confirmations, resumes)
	if err != nil {
		t.Fatal(err)
	}
	protocolDecisions, err := execution.NewProtocolInteractionDecisionService(
		core, unit, execution.NewInteractionProtocolMapper(),
	)
	if err != nil {
		t.Fatal(err)
	}
	confirmation := prepared.Requested.Confirmation
	decisionInput := execution.ProtocolInteractionDecisionInput{
		Context: interactionContext, Presentation: presentation,
		Decision: execution.DecideInteractionInput{
			WorkspaceID: run.WorkspaceID, ConfirmationID: confirmation.ID,
			ActorID: executionOwnerID, Decision: execution.InteractionDecisionCancel,
			IdempotencyKey: interactionDecisionKeyOne,
			Binding: execution.InteractionDecisionBinding{
				RunID: confirmation.RunID, TargetItemID: confirmation.TargetItemID,
				ReleaseID: confirmation.ReleaseID, InputHash: confirmation.InputHash,
				ConnectionID: confirmation.ConnectionID, PlanHash: confirmation.PlanHash,
				Version: confirmation.LockVersion, ExpiresAt: confirmation.ExpiresAt,
				BindingHash: confirmation.InteractionBindingHash,
			},
		},
	}
	decided, err := protocolDecisions.Decide(ctx, decisionInput)
	if err != nil {
		t.Fatal(err)
	}
	if decided.Decision.Cached || decided.Projection.Status != protocolevent.ItemStatusCancelled ||
		len(decided.Events) != 2 ||
		decided.Events[0].Type != protocolevent.EventInteractionResolved ||
		decided.Events[1].Type != protocolevent.EventRunCancelled || effects.Load() != 0 {
		t.Fatalf("decision=%+v events=%+v effects=%d", decided.Decision, decided.Events, effects.Load())
	}
	terminalRun, err := runs.GetAgentRun(ctx, run.WorkspaceID, run.ID)
	if err != nil || terminalRun.Status != "CANCELLED" {
		t.Fatalf("run=%+v err=%v", terminalRun, err)
	}
	var eventCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM protocol_events WHERE workspace_id=$1 AND run_id=$2`,
		run.WorkspaceID, run.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	replayed, err := protocolDecisions.Decide(ctx, decisionInput)
	if err != nil || !replayed.Decision.Cached || len(replayed.Events) != 0 {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	var replayedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM protocol_events WHERE workspace_id=$1 AND run_id=$2`,
		run.WorkspaceID, run.ID).Scan(&replayedCount); err != nil {
		t.Fatal(err)
	}
	if replayedCount != eventCount {
		t.Fatalf("idempotent replay appended events: before=%d after=%d", eventCount, replayedCount)
	}
}
