package chatruntimebridge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/execution"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// localCaptureModelTurn records ModelTurnRecordInput for nested audit unit tests.
type localCaptureModelTurn struct {
	records *[]chatruntime.ModelTurnRecordInput
}

func (c *localCaptureModelTurn) Record(_ context.Context, input chatruntime.ModelTurnRecordInput) (execution.AgentRunStep, error) {
	if c.records != nil {
		*c.records = append(*c.records, input)
	}
	return execution.AgentRunStep{ID: input.StepID, Status: input.NewStatus}, nil
}

func TestNestedAuditModel_SuccessWritesMODELAndEvidence(t *testing.T) {
	t.Parallel()
	store := &captureStepStoreWithTransitions{}
	turns := &localCaptureModelTurn{records: &[]chatruntime.ModelTurnRecordInput{}}
	bridge := &Bridge{steps: store, modelTurns: turns}
	inner := &fixedModel{msg: schema.AssistantMessage("child answer", nil)}
	wrapped := wrapNestedAuditModel(inner, bridge)

	delID := uuid.Must(uuid.NewV7()).String()
	parentStep := uuid.Must(uuid.NewV7()).String()
	ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
		WorkspaceID: ws, ParentRunID: run, RunID: run, CallerAgentID: "agent-b",
		ParentDelegationID: &delID, ParentStepID: &parentStep,
	})
	out, err := wrapped.Generate(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "child answer" {
		t.Fatalf("content=%q", out.Content)
	}
	if len(store.appended) != 1 || store.appended[0].StepType != "MODEL" {
		t.Fatalf("steps=%+v", store.appended)
	}
	if store.appended[0].AgentID != "agent-b" || store.appended[0].DelegationID != delID {
		t.Fatalf("attribution=%+v", store.appended[0])
	}
	if len(*turns.records) != 1 || (*turns.records)[0].NewStatus != "SUCCEEDED" {
		t.Fatalf("evidence=%+v", *turns.records)
	}
}

func TestNestedAuditModel_FailureWritesFAILEDEvidence(t *testing.T) {
	t.Parallel()
	store := &captureStepStoreWithTransitions{}
	turns := &localCaptureModelTurn{records: &[]chatruntime.ModelTurnRecordInput{}}
	bridge := &Bridge{steps: store, modelTurns: turns}
	inner := &fixedModel{err: errors.New("upstream boom")}
	wrapped := wrapNestedAuditModel(inner, bridge)

	delID := uuid.Must(uuid.NewV7()).String()
	ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
		WorkspaceID: ws, ParentRunID: run, RunID: run, CallerAgentID: "agent-b",
		ParentDelegationID: &delID,
	})
	_, err := wrapped.Generate(ctx, nil)
	if err == nil {
		t.Fatal("expected model error")
	}
	if len(store.appended) != 1 {
		t.Fatalf("want FAILED MODEL step, got %+v", store.appended)
	}
	if len(*turns.records) != 1 || (*turns.records)[0].NewStatus != "FAILED" {
		t.Fatalf("evidence=%+v", *turns.records)
	}
	if (*turns.records)[0].ErrorCode != "NESTED_MODEL_FAILED" {
		t.Fatalf("errorCode=%s", (*turns.records)[0].ErrorCode)
	}
}

func TestNestedAuditModel_MissingRecorderFailClosed(t *testing.T) {
	t.Parallel()
	bridge := &Bridge{steps: &captureStepStoreWithTransitions{}, modelTurns: nil}
	inner := &fixedModel{msg: schema.AssistantMessage("x", nil)}
	wrapped := wrapNestedAuditModel(inner, bridge)
	delID := uuid.Must(uuid.NewV7()).String()
	ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
		WorkspaceID: uuid.Must(uuid.NewV7()).String(), RunID: uuid.Must(uuid.NewV7()).String(),
		ParentDelegationID: &delID, CallerAgentID: "b",
	})
	_, err := wrapped.Generate(ctx, nil)
	if err == nil || !containsAll(err.Error(), "model turn recorder") {
		t.Fatalf("want fail-closed missing recorder, got %v", err)
	}
}

// failAccumulateAudit injects AccumulateModelTokens failure after terminal Record.
type failAccumulateAudit struct {
	err error
}

func (f *failAccumulateAudit) CreateDelegationAndStep(context.Context, agentdelegation.CreateDelegationInput) (agentdelegation.Delegation, bool, error) {
	return agentdelegation.Delegation{}, false, nil
}
func (f *failAccumulateAudit) FinalizeDelegation(context.Context, agentdelegation.FinalizeDelegationInput) (agentdelegation.Delegation, error) {
	return agentdelegation.Delegation{}, nil
}
func (f *failAccumulateAudit) SetChildRunID(context.Context, string, string, string) error {
	return nil
}
func (f *failAccumulateAudit) RecordDispatchAttempt(context.Context, string, string) error {
	return nil
}
func (f *failAccumulateAudit) AccumulateModelTokens(context.Context, string, string, agentdelegation.TokenUsage) error {
	return f.err
}

