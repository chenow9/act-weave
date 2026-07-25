package execution

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"actweave/backend/internal/principal"

	"github.com/google/uuid"
)

type ConfirmationServiceOption func(*ConfirmationService) error

type ConfirmationService struct {
	repository     *ConfirmationRepository
	now            func() time.Time
	newResumeToken func() (string, error)
}

func NewConfirmationService(
	repository *ConfirmationRepository,
	options ...ConfirmationServiceOption,
) (*ConfirmationService, error) {
	if repository == nil {
		return nil, errors.New("confirmation repository is required")
	}
	service := &ConfirmationService{
		repository: repository,
		now:        func() time.Time { return time.Now().UTC() },
		newResumeToken: func() (string, error) {
			buffer := make([]byte, 32)
			if _, err := rand.Read(buffer); err != nil {
				return "", err
			}
			return base64.RawURLEncoding.EncodeToString(buffer), nil
		},
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("confirmation service option cannot be nil")
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func WithConfirmationClock(clock func() time.Time) ConfirmationServiceOption {
	return func(service *ConfirmationService) error {
		if clock == nil {
			return errors.New("confirmation clock is required")
		}
		service.now = clock
		return nil
	}
}

func WithConfirmationTokenSource(source func() (string, error)) ConfirmationServiceOption {
	return func(service *ConfirmationService) error {
		if source == nil {
			return errors.New("confirmation token source is required")
		}
		service.newResumeToken = source
		return nil
	}
}

func (service *ConfirmationService) Request(
	ctx context.Context,
	input RequestExecutionConfirmationInput,
) (RequestedExecutionConfirmation, error) {
	request, token, err := service.buildRequest(input)
	if err != nil {
		return RequestedExecutionConfirmation{}, err
	}
	confirmation, err := service.repository.create(ctx, request)
	if err != nil {
		return RequestedExecutionConfirmation{}, err
	}
	return RequestedExecutionConfirmation{Confirmation: confirmation, ResumeToken: token}, nil
}

func (service *ConfirmationService) requestInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	input RequestExecutionConfirmationInput,
) (RequestedExecutionConfirmation, error) {
	if tx == nil {
		return RequestedExecutionConfirmation{}, ErrConfirmationInvalid
	}
	request, token, err := service.buildRequest(input)
	if err != nil {
		return RequestedExecutionConfirmation{}, err
	}
	confirmation, err := service.repository.createWith(ctx, tx, request)
	if err != nil {
		return RequestedExecutionConfirmation{}, err
	}
	return RequestedExecutionConfirmation{Confirmation: confirmation, ResumeToken: token}, nil
}

func (service *ConfirmationService) buildRequest(
	input RequestExecutionConfirmationInput,
) (newExecutionConfirmation, string, error) {
	input = normalizeConfirmationRequest(input)
	requestPrincipal, err := prepareConfirmationRequestPrincipal(
		input.WorkspaceID, input.RequestedBy, input.PrincipalSnapshot,
	)
	if err != nil {
		return newExecutionConfirmation{}, "", err
	}
	input.PrincipalSnapshot = &requestPrincipal
	if !validConfirmationRequest(input) {
		return newExecutionConfirmation{}, "", ErrConfirmationInvalid
	}
	if err := validateConfirmationDecisionBinding(input); err != nil {
		return newExecutionConfirmation{}, "", err
	}
	resumeToken, err := service.newResumeToken()
	if err != nil || len(resumeToken) < 32 {
		return newExecutionConfirmation{}, "", ErrConfirmationTokenInvalid
	}
	createdAt := service.now().UTC()
	if createdAt.IsZero() {
		return newExecutionConfirmation{}, "", ErrConfirmationInvalid
	}
	resumeHash := sha256.Sum256([]byte(resumeToken))
	expiresAt := createdAt.Add(input.Decision.ExpiresIn)
	bindingHash, err := interactionBindingHash(
		input.WorkspaceID, input.RunID, input.TargetItemID, input.ReleaseID,
		input.Decision.InputHash, input.ConnectionID, input.PlanHash,
		requestPrincipal, 1, expiresAt,
	)
	if err != nil {
		return newExecutionConfirmation{}, "", err
	}
	request := newExecutionConfirmation{
		ExecutionConfirmation: ExecutionConfirmation{
			ID: input.ID, WorkspaceID: input.WorkspaceID, ExecutionID: input.ExecutionID,
			RunID: input.RunID, TargetItemID: input.TargetItemID,
			NodeID: input.NodeID, Status: ConfirmationStatusPending,
			Reason: input.Decision.Reason, RiskReasons: append([]string(nil), input.Decision.RiskReasons...),
			ScopeSnapshot: append(json.RawMessage(nil), input.Decision.ScopeSnapshot...),
			ReleaseID:     input.ReleaseID, InputHash: input.Decision.InputHash,
			ConnectionID: input.ConnectionID, PlanHash: input.PlanHash,
			InteractionBindingHash:          bindingHash,
			RequestedBy:                     principalUserID(requestPrincipal),
			RequestPrincipalSnapshotVersion: principal.ExecutionAuthorizationSpecV1,
			RequestPrincipalSnapshot:        requestPrincipal, CreatedAt: createdAt,
			ExpiresAt: expiresAt, LockVersion: 1,
		},
		ResumeTokenHash: hex.EncodeToString(resumeHash[:]),
	}
	return request, resumeToken, nil
}

func (service *ConfirmationService) Confirm(
	ctx context.Context,
	input ConfirmExecutionConfirmationInput,
) (ExecutionConfirmation, error) {
	input = normalizeConfirmExecutionConfirmation(input)
	if !validConfirmExecutionConfirmation(input) {
		return ExecutionConfirmation{}, ErrConfirmationInvalid
	}
	decisionPrincipal, err := prepareConfirmationDecisionPrincipal(
		input.WorkspaceID, input.ActorID, input.PrincipalSnapshot,
	)
	if err != nil {
		return ExecutionConfirmation{}, err
	}
	policySnapshot, err := buildConfirmationDecisionPolicySnapshot(
		decisionPrincipal, input.ServiceDecisionPolicy,
	)
	if err != nil {
		return ExecutionConfirmation{}, err
	}
	canonical, _, err := canonicalConfirmationInput(input.Input)
	if err != nil {
		return ExecutionConfirmation{}, err
	}
	resumeHash := sha256.Sum256([]byte(input.ResumeToken))
	binding := confirmationMutationBinding{
		WorkspaceID: input.WorkspaceID, ConfirmationID: input.ConfirmationID,
		ActorID: input.ActorID, PrincipalSnapshot: decisionPrincipal,
		DecisionPolicySnapshot: policySnapshot,
		ResumeTokenHash:        hex.EncodeToString(resumeHash[:]),
		ReleaseID:              input.ReleaseID, ConnectionID: input.ConnectionID, PlanHash: input.PlanHash,
		InputHash: boundConfirmationInputHash(input.ReleaseID, input.ConnectionID, canonical),
		RunID:     input.RunID, TargetItemID: input.TargetItemID,
		ExpectedLockVersion: input.ExpectedLockVersion, Now: service.now().UTC(),
	}
	return service.repository.confirm(ctx, binding)
}

// ConfirmPreparedInTransaction confirms the exact immutable checkpoint that
// was created by ConfirmationResumeService.PrepareInTransaction. It exists for
// transactional projections such as ChatConfirmation; generic callers should
// continue to use Confirm, which recomputes all bindings from their input.
func (service *ConfirmationService) ConfirmPreparedInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	input ConfirmPreparedExecutionInput,
) (ExecutionConfirmation, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ConfirmationID = strings.TrimSpace(input.ConfirmationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.RunID = strings.TrimSpace(input.RunID)
	input.TargetItemID = strings.TrimSpace(input.TargetItemID)
	input.ReleaseID = strings.TrimSpace(input.ReleaseID)
	input.InputHash = strings.ToLower(strings.TrimSpace(input.InputHash))
	input.ConnectionID = strings.TrimSpace(input.ConnectionID)
	input.PlanHash = strings.ToLower(strings.TrimSpace(input.PlanHash))
	input.InteractionBindingHash = strings.ToLower(strings.TrimSpace(input.InteractionBindingHash))
	decisionPrincipal, principalErr := prepareConfirmationDecisionPrincipal(
		input.WorkspaceID, input.ActorID, input.PrincipalSnapshot,
	)
	policySnapshot, policyErr := buildConfirmationDecisionPolicySnapshot(
		decisionPrincipal, input.ServiceDecisionPolicy,
	)
	if tx == nil || principalErr != nil || policyErr != nil || !invocationValidUUID(input.WorkspaceID) ||
		!invocationValidUUID(input.ConfirmationID) || !invocationValidUUID(input.RunID) ||
		!invocationValidUUID(input.TargetItemID) || !invocationValidUUID(input.ReleaseID) ||
		!validConfirmationHash(input.InputHash) ||
		(input.ConnectionID != "" && !invocationValidUUID(input.ConnectionID)) ||
		(input.PlanHash != "" && !validConfirmationHash(input.PlanHash)) ||
		!validConfirmationHash(input.InteractionBindingHash) || input.ExpiresAt.IsZero() ||
		len(input.ResumeToken) < 32 || input.ExpectedLockVersion <= 0 {
		return ExecutionConfirmation{}, ErrConfirmationInvalid
	}
	resumeHash := sha256.Sum256([]byte(input.ResumeToken))
	binding := confirmationMutationBinding{
		WorkspaceID: input.WorkspaceID, ConfirmationID: input.ConfirmationID,
		ActorID: input.ActorID, PrincipalSnapshot: decisionPrincipal,
		DecisionPolicySnapshot: policySnapshot,
		ResumeTokenHash:        hex.EncodeToString(resumeHash[:]),
		RunID:                  input.RunID, TargetItemID: input.TargetItemID,
		ReleaseID: input.ReleaseID, InputHash: input.InputHash,
		ConnectionID: input.ConnectionID, PlanHash: input.PlanHash,
		InteractionBindingHash: input.InteractionBindingHash,
		ExpiresAt:              input.ExpiresAt,
		ExpectedLockVersion:    input.ExpectedLockVersion, Now: service.now().UTC(),
	}
	confirmation, err := service.repository.confirmPreparedWith(ctx, tx, binding)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutionConfirmation{}, ErrConfirmationConflict
	}
	return confirmation, err
}

