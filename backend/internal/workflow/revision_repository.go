package workflow

import (
	"bytes"
	"context"
	"fmt"
)

func (r *Repository) ListRevisions(
	ctx context.Context,
	workspaceID, capabilityID string,
) ([]Revision, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+revisionColumns+`
		FROM workflow_revisions wr
		JOIN capabilities c
		  ON c.workspace_id=wr.workspace_id AND c.id=wr.capability_id
		WHERE wr.workspace_id=$1 AND wr.capability_id=$2
		 AND c.kind='WORKFLOW' AND c.deleted_at IS NULL
		ORDER BY wr.revision_no DESC,wr.id DESC
	`, workspaceID, capabilityID)
	if err != nil {
		return nil, fmt.Errorf("list workflow revisions: %w", err)
	}
	defer rows.Close()
	values := make([]Revision, 0)
	for rows.Next() {
		value, err := scanRevision(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workflow revision: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflow revisions: %w", err)
	}
	return values, nil
}

func (r *Repository) GetRevision(
	ctx context.Context,
	workspaceID, capabilityID, revisionID string,
) (Revision, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(revisionID) {
		return Revision{}, ErrInvalid
	}
	value, err := scanRevision(r.db.QueryRowContext(ctx, `
		SELECT `+revisionColumns+`
		FROM workflow_revisions wr
		JOIN capabilities c
		  ON c.workspace_id=wr.workspace_id AND c.id=wr.capability_id
		WHERE wr.workspace_id=$1 AND wr.capability_id=$2 AND wr.id=$3
		 AND c.kind='WORKFLOW' AND c.deleted_at IS NULL
	`, workspaceID, capabilityID, revisionID))
	return value, mapRead("get workflow revision", err)
}

func (r *Repository) DiffRevisions(
	ctx context.Context,
	workspaceID, capabilityID, fromRevisionID, toRevisionID string,
) (RevisionDiff, error) {
	if !validUUID(fromRevisionID) || !validUUID(toRevisionID) {
		return RevisionDiff{}, ErrInvalid
	}
	from, err := r.GetRevision(ctx, workspaceID, capabilityID, fromRevisionID)
	if err != nil {
		return RevisionDiff{}, err
	}
	to, err := r.GetRevision(ctx, workspaceID, capabilityID, toRevisionID)
	if err != nil {
		return RevisionDiff{}, err
	}
	return RevisionDiff{
		From:            from,
		To:              to,
		DraftChanged:    !bytes.Equal(from.DraftSnapshot, to.DraftSnapshot),
		SpecChanged:     !bytes.Equal(from.SpecSnapshot, to.SpecSnapshot),
		PlanChanged:     !bytes.Equal(from.PlanSnapshot, to.PlanSnapshot),
		PlanHashChanged: from.PlanHash != to.PlanHash,
	}, nil
}
