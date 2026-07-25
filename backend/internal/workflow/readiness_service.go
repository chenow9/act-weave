package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"actweave/backend/internal/domain"
)

const (
	ReadinessDisabled        = "DISABLED"
	ReadinessCompileRequired = "COMPILE_REQUIRED"
	ReadinessCompileFailed   = "COMPILE_FAILED"
	ReadinessTrialRequired   = "TRIAL_REQUIRED"
	ReadinessPublishReady    = "PUBLISH_READY"
	ReadinessPublished       = "PUBLISHED"
)

type ReadinessService struct{ repository *Repository }

func NewReadinessService(repository *Repository) (*ReadinessService, error) {
	if repository == nil {
		return nil, errors.New("workflow readiness repository is required")
	}
	return &ReadinessService{repository: repository}, nil
}

func (s *ReadinessService) Get(
	ctx context.Context,
	workspaceID, capabilityID string,
) (Readiness, error) {
	workflow, err := s.repository.Get(ctx, workspaceID, capabilityID)
	if err != nil {
		return Readiness{}, err
	}
	draft, err := s.repository.GetDraft(ctx, workspaceID, capabilityID)
	if err != nil {
		return Readiness{}, err
	}
	value := Readiness{
		Stage: ReadinessCompileRequired, CanCompile: true,
		ActiveRevisionID: workflow.ActiveRevisionID,
		Published:        workflow.ActiveRevisionID != nil,
		Blockers:         make([]ReadinessBlocker, 0),
		UpdatedAt:        laterTime(workflow.UpdatedAt, draft.UpdatedAt),
	}
	if workflow.Status == "DISABLED" {
		value.Stage = ReadinessDisabled
		value.CanCompile = false
		value.Blockers = append(value.Blockers, readinessBlocker(
			"workflow_disabled", "Workflow is disabled.", "Enable the workflow before compiling or running it.",
		))
		return value, nil
	}
	if workflow.LatestCompilationID == nil {
		value.Blockers = append(value.Blockers, readinessBlocker(
			"compile_required", "Workflow draft needs a compilation.", "Compile the current workflow draft.",
		))
		return value, nil
	}
	value.CompilationID = workflow.LatestCompilationID
	compilation, err := s.repository.GetCompilation(ctx, workspaceID, capabilityID, *workflow.LatestCompilationID)
	if err != nil {
		return Readiness{}, err
	}
	value.UpdatedAt = laterTime(value.UpdatedAt, compilation.CompiledAt)
	value.CompilationCurrent = compilation.DraftID == draft.ID &&
		compilation.DraftVersion == draft.DraftVersion && compilation.GraphHash == draft.GraphHash
	if !value.CompilationCurrent {
		value.Blockers = append(value.Blockers, readinessBlocker(
			"compile_required", "Workflow draft changed after compilation.", "Compile the current workflow draft again.",
		))
		return value, nil
	}
	value.CompilationValid = compilation.Status == "VALID"
	if !value.CompilationValid {
		value.Stage = ReadinessCompileFailed
		value.Blockers = compilationReadinessBlockers(compilation.Issues)
		if len(value.Blockers) == 0 {
			value.Blockers = append(value.Blockers, readinessBlocker(
				"compile_failed", "Workflow compilation failed.", "Fix the workflow graph and compile it again.",
			))
		}
		return value, nil
	}
	value.CanTrial = true
	trial, err := s.repository.GetLatestSuccessfulTrialRun(ctx, workspaceID, capabilityID, compilation.ID)
	if errors.Is(err, ErrNotFound) {
		value.Stage = ReadinessTrialRequired
		value.Blockers = append(value.Blockers, readinessBlocker(
			"trial_required", "Current compilation needs a successful trial run.", "Run a trial for this compilation.",
		))
		return value, nil
	}
	if err != nil {
		return Readiness{}, err
	}
	value.TrialCurrent = true
	value.TrialSuccessful = trial.Status == "SUCCEEDED"
	value.UpdatedAt = laterTime(value.UpdatedAt, trial.StartedAt)
	if trial.FinishedAt != nil {
		value.UpdatedAt = laterTime(value.UpdatedAt, *trial.FinishedAt)
	}
	if !value.TrialSuccessful {
		value.Stage = ReadinessTrialRequired
		value.Blockers = append(value.Blockers, readinessBlocker(
			"trial_failed", "Latest trial for the current compilation did not succeed.", "Fix the runtime issue and run the trial again.",
		))
		return value, nil
	}
	value.CanPublish = true
	value.Stage = ReadinessPublishReady
	if workflow.ActiveRevisionID != nil {
		sourceCompilationID, err := s.repository.GetActiveRevisionSourceCompilation(
			ctx, workspaceID, capabilityID, *workflow.ActiveRevisionID,
		)
		if err != nil {
			return Readiness{}, err
		}
		if sourceCompilationID == compilation.ID {
			value.Stage = ReadinessPublished
			value.CanPublish = false
		}
	}
	return value, nil
}

func compilationReadinessBlockers(payload json.RawMessage) []ReadinessBlocker {
	var issues []domain.WorkflowCompilationIssue
	if err := json.Unmarshal(payload, &issues); err != nil {
		return nil
	}
	values := make([]ReadinessBlocker, 0, len(issues))
	for _, issue := range issues {
		code := strings.TrimSpace(issue.Code)
		if code == "" {
			code = "compile_failed"
		}
		action := strings.TrimSpace(issue.Suggestion)
		if action == "" {
			action = "Fix this compilation issue and compile the workflow again."
		}
		severity := strings.TrimSpace(issue.Severity)
		if severity == "" {
			severity = "error"
		}
		values = append(values, ReadinessBlocker{
			Code: code, Message: issue.Message, Action: action, Severity: severity,
			SourceStage: string(issue.SourceStage), NodeID: issue.NodeID,
			EdgeID: issue.EdgeID, FieldPath: issue.FieldPath,
		})
	}
	return values
}

func readinessBlocker(code, message, action string) ReadinessBlocker {
	return ReadinessBlocker{Code: code, Message: message, Action: action, Severity: "error"}
}

func laterTime(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}
