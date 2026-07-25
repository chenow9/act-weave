package chat_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/chat"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
)

const (
	chatRunOwnerID          = "908f1f2e-7b5a-7c3d-8e9f-123456789001"
	chatRunWorkspaceID      = "908f1f2e-7b5a-7c3d-8e9f-123456789002"
	chatRunOtherWorkspaceID = "908f1f2e-7b5a-7c3d-8e9f-123456789003"
	chatRunModelID          = "908f1f2e-7b5a-7c3d-8e9f-123456789004"
	chatRunOtherModelID     = "908f1f2e-7b5a-7c3d-8e9f-123456789005"
	chatRunAgentID          = "908f1f2e-7b5a-7c3d-8e9f-123456789006"
	chatRunOtherAgentID     = "908f1f2e-7b5a-7c3d-8e9f-123456789007"
	chatRunSessionID        = "908f1f2e-7b5a-7c3d-8e9f-123456789008"
	chatRunFailedSessionID  = "908f1f2e-7b5a-7c3d-8e9f-123456789009"
	chatRunMessageID        = "908f1f2e-7b5a-7c3d-8e9f-12345678900a"
	chatRunAssistantID      = "908f1f2e-7b5a-7c3d-8e9f-12345678900b"
	chatRunMainRunID        = "908f1f2e-7b5a-7c3d-8e9f-12345678900c"
	chatRunRolledBackID     = "908f1f2e-7b5a-7c3d-8e9f-12345678900d"
	chatRunFailedMessageID  = "908f1f2e-7b5a-7c3d-8e9f-12345678900e"
	chatRunFailedAssistID   = "908f1f2e-7b5a-7c3d-8e9f-12345678900f"
	chatRunFailedRunID      = "908f1f2e-7b5a-7c3d-8e9f-123456789010"
	chatRunRollbackAssistID = "908f1f2e-7b5a-7c3d-8e9f-123456789011"
)

