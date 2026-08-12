package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/principal"

	"github.com/google/uuid"
)

const ErrorCodeConfirmationResumeExecution = "CONFIRMATION_RESUME_EXECUTION_FAILED"

type ConfirmationResumeService struct {
	checkpoints   *ConfirmationResumeRepository
	confirmations *ConfirmationService
	runs          *RunRepository
	executors     *ConfirmationResumeRegistry
	now           func() time.Time
	claimLease    time.Duration
}

func NewConfirmationResumeService(
	checkpoints *ConfirmationResumeRepository,
	confirmations *ConfirmationService,
	runs *RunRepository,
	executors *ConfirmationResumeRegistry,
) (*ConfirmationResumeService, error) {
	if checkpoints == nil || confirmations == nil || runs == nil || executors == nil {
		return nil, errors.New("confirmation resume service dependencies are required")
	}
	return &ConfirmationResumeService{
		checkpoints: checkpoints, confirmations: confirmations, runs: runs, executors: executors,
		now:        func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
		claimLease: 30 * time.Second,
	}, nil
}

func (service *ConfirmationResumeService) Prepare(
	ctx context.Context,
	input PrepareConfirmationResumeInput,
) (PreparedConfirmationResume, error) {
	tx, err := service.checkpoints.begin(ctx)
	if err != nil {
		return PreparedConfirmationResume{}, fmt.Errorf("begin confirmation pause: %w", err)
	}
	defer tx.Rollback()
	prepared, err := service.PrepareInTransaction(ctx, tx, input)
	if err != nil {
		return PreparedConfirmationResume{}, err
	}
	if err := tx.Commit(); err != nil {
		return PreparedConfirmationResume{}, fmt.Errorf("commit confirmation pause: %w", err)
	}
	return prepared, nil
}

// PrepareInTransaction lets a caller persist a projection of the pause (for
// example ChatConfirmation) in the same database transaction.
func (service *ConfirmationResumeService) PrepareInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	input PrepareConfirmationResumeInput,
) (PreparedConfirmationResume, error) {
	if tx == nil {
		return PreparedConfirmationResume{}, ErrConfirmationResumeInvalid
	}
	input = normalizePrepareConfirmationResume(input)
	canonicalInput, _, err := canonicalConfirmationInput(input.Input)
	if err != nil || !validPrepareConfirmationResume(input) {
		return PreparedConfirmationResume{}, ErrConfirmationResumeInvalid
	}
	requestSnapshot, err := canonicalInvocationObject(input.RequestSnapshot)
	if err != nil {
		return PreparedConfirmationResume{}, ErrConfirmationResumeInvalid
	}
	resolvedSnapshot, err := canonicalInvocationObject(input.ResolvedSnapshot)
	if err != nil {
		return PreparedConfirmationResume{}, ErrConfirmationResumeInvalid
	}
	wantInputHash := boundConfirmationInputHash(
		input.Confirmation.ReleaseID, input.Confirmation.ConnectionID, canonicalInput,
	)
	if input.Confirmation.Decision.InputHash != wantInputHash {
		return PreparedConfirmationResume{}, ErrConfirmationBindingChanged
	}
	requestPrincipal, err := prepareConfirmationRequestPrincipal(
		input.Confirmation.WorkspaceID, input.Confirmation.RequestedBy,
		input.Confirmation.PrincipalSnapshot,
	)
	if err != nil {
		return PreparedConfirmationResume{}, err
	}
	snapshotPrincipal, hasSnapshotPrincipal := confirmationPrincipalFromRequest(requestSnapshot)
	principalMatches := hasSnapshotPrincipal && requestPrincipal.SameBinding(snapshotPrincipal)
	if !hasSnapshotPrincipal {
		principalMatches = confirmationActorFromRequest(requestSnapshot) == requestPrincipal.Identity.Actor.ID
	}
	if confirmationReleaseFromRequest(requestSnapshot) != input.Confirmation.ReleaseID ||
		confirmationConnectionFromRequest(requestSnapshot) != input.Confirmation.ConnectionID ||
		!principalMatches ||
		confirmationPlanFromRequest(requestSnapshot) != input.Confirmation.PlanHash {
		return PreparedConfirmationResume{}, ErrConfirmationBindingChanged
	}

	requested, err := service.confirmations.requestInTransaction(ctx, tx, input.Confirmation)
	if err != nil {
		return PreparedConfirmationResume{}, err
	}
	checkpoint := ConfirmationResumeCheckpoint{
		ConfirmationID: requested.Confirmation.ID, WorkspaceID: requested.Confirmation.WorkspaceID,
		Kind: input.Kind, RunID: requested.Confirmation.RunID,
		TargetItemID:   requested.Confirmation.TargetItemID,
		ExecutionID:    requested.Confirmation.ExecutionID,
		AgentRunStepID: input.AgentRunStepID, ExecutionStepID: input.ExecutionStepID,
		NodeID: requested.Confirmation.NodeID, Status: ResumeStatusPending,
		SnapshotSchemaVersion: input.SnapshotSchemaVersion,
		RequestSnapshot:       requestSnapshot, ResolvedSnapshot: resolvedSnapshot,
		Input: canonicalInput, InputHash: requested.Confirmation.InputHash,
		PlanHash:               requested.Confirmation.PlanHash,
		InteractionBindingHash: requested.Confirmation.InteractionBindingHash,
		TerminalOnSuccess:      input.TerminalOnSuccess,
		ResultSnapshot:         json.RawMessage(`{}`), CreatedAt: requested.Confirmation.CreatedAt,
		LockVersion: 1,
	}
	if checkpoint.RunID != "" {
		lock := input.ExpectedRunLockVersion + 1
		checkpoint.RunWaitLockVersion = &lock
	}
	if checkpoint.ExecutionID != "" {
		lock := input.ExpectedExecutionLockVersion + 1
		checkpoint.ExecutionWaitLockVersion = &lock
	}
	checkpoint, err = service.checkpoints.createInTransaction(ctx, tx, checkpoint)
	if err != nil {
		return PreparedConfirmationResume{}, err
	}
	if err := service.pauseTargets(ctx, tx, checkpoint, input); err != nil {
		return PreparedConfirmationResume{}, err
	}
	return PreparedConfirmationResume{Requested: requested, Checkpoint: checkpoint}, nil
}

