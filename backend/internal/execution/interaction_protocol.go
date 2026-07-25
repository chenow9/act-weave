package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

type InteractionPresentation struct {
	TargetItemID     string
	Title            string
	RiskLevel        string
	RiskReasons      []string
	InputSummary     json.RawMessage
	AllowedDecisions []protocolevent.InteractionDecision
	RequiredDecider  protocolevent.RequiredDecider
}

// InteractionProtocolMapper combines the immutable execution binding with a
// separately redacted presentation. Resume tokens and their hashes are not
// accepted by this boundary and therefore cannot enter an event by accident.
type InteractionProtocolMapper struct {
	validator *protocolevent.PayloadValidator
}

func NewInteractionProtocolMapper() *InteractionProtocolMapper {
	return &InteractionProtocolMapper{validator: protocolevent.MustDefaultPayloadValidator()}
}

func (mapper *InteractionProtocolMapper) Map(
	confirmation ExecutionConfirmation,
	presentation InteractionPresentation,
	runID string,
) (protocolevent.Interaction, error) {
	return mapper.mapStatus(confirmation, presentation, runID, mapInteractionStatus(confirmation.Status))
}

// MapDecision preserves the public distinction between a declined action and
// a client cancellation. Both are represented by the core Confirmation's
// CANCELLED state, but they are different protocol decisions.
func (mapper *InteractionProtocolMapper) MapDecision(
	confirmation ExecutionConfirmation,
	presentation InteractionPresentation,
	runID, decision string,
) (protocolevent.Interaction, error) {
	status := mapInteractionStatus(confirmation.Status)
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case InteractionDecisionApprove:
		if confirmation.Status != ConfirmationStatusConfirmed {
			return protocolevent.Interaction{}, ErrConfirmationInvalid
		}
	case InteractionDecisionDecline:
		if confirmation.Status != ConfirmationStatusCancelled {
			return protocolevent.Interaction{}, ErrConfirmationInvalid
		}
		status = protocolevent.InteractionStatusDeclined
	case InteractionDecisionCancel:
		if confirmation.Status != ConfirmationStatusCancelled {
			return protocolevent.Interaction{}, ErrConfirmationInvalid
		}
	default:
		return protocolevent.Interaction{}, ErrConfirmationInvalid
	}
	return mapper.mapStatus(confirmation, presentation, runID, status)
}

func (mapper *InteractionProtocolMapper) mapStatus(
	confirmation ExecutionConfirmation,
	presentation InteractionPresentation,
	runID string,
	status protocolevent.InteractionStatus,
) (protocolevent.Interaction, error) {
	interaction := protocolevent.Interaction{
		ID: confirmation.ID, Kind: protocolevent.InteractionKindApproval,
		Status: status, TargetItemID: strings.TrimSpace(presentation.TargetItemID),
		RunID: strings.TrimSpace(runID), ReleaseID: confirmation.ReleaseID,
		InputHash: confirmation.InputHash, ConnectionID: confirmation.ConnectionID,
		PlanHash: confirmation.PlanHash, Title: strings.TrimSpace(presentation.Title),
		Reason: strings.TrimSpace(confirmation.Reason),
		Risk: protocolevent.InteractionRisk{
			Level:   mapInteractionRisk(presentation.RiskLevel),
			Reasons: append([]string(nil), presentation.RiskReasons...),
		},
		InputSummary:     append(json.RawMessage(nil), presentation.InputSummary...),
		AllowedDecisions: append([]protocolevent.InteractionDecision(nil), presentation.AllowedDecisions...),
		RequiredDecider:  presentation.RequiredDecider,
		Version:          confirmation.LockVersion, ExpiresAt: confirmation.ExpiresAt.UTC(),
	}
	if mapper == nil || mapper.validator == nil || status == protocolevent.InteractionStatusUnknown ||
		!validInteractionConfirmation(confirmation, runID) ||
		!sameInteractionRiskReasons(confirmation.RiskReasons, presentation.RiskReasons) ||
		!validInteractionDecisions(presentation.AllowedDecisions) ||
		protocolevent.ParseRequiredDecider(string(presentation.RequiredDecider)) == protocolevent.RequiredDeciderUnknown {
		return protocolevent.Interaction{}, ErrConfirmationInvalid
	}
	data, err := json.Marshal(protocolevent.InteractionData{Interaction: interaction})
	if err != nil || mapper.validator.ValidateEventData(interactionEventType(status), data) != nil {
		return protocolevent.Interaction{}, ErrConfirmationInvalid
	}
	return interaction, nil
}

