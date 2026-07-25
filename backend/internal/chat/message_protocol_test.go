package chat_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/chat"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

func TestMessageProtocolItems(t *testing.T) {
	ctx := context.Background()
	repository, runRepository, _, db := newChatRunTest(t)
	permanent := &protocolMessagePermanentStore{db: db, content: make(map[string]string)}
	runService, err := execution.NewRunService(runRepository, chatRunSnapshots{}, chatRunAuthorization{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := chat.NewService(
		repository, runRepository, runService, chat.WithPermanentContent(permanent, 32),
	)
	if err != nil {
		t.Fatal(err)
	}
	session, err := repository.CreateSession(ctx, chat.CreateSessionInput{
		ID: chatRunSessionID, WorkspaceID: chatRunWorkspaceID, AgentID: chatRunAgentID,
		Title: "Protocol messages", CreatedBy: chatRunOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	longContent := strings.Repeat("public long message segment. ", 2048)
	sent, err := service.SendMessage(ctx, chat.SendMessageInput{
		MessageID: chatRunMessageID, RunID: chatRunMainRunID,
		WorkspaceID: chatRunWorkspaceID, SessionID: session.ID,
		Content: longContent, CreatedBy: chatRunOwnerID, TraceID: "trace-message-protocol",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sent.Message.Content != "" || sent.Message.ContentObjectID != sent.Message.ID ||
		permanent.content[sent.Message.ID] != longContent {
		t.Fatalf("long message retention changed: %+v", sent.Message)
	}
	run, err := runRepository.GetAgentRun(ctx, chatRunWorkspaceID, chatRunMainRunID)
	if err != nil {
		t.Fatal(err)
	}
	protocolContext := chat.ProtocolMessageContext{
		Scope: protocolevent.RunScope{
			WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
			ConversationID: run.SessionID, RunID: run.ID,
		},
		EventStreamID: run.ID, TraceID: run.TraceID,
	}
	contentReader := &authorizedProtocolMessageReader{
		content: permanent.content, allowedActor: chatRunOwnerID,
	}
	mapper := chat.NewProtocolMessageMapper(contentReader)
	unit, err := protocolevent.NewProtocolUnitOfWork(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	projector, err := chat.NewProtocolMessageProjector(unit, mapper)
	if err != nil {
		t.Fatal(err)
	}

	userResult, err := projector.ProjectCompleted(ctx, chat.ProjectCompletedMessageInput{
		Context: protocolContext, Message: sent.Message, ActorID: chatRunOwnerID, Ordinal: 1,
	})
	if err != nil || userResult.Projection.Status != protocolevent.ItemStatusCompleted ||
		userResult.Projection.SourceType != protocolevent.SourceChatMessage ||
		userResult.Projection.SourceID != sent.Message.ID {
		t.Fatalf("project user message result=%+v err=%v", userResult, err)
	}
	assertProtocolMessageText(t, userResult.Projection.Item, protocolevent.MessageRoleUser, longContent)
	if contentReader.calls != 1 || contentReader.lastActor != chatRunOwnerID ||
		contentReader.lastObject != sent.Message.ContentObjectID {
		t.Fatalf("stored content was not read through authorized boundary: %+v", contentReader)
	}
	serializedUserEvent := string(userResult.Events[0].Data)
	if !strings.Contains(serializedUserEvent, longContent) ||
		strings.Contains(serializedUserEvent, "contentObjectId") ||
		strings.Contains(serializedUserEvent, sent.Message.ContentSHA256) {
		t.Fatalf("public user item leaked carrier metadata or lost content: %.256s", serializedUserEvent)
	}
	retained, err := repository.GetMessage(ctx, chatRunWorkspaceID, sent.Message.ID)
	if err != nil || retained.Content != "" || retained.ContentObjectID != sent.Message.ID ||
		permanent.content[retained.ContentObjectID] != longContent {
		t.Fatalf("projection changed permanent chat fact: %+v err=%v", retained, err)
	}

	deniedReader := &authorizedProtocolMessageReader{
		content: permanent.content, allowedActor: chatRunOwnerID,
	}
	deniedMapper := chat.NewProtocolMessageMapper(deniedReader)
	if _, err := deniedMapper.MapCompleted(ctx, sent.Message, uuid.NewString()); !errors.Is(err, errProtocolMessageReadDenied) {
		t.Fatalf("unauthorized stored message read error=%v", err)
	}

	assistantStartedAt := time.Now().UTC()
	assistantStarted, err := projector.ProjectStarted(ctx, chat.ProjectStartedMessageInput{
		Context: protocolContext, MessageID: chatRunAssistantID, Role: "ASSISTANT",
		Ordinal: 2, StartedAt: assistantStartedAt,
	})
	if err != nil || assistantStarted.Projection.Status != protocolevent.ItemStatusInProgress {
		t.Fatalf("start assistant projection=%+v err=%v", assistantStarted, err)
	}
	firstDelta, err := projector.ProjectDelta(ctx, chat.ProjectMessageDeltaInput{
		Context: protocolContext, MessageID: chatRunAssistantID,
		Index: 0, Text: "Order ", OccurredAt: assistantStartedAt.Add(time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertProtocolMessageText(t, firstDelta.Projection.Item, protocolevent.MessageRoleAssistant, "Order ")
	secondDelta, err := projector.ProjectDelta(ctx, chat.ProjectMessageDeltaInput{
		Context: protocolContext, MessageID: chatRunAssistantID,
		Index: 0, Text: "ready.", OccurredAt: assistantStartedAt.Add(2 * time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertProtocolMessageText(t, secondDelta.Projection.Item, protocolevent.MessageRoleAssistant, "Order ready.")

	assistant, err := service.RecordAssistantResult(ctx, chat.RecordAssistantResultInput{
		AssistantMessageID: chatRunAssistantID, WorkspaceID: chatRunWorkspaceID,
		SessionID: session.ID, UserMessageID: chatRunMessageID, RunID: chatRunMainRunID,
		Content: "Order ready.", ExpectedRunStatus: "RUNNING", ExpectedRunLock: 1,
		RunStatus: "SUCCEEDED", RunOutputSummary: []byte(`{"answer":"ready"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	assistantCompleted, err := projector.CompleteProjected(ctx, chat.CompleteProjectedMessageInput{
		Context: protocolContext, Message: assistant.Message,
		ActorID: chatRunOwnerID, CompletedAt: assistant.Message.CreatedAt,
	})
	if err != nil || assistantCompleted.Projection.Status != protocolevent.ItemStatusCompleted {
		t.Fatalf("complete assistant projection=%+v err=%v", assistantCompleted, err)
	}
	assertProtocolMessageText(t, assistantCompleted.Projection.Item,
		protocolevent.MessageRoleAssistant, "Order ready.")

	toolMessageID := uuid.NewString()
	toolContent := `{"order":"A-1","status":"ready"}`
	toolHash := protocolMessageHash(toolContent)
	if _, err := db.Exec(`
		INSERT INTO chat_messages(
		 id,workspace_id,session_id,role,content,content_sha256,content_length,status,run_id
		) VALUES($1,$2,$3,'TOOL',$4,$5,$6,'EXECUTED',$7)
	`, toolMessageID, chatRunWorkspaceID, chatRunSessionID, toolContent,
		toolHash, len([]byte(toolContent)), chatRunMainRunID); err != nil {
		t.Fatal(err)
	}
	toolMessage, err := repository.GetMessage(ctx, chatRunWorkspaceID, toolMessageID)
	if err != nil {
		t.Fatal(err)
	}
	toolResult, err := projector.ProjectCompleted(ctx, chat.ProjectCompletedMessageInput{
		Context: protocolContext, Message: toolMessage, ActorID: chatRunOwnerID, Ordinal: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertProtocolMessageText(t, toolResult.Projection.Item, protocolevent.MessageRoleTool, toolContent)

	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	events, err := reader.ReadRunAfter(ctx, protocolContext.Scope, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	expectedTypes := []string{
		"item.completed", "item.started", "item.delta", "item.delta", "item.completed", "item.completed",
	}
	if len(events) != len(expectedTypes) {
		t.Fatalf("message event count=%d events=%+v", len(events), events)
	}
	for index, event := range events {
		if event.Sequence != int64(index+1) || event.Type != expectedTypes[index] {
			t.Fatalf("message event[%d]=%s/%d want=%s/%d",
				index, event.Type, event.Sequence, expectedTypes[index], index+1)
		}
	}

	rows, err := db.Query(`
		SELECT id,ordinal FROM run_items
		WHERE workspace_id=$1 AND run_id=$2 ORDER BY ordinal
	`, chatRunWorkspaceID, chatRunMainRunID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	expectedIDs := []string{chatRunMessageID, chatRunAssistantID, toolMessageID}
	index := 0
	for rows.Next() {
		var id string
		var ordinal int
		if err := rows.Scan(&id, &ordinal); err != nil {
			t.Fatal(err)
		}
		if index >= len(expectedIDs) || id != expectedIDs[index] || ordinal != index+1 {
			t.Fatalf("unstable message projection order id=%s ordinal=%d index=%d", id, ordinal, index)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if index != len(expectedIDs) {
		t.Fatalf("message projection rows=%d want=%d", index, len(expectedIDs))
	}
}

var errProtocolMessageReadDenied = errors.New("protocol message content read denied")

type protocolMessagePermanentStore struct {
	db      *sql.DB
	content map[string]string
}

func (store *protocolMessagePermanentStore) PutPermanentChat(
	_ context.Context,
	input chat.PermanentContentInput,
) (string, error) {
	digest := sha256.Sum256(input.Content)
	if _, err := store.db.Exec(`
		INSERT INTO stored_objects(
		 id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
		 encryption_key_id,classification,retention_mode,created_by_type,created_by_id
		) VALUES($1,$2,'protocol-message-test',$3,'CHAT_MESSAGE','text/plain; charset=utf-8',
		 $4,$5,'protocol-message-test-key','SENSITIVE','PERMANENT',$6,$7)
	`, input.ObjectID, input.WorkspaceID, input.WorkspaceID+"/chat/"+input.ObjectID,
		len(input.Content), hex.EncodeToString(digest[:]), input.CreatedByType, input.CreatedByID); err != nil {
		return "", err
	}
	store.content[input.ObjectID] = string(input.Content)
	return input.ObjectID, nil
}

type authorizedProtocolMessageReader struct {
	content      map[string]string
	allowedActor string
	calls        int
	lastActor    string
	lastObject   string
}

func (reader *authorizedProtocolMessageReader) ReadPermanentChat(
	_ context.Context,
	_ string,
	objectID string,
	actorID string,
) (string, error) {
	reader.calls++
	reader.lastActor, reader.lastObject = actorID, objectID
	if actorID != reader.allowedActor {
		return "", errProtocolMessageReadDenied
	}
	content, exists := reader.content[objectID]
	if !exists {
		return "", errProtocolMessageReadDenied
	}
	return content, nil
}

func assertProtocolMessageText(
	t *testing.T,
	item protocolevent.Item,
	expectedRole protocolevent.MessageRole,
	expectedText string,
) {
	t.Helper()
	message, ok := item.(protocolevent.MessageItem)
	if !ok || message.Role != expectedRole || len(message.Content) != 1 {
		t.Fatalf("message item=%T/%+v", item, item)
	}
	part, ok := message.Content[0].(protocolevent.TextContentPart)
	if !ok || part.Text != expectedText || part.Type != protocolevent.ContentPartTypeText {
		t.Fatalf("message text part=%T/%+v want len=%d", message.Content[0], message.Content[0], len(expectedText))
	}
}

func protocolMessageHash(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}
