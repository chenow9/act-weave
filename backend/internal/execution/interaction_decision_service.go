package execution

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"actweave/backend/internal/principal"
)

type InteractionDecisionService struct {
	repository    *InteractionDecisionRepository
	confirmations *ConfirmationService
	resumes       *ConfirmationResumeService
	now           func() time.Time
}

func NewInteractionDecisionService(
	confirmations *ConfirmationService,
	resumes *ConfirmationResumeService,
) (*InteractionDecisionService, error) {
	if confirmations == nil || confirmations.repository == nil || resumes == nil ||
		resumes.checkpoints == nil || confirmations.repository.db != resumes.checkpoints.db {
		return nil, errors.New("interaction decision service dependencies are required")
	}
	repository, err := NewInteractionDecisionRepository(confirmations.repository.db)
	if err != nil {
		return nil, err
	}
	return &InteractionDecisionService{
		repository: repository, confirmations: confirmations, resumes: resumes,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

// DecideStored loads the pending confirmation binding from durable state and
// decides it. Used by Console production Approval (WorkflowExecution-only pauses)
// where the client holds confirmationId + lockVersion from the execute response.
func (service *InteractionDecisionService) DecideStored(
	ctx context.Context,
	workspaceID, confirmationID, actorID, decision, idempotencyKey string,
	expectedLockVersion int64,
) (InteractionDecisionResult, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	confirmationID = strings.TrimSpace(confirmationID)
	actorID = strings.TrimSpace(actorID)
	decision = strings.ToLower(strings.TrimSpace(decision))
	idempotencyKey = strings.ToLower(strings.TrimSpace(idempotencyKey))
	if !invocationValidUUID(workspaceID) || !invocationValidUUID(confirmationID) ||
		!invocationValidUUID(actorID) || !invocationValidUUID(idempotencyKey) ||
		expectedLockVersion <= 0 {
		return InteractionDecisionResult{}, ErrInteractionDecisionInvalid
	}
	record, err := service.confirmations.repository.Get(ctx, workspaceID, confirmationID)
	if err != nil {
		return InteractionDecisionResult{}, err
	}
	if expectedLockVersion != record.LockVersion {
		return InteractionDecisionResult{}, ErrInteractionDecisionBindingChanged
	}
	binding := InteractionDecisionBinding{
		RunID: record.RunID, TargetItemID: record.TargetItemID,
		ReleaseID: record.ReleaseID, InputHash: record.InputHash,
		ConnectionID: record.ConnectionID, PlanHash: record.PlanHash,
		Version: record.LockVersion, ExpiresAt: record.ExpiresAt,
		BindingHash: record.InteractionBindingHash,
	}
	return service.Decide(ctx, DecideInteractionInput{
		WorkspaceID: workspaceID, ConfirmationID: confirmationID,
		ActorID: actorID, Decision: decision, IdempotencyKey: idempotencyKey,
		Binding: binding,
	})
}

// Decide atomically accepts exactly one decision command, commits it, and only
// then attempts to wake the runtime. A committed decision is returned as a
// success even when the Tool/Workflow later fails; callers must inspect
// ResumeStatus or, at the protocol boundary, wait for the target Item terminal
// event.
func (service *InteractionDecisionService) Decide(
	ctx context.Context,
	input DecideInteractionInput,
) (InteractionDecisionResult, error) {
	tx, err := service.repository.begin(ctx)
	if err != nil {
		return InteractionDecisionResult{}, err
	}
	defer tx.Rollback()
	result, err := service.DecideInTransaction(ctx, tx, input)
	if err != nil {
		return InteractionDecisionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return InteractionDecisionResult{}, err
	}
	return service.Dispatch(context.WithoutCancel(ctx), result), nil
}

// DecideInTransaction lets Chat and the AAP protocol append their projection
// and interaction.resolved event in the same transaction as the command CAS.
// The caller must invoke Dispatch only after a successful commit.
func (service *InteractionDecisionService) DecideInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	input DecideInteractionInput,
) (InteractionDecisionResult, error) {
	input = normalizeInteractionDecision(input)
	if tx == nil || !validInteractionDecision(input) {
		return InteractionDecisionResult{}, ErrInteractionDecisionInvalid
	}
	decisionPrincipal, err := prepareConfirmationDecisionPrincipal(
		input.WorkspaceID, input.ActorID, input.PrincipalSnapshot,
	)
	if err != nil {
		return InteractionDecisionResult{}, err
	}
	policySnapshot, err := buildConfirmationDecisionPolicySnapshot(
		decisionPrincipal, input.ServiceDecisionPolicy,
	)
	if err != nil {
		return InteractionDecisionResult{}, err
	}
	principalHash, err := interactionDecisionPrincipalHash(decisionPrincipal)
	if err != nil {
		return InteractionDecisionResult{}, err
	}
	requestHash, err := interactionDecisionRequestHash(input, decisionPrincipal)
	if err != nil {
		return InteractionDecisionResult{}, err
	}

	record, err := service.confirmations.repository.getRecordForUpdate(
		ctx, tx, input.WorkspaceID, input.ConfirmationID,
	)
	if err != nil {
		return InteractionDecisionResult{}, err
	}
	checkpoint, err := service.resumes.checkpoints.getForUpdate(
		ctx, tx, input.WorkspaceID, input.ConfirmationID,
	)
	if err != nil {
		return InteractionDecisionResult{}, err
	}
	command, commandErr := service.repository.get(
		ctx, tx, input.WorkspaceID, input.ConfirmationID, principalHash, input.IdempotencyKey,
	)
	if commandErr == nil {
		if command.RequestHash != requestHash || command.Decision != input.Decision {
			return InteractionDecisionResult{}, ErrInteractionIdempotencyConflict
		}
		if record.Status != command.ConfirmationStatus ||
			record.LockVersion != command.ConfirmationVersion {
			return InteractionDecisionResult{}, ErrInteractionDecisionBindingChanged
		}
		return InteractionDecisionResult{
			Confirmation: record.ExecutionConfirmation, Checkpoint: checkpoint,
			Decision: command.Decision, Cached: true, ResumeStatus: checkpoint.Status,
		}, nil
	}
	if !errors.Is(commandErr, sql.ErrNoRows) {
		return InteractionDecisionResult{}, commandErr
	}
	if record.Status != ConfirmationStatusPending || checkpoint.Status != ResumeStatusPending {
		return InteractionDecisionResult{}, ErrInteractionAlreadyResolved
	}
	now := service.now().UTC()
	if !record.ExpiresAt.After(now) {
		return InteractionDecisionResult{}, ErrConfirmationExpired
	}
	if !record.RequestPrincipalSnapshot.SameDecisionPrincipal(decisionPrincipal) {
		return InteractionDecisionResult{}, ErrConfirmationRequesterMismatch
	}
	if !interactionDecisionBindingMatches(input.Binding, record.ExecutionConfirmation, checkpoint) {
		return InteractionDecisionResult{}, ErrInteractionDecisionBindingChanged
	}

	mutation := confirmationMutationBinding{
		WorkspaceID: input.WorkspaceID, ConfirmationID: input.ConfirmationID,
		ActorID: input.ActorID, PrincipalSnapshot: decisionPrincipal,
		DecisionPolicySnapshot: policySnapshot, ResumeTokenHash: record.ResumeTokenHash,
		RunID: record.RunID, TargetItemID: record.TargetItemID,
		ReleaseID: record.ReleaseID, InputHash: record.InputHash,
		ConnectionID: record.ConnectionID, PlanHash: record.PlanHash,
		InteractionBindingHash: record.InteractionBindingHash,
		ExpiresAt:              record.ExpiresAt, ExpectedLockVersion: input.Binding.Version, Now: now,
	}
	var decided ExecutionConfirmation
	if input.Decision == InteractionDecisionApprove {
		decided, err = service.confirmations.repository.confirmPreparedWith(ctx, tx, mutation)
	} else {
		decided, err = service.confirmations.repository.cancelWith(ctx, tx, mutation)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return InteractionDecisionResult{}, ErrInteractionAlreadyResolved
	}
	if err != nil {
		return InteractionDecisionResult{}, err
	}
	if input.Decision != InteractionDecisionApprove {
		checkpoint, err = service.resumes.checkpoints.cancelInTransaction(
			ctx, tx, input.WorkspaceID, input.ConfirmationID, now,
		)
		if err != nil {
			return InteractionDecisionResult{}, err
		}
		if err := service.resumes.finishTargets(
			ctx, tx, checkpoint, ResumeStatusCancelled,
			json.RawMessage(`{"cancelled":true}`), "",
		); err != nil {
			return InteractionDecisionResult{}, err
		}
	}
	command = interactionDecisionCommand{
		WorkspaceID: input.WorkspaceID, ConfirmationID: input.ConfirmationID,
		PrincipalBindingHash: principalHash, IdempotencyKey: input.IdempotencyKey,
		RequestHash: requestHash, Decision: input.Decision,
		ExpectedVersion: input.Binding.Version, ConfirmationStatus: decided.Status,
		ConfirmationVersion: decided.LockVersion, CreatedAt: now,
	}
	if err := service.repository.create(ctx, tx, command); err != nil {
		return InteractionDecisionResult{}, err
	}
	return InteractionDecisionResult{
		Confirmation: decided, Checkpoint: checkpoint, Decision: input.Decision,
		ResumeStatus: checkpoint.Status,
	}, nil
}

// Dispatch is intentionally post-commit and idempotent. The checkpoint claim
// is the side-effect CAS, so repeated command delivery may safely call it.
func (service *InteractionDecisionService) Dispatch(
	ctx context.Context,
	result InteractionDecisionResult,
) InteractionDecisionResult {
	if service == nil || result.Decision != InteractionDecisionApprove {
		return result
	}
	resumed, err := service.resumes.Resume(
		ctx, result.Confirmation.WorkspaceID, result.Confirmation.ID,
	)
	if resumed.Checkpoint.ConfirmationID != "" {
		result.Checkpoint = resumed.Checkpoint
		result.ResumeStatus = resumed.Checkpoint.Status
	}
	if err != nil {
		result.DispatchError = err
		if latest, getErr := service.resumes.GetCheckpoint(
			ctx, result.Confirmation.WorkspaceID, result.Confirmation.ID,
		); getErr == nil {
			result.Checkpoint = latest
			result.ResumeStatus = latest.Status
		}
	}
	return result
}

func normalizeInteractionDecision(input DecideInteractionInput) DecideInteractionInput {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ConfirmationID = strings.TrimSpace(input.ConfirmationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.Decision = strings.ToLower(strings.TrimSpace(input.Decision))
	input.IdempotencyKey = strings.ToLower(strings.TrimSpace(input.IdempotencyKey))
	input.Binding.RunID = strings.TrimSpace(input.Binding.RunID)
	input.Binding.TargetItemID = strings.TrimSpace(input.Binding.TargetItemID)
	input.Binding.ReleaseID = strings.TrimSpace(input.Binding.ReleaseID)
	input.Binding.InputHash = strings.ToLower(strings.TrimSpace(input.Binding.InputHash))
	input.Binding.ConnectionID = strings.TrimSpace(input.Binding.ConnectionID)
	input.Binding.PlanHash = strings.ToLower(strings.TrimSpace(input.Binding.PlanHash))
	input.Binding.BindingHash = strings.ToLower(strings.TrimSpace(input.Binding.BindingHash))
	input.Binding.ExpiresAt = input.Binding.ExpiresAt.UTC()
	return input
}

func validInteractionDecision(input DecideInteractionInput) bool {
	return invocationValidUUID(input.WorkspaceID) && invocationValidUUID(input.ConfirmationID) &&
		invocationValidUUID(input.IdempotencyKey) &&
		(input.Decision == InteractionDecisionApprove || input.Decision == InteractionDecisionDecline ||
			input.Decision == InteractionDecisionCancel) &&
		// RunID may be empty for production WorkflowExecution-only Approval pauses.
		(input.Binding.RunID == "" || invocationValidUUID(input.Binding.RunID)) &&
		invocationValidUUID(input.Binding.TargetItemID) &&
		invocationValidUUID(input.Binding.ReleaseID) && validConfirmationHash(input.Binding.InputHash) &&
		(input.Binding.ConnectionID == "" || invocationValidUUID(input.Binding.ConnectionID)) &&
		(input.Binding.PlanHash == "" || validConfirmationHash(input.Binding.PlanHash)) &&
		validConfirmationHash(input.Binding.BindingHash) && input.Binding.Version > 0 &&
		!input.Binding.ExpiresAt.IsZero()
}

func interactionDecisionBindingMatches(
	binding InteractionDecisionBinding,
	confirmation ExecutionConfirmation,
	checkpoint ConfirmationResumeCheckpoint,
) bool {
	if verifyInteractionBinding(confirmation) != nil ||
		binding.RunID != confirmation.RunID || binding.TargetItemID != confirmation.TargetItemID ||
		binding.ReleaseID != confirmation.ReleaseID || binding.InputHash != confirmation.InputHash ||
		binding.ConnectionID != confirmation.ConnectionID || binding.PlanHash != confirmation.PlanHash ||
		binding.BindingHash != confirmation.InteractionBindingHash ||
		binding.Version != confirmation.LockVersion || !binding.ExpiresAt.Equal(confirmation.ExpiresAt) {
		return false
	}
	return checkpoint.RunID == confirmation.RunID && checkpoint.TargetItemID == confirmation.TargetItemID &&
		checkpoint.InputHash == confirmation.InputHash && checkpoint.PlanHash == confirmation.PlanHash &&
		checkpoint.InteractionBindingHash == confirmation.InteractionBindingHash
}

func interactionDecisionPrincipalHash(value principal.ExecutionSnapshot) (string, error) {
	type stablePrincipal struct {
		ActorType   string `json:"actorType"`
		ActorID     string `json:"actorId"`
		SubjectType string `json:"subjectType,omitempty"`
		SubjectID   string `json:"subjectId,omitempty"`
		ClientID    string `json:"clientId,omitempty"`
		GrantID     string `json:"grantId,omitempty"`
	}
	if value.Validate() != nil {
		return "", ErrInteractionDecisionInvalid
	}
	stable := stablePrincipal{ActorType: string(value.Identity.Actor.Type), ActorID: value.Identity.Actor.ID,
		ClientID: value.ClientID, GrantID: value.GrantID}
	if value.Identity.Subject != nil {
		stable.SubjectType, stable.SubjectID = string(value.Identity.Subject.Type), value.Identity.Subject.ID
	}
	return hashInteractionDecisionValue(stable)
}

func interactionDecisionRequestHash(
	input DecideInteractionInput,
	value principal.ExecutionSnapshot,
) (string, error) {
	type request struct {
		WorkspaceID        string                     `json:"workspaceId"`
		ConfirmationID     string                     `json:"confirmationId"`
		Decision           string                     `json:"decision"`
		IdempotencyKey     string                     `json:"idempotencyKey"`
		Binding            InteractionDecisionBinding `json:"binding"`
		PrincipalBinding   string                     `json:"principalBinding"`
		GrantVersion       int64                      `json:"grantVersion,omitempty"`
		AgentPolicyVersion int64                      `json:"agentPolicyVersion,omitempty"`
	}
	principalHash, err := interactionDecisionPrincipalHash(value)
	if err != nil {
		return "", err
	}
	return hashInteractionDecisionValue(request{
		WorkspaceID: input.WorkspaceID, ConfirmationID: input.ConfirmationID,
		Decision: input.Decision, IdempotencyKey: input.IdempotencyKey,
		Binding: input.Binding, PrincipalBinding: principalHash,
		GrantVersion: value.GrantVersion, AgentPolicyVersion: value.AgentPolicyVersion,
	})
}

func hashInteractionDecisionValue(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", ErrInteractionDecisionInvalid
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
