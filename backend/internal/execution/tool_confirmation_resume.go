package execution

import (
	"context"
	"encoding/json"
	"errors"

	"actweave/backend/internal/principal"
)

const (
	ToolResumeRequestSnapshotVersion  = "tool-resume-request.v1"
	ToolResumeResolvedSnapshotVersion = "tool-resume-resolved.v1"
)

type toolResumeRequestSnapshot struct {
	SchemaVersion         string                       `json:"schemaVersion"`
	InvocationID          string                       `json:"invocationId"`
	WorkspaceID           string                       `json:"workspaceId"`
	CapabilityID          string                       `json:"capabilityId"`
	ReleaseID             string                       `json:"releaseId"`
	ActorType             string                       `json:"actorType"`
	ActorID               string                       `json:"actorId"`
	TraceID               string                       `json:"traceId"`
	ExplicitConnectionID  string                       `json:"explicitConnectionId,omitempty"`
	PlanConnectionID      string                       `json:"planConnectionId,omitempty"`
	BindingConnectionID   string                       `json:"bindingConnectionId,omitempty"`
	ConnectionID          string                       `json:"connectionId"`
	PlanHash              string                       `json:"planHash,omitempty"`
	IdempotencyKey        string                       `json:"idempotencyKey,omitempty"`
	AgentRunID            string                       `json:"agentRunId,omitempty"`
	WorkflowExecutionID   string                       `json:"workflowExecutionId,omitempty"`
	ExecutionStepID       string                       `json:"executionStepId,omitempty"`
	PrincipalSnapshot     *principal.ExecutionSnapshot `json:"principalSnapshot,omitempty"`
	AuthorizationSnapshot json.RawMessage              `json:"authorizationSnapshot"`
}

type toolResumeResolvedSnapshot struct {
	SchemaVersion string             `json:"schemaVersion"`
	Resolved      ResolvedInvocation `json:"resolved"`
}

type toolResumeResultSnapshot struct {
	InvocationID string          `json:"invocationId"`
	TraceID      string          `json:"traceId"`
	Output       json.RawMessage `json:"output"`
	HTTPStatus   int             `json:"httpStatus"`
	ContentType  string          `json:"contentType,omitempty"`
	Attempts     int             `json:"attempts"`
	Cached       bool            `json:"cached"`
}

func BuildToolConfirmationResumeSnapshots(
	request InvokeRequest,
	resolved ResolvedInvocation,
) (json.RawMessage, json.RawMessage, error) {
	request = normalizeInvokeRequest(request)
	if !validInvokeRequest(request) || resolved.Snapshot.WorkspaceID != request.WorkspaceID ||
		resolved.Snapshot.CapabilityID != request.CapabilityID ||
		resolved.Snapshot.ReleaseID != request.ReleaseID ||
		resolved.Connection.WorkspaceID != request.WorkspaceID ||
		resolved.Connection.ProviderID != resolved.Snapshot.ProviderID {
		return nil, nil, ErrConfirmationResumeInvalid
	}
	requestSnapshot, err := json.Marshal(toolResumeRequestSnapshot{
		SchemaVersion: ToolResumeRequestSnapshotVersion,
		InvocationID:  request.InvocationID, WorkspaceID: request.WorkspaceID,
		CapabilityID: request.CapabilityID, ReleaseID: request.ReleaseID,
		ActorType: request.ActorType, ActorID: request.ActorID, TraceID: request.TraceID,
		ExplicitConnectionID: request.ExplicitConnectionID,
		PlanConnectionID:     request.PlanConnectionID, BindingConnectionID: request.BindingConnectionID,
		ConnectionID: resolved.Connection.ID, PlanHash: request.PlanHash,
		IdempotencyKey: request.IdempotencyKey,
		AgentRunID:     request.AgentRunID, WorkflowExecutionID: request.WorkflowExecutionID,
		ExecutionStepID: request.ExecutionStepID, PrincipalSnapshot: request.PrincipalSnapshot,
		AuthorizationSnapshot: append(json.RawMessage(nil), request.AuthorizationSnapshot...),
	})
	if err != nil {
		return nil, nil, err
	}
	resolved.Connection.Headers = nil
	resolved.Connection.SensitiveHeaderNames = nil
	resolvedSnapshot, err := json.Marshal(toolResumeResolvedSnapshot{
		SchemaVersion: ToolResumeResolvedSnapshotVersion, Resolved: resolved,
	})
	if err != nil {
		return nil, nil, err
	}
	return requestSnapshot, resolvedSnapshot, nil
}

