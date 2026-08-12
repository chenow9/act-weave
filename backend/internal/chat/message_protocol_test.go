package chat_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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

func TestParseMessageContentPartsV1AndLegacy(t *testing.T) {
	fileID := "d41f1f2e-7b5a-7c3d-8e9f-1234567890f1"
	v1 := `{"schemaVersion":"aap.message-content.v1","parts":[{"type":"text","text":"hi"},{"type":"input_file","fileId":"` + fileID + `","mediaType":"image/png"}]}`
	parts, err := chat.ParseMessageContentParts(v1)
	if err != nil || len(parts) != 2 {
		t.Fatalf("parts=%+v err=%v", parts, err)
	}
	filePart, ok := parts[1].(protocolevent.InputFileContentPart)
	if !ok || filePart.FileID != fileID || filePart.MediaType != "image/png" {
		t.Fatalf("file part=%+v", parts[1])
	}
	legacy, err := chat.ParseMessageContentParts("legacy plain text body")
	if err != nil || len(legacy) != 1 {
		t.Fatalf("legacy=%+v err=%v", legacy, err)
	}
	text, ok := legacy[0].(protocolevent.TextContentPart)
	if !ok || text.Text != "legacy plain text body" {
		t.Fatalf("legacy text=%+v", legacy[0])
	}
}

func TestParseMessageContentPartsA2UI(t *testing.T) {
	// Assistant durable multi-part: text + a2ui must rehydrate for MapCompleted (PR-5).
	v1 := `{"schemaVersion":"aap.message-content.v1","parts":[` +
		`{"type":"text","text":"Fill the form."},` +
		`{"type":"a2ui","version":"a2ui-surface.v0","catalogId":"standard","surface":{"root":"form","password":{"label":"Password"}}}` +
		`]}`
	parts, err := chat.ParseMessageContentParts(v1)
	if err != nil || len(parts) != 2 {
		t.Fatalf("parts=%+v err=%v", parts, err)
	}
	text, ok := parts[0].(protocolevent.TextContentPart)
	if !ok || text.Text != "Fill the form." {
		t.Fatalf("text part=%T/%+v", parts[0], parts[0])
	}
	a2uiPart, ok := parts[1].(protocolevent.A2UIContentPart)
	if !ok || a2uiPart.ContentKind() != protocolevent.ContentPartTypeA2UI {
		t.Fatalf("a2ui part=%T/%+v", parts[1], parts[1])
	}
	if a2uiPart.Version != "a2ui-surface.v0" || a2uiPart.CatalogID != "standard" {
		t.Fatalf("a2ui meta=%+v", a2uiPart)
	}
	if !strings.Contains(string(a2uiPart.Surface), `"password"`) {
		t.Fatalf("surface lost keys: %s", a2uiPart.Surface)
	}

	// Empty text + valid a2ui is allowed on durable rehydrate (KD-16).
	emptyText := `{"schemaVersion":"aap.message-content.v1","parts":[` +
		`{"type":"text","text":""},` +
		`{"type":"a2ui","surface":{"root":"card"}}` +
		`]}`
	parts, err = chat.ParseMessageContentParts(emptyText)
	if err != nil || len(parts) != 2 {
		t.Fatalf("empty-text parts=%+v err=%v", parts, err)
	}
	if text, ok = parts[0].(protocolevent.TextContentPart); !ok || text.Text != "" {
		t.Fatalf("empty text part=%+v", parts[0])
	}
	if _, ok = parts[1].(protocolevent.A2UIContentPart); !ok {
		t.Fatalf("expected a2ui part, got %T", parts[1])
	}

	// Unknown content part types still fail closed.
	unknown := `{"schemaVersion":"aap.message-content.v1","parts":[{"type":"text","text":"x"},{"type":"future_part","x":1}]}`
	if _, err := chat.ParseMessageContentParts(unknown); !errors.Is(err, chat.ErrInvalid) {
		t.Fatalf("unknown part error=%v want ErrInvalid", err)
	}
}

