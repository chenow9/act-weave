package execution

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

// WorkflowStepProtocolMapper maps durable WorkflowExecution/ExecutionStep
// facts. It never maps compiled plans, authorization snapshots, step inputs, or
// raw StoredObject payloads into the public result.
type WorkflowStepProtocolMapper struct {
	validator *protocolevent.PayloadValidator
}

func NewWorkflowStepProtocolMapper() *WorkflowStepProtocolMapper {
	return &WorkflowStepProtocolMapper{validator: protocolevent.MustDefaultPayloadValidator()}
}

func (mapper *WorkflowStepProtocolMapper) MapStarted(
	execution WorkflowExecution,
	step ExecutionStep,
	tools []ToolInvocation,
) (protocolevent.WorkflowStepItem, error) {
	if !validProtocolWorkflowStep(execution, step, "QUEUED", "RUNNING", "WAITING_CONFIRMATION") {
		return protocolevent.WorkflowStepItem{}, ErrRunInvalid
	}
	status := protocolevent.ItemStatusInProgress
	if strings.EqualFold(step.Status, "WAITING_CONFIRMATION") {
		status = protocolevent.ItemStatusWaiting
	}
	item, err := mapWorkflowStepIdentity(execution, step, tools, status)
	if err != nil || mapper.validateItem(item, protocolevent.EventItemStarted) != nil {
		return protocolevent.WorkflowStepItem{}, ErrRunInvalid
	}
	return item, nil
}

func (mapper *WorkflowStepProtocolMapper) MapProgress(
	execution WorkflowExecution,
	step ExecutionStep,
	current float64,
	total *float64,
	unit, message string,
) (protocolevent.ProgressDelta, error) {
	delta := protocolevent.ProgressDelta{
		Type: protocolevent.DeltaTypeProgress, Current: current, Total: total,
		Unit: strings.TrimSpace(unit), Message: strings.TrimSpace(message),
	}
	if !validProtocolWorkflowStep(execution, step, "RUNNING", "WAITING_CONFIRMATION") ||
		mapper.validateDelta(step.ID, delta) != nil {
		return protocolevent.ProgressDelta{}, ErrRunInvalid
	}
	return delta, nil
}

func (mapper *WorkflowStepProtocolMapper) MapWaiting(
	execution WorkflowExecution,
	step ExecutionStep,
) (protocolevent.ProgressDelta, error) {
	if !strings.EqualFold(strings.TrimSpace(step.Status), "WAITING_CONFIRMATION") {
		return protocolevent.ProgressDelta{}, ErrRunInvalid
	}
	return mapper.MapProgress(execution, step, 0, nil, "state", "waiting_confirmation")
}

func (mapper *WorkflowStepProtocolMapper) MapCompleted(
	execution WorkflowExecution,
	step ExecutionStep,
	tools []ToolInvocation,
) (protocolevent.WorkflowStepItem, error) {
	if !validProtocolWorkflowStep(execution, step, "SUCCEEDED", "FAILED", "SKIPPED", "CANCELLED") ||
		step.FinishedAt == nil {
		return protocolevent.WorkflowStepItem{}, ErrRunInvalid
	}
	status := mapWorkflowStepStatus(step.Status)
	item, err := mapWorkflowStepIdentity(execution, step, tools, status)
	if err != nil {
		return protocolevent.WorkflowStepItem{}, err
	}
	switch strings.ToUpper(strings.TrimSpace(step.Status)) {
	case "SUCCEEDED":
		result, decoded, resultErr := canonicalPublicToolObject(step.OutputSummary)
		if resultErr != nil || containsInjectedHeader(decoded) || containsInternalWorkflowPlan(decoded) {
			return protocolevent.WorkflowStepItem{}, ErrRunInvalid
		}
		item.Result = result
	case "FAILED":
		if !stableProtocolErrorCode.MatchString(strings.TrimSpace(step.ErrorCode)) {
			return protocolevent.WorkflowStepItem{}, ErrRunInvalid
		}
		item.Result, err = json.Marshal(map[string]any{
			"error": map[string]any{
				"code": step.ErrorCode, "message": "Workflow step failed", "retryable": false,
			},
		})
	case "SKIPPED":
		item.Result = json.RawMessage(`{"outcome":"skipped"}`)
	case "CANCELLED":
		item.Result = json.RawMessage(`{"outcome":"cancelled"}`)
	}
	if err != nil || mapper.validateItem(item, protocolevent.EventItemCompleted) != nil {
		return protocolevent.WorkflowStepItem{}, ErrRunInvalid
	}
	return item, nil
}

