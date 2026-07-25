package execution_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"actweave/backend/internal/chat"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
	"github.com/google/uuid"
)

const acceptanceSessionID = "b08f1f2e-7b5a-7c3d-8e9f-123456789001"

func TestChatRunInvocationAcceptance(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertToolInvocationFixtures(t, db)
	ctx := context.Background()
	runs, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := &mutableRunSnapshotSource{value: execution.AgentRunSnapshots{
		SchemaVersion: "run.v1",
		Model:         json.RawMessage(`{"modelConfigId":"` + executionModelID + `","model":"acceptance-model"}`),
		Capabilities:  json.RawMessage(`{"releases":["` + invocationReleaseID + `"]}`),
		ContextPolicy: json.RawMessage(`{"memory":false,"maxTurns":20}`),
	}}
	authorization := &mutableRunAuthorization{value: json.RawMessage(`{"decision":"ALLOW","role":"EDITOR"}`)}
	runService, err := execution.NewRunService(runs, snapshots, authorization)
	if err != nil {
		t.Fatal(err)
	}
	chatRepository, err := chat.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	chatService, err := chat.NewService(chatRepository, runs, runService)
	if err != nil {
		t.Fatal(err)
	}
	invocations, err := execution.NewToolInvocationRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	eventRepository, err := execution.NewRunEventRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	events := eventRepository
	session, err := chatRepository.CreateSession(ctx, chat.CreateSessionInput{
		ID: acceptanceSessionID, WorkspaceID: executionWorkspaceID,
		AgentID: executionAgentID, Title: "M9 acceptance",
		CreatedBy: executionOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := acceptanceServices{
		chat: chatService, chatRepository: chatRepository, runs: runs,
		runService: runService, invocations: invocations, events: events,
		db: db, workspaceID: executionWorkspaceID, sessionID: session.ID,
	}

	first := fixture.runDirectToolTurn(t, "Where is order A-1?", false)
	second := fixture.runWorkflowToolTurn(t, "Run the published order workflow")
	failed := fixture.runDirectToolTurn(t, "Call the temporarily unavailable endpoint", true)
	recovered := fixture.runDirectToolTurn(t, "Retry as a new user turn", false)

	if first.status != "SUCCEEDED" || second.status != "SUCCEEDED" ||
		failed.status != "FAILED" || recovered.status != "SUCCEEDED" {
		t.Fatalf("unexpected multi-turn results: first=%+v second=%+v failed=%+v recovered=%+v",
			first, second, failed, recovered)
	}
	if failed.traceID == recovered.traceID || failed.runID == recovered.runID {
		t.Fatal("failure recovery must start a new traceable Run")
	}
	replayed, err := events.ListForSessionAfter(ctx, executionWorkspaceID, session.ID,
		second.runID, 1, 100)
	if err != nil || len(replayed) != 2 || replayed[0].SequenceNo != 2 ||
		!replayed[len(replayed)-1].Terminal {
		t.Fatalf("workflow turn event resume failed: %+v err=%v", replayed, err)
	}
	if _, err := events.ListForSessionAfter(ctx, executionWorkspaceID,
		executionSessionID, second.runID, 0, 100); !errors.Is(err, execution.ErrRunNotFound) {
		t.Fatalf("expected event session isolation, got %v", err)
	}

	messages, err := chatRepository.ListMessages(ctx, executionWorkspaceID, session.ID)
	if err != nil || len(messages) != 8 {
		t.Fatalf("expected four permanent user/assistant turns, count=%d err=%v", len(messages), err)
	}
	for _, message := range messages {
		if message.Content == "" || message.ContentSHA256 == "" || message.RunID == "" {
			t.Fatalf("permanent message missing content/hash/run link: %+v", message)
		}
	}
	currentSession, err := chatRepository.GetSession(ctx, executionWorkspaceID, session.ID)
	if err != nil || currentSession.LatestRunID != recovered.runID || currentSession.LockVersion != 5 {
		t.Fatalf("latest run did not follow multi-turn conversation: %+v err=%v", currentSession, err)
	}
	archived, err := chatRepository.ArchiveSession(ctx, executionWorkspaceID,
		session.ID, currentSession.LockVersion)
	if err != nil || archived.Status != "ARCHIVED" {
		t.Fatalf("archive acceptance session: %+v err=%v", archived, err)
	}
	if _, err := db.Exec(`DELETE FROM chat_messages WHERE session_id=$1`, session.ID); err == nil {
		t.Fatal("permanent acceptance messages exposed a physical delete path")
	}

	var sessionRunCount, workflowExecutionCount, invocationCount int
	if err := db.QueryRow(`SELECT count(*) FROM agent_runs WHERE session_id=$1`, session.ID).Scan(&sessionRunCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM workflow_executions WHERE agent_run_id=$1`, second.runID).Scan(&workflowExecutionCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM tool_invocations WHERE agent_run_id IN ($1,$2,$3,$4)`,
		first.runID, second.runID, failed.runID, recovered.runID).Scan(&invocationCount); err != nil {
		t.Fatal(err)
	}
	if sessionRunCount != 4 || workflowExecutionCount != 1 || invocationCount != 4 {
		t.Fatalf("unexpected normalized runtime facts: runs=%d workflows=%d invocations=%d",
			sessionRunCount, workflowExecutionCount, invocationCount)
	}
	var auditEventCount int
	if err := db.QueryRow(`SELECT count(*) FROM audit_events`).Scan(&auditEventCount); err != nil {
		t.Fatal(err)
	}
	if auditEventCount != 0 {
		t.Fatal("M9 runtime acceptance unexpectedly mixed management AuditEvent storage")
	}
}

type acceptanceServices struct {
	chat           *chat.Service
	chatRepository *chat.Repository
	runs           *execution.RunRepository
	runService     *execution.RunService
	invocations    *execution.ToolInvocationRepository
	events         *execution.RunEventRepository
	db             *sql.DB
	workspaceID    string
	sessionID      string
}

type acceptanceTurn struct {
	runID   string
	traceID string
	status  string
}

func (services acceptanceServices) runDirectToolTurn(
	t *testing.T,
	content string,
	shouldFail bool,
) acceptanceTurn {
	t.Helper()
	ctx := context.Background()
	messageID, runID, traceID := uuid.NewString(), uuid.NewString(), "trace-"+uuid.NewString()
	sent, err := services.chat.SendMessage(ctx, chat.SendMessageInput{
		MessageID: messageID, RunID: runID, WorkspaceID: services.workspaceID,
		SessionID: services.sessionID, Content: content,
		CreatedBy: executionOwnerID, TraceID: traceID,
	})
	if err != nil {
		t.Fatalf("send direct tool turn: %v", err)
	}
	services.recordEvent(t, runID, "RUN_STARTED", json.RawMessage(`{"source":"chat"}`))
	step, err := services.runs.AppendAgentRunStep(ctx, execution.AppendAgentRunStepInput{
		ID: uuid.NewString(), WorkspaceID: services.workspaceID, RunID: runID,
		StepType: "TOOL", CapabilityReleaseID: invocationReleaseID,
		InputSummary: json.RawMessage(`{"order_id":"A-1"}`),
	})
	if err != nil {
		t.Fatalf("append direct tool run step: %v", err)
	}
	invocationID := uuid.NewString()
	started, err := services.invocations.Start(ctx, acceptanceInvocationInput(
		invocationID, runID, "", "", "direct-"+runID,
	))
	if err != nil || !started.Created {
		t.Fatalf("start direct tool invocation: %+v err=%v", started, err)
	}
	status, assistantContent, errorCode := "SUCCEEDED", "Order operation completed.", ""
	services.insertPermanentInvocationPayload(t, invocationID)
	if shouldFail {
		status, assistantContent, errorCode = "FAILED", "The upstream service is unavailable.", "UPSTREAM_UNAVAILABLE"
		if _, err := services.invocations.Fail(ctx, services.workspaceID, invocationID,
			execution.FailToolInvocationInput{
				OutputSummary: json.RawMessage(`{"retryable":true}`),
				RawObjectID:   invocationID, ErrorCode: errorCode,
			}); err != nil {
			t.Fatalf("fail direct tool invocation: %v", err)
		}
	} else if _, err := services.invocations.Complete(ctx, services.workspaceID, invocationID,
		execution.CompleteToolInvocationInput{
			OutputSummary: json.RawMessage(`{"order_status":"shipped"}`), RawObjectID: invocationID,
		}); err != nil {
		t.Fatalf("complete direct tool invocation: %v", err)
	}
	stepTransition := execution.StepTransition{
		ExpectedStatus: "RUNNING", NewStatus: status,
		OutputSummary: json.RawMessage(`{"invocationId":"` + invocationID + `"}`),
		ErrorCode:     errorCode,
	}
	if _, err := services.runs.TransitionAgentRunStep(ctx, services.workspaceID,
		step.ID, stepTransition); err != nil {
		t.Fatalf("complete direct tool run step: %v", err)
	}
	services.recordEvent(t, runID, "STEP_COMPLETED",
		json.RawMessage(`{"invocationId":"`+invocationID+`"}`))
	assistantID := uuid.NewString()
	if _, err := services.chat.RecordAssistantResult(ctx, chat.RecordAssistantResultInput{
		AssistantMessageID: assistantID, WorkspaceID: services.workspaceID,
		SessionID: services.sessionID, UserMessageID: sent.Message.ID, RunID: runID,
		Content: assistantContent, ExpectedRunStatus: "RUNNING", ExpectedRunLock: 1,
		RunStatus: status, RunOutputSummary: []byte(`{"invocationId":"` + invocationID + `"}`),
		RunErrorCode: errorCode,
	}); err != nil {
		t.Fatalf("record direct tool assistant result: %v", err)
	}
	terminalType := "RUN_COMPLETED"
	if shouldFail {
		terminalType = "RUN_FAILED"
	}
	services.recordEvent(t, runID, terminalType,
		json.RawMessage(fmt.Sprintf(`{"status":%q}`, status)))
	return acceptanceTurn{runID: runID, traceID: traceID, status: status}
}

func (services acceptanceServices) runWorkflowToolTurn(
	t *testing.T,
	content string,
) acceptanceTurn {
	t.Helper()
	ctx := context.Background()
	messageID, runID, traceID := uuid.NewString(), uuid.NewString(), "trace-"+uuid.NewString()
	sent, err := services.chat.SendMessage(ctx, chat.SendMessageInput{
		MessageID: messageID, RunID: runID, WorkspaceID: services.workspaceID,
		SessionID: services.sessionID, Content: content,
		CreatedBy: executionOwnerID, TraceID: traceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	services.recordEvent(t, runID, "RUN_STARTED", json.RawMessage(`{"source":"workflow"}`))
	workflowExecution, err := services.runService.StartWorkflowExecution(ctx,
		execution.StartWorkflowExecutionRequest{
			ID: uuid.NewString(), WorkspaceID: services.workspaceID,
			WorkflowID: executionWorkflowID, RevisionID: executionRevisionID,
			AgentRunID: runID, TriggerType: "AGENT", TriggeredByType: "USER",
			TriggeredByID: executionOwnerID, TraceID: traceID,
			InputSummary: json.RawMessage(`{"order_id":"A-1"}`),
		})
	if err != nil {
		t.Fatalf("start workflow invocation: %v", err)
	}
	executionStep, err := services.runs.AppendExecutionStep(ctx,
		execution.AppendExecutionStepInput{
			ID: uuid.NewString(), WorkspaceID: services.workspaceID,
			ExecutionID: workflowExecution.ID, NodeID: "tool-1", NodeType: "TOOL",
			InputSummary: json.RawMessage(`{"order_id":"A-1"}`),
		})
	if err != nil {
		t.Fatal(err)
	}
	invocationID, idempotencyKey := uuid.NewString(), "workflow-"+runID
	input := acceptanceInvocationInput(invocationID, runID, workflowExecution.ID,
		executionStep.ID, idempotencyKey)
	started, err := services.invocations.Start(ctx, input)
	if err != nil || !started.Created {
		t.Fatalf("start workflow tool invocation: %+v err=%v", started, err)
	}
	retry := input
	retry.ID, retry.TraceID = uuid.NewString(), "retry-"+traceID
	retried, err := services.invocations.Start(ctx, retry)
	if err != nil || retried.Created || retried.Invocation.ID != invocationID {
		t.Fatalf("workflow tool idempotent retry duplicated invocation: %+v err=%v", retried, err)
	}
	services.insertPermanentInvocationPayload(t, invocationID)
	if _, err := services.invocations.Complete(ctx, services.workspaceID, invocationID,
		execution.CompleteToolInvocationInput{
			OutputSummary: json.RawMessage(`{"workflow_tool":"ok"}`), RawObjectID: invocationID,
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := services.runs.TransitionExecutionStep(ctx, services.workspaceID,
		executionStep.ID, execution.StepTransition{
			ExpectedStatus: "RUNNING", NewStatus: "SUCCEEDED",
			OutputSummary: json.RawMessage(`{"invocationId":"` + invocationID + `"}`),
		}); err != nil {
		t.Fatal(err)
	}
	services.recordEvent(t, runID, "STEP_COMPLETED",
		json.RawMessage(`{"workflowExecutionId":"`+workflowExecution.ID+`"}`))
	if _, err := services.runs.TransitionWorkflowExecution(ctx, services.workspaceID,
		workflowExecution.ID, execution.RunTransition{
			ExpectedStatus: "RUNNING", ExpectedLockVersion: 1,
			NewStatus: "SUCCEEDED", OutputSummary: json.RawMessage(`{"result":"ok"}`),
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := services.chat.RecordAssistantResult(ctx, chat.RecordAssistantResultInput{
		AssistantMessageID: uuid.NewString(), WorkspaceID: services.workspaceID,
		SessionID: services.sessionID, UserMessageID: sent.Message.ID, RunID: runID,
		Content: "The published workflow completed.", ExpectedRunStatus: "RUNNING",
		ExpectedRunLock: 1, RunStatus: "SUCCEEDED",
		RunOutputSummary: []byte(`{"workflowExecutionId":"` + workflowExecution.ID + `"}`),
	}); err != nil {
		t.Fatal(err)
	}
	services.recordEvent(t, runID, "RUN_COMPLETED", json.RawMessage(`{"status":"SUCCEEDED"}`))
	var idempotentRows int
	if err := services.db.QueryRow(`
		SELECT count(*) FROM tool_invocations
		WHERE workspace_id=$1 AND tool_version_id=$2 AND idempotency_key=$3
	`, services.workspaceID, invocationVersionID, idempotencyKey).Scan(&idempotentRows); err != nil {
		t.Fatal(err)
	}
	if idempotentRows != 1 {
		t.Fatalf("idempotency retry created %d workflow invocations", idempotentRows)
	}
	return acceptanceTurn{runID: runID, traceID: traceID, status: "SUCCEEDED"}
}

func (services acceptanceServices) insertPermanentInvocationPayload(t *testing.T, invocationID string) {
	t.Helper()
	if _, err := services.db.Exec(`
		INSERT INTO stored_objects(
		 id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
		 encryption_key_id,classification,retention_mode,created_by_type,created_by_id
		) VALUES($1,$2,'actweave-executions',$3,'TOOL_INVOCATION_PAYLOAD',
		 'application/json',2,$4,'acceptance-tool-key-v1','SENSITIVE','PERMANENT','USER',$5)
	`, invocationID, services.workspaceID,
		services.workspaceID+"/tool-invocation/"+invocationID,
		strings.Repeat("d", 64), executionOwnerID); err != nil {
		t.Fatal(err)
	}
}

func (services acceptanceServices) recordEvent(
	t *testing.T,
	runID, eventType string,
	payload json.RawMessage,
) {
	t.Helper()
	if _, err := services.events.Append(context.Background(), execution.AppendRunEventInput{
		ID: uuid.NewString(), WorkspaceID: services.workspaceID,
		RunID: runID, EventType: eventType, Payload: payload,
	}); err != nil {
		t.Fatalf("record %s event: %v", eventType, err)
	}
}

func acceptanceInvocationInput(
	id, runID, workflowExecutionID, executionStepID, idempotencyKey string,
) execution.StartToolInvocationInput {
	return execution.StartToolInvocationInput{
		ID: id, WorkspaceID: executionWorkspaceID, ToolID: invocationToolID,
		ToolVersionID: invocationVersionID, CapabilityReleaseID: invocationReleaseID,
		ProviderID: invocationProviderID, ConnectionID: invocationConnectionID,
		AgentRunID: runID, WorkflowExecutionID: workflowExecutionID,
		ExecutionStepID: executionStepID, ActorType: "USER", ActorID: executionOwnerID,
		TraceID: "trace-invocation-" + runID, IdempotencyKey: idempotencyKey,
		InputSummary: json.RawMessage(`{"order_id":"A-1"}`),
	}
}