var errProtocolInteractionDecisionCached = errors.New("protocol interaction decision is cached")

type ProtocolInteractionDecisionInput struct {
	Context      ProtocolInteractionContext
	Decision     DecideInteractionInput
	Presentation InteractionPresentation
}

type ProtocolInteractionDecisionResult struct {
	Decision    InteractionDecisionResult
	Projection  protocolevent.RunItemProjection
	Events      []protocolevent.ProtocolEvent
	NotifyError error
}

// ProtocolInteractionDecisionService is the transactional boundary for an
// external interaction command. The domain CAS, Interaction Item terminal
// projection, interaction.resolved event, and cancellation Run snapshot are
// either all committed or all rolled back.
type ProtocolInteractionDecisionService struct {
	decisions *InteractionDecisionService
	unit      *protocolevent.ProtocolUnitOfWork
	mapper    *InteractionProtocolMapper
	runMapper *RunLifecycleMapper
}

func NewProtocolInteractionDecisionService(
	decisions *InteractionDecisionService,
	unit *protocolevent.ProtocolUnitOfWork,
	mapper *InteractionProtocolMapper,
) (*ProtocolInteractionDecisionService, error) {
	if decisions == nil || decisions.resumes == nil || decisions.resumes.runs == nil ||
		unit == nil || mapper == nil {
		return nil, ErrConfirmationInvalid
	}
	return &ProtocolInteractionDecisionService{
		decisions: decisions, unit: unit, mapper: mapper,
		runMapper: NewRunLifecycleMapper(),
	}, nil
}

func (service *ProtocolInteractionDecisionService) Decide(
	ctx context.Context,
	input ProtocolInteractionDecisionInput,
) (ProtocolInteractionDecisionResult, error) {
	if service == nil || service.decisions == nil || service.unit == nil ||
		service.mapper == nil || service.runMapper == nil ||
		validateProtocolInteractionContext(input.Context, ExecutionConfirmation{
			WorkspaceID: input.Decision.WorkspaceID,
			RunID:       input.Decision.Binding.RunID,
		}) != nil {
		return ProtocolInteractionDecisionResult{}, ErrInteractionDecisionInvalid
	}
	var decision InteractionDecisionResult
	var projection protocolevent.RunItemProjection
	unitResult, err := service.unit.Execute(ctx, func(
		ctx context.Context,
		transaction *protocolevent.ProtocolTransaction,
	) error {
		tx, err := transaction.SQLTx()
		if err != nil {
			return err
		}
		decision, err = service.decisions.DecideInTransaction(ctx, tx, input.Decision)
		if err != nil {
			return err
		}
		if decision.Cached {
			return errProtocolInteractionDecisionCached
		}
		interaction, err := service.mapper.MapDecision(
			decision.Confirmation, input.Presentation,
			input.Context.Scope.RunID, decision.Decision,
		)
		if err != nil {
			return err
		}
		occurredAt, err := interactionDecisionOccurredAt(decision.Confirmation)
		if err != nil {
			return err
		}
		projection, err = transaction.CompleteRunItem(ctx, protocolevent.CompleteRunItemInput{
			WorkspaceID: input.Context.Scope.WorkspaceID,
			AgentID:     input.Context.Scope.AgentID,
			RunID:       input.Context.Scope.RunID,
			Item:        protocolInteractionItem(interaction),
			CompletedAt: occurredAt,
		})
		if err != nil {
			return err
		}
		resolved, err := buildInteractionEvent(
			input.Context, interaction, protocolevent.EventInteractionResolved, occurredAt,
		)
		if err != nil {
			return err
		}
		events := []protocolevent.NewProtocolEvent{resolved}
		if decision.Decision != InteractionDecisionApprove {
			run, err := service.decisions.resumes.runs.GetAgentRunInTransaction(
				ctx, tx, input.Context.Scope.WorkspaceID, input.Context.Scope.RunID,
			)
			if err != nil {
				return err
			}
			eventID, err := uuid.NewV7()
			if err != nil {
				return err
			}
			cancelled, err := service.runMapper.Map(RunLifecycleEventInput{
				EventID: eventID.String(), EventStreamID: input.Context.EventStreamID,
				EventType: protocolevent.EventRunCancelled, Run: run,
				OccurredAt: occurredAt,
			})
			if err != nil {
				return err
			}
			events = append(events, cancelled)
		}
		_, err = transaction.Append(ctx, events)
		return err
	})
	if errors.Is(err, errProtocolInteractionDecisionCached) {
		return ProtocolInteractionDecisionResult{Decision: decision}, nil
	}
	if err != nil {
		return ProtocolInteractionDecisionResult{}, err
	}
	// Runtime wake-up is deliberately post-commit. Its outcome cannot roll back
	// an accepted command or the protocol event acknowledging that command.
	decision = service.decisions.Dispatch(context.WithoutCancel(ctx), decision)
	return ProtocolInteractionDecisionResult{
		Decision: decision, Projection: projection,
		Events: unitResult.Events, NotifyError: unitResult.NotifyError,
	}, nil
}

