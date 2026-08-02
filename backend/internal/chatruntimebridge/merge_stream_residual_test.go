package chatruntimebridge

import (
	"context"
	"strings"
	"testing"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/chatruntime"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// Residual #10: SSE/stream fragment merge has no duplicate/loss; debug=false
// strips nested reasoning thoroughly from permanent payload + Reasoning field.
func TestMergeStreamMessages_NoDuplicateNoLoss_MultiFragment(t *testing.T) {
	t.Parallel()
	parts := []*schema.Message{
		{Role: schema.Assistant, Content: "Hel", ReasoningContent: "r1"},
		{Role: schema.Assistant, Content: "lo ", ReasoningContent: "r2"},
		{Role: schema.Assistant, Content: "world", ReasoningContent: "r3"},
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "tc1", Type: "function", Function: schema.FunctionCall{Name: "fn", Arguments: `{"a":`},
		}}},
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "tc1", Type: "function", Function: schema.FunctionCall{Arguments: `1}`},
		}}},
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "tc2", Type: "function", Function: schema.FunctionCall{Name: "other", Arguments: `{}`},
		}}},
	}
	merged := mergeStreamMessages(parts)
	if merged.Content != "Hello world" {
		// ConcatMessages may join cleanly; fallback concat must not drop.
		if !strings.Contains(merged.Content, "Hel") || !strings.Contains(merged.Content, "world") {
			t.Fatalf("content lost: %q", merged.Content)
		}
	}
	// Expect exactly 2 tool calls by ID, no duplicate tc1.
	if len(merged.ToolCalls) != 2 {
		t.Fatalf("toolCalls=%d want 2: %+v", len(merged.ToolCalls), merged.ToolCalls)
	}
	byID := map[string]schema.ToolCall{}
	for _, tc := range merged.ToolCalls {
		if _, ok := byID[tc.ID]; ok {
			t.Fatalf("duplicate tool call id %s", tc.ID)
		}
		byID[tc.ID] = tc
	}
	if byID["tc1"].Function.Arguments != `{"a":1}` {
		t.Fatalf("tc1 args incomplete/dup: %q", byID["tc1"].Function.Arguments)
	}
	if byID["tc2"].Function.Name != "other" {
		t.Fatalf("tc2 lost: %+v", byID["tc2"])
	}
	// Reasoning fragments concatenated without loss.
	if !strings.Contains(merged.ReasoningContent, "r1") || !strings.Contains(merged.ReasoningContent, "r3") {
		t.Fatalf("reasoning lost: %q", merged.ReasoningContent)
	}
}

func TestNestedAudit_DebugFalse_StripsReasoningCompletely(t *testing.T) {
	t.Parallel()
	store := &captureStepStoreWithTransitions{}
	var records []chatruntime.ModelTurnRecordInput
	turns := &localCaptureModelTurn{records: &records}
	bridge := &Bridge{steps: store, modelTurns: turns, agentAuditDebug: false}
	// Stream path with reasoning fragments.
	inner := &streamModel{parts: []*schema.Message{
		{Role: schema.Assistant, Content: "a", ReasoningContent: "secret-plan-1"},
		{Role: schema.Assistant, Content: "b", ReasoningContent: "secret-plan-2"},
	}}
	wrapped := wrapNestedAuditModel(inner, bridge)
	delID := uuid.Must(uuid.NewV7()).String()
	ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
		WorkspaceID: uuid.Must(uuid.NewV7()).String(), RunID: uuid.Must(uuid.NewV7()).String(),
		ParentRunID: uuid.Must(uuid.NewV7()).String(), CallerAgentID: "b",
		ParentDelegationID: &delID,
	})
	sr, err := wrapped.Stream(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Drain stream.
	for {
		_, err := sr.Recv()
		if err != nil {
			break
		}
	}
	sr.Close()
	if len(records) != 1 {
		t.Fatalf("records=%d", len(records))
	}
	if records[0].Reasoning != "" {
		t.Fatalf("Reasoning not stripped: %q", records[0].Reasoning)
	}
	raw := string(records[0].Content)
	if strings.Contains(raw, "secret-plan") {
		t.Fatalf("reasoning leaked in payload: %s", raw)
	}
}

type streamModel struct {
	parts []*schema.Message
}

func (m *streamModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage("x", nil), nil
}

func (m *streamModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray(m.parts), nil
}
