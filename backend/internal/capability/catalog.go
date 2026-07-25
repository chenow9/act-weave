package capability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"actweave/backend/internal/outboundidentity"
)

type BindingReader interface {
	ListEnabledSelections(context.Context, string, string) ([]BindingSelection, error)
}

type Catalog struct {
	repository *Repository
	bindings   BindingReader
	db         *sql.DB
}

func NewCatalog(repository *Repository, bindings BindingReader) (*Catalog, error) {
	if repository == nil || bindings == nil {
		return nil, errors.New("capability catalog repository and binding reader are required")
	}
	return &Catalog{repository: repository, bindings: bindings}, nil
}

// WithDB enables dual-mode outbound requirements enrichment on agent snapshots.
func (c *Catalog) WithDB(db *sql.DB) *Catalog {
	if c != nil {
		c.db = db
	}
	return c
}

func (c *Catalog) Resolve(ctx context.Context, workspaceID, capabilityID, releaseID string) (ResolvedCapability, error) {
	return c.repository.Resolve(ctx, workspaceID, capabilityID, releaseID)
}

func (c *Catalog) ListForAgent(ctx context.Context, workspaceID, agentID string) ([]Descriptor, error) {
	selections, err := c.bindings.ListEnabledSelections(ctx, workspaceID, agentID)
	if err != nil {
		return nil, fmt.Errorf("list enabled capability bindings: %w", err)
	}
	descriptors := make([]Descriptor, 0, len(selections))
	for _, selection := range selections {
		releaseID := ""
		switch selection.VersionPolicy {
		case "FOLLOW_ACTIVE":
		case "PINNED":
			if selection.PinnedReleaseID == nil {
				return nil, ErrInvalid
			}
			releaseID = *selection.PinnedReleaseID
		default:
			return nil, ErrInvalid
		}
		resolved, err := c.repository.Resolve(ctx, workspaceID, selection.CapabilityID, releaseID)
		if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		descriptor := resolved.Descriptor
		if selection.ConnectionID != nil {
			descriptor.ConnectionID = strings.TrimSpace(*selection.ConnectionID)
			if c.db != nil && resolved.Kind == "TOOL" {
				requirements, reqErr := loadOutboundRequirements(ctx, c.db, workspaceID, descriptor.ConnectionID)
				if reqErr != nil {
					return nil, reqErr
				}
				descriptor.OutboundRequirements = requirements
			}
		}
		descriptors = append(descriptors, descriptor)
	}
	sort.Slice(descriptors, func(i, j int) bool {
		leftName, rightName := strings.ToLower(descriptors[i].CallableName), strings.ToLower(descriptors[j].CallableName)
		if leftName == rightName {
			return descriptors[i].ReleaseID < descriptors[j].ReleaseID
		}
		return leftName < rightName
	})
	for index := 1; index < len(descriptors); index++ {
		if strings.EqualFold(descriptors[index-1].CallableName, descriptors[index].CallableName) {
			return nil, ErrCallableConflict
		}
	}
	return descriptors, nil
}

func loadOutboundRequirements(ctx context.Context, db *sql.DB, workspaceID, connectionID string) (json.RawMessage, error) {
	var providerID, status, migrationState string
	var outboundIdentity, driverConfig []byte
	var connectionPolicyVersion, providerPolicyVersion int64
	var machineActive bool
	err := db.QueryRowContext(ctx, `
		SELECT c.provider_id,c.status,c.migration_state,c.outbound_identity,
			c.outbound_identity_policy_version,p.driver_config,p.outbound_identity_policy_version,
			(mv.id IS NOT NULL AND mv.revoked_at IS NULL)
		FROM service_connections c
		JOIN capability_providers p ON p.workspace_id=c.workspace_id AND p.id=c.provider_id
		LEFT JOIN secrets ms ON ms.workspace_id=c.workspace_id AND ms.id=c.machine_credential_secret_id
		LEFT JOIN secret_versions mv ON mv.workspace_id=ms.workspace_id AND mv.secret_id=ms.id
		 AND mv.id=ms.active_version_id AND mv.revoked_at IS NULL
		WHERE c.workspace_id=$1 AND c.id=$2 AND c.deleted_at IS NULL AND p.deleted_at IS NULL
	`, workspaceID, connectionID).Scan(
		&providerID, &status, &migrationState, &outboundIdentity, &connectionPolicyVersion,
		&driverConfig, &providerPolicyVersion, &machineActive,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	readiness := outboundidentity.ConnectionReadiness{
		ConnectionID: connectionID, ProviderID: providerID, Status: status,
		MigrationState: migrationState, OutboundIdentity: outboundIdentity,
		ConnectionPolicyVersion: connectionPolicyVersion, ProviderPolicyVersion: providerPolicyVersion,
		ProviderDriverConfig: driverConfig, MachineCredentialActive: machineActive,
	}
	requirements, err := outboundidentity.BuildRequirementsFromConnections(
		[]outboundidentity.ConnectionReadiness{readiness}, nil,
	)
	if err != nil {
		return nil, err
	}
	return outboundidentity.RequirementsJSON(requirements)
}
