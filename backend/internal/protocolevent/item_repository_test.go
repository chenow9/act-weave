package protocolevent_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

func TestRunItemProjection(t *testing.T) {
	ctx := context.Background()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	db.SetMaxOpenConns(24)
	insertProtocolEventFixtures(t, db)
	repository, err := protocolevent.NewRunItemRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	baseTime := time.Date(2026, 7, 20, 14, 0, 0, 0, time.UTC)
	messageID, sourceID := uuid.NewString(), uuid.NewString()
	startedMessage := protocolevent.MessageItem{
		ID: messageID, Type: protocolevent.ItemTypeMessage,
		Status: protocolevent.ItemStatusInProgress, Role: protocolevent.MessageRoleAssistant,
		Content: []protocolevent.ContentPart{
			protocolevent.TextContentPart{Type: protocolevent.ContentPartTypeText, Text: ""},
		},
	}
	created, err := repository.Create(ctx, protocolevent.CreateRunItemInput{
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID, RunID: protocolRunID,
		Ordinal: 1, SourceType: protocolevent.SourceModelResponse, SourceID: sourceID,
		Item: startedMessage, StartedAt: baseTime,
	})
	if err != nil || created.Status != protocolevent.ItemStatusInProgress ||
		created.SourceType != protocolevent.SourceModelResponse || created.SourceID != sourceID {
		t.Fatalf("create projection=%+v err=%v", created, err)
	}

	projected, err := repository.ApplyDelta(ctx, protocolevent.ApplyItemDeltaInput{
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID,
		RunID: protocolRunID, ItemID: messageID,
		Delta: protocolevent.TextDelta{Type: protocolevent.DeltaTypeText, Index: 0, Text: "hello"},
	})
	if err != nil || projected.Status != protocolevent.ItemStatusInProgress {
		t.Fatalf("apply text delta projection=%+v err=%v", projected, err)
	}
	assertMessageProjectionText(t, projected, "hello")

	finalMessage := startedMessage
	finalMessage.Status = protocolevent.ItemStatusCompleted
	finalMessage.Content = []protocolevent.ContentPart{
		protocolevent.TextContentPart{Type: protocolevent.ContentPartTypeText, Text: "hello"},
	}
	wrongType := protocolevent.ToolCallItem{
		ID: messageID, Type: protocolevent.ItemTypeToolCall,
		Status: protocolevent.ItemStatusCompleted, Name: "wrong.type",
	}
	if _, err := repository.Complete(ctx, protocolevent.CompleteRunItemInput{
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID, RunID: protocolRunID,
		Item: wrongType, CompletedAt: baseTime.Add(time.Second),
	}); !errors.Is(err, protocolevent.ErrRunItemConflict) {
		t.Fatalf("identity type change error=%v", err)
	}
	if _, err := repository.Complete(ctx, protocolevent.CompleteRunItemInput{
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID, RunID: protocolRunID,
		Item: finalMessage, CompletedAt: baseTime.Add(-time.Second),
	}); !errors.Is(err, protocolevent.ErrRunItemInvalid) {
		t.Fatalf("completion before start error=%v", err)
	}
	completed, err := repository.Complete(ctx, protocolevent.CompleteRunItemInput{
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID, RunID: protocolRunID,
		Item: finalMessage, CompletedAt: baseTime.Add(time.Second),
	})
	if err != nil || completed.Status != protocolevent.ItemStatusCompleted || completed.CompletedAt == nil {
		t.Fatalf("complete projection=%+v err=%v", completed, err)
	}
	if completed.SourceType != protocolevent.SourceModelResponse || completed.SourceID != sourceID {
		t.Fatalf("completion changed source identity: %+v", completed)
	}
	assertMessageProjectionText(t, completed, "hello")

	loaded, err := repository.Get(ctx, protocolWorkspaceID, protocolAgentID, protocolRunID, messageID)
	if err != nil || loaded.Status != protocolevent.ItemStatusCompleted || len(loaded.Snapshot) == 0 {
		t.Fatalf("read final snapshot=%+v err=%v", loaded, err)
	}
	assertMessageProjectionText(t, loaded, "hello")
	if _, err := repository.ApplyDelta(ctx, protocolevent.ApplyItemDeltaInput{
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID,
		RunID: protocolRunID, ItemID: messageID,
		Delta: protocolevent.TextDelta{Type: protocolevent.DeltaTypeText, Index: 0, Text: "!"},
	}); !errors.Is(err, protocolevent.ErrRunItemConflict) {
		t.Fatalf("delta after completion error=%v", err)
	}
	if _, err := repository.Complete(ctx, protocolevent.CompleteRunItemInput{
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID, RunID: protocolRunID,
		Item: finalMessage, CompletedAt: baseTime.Add(2 * time.Second),
	}); !errors.Is(err, protocolevent.ErrRunItemConflict) {
		t.Fatalf("second completion error=%v", err)
	}

	terminalAtCreate := protocolevent.NoticeItem{
		ID: uuid.NewString(), Type: protocolevent.ItemTypeNotice,
		Status: protocolevent.ItemStatusCompleted, Code: "BAD", Message: "terminal",
	}
	if _, err := repository.Create(ctx, protocolevent.CreateRunItemInput{
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID, RunID: protocolRunID,
		Ordinal: 2, SourceType: protocolevent.SourceRuntime,
		Item: terminalAtCreate, StartedAt: baseTime,
	}); !errors.Is(err, protocolevent.ErrRunItemInvalid) {
		t.Fatalf("terminal create error=%v", err)
	}

	assertConcurrentRunItemCompletion(t, db, repository, baseTime)
	assertRunItemCallerRollback(t, db, repository, baseTime)
}

