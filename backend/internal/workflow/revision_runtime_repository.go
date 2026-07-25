package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflowruntime"
)

func (r *Repository) ResolveRevisionSnapshot(
	ctx context.Context,
	workspaceID, capabilityID, releaseID string,
) (workflowruntime.RevisionSnapshot, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(releaseID) {
		return workflowruntime.RevisionSnapshot{}, ErrInvalid
	}
	var revisionID, planHash, releaseChecksum string
	var planPayload []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT wr.id,wr.plan_hash,wr.plan_snapshot,r.checksum
		FROM capabilities c
		JOIN capability_releases r
		  ON r.workspace_id=c.workspace_id AND r.capability_id=c.id
		JOIN workflow_revisions wr
		  ON wr.workspace_id=r.workspace_id AND wr.capability_id=r.capability_id
		 AND wr.id=r.source_id
		WHERE c.workspace_id=$1 AND c.id=$2 AND c.kind='WORKFLOW'
		 AND c.status='ACTIVE' AND c.deleted_at IS NULL
		 AND r.id=$3 AND r.source_type='WORKFLOW_REVISION' AND r.retired_at IS NULL
		 AND wr.status='PUBLISHED' AND wr.retired_at IS NULL
	`, workspaceID, capabilityID, releaseID).Scan(
		&revisionID, &planHash, &planPayload, &releaseChecksum,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return workflowruntime.RevisionSnapshot{}, ErrNotFound
	}
	if err != nil {
		return workflowruntime.RevisionSnapshot{}, fmt.Errorf("resolve workflow revision snapshot: %w", err)
	}
	if releaseChecksum != planHash {
		return workflowruntime.RevisionSnapshot{}, ErrConflict
	}
	var plan domain.CompiledExecutionPlan
	if err := json.Unmarshal(planPayload, &plan); err != nil {
		return workflowruntime.RevisionSnapshot{}, fmt.Errorf("decode workflow revision plan snapshot: %w", err)
	}
	return workflowruntime.RevisionSnapshot{
		WorkspaceID: workspaceID, CapabilityID: capabilityID, ReleaseID: releaseID,
		RevisionID: revisionID, PlanHash: planHash, Plan: plan,
	}, nil
}
