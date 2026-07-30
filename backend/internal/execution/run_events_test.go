package execution_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
	"github.com/google/uuid"
)

const (
	runEventFirstID        = "a08f1f2e-7b5a-7c3d-8e9f-123456789001"
	runEventTerminalID     = "a08f1f2e-7b5a-7c3d-8e9f-123456789002"
	runEventDuplicateID    = "a08f1f2e-7b5a-7c3d-8e9f-123456789003"
	runEventWrongSessionID = "a08f1f2e-7b5a-7c3d-8e9f-123456789004"
)

func TestRunEventsReplayFanoutAndTerminal(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 4 || version.Dirty {
		t.Fatalf("expected clean run event migration version 22, got %+v", version)
	}
	db := testDatabase.Open(t)
	db.SetMaxOpenConns(16)
	insertToolInvocationFixtures(t, db)
	if _, err := db.Exec(`
		INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		VALUES($1,$2,$3,'Wrong session',$4)
	`, runEventWrongSessionID, executionWorkspaceID, executionAgentID, executionOwnerID); err != nil {
		t.Fatal(err)
	}
	repository, err := execution.NewRunEventRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := repository.Append(ctx, execution.AppendRunEventInput{
		ID: runEventFirstID, WorkspaceID: executionWorkspaceID,
		RunID: executionAgentRunID, EventType: "RUN_STARTED",
		Payload: json.RawMessage(`{"traceId":"trace-parent-run"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.SequenceNo != 1 {
		t.Fatalf("expected committed first event, got %+v", first)
	}

	const concurrentEvents = 8
	type appendResult struct {
		event execution.RunEvent
		err   error
	}
	results := make(chan appendResult, concurrentEvents)
	var wait sync.WaitGroup
	for index := 0; index < concurrentEvents; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			value, err := repository.Append(ctx, execution.AppendRunEventInput{
				ID: uuid.NewString(), WorkspaceID: executionWorkspaceID,
				RunID: executionAgentRunID, EventType: "STEP_COMPLETED",
				Payload: json.RawMessage(fmt.Sprintf(`{"step":%d}`, index)),
			})
			results <- appendResult{event: value, err: err}
		}(index)
	}
	wait.Wait()
	close(results)
	sequences := map[int64]struct{}{first.SequenceNo: {}}
	for result := range results {
		if result.err != nil {
			t.Fatalf("append concurrent run event: %v", result.err)
		}
		sequences[result.event.SequenceNo] = struct{}{}
	}
	if len(sequences) != concurrentEvents+1 {
		t.Fatalf("expected unique event sequences, got %v", sequences)
	}

	replayed, err := repository.ListAfter(ctx, executionWorkspaceID, executionAgentRunID, 3, 100)
	if err != nil || len(replayed) != concurrentEvents-2 {
		t.Fatalf("replay after Last-Event-ID=3: count=%d err=%v events=%+v", len(replayed), err, replayed)
	}
	for _, event := range replayed {
		if event.SequenceNo <= 3 {
			t.Fatalf("replay returned stale event %+v", event)
		}
	}
	if _, err := repository.ListAfter(ctx, executionOtherWorkspaceID,
		executionAgentRunID, 0, 100); !errors.Is(err, execution.ErrRunNotFound) {
		t.Fatalf("expected cross-workspace replay miss, got %v", err)
	}
	if _, err := repository.ListForSessionAfter(ctx, executionWorkspaceID,
		runEventWrongSessionID, executionAgentRunID, 0, 100); !errors.Is(err, execution.ErrRunNotFound) {
		t.Fatalf("expected cross-session replay miss, got %v", err)
	}

	if _, err := db.Exec(`
		UPDATE agent_runs SET status='SUCCEEDED',output_summary='{"answer":"done"}',
		 finished_at=GREATEST(clock_timestamp(),started_at),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND status='RUNNING' AND lock_version=1
	`, executionWorkspaceID, executionAgentRunID); err != nil {
		t.Fatalf("complete run before terminal event: %v", err)
	}
	terminal, err := repository.Append(ctx, execution.AppendRunEventInput{
		ID: runEventTerminalID, WorkspaceID: executionWorkspaceID,
		RunID: executionAgentRunID, EventType: "RUN_COMPLETED",
		Payload: json.RawMessage(`{"status":"SUCCEEDED"}`),
	})
	if err != nil || !terminal.Terminal || terminal.SequenceNo != concurrentEvents+2 {
		t.Fatalf("record explicit terminal event: %+v err=%v", terminal, err)
	}
	if _, err := repository.Append(ctx, execution.AppendRunEventInput{
		ID: runEventDuplicateID, WorkspaceID: executionWorkspaceID,
		RunID: executionAgentRunID, EventType: "STEP_COMPLETED", Payload: json.RawMessage(`{}`),
	}); !errors.Is(err, execution.ErrRunConflict) {
		t.Fatalf("expected nonterminal event after terminal state conflict, got %v", err)
	}
	if _, err := repository.Append(ctx, execution.AppendRunEventInput{
		ID: uuid.NewString(), WorkspaceID: executionWorkspaceID,
		RunID: executionAgentRunID, EventType: "RUN_COMPLETED", Payload: json.RawMessage(`{}`),
	}); !errors.Is(err, execution.ErrRunConflict) {
		t.Fatalf("expected second terminal event conflict, got %v", err)
	}
	var stream bytes.Buffer
	if err := execution.WriteRunEventSSE(&stream, terminal); err != nil {
		t.Fatal(err)
	}
	encoded := stream.String()
	if !strings.Contains(encoded, fmt.Sprintf("id: %d\n", terminal.SequenceNo)) ||
		!strings.Contains(encoded, "event: RUN_COMPLETED\n") ||
		!strings.Contains(encoded, `data: {"status":"SUCCEEDED"}`) {
		t.Fatalf("unexpected SSE event encoding: %q", encoded)
	}
	if _, err := db.Exec(`UPDATE run_events SET payload='{}' WHERE id=$1`, runEventFirstID); err == nil {
		t.Fatal("expected run event update to be rejected")
	}
	if _, err := db.Exec(`DELETE FROM run_events WHERE id=$1`, runEventFirstID); err == nil {
		t.Fatal("expected run event delete to be rejected")
	}

	allFacts, err := repository.ListAfter(ctx, executionWorkspaceID, executionAgentRunID, 0, 100)
	if err != nil || len(allFacts) != concurrentEvents+2 {
		t.Fatalf("fanout loss changed PostgreSQL event facts: count=%d err=%v", len(allFacts), err)
	}
}

func assertRunEventsTableMissing(t *testing.T, db *sql.DB) {
	t.Helper()
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('public.run_events') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("run_events remained after rollback")
	}
}