func interactionDecisionOccurredAt(confirmation ExecutionConfirmation) (time.Time, error) {
	switch confirmation.Status {
	case ConfirmationStatusConfirmed:
		if confirmation.ConfirmedAt != nil {
			return confirmation.ConfirmedAt.UTC(), nil
		}
	case ConfirmationStatusCancelled:
		if confirmation.CancelledAt != nil {
			return confirmation.CancelledAt.UTC(), nil
		}
	}
	return time.Time{}, ErrConfirmationInvalid
}

type ProtocolInteractionContext struct {
	Scope         protocolevent.RunScope
	EventStreamID string
	TraceID       string
}

type ProjectInteractionRequestedInput struct {
	Context      ProtocolInteractionContext
	Confirmation ExecutionConfirmation
	Presentation InteractionPresentation
	Ordinal      int
}

// ProjectInteractionRequestedWithTarget atomically publishes the waiting
// target Item, Interaction Item, interaction.requested, and run.waiting. This
// is the native boundary used when a tool is gated before a ToolInvocation row
// exists; the later invocation completes the same target Item ID.
type ProjectInteractionRequestedWithTargetInput struct {
	Context            ProtocolInteractionContext
	Confirmation       ExecutionConfirmation
	Presentation       InteractionPresentation
	Run                AgentRun
	Target             protocolevent.ToolCallItem
	TargetOrdinal      int
	InteractionOrdinal int
}

type ProjectInteractionTerminalInput struct {
	Context      ProtocolInteractionContext
	Confirmation ExecutionConfirmation
	Presentation InteractionPresentation
	OccurredAt   time.Time
}

type ProtocolInteractionProjectionResult struct {
	Projection  protocolevent.RunItemProjection
	Events      []protocolevent.ProtocolEvent
	NotifyError error
}

type ProtocolInteractionProjector struct {
	unit   *protocolevent.ProtocolUnitOfWork
	mapper *InteractionProtocolMapper
}

func NewProtocolInteractionProjector(
	unit *protocolevent.ProtocolUnitOfWork,
	mapper *InteractionProtocolMapper,
) (*ProtocolInteractionProjector, error) {
	if unit == nil || mapper == nil {
		return nil, ErrConfirmationInvalid
	}
	return &ProtocolInteractionProjector{unit: unit, mapper: mapper}, nil
}