func TestJoinTextPartsFromDurable(t *testing.T) {
	// Plain / legacy unchanged.
	if got := chat.JoinTextPartsFromDurable("hello plain"); got != "hello plain" {
		t.Fatalf("plain=%q", got)
	}
	// Non-v1 JSON is treated as opaque plain body.
	if got := chat.JoinTextPartsFromDurable(`{"schemaVersion":"other","parts":[]}`); !strings.Contains(got, "other") {
		t.Fatalf("non-v1=%q", got)
	}

	// v1 text + a2ui → natural language only (session reload text-first).
	v1 := `{"schemaVersion":"aap.message-content.v1","parts":[` +
		`{"type":"text","text":"Hello "},` +
		`{"type":"text","text":"world"},` +
		`{"type":"a2ui","surface":{"root":"x","schemaVersion":"must-not-leak"}},` +
		`{"type":"input_file","fileId":"d41f1f2e-7b5a-7c3d-8e9f-1234567890f1","mediaType":"image/png"}` +
		`]}`
	got := chat.JoinTextPartsFromDurable(v1)
	if got != "Hello world" {
		t.Fatalf("joined=%q want %q", got, "Hello world")
	}
	if strings.Contains(got, "schemaVersion") || strings.Contains(got, "surface") ||
		strings.Contains(got, "a2ui") || strings.Contains(got, "input_file") {
		t.Fatalf("envelope leaked into projected text: %q", got)
	}

	// Empty text + a2ui → empty string (model history skip).
	empty := `{"schemaVersion":"aap.message-content.v1","parts":[` +
		`{"type":"text","text":""},{"type":"a2ui","surface":{"root":"only"}}` +
		`]}`
	if got := chat.JoinTextPartsFromDurable(empty); got != "" {
		t.Fatalf("empty text join=%q", got)
	}
}

// A2UISurfacesFromDurable is the read counterpart of JoinTextPartsFromDurable:
// between them, text and surfaces reach a display client through separate fields
// and neither can carry the other.
func TestA2UISurfacesFromDurable(t *testing.T) {
	const want = "a2ui-surface.v1"

	surfaces := chat.A2UISurfacesFromDurable(
		`{"schemaVersion":"aap.message-content.v1","parts":[`+
			`{"type":"text","text":"Two charts."},`+
			`{"type":"a2ui","version":"`+want+`","surface":{"surfaceId":"one","components":[]}},`+
			`{"type":"a2ui","version":"`+want+`","surface":{"surfaceId":"two","components":[]}}`+
			`]}`, want)
	if len(surfaces) != 2 {
		t.Fatalf("surfaces=%d want 2, in part order", len(surfaces))
	}
	for index, expected := range []string{"one", "two"} {
		var decoded struct {
			SurfaceID string `json:"surfaceId"`
		}
		if err := json.Unmarshal(surfaces[index], &decoded); err != nil {
			t.Fatalf("surface %d: %v", index, err)
		}
		if decoded.SurfaceID != expected {
			t.Fatalf("surface %d id=%q want %q", index, decoded.SurfaceID, expected)
		}
		if bytes.Contains(surfaces[index], []byte(`"type"`)) ||
			bytes.Contains(surfaces[index], []byte("schemaVersion")) {
			t.Fatalf("surface %d carries the envelope: %s", index, surfaces[index])
		}
	}

	// Everything that is not a current-version surface stays invisible.
	for name, content := range map[string]string{
		"plain body":    "hello",
		"non-v1 body":   `{"schemaVersion":"other","parts":[{"type":"a2ui","version":"` + want + `","surface":{}}]}`,
		"no parts":      `{"schemaVersion":"aap.message-content.v1","parts":[]}`,
		"text only":     `{"schemaVersion":"aap.message-content.v1","parts":[{"type":"text","text":"x"}]}`,
		"older version": `{"schemaVersion":"aap.message-content.v1","parts":[{"type":"a2ui","version":"a2ui-surface.v0","surface":{"root":"x"}}]}`,
		"no version":    `{"schemaVersion":"aap.message-content.v1","parts":[{"type":"a2ui","surface":{"root":"x"}}]}`,
		"empty surface": `{"schemaVersion":"aap.message-content.v1","parts":[{"type":"a2ui","version":"` + want + `"}]}`,
		"input file":    `{"schemaVersion":"aap.message-content.v1","parts":[{"type":"input_file","fileId":"f","mediaType":"image/png"}]}`,
	} {
		if got := chat.A2UISurfacesFromDurable(content, want); len(got) != 0 {
			t.Fatalf("%s: surfaces=%v want none", name, got)
		}
	}

	// A caller that asks for no version gets nothing, rather than everything.
	both := `{"schemaVersion":"aap.message-content.v1","parts":[` +
		`{"type":"a2ui","version":"` + want + `","surface":{"components":[]}}]}`
	if got := chat.A2UISurfacesFromDurable(both, ""); len(got) != 0 {
		t.Fatalf("empty wantVersion returned %v", got)
	}
}

