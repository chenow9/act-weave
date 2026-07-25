package aap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/principal"
)

// AgentOutboundLoader resolves dual-mode requirements for an Agent's enabled
// capability bindings (Connection allowlist for REQUEST_PASSTHROUGH attach).
type AgentOutboundLoader interface {
	LoadAgentOutbound(
		ctx context.Context,
		workspaceID, agentID string,
	) (outboundidentity.Requirements, []outboundidentity.ConnectionPolicyView, error)
}

// ConfigureOutbound enables REQUEST_PASSTHROUGH vault attach for createRun.
// bootID must match the process RuntimeCredentialVault / dual-mode injector.
func (service *RunService) ConfigureOutbound(
	attacher *outboundidentity.BindingAttacher,
	loader AgentOutboundLoader,
	bootID string,
) error {
	if service == nil || attacher == nil || loader == nil || strings.TrimSpace(bootID) == "" {
		return ErrRunInvalid
	}
	if service.attacher != nil {
		return ErrRunInvalid
	}
	service.attacher = attacher
	service.outbound = loader
	service.bootID = strings.TrimSpace(bootID)
	return nil
}

// DBAgentOutboundLoader loads Agent binding connections and dual-mode readiness.
type DBAgentOutboundLoader struct {
	db *sql.DB
}

// NewDBAgentOutboundLoader constructs a PostgreSQL-backed agent outbound loader.
func NewDBAgentOutboundLoader(db *sql.DB) (*DBAgentOutboundLoader, error) {
	if db == nil {
		return nil, errors.New("agent outbound loader database is required")
	}
	return &DBAgentOutboundLoader{db: db}, nil
}

// LoadAgentOutbound returns the union of dual-mode requirements for enabled
// agent capability bindings that declare a Connection.
func (l *DBAgentOutboundLoader) LoadAgentOutbound(
	ctx context.Context,
	workspaceID, agentID string,
) (outboundidentity.Requirements, []outboundidentity.ConnectionPolicyView, error) {
	var zeroReq outboundidentity.Requirements
	if l == nil || l.db == nil {
		return zeroReq, nil, ErrRunInvalid
	}
	workspaceID = strings.TrimSpace(workspaceID)
	agentID = strings.TrimSpace(agentID)
	if workspaceID == "" || agentID == "" {
		return zeroReq, nil, ErrRunInvalid
	}
	rows, err := l.db.QueryContext(ctx, `
		SELECT DISTINCT b.connection_id
		FROM agent_capability_bindings b
		WHERE b.workspace_id=$1 AND b.agent_id=$2 AND b.enabled
		  AND b.connection_id IS NOT NULL
		ORDER BY b.connection_id
	`, workspaceID, agentID)
	if err != nil {
		return zeroReq, nil, fmt.Errorf("list agent binding connections: %w", err)
	}
	defer rows.Close()
	connectionIDs := make([]string, 0)
	for rows.Next() {
		var connectionID string
		if err := rows.Scan(&connectionID); err != nil {
			return zeroReq, nil, fmt.Errorf("scan agent binding connection: %w", err)
		}
		connectionID = strings.TrimSpace(connectionID)
		if connectionID != "" {
			connectionIDs = append(connectionIDs, connectionID)
		}
	}
	if err := rows.Err(); err != nil {
		return zeroReq, nil, err
	}
	if len(connectionIDs) == 0 {
		return outboundidentity.Requirements{SchemaVersion: outboundidentity.SchemaRequirements}, nil, nil
	}
	items := make([]outboundidentity.ConnectionReadiness, 0, len(connectionIDs))
	views := make([]outboundidentity.ConnectionPolicyView, 0, len(connectionIDs))
	for _, connectionID := range connectionIDs {
		item, view, loadErr := l.loadConnection(ctx, workspaceID, connectionID)
		if loadErr != nil {
			return zeroReq, nil, loadErr
		}
		items = append(items, item)
		views = append(views, view)
	}
	requirements, err := outboundidentity.BuildRequirementsFromConnections(items, nil)
	if err != nil {
		return zeroReq, nil, err
	}
	return requirements, views, nil
}

func (l *DBAgentOutboundLoader) loadConnection(
	ctx context.Context,
	workspaceID, connectionID string,
) (outboundidentity.ConnectionReadiness, outboundidentity.ConnectionPolicyView, error) {
	var zeroR outboundidentity.ConnectionReadiness
	var zeroV outboundidentity.ConnectionPolicyView
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
		return zeroR, zeroV, outboundidentity.ErrIdentityConnectionNotReady
	}
	if err != nil {
		return zeroR, zeroV, fmt.Errorf("load agent connection readiness: %w", err)
	}
	readiness := outboundidentity.ConnectionReadiness{
		ConnectionID: connectionID, ProviderID: providerID, Status: status,
		MigrationState: migrationState, OutboundIdentity: outboundIdentity,
		ConnectionPolicyVersion: connectionPolicyVersion, ProviderPolicyVersion: providerPolicyVersion,
		ProviderDriverConfig: driverConfig, MachineCredentialActive: machineActive,
	}
	view := outboundidentity.ConnectionPolicyView{
		ConnectionID:            connectionID,
		ProviderID:              providerID,
		ConnectionPolicyVersion: connectionPolicyVersion,
		ProviderContractVersion: providerPolicyVersion,
		Executable: strings.EqualFold(status, "VERIFIED") &&
			strings.EqualFold(migrationState, outboundidentity.MigrationStateNone),
	}
	if len(outboundIdentity) > 0 && string(outboundIdentity) != "null" {
		identity, parseErr := outboundidentity.ParseConnectionIdentity(outboundIdentity)
		if parseErr != nil {
			return zeroR, zeroV, parseErr
		}
		view.Mode = identity.Mode
		if identity.RequestPassthrough != nil {
			view.MaxResidenceSeconds = identity.RequestPassthrough.MaxResidenceSeconds
		}
	}
	return readiness, view, nil
}

