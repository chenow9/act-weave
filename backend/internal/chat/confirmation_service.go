package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"
)

const maxChatConfirmationSummaryBytes = 16 << 10

type ConfirmationService struct {
	repository    *Repository
	confirmations *execution.ConfirmationService
	resumes       *execution.ConfirmationResumeService
	decisions     *execution.InteractionDecisionService
	audit         AuditSink
}

type ConfirmationServiceOption func(*ConfirmationService) error

func WithConfirmationAuditSink(sink AuditSink) ConfirmationServiceOption {
	return func(service *ConfirmationService) error {
		if sink == nil {
			return ErrInvalid
		}
		service.audit = sink
		return nil
	}
}

func NewConfirmationService(
	repository *Repository,
	confirmations *execution.ConfirmationService,
	resumes *execution.ConfirmationResumeService,
	options ...ConfirmationServiceOption,
) (*ConfirmationService, error) {
	if repository == nil || confirmations == nil || resumes == nil {
		return nil, errors.New("chat confirmation dependencies are required")
	}
	decisions, err := execution.NewInteractionDecisionService(confirmations, resumes)
	if err != nil {
		return nil, err
	}
	service := &ConfirmationService{
		repository: repository, confirmations: confirmations, resumes: resumes,
		decisions: decisions,
	}
	for _, option := range options {
		if option == nil {
			return nil, ErrInvalid
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (service *ConfirmationService) Prepare(
	ctx context.Context,
	input PrepareChatConfirmationInput,
) (PreparedChatConfirmation, error) {
	input = normalizePrepareChatConfirmation(input)
	summary, err := canonicalChatConfirmationSummary(input.InputSummary)
	if err != nil || !validPrepareChatConfirmation(input) {
		return PreparedChatConfirmation{}, ErrInvalid
	}
	input.InputSummary = summary
	tx, err := service.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return PreparedChatConfirmation{}, err
	}
	defer tx.Rollback()
	prepared, err := service.resumes.PrepareInTransaction(ctx, tx, input.Resume)
	if err != nil {
		return PreparedChatConfirmation{}, err
	}
	executionConfirmation := prepared.Requested.Confirmation
	value, err := service.repository.createConfirmationInTransaction(ctx, tx, ChatConfirmation{
		ID: input.ID, WorkspaceID: input.WorkspaceID, SessionID: input.SessionID,
		RunID:                   executionConfirmation.RunID,
		ExecutionConfirmationID: executionConfirmation.ID,
		TargetType:              input.TargetType, TargetReleaseID: executionConfirmation.ReleaseID,
		RiskLevel: input.RiskLevel, RiskReasons: append([]string(nil), input.RiskReasons...),
		InputSummary: input.InputSummary, Status: executionConfirmation.Status,
		CreatedAt: executionConfirmation.CreatedAt,
	})
	if err != nil {
		return PreparedChatConfirmation{}, err
	}
	if err := service.repository.markMessagePendingConfirmationInTransaction(
		ctx, tx, input.WorkspaceID, input.SessionID, input.MessageID,
		executionConfirmation.RunID, input.ID,
	); err != nil {
		return PreparedChatConfirmation{}, err
	}
	if _, err := service.repository.setPendingConfirmationInTransaction(
		ctx, tx, input.WorkspaceID, input.SessionID, executionConfirmation.RunID,
		input.ID, input.ExpectedSessionLockVersion,
	); err != nil {
		return PreparedChatConfirmation{}, err
	}
	if service.audit != nil {
		eventID, err := newChatAuditEventID()
		if err != nil {
			return PreparedChatConfirmation{}, err
		}
		if err := service.audit.AppendChatAuditEvent(ctx, tx, AuditEvent{
			EventID: eventID, WorkspaceID: input.WorkspaceID,
			ActorType: string(executionConfirmation.RequestPrincipalSnapshot.Identity.Actor.Type),
			ActorID:   executionConfirmation.RequestPrincipalSnapshot.Identity.Actor.ID,
			Action:    "execution.confirmation.requested", ResourceType: "EXECUTION_CONFIRMATION",
			ResourceID: executionConfirmation.ID, Result: "SUCCESS",
			Metadata: map[string]any{
				"chatConfirmationId": input.ID, "sessionId": input.SessionID,
				"runId": executionConfirmation.RunID, "releaseId": executionConfirmation.ReleaseID,
				"riskLevel": input.RiskLevel, "targetType": input.TargetType,
			},
		}); err != nil {
			return PreparedChatConfirmation{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return PreparedChatConfirmation{}, err
	}
	return PreparedChatConfirmation{Confirmation: value, Prepared: prepared}, nil
}

func (service *ConfirmationService) Confirm(
	ctx context.Context,
	input ConfirmChatConfirmationInput,
) (ConfirmedChatConfirmation, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ConfirmationID = strings.TrimSpace(input.ConfirmationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.IdempotencyKey = strings.ToLower(strings.TrimSpace(input.IdempotencyKey))
	decisionPrincipal, principalErr := chatConfirmationDecisionPrincipal(
		input.WorkspaceID, input.ActorID, input.PrincipalSnapshot,
	)
	if !validUUID(input.WorkspaceID) || !validUUID(input.ConfirmationID) ||
		!validUUID(input.IdempotencyKey) || principalErr != nil ||
		input.ExpectedExecutionLockVersion <= 0 {
		return ConfirmedChatConfirmation{}, ErrInvalid
	}

	tx, err := service.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return ConfirmedChatConfirmation{}, err
	}
	defer tx.Rollback()
	value, err := service.repository.getConfirmationForUpdate(
		ctx, tx, input.WorkspaceID, input.ConfirmationID,
	)
	if err != nil {
		return ConfirmedChatConfirmation{}, err
	}
	binding := chatInteractionDecisionBinding(value)
	if input.Binding != nil {
		binding = *input.Binding
	}
	binding.Version = input.ExpectedExecutionLockVersion
	decision, err := service.decisions.DecideInTransaction(ctx, tx, execution.DecideInteractionInput{
		WorkspaceID: input.WorkspaceID, ConfirmationID: value.ExecutionConfirmationID,
		ActorID: input.ActorID, PrincipalSnapshot: &decisionPrincipal,
		ServiceDecisionPolicy: input.ServiceDecisionPolicy,
		Decision:              execution.InteractionDecisionApprove,
		IdempotencyKey:        input.IdempotencyKey, Binding: binding,
	})
	if err != nil {
		if errors.Is(err, execution.ErrInteractionAlreadyResolved) {
			return ConfirmedChatConfirmation{}, ErrConflict
		}
		return ConfirmedChatConfirmation{}, err
	}
	if !decision.Cached {
		if service.audit != nil {
			eventID, err := newChatAuditEventID()
			if err != nil {
				return ConfirmedChatConfirmation{}, err
			}
			if err := service.audit.AppendChatAuditEvent(ctx, tx, AuditEvent{
				EventID: eventID, WorkspaceID: input.WorkspaceID,
				ActorType: string(decisionPrincipal.Identity.Actor.Type),
				ActorID:   decisionPrincipal.Identity.Actor.ID,
				Action:    "execution.confirmation.confirmed", ResourceType: "EXECUTION_CONFIRMATION",
				ResourceID: value.ExecutionConfirmationID, Result: "SUCCESS",
				Metadata: map[string]any{
					"chatConfirmationId": value.ID, "sessionId": value.SessionID,
					"runId": value.RunID, "targetType": value.TargetType,
				},
			}); err != nil {
				return ConfirmedChatConfirmation{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return ConfirmedChatConfirmation{}, err
	}
	decision = service.decisions.Dispatch(context.WithoutCancel(ctx), decision)
	value, err = service.repository.GetConfirmation(ctx, input.WorkspaceID, input.ConfirmationID)
	if err != nil {
		return ConfirmedChatConfirmation{}, err
	}
	return ConfirmedChatConfirmation{
		Confirmation: value,
		Resume: execution.ConfirmationResumeResult{
			Checkpoint: decision.Checkpoint, Cached: decision.Cached,
		},
		Cached: decision.Cached,
	}, nil
}

func (service *ConfirmationService) Cancel(
	ctx context.Context,
	input CancelChatConfirmationInput,
) (CancelledChatConfirmation, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ConfirmationID = strings.TrimSpace(input.ConfirmationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.IdempotencyKey = strings.ToLower(strings.TrimSpace(input.IdempotencyKey))
	decisionPrincipal, principalErr := chatConfirmationDecisionPrincipal(
		input.WorkspaceID, input.ActorID, input.PrincipalSnapshot,
	)
	if !validUUID(input.WorkspaceID) || !validUUID(input.ConfirmationID) ||
		!validUUID(input.IdempotencyKey) || principalErr != nil ||
		input.ExpectedExecutionLockVersion <= 0 {
		return CancelledChatConfirmation{}, ErrInvalid
	}
	tx, err := service.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return CancelledChatConfirmation{}, err
	}
	defer tx.Rollback()
	value, err := service.repository.getConfirmationForUpdate(
		ctx, tx, input.WorkspaceID, input.ConfirmationID,
	)
	if err != nil {
		return CancelledChatConfirmation{}, err
	}
	binding := chatInteractionDecisionBinding(value)
	if input.Binding != nil {
		binding = *input.Binding
	}
	binding.Version = input.ExpectedExecutionLockVersion
	decision, err := service.decisions.DecideInTransaction(ctx, tx, execution.DecideInteractionInput{
		WorkspaceID: input.WorkspaceID, ConfirmationID: value.ExecutionConfirmationID,
		ActorID: input.ActorID, PrincipalSnapshot: &decisionPrincipal,
		ServiceDecisionPolicy: input.ServiceDecisionPolicy,
		Decision:              execution.InteractionDecisionCancel,
		IdempotencyKey:        input.IdempotencyKey, Binding: binding,
	})
	if err != nil {
		if errors.Is(err, execution.ErrInteractionAlreadyResolved) {
			return CancelledChatConfirmation{}, ErrConflict
		}
		return CancelledChatConfirmation{}, err
	}
	if service.audit != nil && !decision.Cached {
		eventID, err := newChatAuditEventID()
		if err != nil {
			return CancelledChatConfirmation{}, err
		}
		if err := service.audit.AppendChatAuditEvent(ctx, tx, AuditEvent{
			EventID: eventID, WorkspaceID: input.WorkspaceID,
			ActorType: string(decisionPrincipal.Identity.Actor.Type),
			ActorID:   decisionPrincipal.Identity.Actor.ID,
			Action:    "execution.confirmation.cancelled", ResourceType: "EXECUTION_CONFIRMATION",
			ResourceID: value.ExecutionConfirmationID, Result: "SUCCESS",
			Metadata: map[string]any{
				"chatConfirmationId": value.ID, "sessionId": value.SessionID,
				"runId": value.RunID, "targetType": value.TargetType,
			},
		}); err != nil {
			return CancelledChatConfirmation{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return CancelledChatConfirmation{}, err
	}
	value, err = service.repository.GetConfirmation(ctx, input.WorkspaceID, input.ConfirmationID)
	if err != nil {
		return CancelledChatConfirmation{}, err
	}
	return CancelledChatConfirmation{
		Confirmation: value, Checkpoint: decision.Checkpoint, Cached: decision.Cached,
	}, nil
}

func chatInteractionDecisionBinding(value ChatConfirmation) execution.InteractionDecisionBinding {
	return execution.InteractionDecisionBinding{
		RunID: value.RunID, TargetItemID: value.TargetItemID,
		ReleaseID: value.TargetReleaseID, InputHash: value.InputHash,
		ConnectionID: value.ConnectionID, PlanHash: value.PlanHash,
		Version: value.ExecutionLockVersion, ExpiresAt: value.ExpiresAt,
		BindingHash: value.InteractionBindingHash,
	}
}

func chatConfirmationDecisionPrincipal(
	workspaceID, actorID string,
	provided *principal.ExecutionSnapshot,
) (principal.ExecutionSnapshot, error) {
	if provided == nil {
		return principal.NewInternalExecutionSnapshot(
			strings.TrimSpace(workspaceID), principal.TypeUser, strings.TrimSpace(actorID),
		)
	}
	value := *provided
	if provided.Identity.Subject != nil {
		subject := *provided.Identity.Subject
		value.Identity.Subject = &subject
	}
	if value.Validate() != nil || value.Identity.Actor.WorkspaceID != strings.TrimSpace(workspaceID) ||
		(strings.TrimSpace(actorID) != "" && value.Identity.Actor.ID != strings.TrimSpace(actorID)) {
		return principal.ExecutionSnapshot{}, ErrInvalid
	}
	return value, nil
}

func normalizePrepareChatConfirmation(input PrepareChatConfirmationInput) PrepareChatConfirmationInput {
	input.ID = strings.TrimSpace(input.ID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.MessageID = strings.TrimSpace(input.MessageID)
	input.TargetType = strings.ToUpper(strings.TrimSpace(input.TargetType))
	input.RiskLevel = strings.ToUpper(strings.TrimSpace(input.RiskLevel))
	for index := range input.RiskReasons {
		input.RiskReasons[index] = strings.TrimSpace(input.RiskReasons[index])
	}
	return input
}

func validPrepareChatConfirmation(input PrepareChatConfirmationInput) bool {
	if !validUUID(input.ID) || !validUUID(input.WorkspaceID) ||
		!validUUID(input.SessionID) || !validUUID(input.MessageID) ||
		input.ExpectedSessionLockVersion <= 0 ||
		(input.TargetType != execution.ResumeKindTool && input.TargetType != execution.ResumeKindWorkflow) ||
		(input.RiskLevel != "LOW" && input.RiskLevel != "MEDIUM" &&
			input.RiskLevel != "HIGH" && input.RiskLevel != "CRITICAL") ||
		input.Resume.Kind != input.TargetType || input.Resume.Confirmation.WorkspaceID != input.WorkspaceID ||
		input.Resume.Confirmation.RunID == "" || len(input.RiskReasons) == 0 ||
		!sameStringSlice(input.RiskReasons, input.Resume.Confirmation.Decision.RiskReasons) {
		return false
	}
	for _, reason := range input.RiskReasons {
		if reason == "" {
			return false
		}
	}
	return true
}

func canonicalChatConfirmationSummary(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maxChatConfirmationSummaryBytes {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, ErrInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || containsSensitiveChatKey(value) {
		return nil, ErrInvalid
	}
	canonical, err := json.Marshal(value)
	if err != nil || len(canonical) > maxChatConfirmationSummaryBytes {
		return nil, ErrInvalid
	}
	return canonical, nil
}

func containsSensitiveChatKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(key))
			for _, forbidden := range []string{
				"authorization", "password", "secret", "token", "cookie", "apikey", "credential",
			} {
				if strings.Contains(normalized, forbidden) {
					return true
				}
			}
			if containsSensitiveChatKey(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsSensitiveChatKey(child) {
				return true
			}
		}
	}
	return false
}

func sameStringSlice(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