func (projector *ProtocolInteractionProjector) ProjectRequested(
	ctx context.Context,
	input ProjectInteractionRequestedInput,
) (ProtocolInteractionProjectionResult, error) {
	if !validProtocolInteractionProjector(projector) || input.Ordinal < 1 ||
		validateProtocolInteractionContext(input.Context, input.Confirmation) != nil ||
		input.Confirmation.Status != ConfirmationStatusPending {
		return ProtocolInteractionProjectionResult{}, ErrConfirmationInvalid
	}
	interaction, err := projector.mapper.Map(
		input.Confirmation, input.Presentation, input.Context.Scope.RunID,
	)
	if err != nil {
		return ProtocolInteractionProjectionResult{}, err
	}
	item := protocolInteractionItem(interaction)
	event, err := buildInteractionEvent(input.Context, interaction, protocolevent.EventInteractionRequested, input.Confirmation.CreatedAt)
	if err != nil {
		return ProtocolInteractionProjectionResult{}, err
	}
	var projection protocolevent.RunItemProjection
	result, err := projector.unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
		if _, err := transaction.EnsureRunEventStream(ctx, input.Context.EventStreamID, input.Context.Scope); err != nil {
			return err
		}
		tx, err := transaction.SQLTx()
		if err != nil {
			return err
		}
		var targetExists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM run_items
			 WHERE workspace_id=$1 AND agent_id=$2 AND run_id=$3 AND id=$4)
		`, input.Context.Scope.WorkspaceID, input.Context.Scope.AgentID,
			input.Context.Scope.RunID, interaction.TargetItemID).Scan(&targetExists); err != nil {
			return err
		}
		if !targetExists {
			return ErrConfirmationInvalid
		}
		projection, err = transaction.CreateRunItem(ctx, protocolevent.CreateRunItemInput{
			WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
			RunID: input.Context.Scope.RunID, Ordinal: input.Ordinal,
			SourceType: protocolevent.SourceExecutionConfirmation, SourceID: input.Confirmation.ID,
			Item: item, StartedAt: input.Confirmation.CreatedAt,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		return ProtocolInteractionProjectionResult{}, err
	}
	return interactionProjectionResult(projection, result), nil
}

func (projector *ProtocolInteractionProjector) ProjectRequestedWithTarget(
	ctx context.Context,
	input ProjectInteractionRequestedWithTargetInput,
) (ProtocolInteractionProjectionResult, error) {
	if !validProtocolInteractionProjector(projector) || input.TargetOrdinal < 1 ||
		input.InteractionOrdinal != input.TargetOrdinal+1 ||
		validateProtocolInteractionContext(input.Context, input.Confirmation) != nil ||
		input.Confirmation.Status != ConfirmationStatusPending ||
		input.Run.Status != "WAITING_CONFIRMATION" ||
		input.Run.ID != input.Context.Scope.RunID || input.Run.WorkspaceID != input.Context.Scope.WorkspaceID ||
		input.Run.AgentID != input.Context.Scope.AgentID || input.Run.SessionID != input.Context.Scope.ConversationID ||
		input.Target.ID != input.Confirmation.TargetItemID ||
		input.Target.Type != protocolevent.ItemTypeToolCall ||
		input.Target.Status != protocolevent.ItemStatusWaiting ||
		protocolevent.ValidateItem(input.Target) != nil {
		return ProtocolInteractionProjectionResult{}, ErrConfirmationInvalid
	}
	interaction, err := projector.mapper.Map(
		input.Confirmation, input.Presentation, input.Context.Scope.RunID,
	)
	if err != nil {
		return ProtocolInteractionProjectionResult{}, err
	}
	interactionItem := protocolInteractionItem(interaction)
	targetEvent, err := buildToolSnapshotEvent(ProtocolToolCallContext{
		Scope: input.Context.Scope, EventStreamID: input.Context.EventStreamID,
		TraceID: input.Context.TraceID,
	}, input.Target, protocolevent.EventItemStarted, input.Confirmation.CreatedAt)
	if err != nil {
		return ProtocolInteractionProjectionResult{}, err
	}
	requested, err := buildInteractionEvent(
		input.Context, interaction, protocolevent.EventInteractionRequested,
		input.Confirmation.CreatedAt,
	)
	if err != nil {
		return ProtocolInteractionProjectionResult{}, err
	}
	waitingID, err := uuid.NewV7()
	if err != nil {
		return ProtocolInteractionProjectionResult{}, err
	}
	waiting, err := NewRunLifecycleMapper().Map(RunLifecycleEventInput{
		EventID: waitingID.String(), EventStreamID: input.Context.EventStreamID,
		EventType: protocolevent.EventRunWaiting, Run: input.Run,
		OccurredAt:     input.Confirmation.CreatedAt,
		InteractionIDs: []string{input.Confirmation.ID},
	})
	if err != nil {
		return ProtocolInteractionProjectionResult{}, err
	}
	var projection protocolevent.RunItemProjection
	result, err := projector.unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
		if _, err := transaction.EnsureRunEventStream(ctx, input.Context.EventStreamID, input.Context.Scope); err != nil {
			return err
		}
		if _, err := transaction.CreateRunItem(ctx, protocolevent.CreateRunItemInput{
			WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
			RunID: input.Context.Scope.RunID, Ordinal: input.TargetOrdinal,
			SourceType: protocolevent.SourceRuntime, SourceID: input.Target.ID,
			Item: input.Target, StartedAt: input.Confirmation.CreatedAt,
		}); err != nil {
			return err
		}
		projection, err = transaction.CreateRunItem(ctx, protocolevent.CreateRunItemInput{
			WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
			RunID: input.Context.Scope.RunID, Ordinal: input.InteractionOrdinal,
			SourceType: protocolevent.SourceExecutionConfirmation, SourceID: input.Confirmation.ID,
			Item: interactionItem, StartedAt: input.Confirmation.CreatedAt,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{targetEvent, requested, waiting})
		return err
	})
	if err != nil {
		return ProtocolInteractionProjectionResult{}, err
	}
	return interactionProjectionResult(projection, result), nil
}

func (projector *ProtocolInteractionProjector) ProjectTerminal(
	ctx context.Context,
	input ProjectInteractionTerminalInput,
) (ProtocolInteractionProjectionResult, error) {
	if !validProtocolInteractionProjector(projector) || input.OccurredAt.IsZero() ||
		validateProtocolInteractionContext(input.Context, input.Confirmation) != nil ||
		input.Confirmation.Status == ConfirmationStatusPending {
		return ProtocolInteractionProjectionResult{}, ErrConfirmationInvalid
	}
	interaction, err := projector.mapper.Map(
		input.Confirmation, input.Presentation, input.Context.Scope.RunID,
	)
	if err != nil || !validInteractionTerminalTime(input.Confirmation, input.OccurredAt) {
		return ProtocolInteractionProjectionResult{}, ErrConfirmationInvalid
	}
	eventType := interactionEventType(interaction.Status)
	event, err := buildInteractionEvent(input.Context, interaction, eventType, input.OccurredAt)
	if err != nil {
		return ProtocolInteractionProjectionResult{}, err
	}
	item := protocolInteractionItem(interaction)
	var projection protocolevent.RunItemProjection
	result, err := projector.unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
		projection, err = transaction.CompleteRunItem(ctx, protocolevent.CompleteRunItemInput{
			WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
			RunID: input.Context.Scope.RunID, Item: item, CompletedAt: input.OccurredAt,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		return ProtocolInteractionProjectionResult{}, err
	}
	return interactionProjectionResult(projection, result), nil
}

func buildInteractionEvent(
	protocolContext ProtocolInteractionContext,
	interaction protocolevent.Interaction,
	eventType string,
	occurredAt time.Time,
) (protocolevent.NewProtocolEvent, error) {
	eventID, err := uuid.NewV7()
	if err != nil {
		return protocolevent.NewProtocolEvent{}, err
	}
	return protocolevent.BuildProtocolEvent(protocolevent.NewProtocolEvent{
		ID: eventID.String(), EventStreamID: protocolContext.EventStreamID,
		WorkspaceID: protocolContext.Scope.WorkspaceID, AgentID: protocolContext.Scope.AgentID,
		ConversationID: protocolContext.Scope.ConversationID, RunID: protocolContext.Scope.RunID,
		Type: eventType, SpecVersion: "1.0", TraceID: protocolContext.TraceID,
		InteractionID: interaction.ID, OccurredAt: occurredAt.UTC(),
	}, protocolevent.InteractionData{Interaction: interaction})
}

func protocolInteractionItem(interaction protocolevent.Interaction) protocolevent.InteractionItem {
	status := protocolevent.ItemStatusWaiting
	switch interaction.Status {
	case protocolevent.InteractionStatusApproved:
		status = protocolevent.ItemStatusCompleted
	case protocolevent.InteractionStatusDeclined:
		status = protocolevent.ItemStatusDeclined
	case protocolevent.InteractionStatusCancelled, protocolevent.InteractionStatusExpired:
		status = protocolevent.ItemStatusCancelled
	}
	return protocolevent.InteractionItem{
		ID: interaction.ID, Type: protocolevent.ItemTypeInteraction,
		Status: status, Interaction: interaction,
	}
}

func mapInteractionStatus(status string) protocolevent.InteractionStatus {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case ConfirmationStatusPending:
		return protocolevent.InteractionStatusPending
	case ConfirmationStatusConfirmed:
		return protocolevent.InteractionStatusApproved
	case ConfirmationStatusCancelled:
		return protocolevent.InteractionStatusCancelled
	case ConfirmationStatusExpired:
		return protocolevent.InteractionStatusExpired
	default:
		return protocolevent.InteractionStatusUnknown
	}
}

func mapInteractionRisk(level string) protocolevent.RiskLevel {
	return protocolevent.ParseRiskLevel(strings.ToLower(strings.TrimSpace(level)))
}

func interactionEventType(status protocolevent.InteractionStatus) string {
	switch status {
	case protocolevent.InteractionStatusPending:
		return protocolevent.EventInteractionRequested
	case protocolevent.InteractionStatusExpired:
		return protocolevent.EventInteractionExpired
	case protocolevent.InteractionStatusApproved, protocolevent.InteractionStatusDeclined,
		protocolevent.InteractionStatusCancelled:
		return protocolevent.EventInteractionResolved
	default:
		return ""
	}
}

func validInteractionConfirmation(confirmation ExecutionConfirmation, runID string) bool {
	if !invocationValidUUID(confirmation.ID) || !invocationValidUUID(confirmation.WorkspaceID) ||
		!invocationValidUUID(strings.TrimSpace(runID)) ||
		confirmation.RunID != strings.TrimSpace(runID) ||
		!invocationValidUUID(confirmation.ReleaseID) ||
		!validConfirmationHash(confirmation.InputHash) ||
		(confirmation.ConnectionID != "" && !invocationValidUUID(confirmation.ConnectionID)) ||
		(confirmation.PlanHash != "" && !validConfirmationHash(confirmation.PlanHash)) ||
		strings.TrimSpace(confirmation.Reason) == "" || len(confirmation.RiskReasons) == 0 ||
		confirmation.CreatedAt.IsZero() || !confirmation.ExpiresAt.After(confirmation.CreatedAt) ||
		confirmation.LockVersion < 1 {
		return false
	}
	return true
}

func validInteractionDecisions(decisions []protocolevent.InteractionDecision) bool {
	if len(decisions) == 0 {
		return false
	}
	seen := make(map[protocolevent.InteractionDecision]struct{}, len(decisions))
	for _, decision := range decisions {
		if protocolevent.ParseInteractionDecision(string(decision)) == protocolevent.InteractionDecisionUnknown {
			return false
		}
		if _, duplicate := seen[decision]; duplicate {
			return false
		}
		seen[decision] = struct{}{}
	}
	return true
}

func sameInteractionRiskReasons(left, right []string) bool {
	if len(left) != len(right) || len(left) == 0 {
		return false
	}
	for index := range left {
		if strings.TrimSpace(left[index]) == "" || left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateProtocolInteractionContext(
	protocolContext ProtocolInteractionContext,
	confirmation ExecutionConfirmation,
) error {
	if !invocationValidUUID(protocolContext.Scope.WorkspaceID) ||
		!invocationValidUUID(protocolContext.Scope.AgentID) ||
		!invocationValidUUID(protocolContext.Scope.ConversationID) ||
		!invocationValidUUID(protocolContext.Scope.RunID) ||
		!invocationValidUUID(strings.TrimSpace(protocolContext.EventStreamID)) ||
		strings.TrimSpace(protocolContext.TraceID) == "" ||
		confirmation.WorkspaceID != protocolContext.Scope.WorkspaceID ||
		confirmation.RunID != protocolContext.Scope.RunID {
		return ErrConfirmationInvalid
	}
	return nil
}

func validInteractionTerminalTime(confirmation ExecutionConfirmation, occurredAt time.Time) bool {
	occurredAt = occurredAt.UTC()
	switch confirmation.Status {
	case ConfirmationStatusConfirmed:
		return confirmation.ConfirmedAt != nil && occurredAt.Equal(confirmation.ConfirmedAt.UTC())
	case ConfirmationStatusCancelled:
		return confirmation.CancelledAt != nil && occurredAt.Equal(confirmation.CancelledAt.UTC())
	case ConfirmationStatusExpired:
		return !occurredAt.Before(confirmation.ExpiresAt.UTC())
	default:
		return false
	}
}

func validProtocolInteractionProjector(projector *ProtocolInteractionProjector) bool {
	return projector != nil && projector.unit != nil && projector.mapper != nil
}

func interactionProjectionResult(
	projection protocolevent.RunItemProjection,
	result protocolevent.UnitOfWorkResult,
) ProtocolInteractionProjectionResult {
	return ProtocolInteractionProjectionResult{
		Projection: projection, Events: result.Events, NotifyError: result.NotifyError,
	}
}
