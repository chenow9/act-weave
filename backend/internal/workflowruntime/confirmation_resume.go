package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"
)

const (
	workflowResumeRequestSnapshotVersion  = "workflow-resume-request.v1"
	workflowResumeResolvedSnapshotVersion = "workflow-resume-resolved.v1"
	workflowResumeResultSnapshotVersion   = "workflow-resume-result.v1"
)

type workflowResumeRequestSnapshot struct {
	SchemaVersion         string                       `json:"schemaVersion"`
	WorkspaceID           string                       `json:"workspaceId"`
	CapabilityID          string                       `json:"capabilityId"`
	ReleaseID             string                       `json:"releaseId"`
	ActorID               string                       `json:"actorId"`
	ActorType             string                       `json:"actorType,omitempty"`
	PrincipalSnapshot     *principal.ExecutionSnapshot `json:"principalSnapshot,omitempty"`
	AuthorizationSnapshot json.RawMessage              `json:"authorizationSnapshot,omitempty"`
	AgentRunID            string                       `json:"agentRunId,omitempty"`
	WorkflowExecutionID   string                       `json:"workflowExecutionId,omitempty"`
	ConnectionID          string                       `json:"connectionId,omitempty"`
	PlanHash              string                       `json:"planHash"`
	// PR14 strategy C: compose resume surface when Approval paused on eino_core.
	EinoCheckPointID string   `json:"einoCheckPointId,omitempty"`
	EinoInterruptIDs []string `json:"einoInterruptIds,omitempty"`
	ApprovalNodeID   string   `json:"approvalNodeId,omitempty"`
	ApprovalReason   string   `json:"approvalReason,omitempty"`
}

type workflowResumeResolvedSnapshot struct {
	SchemaVersion string           `json:"schemaVersion"`
	Snapshot      RevisionSnapshot `json:"snapshot"`
	// Approval is the durable WorkflowApprovalCheckpoint when the pause was an
	// Approval node (wrapper ConfirmApproval or eino compose resume).
	Approval *WorkflowApprovalCheckpoint `json:"approval,omitempty"`
}

// BuildWorkflowConfirmationResumeSnapshots builds durable request/resolved
// snapshots for ResumeKindWorkflow. When approval is non-nil, snapshots carry
// the Approval checkpoint surface for compose / ConfirmApproval resume.
func BuildWorkflowConfirmationResumeSnapshots(
	request PublishedRunRequest,
	snapshot RevisionSnapshot,
) (json.RawMessage, json.RawMessage, error) {
	return BuildWorkflowConfirmationResumeSnapshotsWithApproval(request, snapshot, "", nil)
}

// BuildWorkflowConfirmationResumeSnapshotsWithApproval is the PR14 Approval HITL
// snapshot builder. connectionID may be empty for pure workflow capabilities.
func BuildWorkflowConfirmationResumeSnapshotsWithApproval(
	request PublishedRunRequest,
	snapshot RevisionSnapshot,
	connectionID string,
	approval *WorkflowApprovalCheckpoint,
) (json.RawMessage, json.RawMessage, error) {
	request, requestErr := normalizePublishedRunRequest(request)
	cloned, err := cloneRevisionSnapshot(snapshot)
	if requestErr != nil || err != nil ||
		cloned.WorkspaceID != request.WorkspaceID || cloned.CapabilityID != request.CapabilityID ||
		cloned.ReleaseID != request.ReleaseID || strings.TrimSpace(cloned.PlanHash) == "" {
		return nil, nil, execution.ErrConfirmationResumeInvalid
	}
	reqSnap := workflowResumeRequestSnapshot{
		SchemaVersion: workflowResumeRequestSnapshotVersion,
		WorkspaceID:   request.WorkspaceID, CapabilityID: request.CapabilityID,
		ReleaseID: request.ReleaseID, ActorID: request.ActorID, PlanHash: cloned.PlanHash,
		ActorType: request.ActorType, PrincipalSnapshot: cloneExecutionPrincipalSnapshot(request.PrincipalSnapshot),
		AuthorizationSnapshot: append(json.RawMessage(nil), request.AuthorizationSnapshot...),
		AgentRunID:            request.AgentRunID, WorkflowExecutionID: request.WorkflowExecutionID,
		ConnectionID: strings.TrimSpace(connectionID),
	}
	var approvalCopy *WorkflowApprovalCheckpoint
	if approval != nil {
		copied := *approval
		// Ensure plan is present for wrapper ConfirmApproval.
		if strings.TrimSpace(copied.Plan.WorkflowID) == "" {
			copied.Plan = cloned.Plan
		}
		approvalCopy = &copied
		reqSnap.EinoCheckPointID = strings.TrimSpace(copied.EinoCheckPointID)
		reqSnap.EinoInterruptIDs = append([]string(nil), copied.EinoInterruptIDs...)
		reqSnap.ApprovalNodeID = copied.NodeID
		reqSnap.ApprovalReason = copied.NodeReason
	}
	requestPayload, err := json.Marshal(reqSnap)
	if err != nil {
		return nil, nil, err
	}
	resolvedPayload, err := json.Marshal(workflowResumeResolvedSnapshot{
		SchemaVersion: workflowResumeResolvedSnapshotVersion,
		Snapshot:      cloned,
		Approval:      approvalCopy,
	})
	if err != nil {
		return nil, nil, err
	}
	return requestPayload, resolvedPayload, nil
}

