package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/principal"
)

type RevisionSnapshot struct {
	WorkspaceID  string
	CapabilityID string
	ReleaseID    string
	RevisionID   string
	PlanHash     string
	Plan         domain.CompiledExecutionPlan
}

type RevisionSnapshotResolver interface {
	ResolveRevisionSnapshot(
		ctx context.Context,
		workspaceID, capabilityID, releaseID string,
	) (RevisionSnapshot, error)
}

type PublishedRunRequest struct {
	WorkspaceID           string
	CapabilityID          string
	ReleaseID             string
	ActorID               string
	ActorType             string
	PrincipalSnapshot     *principal.ExecutionSnapshot
	AuthorizationSnapshot json.RawMessage
	AgentRunID            string
	WorkflowExecutionID   string
	Input                 map[string]any
}

// PublishedRunResult is the published-revision run surface.
//
// Strategy C / PR12: Approval and EinoCheckPointID are preserved (not discarded)
// so chat/AAP can productize Workflow Approval HITL in PR14.
type PublishedRunResult struct {
	Snapshot  RevisionSnapshot
	Execution domain.Execution
	// Approval is non-nil when execution paused on an Approval node.
	Approval *WorkflowApprovalCheckpoint
	// EinoCheckPointID is the compose checkpoint key when the run used
	// eino_core/eino and interrupted. Mirrors Approval.EinoCheckPointID when set.
	EinoCheckPointID string
}

type PublishedRevisionRunner struct {
	resolver RevisionSnapshotResolver
	runner   CompiledPlanExecutor
}

// ApprovalResumeDecision is the platform decision passed to ResumeApproval.
type ApprovalResumeDecision struct {
	// Decision is confirmed | cancelled (einoruntime.ApprovalDecision* values).
	Decision string
	// ResolvedBy is the actor id that approved/cancelled.
	ResolvedBy string
}

// CompiledPlanExecutor executes a compiled plan.
//
// PR12 expands the surface with RunWithCheckpoint so published runs no longer
// drop Approval checkpoints. PR14 wires ResumeApproval (compose / ConfirmApproval).
type CompiledPlanExecutor interface {
	Run(plan domain.CompiledExecutionPlan, ctx ExecutionContext) (domain.Execution, error)
	RunWithCheckpoint(
		plan domain.CompiledExecutionPlan,
		ctx ExecutionContext,
	) (domain.Execution, *WorkflowApprovalCheckpoint, error)
	// ResumeApproval continues after platform confirmation.
	// eino_core: compose resume via checkpoint.EinoCheckPointID + interrupt IDs.
	// wrapper: ConfirmApproval / CancelApproval from checkpoint scope snapshot.
	ResumeApproval(
		plan domain.CompiledExecutionPlan,
		ctx ExecutionContext,
		checkpoint WorkflowApprovalCheckpoint,
		decision ApprovalResumeDecision,
	) (domain.Execution, error)
}

func NewPublishedRevisionRunner(
	resolver RevisionSnapshotResolver,
	runner CompiledPlanExecutor,
) (*PublishedRevisionRunner, error) {
	if resolver == nil || runner == nil {
		return nil, errors.New("published workflow runner dependencies are required")
	}
	return &PublishedRevisionRunner{resolver: resolver, runner: runner}, nil
}

func (r *PublishedRevisionRunner) Run(
	ctx context.Context,
	request PublishedRunRequest,
) (PublishedRunResult, error) {
	request, err := normalizePublishedRunRequest(request)
	if err != nil {
		return PublishedRunResult{}, errors.New("published workflow run identity is required")
	}
	if err := ctx.Err(); err != nil {
		return PublishedRunResult{}, err
	}
	snapshot, err := r.resolver.ResolveRevisionSnapshot(
		ctx, request.WorkspaceID, request.CapabilityID, request.ReleaseID,
	)
	if err != nil {
		return PublishedRunResult{}, err
	}
	return r.RunSnapshot(ctx, request, snapshot)
}

// RunSnapshot executes an already persisted immutable revision snapshot and
// never consults the mutable active-release resolver.
func (r *PublishedRevisionRunner) RunSnapshot(
	ctx context.Context,
	request PublishedRunRequest,
	snapshot RevisionSnapshot,
) (PublishedRunResult, error) {
	request, err := normalizePublishedRunRequest(request)
	if err != nil {
		return PublishedRunResult{}, err
	}
	snapshot, err = cloneRevisionSnapshot(snapshot)
	if err != nil {
		return PublishedRunResult{}, err
	}
	if snapshot.WorkspaceID != request.WorkspaceID ||
		snapshot.CapabilityID != request.CapabilityID || snapshot.ReleaseID != request.ReleaseID ||
		strings.TrimSpace(snapshot.RevisionID) == "" || strings.TrimSpace(snapshot.PlanHash) == "" {
		return PublishedRunResult{}, errors.New("resolved workflow revision snapshot does not match request")
	}
	execution, approval, err := r.runner.RunWithCheckpoint(snapshot.Plan, ExecutionContext{
		UserID: request.ActorID, Input: cloneMap(request.Input),
		WorkflowVersion: snapshot.RevisionID, WorkspaceID: snapshot.WorkspaceID,
		Trigger:   "Workflow Published Revision",
		ActorType: request.ActorType, PrincipalSnapshot: request.PrincipalSnapshot,
		AuthorizationSnapshot: append(json.RawMessage(nil), request.AuthorizationSnapshot...),
		AgentRunID:            request.AgentRunID, WorkflowExecutionID: request.WorkflowExecutionID,
	})
	if ctx.Err() != nil && err == nil {
		err = ctx.Err()
	}
	einoCheckPointID := ""
	if approval != nil {
		einoCheckPointID = strings.TrimSpace(approval.EinoCheckPointID)
	}
	return PublishedRunResult{
		Snapshot:         snapshot,
		Execution:        execution,
		Approval:         approval,
		EinoCheckPointID: einoCheckPointID,
	}, err
}