func (mapper *WorkflowStepProtocolMapper) validateItem(
	item protocolevent.WorkflowStepItem,
	eventType string,
) error {
	if mapper == nil || mapper.validator == nil {
		return ErrRunInvalid
	}
	data, err := json.Marshal(protocolevent.ItemSnapshotData{Item: item})
	if err != nil {
		return err
	}
	return mapper.validator.ValidateEventData(eventType, data)
}

func (mapper *WorkflowStepProtocolMapper) validateDelta(
	itemID string,
	delta protocolevent.Delta,
) error {
	if mapper == nil || mapper.validator == nil {
		return ErrRunInvalid
	}
	data, err := json.Marshal(protocolevent.ItemDeltaData{ItemID: itemID, Delta: delta})
	if err != nil {
		return err
	}
	return mapper.validator.ValidateEventData(protocolevent.EventItemDelta, data)
}

type ProjectWorkflowStepStartedInput struct {
	Context   ProtocolToolCallContext
	Execution WorkflowExecution
	Step      ExecutionStep
	Ordinal   int
}

type ProjectWorkflowStepProgressInput struct {
	Context    ProtocolToolCallContext
	Execution  WorkflowExecution
	Step       ExecutionStep
	Current    float64
	Total      *float64
	Unit       string
	Message    string
	OccurredAt time.Time
}

type ProjectWorkflowStepWaitingInput struct {
	Context    ProtocolToolCallContext
	Execution  WorkflowExecution
	Step       ExecutionStep
	OccurredAt time.Time
}

type CompleteProtocolWorkflowStepInput struct {
	Context     ProtocolToolCallContext
	Execution   WorkflowExecution
	Step        ExecutionStep
	CompletedAt time.Time
}

type ProtocolWorkflowStepProjectionResult struct {
	Projection  protocolevent.RunItemProjection
	Events      []protocolevent.ProtocolEvent
	NotifyError error
}

type ProtocolWorkflowStepProjector struct {
	unit   *protocolevent.ProtocolUnitOfWork
	tools  *ToolInvocationRepository
	mapper *WorkflowStepProtocolMapper
}

func NewProtocolWorkflowStepProjector(
	unit *protocolevent.ProtocolUnitOfWork,
	tools *ToolInvocationRepository,
	mapper *WorkflowStepProtocolMapper,
) (*ProtocolWorkflowStepProjector, error) {
	if unit == nil || tools == nil || mapper == nil {
		return nil, ErrRunInvalid
	}
	return &ProtocolWorkflowStepProjector{unit: unit, tools: tools, mapper: mapper}, nil
}

