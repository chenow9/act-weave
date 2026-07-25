package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/outboundidentity"
)

// OutboundRequirementsLoader resolves dual-mode connection readiness for Tool
// nodes during compilation / publish drift checks.
type OutboundRequirementsLoader struct {
	db *sql.DB
}

func NewOutboundRequirementsLoader(db *sql.DB) (*OutboundRequirementsLoader, error) {
	if db == nil {
		return nil, errors.New("outbound requirements loader database is required")
	}
	return &OutboundRequirementsLoader{db: db}, nil
}

// EnrichPlan walks compiled plan Tool nodes, loads Connection dual-mode state,
// and attaches outbound-requirements.v1. Fail-closed on migration / unready /
// legacy / missing connections.
func (l *OutboundRequirementsLoader) EnrichPlan(
	ctx context.Context,
	workspaceID string,
	plan *domain.CompiledExecutionPlan,
) error {
	if l == nil || plan == nil {
		return nil
	}
	connectionIDs := collectToolConnectionIDs(*plan)
	if len(connectionIDs) == 0 {
		// No HTTP Tool connections: leave outboundRequirements empty (non-HTTP).
		plan.OutboundRequirements = nil
		return nil
	}
	items := make([]outboundidentity.ConnectionReadiness, 0, len(connectionIDs))
	for _, connectionID := range connectionIDs {
		item, err := l.loadConnection(ctx, workspaceID, connectionID)
		if err != nil {
			return err
		}
		items = append(items, item)
	}
	requirements, err := outboundidentity.BuildRequirementsFromConnections(items, nil)
	if err != nil {
		return err
	}
	// Store as typed map for stable JSON via domain plan marshal.
	encoded, err := outboundidentity.RequirementsJSON(requirements)
	if err != nil {
		return err
	}
	var asMap map[string]any
	if err := json.Unmarshal(encoded, &asMap); err != nil {
		return err
	}
	plan.OutboundRequirements = asMap
	return nil
}

// ValidatePublishedRequirements re-checks a stored plan descriptor against live
// Provider/Connection policy versions (publish / production gate).
func (l *OutboundRequirementsLoader) ValidatePublishedRequirements(
	ctx context.Context,
	workspaceID string,
	planJSON json.RawMessage,
) error {
	if l == nil || len(planJSON) == 0 {
		return nil
	}
	var plan domain.CompiledExecutionPlan
	if json.Unmarshal(planJSON, &plan) != nil {
		return ErrInvalid
	}
	raw, err := json.Marshal(plan.OutboundRequirements)
	if err != nil || string(raw) == "null" || len(raw) == 0 {
		// No requirements in plan: only valid if plan has no tool connections.
		if len(collectToolConnectionIDs(plan)) == 0 {
			return nil
		}
		return outboundidentity.ErrIdentityPolicyChanged
	}
	snapshot, err := outboundidentity.ParseRequirements(raw)
	if err != nil {
		return err
	}
	items := make([]outboundidentity.ConnectionReadiness, 0, len(snapshot.Connections))
	for _, conn := range snapshot.Connections {
		item, err := l.loadConnection(ctx, workspaceID, conn.ConnectionID)
		if err != nil {
			return err
		}
		items = append(items, item)
	}
	return outboundidentity.DetectPolicyDrift(snapshot, items)
}

func collectToolConnectionIDs(plan domain.CompiledExecutionPlan) []string {
	seen := map[string]struct{}{}
	var ids []string
	for _, node := range plan.Nodes {
		if !strings.EqualFold(node.Type, "Tool") {
			continue
		}
		connectionID := stringFromConfig(node.Config, "connectionId")
		if connectionID == "" {
			connectionID = stringFromConfig(node.Config, "defaultConnectionId")
		}
		if connectionID == "" {
			continue
		}
		if _, ok := seen[connectionID]; ok {
			continue
		}
		seen[connectionID] = struct{}{}
		ids = append(ids, connectionID)
	}
	return ids
}

func stringFromConfig(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	raw, ok := config[key]
	if !ok || raw == nil {
		return ""
	}
	text, _ := raw.(string)
	return strings.TrimSpace(text)
}

func (l *OutboundRequirementsLoader) loadConnection(
	ctx context.Context,
	workspaceID, connectionID string,
) (outboundidentity.ConnectionReadiness, error) {
	var providerID, status, migrationState string
	var outboundIdentity, driverConfig []byte
	var connectionPolicyVersion, providerPolicyVersion int64
	var machineActive bool
	err := l.db.QueryRowContext(ctx, `
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
		return outboundidentity.ConnectionReadiness{}, ErrNotFound
	}
	if err != nil {
		return outboundidentity.ConnectionReadiness{}, fmt.Errorf("load connection readiness: %w", err)
	}
	return outboundidentity.ConnectionReadiness{
		ConnectionID: connectionID, ProviderID: providerID, Status: status,
		MigrationState: migrationState, OutboundIdentity: outboundIdentity,
		ConnectionPolicyVersion: connectionPolicyVersion, ProviderPolicyVersion: providerPolicyVersion,
		ProviderDriverConfig: driverConfig, MachineCredentialActive: machineActive,
	}, nil
}