func TestMapCompletedAssistantA2UIMultiPart(t *testing.T) {
	// Protocol projection holds multi-part text+a2ui for reads (PR-5).
	workspaceID := uuid.NewString()
	sessionID := uuid.NewString()
	runID := uuid.NewString()
	durable := `{"schemaVersion":"aap.message-content.v1","parts":[` +
		`{"type":"text","text":"Please confirm."},` +
		`{"type":"a2ui","version":"a2ui-surface.v0","surface":{"root":"confirm","title":"OK?"}}` +
		`]}`
	message := chat.Message{
		ID: uuid.NewString(), WorkspaceID: workspaceID, SessionID: sessionID, RunID: runID,
		Role: "ASSISTANT", Content: durable, ContentSHA256: protocolMessageHash(durable),
		ContentLength: int64(len([]byte(durable))), Status: "EXECUTED",
		CreatedAt: time.Now().UTC(),
	}

	mapper := chat.NewProtocolMessageMapper(nil)
	item, err := mapper.MapCompleted(context.Background(), message, "")
	if err != nil {
		t.Fatalf("MapCompleted: %v", err)
	}
	if item.Role != protocolevent.MessageRoleAssistant || item.Status != protocolevent.ItemStatusCompleted {
		t.Fatalf("item role/status=%+v", item)
	}
	if len(item.Content) != 2 {
		t.Fatalf("content parts=%d want 2: %+v", len(item.Content), item.Content)
	}
	text, ok := item.Content[0].(protocolevent.TextContentPart)
	if !ok || text.Text != "Please confirm." {
		t.Fatalf("text=%T/%+v", item.Content[0], item.Content[0])
	}
	a2uiPart, ok := item.Content[1].(protocolevent.A2UIContentPart)
	if !ok || !strings.Contains(string(a2uiPart.Surface), `"confirm"`) {
		t.Fatalf("a2ui=%T/%+v", item.Content[1], item.Content[1])
	}

	// Plain text assistant still maps as single text part (no regression).
	plain := "Order ready."
	plainMsg := chat.Message{
		ID: uuid.NewString(), WorkspaceID: workspaceID, SessionID: sessionID, RunID: runID,
		Role: "ASSISTANT", Content: plain, ContentSHA256: protocolMessageHash(plain),
		ContentLength: int64(len([]byte(plain))), Status: "EXECUTED",
		CreatedAt: time.Now().UTC(),
	}
	plainItem, err := mapper.MapCompleted(context.Background(), plainMsg, "")
	if err != nil {
		t.Fatalf("plain MapCompleted: %v", err)
	}
	assertProtocolMessageText(t, plainItem, protocolevent.MessageRoleAssistant, plain)
}

func protocolMessageHash(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}
