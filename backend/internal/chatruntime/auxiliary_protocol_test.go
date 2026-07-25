package chatruntime_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

func TestAuxiliaryProtocolItems(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertRuntimeFixtures(t, db)
	insertAuxiliaryProtocolRun(t, db)

	unit, err := protocolevent.NewProtocolUnitOfWork(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	projector, err := chatruntime.NewAuxiliaryProtocolProjector(
		unit, chatruntime.NewAuxiliaryProtocolMapper(),
	)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	items, err := protocolevent.NewRunItemRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	protocolContext := chatruntime.AuxiliaryProtocolContext{
		Scope: protocolevent.RunScope{
			WorkspaceID: runtimeWorkspaceID, AgentID: runtimeAgentID,
			ConversationID: runtimeSessionID, RunID: runtimeRunID,
		},
		EventStreamID: runtimeRunID, TraceID: "trace-auxiliary-protocol",
	}
	startedAt := time.Date(2026, 7, 20, 1, 0, 0, 0, time.UTC)

	for index, usage := range []struct{ input, output int64 }{{10, 5}, {12, 8}} {
		result, projectErr := projector.ProjectUsage(context.Background(), chatruntime.ProjectUsageInput{
			Context: protocolContext, InputTokens: usage.input, OutputTokens: usage.output,
			OccurredAt: startedAt.Add(time.Duration(index) * time.Millisecond),
		})
		if projectErr != nil || len(result.Events) != 1 ||
			result.Events[0].Type != protocolevent.EventUsageUpdated {
			t.Fatalf("project usage[%d] result=%+v err=%v", index, result, projectErr)
		}
	}
	for _, regression := range []struct{ input, output int64 }{{11, 8}, {12, 8}} {
		if _, err := projector.ProjectUsage(context.Background(), chatruntime.ProjectUsageInput{
			Context: protocolContext, InputTokens: regression.input, OutputTokens: regression.output,
			OccurredAt: startedAt.Add(3 * time.Millisecond),
		}); !errors.Is(err, chatruntime.ErrUsageRegression) {
			t.Fatalf("usage regression/duplicate %d/%d error=%v", regression.input, regression.output, err)
		}
	}

	reasoningID := uuid.NewString()
	reasoningText := "The request was checked against the public policy summary and is ready."
	reasoning, err := projector.ProjectReasoningSummary(context.Background(), chatruntime.ProjectReasoningSummaryInput{
		Context: protocolContext, ItemID: reasoningID, Text: reasoningText,
		Ordinal: 1, SourceID: uuid.NewString(), OccurredAt: startedAt.Add(4 * time.Millisecond),
	})
	if err != nil || reasoning.Projection.SourceType != protocolevent.SourceRuntime ||
		reasoning.Projection.Status != protocolevent.ItemStatusCompleted {
		t.Fatalf("reasoning projection=%+v err=%v", reasoning, err)
	}
	noticeID := uuid.NewString()
	notice, err := projector.ProjectNotice(context.Background(), chatruntime.ProjectNoticeInput{
		Context: protocolContext, ItemID: noticeID, Code: "CONTEXT_TRUNCATED",
		Message: "Older public context was omitted.", Ordinal: 2,
		SourceID: uuid.NewString(), OccurredAt: startedAt.Add(5 * time.Millisecond),
	})
	if err != nil || notice.Projection.SourceType != protocolevent.SourceRuntime ||
		notice.Projection.Status != protocolevent.ItemStatusCompleted {
		t.Fatalf("notice projection=%+v err=%v", notice, err)
	}

	highWatermark, err := reader.HighWatermark(context.Background(), protocolContext.Scope)
	if err != nil || highWatermark != 4 {
		t.Fatalf("auxiliary high watermark=%d err=%v", highWatermark, err)
	}
	for _, raw := range []string{
		"<thinking>private deliberation</thinking>",
		"Here is my chain-of-thought with internal details.",
	} {
		if _, err := projector.ProjectReasoningSummary(context.Background(), chatruntime.ProjectReasoningSummaryInput{
			Context: protocolContext, ItemID: uuid.NewString(), Text: raw,
			Ordinal: 3, OccurredAt: startedAt.Add(6 * time.Millisecond),
		}); !errors.Is(err, chatruntime.ErrRawReasoning) {
			t.Fatalf("raw reasoning error=%v", err)
		}
	}
	if _, err := projector.ProjectNotice(context.Background(), chatruntime.ProjectNoticeInput{
		Context: protocolContext, ItemID: uuid.NewString(), Code: "bad-code",
		Message: "Authorization: Bearer abcdefghijklmnop", Ordinal: 3,
		OccurredAt: startedAt.Add(7 * time.Millisecond),
	}); !errors.Is(err, chatruntime.ErrAuxiliaryProtocolInvalid) {
		t.Fatalf("unsafe notice error=%v", err)
	}
	afterRejected, err := reader.HighWatermark(context.Background(), protocolContext.Scope)
	if err != nil || afterRejected != highWatermark {
		t.Fatalf("rejected auxiliary content changed stream %d -> %d err=%v",
			highWatermark, afterRejected, err)
	}

	events, err := reader.ReadRunAfter(context.Background(), protocolContext.Scope, 0, 100)
	if err != nil || len(events) != 4 {
		t.Fatalf("read auxiliary events=%+v err=%v", events, err)
	}
	for index, event := range events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("auxiliary sequence[%d]=%d", index, event.Sequence)
		}
		payload := strings.ToLower(string(event.Payload))
		for _, forbidden := range []string{"private deliberation", "chain-of-thought", "bearer abcdef"} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("auxiliary payload leaked %q: %s", forbidden, event.Payload)
			}
		}
	}
	firstUsage, err := events[0].DecodeData()
	if err != nil || firstUsage.(protocolevent.UsageData).Usage != (protocolevent.Usage{
		InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
	}) {
		t.Fatalf("first usage=%+v err=%v", firstUsage, err)
	}
	lastUsage, err := events[1].DecodeData()
	if err != nil || lastUsage.(protocolevent.UsageData).Usage != (protocolevent.Usage{
		InputTokens: 12, OutputTokens: 8, TotalTokens: 20,
	}) {
		t.Fatalf("last usage=%+v err=%v", lastUsage, err)
	}
	assertAuxiliaryItem(t, items, reasoningID, protocolevent.ItemTypeReasoningSummary, reasoningText)
	assertAuxiliaryItem(t, items, noticeID, protocolevent.ItemTypeNotice, "CONTEXT_TRUNCATED")
}