// ResumeApproval continues a published-revision Approval pause via the inner
// CompiledPlanExecutor (compose resume or wrapper ConfirmApproval).
func (r *PublishedRevisionRunner) ResumeApproval(
	ctx context.Context,
	request PublishedRunRequest,
	snapshot RevisionSnapshot,
	checkpoint WorkflowApprovalCheckpoint,
	decision ApprovalResumeDecision,
) (PublishedRunResult, error) {
	request, err := normalizePublishedRunRequest(request)
	if err != nil {
		return PublishedRunResult{}, errors.New("published workflow run identity is required")
	}
	snapshot, err = cloneRevisionSnapshot(snapshot)
	if err != nil {
		return PublishedRunResult{}, err
	}
	if snapshot.WorkspaceID != request.WorkspaceID ||
		snapshot.CapabilityID != request.CapabilityID || snapshot.ReleaseID != request.ReleaseID ||
		strings.TrimSpace(snapshot.RevisionID) == "" || strings.TrimSpace(snapshot.PlanHash) == "" {
		return PublishedRunResult{}, errors.New("resolved workflow revision snapshot does not match request")
	}
	if ctx.Err() != nil {
		return PublishedRunResult{}, ctx.Err()
	}
	execCtx := ExecutionContext{
		UserID: request.ActorID, Input: cloneMap(request.Input),
		WorkflowVersion: snapshot.RevisionID, WorkspaceID: snapshot.WorkspaceID,
		Trigger:   "Workflow Published Revision Resume",
		ActorType: request.ActorType, PrincipalSnapshot: request.PrincipalSnapshot,
		AuthorizationSnapshot: append(json.RawMessage(nil), request.AuthorizationSnapshot...),
		AgentRunID:            request.AgentRunID, WorkflowExecutionID: request.WorkflowExecutionID,
	}
	// Prefer checkpoint.Context when present (scope/identity from pause time).
	if strings.TrimSpace(checkpoint.Context.UserID) != "" || checkpoint.Context.WorkspaceID != "" {
		execCtx = cloneExecutionContext(checkpoint.Context)
		if request.AgentRunID != "" {
			execCtx.AgentRunID = request.AgentRunID
		}
		if request.WorkflowExecutionID != "" {
			execCtx.WorkflowExecutionID = request.WorkflowExecutionID
		}
	}
	// Ensure plan on checkpoint matches immutable revision when empty.
	if strings.TrimSpace(checkpoint.Plan.WorkflowID) == "" {
		checkpoint.Plan = snapshot.Plan
	}
	execution, err := r.runner.ResumeApproval(snapshot.Plan, execCtx, checkpoint, decision)
	if ctx.Err() != nil && err == nil {
		err = ctx.Err()
	}
	return PublishedRunResult{
		Snapshot:         snapshot,
		Execution:        execution,
		EinoCheckPointID: strings.TrimSpace(checkpoint.EinoCheckPointID),
	}, err
}

func normalizePublishedRunRequest(request PublishedRunRequest) (PublishedRunRequest, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.CapabilityID = strings.TrimSpace(request.CapabilityID)
	request.ReleaseID = strings.TrimSpace(request.ReleaseID)
	request.ActorID = strings.TrimSpace(request.ActorID)
	request.ActorType = strings.ToUpper(strings.TrimSpace(request.ActorType))
	request.AgentRunID = strings.TrimSpace(request.AgentRunID)
	request.WorkflowExecutionID = strings.TrimSpace(request.WorkflowExecutionID)
	if request.ActorType == "" {
		request.ActorType = string(principal.TypeUser)
	}
	if request.WorkspaceID == "" || request.CapabilityID == "" ||
		request.ReleaseID == "" || request.ActorID == "" {
		return PublishedRunRequest{}, errors.New("published workflow run identity is required")
	}
	if request.PrincipalSnapshot == nil {
		if request.ActorType == string(principal.TypeServicePrincipal) {
			return PublishedRunRequest{}, errors.New("service principal workflow run snapshot is required")
		}
	} else {
		snapshot := cloneExecutionPrincipalSnapshot(request.PrincipalSnapshot)
		if snapshot.Validate() != nil || snapshot.Identity.Actor.WorkspaceID != request.WorkspaceID ||
			string(snapshot.Identity.Actor.Type) != request.ActorType ||
			snapshot.Identity.Actor.ID != request.ActorID {
			return PublishedRunRequest{}, errors.New("published workflow run snapshot does not match actor")
		}
		request.PrincipalSnapshot = snapshot
	}
	request.AuthorizationSnapshot = append(json.RawMessage(nil), request.AuthorizationSnapshot...)
	request.Input = cloneMap(request.Input)
	return request, nil
}

func cloneRevisionSnapshot(value RevisionSnapshot) (RevisionSnapshot, error) {
	payload, err := json.Marshal(value.Plan)
	if err != nil {
		return RevisionSnapshot{}, err
	}
	var plan domain.CompiledExecutionPlan
	if err := json.Unmarshal(payload, &plan); err != nil {
		return RevisionSnapshot{}, err
	}
	value.Plan = plan
	return value, nil
}
