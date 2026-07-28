package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/audit"
	"github.com/google/uuid"
)

const (
	ActionPromptRead                = "agent.prompt.read"
	ErrorCodePromptAuditUnavailable = "PROMPT_AUDIT_UNAVAILABLE"
)

var ErrPromptAuditUnavailable = errors.New("prompt audit unavailable")

// CurrentPrompt is the on-demand read model for an Agent's current system prompt.
// It is never embedded in list/get DTOs.
type CurrentPrompt struct {
	AgentID      string
	RevisionID   string
	RevisionNo   int
	SystemPrompt string
	Source       string
	CreatedBy    string
	CreatedAt    time.Time
}

// PromptReadAuditor records sensitive prompt-read events. Implementations must
// not accept or log the prompt body.
type PromptReadAuditor interface {
	Record(context.Context, audit.ManagementEventInput) (audit.Event, error)
}

// CurrentPromptQuery loads the current Revision for an Agent and returns the
// body only after a successful sensitive-read audit (TD4-A fail closed).
type CurrentPromptQuery struct {
	repository *Repository
	auditor    PromptReadAuditor
}

func NewCurrentPromptQuery(repository *Repository, auditor PromptReadAuditor) (*CurrentPromptQuery, error) {
	if repository == nil || auditor == nil {
		return nil, errors.New("current prompt repository and auditor are required")
	}
	return &CurrentPromptQuery{repository: repository, auditor: auditor}, nil
}

// GetCurrent returns the current system prompt for the Agent in the Workspace.
// Actor is required for audit; actorDisplay is required by audit.Builder (1–255 chars).
// Request/trace IDs are taken from ctx via audit.
func (q *CurrentPromptQuery) GetCurrent(
	ctx context.Context,
	workspaceID, agentID, actorID, actorDisplay string,
) (CurrentPrompt, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	agentID = strings.TrimSpace(agentID)
	actorID = strings.TrimSpace(actorID)
	if !validUUID(workspaceID) || !validUUID(agentID) || !validUUID(actorID) {
		return CurrentPrompt{}, ErrInvalid
	}
	actorDisplay = auditActorDisplay(actorDisplay)

	revision, err := q.repository.GetCurrentPromptRevision(ctx, workspaceID, agentID)
	if err != nil {
		_ = q.recordRead(ctx, workspaceID, agentID, "", actorID, actorDisplay, "DENIED", map[string]any{
			"reason": "NOT_FOUND",
		})
		return CurrentPrompt{}, err
	}

	// Audit success BEFORE returning body. Any audit failure fail-closes.
	if err := q.recordRead(ctx, workspaceID, agentID, revision.ID, actorID, actorDisplay, "SUCCESS", map[string]any{
		"revisionId": revision.ID,
		"revisionNo": revision.RevisionNo,
		"source":     revision.Source,
	}); err != nil {
		return CurrentPrompt{}, fmt.Errorf("%w: %v", ErrPromptAuditUnavailable, err)
	}

	return CurrentPrompt{
		AgentID:      agentID,
		RevisionID:   revision.ID,
		RevisionNo:   revision.RevisionNo,
		SystemPrompt: revision.SystemPrompt,
		Source:       revision.Source,
		CreatedBy:    revision.CreatedBy,
		CreatedAt:    revision.CreatedAt,
	}, nil
}

func (q *CurrentPromptQuery) recordRead(
	ctx context.Context,
	workspaceID, agentID, revisionID, actorID, actorDisplay, result string,
	metadata map[string]any,
) error {
	eventID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	resourceID := agentID
	if revisionID != "" {
		resourceID = revisionID
	}
	meta := map[string]any{
		"agentId": agentID,
		"result":  result,
	}
	for key, value := range metadata {
		meta[key] = value
	}
	// Never include systemPrompt or any body field.
	_, err = q.auditor.Record(ctx, audit.ManagementEventInput{
		EventID:      eventID.String(),
		OccurredAt:   time.Now().UTC(),
		WorkspaceID:  workspaceID,
		ActorType:    "USER",
		ActorID:      actorID,
		ActorDisplay: auditActorDisplay(actorDisplay),
		Action:       ActionPromptRead,
		ResourceType: "AGENT_PROMPT_REVISION",
		ResourceID:   resourceID,
		Result:       result,
		Metadata:     meta,
	})
	return err
}

// auditActorDisplay returns a non-empty display name for audit.Builder (1–255).
// Prefer the caller's username; fall back to a stable workspace-member label.
func auditActorDisplay(display string) string {
	display = strings.TrimSpace(display)
	if display == "" {
		return "Workspace member"
	}
	if len(display) > 255 {
		return display[:255]
	}
	return display
}
