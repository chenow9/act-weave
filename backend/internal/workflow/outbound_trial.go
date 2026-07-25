package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/principal"
	"actweave/backend/internal/workflowruntime"

	"github.com/google/uuid"
)

// TrialOutboundOptions carries the write-only envelope and process identity for a trial.
// Token material must never enter TrialRun persistence or logs.
type TrialOutboundOptions struct {
	CredentialsRaw json.RawMessage
	ActorType      string
	BootID         string
	InstanceID     string
}

// OutboundTrialService extends trial execution with BindingAttacher + terminal cleanup.
// Implements the HTTP WorkflowTrialer.Run surface.
type OutboundTrialService struct {
	repository  *Repository
	runner      TrialRunner
	attacher    *outboundidentity.BindingAttacher
	loader      *OutboundRequirementsLoader
	cleaner     *execution.RootOutboundLifecycle
	connections TrialConnectionLookup
}

// TrialConnectionLookup resolves non-secret Connection policy views for attach.
type TrialConnectionLookup interface {
	LookupConnections(ctx context.Context, workspaceID string, connectionIDs []string) ([]outboundidentity.ConnectionPolicyView, error)
}

// NewOutboundTrialService constructs the outbound-aware trial service.
func NewOutboundTrialService(
	repository *Repository,
	runner TrialRunner,
	attacher *outboundidentity.BindingAttacher,
	loader *OutboundRequirementsLoader,
	cleaner *execution.RootOutboundLifecycle,
	connections TrialConnectionLookup,
) (*OutboundTrialService, error) {
	if repository == nil || runner == nil {
		return nil, errors.New("outbound trial repository and runner are required")
	}
	return &OutboundTrialService{
		repository: repository, runner: runner, attacher: attacher,
		loader: loader, cleaner: cleaner, connections: connections,
	}, nil
}

// Run implements WorkflowTrialer without credentials (Broker-only / no-passthrough plans).
func (s *OutboundTrialService) Run(
	ctx context.Context,
	workspaceID, capabilityID, compilationID, startedBy string,
	input json.RawMessage,
) (TrialRun, error) {
	return s.RunWithOutbound(ctx, workspaceID, capabilityID, compilationID, startedBy, input, TrialOutboundOptions{
		ActorType: "USER",
	})
}

