package workflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/capability"
)

type WorkflowRevisionActivatedEvent struct {
	ID                 string
	Type               string
	WorkspaceID        string
	CapabilityID       string
	PreviousRevisionID *string
	PreviousReleaseID  *string
	TargetRevisionID   string
	TargetRevisionNo   int
	TargetReleaseID    string
	TargetReleaseNo    int
	ActivatedBy        string
	OccurredAt         time.Time
	SchemaVersion      int
}

type ActivationEventWriter interface {
	AppendWorkflowRevisionActivated(context.Context, *sql.Tx, WorkflowRevisionActivatedEvent) error
}

type ActivateRevisionInput struct {
	EventID      string
	WorkspaceID  string
	CapabilityID string
	RevisionID   string
	ActivatedBy  string
}

type ActivateRevisionResult struct {
	Revision Revision
	Release  capability.Release
	Event    WorkflowRevisionActivatedEvent
}

type ActivationService struct {
	repository *Repository
	authorizer PublishAuthorizer
	events     ActivationEventWriter
}

func NewActivationService(
	repository *Repository,
	authorizer PublishAuthorizer,
	events ActivationEventWriter,
) (*ActivationService, error) {
	if repository == nil || authorizer == nil || events == nil {
		return nil, errors.New("workflow activation service dependencies are required")
	}
	return &ActivationService{repository: repository, authorizer: authorizer, events: events}, nil
}

