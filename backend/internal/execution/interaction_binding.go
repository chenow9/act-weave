package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"actweave/backend/internal/principal"
	"actweave/backend/internal/protocolevent"
)

const interactionBindingSpecVersion = "interaction-binding.v1"

// NewInteractionDecisionBinding reconstructs the sealed decision binding from
// the public Interaction and the immutable principal snapshot on its Run. The
// binding hash itself stays internal; callers never need to receive or echo a
// security-sensitive implementation field.
func NewInteractionDecisionBinding(
	workspaceID string,
	interaction protocolevent.Interaction,
	requestPrincipal principal.ExecutionSnapshot,
	expectedVersion int64,
) (InteractionDecisionBinding, error) {
	bindingHash, err := interactionBindingHash(
		workspaceID, interaction.RunID, interaction.TargetItemID,
		interaction.ReleaseID, interaction.InputHash, interaction.ConnectionID,
		interaction.PlanHash, requestPrincipal, expectedVersion, interaction.ExpiresAt,
	)
	if err != nil {
		return InteractionDecisionBinding{}, ErrInteractionDecisionInvalid
	}
	return InteractionDecisionBinding{
		RunID: interaction.RunID, TargetItemID: interaction.TargetItemID,
		ReleaseID: interaction.ReleaseID, InputHash: interaction.InputHash,
		ConnectionID: interaction.ConnectionID, PlanHash: interaction.PlanHash,
		Version: expectedVersion, ExpiresAt: interaction.ExpiresAt,
		BindingHash: bindingHash,
	}, nil
}

// interactionBindingHash seals every immutable fact shown by an Interaction.
// The request Principal snapshot deliberately includes its authorization
// versions; a later decision may carry newer versions, but it cannot rewrite
// the authorization evidence under which the Interaction was created.
func interactionBindingHash(
	workspaceID, runID, targetItemID, releaseID, inputHash, connectionID, planHash string,
	requestPrincipal principal.ExecutionSnapshot,
	interactionVersion int64,
	expiresAt time.Time,
) (string, error) {
	type binding struct {
		SpecVersion        string `json:"specVersion"`
		WorkspaceID        string `json:"workspaceId"`
		ActorType          string `json:"actorType"`
		ActorID            string `json:"actorId"`
		SubjectType        string `json:"subjectType,omitempty"`
		SubjectID          string `json:"subjectId,omitempty"`
		ClientID           string `json:"clientId,omitempty"`
		GrantID            string `json:"grantId,omitempty"`
		GrantVersion       int64  `json:"grantVersion,omitempty"`
		AgentPolicyVersion int64  `json:"agentPolicyVersion,omitempty"`
		RunID              string `json:"runId"`
		TargetItemID       string `json:"targetItemId"`
		ReleaseID          string `json:"releaseId"`
		InputHash          string `json:"inputHash"`
		ConnectionID       string `json:"connectionId,omitempty"`
		PlanHash           string `json:"planHash,omitempty"`
		Version            int64  `json:"version"`
		ExpiresAt          string `json:"expiresAt"`
	}
	if requestPrincipal.Validate() != nil || interactionVersion < 1 || expiresAt.IsZero() {
		return "", ErrConfirmationInvalid
	}
	value := binding{
		SpecVersion: interactionBindingSpecVersion,
		WorkspaceID: strings.TrimSpace(workspaceID),
		ActorType:   string(requestPrincipal.Identity.Actor.Type),
		ActorID:     requestPrincipal.Identity.Actor.ID,
		ClientID:    requestPrincipal.ClientID, GrantID: requestPrincipal.GrantID,
		GrantVersion:       requestPrincipal.GrantVersion,
		AgentPolicyVersion: requestPrincipal.AgentPolicyVersion,
		RunID:              strings.TrimSpace(runID), TargetItemID: strings.TrimSpace(targetItemID),
		ReleaseID: strings.TrimSpace(releaseID), InputHash: strings.ToLower(strings.TrimSpace(inputHash)),
		ConnectionID: strings.TrimSpace(connectionID), PlanHash: strings.ToLower(strings.TrimSpace(planHash)),
		Version: interactionVersion, ExpiresAt: expiresAt.UTC().Format(time.RFC3339Nano),
	}
	if requestPrincipal.Identity.Subject != nil {
		value.SubjectType = string(requestPrincipal.Identity.Subject.Type)
		value.SubjectID = requestPrincipal.Identity.Subject.ID
	}
	// RunID may be empty for WorkflowExecution-only production Approval pauses.
	if !invocationValidUUID(value.WorkspaceID) ||
		(value.RunID != "" && !invocationValidUUID(value.RunID)) ||
		!invocationValidUUID(value.TargetItemID) || !invocationValidUUID(value.ReleaseID) ||
		!validConfirmationHash(value.InputHash) ||
		(value.ConnectionID != "" && !invocationValidUUID(value.ConnectionID)) ||
		(value.PlanHash != "" && !validConfirmationHash(value.PlanHash)) {
		return "", ErrConfirmationInvalid
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", ErrConfirmationInvalid
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func verifyInteractionBinding(value ExecutionConfirmation) error {
	want, err := interactionBindingHash(
		value.WorkspaceID, value.RunID, value.TargetItemID, value.ReleaseID,
		value.InputHash, value.ConnectionID, value.PlanHash,
		value.RequestPrincipalSnapshot, 1, value.ExpiresAt,
	)
	if err != nil || value.InteractionBindingHash != want {
		return ErrConfirmationBindingChanged
	}
	return nil
}