type ConfirmationResumeExecutor struct {
	runner *PublishedRevisionRunner
}

func NewConfirmationResumeExecutor(
	runner *PublishedRevisionRunner,
) (*ConfirmationResumeExecutor, error) {
	if runner == nil {
		return nil, errors.New("workflow confirmation resume runner is required")
	}
	return &ConfirmationResumeExecutor{runner: runner}, nil
}

func (*ConfirmationResumeExecutor) Kind() string { return execution.ResumeKindWorkflow }

func (executor *ConfirmationResumeExecutor) Execute(
	ctx context.Context,
	input execution.ResumeExecutionInput,
) (execution.ResumeExecutionOutput, error) {
	var requestSnapshot workflowResumeRequestSnapshot
	var resolvedSnapshot workflowResumeResolvedSnapshot
	if err := json.Unmarshal(input.RequestSnapshot, &requestSnapshot); err != nil ||
		requestSnapshot.SchemaVersion != workflowResumeRequestSnapshotVersion {
		return execution.ResumeExecutionOutput{}, execution.ErrConfirmationResumeInvalid
	}
	if err := json.Unmarshal(input.ResolvedSnapshot, &resolvedSnapshot); err != nil ||
		resolvedSnapshot.SchemaVersion != workflowResumeResolvedSnapshotVersion {
		return execution.ResumeExecutionOutput{}, execution.ErrConfirmationResumeInvalid
	}
	var workflowInput map[string]any
	if err := json.Unmarshal(input.Input, &workflowInput); err != nil || workflowInput == nil {
		return execution.ResumeExecutionOutput{}, execution.ErrConfirmationResumeInvalid
	}
	runRequest := PublishedRunRequest{
		WorkspaceID: requestSnapshot.WorkspaceID, CapabilityID: requestSnapshot.CapabilityID,
		ReleaseID: requestSnapshot.ReleaseID, ActorID: requestSnapshot.ActorID,
		ActorType:             requestSnapshot.ActorType,
		PrincipalSnapshot:     cloneExecutionPrincipalSnapshot(requestSnapshot.PrincipalSnapshot),
		AuthorizationSnapshot: append(json.RawMessage(nil), requestSnapshot.AuthorizationSnapshot...),
		AgentRunID:            requestSnapshot.AgentRunID, WorkflowExecutionID: requestSnapshot.WorkflowExecutionID,
		Input: workflowInput,
	}

	// PR14 strategy C: Approval pause → ResumeApproval (compose or wrapper).
	// Prefer durable Approval on resolved snapshot; fall back to request IDs.
	if approval := resolvedSnapshot.Approval; approval != nil &&
		(strings.TrimSpace(approval.EinoCheckPointID) != "" ||
			approval.Status == WorkflowApprovalPending ||
			strings.TrimSpace(approval.NodeID) != "") {
		// Overlay request-surface IDs when present (newer pause writers).
		if id := strings.TrimSpace(requestSnapshot.EinoCheckPointID); id != "" {
			approval.EinoCheckPointID = id
		}
		if len(requestSnapshot.EinoInterruptIDs) > 0 {
			approval.EinoInterruptIDs = append([]string(nil), requestSnapshot.EinoInterruptIDs...)
		}
		result, runErr := executor.runner.ResumeApproval(
			ctx, runRequest, resolvedSnapshot.Snapshot, *approval,
			ApprovalResumeDecision{
				Decision:   einoruntime.ApprovalDecisionConfirmed,
				ResolvedBy: requestSnapshot.ActorID,
			},
		)
		return encodeWorkflowResumeResult(result, runErr)
	}
	// Compat: pre-execution workflow confirmation re-runs the immutable revision
	// (no Approval checkpoint). Not the primary Approval HITL path.
	result, runErr := executor.runner.RunSnapshot(ctx, runRequest, resolvedSnapshot.Snapshot)
	return encodeWorkflowResumeResult(result, runErr)
}

func encodeWorkflowResumeResult(
	result PublishedRunResult,
	runErr error,
) (execution.ResumeExecutionOutput, error) {
	status := string(result.Execution.Status)
	if status == "" {
		status = string(domain.ExecutionSuccess)
	}
	payload, err := json.Marshal(map[string]any{
		"schemaVersion":   workflowResumeResultSnapshotVersion,
		"releaseId":       result.Snapshot.ReleaseID,
		"revisionId":      result.Snapshot.RevisionID,
		"planHash":        result.Snapshot.PlanHash,
		"execution":       result.Execution,
		"executionStatus": status,
	})
	if err != nil {
		return execution.ResumeExecutionOutput{}, err
	}
	return execution.ResumeExecutionOutput{Result: payload}, runErr
}