func (s *ActivationService) Activate(
	ctx context.Context,
	input ActivateRevisionInput,
) (ActivateRevisionResult, error) {
	input = normalizeActivateRevision(input)
	if !validActivateRevision(input) {
		return ActivateRevisionResult{}, ErrInvalid
	}
	if _, err := s.authorizer.AuthorizeWorkspace(
		ctx, input.ActivatedBy, input.WorkspaceID, authz.ActionPublish,
	); err != nil {
		return ActivateRevisionResult{}, err
	}
	tx, err := s.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return ActivateRevisionResult{}, fmt.Errorf("begin workflow revision activation transaction: %w", err)
	}
	defer tx.Rollback()
	var capabilityKind, capabilityStatus string
	var previousRevisionID, previousReleaseID sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT c.kind,c.status,w.active_revision_id,c.active_release_id
		FROM capabilities c
		JOIN workflows w ON w.workspace_id=c.workspace_id AND w.capability_id=c.id
		WHERE c.workspace_id=$1 AND c.id=$2 AND c.deleted_at IS NULL
		FOR UPDATE OF c,w
	`, input.WorkspaceID, input.CapabilityID).Scan(
		&capabilityKind, &capabilityStatus, &previousRevisionID, &previousReleaseID,
	); errors.Is(err, sql.ErrNoRows) {
		return ActivateRevisionResult{}, ErrNotFound
	} else if err != nil {
		return ActivateRevisionResult{}, fmt.Errorf("lock workflow revision activation: %w", err)
	}
	if capabilityKind != "WORKFLOW" || capabilityStatus != "ACTIVE" {
		return ActivateRevisionResult{}, ErrInvalid
	}
	if previousRevisionID.Valid && previousRevisionID.String == input.RevisionID {
		return ActivateRevisionResult{}, ErrConflict
	}
	target, err := scanRevision(tx.QueryRowContext(ctx, `
		SELECT `+revisionColumns+`
		FROM workflow_revisions wr
		WHERE wr.workspace_id=$1 AND wr.capability_id=$2 AND wr.id=$3
		 AND wr.status='PUBLISHED' AND wr.retired_at IS NULL
	`, input.WorkspaceID, input.CapabilityID, input.RevisionID))
	if errors.Is(err, sql.ErrNoRows) {
		return ActivateRevisionResult{}, ErrNotFound
	}
	if err != nil {
		return ActivateRevisionResult{}, fmt.Errorf("get target workflow revision: %w", err)
	}
	release, err := scanWorkflowRelease(tx.QueryRowContext(ctx, `
		SELECT
		 r.id,r.workspace_id,r.capability_id,r.release_no,r.source_type,r.source_id,
		 r.callable_name,r.callable_description,r.input_schema,r.output_schema,r.risk_level,
		 r.side_effect_level,r.requires_confirmation,r.checksum,r.published_by,r.published_at,r.retired_at
		FROM capability_releases r
		WHERE r.workspace_id=$1 AND r.capability_id=$2
		 AND r.source_type='WORKFLOW_REVISION' AND r.source_id=$3 AND r.retired_at IS NULL
		ORDER BY r.release_no DESC,r.id DESC
		LIMIT 1
	`, input.WorkspaceID, input.CapabilityID, target.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return ActivateRevisionResult{}, ErrNotFound
	}
	if err != nil {
		return ActivateRevisionResult{}, fmt.Errorf("get target workflow release: %w", err)
	}
	previousRevisionNo := 0
	if previousRevisionID.Valid {
		if err := tx.QueryRowContext(ctx, `
			SELECT revision_no FROM workflow_revisions
			WHERE workspace_id=$1 AND capability_id=$2 AND id=$3
		`, input.WorkspaceID, input.CapabilityID, previousRevisionID.String).Scan(&previousRevisionNo); err != nil {
			return ActivateRevisionResult{}, fmt.Errorf("get previous workflow revision number: %w", err)
		}
	}
	if result, err := tx.ExecContext(ctx, `
		UPDATE workflows SET active_revision_id=$3
		WHERE workspace_id=$1 AND capability_id=$2
	`, input.WorkspaceID, input.CapabilityID, target.ID); err != nil {
		return ActivateRevisionResult{}, mapWrite("activate target workflow revision", err)
	} else if rows, _ := result.RowsAffected(); rows != 1 {
		return ActivateRevisionResult{}, ErrConflict
	}
	if result, err := tx.ExecContext(ctx, `
		UPDATE capabilities
		SET active_release_id=$3,updated_by=$4,updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND kind='WORKFLOW'
		 AND status='ACTIVE' AND deleted_at IS NULL
	`, input.WorkspaceID, input.CapabilityID, release.ID, input.ActivatedBy); err != nil {
		return ActivateRevisionResult{}, mapWrite("activate target workflow release", err)
	} else if rows, _ := result.RowsAffected(); rows != 1 {
		return ActivateRevisionResult{}, ErrConflict
	}
	eventType := "workflow.release.activated"
	if previousRevisionID.Valid && target.RevisionNo < previousRevisionNo {
		eventType = "workflow.release.rolled_back"
	}
	var occurredAt time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&occurredAt); err != nil {
		return ActivateRevisionResult{}, fmt.Errorf("read workflow activation time: %w", err)
	}
	event := WorkflowRevisionActivatedEvent{
		ID: input.EventID, Type: eventType, WorkspaceID: input.WorkspaceID,
		CapabilityID:       input.CapabilityID,
		PreviousRevisionID: nullableStringPointer(previousRevisionID),
		PreviousReleaseID:  nullableStringPointer(previousReleaseID),
		TargetRevisionID:   target.ID, TargetRevisionNo: target.RevisionNo,
		TargetReleaseID: release.ID, TargetReleaseNo: release.ReleaseNo,
		ActivatedBy: input.ActivatedBy, OccurredAt: occurredAt, SchemaVersion: 1,
	}
	if err := s.events.AppendWorkflowRevisionActivated(ctx, tx, event); err != nil {
		return ActivateRevisionResult{}, fmt.Errorf("append workflow activation event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ActivateRevisionResult{}, mapWrite("commit workflow revision activation", err)
	}
	return ActivateRevisionResult{Revision: target, Release: release, Event: event}, nil
}

func normalizeActivateRevision(input ActivateRevisionInput) ActivateRevisionInput {
	input.EventID = strings.TrimSpace(input.EventID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.CapabilityID = strings.TrimSpace(input.CapabilityID)
	input.RevisionID = strings.TrimSpace(input.RevisionID)
	input.ActivatedBy = strings.TrimSpace(input.ActivatedBy)
	return input
}

func validActivateRevision(input ActivateRevisionInput) bool {
	return validUUID(input.EventID) && validUUID(input.WorkspaceID) &&
		validUUID(input.CapabilityID) && validUUID(input.RevisionID) && validUUID(input.ActivatedBy)
}

func nullableStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}