func (service *ConfirmationResumeService) Resume(
	ctx context.Context,
	workspaceID, confirmationID string,
) (ConfirmationResumeResult, error) {
	workspaceID, confirmationID = strings.TrimSpace(workspaceID), strings.TrimSpace(confirmationID)
	checkpoint, err := service.checkpoints.Get(ctx, workspaceID, confirmationID)
	if err != nil {
		return ConfirmationResumeResult{}, err
	}
	if checkpoint.Status == ResumeStatusSucceeded {
		return ConfirmationResumeResult{Checkpoint: checkpoint, Result: canonicalResumeResult(checkpoint.ResultSnapshot), Cached: true}, nil
	}
	if checkpoint.Status == ResumeStatusExecuting {
		return ConfirmationResumeResult{}, ErrConfirmationResumeExecuting
	}
	if checkpoint.Status == ResumeStatusFailed || checkpoint.Status == ResumeStatusCancelled {
		return ConfirmationResumeResult{}, ErrConfirmationResumeConflict
	}
	requestPrincipal, hasRequestPrincipal := confirmationPrincipalFromRequest(checkpoint.RequestSnapshot)
	check := ConfirmationCheck{
		WorkspaceID: checkpoint.WorkspaceID, ConfirmationID: checkpoint.ConfirmationID,
		RunID: checkpoint.RunID, TargetItemID: checkpoint.TargetItemID,
		ReleaseID:    confirmationReleaseFromRequest(checkpoint.RequestSnapshot),
		ConnectionID: confirmationConnectionFromRequest(checkpoint.RequestSnapshot),
		PlanHash:     checkpoint.PlanHash, InputHash: checkpoint.InputHash,
		InteractionBindingHash: checkpoint.InteractionBindingHash,
		ActorID:                confirmationActorFromRequest(checkpoint.RequestSnapshot),
	}
	if hasRequestPrincipal {
		check.PrincipalSnapshot = &requestPrincipal
	}
	if err := service.confirmations.VerifyInvocationConfirmation(ctx, check); err != nil {
		return ConfirmationResumeResult{}, err
	}

	claimID := uuid.Must(uuid.NewV7()).String()
	claim, err := service.claim(ctx, checkpoint, claimID)
	if err != nil {
		if errors.Is(err, ErrConfirmationResumeConflict) {
			latest, getErr := service.checkpoints.Get(ctx, workspaceID, confirmationID)
			if getErr == nil && latest.Status == ResumeStatusSucceeded {
				return ConfirmationResumeResult{Checkpoint: latest, Result: canonicalResumeResult(latest.ResultSnapshot), Cached: true}, nil
			}
		}
		return ConfirmationResumeResult{}, err
	}
	checkpoint = claim.Checkpoint
	checkpoint, err = service.checkpoints.markExecuting(
		ctx, checkpoint.WorkspaceID, checkpoint.ConfirmationID, claimID, service.now().UTC(),
	)
	if err != nil {
		return ConfirmationResumeResult{}, err
	}
	executor, err := service.executors.Resolve(checkpoint.Kind)
	if err != nil {
		return service.finish(ctx, checkpoint, ResumeExecutionOutput{Result: json.RawMessage(`{}`)}, err)
	}
	output, executionErr := executor.Execute(ctx, ResumeExecutionInput{
		Checkpoint: checkpoint, ConfirmationID: checkpoint.ConfirmationID,
		RequestSnapshot:  cloneResumeJSON(checkpoint.RequestSnapshot),
		ResolvedSnapshot: cloneResumeJSON(checkpoint.ResolvedSnapshot),
		Input:            cloneResumeJSON(checkpoint.Input),
	})
	return service.finish(ctx, checkpoint, output, executionErr)
}

