package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/principal"
	"actweave/backend/internal/workflowruntime"

	"github.com/google/uuid"
)

const (
	TrialExecutionSucceeded = "SUCCEEDED"
	TrialExecutionFailed    = "FAILED"
	TrialExecutionCancelled = "CANCELLED"
)

type TrialExecutionRequest struct {
	ExecutionID   string
	WorkspaceID   string
	CapabilityID  string
	CompilationID string
	PlanHash      string
	Plan          domain.CompiledExecutionPlan
	Input         map[string]any
	StartedBy     string
	// ActorType / PrincipalSnapshot propagate immutable identity into Tool invokes
	// (checklist #10). Never carries Token material.
	ActorType         string
	PrincipalSnapshot *principal.ExecutionSnapshot
}

type TrialExecutionResult struct {
	ExecutionID string
	Status      string
}

type TrialRunner interface {
	Run(ctx context.Context, request TrialExecutionRequest) (TrialExecutionResult, error)
}

type TrialService struct {
	repository *Repository
	runner     TrialRunner
}

func NewTrialService(repository *Repository, runner TrialRunner) (*TrialService, error) {
	if repository == nil {
		return nil, errors.New("workflow trial repository is required")
	}
	if runner == nil {
		return nil, errors.New("workflow trial runner is required")
	}
	return &TrialService{repository: repository, runner: runner}, nil
}

func (s *TrialService) Run(
	ctx context.Context,
	workspaceID, capabilityID, compilationID, startedBy string,
	input json.RawMessage,
) (TrialRun, error) {
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
	trialID, err := uuid.NewV7()
	if err != nil {
		return TrialRun{}, fmt.Errorf("create workflow trial id: %w", err)
	}
	executionID, err := uuid.NewV7()
	if err != nil {
		return TrialRun{}, fmt.Errorf("create workflow trial execution id: %w", err)
	}
	trial, err := s.repository.CreateTrialRun(ctx, workspaceID, capabilityID, compilationID, TrialRunCreate{
		ID: trialID.String(), ExecutionID: executionID.String(), InputHash: inputHash, StartedBy: startedBy,
	})
	if err != nil {
		return TrialRun{}, err
	}

	result, runErr := s.runner.Run(ctx, TrialExecutionRequest{
		ExecutionID: executionID.String(), WorkspaceID: workspaceID, CapabilityID: capabilityID,
		CompilationID: compilationID, PlanHash: compilation.PlanHash, Plan: plan,
		Input: normalizedInput, StartedBy: startedBy,
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

func normalizeTrialExecutionStatus(
	ctx context.Context,
	executionID string,
	result TrialExecutionResult,
	runErr error,
) string {
	if ctx.Err() != nil {
		return TrialExecutionCancelled
	}
	if runErr != nil || result.ExecutionID != executionID {
		return TrialExecutionFailed
	}
	status := strings.ToUpper(strings.TrimSpace(result.Status))
	switch status {
	case TrialExecutionSucceeded, TrialExecutionFailed, TrialExecutionCancelled:
		return status
	default:
		return TrialExecutionFailed
	}
}

type CompiledPlanRunner interface {
	Run(plan domain.CompiledExecutionPlan, ctx workflowruntime.ExecutionContext) (domain.Execution, error)
}

type RuntimeTrialRunner struct{ runner CompiledPlanRunner }

func NewRuntimeTrialRunner(runner CompiledPlanRunner) (*RuntimeTrialRunner, error) {
	if runner == nil {
		return nil, errors.New("compiled workflow plan runner is required")
	}
	return &RuntimeTrialRunner{runner: runner}, nil
}

func (r *RuntimeTrialRunner) Run(
	ctx context.Context,
	request TrialExecutionRequest,
) (TrialExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return TrialExecutionResult{ExecutionID: request.ExecutionID, Status: TrialExecutionCancelled}, err
	}
	// TrialMode: Tool nodes still invoke published tools (real mock connection when
	// configured). Approval nodes auto-confirm so 模拟试运行 can complete on
	// realistic smart-dag.v2 topologies (Start→Tool→Approval→End) without HITL.
	// Production :execute keeps TrialMode=false (D11).
	//
	// WorkflowExecutionID must be the durable trial execution row created by
	// CreateTrialRun — tool_invocations.workflow_execution_id FKs that row.
	// Without it, einoruntime falls back to an ephemeral graph ExecutionID and
	// InvocationStarted fails with INVOCATION_RECORD_FAILED.
	execution, err := r.runner.Run(request.Plan, workflowruntime.ExecutionContext{
		UserID: request.StartedBy, Input: request.Input,
		WorkflowVersion: request.CompilationID, WorkspaceID: request.WorkspaceID,
		// Trigger string is copied into tool TraceID paths in adapters; include
		// "trial" so outbound inject uses WORKFLOW_TRIAL vault root keys.
		Trigger: "Workflow Compilation Trial", TrialMode: true,
		WorkflowExecutionID: request.ExecutionID,
		ActorType:           request.ActorType,
		PrincipalSnapshot:   request.PrincipalSnapshot,
	})
	status := TrialExecutionFailed
	switch execution.Status {
	case domain.ExecutionSuccess:
		status = TrialExecutionSucceeded
	case domain.ExecutionApproval, domain.ExecutionRunning:
		// Unexpected pause in trial (e.g. engine ignored TrialMode) → fail closed.
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