// SideEffectInvoker runs an already-resolved capability (TOOL via pipeline or
// WORKFLOW via published-revision runner). *InvocationPipeline implements this.
type SideEffectInvoker interface {
	InvokeResolved(context.Context, InvokeRequest, ResolvedInvocation) (PipelineResult, error)
}

type ToolConfirmationResumeExecutor struct {
	invoker SideEffectInvoker
}

// NewToolConfirmationResumeExecutor accepts the pipeline or a composite invoker
// that can dispatch WORKFLOW capabilities (P3.4 Console Chat path).
func NewToolConfirmationResumeExecutor(
	invoker SideEffectInvoker,
) (*ToolConfirmationResumeExecutor, error) {
	if invoker == nil {
		return nil, errors.New("tool confirmation resume invoker is required")
	}
	return &ToolConfirmationResumeExecutor{invoker: invoker}, nil
}

func (*ToolConfirmationResumeExecutor) Kind() string { return ResumeKindTool }

func (executor *ToolConfirmationResumeExecutor) Execute(
	ctx context.Context,
	input ResumeExecutionInput,
) (ResumeExecutionOutput, error) {
	var requestSnapshot toolResumeRequestSnapshot
	var resolvedSnapshot toolResumeResolvedSnapshot
	if err := json.Unmarshal(input.RequestSnapshot, &requestSnapshot); err != nil ||
		requestSnapshot.SchemaVersion != ToolResumeRequestSnapshotVersion {
		return ResumeExecutionOutput{}, ErrConfirmationResumeInvalid
	}
	if err := json.Unmarshal(input.ResolvedSnapshot, &resolvedSnapshot); err != nil ||
		resolvedSnapshot.SchemaVersion != ToolResumeResolvedSnapshotVersion {
		return ResumeExecutionOutput{}, ErrConfirmationResumeInvalid
	}
	request := InvokeRequest{
		InvocationID: requestSnapshot.InvocationID, WorkspaceID: requestSnapshot.WorkspaceID,
		CapabilityID: requestSnapshot.CapabilityID, ReleaseID: requestSnapshot.ReleaseID,
		ActorType: requestSnapshot.ActorType, ActorID: requestSnapshot.ActorID,
		TraceID: requestSnapshot.TraceID, Input: cloneResumeJSON(input.Input),
		ExplicitConnectionID: requestSnapshot.ExplicitConnectionID,
		PlanConnectionID:     requestSnapshot.PlanConnectionID,
		BindingConnectionID:  requestSnapshot.BindingConnectionID,
		PlanHash:             requestSnapshot.PlanHash, ConfirmationID: input.ConfirmationID,
		IdempotencyKey:        requestSnapshot.IdempotencyKey,
		AgentRunID:            requestSnapshot.AgentRunID,
		WorkflowExecutionID:   requestSnapshot.WorkflowExecutionID,
		ExecutionStepID:       requestSnapshot.ExecutionStepID,
		PrincipalSnapshot:     requestSnapshot.PrincipalSnapshot,
		AuthorizationSnapshot: append(json.RawMessage(nil), requestSnapshot.AuthorizationSnapshot...),
	}
	result, err := executor.invoker.InvokeResolved(ctx, request, resolvedSnapshot.Resolved)
	payload, encodeErr := json.Marshal(toolResumeResultSnapshot{
		InvocationID: result.InvocationID, TraceID: result.TraceID,
		Output: cloneResumeJSON(result.Output), HTTPStatus: result.HTTPStatus,
		ContentType: result.ContentType, Attempts: result.Attempts, Cached: result.Cached,
	})
	if encodeErr != nil {
		return ResumeExecutionOutput{}, encodeErr
	}
	return ResumeExecutionOutput{Result: payload}, err
}