// GetCheckpoint returns the durable execution checkpoint for read projections.
func (service *ConfirmationResumeService) GetCheckpoint(
	ctx context.Context,
	workspaceID, confirmationID string,
) (ConfirmationResumeCheckpoint, error) {
	return service.checkpoints.Get(ctx, workspaceID, confirmationID)
}

// ExpireDue atomically expires pending confirmations with durable checkpoints,
// cancels their checkpoints, and terminates all waiting run targets.
func (service *ConfirmationResumeService) ExpireDue(
	ctx context.Context,
	limit int,
) ([]ConfirmationResumeCheckpoint, error) {
	if limit <= 0 || limit > 1000 {
		return nil, ErrConfirmationResumeInvalid
	}
	now := service.now().UTC()
	tx, err := service.checkpoints.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	due, err := service.checkpoints.listDueInTransaction(ctx, tx, now, limit)
	if err != nil {
		return nil, err
	}
	expired := make([]ConfirmationResumeCheckpoint, 0, len(due))
	for _, checkpoint := range due {
		if _, err := service.confirmations.expireInTransaction(
			ctx, tx, checkpoint.WorkspaceID, checkpoint.ConfirmationID, now,
		); err != nil {
			return nil, err
		}
		cancelled, err := service.checkpoints.cancelInTransaction(
			ctx, tx, checkpoint.WorkspaceID, checkpoint.ConfirmationID, now,
		)
		if err != nil {
			return nil, err
		}
		if err := service.finishTargets(
			ctx, tx, checkpoint, ResumeStatusCancelled,
			json.RawMessage(`{"expired":true}`), "",
		); err != nil {
			return nil, err
		}
		expired = append(expired, cancelled)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return expired, nil
}

func (service *ConfirmationResumeService) Cancel(
	ctx context.Context,
	input CancelConfirmationResumeInput,
) (ConfirmationResumeCheckpoint, error) {
	tx, err := service.checkpoints.begin(ctx)
	if err != nil {
		return ConfirmationResumeCheckpoint{}, err
	}
	defer tx.Rollback()
	cancelled, err := service.CancelInTransaction(ctx, tx, input)
	if err != nil {
		return ConfirmationResumeCheckpoint{}, err
	}
	if err := tx.Commit(); err != nil {
		return ConfirmationResumeCheckpoint{}, err
	}
	return cancelled, nil
}

// CancelInTransaction cancels the execution confirmation, checkpoint and run
// targets atomically with any caller-owned projection updates.
func (service *ConfirmationResumeService) CancelInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	input CancelConfirmationResumeInput,
) (ConfirmationResumeCheckpoint, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ConfirmationID = strings.TrimSpace(input.ConfirmationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	if tx == nil || !invocationValidUUID(input.WorkspaceID) || !invocationValidUUID(input.ConfirmationID) ||
		input.ExpectedConfirmationLockVersion <= 0 {
		return ConfirmationResumeCheckpoint{}, ErrConfirmationResumeInvalid
	}
	checkpoint, err := service.checkpoints.Get(ctx, input.WorkspaceID, input.ConfirmationID)
	if err != nil {
		return ConfirmationResumeCheckpoint{}, err
	}
	if _, err := service.confirmations.cancelInTransaction(ctx, tx, CancelExecutionConfirmationInput{
		WorkspaceID: input.WorkspaceID, ConfirmationID: input.ConfirmationID,
		ActorID: input.ActorID, PrincipalSnapshot: input.PrincipalSnapshot,
		ServiceDecisionPolicy: input.ServiceDecisionPolicy,
		ExpectedLockVersion:   input.ExpectedConfirmationLockVersion,
	}); err != nil {
		return ConfirmationResumeCheckpoint{}, err
	}
	cancelled, err := service.checkpoints.cancelInTransaction(
		ctx, tx, input.WorkspaceID, input.ConfirmationID, service.now().UTC(),
	)
	if err != nil {
		return ConfirmationResumeCheckpoint{}, err
	}
	if err := service.finishTargets(ctx, tx, checkpoint, ResumeStatusCancelled, json.RawMessage(`{"cancelled":true}`), ""); err != nil {
		return ConfirmationResumeCheckpoint{}, err
	}
	return cancelled, nil
}

func (service *ConfirmationResumeService) pauseTargets(
	ctx context.Context,
	tx *sql.Tx,
	checkpoint ConfirmationResumeCheckpoint,
	input PrepareConfirmationResumeInput,
) error {
	summary := json.RawMessage(`{"confirmationId":"` + checkpoint.ConfirmationID + `"}`)
	if checkpoint.AgentRunStepID != "" {
		if _, err := service.runs.TransitionAgentRunStepInTransaction(ctx, tx, checkpoint.WorkspaceID,
			checkpoint.AgentRunStepID, StepTransition{ExpectedStatus: "RUNNING", NewStatus: "WAITING_CONFIRMATION", OutputSummary: summary}); err != nil {
			return err
		}
	}
	if checkpoint.ExecutionStepID != "" {
		if _, err := service.runs.TransitionExecutionStepInTransaction(ctx, tx, checkpoint.WorkspaceID,
			checkpoint.ExecutionStepID, StepTransition{ExpectedStatus: "RUNNING", NewStatus: "WAITING_CONFIRMATION", OutputSummary: summary}); err != nil {
			return err
		}
	}
	if checkpoint.ExecutionID != "" {
		if _, err := service.runs.TransitionWorkflowExecutionInTransaction(ctx, tx, checkpoint.WorkspaceID,
			checkpoint.ExecutionID, RunTransition{ExpectedStatus: "RUNNING",
				ExpectedLockVersion: input.ExpectedExecutionLockVersion, NewStatus: "WAITING_CONFIRMATION", OutputSummary: summary}); err != nil {
			return err
		}
	}
	if checkpoint.RunID != "" {
		if _, err := service.runs.TransitionAgentRunInTransaction(ctx, tx, checkpoint.WorkspaceID,
			checkpoint.RunID, RunTransition{ExpectedStatus: "RUNNING",
				ExpectedLockVersion: input.ExpectedRunLockVersion, NewStatus: "WAITING_CONFIRMATION", OutputSummary: summary}); err != nil {
			return err
		}
	}
	return nil
}

func (service *ConfirmationResumeService) claim(
	ctx context.Context,
	checkpoint ConfirmationResumeCheckpoint,
	claimID string,
) (confirmationResumeClaim, error) {
	tx, err := service.checkpoints.begin(ctx)
	if err != nil {
		return confirmationResumeClaim{}, err
	}
	defer tx.Rollback()
	claim, err := service.checkpoints.claimInTransaction(
		ctx, tx, checkpoint.WorkspaceID, checkpoint.ConfirmationID,
		claimID, service.now().UTC(), service.claimLease,
	)
	if err != nil {
		return confirmationResumeClaim{}, err
	}
	if !claim.Recovered {
		if err := service.resumeTargets(ctx, tx, claim.Checkpoint); err != nil {
			return confirmationResumeClaim{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return confirmationResumeClaim{}, err
	}
	return claim, nil
}

func (service *ConfirmationResumeService) resumeTargets(
	ctx context.Context,
	tx *sql.Tx,
	checkpoint ConfirmationResumeCheckpoint,
) error {
	summary := json.RawMessage(`{"confirmationId":"` + checkpoint.ConfirmationID + `","resumed":true}`)
	if checkpoint.AgentRunStepID != "" {
		if _, err := service.runs.TransitionAgentRunStepInTransaction(ctx, tx, checkpoint.WorkspaceID,
			checkpoint.AgentRunStepID, StepTransition{ExpectedStatus: "WAITING_CONFIRMATION", NewStatus: "RUNNING", OutputSummary: summary}); err != nil {
			return err
		}
	}
	if checkpoint.ExecutionStepID != "" {
		if _, err := service.runs.TransitionExecutionStepInTransaction(ctx, tx, checkpoint.WorkspaceID,
			checkpoint.ExecutionStepID, StepTransition{ExpectedStatus: "WAITING_CONFIRMATION", NewStatus: "RUNNING", OutputSummary: summary}); err != nil {
			return err
		}
	}
	if checkpoint.ExecutionID != "" {
		if _, err := service.runs.TransitionWorkflowExecutionInTransaction(ctx, tx, checkpoint.WorkspaceID,
			checkpoint.ExecutionID, RunTransition{ExpectedStatus: "WAITING_CONFIRMATION",
				ExpectedLockVersion: *checkpoint.ExecutionWaitLockVersion, NewStatus: "RUNNING", OutputSummary: summary}); err != nil {
			return err
		}
	}
	if checkpoint.RunID != "" {
		if _, err := service.runs.TransitionAgentRunInTransaction(ctx, tx, checkpoint.WorkspaceID,
			checkpoint.RunID, RunTransition{ExpectedStatus: "WAITING_CONFIRMATION",
				ExpectedLockVersion: *checkpoint.RunWaitLockVersion, NewStatus: "RUNNING", OutputSummary: summary}); err != nil {
			return err
		}
	}
	return nil
}

func (service *ConfirmationResumeService) finish(
	ctx context.Context,
	checkpoint ConfirmationResumeCheckpoint,
	output ResumeExecutionOutput,
	executionErr error,
) (ConfirmationResumeResult, error) {
	status, errorCode := ResumeStatusSucceeded, ""
	if executionErr != nil {
		status = ResumeStatusFailed
		errorCode = ErrorCode(executionErr)
		if errorCode == "" {
			errorCode = ErrorCodeConfirmationResumeExecution
		}
	}
	result, err := canonicalInvocationObject(output.Result)
	if err != nil {
		status, errorCode = ResumeStatusFailed, ErrorCodeConfirmationResumeExecution
		result = json.RawMessage(`{}`)
		if executionErr == nil {
			executionErr = ErrConfirmationResumeInvalid
		}
	}
	tx, err := service.checkpoints.begin(context.WithoutCancel(ctx))
	if err != nil {
		return ConfirmationResumeResult{}, err
	}
	defer tx.Rollback()
	completed, err := service.checkpoints.completeInTransaction(
		context.WithoutCancel(ctx), tx, checkpoint, status, result, errorCode, service.now().UTC(),
	)
	if err != nil {
		return ConfirmationResumeResult{}, err
	}
	if err := service.finishTargets(context.WithoutCancel(ctx), tx, checkpoint, status, result, errorCode); err != nil {
		return ConfirmationResumeResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ConfirmationResumeResult{}, err
	}
	return ConfirmationResumeResult{Checkpoint: completed, Result: result}, executionErr
}

func (service *ConfirmationResumeService) finishTargets(
	ctx context.Context,
	tx *sql.Tx,
	checkpoint ConfirmationResumeCheckpoint,
	status string,
	result json.RawMessage,
	errorCode string,
) error {
	stepStatus := "SUCCEEDED"
	if status == ResumeStatusFailed {
		stepStatus = "FAILED"
	}
	if status == ResumeStatusCancelled {
		stepStatus = "CANCELLED"
	}
	stepTransition := StepTransition{
		ExpectedStatus: "RUNNING", NewStatus: stepStatus, OutputSummary: result, ErrorCode: errorCode,
	}
	if status == ResumeStatusCancelled {
		stepTransition.ExpectedStatus = "WAITING_CONFIRMATION"
	}
	if checkpoint.AgentRunStepID != "" {
		if _, err := service.runs.TransitionAgentRunStepInTransaction(
			ctx, tx, checkpoint.WorkspaceID, checkpoint.AgentRunStepID, stepTransition,
		); err != nil {
			return err
		}
	}
	if checkpoint.ExecutionStepID != "" {
		if _, err := service.runs.TransitionExecutionStepInTransaction(
			ctx, tx, checkpoint.WorkspaceID, checkpoint.ExecutionStepID, stepTransition,
		); err != nil {
			return err
		}
	}
	terminal := status == ResumeStatusFailed || status == ResumeStatusCancelled ||
		(status == ResumeStatusSucceeded && checkpoint.TerminalOnSuccess)
	if !terminal {
		return nil
	}
	runStatus := "SUCCEEDED"
	if status == ResumeStatusFailed {
		runStatus = "FAILED"
	}
	if status == ResumeStatusCancelled {
		runStatus = "CANCELLED"
	}
	expectedParentStatus := "RUNNING"
	parentLockOffset := int64(1)
	if status == ResumeStatusCancelled {
		expectedParentStatus = "WAITING_CONFIRMATION"
		parentLockOffset = 0
	}
	transition := func(waitLock int64) RunTransition {
		return RunTransition{ExpectedStatus: expectedParentStatus,
			ExpectedLockVersion: waitLock + parentLockOffset, NewStatus: runStatus,
			OutputSummary: result, ErrorCode: errorCode}
	}
	if checkpoint.ExecutionID != "" {
		if _, err := service.runs.TransitionWorkflowExecutionInTransaction(ctx, tx,
			checkpoint.WorkspaceID, checkpoint.ExecutionID, transition(*checkpoint.ExecutionWaitLockVersion)); err != nil {
			return err
		}
	}
	if checkpoint.RunID != "" {
		if _, err := service.runs.TransitionAgentRunInTransaction(ctx, tx,
			checkpoint.WorkspaceID, checkpoint.RunID, transition(*checkpoint.RunWaitLockVersion)); err != nil {
			return err
		}
	}
	return nil
}

func normalizePrepareConfirmationResume(input PrepareConfirmationResumeInput) PrepareConfirmationResumeInput {
	input.Kind = strings.ToUpper(strings.TrimSpace(input.Kind))
	input.SnapshotSchemaVersion = strings.TrimSpace(input.SnapshotSchemaVersion)
	input.AgentRunStepID = strings.TrimSpace(input.AgentRunStepID)
	input.ExecutionStepID = strings.TrimSpace(input.ExecutionStepID)
	return input
}

func validPrepareConfirmationResume(input PrepareConfirmationResumeInput) bool {
	confirmation := input.Confirmation
	if input.Kind != ResumeKindTool && input.Kind != ResumeKindWorkflow {
		return false
	}
	if input.SnapshotSchemaVersion != ConfirmationResumeSnapshotVersion ||
		len(input.RequestSnapshot) == 0 || len(input.ResolvedSnapshot) == 0 {
		return false
	}
	if confirmation.RunID != "" && input.ExpectedRunLockVersion <= 0 {
		return false
	}
	if confirmation.ExecutionID != "" && input.ExpectedExecutionLockVersion <= 0 {
		return false
	}
	return (input.AgentRunStepID == "" || invocationValidUUID(input.AgentRunStepID)) &&
		(input.ExecutionStepID == "" || invocationValidUUID(input.ExecutionStepID))
}

func confirmationReleaseFromRequest(raw json.RawMessage) string {
	var value struct {
		ReleaseID string `json:"releaseId"`
	}
	_ = json.Unmarshal(raw, &value)
	return strings.TrimSpace(value.ReleaseID)
}

func confirmationConnectionFromRequest(raw json.RawMessage) string {
	var value struct {
		ConnectionID string `json:"connectionId"`
	}
	_ = json.Unmarshal(raw, &value)
	return strings.TrimSpace(value.ConnectionID)
}

func confirmationActorFromRequest(raw json.RawMessage) string {
	var value struct {
		ActorID string `json:"actorId"`
	}
	_ = json.Unmarshal(raw, &value)
	return strings.TrimSpace(value.ActorID)
}

func confirmationPrincipalFromRequest(raw json.RawMessage) (principal.ExecutionSnapshot, bool) {
	var value struct {
		PrincipalSnapshot *principal.ExecutionSnapshot `json:"principalSnapshot"`
	}
	if json.Unmarshal(raw, &value) != nil || value.PrincipalSnapshot == nil ||
		value.PrincipalSnapshot.Validate() != nil {
		return principal.ExecutionSnapshot{}, false
	}
	return cloneExecutionSnapshot(*value.PrincipalSnapshot), true
}

func confirmationPlanFromRequest(raw json.RawMessage) string {
	var value struct {
		PlanHash string `json:"planHash"`
	}
	_ = json.Unmarshal(raw, &value)
	return strings.ToLower(strings.TrimSpace(value.PlanHash))
}

func cloneResumeJSON(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func canonicalResumeResult(value json.RawMessage) json.RawMessage {
	canonical, err := canonicalInvocationObject(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return canonical
}
