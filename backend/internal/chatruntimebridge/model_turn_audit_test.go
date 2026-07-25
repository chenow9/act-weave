package chatruntimebridge_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestStreamDeltaRecorder_ModelTurnHookReceivesReasoning(t *testing.T) {
	var mu sync.Mutex
	var seen []einoruntime.ModelTurn
	projector := &chatruntimebridge.StreamDeltaRecorder{
		ModelTurnHook: func(_ context.Context, turn einoruntime.ModelTurn) error {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, turn)
			return nil
		},
	}

	sr, sw := schema.Pipe[*schema.Message](4)
	go func() {
		defer sw.Close()
		_ = sw.Send(&schema.Message{Role: schema.Assistant, ReasoningContent: "step A"}, nil)
		_ = sw.Send(&schema.Message{Role: schema.Assistant, Content: "out"}, nil)
	}()
	if err := einoruntime.ProjectAgentEvent(
		context.Background(), adk.EventFromMessage(nil, sr, schema.Assistant, ""), projector,
	); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("seen = %+v", seen)
	}
	if seen[0].Content != "out" || seen[0].Reasoning != "step A" {
		t.Fatalf("turn = %+v", seen[0])
	}
	got := projector.ModelTurns()
	if len(got) != 1 || got[0].Reasoning != "step A" {
		t.Fatalf("ModelTurns = %+v", got)
	}
}

func TestModelTurnAuditPath_DebugOnStoresReasoningInRecordInput(t *testing.T) {
	// Mirrors Bridge.recordModelTurn + ModelTurnContentService.Record contract:
	// debug on → Reasoning field + payload.reasoning for output_summary merge.
	var captured []chatruntime.ModelTurnRecordInput
	var appended []execution.AppendAgentRunStepInput
	recorder := &captureModelTurnRecorder{records: &captured}
	steps := &captureStepStore{appended: &appended}

	const (
		wsID            = "22222222-2222-7222-8222-222222222222"
		jobRunID        = "11111111-1111-7111-8111-111111111111"
		stepID          = "33333333-3333-7333-8333-333333333333"
		agentAuditDebug = true
	)

	projector := &chatruntimebridge.StreamDeltaRecorder{
		ModelTurnHook: func(ctx context.Context, turn einoruntime.ModelTurn) error {
			inputSummary, _ := json.Marshal(map[string]any{
				"source": "chatruntimebridge", "hasReasoning": turn.Reasoning != "",
			})
			if _, err := steps.AppendAgentRunStep(ctx, execution.AppendAgentRunStepInput{
				ID: stepID, WorkspaceID: wsID, RunID: jobRunID,
				StepType: "MODEL", InputSummary: inputSummary,
			}); err != nil {
				return err
			}
			payload := map[string]any{
				"content": turn.Content, "ok": true, "source": "chatruntimebridge",
			}
			reasoningForAudit := ""
			if agentAuditDebug {
				if text := strings.TrimSpace(turn.Reasoning); text != "" {
					payload["reasoning"] = text
					reasoningForAudit = text
				}
			}
			raw, _ := json.Marshal(payload)
			_, err := recorder.Record(ctx, chatruntime.ModelTurnRecordInput{
				WorkspaceID: wsID, StepID: stepID, Content: raw,
				CreatedByType: "USER", CreatedByID: "user-1",
				ExpectedStatus: "RUNNING", NewStatus: "SUCCEEDED",
				Reasoning: reasoningForAudit,
			})
			return err
		},
	}

	sr, sw := schema.Pipe[*schema.Message](4)
	go func() {
		defer sw.Close()
		_ = sw.Send(&schema.Message{Role: schema.Assistant, ReasoningContent: "I should verify first"}, nil)
		_ = sw.Send(&schema.Message{Role: schema.Assistant, Content: "done"}, nil)
	}()
	if err := einoruntime.ProjectAgentEvent(
		context.Background(), adk.EventFromMessage(nil, sr, schema.Assistant, ""), projector,
	); err != nil {
		t.Fatal(err)
	}

	if len(appended) != 1 || appended[0].StepType != "MODEL" {
		t.Fatalf("appended steps = %+v", appended)
	}
	if len(captured) != 1 {
		t.Fatalf("records = %+v", captured)
	}
	if captured[0].Reasoning != "I should verify first" {
		t.Fatalf("reasoning for audit = %q", captured[0].Reasoning)
	}
	var body map[string]any
	if err := json.Unmarshal(captured[0].Content, &body); err != nil {
		t.Fatal(err)
	}
	if body["reasoning"] != "I should verify first" {
		t.Fatalf("payload = %#v", body)
	}
}