func insertAuxiliaryProtocolRun(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		 VALUES($1,$2,$3,'Auxiliary protocol',$4)`,
			[]any{runtimeSessionID, runtimeWorkspaceID, runtimeAgentID, runtimeOwnerID}},
		{`INSERT INTO agent_runs(
		 id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
		 triggered_by_id,trace_id,model_snapshot,capability_snapshot
		 ) VALUES($1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'trace-auxiliary-protocol','{}','{}')`,
			[]any{runtimeRunID, runtimeWorkspaceID, runtimeSessionID, runtimeAgentID, runtimeOwnerID}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("insert auxiliary protocol fixture: %v", err)
		}
	}
}

func assertAuxiliaryItem(
	t *testing.T,
	repository *protocolevent.RunItemRepository,
	itemID string,
	wantType protocolevent.ItemType,
	wantContent string,
) {
	t.Helper()
	projection, err := repository.Get(
		context.Background(), runtimeWorkspaceID, runtimeAgentID, runtimeRunID, itemID,
	)
	if err != nil || projection.ItemType != string(wantType) ||
		projection.SourceType != protocolevent.SourceRuntime ||
		projection.Status != protocolevent.ItemStatusCompleted {
		t.Fatalf("auxiliary item=%+v err=%v", projection, err)
	}
	switch item := projection.Item.(type) {
	case protocolevent.ReasoningSummaryItem:
		if item.Text != wantContent {
			t.Fatalf("reasoning item=%+v", item)
		}
	case protocolevent.NoticeItem:
		if item.Code != wantContent {
			t.Fatalf("notice item=%+v", item)
		}
	default:
		t.Fatalf("unexpected auxiliary item type %T", item)
	}
}