func (service *ConfirmationService) Cancel(
	ctx context.Context,
	input CancelExecutionConfirmationInput,
) (ExecutionConfirmation, error) {
	binding, err := service.cancelBinding(input)
	if err != nil {
		return ExecutionConfirmation{}, err
	}
	return service.repository.cancel(ctx, binding)
}

func (service *ConfirmationService) cancelInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	input CancelExecutionConfirmationInput,
) (ExecutionConfirmation, error) {
	if tx == nil {
		return ExecutionConfirmation{}, ErrConfirmationInvalid
	}
	binding, err := service.cancelBinding(input)
	if err != nil {
		return ExecutionConfirmation{}, err
	}
	value, err := service.repository.cancelWith(ctx, tx, binding)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutionConfirmation{}, ErrConfirmationConflict
	}
	return value, err
}

func (service *ConfirmationService) cancelBinding(
	input CancelExecutionConfirmationInput,
) (confirmationMutationBinding, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ConfirmationID = strings.TrimSpace(input.ConfirmationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	decisionPrincipal, err := prepareConfirmationDecisionPrincipal(
		input.WorkspaceID, input.ActorID, input.PrincipalSnapshot,
	)
	if err != nil {
		return confirmationMutationBinding{}, err
	}
	policySnapshot, err := buildConfirmationDecisionPolicySnapshot(
		decisionPrincipal, input.ServiceDecisionPolicy,
	)
	if err != nil {
		return confirmationMutationBinding{}, err
	}
	if !invocationValidUUID(input.WorkspaceID) || !invocationValidUUID(input.ConfirmationID) ||
		input.ExpectedLockVersion <= 0 {
		return confirmationMutationBinding{}, ErrConfirmationInvalid
	}
	return confirmationMutationBinding{
		WorkspaceID: input.WorkspaceID, ConfirmationID: input.ConfirmationID,
		ActorID: input.ActorID, PrincipalSnapshot: decisionPrincipal,
		DecisionPolicySnapshot: policySnapshot, ExpectedLockVersion: input.ExpectedLockVersion,
		Now: service.now().UTC(),
	}, nil
}

func (service *ConfirmationService) ExpireDue(
	ctx context.Context,
	limit int,
) ([]ExecutionConfirmation, error) {
	return service.repository.ExpireDue(ctx, service.now().UTC(), limit)
}

func (service *ConfirmationService) Get(
	ctx context.Context,
	workspaceID, confirmationID string,
) (ExecutionConfirmation, error) {
	return service.repository.Get(ctx, workspaceID, confirmationID)
}

func (service *ConfirmationService) expireInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, confirmationID string,
	now time.Time,
) (ExecutionConfirmation, error) {
	if tx == nil || !invocationValidUUID(workspaceID) ||
		!invocationValidUUID(confirmationID) || now.IsZero() {
		return ExecutionConfirmation{}, ErrConfirmationInvalid
	}
	value, err := service.repository.expireWith(
		ctx, tx, workspaceID, confirmationID, now.UTC(),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutionConfirmation{}, ErrConfirmationConflict
	}
	return value, err
}