// RunWithOutbound attaches trial envelope credentials then runs the plan.
func (s *OutboundTrialService) RunWithOutbound(
	ctx context.Context,
	workspaceID, capabilityID, compilationID, startedBy string,
	input json.RawMessage,
	opts TrialOutboundOptions,
) (TrialRun, error) {
	if s == nil {
		return TrialRun{}, ErrInvalid
	}
	startedBy = strings.TrimSpace(startedBy)
	canonicalInput, inputHash, err := canonicalJSON(input, "object")
	if !validUUID(startedBy) || err != nil {
		return TrialRun{}, ErrInvalid
	}
	compilation, err := s.repository.GetCurrentValidCompilation(
		ctx, workspaceID, capabilityID, compilationID,
	)
	if err != nil {
		return TrialRun{}, err
	}
	var plan domain.CompiledExecutionPlan
	if err := json.Unmarshal(compilation.Plan, &plan); err != nil {
		return TrialRun{}, fmt.Errorf("decode immutable workflow trial plan: %w", err)
	}
	var normalizedInput map[string]any
	if err := json.Unmarshal(canonicalInput, &normalizedInput); err != nil {
		return TrialRun{}, ErrInvalid
	}

	actorType := strings.TrimSpace(opts.ActorType)
	if actorType == "" {
		actorType = "USER"
	}
	if actorType == "SYSTEM" {
		return TrialRun{}, outboundidentity.ErrSubjectRequired
	}
	snapshot, err := principal.NewInternalExecutionSnapshot(
		workspaceID, principal.Type(actorType), startedBy,
	)
	if err != nil {
		return TrialRun{}, outboundidentity.ErrSubjectRequired
	}

	// Requirements from plan (compiled descriptor only — no Secret).
	var requirements outboundidentity.Requirements
	if plan.OutboundRequirements != nil {
		raw, _ := json.Marshal(plan.OutboundRequirements)
		if string(raw) != "null" && len(raw) > 0 {
			requirements, err = outboundidentity.ParseRequirements(raw)
			if err != nil {
				return TrialRun{}, err
			}
		}
	}
	needsPassthrough := false
	for _, c := range requirements.Connections {
		if c.Mode == outboundidentity.ModeRequestPassthrough {
			needsPassthrough = true
			break
		}
	}
	if needsPassthrough && len(opts.CredentialsRaw) == 0 {
		return TrialRun{}, outboundidentity.ErrCredentialRequired
	}

	trialID, err := uuid.NewV7()
	if err != nil {
		return TrialRun{}, fmt.Errorf("create workflow trial id: %w", err)
	}
	executionID, err := uuid.NewV7()
	if err != nil {
		return TrialRun{}, fmt.Errorf("create workflow trial execution id: %w", err)
	}

	// Attach before CreateTrialRun fails? Prefer create first so root ID is durable,
	// then attach; if attach fails, complete as FAILED and cleanup.
	trial, err := s.repository.CreateTrialRun(ctx, workspaceID, capabilityID, compilationID, TrialRunCreate{
		ID: trialID.String(), ExecutionID: executionID.String(), InputHash: inputHash, StartedBy: startedBy,
	})
	if err != nil {
		return TrialRun{}, err
	}

	subjectType := outboundidentity.SubjectTypeUser
	subjectID := startedBy
	if snapshot.Identity.Subject != nil {
		switch snapshot.Identity.Subject.Type {
		case principal.TypeExternalSubject:
			subjectType = outboundidentity.SubjectTypeExternalSubject
		case principal.TypeUser:
			subjectType = outboundidentity.SubjectTypeUser
		}
		subjectID = snapshot.Identity.Subject.ID
	}
	bootID := strings.TrimSpace(opts.BootID)
	if bootID == "" && s.cleaner != nil {
		bootID = s.cleaner.BootID
	}
	if bootID == "" {
		bootID = "trial-boot"
	}

	affinityClaimed := false
	if needsPassthrough {
		if s.attacher == nil || s.connections == nil {
			_, _ = s.repository.CompleteTrialRun(
				context.WithoutCancel(ctx), workspaceID, capabilityID, trial.ID, TrialExecutionFailed,
			)
			return trial, outboundidentity.ErrCredentialRequired
		}
		connIDs := make([]string, 0, len(requirements.Connections))
		for _, c := range requirements.Connections {
			connIDs = append(connIDs, c.ConnectionID)
		}
		views, lookupErr := s.connections.LookupConnections(ctx, workspaceID, connIDs)
		if lookupErr != nil {
			_, _ = s.repository.CompleteTrialRun(
				context.WithoutCancel(ctx), workspaceID, capabilityID, trial.ID, TrialExecutionFailed,
			)
			return trial, lookupErr
		}
		attachResult, attachErr := s.attacher.Attach(ctx, outboundidentity.BindingAttachInput{
			RawEnvelope:  opts.CredentialsRaw,
			Requirements: requirements,
			Connections:  views,
			Context: outboundidentity.BindingAttachContext{
				BootID: bootID, WorkspaceID: workspaceID,
				SubjectType: subjectType, SubjectID: subjectID,
				RootScopeType:   outboundidentity.RootScopeWorkflowTrial,
				RootScopeID:     executionID.String(),
				RootDeadline:    time.Now().UTC().Add(30 * time.Minute),
				OwnerInstanceID: strings.TrimSpace(opts.InstanceID),
				OwnerBootID:     bootID,
			},
		})
		if attachErr != nil {
			_, _ = s.repository.CompleteTrialRun(
				context.WithoutCancel(ctx), workspaceID, capabilityID, trial.ID, TrialExecutionFailed,
			)
			return trial, attachErr
		}
		affinityClaimed = attachResult.AffinityClaimed
		// Zero credentials from caller options (best-effort).
		for i := range opts.CredentialsRaw {
			opts.CredentialsRaw[i] = 0
		}
	}

	// Always cleanup vault/cache/affinity on exit (terminal).
	defer func() {
		if s.cleaner != nil {
			s.cleaner.CleanupRoot(context.WithoutCancel(ctx), execution.CleanupRootInput{
				BootID: bootID, WorkspaceID: workspaceID,
				SubjectType: subjectType, SubjectID: subjectID,
				RootScopeType: outboundidentity.RootScopeWorkflowTrial,
				RootScopeID:   executionID.String(),
				ClearAffinity: affinityClaimed || needsPassthrough,
			})
		} else if s.attacher != nil && (affinityClaimed || needsPassthrough) {
			s.attacher.CleanupRequest(context.WithoutCancel(ctx), outboundidentity.BindingAttachContext{
				BootID: bootID, WorkspaceID: workspaceID,
				SubjectType: subjectType, SubjectID: subjectID,
				RootScopeType: outboundidentity.RootScopeWorkflowTrial,
				RootScopeID:   executionID.String(),
			}, affinityClaimed)
		}
	}()

	result, runErr := s.runner.Run(ctx, TrialExecutionRequest{
		ExecutionID: executionID.String(), WorkspaceID: workspaceID, CapabilityID: capabilityID,
		CompilationID: compilationID, PlanHash: compilation.PlanHash, Plan: plan,
		Input: normalizedInput, StartedBy: startedBy,
		PrincipalSnapshot: &snapshot, ActorType: actorType,
	})
	status := normalizeTrialExecutionStatus(ctx, executionID.String(), result, runErr)
	if runErr != nil || status != TrialExecutionSucceeded {
		slog.Warn("workflow trial runner outcome",
			"event", "workflow.trial.runner_outcome",
			"workspace_id", workspaceID,
			"workflow_id", capabilityID,
			"compilation_id", compilationID,
			"execution_id", executionID.String(),
			"result_status", result.Status,
			"normalized_status", status,
			"run_error", fmt.Sprintf("%v", runErr),
		)
	}
	completed, completeErr := s.repository.CompleteTrialRun(
		context.WithoutCancel(ctx), workspaceID, capabilityID, trial.ID, status,
	)
	if completeErr != nil {
		return TrialRun{}, completeErr
	}
	if runErr != nil || status != TrialExecutionSucceeded {
		return completed, ErrTrialFailed
	}
	return completed, nil
}

