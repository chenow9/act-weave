package chatruntimebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/execution"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// nestedAuditModel records MODEL steps for child agents under AgentTool while
// leaving EmitInternalEvents=false so parent FinalAssistantText is not polluted.
// Audit is fail-closed: permanent MODEL_TURN evidence is required when modelTurns is wired.
type nestedAuditModel struct {
	inner  model.BaseChatModel
	bridge *Bridge
}

func wrapNestedAuditModel(inner model.BaseChatModel, b *Bridge) model.BaseChatModel {
	if inner == nil || b == nil {
		return inner
	}
	return &nestedAuditModel{inner: inner, bridge: b}
}

func (m *nestedAuditModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	out, err := m.inner.Generate(ctx, input, opts...)
	if err != nil {
		if recErr := m.recordFailure(ctx, err); recErr != nil {
			return out, fmt.Errorf("%w (nested model audit: %v)", err, recErr)
		}
		return out, err
	}
	if recErr := m.recordSuccess(ctx, out); recErr != nil {
		return out, fmt.Errorf("nested model audit: %w", recErr)
	}
	return out, nil
}

func (m *nestedAuditModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, err := m.inner.Stream(ctx, input, opts...)
	if err != nil {
		if recErr := m.recordFailure(ctx, err); recErr != nil {
			return nil, fmt.Errorf("%w (nested model audit: %v)", err, recErr)
		}
		return nil, err
	}
	if sr == nil {
		return nil, nil
	}
	var parts []*schema.Message
	var recvErr error
	for {
		msg, err := sr.Recv()
		if err != nil {
			if err != io.EOF {
				recvErr = err
			}
			break
		}
		parts = append(parts, msg)
	}
	sr.Close()
	if recvErr != nil {
		if recErr := m.recordFailure(ctx, recvErr); recErr != nil {
			return nil, fmt.Errorf("%w (nested model audit: %v)", recvErr, recErr)
		}
		return nil, recvErr
	}
	merged := mergeStreamMessages(parts)
	if recErr := m.recordSuccess(ctx, merged); recErr != nil {
		return nil, fmt.Errorf("nested model audit: %w", recErr)
	}
	return schema.StreamReaderFromArray([]*schema.Message{merged}), nil
}

func (m *nestedAuditModel) recordSuccess(ctx context.Context, msg *schema.Message) error {
	return m.record(ctx, msg, "SUCCEEDED", "", "")
}

func (m *nestedAuditModel) recordFailure(ctx context.Context, cause error) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	return m.record(ctx, nil, "FAILED", "NESTED_MODEL_FAILED", msg)
}

func (m *nestedAuditModel) record(ctx context.Context, msg *schema.Message, status, errCode, errMsg string) error {
	if m.bridge == nil || m.bridge.steps == nil {
		return nil
	}
	rc, ok := agentdelegation.RunContextFrom(ctx)
	if !ok || rc == nil || rc.ParentDelegationID == nil {
		// Only record when nested under a delegation frame.
		return nil
	}
	// Fail closed: permanent model-turn evidence required for nested audit path.
	if m.bridge.modelTurns == nil {
		return fmt.Errorf("model turn recorder required for nested agent MODEL audit")
	}
	stepID, err := newRuntimeID()
	if err != nil {
		return err
	}
	content, reasoning := "", ""
	usage := agentdelegation.TokenUsage{}
	hasToolCalls := false
	if msg != nil {
		content = strings.TrimSpace(msg.Content)
		if msg.ReasoningContent != "" {
			reasoning = strings.TrimSpace(msg.ReasoningContent)
		}
		if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
			u := msg.ResponseMeta.Usage
			usage = agentdelegation.TokenUsage{
				PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens,
				TotalTokens: u.TotalTokens, Known: true,
			}
		}
		hasToolCalls = len(msg.ToolCalls) > 0
	}
	inputSummary, _ := json.Marshal(map[string]any{
		"source": "chatruntimebridge.nested", "hasReasoning": reasoning != "",
		"contentLength": len(content), "hasToolCalls": hasToolCalls,
		"tokensKnown": usage.Known,
	})
	runID := firstNonEmpty(rc.RunID, rc.ParentRunID)
	modelStep := execution.AppendAgentRunStepInput{
		ID: stepID, WorkspaceID: rc.WorkspaceID, RunID: runID,
		StepType: "MODEL", InputSummary: inputSummary, AgentID: rc.CallerAgentID,
		DelegationID: *rc.ParentDelegationID,
	}
	if sameRunParentStep(rc) {
		modelStep.ParentStepID = *rc.ParentStepID
	}
	if _, err := m.bridge.steps.AppendAgentRunStep(ctx, modelStep); err != nil {
		return fmt.Errorf("append nested MODEL step: %w", err)
	}

	// Match root recordModelTurn: reasoning only in permanent payload when debug on.
	reasoningForAudit := ""
	if m.bridge.agentAuditDebug {
		reasoningForAudit = reasoning
	}
	payloadMap := map[string]any{
		"source": "chatruntimebridge.nested", "status": status,
		"content": content, "errorCode": errCode, "errorMessage": truncateStr(errMsg, 500),
		"hasToolCalls": hasToolCalls,
	}
	if reasoningForAudit != "" {
		payloadMap["reasoning"] = reasoningForAudit
	}
	if usage.Known {
		payloadMap["usage"] = map[string]any{
			"promptTokens": usage.PromptTokens, "completionTokens": usage.CompletionTokens,
			"totalTokens": usage.TotalTokens,
		}
	}
	// Terminal evidence first, then token aggregation. Never leave a RUNNING MODEL
	// step if AccumulateModelTokens fails after append.
	payload, err := json.Marshal(payloadMap)
	if err != nil {
		_ = m.failNestedModelStep(ctx, rc.WorkspaceID, stepID, "NESTED_MODEL_PAYLOAD", err.Error())
		return err
	}
	createdByType, createdByID := "SYSTEM", firstNonEmpty(rc.CallerAgentID, "nested")
	newStatus := status
	if newStatus != "SUCCEEDED" && newStatus != "FAILED" {
		newStatus = "FAILED"
	}
	if _, err := m.bridge.modelTurns.Record(ctx, chatruntime.ModelTurnRecordInput{
		WorkspaceID: rc.WorkspaceID, StepID: stepID,
		Content: payload, CreatedByType: createdByType, CreatedByID: createdByID,
		ExpectedStatus: "RUNNING", NewStatus: newStatus,
		ErrorCode: errCode, Reasoning: reasoningForAudit,
	}); err != nil {
		return fmt.Errorf("record nested MODEL turn evidence: %w", err)
	}
	// Aggregate after terminal write so token-path failures cannot orphan RUNNING steps.
	if usage.Known && m.bridge.delegation != nil && m.bridge.delegation.Audit != nil {
		if aerr := m.bridge.delegation.Audit.AccumulateModelTokens(ctx, rc.WorkspaceID, *rc.ParentDelegationID, usage); aerr != nil {
			return fmt.Errorf("accumulate nested model tokens: %w", aerr)
		}
	}
	return nil
}

