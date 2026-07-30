package protocolevent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// evidencePersistFailedCode is the T8-A stable code (execution.ErrCodeCompactionEvidencePersistFailed).
const evidencePersistFailedCode = "CONTEXT_COMPACTION_EVIDENCE_PERSIST_FAILED"

// ContextCompactionProjector atomically projects compact lifecycle to run_items + events.
// Step lifecycle is coordinated by the caller (CompactStepLifecycle) in the same tx when possible.
type ContextCompactionProjector struct {
	UoW *ProtocolUnitOfWork
}

// ProjectStarted writes in_progress item + item.started (metadata only, zero summary body).
func (p *ContextCompactionProjector) ProjectStarted(
	ctx context.Context,
	scope RunScope,
	itemID string,
	ordinal int,
	meta ContextCompactionPayloadInput,
) error {
	if p == nil || p.UoW == nil {
		return ErrProtocolUnitOfWorkInvalid
	}
	meta.ID = itemID
	meta.Status = ItemStatusInProgress
	if meta.Result == "" {
		meta.Result = "building"
	}
	meta.IncludeSummary = false
	meta.InjectedSummary = ""
	item, err := BuildContextCompactionItem(meta)
	if err != nil {
		return err
	}
	_, err = p.UoW.Execute(ctx, func(ctx context.Context, tx *ProtocolTransaction) error {
		if _, err := tx.EnsureRunEventStream(ctx, scope.RunID, scope); err != nil {
			return err
		}
		if _, err := tx.CreateRunItem(ctx, CreateRunItemInput{
			WorkspaceID: scope.WorkspaceID,
			AgentID:     scope.AgentID,
			RunID:       scope.RunID,
			Ordinal:     ordinal,
			SourceType:  "CONTEXT_COMPACTION",
			SourceID:    itemID,
			Item:        item,
			StartedAt:   time.Now().UTC(),
		}); err != nil {
			return err
		}
		payload, err := json.Marshal(ItemSnapshotData{Item: item})
		if err != nil {
			return err
		}
		_, err = tx.Append(ctx, []NewProtocolEvent{{
			EventStreamID: scope.RunID,
			Type:          EventItemStarted,
			Data:          payload,
			WorkspaceID:   scope.WorkspaceID,
			AgentID:       scope.AgentID,
			ConversationID: scope.ConversationID,
			RunID:         scope.RunID,
		}})
		return err
	})
	if err != nil {
		return fmt.Errorf("%s: %w", evidencePersistFailedCode, err)
	}
	return nil
}

// ProjectTerminal dual-writes terminal item snapshot to run_items.snapshot and item.completed.
// T4-B body rules applied by BuildContextCompactionItem.
func (p *ContextCompactionProjector) ProjectTerminal(
	ctx context.Context,
	scope RunScope,
	itemID string,
	meta ContextCompactionPayloadInput,
) error {
	if p == nil || p.UoW == nil {
		return ErrProtocolUnitOfWorkInvalid
	}
	meta.ID = itemID
	switch strings.TrimSpace(meta.Result) {
	case "completed":
		meta.Status = ItemStatusCompleted
	case "fallback", "failed":
		meta.Status = ItemStatusFailed
	default:
		meta.Status = ItemStatusFailed
		if meta.Result == "" {
			meta.Result = "failed"
		}
	}
	item, err := BuildContextCompactionItem(meta)
	if err != nil {
		return err
	}
	_, err = p.UoW.Execute(ctx, func(ctx context.Context, tx *ProtocolTransaction) error {
		if _, err := tx.EnsureRunEventStream(ctx, scope.RunID, scope); err != nil {
			return err
		}
		if _, err := tx.CompleteRunItem(ctx, CompleteRunItemInput{
			WorkspaceID: scope.WorkspaceID,
			AgentID:     scope.AgentID,
			RunID:       scope.RunID,
			Item:        item,
			CompletedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		payload, err := json.Marshal(ItemSnapshotData{Item: item})
		if err != nil {
			return err
		}
		// Dual-write: completed event payload must match run_items.snapshot terminal body.
		_, err = tx.Append(ctx, []NewProtocolEvent{{
			EventStreamID:  scope.RunID,
			Type:           EventItemCompleted,
			Data:           payload,
			WorkspaceID:    scope.WorkspaceID,
			AgentID:        scope.AgentID,
			ConversationID: scope.ConversationID,
			RunID:          scope.RunID,
		}})
		return err
	})
	if err != nil {
		return fmt.Errorf("%s: %w", evidencePersistFailedCode, err)
	}
	return nil
}
