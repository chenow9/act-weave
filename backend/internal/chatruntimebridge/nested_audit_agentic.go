package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/execution"
)

// nestedAuditAgenticModel records MODEL steps for child agents under
// NewTypedAgentTool while leaving the parent text stream untouched.
// Fail-closed: permanent MODEL_TURN evidence is required when modelTurns is wired.
type nestedAuditAgenticModel struct {
	inner  model.AgenticModel
	bridge *Bridge
}

func wrapNestedAuditAgenticModel(inner model.AgenticModel, b *Bridge) model.AgenticModel {
	if inner == nil || b == nil {
		return inner
	}
	return &nestedAuditAgenticModel{inner: inner, bridge: b}
}

func (m *nestedAuditAgenticModel) Generate(
	ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option,
) (*schema.AgenticMessage, error) {
	out, err := m.inner.Generate(ctx, input, opts...)
	if err != nil {
		if recErr := m.recordFailure(ctx, err); recErr != nil {
			return out, fmt.Errorf("%w (nested agentic model audit: %v)", err, recErr)
		}
		return out, err
	}
	if recErr := m.recordSuccess(ctx, out); recErr != nil {
		return out, fmt.Errorf("nested agentic model audit: %w", recErr)
	}
	return out, nil
}

func (m *nestedAuditAgenticModel) Stream(
	ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option,
) (*schema.StreamReader[*schema.AgenticMessage], error) {
	sr, err := m.inner.Stream(ctx, input, opts...)
	if err != nil {
		if recErr := m.recordFailure(ctx, err); recErr != nil {
			return nil, fmt.Errorf("%w (nested agentic model audit: %v)", err, recErr)
		}
		return nil, err
	}
	if sr == nil {
		return nil, nil
	}
	var parts []*schema.AgenticMessage
	var recvErr error
	for {
		msg, err := sr.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				recvErr = err
			}
			break
		}
		parts = append(parts, msg)
	}
	sr.Close()
	if recvErr != nil {
		if recErr := m.recordFailure(ctx, recvErr); recErr != nil {
			return nil, fmt.Errorf("%w (nested agentic model audit: %v)", recvErr, recErr)
		}
		return nil, recvErr
	}
	merged, err := schema.ConcatAgenticMessages(parts)
	if err != nil {
		if recErr := m.recordFailure(ctx, err); recErr != nil {
			return nil, fmt.Errorf("%w (nested agentic model audit: %v)", err, recErr)
		}
		return nil, err
	}
	if recErr := m.recordSuccess(ctx, merged); recErr != nil {
		return nil, fmt.Errorf("nested agentic model audit: %w", recErr)
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{merged}), nil
}

func (m *nestedAuditAgenticModel) recordSuccess(ctx context.Context, msg *schema.AgenticMessage) error {
	return m.record(ctx, msg, "SUCCEEDED", "", "")
}

func (m *nestedAuditAgenticModel) recordFailure(ctx context.Context, cause error) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	return m.record(ctx, nil, "FAILED", "NESTED_MODEL_FAILED", msg)
}

func (m *nestedAuditAgenticModel) record(
	ctx context.Context, msg *schema.AgenticMessage, status, errCode, errMsg string,
) error {
	if m.bridge == nil || m.bridge.steps == nil {
		return nil
	}
	rc, ok := agentdelegation.RunContextFrom(ctx)
	if !ok || rc == nil || rc.ParentDelegationID == nil {
		return nil
	}
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
		if text, terr := agenticmsg.ExtractAssistantText(msg); terr == nil {
			content = strings.TrimSpace(text)
		} else if !errors.Is(terr, agenticmsg.ErrNoAssistantText) {
			return terr
		}
		if r, rerr := agenticmsg.ExtractReasoningText(msg); rerr == nil {
			reasoning = strings.TrimSpace(r)
		}
		if msg.ResponseMeta != nil && msg.ResponseMeta.TokenUsage != nil {
			u := msg.ResponseMeta.TokenUsage
			usage = agentdelegation.TokenUsage{
				PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens,
				TotalTokens: u.TotalTokens, Known: true,
			}
		}
		for _, block := range msg.ContentBlocks {
			if block != nil && block.FunctionToolCall != nil {
				hasToolCalls = true
				break
			}
		}
	}
	inputSummary, _ := json.Marshal(map[string]any{
		"source": "chatruntimebridge.nested.agentic", "hasReasoning": reasoning != "",
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

	reasoningForAudit := ""
	if m.bridge.agentAuditDebug {
		reasoningForAudit = reasoning
	}
	payloadMap := map[string]any{
		"source": "chatruntimebridge.nested.agentic", "status": status,
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
	payload, err := json.Marshal(payloadMap)
	if err != nil {
		_ = m.failNestedAgenticModelStep(ctx, rc.WorkspaceID, stepID, "NESTED_MODEL_PAYLOAD", err.Error())
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
	if usage.Known && m.bridge.delegation != nil && m.bridge.delegation.Audit != nil {
		if aerr := m.bridge.delegation.Audit.AccumulateModelTokens(
			ctx, rc.WorkspaceID, *rc.ParentDelegationID, usage,
		); aerr != nil {
			return fmt.Errorf("accumulate nested model tokens: %w", aerr)
		}
	}
	return nil
}

func (m *nestedAuditAgenticModel) failNestedAgenticModelStep(
	ctx context.Context, workspaceID, stepID, errCode, errMsg string,
) error {
	if m.bridge == nil || m.bridge.steps == nil || strings.TrimSpace(stepID) == "" {
		return nil
	}
	out, _ := json.Marshal(map[string]any{
		"source": "chatruntimebridge.nested.agentic", "status": "FAILED",
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