// TestNestedAuditModel_AccumulateTokensFail_DoesNotLeaveRUNNING: terminal MODEL
// evidence is recorded before token aggregation; AccumulateModelTokens failure
// must not leave a RUNNING orphan step.
func TestNestedAuditModel_AccumulateTokensFail_DoesNotLeaveRUNNING(t *testing.T) {
	t.Parallel()
	store := &captureStepStoreWithTransitions{}
	turns := &localCaptureModelTurn{records: &[]chatruntime.ModelTurnRecordInput{}}
	bridge := &Bridge{
		steps: store, modelTurns: turns,
		delegation: &DelegationDeps{Audit: &failAccumulateAudit{err: errors.New("token store unavailable")}},
	}
	// Known usage forces AccumulateModelTokens path.
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: "child with usage",
		ResponseMeta: &schema.ResponseMeta{
			Usage: &schema.TokenUsage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8},
		},
	}
	wrapped := wrapNestedAuditModel(&fixedModel{msg: msg}, bridge)
	delID := uuid.Must(uuid.NewV7()).String()
	ws, run := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
		WorkspaceID: ws, ParentRunID: run, RunID: run, CallerAgentID: "agent-b",
		ParentDelegationID: &delID,
	})
	_, err := wrapped.Generate(ctx, nil)
	if err == nil {
		t.Fatal("expected accumulate failure to surface")
	}
	if !strings.Contains(err.Error(), "accumulate nested model tokens") {
		t.Fatalf("want accumulate error, got %v", err)
	}
	// Append happened (RUNNING initially) but Record must have terminalized first.
	if len(store.appended) != 1 || store.appended[0].StepType != "MODEL" {
		t.Fatalf("appended=%+v", store.appended)
	}
	if len(*turns.records) != 1 {
		t.Fatalf("want terminal model-turn record before accumulate fail, got %d", len(*turns.records))
	}
	if (*turns.records)[0].NewStatus != "SUCCEEDED" {
		t.Fatalf("step must be terminal SUCCEEDED before token fail, got %q", (*turns.records)[0].NewStatus)
	}
	if (*turns.records)[0].ExpectedStatus != "RUNNING" {
		t.Fatalf("expected transition from RUNNING, got %q", (*turns.records)[0].ExpectedStatus)
	}
	// No failNestedModelStep path needed when Record already terminalized.
	for _, tr := range store.transitions {
		if tr.NewStatus == "RUNNING" {
			t.Fatalf("must not re-enter RUNNING: %+v", store.transitions)
		}
	}
}

func TestNestedAuditModel_DebugOffOmitsReasoningFromPayload(t *testing.T) {
	t.Parallel()
	store := &captureStepStoreWithTransitions{}
	var records []chatruntime.ModelTurnRecordInput
	turns := &localCaptureModelTurn{records: &records}
	bridge := &Bridge{steps: store, modelTurns: turns, agentAuditDebug: false}
	wrapped := wrapNestedAuditModel(&fixedModel{msg: &schema.Message{
		Role: schema.Assistant, Content: "ans", ReasoningContent: "secret-think",
	}}, bridge)
	delID := uuid.Must(uuid.NewV7()).String()
	ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
		WorkspaceID: uuid.Must(uuid.NewV7()).String(), RunID: uuid.Must(uuid.NewV7()).String(),
		ParentRunID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: "b",
		ParentDelegationID: &delID,
	})
	_, err := wrapped.Generate(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records=%d", len(records))
	}
	if strings.Contains(string(records[0].Content), "secret-think") {
		t.Fatalf("reasoning leaked into payload when debug=false: %s", records[0].Content)
	}
	if records[0].Reasoning != "" {
		t.Fatalf("Reasoning field set when debug=false: %q", records[0].Reasoning)
	}
}

func TestMergeStreamMessages_FragmentedToolCalls(t *testing.T) {
	t.Parallel()
	parts := []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "lookup", Arguments: `{"sk`},
		}}},
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "c1", Type: "function", Function: schema.FunctionCall{Arguments: `u":"1"}`},
		}}},
	}
	merged := mergeStreamMessages(parts)
	if len(merged.ToolCalls) != 1 {
		t.Fatalf("toolCalls=%d want 1: %+v", len(merged.ToolCalls), merged.ToolCalls)
	}
	if merged.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("name=%q", merged.ToolCalls[0].Function.Name)
	}
	if merged.ToolCalls[0].Function.Arguments != `{"sku":"1"}` {
		t.Fatalf("args=%q", merged.ToolCalls[0].Function.Arguments)
	}
}

func TestNestedAuditModel_TASKDoesNotSetCrossRunParentStep(t *testing.T) {
	t.Parallel()
	store := &captureStepStoreWithTransitions{}
	turns := &localCaptureModelTurn{records: &[]chatruntime.ModelTurnRecordInput{}}
	bridge := &Bridge{steps: store, modelTurns: turns}
	wrapped := wrapNestedAuditModel(&fixedModel{msg: schema.AssistantMessage("ok", nil)}, bridge)
	delID := uuid.Must(uuid.NewV7()).String()
	parentStep := uuid.Must(uuid.NewV7()).String()
	parentRun, childRun := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	// TASK: ParentStepID set on RC would be wrong for FK — sameRunParentStep must strip it.
	// Production AuditedAgentTool leaves ParentStepID nil for TASK; defensive check here.
	ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
		WorkspaceID: uuid.Must(uuid.NewV7()).String(),
		ParentRunID: parentRun, RunID: childRun, CallerAgentID: "b",
		ParentDelegationID: &delID, ParentStepID: &parentStep,
	})
	_, err := wrapped.Generate(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	// sameRunParentStep is false when RunID != ParentRunID → ParentStepID not written.
	if store.appended[0].ParentStepID != "" {
		t.Fatalf("cross-run parent_step_id set: %q", store.appended[0].ParentStepID)
	}
	if store.appended[0].RunID != childRun {
		t.Fatalf("run_id=%s want child", store.appended[0].RunID)
	}
}

type fixedModel struct {
	msg *schema.Message
	err error
}

func (m *fixedModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return m.msg, m.err
}
func (m *fixedModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if m.err != nil {
		return nil, m.err
	}
	return schema.StreamReaderFromArray([]*schema.Message{m.msg}), nil
}

func containsAll(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || (len(s) > 0 && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()))
}