func assertConcurrentRunItemCompletion(
	t *testing.T,
	db *sql.DB,
	repository *protocolevent.RunItemRepository,
	baseTime time.Time,
) {
	t.Helper()
	ctx := context.Background()
	itemID, sourceID := uuid.NewString(), uuid.NewString()
	started := protocolevent.ToolCallItem{
		ID: itemID, Type: protocolevent.ItemTypeToolCall,
		Status: protocolevent.ItemStatusInProgress, Name: "weather.lookup",
		Arguments: json.RawMessage(`{"city":"Singapore"}`),
	}
	if _, err := repository.Create(ctx, protocolevent.CreateRunItemInput{
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID, RunID: protocolRunID,
		Ordinal: 2, SourceType: protocolevent.SourceToolInvocation, SourceID: sourceID,
		Item: started, StartedAt: baseTime,
	}); err != nil {
		t.Fatal(err)
	}
	final := started
	final.Status = protocolevent.ItemStatusCompleted
	final.Output = json.RawMessage(`{"temperatureC":29}`)

	const workers = 16
	start := make(chan struct{})
	results := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := repository.Complete(ctx, protocolevent.CompleteRunItemInput{
				WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID, RunID: protocolRunID,
				Item: final, CompletedAt: baseTime.Add(2 * time.Second),
			})
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, protocolevent.ErrRunItemConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent completion error=%v", err)
		}
	}
	if successes != 1 || conflicts != workers-1 {
		t.Fatalf("concurrent completion successes=%d conflicts=%d", successes, conflicts)
	}
	loaded, err := repository.Get(ctx, protocolWorkspaceID, protocolAgentID, protocolRunID, itemID)
	if err != nil || loaded.Status != protocolevent.ItemStatusCompleted ||
		loaded.SourceType != protocolevent.SourceToolInvocation || loaded.SourceID != sourceID {
		t.Fatalf("concurrent final projection=%+v err=%v", loaded, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM run_items WHERE id=$1`, itemID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent completion row count=%d err=%v", count, err)
	}
}

func assertRunItemCallerRollback(
	t *testing.T,
	db *sql.DB,
	repository *protocolevent.RunItemRepository,
	baseTime time.Time,
) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	item := protocolevent.NoticeItem{
		ID: uuid.NewString(), Type: protocolevent.ItemTypeNotice,
		Status: protocolevent.ItemStatusInProgress, Code: "ROLLBACK", Message: "rollback",
	}
	if _, err := repository.CreateInTx(ctx, tx, protocolevent.CreateRunItemInput{
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID, RunID: protocolRunID,
		Ordinal: 3, SourceType: protocolevent.SourceRuntime, Item: item, StartedAt: baseTime,
	}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Get(ctx, protocolWorkspaceID, protocolAgentID, protocolRunID, item.ID); !errors.Is(err, protocolevent.ErrRunItemNotFound) {
		t.Fatalf("caller rollback left item error=%v", err)
	}
}

func assertMessageProjectionText(t *testing.T, projection protocolevent.RunItemProjection, expected string) {
	t.Helper()
	message, ok := projection.Item.(protocolevent.MessageItem)
	if !ok || len(message.Content) != 1 {
		t.Fatalf("projection is not a message: %T %+v", projection.Item, projection)
	}
	part, ok := message.Content[0].(protocolevent.TextContentPart)
	if !ok || part.Text != expected {
		t.Fatalf("message text=%q, want %q", part.Text, expected)
	}
}
