package execution_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

func TestRunLifecycleProtocolEvents(t *testing.T) {
	ctx := context.Background()
	repository, runService, db, _ := newRunStateTest(t)
	db.SetMaxOpenConns(16)
	unit, err := protocolevent.NewProtocolUnitOfWork(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := execution.NewProtocolRunLifecycleService(repository, unit)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("accepted waiting resumed and completed", func(t *testing.T) {
		started := startProtocolLifecycleRun(t, ctx, runService, lifecycle, "complete")
		assertLifecycleEventTypes(t, reader, started.Run, []string{"run.accepted", "run.started"})
		assertLifecycleSnapshot(t, started.Events[0], protocolevent.RunStatusAccepted, "")
		assertLifecycleSnapshot(t, started.Events[1], protocolevent.RunStatusRunning, "")

		interactionID := uuid.NewString()
		waiting, err := lifecycle.TransitionAgentRun(ctx, execution.ProtocolRunTransitionInput{
			WorkspaceID: executionWorkspaceID, RunID: started.Run.ID,
			Transition: execution.RunTransition{
				ExpectedStatus: "RUNNING", ExpectedLockVersion: started.Run.LockVersion,
				NewStatus: "WAITING_CONFIRMATION", OutputSummary: json.RawMessage(`{"waiting":true}`),
			},
			InteractionIDs: []string{interactionID},
		})
		if err != nil || waiting.Run.Status != "WAITING_CONFIRMATION" {
			t.Fatalf("wait lifecycle result=%+v err=%v", waiting, err)
		}
		assertLifecycleSnapshot(t, waiting.Events[0], protocolevent.RunStatusWaitingInteraction, "")

		resumed, err := lifecycle.TransitionAgentRun(ctx, execution.ProtocolRunTransitionInput{
			WorkspaceID: executionWorkspaceID, RunID: started.Run.ID,
			Transition: execution.RunTransition{
				ExpectedStatus: "WAITING_CONFIRMATION", ExpectedLockVersion: waiting.Run.LockVersion,
				NewStatus: "RUNNING", OutputSummary: json.RawMessage(`{"resumed":true}`),
			},
			InteractionID: interactionID,
		})
		if err != nil || resumed.Run.Status != "RUNNING" {
			t.Fatalf("resume lifecycle result=%+v err=%v", resumed, err)
		}
		assertLifecycleSnapshot(t, resumed.Events[0], protocolevent.RunStatusRunning, "")

		completed, err := lifecycle.TransitionAgentRun(ctx, execution.ProtocolRunTransitionInput{
			WorkspaceID: executionWorkspaceID, RunID: started.Run.ID,
			Transition: execution.RunTransition{
				ExpectedStatus: "RUNNING", ExpectedLockVersion: resumed.Run.LockVersion,
				NewStatus: "SUCCEEDED", OutputSummary: json.RawMessage(`{"answer":"ok"}`),
			},
		})
		if err != nil || completed.Run.Status != "SUCCEEDED" || completed.Run.FinishedAt == nil {
			t.Fatalf("complete lifecycle result=%+v err=%v", completed, err)
		}
		assertLifecycleSnapshot(t, completed.Events[0], protocolevent.RunStatusCompleted, "")
		assertLifecycleEventTypes(t, reader, completed.Run, []string{
			"run.accepted", "run.started", "run.waiting", "run.resumed", "run.completed",
		})
		stored, err := repository.GetAgentRun(ctx, executionWorkspaceID, completed.Run.ID)
		if err != nil || stored.Status != completed.Run.Status || stored.LockVersion != completed.Run.LockVersion {
			t.Fatalf("authoritative run mismatch stored=%+v result=%+v err=%v", stored, completed.Run, err)
		}
	})

	t.Run("failed snapshot uses stable public error", func(t *testing.T) {
		started := startProtocolLifecycleRun(t, ctx, runService, lifecycle, "failed")
		_, err := lifecycle.TransitionAgentRun(ctx, execution.ProtocolRunTransitionInput{
			WorkspaceID: executionWorkspaceID, RunID: started.Run.ID,
			Transition: execution.RunTransition{
				ExpectedStatus: "RUNNING", ExpectedLockVersion: started.Run.LockVersion,
				NewStatus: "FAILED", OutputSummary: json.RawMessage(`{}`), ErrorCode: "bad code",
			},
		})
		if !errors.Is(err, execution.ErrRunInvalid) {
			t.Fatalf("unstable error code error=%v", err)
		}
		stored, err := repository.GetAgentRun(ctx, executionWorkspaceID, started.Run.ID)
		if err != nil || stored.Status != "RUNNING" || stored.LockVersion != 1 {
			t.Fatalf("invalid failure changed run=%+v err=%v", stored, err)
		}

		failed, err := lifecycle.TransitionAgentRun(ctx, execution.ProtocolRunTransitionInput{
			WorkspaceID: executionWorkspaceID, RunID: started.Run.ID,
			Transition: execution.RunTransition{
				ExpectedStatus: "RUNNING", ExpectedLockVersion: started.Run.LockVersion,
				NewStatus: "FAILED", OutputSummary: json.RawMessage(`{"stage":"model"}`),
				ErrorCode: "MODEL_PROVIDER_FAILED",
			},
		})
		if err != nil || failed.Run.Status != "FAILED" {
			t.Fatalf("fail lifecycle result=%+v err=%v", failed, err)
		}
		assertLifecycleSnapshot(t, failed.Events[0], protocolevent.RunStatusFailed, "MODEL_PROVIDER_FAILED")
		assertLifecycleEventTypes(t, reader, failed.Run, []string{"run.accepted", "run.started", "run.failed"})
	})

	t.Run("waiting run can be cancelled", func(t *testing.T) {
		started := startProtocolLifecycleRun(t, ctx, runService, lifecycle, "cancelled")
		waiting, err := lifecycle.TransitionAgentRun(ctx, execution.ProtocolRunTransitionInput{
			WorkspaceID: executionWorkspaceID, RunID: started.Run.ID,
			Transition: execution.RunTransition{
				ExpectedStatus: "RUNNING", ExpectedLockVersion: started.Run.LockVersion,
				NewStatus: "WAITING_CONFIRMATION", OutputSummary: json.RawMessage(`{}`),
			},
			InteractionIDs: []string{uuid.NewString()},
		})
		if err != nil {
			t.Fatal(err)
		}
		cancelled, err := lifecycle.TransitionAgentRun(ctx, execution.ProtocolRunTransitionInput{
			WorkspaceID: executionWorkspaceID, RunID: started.Run.ID,
			Transition: execution.RunTransition{
				ExpectedStatus: "WAITING_CONFIRMATION", ExpectedLockVersion: waiting.Run.LockVersion,
				NewStatus: "CANCELLED", OutputSummary: json.RawMessage(`{"reason":"user"}`),
			},
		})
		if err != nil || cancelled.Run.Status != "CANCELLED" {
			t.Fatalf("cancel lifecycle result=%+v err=%v", cancelled, err)
		}
		assertLifecycleSnapshot(t, cancelled.Events[0], protocolevent.RunStatusCancelled, "")
		assertLifecycleEventTypes(t, reader, cancelled.Run, []string{
			"run.accepted", "run.started", "run.waiting", "run.cancelled",
		})
	})

	t.Run("concurrent terminal transition has one fact", func(t *testing.T) {
		started := startProtocolLifecycleRun(t, ctx, runService, lifecycle, "terminal-race")
		type result struct {
			value execution.ProtocolRunLifecycleResult
			err   error
		}
		start := make(chan struct{})
		results := make(chan result, 2)
		var wait sync.WaitGroup
		for _, transition := range []execution.RunTransition{
			{
				ExpectedStatus: "RUNNING", ExpectedLockVersion: started.Run.LockVersion,
				NewStatus: "SUCCEEDED", OutputSummary: json.RawMessage(`{"winner":"success"}`),
			},
			{
				ExpectedStatus: "RUNNING", ExpectedLockVersion: started.Run.LockVersion,
				NewStatus: "FAILED", OutputSummary: json.RawMessage(`{"winner":"failure"}`),
				ErrorCode: "RUNTIME_FAILED",
			},
		} {
			transition := transition
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				value, err := lifecycle.TransitionAgentRun(ctx, execution.ProtocolRunTransitionInput{
					WorkspaceID: executionWorkspaceID, RunID: started.Run.ID, Transition: transition,
				})
				results <- result{value: value, err: err}
			}()
		}
		close(start)
		wait.Wait()
		close(results)
		successes, conflicts := 0, 0
		for outcome := range results {
			switch {
			case outcome.err == nil:
				successes++
				if len(outcome.value.Events) != 1 {
					t.Fatalf("terminal winner events=%+v", outcome.value.Events)
				}
			case errors.Is(outcome.err, execution.ErrRunConflict):
				conflicts++
			default:
				t.Fatalf("terminal race error=%v", outcome.err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("terminal race successes=%d conflicts=%d", successes, conflicts)
		}
		events := readLifecycleEvents(t, reader, started.Run)
		terminalCount := 0
		for _, event := range events {
			if event.Type == "run.completed" || event.Type == "run.failed" || event.Type == "run.cancelled" {
				terminalCount++
			}
		}
		if len(events) != 3 || terminalCount != 1 {
			t.Fatalf("terminal race persisted events=%+v", events)
		}
	})
}

func startProtocolLifecycleRun(
	t *testing.T,
	ctx context.Context,
	runService *execution.RunService,
	lifecycle *execution.ProtocolRunLifecycleService,
	suffix string,
) execution.ProtocolRunLifecycleResult {
	t.Helper()
	request := runStateAgentRequest(uuid.NewString(), "trace-protocol-lifecycle-"+suffix)
	prepared, err := runService.PrepareAgentRun(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := lifecycle.AcceptAndStartAgentRun(ctx, prepared)
	if err != nil {
		t.Fatalf("accept/start protocol run: %v", err)
	}
	if result.Run.ID != request.ID || result.Run.Status != "RUNNING" || len(result.Events) != 2 ||
		result.NotifyError != nil {
		t.Fatalf("accepted protocol run=%+v", result)
	}
	return result
}

func assertLifecycleEventTypes(
	t *testing.T,
	reader *protocolevent.EventReader,
	run execution.AgentRun,
	expected []string,
) {
	t.Helper()
	events := readLifecycleEvents(t, reader, run)
	if len(events) != len(expected) {
		t.Fatalf("run %s event count=%d want=%d events=%+v", run.ID, len(events), len(expected), events)
	}
	for index, event := range events {
		if event.Type != expected[index] || event.Sequence != int64(index+1) {
			t.Fatalf("run %s event[%d]=%s/%d want=%s/%d",
				run.ID, index, event.Type, event.Sequence, expected[index], index+1)
		}
	}
}

func readLifecycleEvents(
	t *testing.T,
	reader *protocolevent.EventReader,
	run execution.AgentRun,
) []protocolevent.ProtocolEvent {
	t.Helper()
	events, err := reader.ReadRunAfter(context.Background(), protocolevent.RunScope{
		WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
		ConversationID: run.SessionID, RunID: run.ID,
	}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func assertLifecycleSnapshot(
	t *testing.T,
	event protocolevent.ProtocolEvent,
	expectedStatus protocolevent.RunStatus,
	expectedErrorCode string,
) {
	t.Helper()
	data, err := protocolevent.DecodeEventData(event.Type, event.Data)
	if err != nil {
		t.Fatalf("decode lifecycle event %s: %v", event.Type, err)
	}
	var snapshot protocolevent.Run
	switch value := data.(type) {
	case protocolevent.RunSnapshotData:
		snapshot = value.Run
	case protocolevent.RunWaitingData:
		snapshot = value.Run
	case protocolevent.RunResumedData:
		snapshot = value.Run
	default:
		t.Fatalf("unexpected lifecycle data %T", data)
	}
	if snapshot.ID != event.RunID || snapshot.AgentID != event.AgentID ||
		snapshot.ConversationID != event.ConversationID || snapshot.Status != expectedStatus {
		t.Fatalf("event/snapshot mismatch event=%+v snapshot=%+v", event, snapshot)
	}
	if expectedErrorCode == "" {
		if snapshot.Error != nil {
			t.Fatalf("unexpected public run error=%+v", snapshot.Error)
		}
		return
	}
	if snapshot.Error == nil || snapshot.Error.Code != expectedErrorCode ||
		snapshot.Error.Message != "Run failed" || snapshot.Error.Retryable {
		t.Fatalf("unstable public run error=%+v", snapshot.Error)
	}
}
