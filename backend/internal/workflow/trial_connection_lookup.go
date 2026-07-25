package workflow

import (
	"context"
	"database/sql"
	"strings"

	"actweave/backend/internal/outboundidentity"
)

// DBTrialConnectionLookup loads dual-mode Connection policy views for trial attach.
type DBTrialConnectionLookup struct {
	db *sql.DB
}

// NewDBTrialConnectionLookup constructs a lookup against PostgreSQL.
func NewDBTrialConnectionLookup(db *sql.DB) (*DBTrialConnectionLookup, error) {
	if db == nil {
		return nil, ErrInvalid
	}
	return &DBTrialConnectionLookup{db: db}, nil
}

// LookupConnections returns policy views for the given Connection IDs.
func (l *DBTrialConnectionLookup) LookupConnections(
	ctx context.Context,
	workspaceID string,
	connectionIDs []string,
) ([]outboundidentity.ConnectionPolicyView, error) {
	if l == nil || l.db == nil {
		return nil, ErrInvalid
	}
	workspaceID = strings.TrimSpace(workspaceID)
	out := make([]outboundidentity.ConnectionPolicyView, 0, len(connectionIDs))
	for _, rawID := range connectionIDs {
		connectionID := strings.TrimSpace(rawID)
		if connectionID == "" {
			continue
		}
		item, err := l.loadOne(ctx, workspaceID, connectionID)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (l *DBTrialConnectionLookup) loadOne(
	ctx context.Context, workspaceID, connectionID string,
) (outboundidentity.ConnectionPolicyView, error) {
	var zero outboundidentity.ConnectionPolicyView
	var status, migration string
	var outboundIdentity []byte
	var connectionPolicy, providerPolicy int64
	var providerID string
	var maxResidence sql.NullInt64
	err := l.db.QueryRowContext(ctx, `
		SELECT c.id, c.provider_id, c.status, c.migration_state,
		       c.outbound_identity, c.outbound_identity_policy_version,
		       p.outbound_identity_policy_version
		FROM service_connections c
		JOIN capability_providers p
		  ON p.workspace_id=c.workspace_id AND p.id=c.provider_id
		WHERE c.workspace_id=$1 AND c.id=$2 AND c.deleted_at IS NULL AND p.deleted_at IS NULL
	`, workspaceID, connectionID).Scan(
		&zero.ConnectionID, &providerID, &status, &migration,
		&outboundIdentity, &connectionPolicy, &providerPolicy,
	)
	if err != nil {
		return zero, err
	}
	zero.ProviderID = providerID
	zero.ConnectionPolicyVersion = connectionPolicy
	zero.ProviderContractVersion = providerPolicy
	zero.Executable = strings.EqualFold(status, "VERIFIED") &&
		strings.EqualFold(migration, outboundidentity.MigrationStateNone)
	if len(outboundIdentity) > 0 && string(outboundIdentity) != "null" {
		identity, parseErr := outboundidentity.ParseConnectionIdentity(outboundIdentity)
		if parseErr != nil {
			return zero, parseErr
		}
		zero.Mode = identity.Mode
		if identity.RequestPassthrough != nil {
			zero.MaxResidenceSeconds = identity.RequestPassthrough.MaxResidenceSeconds
		}
		_ = maxResidence
	}
	return zero, nil
}