func TestSendMessageEnsuresProtocolEventStreamReady(t *testing.T) {
	// PR-U2: after successful SendMessage the protocol stream row must exist so
	// HighWatermark / Console events GET cannot return ErrRunScopeNotFound
	// while the AgentRun already exists.
	repository, runRepository, service, db := newChatRunTest(t)
	ctx := context.Background()
	session, err := repository.CreateSession(ctx, chat.CreateSessionInput{
		ID: chatRunSessionID, WorkspaceID: chatRunWorkspaceID, AgentID: chatRunAgentID,
		Title: "Stream readiness", CreatedBy: chatRunOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SendMessage(ctx, chat.SendMessageInput{
		MessageID: chatRunMessageID, RunID: chatRunMainRunID, WorkspaceID: chatRunWorkspaceID,
		SessionID: session.ID, Content: "ensure stream with run", CreatedBy: chatRunOwnerID,
		TraceID: "trace-chat-stream-ready",
	}); err != nil {
		t.Fatalf("send chat message: %v", err)
	}
	run, err := runRepository.GetAgentRun(ctx, chatRunWorkspaceID, chatRunMainRunID)
	if err != nil || run.Status != "RUNNING" {
		t.Fatalf("expected RUNNING agent run, got %+v err=%v", run, err)
	}
	scope := protocolevent.RunScope{
		WorkspaceID: chatRunWorkspaceID, AgentID: chatRunAgentID,
		ConversationID: session.ID, RunID: chatRunMainRunID,
	}
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	highWatermark, err := reader.HighWatermark(ctx, scope)
	if err != nil {
		t.Fatalf("HighWatermark after SendMessage must succeed, got %v", err)
	}
	if highWatermark != 0 {
		t.Fatalf("empty stream high watermark=%d, want 0", highWatermark)
	}
	var streamID string
	if err := db.QueryRowContext(ctx, `
		SELECT id FROM protocol_event_streams
		WHERE workspace_id=$1 AND agent_id=$2 AND conversation_id=$3 AND run_id=$4
	`, scope.WorkspaceID, scope.AgentID, scope.ConversationID, scope.RunID).Scan(&streamID); err != nil {
		t.Fatalf("protocol stream row missing after SendMessage: %v", err)
	}
	if streamID != chatRunMainRunID {
		t.Fatalf("stream id=%s, want run id %s", streamID, chatRunMainRunID)
	}
	// Idempotent re-ensure (execution path will Ensure again on RecordStarted).
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ensured, err := protocolevent.EnsureRunEventStreamInTx(ctx, tx, chatRunMainRunID, scope)
	if err != nil || ensured != chatRunMainRunID {
		t.Fatalf("second EnsureRunEventStreamInTx id=%s err=%v", ensured, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	highWatermark, err = reader.HighWatermark(ctx, scope)
	if err != nil || highWatermark != 0 {
		t.Fatalf("after second ensure high watermark=%d err=%v", highWatermark, err)
	}
}

func TestChatRunSuccessAndArchive(t *testing.T) {
	repository, runRepository, service, db := newChatRunTest(t)
	ctx := context.Background()
	session, err := repository.CreateSession(ctx, chat.CreateSessionInput{
		ID: chatRunSessionID, WorkspaceID: chatRunWorkspaceID, AgentID: chatRunAgentID,
		Title: "Order support", CreatedBy: chatRunOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateSession(ctx, chat.CreateSessionInput{
		ID: chatRunFailedSessionID, WorkspaceID: chatRunWorkspaceID,
		AgentID: chatRunOtherAgentID, CreatedBy: chatRunOwnerID,
	}); !errors.Is(err, chat.ErrInvalid) {
		t.Fatalf("expected cross-workspace agent/session rejection, got %v", err)
	}

	sent, err := service.SendMessage(ctx, chat.SendMessageInput{
		MessageID: chatRunMessageID, RunID: chatRunMainRunID, WorkspaceID: chatRunWorkspaceID,
		SessionID: session.ID, Content: "  Where is order A-1?  ", CreatedBy: chatRunOwnerID,
		TraceID: "trace-chat-success",
	})
	if err != nil {
		t.Fatalf("send chat message: %v", err)
	}
	if sent.Session.LatestRunID != chatRunMainRunID || sent.Session.LockVersion != 2 ||
		sent.Message.Status != "PROCESSING" || sent.Message.RunID != chatRunMainRunID {
		t.Fatalf("unexpected sent message result: %+v", sent)
	}
	run, err := runRepository.GetAgentRun(ctx, chatRunWorkspaceID, chatRunMainRunID)
	if err != nil || run.Status != "RUNNING" || run.SessionID != session.ID ||
		run.TraceID != "trace-chat-success" {
		t.Fatalf("unexpected chat agent run: %+v err=%v", run, err)
	}
	if string(run.InputSummary) == `{"message":"  Where is order A-1?  "}` ||
		!jsonObjectContains(run.InputSummary, "contentSha256") {
		t.Fatalf("run input must reference the permanent message by hash, got %s", run.InputSummary)
	}

	if _, err := service.SendMessage(ctx, chat.SendMessageInput{
		MessageID: chatRunMessageID, RunID: chatRunRolledBackID,
		WorkspaceID: chatRunWorkspaceID, SessionID: session.ID,
		Content: "duplicate message", CreatedBy: chatRunOwnerID,
		TraceID: "trace-chat-rollback",
	}); err == nil {
		t.Fatal("expected duplicate permanent message insert to fail")
	}
	if _, err := runRepository.GetAgentRun(ctx, chatRunWorkspaceID, chatRunRolledBackID); !errors.Is(err, execution.ErrRunNotFound) {
		t.Fatalf("expected Run insert rollback with failed message, got %v", err)
	}
	stillLatest, err := repository.GetSession(ctx, chatRunWorkspaceID, session.ID)
	if err != nil || stillLatest.LatestRunID != chatRunMainRunID || stillLatest.LockVersion != 2 {
		t.Fatalf("failed send changed latest run: %+v err=%v", stillLatest, err)
	}

	result, err := service.RecordAssistantResult(ctx, chat.RecordAssistantResultInput{
		AssistantMessageID: chatRunAssistantID, WorkspaceID: chatRunWorkspaceID,
		SessionID: session.ID, UserMessageID: chatRunMessageID, RunID: chatRunMainRunID,
		Content: "Order A-1 has shipped.", ExpectedRunStatus: "RUNNING",
		ExpectedRunLock: 1, RunStatus: "SUCCEEDED",
		RunOutputSummary: []byte(`{"answer":"shipped"}`),
	})
	if err != nil {
		t.Fatalf("record assistant success: %v", err)
	}
	if result.Message.Status != "EXECUTED" || result.Message.RunID != chatRunMainRunID {
		t.Fatalf("unexpected assistant message: %+v", result.Message)
	}
	finishedRun, err := runRepository.GetAgentRun(ctx, chatRunWorkspaceID, chatRunMainRunID)
	if err != nil || finishedRun.Status != "SUCCEEDED" || finishedRun.FinishedAt == nil {
		t.Fatalf("chat run was not completed: %+v err=%v", finishedRun, err)
	}
	userMessage, err := repository.GetMessage(ctx, chatRunWorkspaceID, chatRunMessageID)
	if err != nil || userMessage.Status != "EXECUTED" || userMessage.Content != "  Where is order A-1?  " {
		t.Fatalf("user message was not advanced without content loss: %+v err=%v", userMessage, err)
	}
	messages, err := repository.ListMessages(ctx, chatRunWorkspaceID, session.ID)
	if err != nil || len(messages) != 2 || messages[0].ID != chatRunMessageID ||
		messages[1].ID != chatRunAssistantID {
		t.Fatalf("unexpected stable chat message order: %+v err=%v", messages, err)
	}

	archived, err := repository.ArchiveSession(ctx, chatRunWorkspaceID, session.ID, 2)
	if err != nil || archived.Status != "ARCHIVED" || archived.LockVersion != 3 {
		t.Fatalf("archive chat session: %+v err=%v", archived, err)
	}
	if _, err := service.SendMessage(ctx, chat.SendMessageInput{
		MessageID: chatRunFailedMessageID, RunID: chatRunFailedRunID,
		WorkspaceID: chatRunWorkspaceID, SessionID: session.ID, Content: "after archive",
		CreatedBy: chatRunOwnerID, TraceID: "trace-archived",
	}); !errors.Is(err, chat.ErrConflict) {
		t.Fatalf("expected archived session send conflict, got %v", err)
	}
	if _, err := db.Exec(`DELETE FROM chat_sessions WHERE id=$1`, session.ID); err == nil {
		t.Fatal("archiving must not create a physical delete path")
	}
	retained, err := repository.ListMessages(ctx, chatRunWorkspaceID, session.ID)
	if err != nil || len(retained) != 2 {
		t.Fatalf("archived session lost permanent messages: %+v err=%v", retained, err)
	}
}

func TestChatRunFailureTraceability(t *testing.T) {
	repository, runRepository, service, _ := newChatRunTest(t)
	ctx := context.Background()
	session, err := repository.CreateSession(ctx, chat.CreateSessionInput{
		ID: chatRunFailedSessionID, WorkspaceID: chatRunWorkspaceID,
		AgentID: chatRunAgentID, Title: "Failure trace", CreatedBy: chatRunOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SendMessage(ctx, chat.SendMessageInput{
		MessageID: chatRunFailedMessageID, RunID: chatRunFailedRunID,
		WorkspaceID: chatRunWorkspaceID, SessionID: session.ID,
		Content: "Run the unavailable operation", CreatedBy: chatRunOwnerID,
		TraceID: "trace-chat-failure",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := service.RecordAssistantResult(ctx, chat.RecordAssistantResultInput{
		AssistantMessageID: chatRunRollbackAssistID, WorkspaceID: chatRunWorkspaceID,
		SessionID: session.ID, UserMessageID: chatRunMessageID, RunID: chatRunFailedRunID,
		Content: "This should roll back.", ExpectedRunStatus: "RUNNING",
		ExpectedRunLock: 1, RunStatus: "FAILED", RunErrorCode: "UPSTREAM_ERROR",
	}); !errors.Is(err, chat.ErrConflict) {
		t.Fatalf("expected mismatched user message to roll back result, got %v", err)
	}
	stillRunning, err := runRepository.GetAgentRun(ctx, chatRunWorkspaceID, chatRunFailedRunID)
	if err != nil || stillRunning.Status != "RUNNING" || stillRunning.LockVersion != 1 {
		t.Fatalf("assistant transaction failure did not roll back Run: %+v err=%v", stillRunning, err)
	}

	failed, err := service.RecordAssistantResult(ctx, chat.RecordAssistantResultInput{
		AssistantMessageID: chatRunFailedAssistID, WorkspaceID: chatRunWorkspaceID,
		SessionID: session.ID, UserMessageID: chatRunFailedMessageID,
		RunID: chatRunFailedRunID, Content: "The upstream service is unavailable.",
		ExpectedRunStatus: "RUNNING", ExpectedRunLock: 1, RunStatus: "FAILED",
		RunOutputSummary: []byte(`{"retryable":true}`), RunErrorCode: "UPSTREAM_UNAVAILABLE",
	})
	if err != nil || failed.Message.Status != "FAILED" {
		t.Fatalf("record failed assistant result: %+v err=%v", failed, err)
	}
	failedRun, err := runRepository.GetAgentRun(ctx, chatRunWorkspaceID, chatRunFailedRunID)
	if err != nil || failedRun.Status != "FAILED" || failedRun.ErrorCode != "UPSTREAM_UNAVAILABLE" ||
		failedRun.TraceID != "trace-chat-failure" {
		t.Fatalf("failed run is not traceable: %+v err=%v", failedRun, err)
	}
	userMessage, err := repository.GetMessage(ctx, chatRunWorkspaceID, chatRunFailedMessageID)
	if err != nil || userMessage.Status != "FAILED" || userMessage.RunID != failedRun.ID {
		t.Fatalf("failed user message is not linked to Run: %+v err=%v", userMessage, err)
	}
	assistant, err := repository.GetMessage(ctx, chatRunWorkspaceID, chatRunFailedAssistID)
	if err != nil || assistant.Status != "FAILED" || assistant.RunID != failedRun.ID ||
		assistant.Content == "" {
		t.Fatalf("failed assistant message is not permanently traceable: %+v err=%v", assistant, err)
	}
}

func TestChatRunEmitsAuditEventsBeforeCommit(t *testing.T) {
	capture := &chatAuditCapture{}
	repository, _, service, _ := newChatRunTestWithOptions(t, chat.WithAuditSink(capture))
	ctx := context.Background()
	session, err := repository.CreateSession(ctx, chat.CreateSessionInput{
		ID: chatRunSessionID, WorkspaceID: chatRunWorkspaceID, AgentID: chatRunAgentID,
		CreatedBy: chatRunOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SendMessage(ctx, chat.SendMessageInput{
		MessageID: chatRunMessageID, RunID: chatRunMainRunID,
		WorkspaceID: chatRunWorkspaceID, SessionID: session.ID,
		Content: "audit-safe chat content", CreatedBy: chatRunOwnerID, TraceID: "trace-chat-audit",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordAssistantResult(ctx, chat.RecordAssistantResultInput{
		AssistantMessageID: chatRunAssistantID, WorkspaceID: chatRunWorkspaceID,
		SessionID: session.ID, UserMessageID: chatRunMessageID, RunID: chatRunMainRunID,
		Content: "audit-safe assistant content", ExpectedRunStatus: "RUNNING",
		ExpectedRunLock: 1, RunStatus: "SUCCEEDED",
	}); err != nil {
		t.Fatal(err)
	}
	if len(capture.events) != 2 || capture.events[0].Action != "chat.message.sent" ||
		capture.events[0].TraceID != "trace-chat-audit" ||
		capture.events[1].Action != "execution.run.completed" {
		t.Fatalf("chat audit events = %+v", capture.events)
	}
	serialized, _ := json.Marshal(capture.events)
	if strings.Contains(string(serialized), "audit-safe chat content") ||
		strings.Contains(string(serialized), "audit-safe assistant content") {
		t.Fatalf("chat content entered audit event: %s", serialized)
	}
}

type chatAuditCapture struct{ events []chat.AuditEvent }

func (capture *chatAuditCapture) AppendChatAuditEvent(
	_ context.Context,
	_ *sql.Tx,
	event chat.AuditEvent,
) error {
	capture.events = append(capture.events, event)
	return nil
}

type chatRunSnapshots struct{}

func (chatRunSnapshots) SnapshotAgentRun(
	context.Context,
	string,
	string,
) (execution.AgentRunSnapshots, error) {
	return execution.AgentRunSnapshots{
		SchemaVersion: "run.v1",
		Model:         json.RawMessage(`{"modelConfigId":"` + chatRunModelID + `","model":"chat-model"}`),
		Capabilities:  json.RawMessage(`{"releases":[]}`),
		ContextPolicy: json.RawMessage(`{"memory":false}`),
	}, nil
}

type chatRunAuthorization struct{}

func (chatRunAuthorization) AuthorizeRun(
	context.Context,
	string,
	string,
	string,
	string,
	string,
) (json.RawMessage, error) {
	return json.RawMessage(`{"decision":"ALLOW","role":"EDITOR"}`), nil
}

func newChatRunTest(
	t *testing.T,
) (*chat.Repository, *execution.RunRepository, *chat.Service, *sql.DB) {
	return newChatRunTestWithOptions(t)
}

func newChatRunTestWithOptions(
	t *testing.T,
	options ...chat.ServiceOption,
) (*chat.Repository, *execution.RunRepository, *chat.Service, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertChatRunFixtures(t, db)
	repository, err := chat.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	runService, err := execution.NewRunService(runs, chatRunSnapshots{}, chatRunAuthorization{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := chat.NewService(repository, runs, runService, options...)
	if err != nil {
		t.Fatal(err)
	}
	return repository, runs, service, db
}

func insertChatRunFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'chat.run.owner','Chat Run Owner')`, chatRunOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'chat-run-space','Chat Run Space','PRODUCTION',$3,$3,$3),
		($2,'chat-run-other','Chat Run Other','SANDBOX',$3,$3,$3)
	`, chatRunWorkspaceID, chatRunOtherWorkspaceID, chatRunOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(
		 id,workspace_id,name,provider,api_base,model_name,created_by,updated_by
		) VALUES
		($1,$3,'Chat Run Model','openai','https://models.example.test','chat-model',$5,$5),
		($2,$4,'Other Chat Run Model','openai','https://models.example.test','other-chat-model',$5,$5)
	`, chatRunModelID, chatRunOtherModelID, chatRunWorkspaceID,
		chatRunOtherWorkspaceID, chatRunOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES
		($1,$3,'Chat Run Agent',$5,$7,$7),
		($2,$4,'Other Chat Run Agent',$6,$7,$7)
	`, chatRunAgentID, chatRunOtherAgentID, chatRunWorkspaceID,
		chatRunOtherWorkspaceID, chatRunModelID, chatRunOtherModelID,
		chatRunOwnerID); err != nil {
		t.Fatal(err)
	}
}

func jsonObjectContains(value json.RawMessage, key string) bool {
	var object map[string]any
	return json.Unmarshal(value, &object) == nil && object[key] != nil
}