// failNestedModelStep best-effort transitions a just-appended MODEL step off RUNNING
// when a later audit step fails before permanent Record.
func (m *nestedAuditModel) failNestedModelStep(ctx context.Context, workspaceID, stepID, errCode, errMsg string) error {
	if m.bridge == nil || m.bridge.steps == nil || strings.TrimSpace(stepID) == "" {
		return nil
	}
	out, _ := json.Marshal(map[string]any{
		"source": "chatruntimebridge.nested", "status": "FAILED",
		"errorCode": errCode, "errorMessage": truncateStr(errMsg, 500),
	})
	_, err := m.bridge.steps.TransitionAgentRunStep(ctx, workspaceID, stepID, execution.StepTransition{
		ExpectedStatus: "RUNNING",
		NewStatus:      "FAILED",
		OutputSummary:  out,
		ErrorCode:      firstNonEmpty(errCode, "NESTED_MODEL_FAILED"),
	})
	return err
}

func mergeStreamMessages(parts []*schema.Message) *schema.Message {
	if len(parts) == 0 {
		return schema.AssistantMessage("", nil)
	}
	// Content/reasoning: prefer Eino ConcatMessages when it succeeds.
	var content, reasoning string
	var usage *schema.TokenUsage
	if merged, err := schema.ConcatMessages(parts); err == nil && merged != nil {
		content = merged.Content
		reasoning = merged.ReasoningContent
		if merged.ResponseMeta != nil {
			usage = merged.ResponseMeta.Usage
		}
	} else {
		var b, rb strings.Builder
		// Eino usage semantics on stream: take max of cumulative partials, never sum.
		var maxPrompt, maxCompletion, maxTotal, maxReason int
		var anyUsage bool
		for _, p := range parts {
			if p == nil {
				continue
			}
			b.WriteString(p.Content)
			rb.WriteString(p.ReasoningContent)
			if p.ResponseMeta != nil && p.ResponseMeta.Usage != nil {
				anyUsage = true
				u := p.ResponseMeta.Usage
				if u.PromptTokens > maxPrompt {
					maxPrompt = u.PromptTokens
				}
				if u.CompletionTokens > maxCompletion {
					maxCompletion = u.CompletionTokens
				}
				if u.TotalTokens > maxTotal {
					maxTotal = u.TotalTokens
				}
				if u.CompletionTokensDetails.ReasoningTokens > maxReason {
					maxReason = u.CompletionTokensDetails.ReasoningTokens
				}
			}
		}
		content, reasoning = b.String(), rb.String()
		if anyUsage {
			usage = &schema.TokenUsage{
				PromptTokens: maxPrompt, CompletionTokens: maxCompletion, TotalTokens: maxTotal,
				CompletionTokensDetails: schema.CompletionTokensDetails{ReasoningTokens: maxReason},
			}
		}
	}
	// Tool-call fragments must be merged by ID (ConcatMessages leaves partials separate).
	byID := map[string]*schema.ToolCall{}
	var order []string
	var noID []schema.ToolCall
	for _, p := range parts {
		if p == nil {
			continue
		}
		for _, tc := range p.ToolCalls {
			if tc.ID == "" {
				noID = append(noID, tc)
				continue
			}
			if existing, ok := byID[tc.ID]; ok {
				if tc.Function.Name != "" {
					existing.Function.Name = tc.Function.Name
				}
				existing.Function.Arguments += tc.Function.Arguments
				if tc.Type != "" {
					existing.Type = tc.Type
				}
			} else {
				cp := tc
				byID[tc.ID] = &cp
				order = append(order, tc.ID)
			}
		}
	}
	toolCalls := make([]schema.ToolCall, 0, len(order)+len(noID))
	for _, id := range order {
		toolCalls = append(toolCalls, *byID[id])
	}
	toolCalls = append(toolCalls, noID...)
	msg := schema.AssistantMessage(content, toolCalls)
	if reasoning != "" {
		msg.ReasoningContent = reasoning
	}
	if usage != nil {
		msg.ResponseMeta = &schema.ResponseMeta{Usage: usage}
	}
	return msg
}