// Compile-time: OutboundTrialService can back WorkflowTrialer when only Run is needed.
var _ interface {
	Run(context.Context, string, string, string, string, json.RawMessage) (TrialRun, error)
} = (*OutboundTrialService)(nil)

// PrincipalAwareTrialRunner injects Principal into workflowruntime ExecutionContext.
type PrincipalAwareTrialRunner struct {
	inner CompiledPlanRunner
}

// NewPrincipalAwareTrialRunner wraps a plan runner for trial principal propagation.
func NewPrincipalAwareTrialRunner(runner CompiledPlanRunner) (*PrincipalAwareTrialRunner, error) {
	if runner == nil {
		return nil, errors.New("compiled plan runner is required")
	}
	return &PrincipalAwareTrialRunner{inner: runner}, nil
}

// Run implements TrialRunner with principal + workflow execution root.
func (r *PrincipalAwareTrialRunner) Run(
	ctx context.Context,
	request TrialExecutionRequest,
) (TrialExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return TrialExecutionResult{ExecutionID: request.ExecutionID, Status: TrialExecutionCancelled}, err
	}
	execCtx := workflowruntime.ExecutionContext{
		UserID: request.StartedBy, Input: request.Input,
		WorkflowVersion: request.CompilationID, WorkspaceID: request.WorkspaceID,
		Trigger: "Workflow Compilation Trial", TrialMode: true,
		WorkflowExecutionID: request.ExecutionID,
		ActorType:           request.ActorType,
		PrincipalSnapshot:   request.PrincipalSnapshot,
	}
	if execCtx.ActorType == "" {
		execCtx.ActorType = "USER"
	}
	execution, err := r.inner.Run(request.Plan, execCtx)
	status := TrialExecutionFailed
	switch execution.Status {
	case domain.ExecutionSuccess:
		status = TrialExecutionSucceeded
	case domain.ExecutionApproval, domain.ExecutionRunning:
		status = TrialExecutionFailed
	case domain.ExecutionFailed:
		status = TrialExecutionFailed
	}
	if ctx.Err() != nil {
		status = TrialExecutionCancelled
		if err == nil {
			err = ctx.Err()
		}
	}
	return TrialExecutionResult{ExecutionID: request.ExecutionID, Status: status}, err
}