func (projector *ProtocolWorkflowStepProjector) ProjectStarted(
	ctx context.Context,
	input ProjectWorkflowStepStartedInput,
) (ProtocolWorkflowStepProjectionResult, error) {
	if !validProtocolWorkflowProjector(projector) || input.Ordinal < 1 ||
		validateProtocolWorkflowContext(input.Context, input.Execution, input.Step) != nil {
		return ProtocolWorkflowStepProjectionResult{}, ErrRunInvalid
	}
	tools, err := projector.tools.ListForExecutionStep(
		ctx, input.Step.WorkspaceID, input.Step.ExecutionID, input.Step.ID,
	)
	if err != nil {
		return ProtocolWorkflowStepProjectionResult{}, err
	}
	item, err := projector.mapper.MapStarted(input.Execution, input.Step, tools)
	if err != nil {
		return ProtocolWorkflowStepProjectionResult{}, err
	}
	event, err := buildWorkflowStepSnapshotEvent(
		input.Context, item, protocolevent.EventItemStarted, input.Step.StartedAt,
	)
	if err != nil {
		return ProtocolWorkflowStepProjectionResult{}, err
	}
	var projection protocolevent.RunItemProjection
	result, err := projector.unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
		if _, err := transaction.EnsureRunEventStream(ctx, input.Context.EventStreamID, input.Context.Scope); err != nil {
			return err
		}
		projection, err = transaction.CreateRunItem(ctx, protocolevent.CreateRunItemInput{
			WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
			RunID: input.Context.Scope.RunID, Ordinal: input.Ordinal,
			SourceType: protocolevent.SourceWorkflowStep, SourceID: input.Step.ID,
			Item: item, StartedAt: input.Step.StartedAt,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		return ProtocolWorkflowStepProjectionResult{}, err
	}
	return workflowProjectionResult(projection, result), nil
}

func (projector *ProtocolWorkflowStepProjector) ProjectProgress(
	ctx context.Context,
	input ProjectWorkflowStepProgressInput,
) (ProtocolWorkflowStepProjectionResult, error) {
	if !validProtocolWorkflowProjector(projector) || input.OccurredAt.IsZero() ||
		validateProtocolWorkflowContext(input.Context, input.Execution, input.Step) != nil {
		return ProtocolWorkflowStepProjectionResult{}, ErrRunInvalid
	}
	delta, err := projector.mapper.MapProgress(
		input.Execution, input.Step, input.Current, input.Total, input.Unit, input.Message,
	)
	if err != nil {
		return ProtocolWorkflowStepProjectionResult{}, err
	}
	return projector.projectDelta(ctx, input.Context, input.Step.ID, delta, input.OccurredAt)
}

func (projector *ProtocolWorkflowStepProjector) ProjectWaiting(
	ctx context.Context,
	input ProjectWorkflowStepWaitingInput,
) (ProtocolWorkflowStepProjectionResult, error) {
	if !validProtocolWorkflowProjector(projector) || input.OccurredAt.IsZero() ||
		validateProtocolWorkflowContext(input.Context, input.Execution, input.Step) != nil {
		return ProtocolWorkflowStepProjectionResult{}, ErrRunInvalid
	}
	delta, err := projector.mapper.MapWaiting(input.Execution, input.Step)
	if err != nil {
		return ProtocolWorkflowStepProjectionResult{}, err
	}
	return projector.projectDelta(ctx, input.Context, input.Step.ID, delta, input.OccurredAt)
}

func (projector *ProtocolWorkflowStepProjector) Complete(
	ctx context.Context,
	input CompleteProtocolWorkflowStepInput,
) (ProtocolWorkflowStepProjectionResult, error) {
	if !validProtocolWorkflowProjector(projector) || input.CompletedAt.IsZero() ||
		validateProtocolWorkflowContext(input.Context, input.Execution, input.Step) != nil {
		return ProtocolWorkflowStepProjectionResult{}, ErrRunInvalid
	}
	tools, err := projector.tools.ListForExecutionStep(
		ctx, input.Step.WorkspaceID, input.Step.ExecutionID, input.Step.ID,
	)
	if err != nil {
		return ProtocolWorkflowStepProjectionResult{}, err
	}
	item, err := projector.mapper.MapCompleted(input.Execution, input.Step, tools)
	if err != nil {
		return ProtocolWorkflowStepProjectionResult{}, err
	}
	event, err := buildWorkflowStepSnapshotEvent(
		input.Context, item, protocolevent.EventItemCompleted, input.CompletedAt,
	)
	if err != nil {
		return ProtocolWorkflowStepProjectionResult{}, err
	}
	var projection protocolevent.RunItemProjection
	result, err := projector.unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
		projection, err = transaction.CompleteRunItem(ctx, protocolevent.CompleteRunItemInput{
			WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
			RunID: input.Context.Scope.RunID, Item: item, CompletedAt: input.CompletedAt,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		return ProtocolWorkflowStepProjectionResult{}, err
	}
	return workflowProjectionResult(projection, result), nil
}

func (projector *ProtocolWorkflowStepProjector) projectDelta(
	ctx context.Context,
	protocolContext ProtocolToolCallContext,
	itemID string,
	delta protocolevent.Delta,
	occurredAt time.Time,
) (ProtocolWorkflowStepProjectionResult, error) {
	event, err := buildWorkflowStepDeltaEvent(protocolContext, itemID, delta, occurredAt)
	if err != nil {
		return ProtocolWorkflowStepProjectionResult{}, err
	}
	var projection protocolevent.RunItemProjection
	result, err := projector.unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
		projection, err = transaction.ApplyRunItemDelta(ctx, protocolevent.ApplyItemDeltaInput{
			WorkspaceID: protocolContext.Scope.WorkspaceID, AgentID: protocolContext.Scope.AgentID,
			RunID: protocolContext.Scope.RunID, ItemID: itemID, Delta: delta,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		return ProtocolWorkflowStepProjectionResult{}, err
	}
	return workflowProjectionResult(projection, result), nil
}

func buildWorkflowStepSnapshotEvent(
	protocolContext ProtocolToolCallContext,
	item protocolevent.WorkflowStepItem,
	eventType string,
	occurredAt time.Time,
) (protocolevent.NewProtocolEvent, error) {
	eventID, err := uuid.NewV7()
	if err != nil {
		return protocolevent.NewProtocolEvent{}, err
	}
	return protocolevent.BuildProtocolEvent(toolEventBase(
		protocolContext, eventID.String(), item.ID, eventType, occurredAt,
	), protocolevent.ItemSnapshotData{Item: item})
}

func buildWorkflowStepDeltaEvent(
	protocolContext ProtocolToolCallContext,
	itemID string,
	delta protocolevent.Delta,
	occurredAt time.Time,
) (protocolevent.NewProtocolEvent, error) {
	eventID, err := uuid.NewV7()
	if err != nil {
		return protocolevent.NewProtocolEvent{}, err
	}
	return protocolevent.BuildProtocolEvent(toolEventBase(
		protocolContext, eventID.String(), itemID, protocolevent.EventItemDelta, occurredAt,
	), protocolevent.ItemDeltaData{ItemID: itemID, Delta: delta})
}

func mapWorkflowStepIdentity(
	execution WorkflowExecution,
	step ExecutionStep,
	tools []ToolInvocation,
	status protocolevent.ItemStatus,
) (protocolevent.WorkflowStepItem, error) {
	toolIDs := make([]string, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if tool.WorkspaceID != step.WorkspaceID || tool.WorkflowExecutionID != step.ExecutionID ||
			tool.ExecutionStepID != step.ID || !invocationValidUUID(tool.ID) {
			return protocolevent.WorkflowStepItem{}, ErrRunInvalid
		}
		if _, duplicate := seen[tool.ID]; duplicate {
			continue
		}
		seen[tool.ID] = struct{}{}
		toolIDs = append(toolIDs, tool.ID)
	}
	sort.Strings(toolIDs)
	return protocolevent.WorkflowStepItem{
		ID: step.ID, Type: protocolevent.ItemTypeWorkflowStep, Status: status,
		NodeID: strings.TrimSpace(step.NodeID), NodeType: strings.TrimSpace(step.NodeType),
		WorkflowExecutionID: execution.ID, StepSequence: step.SequenceNo,
		ToolCallItemIDs: toolIDs,
	}, nil
}

func mapWorkflowStepStatus(status string) protocolevent.ItemStatus {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCEEDED", "SKIPPED":
		return protocolevent.ItemStatusCompleted
	case "FAILED":
		return protocolevent.ItemStatusFailed
	case "CANCELLED":
		return protocolevent.ItemStatusCancelled
	default:
		return protocolevent.ItemStatusUnknown
	}
}

func validProtocolWorkflowStep(
	execution WorkflowExecution,
	step ExecutionStep,
	statuses ...string,
) bool {
	if !invocationValidUUID(execution.ID) || !invocationValidUUID(execution.WorkspaceID) ||
		!invocationValidUUID(execution.AgentRunID) || !invocationValidUUID(step.ID) ||
		step.WorkspaceID != execution.WorkspaceID || step.ExecutionID != execution.ID ||
		strings.TrimSpace(execution.TraceID) == "" || strings.TrimSpace(step.NodeID) == "" ||
		strings.TrimSpace(step.NodeType) == "" || step.SequenceNo < 1 || step.StartedAt.IsZero() {
		return false
	}
	actual := strings.ToUpper(strings.TrimSpace(step.Status))
	for _, status := range statuses {
		if actual == status {
			return true
		}
	}
	return false
}

func validateProtocolWorkflowContext(
	protocolContext ProtocolToolCallContext,
	execution WorkflowExecution,
	step ExecutionStep,
) error {
	if !invocationValidUUID(protocolContext.Scope.WorkspaceID) ||
		!invocationValidUUID(protocolContext.Scope.AgentID) ||
		!invocationValidUUID(protocolContext.Scope.ConversationID) ||
		!invocationValidUUID(protocolContext.Scope.RunID) ||
		!invocationValidUUID(strings.TrimSpace(protocolContext.EventStreamID)) ||
		strings.TrimSpace(protocolContext.TraceID) == "" ||
		execution.WorkspaceID != protocolContext.Scope.WorkspaceID ||
		execution.AgentRunID != protocolContext.Scope.RunID ||
		execution.TraceID != protocolContext.TraceID || step.ExecutionID != execution.ID ||
		step.WorkspaceID != execution.WorkspaceID {
		return ErrRunInvalid
	}
	return nil
}

func containsInternalWorkflowPlan(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(key))
			switch normalized {
			case "plan", "compiledplan", "executionplan", "plannodes", "dependencies",
				"authorizationsnapshot", "modelsnapshot", "capabilitysnapshot", "contextpolicysnapshot":
				return true
			}
			if containsInternalWorkflowPlan(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsInternalWorkflowPlan(nested) {
				return true
			}
		}
	}
	return false
}

func validProtocolWorkflowProjector(projector *ProtocolWorkflowStepProjector) bool {
	return projector != nil && projector.unit != nil && projector.tools != nil && projector.mapper != nil
}

func workflowProjectionResult(
	projection protocolevent.RunItemProjection,
	result protocolevent.UnitOfWorkResult,
) ProtocolWorkflowStepProjectionResult {
	return ProtocolWorkflowStepProjectionResult{
		Projection: projection, Events: result.Events, NotifyError: result.NotifyError,
	}
}