func (service *RunService) attachOutboundForRun(
	ctx context.Context,
	input CreateRunInput,
	runID string,
	executionPrincipal principal.ExecutionSnapshot,
	existingRun bool,
) (cleanup func(), err error) {
	noop := func() {}
	hasEnvelope := len(input.OutboundCredentialsRaw) > 0 &&
		string(input.OutboundCredentialsRaw) != "null"

	// No dual-mode wiring: fail closed only when caller sent plaintext.
	if service.attacher == nil || service.outbound == nil {
		if hasEnvelope {
			return noop, outboundidentity.ErrCredentialInvalid
		}
		return noop, nil
	}

	requirements, views, loadErr := service.outbound.LoadAgentOutbound(
		ctx, input.Scope.WorkspaceID, input.Scope.AgentID,
	)
	if loadErr != nil {
		return noop, loadErr
	}
	needsPassthrough := false
	for _, c := range requirements.Connections {
		if c.Mode == outboundidentity.ModeRequestPassthrough {
			needsPassthrough = true
			break
		}
	}
	if !needsPassthrough {
		if hasEnvelope {
			return noop, outboundidentity.ErrCredentialInvalid
		}
		return noop, nil
	}
	if !hasEnvelope {
		return noop, outboundidentity.ErrCredentialRequired
	}

	subjectType, subjectID, subjectErr := outboundSubjectFromPrincipal(executionPrincipal)
	if subjectErr != nil {
		return noop, subjectErr
	}
	bootID := strings.TrimSpace(service.bootID)
	if bootID == "" {
		return noop, outboundidentity.ErrCredentialInvalid
	}

	// Idempotent replay: never re-bind a new Token to an existing Run.
	if existingRun {
		alive := service.rootVaultAlive(bootID, input.Scope.WorkspaceID, subjectType, subjectID, runID)
		_, attachErr := service.attacher.Attach(ctx, outboundidentity.BindingAttachInput{
			RawEnvelope:        input.OutboundCredentialsRaw,
			Requirements:       requirements,
			Connections:        views,
			ExistingRunID:      runID,
			ExistingVaultAlive: alive,
			Context: outboundidentity.BindingAttachContext{
				BootID: bootID, WorkspaceID: input.Scope.WorkspaceID,
				SubjectType: subjectType, SubjectID: subjectID,
				RootScopeType: outboundidentity.RootScopeAgentRun,
				RootScopeID:   runID,
				RootDeadline:  time.Now().UTC().Add(30 * time.Minute),
			},
		})
		return noop, attachErr
	}

	attachResult, attachErr := service.attacher.Attach(ctx, outboundidentity.BindingAttachInput{
		RawEnvelope:  input.OutboundCredentialsRaw,
		Requirements: requirements,
		Connections:  views,
		Context: outboundidentity.BindingAttachContext{
			BootID: bootID, WorkspaceID: input.Scope.WorkspaceID,
			SubjectType: subjectType, SubjectID: subjectID,
			RootScopeType: outboundidentity.RootScopeAgentRun,
			RootScopeID:   runID,
			RootDeadline:  time.Now().UTC().Add(30 * time.Minute),
		},
	})
	if attachErr != nil {
		return noop, attachErr
	}
	affinityClaimed := attachResult.AffinityClaimed
	return func() {
		service.attacher.CleanupRequest(context.WithoutCancel(ctx), outboundidentity.BindingAttachContext{
			BootID: bootID, WorkspaceID: input.Scope.WorkspaceID,
			SubjectType: subjectType, SubjectID: subjectID,
			RootScopeType: outboundidentity.RootScopeAgentRun,
			RootScopeID:   runID,
		}, affinityClaimed)
	}, nil
}

func (service *RunService) rootVaultAlive(
	bootID, workspaceID string,
	subjectType outboundidentity.SubjectType,
	subjectID, runID string,
) bool {
	if service == nil || service.attacher == nil {
		return false
	}
	return service.attacher.VaultHasLiveRoot(outboundidentity.RootScope{
		BootID: bootID, WorkspaceID: workspaceID,
		SubjectType: subjectType, SubjectID: subjectID,
		RootScopeType: outboundidentity.RootScopeAgentRun, RootScopeID: runID,
	})
}

func outboundSubjectFromPrincipal(
	snapshot principal.ExecutionSnapshot,
) (outboundidentity.SubjectType, string, error) {
	if snapshot.Validate() != nil {
		return "", "", outboundidentity.ErrSubjectRequired
	}
	if snapshot.Identity.Subject != nil {
		switch snapshot.Identity.Subject.Type {
		case principal.TypeUser:
			return outboundidentity.SubjectTypeUser, snapshot.Identity.Subject.ID, nil
		case principal.TypeExternalSubject:
			return outboundidentity.SubjectTypeExternalSubject, snapshot.Identity.Subject.ID, nil
		default:
			return "", "", outboundidentity.ErrSubjectRequired
		}
	}
	// Pure client_credentials AAP: Actor is SERVICE_PRINCIPAL, no delegated
	// EXTERNAL_SUBJECT. Isolate Vault by actor UUID so REQUEST_PASSTHROUGH still
	// works for third-party backends (must match subjectFromPrincipal inject).
	if snapshot.Identity.Actor.Type == principal.TypeServicePrincipal {
		return outboundidentity.SubjectTypeExternalSubject, snapshot.Identity.Actor.ID, nil
	}
	return "", "", outboundidentity.ErrSubjectRequired
}
