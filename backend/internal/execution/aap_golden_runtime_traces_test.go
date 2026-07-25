package execution_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/domain"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/workflowruntime"

	"github.com/google/uuid"
)

func TestAAPGoldenRuntimeTraces(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		fixture := newAAPRuntimeTraceFixture(t, "text", "CHAT")
		fixture.projectAssistant(t, "你好，欢迎使用 ActWeave。", 1)
		fixture.projectUsage(t, 12, 8)
		fixture.completeRun(t)
		fixture.verify(t, []string{
			protocolevent.EventRunAccepted, protocolevent.EventRunStarted,
			protocolevent.EventItemStarted, protocolevent.EventItemDelta, protocolevent.EventItemDelta,
			protocolevent.EventItemCompleted, protocolevent.EventUsageUpdated,
			protocolevent.EventRunCompleted,
		}, nil)
	})

	t.Run("tool success", func(t *testing.T) {
		fixture := newAAPRuntimeTraceFixture(t, "tool", "API")
		invocation := fixture.startTool(t, `{"city":"Singapore"}`, "weather.lookup", 1, nil, nil)
		output := fixture.sideEffect(`{"temperatureC":29,"condition":"cloudy"}`)
		fixture.completeTool(t, invocation, "weather.lookup", output)
		fixture.projectAssistant(t, "Singapore 当前 29°C，多云。", 2)
		fixture.projectUsage(t, 24, 13)
		fixture.completeRun(t)
		fixture.verify(t, []string{
			protocolevent.EventRunAccepted, protocolevent.EventRunStarted,
			protocolevent.EventItemStarted, protocolevent.EventItemDelta,
			protocolevent.EventItemCompleted, protocolevent.EventItemStarted,
			protocolevent.EventItemDelta, protocolevent.EventItemDelta,
			protocolevent.EventItemCompleted,
			protocolevent.EventUsageUpdated, protocolevent.EventRunCompleted,
		}, nil)
	})

	t.Run("workflow and tool", func(t *testing.T) {
		fixture := newAAPRuntimeTraceFixture(t, "workflow-tool", "WORKFLOW")
		workflowResult := fixture.runPublishedWorkflow(t)
		workflowFact, err := fixture.runService.StartWorkflowExecution(
			context.Background(), execution.StartWorkflowExecutionRequest{
				ID: workflowResult.Execution.ID, WorkspaceID: fixture.run.WorkspaceID,
				WorkflowID: executionWorkflowID, RevisionID: executionRevisionID,
				AgentRunID: fixture.run.ID, TriggerType: "AGENT", TriggeredByType: "USER",
				TriggeredByID: executionOwnerID, TraceID: fixture.run.TraceID,
				InputSummary: json.RawMessage(`{"customerId":"C-42"}`),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		domainStep := workflowResult.Execution.Steps[0]
		step, err := fixture.runs.AppendExecutionStep(context.Background(), execution.AppendExecutionStepInput{
			ID: domainStep.ID, WorkspaceID: fixture.run.WorkspaceID,
			ExecutionID: workflowFact.ID, NodeID: domainStep.NodeID, NodeType: domainStep.NodeType,
			InputSummary: json.RawMessage(domainStep.InputSummary),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.workflowProjector.ProjectStarted(
			context.Background(), execution.ProjectWorkflowStepStartedInput{
				Context: fixture.toolContext, Execution: workflowFact, Step: step, Ordinal: 1,
			},
		); err != nil {
			t.Fatal(err)
		}
		total := float64(2)
		if _, err := fixture.workflowProjector.ProjectProgress(
			context.Background(), execution.ProjectWorkflowStepProgressInput{
				Context: fixture.toolContext, Execution: workflowFact, Step: step,
				Current: 1, Total: &total, Unit: "steps",
				Message: "正在调用客户查询工具", OccurredAt: time.Now().UTC(),
			},
		); err != nil {
			t.Fatal(err)
		}
		invocation := fixture.startTool(
			t, `{"customerId":"C-42"}`, "customer.lookup", 2, &workflowFact, &step,
		)
		fixture.completeTool(t, invocation, "customer.lookup", domainStep.OutputSummary)
		step, err = fixture.runs.TransitionExecutionStep(
			context.Background(), fixture.run.WorkspaceID, step.ID, execution.StepTransition{
				ExpectedStatus: "RUNNING", NewStatus: "SUCCEEDED",
				OutputSummary: json.RawMessage(domainStep.OutputSummary),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.workflowProjector.Complete(
			context.Background(), execution.CompleteProtocolWorkflowStepInput{
				Context: fixture.toolContext, Execution: workflowFact,
				Step: step, CompletedAt: *step.FinishedAt,
			},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.runs.TransitionWorkflowExecution(
			context.Background(), fixture.run.WorkspaceID, workflowFact.ID, execution.RunTransition{
				ExpectedStatus: "RUNNING", ExpectedLockVersion: workflowFact.LockVersion,
				NewStatus: "SUCCEEDED", OutputSummary: json.RawMessage(domainStep.OutputSummary),
			},
		); err != nil {
			t.Fatal(err)
		}
		fixture.projectUsage(t, 30, 6)
		fixture.completeRun(t)
		fixture.verify(t, []string{
			protocolevent.EventRunAccepted, protocolevent.EventRunStarted,
			protocolevent.EventItemStarted, protocolevent.EventItemDelta,
			protocolevent.EventItemStarted, protocolevent.EventItemDelta,
			protocolevent.EventItemCompleted, protocolevent.EventItemCompleted,
			protocolevent.EventUsageUpdated, protocolevent.EventRunCompleted,
		}, nil)
	})

	t.Run("approval resume", func(t *testing.T) {
		fixture := newAAPRuntimeTraceFixture(t, "approval", "API")
		invocation := fixture.startTool(t, `{"orderId":"A-1","amount":88}`, "payment.refund", 1, nil, nil)
		if _, err := fixture.toolProjector.ProjectWaiting(
			context.Background(), execution.ProjectToolCallDeltaInput{
				Context: fixture.toolContext, Invocation: invocation, OccurredAt: time.Now().UTC(),
			},
		); err != nil {
			t.Fatal(err)
		}
		confirmationInput := json.RawMessage(`{"orderId":"A-1","amount":88}`)
		decision, err := execution.EvaluateConfirmationPolicy(execution.ConfirmationPolicyInput{
			WorkspaceSettings: json.RawMessage(`{}`),
			Release: execution.ConfirmationReleaseRisk{
				ReleaseID: invocationReleaseID, RiskLevel: "HIGH",
				SideEffectLevel: "IRREVERSIBLE", RequiresConfirmation: true,
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
			Connection: execution.ConfirmationConnectionRisk{
				ConnectionID: invocationConnectionID, Environment: "PRODUCTION",
			},
			Input: confirmationInput,
		})
		if err != nil {
			t.Fatal(err)
		}
		confirmationRepository, err := execution.NewConfirmationRepository(fixture.db)
		if err != nil {
			t.Fatal(err)
		}
		confirmationService, err := execution.NewConfirmationService(confirmationRepository)
		if err != nil {
			t.Fatal(err)
		}
		requested, err := confirmationService.Request(context.Background(), execution.RequestExecutionConfirmationInput{
			ID: uuid.NewString(), WorkspaceID: fixture.run.WorkspaceID, RunID: fixture.run.ID,
			TargetItemID: invocation.ID,
			NodeID:       "refund", ReleaseID: invocationReleaseID,
			ConnectionID: invocationConnectionID, PlanHash: executionPlanHash,
			RequestedBy: executionOwnerID, Decision: decision,
		})
		if err != nil {
			t.Fatal(err)
		}
		presentation := execution.InteractionPresentation{
			TargetItemID: invocation.ID, Title: "批准退款", RiskLevel: "HIGH",
			RiskReasons:  append([]string(nil), requested.Confirmation.RiskReasons...),
			InputSummary: confirmationInput,
			AllowedDecisions: []protocolevent.InteractionDecision{
				protocolevent.InteractionDecisionApprove, protocolevent.InteractionDecisionCancel,
			},
			RequiredDecider: protocolevent.RequiredDeciderActWeaveUser,
		}
		if _, err := fixture.interactionProjector.ProjectRequested(
			context.Background(), execution.ProjectInteractionRequestedInput{
				Context: fixture.interactionContext, Confirmation: requested.Confirmation,
				Presentation: presentation, Ordinal: 2,
			},
		); err != nil {
			t.Fatal(err)
		}
		waiting, err := fixture.lifecycle.TransitionAgentRun(
			context.Background(), execution.ProtocolRunTransitionInput{
				WorkspaceID: fixture.run.WorkspaceID, RunID: fixture.run.ID,
				Transition: execution.RunTransition{
					ExpectedStatus: "RUNNING", ExpectedLockVersion: fixture.run.LockVersion,
					NewStatus: "WAITING_CONFIRMATION", OutputSummary: json.RawMessage(`{"waiting":true}`),
				},
				InteractionIDs: []string{requested.Confirmation.ID},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		confirmed, err := confirmationService.Confirm(context.Background(), execution.ConfirmExecutionConfirmationInput{
			WorkspaceID: fixture.run.WorkspaceID, ConfirmationID: requested.Confirmation.ID,
			ActorID: executionOwnerID, ResumeToken: requested.ResumeToken,
			RunID: fixture.run.ID, TargetItemID: invocation.ID,
			ReleaseID: invocationReleaseID, ConnectionID: invocationConnectionID,
			PlanHash: executionPlanHash, Input: confirmationInput,
			ExpectedLockVersion: requested.Confirmation.LockVersion,
		})
		if err != nil || confirmed.ConfirmedAt == nil {
			t.Fatalf("confirm approval=%+v err=%v", confirmed, err)
		}
		if _, err := fixture.interactionProjector.ProjectTerminal(
			context.Background(), execution.ProjectInteractionTerminalInput{
				Context: fixture.interactionContext, Confirmation: confirmed,
				Presentation: presentation, OccurredAt: *confirmed.ConfirmedAt,
			},
		); err != nil {
			t.Fatal(err)
		}
		resumed, err := fixture.lifecycle.TransitionAgentRun(
			context.Background(), execution.ProtocolRunTransitionInput{
				WorkspaceID: fixture.run.WorkspaceID, RunID: fixture.run.ID,
				Transition: execution.RunTransition{
					ExpectedStatus: "WAITING_CONFIRMATION", ExpectedLockVersion: waiting.Run.LockVersion,
					NewStatus: "RUNNING", OutputSummary: json.RawMessage(`{"resumed":true}`),
				},
				InteractionID: confirmed.ID,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		fixture.run = resumed.Run
		output := fixture.sideEffect(`{"refundId":"R-900","status":"accepted"}`)
		fixture.completeTool(t, invocation, "payment.refund", output)
		fixture.projectUsage(t, 40, 9)
		fixture.completeRun(t)
		fixture.verify(t, []string{
			protocolevent.EventRunAccepted, protocolevent.EventRunStarted,
			protocolevent.EventItemStarted, protocolevent.EventItemDelta,
			protocolevent.EventItemDelta, protocolevent.EventInteractionRequested,
			protocolevent.EventRunWaiting, protocolevent.EventInteractionResolved,
			protocolevent.EventRunResumed, protocolevent.EventItemCompleted,
			protocolevent.EventUsageUpdated, protocolevent.EventRunCompleted,
		}, []string{requested.ResumeToken})
	})
}

type aapRuntimeTraceFixture struct {
	db                   *sql.DB
	runs                 *execution.RunRepository
	runService           *execution.RunService
	lifecycle            *execution.ProtocolRunLifecycleService
	reader               *protocolevent.EventReader
	messageProjector     *chat.ProtocolMessageProjector
	toolRepository       *execution.ToolInvocationRepository
	toolProjector        *execution.ProtocolToolCallProjector
	workflowProjector    *execution.ProtocolWorkflowStepProjector
	interactionProjector *execution.ProtocolInteractionProjector
	auxiliaryProjector   *chatruntime.AuxiliaryProtocolProjector
	run                  execution.AgentRun
	messageContext       chat.ProtocolMessageContext
	toolContext          execution.ProtocolToolCallContext
	interactionContext   execution.ProtocolInteractionContext
	sideEffects          atomic.Int64
}

func newAAPRuntimeTraceFixture(t *testing.T, suffix, trigger string) *aapRuntimeTraceFixture {
	t.Helper()
	runs, runService, db, _ := newRunStateTest(t)
	db.SetMaxOpenConns(16)
	unit, err := protocolevent.NewProtocolUnitOfWork(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := execution.NewProtocolRunLifecycleService(runs, unit)
	if err != nil {
		t.Fatal(err)
	}
	request := runStateAgentRequest(uuid.NewString(), "trace-aap-runtime-"+suffix)
	request.TriggerType = trigger
	prepared, err := runService.PrepareAgentRun(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	started, err := lifecycle.AcceptAndStartAgentRun(context.Background(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	messageProjector, err := chat.NewProtocolMessageProjector(
		unit, chat.NewProtocolMessageMapper(nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	toolRepository, err := execution.NewToolInvocationRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	toolProjector, err := execution.NewProtocolToolCallProjector(
		unit, execution.NewToolCallProtocolMapper(),
	)
	if err != nil {
		t.Fatal(err)
	}
	workflowProjector, err := execution.NewProtocolWorkflowStepProjector(
		unit, toolRepository, execution.NewWorkflowStepProtocolMapper(),
	)
	if err != nil {
		t.Fatal(err)
	}
	interactionProjector, err := execution.NewProtocolInteractionProjector(
		unit, execution.NewInteractionProtocolMapper(),
	)
	if err != nil {
		t.Fatal(err)
	}
	auxiliaryProjector, err := chatruntime.NewAuxiliaryProtocolProjector(
		unit, chatruntime.NewAuxiliaryProtocolMapper(),
	)
	if err != nil {
		t.Fatal(err)
	}
	scope := protocolevent.RunScope{
		WorkspaceID: started.Run.WorkspaceID, AgentID: started.Run.AgentID,
		ConversationID: started.Run.SessionID, RunID: started.Run.ID,
	}
	return &aapRuntimeTraceFixture{
		db: db, runs: runs, runService: runService, lifecycle: lifecycle, reader: reader,
		messageProjector: messageProjector, toolRepository: toolRepository,
		toolProjector: toolProjector, workflowProjector: workflowProjector,
		interactionProjector: interactionProjector, auxiliaryProjector: auxiliaryProjector,
		run: started.Run,
		messageContext: chat.ProtocolMessageContext{
			Scope: scope, EventStreamID: started.Run.ID, TraceID: started.Run.TraceID,
		},
		toolContext: execution.ProtocolToolCallContext{
			Scope: scope, EventStreamID: started.Run.ID, TraceID: started.Run.TraceID,
		},
		interactionContext: execution.ProtocolInteractionContext{
			Scope: scope, EventStreamID: started.Run.ID, TraceID: started.Run.TraceID,
		},
	}
}

func (fixture *aapRuntimeTraceFixture) projectAssistant(t *testing.T, content string, ordinal int) {
	t.Helper()
	messageID := uuid.NewString()
	startedAt := time.Now().UTC()
	if _, err := fixture.messageProjector.ProjectStarted(
		context.Background(), chat.ProjectStartedMessageInput{
			Context: fixture.messageContext, MessageID: messageID,
			Role: "ASSISTANT", Ordinal: ordinal, StartedAt: startedAt,
		},
	); err != nil {
		t.Fatal(err)
	}
	fragments := []string{content}
	if len([]rune(content)) > 6 {
		runes := []rune(content)
		middle := len(runes) / 2
		fragments = []string{string(runes[:middle]), string(runes[middle:])}
	}
	for _, fragment := range fragments {
		if _, err := fixture.messageProjector.ProjectDelta(
			context.Background(), chat.ProjectMessageDeltaInput{
				Context: fixture.messageContext, MessageID: messageID, Index: 0,
				Text: fragment, OccurredAt: time.Now().UTC(),
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	hash := sha256.Sum256([]byte(content))
	if _, err := fixture.db.Exec(`
		INSERT INTO chat_messages(
		 id,workspace_id,session_id,role,content,content_sha256,content_length,status,run_id
		) VALUES($1,$2,$3,'ASSISTANT',$4,$5,$6,'EXECUTED',$7)
	`, messageID, fixture.run.WorkspaceID, fixture.run.SessionID, content,
		hex.EncodeToString(hash[:]), len([]byte(content)), fixture.run.ID); err != nil {
		t.Fatal(err)
	}
	repository, err := chat.NewRepository(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	message, err := repository.GetMessage(context.Background(), fixture.run.WorkspaceID, messageID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.messageProjector.CompleteProjected(
		context.Background(), chat.CompleteProjectedMessageInput{
			Context: fixture.messageContext, Message: message,
			ActorID: executionOwnerID, CompletedAt: message.CreatedAt,
		},
	); err != nil {
		t.Fatal(err)
	}
}

func (fixture *aapRuntimeTraceFixture) startTool(
	t *testing.T,
	arguments, name string,
	ordinal int,
	workflow *execution.WorkflowExecution,
	step *execution.ExecutionStep,
) execution.ToolInvocation {
	t.Helper()
	input := validToolInvocationStart(uuid.NewString(), uuid.NewString())
	input.AgentRunID = fixture.run.ID
	input.TraceID = fixture.run.TraceID
	input.InputSummary = json.RawMessage(arguments)
	input.WorkflowExecutionID, input.ExecutionStepID = "", ""
	if workflow != nil && step != nil {
		input.WorkflowExecutionID, input.ExecutionStepID = workflow.ID, step.ID
	}
	started, err := fixture.toolRepository.Start(context.Background(), input)
	if err != nil || !started.Created {
		t.Fatalf("start runtime tool=%+v err=%v", started, err)
	}
	if _, err := fixture.toolProjector.ProjectStarted(
		context.Background(), execution.ProjectToolCallStartedInput{
			Context: fixture.toolContext, Invocation: started.Invocation,
			Name: name, Ordinal: ordinal,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.toolProjector.ProjectArguments(
		context.Background(), execution.ProjectToolCallDeltaInput{
			Context: fixture.toolContext, Invocation: started.Invocation,
			OccurredAt: time.Now().UTC(),
		},
	); err != nil {
		t.Fatal(err)
	}
	return started.Invocation
}

func (fixture *aapRuntimeTraceFixture) completeTool(
	t *testing.T,
	invocation execution.ToolInvocation,
	name, output string,
) {
	t.Helper()
	finishedAt := time.Now().UTC()
	completed, err := fixture.toolRepository.Complete(
		context.Background(), fixture.run.WorkspaceID, invocation.ID,
		execution.CompleteToolInvocationInput{
			OutputSummary: json.RawMessage(output), RawObjectID: runStateRawObjectID,
			FinishedAt: finishedAt,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.toolProjector.Complete(
		context.Background(), execution.CompleteProtocolToolCallInput{
			Context: fixture.toolContext, Invocation: completed,
			Name: name, CompletedAt: *completed.FinishedAt,
		},
	); err != nil {
		t.Fatal(err)
	}
}

func (fixture *aapRuntimeTraceFixture) projectUsage(t *testing.T, input, output int64) {
	t.Helper()
	if _, err := fixture.auxiliaryProjector.ProjectUsage(
		context.Background(), chatruntime.ProjectUsageInput{
			Context: chatruntime.AuxiliaryProtocolContext{
				Scope: fixture.toolContext.Scope, EventStreamID: fixture.run.ID,
				TraceID: fixture.run.TraceID,
			},
			InputTokens: input, OutputTokens: output, OccurredAt: time.Now().UTC(),
		},
	); err != nil {
		t.Fatal(err)
	}
}

func (fixture *aapRuntimeTraceFixture) completeRun(t *testing.T) {
	t.Helper()
	current, err := fixture.runs.GetAgentRun(
		context.Background(), fixture.run.WorkspaceID, fixture.run.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := fixture.lifecycle.TransitionAgentRun(
		context.Background(), execution.ProtocolRunTransitionInput{
			WorkspaceID: current.WorkspaceID, RunID: current.ID,
			Transition: execution.RunTransition{
				ExpectedStatus: "RUNNING", ExpectedLockVersion: current.LockVersion,
				NewStatus: "SUCCEEDED", OutputSummary: json.RawMessage(`{"ok":true}`),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.run = completed.Run
}

func (fixture *aapRuntimeTraceFixture) sideEffect(output string) string {
	fixture.sideEffects.Add(1)
	return output
}

func (fixture *aapRuntimeTraceFixture) runPublishedWorkflow(
	t *testing.T,
) workflowruntime.PublishedRunResult {
	t.Helper()
	executionID, stepID := uuid.NewString(), uuid.NewString()
	resolver := &goldenWorkflowResolver{snapshot: workflowruntime.RevisionSnapshot{
		WorkspaceID: fixture.run.WorkspaceID, CapabilityID: executionWorkflowID,
		ReleaseID: invocationReleaseID, RevisionID: executionRevisionID,
		PlanHash: executionPlanHash,
		Plan: domain.CompiledExecutionPlan{
			WorkflowID: executionWorkflowID,
			Nodes: []domain.ExecutionPlanNode{{
				NodeID: "fetch-customer", Type: "TOOL", Config: map[string]any{},
			}},
		},
	}}
	executor := &goldenWorkflowExecutor{
		sideEffects: &fixture.sideEffects, executionID: executionID, stepID: stepID,
	}
	runner, err := workflowruntime.NewPublishedRevisionRunner(resolver, executor)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), workflowruntime.PublishedRunRequest{
		WorkspaceID: fixture.run.WorkspaceID, CapabilityID: executionWorkflowID,
		ReleaseID: invocationReleaseID, ActorID: executionOwnerID,
		Input: map[string]any{"customerId": "C-42"},
	})
	if err != nil || resolver.calls.Load() != 1 || result.Execution.Status != domain.ExecutionSuccess {
		t.Fatalf("published workflow result=%+v resolves=%d err=%v", result, resolver.calls.Load(), err)
	}
	return result
}

type goldenWorkflowResolver struct {
	snapshot workflowruntime.RevisionSnapshot
	calls    atomic.Int64
}

func (resolver *goldenWorkflowResolver) ResolveRevisionSnapshot(
	context.Context,
	string,
	string,
	string,
) (workflowruntime.RevisionSnapshot, error) {
	resolver.calls.Add(1)
	return resolver.snapshot, nil
}

type goldenWorkflowExecutor struct {
	sideEffects *atomic.Int64
	executionID string
	stepID      string
}

func (executor *goldenWorkflowExecutor) Run(
	plan domain.CompiledExecutionPlan,
	ctx workflowruntime.ExecutionContext,
) (domain.Execution, error) {
	execution, _, err := executor.RunWithCheckpoint(plan, ctx)
	return execution, err
}

func (executor *goldenWorkflowExecutor) RunWithCheckpoint(
	_ domain.CompiledExecutionPlan,
	ctx workflowruntime.ExecutionContext,
) (domain.Execution, *workflowruntime.WorkflowApprovalCheckpoint, error) {
	executor.sideEffects.Add(1)
	now := time.Now().UTC()
	output := `{"customerId":"C-42","tier":"gold"}`
	return domain.Execution{
		ID: executor.executionID, WorkflowID: executionWorkflowID,
		WorkflowVersion: executionRevisionID, WorkspaceID: ctx.WorkspaceID,
		Trigger: ctx.Trigger, UserID: ctx.UserID, Status: domain.ExecutionSuccess,
		OutputSummary: output, StartedAt: now, FinishedAt: now.Add(time.Millisecond),
		Steps: []domain.ExecutionStepRecord{{
			ID: executor.stepID, ExecutionID: executor.executionID,
			Name: "Fetch customer", NodeID: "fetch-customer", NodeType: "TOOL",
			Status: domain.ExecutionStepPassed, InputSummary: `{"customerId":"C-42"}`,
			OutputSummary: output, StartedAt: now, FinishedAt: now.Add(time.Millisecond),
		}},
	}, nil, nil
}

func (executor *goldenWorkflowExecutor) ResumeApproval(
	_ domain.CompiledExecutionPlan,
	ctx workflowruntime.ExecutionContext,
	_ workflowruntime.WorkflowApprovalCheckpoint,
	_ workflowruntime.ApprovalResumeDecision,
) (domain.Execution, error) {
	execution, _, err := executor.RunWithCheckpoint(domain.CompiledExecutionPlan{}, ctx)
	return execution, err
}

func (fixture *aapRuntimeTraceFixture) verify(
	t *testing.T,
	wantTypes []string,
	additionalForbidden []string,
) {
	t.Helper()
	events, err := fixture.reader.ReadRunAfter(
		context.Background(), fixture.toolContext.Scope, 0, 500,
	)
	if err != nil {
		t.Fatal(err)
	}
	actualTypes := make([]string, 0, len(events))
	for index, event := range events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("runtime trace sequence[%d]=%d", index, event.Sequence)
		}
		actualTypes = append(actualTypes, event.Type)
		payload := strings.ToLower(string(event.Payload))
		for _, forbidden := range append([]string{
			`"authorization"`, `"resumetoken"`, `"scopesnapshot"`,
			`"headers"`, `"password"`, `"secret"`, `"rawobject"`,
			`"compiledplan"`, `"executionplan"`, "bearer ",
		}, additionalForbidden...) {
			if forbidden != "" && strings.Contains(payload, strings.ToLower(forbidden)) {
				t.Fatalf("runtime trace leaked %q at sequence %d: %s", forbidden, event.Sequence, event.Payload)
			}
		}
	}
	if fmt.Sprint(actualTypes) != fmt.Sprint(wantTypes) {
		t.Fatalf("runtime event types=%v want=%v", actualTypes, wantTypes)
	}

	beforeReplay := fixture.sideEffects.Load()
	for boundary := 0; boundary <= len(events); boundary++ {
		reducer := protocolevent.NewRunReducer()
		if err := reducer.ApplyAll(events[:boundary]); err != nil {
			t.Fatalf("reduce boundary %d: %v", boundary, err)
		}
		snapshot, err := reducer.Snapshot()
		if err != nil || snapshot.LastSequence != int64(boundary) {
			t.Fatalf("snapshot boundary %d=%+v err=%v", boundary, snapshot, err)
		}
	}
	if fixture.sideEffects.Load() != beforeReplay {
		t.Fatalf("event replay repeated side effect %d -> %d", beforeReplay, fixture.sideEffects.Load())
	}

	reducer := protocolevent.NewRunReducer()
	if err := reducer.ApplyAll(events); err != nil {
		t.Fatal(err)
	}
	snapshot, err := reducer.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	storedRun, err := fixture.runs.GetAgentRun(
		context.Background(), fixture.run.WorkspaceID, fixture.run.ID,
	)
	if err != nil || snapshot.Run == nil || snapshot.Run.ID != storedRun.ID ||
		snapshot.Run.ConversationID != storedRun.SessionID || snapshot.Run.AgentID != storedRun.AgentID ||
		snapshot.Run.Status != protocolevent.RunStatusCompleted || storedRun.Status != "SUCCEEDED" ||
		snapshot.Run.CompletedAt == nil || storedRun.FinishedAt == nil ||
		!snapshot.Run.CompletedAt.Equal(storedRun.FinishedAt.UTC()) {
		t.Fatalf("reduced/database run mismatch reduced=%+v stored=%+v err=%v", snapshot.Run, storedRun, err)
	}
	assertReducedItemsMatchDatabase(t, fixture.db, fixture.run, snapshot)
}

func assertReducedItemsMatchDatabase(
	t *testing.T,
	db *sql.DB,
	run execution.AgentRun,
	snapshot protocolevent.ReducedRunSnapshot,
) {
	t.Helper()
	reducedItems := make(map[string]protocolevent.Item, len(snapshot.Items))
	for _, item := range snapshot.Items {
		reducedItems[item.ItemID()] = item
	}
	reducedInteractions := make(map[string]protocolevent.Interaction, len(snapshot.Interactions))
	for _, interaction := range snapshot.Interactions {
		reducedInteractions[interaction.ID] = interaction
	}
	rows, err := db.Query(`
		SELECT id,item_type,snapshot FROM run_items
		WHERE workspace_id=$1 AND agent_id=$2 AND run_id=$3 ORDER BY ordinal
	`, run.WorkspaceID, run.AgentID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	itemCount, interactionCount := 0, 0
	for rows.Next() {
		var id, itemType string
		var raw json.RawMessage
		if err := rows.Scan(&id, &itemType, &raw); err != nil {
			t.Fatal(err)
		}
		stored, err := protocolevent.DecodeItem(raw)
		if err != nil {
			t.Fatal(err)
		}
		if itemType == string(protocolevent.ItemTypeInteraction) {
			interactionCount++
			storedInteraction := stored.(protocolevent.InteractionItem).Interaction
			reduced, ok := reducedInteractions[id]
			if !ok || !sameProtocolJSON(storedInteraction, reduced) {
				t.Fatalf("interaction projection mismatch id=%s stored=%+v reduced=%+v", id, storedInteraction, reduced)
			}
			continue
		}
		itemCount++
		reduced, ok := reducedItems[id]
		if !ok || !sameProtocolJSON(stored, reduced) {
			t.Fatalf("item projection mismatch id=%s stored=%+v reduced=%+v", id, stored, reduced)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if itemCount != len(snapshot.Items) || interactionCount != len(snapshot.Interactions) {
		t.Fatalf("projection counts item=%d/%d interaction=%d/%d",
			itemCount, len(snapshot.Items), interactionCount, len(snapshot.Interactions))
	}
}

func sameProtocolJSON(left, right any) bool {
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	var leftValue, rightValue any
	if json.Unmarshal(leftRaw, &leftValue) != nil || json.Unmarshal(rightRaw, &rightValue) != nil {
		return false
	}
	leftCanonical, _ := json.Marshal(leftValue)
	rightCanonical, _ := json.Marshal(rightValue)
	return string(leftCanonical) == string(rightCanonical)
}
