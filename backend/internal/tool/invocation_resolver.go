package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/capability"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/serviceendpoint"
)

type WorkspaceInvocationAuthorizer struct {
	service *authz.Service
}

func NewWorkspaceInvocationAuthorizer(service *authz.Service) (*WorkspaceInvocationAuthorizer, error) {
	if service == nil {
		return nil, errors.New("workspace authorization service is required")
	}
	return &WorkspaceInvocationAuthorizer{service: service}, nil
}

func (authorizer *WorkspaceInvocationAuthorizer) AuthorizeInvocation(ctx context.Context, actorID, workspaceID string) error {
	_, err := authorizer.service.AuthorizeWorkspace(ctx, actorID, workspaceID, authz.ActionExecute)
	return err
}

type InvocationResolver struct {
	db           *sql.DB
	tools        *Repository
	capabilities *capability.Repository
}

func NewInvocationResolver(db *sql.DB) (*InvocationResolver, error) {
	if db == nil {
		return nil, errors.New("invocation resolver database is required")
	}
	tools, err := NewRepository(db)
	if err != nil {
		return nil, err
	}
	capabilities, err := capability.NewRepository(db)
	if err != nil {
		return nil, err
	}
	return &InvocationResolver{db: db, tools: tools, capabilities: capabilities}, nil
}

func (resolver *InvocationResolver) ResolveInvocation(
	ctx context.Context,
	request execution.ResolveRequest,
) (execution.ResolvedInvocation, error) {
	resolvedRelease, err := resolver.capabilities.Resolve(
		ctx, request.WorkspaceID, request.CapabilityID, request.ReleaseID,
	)
	if err != nil {
		return execution.ResolvedInvocation{}, err
	}
	switch {
	case resolvedRelease.Kind == "TOOL" && resolvedRelease.SourceType == "TOOL_VERSION":
		return resolver.resolveToolInvocation(ctx, request, resolvedRelease)
	case resolvedRelease.Kind == "WORKFLOW" && resolvedRelease.SourceType == "WORKFLOW_REVISION":
		return resolver.resolveWorkflowInvocation(ctx, request, resolvedRelease)
	default:
		return execution.ResolvedInvocation{}, execution.NewError(execution.ErrorCodeResolve, "RESOLUTION", false, 0, nil)
	}
}

func (resolver *InvocationResolver) resolveToolInvocation(
	ctx context.Context,
	request execution.ResolveRequest,
	resolvedRelease capability.ResolvedCapability,
) (execution.ResolvedInvocation, error) {
	version, err := resolver.tools.GetVersion(ctx, request.WorkspaceID, request.CapabilityID, resolvedRelease.SourceID)
	if err != nil {
		return execution.ResolvedInvocation{}, err
	}
	if version.LifecycleStatus != "PUBLISHED" || version.Checksum != resolvedRelease.Checksum {
		return execution.ResolvedInvocation{}, execution.NewError(execution.ErrorCodeResolve, "RESOLUTION", false, 0, nil)
	}
	// Prefer request/plan/binding/version connection, then tool-level default.
	// OpenAPI generate without connectionId leaves version.default_connection_id NULL
	// while tools.default_connection_id is often set later for trial/workflow invoke.
	toolDefaultConnectionID := ""
	if toolMeta, toolErr := resolver.tools.Get(ctx, request.WorkspaceID, request.CapabilityID); toolErr == nil {
		toolDefaultConnectionID = optionalString(toolMeta.DefaultConnectionID)
	}
	connectionID := firstConnectionID(
		request.ExplicitConnectionID, request.PlanConnectionID,
		request.BindingConnectionID, optionalString(version.DefaultConnectionID),
		toolDefaultConnectionID,
	)
	if connectionID == "" {
		return execution.ResolvedInvocation{}, execution.NewError(execution.ErrorCodeResolve, "RESOLUTION", false, 0, nil)
	}
	connection, credential, err := resolver.resolveHTTPConnection(
		ctx, request.WorkspaceID, version.ProviderID, connectionID,
	)
	if err != nil {
		return execution.ResolvedInvocation{}, err
	}
	runtimePolicy, action := invocationPolicies(version.RuntimePolicy, version.ActionConfig)
	return execution.ResolvedInvocation{
		Snapshot: execution.ReleaseSnapshot{
			ReleaseID: resolvedRelease.ReleaseID, WorkspaceID: request.WorkspaceID,
			CapabilityID: request.CapabilityID, ToolVersionID: version.ID,
			ExecutorType: version.ExecutorType, ProviderID: version.ProviderID,
			ActionSchemaVersion: version.ActionSchemaVersion, ActionConfig: cloneRaw(version.ActionConfig),
			InputSchema: cloneRaw(resolvedRelease.InputSchema), OutputSchema: cloneRaw(resolvedRelease.OutputSchema),
			ErrorMappings: cloneRaw(version.ErrorMappings), RuntimePolicy: cloneRaw(version.RuntimePolicy),
			Checksum: resolvedRelease.Checksum,
		},
		Connection: connection, Credential: credential,
		RiskLevel: resolvedRelease.RiskLevel, SideEffectLevel: resolvedRelease.SideEffectLevel,
		RequiresConfirmation:   resolvedRelease.RequiresConfirmation,
		Idempotent:             explicitlyIdempotentHTTP(action.Method, runtimePolicy.Idempotent),
		SupportsIdempotencyKey: supportsIdempotencyKey(runtimePolicy.IdempotencyPolicy),
		RetryCount:             runtimePolicy.RetryCount,
	}, nil
}