// VerifyInvocationConfirmation implements ConfirmationVerifier for a resumed
// invocation. A confirmation is usable only by its requester and only while
// the exact Release/Input/Connection binding remains current.
func (service *ConfirmationService) VerifyInvocationConfirmation(
	ctx context.Context,
	check ConfirmationCheck,
) error {
	return service.repository.VerifyConfirmed(ctx, check, service.now().UTC())
}

func normalizeConfirmationRequest(input RequestExecutionConfirmationInput) RequestExecutionConfirmationInput {
	input.ID = strings.TrimSpace(input.ID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ExecutionID = strings.TrimSpace(input.ExecutionID)
	input.RunID = strings.TrimSpace(input.RunID)
	input.TargetItemID = strings.TrimSpace(input.TargetItemID)
	input.NodeID = strings.TrimSpace(input.NodeID)
	input.ReleaseID = strings.TrimSpace(input.ReleaseID)
	input.ConnectionID = strings.TrimSpace(input.ConnectionID)
	input.PlanHash = strings.ToLower(strings.TrimSpace(input.PlanHash))
	input.RequestedBy = strings.TrimSpace(input.RequestedBy)
	return input
}

func validConfirmationRequest(input RequestExecutionConfirmationInput) bool {
	for _, value := range []string{input.ID, input.WorkspaceID, input.ReleaseID} {
		if !invocationValidUUID(value) {
			return false
		}
	}
	// At least one parent target: AgentRun and/or WorkflowExecution.
	// Console production :execute pauses on WorkflowExecution without an AgentRun.
	if (input.RunID == "" && input.ExecutionID == "") || !invocationValidUUID(input.TargetItemID) {
		return false
	}
	for _, value := range []string{input.ExecutionID, input.RunID, input.ConnectionID} {
		if value != "" && !invocationValidUUID(value) {
			return false
		}
	}
	return input.PrincipalSnapshot != nil && input.PrincipalSnapshot.Validate() == nil &&
		input.PrincipalSnapshot.Identity.Actor.WorkspaceID == input.WorkspaceID &&
		input.NodeID != "" && (input.PlanHash == "" || validConfirmationHash(input.PlanHash))
}

func validateConfirmationDecisionBinding(input RequestExecutionConfirmationInput) error {
	decision := input.Decision
	if !decision.RequiresConfirmation || decision.Reason == "" ||
		len(decision.RiskReasons) == 0 || !validConfirmationHash(decision.InputHash) ||
		decision.ExpiresIn < time.Minute || decision.ExpiresIn > 24*time.Hour ||
		!json.Valid(decision.ScopeSnapshot) {
		return ErrConfirmationInvalid
	}
	var snapshot confirmationScopeSnapshot
	if err := json.Unmarshal(decision.ScopeSnapshot, &snapshot); err != nil ||
		snapshot.SchemaVersion != ConfirmationDecisionSchemaVersion ||
		snapshot.RulesVersion != ConfirmationRulesVersion ||
		snapshot.Release.ID != input.ReleaseID || snapshot.Connection.ID != input.ConnectionID ||
		snapshot.Input.BoundSHA256 != decision.InputHash ||
		snapshot.Decision.RequiresConfirmation != decision.RequiresConfirmation ||
		snapshot.Decision.Mandatory != decision.Mandatory {
		return ErrConfirmationBindingChanged
	}
	return nil
}

func normalizeConfirmExecutionConfirmation(input ConfirmExecutionConfirmationInput) ConfirmExecutionConfirmationInput {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ConfirmationID = strings.TrimSpace(input.ConfirmationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.ResumeToken = strings.TrimSpace(input.ResumeToken)
	input.RunID = strings.TrimSpace(input.RunID)
	input.ReleaseID = strings.TrimSpace(input.ReleaseID)
	input.ConnectionID = strings.TrimSpace(input.ConnectionID)
	input.PlanHash = strings.ToLower(strings.TrimSpace(input.PlanHash))
	input.TargetItemID = strings.TrimSpace(input.TargetItemID)
	return input
}

func validConfirmExecutionConfirmation(input ConfirmExecutionConfirmationInput) bool {
	if !invocationValidUUID(input.WorkspaceID) || !invocationValidUUID(input.ConfirmationID) ||
		!invocationValidUUID(input.RunID) || !invocationValidUUID(input.ReleaseID) ||
		(input.ConnectionID != "" && !invocationValidUUID(input.ConnectionID)) ||
		(input.PlanHash != "" && !validConfirmationHash(input.PlanHash)) ||
		!invocationValidUUID(input.TargetItemID) {
		return false
	}
	return len(input.ResumeToken) >= 32 && input.ExpectedLockVersion > 0
}

func principalUserID(value principal.ExecutionSnapshot) string {
	if value.Identity.Actor.Type == principal.TypeUser {
		return value.Identity.Actor.ID
	}
	return ""
}

func validConfirmationHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func NewConfirmationID() string {
	return uuid.Must(uuid.NewV7()).String()
}
