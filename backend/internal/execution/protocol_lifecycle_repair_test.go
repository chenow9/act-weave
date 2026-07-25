package execution_test

import (
	"context"
	"testing"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
)

func TestProtocolLifecycleRepairDualTransactionGap(t *testing.T) {
	ctx := context.Background()
	db, runs, _ := newConfirmationResumeFixture(t)
	// Domain Run exists (from fixtures) but no protocol stream — classic dual-tx gap.
	run, err := runs.GetAgentRun(ctx, executionWorkspaceID, executionAgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "RUNNING" {
		// Fixture starts RUNNING; if not, force for this drill.
		t.Fatalf("fixture run status=%s", run.Status)
	}

	// Ensure no protocol stream for this run yet.
	var streamCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM protocol_event_streams
		WHERE workspace_id=$1 AND run_id=$2
	`, executionWorkspaceID, executionAgentRunID).Scan(&streamCount); err != nil {
		t.Fatal(err)
	}
	if streamCount != 0 {
		if _, err := db.Exec(`
			DELETE FROM protocol_events WHERE workspace_id=$1 AND run_id=$2;
			DELETE FROM protocol_event_streams WHERE workspace_id=$1 AND run_id=$2;
		`, executionWorkspaceID, executionAgentRunID); err != nil {
			t.Fatal(err)
		}
	}

	notifier := protocolevent.NewInProcessLiveNotifier()
	defer notifier.Close()
	unit, err := protocolevent.NewProtocolUnitOfWork(db, notifier)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := execution.NewProtocolRunLifecycleService(runs, unit)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	repair, err := execution.NewProtocolLifecycleRepair(runs, lifecycle, reader)
	if err != nil {
		t.Fatal(err)
	}

	first, err := repair.EnsureStartedEvents(ctx, executionWorkspaceID, executionAgentRunID)
	if err != nil || len(first.Events) < 2 {
		t.Fatalf("first repair=%+v err=%v", first, err)
	}
	if first.Events[0].Type != protocolevent.EventRunAccepted ||
		first.Events[1].Type != protocolevent.EventRunStarted {
		t.Fatalf("unexpected event types: %+v", first.Events)
	}

	// Idempotent: second repair returns existing facts, no duplicate sequences.
	second, err := repair.EnsureStartedEvents(ctx, executionWorkspaceID, executionAgentRunID)
	if err != nil || len(second.Events) < 2 {
		t.Fatalf("second repair=%+v err=%v", second, err)
	}
	if second.Events[0].ID != first.Events[0].ID || second.Events[1].ID != first.Events[1].ID {
		t.Fatalf("repair not idempotent: first=%+v second=%+v", first.Events, second.Events)
	}
	var eventCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM protocol_events WHERE workspace_id=$1 AND run_id=$2
	`, executionWorkspaceID, executionAgentRunID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 2 {
		t.Fatalf("event count=%d want=2 (no duplicates)", eventCount)
	}
}

// Regression: ListRunsMissingStartedEvents must query agent_runs.started_at
// (not created_at — the column does not exist and caused LIFECYCLE_LIST_FAILED).
func TestListRunsMissingStartedEventsUsesStartedAt(t *testing.T) {
	ctx := context.Background()
	db, runs, _ := newConfirmationResumeFixture(t)

	// started_at is immutable via DB trigger; minAge=0 makes any RUNNING run eligible.
	if _, err := db.Exec(`
		DELETE FROM protocol_events WHERE workspace_id=$1 AND run_id=$2
	`, executionWorkspaceID, executionAgentRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		DELETE FROM protocol_event_streams WHERE workspace_id=$1 AND run_id=$2
	`, executionWorkspaceID, executionAgentRunID); err != nil {
		t.Fatal(err)
	}

	notifier := protocolevent.NewInProcessLiveNotifier()
	defer notifier.Close()
	unit, err := protocolevent.NewProtocolUnitOfWork(db, notifier)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := execution.NewProtocolRunLifecycleService(runs, unit)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	repair, err := execution.NewProtocolLifecycleRepair(runs, lifecycle, reader)
	if err != nil {
		t.Fatal(err)
	}

	// Zero minAge so the aged fixture is always eligible.
	candidates, err := repair.ListRunsMissingStartedEvents(ctx, 50, 0)
	if err != nil {
		t.Fatalf("ListRunsMissingStartedEvents failed (likely wrong column): %v", err)
	}
	found := false
	for _, c := range candidates {
		if c.WorkspaceID == executionWorkspaceID && c.RunID == executionAgentRunID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected fixture run in candidates, got %+v", candidates)
	}
}