// resolveWorkflowInvocation pins a published WORKFLOW_REVISION for agent-tool use.
// Non-HTTP WORKFLOW executors bypass outbound identity entirely — they must not
// synthesize AuthMode=NONE as an HTTP identity scheme.
func (resolver *InvocationResolver) resolveWorkflowInvocation(
	ctx context.Context,
	request execution.ResolveRequest,
	resolvedRelease capability.ResolvedCapability,
) (execution.ResolvedInvocation, error) {
	var revisionID, planHash, revisionStatus string
	err := resolver.db.QueryRowContext(ctx, `
		SELECT wr.id,wr.plan_hash,wr.status
		FROM workflow_revisions wr
		WHERE wr.workspace_id=$1 AND wr.capability_id=$2 AND wr.id=$3
		  AND wr.retired_at IS NULL
	`, request.WorkspaceID, request.CapabilityID, resolvedRelease.SourceID).Scan(
		&revisionID, &planHash, &revisionStatus,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.ResolvedInvocation{}, execution.NewError(execution.ErrorCodeResolve, "RESOLUTION", false, 0, nil)
	}
	if err != nil {
		return execution.ResolvedInvocation{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(revisionStatus), "PUBLISHED") ||
		strings.TrimSpace(planHash) == "" || planHash != resolvedRelease.Checksum {
		return execution.ResolvedInvocation{}, execution.NewError(execution.ErrorCodeResolve, "RESOLUTION", false, 0, nil)
	}
	actionConfig, _ := json.Marshal(map[string]any{
		"kind": "WORKFLOW", "revisionId": revisionID, "planHash": planHash,
	})
	// Explicit non-HTTP bypass: no dual-mode identity, no legacy NONE identity.
	return execution.ResolvedInvocation{
		Snapshot: execution.ReleaseSnapshot{
			ReleaseID: resolvedRelease.ReleaseID, WorkspaceID: request.WorkspaceID,
			CapabilityID: request.CapabilityID, ToolVersionID: revisionID,
			ExecutorType: execution.ExecutorTypeWORKFLOW, ProviderID: "",
			ActionSchemaVersion: "workflow.v1", ActionConfig: actionConfig,
			InputSchema: cloneRaw(resolvedRelease.InputSchema), OutputSchema: cloneRaw(resolvedRelease.OutputSchema),
			ErrorMappings: json.RawMessage(`{}`), RuntimePolicy: json.RawMessage(`{}`),
			Checksum: resolvedRelease.Checksum,
		},
		Connection: execution.ConnectionSnapshot{
			WorkspaceID: request.WorkspaceID, Environment: "PRODUCTION",
		},
		Credential: execution.CredentialReference{
			WorkspaceID:            request.WorkspaceID,
			BypassOutboundIdentity: true,
			AuthConfig:             json.RawMessage(`{}`),
		},
		RiskLevel: resolvedRelease.RiskLevel, SideEffectLevel: resolvedRelease.SideEffectLevel,
		RequiresConfirmation:   resolvedRelease.RequiresConfirmation,
		Idempotent:             false,
		SupportsIdempotencyKey: false,
		RetryCount:             0,
	}, nil
}

func (resolver *InvocationResolver) ResolveTestConnection(
	ctx context.Context, workspaceID, capabilityID, versionID, connectionID string,
) (execution.ConnectionSnapshot, execution.CredentialReference, error) {
	version, err := resolver.tools.GetVersion(ctx, workspaceID, capabilityID, versionID)
	if err != nil {
		return execution.ConnectionSnapshot{}, execution.CredentialReference{}, err
	}
	if version.LifecycleStatus == "PUBLISHED" || strings.TrimSpace(connectionID) == "" {
		return execution.ConnectionSnapshot{}, execution.CredentialReference{}, ErrInvalid
	}
	return resolver.resolveHTTPConnection(ctx, workspaceID, version.ProviderID, connectionID)
}

func (resolver *InvocationResolver) resolveHTTPConnection(
	ctx context.Context,
	workspaceID, providerID, connectionID string,
) (execution.ConnectionSnapshot, execution.CredentialReference, error) {
	var value execution.ConnectionSnapshot
	var policy, endpointConfig, driverConfig, outboundIdentity []byte
	var machineSecretID *string
	var machineActive bool
	var status, providerStatus, migrationState string
	var connectionPolicyVersion, providerPolicyVersion int64
	err := resolver.db.QueryRowContext(ctx, `
		SELECT c.id,c.workspace_id,c.provider_id,c.environment,c.policy,c.status,
			c.outbound_identity,c.outbound_identity_policy_version,c.migration_state,
			c.machine_credential_secret_id,
			p.status,p.endpoint_config,p.driver_config,p.outbound_identity_policy_version,
			(mv.id IS NOT NULL AND mv.revoked_at IS NULL)
		FROM service_connections c
		JOIN capability_providers p
		  ON p.workspace_id=c.workspace_id AND p.id=c.provider_id
		LEFT JOIN secrets ms
		  ON ms.workspace_id=c.workspace_id AND ms.id=c.machine_credential_secret_id
		LEFT JOIN secret_versions mv
		  ON mv.workspace_id=ms.workspace_id AND mv.secret_id=ms.id
		 AND mv.id=ms.active_version_id AND mv.revoked_at IS NULL
		WHERE c.workspace_id=$1 AND c.provider_id=$2 AND c.id=$3
		  AND c.deleted_at IS NULL AND p.deleted_at IS NULL
	`, workspaceID, providerID, connectionID).Scan(
		&value.ID, &value.WorkspaceID, &value.ProviderID, &value.Environment, &policy, &status,
		&outboundIdentity, &connectionPolicyVersion, &migrationState, &machineSecretID,
		&providerStatus, &endpointConfig, &driverConfig, &providerPolicyVersion, &machineActive,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return value, execution.CredentialReference{}, ErrNotFound
	}
	if err != nil {
		return value, execution.CredentialReference{}, err
	}
	if providerStatus != "ACTIVE" {
		return value, execution.CredentialReference{}, execution.NewError(execution.ErrorCodeResolve, "RESOLUTION", false, 0, nil)
	}
	readiness := outboundidentity.ConnectionReadiness{
		ConnectionID:            connectionID,
		ProviderID:              providerID,
		Status:                  status,
		MigrationState:          migrationState,
		OutboundIdentity:        append(json.RawMessage(nil), outboundIdentity...),
		ConnectionPolicyVersion: connectionPolicyVersion,
		ProviderPolicyVersion:   providerPolicyVersion,
		ProviderDriverConfig:    append(json.RawMessage(nil), driverConfig...),
		MachineCredentialActive: machineActive,
	}
	if err := outboundidentity.AssessConnectionReadiness(readiness); err != nil {
		return value, execution.CredentialReference{}, err
	}
	identity, err := outboundidentity.ParseConnectionIdentity(readiness.OutboundIdentity)
	if err != nil {
		return value, execution.CredentialReference{}, err
	}
	identity.PolicyVersion = connectionPolicyVersion
	requirements, err := outboundidentity.BuildRequirementsFromConnections([]outboundidentity.ConnectionReadiness{readiness}, nil)
	if err != nil {
		return value, execution.CredentialReference{}, err
	}
	requirementsJSON, err := outboundidentity.RequirementsJSON(requirements)
	if err != nil {
		return value, execution.CredentialReference{}, err
	}
	endpoint, endpointErr := serviceendpoint.Parse(endpointConfig)
	if endpointErr != nil {
		return value, execution.CredentialReference{}, execution.NewError(execution.ErrorCodeResolve, "RESOLUTION", false, 0, nil)
	}
	value.BaseURL = strings.TrimSpace(endpoint.ServiceBaseURL)
	if value.BaseURL == "" {
		return value, execution.CredentialReference{}, execution.NewError(execution.ErrorCodeResolve, "RESOLUTION", false, 0, nil)
	}
	var endpointExtras struct {
		Headers map[string]string      `json:"headers"`
		Egress  execution.EgressPolicy `json:"egress"`
	}
	_ = json.Unmarshal(endpointConfig, &endpointExtras)
	value.Headers = make(map[string]string, len(endpointExtras.Headers))
	for name, headerValue := range endpointExtras.Headers {
		value.Headers[name] = headerValue
	}
	var connectionPolicy struct {
		Egress                   execution.EgressPolicy `json:"egress"`
		AllowedCredentialHeaders []string               `json:"allowedCredentialHeaders"`
	}
	if json.Unmarshal(policy, &connectionPolicy) != nil {
		return value, execution.CredentialReference{}, execution.NewError(execution.ErrorCodeResolve, "RESOLUTION", false, 0, nil)
	}
	value.EgressPolicy = endpointExtras.Egress
	if invocationEgressConfigured(connectionPolicy.Egress) {
		value.EgressPolicy = connectionPolicy.Egress
	}
	// Dual-mode only: never attach legacy business credential_secret_id.
	// AuthConfig carries non-secret Connection broker selection (clientId/scopes/TTL)
	// for injection — never Secret IDs or Token values.
	authConfig := json.RawMessage(`{}`)
	if identity.Mode == outboundidentity.ModeBrokerOBO && identity.BrokerOBO != nil {
		encoded, encErr := json.Marshal(map[string]any{
			"clientId":           identity.BrokerOBO.ClientID,
			"scopes":             identity.BrokerOBO.Scopes,
			"maxTokenTtlSeconds": identity.BrokerOBO.MaxTokenTTLSeconds,
		})
		if encErr == nil {
			authConfig = encoded
		}
	}
	credential := execution.CredentialReference{
		WorkspaceID:              workspaceID,
		AuthMode:                 string(identity.Mode),
		OutboundMode:             string(identity.Mode),
		OutboundRequirements:     requirementsJSON,
		ProviderDriverConfig:     append(json.RawMessage(nil), driverConfig...),
		AuthConfig:               authConfig,
		AllowedCredentialHeaders: append([]string(nil), connectionPolicy.AllowedCredentialHeaders...),
	}
	return value, credential, nil
}

func invocationEgressConfigured(policy execution.EgressPolicy) bool {
	return len(policy.AllowedHosts) > 0 || len(policy.AllowedPorts) > 0 ||
		len(policy.AllowedCIDRs) > 0 || policy.MaxRedirects != 0
}

type invocationRuntimePolicy struct {
	RetryCount        int    `json:"retryCount"`
	Idempotent        bool   `json:"idempotent"`
	IdempotencyPolicy string `json:"idempotencyPolicy"`
}

type invocationAction struct {
	Method string `json:"method"`
}

func invocationPolicies(runtimeJSON, actionJSON json.RawMessage) (invocationRuntimePolicy, invocationAction) {
	var runtime invocationRuntimePolicy
	var action invocationAction
	_ = json.Unmarshal(runtimeJSON, &runtime)
	_ = json.Unmarshal(actionJSON, &action)
	action.Method = strings.ToUpper(strings.TrimSpace(action.Method))
	return runtime, action
}

func explicitlyIdempotentHTTP(method string, configured bool) bool {
	if configured {
		return true
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
}

func supportsIdempotencyKey(policy string) bool {
	switch strings.ToUpper(strings.TrimSpace(policy)) {
	case "IDEMPOTENCY_KEY", "HEADER", "REQUIRED":
		return true
	default:
		return false
	}
}

func firstConnectionID(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
