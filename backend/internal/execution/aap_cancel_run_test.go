package execution_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
)

func TestAAPCancelRunLifecycleRace(t *testing.T) {
	ctx := context.Background()
	repository, runService, db, _ := newRunStateTest(t)
	db.SetMaxOpenConns(8)
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
	started := startProtocolLifecycleRun(t, ctx, runService, lifecycle, "aap-cancel-race")

	type outcome struct {
		status string
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for _, next := range []string{"SUCCEEDED", "CANCELLED"} {
		next := next
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, transitionErr := lifecycle.TransitionAgentRun(
				ctx,
				execution.ProtocolRunTransitionInput{
					WorkspaceID: executionWorkspaceID, RunID: started.Run.ID,
					Transition: execution.RunTransition{
						ExpectedStatus: "RUNNING", ExpectedLockVersion: started.Run.LockVersion,
						NewStatus: next, OutputSummary: json.RawMessage(`{"winner":"` + next + `"}`),
					},
				},
			)
			results <- outcome{status: result.Run.Status, err: transitionErr}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	successes, conflicts := 0, 0
	winningStatus := ""
	for result := range results {
		if result.err == nil {
			successes++
			winningStatus = result.status
		} else if errors.Is(result.err, execution.ErrRunConflict) {
			conflicts++
		} else {
			t.Fatalf("terminal race error=%v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 ||
		(winningStatus != "SUCCEEDED" && winningStatus != "CANCELLED") {
		t.Fatalf("successes=%d conflicts=%d winner=%s", successes, conflicts, winningStatus)
	}
	events := readLifecycleEvents(t, reader, started.Run)
	terminal := 0
	for _, event := range events {
		if event.Type == protocolevent.EventRunCompleted ||
			event.Type == protocolevent.EventRunCancelled {
			terminal++
		}
	}
	if len(events) != 3 || terminal != 1 {
		t.Fatalf("race persisted events=%+v", events)
	}
	stored, err := repository.GetAgentRun(ctx, executionWorkspaceID, started.Run.ID)
	if err != nil || stored.Status != winningStatus || stored.LockVersion != 2 {
		t.Fatalf("stored=%+v winner=%s err=%v", stored, winningStatus, err)
	}
}
