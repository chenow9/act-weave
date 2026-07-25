package chat

import (
	"strings"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
)

// MapInteractionPresentation converts the immutable ChatConfirmation display
// projection into the redacted half of the public Interaction. The execution
// mapper separately supplies binding hashes and IDs from ExecutionConfirmation.
func MapInteractionPresentation(
	chatConfirmation ChatConfirmation,
	executionConfirmation execution.ExecutionConfirmation,
	targetItemID string,
) (execution.InteractionPresentation, error) {
	if !validUUID(strings.TrimSpace(targetItemID)) ||
		executionConfirmation.TargetItemID != strings.TrimSpace(targetItemID) ||
		chatConfirmation.WorkspaceID != executionConfirmation.WorkspaceID ||
		chatConfirmation.RunID != executionConfirmation.RunID ||
		chatConfirmation.ExecutionConfirmationID != executionConfirmation.ID ||
		chatConfirmation.TargetReleaseID != executionConfirmation.ReleaseID ||
		chatConfirmation.Status != executionConfirmation.Status ||
		len(chatConfirmation.RiskReasons) == 0 || len(chatConfirmation.InputSummary) == 0 {
		return execution.InteractionPresentation{}, ErrInvalid
	}
	title := "Approve action"
	switch strings.ToUpper(strings.TrimSpace(chatConfirmation.TargetType)) {
	case execution.ResumeKindTool:
		title = "Approve tool action"
	case execution.ResumeKindWorkflow:
		title = "Approve workflow action"
	default:
		return execution.InteractionPresentation{}, ErrInvalid
	}
	requiredDecider := interactionRequiredDecider(executionConfirmation)
	allowedDecisions := []protocolevent.InteractionDecision{
		protocolevent.InteractionDecisionApprove,
		protocolevent.InteractionDecisionCancel,
	}
	if requiredDecider != protocolevent.RequiredDeciderActWeaveUser {
		allowedDecisions = []protocolevent.InteractionDecision{
			protocolevent.InteractionDecisionApprove,
			protocolevent.InteractionDecisionDecline,
			protocolevent.InteractionDecisionCancel,
		}
	}
	return execution.InteractionPresentation{
		TargetItemID: targetItemID, Title: title,
		RiskLevel:        strings.ToLower(strings.TrimSpace(chatConfirmation.RiskLevel)),
		RiskReasons:      append([]string(nil), chatConfirmation.RiskReasons...),
		InputSummary:     append([]byte(nil), chatConfirmation.InputSummary...),
		AllowedDecisions: allowedDecisions,
		RequiredDecider:  requiredDecider,
	}, nil
}

func interactionRequiredDecider(
	confirmation execution.ExecutionConfirmation,
) protocolevent.RequiredDecider {
	identity := confirmation.RequestPrincipalSnapshot.Identity
	switch identity.Actor.Type {
	case "USER":
		return protocolevent.RequiredDeciderActWeaveUser
	case "SERVICE_PRINCIPAL":
		if identity.Subject != nil {
			return protocolevent.RequiredDeciderSameExternalSubject
		}
		return protocolevent.RequiredDeciderServicePrincipal
	default:
		return protocolevent.RequiredDeciderUnknown
	}
}