func TestModelTurnAuditPath_DebugOffOmitsReasoning(t *testing.T) {
	var captured []chatruntime.ModelTurnRecordInput
	recorder := &captureModelTurnRecorder{records: &captured}
	steps := &captureStepStore{}
	const agentAuditDebug = false

	projector := &chatruntimebridge.StreamDeltaRecorder{
		ModelTurnHook: func(ctx context.Context, turn einoruntime.ModelTurn) error {
			payload := map[string]any{
				"content": turn.Content, "ok": true, "source": "chatruntimebridge",
			}
			reasoningForAudit := ""
			if agentAuditDebug {
				if text := strings.TrimSpace(turn.Reasoning); text != "" {
					payload["reasoning"] = text
					reasoningForAudit = text
				}
			}
			raw, _ := json.Marshal(payload)
			_, err := recorder.Record(ctx, chatruntime.ModelTurnRecordInput{
				WorkspaceID: "ws", StepID: "step", Content: raw,
				CreatedByType: "USER", CreatedByID: "u",
				ExpectedStatus: "RUNNING", NewStatus: "SUCCEEDED",
				Reasoning: reasoningForAudit,
			})
			_ = steps
			return err
		},
	}

	sr, sw := schema.Pipe[*schema.Message](4)
	go func() {
		defer sw.Close()
		_ = sw.Send(&schema.Message{Role: schema.Assistant, ReasoningContent: "secret"}, nil)
		_ = sw.Send(&schema.Message{Role: schema.Assistant, Content: "public"}, nil)
	}()
	if err := einoruntime.ProjectAgentEvent(
		context.Background(), adk.EventFromMessage(nil, sr, schema.Assistant, ""), projector,
	); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 {
		t.Fatalf("records = %+v", captured)
	}
	if captured[0].Reasoning != "" {
		t.Fatalf("debug off must not pass reasoning: %q", captured[0].Reasoning)
	}
	var body map[string]any
	_ = json.Unmarshal(captured[0].Content, &body)
	if _, ok := body["reasoning"]; ok {
		t.Fatalf("debug off payload must omit reasoning: %#v", body)
	}
}

// --- fakes -----------------------------------------------------------------

type captureModelTurnRecorder struct {
	records *[]chatruntime.ModelTurnRecordInput
}

func (c *captureModelTurnRecorder) Record(
	_ context.Context,
	input chatruntime.ModelTurnRecordInput,
) (execution.AgentRunStep, error) {
	if c.records != nil {
		*c.records = append(*c.records, input)
	}
	return execution.AgentRunStep{ID: input.StepID, Status: input.NewStatus}, nil
}

type captureStepStore struct {
	appended *[]execution.AppendAgentRunStepInput
}

func (s *captureStepStore) AppendAgentRunStep(
	_ context.Context,
	input execution.AppendAgentRunStepInput,
) (execution.AgentRunStep, error) {
	if s.appended != nil {
		*s.appended = append(*s.appended, input)
	}
	return execution.AgentRunStep{ID: input.ID, StepType: input.StepType, Status: "RUNNING"}, nil
}

func (s *captureStepStore) TransitionAgentRunStep(
	context.Context, string, string, execution.StepTransition,
) (execution.AgentRunStep, error) {
	return execution.AgentRunStep{}, nil
}

func (s *captureStepStore) TransitionAgentRun(
	context.Context, string, string, execution.RunTransition,
) (execution.AgentRun, error) {
	return execution.AgentRun{}, nil
}

func (s *captureStepStore) StartWorkflowExecution(
	context.Context, execution.StartWorkflowExecutionInput,
) (execution.WorkflowExecution, error) {
	return execution.WorkflowExecution{}, nil
}

func (s *captureStepStore) TransitionWorkflowExecution(
	context.Context, string, string, execution.RunTransition,
) (execution.WorkflowExecution, error) {
	return execution.WorkflowExecution{}, nil
}

func (s *captureStepStore) GetAgentRun(context.Context, string, string) (execution.AgentRun, error) {
	return execution.AgentRun{}, nil
}
